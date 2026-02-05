# Otter Camp Data Flow Architecture

## Overview

Two WebSocket connections create a real-time pipeline:

```
┌─────────────┐      WebSocket #1      ┌─────────────────┐      WebSocket #2      ┌─────────────┐
│  OpenClaw   │ ───────────────────▶   │  api.otter.camp │  ◀───────────────────  │   Browser   │
│  (Mac/Edge) │   outbound from OC     │    (Railway)    │    browser connects    │  (sam.otter │
└─────────────┘                        └─────────────────┘                        │   .camp)    │
                                                                                  └─────────────┘
```

## Connection #1: OpenClaw → api.otter.camp

**Direction:** OpenClaw initiates outbound connection TO the API server.

**Why outbound?** OpenClaw runs on edge devices (Mac Studio, laptops) often behind NAT/proxies. Outbound connections work; inbound would require port forwarding.

**What flows:**
- Agent session state (tokens, last activity, model, status)
- Real-time message/event stream
- Task dispatch updates
- Feed items (emails, market data, social updates)

**Auth:** TBD - likely a shared secret or signed token in the WebSocket handshake.

## Connection #2: Browser ↔ api.otter.camp

**Direction:** Browser connects to API server (standard WebSocket).

**What flows:**
- UI updates pushed to browser instantly
- User actions sent to server
- Live indicators, typing status, agent heartbeats

**Auth:** Session cookie from magic link login.

## Data Flow Examples

### Agent sends a message
1. Agent generates response in OpenClaw
2. OpenClaw pushes event over WS#1 to api.otter.camp
3. API server broadcasts over WS#2 to connected browsers
4. Browser UI updates instantly (no polling)

### User approves a deploy
1. User clicks "Approve" in browser
2. Browser sends action over WS#2 to API
3. API pushes command over WS#1 to OpenClaw
4. OpenClaw routes to appropriate agent session
5. Agent receives approval, proceeds with deploy

### Feed item arrives (email, market alert)
1. Agent (Penny, Beau) processes incoming data
2. Pushes feed item to OpenClaw
3. OpenClaw forwards over WS#1 to API
4. API stores in DB + broadcasts over WS#2
5. Browser shows new feed item with animation

## Design Principles

1. **FAST** - No polling. Everything is push-based.
2. **MAGIC** - UI updates before you expect them to.
3. **Reactive** - State changes propagate instantly across all connected clients.
4. **Resilient** - Connections auto-reconnect. Missed events sync on reconnect.

## Implementation Status

### WS#2 (Browser ↔ API) — DONE ✅
- [x] WebSocket server endpoint on api.otter.camp (`/ws` route)
- [x] Browser WebSocket client in React (`useWebSocket` hook)
- [x] Hub + broadcast infrastructure (`internal/ws/hub.go`, `handler.go`)
- [x] Frontend connects to api.otter.camp (not same-origin) — fixed in 5fdef40
- [x] Live indicator reflects connection state — fixed in 8f8d854

### WS#1 (OpenClaw → API) — TODO
- [ ] Dedicated endpoint for OpenClaw connections (`/ws/openclaw`?)
- [ ] OpenClaw outbound connector (needs Sam's input on OpenClaw hooks/config)
- [ ] Auth handshake (shared secret or signed token)
- [ ] Event schema (what JSON payloads look like)
- [ ] Reconnection + sync logic on OpenClaw side

---

*Last updated: 2026-02-04 by Frank*
