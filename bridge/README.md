# Bridge Supervisor and Monitor

This directory includes launchd and monitor assets for bridge self-healing.

## Assets

- `com.ottercamp.bridge.plist`: macOS launchd service for continuous bridge runtime
- `bridge-monitor.sh`: external health monitor for cron/OpenClaw scheduling

## launchd setup (macOS)

1. Copy the plist:
   - `cp bridge/com.ottercamp.bridge.plist ~/Library/LaunchAgents/`
2. Reload the service:
   - `launchctl unload ~/Library/LaunchAgents/com.ottercamp.bridge.plist 2>/dev/null || true`
   - `launchctl load ~/Library/LaunchAgents/com.ottercamp.bridge.plist`
3. Verify status:
   - `launchctl print gui/$(id -u)/com.ottercamp.bridge`

Rate limiting is configured via `ThrottleInterval=12` (roughly 5 restart attempts per 60s).

## External monitor usage

Run every 60 seconds from OpenClaw cron or system cron:

- `* * * * * /Users/sam/Documents/Dev/otter-camp-codex/bridge/bridge-monitor.sh`

Behavior:

- Polls `${BRIDGE_HEALTH_URL}` (default `http://127.0.0.1:8787/health`)
- Healthy/degraded response: resets failure counter and exits 0
- Unhealthy/unreachable response: executes restart command and exits 1
- On repeated failure threshold (`BRIDGE_ALERT_THRESHOLD`, default `2`), runs escalation hook

## Monitor environment variables

- `BRIDGE_HEALTH_URL`: health endpoint URL
- `BRIDGE_MONITOR_STATE_FILE`: local state file for consecutive failures
- `BRIDGE_MONITOR_TIMEOUT_SECONDS`: curl timeout seconds
- `BRIDGE_RESTART_CMD`: restart shell command
- `BRIDGE_ALERT_THRESHOLD`: consecutive failures before alert hook
- `BRIDGE_ALERT_CMD`: optional escalation command (for Slack DM/webhook wrappers)

When `BRIDGE_ALERT_CMD` runs, it receives:

- `BRIDGE_MONITOR_REASON`: `unhealthy` or `unreachable`
- `BRIDGE_MONITOR_FAILURES`: consecutive failure count
- `BRIDGE_MONITOR_HEALTH_URL`: monitored URL

Example Slack hook wrapper:

```bash
export BRIDGE_ALERT_CMD='curl -X POST -H "Content-Type: application/json" -d "{\"text\":\"Bridge unhealthy: $BRIDGE_MONITOR_REASON\"}" "$SLACK_WEBHOOK_URL"'
```

## Troubleshooting

- `launchctl` service not starting: confirm the plist path and workspace path in `ProgramArguments`.
- No monitor alerts: verify `BRIDGE_ALERT_CMD` is configured and executable.
- Flapping health: check `/tmp/ottercamp-bridge.error.log` and bridge `/health` payload details.
