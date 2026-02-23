# 026: Agent API Endpoints — Assignments and Skills

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 05 §AgentProjectAssignmentAPI, doc 10 §AgentSkillAPI |
| Spec status | finished |
| Depends on | 025, 015, 007 |
| Blocks | 032, 074 |

## Scope

Build the HTTP endpoints for managing agent project assignments and agent skill attachments.
These endpoints are extensions of the agent API surface established in task 015.

### Must build

**Project assignment endpoints:**
- `POST /v1/agents/:id/project-assignments` — assign agent to a project; body: `{project_id, role, assigned_by_type, assigned_by_id?}`
- `DELETE /v1/agents/:id/project-assignments/:pid` — remove agent from project (soft deactivate); `:pid` is `project_id`
- `GET /v1/agents/:id/project-assignments` — list active project assignments for an agent; returns `{data: [{id, project_id, project_slug, role, assigned_at}], meta}`

**Skill attachment endpoints:**
- `GET /v1/agents/:id/skills` — list active skill attachments for an agent; ordered by `priority ASC`; returns `{data: [{id, skill_id, skill_name, priority, attached_at}], meta}`
- `POST /v1/agents/:id/skills` — attach a skill to an agent; body: `{skill_id, priority?}`
- `DELETE /v1/agents/:id/skills/:sid` — detach a skill from an agent; `:sid` is `skill_id`
- `PATCH /v1/agents/:id/skills/:sid` — update skill attachment priority; body: `{priority}`

**Request/response shapes:**

POST /v1/agents/:id/project-assignments:
```json
{
  "project_id": "<uuid>",
  "role": "pm|worker|reviewer|observer"
}
```
Response: `{data: {id, agent_id, project_id, role, is_active, assigned_at}}`

POST /v1/agents/:id/skills:
```json
{
  "skill_id": "<uuid>",
  "priority": 100
}
```
Response: `{data: {id, agent_id, skill_id, priority, is_active, attached_at}}`

**Validation rules:**
- `role` must be one of `pm`, `worker`, `reviewer`, `observer`
- `priority` must be an integer 1–1000 (default 100)
- Agent must belong to the same organization as the project
- Skill must belong to the same organization as the agent (or be a system skill with `organization_id=null`)
- Attempting to assign an agent that is `lifecycle_status != 'active'` returns 422

**Auth and RBAC:**
- All endpoints require Bearer token or API key authentication (middleware from task 007)
- Writes (`POST`, `DELETE`, `PATCH`) require org-admin or project-admin role
- `GET` endpoints are available to any authenticated user in the org

### Must NOT build
- Agent CRUD endpoints (task 015)
- Project endpoints (task 019)
- Skill registry CRUD (task 011)
- Flow node skill attachment (task 017 / task 019)

## Acceptance Criteria

- [ ] `POST /v1/agents/:id/project-assignments` with `role='pm'` succeeds and existing active PM is deactivated
- [ ] `POST /v1/agents/:id/project-assignments` with an agent from a different org returns 404 (not 403 — org membership leak prevention)
- [ ] `DELETE /v1/agents/:id/project-assignments/:pid` soft-deactivates the row; subsequent `GET` does not include it
- [ ] `GET /v1/agents/:id/skills` returns skills ordered by `priority ASC`
- [ ] `POST /v1/agents/:id/skills` with duplicate `skill_id` reactivates the existing row; returns 200 not 201
- [ ] `DELETE /v1/agents/:id/skills/:sid` returns 404 if the skill is not attached (active) to the agent
- [ ] `PATCH /v1/agents/:id/skills/:sid` updates priority; subsequent `GET` reflects new ordering
- [ ] Unauthenticated requests return 401; insufficient role returns 403

## Tests Required

**Unit tests:**
- Route registration: verify all 7 endpoints are registered on the router with correct HTTP methods
- Request validation: `role` enum check; `priority` range check (0 → 422, 1001 → 422, 1 → ok, 1000 → ok)
- Org isolation: handler rejects cross-org agent+project combinations before calling service

**Integration tests:**
- PM assignment flow: POST assign as PM → GET project assignments → DELETE → GET confirms removed
- Skill attachment lifecycle: POST attach → GET ordered list → PATCH priority → GET re-ordered → DELETE → GET empty
- Concurrent PM assignment: two simultaneous POST requests assigning different agents as PM for the same project; exactly one succeeds with 200, the other succeeds with 200 (service handles the swap atomically); verify DB state has exactly one active PM
- 404 isolation: request assignments for agent belonging to a different org → 404

**E2E tests:**
- None — covered by dedicated E2E task 085

## Implementer Notes

- The `DELETE /v1/agents/:id/project-assignments/:pid` path parameter `:pid` is the `project_id` UUID, not the `agent_project_assignment.id`. This matches the natural key for the resource.
- `GET /v1/agents/:id/skills` uses cursor-based pagination consistent with all list endpoints (task 067). Default page size 50.
- When `POST /v1/agents/:id/skills` reactivates an existing detached row (idempotent reattach), return HTTP 200 with the updated row — not 201. Include a `meta.reactivated: true` flag in the response so callers can distinguish creation from reactivation.
- The PATCH priority endpoint is intentionally separate from a general "update attachment" endpoint to keep the contract narrow. Only `priority` is mutable after attachment; other fields are set at creation time.
