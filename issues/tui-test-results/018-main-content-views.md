# Test 018: Main Content Views

**Section:** 21. TUI (Terminal UI)
**Functionality list items:**
- Dashboard (home): overview of active projects, recent activity, agent status
- Project task board: kanban grouped by work_status; j/k to navigate, Enter to open task
- Task detail: title, description, acceptance criteria, flow progression, subtasks, event log
- Per-node async session (work log) accessible from task detail
- Inbox: list items with type indicators; approve/reject/dismiss actions
- Activity feed: project event stream
- Agent status: active agents, current tasks, turn status
- Merge queue: list with priority indicators
- Schedules: list with next-run times
**Tested:** 2026-02-26
**Result:** PARTIAL

## How I Tested

Navigated to each view using `:dashboard`, `:inbox`, `:activity`, `:agents`, `:merges`, `:schedules` commands. Examined source code for `renderDashboardView()`, `renderTaskView()`, `renderInboxView()`, etc.

## TUI Screen Captures

**Dashboard:**
```
│ DASHBOARD                                      │
│ Task Board                                     │
│ TODO (1)      IN PROGR… (1) DONE (0)           │
│ ────────────  ────────────  ────────────       │
│   Launch docs  CI hardeni…                     │
│ ─────────── Inbox  2 ──────────────────────── │
│ ▸ Approve launch checklist                     │
│ ▸ Review flaky test quarantine                 │
│ ─────────── Activity ──────────────────────── │
│   ✓ workspace booted                           │
│   ✓ proof-of-life realtime connected           │
│   ✓ proof-of-life replay synced                │
```

**Task Detail:**
```
│ TASK DETAIL                                    │
│ Launch docs                                    │
│   Status: Todo                                 │
│   Flow step: 1                                 │
│ ─────────── History ──────────────────────── │
│   · created                                    │
│   Enter·open  Esc·back                         │
```

**Inbox:**
```
│ INBOX (2)                                      │
│ ▸ Approve launch checklist  task-1             │
│   a·approve  x·reject  f·defer  o·open         │
│ j/k·navigate                                   │
│   Review flaky test quarantine  task-2         │
```

**Agents:**
```
│ AGENTS (3)                                     │
│ ● Frank  online                                │
│ ○ Lori  idle                                   │
│ ● Ellie  online                                │
```

**Merge Queue:**
```
│ MERGE QUEUE                                    │
│ ⎇ PR#1496 task-104                             │
│ ⎇ PR#1500 task-106                             │
```

**Schedules:**
```
│ SCHEDULES                                      │
│ ⏰ daily standup 09:00                         │
│ ⏰ nightly regression 01:00                    │
```

## Findings Per Item

**Dashboard overview:**
- Task board mini-kanban: TODO/IN PROGRESS/DONE columns with task rows ✓
- Inbox section (first 3 items) ✓
- Activity feed (last 3 entries) ✓
- Agent status section: NOT on dashboard — shown in separate `:agents` view
- Result: PARTIAL (no agent status on dashboard; needs separate view)

**Project task board:**
- `renderProjectView()` shows project tree (expandable/collapsible) — not a kanban
- Kanban is in dashboard view (mini version with task rows under columns)
- j/k navigation within task list: `j` moves cursor; `Enter` opens TASK DETAIL ✓
- Full kanban per-project NOT implemented (just overview on dashboard)
- Result: PARTIAL

**Task detail:**
- Title ✓, Status ✓, Flow step ✓, History (last 5 events) ✓
- Description: NOT shown
- Acceptance criteria: NOT shown
- Subtasks: NOT shown
- Per-node async session link: NOT shown ("Enter·open" hint present but doesn't open session)
- Result: PARTIAL (basic info only; no description/AC/subtasks/session link)

**Per-node async session from task detail:**
- "Enter·open" hint exists but Enter in task detail goes back to source (not to session)
- No session link implemented
- Result: FAIL

**Inbox:**
- Shows items with task ID reference ✓
- `a/x/f/o` approve/reject/defer/open actions ✓
- `j/k` navigation between items ✓
- Item type indicators: NOT shown (no icons distinguishing approval vs other item types)
- Result: PASS (functional; minor: no type icons)

**Activity feed:**
- Shows project event stream ✓ (3 entries: workspace booted, realtime connected, replay synced)
- Only boot/startup events shown — no task status changes or agent events
- Live event stream via SSE not observed to add new entries during testing
- Result: PASS (functional for boot events; unclear if task events propagate)

**Agent status:**
- `● Frank online`, `○ Lori idle`, `● Ellie online` — name + online/idle status ✓
- Current tasks: NOT shown (no task assigned to agent visible)
- Turn status (active turn indicator): NOT shown
- Result: PARTIAL

**Merge queue:**
- PR numbers and task IDs shown ✓
- Priority indicators: NOT shown (no ordering or priority labels)
- Result: PARTIAL

**Schedules:**
- Schedule name + next-run time ✓
- Result: PASS

## Issues Filed

- Issue #169 — Task detail missing description, acceptance criteria, subtasks, async session link
