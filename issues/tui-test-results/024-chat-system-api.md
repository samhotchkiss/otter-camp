# Test 024: Chat System API

**Section:** 4. Chat System
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Direct API calls using session token (s@swh.me). Tested all Chat System endpoints from functionality-list.md.

## Findings Per Endpoint

**`GET /v1/chat-sessions`** — PASS ✓
- Returns sessions list with scope_type, scope_id, mode, status, title, created_by_type
- Filter `?scope_type=organization` works
- Sessions have modes (sync/async) and statuses (active)

**`POST /v1/chat-sessions`** — PASS ✓
- Requires scope_type, scope_id, mode
- Returns 409 `active_sync_session_exists` if a sync session already exists — correct business logic

**`GET /v1/chat-sessions/:id`** — PASS ✓
- Returns full session with participants embedded

**`PATCH /v1/chat-sessions/:id`** — PARTIAL ✓
- Title update works ✓
- Status update (archive) returns 400 `invalid request body` — status not updatable via PATCH ✗

**`GET /v1/chat-sessions/:id/messages`** — PASS ✓
- Returns message list with sequence_number, role, content, status, is_redacted, metadata
- `?limit=N` works for pagination

**`POST /v1/chat-sessions/:id/messages`** — PASS ✓
- Creates human message; returns message with status=pending, sequence_number assigned

**`DELETE /v1/chat-sessions/:id/messages/:msg_id`** — FAIL ✗
- Returns 405 Method Not Allowed — message redaction endpoint not implemented

**`GET /v1/chat-sessions/:id/participants`** — PASS ✓
- Returns human_user and agent participants with roles (owner/member)

**`POST /v1/chat-sessions/:id/participants`** — PASS ✓
- Adds participant; returns 409 if already participant

**`DELETE /v1/chat-sessions/:id/participants/:p_id`** — UNTESTED

**`POST /v1/chat-sessions/:id/cancel`** — FAIL ✗
- Returns 404 Not Found — cancel turn endpoint not implemented via this path

**Message reactions** (`POST /v1/chat-sessions/:id/messages/:msg_id/reactions`) — PASS ✓
- Works with `{"reaction":"positive"}` field (not `type`)

**Export** (`GET /v1/chat-sessions/:id/export`) — FAIL ✗
- Returns 404 Not Found

**Read cursor** — UNTESTED/UNCLEAR
- `PATCH /v1/chat-sessions/:id/read-cursor` — 404 Not Found on tried path

## Functional Behavior

**Session scoping (org/project/task):** ✓
**Session modes (sync/async):** ✓
**@mention routing:** Tested live via TUI — Frank responds ✓
**Multi-party turn sequencing:** Not directly tested
**Context assembly (7-layer):** Confirmed working via live tool use (previous sessions)

## Issues Filed

- Issue #175 — Message redaction (DELETE .../messages/:id) returns 405
- Issue #176 — Cancel turn endpoint (POST .../cancel) returns 404; session archive via PATCH not supported
- Issue #177 — JSONL export (GET .../export) returns 404
