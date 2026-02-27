# Test 013: Markdown rendered via Glamour

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Markdown rendered via Glamour (headings, bold, italic, lists, links, inline code)
**Tested:** 2026-02-26 16:38
**Result:** FAIL

## How I Tested

1. Sent a message and observed agent response in TUI (agent often responds with markdown)
2. Examined `renderChatMessages()` in view.go to see how content is rendered
3. Searched for calls to `markdownToPlain()` (the Glamour wrapper in chat.go)

## TUI Screen State

Agent response showing raw markdown (not rendered):
```
│ ### Action Plan:                      │
│                                       │
│ **Would you like me to attempt        │
│ advancing the flow to check for       │
│ auto-completion?**                    │
```

The `###` heading and `**bold**` markers are displayed as literal characters, not rendered.

## Code Evidence

`markdownToPlain()` is defined in `chat.go` but **never called anywhere** in the codebase:
```
grep -rn 'markdownToPlain' internal/  → only definition, no calls
```

In `view.go` `renderChatMessages()` line 922–929:
```go
content := strings.TrimSpace(msg.Content)
if content != "" {
    wrapped := wrapText(content, width)      // plain word wrap, no markdown
    for _, wl := range wrapped {
        lines = append(lines, styleText.Render(wl))
    }
}
```

Glamour is imported in `go.mod` and a renderer function exists but is dead code.

## Expected Behavior

`**bold**` → bold text, `### Heading` → larger/styled heading, `- list item` → proper list, `` `code` `` → highlighted inline code. These should be rendered by Glamour before display.

## Notes

This is a significant gap — Frank regularly responds with markdown (headers, bold, lists) and it all shows as literal `**` markers. The fix is to pipe `msg.Content` through `markdownToPlain(content, width)` instead of raw `wrapText`.

Potential issue: Glamour adds extra newlines and formatting that may not integrate cleanly with the line-by-line rendering approach in `renderChatMessages`. Testing needed after fix.

## Issue Filed

Issue #166 — Markdown not rendered in chat messages (Glamour dead code)
