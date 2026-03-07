---
## Summary

This spec defines OtterCamp's web UI, the primary graphical interface introduced in Phase 3. It is a React + TypeScript single-page application (SPA) that connects to the same REST API and SSE event stream used by the TUI (doc 17). The web UI is served as a static bundle from the OtterCamp API service itself -- no separate frontend server. It is designed for a single human operator (not a team collaboration tool) managing work through AI agents. The core design principle is that chat is the primary interface: the right panel is a persistent, always-visible chat pane, and the UI is built around it rather than chat being bolted onto a dashboard.

The layout is a three-panel design: a collapsible sidebar (navigation + session list with unread indicators), a main content area (dashboard, project views, task detail, inbox, agent directory, observability, settings), and the persistent chat pane. The chat pane features a scope pill for zooming between org/project/task sessions, real-time message streaming via SSE, a message queue with steer/edit capabilities, @mention autocomplete, memory attribution indicators, file upload, and reactions. Realtime updates use SSE (not WebSocket) because the data flow is unidirectional (server-to-client), with client mutations going through REST. Responsive behavior degrades gracefully: tablets get swappable panels, and mobile is functional but not optimized until Phase 4.

Key views include: a Dashboard (action items from inbox, project health summary, real-time activity feed), Project View (kanban task board, task list, activity feed, flow templates, merge queue, schedules, environments, settings), Task Detail (flow stepper showing node-by-node progress, subtask lists, diff viewer for code review at review nodes, work logs per flow step, full event history), Inbox (the operator's action-required queue with inline approve/reject/defer for task reviews, draft reviews, escalations, and capability approvals), Agent Directory, Observability (run history with tool call traces, cost tracking, queue depth), and Settings. A critical architectural decision is that no direct object creation or manipulation happens in the UI -- all entity creation and editing goes through chat/agents. The UI is strictly for viewing, navigating, reviewing, and monitoring. A Cmd-K command bar provides Superhuman-style keyboard-driven navigation and fuzzy search across all entities.

---

# 18. Web UI

> Status: Draft
> Phase: 3 ("OtterCamp Builds Itself") — see 14-open-questions-and-phasing.md
> Depends on: 01-architecture-and-domain.md (product principles), 02-chat.md (session model, streaming, turn cycle), 03-projects-and-task-flow.md (task lifecycle, flow, inbox, merge queue, schedules), 03a-shipping-and-delivery.md (deploy tasks, environments), 05-agents-staff-and-temps.md (agent profiles, lifecycle), 06-memory.md (memory attribution, Ellie), 12-api-events-and-realtime.md (event types, realtime transport), 13-security-observability-costs.md (observability, cost tracking), 16-agent-control-plane.md (runs, tool execution), 17-tui.md (TUI as Phase 1/2 interface), ui-spec-for-figma.md (visual design decisions)

## Purpose

The web UI is the primary graphical interface for OtterCamp, introduced in Phase 3. It is the full-featured surface for managing projects, chatting with agents, reviewing work, monitoring agent activity, and acting on inbox items. It builds on and supersedes the TUI (doc 17) for most operator workflows, though the TUI remains fully functional and connects to the same API.

The web UI is designed for a single human operator managing work through AI agents. It is not a team collaboration tool. Every design decision optimizes for one person's view of their projects, agents, and work.

## Architecture

### Single-Page Application

The web UI is a React + TypeScript single-page application (SPA). It connects to the same REST API and realtime event stream that the TUI uses. There is one API, multiple clients.

**Why SPA, not SSR:**
- The app is behind authentication. There is no public content and no SEO requirement.
- SPA provides better interactivity for the real-time, streaming-heavy UI. Chat streaming, live activity feeds, and command bar behavior are all SPA-native patterns.
- The API already exists (designed for the TUI). The web UI is a second consumer of the same API, not a server-rendered frontend that needs its own backend.

**Why React + TypeScript:**
- Mature ecosystem with strong tooling for complex interactive UIs.
- Large library ecosystem for the specific components needed: virtualized lists, syntax-highlighted code blocks, diff viewers, drag-and-drop, markdown rendering.
- TypeScript provides type safety across the API contract and UI component boundaries.
- React's component model maps well to the three-panel layout with independently updating regions.

### Realtime via SSE

All live updates are delivered via Server-Sent Events (SSE). No polling. The web UI opens an SSE connection on load and receives all events relevant to the authenticated operator.

**Why SSE, not WebSocket:**
- The realtime data flow is unidirectional: server to client. The client sends actions via REST API calls, not through the realtime channel.
- SSE works over standard HTTP, simplifies proxy and load balancer configuration, and auto-reconnects natively.
- Chat message submission, inbox actions, and all other client-initiated mutations go through the REST API. SSE delivers the resulting events back.

**Event types received via SSE** (defined in 02-chat.md and 12-api-events-and-realtime.md):
- `chat.turn.started`, `chat.message.created`, `chat.message.delta`, `chat.message.finalized`, `chat.turn.completed`, `chat.listening_eval.completed` — chat streaming
- `task.status.changed`, `task.flow.advanced`, `task.flow.rejected` — task lifecycle
- `inbox.item.created`, `inbox.item.updated` — inbox
- `merge_queue.entry.updated` — merge queue
- `agent.run.started`, `agent.run.completed`, `agent.run.failed` — agent activity
- `notification.created` — notifications
- `schedule.task.created` — scheduled task instances
- `project.push.succeeded`, `project.push.failed` — remote push

The client filters and routes events to the appropriate UI regions based on scope (org, project, task) and current view context.

### API Contract

The web UI consumes the same REST API as the TUI. No web-specific API endpoints. Key endpoint families (from 12-api-events-and-realtime.md):

- Auth and session management
- Chat sessions, messages, participants — including message submission, queue management, cancel, steer
- Projects, tasks, flow templates, flow node executions, subtasks
- Inbox items and actions
- Merge queue status
- Agent profiles and project assignments
- Notifications and read cursors
- Observability: runs, tool executions, cost data, queue depth
- Settings: org, project, model profiles, tool policies

### Build and Deployment

The web UI is a static SPA artifact (HTML, JS, CSS) served by the OtterCamp API service. No separate frontend server. In the modular monolith (doc 01), the API service serves both the REST API and the static web UI assets from the same process.

- **Development**: standard React dev server with hot module replacement, proxying API requests to the running OtterCamp service.
- **Production**: built as a static bundle, embedded in or served alongside the API service binary.
- **Self-host**: the web UI ships with OtterCamp. No additional deployment step. Open the URL, log in, use it.

## Layout

Three-panel layout. Chat is always visible. This is not a chat feature bolted onto a dashboard — chat is the primary interface, and the dashboard is built around it.

```
+------------------+---------------------------+------------------------+
|                  |                           |                        |
|  Sidebar         |     Main Content          |    Chat Pane           |
|  (left)          |     (center)              |    (right, persistent) |
|                  |                           |                        |
|  Navigation +    |  Dashboard / Project /    |  Active session,       |
|  session list    |  Task Board / Task Detail |  always live           |
|  with unread     |  / Inbox / Settings /     |                        |
|  indicators      |  Agent Directory / etc.   |                        |
|                  |                           |                        |
+------------------+---------------------------+------------------------+
```

### Panel Behavior

- **Sidebar**: collapsible. Expanded by default. When collapsed, shows icon-only navigation with unread badges. Keyboard shortcut to toggle.
- **Main content**: takes the remaining horizontal space between sidebar and chat pane. Scrolls independently.
- **Chat pane**: always visible. Fixed width with operator-resizable boundary (drag the left edge). Minimum width enforced to keep chat usable.

### Responsive Behavior

On smaller screens (tablets, narrow browser windows), the layout adapts:

- **Tablet landscape (1024-1279px)**: sidebar collapses to icon-only by default. Main content and chat pane share the remaining space.
- **Tablet portrait / narrow (768-1023px)**: sidebar collapses. Main content and chat pane become swappable views with a toggle. One is visible at a time — chat pane takes full width when active, main content takes full width when active. A persistent tab bar at the bottom switches between them.
- **Below 768px**: functional but not optimized. Mobile-responsive is sufficient for Phase 3. Dedicated mobile app is Phase 4 (doc 19).

No dedicated mobile app until Phase 4. The web UI works on tablets and can handle phone-sized screens at a basic level, but the primary design target is desktop/laptop with a wide viewport.

## Sidebar

The sidebar serves two functions: navigation between views and session switching for the chat pane.

### Structure

```
[OtterCamp logo/wordmark]

[Search / Command bar trigger]

Navigation:
  Dashboard
  Inbox (3)
  Projects
  Agents
  Schedules
  Observability
  Settings

Sessions:
  General                          <- org session (Frank)
  ---------------------------------
  OtterCamp V2                     <- project session (PM)
    Define auth architecture       <- task sync session
    Implement auth             *   <- task sync session (unread)
    Write chat spec                <- task sync session
  ---------------------------------
  Client Portal                    <- project session (PM)
    Fix login bug              *   <- task sync session (unread)
    Plan Sprint 3                  <- task sync session

[Notification bell]
```

### Navigation Section

Top of the sidebar. Links to the main views:

- **Dashboard**: landing page with action items, project health, activity feed.
- **Inbox**: the human's action-required queue (badge shows count of pending items).
- **Projects**: project list. Clicking a project opens its project view in main content.
- **Agents**: agent directory.
- **Schedules**: global schedules view across all projects.
- **Observability**: runs, costs, queue depth.
- **Settings**: org, project, model, and policy configuration.

Clicking a navigation link changes the main content. The chat pane defaults to the most specific scope available for the new context but can be independently controlled via the scope pill or session list.

### Session List

Below the navigation. Lists all sessions the human participates in, grouped by scope.

- **Org session** ("General"): always at the top. Talk to Frank.
- **Project groups**: each project is a collapsible group. The project label is clickable (switches chat pane to the project session). It uses the shared display-name resolver: `project.display_name`, then `project.slug`, then a stable generic fallback such as "Untitled project". Raw internal ID fragments must not be shown. Nested underneath are the task sync sessions for that project.
- Fresh kickoff always resolves to one canonical live project session for that project. The web UI must not route the operator into an archived or duplicate prior-run session while showing the newer live project as selected.
- **Collapsed by default** for inactive projects. Expanded for projects with active work or unread messages.
- **Unread indicators**: dot badge on sessions with unseen messages. Bubbles up: if any task session in a project has unread, the project group header shows an indicator too.
- **Active highlight**: the currently loaded chat session is highlighted in the session list.

**Clicking a session** switches the chat pane to that session. Main content does NOT change. This is the mechanism for "jump to a different conversation without losing your place."

**Per-node async sessions (agent work logs)** do NOT appear in the sidebar. They are viewable from the task detail view in main content.

### Notification Center

Accessible from the notification bell icon at the bottom of the sidebar. Opens as a panel overlay (not a full page).

- Reverse chronological list of notifications.
- Each notification shows: urgency indicator, title, brief description, timestamp, source project/task.
- **Urgency visual treatment** (from 02-chat.md Notifications):
  - **Urgent** (escalations, failures, blockers needing human judgment): prominent color, distinct icon. Impossible to miss.
  - **Normal** (reviews ready, drafts pending, @mentions): standard styling.
  - **Low** (task status changes, agent started work): subtle, grouped/batched.
- Clicking a notification navigates to the relevant context: main content updates to the project/task, chat pane loads the appropriate session.
- Mark individual as read, dismiss, or mark all as read.
- Badge on the bell icon shows count of unread notifications.

## Chat Pane

The chat pane is the right panel, always visible. It is the primary interaction surface — the operator talks to agents here, and agents respond in real time.

### Scope Pill

At the top of the chat pane, a segmented control (pill) lets the operator zoom in/out of scope without changing main content:

```
Viewing a task:     [Implement Auth] [OtterCamp V2] [General]
Viewing a project:                   [OtterCamp V2] [General]
Viewing dashboard:                                   [General]
```

- Segments show the specific name at each scope level.
- Clicking a segment switches the chat pane to the session at that scope level.
- The active segment is visually highlighted.
- Main content does NOT change when the pill is clicked.
- The pill always reflects what scope levels are available given the current main content context.

This is the primary mechanism for "zoom out" — the operator is looking at a task, wants to tell Frank something, clicks [General], talks to Frank, clicks [Implement Auth] to return.

### Viewing Context

When the chat pane scope differs from what the operator is looking at in main content, the system provides a viewing context hint to the agent (from 02-chat.md). The client sends `{viewing_scope_type, viewing_scope_id}` as ephemeral metadata with each message. The agent knows what the operator was looking at without the operator needing to explain.

### Chat Content Area

Messages displayed in a conversation thread. All rendering decisions from ui-spec-for-figma.md apply:

**Message types:**

- **Human messages**: right-aligned or visually distinct. Standard chat bubble styling.
- **Agent messages**: left-aligned with agent avatar and name. Text streams in real-time (tokens appear as they arrive via `chat.message.delta` events).
- **Tool calls**: shown inline during the agent's response. Compact representation: tool name + arguments summary. Expandable to show full details. Tool results shown as status indicator (success/error) below the tool call. Large results collapsed by default with "Show more."
- **System messages**: subtle, de-emphasized. Session start, context compression notices, limit reached notices.
- **Interjection messages**: from a different agent than the default responder. Visually distinct — different agent avatar, subtle label ("Lori interjected").

**Rich content rendering** (from 02-chat.md Rich Content):

- **Text blocks**: rendered as markdown (headings, bold, italic, lists, links, etc.).
- **Code blocks**: syntax-highlighted with language tag. Copy button. Collapsible for long snippets.
- **Image blocks**: rendered inline at appropriate size. Click to expand/zoom in lightbox.
- **File reference blocks**: compact card showing filename, path, and repo context. Click to open file preview or navigate to the file in the project view. Shows diff if referencing a specific commit.
- **Artifact blocks**: downloadable/previewable embed. File icon, name, size. Click to preview or download.

### Streaming and Active Turn Indicator

While an agent is working (turn in progress):

- Which agent is responding (avatar + name).
- Work-in-progress indicator (spinner or animation).
- Tool calls as they happen (streaming list of tool activity).
- Elapsed time (subtle, optional).
- **Cancel button** visible. Keyboard shortcut: Escape. Cancels the current turn without injecting a message (maps to `user_cancelled` stop reason from 02-chat.md).

### Message Queue

When the operator sends messages while a turn is in progress, they are queued (from 02-chat.md Message Queue):

```
+---------------------------------------------+
| Agent: "Looking at the auth module..."      |
|   > read_file(src/auth/handler.go)  ok      |
|   > read_file(src/auth/middleware.go)  ok    |
|   > search_code("validateToken") ...        |  <- still running
|                                              |
| --- queued -------------------------------- |
| You: "What about the error handling?"        |
|                          [Edit] [Steer >]   |
| You: "Also check the logging setup"          |
|                          [Edit] [Steer >]   |
+---------------------------------------------+
```

- Queued messages appear below a "queued" divider.
- Each has **Edit** (inline editing) and **Steer** (cancel current turn, promote this message) buttons.
- Messages can be deleted from the queue.
- Steer cancels the in-progress turn (`user_steered` stop reason) and immediately triggers a new turn cycle with the steered message.

### Reactions

Messages support lightweight reactions (from 02-chat.md Reactions):

- **Double-click** a message to thumbs-up (positive reaction). Quick, low-friction for the common case.
- **Right-click** (or long-press on touch) for context menu: thumbs-down (negative reaction), leave feedback (negative + note).
- Reactions shown as small indicators below the message (thumbs up/down icon + count).
- Agent reactions on human messages shown subtly — small avatar + indicator.

### Memory Attribution

When an agent's response was informed by injected memories (from 06-memory.md), a subtle indicator appears on the message:

- Small "memory" indicator (brain icon or "via memory" label) near the message.
- Expandable to show which memories contributed, their confidence, and sources.
- `memory.query` tool calls render in the tool call stream like any other tool.
- Ellie's structured memory results display as lists with: memory content, confidence indicator (high/medium/low), source (conversation, file, import), and age. Entity definitions highlighted as authoritative.

### File Upload

- **Drag-and-drop** files into the chat pane to attach to the current message.
- **Attach button** (paperclip icon) in the message input area.
- Progress indicator and preview thumbnail (for images) or file icon (for other types).
- Multiple files per message.
- Uploaded files appear as artifact blocks once sent (from 02-chat.md File Uploads).

### @Mention Autocomplete

When the operator types `@` in the message input:

- A dropdown autocomplete appears showing available agents in the current session scope.
- Agents are filtered as the operator types. Shows agent avatar, name, and role title.
- Selecting an agent inserts the @mention, which triggers that agent as the responder instead of the default responder (from 02-chat.md Multi-Party Turn Sequencing).

## Core Views

### Dashboard

The landing page. The operator's starting point on every visit. Shows what needs attention and what is happening.

**Layout:**

```
+-------------------------------------------------------+
|  Action Items (from Inbox)                      (3)   |
|  +--------------------------------------------------+ |
|  | ! Escalation: Auth conflict         OtterCamp V2  | |
|  | > Task Review: Landing page         Client Portal | |
|  | > Draft Review: Welcome email       Client Portal | |
|  +--------------------------------------------------+ |
|                                                       |
|  Runtime Health                     Attention Required|
|  +--------------------------------------------------+ |
|  | Running 3 | Stale 2 | Blocked 1 | Failures 2      | |
|  | Session management              8m stale run lock | |
|  | Billing retry                   Human input wait  | |
|  | Landing page polish             6m retry queued   | |
|  +--------------------------------------------------+ |
|                                                       |
|  Project Health                                       |
|  +--------------------------------------------------+ |
|  | OtterCamp V2    12 tasks   3 in_progress  1 blocked |
|  | Client Portal    8 tasks   2 in_progress  0 blocked |
|  +--------------------------------------------------+ |
|                                                       |
|  Activity Feed                                        |
|  +--------------------------------------------------+ |
|  | 2m   ok  Subtask "Token validation" completed      |
|  | 5m   ->  Task "Fix login bug" merged to main       |
|  | 8m   !!  Blocker filed: "Missing API creds"        |
|  | 12m  ..  Agent started "Session management"        |
|  +--------------------------------------------------+ |
+-------------------------------------------------------+
```

**Sections:**

- **Action items**: top-of-page summary pulled from the inbox. Shows the highest-urgency items that need the operator's attention. Clicking an item navigates to it in the inbox or opens it in context. Limited to the top 5-10 items; "View all in Inbox" link for the rest.
- **Runtime health**: org-wide execution summary sourced from the control-plane dashboard. Shows counts for active work, stale tasks or executions, blocked items, and recent failures. Each section also surfaces the most important recent entries, such as a stuck run lock, human-input wait, failed run, retry promotion, or newly active task. Clicking an item opens the linked task, project, or run detail.
- **Project health**: each project with task counts by work status (in_progress, blocked, review, queued). Quick visual scan of what is moving and what is stuck. Clicking a project navigates to its project view.
- **Activity feed**: real-time stream of significant events across all projects (from ui-spec-for-figma.md Activity Feed). Reverse chronological. Each entry shows timestamp, event type indicator, description, and source project/task. Failures, retries, wakeup promotions, and completions all appear here. Clicking navigates to the relevant context.

The runtime health summary and activity feed update in real time via SSE. New entries appear at the top with a subtle animation, and the dashboard headline reflects whether the system is quietly healthy, actively healthy, or needs attention.

Before a restart-driven full end-to-end run, the shared restart-readiness smoke gate must validate this same REST/SSE contract. Dashboard runtime health, project task counts, and worker warnings in the web UI must agree with control-plane truth; the web UI does not get a looser restart bar than the TUI.

### Project View

The hub for a single project. Multiple sub-views, organized as tabs or a segmented control within the main content area.

**Sub-views:**

#### Task Board (default)

Kanban-style columns by work status: `draft`, `queued`, `in_progress`, `blocked`, `on_hold`, `review`, `done`.

- **Cancelled tasks** are not shown by default. Accessible via a "Show cancelled" toggle or filter.
- **Done tasks** can optionally be hidden via filter to focus on active work.
- Project task counts, project-view task lists, and project labels must stay bound to the latest live project/task state. The UI must not keep showing zero open tasks or raw internal project-ID fragments once the live project record and task stream are available.
- Restart hydration must prefer the best known live project/task truth over sparse detail payloads. A fresh reload must not temporarily regress a live project back to `Untitled project`, `0` open tasks, or a stale failure banner once current runtime state proves active work exists.

Each card shows at-a-glance information (from ui-spec-for-figma.md Task Board Cards):

```
+-----------------------------------+
| Build Auth System                 |
| [## ** ..]  <- flow progress      |
| !! High   @WorkerBot             |
| Implement: 2/3 subtasks done     |
+-----------------------------------+
```

- Task title
- Flow progress bar: small step indicator. Completed nodes filled, active node highlighted, blocked nodes in amber/orange.
- Priority indicator and assignee avatar
- Subtask summary (if current node has subtasks): node name and subtask completion count
- **Deploy tasks** have a visual badge distinguishing them from regular tasks (from 03a-shipping-and-delivery.md). Same card layout, subtle deploy indicator for quick scanning.

Clicking a card navigates to the task detail view.

Cards are not draggable between columns. Status transitions happen through agents/chat, not through direct UI manipulation. Consistent with the product principle: "UI surfaces are for viewing, navigating, and reviewing — not for direct manipulation."

#### Task List

Alternative flat list view of all tasks in the project. Sortable by: status, priority, created date, updated date, assignee. Filterable by: status, priority, assignee, flow template. Useful for finding specific tasks or getting a different perspective than the board.

#### Activity Feed

Project-scoped real-time stream of events. Same format as the dashboard activity feed but filtered to this project only. Shows task status transitions, subtask completions, flow node advancements, blocker filings, merge events, escalations, agent assignments.

#### Flow Templates

Read-only visualization of the project's flow templates. Shows nodes and edges in a linear or graph layout. Each node displays: name, type (work/review), actor type, and connected edges.

Editing flows happens through conversation with the PM — not through the UI. The UI provides a "Discuss with PM" link that focuses the chat pane on the project session with a suggested prompt.

#### Merge Queue

List of tasks awaiting merge to `main` (from 03-projects-and-task-flow.md Merge Queue):

```
Merge Queue                                         (2 pending)

Status    Task                        Branch               Queued
---------------------------------------------------------------
merging   OC-42: Fix login bug        task/fix-login-bug   2m ago
queued    OC-45: Update README        task/update-readme   5m ago
```

Each entry shows: merge status (queued, merging, conflict, merged), task title and number, branch name, time in queue. Conflict entries show conflict details and a link to the task for resolution.

#### Schedules

Project-scoped schedules tab (from ui-spec-for-figma.md Scheduled Tasks):

```
Schedules                                           [Active: 3  Paused: 1]

Name                    Cadence              Last Run        Next Run       Status
------------------------------------------------------------------------------------
Check inbox             Every 10 min         2 min ago ok    8 min         Active
Draft weekly roundup    Mon 9:00 AM          3 days ago ok   4 days        Active
Generate metrics        Daily 6:00 AM        18h ago ok      6 hours       Active
Competitor scan         Wed 2:00 PM          5 days ago      --            Paused
```

Each row shows: schedule name, human-readable cadence, last run timestamp with status indicator, next scheduled run, and active/paused status. Clicking a row expands to show recent instances (last 5-10 tasks created by this schedule) with their status and duration.

Read-only. Editing schedules happens through conversation with the PM.

#### Environments

Shown only for projects with environments configured (from 03a-shipping-and-delivery.md):

```
Environments

Name          Deployed Commit    Deployed At         Deploy Task
----------------------------------------------------------------
staging       abc123d            2h ago              OC-247
production    def456a            1d ago              OC-241
```

Each environment shows: name, currently deployed commit SHA (abbreviated, clickable to view), deployment timestamp, and the deploy task that deployed it. Previous commit SHA shown on hover or in an expanded detail view for rollback reference.

#### Project Settings

Project-level configuration. Read-only display of project context block, repo path, delivery mode, remotes, and agent assignments. All editable through conversation with the PM — the settings view provides visibility, not direct manipulation.

### Task Detail

The detailed view of a single task. Three levels of progressive disclosure: board card -> task detail -> work log.

#### Header

- Task number and title (`OC-42: Build Auth System`)
- Status badge (work status with color coding)
- Priority indicator
- Assignee (agent avatar and name)
- Branch name with copy button (`task/build-auth-system`)
- Deploy badge (if this is a deploy task, from 03a)
- Remote push status (if applicable — `push_succeeded` or `push_failed` from task events)

#### Flow Stepper

Horizontal step indicator across the top of the detail view. Each step represents a flow node (from ui-spec-for-figma.md Flow Stepper):

```
[ok Design] -> [** Implement] -> [.. Code Review] -> [.. Final Review]
                    ^ current
                3 subtasks (2 done, 1 in progress)
```

- **Completed nodes**: checkmark, muted/green.
- **Active node**: highlighted, prominent. If the node has subtasks, summary count below ("3 subtasks: 2 done, 1 in progress").
- **Blocked node**: amber/orange indicator with blocker task link.
- **Pending nodes**: empty/outlined.
- Clicking a node shows its detail below: subtask list (if any), work log link, execution metadata.

#### Subtask List (Per Node)

When viewing a node that has subtasks (from ui-spec-for-figma.md Subtask List):

```
[** Implement] -- 3 subtasks
--------------------------------------------
ok  Auth middleware                  done
**  Token validation            in_progress
..  Session management    queued (depends on Auth middleware)
```

Each subtask shows: title, status, assignee, dependency info if blocked/waiting. Clicking a subtask expands inline to show description, acceptance criteria, and work log link.

Subtask numbering follows the project convention: `OC-42.1`, `OC-42.2`, `OC-42.3`.

#### Body

- **Description**: the task's description, rendered as markdown.
- **Acceptance criteria**: structured list of criteria, each checkable (read-only — agents mark completion).
- **Constraints**: any constraints the agent must follow.
- **Branch info**: task branch name, link to view diff against `main`. At review nodes, the diff is the primary review surface.
- **Dependencies**: "depends on" and "blocks" lists at the task level. Each entry shows the linked task's title, number, and current status. Clicking navigates to the dependency.
- **Artifacts**: files, screenshots, outputs produced during the task. Downloadable/previewable.
- **History**: rendered from `project_task_event` audit trail — status transitions, flow advancements, rejections, blocker filings, escalations, dependency changes, merges, push outcomes, deployments. Each entry shows actor, timestamp, and comment if present. Collapsed by default.

#### Review Node View

When a task is at a review node, the task detail emphasizes the review surface (from ui-spec-for-figma.md Review Node View):

- **Diff view**: task branch vs the `commit_sha` from the previous node's `flow_node_execution` (or vs `main` if first review). Full syntax-highlighted diff with file tree navigation. Inline comments are not supported in V1 — feedback goes through the chat session.
- **Reviewer actions**: Approve / Reject with feedback. These are inline action buttons. Rejecting opens a text input for feedback (maps to `project_task_event.comment`).
- **Subtask summary** from the preceding work node (what was done).
- **Link to work log** for detailed execution trace.

For human-actor review nodes, the task also appears in the inbox. The operator can act from either the task detail or the inbox — both trigger the same downstream action.

#### Work Log (Per Flow Step)

Each completed or active flow step has a "View work log" link. Expanding it shows the agent's async execution trace for that step:

- Tool calls with arguments and results (collapsed results for large outputs).
- Artifacts produced.
- Time spent and token usage.
- For nodes with subtasks, work logs exist per subtask.
- The work-log panel resolves the task's task-scoped execution session directly. Project-scoped PM/coordinator sessions are not fallbacks for task execution history.

The work log is read-only. It shows what the agent did in its per-node async session. Discussion about the work happens in the task's sync session (chat pane), not in the work log.

### Inbox

The operator's action-required queue (from 03-projects-and-task-flow.md Inbox and ui-spec-for-figma.md Inbox). Separate from notifications. Every item blocks progress somewhere until the operator acts.

**Layout:**

```
INBOX (3)
-----------------------------------------------------
! Escalation: Auth conflict              OtterCamp V2
  Frank escalated -- needs your call on token format
  [Open in Context] [Defer]

> Task Scoping: Landing page design      Client Portal
  PM finished scoping -- ready for your review
  [Approve] [Request Changes] [Defer]

> Draft Review: Welcome email             Client Portal
  Agent composed email under draft policy
  [Approve] [Edit] [Reject] [Defer]

DEFERRED (2)
-----------------------------------------------------
> Task Scoping: Blog post series         OtterCamp V2
  Deferred 3 days ago
  [Restore]

> Task Scoping: API docs rewrite         Client Portal
  Deferred 1 week ago
  [Restore]
```

**Item types and actions** (from 03-projects-and-task-flow.md):

| Type | Actions |
|------|---------|
| Task scoping review | Approve (-> queued), Request changes, Defer |
| Task work review | Approve (-> advance flow), Reject with feedback (-> back to work), Defer |
| Draft action review | Approve (execute), Edit then approve, Reject, Defer |
| Escalation | Open in context (jumps to chat), Decide inline, Defer |
| Capability approval | Approve, Approve with constraints, Deny, Defer |

**Behavior:**

- Ordered by urgency then arrival time. Escalations and capability approvals sort above reviews and drafts.
- Each item carries enough context to decide without leaving the inbox: task description, diff preview, agent reasoning, staged action payload.
- "Open in context" navigates to the full picture: main content updates to the relevant task/project, chat pane loads the relevant session.
- Acting on an item triggers the downstream action immediately. No additional confirmation — the inbox itself is the confirmation step.
- Defer moves the item to the deferred section. Restore brings it back.
- No expiry. Agents nudge via chat if items sit too long.
- New inbox items appear in real time via SSE.

### Agent Directory

List of all agents in the organization.

**Layout:**

```
Agents                                          [Staff: 8  Temps: 3]

Name          Role                  Status    Projects              Class
--------------------------------------------------------------------------
Frank         Chief of Staff        active    (org-level)           staff
Lori          Agent Relations       active    (org-level)           staff
Ellie         Memory & Knowledge    active    (org-level)           staff
Maven         Senior Go Dev         active    OtterCamp V2          staff
Kai           Frontend Dev          active    Client Portal         staff
...
```

Each row shows: agent avatar, name, role title, lifecycle status, project assignments (or "org-level"), agent class (staff/temp).

**Clicking an agent** opens the agent detail view:

- Full profile: name, pronouns, role title, description, agent class, scope level.
- Current assignments: which projects, which roles.
- Activity summary: recent runs, current workload (active tasks), last active timestamp.
- Model profile and budget info.
- Skills attached.
- Lifecycle status with transition history.

All profile editing happens through Lori via chat. The agent directory is read-only.

### Schedules (Global View)

Aggregated view of all schedules across all projects. Same table format as the project-level schedules tab but grouped by project. Shows all active and paused schedules with cadence, last run, next run, and status.

Accessible from the sidebar navigation. Useful for the operator to see all recurring work in one place.

### Settings

Configuration views for org-level and project-level settings. All settings are viewable in the UI and editable through conversation with agents (Frank for org-level, PM for project-level).

**Sections:**

- **Organization**: org name, default policies, global concurrency limits.
- **Model profiles**: configured model profiles with provider, model ID, and cost tier. Which profiles are available and which are default.
- **Tool policies**: org-level tool allow/deny policies.
- **Project settings**: per-project configuration (selected from a project picker). Context block, delivery mode, remotes, environments.
- **Notification preferences**: per-urgency-tier delivery, per-scope filtering, per-event-type filtering (from 02-chat.md Notification Preferences).

The settings views provide transparency into current configuration. The operator can see exactly what policies are in effect, which is important for understanding agent behavior. But the settings views do not have edit forms — changes go through chat.

### Observability

Monitoring and operational visibility into the system's internals. The operator's window into agent work, costs, and system health.

**Sub-views:**

#### Run History

List of agent runs with filtering and search (from 16-agent-control-plane.md):

```
Recent Runs

Status    Agent       Task                Duration  Tokens    Cost      Time
------------------------------------------------------------------------------
ok        Maven       OC-42: Build Auth   12m       45k       $0.34     2m ago
ok        Maven       OC-42: Build Auth   3m        12k       $0.09     18m ago
failed    Kai         CP-8: Fix Login     0m        2k        $0.01     45m ago
running   Maven       OC-45: Update docs  --        --        --        now
```

Each run shows: status (running, completed, failed, stopped), agent, associated task, duration, token count (input + output), estimated cost, and timestamp.

Clicking a run expands to show:
- Full tool call trace: each tool call with name, arguments, result summary, and duration.
- Stop reason (if stopped or failed).
- Model used.
- Link to the task and flow node this run was part of.

#### Cost Tracking

Per-period cost breakdown (from 13-security-observability-costs.md):

- **By agent**: which agents are consuming the most budget.
- **By project**: which projects are most expensive.
- **By model**: cost distribution across model providers and profiles.
- **Trend**: daily/weekly cost trend chart. Anomaly highlighting if costs spike.
- **Budget status**: per-org and per-project budget utilization. Visual indicator when approaching limits.

#### Queue Depth

Current state of the scheduling queue:

- Tasks waiting in the queue by priority level.
- Active concurrent runs vs. global concurrency limit.
- Per-provider rate limit utilization.
- Average wait time in queue.

Real-time updates via SSE. The operator can see at a glance whether the system is keeping up or falling behind.

## Command Bar

Keyboard shortcut `Cmd-K` (Mac) / `Ctrl-K` (other) opens a Superhuman-style command palette. This is the primary keyboard-driven navigation and action mechanism.

### Search

- Fuzzy search across: projects, tasks (by title, number, or slug), agents (by name or role), sessions (by title/scope name), flow templates.
- Results grouped by entity type with icons for quick identification.
- Recent searches and frequently accessed items shown when the command bar opens with an empty query.

### Quick Actions

- **Navigate**: "Go to OC-42", "Open Client Portal", "Dashboard", "Inbox".
- **Switch session**: "Talk to Frank", "OtterCamp V2 project session", "OC-42 task session".
- **Filter**: "Show blocked tasks in OtterCamp V2", "Runs by Maven today".
- **System**: "Toggle dark/light mode", "Collapse sidebar", "Toggle chat pane focus".

### Behavior

- Opens centered on screen, overlaying current content.
- Type to search/filter. Arrow keys to navigate results. Enter to select.
- Escape to dismiss.
- Results update as the operator types (no submit required).
- Most recently used items are prioritized in results.

## Design System

### Theme

- **Dark mode default**. The operator is likely a developer or technical person. Dark mode reduces eye strain for extended sessions.
- **Light mode available** via toggle in settings or command bar. Persisted as a user preference.
- Theme applies consistently across all views and components.

### Typography

- **Monospace for code and technical content**: code blocks, file paths, branch names, commit SHAs, tool call arguments, diff views.
- **Proportional font for prose**: task descriptions, agent messages (text blocks), acceptance criteria, comments.
- Clean, dense information design. Not a consumer-friendly design — optimized for power users who want maximum information density without clutter.

### Color System

- **Status colors**: consistent across all views. Green for done/success, blue for in_progress/active, amber/orange for blocked/warning, red for failed/error, gray for pending/queued/cancelled.
- **Priority indicators**: urgent (red), high (orange), normal (blue/default), low (gray).
- **Agent identity**: each agent has a consistent accent color or avatar. Visual distinction between agents in multi-agent conversations.
- **Urgency tiers in notifications**: urgent uses prominent styling (different background color, border), normal uses standard styling, low uses muted styling.

### Information Density

Designed for operators, not casual users:

- Dense tables with compact row heights.
- Minimal whitespace — every pixel earns its place.
- Progressive disclosure everywhere: collapsed sections, expandable details, "show more" for large content.
- Keyboard shortcuts for common actions reduce the need for mouse travel.
- No decorative illustrations, hero images, or marketing copy. Every element serves a functional purpose.

### Component Patterns

- **Cards**: task board cards, inbox items, notification items. Consistent padding, border radius, and hover state.
- **Tables**: run history, schedules, agent directory. Sortable columns, filterable rows.
- **Tabs/segments**: sub-views within project view, scope pill segments.
- **Expandable sections**: tool call details, work logs, task history, large tool results.
- **Badges**: status badges, unread indicators, count badges, deploy badges.
- **Diff viewer**: syntax-highlighted, file-tree navigation, unified or split view toggle.

## Key Interactions

### Reviewing Agent Work

The primary review flow for work at a review node:

1. Operator sees the task in the inbox or navigates to it via the task board.
2. Task detail opens in main content. The flow stepper shows the task is at a review node.
3. The diff view loads: task branch vs the commit SHA from the previous node's execution. Full file-by-file diff with syntax highlighting.
4. The operator reads the diff, checks the subtask summary, and optionally views the work log for execution details.
5. The chat pane shows the task sync session. The operator can ask the PM or worker questions about the implementation.
6. The operator clicks Approve (flow advances to the next node) or Reject with feedback (flow loops back, feedback captured in task event comment).

### Approving Inbox Items

Inline decision-making without leaving the inbox:

1. Inbox item shows context: what it is, who created it, why it needs attention.
2. For task scoping reviews: the operator reads the scoped task (description, acceptance criteria) inline. Approves or requests changes.
3. For draft action reviews: the operator reads the staged action payload (email content, API call details). Approves, edits, or rejects.
4. For escalations: "Open in Context" navigates to the full picture. The operator discusses with Frank or the PM in the chat pane, then returns to the inbox to act (or acts from the task detail).
5. Actions are immediate. No additional confirmation step.

### Monitoring Agent Activity

Real-time awareness of what agents are doing:

1. The activity feed (dashboard or project-scoped) shows events as they happen.
2. For deeper investigation, the observability run history shows individual runs with full tool call traces.
3. Queue depth shows whether the system is keeping up.
4. Cost tracking shows budget utilization.
5. The operator can navigate from any run to the task it belongs to, and from the task to the agent's work log.

### Flow Visualization

Understanding task progress through flows:

1. Task board cards show flow progress at a glance (miniature step indicator).
2. Task detail shows the full flow stepper with node-by-node status.
3. Clicking a node shows its subtasks (if any), execution metadata, and work log link.
4. Project-level flow template view shows the template structure for all flows in the project.
5. The flow stepper is interactive — clicking completed nodes shows historical execution data (what happened, how long it took, which agent handled it).

### Diff View

Code review and change inspection:

1. Available from the task detail at review nodes, and from any task via the branch info section.
2. Shows task branch vs `main` (or vs the previous node's `commit_sha` at review nodes for incremental review).
3. File tree on the left, diff content on the right.
4. Syntax highlighting by file type.
5. Unified diff by default, split view available via toggle.
6. Binary files shown as "binary file changed" with size info.
7. Large diffs show file-level summary first, expandable per-file.

## Keyboard Shortcuts

Power-user navigation and actions. All shortcuts are discoverable via the command bar (typing "keyboard shortcuts" or "?" shows the list).

| Shortcut | Action |
|----------|--------|
| `Cmd-K` / `Ctrl-K` | Open command bar |
| `Escape` | Close command bar / Cancel agent turn / Dismiss overlay |
| `Cmd-Enter` | Send message in chat pane |
| `Cmd-Shift-C` | Toggle sidebar collapsed/expanded |
| `Cmd-1` through `Cmd-9` | Switch to session by position in sidebar |
| `Cmd-[` / `Cmd-]` | Navigate scope pill left/right (zoom out / zoom in) |
| `Cmd-I` | Focus inbox |
| `Cmd-D` | Focus dashboard |
| `/` | Focus chat input (when chat pane is not focused) |

Shortcuts are configurable in settings (future enhancement — ship with sensible defaults first).

## Authentication and Session

- The web UI requires authentication. Login flow uses the same auth system as the API (from 04-auth-tenancy-and-identity.md).
- Session persistence: the operator's view state (which session is loaded, which main content view is open, sidebar collapse state, chat pane width) is persisted in local storage. Returning to the app restores the previous state.
- The SSE connection authenticates using the same session token as the REST API.
- Inactivity does not disconnect the SSE stream. The operator can leave the app open and it continues receiving live updates.

## Accessibility

- Semantic HTML structure. All interactive elements are keyboard-accessible.
- ARIA labels for custom components (command bar, scope pill, flow stepper, status badges).
- Color is not the sole indicator for status — icons and text labels accompany color coding.
- Focus management: modal overlays (command bar, notification center) trap focus appropriately and return focus on dismiss.
- Screen reader support for chat message stream: new messages announced, tool call status communicated.
- Minimum contrast ratios met in both dark and light modes.

## Performance

- **Virtualized lists** for long content: chat message history, run history, activity feed. Only visible items are rendered.
- **Lazy loading** for heavy components: diff viewer, work logs, large tool results. Loaded on demand when the operator expands or navigates to them.
- **SSE event batching**: rapid-fire events (e.g., many task status changes during a burst of completions) are batched on the client before re-rendering. Prevents UI thrashing.
- **Optimistic updates**: inbox actions and chat message submission show immediate UI feedback before the API response confirms. Rollback on failure.
- **Code splitting**: views are loaded on demand. The initial bundle includes the layout shell, sidebar, chat pane, and dashboard. Other views (observability, settings, agent directory) load when first navigated to.
- **Image optimization**: agent avatars and uploaded images are resized/compressed. Thumbnails in chat, full resolution on click.

## Relationship to TUI

The web UI and TUI are sibling clients of the same API. They coexist indefinitely.

- **Phase 1-2**: TUI is the only client. All operator interaction happens through the terminal.
- **Phase 3**: Web UI is introduced. It provides the full graphical experience — task boards, diff views, flow visualization, observability dashboards — that the TUI cannot express well in a terminal.
- **Phase 4+**: Both remain available. The TUI is ideal for quick terminal interactions, scripting, and developers who prefer keyboard-only workflows. The web UI is ideal for visual overview, review workflows, and monitoring.

Feature parity is not a goal. The TUI excels at chat and quick actions. The web UI excels at visual, spatial, and review-oriented tasks. Each plays to the strengths of its medium.

## Resolved Decisions

1. **React + TypeScript SPA.** Mature ecosystem, strong tooling for complex interactive UIs, good library support for diff viewers, virtualized lists, syntax highlighting, and markdown rendering.
2. **SPA, not SSR.** The app is behind auth, SEO does not matter. SPA gives better interactivity for the streaming-heavy, real-time UI. No separate frontend server — static assets served by the API service.
3. **SSE for realtime, not WebSocket.** Data flow is unidirectional (server to client). Client mutations go through REST. SSE is simpler, works over standard HTTP, auto-reconnects, and avoids the complexity of bidirectional WebSocket management.
4. **Mobile-responsive is sufficient for Phase 3.** No dedicated mobile app until Phase 4 (doc 19). The three-panel layout degrades to swappable panels on narrow screens. Tablets work well.
5. **Dark mode default.** The operator is likely a developer or technical person. Light mode available as a toggle. Persisted as user preference.
6. **Chat pane always visible.** Consistent with the product principle "chat is the primary interface." The chat pane is not a drawer or modal — it is a persistent panel. On narrow screens, it becomes a swappable view rather than disappearing.
7. **No direct object creation in UI.** Tasks, agents, flows, schedules, and all other entities are created through chat/agents. The UI provides viewing, navigation, reviewing (diffs, inbox actions), and monitoring. No "new task" buttons, no creation forms, no drag-to-reorder cards.
8. **Sidebar is collapsible.** Defaults to expanded. Can be collapsed to icon-only for more main content space. Keyboard shortcut to toggle.
9. **Chat pane is resizable.** Operator can drag the left edge to adjust width. Minimum width enforced. Width persisted in local storage.
10. **Command bar is the primary keyboard navigation.** Cmd-K opens a fuzzy search across all entities. Quick actions, navigation, session switching, and system commands all accessible from the command bar.
11. **Diff view for code review.** Task branch vs previous node's commit SHA at review nodes (incremental review). Full syntax highlighting, file tree navigation, unified/split toggle. Inline comments deferred to future enhancement — feedback goes through chat.
12. **Feature parity with TUI is not a goal.** Each client plays to the strengths of its medium. TUI excels at chat and quick terminal actions. Web UI excels at visual overview, spatial layouts, review workflows, and monitoring.
13. **Static SPA served by the API service.** No separate frontend server. The web UI ships as a static bundle embedded in the OtterCamp distribution. Self-hosters get it automatically.
14. **Notification center is a sidebar overlay, not a separate page.** Quick access from the bell icon. Dismiss to return to current work.
15. **All settings viewable in UI, editable through chat.** Settings views provide transparency into current configuration. Changes go through agents (Frank for org-level, PM for project-level). No inline edit forms.
16. **View state persisted in local storage.** Active session, main content view, sidebar collapse state, chat pane width, dark/light mode preference. Restored on return.
17. **Activity feed is a core component.** Present on both the dashboard (org-scoped) and project view (project-scoped). Real-time updates via SSE. The operator's proof that the system is alive and working.
18. **Virtualized rendering for long lists.** Chat history, run history, and activity feeds use virtualized scrolling to handle large datasets without performance degradation.

## Open Questions

- **Panel proportions**: exact default widths for sidebar, main content, and chat pane. Should there be presets ("wide chat", "wide main")?
- **Agent avatars**: how visually distinct should agents be? Generated avatars, uploaded images, or simple colored initials? Does each agent need a unique color?
- **Work log visualization**: timeline vs flat list vs collapsible tree for the agent execution trace? The choice affects information density and readability.
- **Transitions/animations**: how much animation when switching scope via pill, opening/collapsing sections, new activity feed items? The target is subtle and functional, not decorative.
- **Offline/degraded mode**: what happens when the SSE connection drops? Show a banner, queue client-side actions, auto-reconnect? How much offline capability is worth building?
- **Inline diff comments**: future enhancement for review workflows. When added, should comments live in the chat session or as a separate annotation layer on the diff?
- **Browser notifications**: should the web UI request browser notification permission for urgent items when the tab is not focused? Or rely solely on in-app notifications and the future mobile app (doc 19)?

## Future Enhancements

- **Customizable dashboard widgets**: operator can configure which sections appear and their layout.
- **Saved filters/views**: save frequently used task board filters (e.g., "my review items", "blocked tasks in OtterCamp V2").
- **Inline diff comments**: annotation layer on the diff viewer for review workflows, feeding comments back into the task sync session.
- **Keyboard shortcut customization**: operator can rebind shortcuts.
- **Plugin/extension system for custom views**: third-party or project-specific UI panels.
- **Split main content**: ability to view two main content panels side by side (e.g., two tasks, or a task and its dependency).
- **Rich text input in chat**: beyond plain text + markdown — formatting toolbar, inline code snippets, structured input templates.
