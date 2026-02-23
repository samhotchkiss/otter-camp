# 049: Tool Resolution Pipeline

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 20 §ToolResolution, doc 20 §SessionToolSet, doc 20 §CapabilityGate, doc 20 §TierAssignment |
| Spec status | finished |
| Depends on | 043, 013, 017, 020, 033, 055 |
| Blocks | 048, 056, 057, 072 |

## Scope

Build the 4-stage tool resolution pipeline, the `session_tool_set` table and its repository,
session-lifetime caching of resolved tool sets, and mid-session capability revocation handling.

### Must build

**`session_tool_set` table** (doc 20 — L4, references `chat_session` and `agent`):
- Migration: `0059_session_tool_set.sql`
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `agent_id uuid not null references agent(id) on delete cascade`
- `resolved_at timestamptz not null default now()`
- `tool_set jsonb not null` — the full resolved tool list; array of tool descriptor objects:
  ```json
  [
    {
      "name": "memory.query",
      "tier": "tier1",
      "domain": "memory",
      "capability": "memory.read",
      "source": "native",
      "mcp_connection_id": null,
      "is_enabled": true,
      "priority": 1
    }
  ]
  ```
- `invalidated_at timestamptz` — null unless the cache was invalidated mid-session (e.g. capability revocation)
- Unique index: `(session_id, agent_id) WHERE invalidated_at IS NULL` — one active tool set per agent per session

**`SessionToolSetRepo`:**
- `Create(ctx, sessionID, agentID, toolSet) (*SessionToolSet, error)`
- `GetActive(ctx, sessionID, agentID) (*SessionToolSet, error)` — returns not-found if invalidated
- `Invalidate(ctx, sessionID, agentID) error` — sets `invalidated_at=now()`
- `ReplaceToolSet(ctx, sessionID, agentID, newToolSet) (*SessionToolSet, error)` — invalidates old, creates new in a transaction

**4-stage tool resolution pipeline** (`ToolResolver`):

Stage 1 — Universe construction:
- Enumerate all native tools from `tool_definition` table (startup-populated, task 003 + 056/057).
- Enumerate all MCP tools from `mcp_tool_catalog` where `is_enabled=true` AND the MCP connection is accessible in the session's scope:
  - If connection is `project_id=null` (org-scoped): available in all sessions.
  - If connection has `project_id` set: only available in sessions scoped to that project or a task within that project.
- Result: a flat list of all candidate tools the system knows about in this session's scope.

Stage 2 — Agent profile filter:
- Load the session's agent's `tool_allow_list` and `tool_deny_list` from the `agent` table (glob patterns, e.g. `"mcp.*"`, `"git.*"`).
- Apply deny list first: remove any tool whose `name` matches any deny glob.
- Apply allow list: if `tool_allow_list` is non-empty, keep only tools whose `name` matches at least one allow glob. If `tool_allow_list` is empty, all non-denied tools pass.
- Glob matching rules: `*` matches any single path segment (no dots); `**` matches any sequence of segments. E.g. `mcp.*` matches `mcp.github.create_issue` but not `memory.query`. `file.*` matches `file.read`, `file.write`, etc.

Stage 3 — Flow node soft deprioritization:
- If the session is scoped to a `project_task` and there is an active `flow_node_execution` for the current flow node:
  - Load `flow_node.tool_domains` (jsonb array of domain strings, e.g. `["memory","file","git"]`).
  - Tools whose `domain` is NOT in `tool_domains` are moved to the end of the tool list (deprioritized) but NOT removed.
  - This is a soft hint to the model (via ordering in the tool descriptions), not a hard exclusion.
  - If `flow_node.tool_domains` is null or empty, Stage 3 is a no-op.
- Also load `flow_node.mcp_tools` (jsonb array of `{connection_id, tool_name}` objects):
  - MCP tools listed here are moved to the FRONT of the tool list (prioritized).
  - MCP tools in the session scope but NOT listed in `flow_node.mcp_tools` remain in their Stage 2 position.

Stage 4 — Capability gate:
- For each tool in the resolved list, check if it requires a capability (from `tool_definition.required_capability`).
- Evaluate the capability against the capability policy engine (task 033):
  - Tier-1 tools: capability gate is checked but does NOT exclude the tool. Tier-1 tools bypass
    the hard capability gate (they are always included regardless of policy); capability check is
    informational only (logged but not enforced for tier-1).
  - Tier-2 tools: if the required capability is denied by policy for this agent/session, the
    tool is EXCLUDED from the resolved tool set entirely.
- Result: the final resolved tool set, ordered by: prioritized MCP tools → standard tools → deprioritized tools.

**`ToolResolver.GetSessionToolSet(ctx, sessionID, agentID) ([]ToolDescriptor, error)`:**
- Checks `session_tool_set` cache first: if an active (non-invalidated) row exists for this session+agent, return the cached tool set without re-running the pipeline.
- Cache miss: run all 4 stages and persist the result via `SessionToolSetRepo.Create`.
- The cache is session-lifetime: it is invalidated only on explicit revocation (see below).

**Mid-session capability revocation:**
- Capability revocation events arrive via domain event `capability.policy.updated` (published by the capability policy service, task 033).
- `ToolResolver.HandlePolicyUpdate(ctx, orgID) error`:
  - Invalidates all active `session_tool_set` rows for the org (calls `Invalidate` for each).
  - The next `GetSessionToolSet` call for any affected session will re-run the pipeline.
  - Does NOT proactively re-resolve; cache is lazily rebuilt on next access.
- Mid-session revocation at execution time: even with a cached tool set, the turn engine (task 048) must re-check capability policy at actual tool execution time (not just at session start). This is done in task 055 (tool_execution dispatch), not here.

**Communication tool `draft_action_review` behavior** (doc 20):
- Communication tools (e.g. `email.compose`, `slack.post`) are always included in the tool
  set if the agent has access (they pass the policy check). However, invoking them creates a
  `draft_action_review` inbox item (task 027) that requires human approval before the action
  is sent. This is a designed behavior, not a tool exclusion. The tool resolver includes these
  tools normally; the behavior is enforced by the tool implementation (tasks 056/057).

### Must NOT build

- Native tool implementations (tasks 056, 057)
- Control plane tool_execution dispatch (task 055)
- Capability policy engine (task 033, already built)
- `flow_node` DDL (task 017, already built)
- MCP tool catalog (task 020, already built)
- Turn engine (task 048)

## Acceptance Criteria

- [ ] Stage 1 includes org-scoped MCP tools and excludes project-scoped MCP tools from sessions not in that project
- [ ] Stage 2 deny list: tool `git.push` with deny glob `git.*` → excluded from resolved set
- [ ] Stage 2 allow list: non-empty allow list `["memory.*"]` → only memory-domain tools retained; all others excluded
- [ ] Stage 3 deprioritization: flow node with `tool_domains=["memory"]` → `file.*` tools appear after `memory.*` tools in the resolved list; `file.*` tools are NOT removed
- [ ] Stage 4 tier-1 capability gate: tier-1 tool with a denied capability is INCLUDED in the resolved set (tier-1 bypasses hard exclusion)
- [ ] Stage 4 tier-2 capability gate: tier-2 tool with a denied capability is EXCLUDED from the resolved set
- [ ] Cache hit: calling `GetSessionToolSet` twice for the same session returns the same tool set from cache (no DB pipeline re-run; verify with a SQL query count or mock spy)
- [ ] Cache invalidation: calling `Invalidate` then `GetSessionToolSet` re-runs the pipeline and creates a new `session_tool_set` row
- [ ] Unit tests achieve 95% code coverage on the 4-stage pipeline

## Tests Required

**Unit tests (95% coverage target):**
- Stage 1: mock `tool_definition` with 5 native + 3 MCP tools; session scoped to project A; 2 MCP connections (1 org-scoped, 1 project-B-scoped) → universe has 5 + 1 = 6 tools (project-B MCP excluded)
- Stage 2 deny glob: tool names `["memory.query","mcp.github.create_issue","git.commit"]`; deny list `["mcp.*"]` → `mcp.github.create_issue` removed; 2 tools remain
- Stage 2 allow glob: allow list `["memory.*","git.*"]`; deny list empty → only memory and git tools retained
- Stage 2 glob matching correctness: `file.*` matches `file.read`, `file.write`; does NOT match `memory.query`; `file.**` matches `file.read` and hypothetically `file.subfolder.tool`
- Stage 3 no-op: session with `flow_node.tool_domains=null` → tool order unchanged from Stage 2
- Stage 3 deprioritization: 3 tools (domains: memory, file, git); `tool_domains=["memory"]` → memory tool first, file and git tools at end; all 3 present
- Stage 4 tier-1 bypass: tier-1 tool with capability denied → still included; stage returns it in list
- Stage 4 tier-2 exclusion: tier-2 tool with capability denied → not in output list
- Cache hit: `GetSessionToolSet` called twice; verify `SessionToolSetRepo.Create` called only once

**Integration tests:**
- Full pipeline end-to-end: seed session + agent (with deny_list=["git.*"]) + tool_definitions + mcp_catalog; call `GetSessionToolSet`; verify `session_tool_set` row created with expected tools (git tools excluded); call again → same row returned (cache hit)
- Policy revocation: seed session + active `session_tool_set`; fire `capability.policy.updated` event; verify `session_tool_set.invalidated_at` set; call `GetSessionToolSet` again → new row created (pipeline re-run)
- MCP scope filter: seed org-scoped MCP connection (project_id=null) and project-scoped MCP connection (project_id=B); create session scoped to project A; call `GetSessionToolSet`; verify org-scoped MCP tools present, project-B MCP tools absent

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- Glob matching: implement using a simple pattern matcher (do not use `filepath.Match` — it has platform-specific behavior). A pattern segment with `*` matches any sequence of characters except `.`. A segment with `**` is a wildcard for the entire remaining path. Write a `matchToolGlob(pattern, name string) bool` function with a test table covering all documented cases.
- The `session_tool_set.tool_set` jsonb stores the full descriptor for each tool including its `source` ('native' or 'mcp'), `mcp_connection_id` (null for native), `tier`, `domain`, and `capability`. The turn engine (task 048) reads this to know which tools are available without re-querying the pipeline on each model call.
- Stage 3 requires knowing the current flow node. This is determined by: `chat_session.scope_id` (the project_task_id if scope_type='project_task') → look up the `project_task.current_flow_node_id` → load `flow_node`. If the session is not task-scoped, Stage 3 is skipped.
- The `ToolResolver` should expose a `ToolDescriptor` struct that the prompt assembler (task 050) uses to build the tool descriptions section of the prompt. The descriptor must include `name`, `description`, `input_schema` (JSON Schema for the tool's parameters), and `tier`.

> ✅ ISSUES #16 + #25 (RESOLVED): `flow_node.tool_domains jsonb` is confirmed present in the final schema (task 017). Implement Stage 3 fully — no graceful degradation needed.
