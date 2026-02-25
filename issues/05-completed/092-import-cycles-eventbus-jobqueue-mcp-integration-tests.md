# Task 092: Fix import cycles in eventbus, jobqueue, mcp integration tests

Layer: L1
Effort: S
Depends on: none

## Context

Three packages fail to compile their integration tests due to import cycles:

```
# internal/eventbus
eventbus_integration_test.go → testutil → agent → eventbus  [CYCLE]

# internal/jobqueue
jobqueue_integration_test.go → testutil → chat → jobqueue  [CYCLE]

# internal/mcp
circuit_breaker_integration_test.go → testutil → controlplane → mcp  [CYCLE]
```

The `internal/testutil` package spans multiple files. Even when a test only needs functions from cycle-free files (e.g., `testutil/eventbus.go` has no domain imports), importing `testutil` brings in ALL files in the package, including:
- `testutil/agent.go` which imports `internal/agent` (→ imports `eventbus`)
- `testutil/chat.go` which imports `internal/chat` (→ imports `jobqueue`)
- `testutil/controlplane.go` which imports `internal/controlplane` (→ imports `mcp`)

### What each test actually needs

**`eventbus/eventbus_integration_test.go`**: Only uses `testutil.PublishEvent`
- `PublishEvent` is defined in `testutil/eventbus.go` which imports NOTHING from domain packages

**`jobqueue/jobqueue_integration_test.go`**: Only uses `testutil.EnqueueJob`
- `EnqueueJob` is also defined in `testutil/eventbus.go` — same situation

**`mcp/circuit_breaker_integration_test.go`**: Uses `testutil.MakeOrg`, `testutil.MakeAgent`, `testutil.MockMCPServer`, `testutil.MakeMCPConnection`, `testutil.ScriptedMCPServer`, `testutil.ScriptedMCPHandler`, `testutil.MCPConnectionOptions`
- These are from `testutil/auth.go`, `testutil/agent.go`, `testutil/mcp.go` — none of which import `controlplane`
- But importing `testutil` pulls in `testutil/controlplane.go` → `internal/controlplane` → `internal/mcp`

## Required Fix

Move `PublishEvent`, `AdvanceCursor`, and `EnqueueJob` from `internal/testutil/eventbus.go` into the `internal/testdb` package (which already has no domain imports). Then update the import paths in the affected test files:

1. Add functions to `internal/testdb/helpers.go` (new file):
   - `testdb.PublishEvent(t, pool, orgID, eventType, payload) int64`
   - `testdb.AdvanceCursor(t, pool, consumerName, seq)`
   - `testdb.EnqueueJob(t, pool, jobType, priority, payload) uuid.UUID`

2. Remove `testutil/eventbus.go` or keep the functions as wrappers calling `testdb` if other packages depend on the `testutil.*` names.

3. Update imports in:
   - `internal/eventbus/eventbus_integration_test.go`: change `testutil` import to `testdb`, update function calls
   - `internal/jobqueue/jobqueue_integration_test.go`: same

For the `mcp` cycle, move `testutil/controlplane.go` functions into a separate package `internal/testutil/cpfix` (or similar), so that `mcp` tests can import `testutil` without pulling in `controlplane`. OR: extract just the MCP-needed helpers from `testutil` into a standalone `testutil/mcpfix` package that does not import `controlplane`.

Alternatively (simpler): move all functions currently in `testutil/controlplane.go` into a new `internal/cpfixtures` package and update all existing callers of those functions.

## Acceptance Criteria

- [ ] `go test ./internal/eventbus/... -tags integration -count=1` compiles and passes
- [ ] `go test ./internal/jobqueue/... -tags integration -count=1` compiles and passes
- [ ] `go test ./internal/mcp/... -tags integration -count=1` compiles and passes
- [ ] All previously-passing integration tests still pass

## Required Tests

- Integration: `go test ./... -tags integration -count=1 -timeout 8m` — previously-passing packages still pass, the 3 previously-broken packages now also pass
- Build: `go build ./...` — no new build errors
