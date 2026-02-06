# Workflows Data Model + API

## Overview
Workflows represent long-running automations (cron jobs, event-driven tasks, manual runs). The current implementation sources workflow data from OpenClaw agent configs (heartbeat schedules) received via the OpenClaw sync bridge.

## Data Model
```json
{
  "id": "openclaw-2b",
  "name": "Derek workflow",
  "trigger": {
    "type": "cron",
    "every": "5m",
    "cron": "*/5 * * * *",
    "event": "New email received",
    "label": "Every 5 minutes"
  },
  "steps": [
    { "id": "2b-run", "name": "Run agent workflow", "kind": "agent" }
  ],
  "status": "active",
  "last_run": "2026-02-06T01:09:12Z"
}
```

### Fields
- `id`: unique workflow ID
- `name`: human-readable name
- `trigger`: how the workflow starts
  - `type`: `cron` | `event` | `manual`
  - `every`: interval string (e.g. `5m`)
  - `cron`: cron expression (optional)
  - `event`: event name (optional)
  - `label`: optional display label
- `steps`: ordered list of steps
- `status`: `active` or `paused`
- `last_run`: ISO timestamp of most recent execution (optional)

## API
### `GET /api/workflows`
Returns the list of workflows.

### `GET /api/workflows?demo=true`
Returns demo workflows for the public demo environment.

## Data Source
- **Primary:** OpenClaw agent configs (heartbeat schedules).
- **Storage:** `openclaw_agent_configs` table, populated by `/api/sync/openclaw`.
- **Last run:** derived from `agent_sync_state.updated_at` when available.

## Notes
- Workflows are currently derived from OpenClaw heartbeat schedules.
- If no configs are synced yet, the API returns an empty list (UI shows an empty state).
