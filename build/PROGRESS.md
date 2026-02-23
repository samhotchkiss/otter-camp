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

### Not yet read (docs 06, 07, 08, 09, 10, 11, 12, 13, 15, 16, 20, 21)
- `memory` and related tables — doc 06
- `model_profile`, `model_invocation`, `model_usage_rollup`, etc. — doc 07
- MCP connection tables — doc 09
- `skill`, `flow_node_skill`, and related — doc 10
- `run`, `run_step`, `tool_execution`, etc. — doc 16

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

## Iteration 2 Status: PENDING

## Iteration 3 Status: PENDING
