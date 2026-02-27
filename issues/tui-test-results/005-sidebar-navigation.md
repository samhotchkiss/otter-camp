# Test 005: Sidebar j/k navigation and l/h expand/collapse

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** j/k navigation; l/h expand/collapse project groups
**Tested:** 2026-02-26 16:19
**Result:** PASS

## How I Tested

1. Focused sidebar (Alt-1)
2. Pressed `j` twice to move cursor down through items
3. Pressed `k` to move cursor back up
4. Pressed `h` on Project Alpha to collapse it — task sessions disappeared, ▾ changed to ▸
5. Pressed `l` on Project Alpha to expand it — task sessions reappeared, ▸ changed to ▾

## TUI Screen State

After `h` (collapse):
```
│ ▸ Project Al…   │  ← collapsed (▸)
│                 │  ← task sessions hidden
```

After `l` (expand):
```
│ ▾ Project Al…   │  ← expanded (▾)
│   › Task 1 /… ○ │  ← task sessions visible
│   › Task 2 /… ◌ │
```

## Observed Behavior

- `j` moves cursor down through visible sidebar items ✓
- `k` moves cursor up ✓
- `h` on a project node collapses it (shows ▸), hiding child task sessions ✓
- `l` on a project node expands it (shows ▾), showing child task sessions ✓
- Arrow indicator changes correctly between ▸ and ▾ ✓

## Expected Behavior

As observed.

## Notes

Navigation feels snappy (<100ms response). The cursor highlight is visible in the actual terminal (ANSI colors don't appear in tmux capture). j/k work as expected for vim-style list navigation.

## Issue Filed

None.
