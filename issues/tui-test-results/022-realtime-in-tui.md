# Test 022: Realtime in TUI

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Persistent SSE connection with auto-reconnect
- Subscribes to: org scope + active session scope
- Chat deltas, task status changes, inbox items, merge queue updates, agent run events
- Sequence-based catch-up on reconnect
- Connection state indicator in status bar (connecting / connected / reconnecting / degraded)
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Examined `realtime.go` and `cmd/ottercamp/tui.go` for SSE connection setup. Observed live TUI status bar.

## Status Bar Observed

```
 ●  connected  ·  General / Frank  ·  M/chat
```
The `●` (solid green) = connected. The connection indicator changes to `◌` during reconnect.

## Findings Per Item

**Persistent SSE connection with auto-reconnect:**
- `RealtimeClient.Run()` is a loop: connects, consumes stream, reconnects on any error ✓
- Exponential backoff: 100ms → 200ms → 400ms → 1s between retries ✓
- Result: PASS

**Subscribes to: org scope + active session scope:**
- `cmd/ottercamp/tui.go:117`: `Scopes: "org"` — only org scope subscribed
- Session scope NOT subscribed via SSE scopes parameter
  - Session-scoped events (chat deltas, turn events) DO arrive because they're filtered by session after receipt
  - But subscription is only to "org" scope, not per-session scope as specced
- Result: PARTIAL (only org scope; session scope not explicitly subscribed)

**Chat deltas, task status changes, inbox items, merge queue updates, agent run events:**
- Chat deltas (`chat.message.delta`): received and applied ✓
- Task status changes: workspace state updates on SSE events ✓
- Inbox items: received via SSE org events ✓
- Merge queue updates: received via SSE ✓
- Agent run events: received but turn state managed separately ✓
- Result: PASS

**Sequence-based catch-up on reconnect:**
- `RealtimeClient`: stores `Reducer.LastSeq()` as `sinceSeq` on reconnect ✓
- On reconnect: `Connector.Connect(ctx, sinceSeq)` passes sequence to server ✓
- Gap detection: `ErrSequenceGap` marks stream as degraded ✓
- Snapshot refresh on reconnect: `refreshSnapshots()` fetches current state ✓
- Result: PASS

**Connection state indicator in status bar:**
- `●` green = connected ✓
- `◌` amber = reconnecting or degraded ✓
- `○` gray = disconnected ✓
- Shown in leftmost position of status bar ✓
- Text label: "connecting / connected / reconnecting / degraded" — code shows "connected (degraded)" as text ✓
- Result: PASS

## Notes

Live TUI observation confirmed `●  connected` displayed throughout testing. The SSE connection is robust. Minor gap: subscription scope says "org" only, not per-session as specced.
