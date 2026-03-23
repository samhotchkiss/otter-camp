# 370: Turn and run dispatch must be command-driven from the active node execution

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | L |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 369 |

## Problem

Queueing and execution wakeups currently rely on a mix of:

- task status events
- flow-advanced events
- synthetic recovery/continuation messages
- runtime-state deferred wakeups
- worker-side stale dispatch cleanup

That makes dispatch inference-heavy and hard to reason about. The worker often has to repair stale or duplicated `agent_turn` jobs because the system does not have one explicit command path for "execute the current node" or "resume the current node".

## Scope

### Must build

- Define explicit execution commands for task lanes, at minimum:
  - execute current node
  - resume current node
  - continue current node after tool result
  - dispatch review decision follow-on
- Make queue items and wakeups target the active node execution directly.
- Remove or sharply reduce synthetic continuation/recovery dispatches whose purpose is to infer the next action from chat/session state.
- Ensure retry/cancel/release paths operate on the same command model.

### Must NOT build

- Another layer of duplicate-dispatch suppression without simplifying dispatch ownership.
- More synthetic user messages as the primary mechanism for task-lane continuation.
- A design where stale `chat_message` rows are still the main source of execution truth.

## Acceptance Criteria

- [ ] Executing or resuming a task lane is represented by an explicit command against the active node execution.
- [ ] Duplicate dispatch suppression becomes a narrow idempotency layer, not a broad stale-state reconciliation system.
- [ ] Worker startup no longer needs multiple special-case purges just to keep task-lane dispatch coherent.
- [ ] Review-lane and work-lane dispatch both use the same command model.

## Tests Required

**Integration tests:**
- duplicate execute/resume commands for the same active node execution coalesce idempotently
- cancelling a live command prevents older queued dispatch from reviving the lane
- flow advance emits the correct next command exactly once

## Implementer Notes

- This issue should follow the ownership contract from issue `369`.
- The target is a command-driven runtime, not another generation of cleanup heuristics.

### Current producer map

The repo currently produces or repairs `agent_turn` dispatch from multiple places:

- `internal/controlplane/task_queue_processor.go`
  - `ensureFlowRun(...)` creates execution wakeups from task-queued / flow-advanced state
  - `dispatchWakeupRun(...)` appends kickoff messages that then become `agent_turn` work
- `internal/jobqueue/worker.go`
  - `RequeueStrandedSupervisorRecoveryTurns(...)`
  - `RequeueStrandedUserMessageTurns(...)`
  - `RequeuePendingTurnsWithoutJobs(...)`
  - `RequeueActiveExecutionSessionsWithoutTurns(...)`
  - `RecoverStaleInProgressContinuationTurns(...)`
  - `RecoverStaleInProgressTriggeredTurns(...)`
  - startup purge / dedupe paths in `PurgeStaleAgentTurnJobs(...)`
- `internal/chat/service.go`
  - async message append / resume flows can enqueue turns from message state
- `internal/turn/engine.go`
  - completion/recovery flows still assume synthetic continuation / retry messages are part of the dispatch surface

That means dispatch is still inferred from message/session/job state after the fact, not issued from one explicit execution command boundary.

### First implementation slice

The first cut for this issue should be deliberately narrow:

- define an execution-lane dispatch command record or helper keyed by `flow_node_execution_id`
- route `task_queue_processor` wakeup dispatch and worker requeue/recovery through that command boundary instead of calling `Enqueue(... agent_turn ...)` ad hoc
- make worker repair paths reissue `resume current execution` commands, not directly synthesize message-based `agent_turn` jobs

### Success signal for the first cut

After the first cut lands, at least these worker paths should no longer enqueue raw `agent_turn` jobs directly:

- `RequeuePendingTurnsWithoutJobs(...)`
- `RecoverStaleInProgressContinuationTurns(...)`
- `RecoverStaleInProgressTriggeredTurns(...)`

Those should become command reissuers against the active `flow_node_execution`, with message/session lookup only as supporting data instead of the primary dispatch identity.

### Current progress

This issue is partially underway, but not complete:

- worker recovery/requeue paths now stamp `flow_node_execution_id` into reissued task-lane `agent_turn` payloads
- claim-time suppression now uses active execution presence to distinguish valid live ownership from stale `chat_session.current_turn_id`
- turn-engine-created retries and auto-continuations now also stamp `flow_node_execution_id` from task-session metadata before enqueue

That means execution identity now survives:

- worker stale triggered-turn recovery
- worker stale continuation-turn recovery
- worker pending-turn requeue
- worker stranded supervisor-recovery requeue
- turn-engine task retries / auto-continuations

The remaining gap is that several primary dispatch producers still create task-lane work from message/session state without routing through one execution-scoped command helper. The next slice should eliminate those remaining raw producers rather than adding another generation of suppression heuristics.
