# 007: Auth API Endpoints

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §AuthEndpoints, doc 04 §RBAC, doc 12 §APIEnvelope |
| Spec status | finished |
| Depends on | 006 |
| Blocks | 012, 015, 019, 022, 026, 032, 034, 037, 042, 046, 054 |

## Scope

Wire authentication endpoints onto the HTTP server, implement the auth middleware (Bearer
token + API key), implement the RBAC role enforcement middleware, and establish the standard
JSON response envelope. This is the gateway through which all subsequent API tasks will hang
their routes.

### Must build

**Endpoints (all under `/v1/`):**
- `POST /v1/auth/login` — body: `{email, password}`; response: `{token, expires_at, user}` wrapped in standard envelope
- `POST /v1/auth/logout` — requires auth; revokes the current session
- `POST /v1/auth/refresh` — requires auth; extends session and returns new `expires_at`
- `GET /v1/auth/me` — requires auth; returns current user info
- `POST /v1/api-keys` — requires auth; body: `{display_name, scopes, expires_at?}`; response: `{id, key, key_prefix, scopes, expires_at}` — `key` returned only on creation
- `DELETE /v1/api-keys/:id` — requires auth; revoke own API key (admin can revoke any key in org)
- `GET /v1/api-keys` — requires auth; list current user's API keys (key hashes never returned)

**Auth middleware** (`internal/middleware/auth.go`):
- Extracts `Authorization: Bearer <token>` header
- Falls back to `X-API-Key: <key>` header (then `?api_key=` query param — for SSE clients)
- Calls `auth.Service.ValidateSession` or `ValidateAPIKey`
- Injects `*SessionInfo` or `*APIKeyInfo` into `context.Context`
- On valid: calls `RefreshSession` to extend sliding window (async — does not block response)
- On invalid/expired/revoked: returns `401 {"error": {"code": "unauthorized", "message": "..."}}`
- Local auth mode: if `OTTERCAMP_AUTH_MODE=local` and request is from loopback and no credentials provided, calls auto-login and injects session into context

**RBAC middleware** (`internal/middleware/rbac.go`):
- `RequireRole(role string)` middleware factory — returns 403 if the authenticated user's role is below the required level
- Role hierarchy: `admin` > `member`
- Usage: `router.With(RequireRole("admin")).Post("/v1/...", handler)`
- API key scopes: checked separately via `RequireScope(scope string)` — returns 403 if the API key lacks the required scope

**Standard JSON envelope** (`internal/api/response.go`):
- Success: `{"data": <payload>, "meta": {"request_id": "...", "timestamp": "..."}}`
- Error: `{"error": {"code": "...", "message": "...", "details": {}}, "meta": {"request_id": "..."}}`
- All handlers use `api.JSON(w, status, data)` and `api.Error(w, status, code, message)`
- `request_id`: generated per-request (UUID); injected into context and logged; returned in response and `X-Request-ID` header

**Router setup** (`internal/server/router.go`):
- `/v1/` prefix group
- Public routes (no auth middleware): `/v1/auth/login`, `/v1/version`, `/v1/health` (stubs for now)
- Protected routes (auth middleware applied): everything else

### Must NOT build
- Any domain-specific endpoints (those are in their respective tasks)
- Audit event recording wiring (task 008 provides the `AuditRecorder` to wire in)
- Session storage — already handled by task 006

## Acceptance Criteria

- [ ] `POST /v1/auth/login` with correct credentials returns 200 with `data.token` and `data.expires_at`; token is a non-empty string
- [ ] `POST /v1/auth/login` with incorrect credentials returns 401 with `error.code = "invalid_credentials"`
- [ ] `GET /v1/auth/me` with valid Bearer token returns 200 with `data.email` matching the logged-in user
- [ ] `GET /v1/auth/me` with no credentials returns 401
- [ ] `GET /v1/auth/me` with an expired session token returns 401 with `error.code = "session_expired"`
- [ ] `POST /v1/api-keys` returns 201 with `data.key` present; a second `GET /v1/api-keys` does NOT return the `key` field (only `key_prefix`)
- [ ] Auth middleware injects request-id into context; `X-Request-ID` header present on all responses
- [ ] `RequireRole("admin")` applied to a route: member-role request returns 403 with `error.code = "forbidden"`
- [ ] `OTTERCAMP_AUTH_MODE=local`: request from `127.0.0.1` with no credentials to a protected endpoint returns 200 (auto-login active)

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- Auth middleware: test token extraction from `Authorization` header, `X-API-Key` header, query param; test 401 on missing credentials; test 401 on service returning `ErrSessionExpired`
- RBAC middleware: admin request through `RequireRole("admin")` passes; member request returns 403
- `api.Error`: verify JSON shape matches the defined envelope; test that all error code constants produce valid JSON

**Integration tests:**
- Full HTTP test (using `httptest.NewServer`) against real PostgreSQL and real `auth.Service`:
  - Login → GET /v1/auth/me → Logout → GET /v1/auth/me returns 401
  - API key: POST /v1/api-keys → use key in `X-API-Key` header → GET /v1/auth/me succeeds → DELETE /v1/api-keys/:id → key no longer valid
  - Rate limiting: 21 rapid `POST /v1/auth/login` calls from same IP → 429 on the 21st

**E2E tests:**
- None — covered by dedicated E2E task 082

## Implementer Notes

- Route registration pattern: each domain's task registers its own routes by calling `server.RegisterRoutes(r chi.Router)`. The auth task establishes the pattern. All subsequent tasks follow the same pattern.
- The `request_id` should be a UUID v4 string. If an incoming request already has `X-Request-ID`, reuse it (pass-through for reverse proxy tracing).
- Async session refresh: call `auth.Service.RefreshSession` in a goroutine after the response is sent, not before. This avoids adding latency to every authenticated request. Errors from async refresh are logged at debug level.
- Per doc 12 (ISSUE #27): all routes use `/v1/` prefix. Doc 21 test examples using `/api/` are pseudocode only. Implementers must use `/v1/`.
- The `api.JSON` helper should set `Content-Type: application/json` and use `json.NewEncoder(w).Encode(envelope)` — not `json.Marshal` + `w.Write` — to avoid double-allocating the response body for large payloads.
- Local auth mode auto-login should only activate on routes protected by the auth middleware. Routes that are explicitly public (login, version, health) are unaffected.
