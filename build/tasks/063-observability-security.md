# 063: Observability, Security Hardening, and Retention Enforcement

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 13 §StructuredLogging, doc 13 §SecretScrubbing, doc 13 §TraceSpan, doc 13 §RetentionPolicy, doc 13 §SecurityModel, doc 08 §HealthEndpoints |
| Spec status | finished |
| Depends on | 001, 002, 003, 007, 024, 052, 036, 062 |
| Blocks | 054, 067, 089 |

## Scope

Build the observability infrastructure: `trace_span` schema stub; structured logging
conventions and request_id middleware; Prometheus metrics collection stubs; health
endpoints; the 6-layer security model (input validation, output sanitization, secret leak
detection, rate limiting, CORS); audit completeness verification; and the daily retention
enforcement job.

### Must build

**`trace_span` table migration** (`0076_trace_span.sql`):

```sql
CREATE TABLE trace_span (
    id          uuid        primary key default gen_random_uuid(),
    trace_id    uuid        not null,
    parent_id   uuid,                          -- null for root spans
    organization_id uuid    references organization(id) on delete cascade,
    span_name   text        not null,
    service     text        not null default 'ottercamp',
    kind        text        not null check (kind in ('internal','server','client','producer','consumer')),
    status      text        not null check (status in ('unset','ok','error')),
    -- OTLP-compatible attribute bag:
    attributes  jsonb       not null default '{}',
    events      jsonb       not null default '[]',
    -- Timing:
    started_at  timestamptz not null,
    ended_at    timestamptz,
    duration_ms integer,
    -- Append-only; partitioned by day
    created_at  timestamptz not null default now()
) PARTITION BY RANGE (created_at);
```

Create the first partition and a daily partition creation job:
- Initial partition: `trace_span_p_default` covering `[now(), now() + 7 days)`.
- Partition creation: background job `trace_span_partition_create` runs daily to pre-create
  the next 7-day window.
- No update or delete on `trace_span` rows (append-only).
- Retention: rows older than 7 days are dropped by detaching and dropping old partitions in
  the retention job (see below).
- Index per partition: `(trace_id)`, `(organization_id, created_at)`.

**Structured logging conventions** (`internal/logging/logger.go`):

OtterCamp uses structured JSON logging via the standard library `log/slog` package.

Required fields on every log entry:
- `request_id` — injected by middleware (see below)
- `organization_id` — injected when org context is available
- `service` — always `"ottercamp"`
- `env` — `OTTERCAMP_MODE` value

`Logger.WithRequestID(ctx, requestID) context.Context`:
- Stores request ID in context.

`Logger.WithOrgID(ctx, orgID) context.Context`:
- Stores org ID in context.

`Logger.FromContext(ctx) *slog.Logger`:
- Returns a `*slog.Logger` pre-loaded with `request_id`, `organization_id`, `service`, `env`
  fields from context.

**5 logging invariants** (enforced by `SecretScrubber`; see below):
1. Secrets never appear in log output.
2. Model prompt/response content never appears in log output (truncate at 200 chars; add `[truncated]`).
3. API response bodies never logged verbatim (log status code + byte count only).
4. Audit event content never duplicated in logs (audit events are already structured).
5. Memory content never logged verbatim (log memory ID and score only).

**Request ID middleware** (`internal/middleware/request_id.go`):

HTTP middleware added to the router (all routes):
- On each request: generate `request_id = ulid.Make().String()` (ULID, sortable, not UUID).
- Set `X-Request-ID` response header.
- If `X-Request-ID` header is present in the incoming request, use it as the request ID
  (for tracing through proxies).
- Inject into context via `Logger.WithRequestID`.
- Log entry at request start: `{level:INFO, msg:"request_started", method, path, remote_addr, request_id}`.
- Log entry at request end: `{level:INFO, msg:"request_completed", status_code, duration_ms, bytes_written, request_id}`.

**Secret scrubbing layer** (`internal/security/scrubber.go`):

`SecretScrubber.Scrub(input string) string`:
- Replaces known secret patterns with `[REDACTED]`:
  - Lines/fields matching `sk-[A-Za-z0-9]{20,}` (OpenAI-style API keys)
  - Lines/fields matching `ANTHROPIC_API_KEY=.+`
  - Fields matching known environment variable names (from the CLI execution denylist in task 058)
  - Bearer tokens in Authorization headers: `Bearer [A-Za-z0-9._-]{20,}` → `Bearer [REDACTED]`
  - JWT-shaped strings: `eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+` → `[JWT_REDACTED]`
  - Secret slugs: scan for patterns matching `$secret.slug_name` in jsonb/text fields
- Applied to: all structured log values, all `domain_event.payload` strings before storage,
  all `audit_event.metadata` strings before storage.
- NOT applied to: prompt/response content in object storage (that is controlled by the org
  redaction policy in task 036).

`SecretScrubber.ScrubMap(m map[string]any) map[string]any`:
- Recursively applies `Scrub` to all string values in the map.
- Used by log middleware to scrub `slog.Attr` values before emission.

**Prometheus metrics stubs** (`internal/metrics/metrics.go`):

Register these metrics at startup (stubs — actual values populated by callers):

```
ottercamp_http_requests_total{method, path, status_code}    counter
ottercamp_http_request_duration_seconds{method, path}       histogram
ottercamp_model_invocations_total{provider, purpose, status} counter
ottercamp_model_tokens_total{provider, direction}           counter
ottercamp_run_created_total{domain, principal_type}         counter
ottercamp_run_duration_seconds{domain, status}              histogram
ottercamp_job_queue_depth{priority}                         gauge
ottercamp_active_browser_sessions                           gauge
ottercamp_active_runs                                       gauge
ottercamp_memory_extraction_total{stage, outcome}           counter
```

Expose at `GET /metrics` (Prometheus pull endpoint, not under `/v1/`).

Callers instrument these metrics in their respective packages using
`metrics.HTTPRequestsTotal.With(labels).Inc()` etc.

**Health endpoints** (`internal/health/handler.go`):

Implement BOTH path variants to resolve ISSUE #22:

`GET /health/live` (also aliased `GET /health`):
- Returns 200 `{"status":"ok"}` immediately. No dependency checks.
- Used by load balancers / Docker healthcheck.

`GET /health/ready`:
- Checks:
  1. PostgreSQL connectivity: `SELECT 1`.
  2. pgvector extension: `SELECT 1 FROM pg_extension WHERE extname='vector'`.
  3. Object storage: ping/head-object on a sentinel key.
  4. Pending migrations: `SELECT COUNT(*) FROM schema_migrations WHERE applied=false`.
- Returns 200 if all pass; 503 with `{"status":"degraded","checks":{...}}` if any fail.
- Timeouts: each check has a 2-second deadline.

> ✅ ISSUE #22 (RESOLVED): Canonical paths: `GET /health/live` (liveness) aliased as `GET /health`; `GET /health/ready` (readiness) aliased as `GET /ready`. Health endpoints are NOT under `/v1/`. Doc 08 §Health Checks updated. Implement all four paths: canonical `/health/live` and `/health/ready` plus their aliases.

**6-layer security model implementation** (`internal/security/`):

Layer 1 — Input validation (`internal/security/input_validator.go`):
- `InputValidator.ValidateRequest(r *http.Request) error`:
  - Max request body size: 10 MB (reject with 413 before reading body).
  - JSON Content-Type required for all POST/PATCH/PUT routes.
  - Reject null bytes in any string field after JSON decode.
  - Reject control characters (U+0000–U+001F except whitespace) in human-visible fields.
- Applied as middleware after the request ID middleware.

Layer 2 — Output sanitization (`internal/security/output_sanitizer.go`):
- `OutputSanitizer.SanitizeResponse(body []byte) []byte`:
  - Applies `SecretScrubber.Scrub` to the JSON-encoded response body.
  - Strips internal stack traces from error responses (replace with `request_id` only).
- Applied in the response writer middleware before sending.

Layer 3 — Secret leak detection (part of `SecretScrubber` above):
- Already defined. Enforce by wrapping the `slog` handler with a scrubbing handler.

Layer 4 — Rate limiting (`internal/security/rate_limiter.go`):
- In-memory token bucket rate limiter (no Redis dependency).
- Two limiter instances:
  - Per-IP: 100 requests/minute, burst 20.
  - Per-API-key: 1000 requests/minute, burst 100.
- `RateLimiter.Allow(key string) bool`:
  - `key` = `"ip:" + remoteAddr` or `"key:" + apiKeyID`.
  - Uses `golang.org/x/time/rate.Limiter`.
- On rate limit: return 429 `{"error":"rate_limit_exceeded","retry_after":N}` with
  `Retry-After: N` header.
- Apply as middleware; per-IP check runs before auth; per-API-key check runs after auth.

Layer 5 — CORS (`internal/security/cors.go`):
- `CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler`:
  - `allowedOrigins` from config: `OTTERCAMP_CORS_ORIGINS` env var (comma-separated; default `["http://localhost:4110"]`).
  - Allowed headers: `Content-Type`, `Authorization`, `X-Request-ID`, `Idempotency-Key`, `Last-Event-ID`.
  - Allowed methods: GET, POST, PUT, PATCH, DELETE, OPTIONS.
  - Credentials: true.
  - Preflight cache: 1 hour.
  - If origin not in list: request proceeds but no `Access-Control-Allow-Origin` header is set (not rejected at CORS layer — host-based trust model documented in deployment guide).

Layer 6 — Audit completeness verification (`internal/security/audit_verifier.go`):
- `AuditVerifier.VerifyCompleteness(ctx, orgID, since time.Time) AuditReport`:
  - Checks that all state-changing API calls in the request log have a corresponding `audit_event` row within 5 seconds.
  - Uses `trace_span` query (if available) or falls back to structured log correlation by `request_id`.
  - Returns `AuditReport{Gaps []string, CheckedCount int, GapCount int}`.
- This is a diagnostic tool, not a hot-path enforcer. Called from the control plane health endpoint and integration tests.

**Retention enforcement job** (`internal/jobs/retention_job.go`):

Job type: `retention_enforce` (registered in job queue from task 024).
Triggered daily via a 24-hour background ticker (not `task_schedule`).

Retention policy table (execute each as a separate DELETE/DROP in its own transaction):

| Table | Retention | Action |
|-------|-----------|--------|
| `chat_message` | 90 days | `DELETE WHERE created_at < now() - interval '90 days'` |
| `chat_turn` | 90 days | `DELETE WHERE created_at < now() - interval '90 days'` |
| `run` | 90 days | `DELETE WHERE created_at < now() - interval '90 days'` |
| `model_invocation` | 90 days (see note) | `DELETE WHERE created_at < now() - interval '90 days'` |
| `domain_event` | 90 days | `DELETE WHERE created_at < now() - interval '90 days'` |
| `audit_event` | 1 year | `DELETE WHERE created_at < now() - interval '1 year'` |
| `memory` (archived) | 1 year | `DELETE WHERE superseded_by IS NOT NULL AND created_at < now() - interval '1 year'` |
| `run_artifact` | 90 days | `DELETE WHERE created_at < now() - interval '90 days'` (also delete object storage file) |
| `trace_span` | 7 days | Detach + DROP old partitions older than 7 days |

> ✅ ISSUE #21 (RESOLVED): `model_invocation` retention is **90 days**. Doc 13 updated to match. `RetentionModelInvocationDays = 90` is correct — no confirmation needed.

Emits `domain_event(event_type='system.retention.completed', payload={tables_processed, rows_deleted})` on success.

### Must NOT build

- Model invocation DDL, object storage, or model gateway (tasks 035, 036)
- Run service or control plane state machine (task 052, 053)
- Auth middleware or rate limiting for auth endpoints (tasks 006, 007) — this task adds API-layer rate limiting at the router level; auth-specific limits remain in task 006
- SSE infrastructure (task 047)
- Any new DB tables beyond `trace_span`

## Acceptance Criteria

- [ ] `GET /health/live` returns 200 immediately with no DB calls; `GET /health` aliases to the same handler
- [ ] `GET /health/ready` returns 503 when the DB is unreachable
- [ ] Every HTTP response includes an `X-Request-ID` header
- [ ] `SecretScrubber.Scrub` replaces `sk-abc123def456ghi789jkl` with `[REDACTED]` and leaves `normal text` unchanged
- [ ] Per-IP rate limiter allows 100 requests then returns 429 with `Retry-After` header; token bucket refills after 1 minute
- [ ] CORS preflight returns correct `Access-Control-Allow-Origin` for a configured origin; request from an unconfigured origin proceeds without the header
- [ ] `RetentionJob.Run` deletes `domain_event` rows older than 90 days and leaves newer ones untouched
- [ ] `trace_span` table accepts insert; attempt to update existing row fails (enforce append-only in service layer)

## Tests Required

**Unit tests:**
- `SecretScrubber.Scrub`: 6 cases — OpenAI key, Anthropic key, Bearer token, JWT, env var line, clean string
- `SecretScrubber.ScrubMap`: nested map with secret value deep in hierarchy → value replaced
- `RateLimiter.Allow`: 100 allows → 101st returns false; wait refill period → allows again
- `InputValidator.ValidateRequest`: body > 10 MB → 413; null byte in JSON string field → error; clean request → nil
- `AuditVerifier.VerifyCompleteness`: mock with 2 state changes + 1 audit event → gap count = 1
- Retention constants: verify `RetentionModelInvocationDays == 90` (document the ISSUE #21 expectation)

**Integration tests:**
- Request ID round-trip: make HTTP request; verify `X-Request-ID` in response; verify request log entry contains same ID
- `trace_span` insert: insert row; verify partition routing; verify `SELECT` returns row; verify `DELETE` via retention job removes rows older than 7 days
- Health check: `GET /health/ready` with good DB → 200; mock DB failure → 503 with `checks.db=false`
- Retention job: insert `domain_event` rows at `now() - 91 days` and `now() - 1 day`; run job; verify old row deleted, new row present

**E2E tests:**
- None — covered by dedicated E2E task 089
