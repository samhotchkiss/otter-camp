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
| `PORT` | Server port | No (defaults to 8080) |

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
