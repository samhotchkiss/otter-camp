# 028: Project Task Service

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 03 §ProjectTask, doc 03 §WorkStatusMachine, doc 03 §InboxItem, doc 03 §MergeQueue |
| Spec status | finished |
| Depends on | 027, 018, 024, 025 |
| Blocks | 030, 032 |

## Scope

Build the project task service: the `work_status` state machine, task creation rules, the
`requires_human_review` gate, task event recording, inbox item creation, and merge queue
management. Does not build the flow execution service (task 030) or API endpoints (task 032).

### Must build

**`internal/task/service.go` — `TaskService`:**

**Task creation (`CreateTask`):**
- Tasks are always created by an agent or human via chat — never through a direct UI form (doc 03)
- Required fields: `project_id`, `title`, `created_by_type`, `created_by_id`
- Optional: `flow_template_id`, `description`, `assigned_agent_id`
- If `assigned_agent_id` is set, verify the agent has an active assignment to the project (via `AgentProjectAssignmentRepo`)
- If the project's PM has `requires_human_review` scope configured: set `task.requires_human_review = true` and initial `work_status = 'draft'` (task is not queued until a human approves)
- If `requires_human_review = false`: initial `work_status = 'draft'` (task waits for explicit queuing by the PM)
- Record `task.created` event via `ProjectTaskEventRepo.Record`
- Publish `task.created` domain event via `EventBus`

**Work status state machine (`TransitionStatus`):**
Valid transitions (doc 03):
```
draft       → queued            (PM queues the task)
draft       → cancelled         (cancelled before queuing)
queued      → in_progress       (worker agent picks up the task)
queued      → cancelled
in_progress → blocked           (dependency unmet or blocker filed)
in_progress → on_hold           (human puts on hold)
in_progress → review            (agent signals done, enters review node)
in_progress → done              (no review node; task completes directly)
in_progress → cancelled
blocked     → queued            (blocker resolved)
blocked     → cancelled
on_hold     → in_progress       (human resumes)
on_hold     → cancelled
review      → done              (human approves in review node)
review      → in_progress       (human rejects, task re-enters work)
review      → cancelled
done        → (terminal, no transitions)
cancelled   → (terminal, no transitions)
```
- Reject invalid transitions with `ErrInvalidStatusTransition{From, To}`
- Set `completed_at` when transitioning to `done` or `cancelled`
- Record `status.changed` event with `payload: {from_status, to_status}`
- Publish `task.status_changed` domain event

**Human review gate (`RequestHumanApproval`):**
- Called when `requires_human_review = true` and task is being queued
- Creates `inbox_item` with `item_type = 'human_approval_required'`, `source_task_id`, targeting the project PM's linked human user (if the PM is a temp agent, target the org admin group)
- Task stays in `draft` status until the human acts on the inbox item
- `ApproveTask(ctx, taskID, actedByUserID)`: marks inbox item acted → transitions task from `draft` → `queued`
- `RejectTask(ctx, taskID, actedByUserID, reason)`: marks inbox item acted → records `task.review_rejected` event; task stays in `draft`

**Blocked dependency handling (`MarkBlocked`):**
- Transition task to `blocked`
- Automatically create a new resolution task assigned to the project PM: `title = "Resolve blocker for task {slug}-{n}"`, `work_status = 'queued'`
- Create `inbox_item` with `item_type = 'blocker_filed'` targeting PM's human user
- Record `status.changed` event with `payload: {blocker_reason}`

**Inbox item management:**
- `CreateInboxItem(ctx, params)` — thin wrapper over `InboxItemRepo.Create`; validates `item_type`, `source_task_id`, `target_user_id` combinations
- `ActOnInboxItem(ctx, itemID, userID, action, payload)` — marks item as acted; dispatches the appropriate domain action (approve, reject, dismiss) based on `item_type`

**Merge queue management:**
- `EnqueueForMerge(ctx, taskID)` — called when task transitions to `done` and has a `branch_name`; creates `merge_queue_entry` with next available position
- `DequeueFromMerge(ctx, entryID, reason)` — used when a task is cancelled after enqueueing; updates status to `skipped`, sets `archived_at`
- `GetMergeQueueStatus(ctx, projectID)` — returns active queue entries in position order

### Must NOT build
- Flow execution (flow advancement, node execution) — task 030
- Delivery (push to remote, deploy task creation) — task 031, 064
- Task API endpoints — task 032
- Subtask management — task 030
- Dependency DAG enforcement — task 030

## Acceptance Criteria

- [ ] `CreateTask` with `assigned_agent_id` pointing to an agent not assigned to the project returns `ErrAgentNotAssigned`
- [ ] `TransitionStatus` rejects `done → queued` with `ErrInvalidStatusTransition`
- [ ] `TransitionStatus` to `done` sets `completed_at` to current time
- [ ] `RequestHumanApproval` creates an `inbox_item` of type `human_approval_required`; task remains in `draft`
- [ ] `ApproveTask` transitions task from `draft` to `queued` and marks inbox item as acted
- [ ] `MarkBlocked` creates a resolution task assigned to PM; creates `blocker_filed` inbox item
- [ ] `EnqueueForMerge` creates a `merge_queue_entry` with the next sequential `position` for the project
- [ ] All status transitions publish a corresponding `task.*` domain event via EventBus
- [ ] `task.created` domain event is published on successful `CreateTask`

## Tests Required

**Unit tests:**
- State machine: table-driven test covering all 20+ valid transitions and at least 15 invalid ones; verify `ErrInvalidStatusTransition` fields
- `completed_at`: transitions to `done` and `cancelled` set `completed_at`; transitions to all other statuses do not
- `MarkBlocked`: verify resolution task title format `"Resolve blocker for task {slug}-{n}"`
- `EnqueueForMerge`: verify position is MAX(position)+1 for the project; first entry gets position 1

**Integration tests:**
- Full status lifecycle: draft → queued → in_progress → review → done; verify events recorded for each transition
- Human approval gate: create task with `requires_human_review=true` → verify inbox item created → `ApproveTask` → verify status is `queued`
- `MarkBlocked` end-to-end: transition to `blocked` → verify resolution task created in DB → verify `blocker_filed` inbox item → resolve via `TransitionStatus(blocked → queued)` → verify resolution task still exists
- Merge queue ordering: enqueue 3 tasks; verify positions 1, 2, 3; dequeue middle one → active list shows positions 1, 3

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- The state machine transitions listed above are exhaustive per doc 03. Any transition not listed is invalid. Implementers should represent the valid transition map as a compile-time constant (e.g. a `map[string][]string` or an enum graph), not as scattered `if` statements.
- `requires_human_review` on the task row reflects the project PM's scope configuration at the time of task creation. If the PM's scope changes after a task is created, existing tasks are not retroactively updated.
- When `MarkBlocked` auto-creates a resolution task, that task is assigned to the PM agent but does NOT require human review (it is a PM-internal task). It starts in `work_status='queued'` immediately so the PM can begin work.
- `EnqueueForMerge` must only be called when `task.branch_name IS NOT NULL`. If `branch_name` is null, return `ErrNoTaskBranch` — tasks without branches should not enter the merge queue.
- The `ActOnInboxItem` dispatcher must handle unknown `item_type` values gracefully (return `ErrUnknownInboxItemType`) to avoid panics when new item types are added in later tasks (e.g., `browser_handoff` from task 059).
