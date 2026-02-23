# Build Progress — Spec Reading Notes

## Iteration 1 Status: COMPLETE

Docs read: 01, 02, 03, 03a, 04, 05, 14

---

## Doc 01 — Architecture and Domain Model
**Status:** In Process (reference only)

**Key decisions:**
- Modular monolith, NOT microservices (two processes: API service + worker)
- Storage: Postgres (transactional), object store (artifacts), optional local filesystem
- Domain events: emitted on create/update, stored in durable log, fanned out realtime

**Domain entities named:**
Organization, HumanUser, Agent, Session (chat), Message, Project, ProjectTask, FlowTemplate, FlowNode, FlowNodeExecution, Memory, ModelProfile, ProviderConnection, Skill, ToolExecution, McpConnection, AgentProfileTemplate, AuditEvent

**Domain boundaries:**
- Chat: sessions, messages, participants
- Project: tasks, flow, approvals
- Agent: identity, lifecycle, capabilities
- Memory: extraction, retrieval, retention
- Model (infra): provider abstraction, routing, cost
- Control Plane (infra): runs, tool executions, policies, worker dispatch

---

## Doc 02 — Chat Spec
**Status:** Finished

**Tables:**
- `chat_session` → organization(id), scope_type+scope_id polymorphic (org/project/task)
- `chat_participant` → chat_session(id), polymorphic participant (human/agent)
- `chat_turn` → chat_session(id), cycle_id (UUID grouping)
- `chat_message` → chat_session(id), chat_turn(id), polymorphic author (human/agent)
- `chat_artifact` → chat_session(id), chat_message(id)
- `chat_summary` → chat_session(id)
- `chat_read_cursor` → chat_session(id), human_user(id) [PK: composite]
- `chat_message_reaction` → chat_message(id), polymorphic reactor (human/agent)

**Key behavioral rules:**
- One session per scope (one org session "General", one per project, one per task for sync)
- Per-node async sessions auto-created for flow node execution (work logs)
- Message states: pending → streaming → final → failed | redacted (append-only)
- Turn cycle: Phase 1 (responder) → Phase 1.5 (agent @mentions, one round) → Phase 2 (listening eval) → Phase 3 (interjections)
- Two-tier tool execution: Tier 1 (read-only, chat layer) vs Tier 2 (mutations/external, control plane)
- Stop conditions: max tool calls per turn, max turn duration (soft enforcement at loop boundaries)
- Progressive summarization: threshold ~50-60% of layer 6 budget, summarize oldest ~25-30%
- Reactions: one per participant per message, positive/negative, feeds Ellie memory pipeline
- Cancel is explicit (Escape/Cancel button), queued messages are serialized

**FK relationships:**
- chat_session.scope_id → organization.id | project.id | project_task.id (polymorphic via scope_type)
- chat_session.created_by_id → human_user.id | agent.id | sentinel UUID
- chat_participant.participant_id → human_user.id | agent.id (via participant_type)
- chat_turn.session_id → chat_session.id
- chat_message.session_id → chat_session.id; chat_message.turn_id → chat_turn.id
- chat_message.author_id → human_user.id | agent.id (via author_type)
- chat_artifact.session_id → chat_session.id; chat_artifact.message_id → chat_message.id
- chat_summary.session_id → chat_session.id
- chat_read_cursor.session_id → chat_session.id; chat_read_cursor.user_id → human_user.id
- chat_message_reaction.message_id → chat_message.id; reactor_id → human_user.id | agent.id

**Realtime events:**
- chat.turn.started, chat.message.created, chat.message.delta, chat.message.finalized
- chat.turn.completed, chat.listening_eval.completed

**API endpoints implied:**
- POST /sessions, GET /sessions/:id/messages, POST /sessions/:id/messages
- SSE /sessions/:id/events
- POST /sessions/:id/turns/:id/cancel
- POST /sessions/:id/messages/:id/react

---

## Doc 03 — Projects and Task Flow
**Status:** Finished

**Tables:**
- `project` → organization(id)
- `flow_template` → project(id) [nullable for system templates]
- `flow_node` → flow_template(id); self-refs next_node_id, reject_node_id
- `project_task` → project(id), flow_template(id), flow_node(id) [current], task_schedule(id)
- `flow_node_execution` → project_task(id), flow_node(id), chat_session(id)
- `project_subtask` → project_task(id), flow_node_execution(id)
- `project_task_dependency` → polymorphic source/depends_on (task|subtask)
- `project_task_participant` → project_task(id), polymorphic participant (agent|human)
- `task_schedule` → project(id), flow_template(id)
- `inbox_item` → organization(id), human_user(id), project(id) [nullable], project_task(id) [nullable]
- `merge_queue_entry` → project(id), project_task(id)
- `project_task_event` → project_task(id), flow_node(id)

**Key behavioral rules:**
- Tasks always created through agents/chat, never UI
- Task states: draft → queued → in_progress → blocked → on_hold → review → done | cancelled
- `requires_human_review` flag gates draft→queued
- Flow templates are immutable once a task uses them (new version = new row)
- Flow progression always explicit (agent signals "step done")
- Subtasks scoped to flow_node_execution (not tasks), run sequentially, one level deep
- Dependencies form a DAG (no cycles), same-level only (task→task or subtask→subtask)
- Blockers are tasks with dependency links (no separate blocker entity)
- Inbox = action-required queue, distinct from notifications
- Merge queue serializes merges to main, archived_at on merge_queue_entry

**FK relationships:**
- project.organization_id → organization.id
- flow_template.project_id → project.id (nullable)
- flow_node.flow_template_id → flow_template.id; next_node_id, reject_node_id → flow_node.id
- project_task.project_id → project.id; flow_template_id → flow_template.id; current_flow_node_id → flow_node.id; schedule_id → task_schedule.id
- flow_node_execution.task_id → project_task.id; flow_node_id → flow_node.id; session_id → chat_session.id
- project_subtask.task_id → project_task.id; flow_node_execution_id → flow_node_execution.id
- project_task_dependency: polymorphic source/depends_on
- project_task_participant.task_id → project_task.id; polymorphic participant_id
- task_schedule.project_id → project.id; flow_template_id → flow_template.id
- inbox_item.organization_id → organization.id; target_user_id → human_user.id; source_project_id → project.id; source_task_id → project_task.id
- merge_queue_entry.project_id → project.id; task_id → project_task.id
- project_task_event.task_id → project_task.id; flow_node_id → flow_node.id

---

## Doc 03a — Shipping and Delivery
**Status:** Finished (Draft)

**Tables (new):**
- `project_remote` → project(id)
- `project_environment` → project(id), project_task(id) [deploy_task_id, nullable]

**Columns added to project:**
- `delivery_mode` (continuous|gated|scheduled, nullable)
- `deploy_flow_template_id` → flow_template(id)

**Event types added to project_task_event:**
- push_succeeded, push_failed, deployed

**Key behavioral rules:**
- Pattern 1: delivery inside task flow (delivery node in flow template)
- Pattern 2: separate deploy task after merge (triggered by merge events)
- Push is infrastructure (merge queue hook), not a task
- 3 delivery modes: continuous (skip if deploy running), gated (human triggers), scheduled
- Environments optional, ordered, track current + previous commit SHA
- Rollback = new deploy task targeting previous commit (not a state transition)
- PostgreSQL advisory lock for deploy task creation (prevent race condition)
- merge_queue_entry rows never hard-deleted; archived_at set on deploy completion

---

## Doc 04 — Auth, Tenancy, and Identity
**Status:** Finished

**Tables:**
- `organization` (root entity, no FKs — one per database)
- `human_user` → organization(id)
- `auth_session` → human_user(id)
- `api_key` → human_user(id)
- `audit_event` → organization(id), polymorphic principal (human/agent/system)

**Key behavioral rules:**
- Database-per-org isolation (NOT row-level security)
- organization_id on tables is for referential integrity, not tenant isolation
- Two identity types: human_user and agent (both first-class principals)
- Principal convention: (principal_type + principal_id) — NOT a separate table
- System actor: actor_type='system', actor_id=sentinel UUID (00000000-...)
- V2 GA: one human per org (owner), multi-user near-term via RBAC roles
- Email+password auth; bcrypt work factor 12; 30-day sliding sessions
- API keys: `oc_<scope>_<random>`, SHA-256 hash stored, scopes: full/read/chat
- SSO/OIDC deferred; schema hooks exist (external_auth_provider, external_auth_id)
- Bootstrap is idempotent (skip if org exists)
- Bootstrap creates: org → owner user → starter trio → model profiles → General session

**FK relationships:**
- human_user.organization_id → organization.id
- auth_session.user_id → human_user.id
- api_key.user_id → human_user.id
- audit_event.organization_id → organization.id; principal_id → human_user.id | agent.id | sentinel

---

## Doc 05 — Agents: Staff and Temps
**Status:** Finished

**Tables:**
- `agent` → organization(id); self-ref promoted_to_agent_id
- `agent_project_assignment` → agent(id), project(id)
- `agent_skill_attachment` → agent(id), skill(id)
- `agent_profile_template` → organization(id) [nullable for system templates]

**Key behavioral rules:**
- Two agent classes: staff (durable, cross-project memory) vs temp (scoped, single-project)
- Staff lifecycle: draft → active → paused → retired → cancelled; temp: active → expired → promoted
- Temps skip draft review, created immediately active
- Concurrent temp limit: configurable per org, default 10
- PM: always staff, one per project (enforced by partial unique index), private_memory_enabled=true
- Temp scope types: project, task, session, ttl
- 7-layer prompt assembly: identity → policies → scope context → skills → memory → history → tools
- Layers 1-2 never cut; layers 3-7 budget-dependent
- Private memory: PMs, Frank, Lori, Ellie → true; all other staff and all temps → false
- Starter trio: Frank, Lori, Ellie — system-created, immediately active, org-level
- Agent profile catalog: 230+ templates from V1 (agent_profile_template table)

**FK relationships:**
- agent.organization_id → organization.id
- agent.promoted_to_agent_id → agent.id
- agent.default_model_profile_id → model_profile.logical_profile_id
- agent_project_assignment.agent_id → agent.id; project_id → project.id
- agent_skill_attachment.agent_id → agent.id; skill_id → skill.id
- agent_profile_template.organization_id → organization.id (nullable); source_agent_id → agent.id

**Note:** agent table is authoritative here; doc 04 references it as principal type only

---

## Doc 14 — Open Questions and Build Phasing
**Status:** Finished (reference only)

**Key resolved decisions (relevant to build):**
- Build phases: 0 (Foundation) → 1 (Sync Chat + TUI) → 2 (Projects + Tasks) → 3 (Self-Building) → 4 (Hardening)
- Sprint 1 = Phase 0 + Phase 1 + Phase 2 + Phase 3 API surface (minus UI)
- Default capability templates: reader, worker, deployer, admin (starter trio gets admin)
- Retention: memories forever, chat 1 year, invocations 90 days, domain events 90 days, audit 1 year
- Budget unit: TOKENS (not cents) — `budget_cap_tokens` on agent table
- Bootstrap sequence: 10 idempotent steps
- Default org policy: communication tools allowed (drafts), CLI needs capability, browser needs granular caps
- SSE is default realtime transport; WebSocket available for bidirectional
- Auto-login for local mode (`OTTERCAMP_MODE=local` or `OTTERCAMP_AUTH_MODE=local`)
- Starter trio profile updates: automatic on startup, `operator_instructions` field never overwritten
- Haiku profile: listening evals, summarization, memory extraction, synthesis

**ISSUE FOUND:**
- Doc 05 defines `budget_cap_cents` on agent table; Doc 14 resolution #24 says rename to `budget_cap_tokens`. Need to reconcile.

---

## Running Table Catalog (after Iteration 1)

### Auth & Tenancy (doc 04)
- organization
- human_user → organization
- auth_session → human_user
- api_key → human_user
- audit_event → organization, polymorphic principal

### Agent Identity (doc 05)
- agent → organization; self-ref promoted_to_agent_id
- agent_project_assignment → agent, project
- agent_skill_attachment → agent, skill
- agent_profile_template → organization (nullable), agent (source_agent_id, nullable)

### Chat (doc 02)
- chat_session → organization, polymorphic scope (org/project/task)
- chat_participant → chat_session, polymorphic participant (human/agent)
- chat_turn → chat_session
- chat_message → chat_session, chat_turn, polymorphic author (human/agent)
- chat_artifact → chat_session, chat_message
- chat_summary → chat_session
- chat_read_cursor → chat_session, human_user [composite PK]
- chat_message_reaction → chat_message, polymorphic reactor (human/agent)

### Project & Task Flow (doc 03)
- project → organization
- flow_template → project (nullable for system templates)
- flow_node → flow_template; self-refs (next_node_id, reject_node_id)
- project_task → project, flow_template, flow_node (current), task_schedule
- flow_node_execution → project_task, flow_node, chat_session
- project_subtask → project_task, flow_node_execution
- project_task_dependency → polymorphic (task|subtask)
- project_task_participant → project_task, polymorphic (agent|human)
- task_schedule → project, flow_template
- inbox_item → organization, human_user, project, project_task
- merge_queue_entry → project, project_task
- project_task_event → project_task, flow_node

### Shipping & Delivery (doc 03a)
- project_remote → project
- project_environment → project, project_task (deploy_task_id)
[additions to project: delivery_mode, deploy_flow_template_id]

### Not yet read: memory, model, deployment, MCP, skills, API, security, migration, control plane, tools, testing

---

## Cross-References Spotted

1. chat_session.scope_id → project.id | project_task.id (polymorphic)
2. flow_node_execution.session_id → chat_session.id (per-node async sessions)
3. project_task.current_flow_node_id → flow_node.id
4. inbox_item.item_type includes 'browser_handoff' (referenced in doc 11?)
5. agent.default_model_profile_id → model_profile (doc 07 to define)
6. agent_skill_attachment.skill_id → skill (doc 10 to define)
7. flow_node.mcp_tools jsonb (doc 09 to define)
8. flow_node.actor_id → agent.id (when actor_type='agent')
9. project_task_event includes 'push_succeeded', 'push_failed', 'deployed' (from doc 03a)
10. Control plane run/run_step → chat_turn (link in doc 16)

---

## Iteration 2 Status: PENDING

## Iteration 3 Status: PENDING
