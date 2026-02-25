# 060: Tool Execution Audit, Retry Logic, and Run Event Fan-Out

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 16 §RetryEnvelope, doc 16 §DeadLetter, doc 16 §RunEventFanOut, doc 16 §ToolExecutionAudit, doc 12 §DomainEvents |
| Spec status | finished |
| Depends on | 052, 053, 055, 024, 047 |
| Blocks | 077, 087 |

## Scope

Complete the tool execution audit trail: ensure every `tool_execution` record has full
lifecycle coverage; implement retry logic for transient failures (including dead-letter
promotion); wire `run_event` fan-out to the SSE event bus and domain event stream; and
implement the control plane's contribution to the event bus domain event taxonomy.

### Must build

**Retry decision engine** (`internal/controlplane/retry.go`):

`RetryDecider.ShouldRetry(te ToolExecution, attempt RunAttempt) (bool, RetryTrigger)`:
- Returns `(true, retry_transient)` if:
  - `attempt.failure_class == 'transient'`
  - AND `attempt.attempt_number < MaxAttemptsForDomain(te.tool_domain)`
  - AND the failure was not caused by a policy denial (`failure_class != 'policy_denied'`)
- Returns `(false, "")` if:
  - `attempt.failure_class == 'permanent'`
  - OR `attempt.failure_class == 'policy_denied'`
  - OR `attempt.failure_class == 'budget_exceeded'`
  - OR `attempt.attempt_number >= MaxAttemptsForDomain(te.tool_domain)`
- `MaxAttemptsForDomain`:
  - `'mcp'` → 3
  - `'browser'` → 2
  - `'cli'` → 2
  - `'native'` → 1 (no retry for native tools — they are deterministic)
  - `'internal'` → 1

`RetryDecider.ClassifyFailure(err error) FailureClass`:
- `FailureClass` is one of: `transient`, `permanent`, `policy_denied`, `budget_exceeded`, `timeout`
- Classification rules:
  - Context deadline exceeded, network timeout → `transient`
  - HTTP 5xx from MCP server → `transient`
  - HTTP 4xx from MCP server (except 429) → `permanent`
  - HTTP 429 (rate limit) → `transient` (with backoff)
  - `ErrCapabilityDenied`, `ErrAgentDenyList` → `policy_denied`
  - `ErrBudgetExceeded` → `budget_exceeded`
  - `ErrCircuitOpen` → `transient`
  - `ErrSessionRevoked`, `ErrDomainPolicyDenied` → `permanent`
  - Unknown/unclassified → `permanent` (fail safe: do not retry unknown errors)

**Retry backoff:**
- For `transient` failures requiring retry, the caller (task 053 `RunService.CreateRetryAttempt`) applies exponential backoff before re-dispatching:
  - Attempt 1 → immediately
  - Attempt 2 → wait `2^1 * base_delay` (base = 2 seconds; jitter ±50%)
  - Attempt 3 → wait `2^2 * base_delay`
- Backoff is implemented in the worker goroutine that consumes the retry trigger (not in the DB).
- HTTP 429 responses: use `Retry-After` header value if present; otherwise standard backoff.

**Dead-letter handling:**
`DeadLetterHandler.Handle(ctx, runID, reason) error`:
- Called by `RunService.DeadLetter` (task 053) after max retries exceeded.
- Actions:
  1. Emit `domain_event(event_type='run.dead_lettered', payload={run_id, task_id, failure_class, attempt_count})`.
  2. Create `project_task_event(event_type='run_dead_lettered', source_run_id=runID)` via `TaskService`.
  3. Create PM inbox item: `inbox_item(item_type='escalation', urgency='urgent', summary='Run dead-lettered after N attempts', action_payload={run_id, failure_class, last_error})` via `InboxService.CreateItem`.
  4. Emit `run_event(event_type='run_failed', payload={dead_lettered:true, reason})` via `RunEventRepository.Append`.
- Returns nil on success; non-fatal errors (e.g. inbox creation failure) are logged but do not cause this function to return an error.

**`tool_execution` audit trail completeness:**
The following invariants must hold for every `tool_execution` row:
1. Every tier 2 tool call creates a `tool_execution` row, regardless of outcome (policy denied, error, or success).
2. `policy_decision` must be `'denied'` for any call that did not pass the capability gate; `'allowed'` for any that did; `'not_checked'` for tier 1 calls.
3. `started_at` is always set when `status` transitions from `pending → in_progress`.
4. `completed_at` is always set when status transitions to a terminal state (`completed|failed|policy_denied|timed_out`).
5. `duration_ms` = `EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000` — computed and stored.

Implement `ToolExecutionAuditVerifier.Verify(ctx, runID) AuditReport` — a diagnostic helper (not called in hot path) that scans `tool_execution` rows for a given run and reports any violations of the above invariants. Called by integration tests and the control plane health endpoint.

**`run_event` fan-out to SSE:**
Wire `run_event` rows to the SSE event bus (task 047) so that the `GET /v1/control/runs/:id/events/stream` endpoint (task 054) can push them to clients in real time.

Implementation:
- After every `RunEventRepository.Append` succeeds, publish a PostgreSQL `NOTIFY` on channel `run_events_<run_id>` with the `run_event` JSON payload as the notification body.
- The SSE handler for `GET /v1/control/runs/:id/events/stream` subscribes to `LISTEN run_events_<run_id>`.
- Use the existing LISTEN/NOTIFY infrastructure from task 047 (same connection pool management).
- On reconnect (`Last-Event-ID` header): replay missed events from `RunEventRepository.ListByRun(fromSequence)` before switching to live LISTEN mode.

**Domain event taxonomy for control plane:**
The following domain events must be emitted by the control plane service (task 053) and
this task adds the explicit event type strings to the `domain_event.event_type` vocabulary:
- `run.created` — emitted by `RunService.CreateRun` on success
- `run.started` — emitted by `RunService.StartRun`
- `run.completed` — emitted by `RunService.CompleteRun`
- `run.failed` — emitted by `RunService.FailRun`; payload includes `{dead_lettered?: boolean}`
- `run.cancelled` — emitted by `RunService.ConfirmCancelled`
- `run.cancellation_requested` — emitted by `RunService.RequestCancel`; consumed by active worker
- `run.paused` — emitted by `RunService.PauseRun`
- `run.resumed` — emitted by `RunService.ResumeRun`
- `run.dead_lettered` — emitted by `DeadLetterHandler`
- `run.supervisor.recovery_initiated` — emitted by `Supervisor.detectStuckRuns`

These event types are added to the `domain_event` table's `event_type` vocabulary. They are
stored as-is in `domain_event.event_type`; no enum constraint on the DB column (varchar, open
vocabulary as per task 024 design).

**`tool_execution` → `domain_event` fan-out:**
For `policy_decision='denied'` tool execution rows: emit `domain_event(event_type='tool.capability_denied', payload={tool_name, capability, agent_id, run_id})` so the audit/observability layer (task 063) can track capability denials without scanning `tool_execution` directly.

### Must NOT build

- New DB tables (schema is in task 052)
- Run service state machine (task 053)
- SSE infrastructure (task 047) — this task is a consumer/wirer, not an implementer
- New CLI commands
- Actual MCP/CLI/browser retry execution (retry dispatch happens in the workers in tasks 055/058/059 after calling `RunService.CreateRetryAttempt`)

## Acceptance Criteria

- [ ] `RetryDecider.ShouldRetry` returns `false` for `failure_class='policy_denied'` regardless of attempt count
- [ ] `RetryDecider.ShouldRetry` returns `false` for `native` domain tool on attempt 2 (max=1)
- [ ] `RetryDecider.ShouldRetry` returns `true` for `mcp` domain transient failure on attempt 2 (max=3)
- [ ] `RetryDecider.ClassifyFailure(context.DeadlineExceeded)` → `transient`
- [ ] `RetryDecider.ClassifyFailure(ErrCapabilityDenied)` → `policy_denied`
- [ ] `DeadLetterHandler.Handle` emits `domain_event(run.dead_lettered)`, creates PM inbox item, appends `run_event`; inbox creation failure is non-fatal (logs warning, returns nil)
- [ ] Every `tool_execution` row for tier 2 calls has `started_at` and `completed_at` populated in terminal state
- [ ] `run_event` NOTIFY is published on `run_events_<run_id>` channel after every successful `RunEventRepository.Append`
- [ ] SSE reconnect with `Last-Event-ID=5` replays events with `sequence > 5` before switching to live NOTIFY stream

## Tests Required

**Unit tests:**
- `RetryDecider.ShouldRetry`: 6 cases covering all `failure_class` values and domain max attempt limits
- `RetryDecider.ClassifyFailure`: HTTP 429 → `transient`; HTTP 400 → `permanent`; `ErrCircuitOpen` → `transient`; unknown error → `permanent`
- `DeadLetterHandler.Handle`: mock `DomainEventBus.Publish` and `InboxService.CreateItem`; verify both called; mock inbox failure → function still returns nil
- Audit invariant: `ToolExecutionAuditVerifier.Verify` on a set of tool_execution rows with one missing `completed_at` → report identifies the violation

**Integration tests:**
- NOTIFY fan-out: call `RunEventRepository.Append`; verify `NOTIFY` received on `run_events_<run_id>` channel (use `pg.Listen` in test)
- SSE reconnect replay: insert 10 `run_event` rows for a run; connect SSE with `Last-Event-ID=5`; verify events 6–10 are replayed before live stream starts
- Dead-letter cascade: mock run reaching max retries; verify `domain_event`, `project_task_event`, `inbox_item` all created in single transaction

**E2E tests:**
- None — covered by dedicated E2E task 087
