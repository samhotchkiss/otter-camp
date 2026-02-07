# Issue #3: Connections & Diagnostics Page

## Problem

When Sam is away from the Mac Studio, there's no way to see if things are healthy or fix problems remotely. Known recurring issues:

- **Gateway port corruption** — WebSocket state gets corrupted, needs full gateway restart (not SIGUSR1). Has happened 3+ times.
- **Browser control service dies silently** — Gateway stays up but browser subsystem fails. Agents get timeouts.
- **OpenClaw bridge disconnects** — The WebSocket connection between Mac Studio and api.otter.camp drops without notification.
- **Agent sessions stall** — Context overflow, rate limits, or aborted runs leave agents unresponsive.

Currently the only fix is to SSH into the Mac Studio or wait until Sam is physically there. Otter Camp should provide visibility and remote remediation.

---

## What to Build

### A "Connections" page in Otter Camp (accessible from Settings or sidebar)

The page shows the health of all connections and provides actions to fix common problems.

---

## Section 1: OpenClaw Bridge Status

### Display

| Field | Source | Description |
|-------|--------|-------------|
| Connection status | `OpenClawHandler.IsConnected()` | 🟢 Connected / 🔴 Disconnected |
| Last sync timestamp | `agent_sync_state` table or in-memory `memoryLastSync` | When the last full sync was received |
| Bridge uptime | Bridge process start time | How long the bridge has been running |
| Gateway version | Sync payload or `/health` | OpenClaw version on the host |
| Gateway PID | Sync payload | Process ID of the gateway |
| Host info | Sync payload | Hostname, OS, uptime, load average |

### Data Source

The bridge (`bridge/openclaw-bridge.ts`) already pushes sync payloads to `POST /api/sync/openclaw` containing session data. Extend the sync payload to include host diagnostics:

```json
{
  "type": "full",
  "timestamp": "...",
  "host": {
    "hostname": "Mac-Studio",
    "os": "Darwin 25.2.0 (arm64)",
    "uptime_seconds": 543200,
    "load_avg": [3.2, 2.8, 2.5],
    "memory_total_gb": 128,
    "memory_used_gb": 45,
    "disk_free_gb": 4600,
    "gateway_pid": 16314,
    "gateway_version": "2026.2.6-3",
    "gateway_port": 18791
  },
  "agents": [...],
  "sessions": [...]
}
```

The bridge can gather this from the host via `os` module and OpenClaw's status endpoints.

---

## Section 2: Agent Sessions Status

### Display

A table/card grid showing all agent sessions:

| Field | Description |
|-------|-------------|
| Agent name | e.g., "Frank", "Derek", "Stone" |
| Status | 🟢 Online / 🟡 Busy / 🔴 Offline / ⚠️ Stalled |
| Model | Current model (e.g., `claude-opus-4-6`) |
| Context tokens | Current token usage |
| Last active | Time since last activity |
| Channel | Current channel (slack, etc.) |
| Aborted | Whether last run was aborted |

### Stall Detection

Flag a session as "stalled" if:
- `abortedLastRun` is true
- Last activity > 30 minutes and session is supposed to be active (has heartbeat)
- Context tokens > 150k (approaching limits)

### Data Source

Already available from the sync payload — `sessions` array contains all of this data.

---

## Section 3: GitHub Sync Status

### Display

| Field | Source | Description |
|-------|--------|-------------|
| Sync health | `GET /api/github/sync/health` | Overall sync status |
| Dead letters | `GET /api/github/sync/dead-letters` | Failed sync jobs |
| Last sync per project | Project repo sync state | When each project last synced |

### Actions

- **Replay dead letter** — `POST /api/github/sync/dead-letters/{id}/replay`
- **Force sync project** — `POST /api/projects/{id}/repo/sync`

These endpoints already exist.

---

## Section 4: Remote Actions

These are the key remediation actions Sam can trigger from the browser:

### 4a. Restart Gateway

**What it does:** Sends a command through the OpenClaw bridge to restart the gateway process.

**Implementation:**
1. Frontend calls `POST /api/admin/gateway/restart`
2. Otter Camp sends a command event via the WebSocket bridge: `{ "type": "command", "data": { "action": "gateway.restart" } }`
3. The bridge receives this and executes `openclaw gateway restart` on the host
4. Bridge reports back success/failure

**UI:** Red button with confirmation dialog ("This will restart all agent sessions. Continue?")

### 4b. Restart Bridge

**What it does:** Restarts the bridge process itself.

**Implementation:** The bridge can implement a self-restart by re-exec'ing itself. Or: a systemd/launchd service restarts it.

**UI:** Button with status indicator showing bridge reconnection.

### 4c. Ping Agent

**What it does:** Sends a wake event to a specific agent to check if it responds.

**Implementation:**
1. Frontend calls `POST /api/admin/agents/{id}/ping`
2. Bridge sends a wake/heartbeat to that agent via OpenClaw
3. Waits for response (with timeout)
4. Reports back: responsive / unresponsive

**UI:** Per-agent "Ping" button in the sessions table, with response time or timeout indicator.

### 4d. Reset Agent Session

**What it does:** Forces a session reset for a stuck agent.

**Implementation:**
1. Frontend calls `POST /api/admin/agents/{id}/reset`
2. Bridge sends session reset command via OpenClaw API
3. Agent gets a fresh session

**UI:** Per-agent "Reset" button (with confirmation).

### 4e. View Gateway Logs

**What it does:** Streams recent gateway logs to the browser.

**Implementation:**
1. Bridge reads last N lines from the OpenClaw log file
2. Pushes via WebSocket or returns via API
3. Frontend shows scrollable log viewer

**UI:** Expandable log panel at the bottom of the page.

---

## Section 5: Connection History / Event Log

A timeline showing connection events:
- Bridge connected/disconnected
- Gateway restarts
- Agent session resets
- Sync failures
- Dead letter events

Store in a `connection_events` table:

```sql
CREATE TABLE connection_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    event_type TEXT NOT NULL,  -- bridge.connected, bridge.disconnected, gateway.restart, agent.stalled, sync.failed
    severity TEXT NOT NULL DEFAULT 'info',  -- info, warning, error
    message TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_connection_events_org_created ON connection_events(org_id, created_at DESC);
```

---

## Files to Create/Modify

### Backend

- **New: `internal/api/admin_connections.go`** — Handlers for:
  - `GET /api/admin/connections` — Aggregated connection status (bridge, agents, GitHub sync)
  - `POST /api/admin/gateway/restart` — Send restart command via bridge
  - `POST /api/admin/agents/{id}/ping` — Ping specific agent
  - `POST /api/admin/agents/{id}/reset` — Reset agent session
  - `GET /api/admin/logs` — Recent gateway logs
  - `GET /api/admin/events` — Connection event history
- **New: `internal/store/connection_event_store.go`** — Store for connection events
- **New migration: `migrations/039_create_connection_events.up.sql`**
- **Modify: `internal/api/router.go`** — Register admin routes
- **Modify: `internal/api/openclaw_sync.go`** — Extend sync payload to include host diagnostics
- **Modify: `internal/ws/openclaw_handler.go`** — Handle command events (gateway.restart, agent.ping, agent.reset) and forward to bridge

### Bridge

- **Modify: `bridge/openclaw-bridge.ts`** — 
  - Collect host diagnostics (hostname, uptime, load, memory, disk) and include in sync payload
  - Handle inbound command events from Otter Camp (restart gateway, ping agent, reset session)
  - Execute local commands and report results back

### Frontend

- **New: `web/src/pages/ConnectionsPage.tsx`** — Main connections/diagnostics page
- **Modify: `web/src/router.tsx`** — Add `/connections` route
- **Modify: `web/src/layouts/DashboardLayout.tsx`** — Add "Connections" to sidebar nav

---

## Security Considerations

- Admin endpoints should require authentication (existing auth middleware)
- Gateway restart and agent reset are destructive — require confirmation in UI
- Bridge command execution should be limited to a whitelist of safe actions (no arbitrary command execution)
- Log viewer should redact sensitive data (tokens, passwords)

---

## Priority Order

1. **Bridge status + agent sessions display** — Most useful immediately, data already available from sync
2. **Gateway restart action** — Most common fix needed
3. **Connection event log** — Visibility into what happened
4. **Agent ping/reset** — Per-agent troubleshooting
5. **Log viewer** — Deep debugging
6. **GitHub sync status** — Already has endpoints, just needs UI
