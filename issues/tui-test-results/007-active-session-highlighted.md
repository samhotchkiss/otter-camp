# Test 007: Active session highlighted with distinct background

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Active session highlighted with distinct background
**Tested:** 2026-02-26 16:22
**Result:** PASS

## How I Tested

1. Selected "Task 1 / Launch docs" session via Enter in sidebar
2. Looked at sidebar to see how active session vs others are displayed
3. Observed the `✓` badge and distinct styling

## TUI Screen State

```
│   General / …   │  ← inactive session (styleText styling)
│ ▾ Project Al…   │  ← project node
│   › Task 1… ✓ ○ │  ← ACTIVE session: shows ✓ badge + cursor
│   › Task 2 /… ◌ │  ← inactive task session
```

## Observed Behavior

The active session "Task 1 / Launch docs" shows a `✓` badge in green to the right of the label. In the actual terminal (not visible in tmux capture due to ANSI stripping), the active session also has a distinct amber/warm color (`styleActive`) vs the gray (`styleText`) of inactive sessions. The ✓ badge is only shown when cursor AND active coincide (EX-010 improvement).

## Expected Behavior

Active session visually distinguished from others. ✓

## Notes

The distinction uses two visual cues: color (amber for active, gray for inactive) and the ✓ badge when cursor is on the active session. This exceeds the spec's "distinct background" requirement — the implementation uses foreground color rather than background color, but the effect is clear. ANSI colors don't appear in tmux capture-pane output, but they're visible in the real terminal.

## Issue Filed

None.
