---
## Summary

This spec defines how OtterCamp integrates with external systems via MCP (Model Context Protocol). OtterCamp acts strictly as an MCP **client** -- it connects to external MCP servers to consume their tools, resources, and prompts, but never exposes itself as an MCP server. The connector runtime is a top-level architectural component responsible for managing the full lifecycle of MCP connections: establishment, health monitoring, circuit breaking, secret injection, and execution brokering. Both stdio (subprocess) and HTTP/SSE (remote) transports are supported at launch.

MCP tools are classified as tier 2 (external mutations) and follow the full control plane policy path: capability checks (`mcp.connection.use`, `mcp.tool.invoke`), agent profile allow/deny lists, and layered policy evaluation. The result is allow or deny. Every call produces a `ToolExecution` record and an `mcp_execution_log` entry with redacted inputs/outputs. Secrets (API keys, tokens) are stored in an encrypted secret store, bound to connections via `mcp_secret_binding`, and injected at runtime by the connector runtime -- agents never see credentials. Resources are read-only (tier 1) and prompts are advisory-only (yielding to OtterCamp skills on conflict).

**Context-aware tool loading** prevents MCP from bloating agent context windows. By default, agents receive only lightweight connection summaries (~20 tokens each) -- not full tool schemas. Agents discover tool schemas on demand via `mcp.discover`. Flow nodes that require specific MCP tools declare them in `flow_node.mcp_tools`, and those schemas are preloaded into the worker's prompt at session start -- no discovery round-trip needed.

Connections are scoped to either an organization or a project and are configured entirely through conversation with Frank (org-level) or the project PM (project-level) -- there is no MCP configuration UI. Tool discovery happens via the MCP `tools/list` endpoint, and all discovered tools default to disabled until an admin explicitly enables them (default-deny posture). The schema comprises four tables: `mcp_connection` (connection config, transport, health, circuit breaker settings), `mcp_tool_catalog` (discovered tools/resources/prompts with enablement flags), `mcp_execution_log` (full audit trail of every MCP interaction), and `mcp_secret_binding` (maps secret references to injection points). Reliability is handled through configurable health checks, a per-connection circuit breaker (closed/open/half-open states), timeout policies, and retry with exponential backoff.

---

# 09. MCP Integration

## Goal

Provide a first-class MCP (Model Context Protocol) client layer so agents can use external tools, resources, and prompts from third-party servers — safely, auditably, and through the same policy pipeline that governs every other agent action — without bloating agent context windows.

## What MCP Provides

MCP is an open protocol for connecting AI agents to external systems. An MCP server exposes three primitive types:

- **Tools**: callable functions with typed inputs and outputs. Examples: create a GitHub issue, query a database, send a Slack message, run a SQL query.
- **Resources**: read-only data the agent can reference. Examples: a file listing, a database schema, a configuration document.
- **Prompts**: reusable prompt templates provided by the server. Examples: a code review template, a data analysis workflow prompt, a troubleshooting guide.

OtterCamp acts as an **MCP client**. It connects to external MCP servers, discovers what they offer, and makes those capabilities available to agents within OtterCamp's policy and security framework. OtterCamp never exposes itself as an MCP server — it consumes, it does not serve.

## Architecture Context

The **connector runtime** is a top-level component (see 01-architecture-and-domain.md). It manages the lifecycle of MCP connections, handles transport-level concerns (connection pooling, health, reconnection), and brokers execution requests between the control plane and external MCP servers.

MCP tools are tier 2 tools (see 02-chat.md Tool Execution Tiers) — they are external mutations by definition. Every MCP tool call goes through the full control plane path: policy evaluation, execution via broker, RunStep audit trail, artifact capture.

```
Agent requests MCP tool call
│
├─ Tool registry lookup → tier 2 (MCP)
│
├─ Control plane policy evaluation
│   ├─ Capability check: mcp.connection.use:<connection_id>
│   ├─ Capability check: mcp.tool.invoke:<connection_id>:<tool_name>
│   ├─ Agent profile tool policy (allow/deny list)
│   ├─ Result: allow / deny
│   │
│   ├─ deny → return "not permitted" to agent
│   └─ allow → dispatch to connector runtime
│
├─ Connector runtime
│   ├─ Resolve connection (health check, circuit breaker)
│   ├─ Inject secrets (runtime resolution, never in prompt)
│   ├─ Validate input against cataloged tool schema
│   ├─ Execute MCP tool call via transport (stdio or HTTP/SSE)
│   ├─ Validate and sanitize response
│   └─ Return result
│
├─ Control plane records ToolExecution
│   ├─ Input (with secrets redacted)
│   ├─ Output
│   ├─ Timing
│   ├─ Connection and tool identifiers
│   └─ Policy decision metadata
│
└─ Result returned to agent in turn loop
```

## Context-Aware Tool Loading

### The Problem: Context Window Bloat

A naive MCP integration loads every available tool schema into every agent's prompt. A single MCP connection can expose 20-50 tools, each with a JSON Schema that costs 50-200 tokens. Three connections at 30 tools each = 4,500-18,000 tokens of tool schemas in every prompt — most of which the agent will never use in that turn. This crowds out space for conversation history, memory, and skills that actually matter for the task at hand.

OtterCamp solves this with **context-aware tool loading**: agents know what connections exist, but tool schemas are only loaded into the prompt when they're actually needed.

### Three Loading Modes

#### 1. Lazy Discovery (Default)

The default for all agents. The agent's prompt includes a lightweight connection summary for each available MCP connection — just the connection name and a one-line description. This costs ~20 tokens per connection regardless of how many tools the connection offers.

What the agent sees in its prompt (layer 7, tool descriptions):

```
Available MCP connections:
  github — GitHub repository management (issues, PRs, branches)
  slack — Slack workspace messaging
  postgres_staging — Staging database queries

Use mcp.discover("<connection>") to see available tools for a connection.
```

The agent does NOT see individual tool schemas until it actively discovers them. If the agent needs to use a GitHub tool, it first calls `mcp.discover("github")` to get the list of available tools and their schemas, then calls the specific tool it needs.

This is ideal for staff agents working in open-ended chat sessions where the MCP tools they'll need aren't predictable in advance.

#### 2. Flow Node Preloading (Eager When Declared)

Flow nodes can declare which MCP tools they require. When a temp worker is spun up for that node, the declared tool schemas are preloaded into the worker's prompt at layer 7 — no discovery round-trip needed. The worker is immediately ready to use those tools.

This is analogous to how `flow_node.skills` declares which OtterCamp skills a node needs (loaded at layer 4). MCP tools are declared in `flow_node.mcp_tools` and loaded at layer 7.

Example: a "Create PR" work node declares:

```json
{
  "mcp_tools": ["github.create_pull_request", "github.add_labels", "github.request_review"]
}
```

When a temp worker spins up for this node, those three tool schemas are preloaded into its prompt alongside any native OtterCamp tools. The worker can immediately call `github.create_pull_request(...)` without a discovery step.

This is the primary mode for temp workers executing flow nodes. The PM knows what tools the node needs and declares them at flow design time. The temp worker gets exactly the tools it needs, nothing more.

#### 3. Full Toolset Loading (Opt-In per Connection)

For connections with a small number of tools where discovery overhead isn't worth it, an admin can set `eager_load = true` on the connection. All enabled tools from that connection are loaded into every agent's prompt that has access to the connection.

This should only be used for connections with fewer than ~10 tools. The admin sets this through conversation: "Frank, set the Sentry connection to eager-load its tools."

### How It Works in Prompt Assembly

Tool loading integrates with the 7-layer prompt assembly pipeline (see 05-agents-staff-and-temps.md):

- **Layer 7 (tool descriptions)**: the assembly process checks each available MCP connection:
  1. If the connection has `eager_load = true`: include all enabled tool schemas.
  2. If the current flow node declares `mcp_tools` for this connection: include only the declared tool schemas.
  3. Otherwise: include only the lightweight connection summary line.
- **The `mcp.discover` tool** is always available (it's a native OtterCamp tool, not an MCP tool). It returns tool schemas from the catalog — no round-trip to the MCP server.

The `mcp.discover` result appears in the conversation history as a tool_result message, making the schemas available for the agent's subsequent turns. This is "just-in-time" schema loading — the agent gets schemas when it needs them, and they persist in conversation context naturally.

### Flow Node MCP Tool Declaration

Flow nodes declare required MCP tools in the `flow_node.mcp_tools` JSONB column (see 03-projects-and-task-flow.md). The format is an array of namespaced tool names:

```json
["github.create_issue", "github.list_prs", "slack.send_message"]
```

Each entry uses the `<connection_slug>.<tool_name>` format, matching how tools are referenced throughout the system.

At session start for a flow node execution:
1. The prompt assembler reads `flow_node.mcp_tools`.
2. For each declared tool, it looks up the full schema from `mcp_tool_catalog`.
3. If the tool is enabled and the agent has the required capabilities, the schema is included in layer 7.
4. If a declared tool is not found, disabled, or the connection is unhealthy, a warning is included in the prompt instead of the schema, and a domain event is emitted.

The PM configures this through conversation when designing flows: "For the code review node, preload the GitHub PR review tools." The PM can also adjust it later: "Add `github.create_issue` to the bug triage node's MCP tools."

### The mcp.discover Tool

`mcp.discover` is a native OtterCamp utility tool (no policy evaluation, no network call) that returns tool information from the local catalog:

```
mcp.discover(connection: string, filter?: string) → Tool[]
```

- `connection`: the connection slug (e.g., `"github"`).
- `filter`: optional substring filter on tool names (e.g., `"issue"` to see only issue-related tools).
- Returns: array of `{name, description, input_schema}` for all enabled tools the agent has access to on that connection.

The tool reads from `mcp_tool_catalog` — it does not make a network call to the MCP server. It's fast and free.

Example agent interaction:
```
Agent: I need to create a GitHub issue for this bug.
       [calls mcp.discover("github", "issue")]
System: Found 3 tools:
        - github.create_issue(title, body, labels, assignees)
        - github.list_issues(state, labels, assignee)
        - github.close_issue(issue_number, comment)
Agent: [calls github.create_issue(title: "Auth timeout bug", body: "...", labels: ["bug"])]
```

## Connection Management

### What an MCP Connection Is

An MCP connection represents a configured link between OtterCamp and a single external MCP server. It captures everything needed to establish, authenticate, and manage that link.

### Scope Levels

Connections are scoped to either an organization or a project:

- **Org-level connections**: available to all projects and agents within the org. Suitable for shared infrastructure — GitHub, Slack, company databases.
- **Project-level connections**: available only within that project. Suitable for project-specific services — a staging environment, a project-specific API, a specialized tool server.

An agent can use a connection if (a) it is within the connection's scope and (b) the agent has the capability grant for that connection (see Security).

### Transport Types

Both MCP transport types are supported at launch:

- **stdio**: the MCP server runs as a subprocess. OtterCamp spawns the process and communicates via stdin/stdout. Best for local tool servers, language-specific SDKs, and self-hosted environments where subprocess management is feasible.
- **HTTP/SSE (Streamable HTTP)**: the MCP server runs as a remote HTTP service. OtterCamp connects via HTTP for requests and SSE for streaming responses. Best for remote/cloud-hosted MCP servers, shared infrastructure, and multi-tenant MCP providers.

The transport type is set per connection and does not change after creation. To switch transports, create a new connection and migrate agents.

### Configuration Through Conversation

MCP connections are configured conversationally — through Frank (org-level) or the project PM (project-level). There is no MCP configuration UI. The admin describes what they want to connect, and the agent handles the setup.

Example flow:
> **Human**: "Frank, I want to connect our GitHub MCP server so agents can manage issues and PRs."
> **Frank**: "Got it. I need the server endpoint URL and an auth token. The token will be stored securely and never appear in agent prompts. What scope — org-wide or a specific project?"
> **Human**: "Org-wide. Here's the endpoint: https://mcp.example.com/github. I'll provide the token."
> **Frank**: "Connected. I found 12 tools on the server — things like create_issue, list_prs, merge_branch. Want me to enable all of them, or restrict to a subset?"

### Connection Lifecycle

- `configuring`: being set up through conversation. Not yet usable.
- `active`: healthy and available for agent use.
- `degraded`: connection is experiencing issues (timeouts, errors) but not fully down. The circuit breaker may be half-open.
- `disabled`: manually disabled by the admin. Agents cannot use it.
- `failed`: connection cannot be established or has been down beyond the recovery threshold. Requires manual intervention.

Transitions:

```
configuring → active → degraded → active (recovered)
                     → disabled (manual)
                     → failed (unrecoverable)
degraded → active (recovered)
         → failed (exceeded threshold)
disabled → active (re-enabled)
failed → configuring (reconfigured)
```

### Connection Configuration Shape

Each connection stores:

- Display name and description (for agent and human reference).
- Transport type (`stdio` or `http_sse`).
- Transport config: endpoint URL (HTTP/SSE) or command + args (stdio).
- Authentication method and secret bindings (see Secret Management).
- Tool allowlist (which discovered tools are enabled — see Tool Discovery).
- Scope (org or project + scope ID).
- Health check configuration (interval, timeout, failure threshold).
- Timeout and retry policy overrides.
- Eager load flag (whether to preload all tool schemas into agent prompts — default false).

## Tool Discovery

### How Discovery Works

When a connection is established (or refreshed), the connector runtime calls the MCP server's `tools/list` endpoint to discover available tools. Each discovered tool includes:

- **Name**: the tool's identifier (e.g., `create_issue`, `run_query`).
- **Description**: what the tool does (provided by the MCP server).
- **Input schema**: JSON Schema describing the tool's expected parameters.

OtterCamp catalogs these tools in the `mcp_tool_catalog` table. The catalog is the system's view of what a given MCP server offers.

### Minimal Schema Normalization

OtterCamp trusts the MCP server's declared tool schema. There is no cross-server normalization, no attempt to unify overlapping tools from different servers into a common interface. Each tool is identified by `connection_id + tool_name`, and its schema is stored as-is from the server.

OtterCamp does validate that agent-provided inputs match the declared JSON Schema before sending the call to the server. This catches malformed inputs early, before they leave OtterCamp. But the schema itself is the server's responsibility — OtterCamp passes it through.

### Tool Enablement

Not all discovered tools are automatically available to agents. When tools are discovered:

1. All tools are cataloged (stored in `mcp_tool_catalog`).
2. The admin (through conversation) decides which tools to enable. By default, all tools are **disabled** until explicitly enabled. This is an MCP-specific defense-in-depth layer — catalog enablement is independent of the capability posture in doc 16.
3. Enabled tools become available for policy evaluation and agent use.

The admin can change enablement at any time: "Frank, disable the `delete_repo` tool on the GitHub connection."

### Catalog Refresh

The tool catalog is refreshed:

- **On connection establishment**: full discovery.
- **On manual refresh**: admin asks Frank to refresh a connection's tools ("Frank, re-sync the GitHub tools").
- **On periodic health check**: if the health check detects that the server's tool list has changed, the catalog is updated. New tools are cataloged but disabled by default. Removed tools are marked `removed` in the catalog.

When tools change on a refresh, the system emits a domain event (`mcp.catalog.changed`) so the admin can be notified of new or removed tools.

### How MCP Tools Appear to Agents

How MCP tools appear in an agent's prompt depends on the loading mode (see Context-Aware Tool Loading):

**Lazy discovery (default)**: the agent sees a one-line connection summary. To use a tool, the agent first calls `mcp.discover("<connection>")` to get schemas, then calls the tool.

**Flow node preloading**: tools declared in `flow_node.mcp_tools` appear in the agent's tool set alongside native OtterCamp tools. From the agent's perspective, calling an MCP tool is identical to calling any other tool — it requests the tool by name with parameters, and gets a result back.

**Eager-load connections**: all enabled tools from the connection appear in the agent's tool set.

In all cases, MCP tools are namespaced with the connection slug for disambiguation:

```
github.create_issue(title: string, body: string, labels: string[]) → Creates a GitHub issue
github.list_prs(state: "open" | "closed" | "all") → Lists pull requests
```

If two connections expose tools with the same name, the connection prefix disambiguates.

### Agent Access to MCP Tools

An agent can use an MCP tool if all of the following are true:

1. The connection is within scope (org or project) for the agent's current context.
2. The connection is `active` (not disabled, failed, or degraded beyond circuit breaker threshold).
3. The tool is enabled in the catalog.
4. The agent has the capability `mcp.connection.use:<connection_id>`.
5. The agent has the capability `mcp.tool.invoke:<connection_id>:<tool_name>` (or a wildcard grant for the connection).
6. The agent's tool policy (allow/deny list on the agent profile) does not deny the tool.
7. The control plane policy evaluation returns `allow` (not `deny`).

## Execution Flow

### Step-by-Step

1. **Agent requests tool call.** During its turn loop, the model emits a tool call for an MCP tool (e.g., `github.create_issue`).

2. **Tool registry lookup.** The tool execution layer identifies this as an MCP tool (tier 2) and routes it to the control plane.

3. **Policy evaluation.** The control plane evaluates:
   - Capability grants: `mcp.connection.use:<connection_id>` and `mcp.tool.invoke:<connection_id>:<tool_name>`.
   - Agent profile tool policy (allow/deny list).
   - Policy layers in priority order (instance safety → org → project → agent profile → request-specific).
   - Risk attributes: MCP calls are external mutations, always evaluated as write operations.

4. **Policy decision.**
   - `deny`: return "not permitted" as the tool result. The agent sees this and adapts. The turn loop continues.
   - `allow`: proceed to execution.

5. **Connector runtime execution.**
   - Resolve the connection from the registry. Check health status.
   - If circuit breaker is open: return a degraded-service error to the agent without attempting the call.
   - Inject secrets into the request (authentication headers, API keys). Secrets are resolved at runtime from the secret store — they are never in the agent's prompt or the tool call parameters.
   - Validate the agent's input parameters against the tool's cataloged JSON Schema. Reject malformed inputs before they leave OtterCamp.
   - Execute the MCP tool call via the configured transport (stdio subprocess or HTTP/SSE request).
   - Apply timeout policy (default 30 seconds per call). If the call exceeds the timeout, abort and return a timeout error.
   - Receive the response. Sanitize output (redact any secrets that may have leaked into the response).

6. **Record ToolExecution.** The control plane creates a `ToolExecution` record (see 16-agent-control-plane.md) capturing:
   - Tool name and connection ID.
   - Input parameters (with secrets redacted).
   - Output (with secrets redacted).
   - Duration and status.
   - Policy decision that authorized the call.
   - The run and run step this execution belongs to.

7. **Return result to agent.** The tool result is returned to the agent in the turn loop as a standard tool_result message. The agent sees the output and continues reasoning.

8. **Domain event emitted.** `mcp.tool.executed` event with connection ID, tool name, status, and duration. Consumed by observability and notification systems.

### In-Flight Call Handling

When a connection goes unhealthy while an MCP call is already in flight:

1. The in-flight call is allowed to complete (or timeout) — it is not preemptively cancelled. The call has already been dispatched to the MCP server; cancelling from OtterCamp's side doesn't cancel the server-side effect.
2. The circuit breaker counts the in-flight call's outcome (success or failure) toward its failure threshold.
3. If the call times out, the agent receives a timeout error. The timeout error includes context: "Connection [name] became degraded during this call."
4. Subsequent calls to this connection are evaluated against the circuit breaker's current state — they may be rejected immediately if the breaker has opened.
5. The `mcp_execution_log` entry records both the call status and the connection's health state at completion time.

## Security

### Capability Model Integration

MCP capabilities use the namespaced model from doc 16:

- `mcp.connection.use:<connection_id>` — agent can use tools from this connection.
- `mcp.tool.invoke:<connection_id>:<tool_name>` — agent can invoke this specific tool.
- `mcp.tool.invoke:<connection_id>:*` — wildcard grant for all enabled tools on a connection.

Capabilities follow the standard policy layers (instance → org → project → agent profile). MCP capabilities must be granted via templates or explicit policy rules — agents do not receive MCP access from the default capability templates (reader, worker, deployer grant only core capabilities).

### Per-Connection Tool Allowlists

Each connection has an enablement list in the catalog. Even if an agent has the capability to use a connection, they can only invoke tools that are enabled in that connection's catalog. This is a defense-in-depth layer:

- Capabilities control which agents can access which connections.
- Catalog enablement controls which tools are available at all.
- Agent profile tool policy controls which tools a specific agent can use.

All three must align for a call to proceed.

### Parameter Validation

Before any MCP call leaves OtterCamp:

1. The agent's input is validated against the tool's cataloged JSON Schema. Type mismatches, missing required fields, and unexpected properties are caught.
2. Input values are scanned for secrets or credentials that should not be sent to the MCP server (defense against prompt injection that tricks the agent into leaking secrets).
3. The validated input is logged (with secrets redacted) in the ToolExecution record.

### Secret Management

Secrets (API keys, auth tokens, OAuth credentials) are critical for MCP connections but must never appear in agent prompts, tool call parameters, or conversation history.

**Secret lifecycle:**

1. **Storage**: secrets are stored in the platform's encrypted secret store (see 08-deployment-and-self-hosting.md for the `secret` table schema, 13-security-observability-costs.md for the security model). Each secret is a named reference, never a raw value.
2. **Binding**: the `mcp_secret_binding` table maps a connection to the secrets it needs, by named reference. A connection might need `auth_token`, `api_key`, etc.
3. **Injection**: when the connector runtime executes an MCP call, it resolves secret references from the store and injects them into the outgoing request (as HTTP headers, query parameters, or environment variables for stdio). This happens at the transport layer, after the agent's input has been validated and logged.
4. **Redaction**: if a secret value appears in the MCP server's response (which it should not, but defense in depth), the response sanitizer strips it before the result reaches the agent or is logged.
5. **Rotation**: secrets can be rotated without reconfiguring the connection. Update the secret in the store; the binding still points to the same named reference.

**What the agent sees:**

The agent never sees secrets. It sees the tool's parameters (e.g., "repository: otter-camp, title: Fix auth bug") and the result. Authentication is handled transparently by the connector runtime.

### Network Isolation

For stdio transport, the subprocess runs in a sandboxed environment with:

- Restricted filesystem access (read-only to necessary paths).
- Constrained network access (configurable per connection — some MCP servers need outbound network, some don't).
- Resource limits (CPU, memory, time).

For HTTP/SSE transport, outbound requests are constrained to the configured endpoint. No server-side request forgery (SSRF) via MCP — the connector runtime validates that requests go only to the registered endpoint.

### Audit Trail

Every MCP interaction is audited at multiple levels:

- **ToolExecution record** (doc 16): full input/output, timing, policy decision.
- **Chat messages**: `tool_call` and `tool_result` messages in the session log.
- **Domain events**: `mcp.tool.executed`, `mcp.connection.health_changed`, `mcp.catalog.changed`.
- **AuditEvent**: security-relevant actions (connection created, secret rotated, tool enabled/disabled, capability granted).

## Reliability

### Health Checks

Each active connection has a periodic health check:

- **Interval**: configurable per connection, default 60 seconds.
- **Mechanism**: for HTTP/SSE, an HTTP ping to the server's health endpoint or a lightweight `tools/list` call. For stdio, check that the subprocess is alive and responsive.
- **Timeout**: configurable, default 5 seconds. If the health check does not respond within this window, it counts as a failure.
- **Failure threshold**: configurable, default 3 consecutive failures before the connection transitions to `degraded`.

Health check results are recorded and visible to the admin through conversation ("Frank, how's the GitHub connection?") and in the connection status.

### Circuit Breaker

The connector runtime implements a circuit breaker per connection:

- **Closed** (normal): requests pass through. Failures are counted.
- **Open** (tripped): requests are immediately rejected without contacting the server. The agent receives a "connection unavailable" error and can adapt. Opens after `failure_threshold` consecutive failures within the `failure_window`.
- **Half-open** (testing): after the `recovery_interval`, one test request is allowed through. If it succeeds, the breaker closes. If it fails, it opens again.

Default configuration:
- `failure_threshold`: 5 consecutive failures.
- `failure_window`: 60 seconds.
- `recovery_interval`: 30 seconds.

All values are configurable per connection.

### Timeout Structure

Three distinct timeout values govern different aspects of MCP reliability. They are independent and serve different purposes:

- **Per-call timeout** (`timeout_ms`, default 30,000ms): maximum time for a single MCP tool call to complete. If the MCP server does not respond within this window, the call is aborted and the agent receives a timeout error. Configurable per connection.
- **Health check timeout** (`health_config.check_timeout_sec`, default 5 seconds): maximum time for a health check probe. If the probe doesn't respond, it counts as a health check failure. Much shorter than the per-call timeout because health checks should be fast.
- **Circuit breaker recovery interval** (`circuit_breaker_config.recovery_interval_sec`, default 30 seconds): how long the circuit breaker stays open before allowing a half-open test request. This is a waiting period, not a timeout.

### Retry Policy

- Configurable per connection. Default is 1 retry with exponential backoff (initial delay 1 second, max delay 10 seconds).
- Only retried on transient failures (network errors, timeouts). Permanent failures (4xx HTTP status, validation errors) are not retried.
- **Idempotency**: the system attaches an idempotency key to retried requests if the MCP server supports it. For servers that don't, retries are only safe for read-only tools. Write tools are not retried by default — the agent is informed of the failure and can decide to retry explicitly.

### Graceful Degradation

When a connection is unhealthy:

1. The connection status transitions to `degraded` or `failed`.
2. The agent's prompt assembly includes a note that the connection is unavailable (in the policies/constraints layer).
3. Tool calls to that connection return a clear error: "Connection [name] is currently unavailable."
4. The agent can adapt — use alternative approaches, skip the step, or file a blocker.
5. A domain event `mcp.connection.health_changed` is emitted. If configured, this generates a notification to the admin.

When the connection recovers, the circuit breaker closes, the status returns to `active`, and agents can resume using it. No manual intervention needed for recovery.

## Resource Support

MCP resources are read-only data provided by the server. Examples: a database schema, a list of available repositories, a configuration file.

### How Resources Work

1. When a connection is established, the connector runtime discovers available resources via `resources/list`.
2. Resources are cataloged alongside tools (recorded in `mcp_tool_catalog` with `entry_type = 'resource'`).
3. Agents can read resources via a synthetic read-only tool: `<connection>.read_resource(uri: string)`.
4. Resource reads are **tier 1** (chat-layer) since they are read-only — no control plane overhead. However, they still require the `mcp.connection.use:<connection_id>` capability.
5. Resource contents can be injected into the agent's context during prompt assembly if the connection and specific resources are configured as context sources for a project or flow node.

### Resource Caching

Resources that don't change frequently (schemas, configurations) can be cached:

- Cache TTL is configurable per connection, default 5 minutes.
- Agents reading a cached resource get the cached version without a round-trip to the MCP server.
- Cache is invalidated on connection refresh or when the resource's `changed` notification is received (if the server supports it).

## Prompt Support

MCP prompts are reusable prompt templates provided by the server. They are structured instructions for specific tasks.

### Advisory Only

MCP prompts are **advisory** — they are informational content that can be used as skill-like instructions, but they do not execute directly. An MCP prompt does not trigger tool calls or autonomous behavior on its own.

### How Prompts Integrate

1. When a connection is established, the connector runtime discovers available prompts via `prompts/list`.
2. Prompts are cataloged in `mcp_tool_catalog` with `entry_type = 'prompt'`.
3. MCP prompts can be attached to flow nodes as supplementary instructions, similar to skills (see 10-skills-integration.md). The PM or admin configures this through conversation.
4. When activated, the prompt content is included in the agent's context (layer 4, skills instructions). It sits alongside OtterCamp skills — same priority, same budget allocation.
5. Prompts are read-only references. The agent uses them as guidance, not as executable instructions.

### Prompts vs Skills

| Aspect | OtterCamp Skills | MCP Prompts |
|--------|-----------------|-------------|
| Origin | Human-authored within OtterCamp | Provided by external MCP server |
| Storage | OtterCamp skill registry | MCP server, cached in catalog |
| Authority | Prescriptive — treated as directives | Advisory — treated as guidance |
| Versioning | OtterCamp-managed | Server-managed, refreshed on sync |
| Conflict resolution | Skills override prompts | Prompts yield to skills |

When both a skill and an MCP prompt cover the same topic, the skill takes precedence. MCP prompts fill gaps that skills don't cover.

## Schema

### mcp_connection

```sql
create table mcp_connection (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  project_id      uuid references project(id),    -- null = org-level
  name            text not null,
  slug            text not null,
  description     text,
  transport_type  text not null check (transport_type in ('stdio', 'http_sse')),
  transport_config jsonb not null,                 -- endpoint URL, command + args, headers, etc.
  auth_method     text check (auth_method in ('none', 'api_key', 'bearer_token', 'oauth2', 'custom')),
  status          text not null default 'configuring'
                  check (status in ('configuring', 'active', 'degraded', 'disabled', 'failed')),
  eager_load      boolean not null default false,  -- if true, all enabled tool schemas loaded into agent prompts
  health_config   jsonb not null default '{
    "check_interval_sec": 60,
    "check_timeout_sec": 5,
    "failure_threshold": 3
  }',
  timeout_ms      int not null default 30000,      -- per-call timeout
  retry_config    jsonb not null default '{
    "max_retries": 1,
    "initial_delay_ms": 1000,
    "max_delay_ms": 10000,
    "retry_on_write": false
  }',
  circuit_breaker_config jsonb not null default '{
    "failure_threshold": 5,
    "failure_window_sec": 60,
    "recovery_interval_sec": 30
  }',
  last_health_check_at  timestamptz,
  last_health_status    text check (last_health_status in ('ok', 'degraded', 'failed')),
  last_catalog_sync_at  timestamptz,
  created_by_type text not null check (created_by_type in ('human', 'agent', 'system')),
  created_by_id   uuid not null,
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}',

  unique (organization_id, slug)
);

create index on mcp_connection (organization_id, status);
create index on mcp_connection (project_id) where project_id is not null;
```

- `project_id` is nullable. Null means org-level. Non-null means project-scoped.
- `eager_load` defaults to false. When true, all enabled tool schemas from this connection are included in every agent prompt that has access to the connection. Only appropriate for connections with a small number of tools (<10).
- `transport_config` structure varies by transport type:
  - stdio: `{"command": "node", "args": ["server.js"], "env": {"KEY": "ref:secret_name"}, "cwd": "/path"}`
  - http_sse: `{"endpoint": "https://mcp.example.com/github", "headers": {"Authorization": "ref:secret_name"}}`
- Secret references in `transport_config` use the `ref:` prefix and are resolved at runtime from the secret store.
- `slug` is unique within the org and used as the tool name prefix (e.g., `github.create_issue`).

### mcp_tool_catalog

```sql
create table mcp_tool_catalog (
  id              uuid primary key default gen_random_uuid(),
  connection_id   uuid not null references mcp_connection(id) on delete cascade,
  entry_type      text not null check (entry_type in ('tool', 'resource', 'prompt')),
  name            text not null,
  description     text,
  input_schema    jsonb,                           -- JSON Schema for tool parameters (tools only)
  output_schema   jsonb,                           -- JSON Schema for output, if declared
  resource_uri    text,                            -- URI template for resources
  prompt_arguments jsonb,                          -- argument definitions for prompts
  is_enabled      boolean not null default false,  -- default-deny: must be explicitly enabled
  status          text not null default 'active'
                  check (status in ('active', 'removed')),
  discovered_at   timestamptz not null default now(),
  updated_at      timestamptz not null default now(),
  metadata        jsonb not null default '{}',

  unique (connection_id, entry_type, name)
);

create index on mcp_tool_catalog (connection_id, entry_type);
create index on mcp_tool_catalog (connection_id, is_enabled) where is_enabled = true;
```

- Catalog entries are the system's view of what the MCP server offers.
- `is_enabled = false` by default. Admin must explicitly enable tools through conversation.
- `status = 'removed'` when a catalog refresh no longer finds the entry on the server. The row is preserved for audit trail (past executions reference this catalog entry).
- `input_schema` is stored as-is from the MCP server. OtterCamp trusts the server's schema and validates agent inputs against it. Also used by `mcp.discover` and flow node preloading to inject schemas into agent prompts.
- The unique constraint on `(connection_id, entry_type, name)` prevents duplicate entries within a connection.

### mcp_execution_log

```sql
create table mcp_execution_log (
  id                uuid primary key default gen_random_uuid(),
  connection_id     uuid not null references mcp_connection(id),
  catalog_entry_id  uuid not null references mcp_tool_catalog(id),
  tool_execution_id uuid references tool_execution(id),   -- link to control plane ToolExecution (tier 2 only)
  run_id            uuid references run(id),                -- link to control plane Run (doc 16)
  agent_id          uuid not null references agent(id),
  entry_type        text not null check (entry_type in ('tool', 'resource', 'prompt')),
  tool_name         text not null,
  input_params      jsonb,                                 -- secrets redacted
  output            jsonb,                                 -- secrets redacted
  status            text not null check (status in ('success', 'error', 'timeout', 'circuit_open', 'validation_error')),
  error_message     text,
  duration_ms       int,
  policy_decision   text check (policy_decision in ('allow', 'deny')),
                    -- nullable: null for resource reads (tier 1, no policy evaluation)
  retries           int not null default 0,
  connection_health_at_completion text,                    -- connection health state when call completed
  created_at        timestamptz not null default now(),
  metadata          jsonb not null default '{}'
);

create index on mcp_execution_log (connection_id, created_at);
create index on mcp_execution_log (agent_id, created_at);
create index on mcp_execution_log (tool_execution_id) where tool_execution_id is not null;
create index on mcp_execution_log (run_id) where run_id is not null;
create index on mcp_execution_log (status);
```

- Every MCP interaction (tool calls and resource reads) is logged here.
- `tool_execution_id` links to the control plane's `ToolExecution` record for tool calls (tier 2). Null for resource reads (tier 1) which don't go through the control plane.
- `input_params` and `output` are always secret-redacted. The raw values with secrets are never persisted.
- `policy_decision` records what the policy evaluation returned. **Nullable** — null for resource reads (tier 1) which bypass the control plane. For denied calls, the decision is recorded even though the call was not executed.
- `status` captures the outcome: success, error (server returned an error), timeout, circuit_open (call rejected by circuit breaker), validation_error (input didn't match schema).
- `connection_health_at_completion` captures the connection's health state when the call finished — useful for diagnosing failures that coincide with connection degradation.

### mcp_secret_binding

```sql
create table mcp_secret_binding (
  id              uuid primary key default gen_random_uuid(),
  connection_id   uuid not null references mcp_connection(id) on delete cascade,
  binding_key     text not null,                   -- logical name: auth_token, api_key, etc.
  secret_ref      text not null,                   -- reference to the secret store entry
  inject_as       text not null check (inject_as in ('header', 'query_param', 'env_var', 'body_field')),
  inject_target   text not null,                   -- header name, query param name, env var name, etc.
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now(),

  unique (connection_id, binding_key)
);

create index on mcp_secret_binding (connection_id);
```

- Maps logical secret names to their injection points.
- `binding_key` is a human-readable name for the secret's role in this connection (e.g., `github_token`, `database_password`).
- `secret_ref` points to the encrypted secret in the platform's secret store. The actual value is never in this table.
- `inject_as` + `inject_target` control how the secret is placed into the outgoing request:
  - `header` + `Authorization`: injected as an HTTP header.
  - `env_var` + `API_KEY`: injected as an environment variable for stdio processes.
  - `query_param` + `token`: appended to the request URL.
  - `body_field` + `credentials.api_key`: injected into the request body at the specified path.

### Cross-Entity Relationships

```
mcp_connection
  ├── mcp_tool_catalog (1:many — tools, resources, prompts from this server)
  ├── mcp_secret_binding (1:many — secrets needed for this connection)
  └── mcp_execution_log (1:many — all calls made through this connection)

mcp_execution_log
  ├── → tool_execution (control plane — tier 2 tool calls)
  └── → agent (who made the call)

mcp_connection
  ├── → organization (always)
  └── → project (optional, for project-scoped connections)

flow_node.mcp_tools → mcp_tool_catalog
  (cross-spec: flow node declares required MCP tools by connection_slug.tool_name;
   prompt assembler resolves these against the catalog at session start)
```

### What's NOT in the MCP Schema

- **Agent capability grants**: live in the control plane's policy tables (doc 16). The MCP schema records connections and catalogs; who can use them is a policy concern.
- **Secret values**: live in the encrypted secret store (doc 08 for schema, doc 13 for security model). The MCP schema holds references only.
- **Detailed run/step records**: live in the control plane's run/tool_execution tables (doc 16). The MCP execution log is the MCP-specific view with connection and tool metadata.
- **Flow node MCP tool declarations**: live on `flow_node.mcp_tools` in doc 03. The MCP catalog provides the schemas; the flow node declares which ones to preload.

## Domain Events

MCP operations emit domain events for observability, notifications, and downstream automation:

| Event | Payload | Trigger |
|-------|---------|---------|
| `mcp.connection.created` | connection_id, name, scope | New connection configured |
| `mcp.connection.status_changed` | connection_id, from_status, to_status | Health or lifecycle state change |
| `mcp.connection.health_changed` | connection_id, health_status, consecutive_failures | Health check result changed |
| `mcp.catalog.changed` | connection_id, added_tools[], removed_tools[], added_resources[] | Catalog refresh found changes |
| `mcp.tool.executed` | connection_id, tool_name, agent_id, status, duration_ms | Tool call completed |
| `mcp.tool.denied` | connection_id, tool_name, agent_id, reason | Policy denied a tool call |
| `mcp.tool.preload_failed` | connection_id, tool_name, flow_node_id, reason | Flow node declared an MCP tool that couldn't be preloaded |

## Observability

### Metrics

Per connection:
- Call count, error rate, latency percentiles (p50, p95, p99).
- Circuit breaker state transitions.
- Health check success rate.

Per tool:
- Call count, error rate, latency percentiles.
- Validation error rate.

Per agent:
- MCP call count by connection and tool.
- MCP error rate.

### Dashboards

The admin can ask about MCP health conversationally ("Frank, how are our MCP connections doing?"). Frank has access to:
- Connection status overview (active, degraded, failed counts).
- Recent execution errors.
- Latency trends.
- Most-used tools and connections.
- Context loading stats: how many tools are preloaded vs discovered on-demand.

## API Surface

MCP management endpoints (see 12-api-events-and-realtime.md):

- `GET /mcp/connections` — list connections (filtered by scope).
- `GET /mcp/connections/{id}` — connection detail with health status.
- `POST /mcp/connections` — create connection (typically called by agents during conversational setup).
- `PATCH /mcp/connections/{id}` — update connection configuration (includes `eager_load` toggle).
- `DELETE /mcp/connections/{id}` — remove connection (cascades to catalog and secret bindings).
- `POST /mcp/connections/{id}/refresh` — trigger catalog re-sync.
- `POST /mcp/connections/{id}/test` — test connection health.
- `GET /mcp/connections/{id}/catalog` — list discovered tools/resources/prompts.
- `PATCH /mcp/connections/{id}/catalog/{entry_id}` — enable/disable a catalog entry.
- `GET /mcp/connections/{id}/executions` — execution log for a connection.

All endpoints require appropriate org/project authorization. Connection creation and modification require admin-level access.

## Non-Goals for Initial Release

- **OtterCamp as an MCP server.** OtterCamp is an MCP client only. Exposing OtterCamp's capabilities via MCP for external consumers is a future consideration.
- **Dynamic MCP server discovery.** Connections are manually configured through conversation. No auto-discovery of MCP servers on the network.
- **Cross-org connection sharing.** Each org has its own connections. No marketplace or shared connection registry.
- **MCP sampling support.** The MCP sampling capability (server requesting the client to make an LLM call) is not supported at launch. This introduces complex trust and cost concerns that need separate design.
- **Custom transport adapters.** Only stdio and HTTP/SSE. Additional transports (WebSocket, gRPC) are deferred.
- **Automatic tool schema compression.** Tool schemas are loaded as-is from the MCP server. No token-level optimization (e.g., schema summarization, parameter pruning) at launch. Context-aware loading handles the bloat problem at the loading-decision level instead.

## Resolved Decisions

- **Support both stdio and HTTP/SSE transports at launch.** Both are common in the MCP ecosystem. Stdio is essential for local/self-hosted setups. HTTP/SSE is essential for remote/cloud MCP servers. Supporting only one would exclude a significant portion of available MCP servers.
- **Minimal schema normalization.** Trust the server's tool schema, pass through. OtterCamp validates inputs match the declared schema but does not attempt to normalize, merge, or unify tool schemas across servers. Each tool is identified by `connection_id + tool_name`.
- **MCP prompts are advisory only.** They can be used as skill-like instructions (loaded into the agent's context when relevant) but do not execute directly. Skills take precedence over MCP prompts in conflict resolution.
- **MCP connections are configured conversationally.** Frank (org-level) or the PM (project-level) handles setup through chat. No dedicated MCP configuration UI. Consistent with the product principle that everything is configured through agents.
- **MCP tools appear alongside native tools in the agent's tool set.** Same policy pipeline, same prompt assembly, same execution flow. From the agent's perspective, an MCP tool is just another tool.
- **Each MCP tool call creates a ToolExecution record.** Full traceability through the control plane. MCP-specific metadata (connection, catalog entry, transport details) is captured in the `mcp_execution_log` for MCP-specific queries.
- **Default-deny for discovered tools.** All tools are cataloged but disabled until the admin explicitly enables them. Consistent with the control plane's default-deny capability posture.
- **Secrets are injected at runtime, never in agent prompts.** The connector runtime resolves secret references from the encrypted store at execution time. Agents never see credentials. Secrets are redacted from all logged inputs and outputs.
- **Resource reads are tier 1 (chat-layer).** They are read-only and don't need full control plane overhead. They still require the `mcp.connection.use` capability for access control. `policy_decision` is nullable in the execution log for these reads.
- **Connection health is the connector runtime's responsibility.** Health checks, circuit breakers, and status transitions are managed by the runtime. The admin is informed through conversation and events, not through a polling dashboard.
- **Circuit breaker prevents cascading failures.** When a connection is unhealthy, calls are rejected at the circuit breaker before reaching the server. Agents receive a clear error and can adapt.
- **Catalog refresh discovers changes, doesn't auto-enable.** When a server adds new tools, they appear in the catalog as disabled. The admin is notified and decides what to enable. Removed tools are marked `removed`, not deleted, for audit trail integrity.
- **One unique slug per connection per org.** The slug serves as the tool name prefix for disambiguation. Two connections in the same org cannot have the same slug.
- **Policy is binary: allow or deny.** Consistent with doc 16 (agent-control-plane.md). There is no runtime approval gating. MCP connections that should be accessible are allowed; those that should not are denied. Permissions are configured in advance.
- **Context-aware tool loading: lazy by default, eager when declared.** Agents receive lightweight connection summaries (~20 tokens each) by default. Full tool schemas are loaded only when: (a) a flow node declares specific MCP tools in `flow_node.mcp_tools`, (b) the connection has `eager_load = true`, or (c) the agent calls `mcp.discover` to get schemas on demand. This prevents MCP from bloating context windows while ensuring workers have the tools they need without wasted round-trips.
- **Flow node MCP tool declaration uses `flow_node.mcp_tools` JSONB.** Analogous to `flow_node.skills` for OtterCamp skills. Format is an array of `connection_slug.tool_name` strings. PM configures through conversation during flow design.
- **`mcp.discover` is a native utility tool, not an MCP tool.** It reads from the local catalog, not from the MCP server. No policy evaluation, no network call. Always available to any agent with MCP connection access.
- **In-flight calls are not preemptively cancelled.** When a connection degrades during an in-flight call, the call is allowed to complete (or timeout). Cancelling from OtterCamp doesn't cancel server-side effects. The circuit breaker counts the outcome.
- **Three distinct timeout values with different purposes.** Per-call timeout (default 30s), health check timeout (default 5s), and circuit breaker recovery interval (default 30s) are independent configurations that govern different aspects of reliability.

## Open Questions

- **MCP sampling**: should OtterCamp support the MCP sampling capability (server requests the client to make an LLM call on its behalf)? This has trust, cost, and security implications that need careful design. Deferred for now.
- **Connection templates**: should we provide pre-built connection templates for popular MCP servers (GitHub, Slack, Postgres, etc.) to simplify setup? Or is conversational setup sufficient?
- **Multi-server aggregation**: if multiple connections expose similar tools (e.g., two database connections both expose `run_query`), should the system provide any disambiguation beyond the connection prefix, such as routing hints or context-aware selection?

## Cross-Spec Dependencies

Changes in this doc that require updates in other specs:

- **Doc 03 (Projects and Task Flow)**: add `mcp_tools jsonb` column to the `flow_node` table. Format: array of `connection_slug.tool_name` strings. Analogous to existing `skills` column.
- **Doc 05 (Agents, Staff, and Temps)**: update layer 7 (tool descriptions) documentation in the prompt assembly section to describe the three MCP tool loading modes (lazy, flow node preload, eager-load connection). Update layer 4 to mention MCP prompts alongside skills.
- **Doc 10 (Skills Integration)**: mention that MCP prompts integrate at layer 4 alongside skills, with skills taking precedence on conflict.
