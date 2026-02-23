# 005: Auth Schema — Users, Sessions, API Keys

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | S (≤1 day) |
| Spec refs | doc 04 §HumanUser, doc 04 §AuthSession, doc 04 §APIKey |
| Spec status | finished |
| Depends on | 002, 003 |
| Blocks | 006, 007, 008, 012 |

## Scope

Create DDL migrations and repository layers for the three authentication tables:
`human_user`, `auth_session`, and `api_key`. This task covers schema and data access only —
business logic (login, session creation, rate limiting) is in task 006.

### Must build

**Migrations:**
- `0007_human_user.sql`
- `0008_auth_session.sql`
- `0009_api_key.sql`

**`human_user` table** (doc 04):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `email text not null`
- `display_name text not null`
- `password_hash text` — bcrypt hash (work factor 12); null when password not set (magic-link only users)
- `role text not null check (role in ('admin', 'member'))` — org-level role
- `is_active boolean not null default true`
- `failed_login_attempts integer not null default 0`
- `locked_until timestamptz` — null = not locked; set to future timestamp on 10 failed attempts (30-min lockout)
- `last_login_at timestamptz`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique constraint: `(organization_id, email)`

**`auth_session` table** (doc 04):
- `id uuid primary key default gen_random_uuid()`
- `user_id uuid not null references human_user(id) on delete cascade`
- `token_hash text not null unique` — SHA-256 of the bearer token (token itself never stored)
- `expires_at timestamptz not null` — 30 days from creation; refreshed on each use (sliding window)
- `created_at timestamptz not null default now()`
- `last_used_at timestamptz not null default now()`
- `revoked_at timestamptz` — null = active; set on explicit logout
- `user_agent text`
- `ip_address text`
- Index: `(user_id)` for listing sessions per user
- Index: `(expires_at)` for cleanup job

**`api_key` table** (doc 04):
- `id uuid primary key default gen_random_uuid()`
- `user_id uuid not null references human_user(id) on delete cascade`
- `key_hash text not null unique` — SHA-256 of the raw key (key itself never stored after creation)
- `key_prefix text not null` — first 8 chars of the raw key (for display/identification only)
- `display_name text not null`
- `scopes text[] not null default '{}'` — e.g. `{read,write,admin}`
- `created_at timestamptz not null default now()`
- `last_used_at timestamptz`
- `expires_at timestamptz` — null = no expiry
- `revoked_at timestamptz` — null = active

**Repository layer:**
- `HumanUserRepo`: `Create`, `GetByID`, `GetByEmail`, `List`, `Update`, `SetActive`, `IncrFailedAttempts`, `ResetFailedAttempts`, `SetLockedUntil`
- `AuthSessionRepo`: `Create`, `GetByTokenHash`, `Revoke`, `RevokeAll` (for user), `TouchLastUsed`, `ExtendExpiry`, `ListActive`, `DeleteExpired`
- `APIKeyRepo`: `Create`, `GetByKeyHash`, `ListByUser`, `Revoke`

All repos accept `context.Context`. Token hashing (`sha256.Sum256`) is done at the service layer (task 006), not in the repo.

### Must NOT build
- Login logic, session creation, rate limiting (task 006)
- Auth middleware or HTTP handlers (task 007)
- Audit event recording (task 008)

## Acceptance Criteria

- [ ] Migrations `0007` through `0009` apply cleanly in sequence; idempotent re-run
- [ ] `human_user` table: `(organization_id, email)` unique constraint; `role` check constraint rejects values other than `admin` and `member`
- [ ] `auth_session.token_hash` unique constraint; attempting to insert a duplicate hash raises a constraint violation
- [ ] `api_key.key_hash` unique constraint; `api_key.key_prefix` is NOT the full key (verified in test: prefix length ≤ 8 chars)
- [ ] `AuthSessionRepo.GetByTokenHash` returns the session with user eagerly loaded (single JOIN query, not N+1)
- [ ] `AuthSessionRepo.DeleteExpired` removes rows where `expires_at < now()` and `revoked_at IS NULL`; leaves active sessions untouched
- [ ] `HumanUserRepo.IncrFailedAttempts` atomically increments the counter (uses `UPDATE ... SET failed_login_attempts = failed_login_attempts + 1`)
- [ ] All foreign key cascade behaviors verified: deleting an organization cascades to `human_user`, which cascades to `auth_session` and `api_key`

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `HumanUserRepo`: verify that `GetByEmail` returns `ErrNotFound` for an unknown email; verify `Update` does not overwrite `password_hash` unless explicitly provided
- `AuthSessionRepo`: verify `GetByTokenHash` returns an error for a revoked session (or caller must check `revoked_at` — document which approach is used)

**Integration tests:**
- All three repos against real PostgreSQL (via `testdb.New(t)`):
  - `human_user` CRUD, unique constraint violation on duplicate `(org_id, email)`
  - `auth_session` create → touch → extend expiry → revoke → verify GetByTokenHash returns revoked session with `revoked_at` set
  - `api_key` create → list → revoke → verify `revoked_at` is set
  - FK cascade: delete org → verify `human_user`, `auth_session`, `api_key` rows are all gone
  - `DeleteExpired`: seed 3 sessions (1 expired, 1 active, 1 revoked-but-not-expired) → call DeleteExpired → verify only the expired row is removed

**E2E tests:**
- None — covered by dedicated E2E tasks 082 and 081

## Implementer Notes

- `password_hash` stores the full bcrypt output string (60 bytes for cost=12). The `$2a$12$...` prefix is preserved. Comparison is done via `bcrypt.CompareHashAndPassword` in the service layer.
- `token_hash` and `key_hash` store `hex.EncodeToString(sha256Hash[:])` — a 64-character hex string. This is consistent with `SELECT length(token_hash) = 64` being a valid sanity check.
- `key_prefix` is derived from the raw key before hashing: first 8 characters. Example: raw key `otk_AbCdEfGhIjKlMnOpQrStUvWxYz0123456789` → prefix `otk_AbCd`. The prefix is shown to users to identify which key is being used/revoked without revealing the full key.
- The sliding 30-day session window: `ExtendExpiry` sets `expires_at = now() + 30 days` on every authenticated request. This is called by the auth middleware (task 007), not the service layer. `TouchLastUsed` updates `last_used_at` in the same operation.
- No partial migration rollback is needed in the auth schema — each file is a single atomic DDL block.
