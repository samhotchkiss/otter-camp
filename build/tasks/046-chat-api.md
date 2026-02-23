# 046: Chat API Endpoints

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 12 §ChatAPI, doc 02 §ChatHTTPRoutes, doc 12 §APIEnvelope |
| Spec status | finished |
| Depends on | 044, 045, 007, 067 |
| Blocks | 072, 083 |

## Scope

Build all HTTP endpoint handlers for the chat subsystem. Wires `ChatService` (task 044)
into REST routes. No new tables, no service logic — HTTP handler layer only.

### Must build

> ⚠️ ISSUE #27 (AMBIGUOUS): All routes below use the `/v1/` prefix. Doc 12 is the authoritative
> source for route paths. Doc 21's test examples use `/api/` prefix and `/sessions/` resource name —
> treat those as pseudocode only. Do NOT implement an `/api/` prefix variant.

**Session endpoints:**
- `POST /v1/chat-sessions` — create a new chat session
  - Body: `{scope_type, scope_id, mode, title?}`
  - Response: `{data: ChatSession}`
  - Calls `ChatService.CreateSession`; maps `ErrActiveSyncSessionExists` → 409
- `GET /v1/chat-sessions` — list sessions for the authenticated org/user
  - Query params: `scope_type`, `scope_id`, `status` (default='active'), `mode`, `limit`, `cursor`
  - Response: `{data: [ChatSession], meta: {cursor, has_more}}`
- `GET /v1/chat-sessions/:id` — get session by ID
  - Response: `{data: ChatSession}` with embedded `participants` array (active participants only)
- `PATCH /v1/chat-sessions/:id` — update mutable session fields
  - Allowed fields: `title`, `mode`
  - Mode switch calls `ChatService.SwitchMode`; maps `ErrTurnInProgress` → 409
- `DELETE /v1/chat-sessions/:id` — close session
  - Calls `ChatService.CloseSession`; maps `ErrTurnInProgress` → 409
  - Returns 200 with `{data: {status: "closed", closed_at}}`

**Message endpoints:**
- `GET /v1/chat-sessions/:id/messages` — paginated message list
  - Query params: `limit` (default 50, max 200), `cursor`, `status`, `before_sequence`, `after_sequence`
  - Response: `{data: [ChatMessage], meta: {cursor, has_more, total_count}}`
  - `total_count` is the total message_count from the session row (not a COUNT(*) query on messages)
- `POST /v1/chat-sessions/:id/messages` — send a new message (triggers a turn)
  - Body: `{content, content_format?}` — role defaults to 'user'; author set from authenticated principal
  - Calls `ChatService.AppendMessage` then publishes to turn engine via domain event
  - Response: `{data: ChatMessage}` with `status='pending'`
  - Returns 202 Accepted (not 201 Created) because the message is queued, not immediately processed
- `PATCH /v1/chat-sessions/:id/messages/:mid` — edit a queued message
  - Body: `{content}`
  - Calls `ChatService.EditQueuedMessage`; maps `ErrMessageNotEditable` → 422
  - Only allowed for human_user principal; returns 403 if called by an agent API key

**Turn control endpoints:**
- `POST /v1/chat-sessions/:id/cancel-turn` — cancel the in-progress turn
  - Body: `{reason?}` — optional cancellation reason text
  - Calls `ChatService.CancelCurrentTurn`; maps `ErrNoActiveTurn` → 409
  - Response: `{data: {turn_id, status: "cancelled"}}`
- `POST /v1/chat-sessions/:id/messages/:mid/steer` — steer an in-progress response
  - Body: `{content}` — the steering instruction
  - Calls `ChatService.SteerTurn`; maps `ErrNoActiveTurn` → 409
  - Response: `{data: ChatMessage}` — the new steer message that was appended

**Reaction endpoints:**
- `POST /v1/chat-sessions/:id/messages/:mid/reactions` — add a reaction
  - Body: `{emoji}`
  - Calls `ChatService.AddReaction`; maps `ErrDuplicateReaction` → 409
  - Response: `{data: ChatMessageReaction}`
- `DELETE /v1/chat-sessions/:id/messages/:mid/reactions/:rid` — remove a reaction
  - Returns 404 if not found; 403 if caller is not the reactor or org-admin
  - Response: 204 No Content

**Participant endpoints:**
- `GET /v1/chat-sessions/:id/participants` — list active participants
  - Response: `{data: [ChatParticipant]}`
- `POST /v1/chat-sessions/:id/participants` — add a participant
  - Body: `{participant_type, participant_id, role?}`
  - Calls `ChatService.AddParticipant`; maps `ErrAlreadyParticipant` → 409
  - Response: `{data: ChatParticipant}` with 201 Created
- `DELETE /v1/chat-sessions/:id/participants/:pid` — remove a participant
  - Returns 404 if participant not found or already removed

**Read cursor endpoints:**
- `GET /v1/chat-sessions/:id/read-cursor` — get the caller's read cursor
  - Response: `{data: {user_id, session_id, last_read_sequence, updated_at}}`
  - Returns 404 if no cursor exists yet (user has not read any messages)
- `PUT /v1/chat-sessions/:id/read-cursor` — update the read cursor
  - Body: `{last_read_sequence}`
  - Calls `ChatReadCursorRepo.Upsert`; idempotent
  - Response: `{data: {last_read_sequence, updated_at}}`
  - Only callable by human_user principals (not agent API keys)

**Artifact endpoint:**
- `GET /v1/chat-sessions/:id/artifacts` — list artifacts for the session
  - Query params: `message_id` (filter by message), `artifact_type`, `limit`, `cursor`
  - Response: `{data: [ChatArtifact], meta: {cursor, has_more}}`

**Auth and RBAC:**
- All chat endpoints require authentication (Bearer token or API key) — 401 if missing
- Session access is scoped to the caller's org: fetching a session from another org returns 404
- Agent API keys can call all endpoints except `PUT /v1/chat-sessions/:id/read-cursor` and
  `PATCH /v1/chat-sessions/:id/messages/:mid` (human-only operations)
- `DELETE /v1/chat-sessions/:id/participants/:pid` requires the caller to be the participant
  being removed, an org-admin, or the session owner

**Response shapes** (all follow the `{data, meta}` envelope from task 067):
```json
// ChatSession
{
  "id": "uuid",
  "organization_id": "uuid",
  "scope_type": "project",
  "scope_id": "uuid",
  "mode": "sync",
  "status": "active",
  "title": "string|null",
  "created_by_type": "human_user",
  "created_by_id": "uuid",
  "last_message_at": "timestamptz|null",
  "turn_count": 0,
  "message_count": 0,
  "created_at": "timestamptz",
  "updated_at": "timestamptz"
}

// ChatMessage
{
  "id": "uuid",
  "session_id": "uuid",
  "turn_id": "uuid|null",
  "sequence_number": 1,
  "author_type": "human_user|agent|null",
  "author_id": "uuid|null",
  "role": "user|assistant|tool_call|tool_result|system",
  "content": "string",
  "content_format": "text",
  "status": "pending|streaming|final|failed|redacted",
  "is_redacted": false,
  "tool_call_id": "string|null",
  "created_at": "timestamptz",
  "updated_at": "timestamptz"
}
```

### Must NOT build

- SSE event stream endpoint (task 047)
- WebSocket endpoint (task 047)
- Turn engine execution (task 048)
- Progressive summarization trigger (task 045 handles this as a background job)
- API envelope middleware (task 067)
- Cursor pagination utilities (task 067)

## Acceptance Criteria

- [ ] `POST /v1/chat-sessions` with an active sync session for the same scope returns 409 with error code `active_sync_session_exists`
- [ ] `POST /v1/chat-sessions/:id/messages` returns 202 (not 201) with `status='pending'`
- [ ] `PATCH /v1/chat-sessions/:id/messages/:mid` called by an agent API key returns 403
- [ ] `PUT /v1/chat-sessions/:id/read-cursor` called by an agent API key returns 403
- [ ] `GET /v1/chat-sessions/:other_org_session_id` returns 404 (not 403) for cross-org access
- [ ] `DELETE /v1/chat-sessions/:id/messages/:mid/reactions/:rid` returns 403 when caller is not the reactor and not org-admin
- [ ] `POST /v1/chat-sessions/:id/cancel-turn` when no turn is in progress returns 409 with error code `no_active_turn`
- [ ] All list endpoints return cursor-based pagination with opaque cursors; `has_more` is accurate

## Tests Required

**Unit tests:**
- Route registration: verify all 18+ routes registered with correct HTTP methods and path patterns
- `POST /v1/chat-sessions` error mapping: `ErrActiveSyncSessionExists` → 409 with `error_code='active_sync_session_exists'`
- `PATCH /v1/chat-sessions/:id/messages/:mid` agent auth rejection: mock API key of agent type → 403
- `GET /v1/chat-sessions/:id` org scoping: mock repo to return `ErrNotFound` for cross-org ID → 404

**Integration tests:**
- Session create → list → get round-trip: `POST` creates session; `GET /v1/chat-sessions` includes it; `GET /v1/chat-sessions/:id` returns full detail with participants
- Message send: `POST /v1/chat-sessions/:id/messages` → response is 202 with status='pending'; message appears in `GET /v1/chat-sessions/:id/messages`
- Reaction lifecycle: add reaction → list reactions → delete reaction → list reactions empty
- Read cursor: `PUT` with `last_read_sequence=5` → `GET` returns `last_read_sequence=5`; second `PUT` with `last_read_sequence=3` → `GET` returns `last_read_sequence=3` (cursor goes backwards — no min enforcement)

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- `POST /v1/chat-sessions/:id/messages` triggers the turn engine asynchronously. The handler appends the message (status='pending'), then publishes a `chat.message.user_sent` domain event. The turn engine (task 048) subscribes to this event and begins the turn. The HTTP response returns immediately with the message row — clients must use SSE (task 047) to observe turn progress.
- `GET /v1/chat-sessions/:id` should embed the active participants list to avoid a second API call. This is a single JOIN query: `SELECT p.* FROM chat_participant p WHERE p.session_id = $1 AND p.removed_at IS NULL`.
- Pagination for `GET /v1/chat-sessions/:id/messages` uses `sequence_number` as the cursor key. The opaque cursor is the base64-encoded sequence_number of the last returned message. `before_sequence` and `after_sequence` query params allow direct range queries (useful for SSE clients catching up after a reconnect).
- All routes must be registered on the shared router from task 007 using the same middleware chain (auth, org-scoping, request logging, error handler). Chat routes do not need a separate router.

> ⚠️ ISSUE #27 (AMBIGUOUS): Doc 21 test code uses `POST /api/sessions/{id}/turns` to send messages. The authoritative path is `POST /v1/chat-sessions/:id/messages`. Do not implement a `/turns` sub-resource or an `/api/` prefix. Doc 21 examples are pseudocode for the test scenarios only.
