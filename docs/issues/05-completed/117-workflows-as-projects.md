# Issue #117 — Workflows Are Projects

> STATUS: READY

## Problem

Workflows and projects are two separate concepts with separate data models, separate pages, separate UI. But a workflow is just a project that creates issues on a schedule. Maintaining two parallel primitives adds complexity without adding value.

## Change

**Kill the separate workflows concept.** A workflow becomes a project with:
1. A cron schedule (when to create issues)
2. An issue template (what each issue looks like)
3. Pipeline config (auto-close? needs review? which agent?)

The Workflows page becomes a filtered view of projects that have cron schedules.

## Current State

### What exists now
- `WorkflowsPage.tsx` — standalone page reading from OpenClaw cron jobs via `/api/workflows`
- `internal/api/workflows.go` — handler that derives workflow data from OpenClaw cron jobs (not from any OtterCamp table)
- Cron jobs live in OpenClaw, not OtterCamp
- No relationship between workflows and projects/issues

### What's wrong
- Workflows have no history (you can see "last run" but not what happened)
- Workflows have no review pipeline
- Workflows aren't connected to the issue system
- Two UIs for similar concepts (project board vs workflow list)
- The bridge has special `flattenSchedule()` logic just for cron display

## New Model

### Database Changes

**Add to `projects` table:**

```sql
-- Migration 049_project_workflow_fields.up.sql

-- A project can optionally be a workflow (has a recurring schedule)
ALTER TABLE projects ADD COLUMN workflow_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE projects ADD COLUMN workflow_schedule JSONB;  -- cron schedule config
ALTER TABLE projects ADD COLUMN workflow_template JSONB;  -- issue template for each run
ALTER TABLE projects ADD COLUMN workflow_agent_id UUID REFERENCES agents(id);  -- who runs it
ALTER TABLE projects ADD COLUMN workflow_last_run_at TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN workflow_next_run_at TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN workflow_run_count INT NOT NULL DEFAULT 0;

CREATE INDEX idx_projects_workflow ON projects(org_id) WHERE workflow_enabled = true;
```

**Down migration:**
```sql
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_enabled;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_schedule;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_template;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_agent_id;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_last_run_at;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_next_run_at;
ALTER TABLE projects DROP COLUMN IF EXISTS workflow_run_count;
```

### Schedule Config (`workflow_schedule`)

```json
{
  "kind": "cron",
  "expr": "0 6 * * *",
  "tz": "America/Denver"
}
```

Or:
```json
{
  "kind": "every",
  "everyMs": 900000
}
```

Or:
```json
{
  "kind": "at",
  "at": "2026-02-10T09:00:00-07:00"
}
```

Same format as OpenClaw cron schedules — the bridge already knows how to flatten these.

### Issue Template (`workflow_template`)

```json
{
  "title_pattern": "Morning Briefing — {{date}}",
  "body": "Generate today's morning briefing. Check email, calendar, overnight agent activity, and anything else worth surfacing.",
  "priority": "P2",
  "labels": ["automated", "briefing"],
  "auto_close": true,
  "pipeline": "none"
}
```

Template variables:
- `{{date}}` — today's date (e.g., "Feb 9, 2026")
- `{{datetime}}` — full datetime
- `{{run_number}}` — sequential run count
- `{{agent_name}}` — assigned agent's display name

### Pipeline Options

The `pipeline` field in the template controls what happens after the agent completes the issue:

| Value | Behavior |
|---|---|
| `"none"` | Agent runs, issue auto-closes. No review. (heartbeat, health sweep) |
| `"auto_close"` | Agent runs, delivers output, issue closes with output attached. (briefings, summaries) |
| `"standard"` | Agent runs, submits for review, reviewer approves/rejects. (content, code) |

### How a Workflow Run Works

1. **Cron fires** (OpenClaw or OtterCamp scheduler — see below)
2. **Bridge creates an issue** in the project using the template
3. **Issue is assigned** to `workflow_agent_id`
4. **Agent works the issue** (via dispatch to OpenClaw session)
5. **Issue completes** per pipeline config (auto-close or review)
6. **Project updated** — `workflow_last_run_at`, `workflow_run_count` incremented

### Where Does the Cron Live?

**Option A: OpenClaw cron (current)**
- Cron jobs stay in OpenClaw
- Bridge maps cron jobs → OtterCamp projects
- Bridge creates issues when cron fires
- Pro: works today, no new scheduler needed
- Con: two systems to configure

**Option B: OtterCamp scheduler (future)**
- OtterCamp has its own scheduler (Go cron library)
- Projects configure their own schedules
- OtterCamp creates issues and dispatches to OpenClaw
- Pro: single source of truth
- Con: needs a scheduler built into OtterCamp

**Recommendation:** Start with Option A (bridge handles it). The bridge already sees cron job executions. When a cron fires, the bridge creates an issue in the corresponding project. Long-term, move to Option B.

## API Changes

### Project Endpoints (existing, extended)

```
PATCH /api/projects/{id}
Body: {
  "workflow_enabled": true,
  "workflow_schedule": { "kind": "cron", "expr": "0 6 * * *", "tz": "America/Denver" },
  "workflow_template": { "title_pattern": "Morning Briefing — {{date}}", ... },
  "workflow_agent_id": "uuid-of-agent"
}
```

### Workflow-Filtered Views

```
GET /api/projects?workflow=true          — list only workflow projects
GET /api/projects/{id}/runs              — list issues created by this workflow (chronological)
GET /api/projects/{id}/runs/latest       — most recent run issue
POST /api/projects/{id}/runs/trigger     — manually trigger a run (creates issue immediately)
```

### Remove Standalone Workflow Endpoints

Delete or deprecate:
```
GET  /api/workflows         → replaced by GET /api/projects?workflow=true
PATCH /api/workflows/{id}   → replaced by PATCH /api/projects/{id}
POST /api/workflows/{id}/run → replaced by POST /api/projects/{id}/runs/trigger
```

## Bridge Changes

### Cron Job → Project Mapping

The bridge needs to map OpenClaw cron jobs to OtterCamp workflow projects. Options:

**a) Name-based mapping:** Bridge looks for a project with `name` matching the cron job name. Simple but fragile.

**b) Metadata mapping:** Cron job config includes `ottercamp_project_id`. Bridge reads this and creates issues in the right project. Requires cron job config changes.

**c) Auto-create:** Bridge auto-creates a workflow project for each cron job it discovers. If a project already exists for that cron, it uses it. If not, it creates one.

**Recommendation:** Option C (auto-create). The bridge already scans all cron jobs during sync. For each cron job:
1. Check if a project exists with `metadata.openclaw_cron_id = job.id`
2. If not, create one with defaults
3. When the cron fires (detected via state change), create an issue in the project

### Issue Creation on Cron Fire

```typescript
async function handleCronExecution(job: OpenClawCronJobSnapshot): Promise<void> {
    const project = await findOrCreateWorkflowProject(job);
    
    const template = project.workflow_template || {
        title_pattern: `${job.name} — {{datetime}}`,
        body: job.payload?.message || job.name,
        auto_close: true,
        pipeline: 'none'
    };
    
    const issue = await createIssue({
        projectId: project.id,
        title: renderTemplate(template.title_pattern),
        body: template.body,
        priority: template.priority || 'P3',
        labels: template.labels || ['automated'],
        assignee: project.workflow_agent_id
    });
    
    // Update project run tracking
    await updateProject(project.id, {
        workflow_last_run_at: new Date(),
        workflow_run_count: project.workflow_run_count + 1
    });
    
    // If auto_close, close the issue after agent responds
    if (template.auto_close || template.pipeline === 'none') {
        // Listen for agent response, then close
        scheduleAutoClose(issue.id, { timeoutMinutes: 30 });
    }
}
```

## Frontend Changes

### Workflows Page → Filtered Project View

**Replace `web/src/pages/WorkflowsPage.tsx`:**

Instead of its own data model, the Workflows page calls `GET /api/projects?workflow=true` and displays workflow projects with:
- Project name
- Schedule (human-readable: "Every day at 6:00 AM MST")
- Assigned agent
- Last run time + status
- Run count
- Pause/Resume toggle (sets `workflow_enabled`)
- Manual trigger button (POST `/api/projects/{id}/runs/trigger`)

Clicking a workflow navigates to the project detail page, where the issues list shows all past runs.

### Project Detail: Workflow Config

When viewing a workflow project, show a "Schedule" section in the project settings:
- Schedule editor (cron expression or interval picker)
- Issue template editor
- Agent assignment dropdown
- Pipeline mode selector (none / auto-close / standard)
- Enable/disable toggle

### Project Creation: Workflow Option

When creating a new project, add a toggle: "This is a recurring workflow"
- Shows schedule config
- Shows template config
- Sets `workflow_enabled = true`

## CLI

```bash
# List workflow projects
otter project list --workflow

# Trigger a run manually
otter project run <project-name>

# View run history
otter project runs <project-name> --limit 20

# Create a workflow project
otter project create "Morning Briefing" \
    --workflow \
    --schedule "0 6 * * *" \
    --tz "America/Denver" \
    --agent frank \
    --template-title "Morning Briefing — {{date}}" \
    --template-body "Generate today's briefing." \
    --auto-close
```

## Migration Plan

### Existing Cron Jobs → Workflow Projects

The bridge should auto-migrate on first run:

1. Read all OpenClaw cron jobs
2. For each, check if a workflow project exists
3. If not, create one:
   - Name: cron job name (e.g., "Morning Briefing")
   - `workflow_enabled`: true if cron is enabled
   - `workflow_schedule`: from cron job schedule
   - `workflow_template`: default template using cron job message
   - `workflow_agent_id`: derived from cron job's session target

### Current 10 Cron Jobs

| Cron Job | Maps to Project | Pipeline |
|---|---|---|
| Morning Briefing (6am) | "Morning Briefing" | auto_close |
| Morning Briefing (7am) | same project, second schedule | auto_close |
| Evening Summary (9pm) | "Evening Summary" | auto_close |
| Junk Mail Summary (5pm) | "Junk Mail Summary" | auto_close |
| Salt Lake Temple Tickets (9am) | "Temple Ticket Check" | auto_close |
| Codex Progress Summary | "Codex Progress" | auto_close |
| Heartbeat (15min) | "System: Heartbeat" | none |
| Memory Extract (5min) | "System: Memory Extract" | none |
| Agent Health Sweep (30min) | "System: Health Sweep" | none |
| GitHub Dispatcher (5min) | "System: GitHub Dispatch" | none |

System workflows (pipeline: none) create issues that auto-close immediately. They'll accumulate but that's fine — it's queryable history. Can add retention/archival later.

## Files to Create/Modify

### New Files
- `migrations/049_project_workflow_fields.up.sql`
- `migrations/049_project_workflow_fields.down.sql`

### Modified Files
- `internal/store/project_store.go` — add workflow fields to Project struct + queries
- `internal/api/projects.go` — handle workflow fields in PATCH, add `?workflow=true` filter
- `internal/api/router.go` — add `/projects/{id}/runs` routes, deprecate `/workflows`
- `internal/api/workflows.go` — gut it or redirect to projects handler
- `bridge/openclaw-bridge.ts` — cron fire → issue creation, auto-migrate existing crons
- `web/src/pages/WorkflowsPage.tsx` — rewrite to use filtered projects view
- `web/src/pages/ProjectDetailPage.tsx` — add workflow config section
- `web/src/components/project/WorkflowConfig.tsx` — new component for schedule/template editing
- `cmd/otter/main.go` — add `project run`, `project runs` subcommands

## Implementation Order

1. **Migration** — add workflow columns to projects
2. **Store + API** — extend project CRUD with workflow fields, add runs endpoints
3. **Bridge** — cron fire detection → issue creation, auto-migrate existing crons
4. **Frontend** — rewrite WorkflowsPage as filtered projects, add config to project detail
5. **CLI** — workflow project commands
6. **Deprecate** — remove standalone workflow handler/routes

## Execution Log

- [2026-02-09 13:32 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 117 from 01-ready to 02-in-progress and created branch codex/spec-117-workflows-as-projects from origin/main | Tests: n/a
- [2026-02-09 13:37 MST] Issue #489,#490,#491,#492,#493,#494,#495,#496,#497 | Commit n/a | created | Created full spec-117 micro-issue plan on GitHub with explicit tests and dependencies before implementation coding | Tests: n/a
- [2026-02-09 13:40 MST] Issue #489 | Commit 3dca9cc | closed | Added migration 049 workflow project fields and extended ProjectStore create/update/read workflow persistence with tests | Tests: go test ./internal/store -run 'TestProjectStore_(Create_WithWorkflowFields|Update_WithWorkflowFields|Create|Update)' -count=1; go test ./internal/store -run 'TestSchemaMigrationsUpDown' -count=1
- [2026-02-09 13:40 MST] Issue #n/a | Commit n/a | pr-opened | Opened draft PR #498 for spec 117 branch visibility | Tests: n/a
- [2026-02-09 13:45 MST] Issue #490 | Commit 42c7b14 | closed | Extended projects API list/get/create/patch with workflow fields and added workflow filter support (`?workflow=true`) plus API tests | Tests: go test ./internal/api -run 'TestProjectsHandler.*Workflow|TestProjectsHandlerPatch.*Workflow|TestProjectsHandlerList.*WorkflowFilter' -count=1 -v (env-skipped); go test ./internal/api -run 'TestProjectsHandlerLabelFilter|TestProjectsHandlerGetIncludesTaskCounts' -count=1 -v (env-skipped); go test ./internal/api -run 'TestJSONContentType|TestRouterSetup' -count=1
- [2026-02-09 13:48 MST] Issue #491 | Commit c1397b7 | closed | Added project workflow runs endpoints (/runs, /runs/latest, /runs/trigger), workflow template rendering, and run tracking updates on trigger | Tests: go test ./internal/api -run 'TestWorkflowTemplateForProjectRendersVariables|TestWorkflowRunFromIssueFormatsClosedAt|TestProjectsHandler(TriggerRunCreatesIssueAndIncrementsRunCount|ListRunsAndLatest)' -count=1 -v (DB-backed tests env-skipped); go test ./internal/api -run 'TestJSONContentType|TestRouterSetup' -count=1
- [2026-02-09 13:50 MST] Issue #492 | Commit 78d0e59 | closed | Reworked legacy `/api/workflows*` endpoints into project-backed compatibility adapters (list/toggle/run) and added parser/route tests | Tests: go test ./internal/api -run 'TestParseProjectWorkflowTrigger(Cron|Every|DefaultManual)|TestDeriveLegacyWorkflowLastStatus|TestWorkflowRoutesAreRegistered' -count=1 -v; go test ./internal/api -run 'TestJSONContentType|TestRouterSetup' -count=1
- [2026-02-09 13:54 MST] Issue #493 | Commit 0f292b6 | closed | Added bridge cron→workflow project sync (create/patch/match) and cron execution run triggers to `/api/projects/{id}/runs/trigger` with helper tests | Tests: npx tsx --test bridge/openclaw-bridge.test.ts bridge/__tests__/openclaw-bridge.workflow-sync.test.ts; npm run test:bridge
- [2026-02-09 13:56 MST] Issue #494 | Commit 2a824de | closed | Rewrote WorkflowsPage to use workflow-enabled projects and project run/toggle endpoints, with new UI tests for render/toggle/trigger flows | Tests: cd web && npm run test -- --run src/pages/WorkflowsPage.test.tsx; cd web && npm run test -- --run src/router.test.tsx src/layouts/DashboardLayout.test.tsx
- [2026-02-09 14:01 MST] Issue #495 | Commit 9aa3275 | closed | Added WorkflowConfig component and integrated project settings workflow schedule/template/agent save flow with new frontend tests | Tests: cd web && npm run test -- --run src/components/project/WorkflowConfig.test.tsx src/pages/ProjectDetailPage.test.tsx
- [2026-02-09 14:04 MST] Issue #496 | Commit 7250496 | closed | Added otter CLI/client workflow project commands (`list --workflow`, `project run`, `project runs`) and workflow create flags with schedule/template helpers | Tests: go test ./internal/ottercli -run 'TestClient.*Project.*Workflow|TestClientProjectMethodsUseExpectedPathsAndPayloads' -count=1; go test ./cmd/otter -run 'TestProject.*Workflow|TestProjectCreateSplitArgsSupportsInterspersedFlags' -count=1
- [2026-02-09 14:07 MST] Issue #497 | Commit f4790af | closed | Updated README to document workflows-as-projects model, API migration map, bridge cron migration behavior, and CLI workflow examples | Tests: rg -n "workflow|/api/workflows|project run|project runs" README.md; go test ./cmd/otter -run 'TestProject.*Workflow' -count=1; go test ./cmd/otter -run 'Test(.*Workflow|BuildWorkflowSchedulePayload)' -count=1
- [2026-02-09 14:07 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Implementation complete for spec 117; moved spec from 02-in-progress to 03-needs-review awaiting external review sign-off | Tests: n/a

- [2026-02-09 19:46 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 117 from 01-ready to 02-in-progress and created continuation branch codex/spec-117-workflows-as-projects-r2 from origin/codex/spec-117-workflows-as-projects (original branch occupied by external worktree) | Tests: n/a
- [2026-02-09 19:48 MST] Issue #554,#555,#556,#557,#558,#559,#560,#561,#562,#563,#564,#565,#566,#567,#568,#569 | Commit n/a | created | Created full reviewer-follow-up micro-issue plan for spec 117 with explicit tests before implementation | Tests: n/a
- [2026-02-09 19:56 MST] Issue #554 | Commit d873b17 | closed | Rebased spec-117 continuation branch onto origin/main, resolved merge conflicts, and passed pre-merge validation gate | Tests: go vet ./...; go build ./...; cd web && npx vitest run
- [2026-02-09 19:56 MST] Issue #n/a | Commit d873b17 | pushed | Pushed branch codex/spec-117-workflows-as-projects-r2 after rebase conflict resolution compatibility fix | Tests: cd web && npm run test -- --run src/pages/ProjectDetailPage.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-09 19:56 MST] Issue #n/a | Commit n/a | pr-opened | Opened draft PR #578 for spec 117 follow-up branch and closed superseded PR #498 | Tests: n/a
- [2026-02-09 19:58 MST] Issue #555 | Commit 7fac102 | closed | Added ON DELETE SET NULL for workflow_agent_id FK and migration regression coverage (including DB-backed null-on-delete test) | Tests: go test ./internal/store -run TestMigration049WorkflowAgentFKUsesOnDeleteSetNull -count=1; go test ./internal/store -run 'TestMigration049WorkflowAgentFKUsesOnDeleteSetNull|TestSchemaWorkflowAgentDeleteSetsProjectFieldNull' -count=1; go test ./internal/store -run 'TestSchemaMigrationsUpDown|TestMigration049WorkflowAgentFKUsesOnDeleteSetNull|TestSchemaWorkflowAgentDeleteSetsProjectFieldNull|TestProjectStore_(Create_WithWorkflowFields|Update_WithWorkflowFields)' -count=1
- [2026-02-09 19:58 MST] Issue #n/a | Commit 7fac102 | pushed | Pushed migration FK fix and tests to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 19:59 MST] Issue #556 | Commit bab0a40 | closed | Fixed workflow store tests to insert agents using real schema columns (slug/display_name/status) | Tests: go test ./internal/store -run 'TestProjectStore_(Create_WithWorkflowFields|Update_WithWorkflowFields)' -count=1 -v (env-skipped without OTTER_TEST_DATABASE_URL)
- [2026-02-09 19:59 MST] Issue #n/a | Commit bab0a40 | pushed | Pushed store-test schema fix to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:02 MST] Issue #557 | Commit be490c6 | closed | Added OptionalWorkspace middleware to legacy workflow routes and added route/middleware coverage including JWT workspace extraction behavior | Tests: go test ./internal/api -run 'TestWorkflowRoutesAreRegistered|TestWorkflowRoutesUseOptionalWorkspaceMiddleware|TestWorkflowListJWTWorkspaceRequiresOptionalMiddleware|TestJSONContentType|TestRouterSetup' -count=1 -v
- [2026-02-09 20:02 MST] Issue #n/a | Commit be490c6 | pushed | Pushed legacy workflow route middleware fix to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:03 MST] Issue #558 | Commit d87fe95 | closed | Fixed bridge auto_close mapping to follow pipeline semantics and added explicit none/auto_close assertions | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.workflow-sync.test.ts; npm run test:bridge
- [2026-02-09 20:03 MST] Issue #n/a | Commit d87fe95 | pushed | Pushed bridge pipeline/auto_close fix to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:06 MST] Issue #559 | Commit 9aea895 | closed | Added integration-style bridge workflow sync orchestration tests (first-sync guard, dedupe, create-failure handling) and test hooks for deterministic state reset/invocation | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.workflow-sync.test.ts; npm run test:bridge
- [2026-02-09 20:06 MST] Issue #n/a | Commit 9aea895 | pushed | Pushed bridge workflow sync orchestration test coverage to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:08 MST] Issue #560 | Commit 6a207e3 | closed | Expanded WorkflowConfig test coverage from 1 to 7 tests across schedule conditionals, agent selection, and template controls | Tests: cd web && npm run test -- --run src/components/project/WorkflowConfig.test.tsx
- [2026-02-09 20:08 MST] Issue #n/a | Commit 6a207e3 | pushed | Pushed WorkflowConfig test expansion to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:09 MST] Issue #561 | Commit 6894da5 | closed | Updated CLI Project.Slug() to prefer server URLSlug with fallback to slugify(Name), and expanded tests for both paths | Tests: go test ./internal/ottercli -run TestProjectSlug -count=1; go test ./internal/ottercli -run 'TestProjectSlug|TestSlugify|TestClientProjectMethodsUseExpectedPathsAndPayloads|TestClient.*Project.*Workflow' -count=1
- [2026-02-09 20:09 MST] Issue #n/a | Commit 6894da5 | pushed | Pushed CLI slug fix to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:12 MST] Issue #562 | Commit 9de3f63 | closed | Added semantic workflow_schedule validation (kind enum, cron shape, timezone, everyMs, at timestamp) with invalid-case coverage | Tests: go test ./internal/api -run TestNormalizeWorkflowPatchJSONScheduleValidation -count=1; go test ./internal/api -run 'TestNormalizeWorkflowPatchJSONScheduleValidation|TestProjectsHandlerPatchWorkflowScheduleValidation|TestProjectsHandlerPatchWorkflowFields' -count=1
- [2026-02-09 20:12 MST] Issue #n/a | Commit 9de3f63 | pushed | Pushed workflow schedule semantic validation to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:13 MST] Issue #563 | Commit 8e623ac | closed | Hardened TriggerRun by using atomic SQL run-count increment with RETURNING and added regression coverage for atomic query shape | Tests: go test ./internal/api -run 'TestIncrementWorkflowRunCountQueryUsesAtomicUpdate|TestNormalizeWorkflowPatchJSONScheduleValidation|TestProjectsHandlerTriggerRunCreatesIssueAndIncrementsRunCount' -count=1 -v; go test ./internal/api -run 'TestProjectsHandler(TriggerRunCreatesIssueAndIncrementsRunCount|ListRunsAndLatest)|TestNormalizeWorkflowPatchJSONScheduleValidation|TestIncrementWorkflowRunCountQueryUsesAtomicUpdate' -count=1
- [2026-02-09 20:13 MST] Issue #n/a | Commit 8e623ac | pushed | Pushed TriggerRun atomic run-count fix to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:15 MST] Issue #564 | Commit 35507e6 | closed | Added response.ok/catch error handling for workflow toggle and manual trigger mutations and covered both failure states in WorkflowsPage tests | Tests: cd web && npm run test -- --run src/pages/WorkflowsPage.test.tsx
- [2026-02-09 20:15 MST] Issue #n/a | Commit 35507e6 | pushed | Pushed WorkflowsPage mutation error-state handling to codex/spec-117-workflows-as-projects-r2 | Tests: same as prior entry
- [2026-02-09 20:22 MST] Issue #566 | Commit 98dbb46 | closed | Added bridge workflow sync concurrency guard with overlap-skip and guard-reset regression coverage | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.workflow-sync.test.ts
- [2026-02-09 20:23 MST] Issue #567 | Commit f86c8af | closed | Restricted fallback name matching to workflow projects and added non-workflow collision regression tests | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.workflow-sync.test.ts
- [2026-02-09 20:23 MST] Issue #568 | Commit fe84a33 | closed | Added WorkflowsPage list-level empty-state and fetch-error tests | Tests: cd web && npm run test -- --run src/pages/WorkflowsPage.test.tsx
- [2026-02-09 20:24 MST] Issue #569 | Commit 38800c1 | closed | Re-synced ProjectDetail workflow settings state after partial save failure and added regression test | Tests: cd web && npm run test -- --run src/pages/ProjectDetailPage.test.tsx src/components/project/WorkflowConfig.test.tsx
- [2026-02-09 20:25 MST] Issue #n/a | Commit 38800c1 | validated | Ran full pre-merge validation after final reviewer fixes | Tests: npm run test:bridge; go vet ./...; go build ./...; cd web && npx vitest run
- [2026-02-09 20:26 MST] Issue #n/a | Commit 38800c1 | pushed | Pushed reviewer-fix commits (98dbb46, f86c8af, fe84a33, 38800c1) to codex/spec-117-workflows-as-projects-r2 | Tests: n/a
- [2026-02-09 20:26 MST] Issue #n/a | Commit n/a | reviewer-block-removed | Removed top-level Reviewer Required Changes block after all mandatory items were resolved; retained completion history in Execution Log | Tests: n/a
- [2026-02-09 20:27 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Moved spec 117 from 02-in-progress to 03-needs-review pending external reviewer sign-off | Tests: n/a
