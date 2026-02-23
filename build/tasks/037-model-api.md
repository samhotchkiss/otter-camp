# 037: Model API Endpoints

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 07 §ModelProviderAPI, doc 07 §ModelProfileAPI, doc 07 §ModelAssignmentAPI, doc 07 §UsageAPI |
| Spec status | finished |
| Depends on | 036, 007 |
| Blocks | 076 |

## Scope

Build all HTTP endpoints for model provider management, model profile CRUD, model profile
assignment management, and usage reporting. No new tables — this task wires the repositories
and services built in tasks 010, 035, and 036 into HTTP handlers.

### Must build

**Provider endpoints:**
- `GET /v1/model/providers` — list all `model_provider` rows; query params: none; returns paginated list
- `PATCH /v1/model/providers/:id` — update a provider row (displayName, is_enabled, notes); NOT used to create new providers (providers are seeded at bootstrap or via migrations); requires org-admin role
- `GET /v1/model/providers/:id/connections` — list `provider_connection` rows for this provider within the caller's org; query params: `status` (active/disabled/all)
- `POST /v1/model/providers/:id/connections` — create a `provider_connection` for this provider in the caller's org; body: `{display_name, api_key_secret_ref, base_url?, failover_priority?, max_concurrent?}`
- `PATCH /v1/model/providers/:id/connections/:cid` — update a connection (display_name, failover_priority, max_concurrent, is_enabled); api_key_secret_ref changes are allowed (updates the `ref:<slug>` stored in the connection)
- `DELETE /v1/model/providers/:id/connections/:cid` — soft-delete (sets `is_enabled=false`); returns 409 if connection is the last active one for any currently-assigned model profile

**Profile endpoints:**
- `GET /v1/model/profiles` — list model profiles visible to the org (org-scoped rows + system-provided rows where `organization_id IS NULL`); query params: `provider_id`
- `POST /v1/model/profiles` — create a new model profile; body: `{display_name, provider_id, model_name, temperature?, max_tokens?, fallback_profile_id?}`; auto-assigns a stable `logical_profile_id` (UUID) on creation
- `PATCH /v1/model/profiles/:logical_profile_id` — update the current version of a profile; creates a new version row (new `version` integer) with `is_current=true`; old row gets `is_current=false`; `logical_profile_id` is stable across versions; returns the new version row
- `GET /v1/model/profiles/:logical_profile_id` — get the current version of a profile
- `GET /v1/model/profiles/:logical_profile_id/history` — list all version rows ordered by version desc

**Assignment endpoints:**
- `GET /v1/model/assignments` — list `model_profile_assignment` rows for the caller's org; query params: `scope_type` (organization/project/agent/flow_node), `scope_id`
- `PUT /v1/model/assignments/:scope_type/:scope_id` — upsert a model profile assignment for the given scope; body: `{logical_profile_id}`; idempotent; returns the assignment row
- `DELETE /v1/model/assignments/:scope_type/:scope_id` — remove the assignment (falls back to next level in hierarchy); cannot delete org-level assignment if it is the only configured assignment for the org (returns 409)

**Usage endpoint:**
- `GET /v1/usage` — query `model_usage_rollup` for the caller's org; query params:
  - `group_by` (required): one of `provider_connection`, `model_provider`, `agent`, `project`
  - `period` (required): one of `today`, `yesterday`, `7d`, `30d`, or `YYYY-MM-DD` (single day)
  - `model_name` (optional): filter to a specific model
  - `invocation_purpose` (optional): filter to a specific purpose
  - Returns: `{data: [{rollup_type, rollup_id, display_name?, rollup_date, total_invocations, total_input_tokens, total_output_tokens, total_cost_microcents}], meta: {period_start, period_end, total_rows}}`
- `GET /v1/usage/summary` — convenience endpoint: returns org-level total across the `30d` period (no `group_by` required); used by the UI dashboard

**Request/response shapes:**

POST /v1/model/profiles:
```json
{
  "display_name": "Standard (GPT-4o)",
  "provider_id": "uuid",
  "model_name": "gpt-4o",
  "temperature": 0.7,
  "max_tokens": 4096,
  "fallback_profile_id": "uuid-of-haiku-profile"
}
```
Response: `{data: {logical_profile_id, version, display_name, provider_id, model_name, temperature, max_tokens, fallback_profile_id, is_current, organization_id, created_at}}`

PUT /v1/model/assignments/project/:project_id:
```json
{"logical_profile_id": "uuid"}
```
Response: `{data: {scope_type, scope_id, logical_profile_id, created_at, updated_at}}`

**Auth and RBAC:**
- All endpoints require authentication
- Read endpoints (`GET`) available to any authenticated user in the org
- Write endpoints (`POST`, `PATCH`, `PUT`, `DELETE`) require org-admin role
- Usage endpoint available to org-admin and project-manager roles; project-scoped usage visible to project members

### Must NOT build
- Model gateway routing and concurrency logic (task 035)
- Token rollup worker (task 036)
- Model invocation recording during agent turns (task 062)
- Provider health state machine (task 035)

## Acceptance Criteria

- [ ] `PATCH /v1/model/providers/:id` with unknown provider ID returns 404
- [ ] `POST /v1/model/profiles` creates a row with `version=1`, `is_current=true`, stable `logical_profile_id`
- [ ] `PATCH /v1/model/profiles/:logical_profile_id` creates a version=2 row; old row has `is_current=false`; `GET` returns version=2 row
- [ ] `GET /v1/model/profiles` includes system-provided profiles (`organization_id IS NULL`) alongside org-scoped ones
- [ ] `PUT /v1/model/assignments/org/:org_id` upserts the org-level assignment; idempotent second call returns same row with `updated_at` refreshed
- [ ] `DELETE /v1/model/assignments/org/:org_id` returns 409 when this is the only remaining assignment for the org
- [ ] `GET /v1/usage?group_by=agent&period=7d` returns one row per agent that made model calls in the past 7 days
- [ ] Non-admin user calling `POST /v1/model/profiles` returns 403

## Tests Required

**Unit tests:**
- Route registration: all endpoints exist with correct HTTP methods
- `PATCH /v1/model/profiles/:id` versioning: calling patch twice creates v1, v2, v3 rows; only v3 is `is_current=true`
- Assignment hierarchy: `PUT` org-level assignment → `GET` assignments returns it; `DELETE` org-level → 409 guard fires

**Integration tests:**
- Profile CRUD: create profile → get → patch (creates new version) → get history (2 rows)
- Assignment upsert: `PUT` assignment for project → `GET` assignments filtered by `scope_type=project` → row present; `DELETE` → row gone; subsequent `GET` returns empty
- Usage query: seed two rollup rows for different agents for today → `GET /v1/usage?group_by=agent&period=today` returns both rows; totals match seed data
- Org isolation: org A creates a profile; org B calls `GET /v1/model/profiles` → does not see org A's profile (system profiles still visible)

**E2E tests:**
- None — covered by dedicated E2E task 076

## Implementer Notes

- The `logical_profile_id` on `model_profile` is the stable external identifier used in assignments and references. The `id` column is the internal row PK (changes with each version). All API endpoints and FK references use `logical_profile_id`. The `PATCH` endpoint takes `logical_profile_id` in the URL path, fetches the current version row, creates a new row with the same `logical_profile_id` and `version+1`, updates `is_current` on the old row. This must happen in a transaction.
- `DELETE /v1/model/providers/:id/connections/:cid` sets `is_enabled=false` (soft delete). Before doing so, check if any `model_profile` with `is_current=true` references this connection's provider AND has this connection as its only active option. If so, return 409 with message `"cannot disable: this is the last active connection for one or more model profiles"`.
- Usage endpoint `period=7d` is defined as the last 7 complete UTC days (not including today). `period=today` is the current UTC day. `period=YYYY-MM-DD` is exactly that day. All period calculations use UTC.
- The `display_name` in usage response rows should be resolved from the appropriate table: for `rollup_type='agent'`, join to `agent.name`; for `rollup_type='project'`, join to `project.name`; for `rollup_type='provider_connection'`, join to `provider_connection.display_name`. Return null if the referenced entity has been deleted (rollup rows are retained for billing history).
