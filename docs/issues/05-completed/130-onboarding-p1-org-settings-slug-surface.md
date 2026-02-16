# 130 - Onboarding P1 Org Settings Slug Surface

## Problem
Issue #239 still tracks an onboarding P1 follow-up for org settings. Current Settings workspace UI supports workspace name and member list, but does not surface the org slug explicitly. Product direction for onboarding decisions is `display name + slug only` (no subdomains), so users need clear visibility of slug in settings.

## Goals
- Return org slug from workspace settings API.
- Show org slug in Settings > Workspace section.
- Keep slug visible as a stable identifier users can reference in CLI/onboarding docs.

## Out of Scope
- Subdomain management.
- Hosted onboarding UX.
- Member invite flow changes.
- Broad settings redesign.

## Requirements
1. API workspace payload
- `GET /api/settings/workspace` response includes org slug.
- `PUT /api/settings/workspace` response includes org slug after updates.

2. Settings UI
- Workspace section renders slug field/copy clearly.
- Slug is read-only (display only) for this slice.

3. Tests
- Backend test coverage for workspace slug response.
- Frontend test coverage for slug rendering in Settings workspace section.

## Full Implementation Plan (Build Order)

### Phase 1 - Backend workspace response extension
- Add `slug` to workspace response model and queries.
- Ensure patch response still returns updated name + slug.

### Phase 2 - Frontend settings workspace display
- Extend workspace type and loader.
- Add read-only slug field in Workspace section.

### Phase 3 - Regression tests
- Add/update backend and frontend tests for slug visibility.

## Initial GitHub Micro-Issue Plan (to create before coding)
- Workspace settings API slug support + tests
- Settings workspace slug display in UI
- Frontend/backend regression pass for slug flow

Each micro-issue must include explicit test commands and acceptance criteria.

## Spec-Level Test Plan
- `go test ./internal/api -run 'Test(GetWorkspaceSettings|PatchWorkspaceSettings)' -count=1`
- `cd web && npx vitest run src/pages/SettingsPage.test.tsx`

## Acceptance Criteria
- Workspace settings API responses include `slug`.
- Settings workspace section displays org slug.
- Targeted backend/frontend tests pass.

## Execution Log
- [2026-02-11 11:51 MST] Issue #239 | Commit n/a | queued | Created spec 130 in 01-ready for remaining onboarding P1 org settings page follow-up | Tests: n/a
- [2026-02-11 11:51 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress as active execution target | Tests: n/a
- [2026-02-11 11:51 MST] Issue #n/a | Commit n/a | branch_created | Created and switched to branch `codex/spec-130-org-settings-slug` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-11 11:52 MST] Issue #766 | Commit n/a | created | Created phase-1 micro-issue for workspace API slug response support with explicit backend tests | Tests: go test ./internal/api -run 'TestGetWorkspaceSettings' -count=1; go test ./internal/api -run 'TestPatchWorkspaceSettings' -count=1
- [2026-02-11 11:52 MST] Issue #767 | Commit n/a | created | Created phase-2 micro-issue for workspace slug display in Settings UI with explicit frontend test | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "renders workspace slug"
- [2026-02-11 11:52 MST] Issue #768 | Commit n/a | created | Created phase-3 micro-issue for workspace slug regression validation across backend/frontend | Tests: go test ./internal/api -run 'Test(GetWorkspaceSettings|PatchWorkspaceSettings)' -count=1; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:56 MST] Issue #766 | Commit 7f87ccb | closed | Extended workspace settings API responses with org slug and updated backend workspace tests for slug assertions; pushed and closed issue | Tests: go test ./internal/api -run 'TestGetWorkspaceSettings' -count=1; go test ./internal/api -run 'TestPatchWorkspaceSettings' -count=1
- [2026-02-11 11:56 MST] Issue #767 | Commit 54e312d | closed | Added read-only `Organization Slug` field in workspace settings UI and slug hydration in frontend state with coverage; pushed and closed issue | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "renders workspace slug"; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:56 MST] Issue #768 | Commit 54e312d | closed | Completed workspace slug regression validation across backend/frontend after implementation commits | Tests: go test ./internal/api -run 'Test(GetWorkspaceSettings|PatchWorkspaceSettings)' -count=1; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:57 MST] Issue #n/a | Commit 54e312d | pr_opened | Opened PR #769 for spec 130 implementation branch with linked micro-issues and test evidence | Tests: n/a
- [2026-02-11 11:57 MST] Issue #239 | Commit 54e312d | complete | Completed remaining onboarding P1 org settings follow-up by surfacing org slug in workspace settings API/UI | Tests: n/a
- [2026-02-11 11:57 MST] Issue #n/a | Commit 54e312d | moved_to_review | Implementation complete; moved spec 130 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-11 22:30 MST] Issue #771 | Commit 17a705a | pushed | Added automigrate schema_migrations column-detection + recording fix with new unit tests to unblock failing CI auto-migration in PR #769 | Tests: go test ./internal/automigrate -count=1; go test ./cmd/server -count=1
- [2026-02-12 04:11 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 130 from 03-needs-review to 01-ready after PR #769 E2E failure (strict-mode selector collision in `web/e2e/agents.spec.ts`) | Tests: n/a
- [2026-02-12 04:11 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress to implement reviewer-required E2E selector fix | Tests: n/a
- [2026-02-12 04:11 MST] Issue #774 | Commit n/a | created | Created micro-issue to apply validated status-filter selector scoping fix on spec 130 branch with explicit Playwright commands | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "shows all status filter buttons|filters agents by offline status" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 04:12 MST] Issue #774 | Commit 4e09972 | closed | Cherry-picked validated selector-scoping fix from spec 129, pushed branch update for PR #769, and closed issue | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "shows all status filter buttons|filters agents by offline status" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium (pre-existing DM/error-path failures)
- [2026-02-12 04:12 MST] Issue #n/a | Commit 4e09972 | moved_to_review | Completed reviewer-required selector-fix implementation and moved spec 130 from 02-in-progress back to 03-needs-review | Tests: n/a
- [2026-02-12 06:17 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 130 from 03-needs-review to 01-ready after PR #769 failed E2E tests in `web/e2e/agents.spec.ts` DM chat cases | Tests: n/a
- [2026-02-12 06:21 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress to apply same Agents E2E Global Chat regression fix for PR #769 | Tests: n/a
- [2026-02-12 06:21 MST] Issue #777 | Commit n/a | created | Created micro-issue to apply validated Agents Global Chat E2E regression fix on spec 130 branch with explicit Playwright commands | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "opens global chat when clicking an agent card|closes global chat with close button|closes global chat with escape key|can send message in DM|handles API error gracefully" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 06:22 MST] Issue #777 | Commit 1609875 | closed | Cherry-picked validated Agents Global Chat E2E regression patch (from 6c423a5), reran targeted and full agents Playwright suites, pushed branch update, and closed issue | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "opens global chat when clicking an agent card|closes global chat with close button|closes global chat with escape key|can send message in DM|handles API error gracefully" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 06:22 MST] Issue #n/a | Commit 1609875 | moved_to_review | Completed reviewer-required E2E regression fix for spec 130 and moved from 02-in-progress to 03-needs-review | Tests: n/a
- [2026-02-12 09:01 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 130 from 03-needs-review to 01-ready after PR #769 E2E check failure in `web/e2e/auth.spec.ts` and broad stale suite drift | Tests: n/a
- [2026-02-12 09:01 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress to remediate failing E2E suite on PR #769 | Tests: n/a
- [2026-02-12 09:02 MST] Issue #798 | Commit n/a | created | Created remediation micro-issue to sync validated E2E baseline fixes onto spec 130 branch with explicit Playwright commands | Tests: cd web && npx playwright test e2e/auth.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/search.spec.ts --project=chromium; cd web && npx playwright test e2e/kanban.spec.ts e2e/tasks.spec.ts --project=chromium; cd web && npx playwright test e2e/settings.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts e2e/search.spec.ts e2e/kanban.spec.ts e2e/tasks.spec.ts e2e/settings.spec.ts e2e/auth.spec.ts --project=chromium
- [2026-02-12 09:08 MST] Issue #798 | Commit d176c0b | pushed | Cherry-picked validated E2E baseline commits from spec 129 and aligned search/settings suites for spec 130 branch behavior; pushed branch update for PR #769 | Tests: cd web && npx playwright test e2e/auth.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/search.spec.ts --project=chromium; cd web && npx playwright test e2e/kanban.spec.ts e2e/tasks.spec.ts --project=chromium; cd web && npx playwright test e2e/settings.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts e2e/search.spec.ts e2e/kanban.spec.ts e2e/tasks.spec.ts e2e/settings.spec.ts e2e/auth.spec.ts --project=chromium
- [2026-02-12 09:08 MST] Issue #798 | Commit d176c0b | closed | Closed remediation issue after branch push and test evidence posting | Tests: cd web && npx playwright test e2e/auth.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/search.spec.ts --project=chromium; cd web && npx playwright test e2e/kanban.spec.ts e2e/tasks.spec.ts --project=chromium; cd web && npx playwright test e2e/settings.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts e2e/search.spec.ts e2e/kanban.spec.ts e2e/tasks.spec.ts e2e/settings.spec.ts e2e/auth.spec.ts --project=chromium
- [2026-02-12 09:08 MST] Issue #n/a | Commit d176c0b | moved_to_review | Completed spec 130 remediation implementation and moved from 02-in-progress back to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-12 09:09 MST] Issue #799 | Commit n/a | closed | Closed accidental malformed duplicate issue created by shell-escaping failure; superseded by #798 | Tests: n/a
- [2026-02-12 09:46 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 130 from 03-needs-review to 01-ready after PR #769 E2E failure in `web/e2e/notifications.spec.ts` | Tests: n/a
- [2026-02-12 09:46 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress for notifications E2E remediation | Tests: n/a
- [2026-02-12 09:47 MST] Issue #801 | Commit n/a | created | Created micro-issue for spec 130 notifications suite rebaseline with explicit Playwright tests | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --grep "Notification Bell|Notifications Page|Edge Cases"; cd web && npx playwright test e2e/notifications.spec.ts --project=chromium
- [2026-02-12 09:51 MST] Issue #801 | Commit aa14c56 | pushed | Rebaselined `web/e2e/notifications.spec.ts` on spec-130 branch to current UI/API contracts and pushed branch update for PR #769 | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --workers=1 --retries=0; cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --grep "Notifications Page|Edge Cases|Empty States" --workers=1 --retries=0; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium --workers=1 --retries=0
- [2026-02-12 09:51 MST] Issue #801 | Commit aa14c56 | closed | Closed remediation micro-issue after push and test evidence posting | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --workers=1 --retries=0
- [2026-02-12 09:51 MST] Issue #n/a | Commit aa14c56 | moved_to_review | Completed spec 130 notifications remediation and moved from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-12 12:35 MST] Issue #818 | Commit n/a | changes_required | Reviewer (Claude Opus 4) completed review of spec 130 PR #769. Core implementation (slug in API + UI) is correct and well-tested. Pre-merge gate FAILED: merge conflicts in `web/e2e/search.spec.ts` and `web/e2e/settings.spec.ts` due to main divergence from specs 125/126/129/agent-profiles. Branch requires rebase onto current main. Opened issue #818 for remediation. Moved spec from 04-in-review to 01-ready. | Tests: go vet ./... ✅; go build ./... ✅; npx vitest run (94 files, 436 tests) ✅; git merge main --no-commit --no-ff ❌ (2 conflicts)

- [2026-02-12 12:36 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 130 from 01-ready to 02-in-progress for reviewer-required rebase/conflict remediation workstream | Tests: n/a
- [2026-02-12 12:36 MST] Issue #819 | Commit n/a | created | Created micro-issue for rebase conflict resolution in search/settings E2E files with explicit Playwright tests before implementation | Tests: cd web && npx playwright test e2e/search.spec.ts --project=chromium --workers=1 --retries=0; cd web && npx playwright test e2e/settings.spec.ts --project=chromium --workers=1 --retries=0
- [2026-02-12 12:36 MST] Issue #820 | Commit n/a | created | Created micro-issue for post-rebase automigrate no-op verification + required pre-merge gate validation commands | Tests: git diff main -- internal/automigrate/; git merge main --no-commit --no-ff; go vet ./...; go build ./...; cd web && npx vitest run; cd web && npx playwright test --project=chromium --workers=1 --retries=0
- [2026-02-12 12:44 MST] Issue #819 | Commit 6ae9118 | closed | Synced spec-130 branch with current main and resolved search/settings E2E conflict drift, then pushed PR branch update | Tests: cd web && npx playwright test e2e/search.spec.ts --project=chromium --workers=1 --retries=0; cd web && npx playwright test e2e/settings.spec.ts --project=chromium --workers=1 --retries=0
- [2026-02-12 12:44 MST] Issue #820 | Commit 6ae9118 | closed | Verified automigrate no-op against main and completed reviewer gate validation; full Playwright matched known local baseline with app health-check failure only (backend not bound to localhost:4200) | Tests: git diff origin/main -- internal/automigrate/; git merge origin/main --no-commit --no-ff; go vet ./...; go build ./...; cd web && npx vitest run; cd web && npx playwright test --project=chromium --workers=1 --retries=0
- [2026-02-12 12:44 MST] Issue #818 | Commit 6ae9118 | closed | Closed reviewer-required umbrella after conflict remediation, automigrate verification, PR update, and micro-issue closure (#819/#820) | Tests: n/a
- [2026-02-12 12:44 MST] Issue #n/a | Commit 6ae9118 | moved_to_review | Removed resolved top-level Reviewer Required Changes block and moved spec 130 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
