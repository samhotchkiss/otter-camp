# 03. Projects and Task Flow

## Keep from Existing Product

- Project-scoped tasks/issues.
- Work status state machine.
- Approval state machine with reviewer gate.
- Flow template + flow step progression.
- PM-to-human escalation (now via task dependencies rather than a separate blocker entity).

## Project Entities

- `project`
- `project_task`
- `project_task_participant`
- `flow_template`
- `flow_node`

## Task Lifecycle

- Work status: `draft`, `queued`, `in_progress`, `blocked`, `on_hold`, `review`, `done`, `cancelled`
- Approval state: `draft`, `ready_for_review`, `needs_changes`, `approved_by_reviewer`, `approved`

### Work Status Definitions

- `draft`: idea captured but not yet planned. Not available for agents to pick up. Needs to be scoped with structured context (description, acceptance criteria, etc.) before it can move forward. This is the PM's job.
- `queued`: planned and ready for an agent to pick up. Has sufficient context for execution.
- `in_progress`: an agent is actively working on this.
- `blocked`: waiting on a dependency (another task) to be resolved.
- `on_hold`: paused intentionally — not blocked, but not being worked on.
- `review`: work is complete and awaiting review.
- `done`: completed and approved.
- `cancelled`: abandoned (reachable from any state).

## Flow Nodes

- Node types: `work`, `review`
- Actor types: `role`, `project_manager`, `human`, `agent`
- Edges: `next_node`, `reject_node`
- Skills: optional list of skill references required for this node. Only these skills are loaded into the agent's prompt during execution of this node (see 10-skills-integration.md).

## Flow Progression

Flow advancement is always explicit — never automatic.

- An agent must signal "step done" to advance the flow to the next node. This is a control plane action (`project.flow.advance`) subject to policy evaluation.
- A successful run does NOT automatically advance the flow. The agent may need multiple runs at a single flow node before the work is complete.
- If the agent encounters an issue, it files a blocking task with a dependency link (see Blockers and Escalation). The flow stays at the current node until the dependency is resolved, then the agent resumes.
- For review nodes, the reviewer must explicitly approve or reject. Approval advances to `next_node`; rejection advances to `reject_node`.

### Runs and Flow Nodes

Every run that happens during a flow node's execution is linked back to the task and the flow node (see 16-agent-control-plane.md Task and Flow Binding). This provides full traceability: what the agent did, what it produced, how much it cost, and how long it took — all scoped to the specific step in the workflow.

## Staff Roles in Flow

- Planner, worker, reviewer role assignments remain project-scoped.
- Flow step owner resolution follows actor type and role assignment.

## Blockers and Escalation

There is no separate blocker entity. Blockers are tasks.

When an agent discovers an issue that prevents progress — a cross-task conflict, a missing decision, a dependency on external input — it files a new task describing the problem. That task is assigned to the project's **project manager** for triage.

### Mechanics

- The agent creates a new task with a dependency link: "my task depends on this new task."
- The agent's task transitions to `blocked` because it has an unresolved dependency.
- The new task is assigned to the project manager by default.
- The PM triages in a project-scoped session: they may resolve it themselves, escalate, or break it into further tasks.
- When the blocking task reaches `done`, the original task's dependency is satisfied and it can resume.

### Escalation Path

- **First level**: the project manager (a per-project role, assigned by Lori during staffing) receives the blocking task and decides how to handle it.
- **Second level**: if the PM cannot resolve it, they escalate to Frank (Chief of Staff) for cross-project or strategic decisions.
- **Third level**: if it requires human judgment, authorization, or external input, it escalates to the human.
- **Escalation is not automatic** — each level makes the call on whether to handle it or pass it up. This keeps the human's attention focused on decisions that actually require them.

### What the Agent Includes When Filing

- Clear description of what's blocking and why.
- Reference to the task being blocked (the dependency link).
- Any relevant context the agent discovered (conflicting code, ambiguous requirements, missing information).
- Suggested resolution if the agent has one.

## Optional V2 Extensions

- Task dependency graph beyond parent-child.
- Relationship types: duplicate, supersedes, relates_to, replies_to.
- Templates that include default labels/checklists.

## Open Questions

- Should task dependencies be strict DAG-only?
- Should approvals be configurable per project (single reviewer vs multiple reviewers)?
- How much backward compatibility with old task endpoints do we keep?

