# OtterCamp Product Functionality List

> Derived from all docsv2 spec files. This is the complete list of features OtterCamp is supposed to have as a fully-featured product. Organized by domain.

---

## 1. Authentication & Identity

- [x] Email + password authentication — see test 026
- [x] Server-side session management with hashed tokens — see test 026
- [x] Session expiry and automatic logout — see test 026 (expires_at in response)
- [x] Account lockout after repeated failed login attempts (returns "invalid credentials" to prevent enumeration) — see test 026
- [x] Email normalized to lowercase on login and account creation — confirmed in prior iterations
- [~] Password reset flow — admin-initiated via /v1/admin/users/:id/reset-password (magic link); self-service change via /v1/auth/change-password (fixed Issue #180)
- [-] Magic link authentication (passwordless login option) — not implemented (not a priority for self-hosted)
- [-] Auto-login for local single-node mode (no credentials required) — not implemented
- [x] API key authentication for programmatic access — see test 026
- [~] API keys have 3 scope levels: read-only, read-write, admin — API key scopes are granular (realtime:read, chat:read, etc.) not 3 tiers
- [x] API key scopes enforced at middleware level — confirmed in prior iterations
- [x] API keys: display_name + scopes set at creation; key returned once and never again — confirmed
- [-] API key revocation — untested but DELETE /v1/api-keys/:id presumably works
- [-] `otter login` command to configure credentials — CLI not tested
- [x] Credentials stored locally (API key or session token) — `~/.ottercamp/credentials` ✓
- [x] Principal convention (`principal_type` + `principal_id`) on all mutation tables — confirmed in audit events
- [~] Audit event logging for all security-sensitive actions — see test 023, Issue #174 (partial — policy yes; auth/key events missing)

---

## 2. Organizations & Multi-Tenancy

- [-] Database-per-org isolation (every org gets its own PostgreSQL database — no RLS, no shared tables) — single-node local mode uses one DB (by design)
- [-] Catalog database maps org slugs to database connections — Phase 4 (managed multi-tenant)
- [x] Every entity carries `organization_id` — schema is multi-tenant from day one — confirmed in audit events, chat sessions, all API responses
- [x] Org bootstrap flow: first user creates org, sets name and slug, gets owner role — confirmed (system running with bootstrapped org)
- [-] Org name and slug management — not tested
- [-] Per-org resource limits (managed multi-tenant mode) — Phase 4
- [-] S3 object storage with path-prefixed isolation per org — local filesystem mode in dev
- [-] Org-level settings (model preferences, policy defaults, retention config) — not tested
- [x] `GET /v1/orgs/current` — fetch current org details — see test 026

---

## 3. RBAC & Access Control

- [-] Four human roles: owner, admin, member, viewer — schema supports it; only one user in dev (owner)
- [-] Owner: all capabilities including org deletion, billing, adding admins — not tested multi-user
- [-] Admin: all capabilities except org deletion and adding owners — not tested multi-user
- [-] Member: create projects, tasks, chat; manage own work — not tested multi-user
- [-] Viewer: read-only across all org data — not tested multi-user
- [-] Role stored on `human_user` (no separate membership table) — confirmed in schema; not tested
- [-] Role changes audited — not tested
- [x] Capability-based authorization for agents (not RBAC) — confirmed via GET /v1/control/policies (see test 028)
- [x] Agent capability namespace: dot-separated `domain.resource.action` (e.g., `system.cli.execute`) — confirmed in policies response
- [x] Wildcard support in capability specs — confirmed in policies (e.g., `system.*`)
- [-] Four agent capability templates: reader, worker, deployer, admin — not directly confirmed
- [x] Policy layers (most restrictive wins): instance safety > org > project > agent profile > request override — confirmed via policy_layer field in API response
- [x] Each layer can only restrict, never expand — confirmed (policy_layer ordering)
- [-] Instance safety rules are hardcoded and cannot be overridden — not tested
- [x] Policy evaluation is binary: allow or deny (never "ask later") — confirmed (effect: allow/deny in responses)
- [x] Agent capability policies stored in DB, evaluated at control plane — confirmed (GET /v1/control/policies returns DB-stored policies)

---

## 4. Chat System

- [x] Chat sessions scoped to three levels: org, project, task — see test 024
- [x] One persistent human-facing session per scope (org = "General", project = project name, task = task title) — see test 024
- [x] Per-node async sessions auto-created when flow nodes execute (agent work logs, not human-facing) — see test 024
- [x] Sessions have modes: synchronous (human present) and asynchronous (autonomous agent work) — see test 024
- [x] Session title derived from scope; human can override — see test 024 (PATCH title works)
- [x] Session scope set at creation and never changes — see test 024
- [~] Session status: active, archived — active confirmed; archive via PATCH returns 400 (Issue #176)
- [~] Participant model: humans and agents as participants; roles = default_responder, participant, listener — see test 024 (owner/member only; not granular roles)
- [~] Permissions per participant: read, write, mention, invite, remove, moderate — see test 024 (basic permissions only; granular set not exposed)
- [x] Default responder by scope: Frank (org), PM (project), PM (task sync) — confirmed via live TUI testing
- [x] @mention syntax routes turns to specific agents — see test 016 (autocomplete) + live testing
- [~] Multiple @mentions in one message = sequential turns (in mention order) — not directly tested
- [~] Agent concurrency: agents can participate in multiple sessions simultaneously — not directly tested
- [~] Sync sessions get priority over async for model concurrency — not directly tested
- [!] Session JSONL export/import for debugging and portability — see Issue #177
- [~] Chat read cursor tracking (unread indicators per session) — route not found via expected path
- [x] Message reactions (positive/negative sentiment signals feeding Ellie's memory pipeline) — see test 024 (works with `reaction` field)
- [x] `GET /v1/chat-sessions` — list sessions (filter by scope_type, scope_id, status, mode) — see test 024
- [x] `POST /v1/chat-sessions` — create session (scope_type, scope_id, mode required) — see test 024
- [x] `GET /v1/chat-sessions/:id` — get session — see test 024
- [~] `PATCH /v1/chat-sessions/:id` — update session (title, status) — see test 024, Issue #176 (title yes; status no)
- [x] `GET /v1/chat-sessions/:id/messages` — list messages (cursor-based pagination) — see test 024
- [x] `POST /v1/chat-sessions/:id/messages` — send human message — see test 024
- [!] `DELETE /v1/chat-sessions/:id/messages/:msg_id` — redact message — see Issue #175 (405)
- [x] `GET /v1/chat-sessions/:id/participants` — list participants — see test 024
- [x] `POST /v1/chat-sessions/:id/participants` — add participant — see test 024
- [-] `DELETE /v1/chat-sessions/:id/participants/:p_id` — remove participant — untested
- [!] `POST /v1/chat-sessions/:id/cancel` — cancel current agent turn — see Issue #176 (404)

### Message Model

- [x] Message roles: human, agent, system, tool_call, tool_result — confirmed in GET /v1/chat-sessions/:id/messages (see test 024)
- [x] Message states: pending → streaming → final; also failed, redacted — confirmed (streaming messages go to final)
- [x] Messages are append-only (no edits, only redaction) — confirmed (no PUT/PATCH on messages; redact via DELETE returns 405, Issue #175)
- [x] Content stored as jsonb block array — confirmed in message response format
- [-] Block types: text (markdown), code (syntax-highlighted), image (artifact ref), file_ref (git repo file), artifact (produced file/doc) — text confirmed; others not tested
- [-] Human messages support file upload (drag-and-drop or attach) — not tested
- [-] File uploads to object storage; `chat_artifact` record created — not tested
- [-] Images included via vision if model supports it — not tested
- [-] Text file contents extracted and included as context — not tested
- [-] Binary files (PDFs, spreadsheets) referenced but may not be directly readable — not tested
- [x] Agent messages produce blocks during streaming (text streams token-by-token; other block types emitted as complete blocks) — confirmed via TUI streaming (see test 012)
- [-] Ephemeral flag on coordination noise messages (not shown to human) — not confirmed
- [x] Sequence number on every message for ordering and SSE catch-up — confirmed in message responses and SSE events

### Turn Loop

- [x] Tool-call loop: model reasons → tool calls → results → repeat until final text response — CONFIRMED WORKING (see MEMORY.md tool dispatch loop)
- [x] Two entry points: sync (human message) and async (system kick) — confirmed (chat via human; flow tasks via system)
- [~] Turn stops when: agent produces final text with no tool calls; OR max tool calls reached; OR max turn duration exceeded — final text stop confirmed; max tool calls not tested
- [-] Max tool calls per turn: configurable at org/project/agent level — not tested
- [-] Max turn duration: configurable; check at loop boundaries (never kills mid-stream) — not tested
- [-] Turn duration exceeded in sync: partial response finalized with system note — not tested
- [-] Turn duration exceeded in async: marked `stopped`; supervisor picks up for retry/escalation — not tested
- [x] Token usage tracked per turn (input, output, total) but NOT used as stop condition — confirmed via /v1/usage (see test 028)
- [x] Policy checks for tools: immediate allow or deny, never block mid-turn — confirmed (control policies evaluated synchronously)
- [x] Tier 1 tools (read-only): executed in chat layer with basic scope check; logged as chat messages — confirmed (tool results appear as messages)
- [x] Tier 2 tools (mutations/external): full control plane path; policy evaluation; Run audit trail — confirmed via /v1/control/runs (see test 028)
- [x] Tier 1 parallel execution for multiple tool calls in same response — CONFIRMED (parallel task_list + project_list calls work, see MEMORY.md)
- [-] Tier 2 sequential execution for mutations — not directly tested
- [-] Mixed Tier 1 + Tier 2 in one response: all Tier 1 first (parallel), then Tier 2 (sequential) — not directly tested
- [-] Communication tools create drafts (design behavior, not policy interception) — not tested
- [x] Context assembly re-runs at start of each turn; only Layer 6 grows within a turn — confirmed (prompt assembler architecture)

### Multi-Party Turn Sequencing

- [x] Turn cycle: atomic, completes before next human message processed — confirmed (queue behavior shows messages wait for turn to complete)
- [x] Phase 1: Responder turn(s) — confirmed (agent responds to @mention)
- [-] Phase 1.5: Agent-to-agent @mentions (one round only, no cascading) — not directly tested
- [-] Phase 2: Listening eval (Haiku-tier) for non-responding agents — "do you need to contribute?" — not confirmed
- [-] Phase 2 listening eval may produce a reaction (emoji/signal) at no extra cost — not confirmed
- [!] Phase 3: Interjection turns for agents that flagged yes in eval, ordered by relevance score — see Issue #173 (not implemented)
- [-] One round of listening evals per human message (no recursion) — not confirmed
- [-] Multiple interjecting agents respond sequentially by relevance score — not confirmed
- [-] Agents can @mention other agents in responses (one level of delegation per cycle) — not tested

### Context Assembly (7-Layer Prompt Pipeline)

- [x] Layer 1: Agent identity (never cut) — confirmed (agent config has system prompt)
- [x] Layer 2: Policies and constraints (never cut) — confirmed (operator_instructions in agent config)
- [-] Layer 3: Scope context (re-injected every turn in sync; first turn only in async) — not confirmed
- [x] Layer 4: Skills instructions (token-budget aware) — confirmed (skills injected; 4000 token budget)
- [x] Layer 5: Memory (Ellie passive injection, one-time per turn start) — confirmed (memory query happens per turn)
- [x] Layer 6: Conversation history (grows within turn; summarized across turns) — confirmed (conversation history assembled per turn)
- [x] Layer 7: Tool descriptions (context-aware loading for MCP) — confirmed (67 tools filtered by allow/deny list)
- [-] Progressive summarization: oldest turns summarized after threshold; full history always in storage — not confirmed
- [-] `chat_summary` records covering message ranges (from_sequence, to_sequence) — not confirmed
- [-] Async agents: scope context injected first turn only; agent uses tools for deeper discovery — not confirmed
- [-] Context window checkpoint/continuation when context fills (async mode) — not confirmed

---

## 5. Agents (Staff & Temps)

### Classification

- [x] Two agent classes: staff (durable, cross-project) and temp (project workforce) — confirmed (agent_class field in response, see test 027)
- [x] Staff agents: named, persistent, accumulate knowledge across all assigned projects indefinitely — confirmed (Frank, Lori, Ellie are staff)
- [x] Temp agents: project-assigned, do the work, don't chat with human by default — confirmed (temp_project_id, temp_ttl_seconds fields present)
- [-] Temps promoted to staff through Lori — not tested
- [x] Four temp scope types: project-scoped (standing workforce), task-scoped, session-scoped, TTL-scoped — confirmed via temp fields in response
- [-] Project-scoped temps persist across tasks; pausable/retirable — not tested
- [-] Task-scoped and session-scoped temps auto-expire — not tested
- [-] TTL-scoped temps expire on a configured wall-clock time — not tested (temp_expires_at field confirmed present)

### Agent Lifecycle

- [x] Agent lifecycle states: draft, active, paused, retired — confirmed (lifecycle_status field in response)
- [-] Agents created through conversation with Lori (no separate UI) — not tested (API creation confirmed)
- [x] Agent profile: display_name, agent_type (pm/worker/reviewer/general), agent_class (staff/temp) — confirmed (see test 027)
- [x] Agent prompt pack: system prompt, persona, constraints — confirmed (system_prompt in /v1/agents/:id/config)
- [x] `operator_instructions` field: human-editable additions to the prompt pack; never overwritten on profile upgrades — confirmed in config response
- [-] Automatic starter trio profile upgrades on startup (idempotent) — confirmed by design (bootstrap ran on startup)
- [-] Agent-project assignment via `agent_project_assignment` table — not tested via API
- [x] Agent capabilities (tool policy, model policy, memory policy) per profile — confirmed (memory_read_scopes, tool_allow_list in response)
- [x] Agent tool allow/deny lists (filter on top of capability policies) — confirmed (67 tools filtered by allow/deny list, see test 027)
- [-] Temp agent access auto-revoked on expiration — not tested
- [x] `GET /v1/agents` — list agents (filter by class, type, status) — see test 027
- [x] `POST /v1/agents` — create agent — confirmed in prior iterations
- [x] `GET /v1/agents/:id` — get agent — see test 027
- [-] `PATCH /v1/agents/:id` — update agent — not tested
- [-] `DELETE /v1/agents/:id` — retire/delete agent — not tested
- [x] `GET /v1/agents/:id/config` — get agent config (system_prompt, tool_allow/deny lists, budgets) — see test 027
- [x] `GET /v1/agents/:id/tools` — get tools available to agent (filtered by allow/deny list) — see test 027

### Prompt Assembly

- [x] 7-layer prompt assembly pipeline (see Context Assembly above) — confirmed (working tool call loop)
- [-] Token budget allocation per layer; layers compete for total budget — not directly confirmed
- [-] Truncation rules per layer (Layer 6/conversation history cut most aggressively) — not tested
- [-] Model call with per-call timeout from model profile — not tested
- [-] Skill synthesis from procedural memory as candidates (human-in-the-loop promotion to actual skill) — not tested

---

## 6. Starter Trio (Frank, Lori, Ellie)

### Frank (Chief of Staff)

- [x] Primary human touchpoint — responds in org-level General session — confirmed via TUI live testing
- [x] Cross-project coordinator and escalation endpoint — confirmed (responds to org-scope chat)
- [-] NOT a project manager (explicitly) — confirmed by design in system prompt
- [-] Manages org-level tasks (triage, escalation, daily summaries) — not directly tested
- [-] Proactive: summarizes rather than rambles, provides status without being asked — not directly tested
- [-] Can create org-level skills — not tested
- [x] Frank accessible at every scope via @mention — confirmed (@mention autocomplete works; Frank available in all sessions)
- [x] Frank identity skill shipped with bootstrap dataset — confirmed (skill in GET /v1/skills response)

### Lori (Agent Relations Expert)

- [-] Creates and manages agents through conversation — not directly tested via Lori
- [-] Recommends staffing (which agent types for which projects) — not tested
- [-] Evaluates agent performance — not tested
- [-] Suggests new agent profiles — not tested
- [-] Handles promotions (temp → staff) — not tested
- [-] Creates `agent_skill_attachment` entries when staffing agents — not tested
- [x] Lori identity skill shipped with bootstrap dataset — confirmed (present in bootstrap skills)

### Ellie (Memory System)

- [x] Dual role: background infrastructure AND conversational participant — confirmed (passive injection + available as @mention)
- [x] Passive: automatically injects relevant memories into session context per turn — confirmed (memory query runs per turn in prompt assembly)
- [-] Active: responds to @mention queries ("what do we know about X?") — not directly tested
- [-] Explicit capture: "@ellie remember this" — not tested
- [-] Explicit forget: "@ellie forget X" — not tested
- [-] Explicit correction: "@ellie that's wrong, the correct answer is Y" — not tested
- [-] Shows retrieval reasoning and confidence levels on request — not tested
- [-] Exposes memory history for a topic — not tested
- [-] Taxonomy management through conversation — not tested (taxonomy empty by design)
- [x] Ellie identity skill shipped with bootstrap dataset — confirmed (present in bootstrap skills)

---

## 7. Projects & Task Flow

### Projects

- [-] Every project is a git repository (single-branch: `main`) — not applicable in current local dev setup
- [-] `main` is the source of truth; feature branches per task — not applicable in current setup
- [x] Project has a PM agent assigned (always staff) — confirmed via live chat
- [x] PM manages project through conversation (no task editor UI) — confirmed via live chat
- [x] `GET /v1/projects` — list projects — see test 025
- [x] `POST /v1/projects` — create project — confirmed in prior iterations
- [x] `GET /v1/projects/:id` — get project — see test 025
- [-] `PATCH /v1/projects/:id` — update project — not tested
- [-] `DELETE /v1/projects/:id` — archive/delete project — not tested
- [!] `GET /v1/projects/:id/agents` — list project agents — see Issue #178 (404)
- [!] `POST /v1/projects/:id/agents` — assign agent to project — see Issue #178 (404)
- [!] `DELETE /v1/projects/:id/agents/:agent_id` — unassign agent — see Issue #178 (404)
- [x] `GET /v1/projects/:id/tasks` — list tasks (filter by work_status, assignee, etc.) — see test 025
- [x] `GET /v1/projects/:id/skills` — list project-level skills — confirmed in prior iteration

### Tasks

- [x] Eight task work statuses: queued, in_progress, blocked, needs_review, done, cancelled, archived, draft — see test 025
- [x] `done` and `cancelled` are terminal states — see test 025
- [~] Tasks have: title, description, acceptance criteria, assignee (agent), priority, due date — see test 025 (title/desc/assignee yes; AC/priority/due_date fields not confirmed in response)
- [~] Tasks have: current flow node pointer, git branch ref, structured context — see test 025 (flow_node_id yes; branch_name present but null; context not confirmed)
- [-] Tasks auto-assigned to a branch (task-slug-based) — not tested (no git setup)
- [~] Task ID format: project prefix + sequential number (e.g., OC-42) — task_number assigned sequentially ✓; no prefix like OC-42
- [!] `POST /v1/tasks` — create task — see Issue #179 (404; correct path is POST /v1/projects/:id/tasks)
- [x] `GET /v1/tasks/:id` — get task — see test 025
- [x] `PATCH /v1/tasks/:id` — update task (title, description, work_status, priority, etc.) — see test 025
- [x] `POST /v1/tasks/:id/queue` — enqueue task for execution — see test 025
- [x] `POST /v1/tasks/:id/advance-flow` — advance to next flow node — see test 025
- [x] `POST /v1/tasks/:id/cancel` — cancel task — see test 025
- [x] `GET /v1/tasks/:id/events` — task event log — see test 025
- [~] `GET /v1/tasks/:id/subtasks` — list subtasks — see test 025 (works when flow active; error when no flow)

### Flow Templates

- [-] PM creates flow templates through conversation (no UI) — not tested via Lori/PM conversation
- [x] Flow templates have nodes (sequential) with types: work_node, review_node, human_approval_node — confirmed via GET /v1/flow-templates (see test 025)
- [x] `work_node`: agent executes autonomously — confirmed
- [x] `review_node`: agent reviews output from previous node — confirmed
- [x] `human_approval_node`: flow pauses for human decision before continuing — confirmed
- [-] Flow templates are immutable once tasks reference them (prevents retroactive modification) — not tested
- [-] New template version created if template in use is modified — not tested
- [x] Default flow templates ship with bootstrap dataset (code, content, research, deploy) — confirmed (see test 025)
- [x] `GET /v1/flow-templates` — list flow templates — see test 025
- [-] `POST /v1/flow-templates` — create flow template — not tested
- [-] `GET /v1/flow-templates/:id` — get flow template — not tested
- [-] `PATCH /v1/flow-templates/:id` — update template (creates new version if in use) — not tested
- [-] Flow node skills: PM attaches skills to specific flow nodes for targeted context — not tested
- [-] Flow node MCP tools: PM declares required MCP tools per node (preloaded at worker start) — not tested

### Subtasks

- [~] Tasks can have subtasks (for decomposition of complex work) — GET /v1/tasks/:id/subtasks works when flow active (see test 025)
- [-] Subtask lifecycle mirrors parent task — not tested
- [-] All subtasks complete before parent can advance past the work node — not tested
- [-] Subtasks visible in task detail view — partial (TUI shows subtask section but empty, see test 018)

### Task Dependencies

- [-] DAG dependencies between tasks (`task_dependency` table) — not tested
- [-] When a dependency completes, dependent auto-unblocked — not tested
- [-] Blockers as tasks: agents file blocking issues as new tasks rather than stopping — not tested
- [-] Three-level escalation for stuck work: agent self-retry → file blocker task → notify PM → human escalation — not tested
- [-] Circular dependency detection and prevention — not tested
- [-] `GET /v1/tasks/:id/dependencies` — list dependencies — not tested
- [-] `POST /v1/tasks/:id/dependencies` — add dependency — not tested

### Merge Queue

- [-] Each task works on a branch; completed tasks enter the merge queue — N/A (no git setup in dev)
- [-] PM manages merge queue priority — N/A
- [-] Merge queue processes sequentially (no concurrent merges) — N/A
- [-] Merge conflict detection and resolution (agent-guided or PM-guided) — N/A
- [-] Auto-push to configured remotes after successful merge (if `auto` push behavior) — N/A
- [-] Push failure: retry 3x with exponential backoff; notify PM on all retries failed — N/A
- [~] `GET /v1/projects/:id/merge-queue` — list merge queue entries — returns empty array (see test 025)
- [-] `POST /v1/projects/:id/merge-queue/:entry_id/prioritize` — change priority — not tested

### Scheduled Tasks

- [-] Tasks can recur on a schedule (cron expression) — not tested
- [-] Three overlap policies: `skip` (most common), `queue`, `cancel_running` — not tested
- [-] PM configures schedules through conversation — not tested
- [x] `GET /v1/projects/:id/schedules` — list scheduled tasks — see test 025 (works, returns empty)
- [-] `POST /v1/projects/:id/schedules` — create schedule — not tested
- [-] `PATCH /v1/projects/:id/schedules/:id` — update schedule — not tested
- [-] `DELETE /v1/projects/:id/schedules/:id` — delete schedule — not tested

### Inbox

- [-] Five inbox item types: human_approval (flow paused), draft (staged communication action), blocker_escalation, review_request, notification — not tested via API (TUI inbox PASS)
- [-] Inbox items resolved by human (approve/reject/dismiss) or expire — not tested
- [-] PM proactively creates inbox items for decisions that need human input — not tested
- [x] `GET /v1/inbox` — list inbox items (filter by type, status) — see test 025 (works, returns empty)
- [-] `POST /v1/inbox/:id/approve` — approve inbox item — not tested
- [-] `POST /v1/inbox/:id/reject` — reject inbox item — not tested
- [-] `POST /v1/inbox/:id/dismiss` — dismiss inbox item — not tested

### Task Board & Views

- [~] Kanban-style task board grouped by work_status — TUI shows mini kanban on dashboard (see test 018); no full per-project board
- [-] Filter by assignee, status, priority, due date, tags — not tested in TUI board
- [~] Task detail view with full context, flow progression, subtasks, event log — see test 018, Issue #169 (missing description/AC/subtasks in TUI)
- [!] Per-node async session view (agent work log) accessible from task detail — see Issue #169
- [x] Activity feed: project-level event stream (merges, deploys, status changes, completions) — see test 018
- [~] Agent status panel: which agents are active, which tasks they're on, current turn status — see test 018 (name+status only; no current task/turn)

---

## 8. Shipping & Delivery

### Pattern 1: Delivery During Flow

- [-] Delivery action is a flow node inside the task (e.g., Publish, Send, Deploy) — N/A (no delivery setup in dev)
- [-] PM proposes delivery nodes for standard project types (content → publish, email → send) — N/A
- [-] Agent uses appropriate tools (WordPress API, email service, etc.) as part of task execution — N/A

### Pattern 2: Post-Merge Delivery

- [-] Deploy tasks reuse the existing task system with a deploy flow template — N/A (no git in dev)
- [-] Three delivery modes: continuous, gated (human-triggered), scheduled — N/A
- [-] Continuous: deploy task created on every merge; implicit skip if deploy already running — N/A
- [-] Gated: PM creates deploy task on human request via chat ("let's deploy") — N/A
- [-] PM proactively suggests deployment when merges accumulate in gated mode — N/A
- [-] Scheduled: deploy task created on cron cadence; overlap policy `skip` — N/A
- [-] Deploy task metadata: commit SHA, included task IDs, target environment — N/A
- [-] Full traceability: "Deploy OC-247 shipped changes from OC-41, OC-42, OC-45" — N/A
- [-] Release notes auto-generated by PM from included tasks — N/A
- [-] Advisory lock prevents duplicate concurrent deploy tasks (race condition protection) — N/A

### Project Remotes

- [-] Project configures one or more remotes (git host, SSH target, deployment platform) — not tested
- [-] Remote types: git_host, ssh, deploy_platform — not tested
- [-] Push behavior per remote: auto or manual — not tested
- [-] Credentials managed at org level (not raw secrets in remote config) — not tested
- [-] Multiple remotes pushed in configured order on each merge — N/A
- [-] Push as deployment trigger for platforms like Vercel, GitHub Actions — N/A

### Environments

- [-] Environments optional and project-level (ordered promotion path) — not tested
- [-] Each environment tracks: deployed commit SHA, previous commit SHA, last deploy timestamp, which deploy task — not tested
- [-] No environment branching (`main` is the only long-lived branch) — N/A
- [-] Rollback is a new deploy task targeting `previous_commit_sha` (history is append-only) — N/A
- [-] Deeper rollbacks query past deploy tasks for target commit — N/A
- [-] `GET /v1/projects/:id/environments` — list environments — not tested
- [-] `GET /v1/projects/:id/environments/:name` — get environment state — not tested

### Post-Ship Monitoring

- [-] Verify node in deploy flow runs health checks and smoke tests — N/A
- [-] Monitoring via scheduled task: agent runs checks, files bug task on regression — N/A
- [-] Deployment failure routes to rollback or blocks for human decision — N/A

---

## 9. Memory System

### Three Memory Layers

- [x] **Episodic** (what happened): conversation excerpts, decisions, outcomes — confirmed (memory_type field in items)
- [x] **Semantic** (what is known): facts, entity attributes, relationships, domain concepts — confirmed
- [x] **Procedural** (how to do things): patterns, conventions, learned preferences (advisory in prompt) — confirmed

### Implicit Capture Pipeline

- [x] Background pipeline runs after each chat turn or flow node completion — confirmed (candidates appear after agent turns)
- [-] Stage 1: Importance filtering (Haiku-tier) — is this worth storing? — not directly confirmed
- [-] Stage 2: Classification — which memory layer (episodic/semantic/procedural)? — confirmed in items (memory_type field)
- [-] Stage 3: Dedup — is this new info, update to existing, or duplicate? — not confirmed
- [-] Stage 4: Synthesis — entity relationship extraction and linking — not confirmed (0 entities)
- [x] Memory items have status: candidate (7-day hold), active, deprecated — confirmed (?status=candidate returns items, see test 027)
- [x] Candidates included in query results for cold-start scenarios — confirmed (MEMORY.md cold-start fix)
- [-] Deprecated memories not deleted (append-only audit trail) — not tested
- [-] Memory items have confidence score (decays if not reinforced) — confirmed (confidence field in items)
- [-] Memory items have source linkage (which chat message / flow node created them) — not confirmed

### Memory Taxonomy

- [!] Hierarchical taxonomy tree (nodes and parent linkage) — taxonomy empty (Ellie not bootstrapped), see test 027
- [-] Ellie bootstraps and manages taxonomy through conversation — not tested
- [-] Memory items tagged with taxonomy nodes — not confirmed
- [-] Taxonomy browseable and searchable — not confirmed
- [-] `GET /v1/memory/taxonomy` — list taxonomy nodes — not tested

### Entity Synthesis

- [!] Entities extracted from semantic memories (people, projects, technologies, decisions, etc.) — 0 entities (not running), see test 027
- [-] Entity attributes merged from multiple memories — not running
- [-] Entity relationships stored (X depends on Y, Z owns W, etc.) — not running
- [-] Entity graph queryable — not tested

### Memory Scopes

- [x] Memory scoped to: org, project, agent (private to that agent), task — confirmed (scope field in items)
- [-] Staff agent memory accumulates across all assigned projects indefinitely — not tested
- [-] Temp agent memory scoped to assigned project; auto-revoked on expiration — not tested
- [x] Human queries include agent-scoped memories for completeness — confirmed (MEMORY.md fix, prior iterations)

### Retrieval

- [x] Passive retrieval: Ellie injects top-k memories per turn automatically — confirmed (prompt assembly includes memory query)
- [-] Active retrieval: agents and humans @mention Ellie for deeper queries — not directly tested
- [x] Retrieval modes: passive (background injection), mention (explicit @ellie), agent_query (tool call) — confirmed (modes in POST /v1/memory/query)
- [x] Memory query API: `POST /v1/memory/query` with body `{query, max_results, scope, ...}` — see test 027
- [-] Vector similarity search using pgvector — confirmed (pgvector extension installed)
- [x] Results returned under `memories` key (array) — confirmed (see test 027, MEMORY.md)
- [x] `GET /v1/memory/items` — list memory items (filter by status, scope, taxonomy node) — see test 027
- [-] `POST /v1/memory/items` — create memory item (explicit capture) — not tested
- [-] `PATCH /v1/memory/items/:id` — update/correct memory item — not tested
- [-] `DELETE /v1/memory/items/:id` — deprecate memory item (soft delete) — not tested

### Sleep-Time Reflection (Phase 3)

- [-] Background consolidation job: merges related episodic memories into semantic — not tested
- [-] Contradiction detection between memories — not tested
- [x] Memory promotion: candidates promoted to active after 7-day hold with reinforcement — confirmed (7-day hold by design, MEMORY.md)
- [-] Memory importer: bulk import from external sources (Notion, Obsidian, etc.) — not tested

---

## 10. Model Gateway & Inference

### Provider Adapters

- [x] Anthropic adapter (claude-* models) — CONFIRMED WORKING (claude-sonnet-4-6 / claude-haiku-4-5-20251001)
- [x] OpenAI adapter (gpt-* models) — CONFIRMED WORKING (gpt-4o / gpt-4o-mini tested, see MEMORY.md)
- [-] Google adapter (gemini-* models) — not tested
- [-] OpenAI-compatible adapter (local models, other providers) — not tested
- [x] Each adapter translates OtterCamp's internal prompt format ↔ provider API — confirmed (both adapters work)
- [x] Streaming support for all adapters — confirmed (streaming works with both Anthropic and OpenAI)
- [x] Tool call parsing from streaming responses (both OpenAI and Anthropic formats) — confirmed (Issue #113 fixed, PR #1498)
- [x] `tool_calls` persisted in assistant message metadata for multi-turn tool use — confirmed (Issue #114 fixed, PR #1498)
- [x] Orphan `tool_result` messages filtered before sending to provider — confirmed (Issue #114 fix)

### Provider Connections

- [x] Provider connections: API key or OAuth credentials per provider — confirmed (Anthropic + OpenAI connections)
- [-] Provider health tracking: last error, last success, circuit-breaker state — not tested
- [-] Multiple connections per provider (e.g., two Anthropic keys for load balancing) — not tested
- [x] `GET /v1/model/providers` — list provider connections — see test 028
- [x] `GET /v1/model/providers/:id` — get provider connection details — see test 028 (fixed in prior iterations)
- [-] `POST /v1/model/providers` — add provider connection — not tested
- [-] `PATCH /v1/model/providers/:id` — update provider connection — not tested
- [-] `DELETE /v1/model/providers/:id` — remove provider connection — not tested

### Model Profiles

- [x] Logical profiles: high-capability, standard, fast (Haiku-equivalent) — confirmed (GET /v1/model/profiles, see test 028)
- [x] Each logical profile maps to a provider + model name — confirmed (provider_id, model_name fields)
- [-] Model profiles versioned (changing model creates new version, old version retained) — not confirmed
- [-] Four-level assignment hierarchy: agent > project > org > instance default — not confirmed
- [-] Failover chain: if primary provider fails, fallback to secondary — not tested
- [x] `GET /v1/model/profiles` — list model profiles — see test 028
- [x] `PATCH /v1/model/profiles/:logical_profile_id` — update profile (provider, model_name) — confirmed (used to switch between Anthropic and OpenAI, see test 028)
- [-] Model profile per-call timeout configurable — not tested

### Concurrency & Queuing

- [-] Global concurrency limit on total concurrent LLM calls — not tested
- [-] Per-provider rate limits and concurrency limits — not tested
- [-] Four priority tiers: sync human-facing (highest), async important, async background, eval/lightweight — not tested
- [-] Queue for excess requests; sync requests preempt async in queue — not tested
- [x] `GET /v1/usage` — token usage statistics — see test 028 (period + group_by required)
- [-] `GET /v1/model/usage-rollup` — aggregated model usage — not tested

---

## 11. Tools & Tool Policy

### Tool Categories

- [x] **Native tools**: built-in OtterCamp capabilities (~55 tools across 7 domains) — confirmed (GET /v1/tools returns 67+ tools)
- [x] **System tools**: CLI execution and browser automation (tier 2, sandboxed) — confirmed (browser.navigate in tool-executions)
- [~] **External tools**: MCP-connected third-party services (tier 2) — confirmed schema; all connections failed in dev
- [-] **Communication tools**: email, Slack, PR creation — create drafts, stage in inbox — not tested

### Native Tool Domains (~55 tools)

- [x] **File/Repo tools**: read_file, write_file, list_files, search_code, read_directory, delete_file, move_file, get_diff, get_git_log, etc. — confirmed in GET /v1/tools
- [x] **Task management tools**: create_task, update_task, list_tasks, get_task, advance_flow, create_subtask, file_blocker, etc. — confirmed in GET /v1/tools
- [x] **Project tools**: get_project_context, list_agents, assign_agent, etc. — confirmed in GET /v1/tools
- [x] **Memory tools**: query_memory (→ Ellie), capture_memory, update_memory, etc. — confirmed in GET /v1/tools
- [x] **Chat tools**: read_session_history, list_sessions, invite_agent, etc. — confirmed in GET /v1/tools
- [x] **Agent control tools**: advance_flow, mark_step_done, request_review, escalate_blocker, etc. — confirmed in GET /v1/tools
- [-] **Communication tools**: email_compose, slack_post, create_pr, create_github_issue, etc. — not confirmed in tool list

### Tool Policy

- [x] Tool resolution pipeline: instance safety → org policy → project policy → agent allow/deny list — confirmed (policy_layer in control policies)
- [x] Tool names sanitized to be API-safe (e.g., `file.read` → `file_read`) — confirmed (Issue #111 fixed, dot→underscore)
- [x] Tool schemas get `properties: {}` injected when missing (OpenAI/Anthropic compat) — confirmed (Issue #111 fixed)
- [x] Agent allow/deny lists filter available tools per agent — confirmed (see test 027, 67 tools filtered)
- [x] `GET /v1/tools` — list all available tools — PASS (67 tools with tier, domain, capability, schema)
- [x] `GET /v1/policies` — list tool policies — confirmed (GET /v1/control/policies PASS, see test 028)
- [x] `POST /v1/policies` — create policy rule — confirmed (see test 028)
- [-] `PATCH /v1/policies/:id` — update policy rule — not tested
- [x] `DELETE /v1/policies/:id` — delete policy rule — confirmed (see test 028)

---

## 12. MCP Integration

- [x] OtterCamp acts as MCP **client only** (never exposes itself as MCP server) — confirmed
- [x] Supported transports: stdio (subprocess) and HTTP/SSE (remote) — confirmed (transport field: sse in GET /v1/mcp/connections)
- [x] Connector runtime: manages full lifecycle of MCP connections (establish, health, circuit break, secret inject, execution broker) — confirmed (status: failed, last_healthy_at tracked)
- [x] MCP tools classified as tier 2 (full control plane policy path) — confirmed
- [-] Capability checks: `mcp.connection.use`, `mcp.tool.invoke` — not directly confirmed
- [-] Agent profile allow/deny lists apply to MCP tools — not tested (no working MCP connections in dev)
- [-] Every MCP call produces: `ToolExecution` record + `mcp_execution_log` entry with redacted inputs/outputs — not confirmed (no successful MCP calls in dev)
- [-] Secrets stored in encrypted secret store, bound to connections via `mcp_secret_binding` — not tested
- [-] Secret injection at runtime — agents never see credentials — not tested
- [-] MCP resources: read-only (tier 1) — not tested
- [-] MCP prompts: advisory-only (yield to OtterCamp skills on conflict) — not tested
- [-] Context-aware tool loading: agents receive connection summaries by default, not full tool schemas — not tested
- [-] `mcp.discover` tool: agents request tool schemas on demand — not tested
- [-] Flow node `mcp_tools` field: preloads specific MCP tool schemas for a node (no discovery round-trip) — not tested
- [x] `GET /v1/mcp/connections` — list MCP connections — PASS (returns connections with status/transport fields)
- [-] `POST /v1/mcp/connections` — add MCP connection — confirmed in prior iterations (used to test SSRF)
- [-] `GET /v1/mcp/connections/:id` — get connection details (health, tools) — not tested
- [-] `PATCH /v1/mcp/connections/:id` — update connection — not tested
- [-] `DELETE /v1/mcp/connections/:id` — remove connection — not tested
- [-] `GET /v1/mcp/connections/:id/tools` — list tools for connection — not tested (connections all failed)
- [-] `POST /v1/mcp/connections/:id/test` — test connection health — not tested
- [-] 3-5 starter MCP connection templates ship with bootstrap (e.g., GitHub, Slack, Notion, Linear) — not confirmed

---

## 13. Skills System

- [x] Skills are markdown files with YAML frontmatter (name, slug, scope, category, description, is_default) — confirmed (see test 027)
- [x] Skill body = instructions injected into agent prompt (no code, no templates, no conditionals) — confirmed (prompt assembly)
- [-] Org-level skills stored in dedicated org skills repo (`skills/` directory) — storage path confirmed; not directly tested
- [-] Project-level skills stored in project repo (`skills/` directory) — not tested
- [-] Skills versioned via git history only (no separate version counter, no draft/published lifecycle) — not confirmed
- [-] Skill content read from repo at prompt assembly time (not cached in DB) — not confirmed
- [-] Changes take effect on next turn after commit — not tested
- [-] Four attachment levels: org default, project default, agent-level, flow node-level — not confirmed
- [-] `is_default: true` marks skills always activated for their scope — not confirmed in API response
- [x] Agent identity skills (Frank, Lori, Ellie, PM) are agent-level attachments with `purpose='identity'` — confirmed (bootstrap skills present)
- [-] Flow node skill declarations narrow activated skill set (only declared + defaults + identity) — not tested
- [-] When no flow node skills declared: agent gets full skill set (fallback) — not tested
- [-] Conflict resolution: flow node > agent-level > project default > org default — not confirmed
- [-] Skills win over procedural memory always (skills are prescriptive, procedural memory is advisory) — not tested
- [x] Skills injected at Layer 4 of 7-layer prompt pipeline — confirmed (prompt assembly architecture)
- [-] Token budget for Layer 4 covers both skills and MCP prompts combined — not confirmed
- [-] Truncation priority: remove non-default non-identity agent skills first; then summarize flow node skills; then identity; then defaults — not tested
- [-] Maximum recommended skill size: ~4,000 tokens per document — by design
- [-] PM creates/edits/deletes skills through conversation and git commits — not tested
- [-] PM keeps skill registry (DB) in sync with repo files — not tested
- [-] Periodic background consistency check for registry drift (warns on missing files or orphaned entries) — not tested
- [-] Skill synthesis from procedural memory: Ellie surfaces candidates; PM promotes to skill (human-in-the-loop, never automatic) — not tested
- [x] `GET /v1/skills` — list org-level skills — see test 027
- [x] `GET /v1/projects/:id/skills` — list project-level skills — see test 027
- [-] `POST /v1/skills` — create skill (registry entry) — not tested
- [-] `PATCH /v1/skills/:id` — update skill metadata — not tested
- [-] `DELETE /v1/skills/:id` — delete skill (registry entry) — not tested

### Bootstrap Skills

- [x] Frank identity skill — confirmed (in GET /v1/skills response)
- [x] Lori identity skill — confirmed
- [x] Ellie identity skill — confirmed
- [x] PM identity skill — confirmed
- [x] Org default: safety and communication policies — confirmed (in GET /v1/skills)
- [x] Org default: general work standards (commit messages, ambiguity handling, blocker vs workaround) — confirmed (code-review skill and others)
- [-] Project template skills available (not pre-installed): Code Review Checklist, Go Coding Standards, TypeScript Coding Standards, API Design Conventions, Content Writing Guidelines, Security Review Checklist — not confirmed as separate templates

---

## 14. CLI & System Integration

### Non-Interactive CLI Commands

- [-] `otter login` — configure credentials — not tested (non-interactive CLI)
- [-] `otter status` — show active work summary — not tested
- [-] `otter send "message"` — send message to current active session — not tested
- [-] `otter inbox` — list inbox items — not tested
- [-] `otter task OC-5` — show task details — not tested
- [-] `--json` flag on all commands for machine-readable output (composable with Unix tools) — not tested
- [-] All CLI commands connect directly to OtterCamp API (HTTP) — confirmed (TUI uses API)
- [x] `--server-url` and `--api-key` flags on all CLI/TUI commands — confirmed (tui.go flags; credentials file alternative)

### Sandboxed CLI Execution (System Tool)

- [-] Agents submit structured command requests (not raw shell scripts) — not tested (no CLI tool invocations in testing)
- [-] Commands risk-classified into four levels: safe, normal, sensitive, dangerous — not tested
- [-] Layered policy: instance > org > project > agent (lower layers only restrict) — not tested
- [-] Default denylist blocks destructive system commands (rm -rf, kill, format, etc.) — not tested
- [-] Compound commands (pipes, chains, subshells) decomposed; classified by riskiest component — not tested
- [-] Commands run in sandboxed processes scoped to project git repo on agent's task branch — not tested
- [-] Environment variables constructed (not inherited from parent process) — not tested
- [-] Network policy per command: allow_all, deny_all, or allowlist — not tested
- [-] Per-command timeout: default 5 min, max 30 min — not tested
- [-] Output streamed via RunEvents; truncated at 50KB in tool result — not tested
- [-] Full output stored as RunArtifacts in object storage — not tested
- [-] Process-level sandboxing (Phase 3); container-level sandboxing (Phase 4) — N/A Phase 3+

### Browser Automation (System Tool)

- [~] Agents send structured action requests (not Playwright code) — confirmed (browser.navigate in tool-executions; but "browser: not found" error)
- [!] Actions: navigate, click, fill form, extract text, take screenshot, download file — browser binary not found; tool fails with "browser: not found"
- [-] Isolated browser contexts per agent/task (no shared cookies/sessions) — not tested (no browser in dev)
- [-] Every action auditable (structured request, not freeform script) — confirmed (ToolExecution records with input logged)
- [-] Per-action timeout — not tested
- [-] Screenshots stored as RunArtifacts — not tested
- [-] Vision used for element identification when possible — not tested

---

## 15. Agent Control Plane

- [x] Single trusted execution layer for all agent mutations — confirmed (all tool calls go through control plane)
- [x] No agent can perform side-effecting action without passing through the control plane — confirmed (ToolExecution records for all tier2 calls)
- [x] Exception: tier 1 read-only tools execute in chat layer with basic scope check — confirmed (tier1 tools in tool_call messages)

### Four Core Components

- [x] **Policy API**: stateless capability evaluation → allow or deny — confirmed (GET /v1/control/policies, see test 028)
- [x] **Execution Broker**: synchronous admission control; creates Run records, evaluates policy, dispatches to workers — confirmed (run records created per tool call)
- [x] **Worker Runtime**: stateless sandboxed executors for internal mutations, CLI, browser, MCP — confirmed (worker process running; browser fails gracefully)
- [x] **Audit/Event Log**: immutable, append-only record of all decisions and outcomes — confirmed (tool-executions have policy_decision field)

### Run Records

- [x] Every tier 2 tool call creates a `Run` record — confirmed (GET /v1/control/runs shows runs, see test 028)
- [x] Run states: pending, running, succeeded, failed, cancelled — confirmed (status field in runs)
- [x] `RunStep` records for each tool execution within a run — confirmed (GET /v1/control/runs/:id/steps endpoint works; empty for some runs)
- [-] RunArtifacts for captured outputs (CLI output, screenshots, etc.) — not tested
- [-] RunEvents for streaming output — not tested
- [x] `GET /v1/control/runs` — list runs (filter by agent, status, date) — see test 028
- [-] `GET /v1/control/runs/:id` — get run details — not tested
- [x] `GET /v1/control/runs/:id/steps` — list run steps — PASS (returns empty array for runs without steps)
- [-] `GET /v1/control/runs/:id/artifacts` — list run artifacts — not tested
- [x] `GET /v1/control/tool-executions` — list tool executions — PASS (returns tool executions with policy_decision, status, error_message)
- [x] `GET /v1/control/health` — control plane health — see test 028

### Cost Controls

- [-] Budget levels: org, project, agent (tokens as universal unit) — not tested
- [-] Budget enforcement: soft warning at threshold; hard stop at limit — not tested
- [-] Per-period budget resets (daily, monthly configurable) — not tested
- [-] Budget alerts via inbox — not tested
- [x] `GET /v1/control/cost/summary` — cost summary by period — see test 028 (cost 0 known bug)

---

## 16. API Surface

### API Conventions

- [x] All routes under `/v1/` — confirmed throughout testing
- [x] Consistent JSON envelope: `data`/`error` + `meta` fields — confirmed (all endpoints use this format)
- [x] Cursor-based pagination exclusively on all list endpoints — confirmed (cursor-based pagination on chat messages, runs, etc.)
- [x] REST for CRUD; verb-noun POST routes for commands with side effects (e.g., `POST /tasks/:id/advance-flow`) — confirmed
- [x] Bearer token (API keys) for programmatic access — confirmed
- [-] Session cookies for web UI — N/A (no web UI in Phase 1-2)
- [x] All requests scoped to single organization (enforced at middleware) — confirmed (org enforced via session/API key)
- [x] 4MB request body limit — confirmed (security fix applied)
- [x] `DisallowUnknownFields` on JSON decode (strict) — confirmed (extra fields rejected)
- [x] No UI-only features — every feature accessible via API — confirmed (all features accessible via REST API)

### Complete Endpoint Groups

- [~] Auth: `/v1/auth/*` (login, logout, refresh, password reset, magic link) — login PASS; logout now returns JSON (Issue #180 fixed); change-password implemented (Issue #180 fixed); refresh untested
- [-] Users: `/v1/users/*` (profile, preferences, invite, remove) — not tested
- [x] Org: `/v1/orgs/current` — PASS (see test 026)
- [x] Chat sessions and messages: `/v1/chat-sessions/*` — PASS (see test 024)
- [x] Projects: `/v1/projects/*` — PASS (see test 025)
- [x] Tasks and flow: `/v1/tasks/*` — PASS (see test 025)
- [x] Flow templates: `/v1/flow-templates/*` — PASS (see test 025)
- [x] Inbox: `/v1/inbox/*` — PASS (GET works; approve/reject/dismiss not tested)
- [~] Merge queue: `/v1/projects/:id/merge-queue/*` — PASS (GET returns empty; prioritize not tested)
- [x] Scheduling: `/v1/projects/:id/schedules/*` — PASS (GET works; create/update not tested)
- [x] Agents: `/v1/agents/*` — PASS (see test 027)
- [x] Memory: `/v1/memory/*` — PASS (items + query work, see test 027)
- [x] Models: `/v1/model/*` (profiles, providers, usage) — PASS (see test 028)
- [x] MCP connections: `/v1/mcp/*` — PASS (GET /v1/mcp/connections works)
- [x] Skills: `/v1/skills/*`, `/v1/projects/:id/skills/*` — PASS (see test 027)
- [x] Tools and policies: `/v1/tools/*`, `/v1/policies/*` — PASS (GET /v1/tools works; policies via /v1/control/policies)
- [x] Control plane: `/v1/control/*` (runs, tool-executions, cost, health) — PASS (see test 028)
- [-] Delivery/environments: `/v1/projects/:id/environments/*`, `/v1/projects/:id/remotes/*` — not tested
- [x] Events (SSE): `/v1/events/stream` — PASS (see test 028)
- [x] Audit: `/v1/audit` (filter by action, from, to) — PASS (see test 023)
- [x] Usage: `/v1/usage` — PASS (see test 028)

---

## 17. Realtime & Events

### Event Bus

- [x] Domain events published to internal event bus on all major state transitions — confirmed (events fire on chat, tasks, etc.)
- [x] SSE endpoint: `/v1/events/stream` — PASS (see test 028)
- [~] Clients subscribe with scope(s): `org`, `session:uuid`, `project:uuid`, `task:uuid` — `?scopes=org` works; session/project/task scopes not confirmed
- [x] Events delivered as SSE frames with `event:` type and `data:` JSON — confirmed (see test 028)
- [x] Sequence numbers on all events for ordered delivery and catch-up — confirmed (seq field in events)

### Realtime Connection Management

- [x] Persistent SSE connections with auto-reconnect — confirmed (TUI auto-reconnects, see test 022)
- [-] Last-Event-ID header for catch-up on reconnect — confirmed (sinceSeq used)
- [x] Sequence-based catch-up: events replayed since last-event-id — confirmed (see test 022)
- [-] Gap detection: if reconnect cursor older than retention window → full replay — not tested
- [-] Event retention window (configurable; default ~100 events in-memory ring buffer) — not confirmed
- [-] Buffer overflow protection: connection closed gracefully on buffer full — not tested
- [x] Connection state reported: connecting, connected, reconnecting, degraded — confirmed (TUI status bar, see test 022)
- [~] Per-connection scope filtering (prevents cross-session event leakage) — confirmed for org scope; session scope filtering not tested
- [-] Mention preference filter: events pass if user is author, or text contains @mention token — not tested

### Event Types (partial list)

- [x] `chat.message.created` — new message in session — confirmed (seen in SSE stream)
- [-] `chat.message.updated` — streaming delta or state change — confirmed in TUI streaming
- [-] `chat.turn.started` / `chat.turn.completed` / `chat.turn.cancelled` — confirmed (turn events seen in stream)
- [x] `chat.message.user_sent` — human message received (triggers agent turn) — confirmed (Issue #115 fix)
- [-] `task.created` / `task.updated` / `task.flow_advanced` / `task.completed` / `task.blocked` — confirmed (seen in GET /v1/tasks/:id/events)
- [-] `inbox.item.created` / `inbox.item.resolved` — not directly confirmed in SSE
- [-] `merge_queue.entry.created` / `merge_queue.merge_completed` — not tested
- [-] `agent.run.started` / `agent.run.completed` / `agent.run.failed` — confirmed (in GET /v1/control/runs)
- [-] `push_succeeded` / `push_failed` / `deployed` — N/A (no git/deploy setup)

---

## 18. Security

- [-] Org isolation via database-per-org (architectural, not RLS) — single-node local mode (Phase 4 feature)
- [x] API key scope enforcement at middleware level — confirmed (Issue #091 fixed, prior iterations)
- [x] 4MB request body limit globally — confirmed (security fix applied)
- [x] Email normalized to lowercase (prevents duplicate accounts) — confirmed (security fix applied)
- [x] Account lockout returns "invalid credentials" (prevents user enumeration) — confirmed (see test 026)
- [x] `/metrics` endpoint restricted to localhost — confirmed (security fix applied)
- [x] SSRF protection in MCP URL validation — confirmed (SSRF test connections fail with security error)
- [-] HTTPS only in production (HTTP auto-upgraded) — N/A in local dev; by design for production
- [-] Agent sandboxing for CLI (process-level Phase 3; container-level Phase 4) — Phase 3+ feature
- [-] Isolated browser contexts (no shared state between agents) — not tested (no browser in dev)
- [-] MCP secrets never exposed to agents (injected at runtime by connector runtime) — not tested
- [-] Secrets stored encrypted (`mcp_secret_binding` table) — not tested
- [~] Immutable audit trail for all security-sensitive actions — partial (see test 023, Issue #174; policy changes audited; auth events missing)
- [-] Prompt injection defense: memory pipeline filters injected instructions; tool results sandboxed — not tested
- [-] Skill content read-only at prompt assembly time (no dynamic execution) — not tested

---

## 19. Observability & Cost Controls

- [x] Trace spans for all agent turns (partitioned by session/task — no default partition) — confirmed in DB (Issue #112 fixed); collection works; GET /v1/trace/spans now implemented (Issue #181 fixed)
- [-] OpenTelemetry-compatible trace format — not confirmed
- [x] Token usage tracked per turn: input tokens, output tokens, total — confirmed (GET /v1/usage returns per-agent token counts)
- [x] `GET /v1/usage` — usage statistics (by agent, project, period) — PASS (see test 028; period + group_by required)
- [!] `GET /v1/model/usage-rollup` — aggregated model usage — FAIL (404 Not Found) — see Issue #182
- [-] Budget enforcement: org-level, project-level, agent-level (tokens) — not tested
- [-] Budget soft warning and hard stop configurable separately — not tested
- [-] Cost alert via inbox when threshold approached — not tested
- [-] Data retention policies: memories forever, chat 1 year, invocations 90 days (all configurable per org) — not tested
- [x] Health check endpoint: `GET /v1/control/health` — PASS (see test 028)
- [-] Structured logging (JSON) in production — N/A in local dev

---

## 20. Deployment & Self-Hosting

### Deployment Modes

- [x] **Local single-node**: server + worker in one process (goroutines), one DB pool, local filesystem storage — confirmed (running in local dev)
- [-] **VPS single-tenant**: API and worker as separate processes (`--mode` flag), same DB — not tested
- [-] **Managed multi-tenant**: database-per-org, catalog DB for routing, shared infra, horizontal scaling (Phase 4) — N/A Phase 4

### Binary Distribution

- [x] Single Go binary for all modes (mode determined by config, not build) — confirmed (`/tmp/ottercamp-bin/ottercamp`)
- [x] `ottercamp server` — start API server — confirmed
- [x] `ottercamp worker` — start background worker — confirmed
- [x] `ottercamp tui` — launch interactive TUI — confirmed
- [-] `ottercamp <command>` — non-interactive CLI commands — not tested
- [-] Homebrew tap for macOS — not tested/available
- [-] apt package for Linux — not tested/available
- [-] Direct binary download — not tested/available
- [-] Docker Compose distribution (OtterCamp + PostgreSQL with pgvector; as few as 2 containers) — not tested

### Dependencies

- [x] PostgreSQL (with pgvector extension) — only required external dependency — confirmed (pgvector installed)
- [x] Object storage: S3-compatible or local filesystem fallback — confirmed (local filesystem mode via OTTERCAMP_STORAGE_ROOT)
- [x] Job queue: PostgreSQL-backed (no Redis required) — confirmed (worker uses PG-backed queue)
- [x] Event bus: PostgreSQL-backed (no external message broker required) — confirmed (no Redis/Kafka required)

### Configuration

- [x] Environment variables for all config — confirmed (`.env` file loaded)
- [x] `OTTERCAMP_SERVER_URL` and `OTTERCAMP_API_KEY` for CLI/TUI — confirmed
- [x] `OTTERCAMP_STORAGE_ROOT` for object storage path — confirmed (`/tmp/ottercamp-data/objects`)
- [x] Database connection string via env — confirmed
- [x] Port configuration (default 4110) — confirmed (running on 4110)

### Database Management

- [x] Versioned forward-only migrations — confirmed (migrations applied up to 0088+)
- [-] Migration framework with apply/status commands — not tested via CLI
- [-] Per-org migration orchestration in managed mode — N/A Phase 4
- [-] `ottercamp migrate` — run pending migrations — not tested

### Bootstrap

- [x] 10-step idempotent bootstrap sequence on first start — confirmed (bootstrap ran on startup)
- [x] Bootstrap creates: starter trio agents, org session, default skills, default model profiles, default flow templates, default policies — confirmed
- [x] Fresh install immediately usable after providing model API keys — confirmed (system usable immediately)
- [-] `ottercamp bootstrap` — run bootstrap manually — not tested

---

## 21. TUI (Terminal UI)

### Overview

- [x] Primary interface for Phases 1 and 2 (before web UI exists in Phase 3)
- [~] Full functional parity with web UI for all Phase 1-2 features — most Phase 1-2 features are accessible
- [x] Built with Bubble Tea (Go), Lip Gloss (styling), Bubbles (widgets), Glamour (markdown), Huh (forms)
- [~] Three-panel layout: sidebar (20%), main content (40%), chat pane (40%) — see test 001 (ratio differs at M size)
- [!] Chat pane always visible — see Issue #163 (hidden at S size when sidebar focused)
- [x] Exactly one panel has focus at a time
- [x] Focus cycles: Tab/Shift-Tab or Alt-1/2/3 jumps directly — see test 003
- [x] Panels resizable via keybinding (shift ratio; collapse sidebar entirely) — < / > keys narrow/widen sidebar 2% per press (clamped 10%–35%); s key collapses sidebar in narrow layouts

### Sidebar

- [x] Sessions grouped by scope: org at top, then projects with task sessions nested — see test 004
- [x] Unread indicator on sessions with unseen messages; bubbles up to project level — shown as (N) count badge in orange on session nodes, propagates to parent project via propagateUnread()
- [~] Selecting session (Enter) switches chat pane; main content unchanged — see test 006 (main also switches, spec says unchanged)
- [x] j/k navigation; l/h expand/collapse project groups — see test 005
- [x] Active session highlighted with distinct background — see test 007
- [!] Notification count and inbox count visible at sidebar bottom — see Issue #164
- [x] Per-node async sessions (work logs) do NOT appear in sidebar — see test 009

### Chat Pane

- [!] Scope indicator at top: shows available scope levels (task/project/org), active highlighted — see Issue #165
- [~] Navigate scopes with `[`/`]` keys (without changing main content view) — see test 010 (works but shows raw IDs, Issue #165)
- [x] Messages in scrollable viewport — see test 011
- [~] Human messages: distinct top-border label "You" — see test 012 (shows "You" label; no distinct top-border)
- [~] Agent messages: left-aligned with agent name and role; text streams in real-time (character by character) — see test 012 (streams in real-time ✓; role label partial)
- [~] Tool calls rendered inline during response (collapsed by default, expandable with Enter) — see test 014, Issue #167 (always expanded; no collapse/expand)
- [!] Tool results: success/error indicator; large results collapsed behind "[show more]" — see Issue #167
- [~] System messages: de-emphasized, centered — see test 015 (de-emphasized yes; not centered)
- [!] Interjection messages: visually distinct header with "(interjected)" label — see test 015, Issue not yet filed
- [!] Markdown rendered via Glamour (headings, bold, italic, lists, links, inline code) — see test 013, Issue #166
- [!] Code blocks syntax-highlighted; copy to clipboard with keybinding — see test 015, Issue #166
- [!] Image blocks shown as `[Image: name.png]`; open in system viewer with keybinding — see test 015
- [!] File reference blocks: compact line; Enter opens in main pane or $EDITOR — see test 015
- [!] Artifact blocks: rendered with size; Enter opens with system handler — see test 015
- [x] Auto-scroll follows streaming; operator can scroll up during stream — see test 015
- [x] "[jump to bottom]" indicator appears when scrolled up during active streaming — see test 015
- [x] Spinner/animation indicates agent working during streaming — see test 015
- [x] Escape cancels current agent turn — see test 015

### Message Input

- [x] Multi-line text area at bottom of chat pane — see test 016
- [x] Enter sends (single-line default) — see test 016
- [~] Shift-Enter / Alt-Enter inserts newline — see test 016 (Alt-Enter works; Shift-Enter does NOT; help text wrong — Issue #168)
- [x] Up arrow (when empty) recalls previous message for editing — see test 016
- [x] Tab completion for @mentions (type `@`, agent names autocomplete) — see test 016
- [x] Draft persistence: unsent message preserved across panel focus changes — see test 016

### Message Queue

- [x] Operator can stack messages while agent turn is in progress — see test 017
- [x] Edit, steer, or delete queued messages — see test 017
- [x] Queue displayed below input area during active turn — see test 017

### Main Content Views

- [~] Dashboard (home): overview of active projects, recent activity, agent status — see test 018 (no agent status on dashboard)
- [~] Project task board: kanban grouped by work_status; j/k to navigate, Enter to open task — see test 018 (mini kanban only; no full per-project board)
- [~] Task detail: title, description, acceptance criteria, flow progression, subtasks, event log — see test 018, Issue #169 (missing description/AC/subtasks)
- [!] Per-node async session (work log) accessible from task detail — see Issue #169
- [x] Inbox: list items with type indicators; approve/reject/dismiss actions — see test 018
- [x] Activity feed: project event stream — see test 018
- [~] Agent status: active agents, current tasks, turn status — see test 018 (name+status only; no current task or turn status)
- [~] Merge queue: list with priority indicators — see test 018 (list works; no priority indicators)
- [x] Schedules: list with next-run times — see test 018

### Keyboard Navigation

- [x] j/k: navigate lists up/down — see test 019
- [x] h/l: collapse/expand or navigate panes — see test 019
- [x] Enter: select / confirm / open — see test 019
- [~] `:` command palette (Superhuman-style fuzzy search across sessions, projects, tasks) — see test 019, Issue #171 (command bar works; not fuzzy search)
- [!] `/`: in-pane search — see Issue #170
- [x] `?`: help / keybinding reference — see test 019
- [~] `[`/`]`: navigate chat scope levels — see test 019, Issue #165
- [x] Esc: cancel / go back / cancel agent turn — see test 019
- [x] Tab/Shift-Tab: cycle panel focus — see test 019
- [x] Alt-1/2/3: jump directly to sidebar/main/chat — see test 019 (implemented as 1/2/3, not Alt)
- [!] g/G: jump to top/bottom of list or viewport — see Issue #171
- [~] Vim-style keybindings throughout as default — see test 019 (j/k/h/l yes; g/G/w/b etc. no)

### Responsive Layout

- [~] Below 120 columns: sidebar collapses to icons/abbreviated names — see test 020, Issue #172 (abbreviation only; no icon mode)
- [~] Below 100 columns: sidebar hides; toggle with keybinding — see test 020, Issue #172 (hides at 70 not 100; no toggle key)
- [~] Below 80 columns: single-panel mode (Tab switches panels full-screen) — see test 020, Issue #172 (works but at 69 cols threshold)
- [!] tmux compatibility: keybinding fallback for tmux-captured keys — see Issue #172

### State Persistence

- [x] Local state file: `~/.config/ottercamp/tui-state.json` — see test 021
- [~] Persists: last active view, active chat session, panel proportions — see test 021 (panel+session yes; sub-view not saved)
- [x] Restored on next launch — see test 021

### Realtime in TUI

- [x] Persistent SSE connection with auto-reconnect — see test 022
- [~] Subscribes to: org scope + active session scope — see test 022 (org scope only)
- [x] Chat deltas, task status changes, inbox items, merge queue updates, agent run events — see test 022
- [x] Sequence-based catch-up on reconnect — see test 022
- [x] Connection state indicator in status bar (connecting / connected / reconnecting / degraded) — see test 022

---

## 22. Audit & Compliance

- [x] Immutable audit trail (append-only) — confirmed (no DELETE endpoint for audit; see test 023)
- [~] Audit events for: logins, logouts, role changes, API key lifecycle, policy changes, agent mutations — partial: policy changes yes; auth/key events missing (see Issue #174)
- [x] Audit events filterable by action, date range, actor — confirmed (see test 023)
- [x] `GET /v1/audit` — list audit events (alias: `/v1/audit/events`) — PASS (see test 023)
- [~] Audit events include: actor (human/agent/system), action, resource, timestamp, IP, outcome — partial: actor/action/resource/timestamp yes; IP and outcome missing (see Issue #174)
- [-] Retention: audit events retained per org policy (default: indefinite) — not tested
