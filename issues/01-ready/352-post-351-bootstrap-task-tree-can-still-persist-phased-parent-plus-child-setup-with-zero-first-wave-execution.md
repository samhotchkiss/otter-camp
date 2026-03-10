## Reviewer Required Changes (2026-03-10 10:58 UTC)
Reviewer: Claude Opus 4.6 (reviewer autowork)

### P1
- [ ] `countProjectBootstrapFirstWaveJobs` SQL query inflates count by including bare async sessions without pending/claimed jobs
  - Files: `internal/turn/engine.go:1910-1930`
  - Required fix: The first branch of the UNION (`SELECT s.scope_id FROM chat_session s WHERE ...`) counts any async task-scoped session regardless of whether a job was ever enqueued. This lowers the verification bar — a session that exists but has no active job (e.g., session created but job enqueue failed) will be counted as a "claimed first-wave job." Either remove the bare-session branch and keep only the job_queue-joined branch, or rename the function and adjust the semantics so callers understand they are counting "kickoff sessions" not "jobs." The failure guard at line 1287 (`progress.FirstWaveJobCount == 0`) relies on this count meaning actual jobs, not just sessions.
  - Required test: Add a test where a task-scoped async session exists for a first-wave child but no `agent_turn` job is pending/claimed; verify `FirstWaveJobCount == 0` and bootstrap fails rather than succeeding.

- [ ] Missing regression test for the phased-parent failure path (acceptance criterion #4)
  - Files: `internal/turn/engine_integration_test.go`
  - Required fix: The new test `TestTurnEngineIntegrationProjectBootstrapPromotesPhasedChildWaveImmediatelyAfterPersistedStructure` only covers the **success** path (phased parents + children → promotion succeeds). The task spec and acceptance criteria explicitly require "Regression coverage exists for the case where phased parent + child setup persists successfully but first-wave promotion is skipped." The existing failure test (`TestTurnEngineIntegrationProjectBootstrapFailsWhenPersistedSetupDoesNotCreateFirstWaveExecution`) uses a non-phased structure and does not exercise the `HasPhasedParentTasks` code path.
  - Required test: Add `TestTurnEngineIntegrationProjectBootstrapFailsPhasedSetupWithoutChildPromotion` — create phased parent + bounded children (all draft), arrange for promotion to produce zero executions/jobs, and verify bootstrap fails with explicit `firstWaveExecution` failure class and the project is archived.

### P2
- [ ] `completeProjectBootstrapGateTask` now allows gate completion when zero setup tasks exist
  - Files: `internal/turn/engine.go:1337-1360`
  - Required fix: The old code returned early when `len(childTasks) == 0`. The new code removed that guard, so the gate completes even if no setup tasks are found. While the caller (`ensureProjectBootstrapFirstWaveExecution`) only reaches this function for phased setups, the function itself is not defensive. Add an early return when `len(childTasks) == 0` to prevent gate completion without any setup-task evidence, or add a clear comment explaining why this is safe for phased-only callers.
  - Required test: Existing tests should continue to pass; no new test required if the early-return guard is restored.

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
