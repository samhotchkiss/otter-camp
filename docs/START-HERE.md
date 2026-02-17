# Otter Camp: START HERE

> Summary: Canonical map of the current Otter Camp system, how components fit together, and how documentation must be maintained.
> Last updated: 2026-02-17
> Audience: Agents first, humans second.

## What Otter Camp Is

Otter Camp is an agent-operations platform built around one workspace/org as the source of truth for:
- Projects and issues
- Agent identities and lifecycle
- Conversation history and memory
- Local and hosted operation with OpenClaw bridge support

The stack is:
- Backend: Go (`cmd/server`, `internal/*`)
- Frontend: React + Vite (`web/`)
- Database: Postgres with migrations (`migrations/`)
- Bridge runtime: TypeScript bridge process (`bridge/openclaw-bridge.ts`)
- CLI: `otter` (`cmd/otter`)

## Two Supported Runtime Modes

### 1. Local Mode (single machine)

Use when running Otter Camp directly on your machine.

- API/UI default: `http://localhost:4200`
- Typical setup path: `scripts/bootstrap.sh` or `otter init --mode local`
- Optional bridge: local OpenClaw -> local Otter API (`bridge/.env` with `OTTERCAMP_URL=http://localhost:4200`)

Key files:
- `scripts/bootstrap.sh`
- `scripts/setup.sh`
- `docker-compose.yml`
- `bridge/.env.example`

### 2. Hosted + Bridge Mode (`{site}.otter.camp`)

Use when UI/API are hosted but OpenClaw runs on operator hardware.

- UI typically `{site}.otter.camp` (historically `sam.otter.camp`)
- API typically `https://api.otter.camp`
- Bridge pushes sync to hosted API and maintains WebSocket control channel

Key files:
- `bridge/openclaw-bridge.ts`
- `internal/api/openclaw_sync.go`
- `internal/ws/openclaw_handler.go`
- `internal/api/admin_connections.go`
- Hosted setup flow: `curl -sSL otter.camp/install | bash -s -- --token <oc_sess_...> --url https://<slug>.otter.camp`

## Two Doc Categories

### Working WITH OtterCamp → `docs/instructions/`
Instructions for agents, operators, and contributors. How to use the system.
- Start at `docs/instructions/START-HERE.md`
- Memory architecture (three-layer system): `docs/instructions/memory-architecture.md`
- Project documentation spec: `docs/instructions/project-docs-spec.md`
- Project workflow (create, clone, commit): `docs/instructions/project-workflow.md`

### Working ON OtterCamp → `docs/` (everything else)
Internal architecture, code docs, subsystem design. How the system works.

## How To Find Information Quickly

Use this map:

1. Memory subsystem: `docs/memories/`
- Start at `docs/memories/overview.md`
- Then read ingest (`initial-ingest.md`, `ongoing-ingest.md`), retrieval (`recall.md`), and embeddings (`vector-embedding.md`)
- Experimental findings: `experiment-log.md`, `entity-synthesis.md`, `file-backed-memories.md`, `dedup.md`

2. Project and issue subsystem: `docs/projects/`
- Start at `docs/projects/overview.md`
- Then issue model and lifecycle (`how-issues-work.md`, `issue-flow.md`)
- Runtime split (`local-vs-bridge.md`) for deployment-dependent behavior
- Hosted wildcard routing rollout (`hosted-wildcard-routing.md`) for DNS/TLS setup and verification

3. Agent subsystem: `docs/agents/`
- Start at `docs/agents/overview.md`
- Lifecycle operations (`hiring-and-firing.md`)
- Runtime behavior (`runtime-modes.md`)

4. Cross-cutting quality and debt:
- `docs/ISSUES-AND-OLD-CODE.md`
- `docs/SAM-QUESTIONS.md`

## Documentation Maintenance Policy (Mandatory)

These docs are the source of truth and must be updated with every meaningful code change.

Required rules:
1. If behavior, architecture, API contract, or workflow changes, update the relevant canonical docs in this `docs/` tree.
2. At minimum, add a new entry in the `## Change Log` at the bottom of each touched doc.
3. If change is a bug fix with no behavior change, still add a log entry explicitly stating that.

Standard log format:
- `- YYYY-MM-DD: <what changed>.`
- Bug-fix/no behavior template:
  - `- YYYY-MM-DD: Bug fix in <area>; no behavior change.`

Enforcement:
- CI runs a docs guard test (`cmd/server/docs_guard_test.go`) on pull requests.
- PRs with non-doc code changes but no canonical docs update fail CI.

## High-Level Component Flow

1. OpenClaw bridge sends sync payloads to `/api/sync/openclaw` and keeps `/ws/openclaw` connected.
2. API persists agent/session/diagnostic snapshots and dispatch queue state.
3. Frontend reads API + websocket events for live status.
4. Background workers ingest conversations, embed, segment, extract memory, inject context, and run scheduled jobs.

## Core Technical Decisions (Recent)

Primary implementation wave came through issue/commit streams around Spec #150-#159:
- #150: Conversation schema redesign foundation
- #151 + follow-ups: Ellie memory infrastructure and retrieval hardening
- #152: Proactive context injection
- #153: Sensitivity fields on conversation/memory paths
- #154: Compliance review orchestration + rule management
- #155: OpenClaw migration/import runner with source guard
- #157: Agent job scheduler (DB/API/CLI/worker)
- #158: Conversation token tracking + rollups
- #159: Semantic retrieval + LLM extraction fallback in Ellie ingestion

## Codebase Map

- `cmd/server`: server startup, migrations, workers, HTTP serving
- `internal/api`: REST endpoints and HTTP handlers
- `internal/store`: DB access and domain stores
- `internal/memory`: retrieval/ingestion/injection/tuning/evaluation pipeline
- `internal/import`: OpenClaw migration and safety guardrails
- `internal/scheduler`: recurring job execution
- `internal/ws`: websocket hub + OpenClaw WS bridge endpoint
- `cmd/otter`: CLI for auth, projects, issues, jobs, migration, local ops
- `web/src`: frontend pages/components/contexts
- `bridge/`: OpenClaw bridge runtime + monitor scripts

## Read Next

1. `docs/instructions/START-HERE.md` (if you're working WITH OtterCamp)
2. `docs/memories/overview.md`
3. `docs/projects/overview.md`
4. `docs/agents/overview.md`
5. `docs/ISSUES-AND-OLD-CODE.md`
6. `docs/SAM-QUESTIONS.md`

## Change Log

- 2026-02-17: Bug fix in hosted CLI setup handoff conflict-resolution follow-up; no behavior change.
- 2026-02-17: Added hosted CLI setup handoff path (`/install` script + `otter init --mode hosted --token --url` import/bridge flow) for non-interactive hosted onboarding (Spec 312).
- 2026-02-17: Documented hosted invite onboarding launch flow (`/join/<invite-code>`) and hosted multi-org bootstrap guard configuration (Spec 310).
- 2026-02-16: Added hosted wildcard DNS/TLS rollout runbook link for `{slug}.otter.camp` operator validation (Spec 311).
- 2026-02-17: Documented uninstall teardown hardening for local runtime cleanup (server/bridge stop, PID/log cleanup, and port-free verification) from Spec 313.
- 2026-02-16: Added project-docs ingestion architecture updates for scanner, retrieval, and migration runner phases (Spec 304).
- 2026-02-16: Bug fix in CI test guardrails; no behavior change.
- 2026-02-16: Added explicit navigation instructions, mandatory docs maintenance policy, and CI docs-guard enforcement guidance.
- 2026-02-16: Added docs/instructions/ directory (working WITH vs ON distinction) and updated navigation.
- 2026-02-16: Created canonical START-HERE map and migrated legacy docs context.
