---
## Summary

This spec defines the OtterCamp native mobile app, a dedicated monitoring and triage interface built with React Native for iOS and Android. The mobile app is NOT a portable version of the full web UI -- it is a lightweight "remote control" optimized for 30-second, interruption-driven interactions. The operator receives a push notification, taps it, takes action (approve, reject, defer, reply), and puts the phone down. It ships in Phase 4 (post-launch); until then, the mobile-responsive web UI (doc 18) provides basic mobile access.

The app has six core screens: Notifications (the primary entry point and home screen), Dashboard (project health at a glance with inbox badge count), Inbox (the action-required queue with inline approve/reject/defer buttons), Chat (lightweight text sessions with agents -- no file uploads, no steer, no reactions), Task Detail (read-only), and Project Status (read-only). Push notifications are mapped to four urgency tiers (Critical and High push by default; Medium and Low do not), and rich notifications on iOS/Android allow quick-approve actions directly from the lock screen without opening the app. Every notification includes a deep link (`ottercamp://inbox/{id}`, `ottercamp://task/{slug}/{num}`, etc.) that navigates directly to the relevant screen with no intermediate navigation.

Authentication uses biometric unlock (Face ID, Touch ID, fingerprint) with session tokens stored in the platform keychain/keystore, supporting the fast-interaction pattern. Sensitive actions like capability approvals require biometric re-confirmation. The app shares the same REST API and realtime WebSocket/SSE endpoints as the web UI (doc 12) with no mobile-specific backend. Offline support is limited to read-only cached state (dashboard, notifications, recent chat); all mutations require connectivity. Key architectural boundaries: mobile is strictly for reacting and monitoring -- no task creation, flow design, configuration, code diff review, or agent management. These belong on the web UI or TUI.

---

# 19. Mobile UI

## Status: Draft

## Purpose

The mobile app is a monitoring and triage interface for OtterCamp. It is NOT the primary work interface. The operator uses it to check status, respond to urgent items, and issue quick instructions to agents while away from the desk. The design target is 30-second interactions: a push notification arrives, the operator taps it, takes action, and puts the phone down.

The mobile-responsive web UI (doc 18) covers basic mobile access before the native app exists. This spec defines the dedicated native experience optimized for interruption-driven use.

## Build Phase

Phase 4: Hardening and Distribution (see 14-open-questions-and-phasing.md). The mobile app is a post-launch priority. The mobile-responsive web UI provides functional mobile access until then. The native app adds push notifications, biometric auth, and an experience optimized for the phone form factor — things the web cannot deliver well.

## Role and Boundaries

### What the Mobile App IS

- A monitoring dashboard for project health at a glance.
- A push notification endpoint for urgent events.
- A triage interface for inbox items (approve, reject, respond).
- A lightweight chat client for quick exchanges with agents.
- A read-only viewer for task detail and project status.

### What the Mobile App is NOT

- A task creation interface. Tasks are created through conversation in the TUI or web UI.
- A flow design tool. Flows are designed conversationally with the PM.
- A configuration surface. Org settings, model profiles, skill management, MCP connections, and agent configuration require the full TUI or web UI.
- A code review tool. Reviewing diffs, browsing file changes, and detailed work log inspection belong on a larger screen.
- A replacement for the web UI. The mobile app is a complement — a quick-access remote for the system, not a portable version of the full interface.

## Usage Patterns

The mobile app is designed for interruption-driven use. The operator is not sitting down to work — they are responding to something that needs attention. The expected flow:

1. Push notification arrives on the lock screen: "OC-42 needs your review" or "Frank escalated: auth token conflict needs your call."
2. Operator taps the notification.
3. The app opens directly to the relevant item (deep link). No navigation required.
4. Operator reads the context, takes action (approve, reject, reply), and leaves.

Secondary flow: the operator opens the app unprompted to check status.

1. Dashboard shows project health at a glance: how many items in the inbox, any blocked tasks, overall progress.
2. If something looks off, the operator taps into the relevant project or inbox item.
3. Quick scan, possibly a chat message to the PM ("hold off on deploying until I'm back"), then done.

Both flows should complete in under 60 seconds. Most interactions are under 30.

## Core Screens

### 1. Notifications (Primary Entry Point)

The notification list is the app's home screen. Push notifications are the primary way the operator enters the app, but the in-app notification list provides a scrollable history of all events.

**Content:**
- Reverse chronological list of notifications.
- Each notification shows: urgency indicator (color/icon), title, brief description, timestamp, source project/task.
- Notifications are grouped by time (Today, Yesterday, Earlier this week, Older).
- Swipe actions: swipe to dismiss, swipe to open.
- Tapping a notification navigates to the relevant screen (inbox item, task detail, chat session).
- Unread/read visual state. Badge count on the app icon reflects unread urgent + high notifications.

**Urgency Visual Treatment:**
- Critical (escalations, agent failures, blockers requiring human judgment): red accent, prominent icon. Always visible at the top regardless of scroll position if any exist.
- High (review requests, draft actions pending, @mentions): orange accent, standard prominence.
- Medium (task completions, status changes): neutral styling. Visible in the list but no push notification.
- Low (agent started work, routine status updates): subdued styling. Grouped/batched ("4 tasks updated in OtterCamp V2").

### 2. Dashboard

Project health at a glance. The operator opens this to answer: "Is everything okay? Does anything need me?"

**Content:**
- **Inbox badge**: prominent count of pending inbox items. Tapping navigates to the inbox. This is the single most important number on the dashboard.
- **Project cards**: one card per active project, showing:
  - Project name
  - Task counts by status: in_progress, blocked, review, queued (compact bar or pill indicators)
  - Blocked item count (highlighted if > 0)
  - Last activity timestamp ("12 min ago")
- **Quick stats**: total active tasks, tasks completed today/this week, pending inbox items.
- Pull-to-refresh for manual update.

The dashboard is deliberately sparse. It answers "what's the state of things?" and nothing more. Drilling in happens by tapping a project card (navigates to project status) or the inbox badge (navigates to inbox).

### 3. Inbox

The action-required queue. Maps directly to the inbox defined in doc 03. Every item blocks progress somewhere until the operator acts.

**Content:**
- Two sections: Active (pending) and Deferred. Active is the default view.
- Each item shows:
  - Item type icon (escalation, task review, draft action, capability approval)
  - Title and brief description
  - Source project/task
  - Timestamp and urgency indicator
  - Inline action buttons

**Inline Actions:**
- **Task scoping review**: [Approve] [Request Changes] [Defer]. Tapping "Approve" queues the task immediately. "Request Changes" opens a text input for feedback. "Defer" moves to deferred section.
- **Task work review**: [Approve] [Reject] [Defer]. "Reject" opens a text input for feedback that goes back to the agent. On mobile, the diff is not shown inline — a "View on Web" link opens the full diff in the web UI for cases where the operator needs to see the code.
- **Draft action review**: [Approve] [Edit] [Reject] [Defer]. "Edit" opens the staged content (e.g., composed email) for modification before approval.
- **Escalation**: [Open Chat] [Defer]. "Open Chat" navigates to the relevant chat session in context so the operator can respond.
- **Capability approval**: [Approve] [Approve with Constraints] [Deny] [Defer]. "Approve with Constraints" opens a text input.

**Behaviors:**
- Ordered by urgency then arrival time, matching the web UI.
- Acting on an item triggers the downstream action immediately. No additional confirmation — the inbox is the confirmation step.
- Deferred items accessible via a toggle/tab. Restore returns them to active.
- Swipe-to-defer on individual items for quick triage.

### 4. Chat (Lightweight)

Quick text sessions with agents. Enough for status checks and simple instructions. Not designed for extended conversations or complex multi-agent sessions.

**Content:**
- Session list: mirrors the session sidebar from the web UI. Grouped by scope (org, projects, tasks). Unread indicators.
- Chat view: conversation thread with streaming agent responses. Text input at the bottom. Send button.
- Active turn indicator: shows which agent is responding and that work is in progress.

**What is supported:**
- Text input and streaming text responses.
- Read agent messages with rich content (text, code blocks rendered as monospace).
- Start a new message while an agent is responding (queued, not steered — Steer is a web/TUI feature).
- @mention agents by name (basic autocomplete from the session's participant list).

**What is NOT supported:**
- File uploads or attachments. No drag-and-drop on mobile. If the operator needs to share a file, they use the web UI.
- Complex @mention chains (mentioning multiple agents in one message). One @mention per message is the practical mobile limit.
- Steer or edit queued messages. These are power-user features for the web UI where the operator has full keyboard access.
- Reactions. The tap/double-tap gesture space on mobile is better used for basic interactions. Reactions are a web UI feature.
- Work log viewing within chat. Agent tool calls are collapsed to a compact "working..." indicator rather than showing the streaming tool call list.
- Image/artifact preview. Images and artifacts in agent responses show as links/thumbnails. Tapping opens in the system browser or a basic preview.

**Typical Mobile Chat Sessions:**
- "Frank, what's the status of the auth project?" (quick status check)
- "PM, hold off on deploying until I'm back." (simple instruction)
- "Frank, is anything blocked right now?" (situation check)
- "Lori, pause the scheduled inbox checks for today." (quick config change via agent)

### 5. Task Detail (Read-Only)

The operator taps into a task from a notification, inbox item, or project view. Read-only — no task editing from mobile.

**Content:**
- Header: task title, status badge, priority, assignee, project name.
- Flow stepper: compact horizontal step indicator showing flow progress. Same concept as the web UI flow stepper but adapted for mobile width (scrollable horizontally if many nodes).
- Subtask summary: if the current node has subtasks, a compact list showing title and status for each.
- Description and acceptance criteria: collapsible sections.
- Dependencies: "depends on" and "blocks" lists showing linked task titles and statuses.
- History: recent events from `project_task_event`, reverse chronological. Compact: "2h ago — Reviewer approved code review" / "5h ago — Agent started implementation."
- Branch info: branch name displayed. No diff view — diffs require the web UI.

**Navigation:**
- Back button returns to the previous context (notification list, inbox, project status).
- If the task is at a review node and the operator is the reviewer, a banner links to the inbox item for taking action.

### 6. Project Status (Read-Only)

Reached by tapping a project card on the dashboard.

**Content:**
- Project name and description.
- Task summary: counts by status displayed as a compact bar chart or pill row.
- Task list: flat list of tasks in the project, sorted by status (blocked/review first, then in_progress, then queued). Each row shows title, status badge, priority, assignee.
- Merge queue: if any entries exist, a section showing tasks awaiting merge with their status.
- Schedules: compact list of active schedules with last run status and next run time.

**Navigation:**
- Tapping a task navigates to the task detail screen.
- Tapping a schedule shows recent instances (last 5 tasks created by that schedule).

## Push Notifications

Push notifications are the primary entry point to the mobile app. They are mapped to the event urgency tiers defined in doc 02 (Notifications section).

### Urgency-to-Push Mapping

| Urgency Tier | Push Notification | In-App Notification | Examples |
|---|---|---|---|
| Critical | Always | Always | Escalation reached human, agent turn failed and unrecoverable, blocker requiring human judgment |
| High | Always | Always | Task ready for review, draft pending in inbox, @mention in any session, capability approval request |
| Medium | Never (default) | Always | Task completed, task status changed, agent started work |
| Low | Never | Available in feed | Routine status updates, agent activity |

### Push Notification Content

Each push notification includes:
- **Title**: concise action summary ("Review needed: OC-42" / "Escalation: Auth token conflict")
- **Body**: one-line context ("PM finished scoping landing page design" / "Frank escalated — needs your call")
- **Category**: mapped to the item type for action buttons (see Rich Notifications below)
- **Deep link**: URL that navigates directly to the relevant screen in the app

### Rich Notifications (iOS/Android)

Where the platform supports it, push notifications include inline action buttons so the operator can act without even opening the app:

- **Review notifications**: [Approve] [Open] — quick-approve directly from the lock screen, or open the full item.
- **Escalation notifications**: [Open Chat] — jumps to the relevant session.
- **Draft review notifications**: [Approve] [Open] — approve the staged action from the notification.

These quick actions map directly to the inbox item actions. The result is the same as opening the inbox and tapping the button.

### User Configuration

The operator can configure push notification behavior:

- **Per urgency tier**: enable/disable push for each tier. Default: Critical and High push, Medium and Low do not.
- **Per project**: override urgency settings per project ("push everything for Project X, only critical for Project Y").
- **Quiet hours**: suppress all non-critical push notifications during configured hours. Critical notifications always push. Default: no quiet hours configured.
- **Per event type**: granular control ("always push for blockers, never push for task completions").

Configuration is accessible from the mobile app settings screen and from the web UI. Changes sync across both.

### Notification Delivery

Push notifications are delivered via platform-native services:
- **iOS**: Apple Push Notification Service (APNs)
- **Android**: Firebase Cloud Messaging (FCM)

The server-side notification layer (event bus consumer, see doc 02) adds a push delivery channel alongside the existing in-app channel. When a notification event fires, the push channel evaluates the operator's push preferences and, if the event qualifies, sends a push via the appropriate platform service.

**Deduplication**: if the operator has the app open and is viewing the relevant screen when an event fires, the push notification is suppressed. The in-app notification is sufficient. This prevents the annoying pattern of getting a push for something you are already looking at.

## Quick Actions

Quick actions are the things the operator can DO from mobile. The mobile app is deliberately limited to actions that are fast, low-risk, and don't require extensive context.

### Allowed Actions

| Action | Where | Description |
|---|---|---|
| Approve inbox item | Inbox, push notification | Approve a scoping review, work review, draft action, or capability request |
| Reject inbox item | Inbox | Reject a work review or draft action with feedback text |
| Respond to escalation | Chat via inbox | Open the relevant chat session and send a text response |
| Defer inbox item | Inbox | Move to deferred for later |
| Unblock task | Task detail | Remove a dependency from a blocked task (PM confirms via the dependency list) |
| Chat with agents | Chat | Send text messages in any session the operator participates in |
| Pause/resume schedule | Project status | Via chat ("PM, pause the inbox check schedule") |

### NOT Allowed from Mobile

| Action | Reason |
|---|---|
| Create tasks | Requires conversation with agents and context that benefits from a full interface |
| Design flows | Conversational design with the PM requires extended back-and-forth |
| Write or edit skills | Skill documents need a proper text editor |
| Configure projects | Settings changes require the full settings UI |
| Manage agents | Hiring, firing, profile changes — handled through Lori in the web UI or TUI |
| Manage MCP connections | Configuration requires the full settings surface |
| Manage model profiles | Configuration requires the full settings surface |
| Review code diffs | Diffs need a large screen. Mobile links to the web UI for this |

The boundary is clear: mobile is for reacting, not creating. If the action requires more than a text input and a button tap, it belongs on the web UI or TUI.

## Chat on Mobile

### Design Philosophy

Mobile chat is lightweight by design. It covers the most common mobile chat use case: a quick question or instruction to an agent. It does NOT attempt to replicate the full chat experience from the web UI.

### Session Access

All sessions the operator participates in are accessible: the org session (General/Frank), project sessions, and task sync sessions. The session list is grouped by scope, matching the web UI sidebar structure. Per-node async sessions (agent work logs) are not listed — they are not human-facing chat.

### Conversation View

- Messages render as a standard mobile chat thread (similar to iMessage or Signal).
- Agent messages stream in real-time. A typing indicator shows while the agent is working.
- Tool call activity is collapsed to a single "Agent is working..." indicator. The operator does not see individual tool calls on mobile. If they need that detail, they use the web UI.
- Rich content blocks in agent messages are simplified:
  - **Text**: rendered as markdown.
  - **Code**: rendered as monospace text blocks. No syntax highlighting (screen too small to be useful).
  - **Images**: thumbnail with tap-to-view.
  - **File references**: shown as a link with filename. Tap opens in system browser.
  - **Artifacts**: download link with file icon and size.

### Input

- Standard text input with keyboard.
- @mention autocomplete: typing `@` shows a list of agents in the current session.
- No file attachments. No voice input (future consideration).
- Send button. No keyboard shortcuts.

### What Happens to Web-Only Features

| Web Feature | Mobile Behavior |
|---|---|
| Scope pill | Not present. The operator switches sessions via the session list. |
| Steer button | Not present. Messages queue normally. |
| Edit queued messages | Not present. Messages cannot be edited once sent. |
| Cancel turn | Available via a simple "Stop" button visible during agent turns. |
| Reactions | Not present. |
| File upload | Not present. |
| Viewing context hint | Not sent. The agent does not know what the operator was looking at in the main content (there is no main content panel on mobile). |

## Authentication

### Biometric Authentication

The mobile app uses biometric authentication (Face ID on iOS, fingerprint on Android) as the primary unlock mechanism. This supports the 30-second interaction pattern — the operator should not have to type a password every time they respond to a notification.

**Flow:**
1. First login: operator enters email and password (same credentials as web UI). The app creates or resumes an auth session (doc 04).
2. The app stores the session token securely in the platform keychain (iOS Keychain / Android Keystore).
3. On subsequent opens: biometric prompt → keychain access → session token used for API requests.
4. If biometric fails 3 times: fall back to password entry.
5. If the session expires (30-day sliding window, per doc 04): full re-authentication with email and password.

### Sensitive Action Confirmation

Certain actions from mobile require biometric re-confirmation even if the app is already unlocked:

- **Capability approvals**: granting an agent a new capability is a security-sensitive action.
- **Approve with constraints**: modifying the scope of an approval.
- Anything configured as requiring re-confirmation in org settings (extensible).

Standard actions (approve a task review, defer an inbox item, send a chat message) do NOT require re-confirmation. The initial biometric unlock is sufficient.

### Session Management

- The mobile app uses the same auth session as other clients (doc 04). Creating a mobile session does not invalidate web or TUI sessions.
- Session lifetime follows the standard 30-day sliding window. Active use of the mobile app extends the session.
- Explicit logout from the app revokes the session token.
- If the operator changes their password from another client, all sessions (including mobile) are revoked. The app prompts for re-authentication on next use.

## Deep Links

Every push notification, every in-app notification, and every navigable item in the app is addressable by a deep link. Deep links are the connective tissue between push notifications and the app's screens.

### URL Scheme

Deep links use a custom URL scheme registered by the app:

```
ottercamp://inbox/{inbox_item_id}
ottercamp://task/{project_slug}/{task_number}
ottercamp://project/{project_slug}
ottercamp://chat/{session_id}
ottercamp://notifications
ottercamp://dashboard
```

### Push Notification Deep Links

Every push notification payload includes a deep link. When the operator taps the notification:

1. The app opens (or foregrounds if already open).
2. The deep link is resolved.
3. The app navigates directly to the target screen.
4. No intermediate screens, no loading spinners (data is prefetched or loaded in-place with a skeleton).

### Universal Links (Web Fallback)

Deep links also work as HTTPS URLs (`https://app.ottercamp.dev/inbox/{id}`) so they can be shared in chat, email, or other contexts. If the app is installed, the OS intercepts and opens the app. If not, the URL falls through to the mobile-responsive web UI.

## Offline Support

The mobile app has limited offline capability. It is designed for connected use — actions require the server — but it caches enough state to be useful when connectivity is intermittent.

### What Works Offline

- **Read-only dashboard**: the most recent dashboard state is cached locally. The operator can see the last-known project health, inbox count, and task statuses. A "Last updated X minutes ago" indicator is visible.
- **Notification history**: recently received notifications are cached and viewable offline.
- **Recent chat history**: the last N messages in recently accessed sessions are cached locally.

### What Requires Connectivity

- **All actions**: approve, reject, defer, send chat message, unblock. These are server mutations and cannot be performed offline.
- **Real-time updates**: streaming chat responses, live notification delivery, dashboard refresh.
- **Navigation to items not in cache**: tapping a notification for an item not yet cached shows a "Connecting..." state.

### Queued Actions (Future Consideration)

In a future iteration, the app could queue actions (approve, send message) taken while offline and submit them when connectivity returns. For the initial release, this is out of scope — the app shows a clear "No connection" indicator and disables action buttons when offline. Simplicity over cleverness.

## Platform and Technology

### React Native

The mobile app is built with React Native, targeting iOS and Android simultaneously from a single codebase. This decision is driven by:

- **Code sharing**: the web UI (doc 18) uses React and TypeScript. React Native allows sharing types, API client code, state management patterns, and potentially some UI component logic. Not pixel-perfect component sharing (mobile and web have different UX patterns), but shared business logic and data layer.
- **Simultaneous platform support**: building native apps for both iOS and Android in parallel with a small team. OtterCamp is a single-operator product — the operator's platform preference should not determine whether they get a mobile app.
- **Ecosystem maturity**: React Native has mature libraries for push notifications (react-native-push-notification), biometric auth (react-native-biometrics), secure storage (react-native-keychain), and deep linking.

### Native Modules

Certain features require platform-native code beyond what React Native provides out of the box:

- **Push notifications**: APNs (iOS) and FCM (Android) integration for reliable background delivery.
- **Biometric auth**: Face ID, Touch ID, fingerprint sensor access via platform keychain APIs.
- **Secure storage**: keychain (iOS) and keystore (Android) for session token storage.
- **Deep linking**: universal links (iOS) and app links (Android) for seamless URL-to-app navigation.

These are handled via established React Native native module libraries, not custom native code.

### API Integration

The mobile app consumes the same REST API and realtime endpoints as the web UI (doc 12). No mobile-specific API is needed. The API already supports:

- Session-based auth with Bearer token (used by the mobile app).
- WebSocket/SSE for realtime updates (chat streaming, notification delivery).
- Standard CRUD endpoints for inbox items, tasks, projects, sessions.

The mobile app may benefit from a lightweight aggregation endpoint (a "mobile dashboard" endpoint that returns inbox count + project summaries + recent notifications in a single request) to reduce the number of API calls on app open. This is an optimization, not a requirement — the app can compose the dashboard from existing endpoints.

## Data Synchronization

### Realtime

When the app is in the foreground, it maintains a WebSocket connection to the server for:
- Chat message streaming (agent responses in real-time).
- Notification delivery (new notifications appear immediately).
- Inbox updates (items added or acted on from another client).
- Dashboard state changes (task status transitions).

### Background

When the app is backgrounded:
- The WebSocket connection is closed (mobile OS will kill it anyway).
- Push notifications handle urgent event delivery.
- On foregrounding, the app reconnects and syncs state. The API supports delta queries ("what changed since timestamp X") for efficient catch-up.

### Conflict Resolution

The operator may act on an inbox item from the web UI while the mobile app is showing it. When the mobile app receives a state update (via WebSocket or on-foreground sync) for an item that has been acted on:
- The item's UI updates to reflect the new state (e.g., "Approved" badge replaces action buttons).
- If the operator was mid-action on mobile (typing a rejection reason), the app warns: "This item was already acted on from another device."

## Accessibility

- Dynamic type / font scaling support on both platforms.
- VoiceOver (iOS) and TalkBack (Android) compatibility for all interactive elements.
- Sufficient color contrast for urgency indicators (not relying on color alone — icons and labels supplement color).
- Haptic feedback on destructive actions (reject, deny) as a confirmation signal.

## Resolved Decisions

- **React Native for iOS and Android simultaneously.** Code sharing with the web UI's React/TypeScript codebase. Shared types, API clients, and business logic — not shared UI components. Simultaneous platform support from a single codebase.
- **Phase 4 deliverable.** The mobile app is a post-launch priority. The mobile-responsive web UI provides basic mobile access until the native app ships. This answers the open question "Is this a post-launch priority, or does it ship with V2?"
- **Push notifications are the primary entry point.** The app is not designed for browsing or exploring. A notification arrives, the operator taps it, acts, and leaves. The notification-to-action path must be fast and direct.
- **Chat is lightweight.** Quick text sessions, no file uploads, no complex @mention chains, no steer/edit, no reactions. Enough for status checks and simple instructions to agents. Extended conversations belong on the web UI or TUI.
- **No task creation, flow design, or configuration from mobile.** These require the full TUI or web UI. Mobile is for reacting and monitoring, not creating.
- **Deep links from every push notification.** Tap → land on the right screen. No navigation required. Universal links provide web fallback.
- **Biometric auth for quick access.** Face ID / Touch ID / fingerprint for fast unlock. Session token stored in platform keychain. Sensitive actions (capability approvals) require biometric re-confirmation.
- **Offline is read-only cache.** Recent dashboard state, notifications, and chat history cached locally. All actions require connectivity. No offline action queue in the initial release.
- **Critical and High urgency events push. Medium and Low do not (by default).** User-configurable per urgency tier, per project, and per event type. Quiet hours suppress non-critical pushes.
- **No diff review on mobile.** Code diffs need a large screen. Mobile inbox items for work reviews link to the web UI for the full diff. The operator can still approve/reject from mobile based on the task description and agent reasoning.
- **Rich notifications for quick actions.** iOS and Android support inline action buttons on push notifications (Approve, Open Chat). The operator can act from the lock screen without opening the app.
- **Single shared API.** The mobile app consumes the same REST API and realtime endpoints as the web UI. No mobile-specific backend. A mobile dashboard aggregation endpoint is an optional optimization.
- **Tool call activity hidden on mobile.** Agent tool calls are collapsed to a "working..." indicator. The streaming tool call list is a web UI feature. Mobile prioritizes simplicity over transparency for in-flight work.

## Open Questions

- **Tablet optimization**: should the app have a tablet-specific layout (closer to the web UI's three-panel design) or use the phone layout on all mobile devices? iPad and Android tablets could benefit from a split view.
- **Voice input**: should the chat input support voice-to-text for faster mobile messaging? Platform speech-to-text APIs make this straightforward, but it adds complexity to the input UX.
- **Widget support**: iOS widgets and Android widgets could show inbox count and project health on the home screen without opening the app. Worth the maintenance cost?
- **Watch companion**: an Apple Watch / Wear OS app could show push notifications with quick-approve actions. Extremely lightweight but extends the "act without pulling out your phone" pattern.
