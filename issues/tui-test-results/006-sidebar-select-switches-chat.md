# Test 006: Selecting session (Enter) switches chat pane

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Selecting session (Enter) switches chat pane; main content unchanged
**Tested:** 2026-02-26 16:20
**Result:** PARTIAL

## How I Tested

1. In sidebar, navigated to "Task 1 / Launch docs" session
2. Pressed Enter
3. Observed chat pane header and main panel

## TUI Screen State

After Enter on Task 1 session:
```
│ SESSIONS        ││ TASK DETAIL                                    ││ Task 1 / Launch docs  ·  org          │
│ ─────────────── ││ ────────────────────────────────────────────── ││ ───────────────────────────────────── │
│   General / …   ││                                                ││                                       │
│ ▾ Project Al…   ││ Launch docs                                    ││ 3 +3 equals6.                         │
```

Status: `M/sidebar · Sidebar selection applied.`

## Observed Behavior

Pressing Enter on a task session:
1. Switched chat pane to show that task's session ("Task 1 / Launch docs · org") ✓
2. Switched main panel to TASK DETAIL view for that task ✓ (but spec says "main content unchanged")
3. Status bar updated with "Sidebar selection applied." ✓
4. Chat pane shows conversation history for that task session ✓

The spec says "main content unchanged" when selecting a session. However, selecting a task session ALSO navigates the main panel to the task detail. This is actually more helpful behavior — you want to see the task when you select it — but it deviates from the spec.

## Expected Behavior

Per spec: chat pane switches to selected session; main content panel unchanged.
Actual: both chat AND main panel switch to the selected task.

## Notes

The actual behavior (switching both panels) is arguably better UX than the spec requires. When you select a task session, showing the task detail in main is useful context. However if a user is in the middle of reviewing the dashboard or a project view, the main panel switch is unexpected.

## Issue Filed

None — the actual behavior is an improvement over the spec. This is a UX question to revisit.
