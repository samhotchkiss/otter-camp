# Issue #124 — Restructure Otter Camp as MCP

> **Priority:** P1
> **Status:** Ready for implementation
> **Affects:** Full stack — new `cmd/mcp/` server + SDK integration

## Summary

Expose all Otter Camp capabilities as an MCP (Model Context Protocol) server. Instead of agents interacting with Otter Camp through a bespoke CLI and REST API, they connect via the standard MCP protocol — making Otter Camp a universal context provider for any MCP-compatible AI client (OpenClaw, Claude Desktop, Claude Code, Cursor, VS Code, etc.).

This is a fundamental architectural shift. The REST API remains for the web UI, but agent-facing operations move to MCP as the primary interface.

## Why MCP

- **Standard protocol** — any MCP host can connect. Not locked to OpenClaw or the `otter` CLI.
- **Discoverable** — tools self-describe with JSON Schema. No docs to read, no CLI flags to memorize.
- **Ecosystem alignment** — Claude Desktop, Claude Code, VS Code, Cursor, Windsurf all support MCP. This is where the industry is going.
- **Simplifies agent onboarding** — agents don't need `otter` binary, auth config files, or shell access. They just connect to the MCP server.
- **Replaces the CLI** — the `otter` CLI becomes a thin wrapper (or unnecessary) once MCP tools exist.

## Architecture

### Transport: Streamable HTTP

Use **Streamable HTTP** transport (not stdio). Reasons:

1. Otter Camp is a remote server (Railway), not a local subprocess
2. Multiple agents connect simultaneously — stdio is 1:1
3. HTTP allows standard auth (bearer tokens, the existing session/API key system)
4. SSE streaming for long-running operations (git push, large searches)

**MCP endpoint:** `POST/GET https://api.otter.camp/mcp` (single endpoint per spec)

For local dev: `http://localhost:4200/mcp`

### Authentication

- Reuse existing Otter Camp API tokens as bearer tokens
- MCP clients send `Authorization: Bearer <otter-api-token>` on every request
- Org context derived from token (no separate org parameter needed)
- Per-agent tokens for attribution (who created this issue, who committed this file)

### Server Implementation

New Go package: `internal/mcp/`
New binary: `cmd/mcp/main.go` (can also be mounted as a handler in the existing `cmd/server/`)

Recommended: **Mount as a route in the existing server** at `/mcp` rather than a separate binary. This shares the database pool, store layer, and auth middleware. The MCP handler translates JSON-RPC ↔ internal service calls.

```
cmd/server/main.go          ← existing HTTP server
internal/mcp/
  server.go                  ← MCP server setup, capability negotiation, lifecycle
  handler.go                 ← HTTP handler (POST/GET /mcp, SSE streaming)
  tools.go                   ← tool registry + dispatch
  tools_projects.go          ← project management tools
  tools_issues.go            ← issue management tools
  tools_git.go               ← git/content tools
  tools_agents.go            ← agent management tools
  tools_memory.go            ← memory/knowledge tools
  tools_search.go            ← search tools
  resources.go               ← resource providers
  prompts.go                 ← prompt templates
  auth.go                    ← token validation, agent identity
```

### Dependency

Use the official Go MCP SDK: `github.com/modelcontextprotocol/go-sdk` (or implement the JSON-RPC 2.0 protocol directly — it's simple enough).

## MCP Primitives

### Tools (Agent-Invocable Actions)

These map to the current REST API / CLI commands. Each tool has a JSON Schema input and returns structured content.

#### Project Management

| Tool | Description | Input |
|------|-------------|-------|
| `project_list` | List all projects in the org | `{ filter?: string, limit?: number }` |
| `project_create` | Create a new project | `{ name: string, description?: string, visibility?: "public"\|"private" }` |
| `project_get` | Get project details | `{ project: string }` |
| `project_delete` | Delete a project | `{ project: string }` |

#### Issue Management

| Tool | Description | Input |
|------|-------------|-------|
| `issue_list` | List issues with filters | `{ project: string, status?: "open"\|"closed"\|"all", assignee?: string, label?: string, priority?: string, limit?: number }` |
| `issue_create` | Create an issue | `{ project: string, title: string, body?: string, priority?: "P0"\|"P1"\|"P2"\|"P3", labels?: string[], assignee?: string }` |
| `issue_get` | Get issue details | `{ project: string, number: number }` |
| `issue_update` | Update an issue | `{ project: string, number: number, title?: string, body?: string, status?: string, priority?: string, labels?: string[], assignee?: string }` |
| `issue_close` | Close an issue | `{ project: string, number: number, comment?: string }` |
| `issue_reopen` | Reopen an issue | `{ project: string, number: number }` |
| `issue_comment` | Add a comment | `{ project: string, number: number, body: string }` |
| `issue_assign` | Assign an issue | `{ project: string, number: number, assignee: string }` |

#### Git / Content Operations

| Tool | Description | Input |
|------|-------------|-------|
| `file_read` | Read a file from a project | `{ project: string, path: string, ref?: string }` |
| `file_write` | Write/create a file (creates a commit) | `{ project: string, path: string, content: string, message: string, ref?: string }` |
| `file_delete` | Delete a file (creates a commit) | `{ project: string, path: string, message: string, ref?: string }` |
| `tree_list` | List directory tree | `{ project: string, path?: string, ref?: string, recursive?: boolean }` |
| `commit_list` | List recent commits | `{ project: string, ref?: string, limit?: number }` |
| `diff` | Get diff between refs | `{ project: string, base: string, head: string }` |
| `branch_list` | List branches | `{ project: string }` |
| `branch_create` | Create a branch | `{ project: string, name: string, from?: string }` |

#### Agent Management

| Tool | Description | Input |
|------|-------------|-------|
| `agent_list` | List all agents in the org | `{ status?: "online"\|"offline"\|"all" }` |
| `agent_get` | Get agent details + role card | `{ agent: string }` |
| `agent_activity` | Get agent's recent activity | `{ agent: string, limit?: number }` |
| `whoami` | Get current agent identity | `{}` |

#### Memory / Knowledge

| Tool | Description | Input |
|------|-------------|-------|
| `memory_read` | Read agent memory | `{ agent?: string, key?: string }` |
| `memory_write` | Write to agent memory | `{ key: string, value: string }` |
| `memory_search` | Semantic search across memory | `{ query: string, limit?: number }` |
| `knowledge_search` | Search shared knowledge base | `{ query: string, project?: string, limit?: number }` |

#### Search

| Tool | Description | Input |
|------|-------------|-------|
| `search` | Full-text search across projects | `{ query: string, project?: string, scope?: "issues"\|"content"\|"all", limit?: number }` |

#### Workflow / Pipeline

| Tool | Description | Input |
|------|-------------|-------|
| `workflow_list` | List workflows | `{ project?: string }` |
| `workflow_run` | Trigger a workflow | `{ project: string, workflow: string, params?: object }` |
| `workflow_status` | Check workflow run status | `{ runId: string }` |

### Resources (Context Data)

Resources provide read-only context that MCP clients can pull in. URI-addressable.

| Resource URI Pattern | Description |
|---------------------|-------------|
| `otter://projects` | List of all projects |
| `otter://projects/{name}` | Project metadata |
| `otter://projects/{name}/issues` | Open issues for a project |
| `otter://projects/{name}/tree` | File tree |
| `otter://projects/{name}/files/{path}` | File contents |
| `otter://agents` | All agents with status |
| `otter://agents/{name}` | Agent profile + role card |
| `otter://agents/{name}/memory` | Agent's memory entries |
| `otter://feed` | Recent activity feed |
| `otter://knowledge/{query}` | Knowledge search results |

Resources support `subscribe` — clients can subscribe to changes (e.g., new issues, file updates) and receive notifications.

### Prompts (Interaction Templates)

Reusable prompt templates for common workflows:

| Prompt | Description | Arguments |
|--------|-------------|-----------|
| `create_spec` | Template for writing an issue spec | `{ title: string, context?: string }` |
| `code_review` | Template for reviewing a diff | `{ project: string, pr?: number }` |
| `daily_summary` | Template for generating a daily summary | `{ agent?: string, date?: string }` |
| `issue_triage` | Template for triaging new issues | `{ project: string }` |

## Implementation Plan

### Phase 1: Core MCP Server + Project/Issue Tools (Week 1)

**Files to create:**
- `internal/mcp/server.go` — MCP server initialization, capability declaration
- `internal/mcp/handler.go` — HTTP handler at `/mcp` route, JSON-RPC dispatch, SSE streaming
- `internal/mcp/auth.go` — Bearer token → org/agent identity resolution
- `internal/mcp/tools.go` — Tool registry, `tools/list` and `tools/call` dispatch
- `internal/mcp/tools_projects.go` — `project_list`, `project_create`, `project_get`, `project_delete`
- `internal/mcp/tools_issues.go` — All issue tools (list, create, get, update, close, reopen, comment, assign)

**Files to modify:**
- `internal/api/router.go` — Mount `/mcp` handler
- `cmd/server/main.go` — Wire MCP server into startup
- `go.mod` — Add MCP SDK dependency (if using official SDK)

**Tests:**
- `internal/mcp/handler_test.go` — JSON-RPC lifecycle (initialize, tools/list, tools/call)
- `internal/mcp/tools_projects_test.go` — Project tool integration tests
- `internal/mcp/tools_issues_test.go` — Issue tool integration tests
- `internal/mcp/auth_test.go` — Token validation, agent identity

**Acceptance criteria:**
- `POST /mcp` accepts JSON-RPC 2.0 messages
- `initialize` → returns capabilities (tools, resources)
- `tools/list` → returns all registered tools with JSON Schema
- `tools/call` for project and issue tools works end-to-end
- Bearer token auth working
- MCP Inspector can connect and invoke tools

### Phase 2: Git/Content Tools + Resources (Week 2)

**Files to create:**
- `internal/mcp/tools_git.go` — file_read, file_write, file_delete, tree_list, commit_list, diff, branch_list, branch_create
- `internal/mcp/resources.go` — Resource registry, `resources/list`, `resources/read`
- `internal/mcp/resources_test.go`
- `internal/mcp/tools_git_test.go`

**Acceptance criteria:**
- Agents can read/write files, list trees, create commits via MCP tools
- Resources accessible via `otter://` URIs
- Resource subscriptions emit notifications on changes

### Phase 3: Agent + Memory + Search Tools (Week 3)

**Files to create:**
- `internal/mcp/tools_agents.go` — agent_list, agent_get, agent_activity, whoami
- `internal/mcp/tools_memory.go` — memory_read, memory_write, memory_search, knowledge_search
- `internal/mcp/tools_search.go` — search tool
- `internal/mcp/prompts.go` — Prompt template registry

**Acceptance criteria:**
- Full tool coverage — all current `otter` CLI capabilities available via MCP
- Prompt templates working
- Memory tools functional

### Phase 4: OpenClaw Integration + CLI Migration (Week 4)

**Files to create/modify:**
- OpenClaw MCP client config documentation
- `cmd/otter/` — Refactor CLI to be a thin MCP client (optional — can coexist)
- Migration guide for existing agents

**Acceptance criteria:**
- OpenClaw can connect to Otter Camp as an MCP server
- Agents use MCP tools instead of `otter` CLI
- Existing REST API unchanged (web UI unaffected)

## JSON-RPC Examples

### Initialize
```json
// Client → Server
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": { "name": "openclaw", "version": "1.0.0" }
  }
}

// Server → Client
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "tools": { "listChanged": true },
      "resources": { "subscribe": true, "listChanged": true },
      "prompts": { "listChanged": true }
    },
    "serverInfo": { "name": "otter-camp", "version": "1.0.0" }
  }
}
```

### Create an Issue
```json
// Client → Server
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "tools/call",
  "params": {
    "name": "issue_create",
    "arguments": {
      "project": "otter-camp",
      "title": "Add dark mode to settings page",
      "body": "Users have requested dark mode support in the settings UI.",
      "priority": "P2",
      "labels": ["enhancement", "frontend"]
    }
  }
}

// Server → Client
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "content": [{
      "type": "text",
      "text": "Created issue #125 in otter-camp: \"Add dark mode to settings page\" (P2, open)"
    }]
  }
}
```

### Read a File
```json
{
  "jsonrpc": "2.0",
  "id": 8,
  "method": "tools/call",
  "params": {
    "name": "file_read",
    "arguments": {
      "project": "otter-camp",
      "path": "internal/api/router.go"
    }
  }
}
```

## Key Design Decisions

1. **Mount on existing server, not separate binary.** Shares DB pool, auth middleware, store layer. Less operational overhead. Single deploy.

2. **Streamable HTTP, not stdio.** Otter Camp is a remote service. Multiple concurrent agents. HTTP auth. SSE for streaming.

3. **REST API stays.** Web UI uses REST. MCP is the agent-facing interface. Both coexist, both hit the same store layer.

4. **One MCP server per org.** Multi-tenant. Token determines org context. No per-org server instances.

5. **Tools over Resources for mutations.** Resources are read-only context (per MCP spec). All write operations are tools. Resources for browsing/context, tools for actions.

6. **Agent identity from token.** Each agent has its own API token. MCP server resolves agent identity from bearer token. Commits, issues, comments attributed to the correct agent.

7. **Structured output where possible.** Tools return structured JSON in content blocks (not just human-readable strings) so MCP clients can parse results programmatically.

## Migration Path

1. Ship MCP server alongside existing REST API (Phase 1-3)
2. Update OpenClaw to support MCP server connections (Phase 4)
3. Update AGENTS.md / OTTERCAMP.md to document MCP usage
4. `otter` CLI continues working — becomes optional convenience layer
5. Eventually: CLI becomes thin MCP client itself

## Out of Scope

- **OAuth 2.1 authorization server** — use simple bearer tokens for now. OAuth can come later for third-party MCP client support.
- **Stdio transport** — not needed for a remote server. Could add later for local dev convenience.
- **Breaking changes to REST API** — web UI must continue working.
- **MCP client in Otter Camp** — Otter Camp is a server, not a client. OpenClaw is the MCP host.

## References

- [MCP Specification (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18)
- [MCP Architecture Overview](https://modelcontextprotocol.io/docs/learn/architecture)
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- [MCP Inspector](https://github.com/modelcontextprotocol/inspector) — for testing
- Current Otter Camp API: `internal/api/router.go`
- Current Otter CLI: `cmd/otter/`

## Execution Log
- [2026-02-12 07:30 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 01-ready to 02-in-progress and initialized execution run | Tests: n/a
- [2026-02-12 07:34 MST] Issue #778 | Commit n/a | created | Created micro-issue for MCP HTTP endpoint, initialize lifecycle, and auth foundation | Tests: go test ./internal/mcp -run 'TestHandler(Initialize|RejectsUnauthorized|InvalidJSONRPC)' -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:34 MST] Issue #779 | Commit n/a | created | Created micro-issue for tool registry plus tools/list and tools/call dispatch contract | Tests: go test ./internal/mcp -run 'TestTools(List|Call)' -count=1; go test ./internal/mcp -run 'TestHandlerToolsMethods' -count=1
- [2026-02-12 07:34 MST] Issue #780 | Commit n/a | created | Created micro-issue for project read tools project_list and project_get | Tests: go test ./internal/mcp -run 'TestProjectTools(Read|List|Get)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestProjectToolsWorkspaceIsolation' -count=1
- [2026-02-12 07:34 MST] Issue #781 | Commit n/a | created | Created micro-issue for project mutation tools project_create and project_delete | Tests: go test ./internal/mcp -run 'TestProjectTools(Create|Delete)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestProjectCreateDeleteRoundTrip' -count=1
- [2026-02-12 07:34 MST] Issue #782 | Commit n/a | created | Created micro-issue for issue read tools issue_list and issue_get | Tests: go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestIssueListFiltersByStatePriority' -count=1
- [2026-02-12 07:34 MST] Issue #783 | Commit n/a | created | Created micro-issue for issue mutation tools create/update/close/reopen/comment/assign | Tests: go test ./internal/mcp -run 'TestIssueTools(Create|Update|Close|Reopen|Comment|Assign)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestIssueMutationLifecycleViaMCP' -count=1
- [2026-02-12 07:34 MST] Issue #784 | Commit n/a | created | Created micro-issue for git/content read tools file_read tree_list commit_list diff branch_list | Tests: go test ./internal/mcp -run 'TestGitTools(Read|Tree|CommitList|Diff|BranchList)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestGitToolsReadAreWorkspaceScoped' -count=1
- [2026-02-12 07:34 MST] Issue #785 | Commit n/a | created | Created micro-issue for git/content write tools file_write file_delete branch_create | Tests: go test ./internal/mcp -run 'TestGitTools(Write|Delete|BranchCreate)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestGitWriteToolsCreateCommits' -count=1
- [2026-02-12 07:34 MST] Issue #786 | Commit n/a | created | Created micro-issue for resources list/read with otter:// URI patterns | Tests: go test ./internal/mcp -run 'TestResources(List|Read)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestResourceURIsWorkspaceIsolation' -count=1
- [2026-02-12 07:34 MST] Issue #787 | Commit n/a | created | Created micro-issue for resource subscriptions and listChanged notifications | Tests: go test ./internal/mcp -run 'TestResources(Subscribe|Unsubscribe|ListChangedNotifications)' -count=1
- [2026-02-12 07:34 MST] Issue #788 | Commit n/a | created | Created micro-issue for agent tools agent_list agent_get agent_activity and whoami | Tests: go test ./internal/mcp -run 'TestAgentTools(List|Get|Activity|WhoAmI)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestWhoAmIUsesTokenIdentity' -count=1
- [2026-02-12 07:34 MST] Issue #789 | Commit n/a | created | Created micro-issue for memory/search/workflow tools and prompt registry | Tests: go test ./internal/mcp -run 'Test(MemoryTools|SearchTools|WorkflowTools|Prompts)' -count=1; OTTER_TEST_DATABASE_URL=... go test ./internal/mcp -run 'TestMemoryAndKnowledgeSearchWorkspaceIsolation' -count=1
- [2026-02-12 07:34 MST] Issue #790 | Commit n/a | created | Created micro-issue for OpenClaw MCP integration docs and CLI migration scaffold | Tests: go test ./cmd/otter -run TestMCP -count=1; go test ./internal/mcp -run TestDocsReferencedExamplesCompile -count=1
- [2026-02-12 07:34 MST] Issue #778 | Commit n/a | planning-complete | Verified full planned issue set #778-#790 exists before first code change | Tests: n/a
- [2026-02-12 07:37 MST] Issue #778 | Commit n/a | in-progress | Implemented MCP auth/handler/server scaffolding, mounted /mcp route, and passed initialize/auth/route tests via TDD red-green cycle | Tests: go test ./internal/mcp -run 'TestHandler(Initialize|RejectsUnauthorized|InvalidJSONRPC)' -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1; go test ./internal/mcp -count=1
- [2026-02-12 07:38 MST] Issue #778 | Commit 0595d21 | pushed | Committed and pushed MCP initialize/auth route foundation on branch codex/spec-124-mcp-restructure | Tests: go test ./internal/mcp -run 'TestHandler(Initialize|RejectsUnauthorized|InvalidJSONRPC)' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1; go test ./internal/api -run TestRouter -count=1
- [2026-02-12 07:38 MST] Issue #778 | Commit 0595d21 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:40 MST] Issue #779 | Commit n/a | in-progress | Added tool registry and tools/list + tools/call dispatch with explicit unknown-tool/invalid-params errors via TDD | Tests: go test ./internal/mcp -run 'TestTools(List|Call)' -count=1; go test ./internal/mcp -run 'TestHandlerToolsMethods' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:40 MST] Issue #779 | Commit 72f94d6 | pushed | Committed and pushed MCP tool registry and tools/list/tools/call dispatch foundation | Tests: go test ./internal/mcp -run 'TestTools(List|Call)' -count=1; go test ./internal/mcp -run 'TestHandlerToolsMethods' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:40 MST] Issue #779 | Commit 72f94d6 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:43 MST] Issue #780 | Commit n/a | in-progress | Implemented MCP project read tools project_list/project_get with workspace scoping and router wiring for project tool registration | Tests: go test ./internal/mcp -run 'TestProjectTools(Read|List|Get)' -count=1; go test ./internal/mcp -run 'TestProjectToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:43 MST] Issue #780 | Commit 96f14f8 | pushed | Committed and pushed MCP project_list/project_get tools plus project tool registration wiring | Tests: go test ./internal/mcp -run 'TestProjectTools(Read|List|Get)' -count=1; go test ./internal/mcp -run 'TestProjectToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:43 MST] Issue #780 | Commit 96f14f8 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:45 MST] Issue #781 | Commit n/a | in-progress | Added MCP project_create/project_delete handlers with argument validation, workspace scoping, and project resolution reuse | Tests: go test ./internal/mcp -run 'TestProjectTools(Create|Delete)' -count=1; go test ./internal/mcp -run 'TestProjectTools(Read|List|Get|Create|Delete|WorkspaceIsolation)' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:45 MST] Issue #781 | Commit dee4cb0 | pushed | Committed and pushed MCP project_create/project_delete mutation tools | Tests: go test ./internal/mcp -run 'TestProjectTools(Create|Delete)' -count=1; go test ./internal/mcp -run 'TestProjectTools(Read|List|Get|Create|Delete|WorkspaceIsolation)' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:45 MST] Issue #781 | Commit dee4cb0 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:47 MST] Issue #782 | Commit n/a | in-progress | Implemented MCP issue read tools issue_list/issue_get, status/priority filters, and router registration wiring for issue tools | Tests: go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)|TestIssueToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:48 MST] Issue #782 | Commit dd2d7e7 | pushed | Committed and pushed MCP issue_list/issue_get read tools with router registration wiring | Tests: go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)|TestIssueToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:48 MST] Issue #782 | Commit dd2d7e7 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:50 MST] Issue #783 | Commit n/a | in-progress | Added MCP issue mutation tools issue_create/update/close/reopen/comment/assign with validation and workspace-safe issue resolution | Tests: go test ./internal/mcp -run 'TestIssueTools(Create|Update|Close|Reopen|Comment|Assign)' -count=1; go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)|TestIssueToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:51 MST] Issue #783 | Commit 426f9c5 | pushed | Committed and pushed MCP issue mutation tools for create/update/close/reopen/comment/assign | Tests: go test ./internal/mcp -run 'TestIssueTools(Create|Update|Close|Reopen|Comment|Assign)' -count=1; go test ./internal/mcp -run 'TestIssueTools(Read|List|Get)|TestIssueToolsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:51 MST] Issue #783 | Commit 426f9c5 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:54 MST] Issue #784 | Commit n/a | in-progress | Implemented MCP git/content read tools file_read tree_list commit_list diff branch_list and wired git tool registration | Tests: go test ./internal/mcp -run 'TestGitTools(Read|Tree|CommitList|Diff|BranchList)' -count=1; go test ./internal/mcp -run 'TestGitToolsReadAreWorkspaceScoped' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:54 MST] Issue #784 | Commit f2be7c2 | pushed | Committed and pushed MCP git/content read tools plus router registration wiring | Tests: go test ./internal/mcp -run 'TestGitTools(Read|Tree|CommitList|Diff|BranchList)' -count=1; go test ./internal/mcp -run 'TestGitToolsReadAreWorkspaceScoped' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:54 MST] Issue #784 | Commit f2be7c2 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:56 MST] Issue #785 | Commit n/a | in-progress | Added MCP git/content write tools file_write file_delete branch_create with commit creation semantics on worktree repos | Tests: go test ./internal/mcp -run 'TestGitTools(Write|Delete|BranchCreate)|TestGitWriteToolsCreateCommits' -count=1; go test ./internal/mcp -run 'TestGitTools(Read|Tree|CommitList|Diff|BranchList)|TestGitToolsReadAreWorkspaceScoped' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:57 MST] Issue #785 | Commit ac6d949 | pushed | Committed and pushed MCP git/content write tools file_write file_delete branch_create with commit semantics | Tests: go test ./internal/mcp -run 'TestGitTools(Write|Delete|BranchCreate)|TestGitWriteToolsCreateCommits' -count=1; go test ./internal/mcp -run 'TestGitTools(Read|Tree|CommitList|Diff|BranchList)|TestGitToolsReadAreWorkspaceScoped' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:57 MST] Issue #785 | Commit ac6d949 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 07:58 MST] Issue #786 | Commit n/a | in-progress | Added MCP resources/list and resources/read handlers with otter://projects URI routing to project/issue/git read paths | Tests: go test ./internal/mcp -run 'TestResources(List|Read)|TestResourceURIsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:58 MST] Issue #786 | Commit 29d7c09 | pushed | Committed and pushed resources/list and resources/read URI routing for MCP | Tests: go test ./internal/mcp -run 'TestResources(List|Read)|TestResourceURIsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 07:58 MST] Issue #786 | Commit 29d7c09 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 08:00 MST] Issue #787 | Commit n/a | in-progress | Added resources/subscribe and resources/unsubscribe with in-memory subscription tracking and listChanged notification counters on mutation tools | Tests: go test ./internal/mcp -run 'TestResources(Subscribe|Unsubscribe|ListChangedNotifications)' -count=1; go test ./internal/mcp -run 'TestResources(List|Read)|TestResourceURIsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:01 MST] Issue #787 | Commit d54c497 | pushed | Committed and pushed resources subscribe/unsubscribe lifecycle plus listChanged counters | Tests: go test ./internal/mcp -run 'TestResources(Subscribe|Unsubscribe|ListChangedNotifications)' -count=1; go test ./internal/mcp -run 'TestResources(List|Read)|TestResourceURIsWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:01 MST] Issue #787 | Commit d54c497 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 08:03 MST] Issue #788 | Commit n/a | in-progress | Implemented MCP agent tools agent_list agent_get agent_activity and whoami with workspace scoping and token-identity output | Tests: go test ./internal/mcp -run 'TestAgentTools(List|Get|Activity|WhoAmI)' -count=1; go test ./internal/mcp -run 'TestWhoAmIUsesTokenIdentity' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:04 MST] Issue #788 | Commit 587a856 | pushed | Committed and pushed MCP agent tools and whoami identity tool with router wiring | Tests: go test ./internal/mcp -run 'TestAgentTools(List|Get|Activity|WhoAmI)' -count=1; go test ./internal/mcp -run 'TestWhoAmIUsesTokenIdentity' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:04 MST] Issue #788 | Commit 587a856 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 08:06 MST] Issue #789 | Commit n/a | in-progress | Implemented MCP memory/search/workflow tools and prompt registry methods (prompts/list, prompts/get) with workspace isolation on in-memory data | Tests: go test ./internal/mcp -run 'Test(MemoryTools|SearchTools|WorkflowTools|Prompts)' -count=1; go test ./internal/mcp -run 'TestMemoryAndKnowledgeSearchWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:06 MST] Issue #789 | Commit 42d0fd8 | pushed | Committed and pushed memory/search/workflow tool suite plus prompts/list and prompts/get methods | Tests: go test ./internal/mcp -run 'Test(MemoryTools|SearchTools|WorkflowTools|Prompts)' -count=1; go test ./internal/mcp -run 'TestMemoryAndKnowledgeSearchWorkspaceIsolation' -count=1; go test ./internal/mcp -count=1; go test ./internal/api -run TestRouterRegistersMCPRoute -count=1
- [2026-02-12 08:06 MST] Issue #789 | Commit 42d0fd8 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 08:13 MST] Issue #790 | Commit n/a | in-progress | Implemented `otter mcp` thin-client scaffold (`info` and `call`), added MCP migration docs, and added docs JSON example validation test | Tests: go test ./cmd/otter -run TestMCP -count=1; go test ./internal/mcp -run TestDocsReferencedExamplesCompile -count=1; go test ./cmd/otter ./internal/mcp -count=1
- [2026-02-12 08:14 MST] Issue #790 | Commit e045199 | pushed | Committed and pushed MCP migration scaffold, CLI thin-client commands, docs updates, and docs example validation test | Tests: go test ./cmd/otter -run TestMCP -count=1; go test ./internal/mcp -run TestDocsReferencedExamplesCompile -count=1; go test ./cmd/otter ./internal/mcp -count=1
- [2026-02-12 08:14 MST] Issue #790 | Commit e045199 | closed | Closed GitHub issue after posting commit hash and test evidence | Tests: n/a
- [2026-02-12 08:14 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 02-in-progress to 03-needs-review after completing all planned issues #778-#790 | Tests: n/a
- [2026-02-12 08:15 MST] Issue #n/a | Commit n/a | pr-opened | Opened PR #791 from codex/spec-124-mcp-restructure into main for reviewer validation and merge | Tests: n/a
- [2026-02-12 11:05 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 04-in-review to 01-ready after PR #791 E2E failure required remediation work | Tests: n/a
- [2026-02-12 11:05 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 01-ready to 02-in-progress to execute reviewer-required CI remediation | Tests: n/a
- [2026-02-12 11:06 MST] Issue #802 | Commit n/a | created | Created micro-issue for PR #791 E2E remediation with explicit command-level tests | Tests: cd web && npx playwright test e2e/agents.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/auth.spec.ts --project=chromium
- [2026-02-12 11:07 MST] Issue #802 | Commit n/a | in-progress | Reproduced failing baseline on spec-124 branch and confirmed unauthenticated fixtures and stale selector expectations | Tests: cd web && npx playwright test e2e/agents.spec.ts --project=chromium
- [2026-02-12 11:10 MST] Issue #802 | Commit 73b11e0 | pushed | Backported E2E stabilization commits for auth bootstrap, agents locators/chat interactions, navigation, and auth login flow; pushed to PR #791 branch | Tests: cd web && npx playwright test e2e/agents.spec.ts --project=chromium; cd web && npx playwright test e2e/navigation.spec.ts --project=chromium; cd web && npx playwright test e2e/auth.spec.ts --project=chromium
- [2026-02-12 11:10 MST] Issue #802 | Commit 73b11e0 | closed | Closed micro-issue after posting commit hashes and passing test evidence | Tests: n/a
- [2026-02-12 11:10 MST] Issue #n/a | Commit n/a | reviewer-required-resolved | Removed `## Reviewer Required Changes` block after completing PR #791 CI remediation scope | Tests: n/a
- [2026-02-12 11:10 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 02-in-progress to 03-needs-review after completing remediation issue #802 | Tests: n/a
- [2026-02-12 12:14 MST] Issue #n/a | Commit n/a | state-transition | Moved spec 124 from 01-ready to 02-in-progress to address top-level Reviewer Required Changes block | Tests: n/a
- [2026-02-12 12:14 MST] Issue #n/a | Commit n/a | in_progress | Switched to dedicated branch codex/spec-124-mcp-restructure for isolated remediation work | Tests: n/a
- [2026-02-12 12:15 MST] Issue #817 | Commit n/a | created | Created P3 remediation micro-issue to document stub tool/subscription limitations with TODO annotations | Tests: n/a
- [2026-02-12 12:15 MST] Issue #814 | Commit n/a | planning-complete | Verified required reviewer-change micro-issue set exists (#814, #815, #817) before coding | Tests: n/a
- [2026-02-12 12:20 MST] Issue #814 | Commit bc86f00 | closed | Added validateGitRef safeguards across git tool ref/base/head inputs with option-injection regression tests | Tests: go test ./internal/mcp -run 'TestValidateGitRef|TestGitToolsRejectOptionLikeRefs|TestGitToolsAllowValidRefs|TestGitTools(Read|CommitList|Diff|Write|Delete)' -count=1; go test ./internal/mcp -count=1
- [2026-02-12 12:21 MST] Issue #815 | Commit 9fb5439 | closed | Removed remaining non-MCP Ellie/Elephant drift from agent UI pages and tests to restore branch isolation and frontend gate stability | Tests: cd web && npx vitest run
- [2026-02-12 12:21 MST] Issue #817 | Commit a915686 | closed | Documented stub limitations with TODO annotations in memory/search/workflow and resource subscription scaffolds | Tests: go test ./internal/mcp -run 'Test(MemoryTools|SearchTools|WorkflowTools|Resources)' -count=1; go test ./internal/mcp -count=1
- [2026-02-12 12:21 MST] Issue #n/a | Commit n/a | reviewer-required-resolved | Removed top-level Reviewer Required Changes block after closing required follow-up issues #814, #815, and #817 | Tests: n/a
- [2026-02-12 12:21 MST] Issue #n/a | Commit n/a | needs_review | Moved spec 124 to 03-needs-review after reviewer-required remediation commits were pushed to PR #791 | Tests: n/a
