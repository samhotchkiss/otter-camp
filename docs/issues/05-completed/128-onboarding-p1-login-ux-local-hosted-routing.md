# 128 - Onboarding P1 Login UX Local/Hosted Routing

## Problem
Issue #239 now includes concrete product decisions for onboarding (Feb 10), but the current login experience still presents a single magic-link form with no explicit setup-path guidance. New users are missing:
- A clear local-first onboarding path.
- A clear hosted path deferral to `otter.camp/setup`.
- Decision-aligned messaging on where setup happens today.

## Goals
- Make login/onboarding intent explicit with a local vs hosted choice on the login page.
- Keep local onboarding as the default and preserve current magic-link flow behavior.
- Provide a clear hosted deferment message linking users to `https://otter.camp/setup`.

## Out of Scope
- CLI onboarding wizard implementation (`otter init`) and dependency installer UX.
- Git token creation UI.
- Org settings page implementation.
- Hosted account provisioning backend.

## Requirements
1. Login page path selection
- Add a setup-path selector on `LoginPage` with `Local` and `Hosted` choices.
- Default selection must be `Local`.

2. Local path behavior
- Local mode continues to show the existing magic-link onboarding form.
- Existing submit/error/success behavior remains intact.

3. Hosted path behavior
- Hosted mode hides the local magic-link form and shows hosted deferment guidance.
- Include a direct link/button to `https://otter.camp/setup`.
- Include clear copy that hosted setup is deferred for now.

4. Tests
- Add page-level tests for local default rendering, hosted rendering, and mode switching.
- Add regression coverage that local mode still submits via `useAuth().login` and preserves success/error states.

## Full Implementation Plan (Build Order)

### Phase 1 - Add path selector and hosted deferment surface
- Add setup-path state and UI controls on `LoginPage`.
- Render local form vs hosted guidance conditionally.

### Phase 2 - Preserve local auth behavior under new UI
- Ensure local mode continues to call `login(email, name, org)` exactly as before.
- Keep loading, success, and error handling unchanged.

### Phase 3 - Add regression tests and polish copy
- Add `LoginPage` tests for mode-switch rendering and local submit flow.
- Verify user-facing copy/labels match issue #239 decisions.

## Initial GitHub Micro-Issue Plan (to create before coding)
- Login UI mode selector + hosted deferment view
- Local flow regression retention under mode switch
- Login page tests + copy polish

Each micro-issue must include explicit test commands and acceptance criteria.

## Spec-Level Test Plan
- `cd web && npx vitest run src/pages/LoginPage.test.tsx`
- `cd web && npx vitest run src/components/AuthHandler.test.tsx src/contexts/__tests__/AuthContext.test.tsx`

## Acceptance Criteria
- Login page defaults to `Local` mode and shows magic-link form.
- Switching to `Hosted` mode shows deferment messaging and a link to `https://otter.camp/setup`.
- Switching back to `Local` restores the magic-link form without regressions.
- Local submit success and error paths still behave correctly.
- Targeted frontend tests pass.

## Execution Log
- [2026-02-11 11:21 MST] Issue #239 | Commit n/a | queued | Created spec 128 in 01-ready from onboarding P1 decisions in issue #239 to resume actionable execution | Tests: n/a
- [2026-02-11 11:22 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 128 from 01-ready to 02-in-progress as active execution target | Tests: n/a
- [2026-02-11 11:22 MST] Issue #n/a | Commit n/a | branch_created | Created and switched to branch `codex/spec-128-onboarding-p1-login-ux` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-11 11:23 MST] Issue #757 | Commit n/a | created | Created phase-1 micro-issue for LoginPage local/hosted selector and hosted setup deferment view with explicit tests | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx -t "defaults to local onboarding mode"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows hosted setup guidance when hosted mode is selected"
- [2026-02-11 11:23 MST] Issue #758 | Commit n/a | created | Created phase-2 micro-issue for preserving local magic-link submit/success/error behavior under mode switching with explicit tests | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx -t "submits local mode via login"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows magic-link success state after local submit"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows API error when local submit fails"
- [2026-02-11 11:23 MST] Issue #759 | Commit n/a | created | Created phase-3 micro-issue for decision-aligned login copy polish and full LoginPage regression coverage with explicit tests | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx; cd web && npx vitest run src/components/AuthHandler.test.tsx src/contexts/__tests__/AuthContext.test.tsx
- [2026-02-11 11:24 MST] Issue #757 | Commit 3200d7a | closed | Added LoginPage local/hosted setup selector with hosted deferment panel and setup link, plus baseline page tests for mode behavior; pushed and closed issue | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx -t "defaults to local onboarding mode"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows hosted setup guidance when hosted mode is selected"
- [2026-02-11 11:26 MST] Issue #758 | Commit 64fbc3e | closed | Added LoginPage regression tests to prove local submit/login success/error behavior remains intact after hosted/local mode switches; pushed and closed issue | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx -t "submits local mode via login"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows magic-link success state after local submit"; cd web && npx vitest run src/pages/LoginPage.test.tsx -t "shows API error when local submit fails"
- [2026-02-11 11:28 MST] Issue #759 | Commit a91993f | closed | Polished decision-aligned local/hosted onboarding copy and expanded LoginPage regression coverage; ran auth-context/auth-handler regressions; pushed and closed issue | Tests: cd web && npx vitest run src/pages/LoginPage.test.tsx; cd web && npx vitest run src/components/AuthHandler.test.tsx src/contexts/__tests__/AuthContext.test.tsx
- [2026-02-11 11:29 MST] Issue #n/a | Commit a91993f | pr_opened | Opened PR #760 for spec 128 implementation branch with issue links and test evidence | Tests: n/a
- [2026-02-11 11:29 MST] Issue #239 | Commit a91993f | partial_complete | Completed onboarding P1 login UX slice (local/hosted path selection + hosted deferment messaging); remaining #239 slices are git token UI and org settings | Tests: n/a
- [2026-02-11 11:29 MST] Issue #n/a | Commit a91993f | moved_to_review | Implementation complete; moved spec 128 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
