# Test 017: Message Queue

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Operator can stack messages while agent turn is in progress
- Edit, steer, or delete queued messages
- Queue displayed below input area during active turn
**Tested:** 2026-02-26
**Result:** PASS

## How I Tested

Sent a message to Frank during an active turn, then sent additional messages and observed queue display. Also examined model.go for queue logic.

## TUI Screen State (during active turn with queue)

```
│ ╭────────────────────────────────╮   │
│ │ ▌                              │   │
│ ╰────────────────────────────────╯   │
│   q1: please respond with jus…       │
│   q2: this is a queued message       │
 ●  connected  ·  General / Frank  ·  M/chat  ·  Queued message (2 pending).
```

## Findings Per Item

**Operator can stack messages during active turn:**
- `sendOrQueueInput()` checks `m.activeTurn` — if true, adds to `m.queuedMessages` ✓
- Multiple messages can be stacked ✓
- Status bar shows count: "Queued message (2 pending)." ✓
- Result: PASS

**Edit, steer, or delete queued messages:**
- When `activeTurn == true` AND `queuedMessages` non-empty AND input is empty:
  - Press `e` → pulls first queued message back into input for editing ✓
  - Press `s` → marks first queued message with `[steer]` flag ✓
  - Press `d` → deletes first queued message ✓
- Also available via command: `:queue edit|steer|delete` ✓
- Result: PASS

**Queue displayed below input area:**
- `renderChatPanel()` appends queue lines below input box ✓
- Shows up to 3 queued messages as `q1: text…`, `q2: text…`, `q3: text…`
- Overflow: "+N more queued" indicator for >3 queued messages ✓
- Steer flag shown as `[steer]` suffix ✓
- Result: PASS

## Notes

Queue implementation is solid. The e/s/d shortcuts are not documented in the help screen — they only appear in the EX-017 code comments. This is a minor discoverability issue.
