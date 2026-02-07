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

## [2026-02-07 15:46:55 MST] Completed Spec002 issue #265 (blob API)
- Added backend route: `GET /api/projects/{id}/blob`.
- Blob API behavior:
  - UTF-8 text files return `encoding: "utf-8"` with raw content.
  - Binary files return `encoding: "base64"`.
  - Directory blob requests are rejected with 400.
- Added tests in `internal/api/project_tree_test.go` for:
  - utf-8 blob response
  - base64 blob response
  - directory rejection
  - unknown path not found
  - blob route registration
- Validation run: `go test ./internal/api -count=1`.
- Remaining:
  - #266 files tab/browser shell
  - #267 rich file viewers
  - #268 create issue from file browser
  - #269 Technonymous review-loop hardening

## [2026-02-07 15:52:36 MST] Completed Spec002 issue #266 (Files tab + browser shell)
- Added `ProjectFileBrowser` (`web/src/components/project/ProjectFileBrowser.tsx`) with:
  - Files/Commit-history toggle inside the Files tab.
  - Tree listing + breadcrumb navigation.
  - File selection that fetches blob data.
  - Loading/error/retry handling for tree and blob requests.
- Updated `ProjectDetailPage` to:
  - Rename tab key from `code` to `files`.
  - Rename label from `Code` to `Files`.
  - Render `ProjectFileBrowser` for the Files tab.
- Added tests:
  - `web/src/components/project/ProjectFileBrowser.test.tsx`
  - `web/src/pages/ProjectDetailPage.test.tsx`
- Validation:
  - `cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx src/pages/ProjectDetailPage.test.tsx --run`
  - `cd web && npm run build:typecheck`
- Remaining:
  - #267 rich file viewers
  - #268 create issue from file browser
  - #269 Technonymous review-loop hardening

## [2026-02-07 15:56:40 MST] Completed Spec002 issue #267 (rich file viewers)
- Upgraded `ProjectFileBrowser` viewer modes by file type:
  - Markdown files: Render/Source toggle using `MarkdownPreview`.
  - Code files: syntax-highlighted preview (`react-syntax-highlighter`).
  - Image files: inline preview from base64 blob payload.
  - Invalid payload/type combinations: safe fallback warning.
- Added/expanded tests in `web/src/components/project/ProjectFileBrowser.test.tsx` for:
  - markdown render/source toggle
  - code syntax preview rendering
  - image preview rendering
  - safe fallback for bad payload mode
- Validation:
  - `cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx src/pages/ProjectDetailPage.test.tsx --run`
  - `cd web && npm run build:typecheck`
- Remaining:
  - #268 create issue from file browser
  - #269 Technonymous review-loop hardening

## [2026-02-07 15:59:37 MST] Completed Spec002 issue #268 (create issue from file browser)
- Added `Create issue for this file` action in `ProjectFileBrowser` for eligible linked-review files (`/posts/*.md`).
- Implementation details:
  - Calls `POST /api/projects/{id}/issues/link` with `document_path`.
  - Navigates to the created issue thread route on success (`/projects/{id}/issues/{issue_id}`).
  - Surfaces actionable inline errors on API failure.
- Added tests in `web/src/components/project/ProjectFileBrowser.test.tsx` for:
  - successful linked issue creation + navigation
  - API failure error handling
- Validation:
  - `cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx src/pages/ProjectDetailPage.test.tsx --run`
  - `cd web && npm run build:typecheck`
- Remaining:
  - #269 Technonymous review-loop hardening

## [2026-02-07 16:06:31 MST] Completed Spec002 issue #269 (review-loop hardening)
- Expanded backend review-loop verification:
  - Added reviewer notification row assertion after address flow (`project_issue_review_notifications` with `addressed_for_reviewer`).
  - Added websocket regression test for `IssueReviewAddressed` event payload broadcast to issue subscribers.
- Expanded frontend realtime regression coverage:
  - Added realtime test ensuring review history + review changes refresh when `IssueReviewAddressed` arrives for the active issue.
  - Added negative-path assertion that events for other issue IDs do not trigger refresh.
- Updated `IssueThreadPanel` websocket handling to react to review lifecycle events:
  - Parses issue IDs from review events.
  - Triggers a targeted refresh of issue/review state when the active issue receives `IssueReviewSaved` or `IssueReviewAddressed`.
- Validation:
  - `go test ./internal/api -run 'TestIssuesHandler(SaveReview|AddressReview|ReviewChanges|ReviewHistory|TechnonymousMode)|TestResolveReviewDiffBaseSHAFallback|TestBuildReviewVersionAddressedSummaryDeterministic'`
  - `cd web && npm test -- src/components/project/IssueThreadPanel.test.tsx src/components/project/IssueThreadPanel.realtime.test.tsx --run`
  - `cd web && npm run build:typecheck`
- Remaining:
  - Spec002 implementation complete.
  - Next queued specs in folder: `102-connections-diagnostics-page.md`, `103-agent-management.md`.

## [2026-02-07 16:12:10 MST] Spec102 decomposition complete; beginning implementation
- Broke `102-connections-diagnostics-page.md` into ordered, test-scoped GitHub issues:
  - #270 `[Spec102][Phase1]` Bridge diagnostics sync + admin connections summary API
  - #271 `[Spec102][Phase1]` Connections page UI + route + nav
  - #272 `[Spec102][Phase2]` Connection event log store + API
  - #273 `[Spec102][Phase2]` Admin gateway + agent remediation actions
  - #274 `[Spec102][Phase3]` Diagnostics runner + gateway log viewer
  - #275 `[Spec102][Phase3]` Cron + process controls in Connections
- Build order selected to maximize immediate value while keeping issues small:
  1) backend summary API (#270)
  2) connections page shell (#271)
  3) event log + remediation flows (#272/#273)
  4) diagnostics, logs, cron/process controls (#274/#275)
- Next in-progress: #270 (backend diagnostics ingestion + `/api/admin/connections`).

## [2026-02-07 16:17:02 MST] Completed Spec102 issue #270 (diagnostics sync + admin connections API)
- Backend sync payload now accepts optional diagnostics snapshots:
  - `host` (`OpenClawHostDiagnostics`)
  - `bridge` (`OpenClawBridgeDiagnostics`)
- Sync handler persistence updates:
  - Stores diagnostics JSON in `sync_metadata` (`openclaw_host_diagnostics`, `openclaw_bridge_diagnostics`)
  - Maintains in-memory fallback snapshots when DB is unavailable
- Added new admin endpoint:
  - `GET /api/admin/connections`
  - Returns bridge websocket status, last sync health, host diagnostics, bridge diagnostics, and session summary (including basic stall detection)
- Router wired for endpoint registration:
  - `/api/admin/connections`
- Added tests:
  - `TestOpenClawSyncHandlePersistsDiagnosticsMetadata`
  - `TestAdminConnectionsGetReturnsDiagnosticsAndSessionSummary`
  - `TestAdminConnectionsGetHandlesMissingDiagnosticsMetadata`
  - Router registration assertion for `/api/admin/connections`
- Validation:
  - `go test ./internal/api -run 'TestOpenClawSyncHandlePersistsDiagnosticsMetadata|TestAdminConnectionsGetReturnsDiagnosticsAndSessionSummary|TestAdminConnectionsGetHandlesMissingDiagnosticsMetadata|TestProjectsAndInboxRoutesAreRegistered|TestRequireOpenClawSyncAuth|TestOpenClawDispatchQueuePullAndAck'`
  - `go test ./internal/api`
- Remaining:
  - #271 Connections page UI + route + nav
  - #272 Connection event log store + API
  - #273 Admin gateway + agent remediation actions
  - #274 Diagnostics runner + gateway log viewer
  - #275 Cron + process controls

## [2026-02-07 16:21:51 MST] Completed Spec102 issue #271 (Connections UI + route + nav)
- Added new `ConnectionsPage` with:
  - Bridge health card (connected/disconnected, last sync, reconnect count)
  - Host diagnostics card (hostname, OS/arch, gateway + node metadata)
  - GitHub sync summary card (stuck jobs + dead letter count)
  - Session summary + agent sessions table with stalled indicator
  - Error + retry state handling
- Wired routing and navigation:
  - Added lazy route `/connections` in `web/src/router.tsx`
  - Added "Connections" item in top nav/mobile nav in `DashboardLayout`
- Added frontend tests:
  - `ConnectionsPage.test.tsx` (load/render + retry on error)
  - `router.test.tsx` (route registration)
  - `DashboardLayout.test.tsx` (nav visibility)
- Validation:
  - `cd web && npm test -- src/pages/ConnectionsPage.test.tsx src/router.test.tsx src/layouts/DashboardLayout.test.tsx --run`
  - `cd web && npm run build:typecheck`
- Remaining:
  - #272 Connection event log store + API
  - #273 Admin gateway + agent remediation actions
  - #274 Diagnostics runner + gateway log viewer
  - #275 Cron + process controls

## [2026-02-07 16:26:45 MST] Completed Spec102 issue #272 (connection events store + API)
- Added migration pair:
  - `039_create_connection_events.up.sql`
  - `039_create_connection_events.down.sql`
- Added workspace-scoped store:
  - `internal/store/connection_event_store.go`
  - supports create/list + explicit workspace create helper for service-level emitters
- Added store tests:
  - `TestConnectionEventStoreCreateAndList`
  - `TestConnectionEventStoreWorkspaceIsolation`
- Added schema migration verification:
  - `TestSchemaConnectionEventsTableCreateAndRollback`
- Added admin events API:
  - `GET /api/admin/events` via `AdminConnectionsHandler.GetEvents`
  - router registration included
- Added API test:
  - `TestAdminConnectionsGetEventsReturnsWorkspaceScopedRows`
- Validation:
  - `go test ./internal/store -run 'TestConnectionEventStore|TestSchemaConnectionEventsTableCreateAndRollback'`
  - `go test ./internal/api -run 'TestAdminConnectionsGetEventsReturnsWorkspaceScopedRows|TestAdminConnectionsGetReturnsDiagnosticsAndSessionSummary|TestAdminConnectionsGetHandlesMissingDiagnosticsMetadata|TestProjectsAndInboxRoutesAreRegistered'`
  - `go test ./...` (fails in pre-existing `internal/ws` tests unrelated to this change: missing symbols in `handler_test.go`)
- Remaining:
  - #273 Admin gateway + agent remediation actions
  - #274 Diagnostics runner + gateway log viewer
  - #275 Cron + process controls
