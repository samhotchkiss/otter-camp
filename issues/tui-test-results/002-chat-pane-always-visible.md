# Test 002: Chat pane always visible

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Chat pane always visible
**Tested:** 2026-02-26 16:12
**Result:** PARTIAL

## How I Tested

1. Verified chat pane visible at M size (110 cols) — confirmed
2. Read layout.go to understand behavior at smaller sizes
3. Checked S size behavior (< 100 cols)

## TUI Screen State

At M size — chat pane visible:
```
╭─────────────────╮╭────────────────────────────────────────────────╮╭───────────────────────────────────────╮
│ SESSIONS        ││ DASHBOARD                                      ││ General / Frank  ·  org               │
```

## Observed Behavior

At M (100–139 cols): chat always visible alongside main.
At S (70–99 cols): when sidebar is focused or visible, only sidebar shows; when focus is main/chat, main+chat show. Chat IS visible when not in sidebar mode.
At XS (<70 cols): only the focused panel shows full-screen. Chat is NOT visible simultaneously with other panels.

The spec says "Chat pane always visible." At S and XS sizes, this is not fully honored.

## Expected Behavior

Chat pane should always be visible, even at narrow widths (possibly as a slim strip or always the active panel).

## Notes

At XS/S this is a design trade-off — the terminal literally doesn't have room for three columns. However the spec states "always visible." The S-size behavior where sidebar focus hides both main and chat is particularly notable — pressing Tab or 1 to focus the sidebar causes chat to disappear entirely.

## Issue Filed

Issue #163 — Chat pane not visible at S size when sidebar is focused
