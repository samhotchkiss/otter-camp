# 073: Project and Task Flow Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1-2 days) |
| Spec refs | doc 03 §TaskStateMachine, doc 03 §FlowExecution, doc 03 §MergeQueue, doc 03 §DependencyDAG, doc 03a §DeliveryModes, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 016, 017, 018, 019, 027, 028, 029, 030, 031, 032 |
| Blocks | 089 |

## Scope

Integration test suite for the project and task domain: project CRUD, the complete
`work_status` state machine, flow node progression and rejection loops, merge queue
operations, dependency DAG cycle detection, and inbox item lifecycle. All tests use a
real PostgreSQL database via `testdb.New(t)`.

### Must build

**Test file:** `internal/project/project_integration_test.go`

**Test file:** `internal/project/task_integration_test.go`

**Test file:** `internal/project/flow_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/project.go`:
- `MakeProject(t, db, orgID)` — creates project row; returns `*project.Project`
- `MakeFlowTemplate(t, db, projectID, nodeCount)` — creates flow_template + N flow_node
  rows in a linear chain; returns `*project.FlowTemplate`
- `MakeTask(t, db, projectID, opts)` — creates project_task row; returns `*project.Task`
- `MakeAgent(t, db, orgID)` — thin wrapper over `testutil.MakeAgent`

**Test scenarios in project_integration_test.go:**

`TestProject_CRUD` — POST /v1/projects; GET /v1/projects/:id; PATCH updates delivery_mode;
project slug is unique within org (second project with same slug returns 409); slug not
shared across orgs (two orgs can have same slug).

`TestFlowTemplate_Immutability` — create flow template; PATCH a flow_node; service
creates a new flow_template row with `is_current=true`; old template gets
`is_current=false`; existing task rows referencing old template are unaffected.

`TestTaskSchedule_CronValidation` — POST /v1/projects/:id/schedules with invalid cron
expression returns 422 with `cron_invalid` error; valid cron creates schedule row.

`TestTaskSchedule_OverlapPolicy` — create schedule with `overlap_policy='skip'`; trigger
schedule tick when a task from this schedule is already in_progress; new task is NOT
created; `system.schedule.skipped` domain event emitted.

**Test scenarios in task_integration_test.go:**

`TestTask_StateMachine_FullPath` — create task (work_status='draft'); transition to
'queued'; to 'in_progress'; to 'review'; to 'done'; assert each `project_task_event`
row is written with correct actor; final state is persisted; task is not in merge queue
until explicitly enqueued.

`TestTask_StateMachine_Blocked` — task transitions to 'in_progress'; mark as 'blocked';
verify `project_task_event` with event_type='blocked'; auto-created resolution task
appears in inbox for the PM (assert `inbox_item` row with `item_type='task_blocked'`).

`TestTask_StateMachine_OnHold` — task in 'in_progress' transitions to 'on_hold'; then
back to 'in_progress'; verify both transition events written.

`TestTask_RequiresHumanReview_Gate` — create task with `requires_human_review=true`;
attempt direct transition from 'draft' to 'queued' (bypassing review); service returns
error; transition is only permitted after `review_decision` endpoint is called with
`approved=true`.

`TestTask_StateMachine_Cancelled` — task in 'in_progress'; POST cancel; work_status
becomes 'cancelled'; any active merge_queue_entry for this task is archived.

`TestMergeQueue_Lifecycle` — task reaches 'review' state; enqueue via merge queue service;
`merge_queue_entry` row created with `archived_at=null`; simulate deploy completion;
`archived_at` set on row (not hard-deleted per ISSUE #7 authoritative rule from doc 03a).

`TestInboxItem_Creation` — complete a task with `requires_human_review=true`; assert
`inbox_item` row created for the PM with `item_type='review_required'`;
POST /v1/inbox/:id/act with `action='approve'`; inbox_item `acted_at` set.

**Test scenarios in flow_integration_test.go:**

`TestFlow_Progression_LinearChain` — 3-node flow template; task starts at node 1; agent
signals flow.advance; task moves to node 2 (new `flow_node_execution` row); advance again
to node 3; advance again to completion (no next_node); task work_status='done'.

`TestFlow_Rejection_VisitCounter` — task at node 2; agent calls reject; new
`flow_node_execution` row created for node 2 with `visit_count=2`; task does NOT go back
to node 1 (rejection loops within the node, not to start); assert `reject_node_id`
routing used if defined.

`TestFlow_PerNodeSession_AutoCreated` — start flow node execution; assert a
`chat_session` row is automatically created with `scope_type='project_task'` and
`session_mode='async'`; `flow_node_execution.session_id` FK is set.

`TestFlow_CommitSha_Recording` — mock commit SHA available; node completion records
`commit_sha` on `flow_node_execution` row; assert non-null after node advance.

`TestDependencyDAG_CycleRejection` — create tasks A, B, C; add A→B dependency; add B→C
dependency; attempt to add C→A dependency; service returns `dependency_cycle_detected`
error; no row written to `project_task_dependency`.

`TestDependencyDAG_BlockedAutoResolutionTask` — create tasks A and B; A depends on B;
mark B as cancelled; assert auto-created resolution task appears assigned to PM;
`inbox_item` created for PM.

`TestDependencyDAG_SameLevelOnly` — attempt to create a dependency between a
`project_task` and a `project_subtask` (cross-level); service returns error due to
CHECK constraint (`source_type = depends_on_type`).

### Must NOT build

- E2E tests for full task workflow (task 084)
- Delivery execution integration tests (task 080)
- Browser handoff inbox items (those are tested in task 078)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/project/... -tags integration`
- [ ] `TestTask_StateMachine_FullPath` writes a `project_task_event` row for each transition and asserts every transition in order
- [ ] `TestMergeQueue_Lifecycle` verifies `archived_at` is set (not the row deleted) on deploy completion, per doc 03a authoritative rule
- [ ] `TestDependencyDAG_CycleRejection` verifies the error is returned before any DB write
- [ ] `TestFlow_PerNodeSession_AutoCreated` verifies `flow_node_execution.session_id` is non-null after node start
- [ ] `TestTask_RequiresHumanReview_Gate` confirms the gate is enforced at the service layer with a named error code
- [ ] `TestDependencyDAG_SameLevelOnly` triggers a DB CHECK constraint error wrapped as a domain error

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestProject_CRUD`
- `TestFlowTemplate_Immutability`
- `TestTaskSchedule_CronValidation`
- `TestTaskSchedule_OverlapPolicy`
- `TestTask_StateMachine_FullPath`
- `TestTask_StateMachine_Blocked`
- `TestTask_StateMachine_OnHold`
- `TestTask_RequiresHumanReview_Gate`
- `TestTask_StateMachine_Cancelled`
- `TestMergeQueue_Lifecycle`
- `TestInboxItem_Creation`
- `TestFlow_Progression_LinearChain`
- `TestFlow_Rejection_VisitCounter`
- `TestFlow_PerNodeSession_AutoCreated`
- `TestFlow_CommitSha_Recording`
- `TestDependencyDAG_CycleRejection`
- `TestDependencyDAG_BlockedAutoResolutionTask`
- `TestDependencyDAG_SameLevelOnly`

**E2E tests:** None — covered by task 084.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Domain event bus: real (in-process dispatch for deterministic tests)
- Clock: injected `clock.Fake` for schedule tick tests
- Control plane / runs: not involved; flow advancement is service-layer direct calls

**ISSUE #7 (archived_at trigger):**
`TestMergeQueue_Lifecycle` explicitly tests the doc 03a authoritative rule: `archived_at`
is set on deploy completion, not on merge completion. The test simulates deploy completion
by calling the delivery service's deploy-complete path rather than the merge service.

**ISSUE #16 (RESOLVED):**
`flow_node` schema is final (task 017): no `skills jsonb`, has `mcp_tools jsonb` and `tool_domains jsonb`. Test helpers set these to `[]` (empty JSON array) for rows that don't need them.

**flow.advance is a tool call, not an API endpoint:**
In production, flow node advancement happens via the `flow.advance` native tool call from
an agent during a run. In integration tests, call the advancement service method directly
rather than simulating a full tool dispatch pipeline. Document this with a comment:
`// In production, advancement is triggered via the flow.advance native tool (task 057).`
