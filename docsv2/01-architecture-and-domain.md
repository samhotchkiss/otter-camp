# 01. Architecture and Domain Model

## Goals

- Replace OpenClaw with direct in-app orchestration.
- Keep product primitives simple: org, user, agent, chat, project, task, memory, skill, connector.
- Support both synchronous UX (chat) and asynchronous automation (runs/jobs).

## Clean-Room Rebuild Decision

- V2 is a fresh implementation.
- No existing V1/OtterCamp runtime code is reused.
- No existing V1 database schema is reused.
- No existing V1 data is migrated.
- V1 can inform requirements, but not runtime architecture, module boundaries, or schema design.

## Architecture Decision (Locked)

- V2 ships as a modular monolith, not microservices.
- Runtime shape for initial releases:
  - One primary application service (HTTP API + realtime endpoints + core domain modules).
  - One worker process for asynchronous jobs (same codebase and contracts).
- Microservice decomposition is explicitly deferred until scale/reliability triggers justify the added complexity.

## Top-Level Runtime Components

- API service (HTTP + WebSocket/SSE)
- Worker service (background runs, tool calls, long jobs)
- Model gateway (provider adapters + routing + policy)
- Memory pipeline (extract, score, store, retrieve)
- Connector runtime (MCP + native integrations)
- Control runtime (CLI operations + browser automation)
- Storage layer (Postgres primary, object store for artifacts)

## Canonical Domain Entities

- Organization
- HumanUser
- Agent
- Session (chat thread)
- Message
- Project
- ProjectTask
- FlowTemplate
- FlowNode
- FlowBlocker
- MemoryItem
- ModelProfile
- Skill
- ToolExecution
- ExternalConnection
- AuditEvent

## Strong Domain Boundaries

- Chat owns sessions/messages/participant state.
- Project system owns tasks, flow progression, and approvals.
- Agent system owns identity, lifecycle, capability policy.
- Memory system owns extraction, retrieval, retention.
- Model system owns provider abstraction, routing, and cost policy.

## Data Storage Strategy

- Postgres for transactional entities.
- Object store for large artifacts (session exports, screenshots, logs, files).
- Optional local filesystem mirror in single-node self-host mode.

## Eventing Model

- Domain events emitted on create/update transitions.
- Durable event log table for replay and debugging.
- Realtime fanout to subscribed clients.

## Non-Goals (for early V2)

- Full distributed scheduler across many regions.
- Hard multi-region active/active in initial release.
- Fully generic plugin API before core APIs stabilize.
- Maintaining source-level backward compatibility with V1 runtime internals.
- Microservice-first architecture.

## Future Service Split Triggers

- Sustained scaling asymmetry between domains (for example inference workload vs core API traffic).
- Release coupling materially slows delivery across teams.
- Reliability isolation requirements exceed what process-level boundaries can provide.
- Repeated incident patterns show one domain destabilizing others.
