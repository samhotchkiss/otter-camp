# Issue #4: File Uploads in Chat

## Summary

Enable file uploads (images, documents, any file under 10MB) in the global chat. Users and agents should both be able to send files. Images render inline with previews; other files render as downloadable attachment cards.

## Current State

**Attachment infrastructure exists** but is wired only to issue comments:
- `internal/api/attachments.go` — full upload handler (`POST /api/messages/attachments`), MIME detection, local disk storage, thumbnail URLs, 10MB limit
- `attachments` table in DB with `comment_id` FK, filename, size, mime type, storage key, URL
- `LinkAttachmentToComment()` + `UpdateCommentAttachments()` for the issue comment flow

**Chat messages are text-only:**
- `project_chat_messages` table has `author` + `body` (text) — no attachments column
- `createProjectChatMessageRequest` is `{ author, body, sender_type }`
- `GlobalChatSurface.tsx` renders messages as plain text
- `GlobalChatDock.tsx` input is a text-only textarea

## Changes

### 1. Schema: Add Attachments to Chat Messages

**Migration:** `039_add_chat_message_attachments.up.sql`

```sql
ALTER TABLE project_chat_messages
  ADD COLUMN attachments JSONB DEFAULT '[]'::jsonb;
```

The JSONB array stores `AttachmentMetadata` objects (same shape as issue comment attachments):
```json
[{
  "id": "uuid",
  "filename": "screenshot.png",
  "size_bytes": 245000,
  "mime_type": "image/png",
  "url": "/uploads/org-id/abc123.png",
  "thumbnail_url": "/uploads/org-id/abc123.png?thumb=1"
}]
```

### 2. Backend: Wire Attachments to Chat Messages

**File:** `internal/api/attachments.go`

- Add `LinkAttachmentToChatMessage(db, attachmentID, chatMessageID)` — same pattern as `LinkAttachmentToComment`.
- Add `chat_message_id` nullable FK column to `attachments` table (migration), so attachments can belong to either a comment or a chat message.

**File:** `internal/api/project_chat.go`

- Extend `createProjectChatMessageRequest` to accept optional `attachment_ids []string`.
- After creating the chat message, link any provided attachment IDs and populate the message's `attachments` JSONB.
- Extend chat message list/get responses to include `attachments` array.
- Scan `attachments` JSONB when reading messages.

**File:** `internal/store/project_chat_store.go`

- Update insert/scan to handle the new `attachments` column.

### 3. Frontend: Upload UI in Chat Input

**File:** `web/src/components/chat/GlobalChatSurface.tsx`

Add a file upload button (📎 or + icon) next to the message input:

- Click → opens native file picker (accept all types, max 10MB)
- Drag-and-drop onto the chat area also triggers upload
- Paste image from clipboard triggers upload
- While uploading, show a progress indicator / thumbnail preview above the input
- Multiple files can be queued before sending
- Uploaded files attach to the next message sent

**Upload flow:**
1. User selects file → `POST /api/messages/attachments` (multipart, existing endpoint)
2. Response returns `AttachmentMetadata` with URL
3. Attachment ID stored in component state
4. User sends message → `attachment_ids` included in the create request
5. If user sends with ONLY attachments (no text), that's fine — `body` can be empty string

### 4. Frontend: Render Attachments in Messages

**File:** `web/src/components/chat/GlobalChatSurface.tsx` (or a new `ChatMessage.tsx` component)

For each message, if `attachments` array is non-empty:

**Images** (`image/*` MIME types):
- Render inline as `<img>` with max-width constraint (~400px)
- Click to open full-size in a lightbox/modal or new tab
- Show thumbnail during loading if `thumbnail_url` is available

**Other files** (PDF, docs, zip, etc.):
- Render as a compact attachment card:
  ```
  📄 report.pdf (2.4 MB)  [Download]
  ```
- Click/download link points to the attachment URL

### 5. Agent-Sent Attachments

Agents send messages via the OpenClaw bridge → chat API. The bridge already sends `{ author, body }`. Extend to support:

- Agent includes `attachment_ids` in the message payload (agent uploads first, then references)
- OR agent includes `inline_images` as base64/URLs that the backend downloads and stores

**Simpler first pass:** Agents can reference image URLs in markdown (`![alt](url)`) in the message body, and the frontend renders them inline. This works without any backend changes for agent→user image sharing.

**Full pass:** Agents use the upload endpoint, get attachment IDs, include them in chat messages. This requires the bridge to support multipart uploads.

### 6. Serve Upload Files

Ensure the `/uploads/` path is served by the Go server (static file handler). Check if this is already wired in the router:

```go
r.Handle("/uploads/*", http.StripPrefix("/uploads", http.FileServer(http.Dir("uploads"))))
```

On Railway (production), uploads directory needs to be a persistent volume or switch to S3/R2 for durability.

## Testing

- [ ] Upload an image in chat → appears inline in the message
- [ ] Upload a PDF/doc → appears as downloadable card
- [ ] Drag-and-drop file into chat → uploads and attaches
- [ ] Paste screenshot from clipboard → uploads and attaches
- [ ] File over 10MB → rejected with clear error
- [ ] Multiple files in one message
- [ ] Message with only attachments (no text)
- [ ] Message with both text and attachments
- [ ] Attachments persist across page reload (stored in DB)
- [ ] Agent sends image URL in markdown → renders inline
- [ ] Agent sends attachment via API → appears in chat
- [ ] Attachment thumbnails render for images
- [ ] Non-image files show filename + size + download link

## Files to Modify

- `migrations/039_add_chat_message_attachments.up.sql` — new migration
- `internal/api/attachments.go` — `LinkAttachmentToChatMessage`, `chat_message_id` support
- `internal/api/project_chat.go` — accept `attachment_ids`, include attachments in responses
- `internal/store/project_chat_store.go` — scan attachments JSONB
- `web/src/components/chat/GlobalChatSurface.tsx` — upload button, drag-drop, paste, render attachments
- `internal/api/router.go` — ensure `/uploads/` static serving is wired
