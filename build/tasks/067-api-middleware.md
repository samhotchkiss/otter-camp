# 067: API Middleware, Envelope Standardization, and Pagination

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 12 §APIConventions, doc 12 §Pagination, doc 12 §IdempotencyKeys, doc 12 §SearchEndpoint |
| Spec status | finished |
| Depends on | 001, 007, 024 |
| Blocks | 046, 054, 037, 032, 042, 022, 019, 015, 069, 070 |

## Scope

Build the shared API middleware layer used by all route handlers: response envelope
standardization; cursor-based pagination; idempotency key middleware; `/v1/` prefix
enforcement; `GET /v1/search`; `GET /v1/tasks/:id/diff`; and the `version` / health
endpoint wiring (delegating to task 063 for health logic).

### Must build

**Response envelope** (`internal/api/envelope.go`):

All successful responses use:
```json
{
  "data": <payload>,
  "meta": {
    "request_id": "01J...",
    "pagination": { ... }  // only on list responses
  }
}
```

All error responses use:
```json
{
  "error": {
    "code": "resource_not_found",
    "message": "Project with id ... not found",
    "request_id": "01J..."
  },
  "meta": {
    "request_id": "01J..."
  }
}
```

`Responder.JSON(w, status int, data any)`:
- Wraps `data` in `{data, meta:{request_id}}`.
- Sets `Content-Type: application/json; charset=utf-8`.

`Responder.JSONList(w, status int, data any, pg PaginationMeta)`:
- Wraps with `meta.pagination` added.

`Responder.Error(w, status int, code, message string)`:
- Wraps in error envelope with `request_id` from context.

Standard error codes (open vocabulary; these are the canonical values):
- `resource_not_found` (404)
- `validation_error` (422)
- `unauthorized` (401)
- `forbidden` (403)
- `rate_limit_exceeded` (429)
- `conflict` (409)
- `internal_error` (500)
- `not_implemented` (501)
- `idempotency_conflict` (409 with `code='idempotency_conflict'`)

**Cursor-based pagination** (`internal/api/pagination.go`):

`PaginationParams`:
- `cursor string` — opaque base64-encoded cursor (encodes `{created_at, id}`)
- `limit int` — default 50, max 200; values outside range are clamped (not errors)
- `order string` — `'asc'` or `'desc'`; default `'desc'`

`PaginationEncoder.Encode(createdAt time.Time, id uuid.UUID) string`:
- Returns base64url-encoded JSON `{"t":"2024-01-15T10:30:00Z","id":"..."}`.

`PaginationDecoder.Decode(cursor string) (createdAt time.Time, id uuid.UUID, error)`:
- Decodes the cursor; returns descriptive error if malformed.

`PaginationMeta` (included in list response `meta.pagination`):
```json
{
  "next_cursor": "eyJ0IjoiMjAy...",  // null if no more results
  "prev_cursor": "eyJ0IjoiMjAy...",  // null if at start
  "limit": 50,
  "total": null                       // total count is NOT provided (expensive; omit)
}
```

All list repositories must accept `(cursor *PaginationCursor, limit int)` and return a
`next_cursor` based on the last row's `(created_at, id)`.

**Idempotency key middleware** (`internal/middleware/idempotency.go`):

Applied to all `POST`, `PUT`, `PATCH` routes.

`IdempotencyMiddleware.Handle(next http.Handler) http.Handler`:
- Reads `Idempotency-Key` header (optional — if absent, request proceeds normally).
- If present:
  1. Hash the key + request method + path + body with SHA-256 → `request_hash`.
  2. Check `idempotency_key` table (task 024): `SELECT response_body, response_status FROM idempotency_key WHERE key=$1 AND organization_id=$2`.
  3. If found AND not expired (< 24 hours): return stored response directly (do not call handler).
  4. If found AND `request_hash` differs: return 409 `{code:'idempotency_conflict', message:'Idempotency key reused with different request body'}`.
  5. If not found: call handler; on success (2xx): store response body + status in `idempotency_key` table with `expires_at = now() + 24h`.
- Key format: any string; recommended `{noun}-{ulid}` pattern (not enforced).

**`/v1/` prefix enforcement** (`internal/middleware/prefix.go`):

`PrefixEnforcementMiddleware`:
- All routes are mounted under `/v1/`.
- Request to a path that does NOT start with `/v1/` and is not one of:
  - `/health`, `/health/live`, `/health/ready`
  - `/metrics`
  - `/` (root redirect to `/v1/` 308)
  → Returns 404 `{code:'not_found', message:'This API uses the /v1/ prefix. See docs.'}`.

**`GET /v1/search`** (`internal/api/search_handler.go`):

`SearchHandler.Search(w, r)`:
- Query params: `q string` (required, min 2 chars), `types []string` (optional filter:
  `project|task|agent|session|flow_template`), `limit int` (default 10, max 50).
- Implementation: run parallel queries against each requested type:
  - `project`: `WHERE slug ILIKE $q OR name ILIKE $q AND organization_id=$org`
  - `task`: `WHERE title ILIKE $q AND project_id IN (user's projects)`
  - `agent`: `WHERE name ILIKE $q AND organization_id=$org`
  - `session`: `WHERE scope matches user's accessible projects`
  - `flow_template`: `WHERE name ILIKE $q AND (organization_id=$org OR project_id IN (...))`
- Uses `%q%` ILIKE pattern (simple contains search; no full-text index required at this stage).
- Returns:
```json
{
  "data": {
    "results": [
      {"type": "project", "id": "...", "title": "My Project", "url": "/v1/projects/..."},
      {"type": "task", "id": "...", "title": "Fix auth bug", "url": "/v1/tasks/..."}
    ],
    "query": "auth",
    "total_results": 2
  }
}
```
- `q` < 2 chars → 422 validation error.
- No authentication scope leakage: results are filtered to the authenticated user's org.

**`GET /v1/tasks/:id/diff`** (`internal/api/diff_handler.go`):

`DiffHandler.GetTaskDiff(w, r)`:
- Loads the `project_task` by ID; verifies task belongs to auth'd org.
- Reads `branch_name` from the task; calls `GitService.Diff(ctx, projectID, branchName, baseBranch)`.
- Returns:
```json
{
  "data": {
    "task_id": "...",
    "branch_name": "feature/...",
    "base_branch": "main",
    "diff_stat": {"files_changed": 3, "insertions": 45, "deletions": 12},
    "diff_text": "diff --git a/..."  // truncated at 500KB
  }
}
```
- If `GitService` is not configured for the project (no `project_remote`): return 422
  `{code:'no_remote', message:'Project has no git remote configured'}`.
- If branch does not exist: return 404.

**`GET /v1/version`** (`internal/api/version_handler.go`):

Returns:
```json
{
  "data": {
    "version": "0.1.0",
    "commit": "a3f8c1d",
    "built_at": "2024-01-15T10:00:00Z",
    "go_version": "go1.22.0"
  }
}
```
Version info is injected at build time via ldflags (see task 068).
No auth required.

### Must NOT build

- Rate limiting (task 063)
- CORS middleware (task 063)
- Secret scrubbing (task 063)
- Request ID middleware (task 063) — this task consumes the request_id that task 063 sets
- Health endpoint logic (task 063) — this task only adds `GET /v1/version`
- Auth middleware (task 007)
- Individual domain handlers (they depend on this task's envelope/pagination utilities)

## Acceptance Criteria

- [ ] `Responder.JSON` wraps payload in `{data, meta:{request_id}}` with correct Content-Type header
- [ ] `Responder.Error` wraps in `{error:{code, message, request_id}, meta:{request_id}}`
- [ ] `PaginationDecoder.Decode` on a valid cursor returns correct `createdAt` and `id`; malformed cursor returns descriptive error
- [ ] List endpoint response includes `meta.pagination.next_cursor` (non-null when more results exist) and `null` when on the last page
- [ ] Idempotency middleware returns stored response on second request with same key+body; returns 409 on key reuse with different body
- [ ] `GET /v1/search?q=auth` returns results across all types; `q=a` (1 char) returns 422
- [ ] `GET /v1/version` returns build-time version, commit, and `built_at` without authentication
- [ ] Request to `/api/projects` returns 404 with the `/v1/ prefix` error message

## Tests Required

**Unit tests:**
- `Responder.JSON`: verify envelope structure; verify `request_id` matches context value
- `Responder.Error`: verify `error.code` and `error.request_id` fields
- `PaginationEncoder` / `PaginationDecoder`: encode then decode → same values; corrupted base64 → error
- Pagination limit clamping: `limit=0` → 50; `limit=300` → 200; `limit=25` → 25
- Idempotency middleware: first call stores response; second call returns stored (no handler invocation); different body → 409

**Integration tests:**
- Full idempotency round-trip with real DB: POST with idempotency key; verify `idempotency_key` row created; POST again → same response; 25-hour-old key is expired → new execution
- `GET /v1/search?q=proj&types=project` with seeded data → returns matching project; empty query → 422
- `GET /v1/tasks/:id/diff` with no remote → 422; with mock git service → diff returned

**E2E tests:**
- None — covered by dedicated E2E task 083 and 088
