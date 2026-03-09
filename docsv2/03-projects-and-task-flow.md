---
## Summary

This spec defines OtterCamp's project and task management domain -- the core system for how work gets planned, executed, reviewed, and delivered. Every project is backed by a git repository. Tasks are always created through agents or chat (never through UI), and move through a state machine: `draft` -> `queued` -> `in_progress` -> `review` -> `done`, with `blocked`, `on_hold`, and `cancelled` as additional states. A `requires_human_review` flag on draft tasks controls whether the PM can autonomously queue them or must get human sign-off first. Tasks are flat at the project level (no parent-child hierarchy); large efforts are modeled as multiple tasks connected by dependencies that form a DAG.

Each task is governed by a **flow template** -- an immutable, directed graph of `work` and `review` nodes designed conversationally through the project manager. Flow progression is always explicit: agents must signal "step done" to advance; run completion does not auto-advance. Review nodes gate quality with approve/reject decisions. Rejection loops create new `flow_node_execution` records (tracked by a visit counter), preserving full audit history per attempt. Tasks can optionally be decomposed into **subtasks** scoped to a specific flow node execution. Subtasks are sequential (they share the task branch to avoid git conflicts), have a reduced status set, and cannot have their own flows -- if a subtask needs a flow, the parent task is too big and should be split.

The scheduling system manages concurrency via global and per-provider limits. Priority levels (`urgent`, `high`, `normal`, `low`) combined with FIFO ordering determine pickup order. Blockers are modeled as tasks with dependency links (not a separate entity), and escalation flows PM -> Frank -> human. Tasks can also declare `blocks_scope = 'all'` to create a project-wide execution gate: while any such task is outstanding, only the lowest-numbered gate task may start. The human's **inbox** is an action-required queue (distinct from notifications) for scoping reviews, work reviews, draft action approvals, escalations, and capability approvals. A **merge queue** serializes completed task branches merging to `main`, with auto-resolution for trivial conflicts and PM escalation for non-trivial ones. **Scheduled tasks** create recurring work from templates on a cron cadence, with three overlap policies (skip, queue, replace) and max duration enforcement. The schema comprises 12 tables: `project`, `project_task`, `project_subtask`, `project_task_participant`, `project_task_dependency`, `project_task_event`, `flow_template`, `flow_node`, `flow_node_execution`, `task_schedule`, `inbox_item`, and `merge_queue_entry`.

---

# 03. Projects and Task Flow

## Keep from Existing Product

- Project-scoped tasks/issues.
- Work status state machine.
- Review gating via flow nodes (replaces standalone approval state machine).
- Flow template + flow step progression.
- PM-to-human escalation (now via task dependencies rather than a separate blocker entity).

## Project Entities

- `project`
- `project_task`
- `project_subtask` (scoped to a task + flow node)
- `project_task_participant`
- `project_task_dependency`
- `project_task_event` (audit trail)
- `flow_template`
- `flow_node`
- `flow_node_execution` (per-task state of each flow node)
- `task_schedule` (recurring task creation)
- `inbox_item`
- `merge_queue_entry`

## Task Creation

Tasks are always created through agents or chat — there is no direct UI creation path. The human talks to agents, and agents create tasks.

### Creation Paths

- **Human via chat**: the human describes what they want ("Frank, I need a landing page for the new product"). The receiving agent (Frank, PM, etc.) creates the task.
- **PM agent (planning)**: the PM breaks down project goals into tasks during planning. This is the primary path for structured work.
- **Any agent (blocker)**: when an agent encounters an issue that prevents progress, it files a new task with a dependency link (see Blockers and Escalation).
- **Frank**: can create tasks at org level that get assigned to projects.
- **Schedule**: recurring task schedules automatically create tasks on a cadence (see Scheduled Tasks). These are pre-scoped and go straight to `queued`.

### Project Bootstrap Gate Task

Every new project starts with a mandatory bootstrap task as the first project task:

- `task_number = 1`
- `blocks_scope = 'all'`
- Flow default:
  - `work` node assigned directly to Lori (`actor_type = 'agent'`, `actor_id = lori`) to produce staffing + decomposition output
  - `review` node assigned directly to Frank (`actor_type = 'agent'`, `actor_id = frank`) to approve/reject the setup
  - Reject loops back to Lori for revision

This uses the normal flow system and normal scheduler rules. The bootstrap task's `blocks_scope = 'all'` gate prevents any other task from starting until Frank approves and the task reaches `done`.

If Lori needs to create a brand-new PM during bootstrap, that PM candidate is created as a draft **staff** agent and then activated as part of successful `project_manager` assignment. Fresh planning must not create a temp PM candidate and then try to assign that temp as the project's PM.

### Fresh Kickoff vs Resume

- A **fresh kickoff** means "start this project again as a clean slate." The system creates at most one new live project and one canonical project-scoped planning session for that kickoff request.
- Repeating or retrying the same fresh kickoff request must reuse that canonical live project/session path. The system must not silently create a second active project or a second parallel project-planning session for the same intended run.
- Fresh kickoff planning maps each explicitly planned workstream to exactly one runtime task by default. Retries and recovery reuse or repair that same task set; they do not silently fan a planned task into extra top-level `(Workstream N)` siblings unless the planner or flow explicitly requested parallel child tasks.
- When a planned workstream is explicitly decomposed into persisted child tasks, the parent workstream becomes orchestration-only. The first queued/executing wave must be the bounded child tasks, and kickoff remains incomplete if the parent is the only queued work item.
- Archived or closed project/session transcripts from prior runs are excluded from fresh-kickoff planning context. They are only reintroduced when the operator explicitly chooses **resume** or **recovery** mode.
- If fresh kickoff cannot reach initial task creation within the prompt/turn guardrails, the session surfaces one concrete blocker and stops auto-churning.

### The `requires_human_review` Flag

Draft tasks carry a `requires_human_review` boolean that controls whether the PM can queue the task autonomously or needs human sign-off first.

**Two draft paths:**

1. **Human-initiated idea capture**: the human has an idea but hasn't fully thought it out. Task is created as `draft` with `requires_human_review = true`. The PM scopes it out (description, acceptance criteria, flow template) but cannot queue it — they must surface it to the human (via inbox or chat) for review. The human approves for queuing, asks for changes, or kills it.

2. **Agent-initiated scoping**: the PM or another agent creates a task and is actively speccing it out. `requires_human_review = false`. When the PM is satisfied the task has sufficient context, they transition it to `queued` directly.

**Defaults:**

- Human says "capture this idea" → `requires_human_review = true`
- Agent files a blocker → `requires_human_review = false` (PM handles triage)
- PM breaks down project work → `requires_human_review = false` (PM is trusted to scope and queue)
- The human can flip this flag on any task at any time ("don't queue this until I look at it")

**Draft tasks with `requires_human_review = true` appear in the human's inbox** when the PM has finished scoping them and considers them ready for review.

## Task Context

Context is hierarchical — each level adds specificity, and agents always see the full stack relevant to their scope.

### Project Context Block

Every project has a context block maintained by the PM. It is automatically included in every task in the project. This gives any agent working on any task the big picture:

- What the project is and its goals
- Architecture decisions and technical constraints
- Coding conventions and standards
- Key references (repos, external docs, prior art)

The project context evolves as the project progresses — the PM updates it as decisions are made and direction shifts. It maps to the scope context layer (layer 3) in prompt assembly (see 05-agents-staff-and-temps.md).

### Task Context

The structured context the PM adds during scoping. This is what makes a task executable:

- **Description**: what the work is, in plain language
- **Acceptance criteria**: specific, testable conditions for "done"
- **Relevant files/references**: pointers to code, documents, prior decisions, related tasks
- **Constraints**: things the agent must or must not do ("don't change the public API," "must be backward compatible")

A task is not ready to be queued until it has sufficient context for an agent to execute without guessing. The PM is responsible for this — they don't queue half-baked tasks.

### Subtask Context

Subtasks inherit project context and parent task context automatically, plus carry their own narrower scope. An agent working on a subtask sees the full stack:

- Project context (the big picture)
- Parent task context (the objective this subtask contributes to)
- Its own context (the specific piece of work)

This means an agent always knows *why* it's doing what it's doing.

### Conversational Context

Beyond structured context, tasks accumulate context through their sync session — the human and PM hashing out requirements, clarifying ambiguity, feedback from rejected reviews. This is available to agents via the session history and Ellie's memory.

For fresh kickoff, this conversational context starts at the new kickoff request and its canonical project handoff/session. Historical transcripts from archived or closed prior runs are not injected unless the operator explicitly resumes or recovers that earlier run.

## Task Lifecycle

Work status: `draft`, `queued`, `in_progress`, `blocked`, `on_hold`, `review`, `done`, `cancelled`

There is no separate approval state machine. Review and approval are handled by review nodes in the flow template (see Flow Nodes). A review node's approve/reject decision advances or loops the flow, which maps directly to work status transitions (`review` → explicit completion/merge → `done`, or `review` → `in_progress`).

### Work Status Definitions

- `draft`: idea captured but not yet planned. Not available for agents to pick up. Needs to be scoped with structured context (description, acceptance criteria, etc.) before it can move forward. This is the PM's job.
- `queued`: planned and ready for an agent to pick up. Has sufficient context for execution.
- `in_progress`: an agent is actively working on this.
- `blocked`: waiting on a dependency (another task) to be resolved.
- `on_hold`: paused intentionally — not blocked, but not being worked on.
- `review`: task is at a review node in its flow, awaiting reviewer decision.
- `done`: completed and approved. Terminal.
- `cancelled`: abandoned. Terminal.

For tasks backed by a flow template, `in_progress` and `review` are runtime-backed states, not cosmetic labels. A task must not enter or remain in either status unless the control plane can point to a concrete `current_flow_node_id` and at least one corresponding `flow_node_execution` row for that task. If execution state is missing, the scheduler/runtime must deterministically repair it before leaving the task active; if it cannot repair the state, the task must fail closed instead of staying active with zero flow lineage.

### Valid Transitions

```
draft ──→ queued ──→ in_progress ──→ review ──→ done
            │         ↑    │  ↑        │
            │         │    │  │        │ (rejected — back to work)
            │         │    ▼  │        ▼
            │         │  blocked    in_progress
            │         │    │  │
            │         │    │  └──→ cancelled
            │         │    │ (dependency resolved)
            │         │    ▼
            │         │  in_progress
            │         │
            ├─────────┼── on_hold ◄── in_progress
            │         │     │
            │         └─────┘ (resumed → back to queued)
            │
            └──→ on_hold (queued → on_hold)

Any non-terminal state ──→ cancelled
```

Explicit transition table:

- `draft` → `queued`, `cancelled`
- `queued` → `in_progress`, `on_hold`, `cancelled`
- `in_progress` → `review`, `blocked`, `on_hold`, `cancelled`
- `blocked` → `in_progress` (dependency resolved), `on_hold`, `cancelled`
- `on_hold` → `queued` (goes back through the queue, not straight to in_progress), `cancelled`
- `review` → `done` (approved), `in_progress` (rejected/needs changes), `cancelled`
- `done` → terminal. If work needs revisiting, create a new task.
- `cancelled` → terminal. Reachable from any non-terminal state.

## Task Scheduling and Queue

### Queue Mechanics

When a task transitions to `queued`, it enters the scheduling queue. The scheduler manages pickup:

- The scheduler monitors concurrency slots managed by the model gateway (`07-models-and-inference.md`).
- When a slot opens, the scheduler picks the highest-priority eligible task and first ensures the task has concrete flow execution state for its current/start node. Only after that state exists does it transition the task into `in_progress`, resolve the actor, resolve or create the canonical task-scoped async session, and start a run. Project-scoped PM sessions may coordinate the work, but they are never the execution container for that task's kickoff or run history.
- **Dependency-aware scheduling**: a task is only eligible for pickup if all its dependencies are `done`. A task can be `queued` with unresolved dependencies — it sits in the queue but is skipped until dependencies clear. This ensures order of operations without requiring the PM to manually sequence queuing.
- **Project gate scheduling (`blocks_scope = 'all'`)**: if any task in a project has `blocks_scope = 'all'` and `work_status` not in (`done`, `cancelled`), the scheduler may only start the lowest `task_number` among those gate tasks. No other task in that project may start until that gate task reaches `done` or `cancelled`.
- Subtasks within a node also go through the scheduler. They are queued as work units and picked up when slots are available, respecting inter-subtask dependencies.
- Review pauses triggered by runtime policy (for example, async hard-stop checkpoints) still require real flow execution lineage. An artifact, chat message, or inbox item by itself must never be treated as sufficient evidence that a task is legitimately in `review`.

### Priority

Simple levels: `urgent`, `high`, `normal`, `low`.

- PM sets priority during scoping. Human can override at any time.
- Human-escalated blockers default to `urgent`.
- Within the same priority level, FIFO ordering (earliest queued first).

### Concurrency

- Sync sessions always get priority over async work. When a sync session starts and the system is at capacity, an async slot can be preempted.
- **Subtasks within a node run sequentially, not in parallel.** They share a single task branch, so serialization avoids git conflicts. Dependencies between subtasks control ordering; without explicit dependencies, subtasks are processed in creation order. This is also a healthy incentive to keep tasks small — multiple tasks run in parallel (separate branches), but subtasks within a task are serial.
- Per-provider limits (API rate limits) are respected — the scheduler won't start a run if the required model provider is at capacity, even if a global slot is available.

### Resumption

When a blocked task or subtask becomes unblocked (dependency resolved), it re-enters the scheduling queue at its original priority level. The scheduler picks it up when a slot opens, same as any other queued work.

### Rejection Resumption

When a reviewer rejects work and the flow loops back (`review → in_progress`), the task doesn't go back to `queued` — it stays `in_progress` because it's a continuation of the same work, not a fresh pickup. However, the agent's new run still requires a concurrency slot from the scheduler. The status transitions immediately (`in_progress`), but the async run kicks off only when the scheduler allocates a slot. This is the same mechanic as unblocking — status changes are immediate, but execution waits for capacity.

## Flow Templates

### Creation

Flow templates are designed conversationally through the project manager, not through a UI. The PM is opinionated — they propose the right flow based on the type of work, not a menu of options.

The PM asks targeted questions only when they need to disambiguate, then builds the flow. For most work, they should propose and let the human adjust:

> "For implementation work like this, I'd set it up as: write code → code review → merge. The worker handles implementation, the reviewer checks quality, and the explicit merge/completion step closes the task. Sound good, or is there something unusual about this one?"

The PM's flow design skill includes:

- **Opinionated defaults for common work types**: implementation, design, content, research, operations. The PM knows what good workflows look like and doesn't make the human figure it out.
- **Brief reasoning**: they explain why they're recommending a particular structure ("I'm including a human review gate because this touches the billing system"), so the human can calibrate without having to interrogate.
- **Validation**: no orphan nodes, review nodes always have both approve and reject edges, and every executable path includes `work` → `review` → terminal `merge`.

The PM may @mention Lori if there's a staffing question about who should own a particular flow step.

### Immutability

Flow templates are **immutable once a task is using them**. If the flow needs to change, the PM creates a new version of the template. Tasks already in progress keep their original flow. New tasks pick up the updated template.

This avoids the nightmare of a flow changing under a task mid-execution.

### Viewing

The UI shows flow templates as read-only visualizations in the project view. Editing always happens through conversation with the PM.

## Flow Nodes

- Canonical stored node types: `work`, `review`, `decision`, `parallel`, `merge`
- Planner/runtime aliases: `human_review` normalizes to `review` with `requires_human_review = true`; `success` and `completion` normalize to terminal `merge`
- Actor types: `role`, `project_manager`, `human`, `agent`
- Edges: `next_node`, `reject_node`
- Skills: optional list of skill references required for this node. Only these skills are loaded into the agent's prompt during execution of this node (see 10-skills-integration.md).
- **Start node**: identified by `flow_template.start_node_id`. This is the first node activated when a task begins execution.
- **Terminal node**: a `merge` node where `next_node_id` is null. When a task completes that terminal `merge` node, the task transitions to `done` (and enters the merge queue if the project has a repo).

## Flow Progression

Flow advancement is always explicit — never automatic.

- An agent must signal "step done" to advance the flow to the next node. This is a control plane action (`project.flow.advance`) subject to policy evaluation.
- A successful run does NOT automatically advance the flow. The agent may need multiple runs at a single flow node before the work is complete.
- **Nodes with subtasks** cannot advance until all subtasks are `done` (or `cancelled`). The agent signals "step done" after confirming all subtask work is complete.
- **Nodes without subtasks** advance when the agent signals "step done" directly.
- If the agent encounters an issue, it files a blocking task with a dependency link (see Blockers and Escalation). The flow stays at the current node until the dependency is resolved, then the agent resumes.
- For review nodes, the reviewer must explicitly approve or reject. Approval advances to `next_node`; rejection advances to `reject_node`.
- **Rejection back to a node with subtasks**: each visit to a node is a separate `flow_node_execution` record. The original subtasks belong to visit 1. When the flow loops back, a new execution record (visit 2) is created. The PM or worker agent creates new subtasks scoped to the rework — informed by the reviewer's feedback (captured in `project_task_event.comment`). Old subtasks from visit 1 remain `done` or `cancelled` for audit purposes. This keeps a clean separation between attempts.

### Flow Node Execution State

The flow template defines the structure (nodes and edges). When a task moves through a flow, each node has an execution state **per task**:

- `pending` — not yet reached.
- `active` — currently being worked on.
- `blocked` — waiting on a dependency to resolve. The task's work status is also `blocked`.
- `completed` — work done, flow advanced past this node.

**How blocked interacts with the flow:**

1. Agent is working at a node, discovers an issue, files a blocker task with a dependency link.
2. Task work status → `blocked`. Flow node execution state → `blocked`.
3. The agent's current run finishes (it stopped working). The flow stays at this node.
4. When the blocking dependency reaches `done`:
   - Task work status → `in_progress`
   - Flow node execution state → `active`
   - System kicks off a new async run at the same node. The agent picks up where it left off (context from previous runs at this node is available via work log and Ellie).

This applies to both `work` and `review` nodes — a reviewer could discover a blocking issue during review, triggering the same mechanic.

**The task's `current_flow_node_id`** always points to the active or blocked node. Combined with the node execution state, this gives the full picture: "Task is blocked at the Code Review step."

### Runs and Flow Nodes

Every run that happens during a flow node's execution is linked back to the task and the flow node (see 16-agent-control-plane.md Task and Flow Binding). This provides full traceability: what the agent did, what it produced, how much it cost, and how long it took — all scoped to the specific step in the workflow.

This design follows the **nondeterministic idempotence** principle: flow node state, acceptance criteria, and artifacts live outside agent context, so workflows complete regardless of how many agent sessions it takes. If a session crashes or fills its context window, a new session picks up the same flow node using the persisted state and Ellie's memories of prior attempts. For long-running content migration/import tasks, the persisted state must include concrete workspace checkpoints: fetched/raw artifacts, manifests, helper scripts, and incremental migrated output files are written to disk and the next turn resumes from that checkpoint instead of replaying raw page bodies in chat. If a checkpoint shows scripts/artifacts but zero migrated outputs, the continuation contract is to use those persisted files to emit the first real output before creating more scaffolding or re-checking workspace state. See 05-agents-staff-and-temps.md Session Continuity and Crash Recovery for the full model.

## Staff Roles in Flow

- Planner, worker, reviewer role assignments remain project-scoped.
- Flow step owner resolution follows actor type and role assignment.

## Proactive Supervision

Escalation is reactive — the agent has to recognize it's stuck and ask for help. But some agents get silently stuck: spinning in circles, retrying the same approach, not realizing they should escalate.

The PM is responsible for **proactive supervision** of in-progress work. This is a periodic check, not a continuous watch:

- The PM reviews active runs and their progress signals (tool call patterns, elapsed time, lack of forward motion).
- If something looks off — an agent has been working unusually long, or its tool calls suggest it's looping — the PM intervenes proactively. They may redirect the agent, file a blocker on its behalf, or escalate.
- This is part of the PM's responsibilities, not a separate agent role. The PM already has project-level context and knows what each task should look like.
- The system supports this with mechanical signals: doc 16's stuck task detection (heartbeat timeout, orphaned runs) feeds alerts to the PM, who applies judgment.

## Blockers and Escalation

There is no separate blocker entity. Blockers are tasks.

When an agent discovers an issue that prevents progress — a cross-task conflict, a missing decision, a dependency on external input — it files a new task describing the problem. That task is assigned to the project's **project manager** for triage.

### Mechanics

- The agent creates a new task with a dependency link: "my task depends on this new task."
- The agent's task transitions to `blocked` because it has an unresolved dependency.
- The new task is assigned to the project manager by default.
- The PM triages in a project-scoped session: they may resolve it themselves, escalate, or break it into further tasks.
- That PM triage session is not the task's work log. The task's execution/review transcript stays on the canonical task-scoped async session so the operator sees the real task history in task detail. If duplicate blank task sessions exist, the runtime collapses them back to that single canonical task session before dispatching more work.
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

## Inbox

The inbox is the human's action-required queue. Every item requires a decision. It is distinct from notifications (which are awareness) — inbox items block progress somewhere until the human acts.

### Item Types

| Type | Source | Available Actions |
|------|--------|-------------------|
| Task scoping review | PM finished scoping a `requires_human_review` draft | Approve (→ queued), Request changes, Defer |
| Task work review | Task reached a review node with `human` actor type | Approve (→ advance flow), Reject with feedback (→ back to work), Defer |
| Draft action review | Communication tool staged a draft for human review (tool behavior, not policy) | Approve (execute), Edit then approve, Reject, Defer |
| Escalation | PM or Frank escalated a blocker to the human | Respond (opens chat in context), Decide inline, Defer |
| Capability approval | Agent requested a capability that requires human pre-auth | Approve, Approve with constraints, Deny, Defer |

### Item Lifecycle

- `pending` — waiting for human action. Default state on arrival.
- `deferred` — human has seen it, not ready to act. Out of the primary view but accessible in a "Deferred" section. Can be restored to active at any time.
- `acted` — human made a decision. Downstream effect triggered.

No expiry. Items persist until the human deals with them. If something sits deferred for a long time, the PM or Frank can nudge via chat ("Hey, there's a scoping review for X that's been sitting — want to look at it?"). This is the agent-driven version of expiry — natural, not mechanical.

### Mechanics

- Items arrive via the event bus. When a domain event matches an inbox trigger (task reaches review node with human actor, communication tool stages a draft, escalation targets human), an inbox item is created.
- Items are ordered by urgency then arrival time. Escalations and capability approvals sort above task reviews and draft actions.
- Each item carries enough context to decide without leaving the inbox — task description, diff preview, agent reasoning, staged action payload. The human can always "Open in context" to see the full picture.
- Acting on an item triggers the downstream action immediately. No additional confirmation step — the inbox itself is the confirmation.

## Project as Git Repo

Every project is backed by a git repository. Git is the canonical store for all project files — code, documents, configs, versioned artifacts. This applies regardless of whether the project is software, content, or anything else. Everything is versionable.

### Repo Provisioning

- One repo per project, created when the project is created.
- The PM (or the system during project setup) initializes it.
- `main` branch is the source of truth — represents the current approved state of the project.

### Branch Strategy

- When a task starts execution (first flow node kicks off), the control plane creates a task branch: `task/<task-slug>`.
- All work for that task happens on the task branch.
- Subtasks work on the same task branch — they're contributing to the same flow node's work. No sub-branches.
- Tasks that produce no file changes (e.g., sending an email, changing external settings) still create a task branch and enter the merge queue on completion. The merge is a no-op fast-forward processed instantly. The simplicity of "every task has a branch" outweighs the negligible overhead of empty merges.
- When a task completes (reaches `done`), the task branch merges to `main`.
- If the flow has review nodes, the reviewer is effectively reviewing the diff of the task branch against `main`.

### How Agents Commit

- Agents commit to their task branch as they work. Frequent, small commits with descriptive messages.
- Commits are linked to the run that produced them (traceability back to 16-agent-control-plane.md).
- Agents never commit directly to `main` — always through a task branch, always through the flow.

### Review Nodes and Git

- When a task reaches a review node, the reviewer sees the diff: task branch vs the `commit_sha` from the previous node's `flow_node_execution` (or vs `main` if this is the first review). This ensures each reviewer sees only the changes since the last completed step, not the full branch diff.
- When a node completes, the current branch HEAD is stored as `commit_sha` on its `flow_node_execution` record.
- Approve = flow advances. The branch isn't merged yet — it merges when the task reaches `done`.
- Reject = flow sends back to a work node. Agent continues working on the same branch.

### Merge Queue

When multiple tasks complete around the same time, they all want to merge to `main`. Without coordination, the second merge can conflict with the first. The merge queue serializes this.

- Tasks that reach `done` enter the project's **merge queue** rather than merging immediately.
- Merges are processed serially, in priority order then FIFO.
- For each merge: attempt fast-forward or clean merge. If it succeeds, `main` is updated and the next merge proceeds.
- If a merge conflicts: the system attempts auto-resolution (trivial conflicts). If auto-resolution fails, the PM is notified and can either resolve it, assign an agent to rebase the task branch, or escalate.
- The merge queue is visible in the project view — the human can see what's queued, what's merging, and what's blocked on conflicts.
- `merge_queue_entry` rows are never hard-deleted. `archived_at` is set when the **deploy** that includes the entry completes successfully — not at merge time.

### Merge Conflicts

- Parallel tasks on separate branches can conflict. The merge queue catches this at merge time.
- Trivial conflicts (non-overlapping changes to the same file) are auto-resolved.
- Non-trivial conflicts are escalated to the PM, who can assign an agent to rebase and resolve.
- Proactive divergence detection (warning before conflicts materialize) is a future optimization.

### File References in Chat

- `file_ref` blocks in chat messages point to a path + commit SHA in the project's repo.
- File references are immutable — they point to a specific version, not "whatever's on main now."
- Agents can reference files from any branch or commit.

### Non-Code Projects

Same mechanics. A content project stores documents, assets, and drafts in git. Git tracks versions of everything — a blog post goes through the same branch → review → merge flow as code.

## Scheduled Tasks

Some work is recurring — check an inbox every 10 minutes, draft a blog post every week, generate a metrics report daily. Scheduled tasks automate this by creating tasks from a template on a cadence.

### How It Works

A **task schedule** defines: which project, which flow template, how often, and what to do when instances overlap. When the schedule fires, the system creates a task — pre-scoped with context from the schedule definition, `requires_human_review = false`, transitions straight to `queued`. The scheduler picks it up like any other task.

Lightweight recurring work (check inbox) still uses the minimum `work` → `review` → `merge` flow, but the steps may be lightweight. Heavyweight recurring work (weekly blog post) uses the same minimum path with richer node assignments and review requirements. Same mechanism, different templates.

### Creation

The PM creates schedules through conversation, same as everything else. The human says "check my inbox every 10 minutes" and the PM sets it up — flow template, cadence, policies.

The PM should proactively suggest schedules when it recognizes recurring work. If the human keeps requesting the same kind of task, the PM should offer: "Want me to set this up on a schedule so you don't have to ask each time?"

### Scoping

All schedules live within a project. There are no org-level schedules. Org-level recurring work (personal inbox checks, daily summaries, operational maintenance) belongs in an operations project — "Personal Operations," "Org Maintenance," or similar. Frank or the PM can suggest creating this project during onboarding.

### Schedule Cadence

Cron expression (or human-friendly equivalent set by the PM). Examples:

- `*/10 * * * *` — every 10 minutes
- `0 9 * * 1` — every Monday at 9am
- `0 0 1 * *` — first of every month

The PM translates the human's intent into the schedule. The human says "every morning," the PM sets `0 9 * * *` and confirms: "I've set it to run at 9am daily. Want a different time?"

### Overlap Policy

What to do when the previous instance hasn't finished and the next tick fires:

- **skip**: don't create a new task, wait for the next tick. Best for polling-style work where a concurrent check adds no value ("check inbox" — if the last check is still running, starting another is pointless).
- **queue**: create the task anyway, it enters the queue normally. Best for independent work where each instance is a distinct deliverable ("draft blog post" — this week's has nothing to do with last week's).
- **replace**: cancel the still-running instance, create a new one. Best for freshness-sensitive work where a stale run is useless ("generate daily metrics" — yesterday's partial run has no value, start fresh).

### Max Task Duration

Wall clock time from task creation. If the task hasn't reached `done` by this deadline:

- The system cancels it (`cancelled`, with a `project_task_event` comment: "exceeded max duration for scheduled task").
- If the task was `in_progress`, the agent's active run is stopped.
- Prevents zombie tasks from piling up and clogging the queue.
- Separate from the per-turn `max_duration` in doc 02 — that governs a single agent turn, this governs the entire task lifecycle.

The PM sets sensible defaults based on schedule frequency. A task that runs every 10 minutes gets a max duration of ~8 minutes. A weekly task might get 48 hours.

### Instance Naming

Each task created by a schedule needs a unique title and slug. The system generates these from the schedule name plus a timestamp:

- **Title**: `"{schedule name} — {date/time}"` (e.g., "Check inbox — Feb 22 14:30", "Weekly blog post — Feb 17")
- **Slug**: `"{schedule-slug}-{YYYYMMDD-HHmm}"` (e.g., `check-inbox-20260222-1430`)
- **Task number**: assigned from the project's sequence like any other task (`OC-147`, `OC-148`, etc.)

The PM can customize the title template when creating the schedule if the default isn't suitable.

### Pause and Resume

Schedules can be paused and resumed through conversation with the PM. A paused schedule stops creating new tasks but does not cancel any in-progress instances. Resuming picks up from the next scheduled tick — it does not backfill missed instances.

### Visibility

- **Project view**: shows schedules for that project — cadence, last run, next run, recent instance status (succeeded, failed, cancelled, in progress).
- **Global schedules view**: all schedules across all projects. Same information, aggregated. Read-only in both views — editing happens through conversation with the PM.

## Tasks, Subtasks, and Flow

### Sizing Principle

A **task** is a measurable output. It has a flow and moves through flow nodes. A **subtask** is the smallest unit of work a task can be broken into. Subtasks live within a flow node and do not have their own flows.

**If a subtask would need its own flow, the task is too big.** The PM should break it into multiple tasks with dependencies instead. This is the PM's job during scoping — sizing tasks so that subtasks remain simple, atomic work units.

Tasks are flat at the project level. There is no task-to-task parent-child hierarchy. Large efforts are modeled as multiple tasks connected by dependencies, not as a parent task containing child tasks.

### How It Works

Every task has a flow template. The task moves from node to node. At any node, the PM or agent can optionally break the work into subtasks if the scope warrants it.

```
Task: "Implement Auth Middleware"
Flow: [Write Code] → [Code Review] → [Done]
          │
          ├── Subtask: "JWT validation layer"
          ├── Subtask: "Session cookie handling"
          └── Subtask: "Rate limiting middleware"
```

- **Tasks** have flows. They carry the full lifecycle state machine (`draft` → `queued` → `in_progress` → ... → `done`).
- **Subtasks** are scoped to a specific flow node (`task_id` + `flow_node_execution_id`). They do not have their own flows. They are simple work units with a reduced status set: `draft`, `queued`, `in_progress`, `blocked`, `done`, `cancelled`.
- A **flow node with subtasks** cannot advance until all its subtasks are `done` (or `cancelled`).
- A **flow node without subtasks** advances when the agent signals "step done" directly.
- Simple tasks ("fix this typo") flow through nodes with no subtasks. Larger tasks ("implement auth middleware") get subtasks at nodes that need decomposition.

### Who Creates Subtasks

- **PM during scoping**: when the PM plans a task, they may pre-decompose a node's work into subtasks before the task is queued. This is the primary path for structured work where the decomposition is known upfront.
- **Worker agent during execution**: when an agent starts working at a node and realizes the work is decomposable, they create subtasks themselves. The agent is trusted to break down its own work — it doesn't need PM approval for subtask creation.
- **PM during rework**: if a review node rejects work and the flow loops back, the PM may create new subtasks for the rework (or the agent may, based on the reviewer's feedback).

Subtask creation is not a privileged operation. The guard rail is that subtasks are scoped to a flow node execution — they can't exist outside that context.
Queueing a draft task must not infer extra top-level task fan-out just because the description contains multiple bullets or checklists. Parallel child tasks are only created when the planner or flow explicitly marks that task for decomposition.
If decomposition produces parallel child tasks, the parent task stays coordination-only and must not enter `queued` or `in_progress` while executable children still exist.

### Subtask Properties

Subtasks are lighter-weight than tasks:

- Description, acceptance criteria, constraints (same structured context)
- Assignee (can differ per subtask, but subtasks run sequentially on the same task branch)
- Work status (reduced set, no `review` or `on_hold` — subtask review is handled at the task level by the flow's review node)
- Inherit project context + parent task context automatically
- Can have dependencies on other subtasks within the same node, or on external tasks
- One level deep — subtasks cannot have their own subtasks

### Dependencies

Dependencies govern execution order. They are separate from the task/subtask containment relationship.

**Task-level dependencies:**

- A task can depend on other tasks. "What must finish before this can start?"
- Dependencies declared during scoping are respected by the scheduler: a `queued` task with unresolved dependencies remains `queued` but is ineligible for pickup until all dependencies are `done` (see Queue Mechanics). Its status does not change — the dependency is visible via the dependency table, not via work status.
- If a dependency is added to a task that is already `in_progress` (e.g., the agent files a blocker), the task transitions to `blocked`.
- When all dependencies are resolved: a blocked task returns to `in_progress`; a queued task becomes eligible for the scheduler.
- Dependencies are the mechanism behind blockers (see Blockers and Escalation).
- Dependencies form a DAG -- cycles are rejected at creation time. Cross-level dependencies (task depending on a subtask of another task, or vice versa) are not allowed. Dependencies can only be task->task or subtask->subtask within the same parent task.
- **Removing dependencies**: the PM can remove a dependency at any time. When a dependency is removed from a `blocked` task and no other unresolved dependencies remain, the task transitions to its previous active state (`in_progress` or `queued`). Dependency removal is logged in `project_task_event`.

**Project-wide gate tasks (`blocks_scope = 'all'`):**

- This is a scheduler gate, not a dependency edge in the DAG.
- It applies at project scope: while a gate task is outstanding (`work_status` not in `done`, `cancelled`), no other task can start.
- If multiple gate tasks are outstanding, they are processed in strict `task_number` order (lowest first).
- Use this for governance checkpoints (for example: staffing/decomposition kickoff approval) where broad sequencing is required.

**Cancelled dependencies:**

- If a dependency task is cancelled, the downstream task becomes `blocked` and the PM receives a blocker signal (inbox/supervision) tied to that blocked task.
- The downstream task stays `blocked` until the PM resolves the dependency situation — by removing the dependency, creating a replacement task, re-scoping the downstream task, or cancelling it too.
- A separate top-level blocker-resolution task is optional, not automatic. Flows or task metadata can opt into that pattern when a dedicated coordination task is genuinely required, but the default blocker path must not silently enlarge the project's baseline task set.

**Modeling large efforts:**

- Large efforts are modeled as multiple tasks with dependencies between them:

```
"Design auth architecture" → "Implement auth middleware" → "Implement auth tests"
                           → "Implement token service"  → "Integration testing"
```

**Subtask-level dependencies:**

- Subtasks within a node can depend on other subtasks within the same parent task.
- A subtask with unresolved dependencies is `blocked`.
- Cross-level dependencies (subtask depending on a task, or vice versa) are not allowed. Dependencies can only be task->task or subtask->subtask within the same parent task.

### Example

```
Task: "Implement Auth Middleware"
Flow: [Write Code] → [Code Review] → [Final Review] → [Done]

At [Write Code] node (subtasks):
  → PM breaks into subtasks:
     Subtask A: "JWT validation layer"
     Subtask B: "Session cookie handling" (depends on A)
     Subtask C: "Rate limiting middleware"
  → Agents work subtasks sequentially (A first, then B since it depends on A, then C)
  → When A, B, C all done → node can advance
  → Flow advances to [Code Review]

At [Code Review] node (review, no subtasks):
  → Reviewer evaluates all work from [Write Code]
  → Approve → advance to [Final Review]
  → Reject → back to [Write Code] (subtasks may need rework)
```

## Database Schema

### project

```sql
create table project (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  name            text not null,
  slug            text not null,
  description     text,
  context_block   text,          -- project context injected into every task's prompt
  repo_path       text,          -- path to the project's git repo
  status          text not null default 'active' check (status in ('active', 'archived')),
  created_by_type text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id   uuid not null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}',
  unique (organization_id, slug)
);
```

- `slug` is a short, unique identifier for the project within the org (e.g., `oc`, `cp`). Assigned at project creation — the PM proposes one, must be unique within the org. Used as the prefix for all task numbering (`OC-1`, `OC-2`, etc.).
- `project.status` stays coarse-grained (`active` or `archived`). Operator-facing surfaces compute a separate operational state on top of it. When a project has no active run, no eligible queued task, no project-scoped pending/claimed job, and its remaining live tasks are only `blocked`/`review` with at least one true blocker, the project is surfaced as `terminal_stalled` with the blocking task reasons. This is a derived state, not a third persisted `project.status` enum.

### flow_template

```sql
create table flow_template (
  id              uuid primary key default gen_random_uuid(),
  project_id      uuid references project(id),  -- nullable for system-provided default templates (e.g., minimum work-review-merge flows)
  name            text not null,
  description     text,
  version         int not null default 1,
  is_current      boolean not null default true,
  start_node_id   uuid,          -- references flow_node(id), set after nodes are created
  created_by_type text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id   uuid not null,
  created_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);
```

- Immutable once a task is using it. New versions create a new row with incremented `version`; old row gets `is_current = false`.
- `start_node_id` identifies the entry point of the flow — the first node activated when a task begins execution.

### flow_node

```sql
create table flow_node (
  id               uuid primary key default gen_random_uuid(),
  flow_template_id uuid not null references flow_template(id),
  name             text not null,
  description      text,
  node_type        text not null check (node_type in ('work', 'review', 'decision', 'parallel', 'merge')),
  actor_type       text not null check (actor_type in ('role', 'project_manager', 'human', 'agent')),
  actor_role       text,              -- set when actor_type = role: worker, reviewer, planner
  actor_id         uuid,              -- set when actor_type = agent
  position         int not null,      -- UI display ordering; execution order is determined by the graph edges (next_node_id, reject_node_id)
  next_node_id     uuid references flow_node(id),
  reject_node_id   uuid references flow_node(id),  -- review nodes only
  -- Skills are linked via flow_node_skill join table (see doc 10)
  mcp_tools        jsonb,             -- MCP tool declarations: ["connection.tool_name", ...] (see 09-mcp-integration.md)
  tool_domains     jsonb,             -- optional: ["cli", "browser", ...] soft-deprioritizes other domains in tool resolution stage 3 (see 20-tools-and-tool-policy.md)
  metadata         jsonb not null default '{}',
  check (node_type = 'review' or reject_node_id is null),  -- only review nodes can have reject edges
  check (actor_type != 'role' or actor_role is not null)   -- role-typed nodes must specify which role
);
```

### project_task

```sql
create table project_task (
  id                    uuid primary key default gen_random_uuid(),
  project_id            uuid not null references project(id),
  title                 text not null,
  slug                  text not null,
  description           text,
  acceptance_criteria   text,
  constraints           text,
  work_status           text not null default 'draft'
    check (work_status in ('draft', 'queued', 'in_progress', 'blocked', 'on_hold', 'review', 'done', 'cancelled')),
  priority              text not null default 'normal'
    check (priority in ('urgent', 'high', 'normal', 'low')),
  blocks_scope          text not null default 'none'
    check (blocks_scope in ('none', 'all')),
  requires_human_review boolean not null default false,
  flow_template_id      uuid references flow_template(id), -- nullable in draft; set during scoping
  current_flow_node_id  uuid references flow_node(id),
  schedule_id           uuid references task_schedule(id),  -- set when created by a schedule
  branch_name           text,          -- task/<slug>, set when execution starts
  task_number           int not null,  -- sequential per project, auto-incremented
  created_by_type       text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id         uuid not null,
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),
  completed_at          timestamptz,       -- set when task reaches done or cancelled
  metadata              jsonb not null default '{}',
  check (schedule_id is null or requires_human_review = false),
  -- Design note: task assignment is tracked via project_task_participant with role = 'worker', not a dedicated assignee column.
  unique (project_id, slug),
  unique (project_id, task_number)
);

create index on project_task (project_id, work_status);
create index on project_task (work_status, priority);  -- scheduler pickup query
create index on project_task (project_id, blocks_scope, task_number, work_status);  -- project-wide gate resolution
create index on project_task (flow_template_id);
create index on project_task (schedule_id) where schedule_id is not null;  -- query instances of a schedule
```

- Tasks are flat at the project level. No `parent_task_id` — large efforts are multiple tasks with dependencies.
- `task_number` is auto-incremented per project. Display format: `{project_slug}-{task_number}` (e.g., `OC-5`).
- `blocks_scope = 'all'` creates a project-wide scheduler gate. While the task is outstanding, only the lowest-numbered outstanding gate task in the project may start.

### flow_node_execution

```sql
create table flow_node_execution (
  id           uuid primary key default gen_random_uuid(),
  task_id      uuid not null references project_task(id),
  flow_node_id uuid not null references flow_node(id),
  visit        int not null default 1,    -- incremented on rejection loops
  session_id   uuid references chat_session(id),  -- async work session for this execution (see 02-chat.md)
  status       text not null default 'pending'
    check (status in ('pending', 'active', 'blocked', 'completed')),
  commit_sha   text,                      -- branch state when this execution completed (for review diff base)
  started_at   timestamptz,
  completed_at timestamptz,
  metadata     jsonb not null default '{}',
  unique (task_id, flow_node_id, visit)
);

create index on flow_node_execution (task_id);
```

- A node can be visited multiple times (rejection loops). Each visit is a separate execution record with its own timestamps, subtasks, and work log.
- First pass: visit 1. Reviewer rejects, flow loops back: visit 2. Clean metrics per attempt.

### project_subtask

```sql
create table project_subtask (
  id                     uuid primary key default gen_random_uuid(),
  task_id                uuid not null references project_task(id),
  flow_node_execution_id uuid not null references flow_node_execution(id),
  subtask_number         int not null,  -- sequential per task, auto-incremented
  slug                   text not null, -- e.g., "implement-auth-middleware"; unique within task
  title                  text not null,
  description            text,
  acceptance_criteria    text,
  constraints            text,
  work_status            text not null default 'draft'
    check (work_status in ('draft', 'queued', 'in_progress', 'blocked', 'done', 'cancelled')),
  priority               text not null default 'normal'
    check (priority in ('urgent', 'high', 'normal', 'low')),
  assignee_type          text check (assignee_type in ('agent', 'human')),
  assignee_id            uuid,
  created_at             timestamptz not null default now(),
  updated_at             timestamptz not null default now(),
  metadata               jsonb not null default '{}',
  unique (task_id, slug)
);

create index on project_subtask (task_id);
create index on project_subtask (flow_node_execution_id);
create index on project_subtask (task_id, work_status);
```

- Subtasks are scoped to a task + flow node. They are the smallest units of work.
- `subtask_number` is auto-incremented per task. Display format: `{project_slug}-{task_number}.{subtask_number}` (e.g., `OC-5.3`).
- Subtasks run sequentially — they share the task branch, so serialization avoids git conflicts. Dependencies between subtasks control ordering; without explicit dependencies, subtasks are processed in creation order.

### project_task_dependency

```sql
create table project_task_dependency (
  id              uuid primary key default gen_random_uuid(),
  source_type     text not null check (source_type in ('task', 'subtask')),
  source_id       uuid not null,
  depends_on_type text not null check (depends_on_type in ('task', 'subtask')),
  depends_on_id   uuid not null,
  created_at      timestamptz not null default now(),
  check (source_type = depends_on_type), -- no cross-level dependencies
  unique (source_type, source_id, depends_on_type, depends_on_id)
);

create index on project_task_dependency (source_type, source_id);
create index on project_task_dependency (depends_on_type, depends_on_id);
```

- Polymorphic: handles task->task and subtask->subtask dependencies. Cross-level dependencies (subtask->task or task->subtask) are rejected by the `check (source_type = depends_on_type)` constraint.
- DAG enforcement: application layer rejects cycles at creation time.

### project_task_participant

```sql
create table project_task_participant (
  id               uuid primary key default gen_random_uuid(),
  task_id          uuid not null references project_task(id),
  participant_type text not null check (participant_type in ('agent', 'human')),
  participant_id   uuid not null,
  role             text not null check (role in ('planner', 'worker', 'reviewer', 'observer')),
  joined_at        timestamptz not null default now(),
  left_at          timestamptz,
  unique (task_id, participant_type, participant_id)
);

create index on project_task_participant (task_id);
```

### inbox_item

```sql
create table inbox_item (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),
  target_user_id    uuid not null references human_user(id),  -- which human this item is for
  item_type         text not null check (item_type in ('task_scoping_review', 'task_work_review', 'draft_action_review', 'escalation', 'capability_approval', 'browser_handoff')),
  status            text not null default 'pending' check (status in ('pending', 'deferred', 'acted')),
  source_project_id uuid references project(id),
  source_task_id    uuid references project_task(id),
  title             text not null,
  description       text,
  context           jsonb,            -- inline context for deciding without leaving inbox
  action_payload    jsonb,            -- what to execute on approval
  created_by_type   text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id     uuid not null,
  acted_at          timestamptz,
  action_taken      text check (action_taken in ('approved', 'rejected', 'dismissed', 'responded')),
  -- Mapping: "Edit then approve" → approved (with action_notes). "Approve with constraints" → approved (with action_notes). "Request changes" → rejected. "Defer" → changes inbox_item.status to deferred, no action_taken recorded.
  action_notes      text,             -- feedback on rejection, constraints on approval
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  metadata          jsonb not null default '{}'
);

create index on inbox_item (target_user_id, status);
create index on inbox_item (organization_id, status);
create index on inbox_item (source_task_id);
```

### merge_queue_entry

```sql
create table merge_queue_entry (
  id               uuid primary key default gen_random_uuid(),
  project_id       uuid not null references project(id),
  task_id          uuid not null references project_task(id),
  status           text not null default 'queued' check (status in ('queued', 'merging', 'conflict', 'merged')),
  branch_name      text not null,
  priority         text not null default 'normal' check (priority in ('urgent', 'high', 'normal', 'low')),
  queued_at        timestamptz not null default now(),
  started_at       timestamptz,
  completed_at     timestamptz,
  conflict_details text,
  archived_at      timestamptz,              -- set when the deploy including this entry completes successfully
  metadata         jsonb not null default '{}',
  unique (task_id)  -- one entry per task at a time
);

create index on merge_queue_entry (project_id, status);
create index on merge_queue_entry (project_id) where (archived_at is null);
```

### task_schedule

```sql
create table task_schedule (
  id                uuid primary key default gen_random_uuid(),
  project_id        uuid not null references project(id),
  flow_template_id  uuid not null references flow_template(id),
  name              text not null,
  description       text,
  cron_expression   text not null,
  overlap_policy    text not null default 'skip' check (overlap_policy in ('skip', 'queue', 'replace')),
  max_duration_ms   bigint,           -- max wall clock time per instance before auto-cancel
  task_context      text,             -- pre-scoped context injected into each created task
  task_priority     text not null default 'normal' check (task_priority in ('urgent', 'high', 'normal', 'low')),
  status            text not null default 'active' check (status in ('active', 'paused')),
  last_run_at       timestamptz,
  next_run_at       timestamptz,
  created_by_type   text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id     uuid not null,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  metadata          jsonb not null default '{}'
);

create index on task_schedule (project_id, status);
create index on task_schedule (status, next_run_at);  -- scheduler pickup query
```

- Created tasks link back via `project_task.schedule_id` FK for traceability (which schedule created this instance).
- `next_run_at` is pre-computed from the cron expression. The scheduler queries for active schedules where `next_run_at <= now()`.
- `last_run_at` updated each time a task is created from this schedule.

### project_task_event

```sql
create table project_task_event (
  id              uuid primary key default gen_random_uuid(),
  task_id         uuid not null references project_task(id),
  event_type      text not null check (event_type in ('status_change', 'flow_advance', 'flow_reject', 'blocked', 'unblocked', 'assigned', 'subtask_created', 'subtask_completed', 'merged', 'push_succeeded', 'push_failed', 'deployed', 'escalated', 'dependency_added', 'dependency_removed', 'schedule_cancelled')),
  from_status     text,              -- previous work_status (for status_change events)
  to_status       text,              -- new work_status (for status_change events)
  flow_node_id    uuid references flow_node(id),  -- which node this event relates to
  visit           int,               -- which visit of the node
  actor_type      text not null check (actor_type in ('human', 'agent', 'system')),
  actor_id        uuid not null,
  comment         text,              -- why this happened: rejection reason, blocker description, escalation context
  created_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);

create index on project_task_event (task_id, created_at);
```

- The full history of a task's lifecycle. Every state transition, flow advancement, rejection, blocker filing, and escalation is recorded with who did it and why.
- `comment` is the key field — captures the reasoning: "Reviewer rejected: error handling doesn't cover timeout case", "Blocked on OC-12: missing API credentials", "PM escalated to Frank: cross-project conflict".
- Append-only. Events are never edited or deleted.
- Powers the History section in the task detail UI.

### Design Notes

- **12 tables** for the project/task domain.
- Tasks are flat — no parent-child between tasks. Dependencies handle ordering.
- Subtasks are contained within tasks, scoped to flow nodes. They're the atomic work units.
- The dependency table is polymorphic (task->task and subtask->subtask within the same parent) but enforces same-level dependencies via a check constraint — no cross-level dependencies (subtask depending on a task, or vice versa).
- `inbox_item` carries enough context (`context` jsonb) to make decisions inline without navigating away.
- `merge_queue_entry` is separate from task status — a task can be `done` but still waiting in the merge queue. Merge state is not duplicated on the task; the UI queries both tables to show the full picture ("Done, merge pending" or "Done, merge conflict"). If a merge conflicts, the PM creates a new task or assigns an agent to resolve it on the branch.
- `project_task_event` provides a complete audit trail of every task's journey through its lifecycle.

## Optional V2 Extensions

- Relationship types: duplicate, supersedes, relates_to, replies_to.
- Templates that include default labels/checklists.

## Resolved Decisions

- **Tasks are always created through agents/chat, never through UI.** The human talks to agents; agents create tasks. No task creation forms, no "new task" buttons. The UI is for viewing, navigating, and reviewing.
- **`requires_human_review` flag gates draft → queued transition.** Human-initiated ideas default to `true` (PM scopes but human approves). Agent-initiated work defaults to `false` (PM is trusted). Human can flip the flag at any time.
- **No separate approval state machine.** Review and approval are handled by review nodes in the flow template. A review node's approve/reject advances or loops the flow.
- **Tasks are flat at the project level.** No task-to-task parent-child hierarchy. Large efforts are modeled as multiple tasks with dependencies.
- **Subtasks are scoped to flow node executions, not tasks.** A subtask belongs to a specific visit of a specific node. If a review rejects and the flow loops back, the new visit gets fresh subtasks.
- **Tasks have flows, subtasks do not.** If a subtask would need its own flow, the task is too big — break it into multiple tasks. The PM handles this during scoping.
- **Subtasks run sequentially within a node.** They share the task branch, so serialization avoids git conflicts. This incentivizes keeping tasks small since multiple tasks run in parallel on separate branches.
- **Who creates subtasks**: PM during scoping, worker agent during execution, PM during rework. Not a privileged operation.
- **Flow templates are designed conversationally through the PM.** The PM is opinionated — proposes the right flow, doesn't present menus. UI shows flows read-only; editing is always through conversation.
- **Flow templates are immutable once a task is using them.** New versions create new rows. In-progress tasks keep their original flow.
- **Flow progression is always explicit.** Agent must signal "step done." Run completion ≠ flow advancement. Multiple runs per node is normal.
- **Rejection loops create new flow_node_execution records.** Visit counter tracks which attempt. Each visit has its own subtasks, session, timestamps, and commit_sha.
- **`commit_sha` on flow_node_execution for review diff base.** Each reviewer sees only changes since the last completed node, not the full branch diff.
- **Blockers are tasks, not a separate entity.** Agent files a new task with a dependency link. PM triages. Escalation: PM → Frank → human.
- **`blocks_scope = 'all'` is a project-wide execution gate.** While any gate task is outstanding, only the lowest-numbered outstanding gate task in that project may start.
- **Every new project starts with a bootstrap gate task.** Task 1 is a `blocks_scope = 'all'` governance flow: Lori does staffing/decomposition work, Frank reviews it, and an explicit completion/merge node closes the gate before other tasks can start.
  The kickoff/bootstrap conversation in the canonical project session is an active bounded workflow, not a passive chat plan. After Frank hands off to Lori, the system may enqueue automatic follow-on bootstrap turns until persisted setup records exist or the session records an explicit bootstrap failure instead of silently idling.
  The project session metadata carries a machine-checkable `project_bootstrap` contract (`active`, `completed`, or `failed`) with persisted-count progress so operators and UI clients can see whether staffing/task/flow setup actually materialized.
- **Cancelled dependencies create resolution tasks.** When a dependency is cancelled, the system auto-creates a task for the PM to resolve the situation. Blocked progress creates tasks, not notifications.
- **Dependencies can be removed.** PM can unlink a dependency at any time. If a blocked task has no remaining unresolved dependencies, it resumes.
- **Dependencies form a DAG.** Cycles rejected at creation time.
- **Inbox is the human's action-required queue.** Distinct from notifications. Items persist until acted on — no expiry. Deferred items are accessible but out of the primary view. PM nudges via chat if items sit too long.
- **Every project is a git repo.** One repo per project. `main` is the source of truth. Task branches: `task/<slug>`. Subtasks share the task branch.
- **Merge queue serializes merges to main.** Tasks enter the queue when `done`. Serial processing. Trivial conflicts auto-resolved; non-trivial escalated to PM.
- **Merge state lives on `merge_queue_entry`, not duplicated on the task.** A task can be `done` but still in the merge queue. UI queries both tables.
- **Proactive supervision is the PM's responsibility.** PM periodically reviews active runs for stuck agents. System provides mechanical signals (heartbeat timeout, orphaned runs) but PM applies judgment.
- **Scheduled tasks use the same task primitive.** A schedule creates tasks from a template on a cron cadence. Lightweight recurring work still uses the minimum work-review-merge flow. Everything lives within a project — org-level recurring work goes in an operations project.
- **Three overlap policies for scheduled tasks**: skip (don't create if previous is running), queue (create anyway), replace (cancel previous and create new).
- **Max task duration on schedules.** Wall clock time from task creation. Exceeded = auto-cancel. Prevents zombie pileup.
- **Schedules are created, paused, and edited through conversation with the PM.** UI shows schedules read-only. PM should proactively suggest schedules when it recognizes recurring patterns.
- **Task numbering is per-project with project slug prefix.** Tasks: `OC-1`, `OC-5`. Subtasks: `OC-5.1`, `OC-5.3`. Project slugs are unique within the org.
- **`review → in_progress` doesn't re-enter the queue** but still requires a scheduler slot. Status transitions immediately; execution waits for capacity.
- **`on_hold → queued`** (goes back through the queue, not straight to `in_progress`).
- **`done` and `cancelled` are terminal.** If work needs revisiting, create a new task.
- **Task event log (`project_task_event`) is the audit trail.** Append-only. Every state transition, flow advancement, rejection, blocker, escalation, dependency change recorded with actor and comment.
- **Queued tasks with unresolved dependencies remain `queued` but ineligible for scheduler pickup.** Only `in_progress` tasks transition to `blocked` when a dependency is added. The dependency is visible via the dependency table, not via work status.
- **Tasks that produce no file changes still follow the branch/merge flow.** Empty merges are no-op fast-forwards processed instantly by the merge queue. The uniform model is simpler than special-casing tasks by whether they produce files.
- **Push outcomes (`push_succeeded`, `push_failed`) are recorded as task events** on the originating task whose merge triggered the push. See 03a for push mechanics.

## Open Questions

_None currently outstanding._
