---
## Summary

This spec defines the Agent Control Plane, the single trusted execution layer through which every agent mutation in OtterCamp flows. No agent can write to the database, execute a shell command, call an external service, or perform any side-effecting action without passing through this system. The only exception is tier 1 (read-only) tool calls, which execute in the chat layer with a basic scope check. The control plane is built around four core components: the **Policy API** (stateless capability evaluation returning allow or deny), the **Execution Broker** (synchronous admission control that creates Run records, evaluates policy, and dispatches to workers), the **Worker Runtime** (stateless sandboxed executors for internal mutations, CLI, browser, and MCP domains), and the **Audit/Event Log** (immutable, append-only record of all decisions and outcomes).

The capability model uses a hierarchical dot-separated namespace (`domain.resource.action`, e.g. `system.cli.execute`, `project.task.create`) with wildcard support. Agents receive generous capabilities from templates: reader (read-only), worker (project mutations + file I/O + CLI + browser + memory), deployer (worker + external comms), and admin (all). The default experience is permissive — agents are empowered to do their jobs. Admins add `deny` rules when they want restrictions. Policies compose across five layers — instance safety (hardcoded, highest priority), organization, project, agent profile, and request-specific overrides (can only restrict, never expand) — using a most-restrictive-wins principle. Policy evaluation is binary: `allow` (execute immediately) or `deny` (reject).

The execution model is captured in an 8-table schema anchored by the `run` entity. A Run represents one end-to-end action with principal identity, capability, policy decision, and result. Runs decompose into RunSteps (sequential stages), RunAttempts (retry envelopes preserving failure history), ToolExecutions (per-tool-call records with policy decisions), RunArtifacts (files, screenshots, logs in object storage), and RunEvents (append-only timeline for replay, debugging, and health monitoring via heartbeats). `runtime_state` stores the current execution owner for each task/session boundary and the authoritative resume contract for that boundary: bound task/session/flow ids, latest progress heartbeat, pending deferred wakeup info, failure disposition, and retirement status. Same-owner wakeups can be coalesced and different-owner wakeups can be deferred without overlapping work, and recovery reads this record first instead of reconstructing from scattered rows. Model invocations are tracked in the `model_invocation` table defined in doc 07, which carries control plane FKs (`run_id`, `run_step_id`, `run_attempt_id`) for cross-referencing. Runs bind to tasks and flow nodes for observability, enabling per-task and per-flow-node token breakdowns. The remaining table is `capability_policy` (declarative policy rules with binary outcomes). Reliability is handled through idempotency keys, optimistic concurrency, explicit retry attempts, dead letter handling, a background supervisor for stuck/orphaned task detection, explicit single-owner wakeup handoff, runtime-state retirement on archive/cancel/completion, and soft/hard budget enforcement expressed in tokens (soft budget = notify admin, hard budget = deny).

---

# 16. Agent Control Plane

## Purpose

Define how agents get real operational control over OtterCamp and external systems while preserving safety, auditability, and multi-tenant isolation. The control plane is the single trusted execution layer — every agent mutation flows through it. There is no back door.

## Outcome

Agents can perform meaningful work (project updates, tool execution, CLI and browser actions, MCP calls) through one trusted runtime path with explicit permissions and full traceability. Every action is attributable, every decision is auditable, and every failure is recoverable.

## Scope

- In scope:
  - Agent authorization model (principal identity, delegation)
  - Capability system (namespaced permissions, templates, grants)
  - Policy evaluation and risk gating (layered composition, conflict resolution)
  - Execution broker and worker orchestration (admission, dispatch, sandboxing)
  - Token accounting (token aggregation, budget enforcement)
  - Reliability and failure repair (stuck detection, orphaned recovery, heartbeats)
  - Audit and observability requirements
  - Full execution entity schema
- Out of scope:
  - UI details for admin configuration (see 17-tui.md, 18-web-ui.md)
  - Provider-specific model tuning internals (see 07-models-and-inference.md)
  - Tool taxonomy and registration (see 20-tools-and-tool-policy.md)
  - MCP connection management (see 09-mcp-integration.md)

## Core Principles

1. **Agent actions are never direct.** All mutations pass through the control plane. No agent writes to the database, executes a command, or calls an external service without the broker's involvement. Read-only internal operations (tier 1 tools) are the sole exception — they execute in the chat layer with a basic scope check.

2. **Every action has an accountable principal.** Human or agent identity is attached to every request. When a human delegates, the delegation chain is recorded. You can always answer "who caused this?"

3. **Capabilities are template-driven and generous by default.** Agents receive capabilities from templates that are designed to let them do real work. Restrictions are opt-in — admins add `deny` rules when they need guardrails, rather than allowlisting from zero. Grants are namespaced, scoped, and audited.

4. **Policy evaluation is binary: allow or deny.** `allow` executes immediately. `deny` rejects immediately. The outcome is always immediate and deterministic.

5. **Every action is replayable from immutable execution records.** Runs, steps, attempts, tool executions, artifacts, and events form a complete audit trail. Nothing is lost.

6. **Predictability over intelligence.** Policy outcomes are deterministic based on static configuration. The human knows exactly what is allowed and denied because they set the policy. No surprises.

## Control Plane Components

### Policy API

Stores and evaluates capability policies. Stateless evaluation — given a principal, capability, resource scope, and runtime context, it returns `allow` or `deny`. Policy rules are stored in the database and cached aggressively.

The Policy API is the single authority for "can this principal do this thing?" Every other component defers to it. There is no second policy evaluation path.

### Execution Broker

Admission control for all requested actions. The broker is the front door of the control plane:

1. Receives an action request from the chat layer (tier 2 tool call).
2. Identifies the principal and requested capability.
3. Calls the Policy API.
4. Routes based on the decision:
   - **deny**: return "not permitted" error to the agent.
   - **allow**: dispatch to the appropriate worker for execution.
5. Creates and manages Run/RunStep/RunAttempt records.
6. Emits events to the realtime channel and audit log.

The broker is not a queue — it is synchronous admission control. Queuing happens at the scheduler level (task queue) and the model gateway level (concurrency limits), not here.

### Worker Runtime

Executes approved actions in controlled sandboxes. Workers are stateless — they receive a dispatch message from the broker, execute the action, and return results. Different worker types handle different execution domains:

- **Internal worker**: OtterCamp mutations (create task, advance flow, update status). Fast — just database operations behind capability checks.
- **CLI worker**: shell command execution in sandboxed environments.
- **Browser worker**: browser automation in isolated contexts.
- **MCP worker**: external service calls via MCP connections.

### Audit/Event Log

Immutable record of decisions and outcomes. Every policy evaluation, every broker decision, and every execution result is logged. This is append-only and tamper-evident.

Policy decisions and access control events are recorded as `run_event` entries. Events that occur outside a run context (e.g., policy changes, capability updates) are recorded in doc 04's `audit_event` table.

## Canonical Execution Entities

These entities form the execution model. They are the authoritative record of what happened when an agent acted.

### Run

One end-to-end unit of work. A run is created when the broker receives an action request. It encompasses the full lifecycle: admission, policy evaluation, execution, and result capture.

A run always has:
- A principal (who requested it)
- A capability (what was requested)
- A policy decision (what the Policy API said)
- A result (what happened)

Runs bind to the task/flow context when the action happens within a task.

### RunStep

One stage inside a run. For simple actions (create a task, advance a flow), a run has one step. For compound actions (CLI execution with setup/teardown, multi-step browser workflows), a run has multiple steps.

Steps are sequential within a run. Each step has its own status, timing, and output.

### RunAttempt

Retry envelope for a step. When a step fails and is retried, a new attempt is created rather than overwriting the failed one. This preserves the full failure history.

Attempts are the mechanism for:
- Transient failure retry (model call timed out, rate limited)
- Manual retry (operator triggers retry of a failed step)

### ModelInvocation (Doc 07)

Model invocations are tracked in the `model_invocation` table defined in doc 07. That table carries `run_id`, `run_step_id`, and `run_attempt_id` FK columns so that model calls made during control plane execution can be attributed back to specific runs. This includes:
- The model call made by the chat layer during the turn (linked via turn_id)
- Any model calls made by workers during execution (e.g., a browser worker using a vision model to analyze a screenshot)

Model invocations feed into token tracking (see Token Accounting below) and are the raw data for the usage dashboards in doc 13.

### ToolExecution

One tool call with structured input/output. Every tier 2 tool call routed through the control plane is captured. This includes the tool name, input arguments, output, timing, and the policy decision that authorized it.

ToolExecution records are the control plane's view of tool calls. The chat layer also records tool calls as `tool_call`/`tool_result` messages. A complete agent activity view requires both — the chat messages for the conversation context and the ToolExecution records for the policy/execution detail.

### RunArtifact

Files, screenshots, log outputs, and other binary content produced by execution. Stored in object storage (S3-compatible), referenced by the artifact record. Linked to the run and step that produced them.

RunArtifacts are distinct from `chat_artifact` (doc 02). Chat artifacts are produced during conversation (file uploads, inline images). Run artifacts are produced during execution (CLI output logs, browser screenshots, generated files).

### RunEvent

Append-only timeline event for replay and debugging. Every state transition, every notable occurrence during execution, is captured as an event. Events are the raw material for:
- Execution replay (what happened, in what order)
- Debugging (where did it fail, what was the state)
- Observability (dashboards, alerting)

## Task and Flow Binding

When a run executes in the context of a task, it carries:

- `task_id`: the task this run is working on.
- `flow_node_id`: the specific flow node this run is executing within.

### Relationship Rules

- A flow node can have **multiple runs**. An agent may need several runs to complete a step — working, discovering issues, querying Ellie, resuming after a blocker is resolved.
- **Run completion does not advance the flow.** A run finishing is an execution event, not a workflow signal. The agent must explicitly signal that the flow step is done.
- The agent signals step completion via a dedicated action (`project.flow.advance`). This is a control plane action subject to normal policy evaluation.
- If the agent encounters an issue before completing the step, it files a blocking task (see 03-projects-and-task-flow.md Blockers and Escalation). The flow stays at the current node until the dependency is resolved.

### Observability

Because runs link to tasks and flow nodes, you can answer:

- What runs happened for this task? For this flow node?
- How many tool calls, model invocations, and tokens were spent on this step?
- What artifacts were produced?
- How long did the agent spend at this flow node (across all runs)?
- Where in the run timeline did the agent file a blocker?
- What was the total token usage for completing this task, broken down by flow node?

### Single Active Execution Owner

Task and async-session execution uses a single-owner contract:

- Each task/session execution boundary has at most one active owner at a time.
- `runtime_state` stores the active run, active principal, lock acquisition time, last wakeup timestamp, and metadata contract needed to resume that boundary safely.
- A wakeup from the same principal is coalesced onto the active run and logged as `wakeup_coalesced`.
- A wakeup from a different principal creates or reuses a deferred run and is logged as `wakeup_deferred`.
- When the active owner exits cleanly or is declared stale, the oldest deferred wakeup is promoted, logged as `wakeup_promoted`, and becomes the new active owner.
- If the stored active run is already terminal, missing, or heartbeat-stale, the control plane clears ownership, fails the abandoned run with a stale-handoff reason, and promotes deferred work once.
- When no owner is active, `runtime_state` still remains as a resumable contract until task completion/archive/cancel retires it. Recovery uses this record before it looks at session rows, queue rows, or stale run timestamps.

This prevents overlapping reviewer/worker turns, duplicate queue wakeups, and recovery races on the same task.

## Agent Principal Model

Each action request includes a principal context that identifies who is requesting the action and under what authority.

### Principal Fields

- `principal_type`: `agent`, `human`, or `system`
- `principal_id`: the agent or human user ID (sentinel UUID `00000000-0000-0000-0000-000000000000` for system actions)
- `organization_id`: the org this action is happening within
- `project_id`: the project scope (null for org-level actions)
- `session_id`: the chat session this action originated from
- `task_id`: the task context (null for non-task actions)
- `flow_node_id`: the flow node context (null for non-flow actions)
- `delegated_by`: when a human explicitly delegates authority to an agent, this records the human's identity. Creates an auditable delegation chain.

### Principal Resolution

The principal is determined by the chat layer before the action reaches the broker:

- **Agent turn in async session**: principal is the agent. `session_id` and `task_id` are set from the session scope.
- **Agent turn in sync session**: principal is the agent. `delegated_by` is set to the human who is in the session, since the human's presence implies oversight.
- **Human-initiated action via chat**: principal is the human. The agent is the executor, but the human is the accountable party.

### Delegation and Authority

When a human says "go ahead, deploy that" in a sync session, the agent's subsequent actions carry `delegated_by` pointing to the human. This matters for policy evaluation — some capabilities may only be available when a human has explicitly delegated.

Delegation is session-scoped. It does not persist across sessions. The human's presence in a sync session is implicit delegation for that session's scope.

## Capability Model

Capabilities are namespaced action permissions. Every mutation an agent can perform maps to exactly one capability. The capability namespace is the bridge between "what the agent wants to do" and "what policy says about it."

### Namespace Structure

Capabilities follow a hierarchical dot-separated namespace: `{domain}.{resource}.{action}`. Wildcards are supported for policy rules: `project.*` grants all project capabilities, `project.task.*` grants all task operations.

### Full Capability Catalog

**Project domain** — operations on projects and their contents:

| Capability | Description |
|---|---|
| `project.read` | Read project metadata, context block, settings |
| `project.update` | Update project metadata, context block |
| `project.task.create` | Create a new task |
| `project.task.read` | Read task details, status, context |
| `project.task.update` | Update task description, acceptance criteria, priority |
| `project.task.status.update` | Transition task work status |
| `project.task.assign` | Assign/reassign agents to a task |
| `project.task.cancel` | Cancel a task |
| `project.subtask.create` | Create a subtask within a flow node |
| `project.subtask.update` | Update subtask details |
| `project.subtask.status.update` | Transition subtask work status |
| `project.flow.advance` | Signal flow step completion, advance to next node |
| `project.flow.blocker.raise` | File a blocking task with dependency link |
| `project.dependency.create` | Add a dependency between tasks/subtasks |
| `project.dependency.remove` | Remove a dependency |
| `project.participant.add` | Add a participant to a task |
| `project.participant.remove` | Remove a participant from a task |
| `project.schedule.create` | Create a task schedule |
| `project.schedule.update` | Update schedule cadence, overlap policy |
| `project.schedule.pause` | Pause a schedule |
| `project.schedule.resume` | Resume a paused schedule |
| `project.merge.initiate` | Trigger a merge from the merge queue |
| `project.merge.resolve` | Resolve a merge conflict |

**Chat domain** — operations on chat sessions and messages:

| Capability | Description |
|---|---|
| `chat.session.create` | Create a new chat session |
| `chat.session.read` | Read session metadata and messages |
| `chat.session.close` | Close a chat session |
| `chat.message.write` | Send a message in a session |
| `chat.participant.add` | Invite a participant to a session |
| `chat.participant.remove` | Remove a participant from a session |
| `chat.artifact.create` | Create/upload an artifact in a session |

**Memory domain** — operations on Ellie's memory system (doc 06):

| Capability | Description |
|---|---|
| `memory.write` | Create or update memory items |
| `memory.entity.create` | Create a new entity in the knowledge graph |
| `memory.entity.update` | Update entity attributes or relationships |
| `memory.entity.merge` | Merge duplicate entities |

**System domain** — operations on the host system:

| Capability | Description |
|---|---|
| `system.file.write` | Write/create/delete files in the project workspace |
| `system.cli.execute` | Execute shell commands |
| `system.browser.navigate` | Navigate to a URL in a browser session |
| `system.browser.interact` | Click, type, scroll in a browser session |
| `system.browser.screenshot` | Capture a screenshot |
| `system.browser.extract` | Extract structured data from a page |

**MCP domain** — operations via MCP connections (doc 09):

| Capability | Description |
|---|---|
| `mcp.connection.use:<connection_id>` | Use a specific MCP connection |
| `mcp.tool.invoke:<connection_id>:<tool_name>` | Invoke a specific tool on a specific connection |
| `mcp.tool.invoke:<connection_id>:*` | Invoke any tool on a specific connection |

**Communication domain** — outbound communications:

| Capability | Description |
|---|---|
| `comms.email.draft` | Draft an email for human review |
| `comms.email.send` | Send an email |
| `comms.slack.post` | Post to a Slack channel |
| `comms.github.pr.create` | Create a GitHub pull request |
| `comms.github.pr.comment` | Comment on a GitHub pull request |

**Agent domain** — operations on other agents:

| Capability | Description |
|---|---|
| `agent.profile.read` | Read another agent's profile |
| `agent.profile.update` | Update an agent's profile (Lori-level) |
| `agent.temp.create` | Create a temporary agent |
| `agent.temp.retire` | Retire a temporary agent |

Tier 1 tools (`memory.read`, `system.file.read`, and other read-only chat-layer tools) are not listed here — they use basic scope checks in the chat layer, not capability grants. See doc 02.

### Capability Templates

**Post-Genesis.** At Genesis, all humans are org admins and capability templates are not enforced. The template definitions below are the target design for role-based capability assignment.

Predefined sets of capabilities for common agent roles. Templates simplify policy configuration — instead of granting individual capabilities, assign a template. Templates can be customized per agent after assignment.

**`reader`** — read-only access. Safe for any agent that only needs to observe. (Tier 1 reads like `memory.read` and `system.file.read` are not listed — they use scope checks, not capability grants.)

```
project.read
project.task.read
chat.session.read
agent.profile.read
```

**`worker`** — the standard template for agents doing real work. Includes project mutations, file I/O, CLI, browser, and memory. Workers can build, test, and interact with systems. No external communications.

```
# Everything in reader, plus:
project.task.create
project.task.update
project.task.status.update
project.subtask.create
project.subtask.update
project.subtask.status.update
project.flow.advance
project.flow.blocker.raise
project.dependency.create
project.participant.add
chat.message.write
chat.artifact.create
memory.write
memory.entity.create
system.file.write
system.cli.execute
system.browser.navigate
system.browser.interact
system.browser.screenshot
system.browser.extract
```

**`deployer`** — everything in worker plus external communications. For agents that need to send emails, post to Slack, or create PRs.

```
# Everything in worker, plus:
comms.*
```

**`admin`** — all capabilities. Reserved for the PM and org-level agents (Frank, Lori) who need unrestricted access within their scope.

```
*
```

Templates are starting points, not prisons. The human or PM can add or remove individual capabilities from any agent's effective set. The template just provides a generous baseline.

The default out-of-box experience is permissive. The starter trio (Frank, Lori, Ellie) get `admin`. Most task-working agents get `worker`. Agents that need to communicate externally get `deployer`.

### Capability Scoping

Capabilities are always evaluated within a scope:

- **Org-scoped**: `project.read` at org level means the agent can read any project in the org.
- **Project-scoped**: `project.task.create` at project level means the agent can create tasks in that specific project.
- **Task-scoped**: `system.file.write` at task level means the agent can write files in that task's workspace.

The narrowest scope wins. An agent with org-level `project.read` and no project-level grants can still read all projects. An agent with project-level `system.cli.execute` only for Project A cannot execute commands in Project B's workspace.

## Policy Evaluation

### Policy Decision Outcomes

- **`allow`**: execute the action immediately. Dispatch to the worker. Return result to the agent's turn loop.
- **`deny`**: reject the action. Return "not permitted" as the tool result. The agent sees this and can adapt. The run is created and immediately failed (for audit).

Agents receive generous capabilities from templates. The default experience is permissive — agents can do real work out of the box. Admins add `deny` rules at the org or project layer when they want restrictions. The system trusts agents to do their jobs; guardrails are opt-in, not opt-out. Instance safety (layer 1) remains the hardcoded safety net for truly catastrophic actions — that is the only default-deny layer.

### Policy Inputs

The Policy API evaluates based on:

- **Principal identity**: who is requesting (agent ID, human ID, delegation chain)
- **Principal role**: the agent's role in this context (PM, worker, reviewer, etc.)
- **Capability requested**: the namespaced capability string
- **Target resource scope**: org, project, task, or specific resource ID
- **Runtime budgets**: remaining token budget and time budget for this org/project

### Policy Layers

Policies compose across five layers. Each layer can allow or deny any capability. Layers are evaluated highest-priority-first:

1. **Instance safety policy** (highest priority)
   - Hardcoded. Cannot be overridden by any lower layer.
   - Prevents catastrophic actions: no `rm -rf /`, no sending to domains on the global blocklist, no accessing other orgs' data.
   - This is the "even if everything else is misconfigured, this saves you" layer.

2. **Organization policy**
   - Set by the org owner/admin.
   - Org-wide restrictions: deny rules for specific capabilities.
   - Budget limits: max tokens per agent per day, max concurrent runs.
   - Example: "All `comms.email.send` actions denied org-wide unless the agent has the deployer template."

3. **Project policy**
   - Set by the human or PM for a specific project.
   - Project-specific overrides: a project working with sensitive data might deny `system.cli.execute` even if the org allows it.
   - Example: "In the billing project, `system.cli.execute` denied."

4. **Agent profile policy**
   - Set on the agent's profile (doc 05).
   - The agent's personal capability set, typically assigned via a template.
   - Example: "This worker agent has the `worker` template plus `system.cli.execute`."

5. **Request-specific overrides** (lowest priority, most restrictive only)
   - Applied by the system at request time based on runtime context.
   - Can only restrict, never expand. A request-specific override can downgrade `allow` to `deny`, but never upgrade `deny` to `allow`.
   - Example: agent has exceeded the hard cost budget — non-essential capabilities denied.

### Composition Rules

Policy layers compose using a **most-restrictive-wins** principle with clear precedence:

1. Start with the agent profile policy (layer 4) as the base. This determines what capabilities the agent has been granted.
2. Apply project policy (layer 3). If the project denies a capability the agent has, the project wins.
3. Apply org policy (layer 2). Same logic — org restrictions override project permissions.
4. Apply instance safety (layer 1). Absolute override. Nothing can override instance safety.
5. Apply request-specific overrides (layer 5). Can only make things more restrictive.

**A lower-priority layer can never grant what a higher-priority layer denies.** Project policy cannot allow something the org denies. Agent profile cannot allow something the project denies.

**A lower-priority layer CAN be more restrictive.** An agent profile can deny capabilities the project allows (the agent simply does not have them). A project can deny something the org allows freely.

### Conflict Resolution

When multiple layers express opinions about the same capability:

- `deny` at any layer = deny. Period. No lower layer can override.
- `allow` only if all applicable layers allow (or are silent). Silence is not denial — if a layer has no opinion on a capability, it passes through.

**Example walkthrough:**

Agent "CodeBot" requests `system.cli.execute` in the billing project:
1. Instance safety: no opinion on `system.cli.execute` in general (it has specific command blocklists, but the capability itself is not blanket-denied). Pass.
2. Org policy: `system.cli.execute` = `allow`. Pass.
3. Project policy (billing): `system.cli.execute` = `deny`. Override to `deny`.
4. Agent profile: CodeBot has `deployer` template, which includes `system.cli.execute`. Has the capability.
5. Request-specific: no overrides active.

Result: `deny`. The billing project's policy dominates.

### Policy Caching

Policy evaluation must be fast — it is in the hot path of every tier 2 tool call. Policies are cached at the broker level:

- Org and project policies are cached with a short TTL (seconds, not minutes). Changes propagate quickly but not instantly.
- Agent profile policies are cached per-session (rebuilt when the session starts or the agent's profile changes).
- Instance safety rules are compiled into the broker at startup and refreshed on deploy.
- Cache invalidation on policy write: the Policy API emits an event when a policy changes, and the broker invalidates its cache.

## Execution Lifecycle

### The 8-Step Flow (Expanded)

```
1. Agent requests an action (tier 2 tool call in the turn loop)
   │
2. Broker creates Run record (status: created)
   │   - Sets principal context (agent/human, org, project, task, flow node)
   │   - Assigns idempotency key
   │   - Creates initial RunStep (status: pending)
   │
3. Broker evaluates policy
   │   - Calls Policy API with principal + capability + scope
   │   - Records policy decision as RunEvent
   │
4. If DENY:
   │   - Run status → failed
   │   - RunStep status → failed
   │   - RunEvent: policy_denied with reason
   │   - Return "not permitted: {reason}" as tool result
   │   - Agent's turn continues
   │
5. If ALLOW:
   │   - Run status → in_progress
   │   - RunStep status → in_progress
   │   - Create RunAttempt (attempt_number: 1)
   │   - Broker dispatches to appropriate worker based on action domain
   │
6. Worker executes action in sandbox
   │   - Worker receives: action type, arguments, sandbox config, timeout
   │   - Emits RunEvent updates during execution (started, progress, output)
   │   - Captures ToolExecutions for any sub-tool calls
   │   - Stores artifacts (logs, screenshots, files) as RunArtifacts
   │
7. Worker returns result
   │   - Success: RunStep status → completed, output captured
   │   - Failure (transient): RunStep status → failed, check retry policy
   │     - If retries remain: create new RunAttempt, go to step 6
   │     - If no retries: RunStep status → failed (terminal)
   │   - Failure (permanent): RunStep status → failed (terminal)
   │   - Timeout: RunStep status → timed_out
   │
8. Broker finalizes
   │   - Run status → completed (all steps done) or failed (any step failed terminally)
   │   - Final RunEvent: run_completed or run_failed
   │   - Emit realtime update to the session
   │   - Return structured result as tool result to the agent's turn loop
   │   - Agent's turn continues
```

### Error Handling

**Transient failures** (network errors, rate limits, temporary unavailability):
- The RunAttempt is marked failed with the error class.
- If the retry policy allows, a new RunAttempt is created after a backoff delay.
- Backoff is exponential with jitter: base * 2^attempt + random(0, base).
- Max retry count is configurable per capability domain (default: 3 for MCP/external, 1 for internal mutations, 2 for CLI/browser).

**Permanent failures** (policy denial, invalid input, unrecoverable error):
- The RunStep and Run are marked failed immediately.
- No retry. The error is returned to the agent.
- The agent can adapt (try a different approach, file a blocker, ask for help).

**Partial failures** (multi-step runs where some steps succeed):
- Each step has its own status. A run can have completed and failed steps.
- The run's overall status reflects the worst outcome.
- Completed steps are not rolled back — the system does not support transactions across steps. If partial state is a concern, the action should be designed as a single atomic step.

### Timeout Handling

Three timeout levels, from inner to outer:

1. **Worker execution timeout**: per-action-type timeout. CLI commands: configurable, default 5 minutes. Browser actions: 30 seconds per interaction. MCP calls: per-connection configurable, default 30 seconds (see doc 09 `mcp_connection.timeout_ms`). Internal mutations: 10 seconds.

2. **RunStep timeout**: the maximum time a single step can take, including retries. Default: 3x the worker execution timeout.

3. **Run timeout**: the maximum time the entire run can take. Default: 10 minutes. Configurable per capability domain.

When a timeout fires:
- The in-flight operation is allowed to complete its current atomic unit (don't kill a command mid-write).
- If it doesn't complete within a grace period (10 seconds), it is forcibly terminated.
- The step/run is marked `timed_out`.
- A RunEvent records the timeout with timing details.

### Cancellation

Runs can be cancelled by:
- The human (via inbox, via chat, via UI).
- The broker (timeout, budget exceeded, orphan detection).
- The supervisor (stuck detection, system shutdown).

Cancellation is graceful:
1. The run is marked `cancelling`.
2. The worker receives a cancellation signal.
3. The worker completes its current atomic unit and exits.
4. If the worker doesn't exit within the grace period, it is forcibly terminated.
5. The run is marked `cancelled`.
6. All pending steps are marked `cancelled`.
7. A RunEvent records the cancellation with the actor and reason.

### Paused Runs

A run enters `paused` when it requires external input (e.g., browser handoff to a human — see doc 11 Human Handoff). Paused runs are exempt from stuck detection — the supervisor does not treat them as stuck. Paused runs have a configurable timeout (default: 24 hours) after which they transition to `timed_out`.

## Inbox Integration

The inbox is the human's review surface for items that require human attention. Inbox items come from two sources: the flow engine (when a `review` flow node with `human` actor type activates) and conversational capability requests from agents. Policy does not push items to the inbox — policy is strictly binary (allow/deny).

Human review of agent actions (e.g., reviewing a draft email before sending) is handled by `review` flow nodes with `human` actor type in the task graph (see doc 03). When a flow reaches a `review` node (with `human` actor type), the flow engine creates an inbox item for the human. This is flow-driven, not policy-driven.

### Conversational Capability Requests (capability_approval)

Agents can conversationally request access to capabilities they don't have. For example, Frank might ask the human: "This task needs CLI access for the worker agent. Should I grant it?" If the human agrees, Frank (or the PM) creates a `capability_approval` inbox item for the admin to formalize.

This is agent-initiated, not policy-triggered. The agent noticed it lacked a capability (because the policy denied it), adapted conversationally, and routed the request to the human through normal communication channels. The inbox item is created by the agent's tool call (e.g., creating an inbox item), not by the policy engine.

## Sandboxing and Isolation

### CLI Sandboxing

CLI execution uses process-level isolation. Each CLI command runs as a restricted OS process with:

- Working directory scoped to the project repo (task branch).
- Filtered environment variables (no host secrets leak). Secrets referenced in the action are resolved at execution time (see Security section) and injected as environment variables. The agent does not see other agents' secrets or other projects' environment.
- Resource limits via ulimit (CPU time, memory, file descriptors).
- Command denylist enforced before execution.
- Network policy enforced at the process level. By default, outbound network access is allowed (agents need to install packages, call APIs, etc.). Org and project policies can restrict network access: allowlist specific domains, denylist specific domains, or deny all outbound. Inbound connections are never allowed.
- Wall clock timeout per command (configurable, default: 5 minutes).
- stdout and stderr are captured independently. Inline limit: 50KB per stream (content included in the tool result). Total capture limit: 10MB per command (excess stored as a RunArtifact). See doc 11 for details.
- Disk write limit (default: 500 MB) to prevent the agent from filling the workspace.

Container-based isolation is a future enhancement for managed/multi-tenant deployments. See doc 11 for the full CLI sandbox model.

### Browser Isolation

Browser execution uses isolated browser contexts:

- Browser sessions are task-scoped — the first run in a task creates the browser session, and subsequent runs within the same task can resume it. Sessions are cleaned up when the task completes. See doc 11 for the full browser session lifecycle.
- Browser contexts are not shared between tasks or between agents.
- Domain restrictions are enforced at the browser level: the navigation policy (allowlist/denylist) is configured per org/project and enforced before any page load.
- All browser actions (navigations, clicks, typed text) are recorded as RunEvents for replay.
- Screenshots are captured at configurable points (every navigation, every interaction, on error, on completion) and stored as RunArtifacts.
- Browser sessions have a maximum duration (default: 10 minutes per run) to prevent abandoned sessions from consuming resources.
- File downloads are captured as RunArtifacts. File uploads are restricted to files in the project workspace.

### MCP Isolation

MCP calls run through the broker with additional validation:

- **Per-connection capability checks**: the agent must have `mcp.connection.use:<connection_id>` to use a connection at all.
- **Per-tool capability checks**: optionally, individual tools can be gated with `mcp.tool.invoke:<connection_id>:<tool_name>`.
- **Request schema validation**: the broker validates the tool call arguments against the tool's declared input schema before dispatching. Malformed requests are rejected.
- **Response schema validation**: the worker validates the response against the tool's declared output schema. Unexpected responses are logged and flagged.
- **Timeout per tool call**: configurable per connection (`mcp_connection.timeout_ms`), default 30 seconds (see doc 09).
- **Secret handling**: MCP connections may require authentication. Secrets are resolved at execution time and injected into the request. They are never exposed to the agent or persisted in the run payload.

### Internal Mutation Isolation

OtterCamp internal mutations (create task, update status, advance flow) are simpler:

- No container or browser involved — they are direct database operations.
- Isolation is via capability checks and org/project scoping.
- The broker verifies the principal has the capability and the target resource is within the principal's scope.
- All mutations are transactional — they succeed or fail atomically.
- Timing is captured but execution is fast (milliseconds).

## Reliability and Recovery

### Durable State Transitions

All run state transitions are durable — written to the database before the broker proceeds. If the broker crashes after recording a state transition but before dispatching the worker, recovery sees the correct state and can resume.

State transitions are guarded by version checks (optimistic concurrency). Two concurrent attempts to update the same run will not corrupt state — one will succeed and one will retry.

### Explicit Retries

Retries are explicit attempts, never silent overwrites. Every retry creates a new RunAttempt record with:
- The attempt number (1, 2, 3, ...)
- The trigger (initial, transient_failure, manual_retry)
- The previous attempt's failure reason
- Timing for the new attempt

### Idempotency

Mutating operations require idempotency keys. The broker deduplicates requests by idempotency key within a window (default: 24 hours). If the same key is seen twice:
- If the first request completed, return the same result.
- If the first request is still in progress, return a "duplicate, in progress" response.
- If the first request failed, allow the retry (new RunAttempt).
- Fresh project kickoff follows the same idempotency rule: a retry of the same fresh-start request must bind back to the canonical live project/session path created by the first successful `project.create`, not mint a second active project or parallel planning session.
- Fresh kickoff and resume are distinct execution modes. Fresh kickoff suppresses archived/closed prior-run transcript context unless the operator explicitly chose resume/recovery; resume/recovery keeps the historical context because continuity is the point.

### Dead Letter Handling

Runs that fail repeatedly (exceed max retry count) are moved to a dead letter state rather than silently dropped:
- Run status → `dead_letter`.
- A project_task_event is created: "Run failed after {n} attempts: {error}".
- The PM is notified via the supervision pipeline.
- The human can see dead letter runs in the operational dashboard and manually retry, cancel, or investigate.

### Failure State Repair and Task Scheduling

The system actively prevents tasks from getting stuck. A background **supervisor process** monitors for failure states and ensures work keeps moving.

**Stuck task detection:**
- Periodically scan for tasks in `in_progress` or `blocked` whose associated runs have failed, timed out, or gone silent.
- "Silent" means: the agent's last heartbeat is older than the configured threshold (default: 90 seconds (3 missed heartbeats at 30-second intervals) for sync, 5 minutes for async).
- For stuck tasks: emit a supervisor event, attempt to diagnose (check run status, check model provider health, check worker health), and take recovery action.
- Recovery actions, in order: (1) start a new run at the same flow node if the failure was transient, (2) file a blocker task if the failure was permanent, (3) escalate to the PM if diagnosis is inconclusive.
- Maximum auto-recovery attempts per task per flow node: 3. After that, escalate.

**Queue drain:**
- When a concurrency slot frees up (global or per-provider), immediately check for eligible queued tasks and dispatch the next one.
- Eligible = `queued` + all dependencies resolved + not at concurrency limit for its required model provider.
- Priority ordering: sync sessions first, then by task priority (urgent > high > normal > low), then FIFO within the same priority.
- No task should sit waiting when capacity is available. The queue is checked on every slot release, not on a timer.

**Orphaned run recovery:**
- Detect runs that started (`in_progress`) but never completed: no RunEvents for longer than the run timeout + a grace period (default: 2x the run timeout).
- Mark orphaned runs as failed with reason `orphaned`.
- Make the associated task eligible for retry or reassignment.
- Create a project_task_event documenting the orphan.

**Blocker staleness:**
- Flag blocking tasks that have been open beyond a configurable threshold (default: 24 hours for normal priority, 4 hours for urgent).
- Escalate stale blockers: first to the PM (via a supervision check-in in the project session), then to the human if the PM cannot resolve.
- Create a RunEvent on the blocked task: "blocker stale, escalated."

**Retry policy:**
- Configurable per capability domain and per flow node.
- Default: auto-retry on transient failures (provider errors, rate limits, timeouts) with exponential backoff.
- No auto-retry on permanent failures (policy denial, invalid input, repeated crashes with the same error).
- After max retries, the supervisor takes over (stuck task detection path).
- Custom retry policies can be set at the project or flow template level: "this deployment step should not auto-retry" or "this data fetch step can retry up to 10 times."
- For fresh kickoff planning turns, exceeding the bounded prompt/continuation guardrail surfaces one blocker in the session and stops recursive churn. The system does not keep spinning new turns indefinitely.

**Health heartbeat:**
- Running agents emit periodic heartbeats (default interval: 30 seconds) as RunEvents.
- Heartbeats include: agent ID, run ID, current step, progress indicator (tool call count, tokens used), and a "still working" flag.
- If heartbeats stop, the supervisor assumes failure after the heartbeat timeout (default: 3 missed heartbeats = 90 seconds).
- The supervisor then follows the stuck task detection path.
- Heartbeats are ephemeral — they are RunEvents tagged as `heartbeat` and can be purged after the run completes.

## Token Accounting

### Model Invocation Tracking

Model invocations are tracked in the `model_invocation` table defined in doc 07. That table carries `run_id`, `run_step_id`, and `run_attempt_id` FK columns so that model calls made during control plane execution can be attributed back to specific runs. The control plane reads from this table for token aggregation and budget enforcement — it does not own the table.

Each model invocation captures provider, model ID, input/output token counts, latency, and execution context. See doc 07 for the canonical schema.

### Token Aggregation

Token counts roll up through the entity hierarchy:

- **Per model invocation**: the raw token count of one model call (doc 07).
- **Per RunAttempt**: sum of all model invocations in the attempt.
- **Per RunStep**: sum of all attempts (retries accumulate -- every attempt consumed real tokens and was billed by providers).
- **Per Run**: sum of all steps.
- **Per flow node execution**: sum of all runs at that node (across the task).
- **Per task**: sum of all runs across all flow nodes.
- **Per project**: sum of all tasks.
- **Per agent**: sum of all runs by that agent (across projects).
- **Per org**: sum of all projects.

These aggregations are materialized periodically (not real-time) for dashboard queries. Real-time token counts are available per-run and per-task.

### Budget Enforcement

Budgets are configured at the org and project level (doc 13) and expressed in tokens. The control plane enforces them:

- **Soft budget**: when an agent's cumulative token usage reaches the soft limit, the system notifies the admin (via inbox or notification). Work continues — the soft limit is a warning, not a gate.
- **Hard budget**: when the hard limit is reached, all non-essential capabilities are denied. Essential capabilities (filing blockers, updating status) remain allowed to prevent tasks from getting stuck with no way to signal the problem.
- Budget checks happen at the broker level during policy evaluation. They use the latest available aggregation (may be slightly stale — a few seconds).

### Usage Dashboard Integration

The control plane provides the raw data for usage dashboards (doc 13):

- Token usage by org, project, agent, model, time period.
- Token usage by capability domain (how much are we spending on CLI vs browser vs MCP vs chat).
- Usage anomaly detection: flag agents or tasks whose token consumption deviates significantly from historical averages.
- Tokens per task completion: average token usage to complete a task, broken down by flow template type.

## Observability Requirements

### Per Run and Per Step Capture

- Status and timestamps (created, started, completed, duration)
- Principal identity and capability
- Policy decision and reason (which layer, which rule)
- Execution latency (wall clock, CPU time where applicable)
- Token counts (input, output)
- Failure class (transient vs permanent) and error details
- Artifact count and total size
- Heartbeat history

### Per Model Invocation Capture

Tracked in doc 07's `model_invocation` table with control plane FKs. Key fields:

- Provider, model ID, model profile
- Input/output token counts
- Latency (time to first token, total)
- Whether this was a retry or fallback
- The prompt hash (for dedup in eval pipelines, not the full prompt)

### Operational Dashboards

Dashboards must support:

- **Active runs**: currently executing, with agent, capability, duration, and progress.
- **Failure rate**: by capability domain, by agent, by time period. Distinguish transient vs permanent.
- **Token usage**: by org, project, agent, model, time period. Budget utilization percentages.
- **Concurrency**: current slot utilization (global and per-provider). Queue depth.
- **Latency**: p50, p95, p99 execution latency by capability domain.
- **Supervisor activity**: stuck detections, orphan recoveries, escalations.

### Alerting

Configurable alerts for:

- Run failure rate exceeds threshold (default: >10% in a 5-minute window).
- Agent heartbeat loss.
- Budget soft/hard limit reached.
- Orphaned run detected.
- Supervisor escalation (stuck task that couldn't be auto-recovered).

## Security Requirements

- **Template-driven capability posture.** Agents receive capabilities from templates that default to generous grants. Instance safety (layer 1) is the only hardcoded deny layer. All other restrictions are opt-in via org, project, or agent-level policy rules.
- **No privileged execution path outside broker/worker.** There is no way to execute a tier 2 action that bypasses the broker. The broker is the only entry point.
- **Secret references resolved at execution time.** When an action needs a secret (API key, token, credential), the action payload contains a reference (secret ID), not the secret value. The worker resolves the reference at execution time from the secret store, injects it into the execution environment, and the secret is never persisted in run payloads, RunEvents, or audit logs.
- **Tamper-evident audit trail.** Run and RunEvent records are append-only. Status transitions include the previous status for verification. The audit log supports hash chaining for tamper detection.
- **Org isolation.** The broker verifies org_id on every request. A principal in Org A cannot create a run that accesses Org B's resources, regardless of capability grants. This is enforced at the database level (database-per-org isolation, see doc 04) in addition to the application level.
- **Session binding.** Runs are bound to the session they originated from. A run cannot be transferred to a different session.
- **Rate limiting.** The broker enforces rate limits on action requests per agent per time window to prevent abuse or runaway loops, independent of model-level concurrency limits.

## Database Schema

### run

```sql
create table run (
  id                  uuid primary key default gen_random_uuid(),
  organization_id     uuid not null references organization(id),
  project_id          uuid references project(id),
  task_id             uuid references project_task(id),
  flow_node_id        uuid references flow_node(id),
  session_id          uuid references chat_session(id),
  turn_id             uuid references chat_turn(id),

  -- Principal
  principal_type      text not null check (principal_type in ('agent', 'human', 'system')),
  principal_id        uuid not null,
  delegated_by        uuid,                     -- human who delegated (if applicable)

  -- Capability
  capability          text not null,             -- e.g., 'system.cli.execute'
  action_type         text not null,             -- e.g., 'cli_execute', 'task_create', 'mcp_invoke'
  action_payload      jsonb not null,            -- the action arguments (secrets redacted)
  idempotency_key     text,                      -- for deduplication

  -- Policy
  policy_decision     text not null check (policy_decision in ('allow', 'deny')),
  policy_decided_by   text,                      -- which layer made the decision
  policy_rule_id      uuid,                      -- the specific rule that decided

  -- Status
  status              text not null default 'created'
    check (status in ('created', 'in_progress', 'paused', 'completed', 'failed', 'timed_out', 'cancelled', 'cancelling', 'dead_letter')),
  failure_reason      text,                      -- null unless failed
  failure_class       text check (failure_class in ('transient', 'permanent', 'timeout', 'policy', 'cancelled', 'orphaned')),

  -- Token counts (rollups from model_invocation in doc 07)
  total_input_tokens  int not null default 0,
  total_output_tokens int not null default 0,

  -- Timing
  created_at          timestamptz not null default now(),
  started_at          timestamptz,
  completed_at        timestamptz,
  duration_ms         int,

  -- Versioning
  version             int not null default 1,    -- optimistic concurrency
  metadata            jsonb not null default '{}'
);

create index on run (organization_id, status);
create index on run (project_id, status) where project_id is not null;
create index on run (task_id) where task_id is not null;
create index on run (session_id) where session_id is not null;
create index on run (principal_type, principal_id, created_at);
create index on run (status, created_at) where status = 'in_progress';
create index on run (idempotency_key) where idempotency_key is not null;
```

### run_step

```sql
create table run_step (
  id              uuid primary key default gen_random_uuid(),
  run_id          uuid not null references run(id) on delete cascade,
  step_number     int not null,
  name            text not null,                 -- human-readable step name
  action_type     text not null,                 -- same domain as run, or sub-action
  action_payload  jsonb,                         -- step-specific arguments (if different from run)

  -- Status
  status          text not null default 'pending'
    check (status in ('pending', 'in_progress', 'completed', 'failed', 'timed_out', 'cancelled', 'skipped')),
  failure_reason  text,
  failure_class   text check (failure_class in ('transient', 'permanent', 'timeout', 'policy', 'cancelled', 'orphaned')),

  -- Output
  output          jsonb,                         -- structured output from execution
  output_summary  text,                          -- human-readable summary of what happened

  -- Token counts (aggregated from attempts)
  input_tokens    int not null default 0,
  output_tokens   int not null default 0,

  -- Timing
  created_at      timestamptz not null default now(),
  started_at      timestamptz,
  completed_at    timestamptz,
  duration_ms     int,

  metadata        jsonb not null default '{}',

  unique (run_id, step_number)
);

create index on run_step (run_id);
create index on run_step (status) where status = 'in_progress';
```

### run_attempt

```sql
create table run_attempt (
  id               uuid primary key default gen_random_uuid(),
  run_step_id      uuid not null references run_step(id) on delete cascade,
  attempt_number   int not null,
  trigger          text not null check (trigger in ('initial', 'transient_failure', 'manual_retry')),

  -- Status
  status           text not null default 'in_progress'
    check (status in ('in_progress', 'completed', 'failed', 'timed_out', 'cancelled')),
  failure_reason   text,
  failure_class    text check (failure_class in ('transient', 'permanent', 'timeout', 'cancelled')),

  -- Output
  output           jsonb,
  output_summary   text,

  -- Worker
  worker_type      text check (worker_type in ('internal', 'cli', 'browser', 'mcp')),
  worker_id        text,                        -- identifier of the worker instance

  -- Token counts
  input_tokens     int not null default 0,
  output_tokens    int not null default 0,

  -- Timing
  started_at       timestamptz not null default now(),
  completed_at     timestamptz,
  duration_ms      int,

  metadata         jsonb not null default '{}',

  unique (run_step_id, attempt_number)
);

create index on run_attempt (run_step_id);
create index on run_attempt (status) where status = 'in_progress';
```

### model_invocation (cross-reference to doc 07)

Model invocations are tracked in the `model_invocation` table defined in doc 07. That table should include `run_id`, `run_step_id`, and `run_attempt_id` FK columns so that model calls made during control plane execution can be attributed back to specific runs. The control plane does not own this table — it reads from it for token aggregation and observability.

### tool_execution

```sql
create table tool_execution (
  id                uuid primary key default gen_random_uuid(),
  run_id            uuid not null references run(id) on delete cascade,
  run_step_id       uuid references run_step(id) on delete cascade,
  run_attempt_id    uuid references run_attempt(id) on delete cascade,

  -- Tool
  tool_name         text not null,               -- e.g., 'create_task', 'cli_execute', 'mcp:github:create_pr'
  tool_tier         text not null check (tool_tier = 'tier2'),  -- only tier 2 calls go through the control plane
  tool_domain       text not null check (tool_domain in ('project', 'chat', 'memory', 'system', 'mcp', 'comms', 'agent')),

  -- Capability
  capability        text not null,               -- the capability that was evaluated
  policy_decision   text not null check (policy_decision in ('allow', 'deny')),

  -- Execution
  input             jsonb not null,              -- tool call arguments (secrets redacted)
  output            jsonb,                       -- tool result
  status            text not null default 'pending'
    check (status in ('pending', 'in_progress', 'completed', 'failed', 'denied', 'cancelled')),
  error_message     text,

  -- Timing
  started_at        timestamptz,
  completed_at      timestamptz,
  duration_ms       int,

  created_at        timestamptz not null default now(),
  metadata          jsonb not null default '{}'
);

create index on tool_execution (run_id);
create index on tool_execution (run_step_id) where run_step_id is not null;
create index on tool_execution (tool_name, created_at);
create index on tool_execution (capability, policy_decision, created_at);
```

### run_artifact

```sql
create table run_artifact (
  id              uuid primary key default gen_random_uuid(),
  run_id          uuid not null references run(id) on delete cascade,
  run_step_id     uuid references run_step(id) on delete cascade,
  run_attempt_id  uuid references run_attempt(id) on delete cascade,

  -- Artifact
  name            text not null,                 -- human-readable name
  artifact_type   text not null check (artifact_type in ('log', 'screenshot', 'file', 'trace', 'diff', 'cli_output', 'download', 'extracted_data', 'page_snapshot', 'build_output', 'test_report')),
  content_type    text not null,                 -- MIME type
  size_bytes      bigint not null,
  storage_path    text not null,                 -- object storage path

  -- Context
  description     text,                          -- what this artifact represents
  is_primary      boolean not null default false, -- is this the main output artifact?

  created_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}'
);

create index on run_artifact (run_id);
create index on run_artifact (run_step_id) where run_step_id is not null;
```

### run_event

```sql
create table run_event (
  id              uuid primary key default gen_random_uuid(),
  run_id          uuid not null references run(id) on delete cascade,
  run_step_id     uuid references run_step(id) on delete cascade,
  run_attempt_id  uuid references run_attempt(id) on delete cascade,
  sequence        int not null,                  -- monotonic within the run

  -- Event
  event_type      text not null check (event_type in (
    'created', 'started', 'completed', 'failed', 'timed_out', 'cancelled',
    'policy_evaluated', 'policy_denied',
    'dispatched', 'worker_assigned', 'progress', 'output_chunk',
    'heartbeat',
    'stuck_detected', 'orphan_detected', 'auto_retry', 'escalated',
    'wakeup_coalesced', 'wakeup_deferred', 'wakeup_promoted'
  )),
  event_data      jsonb not null default '{}',   -- event-specific payload
  actor_type      text check (actor_type in ('agent', 'human', 'system', 'supervisor')),
  actor_id        uuid,

  created_at      timestamptz not null default now(),

  unique (run_id, sequence)
);

create index on run_event (run_id, sequence);
create index on run_event (event_type, created_at);
create index on run_event (run_id, event_type) where event_type = 'heartbeat';
```

### runtime_state

```sql
create table runtime_state (
  id                    uuid primary key default gen_random_uuid(),
  organization_id       uuid not null references organization(id) on delete cascade,
  scope_type            text not null check (scope_type in ('task', 'session')),
  scope_id              uuid not null,
  active_run_id         uuid references run(id) on delete set null,
  active_principal_type text,
  active_principal_id   uuid,
  lock_acquired_at      timestamptz,
  last_wakeup_at        timestamptz,
  metadata              jsonb not null default '{}',
  created_at            timestamptz not null default now(),
  updated_at            timestamptz not null default now(),

  unique (scope_type, scope_id),
  check (
    (active_principal_type is null and active_principal_id is null)
    or (active_principal_type = 'system' and active_principal_id is null)
    or (active_principal_type in ('human_user', 'agent') and active_principal_id is not null)
  )
);

create index on runtime_state (organization_id, scope_type, scope_id);
```

`runtime_state` is not the historical record; it is the mutable coordination row that tells the broker who currently owns execution for a task/session boundary and how to resume it safely when there is no active owner. The `metadata` contract carries the bound task/session/flow execution ids, provider/runtime session identifiers when relevant, last progress timestamp/event, deferred wakeup details, failure disposition (`resumable` vs `terminal`), and retirement reason/time. Historical wakeup decisions live in `run_event` and deferred work lives in ordinary `run` rows.

### capability_policy

```sql
create table capability_policy (
  id                uuid primary key default gen_random_uuid(),
  organization_id   uuid not null references organization(id),

  -- Scope
  policy_layer      text not null check (policy_layer in ('instance', 'org', 'project', 'agent_profile')),
  project_id        uuid references project(id),       -- set for project-layer policies
  agent_id          uuid references agent(id),           -- set for agent_profile-layer policies

  -- Rule
  capability_pattern text not null,              -- e.g., 'system.cli.execute', 'project.task.*', 'comms.*'
  decision          text not null check (decision in ('allow', 'deny')),
  priority          int not null default 0,      -- higher priority wins within the same layer
  conditions        jsonb,                       -- optional conditions (budget threshold, time window, etc.)

  -- Metadata
  description       text,                        -- why this policy exists
  created_by_type   text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id     uuid not null,
  is_active         boolean not null default true,
  created_at        timestamptz not null default now(),
  updated_at        timestamptz not null default now(),
  metadata          jsonb not null default '{}'
);

create index on capability_policy (organization_id, policy_layer, is_active);
create index on capability_policy (project_id, is_active) where project_id is not null;
create index on capability_policy (agent_id, is_active) where agent_id is not null;
create index on capability_policy (capability_pattern);
```

### Schema Design Notes

- **8 tables** for the control plane domain. Model invocations are tracked in doc 07's `model_invocation` table with FK columns back to `run`, `run_step`, and `run_attempt`.
- `run` is the anchor entity. Everything else links back to it.
- `tool_execution` records tier 2 tool calls (control plane path). Tier 1 tool calls (chat-layer reads) are recorded only as chat messages. A complete activity view requires joining both.
- `capability_policy` stores declarative rules with binary outcomes (`allow`, `deny`). The Policy API evaluates them. There are no runtime overrides or session-scoped grants.
- `runtime_state` is the mutable single-owner coordinator for task/session execution boundaries. It stays authoritative even when no owner is active by preserving the resume contract until the task/project lifecycle retires it.
- `run_event` is append-only and serves as both the execution timeline (for replay) and the health monitoring data source (heartbeats). Heartbeat events are purged after run completion to keep the table manageable. The `actor_type` check includes `supervisor` as a doc 16-specific extension of the principal convention, and wakeup lifecycle events (`wakeup_coalesced`, `wakeup_deferred`, `wakeup_promoted`) make merge/defer decisions auditable.
- Token count fields on `run`, `run_step`, and `run_attempt` are denormalized rollups from `model_invocation` (doc 07). They are updated asynchronously (not in the hot path) and may be slightly stale.

## API Contract Surface

### Run Management

- `POST /control/runs` — request an action run. Body includes capability, action payload, principal context. Returns the run record with policy decision.
- `GET /control/runs/{id}` — run detail including all steps, latest attempt, and policy info.
- `GET /control/runs/{id}/steps` — all steps for a run.
- `GET /control/runs/{id}/events` — event stream (supports both polling and realtime via SSE).
- `GET /control/runs/{id}/artifacts` — all artifacts produced by the run.
- `POST /control/runs/{id}/cancel` — cancel an in-progress run.
- `POST /control/runs/{id}/retry` — manually retry a failed run (creates new attempt).

### Policy Management

- `GET /control/policies` — list policies (filtered by layer, project, agent).
- `POST /control/policies` — create a policy rule.
- `PUT /control/policies/{id}` — update a policy rule.
- `DELETE /control/policies/{id}` — deactivate a policy rule (soft delete via `is_active`).
- `POST /control/policies/evaluate` — dry-run policy simulation. "What would happen if agent X tried capability Y in project Z?" Does not create a run.

### Observability

- `GET /control/runs` — list runs with filters (org, project, task, agent, status, capability, time range). Paginated.
- `GET /control/cost/summary` — cost aggregation by org, project, agent, model, time period.
- `GET /control/health` — supervisor status, queue depth, active run count, failure rate.

## Integration with Other V2 Specs

### Chat (Doc 02)

The control plane executes all tier 2 tool calls from the chat turn loop. The turn loop calls the broker, gets an immediate response (allow → result, deny → error), and continues. Policy is binary — allow or deny. Runs link back to the session and turn that triggered them.

Chat messages (tool_call/tool_result) and control plane records (Run/ToolExecution) are complementary views of the same action — chat messages are the conversational context, control plane records are the execution detail.

### Projects and Tasks (Doc 03)

Flow transitions (`project.flow.advance`) and blocker filing (`project.flow.blocker.raise`) are control plane actions subject to policy evaluation. The task scheduler queries for available concurrency slots and kicks off runs when tasks are ready.

Inbox integration: human review of agent actions is handled by `review` flow nodes in the task graph, using `requires_human_review = true` when a human sign-off gate is required, not by the control plane. `capability_approval` inbox items are created conversationally by agents when they need human authorization.

Proactive supervision: the PM receives signals from the supervisor (stuck tasks, orphaned runs) and applies judgment in the project session.

### Models and Inference (Doc 07)

Model invocations during runs are tracked in doc 07's `model_invocation` table with token counts and latency. The model gateway (doc 07) handles routing, fallback, and concurrency limits. The control plane reads from `model_invocation` for token aggregation and feeds usage data into budget enforcement.

Concurrency limits from doc 07 (global max concurrent LLM sessions, per-provider limits) are respected by the task scheduler — it does not dispatch runs when the model gateway is at capacity.

### MCP (Doc 09)

All MCP tool calls run through the broker. Per-connection and per-tool capability checks gate access. The broker validates request schemas before dispatching. MCP execution is handled by the MCP worker, which manages connection health, timeouts, and retries.

### System Integration (Doc 11)

CLI and browser actions require explicit capabilities (`system.cli.execute`, `system.browser.*`). The control plane provisions sandboxed environments and captures execution artifacts. Time limits and resource limits are enforced by the worker runtime.

### Security and Observability (Doc 13)

The control plane provides the raw execution data that powers observability dashboards and token budget controls. Token aggregations from `model_invocation` (doc 07) and `run` feed into the budget system. The audit trail (`run_event`, `capability_policy`) feeds into compliance reporting.

### Tools and Tool Policy (Doc 20)

The capability model in this doc defines what permissions are needed. The tool registry (doc 20) defines what tools exist and maps them to capabilities. Tool tier (1 vs 2) determines whether execution goes through the control plane or stays in the chat layer.

## Non-Goals for Initial Release

- **User-authored arbitrary policy scripting language.** Policies are declarative rules (capability pattern → decision). No embedded scripting engine, no custom logic functions. If the declarative model is insufficient, extend the rule schema — do not add a scripting runtime.
- **Full cross-region distributed run scheduler.** V2 is single-region. Runs are dispatched locally. Multi-region scheduling is a scale concern, not a correctness concern.
- **Peer-to-peer agent trust without central policy evaluation.** Every action goes through the broker. Agents do not trust each other directly. There is no "agent A vouches for agent B" mechanism.
- **Cross-org capability grants.** Capabilities are always scoped within a single org. Multi-org collaboration is not in scope for V2.

## Resolved Decisions

- **Policy is binary: allow, deny.** `allow` executes immediately. `deny` rejects immediately. Policy outcomes are strictly binary — there is no intermediate "review" state at the policy level.

- **Human review is a flow concern, not a policy concern.** Review of agent actions (e.g., reviewing a draft email before sending) is modeled as `review` flow nodes in the task graph, with `requires_human_review = true` when a human sign-off gate is required (see doc 03). The control plane does not intercept or stage actions — policy is strictly binary. If you want a human to review an email before it's sent, the flow has: `[email.draft work node]` → `[review node, requires_human_review=true]` → `[email.send work node]` → `[merge/completion]`. The tool always does what it says — `send` sends, `draft` drafts. Nothing is secretly intercepted by policy.

- **Default posture is permissive.** Agents receive generous capabilities from templates. The default experience empowers agents to do real work. Restrictions are opt-in — admins add `deny` rules at the org or project layer when they want guardrails. Instance safety (layer 1) is the only default-deny layer — it catches truly catastrophic actions. Everything else defaults to allowing what templates grant.

- **Soft budget = notify, hard budget = deny.** When the soft token budget is reached, the admin is notified. Work continues. When the hard limit is reached, non-essential capabilities are denied.

- **Capability approval is conversational.** When an agent needs a capability it doesn't have, it adapts conversationally — Frank or the PM asks the human, creates an inbox item. This is agent-initiated, not policy-triggered.

- **Four default capability templates: reader, worker, deployer, admin.** These are generous starting points. `reader` = read-only. `worker` = read + project mutations + file I/O + CLI + browser + memory. `deployer` = worker + external comms. `admin` = all capabilities. The starter trio (Frank, Lori, Ellie) get `admin`. Most task-working agents get `worker`. Agents that need to communicate externally get `deployer`.

- **Most-restrictive-wins policy composition.** Higher-priority layers always override lower ones. `deny` at any layer is absolute. `allow` only when all applicable layers allow (or are silent). This is simple, predictable, and safe.

- **Process-level CLI sandboxing.** CLI commands run as restricted OS processes with working directory scoped to the project repo, filtered environment variables, resource limits via ulimit, command denylist, and network policy enforcement. Container-based isolation is a future enhancement for managed/multi-tenant deployments. See doc 11 for the full CLI sandbox model.

- **Browser sessions are task-scoped.** The first run in a task creates the browser session, and subsequent runs within the same task can resume it. Sessions are cleaned up when the task completes. This prevents data leakage between tasks and between agents. See doc 11 for the full browser session lifecycle.

- **Dead letter handling for repeated failures.** Runs that exceed max retries go to `dead_letter` status rather than being silently dropped. This ensures nothing is lost and the operator can investigate.

- **Heartbeats as ephemeral RunEvents.** Running agents emit heartbeats as RunEvents tagged with type `heartbeat`. The supervisor monitors heartbeats for liveness detection. Heartbeat events are purged after run completion to keep the event table manageable.

- **Token aggregation is materialized, not real-time.** Per-run and per-task token counts are available quickly (seconds). Cross-project and cross-org aggregations are materialized periodically for dashboard queries. This avoids expensive real-time aggregation queries in the hot path.

- **Budget enforcement uses soft and hard limits expressed in tokens.** Soft limit: notify admin. Hard limit: non-essential capabilities denied. Essential capabilities (blocker filing, status updates) always remain available to prevent tasks from getting stuck.

- **Runs are bound to sessions and cannot be transferred.** This ensures the audit trail is clean — every run has exactly one session context. If work needs to move to a different session, start a new run in the new session.

- **Idempotency keys required for all mutating operations.** The broker deduplicates by idempotency key within a 24-hour window. This prevents double-execution from retries, network hiccups, or duplicate dispatches.

- **Model invocations are owned by doc 07.** The `model_invocation` table is defined in doc 07 and carries `run_id`, `run_step_id`, and `run_attempt_id` FK columns for control plane context. A model invocation can belong to a run (control plane action), a chat turn (conversation), or both. The usage dashboard needs a unified view across both.

## Open Questions

_None currently outstanding._
