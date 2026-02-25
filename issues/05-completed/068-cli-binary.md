# 068: CLI Binary — Build, Packaging, and Command Suite

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 08 §CLICommands, doc 08 §TLSConfiguration, doc 11 §CLIExecution, doc 15 §MigrationCLI |
| Spec status | finished |
| Depends on | 001, 002, 005, 009, 006, 007, 063 |
| Blocks | 051, 065, 071, 081, 082, 089, 090 |

## Scope

Build the `ottercamp` CLI binary: noun-verb command structure; three output modes; auth
credential management; all server-side management commands (serve, migrate, bootstrap,
reset-password, magic-link, unlock-account, backup, restore, secret, health, version);
version injection at build time; cross-platform release build targets; and embedded
migration files.

### Must build

**Command structure** (`cmd/ottercamp/main.go`):

```
ottercamp <noun> <verb> [flags]
```

Top-level nouns:
- `server` — server process management
- `db` — database management
- `auth` — user auth management
- `secret` — secret management
- `backup` — backup/restore
- `health` — health checks
- `version` — version info
- `schedule` — schedule management (wired from task 065)
- `chat` — chat CLI (wired from task 051)

Global flags:
- `--server-url string` (default: `http://localhost:4110`; env: `OTTERCAMP_SERVER_URL`)
- `--api-key string` (env: `OTTERCAMP_API_KEY`; also reads `~/.ottercamp/credentials`)
- `--output string` — `table` (default) | `json` | `quiet`
- `--no-color` — disable ANSI color in table output

**Three output modes:**

`OutputFormatter`:
- `table` — ASCII table via `github.com/olekukonko/tablewriter` or similar; header row in bold
- `json` — raw JSON marshaling of the response; no envelope stripping
- `quiet` — prints only the primary identifier (ID or name) of the created/modified resource

Apply to all list and get commands. Create/update/delete commands print a confirmation
in `table` mode (`✓ Created project foo (id: abc123)`) or the ID alone in `quiet` mode.

**Auth credential management** (`internal/cli/credentials.go`):

`CredentialStore`:
- Location: `~/.ottercamp/credentials` (plain text INI-style, chmod 600 on creation):
  ```ini
  [default]
  server_url = http://localhost:4110
  api_key = oc_live_...
  ```
- `CredentialStore.Load() (Credentials, error)` — reads file; falls back to env vars.
- `CredentialStore.Save(creds Credentials) error` — writes file; sets permissions to 0600.
- `CredentialStore.Clear() error` — removes the file.

**Server commands:**

`ottercamp server start [flags]`:
- Starts the HTTP server + background workers in a single process.
- Flags: `--port int` (default 4110), `--worker-concurrency int` (default 4),
  `--tls-mode string` (none|manual|acme), `--tls-cert string`, `--tls-key string`,
  `--acme-domain string`, `--acme-email string`.
- TLS modes:
  - `none`: plain HTTP (default for dev/local).
  - `manual`: load cert/key from `--tls-cert` and `--tls-key` paths.
  - `acme`: use `golang.org/x/crypto/acme/autocert` with domain and email; stores
    certs in `~/.ottercamp/acme/` by default.
- Writes PID file to `~/.ottercamp/server.pid` on start.

`ottercamp server stop`:
- Reads PID file; sends SIGTERM.
- If process not found: exits with message "Server is not running".

**Database commands:**

`ottercamp db migrate [--dry-run] [--target N]`:
- Runs forward-only migrations via `MigrationRunner` (task 002).
- `--dry-run`: prints pending migrations without applying.
- `--target N`: run only up to migration N.
- Prints each migration as it is applied: `Applying 0042_memory_schema... done (12ms)`.

`ottercamp db status`:
- Prints table of all migrations: `#`, `File`, `Applied`, `Applied At`.

`ottercamp db reset [--force]`:
- Only available in `OTTERCAMP_MODE=test` or `OTTERCAMP_MODE=dev`.
- Drops and recreates the database; runs all migrations; runs bootstrap.
- `--force` bypasses the confirmation prompt.
- Returns non-zero exit code in production mode.

**Auth commands (admin side — server-side operations):**

`ottercamp auth reset-password --user <email> --new-password <password>`:
- Calls `POST /v1/admin/users/:id/reset-password` (admin-only API).
- Reads email; looks up user; resets hash.

`ottercamp auth magic-link --user <email>`:
- Calls `POST /v1/admin/users/:id/magic-link`.
- Prints the one-time login URL.

`ottercamp auth unlock-account --user <email>`:
- Calls `POST /v1/admin/users/:id/unlock`.
- Prints confirmation.

These admin API endpoints are added to the auth API router (task 007) as part of this task.
They require `org:admin` role. They are not in the public-facing API spec but are used
only via the CLI.

**Secret commands** (from task 009 — wire into CLI here):

`ottercamp secret set <slug> [--description "..."] [--from-file path]`:
- Prompts for value if not `--from-file`; calls `SecretService.Create` (task 009).

`ottercamp secret list [--json]`:
- Calls `GET /v1/secrets`; prints slug, description, key_version, created_at.

`ottercamp secret delete <slug> [--force]`:
- Calls `DELETE /v1/secrets/:slug`.
- Without `--force`: prints what would be deleted and prompts for confirmation.

**Backup and restore commands:**

`ottercamp backup --output <path> [--include-objects]`:
- Calls `pg_dump` for the database (requires `pg_dump` binary in PATH).
- If `--include-objects`: tarballs the object storage directory alongside the dump.
- Output is a `.tar.gz` containing `ottercamp_backup.sql` and optionally `objects/`.

`ottercamp restore --input <path> [--force]`:
- Extracts the tarball; restores SQL via `psql`.
- If `--include-objects` was used at backup time: restores the objects directory.
- `--force`: skips confirmation.

**Health command:**

`ottercamp health [--json]`:
- Calls `GET /health/ready`.
- Table mode: prints each check with pass/fail.
- JSON mode: raw response.
- Exits non-zero if any check fails.

**Version command:**

`ottercamp version [--json]`:
- Local mode (no server needed): prints embedded build-time version.
- `--json`: prints `{"version":"...","commit":"...","built_at":"...","go_version":"..."}`.

**Version injection at build time** (`internal/version/version.go`):

```go
package version

var (
    Version   = "dev"      // overridden by -ldflags
    Commit    = "unknown"
    BuiltAt   = "unknown"
    GoVersion = runtime.Version()
)
```

Build flags (in `Makefile` or `goreleaser.yaml`):
```
-ldflags "-X github.com/example/ottercamp/internal/version.Version=${VERSION} \
          -X github.com/example/ottercamp/internal/version.Commit=${COMMIT} \
          -X github.com/example/ottercamp/internal/version.BuiltAt=${BUILT_AT}"
```

**Embedded migration files** (`internal/migrations/`):

Use `//go:embed migrations/*.sql` to embed all migration SQL files into the binary.
The `MigrationRunner` (task 002) reads from the embedded FS, not the filesystem, when
`OTTERCAMP_MIGRATIONS_PATH` is not set. This allows the binary to be deployed without
the SQL files present on disk.

```go
//go:embed migrations
var MigrationsFS embed.FS
```

**Cross-platform build targets** (`goreleaser.yaml`):

Build targets:
- `linux/amd64`
- `linux/arm64`
- `darwin/arm64`
- `darwin/amd64`

Archives: `.tar.gz` for Linux/macOS; include `README.md` and `LICENSE`.

Checksum file: `checksums.txt` (SHA-256 for each archive).

CGO disabled (`CGO_ENABLED=0`) to produce fully static binaries.

Minimum `goreleaser` version: v2.

**Admin API endpoints** (added to task 007's router):

`POST /v1/admin/users/:id/reset-password` — body: `{new_password: string}` — requires `org:admin`
`POST /v1/admin/users/:id/magic-link` — returns `{magic_link_url: string}` — requires `org:admin`
`POST /v1/admin/users/:id/unlock` — clears `locked_until`, resets failed attempt counter — requires `org:admin`

Magic link implementation:
- Generate a one-time token (32 random bytes, base64url-encoded).
- Store in `auth_session` with `session_type='magic_link'` and `expires_at = now() + 1 hour`.
- URL format: `{server_url}/auth/magic?token={token}`.
- `GET /auth/magic?token=...` route: validates token, creates a full auth session, redirects to `/`.

### Must NOT build

- Domain-specific CLI commands beyond what's listed (those live in their domain tasks: 051 for chat, 065 for schedule)
- The HTTP server implementation itself (task 001)
- The migration runner logic (task 002)
- Secret encryption (task 009)
- Any new DB tables

## Acceptance Criteria

- [ ] `ottercamp version` prints version, commit, and build time without contacting a server
- [ ] `ottercamp db migrate --dry-run` lists pending migrations and exits 0 without applying them
- [ ] `ottercamp server start --port 4111` binds on port 4111; `ottercamp server stop` sends SIGTERM and exits 0
- [ ] `ottercamp secret list --output json` returns valid JSON; `--output table` returns ASCII table
- [ ] `ottercamp db reset` in production mode exits non-zero with an error message
- [ ] CLI binary for `linux/amd64` is fully static (no shared library dependencies)
- [ ] Migration SQL files are embedded in the binary (`embed.FS` reads correctly in unit test without disk files)
- [ ] `POST /v1/admin/users/:id/magic-link` returns a valid URL; `GET /auth/magic?token=...` creates a session and redirects

## Tests Required

**Unit tests:**
- `CredentialStore.Load`: env var overrides file; file with chmod 600 loads correctly; missing file → zero value (not error)
- `CredentialStore.Save`: written file has permission 0600
- `OutputFormatter`: table mode → headers + rows; json mode → raw JSON; quiet mode → IDs only
- Version package: confirm `Version`, `Commit`, `BuiltAt` are set by ldflags in build (use a build test that inspects the binary)

**Integration tests:**
- `ottercamp db migrate` against a real test DB: apply migrations; verify `schema_migrations` table updated; `--dry-run` makes no changes
- `ottercamp db status`: returns correct applied/pending counts
- Admin API: `POST /v1/admin/users/:id/magic-link` → URL returned; `GET /auth/magic?token=...` → session created
- Backup/restore: `backup` produces `.tar.gz`; `restore` applies SQL to empty DB; verify data present after restore

**E2E tests:**
- None — covered by E2E task 081 (bootstrap via CLI) and 082 (auth flow)
