# Spec Reading Progress

## Iteration 1 Status: COMPLETE

Docs read: 01, 02, 03, 03a, 04, 05, 14

---

## Running Table Catalog

### Auth & Tenancy (doc 04)
- `organization` — auth/tenancy (doc 04)
- `human_user` — auth/tenancy (doc 04)
- `auth_session` — auth/tenancy (doc 04)
- `api_key` — auth/tenancy (doc 04)
- `audit_event` — auth/tenancy (doc 04)

### Agent Identity (doc 05)
- `agent` — agents (doc 05) ← authoritative definition
- `agent_project_assignment` — agents (doc 05)
- `agent_skill_attachment` — agents (doc 05)
- `agent_profile_template` — agents (doc 05)

### Chat (doc 02)
- `chat_session` — chat (doc 02)
- `chat_participant` — chat (doc 02)
- `chat_turn` — chat (doc 02)
- `chat_message` — chat (doc 02)
- `chat_artifact` — chat (doc 02)
- `chat_summary` — chat (doc 02)
- `chat_read_cursor` — chat (doc 02)
- `chat_message_reaction` — chat (doc 02)

### Project & Task Flow (doc 03)
- `project` — projects (doc 03)
- `flow_template` — projects (doc 03)
- `flow_node` — projects (doc 03)
- `project_task` — projects (doc 03)
- `flow_node_execution` — projects (doc 03)
- `project_subtask` — projects (doc 03)
- `project_task_dependency` — projects (doc 03)
- `project_task_participant` — projects (doc 03)
- `task_schedule` — projects (doc 03)
- `inbox_item` — projects (doc 03)
- `merge_queue_entry` — projects (doc 03)
- `project_task_event` — projects (doc 03)

### Shipping & Delivery (doc 03a)
- `project_remote` — shipping (doc 03a)
- `project_environment` — shipping (doc 03a)
- [2 columns added to `project`: `delivery_mode`, `deploy_flow_template_id`]

### Memory (doc 06)
- `memory` — core memory record with pgvector embedding, scope columns, supersession chain (doc 06)
- `memory_taxonomy_node` — global taxonomy tree nodes (doc 06)
- `memory_taxonomy_tag` — many-to-many: memories ↔ taxonomy nodes (doc 06)
- `memory_entity` — known entities (person/project/tool/concept/organization) (doc 06)
- `memory_entity_mention` — many-to-many: memories ↔ entities (doc 06)
- `memory_source` — provenance records linking memories to source events/messages (doc 06)
- `memory_dedup_reviewed` — tracks reviewed dedup pairs to avoid rework (doc 06)
- `memory_compaction_run` — tracks consolidation/compaction job runs (doc 06)
- `memory_import` — tracks bulk import jobs (doc 06)

### Models & Inference (doc 07)
- `model_provider` — instance-level provider registration (doc 07)
- `provider_connection` — per-org connection with health tracking and failover (doc 07)
- `model_profile` — versioned profile with stable logical_profile_id (doc 07)
- `model_profile_assignment` — hierarchy-based profile assignments (scope: org|project|agent|flow_node) (doc 07)
- `model_invocation` — per-call record with full attribution, token counts, retry/fallback chain (doc 07)
- `model_usage_rollup` — pre-computed daily aggregations by connection/model/agent/project (doc 07)

### Deployment & Secrets (doc 08)
- `secret` — encrypted secret storage; organization_id, name, slug (unique within org), category (model_provider|ssh_key|mcp_credential|external_service), encrypted_value bytea, nonce bytea, key_version int, created_by_type+created_by_id, last_rotated_at, expires_at, metadata jsonb (doc 08)

### MCP Integration (doc 09)
- `mcp_connection` — org or project-scoped connection config, transport, health, circuit breaker (doc 09)
- `mcp_tool_catalog` — discovered tools/resources/prompts with enablement flags (doc 09)
- `mcp_execution_log` — full audit trail of every MCP interaction (doc 09)
- `mcp_secret_binding` — maps secret references (slugs) to injection points for a connection (doc 09)

### Skills (doc 10)
- `skill` — registry of skill files (pointer to file_path in git repo; content NOT in DB) (doc 10)
- `agent_skill_attachment` — links agents to profile-level skills with priority ordering; `purpose='identity'` recognized by activation algorithm (doc 10) ← this was already referenced in doc 05; now fully defined
- `flow_node_skill` — links flow nodes to required skills; replaces flow_node.skills jsonb column (doc 10) ← confirmed for ISSUE #6

### Agent Control Plane (doc 16)
- `run` — one end-to-end unit of work; principal, capability, policy decision, status (doc 16)
- `run_step` — one stage inside a run; sequential; unique(run_id, step_number) (doc 16)
- `run_attempt` — retry envelope for a step; trigger (initial|transient_failure|manual_retry); unique(run_step_id, attempt_number) (doc 16) ← ISSUE #12 RESOLVED
- `tool_execution` — per-tier-2-tool-call record with policy decision; links to run/run_step/run_attempt (doc 16) ← ISSUE #14 RESOLVED
- `run_artifact` — files/screenshots/logs in object storage; links to run/run_step/run_attempt (doc 16)
- `run_event` — append-only timeline; event_type includes heartbeat; unique(run_id, sequence) (doc 16)
- `capability_policy` — declarative policy rules (allow|deny) at instance|org|project|agent_profile layers (doc 16)

### CLI and Browser (doc 11)
- `cli_execution` — CLI command lifecycle with risk classification and output capture (doc 11)
- `browser_session` — task-scoped browser context, persists across runs within a task (doc 11)
- `browser_action` — fine-grained record of every browser interaction (doc 11)
- `browser_handoff` — human handoff lifecycle bridging browser domain and inbox (doc 11)

### Events and Jobs (doc 12)
- `domain_event` — durable event log with seq-based total ordering; actor_type includes 'supervisor' extension (doc 12)
- `consumer_cursor` — per-consumer event position tracking (doc 12)
- `job_queue` — PostgreSQL-backed async job queue with priority, retry, dead-letter (doc 12)
- `idempotency_key` — mutation replay safety; 24-hour TTL; org-scoped via db-per-org (doc 12)

### Security, Observability, Cost (doc 13)
- `token_budget` — org or project-level token budget with soft/hard limits and period type (doc 13)
- `trace_span` — distributed trace spans, partitioned by day, 7-day retention (doc 13, schema implied not DDL-defined)

### Tools (doc 20)
- `tool_definition` — native tool registry populated at startup; tier (1 or 2), category, domain, required_capability (doc 20)
- `session_tool_set` — per-agent, per-session cached tool resolution; tool_names text[], mcp_tools jsonb (doc 20)

### No new tables from docs 17, 18, 19, 21
All tables now covered in running catalog above.

---

## Doc 01: Architecture and Domain

**Tables:** none (domain naming only — no DDL in this doc)
**FKs:** none
**Polymorphic patterns:** none defined here
**Key behavioral rules:**
- Modular monolith, NOT microservices. Two runtime processes: API service (HTTP + WS/SSE) and worker process (background jobs), same codebase.
- Storage: Postgres (transactional), object store (artifacts), optional local filesystem for single-node.
- Domain events: emitted on entity transitions, stored in durable event log, fanned out realtime.
- V2 is a clean-room rebuild — no V1 runtime code, schema, or data reused.
- Six strong domain boundaries: Chat, Project, Agent, Memory (product), Model, Control Plane (infrastructure).
**API endpoints:** none defined here
**Notes:**
- Canonical entities named: Organization, HumanUser, Agent, Session, Message, Project, ProjectTask, FlowTemplate, FlowNode, FlowNodeExecution, Memory, ModelProfile, ProviderConnection, Skill, ToolExecution, McpConnection, AgentProfileTemplate, AuditEvent
- Future service split triggers: scaling asymmetry, release coupling, reliability isolation, repeated incidents

---

## Doc 02: Chat

**Tables:**
- `chat_session` — scope_type+scope_id polymorphic (org/project/task); created_by_type+created_by_id
- `chat_participant` — participant_type+participant_id polymorphic (human/agent); roles: default_responder, participant, listener
- `chat_turn` — trigger, responding_type (always 'agent'), cycle_id groups turns per human message
- `chat_message` — role, author_type+author_id polymorphic (human/agent); nullable for system/tool_result
- `chat_artifact` — object storage reference
- `chat_summary` — from_sequence to to_sequence range
- `chat_read_cursor` — composite PK (session_id, user_id)
- `chat_message_reaction` — reactor_type+reactor_id polymorphic (human/agent); unique (message_id, reactor_type, reactor_id)

**FKs:**
- `chat_session.organization_id` → `organization.id`
- `chat_session.scope_id` → `organization.id` | `project.id` | `project_task.id` (via scope_type)
- `chat_session.created_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via created_by_type)
- `chat_participant.session_id` → `chat_session.id`
- `chat_participant.participant_id` → `human_user.id` | `agent.id` (via participant_type)
- `chat_turn.session_id` → `chat_session.id`
- `chat_message.session_id` → `chat_session.id`
- `chat_message.turn_id` → `chat_turn.id` (nullable — null for pending queued messages)
- `chat_message.author_id` → `human_user.id` | `agent.id` (via author_type; nullable for system/tool_result)
- `chat_artifact.session_id` → `chat_session.id`
- `chat_artifact.message_id` → `chat_message.id`
- `chat_summary.session_id` → `chat_session.id`
- `chat_read_cursor.session_id` → `chat_session.id`
- `chat_read_cursor.user_id` → `human_user.id`
- `chat_message_reaction.message_id` → `chat_message.id`
- `chat_message_reaction.reactor_id` → `human_user.id` | `agent.id` (via reactor_type)

**Polymorphic patterns:**
- `chat_session`: (scope_type, scope_id) → 'org'→organization | 'project'→project | 'task'→project_task
- `chat_session`: (created_by_type, created_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID
- `chat_participant`: (participant_type, participant_id) → 'human'→human_user | 'agent'→agent
- `chat_message`: (author_type, author_id) → 'human'→human_user | 'agent'→agent | null for system/tool_result/tool_call
- `chat_message_reaction`: (reactor_type, reactor_id) → 'human'→human_user | 'agent'→agent

**Key behavioral rules:**
- One session per scope: one org session ("General"), one per project, one sync session per task
- Per-node async sessions auto-created when a flow node begins execution (agent work logs, not human-facing)
- Session mode: sync (human present, latency matters) vs async (autonomous, quality matters); mode is mutable
- Message states: pending → streaming → final → failed | redacted. Append-only after final.
- `redacted` state zeroes content but preserves the row (audit integrity)
- Turn cycle: Phase 1 (responder) → Phase 1.5 (agent @mentions, one round only) → Phase 2 (listening eval, Haiku-tier) → Phase 3 (interjections, ordered by relevance score). Cycle is atomic.
- Interjections do NOT trigger another listening eval round (prevents infinite recursion)
- Two-tier tool execution: Tier 1 (read-only, chat layer, basic permission check) vs Tier 2 (mutations/external, full control plane; policy: allow/deny, binary, no mid-turn blocking)
- Stop conditions: max_tool_calls (hard counter) and max_duration (soft, checked at loop boundaries). Tokens tracked but NOT a stop condition.
- Progressive summarization: threshold ~50-60% of layer 6 budget, summarize oldest ~25-30% of unsummarized turns
- Summaries: immutable, cover from_sequence to to_sequence, preserve file paths/URLs/artifact IDs/entity names verbatim AND failure evidence
- Cancel = explicit (Escape/Cancel button). New messages queue, not cancel. Steer = cancel + promote specific queued message.
- Queued messages are editable while pending. Multi-human sessions: single shared queue ordered by submission time.
- Reactions: one per participant per message (unique constraint). Only on final messages. Not on own messages.
- Each agent text segment between tool calls is its own message row. Tool calls and results are discrete messages.
- Tier 1 tool calls in same response can execute in parallel; Tier 2 execute sequentially.
- Continuation turns: system informs agent when context was compressed; agent directed to query Ellie for gaps.
- Progressive summarization fires after each turn. Session lifecycle cleanup: immediate (close+extraction), deferred daily (ephemeral purge, summary consolidation, tool result compaction), retention-enforced (doc 13).
- Default responder: Frank (org session), PM (project session, task sync session), assigned agent (per-node async)

**API endpoints implied:**
- POST /sessions, GET /sessions/:id/messages, POST /sessions/:id/messages
- SSE /sessions/:id/events
- POST /sessions/:id/turns/:id/cancel
- POST /sessions/:id/messages/:id/react

**Realtime events:**
- chat.turn.started, chat.message.created, chat.message.delta, chat.message.finalized
- chat.turn.completed, chat.listening_eval.completed

**Cross-spec references:**
- `flow_node_execution.session_id` → `chat_session.id` (link lives on flow execution side, not chat_session)
- Chat turns link to control plane runs via run.session_id + run.turn_id (doc 16)
- Tool execution details live in control plane run/tool_execution tables (doc 16)
- Memory items live in Ellie's memory system (doc 06)
- Model invocation details live in model gateway (doc 07)
- Chat session retention controlled by doc 13 daily job (default 90 days for messages; summaries kept forever)

---

## Doc 03: Projects and Task Flow

**Tables:**
- `project` — organization_id, slug (unique within org), context_block, repo_path, created_by_type+created_by_id
- `flow_template` — project_id (nullable for system templates), version, is_current, start_node_id
- `flow_node` — flow_template_id, node_type (work|review), actor_type (role|project_manager|human|agent), actor_id, next_node_id, reject_node_id, mcp_tools jsonb; skills linked via flow_node_skill join table (doc 10)
- `project_task` — project_id, flow_template_id, current_flow_node_id, schedule_id, branch_name, task_number, work_status, priority, requires_human_review, created_by_type+created_by_id
- `flow_node_execution` — task_id, flow_node_id, visit (incremented on rejection), session_id, status, commit_sha
- `project_subtask` — task_id, flow_node_execution_id, subtask_number, assignee_type+assignee_id, work_status (no 'review' or 'on_hold')
- `project_task_dependency` — source_type+source_id, depends_on_type+depends_on_id; polymorphic (task|subtask); check constraint enforces source_type = depends_on_type (no cross-level)
- `project_task_participant` — task_id, participant_type+participant_id (agent|human), role (planner|worker|reviewer|observer)
- `task_schedule` — project_id, flow_template_id, cron_expression, overlap_policy, max_duration_ms, status (active|paused)
- `inbox_item` — organization_id, target_user_id, item_type, source_project_id, source_task_id, status (pending|deferred|acted), action_payload, action_taken
- `merge_queue_entry` — project_id, task_id, status (queued|merging|conflict|merged), branch_name, archived_at
- `project_task_event` — task_id, event_type, flow_node_id, visit, actor_type+actor_id, comment

**FKs:**
- `project.organization_id` → `organization.id`
- `flow_template.project_id` → `project.id` (nullable)
- `flow_template.start_node_id` → `flow_node.id` (set after nodes created)
- `flow_node.flow_template_id` → `flow_template.id`
- `flow_node.next_node_id` → `flow_node.id` (self-ref)
- `flow_node.reject_node_id` → `flow_node.id` (self-ref; review nodes only)
- `flow_node.actor_id` → `agent.id` (when actor_type = 'agent')
- `project_task.project_id` → `project.id`
- `project_task.flow_template_id` → `flow_template.id` (nullable in draft)
- `project_task.current_flow_node_id` → `flow_node.id`
- `project_task.schedule_id` → `task_schedule.id` (nullable)
- `flow_node_execution.task_id` → `project_task.id`
- `flow_node_execution.flow_node_id` → `flow_node.id`
- `flow_node_execution.session_id` → `chat_session.id`
- `project_subtask.task_id` → `project_task.id`
- `project_subtask.flow_node_execution_id` → `flow_node_execution.id`
- `project_subtask.assignee_id` → `agent.id` | `human_user.id` (via assignee_type)
- `project_task_dependency.source_id` → `project_task.id` | `project_subtask.id` (via source_type)
- `project_task_dependency.depends_on_id` → `project_task.id` | `project_subtask.id` (via depends_on_type)
- `project_task_participant.task_id` → `project_task.id`
- `project_task_participant.participant_id` → `agent.id` | `human_user.id` (via participant_type)
- `task_schedule.project_id` → `project.id`
- `task_schedule.flow_template_id` → `flow_template.id`
- `inbox_item.organization_id` → `organization.id`
- `inbox_item.target_user_id` → `human_user.id`
- `inbox_item.source_project_id` → `project.id` (nullable)
- `inbox_item.source_task_id` → `project_task.id` (nullable)
- `inbox_item.created_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via created_by_type)
- `merge_queue_entry.project_id` → `project.id`
- `merge_queue_entry.task_id` → `project_task.id`
- `project_task_event.task_id` → `project_task.id`
- `project_task_event.flow_node_id` → `flow_node.id` (nullable)
- `project_task_event.actor_id` → `human_user.id` | `agent.id` | sentinel UUID (via actor_type)

**Polymorphic patterns:**
- `project_task_dependency`: (source_type, source_id) → 'task'→project_task | 'subtask'→project_subtask; same for (depends_on_type, depends_on_id). CHECK constraint: source_type = depends_on_type (no cross-level deps)
- `project_task_participant`: (participant_type, participant_id) → 'agent'→agent | 'human'→human_user
- `project_subtask`: (assignee_type, assignee_id) → 'agent'→agent | 'human'→human_user
- `inbox_item`, `project_task_event`, `project`, `project_task`: (created_by_type/actor_type, id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID
- `flow_node`: (actor_type, actor_id) → 'agent'→agent when actor_type='agent'; actor_id null for role/project_manager/human

**Key behavioral rules:**
- Tasks always created through agents/chat — no UI creation path
- Task work_status: draft → queued → in_progress → blocked | on_hold → review → done | cancelled (terminal states: done, cancelled)
- `requires_human_review` flag: true = PM scopes but human must approve before queuing. false = PM queues autonomously.
- Flow templates are immutable once a task is using them. New version = new row, old row gets is_current=false.
- Flow progression always explicit: agent signals "step done" (flow.advance tool call). Run completion does NOT auto-advance.
- Rejection loops create new flow_node_execution records (visit counter). Each visit has own subtasks, session, commit_sha.
- commit_sha on flow_node_execution stores branch HEAD when node completed (used as review diff base for next reviewer)
- Subtasks: no 'review' or 'on_hold' states. Scoped to flow_node_execution, not task. Sequential (shared task branch). One level deep.
- Dependencies form a DAG (cycles rejected at creation). Same-level only (task→task or subtask→subtask within same parent).
- Cancelled dependency → system auto-creates resolution task assigned to PM (blocked progress creates tasks, not notifications)
- Inbox items persist until acted on (no expiry). Deferred items accessible but out of primary view.
- merge_queue_entry rows never hard-deleted; archived_at set when deploy including entry completes successfully.
- Task branch: task/<slug>. Subtasks share the task branch (no sub-branches).
- Every task has a branch + merge queue entry even if no file changes (empty merge = no-op fast-forward).
- inbox_item.item_type values: task_scoping_review, task_work_review, draft_action_review, escalation, capability_approval, browser_handoff
- project_task_event.event_type values include: status_change, flow_advance, flow_reject, blocked, unblocked, assigned, subtask_created, subtask_completed, merged, push_succeeded, push_failed, deployed, escalated, dependency_added, dependency_removed, schedule_cancelled
- Task numbering: {project_slug}-{task_number} (e.g., OC-5). Subtask: OC-5.3.

**Cross-spec references:**
- `flow_node_execution.session_id` → `chat_session.id` (doc 02)
- `flow_node.mcp_tools` jsonb (doc 09)
- `flow_node.actor_id` → `agent.id` (doc 05)
- flow_node_skill join table (doc 10)
- `project_task_participant.participant_id` → `agent.id` (doc 05)
- Concurrency slot management from model gateway (doc 07)
- Control plane run/run_step link to task and flow node (doc 16)
- Blocker escalation: agent → PM → Frank → human (doc 05)

---

## Doc 03a: Shipping and Delivery

**Tables (new):**
- `project_remote` — project_id, remote_type (git_host|ssh|deploy_platform), push_behavior (auto|manual), auth_config jsonb (credential references, not raw secrets), position
- `project_environment` — project_id, position, deployed_commit_sha, previous_commit_sha, deployed_at, deploy_task_id

**Columns added to `project`:**
- `delivery_mode` — text check (continuous|gated|scheduled), nullable (null = no Pattern 2 delivery configured)
- `deploy_flow_template_id` → `flow_template.id`

**Event types added to `project_task_event.event_type`:**
- `push_succeeded` — recorded on originating task after successful push
- `push_failed` — recorded on originating task after failed push (comment includes error details)
- `deployed` — recorded on deploy task after successful deploy to environment

**FKs:**
- `project_remote.project_id` → `project.id`
- `project_environment.project_id` → `project.id`
- `project_environment.deploy_task_id` → `project_task.id` (nullable)
- `project.deploy_flow_template_id` → `flow_template.id`

**Polymorphic patterns:** none new

**Key behavioral rules:**
- Pattern 1: delivery is a flow node inside the task (common case). By the time task reaches done, value already delivered.
- Pattern 2: delivery is a separate deploy task triggered after merge to main (for code deployment, batched releases).
- Deploy tasks are regular project_task rows with deploy-specific context in metadata jsonb. No new entity type.
- Push = infrastructure, not a task. Fires as final step of merge queue hook (after branch merges to main).
- Auto-push: 3 retries with exponential backoff. Failure → push_failed event on originating task, PM notified.
- Subsequent merges still attempt push — transient issues self-heal.
- Continuous mode: new deploy task only if no deploy currently pending/in_progress. PostgreSQL advisory lock per project prevents race condition in deploy task creation.
- Gated mode: human triggers via chat with PM. PM may proactively suggest.
- Scheduled mode: task schedule (doc 03) with overlap policy 'skip'.
- Rollback = new deploy task targeting environment.previous_commit_sha. Original tasks stay done. Append-only history.
- project_environment.previous_commit_sha is set to old deployed_commit_sha on each deploy. One-level rollback target.
- Deploy task metadata convention: metadata.deploy = {commit_sha, included_task_ids, target_environment}
- included_task_ids computed by querying merge_queue_entry for all entries merged between last deploy and current commit.
- merge_queue_entry archived_at set when deploy including that entry completes (never hard-deleted).

**Cross-spec references:**
- Uses flow_template (doc 03) for deploy flows — same immutability rules apply
- Delivery completion notifications via existing event bus (doc 12) — no new mechanisms
- PM manages delivery config conversationally (doc 03/05)

---

## Doc 04: Auth, Tenancy, and Identity

**Tables:**
- `organization` — slug (unique within database/instance), settings jsonb, status (active|suspended|deleted)
- `human_user` — organization_id, email (unique within org database), role (owner|admin|member|viewer), password_hash (nullable for future SSO), external_auth_provider, external_auth_id, status (active|suspended|deleted)
- `auth_session` — user_id, token_hash (SHA-256), ip_address, user_agent, expires_at, revoked_at, last_active_at
- `api_key` — user_id, key_hash (SHA-256), key_prefix (first 8 chars), scope (full|read|chat), expires_at (nullable), revoked_at, last_used_at
- `audit_event` — organization_id, action, principal_type (human|agent|system), principal_id, delegated_by, scope_type (org|project|task|session), scope_id, context jsonb

**FKs:**
- `human_user.organization_id` → `organization.id`
- `auth_session.user_id` → `human_user.id`
- `api_key.user_id` → `human_user.id`
- `audit_event.organization_id` → `organization.id`
- `audit_event.principal_id` → `human_user.id` | `agent.id` | sentinel UUID (via principal_type)
- `audit_event.delegated_by` → `human_user.id` (nullable)

**Polymorphic patterns:**
- `audit_event`: (principal_type, principal_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID (00000000-...)
- `audit_event`: (scope_type, scope_id) → 'org'→organization | 'project'→project | 'task'→project_task | 'session'→chat_session
- Principal convention throughout: (principal_type/actor_type/author_type/created_by_type, id) — same polymorphic pattern everywhere

**Key behavioral rules:**
- Database-per-org isolation (NOT row-level security). organization_id on tables is for referential integrity + query consistency, NOT security.
- V2 GA: one human per org (owner). Multi-user near-term with four RBAC roles (owner/admin/member/viewer).
- No org_membership table — role lives directly on human_user.
- Email + password auth (bcrypt work factor 12). 30-day sliding session window.
- API keys: `oc_<scope>_<random>` format. SHA-256 hash stored. Three scopes: full/read/chat.
- SSO/OIDC deferred. Schema hooks: external_auth_provider, external_auth_id on human_user.
- Agents do NOT authenticate themselves — platform asserts identity via execution context.
- System actor: actor_type='system', actor_id = sentinel UUID (00000000-0000-0000-0000-000000000000)
- Delegation: when human authorizes agent action, audit trail records both agent (performer) and human (delegated_by)
- Bootstrap is idempotent (skip if org exists). Detected by checking for any organization row.
- Rate limiting: per-IP (10 failed/15min) and per-account (5 failed/hour, 30-min lockout). In-memory counters.
- auth_session ≠ chat_session (separate concepts, separate tables)
- Two deployment modes: self-hosted (single database) and managed hosting (separate database per org + routing layer)
- Password-less local auth: OTTERCAMP_AUTH_MODE=local (self-hosted only, localhost binding only)

**Bootstrap sequence (doc 14 is authoritative — 10 steps):**
1. Run database migrations
2. Create organization
3. Create first human user (owner)
4. Create org-level skills repo + default skills
5. Seed model profiles (high-capability, standard, haiku)
6. Seed default flow templates
7. Seed starter trio agents (Frank, Lori, Ellie)
8. Create General session with participants
9. Seed default org policy
10. Record bootstrap audit event

**Cross-spec references:**
- agent table schema defined in doc 05 (this doc references agent as principal type only)
- chat_session created during bootstrap (doc 02)
- Model profiles seeded during bootstrap (doc 07)
- Skills repo created during bootstrap (doc 10)

---

## Doc 05: Agents — Staff and Temps

**Tables:**
- `agent` — organization_id, slug (unique within org), agent_class (staff|temp), scope_level (org|project), lifecycle_status, system_prompt, policy_addendum, tool_allow_list text[], tool_deny_list text[], default_model_profile_id, allowed_model_profiles uuid[], budget_cap_cents, budget_period, memory_read_scopes text[], private_memory_enabled, temp_scope_type, temp_scope_id, temp_ttl_seconds, temp_expires_at, promoted_to_agent_id
- `agent_project_assignment` — agent_id, project_id, role (project_manager|worker|reviewer|planner), is_active, assigned_by_type+assigned_by_id, removed_at
- `agent_skill_attachment` — agent_id, skill_id, purpose, priority (lower value = higher precedence), attached_by_type+attached_by_id
- `agent_profile_template` — organization_id (nullable for system templates), slug, role_title, domain_tags text[], recommended_class (staff|temp), system_prompt, policy_addendum, suggested_skills text[], suggested_tool_policy jsonb, suggested_model_tier (high|standard|fast), source (system|org|promoted), source_agent_id, is_active

**FKs:**
- `agent.organization_id` → `organization.id`
- `agent.promoted_to_agent_id` → `agent.id` (self-ref, nullable)
- `agent.default_model_profile_id` → `model_profile.logical_profile_id` (stable identity; doc 07)
- `agent_project_assignment.agent_id` → `agent.id`
- `agent_project_assignment.project_id` → `project.id`
- `agent_project_assignment.assigned_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via assigned_by_type)
- `agent_skill_attachment.agent_id` → `agent.id`
- `agent_skill_attachment.skill_id` → `skill.id` (doc 10)
- `agent_skill_attachment.attached_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via attached_by_type)
- `agent_profile_template.organization_id` → `organization.id` (nullable; null = system-shipped template)
- `agent_profile_template.source_agent_id` → `agent.id` (nullable; set when promoted from live agent)

**Polymorphic patterns:**
- `agent`: (temp_scope_type, temp_scope_id) → 'project'→project | 'task'→project_task | 'session'→chat_session (no 'ttl' since ttl uses temp_ttl_seconds)
- `agent_project_assignment`: (assigned_by_type, assigned_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID

**Key behavioral rules:**
- Two agent classes: staff (durable, cross-project memory, lifecycle: draft→active→paused→retired/cancelled) vs temp (ephemeral, single-project, lifecycle: active→expired→promoted; no draft step)
- Temp scope types: project (standing workforce, persist across tasks), task, session, ttl
- PM: always staff, always exactly one per project (enforced by partial unique index on agent_project_assignment)
- Temps skip draft review — created immediately active. Staff go through draft → human approval → active.
- Concurrent temp agents: configurable per-org, stored in organization.settings.agents.max_concurrent_temps, default 10
- Private memory: true for Frank, Lori, Ellie, and all PMs. False for all other staff and all temps.
- memory_read_scopes default: {org, assigned_projects, current_task} for both staff and temps. Staff cross-project memory comes from being assigned to multiple projects, not from different scopes.
- 7-layer prompt assembly: (1) agent identity (never cut), (2) policies+constraints (never cut), (3) scope context, (4) skills, (5) memory, (6) conversation history, (7) tool descriptions
- Layers 1-2 always included. Layers 3-7 budget-dependent. Assembly runs once per turn start.
- Tool allow_list and deny_list are mutually exclusive (check constraint). Both null = inherit org/project policy.
- budget_cap_cents column name in schema (NOTE: doc 14 resolved Q#24 says rename to budget_cap_tokens — inconsistency logged as issue)
- Temp auto-retirement: event-driven. Task completion → task-scoped temps expired. Session close → session-scoped temps expired. Periodic scheduler → TTL temps expired.
- Temp expiration: agent_project_assignment.is_active set to false, project access revoked.
- Archival summary on temp expiration/retirement: Ellie generates brief summary (what it was for, what it did, outcome) stored as episodic memory at project scope.
- Temp promotion to staff: Lori reviews config, proposes staff profile, human approves (draft→active flow), temp marked 'promoted' with promoted_to_agent_id set.
- Staff agent lifecycle_status values: draft, active, paused, retired, cancelled
- Temp lifecycle_status values: active, expired, promoted
- (lifecycle_status column shared — both staff and temp use same column)
- Agent profile catalog: 230+ templates from V1 in agent_profile_template table. System templates have organization_id=null.
- Agent identity is stateless at prompt level — everything assembled fresh from profile + context + memory + history on every turn.
- Nondeterministic idempotence: acceptance criteria define "done"; workflows complete regardless of how many sessions it takes.
- Staff agents assigned to multiple projects have memory from all assigned projects injected on every turn.
- project_manager partial unique index: (project_id) WHERE role = 'project_manager' AND is_active = true

**Cross-spec references:**
- agent_skill_attachment.skill_id → skill (doc 10)
- agent.default_model_profile_id → model_profile (doc 07)
- flow_node.actor_id → agent.id (doc 03)
- chat_participant.participant_id → agent.id (doc 02)
- project_task_participant.participant_id → agent.id (doc 03)
- memory.agent_id → agent.id for agent-private memory (doc 06)
- Deploy flow templates and environments defined in doc 03a

---

## Doc 14: Open Questions and Build Phasing

**Tables:** none new

**Key resolved decisions relevant to build:**
- Build phases: 0 (Foundation) → 1 (Sync Chat + TUI) → 2 (Projects + Tasks) → 3 (Self-Building) → 4 (Hardening)
- Sprint 1 = Phase 0 + Phase 1 + Phase 2 + Phase 3 API surface (minus UI)
- Default capability templates: reader, worker, deployer, admin. Starter trio gets admin. Most task-working agents get worker.
- Retention: memories forever, chat 1 year, chat summaries forever, model invocations 90 days (rollups forever), domain events 90 days, audit events 1 year, tool executions 90 days. All configurable per org.
- Budget unit: TOKENS (not cents). Rename budget_cap_cents → budget_cap_tokens. All three levels (org, project, agent) use tokens. UI shows dollar estimates (display only).
- Bootstrap sequence: 10 idempotent steps (doc 14 is authoritative; doc 04's 7-step sequence is stale).
- Default org policy: communication tools allowed (create drafts as designed tool behavior). CLI needs system.cli.execute capability. Browser needs granular capability grants.
- SSE is default realtime transport; WebSocket available for bidirectional.
- Auto-login for local mode: OTTERCAMP_MODE=local auto-authenticates as bootstrap user.
- Starter trio profile updates on startup: automatic with customization guard. system_prompt updated; operator_instructions field never overwritten.
- Haiku profile: used for listening evals, summarization, memory extraction, memory synthesis.
- MCP sampling: not supported.
- 3-5 starter MCP connection templates (GitHub, Slack, Postgres, filesystem, web search).
- doc 14 bootstrap dataset section is authoritative for the 10-step bootstrap sequence.
- Lori has private_memory_enabled=true (doc 05 is authoritative, confirmed here).

**Notes:**
- Doc 14 Q#24 resolution: "rename budget_cap_cents to budget_cap_tokens" — but doc 05 schema still uses budget_cap_cents. This is a spec inconsistency (logged as ISSUE #1).
- Doc 14 Q#7 resolution: "doc 04's bootstrap sequence is stale — doc 14 is authoritative for the 10-step sequence" — but doc 04 has not been updated to reflect this. Doc 04's 7-step description differs from doc 14's 10-step sequence. (logged as ISSUE #2)

---

## Cross-Reference Summary

Key cross-doc FK relationships found:

1. `chat_session.scope_id` → `organization.id` | `project.id` | `project_task.id` (polymorphic)
2. `flow_node_execution.session_id` → `chat_session.id`
3. `project_task.current_flow_node_id` → `flow_node.id`
4. `flow_node.actor_id` → `agent.id`
5. `agent.default_model_profile_id` → `model_profile.logical_profile_id` (doc 07 to define)
6. `agent_skill_attachment.skill_id` → `skill.id` (doc 10 to define)
7. `flow_node.mcp_tools` jsonb (doc 09 to define)
8. `memory.agent_id` → `agent.id` (doc 06 to define)
9. `chat_turn` ↔ control plane run/run_step (doc 16 to define)
10. `inbox_item.item_type = 'browser_handoff'` — referenced in doc 03 schema but behavior not defined in docs 01-05 or 14; likely defined in doc 11

---

---

## Doc 06: Memory Management (Ellie V2)

**Tables:**
- `memory` — organization_id, project_id (null=org), task_id (null=project+), agent_id (non-null=agent-private), kind, layer (episodic|semantic|procedural), status (candidate|active|consolidated|archived), is_hardened boolean, content, confidence real, utility real, occurred_at, valid_from, valid_until, archived_reason, superseded_by (self-FK), sensitivity (public|internal|restricted), content_hash, source_file_path, source_file_hash, source_file_mtime, embedding vector(1536), metadata jsonb
- `memory_taxonomy_node` — organization_id, parent_id (self-FK nullable), name, path (materialized, e.g. "engineering > deployment > ci-pipelines"), depth, description; unique(organization_id, path)
- `memory_taxonomy_tag` — memory_id, taxonomy_node_id; unique(memory_id, taxonomy_node_id)
- `memory_entity` — organization_id, name, entity_type (person|project|tool|concept|organization), aliases text[], synthesis_memory_id (FK to memory.id, nullable), last_synthesized_at; unique(organization_id, entity_type, name)
- `memory_entity_mention` — memory_id, entity_id; unique(memory_id, entity_id)
- `memory_source` — memory_id, source_type (chat_message|event|file|import|explicit), source_id uuid (polymorphic), session_id uuid (nullable), import_id (FK to memory_import.id nullable), trust_tier real
- `memory_dedup_reviewed` — memory_id_a, memory_id_b (check: a < b for canonical ordering), decision (keep_both|deprecated_a|deprecated_b|merged); unique(memory_id_a, memory_id_b)
- `memory_compaction_run` — organization_id, run_type (dedup|synthesis|decay|distillation|reflection|task_completion|reembed), status (running|completed|failed), memories_processed, memories_archived, memories_created, error_count, error_details, started_at, completed_at
- `memory_import` — organization_id, requested_by (FK human_user.id), source_filename, source_size_bytes, status (pending|processing|completed|failed), files_in_archive, files_processed, memories_extracted, duplicates_skipped, error_count, error_details, started_at, completed_at

**FKs:**
- `memory.organization_id` → `organization.id`
- `memory.project_id` → `project.id` (nullable)
- `memory.task_id` → `project_task.id` (nullable)
- `memory.agent_id` → `agent.id` (nullable; non-null = agent-private scope)
- `memory.superseded_by` → `memory.id` (self-ref, nullable)
- `memory_taxonomy_node.organization_id` → `organization.id`
- `memory_taxonomy_node.parent_id` → `memory_taxonomy_node.id` (self-ref, nullable)
- `memory_taxonomy_tag.memory_id` → `memory.id` (on delete cascade)
- `memory_taxonomy_tag.taxonomy_node_id` → `memory_taxonomy_node.id`
- `memory_entity.organization_id` → `organization.id`
- `memory_entity.synthesis_memory_id` → `memory.id` (nullable)
- `memory_entity_mention.memory_id` → `memory.id` (on delete cascade)
- `memory_entity_mention.entity_id` → `memory_entity.id`
- `memory_source.memory_id` → `memory.id` (on delete cascade)
- `memory_source.import_id` → `memory_import.id` (nullable)
- `memory_source.session_id` → `chat_session.id` (nullable; implied, not explicit FK in DDL)
- `memory_dedup_reviewed.memory_id_a` → `memory.id`
- `memory_dedup_reviewed.memory_id_b` → `memory.id`
- `memory_compaction_run.organization_id` → `organization.id`
- `memory_import.organization_id` → `organization.id`
- `memory_import.requested_by` → `human_user.id`

**Polymorphic patterns:**
- `memory_source`: (source_type, source_id) → 'chat_message'→chat_message | 'event'→(event bus event, no table FK) | 'file'→(filesystem/object store, no table FK) | 'import'→memory_import | 'explicit'→(no table FK, direct capture); source_id may be null for some types

**Key behavioral rules:**
- Memory scope determined by combination of columns: org_id only = org scope; org_id+project_id = project scope; org_id+project_id+task_id = task scope; agent_id non-null = agent-private
- Memory kinds: fact, decision, preference, lesson, pattern, anti_pattern, correction, context, entity_definition, process_outcome
- Memory layers: episodic (time-stamped events, decay over time), semantic (distilled durable facts, no decay), procedural (learned heuristics, decay if not reinforced)
- Memory states: candidate → active → consolidated → archived. Candidate held 7 days; promoted if corroborated else discarded.
- is_hardened boolean: active+is_hardened=false = provisional (retrievable via entity/taxonomy but excluded from vector search). Hardening completes in seconds. Failure reverts to candidate.
- Extraction pipeline: Stage 0 (deterministic garbage rejection + behavioral override rejection) → Stage 1 (LLM extraction, Haiku-class) → Stage 2 (scoring 0–100, threshold 40) → Stage 3 (normalization: entity names, taxonomy assignment) → Stage 4 (embed + dedup + store)
- Confidence (0.0–1.0) = LLM self-assessed confidence stored on memory row. Utility score (0–100) = composite scoring from Stage 2 stored in metadata. Both preserved but used for different purposes.
- Confidence is CAPPED by max(trust_tier) across memory_source records. Trust tiers: human direct=1.0, human reaction=0.9, agent from conversation=0.8, agent from task=0.7, file=0.7, import=0.6, external=0.4.
- Confidence and utility are NOT used as retrieval ranking weights (V1 proven -4pp regression). Used only for lifecycle decisions.
- Retrieval pipeline (4 stages): scope filter (hard WHERE clause) → taxonomy classification (LLM or rule-based, Haiku-class) → subtree retrieval (pull memories tagged with taxonomy nodes) → relevance ranking (vector cosine + recency + confidence; semantic > episodic > procedural priority for factual queries). Fallback: if low classification confidence or <3 results → skip taxonomy, search full corpus.
- Three retrieval modes: (1) passive injection (every turn, budget-aware, injection cooldown, attention-aware ordering — most relevant last), (2) active query via @mention (deeper, cross-encoder reranking, can cross scope), (3) agent-initiated via memory.query tool (excludes already-injected memories)
- Injection ordering: most relevant memories LAST in block (recency bias). Claim content before metadata within each memory.
- Sensitivity: 'restricted' memories excluded from passive injection (only active queries). Public = no restrictions. Internal = default.
- Entity synthesis: detect entities with high mention count, gather all memories mentioning them, LLM generates entity_definition memory, stored at high confidence. #1 retrieval improvement (+15pp). Must be periodic pipeline step, not optional.
- Dedup: semantic threshold pre-screening (cosine ≥ 0.88) → LLM cluster dedup (Haiku) → cursor-based progress tracking in memory_dedup_reviewed. Improves diversity, not hit rate. Run weekly.
- Contradiction detection: two-path. Primary at extraction time (shared entity/taxonomy overlap + LLM comparison). Secondary during sleep-time reflection (periodic sweep for contradictions without shared entities).
- Supersession: fact/decision types → older archived with archived_reason='superseded', superseded_by FK set. Preference/pattern/lesson types → confidence decreases on contradiction, archived only if confidence < threshold.
- Supersession chain FK: archived memories retain superseded_by → newer memory, enabling temporal audit traversal.
- Task completion triggers immediate consolidation run (type: task_completion): scope promotion (task_id=null), episodic distillation, execution summary (procedural memory = tool choreography playbook), targeted entity synthesis.
- Scope promotion = set task_id=null on memory row. No duplicate created. memory_source records preserve provenance.
- Sleep-time reflection: periodic holistic pattern recognition across recent episodic memories by project+taxonomy. Also triggered by friction signals (retrieval failures, corrections).
- File-backed memories: tracked via source_file_path, source_file_hash, source_file_mtime. Periodic freshness scan; re-extract on change. Live file reading at query time is optional.
- memory.query tool available to all agents. Results exclude already-injected memories for the current turn.
- Ellie's dual role: background memory infrastructure AND conversational agent (@mention, on-demand ops).
- Extraction runs in isolated contexts — never in active agent's session. Won't consume agent's context budget.
- Instruction poisoning defense: Stage 0 deterministic pattern rejection + Stage 1 LLM behavioral vs factual classification. Behavioral candidates rejected with structured log.
- Chat reactions feed memory: positive → increase confidence of contributing memories; negative → decrease; correction → create 'correction' memory that invalidates stale one.
- Global taxonomy: one org-level tree, not per-project. Ellie manages autonomously (create, merge, prune). Self-bootstraps from first memories. Multi-label (one memory → many taxonomy nodes).
- Taxonomy is pre-filter (narrows corpus before vector search), NOT supplement after. V1 tested both; pre-filter is better for obscure/older items.
- Embedding: 1536d (OpenAI text-embedding-3-small). Never truncate. Re-embed via memory_compaction_run (type: reembed) when model changes. During migration, un-re-embedded memories excluded from vector search.
- content_hash: SHA256 of content after whitespace trimming only (no lowercasing). Fast exact-dedup and idempotent import detection.
- Import via zip upload containing JSONL files. Each line: {timestamp, author, content} + optional {role, session_id, metadata}. Groups by session_id or time proximity.
- memory_import.requested_by → human_user.id (imports only triggered by humans, not agents).
- Provenance enforcement: every active memory must have ≥1 memory_source record. Orphaned memories flagged for archival.
- Ellie does NOT own: working memory (doc 02 progressive summarization), skills (doc 10), project documentation (indexed but not authored), task orchestration (PM + Frank).
- Procedural memory includes tool choreography: learned tool call sequences and strategies. Captured in task-completion execution summaries.
- Decay: episodic = configurable half-life, reinforced by retrieval. Semantic = no decay, invalidated only by contradiction. Procedural = decays if not reinforced by successful outcomes.
- Candidate quality gates: confidence threshold (kind-specific), utility, novelty check. Auto-promoted to provisional active if threshold met.

**API endpoints implied:**
- POST /memory/import (upload zip) — triggers memory_import job
- GET /memory/imports/:id — check import status

**Cross-spec references:**
- `memory.organization_id` → `organization.id` (doc 04)
- `memory.project_id` → `project.id` (doc 03)
- `memory.task_id` → `project_task.id` (doc 03)
- `memory.agent_id` → `agent.id` (doc 05) — agent-private scope
- `memory_source.session_id` → `chat_session.id` (doc 02)
- `memory_import.requested_by` → `human_user.id` (doc 04)
- Agent `memory_read_scopes` array (doc 05) drives scope filter in retrieval Stage 1
- Memory layer 5 of 7 in agent prompt assembly (doc 05)
- Chat reactions drive memory confidence feedback (doc 02 chat_message_reaction)
- Task completion triggers consolidation (doc 03 project_task status = done)
- Progressive summarization handles working memory (doc 02); this doc handles durable memory only
- memory.query tool available in all agents' tool sets (doc 05/doc 16 for tool execution)
- Retention: memories kept forever (doc 14)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #9 (below): memory_source.session_id references chat_session.id but has no explicit FK in DDL (just a plain uuid column) — provenance enforcement is partially application-layer
- ISSUE #10 (below): memory_source.source_id is polymorphic (chat_message|event|file|import|explicit) but source_type='event' and source_type='file' have no corresponding database table FK — provenance for these types is not enforced by the DB

---

## Doc 16: Agent Control Plane

**Tables:**
- `run` — organization_id, project_id, task_id, flow_node_id, session_id, turn_id, principal_type (agent|human|system), principal_id, delegated_by uuid (nullable), capability text, action_type text, action_payload jsonb (secrets redacted), idempotency_key, policy_decision (allow|deny), policy_decided_by, policy_rule_id, status (created|in_progress|paused|completed|failed|timed_out|cancelled|cancelling|dead_letter), failure_reason, failure_class (transient|permanent|timeout|policy|cancelled|orphaned), total_input_tokens, total_output_tokens, version int (optimistic concurrency), metadata jsonb
- `run_step` — run_id, step_number, name, action_type, action_payload, status (pending|in_progress|completed|failed|timed_out|cancelled|skipped), failure_reason, failure_class, output jsonb, output_summary text, input_tokens, output_tokens; unique(run_id, step_number)
- `run_attempt` — run_step_id, attempt_number, trigger (initial|transient_failure|manual_retry), status (in_progress|completed|failed|timed_out|cancelled), failure_reason, failure_class (transient|permanent|timeout|cancelled), output, output_summary, worker_type (internal|cli|browser|mcp), worker_id, input_tokens, output_tokens; unique(run_step_id, attempt_number)
- `tool_execution` — run_id, run_step_id (nullable), run_attempt_id (nullable), tool_name, tool_tier (always 'tier2'), tool_domain (project|chat|memory|system|mcp|comms|agent), capability, policy_decision (allow|deny), input jsonb (secrets redacted), output jsonb, status (pending|in_progress|completed|failed|denied|cancelled), error_message, started_at, completed_at, duration_ms, metadata jsonb
- `run_artifact` — run_id, run_step_id (nullable), run_attempt_id (nullable), name, artifact_type (log|screenshot|file|trace|diff|cli_output|download|extracted_data|page_snapshot|build_output|test_report), content_type MIME, size_bytes, storage_path, description, is_primary boolean, metadata jsonb
- `run_event` — run_id, run_step_id (nullable), run_attempt_id (nullable), sequence int (monotonic within run), event_type (created|started|completed|failed|timed_out|cancelled|policy_evaluated|policy_denied|dispatched|worker_assigned|progress|output_chunk|heartbeat|stuck_detected|orphan_detected|auto_retry|escalated), event_data jsonb, actor_type (agent|human|system|supervisor), actor_id uuid; unique(run_id, sequence)
- `capability_policy` — organization_id, policy_layer (instance|org|project|agent_profile), project_id (nullable), agent_id (nullable), capability_pattern text (e.g. 'system.cli.execute', 'project.task.*'), decision (allow|deny), priority int, conditions jsonb (optional runtime conditions), description, created_by_type (human|agent|system), created_by_id, is_active boolean, metadata jsonb

**FKs:**
- `run.organization_id` → `organization.id`
- `run.project_id` → `project.id` (nullable)
- `run.task_id` → `project_task.id` (nullable)
- `run.flow_node_id` → `flow_node.id` (nullable)
- `run.session_id` → `chat_session.id` (nullable)
- `run.turn_id` → `chat_turn.id` (nullable)
- `run.principal_id` → `agent.id` | `human_user.id` | sentinel UUID (via principal_type; application-layer)
- `run.delegated_by` → `human_user.id` (nullable)
- `run_step.run_id` → `run.id` (on delete cascade)
- `run_attempt.run_step_id` → `run_step.id` (on delete cascade)
- `tool_execution.run_id` → `run.id` (on delete cascade)
- `tool_execution.run_step_id` → `run_step.id` (nullable, on delete cascade)
- `tool_execution.run_attempt_id` → `run_attempt.id` (nullable, on delete cascade)
- `run_artifact.run_id` → `run.id` (on delete cascade)
- `run_artifact.run_step_id` → `run_step.id` (nullable, on delete cascade)
- `run_artifact.run_attempt_id` → `run_attempt.id` (nullable, on delete cascade)
- `run_event.run_id` → `run.id` (on delete cascade)
- `run_event.run_step_id` → `run_step.id` (nullable, on delete cascade)
- `run_event.run_attempt_id` → `run_attempt.id` (nullable, on delete cascade)
- `capability_policy.organization_id` → `organization.id`
- `capability_policy.project_id` → `project.id` (nullable)
- `capability_policy.agent_id` → `agent.id` (nullable)
- `capability_policy.created_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via created_by_type; application-layer)

**Polymorphic patterns:**
- `run`: (principal_type, principal_id) → 'agent'→agent | 'human'→human_user | 'system'→sentinel UUID
- `run_event`: actor_type includes 'supervisor' — doc-16-specific extension beyond the standard principal convention. Distinguishes system-automated from supervisor-automated events in the event log.

**Key behavioral rules:**
- OtterCamp never directly executes agent mutations. All tier 2 tool calls flow through the broker → policy eval → worker.
- Policy evaluation: binary allow/deny. 5 layers (highest to lowest priority): instance safety (hardcoded) → org policy → project policy → agent profile → request-specific overrides. Most-restrictive-wins. Deny at any layer is absolute. Silence passes through (does not deny).
- Request-specific overrides can only restrict, never expand (can downgrade allow→deny, never upgrade deny→allow).
- Policy caching: org/project policies cached with short TTL. Agent profile cached per-session. Instance safety compiled at startup. Cache invalidated on policy write.
- Run is created before policy evaluation. If denied: run status=failed immediately. If allowed: run proceeds.
- Run states: created → in_progress → paused | completed | failed | timed_out | cancelled | cancelling | dead_letter.
- paused: waiting for external input (e.g., browser handoff to human). Exempt from stuck detection. 24-hour default timeout.
- dead_letter: exceeds max retry count → project_task_event created, PM notified.
- Retry: new RunAttempt per retry, never overwrite. Trigger: initial|transient_failure|manual_retry. Max retries by domain: MCP/external=3, internal=1, CLI/browser=2.
- Idempotency: broker deduplicates by idempotency_key within 24-hour window.
- Token counts on run/run_step/run_attempt are denormalized rollups from model_invocation (doc 07). Updated asynchronously, may be slightly stale.
- model_invocation table is doc 07's — run carries FKs run_id/run_step_id/run_attempt_id back to control plane. This doc does NOT own model_invocation.
- Heartbeats: running agents emit RunEvent (type: heartbeat) every 30s. Supervisor detects silence after 3 missed = 90s for sync, 5min for async.
- Supervisor: background process. Detects stuck tasks (runs failed/timed_out/silent), orphaned runs (in_progress but no events for run_timeout + grace), stale blockers (24h normal, 4h urgent). Recovery: (1) start new run, (2) file blocker, (3) escalate to PM. Max 3 auto-recovery attempts per task per flow node.
- Budget enforcement: soft budget → notify admin, work continues. Hard budget → non-essential capabilities denied. Essential (blocker filing, status updates) always available.
- worker_type on run_attempt: internal|cli|browser|mcp. No model_gateway worker type — model calls are tracked via model_invocation, not as worker runs.
- tool_execution.tool_tier is always 'tier2'. Tier 1 reads are NOT tracked here — only chat messages record them.
- run_event is append-only. heartbeat events are purged after run completion.
- Capability templates: reader (read-only), worker (project mutations + file I/O + CLI + browser + memory), deployer (worker + comms.*), admin (*). Starter trio get admin. Task-working agents get worker or deployer.
- Flow advance (project.flow.advance) is a control plane action subject to policy evaluation — it's not free.
- Human review of agent work → review flow nodes with human actor_type (doc 03), NOT a policy concern.
- capability_approval inbox items → created conversationally by agents (not by policy engine).
- Sandbox: CLI = restricted OS process with ulimit, command denylist, network policy, 5-min default timeout, 50KB inline stdout/stderr cap (excess as RunArtifact). Container-based isolation deferred for future.
- Browser sessions: task-scoped (first run creates, subsequent runs in same task can resume). Cleaned up on task completion.
- Run cancellation: graceful (run→cancelling, worker receives signal, completes atomic unit, then exits; if not within grace period, forcibly terminated, then run→cancelled).
- Runs are session-bound. Cannot transfer to different session.
- capability_policy.policy_layer: 'instance' is also stored in the DB (not just hardcoded in broker). But instance safety rules are compiled into broker at startup.
- Delegation: in sync sessions, delegated_by is set to the human in the session. In async sessions, delegated_by is null.

**API endpoints defined:**
- POST /control/runs, GET /control/runs/{id}, GET /control/runs/{id}/steps, GET /control/runs/{id}/events, GET /control/runs/{id}/artifacts
- POST /control/runs/{id}/cancel, POST /control/runs/{id}/retry
- GET /control/policies, POST /control/policies, PUT /control/policies/{id}, DELETE /control/policies/{id}
- POST /control/policies/evaluate (dry-run simulation)
- GET /control/runs (list with filters), GET /control/cost/summary, GET /control/health

**Cross-spec references:**
- `run.organization_id` → `organization.id` (doc 04)
- `run.project_id` → `project.id` (doc 03)
- `run.task_id` → `project_task.id` (doc 03)
- `run.flow_node_id` → `flow_node.id` (doc 03)
- `run.session_id` → `chat_session.id` (doc 02)
- `run.turn_id` → `chat_turn.id` (doc 02)
- `run.principal_id` → `agent.id` | `human_user.id` (docs 05, 04)
- `model_invocation.run_id`, `run_step_id`, `run_attempt_id` → run/run_step/run_attempt (doc 07) ← doc 07 defined these FKs; doc 16 is the FK target
- `mcp_execution_log.tool_execution_id` → `tool_execution.id` (doc 09) ← ISSUE #14 RESOLVED
- `mcp_execution_log.run_id` → `run.id` (doc 09)
- Supervisor emits project_task_event (doc 03) on stuck/orphan/dead_letter
- Budget enforcement reads from model_invocation rollups (doc 07)
- RunArtifacts stored in object storage same as chat artifacts and prompt captures (docs 02, 07)
- capability_policy creates the full permission model referenced by doc 09 (MCP capabilities)
- Tools and tool registry referenced from doc 20 (not yet read)
- CLI and browser sandbox details deferred to doc 11 (not yet read)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #12 RESOLVED: run_attempt table fully defined in doc 16 with columns as expected.
- ISSUE #14 RESOLVED: tool_execution table fully defined in doc 16 with columns as expected.
- ISSUE #17 (below): doc 16 defines `capability_policy.policy_layer` as including 'instance'. But it also says "Instance safety rules are compiled into the broker at startup and refreshed on deploy." This implies instance-level rules are BOTH stored in the database (as capability_policy rows) AND compiled into the broker at startup. This is redundant/potentially inconsistent: if instance safety rules can be stored in the DB as regular capability_policy rows (policy_layer='instance'), can they be modified through the API (POST /control/policies)? If yes, who can create them (only system bootstrap?) and are they protected from modification by org admins? The spec says "Hardcoded. Cannot be overridden by any lower layer" but doesn't say they can't be DELETED from the DB. Clarify: are instance-level policy rows protected (read-only via API) or are they truly DB-stored and manageable?
- ISSUE #18 (below): doc 16 says "flow transitions (project.flow.advance) and blocker filing (project.flow.blocker.raise) are control plane actions subject to policy evaluation." But doc 03 says "flow progression always explicit: agent signals 'step done' (flow.advance tool call). Run completion does NOT auto-advance." These are consistent. However, neither doc clarifies: when the agent calls flow.advance, does the broker create a Run record for it (since it's a control plane action), and if so, does that Run carry task_id and flow_node_id? Yes, the doc 16 run schema has these fields. But flow.advance is listed as a capability that worker template agents have, meaning it creates a Run. Multiple Runs per flow node are expected ("a flow node can have multiple runs"). This is consistent. No issue.

---

## Doc 10: Skills Integration

**Tables:**
- `skill` — organization_id, project_id (null=org-scoped), name, slug (unique within org or project via partial unique indexes), scope (org|project), category, description, is_default boolean (always activated for scope level), file_path (path in git repo), created_by_type (human|agent), created_by_id, metadata jsonb; unique(org_id, slug) WHERE project_id IS NULL; unique(project_id, slug) WHERE project_id IS NOT NULL
- `agent_skill_attachment` — agent_id, skill_id, purpose text (descriptive; 'identity' is system-recognized for activation), priority int (lower=higher precedence for budget ordering), attached_by_type (human|agent|system), attached_by_id; unique(agent_id, skill_id)
- `flow_node_skill` — flow_node_id, skill_id, position int (ordering for truncation tie-breaking); unique(flow_node_id, skill_id)

**FKs:**
- `skill.organization_id` → `organization.id`
- `skill.project_id` → `project.id` (nullable)
- `skill.created_by_id` → `human_user.id` | `agent.id` (via created_by_type; NO system sentinel here — created_by_type only 'human'|'agent', not 'system')
- `agent_skill_attachment.agent_id` → `agent.id` (on delete cascade)
- `agent_skill_attachment.skill_id` → `skill.id` (on delete cascade)
- `agent_skill_attachment.attached_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via attached_by_type)
- `flow_node_skill.flow_node_id` → `flow_node.id`
- `flow_node_skill.skill_id` → `skill.id`

**Polymorphic patterns:**
- `agent_skill_attachment`: (attached_by_type, attached_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID
- (Note: `skill.created_by_type` has ONLY 'human'|'agent' — no 'system'. No sentinel UUID path here.)

**Key behavioral rules:**
- Skills are plain markdown files with YAML frontmatter. No code, no templating, no conditional logic.
- Content lives in git repos (org skills repo for org-scope, project repo for project-scope), under `skills/` directory. Content NOT stored in DB — read from file at prompt assembly time. Newest committed version always active.
- Skill table is a registry only (metadata + file_path pointer). DB stores metadata; content is git-managed.
- Activation rules: (a) if flow_node has flow_node_skill entries → load: org defaults + project defaults + agent identity skills + flow_node skills. Other agent skills NOT loaded. (b) if no flow_node_skill entries → load: all agent skills + org defaults + project defaults. (c) sync sessions (no flow node) → same as (b).
- Identity skills: agent_skill_attachment entries where purpose='identity'. Selected by activation algorithm when flow node declares specific skills (to preserve agent identity even in focused mode).
- Resolution order (most specific wins): flow node > agent-level > project default > org default. Later in prompt = higher precedence (content assembled org defaults first, flow node skills last).
- Token budget: layer 4 of 7-layer assembly. Cut order: non-default non-identity agent skills first → summarize flow node skills → summarize agent identity skills → summarize org/project defaults. LLM-powered summarization uses Haiku-class (same as doc 07 invocation_purpose: skill_summarization).
- MCP prompts also at layer 4 alongside skills. Skills take precedence over MCP prompts on conflict. Combined budget covers both.
- Recommended max skill size: ~4,000 tokens per file. Guideline, not hard limit.
- flow_node.skills jsonb column REPLACED by flow_node_skill join table. The jsonb column should be dropped.
- No skill versioning beyond git history. Latest on main = active. No draft/published lifecycle.
- Slug uniqueness: org skills unique within org (project_id IS NULL). Project skills unique within project. Separate partial unique indexes enforce this.
- Default skills bootstrapped: Frank/Lori/Ellie identity skills (agent-level), PM identity skill, org-wide safety/communication/general work standards (is_default=true). Project template skills available but not pre-installed.
- Skill lifecycle managed through conversation with PM (project-scope) or Frank/Lori (org-scope). No skill editor UI.
- Registry consistency: PM keeps skill table and repo in sync. Background consistency check scans skills/ dir and reconciles with registry. Missing file → warning + skip (no runtime error).
- Skill synthesis from procedural memory: Ellie surfaces candidates; PM reviews and writes the skill. Human-in-the-loop, never automatic.
- Skills beat procedural memory: prescriptive (layer 4) vs advisory (layer 5). Always.
- `attached_by_type` on agent_skill_attachment allows 'system' (sentinel UUID). But `created_by_type` on skill only allows 'human'|'agent' — no 'system' sentinel.

**Cross-spec references:**
- `skill.organization_id` → `organization.id` (doc 04)
- `skill.project_id` → `project.id` (doc 03)
- `agent_skill_attachment.agent_id` → `agent.id` (doc 05) ← this table was already referenced in doc 05, now fully defined
- `flow_node_skill.flow_node_id` → `flow_node.id` (doc 03) ← ISSUE #6 RESOLVED: flow_node_skill is now fully defined
- Layer 4 of 7-layer prompt assembly (doc 05)
- MCP prompts at layer 4 alongside skills (doc 09)
- Ellie surfaces skill synthesis candidates from procedural memory (doc 06 Future Enhancements)
- Skill summarization uses Haiku-class LLM (doc 07 invocation_purpose: skill_summarization)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #6 RESOLVED: `flow_node_skill` fully defined in doc 10 with columns (flow_node_id, skill_id, position). Confirmed.
- ISSUE #16 (below): doc 10 says "The `skills` jsonb column on `flow_node` is no longer needed — the join table provides proper referential integrity and queryability. The `skills` jsonb column on `flow_node` should be dropped." But doc 03 defines `flow_node` with a `skills` jsonb column, and doc 09 ALSO references `flow_node.mcp_tools` jsonb column as separate from the `skills` column. If flow_node.skills jsonb is dropped, the doc 03 DDL needs to be updated. The specs still show both columns in doc 03 and doc 09's "Cross-Spec Dependencies" note says to add `mcp_tools jsonb` to flow_node. This means the current flow_node DDL is out of date: `skills` should be DROPPED, `mcp_tools` should be ADDED. The final flow_node schema is ambiguous across docs 03, 09, and 10.

---

## Doc 09: MCP Integration

**Tables:**
- `mcp_connection` — organization_id, project_id (null=org-level), name, slug (unique within org), description, transport_type (stdio|http_sse), transport_config jsonb (endpoint or command+args; `ref:<slug>` inline secret refs), auth_method (none|api_key|bearer_token|oauth2|custom), status (configuring|active|degraded|disabled|failed), eager_load boolean, health_config jsonb (check_interval_sec, check_timeout_sec, failure_threshold), timeout_ms int, retry_config jsonb, circuit_breaker_config jsonb, last_health_check_at, last_health_status, last_catalog_sync_at, created_by_type+created_by_id, metadata jsonb; unique(organization_id, slug)
- `mcp_tool_catalog` — connection_id, entry_type (tool|resource|prompt), name, description, input_schema jsonb, output_schema jsonb, resource_uri, prompt_arguments jsonb, is_enabled boolean (default false — default-deny), status (active|removed), discovered_at; unique(connection_id, entry_type, name)
- `mcp_execution_log` — organization_id, connection_id, catalog_entry_id, tool_execution_id (nullable FK→tool_execution.id, null for tier 1 resource reads), run_id (nullable FK→run.id), agent_id, entry_type (denormalized), tool_name, input_params jsonb (secrets redacted), output jsonb (secrets redacted), status (success|error|timeout|circuit_open|validation_error), error_message, duration_ms, policy_decision (allow|deny|null), retries, connection_health_at_completion
- `mcp_secret_binding` — connection_id, binding_key (logical name), secret_ref (secret.slug — not uuid FK), inject_as (header|query_param|env_var|body_field), inject_target; unique(connection_id, binding_key)

**FKs:**
- `mcp_connection.organization_id` → `organization.id`
- `mcp_connection.project_id` → `project.id` (nullable)
- `mcp_connection.created_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via created_by_type; application-layer)
- `mcp_tool_catalog.connection_id` → `mcp_connection.id` (on delete cascade)
- `mcp_execution_log.organization_id` → `organization.id`
- `mcp_execution_log.connection_id` → `mcp_connection.id`
- `mcp_execution_log.catalog_entry_id` → `mcp_tool_catalog.id`
- `mcp_execution_log.tool_execution_id` → `tool_execution.id` (nullable; doc 16, not yet read)
- `mcp_execution_log.run_id` → `run.id` (nullable; doc 16, not yet read)
- `mcp_execution_log.agent_id` → `agent.id`
- `mcp_secret_binding.connection_id` → `mcp_connection.id` (on delete cascade)
- `mcp_secret_binding.secret_ref` → `secret.slug` (application-layer; NOT a SQL FK — string reference into secret store)

**Polymorphic patterns:**
- `mcp_connection`: (created_by_type, created_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID
- `mcp_tool_catalog`: single table for tools, resources, AND prompts — same row structure, entry_type discriminator

**Key behavioral rules:**
- OtterCamp is MCP CLIENT only. Never an MCP server. Supports stdio and HTTP/SSE transports.
- Scope: org-level (project_id=null) or project-scoped (project_id set). Agent uses connection if: (a) in scope AND (b) has capability grant.
- Default-deny: all discovered tools cataloged with is_enabled=false. Admin must explicitly enable via conversation. Defense-in-depth separate from capability grants.
- Tool naming: <connection_slug>.<tool_name> e.g., `github.create_issue`. Slug unique within org prevents namespace collisions.
- Context-aware tool loading (3 modes): (1) lazy (default): agent prompt shows one-line connection summary per connection (~20 tokens each), agent calls mcp.discover to get schemas on-demand; (2) flow node preloading: tools declared in flow_node.mcp_tools are preloaded into worker prompt at layer 7 at session start (no discovery round-trip); (3) eager_load=true on connection: all enabled tools from that connection always preloaded (only for <~10 tools).
- flow_node.mcp_tools format: array of `connection_slug.tool_name` strings. Resolved against mcp_tool_catalog at session start. If tool not found, disabled, or connection unhealthy → warning in prompt + domain event emitted.
- mcp.discover is a native OtterCamp utility tool (no policy eval, no network call). Reads from mcp_tool_catalog. Always available.
- MCP tools are tier 2 (external mutations). Full control plane policy path: capability check (mcp.connection.use:<id> AND mcp.tool.invoke:<id>:<name>) → agent allow/deny list → policy evaluation → allow/deny.
- MCP resources are tier 1 (read-only). Basic scope check only (agent has access to connection's project?). No capability grant required. policy_decision=null in mcp_execution_log.
- MCP prompts: advisory only. Loaded at layer 4 (skills layer) alongside OtterCamp skills. Skills take precedence on conflict.
- Secret injection: secrets NEVER in agent prompts or tool params. Injected at runtime by connector runtime via mcp_secret_binding + secret store. Response sanitizer strips any leaked secrets from output before returning to agent.
- `transport_config` uses `ref:<slug>` inline refs for embedded secret values. Resolved at runtime. See doc 08 secret reference convention.
- Circuit breaker per connection: closed → open (after failure_threshold failures in failure_window_sec) → half-open (after recovery_interval_sec, test call) → closed (if success). Default: threshold=5, window=60s, recovery=30s.
- In-flight calls not cancelled when connection degrades. Circuit breaker counts outcome.
- Three distinct timeouts: per-call (default 30s), health check (default 5s), circuit breaker recovery (default 30s).
- Retry: default 1 retry with exponential backoff. Write tools NOT retried by default (idempotency concern). Attaches idempotency key if server supports it.
- Catalog refresh: on connection establishment, manual request, or periodic health check detects changes. New tools → cataloged disabled. Removed tools → status='removed' (row preserved for audit).
- mcp.catalog.changed domain event emitted when catalog changes.
- Connection is configured conversationally (Frank for org-level, PM for project-level). No MCP config UI.
- `mcp_execution_log.tool_execution_id` links to the control plane ToolExecution record (doc 16). Null for resource reads.
- `mcp_execution_log.run_id` links to the control plane Run (doc 16). Forward reference.
- Connection status lifecycle: configuring → active → degraded → active | failed. disabled → active (re-enabled). failed → configuring (reconfigured).
- Managed mode: flow_node.mcp_tools JSONB column already on flow_node (doc 03 already has it).

**API endpoints defined:**
- GET /mcp/connections, GET /mcp/connections/{id}, POST /mcp/connections, PATCH /mcp/connections/{id}, DELETE /mcp/connections/{id}
- POST /mcp/connections/{id}/refresh, POST /mcp/connections/{id}/test
- GET /mcp/connections/{id}/catalog, PATCH /mcp/connections/{id}/catalog/{entry_id}
- GET /mcp/connections/{id}/executions

**Cross-spec references:**
- `mcp_connection.organization_id` → `organization.id` (doc 04)
- `mcp_connection.project_id` → `project.id` (doc 03)
- `mcp_execution_log.agent_id` → `agent.id` (doc 05)
- `mcp_execution_log.tool_execution_id` → `tool_execution.id` (doc 16)
- `mcp_execution_log.run_id` → `run.id` (doc 16)
- `mcp_secret_binding.secret_ref` → `secret.slug` (doc 08)
- `mcp_connection.transport_config` uses `ref:<slug>` inline secret refs (doc 08)
- `flow_node.mcp_tools` JSONB resolved against mcp_tool_catalog at session start (doc 03)
- MCP prompts load at layer 4 (skills layer) of prompt assembly (doc 05)
- MCP tool calls create ToolExecution records (doc 16)
- mcp.discover is a native tool (doc 05/16 for tool registry)
- Secret store (doc 08) for all credential storage
- MCP capabilities defined in control plane capability model (doc 16)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #6 CONFIRMED: `flow_node.mcp_tools` JSONB column confirmed in doc 09 (consistent with doc 03). flow_node_skill join table remains unresolved until doc 10.
- ISSUE #14 (below): `mcp_execution_log` has a FK to `tool_execution.id` but `tool_execution` table is not defined until doc 16. Forward reference confirmed. Policy_decision nullable because tier 1 resource reads bypass control plane — this is intentional and explicit.
- ISSUE #15 (below): doc 09 says "resource reads are tier 1 (chat-layer) governed by a basic scope check" but does NOT define what constitutes the "basic scope check" — specifically, what query checks whether an agent has access to a connection's project. The capability model for resources is deliberately simpler than tools, but the exact enforcement mechanism (a runtime check? a project membership lookup?) is not specified. Implementers need to know what check to perform.

---

## Doc 08: Deployment and Self-Hosting

**Tables:**
- `secret` — organization_id, name (human label), slug (machine identifier; unique within org), category (model_provider|ssh_key|mcp_credential|external_service), encrypted_value bytea (AES-256-GCM), nonce bytea (unique per encryption), key_version int, created_by_type (human|agent|system), created_by_id, last_rotated_at, expires_at (nullable, informational only), metadata jsonb; unique(organization_id, slug)

**FKs:**
- `secret.organization_id` → `organization.id`
- `secret.created_by_id` → `human_user.id` | `agent.id` | sentinel UUID (via created_by_type; application-layer)

**Polymorphic patterns:**
- `secret`: (created_by_type, created_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID

**Key behavioral rules:**
- Secret reference convention: column-level refs store plain slug (e.g., `provider_connection.api_key_secret_ref`); inline JSON refs use `ref:<slug>` prefix. Both reference `secret.slug`.
- Deletion safety: application-layer enforcement (no SQL FKs on slug refs). Queries known reference columns before allowing delete; blocks if active references exist.
- Encryption: AES-256-GCM with unique nonce per secret. Master key NOT stored in DB. Sources (in order): OTTERCAMP_MASTER_KEY env var → key file → cloud KMS (managed mode). Auto-generate on first run if none provided.
- key_version tracks which master key version encrypted the row; used for re-encryption on rotation.
- Secrets decrypted at runtime only, held in memory for duration of operation, never logged.
- Bootstrap convenience: ANTHROPIC_API_KEY etc. env vars read on first run only. After bootstrap, provider connections in DB are source of truth. Managed mode never uses env vars for tenant secrets.
- Provider connection bootstrap (step-by-step): (1) register model_provider rows, (2) create secret row for API key, (3) create provider_connection row with api_key_secret_ref=slug, (4) create default model profiles, (5) create org-level model_profile_assignment, (6) record bootstrap audit event. This resolves ISSUE #11 — step 5 explicitly creates the org-level assignment.
- MCP YAML config seeds mcp_connection records on first run only. Not re-synced on subsequent starts.
- Schema migrations: embedded in binary, forward-only, numbered, transactional per migration, auto-run on startup. schema_migrations table tracks applied migrations.
- schema_migrations table implied (not DDL-defined in this doc; standard migration tracking table).
- pgvector is a required PostgreSQL extension. First migration enables it via CREATE EXTENSION IF NOT EXISTS vector.
- Deployment modes: local single-node, VPS single-tenant (optional split API/Worker), managed multi-tenant.
- Managed mode: database-per-org isolation. Catalog database maps org slugs to database connections. No RLS.
- Catalog database contents: tenant registry (slug, db connection string, org status, schema version), slug uniqueness (unique index), provisioning state.
- Object storage: filesystem (default/local) or S3-compatible (for VPS/managed).
- Job queue: PostgreSQL LISTEN/NOTIFY + polling. No Redis or external queue.
- Event bus: durable domain_event table + LISTEN/NOTIFY fanout. domain_event table lives in doc 12.
- WebSocket/SSE: no sticky sessions required even with multiple API processes — any process subscribes to PG LISTEN/NOTIFY and fans out.
- Concurrency config: MODEL_CONCURRENCY_GLOBAL=10 default, MODEL_CONCURRENCY_PER_PROVIDER=5 default. Post-bootstrap, per-provider overrides managed via subscription dashboard, stored in organization.settings.
- Health endpoints: GET /health (liveness, no dep checks) and GET /ready (readiness: checks DB, pgvector, object storage, migrations).
- TLS: three options for self-host (reverse proxy, built-in ACME, manual certs). Local mode: no TLS. Managed: TLS at load balancer.
- CLI commands: ottercamp serve, migrate, bootstrap, reset-password, magic-link, unlock-account, backup, restore, secret set/list/delete, version, health.
- Backup: pg_dump + object storage tarball in one command. Restore checks schema version compatibility, drops+recreates DB, runs pg_restore, restores objects.
- Git repos path: GIT_REPOS_PATH env var (default ~/.ottercamp/repos). git repositories are stored locally on the server.
- Worker concurrency: WORKER_CONCURRENCY=5 default, polling via WORKER_POLL_INTERVAL=1s (also LISTEN/NOTIFY wakeup).

**API endpoints defined:**
- GET /health — liveness check
- GET /ready — readiness check

**Cross-spec references:**
- `secret.slug` referenced by `provider_connection.api_key_secret_ref` (doc 07) and `mcp_secret_binding.secret_ref` (doc 09)
- `project_remote.auth_config` jsonb uses `ref:<slug>` for deploy platform credentials (doc 03a)
- SSH keys (secret category: ssh_key) used for git clone/push (doc 03/03a)
- domain_event table defined in doc 12 (event bus backing store)
- Bootstrap audit event (doc 04 step 10)
- Organization.settings stores retention policies, per-provider concurrency overrides, budget controls (docs 02, 07, 13)
- Provider connection bootstrap explicitly creates org-level model_profile_assignment (resolves concern from ISSUE #11)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #11 RESOLVED: doc 08 bootstrap step 5 explicitly creates the org-level model_profile_assignment row. No longer a gap.
- ISSUE #13 (below): `domain_event` table is referenced by doc 08 ("domain events are written to a durable domain_event table — see 12-api-events-and-realtime.md") but doc 12 has not yet been read. This is a forward reference. Flag for confirmation.

---

## Doc 07: Models and Inference

**Tables:**
- `model_provider` — instance-level; slug (unique), name, adapter_type, is_enabled, base_url, metadata jsonb
- `provider_connection` — per-org; organization_id, provider_id, label, is_enabled, api_key_secret_ref (secret.slug reference, not raw key), base_url_override, max_concurrent (nullable), health_status (healthy|degraded|rate_limited|unavailable), last_success_at, last_error_at, last_error_class, rate_limit_remaining, rate_limit_reset_at, failover_priority, total_invocations, total_input_tokens, total_output_tokens, total_errors, metadata jsonb
- `model_profile` — organization_id (nullable for system defaults), logical_profile_id uuid (stable across versions), slug, name, description, provider_id, model_id, version int, is_current boolean, profile_type (agent|system), system_purpose (summarization|listening_eval|memory_extraction|memory_synthesis|null), max_input_tokens, max_output_tokens, temperature numeric(4,2), top_p, top_k, presence_penalty, frequency_penalty, tool_use_enabled, parallel_tool_calls, stream_by_default, per_call_timeout_ms, deterministic boolean, deterministic_seed, fallback_profile_id (references logical_profile_id not id), max_retries, created_by_type, created_by_id, metadata jsonb
- `model_profile_assignment` — organization_id, scope_type (org|project|agent|flow_node), scope_id uuid, profile_id uuid (references logical_profile_id), pinned_version int (nullable); unique(scope_type, scope_id)
- `model_invocation` — organization_id, profile_id (logical), profile_version, provider_id, model_id, connection_id (nullable), session_id, turn_id, agent_id, project_id, task_id, run_id, run_step_id, run_attempt_id, invocation_purpose (agent_turn|listening_eval|summarization|skill_summarization|memory_extraction|memory_synthesis|memory_dedup|memory_reflection|memory_classification|memory_reranking|replay), request_priority (sync_interactive|sync_system|async_agent|async_system), queue_wait_ms, was_preempted, stream_mode, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, time_to_first_token_ms, total_duration_ms, prompt_hash, prompt_storage_ref, response_storage_ref, status (success|error|timeout|cancelled), error_class, error_message, retry_of_invocation_id (self-FK), fallback_of_invocation_id (self-FK), attempt_number, provider_request_id, provider_model_version, metadata jsonb
- `model_usage_rollup` — organization_id, rollup_type (connection|model|agent|project), rollup_id uuid (polymorphic), model_id (for model rollups), period_date, total_invocations, total_input_tokens, total_output_tokens, total_cache_read_tokens, total_cache_write_tokens, total_errors; unique(org, rollup_type, rollup_id, model_id, period_date)

**FKs:**
- `provider_connection.organization_id` → `organization.id`
- `provider_connection.provider_id` → `model_provider.id`
- `model_profile.organization_id` → `organization.id` (nullable; null = system-provided)
- `model_profile.provider_id` → `model_provider.id`
- `model_profile.fallback_profile_id` → `model_profile.logical_profile_id` (application-layer resolve, NOT a SQL FK on the PK)
- `model_profile_assignment.organization_id` → `organization.id`
- `model_profile_assignment.scope_id` → `organization.id` | `project.id` | `agent.id` | `flow_node.id` (via scope_type; application-layer FK, NOT SQL constraint)
- `model_profile_assignment.profile_id` → `model_profile.logical_profile_id` (application-layer resolve, NOT a SQL FK)
- `model_invocation.organization_id` → `organization.id`
- `model_invocation.provider_id` → `model_provider.id`
- `model_invocation.connection_id` → `provider_connection.id` (nullable)
- `model_invocation.session_id` → `chat_session.id` (nullable)
- `model_invocation.turn_id` → `chat_turn.id` (nullable)
- `model_invocation.agent_id` → `agent.id` (nullable)
- `model_invocation.project_id` → `project.id` (nullable)
- `model_invocation.task_id` → `project_task.id` (nullable)
- `model_invocation.run_id` → `run.id` (nullable; doc 16)
- `model_invocation.run_step_id` → `run_step.id` (nullable; doc 16)
- `model_invocation.run_attempt_id` → `run_attempt.id` (nullable; doc 16 — NOTE: run_attempt not yet confirmed in any doc)
- `model_invocation.retry_of_invocation_id` → `model_invocation.id` (self-ref, nullable)
- `model_invocation.fallback_of_invocation_id` → `model_invocation.id` (self-ref, nullable)
- `model_usage_rollup.organization_id` → `organization.id`

**Polymorphic patterns:**
- `model_profile_assignment`: (scope_type, scope_id) → 'org'→organization | 'project'→project | 'agent'→agent | 'flow_node'→flow_node; application-layer only, no SQL FK
- `model_usage_rollup`: (rollup_type, rollup_id) → 'connection'→provider_connection | 'model'→model_provider | 'agent'→agent | 'project'→project; application-layer only, no SQL FK
- `model_invocation`: (created_by_type, created_by_id) → 'human'→human_user | 'agent'→agent | 'system'→sentinel UUID

**Key behavioral rules:**
- model_provider is instance-level (not per-org). provider_connection is per-org (org's API keys).
- An org can have multiple connections to the same provider. Failover: same provider (different connection) first, then cross-provider via fallback_profile_id.
- Connection selection: eligible = is_enabled=true AND health_status != 'unavailable'. Then sort: healthy > degraded (rate_limited = not eligible until reset). Within tier, sort by failover_priority (lower = first).
- Health transitions: healthy → degraded (intermittent errors) → rate_limited (429) → unavailable (persistent auth/quota failures). Recovery via traffic or active health probing (60s for degraded/rate_limited, 5min for unavailable).
- is_enabled (operator disable) is independent of health_status (operational state).
- Profile assignment hierarchy (first non-null wins): flow_node > agent > project > org. System profiles resolved separately — NOT overridable by this hierarchy.
- logical_profile_id is stable across versions. Assignments reference logical_profile_id. SQL unique indexes use (org, slug, version). No SQL FK enforces logical_profile_id uniqueness — application-layer resolves to is_current=true.
- model_profile.fallback_profile_id also references logical_profile_id (not the PK id). Max fallback chain depth = 3. Circular references rejected at config time.
- System profiles have system_purpose field for programmatic lookup. Profile types: 'agent' (assignable) vs 'system' (not assignable to agents).
- Concurrency: global limit (instance-level), per-provider limit (instance-level, further constrainable per-org). Reserved sync slot: always ≥1 slot for sync interactive. Slots held per-model-call, released during tool execution.
- Priority queue: sync_interactive > sync_system > async_agent > async_system. FIFO within tier.
- Soft preemption: most-recently-started async session pauses after current model call when sync request arrives at full capacity. Not cancellation.
- Queue timeouts by priority: sync_interactive=30s, sync_system=15s, async_agent=5min, async_system=30min.
- Error taxonomy: rate_limited, overloaded, timeout, auth_failed, invalid_request, model_unavailable, provider_error, network_error, quota_exhausted, queue_timeout.
- Retryable: rate_limited, overloaded, timeout (once), provider_error (once), network_error (with backoff). Non-retryable+failover: auth_failed, model_unavailable, quota_exhausted, queue_timeout.
- Retry: max 2 retries, base 1s, 2x multiplier, 0-500ms jitter. Rate limit errors use provider's Retry-After header.
- Token tracking: per-invocation full attribution (org, project, agent, session, task, connection). Rollups: daily per connection/model/agent/project. Rollup rows immutable for historical periods.
- No dollar-based cost tracking at V2 launch. Token rollups only. Dollar estimates are display-layer calculations.
- Prompt + response stored in object storage (same store as chat artifacts). References stored in model_invocation. Subject to org redaction policy. Redaction applied at capture time — no un-redacted copy.
- Prompt metadata includes per-layer token counts (all 7 layers), memory injection manifest, compression events, truncation events — stored in model_invocation.metadata.context_assembly.
- Streaming: sync sessions stream by default; async do not. Overridable per-call. When async-to-sync switch mid-turn: current non-streaming call completes normally, streaming takes effect on next model call.
- Deterministic mode: temperature=0, deterministic_seed set. Never production. For testing/replay only.
- api_key_secret_ref references the encrypted secret store (doc 08 or doc 13 — secret.slug convention). API keys never stored in plaintext in this table.
- invocation_purpose values include memory sub-types (dedup, reflection, classification, reranking) and skill_summarization. memory_classification and memory_reranking use the memory_synthesis system profile.
- run_attempt_id FK: spec introduces a `run_attempt` entity referenced here, but doc 16 (not yet read) defines it. NOTE: this is a forward reference.

**API endpoints implied:**
- CRUD on provider connections via settings UI / API
- GET /model/providers, GET /model/profiles
- Subscription dashboard data endpoints

**Cross-spec references:**
- `model_invocation.session_id` → `chat_session.id` (doc 02)
- `model_invocation.turn_id` → `chat_turn.id` (doc 02)
- `model_invocation.agent_id` → `agent.id` (doc 05); `agent.default_model_profile_id` → `model_profile.logical_profile_id`
- `model_invocation.task_id` → `project_task.id` (doc 03)
- `model_profile_assignment` scope_type='flow_node' → `flow_node.id` (doc 03); this is the single source of truth for flow node model overrides (NOT flow_node.metadata)
- `model_invocation.run_id`, `run_step_id`, `run_attempt_id` → doc 16 tables (not yet read)
- Prompt/response object storage same store as chat artifacts (doc 02)
- Failover notifications via inbox items / realtime events (doc 12, not yet read)
- api_key_secret_ref → encrypted secret store (doc 08, not yet read)
- Token rollups feed operational dashboard (doc 13, not yet read)
- Budget tracking at agent level uses tokens (doc 05/14: budget_cap_tokens)

**Gaps/conflicts with iteration 1 docs:**
- ISSUE #11 (below): doc 05 says `agent.default_model_profile_id` references `model_profile.logical_profile_id`. Doc 07 confirms this is a logical reference (not a FK to the PK), resolved application-layer via `WHERE logical_profile_id = $1 AND is_current = true`. This is consistent BUT: `model_profile.organization_id` is nullable (null = system-provided). An agent in org A can reference a system-provided profile (org_id=null). However, when an org deletes a system profile override and falls back to the system default, the model_profile_assignment row must be absent — there is no "inherit system default" flag on the assignment row. If the org default profile row is missing, the spec says "the request fails with a clear error." This means orgs MUST have an org-level assignment or the system will fail. Who creates the org-level assignment? The bootstrap sequence (doc 14 step 5) says "seed model profiles (high-capability, standard, haiku)" but does not explicitly say "create org-level model_profile_assignment rows." Clarify whether bootstrap step 5 also creates the org-level assignments, or whether the gateway falls back to system profiles when no org assignment exists.
- ISSUE #12 (below): `model_invocation` has a `run_attempt_id` FK referencing a `run_attempt` table. This entity is introduced by doc 07 but defined in doc 16. It is a forward reference — no `run_attempt` table has been defined in any doc read so far. Flag for confirmation when reading doc 16.

---

## Iteration 2 Status: COMPLETE

Docs read: 06, 07, 08, 09, 10, 16

## Iteration 3 Status: COMPLETE

Docs read: 11, 12, 13, 15, 20, 21, 17, 18, 19

---

## Doc 11: System Integration — CLI and Browser

**Tables:**
- `cli_execution` — run_id, run_step_id, task_id, project_id (denorm), agent_id, command, working_dir, risk_level (safe|normal|sensitive|dangerous), policy_decision (allow|deny), exit_code (nullable), stdout_preview, stderr_preview, stdout_artifact_id (→run_artifact.id), stderr_artifact_id (→run_artifact.id), duration_ms, status (pending|running|completed|failed|cancelled|denied), env_vars jsonb (keys only, no values), network_policy (allow_all|deny_all|allowlist), timeout_ms, started_at, completed_at, metadata jsonb
- `browser_session` — task_id, project_id (denorm), agent_id, status (active|suspended|closed), domain_policy (allow_all|denylist|allowlist), domain_rules jsonb, credential_refs jsonb (secret name refs, NOT values), current_url, current_title, last_action_at, suspended_at, closed_at, close_reason (task_completed|task_cancelled|idle_timeout|revoked|manual), metadata jsonb
- `browser_action` — browser_session_id, run_id, run_step_id, action_type (navigate|click|type|select|hover|scroll|press_key|screenshot|extract_text|extract_structured|get_page_info|wait_for|wait_for_navigation|back|forward|refresh), action_params jsonb, description, page_url_before, page_url_after, success boolean, error_message, screenshot_artifact_id (→run_artifact.id), extracted_data jsonb, policy_decision (allow|deny), duration_ms, completed_at, metadata jsonb
- `browser_handoff` — browser_session_id, inbox_item_id (→inbox_item.id), run_id, reason (captcha|two_factor|payment|agent_request), agent_description, page_url, screenshot_artifact_id (→run_artifact.id), status (pending|in_progress|completed|expired), human_completed_at, post_handoff_screenshot_id (→run_artifact.id), expires_at, metadata jsonb

**FKs:**
- `cli_execution.run_id` → `run.id` (doc 16)
- `cli_execution.run_step_id` → `run_step.id` (doc 16)
- `cli_execution.task_id` → `project_task.id` (doc 03)
- `cli_execution.project_id` → `project.id` (doc 03)
- `cli_execution.agent_id` → `agent.id` (doc 05)
- `cli_execution.stdout_artifact_id` → `run_artifact.id` (doc 16, nullable)
- `cli_execution.stderr_artifact_id` → `run_artifact.id` (doc 16, nullable)
- `browser_session.task_id` → `project_task.id` (doc 03)
- `browser_session.project_id` → `project.id` (doc 03)
- `browser_session.agent_id` → `agent.id` (doc 05)
- `browser_action.browser_session_id` → `browser_session.id`
- `browser_action.run_id` → `run.id` (doc 16)
- `browser_action.run_step_id` → `run_step.id` (doc 16)
- `browser_action.screenshot_artifact_id` → `run_artifact.id` (doc 16, nullable)
- `browser_handoff.browser_session_id` → `browser_session.id`
- `browser_handoff.inbox_item_id` → `inbox_item.id` (doc 03)
- `browser_handoff.run_id` → `run.id` (doc 16)
- `browser_handoff.screenshot_artifact_id` → `run_artifact.id` (doc 16, nullable)
- `browser_handoff.post_handoff_screenshot_id` → `run_artifact.id` (doc 16, nullable)

**Polymorphic patterns:** none new

**Key behavioral rules:**
- CLI and browser are tier 2 tools — all invocations route through control plane policy evaluation before execution
- CLI risk levels: safe (always allowed if capability granted), normal (allowed by default), sensitive (denied by default, configurable), dangerous (denied)
- Default CLI denylist: system destruction (rm -rf /), system control (shutdown/reboot/halt), privilege escalation (sudo/su), system modification (systemctl/service/etc), process interference (kill -9 1/killall), network reconfiguration (iptables/ifconfig)
- Compound CLI commands decomposed; overall risk = maximum of all components
- Working directory scoped to project git repo at task branch HEAD. Path traversal (../) that escapes repo is rejected.
- Constructed environment (not inherited): always-set vars, project-config injected vars, org-secret injected credentials. Blocked: OPENAI_API_KEY, ANTHROPIC_API_KEY, OtterCamp internal service credentials.
- CLI output inline limit: 50KB per stream (stdout/stderr). Total capture limit: 10MB per command. Excess stored as RunArtifact.
- CLI streaming events: cli.stdout, cli.stderr, cli.exit — map to doc 16 run_event type 'output_chunk'
- cli_execution status 'denied' — record created even when command is denied for audit purposes
- env_vars on cli_execution stores only KEYS, never values
- Browser sessions: task-scoped (NOT run-scoped). One active browser session per task. Persist across multiple runs.
- Browser session lifecycle: active → suspended (task on_hold) → closed (task done/cancelled/idle/revoked/manual)
- Browser session idle timeout: 1 hour default. Cleanup on task completion/cancellation.
- Automatic screenshots after every navigation, every interaction, every error — stored as RunArtifacts, NOT returned to agent inline
- Agent calls browser.screenshot() explicitly when it needs to see page state (included in tool result for vision)
- Domain policy: allow_all (default), denylist, allowlist. Sensitive domains (financial/auth/admin/email) denied by default.
- Credentials never in agent prompts — injected into browser context at session creation time by worker runtime
- Human handoff: agent-initiated (not policy-triggered). Creates inbox_item (item_type='browser_handoff'). Run pauses. Handoff expires after 24 hours default.
- Handoffs are one-way: human completes action → signals completion → agent resumes. No back-and-forth.
- Git operations are regular CLI tool calls. No push to main (merge queue only). No force-push to shared branches. No branch deletion. Force-push to own task branch allowed when push enabled. Git push classified as 'sensitive'.
- Browser per-action timeout: 30s for interactions, 60s for navigation (configurable). Browser sessions: 1-hour idle timeout.
- Revocation immediate: in-flight ops finish, next tool call returns "not permitted"
- Container-level isolation deferred (future); current model is process-level isolation

**API endpoints:** none new beyond doc 16 control plane endpoints

**Cross-spec references:**
- cli_execution.run_id, run_step_id → run/run_step (doc 16)
- cli_execution.stdout/stderr_artifact_id → run_artifact (doc 16)
- browser_handoff.inbox_item_id → inbox_item (doc 03) — RESOLVES ISSUE #3: browser_handoff fully defined here
- browser_session scoped to project_task (doc 03)
- Capabilities: system.cli.execute, system.browser.navigate/.interact/.screenshot/.extract (doc 16 capability policy)
- Secret injection via org secret store (doc 08)
- Project network policy stored in project config (doc 03/08)
- CLI/browser RunEvents reference doc 16 run_event table
- Tools registered in tool registry (doc 20, not yet read)
- Artifact retention follows org-level retention policy (doc 13, not yet read)

**Gaps/conflicts:**
- ISSUE #3 RESOLVED: browser_handoff table defines inbox_item_id → inbox_item.id. item_type='browser_handoff' on inbox_item is the link. The handoff reason values are: captcha, two_factor, payment, agent_request.
- ISSUE #19 (new, see ISSUES.md): cli_execution references run_step_id as NOT NULL, but doc 16's run_step table has tool_execution records that can have nullable run_step_id. Clarify whether run_step_id is always required for cli_execution or can be null for direct-run executions.

---

---

## Doc 17: TUI (Backend API Contracts and Figma Gaps Only)

**Backend API contracts required by TUI:**
- SSE event stream: `GET /v1/events/stream?scopes=org:{id},project:{id},session:{id},task:{id}` — used for real-time chat streaming, task status updates, inbox updates, merge queue updates, activity feed, agent status, unread indicators. Last-Event-ID header for reconnect/catch-up.
- Session list: `GET /v1/chat-sessions?scope=...` — sidebar session list, grouped by scope
- Dashboard data: org projects summary, inbox count, recent activity — requires: `GET /v1/projects`, `GET /v1/inbox`, `GET /v1/events?scope=org:{id}&limit=50` (or similar)
- Task board: `GET /v1/projects/{id}/tasks?status=...` — kanban columns by work status
- Task detail: `GET /v1/tasks/{id}` — full task details including flow state, subtasks, dependencies
- Inbox list: `GET /v1/inbox` — inbox items with status, type, action_payload
- Agent status: `GET /v1/agents` — list agents with status, current work (requires run association)
- Merge queue: `GET /v1/projects/{id}/merge-queue` — merge queue entries
- Schedules: `GET /v1/projects/{id}/schedules` — task schedules
- Message send: `POST /v1/chat-sessions/{id}/messages` — from chat input area
- Turn cancel: `POST /v1/chat-sessions/{id}/cancel-turn` — Escape key during streaming
- Message queue actions: steer (`POST /v1/chat-sessions/{id}/messages/{mid}/steer`), edit (`PATCH /v1/chat-sessions/{id}/messages/{mid}`), delete (`DELETE /v1/chat-sessions/{id}/messages/{mid}`)
- Reactions: `POST /v1/chat-sessions/{id}/messages/{mid}/reactions` (+/-), `DELETE /v1/chat-sessions/{id}/messages/{mid}/reactions/{rid}`
- Inbox actions: `POST /v1/inbox/{id}/act` (approve/reject/defer)
- @mention autocomplete: requires `GET /v1/agents?scope=...` for name lookup

**Agent status view requires join-like data:** agent status + current_run must provide agent_id with their active run task. Backend needs to either: (a) include current task info on agent response, or (b) TUI queries runs separately. No current endpoint returns "which task is this agent currently working on?"

**SSE events consumed by TUI (from doc 12):**
- chat.message.created, chat.message.delta, chat.message.finalized, chat.turn.started, chat.turn.completed, chat.listening_eval.completed
- task.status_changed, task.flow.advanced, task.subtask.completed
- inbox.item.created, inbox.item.acted
- agent.activated, agent.paused (for agent status view)
- project.merge.queued, project.merge.completed, project.merge.conflict
- system.schedule.fired, system.schedule.skipped

**Figma gaps from doc 17:**
- Scope indicator component (chat pane top): scope pills (Task / Project / Org) with active highlight. `[`/`]` navigation.
- Message queue section: queued messages below active turn, with inline action hints `[e]dit [s]teer [d]elete`
- Tool call inline rendering: compact status lines within agent message (`> read_file() ... done`)
- Active turn indicator: agent name + elapsed time + tool activity + `[Esc to cancel]`
- Flow stepper text representation: `[x Done] -> [* Active] -> [ Pending]` — terminal text form of flow visualization
- Reaction indicators below messages: `[+1]` / `[-1 "note"]` compact form
- Unread bubble-up in sidebar: `*` on task session bubbles to project grouping

---

## Doc 21: Testing

**Tables:** none (no test-specific tables — test infrastructure is application-level, not schema-level)

**OTTERCAMP_MODE=test flag behavior:**
- Set via environment variable. Read once at startup, immutable at runtime. Default = production mode.
- Test mode enables: deterministic model responses (doc 07 deterministic mode), state reset API, synthetic time control, test-only seeding endpoints, relaxed timeouts (configurable multiplier, default 3x via OTTERCAMP_TEST_TIMEOUT_MULTIPLIER).
- Test mode does NOT disable: auth, policy evaluation, sandboxing, control plane enforcement, audit logging.
- OTTERCAMP_MODE value is NOT stored in database.

**State reset API endpoints (test mode only):**
- `POST /test/reset` — truncates all tables, re-runs bootstrap. Returns org_id, user credentials, API key. Does NOT exist in production mode.
- `POST /test/time/advance` — advances system clock by specified duration. Requires injectable clock abstraction in all time-dependent code.
- `POST /test/seed/memories` — bulk load memories with specific content, confidence, source types
- `POST /test/seed/invocations` — create model invocation records for cost/budget testing
- `POST /test/seed/events` — inject domain events for testing event subscribers

**3-layer test architecture:**
- **Unit tests (Layer 1)**: all packages in isolation, mocking model providers/DB/filesystem/external services. 90%+ line coverage minimum. 95%+ for critical paths (policy, tools, prompt assembly, flow, memory). Must complete < 2 minutes. Test files alongside source: policy.go → policy_test.go. Table-driven tests for combinatorial cases.
- **Integration tests (Layer 2)**: real PostgreSQL, real schema migrations, recorded provider responses. Each suite gets own DB (created/dropped by harness, cloned from migrated template). Recorded responses in testdata/responses/ committed to version control. In-process API server. Covers: DB queries/constraints, event bus LISTEN/NOTIFY delivery, model gateway routing, auth, memory pipeline, tool execution.
- **E2E tests (Layer 3)**: full OtterCamp instance in test mode. Real auth, real policy, real control plane. CLI binary + HTTP client as test clients. Before each test: POST /test/reset. Must complete < 10 minutes.

**Coverage gates:**
- Overall minimum: 90% line coverage
- Critical packages (policy, tools, prompt, flow, memory): 95% minimum
- Coverage delta: no merge if coverage decreases > 1% from base branch
- New .go files without _test.go files are flagged by CI

**CI pipeline stages (total budget: 15 minutes):**
1. Lint (< 30s)
2. Unit tests (< 2min) — coverage report
3. Build (< 1min)
4. Integration tests (< 5min) — requires step 3
5. E2E tests (< 10min) — requires step 3
6. Coverage gate — requires steps 2, 4, 5

**Key behavioral rules:**
- Model response fixtures: stored in testdata/responses/ organized by scenario (chat/, flow/, memory/). Version-controlled test fixtures. Can optionally call live providers behind OTTERCAMP_TEST_LIVE_MODELS=true env var.
- No internet access during tests (except gated live-model tests).
- Browser tests: headless Chrome in Docker.
- Injectable clock abstraction: ALL time-dependent code reads from this abstraction. In test mode, time is controllable via /test/time/advance. In production, returns real system time.
- Recorded model responses: include prompt hash, provider response, token counts, latency. Replayed deterministically in tests.
- E2E test uses POST /api/sessions/{id}/turns (note: different path than /v1/ — confirm consistency with doc 12 endpoint catalog).

**Critical unit test cases explicitly required:**
- Policy evaluation: layers in order, deny overrides allow, instance safety unoverridable
- Tool resolution: all 4 stages, glob matching, tier 1 bypasses capability gate
- Prompt assembly: 7 layers in order, budget truncation, skill activation rules
- Flow progression: transitions, rejection loops, visit counter, dependency enforcement
- Memory retrieval: 4-stage pipeline, trust tiers, dedup, passive injection dedup

**Cross-spec references:**
- doc 07 deterministic mode → used for test model responses
- doc 08 OTTERCAMP_MODE env var → deployment configuration
- doc 15 JSONL import → testdata/import/ fixtures
- doc 12 API endpoints → used for E2E verification; NOTE: doc 21 uses /api/ prefix, doc 12 uses /v1/ prefix — possible inconsistency

**Gaps/conflicts:**
- ISSUE #27 (new): doc 21 E2E test examples use paths like `POST /api/sessions/{id}/turns` and `GET /api/projects`, while doc 12 defines all routes under `/v1/` prefix (e.g., `POST /v1/chat-sessions/:id/messages`). Either doc 21's examples use a shorthand, or the test mode uses a different API prefix. Additionally, doc 21 refers to `POST /api/sessions/{id}/turns` as a way to send a message, but doc 12 defines message sending as `POST /v1/chat-sessions/:id/messages`. Both the path prefix (/api vs /v1) and the resource name (sessions vs chat-sessions) differ. Clarify whether the doc 21 examples are accurate test code or illustrative pseudocode, and confirm the authoritative API paths are from doc 12.

---

## Doc 20: Tools and Tool Policy

**Tables:**
- `tool_definition` — name (unique text), domain (project|chat|memory|agent|system|browser|communication), category (native|system|browser|external), tier (int: 1 or 2), description, parameter_schema jsonb, return_schema jsonb (optional), required_capability text (null for tier 1), is_active boolean (default true), version int; populated at application startup from code, not user-editable
- `session_tool_set` — session_id, agent_id, tool_names text[] (ordered), tool_versions jsonb, mcp_tools jsonb (array of {connection_id, tool_name, schema_hash}), resolved_at; unique(session_id, agent_id)

**FKs:**
- `session_tool_set.session_id` → `chat_session.id`
- `session_tool_set.agent_id` → `agent.id`
- `tool_definition` has no FKs (standalone registry)

**Polymorphic patterns:** none new

**Key behavioral rules:**
- Four tool categories: native (shipped with OtterCamp), system (CLI/file/git), browser (web interaction), external (MCP and remote APIs)
- Two-tier execution model: tier 1 (read-only, chat-layer, scope check only) vs tier 2 (mutations + external, full control plane path)
- Default is tier 2. Tier 1 is a strict whitelist. Tools never move from tier 2 to tier 1.
- All external tools are ALWAYS tier 2 (no exceptions). All browser tools are ALWAYS tier 2.
- Tier 1 complete whitelist: project.list/get, task.list/get, subtask.list/get, flow.get_template/get_execution, inbox.list, merge_queue.status, schedule.list, session.list/get/history, memory.query, agent.list/get, file.read/list/search, git.status/diff/log
- Tier 1 permission check is scope-based (is data within agent's current scope?), NOT policy-based.
- Four-stage tool resolution pipeline (runs once at session start, cached for session lifetime): (1) Universe = all native + enabled external in scope; (2) Agent profile filter (allow/deny globs); (3) Flow node filter (soft deprioritize, NOT hard exclude); (4) Capability gate (exclude tier 2 tools without matching capability)
- Flow node may declare optional `tool_domains` jsonb field for stage 3 soft filtering (new field on flow_node table — adds to ISSUE #16).
- Tool descriptions are layer 7 of 7-layer prompt assembly (lowest priority, dropped first under budget pressure). Full catalog ~55 tools ≈ 4,000-5,000 tokens.
- Session tool set is cached and stable. Configuration changes take effect on NEXT session, not current. Mid-session capability revocation enforced at EXECUTION TIME (not prompt removal).
- Communication tools (email.compose, slack.post) create draft_action_review inbox items as DESIGNED BEHAVIOR, not policy interception. Tool succeeds; agent's turn continues immediately; agent not notified of inbox decision within same turn.
- Agent profile allow/deny uses glob patterns. `project.*`, `github.*` (MCP connection slug), `browser.*`, `*`. Deny always wins over allow.
- Starter trio tool policies: Frank = project/task/subtask/flow/session/memory/agent/inbox/schedule (no system/browser/external); Lori = agent.* + read tools; Ellie = memory.* + session.history + file.read/search.
- Tool names use connection SLUGS for prompt clarity (github.create_issue). Capability names use connection UUIDs for stable policy references (mcp.tool.invoke:<uuid>:<tool_name>).
- External tools default to is_enabled=false until admin explicitly enables via conversation.
- Parallel execution: tier 1 in parallel, tier 2 sequential. Mixed tier: tier 1 first (parallel), then tier 2 (sequential).
- Tool timeouts: tier 1 = 5s, tier 2 native = 10s, CLI = 60s, browser = 30s, external = 30s per-connection configurable.
- No batch tools. No dynamic tool generation (agents cannot create tools at runtime).
- flow_node.tool_domains: new optional jsonb field on flow_node — NOT mentioned in doc 03, 09, 10. This adds to the flow_node schema gaps (ISSUE #16 already tracks this).
- git.commit requires `system.file.write` capability — same as file.write/delete.

**Native tool catalog (55 tools, 7 domains):**
- Project/Task: project.list/get/create/update, task.list/get/create/update/add_dependency/remove_dependency, subtask.list/get/create/update, flow.get_template/get_execution/advance/review_decision/create_template, inbox.list, merge_queue.status, schedule.list/create/update/delete (25 tools)
- Chat/Session: session.list/get/history/create/invite_agent, message.send (6 tools)
- Memory: memory.query (T1), memory.record (T2) (2 tools)
- Agent: agent.list/get/create_temp/update (4 tools)
- System: file.read/list/search/write/delete, cli.execute, git.status/diff/log/commit (10 tools)
- Browser: browser.navigate/click/type/screenshot/extract/evaluate (6 tools)
- Communication: email.compose, slack.post (2 tools)

**Cross-spec references:**
- mcp_tool_catalog (doc 09) + tool_definition = full tool universe
- tool_execution (doc 16) = control plane record for tier 2 calls
- chat_message (doc 02) = tier 1 calls recorded as tool_call/tool_result messages
- flow_node.tool_domains: new optional field on flow_node table (doc 03) — not yet specified in doc 03 DDL
- agent profile allow/deny lists (doc 05)
- capability policy (doc 16) = stage 4 of resolution pipeline
- layer 7 of 7-layer prompt assembly (doc 05)
- communication tools create draft_action_review inbox items (doc 03 inbox_item.item_type)

**Gaps/conflicts:**
- ISSUE #25 (new): doc 20 introduces `flow_node.tool_domains` as a new optional jsonb field on the `flow_node` table (used in stage 3 of tool resolution as a soft deprioritization signal). This field is NOT mentioned in doc 03 (flow_node DDL), doc 09 (which only adds `mcp_tools`), or doc 10. This adds a THIRD new field to flow_node beyond what any single doc defines: (a) drop `skills` jsonb (doc 10), (b) add `mcp_tools` jsonb (doc 09), (c) add `tool_domains` jsonb (doc 20). Doc 03 must be updated to reflect all three changes. ISSUE #16 should be updated accordingly.
- ISSUE #26 (new): doc 20 defines `browser.evaluate` as a browser tool ("Execute JavaScript in the page context"). Doc 11 (System Integration) defines the browser action API and does NOT include an `evaluate`/JS execution tool. Doc 11's API is: navigate, click, type, select, hover, scroll, press_key, screenshot, extract_text, extract_structured, get_page_info, wait_for, wait_for_navigation, back, forward, refresh. No JavaScript evaluation. This is a direct contradiction — doc 20 says browser.evaluate exists; doc 11 does not define it and does not mention JavaScript execution. Clarify: (a) does browser.evaluate exist? (b) is it intentionally excluded from doc 11 for security reasons? (c) if it should exist, what are its sandbox constraints?

---

## Doc 15: Migration and Backward Compatibility

**Tables:** none new

**Key behavioral rules:**
- V2 is a clean-room rebuild. No V1 code, schema, or data reused. No migration shims.
- Only data bridge: JSONL memory import (CLI-only, permanent capability). Uses doc 06's standard extraction pipeline. No stages skipped.
- Imported memories: source_type='import', trust_tier=0.6 (medium-low, per doc 06 source trust tiers).
- Bootstrap is idempotent — detected by checking for any organization row. Skips if org exists.
- Bootstrap minimum dataset (authoritative from doc 14 + confirmed here): Frank, Lori, Ellie (starter trio), 4 default flow templates ("Single Step", "Work + Review", "Work + Code Review + Human Review", "Research"), 2 org-default skills (Safety and Communication Policies, General Work Standards), 3 model profiles (high-capability/Opus, standard/Sonnet, haiku/Haiku), default org policy, "General" org session, one bootstrap audit event.
- V1 archives retained indefinitely in cold storage. Never queried at runtime.
- Rollback strategy is forward-only (fix in V2). V1 restoration is last resort (OpenClaw dependency risk + data loss of V2-era work).
- Starter trio profile updates on upgrade: OPEN QUESTION (flagged from doc 04). Bootstrap is idempotent and skips, so a separate upgrade mechanism is needed. Options deferred.

**Cross-spec references:**
- Bootstrap sequence: doc 14 is authoritative (10-step sequence). Doc 15 confirms and expands details.
- JSONL import: doc 06 memory importer is the only data bridge.
- Default flow templates: doc 03 system-provided templates (project_id=null).
- Default skills: doc 10 org-scope skills with is_default=true.
- Default model profiles: doc 07 system-provided profiles with organization_id=null.
- Bootstrap org policy: doc 16 capability_policy defaults.

**Gaps/conflicts:**
- ISSUE #24 (new): doc 15 open question: "Starter trio profile updates on upgrade: when a new OtterCamp version ships with updated prompt packs, policies, or tool configurations for the starter trio (Frank, Lori, Ellie), how are those applied to existing installs?" Doc 14 resolved this partially (system_prompt updated; operator_instructions field never overwritten) but doc 15 still lists it as an open question. Gap: is the doc 14 partial resolution sufficient for implementation, or does a specific upgrade mechanism (migration script, CLI command, version check) need to be designed?

---

## Doc 13: Security, Observability, and Cost Controls

**Tables:**
- `token_budget` — organization_id, project_id (nullable — null = org-level budget), period_type (daily|weekly|monthly), soft_limit bigint (tokens, nullable), hard_limit bigint (tokens, nullable), created_by_type (human|system), created_by_id uuid; unique(organization_id, coalesce(project_id, sentinel_uuid), period_type); index on organization_id
- `trace_span` — implied (not DDL-defined in this doc); append-only, partitioned by day, 7-day retention default; OTLP-compatible; stores distributed trace spans with start/end timestamps, status, attributes, parent span ID

**Cross-reference tables confirmed by doc 13 (not new DDL, but authoritative statements about existing tables):**
- `secret` (doc 08) — categories: model_provider|ssh_key|mcp_credential|external_service; encrypted via AES-256-GCM; master key from OTTERCAMP_MASTER_KEY env var or KMS; key_version tracks master key rotation
- `model_usage_rollup` (doc 07) — retained indefinitely (rollups survive 30-day raw invocation purge)
- `model_invocation` (doc 07) — 30-day retention (shorter than doc 14's stated 90 days)

**FKs:**
- `token_budget.organization_id` → `organization.id`
- `token_budget.project_id` → `project.id` (nullable)
- `token_budget.created_by_id` → `human_user.id` (when created_by_type='human') | sentinel UUID (when 'system'); application-layer only

**Polymorphic patterns:**
- `token_budget`: created_by_type in ('human', 'system') — no 'agent' type here (only humans and system create budgets)

**Key behavioral rules:**
- Defense-in-depth: 6 layers: (1) org isolation/db-per-org, (2) auth, (3) RBAC+capability authorization, (4) control plane gating, (5) agent sandboxing, (6) audit trail
- Authorization posture is PERMISSIVE via capability templates (not default-deny). Instance safety policy is the only default-deny floor.
- Budget enforcement is at TWO levels: org and project. (Agent-level budget via agent.budget_cap_tokens in doc 05 is an OPEN QUESTION per doc 13 Q1 — units conflict: doc 05 uses cents, doc 13 uses tokens)
- Hard budget limit: fail CLOSED — block non-essential capabilities. Do NOT silently degrade to cheaper model. Essential capabilities (blocker filing, status updates) remain allowed.
- Soft budget limit: warn once per period. Warning appears in activity feed + usage dashboard. Run proceeds.
- Anomaly detection: background job every 15 minutes. If trailing-60-min token usage > 3x 7-day hourly average → alert. Max one spike alert per hour per org.
- 5 codebase-wide secret safety invariants: never in prompts, never in logs, never in API responses, never in audit events, never in memory. Scrubbing layer enforces.
- Log scrubbing replaces known secret patterns with [REDACTED]. Patterns: API key prefixes (sk-, key-), SSH private key headers, bearer token values, base64 blobs >32 chars in secret-adjacent fields.
- Trace retention: 7 days default, configurable. Error traces always recorded regardless of sampling rate.
- Retention enforcement: daily background job. Chat transcripts: 90 days. Run records + events: 90 days. Model invocation logs: 30 days. Domain events: 90 days. Audit events: 1 year (self-hosted), 3 years (managed). Memories (active): indefinite. Memories (archived): 1 year. Object storage artifacts: 90 days. Trace spans: 7 days.
- NOTE: doc 13 retention for model invocations = 30 days. Doc 14 resolution says 90 days. INCONSISTENCY logged as ISSUE #21.
- Retention enforcement order: for chat/invocation logs, archive to object storage before deletion if org has archival enabled. Audit events: archive before deletion, never hard-delete without archival.
- Data deletion: delete project → memories archived (not deleted) with reason 'project_deleted'. Delete agent → soft-delete only; audit trail integrity preserved.
- Org wipe: deletes all org data except audit events. CLI-only. Requires typing org slug. Not exposed in web UI.
- GDPR-aware design: right to access (export API), erasure (deletion workflow), portability (JSON archive), minimization (retention policies). Not certified at GA.
- Circuit breakers on external deps: model providers, MCP servers, git remotes. Open after 5 failures or >50% error rate over 1 min. Half-open after 30s.
- Usage dashboard data source: model_usage_rollup for 30d/90d views (pre-aggregated). Raw model_invocation for 7d and real-time drill-downs.
- Inference context replay: stored in model_invocation.metadata — per-layer token counts, memory injection manifest, compression events, truncation events. Enables "why did agent do X?" reconstruction.
- Metrics endpoint: /metrics (Prometheus-compatible, pull model). Optional OTLP push for managed.
- Health endpoints (per doc 08): /health/live (liveness) and /health/ready (readiness + deps). Doc 13 references /health/live and /health/ready — slight naming difference from doc 08 which uses /health and /ready. ISSUE #22.

**Retention table (authoritative from doc 13):**
| Data Type | Default Retention |
|---|---|
| Chat session transcripts | 90 days |
| Memory items (active) | Indefinite |
| Memory items (archived) | 1 year |
| Audit events | 1 year (self-hosted), 3 years (managed) |
| Run records and events | 90 days |
| Model invocation logs | 30 days |
| Domain events | 90 days |
| Trace spans | 7 days |
| Object storage artifacts | 90 days |
| idempotency_key | 24h TTL |
| job_queue completed jobs | Daily purge |

**API endpoints implied:** None new. /metrics (Prometheus), /health/live, /health/ready already defined in other docs.

**Cross-spec references:**
- secret table (doc 08) — AES-256-GCM encrypted
- model_usage_rollup (doc 07) — usage dashboard data source, retained indefinitely
- model_invocation (doc 07) — per-request token tracking, 30-day retention
- audit_event (doc 04) — never hard-deleted without archival
- token_budget.project_id → project (doc 03)
- budget enforcement in control plane broker (doc 16)
- trace_span partitioned table — not DDL-defined here; implied infrastructure
- Operational dashboards in web UI (doc 18, not yet read)

**Gaps/conflicts:**
- ISSUE #21 (new): doc 13 says model invocation log retention = 30 days. Doc 14 resolution Q says 90 days for model invocations. These are contradictory. Need definitive answer.
- ISSUE #22 (new): doc 13 says health endpoints are /health/live and /health/ready. Doc 08 defines /health (liveness, "no dep checks") and /ready (readiness). Different paths. Need canonical path names.
- ISSUE #23 (new): doc 13 has an open question explicitly stating the per-agent budget system conflicts with the org/project token_budget system: "Doc 05 uses cents (budget_cap_cents) while doc 13's token_budget system is entirely token-based — these units are incompatible. Either the agent-level budget should be converted to tokens (and the column renamed), or a conversion layer is needed." This confirms ISSUE #1 (budget_cap_cents vs tokens) and extends it: the budget enforcement path in doc 16 cannot compare across org/project/agent levels until this is resolved. Marking as BLOCKER.

---

## Doc 12: API, Events, and Realtime Contracts

**Tables:**
- `domain_event` — id (uuid), seq (bigint GENERATED ALWAYS AS IDENTITY — strict total ordering), type (text), occurred_at, actor_type (human|agent|system|supervisor), actor_id (uuid nullable), organization_id, project_id (nullable), task_id (nullable), session_id (nullable), payload jsonb, metadata jsonb; unique index on seq; scope indexes on (org_id, seq), (project_id, seq), (task_id, seq), (session_id, seq), (type, seq); retention index on occurred_at
- `consumer_cursor` — consumer_name (PK text), last_seq (bigint default 0), last_event_at, updated_at; four named consumers: realtime_push, memory_pipeline, notification_evaluator, internal_reactions
- `job_queue` — id, job_type, priority (int), payload jsonb, status (pending|claimed|running|completed|failed|dead_letter), attempts, max_attempts (default 3), claimed_by, claimed_at, started_at, completed_at, failed_at, error_message, error_details jsonb, result jsonb, run_after (timestamptz, for delayed/retry), created_at, updated_at
- `idempotency_key` — key (text PK), method, path, request_hash (SHA-256), response_status, response_body jsonb (null for 204), created_at, expires_at; index on expires_at for cleanup

**FKs:**
- `domain_event.organization_id` → `organization.id` (structural consistency; tenant isolation via db-per-org)
- `domain_event.actor_id` → `human_user.id` | `agent.id` (via actor_type; application-layer; may be null for system/supervisor events)
- No explicit FK constraints on project_id, task_id, session_id on domain_event (denormalized for query; not enforced at DB level)
- `consumer_cursor` has no FKs (standalone)
- `job_queue` has no FKs (standalone; payload contains entity IDs)
- `idempotency_key` has no FKs (org-scoped implicitly via db-per-org)

**Polymorphic patterns:**
- `domain_event`: actor_type in ('human','agent','system','supervisor') — note: 'supervisor' is an extension beyond the canonical 3-type principal convention of docs 04/05/16. All code switching on actor_type must handle 4 values.
- `domain_event`: (scope fields) are NOT a polymorphic FK pair — they are independent nullable columns; an event may have project_id AND task_id AND session_id simultaneously

**Key behavioral rules:**
- API-first principle: every feature accessible via REST API. No UI-only features. Web UI is an API client.
- All routes under /v1/. Version in URL path, not headers.
- REST for CRUD; explicit command endpoints (POST verb-noun) for actions with side effects (e.g., POST /tasks/:id/advance-flow). PATCH to status fields is wrong for state transitions.
- Consistent envelope: {data, meta} for success; {error, meta} for failures. data never null (empty list = []). meta always present with request_id + timestamp.
- Cursor-based pagination only. Default page_size=50, max=200. Cursors are opaque.
- Idempotency: all mutations accept Idempotency-Key header. Stored for 24-hour TTL. Same key + same request = replay stored response. Same key + different request = 409.
- No API-layer rate limiting. Rate limiting only at model layer (doc 07).
- All requests scoped to organization (from auth context). Cross-org access impossible.
- domain_event.seq is BIGSERIAL identity — strict total ordering, unique even within same transaction.
- Events written transactionally with domain operations (same DB transaction). LISTEN/NOTIFY wake-up after commit.
- At-least-once delivery to consumers. Consumers must be idempotent.
- Each consumer tracks own position via consumer_cursor.last_seq. A slow consumer does not block others.
- Retention: default 90 days for events. Before purging, system checks all consumer cursors advanced past boundary. Consumer stalled >24h → operator alert.
- SSE as default realtime transport: /v1/events/stream?scopes=org:id,project:id,task:id,session:id
- SSE id field = event.seq (monotonic integer). Last-Event-ID for reconnect = simple seq > $last_seq comparison.
- If SSE buffer overflows (default 1000 events), server drops connection. Client reconnects with Last-Event-ID.
- If seq referenced by Last-Event-ID was purged, server responds with oldest available + X-Events-Gap: true header.
- WebSocket: /v1/ws — bidirectional, for typing indicators and live-pairing. Not required for normal chat.
- API key scope enforcement on SSE: oc_chat_ → only session:{id} scopes + chat.* events; oc_read_ → any scope; oc_full_ → unrestricted
- Scope hierarchy: org:{id} ⊃ project:{id} ⊃ task:{id} ⊃ session:{id}
- Job priority tiers: 100 (sync/human-waiting), 50 (standard async), 25 (background), 10 (maintenance)
- Job pickup: SELECT ... FOR UPDATE SKIP LOCKED for concurrent worker claiming without contention
- Job retry: exponential backoff base 5s, max 5min. When attempts >= max_attempts → dead_letter status.
- Stale claim recovery: background process detects claimed/running jobs past timeout (default 30min for agent turns, 5min lightweight). Resets to pending or dead_letter.
- Webhooks: deferred to V2.1. Event log designed to support them (stable types, consistent structure).
- CLI client: `ottercamp` binary with noun-verb commands. Maps to REST API. Three output modes: table (default), json, quiet (IDs only).
- CLI auth: --api-key flag, OTTERCAMP_API_KEY env var, or ~/.ottercamp/credentials file.
- CLI default server URL: http://localhost:4110 (doc 08 default port).

**Event type catalog — 140+ events across 13 domains:**
- Chat: chat.session.{created|closed|mode_changed}, chat.message.{created|delta|finalized|failed|redacted}, chat.turn.{started|completed|failed|stopped}, chat.summary.created, chat.read_cursor.updated, chat.listening_eval.completed, chat.participant.{joined|left}, chat.reaction.{created|updated|deleted}
- Task: task.{created|updated|status_changed|queued|started|blocked|unblocked|held|resumed|review_started|completed|cancelled}, task.subtask.{created|status_changed|completed}, task.participant.{added|removed}
- Flow: task.flow.{advanced|rejected|node_started|node_completed|node_blocked}
- Dependency: task.dependency.{added|removed|resolved|cancelled}
- Agent: agent.{created|activated|paused|retired|cancelled|expired|promoted|profile_updated}, agent.project.{assigned|unassigned}
- Memory: memory.item.{created|updated|archived|superseded|promoted}, memory.entity.{created|synthesized}, memory.contradiction.detected, memory.consolidation.{started|completed}, memory.import.{started|completed|failed}
- Model: model.request.{started|completed|failed|fallback}, model.budget.{warning|exceeded}
- Control: control.run.{created|started|completed|failed|cancelled|timed_out|paused|resumed|dead_lettered|stuck_detected|escalated}, control.policy.{created|updated|deleted|evaluated|denied}
- Project: project.{created|updated|archived}
- Merge/Delivery: project.merge.{queued|started|completed|conflict}, project.push.{succeeded|failed}, project.deploy.{started|completed|failed|rollback}, project.remote.{created|updated|removed}, project.environment.deployed
- System: system.health.{degraded|recovered}, system.schedule.{created|updated|paused|resumed|fired|skipped}, system.job.{started|completed|failed|dead_lettered}
- System Integration: system.cli.{executed|denied}, system.browser.{session_created|session_closed|handoff_created|handoff_completed|handoff_expired|domain_blocked}
- MCP: mcp.connection.{created|updated|deleted|status_changed|health_changed}, mcp.catalog.changed, mcp.tool.{executed|denied|preload_failed}
- Inbox: inbox.item.{created|acted|deferred}

**Job types catalog:**
- agent_turn, flow_node_start, memory_extraction, memory_consolidation, notification_delivery, merge_execute, push_execute, deploy_task_create, schedule_tick, summary_generate, cleanup_ephemeral, idempotency_cleanup, event_log_retention, session_cleanup, api_key_cleanup

**API endpoint catalog (comprehensive — from Section 4):**
- Auth/org, chat sessions/messages/participants/turns/artifacts/reactions/read-cursor, projects, tasks/flow/subtasks/dependencies/events/participants, flow templates/nodes/skills, inbox, merge queue, scheduling, agents/skills/project-assignments/templates, memory/query/items/entities/taxonomy/import/consolidate, models/providers, MCP connections/catalog/executions, skills, tools/tool-policies, control plane runs/steps/artifacts/events/cancel/retry/policies/evaluate/cost/health, system integration CLI/browser/handoffs, delivery remotes/environments, events/stream/ws, health/version

**Cross-spec references:**
- ISSUE #13 RESOLVED: domain_event table fully defined in doc 12 with seq-based ordering. See DDL above.
- consumer_cursor bootstrapped with last_seq=0 for each consumer on first startup
- Job types reference all domain operations defined in docs 02, 03, 03a, 06, 07, 16
- supervisor actor_type extends canonical 3-type convention from docs 04/05/16 — handled in domain_events only, same as run_event from doc 16

**Gaps/conflicts:**
- ISSUE #20 (new): doc 12 defines `actor_type` on `domain_event` as ('human','agent','system','supervisor'). Doc 16 defines `run_event.actor_type` also including 'supervisor'. But the canonical principal convention from doc 04 defines only 3 types: 'human','agent','system'. Doc 12 explicitly notes this extension and says "code that switches on actor_type must handle all four values." However, the audit_event table (doc 04) only allows ('human','agent','system') — it does not include 'supervisor'. This means supervisor-initiated actions (stuck run recovery, timeout cancellation) cannot be attributed in audit_event. Clarify: should audit_event also be updated to allow actor_type='supervisor', or is supervisor attribution tracked only through domain_event and run_event (not audit_event)?

---

## Running Table Catalog additions from Doc 11

### CLI and Browser (doc 11)
- `cli_execution` — CLI command lifecycle with risk classification and output capture (doc 11)
- `browser_session` — task-scoped browser context, persists across runs within a task (doc 11)
- `browser_action` — fine-grained record of every browser interaction (doc 11)
- `browser_handoff` — human handoff lifecycle bridging browser domain and inbox (doc 11)

---

## Doc 18: Web UI (Backend API Contracts and Figma Gaps Only)

**Tables:** none new

**Backend API contracts required by Web UI:**
- Same REST API as TUI (doc 12 full endpoint catalog). No mobile-specific or web-specific endpoints.
- Static SPA served by API service (no separate frontend server). React+TypeScript bundle.
- SSE realtime: `GET /v1/events/stream?scopes=...` — same as TUI. Chat streaming, task status, inbox updates, merge queue, activity feed, agent status, unread indicators.
- WebSocket: `GET /v1/ws` — optional, used for typing indicators and live-pairing (bidirectional). Not required for normal chat.
- Auth: session-based auth (Bearer token), same as API. SSE authenticates with same session token.
- Delta sync on reconnect: `GET /v1/events/stream?scopes=...&last-event-id={seq}` — reconnect catch-up.
- Diff view: `GET /v1/tasks/{id}/diff?from_sha={sha}&to_sha={sha}` — task branch vs commit SHA (or vs main). Required for review node view. Branch vs previous `flow_node_execution.commit_sha` for incremental review.
- Work log endpoint: `GET /v1/tasks/{id}/flow-nodes/{node_id}/work-log` or equivalent. Returns agent's async session execution trace: tool calls, arguments, results, artifacts, time, tokens.
- Agent detail: `GET /v1/agents/{id}` — must include current assignments, model profile, budget info, skills, lifecycle status with transition history, activity summary (recent runs, active tasks, last active).
- Agent activity: `GET /v1/agents/{id}/runs?limit=N` — recent runs for agent detail view.
- Cost tracking data: `GET /v1/usage?group_by=agent|project|model&period=7d|30d|90d` — feeds Cost Tracking sub-view. Reads from model_usage_rollup (30d/90d) or raw model_invocation (7d).
- Queue depth: `GET /v1/control/health` or polling endpoint for active runs count, wait queue depth, per-provider rate limit utilization, avg wait time.
- Run history: `GET /v1/control/runs?status=...&agent=...&period=...` — feeds Run History observability sub-view.
- Run detail: `GET /v1/control/runs/{id}` — full tool call trace, stop reason, model used, task/flow node link.
- View state persistence: local storage only (no server-side view state). Active session, main content view, sidebar collapse, chat pane width, dark/light mode preference.
- Optimistic inbox actions: client-side state update before API confirmation. Rollback on failure.
- Cmd-K search: `GET /v1/search?q=...&types=project,task,agent,session,flow_template` — fuzzy search for command bar.
- Keyboard shortcuts: `Cmd-[`/`Cmd-]` for scope pill navigation (same semantic as TUI `[`/`]` keys).

**SSE events consumed by Web UI (same as TUI doc 17, plus):**
- All events consumed by TUI (doc 17 notes).
- Additional: project.deploy.{started|completed|failed|rollback}, project.environment.deployed — for Environments sub-view.
- model.budget.{warning|exceeded} — for Budget Status widget in Cost Tracking.
- system.health.{degraded|recovered} — for Queue Depth view.
- system.job.{started|completed|failed} — for monitoring views.

**Data shapes required by Web UI views:**
- Agent directory row: agent_id, name, role title, status, project_names[], agent_class (staff|temp).
- Run history row: run_id, status, agent_name, task_title, task_number, duration_ms, input_tokens+output_tokens, estimated_cost (display-layer calc), created_at.
- Task detail history (from project_task_event): actor_name, actor_type, event_type, comment, created_at — rendered as human-readable prose.
- Diff view: file-tree (path, change type, additions, deletions) + per-file unified diff content. Syntax highlighting by file extension.
- Flow stepper node data: node_id, node_title, status (completed|active|blocked|pending), subtask_count, subtask_done, is_review_node, commit_sha (if completed), work_log_link.
- Inbox item content: item_type, item_title, source_project, source_task, created_by, created_at, urgency, action_payload (diff preview for reviews, staged content for drafts, agent reasoning for escalations).

**Figma gaps from doc 18 (not yet in ui-spec-for-figma.md):**
- Sidebar collapse to icon-only: collapsible behavior for the session sidebar. Keyboard shortcut `Cmd-Shift-C`. Defaults to expanded.
- Chat pane resizable: operator drags left edge of chat pane to adjust width. Minimum width enforced. Width persisted in local storage.
- Viewing context hint at top of chat pane: "Viewing: OC-42 (Implement Auth)" — subtle contextual banner showing what main content is displayed, sent to agent as context.
- Scope pill in web UI: segmented control with available scope levels. Cmd-[ / Cmd-] for keyboard navigation. Web-equivalent of TUI `[`/`]` scope navigation.
- Notification center: bell icon sidebar overlay (NOT a separate page). Shows urgency-tiered notifications with grouped low-urgency items ("3 tasks updated in OtterCamp V2"). Dismiss individual or mark all read.
- Task board card: flow progress bar (miniature step indicator) + priority + assignee + subtask progress. Dense kanban layout.
- Schedules tab in Project View: table with schedule name, cadence, last run (with success/failed status indicator), next run, active/paused status. Row expansion shows last 5-10 task instances created by the schedule.
- Environments tab in Project View: deployed commit SHA (abbreviated, clickable), deploy timestamp, deploy task link, previous commit on hover/expand for rollback reference.
- Project Settings sub-view: read-only display of project context block, repo path, delivery mode, remotes, agent assignments. No edit forms.
- Run History observability sub-view: table with status/agent/task/duration/tokens/cost/timestamp. Expandable row: full tool call trace, stop reason, model, task/flow node link.
- Cost Tracking sub-view: per-agent, per-project, per-model breakdown. Trend chart. Budget status visual with approaching-limit indicator.
- Queue Depth sub-view: tasks by priority level, active vs concurrency limit, per-provider rate limit utilization, avg wait time. Real-time SSE updates.
- Command bar (Cmd-K): Superhuman-style centered overlay. Fuzzy search across projects/tasks/agents/sessions/flow templates. Results grouped by entity type. Recent searches shown when empty. Arrow-key navigation, Enter to select, Escape to dismiss. Quick actions: navigate, switch session, filter, system.
- Keyboard shortcut cheatsheet: `Cmd-1` through `Cmd-9` (session by position), `Cmd-I` (inbox), `Cmd-D` (dashboard), `/` (focus chat input).
- Dark mode toggle (persistent user preference via local storage).
- Deployment badge on task header: shown when task is a deploy task (from doc 03a).
- Remote push status indicator on task header: `push_succeeded` or `push_failed` badge (from task events).
- Review node view: diff viewer as primary review surface. Approve/Reject buttons inline in task detail. Reject opens text input for feedback. Subtask summary from preceding work node. Work log link.
- Diff viewer component: file tree left + diff content right. Syntax highlighting by file type. Unified/split toggle. File-level summary for large diffs (expandable per-file). Binary file indicator.
- Mobile-responsive degradation: three-panel degrades to swappable panels on narrow screens. Tablets work in the three-panel layout.

**Cross-spec references:**
- Same API as TUI (doc 12 endpoint catalog; doc 17 for SSE event set).
- Diff view requires `flow_node_execution.commit_sha` (doc 16) for incremental review (branch vs prev node commit SHA, not just vs main).
- Run History data from `run`/`run_step`/`run_attempt`/`tool_execution` tables (doc 16).
- Cost Tracking from `model_usage_rollup`/`model_invocation` (doc 07). Budget status from `token_budget` (doc 13).
- Observability dashboards: same 4 dashboards as ui-spec-for-figma.md Settings/Observability section (Usage Explorer, Overview, Performance, Agents).
- Activity feed from domain_event (doc 12) filtered by scope.
- Inbox from inbox_item (doc 03) with full action_payload.
- Environments from project_environment (doc 03a).
- Delivery deploy badge from project_task `is_deploy_task` flag (doc 03a — task.deploy_flow_template_id context).

**Gaps/conflicts:** none new beyond previously logged issues.

---

## Doc 19: Mobile UI (Backend API Contracts and Figma Gaps Only)

**Tables:** none new

**Backend API contracts required by Mobile:**
- Shares same REST API and realtime endpoints as web UI (doc 12). No mobile-specific backend.
- Push notification delivery: server-side notification event bus consumer (existing from doc 02) adds push channel. Evaluates operator's push preferences, sends via APNs (iOS) or FCM (Android). No new API endpoint — push is a server-side delivery mechanism.
- Deep link URL scheme: `ottercamp://{resource}/{id}` (custom scheme) + universal links (`https://app.ottercamp.dev/{resource}/{id}`) as web fallback. These are client-side constructs mapping to existing API resources. No new server-side routes required.
- Mobile dashboard aggregation endpoint (optional optimization): `GET /v1/mobile/dashboard` — returns inbox_count + project summaries + recent notifications in one request. Reduces API calls on app open. NOT a requirement — app can compose from existing endpoints. If added, backend concern only (no Figma impact).
- WebSocket preferred over SSE for mobile foreground realtime (bidirectional, easier mobile reconnection behavior). Same `/v1/ws` endpoint as web UI. Fallback to SSE if WebSocket unavailable.
- Delta sync on foreground: `GET /v1/events/stream?last-event-id={seq}` — catch up on missed events after backgrounding. API supports delta queries ("what changed since timestamp X").
- Auth session: same session lifecycle as web (30-day sliding window, per doc 04). Mobile creates standard auth session. Multiple sessions (mobile + web + TUI) coexist — creating mobile session does not invalidate others.
- Biometric auth: purely client-side (keychain/keystore + platform biometric APIs). Session token stored in platform keychain. No new server-side auth flow — server only sees Bearer token header.
- Push notification payload includes: title, body, category (for rich notification action buttons), deep link URL, item_id (for inbox items).
- Rich notification quick actions (iOS/Android): Approve, Open Chat — these map directly to existing inbox item action endpoints (`POST /v1/inbox/{id}/act`). Backend is unchanged; platform handles the button-to-request wiring.
- Conflict detection for concurrent multi-device actions: server's standard last-write-wins + 409/conflict responses. Mobile app receives SSE/WebSocket update when another client acts on the same inbox item.

**Push notification operator preferences stored server-side:**
- Push preferences (per urgency tier, per project, quiet hours, per event type) must be stored somewhere. Doc 19 says "configuration accessible from mobile app settings screen and from the web UI. Changes sync across both." This implies a new table or extension to an existing table for push notification preferences. No DDL defined in doc 19. Possible homes: `organization.settings` jsonb (per-org, not per-operator), `human_user` table extension, or a new `push_notification_preference` table. This is an OPEN DATA MODEL QUESTION.

**Data shapes required by Mobile:**
- Inbox item (mobile): item_type, title, description (brief), source_project, source_task, timestamp, urgency, inline actions available. On mobile, no diff inline for work reviews — "View on Web" link instead.
- Notification list item: urgency indicator, title, brief description, timestamp, source project/task, deep link.
- Project card (dashboard): project_name, task counts by status (in_progress, blocked, review, queued), blocked_count, last_activity_at.
- Task detail (mobile, read-only): header (title, status, priority, assignee, project_name), flow stepper (compact horizontal, scrollable), subtask summary, description, acceptance criteria, dependencies, history, branch name. No diff view.
- Chat view (mobile): streaming text, tool calls collapsed to "Agent is working..." indicator. No file upload, no steer, no reactions.

**Figma gaps from doc 19 (not yet in ui-spec-for-figma.md — mobile-specific components):**
- Push notification UI (lock screen/notification shade): title + body + category-specific inline action buttons (Approve, Open Chat). Rich notification format.
- Mobile Notifications screen (home screen): reverse chronological list grouped by time (Today/Yesterday/Earlier). Urgency color indicators. Swipe-to-dismiss, swipe-to-open. Unread/read state. Critical items always pinned at top.
- Mobile Dashboard screen: inbox badge (prominent count, taps to inbox), project card grid (name + compact task status bar + blocked count + last activity), quick stats bar.
- Mobile Inbox screen: two sections (Active / Deferred). Item type icon + title + description + urgency. Inline action buttons (type-specific: Approve/Request Changes/Defer, or Approve/Reject/Defer, or Open Chat/Defer, etc.). Swipe-to-defer gesture. Deferred section toggle.
- Mobile Chat screen: session list grouped by scope + unread indicators. Chat view: mobile thread layout, streaming responses, typing indicator ("Agent is working..." collapsed), @mention basic autocomplete, text input + send button, Stop button during agent turns.
- Mobile Task Detail screen: read-only. Compact flow stepper (horizontal scroll). Subtask compact list. History compact list. "View on Web" link for diff at review nodes. Review node banner: "This task needs your review" → link to inbox item.
- Mobile Project Status screen: project name + description, task summary compact bar chart, flat task list (blocked/review first), merge queue section, schedules compact list.
- Biometric auth prompt: Face ID / Touch ID / fingerprint prompt on app open. Fallback to password after 3 failures. Biometric re-confirmation for capability approvals.
- Deep link navigation: tap-to-open with skeleton loading state. No intermediate navigation screens.
- Offline indicator: "Last updated X minutes ago" banner on dashboard. "No connection" banner with disabled action buttons.
- Conflict warning: "This item was already acted on from another device" when mid-action state update arrives.

**Cross-spec references:**
- Same API as web UI and TUI (doc 12 endpoint catalog).
- Push notification channel extends existing notification delivery consumer (doc 02 notification system).
- Inbox item actions map to `POST /v1/inbox/{id}/act` (doc 03 / doc 12).
- Auth session follows doc 04 session lifecycle (30-day sliding, Bearer token).
- Push preferences storage: open question (new table vs human_user extension vs organization.settings jsonb).
- Mobile WebSocket: same `/v1/ws` endpoint (doc 12).

**Gaps/conflicts:**
- ISSUE #28 (new): doc 19 states push notification preferences are synced between mobile and web UI, implying server-side storage. No doc defines a push preference table or column. The existing notification preference system (doc 02) covers in-app notification filtering by urgency/project/event-type, but doc 19 requires per-urgency push enable/disable, per-project override, and quiet hours — which extend beyond what doc 02's notification_preference structure defines. Need: either extend doc 02's notification preferences to include push-specific fields, or define a new `push_notification_preference` table.

---
