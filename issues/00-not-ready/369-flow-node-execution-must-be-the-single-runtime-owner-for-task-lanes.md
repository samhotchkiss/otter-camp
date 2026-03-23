# 369: flow_node_execution must be the single runtime owner for task lanes

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | L |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 359 |

## Problem

The current runtime still allows task truth to split across:

- `project_task.work_status`
- `flow_node_execution.status`
- `chat_session.current_turn_id`
- `runtime_state`
- `run`
- `job_queue`

The result is recurring stale-owner, stale-turn, stale-dispatch, and recovery-resume bugs that the worker and supervisor have to repair after the fact.

That violates the intended product model. A task lane should be governed by its active flow-node execution, with session/turn/run ownership hanging off that execution boundary.

## Scope

### Must build

- Define a canonical execution ownership contract for a task lane.
- Make the active `flow_node_execution` the authoritative runtime owner for:
  - bound execution session
  - current turn
  - active run
  - resumable runtime disposition
- Constrain queue/start/resume logic to operate through that execution owner instead of inferring truth from broader task/session state.
- Ensure flow advance/reject creates a fresh ownership boundary for the new node execution.

### Must NOT build

- Another broad worker/supervisor cleanup pass without changing execution ownership.
- Direct task-row repair writes that bypass the canonical flow/task transition services.
- A design where task-scoped async sessions remain the primary owner across multiple node executions.

## Acceptance Criteria

- [ ] A live task lane has one authoritative runtime owner anchored to the active `flow_node_execution`.
- [ ] Session, current turn, and active run identity can be resolved from that execution owner without broad cross-table inference.
- [ ] Flow advance and flow reject move ownership cleanly to the new node execution without inheriting stale live ownership from the prior node.
- [ ] Worker/supervisor recovery no longer needs broad stale-turn and stale-dispatch inference just to determine which execution owns the lane.

## Tests Required

**Integration tests:**
- advancing a task from work -> review creates a new execution ownership boundary and does not reuse stale prior-node ownership
- rejecting a review node creates a fresh work-node execution with fresh runtime ownership
- a stale prior run/turn cannot reopen or overwrite the newer active execution owner

## Implementer Notes

- This issue comes directly from the 2026-03-23 architecture review in `reports/2026-03-23-flow-execution-architecture-review.md`.
- The purpose is to remove structural drift, not just patch another stranded-execution symptom.
