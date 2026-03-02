# Task 191: Enforce flow requirement for in_progress tasks

Layer: L2
Effort: M
Depends on: none

## Context

Tasks in `in_progress` status should have an active flow execution. Currently the system allows tasks to be transitioned to `in_progress` without any flow, which represents an invalid state. The TUI now shows a warning ("No active flow — task should have a flow to be in progress") but the backend doesn't enforce this.

## Current State

- `TransitionStatus()` in `internal/task/service.go` validates allowed transitions but does not check for flow existence
- Tasks can be moved to `in_progress` via API without an active flow execution
- TUI shows a warning badge but cannot prevent the invalid state

## Required Fix

**1. Backend validation in TransitionStatus**
When transitioning to `in_progress`, check if the task has an active flow execution. If not:
- Option A: Reject the transition with a typed error (strict)
- Option B: Auto-create a flow execution using the project's default flow template (lenient)
- Recommendation: Start with Option A (reject) and add an override flag for system transitions

**2. Cleanup existing invalid tasks**
Add a one-time migration or script to identify tasks that are `in_progress` with no active flow and either:
- Transition them back to `queued`
- Or create flow executions for them retroactively

## Acceptance Criteria

- [ ] `TransitionStatus("in_progress")` validates flow existence
- [ ] Error message is clear: "task requires an active flow to be in_progress"
- [ ] System-initiated transitions (supervisor, scheduler) can bypass with flag
- [ ] Existing invalid tasks cleaned up

## Required Tests

- Unit test: transitioning to in_progress without flow returns error
- Unit test: transitioning to in_progress with active flow succeeds
- Unit test: system override flag bypasses validation
