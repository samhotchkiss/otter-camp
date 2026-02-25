# Task 107: Main workspace views (sidebar, board, detail, inbox, activity)

Layer: L2
Effort: L
Depends on: 103, 105

## Context

Doc 17 requires a three-panel operations workspace where the main panel covers dashboard, task board/detail, inbox, activity feed, agent status, merge queue, and schedules; sidebar provides session/project navigation with unread propagation.

## Required Fix

Implement the main operational views and navigation:

- Sidebar:
  - org session pinned first (`General` / Frank)
  - project groups with nested task sessions
  - unread markers on session and bubbled project level
  - expand/collapse with `h/l`, select with `Enter`
- Main views:
  - dashboard
  - task board (queue columns + counts)
  - task detail (flow stepper, subtasks/dependencies/history sections)
  - inbox (approve/reject/defer/open-in-context)
  - activity feed
  - agent status
  - merge queue
  - schedules (read-only acceptable)
- Keyboard behavior per spec (`j/k`, `h/l`, `Enter`, `Escape`, `r`, `g`, `G`).
- Command palette navigation commands:
  - `:dashboard`, `:project`, `:task`, `:inbox`, `:activity`, `:agents`, `:merges`, `:schedules`
- Ensure chat and main remain independently navigable/synchronized by context actions (e.g., inbox `open in context`).

## Acceptance Criteria

- [ ] All required main views are reachable by keyboard and command palette
- [ ] Sidebar unread indicators update correctly from realtime events
- [ ] Inbox actions mutate state and reflect updates across board/detail/activity
- [ ] Open-in-context jumps to corresponding task/session without desync
- [ ] Task board/detail updates live on `task.status.changed` and `task.flow.advanced`
- [ ] `go build ./...` passes

## Required Tests

- Unit: sidebar unread propagation and tree navigation logic
- Unit: command palette route resolution tests for view commands
- Golden render: snapshots for each primary view in at least M and L size classes
- Integration: inbox action updates board/detail/activity consistently
- End-to-end: keyboard-only navigation across sidebar/main/chat with open-in-context flow
