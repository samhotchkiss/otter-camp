# 003: L0 Schema — Foundation Tables

| Field | Value |
|-------|-------|
| Layer | L0 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §Organization, doc 07 §ModelProvider, doc 20 §ToolDefinition, doc 06 §MemoryTaxonomyNode |
| Spec status | finished |
| Depends on | 002 |
| Blocks | 005, 008, 009, 010, 011, 012 |

## Scope

Create DDL migrations and repository layers for the four L0 tables: `organization`,
`model_provider`, `tool_definition`, and `memory_taxonomy_node`. These are the root entities —
no application table depends on anything that comes before them.

### Must build

**Migrations** (in `migrations/`):
- `0002_organization.sql` — `organization` table
- `0003_model_provider.sql` — `model_provider` table
- `0004_tool_definition.sql` — `tool_definition` table
- `0005_memory_taxonomy_node.sql` — `memory_taxonomy_node` table

**`organization` table** (doc 04):
- `id uuid primary key default gen_random_uuid()`
- `slug text not null unique` — URL-safe identifier, lowercase alphanumeric + hyphens
- `display_name text not null`
- `settings jsonb not null default '{}'` — structured settings (agents config, redaction policy, etc.)
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- `archived_at timestamptz` — soft delete; null = active

**`model_provider` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `slug text not null unique` — stable identifier (e.g. `anthropic`, `openai`)
- `display_name text not null`
- `api_base_url text not null`
- `supported_features text[] not null default '{}'` — e.g. `{streaming,vision,tool_use}`
- `is_enabled boolean not null default true`
- `metadata jsonb not null default '{}'`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`

**`tool_definition` table** (doc 20):
- `id uuid primary key default gen_random_uuid()`
- `name text not null unique` — dotted namespace, e.g. `file.read`, `cli.execute`, `browser.navigate`
- `display_name text not null`
- `description text not null`
- `tool_tier text not null check (tool_tier in ('tier1', 'tier2'))` — tier1 = chat-layer, tier2 = capability-gated
- `tool_domain text not null` — e.g. `file`, `cli`, `browser`, `git`, `memory`, `project`, `chat`, `agent`, `system`, `mcp`
- `required_capability text` — capability slug required for tier2 tools (nullable for tier1)
- `input_schema jsonb not null default '{}'` — JSON Schema for tool input
- `is_enabled boolean not null default true`
- `created_at timestamptz not null default now()`

**`memory_taxonomy_node` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `parent_id uuid references memory_taxonomy_node(id) on delete restrict` — null = root node
- `slug text not null` — unique within siblings under the same parent + org
- `display_name text not null`
- `description text`
- `created_at timestamptz not null default now()`
- Unique constraint: `(organization_id, parent_id, slug)` — sibling uniqueness

**Repository layer** (`internal/repo/`):
- `OrgRepo`: `Create`, `GetByID`, `GetBySlug`, `List`, `Archive`, `Update`, `UpdateSettings`
- `ModelProviderRepo`: `Create`, `GetByID`, `GetBySlug`, `List`, `SetEnabled`
- `ToolDefinitionRepo`: `Create`, `GetByName`, `ListByDomain`, `ListByTier`, `SetEnabled`, `BulkUpsert` (for startup population)
- `MemoryTaxonomyNodeRepo`: `Create`, `GetByID`, `GetBySlug`, `ListChildren`, `ListSubtree`, `Delete`

All repos accept `context.Context` as first arg; take a `*pgxpool.Pool` (or `db.Router`).

### Must NOT build
- Human user, auth session, or any L1+ tables (tasks 005+)
- Model profiles or provider connections (task 010)
- Bootstrap data seeding (task 012)
- Any HTTP handlers

## Acceptance Criteria

- [ ] Migrations `0002` through `0005` apply cleanly on a fresh database in sequence; running them again is a no-op
- [ ] `organization` table exists with all columns, correct types, `slug` unique constraint, and `archived_at` nullable
- [ ] `model_provider` table exists with `slug` unique constraint and `supported_features text[]`
- [ ] `tool_definition` table exists with `tool_tier check` constraint (only `tier1` or `tier2` accepted); inserting a third value raises a constraint violation
- [ ] `memory_taxonomy_node` table exists with `parent_id` self-reference and `(organization_id, parent_id, slug)` unique constraint; `parent_id` delete restrict prevents deleting a node that has children
- [ ] `OrgRepo.GetBySlug` returns the correct organization or `ErrNotFound`
- [ ] `MemoryTaxonomyNodeRepo.ListSubtree` returns all descendants of a given node (recursive CTE)
- [ ] `ToolDefinitionRepo.BulkUpsert` inserts new rows and updates existing rows by `name` in a single statement (INSERT ... ON CONFLICT DO UPDATE)

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `OrgRepo.UpdateSettings`: test that settings are merged/replaced correctly (full replace, not partial merge — confirm with spec behavior)
- `MemoryTaxonomyNodeRepo.ListSubtree`: test recursive CTE with 3-level deep taxonomy using an in-memory mock or parameterized SQL test

**Integration tests:**
- All four repo types against real PostgreSQL (via `testdb.New(t)`):
  - CRUD round-trips for all four entities
  - Unique constraint violations return typed errors (not raw pgx errors)
  - `memory_taxonomy_node` parent delete restrict: inserting a child, then deleting the parent fails
  - `memory_taxonomy_node.ListSubtree`: seed a 3-level tree, verify all 6 descendants are returned
  - `tool_definition.BulkUpsert`: insert 5 tools, call again with 3 updated + 2 new, verify 7 total rows with updated fields

**E2E tests:**
- None — covered by dedicated E2E task 081

## Implementer Notes

- `organization.settings` is a free-form jsonb document. The shape is defined by convention, not a DB constraint. Known top-level keys from the spec: `agents` (object with `max_concurrent_temps` integer), `redaction` (object with `enabled` boolean, `policy` string). Use Go struct tags + jsonb scanning, not raw map access.
- `tool_definition` rows are populated at server startup by `ToolDefinitionRepo.BulkUpsert` from a compiled-in registry — not by a migration. The migration creates the table; the startup hook seeds the rows. This means the table may be empty immediately after migration, before the server runs for the first time. Tests that depend on tool definitions must either call `BulkUpsert` in setup or use `testdb` with a seeded template.
- `memory_taxonomy_node.parent_id` references itself (same table). PostgreSQL supports self-referencing FKs in a single `CREATE TABLE` statement with no issues. The `ON DELETE RESTRICT` prevents orphaning children. To delete a subtree, delete leaves first.
- `model_provider` rows are seeded during bootstrap (task 012), not by a migration. The migration creates an empty table.
- The `updated_at` column on `organization` and `model_provider` should be kept current via an `UPDATE` trigger or explicit application-layer update on every write. Use a shared trigger function `set_updated_at()` created in a separate migration (`0006_shared_triggers.sql`) and reused across all tables that have `updated_at`.
