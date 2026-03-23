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
- chat-layer `chat.message.user_sent` events for bound task sessions now also carry `flow_node_execution_id` directly

That means execution identity now survives:

- worker stale triggered-turn recovery
- worker stale continuation-turn recovery
- worker pending-turn requeue
- worker stranded supervisor-recovery requeue
- turn-engine task retries / auto-continuations
- worker startup purge of legacy task-lane queue rows that predate execution-bound payloads
- normal task-lane message-trigger dispatch from `chat.message.user_sent`

The remaining gap is that several primary dispatch producers still create task-lane work from message/session state without routing through one execution-scoped command helper. The next slice should eliminate those remaining raw producers rather than adding another generation of suppression heuristics.

Latest slice completed:

- `HandleUserMessageEvent(...)` now refuses the legacy raw `agent_turn` fallback for execution-owned task events when the session cannot be loaded.
- That removes another shadow dispatch path where `project_task` work could be recreated from `session_id/message_id` alone even though the event already carried `flow_node_execution_id`.
- Focused turn-engine coverage now asserts that execution-owned task events with a missing session no-op instead of enqueueing an orphan job.
- `chat.SteerTurn(...)` now also includes `flow_node_execution_id` in the replacement `chat.message.user_sent` payload for bound task sessions.
- Focused chat integration coverage now asserts steered task-session user events remain execution-owned instead of falling back to message-only dispatch identity.
- synthetic continuation/recovery user prompts appended by the turn engine now also inherit `flow_node_execution_id` from the bound task session metadata.
- Focused turn-engine coverage now asserts both continuation-root and synthetic recovery metadata keep execution ownership when present.
- worker pending-turn repair now ignores failed execution `live_turn_id` pointers when selecting the authoritative pending turn to requeue.
- Focused jobqueue integration coverage now asserts a stale failed `live_turn_id` can no longer mask a real pending `current_turn_id`.
- terminal `project_task` async session cleanup now runs in the worker’s periodic maintenance loop, not only on startup.
- Focused worker integration coverage now asserts a terminal task session closes automatically during steady-state worker operation.
- legacy `assigned_task` dispatch now no-ops for any task that already has a `flow_template_id` or `current_flow_node_id`.
- that trims another shadow control-plane path where a stale generic task-session wakeup could still be emitted or dispatched even though the lane already belonged to a `flow_node_execution`.
- focused control-plane unit coverage now asserts both the producer (`ensureAssignedAgentRun`) and dispatcher (`dispatchTaskQueueWakeup`) skip `assigned_task` for flow-owned tasks.
- recovery checkpoint reconciliation now prefers task-local historical target-path hints even when the earlier draft content was unusable, instead of only preferring historical targets when a substantive draft survived.
- focused turn-engine coverage now asserts:
  - a generic poisoned checkpoint like `deliverables/oc-11-task-summary.md` is overridden by the task-local historical workflow-spec target path
  - a poisoned planning-artifact checkpoint like `planning/prd-spec/oc-12-acceptance-criteria.md` is overridden by the task-local historical validation-report target path
- live validation on `speaker-pipeline-ops-validation-fresh-5` confirmed the new behavior:
  - task `11` resumed from `blocked` and retargeted onto `deliverables/oc-11-validation-workflow-spec.md`
  - task `12` completed its work lane and advanced into `review` after the fresh session reused `planning/prd-spec/oc-12-validation-report.md` instead of the old acceptance-criteria checkpoint
- `AdvanceFlow(...)` now hydrates missing required planning evidence before entering review by persisting synthesized `planning.artifact_evidence` back onto the task record when partial evidence already exists.
- focused `internal/flow` integration coverage now reproduces the live task-15 failure shape:
  - enforced discovery plan
  - all required discovery artifacts already persisted with `artifact_id` / `content_sha256`
  - one unrelated extra evidence entry already present
  - flow advance must still succeed and persist the four missing required evidence rows before moving to review
- live validation on `speaker-pipeline-ops-validation-fresh-5` confirmed task `15` no longer blocks on `planning artifact contract is incomplete` once the review-boundary hydration slice is deployed.
