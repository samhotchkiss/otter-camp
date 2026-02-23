---
## Summary

This spec defines the complete tool system for OtterCamp V2 -- how agents discover, access, and execute tools. Tools are what transform agents from conversation partners into operators that can act on the world. The system answers four questions for every tool call: what tools exist, which does this agent have, what is allowed right now, and how does it execute. All tools fall into exactly one of four categories: Native (shipped with OtterCamp, operating on domain entities like tasks/projects/memory), System (filesystem and CLI access within sandboxed workspaces), Browser (web interaction in isolated contexts), and MCP (dynamically discovered from external MCP servers).

The core architectural pattern is a two-tier execution model. Tier 1 tools are read-only operations (listing tasks, querying memory, reading files) that execute directly in the chat layer with a simple scope check -- fast and lightweight since reads vastly outnumber writes in a typical agent turn. Tier 2 tools are mutations and external calls that route through the full control plane pipeline: policy evaluation (allow/deny/draft_review), broker execution, sandboxing, and RunStep audit trail. Tier assignment is static and defaults to tier 2; tier 1 is a strict whitelist. All MCP and browser tools are always tier 2 with no exceptions.

Tool availability is determined by a four-stage resolution pipeline that runs once per turn: (1) Universe -- all native plus active MCP tools, (2) Agent Profile Filter -- allow/deny lists with glob patterns from the agent's profile (e.g., Frank gets project/task/session tools but no system or browser tools), (3) Flow Node Filter -- a soft deprioritization for prompt budget, not a hard access gate, (4) Capability Gate -- tier 2 tools require matching control plane capabilities. The resolved tool set is cached per session for stability. The native catalog ships approximately 40 tools across 7 domains (project, chat, memory, agent, system, browser, communication). Key schema tables are `tool_definition` (native tool metadata, populated at startup) and `session_tool_set` (per-agent, per-session cache of resolved tools for debugging). The `draft_review` policy outcome is central to sensitive operations like email and Slack -- the action is staged in the human's inbox rather than executed immediately, and the agent's turn continues without blocking.

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

System tools are always available but heavily policy-gated. The capabilities `system.cli.execute`, `system.file.write`, and `system.browser.control` (doc 16) gate access.

### Category 3: Browser Tools

Tools for web interaction — navigating pages, clicking elements, extracting content, capturing screenshots. Browser tools run in isolated browser contexts per run/session (doc 11).

Separated from system tools because browser execution has distinct isolation requirements (domain allowlists/denylists, per-run contexts, artifact capture) and a different risk profile.

### Category 4: MCP Tools

Tools dynamically discovered from connected MCP servers (doc 09). These are external — they call services outside OtterCamp's control. An MCP tool's schema, description, and capabilities come from the MCP server at connection time, not from OtterCamp's codebase.

MCP tools are the only category that is not statically known. They appear and disappear as MCP connections are added, removed, or updated. Once discovered, they participate in the same policy pipeline as native tools.

## Native Tool Catalog

The complete set of tools that ship with OtterCamp before any MCP connections are configured. Grouped by domain. Each tool is tagged with its execution tier (see Two-Tier Execution below).

### Project and Task Domain

| Tool | Description | Tier |
|---|---|---|
| `project.list` | List projects the agent has access to | 1 (read) |
| `project.get` | Get project details, context block, repo info | 1 (read) |
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

### Chat and Session Domain

| Tool | Description | Tier |
|---|---|---|
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

### System Domain

| Tool | Description | Tier |
|---|---|---|
| `file.read` | Read file contents from the project workspace | 1 (read) |
| `file.list` | List files in a directory within the project workspace | 1 (read) |
| `file.search` | Search file contents in the project workspace (grep/ripgrep equivalent) | 1 (read) |
| `file.write` | Write or update a file in the project workspace | 2 (mutation) |
| `file.delete` | Delete a file from the project workspace | 2 (mutation) |
| `cli.execute` | Execute a shell command in the project workspace sandbox | 2 (mutation) |
| `git.status` | Get git status of the project workspace | 1 (read) |
| `git.diff` | Get diff of working changes or between refs | 1 (read) |
| `git.log` | Get commit history | 1 (read) |
| `git.commit` | Commit staged changes to the task branch | 2 (mutation) |

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
| `email.compose` | Compose and stage an email for review | 2 (mutation, typically `draft_review`) |
| `slack.post` | Post a message to a Slack channel | 2 (mutation, typically `draft_review`) |

Communication tools default to `draft_review` policy — the action is staged in the human's inbox rather than executed immediately. The human reviews, edits if needed, and approves. See doc 02 for the `draft_review` outcome and doc 03 for the inbox model.

### Catalog Size and Scope

The native catalog above contains approximately 40 tools across 7 domains. This is the full set that ships with OtterCamp. It is intentionally bounded:

- Every tool maps to a real operation agents need to perform.
- There are no "convenience" tools that bundle multiple operations.
- There is no tool for creating new tools at runtime — all tools are either native (shipped with OtterCamp) or from MCP servers. Agents cannot create new tools.

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
- `session.get`, `session.history`
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

**Permission check for tier 1**: The check is scope-based, not policy-based. An agent in a project-scoped session can read tasks in that project. An agent in a task-scoped session can read files in that task's workspace. The check answers "is this data within the agent's current scope?" — not "is this agent allowed to read?". All agents can read within their scope. If a tier 1 call requests data outside the agent's scope (e.g., reading a task from another project), it returns an error, not a policy denial.

### Tier 2: Control-Plane Tools (Mutations, External, Side Effects)

These change state or reach outside OtterCamp. Full control plane path (doc 16): policy evaluation (allow/deny/draft_review), execution via broker, RunStep audit trail, artifact capture.

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
- All MCP tools (every MCP call is tier 2, no exceptions)

**Execution path:**
```
Agent requests tool call
  -> Tool registry lookup: tier 2
  -> Route to control plane
  -> Policy evaluation:
     - allow: dispatch to broker -> sandbox -> execute -> return result
     - deny: return "not permitted" as tool result
     - draft_review: stage action, return "queued for review" as tool result
  -> Log as chat_message (tool_call + tool_result)
  -> Log as RunStep in control plane audit trail
```

### Why Two Tiers

In a typical async turn, an agent might do 30 reads and 3 writes. Running 30 reads through the full broker pipeline (create RunStep, evaluate policy, dispatch, capture, finalize) adds substantial overhead for operations that will always be allowed within scope. The two-tier model keeps reads fast and lightweight while preserving full control plane rigor for actions with consequences.

### Tier Assignment Rules

1. **Default is tier 2.** When in doubt, it is tier 2. The cost of running a read through the control plane is latency overhead. The cost of running a mutation through the chat layer is missing audit trail and policy checks.
2. **Tier 1 is a whitelist.** Only tools explicitly listed as tier 1 bypass the control plane. The list above is exhaustive.
3. **Tools may migrate from tier 1 to tier 2** if requirements change (e.g., adding rate limiting or compliance logging to file reads). Migration never goes the other direction — a tool does not move from tier 2 to tier 1.
4. **MCP tools are always tier 2.** External calls are never side-effect-free from OtterCamp's perspective, even if the MCP server claims the operation is read-only. We cannot verify that claim.

## Tool Resolution Pipeline

When an agent takes a turn, it needs a concrete set of tools. The tool set is determined by a four-stage resolution pipeline that runs once at the start of each turn, as part of prompt assembly (doc 05, layer 7).

### Stage 1: Universe

Start with all registered tools — the full native catalog plus all tools from active MCP connections in the agent's scope.

For MCP tools, "active MCP connections in the agent's scope" means: connections registered at the org level, plus connections registered at the project level for the agent's current project. Connection health is checked — tools from unhealthy connections (circuit breaker open, connection down) are excluded.

### Stage 2: Agent Profile Filter

Apply the agent's tool policy from its profile (doc 05). The agent profile carries allow/deny lists:

- **Allow list**: if non-empty, only these tools are available. Everything else is excluded.
- **Deny list**: these tools are explicitly excluded, even if they appear on the allow list.

The allow/deny model works on tool names with glob support:
- `project.*` — all tools in the project domain
- `file.write` — a specific tool
- `mcp.*` — all MCP tools
- `mcp.tool:<connection_id>:*` — all tools from a specific MCP connection
- `browser.*` — all browser tools
- `*` — everything (the default allow list for unrestricted agents)

**Starter trio defaults:**
- **Frank** (Chief of Staff): `project.*`, `task.*`, `subtask.*`, `flow.*`, `session.*`, `memory.*`, `agent.*`, `inbox.*`, `schedule.*`. No system tools, no browser, no MCP. Frank operates at the organizational level — he creates tasks and coordinates, he does not execute.
- **Lori** (Agent Relations): `agent.*`, `project.list`, `project.get`, `task.list`, `task.get`, `memory.query`. Lori manages staffing, not execution.
- **Ellie** (Memory): `memory.*`, `session.history`, `file.read`, `file.search`. Ellie reads broadly for memory extraction but does not mutate project state.

Worker agents typically get the full catalog — they need system tools, file access, CLI, and potentially browser and MCP tools to do their jobs. Reviewer agents get read tools plus `flow.review_decision`.

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
| `task.create`, `task.update` | `project.task.update` |
| `flow.advance`, `flow.review_decision` | `project.flow.advance` |
| `file.write`, `file.delete` | `system.file.write` |
| `cli.execute` | `system.cli.execute` |
| `browser.*` | `system.browser.control` |
| `git.commit` | `system.file.write` |
| `session.create`, `session.invite_agent` | `chat.participant.manage` |
| `message.send` | `chat.message.write` |
| `memory.record` | `memory.write` |
| `agent.create_temp` | `agent.manage` |
| MCP tools | `mcp.tool.invoke:<connection_id>:<tool_name>` |
| `email.compose`, `slack.post` | `communication.send` |

Tools that require a capability the agent does not hold are excluded from the tool set. They do not appear in the prompt and cannot be called.

Tier 1 tools bypass the capability gate — they are governed by scope checks at execution time, not by pre-assigned capabilities.

### Pipeline Summary

```
All registered tools (native + MCP)
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
- **Policy hint**: if the tool is under `draft_review` policy, the description says so — the agent knows upfront that calling this tool will stage the action for review, not execute it immediately

### Budget Management

When the token budget is tight, tool descriptions are the first thing reduced:

1. **Full descriptions**: all tools in the resolved set get complete descriptions. This is the default when budget allows.
2. **Deprioritized tools dropped first**: tools deprioritized by stage 3 (flow node filter) are removed before any other cuts. An agent writing code does not need `email.compose` descriptions consuming prompt budget.
3. **Compact descriptions**: if still over budget, tool descriptions are shortened to name + one-line summary, dropping parameter schemas. The agent can still call the tool — the model has general knowledge of common tool patterns — but loses the specific parameter documentation.
4. **Essential tools only**: in extreme budget pressure, only tools directly relevant to the current task scope are included. For a task-scoped session, this means the task/flow/file/cli tools. For an org-scoped session, this means project/agent/memory tools.

In practice, tool descriptions are small relative to other layers. The full native catalog of ~40 tools with complete descriptions consumes roughly 3,000-4,000 tokens — a fraction of most context windows. Budget pressure on tools typically only occurs when MCP connections contribute dozens of additional tools.

### Caching and Versioning

Tool descriptions are cached and versioned. They do not change mid-session:

- The resolved tool set is computed at session start (or on the first turn) and cached.
- Within a session, the tool set is stable — tools do not appear or disappear between turns.
- If MCP connections change or agent capabilities are updated, the new tool set takes effect on the next session, not the current one.
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

Defined in the control plane (doc 16). Determines whether a specific tool call is allowed, denied, or requires approval at runtime. Evaluated on every tier 2 tool call.

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
Does agent hold project.task.update capability?
  -> No: deny (this should not happen if stage 4 resolution is correct)
  -> Yes: continue
  |
  v
Evaluate policy layers: allow / deny / draft_review
  -> allow: execute
  -> deny: return "not permitted"
  -> draft_review: stage for inbox review
```

The profile filter ensures agents never see tools they should not have. The capability gate ensures the resolution pipeline was correct. The policy evaluation adds runtime context — the same capability might be `allow` in one project and `draft_review` in another.

### The `draft_review` Outcome

When a tool call results in `draft_review`:

1. The action is staged — not executed. The tool result returned to the agent says "queued for review."
2. An inbox item is created for the human (doc 03, Inbox section) with the action payload, context, and preview.
3. The agent's turn continues immediately. No blocking.
4. When the human acts on the inbox item (approve, edit+approve, reject), the action executes or is discarded.
5. The agent is not notified of the inbox decision within the same turn. If the agent needs to know the outcome, it can check `inbox.list` on a subsequent turn.

`draft_review` is the primary mechanism for sensitive operations — sending emails, posting to external channels, deploying to production. The agent does the work of composing the action; the human does the work of approving it.

## MCP Tool Integration

MCP tools (doc 09) participate in the same pipeline as native tools, with additional considerations for their dynamic nature.

### Discovery and Registration

When an MCP connection is registered (org-level or project-level), OtterCamp's MCP client queries the server for its tool catalog. Each discovered tool is stored in `mcp_tool_catalog` (doc 09) with:

- Connection ID
- Tool name (namespaced as `mcp:<connection_id>:<tool_name>`)
- Description (from the MCP server)
- Input schema (JSON Schema, from the MCP server)
- Output schema (if provided)

The MCP tool catalog is refreshed on connection health checks. New tools appear, removed tools disappear. But per the caching rule above, changes take effect on the next session, not the current one.

### Policy Integration

MCP tools are subject to the same four-stage resolution pipeline:

1. **Universe**: included if the connection is in the agent's scope.
2. **Agent profile**: matched against allow/deny patterns. `mcp.*` matches all MCP tools. `mcp.tool:<connection_id>:*` matches all tools from a specific connection. `mcp.tool:<connection_id>:search` matches one specific tool.
3. **Flow node**: deprioritized if the node does not declare MCP-relevant domains.
4. **Capability gate**: requires `mcp.tool.invoke:<connection_id>:<tool_name>` or the broader `mcp.connection.use:<connection_id>`.

### Execution

All MCP tool calls are tier 2, no exceptions. The execution path:

1. Policy evaluation (allow/deny/draft_review)
2. Input validation against the stored JSON Schema
3. Call to the MCP server via the connection
4. Response capture and validation
5. Timeout handling (per-connection and per-tool timeouts)
6. Result returned to agent, logged as RunStep

### MCP Tool Descriptions in Prompt

MCP tool descriptions are generated from the stored catalog, not from the MCP server at prompt assembly time. This ensures consistency within a session and avoids latency from MCP server calls during prompt assembly.

The description format is the same as native tools. If the MCP server provides a rich description and parameter schema, those are used. If the description is sparse, the tool still appears with its name and parameter schema — the agent can reason about it from the name and schema alone.

### MCP Tool Limits

When an MCP connection provides many tools (10+), prompt budget becomes a concern. The resolution is the same as for native tools — deprioritized MCP tools are dropped first when budget is tight. The agent profile can also use deny patterns to exclude specific MCP tools that are not relevant to the agent's work.

## Schema

### tool_definition

Stores metadata for native tools. This table is populated at application startup from the tool registry in code. It is not user-editable.

```sql
create table tool_definition (
  id              uuid primary key default gen_random_uuid(),
  name            text not null unique,                     -- e.g., "task.create", "file.read"
  domain          text not null,                            -- project, chat, memory, agent, system, browser, communication
  category        text not null,                            -- native, system, browser, communication
  tier            int not null,                             -- 1 = chat-layer (read), 2 = control-plane (mutation)
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
  agent_id        uuid not null,
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
- `mcp_tools` includes schema hashes so that MCP tool schema changes between sessions are detectable.
- This table is for debugging and observability. It is not on the critical path — the resolved tool set is held in memory during the session and persisted here for after-the-fact analysis.

### Cross-References

- `mcp_tool_catalog` (doc 09): stores the discovered tools from each MCP connection. `tool_definition` covers native tools; `mcp_tool_catalog` covers MCP tools. Together they form the tool universe.
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

### Error Handling

Tool call errors are returned as tool results, not as exceptions. The agent sees the error and can adapt:

- **Tool not found**: `{"error": "tool_not_found", "message": "No tool named 'task.destroy' exists."}`
- **Parameter validation**: `{"error": "invalid_parameters", "message": "Missing required field 'title'.", "details": {...}}`
- **Permission denied**: `{"error": "permission_denied", "message": "You do not have permission to execute CLI commands in this project."}`
- **Execution failure**: `{"error": "execution_failed", "message": "Command exited with code 1.", "output": "..."}`
- **Timeout**: `{"error": "timeout", "message": "Tool execution exceeded the 30-second timeout."}`
- **Draft review**: `{"status": "queued_for_review", "inbox_item_id": "uuid", "message": "Email staged for human review."}`

The `draft_review` outcome is not an error — it is a normal result. The agent should acknowledge it and continue its turn.

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
- **MCP tools**: per-connection configurable, default 30 seconds. External services have variable latency.

Timeouts are enforced by the execution layer. A timed-out tool call returns a timeout error to the agent. The turn duration envelope (doc 02) provides the outer bound — individual tool timeouts are within that.

## Resolved Decisions

- **Four tool categories**: native, system, browser, MCP. Every tool falls into exactly one. Browser is separated from system due to distinct isolation requirements and risk profile.
- **Two-tier execution**: tier 1 (read-only, chat-layer) and tier 2 (mutations, control-plane). Tier is static, set at registration. Default is tier 2. Tier 1 is a whitelist.
- **All MCP tools are tier 2**: external calls cannot be verified as side-effect-free. No exceptions.
- **All browser tools are tier 2**: even screenshots have external side effects (network requests, cookies).
- **No dynamic tool generation**: agents cannot create new tools at runtime. All tools are native (shipped) or MCP (discovered from servers).
- **Tool descriptions cached per session**: the resolved tool set is computed once and does not change mid-session. Configuration changes take effect on the next session.
- **Four-stage resolution pipeline**: universe -> profile filter -> flow node filter -> capability gate. Each stage narrows the set.
- **Flow node filter is a soft filter**: deprioritizes for prompt budget, does not exclude from callable set. Access control is handled by stages 2 and 4.
- **Profile tool policy uses allow/deny with globs**: `project.*`, `mcp.tool:<connection_id>:*`, etc. Deny always wins over allow.
- **Tool descriptions are lowest-priority prompt layer**: dropped first when budget is tight. Deprioritized tools (from flow node filter) are dropped before others.
- **Compact description fallback**: when budget forces tool description reduction, names + one-line summaries replace full schemas. Agent can still call tools.
- **`draft_review` is a normal result, not an error**: the agent's turn continues. Inbox item is created. Agent is not notified of the decision within the same turn.
- **Parallel tier 1, sequential tier 2**: multiple tool calls in one model response follow this execution order.
- **Error results, not exceptions**: all tool failures are returned as structured tool results the agent can interpret and adapt to.
- **Native tool catalog is approximately 40 tools across 7 domains**: project, chat, memory, agent, system, browser, communication. This is the complete set.
- **Capability revocation mid-session is enforced at execution time**: the tool description may still be in the prompt, but the call returns "not permitted." This is the guardrail, not the primary mechanism.
- **Communication tools default to `draft_review`**: emails, Slack posts, and similar external communications are staged for human review.
- **`memory.query` is tier 1**: it is a read operation on the memory system. All agents have access to it.
- **`memory.record` is tier 2**: it creates durable state (a memory item). Goes through the control plane.

## Open Questions

- **Tool usage analytics**: should we track per-agent, per-tool call frequency and success rate as a first-class metric? Useful for identifying agents that struggle with specific tools, or tools that fail frequently. Deferred to observability spec (doc 13).
- **Tool versioning across MCP schema changes**: when an MCP server updates a tool's schema between sessions, how do we communicate the change to the agent? The cached-per-session model means the agent sees the old schema until next session. If schema changes are breaking, the tool call may fail with a validation error — is that sufficient, or do we need proactive notification?
- **Bulk operations**: should there be a `task.create_batch` or similar tools for agents that need to create many items at once? Currently, an agent creating 10 subtasks makes 10 individual `subtask.create` calls. The overhead may be acceptable, or it may warrant batch tools.
