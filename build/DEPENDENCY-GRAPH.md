# OtterCamp V2 — Dependency Graph

Generated: Iteration 4

This document assigns every table from the Running Table Catalog to a build layer based
on foreign-key depth, catalogs all polymorphic patterns, lists cross-domain touch points,
and flags tables with open BLOCKERs from ISSUES.md.

---

## L0 — Foundation

Tables with no FKs into other application tables, or only self-references. These are the
root entities. No other table must exist first.

- `organization` — no application FKs; root entity for all tenancy [doc 04]
- `model_provider` — instance-level provider registry; no FKs (standalone) [doc 07]
- `tool_definition` — startup-populated native tool registry; no FKs (standalone) [doc 20]

**Self-referencing (still L0):**
- `memory_taxonomy_node` — organization_id (L0 loop) + parent_id → self [doc 06]

**Note on `organization`:** It carries a self-referencing `settings jsonb` but no FK to
another table. It is unambiguously L0.

---

## L1 — Core Identity

Tables that FK only into L0 tables (organization or model_provider).

- `human_user` → organization [doc 04]
- `auth_session` → human_user (L1 self-loop, but resolves within L1) [doc 04]
- `api_key` → human_user [doc 04]
- `audit_event` → organization; principal_id polymorphic (agent at L2, but org FK is L0) [doc 04]
- `secret` → organization [doc 08]
- `provider_connection` → organization + model_provider (both L0) [doc 07]
- `model_profile` → organization (nullable) + model_provider (L0); fallback_profile_id self-ref (application-layer, not SQL FK) [doc 07]
- `skill` → organization + project (project is L2, but skill's organization_id FK alone places base at L1; project_id nullable) [doc 10]

**Note on `skill`:** The `project_id` FK is nullable. The organization_id FK alone is L0.
Because the project_id FK is nullable and an org-scoped skill has no project FK, skills
are assigned to L1 here. Project-scoped skill rows depend on the project table (L2) but
the table itself is defined at L1 depth.

---

## L2 — Domain Core

Tables that FK into L0 + L1 tables. These form the primary domain entities.

### Auth & Tenancy
- `audit_event` — polymorphic principal_id includes agent (agent is defined below at L2; the
  table itself is logically in this layer because its primary FK is to organization)
  [doc 04] ← NOTE: moved from L1 to L2 because the agent FK target is also L2

### Agent Domain
- `agent` → organization (L0); promoted_to_agent_id self-ref; default_model_profile_id →
  model_profile.logical_profile_id (L1, application-layer) [doc 05]
- `agent_profile_template` → organization (L0, nullable); source_agent_id → agent (L2 self-loop) [doc 05]

### Project Domain
- `project` → organization [doc 03]
- `flow_template` → project (nullable for system templates) [doc 03]
- `task_schedule` → project + flow_template [doc 03]

### Model Domain
- `model_profile_assignment` → organization + scope_id polymorphic (org/project/agent/flow_node;
  application-layer FKs) [doc 07]

### MCP Domain
- `mcp_connection` → organization + project (nullable) [doc 09]

### Memory Domain
- `memory_entity` → organization + synthesis_memory_id → memory (but memory itself is L3;
  synthesis_memory_id is nullable — table defined here, FK to memory is L3-forward reference
  managed at application layer) [doc 06]

### Events & Jobs
- `domain_event` → organization [doc 12]
- `job_queue` — no FKs (standalone; payload carries entity IDs) [doc 12]
- `idempotency_key` — no FKs (org-scoped by db-per-org, no SQL FK) [doc 12]
- `consumer_cursor` — no FKs (standalone) [doc 12]

### Cost Controls
- `token_budget` → organization + project (nullable) [doc 13]

---

## L3 — Domain Features

Tables that FK into L0–L2 tables. These are primary feature tables within each domain.

### Auth & Tenancy
- `audit_event` — final placement: FKs to organization (L0), human_user (L1), agent (L2) [doc 04]

### Agent Domain
- `agent_project_assignment` → agent (L2) + project (L2) [doc 05]
- `agent_skill_attachment` → agent (L2) + skill (L1) [doc 05, doc 10]

### Project Domain
- `flow_node` → flow_template (L2); actor_id → agent (L2, nullable); next_node_id/reject_node_id
  self-refs; **FINAL SCHEMA**: drop `skills jsonb`, add `mcp_tools jsonb`, add `tool_domains jsonb`
  (see ISSUE #16 / ISSUE #25) [doc 03, doc 09, doc 10, doc 20]
- `project_task` → project (L2) + flow_template (L2, nullable) + current_flow_node_id → flow_node
  (L3 self-loop within domain) + schedule_id → task_schedule (L2, nullable) [doc 03]
- `inbox_item` → organization (L0) + target_user_id → human_user (L1) + source_project_id → project
  (L2, nullable) + source_task_id → project_task (L3, nullable) [doc 03]
- `merge_queue_entry` → project (L2) + task_id → project_task (L3) [doc 03]
- `project_task_event` → project_task (L3) + flow_node_id (L3, nullable) [doc 03]
- `project_remote` → project (L2) [doc 03a]
- `project_environment` → project (L2) + deploy_task_id → project_task (L3, nullable) [doc 03a]

### Skill Domain
- `flow_node_skill` → flow_node (L3) + skill (L1) [doc 10]

### Model Domain
- `model_usage_rollup` → organization (L0); rollup_id polymorphic (application-layer) [doc 07]

### MCP Domain
- `mcp_tool_catalog` → mcp_connection (L2) [doc 09]
- `mcp_secret_binding` → mcp_connection (L2); secret_ref is application-layer slug reference [doc 09]

### Memory Domain
- `memory` → organization (L0) + project (L2, nullable) + project_task (L3, nullable) + agent (L2,
  nullable) + superseded_by self-ref [doc 06]
- `memory_taxonomy_tag` → memory (L3) + memory_taxonomy_node (L0) [doc 06]
- `memory_entity_mention` → memory (L3) + memory_entity (L2) [doc 06]
- `memory_import` → organization (L0) + requested_by → human_user (L1) [doc 06]
- `memory_compaction_run` → organization (L0) [doc 06]

### Control Plane
- `capability_policy` → organization (L0) + project (L2, nullable) + agent (L2, nullable) [doc 16]

### Events & Jobs
- `trace_span` — implied; no explicit FKs defined; partitioned by day; infrastructure-layer [doc 13]

---

## L4 — Cross-Domain

Tables that FK across multiple domains or reference broadly into L0–L3. These are the
integration tables that tie domains together.

### Chat Domain (deep cross-domain)
- `chat_session` → organization (L0); scope_id polymorphic → organization | project (L2) | project_task
  (L3); created_by polymorphic [doc 02]
- `chat_participant` → chat_session (L4) + participant_id polymorphic → human_user (L1) | agent (L2) [doc 02]
- `chat_turn` → chat_session (L4) [doc 02]
- `chat_message` → chat_session (L4) + turn_id → chat_turn (L4, nullable) + author polymorphic [doc 02]
- `chat_artifact` → chat_session (L4) + message_id → chat_message (L4) [doc 02]
- `chat_summary` → chat_session (L4) [doc 02]
- `chat_read_cursor` → chat_session (L4) + user_id → human_user (L1) [doc 02]
- `chat_message_reaction` → chat_message (L4) + reactor polymorphic [doc 02]

### Control Plane (cross-domain hub)
- `run` → organization (L0) + project (L2, nullable) + project_task (L3, nullable) + flow_node (L3,
  nullable) + session_id → chat_session (L4, nullable) + turn_id → chat_turn (L4, nullable) +
  principal polymorphic [doc 16]
- `run_step` → run (L4) [doc 16]
- `run_attempt` → run_step (L4) [doc 16]
- `tool_execution` → run (L4) + run_step (L4, nullable) + run_attempt (L4, nullable) [doc 16]
- `run_artifact` → run (L4) + run_step (L4, nullable) + run_attempt (L4, nullable) [doc 16]
- `run_event` → run (L4) + run_step (L4, nullable) + run_attempt (L4, nullable) [doc 16]

### Model Domain (cross-domain attribution)
- `model_invocation` → organization (L0) + model_provider (L0) + provider_connection (L1, nullable) +
  chat_session (L4, nullable) + chat_turn (L4, nullable) + agent (L2, nullable) + project (L2,
  nullable) + project_task (L3, nullable) + run (L4, nullable) + run_step (L4, nullable) +
  run_attempt (L4, nullable) + self-refs [doc 07]

### Project Domain (cross-domain features)
- `flow_node_execution` → project_task (L3) + flow_node (L3) + session_id → chat_session (L4) [doc 03]
- `project_subtask` → project_task (L3) + flow_node_execution (L4) + assignee polymorphic [doc 03]
- `project_task_dependency` → source/depends_on polymorphic → project_task (L3) | project_subtask (L4) [doc 03]
- `project_task_participant` → project_task (L3) + participant polymorphic [doc 03]

### CLI & Browser Domain
- `cli_execution` → run (L4) + run_step (L4) + project_task (L3) + project (L2) + agent (L2) +
  stdout/stderr_artifact_id → run_artifact (L4, nullable) [doc 11]
- `browser_session` → project_task (L3) + project (L2) + agent (L2) [doc 11]
- `browser_action` → browser_session (L4) + run (L4) + run_step (L4) + screenshot_artifact_id →
  run_artifact (L4, nullable) [doc 11]
- `browser_handoff` → browser_session (L4) + inbox_item (L3) + run (L4) + screenshot/post-handoff
  → run_artifact (L4, nullable) [doc 11]

### MCP Domain (cross-domain logging)
- `mcp_execution_log` → organization (L0) + mcp_connection (L2) + mcp_tool_catalog (L3) +
  tool_execution (L4, nullable) + run (L4, nullable) + agent (L2) [doc 09]

### Memory Domain (cross-domain provenance)
- `memory_source` → memory (L3) + import_id → memory_import (L3, nullable) + session_id →
  chat_session (L4, nullable; soft reference) [doc 06]
- `memory_dedup_reviewed` → memory.id × 2 (L3) [doc 06]

### Tool Domain
- `session_tool_set` → chat_session (L4) + agent (L2) [doc 20]

---

## L5 — API + CLI + Tests

No new tables. This layer covers:

- REST API endpoints (all routes under /v1/ per doc 12)
- SSE event stream (/v1/events/stream) and WebSocket (/v1/ws)
- CLI commands (ottercamp serve, migrate, bootstrap, reset-password, magic-link,
  unlock-account, backup, restore, secret set/list/delete, version, health)
- Integration tests (real PostgreSQL, recorded provider responses)
- E2E tests (full instance in OTTERCAMP_MODE=test)
- Seed data (bootstrap dataset: starter trio, default flow templates, org-default skills,
  model profiles, capability policies, org session)
- API documentation
- Deployment runbooks
- Test fixtures (testdata/responses/, testdata/import/)

---

## Summary Table Counts by Layer

| Layer | Table Count |
|-------|-------------|
| L0 | 4 (`organization`, `model_provider`, `tool_definition`, `memory_taxonomy_node`) |
| L1 | 7 (`human_user`, `auth_session`, `api_key`, `secret`, `provider_connection`, `model_profile`, `skill`) |
| L2 | 13 (`agent`, `agent_profile_template`, `project`, `flow_template`, `task_schedule`, `model_profile_assignment`, `mcp_connection`, `memory_entity`, `domain_event`, `job_queue`, `idempotency_key`, `consumer_cursor`, `token_budget`) |
| L3 | 22 (`audit_event`, `agent_project_assignment`, `agent_skill_attachment`, `flow_node`, `project_task`, `inbox_item`, `merge_queue_entry`, `project_task_event`, `project_remote`, `project_environment`, `flow_node_skill`, `model_usage_rollup`, `mcp_tool_catalog`, `mcp_secret_binding`, `memory`, `memory_taxonomy_tag`, `memory_entity_mention`, `memory_import`, `memory_compaction_run`, `capability_policy`, `trace_span`) |
| L4 | 30 (`chat_session`, `chat_participant`, `chat_turn`, `chat_message`, `chat_artifact`, `chat_summary`, `chat_read_cursor`, `chat_message_reaction`, `run`, `run_step`, `run_attempt`, `tool_execution`, `run_artifact`, `run_event`, `model_invocation`, `flow_node_execution`, `project_subtask`, `project_task_dependency`, `project_task_participant`, `cli_execution`, `browser_session`, `browser_action`, `browser_handoff`, `mcp_execution_log`, `memory_source`, `memory_dedup_reviewed`, `session_tool_set`) |
| L5 | 0 new tables |
| **Total** | **72 tables** |

---

## Polymorphic FK Catalog

Every polymorphic (type + id) pair found across all specs, with concrete types and source docs.

### scope_type + scope_id
Used to attach an entity to an organization, project, or task context.
- Concrete types: `organization` | `project` | `project_task`
- On tables: `chat_session` (also includes 'task' variant), `audit_event` (also includes 'session') [docs 02, 04]

### created_by_type + created_by_id
Used on entity creation to record who created the row.
- Concrete types: `human_user` | `agent` | system sentinel UUID (00000000-0000-0000-0000-000000000000)
- On tables: `chat_session`, `project`, `project_task`, `project_task_event`, `inbox_item`, `mcp_connection`, `secret`, `skill` (no sentinel), `model_profile`, `capability_policy`, `token_budget` (no agent) [docs 02, 03, 04, 07, 08, 09, 10, 13, 16]

### principal_type + principal_id
The executing actor for control plane runs and audit events.
- Concrete types: `human_user` | `agent` | system sentinel UUID
- On tables: `run`, `audit_event` [docs 04, 16]

### actor_type + actor_id
Used in event log and task event attribution.
- Concrete types: `human_user` | `agent` | system sentinel UUID | `supervisor` (domain_event + run_event only)
- On tables: `domain_event`, `run_event`, `project_task_event` [docs 03, 12, 16]
- NOTE: `supervisor` is an extension beyond the canonical 3-type convention. `audit_event` does NOT include 'supervisor' (ISSUE #20).

### participant_type + participant_id
Used for session and task participant records.
- Concrete types: `human_user` | `agent`
- On tables: `chat_participant`, `project_task_participant` [docs 02, 03]

### author_type + author_id
Used on chat messages to record the message author.
- Concrete types: `human_user` | `agent` | null (system/tool_result/tool_call messages)
- On tables: `chat_message` [doc 02]

### reactor_type + reactor_id
Used on reactions to messages.
- Concrete types: `human_user` | `agent`
- On tables: `chat_message_reaction` [doc 02]

### assignee_type + assignee_id
Used on project subtasks.
- Concrete types: `human_user` | `agent`
- On tables: `project_subtask` [doc 03]

### assigned_by_type + assigned_by_id
Used when assigning agents to projects.
- Concrete types: `human_user` | `agent` | system sentinel UUID
- On tables: `agent_project_assignment` [doc 05]

### attached_by_type + attached_by_id
Used when attaching skills to agents.
- Concrete types: `human_user` | `agent` | system sentinel UUID
- On tables: `agent_skill_attachment` [docs 05, 10]

### temp_project_id (temp agent project reference)
Used on temp agents to declare their project. All temps are project-scoped.
- Always references `project.id`; required (NOT NULL) for agent_class='temp'; null for staff
- On tables: `agent` [doc 05]
- ✅ ISSUE #8 RESOLVED: replaced `temp_scope_type + temp_scope_id` polymorphic pair

### source_type + source_id (memory provenance)
Used to link memories to their originating event or artifact.
- Concrete types: `chat_message` | `event` (no DB table) | `file` (no DB table) | `memory_import` | `explicit` (no DB table)
- On tables: `memory_source` [doc 06]
- NOTE: 'event', 'file', 'explicit' types have no DB FK enforcement (ISSUES #9, #10).

### source_type + source_agent_id (agent profile template)
Used on agent_profile_template to track the origin agent.
- Concrete types: `agent` (source_agent_id → agent.id; source ∈ system|org|promoted)
- On tables: `agent_profile_template` [doc 05]

### rollup_type + rollup_id (model usage)
Used for polymorphic aggregation grouping.
- Concrete types: `provider_connection` | `model_provider` | `agent` | `project`
- On tables: `model_usage_rollup` (application-layer only, no SQL FK) [doc 07]

### scope_type + scope_id (model profile assignment)
Used to assign model profiles to org/project/agent/flow_node.
- Concrete types: `organization` | `project` | `agent` | `flow_node`
- On tables: `model_profile_assignment` (application-layer only, no SQL FK) [doc 07]

### source_type + source_id (project task dependency)
Used for dependency edges between tasks and subtasks.
- Concrete types: `project_task` | `project_subtask` (CHECK: source_type = depends_on_type — no cross-level deps)
- On tables: `project_task_dependency` [doc 03]

---

## Cross-Domain Touch Points

Where domains interact at the data layer. These are the FK edges that cross domain boundaries.

### Chat ↔ Project/Task
- `chat_session.scope_id` → `project.id` | `project_task.id` (via scope_type) — a session is scoped to a project or task
- `flow_node_execution.session_id` → `chat_session.id` — each node execution creates a dedicated async session

### Chat ↔ Auth
- `chat_session.organization_id` → `organization.id`
- `chat_read_cursor.user_id` → `human_user.id`
- `chat_participant.participant_id` → `human_user.id` | `agent.id`

### Control Plane ↔ Chat
- `run.session_id` → `chat_session.id` — a run is associated with the session that triggered it
- `run.turn_id` → `chat_turn.id` — a run is associated with the specific turn that triggered it
- `model_invocation.session_id` → `chat_session.id` — model calls attributed to chat sessions
- `model_invocation.turn_id` → `chat_turn.id` — model calls attributed to specific turns

### Control Plane ↔ Project/Task
- `run.task_id` → `project_task.id` — a run executes work for a task
- `run.flow_node_id` → `flow_node.id` — a run executes at a specific flow node
- `tool_execution` and `run_artifact` cascade from run → task context

### Control Plane ↔ Agent
- `run.principal_id` → `agent.id` (via principal_type='agent') — the agent executing the run
- `capability_policy.agent_id` → `agent.id` — per-agent policy overrides

### Model Gateway ↔ Control Plane
- `model_invocation.run_id` → `run.id` — model calls attributed to runs
- `model_invocation.run_step_id` → `run_step.id`
- `model_invocation.run_attempt_id` → `run_attempt.id`

### Memory ↔ Chat
- `memory_source.session_id` → `chat_session.id` (soft reference — see ISSUE #9)
- Chat reactions on `chat_message_reaction` feed memory confidence updates (application-layer)

### Memory ↔ Project/Task
- `memory.project_id` → `project.id` — project-scoped memories
- `memory.task_id` → `project_task.id` — task-scoped memories; task completion triggers consolidation run

### Memory ↔ Agent
- `memory.agent_id` → `agent.id` — agent-private memories
- Agent `memory_read_scopes` array (doc 05) drives scope filter in retrieval

### MCP ↔ Control Plane
- `mcp_execution_log.tool_execution_id` → `tool_execution.id` — MCP invocations linked to control plane records
- `mcp_execution_log.run_id` → `run.id` — MCP invocations linked to the parent run

### MCP ↔ Secrets
- `mcp_secret_binding.secret_ref` → `secret.slug` (application-layer slug reference)
- `mcp_connection.transport_config` uses `ref:<slug>` inline secret references

### MCP ↔ Project/Flow
- `flow_node.mcp_tools jsonb` → resolved against `mcp_tool_catalog` at session start

### CLI & Browser ↔ Control Plane
- `cli_execution.run_id` → `run.id`
- `browser_action.run_id` → `run.id`
- `browser_handoff.run_id` → `run.id`
- All three reference `run_artifact.id` for output/screenshot storage

### CLI & Browser ↔ Project/Task
- `cli_execution.task_id` → `project_task.id`
- `browser_session.task_id` → `project_task.id` (task-scoped, not run-scoped)
- `browser_handoff.inbox_item_id` → `inbox_item.id` (bridges browser domain and project inbox)

### Agent ↔ Project
- `agent_project_assignment.project_id` → `project.id`
- `flow_node.actor_id` → `agent.id` (when actor_type='agent')
- `project_task_participant.participant_id` → `agent.id`

### Agent ↔ Skills
- `agent_skill_attachment.skill_id` → `skill.id`
- `flow_node_skill.skill_id` → `skill.id`

### Agent ↔ Model Gateway
- `agent.default_model_profile_id` → `model_profile.logical_profile_id` (application-layer)
- `model_invocation.agent_id` → `agent.id`

### Tools ↔ Chat
- `session_tool_set.session_id` → `chat_session.id` — per-session cached tool resolution

### Cost / Budget ↔ Model Gateway
- `token_budget` (org + project level) enforced against `model_invocation` token counts
- `model_usage_rollup` aggregates from `model_invocation` (daily rollups)

---

## Tables With Open BLOCKERs

Tables directly affected by BLOCKER-severity issues in ISSUES.md. Task writers should flag
these as blocked until the spec issue is resolved by Sam.

- `agent` — ISSUE #1 (BLOCKER): `budget_cap_cents` vs `budget_cap_tokens` column name and type
  unresolved between doc 05 and doc 14. DDL cannot be finalized.

- `agent`, `token_budget`, `run` (budget enforcement path) — ✅ ISSUE #23 (RESOLVED): per-agent
  budget (`budget_cap_tokens`/`budget_period`) integrates with the org/project `token_budget`
  table via hierarchical/additive enforcement: a single invocation depletes all three applicable
  levels simultaneously; any exceeded hard limit blocks the invocation.

- `flow_node` — ISSUE #16 (BLOCKER): authoritative `flow_node` schema is scattered across
  three contradictory docs. Required changes: (a) DROP `skills jsonb` (doc 10), (b) ADD
  `mcp_tools jsonb` (doc 09), (c) ADD `tool_domains jsonb` (doc 20 — tracked also as
  ISSUE #25). Doc 03 DDL must be updated with all three changes before implementation.

- `tool_definition`, `browser_action`, `browser_session` (browser tool catalog) — ✅ ISSUE #26
  (RESOLVED): `browser.evaluate` (JS execution in page context) confirmed and specified in
  doc 11 §Scripted Execution. `browser_action.action_type` enum includes 16 actions including
  `evaluate`.

- `model_invocation` (retention policy) — ISSUE #21 (BLOCKER): doc 13 says 30-day retention;
  doc 14 says 90-day retention. Retention enforcement job and archival policy cannot be
  implemented until one is designated authoritative.

---

## Tables With Open AMBIGUOUS Issues (Non-Blocking Flags)

These tables have ambiguities that implementers should note but can proceed with best-judgment defaults.

- `audit_event` — ISSUE #20: should principal_type include 'supervisor'? Supervisor-initiated
  actions currently cannot be logged to audit_event.

- `flow_node` / `capability_policy` — ISSUE #17: instance-level policy rows may be writable via
  the API with no DB constraint preventing modification by org admins.

- `agent` (lifecycle_status) — ISSUE #5: no DB constraint prevents staff agents reaching
  'expired' or temp agents reaching 'draft'; application-layer enforcement only.

- `merge_queue_entry` — ISSUE #7: archived_at trigger (merge completion vs deploy completion)
  is ambiguous in doc 03 prose; doc 03a is authoritative (deploy completion).

- `model_invocation` (run_attempt_id) — ISSUE #18: unclear whether model calls in agent turn
  loop (outside a worker run) receive a run_attempt_id. Affects token aggregation queries.

- `cli_execution` / `browser_action` — ISSUE #19: run_step_id is NOT NULL on cli_execution,
  implying broker always creates a RunStep before CLI dispatch. Not explicitly confirmed.

- `memory_source` — ISSUES #9, #10: session_id is a soft reference (no SQL FK). source_id for
  'event', 'file', 'explicit' source_types has no DB FK enforcement.

- `push_notification_preference` (implied, not yet defined) — ISSUE #28: doc 19 requires
  server-side push preferences but no table or column is defined in any spec.
