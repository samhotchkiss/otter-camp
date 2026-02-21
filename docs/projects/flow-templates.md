# Projects: Flow Templates

> Summary: Reusable node-based task flows with enforceable handoffs and rejection loops.
> Last updated: 2026-02-21
> Audience: Agents and engineers defining or implementing task flows.

## Primitive Model

A flow is an ordered set of nodes. Each node defines:
- `step_key`: stable node key.
- `node_type`: `work` or `review`.
- `objective`: what must be completed.
- `actor_type`: who owns the node (`role`, `project_manager`, `human`, `agent`).
- `actor_value`: role or explicit agent id when needed.
- `next_step_key`: normal forward transition.
- `reject_step_key`: required on review nodes; where to send work back on rejection.

## Data Model (Canonical)

- `project_flow_templates`
  - `id`, `project_id`, `name`, `description`, `is_default`
- `project_flow_template_steps`
  - `flow_template_id`, `step_order`, `step_key`, `label`, `node_type`, `objective`, `actor_type`, `actor_value`, `next_step_key`, `reject_step_key`
- `project_issues` (tasks)
  - `flow_template_id`, `flow_step_key`, `flow_step_index`
- `project_issue_flow_blockers`
  - Open blocker records and escalation state (`project_manager` or `human`)

## API (Representative)

Project-scoped:
- `GET /api/projects/{id}/flow-templates`
- `POST /api/projects/{id}/flow-templates`
- `PUT /api/projects/{id}/flow-templates/{flowID}/steps`

Task-scoped:
- `POST /api/project-tasks/{id}/flow/assign`
- `POST /api/project-tasks/{id}/flow/advance` with `decision=complete|approve|reject`
- `POST /api/project-tasks/{id}/flow/blockers`
- `POST /api/project-tasks/{id}/flow/blockers/{blockerID}/escalate-human`
- `POST /api/project-tasks/{id}/flow/blockers/{blockerID}/resolve`
- Legacy aliases also exist under `/api/issues/{id}/...`

## Enforcement Behavior

- Assign sets the task to the first node.
- Advance follows `next_step_key` for `complete/approve`, and `reject_step_key` for `reject`.
- Node owner assignment is resolved by actor:
  - `role`: mapped from project pipeline roles.
  - `project_manager`: project `primary_agent_id`.
  - `agent`: explicit agent id when provided.
  - `human`: no agent owner; task waits for human.

## Blocker Escalation

- Sub-agent blocker is raised as an open flow blocker assigned to project manager.
- If unresolved, project manager escalates blocker to human.
- Human escalation moves task to `on_hold` and inbox.
- Flow cannot advance while an open blocker exists.

## Suggested Starter Flows

### Frontend Build

1. `spec` (`work`, actor=`role:planner`)
2. `design_review` (`review`, actor=`role:reviewer`, reject=`spec`)
3. `implementation` (`work`, actor=`role:worker`)
4. `qa_review` (`review`, actor=`role:reviewer`, reject=`implementation`)
5. `ship` (`work`, actor=`human`)

### Backend Build

1. `spec` (`work`, actor=`role:planner`)
2. `architecture_review` (`review`, actor=`role:reviewer`, reject=`spec`)
3. `implementation` (`work`, actor=`role:worker`)
4. `code_review` (`review`, actor=`role:reviewer`, reject=`implementation`)
5. `ship` (`work`, actor=`human`)

## Change Log

- 2026-02-21: Shifted flow design from linear role steps to node primitives with reject loops and blocker escalation.
- 2026-02-21: Added flow templates spec, API, and suggested template library.
