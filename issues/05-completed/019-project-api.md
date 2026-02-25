# 019: Project API Endpoints

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §ProjectAPI, doc 12 §APIConventions, doc 03a §DeliveryModes |
| Spec status | finished |
| Depends on | 018, 007 |
| Blocks | 032, 073, 084 |

## Scope

Wire all project-domain HTTP endpoints for projects, flow templates, flow nodes, and task
schedules. Handlers delegate to `ProjectService` (task 018). Auth middleware from task 007
applied to all routes.

### Must build

**Route registrations** (all under `/v1/` prefix, doc 12 authoritative):

```
GET    /v1/projects                                     → ProjectService.List
POST   /v1/projects                                     → ProjectService.Create
GET    /v1/projects/:id                                 → ProjectService.Get
PATCH  /v1/projects/:id                                 → ProjectService.Update
DELETE /v1/projects/:id                                 → ProjectService.Delete

GET    /v1/projects/:id/flow-templates                  → ProjectService.ListFlowTemplates (project-scoped)
POST   /v1/projects/:id/flow-templates                  → ProjectService.CreateFlowTemplate
GET    /v1/flow-templates                               → ProjectService.ListFlowTemplates (all current: org + system)
GET    /v1/flow-templates/:id                           → ProjectService.GetFlowTemplate
PATCH  /v1/flow-templates/:id                           → ProjectService.UpdateFlowTemplate

GET    /v1/flow-templates/:id/nodes                     → ProjectService.GetFlowNodes
POST   /v1/flow-templates/:id/nodes                     → ProjectService.AddFlowNode
PATCH  /v1/flow-templates/:id/nodes/:node_id            → ProjectService.UpdateFlowNode
DELETE /v1/flow-templates/:id/nodes/:node_id            → ProjectService.RemoveFlowNode

GET    /v1/projects/:id/schedules                       → ProjectService.ListSchedules
POST   /v1/projects/:id/schedules                       → ProjectService.CreateSchedule
PATCH  /v1/projects/:id/schedules/:schedule_id          → ProjectService.UpdateSchedule
DELETE /v1/projects/:id/schedules/:schedule_id          → ProjectService.DeleteSchedule
```

**Request/response shapes:**

`POST /v1/projects` body:
```json
{
  "slug": "my-project",
  "display_name": "My Project",
  "description": "",
  "delivery_mode": "gated",
  "settings": {}
}
```

`POST /v1/flow-templates/:id/nodes` body:
```json
{
  "display_name": "Review Step",
  "node_type": "review",
  "actor_type": "human",
  "actor_id": null,
  "next_node_id": null,
  "reject_node_id": null,
  "mcp_tools": [],
  "tool_domains": [],
  "requires_human_review": true,
  "max_visits": 10,
  "position": 1
}
```

`POST /v1/projects/:id/schedules` body:
```json
{
  "display_name": "Daily standup",
  "flow_template_id": "<uuid>",
  "cron_expression": "0 9 * * 1-5",
  "overlap_policy": "skip",
  "max_duration_ms": null,
  "is_enabled": true
}
```

**Error mapping:**
- `ErrSlugTaken` → 409 Conflict
- `ErrProjectHasActiveTasks` → 409 Conflict with `{error: {code: "project_has_active_tasks"}}`
- `ErrSystemTemplateProtected` → 403 Forbidden
- `ErrTemplateInUse` → 409 Conflict with `{error: {code: "template_in_use", message: "A new template version was not created because no modifications were requested"}}`
- `ErrInvalidCronExpression` → 422 Unprocessable Entity with the parser message in `error.detail`
- `ErrAgentNotFound` (flow node actor) → 422 Unprocessable Entity

**RBAC enforcement:**
- `POST /v1/projects`, `DELETE /v1/projects/:id`: `admin` role required
- `POST /v1/flow-templates`, `PATCH`, node management: `admin` or `member` (project members can edit their project's templates)
- `GET` endpoints: any authenticated principal
- Schedule CRUD: `admin` or `member`

**Pagination:** All list endpoints support `cursor` and `limit` query parameters (default 50, max 200).

### Must NOT build
- Task endpoints (`/v1/projects/:id/tasks`, `/v1/tasks/:id`) — task 032
- Delivery endpoints (`/v1/projects/:id/remotes`, environments) — task 032/061
- Merge queue endpoints — task 032
- Inbox endpoints — task 032

## Acceptance Criteria

- [ ] `POST /v1/projects` with valid body → 201 Created; `GET /v1/projects/:id` returns the same data
- [ ] `POST /v1/projects` with duplicate slug → 409 Conflict
- [ ] `DELETE /v1/projects/:id` with no active tasks → 204 No Content
- [ ] `DELETE /v1/projects/:id` with active tasks → 409 Conflict
- [ ] `POST /v1/flow-templates/:id/nodes` with valid body → 201 Created; `GET /v1/flow-templates/:id/nodes` includes the new node
- [ ] `PATCH /v1/flow-templates/:id` on system template → 403 Forbidden
- [ ] `POST /v1/projects/:id/schedules` with invalid cron → 422 with `error.code='invalid_cron_expression'`
- [ ] Unauthenticated request → 401; non-member `DELETE` → 403

## Tests Required

**Unit tests:**
- Handler request validation: missing `slug` on project create → 422 before service call
- Error mapping: each `ErrXxx` maps to correct HTTP status

**Integration tests:**
- Full HTTP round-trip via `httptest.Server` with real `ProjectService` and PostgreSQL:
  - Project CRUD lifecycle
  - Flow template update (in-use → new version; response includes `version` field incremented)
  - Schedule create with valid cron → `next_fire_at` present in response
  - Flow node CRUD: add, update, reorder (position), delete; verify `GET /nodes` ordering
- RBAC: member token on admin-only route → 403; unauthenticated → 401

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- When `UpdateFlowTemplate` creates a new version, the response body should return the NEW version row (new ID, incremented version number). The old row ID is not surfaced. Include a `version` field in the response so clients can detect that a version bump occurred.
- The `GET /v1/flow-templates` (without project ID) returns all current templates the caller can see: system templates (`is_system=true`) + org-scoped templates for the caller's org. It does NOT return project-scoped templates unless the caller is a member of that project. Implement an appropriate filter in `ProjectService.ListFlowTemplates`.
- Flow node ordering in the response: always sorted by `position` ascending. The API does not provide a dedicated "reorder" endpoint — clients set the `position` field on each node individually via `PATCH`. Ties in position are broken by `created_at` ascending.
- `DELETE /v1/projects/:id/schedules/:schedule_id` is a hard delete. Return 204 No Content. If the schedule does not exist, return 404.
- Do not add a `POST /v1/projects/:id/schedules/:schedule_id/enable` or `/disable` verb endpoint — use `PATCH` with `{"is_enabled": true/false}` for enable/disable. State transitions for schedules are simple boolean toggles, not lifecycle state machines.
