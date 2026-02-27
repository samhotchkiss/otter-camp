# Test 029: Tools & MCP Integration

**Sections:** 11. Tools & Tool Policy, 12. MCP Integration
**Tested:** 2026-02-26
**Result:** PARTIAL

## Tools (Section 11)

**`GET /v1/tools`** — PASS ✓
- Returns 67 tools with: id, name, display_name, description, tool_tier (tier1/tier2), tool_domain (native/browser/cli/mcp), required_capability, input_schema, allowed
- Tool names sanitized (dot→underscore, e.g. `agent.create_temp` not `agent_create_temp` — note: dots still present in name but sanitized for schema keys) ✓
- Tool domains present: native, browser, cli (tier2 tools)

**Tool policy enforcement:**
- `GET /v1/control/policies` / `POST` / `DELETE` — PASS ✓ (confirmed from test 028)
- `PATCH /v1/control/policies/:id` — not tested

**Tool executions:**
- `GET /v1/control/tool-executions` — PASS ✓
- Returns execution records with: policy_decision, status, error_message, duration_ms
- Browser tools fail with "browser: not found" (expected in dev — no browser binary)

## MCP Integration (Section 12)

**`GET /v1/mcp/connections`** — PASS ✓
- Returns connections with: id, display_name, slug, transport (sse/stdio), transport_config, status, is_enabled, last_healthy_at
- All connections in dev show status: "failed" (no real MCP servers running)
- SSRF test connections created in prior iterations (file:///etc/passwd, 169.254.169.254) → correctly blocked

**Connection health:**
- status field tracks connection health ✓
- last_healthy_at is null for never-healthy connections ✓

**Untested:**
- `GET /v1/mcp/connections/:id` — not tested
- `POST /v1/mcp/connections/:id/test` — not tested
- `GET /v1/mcp/connections/:id/tools` — N/A (all connections failed)
- MCP tool execution — N/A (no working connections in dev)

## Issues Filed

None new from this test.
