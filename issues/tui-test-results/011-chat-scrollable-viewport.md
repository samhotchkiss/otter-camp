# Test 011: Chat messages in scrollable viewport

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Messages in scrollable viewport
**Tested:** 2026-02-26 16:30
**Result:** PASS

## How I Tested

1. Focused chat pane (Alt-3) on General/Frank session (has 2500+ messages from previous testing)
2. Observed scroll indicator showing "↓ 2563 more · PgDn to scroll"
3. Pressed PgUp — scrolled up, indicator count changed
4. Pressed PgDn — scrolled back down

## TUI Screen State

Scroll indicator visible:
```
│     ↓ 2563 more · PgDn to scroll      │
```

Status bar confirmation:
```
M/chat · Chat scrolled up.
```

## Observed Behavior

- Messages display in a scrollable viewport ✓
- PgUp scrolls up through history ✓
- PgDn scrolls down ✓
- Scroll indicator shows "↓ N more · PgDn to scroll" when not at bottom (EX-004) ✓
- Help line updates to show "PgUp/PgDn scroll" when chat is focused ✓
- Status bar shows "Chat scrolled up." confirmation ✓

## Expected Behavior

As observed.

## Notes

The session has accumulated 2500+ messages from previous testing loops. Scrolling performance is good — no lag even with large history. The EX-004 scroll indicator is working correctly.

## Issue Filed

None.
