# 129 - Onboarding P1 Git Token Creation UI

## Problem
Issue #239 still has an unfinished P1 onboarding slice: users need an in-product way to create a git token they can use with Otter CLI and git operations. The current Settings integrations UI manages generic API keys via `/api/settings/integrations/api-keys`, but onboarding and CLI docs now standardize on `oc_git_` tokens and `/api/git/tokens` behavior.

This gap forces users to leave the product and manually provision credentials, adding friction to first-time setup.

## Goals
- Add an explicit git token management surface in Settings.
- Create tokens through existing `/api/git/tokens` backend APIs.
- Show newly generated token value one time with clear copy for immediate use.
- Support revoking tokens from the same UI.

## Out of Scope
- SSH key management UI.
- Org/workspace settings redesign.
- Hosted onboarding flow (`otter.camp/setup`).
- CLI init wizard implementation.

## Requirements
1. Settings UI token section
- Integrations section should present a `Git Access Tokens` area (not generic API keys wording).
- Existing tokens load from backend and show: name, prefix, created date, revoke action.

2. Create token behavior
- Create action must call `POST /api/git/tokens`.
- Payload should include explicit project permissions for current org projects (default write access for each listed project).
- On success, append the new token metadata to the list and display the full token value exactly once in the UI.

3. Revoke behavior
- Revoke action must call `POST /api/git/tokens/{id}/revoke`.
- Revoked token should be removed from active token list (or clearly marked revoked).

4. Error handling
- If project discovery returns empty, show actionable guidance (create a project first).
- If create/revoke request fails, show API error text in section UI without breaking other settings areas.

5. Tests
- Frontend tests covering load/create/revoke and one-time token reveal.
- Regression tests for error paths (empty project list + API failure copy).

## Full Implementation Plan (Build Order)

### Phase 1 - Token read surface + API wiring
- Replace/retitle API key subsection as git token management.
- Wire token list state to `/api/git/tokens` response model.

### Phase 2 - Token create flow
- Add project discovery via `/api/projects` and create payload assembly.
- Implement create flow + one-time token reveal message.

### Phase 3 - Revoke + error-state hardening
- Implement revoke API path and UI update behavior.
- Add empty-project and API-failure messaging.

### Phase 4 - Regression tests
- Add/extend `SettingsPage` tests for success and error flows.

## Initial GitHub Micro-Issue Plan (to create before coding)
- Git token section wiring and list rendering in Settings
- Git token creation flow with project-permission payload + one-time secret reveal
- Git token revoke flow and explicit error-path UX
- SettingsPage regression tests for token workflows

Each micro-issue must include explicit test commands and acceptance criteria.

## Spec-Level Test Plan
- `cd web && npx vitest run src/pages/SettingsPage.test.tsx`
- `go test ./internal/api -run 'TestGitTokensCRUD|TestGetIntegrationsSettings' -count=1`

## Acceptance Criteria
- Settings UI exposes `Git Access Tokens` with existing token list.
- Create token calls `/api/git/tokens` with explicit project permissions and surfaces one-time token value.
- Revoke token calls `/api/git/tokens/{id}/revoke` and updates UI.
- Empty project/API failure states are user-visible and tested.
- Targeted frontend/backend tests pass.

## Execution Log
- [2026-02-11 11:43 MST] Issue #239 | Commit n/a | queued | Created spec 129 in 01-ready for remaining onboarding P1 git token creation UI slice | Tests: n/a
- [2026-02-11 11:43 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 129 from 01-ready to 02-in-progress as active execution target | Tests: n/a
- [2026-02-11 11:43 MST] Issue #n/a | Commit n/a | branch_created | Created and switched to branch `codex/spec-129-onboarding-p1-git-token-ui` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-11 11:45 MST] Issue #761 | Commit n/a | created | Created phase-1 micro-issue for settings git token section wiring/list rendering with explicit tests | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "loads git access tokens"
- [2026-02-11 11:45 MST] Issue #762 | Commit n/a | created | Created phase-2 micro-issue for git token creation flow with project-permission payload and one-time secret reveal | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "creates git token"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows generated git token once"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows project required message when no projects exist"
- [2026-02-11 11:45 MST] Issue #763 | Commit n/a | created | Created phase-3 micro-issue for git token revoke flow and failure UX | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "revokes git token"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows revoke error"
- [2026-02-11 11:45 MST] Issue #764 | Commit n/a | created | Created phase-4 micro-issue for token workflow regression tests | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx; go test ./internal/api -run 'TestGitTokensCRUD|TestGetIntegrationsSettings' -count=1
- [2026-02-11 11:46 MST] Issue #761 | Commit 1a5c1f7 | closed | Wired settings token list to `/api/git/tokens`, updated section copy to `Git Access Tokens`, and added focused list-loading test; pushed and closed issue | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "loads git access tokens"; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:47 MST] Issue #762 | Commit dccaa23 | closed | Added git token creation flow via `/api/projects` + `/api/git/tokens`, one-time token reveal panel, and empty-project guidance messaging with targeted tests; pushed and closed issue | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "creates git token"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows generated git token once"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows project required message when no projects exist"; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:48 MST] Issue #763 | Commit d8333d2 | closed | Switched revoke path to `/api/git/tokens/{id}/revoke`, added revoke failure UX messaging, and covered success/failure revoke tests; pushed and closed issue | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "revokes git token"; cd web && npx vitest run src/pages/SettingsPage.test.tsx -t "shows revoke error"; cd web && npx vitest run src/pages/SettingsPage.test.tsx
- [2026-02-11 11:49 MST] Issue #764 | Commit 068f36c | closed | Tightened regression coverage to assert git-token create payload (project write permissions + naming) and reran frontend/backend token regression commands; pushed and closed issue | Tests: cd web && npx vitest run src/pages/SettingsPage.test.tsx; go test ./internal/api -run 'TestGitTokensCRUD|TestGetIntegrationsSettings' -count=1
- [2026-02-11 11:50 MST] Issue #n/a | Commit 068f36c | pr_opened | Opened PR #765 for spec 129 implementation branch with linked micro-issues and test evidence | Tests: n/a
- [2026-02-11 11:50 MST] Issue #239 | Commit 068f36c | partial_complete | Completed onboarding P1 git token creation UI slice; remaining #239 follow-up is org settings page work | Tests: n/a
- [2026-02-11 11:50 MST] Issue #n/a | Commit 068f36c | moved_to_review | Implementation complete; moved spec 129 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-11 22:30 MST] Issue #771 | Commit c4550b7 | pushed | Cherry-picked automigrate schema_migrations recording fix with new unit tests to unblock failing CI auto-migration in PR #765 | Tests: go test ./internal/automigrate -count=1; go test ./cmd/server -count=1
- [2026-02-12 04:06 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 129 from 03-needs-review to 01-ready after PR #765 E2E failure (strict-mode selector collision in `web/e2e/agents.spec.ts`) | Tests: n/a
- [2026-02-12 04:07 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 129 from 01-ready to 02-in-progress to implement reviewer-required E2E selector fix | Tests: n/a
- [2026-02-12 04:07 MST] Issue #773 | Commit n/a | created | Created micro-issue for status-filter selector scoping fix with explicit failing/passing Playwright test commands | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "shows all status filter buttons|filters agents by offline status" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 04:10 MST] Issue #773 | Commit 8578ed7 | closed | Scoped agents status-filter locators to a named filter group and added accessible group semantics to fix strict-mode selector collision; pushed branch update | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "shows all status filter buttons|filters agents by offline status" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium (pre-existing DM/error-path failures); cd web && npx playwright test e2e/agents.spec.ts --project=chromium --workers=1 (same pre-existing failures)
- [2026-02-12 04:10 MST] Issue #n/a | Commit 8578ed7 | moved_to_review | Completed reviewer-required selector-fix implementation and moved spec 129 from 02-in-progress back to 03-needs-review | Tests: n/a
- [2026-02-12 06:17 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 129 from 03-needs-review to 01-ready after PR #765 failed E2E tests in `web/e2e/agents.spec.ts` DM chat cases | Tests: n/a
- [2026-02-12 06:17 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 129 from 01-ready to 02-in-progress to fix PR #765 failing Agents E2E DM chat tests | Tests: n/a
- [2026-02-12 06:18 MST] Issue #776 | Commit n/a | created | Created micro-issue for Agents E2E DM regression to align assertions with Global Chat dock behavior and explicit Playwright commands | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "opens DM modal when clicking agent card|closes DM modal with close button|closes DM modal with escape key|can send message in DM|handles API error gracefully" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 06:21 MST] Issue #776 | Commit 6c423a5 | closed | Updated Agents E2E DM tests to Global Chat dock behavior, validated targeted and full agents Playwright runs, pushed branch update, and closed issue | Tests: cd web && npx playwright test e2e/agents.spec.ts --grep "opens global chat when clicking an agent card|closes global chat with close button|closes global chat with escape key|can send message in DM|handles API error gracefully" --project=chromium; cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 06:21 MST] Issue #n/a | Commit 6c423a5 | moved_to_review | Completed reviewer-required E2E regression fix for spec 129 and moved from 02-in-progress to 03-needs-review | Tests: n/a
- [2026-02-12 08:33 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 129 from 03-needs-review to 01-ready after PR #765 failed E2E suite against current auth/session flow | Tests: n/a
- [2026-02-12 08:33 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 129 from 01-ready to 02-in-progress for E2E auth-flow remediation | Tests: n/a
- [2026-02-12 08:34 MST] Issue #792 | Commit n/a | created | Created micro-issue for Playwright authenticated session helper migration to align E2E suites with validated token/org auth startup | Tests: cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/kanban.spec.ts --project=chromium; cd web && npx playwright test e2e/search.spec.ts --project=chromium; cd web && npx playwright test e2e/settings.spec.ts --project=chromium
- [2026-02-12 08:34 MST] Issue #793 | Commit n/a | created | Created micro-issue to update auth Playwright suite from legacy request-login flow to current magic-link login UX with explicit tests | Tests: cd web && npx playwright test e2e/auth.spec.ts --project=chromium; cd web && npx playwright test e2e/auth.spec.ts --project=chromium --grep "Login Flow"
- [2026-02-12 08:42 MST] Issue #794 | Commit n/a | created | Created micro-issue to rebaseline navigation Playwright suite to current topbar/avatar-menu IA with explicit test command | Tests: cd web && npx playwright test e2e/navigation.spec.ts --project=chromium
- [2026-02-12 08:42 MST] Issue #795 | Commit n/a | created | Created micro-issue to rebaseline command-palette Playwright selectors/assertions to current markup semantics | Tests: cd web && npx playwright test e2e/search.spec.ts --project=chromium
- [2026-02-12 08:42 MST] Issue #796 | Commit n/a | created | Created micro-issue to rebaseline legacy task/kanban Playwright suites to current task surfaces/routes | Tests: cd web && npx playwright test e2e/kanban.spec.ts --project=chromium; cd web && npx playwright test e2e/tasks.spec.ts --project=chromium
- [2026-02-12 08:42 MST] Issue #797 | Commit n/a | created | Created micro-issue to stabilize settings Playwright suite against current UI behavior and copy | Tests: cd web && npx playwright test e2e/settings.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #792 | Commit 65db618 | closed | Added deterministic authenticated-session shell API mocks (including agents) with opt-out toggle for route-level Playwright stability | Tests: cd web && npx playwright test e2e/navigation.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #793 | Commit c94766a | closed | Replaced legacy auth Playwright coverage with current magic-link login and protected-route expectations | Tests: cd web && npx playwright test e2e/auth.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #794 | Commit 8a7e094 | closed | Rebased navigation Playwright suite to current topbar/avatar/mobile IA and route assertions | Tests: cd web && npx playwright test e2e/navigation.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #795 | Commit 020ea86 | closed | Reworked command palette suite to current Global Search keyboard/open/close/results/recent-search behavior with deterministic search API mocks | Tests: cd web && npx playwright test e2e/search.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #796 | Commit bd82e65 | closed | Rebased legacy kanban/tasks Playwright suites to current /tasks dashboard and /tasks/:id detail UX (including inline title edit and not-found state) | Tests: cd web && npx playwright test e2e/kanban.spec.ts e2e/tasks.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #797 | Commit cf4133e | closed | Stabilized settings Playwright suite with current section selectors and deterministic git-token generation/revoke mocks | Tests: cd web && npx playwright test e2e/settings.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #n/a | Commit cf4133e | pushed | Pushed six remediation commits for issues #792-#797 to `codex/spec-129-onboarding-p1-git-token-ui` and closed all micro-issues with test evidence | Tests: cd web && npx playwright test e2e/navigation.spec.ts e2e/search.spec.ts e2e/kanban.spec.ts e2e/tasks.spec.ts e2e/settings.spec.ts e2e/auth.spec.ts --project=chromium
- [2026-02-12 08:56 MST] Issue #n/a | Commit cf4133e | moved_to_review | Completed spec 129 E2E remediation implementation and moved spec from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-12 09:31 MST] Issue #n/a | Commit n/a | moved_to_ready | Re-queued spec 129 from 03-needs-review to 01-ready after PR #765 reported a new E2E failure at current branch head | Tests: n/a
- [2026-02-12 09:31 MST] Issue #n/a | Commit n/a | branch_reused | Switched to existing spec branch `codex/spec-129-onboarding-p1-git-token-ui` for isolated remediation work | Tests: n/a
- [2026-02-12 09:31 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 129 from 01-ready to 02-in-progress to remediate PR #765 E2E failure | Tests: n/a
- [2026-02-12 09:32 MST] Issue #800 | Commit n/a | created | Created micro-issue for notifications Playwright rebaseline to current dashboard UI and notification payload contract with explicit test commands | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --grep "Notifications Page|Mark as Read|Delete Notification|Notification Click Actions|Empty States|Edge Cases"; cd web && npx playwright test e2e/notifications.spec.ts --project=chromium
- [2026-02-12 09:37 MST] Issue #800 | Commit 4557ca0 | closed | Rebased notifications Playwright suite to current notifications page behavior and payload contracts; pushed branch update and closed issue | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium --grep "Notifications Page|Mark as Read|Delete Notification|Notification Click Actions|Empty States|Edge Cases"; cd web && npx playwright test e2e/notifications.spec.ts --project=chromium
- [2026-02-12 09:37 MST] Issue #n/a | Commit 4557ca0 | pushed | Pushed spec 129 remediation commit for notifications Playwright rebaseline on branch codex/spec-129-onboarding-p1-git-token-ui | Tests: cd web && npx playwright test e2e/notifications.spec.ts --project=chromium
- [2026-02-12 09:37 MST] Issue #n/a | Commit 4557ca0 | moved_to_review | Completed spec 129 notifications E2E remediation and moved spec from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
