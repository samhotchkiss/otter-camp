# Task 098: Tool definitions not sent to LLM provider API — model can never call tools

Layer: L2
Effort: S
Depends on: 094 (tool dispatcher implemented)

## Context

The tool dispatcher (issue 094) is fully implemented and wired. The `ToolBroker` returns tool descriptors in `req.Prompt.ToolDescriptors`. However, `buildProviderBody` in `internal/gateway/client.go` constructs the OpenAI/Anthropic API payload without ever including the `tools` array.

Evidence from live session:
- Frank responds: "I'm currently unable to access external tools or browse the internet"
- Inspected `buildProviderBody` in `internal/gateway/client.go`: builds `{"model":..., "messages":..., "stream":true}` — no `tools` key
- `req.Prompt.ToolDescriptors` is populated by `ToolBroker` but never serialized into the API payload
- The model receives no tool definitions so it cannot invoke any tools

## Root Cause

In `internal/gateway/client.go`, `buildProviderBody` (approximately line 850) constructs the provider-specific request body. For OpenAI:

```go
body := map[string]any{
    "model":    req.Model,
    "messages": messages,
    "stream":   true,
}
// req.Prompt.ToolDescriptors is populated but never added to body
return body, nil
```

The OpenAI API expects a `tools` array in the format:
```json
{
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "tool_name",
        "description": "...",
        "parameters": { ... }
      }
    }
  ]
}
```

Similarly, the Anthropic API expects a `tools` array.

## Required Fix

In `buildProviderBody` (or equivalent), when `len(req.Prompt.ToolDescriptors) > 0`:

1. For **OpenAI**: serialize `req.Prompt.ToolDescriptors` into OpenAI `tools` format and include in the request body. Also set `tool_choice: "auto"`.
2. For **Anthropic**: serialize into Anthropic `tools` format and include in the request body.

`ToolDescriptor` likely has fields: `Name`, `Description`, `Parameters` (JSON schema). Map these to the provider's expected format.

## Acceptance Criteria

- [ ] `buildProviderBody` includes `tools` array when `req.Prompt.ToolDescriptors` is non-empty
- [ ] OpenAI format: `[{"type":"function","function":{"name":...,"description":...,"parameters":{...}}}]`
- [ ] Anthropic format: `[{"name":...,"description":...,"input_schema":{...}}]`
- [ ] When no tool descriptors, `tools` key is omitted (backwards compatible)
- [ ] Live validation: agent turn with tools configured results in a tool_use response (or no "unable to use tools" message)
- [ ] `go build ./...` passes

## Required Tests

- Unit: `buildProviderBody` with non-empty ToolDescriptors includes `tools` in OpenAI payload
- Unit: `buildProviderBody` with non-empty ToolDescriptors includes `tools` in Anthropic payload
- Unit: `buildProviderBody` with empty ToolDescriptors omits `tools` key
