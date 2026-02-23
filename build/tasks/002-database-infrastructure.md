# 002: Database Infrastructure

| Field | Value |
|-------|-------|
| Layer | L0 |
| Size | S (≤1 day) |
| Spec refs | doc 08 §Database, doc 15 §Migration Strategy, doc 21 §Test Harness |
| Spec status | finished |
| Depends on | 001 |
| Blocks | 003, 005, 008, 009, 010, 011, 012 |

## Scope

Establish the PostgreSQL connection pool, implement the migration runner, enable the pgvector
extension, create a db-per-org connection routing stub, and build the test harness database
clone setup used in all integration and E2E tests.

### Must build
- `internal/db/` package:
  - `db.Pool` — wraps `pgxpool.Pool`; constructed from `OTTERCAMP_DATABASE_URL` env var
  - Connection pool configuration: min 2 / max 20 connections, connect timeout 5s, acquire timeout 10s
  - `db.Pool.Ping(ctx)` health check
- `internal/migrate/` package:
  - Forward-only migration runner
  - Migrations stored as numbered SQL files: `migrations/NNNN_description.sql` (4-digit zero-padded sequence)
  - `schema_migrations` table (columns: `version integer primary key, applied_at timestamptz not null`)
  - On startup: acquire advisory lock (pg_advisory_lock), run all unapplied migrations in order, release lock
  - Transactional migrations: each migration file runs in a single transaction; failure rolls back that migration and halts
  - `ottercamp migrate` CLI command: runs pending migrations and exits
- pgvector extension: first migration (`0001_enable_pgvector.sql`) runs `CREATE EXTENSION IF NOT EXISTS vector`
- db-per-org connection routing stub:
  - `internal/db/router.go` — `Router` interface with `ConnFor(ctx, orgID uuid.UUID) (*pgxpool.Pool, error)`
  - Single-DB implementation (returns the shared pool for all orgs); multi-DB routing deferred
- Test harness:
  - `internal/testdb/` package
  - `testdb.New(t)` — creates a fresh named database by cloning a pre-migrated template DB; registers `t.Cleanup` to drop it
  - Template DB is created once per test binary run (sync.Once), migrations applied to template, then each test gets a `CREATE DATABASE ... TEMPLATE` clone
  - `OTTERCAMP_TEST_DATABASE_URL` env var for test DSN (defaults to `postgres://localhost/ottercamp_test_template`)

### Must NOT build
- Any application schema tables (task 003+)
- Multi-database routing implementation (deferred to managed-mode deployment)
- Any domain repositories

## Acceptance Criteria

- [ ] `ottercamp migrate` connects to the database, creates `schema_migrations` if absent, runs `0001_enable_pgvector.sql`, records the version, and exits 0
- [ ] Running `ottercamp migrate` a second time is a no-op (idempotent; no error if already applied)
- [ ] `vector` extension is queryable after migration: `SELECT typname FROM pg_type WHERE typname = 'vector'` returns a row
- [ ] Advisory lock prevents two concurrent `migrate` processes from racing (verified by spawning two goroutines against the same DB in a test)
- [ ] A migration file containing invalid SQL causes the runner to roll back that migration, log the error, and exit non-zero; previously applied migrations remain in `schema_migrations`
- [ ] `testdb.New(t)` returns a `*pgxpool.Pool` connected to an isolated test database; the database is dropped after the test completes
- [ ] `db.Pool.Ping(ctx)` returns nil when the database is reachable and returns an error within 5 seconds when it is not

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `internal/migrate`: test version tracking logic (which migrations are pending, already applied, order enforcement) using a mock DB interface
- `internal/db`: test pool construction validates required env var; error on missing `OTTERCAMP_DATABASE_URL`

**Integration tests:**
- Migration runner against real PostgreSQL: apply migrations in order → verify `schema_migrations` rows; apply again → no-op; introduce a bad migration → rollback and halt
- Advisory lock race: two goroutines both call the migration runner concurrently → exactly one proceeds, the other waits or returns an error (no double-apply)
- `testdb.New(t)`: creates isolated DB, runs a `SELECT 1`, verifies DB is dropped after test cleanup

**E2E tests:**
- None — covered by dedicated E2E task 081

## Implementer Notes

- Use `pgx/v5` and `pgxpool` directly. Do not introduce an ORM.
- Migration files live at `migrations/` at the repo root. The migration runner embeds them via `//go:embed migrations/*.sql`.
- The `schema_migrations` table uses an integer version (the 4-digit prefix of the filename), not a hash. This makes ordering unambiguous.
- The `testdb` template clone pattern is critical for fast integration tests. Creating a new database from a template (which already has all migrations applied) takes ~50ms vs running migrations from scratch per test. The template is created once; each test gets a clone.
- db-per-org routing stub: the `Router` interface is introduced now so all repository constructors accept a `Router` instead of a raw pool. The single-DB implementation satisfies the interface. This allows managed multi-tenant mode to swap in a real routing implementation without changing repository code.
- Connection pool sizing: adjust via `OTTERCAMP_DB_MAX_CONNS` env var (default 20).
