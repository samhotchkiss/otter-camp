# 094: Tool Dispatcher Not Implemented — Both Tiers Return "Unavailable"

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | L (1 week+) |
| Spec refs | doc 16 §ExecutionBroker, doc 11 §CLIExecution, doc 11 §BrowserExecution, doc 09 §MCPTools, doc 20 §ToolTaxonomy |
| Spec status | unimplemented |
| Depends on | 093 (live model gateway — unimplemented), 054 (control plane schema — done), 055 (tool execution service — done), 056 (native tools tier 1 — done), 059 (browser execution — done), 060 (tool execution audit/retry — done) |
| Blocks | All agent tool use (CLI, browser, MCP, native mutations) |
| Severity | HIGH — agents can't take any actions beyond conversation |

## Problem

The worker wires `turn.UnavailableToolDispatcher{}` for both tool tiers:

```go
// internal/worker/worker.go:201
Dispatcher: turn.UnavailableToolDispatcher{},
```

Both methods return an error:
```go
func (UnavailableToolDispatcher) DispatchTier1(_ context.Context, call ToolCall) (ToolResult, error) {
    return ToolResult{}, fmt.Errorf("tool dispatcher is not configured")
}
func (UnavailableToolDispatcher) DispatchTier2(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
    return ToolResult{}, fmt.Errorf("tool dispatcher is not configured")
}
```

The underlying services ARE built (CLI executor, browser executor, MCP service, native tools, tool resolver) but not wired into a live `ToolDispatcher` implementation.

## Must Build

### Tier 1 (read-only, in-chat): `LiveTier1Dispatcher`
Native read-only tools: task lookup, memory query, project/agent status, etc.
Uses `tools.ToolResolver` (already initialized in worker, line 165).

### Tier 2 (side-effecting, via control plane broker): `LiveTier2Dispatcher`
Routes to:
- `system.cli.execute` → `internal/cli.Executor`
- `browser.*` → `internal/browser.Executor`
- `mcp.*` → `internal/mcp.Service`
- Native mutations → task CRUD, flow advancement, etc.

Each tier 2 call must:
1. Create a `Run` record via `controlplane.RunService` (already initialized, line 155)
2. Evaluate capability policy via `controlplane.PolicyService`
3. Execute the action
4. Record result as `RunArtifact`/`RunEvent`
5. Update `Run` to completed or failed

## Notes
- Priority: unblock after issue 093 (model gateway) — tools are useless without a working LLM turn
- The `ToolResolver` is already initialized in the worker (line 165) and passed to the turn engine
- `controlplane.RunService` is already initialized (line 155)
- Tier 1 dispatch can be done incrementally — start with the tools agents actually use first
