# 069: Mobile API — Dashboard Aggregation, Push Token Registration, and WebSocket Preference

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 19 §MobileDashboard, doc 19 §WebSocketPreference, doc 19 §DeltaSync, doc 19 §BiometricAuth, doc 19 §ConcurrentSessions |
| Spec status | finished |
| Depends on | 007, 043, 044, 047, 027, 028, 066, 067 |
| Blocks | 083 |

## Scope

Build the mobile-specific API additions: the `/v1/mobile/dashboard` aggregation endpoint;
delta sync via `last-event-id` on the SSE stream; WebSocket-preferred connection negotiation;
multiple concurrent session support; and the connection between push token registration
(task 066) and this endpoint layer.

### Must build

**`GET /v1/mobile/dashboard`** (`internal/api/mobile_handler.go`):

Optional aggregation endpoint — returns a single payload with the most important state
for the mobile home screen, reducing the number of individual requests a mobile client
must make at startup.

Query params:
- `project_ids` (optional, comma-separated UUIDs) — include project summaries for these projects
- `inbox_limit` (optional, int, default 10, max 50) — number of inbox items to include

Response:
```json
{
  "data": {
    "inbox": {
      "unread_count": 3,
      "items": [ ... ]          // top N inbox items ordered by urgency desc, created_at desc
    },
    "projects": [
      {
        "id": "...",
        "name": "...",
        "slug": "...",
        "active_task_count": 2,
        "blocked_task_count": 1,
        "latest_deploy": {
          "deployed_at": "...",
          "commit_sha": "...",
          "status": "deployed"
        }
      }
    ],
    "recent_sessions": [
      {
        "id": "...",
        "scope_type": "project",
        "scope_id": "...",
        "last_message_at": "...",
        "unread_message_count": 0
      }
    ],
    "server_time": "2024-01-15T10:30:00Z"
  }
}
```

Implementation:
- Run inbox query, project query, and session query in parallel (use `errgroup`).
- `inbox`: `SELECT * FROM inbox_item WHERE target_user_id=$1 AND actioned_at IS NULL ORDER BY urgency DESC, created_at DESC LIMIT $2`
- `projects`: for each requested project ID, load `project` + latest `project_environment` row.
- `recent_sessions`: load up to 5 sessions scoped to the user's projects, ordered by most recent message.
- Total response time target: < 200ms (parallel queries; no sequential dependency).
- No new DB tables — this is a read-only aggregation over existing tables.

**Delta sync via SSE** (`internal/api/events_handler.go`):

The SSE endpoint (`GET /v1/events/stream?scopes=...`) from task 047 already supports
`Last-Event-ID`. This task adds documentation and validation:
- Validate `Last-Event-ID` header is a valid integer (return 400 if malformed).
- On reconnect: replay events with `seq > last_event_id` from `domain_event` table before
  switching to live LISTEN mode (already implemented in task 047; this task validates the
  behavior is correct for mobile reconnect patterns).
- Add `X-Events-Gap` header when purged events are detected (seq gap > 1 in replayed events).
- Document: mobile clients should use `Last-Event-ID` on reconnect after network interruptions
  instead of fetching the full state again.

**WebSocket-preferred connection negotiation** (`internal/api/ws_handler.go`):

Extend the WebSocket endpoint (`/v1/ws`) from task 047:
- Add `GET /v1/ws/negotiate` endpoint that returns the server's preferred transport:
  ```json
  {
    "data": {
      "preferred": "websocket",
      "fallback": "sse",
      "websocket_url": "wss://example.com/v1/ws",
      "sse_url": "https://example.com/v1/events/stream"
    }
  }
  ```
- Mobile clients call this endpoint first to get the server's current transport preference.
- The server currently always returns `"preferred": "websocket"` for mobile clients
  (detected by `User-Agent: OtterCamp-iOS/` or `OtterCamp-Android/` prefix).
- For non-mobile clients: returns `"preferred": "sse"`.
- This is a hint only — clients may use either transport regardless of the preference.

**Multiple concurrent sessions** (`internal/api/session_handler.go`):

Doc 19 specifies that mobile + web + TUI can be logged in simultaneously (multiple concurrent
sessions per user). The auth system (task 006) already supports multiple `auth_session` rows
per user. This task adds:
- `GET /v1/auth/sessions` — lists all active sessions for the authenticated user:
  ```json
  {
    "data": {
      "sessions": [
        {
          "session_id": "...",
          "client_type": "mobile",   // from user_agent
          "created_at": "...",
          "last_used_at": "...",
          "ip_address": "..."
        }
      ]
    }
  }
  ```
- `DELETE /v1/auth/sessions/:id` — revokes a specific session (e.g., "log out this device").
- `DELETE /v1/auth/sessions` — revokes all sessions except the current one ("log out all other devices").

Client type detection from `User-Agent`:
- `OtterCamp-iOS/` → `mobile`
- `OtterCamp-Android/` → `mobile`
- `OtterCamp-TUI/` → `tui`
- `Mozilla/` → `web`
- Everything else → `api`

**Biometric auth note:**
- Doc 19 states: "biometric authentication is client-side only; no server changes required."
- This task adds NO server-side changes for biometric auth. The server sees a normal API
  key request after the client completes local biometric verification.
- Add a comment in the auth handler documenting this design decision.

**Push token API wiring** (cross-reference to task 066):
- `POST /v1/me/push-token` and `DELETE /v1/me/push-token/:device_id` are defined in task 066.
- This task adds them to the router. No implementation needed here — just route wiring.

**Mobile-specific error handling:**
- Mobile clients often disconnect mid-request. Ensure all streaming handlers (SSE, WS) clean
  up properly on client disconnect:
  - Detect via `ctx.Done()` / `CloseNotify`.
  - Log at DEBUG level (not INFO) to avoid log spam from normal mobile disconnects.
  - No 5xx errors emitted for client disconnects.

### Must NOT build

- SSE infrastructure (task 047)
- WebSocket infrastructure (task 047)
- Push notification delivery or preference storage (task 066)
- Inbox item service (tasks 027, 028)
- Chat session service (tasks 043, 044)
- Auth session management (tasks 005, 006) — this task only adds session listing/revocation endpoints

## Acceptance Criteria

- [ ] `GET /v1/mobile/dashboard` returns 200 with `inbox`, `projects`, and `recent_sessions` keys; all three sub-queries run in parallel (no sequential dependency)
- [ ] `GET /v1/mobile/dashboard?project_ids=<id>` includes a `latest_deploy` sub-object for the requested project
- [ ] `GET /v1/ws/negotiate` returns `preferred: 'websocket'` for `User-Agent: OtterCamp-iOS/1.0` and `preferred: 'sse'` for a browser user agent
- [ ] `GET /v1/auth/sessions` returns all active sessions for the authenticated user
- [ ] `DELETE /v1/auth/sessions/:id` revokes the specified session; subsequent requests with that session token return 401
- [ ] `DELETE /v1/auth/sessions` revokes all sessions except the caller's current session; caller remains authenticated
- [ ] `GET /v1/events/stream` with malformed `Last-Event-ID` returns 400; valid integer reconnects correctly

## Tests Required

**Unit tests:**
- Client type detection: `OtterCamp-iOS/2.1` → `mobile`; `Mozilla/5.0 ...` → `web`; empty → `api`
- Dashboard parallel execution: mock all three sub-queries; verify they are invoked concurrently (use a sync mechanism to detect sequencing)
- `DELETE /v1/auth/sessions` — "log out all others": verify current session token still valid after call; other sessions invalid

**Integration tests:**
- `GET /v1/mobile/dashboard`: seed inbox items + project + session; verify response contains correct counts
- Session listing/revocation: create 3 auth sessions; `GET /v1/auth/sessions` returns 3; `DELETE /v1/auth/sessions` revokes 2; caller still authenticated; other sessions return 401
- SSE reconnect: insert 10 events; connect with `Last-Event-ID=7`; verify events 8, 9, 10 replayed

**E2E tests:**
- None — covered by dedicated E2E task 083
