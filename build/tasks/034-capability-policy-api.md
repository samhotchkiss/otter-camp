# 034: Capability Policy API Endpoints

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 16 §CapabilityPolicyAPI, doc 16 §PolicyEvaluateDryRun |
| Spec status | finished |
| Depends on | 033, 007 |
| Blocks | 054, 077, 087 |

## Scope

Build the HTTP endpoints for capability policy management plus the dry-run evaluation
endpoint. Includes instance-layer write protection enforcement at the API level.

### Must build

**Policy management endpoints:**
- `GET /v1/control/policies` — list policies; query params: `policy_layer`, `capability`, `project_id`, `agent_id`; returns paginated list
- `POST /v1/control/policies` — create a new policy rule; body: `{policy_layer, organization_id?, project_id?, agent_id?, capability, effect, conditions?, priority?}`
- `PUT /v1/control/policies/:id` — replace a policy rule (full replacement, not partial update; use PUT per doc 16)
- `DELETE /v1/control/policies/:id` — delete a policy rule

**Dry-run evaluation endpoint:**
- `POST /v1/control/policies/evaluate` — dry-run evaluate a capability for a given context; does NOT create any records; returns the full decision trace
  - Body: `{capability, organization_id, project_id?, agent_id?, context?}`
  - Response: `{data: {effect: "allow"|"deny", layer: string, reason: string, trace: [{layer, effect, matched_rule_id?, reason}]}}`
  - The `trace` array shows evaluation at each layer even if a decision is reached early (useful for debugging policy stacks)

**Request/response shapes:**

POST /v1/control/policies body:
```json
{
  "policy_layer": "org",
  "capability": "system.file.write",
  "effect": "deny",
  "conditions": {},
  "priority": 100
}
```
Response: `{data: {id, policy_layer, capability, effect, conditions, priority, created_at}}`

**Validation rules:**
- `policy_layer` must be one of the five valid values
- `effect` must be `allow` or `deny`
- `priority` integer 1–1000 (default 100)
- For `policy_layer='project'`: `project_id` is required
- For `policy_layer='agent_profile'`: `agent_id` is required
- For `policy_layer='org'` or `'request'`: neither `project_id` nor `agent_id` should be set
- `policy_layer='instance'` write protection: any `POST`, `PUT`, or `DELETE` with `policy_layer='instance'` from a non-system actor is rejected with 403 and message `"instance-layer policies are managed by the system and cannot be modified via the API"`
- `policy_layer='instance'` is visible in `GET` responses (readable but not writable)

**Auth and RBAC:**
- All endpoints require authentication
- `GET` available to any org admin
- `POST`, `PUT`, `DELETE` require org-admin role
- `POST /v1/control/policies/evaluate` available to any authenticated user (useful for agent self-inspection)
- Requests scoped to a `project_id` must verify the project belongs to the caller's organization

### Must NOT build
- Policy evaluation within the broker (task 055)
- Budget gate (task 033)
- Control plane run endpoints (task 054)

## Acceptance Criteria

- [ ] `POST /v1/control/policies` with `policy_layer='instance'` from non-system actor returns 403
- [ ] `DELETE /v1/control/policies/:id` on an instance-layer row returns 403
- [ ] `GET /v1/control/policies?policy_layer=instance` returns instance-layer rows (read-only access)
- [ ] `POST /v1/control/policies/evaluate` with no matching rules returns `{effect: "allow", layer: "none"}`
- [ ] `POST /v1/control/policies/evaluate` trace includes entries for all 5 layers even if deny found at layer 2
- [ ] `POST /v1/control/policies` with `policy_layer='project'` and missing `project_id` returns 422
- [ ] `GET /v1/control/policies?capability=system.file.write` returns only rows matching that capability (across all layers for the org)
- [ ] All write operations are covered by audit events (via `AuditEvent.Record` from task 008)

## Tests Required

**Unit tests:**
- Route registration: all 5 endpoints registered with correct HTTP methods
- Instance-layer write protection: POST/PUT/DELETE with `policy_layer='instance'` → 403; GET → 200
- Validation: `policy_layer='project'` without `project_id` → 422; `effect='maybe'` → 422

**Integration tests (95% coverage target per doc 16):**
- Layer ordering: create org deny + agent_profile allow for same capability; evaluate → deny wins; trace shows deny at org layer
- Deny is absolute: create instance deny + project allow + agent_profile allow; evaluate → deny (instance); trace shows all three layers but decision from instance
- Silence passes: no rules for capability `"unknown.capability"`; evaluate → allow, layer="none"
- Policy CRUD: create → GET → PUT (update effect) → DELETE → GET returns 404
- Org isolation: create policy for org A; org B admin calls GET /v1/control/policies → does not see org A's policies

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

> ⚠️ ISSUE #17 (AMBIGUOUS): Instance-layer write protection is enforced at this API layer. The handler must check `policy_layer='instance'` on any mutating request and return 403 before calling the service layer. Additionally, write attempts should be recorded as audit events with `event_type='policy.instance_write_attempt'` even though the request is rejected — this provides a security trail for unauthorized modification attempts.

- The `POST /v1/control/policies/evaluate` dry-run endpoint always calls `PolicyEvaluator.Evaluate` with the provided context. It must return the full trace even if the first layer produces a deny. This requires the evaluator to support a "collect-all" mode where it continues evaluation after reaching a deny decision, for tracing purposes only.
- The `PUT /v1/control/policies/:id` endpoint is a full replacement. It must validate all required fields for the `policy_layer` type, not just the fields provided in the body. This distinguishes it from PATCH semantics.
- `GET /v1/control/policies` without filter params returns all policies visible to the caller's org (org + project + agent_profile layers within the org, plus instance layer). It does NOT return policies from other organizations.
- All policy write operations (create/update/delete) should invalidate the relevant policy cache entries in `PolicyEvaluator` immediately. Use the evaluator's `InvalidateCache(orgID, capability)` method to trigger cache invalidation. This ensures the stale-cache window is only for reads that happen before an explicit write, not for the period after a write.
