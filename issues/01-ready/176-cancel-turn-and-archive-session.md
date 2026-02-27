# Task 176: Cancel turn endpoint returns 404; session status (archive) not updatable via PATCH

Layer: L1
Effort: S
Depends on: none

## Context

Two related issues with the chat session API:

### 1. Cancel turn endpoint — 404
`POST /v1/chat-sessions/:id/cancel` returns 404 Not Found.

The TUI uses `requestChatCancelCmd()` which calls this endpoint. It partially works via the TUI but the endpoint doesn't return correctly, resulting in "Cancel request failed" error in the TUI status bar.

### 2. Session archive — PATCH doesn't support status field
`PATCH /v1/chat-sessions/:id` with `{"status":"archived"}` returns 400 "invalid request body".

The PATCH handler appears to only accept `title` updates and rejects unknown fields via `DisallowUnknownFields`. Status management (archiving) is not implemented.

## Required Fix

1. **Cancel turn**: Register and implement `POST /v1/chat-sessions/:id/cancel` — cancel any in-progress agent turn for the session
2. **Session status**: Add `status` field support to PATCH handler — allow `active` → `archived` transitions

## Acceptance Criteria

- [ ] `POST /v1/chat-sessions/:id/cancel` returns 200 on success, cancels active agent turn
- [ ] `PATCH /v1/chat-sessions/:id` with `{"status":"archived"}` returns updated session with `status: "archived"`
- [ ] TUI Esc key sends cancel successfully without "Cancel request failed" error
