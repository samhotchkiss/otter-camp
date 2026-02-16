# Issue #12: Chat Panel Shows Raw UUIDs and Internal Slot Names

## Problem

The Global Chat panel (opened via the "Chats" button in the bottom-right) has multiple display bugs:

### 12.1 Project Chat Shows Truncated UUID Instead of Name

One of the chat channels displays as:

```
Project 944adcaf
PROJECT · Project
Project chat
```

Instead of the actual project name. The system is using a truncated project UUID as the display name. The avatar shows "P9" (from the UUID characters).

When selected, the chat header shows "Project 944adcaf" and the input placeholder says "Message Project 944adcaf...". The "Start a conversation with Project 944adcaf" prompt is also wrong.

Additionally, this project chat shows a **red "not found" error** at the bottom of the chat area, suggesting the project or its chat endpoint doesn't exist or can't be resolved.

### 12.2 Agent DM Shows Internal Slot Name Instead of Display Name

The agent DM channel shows:

```
avatar-design
DM · Direct message
Agent chat
```

Instead of showing the agent's display name "Jeff G". The chat header says "avatar-design" and the input says "Message avatar-design...".

This is because the system uses the OpenClaw agent slot name (e.g., `avatar-design`, `2b`, `three-stones`) instead of resolving to the agent's configured display name (Jeff G, Derek, Stone).

### 12.3 Otter Camp Project Chat Works Correctly

The "Otter Camp" project chat correctly shows the project name, proper header, and working chat interface — so the resolution logic works for some projects but not all.

## Acceptance Criteria

- [ ] All project chats show the full project name (not truncated UUID)
- [ ] All agent DM chats show the agent's display name (not the internal slot name)
- [ ] Chat header, input placeholder, and "Start a conversation with..." prompt all use resolved display names
- [ ] Avatar initials in the chat list use the display name (e.g., "JG" for Jeff G, not "av" for avatar-design)
- [ ] If a project chat can't be resolved (orphaned/deleted project), show a clean error state instead of "Project {uuid}" with a "not found" error
- [ ] Clean up or remove orphaned chat channels that reference non-existent projects

## Files to Investigate

- `web/src/components/chat/GlobalChat.tsx` or `web/src/components/chat/ChatPanel.tsx` — Chat panel component
- `web/src/components/chat/ChatList.tsx` — Chat channel list rendering
- `web/src/components/chat/ChatHeader.tsx` — Header display
- `internal/api/chat.go` or `internal/api/messages.go` — Chat/session API that returns channel info
- `internal/store/chat_store.go` — How chat sessions store project/agent references
- Agent name resolution — wherever slot names get mapped to display names (check agent sync data)

## Test Plan

```bash
# Backend
go test ./internal/api -run TestChatSessionsResolveProjectName -count=1
go test ./internal/api -run TestChatSessionsResolveAgentDisplayName -count=1

# Frontend
cd web && npm test -- --grep "ChatPanel"
cd web && npm test -- --grep "chat channel name"
```

## Execution Log
- [2026-02-08 15:26 MST] Issue #368/#369/#370 | Commit 53ce2d5,0f72858,3f43847 | reconciled | Verified existing Spec012 micro-issues were already implemented and closed in GitHub history | Tests: n/a
- [2026-02-08 15:26 MST] Issue spec #012 | Commit e332a4d | reconciled | Confirmed merged implementation PR #371 and validated targeted chat regressions locally | Tests: cd web && npm test -- src/contexts/GlobalChatContext.test.tsx src/components/chat/GlobalChatSurface.test.tsx src/components/chat/GlobalChatDock.test.tsx --run
- [2026-02-08 20:00 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Reconciled local queue state: spec already implemented/reconciled in execution log; moved from `01-ready` to `03-needs-review` for review tracking | Tests: n/a
- [2026-02-08 21:01 MST] Issue spec local-state | Commit n/a | reconciled-folder-state | Re-ran preflight reconciliation and moved spec from `01-ready` to `03-needs-review` to match closed GitHub issues (#368/#369/#370) and existing implementation commits. | Tests: n/a
- [2026-02-08 21:14 MST] Issue spec #012 | Commit n/a | APPROVED | Josh S (Opus) review: Implementation verified on main. `GlobalChatContext.tsx` resolves project UUIDs→names via `normalizeProjectDirectory` + `looksLikeProjectIdentifierTitle`, and agent slots→display names via `looksLikeAgentSlotName` + agent directory lookup. Unresolvable projects fall back to "Project chat" (clean). Tests: 2/2 pass (`GlobalChatContext.test.tsx`). No isolated branch (work merged via PR #371 before review process). Acceptance criteria met. | Tests: `cd web && npx vitest run src/contexts/GlobalChatContext.test.tsx` — 2 passed
- [2026-02-08 21:06 MST] Issue spec local-state | Commit n/a | moved-to-completed | Reconciled local folder state after verification that associated GitHub work is merged/closed and implementation commits are present on main. | Tests: n/a
