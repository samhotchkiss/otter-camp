# Task 111: Tool function names contain dots — rejected by OpenAI and Anthropic API

Layer: L2
Effort: S
Priority: CRITICAL — blocks ALL agent turns (every session has 67 tools, all names invalid)
Depends on: 098 (tools sent to provider)

## Context

After issue 098 fix, tool definitions are now sent to OpenAI. However, all native tool names use dot notation (e.g., `file.read`, `git.status`, `memory.query`) which violates OpenAI's naming constraint.

OpenAI error (live):
```
Invalid 'tools[0].function.name': string does not match pattern.
Expected a string that matches the pattern '^[a-zA-Z0-9_-]+$'.
```

Native tool names in the DB (all use dot notation):
- `file.read`, `file.list`, `file.search`
- `git.status`, `git.diff`, `git.log`
- `memory.query`
- `project.list`, `project.get`
- `task.list`, `task.get`, `task.create`, etc.

Anthropic has the same restriction: tool names must match `^[a-zA-Z0-9_-]+$`.

## Root Cause

In `internal/tools/resolver.go`, `buildUniverse` sets `Name: tool.Name` directly from the DB. The DB stores names in `namespace.action` format. Neither the resolver nor `internal/gateway/client.go` sanitizes these names before they reach the provider API.

## Required Fix

In `internal/gateway/client.go`, in `openAITools` and `anthropicTools`, sanitize the tool name before including it in the provider payload:

```go
func sanitizeToolName(name string) string {
    // Replace dots and other invalid chars with underscores
    var sb strings.Builder
    for _, c := range name {
        if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
           (c >= '0' && c <= '9') || c == '_' || c == '-' {
            sb.WriteRune(c)
        } else {
            sb.WriteRune('_')
        }
    }
    return sb.String()
}
```

Example: `file.read` → `file_read`, `git.status` → `git_status`

**Important**: When the model returns a `tool_calls` response with a sanitized function name (e.g., `file_read`), the tool dispatcher must be able to look up the original tool name (`file.read`). Two options:
1. **Reverse mapping**: Before sending to API, store a `sanitized_name → original_name` map in the turn context. When processing tool call responses, un-sanitize using the map.
2. **Deterministic reverse**: Since dots are the only invalid char, `strings.Replace(sanitizedName, "_", ".", -1)` would NOT work reliably (underscores could be original). Use option 1.

The cleanest fix: add a `sanitized_name` field to `ToolDescriptor` that is computed once and used in API calls. The dispatcher can look up tools by `sanitized_name` when processing model responses.

Alternatively, if the tool dispatcher already looks up by name, simply add a DB lookup fallback: try original name first, then try with dots replaced by underscores.

## Acceptance Criteria

- [ ] OpenAI API accepts tool definitions without 400 error on name validation
- [ ] Anthropic API accepts tool definitions without 400 error on name validation
- [ ] Tool call responses from the model can be successfully dispatched (sanitized name maps back to original)
- [ ] `file.read` → sent as `file_read` → dispatcher resolves back to `file.read` handler
- [ ] `go build ./...` passes

## Required Tests

- Unit: `sanitizeToolName("file.read")` → `"file_read"`
- Unit: `sanitizeToolName("git.status")` → `"git_status"`
- Unit: OpenAI payload uses sanitized tool names
- Unit: Anthropic payload uses sanitized tool names
- Integration: agent turn with tools present completes (no 400 from provider)
