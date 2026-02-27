# Task 175: Message redaction endpoint (DELETE .../messages/:id) returns 405 Method Not Allowed

Layer: L1
Effort: S
Depends on: none

## Context

The spec requires: `DELETE /v1/chat-sessions/:id/messages/:msg_id` — redact message.

Calling `DELETE /v1/chat-sessions/{session_id}/messages/{msg_id}` returns HTTP 405 Method Not Allowed, indicating the DELETE method is not registered on this route.

This is different from "not implemented" — the route likely exists (GET works), but DELETE is not registered.

## Required Fix

1. Add `DELETE /v1/chat-sessions/:session_id/messages/:message_id` route handler
2. Handler should set `is_redacted = true` and clear content (replace with redacted placeholder)
3. Return updated message object
4. Only allow redaction by session owner or message author

## Acceptance Criteria

- [ ] `DELETE /v1/chat-sessions/:session_id/messages/:message_id` returns 200 on success
- [ ] Redacted message shows `is_redacted: true` in subsequent GET
- [ ] Content replaced with redaction placeholder
- [ ] Non-owners/non-authors get 403
