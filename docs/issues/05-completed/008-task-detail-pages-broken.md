# Issue #8: Replace Legacy Task Board with Issues-Based Project View

## Problem

The project detail page still shows a **task-based Board view** (Queued / In Progress / Review / Done columns) with legacy task cards. Tasks have been **deprecated in favor of issues** as the single work tracking primitive (decision from Feb 7).

### Current Broken State

1. **Task cards link to dead pages** — Clicking any task card navigates to a URL that shows "Task not found — The requested task doesn't exist (or isn't available in this demo)." This is because task detail routes/pages were never properly built.
2. **Leftover demo text** — Error message references "this demo" which is development placeholder copy.
3. **Board shows stale task data** — The 8 tasks shown (e.g., "CRITICAL: DATABASE_URL not set", "Auth accepts any token") are old task records that don't reflect current work.
4. **Issues tab shows 0 issues** — The Issues tab exists but shows "No issues found for the selected filters" even though issues have been created via the CLI. Issues and tasks are disconnected systems.

### What Should Happen

The project Board view should be powered by **issues** (not tasks). The Board columns (Queued, In Progress, Review, Done) should map to issue `work_status` values. Clicking an issue card should open the issue detail page.

## Acceptance Criteria

- [ ] Project Board view shows **issues** grouped by `work_status` (queued, in_progress, review, done) instead of legacy tasks
- [ ] Clicking an issue card on the Board opens the issue detail view with full info (title, body, priority, assignee, status, comments)
- [ ] Project List view shows issues (not tasks) with status, assignee, and priority columns
- [ ] Issues tab filters work correctly and show issues created via CLI
- [ ] Remove or hide the legacy task system from the UI (no more "+ Add Task" buttons referencing the old task model)
- [ ] Remove all "demo" references from error messages and UI copy
- [ ] If an issue genuinely doesn't exist, show a proper 404 with "Back to Project" link

## Files to Investigate

- `web/src/components/projects/ProjectBoard.tsx` — Board component, needs to fetch issues instead of tasks
- `web/src/components/projects/ProjectList.tsx` — List component, same change
- `web/src/router.tsx` — Route definitions for issue detail
- `web/src/pages/IssueDetailPage.tsx` — May need to be created
- `internal/api/issues.go` — Issue API endpoints
- `internal/api/tasks.go` — Legacy task API (candidate for removal or deprecation)
- `internal/store/project_issue_store.go` — Issue store with work_status
- Search for "isn't available in this demo" and remove

## Test Plan

```bash
# Backend
go test ./internal/api -run TestGetProjectIssues -count=1
go test ./internal/api -run TestGetIssueDetail -count=1
go test ./internal/api -run TestIssuesByWorkStatus -count=1

# Frontend
cd web && npm test -- --grep "ProjectBoard"
cd web && npm test -- --grep "IssueDetail"
cd web && npm test -- --grep "issue card click"
```

## Execution Log
- [2026-02-08 14:38 MST] Issue spec #008 | Commit n/a | in-progress | Moved spec from `01-ready` to `02-in-progress` and selected branch `codex/spec-008-task-detail-pages-broken` from `origin/main` for isolated implementation | Tests: n/a
- [2026-02-08 14:42 MST] Issue #373 #374 #375 #376 #377 #378 | Commit n/a | created | Created full Spec008 micro-issue set with explicit tests, scope, acceptance criteria, and dependencies before implementation | Tests: n/a
- [2026-02-08 14:45 MST] Issue #373 | Commit 180cb30 | closed | Rewired ProjectDetail board/list data fetch from `/api/tasks` to `/api/issues` with issue-based counters and regression coverage | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 14:47 MST] Issue #374 | Commit 2a8964f | closed | Updated board columns to group issue work_status values, removed legacy "+ Add Task" control, and switched board empty copy to issue-centric wording | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 14:49 MST] Issue #375 | Commit 07d1d69 | closed | Converted List tab to issue-oriented columns (Issue/Assignee/Status/Priority) and replaced list empty copy with "No active issues" | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 14:51 MST] Issue #376 | Commit 53f9af7 | closed | Updated board/list row click navigation to `/projects/:id/issues/:issueId` and aligned route integration tests | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run; cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx --run
- [2026-02-08 14:53 MST] Issue #377 | Commit 6558c78 | closed | Added issue detail metadata (body/priority/work status/assignee) and project-scoped issue-not-found recovery UI with Back to Project link | Tests: cd web && npm test -- src/components/project/IssueThreadPanel.test.tsx --run; cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 14:55 MST] Issue #378 | Commit fb1ca2b | closed | Added mixed-payload filter regression coverage and client-side state/kind/origin filtering fallback in `ProjectIssuesList` for reliable issues-tab visibility | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx --run; cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx --run
- [2026-02-08 14:56 MST] Issue #373 #374 #375 #376 #377 #378 | Commit fb1ca2b | implementation-complete | All planned Spec008 micro-issues implemented, pushed on branch `codex/spec-008-task-detail-pages-broken`, and PR opened for review (`#379`) | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx src/components/project/IssueThreadPanel.test.tsx src/components/project/ProjectIssuesList.test.tsx src/components/project/ProjectIssuesList.integration.test.tsx --run
- [2026-02-08 19:51 MST] Issue spec #008-task-detail | Commit n/a | moved-to-in-progress | Prioritized reviewer-required changes and resumed implementation on branch `codex/spec-008-task-detail-pages-broken` | Tests: n/a
- [2026-02-08 19:52 MST] Issue #410 | Commit n/a | replanned | Updated issue to full micro-spec format with explicit command-level tests before implementation | Tests: n/a
- [2026-02-08 19:52 MST] Issue #411 | Commit n/a | replanned | Updated issue to full micro-spec format with explicit command-level tests before implementation | Tests: n/a
- [2026-02-08 19:52 MST] Issue #412 | Commit n/a | replanned | Updated issue to full micro-spec format with explicit command-level tests before implementation | Tests: n/a
- [2026-02-08 19:52 MST] Issue #413 | Commit n/a | replanned | Updated issue to full micro-spec format with explicit command-level tests before implementation | Tests: n/a
- [2026-02-08 19:52 MST] Issue #410 | Commit n/a | closed | Verified fix already landed on branch (dual fetch mocks present) and confirmed integration regression test passes | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx --run; cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx --run
- [2026-02-08 19:54 MST] Issue #411 | Commit 4d73e31 | closed | Replaced issue-thread not-found back link anchor with React Router Link and updated router-context test coverage | Tests: cd web && npm test -- src/components/project/IssueThreadPanel.test.tsx --run; cd web && npm test -- src/components/project/IssueThreadPanel.realtime.test.tsx --run
- [2026-02-08 19:55 MST] Issue #412 | Commit 55ef857 | closed | Removed redundant client-side issue filtering and aligned tests to server-query filtering contract | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx --run; cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx --run
- [2026-02-08 19:57 MST] Issue #413 | Commit acd2c73 | closed | Moved agent name mapping to component-scoped ref with per-fetch reset and added cross-project leakage regression test | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run; cd web && npm test -- src/components/project/ProjectIssuesList.test.tsx --run
- [2026-02-08 19:57 MST] Issue #420 | Commit n/a | opened | Split final reviewer-required dead-code item into explicit micro-issue (remove legacy `ApiTask`/`payload.tasks` fallback) with command-level tests | Tests: n/a
- [2026-02-08 19:59 MST] Issue #420 | Commit 1c5f161 | closed | Removed legacy `ApiTask`/`payload.tasks` fallback from project issue loader and updated fixtures to issue-shaped responses | Tests: cd web && npm test -- src/pages/ProjectDetailPage.test.tsx --run; cd web && npm test -- src/components/project/IssueThreadPanel.test.tsx --run
- [2026-02-08 19:59 MST] Issue spec #008-task-detail | Commit 1c5f161 | reviewer-required-resolved | Closed reviewer fix set (#410, #411, #412, #413, #420) and removed top-level `Reviewer Required Changes` block | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx src/components/project/ProjectIssuesList.test.tsx src/components/project/IssueThreadPanel.test.tsx src/components/project/IssueThreadPanel.realtime.test.tsx src/pages/ProjectDetailPage.test.tsx --run
- [2026-02-08 19:59 MST] Issue spec #008-task-detail | Commit 1c5f161 | moved-to-needs-review | Reviewer-required fixes complete; spec moved from `02-in-progress` to `03-needs-review` pending external sign-off (PR #379) | Tests: cd web && npm test -- src/components/project/ProjectIssuesList.integration.test.tsx src/components/project/ProjectIssuesList.test.tsx src/components/project/IssueThreadPanel.test.tsx src/components/project/IssueThreadPanel.realtime.test.tsx src/pages/ProjectDetailPage.test.tsx --run
