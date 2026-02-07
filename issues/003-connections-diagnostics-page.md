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
| Sync latency | Time between syncs | Average and max over last hour |
| Bridge uptime | Bridge process start time | How long the bridge has been running |
| Bridge reconnect count | Bridge tracks internally | How many times WS reconnected since start |
| Gateway version | Sync payload or `/health` | OpenClaw build (e.g., `2026.2.6-3 build 85ed6c7`) |
| Gateway PID | Sync payload | Process ID of the gateway |
| Gateway port | Sync payload | Current port (e.g., 18791) |
| Gateway uptime | Sync payload | Time since gateway process started |
| Node.js version | Sync payload | Runtime version (e.g., v25.4.0) |
| Host hostname | Sync payload | Machine name |
| Host OS | Sync payload | e.g., Darwin 25.2.0 (arm64) |
| Host uptime | Sync payload | System uptime (not just gateway) |
| Load average | Sync payload | 1m, 5m, 15m load |
| Memory | Sync payload | Total / used / available (GB) |
| Disk | Sync payload | Total / used / free (TB) |
| CPU | Sync payload | Chip model, core count (e.g., M1 Ultra, 20 cores) |
| Network | Sync payload | Local IP, public IP (if detectable), active interfaces |

### Data Source

The bridge (`bridge/openclaw-bridge.ts`) already pushes sync payloads to `POST /api/sync/openclaw` containing session data. Extend the sync payload to include host diagnostics:

```json
{
  "type": "full",
  "timestamp": "...",
  "host": {
    "hostname": "Mac-Studio",
    "os": "Darwin 25.2.0",
    "arch": "arm64",
    "platform": "darwin",
    "uptime_seconds": 543200,
    "load_avg": [3.2, 2.8, 2.5],
    "cpu_model": "Apple M1 Ultra",
    "cpu_cores": 20,
    "memory_total_bytes": 137438953472,
    "memory_used_bytes": 48318382080,
    "memory_available_bytes": 89120571392,
    "disk_total_bytes": 8001563222016,
    "disk_used_bytes": 3401563222016,
    "disk_free_bytes": 4600000000000,
    "network_interfaces": [
      { "name": "en0", "address": "192.168.1.50", "family": "IPv4" }
    ],
    "gateway_pid": 16314,
    "gateway_version": "2026.2.6-3",
    "gateway_build": "85ed6c7",
    "gateway_port": 18791,
    "gateway_uptime_seconds": 234567,
    "node_version": "v25.4.0",
    "ollama_version": "0.15.1",
    "ollama_models_loaded": ["llama3.1:70b"]
  },
  "bridge": {
    "uptime_seconds": 123456,
    "reconnect_count": 3,
    "last_sync_duration_ms": 45,
    "sync_count_total": 8920,
    "dispatch_queue_depth": 0,
    "errors_last_hour": 2
  },
  "agents": [...],
  "sessions": [...]
}
```

The bridge gathers this from:
- `os` module: hostname, uptime, load, memory, cpu, network
- `openclaw status` or gateway API: version, PID, port
- `ollama list`: loaded models
- Internal bridge state: reconnect count, sync stats

---

## Section 2: Agent Sessions Status

### Display

A table/card grid showing all agent sessions:

| Field | Description |
|-------|-------------|
| Agent name | e.g., "Frank", "Derek", "Stone" |
| Agent slot | e.g., "main", "2b", "three-stones" |
| Status | 🟢 Online / 🟡 Busy / 🔴 Offline / ⚠️ Stalled |
| Model | Current model (e.g., `claude-opus-4-6`) |
| Context tokens | Current token usage with color coding (green < 100k, yellow 100-150k, red > 150k) |
| Total tokens | Lifetime token count for the session |
| Last active | Time since last activity (relative + absolute timestamp) |
| Session key | The OpenClaw session key |
| Session ID | The internal session ID |
| Channel | Current channel (slack, discord, etc.) |
| Last channel target | Last `to` / `accountId` used |
| Heartbeat interval | How often the agent checks in (from config) |
| Aborted last run | Whether the last run was aborted (⚠️ flag) |
| Transcript path | Path to the session transcript file on host |

### Stall Detection

Flag a session as "stalled" (⚠️) if any of:
- `abortedLastRun` is true
- Last activity > 2× heartbeat interval and session has heartbeat configured
- Context tokens > 150k (approaching OpenClaw's effective limit given system prompt overhead)
- Session hasn't been seen in sync data for > 2 sync cycles

### Session Detail Expandable

Clicking an agent row expands to show:
- Full session metadata JSON (raw, copyable)
- Recent cron jobs associated with this agent (from OpenClaw cron list)
- Error count in last hour
- Suggested actions based on state:
  - Stalled → "Reset Session" button
  - High tokens → "Session may need restart to clear context"
  - Aborted → "Last run failed — check logs"

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
1. Bridge reads last N lines from the OpenClaw log file (typically `~/.openclaw/logs/`)
2. Pushes via WebSocket or returns via API
3. Frontend shows scrollable log viewer with auto-scroll

**UI:** Expandable log panel at the bottom of the page. Features:
- Tail mode (auto-scroll new lines)
- Filter by level (error, warn, info, debug)
- Filter by agent name
- Search/grep within logs
- Copy log selection to clipboard
- Timestamp display (absolute and relative)

### 4f. Run Diagnostics

**What it does:** Runs a suite of health checks and reports results.

**Implementation:**
1. Frontend calls `POST /api/admin/diagnostics`
2. Bridge executes a diagnostic script that checks:
   - Gateway process health (`openclaw gateway status`)
   - Port availability (is the gateway port actually listening?)
   - API responsiveness (can the gateway respond to a health check?)
   - Disk space remaining
   - Memory pressure
   - Ollama service health (`ollama list`)
   - Network connectivity (can reach api.otter.camp from host?)
   - Pending cron jobs
   - Stuck/zombie processes
3. Returns structured results with pass/fail/warn per check

**UI:** "Run Diagnostics" button → shows a checklist of results as they complete:
```
✅ Gateway process running (PID 16314)
✅ Port 18791 listening
✅ Gateway API responding (12ms)
✅ Disk space OK (4.6TB free)
⚠️ Memory usage high (92GB / 128GB)
✅ Ollama running (3 models loaded)
✅ Network: api.otter.camp reachable (45ms)
❌ Agent "Stone" stalled (no activity in 3h)
⚠️ 2 cron jobs failed in last hour
```

### 4g. Emergency Stop

**What it does:** Stops the gateway entirely (all agents go offline).

**Implementation:** `openclaw gateway stop` via bridge.

**UI:** Red button, double-confirmation ("This will take ALL agents offline. Type 'STOP' to confirm.")

### 4h. Port Bump

**What it does:** Bumps the gateway port to fix WS corruption (known recurring issue).

**Implementation:**
1. Bridge reads current config
2. Increments `gateway.port` by 1
3. Applies config and restarts gateway

**UI:** Button labeled "Fix WebSocket (Port Bump)" with explanation tooltip: "Bumps the gateway port to clear corrupted WebSocket state. This is a known fix for connection issues."

---

## Section 5: Cron Jobs Status

### Display

Table showing all active cron jobs:

| Field | Description |
|-------|-------------|
| Job name | Human-readable name |
| Job ID | UUID |
| Schedule | Cron expression or interval |
| Session target | main / isolated |
| Payload type | systemEvent / agentTurn |
| Last run | When it last fired |
| Last status | success / error |
| Next run | When it will fire next |
| Enabled | Toggle on/off |

### Actions
- **Enable/disable** individual jobs
- **Force run** a job immediately
- **View run history** for a specific job

### Data Source
Bridge can query OpenClaw cron API and relay to Otter Camp.

---

## Section 6: Process List

Show active background processes (exec sessions) on the host:

| Field | Description |
|-------|-------------|
| Session ID | exec session identifier |
| Command | What's running (truncated) |
| PID | OS process ID |
| Status | running / completed / failed |
| Duration | How long it's been running |
| Agent | Which agent spawned it |

### Actions
- **Kill process** — terminate a stuck background process
- **View output** — stream stdout/stderr

---

## Section 7: Connection History / Event Log

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
