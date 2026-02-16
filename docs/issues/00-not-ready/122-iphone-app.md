# Issue #122: Otter Camp iPhone App

> **Status:** 🚫 NOT READY — Needs Sam's review before moving to `01-ready`

## Overview

Native iPhone app for Otter Camp — giving users mobile access to their agent teams, projects, and conversations. Built with Swift/SwiftUI, talking to the same API (`api.otter.camp` or self-hosted).

## Why Native (Not PWA)

- Push notifications for agent activity, issue updates, mentions
- Background refresh — agents keep working while your phone is in your pocket
- Share extension — send links/text/images from any app directly to an agent or project
- Haptics, native gestures, smooth scrolling, feels right
- App Store presence matters for credibility (especially for Tier 3 customers)
- Camera/mic access for future voice interaction (#113)

## Core Screens

### 1. Dashboard (Home)
- Activity feed — recent agent activity, issue updates, commits
- Quick stats: active agents, open issues, recent commits
- Agent status indicators (online/working/idle/offline)
- Tap any item → deep link to detail view

### 2. Agents
- Grid/list of agents with avatar, name, role, status indicator
- Tap agent → Agent Detail:
  - Current status + what they're working on
  - Recent activity timeline
  - DM conversation (chat with agent directly)
  - Memory/context summary (from #111 Memory system)
- Quick actions: ping agent, assign issue, view recent work

### 3. Agent Chat (DM)
- Full conversational interface with any agent
- Messages route through Otter Camp API → bridge → OpenClaw
- Markdown rendering for agent responses (code blocks, lists, links)
- Image/file attachments (agent can send screenshots, files)
- Typing indicator when agent is working
- Chat history persisted server-side

### 4. Projects
- List of all projects with status, issue counts
- Tap project → Project Detail:
  - Issues list (filterable: open/closed, labels, assignee)
  - Files browser (tree view of repo contents)
  - Recent commits
  - Project settings

### 5. Issues
- Issue list view with filters (project, status, label, assignee, priority)
- Issue detail: title, body (markdown), comments, labels, assignee, status
- Create new issue (project picker, title, body, labels, priority)
- Add comments
- Change status (open → in progress → done)
- Assign to agent or self
- Kanban view option (swipe between columns)

### 6. Inbox / Notifications
- All mentions, assignments, agent questions, review requests
- Grouped by project or chronological
- Mark read/unread
- Tap → navigate to source (issue, chat, etc.)

### 7. Settings
- Account (name, email, org)
- Connection status (bridge online/offline, agent count)
- Notification preferences (per-project, per-agent toggles)
- Appearance (dark/light/auto — dark by default, matching web)
- Server URL config (for self-hosted users)

## API Integration

The app talks to the same REST API as the web frontend. No new backend work needed for core features.

### Endpoints Used

| Feature | Endpoint | Exists? |
|---------|----------|---------|
| Auth (magic link) | `POST /api/auth/magic` + `GET /api/auth/validate` | ✅ |
| Projects list | `GET /api/projects` | ✅ |
| Project detail | `GET /api/projects/:id` | ✅ |
| Issues list | `GET /api/projects/:id/issues` | ✅ |
| Issue CRUD | `POST/PATCH /api/projects/:id/issues` | ✅ |
| Issue comments | `GET/POST /api/projects/:id/issues/:n/comments` | ✅ |
| Agents list | `GET /api/agents` | ✅ (via bridge sync) |
| Agent DM | `POST /api/agents/:id/message` | ✅ |
| Activity feed | `GET /api/activity` | ✅ |
| Files browse | `GET /api/projects/:id/files` | ✅ |
| Health | `GET /health` | ✅ |
| Notifications | — | ❌ Needs new endpoint |
| Push registration | — | ❌ Needs new endpoint |

### New Backend Work Required

1. **Push notification infrastructure**
   - `POST /api/devices` — register APNs device token
   - `DELETE /api/devices/:id` — unregister
   - APNs integration (send push when: agent mentions user, issue assigned, review requested, agent asks question)
   - New DB table: `devices (id, user_id, org_id, platform, token, created_at)`

2. **Notification feed endpoint**
   - `GET /api/notifications` — paginated list of notifications
   - `PATCH /api/notifications/:id` — mark read
   - `POST /api/notifications/read-all`
   - New DB table: `notifications (id, user_id, org_id, type, title, body, source_type, source_id, read, created_at)`

3. **WebSocket for real-time**
   - Agent chat messages need real-time delivery
   - Reuse existing `/ws` endpoint or add `/ws/mobile`
   - Mobile-friendly: handle reconnection on network changes, background/foreground transitions

## Tech Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Swift 6 | Modern concurrency, type safety |
| UI | SwiftUI | Declarative, less code, native feel |
| Min iOS | 17.0 | Enables latest SwiftUI features, covers 95%+ of active devices |
| Networking | URLSession + async/await | No dependencies needed |
| WebSocket | URLSessionWebSocketTask | Built-in, handles reconnection |
| Local storage | SwiftData | Offline cache for projects/issues |
| Push | APNs (via server-side) | Native iOS push |
| Auth | Keychain (token storage) | Secure credential storage |
| Images | AsyncImage + disk cache | Built-in SwiftUI |
| Markdown | AttributedString | Native iOS 17 markdown rendering |

### Zero External Dependencies

The app should ship with **no third-party packages**. Everything needed is in the iOS SDK. This keeps the binary small, avoids supply chain risk, and eliminates version conflicts.

## Project Structure

```
OtterCamp/
├── OtterCamp.xcodeproj
├── OtterCamp/
│   ├── App/
│   │   ├── OtterCampApp.swift          # Entry point
│   │   └── AppState.swift              # Global state
│   ├── Models/
│   │   ├── Agent.swift
│   │   ├── Project.swift
│   │   ├── Issue.swift
│   │   ├── Comment.swift
│   │   ├── Activity.swift
│   │   └── Notification.swift
│   ├── Services/
│   │   ├── APIClient.swift             # HTTP client
│   │   ├── WebSocketClient.swift       # Real-time
│   │   ├── AuthService.swift           # Token management
│   │   ├── PushService.swift           # APNs registration
│   │   └── CacheService.swift          # SwiftData offline cache
│   ├── Views/
│   │   ├── Dashboard/
│   │   │   └── DashboardView.swift
│   │   ├── Agents/
│   │   │   ├── AgentsListView.swift
│   │   │   ├── AgentDetailView.swift
│   │   │   └── AgentChatView.swift
│   │   ├── Projects/
│   │   │   ├── ProjectsListView.swift
│   │   │   ├── ProjectDetailView.swift
│   │   │   └── FileBrowserView.swift
│   │   ├── Issues/
│   │   │   ├── IssuesListView.swift
│   │   │   ├── IssueDetailView.swift
│   │   │   ├── IssueCreateView.swift
│   │   │   └── KanbanView.swift
│   │   ├── Inbox/
│   │   │   └── InboxView.swift
│   │   ├── Settings/
│   │   │   └── SettingsView.swift
│   │   ├── Auth/
│   │   │   └── LoginView.swift
│   │   └── Shared/
│   │       ├── MarkdownView.swift
│   │       ├── AgentAvatar.swift
│   │       ├── StatusBadge.swift
│   │       ├── LabelPill.swift
│   │       └── EmptyStateView.swift
│   ├── Extensions/
│   │   ├── Date+Relative.swift
│   │   └── Color+Theme.swift
│   └── Resources/
│       ├── Assets.xcassets
│       └── OtterCamp.entitlements
├── OtterCampTests/
└── OtterCampUITests/
```

## Design Language

Match the web app's aesthetic:
- **Dark mode default** — deep slate/charcoal backgrounds
- **Warm accent color** — the golden/amber from the web login button
- **Otter emoji** 🦦 as app icon base (custom illustrated version for App Store)
- **Rounded cards** with subtle borders, like the web dashboard
- **SF Symbols** for iconography (native, consistent)
- **Fluid animations** — spring-based transitions, no jarring cuts

### Tab Bar
```
🏠 Home  |  🤖 Agents  |  📋 Issues  |  📁 Projects  |  ⚙️ Settings
```

## Auth Flow

### Hosted (otter.camp)
1. User opens app → Login screen
2. Enters email → app calls `POST /api/auth/magic`
3. Magic link sent to email
4. User taps link → deep link opens app with `?auth=<token>`
5. App validates token via `GET /api/auth/validate`
6. Token stored in Keychain
7. Org picker if multiple orgs

### Self-Hosted
1. User opens app → Settings → enters server URL (e.g., `http://192.168.1.50:4200`)
2. App calls `/health` to verify connection
3. Auto-login via magic link (same flow, pointed at their server)

### Token Refresh
- Tokens have TTL (currently 7 days)
- App checks expiry on launch, refreshes if < 24h remaining
- On 401 → redirect to login

## Offline Support

- **SwiftData** caches: projects, issues, agents, recent activity
- App remains browsable offline (read-only)
- Queued actions (comments, status changes) sync when back online
- Clear "offline" indicator in UI
- Agent chat requires connectivity (no offline queuing for DMs)

## Push Notifications

### Notification Types
| Type | Trigger | Priority |
|------|---------|----------|
| Agent mention | Agent @mentions user in activity | High |
| Issue assigned | Issue assigned to user | High |
| Agent question | Agent asks a question needing human input | High (time-sensitive) |
| Review requested | Issue moved to review | Medium |
| Build/deploy status | CI/deploy succeeds or fails | Medium |
| Agent status change | Agent goes offline unexpectedly | Low |

### Implementation
- APNs with token-based auth (`.p8` key)
- Server sends pushes via `POST` to APNs HTTP/2 endpoint
- Notification Service Extension for rich notifications (agent avatar, preview)
- Background refresh for keeping cache fresh

## Share Extension

"Send to Otter Camp" from any app:
- Select agent or project to send to
- Text → creates message in agent DM or issue comment
- URL → agent can summarize/process
- Image → attached to DM or issue
- Lightweight UI: agent picker + optional note + send button

## Phase Plan

### Phase 1: Core (MVP)
- Auth flow (magic link)
- Dashboard with activity feed
- Agents list + detail + DM chat
- Projects list + detail
- Issues list + detail + create + comment
- Settings (server URL, appearance)
- Push notifications (basic)
- **Ship to TestFlight**

### Phase 2: Polish
- Offline caching (SwiftData)
- Kanban board view
- File browser
- Share extension
- Rich push notifications
- Search (global)
- Widgets (agent status, active issues)

### Phase 3: Advanced
- Voice interaction (#113 — talk to agents via app)
- Live Activities (agent working on your issue)
- Shortcuts integration (Siri: "Ask Frank to...")
- Apple Watch complication (agent status at a glance)
- iPad layout (sidebar + detail)

## Open Questions for Sam

1. **App Store account** — Do you have an Apple Developer account ($99/yr)? Need one for TestFlight + App Store.
2. **App name** — "Otter Camp" or something shorter? ("Otter"? "Camp"?)
3. **TestFlight first or App Store?** — Recommend TestFlight for Phase 1, App Store at Phase 2.
4. **iPad support?** — Universal app from day 1, or iPhone-only for MVP?
5. **Who builds it?** — Codex can scaffold Swift/SwiftUI but has limits. Human review critical for UIKit edge cases. Derek's team, or separate iOS agent?

## Non-Goals (for now)

- Android app (future, after iOS proven)
- Apple Watch standalone app (Phase 3 complication only)
- macOS native app (web app covers this)
- Widget editing/configuration (Phase 2)
- In-app purchases or billing
