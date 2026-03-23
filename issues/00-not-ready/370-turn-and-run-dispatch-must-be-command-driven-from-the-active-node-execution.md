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
