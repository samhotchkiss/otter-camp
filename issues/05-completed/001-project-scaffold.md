# 001: Project Scaffold and Runtime Harness

| Field | Value |
|-------|-------|
| Layer | L0 |
| Size | S (≤1 day) |
| Spec refs | doc 08 §Deployment, doc 21 §Test Mode |
| Spec status | finished |
| Depends on | — |
| Blocks | 002, 003, 004, 005, 006, 007, 008, 009, 010, 011, 012 |

## Scope

Bootstrap the Go module, lay out the project directory structure, wire up configuration
loading from environment variables, provide an injectable clock abstraction, implement the
`OTTERCAMP_MODE` flag, set up structured logging, and create the entrypoints for the HTTP
server and background worker processes.

### Must build
- Go module (`go.mod`, `go.sum`) with initial dependency set (stdlib + logging library)
- Top-level directory structure:
  - `cmd/ottercamp/` — main binary entrypoint
  - `internal/config/` — config loading from env vars
  - `internal/clock/` — injectable clock interface and real/fake implementations
  - `internal/log/` — structured logger initialization (JSON in production, text in development)
  - `internal/server/` — HTTP server bootstrap (chi or stdlib mux stub)
  - `internal/worker/` — background worker entrypoint stub
  - `testdata/` — placeholder for test fixtures
- `OTTERCAMP_MODE` env var: `production` | `development` | `test`
- `OTTERCAMP_LOG_LEVEL` env var: `debug` | `info` | `warn` | `error`
- `OTTERCAMP_ADDR` env var (default: `:4110`)
- `ottercamp serve` CLI command: starts HTTP server, blocks until SIGINT/SIGTERM
- `ottercamp version` CLI command: prints build version (injected at link time via `-ldflags`)
- Graceful shutdown: on SIGINT/SIGTERM, stop accepting new requests, wait up to 30 seconds for in-flight to drain
- `Makefile` with targets: `build`, `test`, `lint`

### Must NOT build
- Database connection or migration logic (task 002)
- Any schema or domain tables (task 003+)
- Object storage (task 004)
- Authentication middleware (task 006)
- Any application routes beyond `/version` stub

## Acceptance Criteria

- [ ] `go build ./cmd/ottercamp` produces a binary without errors
- [ ] `ottercamp serve` starts an HTTP listener on the configured address and logs "server started" at info level
- [ ] `ottercamp version` prints the version string injected at build time (e.g. `dev` in local builds)
- [ ] `OTTERCAMP_MODE=test` is recognized and a `clock.Fake` implementation is returned by `clock.New()`; `OTTERCAMP_MODE=production` returns `clock.Real`
- [ ] `OTTERCAMP_MODE=unknown` causes startup to return a non-zero exit code with a clear error message
- [ ] Graceful shutdown: sending SIGINT to a running server causes it to exit with code 0 after draining in-flight requests (verified in test with a slow handler)
- [ ] `make test` runs all unit tests and passes (≥90% coverage on `internal/config`, `internal/clock`, `internal/log`)

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `internal/config`: test that all env vars are parsed, required vars absent → error, unknown mode → error, defaults applied correctly
- `internal/clock`: `Real.Now()` returns current time; `Fake.Now()` returns frozen time; `Fake.Advance(d)` moves the clock
- `internal/log`: logger initializes without panic; JSON output in production mode; text output in development mode

**Integration tests:**
- None at this layer — no external dependencies yet

**E2E tests:**
- None — covered by dedicated E2E task 081

## Implementer Notes

- The clock abstraction is used throughout the codebase wherever `time.Now()` would otherwise appear. Tests inject `clock.Fake` to control time deterministically (required for session expiry, retention, scheduler tests).
- `OTTERCAMP_MODE=test` enables the `POST /test/reset` endpoint (task 012) and switches certain components to deterministic/fake implementations (model gateway in task 035, clock, etc.).
- Version injection pattern: `go build -ldflags "-X main.version=$(git describe --tags --always)"`.
- Logging library choice: use `log/slog` (stdlib, Go 1.21+). Structured fields used consistently — no `fmt.Printf` in application code paths.
- Keep `internal/server` and `internal/worker` as stubs only; real wiring happens as tasks are completed.
