# 021: MCP Circuit Breaker and Health

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | M (1–2 days) |
| Spec refs | doc 09 §CircuitBreaker, doc 09 §HealthCheck, doc 09 §Timeouts, doc 09 §RetryPolicy, doc 09 §MCPDiscover |
| Spec status | finished |
| Depends on | 020, 024 |
| Blocks | 022, 049, 055 |

## Scope

Implement the MCP circuit breaker per connection, the health check scheduler, real transport
adapters (stdio, http, sse), and the `mcp.discover` native utility tool stub. Builds on
`MCPService` from task 020.

### Must build

**`internal/mcp/circuit_breaker.go`:**

Circuit breaker per `mcp_connection`. States: `closed` (normal), `open` (failing), `half-open` (testing recovery).

```go
type CircuitBreaker struct {
    connectionID  uuid.UUID
    state         CBState  // closed, open, half-open
    failureCount  int
    successCount  int
    lastFailureAt time.Time
    openedAt      time.Time
}

type CBState string
const (
    CBClosed   CBState = "closed"
    CBOpen     CBState = "open"
    CBHalfOpen CBState = "half-open"
)
```

**Circuit breaker state machine:**
- `closed → open`: after `failure_threshold` consecutive failures (configurable, default: 5); set connection status to `degraded` in DB
- `open → half-open`: after `recovery_timeout` has elapsed (configurable, default: 60 seconds); allow a single probe call through
- `half-open → closed` (recovery): probe call succeeds; reset failure count; set connection status to `active`
- `half-open → open` (re-open): probe call fails; increment open timer; set connection status to `degraded`
- `open/half-open`: calls return `ErrCircuitOpen` immediately without attempting the transport
- After `max_consecutive_opens` (configurable, default: 3): set connection status to `failed`; stop opening to half-open (circuit stays open permanently until manual re-enable)

**Three distinct timeouts** (doc 09):
- `per_call_timeout` — maximum duration for a single MCP tool call (default: 30s)
- `health_check_timeout` — maximum duration for a health check probe (default: 10s)
- `recovery_timeout` — time to wait in `open` state before trying half-open probe (default: 60s)

**Retry policy** (doc 09):
- Transient failures (timeout, network error): retry up to 3 times with exponential backoff (base 100ms, multiplier 2, jitter ±20%)
- Write tools: do NOT retry by default (idempotency concerns); retry only if the tool explicitly declares `safe_to_retry: true` in its catalog metadata
- Read tools and `resources/read`: retry up to 3 times
- Circuit breaker failure count increments on non-retryable final failures only (retried-and-succeeded = success)

**Health check scheduler:**
- Background goroutine; check interval configurable (default: 30s for active connections, 120s for degraded)
- For each enabled connection: call `MCPTransport.HealthCheck()` (a lightweight ping — implementation varies by transport)
- On success: update `last_healthy_at`, reset circuit breaker failure count
- On failure: record failure in circuit breaker; may trigger state transition
- Uses `health_check_timeout` per probe
- Scheduler is registered as a worker in the main server lifecycle (start on serve, stop on shutdown)

**Real transport adapters:**
- `StdioTransport`: spawns subprocess, communicates via stdin/stdout JSON-RPC; health check = send a `ping` message
- `HTTPTransport`: HTTP POST to endpoint; health check = `GET /health` or `POST tools/list` with empty filter
- `SSETransport`: connects to SSE endpoint; health check = establish connection + receive first event
- Each adapter implements:
  ```go
  type MCPTransport interface {
      Call(ctx context.Context, tool string, input json.RawMessage) (json.RawMessage, error)
      HealthCheck(ctx context.Context) error
      DiscoverTools(ctx context.Context) ([]MCPToolManifest, error)
      Close() error
  }
  ```

**`mcp.catalog.changed` domain event** (emitted by `RefreshCatalog` in task 020):
- Event type: `mcp.catalog.changed`
- Payload: `{connection_id, added_count, updated_count, removed_count, timestamp}`
- Consumers: tool resolution pipeline (task 049) invalidates session tool set cache for sessions using this connection

**`mcp.discover` native utility tool** (doc 09):
- Tool name: `mcp.discover`
- Tier: 1 (chat-layer, no capability grant required)
- Description: "Discover available MCP tools and their descriptions for a given connection"
- Input: `{connection_id: uuid, filter?: string}`
- Output: list of enabled tools from `mcp_tool_catalog` for the connection (filtered by agent's access rights per ISSUE #15)
- Implementation: query `MCPToolCatalogRepo.GetEnabled(connectionID)`, filter by agent scope, return JSON
- Register in `tool_definition` table (via `ToolDefinitionRepo.BulkUpsert` at startup)

### Must NOT build
- MCP API endpoints (task 022)
- MCP execution log (task 055)
- Full tool resolution pipeline (task 049)
- MCP tool invocation through the control plane (task 055)

## Acceptance Criteria

- [ ] Circuit breaker transitions `closed → open` after 5 consecutive failures
- [ ] Circuit breaker in `open` state returns `ErrCircuitOpen` without calling transport
- [ ] Circuit breaker transitions `open → half-open` after `recovery_timeout` elapses
- [ ] Successful probe in `half-open` resets to `closed`; connection status set to `active`
- [ ] Write tool call: no retry; error returned immediately to caller
- [ ] Read tool call: retried up to 3 times with exponential backoff on transient error
- [ ] Health check scheduler runs at configured interval; `last_healthy_at` updated on success
- [ ] `mcp.discover` tool registered in `tool_definition` at startup; returns enabled tools for connection
- [ ] `StdioTransport.HealthCheck` returns no error for a running MCP subprocess; returns error if process is dead

## Tests Required

**Unit tests:**
- Circuit breaker state machine: all transitions with mocked failure/success sequences
- `ErrCircuitOpen` returned when open; no transport calls made (verify via mock call count)
- Retry logic: transient error → 3 retries → final failure; verify backoff timing (use fake clock)
- Write tool no-retry: single failure → immediate error; mock called exactly once
- `mcp.discover` tool: mock catalog repo; verify scope filtering applied

**Integration tests:**
- Health check scheduler: start scheduler with a mock transport that fails; verify connection status transitions to `degraded` after failure threshold
- `StdioTransport` end-to-end: spawn a minimal MCP echo server (test fixture); call a tool; verify response; kill process; verify `HealthCheck` returns error
- Circuit breaker + connection status: run 5 failures → verify `mcp_connection.status='degraded'` in DB; run recovery probe success → verify `status='active'`

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

- Circuit breaker state is kept **in-memory per process** (not in the DB). The DB only stores the resulting connection `status` field. This means a process restart resets all circuit breakers to `closed`. This is acceptable — the health check scheduler will quickly re-detect any failing connections.
- The health check scheduler must be shut down gracefully on server shutdown (use a `context.Context` passed from the main server lifecycle). All in-flight health checks should be cancelled.
- `StdioTransport` process management: track child PIDs; send SIGTERM on `Close()`; wait for process exit with a 5-second deadline before SIGKILL. On health check failure, record the exit code in the error.
- The `recovery_timeout`, `failure_threshold`, and `per_call_timeout` values should be configurable per connection via `transport_config`. Provide org-level defaults via `organization.settings` (path: `settings.mcp.defaults`). Instance-level defaults are hardcoded (30s/10s/60s as above).
- When emitting `mcp.catalog.changed`, include the org_id in the event payload so consumers can scope their cache invalidation correctly.
