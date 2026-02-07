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

## [2026-02-07 15:28:04 MST] Completed Spec001 Issues #260 and #261 validation coverage
- Added API tests for `PATCH /api/issues/{id}` work-tracking updates:
  - partial updates for owner/work_status/priority/due fields
  - nullable field clearing (`due_at`, `next_step`, `next_step_due_at`)
  - invalid transition and invalid priority rejection
- Added API tests for issue list filters:
  - combined `owner_agent_id`, `work_status`, and `priority` filters
  - invalid filter value rejection path
- Added router registration assertion for `PATCH /api/issues/{id}`.
- Validation runs:
  - `go test ./internal/api -count=1`
  - `go test ./internal/store -count=1`
  - `web: npm run build:typecheck`
- Remaining (Spec001): #262, #263 (CLI issue command set).
- Global chat update delivered: clear-session is now available for DM/project/issue chats, with session divider markers persisted across chat types.

## [2026-02-07 15:32:25 MST] Completed Spec001 Issues #262 and #263 (CLI issue commands)
- Added `otter issue` command group with subcommands:
  - `create`, `list`, `view`, `comment`, `assign`, `close`, `reopen`
- Added API client support in `internal/ottercli/client.go`:
  - `ListAgents`, `ResolveAgent`
  - `CreateIssue`, `ListIssues`, `GetIssue`, `PatchIssue`, `CommentIssue`
- Added issue reference resolution logic:
  - UUID issue IDs accepted directly
  - numeric refs (`#123`/`123`) resolved within `--project`
- Added `--mine` support on `otter issue list` (uses `OTTER_AGENT_ID` for identity).
- Added tests:
  - `internal/ottercli/client_test.go` (issue APIs + agent resolution)
  - `cmd/otter/main_test.go` (issue ref helper parsing)
- Validation runs:
  - `go test ./internal/ottercli -count=1`
  - `go test ./cmd/otter -count=1`
  - `go test ./internal/api ./internal/store -count=1`
- Remaining: Spec001 implementation complete. Next: create Spec002 issue breakdown and execute in order.


## [2026-02-07 15:39:47 MST] Spec002 issue stack created
- Created ordered GitHub issues with tests:
  - #264 tree API
  - #265 blob API
  - #266 Files tab + browser shell
  - #267 file viewer modes
  - #268 create linked issue from file browser
  - #269 end-to-end Technonymous review loop hardening
- Starting implementation with #264 now.
- Remaining:
  - Implement and push #264 → #269 in order.

## [2026-02-07 15:43:57 MST] Completed Spec002 issue #264 (tree API)
- Wired backend route: .
- Added tree API tests in  for:
  - root tree listing
  - subdirectory immediate children
  - traversal path rejection
  - invalid repo-path conflict handling
  - route registration
- Validation:
  - ok  	github.com/samhotchkiss/otter-camp/internal/api	0.019s
- Remaining:
  - #265 blob API
  - #266 files tab/browser shell
  - #267 rich file viewers
  - #268 create issue from file browser
  - #269 Technonymous review-loop hardening

## [2026-02-07 15:44:15 MST] Progress log correction for #264 entry
- Correcting prior malformed shell-interpolated lines in the previous log entry.
- Completed Spec002 #264 details:
  - Wired backend route: `GET /api/projects/{id}/tree`.
  - Added tests in `internal/api/project_tree_test.go` for root listing, subdirectory listing, traversal rejection, invalid repo-path handling, and route registration.
  - Validation run: `go test ./internal/api -count=1`.
- Remaining:
  - #265 blob API
  - #266 files tab/browser shell
  - #267 rich file viewers
  - #268 create issue from file browser
  - #269 Technonymous review-loop hardening
