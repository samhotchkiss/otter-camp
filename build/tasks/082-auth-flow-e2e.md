# 082: Auth Flow E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §AuthSession, doc 04 §APIKeys, doc 04 §OrgIsolation, doc 06 §LocalAuthMode, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for the full authentication and session management lifecycle. Uses
only the `ottercamp` CLI binary and REST API. Verifies: user registration, login,
authenticated endpoint access, session refresh, API key issuance and authentication,
org isolation (a second org's data is not visible from the first org's token), logout,
and session invalidation after logout.

### Must build

**Test file:** `e2e/auth_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /v1/test/reset` and `ottercamp bootstrap` before scenario begins.
Uses `e2e/testutil/` helpers: `StartServer(t)`, `ResetState(t, baseURL)`, `AdminToken(t,
baseURL)`, `GET(...)`, `POST(...)`.

**Scenario: `TestAuth_RegisterLoginRefreshLogout`**

Step 1 — Reset and bootstrap:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
```

Step 2 — Register a new user (via admin endpoint):
```
POST /v1/users
Authorization: Bearer <admin-token>
{
  "email": "alice@example.com",
  "password": "securepassword123",
  "name": "Alice"
}
→ 201
→ body.data.id is non-empty UUID
→ body.data.email == "alice@example.com"
```

Step 3 — Login as the new user:
```
POST /v1/auth/login
{
  "email": "alice@example.com",
  "password": "securepassword123"
}
→ 200
→ body.data.session_token is non-empty string (length > 20)
→ body.data.expires_at is a future timestamp
```

Step 4 — Call an authenticated endpoint:
```
GET /v1/auth/me
Authorization: Bearer <session-token>
→ 200
→ body.data.email == "alice@example.com"
→ body.data.id is non-empty UUID
```

Step 5 — Refresh the session:
```
POST /v1/auth/refresh
Authorization: Bearer <session-token>
→ 200
→ body.data.session_token is non-empty string
→ body.data.session_token != <original-session-token>  (new token issued)
→ body.data.expires_at is a future timestamp
```

Step 6 — Verify the new token works (old token should still work within sliding window):
```
GET /v1/auth/me
Authorization: Bearer <new-session-token>
→ 200
→ body.data.email == "alice@example.com"
```

Step 7 — Logout:
```
POST /v1/auth/logout
Authorization: Bearer <new-session-token>
→ 204 No Content
```

Step 8 — Verify session is invalidated:
```
GET /v1/auth/me
Authorization: Bearer <new-session-token>
→ 401
→ body.error.code == "session_invalid" or "unauthorized"
```

**Scenario: `TestAuth_APIKey_IssueAndAuthenticate`**

Step 1 — Reset, bootstrap, login as admin:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
GET admin token via POST /v1/auth/login
```

Step 2 — Issue API key:
```
POST /v1/api-keys
Authorization: Bearer <admin-token>
{
  "name": "ci-test-key",
  "scopes": ["read", "write"]
}
→ 201
→ body.data.key is non-empty string (the raw key value; only returned once)
→ body.data.id is non-empty UUID
→ body.data.name == "ci-test-key"
```

Step 3 — Authenticate with API key:
```
GET /v1/auth/me
Authorization: Bearer <api-key-value>
→ 200
→ body.data.email is non-empty (same user who issued the key)
```

Step 4 — Verify API key appears in listing (key value NOT included):
```
GET /v1/api-keys
Authorization: Bearer <admin-token>
→ 200
→ body.data array contains an entry with id == <api-key-id> and name == "ci-test-key"
→ no entry in body.data has a "key" field with the raw key value
```

Step 5 — Delete API key:
```
DELETE /v1/api-keys/<api-key-id>
Authorization: Bearer <admin-token>
→ 204
```

Step 6 — Verify deleted key no longer authenticates:
```
GET /v1/auth/me
Authorization: Bearer <api-key-value>
→ 401
```

**Scenario: `TestAuth_OrgIsolation`**

Step 1 — Reset and bootstrap org A:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
```

Step 2 — Create a project in org A:
```
POST /v1/projects
Authorization: Bearer <org-a-admin-token>
{ "name": "org-a-project", "slug": "org-a-project" }
→ 201
→ record project_id_a
```

Step 3 — Create a second org and obtain its token:
```
POST /v1/orgs
Authorization: Bearer <system-or-admin-token>
{ "name": "Org B", "slug": "org-b" }
→ 201
→ record org_b_id

POST /v1/auth/login with org B credentials
→ record org_b_token
```

Step 4 — Verify org B cannot see org A's project:
```
GET /v1/projects/<project_id_a>
Authorization: Bearer <org-b-token>
→ 404 or 403
→ org B cannot access org A resources

GET /v1/projects
Authorization: Bearer <org-b-token>
→ 200
→ body.data does NOT contain an entry with id == project_id_a
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Rate limiting stress tests (those are integration tests, not E2E)
- Tests for password reset, magic link, or account unlock flows (those are CLI command
  tests in task 068)

## Acceptance Criteria

- [ ] `TestAuth_RegisterLoginRefreshLogout` passes: session token changes on refresh, old session is rejected after logout
- [ ] `TestAuth_APIKey_IssueAndAuthenticate` passes: raw API key value is returned only on creation; deleted key returns 401
- [ ] `TestAuth_OrgIsolation` passes: org B token cannot access org A project by ID; org B's project list excludes org A data
- [ ] All authenticated endpoints return 401 when called with an invalid or expired token
- [ ] Session token after logout returns 401 (not 200 or 403)
- [ ] Full scenario completes in under 60 seconds

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestAuth_RegisterLoginRefreshLogout` — register → login → refresh → logout → session invalid
- `TestAuth_APIKey_IssueAndAuthenticate` — issue key → authenticate → delete → key invalid
- `TestAuth_OrgIsolation` — create project in org A → login as org B → org B cannot see org A project

## Implementer Notes

**ISSUE #27 (RESOLVED — path prefix):**
All API calls use `/v1/` paths (doc 12). Doc 21 examples have been corrected.

**Org isolation test setup:**
The second org creation in `TestAuth_OrgIsolation` may require a special system-level
endpoint or the test bootstraps it via CLI. In `OTTERCAMP_MODE=test`, the server may
expose a helper for creating multiple orgs. If not, create a second org via admin API
with appropriate credentials.

**Session token format:**
The test treats session tokens as opaque strings. Do not assert format (e.g., JWT vs
random token). Assert only that the token is non-empty, that it works for authentication,
and that it stops working after logout.

**Sliding window refresh:**
The 30-day sliding window means the original token may still work briefly after refresh
(within the sliding window). `TestAuth_RegisterLoginRefreshLogout` tests only that the
new token works; it does not assert the old token is immediately invalid. The invalidation
assertion is only after explicit logout.

**Password-less localhost auto-login:**
When running `ottercamp bootstrap` in test mode, the server likely seeds a fixed test
user. The `AdminToken` helper in `e2e/testutil/` knows these credentials. In
`OTTERCAMP_MODE=test`, password-less localhost auto-login (doc 06 local auth mode) may
be active — the `AdminToken` helper should handle both cases (try password login; fall
back to auto-login if the server is in local auth mode).
