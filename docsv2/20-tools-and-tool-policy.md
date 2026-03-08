---
## Summary

This spec defines the complete tool system for OtterCamp V2 -- how agents discover, access, and execute tools. Tools are what transform agents from conversation partners into operators that can act on the world. The system answers four questions for every tool call: what tools exist, which does this agent have, what is allowed right now, and how does it execute. All tools fall into exactly one of four categories: Native (shipped with OtterCamp, operating on domain entities like tasks/projects/memory), System (filesystem and CLI access within sandboxed workspaces), Browser (web interaction in isolated contexts), and External (remote APIs and MCP servers — any service outside OtterCamp's control).

The core architectural pattern is a two-tier execution model. Tier 1 tools are read-only operations (listing tasks, querying memory, reading files) that execute directly in the chat layer with a simple scope check -- fast and lightweight since reads vastly outnumber writes in a typical agent turn. Tier 2 tools are mutations and external calls that route through the full control plane pipeline: policy evaluation (allow/deny), broker execution, sandboxing, and RunStep audit trail. Tier assignment is static and defaults to tier 2; tier 1 is a strict whitelist. All external and browser tools are always tier 2 with no exceptions.

Tool availability is determined by a four-stage resolution pipeline that runs once per session: (1) Universe -- all native plus enabled external tools, (2) Agent Profile Filter -- allow/deny lists with glob patterns from the agent's profile (e.g., Frank gets project/task/session tools but no system or browser tools), (3) Flow Node Filter -- a soft deprioritization for prompt budget, not a hard access gate, (4) Capability Gate -- tier 2 tools require matching control plane capabilities. The resolved tool set is cached per session for stability. The native catalog ships approximately 55 tools across 7 domains (project, chat, memory, agent, system, browser, communication). Key schema tables are `tool_definition` (native tool metadata, populated at startup) and `session_tool_set` (per-agent, per-session cache of resolved tools for debugging). Communication tools (email, Slack) create drafts as their designed behavior -- the tool succeeds and stages the action in the human's inbox for review. This is tool behavior, not a policy interception.

---

# 20. Tools and Tool Policy

## Purpose

Define how agents discover, access, and execute tools. This spec unifies the currently fragmented tool references across agent profiles (doc 05), the control plane (doc 16), MCP (doc 09), system integration (doc 11), and memory (doc 06) into one authoritative model.

Tools are how agents act on the world. Without tools, an agent is a conversation partner. With tools, an agent is an operator. The tool system must answer four questions for every tool call: what tools exist, which does this agent have, what is allowed right now, and how does it execute.

## Tool Taxonomy

Every tool in OtterCamp falls into exactly one of four categories. The category determines where the tool comes from, how it is registered, and what execution path it follows.

### Category 1: OtterCamp Native Tools

Tools that ship with OtterCamp. They operate on OtterCamp's own domain entities — tasks, projects, flows, memory, sessions, agents. These are the tools agents use to do their job within the platform.

Native tools are defined in code, versioned with the application, and always available. They do not require any external connections or configuration.

### Category 2: System Tools

Tools that give agents controlled access to the local execution environment — the filesystem, CLI, and codebase. These operate within sandboxed workspaces scoped to the project's git repo.

System tools are always available but heavily policy-gated. The capabilities `system.cli.execute` and `system.file.write` (doc 16) gate access to CLI and file write operations.

Within a single task execution there is one workspace contract, not separate per-tool filesystems. `file.read`, `file.list`, `file.search`, `file.write`, `git.*`, and `cli.execute` all resolve the same project workspace root on disk. Task scope determines which project/task binding is allowed to use that root; it does not create a second task-only directory tree. A file or manifest created through one system-tool surface must be immediately visible to the others without path translation or drift.

`cli.execute` hard denies are invocation-aware. Dangerous command tokens and dangerous pipelines such as `sudo`, `su`, `eval`, `exec`, or `curl ... | bash` are matched on the actual command path outside quoted payload text. Benign arguments or encoded payloads that merely contain substrings like `su` are not denied just because those bytes appear inside otherwise-safe command content.

For long-running content migration/import work, system tools are the primary continuity mechanism: fetched pages, manifests, helper scripts, and migrated outputs are expected to live in workspace files. Continuations should resume from those persisted files and checkpoint manifests, not by replaying large raw blobs through the chat transcript. When persisted scripts/artifacts exist but migrated outputs do not, the next turn should use that checkpointed state to write the first real output file before re-listing workspace state or generating more helper scripts.

### Category 3: Browser Tools

Tools for web interaction — navigating pages, clicking elements, extracting content, capturing screenshots. Browser tools run in isolated browser contexts per run/session (doc 11).

Separated from system tools because browser execution has distinct isolation requirements (domain allowlists/denylists, per-run contexts, artifact capture) and a different risk profile.

### Category 4: External Tools (MCP and Remote APIs)

Tools that interact with services outside OtterCamp's control. The primary integration protocol is MCP (Model Context Protocol, doc 09), but this category covers any remote API interaction — MCP servers, REST APIs, GraphQL endpoints, or any external service an agent needs to call. The common thread is that the tool's implementation lives outside OtterCamp and its behavior is outside OtterCamp's control.

External tools are the only category that is not statically known. They appear and disappear as connections are added, removed, or updated. Once discovered, they participate in the same policy pipeline as native tools. All external tools are tier 2 with no exceptions — external calls cannot be verified as side-effect-free.

## Native Tool Catalog

The complete set of tools that ship with OtterCamp before any external connections are configured. Grouped by domain. Each tool is tagged with its execution tier (see Two-Tier Execution below).

### Project and Task Domain

| Tool | Description | Tier |
|---|---|---|
| `project.list` | List projects the agent has access to | 1 (read) |
| `project.get` | Get project details, context block, repo info | 1 (read) |
| `project.create` | Create a new project with name, description, and initial configuration | 2 (mutation) |
| `project.update` | Update project metadata: description, context block, settings | 2 (mutation) |
| `task.list` | List tasks in a project, filterable by status/priority/assignee | 1 (read) |
| `task.get` | Get task details: description, acceptance criteria, constraints, flow state, dependencies | 1 (read) |
| `task.create` | Create a new task in a project with title, description, flow template, priority | 2 (mutation) |
| `task.update` | Update task fields: description, acceptance criteria, constraints, priority | 2 (mutation) |
| `task.add_dependency` | Add a dependency link between tasks or subtasks | 2 (mutation) |
| `task.remove_dependency` | Remove a dependency link | 2 (mutation) |
| `subtask.list` | List subtasks for a flow node execution | 1 (read) |
| `subtask.get` | Get subtask details | 1 (read) |
| `subtask.create` | Create a subtask within a flow node execution | 2 (mutation) |
| `subtask.update` | Update subtask fields | 2 (mutation) |
| `flow.get_template` | Get flow template structure: nodes, edges, skills | 1 (read) |
| `flow.get_execution` | Get current flow state for a task: active node, visit, status | 1 (read) |
| `flow.advance` | Signal that the current flow node's work is complete, advance to next node | 2 (mutation) |
| `flow.review_decision` | Submit approve or reject decision at a review node | 2 (mutation) |
| `inbox.list` | List pending inbox items for the human | 1 (read) |
| `merge_queue.status` | Get merge queue status for a project | 1 (read) |
| `schedule.list` | List task schedules for a project | 1 (read) |
| `schedule.create` | Create a recurring task schedule with cron expression and overlap policy | 2 (mutation) |
| `schedule.update` | Update schedule: cron expression, overlap policy, enabled/disabled | 2 (mutation) |
| `schedule.delete` | Delete a task schedule | 2 (mutation) |
| `flow.create_template` | Create a new flow template with nodes, edges, and skill declarations | 2 (mutation) |

### Chat and Session Domain

| Tool | Description | Tier |
|---|---|---|
| `session.list` | List sessions the agent has access to, filterable by scope | 1 (read) |
| `session.get` | Get session metadata: scope, mode, participants | 1 (read) |
| `session.history` | Read conversation history from a session (within permitted scope) | 1 (read) |
| `session.create` | Create a new session at a scope | 2 (mutation) |
| `session.invite_agent` | Invite an agent to participate in a session | 2 (mutation) |
| `message.send` | Send a message in a session (agent-to-agent coordination, system messages) | 2 (mutation) |

### Memory Domain

| Tool | Description | Tier |
|---|---|---|
| `memory.query` | Query Ellie's memory system with natural language. Runs the four-stage retrieval pipeline (doc 06). Returns relevant memories with sources and confidence. | 1 (read) |
| `memory.record` | Explicitly record a memory item via Ellie. Bypasses the event-level pre-filter — goes directly to extraction. | 2 (mutation) |

`memory.query` is the tool described in doc 06's "Agent-initiated query" section. It fills the gap between passive injection (automatic every turn) and human-initiated @mention queries. All agents have access to it. Results exclude memories already injected via passive injection in the current turn to avoid duplicate context consumption.

### Agent Management Domain

| Tool | Description | Tier |
|---|---|---|
| `agent.list` | List agents available in the current scope (org, project) | 1 (read) |
| `agent.get` | Get agent profile details: role, skills, tool policy | 1 (read) |
| `agent.create_temp` | Create a temporary agent for specialized work | 2 (mutation) |
| `agent.update` | Update agent profile: tool policy, model policy, memory policy, skills | 2 (mutation) |

### System Domain

| Tool | Description | Tier |
|---|---|---|
| `file.read` | Read file contents from the project workspace | 1 (read) |
| `file.list` | List files in a directory within the project workspace | 1 (read) |
| `file.search` | Search file contents in the project workspace (grep/ripgrep equivalent) | 1 (read) |
| `file.write` | Write or update a file in the project workspace | 2 (mutation) |
| `file.delete` | Delete a file from the project workspace | 2 (mutation) |
| `cli.execute` | Execute a shell command in the project workspace sandbox; relative file output via `>`, `>>`, and heredoc is supported | 2 (mutation) |
| `git.status` | Get git status of the project workspace | 1 (read) |
| `git.diff` | Get diff of working changes or between refs | 1 (read) |
| `git.log` | Get commit history | 1 (read) |
| `git.commit` | Commit staged changes to the task branch | 2 (mutation) |

For migration/import tasks specifically, `file.write` and `cli.execute` should be used incrementally throughout the run: write raw fetch artifacts/manifests immediately, execute transform scripts against those files, and emit migrated content outputs as they are completed. After a continuation rollover, existing scripts/artifacts are inputs to the next output-producing step, not a reason to spend another turn re-checking or regenerating scaffolding. When file tools are temporarily unavailable or malformed, supported `cli.execute` recovery for file production includes shell redirection and heredoc patterns that write into the same shared workspace root.

### Browser Domain

| Tool | Description | Tier |
|---|---|---|
| `browser.navigate` | Navigate to a URL in the browser context | 2 (mutation) |
| `browser.click` | Click an element on the page | 2 (mutation) |
| `browser.type` | Type text into a form field | 2 (mutation) |
| `browser.screenshot` | Capture a screenshot of the current page | 2 (mutation) |
| `browser.extract` | Extract structured content from the current page | 2 (mutation) |
| `browser.evaluate` | Execute JavaScript in the page context | 2 (mutation) |

All browser tools are tier 2. Even `browser.screenshot` — which might seem read-only — runs in an isolated browser context that has external side effects (network requests, cookies, session state). Browser actions are never side-effect-free.

### Communication Domain

| Tool | Description | Tier |
|---|---|---|
| `email.compose` | Compose and stage an email draft for review | 2 (mutation) |
| `slack.post` | Compose and stage a Slack message draft for review | 2 (mutation) |

Communication tools create drafts as their designed behavior — the tool succeeds and stages the action in the human's inbox as a `draft_action_review` item. The human reviews, edits if needed, and approves. This is tool behavior, not policy interception — the policy engine evaluates allow/deny for the communication capability; the tool itself decides to stage a draft rather than send immediately.

### Catalog Size and Scope

The native catalog above contains approximately 55 tools across 7 domains. This is the full set that ships with OtterCamp. It is intentionally bounded:

- Every tool maps to a real operation agents need to perform.
- There are no "convenience" tools that bundle multiple operations.
- There is no tool for creating new tools at runtime — all tools are either native (shipped with OtterCamp) or from external connections. Agents cannot create new tools.

## Two-Tier Execution

Tools are split into two execution tiers based on whether they have side effects. A tool's tier is determined at registration — it is a static property of the tool definition, never a runtime decision.

### Tier 1: Chat-Layer Tools (Read-Only, Internal)

How the agent understands its environment. No side effects. Executed directly in the chat layer with a basic permission check (does this agent have access to this scope's data?). Logged as `tool_call`/`tool_result` chat messages — that IS the audit trail for reads.

**What qualifies for tier 1:**
- Reads on data the agent is already scoped to access
- No state changes, no external calls, no file writes
- Results are deterministic given the current state

**Tier 1 tools (complete list):**
- `project.list`, `project.get`
- `task.list`, `task.get`
- `subtask.list`, `subtask.get`
- `flow.get_template`, `flow.get_execution`
- `inbox.list`, `merge_queue.status`, `schedule.list`
- `session.list`, `session.get`, `session.history`
- `memory.query`
- `agent.list`, `agent.get`
- `file.read`, `file.list`, `file.search`
- `git.status`, `git.diff`, `git.log`

**Execution path:**
```
Agent requests tool call
  -> Tool registry lookup: tier 1
  -> Permission check: does this agent's scope include the requested resource?
  -> Execute directly (in-process, no broker)
  -> Return result
  -> Log as chat_message (tool_call + tool_result)
```

**Permission check for tier 1**: The check is scope-based, not policy-based. An agent in a project-scoped session can read tasks in that project. An agent in a task-scoped session can read files in that task's bound project workspace. The check answers "is this data within the agent's current scope?" — not "is this agent allowed to read?". All agents can read within their scope. If a tier 1 call requests data outside the agent's scope (e.g., reading a task from another project), it returns an error, not a policy denial.

Session scope is the canonical source of truth. If a project-scoped session resumes with stale task binding from some other run, the broker/native executors must re-anchor to the live session project and drop the stale task binding instead of enforcing task-scoped invariants. Same-project task bindings may still be preserved when the execution is genuinely task-bound inside that project.

### Tier 2: Control-Plane Tools (Mutations, External, Side Effects)

These change state or reach outside OtterCamp. Full control plane path (doc 16): policy evaluation (allow/deny), execution via broker, RunStep audit trail, artifact capture.

**What qualifies for tier 2:**
- Writes to OtterCamp state (create/update/delete)
- External system calls (MCP, browser, CLI)
- File modifications
- Communication actions
- Anything with observable side effects

**Tier 2 includes:**
- All mutation tools from the native catalog
- All browser tools
- All communication tools
- All external tools (every remote API call is tier 2, no exceptions)

**Execution path:**
```
Agent requests tool call
  -> Tool registry lookup: tier 2
  -> Route to control plane
  -> Policy evaluation:
     - allow: dispatch to broker -> sandbox -> execute -> return result
     - deny: return "not permitted" as tool result
  -> Log as chat_message (tool_call + tool_result)
  -> Log as RunStep in control plane audit trail
```

### Why Two Tiers

In a typical async turn, an agent might do 30 reads and 3 writes. Running 30 reads through the full broker pipeline (create RunStep, evaluate policy, dispatch, capture, finalize) adds substantial overhead for operations that will always be allowed within scope. The two-tier model keeps reads fast and lightweight while preserving full control plane rigor for actions with consequences.

### Tier Assignment Rules

1. **Default is tier 2.** When in doubt, it is tier 2. The cost of running a read through the control plane is latency overhead. The cost of running a mutation through the chat layer is missing audit trail and policy checks.
2. **Tier 1 is a whitelist.** Only tools explicitly listed as tier 1 bypass the control plane. The list above is exhaustive.
3. **Tools may migrate from tier 1 to tier 2** if requirements change (e.g., adding rate limiting or compliance logging to file reads). Migration never goes the other direction — a tool does not move from tier 2 to tier 1.
4. **External tools are always tier 2.** Remote API calls are never side-effect-free from OtterCamp's perspective, even if the server claims the operation is read-only. We cannot verify that claim.

## Tool Resolution Pipeline

When an agent joins a session, it needs a concrete set of tools. The tool set is determined by a four-stage resolution pipeline that runs once at session start (or the agent's first turn) and is cached for the session's lifetime. The resolved tools are used during prompt assembly (doc 05, layer 7).

### Stage 1: Universe

Start with all registered tools — the full native catalog plus all enabled tools from active external connections in the agent's scope.

For external tools, "active connections in the agent's scope" means: connections registered at the org level, plus connections registered at the project level for the agent's current project. Connection health is checked — tools from unhealthy connections (circuit breaker open, connection down) are excluded.

### Stage 2: Agent Profile Filter

Apply the agent's tool policy from its profile (doc 05). The agent profile carries allow/deny lists:

- **Allow list**: if non-empty, only these tools are available. Everything else is excluded.
- **Deny list**: these tools are explicitly excluded, even if they appear on the allow list.

The allow/deny model works on tool names with glob support:
- `project.*` — all tools in the project domain
- `file.write` — a specific tool
- `github.*` — all tools from the "github" external connection (matches by slug)
- `<slug>.*` — all tools from any specific external connection
- `browser.*` — all browser tools
- `*` — everything (the default allow list for unrestricted agents)

**Starter trio defaults:**
- **Frank** (Chief of Staff): `project.*`, `task.*`, `subtask.*`, `flow.*`, `session.*`, `memory.*`, `agent.*`, `inbox.*`, `schedule.*`. No system tools, no browser, no external tools. Frank operates at the organizational level — he creates projects and tasks and coordinates, he does not execute.
- **Lori** (Agent Relations): `agent.*`, `project.list`, `project.get`, `task.list`, `task.get`, `memory.query`. Lori manages agents (create, update, staff) but does not execute tasks.
- **Ellie** (Memory): `memory.*`, `session.history`, `file.read`, `file.search`. Ellie reads broadly for memory extraction but does not mutate project state.

Worker agents typically get the full catalog — they need system tools, file access, CLI, and potentially browser and external tools to do their jobs. Reviewer agents get read tools plus `flow.review_decision`.

### Stage 3: Flow Node Filter (Contextual)

If the agent is executing within a flow node, the node may declare relevant tool domains. This is an additive relevance signal, not a hard filter:

- Flow nodes already declare required skills (doc 03, doc 10). Skills imply tool relevance — a "Go Coding Standards" skill implies system tools are relevant, not browser tools.
- If a flow node explicitly declares `tool_domains` (e.g., `["system", "project"]`), tools outside those domains are deprioritized in the prompt (they can still be called but their descriptions are dropped first when budget is tight).
- If no `tool_domains` are declared on the flow node, all tools from stage 2 pass through.

This stage is about prompt budget optimization, not access control. An agent can always call any tool that passes stage 2 and stage 4 — but if the prompt is tight, irrelevant tool descriptions are the first thing cut.

### Stage 4: Capability Gate

Cross-reference the remaining tools against the agent's control plane capabilities (doc 16). For tier 2 tools, the agent must hold the corresponding capability:

| Tool Domain | Required Capability |
|---|---|
| `project.create` | `project.create` |
| `project.update` | `project.update` |
| `task.create` | `project.task.create` |
| `task.update`, `task.add_dependency`, `task.remove_dependency` | `project.task.update` |
| `subtask.create`, `subtask.update` | `project.task.update` |
| `flow.advance`, `flow.review_decision` | `project.flow.advance` |
| `flow.create_template` | `project.flow.create_template` |
| `schedule.create`, `schedule.update`, `schedule.delete` | `project.schedule.manage` |
| `file.write`, `file.delete` | `system.file.write` |
| `cli.execute` | `system.cli.execute` |
| `browser.navigate` | `system.browser.navigate` |
| `browser.click`, `browser.type`, `browser.evaluate` | `system.browser.interact` |
| `browser.screenshot` | `system.browser.screenshot` |
| `browser.extract` | `system.browser.extract` |
| `git.commit` | `system.file.write` |
| `session.create` | `chat.session.create` |
| `session.invite_agent` | `chat.participant.add` |
| `message.send` | `chat.message.write` |
| `memory.record` | `memory.write` |
| `agent.update` | `agent.update` |
| `agent.create_temp` | `agent.temp.create` |
| External tools (MCP, etc.) | `mcp.tool.invoke:<connection_id>:<tool_name>` |
| `email.compose` | `comms.email.draft` |
| `slack.post` | `comms.slack.post` |

Tools that require a capability the agent does not hold are excluded from the tool set. They do not appear in the prompt and cannot be called.

Tier 1 tools bypass the capability gate — they are governed by scope checks at execution time, not by pre-assigned capabilities.

### Pipeline Summary

```
All registered tools (native + external)
  |
  v
Agent profile allow/deny filter
  |
  v
Flow node domain filter (deprioritize, not exclude)
  |
  v
Capability gate (exclude tier 2 tools without matching capability)
  |
  v
Final tool set for this turn
```

The final tool set is used for two things:
1. **Prompt generation**: tool descriptions are rendered into the prompt (layer 7 of the 7-layer pipeline).
2. **Runtime validation**: when the agent calls a tool, the system verifies it is in the resolved set before executing.

## Tool Descriptions in Prompt

Tool descriptions are layer 7 in the 7-layer prompt assembly pipeline (doc 05) — the lowest priority layer. They are generated from the resolved tool set and included in the agent's prompt.

### Description Format

Each tool description includes:
- **Name**: the tool identifier (e.g., `task.create`)
- **Description**: one to two sentences explaining what the tool does and when to use it
- **Parameters**: typed parameter schema (JSON Schema format)
- **Returns**: description of the return value structure
- **Behavior hint**: if the tool creates drafts for review (e.g., communication tools), the description says so — the agent knows upfront that calling this tool will stage the action for review, not execute it immediately

### Budget Management

When the token budget is tight, tool descriptions are the first thing reduced:

1. **Full descriptions**: all tools in the resolved set get complete descriptions. This is the default when budget allows.
2. **Deprioritized tools dropped first**: tools deprioritized by stage 3 (flow node filter) are removed before any other cuts. An agent writing code does not need `email.compose` descriptions consuming prompt budget.
3. **Compact descriptions**: if still over budget, tool descriptions are shortened to name + one-line summary, dropping parameter schemas. The agent can still call the tool — the model has general knowledge of common tool patterns — but loses the specific parameter documentation.
4. **Essential tools only**: in extreme budget pressure, only tools directly relevant to the current task scope are included. For a task-scoped session, this means the task/flow/file/cli tools. For an org-scoped session, this means project/agent/memory tools.

In practice, tool descriptions are small relative to other layers. The full native catalog of ~55 tools with complete descriptions consumes roughly 4,000-5,000 tokens — a fraction of most context windows. Budget pressure on tools typically only occurs when external connections contribute dozens of additional tools.

### Caching and Versioning

Tool descriptions are cached and versioned. They do not change mid-session:

- The resolved tool set is computed at session start (or on the first turn) and cached.
- Within a session, the tool set is stable — tools do not appear or disappear between turns.
- If external connections change or agent capabilities are updated, the new tool set takes effect on the next session, not the current one.
- The tool set version is stored on the session metadata for debugging.

This avoids a class of confusing agent behavior where a tool is available on turn 3 but gone on turn 4. Stability within a session matters more than instant reactivity to configuration changes.

**Exception**: if the human explicitly revokes an agent's capability mid-session (e.g., "stop using the CLI"), the control plane enforces the revocation at execution time — the tool call returns "not permitted" even if the description is still in the prompt. The agent sees the denial and adapts. This is the guardrail path, not the primary mechanism.

## Tool Policy Model

Tool policy operates at two levels: the agent profile level (what tools are available) and the control plane level (what is allowed at runtime). These are complementary, not redundant.

### Agent Profile Tool Policy (Availability)

Defined on the agent profile (doc 05). Determines which tools appear in the agent's prompt and can be called. This is a static configuration set when the agent is created or updated.

```
tool_policy:
  allow:
    - "project.*"
    - "task.*"
    - "memory.query"
    - "file.*"
    - "cli.execute"
  deny:
    - "agent.create_temp"
```

The profile tool policy answers: "what tools does this agent have access to?" It is the coarse filter.

### Control Plane Capability Policy (Permission)

Defined in the control plane (doc 16). Determines whether a specific tool call is allowed or denied at runtime. Evaluated on every tier 2 tool call.

The capability model answers: "is this agent allowed to perform this specific action right now?" It is the fine filter.

Policy layers (from doc 16, highest priority first):
1. Instance safety policy
2. Organization policy
3. Project policy
4. Agent profile policy
5. Request-specific overrides (most restrictive only)

### How They Interact

```
Agent calls task.create
  |
  v
Is task.create in the resolved tool set? (profile filter passed)
  -> No: reject immediately, tool not available
  -> Yes: continue
  |
  v
Is task.create tier 1 or tier 2?
  -> Tier 2: route to control plane
  |
  v
Does agent hold project.task.create capability?
  -> No: deny (this should not happen if stage 4 resolution is correct)
  -> Yes: continue
  |
  v
Evaluate policy layers: allow / deny
  -> allow: execute
  -> deny: return "not permitted"
```

The profile filter ensures agents never see tools they should not have. The capability gate ensures the resolution pipeline was correct. The policy evaluation adds runtime context — the same capability might be `allow` in one project and `deny` in another.

### Communication Tool Drafts

Communication tools (`email.compose`, `slack.post`) create drafts as their **designed behavior**, not as a policy interception. The policy engine evaluates allow/deny for the communication capability. If allowed, the tool executes — and its normal successful output is a staged draft:

1. The tool succeeds. The tool result returned to the agent says "draft staged for review."
2. The tool itself creates a `draft_action_review` inbox item for the human (doc 03, Inbox section) with the composed content, context, and preview.
3. The agent's turn continues immediately. No blocking.
4. When the human acts on the inbox item (approve, edit+approve, reject), the inbox handler executes the final action (e.g., sends the email). This may trigger a new run for audit purposes.
5. The agent is not notified of the inbox decision within the same turn. If the agent needs to know the outcome, it can check `inbox.list` on a subsequent turn.

This is tool behavior, not policy. The distinction matters: the policy engine said "allow" — the agent has the communication capability. The tool then chose to stage a draft rather than send immediately, because creating drafts for human review is what communication tools do by design. The agent does the work of composing the action; the human does the work of approving it.

## External Tool Integration

External tools (currently via MCP, doc 09) participate in the same pipeline as native tools, with additional considerations for their dynamic nature.

### Discovery and Registration

When an external connection is registered (org-level or project-level), OtterCamp's client queries the server for its tool catalog. Each discovered tool is stored in `mcp_tool_catalog` (doc 09) with:

- Connection ID and slug
- Tool name (namespaced as `<connection_slug>.<tool_name>`, e.g., `github.create_issue` — see doc 09)
- Description (from the MCP server)
- Input schema (JSON Schema, from the MCP server)
- Output schema (if provided)

Note: tool names use the connection slug (human-readable), while capability names use the connection UUID (e.g., `mcp.tool.invoke:<connection_id>:<tool_name>`). The slug provides readable prompt names; the UUID provides stable policy references.

Discovered external tools default to disabled (`is_enabled = false` in `mcp_tool_catalog`, doc 09). The admin enables tools through conversation — Frank (org-level) or a PM (project-scoped) reports what was discovered and the admin decides which to enable. This is a critical security gate — a newly connected server's tools are visible but inert until explicitly enabled through conversation.

The MCP tool catalog is refreshed on connection health checks. New tools appear, removed tools disappear. But per the caching rule above, changes take effect on the next session, not the current one.

### Policy Integration

External tools are subject to the same four-stage resolution pipeline:

1. **Universe**: included if the connection is in the agent's scope.
2. **Agent profile**: matched against allow/deny patterns. `github.*` matches all tools from the "github" connection. `github.create_issue` matches one specific tool. Patterns use connection slugs, not UUIDs.
3. **Flow node**: deprioritized if the node does not declare relevant tool domains.
4. **Capability gate**: requires `mcp.tool.invoke:<connection_id>:<tool_name>` or the broader `mcp.connection.use:<connection_id>`.

### Execution

All external tool calls are tier 2, no exceptions. The execution path:

1. Policy evaluation (allow/deny)
2. Input validation against the stored JSON Schema
3. Call to the external server via the connection
4. Response capture and validation
5. Timeout handling (per-connection and per-tool timeouts)
6. Result returned to agent, logged as RunStep

### External Tool Descriptions in Prompt

External tool descriptions are generated from the stored catalog, not fetched from the server at prompt assembly time. This ensures consistency within a session and avoids latency from server calls during prompt assembly.

The description format is the same as native tools. If the server provides a rich description and parameter schema, those are used. If the description is sparse, the tool still appears with its name and parameter schema — the agent can reason about it from the name and schema alone.

### External Tool Limits

When a connection provides many tools (10+), prompt budget becomes a concern. The resolution is the same as for native tools — deprioritized external tools are dropped first when budget is tight. The agent profile can also use deny patterns to exclude specific external tools that are not relevant to the agent's work.

## Schema

### tool_definition

Stores metadata for native tools. This table is populated at application startup from the tool registry in code. It is not user-editable.

```sql
create table tool_definition (
  id              uuid primary key default gen_random_uuid(),
  name            text not null unique,                     -- e.g., "task.create", "file.read"
  domain          text not null check (domain in ('project', 'chat', 'memory', 'agent', 'system', 'browser', 'communication')),
  category        text not null check (category in ('native', 'system', 'browser', 'external')), -- matches the four-category taxonomy
  tier            int not null check (tier in (1, 2)),       -- 1 = chat-layer (read), 2 = control-plane (mutation)
  description     text not null,                            -- human-readable description for prompt
  parameter_schema jsonb not null default '{}',             -- JSON Schema for tool parameters
  return_schema   jsonb,                                    -- JSON Schema for return value (optional)
  required_capability text,                                 -- control plane capability needed (null for tier 1)
  is_active       boolean not null default true,            -- can be disabled without removal
  version         int not null default 1,                   -- bumped when description or schema changes
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create index on tool_definition (domain);
create index on tool_definition (tier);
create index on tool_definition (is_active) where is_active = true;
```

Design notes:
- `tier` is an integer (1 or 2), not an enum, because it is used in routing logic where an integer comparison is cleaner.
- `required_capability` is null for tier 1 tools — they rely on scope checks, not capabilities.
- `version` is incremented when the tool description or parameter schema changes across application releases. The session-cached tool set stores the version, enabling debugging when an agent's behavior differs from expectations.
- `is_active` allows disabling a tool without removing its definition — useful for feature flags and gradual rollouts.

### session_tool_set

Caches the resolved tool set for a session. Computed on the first turn, stable for the session's lifetime.

```sql
create table session_tool_set (
  id              uuid primary key default gen_random_uuid(),
  session_id      uuid not null references chat_session(id),
  agent_id        uuid not null references agent(id),
  tool_names      text[] not null,                          -- ordered list of tool names in the resolved set
  tool_versions   jsonb not null default '{}',              -- {tool_name: version} for debugging
  mcp_tools       jsonb not null default '[]',              -- [{connection_id, tool_name, schema_hash}]
  resolved_at     timestamptz not null default now(),

  unique (session_id, agent_id)
);

create index on session_tool_set (session_id);
```

Design notes:
- One row per agent per session. Different agents in the same session may have different tool sets.
- `tool_names` is the complete list of tools available to this agent in this session, in prompt inclusion order (highest priority first).
- `mcp_tools` (name reflects MCP as the primary external protocol) includes schema hashes so that external tool schema changes between sessions are detectable.
- This table is for debugging and observability. It is not on the critical path — the resolved tool set is held in memory during the session and persisted here for after-the-fact analysis.

### Cross-References

- `mcp_tool_catalog` (doc 09): stores the discovered tools from each external connection. `tool_definition` covers native tools; `mcp_tool_catalog` covers external tools. Together they form the tool universe.
- `tool_execution` (doc 16): the control plane's record of tier 2 tool executions. Links back to the tool name and captures input/output/timing.
- `chat_message` (doc 02): tier 1 tool calls are recorded as `tool_call`/`tool_result` messages. Tier 2 tool calls are recorded in both `chat_message` and `tool_execution`.
- `flow_node.tool_domains` (new field, optional): jsonb array of tool domain names that are relevant to this flow node. Used by stage 3 of the resolution pipeline.

## Runtime Behavior

### Tool Call Validation

When an agent emits a tool call during the turn loop:

1. **Name check**: is the tool name in the session's resolved tool set? If not, return an error. This catches hallucinated tool names.
2. **Parameter validation**: do the parameters match the tool's JSON Schema? If not, return a validation error with specifics. The agent can retry with corrected parameters.
3. **Tier routing**: look up the tool's tier. Route to chat-layer execution (tier 1) or control plane (tier 2).
4. **Execution**: execute the tool per its tier's path.
5. **Result formatting**: return the result in a consistent format regardless of tier. The agent does not know or care which tier a tool belongs to.

`file.write` gets one extra normalization pass before validation. If the provider leaves a recoverable `_raw` argument blob behind, the runtime extracts `path`, `content`, `encoding`, and `create_dirs` deterministically instead of spuriously failing with `path_required`. Recovery must handle the real write payloads seen in migration/template work, including malformed raw JSON that still contains valid multi-line content with embedded quotes, plus common content aliases such as `body`/`text`/`contents` when the provider serialized the payload under the wrong key. If `path` is still absent after normalization, the runtime returns one actionable validation payload. If `path` is present but `content` is still absent or nil, the runtime returns `content_required` rather than mislabeling the failure as missing-path noise or silently writing an empty file.

`cli.execute` gets the same recovery treatment before validation. If the provider emits `_raw`, uses legacy alias keys such as `cmd`, `script`, `working_dir`, `cwd`, `timeout_ms`, or `env`, or serializes the tool call as a bare shell string, the runtime normalizes that payload onto the canonical `command`, `working_directory`, `timeout_seconds`, and `env_overrides` fields before the executor validates it. Valid recovery commands must not fail with `command_required` solely because the model serialized the payload poorly.

Recovery turns reopened after deterministic validation blockers, whether the kickoff came from an explicit task resume or a supervisor wakeup, get one extra guardrail: if the provider emits `cli.execute` with no recoverable `command` at all, the turn runtime rejects that call before dispatch, injects one exact heredoc-style correction message back into the same turn, and gives the model one bounded retry. A second empty-command retry halts the turn with a diagnosable recovery message and, unless a real follow-on wakeup is already queued, moves the task into an explicit blocked state instead of leaving a silent `in_progress` dead end.

Those same recovery turns apply the bounded pattern to `file.write` when a concrete target `path` exists but `content` is still empty. The runtime first looks for a substantive assistant draft already produced in the same turn and, if found, carries that draft directly into the write so document-generation tasks can still produce the artifact. That recovered write is not complete until the runtime can verify the target file exists durably on disk; a nominal tool result with no file on disk is a recovery failure, not a success. In that case the turn must halt immediately, persist the draft plus the last write failure in `.ottercamp/recovery/...` when possible, and record the same failure reason in task checkpoint metadata so the next resume knows why the write did not land. If no draft exists yet, the runtime injects one correction that requires the full file body before another mutation attempt. A second empty-content retry halts the turn and persists a recovery checkpoint contract in task metadata. If the runtime can write a resumable artifact under `.ottercamp/recovery/...`, it must do that before telling the next turn to resume from the artifact; if it cannot, the halt message must use a truthful no-artifact contract instead of pointing at a non-existent file. That halt should persist `recovery_content_required` when the deployed `chat_turn` schema accepts it, and otherwise fall back to `model_error` rather than crashing the turn on stop-reason persistence. If no real follow-on wakeup is already queued, the task must also move out of misleading `in_progress` into `blocked` so the checkpoint is paired with a truthful bounded operator state. Supervisor recovery must not spin a new identical turn while that same unresolved checkpoint remains the task's current halted turn.

Recovery turns that never reach tool dispatch because prompt assembly stays compressed or prompt input keeps breaching guardrail size across the full continuation-depth budget must use the same truthful pattern. The runtime must halt with explicit guidance, suppress silent auto-continuation for that exhausted recovery loop, and either keep the task `in_progress` only when a real queued follow-on wakeup already exists or move it to `blocked` with instructions to narrow the next resume attempt. If task metadata already carries a recovery checkpoint, the guardrail halt must point the operator back to that checkpoint instead of dropping the recovery context.

### Error Handling

Tool call errors are returned as tool results, not as exceptions. The agent sees the error and can adapt:

- **Tool not found**: `{"error": "tool_not_found", "message": "No tool named 'task.destroy' exists."}`
- **Parameter validation**: `{"error": "invalid_parameters", "message": "Missing required field 'title'.", "details": {...}}`
- **`file.write` missing path**: `{"error": "path_required", "message": "file.write requires a non-empty path. Provide a workspace-relative file path in \`path\`."}`
- **`file.write` missing content**: `{"error": "content_required", "message": "file.write requires content. Provide file contents in \`content\`."}`
- **Permission denied**: `{"error": "permission_denied", "message": "You do not have permission to execute CLI commands in this project."}`
- **Execution failure**: `{"error": "execution_failed", "message": "Command exited with code 1.", "output": "..."}`
- **Timeout**: `{"error": "timeout", "message": "Tool execution exceeded the 30-second timeout."}`
- **Draft staged**: `{"status": "draft_staged", "inbox_item_id": "uuid", "message": "Email staged for human review."}`

The draft staged outcome is not an error — it is a normal result from communication tools. The tool succeeded; the action was staged for review as the tool's designed behavior. The agent should acknowledge it and continue its turn.

### Parallel Tool Execution

When the model emits multiple tool calls in one response:

- **Tier 1 tools**: can execute in parallel. They are read-only and independent.
- **Tier 2 tools**: execute sequentially. Mutations may have ordering dependencies, and the control plane processes them one at a time.
- **Mixed tier**: tier 1 tools execute in parallel first, then tier 2 tools execute sequentially.

This matches the behavior described in doc 02 (Streaming and Message Lifecycle).

### Timeouts

Each tool has a per-call timeout:

- **Tier 1 tools**: 5-second default. These are DB reads and file reads — if they take longer, something is wrong.
- **Tier 2 native tools**: 10-second default. Mutations are slightly more expensive but still fast.
- **CLI execution**: configurable, default 60 seconds. Some commands are legitimately slow (builds, tests).
- **Browser tools**: configurable, default 30 seconds. Page loads and interactions vary.
- **External tools**: per-connection configurable, default 30 seconds. Remote services have variable latency.

Timeouts are enforced by the execution layer. A timed-out tool call returns a timeout error to the agent. The turn duration envelope (doc 02) provides the outer bound — individual tool timeouts are within that.

## Resolved Decisions

- **Four tool categories**: native, system, browser, external. Every tool falls into exactly one. Browser is separated from system due to distinct isolation requirements and risk profile. External covers MCP servers and any remote API — the common trait is that the service is outside OtterCamp's control.
- **Two-tier execution**: tier 1 (read-only, chat-layer) and tier 2 (mutations, control-plane). Tier is static, set at registration. Default is tier 2. Tier 1 is a whitelist.
- **All external tools are tier 2**: remote API calls (MCP or otherwise) cannot be verified as side-effect-free. No exceptions.
- **All browser tools are tier 2**: even screenshots have external side effects (network requests, cookies).
- **No dynamic tool generation**: agents cannot create new tools at runtime. All tools are native (shipped) or external (discovered from MCP servers or registered via API).
- **Tool descriptions cached per session**: the resolved tool set is computed once and does not change mid-session. Configuration changes take effect on the next session.
- **Four-stage resolution pipeline**: universe -> profile filter -> flow node filter -> capability gate. Each stage narrows the set.
- **Flow node filter is a soft filter**: deprioritizes for prompt budget, does not exclude from callable set. Access control is handled by stages 2 and 4.
- **Profile tool policy uses allow/deny with globs**: `project.*`, `github.*` (MCP connection slug), etc. Deny always wins over allow.
- **Tool descriptions are lowest-priority prompt layer**: dropped first when budget is tight. Deprioritized tools (from flow node filter) are dropped before others.
- **Compact description fallback**: when budget forces tool description reduction, names + one-line summaries replace full schemas. Agent can still call tools.
- **Communication tool drafts are a normal result, not an error**: the tool succeeds and stages a draft. The agent's turn continues. Inbox item is created by the tool. Agent is not notified of the decision within the same turn.
- **Parallel tier 1, sequential tier 2**: multiple tool calls in one model response follow this execution order.
- **Error results, not exceptions**: all tool failures are returned as structured tool results the agent can interpret and adapt to.
- **Native tool catalog is approximately 55 tools across 7 domains**: project, chat, memory, agent, system, browser, communication. This is the complete set.
- **Capability revocation mid-session is enforced at execution time**: the tool description may still be in the prompt, but the call returns "not permitted." This is the guardrail, not the primary mechanism.
- **Communication tools create drafts as their designed behavior**: emails, Slack posts, and similar external communications are staged for human review. This is tool behavior, not policy interception — the policy engine evaluates allow/deny for the capability, and the tool itself stages the draft.
- **`memory.query` is tier 1**: it is a read operation on the memory system. All agents have access to it.
- **`memory.record` is tier 2**: it creates durable state (a memory item). Goes through the control plane.
- **External tools default to disabled**: discovered tools in `mcp_tool_catalog` have `is_enabled = false` until an admin explicitly enables them through conversation. This is defense-in-depth independent of the capability posture.
- **Tool names use connection slugs, capability names use connection UUIDs**: external tool names (e.g., `github.create_issue`) use human-readable slugs for prompt clarity. Capability names (e.g., `mcp.tool.invoke:<uuid>:<tool_name>`) use stable UUIDs for policy evaluation.
- **Tool resolution is per-session, not per-turn**: the four-stage pipeline runs once when an agent joins a session. The resolved set is cached and stable for the session's lifetime. Configuration changes take effect on the next session.

## Open Questions

- **Tool usage analytics**: should we track per-agent, per-tool call frequency and success rate as a first-class metric? Useful for identifying agents that struggle with specific tools, or tools that fail frequently. Deferred to observability spec (doc 13).
- **Tool versioning across external schema changes**: when an external server updates a tool's schema between sessions, how do we communicate the change to the agent? The cached-per-session model means the agent sees the old schema until next session. If schema changes are breaking, the tool call may fail with a validation error — is that sufficient, or do we need proactive notification?
- **Bulk operations**: should there be a `task.create_batch` or similar tools for agents that need to create many items at once? Currently, an agent creating 10 subtasks makes 10 individual `subtask.create` calls. The overhead may be acceptable, or it may warrant batch tools.
