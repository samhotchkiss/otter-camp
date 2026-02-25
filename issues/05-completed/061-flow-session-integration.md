# 061: Flow-Session Integration and Task Participant Cross-Domain Wiring

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §FlowNodeExecution, doc 03 §ProjectTaskParticipant, doc 03 §ProjectSubtask, doc 02 §SessionLifecycle, doc 07 §ModelProfileAssignment |
| Spec status | finished |
| Depends on | 029, 030, 043, 044, 052, 053 |
| Blocks | 062, 064, 073, 084 |

## Scope

Wire the cross-domain integration points that sit between flow execution and the chat/control
plane domains: async chat session auto-creation when a run reaches a flow node; commit_sha
recording on node completion; per-node work log session routing; `project_task_participant`
service; `project_subtask` service cross-domain assignment; and the `model_profile_assignment`
scope resolution for `scope_type='flow_node'`.

### Must build

**Flow node async session auto-creation** (`internal/project/flow_session.go`):

`FlowSessionBridge.EnsureNodeSession(ctx, fne FlowNodeExecution) (ChatSession, error)`:
- Called by `FlowExecutionService.StartNodeExecution` (task 030) immediately after the
  `flow_node_execution` row is created.
- Checks whether `fne.session_id` is already set; if so, returns the existing session.
- If not set: calls `ChatService.CreateSession` (task 044) with:
  - `scope_type = 'project_task'`
  - `scope_id = fne.task_id`
  - `mode = 'async'`
  - `created_by_type = 'system'`, `created_by_id = system_sentinel_uuid`
  - `metadata = {flow_node_execution_id: fne.id, flow_node_id: fne.flow_node_id}`
- After session creation: updates `flow_node_execution.session_id = session.id` via
  `FlowNodeExecutionRepository.SetSessionID`.
- Returns the created/existing session.
- Error handling: session creation failure → log error, return error (node cannot start
  without a session; the caller in task 030 should mark the node execution as failed).

`FlowSessionBridge.RecordCommitSHA(ctx, fneID uuid, commitSHA string) error`:
- Called by `FlowExecutionService.CompleteNodeExecution` (task 030) when the node transitions
  to `completed`.
- Updates `flow_node_execution.commit_sha = commitSHA` via
  `FlowNodeExecutionRepository.SetCommitSHA`.
- `commitSHA` comes from the run's output artifact (the agent emits `git.commit` SHA in the
  turn loop).
- If `commitSHA` is empty string: updates `commit_sha = null` (not required, just recorded if available).

**Per-node work log session routing:**
- Every `run` that is associated with a `flow_node_execution` (via `run.flow_node_id`) has its
  SSE events routed to the node's dedicated chat session.
- Implement `FlowSessionBridge.RouteRunToSession(ctx, run Run) error`:
  - If `run.flow_node_id` is null: no-op (return nil).
  - Load the active `flow_node_execution` for `(run.task_id, run.flow_node_id)`.
  - If the execution has no `session_id`: call `EnsureNodeSession` to create one.
  - Append a `chat_message` to the session with:
    - `author_type = null`, `author_id = null` (system message)
    - `message_type = 'tool_result'`
    - `content = {type:'run_log_link', run_id: run.id, summary: run.type}`
  - This ensures the session has a visible record of each run associated with the node.
- `RouteRunToSession` is called from `RunService.CreateRun` (task 053) when
  `run.flow_node_id` is set.

**`project_task_participant` service** (`internal/project/participant_service.go`):

`TaskParticipantService.AddParticipant(ctx, taskID, participantType, participantID, role string) error`:
- Validates `participantType` in `('human_user', 'agent')`.
- Validates `role` in `('planner','worker','reviewer','observer')`.
- Inserts `project_task_participant` row; ignores duplicate (ON CONFLICT DO NOTHING).
- Emits `domain_event(event_type='task.participant_added', payload={task_id, participant_type, participant_id, role})`.

`TaskParticipantService.RemoveParticipant(ctx, taskID, participantType, participantID string) error`:
- Deletes matching `project_task_participant` row.
- Emits `domain_event(event_type='task.participant_removed', ...)`.

`TaskParticipantService.ListParticipants(ctx, taskID) ([]TaskParticipant, error)`:
- Returns all participants for the task with their roles.

**`project_subtask` cross-domain assignment validation:**
- Extend `FlowExecutionService.CreateSubtask` (task 030) to validate the `assignee_id`
  exists as either a `human_user` or `agent` row before insertion.
- `agent` validation: call `AgentRepository.GetByID`; return `ErrAgentNotFound` if missing.
- `human_user` validation: call `UserRepository.GetByID`; return `ErrUserNotFound` if missing.
- Both checks use the repositories already built in tasks 013 and 005.

**`model_profile_assignment` flow_node scope support:**
- Extend `ModelGateway.ResolveModelProfile` (task 035) to handle
  `scope_type='flow_node'` in the assignment hierarchy:
  - New step in the resolution chain (highest priority): check
    `model_profile_assignment WHERE scope_type='flow_node' AND scope_id=run.flow_node_id`.
  - This step runs before the `agent` scope check.
  - Full resolution order: `flow_node > agent > project > org`.
- Add `ModelProfileAssignmentRepository.GetByScope(ctx, scopeType, scopeID)` overload if
  it does not already exist (task 010 may have omitted flow_node scope).
- The API for creating/updating flow_node-scoped assignments already exists via task 037
  `PUT /v1/model/assignments/:scope` — no new API endpoint needed.

**Delivery API endpoint wiring** (extending task 032):
- Add to the existing router (task 032 handlers file):
  - `GET /v1/projects/:id/remotes` → `DeliveryHandler.ListRemotes`
  - `POST /v1/projects/:id/remotes` → `DeliveryHandler.CreateRemote`
  - `GET /v1/projects/:id/environments` → `DeliveryHandler.ListEnvironments`
- These handlers call `ProjectRemoteRepository` and `ProjectEnvironmentRepository`
  (both built in task 031).
- Response envelopes use the standard `{data, meta}` format (task 067).

### Must NOT build

- `flow_node_execution` DDL or `project_task_participant` DDL (tasks 029 and 027)
- Chat session DDL or ChatService core logic (tasks 043/044)
- Run service state machine (task 053)
- Model profile assignment DDL or assignment creation API (tasks 010, 037)
- `FlowExecutionService` subtask CRUD (task 030) — this task extends/calls it
- Delivery schema DDL (task 031)

## Acceptance Criteria

- [ ] `FlowSessionBridge.EnsureNodeSession` creates a chat session with `scope_type='project_task'` and `mode='async'` and sets `flow_node_execution.session_id`; calling it again for the same execution returns the same session ID without creating a duplicate
- [ ] `FlowSessionBridge.RecordCommitSHA` updates `flow_node_execution.commit_sha`; empty string sets null
- [ ] `FlowSessionBridge.RouteRunToSession` appends a `chat_message` of `message_type='tool_result'` to the node session when `run.flow_node_id` is set
- [ ] `FlowSessionBridge.RouteRunToSession` is a no-op when `run.flow_node_id` is null
- [ ] `TaskParticipantService.AddParticipant` emits `task.participant_added` domain event; duplicate add is idempotent (no error, no duplicate row)
- [ ] `TaskParticipantService.RemoveParticipant` emits `task.participant_removed` domain event
- [ ] `FlowExecutionService.CreateSubtask` with a non-existent `agent_id` returns `ErrAgentNotFound`
- [ ] `ModelGateway.ResolveModelProfile` with a `flow_node`-scoped assignment returns it before the `agent`-scoped one for the same run

## Tests Required

**Unit tests:**
- `EnsureNodeSession`: mock `ChatService.CreateSession` and `FlowNodeExecutionRepository.SetSessionID`; verify session created with correct scope fields; second call with `session_id` already set → `CreateSession` not called
- `RecordCommitSHA`: mock repository; verify `SetCommitSHA` called with correct SHA; empty SHA → null set
- `RouteRunToSession`: run with non-null `flow_node_id` → `EnsureNodeSession` + `ChatMessageRepository.Append` called; run with null `flow_node_id` → neither called
- `TaskParticipantService.AddParticipant`: mock domain event bus; verify event emitted with correct payload; verify ON CONFLICT behavior does not double-emit
- Model profile scope resolution: build resolution chain with `flow_node` assignment present and `agent` assignment present → flow_node returned; remove flow_node assignment → agent returned

**Integration tests:**
- `EnsureNodeSession` round-trip with real DB: insert `flow_node_execution`; call `EnsureNodeSession`; verify `chat_session` row exists with `scope_type='project_task'` and `flow_node_execution.session_id` updated
- Delivery API endpoints: `GET /v1/projects/:id/remotes` returns 200 with empty array for new project; `POST` creates a remote; `GET /v1/projects/:id/environments` returns 200

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

The `system_sentinel_uuid` (`00000000-0000-0000-0000-000000000000`) is defined in task 003 as
a package-level constant. Use it for system-originated chat session creation.

The chat message appended by `RouteRunToSession` is informational only — it is not processed
by the turn engine. Its `message_type='tool_result'` is reused here for structural consistency
but represents a system-generated log link, not an actual tool result. This is a pragmatic
choice; if a dedicated `message_type='system_log'` value is added in future, migrate these rows.
