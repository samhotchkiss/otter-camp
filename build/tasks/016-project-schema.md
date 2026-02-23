# 016: Project Schema

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 03 §ProjectTable, doc 03a §DeliveryMode, doc 03 §FlowTemplate, doc 03 §TaskSchedule |
| Spec status | finished |
| Depends on | 003, 005, 012 |
| Blocks | 017, 018, 019, 027, 031 |

## Scope

Build schema and repository layers for the three project-domain foundation tables:
`project`, `flow_template`, and `task_schedule`. The `flow_node` table that builds on
`flow_template` is a separate task (017) due to its BLOCKER issue. This task delivers
migrations and repository interfaces only — project service logic is in task 018.

### Must build

**Migrations:**
- `0017_project.sql`
- `0018_flow_template.sql`
- `0019_task_schedule.sql`

**`project` table** (doc 03, doc 03a):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `slug text not null` — URL-safe identifier; unique within org
- `display_name text not null`
- `description text not null default ''`
- `delivery_mode text not null default 'gated' check (delivery_mode in ('continuous','gated','scheduled'))` — doc 03a
- `deploy_flow_template_id uuid` — nullable; references `flow_template(id)` for deploy tasks (Pattern 2); FK deferred to after flow_template table exists (added in this migration via ALTER TABLE or same migration after flow_template)
- `settings jsonb not null default '{}'`
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, slug)`
- Index: `(organization_id, created_at DESC)` — list query

**`flow_template` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid references organization(id) on delete cascade` — null = system-provided template
- `project_id uuid references project(id) on delete cascade` — null = org-wide or system template
- `slug text not null` — stable identifier
- `display_name text not null`
- `description text not null default ''`
- `is_current boolean not null default true` — only the latest version is current; old versions kept
- `version integer not null default 1`
- `start_node_id uuid` — references `flow_node(id)`; nullable initially; populated via FK added in task 017
- `is_system boolean not null default false` — true = system-provided (bootstrap-seeded)
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, project_id, slug, version)` — use partial index with COALESCE for nullable columns
- Index: `(organization_id, project_id, is_current) WHERE is_current = true`
- Index: `(project_id) WHERE project_id IS NOT NULL`

**`task_schedule` table** (doc 03):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid not null references project(id) on delete cascade`
- `flow_template_id uuid not null references flow_template(id)` — which template to instantiate
- `display_name text not null`
- `cron_expression text not null` — validated at service layer; stored raw in DB
- `overlap_policy text not null default 'skip' check (overlap_policy in ('skip','queue','cancel_existing'))` — behavior when tick fires and existing task is pending/in_progress
- `max_duration_ms bigint` — null = no timeout; task auto-cancelled if exceeded
- `is_enabled boolean not null default true`
- `last_fired_at timestamptz` — updated by scheduler on each tick
- `next_fire_at timestamptz` — pre-computed; updated after each fire
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(project_id, is_enabled) WHERE is_enabled = true`
- Index: `(next_fire_at) WHERE is_enabled = true` — scheduler scan

**Repository layer:**
- `ProjectRepo`: `Create`, `GetByID`, `GetBySlug`, `List`, `Update`, `Delete`
- `FlowTemplateRepo`: `Create`, `GetByID`, `GetCurrentBySlug`, `ListCurrent`, `ListAll`, `Deprecate` (creates new version row, sets old is_current=false), `BulkUpsertBySlug`
- `TaskScheduleRepo`: `Create`, `GetByID`, `List`, `Update`, `Enable`, `Disable`, `UpdateLastFired`, `ListDue`

**Bootstrap integration (task 012 stub replacement for step 6):**
- Implement `Bootstrapper.RegisterStep("seed-flow-templates", fn)` that creates the two system templates (`default-single-agent`, `default-review`) using `FlowTemplateRepo.BulkUpsertBySlug`; idempotent via upsert

### Must NOT build
- `flow_node` table (task 017 — blocked by ISSUE #16)
- Project service CRUD logic (task 018)
- Project API endpoints (task 019)
- `project_task`, `inbox_item`, `merge_queue_entry` (task 027)

## Acceptance Criteria

- [ ] Migrations `0017`–`0019` apply cleanly in sequence with no FK violations
- [ ] `project` unique constraint on `(organization_id, slug)` rejects duplicate slugs within org
- [ ] `project` allows two projects with identical slugs in different orgs
- [ ] `flow_template.is_current` partial index: `(organization_id, project_id, is_current) WHERE is_current = true` is present
- [ ] `FlowTemplateRepo.Deprecate`: old row gets `is_current=false`, new row created with `version+1, is_current=true`; both rows persist
- [ ] `FlowTemplateRepo.BulkUpsertBySlug`: running twice with same slug list does not create duplicates
- [ ] `task_schedule.overlap_policy` check constraint rejects any value not in the 3-value set
- [ ] Bootstrap step 6 replacement: system flow templates created on bootstrap; second run is no-op

## Tests Required

**Unit tests:**
- `FlowTemplateRepo.Deprecate` idempotency: verify calling deprecate twice does not create duplicate new-version rows
- `TaskScheduleRepo.ListDue`: given a mix of due and future schedules, only due+enabled ones returned

**Integration tests:**
- `ProjectRepo.Create` + `GetBySlug`: round-trip; unique constraint tested (same slug + org → error)
- `FlowTemplateRepo.Deprecate`: real PostgreSQL; verify row counts before and after
- `TaskScheduleRepo.UpdateLastFired` + `ListDue`: seed schedules with past/future `next_fire_at`; verify ListDue returns only due ones
- Bootstrap step 6: register step, run bootstrap, verify system templates in DB; second bootstrap, verify no duplicates

**E2E tests:**
- None — covered by dedicated E2E task 084

## Implementer Notes

- The `start_node_id` FK on `flow_template` references `flow_node(id)` which does not exist until task 017. Add the FK as a deferred constraint or via a separate `ALTER TABLE` migration in task 017. Do not create the FK in this migration — it will fail because `flow_node` does not yet exist.
- `delivery_mode` on `project` is used by the delivery engine (task 031/064). This task only stores the value; enforcement is in the delivery service.
- The `deploy_flow_template_id` FK similarly references `flow_template(id)`. It can be added in the same migration as `flow_template` since both tables are in the same migration file, or as an `ALTER TABLE` after both tables exist.
- Slug format validation is application-layer: lower-case alphanumeric and hyphens only, max 64 characters. Add a `CHECK (slug ~ '^[a-z0-9-]+$')` constraint in the migration.
- `task_schedule.next_fire_at` is pre-computed by the service layer after each fire (task 065). The repo method `ListDue` queries `WHERE next_fire_at <= now() AND is_enabled = true`.
- System flow templates have `is_system=true`, `organization_id=null`, `project_id=null`. They are shared across all orgs and cannot be modified via the project API (enforced in task 018).
