# Progress Log

## [2026-02-07 15:05:26 MST] Session resumed
- Prioritized: fix global chat flashing/remount behavior before spec implementation.
- Remaining:
  - Validate + push flashing fix
  - Break down Spec 001 into GitHub issues with tests
  - Implement Spec 001 issues
  - Break down Spec 002 into GitHub issues with tests
  - Implement Spec 002 issues

## [2026-02-07 15:06:36 MST] Completed chat flashing fix
- Refactored global chat surface reload dependencies to use stable conversation identity (type/key/thread/project/issue IDs) instead of full conversation object metadata.
- Added regression tests in web/src/components/chat/GlobalChatSurface.test.tsx:
  - Metadata-only conversation updates do not trigger refetch.
  - Switching to a different conversation does trigger refetch.
- Validation:
  - web: npm test -- GlobalChatSurface.test.tsx --run
  - web: npm run build:typecheck
- Remaining:
  - Decompose Spec 001 into small GitHub issues with test lists
  - Implement Spec 001 issues in order (commit + push each)
  - Decompose Spec 002 into small GitHub issues with test lists
  - Implement Spec 002 issues in order (commit + push each)

## [2026-02-07 15:07:24 MST] Pushed anti-flash regression baseline
- Pushed commit: 51bd50c
- Added tests that ensure GlobalChatSurface does not refetch on metadata-only conversation updates.
- Created and pushed progress tracking files (progress-log.md, notes.md).
- Remaining:
  - Create ordered GitHub issue breakdown for Spec 001 with tests
  - Implement Spec 001 issues one-by-one with commits/pushes
  - Create ordered GitHub issue breakdown for Spec 002 with tests
  - Implement Spec 002 issues one-by-one with commits/pushes

## [2026-02-07 15:10:51 MST] Completed Spec001 Issue #257 (schema migration)
- Added migration  with new  columns:
  - , , , , , 
- Added DB constraints for  and  plus project-scoped indexes for status/owner/priority queries.
- Added store-level DB test  validating:
  - defaults (, )
  - nullable fields remain null by default
  - invalid status/priority writes are rejected.
- Validation run: ok  	github.com/samhotchkiss/otter-camp/internal/store	0.010s.
- Remaining (Spec001): #258, #259, #260, #261, #262, #263.

## [2026-02-07 15:11:15 MST] Progress log correction for #257 entry
- Correcting prior malformed markdown line caused by shell interpolation while logging.
- Completed Spec001 Issue #257 details:
  - Added migration `038_add_issue_work_tracking_columns` for `project_issues`.
  - Added columns: `owner_agent_id`, `work_status`, `priority`, `due_at`, `next_step`, `next_step_due_at`.
  - Added DB check constraints for `work_status` and `priority` values.
  - Added indexes for project+status, project+owner, and project+priority query paths.
  - Added test `TestProjectIssueStore_WorkTrackingSchemaDefaultsAndConstraints` to verify defaults and constraint enforcement.
- Remaining (Spec001): #258, #259, #260, #261, #262, #263.

## [2026-02-07 15:15:52 MST] Created Spec001 GitHub issue stack and completed #258
- Created ordered Spec001 issues:
  - #257 `[Spec001][Phase1] Add project_issues work-tracking schema migration`
  - #258 `[Spec001][Phase1] Update issue store model for owner/status/priority/due fields`
  - #259 `[Spec001][Phase1] Add POST /api/projects/{id}/issues standalone issue creation endpoint`
  - #260 `[Spec001][Phase1] Add PATCH /api/issues/{id} work-tracking update + transition endpoint`
  - #261 `[Spec001][Phase1] Extend issue list/detail APIs with owner/status/priority filters`
  - #262 `[Spec001][Phase2] CLI issue commands: create/list/view/comment`
  - #263 `[Spec001][Phase2] CLI issue commands: assign/close/reopen and --mine`
- Completed #258 implementation:
  - Extended `ProjectIssue` and create/list filter models with owner/work-status/priority/due/next-step fields.
  - Added work-status + priority normalization/validation helpers.
  - Added work-status transition state machine and `TransitionWorkStatus` store method.
  - Updated issue create/upsert/list/get/document queries and scanners to round-trip new columns.
  - Added new store tests:
    - `TestProjectIssueStore_CreateIssuePersistsWorkTrackingFields`
    - `TestProjectIssueStore_ListIssuesFiltersByOwnerStatusAndPriority`
    - `TestProjectIssueStore_TransitionWorkStatusEnforcesStateMachine`
- Validation run: `go test ./internal/store -count=1`.
- Remaining (Spec001): #259, #260, #261, #262, #263.

## [2026-02-07 15:18:24 MST] Completed Spec001 Issue #259 (standalone issue creation API)
- Added `POST /api/projects/{id}/issues` in `internal/api/issues.go` and wired route in `internal/api/router.go`.
- Endpoint now supports standalone issue creation with work-tracking fields:
  - `title`, `body`, `owner_agent_id`, `priority`, `work_status`, `state`, `approval_state`, `due_at`, `next_step`, `next_step_due_at`
- Added RFC3339 parsing/validation for due-date fields.
- Owner assignment also creates/ensures owner participant for compatibility with existing participant-based flows.
- Added handler coverage:
  - `TestIssuesHandlerCreateIssueCreatesStandaloneIssueWithWorkTrackingFields`
  - `TestIssuesHandlerCreateIssueValidatesPayload`
- Added router regression check to confirm `/api/projects/{id}/issues` registration.
- Validation run: `go test ./internal/api -count=1`.
- Remaining (Spec001): #260, #261, #262, #263.

