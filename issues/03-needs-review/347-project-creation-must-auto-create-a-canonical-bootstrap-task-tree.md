# 347: Project creation must auto-create a canonical bootstrap task tree

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M |
| Spec refs | docsv2/03-projects-and-task-flow.md, docsv2/05-agents-staff-and-temps.md, docsv2/16-agent-control-plane.md |
| Depends on | 339, 340 |

## Problem

Bootstrap is still too dependent on one long kickoff/project-session conversation. That leaves the product vulnerable to:

- bloated project-session transcripts
- partial setup persistence with no runnable execution
- ambiguous restart points
- unclear ownership for planning, validation, and sign-off

We clarified a better model: project creation should immediately create a canonical bootstrap task tree instead of relying on one conversational bootstrap blob.

## Required behavior

When Frank creates a new project, OtterCamp must auto-create a canonical bootstrap task tree. At minimum, that tree must include bounded setup tasks covering:

1. bind repo and environment
2. staff the project
3. decompose workstreams into bounded executable tasks
4. validate task sizing and dependencies
5. attach and validate flow templates
6. select first-wave runnable tasks
7. request/record Frank sign-off

The bootstrap tree may use subtasks inside those setup tasks, but the setup steps themselves must be real tasks with normal task semantics and state.

## Why this matters

Bootstrap needs a real tracked work structure, not just a project-session transcript. That gives the system:

- explicit done criteria
- explicit restart points
- reviewable setup artifacts
- a clean boundary between setup and real project execution

## Acceptance criteria

- Project creation auto-creates a canonical bootstrap task tree.
- The bootstrap tree contains bounded setup tasks for repo binding, staffing, decomposition, task-shape validation, flow attachment/validation, first-wave selection, and Frank sign-off.
- Bootstrap setup is represented as tasks, not only as project-session chat state.
- Each bootstrap task can carry normal task state and verification semantics.
- Relevant `docsv2` specs are updated in the same change.

## Verification

- Integration test:
  - create a fresh project
  - verify the canonical bootstrap task tree exists immediately
  - verify the expected bootstrap task slugs/titles are present
- Integration test:
  - verify bootstrap setup survives project-session rotation because the setup state is task-backed, not only chat-backed
