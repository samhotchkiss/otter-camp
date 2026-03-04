# Autowork Runner Notes

## Reviewer MCP policy

`claude-review-autowork.sh` uses a headless MCP preflight to reduce repeated OAuth noise from claude.ai Google connectors.

- Default: claude.ai MCP servers are disabled in unattended reviewer runs.
- Opt-in: set `AUTO_REVIEW_ENABLE_CLAUDEAI_MCP=1`.
- Preflight still disables claude.ai MCP servers when:
  - Claude OAuth credentials are missing, or
  - `~/.claude/mcp-needs-auth-cache.json` reports Gmail/Google Calendar auth gaps.

The runner logs exactly one diagnostic per run:

- `reviewer-mcp preflight: claude_ai_mcp=enabled reason=opt_in_and_auth_present`
- `reviewer-mcp preflight: claude_ai_mcp=disabled reason=...`

At execution time, the script exports `ENABLE_CLAUDEAI_MCP_SERVERS=true|false` before invoking `claude`.
