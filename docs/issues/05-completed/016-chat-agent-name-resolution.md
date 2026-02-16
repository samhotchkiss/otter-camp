# Issue #016 — Chat Agent Name Resolution

## Problem
In the Global Chat panel, agent messages show the OpenClaw slot name (e.g., "avatar-design") instead of the display name (e.g., "Jeff G"). The sender label shows "avatar-design Agent" instead of "Jeff G".

## Root Cause
Messages store `senderName` at creation time. The agent name directory (`/api/sync/agents`) loads asynchronously. Messages that arrive (or are loaded from history) before the directory is populated retain the raw slot name.

## Fix Required

### 1. Resolve names at render time, not storage time
In `web/src/components/messaging/MessageHistory.tsx`, the `message.senderName` should be resolved against the agent directory at render time:

```tsx
// Instead of displaying message.senderName directly, look up the display name
const displayName = agentNamesByID?.get(message.agentId) || message.senderName;
```

### 2. Pass agent directory to MessageHistory
The `agentNamesByID` map from `GlobalChatContext` needs to be accessible in `MessageHistory.tsx`. Either:
- Pass it as a prop
- Use the context directly in MessageHistory
- Add a `useAgentName(id)` hook

### 3. Update conversation sidebar labels
The conversation list in `GlobalChatSurface.tsx` also shows raw slot names. These should also resolve against the directory.

### 4. Fix message avatar initials
The `MessageAvatar` component uses `name` to generate initials. "avatar-design" → "A" but "Jeff G" → "JG". The resolved name should be used.

## Files to Change
- `web/src/components/messaging/MessageHistory.tsx` — resolve senderName at render
- `web/src/components/chat/GlobalChatSurface.tsx` — resolve conversation labels
- `web/src/components/messaging/MessageAvatar.tsx` — use resolved name for initials
- `web/src/contexts/GlobalChatContext.tsx` — expose agentNamesByID via context or hook

## Test
1. Open Global Chat
2. Click on a DM conversation with any agent
3. Agent messages should show display name ("Jeff G"), not slot name ("avatar-design")
4. Conversation sidebar should show "Jeff G", not "avatar-design"
5. Avatar initials should be "JG", not "A"

## Execution Log

- [2026-02-08 22:22 MST] Issue #016 | Commit n/a | in_progress | Moved spec from 01-ready to 02-in-progress and started execution on branch codex/spec-016-chat-agent-name-resolution | Tests: n/a
- [2026-02-08 22:25 MST] Issue #440 | Commit n/a | created | Created Spec016 Phase1 context resolver micro-issue with explicit provider tests | Tests: cd web && npx vitest run src/contexts/GlobalChatContext.test.tsx
- [2026-02-08 22:25 MST] Issue #441 | Commit n/a | created | Created Spec016 Phase2 MessageHistory render-time resolution micro-issue with sender label/initial tests | Tests: cd web && npx vitest run src/components/messaging/__tests__/MessageHistory.test.tsx
- [2026-02-08 22:25 MST] Issue #442 | Commit n/a | created | Created Spec016 Phase3 GlobalChatDock render-time label resolution micro-issue with initials/title tests | Tests: cd web && npx vitest run src/components/chat/GlobalChatDock.test.tsx
- [2026-02-08 22:25 MST] Issue #443 | Commit n/a | created | Created Spec016 Phase4 MessageAvatar initials micro-issue with avatar/task-thread tests | Tests: cd web && npx vitest run src/components/messaging/__tests__/MessageAvatar.test.tsx src/components/messaging/__tests__/TaskThreadView.test.tsx
- [2026-02-08 22:28 MST] Issue #440 | Commit cf11525 | closed | Added GlobalChatContext agent resolver helpers, exposed resolveAgentName/agentNamesByID, and applied DM parse/reconcile resolution; pushed branch and closed issue | Tests: cd web && npx vitest run src/contexts/GlobalChatContext.test.tsx; cd web && npx vitest run src/components/chat/GlobalChatDock.test.tsx
- [2026-02-08 22:30 MST] Issue #441 | Commit 1cd1ead | closed | Implemented render-time agent sender name/initial resolution in MessageHistory and threaded resolver props through GlobalChatSurface; pushed branch and closed issue | Tests: cd web && npx vitest run src/components/messaging/__tests__/MessageHistory.test.tsx; cd web && npx vitest run src/components/chat/GlobalChatSurface.test.tsx
- [2026-02-08 22:32 MST] Issue #442 | Commit cae598c | closed | Applied render-time DM title/initial resolution in GlobalChatDock and passed resolver metadata into GlobalChatSurface; pushed branch and closed issue | Tests: cd web && npx vitest run src/components/chat/GlobalChatDock.test.tsx; cd web && npx vitest run src/components/chat/GlobalChatSurface.test.tsx src/components/messaging/__tests__/MessageHistory.test.tsx
- [2026-02-08 22:33 MST] Issue #443 | Commit 4f4490a | closed | Switched MessageAvatar agent fallback to initials and added MessageAvatar/TaskThreadView tests with broader chat regression run; pushed branch and closed issue | Tests: cd web && npx vitest run src/components/messaging/__tests__/MessageAvatar.test.tsx src/components/messaging/__tests__/TaskThreadView.test.tsx; cd web && npx vitest run src/contexts/GlobalChatContext.test.tsx src/components/chat/GlobalChatDock.test.tsx src/components/chat/GlobalChatSurface.test.tsx src/components/messaging/__tests__/MessageHistory.test.tsx src/components/messaging/__tests__/TaskThread.test.tsx src/components/messaging/__tests__/MessageAvatar.test.tsx src/components/messaging/__tests__/TaskThreadView.test.tsx
