# 076: Control Plane Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1-2 days) |
| Spec refs | doc 16 §RunLifecycle, doc 16 §PolicyEvaluation, doc 16 §RetryMatrix, doc 16 §DeadLetter, doc 16 §CapabilityGate, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 033, 034, 052, 053, 054, 055 |
| Blocks | 089 |

## Scope

Integration test suite for the control plane: run lifecycle (all 8 state transitions),
policy evaluation (allow/deny/escalate layers), retry decision matrix, dead-letter
promotion, capability gate enforcement, tool execution dispatch, and supervisor stuck
detection. All tests use a real PostgreSQL database via `testdb.New(t)`. Workers are
stubbed with in-process implementations that complete or fail on command.

### Must build

**Test file:** `internal/controlplane/run_integration_test.go`

**Test file:** `internal/controlplane/policy_integration_test.go`

**Test file:** `internal/controlplane/supervisor_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/controlplane.go`:
- `MakeRun(t, db, orgID, opts)` — creates a `run` row in 'created' status; returns `*run.Run`
- `MakeCapabilityPolicy(t, db, opts)` — creates a `capability_policy` row; returns policy
- `StubWorker(t, outcome)` — returns a `worker.Worker` that completes with the given outcome
  (success, transient_failure, permanent_failure, timeout)

**Test scenarios in run_integration_test.go:**

`TestRun_Lifecycle_FullSuccess` — create run; transition to in_progress; emit heartbeats
(every 30s via clock.Fake); complete; assert status='completed'; `run.completed_at` set;
`run_step` rows with correct step numbers; `run_event` rows for each state transition.

`TestRun_Lifecycle_AllStates` — exercise all 8 transitions from the run state machine:
created → in_progress → paused → in_progress → cancelling → cancelled; assert each
`run_event` row with correct `event_type`; final status persisted.

`TestRun_Lifecycle_TimedOut` — run starts; mock worker stops emitting heartbeats; advance
clock past heartbeat silence threshold (90s sync, 5min async); supervisor detects silence;
run transitions to 'timed_out'; `run_event` with actor_type='supervisor' written.

`TestRun_Lifecycle_DeadLetter` — run fails; retry max exhausted (domain: MCP/external=3,
internal=1); run promoted to 'dead_letter'; `project_task_event` with type='run_dead_letter'
written; PM inbox_item created.

`TestRun_Retry_NewAttempt` — run_step fails with `failure_class='transient'`; retry is
allowed (domain: MCP/external=3); new `run_attempt` row created (attempt_number++); old
attempt row preserved (never overwritten); eventual success on attempt 2 completes step.

`TestRun_Retry_PermanentFailure_NoRetry` — `failure_class='permanent'`; no new
run_attempt created; run transitions directly to failed; `failure_class` recorded on
attempt row.

`TestRun_Idempotency` — submit run creation with `idempotency_key`; submit identical
request again within 24h window; second request returns the existing run row (not a new
row); `idempotency_key` table has exactly 1 row for the key.

`TestRun_OptimisticConcurrency` — two goroutines attempt to transition the same run
concurrently; one wins (version matches); the other gets a conflict error; run status is
consistent (not a split-brain state).

`TestRun_GracefulCancellation` — run is in_progress; POST /v1/control/runs/:id/cancel;
run transitions to 'cancelling'; mock worker receives signal; worker completes atomically;
run transitions to 'cancelled'; `run_event` sequence is correct.

**Test scenarios in policy_integration_test.go:**

`TestPolicy_Eval_Allow` — create capability_policy with policy_layer='org', action=allow
for capability 'file.write'; agent requests file.write capability; broker evaluates; result
is allow; `tool_execution` row with `policy_decision='allow'` written.

`TestPolicy_Eval_Deny_Absolute` — create deny policy at org layer; create allow policy at
project layer for same capability; evaluate; deny wins (deny is absolute); result is deny
regardless of project-layer allow; `tool_execution.policy_decision='deny'`.

`TestPolicy_Eval_InstanceLayer_Unoverridable` — create allow policy at org layer for a
capability that has an instance-layer deny rule; evaluate; instance deny wins; org allow
does not override it; API returns 403 if org-admin attempts to write a policy_layer='instance'
row (application-layer guard).

`TestPolicy_Eval_Escalate` — policy returns 'escalate' result; run is paused; inbox_item
created for the appropriate reviewer; run resumes after inbox item acted with 'approve'.

`TestPolicy_Eval_Silence_Passes` — no policy rows for a capability; evaluation returns
allow (silence passes per spec); `tool_execution.policy_decision='allow'` written.

`TestPolicy_Eval_BudgetGate` — org has token_budget with hard limit exceeded; capability
gate check returns deny for non-essential capabilities; essential capabilities still pass;
verify the budget gate is checked before policy evaluation.

`TestPolicy_Eval_AgentAllowDenyList` — agent has `tool_allow_list=['file.read']` and
`tool_deny_list=['file.write']`; request for file.read: allow; request for file.write:
deny (deny list takes precedence over policy allow).

**Test scenarios in supervisor_integration_test.go:**

`TestSupervisor_StuckRun_Detection` — run in in_progress; mock worker emits no heartbeat;
advance clock past silence threshold; supervisor poll runs; run marked 'timed_out';
recovery: supervisor starts new run attempt (attempt 1 of max); `run_event` with
actor_type='supervisor'.

`TestSupervisor_OrphanedRun_Recovery` — run in in_progress with no events for
timeout+grace period; supervisor detects orphan; files blocker; `project_task_event`
with type='run_orphaned' written.

`TestSupervisor_MaxRecoveryAttempts` — supervisor has attempted recovery 3 times for a
task/flow_node combination; 4th stuck detection: supervisor does NOT start a new run;
instead promotes to dead_letter and escalates to PM.

`TestSupervisor_PausedRun_Exempt` — run in 'paused' state; advance clock well past
normal silence threshold; supervisor poll runs; paused run NOT flagged as stuck (paused
runs have 24h default timeout before supervisor intervenes).

### Must NOT build

- E2E tests for control plane (task 087)
- Tool implementation tests (tasks 056, 057) — those test tool logic; this tests dispatch
- Browser and CLI execution tests (task 078)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/controlplane/... -tags integration`
- [ ] `TestRun_Lifecycle_AllStates` exercises all 8 run status values and asserts the correct `run_event` is written for each
- [ ] `TestPolicy_Eval_Deny_Absolute` verifies deny wins at every layer combination (at least 3 layer pairs tested)
- [ ] `TestRun_Retry_NewAttempt` asserts the old attempt row is NOT modified (never overwrite semantics)
- [ ] `TestRun_Idempotency` asserts the `idempotency_key` table has exactly 1 row after 2 identical requests
- [ ] `TestSupervisor_MaxRecoveryAttempts` verifies the 3-attempt cap is enforced per (task, flow_node) pair
- [ ] `TestPolicy_Eval_InstanceLayer_Unoverridable` tests the API-layer guard (HTTP 403 response, not just service error)
- [ ] All `run_event` rows with actor_type='supervisor' are verified in supervisor tests (per ISSUE #20 note below)

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestRun_Lifecycle_FullSuccess`
- `TestRun_Lifecycle_AllStates`
- `TestRun_Lifecycle_TimedOut`
- `TestRun_Lifecycle_DeadLetter`
- `TestRun_Retry_NewAttempt`
- `TestRun_Retry_PermanentFailure_NoRetry`
- `TestRun_Idempotency`
- `TestRun_OptimisticConcurrency`
- `TestRun_GracefulCancellation`
- `TestPolicy_Eval_Allow`
- `TestPolicy_Eval_Deny_Absolute`
- `TestPolicy_Eval_InstanceLayer_Unoverridable`
- `TestPolicy_Eval_Escalate`
- `TestPolicy_Eval_Silence_Passes`
- `TestPolicy_Eval_BudgetGate`
- `TestPolicy_Eval_AgentAllowDenyList`
- `TestSupervisor_StuckRun_Detection`
- `TestSupervisor_OrphanedRun_Recovery`
- `TestSupervisor_MaxRecoveryAttempts`
- `TestSupervisor_PausedRun_Exempt`

**E2E tests:** None — covered by task 087.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Workers: `StubWorker` in-process implementations (complete/fail on command via channel)
- Clock: injected `clock.Fake` for heartbeat silence and stuck detection timing
- Domain event bus: real (in-process dispatch)
- Model gateway: not involved in control plane dispatch tests

**ISSUE #17 (instance policy write-protection):**
`TestPolicy_Eval_InstanceLayer_Unoverridable` tests the API-layer guard: a request by
an org-admin to POST a policy with policy_layer='instance' returns 403. This is
application-layer enforcement only (no DB constraint). Test verifies the handler-level
check via HTTP response code.

**ISSUE #20 (supervisor actor_type in audit_event):**
Supervisor-generated `run_event` rows use actor_type='supervisor'. The `audit_event`
table does NOT support 'supervisor' (ISSUE #20 unresolved). Supervisor tests verify
`run_event` rows are written but do NOT assert `audit_event` rows for supervisor actions.
Add a comment: `// TODO(issue-20): supervisor actions cannot be logged to audit_event.`

**ISSUE #23 (RESOLVED — hierarchical/additive budget enforcement):**
`TestPolicy_Eval_BudgetGate` tests the full three-level check:
- Seed `token_budget` org row with hard limit exhausted → non-essential capabilities denied; essential capabilities (filing blockers, notifying operator) still allowed.
- Also verify that per-agent cap (`agent.budget_cap_tokens`) causes deny when the agent's period usage is at/above the cap, even if org/project have remaining headroom.
- Verify `BudgetService.RecordUsage` charges the invocation token count to all three applicable levels simultaneously: assert org usage, project usage, and agent period usage each increment by the correct amount after a completed invocation.

**Run state machine optimistic concurrency:**
`TestRun_OptimisticConcurrency` requires both goroutines to attempt the state transition
at the same time. Use a sync.WaitGroup + channel to coordinate goroutine start, ensuring
both see the same `version` value before the first UPDATE runs.
