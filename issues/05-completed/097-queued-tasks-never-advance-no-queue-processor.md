# Task 097: Add task queue processor — queued tasks never advance to in_progress

Layer: L2
Effort: M
Depends on: 095 (flow templates need nodes)

## Context

When a project task is created and transitions to `work_status = 'queued'`, there is NO automatic mechanism that picks it up and starts execution. Tasks remain permanently queued unless manually triggered via `POST /control/runs`.

Evidence:
- Task `b0dd6f59` created for the OtterCamp sales site project: `work_status = 'queued'` indefinitely
- The scheduling engine only creates new tasks from cron schedules — it does not process existing queued tasks
- The supervisor only monitors existing `in_progress` runs (stuck run detection, orphan recovery)
- The worker has no subscriber for `task.status_changed` → `queued` that creates a run and starts the flow

In test mode, `internal/server/task_handlers.go` lines 648-665 manually advance tasks:
```go
if isTestMode() && createdTask.FlowTemplateID != nil && h.flowService != nil {
    // transitions draft→queued→in_progress, calls StartFlow
}
```
This code is gated behind `isTestMode()` and runs in the HTTP handler, not as a background process.

### Intended behavior (from doc 03)

> Flow progression is always explicit: agents must signal "step done" to advance; run completion does not auto-advance.

However, the INITIAL launch of a task — going from `queued` to `in_progress` with a run created — is not documented as requiring manual intervention. The scheduler fires tasks into the queue; something must consume them.

## Required Fix

Add a task queue processor that:

1. Subscribes to `task.status_changed` events where `to_status = 'queued'` (or polls `work_status = 'queued'` rows)
2. For each queued task:
   a. If the task has a `flow_template_id`: call `TransitionStatus(queued→in_progress)`, then `StartFlow(taskID)` to create the first `FlowNodeExecution`, then publish/enqueue the initial run
   b. If the task has NO `flow_template_id` but has an `assigned_agent_id`: create a bare `agent_turn` job directly (no flow scaffolding), associate it with the task's session

This processor should be idempotent: if a task is already `in_progress`, skip it.

### Minimal implementation path

Add an event consumer in `internal/controlplane` or a new `internal/task` consumer:

```go
func SubscribeTaskQueued(events eventbus.EventBus, ...) eventbus.Subscription {
    return events.Subscribe("controlplane.task-queued", nil, func(ctx context.Context, event eventbus.DomainEvent) error {
        if event.EventType != "task.status_changed" { return nil }
        // parse to_status, task_id
        // if to_status != "queued" { return nil }
        // load task, transition to in_progress, call StartFlow or create bare run
        ...
    })
}
```

Wire this subscription in `internal/worker/worker.go`.

Alternatively: add a polling loop (every 30s) that queries for tasks with `work_status = 'queued'` that have been queued for >5 seconds and haven't been picked up yet.

## Acceptance Criteria

- [ ] Create a task with a valid flow template (after issue 095 fix), set `work_status = queued`
- [ ] Within 10 seconds, the task automatically transitions to `work_status = in_progress`
- [ ] A `flow_node_execution` row is created (if task has flow template)
- [ ] A run is created and begins processing
- [ ] Tasks without a flow template but with an assigned agent also advance
- [ ] Idempotent: already-in_progress tasks are not re-started
- [ ] `go build ./...` passes

## Required Tests

- Integration: create a task with a flow template, set to queued, assert transition to in_progress within 10s
- Integration: create a task without a flow template but with assigned_agent_id, assert it gets a run
- Unit: event consumer filters non-queued events
