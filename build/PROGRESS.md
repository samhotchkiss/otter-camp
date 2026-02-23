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

### Not yet read (docs 11, 12, 13, 15, 20, 21)
- domain_event table — doc 12
- Security/observability tables — doc 13
- Tool registry tables — doc 20

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

## Iteration 3 Status: PENDING
