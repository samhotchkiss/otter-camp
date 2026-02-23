# 071: Auth and Tenancy Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §Auth, doc 04 §Tenancy, doc 04 §RateLimiting, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 005, 006, 007, 008, 009, 012 |
| Blocks | 089 |

## Scope

Integration test suite for authentication, session lifecycle, API key management, RBAC
enforcement, org isolation, and rate limiting. All tests use a real PostgreSQL database
via `testdb.New(t)`. No mocks except the in-memory rate limit counters (which are real
per-test instances).

### Must build

**Test file:** `internal/auth/auth_integration_test.go`

Build tag: `//go:build integration`

Test setup helper in `internal/testutil/auth.go`:
- `MakeOrg(t, db)` — creates an org row and returns org ID
- `MakeUser(t, db, orgID, role)` — creates a human_user row with hashed password
- `LoginUser(t, srv, email, password)` — calls POST /v1/auth/login, returns session token
- `MakeAPIKey(t, db, userID)` — creates an api_key row, returns raw key

**Test scenarios:**

`TestLogin_Success` — POST /v1/auth/login with valid credentials returns 200 with
`session_token` field; `auth_session` row exists in DB with correct user_id and
`expires_at` ~30 days in the future.

`TestLogin_WrongPassword` — POST /v1/auth/login with wrong password returns 401;
no session row created; `login_attempt_count` on human_user incremented.

`TestLogin_AccountLockout` — 5 consecutive failed login attempts for same account
triggers lockout; 6th attempt returns 423 (locked); `locked_until` is set on
human_user row; `audit_event` row with action='login_failed_lockout' is present.

`TestSession_SlidingExpiry` — login, then call GET /v1/auth/me; assert `expires_at`
is refreshed (30-day sliding window); call again immediately and confirm idempotent
(no double-extension within same request window).

`TestSession_Revocation` — POST /v1/auth/logout with valid Bearer token; subsequent
GET /v1/auth/me returns 401; `auth_session` row has `revoked_at` set.

`TestSession_ExpiredToken` — create a session row with `expires_at` in the past;
GET /v1/auth/me with that token returns 401; no session refresh occurs.

`TestAPIKey_Issuance` — POST /v1/api-keys creates row in `api_key` table; response
contains raw key (only shown once); key is stored as SHA-256 hash; raw key is not
stored.

`TestAPIKey_Validation` — use raw API key as Bearer token on GET /v1/auth/me; returns
200 with correct user identity; `last_used_at` updated on `api_key` row.

`TestAPIKey_Revocation` — DELETE /v1/api-keys/:id; subsequent request with that key
returns 401; row has `revoked_at` set.

`TestRBAC_AdminOnly` — non-admin user attempts admin-only endpoint (e.g., POST
/v1/agents — requires admin or PM role per RBAC matrix); receives 403; admin user
performing same request receives 2xx.

`TestOrgIsolation_CrossOrgData` — create two orgs (A and B) each with one user and
one project; authenticate as user in org A; attempt GET /v1/projects/:id where the
project belongs to org B; receives 404 (not 403 — org isolation returns not-found to
avoid leaking existence). Verify no org-B data surfaces in list endpoints.

`TestOrgIsolation_AuditEvents` — org A user creates a resource; org B user queries
GET /v1/audit-events (if exposed) and receives zero results for org A's events.

`TestRateLimit_PerIP` — send 11 POST /v1/auth/login requests from the same IP within
the rate limit window; 11th returns 429; `Retry-After` header present.

`TestLocalAuth_AutoLogin` — when `OTTERCAMP_AUTH_MODE=local` and request originates
from 127.0.0.1/::1, GET /v1/auth/me succeeds without a token (auto-login as the
instance owner user); returns 200 with user identity.

`TestAuditEvent_DelegationPrincipal` — perform an action where `delegated_by` is set
(agent acting on behalf of user); assert `audit_event` row has correct
`principal_type='agent'`, `principal_id`, and non-null `delegated_by_id` pointing to
the human user.

### Must NOT build

- E2E tests for auth flows (those are in task 082)
- Tests that mock the database layer
- Load/performance tests (CI budget is 5 minutes for integration suite)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/auth/... -tags integration`
- [ ] `testdb.New(t)` creates an isolated schema per test; parallel tests do not share state
- [ ] `TestLogin_AccountLockout` verifies the audit_event row is written for the lockout event
- [ ] `TestOrgIsolation_CrossOrgData` uses two separate org rows with separate users; confirms 404 (not 403) on cross-org access
- [ ] `TestAPIKey_Issuance` confirms the raw key is NOT stored in the api_key table (only the hash)
- [ ] `TestLocalAuth_AutoLogin` sets `OTTERCAMP_AUTH_MODE=local` via environment and tests the auto-login path
- [ ] `TestRateLimit_PerIP` uses the real in-memory rate limiter (not mocked); confirms 429 on the N+1 request
- [ ] All tests clean up after themselves (testdb teardown is automatic via `t.Cleanup`)

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestLogin_Success`
- `TestLogin_WrongPassword`
- `TestLogin_AccountLockout`
- `TestSession_SlidingExpiry`
- `TestSession_Revocation`
- `TestSession_ExpiredToken`
- `TestAPIKey_Issuance`
- `TestAPIKey_Validation`
- `TestAPIKey_Revocation`
- `TestRBAC_AdminOnly`
- `TestOrgIsolation_CrossOrgData`
- `TestOrgIsolation_AuditEvents`
- `TestRateLimit_PerIP`
- `TestLocalAuth_AutoLogin`
- `TestAuditEvent_DelegationPrincipal`

**E2E tests:** None — covered by task 082.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Rate limiter: real in-memory instance per test (not shared between tests; no Redis/external store)
- Password hashing: real bcrypt (work factor 12 — tests will be slower; acceptable)
- Clock: injected via `clock.Clock` interface (task 001); use `clock.Fake` to test session expiry
  without waiting 30 days

**testdb pattern:**
```go
func TestLogin_Success(t *testing.T) {
    db := testdb.New(t)
    srv := testserver.New(t, db)
    org := testutil.MakeOrg(t, db)
    user := testutil.MakeUser(t, db, org.ID, "member")
    // ... call srv.Client().Post(...)
}
```

**ISSUE #1/#23 impact:** Budget cap column name is unresolved. Auth tests do not touch
budget columns; no stub behavior needed here.

**Session token storage:** The raw session token returned by login is never stored in
the DB — only the SHA-256 hash. Tests must store the raw token from the login response
and use it as a Bearer token; they cannot reconstruct it from the DB.

**Parallel test safety:** Each `testdb.New(t)` call creates a dedicated PostgreSQL schema
(or database clone depending on implementation in task 002). Tests are safe to run with
`t.Parallel()` within this file.
