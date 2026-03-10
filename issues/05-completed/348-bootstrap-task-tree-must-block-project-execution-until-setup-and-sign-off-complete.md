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
