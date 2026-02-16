# Issue #108: Pipeline Settings — UI & CLI

## Summary

Add project-level settings for the issue pipeline: role assignment (Planner, Worker, Reviewer), deployment configuration, and human review gate. Every setting must be accessible from both the web UI and the `otter` CLI.

## What Exists Today

- **DB table**: `issue_role_assignments` (migration 043) — stores per-project role→agent mapping. No Go code reads/writes it yet.
- **Settings page**: `web/src/pages/SettingsPage.tsx` with tabs (Profile, Workspace, Notifications, Integrations, GitHub, Labels, Data). No pipeline/deployment settings.
- **Projects table**: Has `repo_url` and `local_repo_path` columns but no deployment config.
- **Design mocks**: `designs/dashboard-v5/settings.html`, `settings-github.html` — reference for visual style.

## Requirements

### 1. Pipeline Role Assignment (per project)

Each project can assign an agent to each of the three pipeline roles:

| Role | Description | Default |
|------|------------|---------|
| Planner | Decomposes `ready` issues into sub-issues | Unset (manual) |
| Worker | Implements `ready_for_work` sub-issues | Unset (manual) |
| Reviewer | Reviews `review` sub-issues, merges or kicks back | Unset (manual) |

**Behavior when unset:** The pipeline stage must be triggered manually (no auto-pickup).

**UI:** Dropdown selects in a "Pipeline" section under project settings. Each dropdown lists available agents for the org (from `agents` table) plus "Manual (no agent)" option.

**API:**
```
GET    /api/projects/{id}/pipeline-roles
PUT    /api/projects/{id}/pipeline-roles
```

PUT body:
```json
{
  "planner": { "agentId": "uuid-or-null" },
  "worker": { "agentId": "uuid-or-null" },
  "reviewer": { "agentId": "uuid-or-null" }
}
```

Uses existing `issue_role_assignments` table. Upsert on `(project_id, role)`.

**CLI:**
```bash
otter pipeline roles --project <name>                    # list current assignments
otter pipeline set-role --project <name> --role planner --agent <name>   # assign
otter pipeline set-role --project <name> --role planner --none           # clear
```

### 2. Deployment Configuration (per project)

How a project deploys after Reviewer approves and merges.

**New DB table:**
```sql
CREATE TABLE project_deploy_config (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deploy_method TEXT NOT NULL DEFAULT 'none'
        CHECK (deploy_method IN ('none', 'github_push', 'cli_command')),
    github_repo_url TEXT,        -- for github_push: the remote to push to
    github_branch TEXT DEFAULT 'main',  -- target branch
    cli_command TEXT,             -- for cli_command: shell command to run via OpenClaw
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id)
);
```

**Deploy methods:**
- `none` — no auto-deploy (default)
- `github_push` — push merged branch to connected GitHub repo. Uses existing `repo_url` from projects table or override `github_repo_url` here.
- `cli_command` — send a CLI command to the project owner's OpenClaw instance to execute (e.g. `npx itsalive-co`, `railway up`)

**UI:** "Deployment" section in project settings:
- Radio/select for method (None / Push to GitHub / Run CLI command)
- Conditional fields: GitHub repo URL + branch, or CLI command text input
- Help text explaining each option

**API:**
```
GET    /api/projects/{id}/deploy-config
PUT    /api/projects/{id}/deploy-config
```

**CLI:**
```bash
otter deploy config --project <name>                          # show current config
otter deploy set --project <name> --method github_push --repo <url> --branch main
otter deploy set --project <name> --method cli_command --command "npx itsalive-co"
otter deploy set --project <name> --method none
```

### 3. Human Review Gate (per project)

Toggle: does a human need to approve before the Reviewer can merge/deploy?

**DB:** Add column to projects:
```sql
ALTER TABLE projects ADD COLUMN require_human_review BOOLEAN NOT NULL DEFAULT false;
```

When `true`:
- Reviewer flags the issue as `approved_by_reviewer` instead of merging
- Human gets notified and must explicitly approve
- Only after human approval does merge + deploy happen

When `false`:
- Reviewer can merge and trigger deploy directly

**UI:** Toggle switch in the Pipeline section: "Require human approval before merge"

**API:** Part of existing project update endpoint:
```
PATCH /api/projects/{id}
{ "requireHumanReview": true }
```

**CLI:**
```bash
otter pipeline set --project <name> --require-human-review true
otter pipeline set --project <name> --require-human-review false
```

### 4. CLI Parity Rule

**Every setting in the Otter Camp web UI must be accessible via the `otter` CLI.** This is a hard requirement, not just for pipeline settings but as a design principle going forward.

For this spec, that means:
- Pipeline roles: get/set via CLI ✅
- Deploy config: get/set via CLI ✅  
- Human review toggle: get/set via CLI ✅

The CLI should output JSON by default (with `--json` flag) and human-readable tables otherwise.

## UI Layout

Add a new settings tab/section for project-level settings. Current SettingsPage is org/user-level. Project settings should be accessible from the project detail view.

**Navigation:** Project page → Settings tab (or gear icon) → sections:
1. **General** (name, description, status — existing)
2. **Pipeline** (role assignments + human review toggle)
3. **Deployment** (deploy method + config)
4. **GitHub** (existing GitHub sync settings, moved here from org settings)

## Anti-Hallucination Rules

- Anti-hallucination for Planner and Worker is **always on** — not configurable. Do not add a toggle for it.
- The `issue_role_assignments` table already exists — use it, don't create a duplicate.
- The `agents` table is the source for agent dropdowns — query it by org_id.

## Files to Create/Modify

### Backend (Go)
- `migrations/048_create_project_deploy_config.up.sql` — new deploy config table
- `migrations/048_create_project_deploy_config.down.sql`
- `migrations/049_add_require_human_review.up.sql` — add column to projects
- `migrations/049_add_require_human_review.down.sql`
- `internal/store/pipeline_role_store.go` — CRUD for `issue_role_assignments`
- `internal/store/deploy_config_store.go` — CRUD for `project_deploy_config`
- `internal/api/pipeline_roles.go` — GET/PUT handlers
- `internal/api/deploy_config.go` — GET/PUT handlers
- `internal/api/router.go` — register new routes
- Update `internal/api/projects.go` — handle `requireHumanReview` in PATCH

### Frontend (React/TSX)
- `web/src/pages/project/ProjectSettingsPage.tsx` — new page
- `web/src/components/project/PipelineSettings.tsx` — role assignment + human review toggle
- `web/src/components/project/DeploySettings.tsx` — deployment configuration
- Route registration in app router

### CLI (Go)
- `cmd/otter/pipeline.go` — `otter pipeline` subcommand (roles, set-role, set)
- `cmd/otter/deploy.go` — `otter deploy` subcommand (config, set)
- Next migration number: check `migrations/` for latest and use next available

## Test Expectations

- Store tests for pipeline role CRUD (upsert, get by project, clear)
- Store tests for deploy config CRUD
- API handler tests for all new endpoints (auth, validation, happy path)
- Frontend component tests for settings rendering and form submission
- CLI integration: commands parse correctly, hit correct API endpoints
- Human review gate: verify issue state machine respects the toggle

## Execution Log

- [2026-02-08 21:42 MST] Issue #108 | Commit n/a | in_progress | Moved spec from 01-ready to 02-in-progress and began execution on branch codex/spec-108-pipeline-settings-ui-cli | Tests: n/a
- [2026-02-08 21:45 MST] Issue #429 | Commit n/a | created | Created Spec108 Phase1 migration micro-issue with explicit schema test plan | Tests: go test ./internal/store -run TestSchema -count=1
- [2026-02-08 21:45 MST] Issue #430 | Commit n/a | created | Created Spec108 Phase2 deploy-config store micro-issue with explicit store tests | Tests: go test ./internal/store -run 'TestDeployConfigStore' -count=1
- [2026-02-08 21:45 MST] Issue #431 | Commit n/a | created | Created Spec108 Phase3 pipeline-role store micro-issue with explicit store tests | Tests: go test ./internal/store -run 'TestPipelineRoleStore' -count=1
- [2026-02-08 21:45 MST] Issue #432 | Commit n/a | created | Created Spec108 Phase4 pipeline-roles API micro-issue with explicit handler tests | Tests: go test ./internal/api -run 'TestPipelineRolesHandler' -count=1
- [2026-02-08 21:45 MST] Issue #433 | Commit n/a | created | Created Spec108 Phase5 deploy-config API micro-issue with explicit handler tests | Tests: go test ./internal/api -run 'TestDeployConfigHandler' -count=1
- [2026-02-08 21:45 MST] Issue #434 | Commit n/a | created | Created Spec108 Phase6 human-review gate micro-issue with explicit project/review tests | Tests: go test ./internal/api -run 'TestProjectsHandlerPatchRequireHumanReview|TestIssueReviewRequireHumanGate' -count=1
- [2026-02-08 21:45 MST] Issue #435 | Commit n/a | created | Created Spec108 Phase7 ottercli client micro-issue with explicit client tests | Tests: go test ./internal/ottercli -run 'TestClient(PipelineRoles|DeployConfig|PatchProjectRequireHumanReview)' -count=1
- [2026-02-08 21:45 MST] Issue #436 | Commit n/a | created | Created Spec108 Phase8 CLI command micro-issue with explicit parser tests | Tests: go test ./cmd/otter -run 'TestHandle(Pipeline|Deploy)' -count=1
- [2026-02-08 21:45 MST] Issue #437 | Commit n/a | created | Created Spec108 Phase9 pipeline settings UI micro-issue with explicit vitest coverage | Tests: cd web && npx vitest run src/components/project/PipelineSettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-08 21:45 MST] Issue #438 | Commit n/a | created | Created Spec108 Phase10 deployment settings UI micro-issue with explicit vitest coverage | Tests: cd web && npx vitest run src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-08 21:50 MST] Issue #429 | Commit d8e8ff8 | closed | Added migrations 047/048 for deploy config and require_human_review plus migration contract tests; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)' -count=1; go test ./internal/api -run 'TestProjectSettingsMigrations' -count=1 -v (skipped without OTTER_TEST_DATABASE_URL); go test ./internal/api -count=1 (baseline unrelated failures)
- [2026-02-08 21:52 MST] Issue #430 | Commit 47880b5 | closed | Implemented DeployConfigStore get/upsert with deploy-method validation and added targeted store tests; pushed branch and closed issue | Tests: go test ./internal/store/deploy_config_store_test.go ./internal/store/deploy_config_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -run TestNormalizeDeployConfigInput -count=1; go test ./internal/store/deploy_config_store_test.go ./internal/store/deploy_config_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -run 'TestDeployConfigStore_(UpsertAndGetByProject|RejectsCrossWorkspaceProject)' -count=1 -v (skipped without OTTER_TEST_DATABASE_URL); go test ./internal/store/deploy_config_store_test.go ./internal/store/deploy_config_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -count=1
- [2026-02-08 21:54 MST] Issue #431 | Commit 9496083 | closed | Implemented PipelineRoleStore upsert/list with role validation and manual-clear semantics plus targeted tests; pushed branch and closed issue | Tests: go test ./internal/store/pipeline_role_store_test.go ./internal/store/pipeline_role_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -run TestNormalizePipelineRoleAssignmentInput -count=1; go test ./internal/store/pipeline_role_store_test.go ./internal/store/pipeline_role_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -run 'TestPipelineRoleStore_(UpsertListAndClear|RejectsCrossWorkspaceAgent)' -count=1 -v (skipped without OTTER_TEST_DATABASE_URL); go test ./internal/store/pipeline_role_store_test.go ./internal/store/pipeline_role_store.go ./internal/store/store.go ./internal/store/test_helpers_test.go -count=1
- [2026-02-08 21:57 MST] Issue #432 | Commit abab56b | closed | Added GET/PUT /api/projects/{id}/pipeline-roles handlers with router wiring and validation tests; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestPipelineRolesHandler(GetAndPut|Validation)|TestRouterRegistersPipelineRolesRoutes' -count=1
- [2026-02-08 21:58 MST] Issue #433 | Commit de405a1 | closed | Added GET/PUT /api/projects/{id}/deploy-config handlers with router wiring and validation tests; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestDeployConfigHandler(GetAndPut|Validation)|TestRouterRegistersDeployConfigRoutes' -count=1
- [2026-02-08 22:03 MST] Issue #434 | Commit 3a8d03e | closed | Added requireHumanReview project patch/store support and reviewer-gated approval flow with approval_state migration 049; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestProjectsHandlerPatchUpdatesProjectFields|TestIssuesHandlerApprove(RequiresReadyForReviewAndEmitsCompletionActivity|UsesReviewerGateWhenProjectRequiresHumanReview)' -count=1; go test ./internal/api -run 'TestProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)' -count=1; go test ./internal/api -count=1 (baseline unrelated failures)
- [2026-02-08 22:05 MST] Issue #435 | Commit faef757 | closed | Extended ottercli client with pipeline-role/deploy-config methods and requireHumanReview patch helper plus focused HTTP tests; pushed branch and closed issue | Tests: go test ./internal/ottercli/client.go ./internal/ottercli/config.go ./internal/ottercli/client_pipeline_deploy_test.go -run 'TestClient(PipelineRoleMethods|DeployConfigMethods|SetProjectRequireHumanReview)' -count=1; go test ./internal/ottercli/client.go ./internal/ottercli/config.go ./internal/ottercli/client_pipeline_deploy_test.go -count=1
- [2026-02-08 22:13 MST] Issue #436 | Commit a3b09d1 | closed | Added otter pipeline/deploy command groups with strict flag validation and command wiring tests; pushed branch and closed issue | Tests: go test ./cmd/otter -run 'TestHandle(Pipeline|Deploy)' -count=1; go test ./cmd/otter -count=1
- [2026-02-08 22:18 MST] Issue #437 | Commit b0626ef | closed | Added ProjectSettingsPage and PipelineSettings UI with role selector/toggle API wiring plus vitest coverage; pushed branch and closed issue | Tests: cd web && npx vitest run src/components/project/PipelineSettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx; cd web && npx vitest run src/pages/ProjectDetailPage.test.tsx
- [2026-02-08 22:20 MST] Issue #438 | Commit f2cba01 | closed | Added DeploySettings UI with method-specific fields/validation, integrated into ProjectSettingsPage, and shipped vitest coverage; pushed branch and closed issue | Tests: cd web && npx vitest run src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx; cd web && npx vitest run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx src/pages/ProjectDetailPage.test.tsx
- [2026-02-09 15:07 MST] Issue #108 | Commit n/a | in_progress | Resumed spec for reviewer-required follow-up work; moved file from 01-ready to 02-in-progress and began pre-implementation reconciliation | Tests: n/a
- [2026-02-09 15:09 MST] Issue #500 | Commit n/a | created | Created follow-up micro-issue for merge-base cleanup and spec-only diff isolation checks | Tests: git merge origin/main --no-commit --no-ff; git diff --name-only origin/main...HEAD
- [2026-02-09 15:09 MST] Issue #501 | Commit n/a | created | Created follow-up micro-issue to restore `go vet ./...` clean status on spec branch | Tests: go vet ./...
- [2026-02-09 15:09 MST] Issue #502 | Commit n/a | created | Created follow-up micro-issue to sanitize unexpected API error responses and status codes | Tests: go test ./internal/api -run 'Test(DeployConfigHandlerUnexpectedStoreErrorSanitized|PipelineRolesHandlerUnexpectedStoreErrorSanitized|IssuesHandlerUnexpectedStoreErrorReturns500)' -count=1
- [2026-02-09 15:09 MST] Issue #503 | Commit n/a | created | Created follow-up micro-issue for transactional pipeline-role PUT semantics | Tests: go test ./internal/api -run 'TestPipelineRolesHandlerPutIsAtomicOnMixedValidity' -count=1
- [2026-02-09 15:09 MST] Issue #504 | Commit n/a | created | Created follow-up micro-issue to enforce human-only second approval when gate is enabled | Tests: go test ./internal/api -run 'TestIssuesHandlerApproveRequiresHumanActorForSecondApproval' -count=1
- [2026-02-09 15:09 MST] Issue #505 | Commit n/a | created | Created follow-up micro-issue to unify `require_human_review` JSON casing behavior | Tests: go test ./internal/api -run 'TestProjectsHandlerPatchRequireHumanReviewJSONCasing' -count=1
- [2026-02-09 15:09 MST] Issue #506 | Commit n/a | created | Created follow-up micro-issue to use shared frontend API client and URL-encode project ids | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-09 15:09 MST] Issue #507 | Commit n/a | created | Created follow-up micro-issue for migration 049 test coverage and `os.ReadFile` modernization | Tests: go test ./internal/api -run 'TestProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)' -count=1
- [2026-02-09 15:13 MST] Issue #500 | Commit 13a3246 | closed | Replayed spec-108 commit stack on top of current main and validated clean merge/diff isolation for reviewer P0 branch-cleanliness gate | Tests: git merge origin/main --no-commit --no-ff; git diff --name-only origin/main...HEAD
- [2026-02-09 15:13 MST] Issue #501 | Commit 4258fc4 | closed | Fixed `go vet` failure by renaming conflicting test helper in deploy config store tests and pushed branch | Tests: go vet ./...
- [2026-02-09 15:13 MST] Issue #502 | Commit d019f27 | closed | Sanitized unexpected API error responses for deploy/pipeline/issues handlers and added default-path tests; pushed branch | Tests: go test ./internal/api -run 'Test(DeployConfigStoreErrorMessageSanitizesUnexpectedError|PipelineRoleStoreErrorMessageSanitizesUnexpectedError|HandleIssueStoreErrorUnexpectedReturns500|DeployConfigHandlerUnexpectedStoreErrorSanitized|PipelineRolesHandlerUnexpectedStoreErrorSanitized|IssuesHandlerUnexpectedStoreErrorReturns500)' -count=1; go test ./internal/api -run 'Test(DeployConfigHandler(GetAndPut|Validation)|PipelineRolesHandler(GetAndPut|Validation)|IssuesHandlerApprove(RequiresReadyForReviewAndEmitsCompletionActivity|UsesReviewerGateWhenProjectRequiresHumanReview)|HandleIssueStoreErrorUnexpectedReturns500)' -count=1
- [2026-02-09 15:15 MST] Issue #503 | Commit 5f383b9 | closed | Made pipeline role PUT all-or-nothing via transactional batch upsert and added mixed-validity rollback regression test; pushed branch | Tests: go test ./internal/api -run 'Test(PipelineRolesHandler(GetAndPut|Validation|PutIsAtomicOnMixedValidity)|PipelineRoleStoreErrorMessageSanitizesUnexpectedError)' -count=1; go test ./internal/store -run 'Test(NormalizePipelineRoleAssignmentInput|PipelineRoleStore_(UpsertListAndClear|RejectsCrossWorkspaceAgent))' -count=1
- [2026-02-09 15:19 MST] Issue #504 | Commit 813b622 | closed | Enforced human-only second approval when `require_human_review` is enabled and updated approval-gate tests for explicit human context path; pushed branch | Tests: go test ./internal/api -run 'Test(IssuesHandlerApproveRequiresHumanActorForSecondApproval|IssuesHandlerApproveUsesReviewerGateWhenProjectRequiresHumanReview)' -count=1
- [2026-02-09 15:19 MST] Issue #505 | Commit 14ef29b | closed | Unified project PATCH casing handling for `require_human_review` (snake_case + camelCase compatibility with conflict guard) and added casing tests; pushed branch | Tests: go test ./internal/api -run 'Test(ProjectsHandlerPatchRequireHumanReviewJSONCasing|ResolveRequireHumanReviewPatch|ProjectsHandlerPatchUpdatesProjectFields)' -count=1
- [2026-02-09 15:19 MST] Issue #506 | Commit 4f41f03 | closed | Refactored Pipeline/Deploy settings components to shared `apiFetch`, removed duplicate org-query helpers, and URL-encoded project IDs with updated frontend tests; pushed branch | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-09 15:19 MST] Issue #507 | Commit bac6e06 | closed | Expanded migration contract tests for migration 049, replaced deprecated `ioutil.ReadFile`, and asserted rollback removes `approved_by_reviewer` constraint variant; pushed branch | Tests: go test ./internal/api -run 'TestProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)' -count=1
- [2026-02-09 15:20 MST] Issue #108 | Commit bac6e06 | completed | Resolved all reviewer-required follow-up issues (#500-#507) and removed top-level `## Reviewer Required Changes` block; summary preserved in execution log entries | Tests: see issue-level entries
- [2026-02-09 15:20 MST] Issue #108 | Commit bac6e06 | needs_review | Moved spec file from 02-in-progress to 03-needs-review after completing implementation follow-ups and pushing all commits | Tests: go vet ./...; go test ./internal/api -run 'Test(IssuesHandlerApproveRequiresHumanActorForSecondApproval|IssuesHandlerApproveUsesReviewerGateWhenProjectRequiresHumanReview|ProjectsHandlerPatchRequireHumanReviewJSONCasing|ResolveRequireHumanReviewPatch|ProjectSettingsMigrationFiles(Exist|ContainExpectedDDL))' -count=1; cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx src/pages/project/ProjectSettingsPage.test.tsx
- [2026-02-09 16:15 MST] Issue #108 | Commit n/a | in_progress | Resumed reviewer-required implementation run; moved spec from 01-ready to 02-in-progress after preflight reconciliation | Tests: n/a
- [2026-02-09 16:17 MST] Issue #513 | Commit n/a | created | Split reviewer P1 frontend partial-save requirement into dedicated micro-issue with explicit PipelineSettings test command | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx
- [2026-02-09 16:17 MST] Issue #514 | Commit n/a | created | Split reviewer P2 error-classification requirement into dedicated sentinel-error micro-issue for deploy/pipeline handlers | Tests: go test ./internal/api -run 'Test(DeployConfigHandlerValidationErrorClassification|PipelineRolesHandlerValidationErrorClassification)' -count=1
- [2026-02-09 16:17 MST] Issue #515 | Commit n/a | created | Split reviewer P2 issue transition error-status requirement into dedicated micro-issue for handleIssueStoreError mapping | Tests: go test ./internal/api -run 'TestHandleIssueStoreErrorTransitionValidationMapsTo409' -count=1
- [2026-02-09 16:17 MST] Issue #516 | Commit n/a | created | Split reviewer P2 CLI coverage requirement into dedicated micro-issue for pipeline roles list and --none clear path tests | Tests: go test ./cmd/otter -run 'TestHandlePipelineRolesShowsRoles|TestHandlePipelineSetRoleClearsAssignment' -count=1
- [2026-02-09 16:17 MST] Issue #517 | Commit n/a | created | Split reviewer P2 frontend coverage requirement into dedicated micro-issue for initial-load failures and callback test coverage | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 16:17 MST] Issue #518 | Commit n/a | created | Split reviewer P2 success-banner behavior requirement into dedicated micro-issue for clear-on-edit semantics | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 16:17 MST] Issue #512 | Commit n/a | closed | Closed bundled follow-up issue as superseded by micro-issues #513-#518 to enforce one-shippable-slice issue sizing | Tests: n/a
- [2026-02-09 16:19 MST] Issue #508 | Commit 03163a9 | closed | Renumbered spec-108 migrations to 050/051/052, updated migration contract tests, pushed branch, and closed issue | Tests: go test ./internal/api -run 'TestProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)' -count=1; go test ./internal/api -run 'TestProjectSettingsMigration' -count=1
- [2026-02-09 16:22 MST] Issue #509 | Commit f5fea86 | closed | Added approve fail-fast DB-nil guard and explicit projectRequiresHumanReview DB-unavailable error path with non-DB panic-regression test; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestIssuesHandlerApproveReturnsErrorWhenDBNil' -count=1; go test ./internal/api -run 'TestIssuesHandlerApprove(ReturnsErrorWhenDBNil|UsesReviewerGateWhenProjectRequiresHumanReview|RequiresHumanActorForSecondApproval)' -count=1
- [2026-02-09 16:23 MST] Issue #510 | Commit 02db03f | closed | Added explicit approve target-state resolver to return 409 for non-ready-for-review reviewer-gate approvals (including draft) and retained human second-approval guard; pushed branch and closed issue | Tests: go test ./internal/api -run 'TestIssuesHandlerApproveFromDraftWithHumanReviewReturns409' -count=1; go test ./internal/api -run 'TestIssuesHandlerApprove(FromDraftWithHumanReviewReturns409|ReturnsErrorWhenDBNil|RequiresHumanActorForSecondApproval)' -count=1; go test ./internal/api -run 'TestIssuesHandlerApprove' -count=1
- [2026-02-09 16:25 MST] Issue #511 | Commit b78e2d5 | closed | Scoped deploy-branch defaulting to github_push only and added CLI regression test proving --method none sends empty branch; pushed branch and closed issue | Tests: go test ./cmd/otter -run 'TestDeploySetMethodNoneSendsEmptyBranch' -count=1; go test ./cmd/otter -run 'Test(DeploySetMethodNoneSendsEmptyBranch|HandleDeploySetCallsSetDeployConfig|HandleDeploySetValidationForCliCommandRequiresCommand)' -count=1
- [2026-02-09 16:26 MST] Issue #513 | Commit b86adb0 | closed | Added explicit partial-save PipelineSettings error when role PUT succeeds but human-review PATCH fails, plus regression coverage; pushed branch and closed issue | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx --testNamePattern partial-save; cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx
- [2026-02-09 16:28 MST] Issue #514 | Commit 7f6d304 | closed | Introduced store.ErrValidation sentinel and replaced deploy/pipeline handler string-matching error classification with errors.Is checks plus false-positive regression tests; pushed branch and closed issue | Tests: go test ./internal/api -run 'Test(DeployConfigHandlerValidationErrorClassification|PipelineRolesHandlerValidationErrorClassification|DeployConfigStoreErrorMessageSanitizesUnexpectedError|PipelineRoleStoreErrorMessageSanitizesUnexpectedError)' -count=1; go test ./internal/store -run 'Test(NormalizeDeployConfigInput|NormalizePipelineRoleAssignmentInput|NormalizeWorkspaceID)' -count=1
- [2026-02-09 16:30 MST] Issue #515 | Commit 6f97fd1 | closed | Added typed transition conflict/validation errors in issue store and mapped handleIssueStoreError to sanitized 409/400 responses instead of default 500 for state-transition validation paths; pushed branch and closed issue | Tests: go test ./internal/api -run 'Test(HandleIssueStoreErrorTransitionValidationMapsTo409|HandleIssueStoreErrorUnexpectedReturns500|IssuesHandlerApproveFromDraftWithHumanReviewReturns409|IssuesHandlerApproveReturnsErrorWhenDBNil)' -count=1; go test ./internal/store -run 'Test(NormalizeWorkspaceID|ParseIssueState)' -count=1
- [2026-02-09 16:31 MST] Issue #516 | Commit d49f72f | closed | Added missing CLI coverage for `otter pipeline roles` output and `otter pipeline set-role --none` clear-assignment path, including no-ResolveAgent behavior checks; pushed branch and closed issue | Tests: go test ./cmd/otter -run 'TestHandlePipeline(RolesShowsRoles|SetRoleClearsAssignment)' -count=1; go test ./cmd/otter -run 'TestHandle(PipelineSetRoleCallsSetPipelineRoles|PipelineRolesShowsRoles|PipelineSetRoleClearsAssignment|PipelineSetUpdatesRequireHumanReview)' -count=1
- [2026-02-09 16:32 MST] Issue #517 | Commit a739594 | closed | Added missing frontend tests for PipelineSettings/DeploySettings initial-load failures and PipelineSettings onRequireHumanReviewSaved success callback invocation; pushed branch and closed issue | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 16:34 MST] Issue #518 | Commit c6a2363 | closed | Cleared stale success banners in PipelineSettings and DeploySettings on post-save form edits and added regression tests for both components; pushed branch and closed issue | Tests: cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 16:35 MST] Issue #108 | Commit c6a2363 | completed | Resolved reviewer-required fixes (#508-#511, #513-#518), closed all micro-issues, and removed the top-level Reviewer Required Changes block after marking all items complete | Tests: go test ./internal/api -run 'Test(ProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)|IssuesHandlerApprove(FromDraftWithHumanReviewReturns409|ReturnsErrorWhenDBNil)|HandleIssueStoreErrorTransitionValidationMapsTo409|DeployConfigHandlerValidationErrorClassification|PipelineRolesHandlerValidationErrorClassification)' -count=1; go test ./cmd/otter -run 'Test(DeploySetMethodNoneSendsEmptyBranch|HandlePipelineRolesShowsRoles|HandlePipelineSetRoleClearsAssignment)' -count=1; cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 16:35 MST] Issue #108 | Commit c6a2363 | needs_review | Moved spec file from 02-in-progress to 03-needs-review after completing all reviewer-required changes and closing all associated micro-issues | Tests: go test ./internal/api -run 'Test(ProjectSettingsMigrationFiles(Exist|ContainExpectedDDL)|IssuesHandlerApprove(FromDraftWithHumanReviewReturns409|ReturnsErrorWhenDBNil)|HandleIssueStoreErrorTransitionValidationMapsTo409|DeployConfigHandlerValidationErrorClassification|PipelineRolesHandlerValidationErrorClassification)' -count=1; go test ./cmd/otter -run 'Test(DeploySetMethodNoneSendsEmptyBranch|HandlePipelineRolesShowsRoles|HandlePipelineSetRoleClearsAssignment)' -count=1; cd web && npm run test -- --run src/components/project/PipelineSettings.test.tsx src/components/project/DeploySettings.test.tsx
- [2026-02-09 17:28 MST] Issue #108 | Commit n/a | in_progress | Resumed spec 108 reviewer-required follow-up, moved spec from 01-ready to 02-in-progress, and started branch codex/spec-108-pipeline-settings-ui-cli-r3 from origin/codex/spec-108-pipeline-settings-ui-cli-r2 due worktree lock on base branch name | Tests: n/a
- [2026-02-09 17:28 MST] Issue #534 | Commit n/a | created | Adopted reviewer-required rebase/conflict micro-issue for spec 108 with explicit ProjectDetailPage import-collision and full gate test plan | Tests: cd web && npx vitest run src/pages/ProjectDetailPage.test.tsx; git merge main --no-commit --no-ff; go vet ./...; go build ./...; cd web && npx vitest run
- [2026-02-09 17:36 MST] Issue #534 | Commit f1f2614 | closed | Rebased branch onto current main, resolved ProjectDetailPage import collision (keeping both imports), reconciled follow-on rebase conflicts with mainline behavior preserved, fixed deploy-config store test helper redeclaration, pushed branch codex/spec-108-pipeline-settings-ui-cli-r3, opened PR #535, and closed issue #534 | Tests: cd web && npx vitest run src/pages/ProjectDetailPage.test.tsx; go build ./...; go vet ./...; npm run test:bridge; cd web && npx vitest run
- [2026-02-09 17:36 MST] Issue #108 | Commit f1f2614 | needs_review | Completed reviewer-required rebase follow-up for spec 108, removed stale top-level Reviewer Required Changes block, and moved spec file from 02-in-progress to 03-needs-review pending external sign-off | Tests: cd web && npx vitest run src/pages/ProjectDetailPage.test.tsx; go build ./...; go vet ./...; npm run test:bridge; cd web && npx vitest run
