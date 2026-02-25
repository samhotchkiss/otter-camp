# 006: Auth Service

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | M (1–2 days) |
| Spec refs | doc 04 §Authentication, doc 04 §SessionManagement, doc 04 §APIKeyAuth, doc 04 §RateLimiting, doc 08 §LocalAuth |
| Spec status | finished |
| Depends on | 005 |
| Blocks | 007, 012 |

## Scope

Implement the authentication service: password login, session creation and lifecycle,
session refresh with sliding 30-day window, session revocation, API key issuance and
validation, per-IP and per-account rate limiting, local-auth mode, and the password-less
localhost auto-login feature.

### Must build

**`internal/auth/` package:**

`Service` interface:
```go
type Service interface {
    Login(ctx, email, password, ipAddr, userAgent string) (*LoginResult, error)
    Logout(ctx, sessionToken string) error
    RefreshSession(ctx, sessionToken string) (*SessionInfo, error)
    ValidateSession(ctx, sessionToken string) (*SessionInfo, error)
    ValidateAPIKey(ctx, rawKey string) (*APIKeyInfo, error)
    IssueAPIKey(ctx, userID uuid.UUID, displayName string, scopes []string, expiresAt *time.Time) (*IssueResult, error)
    RevokeAPIKey(ctx, keyID, requestingUserID uuid.UUID) error
    ListAPIKeys(ctx, userID uuid.UUID) ([]*APIKeyInfo, error)
    MagicLink(ctx, email string) (*MagicLinkResult, error)  // stub: returns token for local use
    ResetPassword(ctx, token, newPassword string) error
    UnlockAccount(ctx, userID uuid.UUID) error
}
```

**Login flow:**
1. Look up `human_user` by `(org_id, email)` — org determined from `OTTERCAMP_DEFAULT_ORG_ID` env var or request context in multi-tenant mode (single-tenant for now)
2. Check per-IP rate limit: max 20 login attempts per IP per 15 minutes (in-memory counter, keyed by IP)
3. Check per-account rate limit: `failed_login_attempts >= 10` → check `locked_until`; if locked, return `ErrAccountLocked`
4. `bcrypt.CompareHashAndPassword(user.PasswordHash, password)` (work factor 12)
5. On failure: `IncrFailedAttempts`; if count reaches 10, set `locked_until = now() + 30 minutes`
6. On success: `ResetFailedAttempts`, generate 32-byte cryptographically random session token (`crypto/rand`), SHA-256 hash it, `AuthSessionRepo.Create`, return raw token + session metadata

**Session validation:**
- `ValidateSession`: hash the bearer token, call `AuthSessionRepo.GetByTokenHash`, check `revoked_at IS NULL` and `expires_at > now()`
- `RefreshSession`: validate + extend expiry by 30 days from now + update `last_used_at`

**API key issuance:**
- Generate a raw key: `otk_` prefix + 40 random base58 characters
- SHA-256 hash the raw key
- Extract 8-char prefix from raw key
- `APIKeyRepo.Create` with hash + prefix
- Return raw key once — never stored; caller must save it

**API key validation:**
- `ValidateAPIKey`: SHA-256 hash the raw key, `APIKeyRepo.GetByKeyHash`, check `revoked_at IS NULL` and `expires_at IS NULL OR expires_at > now()`, update `last_used_at`

**Rate limiting (in-memory):**
- `internal/ratelimit/` package
- IP-based counter: sliding window (15 minutes), max 20 attempts; `sync.Mutex`-protected map
- Account-based: tracked in DB (`failed_login_attempts` + `locked_until` columns on `human_user`)
- Rate limit state is lost on server restart (acceptable for in-memory implementation)

**Local auth mode** (`OTTERCAMP_AUTH_MODE=local`):
- Disables per-IP rate limiting
- Enables password-less auto-login: when a request arrives from `127.0.0.1` or `::1` with no credentials, auto-authenticate as the first admin user in the org

**Magic link** (stub for CLI/bootstrap use):
- `MagicLink(ctx, email)` generates a short-lived (15-minute) single-use token and returns it
- Full email delivery is out of scope; the token is returned in the API response for local use and printed by the CLI
- `ottercamp magic-link --email user@example.com` CLI command (wired to this service)

**CLI commands** (wired to `Service`):
- `ottercamp reset-password --user-id <id> --new-password <pw>` — calls `ResetPassword`
- `ottercamp unlock-account --user-id <id>` — calls `UnlockAccount`
- `ottercamp magic-link --email <email>` — calls `MagicLink`, prints the token

### Must NOT build
- HTTP handlers or middleware (task 007)
- RBAC enforcement beyond role storage (task 007)
- Audit event recording (task 008) — the service should accept an optional `AuditRecorder` interface parameter for later wiring

## Acceptance Criteria

- [ ] Correct password + active account → session token returned; `auth_session` row created with correct `token_hash`, `expires_at = now() + 30 days`
- [ ] Wrong password → `ErrInvalidCredentials` returned; `failed_login_attempts` incremented in DB
- [ ] 10 consecutive failed logins → `locked_until` set to `now() + 30 minutes`; subsequent login attempts return `ErrAccountLocked` even with correct password
- [ ] `UnlockAccount` resets `failed_login_attempts = 0` and `locked_until = NULL`
- [ ] `ValidateSession` with an expired session returns `ErrSessionExpired`; with a revoked session returns `ErrSessionRevoked`
- [ ] `RefreshSession` sets `expires_at` to exactly 30 days from the call time (verified with `clock.Fake`)
- [ ] API key raw value contains `otk_` prefix; `IssueAPIKey` returns the raw key; calling `IssueAPIKey` again with same name returns a different key each time
- [ ] `ValidateAPIKey` with the raw key from `IssueAPIKey` succeeds; with a garbage value returns `ErrInvalidAPIKey`
- [ ] Per-IP rate limit: 21st login attempt from the same IP within 15 minutes returns `ErrRateLimited`
- [ ] `OTTERCAMP_AUTH_MODE=local` + request from `127.0.0.1` with no credentials → auto-login returns the first org admin's session
- [ ] `ottercamp magic-link --email user@example.com` prints a token to stdout

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- Token generation: `crypto/rand` source; two calls never return the same token (probabilistic test with 1000 iterations)
- `bcrypt.CompareHashAndPassword` wired correctly: known password + precomputed hash succeeds; wrong password fails
- Rate limiter: test counter increment, window expiry (via `clock.Fake.Advance`), reset on window expiry
- `MagicLink`: token TTL enforced; second use of the same token returns `ErrTokenExpired`

**Integration tests:**
- Full login → validate → refresh → logout flow against real PostgreSQL (via `testdb.New(t)`)
- Failed login counter accumulation and lockout (10 attempts → locked)
- API key issuance → validation → revocation
- Session expiry: create session with `expires_at` in the past → `ValidateSession` returns `ErrSessionExpired`
- Local auth mode: set `OTTERCAMP_AUTH_MODE=local`, make a request from loopback, verify auto-login

**E2E tests:**
- None — covered by dedicated E2E task 082

## Implementer Notes

- Use `crypto/rand` for all token and key generation. `math/rand` is never acceptable for security-sensitive values.
- bcrypt work factor 12 is specified in doc 04. Do not use a lower value even in tests — use `bcrypt.MinCost` in test mode only if benchmarks show test suite slowdown. Gate via `OTTERCAMP_MODE=test` in the service constructor.
- The rate limiter's in-memory state resets on server restart. This is explicitly acceptable per the spec — persistent rate limiting is not required.
- `OTTERCAMP_DEFAULT_ORG_ID` is used in single-org mode (the standard deployment scenario). In multi-tenant managed mode, the org is determined from the subdomain or a request header — that routing is handled at the HTTP layer (task 007) and passed into the service via context.
- The `AuditRecorder` interface injection point: the `auth.Service` constructor accepts an optional `AuditRecorder`. Pass `nil` in task 006; task 008 will provide a real implementation. This avoids a circular dependency between auth and audit.
- Per the spec (doc 04): sessions are org-scoped. A user can have multiple active sessions (one per device/browser). `ListActive` returns all non-revoked, non-expired sessions for a user.
