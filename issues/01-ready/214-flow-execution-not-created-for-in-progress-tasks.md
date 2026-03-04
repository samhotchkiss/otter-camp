# Issue 214: flow.advance fails — flow execution not created for in-progress tasks

## Problem

When agents call `flow.advance` on Sam.blog tasks, they get a "not found" error. The flow template was assigned to tasks via SQL (Decision 4) AFTER the tasks were already in `in_progress` status. The `TaskQueueProcessor` creates flow executions during the `queued → in_progress` transition, but since the tasks were already in-progress when `flow_template_id` was set, no flow executions were ever created.

This means:
- `flow.advance` tool returns "not found" for all 11 Sam.blog tasks
- Tasks cannot progress through Work → Review → Done via the flow system
- The TUI shows empty Flow section in task detail
- Agents are working around this by using `task.update` to change `work_status`, but this bypasses flow enforcement

## Root Cause

`internal/task/queue_processor.go` (or equivalent) only creates flow executions when processing the task queue transition. If `flow_template_id` is set on a task that's already `in_progress`, no flow execution is created.

## Expected Behavior

When `flow_template_id` is set on a task (via `task.update` or any other mechanism), a flow execution should be created if one doesn't already exist — regardless of the task's current status.

Alternatively, `flow.advance` should check for missing flow executions and create them on-demand if a `flow_template_id` exists on the task.

## Impact

- **Critical for Ralph Loop**: All 11 Sam.blog tasks are blocked from completing via the flow system
- Agents can't advance tasks from Work → Review → Done
- The flow enforcement spec (doc 03: "Task reaches done ONLY via flow terminal node") is unenforceable

## Reproduction

1. Create a task
2. Move task to `in_progress` (via task queue or task.update)
3. Set `flow_template_id` on the task
4. Have an agent call `flow.advance` → returns "not found"

## Suggested Fix

In the task update handler or the flow.advance tool handler, add logic:
```
if task has flow_template_id but no active flow_execution:
    create flow_execution from template
    set initial node to first Work node
```

This should be ~10-20 lines in the flow service.
