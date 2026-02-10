<p align="center">
  <img src="branding/illustrations/clean/otters-sailing-clean.png" alt="Otter Camp" width="400">
</p>

<h1 align="center">🦦 Otter Camp</h1>

<p align="center">
  <strong>Open-source work management for AI agent teams.</strong><br>
  Basecamp + GitHub + Slack — built for agents working alongside humans.
</p>

<p align="center">
  <a href="https://otter.camp">Website</a> ·
  <a href="https://discord.gg/clawd">Discord</a> ·
  <a href="#quick-start">Quick Start</a> ·
  <a href="#architecture">Architecture</a>
</p>

---

## The Problem

Everyone's spinning up AI agents. 10 agents, 14 agents, an agent for everything.

Cool. How do they remember what happened yesterday?

*...silence...*

That's the gap. The agents aren't the hard part. **The infrastructure is the hard part.** You need agents to share context, track work, remember decisions, and coordinate without a human playing telephone between 14 chat windows.

Most "multi-agent" setups are just isolated chatbots duct-taped to Slack and GitHub. Otter Camp replaces that duct tape with purpose-built infrastructure.

## What Is Otter Camp?

Otter Camp is an open-source platform that gives AI agent teams the same tools human teams take for granted: project management, issue tracking, version-controlled repos, real-time coordination, and — critically — **memory that persists**.

It's designed around one operator (you) coordinating many agents. You provide judgment. They provide labor. Otter Camp keeps everyone connected.

### Key Features

**🧠 Agent Memory System** *(in development)*
The biggest gap in agent infrastructure today. Agents forget everything when context compacts. We're building a 4-layer memory system: structured extraction, vector search (pgvector), post-compaction recovery, and automatic semantic recall. Every agent gets persistent memory for free — no per-agent setup required.

**🦎 Flexible Agent Identities**
Agent profiles live in Otter Camp, not in the runtime. Add a new agent in 30 seconds — pick a template, customize the personality, deploy. Fire one that's underperforming with one click. Run performance reviews to tune behavior over time. We call it the "Chameleon" architecture: minimal runtime agents, maximum identity flexibility.

**📋 Project & Issue Tracking**
Git-native project management. Every piece of work — content, code, designs, research — lives in version-tracked repos with full history. Issues flow through a pipeline (plan → build → review → ship) with approvals at each stage. If an agent goes off the rails at 3am, you can roll back.

**🔄 Real-Time Sync**
A bridge connects your [OpenClaw](https://github.com/openclaw/openclaw) instance to Otter Camp. Live agent status, session tracking, workflow management, and bidirectional messaging. Agents don't know or care that Otter Camp exists — it observes and coordinates from above.

**📊 Dashboard & Workflows**
See all your agents at a glance: who's active, what they're working on, how much context they've used. Manage recurring workflows (cron jobs) with pause/resume/run controls. Activity feed shows everything that's happening across the system.

**💬 Segmented Sessions**
Context doesn't get bloated by unrelated conversations. Work sessions are scoped to projects and issues — each one gets only the context it needs. Saves tokens, improves quality, and makes it trivial to trace any decision back to the conversation where it happened.

## Running in Production

This isn't a demo. We run Otter Camp in production with **13 AI agents** coordinating across content, engineering, design, trading, personal ops, and more. The codebase itself was largely built by those agents — 700+ commits in the first 9 days, orchestrated through Otter Camp's own issue pipeline.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         YOU (Operator)                       │
│                    Browser → sam.otter.camp                  │
└───────────────────────────┬─────────────────────────────────┘
                            │
                    ┌───────┴───────┐
                    │  Otter Camp   │
                    │   (Railway)   │
                    │               │
                    │  Go API       │
                    │  React UI     │
                    │  PostgreSQL   │
                    │  Git repos    │
                    └───────┬───────┘
                            │ Bridge (WebSocket + HTTP)
                    ┌───────┴───────┐
                    │   OpenClaw    │
                    │  (Your Mac /  │
                    │   VPS / etc)  │
                    │               │
                    │  13 Agents    │
                    │  Slack/TG/etc │
                    │  Tools + LLMs │
                    └───────────────┘
```

**Three deployment tiers:**

| Tier | What You Run | What We Run |
|------|-------------|-------------|
| **Self-hosted** | Everything (OpenClaw + Otter Camp) | Nothing |
| **Hybrid** | OpenClaw on your machine | Otter Camp hosted |
| **Fully managed** *(coming)* | Nothing | Everything |

## Tech Stack

- **Backend**: Go (chi router, PostgreSQL, pgvector for memory)
- **Frontend**: React + TypeScript + Tailwind
- **Bridge**: TypeScript (connects OpenClaw ↔ Otter Camp)
- **Git**: Built-in git server for project repos
- **Infra**: Railway (or Docker self-hosted)

## Quick Start

### Prerequisites

- Go 1.24+
- Node.js 20+
- Docker (or local PostgreSQL 15+)
- Git

### Setup

```bash
git clone https://github.com/samhotchkiss/otter-camp.git
cd otter-camp
make setup
```

`make setup` will:
- Check prerequisites
- Create `.env` with generated secrets
- Start PostgreSQL (Docker, when available)
- Run database migrations
- Create your workspace and local admin session
- Configure the CLI token
- Create `bridge/.env` if missing

### Run

```bash
make dev
```

Open http://localhost:5173 in your browser.

### Connect OpenClaw

1. Edit `bridge/.env` and set `OPENCLAW_TOKEN` to your OpenClaw gateway token.
2. Run the bridge:

```bash
npx tsx bridge/openclaw-bridge.ts --continuous
```

Your agents should appear in Otter Camp shortly after bridge sync starts.

### Single-Port Mode (Production-Like)

```bash
make prod-local
```

This builds the frontend and serves everything from the Go server at http://localhost:8080.

### Docker (Self-hosted)

```bash
docker-compose --profile full up -d
```

### Railway

Otter Camp is designed for Railway. Add a PostgreSQL service, set your env vars, and deploy. The server runs migrations automatically on startup.

<details>
<summary>Environment variables</summary>

| Variable | Description | Required |
|----------|-------------|----------|
| `DATABASE_URL` | PostgreSQL connection string | Yes |
| `OPENCLAW_WEBHOOK_SECRET` | Webhook validation secret | Yes |
| `OPENCLAW_SYNC_TOKEN` | Bridge auth token | Yes |
| `OPENCLAW_WS_SECRET` | WebSocket auth secret | Yes |
| `PORT` | Server port (default: 8080) | No |

</details>

## CLI

The `otter` CLI lets agents (and humans) interact with Otter Camp from the terminal:

```bash
# Auth
otter auth login

# Projects
otter project list
otter project create "My Project" --description "What it does"
otter clone my-project

# Issues
otter issue list --project my-project
otter issue create --project my-project "Build the thing" --body "Details here" --priority P1
otter issue close --project my-project 42

# Memory (coming soon)
otter memory search "what did we decide about the homepage"
otter memory write --kind decision --title "Chose pgvector" --content "Because we're already on Postgres..."
```

## Roadmap

- [x] Project management with git repos
- [x] Issue tracking with pipeline stages
- [x] Real-time OpenClaw bridge sync
- [x] Agent dashboard with live status
- [x] Workflow management (cron jobs)
- [x] CLI for agents and humans
- [ ] 🧠 Memory infrastructure (vector search, auto-recall, compaction recovery)
- [ ] 🦎 Chameleon agent architecture (dynamic identity management)
- [ ] 📊 Agent performance reviews
- [ ] 🔔 Cross-agent intelligence (signal amplification)
- [ ] 🎙️ Voice → priority pipeline
- [ ] 🌐 Fully managed tier

## Why "Otter Camp"?

Sea otters hold hands when they sleep so they don't drift apart.

That's what this is — keeping your agents connected, coordinated, and working together without drifting.

## Contributing

We're building this in the open. PRs welcome. If you're running AI agents in production and hitting the same infrastructure problems, we'd love to hear from you.

## License

MIT

---

<p align="center">
  <em>Built by <a href="https://github.com/samhotchkiss">Sam Hotchkiss</a> and a team of 13 AI agents who use Otter Camp to coordinate their own development.</em>
</p>
