# Flow Execution Architecture Review

Date: 2026-03-23

## Purpose

This note captures the architectural conclusion after the SAM.blog hardening run and the current Speaker Pipeline canary work.

The system has improved materially, but the recurring stalls are no longer best understood as isolated bugs. They are a structural consequence of runtime ownership being split across too many overlapping state systems.

The goal of the next phase is not to abandon the current flow model. It is to make the implementation finally obey that model.

## Intended Product Model

The intended product model is straightforward:

- every task has a flow template
- the task has one current node in that flow
- the current node determines the current actor and legal actions
- execution keeps advancing node-by-node until it hits:
  - another executable node
  - a defined human-review or human-approval node
  - or a terminal node

Everything else should be subordinate machinery.

That means:

- `flow_node_execution` should be the runtime anchor for task-lane execution
- session ownership should belong to the active flow-node execution
- turn ownership should belong to that execution session
- queue dispatch should execute or resume the active node
- recovery should resume the same node execution from a structured checkpoint
- `project_task.work_status` should be a constrained projection of node/runtime state, not an independent competing state machine

## Current Structural Problem

Today, the system still has multiple partially-authoritative notions of "what is live":

- `project_task.work_status`
- `project_task.current_flow_node_id`
- `flow_node_execution.status`
- `chat_session.status` and `current_turn_id`
- `chat_turn.status`
- `runtime_state`
- `run.status`
- `job_queue` presence/absence

Those layers often need to be reconciled after the fact. When they diverge, the worker and supervisor perform repair rather than simply advancing the canonical flow state machine.

## Evidence From The Code

### 1. Worker startup and watchdog logic are doing broad state repair

`internal/jobqueue/worker.go` has multiple startup purge/requeue paths for stale dispatch, duplicate dispatch, synthetic continuation churn, supervisor recovery churn, pending turns without jobs, and active execution sessions without turns.

That logic is valuable as a safety net, but its current size is evidence that the core runtime model is emitting contradictory states too often.

### 2. Execution ownership is modeled separately from flow execution

`internal/controlplane/execution_wakeup.go` introduces a second coordination layer:

- started
- coalesced
- deferred
- promoted

paired with `runtime_state` as the mutable owner record.

This is useful for concurrency control, but today it behaves like a parallel execution model rather than a thin coordination wrapper around `flow_node_execution`.

### 3. The supervisor must reconstruct truth by joining many tables

`internal/controlplane/stranded_execution.go` determines whether execution is stranded by combining:

- `flow_node_execution`
- `project_task`
- `project`
- `chat_session`
- `chat_turn`
- `runtime_state`
- `run`
- `job_queue`

The need for that wide inference query is the clearest sign that execution truth is split.

### 4. Recovery is too heuristic-heavy

`internal/task/recovery_resume.go` and `internal/turn/engine.go` currently rebuild or infer resumable state from combinations of:

- task metadata
- recovery artifacts on disk
- prior chat messages
- prior drafts
- current assistant content
- session summaries

That approach was necessary to salvage live runs, but it is too permissive and too indirect. It is the main reason repeated placeholder, intent-only, and wrong-target recovery loops kept resurfacing in new shapes.

### 5. Prompts are still compensating for missing runtime contract surfaces

The current review-lane prompt instructs the model to use `flow.review_decision` with the active `flow_node_execution_id`, but the task context still does not reliably surface that ID in the prompt body. The model is being asked to reason around a missing runtime affordance instead of being given the exact execution handle it needs.

That is a symptom of the broader issue: prompts are still making up for incomplete product contracts.

## Architectural Conclusion

The next phase should be a focused execution-architecture rework, not another long stretch of purely reactive live-run bug fixing.

This does not mean rewriting OtterCamp from scratch.

It means tightening the system around the model the specs already describe:

- flow execution is the primary state machine
- work/review/bootstrap/orchestration are explicit task modes with explicit allowed operations
- queueing and recovery are servants of that state machine, not parallel systems

## Target Runtime Model

### 1. `flow_node_execution` becomes the authoritative runtime owner for a task lane

For any executing task lane, there should be exactly one authoritative runtime anchor:

- active `flow_node_execution`
- bound execution session
- current turn, if any
- active run, if any
- recovery checkpoint, if any

These should all hang off the active node execution rather than being inferred from the broader task/session surface.

### 2. Session and turn identity are per node execution

Each executable node execution owns:

- its execution session
- the current responder
- the current active turn

Advancing or rejecting a flow creates a new node execution and therefore a new execution ownership boundary. The runtime should not silently keep reusing a broader task-scoped async session as if the node were incidental.

### 3. Queue items become explicit execution commands

The queue should carry intent like:

- execute current node
- resume current node
- continue current node after tool result
- advance after review decision

It should not need to infer whether to reopen, resume, promote, or synthesize a continuation from loosely-related state.

### 4. Recovery uses structured checkpoints only

Recovery should resume from a small structured checkpoint tied to the active node execution:

- checkpoint class
- target artifact path
- last known good draft or artifact reference
- blocker reason
- resumable action

If those fields are not present, the turn should fail and surface a product bug. It should not hunt across prior assistant messages and workspace artifacts trying to guess the right next write.

### 5. `work_status` becomes a projection, not a second controller

`draft`, `queued`, `in_progress`, `review`, `blocked`, `done`, `cancelled` are still useful operator-facing states, but they should be tightly derived from flow/runtime truth.

The system should not allow `work_status` to drift independently from:

- whether a flow execution exists
- whether the current node is executable, review, or terminal
- whether the lane is waiting on a human gate

## Refactor Sequence

### Slice 1: Define the execution contract

Write and agree on a short implementation contract for a task lane:

- what table is authoritative
- what transitions are legal
- what component owns each transition
- what must be updated atomically

This should explicitly cover:

- task execution node
- review node
- human-review gate
- blocked/resumable recovery
- terminal completion

### Slice 2: Bind runtime ownership to node execution

Refactor control-plane and queue code so the active node execution owns:

- session id
- current turn id
- active run id
- resumable checkpoint id or payload

`runtime_state` may still exist, but only as a coordination cache over the node execution contract, not as an additional truth source.

### Slice 3: Replace heuristic recovery with checkpoint recovery

Stop recovering from "best available draft".

Instead:

- persist the checkpoint during the failing turn
- resume only from that checkpoint
- fail closed if the checkpoint is missing or malformed

### Slice 4: Make mode restrictions product-native

Do not rely on prompts to keep bootstrap/review/orchestration sessions honest.

Each mode should have explicit allowed mutations:

- review nodes cannot write deliverables
- execution-first tasks cannot drift back into planning artifacts when the deliverable path is explicit
- bootstrap nodes cannot pretend setup is complete without persisted first-wave execution
- orchestration parents cannot do child production work when executable children exist

### Slice 5: Shrink the worker repair surface

Once the above is in place, the worker should be simplified.

The goal is to delete large classes of startup/watchdog repair logic, keeping only a thin safety net for:

- crashed workers
- lost claims
- stale long-running model invocations

## Concrete Rework Priorities

1. Make `flow_node_execution` the single runtime owner for task-lane execution.
2. Collapse duplicated turn/run ownership into that execution boundary.
3. Replace heuristic recovery draft selection with checkpoint-only recovery.
4. Tighten `work_status` so it cannot contradict flow/runtime truth.
5. Resume canary validation only after those ownership rules are in place.

## What Not To Do

- Do not keep treating each new stalled lane as a one-off prompt bug first.
- Do not add more draft-shape heuristics before fixing checkpoint authority.
- Do not let queue/watchdog cleanup continue to grow as the main consistency mechanism.
- Do not reintroduce direct task-row writes as a shortcut around flow/task transition services.

## Immediate Next Step

Turn this review into implementation work:

- one issue for execution ownership
- one issue for turn/run dispatch unification
- one issue for checkpoint-only recovery

Those should be completed before the next long unattended validation cycle becomes the primary focus again.
