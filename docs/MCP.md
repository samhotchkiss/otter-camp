# MCP Integration and Migration

Otter Camp now exposes an MCP server at `/mcp` using streamable HTTP transport.
Use this guide to connect OpenClaw (or any MCP host), migrate automation, and keep rollback simple.

## Endpoints

- Hosted: `https://api.otter.camp/mcp`
- Local dev: `http://localhost:4200/mcp`

Derive the MCP endpoint from your configured API base URL:

- `https://api.otter.camp` -> `https://api.otter.camp/mcp`
- `http://localhost:4200` -> `http://localhost:4200/mcp`

## Authentication

MCP uses the same bearer token model as the Otter API.

- Header: `Authorization: Bearer <otter-token>`
- Recommended: per-agent token for attribution
- Keep org scope configured in token/default org

## OpenClaw MCP Configuration

Use `otter mcp info` to print endpoint details and a starter config snippet.

```bash
otter auth login --token <oc_sess_or_oc_git_token> --org <org-id> --api https://api.otter.camp
otter mcp info
otter mcp info --json
```

Example OpenClaw MCP snippet (hosted):

```json
{
  "mcpServers": {
    "otter-camp": {
      "transport": "streamable-http",
      "url": "https://api.otter.camp/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

Example OpenClaw MCP snippet (local):

```json
{
  "mcpServers": {
    "otter-camp-local": {
      "transport": "streamable-http",
      "url": "http://localhost:4200/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

## CLI Thin MCP Client

`otter` now includes a thin MCP wrapper for migration support:

- `otter mcp info` shows endpoint and starter config
- `otter mcp call` sends raw JSON-RPC method calls

Examples:

```bash
otter mcp call tools/list --params '{}' --json
otter mcp call tools/call --params '{"name":"project_list","arguments":{"limit":10}}' --json
```

Initialize example via CLI:

```bash
otter mcp call initialize --params '{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"otter-cli","version":"dev"}}' --json
```

## JSON-RPC Examples

Initialize:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": {
      "name": "openclaw",
      "version": "1.0.0"
    }
  }
}
```

List tools:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list",
  "params": {}
}
```

Call tool:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "issue_create",
    "arguments": {
      "project": "otter-camp",
      "title": "Document MCP migration path",
      "priority": "P2"
    }
  }
}
```

## Compatibility Boundaries

- MCP is now the primary agent interface for automation and host integrations.
- Existing REST endpoints remain unchanged for web/UI flows.
- Existing `otter` CLI commands remain available; MCP adoption is incremental.
- Bridge/websocket paths remain supported while migration is in progress.

## Phased Rollout and Rollback

1. Phase 1: connect host and validate `initialize`, `tools/list`, `resources/list`.
2. Phase 2: move read-only automation to MCP (`project_list`, `issue_list`, `file_read`).
3. Phase 3: move mutation flows (`issue_create`, `file_write`, workflow actions).
4. Phase 4: treat legacy CLI/API calls as fallback-only.

Rollback:

1. Keep current CLI and REST scripts in place until MCP parity is validated.
2. If MCP integration breaks, switch automation back to existing CLI/REST commands.
3. Keep token/org config unchanged; only the caller path changes.
