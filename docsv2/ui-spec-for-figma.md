---
## Summary

This document is the UI design specification for OtterCamp V2, a chat-primary agent orchestration platform where a single human operator manages projects, tasks, and AI agents. The core layout is a persistent three-panel design: a session sidebar (left) for navigating between conversations, a main content area (center) for dashboards/projects/task details, and an always-visible chat pane (right) that serves as the primary interaction surface. Chat is not a secondary feature -- it is how the human directs all work. There are no direct UI creation paths; everything (task creation, flow editing, scheduling) happens through conversation with agents.

The session sidebar organizes conversations hierarchically by scope: org-level (talk to Frank, the Chief of Staff), project-level (talk to a PM agent), and task-level (sync sessions for individual tasks). A scope pill at the top of the chat pane lets the user zoom between Task, Project, and Org scope without changing the main content view. Unread indicators bubble up from tasks to projects. Per-node async sessions (agent work logs) are deliberately excluded from the sidebar and only accessible from task detail views, maintaining the distinction between sync sessions (human-agent conversation) and async sessions (agent execution traces).

The main content area features several key views: a Dashboard with action items, project status, and a real-time activity feed; a Project View with a kanban-style task board (columns by work status), flow visualization, merge queue, and scheduled tasks; and a Task Detail view with progressive disclosure across three levels (board card, detail with flow stepper and subtask list, and per-node work logs). The Inbox is a separate action-required queue distinct from notifications -- every inbox item blocks progress somewhere until the human acts, with item types including task scoping reviews, work reviews, draft action reviews, escalations, and capability approvals. The activity feed provides real-time "proof of life" showing agent work, task transitions, merges, blockers, and memory system events via a live event bus.

The chat pane supports rich content rendering (markdown, syntax-highlighted code, inline images, file references, artifacts), file upload via drag-and-drop or attachment button, a message queue with Edit/Steer/Delete controls for messages sent while an agent turn is in progress, lightweight reactions (double-click for thumbs-up, right-click for thumbs-down with optional feedback), and memory attribution indicators showing when agent responses drew on injected memories. Settings includes four observability dashboards (overview, usage, performance, agents); the Usage Explorer is the primary cost visibility tool, showing total tokens over time as a stacked area chart with five interactive drill-down dimensions (purpose, project, model, agent, provider) — clicking any chart segment filters the page, and filters compose across dimensions. Design principles emphasize: chat-primary interaction, real-time streaming by default, dark mode, keyboard-driven navigation (Superhuman-style command bar), operator-focused single-user design, and progressive disclosure to avoid overwhelming the user while keeping everything accessible.

---

# UI Specification for Figma

This document captures all UI decisions made during the V2 chat spec process. It is intended as a design brief for building mockups and prototypes.

## Overall Layout

Three-panel layout. Chat is always visible — it is the primary interface, not a secondary feature.

```
┌──────────────────┬───────────────────────────┬────────────────────────┐
│                  │                           │                        │
│  Session Sidebar │     Main Content           │    Chat Pane           │
│  (left)          │     (center)               │    (right, persistent) │
│                  │                           │                        │
│  Navigation +    │  Dashboard / Project /     │  Active session,       │
│  session list    │  Task Board / Task Detail  │  always live           │
│  with unread     │  / Settings / etc.         │                        │
│  indicators      │                           │                        │
│                  │                           │                        │
└──────────────────┴───────────────────────────┴────────────────────────┘
```

- **Session sidebar**: the human's workspace navigator. Lists all sessions they participate in. Also hosts the notification center.
- **Main content**: context-dependent. Changes based on navigation.
- **Chat pane**: always present. Shows the active chat session. Changes based on context or explicit user action.

## Session Sidebar

### Structure

Sessions are grouped by scope, with org at the top, then projects, each containing their task sessions:

```
🔔 Notifications

General                          ← org session (talk to Frank)
─────────────────────────────
OtterCamp V2                     ← project session (talk to PM)
  Define auth architecture       ← task sync session
  Implement auth             •   ← task sync session (unread)
  Write chat spec                ← task sync session
─────────────────────────────
Client Portal                    ← project session (talk to PM)
  Fix login bug              •   ← task sync session (unread)
  Plan Sprint 3                  ← task sync session
```

### Behaviors

- Every entry is one session. No ambiguity.
- **Unread indicators** (dots) on sessions with unseen messages. Bubble up: if any task in a project has unread, the project group shows an indicator too.
- **Clicking a session** switches the chat pane to that session. Main content does NOT change. This is for "jump to something else."
- Active/highlighted state shows which session is currently loaded in the chat pane.
- Collapsed by default for inactive projects, expanded for active ones.
- Per-node async sessions (agent work logs) do NOT appear in the sidebar. They are viewable from task detail in the main content.

## Chat Pane

### Scope Pill

At the top of the chat pane, a segmented control (pill) lets the human zoom in/out of scope without changing main content:

```
Viewing a task:     [Task] [Project] [Org]    ← all three segments
Viewing a project:         [Project] [Org]    ← two segments
Viewing dashboard:                   [Org]    ← org only
```

- Clicking a segment switches the chat pane to the session at that scope level.
- The active segment is visually highlighted.
- Main content does not change when the pill is clicked.
- The pill labels could show the specific name: [Implement Auth] [OtterCamp V2] [General]

### Chat Content Area

Messages displayed in a conversation thread:

- **Human messages**: standard chat bubble, right-aligned or distinct styling.
- **Agent messages**: left-aligned, with agent avatar/name. Agent text streams in real-time (tokens appear as they arrive).
- **Tool calls**: shown inline during the agent's response. Compact representation: tool name + arguments. Expandable to show full details.
- **Tool results**: shown as a status indicator (success/error) below the tool call. Large results are collapsed by default with "Show more."
- **System messages**: subtle, centered or de-emphasized. Session start, context compression notices, limit reached notices.
- **Interjection messages**: from a different agent than the default responder. Should be visually distinct (different agent avatar, possibly a subtle label: "Lori interjected").

### Rich Content Rendering

Messages can contain multiple content blocks. The chat pane renders each block type appropriately:

- **Text blocks**: rendered as markdown (headings, bold, italic, lists, links, etc.).
- **Code blocks**: syntax-highlighted with language tag. Copy button. Potentially collapsible for long snippets.
- **Image blocks**: rendered inline at appropriate size. Click to expand/zoom. Lightbox for full resolution.
- **File reference blocks**: rendered as a compact card showing filename, path, and repo context. Click to open file preview or navigate to the file in the project view. Shows diff if referencing a specific commit.
- **Artifact blocks**: rendered as a downloadable/previewable embed. File icon, name, size. Click to preview (if previewable) or download.

### File Upload

- **Drag-and-drop** files into the chat pane to attach to the current message.
- **Attach button** (paperclip icon) in the message input area for file picker.
- Uploading files shows a progress indicator and preview thumbnail (for images) or file icon (for other types).
- Multiple files can be attached to a single message.
- Uploaded files appear as artifact blocks in the message once sent.

### Message States (Visual)

- **Pending** (queued human messages): shown in the chat with a visual indicator that they haven't been processed yet. Editable. Each has a **Steer** button.
- **Streaming**: agent text appearing token by token. Animated cursor or typing indicator at the end.
- **Final**: complete message, static.
- **Failed**: error styling, with context about what went wrong.
- **Redacted**: placeholder text ("Message redacted") in place of content.

### Message Queue UI

When the human sends messages while an agent turn is in progress:

```
┌─────────────────────────────────────────────┐
│ Agent: "Looking at the auth module..."      │
│   ↳ read_file(src/auth/handler.go) ✓        │
│   ↳ read_file(src/auth/middleware.go) ✓      │
│   ↳ search_code("validateToken") ...        │ ← still running
│                                              │
│ ─── queued ──────────────────────────────── │
│ You: "What about the error handling?"        │
│                          [Edit] [Steer ▶]   │
│ You: "Also check the logging setup"          │
│                          [Edit] [Steer ▶]   │
└─────────────────────────────────────────────┘
```

- Queued messages appear below a "queued" divider.
- Each has **Edit** (inline editing) and **Steer** (cancel current turn, promote this message) buttons.
- Steer button should convey "this will interrupt the agent and process your message now."
- Messages can also be deleted from the queue.
- Edit/Steer/Delete controls only appear on your own messages (multi-human sessions).

### Cancel Button

While an agent turn is in progress, a **Cancel** button is visible (Escape as keyboard shortcut). Cancels the current turn without injecting any message.

### Reactions

Messages support lightweight reactions (positive/negative sentiment). Both humans and agents can react.

**Interaction:**
- **Double-click** a message to thumbs-up (positive reaction). Quick, low-friction for the common case.
- **Right-click** a message for a context menu: thumbs-down (negative reaction), leave feedback (negative + note). This is for the less common case where something needs correction or context.

**Visual treatment:**
- Reactions shown as small indicators below the message (thumbs up/down icon + count if multiple humans reacted).
- Agent reactions on human messages shown subtly — small avatar + indicator below the message.
- A message with negative feedback + note could show a subtle flag icon, expandable to see the note.

### Memory Attribution

When an agent's response was informed by injected memories (passive injection or memory.query tool), a subtle indicator appears on the message. This is essential for the memory feedback loop — humans need to know a response drew on memory so their reactions (positive/negative) are meaningful signals back to the memory system.

**Visual treatment:**
- A small "memory" indicator (e.g., a subtle brain icon or "via memory" label) near the message, expandable to show which memories contributed, their confidence, and sources.
- Memory.query tool calls render in the tool call stream like any other tool: `memory.query("deployment conventions")` → results (collapsed by default, expandable).

**Ellie's memory results:**
When Ellie responds to a memory query ("What do we know about X?"), results are formatted as a structured list rather than plain prose:
- Each result shows: memory content, confidence indicator (high/medium/low), source (conversation, file, import), and age.
- Entity definition results are highlighted as authoritative.
- Supersession history ("What did we believe before?") renders as a timeline: newest → oldest, with each step showing what changed and when.

### Active Turn Indicator

While the agent is working (turn in progress), the chat pane should show:
- Which agent is responding (avatar + name)
- That work is in progress (spinner or animation)
- Tool calls as they happen (streaming list of tool activity)
- Elapsed time (optional, subtle)

## Notification Center

Accessible from the sidebar (bell icon or "Notifications" link at top).

### Notification List

- Reverse chronological order.
- Each notification shows: urgency indicator, title, brief description, timestamp, source (which project/task/session).
- Clicking a notification navigates to the relevant context: main content updates to the project/task, chat pane loads the appropriate session.
- Unread/read state per notification.
- Dismiss individual notifications or mark all as read.

### Urgency Visual Treatment

- **Urgent** (escalations, failures, blockers needing human judgment): prominent styling, possibly a different color or icon. These should be impossible to miss.
- **Normal** (reviews ready, drafts pending, @mentions): standard styling.
- **Low** (task status changes, agent started work): subtle, possibly grouped/batched ("3 tasks updated in OtterCamp V2").

## Main Content Areas

### Dashboard

The landing page. Shows:
- Action items: things needing the human's attention (from inbox + notifications)
- Project status summary: each project with task counts by status
- **Activity feed**: real-time stream of significant events across all projects (see Activity Feed below)
- Quick navigation to active projects/tasks

### Project View

- Project overview: description, team (staff agents assigned), progress summary
- **Task board**: kanban-style columns by work status (draft, queued, in_progress, blocked, on_hold, review, done). This is the primary project view. Cancelled tasks are not shown on the board by default — they are filtered out but accessible via a "Show cancelled" toggle or filter.
- Task list: alternative flat list view with sorting/filtering
- **Activity feed**: project-scoped real-time stream of events (see Activity Feed below)
- **Flow visualization**: read-only view of the project's flow template(s), showing nodes and edges. Editing flows happens conversationally through the PM, not through the UI.
- **Merge queue**: list of tasks awaiting merge to `main`, showing merge status (queued, merging, conflict). Visible in the project view.

#### Task Board Cards

Each card on the kanban board shows at-a-glance information without overwhelming detail:

```
┌─────────────────────────────────┐
│ Build Auth System               │
│ [■ ■ ● □ □]  ← flow progress   │
│ 🔶 High   @WorkerBot           │
│ Implement: 2/3 subtasks done   │
└─────────────────────────────────┘
```

- **Task title**
- **Flow progress bar**: small step indicator showing how far through the flow. Completed nodes filled, active node highlighted, blocked nodes in amber/orange.
- **Priority indicator** and **assignee avatar**
- **Subtask summary** (if current node has subtasks): compact progress line showing node name and subtask completion count

### Task Detail

Three levels of progressive disclosure: board → detail → work log.

#### Header

- Task title, status badge, priority, assignee

#### Flow Stepper

Horizontal step indicator across the top of the detail view. Each step represents a flow node:

```
[✓ Design] → [● Implement] → [○ Code Review] → [○ Final Review]
                  ↑ current
              3 subtasks (2 done, 1 in progress)
```

- Completed nodes: checkmark, muted/green
- Active node: highlighted, prominent. If the node has subtasks, show a summary count below (e.g., "3 subtasks: 2 done, 1 in progress").
- Blocked node: amber/orange indicator with blocker task link
- Pending nodes: empty/outlined

#### Subtask List (Per Node)

When viewing a node that has subtasks, show the subtask list below the flow stepper:

```
[● Implement] — 3 subtasks
─────────────────────────────────────────
✓  Auth middleware                  done
●  Token validation            in_progress
○  Session management    queued (depends on Auth middleware)
```

Each subtask shows: title, status, assignee, dependency info if blocked/waiting. Clicking a subtask could expand inline detail or navigate to a subtask detail view.

#### Body

- Task metadata: description, acceptance criteria
- **Branch info**: shows the task branch name (`task/<slug>`), link to view diff against `main`. At review nodes, the diff is the primary review surface.
- **Dependencies**: "depends on" and "blocks" lists at the task level. Each entry shows the linked task's title and status.
- **Artifacts**: files, screenshots, outputs produced during the task
- **History**: rendered from the `project_task_event` audit trail — status transitions, flow advancements, rejections, blocker filings, escalations, dependency changes, merges. Each entry shows actor, timestamp, and comment if present. Collapsed by default.

#### Review Node View

When a task is at a review node, the task detail emphasizes the review surface:

- Diff view: task branch vs `main`, showing all changes made during the work node(s) leading up to this review
- Reviewer actions: Approve / Reject with feedback
- Subtask summary from the preceding work node (what was done)
- Link to work log for detailed execution trace

#### Work Log (Per Flow Step)

Each completed or active flow step has a collapsible "View work log" link. Expanding it shows the agent's async execution trace for that step — tool calls, artifacts produced, time spent. Read-only. This is the deepest level of detail and is only shown on demand. For nodes with subtasks, work logs exist per subtask.

### Inbox

The human's action-required queue. Separate from notifications (which are awareness). Every item blocks progress somewhere until the human acts.

#### Layout

Two sections: active items (pending) and deferred items. Active is the default view.

```
INBOX (3)
─────────────────────────────────────────────────────
⚡ Escalation: Auth conflict              OtterCamp V2
   Frank escalated — needs your call on token format
   [Open in Context] [Defer]

📋 Task Scoping: Landing page design      Client Portal
   PM finished scoping — ready for your review
   [Approve] [Request Changes] [Defer]

✉️ Draft Review: Welcome email             Client Portal
   Agent composed email under draft policy
   [Approve] [Edit] [Reject] [Defer]

DEFERRED (2)
─────────────────────────────────────────────────────
📋 Task Scoping: Blog post series         OtterCamp V2
   Deferred 3 days ago
   [Restore]

📋 Task Scoping: API docs rewrite         Client Portal
   Deferred 1 week ago
   [Restore]
```

#### Item Types and Actions

- **Task scoping review**: PM finished scoping a `requires_human_review` draft. Actions: Approve (→ queued), Request changes, Defer.
- **Task work review**: task reached a review node with `human` actor type. Actions: Approve (→ advance flow), Reject with feedback (→ back to work), Defer.
- **Draft action review**: communication tool staged a draft for review (tool behavior, not policy). Actions: Approve (execute), Edit then approve, Reject, Defer.
- **Escalation**: PM or Frank escalated a blocker. Actions: Open in context (jumps to chat), Decide inline, Defer.
- **Capability approval**: agent needs human pre-authorization. Actions: Approve, Approve with constraints, Deny, Defer.

#### Item Content

Each item carries enough context to decide without leaving the inbox:
- Item type indicator and title
- Source project/task
- Who/what created it and when
- Relevant context: task description, diff preview, agent reasoning, staged action payload
- "Open in context" link to navigate to the full picture (main content updates to the relevant task/project, chat pane loads the relevant session)

#### Behaviors

- Ordered by urgency then arrival time. Escalations and capability approvals sort above reviews and drafts.
- Acting on an item triggers the downstream action immediately — no additional confirmation. The inbox itself is the confirmation step.
- Defer moves the item to the deferred section. Restore brings it back to active.
- No expiry. Deferred items persist indefinitely. Agents nudge via chat if something is stale.

**Note on task creation:** tasks are never created directly through the UI. The human describes what they want via chat, and agents create tasks. This reinforces chat as the primary interface.

### Activity Feed

The activity feed is a real-time stream of what the system is doing. It is the human's "proof of life" — visible confirmation that agents are working, tasks are progressing, and the system is alive. This was a core component of V1 and remains essential in V2.

#### Content

The feed shows significant events in reverse chronological order:

```
2 min ago   ✓ Subtask "Token validation" completed        Build Auth System
5 min ago   → Task "Fix login bug" merged to main          Client Portal
8 min ago   🔶 Blocker filed: "Missing API credentials"    Build Auth System
12 min ago  ● Agent started work on "Session management"   Build Auth System
15 min ago  ✓ Task "Update README" completed               Client Portal
20 min ago  📋 PM scoped "Landing page design"             Client Portal
```

Each entry shows: timestamp, event type indicator, description, and source project/task.

#### Event Types

- Task status transitions (queued → in_progress, in_progress → review, review → done)
- Subtask completions
- Flow node advancements
- Blocker filings and resolutions
- Merge queue events (merge started, merge succeeded, merge conflict)
- Agent assignments and handoffs
- Escalations
- Memory system events (import completed, entity synthesis run, reflection completed) — shown at Low urgency, grouped when multiple occur together

#### Scoping

- **Dashboard**: feed shows events across all projects
- **Project view**: feed shows events for that project only
- Clicking any feed entry navigates to the relevant task/context

#### Behavior

- Real-time updates via the event bus — new events appear at the top as they happen
- No polling; events stream in live
- Subtle animation for new entries to draw attention without being distracting
- Collapsible/expandable — can be minimized if the human is focused on something else

### Scheduled Tasks

Schedules are visible in both project view and a global view. Read-only — editing happens through conversation with the PM.

#### Project View — Schedules Tab

```
Schedules                                              [Active: 3  Paused: 1]

Name                    Cadence              Last Run        Next Run       Status
─────────────────────────────────────────────────────────────────────────────────
Check inbox             Every 10 min         2 min ago ✓     8 min         Active
Draft weekly roundup    Mon 9:00 AM          3 days ago ✓    4 days        Active
Generate metrics        Daily 6:00 AM        18 hours ago ✓  6 hours       Active
Competitor scan         Wed 2:00 PM          5 days ago      —             Paused
```

Each row shows: schedule name, human-readable cadence, last run timestamp with status indicator (succeeded/failed/cancelled), next scheduled run, and active/paused status.

Clicking a row expands to show recent instances (last 5-10 tasks created by this schedule) with their status and duration.

#### Global Schedules View

Same table format, but aggregated across all projects. Grouped by project. Accessible from the sidebar or dashboard.

#### Activity Feed Integration

Schedule-created tasks appear in the activity feed like any other task. The feed entry indicates the schedule origin:

```
2 min ago   ● Scheduled: "Check inbox" started              Personal Operations
```

## Design Principles

- **Chat is primary**: the chat pane is always visible and is the main way humans interact with OtterCamp. The main content area is for viewing and navigating; the chat pane is for doing.
- **Real-time by default**: streaming agent responses, live tool call activity, unread indicators updating instantly.
- **Dark mode**: default theme.
- **Keyboard-driven**: command bar (Superhuman-style) for fast navigation. Escape to cancel. Shortcuts for common actions.
- **Operator-focused**: designed for the human running the operation, not for a team. One person's view of their projects, agents, and work.
- **Progressive disclosure**: tool call details collapsed by default, work logs expandable, large tool results behind "show more." Don't overwhelm, but make everything accessible.

## Interaction Patterns

### Scope Switching (Zoom In/Out)

The scope pill at the top of the chat pane is the primary mechanism for zooming between scope levels:

```
Looking at a task, want to ask Frank something:
  → Click [Org] on the pill
  → Chat pane switches to General (Frank)
  → Frank's prompt includes viewing context (which task the human was looking at)
  → Human asks their question
  → Click [Task] on the pill to return

Looking at a task, want to discuss the project:
  → Click [Project] on the pill
  → Chat pane switches to the project session (PM)
  → Human discusses project-level concern
  → Click [Task] to return
```

### Jumping to a Different Context

The session sidebar is for switching to a different project/task entirely:

```
Working on Task A in Project X, notification about Task B in Project Y:
  → Click Task B in the sidebar (or click the notification)
  → Chat pane switches to Task B's session
  → Main content stays on Task A (sidebar click) or navigates to Task B (notification click)
  → When done, click Task A in the sidebar to return
```

### Reviewing Agent Work

From the task detail in the main content:

```
Task: Implement Auth
├─ Flow Step 1: Write Code (done)
│   └─ [View work log]        ← expand to see the agent's async session
│       ├─ tool_call: read_file(...)
│       ├─ tool_call: write_file(...)
│       ├─ tool_call: run_tests(...)
│       └─ 47 tool calls, 12 min, 45k tokens
├─ Flow Step 2: Code Review (in progress)
│   └─ [View work log]
└─ Flow Step 3: Final Review (pending)
```

The work log is read-only — it shows what the agent did in its per-node async session. The human discusses the work in the task's sync session (chat pane), not in the work log.

## Settings: Observability Dashboards

Accessed from the main navigation (sidebar or top nav). Four dashboards live in the Settings/Observability section of the main content area (see 13-security-observability-costs.md for the full specification). The chat pane remains visible — the operator can ask agents questions about usage while viewing the data.

### Usage Explorer

The primary cost visibility tool. Designed for interactive exploration, not just summary display.

```
┌─────────────────────────────────────────────────────────────────────┐
│  USAGE                                                    [7d] [30d] [90d] │
│                                                                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────────────────┐               │
│  │ 2.4M     │  │ 68%      │  │ ⚠ Spike detected     │               │
│  │ tokens   │  │ of budget │  │ 3.2x avg (2h ago)    │               │
│  │ today    │  │ (monthly) │  │                      │               │
│  └──────────┘  └──────────┘  └──────────────────────┘               │
│                                                                       │
│  Group by: [Purpose] [Project] [Model] [Agent] [Provider]           │
│  ┌───────────────────────────────────────────────────────────┐      │
│  │                                                           │      │
│  │  ████████████████████████████████  ← agent turns          │      │
│  │  ████████████████                  ← summarization        │      │
│  │  ██████████                        ← memory extraction    │      │
│  │  ████                              ← memory synthesis     │      │
│  │  ██                                ← listening eval       │      │
│  │  ·····                             ← other                │      │
│  │  |---------|---------|---------|                           │      │
│  │  Feb 1     Feb 8     Feb 15    Feb 22                     │      │
│  │                                                           │      │
│  └───────────────────────────────────────────────────────────┘      │
│  ↑ Click any segment to filter. Filters compose across dimensions.  │
│                                                                       │
│  TOP CONSUMERS (this period)                                         │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │  Agent            Tokens       Trend        % of total   │       │
│  │  ─────────────────────────────────────────────────────── │       │
│  │  Frank            842K         ▁▂▃▅▇        35%          │       │
│  │  Lori             614K         ▂▂▃▃▄        26%          │       │
│  │  build-worker-3   412K         ▁▁▁▇▇        17%          │       │
│  │  Ellie            289K         ▂▂▂▂▂        12%          │       │
│  └──────────────────────────────────────────────────────────┘       │
│                                                                       │
│  PROJECTION: at current rate, ~7.1M tokens by end of month (budget: 10M) │
└─────────────────────────────────────────────────────────────────────┘
```

**Key interactions:**
- **Dimension toggle** (Purpose / Project / Model / Agent / Provider): changes the stacking axis of the time series chart. Default: Purpose.
- **Click a chart segment**: filters the entire page to that segment. E.g., click "agent turns" then switch to "by Project" to see which projects drive agent turn costs. Active filters shown as removable chips above the chart.
- **Time range**: 7d / 30d / 90d toggles. 7d reads from raw `model_invocation` records (more granular). 30d/90d read from pre-aggregated `model_usage_rollup`.
- **Top consumers table**: ranked by tokens in the current budget period. Sparkline shows the 7-day trend at a glance.
- **Budget gauges**: summary bar at top shows current period usage against budget, with soft/hard limit indicators.

### Overview Dashboard

- Current active runs and their status
- Today's token usage vs budget (bar chart)
- Error rate trend (24 hours)
- Approval queue depth and oldest pending item
- Agent activity feed (last 50 events)

### Performance Dashboard

- API latency percentiles (line chart, 24-hour view)
- Model latency by provider (line chart)
- Queue depth over time (area chart)
- Memory retrieval latency (line chart)
- Error rate by component (stacked area chart)

### Agents Dashboard

- Per-agent activity: runs completed, runs failed, tokens consumed
- Agent utilization timeline (gantt-style: active, idle, blocked, waiting for approval)
- Per-agent error rate

## Resolved Layout Decisions (from docs 17, 18)

- **Chat pane is resizable.** Operator drags the left edge to adjust width. Minimum width enforced. Width persisted in local storage.
- **Session sidebar is collapsible.** Defaults to expanded. Collapses to icon-only (showing session icons without labels) via keyboard shortcut `Cmd-Shift-C` (web) or a toggle key (TUI). In collapsed state, unread indicators remain visible as dots on session icons.
- **Notification center is a sidebar overlay.** Opened via bell icon at the top of the sidebar. Slides in as an overlay panel over the sidebar — NOT a separate page. Dismissed to return to the session list. Operator can view the current main content while the notification center is open.
- **Command bar (Cmd-K).** Centered full-overlay. Fuzzy search across projects, tasks, agents, sessions, flow templates. Results grouped by entity type with icons. Recent searches and frequently accessed items shown when query is empty. Arrow-key navigation, Enter to select, Escape to dismiss. Results update as operator types (no submit). Quick actions: navigate ("Go to OC-42"), switch session ("Talk to Frank"), filter ("Show blocked tasks in OtterCamp V2"), system ("Toggle dark mode"). See Command Bar section below.
- **Mobile-responsive.** Three-panel layout degrades to swappable panels on narrow screens (phone). On tablet widths, three-panel layout is preserved. Chat pane becomes a swappable view (not hidden) on narrow screens. See Mobile section below.
- **Agent avatars.** Each agent has a consistent accent color and initial-based avatar (colored circle with initials). Uploaded photos are supported but not required. Visual distinction between agents in multi-agent conversations via avatar + name label. The starter trio (Frank, Lori, Ellie) should have recognizable visual identities in the design system.
- **Work log visualization: collapsible tree.** Tool calls organized as a tree: each top-level item is a tool call with collapsible result detail. Large results (>100 lines) show a "Show more" expansion. Artifacts shown as downloadable items. Time/token summary at the top of each work log section.
- **Transitions and animations.** Scope pill switching: subtle cross-fade of chat content (not a slide). Activity feed new items: slide-in from top with a brief highlight fade. Sidebar unread indicators: instant update (no animation). Goal: functional, not decorative.

## Resolved Layout Questions (from doc 18)

The following open questions from the original spec are now resolved:

| Question | Resolution |
|---|---|
| Exact proportions of three panels | Sidebar ~220px, main content flexible, chat pane ~380px default. Both resizable. |
| Chat pane resizable? | Yes. Operator drags left edge. Min width ~280px. Width persisted in local storage. |
| Session sidebar collapsible? | Yes. Collapses to icon-only (~56px). `Cmd-Shift-C` toggle. Default: expanded. |
| Notification center: overlay or page? | Sidebar overlay panel. Not a separate page. |
| Command bar design? | Superhuman-style centered overlay. Cmd-K / Ctrl-K. See Command Bar section. |
| Mobile-responsive? | Three panels → swappable views on mobile. Tablet gets three-panel layout. |

## Command Bar

Keyboard shortcut `Cmd-K` (Mac) / `Ctrl-K` (other). Superhuman-style command palette.

### Visual Design

- Centered on screen, overlaying current content with a backdrop.
- Width: ~640px. Rounded corners. Shadow.
- Input field at the top. Results below.
- Grouped results: each group has a label ("Projects", "Tasks", "Agents", "Sessions"). Max ~3 items per group before "Show more."
- Keyboard focus stays in the input field. Arrow keys navigate the results list.
- Selected result highlighted. Enter to activate.

### Empty State

When opened with no query, shows:
- Recently accessed items (last ~5 items across all types)
- Quick action shortcuts: "Dashboard", "Inbox", "Talk to Frank"

### Result Types

- **Projects**: project name, icon (project color/avatar)
- **Tasks**: "OC-42: Build Auth System" — task number + title, project name, status badge
- **Agents**: agent name + role, status indicator
- **Sessions**: session scope path ("OtterCamp V2 > OC-42")
- **Flow templates**: template name

### Quick Actions (typed commands)

- Navigate: "dashboard", "inbox", "settings", "observability"
- Switch session: "talk to [agent name]", "[project name] session", "[task number] session"
- Filter tasks: "blocked tasks", "tasks in review", "show [project name]"
- System: "dark mode", "light mode", "collapse sidebar", "focus chat"

## Keyboard Shortcuts

All shortcuts discoverable via command bar (type "?" or "keyboard shortcuts").

| Shortcut (Mac) | Shortcut (Other) | Action |
|---|---|---|
| `Cmd-K` | `Ctrl-K` | Open command bar |
| `Escape` | `Escape` | Close command bar / Cancel agent turn / Dismiss overlay |
| `Cmd-Enter` | `Ctrl-Enter` | Send message in chat pane |
| `Cmd-Shift-C` | `Ctrl-Shift-C` | Toggle sidebar collapsed/expanded |
| `Cmd-1` through `Cmd-9` | `Ctrl-1` through `Ctrl-9` | Switch to session by position in sidebar |
| `Cmd-[` | `Ctrl-[` | Scope pill: navigate left (zoom out) |
| `Cmd-]` | `Ctrl-]` | Scope pill: navigate right (zoom in) |
| `Cmd-I` | `Ctrl-I` | Focus inbox |
| `Cmd-D` | `Ctrl-D` | Focus dashboard |
| `/` | `/` | Focus chat input (when chat pane is not focused) |

TUI equivalent shortcuts (from doc 17):
- `[` / `]` — scope pill left/right
- `e`, `s`, `d` — edit/steer/delete queued messages
- Vim-style navigation: `j`/`k` for up/down, `g`/`G` for top/bottom

## Sidebar Details

### Collapse Behavior

- **Expanded** (default): full session list with names, unread dots, project groupings.
- **Collapsed** (icon-only): ~56px wide. Each session is represented by an icon (agent avatar or project color circle). Unread indicators visible as dots. Hovering an icon shows a tooltip with the session name. Notification bell icon still visible.
- Transition: instant (no animation).

### Notification Center Overlay

- Triggered by bell icon at top of sidebar.
- Slides in as an overlay panel over the sidebar. Main content and chat pane remain visible behind it.
- Width: same as expanded sidebar (~220px).
- Dismiss via X button or clicking outside the overlay.
- Shows: urgency-tiered list (urgent/normal/low). Low-urgency notifications grouped ("3 tasks updated in OtterCamp V2"). Each item is clickable to navigate to context.
- Unread badge on bell icon reflects count of unread urgent + normal notifications.

## Task Board Cards (Detail)

Each card on the kanban board (compact, dense design):

```
┌─────────────────────────────────┐
│ OC-42: Build Auth System        │
│ [■ ■ ● □ □]                     │  ← flow progress (5 nodes)
│ 🔶 High  @Maven   [deploying]   │  ← priority, assignee, deploy badge
│ Implement: 2/3 subtasks         │  ← subtask progress (active node only)
└─────────────────────────────────┘
```

- **Flow progress bar**: filled dots for completed nodes, highlighted dot for active node, empty dots for pending nodes. Amber/orange dot for blocked node.
- **Deploy badge**: shown on deploy tasks (from doc 03a). Green "deploying" or "deployed" pill.
- **Remote push status**: shown when applicable — "push ok" (green) or "push failed" (red) micro-badge.
- **Subtask progress**: only shown for the currently active node if it has subtasks.
- Cards are not draggable (no drag-to-reorder). Task status changes through agents.
- Clicking a card opens the task detail in main content.

## Project View Sub-Views (New)

### Schedules Tab

Read-only. Editing schedules happens through conversation with the PM.

```
Schedules                                          [Active: 3  Paused: 1]

Name                    Cadence              Last Run        Next Run       Status
─────────────────────────────────────────────────────────────────────────────────
Check inbox             Every 10 min         2 min ago ✓     8 min         Active
Draft weekly roundup    Mon 9:00 AM          3 days ago ✓    4 days        Active
Generate metrics        Daily 6:00 AM        18h ago ✓       6 hours       Active
Competitor scan         Wed 2:00 PM          5 days ago      —             Paused
```

- Last Run shows: timestamp + status icon (✓ for success, ✗ for failure, — for never run).
- Status column: "Active" (green) or "Paused" (amber/gray).
- Clicking a row expands inline to show the last 5-10 task instances created by this schedule: task title, status badge, duration.

### Environments Tab

Shown only for projects with environments configured (doc 03a).

```
Environments

Name          Deployed Commit    Deployed At         Deploy Task
----------------------------------------------------------------
staging       abc123d            2h ago              OC-247
production    def456a            1d ago              OC-241
```

- Commit SHA abbreviated (7 chars). Clickable to view full SHA.
- Deploy Task links to the task detail.
- Hover over a row (or expand) shows previous deployed commit SHA for rollback reference.

### Project Settings Sub-View

Read-only. All editable through conversation (PM for project-level, Frank for org-level).

Sections:
- Project context block (the PM's context document)
- Repo path and delivery mode (shipping | feedback | none)
- Configured remotes (from doc 03a)
- Agent assignments (staff agents assigned to this project)

No edit forms. Provides transparency into current configuration.

## Observability Sub-Views (New)

### Run History

```
Recent Runs

Status    Agent       Task                   Duration  Tokens    Cost      Time
─────────────────────────────────────────────────────────────────────────────
✓ ok      Maven       OC-42: Build Auth       12m       45k       $0.34     2m ago
✓ ok      Maven       OC-42: Build Auth        3m       12k       $0.09     18m ago
✗ failed  Kai         CP-8: Fix Login          0m        2k       $0.01     45m ago
⟳ running Maven       OC-45: Update docs       --        --         --       now
```

Expandable row shows:
- Tool call trace: each tool call with name, arguments (collapsed), result summary, duration.
- Stop reason (for stopped/failed runs).
- Model profile used.
- Link to task and flow node this run was part of.

Filtering: by status, agent, task, date range.

### Cost Tracking

- **By agent**: bar chart ranking agents by token consumption.
- **By project**: bar chart ranking projects.
- **By model**: distribution across model profiles.
- **Trend**: daily/weekly cost trend line chart. Anomaly highlighting if costs spike (3x 7-day average triggers an alert indicator).
- **Budget status**: per-org and per-project token_budget utilization. Soft limit = amber indicator; hard limit = red indicator; ok = green.

### Queue Depth

Real-time (SSE updates):
- Tasks waiting in queue by priority tier (100/50/25/10).
- Active concurrent runs vs global concurrency limit.
- Per-provider rate limit utilization bars.
- Average wait time in queue.

## Viewing Context Hint

A subtle banner at the top of the chat pane (below the scope pill) showing what the main content area is currently displaying:

```
Viewing: OC-42 · Implement Auth System
```

or

```
Viewing: OtterCamp V2 · Task Board
```

- Muted styling — not a primary element.
- Helps the operator understand what context the agent is operating in (this hint is sent to the agent as part of the prompt, per doc 02).
- Updates when main content changes.
- On mobile: not present (no main content panel on mobile).

## Task Detail: Additional Header Elements

The task detail header (in addition to title/status/priority/assignee from the original spec):

- **Branch name**: `task/build-auth-system` with a copy-to-clipboard button.
- **Deploy badge**: if this is a deploy task (doc 03a), a deploy status pill: "deploying", "deployed", "deploy failed".
- **Remote push status**: if applicable — `push ok` (green) or `push failed` (red). Shown when task's `push_succeeded` or `push_failed` event has fired.

## Review Node View (Updated)

When a task is at a review node, the task detail emphasizes the review surface:

- **Diff view** (see Diff Viewer component below): primary review surface. Task branch vs the `commit_sha` from the previous flow node's execution (incremental review), or vs `main` if this is the first review node.
- **Reviewer actions**: Approve / Reject with feedback. Inline buttons in the task detail header area. Reject opens a text input for feedback.
- **Subtask summary**: what was completed in the preceding work node(s).
- **Link to work log**: for the detailed async execution trace.

For human-actor review nodes, the task also appears in the inbox. The operator can act from either the task detail or the inbox — both trigger the same downstream action. An inbox badge on the task detail header links to the inbox item.

## Diff Viewer Component

Used in task detail (review node view) and from any task's branch info section.

```
┌─────────────────────────────────────────────────────────────────┐
│ [Unified] [Split]   src/auth/handler.go (+34 -12)               │
├─────────────┬───────────────────────────────────────────────────┤
│ File tree   │  @@ -45,7 +45,7 @@                                │
│             │  - func handleToken(w, r) {                       │
│ ▸ src/      │  + func handleToken(ctx context.Context, ...) {   │
│   ▸ auth/   │    if token == "" {                                │
│     handler │  -   return errors.New("empty token")             │
│     middlew │  +   return ErrEmptyToken                         │
│   ▸ tests/  │  }                                                │
└─────────────┴───────────────────────────────────────────────────┘
```

- **File tree** (left panel): file paths with change indicators (+/-/~). Clicking a file scrolls to its diff.
- **Diff content** (right panel): syntax-highlighted. Line numbers. Addition lines (green), deletion lines (red).
- **Unified/Split toggle**: unified diff by default. Split (side-by-side) available via toggle button.
- **Large diffs**: file-level summary first (filename + additions/deletions count). Click to expand individual file diff.
- **Binary files**: shown as "Binary file changed (old: 24KB → new: 31KB)".
- **Hunk headers**: `@@ -45,7 +45,7 @@` standard unified diff format.

## TUI-Specific Components

The TUI (doc 17) has terminal-specific representations of components that exist in both the web and terminal clients. These are NOT standard web UI components but should be documented for design consistency.

### TUI Scope Indicator

```
[Task: OC-42] [Project: OtterCamp V2] [Org: General]
                   ↑ active (highlighted)
```

- `[` and `]` keys navigate left/right.
- Active scope is highlighted in the terminal (e.g., reverse video or bold).
- Shows the actual name, not just the level label.

### TUI Message Queue Section

When messages are queued while an agent turn is in progress:

```
─── active turn ──────────────────────────────────────
Agent: "Looking at the auth module..."
  > read_file(src/auth/handler.go) ✓
  > read_file(src/auth/middleware.go) ✓
  > search_code("validateToken") ...

─── queued ───────────────────────────────────────────
[1] "What about the error handling?"      [e]dit [s]teer [d]elete
[2] "Also check the logging setup"        [e]dit [s]teer [d]elete
```

- Queued messages below a "queued" divider.
- Numbered for reference.
- Keyboard shortcuts `e`, `s`, `d` for edit/steer/delete of the selected queued message.

### TUI Active Turn Indicator

```
● Maven  [12s]  > search_code("validateToken") ...    [Esc: cancel]
```

- Shows: agent name, elapsed time in seconds, current tool call in progress, cancel hint.
- Updates every second (elapsed time) and on each tool call.
- Located at the bottom of the chat view or above the input area.

### TUI Flow Stepper

Text-form flow visualization:

```
[✓ Design] → [* Implement] → [  Code Review] → [  Final Review]
                 ↑ current
             3 subtasks: 2 done, 1 in progress
```

- `✓` = completed. `*` = active (current). ` ` = pending. `!` = blocked.
- Arrow separators between nodes.
- Active node summary shown below.
- Compact enough to fit in an 80-column terminal.

### TUI Reaction Indicators

Compact inline reaction indicators below messages:

```
[+1]                    ← thumbs up
[-1 "needs revision"]   ← thumbs down with note (truncated)
```

- No count badges for space efficiency.
- Note text truncated to fit available width.

## Mobile App UI (Phase 4)

The mobile app is a monitoring and triage interface (React Native, iOS + Android). It ships in Phase 4. The three-panel layout does not apply to mobile. See doc 19 for full spec.

### Mobile Layout Principles

- **Single-panel**: one screen at a time. No persistent chat pane. Navigation via the bottom tab bar or back button.
- **30-second interaction target**: notification → tap → act → done.
- **No creation, no configuration**: read-only or approve/reject/defer/chat only.

### Bottom Navigation (Tab Bar)

Five tabs:
1. **Notifications** (home) — primary entry point
2. **Dashboard** — project health at a glance
3. **Inbox** — action-required queue (badge count)
4. **Chat** — lightweight session access
5. **More** — settings, preferences, account

### Mobile Notifications Screen

Home screen. Reverse chronological notification list.

```
TODAY
────────────────────────────────────
🔴 [CRITICAL] Escalation: Auth conflict      OtterCamp V2
   Frank escalated — token format decision needed
   2m ago                                [Open Chat]

🟠 [HIGH] Review needed: OC-42              OtterCamp V2
   PM finished scoping landing page
   15m ago                         [Approve] [Open]

YESTERDAY
────────────────────────────────────
🔵 OC-40: Update README completed           OtterCamp V2
   Task completed by Maven                  2h ago
```

- Grouped by time (Today / Yesterday / Earlier this week / Older).
- Critical items pinned at top regardless of scroll position if any exist.
- Swipe-right to open, swipe-left to dismiss.
- Tapping navigates to the relevant screen (inbox item, task detail, chat session) via deep link.

### Mobile Dashboard Screen

```
INBOX  ┌─────────────────┐
  (3)  │ OtterCamp V2    │  ← inbox badge + project card
       │ 2 in progress   │
  ↓    │ 1 blocked ⚠     │
Tap    │ 12m ago         │
to     └─────────────────┘
inbox  ┌─────────────────┐
       │ Client Portal   │
       │ 3 in progress   │
       │ 0 blocked       │
       │ 45m ago         │
       └─────────────────┘

QUICK STATS
5 active tasks · 3 completed today · 3 inbox items
```

- Inbox badge: prominent, taps to inbox. The most important number.
- Project cards: compact (name, task status summary bar, blocked count highlighted if > 0, last activity).
- Pull-to-refresh.

### Mobile Inbox Screen

```
ACTIVE (3)
────────────────────────────────────────
⚡ Escalation: Auth conflict
   OtterCamp V2 · Frank escalated
   [Open Chat]              [Defer]

📋 Task Review: OC-42 Build Auth
   OtterCamp V2 · Ready for your review
   No diff on mobile — [View on Web]
   [Approve]  [Reject]  [Defer]

✉️ Draft: Welcome email
   Client Portal · Approval needed
   [Approve]  [Edit]  [Reject]  [Defer]

DEFERRED (2)
────────────────────────────────────────
[Restore]  Task Scoping: Blog post...
[Restore]  Task Scoping: API docs...
```

- Ordered by urgency then arrival time.
- Action buttons are large tap targets (minimum 44px).
- Swipe-left-to-defer gesture.
- No diff view on mobile — work review items show "View on Web" link.

### Mobile Chat Screen

Session list → Chat view (two sub-screens):

**Session list:**
```
General                     ●        ← org session (unread)
OtterCamp V2                         ← project session
  OC-42: Build Auth System  ●        ← task session (unread)
Client Portal
```

**Chat view:**
```
┌──────────────────────────────────────┐
│ Maven  ●  OC-42: Build Auth          │  ← header
│                                      │
│  You: What's the status of OC-42?    │
│                                      │
│  Maven:                              │
│  ┌────────────────────────────────┐  │
│  │ Agent is working...            │  │  ← collapsed tool activity
│  └────────────────────────────────┘  │
│  The auth system is progressing...   │
│                                      │
│  [Stop]                              │  ← visible during agent turn
└──────────────────────────────────────┘
│ Type a message...            [Send]  │
└──────────────────────────────────────┘
```

- Tool calls collapsed to "Agent is working..." indicator.
- Stop button visible during agent turns.
- @mention: typing `@` shows agent list autocomplete.
- No file attachments, no steer, no reactions.

### Mobile Task Detail Screen

Read-only. Compact layout.

```
← Back to Inbox

OC-42: Build Auth System
● in_progress  🔶 High  @Maven

Flow: [✓ Design] → [* Implement] → [  Review] → [  Final]
              ↑ 2/3 subtasks done

⚠ This task needs your review
[Open in Inbox]

Description: Build the authentication system...

Dependencies: depends on OC-38 (✓ done)

History (recent):
2h ago  Maven started implementation
5h ago  Design node approved
```

- Flow stepper: horizontal, scrollable if many nodes.
- Review banner: shown when task is at a review node and operator is the reviewer.
- No diff view — review banner links to inbox item.

### Push Notification Designs

**Standard notification:**
```
OtterCamp
Review needed: OC-42 Build Auth
PM finished scoping landing page
```

**Rich notification (iOS/Android — with action buttons):**
```
OtterCamp                           [Approve] [Open]
Review needed: OC-42 Build Auth
PM finished scoping landing page
```

- Critical notifications: red indicator, vibration, sound.
- High notifications: standard sound.
- Medium/Low: no push (by default).
- Action buttons map directly to inbox item actions (no app open required).

### Biometric Auth Prompt

- Face ID / Touch ID / fingerprint prompt appears on app open (after initial password login).
- "Use Face ID to unlock OtterCamp" — standard platform biometric prompt UI.
- Fallback: "Use Password" link after 3 biometric failures.
- Biometric re-prompt for sensitive actions: capability approvals. Standard prompt text: "Confirm with Face ID to approve this capability."

### Deep Link Navigation

Every push notification and in-app notification item navigates directly to the relevant screen:
- `ottercamp://inbox/{id}` → Mobile Inbox Screen, scrolled to item
- `ottercamp://task/{project_slug}/{task_number}` → Mobile Task Detail Screen
- `ottercamp://project/{project_slug}` → Mobile Project Status Screen
- `ottercamp://chat/{session_id}` → Mobile Chat Screen, showing session
- `ottercamp://notifications` → Mobile Notifications Screen
- `ottercamp://dashboard` → Mobile Dashboard Screen

Universal links (`https://app.ottercamp.dev/...`) open the app if installed, fall through to mobile-responsive web UI if not.

### Offline State

When offline:
- Dashboard shows cached state with "Last updated X minutes ago" banner (amber).
- Action buttons disabled with "No connection" tooltip.
- Notification history readable (cached).
- Mutations require connectivity — no optimistic updates on mobile.

## Open Questions for Design

- **Agent avatar images**: generated avatars, uploaded photos, or colored initials? Should the starter trio have illustrated/illustrated avatars for personality?
- **Panel proportions**: exact default widths for sidebar, main content, and chat pane. Presets ("wide chat", "wide main")?
- **Work log detail**: timeline vs flat collapsible tree for the agent execution trace within each flow step?
- **Inline diff comments**: resolved as CriticMarkup-backed review artifacts stored in the repo. Open design question: whether the diff viewer renders them inline, in a side gutter, or both.
- **Browser notifications**: should the web UI request browser notification permission for urgent items when the tab is not focused?
- **Tablet mobile layout**: should the mobile app show a split-view (closer to web three-panel) on tablet widths?
