# 032: Task and Project API Endpoints

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 03 §TaskAPI, doc 03 §InboxAPI, doc 03 §MergeQueueAPI, doc 03a §DeliveryAPI, doc 12 §APIRoutes |
| Spec status | finished |
| Depends on | 028, 030, 031, 019, 007 |
| Blocks | 073, 084 |

## Scope

Build all HTTP endpoints for tasks, inbox items, merge queue, and delivery (remotes + environments).
This is the largest API surface in L3, covering 18 endpoints. Depends on task service (028),
flow execution service (030), and delivery service (031).

### Must build

**Task endpoints:**
- `GET /v1/projects/:id/tasks` — list tasks for a project; query params: `status` (multi-value), `assigned_agent_id`, `page_cursor`, `limit`
- `POST /v1/projects/:id/tasks` — create a task; body: `{title, description?, flow_template_id?, assigned_agent_id?}`
- `GET /v1/tasks/:id` — get a single task with current flow node info
- `PATCH /v1/tasks/:id` — update mutable fields: `title`, `description`, `assigned_agent_id`, `requires_human_review`; status transitions are via dedicated verb-noun endpoints
- `POST /v1/tasks/:id/queue` — transition `draft → queued` (or trigger human approval gate if required)
- `POST /v1/tasks/:id/cancel` — transition to `cancelled`
- `POST /v1/tasks/:id/advance-flow` — agent-facing: call `FlowExecutionService.AdvanceFlow`; returns updated flow state
- `POST /v1/tasks/:id/reject-flow` — agent-facing: call `FlowExecutionService.RejectFlowNode`; body: `{reason?}`
- `POST /v1/tasks/:id/review-decision` — human: approve or reject the review gate; body: `{decision: 'approve'|'reject', reason?}`

**Task sub-resource endpoints:**
- `GET /v1/tasks/:id/flow` — get full flow state for the task: current node, all executions, subtasks
- `GET /v1/tasks/:id/subtasks` — list subtasks for the current flow node execution
- `POST /v1/tasks/:id/subtasks` — create a subtask; body: `{title, description?, assignee_type?, assignee_id?, sequence_number?}`
- `PATCH /v1/tasks/:id/subtasks/:sid` — update subtask: `title`, `description`, `work_status`
- `POST /v1/tasks/:id/dependencies` — add dependency; body: `{source_type, source_id, depends_on_type, depends_on_id}`
- `DELETE /v1/tasks/:id/dependencies/:did` — remove dependency; `:did` is `project_task_dependency.id`
- `GET /v1/tasks/:id/events` — list task events; ordered `created_at DESC`; cursor pagination
- `GET /v1/tasks/:id/participants` — list active task participants

**Inbox endpoints:**
- `GET /v1/inbox` — list inbox items for the authenticated user; query params: `is_acted`, `item_type`, `page_cursor`; returns user's direct items + broadcast items (target_user_id IS NULL)
- `POST /v1/inbox/:id/act` — act on an inbox item; body: `{action: string, payload?: object}`; dispatches via `TaskService.ActOnInboxItem`

**Merge queue endpoints:**
- `GET /v1/projects/:id/merge-queue` — list active merge queue entries for a project, ordered by position

**Delivery endpoints:**
- `GET /v1/projects/:id/remotes` — list project remotes
- `POST /v1/projects/:id/remotes` — add a remote; body: `{name, url, transport, credential_ref?, is_default?}`
- `PATCH /v1/projects/:id/remotes/:rid` — update remote; body: any subset of mutable fields
- `DELETE /v1/projects/:id/remotes/:rid` — delete a remote (only if not referenced by any active environment)
- `GET /v1/projects/:id/environments` — list project environments
- `POST /v1/projects/:id/environments` — create an environment; body: `{name, delivery_mode, remote_id?, target_branch?, schedule_id?}`
- `PATCH /v1/projects/:id/environments/:eid` — update environment
- `POST /v1/projects/:id/environments/:eid/deploy` — manually trigger a deploy (gated mode); calls `DeliveryService.CreateDeployTask`

**Response shapes:**

`GET /v1/tasks/:id/flow` response:
```json
{
  "data": {
    "task_id": "<uuid>",
    "flow_template_id": "<uuid>",
    "current_node": { "id": "<uuid>", "display_name": "...", "node_type": "work", ... },
    "current_execution": { "id": "<uuid>", "visit_number": 1, "status": "active", ... },
    "executions": [ ... ],
    "subtasks": [ ... ]
  }
}
```

`GET /v1/inbox` response:
```json
{
  "data": [ { "id": "<uuid>", "item_type": "...", "title": "...", "body": "...", "is_read": false, "is_acted": false, "source_task": {...}, "created_at": "..." } ],
  "meta": { "next_cursor": "...", "total": 12 }
}
```

**Validation rules:**
- Task `title` max 500 chars
- `assigned_agent_id` must be an agent active in the project
- `review-decision` only valid when task `work_status='review'`; returns 422 otherwise
- `advance-flow` and `reject-flow` only valid when task `work_status='in_progress'`; returns 422 otherwise
- `POST /v1/inbox/:id/act` with unknown `action` for the item type returns 422

**Auth and RBAC:**
- All task reads available to any org member
- Task writes (`POST /v1/projects/:id/tasks`, `PATCH /v1/tasks/:id`) require project membership
- `review-decision` requires the requesting user to be the `target_user_id` on the corresponding inbox item
- Delivery writes require org-admin role
- Inbox acts require the inbox item to be addressed to the requesting user (or broadcast)

### Must NOT build
- Turn execution (control plane runs) — task 052
- Chat endpoints — task 046
- Scheduling endpoints (task_schedule CRUD is in task 019 scope and task 065)
- Subtask participant management (task 061 — L4)

## Acceptance Criteria

- [ ] `GET /v1/projects/:id/tasks?status=in_progress&status=review` returns only tasks matching either status
- [ ] `POST /v1/tasks/:id/queue` with `requires_human_review=true` creates inbox item and returns task still in `draft`; does not transition to `queued`
- [ ] `POST /v1/tasks/:id/review-decision` with `decision='approve'` when task is not in `review` status returns 422
- [ ] `POST /v1/tasks/:id/advance-flow` returns 404 if the task has no active flow execution
- [ ] `GET /v1/inbox` returns both user-targeted items and broadcast items; excludes acted items by default
- [ ] `POST /v1/inbox/:id/act` on an item belonging to a different user returns 403
- [ ] `DELETE /v1/projects/:id/remotes/:rid` on a remote referenced by an active environment returns 409
- [ ] `GET /v1/tasks/:id/events` supports cursor pagination; `created_at DESC` ordering

## Tests Required

**Unit tests:**
- Route registration: verify all endpoints registered with correct HTTP methods
- Validation: `review-decision` with invalid `decision` value → 422; `PATCH /v1/tasks/:id` with unknown field → ignored (not 400)
- RBAC: `review-decision` called by a user who is not the inbox item target → 403

**Integration tests:**
- Task creation → status lifecycle via API: POST create → POST queue → POST review-decision approve → GET verify `done`
- Flow advancement via API: create task with flow → POST advance-flow → verify execution completed, new execution created for next node
- Inbox list: create 3 direct items + 2 broadcast items for user; GET /v1/inbox returns 5; filter `is_acted=false` returns only unacted
- Merge queue GET: enqueue 3 tasks for project; GET /v1/projects/:id/merge-queue returns 3 in position order
- Remote delete protection: create environment referencing remote; DELETE remote → 409; delete environment → DELETE remote succeeds

**E2E tests:**
- None — covered by dedicated E2E tasks 083, 084

## Implementer Notes

- All verb-noun status transition endpoints (`/queue`, `/cancel`, `/advance-flow`, `/reject-flow`, `/review-decision`) use `POST`, not `PATCH`. This follows the doc 12 convention that state transitions use POST verb-noun paths, not PATCH to a `status` field.
- `GET /v1/tasks/:id/flow` is a read-only aggregation endpoint. It should NOT call any service method that has side effects. Build it as a direct query layer that joins task, flow_node_execution, flow_node, and project_subtask.
- `POST /v1/tasks/:id/subtasks` always creates a subtask in the **currently active** `flow_node_execution`. If there is no active execution (no flow or flow completed), return 422.
- `GET /v1/inbox` must return the authenticated user's targeted items UNION broadcast items (target_user_id IS NULL), deduplicated (a user cannot receive a broadcast item and a targeted item with the same ID). Order by `created_at DESC` across both sets.
- `POST /v1/projects/:id/environments/:eid/deploy` is only valid when `delivery_mode='gated'`. For continuous or scheduled environments, return 422 with a clear message.
- The `meta.total` in pagination responses is a best-effort count (not a strict `COUNT(*)`). Use `COUNT(*) OVER ()` window function in the paginated query to avoid a separate count query.
