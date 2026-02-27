# Test 004: Sidebar — sessions grouped by scope

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Sessions grouped by scope: org at top, then projects with task sessions nested
**Tested:** 2026-02-26 16:17
**Result:** PARTIAL

## How I Tested

1. Focused sidebar with Alt-1
2. Captured sidebar content
3. Examined session hierarchy: org session vs project sessions vs task sessions

## TUI Screen State

```
│ SESSIONS        │
│ ─────────────── │
│   General … ✓   │   ← org-level session (truncated "General / Frank")
│ ▾ Project Al…   │   ← project node (expanded with ▾)
│   › Task 1 /… ○ │   ← task session nested under project
│   › Task 2 /… ◌ │   ← task session nested under project
│                 │
```

## Observed Behavior

Sidebar shows:
- Org-level session "General / Frank" at top ✓
- Project "Project Alpha" as an expandable tree node ✓
- Task sessions ("Task 1 / Launch docs", "Task 2 / CI hardening") nested under project ✓
- Correct nesting with `› ` prefix for task sessions ✓
- Project shown with ▾ (expanded) or ▸ (collapsed) indicator ✓

Labels are truncated with `…` at sidebar width (sidebar is ~19 chars wide at M size). The full labels are "General / Frank" and "Task 1 / Launch docs" etc.

The spec says "org at top, then projects with task sessions nested." This is working correctly. However the label truncation at M size is aggressive — "General / Frank" becomes "General …" which loses the agent name context.

## Expected Behavior

Correct hierarchy as observed. Truncation is unavoidable at M size but could be improved at L/XL.

## Notes

The `✓` badge on "General / Frank" correctly shows the active+cursor state. The `○` and `◌` status icons on task sessions are working (todo=○, in_progress=◌). Sidebar is 19 chars wide at M, making it quite cramped — all labels truncate.

## Issue Filed

None — this is correct behavior. Truncation at M is expected.
