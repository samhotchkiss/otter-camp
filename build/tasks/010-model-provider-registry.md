# 010: Model Provider Registry

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | M (1–2 days) |
| Spec refs | doc 07 §ModelProvider, doc 07 §ProviderConnection, doc 07 §ModelProfile, doc 07 §ModelProfileAssignment |
| Spec status | finished |
| Depends on | 003 |
| Blocks | 012, 035, 036, 037 |

## Scope

Build schema and repository layers for the three model-gateway configuration tables:
`provider_connection`, `model_profile`, and `model_profile_assignment`. The `model_provider`
table was created in task 003 (L0). This task covers the L1 layers that build on top of it,
plus the scope hierarchy resolution logic for profile assignments.

### Must build

**Migrations:**
- `0012_provider_connection.sql`
- `0013_model_profile.sql`
- `0014_model_profile_assignment.sql`

**`provider_connection` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `provider_id uuid not null references model_provider(id)`
- `display_name text not null`
- `api_key_ref text not null` — `ref:<secret-slug>` or raw key (resolved at call time via `secret.ResolveRef`)
- `api_base_url_override text` — null = use model_provider's api_base_url
- `failover_priority integer not null default 100` — lower = tried first; connections with same provider sorted by this
- `health_status text not null default 'healthy' check (health_status in ('healthy','degraded','rate_limited','unavailable'))`
- `is_enabled boolean not null default true`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id, provider_id, failover_priority)` — primary selection query

**`model_profile` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `logical_profile_id text not null` — stable slug identifier (e.g. `high-capability`, `standard`, `haiku`); persists across versions
- `organization_id uuid references organization(id) on delete cascade` — null = system-provided profile
- `version integer not null default 1` — incremented on each update
- `is_current boolean not null default true` — only the latest version is current; old versions kept for audit
- `provider_id uuid not null references model_provider(id)`
- `model_name text not null` — model string sent to the provider API (e.g. `claude-opus-4-5`, `claude-haiku-3-5`)
- `context_window_tokens integer not null` — informational; used for budget layer calculation
- `max_output_tokens integer not null`
- `supports_streaming boolean not null default true`
- `supports_vision boolean not null default false`
- `temperature numeric(3,2)` — null = use provider default
- `invocation_purpose text not null default 'agent_turn' check (invocation_purpose in ('agent_turn','listening_eval','summarization','skill_summarization','memory_extraction','memory_distillation','memory_entity_synthesis','replay'))` — which invocation type this profile is for
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(logical_profile_id, organization_id, version)` (organization_id nullable — use `COALESCE(organization_id, '00000000-0000-0000-0000-000000000000')` in a unique index or a partial index approach)
- Index: `(organization_id, logical_profile_id) WHERE is_current = true`
- `fallback_profile_id text` — references `logical_profile_id` of another profile (application-layer; not a SQL FK)

**`model_profile_assignment` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `scope_type text not null check (scope_type in ('organization','project','agent','flow_node'))`
- `scope_id uuid not null` — no SQL FK (polymorphic application-layer)
- `logical_profile_id text not null` — references `model_profile.logical_profile_id` (application-layer)
- `invocation_purpose text not null` — same check constraint values as `model_profile`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, scope_type, scope_id, invocation_purpose)`

**Repository layer:**
- `ProviderConnectionRepo`: `Create`, `GetByID`, `List`, `Update`, `SetHealthStatus`, `SetEnabled`, `ListByProvider`
- `ModelProfileRepo`: `Create`, `GetCurrentByLogicalID`, `ListCurrent`, `ListAll`, `Deprecate` (sets is_current=false on old version, creates new row for new version)
- `ModelProfileAssignmentRepo`: `Upsert`, `GetByScope`, `ListByOrg`, `Delete`

**Scope hierarchy resolution service** (`internal/model/profile_resolver.go`):
```go
type ProfileResolver interface {
    Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...Scope) (*ModelProfile, error)
}
// Scope: {Type: "flow_node"|"agent"|"project"|"organization", ID: uuid.UUID}
// Resolution order: flow_node > agent > project > organization
// No match at any level: return ErrNoProfileAssigned
// Fallback chain: if resolved profile has fallback_profile_id, follow it (max 3 hops to prevent cycles)
```

### Must NOT build
- Model gateway invocation logic (task 035)
- Health state machine (task 035)
- Usage rollup aggregation (task 036)
- Bootstrap data seeding of profiles and assignments (task 012)

## Acceptance Criteria

- [ ] Migrations `0012`–`0014` apply cleanly in sequence
- [ ] `provider_connection.health_status` check constraint: `'unknown'` is rejected
- [ ] `model_profile` unique index prevents two `is_current=true` rows with the same `(logical_profile_id, organization_id)` pair
- [ ] `model_profile_assignment` unique constraint: upserting the same `(org, scope_type, scope_id, purpose)` tuple updates the existing row rather than inserting a duplicate
- [ ] `ModelProfileRepo.Deprecate`: calling it on a current profile creates a new row with `version+1, is_current=true` and sets `is_current=false` on the old row; both rows persist in the DB
- [ ] `ProfileResolver.Resolve` with scopes `[flow_node, agent, project, org]`: if flow_node has no assignment but agent does, returns the agent-level profile
- [ ] `ProfileResolver.Resolve` with no assignment at any scope level: returns `ErrNoProfileAssigned` (not a default profile)
- [ ] `ProfileResolver.Resolve` follows the `fallback_profile_id` chain: profile A has `fallback_profile_id='B'`; profile B exists → B is returned when A's provider is unavailable
- [ ] `ProfileResolver.Resolve` fallback cycle: A → B → A (cycle) is detected and returns `ErrFallbackCycle` after 3 hops

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `ProfileResolver.Resolve`: scope priority ordering (flow_node wins over agent); no-assignment → error; fallback chain traversal; cycle detection

**Integration tests:**
- All three repos against real PostgreSQL:
  - `ProviderConnectionRepo` CRUD + `SetHealthStatus`
  - `ModelProfileRepo.Deprecate`: verify old row `is_current=false`, new row created
  - `ModelProfileAssignmentRepo.Upsert`: insert, then upsert same key → single row with updated value
  - `ProfileResolver.Resolve` against seeded assignment rows: verify each scope level takes precedence correctly

**E2E tests:**
- None — covered by dedicated E2E task 081 (bootstrap seeds model profiles and org-level assignments)

## Implementer Notes

- ISSUE #11 (RESOLVED): Bootstrap step 5 (task 012) must create org-level `model_profile_assignment` rows for each profile + purpose combination. If the bootstrap does not create these rows, every fresh org will hit `ErrNoProfileAssigned` on the first model call. The resolver must NOT have a hidden default — the error must surface clearly with a message explaining that the org has no model profile assignment for the requested purpose.
- The `logical_profile_id` + `is_current=true` pattern is how model profiles are versioned without breaking existing assignments: assignments reference the logical ID, not the row ID. When a profile is updated, a new row is created with `version+1`, and the old row gets `is_current=false`. The resolver always queries `WHERE is_current=true`.
- `fallback_profile_id` is intentionally NOT a SQL FK because the fallback target may be a system-provided profile (`organization_id = null`) while the primary profile is org-scoped. Cross-null FK constraints are awkward in PostgreSQL. Application-layer resolution is cleaner.
- `invocation_purpose` on `model_profile` determines which system prompt variant is used. The `agent_turn` profile is the default for conversational agent responses. `listening_eval` uses a Haiku-class model. `summarization` is for progressive summarization. `memory_extraction` / `memory_distillation` / `memory_entity_synthesis` are memory pipeline purposes. `replay` is for deterministic test replay.
- The `supported_features` array on `model_provider` (task 003) must be checked at the service layer before routing a request that requires `vision` or `tool_use` to a provider/model that doesn't support it.
