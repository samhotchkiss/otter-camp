# Test 020: Responsive Layout

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Below 120 columns: sidebar collapses to icons/abbreviated names
- Below 100 columns: sidebar hides; toggle with keybinding
- Below 80 columns: single-panel mode (Tab switches panels full-screen)
- tmux compatibility: keybinding fallback for tmux-captured keys
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Used `tmux resize-window` to resize to 115 cols and 60 cols, observed layout changes. Examined `layout.go:resolveSizeClass()` and `computeLayout()`.

## Actual Size Class Thresholds (from code)

```
XS: ≤69 cols or ≤22 rows  → single focused panel only
S:  70-99 cols             → sidebar+chat OR main+chat
M:  100-139 cols           → all 3 panels (18/46/36% weights)
L:  140-199 cols           → all 3 panels (standard proportions)
XL: ≥200 cols              → all 3 panels + gutter centering
```

## Findings Per Item

**Below 120 columns: sidebar collapses to icons/abbreviated names:**
- Spec says: below 120 → sidebar shows icons/abbreviated names
- Actual: at 100-139 cols (SizeM), all 3 panels show with 18% sidebar width
  - Sidebar shows abbreviated labels (e.g. "General…") due to width truncation ✓
  - No dedicated icon-only mode
- At 70-99 cols (SizeS), sidebar hides when focus is on main/chat ✓
- Result: PARTIAL (abbreviation happens naturally; no icon-only mode)

**Below 100 columns: sidebar hides; toggle with keybinding:**
- Spec: below 100 cols → sidebar hides; toggle keybinding
- Actual: at SizeS (70-99 cols), sidebar hides when focus is on main/chat ✓
- Toggling sidebar: no explicit sidebar-toggle keybinding found
  - Switching focus to sidebar (press `1`) brings it back ✓
  - `h` key collapses sidebar nodes (not sidebar visibility)
- Result: PARTIAL (sidebar hides at S size; no explicit toggle keybinding)

**Below 80 columns: single-panel mode (Tab switches panels full-screen):**
- Spec: below 80 cols → single panel, Tab switches full-screen
- Actual: XS mode triggers at ≤69 cols (not 80)
  - At 29-col pane (60 col window split 2 ways), XS mode activates ✓
  - Only focused panel visible ✓
  - Tab switches panels full-screen ✓
- Threshold mismatch: spec says 80 cols, code uses 69 cols
- Result: PARTIAL (feature works at 69 cols threshold; spec says 80 cols)

**tmux compatibility: keybinding fallback for tmux-captured keys:**
- tmux captures `Ctrl-b` (prefix), `Ctrl-a`, and other keys
- The TUI has no explicit tmux-compatibility mode or fallback detection
- Alt/Meta keys via tmux send as ESC prefix sequences — some work, some don't
- No TERM_PROGRAM or TMUX env detection for keybinding adaptation
- Result: FAIL — not implemented

## Screen Captures

At 60-col total window (29-col pane), XS mode:
```
│ DASHBOARD                 │
│ Task Board                │
│ TODO (1) IN … (1) DONE    │
│ (0)                       │
│ ───────  ───────  ─────── │
│   Launc…  CI ha…          │
│ ─────── Inbox  2 ──────── │
│ ▸ Approve launch check…   │
```
Single panel only, Tab switches to chat or sidebar.

## Issues Filed

- Issue #172 — Responsive layout thresholds don't match spec (XS at 69 not 80; no sidebar-toggle keybinding; no tmux fallback mode)
