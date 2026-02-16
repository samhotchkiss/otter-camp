# Issue #14: Connections Page Shows Disconnected/Stale/Empty Data

## Problem

The Connections & Diagnostics page (`/connections`) shows degraded or missing data across every section:

### 14.1 Bridge Status Cards

| Card | Shows | Expected |
|------|-------|----------|
| Bridge | "Disconnected", "Sync health: Degraded" | "Connected" with recent sync time |
| Host | "Unknown host", "OS: Unknown", "Gateway: Unknown · Port ?" | Mac Studio, macOS 26.2, Gateway port 18791 |
| GitHub Sync | "Stuck jobs: N/A", "Dead letters: N/A" | Actual sync stats or "Not configured" |

The bridge was last synced at 1:28 PM but shows as disconnected. Either the sync stopped or the UI doesn't poll/update.

### 14.2 Session Summary Contradictions

```
Total: 12    Online: 12    Busy: 0    Offline: 0    Stalled: 12
Memory used: Unknown / Unknown
```

- Shows **12 Online AND 12 Stalled** simultaneously — contradictory
- "Memory used: Unknown / Unknown" — not resolving memory data

### 14.3 Agent Sessions Table

All 12 agents show status "stalled" with:
- Some "Last Seen" dates are days old (Feb 5, 6, 7) but some are same-day with UTC timestamps
- Last Seen timestamps are in raw ISO format (`2026-02-08T20:25:19Z`) — should be relative ("3 hours ago") or localized
- "Last Activity" is "No recent activity" for all agents
- Several agents show Channel "—" (Beau H, Ivy, Nova, Jeremy H) — these should show their configured channel

### 14.4 Gateway Logs Empty

"No logs available." — The gateway is running and producing logs, but this section can't access them. Either the API endpoint isn't implemented or the bridge doesn't forward log data.

### 14.5 Cron Jobs Empty

"No cron jobs reported." — There are multiple active cron jobs configured in OpenClaw (heartbeat, memory-extract, morning briefing, etc.), but this section shows none.

### 14.6 Active Processes Empty

"No active processes reported." — May be correct if no background processes are running, but should at minimum show recently completed processes.

## Root Cause

The Connections page depends heavily on the OpenClaw bridge sync to push data to the Otter Camp API. The issues suggest:

1. **Bridge sync is broken or incomplete** — It syncs agent session data but not host info, logs, cron jobs, or processes
2. **Sync frequency is too low** — Last sync was at 1:28 PM, data goes stale quickly
3. **Status calculation is wrong** — "Online" and "Stalled" computed differently/incorrectly
4. **No polling on the page** — The page loads once and doesn't refresh; needs periodic re-fetch or WebSocket updates

## Acceptance Criteria

- [ ] Bridge card shows "Connected" when last sync is within a reasonable window (e.g., last 5 minutes), not just "Disconnected"
- [ ] Host card shows actual host info (OS, gateway version, port) from the sync payload
- [ ] Session Summary status counts are consistent (can't be both Online and Stalled)
- [ ] Agent Sessions table shows correct status based on actual last-seen recency
- [ ] Last Seen timestamps are displayed in relative format ("3 hours ago") or local timezone (not raw UTC ISO)
- [ ] Channel column shows the agent's configured channel (not "—" for agents with channels)
- [ ] Memory used shows actual data or "N/A" (not "Unknown / Unknown")
- [ ] Cron Jobs section loads and displays configured cron jobs from the sync bridge
- [ ] Gateway Logs section loads available logs (or shows "Connect bridge to view logs" if bridge is disconnected)
- [ ] The page auto-refreshes or polls data periodically (every 30s) to stay current

## Files to Investigate

- `web/src/pages/ConnectionsPage.tsx` — Connections page component
- `web/src/components/connections/` — Sub-components for each card/section
- `internal/api/connections.go` or `internal/api/diagnostics.go` — API endpoints
- `internal/api/openclaw_sync.go` — What data the sync bridge provides
- Bridge sync endpoint — Check what fields are included in the sync payload
- Status calculation logic — How "online" vs "stalled" is determined

## Test Plan

```bash
# Backend
go test ./internal/api -run TestConnectionsEndpoint -count=1
go test ./internal/api -run TestAgentSessionStatus -count=1
go test ./internal/api -run TestSyncPayloadFields -count=1

# Frontend
cd web && npm test -- --grep "ConnectionsPage"
cd web && npm test -- --grep "bridge status"
```

## Execution Log
- [2026-02-08 15:30 MST] Issue spec #014 | Commit n/a | in-progress | Moved spec from 01-ready to 02-in-progress and switched to branch `codex/spec-014-connections-page-stale-data` from `origin/main` | Tests: n/a
- [2026-02-08 15:32 MST] Issue #390/#391/#392/#393 | Commit n/a | planned | Created full Spec014 micro-issue set (backend bridge recency, channel fallback, UI formatting, polling) with explicit tests before implementation | Tests: n/a
- [2026-02-08 15:34 MST] Issue #390 | Commit 7fdaf40 | closed | Added 5-minute bridge recency logic so admin connections marks bridge connected/healthy from recent sync even when websocket link is temporarily down | Tests: go test ./internal/api -run 'TestAdminConnectionsGet(UsesRecentLastSyncAsConnectedSignal|MarksBridgeDisconnectedWhenLastSyncIsStale|ReturnsDiagnosticsAndSessionSummary)|TestAgentStatusConsistency' -count=1
- [2026-02-08 15:36 MST] Issue #391 | Commit d0639d9 | closed | Added admin-connections channel fallback derivation from session keys (preserving explicit channels) and covered memory-backed session behavior | Tests: go test ./internal/api -run 'Test(AdminConnectionsGet(UsesRecentLastSyncAsConnectedSignal|MarksBridgeDisconnectedWhenLastSyncIsStale|UsesDerivedChannelForMemorySessions|ReturnsDiagnosticsAndSessionSummary)|DeriveSessionChannel)' -count=1
- [2026-02-08 15:38 MST] Issue #392 | Commit 46b24c7 | closed | Updated ConnectionsPage formatting: memory fallback now N/A, session last-seen values humanized, and disconnected log panel shows bridge-connect guidance | Tests: cd web && npm test -- src/pages/ConnectionsPage.test.tsx --run
- [2026-02-08 15:40 MST] Issue #393 | Commit 0d460b7 | closed | Added 30-second auto-refresh polling for Connections diagnostics with cleanup-on-unmount and timer-based regression coverage | Tests: cd web && npm test -- src/pages/ConnectionsPage.test.tsx --run
- [2026-02-08 15:40 MST] Issue spec #014 | Commit n/a | verified | Ran final Spec014 backend/frontend regression suite across admin connections recency/channel logic and UI diagnostics behavior | Tests: go test ./internal/api -run 'Test(AdminConnectionsGet(UsesRecentLastSyncAsConnectedSignal|MarksBridgeDisconnectedWhenLastSyncIsStale|UsesDerivedChannelForMemorySessions|ReturnsDiagnosticsAndSessionSummary)|DeriveSessionChannel|AgentStatusConsistency|OpenClawSyncHandlePersistsDiagnosticsMetadata)' -count=1; cd web && npm test -- src/pages/ConnectionsPage.test.tsx --run
- [2026-02-08 15:40 MST] Issue spec #014 | Commit 0d460b7 | review-ready | Opened PR #394 for reviewer visibility after completing all planned micro-issues (#390-#393) | Tests: n/a
