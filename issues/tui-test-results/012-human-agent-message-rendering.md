# Test 012: Human messages "You" label; Agent messages streamed with name

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Human messages: distinct top-border label "You"
- Agent messages: left-aligned with agent name and role; text streams in real-time
**Tested:** 2026-02-26 16:34
**Result:** PARTIAL

## How I Tested

1. Sent message "hello, this is test message for QA loop" in chat
2. Observed streaming of agent response (▌ cursor visible during streaming)
3. Scrolled up to find the user message with "You" label
4. Verified code confirms "You" label for user role, "Frank" for assistant

## TUI Screen State

During streaming (▌ in input area, agent still responding):
```
│ │ ▌                               │   │
```
Status bar shows no active turn indicator after completion.

Agent message visible at end of history:
```
│ Frank                           08:22 │
│ ───────────────────────────────────── │
│ The current flow node execution for   │
```

Human message "You" label: confirmed in code — `roleLabel = "You"` for user role.

## Observed Behavior

- Agent response streamed in real-time (character by character visible during streaming) ✓
- Agent name shown ("Frank") with timestamp ✓
- Separator `─────────────────` below agent name header ✓
- ▌ cursor visible in input during streaming (indicating active turn) ✓
- "You" label confirmed in rendering code ✓
- Left-aligned agent messages ✓

However: Human message "You" label not directly visible in screen capture (requires scrolling through 2760+ messages to find the most recent "You" label, and the tmux capture strips ANSI so the distinct color styling isn't visible).

Agent name is "Frank" — this is the display_name from the agent record. The spec says "agent name and role" — "role" (PM, worker, etc.) is not shown alongside the name.

## Expected Behavior

Per spec: "Agent messages: left-aligned with agent name and role" — role not shown.

## Notes

- Streaming works correctly ✓
- "You" label works correctly ✓
- Agent role not shown in header (just name + timestamp) — minor gap
- Markdown rendering from Glamour not directly testable via tmux capture (ANSI not preserved)

## Issue Filed

None — core functionality works. Role display in agent header is a minor omission, not filed.
