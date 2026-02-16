# Issue #9: Agent Activity Log (Holistic Session History)

## Summary

Build a unified agent activity log that captures **everything an agent does**, regardless of how it was triggered — Slack, TUI, Telegram, cron job, sub-agent spawn, heartbeat, or direct chat. The agent dashboard should show the last thing each agent worked on, and a full log should show the complete timeline of all agent activity within OpenClaw.

## The Problem

Today, agent activity is fragmented across multiple disconnected surfaces:

1. **Session transcripts** — OpenClaw writes transcript files to disk, but they're per-session, scattered across the filesystem, and not queryable
2. **Sync snapshots** — The bridge pushes session state every 30s, but only captures a point-in-time snapshot (status, currentTask, tokens) — not a timeline
3. **Agent cards** — Show `currentTask` from the latest sync, but no history of what the agent did before
4. **Chat history** — DM and project chat messages are stored in Otter Camp, but only capture explicit chat interactions — not cron runs, heartbeats, tool calls, or sub-agent spawns
5. **Cron/process views** — The connections page shows cron jobs and processes, but doesn't tie them back to specific agent activity
6. **No cross-channel view** — If Frank responds in Slack, then runs a cron job, then handles a Telegram message — there's no single place to see that timeline

The user should be able to: open an agent's page → see everything that agent has done today, in order, with context about what triggered each action and what the outcome was.

## Architecture

### Activity Event Model

```go
type AgentActivityEvent struct {
    ID          string    `json:"id"`           // unique event id (ULID for time-ordering)
    AgentID     string    `json:"agent_id"`     // which agent
    SessionKey  string    `json:"session_key"`  // OpenClaw session key
    Trigger     string    `json:"trigger"`      // what started this activity (see below)
    Channel     string    `json:"channel"`      // slack, telegram, tui, cron, system, etc.
    Summary     string    `json:"summary"`      // human-readable one-liner
    Detail      string    `json:"detail"`       // longer context (truncated message, tool output, etc.)
    Scope       *ActivityScope `json:"scope,omitempty"` // optional project/issue context
    TokensUsed  int       `json:"tokens_used"`  // tokens consumed in this activity
    ModelUsed   string    `json:"model_used"`   // which model handled it
    DurationMs  int64     `json:"duration_ms"`  // how long the activity took
    Status      string    `json:"status"`       // started, completed, failed, timeout
    StartedAt   time.Time `json:"started_at"`
    CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type ActivityScope struct {
    ProjectID   string `json:"project_id,omitempty"`
    IssueID     string `json:"issue_id,omitempty"`
    IssueNumber int    `json:"issue_number,omitempty"`
    ThreadID    string `json:"thread_id,omitempty"`
}
```

### Trigger Types

| Trigger | Description | Example |
|---------|-------------|---------|
| `chat.slack` | User message via Slack | Sam DMs Frank in #leadership |
| `chat.telegram` | User message via Telegram | Sam messages from phone |
| `chat.tui` | User message via TUI | Sam uses `openclaw chat` |
| `chat.discord` | User message via Discord | Message in Discord channel |
| `cron.scheduled` | Cron job fired | `codex-progress-summary` every 10m |
| `cron.manual` | Cron job triggered manually | Admin runs job from connections page |
| `heartbeat` | Heartbeat poll | 15-minute heartbeat check-in |
| `spawn.sub_agent` | Sub-agent spawned by another agent | Frank spawns Codex worker |
| `spawn.isolated` | Isolated session created | Cron job in isolated session |
| `system.event` | System event injected | Config change, wake event |
| `dispatch.issue` | Issue comment dispatched to agent | Otter Camp sends comment to agent |
| `dispatch.dm` | DM dispatched to agent | Otter Camp sends DM to agent |
| `dispatch.project_chat` | Project chat dispatched | Otter Camp sends project message |

### Data Sources

The bridge collects activity data from multiple OpenClaw surfaces:

1. **Session list deltas** — Compare sessions between syncs. New sessions = new activity. Changed `updatedAt` = activity happened. Changed `displayName` = new task.

2. **Transcript files** — OpenClaw writes session transcripts to disk. The bridge can tail these for detailed activity:
   - Each transcript entry has: timestamp, role (user/assistant/system/tool), content
   - Parse into activity events with trigger detection (system messages from cron have identifiable patterns)

3. **OpenClaw WS events** — The bridge's WebSocket connection to OpenClaw receives:
   - Session start/end events
   - Agent turn start/end
   - Tool call events
   - Heartbeat responses

4. **Cron job execution** — Bridge already tracks cron jobs. Correlate job runs with session activity.

5. **Dispatch queue** — Bridge processes dispatch events (DM, project chat, issue comments). Each dispatch = activity.

### Storage

Unlike emissions (Issue #8, ephemeral), activity events are **persistent** — they're the historical record.

```sql
CREATE TABLE agent_activity_events (
    id              TEXT PRIMARY KEY,        -- ULID
    org_id          TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    session_key     TEXT,
    trigger         TEXT NOT NULL,
    channel         TEXT,
    summary         TEXT NOT NULL,
    detail          TEXT,
    project_id      TEXT,
    issue_id        TEXT,
    issue_number    INTEGER,
    thread_id       TEXT,
    tokens_used     INTEGER DEFAULT 0,
    model_used      TEXT,
    duration_ms     BIGINT,
    status          TEXT NOT NULL DEFAULT 'completed',
    started_at      TIMESTAMPTZ NOT NULL,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_activity_agent_started ON agent_activity_events(agent_id, started_at DESC);
CREATE INDEX idx_activity_org_started ON agent_activity_events(org_id, started_at DESC);
CREATE INDEX idx_activity_project ON agent_activity_events(project_id, started_at DESC) WHERE project_id IS NOT NULL;
CREATE INDEX idx_activity_trigger ON agent_activity_events(trigger, started_at DESC);
CREATE INDEX idx_activity_status ON agent_activity_events(status) WHERE status != 'completed';
```

Retention: Keep 30 days of detailed events, then aggregate to daily summaries. (Configurable.)

### Bridge Collection

The bridge becomes the primary collector. On each sync cycle:

```typescript
// 1. Detect session deltas
const deltas = detectSessionDeltas(currentSessions, previousSessions);

// 2. For each delta, create an activity event
for (const delta of deltas) {
    const event = {
        agent_id: delta.agentId,
        session_key: delta.sessionKey,
        trigger: inferTrigger(delta),   // inspect session kind, channel, etc.
        channel: delta.channel,
        summary: buildSummary(delta),
        tokens_used: delta.tokenDelta,
        model_used: delta.model,
        started_at: delta.timestamp,
        status: 'completed',
    };
    activityBuffer.push(event);
}

// 3. Push events to Otter Camp
await pushActivityEvents(activityBuffer);
```

**Trigger inference**: The bridge determines what triggered an activity by inspecting:
- `session.kind` — "main" = user chat, "isolated" = cron/spawn, "sub" = sub-agent
- `session.channel` — "slack", "telegram", "tui", etc.
- `session.lastChannel` + `session.lastTo` — routing info
- Session key patterns — `cron:*` = cron, `spawn:*` = sub-agent
- Display name changes — often contain task descriptions

## API Endpoints

```
GET  /api/agents/{id}/activity     — Agent's activity timeline
     ?limit=50                     — pagination
     &before=<timestamp>           — cursor-based pagination
     &trigger=cron.scheduled       — filter by trigger type
     &channel=slack                — filter by channel
     &status=failed                — filter by status
     &project_id=<uuid>            — filter by project scope

GET  /api/activity/recent          — All agents' recent activity
     ?limit=50
     &agent_id=<id>                — filter to specific agent
     &trigger=<type>
     &channel=<channel>

POST /api/activity/events          — Bridge pushes activity events (authed)
```

## Frontend Integration

### Agent Card Enhancement

Each agent card shows:
- **Last activity summary** — "Responded to Sam in #leadership" or "Ran codex-progress-summary cron" or "Completed issue #290 close-flow tests"
- **Trigger badge** — small icon/label showing what triggered it (💬 Slack, ⏰ Cron, 🔧 System)
- **Time since** — "2m ago" (live-updating, pairs with Issue #8's LiveTimestamp)
- **Activity sparkline** — tiny chart showing activity density over last 24h (optional, polish phase)

### Agent Detail — Activity Tab

Full timeline view:

```
┌──────────────────────────────────────────────────────────────┐
│ Frank — Activity                                    [filters]│
├──────────────────────────────────────────────────────────────┤
│                                                              │
│ 10:43 AM  💬 Slack                                          │
│ Responded to Sam: "Can you set a cron to read the progress  │
│ log file every 10 minutes..."                                │
│ opus-4-6 · 4.2k tokens · 12s                                │
│                                                              │
│ 10:35 AM  ⏰ Cron (codex-progress-summary)                  │
│ Read progress log, summarized Codex work for Sam             │
│ sonnet-4 · 1.8k tokens · 8s                                 │
│                                                              │
│ 10:20 AM  💬 Slack                                          │
│ Cleaned up 49 GitHub branches (43 merged + 6 stale)          │
│ opus-4-6 · 6.1k tokens · 45s                                │
│                                                              │
│ 09:50 AM  🔧 System (memory flush)                          │
│ Stored daily notes to memory/2026-02-08.md, updated MEMORY.md│
│ opus-4-6 · 2.1k tokens · 5s                                 │
│                                                              │
│ 09:40 AM  💓 Heartbeat                                      │
│ HEARTBEAT_OK — nothing to report                             │
│ opus-4-6 · 0.3k tokens · 2s                                 │
│                                                              │
│  ─── Earlier Today ───                                       │
│                                                              │
│ 08:51 AM  ⏰ Cron (isolated)                                │
│ Codex completed Spec006 #286, moved to needs-review          │
│ ...                                                          │
└──────────────────────────────────────────────────────────────┘
```

**Filters:**
- By trigger type (chat, cron, heartbeat, spawn, system)
- By channel (Slack, Telegram, TUI, all)
- By status (completed, failed, in-progress)
- By date range
- By project scope

**Detail expansion:** Click any event to expand and see full detail (complete message, tool calls, response)

### Dashboard — "Last Action" Column

The dashboard's project sidebar and any agent list view should show each agent's last action:

```
Frank    💬 "Setting up progress log cron"     2m ago
Derek    ⏰ "Implementing #105 pipeline spec"   now
Stone    💬 "Drafting Technonymous post"         3h ago
Nova     ⏰ "Running engagement cycle"           15m ago
```

### Global Activity Feed

The Feed page gets a new "Agent Activity" mode (tab or toggle) that shows all agent activity across the org, unified and time-ordered. This is the "mission control" view — everything happening in OpenClaw in one stream.

### Connections Page Enhancement

The connections page agent session table gets:
- **Last activity** column — summary + timestamp of most recent activity event
- **Activity count** — number of events in last hour/day
- **Error highlight** — agents with recent failed activities flagged

## Relationship to Issue #8 (Emissions)

**Emissions** (Issue #8) are **ephemeral, real-time, UI-focused** — they make the dashboard feel alive right now. They're in-memory only, no persistence.

**Activity events** (this issue) are **persistent, historical, data-focused** — they build the complete record of what happened. They're stored in the database with retention.

They complement each other:
- Emissions power the "pulsing dot" and "12s ago" ticker
- Activity events power the "what did Frank do today?" timeline
- An emission can *generate* an activity event (milestone emissions → activity events)
- Activity events can *generate* emissions (new activity → emission for real-time display)

In practice, the bridge might produce both from the same source data — an emission for immediate display and an activity event for the permanent record.

## Backend Implementation

### New Files

```
internal/api/agent_activity.go          — handlers + activity event types
internal/api/agent_activity_test.go     — tests
internal/store/agent_activity_store.go  — DB queries (create, list, aggregate)
internal/store/agent_activity_store_test.go
migrations/040_create_agent_activity_events.up.sql
migrations/040_create_agent_activity_events.down.sql
```

### Store Methods

```go
type AgentActivityStore interface {
    CreateEvent(ctx context.Context, event *AgentActivityEvent) error
    CreateEvents(ctx context.Context, events []*AgentActivityEvent) error  // batch insert
    ListByAgent(ctx context.Context, agentID string, opts ListActivityOpts) ([]AgentActivityEvent, error)
    ListRecent(ctx context.Context, orgID string, opts ListActivityOpts) ([]AgentActivityEvent, error)
    LatestByAgent(ctx context.Context, orgID string) (map[string]AgentActivityEvent, error)  // last event per agent
    CountByAgent(ctx context.Context, agentID string, since time.Time) (int, error)
    Cleanup(ctx context.Context, olderThan time.Time) (int64, error)  // retention
}

type ListActivityOpts struct {
    Limit     int
    Before    time.Time
    Trigger   string
    Channel   string
    Status    string
    ProjectID string
}
```

### Bridge Enhancement

```typescript
// New bridge capabilities

// 1. Activity event collection
const activityBuffer: AgentActivityEvent[] = [];

// 2. Trigger inference from session metadata
function inferTrigger(session: OpenClawSession, previousSession?: OpenClawSession): string {
    if (session.key.startsWith('cron:')) return 'cron.scheduled';
    if (session.key.startsWith('spawn:')) return 'spawn.sub_agent';
    if (session.kind === 'isolated') return 'spawn.isolated';
    if (!previousSession) return `chat.${session.channel || 'unknown'}`;
    // More heuristics based on session metadata changes
    return `chat.${session.channel || 'unknown'}`;
}

// 3. Push activity events to Otter Camp
async function pushActivityEvents(events: AgentActivityEvent[]): Promise<void> {
    if (events.length === 0) return;
    await fetch(`${OTTERCAMP_URL}/api/activity/events`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
            Authorization: `Bearer ${OTTERCAMP_TOKEN}`,
        },
        body: JSON.stringify({ events }),
    });
}

// 4. Dispatch events → activity events
// Every dispatch event (DM, project chat, issue comment) the bridge processes
// also generates an activity event for the responding agent
```

### Frontend Files

```
web/src/components/agents/AgentActivityTimeline.tsx    — full timeline component
web/src/components/agents/AgentActivityItem.tsx        — single activity event row
web/src/components/agents/ActivityTriggerBadge.tsx     — trigger type icon/label
web/src/components/agents/AgentLastAction.tsx           — compact "last action" display
web/src/hooks/useAgentActivity.ts                      — fetch + WS-driven activity hook
```

## Testing

### Backend
- Store: create, list with filters, latest-by-agent, batch insert, cleanup
- API: auth, pagination, filter params, scope filtering
- Migration: up and down, indexes exist

### Bridge
- Trigger inference: correct trigger from session metadata
- Delta detection: activity events generated for status/task changes
- Batch push: events buffered and sent efficiently
- Dispatch correlation: dispatch events produce activity events

### Frontend
- Timeline renders events with correct formatting
- Trigger badges show correct icons
- Filters work (trigger, channel, status, date)
- Real-time updates: new events appear via WS
- Agent card shows last activity
- Pagination: scroll loads more events

## Rollout

1. **Migration + store** — Create table, indexes, store methods
2. **API endpoints** — Activity list + recent + push
3. **Bridge collection** — Delta detection, trigger inference, push to Otter Camp
4. **Agent card enhancement** — Show last activity on agent cards
5. **Agent detail timeline** — Full activity tab on agent page
6. **Dashboard integration** — Last action in sidebar, global feed mode
7. **Connections page** — Activity columns in session table

## Success Criteria

- Open any agent's page → see complete timeline of everything they did today
- Each activity event shows: what happened, what triggered it, which channel, how long, how many tokens
- Can filter by trigger type to see "just cron runs" or "just Slack conversations"
- Agent cards show the last thing each agent did + how long ago
- Works for every trigger type: Slack, Telegram, TUI, cron, sub-agent, heartbeat, system events
- No information lost — every agent turn, regardless of source, is captured
- 30-day retention with automatic cleanup

## Execution Log
- [2026-02-08 12:29 MST] Issue spec #009 | Commit n/a | in-progress | Moved spec from `01-ready` to `02-in-progress` and created branch `codex/spec-009-agent-activity-log` from `origin/main` | Tests: n/a
- [2026-02-08 12:30 MST] Issue #329 | Commit n/a | opened | Planned schema + core store for persistent `agent_activity_events` with pagination/filter tests | Tests: n/a
- [2026-02-08 12:30 MST] Issue #330 | Commit n/a | opened | Planned store batch insert/aggregations/cleanup methods for activity retention and agent summaries | Tests: n/a
- [2026-02-08 12:30 MST] Issue #331 | Commit n/a | opened | Planned bridge-authenticated `POST /api/activity/events` ingestion endpoint and validation tests | Tests: n/a
- [2026-02-08 12:30 MST] Issue #332 | Commit n/a | opened | Planned agent and org recent activity query endpoints with filter/pagination coverage | Tests: n/a
- [2026-02-08 12:30 MST] Issue #333 | Commit n/a | opened | Planned bridge trigger inference and session-delta activity event generation | Tests: n/a
- [2026-02-08 12:30 MST] Issue #334 | Commit n/a | opened | Planned bridge batching/push and dispatch-correlation activity event pipeline | Tests: n/a
- [2026-02-08 12:30 MST] Issue #335 | Commit n/a | opened | Planned frontend `useAgentActivity` hook and timeline primitive components/tests | Tests: n/a
- [2026-02-08 12:30 MST] Issue #336 | Commit n/a | opened | Planned agent last-action and agent detail timeline route integration/tests | Tests: n/a
- [2026-02-08 12:30 MST] Issue #337 | Commit n/a | opened | Planned feed/connections integration for persistent activity mode/columns | Tests: n/a
- [2026-02-08 12:30 MST] Issue #338 | Commit n/a | opened | Planned websocket broadcast and realtime activity-hook updates | Tests: n/a
- [2026-02-08 12:33 MST] Issue #329 | Commit e972a29 | closed | Added `agent_activity_events` migration + RLS and core `AgentActivityEventStore` create/list-by-agent/list-recent methods with filter/pagination tests | Tests: go test ./internal/store -run 'TestAgentActivityEventStore(Create|ListByAgent|ListRecent)' -count=1; go test ./internal/store -count=1
- [2026-02-08 12:35 MST] Issue #330 | Commit 25ab764 | closed | Extended `AgentActivityEventStore` with batch insert, latest-by-agent, count-since, and cleanup retention operations + tests | Tests: go test ./internal/store -run 'TestAgentActivityEventStore(BatchCreate|LatestByAgent|CountByAgentSince|CleanupOlderThan)' -count=1; go test ./internal/store -count=1
- [2026-02-08 12:38 MST] Issue #331 | Commit 7b055c0 | closed | Added authenticated `POST /api/activity/events` with strict validation, workspace-scoped batch persistence, and API tests for success/auth/validation paths | Tests: go test ./internal/api -run 'TestActivityEventsIngestHandler' -count=1; go test ./internal/api -count=1
- [2026-02-08 12:45 MST] Issue #332 | Commit 9e035cf | closed | Added `GET /api/agents/{id}/activity` and `GET /api/activity/recent` endpoints with filter/pagination validation, route wiring, and API coverage for scope/query failures | Tests: go test ./internal/api -run 'TestAgentActivity(ListByAgent|Recent)Handler' -count=1; go test ./internal/api -count=1
- [2026-02-08 12:51 MST] Issue #333 | Commit 1ee66e9 | closed | Added bridge activity-event model, trigger inference, deterministic session-delta event generation, and bridge delta tests with vitest config updates for external bridge test paths | Tests: cd web && npm test -- ../bridge/__tests__/openclaw-bridge.activity-delta.test.ts --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 12:57 MST] Issue #334 | Commit 7230114 | closed | Added bridge org-buffered activity batch push to `POST /api/activity/events`, dispatch-correlation activity events, retry/dedupe safeguards, and bridge buffer tests | Tests: cd web && npm test -- ../bridge/__tests__/openclaw-bridge.activity-delta.test.ts ../bridge/__tests__/openclaw-bridge.activity-buffer.test.ts --run; go test ./internal/api -run 'TestOpenClawSync(ActivityEvents|DispatchCorrelation)' -count=1; go test ./internal/api -count=1; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:03 MST] Issue #335 | Commit 4f09493 | closed | Added `useAgentActivity` query/pagination hook and new agent timeline primitives (`ActivityTriggerBadge`, `AgentActivityItem`, `AgentActivityTimeline`) with frontend tests | Tests: cd web && npm test -- src/hooks/useAgentActivity.test.ts src/components/agents/ActivityTriggerBadge.test.tsx src/components/agents/AgentActivityItem.test.tsx src/components/agents/AgentActivityTimeline.test.tsx --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:09 MST] Issue #336 | Commit 9b62aaa | closed | Added `AgentLastAction`, wired latest persistent activity onto `AgentsPage` cards, and introduced `/agents/:id` timeline page + route with filters/tests | Tests: cd web && npm test -- src/components/agents/AgentLastAction.test.tsx src/pages/AgentsPage.test.tsx src/pages/AgentDetailPage.test.tsx --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:13 MST] Issue #337 | Commit 0491223 | closed | Added Feed mode toggle for org-wide persistent activity timeline and enriched Connections session table with last activity/1h count/error indicators + tests | Tests: cd web && npm test -- src/pages/FeedPage.test.tsx src/pages/ConnectionsPage.test.tsx --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:19 MST] Issue #338 | Commit 46a6a89 | closed | Added ActivityEventReceived websocket broadcast from ingest plus realtime `useAgentActivity` append/dedupe/filter handling and ws parser updates with backend/frontend tests | Tests: go test ./internal/api -run 'TestActivityEventWebsocketBroadcast' -count=1; go test ./internal/api -count=1; cd web && npm test -- src/hooks/__tests__/useWebSocket.test.ts src/hooks/useAgentActivity.test.ts --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:19 MST] Issue spec #009 | Commit 46a6a89 | ready-for-review | Completed all planned Spec009 issues (#329-#338) and moved spec from `02-in-progress` to `03-needs-review` for external review | Tests: see per-issue log entries
- [2026-02-08 13:33 MST] Issue spec #009 | Commit n/a | resumed-in-progress | Moved spec from `01-ready` to `02-in-progress` to address reviewer-required fixes (#344-#349) in priority order | Tests: n/a
- [2026-02-08 13:33 MST] Issue spec #009 | Commit n/a | branch-selected | Using branch `codex/spec-009-agent-activity-log-fixes` (created from `codex/spec-009-agent-activity-log` because that branch is attached to another local worktree) | Tests: n/a
- [2026-02-08 13:34 MST] Issue #344 | Commit 52a882f | closed | Replaced ES2022 `.at(-1)` usage in `AgentDetailPage.test.tsx` with ES2020-safe indexing to restore `tsc` compatibility | Tests: cd web && npm run build:typecheck
- [2026-02-08 13:35 MST] Issue #345 | Commit 2732d00 | closed | Forced bridge tests to Node env and hardened shared test setup for non-window environments so bridge suites run outside jsdom | Tests: cd web && npm test -- ../bridge/__tests__/openclaw-bridge.activity-delta.test.ts ../bridge/__tests__/openclaw-bridge.activity-buffer.test.ts --run; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:37 MST] Issue #346 | Commit af822e4 | closed | Added regression coverage proving activity events remain queued on failed push and are marked delivered only after successful ACK | Tests: cd web && npm test -- ../bridge/__tests__/openclaw-bridge.activity-buffer.test.ts ../bridge/__tests__/openclaw-bridge.activity-delta.test.ts --run
- [2026-02-08 13:39 MST] Issue #347 | Commit 6d9df1a | closed | Added bounded `sessionContexts` retention (default 5000 with eviction + context-prime cleanup) and eviction regression tests | Tests: cd web && npm test -- ../bridge/__tests__/openclaw-bridge.activity-delta.test.ts ../bridge/__tests__/openclaw-bridge.activity-buffer.test.ts --run
- [2026-02-08 13:41 MST] Issue #348 | Commit 5f938f4 | closed | Bounded realtime dedupe ID tracking in `useAgentActivity` to 1000 entries with oldest eviction and helper-level tests | Tests: cd web && npm test -- src/hooks/useAgentActivity.test.ts --run
- [2026-02-08 13:42 MST] Issue #349 | Commit 20edcc2 | closed | Added `aria-expanded` to agent activity detail toggle and updated component tests to verify expanded-state accessibility | Tests: cd web && npm test -- src/components/agents/AgentActivityItem.test.tsx --run
- [2026-02-08 13:43 MST] Issue spec #009 | Commit 20edcc2 | reviewer-required-resolved | Resolved all reviewer-required fixes (#344-#349) and removed top-level reviewer-required block from spec body; summary preserved in execution log | Tests: cd web && npm run build:typecheck; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 13:44 MST] Issue spec #009 | Commit 20edcc2 | moved-to-needs-review | Completed reviewer fix issue set (#344-#349); moved spec from `02-in-progress` to `03-needs-review` pending external sign-off | Tests: cd web && npm run build:typecheck; cd web && npm test -- --run (known pre-existing failure: src/layouts/DashboardLayout.test.tsx)
- [2026-02-08 20:00 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Reconciled local queue state: spec already implemented/reconciled in execution log; moved from `01-ready` to `03-needs-review` for review tracking | Tests: n/a
