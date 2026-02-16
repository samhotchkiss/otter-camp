# Issue #10: Files Tab Shows "ref or path not found" Error

## Problem

The Files tab on any project page shows:

```
Root
ref or path not found
[Retry]
```

The right panel shows "Select a file from the left panel to preview it." but the left panel (file tree) can't load.

The "Commit history" toggle next to "Files" also likely fails since it depends on the same git integration.

## Root Cause Investigation

The Files tab was built as part of Issue #2 (Files Tab and Review Flow). The file tree component attempts to fetch the git tree from the API, but the request fails. Possible causes:

1. **No git repo configured for the project** — The project may not have a `repo_url` or git remote set up. The API can't browse files without a repo.
2. **API endpoint returns error** — The `/api/projects/{id}/files/tree` or similar endpoint may fail (missing git repo path, invalid ref, or the local clone doesn't exist).
3. **Default branch not set** — The file browser may default to `main` or `HEAD` but the project's repo uses a different default branch.
4. **Git bare repo not initialized** — Otter Camp may need a bare repo on the server side that doesn't exist yet.

## Acceptance Criteria

- [ ] Files tab shows the file tree for projects that have a git repo configured
- [ ] For projects without a git repo, show a clear empty state: "No repository configured for this project" (not a raw error)
- [ ] Clicking a file in the tree shows the file content in the right panel
- [ ] Commit history tab shows recent commits
- [ ] Retry button actually retries the file tree fetch

## Files to Investigate

- `web/src/components/projects/ProjectFiles.tsx` — File tree component
- `web/src/components/projects/FileTree.tsx` or similar — Tree fetching logic
- `internal/api/files.go` or `internal/api/project_files.go` — File/tree API endpoints
- `internal/git/` — Git integration layer
- Project model/store — Check if `repo_url` or `repo_path` is populated

## Test Plan

```bash
# Backend
go test ./internal/api -run TestProjectFilesTree -count=1
go test ./internal/api -run TestProjectFilesTreeNoRepo -count=1
go test ./internal/api -run TestProjectFilesBlob -count=1

# Frontend
cd web && npm test -- --grep "FileTree"
cd web && npm test -- --grep "ProjectFiles"
```

## Execution Log
- [2026-02-08 15:12 MST] Issue spec #010 | Commit n/a | in-progress | Moved spec from 01-ready to 02-in-progress; next step is full micro-issue planning with explicit tests before coding | Tests: n/a
- [2026-02-08 15:14 MST] Issue #386,#387,#388 | Commit n/a | planned | Created full Spec010 micro-issue set with explicit tests/dependencies before coding | Tests: n/a
- [2026-02-08 15:20 MST] Issue #386 | Commit 8bb5b22 | closed | Added tree ref fallback + empty-repo 200 response path with backend regression coverage | Tests: go test ./internal/api -run 'TestProjectTreeHandler(FallsBackToHeadWhenDefaultBranchMissing|FallsBackToExistingBranchWhenHeadInvalid|ReturnsEmptyEntriesForEmptyRepository)' -count=1
- [2026-02-08 15:20 MST] Issue #387 | Commit d7224de | closed | Normalized root ref/path tree errors into friendly empty-state UX while preserving retry behavior | Tests: cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx --run
- [2026-02-08 15:20 MST] Issue #388 | Commit fdf8b01 | closed | Added commit-history toggle regression coverage for tree-error states | Tests: cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx --run
- [2026-02-08 15:21 MST] Spec #010 | Commit fdf8b01 | moved-to-needs-review | Closed planned micro-issues #386-#388; branch pushed and PR opened at https://github.com/samhotchkiss/otter-camp/pull/389 | Tests: go test ./internal/api -run 'TestProjectTreeHandler(FallsBackToHeadWhenDefaultBranchMissing|FallsBackToExistingBranchWhenHeadInvalid|ReturnsEmptyEntriesForEmptyRepository|ReturnsNoRepoConfiguredWhenBindingMissing)' -count=1; cd web && npm test -- src/components/project/ProjectFileBrowser.test.tsx --run
- [2026-02-08 20:00 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Reconciled local queue state: spec already implemented/reconciled in execution log; moved from `01-ready` to `03-needs-review` for review tracking | Tests: n/a
