---
## Summary

This spec defines the foundational architecture and domain model for OtterCamp V2, a ground-up rewrite that eliminates the OpenClaw dependency in favor of direct in-app orchestration. The system is built as a **modular monolith** (not microservices), with two runtime processes: a primary API service (HTTP + SSE) and a worker process for background jobs, both sharing the same Go codebase. Microservice decomposition is explicitly deferred until scale or reliability triggers justify it. The storage strategy is PostgreSQL with database-per-org isolation for transactional data, pgvector for memory embeddings, and object store for large artifacts.

The core domain is organized across **eight domain boundaries**. Four product domains — Chat (sessions, turns, messages, participants, artifacts), Project (tasks, subtasks, flows, scheduling, merge queue, inbox, shipping), Agent (identity, lifecycle, staffing, prompt assembly, 230+ profile catalog), and Memory (extraction, taxonomy, entity synthesis, retrieval, consolidation) — define the user-facing experience. Four infrastructure domains — Model (provider abstraction, routing, cost tracking, budgets), Control Plane (runs, tool executions, capability policies, worker dispatch), Tools (native/system/browser/external tool registry, resolution pipeline), and Events (domain event bus, consumer cursors, job queue) — support the product domains. The full schema comprises ~65 tables across these domains.

Eight product principles guide all design: chat is the primary interface, the API is the single source of truth (all clients are API consumers), agents have strong defaults and act as domain experts, policy is binary (allow/deny with no mid-turn blocking), communication tools create drafts (not sends), blocked progress creates persistent tasks, and progressive disclosure controls information density. Domain events are emitted on entity transitions, stored durably for replay, and fanned out to clients via SSE (default) with WebSocket available for bidirectional needs. Three deployment modes are supported from the same codebase: local single-node, self-hosted VPS, and managed multi-tenant.

---

# 01. Architecture and Domain Model

## Goals

- Replace OpenClaw with direct in-app orchestration.
- Keep product primitives simple: org, user, agent, chat, project, task, memory, skill, tool, connector.
- Support both synchronous UX (chat) and asynchronous automation (runs/jobs).
- One codebase, three deployment modes (local, self-hosted, managed).

## Product Principles

1. **Opinionated, not a blank canvas.** OtterCamp guides users into doing things the right way. Agents propose the right approach based on context, not present menus of options. Defaults should be good enough that most people just say "yeah, go." Users can override, but the happy path is the default path.

2. **Chat is the primary interface.** The human talks to agents; agents take action. UI surfaces are for viewing, navigating, and reviewing — not for direct manipulation of system objects. Tasks are created through conversation, flows are designed through conversation, project setup happens through conversation.

3. **API-first.** The API is the single source of truth (doc 12). The TUI, web UI, mobile app, and CLI are all API clients — no special internal paths. Every user-facing action is an API call. This ensures all clients get the same behavior, auth, policy evaluation, and audit logging.

4. **Binary policy.** Agent permissions are allow or deny — no "requires approval," no mid-turn blocking, no runtime approval gates (docs 16, 20). Permissions are configured in advance. Policy layers are strictly tightening: instance safety > org > project > agent profile. The turn never blocks waiting for human input.

5. **Communication tools create drafts, not sends.** When an agent composes an email or Slack message, the tool's designed behavior is to stage a draft for human review (doc 20). This is tool behavior, not policy interception — the policy says "allow," and the tool itself creates a draft.

6. **Agents have strong defaults.** When the PM designs a flow, they recommend the right one. When the PM scopes a task, they produce something ready to execute. Each agent is an expert in their domain and acts like it.

7. **Blocked progress creates tasks, not notifications.** When something requires action to keep a project moving forward, the system creates a task assigned to the responsible party. Notifications can be missed or dropped. Tasks persist until resolved.

8. **Progressive disclosure.** Show status at a glance, let the user drill in for detail. Don't overwhelm with information that isn't needed at the current level of zoom.

## Clean-Room Rebuild Decision

- V2 is a fresh implementation.
- No existing V1/OtterCamp runtime code is reused.
- No existing V1 database schema is reused.
- No existing V1 data is migrated (JSONL memory import is the only data bridge — doc 15).
- V1 can inform requirements, but not runtime architecture, module boundaries, or schema design.

## Architecture Decision (Locked)

- V2 ships as a modular monolith, not microservices.
- Language: Go.
- Runtime shape for initial releases:
  - One primary application service (HTTP API + SSE realtime + core domain modules).
  - One worker process for asynchronous jobs (same codebase and contracts).
- Microservice decomposition is explicitly deferred until scale/reliability triggers justify the added complexity.
- Three deployment modes from the same codebase (doc 08):
  - **Local**: single-node, all-in-one process, auto-login, for development.
  - **Self-hosted VPS**: Docker Compose, separate API + worker + PostgreSQL, single tenant.
  - **Managed multi-tenant**: database-per-org isolation, orchestration TBD (Phase 4).

## Top-Level Runtime Components

| Component | Responsibility | Spec |
|-----------|---------------|------|
| API service | HTTP REST + SSE realtime, auth, request routing | 12 |
| Worker service | Background runs, async tool execution, long jobs | 16 |
| Model gateway | Provider adapters, routing, concurrency, cost tracking | 07 |
| Memory pipeline | Extract, score, store, retrieve, consolidate | 06 |
| External tool runtime | MCP client, remote API connections, tool discovery | 09 |
| Control runtime | CLI sandboxing, browser automation, artifact capture | 11 |
| Prompt assembly | 7-layer pipeline, token budgeting, skill injection | 05 |
| Storage layer | PostgreSQL (db-per-org), pgvector, object store | 04, 08 |

## Domain Model

### Domain Boundaries

The system is organized into eight domain boundaries. Each domain owns its schema, business rules, and internal consistency. Domains communicate through domain events (doc 12) and well-defined service interfaces.

**Product domains** — the user-facing experience:

| Domain | Owns | Primary Spec | Tables |
|--------|------|-------------|--------|
| Chat | Sessions, turns, messages, participants, artifacts, summaries, reactions, read cursors | 02 | 8 |
| Project | Tasks, subtasks, flows, dependencies, scheduling, merge queue, inbox, shipping, environments | 03, 03a | 14 |
| Agent | Identity, lifecycle, staffing, project assignment, prompt packs, 230+ profile catalog | 05 | 4 |
| Memory | Extraction, taxonomy, entities, retrieval, dedup, consolidation, import | 06 | 9 |

**Infrastructure domains** — operational support:

| Domain | Owns | Primary Spec | Tables |
|--------|------|-------------|--------|
| Model | Provider abstraction, connections, profiles, routing, invocation tracking, usage rollups | 07 | 6 |
| Control Plane | Runs, run steps, tool executions, capability policies, artifacts, worker dispatch | 16 | 7 |
| Tools | Tool registry, resolution pipeline, session tool sets | 20 | 2 |
| Events & API | Domain event bus, consumer cursors, job queue, idempotency | 12 | 4 |

**Cross-cutting concerns** (serve multiple domains, tables listed where they have their own schema):

| Concern | Owns | Primary Spec | Tables |
|---------|------|-------------|--------|
| Auth & Identity | Org, users, sessions, API keys, audit events, db-per-org isolation | 04 | 5 |
| Skills | Skill documents, activation rules, flow node skill bindings | 10 | 2 |
| Security & Costs | Token budgets, retention, observability, alerting | 13 | 1 |
| External Connections | MCP connections, tool catalog, execution log, secret bindings | 09 | 4 |
| Testing | Test mode flag, state reset, synthetic time, fixture system | 21 | 0 |

**Total: ~65 tables across all domains.**

### Key Entities

The canonical entities that appear across multiple specs:

| Entity | Domain | Description |
|--------|--------|-------------|
| Organization | Auth | Top-level tenant. Every entity is org-scoped. Database-per-org isolation. |
| HumanUser | Auth | The human operator. Single user per org at GA. Four RBAC roles for future multi-user. |
| Agent | Agent | An AI agent with identity, personality, skills, tool policy, and model assignment. Staff (durable) or temp (scoped lifetime). |
| ChatSession | Chat | A conversation thread. Scoped to org, project, or task. Sync or async mode. |
| ChatTurn | Chat | One human-initiated turn cycle: human message, responder reply, listening evals, interjections. |
| Project | Project | A body of work backed by a git repository. Staffed by a PM and worker agents. |
| ProjectTask | Project | A unit of work within a project. Progresses through a flow template. Has a branch. |
| FlowTemplate | Project | A reusable workflow definition: nodes (work, review) connected by edges. |
| Memory | Memory | A durable knowledge record: episodic, semantic, or procedural. Scoped to org, project, task, or agent. |
| Run | Control Plane | A single agent execution: one agent working one turn or one async step. Contains RunSteps. |
| ToolExecution | Control Plane | A single tool call within a RunStep. Records the tool, parameters, result, and policy decision. |
| ToolDefinition | Tools | A registered tool: name, domain, category (native/system/browser/external), tier (1 or 2). |
| ModelProfile | Model | A model configuration: provider, model, temperature, timeouts. Assigned at org/project/agent/flow-node level. |
| Skill | Skills | A markdown instruction document injected into prompt layer 4. Activation rules control when it's loaded. |
| DomainEvent | Events | An immutable record of a state change. Stored durably, fanned out via SSE. |
| TokenBudget | Security | A spending limit (soft/hard) for an org or project, in tokens, for a time period. |

### Entity Relationships (Simplified)

```
Organization
 ├── HumanUser (owner, single user at GA)
 ├── Agent (staff: Frank, Lori, Ellie, PMs, workers, reviewers)
 │    ├── AgentProjectAssignment (many-to-many with projects)
 │    └── AgentSkillAttachment (many-to-many with skills)
 ├── ChatSession (org/project/task scoped)
 │    ├── ChatParticipant (agents + human)
 │    └── ChatTurn → ChatMessage → ChatArtifact
 ├── Project (git repo)
 │    ├── ProjectTask (branch per task)
 │    │    ├── FlowNodeExecution (per-task state of each flow node)
 │    │    ├── ProjectSubtask (within a flow node)
 │    │    └── ProjectTaskDependency (DAG)
 │    ├── TaskSchedule (cron-based auto-creation)
 │    ├── MergeQueueEntry (serial merge processing)
 │    └── InboxItem (human action required)
 ├── Memory (org/project/task/agent scoped)
 │    ├── MemoryTaxonomyNode (global classification tree)
 │    ├── MemoryEntity (consolidated knowledge about a concept)
 │    └── MemorySource (provenance chain)
 ├── ModelProfile → ProviderConnection → ModelProvider
 ├── Skill (org-level skills repo)
 ├── FlowTemplate → FlowNode (system + project templates)
 ├── ToolDefinition (native + external tools)
 ├── MCP Connection → McpToolCatalog
 ├── TokenBudget (org + project level)
 ├── CapabilityPolicy (per policy layer)
 └── DomainEvent (immutable audit trail)

Run (agent execution)
 └── RunStep (one step within a run)
      └── ToolExecution (one tool call)
```

## Data Storage Strategy

- **PostgreSQL** for all transactional data. Database-per-org isolation in managed mode (doc 04). One database in self-hosted mode.
- **pgvector** extension for memory embeddings (1536d, OpenAI-compatible, never truncated — doc 06).
- **Object store** (S3-compatible) for large artifacts: session exports, screenshots, agent work logs, file snapshots. Local filesystem fallback for single-node self-host.
- **Tiered retention**: memories forever, chat messages 1 year, model invocations 90 days (rollups forever), domain events 90 days, audit events 1 year. All configurable per org. Expired data archived to object storage, not deleted (doc 14).

## Eventing Model

- Domain events emitted on every create/update/delete transition (doc 12).
- `domain_event` table: durable, append-only log for replay and debugging. ~140 event types across all domains.
- **SSE** is the default realtime transport for server-to-client push (doc 12). WebSocket available as secondary for bidirectional needs (typing indicators, interactive tool sessions).
- **LISTEN/NOTIFY** (PostgreSQL) for inter-process wake-up between API and worker services (docs 08, 12).
- Consumer cursors for at-least-once delivery to event subscribers (memory pipeline, notification system, control plane).

## Non-Goals (for early V2)

- Full distributed scheduler across many regions.
- Hard multi-region active/active in initial release.
- Fully generic plugin API before core APIs stabilize.
- Maintaining source-level backward compatibility with V1 runtime internals.
- Microservice-first architecture.
- MCP sampling (server-initiated LLM calls through OtterCamp — doc 14).

## Future Service Split Triggers

- Sustained scaling asymmetry between domains (for example inference workload vs core API traffic).
- Release coupling materially slows delivery across teams.
- Reliability isolation requirements exceed what process-level boundaries can provide.
- Repeated incident patterns show one domain destabilizing others.

## Cross-Doc References

| Spec | Relevance |
|------|-----------|
| Doc 02 (Chat) | Chat domain: sessions, turns, messages, streaming, progressive summarization |
| Doc 03 (Projects) | Project domain: tasks, flows, subtasks, scheduling, merge queue, inbox |
| Doc 03a (Shipping) | Project domain: delivery patterns, remotes, environments |
| Doc 04 (Auth) | Auth: db-per-org, principal model, bootstrap, RBAC |
| Doc 05 (Agents) | Agent domain: identity, prompt assembly, starter trio, profile catalog |
| Doc 06 (Memory) | Memory domain: 9-table schema, retrieval pipeline, consolidation |
| Doc 07 (Models) | Model domain: provider adapters, routing, cost tracking, deterministic mode |
| Doc 08 (Deployment) | Three deployment modes, Docker Compose, environment configuration |
| Doc 09 (MCP) | External connections: MCP client, tool discovery, secret management |
| Doc 10 (Skills) | Skills: markdown documents, activation rules, prompt layer 4 |
| Doc 11 (System) | Control runtime: CLI sandboxing, browser automation, artifact capture |
| Doc 12 (API/Events) | Events domain: REST API, SSE, domain event bus, ~140 event types |
| Doc 13 (Security) | Security: defense-in-depth, token budgets, observability, retention |
| Doc 14 (Phasing) | Build phases, bootstrap dataset, resolved product decisions |
| Doc 15 (Migration) | Clean-room rebuild, JSONL memory import |
| Doc 16 (Control Plane) | Control plane domain: runs, policy evaluation, capability templates |
| Doc 20 (Tools) | Tools domain: 4 categories, 2 tiers, ~55 native tools, resolution pipeline |
| Doc 21 (Testing) | Testing: OTTERCAMP_MODE, 3-layer test architecture, CI pipeline |
