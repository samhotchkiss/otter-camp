# 015: Agent API Endpoints

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 05 §AgentAPI, doc 12 §APIConventions |
| Spec status | finished |
| Depends on | 014, 007 |
| Blocks | 026, 074, 085 |

## Scope

Wire all agent-domain HTTP endpoints. Handlers delegate to `AgentService` (task 014).
Auth middleware (task 007) is applied to all routes. No new business logic here — only
request parsing, response serialization, and error mapping.

### Must build

**Route registrations** (all under `/v1/` prefix, doc 12 authoritative):

```
GET    /v1/agents                        → AgentService.List
POST   /v1/agents                        → AgentService.Create
GET    /v1/agents/:id                    → AgentService.Get
PATCH  /v1/agents/:id                    → AgentService.Update
POST   /v1/agents/:id/pause              → AgentService.Pause
POST   /v1/agents/:id/unpause            → AgentService.Unpause
POST   /v1/agents/:id/retire             → AgentService.Retire
POST   /v1/agents/:id/cancel             → AgentService.Cancel
GET    /v1/agent-templates               → AgentProfileTemplateRepo.List
POST   /v1/agent-templates               → AgentProfileTemplateRepo.Create
GET    /v1/agent-templates/:id           → AgentProfileTemplateRepo.GetByID
```

**Request/response shapes:**

`POST /v1/agents` body:
```json
{
  "display_name": "string",
  "agent_class": "staff|temp",
  "agent_type": "pm|worker|reviewer|general",
  "system_prompt": "string",
  "operator_instructions": "string",
  "default_model_profile_id": "string|null",
  "tool_allow_list": ["glob..."],
  "tool_deny_list": ["glob..."],
  "memory_read_scopes": ["org","project","agent"],
  "private_memory": false,
  "budget_cap_tokens": 0,
  "budget_period": "daily|weekly|monthly|null",
  "temp_project_id": "uuid|null",
  "temp_ttl_seconds": 0
}
```

`GET /v1/agents` query parameters:
- `lifecycle_status` — filter by status value(s); comma-separated allowed
- `agent_class` — `staff` or `temp`
- `agent_type` — filter by type
- `cursor` — opaque pagination cursor
- `limit` — default 50, max 200

All responses use the standard `{data, meta}` envelope (task 067 convention; implement the envelope here — task 067 will not change the shape, only add middleware enforcement).

**Error mapping:**
- `ErrInvalidTransition` → HTTP 409 Conflict with `{error: {code: "invalid_transition", message: "..."}, meta: {}}`
- `ErrInvalidForTempAgent` → HTTP 422 Unprocessable Entity
- `ErrConcurrentTempLimitReached` → HTTP 429 Too Many Requests with `{error: {code: "temp_limit_reached", ...}}`
- `ErrStarterTrioProtected` → HTTP 403 Forbidden
- `ErrNotFound` → HTTP 404

**RBAC enforcement:**
- `POST /v1/agents` (staff): `admin` role required
- `POST /v1/agents` (temp): `admin` or `member` role (agents may create their own temps via API key)
- `PATCH /v1/agents/:id`, lifecycle transitions: `admin` role required
- `GET` endpoints: any authenticated principal

### Must NOT build
- Project assignment endpoints (`/v1/agents/:id/project-assignments`) — task 026
- Skill attachment endpoints (`/v1/agents/:id/skills`) — task 026
- Agent creation via chat / tool calls — task 056

## Acceptance Criteria

- [ ] `GET /v1/agents` returns paginated list with `{data: [...], meta: {cursor, total}}` envelope
- [ ] `POST /v1/agents` with valid staff body → 201 Created with agent payload
- [ ] `POST /v1/agents/:id/pause` on active agent → 200 OK; subsequent `GET` shows `lifecycle_status='paused'`
- [ ] `POST /v1/agents/:id/pause` on already-paused agent → 409 Conflict
- [ ] `POST /v1/agents/:id/retire` on a temp agent → 422 Unprocessable Entity
- [ ] `GET /v1/agents?lifecycle_status=active&agent_class=temp` returns only matching rows
- [ ] Unauthenticated request to any agent endpoint → 401 Unauthorized
- [ ] Non-admin attempting `POST /v1/agents` (staff) → 403 Forbidden

## Tests Required

**Unit tests:**
- Request validation: missing required fields (`display_name`, `agent_class`) → validation error before service call
- Error mapping: each `ErrXxx` type maps to the correct HTTP status code

**Integration tests:**
- Full HTTP round-trip via `httptest.Server` with real `AgentService` and real PostgreSQL:
  - Create staff agent → pause → verify status change
  - Create temp agent → exceed concurrent limit → 429
  - List with filters → correct subset returned
  - Auth middleware: bearer token required; wrong token → 401
  - RBAC: member token on admin-only route → 403

**E2E tests:**
- None — covered by dedicated E2E task 085

## Implementer Notes

- All state-transition endpoints use `POST /v1/agents/:id/<action>` (verb-noun) rather than `PATCH`
  to status, per doc 12 conventions. Task 067 will enforce this globally; implement consistently here.
- The `agent-templates` endpoints do not require special RBAC beyond authentication — any authenticated
  user can read templates; only admins can create them (apply same `admin` role check as agent create).
- The `budget_cap_tokens` field is surfaced in the API response; this maps directly to the
  resolved `agent.budget_cap_tokens` column (ISSUE #1 RESOLVED).
- Cursor-based pagination: encode `(created_at, id)` as an opaque base64 cursor. Default page size 50.
  The `meta` object includes `next_cursor` (null if last page) and `total` (count query, cached for 60s max).
