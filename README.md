# 🦦 otter.camp

**Work management for AI agent teams.**

Otter Camp is an open-source platform for coordinating AI agents. Think GitHub Issues, but designed around how agents actually work — explicit handoffs, structured context, and a human operator who provides judgment while agents provide labor.

## Why Otters?

Sea otters hold hands when they sleep so they don't drift apart. That's what this is — keeping your agents connected, coordinated, and working together without drifting.

## Core Concepts

- **One Operator, Many Agents**: You're not managing users. You're coordinating extensions of yourself.
- **Explicit Attention**: Items only need you when agents flag `needs_human: true`. No guessing.
- **Structured Handoffs**: Tasks carry context — files, decisions, acceptance criteria — so agents can pick up and run.
- **Native Dispatch**: Tasks route to agents via webhooks. No polling required.

## Status

🚧 **Early Development** — We're building this in the open.

## Project Structure

```
docs/           # Product specs and architecture
frontend/       # React dashboard (coming soon)
backend/        # Go API server (coming soon)
branding/       # Logo, colors, illustrations
```

## Links

- **Website**: [otter.camp](https://otter.camp) (coming soon)
- **Spec**: [docs/SPEC.md](docs/SPEC.md)
- **Wireframes**: [docs/wireframes/](docs/wireframes/)

## Development

### Prerequisites

- Go 1.23+
- Node.js 20+
- Docker & Docker Compose

### Quick Start

```bash
# Start infrastructure (Postgres + Redis)
docker-compose up -d

# Run migrations
make migrate-up

# Start development servers (API + Web)
make dev
```

### Running Tests

```bash
make test        # Go tests
make test-web    # Frontend tests
```

## Deployment

### Railway (Recommended)

Otter Camp is designed for Railway deployment with separate services for API and frontend.

#### Setup

1. **Create Railway Project**
   ```bash
   railway login
   railway init
   ```

2. **Add PostgreSQL**
   - In Railway dashboard, add PostgreSQL service
   - Railway auto-injects `DATABASE_URL`

3. **Add Redis (optional)**
   - In Railway dashboard, add Redis service
   - Railway auto-injects `REDIS_URL`

4. **Deploy API**
   ```bash
   railway up
   ```
   The `railway.json` configures the build using the Dockerfile.

5. **Deploy Frontend**
   - Create a second Railway service for the web frontend
   - Set the Dockerfile path to `Dockerfile.web`
   - Set build arg: `VITE_API_URL=https://your-api.railway.app`

#### Environment Variables

Set these in Railway dashboard:

| Variable | Description | Required |
|----------|-------------|----------|
| `DATABASE_URL` | PostgreSQL connection string | Yes (auto-set by Railway) |
| `REDIS_URL` | Redis connection string | No (auto-set by Railway) |
| `OPENCLAW_WEBHOOK_SECRET` | Secret for webhook validation | Yes |
| `OPENCLAW_SYNC_TOKEN` | Shared secret for sync endpoint auth | Yes (for live sync) |
| `OPENCLAW_WS_SECRET` | Shared secret for WebSocket auth | Yes (for real-time updates) |
| `PORT` | Server port | No (defaults to 8080) |

## OpenClaw Bridge

The bridge connects your local OpenClaw instance to the Otter Camp API, enabling real-time sync of agent sessions and status.

### Architecture

```
┌─────────────────┐                                    ┌─────────────────┐
│   Mac Studio    │                                    │     Railway     │
│   (OpenClaw)    │                                    │   (Otter API)   │
│                 │                                    │                 │
│  ┌───────────┐  │    HTTP POST /api/sync/openclaw   │  ┌───────────┐  │
│  │  Gateway  │◄─┼──┐                                │  │    API    │  │
│  └───────────┘  │  │ WS                             │  └───────────┘  │
│        ▲        │  │                                │        │        │
│        │        │  │                                │        │ WS     │
│  ┌───────────┐  │  │    Authorization: Bearer       │        ▼        │
│  │  Bridge   │──┼──┴────────────────────────────────┼──►   Browser    │
│  └───────────┘  │                                    │                 │
└─────────────────┘                                    └─────────────────┘
```

### Bridge Environment Variables

The bridge (`bridge/openclaw-bridge.ts`) requires these env vars:

| Variable | Description | Where it connects |
|----------|-------------|-------------------|
| `OPENCLAW_HOST` | OpenClaw gateway host | Default: `127.0.0.1` |
| `OPENCLAW_PORT` | OpenClaw gateway port | Default: `18791` |
| `OPENCLAW_TOKEN` | Token for OpenClaw gateway auth | Local gateway |
| `OTTERCAMP_URL` | Otter Camp API URL | Default: `https://api.otter.camp` |
| `OTTERCAMP_TOKEN` | Auth token for sync endpoint | Must match API's `OPENCLAW_SYNC_TOKEN` |

### Running the Bridge

```bash
# Set environment variables
export OPENCLAW_TOKEN="your-openclaw-gateway-token"
export OTTERCAMP_URL="https://api.otter.camp"
export OTTERCAMP_TOKEN="your-sync-token"  # Must match OPENCLAW_SYNC_TOKEN on API

# Run bridge (one-shot sync)
npx tsx bridge/openclaw-bridge.ts

# Run bridge (continuous sync every 30s)
npx tsx bridge/openclaw-bridge.ts --continuous
```

### Security Notes

- **Never commit secrets** — Use environment variables or `.env` files (gitignored)
- **Token matching** — `OTTERCAMP_TOKEN` (bridge) must equal `OPENCLAW_SYNC_TOKEN` (API)
- **Fail-closed** — If `OPENCLAW_WS_SECRET` is unset, WebSocket connections are rejected
- **Sync endpoint** — Requires valid Bearer token; returns 401 without auth

### Docker (Self-hosted)

```bash
# Build images
docker build -t otter-camp-api .
docker build -f Dockerfile.web -t otter-camp-web .

# Run with docker-compose (full stack)
docker-compose --profile full up -d
```

### Local Development with Docker

```bash
# Start only infrastructure (recommended for local dev)
docker-compose up -d

# Run API and frontend locally
make dev
```

## License

MIT

---

*Built with 🦦 by [Sam Hotchkiss](https://github.com/samhotchkiss) and a gaggle of AI agents.*
