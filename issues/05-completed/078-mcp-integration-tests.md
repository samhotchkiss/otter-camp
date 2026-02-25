# 078: MCP Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | S (≤1 day) |
| Spec refs | doc 09 §ConnectionLifecycle, doc 09 §ToolCatalogRefresh, doc 09 §CircuitBreaker, doc 09 §SecretBinding, doc 09 §ExecutionLog, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 020, 021, 022, 009 |
| Blocks | 089 |

## Scope

Integration test suite for the MCP domain: connection lifecycle (configuring → active →
degraded → failed), tool catalog refresh, circuit breaker state machine
(closed → open → half-open), secret binding resolution, and execution log recording.
All tests use a real PostgreSQL database via `testdb.New(t)`. MCP servers are in-process
test doubles with scripted responses.

### Must build

**Test file:** `internal/mcp/connection_integration_test.go`

**Test file:** `internal/mcp/circuit_breaker_integration_test.go`

**Test file:** `internal/mcp/execution_log_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/mcp.go`:
- `MakeMCPConnection(t, db, orgID, opts)` — creates `mcp_connection` row; returns ID
- `MockMCPServer(t, opts)` — starts an in-process MCP server with scripted tool catalog
  and execution responses; returns the connection URL
- `ScriptedMCPServer(t, handlers)` — returns a server that uses the handlers list
  (each call pops the next handler; if exhausted, returns error)

**Test scenarios in connection_integration_test.go:**

`TestConnection_Lifecycle_ConfiguringToActive` — create connection row with
status='configuring'; mock MCP server responds with a valid tool list; trigger catalog
refresh; connection status becomes 'active'; `mcp_tool_catalog` rows created (all with
is_enabled=false per default-deny); `mcp.catalog.changed` domain event emitted.

`TestConnection_CatalogRefresh_DefaultDeny` — after catalog refresh, GET
/v1/mcp/connections/:id/catalog returns all tools with `is_enabled=false`; PATCH
catalog entry to is_enabled=true; subsequent GET shows is_enabled=true; all others
remain false (targeted enabling, not bulk enable).

`TestConnection_CatalogRefresh_NewTool` — connection has 3 tools in catalog; mock server
adds a 4th tool; trigger refresh (POST /v1/mcp/connections/:id/refresh); new tool row
created with is_enabled=false; existing 3 rows unchanged; `mcp.catalog.changed` event.

`TestConnection_CatalogRefresh_RemovedTool` — mock server drops a previously-listed tool
from the catalog; trigger refresh; removed tool's catalog row gets `is_enabled=false` and
a `removed_at` timestamp (or equivalent soft-delete); domain event emitted.

`TestConnection_SecretBinding_Resolution` — create `secret` row with slug='db-password';
create `mcp_secret_binding` row for the connection referencing slug='db-password'; when
MCP server is called, the transport_config `ref:db-password` is resolved to the decrypted
secret value; the raw secret value is NOT logged anywhere (scrubber invariant).

`TestConnection_SecretBinding_MissingSecret` — `mcp_secret_binding` references a
slug that does not exist in `secret` table; connection refresh fails with
`mcp.secret_not_found` error; connection status becomes 'failed'.

`TestConnection_PeriodicHealthCheck` — connection is active; advance clock past health
check interval; health check job runs; mock server returns healthy; connection remains
active; heartbeat_at field updated.

`TestConnection_ProjectScope_vs_OrgScope` — create one org-scoped connection and one
project-scoped connection; GET /v1/mcp/connections filtered by project returns only
the project-scoped connection and the org-scoped one; does NOT return connections from
other projects.

**Test scenarios in circuit_breaker_integration_test.go:**

`TestCircuitBreaker_Closed_NormalOperation` — circuit in 'closed' state; MCP tool call
succeeds; circuit remains closed; `mcp_execution_log` row with status='success'.

`TestCircuitBreaker_Trips_OnConsecutiveFailures` — mock server returns errors for N
consecutive calls (N = circuit breaker failure threshold); after Nth failure, circuit
transitions to 'open'; next call is rejected immediately (no attempt to MCP server);
`mcp_connection.circuit_state='open'` persisted.

`TestCircuitBreaker_Open_RejectsAllCalls` — circuit in 'open' state; send a call; it is
rejected with `mcp.circuit_open` error; no attempt made to the MCP server (verify by
assert mock server received 0 calls during this test step).

`TestCircuitBreaker_HalfOpen_RecoveryAttempt` — circuit is open; advance clock past
recovery window; circuit transitions to 'half-open'; one probe call is sent; mock server
returns success; circuit transitions to 'closed'; normal operation resumes.

`TestCircuitBreaker_HalfOpen_FailsBack` — circuit is half-open; probe call fails; circuit
transitions back to 'open'; recovery window resets.

`TestCircuitBreaker_PerCallTimeout` — mock server hangs beyond per-call timeout; call
returns timeout error; circuit records the failure; circuit state may trip if threshold
reached.

`TestCircuitBreaker_WriteTools_NoAutoRetry` — tool marked as a write operation (mutating);
on failure, circuit DOES record the failure but the individual call is NOT auto-retried
(write tools are not retried by default per spec).

**Test scenarios in execution_log_integration_test.go:**

`TestExecutionLog_RecordOnSuccess` — MCP tool invocation succeeds; `mcp_execution_log`
row created with status='success'; `duration_ms` set; `tool_name` matches; `agent_id`
set correctly.

`TestExecutionLog_RecordOnFailure` — MCP tool invocation fails; `mcp_execution_log` row
with status='error'; `error_message` field populated; row is still written (errors are
logged, not dropped).

`TestExecutionLog_RunLinkage` — tool invocation occurs within a control plane run;
`mcp_execution_log.run_id` FK set; `mcp_execution_log.tool_execution_id` FK set;
both IDs resolve to real rows.

`TestExecutionLog_SecretScrubber` — MCP tool response contains a value matching a known
secret pattern (e.g., the db-password value from a binding); `mcp_execution_log.output`
has the secret value replaced with `[REDACTED]`; raw secret is never stored.

### Must NOT build

- E2E tests that involve the full agent → MCP tool call path (task 087)
- CLI execution tests (task 079)
- Tests that make real external MCP server calls (all MCP servers use `MockMCPServer`)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/mcp/... -tags integration`
- [ ] `MockMCPServer` is fully in-process; no external network calls required
- [ ] `TestConnection_CatalogRefresh_DefaultDeny` verifies every new catalog entry starts with `is_enabled=false`
- [ ] `TestCircuitBreaker_Open_RejectsAllCalls` verifies zero calls reach the mock server after the circuit is open
- [ ] `TestExecutionLog_SecretScrubber` verifies the scrubbed value in the DB log column, not just in the response
- [ ] `TestConnection_SecretBinding_Resolution` verifies the resolved value is used in transport but not logged
- [ ] `TestCircuitBreaker_HalfOpen_FailsBack` verifies the recovery window resets after a failed probe

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestConnection_Lifecycle_ConfiguringToActive`
- `TestConnection_CatalogRefresh_DefaultDeny`
- `TestConnection_CatalogRefresh_NewTool`
- `TestConnection_CatalogRefresh_RemovedTool`
- `TestConnection_SecretBinding_Resolution`
- `TestConnection_SecretBinding_MissingSecret`
- `TestConnection_PeriodicHealthCheck`
- `TestConnection_ProjectScope_vs_OrgScope`
- `TestCircuitBreaker_Closed_NormalOperation`
- `TestCircuitBreaker_Trips_OnConsecutiveFailures`
- `TestCircuitBreaker_Open_RejectsAllCalls`
- `TestCircuitBreaker_HalfOpen_RecoveryAttempt`
- `TestCircuitBreaker_HalfOpen_FailsBack`
- `TestCircuitBreaker_PerCallTimeout`
- `TestCircuitBreaker_WriteTools_NoAutoRetry`
- `TestExecutionLog_RecordOnSuccess`
- `TestExecutionLog_RecordOnFailure`
- `TestExecutionLog_RunLinkage`
- `TestExecutionLog_SecretScrubber`

**E2E tests:** None — covered by task 087.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- MCP server: `MockMCPServer` in-process test double (scripted tool catalog + call responses)
- Secret store: real (AES-256-GCM decryption via task 009); secret rows in testdb
- Clock: injected `clock.Fake` for health check interval and circuit recovery window tests

**ISSUE #15 (RESOLVED — resource read scope check):**
`TestConnection_ProjectScope_vs_OrgScope` tests access visibility. The scope check
is: project-scoped connection → verify `agent_project_assignment` exists for agent+project;
org-scoped connection → allow all org agents. Remove the `t.Skip` stub and implement
`TestMCPResource_ScopeCheck` with both project-scoped (access denied / granted) and
org-scoped (always granted) cases.

**MockMCPServer transport:**
The mock server should speak the MCP protocol (JSON-RPC or HTTP-based, depending on the
MCP connection transport_type). Keep the mock simple: for stdio transport type, use an
in-process pipe; for HTTP transport, use httptest.NewServer.

**Secret binding in transport_config:**
The transport_config jsonb field uses `ref:<slug>` syntax for inline secret references.
`TestConnection_SecretBinding_Resolution` tests that the reference is resolved at
connection time (when the transport is initialized), not at each tool call.
