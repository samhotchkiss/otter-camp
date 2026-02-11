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
