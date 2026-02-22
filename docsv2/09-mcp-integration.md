# 09. MCP Integration

## Goal

- Provide a first-class MCP client layer so agents can use external tools/resources safely.

## MCP Integration Scope

- Register MCP servers per org or project.
- Discover tools/resources/prompts from connected MCP servers.
- Execute MCP calls through policy and audit middleware.

## Entities

- `mcp_connection`
- `mcp_tool_catalog`
- `mcp_execution_log`
- `mcp_secret_binding`

## Security Controls

- Per-connection allowlist for tools.
- Parameter validation and secret redaction.
- Capability-based authorization before execution.

## Reliability

- Connection health checks.
- Timeout and retry policies.
- Circuit breaker for unstable endpoints.

## UX Requirements

- Admin can add/remove/test MCP connections.
- Agent profiles can opt into specific MCP tool sets.
- Tool call traces visible in session/task timeline.

## Open Questions

- How much tool schema normalization do we do across servers?
- Do we support both stdio and network transports at launch?
- Should MCP prompts be directly executable or advisory only?

