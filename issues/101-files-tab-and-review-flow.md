# Issue #101: Files Tab + Technonymous Review Flow

> ⚠️ **NOT READY FOR WORK** — This issue is still being specced. Do not begin implementation until this banner is removed.


## Problem Statement

Two issues with the current project detail view:

1. **The "Files" tab is empty** (currently labeled "Code" — rename to "Files") — When viewing a project with committed files (e.g., Technonymous), the tab shows nothing. It currently renders `<ProjectCommitBrowser>` which is a commit history viewer, not a file/tree browser.

2. **The Technonymous PR-like review flow is not fully wired** — The use case specs (`use-cases/projects/Technonymous/`) describe a workflow where each blog post is treated like a PR: create issue → link document → inline review → approve. Parts of this exist in the codebase but the end-to-end flow may not be connected.

---

## Part 1: Files Tab — File Browser

### Current State

The tab is currently labeled "Code" — **rename to "Files"**. It uses `activeTab === "code"` and renders `<ProjectCommitBrowser projectId={project.id} />` (in `web/src/pages/ProjectDetailPage.tsx`). Rename the tab key to `"files"` and label to `"Files"` in the `tabs` array.

`ProjectCommitBrowser` (`web/src/components/project/ProjectCommitBrowser.tsx`) fetches from:
- `GET /api/projects/{id}/commits` — returns commit list
- `GET /api/projects/{id}/commits/{sha}/diff` — returns diff for a commit

This is a **commit history browser**, not a **file tree browser**. There is no API endpoint to browse the repository's current file tree or view file contents.

### What Needs to Be Built

#### Backend: File Tree + Blob APIs

The project's git repo is accessible server-side (projects have `repo_url` and `local_repo_path` fields). We need endpoints to browse repo contents:

```
GET /api/projects/{id}/tree?ref=main&path=/
```
Returns directory listing:
```json
{
  "ref": "main",
  "path": "/",
  "entries": [
    { "name": "posts", "type": "dir", "path": "posts/" },
    { "name": "notes", "type": "dir", "path": "notes/" },
    { "name": "README.md", "type": "file", "path": "README.md", "size": 1234 }
  ]
}
```

```
GET /api/projects/{id}/blob?ref=main&path=posts/2026-01-15-digital-minimalism.md
```
Returns file content:
```json
{
  "ref": "main",
  "path": "posts/2026-01-15-digital-minimalism.md",
  "content": "# Digital Minimalism\n\nContent here...",
  "size": 4567,
  "encoding": "utf-8"
}
```

For binary files (images), return base64:
```json
{
  "ref": "main",
  "path": "assets/cover.png",
  "content": "<base64>",
  "size": 45678,
  "encoding": "base64"
}
```

**Implementation notes:**
- Projects store `local_repo_path` — use `go-git` or shell out to `git` to read the tree
- If `local_repo_path` is empty, fall back to the OtterCamp git hosting path (`/git/{org-id}/{project-id}.git`)
- Ref defaults to the project's default branch (usually `main`)

#### Files to create/modify (Backend):

- **New: `internal/api/project_tree.go`** — `TreeHandler` with `GetTree` and `GetBlob` methods
- **Modify: `internal/api/router.go`** — Register routes:
  ```
  r.Get("/projects/{id}/tree", treeHandler.GetTree)
  r.Get("/projects/{id}/blob", treeHandler.GetBlob)
  ```

#### Frontend: File Tree Component

Replace or augment the Code tab to show a file tree browser with file content viewer.

**UI requirements:**
- Left panel: directory tree (expandable folders)
- Right panel: file content viewer
  - Markdown files (`.md`): rendered view with source toggle (per `use-cases/projects/Technonymous/07-editor-detection.md`)
  - Code files (`.go`, `.ts`, `.js`, `.py`): syntax highlighted
  - Images (`.png`, `.jpg`, `.gif`): preview
- Breadcrumb navigation showing current path
- The commit history browser should still be accessible (maybe as a sub-tab or secondary view within Code)

#### Files to create/modify (Frontend):

- **New: `web/src/components/project/ProjectFileBrowser.tsx`** — File tree + content viewer
- **Modify: `web/src/pages/ProjectDetailPage.tsx`** — Rename "Code" tab to "Files" (key `"files"`, label `"Files"`), render `ProjectFileBrowser` (optionally keep `ProjectCommitBrowser` as a sub-view)

---

## Part 2: Technonymous Review Flow (Issue-as-PR)

### Spec Summary (from `use-cases/projects/Technonymous/`)

The intended workflow:

1. Agent drafts a post in `/posts/` and commits it
2. An **issue** is created for the post, linked to the document file
3. The post evolves through commits (each commit = meaningful change)
4. Sam opens the issue, sees the **document workspace** (rendered Markdown)
5. Sam leaves **inline comments** using CriticMarkup `{>> comment <<}`
6. Saving a review creates a **review checkpoint commit**
7. Agent is notified, processes comments, commits a new version
8. Sam can view **"changes since last review"** diff
9. When satisfied, Sam clicks **Approve** → confetti 🎉
10. Issue moves to approved/closed state

### What Already Exists

Based on the codebase, significant pieces are already built:

| Component | Status | Location |
|-----------|--------|----------|
| Issue CRUD + participants + comments | ✅ Built | `internal/api/issues.go`, `internal/store/project_issue_store.go` |
| Approval state machine (draft → ready_for_review → approved) | ✅ Built | `internal/store/project_issue_store.go` (lines 191-227) |
| Review checkpoint storage | ✅ Built | `internal/store/project_issue_store.go` (review versions) |
| Review save + address endpoints | ✅ Built | `internal/api/issue_review_save.go`, `internal/api/issue_review_address.go` |
| Review diff/history APIs | ✅ Built | `GET /issues/{id}/review/changes`, `GET /issues/{id}/review/history` |
| Linked issue creation | ✅ Built | `POST /projects/{id}/issues/link` |
| Confetti on approval | ✅ Built | Referenced in commit `981b927` |
| CriticMarkup parser | ✅ Built | `web/src/components/content-review/criticMarkup.ts` |
| Document workspace UI | ✅ Built | `web/src/components/content-review/DocumentWorkspace.tsx` |
| Content review UI | ✅ Built | `web/src/components/content-review/ContentReview.tsx` |
| Review state machine | ✅ Built | `web/src/components/content-review/reviewStateMachine.ts` |
| Markdown preview with toggle | ✅ Built | `web/src/components/content-review/MarkdownPreview.tsx` (referenced in `05-markdown-viewer-editor.md`) |
| Issue thread panel | ✅ Built | `web/src/components/project/IssueThreadPanel.tsx` |
| Project issues list | ✅ Built | `web/src/components/project/ProjectIssuesList.tsx` |
| Project chat | ✅ Built | `web/src/components/project/ProjectChatPanel.tsx` |
| Review notification system | ✅ Built | `internal/api/issue_review_notification.go` |

### What May Be Missing or Broken

The individual pieces exist but the end-to-end flow may not be connected. Investigate:

1. **Can an agent create an issue linked to a document file via CLI/API?**
   - `POST /projects/{id}/issues/link` exists but takes `document_path` — is this wired for the Technonymous workflow?
   - There's no `POST /projects/{id}/issues` for standalone creation (see Issue Work Tracking Spec)

2. **Does the issue detail view show the document workspace?**
   - `DocumentWorkspace.tsx` and `ContentReview.tsx` exist — are they rendered in the issue detail view when `document_path` is set?
   - Check `IssueThreadPanel.tsx` to see if it loads the document workspace for linked issues

3. **Does saving a review actually create a commit?**
   - Per `08-review-loop.md`: "When a review is saved, a new commit is created"
   - Check if `SaveReview` handler in `issue_review_save.go` creates a git commit or just stores a checkpoint

4. **Is the agent notified when a review is saved?**
   - `issue_review_notification.go` exists — does it dispatch to OpenClaw to notify the agent?
   - Check notification types: `review_saved_for_owner`, `review_addressed_for_reviewer`

5. **Does "changes since last review" diff work?**
   - `GET /issues/{id}/review/changes` exists — does it compare current state to last review checkpoint?

6. **Is the file tree needed for the review flow?**
   - The issue links to a specific `document_path` — the review UI may load that file directly without needing a tree browser
   - But Sam needs to be able to browse files to find posts and create issues for them

### Recommended Investigation

Run the Technonymous project through the full flow on sam.otter.camp:
1. Check if the Technonymous project has any issues in the Issues tab
2. Try creating a linked issue for an existing post
3. Open the issue and verify the document workspace loads
4. Try the review flow (add CriticMarkup comments, save review)
5. Check if the agent notification fires

---

## Part 3: Priority Order

1. **File tree API + UI** (Files tab) — This is the most visible gap. Users expect to browse files.
2. **Verify issue-as-PR flow** — Test end-to-end, fix any broken connections
3. **Issue creation from file browser** — "Create issue for this file" action in the Code tab
4. **Agent notification wiring** — Ensure review saves trigger agent notifications via OpenClaw

---

## Key Files Reference

### Backend
- `internal/api/router.go` — Route registration (production version is ~340 lines with DB, not the 86-line demo)
- `internal/api/issues.go` — Issue handlers
- `internal/api/issue_review_save.go` — Review save handler
- `internal/api/issue_review_address.go` — Review address handler
- `internal/api/issue_review_notification.go` — Notification dispatch
- `internal/api/project_commits.go` — Commit list/detail/diff handlers
- `internal/api/project_commit_create.go` — Commit creation
- `internal/store/project_issue_store.go` — Issue data model + store (ProjectIssue struct, approval state machine, review versions, participants)
- `internal/store/project_commit_store.go` — Commit store

### Frontend
- `web/src/pages/ProjectDetailPage.tsx` — Project detail with tabs (Board, List, Activity, Chat, Code, Issues, Settings)
- `web/src/components/project/ProjectCommitBrowser.tsx` — Current "Code" tab (commit history only)
- `web/src/components/project/ProjectIssuesList.tsx` — Issues list
- `web/src/components/project/IssueThreadPanel.tsx` — Issue detail/thread
- `web/src/components/project/ProjectChatPanel.tsx` — Project chat
- `web/src/components/content-review/DocumentWorkspace.tsx` — Document editing/review
- `web/src/components/content-review/ContentReview.tsx` — Content review container
- `web/src/components/content-review/criticMarkup.ts` — CriticMarkup parser
- `web/src/components/content-review/MarkdownPreview.tsx` — Markdown render/source toggle
- `web/src/components/content-review/reviewStateMachine.ts` — Review state transitions
- `web/src/components/review/CodeReview.tsx` — Code review (for non-markdown files)
- `web/src/components/review/ReviewFileTree.tsx` — Review file tree

### Use Case Specs
- `use-cases/projects/Technonymous/00-summary.md` through `09-media-and-metadata.md` — Full workflow spec
