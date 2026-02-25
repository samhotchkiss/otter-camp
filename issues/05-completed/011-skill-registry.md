# 011: Skill Registry Schema and Service

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | S (≤1 day) |
| Spec refs | doc 10 §SkillSchema, doc 10 §SkillService, doc 10 §SkillCLI |
| Spec status | finished |
| Depends on | 003 |
| Blocks | 012, 017, 025 |

## Scope

Create the `skill` table DDL and migration, implement the skill repository and service
(org-scope vs project-scope skills, file_path pointer convention, skill registry consistency
check), and wire the CLI hooks for skill create/update/delete.

Note: `skill.project_id` is nullable — an org-scoped skill has no project FK. The project
table does not yet exist at L1. The FK constraint for `project_id` will be added as an
`ALTER TABLE` in task 016 (when the `project` table is created at L2). For now, `project_id`
is stored as a plain `uuid` with no FK constraint.

### Must build

**Migration:**
- `0015_skill.sql`

**`skill` table** (doc 10):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid` — no FK constraint yet (project table not yet defined); null = org-scoped skill
- `slug text not null` — URL-safe, lowercase alphanumeric + hyphens
- `display_name text not null`
- `description text not null`
- `file_path text not null` — relative path within the org's skills repository (e.g. `skills/summarize.md`)
- `version integer not null default 1`
- `is_active boolean not null default true`
- `created_by_type text not null check (created_by_type in ('human', 'agent', 'system'))`
- `created_by_id uuid not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Partial unique index: `(organization_id, slug) WHERE project_id IS NULL` — org-scoped slug uniqueness
- Partial unique index: `(project_id, slug) WHERE project_id IS NOT NULL` — project-scoped slug uniqueness

**Repository layer:**
- `SkillRepo`: `Create`, `GetByID`, `GetBySlug`, `ListByOrg`, `ListByProject`, `Update`, `SetActive`, `BulkUpsertBySlug` (for bootstrap seeding)

**`internal/skill/` package:**

`Service` interface:
```go
type Service interface {
    Create(ctx context.Context, orgID uuid.UUID, req CreateRequest) (*Skill, error)
    Update(ctx context.Context, skillID uuid.UUID, req UpdateRequest) (*Skill, error)
    Delete(ctx context.Context, skillID uuid.UUID) error  // soft delete: sets is_active=false
    Get(ctx context.Context, skillID uuid.UUID) (*Skill, error)
    List(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID) ([]*Skill, error)
    CheckConsistency(ctx context.Context, orgID uuid.UUID, skillsDir string) (*ConsistencyReport, error)
}

type ConsistencyReport struct {
    MissingFiles   []string  // skills in DB with no file at file_path
    UnregisteredFiles []string  // .md files in skillsDir not in DB
    Mismatches     []string  // DB slug doesn't match filename convention
}
```

**`CheckConsistency`:**
- Lists all `is_active=true` skills for the org
- Scans `skillsDir` (default: `./skills/`) for `*.md` files
- Reports skills in DB whose `file_path` has no corresponding file on disk
- Reports `.md` files in the directory that have no corresponding skill in DB
- Does NOT auto-create or auto-delete; only reports

**CLI commands** (wired to `Service`):
- `ottercamp skill create --slug <s> --display-name <n> --file-path <path>` — creates org-scoped skill
- `ottercamp skill update --slug <s> [--display-name <n>] [--file-path <path>]`
- `ottercamp skill delete --slug <s>` — soft delete (sets is_active=false)
- `ottercamp skill list` — table of slug, display_name, file_path, version, is_active
- `ottercamp skill check` — runs `CheckConsistency`, prints report; exits 1 if any issues found

### Must NOT build
- `flow_node_skill` join table (that is part of task 017 — flow node schema)
- `agent_skill_attachment` table (task 025)
- Project-scoped skills (the partial unique index supports them, but the project_id FK constraint and project-aware service logic wait until task 016 adds the `project` table)
- Skill file content loading or rendering (that is the prompt assembly responsibility, task 050)

## Acceptance Criteria

- [ ] Migration `0015` applies cleanly; both partial unique indexes exist
- [ ] Org-scoped unique constraint: two skills with the same slug and `project_id IS NULL` in the same org raises a constraint violation; same slug under different orgs is allowed
- [ ] Project-scoped unique constraint: two skills with the same slug and same `project_id` raises a violation; same slug with different `project_id` is allowed
- [ ] `SkillRepo.GetBySlug` with `projectID=nil` returns the org-scoped skill; with a project UUID returns the project-scoped skill
- [ ] `Service.Delete` sets `is_active=false` (soft delete); the row is still retrievable by ID with `is_active=false`
- [ ] `Service.CheckConsistency` reports a missing file when a skill's `file_path` does not exist on disk; reports an unregistered file when a `.md` file exists with no matching skill in DB
- [ ] `SkillRepo.BulkUpsertBySlug` inserts new skills and updates existing skills by `(organization_id, slug)` in a single statement

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `CheckConsistency`: mock SkillRepo returning known skills; mock filesystem (use `t.TempDir()` with planted files); verify all three report categories
- `Service.Create`: slug validation (lowercase alphanumeric+hyphens only; error on spaces or uppercase)

**Integration tests:**
- `SkillRepo` against real PostgreSQL:
  - CRUD round-trip for org-scoped skill
  - Partial unique index: duplicate org-scoped slug → error; same slug in different org → success
  - `BulkUpsertBySlug`: insert 3 skills, call again with 2 updated + 1 new → 4 total rows
  - `ListByOrg` returns only `is_active=true` by default; with `include_inactive=true` returns all
  - Soft delete: `Service.Delete` → `ListByOrg` doesn't include it; `GetByID` still returns it

**E2E tests:**
- None — covered by dedicated E2E task 081 (bootstrap seeds default org skills)

## Implementer Notes

- The `file_path` convention: skills are stored as Markdown files in a `skills/` directory at the org level. The `file_path` is a relative path within that directory (e.g., `skills/summarize-meeting.md`). The actual file is stored in object storage under `orgs/{org_id}/skills/{file_path}` (or on the local filesystem in development mode).
- Version tracking on `skill`: the `version` column is incremented on every `Update`. This is an application-layer increment (`UPDATE ... SET version = version + 1`), not a separate version row. Old content is not retained in the DB — the skill file in object storage is overwritten. If version history is needed, git is the source of truth.
- `created_by_type='system'` with sentinel UUID is used for bootstrap-seeded skills. The sentinel UUID is `00000000-0000-0000-0000-000000000000`.
- The project_id FK constraint deferred to task 016: the migration in this task omits the FK. Task 016 adds `ALTER TABLE skill ADD CONSTRAINT skill_project_id_fkey FOREIGN KEY (project_id) REFERENCES project(id) ON DELETE CASCADE` in its own migration file. Until then, any `project_id` value can be stored without referential integrity enforcement — this is acceptable because project-scoped skills are not usable until the project table exists.
