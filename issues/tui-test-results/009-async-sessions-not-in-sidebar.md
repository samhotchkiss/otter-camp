# Test 009: Per-node async sessions (work logs) do NOT appear in sidebar

**Section:** 21. TUI (Terminal UI)
**Functionality list item:** Per-node async sessions (work logs) do NOT appear in sidebar
**Tested:** 2026-02-26 16:26
**Result:** PASS

## How I Tested

1. Queried API for all chat sessions to understand how many exist
2. Compared API session count vs sidebar display count

## API Evidence

```json
{
  "organization/async": 17,
  "organization/sync": 1,
  "project/async": 5,
  "project/sync": 1,
  "project_task/async": 26
}
```
Total sessions: 50.

## TUI Screen State

Sidebar shows only:
```
│ SESSIONS        │
│ ─────────────── │
│   General / …   │  ← 1 org-level session (sync)
│ ▾ Project Al…   │  ← 1 project (Project Alpha)
│   › Task 1… ✓ ○ │  ← 1 task session
│   › Task 2 /… ◌ │  ← 1 task session
│                 │
```

4 items visible (2 task sessions + 1 org session + 1 project group) — not the 50 async sessions.

## Observed Behavior

The sidebar correctly filters to show only the meaningful interactive sessions (org sync session = "General / Frank", plus task sessions under their project). The 17 async org sessions, 26 async task sessions, and 5 async project sessions are all hidden from the sidebar. These are "work logs" — background/autonomous sessions that don't need to appear in the main navigation.

## Expected Behavior

Async sessions not in sidebar — ✓

## Notes

The workspace.go code builds the sidebar tree by looking at projects, tasks, and specifically the `chat-sessions` associated with tasks. The async sessions are properly excluded.

## Issue Filed

None.
