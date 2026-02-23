# 033: Capability Policy Schema and Service

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 16 §CapabilityPolicy, doc 16 §PolicyEvaluation, doc 16 §PolicyCaching, doc 16 §BudgetGate |
| Spec status | finished |
| Depends on | 003, 016, 013, 023 |
| Blocks | 034, 049, 052, 055 |

## Scope

Build the capability policy system: `capability_policy` DDL, the five-layer policy evaluation
engine (most-restrictive-wins), policy caching, and the budget gate integration. This is the
access control backbone used by the control plane broker (task 052) and tool resolution
pipeline (task 049).

### Must build

**Migrations:**
- `0042_capability_policy.sql`

**`capability_policy` table** (doc 16):
- `id uuid primary key default gen_random_uuid()`
- `policy_layer text not null check (policy_layer in ('instance','org','project','agent_profile','request'))` — five layers; evaluated from broadest (instance) to narrowest (request)
- `organization_id uuid references organization(id) on delete cascade` — null for instance-layer rows
- `project_id uuid references project(id) on delete cascade` — non-null only for project-layer rows
- `agent_id uuid references agent(id) on delete cascade` — non-null only for agent_profile-layer rows
- `capability text not null` — capability identifier string (e.g. `system.file.write`, `mcp.connection.use`, `browser.navigate`)
- `effect text not null check (effect in ('allow','deny'))` — allow or deny
- `conditions jsonb not null default '{}'` — optional conditions (e.g. `{max_file_size_kb: 512}`); evaluated at application layer
- `priority integer not null default 100` — within a layer, lower number = evaluated first; most layers have one rule per capability but priority allows ordering when there are multiple
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id, policy_layer, capability)` — primary lookup
- Index: `(project_id, capability) WHERE project_id IS NOT NULL` — project-layer lookup
- Index: `(agent_id, capability) WHERE agent_id IS NOT NULL` — agent_profile-layer lookup
- Partial unique index: `(capability, policy_layer) WHERE policy_layer = 'instance'` — only one instance rule per capability
- Check constraint: `NOT (policy_layer = 'instance' AND organization_id IS NOT NULL)` — instance rows have no org scoping
- Check constraint: `NOT (policy_layer = 'project' AND project_id IS NULL)` — project rows must have project_id
- Check constraint: `NOT (policy_layer = 'agent_profile' AND agent_id IS NULL)` — agent_profile rows must have agent_id

**Repository layer:**
- `CapabilityPolicyRepo`: `Create`, `GetByID`, `ListByLayer`, `ListForEvaluation`, `Update`, `Delete`
- `ListForEvaluation(ctx, orgID, projectID *uuid.UUID, agentID *uuid.UUID, capability string) ([]CapabilityPolicy, error)` — returns all rows affecting this evaluation context, ordered by layer (instance first, then org, project, agent_profile, request)

**Policy evaluation engine (`internal/policy/evaluator.go` — `PolicyEvaluator`):**

**Five-layer evaluation (most-restrictive-wins):**
1. **Instance layer** — compiled at startup from `policy_layer='instance'` rows; refreshed on deploy; cannot be overridden
2. **Org layer** — `policy_layer='org'` rows for the request's organization
3. **Project layer** — `policy_layer='project'` rows for the request's project (if any)
4. **Agent profile layer** — `policy_layer='agent_profile'` rows for the requesting agent (if any)
5. **Request-specific** — `policy_layer='request'` rows attached to the current run/session context

**Evaluation rules (doc 16):**
- Silence (no rule found at any layer) = **allow** (pass-through)
- A `deny` at ANY layer is **absolute** — no lower layer can override it
- An `allow` at a lower layer does NOT override a `deny` at a higher layer
- If a layer has multiple rules for the same capability: `deny` wins within the layer before cross-layer evaluation
- Conditions on an `allow` rule: all conditions must be satisfied; if conditions fail, treat as no rule found (not deny) — the next layer is checked

**`Evaluate(ctx, req EvaluationRequest) PolicyDecision`:**
```go
type EvaluationRequest struct {
    OrganizationID uuid.UUID
    ProjectID      *uuid.UUID
    AgentID        *uuid.UUID
    Capability     string
    Context        map[string]any  // additional context for condition evaluation
}

type PolicyDecision struct {
    Effect     string  // "allow" | "deny"
    Layer      string  // which layer made the decision
    Reason     string  // human-readable explanation
    Conditions map[string]any  // conditions from the matching rule (if any)
}
```

**Instance policy — compile at startup:**
```go
func (e *PolicyEvaluator) LoadInstancePolicies(ctx context.Context) error
// Reads all policy_layer='instance' rows; compiles into in-memory map
// Called once at startup; re-called on deploy/reload signal
```

**Policy caching (doc 16):**
- Org-layer and project-layer policies: cached with TTL = 5 minutes; key = `(org_id, capability)` and `(project_id, capability)`
- Agent-profile-layer policies: cached per session start; invalidated when agent's policy rows are modified
- Instance policies: in-memory, no TTL, refreshed only on startup/deploy
- Cache implementation: use a simple in-process `sync.Map` with TTL entries; no Redis required

**Budget gate integration:**
- `CheckBudgetGate(ctx, orgID uuid.UUID, projectID *uuid.UUID, agentID *uuid.UUID) (allowed bool, reason string)`
- Calls `TokenBudgetService.CheckLimits` (task 023) to verify hard limits are not exceeded
- A budget hard-limit breach returns an implicit `deny` effect equivalent to a policy deny — even if capability policies allow the action
- Budget gate is checked AFTER policy evaluation (a policy deny always wins without budget check)

### Must NOT build
- Capability policy API endpoints (task 034)
- Broker dispatch pipeline (task 055)
- Tool resolution pipeline (task 049)
- Budget anomaly detection (already in task 023)
- Control plane run creation (task 052)

## Acceptance Criteria

- [ ] Migration `0042_capability_policy.sql` applies cleanly; all three check constraints are present
- [ ] Partial unique index prevents two instance-layer rows for the same capability
- [ ] `Evaluate` with no matching rules returns `{effect: "allow", layer: "none", reason: "silence passes"}`
- [ ] `Evaluate` with org-layer `deny` returns deny even when agent-profile layer has `allow` for same capability
- [ ] `Evaluate` with instance-layer `deny` returns deny regardless of all other layers
- [ ] `LoadInstancePolicies` populates the in-memory map from DB rows; subsequent `Evaluate` uses cached values (no DB query for instance layer)
- [ ] `CheckBudgetGate` returns `allowed=false` when `TokenBudgetService.CheckLimits` returns hard-limit exceeded
- [ ] Policy cache TTL: modify an org-layer policy row; within 5 minutes, stale cached decision is still returned; after TTL expiry, fresh decision reflects the update

## Tests Required

**Unit tests:**
- Evaluation precedence: table-driven tests covering all layer combinations; deny at instance overrides allow at all lower layers; deny at project overrides allow at agent_profile
- Silence = allow: no rules for a capability → `Evaluate` returns allow
- Condition evaluation: allow rule with `{max_file_size_kb: 512}` → context with `file_size_kb=256` → allow; context with `file_size_kb=1024` → falls through to next layer (not deny)
- Multi-rule within layer: org-layer has two rules for same capability — allow at priority 50 and deny at priority 100; deny wins within layer → result is deny

**Integration tests:**
- `ListForEvaluation` returns rows from all applicable layers ordered by instance, org, project, agent_profile
- Cache TTL: insert org-layer deny; evaluate (cached); update to allow in DB; evaluate again within 5 minutes → still deny (stale cache); wait for expiry → allow
- Budget gate integration: set `token_budget.hard_limit_reached=true` for org; `CheckBudgetGate` returns `allowed=false`

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

> ⚠️ ISSUE #17 (AMBIGUOUS): Doc 16 states that instance-level policy rows "cannot be overridden" but there is no DB constraint or API-level enforcement preventing an org admin from inserting rows with `policy_layer='instance'`. Implement write protection at the API handler layer (task 034): reject `Create` and `Update` calls for `policy_layer='instance'` unless the caller's `created_by_type='system'` or the server is in bootstrap mode. Log an audit event for any such attempt. Do not rely on DB constraints alone.

- ISSUE #23 (BLOCKER): Budget enforcement path is partially blocked. `CheckBudgetGate` integrates with `TokenBudgetService` from task 023, but the interaction between per-agent, per-project, and per-org limits is unresolved (ISSUE #23). Implement budget gate as: check org-level hard limit → check project-level hard limit (if project scoped) → skip agent-level check until ISSUE #23 is resolved. Add a `// TODO: ISSUE #23 — add per-agent budget check once units/hierarchy resolved` comment.
- The `request` policy layer (5th layer) is used for ephemeral policies attached to a specific run or session (e.g., a temp agent created for a session gets additional capabilities). These rows are created and deleted as part of run lifecycle management (task 052). This task only builds the schema and evaluator; the request-layer population is in task 052.
- The condition evaluation engine for `conditions jsonb` is intentionally minimal in this task: support only `{max_file_size_kb, max_token_count, allowed_domains}` as known condition keys. Unknown condition keys are ignored (treated as always-satisfied). Document this limitation as a known gap for future extension.
- The in-process cache (`sync.Map` with TTL) is acceptable for a single-node deployment. Multi-node invalidation is not required in V2. Add a `// NOTE: cache is per-process — multiple workers will have independent caches with independent TTLs` comment to the implementation.
