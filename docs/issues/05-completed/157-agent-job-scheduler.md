# 157 — Agent Job Scheduler

> **Priority:** P1
> **Status:** Ready
> **Depends on:** #150 (Conversation Schema Redesign)
> **Author:** Josh S

## Summary

Otter Camp needs its own job scheduling system so agents can run recurring tasks, reminders, and background work without depending on OpenClaw's cron infrastructure. Jobs are defined in the database, managed via CLI and API, and executed by a scheduler worker running inside the Otter Camp server process.

## Why

Currently, agent scheduled tasks (heartbeats, monitors, periodic checks) rely on OpenClaw's cron system. When agents move to Otter Camp, they need equivalent scheduling that:
- Lives in our database (portable, inspectable, manageable)
- Survives server restarts (persistent, not in-memory)
- Supports the same patterns agents use today (recurring intervals, cron expressions, one-shot timers)
- Is visible to users (what's running, when did it last fire, did it fail?)
- Can be managed conversationally ("run this every 10 minutes") or via CLI/API

## Schema

### Migration: `CREATE TABLE agent_jobs`

```sql
CREATE TABLE agent_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    
    -- Definition
    name TEXT NOT NULL,                    -- human-readable: "Plex Quality Monitor", "Email Check"
    description TEXT,                      -- optional context for what/why
    
    -- Schedule
    schedule_kind TEXT NOT NULL CHECK (schedule_kind IN ('cron', 'interval', 'once')),
    cron_expr TEXT,                        -- cron: "*/10 * * * *" (every 10 min)
    interval_ms BIGINT,                   -- interval: milliseconds between runs
    run_at TIMESTAMPTZ,                   -- once: specific time to fire
    timezone TEXT DEFAULT 'UTC',          -- for cron evaluation
    
    -- Payload
    payload_kind TEXT NOT NULL CHECK (payload_kind IN ('message', 'system_event')),
    payload_text TEXT NOT NULL,           -- the message/prompt to inject
    
    -- Targeting
    room_id UUID REFERENCES rooms(id),   -- which room to inject into (optional — creates new if null)
    
    -- State
    enabled BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'completed', 'failed')),
    last_run_at TIMESTAMPTZ,
    last_run_status TEXT CHECK (last_run_status IN ('success', 'error', 'timeout', 'skipped')),
    last_run_error TEXT,
    next_run_at TIMESTAMPTZ,             -- precomputed for efficient polling
    run_count INT NOT NULL DEFAULT 0,
    error_count INT NOT NULL DEFAULT 0,
    max_failures INT DEFAULT 5,          -- auto-pause after N consecutive failures
    consecutive_failures INT NOT NULL DEFAULT 0,
    
    -- Metadata
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by UUID,                      -- user or agent who created the job
    
    CONSTRAINT valid_cron CHECK (schedule_kind != 'cron' OR cron_expr IS NOT NULL),
    CONSTRAINT valid_interval CHECK (schedule_kind != 'interval' OR interval_ms IS NOT NULL),
    CONSTRAINT valid_once CHECK (schedule_kind != 'once' OR run_at IS NOT NULL)
);

-- Indexes
CREATE INDEX idx_agent_jobs_next_run ON agent_jobs (next_run_at) WHERE enabled = true AND status = 'active';
CREATE INDEX idx_agent_jobs_org ON agent_jobs (org_id);
CREATE INDEX idx_agent_jobs_agent ON agent_jobs (agent_id);

-- RLS
ALTER TABLE agent_jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY agent_jobs_org_isolation ON agent_jobs
    USING (org_id = current_setting('app.current_org_id')::UUID);
```

### Migration: `CREATE TABLE agent_job_runs`

```sql
CREATE TABLE agent_job_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    
    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'error', 'timeout', 'skipped')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    duration_ms INT,
    error TEXT,
    
    -- What was injected
    payload_text TEXT NOT NULL,
    message_id UUID REFERENCES chat_messages(id),  -- the message that was created
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_job_runs_job ON agent_job_runs (job_id, started_at DESC);
CREATE INDEX idx_agent_job_runs_org ON agent_job_runs (org_id);

-- RLS
ALTER TABLE agent_job_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY agent_job_runs_org_isolation ON agent_job_runs
    USING (org_id = current_setting('app.current_org_id')::UUID);

-- Auto-prune: keep last 100 runs per job (cleanup worker or ON INSERT trigger)
```

## Scheduler Worker

A single goroutine that polls for due jobs and executes them.

### Core Loop

```go
func (w *JobSchedulerWorker) Run(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)  // poll interval
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            w.processDueJobs(ctx)
        }
    }
}

func (w *JobSchedulerWorker) processDueJobs(ctx context.Context) {
    // SELECT * FROM agent_jobs 
    // WHERE enabled = true AND status = 'active' AND next_run_at <= NOW()
    // ORDER BY next_run_at ASC
    // FOR UPDATE SKIP LOCKED  -- safe for future multi-instance
    // LIMIT 50
    
    for _, job := range dueJobs {
        w.executeJob(ctx, job)
    }
}
```

### Job Execution

```
1. Create agent_job_runs row (status: 'running')
2. Inject payload_text as a message into the target room
   - payload_kind: 'message' → user-type message (triggers agent response)
   - payload_kind: 'system_event' → system-type message (context injection, no response expected)
3. Update agent_job_runs (status: 'success', duration, message_id)
4. Update agent_jobs (last_run_at, last_run_status, next_run_at, run_count, reset consecutive_failures)
5. On error: update with error, increment consecutive_failures
6. If consecutive_failures >= max_failures → set status = 'paused', log warning
```

### next_run_at Computation

| Kind | Logic |
|------|-------|
| `cron` | Parse cron_expr with timezone, compute next fire time after NOW() |
| `interval` | `last_run_at + interval_ms` (or `NOW() + interval_ms` if first run) |
| `once` | `run_at` (set status = 'completed' after execution) |

### Concurrency Safety

- `FOR UPDATE SKIP LOCKED` on the poll query — safe if we ever run multiple server instances
- One job executes at a time per worker (no parallel execution needed — message injection is fast)
- If a job is still "running" from a crashed server, the cleanup pass marks it as `error` after a timeout (default: 5 minutes)

## API Endpoints

```
POST   /api/v1/jobs                    — Create a job
GET    /api/v1/jobs                    — List jobs (with filters)
GET    /api/v1/jobs/:id                — Get job details
PATCH  /api/v1/jobs/:id                — Update job (schedule, payload, enabled)
DELETE /api/v1/jobs/:id                — Delete job
POST   /api/v1/jobs/:id/run            — Trigger immediately (regardless of schedule)
GET    /api/v1/jobs/:id/runs           — Get run history
POST   /api/v1/jobs/:id/pause          — Pause job
POST   /api/v1/jobs/:id/resume         — Resume job
```

## CLI

```bash
# List all jobs
otter jobs list
otter jobs list --agent frank

# Create a job
otter jobs create --agent frank --name "Email Check" \
  --schedule "*/30 * * * *" \
  --payload "Check for new important emails and summarize any that need attention"

# Interval-based
otter jobs create --agent frank --name "Heartbeat" \
  --every 30s \
  --payload "Heartbeat check — report only if something needs attention"

# One-shot reminder
otter jobs create --agent frank --name "Dinner Reminder" \
  --at "2026-02-12T18:00:00-07:00" \
  --payload "Remind Sam about dinner reservation at 6:30"

# Manage
otter jobs pause <id>
otter jobs resume <id>
otter jobs run <id>          # trigger now
otter jobs history <id>      # show recent runs
otter jobs delete <id>

# Status overview
otter jobs status
```

### Example Output

```
$ otter jobs list

Jobs (7 active, 1 paused):
  ✅ Email Check          frank     */30 * * * *    Last: 2m ago (success)
  ✅ Plex Quality Monitor max       0 */6 * * *     Last: 3h ago (success)  
  ✅ Heartbeat            frank     every 30s       Last: 12s ago (success)
  ✅ Market Summary       beau-h    0 9 * * 1-5     Last: 6h ago (success)
  ✅ Social Monitor       nova      */15 * * * *    Last: 8m ago (success)
  ✅ Health Check         max       0 8 * * *       Last: 14h ago (success)
  ✅ Issue Monitor        josh-s    every 10m       Last: 4m ago (success)
  ⏸️  Deploy Watch        josh-s    every 5m        Paused (5 consecutive failures)
```

## Conversational Management

Agents can create and manage their own jobs. When an agent says "I'll check on this every 10 minutes," it should be able to create a job via the API. This requires:

1. **Agent API access** — agents can call job endpoints with their own agent_id
2. **Self-scoping** — agents can only create/manage jobs for themselves
3. **Natural language** — Ellie or Frank can interpret "remind me in 2 hours" → create a one-shot job

### Agent-Created Job Flow

```
User: "Can you check the deployment status every 10 minutes?"
Agent: (calls POST /api/v1/jobs with interval_ms=600000)
Agent: "Done — I'll check every 10 minutes and let you know if anything changes."
```

## Migration from OpenClaw

During the OpenClaw migration (#155), existing cron jobs should be imported:

1. Read OpenClaw's cron configuration (from `openclaw.json` or cron state)
2. Map each cron job to an `agent_jobs` row
3. Set `enabled = false` initially (user reviews and activates)
4. Log: "Imported N scheduled jobs from OpenClaw (disabled — review with `otter jobs list --disabled`)"

**Note:** OpenClaw cron jobs reference OpenClaw session concepts. The payload text is portable, but targeting (which room/agent) needs mapping during import.

## Web UI

The dashboard shows a "Scheduled Jobs" section:
- List of active jobs with last run status and next run time
- Create/edit/pause/delete from the UI
- Run history per job (expandable)
- Auto-pause warning when a job hits max failures

## Configuration

```go
type JobSchedulerConfig struct {
    Enabled       bool          `json:"enabled" default:"true"`
    PollInterval  time.Duration `json:"pollInterval" default:"5s"`
    MaxPerPoll    int           `json:"maxPerPoll" default:"50"`
    RunTimeout    time.Duration `json:"runTimeout" default:"5m"`
    MaxRunHistory int           `json:"maxRunHistory" default:"100"`  // per job
}
```

## Acceptance Criteria

- [ ] `agent_jobs` and `agent_job_runs` tables created with RLS
- [ ] Scheduler worker starts with server, respects cancellation
- [ ] Cron, interval, and one-shot schedules all work correctly
- [ ] Jobs auto-pause after `max_failures` consecutive errors
- [ ] `FOR UPDATE SKIP LOCKED` prevents double-execution
- [ ] API endpoints for full CRUD + trigger + history
- [ ] CLI commands for list, create, pause, resume, run, history, delete
- [ ] One-shot jobs set status = 'completed' after firing
- [ ] Stale "running" jobs cleaned up after timeout
- [ ] Run history auto-pruned to `maxRunHistory` per job
- [ ] Agent can create jobs for itself via API
- [ ] `go test ./... -count=1` passes

## References

- #150 — Conversation Schema Redesign (rooms, chat_messages tables)
- #155 — Migration from OpenClaw (cron import)
- OpenClaw cron: supports `at`, `every`, `cron` schedule kinds — same three we implement

## Execution Log

- [2026-02-12 18:30 MST] Issue #921 | Commit n/a | queue-transition | Moved `157-agent-job-scheduler.md` from `01-ready` to `02-in-progress` and switched to branch `codex/spec-157-agent-job-scheduler` from `origin/main` | Tests: n/a
- [2026-02-12 18:30 MST] Issue #921 | Commit n/a | created | Created migration micro-issue for scheduler tables/RLS with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #922 | Commit n/a | created | Created AgentJobStore lifecycle micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #923 | Commit n/a | created | Created schedule computation micro-issue (`cron`/`interval`/`once`) with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #924 | Commit n/a | created | Created scheduler worker execution/failure-handling micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #925 | Commit n/a | created | Created jobs API endpoints micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #926 | Commit n/a | created | Created CLI jobs command micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #927 | Commit n/a | created | Created scheduler config/server startup integration micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #928 | Commit n/a | created | Created OpenClaw cron import micro-issue with explicit test plan | Tests: n/a
- [2026-02-12 18:30 MST] Issue #922 | Commit n/a | updated | Corrected issue body after shell quoting artifact to restore explicit test commands and implementation notes | Tests: n/a
- [2026-02-12 18:30 MST] Issue #921 | Commit bdb9c35 | closed | Added migration 073 (`agent_jobs` + `agent_job_runs`) with indexes/RLS and schema regression test coverage | Tests: go test ./internal/store -run TestMigration073AgentJobsFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1; go test ./internal/store -count=1
- [2026-02-12 18:37 MST] Issue #922 | Commit 48a7340 | closed | Added `AgentJobStore` CRUD/lifecycle operations, due-job leasing, stale-run cleanup, and run-history pruning with org-isolation coverage | Tests: go test ./internal/store -run 'TestAgentJobStore(CreateListGetUpdateDelete|OrgIsolation)' -count=1; go test ./internal/store -run 'TestAgentJobStore(PickupDueUsesSkipLockedAndLeasesRows|CleanupStaleRunning|PruneRunHistory)' -count=1; go test ./internal/store -count=1; go test ./... -count=1
- [2026-02-12 18:37 MST] Issue #923 | Commit 9401993 | closed | Added `internal/scheduler` schedule normalization + next-run computation for `cron`/`interval`/`once` with timezone-aware cron parsing | Tests: go test ./internal/scheduler -run TestJobSchedule -count=1; go test ./internal/scheduler -count=1; go test ./... -count=1
- [2026-02-12 18:43 MST] Issue #924 | Commit 60729be | closed | Added `AgentJobWorker` polling/execution loop with stale-run cleanup, failure handling, one-shot completion, room auto-provisioning, and run-history pruning hooks | Tests: go test ./internal/scheduler -run TestJobSchedulerWorker -count=1; go test ./internal/store -run TestAgentJobStorePickupDueUsesSkipLocked -count=1; go test ./internal/scheduler ./internal/store -count=1; go test ./... -count=1
- [2026-02-12 18:50 MST] Issue #925 | Commit 3ed88c0 | closed | Added jobs API handlers/routes for CRUD, run trigger, run history, pause/resume, and agent self-scoping safeguards with validation coverage | Tests: go test ./internal/api -run 'TestJobsHandler|TestRouterRegistersJobsRoutes' -count=1; go test ./internal/api -count=1; go test ./... -count=1
- [2026-02-12 18:55 MST] Issue #926 | Commit 04f00ba | closed | Added otter jobs CLI command group (list/create/pause/resume/run/history/delete), schedule-mode validation, and jobs client bindings for  endpoints | Tests: go test ./cmd/otter -run TestHandleJobs -count=1; go test ./internal/ottercli -run 'TestClient.*Jobs' -count=1; go test ./cmd/otter ./internal/ottercli -count=1; go test ./... -count=1
- [2026-02-12 18:55 MST] Issue #926 | Commit 04f00ba | updated | Corrected prior execution-log text after shell quoting expansion; jobs client bindings target /api/v1/jobs endpoints as implemented | Tests: n/a
- [2026-02-12 18:58 MST] Issue #927 | Commit 4b119db | closed | Added JobScheduler config defaults/env validation and started AgentJobWorker via shared worker lifecycle hook in cmd/server when enabled with DB available | Tests: go test ./internal/config -run 'TestLoad.*JobScheduler' -count=1; go test ./cmd/server -run TestMainStartsJobSchedulerWorkerWhenConfigured -count=1; go test ./internal/config ./cmd/server -count=1; go test ./... -count=1
- [2026-02-12 19:04 MST] Issue #928 | Commit 21dbd8b | closed | Added OpenClaw cron metadata importer (sync_metadata to disabled agent_jobs), API import endpoint, and CLI migrate from-openclaw cron command with idempotent upsert + warning handling | Tests: go test ./internal/import -run TestOpenClawCronJobImport -count=1; go test ./cmd/otter -run 'TestMigrateFromOpenClaw.*Cron' -count=1; go test ./internal/import ./cmd/otter -count=1; go test ./... -count=1
- [2026-02-12 19:04 MST] Issue #928 | Commit n/a | queue-transition | Moved 157-agent-job-scheduler.md from /Users/sam/Documents/Dev/otter-camp/issues/02-in-progress to /Users/sam/Documents/Dev/otter-camp/issues/03-needs-review after closing issues #921-#928 | Tests: n/a
