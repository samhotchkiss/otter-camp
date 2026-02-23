# 070: Web UI Static File Serving and SPA Infrastructure

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 19 §WebUI, doc 12 §StaticServing, doc 19 §ViewStatePersistence |
| Spec status | finished |
| Depends on | 001, 063, 067 |
| Blocks | 089, 090 |

## Scope

Build the web UI static file serving infrastructure: the Go middleware that serves a
React+TypeScript SPA from the same process as the API server; build pipeline integration;
embedded or directory-based asset serving; SPA fallback routing; view state persistence
contract; and the `Cmd-K` search endpoint alias.

This task builds the **serving infrastructure only** — no UI assets, no React components,
no CSS. The web front-end is a separate project. This task provides the Go-side server
that will host whatever assets the front-end build produces.

### Must build

**Static file serving middleware** (`internal/web/static_handler.go`):

`StaticFileServer` serves a compiled React SPA from the API process.

Two serving modes, selected at startup:

**Mode 1: Embedded assets** (`OTTERCAMP_WEB_MODE=embedded`, default for release builds):
- The front-end build output (`dist/`) is embedded into the binary using `//go:embed web/dist`.
- Used in production releases where the binary ships with the UI baked in.
- If the `web/dist` directory does not exist at compile time: the build succeeds but the
  embedded FS is empty (the server serves 404 for all asset requests, with a developer hint
  response body `{"error":"web_assets_not_built","hint":"Run 'make build-web' first"}`).

**Mode 2: Directory serving** (`OTTERCAMP_WEB_MODE=directory`, for development):
- `OTTERCAMP_WEB_DIR` env var points to the front-end build output directory (default: `./web/dist`).
- Files are served directly from disk; no caching (allows hot reload during development).
- Used with `vite build --watch` or a dev proxy during front-end development.

```go
//go:embed web/dist
var webAssets embed.FS

type StaticFileServer struct {
    fs   http.FileSystem  // either embed.FS or os.DirFS
    mode string           // "embedded" | "directory"
}
```

**Route mounting** (in `internal/api/router.go`):

Asset routes (all mounted at `/`):
- `/assets/*` → static files with long-lived cache headers (`Cache-Control: public, max-age=31536000, immutable`) — these are content-hashed by the build tool
- `/favicon.ico` → direct file serve
- `/*` (catch-all) → SPA fallback (see below)

API routes take priority. The order of route registration:
1. `/metrics` (Prometheus — task 063)
2. `/health*` (health endpoints — task 063)
3. `/auth/magic` (magic link — task 068)
4. `/v1/*` (all API routes)
5. `/assets/*` (static assets — this task)
6. `/*` (SPA fallback — this task)

**SPA fallback routing** (`internal/web/spa_handler.go`):

For any request that:
- Does NOT match a known `/v1/` API route
- Does NOT match a static asset (`/assets/`, `/favicon.ico`)
- Is NOT a health/metrics endpoint

→ Serve `web/dist/index.html` with `Cache-Control: no-cache, no-store` (the index file
  must not be cached; only content-hashed assets get long-lived cache headers).

This enables client-side routing: the SPA handles all navigation within `/*`.

Request to `/v1/nonexistent-route` still returns a 404 JSON error from the API layer
(the SPA fallback does not apply to `/v1/` paths).

**Cache headers policy:**

| Path pattern | Cache-Control |
|---|---|
| `/assets/*` | `public, max-age=31536000, immutable` |
| `/favicon.ico` | `public, max-age=86400` |
| `index.html` (SPA fallback) | `no-cache, no-store` |
| `/v1/*` (API) | `no-store` |

**Security headers** (add to the static serving middleware):
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: SAMEORIGIN`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Content-Security-Policy: default-src 'self'; script-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self' 'unsafe-inline'`
  - Note: `unsafe-inline` for styles is a pragmatic choice for initial deployment; tighten
    once the front-end build produces hashed style references.

These headers are applied to all responses (API and static).

**Build pipeline integration** (`Makefile`):

```makefile
.PHONY: build-web build build-all

build-web:
	cd web && npm ci && npm run build

build: build-web
	go build -ldflags "..." -o bin/ottercamp ./cmd/ottercamp

build-all: build-web
	goreleaser build --snapshot --clean
```

The `make build` target always builds the web UI first so the embedded FS is populated
before the Go binary is compiled.

For CI: add a `web/dist/.gitkeep` file so the `//go:embed web/dist` directive does not
fail when the front-end has not been built yet (the embed includes the gitkeep file;
the empty FS triggers the "not built" hint response).

**View state persistence contract:**

Doc 19 states: "view state persistence is local storage only — no server-side view state."

Implement the contract as a comment in `internal/web/static_handler.go`:
```go
// ViewStatePersistenceContract: All UI view state (selected filters, column widths,
// panel open/closed state, etc.) MUST be persisted in the browser's localStorage only.
// The API server will never store or return view state. This is a hard design constraint.
// Any request from the front-end team to add view state to the API should be rejected
// and redirected to use localStorage instead.
```

No code is needed for this — it is an architectural boundary enforced by convention.

**`GET /v1/search` alias for Cmd-K:**

The `GET /v1/search` endpoint is implemented in task 067. This task ensures the route
registration order in the router places `/v1/search` correctly (before the SPA catch-all)
and adds the search endpoint to the OpenAPI stub (if one exists).

Add a developer note in `internal/api/router.go`:
```go
// GET /v1/search is the Cmd-K global search endpoint.
// It is registered explicitly here (before the SPA fallback) to prevent the SPA
// handler from intercepting search requests.
```

**Development proxy configuration stub** (`web/vite.config.ts.example`):

Create a non-functional example file showing how the front-end development server should
proxy API requests to the Go backend:

```typescript
// Example vite.config.ts for development
// Copy to web/vite.config.ts and adjust as needed
export default defineConfig({
  server: {
    proxy: {
      '/v1': 'http://localhost:4110',
      '/health': 'http://localhost:4110',
    }
  }
})
```

This is documentation only; the file is committed as `.example` and not executed.

### Must NOT build

- Any React components, TypeScript source, CSS, or front-end tooling
- The actual web UI (separate project)
- API endpoint implementations (those are in their respective domain tasks)
- Content Security Policy for a specific front-end framework (the stub CSP here is intentionally broad)
- WebSocket or SSE infrastructure (task 047)

## Acceptance Criteria

- [ ] In embedded mode, `GET /` returns the contents of `web/dist/index.html` with `Cache-Control: no-cache, no-store`
- [ ] In embedded mode with no assets built (empty `web/dist`), `GET /` returns 404 with `{"error":"web_assets_not_built"}` hint
- [ ] `GET /assets/main.abc123.js` returns `Cache-Control: public, max-age=31536000, immutable`
- [ ] `GET /v1/nonexistent` returns JSON `{"error":{...}}` (not the SPA index.html)
- [ ] `GET /some/spa/route` returns `index.html` (SPA fallback applied)
- [ ] All responses include `X-Content-Type-Options: nosniff` and `X-Frame-Options: SAMEORIGIN` headers
- [ ] `make build-web` target exists and documents how to build web assets
- [ ] Directory mode serves files from `OTTERCAMP_WEB_DIR` without caching

## Tests Required

**Unit tests:**
- SPA fallback routing: request to `/dashboard/projects` → `index.html` served; request to `/v1/projects` → passes to API handler (returns 200 or 401, not index.html)
- Asset cache headers: request to `/assets/app.abc.js` → `Cache-Control: public, max-age=31536000, immutable`; request to `/` → `Cache-Control: no-cache, no-store`
- Security headers: any response includes all required security headers
- Empty embedded FS: mock empty `web/dist`; `GET /` → 404 with hint message

**Integration tests:**
- Embedded mode: build test binary with a stub `web/dist/index.html`; `GET /` returns it; `GET /v1/version` returns JSON (API takes priority)
- Directory mode: set `OTTERCAMP_WEB_DIR` to a temp dir with a stub `index.html`; `GET /` returns it

**E2E tests:**
- None — the web serving infrastructure is validated by the CI build test that confirms the
  binary serves `/` correctly. Full browser E2E is deferred to L5 task 088.

## Implementer Notes

**`web/dist/.gitkeep`:** Commit this file to prevent the `//go:embed web/dist` directive
from failing when the `web/` directory has no built assets. The presence of `.gitkeep`
makes the embed succeed with a near-empty FS; the empty FS detection logic then returns
the "not built" hint response for `GET /`.

**Route registration order is critical.** The SPA catch-all `/*` must be the LAST route
registered. Any route registered after it will be shadowed. The recommended pattern is to
use a router that processes routes in declaration order and explicitly test that `/v1/`
routes are not shadowed by running the test suite with the catch-all active.

**`embed.FS` path prefix:** The embedded FS path includes the `web/dist/` prefix. When
serving, strip the `web/dist` prefix so that `GET /index.html` resolves to
`web/dist/index.html` in the embedded FS. Use `fs.Sub(webAssets, "web/dist")`.
