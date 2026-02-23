# 013: Agent Schema

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | M (1–2 days) |
| Spec refs | doc 05 §AgentTable, doc 05 §AgentProfileTemplate, doc 14 §StarterTrio |
| Spec status | finished |
| Depends on | 003, 012 |
| Blocks | 014, 015, 025, 026 |

## Scope

Build schema and repository layers for the two agent-domain tables: `agent` and
`agent_profile_template`. This task delivers the DDL migrations and repository interfaces
only — lifecycle business logic lives in task 014.

### Must build

**Migrations:**
- `0015_agent.sql`
- `0016_agent_profile_template.sql`

**`agent` table** (doc 05):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `display_name text not null`
- `agent_class text not null check (agent_class in ('staff','temp'))`
- `lifecycle_status text not null check (lifecycle_status in ('draft','active','paused','retired','cancelled','expired','promoted'))`
- `system_prompt text not null default ''`
- `operator_instructions text not null default ''`
- `agent_type text not null check (agent_type in ('pm','worker','reviewer','general'))`
- `is_starter_trio boolean not null default false` — Frank, Lori, Ellie; guarded against profile deletion
- `private_memory boolean not null default false` — doc 05: true for staff PMs, Frank, Lori, Ellie; false for all others
- `memory_read_scopes text[] not null default '{}'` — e.g. `{org, project, agent}`
- `tool_allow_list text[] not null default '{}'` — glob patterns; empty = allow all
- `tool_deny_list text[] not null default '{}'` — glob patterns; empty = deny none
- `default_model_profile_id text` — references `model_profile.logical_profile_id` (application-layer, no SQL FK)
- `budget_cap_tokens bigint` — per-agent token cap; see ISSUE #1
- `budget_period text check (budget_period in ('daily','weekly','monthly'))` — null = no rolling period
- `temp_scope_type text check (temp_scope_type in ('project','project_task','chat_session','ttl'))` — null for staff
- `temp_scope_id uuid` — see ISSUE #8 for ttl-type semantics
- `temp_ttl_seconds integer` — null unless temp_scope_type='ttl'
- `temp_expires_at timestamptz` — computed from created_at + temp_ttl_seconds; null for staff
- `promoted_to_agent_id uuid references agent(id)` — set when a temp is promoted; null otherwise
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id, lifecycle_status)`
- Index: `(organization_id, agent_class, lifecycle_status)` — concurrent temp limit query
- Index: `(temp_expires_at) WHERE temp_expires_at IS NOT NULL` — expiry scheduler scan

**`agent_profile_template` table** (doc 05):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid references organization(id) on delete cascade` — null = system-provided template
- `display_name text not null`
- `source text not null check (source in ('system','org','promoted'))` — origin of this template
- `source_agent_id uuid references agent(id) on delete set null` — agent this was derived from
- `system_prompt text not null default ''`
- `operator_instructions text not null default ''`
- `agent_type text not null check (agent_type in ('pm','worker','reviewer','general'))`
- `default_model_profile_id text` — logical_profile_id reference (application-layer)
- `tool_allow_list text[] not null default '{}'`
- `tool_deny_list text[] not null default '{}'`
- `memory_read_scopes text[] not null default '{}'`
- `private_memory boolean not null default false`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id) WHERE organization_id IS NOT NULL`

**Repository layer:**
- `AgentRepo`: `Create`, `GetByID`, `List`, `Update`, `SetLifecycleStatus`, `ListActiveTemps`, `ListExpiredTemps`, `CountActiveTemps`, `GetStarterTrio`
- `AgentProfileTemplateRepo`: `Create`, `GetByID`, `List`, `Update`, `Delete`

**Bootstrap integration (task 012 stub replacement):**
- Implement `Bootstrapper.RegisterStep("create-starter-trio", fn)` that creates Frank, Lori, and Ellie in `agent_class='staff'`, `lifecycle_status='active'`, `is_starter_trio=true`, using the system_prompts from doc 14; idempotent (check by display_name + is_starter_trio)

### Must NOT build
- Agent lifecycle state machine transitions (task 014)
- Agent service methods: pause, retire, temp auto-retirement, promotion workflow (task 014)
- Agent project assignment or skill attachment tables (task 025)
- Agent API endpoints (task 015)

## Acceptance Criteria

- [ ] Migration `0015_agent.sql` applies cleanly; all columns and indexes created
- [ ] Migration `0016_agent_profile_template.sql` applies cleanly
- [ ] `agent.lifecycle_status` check constraint rejects any value not in the 7-value set
- [ ] `agent.agent_class` + `temp_scope_type` consistency: a staff agent row with `temp_scope_type` set is accepted at the DB level (constraint is application-layer per ISSUE #5 — do not add a compound check constraint)
- [ ] `AgentRepo.Create` inserts a row and returns the full struct including generated ID and timestamps
- [ ] `AgentRepo.CountActiveTemps` returns the count of `agent_class='temp'` AND `lifecycle_status='active'` rows for a given org
- [ ] `AgentRepo.GetStarterTrio` returns exactly 3 rows where `is_starter_trio=true`
- [ ] Bootstrap step 7 stub replaced: fresh bootstrap creates Frank, Lori, Ellie with correct fields; second bootstrap run does not create duplicate agents
- [ ] `agent_profile_template` with `organization_id=null` (system template) is permitted by the schema

## Tests Required

**Unit tests:**
- `AgentRepo` field mapping: verify that all nullable columns (temp_scope_type, temp_expires_at, promoted_to_agent_id, budget_cap_tokens, budget_period) marshal to/from Go structs correctly (nil pointer vs zero value)
- Bootstrap step 7 idempotency check function in isolation

**Integration tests:**
- `AgentRepo.Create` + `GetByID`: round-trip for staff agent and temp agent variants
- `AgentRepo.CountActiveTemps`: seeded with 3 active temps, 1 expired temp, 1 staff → returns 3
- `AgentRepo.ListExpiredTemps`: only rows with `temp_expires_at < now()` and `lifecycle_status='active'` returned
- `AgentProfileTemplateRepo` CRUD: org-scoped template and system template (org=null) both insertable
- Bootstrap step 7 replacement: register step, run bootstrap, verify Frank/Lori/Ellie created; run again, verify no duplicates

**E2E tests:**
- None — covered by dedicated E2E task 085

## Implementer Notes

> ✅ ISSUE #1 (RESOLVED): Column is `budget_cap_tokens bigint` (tokens). Doc 05 updated. Dollar/cent units are never used in the data layer.

> ✅ ISSUE #23 (RESOLVED): Budget enforcement is hierarchical/additive. The per-agent `budget_cap_tokens`/`budget_period` columns work within the three-level hierarchy: a single invocation is charged to agent, project, and org levels simultaneously. All three levels are checked before dispatch in task 053. `BudgetService.CheckBudget` (task 023) accepts an `agentID *uuid.UUID` parameter and checks the agent cap alongside org/project limits. This task delivers the schema; enforcement is wired in task 053.

- ISSUE #5 (AMBIGUOUS): No DB check constraint prevents a staff agent from reaching `expired` or a temp agent from reaching `draft`. This is application-layer enforcement only (implemented in task 014). Do not add a compound check on `(agent_class, lifecycle_status)` — document this as intentional in a code comment.
- ISSUE #8 (GAP): When `temp_scope_type='ttl'`, `temp_scope_id` semantics are unclear — the spec does not state whether it points to the associated project or is null. Until Sam resolves this, make `temp_scope_id` nullable and add a code comment referencing ISSUE #8.
- The `created_by_type + created_by_id` polymorphic pair follows the canonical 3-type convention (`human_user | agent | system`). The system sentinel UUID is `00000000-0000-0000-0000-000000000000`.
- `promoted_to_agent_id` self-reference: when a temp is promoted to a staff agent, this column is set to the new staff agent's ID. The FK uses `ON DELETE SET NULL` to avoid cascading deletes if the promoted agent is ever retired and deleted.
- The starter trio agents must have `is_starter_trio=true` and must not be deletable via the normal agent delete path. The `AgentRepo.Delete` method (if implemented) must check `is_starter_trio=true` and return `ErrStarterTrioProtected`.
- ISSUE #4 (AMBIGUOUS): `private_memory=true` applies to staff PMs, Frank, Lori, and Ellie only — not all staff. Implement per doc 05 body text until Sam resolves the contradiction with doc 14's bootstrap dataset wording.
