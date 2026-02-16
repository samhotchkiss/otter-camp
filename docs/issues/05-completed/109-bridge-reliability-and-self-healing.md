# 109 — Bridge Reliability: Multi-Layer Redundancy and Self-Healing

**Priority:** P1 — Critical Infrastructure
**Status:** Ready

## Problem

The OtterCamp ↔ OpenClaw bridge (`bridge/openclaw-bridge.ts`) goes down silently. When it dies, there's no detection, no auto-recovery, and no alerting. This is the critical link between agents and the Otter Camp UI — if it's down, the whole system is dark.

**Today's failure (Feb 8):** A stale WebSocket connection that never reconnected. Had to manually find and kill the process. Supervisor restarted it, but there was an unknown-duration outage with zero visibility.

## Root Causes

1. No auto-reconnect on WebSocket close/error events
2. No heartbeat/ping to detect dead sockets (half-open connections)
3. No health endpoint to monitor from outside
4. No structured logging of connection state transitions
5. No message queue for durability during disconnections

## Requirements

**The bridge must recover from any single failure within 15 seconds with zero message loss.**

### Layer 1: Self-Healing WebSocket Connections

The bridge maintains two WS connections (OpenClaw gateway + OtterCamp API). Both must be resilient.

- **Auto-reconnect** on `close`, `error`, and detected silence
- **Exponential backoff with jitter**: 1s → 2s → 4s → 8s → 16s → 30s max
- **Reset backoff** on successful connection + first message received
- **Heartbeat ping/pong** on both connections: send ping every 10s, expect pong within 5s
- If pong missed 2x consecutively → force-close and reconnect
- If reconnect fails 5x consecutively → full process exit (let supervisor handle)
- **Connection state machine**: `connecting → connected → degraded → disconnected → reconnecting`
- Log every state transition with timestamp

### Layer 2: Supervisor / Process Management

- **launchd plist** on macOS (systemd unit on Linux) to keep bridge alive
- Restart on crash with rate limiting (max 5 restarts per 60s, then back off)
- **HTTP health endpoint** (`/health` on configurable port) returning:
  ```json
  {
    "status": "healthy|degraded|unhealthy",
    "openclaw": { "connected": true, "lastMessage": "2026-02-08T...", "reconnects": 0 },
    "ottercamp": { "connected": true, "lastMessage": "2026-02-08T...", "reconnects": 0 },
    "uptime": 3600,
    "queueDepth": 0
  }
  ```
- Supervisor polls `/health` every 10s; restarts process if unhealthy for 30s+

### Layer 3: External Monitoring (OpenClaw Cron)

- **Cron job every 60s** hits bridge `/health` endpoint
- If bridge reports unhealthy or is unreachable:
  1. Attempt to restart bridge process
  2. If restart fails after 2 attempts, alert Sam via Slack DM
- Track and log uptime metrics (rolling 24h availability %)
- Distinguish between "bridge process down" vs "bridge up but WS disconnected"

### Layer 4: Server-Side Detection (OtterCamp API)

- API tracks `last_sync_at` timestamp per connected OpenClaw instance
- If no sync received within 30s, mark instance as `disconnected`
- **UI indicator**: green (connected, <10s), yellow (>10s since last sync), red (>30s / disconnected)
- Show indicator in the sidebar or header — always visible
- Optional webhook: OtterCamp can POST to a configured URL when connection drops (future)

### Layer 5: Message Durability

- **Outbound message queue** (in-memory with optional disk persistence)
- When either WS is disconnected, queue messages instead of dropping
- On reconnect, replay queued messages in order (dedup by message ID)
- Queue max size: 1000 messages or 10MB, whichever first (drop oldest if exceeded)
- **UI banner**: "Messages may be delayed — bridge reconnecting" when OtterCamp detects disconnection

## Out of Scope

- Multi-bridge failover (single bridge instance is fine for now)
- Encrypted message queue (bridge runs locally, trust the host)
- Custom alerting channels beyond Slack DM

## Implementation Notes

- Bridge is TypeScript (`bridge/openclaw-bridge.ts`)
- Health endpoint can use Node's built-in `http.createServer` (no framework needed)
- launchd plist should live in `bridge/com.ottercamp.bridge.plist` with install instructions
- Cron monitor can be an OpenClaw cron job (isolated session, 60s interval)
- Server-side tracking needs a new column on the `openclaw_connections` table (or equivalent)

## Test Plan

- **Unit tests**: reconnect logic, backoff calculation, queue enqueue/dequeue/dedup, health endpoint response
- **Integration tests**: simulate WS close → verify reconnect within backoff window; simulate both WS down → verify health reports unhealthy; simulate reconnect → verify queued messages replayed in order
- **Manual verification**: kill bridge process → confirm supervisor restarts within 5s; kill OtterCamp WS → confirm bridge reconnects and UI shows yellow→green

## Acceptance Criteria

- [ ] Bridge auto-reconnects on WS close/error with exponential backoff
- [ ] Heartbeat ping/pong detects dead sockets within 15s
- [ ] `/health` endpoint returns accurate connection state
- [ ] launchd plist keeps bridge alive on crash
- [ ] OpenClaw cron monitor detects outage within 60s and alerts
- [ ] OtterCamp UI shows live connection indicator (green/yellow/red)
- [ ] Messages queued during disconnection are replayed on reconnect
- [ ] Zero message loss during disconnections under 60s
- [ ] All connection state transitions are logged with timestamps

## Execution Log

- [2026-02-08 23:13 MST] Issue #109 | Commit n/a | in_progress | Moved spec from 01-ready to 02-in-progress and started branch codex/spec-109-bridge-reliability-self-healing | Tests: n/a
- [2026-02-08 23:15 MST] Issue #457 | Commit n/a | created | Added bridge socket state machine + reconnect/backoff policy micro-issue with explicit vitest command | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts
- [2026-02-08 23:15 MST] Issue #458 | Commit n/a | created | Added heartbeat ping/pong dead-socket detection micro-issue with explicit vitest command | Tests: npx vitest run bridge/__tests__/openclaw-bridge.heartbeat.test.ts
- [2026-02-08 23:15 MST] Issue #459 | Commit n/a | created | Added bridge /health endpoint diagnostics micro-issue with explicit vitest command | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts
- [2026-02-08 23:15 MST] Issue #460 | Commit n/a | created | Added bridge outbound dispatch durability queue micro-issue with explicit vitest command | Tests: npx vitest run bridge/__tests__/openclaw-bridge.dispatch-durability.test.ts
- [2026-02-08 23:15 MST] Issue #461 | Commit n/a | created | Added launchd + external monitor assets micro-issue with explicit tests | Tests: npx vitest run bridge/__tests__/bridge-monitor.test.ts; bash -n bridge/bridge-monitor.sh
- [2026-02-08 23:15 MST] Issue #462 | Commit n/a | created | Added API bridge freshness threshold/status micro-issue with explicit go test commands | Tests: go test ./internal/api -run TestAdminConnectionsHandlerBridgeHealthChecks -count=1; go test ./internal/api -run TestOpenClawSyncHandlerHealthEndpoint -count=1
- [2026-02-08 23:15 MST] Issue #463 | Commit n/a | created | Added global UI bridge status indicator + delayed-message banner micro-issue with explicit vitest command | Tests: cd web && npx vitest run src/layouts/DashboardLayout.test.tsx
- [2026-02-08 23:19 MST] Issue #457 | Commit 192559b | closed | Added bridge connection transition helpers, jittered reconnect delay policy, and reconnect scheduling integration for OpenClaw/OtterCamp sockets | Tests: npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts; npx vitest run bridge/__tests__/openclaw-bridge.activity-buffer.test.ts bridge/__tests__/openclaw-bridge.activity-delta.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts bridge/__tests__/openclaw-bridge.connection-state.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts
- [2026-02-08 23:22 MST] Issue #458 | Commit 48928cc | closed | Added heartbeat policy helpers and ping/pong loops for OpenClaw/OtterCamp sockets with degraded/disconnected transitions on missed pongs | Tests: npx vitest run bridge/__tests__/openclaw-bridge.heartbeat.test.ts; npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.heartbeat.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts bridge/__tests__/openclaw-bridge.activity-delta.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts
- [2026-02-08 23:24 MST] Issue #459 | Commit acb846c | closed | Added runtime bridge /health endpoint with per-socket connection diagnostics, uptime, queueDepth, and payload status classification | Tests: npx vitest run bridge/__tests__/openclaw-bridge.health-endpoint.test.ts; npx vitest run bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.heartbeat.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts bridge/__tests__/openclaw-bridge.activity-delta.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts
- [2026-02-08 23:30 MST] Issue #460 | Commit 8acd0b6 | closed | Added bounded in-memory dispatch replay queue with dedupe, overflow controls, reconnect replay flush, and durability tests for enqueue/eviction/FIFO replay | Tests: npx vitest run bridge/__tests__/openclaw-bridge.dispatch-durability.test.ts; npx vitest run bridge/__tests__/openclaw-bridge.dispatch-durability.test.ts bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.heartbeat.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts bridge/__tests__/openclaw-bridge.activity-delta.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts
- [2026-02-08 23:34 MST] Issue #461 | Commit e88bc09 | closed | Added launchd bridge plist, cron monitor script with restart/escalation hooks, and bridge monitor setup docs with scripted behavior tests | Tests: npx vitest run bridge/__tests__/bridge-monitor.test.ts; bash -n bridge/bridge-monitor.sh; npx vitest run bridge/__tests__/bridge-monitor.test.ts bridge/__tests__/openclaw-bridge.dispatch-durability.test.ts bridge/__tests__/openclaw-bridge.connection-state.test.ts bridge/__tests__/openclaw-bridge.heartbeat.test.ts bridge/__tests__/openclaw-bridge.health-endpoint.test.ts bridge/__tests__/openclaw-bridge.activity-buffer.test.ts bridge/__tests__/openclaw-bridge.activity-delta.test.ts bridge/__tests__/openclaw-bridge.admin-command.test.ts; npx tsx --test bridge/openclaw-bridge.test.ts
- [2026-02-08 23:39 MST] Issue #462 | Commit 52e0275 | closed | Added central bridge freshness helper (<10s healthy, 10-30s degraded, >30s unhealthy), exposed status/age metadata in admin and sync APIs, and added boundary health tests | Tests: go test ./internal/api -run TestAdminConnectionsHandlerBridgeHealthChecks -count=1; go test ./internal/api -run TestOpenClawSyncHandlerHealthEndpoint -count=1; go test ./internal/api -run 'TestAdminConnections|TestOpenClawSync' -count=1
- [2026-02-08 23:43 MST] Issue #463 | Commit fed1773 | closed | Added global dashboard bridge health indicator (healthy/degraded/unhealthy), delayed-message reconnect banner, and layout tests for bridge status mapping/transitions | Tests: cd web && npx vitest run src/layouts/DashboardLayout.test.tsx; cd web && npx vitest run src/layouts/DashboardLayout.test.tsx src/pages/ConnectionsPage.test.tsx
- [2026-02-08 23:43 MST] Issue #109 | Commit n/a | ready_for_review | Completed planned micro-issues #457-#463 for bridge reliability layers and prepared spec handoff to 03-needs-review | Tests: n/a
- [2026-02-08 23:43 MST] Issue #109 | Commit n/a | opened_pr | Opened PR #464 (codex/spec-109-bridge-reliability-self-healing -> main) for reviewer validation | Tests: n/a
- [2026-02-08 23:43 MST] Issue #109 | Commit n/a | moved_to_needs_review | Moved spec from 02-in-progress to 03-needs-review after completing issues #457-#463 | Tests: n/a
