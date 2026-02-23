# 047: SSE Realtime and WebSocket

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 12 §SSEStream, doc 12 §WebSocket, doc 12 §EventFanOut, doc 19 §MobileRealtime |
| Spec status | finished |
| Depends on | 024, 043, 044, 007 |
| Blocks | 048, 072, 083 |

## Scope

Build the realtime event streaming layer: the SSE endpoint for browsers, the WebSocket
endpoint for bidirectional communication (primarily mobile), and the event fan-out mechanism
that publishes domain events from the `domain_event` table to connected clients.

### Must build

**SSE endpoint:**
- `GET /v1/events/stream` — server-sent events stream
  - Query params:
    - `scopes` — comma-separated scope specs, e.g. `session:uuid,project:uuid,org` — filters which events the client receives
    - `last-event-id` — reconnect: the SSE `Last-Event-ID` header value (also accepted as query param for clients that cannot set headers)
  - Response headers:
    - `Content-Type: text/event-stream`
    - `Cache-Control: no-cache`
    - `X-Accel-Buffering: no` — disable nginx buffering
    - `Connection: keep-alive`
  - Auth: API key or Bearer token required; 401 if missing
  - Scope enforcement: client can only subscribe to scopes they have access to (org membership;
    session must be in caller's org; project must be in caller's org). Invalid scopes → 400.
  - **Event format** (standard SSE):
    ```
    id: 12345\n
    event: chat.message.created\n
    data: {"event_type":"chat.message.created","payload":{...},"occurred_at":"..."}\n\n
    ```
  - `id` is the `domain_event.seq` integer — monotonically increasing, globally ordered within the org
  - On connection: immediately sends a `connected` event with the current high-watermark seq:
    ```
    event: connected
    data: {"high_watermark": 12345}
    ```
  - **Reconnect / gap detection**: on reconnect with `Last-Event-ID=N`, replay all events with
    `seq > N` for the subscribed scopes. If `N` is older than the retention window (events purged),
    send a special gap header `X-Events-Gap: true` on the first data frame and set
    `data.gap=true` in the `connected` event. Client must do a full refresh.
  - **Buffer overflow**: the per-connection send buffer is 1000 events. If the buffer fills (slow
    client or network), close the connection with a `buffer_overflow` close event and let the
    client reconnect.
  - **Keepalive**: send a SSE comment line `: heartbeat` every 15 seconds to prevent proxy timeouts.

**WebSocket endpoint:**
- `GET /v1/ws` — WebSocket upgrade endpoint
  - Same `scopes` query param and `last-event-id` as SSE
  - Auth: same as SSE (API key or Bearer token in initial HTTP request headers or `?token=` query param for clients that cannot set WS headers)
  - **Bidirectional messages from client to server** (JSON frames):
    - `{type: "subscribe", scopes: ["session:uuid"]}` — add new scope subscriptions mid-session
    - `{type: "unsubscribe", scopes: ["session:uuid"]}` — remove scope subscriptions
    - `{type: "typing", session_id: "uuid"}` — broadcast typing indicator event to other session participants
    - `{type: "ping"}` — client-initiated keepalive; server responds with `{type: "pong"}`
  - **Messages from server to client** (JSON frames, same event format as SSE):
    - All domain events matching the client's subscribed scopes
    - `{type: "typing", session_id, user_id, expires_at}` — typing indicator (15-second TTL)
    - `{type: "pong"}`
  - Typing indicators are ephemeral: broadcast to all OTHER session participants via the fan-out
    layer, expire after 15 seconds. Not persisted to `domain_event` table.
  - Reconnect semantics: same as SSE — `last-event-id` query param on upgrade request.
  - On disconnect, server-side subscription state is cleaned up. No persistent subscription state.

**LISTEN/NOTIFY fan-out:**
- The event fan-out mechanism uses PostgreSQL `LISTEN/NOTIFY` as the inter-process pub/sub channel.
- `FanOutWorker` — a goroutine (or goroutine pool) that:
  1. Holds a dedicated PostgreSQL connection with `LISTEN domain_events_channel`.
  2. When a `NOTIFY` arrives on `domain_events_channel`, reads the payload (contains the `domain_event.seq` and `organization_id`).
  3. Fetches the full `domain_event` rows from DB for the notified seq range.
  4. Routes events to all active SSE and WebSocket connections whose subscribed scopes match the event's `entity_type` and `entity_id`.
- `NOTIFY` is triggered by the event bus writer (task 024) via a DB trigger or explicit `pg_notify` call in the `InsertDomainEvent` transaction.
- Fan-out is in-process (all SSE/WS connections are on the same server process in V2). Multi-process fan-out (via a message broker) is out of scope for V2.

**Scope subscription routing:**
- Each connected client declares which scopes it subscribes to.
- Scope types and their event routing:
  - `org` — receives all events for the org (admin-only; reject if caller not org-admin)
  - `session:uuid` — receives `chat.*` events for the specified session
  - `project:uuid` — receives `project.*` and `task.*` events for the specified project
  - `task:uuid` — receives `task.*` events for the specific task
- Access check on each scope at subscribe time: caller must have read access.
- A single SSE connection can subscribe to multiple scopes simultaneously.

**API key scope enforcement on SSE:**
- If the caller authenticates with an API key, they can only subscribe to scopes that the API
  key has access to. Agent API keys cannot subscribe to the `org` scope (org-admin only).
- API key auth for SSE uses the same middleware as REST endpoints (task 007).

**Notification preference filtering:**
- Before dispatching a `chat.*` event to a connected human_user, check their
  `chat_participant.notification_preference` for the relevant session:
  - `'all'` — dispatch all events
  - `'mentions'` — dispatch only events where the user is mentioned (author_id = user_id or
    message content contains `@user_name`)
  - `'none'` — suppress all chat events for this session (client still receives non-chat events)
- This filter is applied in the fan-out layer, not in the DB query.

**Event types broadcast via SSE/WS** (non-exhaustive; covers chat domain events):
- `chat.session.created` — new session
- `chat.session.updated` — mode change, title change, status change
- `chat.session.closed` — session closed
- `chat.message.created` — new message (pending or final)
- `chat.message.updated` — status change (pending→streaming, streaming→final)
- `chat.message.redacted` — message redacted
- `chat.turn.started` — turn began
- `chat.turn.completed` — turn ended
- `chat.turn.cancelled` — turn cancelled
- `chat.reaction.added` — reaction added
- `chat.reaction.removed` — reaction removed
- `project.task.updated` — task status change (for project scope subscribers)
- `project.task.created` — new task

### Must NOT build

- Domain event table DDL (task 024)
- Job queue (task 024)
- Chat service logic (task 044)
- Push notification delivery (task 066)
- WebSocket mobile optimization (task 069)
- Authentication middleware (task 007)

## Acceptance Criteria

- [ ] `GET /v1/events/stream` without auth returns 401 (not a 200 with an error event)
- [ ] SSE connection with `scopes=session:uuid` receives `chat.message.created` events when a message is appended to that session
- [ ] Reconnect: connect with `last-event-id=N`, disconnect, send 3 more events, reconnect with `last-event-id=N` → receive the 3 missed events in order
- [ ] Buffer overflow: if the server-side buffer reaches 1000 events, the connection is closed with a `buffer_overflow` event (not silently dropped)
- [ ] `GET /v1/events/stream?scopes=session:other_org_session_id` returns 400 (invalid scope — session not in caller's org)
- [ ] `GET /v1/ws` with `type=typing` message from one client broadcasts a `typing` frame to other connected clients subscribed to the same session
- [ ] SSE keepalive: after 15 seconds of no events, a `: heartbeat` comment line is sent
- [ ] Agent API key subscribing to `org` scope returns 403 in the scope validation step

## Tests Required

**Unit tests:**
- Scope routing: event with `entity_type='chat_session', entity_id=X` routes to subscribers with scope `session:X` and `org`; does NOT route to `session:Y`
- Notification preference filter: `preference='mentions'` suppresses event where user is not mentioned; passes event where `author_id=user_id`
- Gap detection: request with `last-event-id=50` where earliest stored event is seq=60 → `X-Events-Gap: true` header and `gap=true` in connected event
- Buffer overflow: simulate 1001 events queued for a slow consumer → connection closed with `buffer_overflow` close event

**Integration tests:**
- Full fan-out cycle: append a chat message via `ChatService.AppendMessage`; verify `domain_event` row created; verify SSE client receives `chat.message.created` event within 500ms
- Multi-scope: client subscribed to `session:A` and `project:B`; events for session A and project B both delivered; event for session C not delivered
- Reconnect replay: establish SSE connection, receive events 1–5, disconnect, events 6–8 published, reconnect with `last-event-id=5` → receive events 6–8
- WebSocket typing: connect two clients to same session; client 1 sends `{type:"typing"}`; client 2 receives `typing` frame within 200ms

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- The `FanOutWorker` must hold a single dedicated PostgreSQL connection for LISTEN. This connection is separate from the connection pool. If the LISTEN connection drops, the worker must reconnect and re-issue `LISTEN domain_events_channel`. On reconnection, it should query the `domain_event` table for any events missed during the downtime (use `consumer_cursor` pattern from task 024).
- SSE connections must be properly cleaned up when the HTTP request context is cancelled (client disconnect). Use a goroutine per connection that selects on both the fan-out event channel and the `ctx.Done()` channel. The fan-out layer must remove the connection from its routing table on cleanup.
- Typing indicators should NOT be persisted to the `domain_event` table. They are ephemeral and high-frequency. Use a direct in-process broadcast: when a typing message arrives on a WebSocket connection, immediately broadcast to all other WebSocket/SSE connections subscribed to the same session without a DB round-trip.
- `X-Accel-Buffering: no` is required for nginx compatibility. Also send `X-Content-Type-Options: nosniff`. For AWS CloudFront or other CDN users, document that SSE requires the CDN to be configured for streaming (not caching).
- For V2, all SSE and WebSocket connections are handled in-process on a single server. The `FanOutWorker` maintains an in-memory map of `session_id → []chan Event` for routing. This is not horizontally scalable, but is correct for the V2 single-process deployment model. Note this limitation in the code with a comment: `// TODO: replace with Redis pub/sub for multi-process deployments`.
