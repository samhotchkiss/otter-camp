# Test 010: Chat pane scope indicator and [/] navigation

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Scope indicator at top: shows available scope levels (task/project/org), active highlighted; Navigate scopes with `[`/`]` keys
**Tested:** 2026-02-26 16:28
**Result:** PARTIAL

## How I Tested

1. Focused chat pane (Alt-3) while on task session "Task 1 / Launch docs"
2. Observed chat header — shows "Task 1 / Launch docs · org"
3. Pressed `[` to navigate scope backward
4. Pressed `]` to navigate scope forward
5. Observed header changes

## TUI Screen State

Initial (org scope):
```
│ Task 1 / Launch docs  ·  org          │
```

After `[` (task scope):
```
│ session-task-current  ·  task         │
```

After `]` (org scope):
```
│ General / Frank  ·  org               │
```

## Observed Behavior

`[` and `]` DO navigate between scope levels. However:

1. **Scope indicator design doesn't match spec**: The spec says "shows available scope levels (task/project/org), active highlighted" — implying a persistent indicator like `[task] [project] [org]` with the active one highlighted. What's implemented is just the session name + current scope label in the header. No visual comparison of available levels.

2. **Project scope is skipped**: Navigating with `[`/`]` jumps between task and org scopes, skipping project scope. There may not be a project-level session configured.

3. **Raw session ID shown**: When switching to task scope, the header shows the raw session ID "session-task-current" instead of a human-readable label. The `sessionLabel()` lookup is not resolving this ID.

4. **`]` from org scope jumps to General/Frank**: Pressing `]` switched from the task context entirely to the org-level General/Frank session — this is confusing; it changed the active session, not just the scope perspective.

## Expected Behavior

Per spec: persistent scope indicator showing all levels (task/project/org) with active highlighted. The current implementation shows only the active scope inline.

## Notes

The `[` and `]` scope navigation is working in principle but has UX issues: the header shows raw IDs for some scopes, the "all levels visible" indicator is missing, and scope switching behavior is not clearly different from session switching.

## Issue Filed

Issue #165 — Scope indicator should show all levels; raw session IDs shown for task scope
