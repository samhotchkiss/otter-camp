---
## Summary

This document is the UI design specification for OtterCamp V2, a chat-primary agent orchestration platform where a single human operator manages projects, tasks, and AI agents. The core layout is a persistent three-panel design: a session sidebar (left) for navigating between conversations, a main content area (center) for dashboards/projects/task details, and an always-visible chat pane (right) that serves as the primary interaction surface. Chat is not a secondary feature -- it is how the human directs all work. There are no direct UI creation paths; everything (task creation, flow editing, scheduling) happens through conversation with agents.

The session sidebar organizes conversations hierarchically by scope: org-level (talk to Frank, the Chief of Staff), project-level (talk to a PM agent), and task-level (sync sessions for individual tasks). A scope pill at the top of the chat pane lets the user zoom between Task, Project, and Org scope without changing the main content view. Unread indicators bubble up from tasks to projects. Per-node async sessions (agent work logs) are deliberately excluded from the sidebar and only accessible from task detail views, maintaining the distinction between sync sessions (human-agent conversation) and async sessions (agent execution traces).

The main content area features several key views: a Dashboard with action items, project status, and a real-time activity feed; a Project View with a kanban-style task board (columns by work status), flow visualization, merge queue, and scheduled tasks; and a Task Detail view with progressive disclosure across three levels (board card, detail with flow stepper and subtask list, and per-node work logs). The Inbox is a separate action-required queue distinct from notifications -- every inbox item blocks progress somewhere until the human acts, with item types including task scoping reviews, work reviews, draft action reviews, escalations, and capability approvals. The activity feed provides real-time "proof of life" showing agent work, task transitions, merges, blockers, and memory system events via a live event bus.

The chat pane supports rich content rendering (markdown, syntax-highlighted code, inline images, file references, artifacts), file upload via drag-and-drop or attachment button, a message queue with Edit/Steer/Delete controls for messages sent while an agent turn is in progress, lightweight reactions (double-click for thumbs-up, right-click for thumbs-down with optional feedback), and memory attribution indicators showing when agent responses drew on injected memories. Design principles emphasize: chat-primary interaction, real-time streaming by default, dark mode, keyboard-driven navigation (Superhuman-style command bar), operator-focused single-user design, and progressive disclosure to avoid overwhelming the user while keeping everything accessible.

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
- **Draft action review**: agent staged an action under `draft_review` policy. Actions: Approve (execute), Edit then approve, Reject, Defer.
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

## Open Questions for Design

- Exact proportions of the three panels. Should the chat pane be resizable?
- Session sidebar: always visible or collapsible?
- Notification center: panel overlay or separate page?
- Command bar design and available commands.
- Mobile-responsive behavior: how do three panels collapse on smaller screens?
- Agent avatars/identity: how visually distinct should different agents be?
- Work log visualization: timeline vs flat list vs collapsible tree?
- Transitions/animations when switching scope via pill.
