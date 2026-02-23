# 053: Control Plane Service — Run Lifecycle, State Machine, Supervisor

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 16 §RunLifecycle, doc 16 §RetryEnvelope, doc 16 §Supervisor, doc 16 §Heartbeat, doc 16 §BudgetGate, doc 16 §IdempotencyDedup |
| Spec status | finished |
| Depends on | 052, 033, 023, 024, 027, 028 |
| Blocks | 054, 055, 058, 059, 077, 087 |

## Scope

Build the control plane service: run creation with policy pre-check, the run/step/attempt state
machines, the heartbeat loop, graceful cancellation, idempotency dedup, the budget enforcement
gate, and the Supervisor background process. This is the orchestration layer that all tool
executors (tasks 055, 058, 059) call into.

### Must build

**`RunService`** (in `internal/controlplane/service.go`):

**`RunService.CreateRun(ctx, CreateRunInput) (Run, error)`:**
- Input: `{organization_id, principal_type, principal_id, trigger_type, task_id?, flow_node_id?, session_id?, turn_id?, idempotency_key?, metadata?}`
- Step 1: Idempotency dedup — if `idempotency_key` is set, check `RunRepository.GetByIdempotencyKey`; if found and `status != failed/dead_letter`, return the existing run (idempotent).
- Step 2: Policy pre-check — call `PolicyService.EvaluateRunCreation(ctx, org_id, principal)` to verify the principal is allowed to initiate runs. A denied result immediately creates a run row with `status='failed', failure_class='policy_denied'` and returns an error (run is recorded, not silently dropped).
- Step 3: Budget gate — call `BudgetService.CheckBudget(ctx, org_id, project_id?)`. Hard budget exceeded → create run with `status='failed', failure_class='budget_exceeded'`. Soft limit warning → emit `run_event` with `event_type='budget_exceeded'` (non-blocking; run proceeds).
- Step 4: Insert run row with `status='created'`.
- Step 5: Publish `domain_event` `run.created` with run_id in payload.
- Returns the newly created (or deduped existing) run.

**`RunService.StartRun(ctx, runID) error`:**
- Transitions run status: `created → in_progress` using optimistic concurrency (version check).
- Records `run_event(event_type='run_started', actor_type='system')`.
- Sets `started_at = now()`.

**`RunService.CompleteRun(ctx, runID, output?) error`:**
- Transitions: `in_progress → completed`.
- Sets `completed_at = now()`.
- Records `run_event(event_type='run_completed')`.
- Publishes `domain_event` `run.completed`.

**`RunService.FailRun(ctx, runID, reason, failureClass) error`:**
- Transitions: `in_progress | paused → failed` (or `timed_out` if `failureClass='timeout'`).
- Populates `failure_reason`, `failure_class`, `completed_at`.
- Records appropriate `run_event`.
- Publishes `domain_event` `run.failed`.

**`RunService.RequestCancel(ctx, runID, requestedBy) error`:**
- Transitions: `in_progress | paused → cancelling`.
- Records `run_event(event_type='run_cancelled', payload={requested_by})`.
- Publishes `domain_event` `run.cancellation_requested` — consumed by the active worker goroutine.
- If run is `created` (not yet started): immediately transition to `cancelled` (no worker to signal).

**`RunService.ConfirmCancelled(ctx, runID) error`:**
- Called by the worker after it finishes its current atomic unit.
- Transitions: `cancelling → cancelled`.
- Sets `completed_at = now()`.
- Records `run_event(event_type='run_cancelled')`.

**`RunService.PauseRun(ctx, runID) error`:**
- Transitions: `in_progress → paused`.
- Used by browser handoff flow (task 059): run suspends while waiting for human.
- Records `run_event(event_type='run_paused')`.

**`RunService.ResumeRun(ctx, runID) error`:**
- Transitions: `paused → in_progress`.
- Used when human completes browser handoff or other wait condition.
- Records `run_event(event_type='run_started', payload={resumed:true})`.
- Publishes `domain_event` `run.resumed`.

**Retry envelope:**
`RunService.CreateRetryAttempt(ctx, runStepID, trigger) (RunAttempt, error)`:
- Creates a new `run_attempt` row with `attempt_number = (latest attempt_number + 1)`.
- Never modifies the previous attempt row.
- `trigger` must be one of: `retry_transient`, `retry_policy`, `supervisor_recovery`.
- Max retry limits by domain (enforced here, not in individual workers):
  - `worker_type='mcp'` or `worker_type='native'` (external): max 3 attempts
  - `worker_type='cli'` or `worker_type='browser'`: max 2 attempts
  - `worker_type='internal'`: max 1 attempt (no retry)
- If max attempts exceeded: call `FailRun` with `failure_class='permanent'`, then transition to `dead_letter` via `RunService.DeadLetter(ctx, runID)`.
- Returns the new attempt; returns `ErrMaxAttemptsExceeded` if limit hit.

**`RunService.DeadLetter(ctx, runID) error`:**
- Transitions: `failed → dead_letter`.
- Records `run_event(event_type='run_failed', payload={dead_lettered:true})`.
- Calls `TaskService.CreateProjectTaskEvent` with `event_type='run_dead_lettered'`.
- Notifies PM via inbox item (calls `InboxService.CreateItem` with `item_type='escalation'`).

**Heartbeat emission:**
`RunService.EmitHeartbeat(ctx, runID, runAttemptID?) error`:
- Appends `run_event(event_type='heartbeat', payload={ts: now()})`.
- Must be called by workers every 30 seconds.
- Heartbeat silence thresholds: 90 seconds for sync runs; 5 minutes for async runs.
- `HeartbeatMonitor` (goroutine, part of Supervisor — see below) checks for silence.

**Run step service helpers:**
- `RunService.CreateStep(ctx, runID, toolName, toolTier) (RunStep, error)` — creates step with `step_number = (max+1)`, status=`pending`
- `RunService.StartStep(ctx, stepID) error` — transitions step `pending → in_progress`
- `RunService.CompleteStep(ctx, stepID) error` — transitions step `in_progress → completed`
- `RunService.FailStep(ctx, stepID, reason) error` — transitions step `in_progress → failed`

**`Supervisor`** (background process in `internal/controlplane/supervisor.go`):
- Runs as a long-lived goroutine started by the worker entrypoint.
- Poll interval: every 60 seconds.

**Stuck run detection** (`Supervisor.detectStuckRuns`):
- Query: `SELECT id FROM run WHERE status='in_progress' AND updated_at < now() - $heartbeat_grace` (90s sync, 5min async — use `metadata.run_mode` to distinguish).
- For each stuck run: check `run_event` for last heartbeat; if silence exceeds threshold → classify as stuck.
- Recovery cascade (up to 3 attempts per task per flow_node):
  1. Create a new run with `trigger_type='supervisor'` for the same task/flow_node.
  2. If 3 runs have already been attempted for this task+flow_node combo in `dead_letter` state → skip new run and proceed to blocker.
  3. File a blocker: call `InboxService.CreateItem(item_type='blocker', urgency='urgent')` targeted at the project's PM agent.
  4. If blocker has been open for >24h (normal) or >4h (urgent tasks) → escalate: create inbox item for human review.
- Paused runs: exempt from stuck detection.

**Orphaned run detection** (`Supervisor.detectOrphanedRuns`):
- Query: `SELECT id FROM run WHERE status='in_progress' AND NOT EXISTS (SELECT 1 FROM run_event WHERE run_id=run.id AND created_at > now() - interval '10 minutes')`.
- Orphaned = no events at all for 10 minutes while in_progress.
- Action: call `FailRun(id, "orphaned: no events for 10 minutes", "transient")` then attempt recovery same as stuck runs.

**Dead-letter notification** (`Supervisor.handleDeadLetter`):
- Triggered by consuming `domain_event` `run.failed` where payload contains `{dead_lettered:true}`.
- Creates `project_task_event` with `event_type='run_dead_lettered'`.
- Creates PM inbox item with `item_type='escalation'`, `urgency='urgent'`.
- Publishes `domain_event` `supervisor.escalation_created`.

**`run_event.actor_type='supervisor'` extension:**
- The supervisor records `run_event` rows with `actor_type='supervisor'` for all recovery actions.
- See ISSUE #20: supervisor actions cannot currently be recorded to `audit_event` (which only allows 'human_user'|'agent'|'system'). Supervisor recovery is tracked via `run_event` and `domain_event` only — not `audit_event`.

### Must NOT build

- Control plane API endpoints (task 054)
- Tool execution dispatch service (task 055)
- CLI execution (task 058)
- Browser execution (task 059)
- Schema/DDL (task 052)

## Acceptance Criteria

- [ ] `RunService.CreateRun` with a known `idempotency_key` returns the existing run (same ID) when called twice without failure in between
- [ ] `RunService.CreateRun` with a policy-denied principal creates a run row with `status='failed', failure_class='policy_denied'` (not silently dropped)
- [ ] `RunService.RequestCancel` on a `created` run (not yet started) immediately sets status to `cancelled` without waiting for a worker signal
- [ ] `RunService.CreateRetryAttempt` with `worker_type='internal'` and an existing attempt returns `ErrMaxAttemptsExceeded` on the second call
- [ ] `RunService.DeadLetter` creates a PM inbox item with `item_type='escalation'` and publishes `domain_event` `run.failed` with `dead_lettered:true`
- [ ] Supervisor `detectStuckRuns` identifies an in_progress run with no heartbeat for >90 seconds (sync) and initiates recovery
- [ ] Supervisor skips recovery after 3 dead-letter runs for the same `task_id+flow_node_id` and files a blocker inbox item instead
- [ ] `run_event` rows written by the supervisor carry `actor_type='supervisor'`

## Tests Required

**Unit tests:**
- `CreateRun` idempotency: call twice with same key; mock repo `GetByIdempotencyKey` returns existing run on second call → same run returned, no second insert
- `CreateRun` policy denied: mock `PolicyService.EvaluateRunCreation` returns deny → run inserted with `status='failed'`, error returned to caller
- `CreateRun` budget hard exceeded: mock `BudgetService.CheckBudget` returns hard exceeded → run inserted with `status='failed', failure_class='budget_exceeded'`
- `CreateRun` budget soft exceeded: mock returns soft warn → run created with `status='created'`, `run_event(budget_exceeded)` appended, no error returned to caller
- `RequestCancel` on `created` run: mock repo returns run with status='created' → status immediately set to 'cancelled'
- `CreateRetryAttempt` max attempts (internal=1): first call succeeds; second call returns `ErrMaxAttemptsExceeded` and calls `FailRun`
- `CreateRetryAttempt` max attempts (mcp=3): attempt 4 returns `ErrMaxAttemptsExceeded`
- State machine: `StartRun` on a `completed` run → returns `ErrInvalidTransition`
- Supervisor `detectStuckRuns`: mock runs with old `updated_at` and no recent heartbeat → recovery initiated

**Integration tests:**
- Full run lifecycle: CreateRun → StartRun → CreateStep → StartStep → CompleteStep → CompleteRun; verify all state transitions, run_event rows, domain_events published
- Retry envelope: CreateRun → fail → CreateRetryAttempt x3 → 4th call returns `ErrMaxAttemptsExceeded` → DeadLetter called → inbox item created
- Idempotency dedup: concurrent `CreateRun` calls with same key → exactly one run row created (use advisory lock or transaction isolation)
- Supervisor orphan detection: insert in_progress run with no recent events; run supervisor tick; run transitions to failed + recovery initiated

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

**Optimistic concurrency pattern:**
All status transitions use `UPDATE run SET status=$new, version=version+1, updated_at=now() WHERE id=$id AND version=$expected AND status=$expected_status RETURNING version`. If rows_affected=0, retry up to 3 times with exponential jitter before returning `ErrConflict`.

**Cancellation propagation:**
When `RequestCancel` is called, a `domain_event` `run.cancellation_requested` is published to the event bus. The active worker goroutine (task 055/058/059) must subscribe to this event and check it at each "atomic boundary" in its execution loop. Workers must NOT check for cancellation mid-operation (e.g. do not interrupt a running CLI process mid-stream); instead, check between tool calls. After completing the current atomic unit, the worker calls `RunService.ConfirmCancelled`.

**Supervisor as a consumer:**
The Supervisor subscribes to `run.failed` domain events to trigger dead-letter handling. It also runs a polling loop for stuck/orphaned detection. Both are in the same goroutine group. Use a `context.Context` from the worker entrypoint; the supervisor must shut down cleanly on context cancellation.

**Paused runs and the 24-hour timeout:**
Paused runs are exempt from the heartbeat silence check. However, a separate `detectStalePaused` check runs: if a run has been `paused` for >24 hours, the supervisor considers it stale and fails it with `failure_reason="paused timeout exceeded"`. This handles abandoned browser handoffs (task 059).

**Budget gate interaction (ISSUE #23 RESOLVED — hierarchical/additive):**
`BudgetService.CheckBudget(ctx, orgID, projectID, agentID, estimatedTokens)` checks all three levels in order:
1. **Org level**: check `token_budget` where `project_id IS NULL`; if hard limit exceeded → `Allowed=false, Level="org"`.
2. **Project level**: check `token_budget` where `project_id = run.ProjectID`; if hard limit exceeded → `Allowed=false, Level="project"`.
3. **Agent level**: check `agent.budget_cap_tokens` using `agent.budget_period` as the window; compute current-period usage via `model_invocation` sum for the agent; if exceeded → `Allowed=false, Level="agent"`.

After the invocation completes, call `BudgetService.RecordUsage(ctx, orgID, projectID, agentID, actualTokens)` to charge the token count against all three applicable levels simultaneously. The broker calls `RecordUsage` regardless of whether the pre-check used an estimate.

> ⚠️ ISSUE #17 (AMBIGUOUS): Policy evaluation for run creation must reject writes to `policy_layer='instance'` rows. Enforce at the API level in task 054, not in this service layer.
