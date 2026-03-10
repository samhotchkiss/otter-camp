# 352 — Post-351 bootstrap task tree can still persist phased parent plus child setup with zero first-wave execution

## Problem
After `351`, a fresh Sam.blog bootstrap can now persist a cleaner phased setup tree, but it can still stop after creating the bootstrap structure without promoting any first-wave executable tasks into real flow execution.

Observed on project `944a5e96-ace8-4218-9b18-c390d632ea30` (`sam-blog-rebuild-v3`):
- `3` project assignments persisted
- `1` flow template persisted
- `14` tasks persisted
- task tree included both phased parent tasks and bounded child tasks
- all tasks remained `draft`
- `0` active `flow_node_execution`
- `0` runnable first-wave jobs

This means the bootstrap task-tree model is structurally better, but the system still fails the actual bootstrap contract because persisted setup never becomes runnable execution.

## Why this matters
A clean bootstrap is not complete when the project/session/tasks exist. It is complete only when the first executable child wave has actually been promoted into runnable task-scoped execution.

If OtterCamp can persist a well-shaped bootstrap tree but still leave every task in `draft`, it will continue to waste time in planning/setup artifacts without ever starting work.

## Required behavior
- Once bootstrap has persisted:
  - assignments
  - runnable flow template(s)
  - bounded child task tree
- the runtime must immediately select and promote the first-wave executable child tasks.
- Parent/bootstrap tasks must remain orchestration-only and may not count as first-wave execution.
- A project with persisted bootstrap structure but zero promoted/runnable first-wave child tasks must fail bootstrap instead of remaining active.
- The bootstrap phase/checkpoint model must record this specifically as a failure to create first-wave execution, not as a successful setup.

## Acceptance criteria
- Fresh bootstrap that persists phased parent + child setup also persists at least one first-wave executable child task promoted out of `draft` into runnable execution.
- If no child task is promoted, bootstrap fails immediately with explicit failure phase/class/reason.
- Parent/bootstrap tasks are never marked executable in place of child tasks.
- Regression coverage exists for the case where phased parent + child setup persists successfully but first-wave promotion is skipped.
- `docsv2` is updated to state that phased bootstrap setup is still invalid until first-wave child execution exists.

## Verification
- Add an integration test that reproduces the observed state (`assignments + template + phased parents + bounded children + zero executions`) and proves it now fails/archives or promotes correctly.
- Run targeted bootstrap/turn integration tests covering the new failure/promotion path.
