# OtterCamp Design Brief

**Author:** Jeff G (Head of Design)  
**Date:** 2026-02-04  
**Status:** MVP Requirements Compilation

---

## Sources

Compiled from:
- `SPEC.md` — Full product specification
- `HUMAN-WORKFLOW.md` — Operator experience spec
- `USER-STORIES.md` — Usage scenarios
- Engineering channel discussion (2026-02-04)

---

## Design Principles

### From Spec
- **Draplin/Field Notes aesthetic** — Clean, bold, utilitarian
- **Dark mode by default** (toggle available)
- **Woodcut otter illustration** prominent
- **Fun otter fact in footer** (100+ facts, rotates)
- **Link to Sea Otter Foundation Trust** donation

### From Discussion
- **Mission control mindset** — Operator sees everything at a glance
- **No ambiguity** — Status indicators are immediate and clear
- **Real-time by default** — No refresh needed, WebSocket everywhere
- **Internal workspace, public artifact** — OtterCamp is messy, GitHub is clean

---

## Primary Views

### 1. Dashboard (Home)

**Two-column layout for wide screens:**

#### Main Column
- **Action Items** — Tasks needing human input (🔴 blocked items)
- **Your Feed** — 
  - Top card: "Since you were last here..." progress summary
  - Stream of agent updates qualifying for attention
  - Important emails, market summaries, news
  - Filterable by project/agent

#### Secondary Column  
- **Quick Add** — Otter-themed button → input for thoughts/tasks
- **Projects List** — Cards showing:
  - Project name
  - Status indicator (🔵🟢🟡🔴↺)
  - One-sentence status
  - Time since last update ("6 minutes ago")

#### Project Card States
| Status | Color | Meaning |
|--------|-------|---------|
| 🔵 Blue | Idle | No active tasks |
| 🟢 Green | Cranking | Active work, no blockers |
| 🟡 Yellow | Blocked | Waiting on external/dependency |
| 🔴 Red | Needs You | Blocked on human input |
| ↺ Syncing | Animated | Active operation in progress |

**Sort order:** Red → Yellow → Green → Blue (needs-you first)

---

### 2. Human Inbox

**The action queue.** Only items requiring human judgment.

#### Inbox Item Types
| Type | When Used | Typical Actions |
|------|-----------|-----------------|
| Approval | Before external action (deploy, send, publish) | Approve / Hold / Reject |
| Decision | Fork in the road | Option A / Option B / Other |
| Question | Agent needs information | Free-text response |
| Review | Work complete | Approve / Request Changes |
| Unblock | Agent stuck | Provide resource / Reassign / Cancel |

#### Inbox Item Card
```
┌─────────────────────────────────────────────────┐
│ 🔴 APPROVAL · ItsAlive · from Ivy · 5m ago     │
│                                                 │
│ Approve production deploy?                      │
│                                                 │
│ All tests pass. Staging verified. Ready to ship.│
│                                                 │
│ [Approve]  [Hold]  [Reject]  [View Details →]  │
└─────────────────────────────────────────────────┘
```

#### Interactions
- **One-click resolution** — Act directly from inbox without opening task
- **Snooze** — "Remind me in 2 hours"
- **Auto-expire** — Stale requests (>48h) get flagged for bulk dismiss

---

### 3. Crankfeed (Activity Stream)

**Ambient awareness.** All activity, skimmable, no action required.

```
┌─────────────────────────────────────────────────┐
│ CRANKFEED                            [Filters]  │
├─────────────────────────────────────────────────┤
│ 11:05 · Derek pushed 3 commits to pearl/main   │
│ 11:04 · Ivy marked eng-042 complete            │
│ 11:02 · Stone started content-015              │
│ 11:00 · Nova commented on social-008           │
│ 10:58 · Jeremy approved PR #47                 │
│ ...                                            │
└─────────────────────────────────────────────────┘
```

**Filters:** By project, agent, activity type, time range
**Key rule:** No push notifications from feed. Pull-only.

---

### 4. Project View

**All tasks across a project, regardless of agent.**

#### Views
- **Board view** — Kanban columns by status
- **List view** — Filterable table
- **Dependency graph** — Visual task relationships

#### Board Columns
`Queued` → `In Progress` → `Blocked` → `Review` → `Done`

#### Task Card (Board)
```
┌──────────────────────────────┐
│ eng-042                      │
│ Implement retry logic        │
│                              │
│ 🔧 Derek      P1  ⚡ 2h ago │
└──────────────────────────────┘
```

Shows: Task number, title, assigned agent avatar, priority, last activity

---

### 5. Task Detail View (NEW)

**Full page for single task.** Self-contained context for sub-agent handoff.

#### Sections
1. **Header** — Title, status, priority, assigned agent
2. **Context Block** (collapsible)
   - Files (paths + reasons)
   - Decisions made
   - Acceptance criteria
   - Related knowledge
3. **Activity Timeline** — Every change logged
4. **Discussion Thread** — Comments, @mentions
5. **Dependencies** — Visual upstream/downstream

#### Key Principle
A sub-agent should complete the task with ONLY the context block + codebase access. No conversation history needed.

---

### 6. Agent Status Dashboard (NEW — #61)

**Mission control for agent health.**

#### Per-Agent Status Card
```
┌──────────────────────────────────────────────────┐
│ 🔧 Derek                             [↺ Active] │
│                                                  │
│ Context: ████████░░ 78%        ⚠️ Running hot   │
│ Heartbeat: 2m ago              ✅ Healthy        │
│ Working on: eng-042 "Retry logic"               │
│                                                  │
│ Sub-agents: 2 active                            │
│   └─ vivid-tidepool (schema tests) ✅ done      │
│   └─ warm-mist (WS hub) ⏳ running              │
└──────────────────────────────────────────────────┘
```

#### Status Indicators
- **Context gauge** — % used, warning at 80%, critical at 95%
- **Heartbeat** — Green (recent), Yellow (stale), Red (missed)
- **Activity state** — Idle / Working / Blocked
- **Sub-agents** — Nested list, same indicators

#### Alerts
- Push notification when context > 80%
- Heartbeat miss surfaces immediately
- Agent crash detection (repeated errors)

---

### 7. Instance Management UI (NEW — #65)

**OpenClaw connection management.**

#### Connection Card
```
┌──────────────────────────────────────────────────┐
│ 🦦 sam-openclaw                    [🟢 Connected]│
│                                                  │
│ Version: 1.2.3                                   │
│ Uptime: 4d 12h                                   │
│ Sessions: 12 active                              │
│                                                  │
│ [View Logs] [Restart Gateway] [Reload Config]   │
└──────────────────────────────────────────────────┘
```

#### Controls
- **Restart Gateway** — Full restart with confirmation
- **Reload Config** — SIGUSR1 equivalent
- **View Logs** — Stream recent gateway logs

#### Diagnostics
- Connection test to all services
- Token/API key validation
- Webhook delivery test

---

### 8. Publish UI (NEW — #71)

**Squash internal work → clean GitHub commit.**

#### Publish Flow
1. **Select work** — Choose tasks/commits to publish
2. **Review diff** — See exactly what's going out
3. **Write commit message** — Suggest from internal commits
4. **Confirm** — Author attribution (always human operator)
5. **Push** — One-click, shows success/failure

#### Post-Publish
- OtterCamp issue updated with "Published to GitHub#123" link
- GitHub issue gets comment: "Internal work tracked at OtterCamp#456"
- GitHub issue closed with summary (if linked)

---

### 9. Code Review UI (NEW — from discussion)

**Review agent code changes.**

#### Diff Viewer
- Side-by-side or unified diff
- Syntax highlighting
- Changed files tree

#### Inline Comments
- Click line to add comment
- Threaded replies
- Resolve/unresolve

#### Actions
- Approve
- Request Changes (with comment)
- View in context (link to file browser)

---

### 10. Content Review UI (NEW — from discussion)

**Review markdown/prose before publish.**

#### Preview Mode
- Rendered markdown
- Side-by-side with source (optional)

#### Inline Comments
- Highlight text to comment
- Suggestions (like Google Docs)

#### Actions
- Approve
- Request Changes
- Edit directly (optional)

---

### 11. Chat System (F14)

**Full chat: DMs + issue discussions.**

#### Unified Chat Sidebar
- Direct messages with agents
- Issue-specific discussions
- Unread indicators
- Recent/pinned at top

#### DM View
- Real-time messaging via WebSocket
- History persisted and searchable
- Pull in additional agents (group DM)

#### Issue Discussion
- Threaded conversation attached to task
- @mention to pull agents in
- Separated from Activity Log

---

### 12. Command Bar (F15) — Superhuman-style

**`/` or `⌘K` opens from anywhere.**

#### Core Behavior
- Type to search/filter
- Arrow keys navigate, Enter selects
- Escape closes
- Stays open after action (rapid commands)

#### Commands
- Agent name → jump to DM
- Project name → jump to project
- Task number → jump to task
- Agent + Tab → inline DM draft (don't leave current view)

#### Mobile
- Swipe-up or tap search
- Voice input option

---

## Keyboard Navigation

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate list items |
| `Enter` | Open selected |
| `a` | Approve (in context) |
| `r` | Reject |
| `h` | Hold |
| `Esc` | Back |
| `g i` | Go to Inbox |
| `g p` | Go to Projects |
| `g f` | Go to Feed |
| `/` | Command bar |

---

## Empty States

**No projects:**
```
Welcome to Otter Camp!
Create your first project to get started.
[+ New Project]
```

**Inbox Zero:**
```
✨ Inbox Zero
Your agents are cranking. Nothing needs you right now.
```

**No feed activity:**
```
Quiet so far today.
Activity will appear here as agents work.
```

---

## Real-Time Indicators

- **Connection status** — Live WebSocket indicator in header
- **Reconnecting** — "Reconnecting... (last update: 2m ago)"
- **Live updates** — No page refresh needed anywhere

---

## Auth UI (NEW — from discussion)

**MVP: OpenClaw token auth only.**

#### Login Flow
1. Enter email → receive magic link with token
2. Click link → authenticated
3. Future: GitHub OAuth, Apple login

#### Session UI
- "Connected as sam@example.com"
- Logout option

---

## GitHub Connection UI (NEW — from discussion)

**Link external GitHub repos to projects.**

#### Settings Panel
- **Link Repository** — Enter GitHub URL, authenticate
- **OAuth flow** — Connect GitHub account
- **Branch mapping** — Internal branch → external branch

#### Status Indicators
- "14 internal commits → ready to sync"
- Divergence warning if external has new commits

---

## Exec Approvals Integration (NEW — from discussion)

**"Approve Deploy" buttons that trigger agent actions.**

#### In Inbox
- Approval item with [Approve Deploy] button
- Context shows what will deploy

#### On Click
- Routes approval back to OpenClaw
- Agent receives webhook, continues
- Status updates in real-time

---

## Mobile Considerations

**Priority: Triage, not management.**

#### Mobile Views
1. **Inbox** (default) — Action items only
2. **Projects** — Status cards
3. **Feed** — Crankfeed, scrollable

#### Gestures
- Swipe right → Approve
- Swipe left → Snooze
- Pull down → Refresh

---

## Component Inventory

### Buttons
- Primary (otter blue)
- Secondary (outline)
- Danger (red, for destructive)
- Ghost (text only)

### Status Indicators
- Pill badges (colored)
- Progress gauges (context window)
- Dot indicators (heartbeat)

### Cards
- Project card
- Task card
- Agent status card
- Inbox item card

### Forms
- Text input
- Select/dropdown
- Checkbox
- Radio (for decisions)

### Modals
- Confirmation (destructive actions)
- Quick input (inline add)

### Navigation
- Sidebar (collapsible)
- Tabs (within views)
- Breadcrumbs (deep pages)

---

## Open Design Questions

1. **Command bar vs chat input** — Should typing in command bar be able to route to chat directly?

2. **Notification sound** — Otter chirp? Or professional silence?

3. **Project colors** — Auto-assigned or user-picked?

4. **Agent avatars** — Emoji only, or allow custom images?

5. **Dark mode toggle location** — Settings only, or quick-toggle in header?

---

*End of Design Brief*
