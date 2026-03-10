## Reviewer Required Changes (2026-03-09 22:30 UTC)
Reviewer: Claude Opus 4.6 (autowork reviewer)

### P0
- [ ] PR #1729 has merge conflicts with main after PR #1728 (task 342) was merged
  - Files: `internal/turn/engine.go`, `internal/turn/engine_integration_test.go`, `docsv2/03-projects-and-task-flow.md`, `docsv2/16-agent-control-plane.md`
  - Required fix: Rebase `task/348-bootstrap-tree-gate` onto current `main` and resolve conflicts. Both PRs touch the same files in overlapping regions.
  - Required test: After rebase, verify `go test ./internal/turn -tags integration` passes locally.

### Review notes
Code review passed. The gate logic, event handling, child verification, test coverage, and docsv2 updates are all correct. Only low-severity observations noted (test helper empty-slug edge case, silent JSON parse in event handler — both consistent with codebase patterns). Once the rebase is complete and CI passes, this is ready to merge.

# 348: Bootstrap task tree must block project execution until setup and sign-off complete

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/16-agent-control-plane.md |
| Depends on | 339, 340, 341, 347 |

## Problem

Even when setup tasks or kickoff artifacts exist, OtterCamp can still drift into a half-bootstrapped state where the project looks active before setup is actually complete. We agreed on a stronger rule:

- the project is not "live" just because a project row, session, assignments, or tasks exist
- the bootstrap tree must finish and be signed off before first-wave execution is allowed to start

Without that gate, the system keeps treating partial setup as a runnable project.

## Required behavior

The canonical bootstrap task tree must act as a hard execution gate:

- first-wave execution cannot start until required bootstrap tasks are complete
- Frank sign-off must be recorded before the execution gate opens
- bootstrap parent completion must verify that child setup tasks succeeded and that their outputs work together
- when bootstrap is incomplete, the project must not claim first-wave jobs

## Why this matters

This gives the product one clean line:

- before bootstrap completion/sign-off: setup phase
- after bootstrap completion/sign-off: runnable project execution

That is the boundary needed for correct fail-fast vs pause/recover behavior.

## Acceptance criteria

- First-wave execution is blocked until the canonical bootstrap task tree completes.
- Frank sign-off is required before the bootstrap gate opens.
- Bootstrap completion verifies child setup outputs together instead of trusting project-session chatter alone.
- A project cannot become execution-runnable while bootstrap tasks remain incomplete.
- Relevant `docsv2` specs are updated in the same change.

## Verification

- Integration test:
  - create a fresh project with bootstrap tasks present
  - verify first-wave execution cannot start before bootstrap completion + Frank sign-off
- Integration test:
  - complete required bootstrap tasks and record sign-off
  - verify first-wave execution becomes runnable
- Integration test:
  - leave one required bootstrap task incomplete
  - verify no first-wave jobs are claimable
