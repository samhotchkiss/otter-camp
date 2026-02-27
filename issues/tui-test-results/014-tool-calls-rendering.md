# Test 014: Tool calls rendered inline during response

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Tool calls rendered inline during response (collapsed by default, expandable with Enter)
**Tested:** 2026-02-26 16:40
**Result:** PARTIAL

## How I Tested

1. Examined `renderChatMessages()` in view.go for tool call rendering
2. Observed actual tool call display in live chat (Frank used tools in recent message)

## Code Analysis

```go
for _, tc := range msg.ToolCalls {
    var statusStyle lipgloss.Style
    switch tc.Status {
    case "success": statusStyle = styleTool
    case "pending": statusStyle = styleReconnecting
    default:        statusStyle = styleDisconnected
    }
    tcLine := "  ⚙ " + tc.Name + "  " + statusStyle.Render(tc.Status)
    lines = append(lines, styleMuted.Render(tcLine))
}
```

## Observed Behavior

Tool calls ARE rendered inline with:
- `⚙` icon + tool name + status ("success", "pending", or error)
- Color-coded by status (green=success, amber=pending, red=error)

What is NOT implemented:
- "Collapsed by default" — tool calls show always, no collapse
- "Expandable with Enter" — no expand/collapse toggle exists
- Tool results not shown separately (spec: "Tool results: success/error indicator; large results collapsed behind [show more]")

## Expected Behavior

Per spec:
- Tool calls collapsed by default (just `⚙ tool_name` toggle line)
- Enter expands to show full tool call details
- Tool results: success/error; large results behind "[show more]"

## Notes

The current rendering is minimal but functional — at least tool calls are visible. The collapse/expand interaction is not implemented. Given the complexity, this is a medium-effort enhancement.

## Issue Filed

Issue #167 — Tool call expand/collapse not implemented; results not shown
