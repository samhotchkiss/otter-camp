---
## Summary

This is the meta-planning document for the OtterCamp V2 ground-up rewrite. It serves three purposes: (1) tracking the four-phase build plan with concrete deliverables, dependencies, and milestones for each phase; (2) recording the resolution of major product-level decisions; and (3) cataloging genuinely open questions, risk areas, and the complete bootstrap dataset that ships with a fresh install. The three biggest product decisions have been resolved: agent capabilities use a default-deny posture with binary allow/deny policy outcomes and no mid-turn blocking; self-host ships first (Phases 1-2) with managed multi-tenant deferred to Phase 4; and the minimum bootstrap dataset includes the starter trio (Frank, Lori, Ellie), default model profiles, flow templates, skills, and policies so a fresh install is immediately usable.

The build phases are strictly sequential. Phase 0 (Foundation) delivers infrastructure with no user-facing product: PostgreSQL schema, auth/identity, model gateway with Anthropic adapter, event bus, 7-layer prompt assembly pipeline, control plane with default-deny capability model, two-tier tool framework, and the CLI bootstrap command. Phase 1 (Sync Chat + TUI) produces the first usable product where a human chats with agents via a terminal UI, including the full chat session system, starter trio agents with prompt packs, basic memory (Ellie's passive injection and implicit capture), skills foundation, and streaming model responses. Phase 2 (Projects + Tasks) adds autonomous work: projects as git repos, task lifecycle with flow templates and node progression, subtasks, scheduling with cron, async mode, merge queue, inbox, blocker escalation, temp agents, shipping/delivery, and memory hardening (dedup, entity synthesis, contradiction detection). Phase 3 (Self-Building) is where OtterCamp builds itself, delivering the web UI (React SPA), MCP integration for external tools, system integration (sandboxed CLI/browser), full control plane with binary policy enforcement, multi-provider model routing, observability stack, complete memory pipeline (sleep-time reflection, taxonomy management, importer), and the REST API surface. Phase 4 (Hardening + Distribution) covers security audit, Docker Compose self-host packaging, mobile UI with push notifications, multi-tenant operations with RLS, additional model providers including local models, and migration tooling.

All 48 open questions collected from across the spec suite have been resolved — the document preserves each decision with its rationale. Key resolutions include: tokens as the universal budget unit across org/project/agent levels, automatic starter trio profile upgrades on startup with an `operator_instructions` field that is never overwritten, tiered data retention (memories forever, chat 1 year, invocations 90 days, all configurable per org), auto-login for local mode, no MCP sampling support, 3-5 starter MCP connection templates, and process-level CLI sandboxing. UI detail questions (panel proportions, animations, tablet layout, etc.) are deferred to Figma/design phase. The document also identifies five technically hardest challenges (prompt budget tuning, memory retrieval at scale, async agent reliability, git merge conflicts, MCP edge-case reliability), five plan-adjustment risks, and four "what could go wrong" scenarios. The bootstrap dataset section fully specifies the starter trio profiles (Frank as Chief of Staff, Lori as Agent Relations, Ellie as Memory), default skills, three model profiles, four flow templates, default policies, the General chat session, and the 10-step idempotent bootstrap sequence.

---

# 14. Open Questions and Build Phasing

## Purpose

This is the meta-planning document for OtterCamp V2. It tracks build phases, dependencies between specs, resolved product-level decisions, genuinely open questions, risk areas, and the bootstrap dataset. It references decisions made in other specs rather than duplicating them.

---

## Resolved Product Decisions

The original stub listed three "biggest open product decisions." All three are now resolved based on decisions made across the finished and drafted specs.

### 1. How strong is default human approval for risky actions?

**Resolved: default-deny for agent capabilities, with binary policy outcomes (allow/deny) and no mid-turn blocking.**

The control plane (doc 16) establishes a default-deny capability posture. Agents can only do what they are explicitly permitted to do. Every tier 2 tool call (doc 20) goes through policy evaluation with two possible outcomes:

- **allow**: execute immediately.
- **deny**: return "not permitted" as a tool result. The agent adapts.

Policy is binary — always immediate, no runtime approval gating. Permissions are configured in advance. Communication tools (email, Slack) create drafts as their designed tool behavior, not as a policy outcome — the policy says "allow," and the tool itself stages a draft for human review (doc 20). Sensitive domains and commands are denied by default and must be explicitly allowlisted.

Policy layers are strictly tightening (doc 05): instance safety > org > project > agent profile. Each layer can only restrict, never expand. The human configures the policy posture per their risk tolerance. The system ships with sensible defaults that are secure without being paralyzing.

No mid-turn approval gates exist (doc 02). The turn never blocks waiting for human input. This is a fundamental design decision that keeps agent execution predictable and prevents deadlocks.

### 2. What is the managed-hosting launch shape vs self-host launch shape?

**Resolved: self-host first (Phase 1-2), managed hosting deferred (Phase 4).**

The deployment architecture (doc 08) defines three modes: local single-node, VPS single-tenant (self-host), and managed multi-tenant. The modular monolith architecture (doc 01) supports all three from the same codebase.

Self-host is the primary launch target. Docker Compose is the packaging format (doc 08). The operator runs one API service, one worker service, PostgreSQL, and optional object storage. This is sufficient for Phase 0 through Phase 3.

Managed multi-tenant adds: org isolation with RLS (doc 04), shared infrastructure concerns, billing, and operational monitoring at scale. This is Phase 4 scope.

The schema is multi-tenant from day one (doc 04 — every entity carries `organization_id`), so managed hosting does not require a schema migration. The delta is operational infrastructure, not architecture.

### 3. Minimum bootstrap dataset that ships with V2?

**Resolved: starter trio profiles, org session, default skills, default model profiles, default flow templates, default policies.**

See the Bootstrap Dataset section at the end of this document for the complete specification. The short version: every fresh install is immediately usable. The human opens OtterCamp, talks to Frank, and starts working. No configuration required beyond providing model provider API keys.

---

## Build Phases Overview

V2 builds in four phases, each producing a usable product that extends the prior phase. Dependencies between phases are strict — nothing in Phase N can be built without the foundation from Phase N-1.

```
Phase 0: Foundation       (infra — no usable product)
Phase 1: Sync Chat + TUI  (first usable product)
Phase 2: Projects + Tasks  (autonomous work)
Phase 3: Self-Building     (OtterCamp builds itself)
Phase 4: Hardening + Scale (security, packaging, mobile, multi-tenant)
```

---

## Phase 0: Foundation

Everything else depends on this. Phase 0 produces no user-facing product — it produces the infrastructure that Phases 1-4 build on.

### Deliverables

**Domain Model and Schema (doc 01)**
- PostgreSQL database with all foundational tables: `organization`, `human_user`, `agent`, `auth_session`, `api_key`, `audit_event`. No `org_membership` table — with db-per-org isolation, role lives directly on `human_user` (doc 04).
- Migration framework (versioned, forward-only migrations).
- Object storage abstraction (S3-compatible, with local filesystem fallback for development).

**Auth and Identity (doc 04)**
- Email + password authentication.
- Server-side session management with hashed tokens.
- API key authentication for programmatic access.
- Org isolation: every query scoped by `organization_id`.
- The principal convention (`principal_type` + `principal_id`) established on all mutation-capable tables.
- Audit event logging for security-sensitive actions.
- Rate limiting for login and API endpoints.

**Agent Identity (docs 04, 05)**
- Agent table with full profile shape: identity, classification, prompt pack, tool policy, model policy, memory policy.
- Agent lifecycle states (`draft`, `active`, `paused`, `retired`).
- Staff vs temp classification.
- `agent_project_assignment` table for project staffing.

**Model Gateway (doc 07)**
- Provider adapter interface with at least one implementation (Anthropic).
- Model profile abstraction: provider + model + settings + cost controls.
- Profile versioning and assignment hierarchy (flow node > agent > project > org).
- Global and per-provider concurrency management with priority queuing.
- Cost tracking: `model_invocation` record per call, `model_usage_rollup` daily aggregation.
- Retry with exponential backoff and fallback chain (up to 3 profiles deep).
- System profiles: summarization, listening eval, memory extraction.
- Error classification into canonical taxonomy.

**Event Bus (doc 12)**
- Domain event emission on create/update transitions.
- Durable event log table for replay and debugging.
- Realtime fanout to subscribed clients via SSE (default transport, doc 12). WebSocket available as secondary for bidirectional needs.
- Event subscriber registration for downstream consumers (notifications, memory pipeline, control plane).

**Prompt Assembly Pipeline (doc 05)**
- 7-layer pipeline: identity, policies, scope context, skills, memory, conversation history, tool descriptions.
- Budget management: reserve fixed layers, allocate remaining budget, fill, compress.
- Token counting and budget enforcement per layer.

**Control Plane Foundation (doc 16)**
- Capability model: namespaced permissions, policy evaluation (allow/deny).
- Policy layers: instance safety > org > project > agent profile.
- Execution broker skeleton: Run, RunStep, ToolExecution entities.
- Default-deny posture.

**Tool Framework (doc 20)**
- Tool registry with native tool definitions.
- Two-tier execution routing (tier 1 chat-layer, tier 2 control-plane).
- Four-stage resolution pipeline: universe > profile filter > flow node filter > capability gate.
- Tool call validation (name check, parameter validation, tier routing).

**Bootstrap Flow (doc 04)**
- CLI bootstrap command (`ottercamp bootstrap`).
- Creates org, first user (owner), starter trio (Frank, Lori, Ellie), General session.
- Idempotent: skipped if org already exists.

### Key Milestones

1. **Database boots**: migrations run, schema is up, bootstrap completes.
2. **Auth works**: human can log in, sessions are valid, API keys can be created.
3. **Model gateway routes**: a request can reach a model provider and return a response.
4. **Event bus fires**: domain events emit and can be consumed.
5. **Prompt assembly runs**: given an agent profile and context, the pipeline produces a complete prompt within token budget.

### Dependencies

- PostgreSQL must be available.
- At least one model provider API key (Anthropic recommended) must be configured.
- No external services beyond the model provider are required.

### Spec Dependencies

| Deliverable | Primary Spec | Supporting Specs |
|---|---|---|
| Schema + migrations | 01 | All (schema sections) |
| Auth + identity | 04 | 05 (agent identity) |
| Model gateway | 07 | 13 (cost controls) |
| Event bus | 12 | 01 (eventing model) |
| Prompt assembly | 05 | 02 (context assembly), 10 (skills layer) |
| Control plane | 16 | 20 (tool tiers), 04 (principal model) |
| Tool framework | 20 | 16 (capability gate), 02 (turn loop integration) |
| Bootstrap | 04 | 05 (starter trio seed data) |

---

## Phase 1: Synchronous Chat via TUI

The first usable product. A human can open the TUI and talk to agents in real-time.

### Deliverables

**TUI Application (doc 17)**
- Terminal-based interface built with Bubble Tea (Go) and the Charm ecosystem (doc 17).
- Real-time streaming of agent responses.
- Session sidebar: General session, future project/task sessions.
- Keyboard-driven navigation and input.
- Connects to the API service over HTTP/SSE.

**Chat System (doc 02)**
- Full chat session schema: `chat_session`, `chat_participant`, `chat_turn`, `chat_message`, `chat_artifact`, `chat_summary`.
- Session scoping: org scope implemented (project and task scopes ship in Phase 2).
- Sync mode: human sends message, agent responds, streaming.
- Turn cycle: Phase 1 (responder), Phase 2 (listening eval), Phase 3 (interjections).
- Rich content blocks: text, code, artifact references.
- Message states: pending, streaming, final, failed, redacted.
- Progressive summarization of conversation history.
- Cancel, steer, and message queue.
- Unread tracking with `chat_read_cursor`.
- Reactions (positive/negative with optional note).

**Starter Trio (doc 05)**
- Frank: active, org-level, Chief of Staff. Default responder in General session.
- Lori: active, org-level, Agent Relations Expert. Participant in General session.
- Ellie: active, org-level, Memory & Knowledge. Participant in General session.
- Full prompt packs for all three with identity prompts and policy addenda.
- Default tool policies per starter trio member (doc 20): Frank gets project/task/session tools; Lori gets agent management tools; Ellie gets memory and read tools.

**Basic Memory (doc 06, subset)**
- Ellie's passive injection: top-k memory retrieval injected into prompt layer 5 on every turn.
- Memory table and core schema (enough for basic operation — full 9-table schema can land incrementally).
- Implicit capture: Ellie subscribes to chat events and extracts memories.
- Explicit capture: @mention Ellie to remember something.
- `memory.query` tool available to all agents.
- Taxonomy bootstrapping: flat nodes created as memories arrive, structured later.
- No entity synthesis, dedup, or consolidation pipelines yet (Phase 2/3).

**Skills Foundation (doc 10)**
- Org-level skills repo created during bootstrap.
- Starter trio identity skills loaded.
- Org default skills (safety, communication, general work standards) loaded.
- Skills injected into prompt layer 4 during assembly.
- Skill activation rules operational (defaults always loaded, agent-level skills loaded in sync sessions).

**Model Provider (doc 07, subset)**
- Anthropic adapter fully functional.
- At least one default model profile (high-capability for agent turns).
- At least one system profile (Haiku-tier for listening evals and summarization).
- Streaming for sync sessions.
- Basic cost tracking (per-invocation recording).

**Notifications (doc 02, subset)**
- In-app notification center accessible from TUI.
- Urgent notifications delivered in real-time (escalations, failures).

### Key Milestones

1. **"Hello, Frank"**: the human opens the TUI, sees the General session, and talks to Frank. Frank responds with personality and context.
2. **Multi-agent conversation**: the human @mentions Lori in the General session. Lori responds. The listening eval runs. Frank may interject.
3. **Memory works**: the human tells Frank something ("I prefer short commit messages"). Later, the human asks Ellie, and Ellie remembers.
4. **Reactions work**: the human thumbs-up an agent response. Ellie captures the signal.
5. **Streaming works**: agent responses stream token-by-token in the TUI.

### Dependencies on Phase 0

Everything. Phase 1 cannot start until Phase 0 is complete. The chat system needs auth, the model gateway, the event bus, prompt assembly, and the tool framework.

### Spec Dependencies

| Deliverable | Primary Spec | Supporting Specs |
|---|---|---|
| TUI | 17 | 02 (chat events), 12 (realtime transport) |
| Chat system | 02 | 05 (prompt assembly), 07 (model calls), 12 (events) |
| Starter trio | 05 | 04 (agent identity), 10 (identity skills) |
| Basic memory | 06 | 02 (event subscription), 05 (prompt layer 5) |
| Skills | 10 | 05 (prompt layer 4), 03 (git repos) |
| Model provider | 07 | 13 (cost tracking) |

---

## Phase 2: Projects and Tasks

The system can manage work, not just chat. Agents work autonomously.

### Deliverables

**Project System (doc 03)**
- Full project schema: `project`, `project_task`, `project_subtask`, `project_task_participant`, `project_task_dependency`, `project_task_event`.
- Project creation through conversation with Frank.
- Project as git repo: one repo per project, `main` as source of truth.
- Project context block maintained by PM.

**Task System (doc 03)**
- Task lifecycle: `draft` > `queued` > `in_progress` > `blocked` > `review` > `done`.
- Task creation through agents (no UI creation path).
- `requires_human_review` flag for draft gating.
- Task numbering per project (`OC-1`, `OC-5.3` for subtasks).
- Flat task hierarchy with dependency DAG.
- Priority levels: urgent, high, normal, low.

**Flow Templates and Progression (doc 03)**
- `flow_template`, `flow_node`, `flow_node_execution` tables.
- Flow templates designed conversationally through PM.
- Node types: work, review. Actor types: role, project_manager, human, agent.
- Flow progression: explicit advancement via `flow.advance` tool.
- Rejection loops with visit counter and fresh execution records.
- Review nodes with approve/reject.

**Subtasks (doc 03)**
- `project_subtask` table scoped to flow node executions.
- Sequential execution within a node (shared task branch).
- Created by PM or worker agent.

**Task Scheduling (doc 03)**
- `task_schedule` table with cron expressions.
- Three overlap policies: skip, queue, replace.
- Max task duration for auto-cancel.

**Async Mode (doc 02)**
- Asynchronous sessions for autonomous agent work.
- System kick triggers async turns.
- Context reset and continuation messages.
- Per-node async sessions created and closed automatically.

**Project Staffing (doc 05)**
- PM assignment (one per project, enforced by schema).
- Worker and reviewer assignment via `agent_project_assignment`.
- Lori recommends staffing through conversation.
- Flow node actor resolution: role, project_manager, human, agent.

**Blocker Escalation (doc 03)**
- Blockers are tasks with dependency links.
- Escalation path: agent > PM > Frank > human.
- Cancelled dependencies auto-create resolution tasks.

**Scheduling and Queue (docs 03, 16)**
- Task scheduling queue with dependency-aware pickup.
- Sync priority over async work.
- Concurrency slot management.
- Resumption when blocked tasks become unblocked.

**Inbox (doc 03)**
- `inbox_item` table with five item types: task scoping review, task work review, draft action review, escalation, capability approval.
- Item lifecycle: pending, deferred, acted.
- PM nudges for deferred items.

**Merge Queue (doc 03)**
- `merge_queue_entry` table.
- Serial merge processing with conflict detection.
- Trivial conflicts auto-resolved; non-trivial escalated to PM.

**Skills Expansion (doc 10)**
- Project-level skills in project repos.
- Flow node skill declarations (`flow_node_skill` table).
- PM creates and manages skills through conversation.
- Full activation rules: flow node skills narrow the active set.

**Shipping and Delivery (doc 03a)**
- Two delivery patterns: during flow (Pattern 1) and after merge (Pattern 2).
- `project_remote` table for push configuration.
- Push as merge queue hook (auto or manual).
- Delivery modes: continuous, gated, scheduled.
- `project_environment` table for deployment tracking.

**Temp Agents (doc 05)**
- Temp agent creation via `agent.create_temp` tool.
- Scoped lifetime: task, session, or TTL.
- Auto-expiration and archival summary.
- Promotion to staff via Lori.
- Per-org concurrent limit (default 10).

**Proactive Supervision (doc 03)**
- PM monitors active runs for stuck agents.
- Stuck task detection (doc 16): heartbeat timeout, orphaned runs.
- PM intervenes or escalates.

**Memory Hardening (doc 06, subset)**
- Deduplication pipeline (semantic threshold + LLM cluster dedup).
- Entity synthesis: periodic consolidation of scattered facts into definitional memories.
- Contradiction detection at extraction time.
- Task-completion consolidation: scope promotion, distillation, execution summary.
- File-backed memories from project repos.

### Key Milestones

1. **"Create a project"**: the human tells Frank to create a project. Frank routes to Lori for staffing. PM is assigned. Project repo initialized.
2. **Task flow works**: PM scopes a task with acceptance criteria. Task queues. Agent picks it up, creates a branch, writes code, signals step done, flow advances to review.
3. **Autonomous execution**: the human goes away. Agents work tasks, file blockers, escalate through PM to Frank. Work progresses without human intervention.
4. **Review loop**: reviewer rejects work. Flow loops back. Agent reworks on the same branch. Reviewer approves. Task merges to main.
5. **Scheduled tasks**: PM sets up a recurring check. Tasks auto-create on cadence and execute.

### Dependencies on Phase 1

Chat must work. Agents must be able to take turns. Memory must be capturing. The model gateway must be routing. Without sync chat, there is no way to create projects or manage staffing.

---

## Phase 3: OtterCamp Builds Itself

Development moves inside OtterCamp. The system is building its own features.

### Deliverables

**Web UI (doc 18)**
- React + TypeScript SPA (doc 18) with full dashboard.
- Three-panel layout: session sidebar, main content, chat pane.
- Scope pill for zoom in/out (doc 02).
- Chat: org/project/task-scoped sessions, streaming, reactions, cancel/steer.
- Projects: task boards (kanban), task detail, flow visualization.
- Agents: directory, profiles, activity timelines.
- Inbox: action-required queue with inline context and one-click decisions.
- Merge queue visualization.
- Schedule management views.
- Activity feed per project.
- Command bar for keyboard-driven navigation.
- Dark mode by default.

**MCP Integration (doc 09)**
- MCP client layer: stdio and HTTP/SSE transport.
- Connection management (configured conversationally through Frank/PM).
- Tool discovery and catalog (`mcp_tool_catalog`).
- Default-deny: discovered tools disabled until admin enables.
- Secret management: runtime injection, never in agent prompts.
- Policy integration: per-connection and per-tool capability grants.
- Circuit breaker and health checks.
- Resource and prompt support.
- Starter connection templates for popular servers (GitHub, Slack, Postgres, filesystem, web search).
- `mcp_execution_log` for MCP-specific audit.

**System Integration (doc 11)**
- CLI execution: sandboxed shell commands in project workspaces.
- Browser automation: task-scoped browser sessions with reuse across runs within the same task (doc 11).
- Sandboxing: constrained filesystem, network, and resource access.
- Artifact capture: screenshots, logs, command output.
- Domain allowlists/denylists for browser.

**Full Control Plane (doc 16)**
- Complete execution lifecycle: Run > RunStep > ToolExecution.
- Sandboxing and isolation for CLI, browser, and MCP execution.
- Failure state repair: stuck task detection, queue drain, orphaned run recovery, blocker staleness alerts.
- Health heartbeat from running agents.
- Full observability: active/running/blocked runs, failure rates.

**Model Routing Expansion (doc 07)**
- OpenAI adapter, Google adapter, OpenAI-compatible adapter.
- Fallback chains across providers.
- Per-project and per-agent budget caps with soft/hard limits.
- Prompt capture and replay infrastructure.
- Deterministic mode for testing.
- Queue observability: depth, wait times, preemption events.

**Observability Stack (doc 13)**
- Structured logs with trace IDs across API > worker > model/tool calls.
- Metrics: latency, errors, queue depth, tool success rates, token usage.
- Operator dashboards (accessible conversationally through Frank and via web UI).
- Cost tracking dashboards: per-org, per-project, per-agent, per-model.
- Alerting thresholds for budget, error rate, queue depth.
- Tool usage analytics: per-agent, per-tool success rates and failure patterns (built on existing ToolExecution data).

**Memory Pipeline Hardening (doc 06)**
- Full 9-table schema operational.
- Sleep-time reflection: holistic pattern recognition during low-activity periods.
- Friction-triggered reflection: automatic when retrieval quality degrades.
- Taxonomy management: merge, prune, restructure nodes.
- File-backed memory freshness detection and re-extraction.
- Cross-encoder reranking on active (explicit) queries.
- Injection cooldown to prevent repetitive context.
- Attention-aware injection ordering.
- Memory feedback loop: reactions adjust confidence.
- Importer: CLI-only JSONL import (`ottercamp memory import`) for historical data.

**API Surface (doc 12)**
- REST endpoints for all domain entities.
- Realtime channel: SSE for server-to-client push, WebSocket for bidirectional (doc 12).
- Internal job API for async orchestration.
- Idempotency keys for mutation endpoints.
- Consistent error shape and codes.

### Key Milestones

1. **Web UI is usable**: the human can navigate projects, review tasks, chat with agents, and manage the inbox from a browser.
2. **MCP connections work**: an agent calls an external tool (GitHub, database, etc.) through MCP. Policy controls what is allowed.
3. **Agents write code**: system integration lets agents execute CLI commands, read/write files, and run tests in sandboxed workspaces.
4. **OtterCamp works on itself**: development tasks for OtterCamp features are managed within OtterCamp. Agents write code, reviewers review it, the merge queue handles integration.
5. **Full observability**: the operator sees cost breakdowns, error rates, queue health, and agent performance from the dashboard.

### Dependencies on Phase 2

Projects, tasks, and flows must work. Async execution must be functional. Without the task system, there is nothing for system integration to execute against. Without async mode, agents cannot work autonomously.

---

## Phase 4: Hardening and Distribution

Production readiness for external users and operators.

### Deliverables

**Security Hardening (doc 13)**
- Comprehensive security audit of all execution paths.
- Defense-in-depth review: auth, authorization, sandboxing, secret management, network isolation.
- Retention controls: configurable per org, automated expiry with object storage archival.
- Redaction pipeline for model transcripts and logs.
- Export/delete tooling for compliance workflows.
- Row-level security (RLS) in PostgreSQL for multi-tenant.

**Self-Host Packaging (doc 08)**
- Docker Compose configuration: API service, worker service, PostgreSQL, object storage.
- One-command bootstrap: `docker compose up` followed by `ottercamp bootstrap`.
- Versioned migration bundles for upgrades.
- Backup and restore playbook.
- Explicit upgrade guide with blue/green or rolling strategy.
- Environment-based configuration with secret injection.
- Minimum footprint documentation.

**Mobile UI (doc 19)**
- React Native for iOS and Android simultaneously (doc 19).
- Dashboard: project status at a glance, blocked items, progress summary.
- Push notifications: escalations, review requests, task completions.
- Quick actions: approve/reject inbox items, respond to escalations.
- Lightweight chat: quick questions to Frank.
- Biometric auth for sensitive operations.
- Deep links from notifications to specific tasks/sessions.

**Multi-Tenant Operations (doc 04, doc 08)**
- Managed hosting infrastructure: shared instance, many orgs.
- Org suspension and soft-delete.
- RLS enforcement at the database layer.
- Billing integration (external — not in core spec).
- SSO/OIDC support (Google, GitHub).
- Email-based password reset for managed deployments.
- Per-org provider credential isolation.

**Additional Providers (doc 07)**
- Complete adapter suite: Anthropic, OpenAI, Google, OpenAI-compatible.
- Local model support via OpenAI-compatible adapter (Ollama, vLLM, LM Studio).
- Regression suite UX for evaluating model upgrades.

**Migration Tools (doc 15)**
- CLI-only JSONL importer (`ottercamp memory import`) operational for historical data (via memory importer, doc 06).
- No CSV/JSON import for other data types — JSONL memory import is the only data bridge (doc 15, RD 2).
- Validation checklist: fresh install reproducible, permissions enforced, core workflows functional, audit logs active.

### Key Milestones

1. **Security audit passes**: all execution paths reviewed, no critical vulnerabilities.
2. **Self-host works end-to-end**: a new operator `docker compose up`, runs bootstrap, starts chatting with Frank, creates a project, and agents work tasks.
3. **Mobile notifications**: the operator gets a push notification when something needs attention. They approve a review from their phone.
4. **Multi-tenant isolation**: two orgs on the same instance cannot see each other's data. RLS enforced.
5. **Model flexibility**: the operator runs OtterCamp with local models only (no cloud API keys required) via OpenAI-compatible adapter.

### Dependencies on Phase 3

The full product must exist before hardening it. Security audit requires all execution paths to be implemented. Self-host packaging requires a stable API and schema. Mobile requires the API layer to be settled.

---

## Resolved Open Questions

All open questions from across the spec suite have been reviewed and resolved. This section preserves the decisions for reference. Questions are grouped by domain with the resolution noted.

### Architecture and Infra

1. ~~**Realtime transport**~~ **Resolved in doc 12**: SSE is the default for server-to-client push. WebSocket available as secondary transport for bidirectional needs (typing indicators, interactive tool sessions). Normal chat uses POST + SSE.

2. ~~**Kubernetes for managed hosting**~~ **Resolved**: deferred to Phase 4. The managed hosting orchestration choice depends on scale requirements that won't be clear until self-host is running with real demand. The architecture supports any orchestration layer.

3. ~~**Minimum self-host footprint**~~ **Resolved**: 4GB RAM / 2 vCPU / 40GB disk (~$20/mo VPS). Sufficient for single operator with a few concurrent projects. Document that heavier workloads (many async agents, large pgvector indexes) need 8GB+.

4. ~~**API version compatibility**~~ **Resolved**: clients bundled with server. The TUI ships as part of the server binary. The web UI is served as static assets from the server. Mobile app targets a minimum server version. No version skew problem for TUI/web — they always match the server.

### Auth and Identity

5. ~~**Password-less auth for self-hosted**~~ **Resolved**: auto-login for local mode. When `OTTERCAMP_MODE=local` (or equivalent), the system auto-authenticates as the bootstrap user. Auth infrastructure still exists (sessions, API keys, audit logging all work) — it just skips the credential prompt. VPS and managed modes always require auth.

6. ~~**Service account keys**~~ **Resolved**: deferred. For a single-operator product, the operator's API key works for CI/CD. Service accounts add schema complexity for a problem that doesn't exist yet. Add when multi-user orgs ship.

7. ~~**Doc 04 bootstrap sequence is stale**~~ **Resolved**: doc 14's Bootstrap Dataset section (10-step sequence) is the authoritative specification. Doc 04 defers to doc 14 for the complete sequence. Tracked as deferred item for doc 04's next review.

### Agents

8. ~~**Lori private_memory inconsistency**~~ **Resolved**: doc 05 is authoritative — all staff agents (including Lori) have private memory enabled. Doc 14's bootstrap dataset updated to match.

9. ~~**Starter trio profile updates on upgrade**~~ **Resolved**: automatic on startup with customization guard. On startup, the system checks a system profile version against the shipped version. If newer, it auto-applies updates to system agents (system_prompt, default tool policy, skills). Agent profiles have two instruction layers: `system_prompt` (shipped by OtterCamp, updated on upgrade) and `operator_instructions` (custom additions, never overwritten). The effective prompt is both combined. Operator customizations are always preserved.

### Control Plane

10. ~~**Default capability templates**~~ **Resolved in doc 16**: four templates — `reader` (read-only), `worker` (project mutations + file I/O + CLI + browser + memory), `deployer` (worker + external comms), `admin` (all capabilities). Starter trio gets `admin`. Most task-working agents get `worker`.

### System Integration

11. ~~**`system.browser.control` capability doesn't exist**~~ **Resolved**: doc 14's default org policy updated to reference the actual granular capabilities from doc 16.

12. ~~**Browser session isolation**~~ **Resolved in doc 11**: browser sessions are task-scoped with reuse across runs within the same task. Cleaned up when the task completes.

13. ~~**Minimum sandbox model**~~ **Resolved in doc 11**: process-level isolation with restricted working directory and environment. Container-level isolation is a future enhancement for managed/multi-tenant.

14. ~~**Default sensitive action policy**~~ **Resolved in docs 11 and 16**: CLI commands are risk-classified (`safe`, `normal`, `sensitive`, `dangerous`). Default posture is permissive. `sensitive` and `dangerous` denied by default, configurable via policy.

### MCP and External Tools

15. ~~**MCP sampling**~~ **Resolved**: not supported. External servers cannot trigger model calls through OtterCamp. If a server needs LLM capabilities, it brings its own model access. Eliminates trust, cost, and security risks entirely. Can be revisited later with strict guardrails if a compelling use case emerges.

16. ~~**Connection templates**~~ **Resolved**: ship 3-5 starter templates for popular servers (GitHub, Slack, Postgres, filesystem, web search). Templates are pre-filled connection configs — transport, URL pattern, required credentials, default tool enable list. Frank uses them to streamline setup. Community contributes more over time.

17. ~~**Multi-server tool disambiguation**~~ **Resolved**: connection prefix is sufficient. The slug prefix (`staging_db.run_query` vs `prod_db.run_query`) plus each connection's description in the tool catalog gives agents enough context. No additional routing machinery needed. If an agent picks wrong, the operator adds a note to project skills.

### Tools

18. ~~**Tool usage analytics**~~ **Resolved**: yes, built on existing data. Every tool call is logged in `ToolExecution` (doc 16) and `domain_event` (doc 12). Build aggregation views that surface tool success rates, call frequency, and failure patterns per agent. No new tables — queries and dashboard widgets in the observability stack (doc 13). Ship in Phase 3.

19. ~~**External tool schema change notification**~~ **Resolved**: validation error is sufficient. When a tool call fails due to schema mismatch, the agent gets the error and adapts. The next session discovers the new schema. No proactive notification machinery needed.

20. ~~**Bulk operations**~~ **Resolved**: no batch tools. Individual tool calls are fast enough (~200ms each). Batch operations complicate error handling and add API surface. If it becomes a bottleneck with real usage data, add batch variants then.

### Security and Observability

21. ~~**Compliance targets**~~ **Resolved**: none for launch, but design for it. Build with SOC 2 and GDPR principles in mind (audit logging, access controls, data retention/deletion, encryption). Self-host-first means the operator controls their own data, sidestepping many compliance questions. Pursue formal certification when enterprise demand materializes.

22. ~~**Default retention policy**~~ **Resolved**: tiered retention by data type. Memories: forever. Chat messages: 1 year. Chat summaries: forever. Model invocations: 90 days (rollups kept forever). Domain events: 90 days. Audit events: 1 year. Tool executions: 90 days. All configurable per org. Expired data archived to object storage, not deleted.

23. ~~**Cost limit behavior**~~ **Resolved in doc 13**: fail closed. Hard limits deny non-essential runs (`failure_reason = 'budget_exceeded'`).

24. ~~**Per-agent token budgets**~~ **Resolved**: tokens everywhere. Rename `budget_cap_cents` to `budget_cap_tokens` on the agent table. All three levels (org, project, agent) use tokens as the unit. Enforcement order: agent budget check → project budget check → org budget check (most restrictive wins, all independent). The UI shows dollar estimates calculated from model pricing data (display-only).

25. ~~**Alerting channels**~~ **Resolved**: post-launch via external tools. For launch, alerts go to the inbox and activity feed (plus mobile push in Phase 4). External alerting handled by the operator connecting a webhook via the external tool system. The event bus (doc 12) already emits the right events.

### Migration and Upgrades

26. ~~**V1 data imports**~~ **Resolved in doc 15**: JSONL memory import only, CLI-only, permanently available.

27. ~~**V1 archive retention**~~ **Resolved in doc 15**: indefinitely. Cold storage, not active infrastructure.

### UI — TUI (doc 17)

28. ~~**TUI framework**~~ **Resolved in doc 17**: Bubble Tea (Go) with Charm ecosystem.

29. ~~**TUI vs CLI relationship**~~ **Resolved in doc 17**: TUI is a mode of the `otter` binary.

30. ~~**Keybinding customization**~~ **Resolved**: `~/.config/ottercamp/keybindings.toml` with override support. Ship with vim-style and emacs-style presets plus a default preset.

31. ~~**Mouse support**~~ **Resolved**: optional mouse support enabled by default. Click sidebar items, scroll viewports, click messages. All mouse actions have keyboard equivalents. Mouse can be disabled via config.

32. ~~**TUI notification delivery**~~ **Resolved**: badge + subtle inline indicator. Notification count badge on inbox panel. Urgent events show a brief one-line indicator at the top of the current view ("Budget limit reached — press I for inbox"). Auto-dismisses after 5 seconds. Not a modal popup.

33. ~~**Copy/paste handling**~~ **Resolved**: standard system copy via terminal's native selection, plus a `y` key to programmatically copy the currently focused message or code block to the clipboard.

34. ~~**Terminal multiplexer compatibility**~~ **Resolved**: explicit test requirement. Test in tmux, screen, and common terminal emulators (iTerm2, Alacritty, Windows Terminal, kitty). Document known key conflicts and workarounds. Common setups must work at launch.

### UI — Web (doc 18)

35. ~~**Web UI framework**~~ **Resolved in doc 18**: React + TypeScript SPA.

36. ~~**Web UI rendering**~~ **Resolved in doc 18**: SPA, no SSR.

37. ~~**Agent avatars**~~ **Resolved**: pre-made avatars ship with each agent. Custom avatars stored alongside agent profiles. Operator can upload replacements.

38. ~~**Work log visualization**~~ **Resolved**: collapsible tree. Run > RunSteps > ToolExecutions. Collapsed by default, expand for detail.

39. ~~**Offline/degraded mode**~~ **Resolved**: banner + auto-reconnect with mutations disabled. Persistent "Connection lost" banner. Auto-reconnect with exponential backoff. Buttons grayed out while disconnected. On reconnect, fetch missed events via Last-Event-ID.

40. ~~**Browser notifications**~~ **Resolved**: no browser notifications. Rely on in-app notification system. Mobile push (Phase 4) handles the away-from-computer case.

41. **Panel proportions, transitions/animations, inline diff comments**: deferred to Figma/design phase. These are visual design decisions to be resolved during UI implementation with the Figma agent, not in the spec.

### UI — Mobile (doc 19)

42. ~~**Mobile platform**~~ **Resolved in doc 19**: iOS and Android simultaneously via React Native.

43. ~~**Mobile framework**~~ **Resolved in doc 19**: React Native.

44. **Tablet optimization, voice input, widget support, watch companion**: deferred to Figma/design phase. These are UX decisions to be resolved during mobile implementation, not in the spec.

### Testing (doc 21)

45. ~~**Live model tests in CI**~~ **Resolved**: yes, small subset on every merge. Run 2-3 smoke tests against a real provider on every merge (~30s, ~$0.01). Catches major API breakage immediately. Provider outage handling: if the live tests fail but all fixture tests pass, warn but don't block merge.

46. ~~**Browser test infrastructure**~~ **Resolved**: required only for browser-touching PRs. Browser E2E runs when code in browser integration, system integration, or tool execution paths is modified. Skipped for unrelated changes.

47. ~~**Test data freshness**~~ **Resolved**: manual re-recording with CI detection. When prompts change, the developer re-records affected fixtures locally with `OTTERCAMP_TEST_LIVE_MODELS=true`. CI detects stale fixtures (prompt hash mismatch) and fails with a clear message.

48. ~~**Performance regression testing**~~ **Resolved**: simple threshold alerts. Track total duration of each CI stage. Warn if a stage exceeds its time budget, fail if it exceeds 2x. Time budgets already defined in doc 21.

---

## Risk Areas

### Technically Hardest

1. **Prompt assembly budget management.** The 7-layer pipeline must produce good prompts within tight token budgets. Getting the allocation, truncation, and compression right across diverse session types (sync org chat, async code task, long-running review) is a tuning problem that will require iteration. Doc 05 defines the framework; the real challenge is parameter tuning.

2. **Memory retrieval quality at scale.** V1 experiments (doc 06) proved that entity synthesis, 1536d embeddings, and taxonomy pre-filtering work. But V1 had 13K memories from one user. V2 will accumulate memories much faster across many projects. Retrieval quality at 100K+ memories with complex taxonomy trees is unproven.

3. **Async agent reliability.** Agents working autonomously through multi-step flows must handle context limits, tool failures, ambiguous requirements, and environmental surprises. The checkpoint/continuation mechanism (doc 02), blocker escalation (doc 03), and failure state repair (doc 16) are the safety nets. Whether agents can reliably complete multi-hour tasks without going off the rails is the core product risk.

4. **Git merge conflict resolution.** The merge queue (doc 03) handles sequential merging. Parallel tasks on separate branches will occasionally conflict. Auto-resolution of trivial conflicts is tractable. Non-trivial conflicts require an agent to understand both changes and rebase correctly. This is hard for current models and may require human fallback more often than desired.

5. **MCP reliability at the edges.** MCP servers are external, diverse, and outside OtterCamp's control. The circuit breaker and health check infrastructure (doc 09) handles the common failure modes. But MCP servers with incorrect schemas, flaky behavior, or slow responses will produce agent errors that are hard to diagnose. The agent must be resilient to MCP failures without cascading into bad decisions.

### Plan Adjustment Risks

1. **Model provider API changes.** OtterCamp depends on model provider APIs. Provider API changes (rate limits, pricing, deprecations, new features) could force adapter updates at any time. The adapter pattern (doc 07) isolates this, but a major API change to the primary provider (Anthropic) could delay a phase.

2. **Context window economics.** The prompt assembly pipeline assumes generous context windows. If model costs increase or context windows shrink, the budget allocation strategy may need rework. The system currently trusts that context windows will grow, not shrink.

3. **Agent capability vs expectations.** OtterCamp's design assumes agents can reliably execute multi-step tasks with tool use. If foundation model capabilities plateau or regress in specific areas (code generation, long-horizon planning, tool use accuracy), the product promise weakens. The flow system's human review gates (doc 03) are the mitigation, but if agents need too much human intervention, the value proposition suffers.

4. **Self-host complexity.** Docker Compose is the packaging target (doc 08), but PostgreSQL + object storage + model provider credentials + git repos is already meaningful operational overhead. If self-hosting proves too complex for the target audience, we may need to ship a more opinionated, simplified deployment (single binary with embedded database, for example).

5. **Memory system bootstrapping.** On a fresh install, Ellie has no memories, no taxonomy, and no entity definitions. The system self-bootstraps (doc 06), but early conversations will have poor memory retrieval until enough data accumulates. The bootstrap dataset (default skills, agent profiles) provides structure, but rich memory takes time to build.

### What Could Go Wrong

- **Agents loop without progress.** The proactive supervision mechanism (doc 03) and stuck task detection (doc 16) mitigate this, but if it happens frequently, it destroys trust in the autonomous work promise.
- **Cost overruns.** Autonomous agents can consume tokens rapidly. Budget caps (doc 07) and per-agent limits are the guardrails. But a misconfigured flow with expensive models and long turns could produce a surprising bill before the soft limit fires.
- **Memory pollution.** If the extraction pipeline (doc 06) captures too much noise, retrieval quality degrades. The quality gates, scoring thresholds, and garbage pattern rejection are defenses, but they need tuning with real data.
- **Bootstrap friction.** If the first-run experience requires too many configuration steps before the human can talk to Frank, adoption suffers. The bootstrap must be minimal: provide email, password, and API key. Everything else should have defaults.

---

## Bootstrap Dataset

Everything that ships in the box when OtterCamp is installed fresh. The human should be able to bootstrap, open the TUI, and start working within minutes.

### Starter Trio Agent Profiles

Created during bootstrap (doc 04), immediately active (no draft review). Full specifications in doc 05.

**Frank (Chief of Staff)**
- Slug: `frank`, pronouns: he/him, scope: org
- System prompt: organizational strategist, primary human touchpoint, cross-project coordinator, escalation handler, warm but direct
- Tool policy: project, task, subtask, flow, session, memory, agent, inbox, schedule tools. No system/browser/external tools.
- Model: org's high-capability profile
- Private memory: enabled
- Identity skill: `skills/identities/frank.md`

**Lori (Agent Relations Expert)**
- Slug: `lori`, pronouns: she/her, scope: org
- System prompt: staffing expert, agent creator, workforce manager, thoughtful and precise
- Tool policy: agent management, project/task read, memory.query
- Model: org's high-capability profile
- Private memory: enabled (default for all staff — doc 05)
- Identity skill: `skills/identities/lori.md`

**Ellie (Memory & Knowledge)**
- Slug: `ellie`, pronouns: she/her, scope: org
- System prompt: memory system, knowledge keeper, dual role (passive infrastructure + active conversational), precise, source-aware, transparent about confidence
- Tool policy: memory tools, session history, file read/search
- Model: standard profile for conversation, Haiku-class for pipeline work
- Private memory: enabled
- Identity skill: `skills/identities/ellie.md`

### Default Skills

Stored in the org-level skills repository (created during bootstrap, doc 10).

**Identity Skills (agent-level, not org defaults)**
- `skills/identities/frank.md` — Frank's identity, responsibilities, communication style
- `skills/identities/lori.md` — Lori's identity, responsibilities, communication style
- `skills/identities/ellie.md` — Ellie's identity, responsibilities, conversational capabilities
- `skills/identities/pm.md` — Default PM identity: opinionated planning, task scoping, proactive supervision, flow design

**Org Default Skills (always active for all agents)**
- `skills/safety-and-communication.md` — sensitive information handling, escalation behavior, external communication boundaries
- `skills/general-work-standards.md` — commit message conventions, handling ambiguity, blocker vs workaround, signaling done vs help

**Project Template Skills (available but not pre-installed)**
- `skills/templates/code-review-checklist.md`
- `skills/templates/go-coding-standards.md`
- `skills/templates/typescript-coding-standards.md`
- `skills/templates/api-design-conventions.md`
- `skills/templates/content-writing-guidelines.md`
- `skills/templates/security-review-checklist.md`

### Default Model Profiles

Seeded during bootstrap (doc 07). The human must provide at least one provider API key.

**High-Capability Profile** (default for Frank, Lori, PMs)
- Provider: Anthropic, model: Claude Opus (or current best)
- Temperature: 0.7, tool use enabled, streaming enabled
- Per-call timeout: 60s (sync), 120s (async)
- Assigned as org default

**Standard Profile** (default for workers, reviewers)
- Provider: Anthropic, model: Claude Sonnet (or current mid-tier)
- Temperature: 0.7, tool use enabled, streaming enabled
- Per-call timeout: 60s (sync), 120s (async)
- Fallback: high-capability profile

**Haiku Profile** (system operations)
- Provider: Anthropic, model: Claude Haiku (or current fast/cheap tier)
- Temperature: 0.3, tool use disabled, streaming disabled
- Per-call timeout: 45s
- Used for: listening evals, summarization, memory extraction, memory synthesis

### Default Flow Templates

System-provided templates (null `project_id` on `flow_template`, doc 03). Available for PMs to assign to tasks.

**Single Step** (simplest flow)
```
[Work] -> [Done]
```
- One work node, actor type: role (worker). No review.

**Work + Review** (standard for most implementation tasks)
```
[Work] -> [Review] -> [Done]
         <- (reject)
```
- Work node (role: worker) -> review node (role: reviewer, reject loops back to work).

**Work + Code Review + Human Review** (for sensitive changes)
```
[Work] -> [Code Review] -> [Human Review] -> [Done]
          <- (reject)      <- (reject)
```
- Work -> agent code review -> human review gate.

**Research** (for exploration and analysis tasks)
```
[Research] -> [Done]
```
- Single work node with no review. For tasks where the deliverable is knowledge, not code.

### Default Policies

**Instance Safety Policy** (baked in, not configurable)
- Cannot delete the organization.
- Cannot modify system tables directly.
- Cannot exfiltrate secrets via tool calls.
- Cannot bypass authentication.

**Default Org Policy** (configurable by human)
- Communication tools (`email.compose`, `slack.post`) are allowed — they create drafts as their designed tool behavior (doc 20).
- CLI execution requires `system.cli.execute` capability grant.
- Browser control requires granular capability grants: `system.browser.navigate`, `system.browser.interact`, `system.browser.screenshot`, `system.browser.extract` (doc 16).
- External tools (MCP and remote APIs) require per-connection capability grants (doc 20).
- Max tool calls per turn: 50.
- Max turn duration: 5 minutes (sync), 30 minutes (async).

### General Session

Created during bootstrap (doc 04). The persistent org-level chat session.

- Title: "General"
- Scope: org
- Mode: sync
- Participants: human (owner), Frank (default_responder), Lori (participant), Ellie (listener)

### Bootstrap Sequence Summary

```
1. Run database migrations
2. Create organization (name from CLI args or env)
3. Create first human user (email/password from CLI args or env) as org owner
4. Create org-level skills repo and populate with default skills
5. Seed model profiles (high-capability, standard, haiku)
6. Seed default flow templates
7. Seed starter trio agents (Frank, Lori, Ellie) with profiles, skills, and tool policies
8. Create General session with participants
9. Seed default org policy
10. Record bootstrap audit event
```

All steps are idempotent. If bootstrap has already run (detected by existence of any organization row), the sequence is skipped.

---

## Spec Status for Build Planning

17 of 22 specs are finished (first-principles reviewed, all open questions resolved within each spec). The remaining specs:

- **01 — Architecture and Domain**: in process. Finalize domain model, runtime component boundaries, eventing contracts.
- **14 — Open Questions and Phasing**: this document. Will be finalized last after all open questions above are resolved.
- **17 — TUI**: initial draft. Bubble Tea framework confirmed. UX details to resolve during implementation.
- **18 — Web UI**: initial draft. React + TypeScript SPA confirmed. UX details to resolve during implementation.
- **19 — Mobile UI**: initial draft. React Native confirmed. UX details to resolve during implementation.

The UI specs (17, 18, 19) are intentionally less detailed than the backend specs. Their open questions are largely UX/implementation decisions that should be resolved during their respective build phases, not upfront.
