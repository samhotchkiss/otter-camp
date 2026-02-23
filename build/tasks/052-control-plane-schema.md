# 052: Control Plane Schema — Run, Step, Attempt, Tool Execution, Artifact, Event

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 16 §RunSchema, doc 16 §RunStepSchema, doc 16 §RunAttemptSchema, doc 16 §ToolExecution, doc 16 §RunArtifact, doc 16 §RunEvent |
| Spec status | finished |
| Depends on | 003, 004, 013, 016, 027, 033, 043 |
| Blocks | 053, 054, 055, 056, 057, 058, 059, 062, 077, 087 |

## Scope

Build the control plane schema: all six core tables (`run`, `run_step`, `run_attempt`,
`tool_execution`, `run_artifact`, `run_event`) plus repository layer for each. These tables
were forward-referenced as plain `uuid` columns in tasks 035, 048, and 049 — this task
creates the authoritative DDL and fixes those stubs.

### Must build

**Migrations:**
- `0055_run.sql`
- `0056_run_step.sql`
- `0057_run_attempt.sql`
- `0058_tool_execution.sql`
- `0059_run_artifact.sql`
- `0060_run_event.sql`

**`run` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid references project(id) on delete set null`
- `task_id uuid references project_task(id) on delete set null`
- `flow_node_id uuid references flow_node(id) on delete set null`
- `session_id uuid references chat_session(id) on delete set null`
- `turn_id uuid references chat_turn(id) on delete set null`
- `principal_type text not null check (principal_type in ('human_user','agent','system'))`
- `principal_id uuid not null`
- `status text not null check (status in ('created','in_progress','paused','completed','failed','timed_out','cancelled','cancelling','dead_letter'))` default `'created'`
- `idempotency_key text` — nullable; unique within org (application-layer dedup, see task 053)
- `trigger_type text not null check (trigger_type in ('chat_turn','scheduler','api','supervisor','agent_tool'))` — what initiated the run
- `version integer not null default 1` — optimistic concurrency token; incremented on every status update
- `failure_reason text` — populated on failed/timed_out/dead_letter
- `failure_class text check (failure_class in ('transient','permanent','policy_denied','budget_exceeded','timeout'))` — nullable
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- `started_at timestamptz`
- `completed_at timestamptz`
- Indexes: `(organization_id, status)`, `(task_id, status)`, `(session_id)`, `(idempotency_key)` WHERE idempotency_key IS NOT NULL

**`run_step` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `run_id uuid not null references run(id) on delete cascade`
- `step_number integer not null` — sequential within the run, starting at 1
- `status text not null check (status in ('pending','in_progress','completed','failed','cancelled','skipped'))` default `'pending'`
- `tool_name text` — the tool being executed in this step (nullable for orchestration steps)
- `tool_tier text check (tool_tier in ('tier1','tier2'))` — nullable
- `started_at timestamptz`
- `completed_at timestamptz`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `UNIQUE (run_id, step_number)`
- Index: `(run_id, status)`

**`run_attempt` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `run_step_id uuid not null references run_step(id) on delete cascade`
- `attempt_number integer not null` — retry count starting at 1; new row per retry (never overwrite)
- `trigger text not null check (trigger in ('initial','retry_transient','retry_policy','supervisor_recovery'))` — why this attempt was created
- `status text not null check (status in ('pending','in_progress','completed','failed','timed_out','cancelled'))` default `'pending'`
- `failure_reason text`
- `failure_class text check (failure_class in ('transient','permanent','policy_denied','budget_exceeded','timeout'))` — nullable
- `output jsonb` — nullable; structured result of the attempt
- `output_summary text` — nullable; human-readable summary
- `worker_type text check (worker_type in ('cli','browser','mcp','native','internal'))` — nullable
- `worker_id text` — nullable; identifies the specific worker process/goroutine
- `input_tokens integer not null default 0`
- `output_tokens integer not null default 0`
- `started_at timestamptz`
- `completed_at timestamptz`
- `duration_ms integer` — populated on completion
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `UNIQUE (run_step_id, attempt_number)`
- Index: `(run_step_id, status)`

**`tool_execution` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `run_id uuid not null references run(id) on delete cascade`
- `run_step_id uuid references run_step(id) on delete set null`
- `run_attempt_id uuid references run_attempt(id) on delete set null`
- `tool_name text not null`
- `tool_tier text not null check (tool_tier in ('tier1','tier2'))` — always `tier2` for full control-plane executions; tier1 may appear for audit purposes
- `tool_domain text not null check (tool_domain in ('native','mcp','cli','browser'))` — which executor handled it
- `capability text` — the capability string checked (e.g. `system.file.write`); nullable for tier1
- `policy_decision text not null check (policy_decision in ('allowed','denied','not_checked'))` — result of capability gate; tier1 tools skip gate → `not_checked`
- `input jsonb not null default '{}'` — sanitized tool input (secrets redacted before storage)
- `output jsonb` — nullable; populated on completion
- `status text not null check (status in ('pending','in_progress','completed','failed','policy_denied','timed_out'))` default `'pending'`
- `error_message text` — nullable
- `started_at timestamptz`
- `completed_at timestamptz`
- `duration_ms integer`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- Indexes: `(run_id)`, `(run_step_id)`, `(tool_name, created_at)`, `(policy_decision)` WHERE policy_decision = 'denied'

**`run_artifact` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `run_id uuid not null references run(id) on delete cascade`
- `run_step_id uuid references run_step(id) on delete set null`
- `run_attempt_id uuid references run_attempt(id) on delete set null`
- `artifact_type text not null check (artifact_type in ('stdout','stderr','screenshot','file_snapshot','structured_output','error_detail'))` — what kind of output
- `storage_key text not null` — object storage key (see task 004 storage abstraction); unique
- `content_type text not null` — MIME type (e.g. `text/plain`, `image/png`, `application/json`)
- `byte_size integer not null` — file size in bytes
- `inline_content text` — nullable; populated for outputs ≤50KB (see task 058 inline limit)
- `filename text` — nullable; original filename for file_snapshot artifacts
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `UNIQUE (storage_key)`
- Indexes: `(run_id)`, `(run_step_id)`, `(artifact_type, run_id)`

**`run_event` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `run_id uuid not null references run(id) on delete cascade`
- `run_step_id uuid references run_step(id) on delete set null`
- `run_attempt_id uuid references run_attempt(id) on delete set null`
- `sequence integer not null` — monotonic sequence within the run; enforced unique
- `event_type text not null check (event_type in ('run_started','run_completed','run_failed','run_cancelled','run_timed_out','run_paused','step_started','step_completed','step_failed','attempt_started','attempt_completed','attempt_failed','tool_called','tool_returned','heartbeat','output_chunk','policy_denied','budget_exceeded','supervisor_recovery'))` — event taxonomy
- `actor_type text not null check (actor_type in ('human_user','agent','system','supervisor'))` — NOTE: supervisor extension; see ISSUE #20 for audit_event divergence
- `actor_id uuid` — nullable for system/supervisor events
- `payload jsonb not null default '{}'` — event-specific data (e.g. output chunk delta, error info)
- `created_at timestamptz not null default now()`
- `UNIQUE (run_id, sequence)`
- Index: `(run_id, created_at)`, `(run_id, event_type)`

**Repository layer** (in `internal/controlplane/`):
- `RunRepository` — Create, Get, GetByIdempotencyKey, UpdateStatus (optimistic concurrency via `version`), List (filters: org_id, task_id, status, principal_id, limit/cursor), ListByTask, Cancel
- `RunStepRepository` — Create, Get, GetByRunAndNumber, UpdateStatus, ListByRun
- `RunAttemptRepository` — Create, Get, GetLatestByStep, UpdateStatus, ListByStep, ListByRun
- `ToolExecutionRepository` — Create, Get, UpdateStatus, ListByRun, ListByStep, ListDenied
- `RunArtifactRepository` — Create, Get, GetByStorageKey, ListByRun, ListByStep
- `RunEventRepository` — Append (next sequence auto-assigned with SELECT MAX(sequence)+1 FOR UPDATE), ListByRun (from_sequence), GetLatestHeartbeat

**FK stub migration** (fixes forward references):
- `0061_run_fk_fixup.sql` — adds FK constraints that were deferred in tasks 035 (`model_invocation.run_id`), 048 (turn engine run_id column references), 049 (`session_tool_set` metadata run attribution): `ALTER TABLE model_invocation ADD CONSTRAINT fk_model_invocation_run FOREIGN KEY (run_id) REFERENCES run(id) ON DELETE SET NULL`, same for `run_step_id` and `run_attempt_id`.

### Must NOT build

- Run execution logic and state machine (task 053)
- Control plane API endpoints (task 054)
- Tool execution dispatch and service (task 055)
- CLI sandbox execution (task 058)
- Browser execution (task 059)
- Supervisor (task 053)

## Acceptance Criteria

- [ ] All six migrations apply cleanly forward; `run`, `run_step`, `run_attempt`, `tool_execution`, `run_artifact`, `run_event` tables exist with correct columns and constraints
- [ ] `UNIQUE (run_id, step_number)` on `run_step` is enforced at DB level
- [ ] `UNIQUE (run_step_id, attempt_number)` on `run_attempt` is enforced at DB level
- [ ] `UNIQUE (run_id, sequence)` on `run_event` is enforced at DB level
- [ ] `RunEventRepository.Append` assigns the next sequence atomically (no duplicates under concurrent appends to the same run)
- [ ] `RunRepository.UpdateStatus` rejects updates where the supplied `version` does not match current DB value (optimistic concurrency; returns `ErrConflict`)
- [ ] FK fixup migration adds FK constraints on `model_invocation.run_id/run_step_id/run_attempt_id` pointing to the new tables
- [ ] `run_artifact.inline_content` is null when `byte_size > 51200` (50KB); application layer enforces this before insert

## Tests Required

**Unit tests:**
- `RunRepository.UpdateStatus`: mock DB; supply wrong `version` → returns `ErrConflict`; supply correct `version` → succeeds, version incremented
- `RunEventRepository.Append`: two concurrent appends to same `run_id` → distinct sequence numbers, no constraint violation
- `RunArtifactRepository.Create`: `byte_size=60000, inline_content="..."` → validation error before insert
- Schema validation: `run_attempt.attempt_number` starts at 1 (not 0); test that insert with `attempt_number=0` fails CHECK if added, or is caught at service layer

**Integration tests:**
- Create run → create run_step → create run_attempt; cascade: delete run → all child rows deleted
- `run_event` unique sequence: insert two events with sequence=1 for same run → second insert fails with unique constraint violation
- FK fixup: `model_invocation` rows with a valid `run_id` are correctly linked; `ON DELETE SET NULL` behavior verified by deleting the run

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

**Optimistic concurrency on `run`:**
The `version` column is the concurrency token. Every `UPDATE run SET status=$1, version=version+1 WHERE id=$2 AND version=$3` must check `rows_affected == 1`. If 0, return `ErrConflict`. The caller (task 053 state machine) handles retry.

**Sequence assignment for `run_event`:**
Use `SELECT COALESCE(MAX(sequence),0)+1 FROM run_event WHERE run_id=$1 FOR UPDATE` inside a transaction, then insert with that value. This serializes appends per run without a global sequence. Heartbeat events are appended every 30 seconds by the worker; the FOR UPDATE lock duration is negligible.

**`inline_content` policy:**
- If `byte_size <= 51200` (50KB): populate `inline_content` with the UTF-8 text content.
- If `byte_size > 51200`: leave `inline_content = NULL`; always write to object storage regardless.
- MIME type `image/png` and `image/jpeg`: never inline (always null, always stored).
- This rule is enforced in `RunArtifactRepository.Create`, not the migration constraint.

**FK stub fix timing:**
Migration `0061_run_fk_fixup.sql` must run AFTER `0055_run.sql`. The model_invocation table (from task 036) was created with nullable `uuid` columns for `run_id/run_step_id/run_attempt_id` and no FK constraint. This migration adds the FK constraints using `ALTER TABLE ... ADD CONSTRAINT`.

> ⚠️ ISSUE #19 (AMBIGUOUS): `cli_execution.run_step_id` is NOT NULL in the DDL (task 058). This implies the broker always creates a `run_step` before dispatching CLI commands. Verify with Sam if this is intended. If so, ensure `run_step` creation is documented as a required pre-condition in task 053.
