# 022: MCP API Endpoints

| Field | Value |
|-------|-------|
| Layer | L2 |
| Size | S (≤1 day) |
| Spec refs | doc 09 §MCPAPI, doc 12 §APIConventions |
| Spec status | finished |
| Depends on | 021, 007 |
| Blocks | 077, 088 |

## Scope

Wire all MCP-domain HTTP endpoints. Handlers delegate to `MCPService` (task 020) and
circuit breaker (task 021). Auth middleware from task 007 applied to all routes.

### Must build

**Route registrations** (all under `/v1/mcp/` prefix, doc 12 authoritative):

```
GET    /v1/mcp/connections                             → MCPService.ListConnections
POST   /v1/mcp/connections                             → MCPService.CreateConnection
GET    /v1/mcp/connections/:id                         → MCPService.GetConnection
PATCH  /v1/mcp/connections/:id                         → MCPService.UpdateConnection
DELETE /v1/mcp/connections/:id                         → MCPService.DeleteConnection
POST   /v1/mcp/connections/:id/refresh                 → MCPService.RefreshCatalog
POST   /v1/mcp/connections/:id/test                    → test connection (health check probe)
GET    /v1/mcp/connections/:id/catalog                 → MCPToolCatalogRepo.ListByConnection
PATCH  /v1/mcp/connections/:id/catalog/:entry_id       → MCPService.EnableTool / DisableTool
GET    /v1/mcp/connections/:id/executions              → MCPExecutionLogRepo.ListByConnection (stub — real in task 055)
```

**Request/response shapes:**

`POST /v1/mcp/connections` body:
```json
{
  "display_name": "My MCP Server",
  "slug": "my-mcp-server",
  "project_id": null,
  "transport": "http",
  "transport_config": {
    "base_url": "https://mcp.example.com",
    "api_key": "ref:my-mcp-secret"
  },
  "is_enabled": true
}
```

`PATCH /v1/mcp/connections/:id/catalog/:entry_id` body:
```json
{ "is_enabled": true }
```

`POST /v1/mcp/connections/:id/test` response:
```json
{
  "data": {
    "status": "ok",
    "latency_ms": 42,
    "circuit_state": "closed"
  }
}
```
On failure:
```json
{
  "data": {
    "status": "error",
    "error": "connection refused",
    "circuit_state": "open"
  }
}
```
Note: `POST /test` always returns 200 OK — the test result is in the payload. It does not trigger circuit breaker state changes.

**Error mapping:**
- `ErrCircuitOpen` → 503 Service Unavailable with `{error: {code: "circuit_open"}}`
- `ErrConnectionFailed` → 503 Service Unavailable
- Slug conflicts → 409 Conflict

**RBAC enforcement:**
- `POST /v1/mcp/connections`, `DELETE`, `PATCH` (enable/disable): `admin` role required
- `POST /v1/mcp/connections/:id/refresh`, `POST /test`: `admin` or `member`
- `PATCH catalog/:entry_id` (enable/disable tools): `admin` role required
- `GET` endpoints: any authenticated principal

**`GET /v1/mcp/connections/:id/executions`** — stub implementation for this task:
- Return `{data: [], meta: {total: 0}}` with a comment that real data is populated in task 055
- Route must be registered so task 055 can replace only the handler, not the route

### Must NOT build
- MCP execution log implementation (task 055)
- Tool invocation via API (MCP tools are invoked via the control plane, not directly via API)
- Secret binding management endpoint (bindings are created as part of connection create/update)

## Acceptance Criteria

- [ ] `POST /v1/mcp/connections` with valid body → 201 Created; connection in `configuring` status
- [ ] `POST /v1/mcp/connections/:id/refresh` triggers `RefreshCatalog`; catalog endpoint returns newly discovered tools
- [ ] `PATCH /v1/mcp/connections/:id/catalog/:entry_id` with `{"is_enabled": true}` → tool `is_enabled=true` in DB
- [ ] `POST /v1/mcp/connections/:id/test` when circuit is open → 200 OK with `status: "error"` (not 503)
- [ ] `DELETE /v1/mcp/connections/:id` → 204 No Content; subsequent `GET` → 404
- [ ] Non-admin attempting `POST /v1/mcp/connections` → 403 Forbidden
- [ ] Unauthenticated request → 401 Unauthorized

## Tests Required

**Unit tests:**
- `POST /test` handler: circuit open → 200 with `status: "error"`; circuit closed → 200 with `status: "ok"`
- Error mapping: `ErrCircuitOpen` → 503 (for non-test endpoints that propagate it)

**Integration tests:**
- Full HTTP round-trip via `httptest.Server`:
  - Create connection → refresh → catalog has tools (is_enabled=false) → enable tool → verify enabled
  - Delete connection → cascade verified (catalog empty)
  - Test endpoint: healthy mock transport → `status: "ok"` with latency_ms > 0
- RBAC: member token on admin-only route → 403

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

- Secret bindings can be passed in `POST /v1/mcp/connections` as an optional `secret_bindings` array:
  ```json
  "secret_bindings": [
    {"secret_ref": "ref:my-secret", "env_var_name": "API_KEY"}
  ]
  ```
  The service creates `mcp_secret_binding` rows transactionally with the connection row.
- `PATCH /v1/mcp/connections/:id` allows updating `transport_config`, `display_name`, `is_enabled`. Updating `transport` type is not allowed after creation (return 422 if `transport` field is present in PATCH body).
- `POST /v1/mcp/connections/:id/refresh` is async-safe but currently implemented as synchronous (waits for catalog refresh). If the refresh takes more than 10 seconds, return 504 Gateway Timeout. A future task may make this async; for now, synchronous is simpler.
- The executions stub endpoint should include a `X-Stub: true` response header so integration tests can detect it and skip execution-log assertions until task 055 is complete.
