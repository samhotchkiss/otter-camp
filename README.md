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
A bridge connects your [OpenClaw](https://github.com/openclaw/openclaw) instance to Otter Camp. Live agent status, session tracking, workflow project sync, and bidirectional messaging. Agents don't know or care that Otter Camp exists — it observes and coordinates from above.

**📊 Dashboard & Workflow Projects**
See all your agents at a glance: who's active, what they're working on, how much context they've used. Manage recurring workflow projects with pause/resume/run controls and run history in project issues. Activity feed shows everything that's happening across the system.

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

- Go 1.23+
- Node.js 20+
- PostgreSQL 15+ (with pgvector extension)
- An [OpenClaw](https://github.com/openclaw/openclaw) instance

### Development

```bash
# Clone
git clone https://github.com/samhotchkiss/otter-camp.git
cd otter-camp

# Start Postgres
docker-compose up -d

# Run migrations
make migrate-up

# Start dev servers (API + frontend)
make dev
```

### Bridge Setup

The bridge syncs your local OpenClaw instance with Otter Camp:

```bash
# Configure
export OPENCLAW_TOKEN="your-gateway-token"
export OTTERCAMP_URL="https://api.otter.camp"  # or localhost
export OTTERCAMP_TOKEN="your-sync-token"

# Run (continuous sync)
npx tsx bridge/openclaw-bridge.ts --continuous
```

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
otter project list --workflow
otter project create "My Project" --description "What it does"
otter project create "Morning Briefing" --workflow --schedule "0 6 * * *" --tz "America/Denver" --template-title "Morning Briefing — {{date}}" --auto-close
otter project run "Morning Briefing"
otter project runs "Morning Briefing" --limit 20
otter clone my-project

# Issues
otter issue list --project my-project
otter issue create --project my-project "Build the thing" --body "Details here" --priority P1
otter issue close --project my-project 42

# Memory (coming soon)
otter memory search "what did we decide about the homepage"
otter memory write --kind decision --title "Chose pgvector" --content "Because we're already on Postgres..."
```

## Workflows Are Projects

Otter Camp models recurring workflows as standard projects with extra workflow fields:

- `workflow_enabled`
- `workflow_schedule`
- `workflow_template`
- `workflow_agent_id`
- run tracking fields (`workflow_last_run_at`, `workflow_next_run_at`, `workflow_run_count`)

### API migration map

| Legacy endpoint | Project workflow endpoint |
|---|---|
| `GET /api/workflows` | `GET /api/projects?workflow=true` |
| `PATCH /api/workflows/{id}` | `PATCH /api/projects/{id}` |
| `POST /api/workflows/{id}/run` | `POST /api/projects/{id}/runs/trigger` |

Legacy `/api/workflows` routes remain as compatibility adapters while clients migrate.

### Bridge migration behavior

On sync, the bridge auto-discovers OpenClaw cron jobs and maps each job to a workflow project (via embedded `cron_id` in schedule metadata, with name fallback). When cron `last_run_at` advances, the bridge triggers:

- `POST /api/projects/{id}/runs/trigger`

This creates a project issue per run and preserves a queryable run history.

## Roadmap

- [x] Project management with git repos
- [x] Issue tracking with pipeline stages
- [x] Real-time OpenClaw bridge sync
- [x] Agent dashboard with live status
- [x] Workflow projects (cron-backed recurring issue creation)
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
