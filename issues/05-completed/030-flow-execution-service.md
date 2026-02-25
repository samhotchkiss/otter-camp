# 030: Flow Execution Service

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 03 §FlowExecution, doc 03 §FlowAdvancement, doc 03 §RejectionLoop, doc 03 §ProjectSubtask, doc 03 §DependencyDAG |
| Spec status | finished |
| Depends on | 029, 028, 024 |
| Blocks | 032, 060 |

## Scope

Build the flow execution service: flow advancement logic (agent-signalled, not auto-advance),
rejection loops (new execution row + visit counter), commit SHA recording, subtask CRUD with
sequential enforcement, and dependency DAG validation (cycle rejection + cancelled dependency
handling). Does not include the chat session creation for flow nodes (task 060) or API
endpoints (task 032).

### Must build

**`internal/flow/execution_service.go` — `FlowExecutionService`:**

**Flow start (`StartFlow`):**
- Called when a task transitions to `in_progress` and has a `flow_template_id`
- Set `project_task.current_flow_node_id` to the template's `start_node_id`
- Create a `flow_node_execution` row with `status='active'`, `visit_number=1`
- Publish `flow.started` domain event
- Return the created `flow_node_execution`

**Flow advancement (`AdvanceFlow`):**
- Triggered by agent calling the `flow.advance` native tool (NOT automatic on run completion)
- Locate the active `flow_node_execution` for the task
- If the current node has `requires_human_review=true` AND the task is not yet in `review` status: block advancement, transition task to `review`, create inbox item of type `task_review` — agent must wait for human action
- If no `requires_human_review` or review already cleared:
  - Mark current `flow_node_execution` as `completed` (set `completed_at`)
  - Advance `project_task.current_flow_node_id` to `flow_node.next_node_id`
  - If `next_node_id` IS NULL: task has reached the terminal node; transition `work_status → done`
  - Else: create new `flow_node_execution` for the next node with `visit_number=1`
- Record `flow.advanced` task event
- Publish `flow.advanced` domain event

**Flow rejection (`RejectFlowNode`):**
- Triggered by human rejecting a review node, or agent calling `flow.reject` tool
- Locate the active `flow_node_execution` for the task
- Mark current execution as `rejected` (set `completed_at`, `status='rejected'`)
- If current node has NO `reject_node_id`: rejection is not supported on this node — return `ErrNoRejectionPath`
- If current node `reject_node_id` IS NOT NULL:
  - Advance `current_flow_node_id` to `reject_node_id`
  - Create new `flow_node_execution` for the reject node:
    - If the reject node has been visited before (any prior `flow_node_execution` row with same `flow_node_id` + same `task_id`): `visit_number = MAX(prior visit_number) + 1`
    - If this is the first visit: `visit_number = 1`
  - If new `visit_number > flow_node.max_visits`: do NOT create the execution; instead transition task to `blocked` and create a `blocker_filed` inbox item; return `ErrMaxVisitsExceeded`
- Record `flow.rejected` task event
- Publish `flow.rejected` domain event

**Commit SHA recording (`RecordNodeCommit`):**
- Called when agent completes git work within a flow node
- Set `flow_node_execution.commit_sha = <sha>` for the active execution
- Set `project_task.branch_name = <branch>` if not already set

**Subtask management:**
- `CreateSubtask(ctx, flowNodeExecutionID, title, description, assignee)` — creates subtask with next sequence number; enforces that all prior subtasks in the execution are `done` before creation is useful (validation advisory only, not enforced at DB level — sequential execution is a runtime guarantee, not a schema constraint)
- `UpdateSubtaskStatus(ctx, subtaskID, newStatus)` — valid transitions: `pending → in_progress`, `in_progress → done`, `in_progress → cancelled`, `pending → cancelled`; set `completed_at` on terminal states
- `ListSubtasks(ctx, flowNodeExecutionID)` — returns subtasks ordered by `sequence_number ASC`

**Dependency DAG enforcement:**
- `AddDependency(ctx, sourceType, sourceID, dependsOnType, dependsOnID, createdBy)`:
  - Validate same-level constraint (source_type = depends_on_type) — returns `ErrCrossLevelDependency` if violated
  - Validate no self-dependency — returns `ErrSelfDependency`
  - Cycle detection via `CheckCycle`: perform a graph traversal from `dependsOnID` to check if it eventually reaches `sourceID`; if so, return `ErrCyclicDependency`
  - Insert `project_task_dependency` row
  - If the depended-on task is currently `blocked` or `cancelled`: immediately create a resolution task (same logic as `MarkBlocked` in task 028)
- `RemoveDependency(ctx, sourceType, sourceID, dependsOnType, dependsOnID)` — hard delete (dependencies are not soft-deleted; they reflect the current desired state)
- `OnTaskCancelled(ctx, taskID)` — event handler: find all tasks depending on this task → for each dependent, call `MarkBlocked` with reason `"dependency {slug}-{n} was cancelled"` + auto-create resolution task

### Must NOT build
- `flow.advance` native tool implementation (task 056)
- `chat_session` creation for flow nodes (task 060)
- Task API endpoints (task 032)
- Control plane run management (tasks 051, 052)
- Delivery / merge queue workers (tasks 031, 064)

## Acceptance Criteria

- [ ] `StartFlow` creates `flow_node_execution` with `visit_number=1` and sets `current_flow_node_id` on the task
- [ ] `AdvanceFlow` on a node with `requires_human_review=true` transitions task to `review` instead of advancing; creates `task_review` inbox item
- [ ] `AdvanceFlow` on terminal node (`next_node_id IS NULL`) transitions task to `done`
- [ ] `RejectFlowNode` increments `visit_number` correctly on a node that has been visited before
- [ ] `RejectFlowNode` when new visit count exceeds `max_visits` transitions task to `blocked` and creates inbox item; does NOT create new `flow_node_execution`
- [ ] `RejectFlowNode` on node with no `reject_node_id` returns `ErrNoRejectionPath`
- [ ] `AddDependency` with a cycle (A depends on B, B depends on A) returns `ErrCyclicDependency`; no row inserted
- [ ] `AddDependency` with cross-level types returns `ErrCrossLevelDependency`
- [ ] `OnTaskCancelled` marks all dependent tasks as blocked and creates resolution tasks for each

## Tests Required

**Unit tests:**
- `visit_number` computation: mock repo with existing executions for a node; verify `MAX(visit_number)+1` is used
- `CheckCycle` algorithm: linear chain A→B→C; adding C→A detected as cycle; adding D→A not a cycle
- `UpdateSubtaskStatus` transitions: table-driven test; `done → in_progress` returns `ErrInvalidSubtaskTransition`
- `RejectFlowNode` max_visits guard: execution returns visit_number=10, node max_visits=10 → `ErrMaxVisitsExceeded`

**Integration tests:**
- Full flow advancement: create flow template with 3 nodes → start flow → advance through all 3 → task reaches `done`; verify `flow_node_execution` rows: 3 rows, all `completed`
- Rejection loop: advance to review node → reject → verify new `flow_node_execution` with `visit_number=2`; advance through work node again → advance to review again → reject → `visit_number=3`
- Max visits enforcement: set `max_visits=2`; reject twice → second rejection creates execution with `visit_number=2`; reject third time → `ErrMaxVisitsExceeded`, task blocked
- Dependency cycle: task A depends on B (inserted); add B depends on A → `ErrCyclicDependency`; DB has only one dependency row (A→B)
- Cancelled dependency: task A depends on task B; cancel task B → `OnTaskCancelled` marks A as blocked; resolution task created with title matching format

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

> ✅ ISSUE #16 (RESOLVED): `flow_node` schema finalised in task 017. This service reads only `next_node_id`, `reject_node_id`, `requires_human_review`, and `max_visits`. `mcp_tools` and `tool_domains` are read by task 049, not this service.

- Flow advancement is explicitly agent-signalled: the agent must call the `flow.advance` tool. The control plane completing a run does NOT automatically advance the flow. This is a critical behavioral rule — implementers must resist the temptation to auto-advance on run success.
- `RejectFlowNode` creates a new `flow_node_execution` row for the rejection target. It does NOT modify the `visit_number` on any existing row. The visit count for a node is computed as `COUNT(*) of flow_node_execution WHERE task_id = $1 AND flow_node_id = $2`, which equals the `MAX(visit_number)` since visit numbers are sequential.
- The cycle detection algorithm in `AddDependency` must be bounded to prevent infinite traversal in large graphs. Use depth-first search with a visited-set; abort and return `ErrDAGTooDeep` if depth exceeds 100 edges.
- `OnTaskCancelled` is registered as a domain event handler for `task.status_changed` events where `to_status='cancelled'`. Wire this up in the event bus subscriber setup, not as a direct service call from `TransitionStatus`.
