# 125 — Chat Persistence & Archiving

## Problem

On a fresh login, the chat window loads empty. Users lose all previous chat history and have to start new conversations. There's no way to view past chats or manage them.

## Requirements

### 1. Persistent Chat List
- On login, load ALL previous chats the user has had
- Display in sidebar/list sorted by **most recent activity** (newest at top)
- Each chat entry shows: agent name/avatar, last message preview, timestamp
- Chats persist across sessions (stored in DB, not localStorage)

### 2. Chat Archiving
- Users can **archive** a chat to remove it from the active list
- Archived chats are hidden from the default view but NOT deleted
- Add an "Archive" action (swipe, context menu, or button per chat)

### 3. Archived Chats View
- Add a route/page to view archived chats (e.g., `/chats/archived`)
- Archived chats can be **unarchived** to bring them back to the active list
- Archived chats are still searchable

### 4. Auto-Archiving Rules
- When an **issue is closed**, any chat tied to that issue is automatically archived
- When a **project is archived**, any chats tied to that project are automatically archived
- Auto-archived chats can still be manually unarchived

## Database Schema

```sql
-- Chat threads table (if not already existing)
CREATE TABLE IF NOT EXISTS chat_threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_id UUID REFERENCES agents(id) ON DELETE SET NULL,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    issue_id UUID REFERENCES project_issues(id) ON DELETE SET NULL,
    title TEXT,
    archived_at TIMESTAMPTZ,
    auto_archived_reason TEXT, -- 'issue_closed', 'project_archived', NULL for manual
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_chat_threads_user_active ON chat_threads(user_id, org_id) WHERE archived_at IS NULL;
CREATE INDEX idx_chat_threads_user_archived ON chat_threads(user_id, org_id) WHERE archived_at IS NOT NULL;
CREATE INDEX idx_chat_threads_issue ON chat_threads(issue_id) WHERE issue_id IS NOT NULL;
CREATE INDEX idx_chat_threads_project ON chat_threads(project_id) WHERE project_id IS NOT NULL;
```

## API Endpoints

```
GET    /api/chats                    — List active chats (sorted by last_message_at DESC)
GET    /api/chats?archived=true      — List archived chats
GET    /api/chats/:id                — Get chat with messages
POST   /api/chats/:id/archive        — Archive a chat
POST   /api/chats/:id/unarchive      — Unarchive a chat
```

## Frontend Changes

### Chat Sidebar
- On mount, fetch `/api/chats` and populate sidebar
- Store active chat ID in URL (e.g., `/chats/:id`) so refresh preserves selection
- "Archive" button/action on each chat item

### Archived View
- Route: `/chats/archived`
- Same layout as active chats but with "Unarchive" action
- Link/button to navigate between active and archived views

### Auto-Archive Triggers (Backend)
- In the issue close handler: find chat_threads with matching `issue_id`, set `archived_at = NOW()`, `auto_archived_reason = 'issue_closed'`
- In the project archive handler: find chat_threads with matching `project_id`, set `archived_at = NOW()`, `auto_archived_reason = 'project_archived'`

## Implementation Notes

- If chat_threads table doesn't exist yet, create it via migration
- If chats currently use localStorage or ephemeral state, migrate to DB-backed storage
- Ensure RLS policy on chat_threads for org isolation
- `last_message_at` should update whenever a new message is added to the thread
- Consider WebSocket push to update chat list in real-time when new messages arrive

## Testing

- Create multiple chats, log out, log back in → all chats visible
- Archive a chat → disappears from active list, appears in archived view
- Close an issue tied to a chat → chat auto-archives
- Archive a project tied to chats → those chats auto-archive
- Unarchive a chat → returns to active list

## Execution Log
- [2026-02-10 22:48 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 125 from 01-ready to 02-in-progress for implementation kickoff | Tests: n/a
- [2026-02-10 22:49 MST] Issue #730 | Commit n/a | created | Added DB migration/store foundation work unit for chat_threads | Tests: go test ./internal/store -run TestChatThreadStore -count=1; go test ./internal/store -run TestSchema.*ChatThreads -count=1
- [2026-02-10 22:49 MST] Issue #731 | Commit n/a | created | Added handler touch wiring work unit for DM/project/issue message writes | Tests: go test ./internal/api -run TestMessageHandler.*ChatThread -count=1; go test ./internal/api -run TestProjectChatHandler.*ChatThread -count=1; go test ./internal/api -run TestIssuesHandler.*ChatThread -count=1
- [2026-02-10 22:49 MST] Issue #732 | Commit n/a | created | Added /api/chats endpoints work unit (list/get/archive/unarchive) | Tests: go test ./internal/api -run TestChatsHandler -count=1; go test ./internal/api -run TestRouterRegistersChatsRoutes -count=1
- [2026-02-10 22:49 MST] Issue #733 | Commit n/a | created | Added auto-archive trigger work unit for issue close/project archive transitions | Tests: go test ./internal/api -run TestIssuesHandlerPatchIssueAutoArchivesChats -count=1; go test ./internal/api -run TestProjectsHandlerPatchAutoArchivesChats -count=1
- [2026-02-10 22:49 MST] Issue #734 | Commit n/a | created | Added frontend active chat migration work unit (server list + URL sync + archive action) | Tests: cd web && npm test -- src/contexts/GlobalChatContext.test.tsx --run; cd web && npm test -- src/components/chat/GlobalChatDock.test.tsx --run
- [2026-02-10 22:49 MST] Issue #735 | Commit n/a | created | Added archived chats route/page work unit with search and unarchive | Tests: cd web && npm test -- src/pages/ArchivedChatsPage.test.tsx --run; cd web && npm test -- src/router.test.tsx --run
- [2026-02-10 22:54 MST] Issue #730 | Commit 6364318 | closed | Added chat_threads migration + ChatThreadStore with RLS/isolation/archive coverage, committed, pushed, and closed issue | Tests: go test ./internal/store -run TestChatThreadStore -count=1; go test ./internal/store -run TestSchema.*ChatThreads -count=1; go test ./internal/store -run TestRLSEnabled\|TestChatThreadStore\|TestSchemaChatThreadsRLSAndIndexes -count=1
- [2026-02-10 22:59 MST] Issue #731 | Commit e22b306 | closed | Wired DM/project/issue create handlers to touch user chat_threads metadata on authenticated writes; committed, pushed, and closed issue | Tests: go test ./internal/api -run TestCreateMessageDMTouchesChatThreadForAuthenticatedUser\|TestProjectChatHandlerCreateTouchesChatThreadForAuthenticatedUser\|TestIssuesHandlerCommentCreateTouchesChatThreadForAuthenticatedUser -count=1; go test ./internal/api -run TestCreateMessageDMTouchesChatThreadForAuthenticatedUser\|TestProjectChatHandlerCreateTouchesChatThreadForAuthenticatedUser\|TestIssuesHandlerCommentCreateTouchesChatThreadForAuthenticatedUser\|TestCreateMessageDMDispatchesToOpenClaw\|TestProjectChatHandlerCreateAndList\|TestIssuesHandlerCommentCreateValidatesAndPersists -count=1
- [2026-02-10 23:02 MST] Issue #732 | Commit 057c5ed | closed | Added ChatsHandler endpoints (/api/chats list/get/archive/unarchive), route wiring, and API coverage; committed, pushed, and closed issue | Tests: go test ./internal/api -run TestChatsHandler\|TestRouterRegistersChatsRoutes -count=1; go test ./internal/api -run TestChatsHandler\|TestRouterRegistersChatsRoutes\|TestCreateMessageDMTouchesChatThreadForAuthenticatedUser\|TestProjectChatHandlerCreateTouchesChatThreadForAuthenticatedUser\|TestIssuesHandlerCommentCreateTouchesChatThreadForAuthenticatedUser\|TestMessagesRouteIsRegistered -count=1
- [2026-02-10 23:04 MST] Issue #733 | Commit 131f80e | closed | Added auto-archive triggers for issue-close and project-archive transitions with reason metadata and regression coverage; committed, pushed, and closed issue | Tests: go test ./internal/api -run TestIssuesHandlerPatchIssueAutoArchivesChatsOnCloseTransition\|TestProjectsHandlerPatchAutoArchivesChatsOnProjectArchiveTransition -count=1; go test ./internal/api -run TestIssuesHandlerPatchIssueAutoArchivesChatsOnCloseTransition\|TestProjectsHandlerPatchAutoArchivesChatsOnProjectArchiveTransition\|TestIssuesHandlerPatchIssueUpdatesAndClearsWorkTrackingFields\|TestProjectsHandlerPatchUpdatesProjectFields -count=1
- [2026-02-10 23:10 MST] Issue #734 | Commit 54359e8 | closed | Migrated active global chat list to /api/chats with URL-selected chat IDs and archive action wiring; committed, pushed, and closed issue | Tests: cd web && npm test -- src/contexts/GlobalChatContext.test.tsx src/components/chat/GlobalChatDock.test.tsx src/router.test.tsx --run
- [2026-02-10 23:13 MST] Issue #735 | Commit fc5a6f9 | closed | Added Archived Chats page (/chats/archived) with archived fetch, server-backed search, unarchive action, and route wiring; committed, pushed, and closed issue | Tests: cd web && npm test -- src/pages/ArchivedChatsPage.test.tsx src/router.test.tsx --run; cd web && npm test -- src/contexts/GlobalChatContext.test.tsx src/components/chat/GlobalChatDock.test.tsx src/pages/ArchivedChatsPage.test.tsx src/router.test.tsx --run
- [2026-02-10 23:14 MST] Issue #n/a | Commit n/a | moved_to_review | Implementation complete; moved spec 125 from 02-in-progress to 03-needs-review pending external validation | Tests: n/a
- [2026-02-10 23:16 MST] Issue #n/a | Commit n/a | pr_opened | Opened PR #736 for spec 125 implementation branch and updated PR body with full summary + test evidence | Tests: n/a

- [2026-02-10 23:25 MST] Issue #737/#738/#739/#740/#741 | Commit n/a | in_progress | Re-opened spec 125 for reviewer-required P2 fixes, validated micro-issue coverage before coding, moved spec back to 02-in-progress | Tests: n/a
- [2026-02-10 23:39 MST] Issue #737 | Commit 00bbe40 | closed | Stabilized DM thread title resolution to prevent sender-based title oscillation and added regression coverage | Tests: go test ./internal/api -run 'TestDMChatThreadTitleStabilityAcrossMultipleSenders|TestCreateMessageDMTouchesChatThreadForAuthenticatedUser' -count=1
- [2026-02-10 23:39 MST] Issue #738 | Commit 40bc86c | closed | Added cursor-based pagination (limit + cursor) for GET /api/chats with next_cursor response support | Tests: go test ./internal/api -run 'TestChatsHandlerListPaginationReturnsCursorAndNextPage|TestChatsHandlerListArchiveUnarchive' -count=1; go test ./internal/store -run TestChatThreadStore -count=1
- [2026-02-10 23:39 MST] Issue #739 | Commit c61d5f7 | closed | Debounced archived chat search requests in ArchivedChatsPage and added single-request debounce test coverage | Tests: cd web && npm test -- src/pages/ArchivedChatsPage.test.tsx --run
- [2026-02-10 23:39 MST] Issue #740 | Commit 2170163 | closed | Added chat_threads length-limit constraints and store-side preview truncation with schema/store regression tests | Tests: go test ./internal/store -run 'TestChatThreadStoreTruncatesLongPreview|TestMigration061ChatThreadsLengthLimitFilesExistAndContainConstraints|TestSchemaChatThreadsLengthConstraints' -count=1
- [2026-02-10 23:39 MST] Issue #741 | Commit eea1b6e | closed | Expanded auto-archive tests to multi-user coverage and added frontend archived/archive failure-path tests | Tests: go test ./internal/store -run TestChatThreadStore_AutoArchiveByIssueAndProject -count=1; cd web && npm test -- src/pages/ArchivedChatsPage.test.tsx src/components/chat/GlobalChatDock.test.tsx --run
- [2026-02-10 23:39 MST] Issue #n/a | Commit n/a | reviewer_changes_resolved | Removed top-level reviewer-required changes block after completing all mandatory P2 fixes; retained resolution summary in execution log | Tests: n/a
- [2026-02-10 23:56 MST] Issue #n/a | Commit eea1b6e | moved_to_completed | External reviewer approved and merged PR #736 to main; reconciled local queue state and confirmed spec remains only in 05-completed | Tests: n/a
