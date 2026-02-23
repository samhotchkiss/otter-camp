# 055: Tool Execution Service and Dispatch Pipeline

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 16 §ToolExecutionBroker, doc 16 §BrokerDispatch, doc 09 §MCPExecution, doc 20 §TierDispatch |
| Spec status | finished |
| Depends on | 052, 053, 033, 020, 021, 024 |
| Blocks | 056, 057, 058, 059, 077, 087 |

## Scope

Build the tool execution dispatch pipeline: the broker that receives a tool-call request,
runs the capability check and agent allow/deny filter, evaluates policy, dispatches to the
correct executor (native, MCP, CLI, browser), and writes the `tool_execution` audit record.
Also build the `mcp_execution_log` DDL and MCP tool invocation via the control plane path.

### Must build

**Migration:**
- `0062_mcp_execution_log.sql`

**`mcp_execution_log` table** (doc 09):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `mcp_connection_id uuid not null references mcp_connection(id) on delete cascade`
- `mcp_tool_catalog_id uuid references mcp_tool_catalog(id) on delete set null`
- `tool_execution_id uuid references tool_execution(id) on delete set null`
- `run_id uuid references run(id) on delete set null`
- `agent_id uuid references agent(id) on delete set null`
- `method text not null` — e.g. `tools/call`, `resources/read`
- `tool_name text` — nullable (null for resource reads)
- `resource_uri text` — nullable (null for tool calls)
- `request_payload jsonb not null default '{}'`
- `response_payload jsonb`
- `status text not null check (status in ('pending','success','error','timeout','circuit_open'))`
- `error_message text`
- `latency_ms integer`
- `created_at timestamptz not null default now()`
- Index: `(mcp_connection_id, created_at)`, `(tool_execution_id)`, `(run_id)`

**`MCPExecutionLogRepository`** — Create, Get, ListByConnection, ListByRun

**`ToolBroker`** (in `internal/controlplane/broker.go`):

**`ToolBroker.Dispatch(ctx, DispatchInput) (ToolExecution, error)`:**
- Input: `{run_id, run_step_id, run_attempt_id, agent_id, tool_name, tool_tier, input}`
- Returns the completed `tool_execution` record (or an error).
- Implements the 5-step dispatch pipeline:

**Step 1 — Capability check (tier 2 only):**
- If `tool_tier == 'tier2'`: look up the capability string for `tool_name` in the tool definition registry (`tool_definition` table, task 003).
- Call `PolicyService.EvaluateCapability(ctx, org_id, project_id, agent_id, capability)`.
- If denied: create `tool_execution` row with `policy_decision='denied', status='policy_denied'`; return `ErrCapabilityDenied`. Do NOT proceed to step 2.
- If `tool_tier == 'tier1'`: set `policy_decision='not_checked'`; skip to step 2.

**Step 2 — Agent allow/deny filter:**
- Load the agent's `tool_allow_list` and `tool_deny_list` from `agent` table (task 013).
- Deny list is checked first (glob matching); if matched → return `ErrAgentDenyList`.
- Allow list is checked next (glob matching); empty allow list means "allow all".
- If denied: create `tool_execution` row with `policy_decision='denied', status='policy_denied'`; return `ErrAgentDenyList`.

**Step 3 — Create `tool_execution` row:**
- Insert `tool_execution` with `status='in_progress', policy_decision='allowed'`, `input` (secrets already redacted from input before insert — see Secret Redaction below).

**Step 4 — Dispatch to executor:**
Based on `tool_domain` (resolved from tool definition):
- `'native'`: call `NativeToolExecutor.Execute(ctx, tool_name, input)` (tasks 056, 057)
- `'cli'`: call `CLIExecutor.Execute(ctx, input)` (task 058)
- `'browser'`: call `BrowserExecutor.Execute(ctx, input)` (task 059)
- `'mcp'`: call `MCPExecutor.Execute(ctx, tool_name, input, mcp_connection_id)` (see below)

**Step 5 — Update `tool_execution` row:**
- On success: update `status='completed', output=result, completed_at=now(), duration_ms=...`
- On error: update `status='failed', error_message=..., completed_at=now()`
- On timeout: update `status='timed_out'`

**Secret redaction before storage:**
- Before inserting the `tool_execution.input` jsonb, scan all string values for known secret patterns.
- Patterns: `ref:<slug>` references (replace with `[SECRET:<slug>]`), bearer tokens (regex: `(?i)bearer\s+[a-zA-Z0-9\-._~+/]+`), API key patterns.
- Use the same redaction helper as task 063 (when available); implement inline for now.

**`MCPExecutor`** (in `internal/mcp/executor.go`):
`MCPExecutor.Execute(ctx, toolName, input, mcpConnectionID) (map[string]any, error)`:
- Loads `mcp_connection` row and constructs the MCP client (from task 020).
- Checks circuit breaker state (from task 021); if `open` → return `ErrCircuitOpen`.
- Calls `mcp_connection.CallTool(toolName, input)` with per-call timeout (from task 020 config).
- Creates `mcp_execution_log` row (method=`tools/call`) before the call; updates on completion.
- On success: return tool result payload.
- On transient error (network timeout, 5xx): report to circuit breaker; return error for retry by broker.
- On permanent error (4xx, schema error): mark circuit as failed; return `ErrPermanent`.

**MCP resource reads (tier 1 path):**
`MCPExecutor.ReadResource(ctx, resourceURI, mcpConnectionID) (map[string]any, error)`:
- Scope check: verify the agent has access to this connection (see ISSUE #15 — implement as `agent_project_assignment` check if connection is project-scoped; allow all agents if connection is org-scoped).
- Does NOT go through the capability gate (tier 1).
- Creates `mcp_execution_log` row (method=`resources/read`).
- Returns resource content.

**Retry integration:**
- `ToolBroker.Dispatch` does NOT retry internally. If a transient error occurs, it returns the error to the caller (task 048 turn engine or task 053 service).
- The caller is responsible for calling `RunService.CreateRetryAttempt` and re-dispatching.
- This keeps retry logic in the control plane service (task 053), not scattered across executors.

**`ToolExecutionService`** — thin facade over `ToolBroker` for use by the turn engine (task 048):
- `ToolExecutionService.ExecuteTier1Tool(ctx, sessionID, agentID, toolName, input) (output, error)` — wraps the broker for tier 1 calls; creates a lightweight `tool_execution` record without a full run context.
- `ToolExecutionService.ExecuteTier2Tool(ctx, runID, runStepID, runAttemptID, agentID, toolName, input) (output, error)` — wraps the broker for tier 2 calls with full run context.

### Must NOT build

- Native tool implementations (tasks 056, 057)
- CLI executor (task 058)
- Browser executor (task 059)
- MCP connection and health service (tasks 020, 021)
- Policy evaluation engine (task 033)
- Control plane schema (task 052)

## Acceptance Criteria

- [ ] `ToolBroker.Dispatch` for a tier2 tool with a denied capability creates a `tool_execution` row with `policy_decision='denied', status='policy_denied'` and returns `ErrCapabilityDenied`
- [ ] `ToolBroker.Dispatch` for a tier1 tool skips the capability gate and sets `policy_decision='not_checked'`
- [ ] Agent deny list glob match: `tool_deny_list=['file.*']` blocks `file.read` but not `git.status`
- [ ] Agent allow list: empty allow list allows all tools; `tool_allow_list=['git.*']` blocks `file.read`
- [ ] Input secrets are redacted before `tool_execution.input` is stored: `ref:my_key` → `[SECRET:my_key]`
- [ ] `MCPExecutor.Execute` with circuit breaker in `open` state returns `ErrCircuitOpen` immediately (no MCP call made)
- [ ] `MCPExecutor.Execute` creates `mcp_execution_log` row on every call (success or failure)
- [ ] `MCPExecutor.ReadResource` for a project-scoped connection verifies `agent_project_assignment` before proceeding

## Tests Required

**Unit tests:**
- Broker step 1 (capability denied): mock `PolicyService.EvaluateCapability` returns deny → `tool_execution.policy_decision='denied'` inserted, `ErrCapabilityDenied` returned
- Broker step 2 (agent deny list): agent with `tool_deny_list=['browser.*']`; dispatch `browser.navigate` → `ErrAgentDenyList`
- Broker step 2 (allow list): agent with `tool_allow_list=['git.*']`; dispatch `file.read` → `ErrAgentDenyList`
- Secret redaction: input `{"api_key": "ref:prod_key", "token": "Bearer abc123xyz"}` → stored input has `[SECRET:prod_key]` and `[REDACTED]`
- MCP circuit open: mock circuit breaker state=open → `Execute` returns `ErrCircuitOpen` without calling MCP
- MCP resource scope check (project-scoped): agent not in project → `ErrAccessDenied` (ISSUE #15 best-judgment: check `agent_project_assignment`)

**Integration tests:**
- Tier 2 tool full pipeline: create run + step + attempt; dispatch tier 2 native tool (mock executor); verify `tool_execution` row created with `policy_decision='allowed', status='completed'`
- MCP execution: dispatch MCP tool; verify `mcp_execution_log` row created with correct `tool_execution_id`
- Policy denied pipeline: dispatch tier 2 tool with policy deny; verify `tool_execution.policy_decision='denied'` and `status='policy_denied'` in DB

**E2E tests:**
- None — covered by dedicated E2E task 087

## Implementer Notes

**Glob matching for agent allow/deny lists:**
Use `path.Match` from the Go standard library. The pattern `file.*` matches `file.read` and `file.write` but not `file.git.status`. Use `**` patterns (implement a simple two-pass split on `**`) only if the spec uses them; otherwise `*` is sufficient.

**`tool_execution` for tier 1 calls without a run:**
For tier 1 calls that occur outside a control plane run (e.g., a memory.query called during prompt assembly), `run_id`, `run_step_id`, and `run_attempt_id` may all be null. The `tool_execution` row is still created for audit purposes. The `ToolExecutionService.ExecuteTier1Tool` path accepts `runID=nil`.

**MCP timeout hierarchy (from task 020):**
- Per-call timeout: from `mcp_connection.config.call_timeout_ms`
- Health check timeout: separate
- Recovery timeout: separate
Use the per-call timeout for `MCPExecutor.Execute`. If the context deadline is exceeded, return `ErrTimeout` and update `tool_execution.status='timed_out'`.

> ⚠️ ISSUE #15 (AMBIGUOUS): MCP resource read scope check implementation is not specified. Implement as: if `mcp_connection.project_id IS NOT NULL`, check `agent_project_assignment` for the agent+project pair. If `mcp_connection.project_id IS NULL` (org-scoped), allow any org agent. Document this decision for Sam's review.
