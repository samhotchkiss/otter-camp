## Reviewer Note (2026-03-10 — dependency gate)
Reviewer: Claude Opus 4 (reviewer agent)

Implementation is **approved on quality/correctness grounds** (PR #1732, merged to main). All acceptance criteria met, all three required integration tests present and correct. No code changes required.

**Blocked by dependency gate:** Task 348 is still in `01-ready` (not `05-completed`). Per reviewer policy, this task cannot move to `05-completed` until all dependencies are complete. Re-queue to `03-needs-review` once task 348 reaches `05-completed`.

# 349: Bootstrap setup tasks must enforce bounded scope and delegate real work to project tasks

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/05-agents-staff-and-temps.md, docsv2/16-agent-control-plane.md |
| Depends on | 335, 339, 347, 348 |

## Problem

We clarified that bootstrap/setup tasks should not become broad production loops either. Their purpose is to prepare the project for execution, not to absorb the actual project work.

That means bootstrap tasks must stay bounded and must delegate real deliverable work into normal project tasks.

Without that rule, bootstrap can collapse back into:

- Lori doing one giant planning monologue
- oversized setup tasks that hide real work
- parent/bootstrap tasks doing project production work instead of orchestration and validation

## Required behavior

Bootstrap setup tasks must:

- obey the same bounded task-size policy as the rest of the system
- stay focused on setup/orchestration outcomes
- create/delegate the actual project deliverable work into normal project tasks
- use subtasks only for lightweight internal checkpoints inside a bounded setup task

Bootstrap setup tasks must NOT:

- absorb large amounts of actual project production work
- replace the real executable task tree
- become another monolithic planning loop

## Why this matters

The whole point of the bootstrap tree is to make launch structured and reviewable. If bootstrap tasks become oversized work buckets, we recreate the same failure mode in a different shape.

## Acceptance criteria

- Bootstrap setup tasks are validated against the bounded task-size policy.
- Bootstrap setup tasks create/delegate real project work into normal executable project tasks.
- Bootstrap setup tasks remain orchestration/setup work, not broad production work.
- Bootstrap subtasks are used only as lightweight internal checkpoints, not as a hidden replacement for real task decomposition.
- Relevant `docsv2` specs are updated in the same change.

## Verification

- Integration test:
  - bootstrap setup creates bounded executable project tasks instead of absorbing the work into bootstrap tasks
- Integration test:
  - oversized bootstrap setup item is rejected by the same planning-size validator
- Integration test:
  - bootstrap subtasks can exist as lightweight checkpoints without replacing executable project tasks
