---
## Summary

This spec defines the Terminal UI (TUI) for OtterCamp, which is the **first and primary interface** for the product. The TUI is not a developer convenience tool -- it is the sole operator interface for Phases 1 and 2, before the web UI arrives in Phase 3. It is built with **Bubble Tea** (Go, from the Charm ecosystem) and runs as a mode of the single `otter` binary: `otter tui` launches the interactive full-screen TUI, while `otter <command>` runs non-interactive CLI commands (e.g., `otter send`, `otter inbox`, `otter task`). Both connect directly to the OtterCamp API via HTTP + SSE -- the TUI is an API client, same as the web UI will be.

The TUI uses a **three-panel layout**: a left sidebar (session/project navigator, 20%), a center main content area (dashboard, task board, task detail, inbox, activity feed, agent status, merge queue, schedules -- 40%), and a right chat pane (always visible, 40%). Exactly one panel has focus at a time. Focus cycles with Tab/Shift-Tab or jumps with Alt-1/2/3. The chat pane features a **scope indicator** allowing the operator to switch between org, project, and task scopes using `[`/`]` keys without changing the main content view. Messages render with Glamour (markdown), support streaming token-by-token via SSE, and display tool calls inline. A **message queue** lets the operator stack messages while an agent turn is in progress, with edit/steer/delete actions. Vim-style keybindings are the default throughout (j/k navigation, h/l expand/collapse, `:` command palette, `/` search, `?` help).

Real-time updates flow through a persistent SSE connection with auto-reconnect and sequence-based catch-up for missed events. The TUI subscribes to the same event types as the web UI (chat deltas, task status changes, inbox items, merge queue updates, agent run events). The layout degrades gracefully for narrow terminals: sidebar abbreviates below 120 columns, hides below 100, and the TUI switches to single-panel mode below 80 columns. Session state (last view, active chat, panel proportions) is persisted locally to `~/.config/ottercamp/tui-state.json` and restored on next launch. The TUI maintains **full functional parity** with the web UI for all Phase 1-2 features; the web UI adds only visual richness (flow diagrams, drag-and-drop, inline images, rich media previews). Non-interactive CLI commands output plain text by default and JSON with `--json`, making them composable with standard Unix tools.

---

# 17. Terminal UI (TUI)

## Purpose

The TUI is the **first and primary interface** for OtterCamp. Phase 1 delivers synchronous chat via TUI before the web UI exists. Phase 2 adds project and task management. The TUI is not a secondary interface or a developer convenience tool — it is how the human operator interacts with OtterCamp for the first two phases of the product.

The web UI (doc 18) arrives in Phase 3 and adds visual richness — flow diagrams, drag-and-drop, rich media previews. But the TUI covers all functional capabilities. Everything you can do in the web UI, you can do in the TUI.

## Relationship to the `otter` CLI

The TUI is a mode of the `otter` binary, not a separate binary. The `otter` binary serves two purposes:

- **`otter <command>`** — non-interactive CLI commands. Quick, scriptable, pipe-friendly. Examples: `otter status`, `otter send "deploy the landing page"`, `otter inbox`, `otter task OC-5`.
- **`otter tui`** — launches the interactive TUI. Full-screen, persistent, real-time.

Both connect directly to the OtterCamp API (HTTP + SSE). There is no local CLI layer in between — the TUI is an API client, same as the web UI. Authentication is handled via stored credentials (API key or token, configured during `otter login`).

The CLI commands are useful for scripting and quick one-off actions. The TUI is for sustained interaction — chatting with agents, monitoring work, reviewing tasks, managing the operation.

## Framework

**Bubble Tea** (Go). The TUI is built with the Bubble Tea framework from Charm.

- Matches the Go codebase — no language boundary between the TUI and the rest of OtterCamp.
- Mature, well-supported, actively maintained. Large ecosystem of components (Lip Gloss for styling, Bubbles for common widgets, Huh for forms).
- Built for complex, multi-panel TUIs with real-time updates.
- Elm architecture (model-update-view) maps cleanly to reactive UI patterns.

Supporting libraries from the Charm ecosystem:

- **Lip Gloss** — styling, layout, borders, colors. Handles the visual design of panels, text formatting, and theme.
- **Bubbles** — pre-built components: text input, viewport (scrollable content), list, spinner, table, paginator.
- **Glamour** — markdown rendering in the terminal. Used for rendering agent responses, task descriptions, and any markdown content.
- **Huh** — interactive forms. Used for inline editing (queue message editing, inbox actions with notes).

## Layout

Three-panel layout adapted from the web UI spec (ui-spec-for-figma.md). Chat is always visible.

```
┌──────────────┬──────────────────────────────┬─────────────────────────────┐
│              │                              │                             │
│  Sidebar     │  Main Content                │  Chat Pane                  │
│  (left)      │  (center)                    │  (right, always visible)    │
│              │                              │                             │
│  Sessions    │  Dashboard / Project /       │  Active chat session        │
│  Projects    │  Task Board / Task Detail /  │  Streaming responses        │
│  Navigation  │  Inbox / Activity / Agents   │  Message input              │
│              │                              │                             │
└──────────────┴──────────────────────────────┴─────────────────────────────┘
```

### Panel Proportions

Default proportions: sidebar 20%, main content 40%, chat pane 40%. The chat pane is intentionally large — chat is the primary interface, not a sidebar accessory.

Panels are resizable via keybinding (see Keyboard Navigation). The operator can shift the ratio — collapse the sidebar entirely, expand main content for a wider task board, or maximize chat when focused on a conversation.

### Panel Focus

Exactly one panel has focus at any time. The focused panel:

- Receives all keyboard input.
- Has a visually distinct border (bright/highlighted vs dim).
- Shows context-appropriate keybinding hints in the status bar.

Tab and Shift-Tab cycle focus between panels (left to right, wrapping). The operator can jump directly to a panel with Alt-1 (sidebar), Alt-2 (main), Alt-3 (chat).

### Responsive Behavior

For narrow terminals (< 120 columns), the layout degrades gracefully:

- Below 120 columns: sidebar collapses to icons/abbreviated names.
- Below 100 columns: sidebar hides entirely. Toggle with a keybinding.
- Below 80 columns: single-panel mode. Only the focused panel is visible. Tab switches between panels (full-screen swap).

## Sidebar

The sidebar mirrors the session sidebar from the web UI spec — the operator's workspace navigator.

### Structure

```
 General                          ← org session (Frank)
 ─────────────────────────────
 OtterCamp V2                     ← project session (PM)
   Define auth architecture       ← task sync session
   Implement auth             *   ← task sync session (unread)
   Write chat spec                ← task sync session

 Client Portal                    ← project session (PM)
   Fix login bug              *   ← task sync session (unread)
   Plan Sprint 3                  ← task sync session
 ─────────────────────────────
 [N] Notifications    [I] Inbox
```

### Behaviors

- Sessions grouped by scope: org at top, then projects with their task sessions nested.
- Project labels shown in the sidebar, project view header, dashboard task-board header, and task context must use one shared resolver: `project.display_name` first, then `project.slug`, then a stable generic fallback such as "Untitled project". Raw `Project <id-fragment>` placeholders must not be shown to the operator.
- Project view open-task counts and task rows must follow the latest project-task reload/event stream. A stale detail snapshot must not keep showing `OPEN TASKS (0)` after live active tasks already exist for that project.
- A fresh kickoff binds the project row, project session entry, and downstream task/session surfaces to one canonical live project/session path. The TUI must not show one project while the worker is continuing a duplicate or archived session from an earlier run.
- **Unread indicator** (`*`) on sessions with unseen messages. Bubbles up: if any task in a project has unread, the project entry shows `*` too.
- Selecting a session (Enter) switches the chat pane to that session. Main content does not change.
- Selecting a task-scoped sidebar chat also binds the task detail right pane to that task's sync discussion session. If task detail data arrives without an explicit discussion session ID, the TUI keeps using the already-open sidebar task chat as the discussion binding.
- j/k navigates up/down the list. Enter selects. Folding/unfolding project groups with l/h (expand/collapse).
- Per-node async sessions (agent work logs) do not appear in the sidebar.
- Active session is highlighted with a distinct background.
- Notification count badge and inbox count visible at the bottom of the sidebar.

## Chat Pane

The chat pane is the core of the TUI. It is always visible and always shows the active chat session.

### Scope Indicator

At the top of the chat pane, a scope indicator shows the current session context and allows zooming between scope levels:

```
  [Task: Implement auth] [Proj: OtterCamp V2] [Org: General]
                              ^active
```

- Brackets show available scope levels based on the main content context.
- The active scope is highlighted.
- Navigate between scopes with `[` and `]` (previous/next scope level) when the chat pane has focus.
- Switching scope changes the chat session without changing the main content — the operator can talk to Frank (org scope) while looking at a task detail.

### Message Display

Messages render in a scrollable viewport:

```
  ┌ You ────────────────────────────────────────────────────┐
  │ What's the status on the auth implementation?           │
  └─────────────────────────────────────────────────────────┘

  ┌ Frank (Chief of Staff) ─────────────────────────────────┐
  │ The auth middleware task is in progress. Here's where    │
  │ things stand:                                           │
  │                                                         │
  │ - JWT validation: done                                  │
  │ - Session cookies: in progress (OC-5.2)                 │
  │ - Rate limiting: queued, waiting on session cookies     │
  │                                                         │
  │ The worker agent expects to finish by end of day.       │
  │ Want me to flag anything as urgent?                     │
  └─────────────────────────────────────────────────────────┘
```

**Message types and rendering:**

- **Human messages**: right-aligned or distinct top-border label ("You"). Standard text.
- **Agent messages**: left-aligned with agent name and role in the header. Text streams in real-time (character by character as tokens arrive).
- **Tool calls**: rendered inline during agent response as compact status lines. Collapsed by default, expandable with Enter.
  ```
    > read_file(src/auth/handler.go) ... done
    > search_code("validateToken") ... done (3 results)
    > write_file(src/auth/middleware.go) ... done
  ```
- **Tool results**: shown as success/error indicator. Large results collapsed behind "[show more]".
- **System messages**: de-emphasized, centered. Session start, context compression, limit notices.
- **Interjection messages**: from a different agent than the default responder. Visually distinct — different header style, label like "(interjected)".

### Rich Content in Terminal

Agent and human messages use the block content model (doc 02). The TUI renders each block type appropriately for the terminal:

- **Text blocks**: rendered as markdown via Glamour. Headings, bold, italic, lists, links, inline code — all rendered with terminal formatting (ANSI colors, bold/underline).
- **Code blocks**: syntax-highlighted with language tag (Glamour handles this). Scrollable if long. Copy to clipboard with a keybinding.
- **Image blocks**: displayed as `[Image: diagram.png]` with dimensions. Not rendered inline (terminal limitation). The operator can open images in their system viewer with a keybinding (Enter on the image reference).
- **File reference blocks**: rendered as a compact line: `[File: src/auth/handler.go @ abc123]`. Enter opens the file content in the main content pane or the operator's `$EDITOR`.
- **Artifact blocks**: rendered as `[Artifact: auth-design.pdf (2.3 MB)]`. Enter to open with system handler.

### Streaming

Agent responses stream in real-time — characters appear as tokens arrive from the model. The viewport auto-scrolls to follow the stream. During streaming:

- A spinner or animation indicates the agent is working.
- The agent's name and a "typing..." indicator appear.
- Tool calls appear inline as they execute.
- Escape cancels the current turn.
- The operator can scroll up to review history while streaming continues at the bottom. A "[jump to bottom]" indicator appears when scrolled up during active streaming.

### Message Input

The message input is a multi-line text area at the bottom of the chat pane:

- Enter sends the message (single-line default).
- Shift-Enter or Alt-Enter inserts a newline (multi-line input).
- Up arrow (when input is empty) recalls the previous message for editing.
- Tab completion for @mentions — type `@` and agent names autocomplete.
- The input area expands vertically as the operator types multi-line content, up to a configurable maximum before it scrolls internally.

### Message Queue

When the operator sends messages while an agent turn is in progress, queued messages appear below a divider:

```
  ┌ Frank ─── streaming... ─────────────────────────────────┐
  │ Looking at the auth module...                           │
  │   > read_file(src/auth/handler.go) ... done             │
  │   > search_code("validateToken") ...                    │
  └─────────────────────────────────────────────────────────┘

  ── queued ─────────────────────────────────────────────────
  You: "What about the error handling?"       [e]dit [s]teer
  You: "Also check the logging setup"         [e]dit [s]teer
```

- Each queued message shows inline action hints: `[e]dit`, `[s]teer`, `[d]elete`.
- Steer cancels the current turn and promotes that message to the front.
- Edit opens the message in the input area for modification.
- Delete removes the queued message.

### Reactions

Lightweight reactions adapted for terminal:

- `+` on a focused message: positive reaction (thumbs up).
- `-` on a focused message: negative reaction. Prompts for an optional note.
- Reactions shown as small indicators below messages: `[+1]` or `[-1 "factually incorrect"]`.
- Agent reactions on human messages shown subtly: `(Frank: +1)`.

### Active Turn Indicator

While an agent is working:

```
  Frank is responding...  [3.2s]  [Esc to cancel]
    > read_file(src/auth/handler.go) ... done
    > search_code("validateToken") ... running
```

Shows: which agent, elapsed time, tool activity, and how to cancel.

## Main Content Views

The main content panel shows different views depending on navigation context. The operator switches views via the command palette, sidebar selection, or keyboard shortcuts.

### Dashboard

The landing view on TUI launch. Quick status overview.

```
  OTTERCAMP                                          Feb 22, 2026
  ───────────────────────────────────────────────────────────────

  RUNTIME HEALTH                                  ATTENTION REQUIRED
  Running (3)         Stale (2)         Blocked (1)       Recent Failures (2)
  4 Task "Session management"            8m stale run lock      3m run failed
  5 Task "Billing retry"                 12m stale task         6m retry queued
  6 Task "Landing page polish"           Human input waiting    9m wakeup promoted

  INBOX (3 items)
  ! Escalation: Auth conflict                    OtterCamp V2
    Task Scoping: Landing page design            Client Portal
    Draft Review: Welcome email                  Client Portal

  PROJECTS
  OtterCamp V2     4 active  2 blocked  1 review    [12 total]
  Client Portal    2 active  0 blocked  1 review    [8 total]

  RECENT ACTIVITY
  2m ago   * Subtask "Token validation" completed     OC V2
  5m ago   > Task "Fix login bug" merged to main      Client
  8m ago   ! Blocker: "Missing API credentials"       OC V2
  12m ago  . Agent started "Session management"       OC V2
```

- Runtime health summarizes live execution state across the org. It highlights running work, stale tasks/executions, blocked items (including human-input waits), recent failures, and the most recent operator-relevant activity.
- The dashboard headline shifts between healthy and attention-required states based on stale work, blockers, or recent failures.
- Inbox section shows top items with urgency indicators.
- Project summary shows task counts by status category.
- Activity feed shows recent events across all projects, including failures, retries, promotions, and completions.
- When runtime targets are present, keys `4`-`9` jump directly into the linked task or project detail from the runtime health list.
- j/k navigates, Enter drills into the selected item.

### Project List

All projects with summary stats:

```
  PROJECTS
  ───────────────────────────────────────────────────────────────

  Name              Status    Tasks                    Active
  ─────────────────────────────────────────────────────────────
  OtterCamp V2      active    4 active, 2 blocked      yes
  Client Portal     active    2 active, 1 review       yes
  Personal Ops      active    1 active (scheduled)     yes
```

Enter on a project navigates to the project view.

### Task Board

Simplified kanban rendered in the terminal. Columns are the work status values:

```
  OTTERCAMP V2 - TASK BOARD
  ───────────────────────────────────────────────────────────────

  QUEUED (2)       IN PROGRESS (3)    BLOCKED (1)      REVIEW (1)
  ─────────────    ────────────────   ─────────────    ──────────
  OC-8 API docs    OC-5 Auth midlwr   OC-7 Deploy     OC-4 DB schema
    normal           high               waiting OC-5     @Reviewer
    @WorkerBot       [## ..]                              awaiting
                     3/5 subtasks

  OC-9 Tests       OC-6 Token svc
    normal           normal
                     [####.]
                   OC-10 Session
                     normal
```

- Each card shows: task ID, title (truncated), priority, assignee, flow progress bar, subtask summary.
- Flow progress rendered as a compact bar: `[##..]` (2 of 4 nodes done).
- j/k navigates within a column. h/l moves between columns. Enter opens task detail.
- No drag-and-drop (terminal limitation). Status changes happen through chat.

### Task Detail

Progressive disclosure: board card -> task detail -> work log.

```
  OC-5: Implement Auth Middleware                      HIGH
  Status: in_progress    Branch: task/implement-auth
  ───────────────────────────────────────────────────────────────

  FLOW
  [x Design] -> [* Implement] -> [ Code Review] -> [ Final Review]
                     ^current
                 3 subtasks (2 done, 1 in progress)

  SUBTASKS
  x  OC-5.1  JWT validation layer              done
  *  OC-5.2  Session cookie handling            in_progress
  .  OC-5.3  Rate limiting middleware           queued (depends on 5.1)

  DESCRIPTION
  Implement the auth middleware stack for the API service...

  ACCEPTANCE CRITERIA
  - JWT tokens validated on every authenticated endpoint
  - Session cookies supported as alternative auth method
  - Rate limiting per-user with configurable thresholds

  DEPENDENCIES
  Depends on: OC-3 Design auth architecture (done)
  Blocks:     OC-7 Deploy auth to staging (blocked)

  HISTORY                                              [h to expand]

  WORK LOG                                             [w to expand]
```

- Flow stepper rendered as a text progression with `[x]` done, `[*]` active, `[!]` blocked, `[ ]` pending.
- Subtask list with status indicators.
- Scrollable. Sections are collapsible with keybindings.
- Tab to switch focus to the chat pane for discussing the task.
- In task detail, the right-pane `Discussion` tab must stay bound to the task's real sync discussion session while the operator cycles tabs/scopes. The `No task discussion session.` placeholder appears only when no discussion session can actually be resolved for that task.
- In task detail, the right-pane `Journal`/work-log view must resolve the task's real task-scoped execution session. A same-project PM/project session is never a valid substitute for that task history.

### Inbox

The operator's action-required queue. Full inbox rendered in terminal:

```
  INBOX (3 pending, 2 deferred)
  ───────────────────────────────────────────────────────────────

  PENDING
  ! Escalation: Auth conflict                    OtterCamp V2
    Frank escalated -- needs your call on token format
    [o]pen in context  [d]efer

    Task Scoping: Landing page design            Client Portal
    PM finished scoping -- ready for your review
    [a]pprove  [r]equest changes  [d]efer

    Draft Review: Welcome email                  Client Portal
    Agent composed email under draft policy
    [a]pprove  [e]dit  [x]reject  [d]efer

  DEFERRED
    Task Scoping: Blog post series               OtterCamp V2
    Deferred 3 days ago
    [R]estore
```

- Ordered by urgency then arrival time.
- Each item shows inline action keybindings.
- Enough context to decide without navigating away.
- `o` (open in context) navigates main content to the relevant task and chat pane to the relevant session.

### Activity Feed

Real-time stream of system events:

```
  ACTIVITY FEED
  ───────────────────────────────────────────────────────────────

  2m ago   * Subtask "Token validation" completed     OC V2
  5m ago   > "Fix login bug" merged to main           Client
  8m ago   ! Blocker: "Missing API credentials"       OC V2
  12m ago  . Agent started "Session management"       OC V2
  15m ago  * Task "Update README" completed            Client
  20m ago  + PM scoped "Landing page design"           Client
```

- Reverse chronological.
- Events stream in real-time — new entries appear at the top as they happen.
- Scoped: on dashboard shows all projects; within a project view shows that project only.
- Enter on an entry navigates to the relevant task.
- Indicators: `*` completed, `>` merged, `!` blocker/escalation, `.` started, `+` created, `~` status change.

### Agent Status

Overview of agent activity:

```
  AGENTS
  ───────────────────────────────────────────────────────────────

  Name          Role              Status         Current Work
  ─────────────────────────────────────────────────────────────
  Frank         Chief of Staff    idle           --
  Lori          Agent Relations   idle           --
  Ellie         Memory            background     memory pipeline
  WorkerBot     Worker            active         OC-5.2 Session cookies
  Reviewer      Reviewer          active         OC-4 DB schema review
  ContentBot    Worker            queued         waiting for slot

  CONCURRENCY
  Active runs: 3/5 (global limit: 5)
  Queue depth: 2 tasks, 1 subtask
  Provider: anthropic 3/5 slots, openai 0/3 slots
```

- Shows each agent, their role, current status, and what they are working on.
- Concurrency overview: active runs vs limits, queue depth, per-provider utilization.

### Merge Queue

Visible from the project view:

```
  MERGE QUEUE - OtterCamp V2
  ───────────────────────────────────────────────────────────────

  Status    Task                          Branch
  ────────  ────────────────────────────  ──────────────────────
  merging   OC-4 DB schema               task/db-schema
  queued    OC-5 Auth middleware          task/implement-auth
  conflict  OC-3 Design docs             task/design-docs
              ! Non-trivial conflict -- PM notified
```

### Scheduled Tasks

Read-only view of schedules:

```
  SCHEDULES - Personal Ops
  ───────────────────────────────────────────────────────────────

  Name                  Cadence           Last Run       Next      Status
  ────────────────────  ────────────────  ────────────   ────────  ──────
  Check inbox           Every 10 min      2m ago (ok)    8m       active
  Daily metrics         Daily 6:00 AM     18h ago (ok)   6h       active
  Weekly roundup        Mon 9:00 AM       3d ago (ok)    4d       active
  Competitor scan       Wed 2:00 PM       5d ago         --       paused
```

## Keyboard Navigation

Vim-inspired keybindings as the default. All keybindings are listed in a help overlay accessible with `?`.

### Global (Available in Any Panel)

| Key | Action |
|---|---|
| Tab | Focus next panel (left -> center -> right -> left) |
| Shift-Tab | Focus previous panel |
| Alt-1 | Focus sidebar |
| Alt-2 | Focus main content |
| Alt-3 | Focus chat pane |
| `:` | Open command palette |
| `/` | Search (context-dependent) |
| `?` | Show keybinding help overlay |
| Ctrl-C | Quit TUI |

### Sidebar (When Focused)

| Key | Action |
|---|---|
| j / Down | Next item |
| k / Up | Previous item |
| Enter | Select session (loads in chat pane) |
| l / Right | Expand project group |
| h / Left | Collapse project group |
| n | Jump to notifications |
| i | Jump to inbox (opens in main content) |

### Main Content (When Focused)

| Key | Action |
|---|---|
| j / Down | Next item in list/board |
| k / Up | Previous item |
| h / Left | Previous column (task board) |
| l / Right | Next column (task board) |
| Enter | Drill into selected item |
| Escape / Backspace | Go back / up one level |
| r | Refresh current view |
| g | Go to top of list |
| G | Go to bottom of list |

### Chat Pane (When Focused)

| Key | Action |
|---|---|
| Enter | Send message (single-line mode) |
| Shift-Enter / Alt-Enter | Newline in input |
| Escape | Cancel current agent turn |
| Up (empty input) | Recall previous message |
| `@` + typing | Agent mention autocomplete |
| `[` | Switch to previous scope level |
| `]` | Switch to next scope level |
| Ctrl-U | Scroll up in message history |
| Ctrl-D | Scroll down in message history |
| `+` | Positive reaction on focused message |
| `-` | Negative reaction on focused message |
| `e` | Edit queued message |
| `s` | Steer (promote queued message) |

### Task Detail (When Focused)

| Key | Action |
|---|---|
| w | Toggle work log expansion |
| h | Toggle history expansion |
| f | Focus flow stepper (navigate nodes with h/l) |
| Enter | Drill into selected subtask |
| Escape | Back to task board |

### Inbox (When Focused)

| Key | Action |
|---|---|
| a | Approve selected item |
| r | Request changes / reject with feedback |
| d | Defer selected item |
| o | Open in context (navigate to relevant task + session) |
| e | Edit (for draft review items) |

## Command Palette

The `:` prefix opens a command palette for quick navigation and actions. Commands autocomplete with fuzzy matching.

### Navigation Commands

| Command | Action |
|---|---|
| `:dashboard` | Go to dashboard |
| `:project <name>` | Go to project by name (fuzzy match) |
| `:task <id>` | Go to task by ID (e.g., `:task OC-5`) |
| `:inbox` | Go to inbox |
| `:activity` | Go to activity feed |
| `:agents` | Go to agent status |
| `:schedules` | Go to schedules view |
| `:merges` | Go to merge queue |

### Session Commands

| Command | Action |
|---|---|
| `:chat <session>` | Switch chat pane to session by name (fuzzy match) |
| `:general` | Switch to org session (Frank) |
| `:scope org` | Switch chat to org scope |
| `:scope project` | Switch chat to project scope |
| `:scope task` | Switch chat to task scope |

### Action Commands

| Command | Action |
|---|---|
| `:send <message>` | Send a message in the active chat session |
| `:mention <agent>` | Insert @mention in the input |
| `:cancel` | Cancel the current agent turn |
| `:refresh` | Refresh current view |

### System Commands

| Command | Action |
|---|---|
| `:theme <name>` | Switch theme |
| `:resize <panel> <cols>` | Resize a panel |
| `:keybindings` | Show full keybinding reference |
| `:quit` | Exit the TUI |

All commands support fuzzy matching — `:proj otter` matches `:project OtterCamp V2`. Tab completes the first match. Arrow keys navigate suggestions.

## Real-Time Updates

The TUI maintains a persistent SSE connection to the OtterCamp API for real-time updates. Events drive UI updates without polling.

### What Updates in Real-Time

- **Chat**: agent response tokens stream character by character. New messages from other agents (interjections) appear immediately. Message state transitions (pending -> streaming -> final) reflected visually.
- **Task status**: work status changes reflected on the task board, task detail, and dashboard. Cards move between columns. Status badges update.
- **Queue depth**: concurrency utilization and queue depth update on the agent status view and dashboard.
- **Inbox**: new inbox items appear with a count badge update in the sidebar. The inbox view updates live.
- **Activity feed**: new events stream in at the top of the feed.
- **Unread indicators**: sidebar session entries update their unread markers as new messages arrive in sessions the operator is not currently viewing.
- **Notifications**: new notification count updates in the sidebar badge.
- **Merge queue**: merge status changes (queued -> merging -> merged/conflict) update live.

### SSE Event Mapping

The TUI subscribes to the same event types the web UI would (doc 02, doc 12):

- `chat.message.created` -> new message in chat pane (if active session) or unread indicator (if not)
- `chat.message.delta` -> streaming text appended to the active agent message
- `chat.message.finalized` -> message state updated to final
- `chat.turn.started` -> active turn indicator shown
- `chat.turn.completed` -> turn indicator cleared, tool call summary finalized
- `chat.listening_eval.completed` -> brief pause then interjection messages if any
- `task.status.changed` -> task board card moves, task detail badge updates
- `task.flow.advanced` -> flow stepper updates
- `inbox.item.created` -> inbox count increments, new item appears if inbox is visible
- `agent.run.started` / `agent.run.completed` -> agent status view updates
- `merge.status.changed` -> merge queue view updates

### Connection Management

- Auto-reconnect on connection drop with exponential backoff.
- Missed events recovered via a sequence-based catch-up mechanism (the TUI tracks the last event sequence it received and requests missed events on reconnect).
- Connection status indicator in the status bar: connected (green dot), reconnecting (yellow), disconnected (red).

## Status Bar

A persistent status bar at the bottom of the terminal shows context and hints:

```
  [Connected]  OC V2 > OC-5  |  Chat: Implement auth (task)  |  Inbox: 3  |  ?=help  :=cmd
```

- Connection status.
- Current navigation context (project > task).
- Active chat session name and scope.
- Inbox item count.
- Worker-offline warnings are only shown when there is no recent worker heartbeat/activity signal; an actively claiming or heartbeat-producing worker must not be labeled offline.
- Keybinding hints for command palette and help.

The status bar also shows contextual keybinding hints based on the focused panel and current state.

## Theming

- **Dark mode** is the default (consistent with the web UI design principle).
- A minimal light mode is available via `:theme light`.
- Colors use the terminal's color palette (16-color safe, with 256-color and true-color enhancements when supported).
- Borders, highlights, and status indicators are styled via Lip Gloss and are theme-aware.
- Agent messages can be color-coded by agent (e.g., Frank always in blue, Lori in green) for quick visual identification.

## Limitations vs Web UI

The TUI covers full functional parity for Phase 1 and Phase 2 features. The web UI adds visual richness that the terminal cannot match:

| Capability | TUI | Web UI |
|---|---|---|
| Chat (full) | Yes | Yes |
| Streaming responses | Yes | Yes |
| @mentions, multi-agent turns | Yes | Yes |
| Scope switching | Yes (`[`/`]` keys) | Yes (scope pill) |
| Task board | Text-based kanban | Visual kanban with drag-and-drop |
| Task detail | Full text layout | Rich layout with sections |
| Flow visualization | Text stepper `[x]->[*]->[ ]` | Visual diagram with node graphics |
| Subtask management | Text list | Interactive list |
| Inbox | Full actions | Full actions with rich previews |
| Activity feed | Text stream | Styled stream with animations |
| Code highlighting | Terminal ANSI colors | Full syntax highlighting |
| Image preview | Reference only (open externally) | Inline rendering |
| File uploads | Via file path in message | Drag-and-drop + file picker |
| Rich media | Open externally | Inline preview |
| Agent avatars | Name + color | Visual avatar |
| Merge queue | Text table | Visual status |
| Scheduled tasks | Text table | Visual table |
| Reactions | `+`/`-` keys | Double-click / right-click |
| Command palette | `:` prefix | Superhuman-style overlay |

**What the TUI will never do:**
- Render images inline (terminal limitation).
- Drag-and-drop (no mouse-based reordering).
- Render flow diagrams as visual graphs (text representation instead).
- Show rich media previews inline (opens externally).
- Support file upload via drag-and-drop (use file path references instead).

**What the TUI does that the web UI does not:**
- Pipe-friendly output from CLI commands (`otter inbox | grep urgent`).
- Integration with shell workflows (`otter send "$(git log --oneline -5)"`).
- Works over SSH to a remote server.
- Zero browser overhead — instant startup.

## File Handling in the TUI

Since the TUI cannot render images or rich media inline, file interactions work differently:

- **File references in messages**: displayed as clickable-ish references. Enter on a file reference opens the file in `$EDITOR` (or a pager for read-only).
- **Sharing files in chat**: the operator types a file path and it is included as an artifact block. Example: the operator can reference a local file by path in their message, and the TUI handles upload.
- **Artifacts from agents**: displayed as references with name, type, and size. Enter opens with the system default handler (`open` on macOS, `xdg-open` on Linux).
- **Image artifacts**: displayed as `[Image: name.png (800x600, 45KB)]`. Enter opens in the system image viewer.

## Startup and Session Persistence

### Launch

`otter tui` launches the TUI. On launch:

1. Authenticate with the API (using stored credentials from `otter login`).
2. Load the operator's session list, inbox count, and dashboard data.
3. Open to the dashboard view in main content, with the org session (General / Frank) in the chat pane.
4. Establish the SSE connection for real-time updates.
5. If the operator had a previous TUI session, restore the last view and active chat session (see Session Persistence).

### Session Persistence

The TUI saves its UI state locally (in `~/.config/ottercamp/tui-state.json` or equivalent XDG path):

- Last active view (which main content view was open).
- Last active chat session.
- Panel proportions if customized.
- Sidebar collapse state.

On next launch, this state is restored so the operator picks up where they left off.

### Restart Readiness Gate

Before restarting a live operator session for a full end-to-end run, operators must pass an automated restart-readiness smoke gate. The gate is fail-closed: no restart proceeds until the runtime proves singleton kickoff, live `in_progress` execution state, on-disk task output advancing to `review`, long-running continuation/resume behavior, and TUI reconciliation of project/task/worker truth after live data reloads. The TUI must not keep showing stale zero-open-task state or a false worker-health picture once the latest project/task/session data is available.

### Graceful Shutdown

Ctrl-C or `:quit` exits the TUI. Before exiting:

- Save UI state for next launch.
- Close the SSE connection cleanly.
- Any in-progress message draft is preserved in the saved state.

## Non-Interactive CLI Commands

The `otter` binary also provides non-interactive commands for scripting and quick actions. These are not part of the TUI but share the same binary and API connection.

| Command | Description |
|---|---|
| `otter status` | Quick overview: inbox count, active tasks, queue depth |
| `otter inbox` | List inbox items with status |
| `otter inbox approve <id>` | Approve an inbox item |
| `otter send "<message>"` | Send a message to the active session (or specify with `--session`) |
| `otter send --to frank "<message>"` | Send a message to a specific agent |
| `otter task <id>` | Show task detail |
| `otter tasks [--project <name>]` | List tasks, optionally filtered by project |
| `otter projects` | List projects |
| `otter agents` | List agents and their status |
| `otter activity` | Show recent activity feed |
| `otter login` | Authenticate and store credentials |

CLI commands output plain text by default, JSON with `--json` flag. This makes them composable with standard Unix tools.

## Phase Delivery

### Phase 1: Synchronous Chat via TUI

The minimum viable TUI. The operator can chat with agents.

- Three-panel layout with sidebar, main content (dashboard only), and chat pane.
- Chat pane: message display, streaming responses, message input, @mentions.
- Sidebar: org session listed. Project/task sessions appear as they are created in Phase 2.
- Scope indicator (org only in Phase 1).
- Keyboard navigation: Tab between panels, j/k in lists, Enter to select, Escape to cancel.
- Command palette with basic navigation commands.
- SSE connection for real-time streaming.
- Status bar with connection status and keybinding hints.
- Non-interactive CLI commands: `otter send`, `otter status`, `otter login`.

### Phase 2: Projects and Tasks

Full task management in the TUI.

- Task board view (text kanban).
- Task detail view with flow stepper, subtasks, dependencies, history.
- Inbox view with action keybindings.
- Activity feed (real-time).
- Agent status view.
- Merge queue view.
- Scheduled tasks view.
- Project list and project detail.
- Scope switching (`[`/`]`) with project and task scopes.
- Full sidebar with project groups and task sessions.
- Expanded command palette with task/project/inbox commands.
- Expanded CLI commands: `otter task`, `otter tasks`, `otter inbox approve`, etc.

### Phase 3: Web UI Arrives

The TUI continues to be maintained and supported alongside the web UI. It remains the preferred interface for operators who work in the terminal. No features are removed from the TUI when the web UI launches.

## Resolved Decisions

- **Framework**: Bubble Tea (Go). Natural fit for the Go codebase. Mature, well-supported, good for complex TUIs. Supporting libraries from the Charm ecosystem (Lip Gloss, Bubbles, Glamour, Huh).
- **TUI is a mode of the `otter` CLI binary, not a separate binary.** `otter tui` launches the interactive TUI. `otter <command>` runs non-interactive CLI commands. Both connect to the same API.
- **TUI connects directly to the API (HTTP + SSE).** Same as the web UI. No local CLI layer or intermediary. The TUI is an API client.
- **Full feature parity with web UI for Phase 1-2 features.** The web UI adds visual richness (flow diagrams, drag-and-drop, rich media previews, inline images) but the TUI covers all functional capabilities. Everything you can do in the web UI, you can do in the TUI.
- **Vim-style keybindings as default.** j/k for navigation, h/l for expand/collapse, Enter to select, Escape to go back. `:` for command palette, `/` for search, `?` for help. Keybindings are customizable.
- **Three-panel layout matches the web UI.** Sidebar, main content, chat pane. Chat pane is always visible. Panels are independently navigable.
- **Tab/Shift-Tab for panel focus cycling.** Alt-1/2/3 for direct panel focus. Exactly one panel has focus at a time.
- **Scope switching via `[`/`]` keys** in the chat pane, matching the web UI's scope pill. Same concept, keyboard-native interaction.
- **Dark mode default.** Light mode available. Colors use terminal palette with 256/true-color enhancements when available.
- **Images and rich media open externally.** The terminal cannot render images inline. File references and artifacts open in the system default handler or `$EDITOR`.
- **SSE for real-time updates.** Same event stream as the web UI. Auto-reconnect with exponential backoff. Sequence-based catch-up for missed events.
- **Session state persisted locally.** TUI saves last view, active session, and panel layout to restore on next launch.
- **Non-interactive CLI commands share the `otter` binary.** `otter send`, `otter inbox`, `otter task`, etc. Plain text output by default, JSON with `--json`. Composable with Unix tools.
- **Responsive degradation for narrow terminals.** Three breakpoints: sidebar abbreviation at 120 cols, sidebar hidden at 100 cols, single-panel mode at 80 cols.

## Open Questions

- **Keybinding customization mechanism**: config file format and location for custom keybinding overrides. Likely `~/.config/ottercamp/keybindings.toml` but exact format TBD.
- **Mouse support**: Bubble Tea supports mouse events. Should the TUI support mouse clicking on sidebar items, message reactions, etc., or stay purely keyboard-driven? Leaning toward optional mouse support for convenience without requiring it.
- **Notification delivery in TUI**: beyond the count badge, should the TUI show a brief notification popup (toast-style) when urgent events arrive? Or rely on the badge and let the operator check when ready?
- **Copy/paste handling**: how to handle copying message content, code blocks, or file references to the system clipboard from within the TUI. Bubble Tea has clipboard support but platform behavior varies.
- **Terminal multiplexer compatibility**: testing and accommodating behavior within tmux, screen, and similar environments. Some key combinations may conflict.
