# 062: Model Attribution — Run FK Linkage, Usage Rollup, and Cost Pipeline

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 07 §ModelInvocation, doc 07 §UsageRollup, doc 16 §TokenAggregation, doc 13 §CostAttribution |
| Spec status | finished |
| Depends on | 035, 036, 052, 053, 043, 044 |
| Blocks | 063, 076, 077, 087 |

## Scope

Complete the cross-domain attribution wiring for `model_invocation`: populate `run_id`,
`run_step_id`, and `run_attempt_id` from the control plane execution context; implement
the daily token rollup job; wire the invocation purpose routing; and provide the cost
attribution query helpers used by the control plane cost API.

### Must build

**Model invocation FK population** (`internal/model/attribution.go`):

The `ModelGateway.Invoke` function (task 035) receives an `InvocationContext`. Extend
`InvocationContext` to carry the optional control plane fields:

```go
type InvocationContext struct {
    OrganizationID    uuid.UUID
    AgentID           *uuid.UUID
    ProjectID         *uuid.UUID
    ProjectTaskID     *uuid.UUID
    SessionID         *uuid.UUID
    TurnID            *uuid.UUID
    // Control plane attribution (all optional):
    RunID             *uuid.UUID
    RunStepID         *uuid.UUID
    RunAttemptID      *uuid.UUID
    InvocationPurpose string  // see purpose routing below
}
```

When `RunService` (task 053) dispatches a worker run, it must populate all three control
plane fields on the `InvocationContext` it passes to the model gateway:
- `RunID` = the current run's ID
- `RunStepID` = the current run step's ID (if a step is active)
- `RunAttemptID` = the current attempt's ID

In the turn engine (task 048), model calls during an agent turn that are NOT inside a
worker run (i.e., sync chat turn loop calls) must set `RunID=nil`, `RunStepID=nil`,
`RunAttemptID=nil`. The session and turn IDs are always set if available.

Implement `AttributionMiddleware.Populate(ctx context.Context) InvocationContext`:
- Reads attribution fields from `ctx` using typed context keys.
- Callers (turn engine, worker dispatcher, summarization pipeline) are responsible for
  injecting their attribution context before calling the model gateway.
- This replaces ad-hoc attribution wiring — all gateway callers must use this helper.

**Invocation purpose routing** — `model_invocation.invocation_purpose` is set to one of:
- `'agent_turn'` — agent responding in a chat session (set by turn engine)
- `'listening_eval'` — Haiku-class listening evaluation (set by turn engine phase 1.5)
- `'summarization'` — progressive summarization (set by summarization pipeline, task 045)
- `'skill_summarization'` — skill prompt summarization (set by prompt assembly, task 050)
- `'memory_extraction'` — memory extraction pipeline (set by task 039)
- `'memory_retrieval_classification'` — taxonomy classification in retrieval (set by task 040)
- `'memory_dedup'` — dedup clustering LLM call (set by task 041)
- `'memory_contradiction'` — contradiction detection (set by task 041)
- `'replay'` — inference context replay (set by task 063 observability)
- `'tool_vision'` — vision model call from browser screenshot analysis (set by task 059)

`invocation_purpose` is stored as a `text` column (open vocabulary; no DB enum).

**Denormalized token rollup on run/run_step/run_attempt** (`internal/model/rollup_updater.go`):

After every `model_invocation` row is committed, asynchronously update the denormalized token
counts on the associated control plane rows:

`RollupUpdater.UpdateRunTokenCounts(ctx, invocation ModelInvocation) error`:
- If `invocation.run_id` is not null: execute
  `UPDATE run SET input_tokens = input_tokens + $1, output_tokens = output_tokens + $2 WHERE id = $3`
  (atomic increment; no read-modify-write race).
- If `invocation.run_step_id` is not null: same pattern for `run_step`.
- If `invocation.run_attempt_id` is not null: same pattern for `run_attempt`.
- All three updates run in a single DB transaction.
- Called asynchronously from a goroutine spawned after `ModelInvocationRepository.Create`
  succeeds; errors are logged but do not fail the model invocation itself.

Note: `run`, `run_step`, and `run_attempt` tables must have `input_tokens integer not null
default 0` and `output_tokens integer not null default 0` columns. If these columns are absent
from the task 052 schema, add them via a supplemental migration:
`0075_run_token_columns.sql`.

**Daily token rollup job** (`internal/model/rollup_job.go`):

Job type: `model_usage_rollup_daily` (registered in the job queue from task 024).

`DailyRollupJob.Run(ctx, job Job) error`:
- Triggered once per day (scheduled via `task_schedule` or a cron-style background tick —
  use a simple 24-hour ticker in the worker process, not a `task_schedule` row, to avoid
  circular dependency).
- Aggregation query: for each `(organization_id, model_provider_id, rollup_type, rollup_id)`
  combination with `model_invocation` rows created in the previous calendar day (UTC):
  - Sum `input_tokens`, `output_tokens`, `total_tokens`.
  - Upsert `model_usage_rollup` row with `period_start = start of previous day`,
    `period_end = end of previous day`, `granularity='daily'`.
- `rollup_type` / `rollup_id` combinations to aggregate:
  - `('provider_connection', provider_connection_id)` — per connection
  - `('model_provider', model_provider_id)` — per provider
  - `('agent', agent_id)` where `agent_id` is not null
  - `('project', project_id)` where `project_id` is not null
- Idempotent: uses `ON CONFLICT (organization_id, rollup_type, rollup_id, period_start)
  DO UPDATE SET input_tokens = EXCLUDED.input_tokens, ...` so re-running for the same day
  overwrites with the fresh sum.
- Emits `domain_event(event_type='model.usage_rollup.completed', payload={date, org_id,
  row_count})` on success.

**Cost attribution query helpers** (`internal/model/cost_query.go`):

`CostQuery.SumForRun(ctx, runID) TokenSummary`:
- `SELECT SUM(input_tokens), SUM(output_tokens), SUM(total_tokens) FROM model_invocation WHERE run_id = $1`
- Returns `TokenSummary{InputTokens, OutputTokens, TotalTokens, EstimatedCostCents}`.
- `EstimatedCostCents` is computed display-only:
  `(input_tokens * provider.input_cost_per_1k / 1000) + (output_tokens * provider.output_cost_per_1k / 1000)`
  using the `model_provider` cost columns. Never stored.

`CostQuery.SumForTask(ctx, taskID) TokenSummary`:
- Sums across all model invocations where `project_task_id = $1`.

`CostQuery.SumForSession(ctx, sessionID) TokenSummary`:
- Sums across all model invocations where `session_id = $1`.

These helpers are called by the control plane cost API (`GET /v1/control/cost/summary`
in task 054) and the model usage API (`GET /v1/usage` in task 037).

### Must NOT build

- `model_invocation` DDL (task 036)
- `model_usage_rollup` DDL (task 036)
- Model gateway core routing/streaming (tasks 035, 036)
- Run/run_step/run_attempt DDL (task 052)
- Token budget enforcement (tasks 023, 052)
- `GET /v1/control/cost/summary` API handler (task 054)

## Acceptance Criteria

- [ ] `InvocationContext` carries `RunID`, `RunStepID`, `RunAttemptID`; all three are null for chat-only turn loop calls
- [ ] After a model invocation inside a worker run, `run.input_tokens` and `run.output_tokens` are atomically incremented
- [ ] `DailyRollupJob.Run` produces `model_usage_rollup` rows with correct `rollup_type='agent'` and `rollup_type='project'` groupings; re-running for the same day overwrites (no duplicates)
- [ ] `CostQuery.SumForRun` returns the correct token sum for a run with multiple model invocations
- [ ] `CostQuery.SumForRun` returns zero-value `TokenSummary` for a run with no invocations
- [ ] `model_invocation.invocation_purpose` is set to `'agent_turn'` for turn-engine calls and `'memory_extraction'` for extraction pipeline calls

## Tests Required

**Unit tests:**
- `AttributionMiddleware.Populate`: inject run attribution into context; verify all three fields present; empty context → all nil
- `RollupUpdater.UpdateRunTokenCounts`: mock DB; verify three UPDATE statements issued in single transaction; null `run_attempt_id` → run_attempt UPDATE not issued
- `CostQuery.SumForRun`: mock repository returning 3 invocations; verify sum correct; verify `EstimatedCostCents` computed and not stored
- `DailyRollupJob.Run`: mock DB with known invocation rows; verify correct number of upsert calls; idempotency: run twice → same result

**Integration tests:**
- Full attribution round-trip: create run + run_step + run_attempt; invoke model gateway with populated context; verify `model_invocation` row has all three FK columns set
- Token increment: create run; fire two model invocations (100 + 200 input tokens); verify `run.input_tokens = 300`
- Daily rollup: seed 5 `model_invocation` rows for yesterday across 2 agents; run `DailyRollupJob`; verify 2 `model_usage_rollup` rows with correct sums; run again → same 2 rows (no duplicates)

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

> ✅ ISSUE #18 (RESOLVED): `run_attempt_id` is null for standalone chat turn-loop calls; set only for model calls within a control plane run attempt. `CostQuery.SumForRun` uses `WHERE run_id = $1` to capture all invocations in a run. Per-attempt breakdowns use `WHERE run_attempt_id = $1`. No change needed to existing aggregation logic.

The `run`, `run_step`, and `run_attempt` token columns may need to be added via
`0075_run_token_columns.sql` if task 052 did not include them. Check task 052 schema
before writing this migration; if the columns exist, skip it.
