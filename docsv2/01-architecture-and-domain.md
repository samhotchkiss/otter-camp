---
## Summary

This spec defines the foundational architecture and domain model for OtterCamp V2, a ground-up rewrite that eliminates the OpenClaw dependency in favor of direct in-app orchestration. The system is built as a **modular monolith** (not microservices), with two runtime processes: a primary API service (HTTP + WebSocket/SSE) and a worker process for background jobs, both sharing the same codebase. Microservice decomposition is explicitly deferred until scale or reliability triggers justify it. The storage strategy is Postgres for transactional data, object store for large artifacts, with an optional local filesystem mirror for single-node self-hosting.

The core domain is organized around a small set of canonical entities: Organization, HumanUser, Agent, Session (chat thread), Message, Project, ProjectTask, FlowTemplate/FlowNode/FlowNodeExecution, Memory, ModelProfile, ProviderConnection, Skill, ToolExecution, McpConnection, and AuditEvent. These are grouped into **five strong domain boundaries**: Chat (sessions, messages, participants), Project (tasks, flow progression, approvals), Agent (identity, lifecycle, capability policy), Memory (extraction, retrieval, retention), and Model (provider abstraction, routing, cost policy). The top-level runtime components include the API service, worker service, model gateway, memory pipeline, connector runtime (MCP + native integrations), control runtime (CLI + browser automation), and the storage layer.

The product principles are opinionated: chat is the primary interface where humans talk to agents who take action, UI surfaces are for viewing and reviewing rather than direct manipulation, agents have strong defaults and act as domain experts, and blocked progress creates persistent tasks rather than dismissible notifications. Progressive disclosure is a core UX pattern. The spec also establishes that V2 is a clean-room rebuild with no reuse of V1 runtime code, database schema, or migrated data -- V1 informs requirements only. Domain events are emitted on entity transitions, stored in a durable log for replay and debugging, and fanned out in realtime to subscribed clients.

---

# 01. Architecture and Domain Model

## Goals

- Replace OpenClaw with direct in-app orchestration.
- Keep product primitives simple: org, user, agent, chat, project, task, memory, skill, connector.
- Support both synchronous UX (chat) and asynchronous automation (runs/jobs).

## Product Principles

- **Opinionated, not a blank canvas.** OtterCamp guides users into doing things the right way. Agents propose the right approach based on context, not present menus of options. Defaults should be good enough that most people just say "yeah, go." Users can override, but the happy path is the default path.
- **Chat is the primary interface.** The human talks to agents; agents take action. UI surfaces are for viewing, navigating, and reviewing — not for direct manipulation of system objects. Tasks are created through conversation, flows are designed through conversation, project setup happens through conversation.
- **Progressive disclosure.** Show status at a glance, let the user drill in for detail. Don't overwhelm with information that isn't needed at the current level of zoom.
- **Agents have strong defaults.** When the PM designs a flow, they recommend the right one — they don't ask "which pattern?" When the PM scopes a task, they produce something ready to execute — they don't ask the human to fill in a form. Each agent is an expert in their domain and acts like it.
- **Blocked progress creates tasks, not notifications.** When something requires action to keep a project moving forward, the system creates a task assigned to the responsible party. Notifications can be missed or dropped. Tasks persist until resolved. This applies to cancelled dependencies, merge conflicts, stuck agents, and any other situation where inaction means work stops.

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
- FlowNodeExecution
- Memory
- ModelProfile
- ProviderConnection
- Skill
- ToolExecution
- McpConnection
- AgentProfileTemplate (catalog of 230+ pre-built agent profiles)
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
