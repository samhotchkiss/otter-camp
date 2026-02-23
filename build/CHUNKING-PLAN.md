# OtterCamp V2 — Chunking Plan

Generated: Iteration 4

This document organizes all implementation work into task groups ordered by layer. Each
task group becomes 1–2 task files. Size labels: S = small (1–2 days), M = medium (3–4 days),
L = large (5–7 days).

Every major domain follows the pattern: schema → service → API endpoints → CLI commands → tests.
Small domains combine steps; every domain must have all five covered somewhere.

---

## L0 — Foundation (est. 4 task files)

Running total after L0: **4**

- [ ] 001: Project scaffold and runtime harness — Go module init, directory structure,
  config loading (env vars), injectable clock abstraction, OTTERCAMP_MODE flag, logging
  framework, server entrypoint (serve command), worker entrypoint — S

- [ ] 002: Database infrastructure — PostgreSQL connection pool, migration runner (forward-only,
  numbered, transactional, schema_migrations table), pgvector extension, db-per-org connection
  routing stub, test harness DB clone setup — S

- [ ] 003: L0 schema — `organization`, `model_provider`, `tool_definition`, `memory_taxonomy_node`
  DDL migrations; repository layer for each; unit tests — S

- [ ] 004: Object storage abstraction — filesystem adapter (default/local) + S3-compatible
  adapter; unified interface used by chat artifacts, run artifacts, prompt/response storage;
  backup/restore CLI hooks — S

---

## L1 — Core Identity (est. 8 task files)

Running total after L1: **12**

- [ ] 005: Auth schema — `human_user`, `auth_session`, `api_key` DDL + repositories; bcrypt
  password hashing (work factor 12); SHA-256 token/key hashing; unique constraints — S

- [ ] 006: Auth service — login, session creation, session refresh (30-day sliding window),
  session revocation, API key issuance and validation; per-IP + per-account rate limiting
  (in-memory counters); local-auth mode (OTTERCAMP_AUTH_MODE=local); password-less localhost
  auto-login — M

- [ ] 007: Auth API endpoints — POST /v1/auth/login, POST /v1/auth/logout, POST /v1/auth/refresh,
  GET /v1/auth/me, POST /v1/api-keys, DELETE /v1/api-keys/:id; auth middleware (Bearer token +
  API key); RBAC role enforcement middleware — S

- [ ] 008: Audit event schema + service — `audit_event` DDL + repository; AuditEvent.Record()
  helper called throughout; delegation pattern (principal + delegated_by); unit tests for
  all principal_type values — S

- [ ] 009: Secret store — `secret` DDL + repository; AES-256-GCM encryption/decryption;
  nonce generation; master key loading (env var → key file → KMS stub); key_version tracking;
  `ref:<slug>` resolution helper; deletion safety check (scan known reference columns); CLI
  commands: secret set, secret list, secret delete — M

- [ ] 010: Model provider registry — `model_provider` DDL + repository; `provider_connection`
  DDL + repository; `model_profile` DDL + repository (including logical_profile_id stable
  identity, versioning, fallback_profile_id application-layer resolution); `model_profile_assignment`
  DDL + repository with scope hierarchy resolution (flow_node > agent > project > org);
  **FLAG: ISSUE #11 — bootstrap must create org-level assignment rows** — M

- [ ] 011: Skill registry schema + service — `skill` DDL + repository; org-scope vs
  project-scope partial unique indexes; file_path pointer convention; consistency check
  (scan skills/ dir vs registry); CLI hooks for create/update/delete skill — S

- [ ] 012: Bootstrap sequence — 10-step idempotent bootstrap (doc 14 authoritative): migrations,
  org, first human user, org skills repo + default skills, model profiles + org assignments,
  default flow templates, starter trio agents (Frank, Lori, Ellie), General session +
  participants, default org policy, bootstrap audit event; bootstrap CLI command;
  `POST /test/reset` test mode endpoint; integration test — M

---

## L2 — Domain Core (est. 12 task files)

Running total after L2: **24**

- [ ] 013: Agent schema — `agent` DDL (includes lifecycle_status combined column; tool allow/deny
  lists; memory_read_scopes; temp columns); `agent_profile_template` DDL + repositories;
  **FLAG: ISSUE #1 BLOCKER — budget_cap_cents vs budget_cap_tokens; implement with budget_cap_tokens
  pending spec resolution; ISSUE #5 — lifecycle_status cross-class constraint is app-layer only** — M

- [ ] 014: Agent service — staff lifecycle (draft → active → paused → retired/cancelled); temp
  lifecycle (active → expired/promoted); concurrent temp limit enforcement (organization.settings
  .agents.max_concurrent_temps); temp auto-retirement (event-driven: task_completed,
  session_closed, TTL scheduler); promotion workflow (Lori reviews → staff draft → human
  approval); archival summary on expiration — M

- [ ] 015: Agent API endpoints — GET /v1/agents, GET /v1/agents/:id, POST /v1/agents,
  PATCH /v1/agents/:id, POST /v1/agents/:id/pause, POST /v1/agents/:id/retire,
  GET /v1/agent-templates, POST /v1/agent-templates — S

- [ ] 016: Project schema — `project` DDL (delivery_mode, deploy_flow_template_id columns from
  doc 03a); `flow_template` DDL (immutability: is_current, start_node_id); `task_schedule` DDL;
  repositories for all three; project unique slug within org — S

- [ ] 017: Flow node schema — `flow_node` DDL with AUTHORITATIVE final schema: drop `skills jsonb`,
  keep `mcp_tools jsonb`, add `tool_domains jsonb`; self-references (next_node_id, reject_node_id);
  `flow_node_skill` join table DDL; repository layer;
  **FLAG: ISSUE #16 BLOCKER — coordinate with Sam before finalizing DDL** — S

- [ ] 018: Project service — project CRUD; flow template CRUD + immutability enforcement (new
  version = new row, old gets is_current=false); task_schedule CRUD + cron validation +
  overlap policy; project slug uniqueness — S

- [ ] 019: Project API endpoints — GET /v1/projects, POST /v1/projects, GET /v1/projects/:id,
  PATCH /v1/projects/:id; GET /v1/projects/:id/flow-templates, POST /v1/projects/:id/flow-templates;
  GET /v1/flow-templates/:id/nodes; GET /v1/projects/:id/schedules, POST, PATCH, DELETE — S

- [ ] 020: MCP connection schema + service — `mcp_connection` DDL + repository; `mcp_tool_catalog`
  DDL + repository (default-deny, is_enabled=false); `mcp_secret_binding` DDL + slug-reference
  resolution; catalog refresh on connection establishment / periodic health check; connection
  status lifecycle (configuring → active → degraded → failed) — M

- [ ] 021: MCP circuit breaker + health — circuit breaker per connection (closed/open/half-open);
  health check scheduler; three distinct timeouts (per-call, health-check, recovery); retry
  with backoff (write tools not retried by default); `mcp.catalog.changed` domain event;
  `mcp.discover` native utility tool — M

- [ ] 022: MCP API endpoints — GET /v1/mcp/connections, POST, GET/:id, PATCH/:id, DELETE/:id,
  POST/:id/refresh, POST/:id/test; GET/:id/catalog, PATCH/:id/catalog/:entry_id;
  GET/:id/executions — S

- [ ] 023: Token budget schema + service — `token_budget` DDL + repository; soft limit (warn once
  per period, activity feed entry) + hard limit (block non-essential capabilities, fail closed);
  anomaly detection background job (15-min interval, 3x 7-day average threshold);
  per-agent cap (`budget_cap_tokens`) enforced via hierarchical/additive model (✅ ISSUE #23 resolved) — S

- [ ] 024: Event bus + job queue — `domain_event` DDL (seq-based ordering, LISTEN/NOTIFY wake-up);
  `consumer_cursor` DDL + per-consumer position tracking; `job_queue` DDL (priority tiers,
  SKIP LOCKED, dead-letter); `idempotency_key` DDL (24-hour TTL, request_hash collision detection);
  job worker polling loop + LISTEN/NOTIFY wakeup; stale claim recovery; daily idempotency cleanup job — M

---

## L3 — Domain Features (est. 18 task files)

Running total after L3: **42**

- [ ] 025: Agent project assignment + skill attachment — `agent_project_assignment` DDL + repository
  (partial unique index for PM role); `agent_skill_attachment` DDL + repository (priority ordering);
  PM assignment enforcement (exactly one per project); service layer for assign/remove/deactivate — S

- [ ] 026: Agent API endpoints (assignments + skills) — POST /v1/agents/:id/project-assignments,
  DELETE /v1/agents/:id/project-assignments/:pid; GET /v1/agents/:id/skills,
  POST /v1/agents/:id/skills, DELETE /v1/agents/:id/skills/:sid — S

- [ ] 027: Project task schema — `project_task` DDL (work_status enum, requires_human_review,
  branch_name, task_number {slug}-{n}); `project_task_event` DDL; `inbox_item` DDL (all
  item_type values including browser_handoff); `merge_queue_entry` DDL (archived_at = on deploy,
  ISSUE #7 clarification); repositories for all; task_number auto-increment per project — M

- [ ] 028: Project task service — task creation (always via agent/chat, no UI path); work_status
  machine (draft → queued → in_progress → blocked/on_hold → review → done/cancelled);
  requires_human_review gate (PM scopes, human approves before queuing); task event recorder;
  inbox item creation; merge queue management (enqueue, archive on deploy);
  blocked dependency → auto-create resolution task (PM assigned) — M

- [ ] 029: Flow execution schema — `flow_node_execution` DDL (visit counter, session_id,
  commit_sha); `project_subtask` DDL (no review/on_hold states; sequential; flow_node_execution
  scoped); `project_task_dependency` DDL (DAG enforcement, same-level-only CHECK constraint);
  `project_task_participant` DDL; repositories for all — S

- [ ] 030: Flow execution service — flow advancement (agent signals "step done" via flow.advance
  tool; does NOT auto-advance on run completion); rejection loops (new flow_node_execution row,
  visit counter++); commit_sha recording on node completion; subtask CRUD; dependency DAG
  enforcement (cycle rejection at creation); cancelled dependency → resolution task — M

- [ ] 031: Delivery schema + service — `project_remote` DDL + repository; `project_environment`
  DDL + repository; delivery modes (continuous, gated, scheduled); Pattern 1 (delivery inside
  flow node) vs Pattern 2 (separate deploy task); auto-push retry (3x exponential backoff);
  rollback = new deploy task; PostgreSQL advisory lock for continuous mode race prevention — M

- [ ] 032: Task + project API endpoints — GET/POST /v1/projects/:id/tasks; GET/PATCH /v1/tasks/:id;
  POST /v1/tasks/:id/advance-flow; POST /v1/tasks/:id/review-decision; GET /v1/tasks/:id/flow;
  GET /v1/tasks/:id/subtasks; POST/DELETE /v1/tasks/:id/dependencies; GET /v1/tasks/:id/events;
  GET /v1/tasks/:id/participants; GET /v1/projects/:id/merge-queue; GET /v1/inbox;
  POST /v1/inbox/:id/act; GET /v1/projects/:id/remotes; GET /v1/projects/:id/environments — M

- [ ] 033: Capability policy schema + service — `capability_policy` DDL + repository (5 layers:
  instance/org/project/agent_profile/request-specific); policy evaluation engine
  (most-restrictive-wins, deny is absolute, silence passes); policy caching (org/project TTL,
  agent per-session, instance compiled at startup); budget gate integration;
  **FLAG: ISSUE #17 — instance policy rows write-protection not DB-enforced** — M

- [ ] 034: Capability policy API endpoints — GET /v1/control/policies, POST, PUT/:id, DELETE/:id;
  POST /v1/control/policies/evaluate (dry-run); unit tests: policy layer ordering, deny
  overrides allow, instance safety unoverridable (95% coverage) — S

- [ ] 035: Model gateway — concurrency manager (global + per-provider slots, sync reservation);
  priority queue (sync_interactive > sync_system > async_agent > async_system; FIFO within tier);
  soft preemption (pause most-recently-started async on sync arrival); queue timeouts by priority;
  connection selection (health-aware, failover_priority); health state machine
  (healthy → degraded → rate_limited → unavailable); retry + fallback logic;
  `model_invocation` DDL + repository — L

- [ ] 036: Model gateway — streaming, token tracking, rollups — streaming mode (sync=stream,
  async=no-stream); `model_usage_rollup` DDL + daily aggregation job; prompt/response capture
  to object storage (org redaction policy); prompt metadata (per-layer token counts, memory
  injection manifest); invocation_purpose routing to correct system profile; deterministic mode
  for tests — M

- [ ] 037: Model API endpoints — GET /v1/model/providers, PATCH /v1/model/providers/:id;
  GET /v1/model/profiles, POST, PATCH/:id; GET /v1/model/assignments, PUT /v1/model/assignments/:scope;
  GET /v1/usage?group_by=...&period=... — S

- [ ] 038: Memory schema — `memory` DDL (pgvector embedding vector(1536), scope columns,
  supersession chain, sensitivity, content_hash, file backing fields); `memory_taxonomy_tag`
  DDL; `memory_entity_mention` DDL; `memory_source` DDL (soft session_id reference — ISSUE #9);
  `memory_import` DDL; `memory_compaction_run` DDL; `memory_dedup_reviewed` DDL; repositories;
  pgvector index creation — M

- [ ] 039: Memory extraction pipeline — Stage 0 (deterministic garbage rejection, behavioral
  override rejection); Stage 1 (LLM extraction, Haiku-class); Stage 2 (scoring 0–100,
  threshold 40); Stage 3 (normalization: entity names, taxonomy assignment); Stage 4 (embed +
  dedup + store); trust tier capping; confidence vs utility distinction; candidate 7-day hold;
  hardening (active + is_hardened); extraction runs in isolated contexts (not agent's session) — L

- [ ] 040: Memory retrieval pipeline — 4-stage retrieval: scope filter (hard WHERE) →
  taxonomy classification (LLM or rule-based) → subtree retrieval → relevance ranking
  (vector cosine + recency + confidence); fallback to full-corpus on low confidence / <3 results;
  3 retrieval modes (passive injection, active @mention, agent-initiated query); injection ordering
  (most relevant last); sensitivity gating ('restricted' = no passive injection);
  entity synthesis pipeline; dedup (cosine ≥ 0.88 pre-screen + LLM cluster dedup); contradiction
  detection; supersession; file-backed memory freshness scan — L

- [ ] 041: Memory compaction + import — sleep-time reflection job (periodic, friction-signal
  triggered); task-completion consolidation run (scope promotion, episodic distillation, execution
  summary, entity synthesis); memory decay (episodic half-life, procedural decay); JSONL zip
  import pipeline (CLI + API: POST /v1/memory/import); `memory_import` status tracking;
  import provenance (source_type='import', trust_tier=0.6) — M

- [ ] 042: Memory API endpoints — POST /v1/memory/query; GET /v1/memory/items, GET/:id;
  GET /v1/memory/entities, GET/:id; GET /v1/memory/taxonomy; POST /v1/memory/import;
  GET /v1/memory/imports/:id; POST /v1/memory/consolidate; unit tests: 4-stage pipeline,
  trust tiers, dedup, passive injection dedup (95% coverage) — S

---

## L4 — Cross-Domain (est. 28 task files)

Running total after L4: **70**

- [ ] 043: Chat session schema — `chat_session` DDL (scope polymorphic, mode mutable, per-scope
  uniqueness rule); `chat_participant` DDL; `chat_turn` DDL (cycle_id, responding_type always 'agent');
  `chat_message` DDL (states: pending → streaming → final → failed | redacted; append-only after final;
  redacted zeroes content); `chat_artifact` DDL; `chat_summary` DDL; `chat_read_cursor` DDL;
  `chat_message_reaction` DDL (unique per participant per message); repositories for all — M

- [ ] 044: Chat service — session lifecycle (create, per-scope uniqueness enforcement,
  per-node async session auto-creation); participant management; message append; message state
  machine (pending → streaming → final); redaction (zeros content, preserves row); turn cycle
  orchestration (Phase 1 → 1.5 → 2 → 3); turn cancellation (cancel vs queue-new vs steer);
  queued message editing; multi-human shared queue — L

- [ ] 045: Chat progressive summarization + retention — threshold trigger (~50-60% of layer 6
  budget); summarize oldest ~25-30% unsummarized turns; immutable summary rows (from_sequence,
  to_sequence); preserve file paths/URLs/artifact IDs verbatim; session lifecycle cleanup
  (immediate close+extraction, deferred daily ephemeral purge, summary consolidation,
  tool result compaction) — M

- [ ] 046: Chat API endpoints — POST /v1/chat-sessions, GET /v1/chat-sessions, GET/:id;
  GET /v1/chat-sessions/:id/messages, POST (send message); POST /v1/chat-sessions/:id/cancel-turn;
  POST /v1/chat-sessions/:id/messages/:mid/steer; PATCH /v1/chat-sessions/:id/messages/:mid;
  POST /v1/chat-sessions/:id/messages/:mid/reactions; DELETE reactions/:rid;
  POST /v1/chat-sessions/:id/participants; GET /v1/chat-sessions/:id/read-cursor,
  PUT (update cursor); GET /v1/chat-sessions/:id/artifacts — S

- [ ] 047: SSE realtime + WebSocket — SSE endpoint GET /v1/events/stream?scopes=... with
  Last-Event-ID reconnect; seq-based event IDs; gap detection (purged events: X-Events-Gap header);
  WebSocket /v1/ws for bidirectional (typing indicators, live-pairing); LISTEN/NOTIFY subscription
  fan-out; API key scope enforcement on SSE; buffer overflow handling (1000 events, drop + client
  reconnect) — M

- [ ] 048: Turn execution engine — tier-1 vs tier-2 tool dispatch; listening eval (Haiku-class);
  stop conditions (max_tool_calls, max_duration — tokens NOT a stop); tier-1 parallel execution;
  tier-2 sequential execution; continuation turn (context compressed signal + query Ellie);
  reactions triggering memory confidence feedback — M

- [ ] 049: Tool resolution pipeline — `session_tool_set` DDL + repository; 4-stage resolution:
  (1) universe (native + enabled MCP in scope), (2) agent profile filter (allow/deny globs),
  (3) flow node soft deprioritize (tool_domains — ISSUE #25), (4) capability gate (tier 2 without
  capability excluded); cache for session lifetime; mid-session capability revocation at execution
  time; communication tool draft_action_review inbox items as designed behavior; unit tests:
  all 4 stages, glob matching, tier 1 bypasses capability gate (95% coverage) — M

- [ ] 050: Prompt assembly engine — 7-layer assembly: (1) agent identity [never cut],
  (2) policies+constraints [never cut], (3) scope context, (4) skills + MCP prompts
  (flow_node_skill priority, MCP prompts alongside, skills beat MCP on conflict), (5) memory
  injection (budget-aware, cooldown, attention-aware ordering — most relevant last), (6) conversation
  history (progressive summarization), (7) tool descriptions (~4-5k tokens, dropped first); per-layer
  token counts recorded in model_invocation.metadata; truncation + compression events logged;
  unit tests (95% coverage) — L

- [ ] 051: Control plane run schema — `run` DDL (status enum, optimistic concurrency version,
  idempotency_key dedup); `run_step` DDL (unique(run_id, step_number)); `run_attempt` DDL
  (unique(run_step_id, attempt_number), trigger enum, worker_type); `run_artifact` DDL;
  `run_event` DDL (heartbeat, unique(run_id, sequence)); repositories for all;
  cascade on delete behavior — M

- [ ] 052: Control plane run service — run creation (before policy eval; denied = immediate
  failure); run state machine (created → in_progress → paused/completed/failed/timed_out/
  cancelled/cancelling/dead_letter); retry envelope (new RunAttempt per retry, never overwrite;
  max retries by domain: MCP/external=3, internal=1, CLI/browser=2); heartbeat emission (every 30s);
  graceful cancellation (cancelling → worker signal → atomic unit → cancelled or forced); budget
  enforcement gate; idempotency dedup (24-hour window) — L

- [ ] 053: Supervisor — background process detecting stuck tasks (runs failed/timed_out/silent);
  orphaned runs (in_progress, no events for timeout + grace); stale blockers (24h normal, 4h urgent);
  recovery: (1) start new run, (2) file blocker, (3) escalate to PM; max 3 auto-recovery attempts
  per task per flow node; paused runs exempt from stuck detection (24-hour default timeout);
  heartbeat silence thresholds (90s sync, 5min async); dead_letter → project_task_event +
  PM notification; run_event.actor_type='supervisor' extension — M

- [ ] 054: Control plane API endpoints — POST /v1/control/runs, GET/:id, GET/:id/steps,
  GET/:id/events, GET/:id/artifacts; POST/:id/cancel, POST/:id/retry; GET /v1/control/runs
  (list with filters); GET /v1/control/cost/summary; GET /v1/control/health — S

- [ ] 055: tool_execution schema + service — `tool_execution` DDL (tool_tier always 'tier2',
  tool_domain enum, policy_decision, secrets redacted); `mcp_execution_log` DDL; broker dispatch
  pipeline: capability check → agent allow/deny → policy eval → allow/deny → worker dispatch;
  MCP tool invocation via control plane path; MCP resource read via basic scope check (ISSUE #15) — M

- [ ] 056: Native tool implementations (project/task domain) — implement all 25 project/task
  native tools: project.list/get/create/update, task.list/get/create/update/add_dependency/
  remove_dependency, subtask.list/get/create/update, flow.get_template/get_execution/advance/
  review_decision/create_template, inbox.list, merge_queue.status, schedule.list/create/update/delete;
  tier classification; capability requirements — M

- [ ] 057: Native tool implementations (chat + memory + agent + system) — implement 22 tools:
  session.list/get/history/create/invite_agent, message.send; memory.query (T1), memory.record (T2);
  agent.list/get/create_temp/update; file.read/list/search/write/delete, cli.execute,
  git.status/diff/log/commit; tier classification; capability requirements; git.commit requires
  system.file.write capability — M

- [ ] 058: CLI tool execution — `cli_execution` DDL + repository (risk_level enum, policy_decision,
  env_vars keys-only); constructed environment (always-set + project-config + org secrets; blocked
  keys: OPENAI_API_KEY, ANTHROPIC_API_KEY, OtterCamp internals); denylist enforcement; compound
  command decomposition (risk = max of parts); 50KB inline limit, 10MB total cap (excess → RunArtifact);
  cli.stdout/stderr/exit events mapped to run_event.output_chunk; working directory enforcement
  (path traversal rejection); process-level isolation; git operation rules (no push to main,
  no force-push to shared branches); ✅ ISSUE #26 resolved: `browser.evaluate` confirmed (doc 11 §Scripted Execution);
  ISSUE #19 (run_step_id NOT NULL constraint) remains open — M

- [ ] 059: Browser tool execution — `browser_session` DDL + repository (task-scoped, one active
  per task, persists across runs); `browser_action` DDL + repository (16 action types including
  `evaluate`; automatic screenshot after every nav/interaction/error → RunArtifact, NOT inline);
  `browser_handoff` DDL + repository (agent-initiated, inbox_item created, run paused, 24-hour
  expiry); domain policy enforcement (sensitive domains denied by default); credential injection at
  session creation time; idle timeout (1 hour); revocation (in-flight ops finish, next call denied);
  ✅ ISSUE #26 resolved: `browser.evaluate` included (JS execution, `system.browser.interact` required) — M

- [ ] 060: Flow node execution + session linking — `flow_node_execution` DDL integration with
  chat_session creation (per-node async session auto-created on node start); commit_sha recording
  on node completion; visit counter on rejection; session_id FK; subtask scoping to flow_node_execution;
  model_profile_assignment scope_type='flow_node' support in model gateway — S

- [ ] 061: Project task participant + dependency cross-domain — `project_task_participant` service
  (agent or human; roles: planner/worker/reviewer/observer); `project_subtask` service cross-domain
  (assignee polymorphic, sequential execution on shared task branch); dependency DAG cross-domain
  validation; delivery API endpoints: GET/POST /v1/projects/:id/remotes,
  GET /v1/projects/:id/environments — S

- [ ] 062: Model invocation attribution — `model_invocation` integration with control plane
  (run_id, run_step_id, run_attempt_id nullable FKs); token count denormalized rollups on
  run/run_step/run_attempt (asynchronous update); per-layer token counts in metadata; invocation
  purpose routing (agent_turn, listening_eval, summarization, skill_summarization, memory subtypes,
  replay); session + turn attribution; **FLAG: ISSUE #18 — whether agent turn-loop model calls
  receive run_attempt_id** — S

- [ ] 063: Observability + security layer — `trace_span` schema stub (implied, OTLP-compatible,
  partitioned by day, 7-day retention); Prometheus /metrics endpoint; /health/live + /health/ready
  endpoints (**FLAG: ISSUE #22 — canonical path names /health vs /health/live**); log scrubbing
  (5 invariants: never in prompts/logs/API responses/audit/memory); secret scrubbing layer
  (known patterns → [REDACTED]); inference context replay (prompt metadata per-layer token counts);
  retention enforcement daily job (chat 90d, run records 90d, model invocations 30d/90d ISSUE #21,
  domain events 90d, audit events 1yr, archived memories 1yr, trace spans 7d, artifacts 90d) — M

- [ ] 064: Delivery + merge queue execution — merge queue worker (job_type: merge_execute);
  auto-push worker (job_type: push_execute, 3 retries exponential backoff); deploy task creation
  worker (job_type: deploy_task_create, advisory lock for continuous mode); push_succeeded /
  push_failed task events; project_environment update on deploy; rollback flow (new deploy task
  targeting previous_commit_sha); merge_queue_entry archived_at on deploy completion — M

- [ ] 065: Scheduling engine — task_schedule service; job_type: schedule_tick; cron evaluation;
  overlap_policy enforcement (skip if existing task pending/in_progress); max_duration_ms timeout;
  system.schedule.fired + system.schedule.skipped domain events; schedule CRUD API endpoints — S

- [ ] 066: Push notification preference schema — `push_notification_preference` table or
  `human_user` extension DDL (per urgency tier, per project, quiet hours, per event type);
  push delivery consumer (APNs/FCM adapters); deep link URL scheme; notification payload builder
  (title, body, category, deep link, item_id); **FLAG: ISSUE #28 — table definition deferred in spec;
  implement with best judgment per doc 19 requirements** — S

- [ ] 067: API envelope + pagination + idempotency middleware — consistent {data, meta} envelope
  for all responses; {error, meta} for failures; cursor-based pagination (opaque cursors, default 50,
  max 200); Idempotency-Key header handling (24-hour TTL, request_hash collision → 409); /v1/ prefix
  enforcement; all state transitions via POST verb-noun (not PATCH to status); version/health
  endpoints; GET /v1/search (fuzzy, types: project/task/agent/session/flow_template); diff endpoint
  GET /v1/tasks/:id/diff — S

- [ ] 068: CLI client binary — `ottercamp` noun-verb command structure; three output modes
  (table/json/quiet); auth (--api-key, OTTERCAMP_API_KEY env, ~/.ottercamp/credentials);
  default server URL (localhost:4110); all ottercamp serve, migrate, bootstrap, reset-password,
  magic-link, unlock-account, backup, restore, version, health commands (doc 08 CLI catalog);
  TLS options (reverse proxy, ACME, manual) — M

- [ ] 069: Mobile/push API optimization — GET /v1/mobile/dashboard optional aggregation endpoint
  (inbox_count + project summaries + recent notifications); WebSocket preferred over SSE for
  mobile; delta sync (GET /v1/events/stream?last-event-id=N); biometric auth: client-side only,
  no server changes; multiple concurrent sessions (mobile + web + TUI) — S

- [ ] 070: Web UI static serving — React+TypeScript SPA served by API service (no separate server);
  static asset serving middleware; build pipeline integration (vite or equivalent); view state
  persistence contract (local storage only — no server-side view state); Cmd-K search endpoint
  GET /v1/search — S

---

## L5 — API + CLI + Tests (est. 20 task files)

Running total after L5: **90**

### Integration Tests

- [ ] 071: Auth integration tests — real PostgreSQL, login/session/API key flows, rate limiting,
  local auth mode, session expiry, delegation audit trail — S

- [ ] 072: Chat integration tests — session creation, message state machine, turn cycle, progressive
  summarization, tier 1/2 tool dispatch, listening eval, turn cancel, steer, reactions, read cursor — M

- [ ] 073: Project + task flow integration tests — task creation, flow advancement, rejection loops,
  visit counter, subtask sequencing, dependency DAG, merge queue, task events, inbox item creation — M

- [ ] 074: Agent lifecycle integration tests — staff lifecycle transitions, temp auto-retirement,
  concurrent temp limit, PM partial unique index, skill attachment, project assignment, promotion
  workflow — S

- [ ] 075: Memory pipeline integration tests — extraction pipeline stages, trust tier capping,
  candidate promotion, retrieval 4-stage pipeline, entity synthesis, dedup, contradiction detection,
  scope promotion on task completion, JSONL import — M

- [ ] 076: Model gateway integration tests — connection selection (health-aware), failover chain,
  concurrency slots, preemption, priority queue, retry logic, token tracking, rollup aggregation,
  prompt/response capture — M

- [ ] 077: Control plane integration tests — run creation, policy eval layers, retry envelopes,
  heartbeat + supervisor stuck detection, dead-letter, cancellation, budget gate, tool_execution
  dispatch, MCP circuit breaker — M

- [ ] 078: CLI + browser integration tests — risk classification, denylist enforcement, constructed
  environment, working directory scoping, output capture, browser session lifecycle, domain policy,
  handoff to inbox, run_artifact creation — S

- [ ] 079: Event bus + job queue integration tests — LISTEN/NOTIFY delivery, consumer cursor
  tracking, job priority + retry, dead-letter, idempotency key replay and collision detection,
  at-least-once delivery verification — S

- [ ] 080: Delivery + scheduling integration tests — auto-push retry, merge queue advisory lock,
  deploy task creation modes (continuous/gated/scheduled), schedule tick, overlap policy, rollback
  flow — S

### E2E Tests (Layer 3, full instance, OTTERCAMP_MODE=test)

- [ ] 081: Org bootstrap E2E — fresh install → bootstrap command → 10-step sequence verified;
  organization created, starter trio active, General session exists, default policy loaded,
  audit event recorded; idempotent second run (no-op); POST /test/reset produces clean state — S

- [ ] 082: Auth flow E2E — login → session → API key issuance; rate limiting (10 failed IPs,
  30-min account lockout); session expiry + refresh; API key scope enforcement on SSE; local
  auth mode auto-login — S

- [ ] 083: Chat lifecycle E2E — create session; send message; turn cycle (Phase 1 → 1.5 → 2 → 3);
  streaming SSE delivery; cancel turn; steer; progressive summarization trigger; reaction + memory
  confidence feedback; session mode switch (sync ↔ async) — M

- [ ] 084: Project + task flow E2E — create project via chat; PM creates task; flow advancement
  across nodes; rejection loop (visit counter++, new session per node); subtask sequencing;
  dependency enforcement; merge queue → merge → push → project_environment update;
  gated deploy via human chat trigger — M

- [ ] 085: Agent management E2E — create staff agent (draft → approval → active); create temp
  (project-scoped); temp auto-retirement on task completion; concurrent temp limit enforcement;
  temp promotion (Lori review → draft staff); starter trio profile update guard — M

- [ ] 086: Memory pipeline E2E — chat messages → extraction pipeline → memory candidate → active
  → passive injection on next turn; task completion → consolidation run → scope promotion;
  JSONL import via CLI; entity synthesis; contradiction detection + supersession; dedup run;
  memory.query tool usage from agent — M

- [ ] 087: Control plane E2E — agent tier 2 tool call → broker → policy eval → run created →
  worker dispatched → heartbeat → completion; policy deny (run status=failed immediately);
  retry (transient failure → new RunAttempt); stuck detection → supervisor recovery → escalation;
  budget hard limit enforcement — M

- [ ] 088: Full workflow E2E — end-to-end: human opens chat → instructs PM to create task →
  PM creates task → agent works task (CLI + MCP tools) → review flow node (human approves via inbox) →
  task done → merge → push → deploy; memory persists across sessions; all SSE events received;
  full audit trail present; token usage rollup updated — L

### CI Pipeline, Coverage, and Deployment

- [ ] 089: CI pipeline — lint (< 30s), unit tests (< 2min, 90% minimum / 95% critical paths),
  build (< 1min), integration tests (< 5min), E2E tests (< 10min), coverage gate (no merge if
  > 1% regression); total budget 15 minutes; coverage reporting; new .go files without _test.go
  flagged; test fixture management (testdata/responses/ committed) — S

- [ ] 090: Deployment docs + runbooks — self-hosted VPS deployment guide (Docker Compose);
  managed multi-tenant deployment (catalog database, db-per-org routing); backup/restore procedure
  (pg_dump + object storage tarball); TLS configuration options; environment variable reference;
  worker concurrency tuning guide; health endpoint monitoring integration — S

---

## Running Task Count Summary

| Layer | Tasks | Cumulative |
|-------|-------|-----------|
| L0 — Foundation | 4 | 4 |
| L1 — Core Identity | 8 | 12 |
| L2 — Domain Core | 12 | 24 |
| L3 — Domain Features | 18 | 42 |
| L4 — Cross-Domain | 28 | 70 |
| L5 — API + CLI + Tests | 20 | **90** |

**Total: 90 task files** (within 60–120 target range)

---

## Domain Task Coverage Matrix

Confirming all five coverage areas (schema / service / API / CLI / tests) are covered for each
domain. Small domains combine; every domain has all five somewhere.

| Domain | Schema | Service | API | CLI | Tests |
|--------|--------|---------|-----|-----|-------|
| Auth & Tenancy | 005, 008 | 006, 008 | 007 | 012, 068 | 071, 081, 082 |
| Secrets | 009 | 009 | — | 009, 068 | 071 |
| Agent | 013, 025 | 014, 025 | 015, 026 | 068 | 074, 085 |
| Project & Task | 016, 017, 027, 029, 031 | 018, 028, 030, 031, 065 | 019, 032 | 068 | 073, 080, 084, 088 |
| Chat | 043 | 044, 045, 048 | 046, 047 | 068 | 072, 083, 088 |
| Memory | 038 | 039, 040, 041 | 042 | 041, 068 | 075, 086, 088 |
| Model Gateway | 010, 036 | 035, 036, 062 | 037 | 068 | 076, 088 |
| Control Plane | 051 | 052, 053, 055 | 054 | 068 | 077, 087, 088 |
| MCP | 020 | 020, 021 | 022 | 068 | 077, 088 |
| Skills | 011, 017 | 011, 050 | 026, 032 | 012, 068 | 074 |
| CLI & Browser | 058, 059 | 058, 059 | — | 068 | 078, 087 |
| Events & Jobs | 024 | 024, 047 | 047, 067 | 068 | 079 |
| Observability | 063 | 063 | 063 | 068 | 089 |
| Delivery | 031, 061, 064 | 031, 064 | 032, 061 | 068 | 080, 084 |
| Tools | 049, 056, 057 | 049, 056, 057 | 067 | 068 | 072, 077, 083 |
| Mobile/Push | 066 | 066 | 066, 069 | — | 083 |

---

## E2E Test Scenarios — Confirmed Present

All 8 required E2E scenarios have dedicated task files in L5:

1. **Org bootstrap E2E** → task 081
2. **Auth flow E2E** → task 082
3. **Chat lifecycle E2E** → task 083
4. **Project + task flow E2E** → task 084
5. **Agent management E2E** → task 085
6. **Memory pipeline E2E** → task 086
7. **Control plane E2E** → task 087
8. **Full workflow E2E** → task 088

---

## Thin Spec Areas

Sections where the spec lacks sufficient detail and implementers will need to make judgment calls.
Each of these should be flagged when writing the corresponding task file.

- **Doc 08 (Deployment):** Managed mode catalog database connection routing is described at a
  high level but the routing layer implementation (how the API service selects the correct per-org
  database connection on request) is not specified. Implementers will need to design the connection
  pool keying strategy and the catalog database schema for the routing layer.

- **Doc 09 (MCP), ISSUE #15:** Resource read scope check ("does the agent have access to this
  connection's project?") is not specified. Implementers must decide whether it checks
  `agent_project_assignment` membership or simply whether the connection is org-scoped (available
  to all org agents) vs project-scoped (requires project assignment).

- **Doc 11 (CLI & Browser):** Constructed environment for CLI execution specifies "project-config
  injected vars" but does not list what project configuration variables are injected or how they
  are declared. Implementers will need to design the project configuration variable convention.

- **Doc 12 (API), ISSUE #27:** API path prefix inconsistency between doc 12 (/v1/) and doc 21
  test examples (/api/). Doc 12's /v1/ routes are authoritative. Implementers should treat doc 21
  examples as pseudocode only.

- **Doc 13 (Observability):** trace_span table is "implied, not DDL-defined." The exact schema
  (column names, OTLP attribute mapping, partition strategy) is left to implementers. Only
  retention (7 days) and append-only nature are specified.

- **Doc 14 + 15 (Bootstrap, Migration), ISSUE #24:** Starter trio profile update on OtterCamp
  version upgrade is only partially resolved. system_prompt is updated; operator_instructions
  is not overwritten. But tool policy updates, model assignment updates, and skill attachment
  updates on upgrade are not specified. Implementers must choose a safe no-op behavior for
  unspecified fields.

- **Doc 16 (Control Plane), ISSUE #17:** Instance-level capability_policy rows are "hardcoded,
  cannot be overridden" but no DDL or API constraint enforces write protection. Implementers must
  design the access control mechanism (e.g., API rejects writes to policy_layer='instance' for
  non-system actors, enforced in the handler, not the DB).

- **Doc 19 (Mobile), ISSUE #28:** Push notification preference table is completely unspecified.
  Implementers must design the schema. Recommended approach: add `push_preferences jsonb` to
  `human_user` (per-urgency enable/disable, per-project overrides, quiet hours start/end) and
  surface it via a new GET/PATCH /v1/me/push-preferences endpoint.

- **Doc 20 (Tools):** Communication tools (email.compose, slack.post) create `draft_action_review`
  inbox items as designed behavior — but the full inbox item lifecycle for draft actions (what
  "approve" does, what "reject" does, whether the original tool result is replayed) is not detailed.
  Implementers will need to define the approval action payload and the post-approval behavior.

- **Doc 21 (Testing):** Browser E2E tests require "headless Chrome in Docker" but no Docker
  setup, Playwright/Puppeteer dependency, or container orchestration approach is specified.
  Implementers must choose the browser automation framework and integrate it into the CI pipeline.
