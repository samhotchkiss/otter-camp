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

## EX-059: Chat panel shows "loading messages..." while history is in-flight

**Observation:** When selecting a different chat session from the sidebar, the chat panel cleared immediately and showed "no messages yet" while the history fetch was still in progress. This was confusing — users couldn't tell if the session was empty or still loading.

**Improvement:** Added `chatHistoryLoading bool` flag to the Model. It's set to `true` when chat history is cleared and a new `loadChatHistoryCmd` is dispatched (session node selection), and cleared to `false` when `chatHistoryLoadedMsg` is received. The chat panel shows "◌ loading messages..." when `chatHistoryLoading` is true and there are no messages yet.

**Why it matters:** Visual feedback during data loading is a fundamental UX principle. "Loading..." is unambiguous and reassures the user that content is coming. "No messages yet" only appears after the load completes and the session is genuinely empty.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-060: Inbox items loaded from API when navigating to inbox view

**Observation:** The inbox view always showed "✓ Inbox clear" even when the server had unacted inbox items. Only the count (in the sidebar INBOX row) was fetched from the API; the actual item list was never requested. Items were populated only via SSE `inbox.item_created` events (i.e., only items arriving after TUI start were visible).

**Improvement:** Added `InboxSummaryItem` struct to `runtime.go` and `LoadInboxItems func(ctx context.Context) ([]InboxSummaryItem, error)` to `RuntimeHints`. Wired in `cmd/ottercamp/tui.go` via `GET /v1/inbox?is_acted=false&limit=50`. Added `loadInboxItemsCmd` and `inboxItemsLoadedMsg`. The command fires when navigating to inbox: on sidebar `sidebarKindInbox` Enter, on the `i` global hotkey, and the handler populates `workspace.inbox` from the API response.

**Why it matters:** The inbox is a critical action queue (approvals, rejections, deferrals). Without loading items from the API, a freshly-started TUI would always show an empty inbox even if there were pending items, causing users to miss required actions.

**Effort:** Medium
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-061: Task detail action hint adapts to work_status and review state

**Observation:** The task detail panel always showed "Enter·open async session" regardless of whether the task was in_progress, done, or had no session. The language was confusing — "open async session" doesn't convey that you're resuming an active conversation.

**Improvement:** The action hint in `renderTaskView` now changes based on `task.Status`:
- `in_progress` → "Enter·resume session"
- `done`/`approved` → "Enter·view session log"
- other → "Enter·open session"
When no sessionID exists: "(no session)" instead of "(no active session)". When `RequiresHumanReview` is true, an additional action row appears: "a·approve  x·reject  f·defer  o·open task session" in warning color. The `handleEnterKey` status message was similarly updated: "Resumed task session." / "Viewing completed task session." / "Opened task session." based on work_status.

**Why it matters:** Action hint language should match the actual action. "Resume" is natural for an active task; "view log" communicates a read-only completed session. The inline review actions reduce friction — users no longer need to navigate to the inbox to approve or reject a task.

**Effort:** Low
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---


## EX-062: Dashboard task board cursor navigation with j/k

**Observation:** The task board (dashboard view) showed tasks in kanban columns but had no selection cursor — you couldn't navigate between tasks and then press Enter to open one. The only way to open a task from the dashboard was through the sidebar.

**Improvement:** Added `dashboardCursor int` to `workspaceState` and `moveDashboardCursor(delta int)` method. Pressing `j`/`k` on the main panel while on the dashboard moves a `►` cursor through the active (non-done) tasks across all kanban columns. Pressing Enter opens the selected task's detail view. The cursor is not shown on done/approved/cancelled tasks. A navigation hint "j/k·select task  ·  Enter·open" appears at the bottom of the board when active tasks exist.

**Bug fixed:** The `dashboardCursor` started at 0, so the first `j` press incremented to 1, selecting the *second* task instead of the first. Fixed by detecting `selectedTaskID == ""` (nothing selected yet) and initialising `dashboardCursor` to -1 (for j) or `len(active)` (for k) before the delta is applied, so the first key press lands on the first or last active task.

**Test added:** `TestDashboardCursorJKMoveSelection` — seeds 2 active + 1 done task, presses j/k, asserts selectedTaskID transitions correctly and `►` appears on the right task in the rendered view.

**Why it matters:** Keyboard-only navigation is a core TUI value. Needing to click the sidebar just to open a task from the board breaks the flow. Now the user can press `d`, navigate with `j/k`, and press Enter — a natural three-key workflow.

**Effort:** Medium (bug in cursor initialisation required unit test to diagnose)
**Issue:** N/A (implemented directly in ralph-loop)
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-063: Arrow key support for dashboard task cursor

**Observation:** The dashboard task board supported `j`/`k` for cursor navigation but not `↑`/`↓` arrow keys. Arrow key support was already present for the project view task list (ViewProject) but was missing for ViewDashboard.

**Improvement:** Added `↑`/`↓` arrow key handling for `MainPanel + ViewDashboard` in the `tea.KeyEnter, tea.KeyEsc, ..., tea.KeyUp, tea.KeyDown, ...` case block (model.go). The same `moveDashboardCursor` function is used by both j/k and arrow keys, so the behaviour is identical. Test added: `TestDashboardArrowKeysMoveCursor`.

**Why it matters:** Users who default to arrow keys (coming from GUI apps) will expect ↑/↓ to work. Forcing vim-style keys only is unnecessarily restrictive for navigation.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-064: Dynamic selected-task info row in dashboard nav hint

**Observation:** The dashboard navigation hint always showed the static `j/k·select task  ·  Enter·open` line regardless of whether a task was selected. There was no way to see which task you'd open before pressing Enter.

**Improvement:** When `m.workspace.selectedTaskID` is set and the task is found in the tasks map, the hint line changes to `► OC-N: Task Title  Enter·open  ·  j/k·navigate` (rendered with focus colour), giving immediate context for the keyboard action. When no task is selected, the static fallback remains.

**Why it matters:** Keyboard navigation without visual confirmation of the target is a UX antipattern. The dynamic hint closes the feedback loop: the user sees exactly which task they'll open before committing to Enter, reducing mistakes.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-065: Dynamic task name hint and ► cursor in project view

**Observation:** The project view (OPEN TASKS list) showed a static `j/k navigate · Enter·open task · Esc·back` hint at the bottom regardless of cursor position. The task under the cursor showed a work-status icon (`◌`/`○`/`✗`) like every other row, giving no visual indication of which task was selected. This was inconsistent with the dashboard, which shows a `►` cursor icon and a dynamic task name hint.

**Improvement:** Aligned the project view with the dashboard pattern: (1) the task row under the cursor now renders with `►` and focus-colour bold text instead of the work-status icon and `styleSelected` background; (2) the bottom hint changes to `► OC-N: Task Title  Enter·open  ·  j/k·navigate  ·  Esc·back` (rendered with focus colour) when the cursor is on a valid task, falling back to the static `j/k navigate · Enter·open task · Esc·back` when no tasks are present.

**Why it matters:** Visual consistency across dashboard and project views reduces the learning burden. Users learn the `►` + dynamic hint pattern once and it works everywhere. Without the cursor indicator, you can't tell at a glance which task would open on Enter — especially when multiple tasks look similar.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-066: Inbox cursor ► consistency + live task status sync

**Observation:** Two separate but related consistency gaps:
1. The inbox view used `▸` as its cursor indicator prefix, while the dashboard and project views both use `►`. The difference was invisible in the sidebar (which uses background-highlight for the cursor, not a prefix icon), but the inbox view's own cursor prefix was inconsistent.
2. When a task changes `work_status` (via SSE `task.status_changed` or `task.completed` events), the sidebar task node icon and the project view task list did not update automatically. The old status persisted until the user manually refreshed, causing stale icons.

**Improvement:**
1. Changed the inbox view cursor prefix from `▸ ` to `► ` in `renderInboxView`, matching the dashboard and project view cursor pattern.
2. Added a `task.status_changed` / `task.completed` handler in `applyWorkspaceCommand` that updates (a) the in-memory `taskRecord.Status`, (b) the sidebar task node's `WorkStatus` so the icon refreshes immediately, and (c) the `selectedProject.Tasks` slice so the project view task list stays current.

**Why it matters:** Cursor inconsistency breaks the visual pattern — users learn `►` means "selected item" from the dashboard, then are confused by `▸` in the inbox. Live task status sync means agents working on tasks are visible in real time: a task flipping from `in_progress` to `done` immediately shows `✓` in the sidebar and project view without a manual refresh.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-067: Color-coded task status labels in project and task views

**Observation:** All task status labels in the project view ("In Progress", "Draft", "Blocked") were rendered in the same muted gray regardless of urgency. The task detail view used an ad-hoc local color switch that differed from what the project view used. The old mapping also made "draft" and "todo" green (implying completion) which was misleading.

**Improvement:**
1. Extracted a `taskStatusColor(s string) lipgloss.Color` helper in `view.go` with a consistent semantic mapping:
   - `in_progress` → amber (warning: active work in progress)
   - `blocked` / `rejected` / `deferred` → red (error: needs attention)
   - `done` / `approved` / `cancelled` → green (success: completed)
   - `draft` / `todo` / unknown → muted gray (neutral: queued)
2. Applied `taskStatusColor` to the right-aligned status labels in `renderProjectView` (both cursor and non-cursor rows).
3. Replaced the bespoke local switch in `renderTaskView` with `taskStatusColor`, giving both views the same colours.

**Why it matters:** Status labels convey urgency. A blocked task should scream red; an in-progress task should glow amber. When everything is the same muted gray, the user must read every label to understand the state of the board. With colour, the eye jumps instantly to blocked/urgent rows and de-emphasises queued work.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-068: Sidebar cursor syncs to task when opening from project view

**Observation:** When navigating from the project view (j/k to select a task, Enter to open), the sidebar remained visually unchanged — the cursor stayed on the project node (or wherever it was), not the specific task just opened. The user had no spatial feedback in the sidebar to confirm which task detail they were viewing, and pressing Enter in the sidebar would re-navigate to the wrong task.

**Improvement:**
1. Added `syncSidebarToTask(taskID string)` method to `workspaceState` in `workspace.go`. It looks up the task's sidebar node, expands the parent project section if collapsed, recomputes the visible sidebar list, and positions `sidebarCursor` at the task's index.
2. Called `syncSidebarToTask(taskID)` in `model.go` immediately after `setMainView(ViewTask)` in the Enter-key handler for the project task list.

The sidebar cursor now automatically jumps to the corresponding task node when a task is opened from the project view, even if the project was previously collapsed (it expands automatically).

**Why it matters:** The sidebar is the user's navigation tree. When the main panel transitions to a task detail, the sidebar should reflect that selection. Without sync, the cursor and content are visually disconnected — the user looking at OC-3 detail while the sidebar cursor sits on the project header is confusing. With sync, the highlighted sidebar row always matches the main content.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-069: Sync project task cursor when opening task from sidebar + eagerly load project detail

**Observation:** Two related gaps when opening a task by pressing Enter on a sidebar task node (rather than from the project view task list):
1. `projectTaskCursor` was never updated, so pressing `p` (project view) would show the cursor on the wrong task (position 0 / OC-6) rather than the task just opened.
2. `loadProjectDetailCmd` was never called when navigating to a task from the sidebar, so the project view showed "Loading…" indefinitely when you pressed `p`.

**Improvement:**
1. Added `syncProjectCursorToTask(taskID string)` call in the `sidebarKindTask` Enter handler. The method finds the task's index in the open-tasks list and sets `projectTaskCursor`. If `selectedProject` is nil (not yet loaded), the task ID is stored in `pendingProjectCursorTaskID` and applied when the project detail loads.
2. In the `sidebarKindTask` handler, also issue `loadProjectDetailCmd` when `selectedProject` is nil, so "p" immediately shows the populated project view.
3. In the `projectDetailLoadedMsg` handler, apply `pendingProjectCursorTaskID` if set.

**Why it matters:** Pressing `p` from a task detail is a natural shortcut — "show me the other tasks in this project". Before this fix, the cursor snapped to the top of the list regardless of which task you were viewing. The combined fix makes navigation feel seamless: open any task from the sidebar, press `p`, and you see the project view with the correct task highlighted.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-070: j/k navigation through tasks from task detail view

**Observation:** When viewing a task detail, there was no way to move to the next or previous task in the project without pressing Esc (back to project), then j/k to move the cursor, then Enter to open the next task. Three keypresses for a basic "next task" action.

**Improvement:**
1. Added `j` and `k` handlers for `MainPanel + ViewTask` in `handleWorkspaceRune`. They call `stepTaskInProject(±1)`, a new method that:
   - Calls `openTasksForProject()` to get the open task list
   - Increments/decrements `projectTaskCursor` (clamped at boundaries)
   - Sets `selectedTaskID` to the new task
   - Calls `syncSidebarToTask` to update the sidebar cursor
   - Returns `loadTaskDetailCmd` to fetch the new task detail
2. The task detail hint line now appends `  j/k·next/prev task` when the project context is loaded with multiple tasks.
3. Updated the help screen description to mention task-detail navigation.

Only works when in a project context (`selectedProject` is loaded with ≥2 tasks). At boundaries (first/last task), pressing k/j is silently ignored.

**Why it matters:** Reviewing tasks one by one is a common workflow — standing up before a daily sync, or triaging a backlog. The j/k muscle memory from the project view transfers naturally to the task detail. One keypress instead of three for each task transition dramatically speeds up task review.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-071: Scroll project task list to keep cursor visible

**Observation:** When a project had more tasks than fit in the available viewport, the task list was always clipped at the first N entries with a `+M more tasks · j/k to navigate` footer. Moving the cursor down past the last visible row would navigate correctly (the cursor position advanced), but the cursor task was invisible — the view still showed tasks 1 through N.

**Improvement:** Replaced the static clip with a stateless scroll window centered on the cursor. When the task list overflows:
- Compute `scrollStart = clamp(cursor - visible/2, 0, len-visible)` to center the cursor in the window.
- Show `visible = availForTasks - 1` task rows from `[scrollStart, scrollStart+visible)`.
- Footer adapts to show context: `↑ N above · +M more · j/k` (both directions), `↑ N above · j/k` (only above), or `+M more tasks · j/k to navigate` (only below, matching original phrasing).

No extra state is required — the scroll position is derived purely from the cursor index each render.

**Why it matters:** A project with 20 tasks is realistic. Without scroll-aware rendering, pressing `j` past row 10 is disorienting — the cursor moves but nothing in the view changes. With the fix, the window slides to always show the selected row, identical to the behavior users expect from any list-based UI.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [ ] Tested (no overflow data in dev env)

---

## EX-072: Full navigation context when opening task from dashboard

**Observation:** When pressing Enter on a task in the dashboard (task board), the task detail opened correctly, but the navigation context was empty: `selectedProjectID` was not set, so the hint showed only `Enter·resume session  Esc·back` (without `p·project view` or `j/k·next/prev task`). Pressing `p` did nothing; Esc went to dashboard (not project). The sidebar didn't expand to highlight the task either.

**Improvement:** In the dashboard Enter handler, after selecting the task:
1. Look up the task's sidebar node to find its parent project node, and set `selectedProjectID`.
2. Call `syncSidebarToTask` to expand the project in the sidebar and position the sidebar cursor.
3. Call `syncProjectCursorToTask` to set the project task cursor for the `p` key.
4. Eagerly issue `loadProjectDetailCmd` (if project not yet loaded) so `p` immediately shows the populated project view.

**Why it matters:** All four task entry points (sidebar task node, project task list, task detail j/k, and dashboard) now set the same navigation context. From any entry point, the user gets: `Esc·back to project`, `p·project view`, `j/k·next/prev task`, and a correctly positioned sidebar cursor.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-073: Full task title in panel header

**Observation:** When viewing a task detail, the main panel header showed only `OC-6` — just the task number. The project view header shows the full project name in uppercase. Consistency and orientation both argue for showing the full task title in the header.

**Improvement:** Changed the `ViewTask` title case in `renderMainPanel` to produce `OC-N: TASK TITLE` (uppercase, truncated to fit the column width). If the task has no number, the title alone is shown uppercased.

**Why it matters:** The panel header is the primary orientation anchor. Seeing `OC-6: FLOW KICKOFF FIX TEST` at a glance is more informative than `OC-6` alone, especially when switching between tasks rapidly with j/k. It also makes the panel header consistent with the project view which shows the full project name.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-074: Search filter applied in project task list

**Observation:** The `/` search filter worked on the dashboard (recent chats list and task list on the dashboard) but was ignored in the project view. Typing `/flow` while viewing "OtterCamp Sales Site" showed all 5 tasks unchanged — none were filtered out.

**Improvement:** Added `query := normalizedFilterQuery(m.mainFilter)` and a `matchesFilter` check to the open tasks loop in `renderProjectView`. Tasks are now filtered against the query in both their formatted label (`OC-N: Title`) and their `work_status`. The header count reflects the filtered set (e.g. "OPEN TASKS (2) · 1 done"). Pressing Escape clears the filter and all tasks return.

**Why it matters:** The search bar is prominently displayed in all views, so users reasonably expect it to filter the content they're looking at. Silently ignoring the filter in project view creates confusion and forces users to scroll through all tasks to find what they're looking for.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-075: Project view footer hint — remove duplicate task name

**Observation:** The project view rendered a hint footer at the bottom of the task list that repeated the selected task's full title with the `►` cursor icon: `► OC-1: Build OtterCamp sales landing p…  Enter·open  ·  j/k·navigate  ·  Esc·back`. The `►` cursor icon and task name were already shown inline in the task list row above, making the footer redundant and visually noisy.

**Improvement:** Simplified the footer to just the keybinding hints: `Enter·open  ·  j/k·navigate  ·  Esc·back`. The cursor position in the task list already tells the user which task is selected. Also updated the ViewTask help line in `commandFallbackHelp` to include `j/k next/prev · p project view` when project context is loaded.

**Why it matters:** Repeating the task name twice in quick succession (task row + hint footer) is distracting. The cleaner footer lets users read the available actions without having to parse repeated context.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-077: Show done tasks in project view with 'd' toggle

**Observation:** The project view showed "OPEN TASKS (5) · 1 done" but there was no way to see which tasks were done. The "1 done" count was purely informational — clicking it or pressing any key didn't reveal the done task.

**Improvement:** Added a `d` key toggle (context-sensitive: only active in `ViewProject` with main panel focus) that reveals or hides a "DONE (N)" section below the open task list. Done tasks show with a `✓` icon and muted styling. The hint row adapts: `d·show 1 done` when hidden, `d·hide done` when shown. The help line at the bottom also shows `d toggle done`. `LoadProjectDetail` was updated to populate a `DoneTasks []SidebarTaskItem` field alongside `DoneCount`. Elsewhere `d` still navigates to the dashboard.

**Why it matters:** Users often want to confirm that a task actually reached "done" state, or review what was completed recently. Without this toggle, the "1 done" badge was teasing information that was impossible to access from the project view.

**Effort:** Medium
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-076: Task detail hint — remove "(no session)" marker

**Observation:** When viewing a task that had no associated chat session (e.g., a Draft task), the task detail hint showed `Esc·back to project  p·project view  j/k·next/prev task  (no session)`. The `(no session)` label was added as a parenthetical that communicated why there was no `Enter·open session` hint, but it's awkward and adds no actionable information.

**Improvement:** Removed `(no session)` from the hint. The absence of the `Enter·open session` hint is sufficient context — users can infer the task has no session without an explicit label for it.

**Why it matters:** Parenthetical status labels in action hints read like debug output. Cleaning them up makes the hint row feel like a polished UI element rather than an afterthought.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-078: Consistent hint separators across task detail and inbox views

**Observation:** The task detail view's action hint used plain double spaces as separators between actions: `Enter·resume session  Esc·back to project  p·project view  j/k·next/prev task`. Compare with the project view hint that uses `  ·  ` (dot) separators: `Enter·open  ·  j/k·navigate  ·  Esc·back`. The inconsistency made the task hint harder to scan — the word boundaries between separate actions were not visually clear. Same issue in the inbox action hint row.

Additionally, the `?` help screen listed `d` only under "Global" as "jump to Dashboard", but `d` now has context-sensitive behavior: in the project view (main panel focused) it toggles the done tasks section instead. Users reading the help screen had no way to discover this.

**Improvement:** 
1. Refactored `renderTaskView` to collect all hint parts into a slice and join them with `"  ·  "`, matching the project view style. The "Esc·back to project", "p·project view", and "j/k·next/prev task" parts each become separate entries. Also fixed the inbox view action hint to use `  ·  ` separators.
2. Added `d · toggle done tasks section (project view only)` to the Main Panel section of the `?` help screen.
3. Updated the Global `d` entry to read `d · jump to Dashboard (or toggle done in project view)`.

**Why it matters:** Consistent separators make the hint bar feel like a coherent UI element rather than freeform text. Dots are a recognized "menu divider" pattern — the eye quickly segments `Enter·open  ·  j/k·navigate  ·  Esc·back` as three distinct actions. The help screen update ensures that the context-sensitive `d` behavior is discoverable.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-079: Persist selected project across TUI restarts

**Observation:** After navigating into a project's task board (`ViewProject`), quitting, and reopening the TUI, the project context was completely lost. The TUI showed the generic Dashboard even though the user had been working in a specific project. The `selectedProjectID` field was written into `workspaceState` when a project node was selected, but it was never saved to `UIState` and therefore not written to `~/.config/ottercamp/tui-state.json`.

**Improvement:**
1. Added `LastSelectedProjectID string` field to `UIState` (persisted as `last_selected_project_id` in JSON).
2. `State()` saves `m.workspace.selectedProjectID` into that field on every quit/save cycle.
3. `NewModelWithRuntime` restores `selectedProjectID` from the persisted field on startup. Added a guard: if the saved `last_main_view` is `"project"` but `last_selected_project_id` is empty (e.g., old state file), it falls back to Dashboard rather than showing a blank project board.
4. In the `sidebarDataLoadedMsg` handler, if `selectedProjectID` is non-empty and the project detail hasn't been loaded yet, `loadProjectDetailCmd` is dispatched so the project board populates immediately without requiring a sidebar click.

**Why it matters:** Users who were in the middle of a project sprint shouldn't have to re-navigate every time they restart the TUI. Persistent context is a baseline UX expectation for any tool people use repeatedly throughout the day.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-084: Sidebar done-task sessions use ✓ instead of ●

**Observation:** In the sidebar CHATS section, task-scoped sessions with `done` or `approved` work status used the `●` prefix icon (filled circle). `●` is also used by the connection indicator in the status bar to mean "SSE connected/active". A done task showing `● OC-5: Test agent participant validation` looked like it was actively running — the exact opposite of what's true. Meanwhile the project view's DONE section uses `✓` to indicate completed tasks, and the general chat session `Frank / General ✓` uses `✓` as a "read" marker.

**Improvement:** Changed the done/approved task session sidebar prefix from `● ` to `✓ `. Now:
- `✓ OC-5: Test agent participant validation 2h` — done task (clear completion indicator)
- `◌ OC-3: Test Task Queue Processor` — in_progress (unchanged)
- `⚠ ...` — blocked/rejected (unchanged)
- `○ ...` — draft/todo (unchanged)

This matches the `✓` used in the project view's DONE section for done tasks.

**Why it matters:** `●` carries the strong visual meaning of "active" or "live" (it's used for the SSE connection indicator). A done task wearing the `●` badge looks like it's running, which is misleading. `✓` communicates completion unambiguously and is already used elsewhere in the TUI for the same semantic.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-083: Explicit Esc destinations in hints — "Esc·project" and "Esc·dashboard"

**Observation:** Both the project view and task detail view showed `Esc·back` in their navigation hint rows, but "back" is ambiguous — it doesn't tell the user where they'll end up. In project view, Esc goes to the dashboard. In task detail, Esc goes to the project view (if a project is selected) or the dashboard (if there's no project context). A user learning keyboard shortcuts had to mentally model the two-level hierarchy to predict what Esc would do.

**Improvement:**
- Project view hint: `Esc·back` → `Esc·dashboard`
- Task detail hint (with project context): `Esc·back` → `Esc·project`
- Task detail hint (without project context): `Esc·back` → `Esc·dashboard`
- Updated the context-sensitive bottom help bar for both ViewProject and ViewTask to use the same destination labels.

**Why it matters:** Explicit navigation destinations in hint rows make keyboard navigation feel safe and predictable. A user who sees `Esc·project` knows exactly where they'll land. Without it, they have to guess or press Esc speculatively. The destination labels also serve as a reminder of the breadcrumb hierarchy (task → project → dashboard).

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-082: Consistent dot separators in dashboard hint and task detail field alignment

**Observation:** Two small but noticeable formatting inconsistencies remained after EX-078's separator standardization:

1. **Dashboard hint**: The hint line showing the selected task name used `  ` (double space) to separate the task name from the navigation actions: `OC-4: Test Flow Template Task  Enter·open  ·  j/k·navigate`. The gap between the name and the first action looked different from the `  ·  ` separators between the actions.
2. **Task detail fields**: Labels `Agent:` and `Flow:` had extra trailing spaces (`Agent:  Frank`, `Flow:   Work`) to visually align values with the longer `Project:` label. But `Status:` did not receive the same treatment, so values were aligned to column 10 for Status/Agent/Flow but column 11 for Project — a hidden inconsistency that still caused subtle misalignment.

**Improvement:**
1. Changed the dashboard hint separator: `styleMuted.Render("  ·  Enter·open  ·  j/k·navigate")` — the dot separator now bridges the selected task name and the actions.
2. Removed the extra padding from `Agent:` and `Flow:`, switching to a single space after the colon for all four fields (Status/Project/Agent/Flow). The values no longer attempt forced alignment (which was broken anyway since values have different lengths).

**Why it matters:** Every hint row and metadata block in the TUI uses `  ·  ` as a separator. The dashboard hint was the one remaining exception, making the line feel like two separate text fragments pasted together. The field alignment fix removes the hidden "someone added extra spaces" feel from task details.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-081: Suppress spurious "Layout changed: XL" on startup

**Observation:** Every TUI startup in tmux showed a persistent `Layout changed: XL` in the status bar, overwriting the more useful initial status message (e.g., the tmux key-binding note). The message appeared because tmux sends two `SIGWINCH` events when a pane is created: a first event with the initial (possibly smaller) terminal size, and a second event with the actual terminal size. The first sets `sizeClass` from `""` to `"M"` (no message), but the second changes from `"M"` to `"XL"` (triggers the notification). Since no user interaction had cleared the status, it persisted forever.

**Improvement:** Added a 3-second startup suppression window in the `WindowSizeMsg` handler. If the TUI has been running for fewer than 3 seconds, layout-class changes are silently applied (layout still updates correctly) but no status notification is shown. After the first 3 seconds any genuine terminal resize by the user still triggers the notification as expected.

**Why it matters:** The startup status message communicates important setup hints (like tmux modifier fallbacks). Having it immediately overwritten by a noise event from the terminal driver was confusing and made the TUI feel slightly broken on every startup.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-080: Auto-expand active project sidebar node on startup

**Observation:** After EX-079 restored the project view and project detail on TUI restart, the sidebar still showed the project node as collapsed (`▸ OtterCamp Sales Site (5)`). Users had to click the project in the sidebar to expand it and see the task list, even though the main panel was already showing the project board. The sidebar and main panel were out of sync on startup.

**Improvement:** In `sidebarDataLoadedMsg`, when dispatching `loadProjectTasksCmd` for each project, pass `expand=true` if the project ID matches `selectedProjectID`. `setProjectTasks` already supports an `expandNode` flag that sets `node.Expanded = true`; we just weren't using it on restore. After this change the sidebar auto-expands the active project's task list on startup, matching the state the user had before quitting.

**Why it matters:** The sidebar and main panel should stay in sync. If the main panel is showing a project board, the sidebar should be showing that project's tasks expanded. Otherwise the sidebar is useless until the user re-clicks the project.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-085: Chat panel shows "◌  thinking…" while waiting for first assistant token

**Observation:** After sending a message to Frank, there was a gap between the user's sent message and the first assistant token appearing. During this window, `activeTurn` is true (the `◌` shows in the session header and status bar), but the message area showed nothing — just the user's message at the bottom followed by the input box. A user watching the chat area had no way to know whether the request was actually being processed or had silently failed.

The existing `◌ waiting for response...` indicator only appears when the chat history is **empty** (zero messages). With an active conversation, the same gap existed with no feedback in the message area.

**Improvement:** Added a `◌  thinking…` indicator that appears at the bottom of the message area when all of the following are true:
1. `activeTurn` is true
2. The active turn is for the currently-visible session (uses the same `activeTurnSessionID` check as the header indicator)
3. The last message in `chatMessages` is NOT an unfinalized (in-progress) assistant message — once Frank starts streaming, the existing `▌` streaming cursor takes over

Three unit tests were added: one confirming the indicator appears correctly, one confirming it's absent when `activeTurn=false`, and one confirming it doesn't appear when an assistant message is already streaming (the `▌` cursor takes that role).

**Why it matters:** The window between "message sent" and "first token streamed" is especially noticeable for slow workers or high-latency model providers. Without feedback in the message area, users aren't sure if the system received their message or is working on it. The `◌  thinking…` line closes that loop: you see your message, then `◌  thinking…`, then Frank's response starts to appear.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-086: Dashboard help line shows task navigation hints

**Observation:** When the main panel was focused on the dashboard, the bottom help line showed a generic fallback: `"i inbox · d dashboard · n next unread · r refresh · / filter · : commands · ? help"`. This was the same line used by the Agents, Activity, Merges, and Schedules views — views with no interactive navigation. But the dashboard supports `j/k` task cursor navigation and `Enter` to open a task (since EX-062/EX-063), making those the most important actions on that view. They were completely absent from the help line.

The `commandFallbackHelp()` switch handled ViewInbox, ViewTask, and ViewProject with specific lines but had no `case ViewDashboard:`, so it fell through to `default`.

**Improvement:** Added `case ViewDashboard:` to `commandFallbackHelp()` with a context-sensitive return:
- When active tasks exist (the board has tasks to navigate): `j/k select task · Enter open · i inbox · n next unread · / filter · : commands · ? help`
- When no active tasks: `i inbox · n next unread · r refresh · : commands · ? help`

Golden layout snapshots regenerated.

**Why it matters:** The bottom help line is the first place a new user looks when they don't know what to press. If `j/k` and `Enter` — the primary dashboard interactions — don't appear there, users discover them by accident (or not at all). The specific hint is consistent with how ViewTask and ViewProject each have their own tailored help lines.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-087: Chat message header shows actual agent name in task sessions

**Observation:** Every assistant-role chat message showed "Frank" as the author label, regardless of which session was open or which agent was actually assigned to the task. In a multi-agent deployment (e.g., a project where separate PM, worker, and reviewer agents exist), every response would be incorrectly attributed to "Frank". The label was a hardcoded string in `renderChatMessages`.

**Improvement:** Replaced the hardcoded `"Frank"` with a new `assistantLabel()` helper on the model. The helper:
- When `activeScope == ScopeTask` and a task is loaded with a non-empty `AgentName`: returns the agent's display name (e.g., `"Ellie"`, `"Project Manager"`).
- In all other cases (org/project scope, or task not yet loaded, or task has no assigned agent): returns `"Frank"` as the default fallback.

Four unit tests added to `view_chat_test.go` covering: org scope fallback, task scope with agent, task scope without agent, and the full message render path confirming the agent name appears in rendered output.

**Why it matters:** Message attribution matters — users should know which agent is responding. In single-agent setups the change is invisible (still shows "Frank"). In multi-agent setups (the target production scenario), messages are correctly attributed to the responsible agent. The fallback is safe and the change is backward-compatible.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-088: Queue action hints shown above input box

**Observation:** The `e`, `s`, `d` queue-management keys (edit / steer / delete queued message) were completely undiscoverable. They were documented in the help screen but nothing in the chat panel hinted that they existed. Users who queued a message had no idea they could edit or remove it before the turn ended.

Additionally, the queued message preview lines (`q1: …`) that were added in EX-017 were being rendered *below* the input box in `bottomLines`. Because `buildPanelContent` trims content from the end of the line list when embedded-newline messages inflate the actual row count beyond `targetH`, the queue section was silently dropped — invisible even though it was logically included.

**Improvement:**
- Reordered `bottomLines` so queue preview lines and the action hint come *before* (above) the input box instead of after it. Visual order: blank separator → `q1: …` lines → `e·edit  ·  s·steer  ·  d·delete queued` hint → input box.
- The hint only shows when the input field is empty (typing a new message hides it to keep the panel clean).
- Two unit tests added: `TestQueuedMessageAndHintVisibleInChatPanel` and `TestQueueHintHiddenWhenInputIsNonEmpty`.

**Why it matters:** Queued messages are a first-class feature — the ability to edit or cancel a queued message is the escape hatch that makes async queueing feel safe rather than scary. If users can't see the hint, they won't know the feature exists and will worry about sending something they didn't mean to. The reordering fix also makes the feature actually visible for the first time.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-089: Queue-management keys missing from help screen

**Observation:** The `e` (edit), `s` (steer), and `d` (delete) queue-management keys were added in EX-088 as action hints in the chat panel, but were never added to the `?` help screen's Chat section. A user who found the help screen would not know these keys exist.

**Improvement:** Added `e edit queued  ·  s steer  ·  d delete queued` to the Chat section of the help screen. Also reordered the Chat section so Alt-Enter appears before the scroll keys, and clarified `[ / ]` scope hint wording.

**Why it matters:** The help screen is the canonical reference. Any key that exists should appear there; gaps create distrust ("is this feature real?").

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-090: Chat scroll indicators don't mention jump shortcuts

**Observation:** When users scroll up in a long chat history, the indicator lines showed `↓ N more  ·  PgDn scroll` and `↑ N older  ·  PgUp scroll`. The `G` (jump to latest) and `g` (jump to oldest) shortcuts were only in the help screen — there was no hint in the panel itself that these shortcuts existed.

**Improvement:** Extended both scroll indicator lines to include the jump shortcut: `↓ N more  ·  PgDn scroll  ·  G jump to latest` and `↑ N older  ·  PgUp scroll  ·  g jump to oldest`.

**Why it matters:** Users who scroll up looking at history will benefit most from knowing they can jump back to the bottom. Surfacing G/g at the moment it's useful prevents the frustrating "repeated PgDn" experience.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-091: Chat help hints don't adapt when a tool-call message is focused

**Observation:** The chat panel help line always showed `Enter·send` regardless of context. But when the input box is empty and the most recent message contains tool calls, `Enter` toggles the tool-call expansion/collapse — not send. The hint was misleading and the toggle was undiscoverable.

**Improvement:** When the input is empty and the last message has tool calls, the help line now reads `Enter expand/collapse tool  ·  PgUp/PgDn scroll  ·  g/G jump  ·  …` instead of `Enter send …`. Also updated the `?` help screen Chat section to document the dual Enter behaviour.

**Why it matters:** Tool-call output is important context for debugging agentic runs. If users don't know they can expand/collapse it, they either get overwhelmed by verbose output or miss it entirely.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-092: Task status changes not reflected in dashboard Activity log

**Observation:** The dashboard Activity section only showed startup events. Live task status transitions (e.g. a task moving from `todo` → `in_progress`) were silently ignored — the activity log didn't update even when the SSE stream delivered `task.status_changed` and `task.completed` events.

**Improvement:** In `applyTaskEvent`, when a `task.status_changed` or `task.completed` event arrives, append a human-readable entry to `workspace.activityLog` (e.g. `"OC-3: In Progress"` or `"OC-5: Done"`). The Activity section now shows real-time task progress without requiring a manual refresh.

**Why it matters:** One of the core selling points of the platform is live visibility into what agents are doing. An activity log that never changes during an active run is worse than useless — it creates a false sense that nothing is happening.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-093: Activity view always shows ✓ regardless of task outcome

**Observation:** EX-092 added task status transitions to the activity log, but the dashboard Activity widget rendered every entry with `✓` (green success check). That meant a task transitioning to `Blocked` or `Rejected` would show a misleading ✓ icon.

**Improvement:** Added `activityIcon(entry string) string` helper that inspects the entry text for status keywords:
- `✗` (red) for blocked / rejected / deferred / failed
- `◌` (amber) for in_progress
- `✓` (green) for everything else (done, approved, startup events)

Applied to both the full Activity view and the dashboard Activity widget (EX-103 later made the dashboard consistent too).

**Why it matters:** A green ✓ next to "OC-3: Blocked" is actively wrong. Correct icons make the activity feed scannable at a glance.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-094: Review-required tasks not visually distinguishable in dashboard

**Observation:** Tasks with `RequiresHumanReview: true` looked identical to normal tasks in the dashboard board columns. Users had to open each task to discover it was waiting on them.

**Improvement:** Tasks awaiting human review now display a `⚠` suffix in the board column (e.g. `◌ OC-4: Deploy pipeline ⚠`). In the in-progress column they also receive accent color + bold weight so they stand out visually.

**Why it matters:** Human review tasks are the most time-sensitive items in a run — every minute the operator doesn't notice them, the agent is blocked. The `⚠` badge makes them immediately scannable.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-095: Status bar truncates long worker-offline message

**Observation:** The worker-offline status message (`"Worker appears offline — check that ottercamp worker is running."`) was being truncated at 40 characters, cutting it off before the actionable command name.

**Improvement:** Increased status bar message truncation limit from 40 → 60 characters so the full worker warning is visible.

**Why it matters:** Truncating the message at the most actionable part defeats its purpose. The operator needs to see the command name to act on the warning.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-096: Chat empty state is anonymous; sidebar missing refresh hint

**Observation:** Two discoverability gaps:
1. The chat panel empty state showed `Tab·focus chat  Enter·send` — no indication of who you're talking to or that the session has context.
2. The sidebar help line had no `r refresh` hint, so users who saw stale data had no way to know they could reload without searching the help screen.

**Improvement:**
- Chat empty state now reads `Enter·send a message to Frank` (or the agent's name in task scope), making the first interaction intentional and named.
- Sidebar help line now includes `r refresh`.

**Why it matters:** The first message a user sends to Frank is a moment of trust. Generic placeholder text undermines that. The refresh hint solves a common "why is my data stale?" frustration.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-097: Tool call result [show more] looks clickable but does nothing

**Observation:** When a tool call result exceeded 280 characters, the chat panel showed `[show more]` at the truncation point. That label looked like a clickable link or a key hint, but pressing any key had no effect. Users wasted time trying to interact with it.

**Improvement:** Replaced `[show more]` with `… (result truncated)`, which is clearly informational. Also raised the display limit from 280 → 400 runes to reduce how often truncation occurs.

**Why it matters:** False affordances erode trust. A label that looks interactive but isn't is worse than no label at all.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-098: Panel resize hint `< / >` only listed under Sidebar in help screen

**Observation:** The `<` and `>` keys resize the focused panel (sidebar OR chat panel), but the help screen only listed them under the Sidebar section. Users focused on the chat panel who wanted to resize it had no way to discover this shortcut.

**Improvement:** Moved the resize hint to the Global section with the description: `resize focused panel (sidebar or chat) narrower / wider`.

**Why it matters:** The sidebar-specific listing was actively misleading. A user who looked up `<` in the chat context wouldn't find it.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-099: Input box cleared after send failure, forcing re-type

**Observation:** When a chat message failed to send (API error, network issue), the input box was already cleared. Users had to retype their entire message to retry — even if the failure was transient and completely outside their control.

**Improvement:** On `chatSendCompletedMsg` with a non-nil error, restore the last sent message from `chatHistory` back into the input box. Update the status message to `"Send failed (input restored) — <error>"` so users know they can edit and re-send immediately.

**Why it matters:** Losing composed text on a transient error is a highly punishing UX failure. The fix costs nothing and eliminates a moment of frustration that would otherwise happen on every network blip.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-100: Degraded mode banner shows generic developer jargon

**Observation:** The `DEGRADED MODE` banner displayed a single static message regardless of connection state. The message used internal terms (`EventReducer`, `replay`) that meant nothing to an operator, and gave no actionable guidance.

**Improvement:** `degradedModeBanner()` now picks a context-appropriate message based on the actual connection state:
- `ConnectionDisconnected` → `"Connection lost — data may be stale. Reconnecting automatically."`
- `ConnectionReconnecting` → `"Reconnecting to server…"`
- Connected but stream stale → `"Event stream stale — some data may be delayed. Press r to refresh."`

**Why it matters:** A banner that says "DEGRADED MODE — EventReducer stale" tells an operator nothing. A banner that says "Connection lost — Reconnecting automatically" tells them exactly what is happening and what (if anything) they need to do.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-101: Inbox badge hidden when sidebar is collapsed on narrow screens

**Observation:** The inbox item count badge was only visible in the sidebar node list. On narrow screens where users collapse the sidebar to reclaim space, pending inbox items were invisible — the operator had no signal that their attention was needed.

**Improvement:** Added a `✉ N` badge to the status bar that appears whenever `workspace.inboxCount > 0` and the user is not already viewing the Inbox. The badge is always visible regardless of sidebar state.

**Why it matters:** Inbox items often require human decisions that block agentic runs. Missing an inbox notification means a run stays blocked indefinitely.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-102: Merges and Schedules views missing item count badges

**Observation:** The Inbox, Activity, and Agents panel titles showed `(N)` count badges. Merges and Schedules did not, creating an inconsistency — users had to open those views to see if anything was pending.

**Improvement:** Merges and Schedules panel titles now show `(N)` count badges when items exist, matching the established pattern for other list views.

**Why it matters:** Consistency. Once users learn that `(N)` means "items waiting", they expect it everywhere. A missing badge on Merges implies the feature is different or broken.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-103: Dashboard Activity widget always shows ✓ icon

**Observation:** EX-093 added `activityIcon()` to the full Activity view, but the dashboard's inline Activity section was still using the old hardcoded `"  ✓ "` prefix for every entry. Task-blocked and in-progress status transitions were showing green success checks in the dashboard widget.

**Improvement:** Updated the dashboard Activity section to call `activityIcon(entry)` with a 2-space indent prefix, consistent with the full Activity view.

**Why it matters:** A separate code path for the dashboard widget was a silent inconsistency that would have required two separate fixes every time icon logic changed. Now both views use the same helper.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-104: Task-scoped chat empty state doesn't show which task it belongs to

**Observation:** When opening a task-scoped chat session with no messages yet, the empty state only showed `"no messages yet"` and the send prompt. Users had no way to confirm which task the session was scoped to without switching to the task detail view.

**Improvement:** When `activeScope == ScopeTask` and a task record exists with a title, the empty state now shows the task name below the prompt: `"OC-7: Build login flow"`. For tasks without a number, just the title is shown. Two unit tests added.

**Why it matters:** Starting a conversation in the wrong task scope is a real risk in multi-task projects. Showing the task name in the empty state is a low-cost confirmation that prevents wasted exchanges.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-105: Status bar messages persist indefinitely

**Observation:** Status messages set by navigation actions (`"Inbox"`, `"Scrolled up"`, `"Jumped to next unread"`) remained in the status bar until the next action replaced them. Old messages from minutes ago cluttered the bar and could confuse users about the current state.

**Improvement:** Status messages now auto-clear after 5 seconds. A `statusGeneration int` counter prevents stale timers from clearing newer messages: each `statusAutoClearCmd(gen)` only clears the bar if `gen == m.statusGeneration`. Three unit tests added.

**Why it matters:** A status bar that shows yesterday's "Scrolled up" while the user is doing something completely unrelated is worse than no status bar. Transient messages should be transient.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-106: Task detail navigation hint doesn't show position within project

**Observation:** The `j/k` navigation hint in the task detail view read `"j/k·next/prev task"` with no indication of how many tasks were in the project or where the current task fell. Users navigating a 10-task project had no way to tell if they were at the beginning, middle, or end.

**Improvement:** Hint now reads `"j/k·next/prev task  (2 of 5)"` — position (N of M) appended inline.

**Why it matters:** Positional context reduces disorientation when navigating a task list with j/k. It's the same information a table of contents provides.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-107: Dashboard inbox widget has no action hint

**Observation:** The dashboard showed inline inbox items but gave no hint that pressing `i` would open the full inbox view. Users could see items needed attention but had no in-panel signal about how to respond.

**Improvement:** Added `"  i·open inbox"` below the item list (≤3 items) and `"+N more  ·  i·view all"` on the overflow line (>3 items).

**Why it matters:** The dashboard is designed to surface actionable items. Without a call-to-action, it's read-only noise. The `i` hint transforms it into an action starting point.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-108: First-run tour and cold-open banner use jargon

**Observation:**
- The tour line used `/` separators and `:frank` / `:inbox` command syntax that was unfamiliar to first-time users. The `?·help` shortcut was not mentioned at all, so new users had no obvious way to discover the full keybinding reference.
- The cold-open banner said `"FIRST RUN  Booting operator console..."` — internal/developer language that would feel alien to a non-technical operator.

**Improvement:**
- Tour line changed from `"1/sidebar  2/main  3/chat  ·  :frank  :inbox  :tour dismiss"` to `"1·sidebar  2·main  3·chat  ·  i·inbox  ?·help  :tour dismiss"`. Consistent `·` separators, natural key hints instead of colon-commands.
- Cold-open banner changed from `"// FIRST RUN  Booting operator console..."` to `"// WELCOME  Setting up your workspace…"`.

**Why it matters:** First impressions matter. Jargon in a welcome screen signals that the tool is built for developers, not operators. Plain language is inclusive without being condescending.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-109: Dashboard column headers show total counts even when filter is active

**Observation:** When a search filter was active in the dashboard (e.g. typing `api` to find API-related tasks), the task rows correctly filtered to matching tasks. However, the column headers (TODO `2`, IN PROGRESS `1`, DONE `1`) continued to show the total unfiltered counts. The mismatch was confusing — a column header showing `2` but only `1` task row visible implied hidden tasks or a bug.

**Improvement:** When `query != ""`, `renderDashboardView` now computes per-column counts from the filtered task set (using the same `matchesFilter` predicate applied to task rows) before building the column headers. Unfiltered counts are only used when no filter is active. Unit test added.

**Why it matters:** Column counts are a summary of what's visible. Showing totals while rows are filtered breaks the user's mental model and makes the filter feel broken.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-110: Agents view ignores search filter

**Observation:** Unlike every other list view (dashboard, project tasks, inbox, activity), `renderAgentsView` did not apply `mainFilter` to the agents list. Searching for "ellie" while in the Agents view would show no visual feedback — all agents remained visible.

**Improvement:** `renderAgentsView` now applies the same `matchesFilter` predicate used by other views, filtering agents by name and status. When the filter excludes all agents, shows `"no agents matching <query>"` instead of a confusing empty list.

**Why it matters:** Consistency. Users who learn that `/` filters content expect it to work in every view. Silent non-filtering is worse than no filter at all — it makes users think the filter is broken.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-111: Inbox empty state says "✓ Inbox clear" when filter excludes all items

**Observation:** When a search filter was active and no inbox items matched, `renderInboxView` showed `"✓ Inbox clear"`. That message implies there are no pending items — but there were pending items, they just didn't match the filter. Misleading.

**Improvement:** When `query != ""` and there are inbox items that don't match, the empty state now shows `"no inbox items matching <query>"`. The `"✓ Inbox clear"` message is only shown when the inbox is genuinely empty.

**Why it matters:** "✓ Inbox clear" is a signal that no action is needed. Showing it when items exist (but are filtered) creates false confidence that could cause an operator to miss a task requiring review.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-112: Activity view says "No activity yet" when filter excludes all entries

**Observation:** Same pattern as EX-111: when a filter was active and no activity entries matched, `renderActivityView` displayed `"No activity yet"`. That message implies the log is empty, not filtered.

**Improvement:** When `query != ""` and there are activity entries that don't match, shows `"no activity matching <query>"` instead of `"No activity yet"`.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-113: Merges and Schedules views don't apply search filter

**Observation:** `renderMergesView` and `renderSchedulesView` did not apply `mainFilter`. All merges/schedules were always shown regardless of the active search query — the only views that silently ignored the filter.

**Improvement:** Both views now apply `matchesFilter` to their items. Empty state messages distinguish filtered-out vs genuinely empty:
- Merges: `"no merges matching <query>"` vs `"No pending merges"`
- Schedules: `"no schedules matching <query>"` vs `"No schedules"`

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-114: Dashboard BLOCKED column header overflows available width

**Observation:** When `counts.Blocked > 0` and `width > 80`, a 4th `BLOCKED (N)` column header was appended to the 3-column header row. However:
1. `colW` was computed as `(width-6)/3` — sized for 3 columns. A 4th column header at this width overflowed the available space.
2. The 4th column header appeared, but blocked task rows were never rendered in any column — so the `BLOCKED (N)` count was a header with no rows underneath.

**Improvement:**
- When `showBlockedCol` is true, recompute `colW = (width-6)/4` so all 4 columns fit within the available width.
- Render blocked/rejected/deferred tasks as rows in the 4th column with `✗` icon and `colError` color.
- Update overflow indicators to include the blocked column.
- Add `safeIndex` helper to simplify row slot access (replaces bounds-checked `if i < len(slice)` pattern).

**Why it matters:** The old layout had a visual bug — a column header with no rows underneath, and text overflowing the panel width on screens wider than 80 columns. Blocked tasks are the most attention-critical items; they deserve a visible column.

**Effort:** Medium
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-115: Project view done-tasks section ignores search filter

**Observation:** In the project view, when "d" is toggled to show done tasks, the `DONE (N)` section always showed all done tasks regardless of any active search filter. The open-task section above it correctly filtered tasks by `mainFilter`, but the done-tasks section did not.

**Improvement:** The done-tasks section now applies the same `matchesFilter` predicate used by the open-task section. The section header count reflects filtered results, and the section is skipped entirely when no done tasks match.

**Why it matters:** If a user is searching for "OC-3" to find a specific task, they expect the done section to also filter to matching tasks. Showing all done tasks while the open section is filtered creates a confusing mixed view.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-116: Dashboard help line missing "t·task detail" hint

**Observation:** When tasks are present in the dashboard, the help line showed `"j/k select task · Enter open · i inbox · ..."`. Once a task is selected, pressing `t` jumps to the task detail view — but this key was never mentioned in the dashboard context hint. Users had no way to discover it without opening the full help screen.

**Improvement:** When `selectedTaskID != ""` in the dashboard, the help line now reads `"j/k select task · Enter open · t task detail · i inbox · ..."`.

**Why it matters:** `t` is the primary shortcut for revisiting a task you were just working on. Not surfacing it in the dashboard hint means users fall back to the slower sidebar navigation path.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-117: Dashboard navigation hint shows task name even when filter hides all tasks

**Observation:** When a search filter excluded all tasks from the board columns (`todoTasks + inProgTasks + doneTasks + blockedTasks` all empty), the navigation hint at the bottom still showed the selected task name and `"Enter·open  ·  j/k·navigate"` hints. The task wasn't visible anywhere in the filtered board, making the navigation hint misleading.

**Improvement:** When `query != ""` and all board column task lists are empty (filter excluded everything), show `"no tasks matching <query>"` instead of the selected task name hint.

**Why it matters:** Showing navigation hints for tasks that aren't visible implies those tasks are in the current view. It's a subtle but confusing inconsistency that would make users wonder where the task went.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-118: Project view shows "No open tasks" when filter excludes all tasks

**Observation:** In the project view, when a filter excluded all open tasks, the empty state showed `"No open tasks."` — the same message shown when the project genuinely has no open tasks. Users couldn't tell whether tasks were filtered out or the project was actually empty.

**Improvement:** Track `totalUnfilteredOpenTasks` separately from the filtered `openTasks` slice. When `query != ""` and there are unfiltered open tasks that were filtered out, show `"no open tasks matching <query>"` instead of `"No open tasks."`.

**Why it matters:** Same principle as EX-111/112 — showing "nothing here" when the filter is the cause is misleading. The user would think the project has no work, when actually their search just doesn't match current task labels.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-119: Sidebar filter shows section headers even when no children match

**Observation:** When filtering the sidebar (e.g., `/ellie`), all section headers (`CHATS`, `PROJECTS`, `INBOX`) were always included in the filtered results regardless of whether any items in that section matched the query. Users would see empty section dividers between sections that had no matching items, adding visual noise.

**Improvement:** `filteredSidebarIDs` now performs a second pass after the initial content matching pass. A section header is only included when at least one of its subsequent sibling nodes (up to the next header) appears in the include set. Headers with no matching children are suppressed.

Also updated `INBOX` treatment: now only included when `inbox` or the node's label matches the query, rather than always being shown.

**Why it matters:** Clean search results. If I search for "ellie" and only task sessions match, showing the `PROJECTS` and `INBOX` section headers with nothing under them adds noise. Suppressing empty sections makes the filtered sidebar immediately readable.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-120: New inbox items don't appear until manual refresh

**Observation:** When an `inbox.item_created` SSE event arrived, the TUI incremented `inboxCount` (showing the `✉ N` badge in the status bar) but didn't reload the inbox item list. If the user opened the inbox immediately, the new item wasn't visible — the list was stale from the last load. Only a manual `r` refresh would show it.

**Improvement:** The `inbox.item_created` handler now returns `loadInboxItemsCmd(m.runtimeHints)` to trigger an immediate reload. The badge and the list are now always in sync.

**Why it matters:** A badge that says "1 new item" but shows nothing when you open the inbox is worse than no badge. It makes users think the system is broken. Immediate reload is the correct behavior.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-121: Project view "d toggle done" hint didn't reflect current state

**Observation:** In the project view, the help line always showed `"d toggle done"` regardless of whether done tasks were currently visible or hidden. This generic hint doesn't tell you what pressing `d` will actually *do* — it reads the same before and after.

**Improvement:** `commandFallbackHelp()` for `ViewProject` now returns `"d show done"` or `"d hide done"` based on `m.workspace.showDoneTasks`. The hint is now a direct instruction, not a toggle description.

**Why it matters:** `"d toggle done"` is programmer-speak. `"d show done"` is what the user actually wants to know: pressing `d` will make done tasks appear. Once they're visible, `"d hide done"` tells them pressing again will hide them.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-122: Task detail ⚠ badge didn't appear when inbox item arrived

**Observation:** When a new inbox review item was created for a task (e.g., the agent flagged a draft action for human review), the TUI received the `inbox.item_created` SSE event and reloaded the inbox list (EX-120). However, it didn't update the task record's `RequiresHumanReview` field. If the user was viewing the task detail, the `⚠ Human review required` line and the `a·approve · x·reject · f·defer` action row would not appear until they navigated away and back to trigger a full task detail reload.

**Improvement:** After `inboxItemsLoadedMsg` stores the inbox items, it calls `syncTaskHumanReviewFromInbox()` which sets `RequiresHumanReview = true` on any task record that has a corresponding pending inbox item.

**Why it matters:** The ⚠ badge and action row are critical UI — they tell the user that their input is needed. A badge that doesn't appear until navigation is unreliable and erodes trust in real-time notifications.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-123: Task detail ⚠ badge persisted after user acted on review request

**Observation:** When the user approved, rejected, or deferred an inbox review item from the inbox view, `removeInboxItem` removed the item from the local list. But `RequiresHumanReview` on the task record was never cleared. If the user then navigated to the task detail, the `⚠ Human review required` line would still appear — even though they had just acted on the review.

**Improvement:** `removeInboxItem` now records the `TaskID` of the removed item and, after filtering, checks if any other inbox items remain for that task. If none remain, it sets `RequiresHumanReview = false` on the task record. The ⚠ badge disappears immediately when the last review item is actioned.

**Why it matters:** Stale state in both directions is confusing. EX-122 makes the badge appear immediately; EX-123 makes it disappear immediately. Together they make the review workflow feel coherent and instantaneous.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-124: Inbox view had no position indicator

**Observation:** When multiple inbox items were queued, the action hint beneath the current item (`a·approve  ·  x·reject  ·  f·defer  ·  o·open  ·  j/k·navigate`) gave no indication of position. The user couldn't tell if they were on item 3 of 10 or item 1 of 2.

**Improvement:** When `len(filteredInbox) > 1`, the action hint now includes `  ·  N of M` at the end. When there is only one item, the position hint is suppressed since it would be redundant.

**Why it matters:** Spatial awareness in a list is table-stakes UX. Without it, users can't plan how much work remains or know when they've reached the end. The `N of M` pattern is already used in the project view task list (EX-106).

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-125: flow.advanced SSE event was silently dropped — task flow step never updated in real-time

**Observation:** The server publishes `"flow.advanced"` events when a task advances to the next flow node. The TUI had a handler for `"task.flow.advanced"` in `workspace.applyRealtimeEnvelope` — but the server sends `"flow.advanced"`, not `"task.flow.advanced"`. The names never matched, so the handler was dead code and the task detail view's "Flow: step N" and "FlowNodeName" fields were never updated via SSE. Users saw stale flow step information until they manually navigated away and back to the task.

**Improvement:** Added a `"flow.advanced"` handler in `applyWorkspaceCommand` that:
1. Decodes `task_id` from the payload  
2. Appends `"OC-N: flow advanced"` to the activity log
3. Returns `loadTaskDetailCmd(taskID, ...)` to reload the full task detail asynchronously

Since `applyWorkspaceCommand` can return `tea.Cmd`, this approach naturally picks up the current flow node name from the API without needing the server to include it in the event payload.

**Why it matters:** If a task flow advances (e.g., from "In Progress" to "Review"), the task detail view should reflect that immediately. Stale flow step information misleads the user about where in the pipeline their work is.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-126: Dashboard board hid the cursor row when a column had 5+ tasks

**Observation:** The dashboard board rendered at most 4 rows per column. When the cursor moved (via `j`/`k`) to a task that was the 5th or later item in its column, the cursor row was invisible — hidden behind the `"+N more"` indicator. The user could navigate to a task they could not see, creating a disorienting experience where pressing `Enter` would open a task that never appeared highlighted.

**Improvement:** Each column now computes its own scroll start via `boardColStart()`. When the cursor is at index ≥ 4 in a column, the window shifts so the cursor appears at the last visible row (index 3 of 4). The overflow indicator is updated to show `"↑N above"` (for items scrolled off the top) and/or `"+N more"` (for items below the visible window).

**Why it matters:** A cursor the user cannot see destroys the mental model of keyboard navigation. The user would press `j` and not see any visual change, then press `Enter` and be surprised by which task opened. Keeping the cursor visible is table-stakes for any interactive list.

**Effort:** Medium
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-127: Cross-session event leakage window after chat.turn.started

**Observation:** `activeTurnSessionID` is set via the async `SessionResolvedMsg` message, which arrives after the session UUID is resolved in `tui.go`. Between receiving `chat.turn.started` and receiving `SessionResolvedMsg`, `activeTurnSessionID` was empty — and `sessionMatchesActive` accepts all sessions when empty. During this window, SSE events from OTHER sessions (e.g., supervisor recovery runs) could incorrectly set `activeTurn = true` or append messages to the wrong session's chat.

**Improvement:** When `chat.turn.started` fires and the payload contains a valid UUID `session_id`, it is immediately stored in `activeTurnSessionID` (unless it's already set). This closes the leakage window at the source.

**Why it matters:** The root cause of EX-144 (cross-session leakage for supervisor recovery runs) was `activeTurnSessionID` being empty at startup. EX-127 applies the same defense at turn start, so even if `SessionResolvedMsg` is delayed, the session filter is already active.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-128: task.created SSE event not handled — project view never updated in real-time

**Observation:** When a new task was created (either by the user, an agent, or an automated flow), the server published a `"task.created"` event. The TUI had no handler for this event, so the project view's task list stayed stale. Users would not see the new task until they manually refreshed (pressed `r`) or navigated away and back.

**Improvement:** `applyWorkspaceCommand` now handles `"task.created"`. It adds `"OC-N: created"` to the activity log and, when the new task's `project_id` matches the currently viewed project, triggers `loadProjectDetailCmd` to refresh the task list immediately.

**Why it matters:** Real-time collaboration requires that both users and agents see new tasks appear as they are created. A stale project board undermines the "live" feel of the system, especially in flow-driven workflows where tasks are created automatically.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-129: Four server SSE events silently dropped — budget anomaly, task.merged, flow.started, flow.rejected

**Observation:** The server publishes four events that the TUI had no handlers for:
- `budget.anomaly_detected` — fires when token usage is 3× above the rolling average
- `task.merged` — fires when a merge queue entry is processed and pushed
- `flow.started` — fires when a task's flow execution is first created
- `flow.rejected` — fires when a reviewer rejects a task and it moves to the reject node

All four were silently dropped. Budget anomalies went unnoticed until the user checked the usage summary manually. Merge completions were invisible (no activity entry, no indication). Flow start and rejection events meant the task detail view's flow step indicator stayed stale.

**Improvement:** Added four handlers in `applyWorkspaceCommand`:
1. **`budget.anomaly_detected`**: Sets `statusMessage` with a formatted warning: `"⚠ Budget anomaly: daily usage is ~6x above average (90000 tokens vs avg 15000)."`. Multiplier is derived from `rolling_average_ratio`, clamped to ≥ 2.
2. **`task.merged`**: Appends `"<branch_name>: merged"` to the activity log.
3. **`flow.started`**: Appends `"OC-N: flow started"` to the activity log and triggers `loadTaskDetailCmd` to refresh the task detail view.
4. **`flow.rejected`**: Appends `"OC-N: flow rejected"` to the activity log and triggers `loadTaskDetailCmd` to refresh the task detail view.

Also extracted a `taskLabel(rec *taskRecord, taskID string) string` helper (used by flow.started, flow.rejected, and the existing flow.advanced handler) to avoid duplicating the OC-N / title / ID fallback logic three times.

**Why it matters:** Budget anomalies are high-urgency signals — the user needs to know immediately, not after opening a separate view. Merge and flow lifecycle events belong in the activity log so the user can see the pipeline progressing without leaving the TUI. Stale flow step indicators in task detail break the real-time feel of the system.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-130: Four deployment and policy SSE events silently dropped — deployed, rollback, approval, capability denied

**Observation:** Four more server events were published with no TUI handlers:
- `project.deployed` — fires when a deployment completes successfully (env_updater finalises the deploy task)
- `project.rollback_initiated` — fires when a rollback is triggered
- `deploy.approval_requested` — fires when a deploy is waiting on human approval before proceeding
- `tool.capability_denied` — fires whenever an agent's tool call is blocked by a capability policy

None of these surfaced anywhere in the TUI. Users had to open the delivery view or check logs to know a deploy landed, a rollback happened, or a tool was blocked.

**Improvement:** Added four handlers in `applyWorkspaceCommand`:
1. **`project.deployed`**: Appends `"deployed <sha[:8]>"` to the activity log.
2. **`project.rollback_initiated`**: Appends `"rollback to <sha[:8]>"` to the activity log.
3. **`deploy.approval_requested`**: Sets `statusMessage` to `"Deploy pending approval: <sha[:8]>"` so the user sees it immediately.
4. **`tool.capability_denied`**: Sets `statusMessage` to `"Policy denied tool: <tool_name> (<capability>)"` so the user knows an agent was blocked.

Commit SHAs are truncated to 8 characters for readability. The `statusMessage` for `deploy.approval_requested` and `tool.capability_denied` will clear naturally when the user takes any action that overwrites the status bar.

**Why it matters:** Deployment events are high-signal — users need to know when code lands in production. A rollback is even higher urgency. A deploy waiting on approval creates a stall if the user doesn't notice. Tool capability denials indicate policy configuration may need tuning; surfacing them inline saves a round-trip to the logs.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-131: memory.extracted and mcp.catalog.changed events silently dropped + taskLabel refactor

**Observation:** Two more server events were not handled by the TUI:
- `memory.extracted` — fires after each background memory extraction pass (per conversation turn). Users had no visibility into when memories were being created.
- `mcp.catalog.changed` — fires when the MCP tool catalog is refreshed (tools added/updated/removed from a connection). Users couldn't tell when their available tools changed.

Additionally, the `task.status_changed` activity entry code was duplicating the OC-N / title / ID label resolution logic that had since been extracted into `taskLabel()` in EX-129.

**Improvements:**
1. **`memory.extracted`**: Appends `"memory: N items extracted"` (or "item" for singular) to the activity log when `count > 0`. Events with `count=0` are silently ignored.
2. **`mcp.catalog.changed`**: Sets `statusMessage` with a delta summary, e.g. `"MCP catalog updated: +5 added, -1 removed."` Uses cases for added-only, removed-only, both, or a generic refresh message.
3. **Refactor**: `task.status_changed` activity label computation now calls `taskLabel(m.workspace.tasks[taskID], taskID)` instead of duplicating the three-way fallback inline.

**Why it matters:** Memory extraction is a key background process — seeing it happen in the activity log gives the user confidence the system is learning. MCP catalog changes affect what tools agents can use; a status bar notice lets the user know without navigating to settings. The refactor ensures all activity label code goes through one place.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-132: Activity log grew unbounded — memory leak for long-running sessions

**Observation:** The activity log (`workspace.activity []string`) had no size cap. Every SSE event that triggered an `append` to the activity log permanently grew the slice. After EX-129–131 added 10 new event types that append to the log, a long-running session with active agents and frequent memory extraction could accumulate thousands of entries, consuming growing amounts of heap memory with no way to reclaim it.

**Improvement:** Added `appendActivity(entries []string, entry string) []string` helper with a `const activityMaxEntries = 200` cap. When the slice exceeds the cap, the oldest entries are dropped from the front (`entries[len-200:]`). All 20 append sites in `model.go` and `workspace.go` were updated to call `appendActivity` instead of bare `append`.

**Why it matters:** TUI processes are long-lived — users may leave them running for hours or days. An unbounded activity log would slowly grow without bound. The 200-entry cap preserves enough history to be useful (the last 200 events) while keeping the slice O(1) in steady state for high-event-rate systems.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-133: `r` key refreshed only sidebar from task/project detail views — context ignored

**Observation:** In `ViewTask` and `ViewProject`, pressing `r` always triggered `loadSidebarDataCmd` and showed "Refreshing sidebar data…". The sidebar reload doesn't update the task title, status, flow step, description, or event log shown in the main panel. Users who wanted to manually refresh a task detail after an agent made changes had no way to do it via keyboard.

**Improvement:** `r` in `ViewTask` (with `MainPanel` focus + a selected task ID) now calls `loadTaskDetailCmd` and shows "Refreshing task detail…". `r` in `ViewProject` calls `loadProjectDetailCmd` and shows "Refreshing project detail…". All other contexts continue to fall through to the existing sidebar refresh. Help hints for both views were updated to include `r refresh`.

**Why it matters:** Agents can modify task descriptions, change status, update flow steps, and add event log entries. When the user is looking at a task detail view, they need a quick way to pull the latest state without navigating away and back. The `r` key is already the muscle memory for "refresh" — making it context-aware means it does the right thing wherever the user is focused.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-134: Help screen r key description was stale after EX-133

**Observation:** After EX-133 made `r` context-aware (refreshing task/project detail in those views), the help screen still showed `"r   refresh sidebar data"` — which is inaccurate for `ViewTask` and `ViewProject` contexts.

**Improvement:** Updated the help screen `r` key description to `"refresh task / project detail (in detail views), or sidebar"`.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-135: g/G keys did not work in dashboard board view

**Observation:** `g` (jump to top) and `G` (jump to bottom) worked in the sidebar, inbox, and project views, but in `ViewDashboard` they did nothing — neither key was handled for that view. Users familiar with vim-style navigation would naturally expect `g`/`G` to work on the dashboard board.

**Improvement:** Added `g`/`G` handlers for `ViewDashboard` (when `MainPanel` is focused):
- `g`: jumps cursor to the first task in `dashboardActiveTasks()`, updates `dashboardCursor=0` and `selectedTaskID`, sets status bar to `"▸ OC-N: title"`.
- `G`: jumps cursor to the last task, updates `dashboardCursor=len-1` and `selectedTaskID`.

Also added `g/G first/last` to the dashboard help hint when tasks are present.

**Why it matters:** Keyboard power users expect consistent navigation primitives across views. When `g`/`G` work everywhere else but silently do nothing on the dashboard, it breaks the muscle memory contract. The fix aligns dashboard navigation with the rest of the TUI.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-136: Chat header showed "Task Session" but not which task — missing context breadcrumb

**Observation:** When the chat scope was `ScopeTask`, the chat panel header showed just "Task Session" (or a session UUID if unresolved). The `ScopeProject` context already appended `" › Project Name"` to the header. The `ScopeTask` context did not, so users couldn't tell at a glance which task's session they were talking to — they had to look at the main panel.

**Improvement:** When `m.activeScope == ScopeTask` and `m.workspace.selectedTaskID` resolves to a cached `taskRecord`, the chat header now appends `" › OC-N: title"` (truncated to 30 chars). If the task number is absent, just the title is shown. This mirrors the exact same pattern as the project breadcrumb above it.

**Why it matters:** In a multi-task workflow, the user may switch between task sessions rapidly. The chat header is the only always-visible indicator of which conversation is active. "Task Session › OC-42: Integrate payments" is immediately recognizable; "Task Session" alone is ambiguous.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-137: agent.pm_removed event silently dropped — PM removal invisible to user

**Observation:** `agent.pm_removed` fires when a PM agent is removed from a project (via `DELETE /agents/{id}/project-assignments/{pid}`). The event went unhandled, so when an agent was unassigned from the PM role the activity log showed nothing.

**Improvement:** Added a handler in `applyWorkspaceCommand` that appends a "PM agent removed from project" entry to the activity log. When the removed agent's name can be resolved from the cached agents list, the entry is more specific: "PM removed: <agent name>".

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-138: Done section empty after task completion via SSE — DoneTasks not updated in real-time

**Observation:** When `task.status_changed` fired and a task in the selected project moved to "done", the code updated the task's `WorkStatus` in `selectedProject.Tasks` and incremented `DoneCount`. However, `selectedProject.DoneTasks` — the separate list used by the Done section when `d` is toggled — was never updated. If the user pressed `d` to show done tasks after a real-time completion event, the Done section showed the updated count badge but an empty task list (or the stale list from the last full reload).

**Improvement:** When `task.status_changed` (or `task.completed`) fires with a done-boundary transition (`to_status` ∈ {done, approved, cancelled}) in the currently viewed project, `applyWorkspaceCommand` now returns `loadProjectDetailCmd` to refresh the project detail. This ensures `DoneTasks` is populated correctly after real-time completion events.

**Why it matters:** The Done section (`d` key toggle) is a key workflow tool for seeing how much has shipped in a sprint. If it shows a count badge but empty list after an SSE completion event, users lose trust in the real-time data.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-139: EventReducer silently dropped 15 event types — wrong names + missing registrations

**Observation:** The `EventReducer.knownEventTypes` map in `realtime.go` is the accept list for SSE events: events whose type is NOT in the map return `wasApplied=false`, and the `OnEvent` callback in `cmd/ottercamp/tui.go` skips forwarding them (`if !applied { return }`). The map contained two wrong event names and was missing 13 others:

Wrong names (server never matches):
- `"task.status.changed"` → server sends `"task.status_changed"` (underscore)
- `"task.flow.advanced"` → server sends `"flow.advanced"` (different prefix entirely)

Missing (handled in `model.go` but never received):
- `task.completed`, `task.created`, `flow.started`, `flow.rejected`
- `inbox.item_created`, `budget.anomaly_detected`, `task.merged`
- `project.deployed`, `project.rollback_initiated`, `deploy.approval_requested`
- `tool.capability_denied`, `agent.pm_removed`, `memory.extracted`, `mcp.catalog.changed`

This meant **every task status update, flow advance, inbox notification, budget alert, and project deployment event was silently dropped by the reducer before it could reach the model.** All the EX-129–EX-138 handlers were unreachable in production.

Additionally, `workspace.go`'s `applyRealtimeEnvelope` had two matching dead-code cases (`"task.status.changed"` and `"task.flow.advanced"`) that also used wrong event names and incorrect payload field names (`status` instead of `to_status`, non-existent `session_id` field).

**Improvement:**
1. Fixed `NewEventReducer` in `realtime.go`: replaced the two wrong names with the correct server-side names, and added all 13 missing workspace event types.
2. Removed the two dead cases from `workspace.go`'s `applyRealtimeEnvelope` (the model-level `applyWorkspaceCommand` already handles these correctly with the right names).
3. Added `TestEventReducerWorkspaceEventNames` in `realtime_test.go` to assert that each correct event name is accepted (`applied=true`) and the old wrong names are rejected.

**Why it matters:** Without this fix, every workspace SSE event — task completions, flow advances, inbox items, budget alerts, deployments — was silently discarded at the reducer boundary, making the entire real-time workspace update system non-functional. The TUI appeared connected and streaming but showed no live data changes beyond chat messages.

**Effort:** Low (bug in config map, not logic)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-140: Project lifecycle, session lifecycle, run failures, and message redaction unhandled

**Observation:** Several server-emitted events had no TUI handlers: `project.created/updated/archived/deleted`, `chat.session.created/closed`, `run.failed`, `run.dead_lettered`, `chat.message.redacted`. These events were previously also blocked by the reducer gap fixed in EX-139 — with the reducer fixed, adding handlers makes them visible.

- Project created/archived/deleted: sidebar needs to reload to show new/missing projects
- Project updated: if the user is viewing that project's detail, it needs a reload
- Chat session created/closed: sidebar needs to reload to show the new or archived session
- Run failed: users need immediate visibility when an agent run fails
- Run dead-lettered: permanent failure after exhausted retries deserves a prominent warning
- Chat message redacted: a message removed via API should show `[Redacted]` in the chat view

**Improvement:**
- `project.created`: appends "project created: slug" to activity log; triggers `loadSidebarDataCmd`
- `project.archived`: appends "project archived: slug" to activity log; triggers `loadSidebarDataCmd`
- `project.deleted`: appends "project deleted: slug" to activity log; triggers `loadSidebarDataCmd`
- `project.updated`: triggers `loadProjectDetailCmd` only if the event's `project_id` matches the currently selected project; no-op otherwise
- `chat.session.created`: triggers `loadSidebarDataCmd` (new session needs to appear in sidebar)
- `chat.session.closed`: appends "active session closed" if it's the active session; triggers `loadSidebarDataCmd`
- `run.failed`: sets statusMessage `"⚠ Run failed: <reason>"` (truncated to 60 chars)
- `run.dead_lettered`: sets statusMessage `"⚠ Run dead-lettered after N attempt(s): <last_error>"`
- `chat.message.redacted` (in `applyChatEnvelope`): replaces the message's content with `"[Redacted]"` if the session matches; ignores events for non-active sessions
- Added all 9 new event types to `knownEventTypes` in `realtime.go`

**Why it matters:** Project lifecycle changes (new sprint, archive old project) and session management are common operations in a team workspace. Without project.created→sidebar reload, users must manually press `r` to see a new project. The run.failed/dead_lettered warnings surface critical agent failures directly in the status bar so users know to investigate. Message redaction compliance means a message removed via the API disappears from the TUI view immediately.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-141---

## EX-141: Agent lifecycle, supervisor escalation, and status bar truncation too short

**Observation:** Three groups of issues:

1. `agent.expired` and `agent.promoted` events were unhandled. AGENTS view showed stale data after lifecycle changes.
2. `supervisor.escalation_created` was unhandled. Users had no visibility into stuck-run recovery.
3. Status bar truncated at 60 chars; ⚠ warning messages added in EX-140 were cut off.

**Improvement:**
- `agent.expired`: appends "agent expired: reason" to activity + calls `loadAgentsCmd`
- `agent.promoted`: appends "agent promoted to staff" to activity + calls `loadAgentsCmd`
- `supervisor.escalation_created`: sets statusMessage `"⚠ Supervisor escalation: run <id[:8]> appears stuck. Recovery initiated."`
- Status bar truncation raised from 60 → 80 chars
- All 3 event types added to `knownEventTypes`

**Why it matters:** Agent lifecycle and supervisor escalations are key operational signals in a multi-agent workspace. 80-char status bar fits all current warning messages.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-142---

## EX-142: task.review_rejected and chat.session.mode_changed unhandled

**Observation:** Two more event types were silently dropped:

1. `task.review_rejected` — emitted when a reviewer rejects a task. Users had no way to know a task was rejected without manually polling.
2. `chat.session.mode_changed` — emitted when session mode transitions (sync↔async). Users couldn't track these transitions in the activity log.

**Improvement:**
- `task.review_rejected`: appends activity entry `"<task-label>: review rejected — <reason[:40]>"` with task label from workspace cache
- `chat.session.mode_changed`: appends activity entry `"session mode: <old> → <new>"`
- Both added to `knownEventTypes` so the reducer no longer drops them

**Why it matters:** Task review rejections require immediate attention from the assignee. Mode changes affect agent behavior and should be auditable in the activity feed.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-143---

## EX-143: Run failure events disappeared from view after 5s (status bar only)

**Observation:** `run.failed` and `run.dead_lettered` only set `statusMessage`, which auto-clears in 5 seconds. After that, there was no persistent record of the failure — users had no way to see that a run had failed without checking external logs.

**Improvement:** Both events now also append to the activity log (Activity view). The log entry persists for the entire session and is visible via the `activity` command or Alt-8 shortcut.

**Why it matters:** Run failures are critical operational signals. The 5-second status bar is insufficient for failures that require investigation. The activity log provides a durable audit trail.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-144---

## EX-144: task.review_rejected didn't reload task detail

**Observation:** When `task.review_rejected` fired, the activity log was updated but `loadTaskDetailCmd` was never called. If the server clears the `RequiresHumanReview` flag when a task is sent back for rework, the ⚠ badge in the task view would remain stale until a manual refresh.

**Improvement:** `task.review_rejected` now calls `loadTaskDetailCmd(payload.TaskID, ...)` to pick up the updated flag and task status from the server.

**Why it matters:** Stale human review badges confuse the operator — a "fix needed" badge on a task already back in progress is misleading.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-145---

## EX-145: deploy.approval_requested was transient (status bar only)

**Observation:** `deploy.approval_requested` set a status bar message (5s then gone). Operators who missed the notification had no way to discover the pending approval from within the TUI.

**Improvement:** Also appends to the activity log so the approval request is visible in the Activity view until explicitly dismissed.

**Why it matters:** A pending deploy approval can block production. It should not be a silently disappearing notification.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-146---

## EX-146: chat.session.created silently reloaded sidebar without logging

**Observation:** `chat.session.created` called `loadSidebarDataCmd` but added nothing to the activity log. Users couldn't tell from the Activity view when a new session was created.

**Improvement:** Now prepends a "session created: <scope_type>" entry to the activity log, falling back to "session created" if the scope isn't in the payload.

**Why it matters:** Session creation is an important event in async workflows (project agents creating task sessions). Logging it makes the workflow more observable.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-147---

## EX-147: Activity log icons wrong for failures and pending actions

**Observation:** `activityIcon` checked for `: blocked`, `: rejected`, `: deferred`, `: failed` markers. All new entry formats added in EX-143 through EX-146 used different text:
- `"run failed: ..."` — doesn't contain `": failed"` (the pattern was `": failed"`, not `"failed"`)
- `"run dead-lettered (...): ..."` — no match
- `"OC-7: review rejected"` — has `"rejected"` but not `": rejected"` before it
- `"Deploy pending approval: ..."` — should be warning (◌), was getting ✓
- `"rollback to ..."` — should be warning (◌), was getting ✓

All these entries were incorrectly displayed with the ✓ success icon.

**Improvement:** Extended `activityIcon` with explicit string markers for:
- Error (✗): `"run failed"`, `"dead-lettered"`, `"review rejected"`, `"agent expired"`
- Warning (◌): `"rollback"`, `"deploy pending approval"`, `"supervisor escalation"`

**Why it matters:** Icon color is the fastest signal in a busy activity log. Failures showing ✓ green look like successes — a serious readability hazard in a production ops context.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-148---

## EX-148: Task view approve/reject/defer hints not functional (dead keybindings)

**Observation:** When a task had `RequiresHumanReview=true`, the task detail view showed:
```
a·approve  ·  x·reject  ·  f·defer  ·  o·open task session
```
But pressing `a`, `x`, or `f` from ViewTask did nothing — the handlers only checked `mainView == ViewInbox`. The hint existed but the shortcuts were dead.

**Improvement:**
- Added `applyInboxActionForTask(taskID, action)` method to `workspaceState` that finds the inbox item for the selected task and delegates to the existing `applyInboxAction` logic.
- Extended `handleWorkspaceRune` `a`/`x`/`f` cases to also act when `mainView == ViewTask` and the task `RequiresHumanReview` is true.
- No-op when no inbox item exists for the task (avoids acting on wrong item).

**Why it matters:** The hint was a lie. Users would follow the on-screen instructions and nothing would happen, creating confusion and forcing them to navigate to the Inbox view to perform the same action. This is a core workflow for human-in-the-loop agent review.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-149---

## EX-149: Task view help bar didn't show approve/reject/defer hints; :help missing commands

**Observation:** Two related gaps after EX-148:

1. The contextual help bar for ViewTask showed `"Enter open session · Esc project · ..."` but didn't mention `a/x/f` even when `RequiresHumanReview=true` and the keys were now functional.
2. The `:help` command listed only `":focus :frank :dashboard :inbox :send :cancel-turn :sidebar :tour dismiss :quit"` — missing `:scope`, `:queue`, `:agents`, `:activity`, `:merges`, `:schedules`, `:project`, `:task`.

**Improvement:**
- `commandFallbackHelp` for ViewTask now conditionally appends `"· a approve · x reject · f defer"` when the selected task has `RequiresHumanReview=true`.
- `:help` command output updated to include all supported commands.
- Help view "a/x/f/o" key description updated to mention both inbox and task view contexts.

**Why it matters:** After EX-148, the keys worked but users couldn't discover them from the contextual help bar. Discoverability is as important as functionality.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-150---

## EX-150: Project view task list missing ⚠ human review badge

**Observation:** The dashboard task board (EX-094) shows `⚠` on tasks with `RequiresHumanReview=true`. But the project view task list did not. A user switching from the dashboard to the project view after an `inbox.item_created` event would see the task without any review indicator — potentially missing an urgent action item.

**Improvement:** `renderProjectView` now cross-references `m.workspace.tasks[task.ID].RequiresHumanReview` for each task in the list. When true, appends `" ⚠"` to the truncated title, consistent with the dashboard board behavior.

**Why it matters:** Both the dashboard and project view should give consistent visual signals. An operator switching to project view for detailed navigation shouldn't lose the ⚠ badge visibility.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-151---

## EX-151: Sidebar task nodes missing ⚠ human review badge

**Observation:** Completing the consistency set from EX-094/EX-150: the dashboard board and project view showed `⚠` for tasks requiring human review, but the sidebar task nodes under expanded project trees did not. A user navigating via the sidebar would see no visual indicator that a task needs their attention.

**Improvement:** `renderSidebarNode` `sidebarKindTask` case now cross-references `m.workspace.tasks[node.TaskID].RequiresHumanReview`. When true, appends `" ⚠"` to the label.

**Why it matters:** The ⚠ badge is the primary discoverability signal for urgent human-in-the-loop tasks. If it only appears in some views but not the sidebar (the main navigation surface), users miss it when they're not on the dashboard.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-152---

## EX-152: `RequiresHumanReview` not synced when project tasks load after inbox

**Observation:** Race condition in task loading order. `syncTaskHumanReviewFromInbox` is called when inbox items are loaded, but it silently skips task IDs that don't yet exist in `w.tasks`. If inbox loads before project tasks (the common startup order), the newly-seeded task records in `setProjectTasks` never get `RequiresHumanReview=true`, so the ⚠ badge would not appear in the sidebar or project view until the next inbox reload.

**Improvement:** `projectTasksLoadedMsg` handler now calls `m.workspace.syncTaskHumanReviewFromInbox()` after `setProjectTasks` seeds new records. This re-applies the inbox cross-reference to all task records including the newly created ones.

**Why it matters:** The ⚠ badge (EX-094, EX-150, EX-151) is the primary discovery signal for human review items. If it only appears after a subsequent event (like another inbox reload) rather than immediately on first load, users would miss urgent action items during their initial session.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-153---

## EX-153: Four high-priority events not persisted in activity log (lost on 5s auto-clear)

**Observation:** Four important SSE events only set `statusMessage` and were wiped when the 5-second auto-clear timer fired:
- `supervisor.escalation_created` — agent stuck, recovery triggered
- `tool.capability_denied` — policy blocked an agent's tool call (security event)
- `mcp.catalog.changed` — MCP tool catalog changed (affects agent capabilities)
- `budget.anomaly_detected` — token usage spike (financial risk)

A user who missed the 5-second window had no way to know these events occurred.

**Improvement:** Each event handler now also calls `appendActivity` with a brief entry, so the information persists in the Activity view after the status bar clears.

**Why it matters:** Escalations and policy denials are operational signals that operators need to review even if they weren't watching the status bar at the exact moment the event arrived. The Activity log is the audit trail for these events.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-154---

## EX-154: `:help` command output missing `:merges` and `:schedules`

**Observation:** The `:help` command listed view navigation commands but was missing `:merges` and `:schedules`, both of which are valid commands handled by `executeCommand`. Users who discovered these views through the `m` and `v` keybindings couldn't find the matching `:` commands.

**Improvement:** Added `:merges` and `:schedules` to the `:help` output string.

**Why it matters:** The command palette is a discovery mechanism. Missing commands imply they don't exist, leading users to think features aren't available.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-155---

## EX-155: Help view and command mode bar missing key commands

**Observation:** Two related documentation gaps:

1. The `?` help view's Commands section only listed 7 commands (`:frank`, `:dashboard`, `:inbox`, `:project`, `:task`, `:focus`, `:send`, `:cancel-turn`, `:quit`), omitting: `:agents`, `:activity`, `:merges`, `:schedules`, `:scope`, `:queue`, `:sidebar`, `:inbox <action>`, `:tour dismiss`. These are all fully-implemented commands that users can't discover from the help screen.

2. The command mode bar (status line shown while typing `:`) didn't mention `:agents`, `:merges`, `:schedules`, `:scope`, or `?·help`, making it hard to discover navigation commands beyond the basic set.

**Improvement:**
- `renderHelpView` Commands section now lists all command categories with usage descriptions.
- `commandFallbackHelp` commandMode branch now includes the full view navigation set and a `? help` reminder.

**Why it matters:** The `?` help screen is the primary discovery surface for commands. An incomplete help screen leads users to believe commands don't exist when they do — resulting in unnecessary workarounds or feature ignorance.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-156---

## EX-156: Project view fallback path used sidebar node IDs instead of task UUIDs

**Observation:** When `proj.Tasks` is empty (project detail not yet loaded from API) and the project view falls back to sidebar child task nodes, `SidebarTaskItem.ID` was set to `child.ID` (the sidebar node ID like `"task-abc123"`) instead of `child.TaskID` (the raw UUID `"abc123"`). This caused three bugs:

1. `m.workspace.tasks[task.ID]` lookup used the wrong key → `RequiresHumanReview` check always failed → ⚠ badge never appeared in the fallback path
2. `loadTaskDetailCmd(taskID)` received the wrong key → task detail API call used wrong ID
3. `TaskNumber` was not copied → `"OC-N:"` prefix never rendered in the fallback list

**Improvement:** Changed `ID: child.ID` to `ID: child.TaskID` and added `TaskNumber: child.TaskNumber` in the fallback path.

**Why it matters:** The fallback path is triggered whenever a user selects a project before the project detail API call completes (common when navigating quickly). All three bugs combined mean the fallback view was effectively broken for navigation and showed incorrect badges.

**Effort:** Low (one-line fix + field addition)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-157---

## EX-157: `tui.command` navigate handler missing `task` case

**Observation:** The `tui.command` SSE event with `action="navigate"` handled `target="project"`, `target="inbox"`, and `target="dashboard"`, but had no `case "task"`. When the server emitted `{action:"navigate",target:"task",target_id:"<uuid>"}` (e.g. from Frank directing the user to a specific task), the TUI silently did nothing — no navigation occurred, no error shown.

**Improvement:** Added `case "task":` to the navigate switch in `applyWorkspaceCommand`. Sets `selectedTaskID`, switches to `ViewTask`, sets `ScopeTask`, syncs sidebar and project cursors, and returns `loadTaskDetailCmd`.

**Why it matters:** Server-initiated navigation is used by Frank to guide users to relevant tasks after completing work. Without the task case, this assistant-driven deep-linking feature was completely broken.

**Effort:** Low
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-158---

## EX-158: Activity log icons wrong for budget anomaly and policy denied entries

**Observation:** Activity entries added in EX-153 showed wrong icons:
- `"budget anomaly: ..."` showed ✓ (green success) — should be ◌ (amber warning)
- `"policy denied: ..."` showed ✓ (green success) — should be ✗ (red error)

**Improvement:** Updated `activityIcon` to match "budget anomaly" → warning ◌ and "policy denied" → error ✗. Also added "worker unresponsive" → warning ◌ for the EX-159 entries.

**Why it matters:** Activity icon semantics are the primary signal in the scrolling log. A security policy denial showing as a green checkmark conveys completely wrong meaning — the user might ignore a blocked tool call that needs attention.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-159---

## EX-159: `worker.unresponsive` event only set status bar, not activity log

**Observation:** The `worker.unresponsive` event handler set `m.statusMessage` (which auto-clears after 5 seconds) but never called `appendActivity`. If the user was away or navigated in the 5s window, the warning was lost. There was no record in the activity log of the worker being unresponsive.

**Improvement:** Added `appendActivity(m.workspace.activity, "worker unresponsive: check `ottercamp worker`")` to the handler.

**Why it matters:** Worker unavailability is a critical operational event. If a user's chat message silently hangs because the worker is offline, they need a persistent record to diagnose the issue — not a 5-second toast.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-160---

## EX-160: Inbox approve/reject/defer were local-only, never sent to server

**Observation:** Pressing `a`, `x`, `f` in the inbox view (or ``:inbox approve|reject|defer``) updated local TUI state (removed the item from the list, updated task status locally, added activity entry) but **never made an API call**. On the next inbox reload, all "actioned" items would reappear because the server state was unchanged.

**Root cause:** `applyInboxAction` is a pure in-memory mutation. The key handlers returned `nil` cmd instead of an API cmd.

**Improvement:**
- Added `ActOnInboxItem func(ctx, itemID, action string) error` to `RuntimeHints`
- Added `actOnInboxItemCmd()` async cmd factory
- Key handlers ('a','x','f') now capture the item ID before `applyInboxAction` removes the item from the list, then return the API cmd
- `executeInboxCommand` now returns `tea.Cmd` so `:inbox approve|reject|defer` also sends the server request
- Added `inboxItemIDForTask()` helper for the task-view approval path
- `inboxActionCompletedMsg` handler surfaces server errors in the status bar
- Wired `POST /v1/inbox/{id}/act` in `cmd/ottercamp/tui.go`

**Why it matters:** Inbox actions are the primary human-review workflow. Users pressing approve/reject in the TUI believing they're approving task execution, when in reality nothing happened on the server. This is a data integrity issue — the TUI was silently lying about task approvals.

**Effort:** Medium (architectural threading, 4 key handlers + command handler + RuntimeHints + wiring)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-161---

## EX-161: `chat.session.*` events silently dropped (wrong SSE routing)

**Observation:** `chat.session.created`, `chat.session.closed`, and `chat.session.mode_changed` all have their handlers in `applyWorkspaceCommand` (handling sidebar reloads, activity entries, etc.). However in `tui.go`, all events with a `chat.` prefix were routed to `ChatEnvelopeMsg` → `applyChatEnvelope`. The `applyChatEnvelope` switch had no cases for `chat.session.*` — so these events were silently dropped every time, never reaching their handlers.

**Root cause:** The routing predicate in `tui.go` was `strings.HasPrefix(event.EventType, "chat.")`, which matched all three session events even though their handlers lived on the workspace side.

**Improvement:** Changed the routing condition to `strings.HasPrefix(event.EventType, "chat.") && !strings.HasPrefix(event.EventType, "chat.session.")`. Now `chat.session.*` events route to `WorkspaceEnvelopeMsg` → `applyWorkspaceCommand` where their handlers exist.

**Why it matters:** Every time a chat session was created (e.g. when a task starts), the sidebar should have refreshed to show the new session node. Every time a session closed, the sidebar should have updated. These were silently failing, leaving the sidebar stale until the user manually pressed 'r'.

**Effort:** Trivial (one-line routing condition change)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-162---

## EX-162: 'r' refresh not context-aware for ViewInbox and ViewAgents

**Observation:** In `handleWorkspaceRune`, the `case 'r':` handler refreshed ViewTask (→ task detail) and ViewProject (→ project detail) correctly, but had no special cases for ViewInbox or ViewAgents. Both fell through to the generic `loadSidebarDataCmd` fallback. `loadSidebarDataCmd` only loads sidebar metadata (inbox count, chats, projects) — it does NOT reload the inbox items list or agents list. So pressing 'r' while viewing the inbox or agents panel was effectively a no-op for the content the user was actually looking at.

**Improvement:** Added two new checks in `case 'r':`:
- `m.workspace.mainView == ViewInbox` → returns `loadInboxItemsCmd`
- `m.workspace.mainView == ViewAgents` → returns `loadAgentsCmd`

**Why it matters:** Users pressing 'r' to refresh the inbox expect to see the current list of pending review items. With the bug, they'd get the sidebar count refreshed but the items list would remain stale, potentially showing already-handled items or missing new ones.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-163---

## EX-163: Queued messages promoted locally but never sent to the server

**Observation:** When the user types a message while an agent turn is active, `sendOrQueueInput` stores the message in a local `queuedMessages` list (correctly deferring the send). When the turn ends, `completeTurnAndPromoteQueue` was called — it showed the message in the local chat, set `activeTurn=true`, but **never called `requestChatSendCmd`**. The server never received the queued message. The user would see their message in the chat, the "waiting for response" spinner would be active, and the agent would never respond because it had no message to process.

**Root cause:** `completeTurnAndPromoteQueue` had `void` return type; it only mutated local state. `applyChatEnvelope` also returned `void`, so even if `completeTurnAndPromoteQueue` returned a cmd, it would have been discarded.

**Improvement:**
- Changed `completeTurnAndPromoteQueue` to return `tea.Cmd` (the `requestChatSendCmd` for the promoted message)
- Changed `applyChatEnvelope` from `void` to `tea.Cmd` return type so the send cmd propagates up
- Updated the `ChatEnvelopeMsg` case in `Update` to use the returned cmd
- Updated all bare `return` statements inside `applyChatEnvelope` to `return nil`

**Why it matters:** Message queuing is the UX for uninterrupted conversation (type while the agent responds, messages are sent in order). If queued messages are silently dropped, users lose messages without any indication — the conversation stalls and there's no way to know why.

**Effort:** Medium (signature changes to two functions + 10+ bare return statements updated)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-164---

## EX-164: `:scope` command discarded the history-reload cmd

**Observation:** The `[` and `]` keys cycle through chat scopes and correctly use the returned `tea.Cmd` from `switchScope` (which includes a `loadChatHistoryCmd` when switching to a UUID session). But the `:scope` command in `executeCommand` called `m.switchScope(normalizeScope(fields[1]))` and **silently discarded the returned cmd**. Typing `:scope task` would update the scope indicator but the chat panel would remain empty or stale.

**Improvement:** Changed `:scope` handler from `m.switchScope(...)` (discarding return) to `return m.switchScope(...)`.

**Why it matters:** The `:scope task` command is the keyboard path for switching to task context. If history isn't reloaded, users see an empty or wrong chat after typing the command.

**Effort:** Trivial (one-line change)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-165---

## EX-165: Ctrl-G / `:frank` / `0` didn't reload chat history after switching sessions

**Observation:** `jumpToFrankSession` (called by `Ctrl-G`, `0` outside chat input, and `:frank`) set `m.activeSession` to the Frank/org session UUID but never called `loadChatHistoryCmd`. If the user had navigated to a task session (which cleared chat messages and loaded task history), pressing Ctrl-G would switch the scope indicator back to "org" but the chat panel would still show the task session's messages. The chat header would say "Frank" while the content was the task conversation.

**Improvement:** Changed `jumpToFrankSession` from `void` return to `tea.Cmd`. When switching to a valid UUID session, it now clears `chatMessages`, sets `chatHistoryLoading = true`, and returns `loadChatHistoryCmd`. All three call sites (`KeyCtrlG`, `'0'` key, `:frank` command in `updateCommandInput`, and `executeCommand`) updated to use the returned cmd.

**Why it matters:** After pressing Ctrl-G or 0, the chat should immediately show Frank's conversation. Showing stale task messages under the Frank header creates a deeply confusing state where the heading and content don't match.

**Effort:** Low (function signature change + 4 call site updates)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-166---

## EX-166: `'n'` (next unread) didn't reload chat history after jumping to session

**Observation:** Pressing `'n'` to jump to the next unread session called `m.workspace.selectSidebarNode()` which sets the session ID, then set `m.activeSession`. But it never cleared `chatMessages` or called `loadChatHistoryCmd`. If the user had been reading a different session, the chat panel would show that session's conversation while the header displayed the new unread session. The "3 unread messages" badge would disappear from the sidebar (cleared by selectSidebarNode), but the messages themselves would never appear in the chat.

**Improvement:** After `selectSidebarNode()`, if the new session ID looks like a UUID and `LoadChatHistory` is configured, clear `chatMessages`, set `chatHistoryLoading = true`, and return `loadChatHistoryCmd` (mirrors the behavior of sidebar Enter selection).

**Why it matters:** `'n'` is the keyboard shortcut for checking your "inbox" of unread conversations. If pressing it shows the wrong session's content, users will miss messages from agents waiting for their input.

**Effort:** Trivial
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-184---

## EX-184: `r` in ViewProject only reloaded project detail, not project tasks

**Observation:** The 'r' refresh key in ViewProject dispatched `loadProjectDetailCmd` but not `loadProjectTasksCmd`. Real-time SSE events (task.created, task.status_changed) keep the task list fresh during an active session, but a user who came back from sleep, or whose connection was briefly interrupted, would have stale task data. Manual refresh with 'r' only updated the project metadata (description, delivery mode, done count) without fetching the task list.

**Improvement:** Changed `return true, loadProjectDetailCmd(...)` to `return true, tea.Batch(loadProjectDetailCmd(...), loadProjectTasksCmd(...))`.

**Why it matters:** The 'r' key is the user's fallback when data looks wrong. If it only partially refreshes, users may think the project is correctly shown even when tasks are missing or have stale statuses.

**Effort:** Trivial (wrap in tea.Batch, add loadProjectTasksCmd)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-183---

## EX-183: Failed inbox action left inconsistent local state without recovery

**Observation:** `inboxActionCompletedMsg` showed an error in the status bar on failure, but returned `nil` as the cmd. The optimistic local update (item removed from inbox list, task status set to approved/rejected/deferred) was not rolled back and not refreshed from the server. A failed `approve` would leave an inbox item permanently missing from the local list even though it still needed review on the server.

**Improvement:** On error, dispatch `loadInboxItemsCmd` to sync local state with the server. This is simpler and more robust than a manual undo, and ensures the re-review item re-appears.

**Why it matters:** Silently hiding an unreviewed item after a network error could cause an agent to wait indefinitely for approval that never comes because the user doesn't see it.

**Effort:** Trivial (one line — return cmd instead of nil on error)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-182---

## EX-182: Two paths dispatched history load without clearing stale messages

**Observation:** Two code paths dispatched `loadChatHistoryCmd` without the standard `chatMessages = nil` / `chatHistoryLoading = true` clear:
1. **sidebarKindProject Enter**: when navigating to a project and switching to the Frank/org session, `chatMessages` and `chatMessageIndex` were cleared but `chatHistoryLoading` was NOT set. The loading indicator never appeared; the panel would show blank content briefly.
2. **ViewTask → Enter to open session**: `loadChatHistoryCmd` was dispatched but `chatMessages` wasn't cleared and `chatHistoryLoading` wasn't set. The task session's messages loaded "in the background" while the previous session's messages remained visible.

**Improvement:** Added the standard clear-and-loading block (`chatMessages = nil`, `chatHistoryLoading = true`, `chatMessageIndex = make(...)`) at both call sites.

**Why it matters:** Inconsistency in the loading state causes visual artifacts: the user sees one session's messages while another session is loading. The `chatHistoryLoading` flag drives the `◌ loading messages...` indicator.

**Effort:** Trivial (three lines each)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-181---

## EX-181: `switchScope` didn't clear stale chat messages before loading new session history

**Observation:** When the user called `:scope task` or `:scope org` to switch the active chat scope, `switchScope` dispatched `loadChatHistoryCmd` but didn't clear `chatMessages`, `chatMessageIndex`, or set `chatHistoryLoading = true`. This meant the previous session's messages remained visible in the chat panel until the new session's history arrived — sometimes seconds later. A user switching from the org session to a task session would see Frank's messages in the task chat panel briefly.

**Improvement:** Added the standard clear-and-loading block before `loadChatHistoryCmd`, mirroring the pattern used in EX-165 (`jumpToFrankSession`), EX-166 (unread navigation), EX-168 (sidebar select), and EX-169 (inbox open).

**Why it matters:** Stale messages from a different session are confusing and can mislead users about the conversation history of the newly switched-to session.

**Effort:** Trivial (three lines)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-180---

## EX-180: Escape from ViewTask to ViewProject didn't load data when project detail was missing

**Observation:** `handleEscapeKey` navigated from ViewTask back to ViewProject when `selectedProjectID != ""` but returned `void` — it could never dispatch a tea.Cmd. When the project detail hadn't loaded yet (same timing gap as EX-176 but via the back-button path), pressing Escape would show a blank project view with no task list.

**Improvement:** Changed `handleEscapeKey` signature from `func() void` to `func() tea.Cmd`. The call site now propagates the cmd via `return m, m.handleEscapeKey()`. Added the same `selectedProject == nil` guard from EX-176: when the detail is missing, dispatches `loadProjectDetailCmd + loadProjectTasksCmd` via `tea.Batch`.

**Why it matters:** Escape is the intuitive "back" key. If pressing back from a task shows an empty project board, users will think the project has no tasks rather than recognizing it as a loading issue.

**Effort:** Low (signature change + guard copied from EX-176)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-179---

## EX-179: `tui.command navigate inbox` didn't load fresh inbox data

**Observation:** The `tui.command` SSE event handler's `"inbox"` navigation case set `mainView = ViewInbox` and returned `nil`. The inbox list would be empty unless inbox data had been previously loaded by another path. The `"project"` and `"task"` cases correctly dispatched data loads, but `"inbox"` was inconsistent.

**Improvement:** Added `return loadInboxItemsCmd(m.runtimeHints)` in the `"inbox"` navigation case, mirroring the `'i'` key and `:inbox` command behaviours.

**Why it matters:** Server-driven navigation to the inbox (e.g. when an agent sends a review request and redirects the user) would show a blank inbox, defeating the purpose of the navigation event.

**Effort:** One line
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-178---

## EX-178: Opening a task from project view or dashboard didn't set `activeScope = ScopeTask`

**Observation:** `handleEnterKey` for `ViewProject` and `ViewDashboard` navigated to ViewTask but never set `m.activeScope = ScopeTask`. The sidebar task-node and inbox open paths both set `activeScope` explicitly. Without it, `assistantLabel()` would return the wrong agent name (org-level Frank instead of the task's assigned agent), and the chat header scope indicator ([task]/[project]/[org]) would show the wrong value.

**Improvement:** Added `m.activeScope = ScopeTask` to the `ViewProject → Enter` path (before the `loadTaskDetailCmd` return) and the `ViewDashboard → Enter` path (after `setMainView(ViewTask)`). Both paths already navigated correctly; this was a missing one-liner.

**Why it matters:** The scope indicator is how users know which agent they're talking to in the chat panel. An incorrect scope makes the assistant label misleading, especially when switching between an org session and a task session.

**Effort:** Trivial (two one-line additions)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-177---

## EX-177: `:sidebar select` didn't dispatch data loads for project/task/inbox nodes

**Observation:** The `:sidebar select` command (EX-168) only reloaded chat history after calling `selectSidebarNode()`. It had no awareness of the selected node's kind. The keyboard Enter path in `handleEnterKey` dispatches `loadProjectDetailCmd + loadProjectTasksCmd` for project nodes, `loadTaskDetailCmd` for task nodes, and `loadInboxItemsCmd` for the inbox node. `:sidebar select` skipped all of these, so the corresponding view would be blank.

**Improvement:** Captured the current node before `selectSidebarNode()` and added a `switch node.Kind` block to dispatch the appropriate data loads, building a `tea.Batch` that includes them alongside the existing history reload. The logic mirrors the `handleEnterKey` path so that `:sidebar select` and Enter are now functionally equivalent.

**Why it matters:** `:sidebar select` is the primary way tmux users navigate the sidebar (many avoid mouse clicks and use command mode). Without data loads, selecting a project in tmux mode produced a blank project view.

**Effort:** Low (add node capture + switch block, convert return to tea.Batch)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-176---

## EX-176: 'p' key showed empty project view when detail hadn't loaded yet

**Observation:** The 'p' key handler switched to ViewProject and returned `nil` for the command — it never checked whether `selectedProject` was actually loaded. This created a blank task list when the user pressed 'p' before the project detail load (dispatched when expanding the project in the sidebar) had completed. Example: user clicks a project to expand it, immediately clicks a child task, then presses 'p' — the initial project detail load was still in-flight.

**Improvement:** In the 'p' handler, when `selectedProject == nil`, added a `tea.Batch` of `loadProjectDetailCmd` + `loadProjectTasksCmd`. Once both return, the project view renders with the full task list as expected.

**Why it matters:** The project view is the primary task management surface. On slow connections or after jumping from inbox to task, the project view could be blank indefinitely with no indication that anything was loading.

**Effort:** Trivial (guard + two-cmd Batch)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-175---

## EX-175: Inbox "open" paths didn't load task detail

**Observation:** All three inbox "open" paths ('o' key, Enter on ViewInbox, `:inbox open` command) called `applyInboxAction("open")` which sets `selectedTaskID` and navigates to ViewTask — but none of them dispatched `loadTaskDetailCmd`. If a user navigated directly to the inbox (via 'i' key or `:inbox`) without ever expanding the project in the sidebar, `m.workspace.tasks[taskID]` would have no `TaskNumber`, `Description`, or `AgentName`. The task view would render just the raw task ID.

**Improvement:** In all three open paths, after `applyInboxAction("open")`, added `loadTaskDetailCmd(taskID, m.runtimeHints)` via `tea.Batch` alongside the existing history reload. This mirrors the sidebar task-node Enter path which always fetches full task detail on selection.

**Why it matters:** Users arriving from the inbox are reviewing tasks for the first time. Showing "Task: some-uuid" instead of "OC-42: Build the thing — assigned to Ellie" is a poor first impression and makes the task view useless for approval decisions.

**Effort:** Low (three call sites, each replaced with `tea.Batch(loadTaskDetailCmd, loadChatHistoryCmd)`)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-174---

## EX-174: Stale project detail overwrote current project after rapid navigation

**Observation:** `projectDetailLoadedMsg` had no guard to check whether the loaded project still matched the currently selected project. If a user clicked project A, then immediately clicked project B before A's detail response arrived, A's detail would be stored in `m.workspace.selectedProject` — overwriting the correct project B detail or replacing a nil with stale data.

**Improvement:** Added a stale-load guard in the `projectDetailLoadedMsg` handler: if `typed.Detail.ID != ""` and it doesn't match `m.workspace.selectedProjectID`, the result is discarded. This mirrors the EX-173 pattern used for chat history.

**Why it matters:** Project detail panels show title, description, task counts. Showing stale data from a previously selected project is confusing, especially since project detail loads can take several hundred milliseconds on slow connections.

**Effort:** Trivial (two lines)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-173---

## EX-173: Stale chat history merged into wrong session when switching rapidly

**Observation:** `chatHistoryLoadedMsg` did not carry the session ID it was requested for. When a user switches sessions quickly (e.g., pressing 'n' multiple times to step through unread sessions), two or more history loads could be in flight simultaneously. The first load to complete would be merged regardless of which session is currently active. If session A's history arrived after the user had already switched to session B (whose messages were cleared), session A's messages would appear in session B's chat panel.

**Improvement:** Added `SessionID string` to `chatHistoryLoadedMsg` and set it in `loadChatHistoryCmd`. In the `chatHistoryLoadedMsg` handler, if `typed.SessionID != "" && typed.SessionID != m.activeSession`, the load is discarded (with `chatHistoryLoading = false` still cleared so the panel doesn't stay in "loading..." state forever). The guard is skipped when `SessionID` is empty (legacy callsites or empty session) to avoid breaking replays during startup.

**Why it matters:** A user reviewing several unread task sessions in rapid succession would otherwise see messages from session A mixed into session B's view — a corrupted, confusing display that's very hard to debug.

**Effort:** Low (add field to struct, propagate through `loadChatHistoryCmd`, add guard in handler)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-172---

## EX-172: Inbox open didn't set `activeScope = ScopeTask`

**Observation:** All three inbox open paths ('o' key, Enter in ViewInbox, `:inbox open` command) set `m.activeSession` to the task's session UUID but never updated `m.activeScope`. If the user had been in `ScopeOrg`, opening an inbox item would leave `m.activeScope = ScopeOrg`. This caused two issues: (1) `assistantLabel()` would return "Frank" instead of the task's agent name (e.g., "Ellie"), because it only checks `m.workspace.tasks` when `m.activeScope == ScopeTask`; (2) the `[task]` / `[project]` / `[org]` scope indicator in the chat header would show the wrong scope level.

**Improvement:** Added `m.activeScope = ScopeTask` to all three inbox open paths, immediately after `m.activeSession = m.workspace.activeSessionID`. Added a table-driven test covering all three paths.

**Why it matters:** After approving or rejecting an inbox item, or opening one to inspect the conversation, the user's chat context should reflect the task they just reviewed. Showing "Frank" as the assistant name when they just opened Ellie's task is misleading.

**Effort:** Trivial (one line added to each of three locations)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-171---

## EX-171: `:inbox open` command didn't reload chat history

**Observation:** The `executeInboxCommand` handler for `"open"` was fixed in EX-169's set of keyboard paths (the `'o'` key and Enter in ViewInbox) but the command-mode equivalent (`:inbox open`) was missed. It updated `m.activeSession` to the task session but never called `loadChatHistoryCmd`, leaving stale messages visible in the chat panel.

**Improvement:** Added the same history-reload block as EX-169's paths: after setting `m.activeSession`, if the session looks like a UUID and `LoadChatHistory` is configured, clear `chatMessages`, set `chatHistoryLoading = true`, and return `loadChatHistoryCmd`.

**Why it matters:** Command mode is the primary navigation tool in tmux. Inconsistency between the 'o' key and `:inbox open` would have re-introduced the bug for tmux users.

**Effort:** Trivial (eight lines)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-168---

## EX-168: `:sidebar select` command didn't reload chat history

**Observation:** `executeSidebarCommand` handled `:sidebar select` by calling `m.workspace.selectSidebarNode()` and updating `m.activeSession`, but never called `loadChatHistoryCmd`. Using the command palette to jump to a session left the chat panel showing the previous session's messages under the new session's header. The keyboard shortcut (Enter) correctly reloaded history, but the command equivalent did not.

**Improvement:** Changed `executeSidebarCommand` return type from `void` to `tea.Cmd`. The `"select"/"open"` case now mirrors the `handleEnterKey` `sidebarKindSession` path: clears `chatMessages`, sets `chatHistoryLoading = true`, and returns `loadChatHistoryCmd`. Updated `executeCommand` to use `return m.executeSidebarCommand(...)`.

**Why it matters:** The command palette is the preferred input path in tmux environments where modifier keys are unreliable. If `:sidebar select` shows stale messages, tmux users see a broken experience every time they navigate sessions.

**Effort:** Low (function signature change + history reload block)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-169---

## EX-169: Inbox open via 'o' key and Enter key didn't reload chat history

**Observation:** In `handleWorkspaceRune` case `'o'` and in `handleEnterKey` for `ViewInbox`, `applyInboxAction("open")` sets `w.activeSessionID` to the task's session UUID, which is then copied to `m.activeSession`. But neither path cleared `chatMessages` or called `loadChatHistoryCmd`. After pressing 'o' or Enter to open an inbox item, the chat panel header would already show the task name (from the updated `activeSession`) while the message list still showed the previous session's conversation. Users reviewing a task with pending approval would see the wrong chat history.

**Improvement:** After `m.activeSession = m.workspace.activeSessionID`, if the session ID looks like a UUID and `LoadChatHistory` is configured, clear `chatMessages`, set `chatHistoryLoading = true`, and return `loadChatHistoryCmd`. Applied in both `handleWorkspaceRune` (`'o'` key) and `handleEnterKey` (`ViewInbox`).

**Why it matters:** The inbox is the primary review interface — approving or rejecting a task should naturally show that task's conversation. Showing stale messages from a previous session while reviewing is confusing and could lead to incorrect approval/rejection.

**Effort:** Trivial (two locations, identical pattern)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-170---

## EX-170: `:inbox` and `:agents` commands didn't load fresh data

**Observation:** In `executeCommand`, the catch-all case for view commands (`dashboard`, `project`, `task`, `inbox`, `activity`, `agents`, `merges`, `schedules`) called `m.workspace.setMainView(view)` without triggering any data fetch. This was inconsistent with the keyboard shortcuts: `'i'` switches to the inbox view AND calls `loadInboxItemsCmd`, but `:inbox` just switched the view. If the inbox hadn't been loaded yet (e.g. fresh session, no sidebar click), `:inbox` would show an empty list with no indication that data was missing.

**Improvement:** Added a `switch view` block after `setMainView`: `ViewInbox` triggers `loadInboxItemsCmd`, `ViewAgents` triggers `loadAgentsCmd`. Other views (project/task/dashboard) don't need this because they are populated by sidebar selection + task detail loads.

**Why it matters:** In tmux environments, `:inbox` is the safest way to navigate to the inbox. If it shows an empty list when data hasn't been loaded, users may think there are no items and miss reviews waiting for them.

**Effort:** Trivial (four lines)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-167---

## EX-167: `chat.session.closed` for the active session set no feedback

**Observation:** When the server sent a `chat.session.closed` SSE event for the currently active session, the TUI updated `m.workspace.activity` (the invisible internal activity log) but never changed `m.statusMessage`. The user had no indication that their session had ended. They would continue to see the old messages in the chat panel and could try to send a new message, which would silently fail or behave unexpectedly.

**Improvement:** In the `applyChatEnvelope` case for `"chat.session.closed"`, after the existing `appendActivity` call, added `m.statusMessage = "Active session closed — select another session to continue."` when `sessionMatchesActive` returns true.

**Why it matters:** Session closure is a significant state change. Without a visible notification, users are left in a zombie state — the chat looks active but is not. A status bar message prompts them to take action.

**Effort:** Trivial (one line)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-185---

## EX-185: Escape cancel didn't clear `activeTurnSessionID`, blocking the next turn's SSE update

**Observation:** When the user pressed Escape to cancel an active turn, `m.activeTurn` was cleared to `false` but `m.activeTurnSessionID` was not cleared. On the next turn, `applyChatEnvelope` had the guard `if m.activeTurnSessionID == "" { m.activeTurnSessionID = ... }` — because the field was non-empty, the guard prevented the new turn's session ID from being recorded. If the user cancelled, then sent a new message to a different session, the TUI might still show the cancel/complete state as belonging to the previous session rather than the new one.

**Improvement:** In the `tea.KeyEsc` branch of `handleChatControlKey`, added `m.activeTurnSessionID = ""` alongside `m.activeTurn = false`. The `completeTurnAndPromoteQueue` and `chat.turn.cancelled` paths already cleared it; now Escape is consistent.

**Why it matters:** `activeTurnSessionID` is used to filter cross-session SSE events — it prevents a supervisor recovery run in another session from re-enabling the spinner for the user's current session. If it's stale after a cancel, the next turn's `chat.turn.started` event can't update it, and cross-session filtering breaks.

**Effort:** Trivial (one line + one comment)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-186---

## EX-186: Switching sessions with an active turn shows a stale spinner in the new session

**Observation:** When a user sends a message to session A (triggering the "Waiting for assistant response…" spinner via `activeTurn = true`) and then switches to session B (via `[ / ]` scope cycle, selecting a project in the sidebar, opening a task session from ViewTask, or pressing Ctrl-G to jump to the org session), the spinner persists in session B even though no turn is active there. When session A's turn eventually completes, it fires `completeTurnAndPromoteQueue`, which clears `activeTurn`, stops the spinner, AND would promote any queued messages into session B — cross-session message leakage.

**Improvement:** Added a helper `clearTurnIfSwitchingSession(newSessionID)` that clears `activeTurn` and `activeTurnSessionID` if the new session differs from the current `activeSession`. Called it in the five session-switch paths: `switchScope`, `jumpToFrankSession`, the `sidebarKindProject` Enter handler, the `ViewTask` Enter handler, and `executeSidebarCommand("select")`.

**Why it matters:** The spinner is the primary visual indicator of whether the assistant is thinking. A stale spinner in the wrong session makes the TUI look broken and could mislead users into thinking their new session is busy. Worse, queued messages written for session A could be incorrectly sent to session B if `completeTurnAndPromoteQueue` fires after a scope switch.

**Effort:** Small (helper method + 5 call sites)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-187---

## EX-187: Queued messages for the old session sent to the new session after a switch

**Observation:** When a user queues multiple messages while an active turn is in progress (session A), then switches to session B, EX-186 clears `activeTurn` and `activeTurnSessionID`. However, `queuedMessages` was NOT cleared. When session A's `chat.turn.completed` SSE event subsequently arrived, `completeTurnAndPromoteQueue` would fire (because `len(queuedMessages) > 0`), dequeue the next message, and call `requestChatSendCmd(m.ActiveChatSession(), text)` — which now resolves to session B's ID. The queued messages intended for session A were sent to session B.

**Improvement:** Extended `clearTurnIfSwitchingSession` to also clear `queuedMessages` and `editingQueued` when the session changes. The check was also generalized from `activeTurn`-only to `activeTurn || len(queuedMessages) > 0` so queued messages are discarded even if the turn was already false when the switch occurs.

**Why it matters:** Cross-session message leakage is one of the worst possible chat bugs — the user's private message to one AI agent is silently delivered to a different agent in a different context. Queued messages are especially dangerous because they're intended for a specific session's ongoing conversation thread.

**Effort:** Trivial (3 lines added to clearTurnIfSwitchingSession)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
---EX-188---

## EX-188: EX-186 re-introduced EX-144 by resetting activeTurnSessionID to "" on session switch

**Observation:** EX-186 cleared `activeTurnSessionID = ""` when switching sessions, inadvertently re-enabling the "accept-all" SSE filter mode. This re-introduced the EX-144 regression: any `chat.turn.started` event from a supervisor recovery run in a different session would pass `sessionMatchesActive` (since `activeTurnSessionID == ""` → `true`), set `activeTurn = true` (showing the spinner in the new session), and overwrite `activeTurnSessionID` with the recovery run's session ID. From that point, normal SSE events from the user's actual new session would be rejected, leaving the TUI in a permanently broken state.

**Improvement:** Changed `clearTurnIfSwitchingSession` to set `activeTurnSessionID = newSessionID` (the UUID of the session being switched to) instead of `""`. This keeps cross-session filtering active. When the new session isn't resolved yet (newSessionID is a placeholder, not a UUID), it falls back to `""` (accept-all) as a safe default, which will be tightened by the next `chat.turn.started` event.

**Why it matters:** This is a second-order correctness fix: EX-186 solved the visible spinner symptom, but the underlying filter state left the TUI vulnerable to EX-144 again. Setting `activeTurnSessionID` to the destination session UUID ensures the TUI correctly accepts events only from the newly active session and rejects everything else from the moment of the switch.

**Effort:** Trivial (conditional assignment instead of clearing to "")
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-189: Stale SessionResolvedMsg from in-flight send overwrites new session's filter

**Observation:** `SendChatMessage` is async — it dispatches `sendChatMessageCmd`, which calls the API and then returns `SessionResolvedMsg{SessionID: resolvedUUID}` to set `activeTurnSessionID`. If the user switches sessions between the send and the resolution (e.g., the API call takes a second), a stale `SessionResolvedMsg` from session A arrives after `clearTurnIfSwitchingSession` has already set `activeTurnSessionID` to session B's UUID. The unconditional assignment would overwrite session B's UUID with session A's UUID, re-introducing cross-session SSE filter confusion.

**Improvement:** Added a guard: `SessionResolvedMsg` only applies when `m.activeTurn == true`. After a session switch, `activeTurn` is always false (cleared by `clearTurnIfSwitchingSession`), so the stale message is silently dropped. When a turn is genuinely in-flight and the UUID arrives, `activeTurn` is true, so the assignment proceeds normally.

**Why it matters:** This closes the last known race in the EX-144 / EX-186 / EX-187 / EX-188 cascade. Without this guard, a slow API call + quick session switch would leave `activeTurnSessionID` pointing at the wrong session, causing the TUI to accept SSE events from session A and reject legitimate events from session B.

**Effort:** Trivial (if guard wrapping one assignment)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-190: g/G on empty dashboard board silently does nothing

**Observation:** Pressing `g` or `G` (vim-style first/last jump) on the dashboard when no active tasks exist was a silent no-op — no status message, no visual feedback. The user had no way to tell if the key binding was broken or if the board was genuinely empty.

**Improvement:** Added `else` branches to both `'g'` and `'G'` dashboard handlers: when `dashboardActiveTasks()` returns an empty slice, set `m.statusMessage = "No active tasks on dashboard."` instead of silently returning.

**Why it matters:** Silent no-ops create doubt ("is my keyboard working?"). A clear status line message explains the state immediately.

**Effort:** Trivial (2 else blocks, 1 line each)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-191: Enter in ViewProject with no open tasks silently opens a blank task detail

**Observation:** Pressing Enter on a project view when all tasks were done/approved/cancelled transitioned the main panel to ViewTask even though no task was selected. The blank task detail said "Opened task detail." despite there being nothing to open — and the status message was misleading.

**Improvement:** When `openTasks` is empty (all tasks done/approved/cancelled), return a contextual status message instead of transitioning: "All tasks complete. Press 'd' to show done tasks." if tasks exist but are all finished, or "No tasks in this project." if the project has no tasks at all. The fallback `setMainView(ViewTask)` is now only reached when no project detail has loaded yet (loading state).

**Why it matters:** Transitioning to a blank view after pressing Enter looks broken. A clear message explains the state and suggests the next action.

**Effort:** Low (6 lines replacing one unconditional transition)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-192: Enter on empty inbox silently does nothing

**Observation:** When the inbox was empty (all items actioned or none loaded), pressing Enter in ViewInbox fell through `applyInboxAction("open")` returning false with no status message and no visual feedback.

**Improvement:** Added an `else` path after the `applyInboxAction` block: if the open action fails, set `m.statusMessage = "No inbox items to open."` and return immediately.

**Why it matters:** Same as EX-190 — silent no-ops create uncertainty. The user needs confirmation that the inbox is empty, not just nothing happening.

**Effort:** Trivial (2 lines)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-193: Empty sidebar shows blank space with no placeholder text

**Observation:** On first launch (or when all sessions/projects are filtered out by search), the sidebar content area was completely blank. There was no message indicating whether data was still loading or genuinely absent — visually indistinguishable from a rendering bug.

**Improvement:** Added an empty-state placeholder in `renderSidebarPanel`: when `len(lines) == 0` after rendering all nodes, display "No projects or chats" (muted style). If a search filter is active but produced no matches, display "No matches for /<query>" instead, giving immediate feedback that the filter is the cause.

**Why it matters:** A blank sidebar panel looks broken. A one-line placeholder communicates state clearly — new users understand data hasn't arrived yet or the search has no results, rather than assuming the app is malfunctioning.

**Effort:** Low (5 lines in renderSidebarPanel)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-194: Sidebar search bar lacks Escape hint

**Observation:** When the sidebar search was active (typing `/<query>`), the search bar showed `"Search /query▌"` with no indication of how to exit. Users unfamiliar with the vim-style `/` search had to guess that Esc exits the mode.

**Improvement:** When the search panel is actively receiving input (`m.searchMode && m.searchPanel == SidebarPanel`), the search bar now renders `"Search /query▌  (Esc to cancel)"` instead of the bare prompt. When the filter is applied but editing is not active, the bar reverts to the minimal display.

**Why it matters:** Discoverability of exit paths is critical for modes. Without an Esc hint, users either get stuck in search mode or accidentally open items when pressing Enter expecting to exit.

**Effort:** Trivial (3 lines replacing the hint string)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-195: Sidebar overflow +N hides cursor when navigating past visible window

**Observation:** The sidebar renders nodes starting from index 0. When the cursor is moved (via j/k, 'G', or `syncSidebarToTask`) to a node beyond the visible area, that node was invisible and only a "+N more" counter was shown. The user could navigate j/k and see the cursor indicator disappear without any indication of why. The "+N more" counter was the only signal, but did not tell the user which item was selected or where to find it.

**Improvement:** Implemented viewport scrolling in `renderSidebarPanel`: compute a `sidebarScrollStart` by scanning display-line counts (accounting for section-divider lines between header groups) until the cursor row is within the window. When scrolling occurs, replace the first rendered line with an "↑ N above" indicator. The existing "+N more" below-fold indicator is preserved for items beyond the bottom of the window.

**Why it matters:** Any time a selected/cursor item is off-screen, the TUI feels broken. Navigation should always keep the current selection visible — this is a fundamental scroll-follows-cursor expectation in any list-based UI.

**Effort:** Medium (60 lines: two-phase scroll calculation + scroll indicator)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---

## EX-196: Approve/reject/defer keys give no feedback when conditions aren't met

**Observation:** Pressing `a` (approve), `x` (reject), or `f` (defer) when the inbox was empty — or when the current task did not require human review — was a silent no-op. No status message was shown, no action was taken, and the user had no way to tell whether they'd successfully acted or whether the key binding was simply not available in this context.

**Improvement:** Added fallback status messages for all three action keys:
- In ViewInbox with no current item: "No inbox item to approve/reject/defer."
- In ViewTask with a task that doesn't have `RequiresHumanReview`: "This task doesn't require review."

These messages are returned immediately (`return true, nil`) so the auto-clear timer fires and the message disappears after 5s.

**Why it matters:** Approval/rejection are high-stakes actions. If a user isn't sure their keypress was registered, they might double-press (causing unexpected behavior) or assume the app is broken. A clear status message disambiguates.

**Effort:** Low (12 lines of fallback cases across 3 action handlers)
**Issue:** N/A
**Status:** [x] Discovered | [x] Implemented | [x] Tested

---
