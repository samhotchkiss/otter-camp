# 127 - Onboarding P0 Auth and Magic Link Hardening

## Problem
Open onboarding issue #239 has P0 implementation work that is actionable now, but local queue state had no ready/in-progress specs. Current gaps block new-account onboarding reliability:
- `requireSessionIdentity` does not consistently accept `oc_magic_` session tokens.
- Frontend auth bootstrap uses hardcoded token assumptions instead of validating session state via API.
- Magic-link completion handler does not fully accept/create custom org name/slug/email inputs for self-serve onboarding.

## Goals
- Make magic-link session tokens first-class in API identity resolution.
- Remove hardcoded frontend token validation paths and rely on server session validation.
- Ensure magic-link onboarding flow accepts explicit org/email inputs and persists expected org/user identity.

## Out of Scope
- P1 product UX decisions from issue #239 (login UX variants, git token UI, org settings UX).
- Billing/free-tier limits.
- Broad onboarding redesign outside P0 bug-fix scope.

## Requirements
1. API identity acceptance for magic-link tokens
- `requireSessionIdentity` (and any shared auth parsing path) must accept and validate `oc_magic_` tokens.
- Fail closed for invalid/malformed tokens.
- Existing non-magic token behavior must remain unchanged.

2. Frontend auth validation via API
- Replace hardcoded token allowlists/heuristics in frontend auth bootstrap with explicit server validation.
- On invalid session, clear local auth state and route consistently to login/onboarding.
- Preserve existing success-path user/session hydration behavior.

3. HandleMagicLink onboarding inputs
- `HandleMagicLink` must accept custom org name/slug/email inputs required for onboarding.
- Persist/return expected org + user linkage for first-login flow.
- Validate malformed/missing inputs with clear API errors.

4. Tests
- Add/extend tests for API auth acceptance/rejection around `oc_magic_` tokens.
- Add frontend auth bootstrap tests proving API-backed validation path.
- Add magic-link handler tests for custom org/email success + validation failures.

## Full Implementation Plan (Build Order)

### Phase 1 - Auth token parser acceptance
- Extend backend auth identity parsing to include `oc_magic_` token family.
- Add focused unit tests for accepted and rejected token cases.

### Phase 2 - Frontend auth bootstrap hardening
- Remove hardcoded frontend token assumptions.
- Wire startup/session checks to API validation endpoint(s).
- Add component/context tests for valid/invalid session outcomes.

### Phase 3 - Magic-link handler input support
- Update magic-link completion handler schema/parsing for custom org name/slug/email.
- Ensure org/user creation or lookup logic honors supplied values safely.
- Add handler tests for success and validation errors.

### Phase 4 - Regression pass + docs/comments
- Add/update concise comments/docs where behavior changed.
- Run targeted backend/frontend regressions for onboarding/auth.

## Initial GitHub Micro-Issue Plan (to create before coding)
- Auth identity acceptance slice (`oc_magic_` support + tests)
- Frontend auth validation slice (API-backed bootstrap + tests)
- Magic-link handler input slice (org/email handling + tests)
- Regression/docs slice (cross-surface verification)

Each micro-issue must include explicit test commands and acceptance criteria.

## Spec-Level Test Plan
- `go test ./internal/api -run 'Test(RequireSessionIdentity|Auth).*Magic' -count=1`
- `go test ./internal/auth/...`
- `cd web && npm test -- src/contexts/AuthContext.test.tsx --run`
- `cd web && npm test -- src/pages/LoginPage.test.tsx --run`

## Acceptance Criteria
- Backend accepts valid `oc_magic_` session identities and rejects invalid/malformed variants.
- Frontend auth startup relies on API validation rather than hardcoded token logic.
- Magic-link handler supports custom org/email onboarding inputs with tested success/failure paths.
- Targeted regression suites pass.

## Execution Log
- [2026-02-11 09:39 MST] Issue #239 | Commit n/a | queued | Created spec 127 in 01-ready from open onboarding P0 work to reconcile local queue with GitHub state | Tests: n/a
- [2026-02-11 09:40 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 127 from 01-ready to 02-in-progress as active execution target | Tests: n/a
- [2026-02-11 09:41 MST] Issue #n/a | Commit n/a | branch_created | Created and switched to branch `codex/spec-127-onboarding-p0-auth` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-11 09:46 MST] Issue #747 | Commit n/a | created | Created phase-1 micro-issue for backend `requireSessionIdentity` magic-token acceptance/rejection coverage with explicit tests | Tests: go test ./internal/api -run 'TestRequireSessionIdentity.*Magic|TestShouldBypassDBTokenValidation' -count=1; go test ./internal/api -run 'TestHandleValidateToken.*' -count=1
- [2026-02-11 09:46 MST] Issue #748 | Commit n/a | created | Created phase-2 micro-issue for frontend API-backed auth bootstrap validation with explicit tests | Tests: cd web && npm test -- src/contexts/__tests__/AuthContext.test.tsx --run; cd web && npm test -- src/components/AuthHandler.test.tsx --run
- [2026-02-11 09:46 MST] Issue #749 | Commit n/a | created | Created phase-3 micro-issue for HandleMagicLink custom org/email onboarding input support with explicit tests | Tests: go test ./internal/api -run 'TestHandleMagicLink.*(Custom|Org|Email)' -count=1; go test ./internal/api -run 'TestHandleMagicLink.*' -count=1
- [2026-02-11 09:46 MST] Issue #750 | Commit n/a | created | Created phase-4 micro-issue for auth onboarding regression/docs pass with explicit tests | Tests: go test ./internal/api -run 'Test(RequireSessionIdentity|HandleMagicLink|HandleValidateToken).*' -count=1; cd web && npm test -- src/contexts/__tests__/AuthContext.test.tsx src/components/AuthHandler.test.tsx --run
- [2026-02-11 09:52 MST] Issue #747 | Commit 55867ba | closed | Hardened `requireSessionIdentity` malformed-token rejection path and added magic-token acceptance/rejection regression tests; pushed and closed issue | Tests: go test ./internal/api -run 'TestRequireSessionIdentity.*Magic|TestShouldBypassDBTokenValidation' -count=1; go test ./internal/api -run 'TestHandleValidateToken.*' -count=1; go test ./internal/api -run 'TestRequireSessionIdentity.*|TestHandleValidateToken.*|TestShouldBypassDBTokenValidation' -count=1
- [2026-02-11 10:02 MST] Issue #748 | Commit b2967bb | closed | Refactored frontend auth bootstrap to API-validated startup sessions, removed `oc_magic_` heuristic bootstrap path, and added invalid-token cleanup regression coverage; pushed and closed issue | Tests: cd web && npm test -- src/contexts/__tests__/AuthContext.test.tsx --run; cd web && npm test -- src/components/AuthHandler.test.tsx --run; cd web && npm test -- src/contexts/__tests__/AuthContext.test.tsx src/components/AuthHandler.test.tsx --run
- [2026-02-11 10:11 MST] Issue #749 | Commit f747243 | closed | Extended `HandleMagicLink` custom org/email input support (`organization_name`/`org_name`/`org_slug`), added early validation errors, and covered success/failure paths with sqlmock tests; pushed and closed issue | Tests: go test ./internal/api -run 'TestHandleMagicLink(SupportsCustomOrgSlugAndEmail|RejectsInvalidCustomEmail|RejectsInvalidCustomOrgSlug)' -count=1; go test ./internal/api -run 'TestHandleMagicLink.*' -count=1
- [2026-02-11 10:13 MST] Issue #750 | Commit d82f5e2 | closed | Completed auth onboarding regression/docs pass by converting session-auth coverage to sqlmock (non-skipped) and running backend/frontend auth regression commands; pushed and closed issue | Tests: go test ./internal/api -run 'Test(RequireSessionIdentity|HandleMagicLink|HandleValidateToken).*' -count=1; cd web && npm test -- src/contexts/__tests__/AuthContext.test.tsx src/components/AuthHandler.test.tsx --run
- [2026-02-11 10:16 MST] Issue #n/a | Commit d82f5e2 | pr_opened | Opened PR #753 for spec 127 implementation branch with issue links and test evidence | Tests: n/a
- [2026-02-11 10:16 MST] Issue #239 | Commit d82f5e2 | partial_complete | Completed P0 implementation slices (issues #747-#750) and documented remaining P1 product-decision work as follow-up | Tests: n/a
- [2026-02-11 10:16 MST] Issue #n/a | Commit d82f5e2 | moved_to_review | Implementation complete; moved spec 127 from 02-in-progress to 03-needs-review pending external reviewer validation | Tests: n/a
- [2026-02-11 10:23 MST] Issue #n/a | Commit n/a | in_progress | Resumed spec 127 reviewer-required cycle: moved spec from 01-ready to 02-in-progress and switched back to branch `codex/spec-127-onboarding-p0-auth` | Tests: n/a
- [2026-02-11 10:25 MST] Issue #754 | Commit 0437e7b | closed | Fixed `setAuthDBMock` to avoid copying `sync.Once` lock value and restored `go vet` pass | Tests: go vet ./...; go test ./internal/api -run 'TestHandleMagicLink(SupportsCustomOrgSlugAndEmail|RejectsInvalidCustomEmail|RejectsInvalidCustomOrgSlug)' -count=1
- [2026-02-11 10:29 MST] Issue #755 | Commit b0268c9 | closed | Fixed localhost magic-link bootstrap regression (`admin@localhost`) with local-token path handling and added backend/frontend regressions | Tests: go test ./internal/api -run 'TestHandleMagicLinkAllowsLocalhostEmailWhenUsingLocalAuthToken' -count=1; go test ./internal/api -run 'TestHandleMagicLink(SupportsCustomOrgSlugAndEmail|RejectsInvalidCustomEmail|RejectsInvalidCustomOrgSlug)' -count=1; go test ./internal/api -run 'TestHandleMagicLink.*' -count=1; cd web && npx vitest run src/contexts/__tests__/AuthContext.test.tsx
- [2026-02-11 10:30 MST] Issue #756 | Commit efd96c2 | closed | Added explicit malformed magic-token injection-character rejection tests for `/`, `;`, and `..` variants | Tests: go test ./internal/api -run 'TestRequireSessionIdentity.*' -count=1; go test ./internal/api -run 'Test(RequireSessionIdentity|HandleMagicLink).*' -count=1
- [2026-02-11 10:30 MST] Issue #n/a | Commit efd96c2 | reviewer_changes_resolved | Completed all reviewer-required fixes (#754-#756), removed resolved reviewer block from top of spec, and reran pre-merge regressions | Tests: go vet ./...; go test ./internal/api -run 'Test(RequireSessionIdentity|HandleMagicLink|HandleValidateToken).*' -count=1; cd web && npx vitest run src/contexts/__tests__/AuthContext.test.tsx src/components/AuthHandler.test.tsx
- [2026-02-11 10:31 MST] Issue #n/a | Commit efd96c2 | moved_to_review | Reviewer-required implementation complete; moved spec 127 from 02-in-progress to 03-needs-review pending external sign-off | Tests: n/a
