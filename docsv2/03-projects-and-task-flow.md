# 03. Projects and Task Flow

## Keep from Existing Product

- Project-scoped tasks/issues.
- Work status state machine.
- Approval state machine with reviewer gate.
- Flow template + flow step progression.
- Flow blockers with PM-to-human escalation.

## Project Entities

- `project`
- `project_task`
- `project_task_participant`
- `flow_template`
- `flow_node`
- `flow_blocker`

## Task Lifecycle

- Work status: `queued`, `in_progress`, `blocked`, `on_hold`, `review`, `done`, `cancelled`
- Approval state: `draft`, `ready_for_review`, `needs_changes`, `approved_by_reviewer`, `approved`

## Flow Nodes

- Node types: `work`, `review`
- Actor types: `role`, `project_manager`, `human`, `agent`
- Edges: `next_node`, `reject_node`

## Staff Roles in Flow

- Planner, worker, reviewer role assignments remain project-scoped.
- Flow step owner resolution follows actor type and role assignment.

## Blockers and Escalation

- Blockers are first-class records linked to task.
- Escalation levels: `project_manager`, then `human`.
- Advancing flow is blocked while blocker is open.

## Optional V2 Extensions

- Task dependency graph beyond parent-child.
- Relationship types: duplicate, supersedes, relates_to, replies_to.
- Templates that include default labels/checklists.

## Open Questions

- Should task dependencies be strict DAG-only?
- Should approvals be configurable per project (single reviewer vs multiple reviewers)?
- How much backward compatibility with old task endpoints do we keep?

