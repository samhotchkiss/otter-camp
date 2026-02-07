# OpenClaw Secrets Runbook

This runbook covers required secrets for OpenClaw sync and websocket connectivity.

## Required Environment Variables

- `OPENCLAW_WS_SECRET`
  - Required by `/ws/openclaw`
  - OpenClaw bridge must present this token via query param `token` or header `X-OpenClaw-Token`
- `OPENCLAW_SYNC_SECRET` (preferred)
  - Required by `/api/sync/openclaw`
  - Backward-compatible aliases:
    - `OPENCLAW_SYNC_TOKEN`
    - `OPENCLAW_WEBHOOK_SECRET` (legacy fallback)
- `OPENCLAW_AUTH_SECRET`
  - Required for OpenClaw auth token validation
- `OTTERCAMP_ALLOW_INSECURE_MAGIC_AUTH` (optional, unsafe)
  - Local-only fallback for validating `oc_magic_*` tokens without DB sessions

## Deployment Checklist

1. Generate strong random values for all required secrets.
2. Set values in API runtime environment.
3. Set matching `OPENCLAW_WS_SECRET` and sync secret in bridge runtime.
4. Restart API and bridge deployments.

## Verification

### Sync endpoint auth

Valid token should succeed:

```bash
curl -i -X POST \
  -H "X-OpenClaw-Token: $OPENCLAW_SYNC_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"source":"bridge","agents":[],"sessions":[]}' \
  https://api.otter.camp/api/sync/openclaw
```

Missing/invalid token should fail with `401`.

### WebSocket auth

Valid token should establish a websocket:

```bash
wscat -c "wss://api.otter.camp/ws/openclaw?token=$OPENCLAW_WS_SECRET"
```

Missing/invalid token should fail with `401`.
