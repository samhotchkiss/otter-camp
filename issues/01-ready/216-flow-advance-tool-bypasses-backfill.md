# Issue 216: flow.advance tool handler bypasses backfill logic from PR #1593

## Summary

PR #1593 (issue 214) added backfill logic to `loadActiveFlowState()` in the flow execution service. But the native tool handler `handleFlowAdvance()` in `internal/tools/native/mutation_tools.go` bypasses this logic entirely by calling `e.flowExecs.GetByID()` directly. Agents calling `flow.advance` still get "repo: not found" errors.

## Root cause

Two code paths for flow.advance:

1. **Service layer** (`internal/flow/execution_service.go` lines 770-824): Has backfill logic that creates missing flow executions when a task has `flow_template_id` but no active execution.

2. **Tool handler** (`internal/tools/native/mutation_tools.go` lines 1214-1239): Calls `e.flowExecs.GetByID()` directly with the `flow_node_execution_id` parameter. Does NOT call the service's `AdvanceFlow()` method. Backfill never fires.

## Fix

The tool handler `handleFlowAdvance()` should call the flow execution service's `AdvanceFlow()` method instead of directly querying the repository. The service method already has the backfill logic.

Alternatively, modify `handleFlowAdvance()` to:
1. Try `GetByID()` first
2. If not found, call `loadActiveFlowState()` to backfill
3. Retry the lookup

## Impact

All 11 Sam.blog tasks cannot advance through the flow system. Agents work around it by using `task.update` to change `work_status`, but tasks cannot reach "done" via the flow terminal node as the spec requires.

## Related

- Issue 214 (flow execution not created for in-progress tasks) — PR #1593 partially fixed this
- Decision 7 in decisions.md documents this gap
