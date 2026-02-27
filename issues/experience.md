# OtterCamp TUI — Experience Improvements

> Ideas discovered during hands-on testing. Each one is implemented and verified.

---

## EX-001: Remove debug state lines from panel footers

**Observation:** Every panel had a debug status line at its bottom (inside the border), showing raw internal state like "Sidebar state: ready | current=General / Frank unread=0", "Main state: ready | view=dashboard size=M tasks(todo=1,...)", "Chat state: ready | messages=12 queued=0". These lines consumed one row of content space in each panel and felt like a development artifact accidentally shipped.

**Improvement:** Removed the three debug state footer lines from renderSidebarPanel, renderMainPanel, and renderChatPanel in view.go. Reclaimed the lost content row in each panel. Updated golden test files and flipped quality_matrix_test.go to assert debug lines are NOT present.

**Why it matters:** Debug state text leaking into the production UI looks unfinished and distracts from content. Every power user will notice it and wonder if the app is broken. Removing it makes the TUI feel polished.

**Effort:** Low
**Issue:** #144
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-002: Context-aware help line replaces truncated static list

**Observation:** The help line at the bottom always showed the same long string ":focus sidebar|main|chat | :frank | :dashboard/:project/:task/:inbox | :send | :cancel-turn | PgUp/PgDn chat scroll | :tour dismiss | :quit" which was always truncated by the terminal width, cutting off at "canc". Users saw the same irrelevant hints no matter which panel they were in.

**Improvement:** The help line now shows context-aware keybindings based on the focused panel and current view. Sidebar focus shows j/k/h/l hints; inbox view shows a/x/f/o approve/reject keys; chat focus shows Enter/PgUp/PgDn/Esc; command mode shows available commands.

**Why it matters:** The most helpful information is what's relevant right now. A sidebar user doesn't need to know about PgDn chat scroll; an inbox user needs to see the a/x/f/o actions. Showing the right hint at the right time eliminates the "what do I press?" friction.

**Effort:** Low
**Issue:** #145
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-003: ? key opens keybinding reference screen

**Observation:** The ? key did nothing — no feedback, no help screen. The help line at the bottom was the only reference, but it was always truncated. First-time users had to guess or read external docs to find keyboard shortcuts.

**Improvement:** Press ? from any panel to open a full keybinding reference in the main panel. Organized into sections: Navigation, Chat, Main Panel, Commands. Each entry shows the key and its description in aligned columns. Press ? again or Esc to close.

**Why it matters:** Self-documenting UIs dramatically reduce the learning curve. Having all keybindings available at a single ? keypress means users never need to leave the TUI to look up how to do something.

**Effort:** Medium
**Issue:** #146
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-004: Chat scroll position indicator when not at bottom

**Observation:** When scrolling up through chat history, there was no indication of how many messages were below or how to get back to the bottom. Users had to keep pressing PgDn blindly until content stopped changing.

**Improvement:** When the viewport is scrolled up (messages exist below the fold), a centered "↓ N more · PgDn to scroll" hint appears above the input box, showing exactly how many lines remain. The hint disappears automatically when scrolled to the bottom.

**Why it matters:** Without a scroll position indicator, users don't know how deep into history they've gone or how many new messages they're missing. The hint creates closure — you can see exactly what's below and exactly how to get there.

**Effort:** Low
**Issue:** #147
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-005: Human-readable session label in chat panel header

**Observation:** The chat panel header showed the raw session ID ("session-org-general") instead of the human-readable name ("General / Frank"). Users had no way to know which session they were chatting in without navigating to the sidebar.

**Improvement:** Added `sessionLabel()` to workspaceState that looks up the sidebar node's display label for a session ID. The chat panel header now shows "General / Frank" (or the task session name like "Task 1 / Launch docs") instead of the raw ID.

**Why it matters:** Session IDs are implementation details. Users think in terms of "I'm talking to Frank" not "session-org-general". Showing human-readable names makes the interface feel coherent.

**Effort:** Low
**Issue:** #148
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-006: Status bar shows human-readable session label

**Observation:** The status bar at the bottom showed the raw session ID or "none" for the current session. After EX-005 fixed the chat header, the status bar still showed internal IDs.

**Improvement:** Status bar now uses `sessionLabel()` to show the friendly display name (e.g., "General / Frank" or "Frank" as default) instead of "session-org-general" or "none".

**Why it matters:** Consistent naming across all UI surfaces reduces confusion. Users should see the same name for the same session everywhere.

**Effort:** Low
**Issue:** #149
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-007: Task status uses human-readable Title Case labels

**Observation:** The task detail view showed raw status strings like "todo", "in_progress", "done" — lowercase with underscores, clearly unformatted internal values.

**Improvement:** Added `formatTaskStatus()` helper that maps raw status strings to Title Case labels: "todo" → "Todo", "in_progress" → "In Progress", "done" → "Done", "blocked" → "Blocked", etc. Used in the task detail view.

**Why it matters:** "In Progress" reads as polished UI copy. "in_progress" reads as a database column name. Small formatting details signal attention to craft.

**Effort:** Low
**Issue:** #150
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-008: Dashboard shows "+N more tasks" when board is truncated

**Observation:** The dashboard task board silently truncated to 4 rows. If there were 10 tasks, 6 were hidden with no indication — users had no way to know they existed.

**Improvement:** When more than 4 task rows are hidden, a "+N more tasks" line appears below the visible rows, telling users exactly how many tasks are off-screen.

**Why it matters:** Silent truncation creates invisible data. Users need to know what exists even if they can't see it inline — otherwise they might miss critical in-progress work.

**Effort:** Low
**Issue:** #151
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-009: Command mode shows ▌ cursor in input box

**Observation:** When pressing `:` to enter command mode, the input box turned amber (border color change) but showed no cursor. It wasn't obvious that keystrokes were being captured.

**Improvement:** Command mode now always shows the ▌ cursor after the current command text, confirming that the input box is active and capturing keypresses.

**Why it matters:** Visual confirmation of input focus is a basic usability requirement. Without a cursor, users may type commands into the void and wonder why nothing happens.

**Effort:** Low
**Issue:** #152
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-010: Active + cursor sidebar node shows ✓ badge

**Observation:** When the keyboard cursor happened to be on the currently active chat session in the sidebar, it looked identical to the cursor being on a different (inactive) session. Users couldn't tell the difference between "I'm hovering here" and "this is my current session".

**Improvement:** When the cursor lands on the active session, a green ✓ badge appears to the right of the label, making the dual state (cursor + active) visually distinct from cursor-only selection.

**Why it matters:** The visual difference between "I'm looking at" and "I'm in" is essential for navigation confidence. Without it, users don't know if Enter would change sessions or confirm the current one.

**Effort:** Low
**Issue:** #153
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-011: Down arrow advances through chat history (forward navigation)

**Observation:** Pressing Up when the chat input was empty recalled the last sent message. But pressing Down only scrolled the chat viewport. There was no way to navigate forward through history once you'd gone back.

**Improvement:** Added `forwardHistory()` method. When in history navigation mode (after pressing Up at least once), Down advances toward the newest message. Reaching the newest entry clears the input, returning to normal typing mode.

**Why it matters:** History navigation is only useful if you can go in both directions. Up-only history navigation forces users to manually delete the recalled text and retype from scratch.

**Effort:** Low
**Issue:** #154
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-012: Sidebar title shows total unread session count

**Observation:** Unread badges appeared on individual session nodes (orange number in parentheses), but there was no aggregate count at the top level. To know if any session had unreads, users had to scan the entire sidebar.

**Improvement:** The sidebar title "SESSIONS" now shows a total unread count badge (e.g., "SESSIONS (5)") when any session has unread messages. The badge uses the orange unread color for immediate recognition.

**Why it matters:** The sidebar title is always visible. An aggregate unread count lets users know at a glance whether anything needs attention without scrolling through all sessions.

**Effort:** Low
**Issue:** #155
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-013: 'q' key closes help screen

**Observation:** The help screen (opened with ?) could only be closed with ? again or Esc. The 'q' key (universally used for "quit/close" in terminal UIs like vim, less, man pages) did nothing.

**Improvement:** Pressing 'q' from any non-chat panel while the help screen is open closes it and returns to the dashboard. The behavior matches 'q' in vim's help, less, and other terminal pagers.

**Why it matters:** Users follow muscle memory. 'q' to close a reference screen is universal terminal convention. Not supporting it creates friction for power users.

**Effort:** Low
**Issue:** #156
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-014: Main panel titles are human-readable

**Observation:** The main panel title showed the raw view name uppercased: "TASK" (not "TASK DETAIL"), "MERGES" (not "MERGE QUEUE"), "HELP" just showed "HELP". The titles were ambiguous.

**Improvement:** Added `mainViewTitle()` that maps view constants to proper human-readable titles: "TASK DETAIL", "MERGE QUEUE", "HELP" for the help screen (previously showed the generic upcase). "DASHBOARD" and "INBOX" stay the same.

**Why it matters:** Panel titles are navigation landmarks. "TASK DETAIL" immediately communicates what you're looking at. "TASK" is ambiguous (is it a list? a single task? the task board?).

**Effort:** Low
**Issue:** #157
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-015: Activity, inbox, and agents views show item count in panel title

**Observation:** Views like "INBOX", "ACTIVITY", and "AGENTS" gave no indication of how many items were present until you scrolled through the content. The count information was available but not surfaced.

**Improvement:** When these views have items, the panel title shows a count badge: "INBOX (2)", "ACTIVITY (3)", "AGENTS (3)". The count updates dynamically as items are added or removed.

**Why it matters:** Counts tell users whether a view is worth navigating to before they switch focus. "INBOX (5)" immediately communicates workload. "INBOX" alone requires a context switch to check.

**Effort:** Low
**Issue:** #158
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-016: Chat input shows character count when message is long

**Observation:** The chat input had no length indicator. For long messages (instructions, code snippets), users had no way to gauge message length or know if they were approaching limits.

**Improvement:** When the chat input exceeds 100 characters, a `[N]` character count appears inline before the cursor (e.g., `my long message [142]▌`). The count disappears automatically for short messages.

**Why it matters:** Length awareness helps users self-edit before sending. It's also useful for understanding token consumption when working with AI agents.

**Effort:** Low
**Issue:** #159
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-017: All queued messages visible in chat panel

**Observation:** When an agent turn was active and messages were queued (e.g., 3 messages waiting to be sent), only the first queued message was shown. The others were invisible.

**Improvement:** All queued messages (up to 3) are now shown with numbered prefixes (q1:, q2:, q3:). If more than 3 are queued, a "+N more queued" overflow indicator appears. Steer flags are shown per-message.

**Why it matters:** Hidden state creates surprises. Users need to see all queued messages to manage them (edit, reorder, delete) confidently.

**Effort:** Low
**Issue:** #160
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-018: Sidebar task sessions show task status icon

**Observation:** Task-linked sessions in the sidebar (e.g., "Task 1 / Launch docs") had no visual indication of the task's current status. Users had to navigate to the task detail view to check status.

**Improvement:** Task sessions now show a colored status icon to the right of their label: ○ (todo, gray), ◌ (in-progress, amber), ● (done/approved, green), ⚠ (blocked/rejected, red). The icon updates when task status changes.

**Why it matters:** Task status is critical ambient information. Surfacing it in the sidebar means users can see the health of all tasks at a glance without leaving their current context.

**Effort:** Low
**Issue:** #161
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-019: Inbox count shown in panel title

**Observation:** Same issue as EX-015 but specifically noted for inbox: navigating to the inbox view showed "INBOX" with no item count. The inbox count was only visible in the dashboard divider.

**Improvement:** Combined with EX-015 — inbox view title shows "INBOX (N)" when items are pending.

**Why it matters:** Inbox count in the title confirms the count before and while you're in the inbox, making it easier to track progress as you approve/reject items.

**Effort:** Low
**Issue:** #158 (combined with EX-015)
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-020: Degraded mode banner truncates gracefully on narrow terminals

**Observation:** The DEGRADED MODE banner message was a long fixed string that overflowed on narrow terminals, pushing content off-screen or wrapping awkwardly.

**Improvement:** `degradedModeBanner()` now measures available terminal width and truncates the description text with "…" to fit. The icon and "DEGRADED MODE:" label are always fully shown; only the explanation truncates.

**Why it matters:** Degraded mode is when users most need the UI to work correctly. A broken banner during degraded mode would compound the already-bad situation.

**Effort:** Low
**Issue:** #162
**Status:** [x] Discovered | [x] Filed | [x] Implemented | [x] Tested

---

## EX-021: OC-N task numbers in sidebar and dashboard

**Observation:** Tasks were displayed by title only (e.g., "Flow kickoff fix test") with no unique identifier. In a project with many tasks, it was hard to distinguish items or reference them in conversation.

**Improvement:** All task displays now prefix with `OC-N` (e.g., "OC-6: Flow kickoff fix test") in the PROJECTS sidebar task nodes, dashboard task board columns, task detail header, and project detail task list.

**Why it matters:** Task numbers give users a quick reference identifier for discussion and navigation. "OC-6 is blocked" is clearer than "that Flow kickoff task that's... wait, which one?"

**Effort:** Medium
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-022: OC-N prefix in CHATS sidebar for task-scoped sessions

**Observation:** Recent chat sessions in the CHATS section showed bare session titles or bare task titles with no task number context. A session named "Iter40 Scheduler Run Completion Test" gave no indication of which task it was for.

**Improvement:** For `project_task`-scoped sessions, the CHATS section now shows `OC-N: {session title}` (e.g., "OC-6: Iter40 Scheduler Run Completion Test"), always fetching the task number even if the session has a custom title.

**Why it matters:** Users need to see at a glance which task a chat session belongs to, especially when they have multiple task sessions active simultaneously.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-023: Clicking CHATS task session navigates to task detail

**Observation:** Clicking a task-scoped session in the CHATS sidebar would switch the chat to that session but leave the main panel showing whatever was already there (dashboard, inbox, etc.). Users had to separately navigate to the task.

**Improvement:** When a user selects a `project_task`-scoped session in CHATS, the main panel automatically navigates to ViewTask showing that task's detail (title, status, description) and loads the task record from the API if needed.

**Why it matters:** The whole point of clicking on a task session is to work on that task. Making the task detail appear automatically saves two extra key presses and makes the workflow feel cohesive.

**Effort:** Medium
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-024: Scope indicator updates when switching CHATS sessions

**Observation:** The chat panel header always showed `[org]` as the active scope even when the selected session was scoped to a task (`[task]`) or project (`[project]`).

**Improvement:** Session nodes now carry their `ScopeType` from the API. When a user selects a session in CHATS, the scope indicator updates to reflect the session's actual scope: `[task]` for project_task sessions, `[project]` for project sessions, `[org]` for organization sessions.

**Why it matters:** The scope badges tell users what context their chat is operating in. Showing the wrong scope is actively misleading — it made every session look like an org-scope session.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-025: r key now triggers actual sidebar data refresh

**Observation:** The `r` key added an activity log entry saying "manual refresh requested" but didn't actually re-fetch any data from the API. The sidebar content only updated on startup.

**Improvement:** `r` now dispatches `loadSidebarDataCmd` to re-fetch inbox count, recent chats, and projects from the API. The status bar shows "Refreshing sidebar data…" while the request is in flight.

**Why it matters:** Real-time sync is great, but when something looks stale or after a long session, users want a way to force a fresh fetch. Without a working refresh, they'd have to quit and restart the TUI.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-026: Interactive task cursor in project view

**Observation:** The project detail view showed open tasks as a static list with no way to navigate or select them. Users had to navigate via the sidebar to open a specific task.

**Improvement:** Added `projectTaskCursor` navigation: j/k/↑/↓ move a highlighted cursor through the open tasks list, and Enter opens the selected task's detail view. The cursor is highlighted with the focus style when the main panel is active.

**Why it matters:** The project view is the natural starting point for task work. Being able to navigate tasks directly from the project view without touching the sidebar is much faster than the 4-step sidebar route.

**Effort:** Medium
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-027: Smart Escape navigation from task detail

**Observation:** Pressing Escape from the task detail view always returned to the dashboard, even if the user had navigated to the task from within a project view.

**Improvement:** Escape from ViewTask now checks if `selectedProjectID` is set. If so, it returns to ViewProject (the project the task belongs to) instead of jumping all the way back to the dashboard.

**Why it matters:** Navigation should respect the path you took to get somewhere. If you came from a project, pressing Escape should return you to that project, not reset your entire navigation state.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-028: Per-column overflow indicator in dashboard task board

**Observation:** When more than 4 tasks existed in any column, the "+N more tasks" overflow indicator always appeared under the leftmost (TODO) column regardless of which column had overflow.

**Improvement:** The overflow row is now computed per-column: each of TODO, IN PROGRESS, and DONE columns independently shows "+N more" only for their own overflow. Columns without overflow show nothing.

**Why it matters:** A misaligned indicator is confusing — it made it look like TODO had overflow when actually IN PROGRESS did. Now the count accurately tells you which column has hidden tasks.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-029: Task sort order by number in sidebar and dashboard

**Observation:** Tasks appeared in a random order determined by UUID string sort order. New tasks (with higher OC-N numbers) appeared mixed throughout the list.

**Improvement:** Tasks are now sorted by task number descending (newest first) in both the PROJECTS sidebar and the dashboard task board. This brings the most recently created tasks to the top.

**Why it matters:** The most recently created tasks are typically the ones being actively worked on. Showing them first puts the relevant work in front of users immediately.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-030: Status bar shows current view name when main panel is focused

**Observation:** When the main panel was focused, the status bar showed "main" for the focus indicator, which was not descriptive. Users couldn't tell at a glance whether they were looking at the dashboard, a project, a task, or the inbox.

**Improvement:** When the main panel is focused, the focus indicator now shows the actual view name (dashboard, project, task, inbox, etc.) instead of the generic "main" label.

**Why it matters:** The status bar is the user's constant reference for "where am I". Showing "XL/dashboard" vs "XL/task" makes orientation much clearer.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-031: Chat panel switches to Frank when selecting a project

**Observation:** When a task session was active in the chat panel (e.g., "OC-4: Test Flow Template Task") and the user clicked a project node in the sidebar, the chat header continued showing the task session while the scope badge said `[project]`. This was confusing — the chat was operating in task context while the main panel showed the project.

**Improvement:** When clicking a project node in the sidebar, the chat panel automatically switches to Frank (the org-level session) and the scope indicator updates to `[project]`. This makes the chat context consistently reflect "I'm in a project" rather than the stale task session.

**Why it matters:** Chat context and navigation context should always match. Showing a task session while browsing a different project's dashboard creates disorientation — users might ask Frank a question thinking it's scoped to the project, but the session was actually scoped to a task they left 10 minutes ago.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-032: Dashboard DONE column shows real completed task count

**Observation:** The dashboard task board's DONE column always showed (0) even when tasks had been completed. Done tasks were filtered from the API response before reaching the TUI, so `boardCounts()` never saw them. Completed work was invisible on the board.

**Improvement:** `LoadProjectTasks` now fetches all tasks regardless of status. `setProjectTasks` still only adds active tasks (todo/in_progress/blocked) to the sidebar tree nodes, but adds ALL tasks (including done/approved/cancelled) to `w.tasks` for the dashboard board. The DONE column now shows an accurate count of completed tasks.

**Why it matters:** The dashboard board is meant to show the current state of all work. A DONE column that always reads zero gives no satisfaction or closure — users can't see how much has been accomplished. Showing real completions makes the board a useful team status snapshot.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-033: Activity log uses human-readable entries

**Observation:** Activity log entries used raw internal strings like "proof-of-life realtime connected", "inbox approve <uuid>", and "manual refresh at". These looked like debug logs, not user-facing messages.

**Improvement:** Activity log entries now use human-readable text: "realtime events connected", "event replay complete", "sidebar refreshed at HH:MM:SS". Inbox and task activity entries use OC-N task numbers when available (e.g., "approved: OC-3", "OC-6: status → done") instead of raw UUIDs.

**Why it matters:** The Activity section in the dashboard is visible to users. Debug-looking strings make the app feel unfinished and leak implementation details. Human-readable entries give users actual insight into what happened.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-034: PROJECTS section shows project count badge

**Observation:** The PROJECTS section header showed "▾ PROJECTS" with no indication of how many projects were loaded. Users had no way to know from the header whether projects were available.

**Improvement:** The PROJECTS section header now shows a count badge: "▾ PROJECTS (3)" when projects are loaded. The count uses `projectCount()` which counts project-kind nodes in the sidebar.

**Why it matters:** Section count badges give at-a-glance awareness of what's available before scrolling. "PROJECTS (3)" tells the user they have work to look at; "PROJECTS" alone is ambiguous.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-035: Project nodes show open task count badge

**Observation:** Collapsed and expanded project nodes showed only the project name. Users had no way to know how many open tasks each project had without expanding it.

**Improvement:** Project sidebar nodes now show an open task count after the name: "▸ OtterCamp Sales Site (6)". The count is computed by `projectChildren()` which returns the active task children for that project node. The count updates when tasks are loaded.

**Why it matters:** Task count per project lets users prioritize which project to open first without expanding each one. A project with 0 open tasks needs no immediate attention; one with 6 open tasks does.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-036: CHATS sidebar sessions show relative time

**Observation:** Chat sessions in the CHATS sidebar showed only the session name with no indication of when they were last active. Users couldn't tell at a glance whether a session was from 5 minutes ago or 3 days ago.

**Improvement:** Top-level session nodes (Frank and recent chats) now show a subtle relative time suffix: "now", "5m", "2h", "3d". The time comes from the session's `UpdatedAt` field and is rendered in `styleSubtle` (dim color) to the right of the session label.

**Why it matters:** Recency is critical for chat context. Seeing "2h" tells you the session has been quiet; "now" or "5m" tells you it's active. Without relative time, users must open each session to check activity.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-037: Agent thinking indicator in chat panel header

**Observation:** When an agent turn was active, there was no visual indicator in the chat panel header. Users had to look at the status bar or wait for messages to know Frank was working.

**Improvement:** When an active agent turn matches the current chat session, an amber `◌` indicator appears to the right of the scope badge in the chat panel header. The indicator appears only for the session that has the active turn, so it disappears when the turn completes or is cancelled.

**Why it matters:** Immediate visual feedback that the agent is processing is a fundamental UX requirement. Without it, users wonder if their message was received or if the system is hung. The `◌` in the header is always visible regardless of scroll position.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-038: 'i' and 'd' keyboard shortcuts for quick navigation

**Observation:** Jumping to the Inbox or Dashboard required either typing a `:inbox` / `:dashboard` command or pressing Escape to return to dashboard. Power users expect single-key navigation for common destinations.

**Improvement:** Pressing `i` from any non-chat panel jumps directly to the Inbox view in the main panel. Pressing `d` jumps directly to the Dashboard. Both keys are documented in the `?` help screen alongside `r` (refresh).

**Why it matters:** The inbox is a high-frequency destination — users need to check it after every agent turn. A single keypress (`i`) vs a four-keystroke command (`:inbox` + Enter) saves friction on every inbox check.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
## EX-039: Panel title shows project/task context

**Observation:** The main panel title always showed a generic view name ("PROJECT", "TASK") regardless of which project or task was selected. Users had to look at the sidebar to know what content they were viewing.

**Improvement:** When a project is selected, the main panel title shows the project name in uppercase (e.g., "OTTERCAMP SALES SITE"). When a task is selected, the title shows the OC-N number (e.g., "OC-4"). This gives immediate orientation about what's in the main panel.

**Why it matters:** The panel title is always visible. Showing the actual context name instead of a generic label eliminates the need to look at the sidebar for orientation, especially after switching views.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-040: 'n' key jumps to next unread session

**Observation:** With multiple chat sessions open, finding unread sessions required visually scanning the sidebar for unread badges. There was no keyboard shortcut to jump directly to the next unread session.

**Improvement:** Pressing `n` from any non-chat panel cycles through visible sidebar nodes and jumps to the next session with an unread count. If there are no unread sessions, a "No unread sessions." status message is shown.

**Why it matters:** Unread sessions represent pending responses or activity that needs attention. A quick keyboard jump (`n`) lets users triage all unread sessions rapidly without pointer navigation.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-041: Project view shows done task count

**Observation:** The project view showed only open/active tasks, giving no indication of completed work. A project with 6 open tasks and 12 done tasks looked the same as a project with no completed work.

**Improvement:** The project view header now shows "OPEN TASKS (N) · M done" when there are completed tasks. The done count comes from `DoneCount` in the project detail, populated by counting done/approved/cancelled tasks separately from open tasks.

**Why it matters:** Done task counts provide a sense of progress and momentum. Showing only open tasks creates a perpetually incomplete-looking project. The done count gives users a snapshot of throughput and completion rate.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-042: Flow step shown in task detail

**Observation:** When viewing a task in the main panel, there was no indication of where the task was in its flow/pipeline. Users couldn't see if the task was at step 2 of 5 or waiting for a specific flow stage.

**Improvement:** The task detail view now shows the current flow step number (e.g., "Flow step: 3") after the status line when a flow step is recorded. This comes from the task's `Flow` field populated via SSE events.

**Why it matters:** Flow steps are the fundamental progress indicator for automated task pipelines. Without step visibility, users have no insight into pipeline progress beyond the binary status.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-043: Active turn indicator in status bar

**Observation:** The status bar showed connection state and session context but gave no indication when an agent turn was actively processing. Users had to look at the chat panel header or wait for messages.

**Improvement:** When an agent turn is active, the session label in the status bar shows an amber `◌` prefix: "◌ Frank / General". This is visible from any panel, not just the chat panel.

**Why it matters:** The status bar is always visible. Having the turn indicator there means users get at-a-glance feedback regardless of which panel is focused. Combined with the chat header indicator (EX-037), there are now two visual cues for active processing.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-044: CHATS header shows session count and unread indicator

**Observation:** The CHATS section header showed just "▾ CHATS" with no indication of how many non-Frank sessions were loaded, or how many had unread messages.

**Improvement:** The CHATS header now shows "▾ CHATS (N)" where N is the count of non-Frank sessions. When any session has unread messages, it also shows "+M unread". Fixed a visual bug where the relative time suffix for long session labels wrapped to the next line — the label is now truncated to reserve space for the time suffix.

**Why it matters:** Section count badges give at-a-glance awareness. Knowing you have "(4)" sessions and "+2 unread" tells you immediately that attention is needed without scanning the sidebar content.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-045: Inbox items show OC-N task label

**Observation:** Inbox items for task-related events (task_review, task_status_change) showed raw UUIDs in the action description. Users had to know the UUID → task mapping to understand which task an inbox item referred to.

**Improvement:** Inbox items for task-related events now show "OC-N" task labels. The inbox view renders a badge showing "OC-N" to the right of the item type when the task number is available. The dashboard inbox preview also shows OC-N in parentheses after the item type.

**Why it matters:** Task numbers (OC-N) are the user-facing identifiers. Showing UUIDs in the inbox is a leaky implementation detail. OC-N gives users the context they need to act on inbox items.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-046: 'p' and 't' keyboard shortcuts for project/task

**Observation:** After navigating away from a project or task view, returning required scrolling the sidebar and clicking the node. There was no quick keyboard shortcut to jump back to the last selected project or task.

**Improvement:** Pressing `p` from any non-chat panel returns to the currently selected project view (if any). Pressing `t` returns to the currently selected task view (if any). Both keys are documented in the `?` help screen.

**Why it matters:** When working on a specific project or task, users frequently switch between views (inbox, dashboard, task detail) and need to return quickly. Single-key jumps eliminate the navigation friction of re-selecting from the sidebar.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-047: Dashboard task board shows status icons

**Observation:** The dashboard task board showed tasks in TODO/IN PROGRESS/DONE columns but without any status icon prefixes, making the columns look identical in content structure.

**Improvement:** Dashboard task board columns now show status icons matching the sidebar: ○ for todo, ◌ for in_progress, ✓ for done/approved. The icon appears before each task title in the board view.

**Why it matters:** Visual consistency between the sidebar (task nodes with status icons) and the dashboard board helps users quickly understand status at a glance. The icons also make columns scannable at a glance without reading the column header.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-048: Project task list right-aligns status labels

**Observation:** In the project view, task status labels ("In Progress", "Todo") appeared immediately after the truncated task title, making the alignment messy and hard to scan.

**Improvement:** Status labels in the project task list are now right-aligned. The task title is truncated to fit available space, and padding spaces are computed so the status label aligns flush with the right edge of the panel content area.

**Why it matters:** Right-aligned status columns are a standard tabular layout convention. Consistent alignment lets users scan the status column at a glance rather than hunting for it after each variable-length title.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-049: Chat header shows project name when in project scope

**Observation:** When navigating to a project in the sidebar and switching to project scope, the chat panel header showed only "Frank / General" with no indication of which project was selected.

**Improvement:** When the active scope is ScopeProject and a project is selected, the chat panel header shows a context breadcrumb: "Frank / General › OtterCamp Sales Site". The project name comes from either the loaded project detail or the sidebar node label.

**Why it matters:** The chat panel header is the primary orientation cue for which context you're chatting in. Showing the project name makes it immediately clear the conversation is scoped to that project, without requiring the user to look at the sidebar.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-050: Project task list overflow indicator and blocked task icon

**Observation:** When a project had more open tasks than could fit in the available panel height, the list silently truncated. All tasks used the same ○ icon regardless of blocked or rejected status.

**Improvement:** Three additions: (1) when the task list overflows, a "+N more tasks · j/k to navigate" hint appears; (2) blocked, rejected, and deferred tasks show a ✗ icon instead of ○; (3) the task detail footer hint shows "Esc·back to project  p·project view" when a project is selected.

**Why it matters:** Silent truncation hides tasks needing attention. The overflow count shows exactly how many more exist. The blocked icon (✗) gives instant visual distinction. The navigation hints reduce friction of moving between task detail and project view.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-051: Task detail shows assigned agent name and current flow step name

**Observation:** The task detail view showed task number, title, status, description, and subtasks — but no indication of which agent was assigned to the task or what flow step it was currently in.

**Improvement:** `LoadTaskDetail` now fetches `assigned_agent_id` and `current_flow_node` from `/v1/tasks/{id}`, then makes a second API call to `/v1/agents/{id}` to get the agent's `display_name`. The task detail panel shows "Agent: Frank" and "Flow: Work" lines below the status when those fields are present. A "⚠  Human review required" warning appears if the task has `requires_human_review: true`.

**Why it matters:** Users often need to know who is working on a task and what stage of the workflow it's in. Previously this required a separate API call or checking the API directly.

**Effort:** Medium
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-052: Project view navigation hint at bottom of task list

**Observation:** When a project's task list was shown in the center panel, there was no indication of how to navigate the list or open a task. Users unfamiliar with the vim-style keys had no hint that `j`/`k` navigated tasks or that Enter opened a task.

**Improvement:** A muted italic hint line "j/k navigate  ·  Enter·open task  ·  Esc·back" appears below the task list when space permits. The help screen (`?`) now also lists "j/k ↑/↓ — navigate tasks in project view" in the Main Panel section.

**Why it matters:** Keyboard hint lines dramatically reduce the learning curve. Users expect to see available controls near the UI element they apply to. The one-liner is unobtrusive but always present.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-053: Improved empty state for project with no open tasks

**Observation:** When a project had no open tasks, the center pane showed only a bare "All tasks complete (N done)." text line without a section header, which was visually inconsistent with the task list when tasks were present.

**Improvement:** Empty state now shows an "OPEN TASKS (0)" bold section header followed by "✓  All N tasks complete" in the connected/success color, or "No open tasks." in muted style if no tasks exist at all.

**Why it matters:** Consistent visual structure — always showing the "OPEN TASKS" label — makes scanning predictable regardless of whether tasks are present. The green checkmark with the count is a positive signal that distinguishes "all done" from "no tasks added yet".

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-054: Task detail shows parent project name

**Observation:** When viewing a task detail (ViewTask), there was no indication of which project the task belonged to. A user navigating from the sidebar's PROJECTS section task tree directly to a task would lose project context.

**Improvement:** The task detail now shows "Project: OtterCamp Sales Site" (or whichever project) below the status line. The project name is resolved via the sidebar node graph: the task's sidebar node (`task-{id}`) has a `ParentID` pointing to the project node (`project-{id}`), whose Label is the project name. Additionally, when navigating to a task via a sidebar task node, `selectedProjectID` is now set so the "Esc·back to project" and "p·project view" navigation hints appear.

**Why it matters:** Users often lose track of which project they're in when deep in task detail. Showing the project name gives immediate context, and the navigation hints make it easy to jump back.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-055: Sidebar task nodes show ✗ for blocked/rejected/deferred

**Observation:** Sidebar task nodes under expanded projects only used `◌` (in_progress) and `○` (default) icons. There was no visual distinction for blocked, rejected, or deferred tasks.

**Improvement:** Added `✗` icon for tasks with work_status of `blocked`, `rejected`, or `deferred` in the sidebar node rendering. This matches the icon already used in the project view (renderProjectView) for consistency.

**Why it matters:** Blocked or rejected tasks need immediate attention. The ✗ icon provides instant visual scanning without requiring users to open the task to see its status.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-056: Dashboard Enter key auto-selects first open task

**Observation:** Pressing Enter on the dashboard with no previously selected task navigated to ViewTask but showed "No task selected" — a dead-end that required the user to go back and navigate to a task explicitly.

**Improvement:** When Enter is pressed on the dashboard and `selectedTaskID` is empty, the handler now auto-selects the first non-completed task from `taskOrder` before switching to ViewTask. The task detail is immediately loaded via `loadTaskDetailCmd`.

**Why it matters:** The primary user action from the dashboard is "open a task". Pressing Enter should do something useful. Auto-selecting the first open/in-progress task is the most sensible default — it means a single keypress opens the most recently active work item.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-057: Agents view shows empty state message

**Observation:** When the agents list was empty (not yet loaded or no agents configured), `renderAgentsView` returned a blank panel with just an empty line.

**Improvement:** Added a centered "no agents loaded" empty state message, consistent with other empty states in the TUI.

**Why it matters:** Blank panels are confusing — users don't know if the view is loading, empty, or broken. A message makes the state explicit.

**Effort:** Trivial
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-058: Dashboard task board header shows active project name

**Observation:** The dashboard Task Board header always said "Task Board" regardless of project context, even when a project was selected via sidebar navigation.

**Improvement:** When `selectedProjectID` is set, the header becomes "OtterCamp Sales Site — Task Board" (or whichever project is selected). The project name is resolved from the sidebar node graph via `nodes["project-"+selectedProjectID].Label`.

**Why it matters:** Users who navigated to a project (which sets `selectedProjectID`) then switched back to the dashboard expected to see project-scoped task data. The project name in the header confirms they're seeing tasks for the right project.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

