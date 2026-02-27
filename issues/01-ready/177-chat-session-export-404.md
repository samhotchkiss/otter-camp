# Task 177: Chat session JSONL export endpoint returns 404

Layer: L2
Effort: S
Depends on: none

## Context

The spec requires: "Session JSONL export/import for debugging and portability".

`GET /v1/chat-sessions/:id/export` returns 404 Not Found — the endpoint is not implemented.

## Required Fix

1. Add `GET /v1/chat-sessions/:id/export` endpoint
2. Returns JSONL (one message per line as JSON) with Content-Type: application/x-ndjson
3. Each line: full message object (id, role, content, metadata, created_at)
4. Optional: `?format=jsonl` (default) or `?format=json` (array)
5. Optional: import endpoint `POST /v1/chat-sessions/:id/import` (lower priority)

## Acceptance Criteria

- [ ] `GET /v1/chat-sessions/:id/export` returns 200 with JSONL content
- [ ] Each line is a valid JSON message object
- [ ] Works for all session types (sync/async, all scopes)
