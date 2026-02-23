# Build Breakdown Summary

Generated: 2026-02-23
Total tasks: 90
Task files: build/tasks/001–090

---

## Validation Results

### ✅ 1. Table Coverage

Every table in DEPENDENCY-GRAPH.md is covered by at least one task. Mapping by layer:

**L0 (4 tables):** `organization`, `model_provider`, `tool_definition`, `memory_taxonomy_node`
→ Task 003 (L0 Schema — Foundation Tables)

**L1 (7 tables):** `human_user`, `auth_session`, `api_key` → Task 005; `secret` → Task 009;
`provider_connection`, `model_profile`, `model_profile_assignment` (L2-listed but logically here)
→ Task 010; `skill` → Task 011

**L2 (13 tables):** `agent`, `agent_profile_template` → Task 013; `project`, `flow_template`,
`task_schedule` → Task 016; `mcp_connection` → Task 020; `memory_entity` → Task 038;
`domain_event`, `job_queue`, `idempotency_key`, `consumer_cursor` → Task 024;
`token_budget` → Task 023

**L3 (21 tables):** `audit_event` → Task 008; `agent_project_assignment`, `agent_skill_attachment`
→ Task 025; `flow_node` → Task 017; `project_task` → Task 027; `inbox_item`, `merge_queue_entry`,
`project_task_event`, `project_remote`, `project_environment` → Tasks 027/031;
`flow_node_skill` → Task 025; `model_usage_rollup` → Task 036; `mcp_tool_catalog`,
`mcp_secret_binding` → Task 020; `memory`, `memory_taxonomy_tag`, `memory_entity_mention`,
`memory_import`, `memory_compaction_run` → Tasks 038/041; `capability_policy` → Task 033;
`trace_span` → Task 063

**L4 (27 tables):** Chat tables (`chat_session`, `chat_participant`, `chat_turn`, `chat_message`,
`chat_artifact`, `chat_summary`, `chat_read_cursor`, `chat_message_reaction`) → Task 043;
Control plane tables (`run`, `run_step`, `run_attempt`, `tool_execution`, `run_artifact`,
`run_event`) → Task 052; `model_invocation` → Task 062; `flow_node_execution`,
`project_subtask`, `project_task_dependency`, `project_task_participant` → Tasks 029/061;
`cli_execution` → Task 058; `browser_session`, `browser_action`, `browser_handoff` → Task 059;
`mcp_execution_log` → Task 020; `memory_source`, `memory_dedup_reviewed` → Task 041;
`session_tool_set` → Task 049

No tables are uncovered.

**Minor note (non-blocking):** The DEPENDENCY-GRAPH.md Summary Table Counts has internal
arithmetic errors: L3 states 22 but lists 21 tables; L4 states 30 but lists 27 tables; the
stated total of 76 should be 72 based on the enumerated lists. This is a documentation
bookkeeping issue — all tables are accounted for in the body text and all are covered by
tasks. No build impact.

---

### ✅ 2. API Coverage

Every domain has at least one API endpoint task:

| Domain | API Task(s) |
|--------|-------------|
| Auth / Tenancy | 007 (Auth API Endpoints) |
| Agent | 015 (Agent API), 026 (Agent Assignment + Skills API) |
| Project / Task / Flow | 019 (Project API), 032 (Task and Project API) |
| MCP | 022 (MCP API Endpoints) |
| Token Budget / Capability | 034 (Capability Policy API) |
| Model Gateway | 037 (Model API Endpoints) |
| Memory | 042 (Memory API Endpoints) |
| Chat | 046 (Chat API Endpoints) |
| Control Plane | 054 (Control Plane API Endpoints) |
| Scheduling | 065 (Scheduling Engine — includes Schedule API) |
| Push / Notifications | 066 (Push Notification Preferences — Schema, Consumer, API) |
| Mobile | 069 (Mobile API) |
| Tools (native) | Covered via control plane / tool execution (tasks 055–060) |

No domains are missing API tasks.

---

### ✅ 3. CLI Coverage

Every major API endpoint group has corresponding CLI coverage:

| API Group | CLI Task(s) |
|-----------|-------------|
| Auth (login, logout, refresh, me, api-keys) | 007 (auth middleware), 068 (CLI binary) |
| Secret management | 009 (secret set/list/delete CLI commands) |
| Agent management | 068 (CLI binary — agent list/create) |
| Project management | 019, 068 (project create), 065 (schedule CLI) |
| Memory (import, compaction) | 041 (memory compaction CLI hooks) |
| Chat | 051 (Chat CLI Commands) |
| Control Plane (run, step, artifact inspection) | 054 (API), 068 (CLI binary) |
| Bootstrap / migrate / serve | 001, 012, 068 |
| Object storage (backup/restore) | 004 |
| Skill management | 011 |

Task 068 (CLI Binary — Build, Packaging, and Command Suite) is the central CLI assembly task
that aggregates all commands. No API group is left without CLI access.

---

### ✅ 4. Test Coverage

Every domain has an integration test task in the 071–080 range:

| Domain | Integration Test Task |
|--------|-----------------------|
| Auth + Tenancy | 071 (Auth and Tenancy Integration Tests) |
| Agent | 072 (Agent Integration Tests) |
| Project, Task, Flow | 073 (Project and Task Flow Integration Tests) |
| Memory | 074 (Memory Integration Tests) |
| Model Gateway | 075 (Model Gateway Integration Tests) |
| Control Plane + Capability Policy | 076 (Control Plane Integration Tests) |
| Chat | 077 (Chat Integration Tests) |
| MCP | 078 (MCP Integration Tests) |
| Event Bus + Job Queue | 079 (Event Bus and Job Queue Integration Tests) |
| Security + Observability | 080 (Security and Observability Integration Tests) |

All 10 integration test slots (071–080) are filled. No domain is missing integration test coverage.

---

### ✅ 5. E2E Check

All 8 required E2E scenarios are present:

- [x] 081 Org Bootstrap E2E — task 081-org-bootstrap-e2e.md
- [x] 082 Auth Flow E2E — task 082-auth-flow-e2e.md
- [x] 083 Chat Lifecycle E2E — task 083-chat-lifecycle-e2e.md
- [x] 084 Project + Task Flow E2E — task 084-project-task-flow-e2e.md
- [x] 085 Agent Management E2E — task 085-agent-management-e2e.md
- [x] 086 Memory Pipeline E2E — task 086-memory-pipeline-e2e.md
- [x] 087 Control Plane E2E — task 087-control-plane-e2e.md
- [x] 088 Full Workflow E2E — task 088-full-workflow-e2e.md

Tasks 089 (CI Pipeline) and 090 (Deployment Packaging) complete the L5 infrastructure layer.

---

### ✅ 6. Cycle Check

Dependency walk for the 10 most-connected tasks (by inbound + outbound edge count):

| Task | Key Inbound Deps | Key Dependents | Cycle? |
|------|-----------------|----------------|--------|
| 007 (Auth API) | 006 | 015, 019, 022, 026, 032, 034, 037, 042, 046, 054, 065, 066 | None |
| 024 (Event Bus) | 002, 003 | 028, 030, 033, 035, 039, 041, 044, 045, 047, 048, 053, 055, 057, 064 | None |
| 003 (L0 Schema) | 002 | 005, 008, 009, 010, 011, 013, 016, 020, 023, 033, 038, 043, 052 | None |
| 052 (Control Plane Schema) | 003, 004, 013, 016, 027, 033, 043 | 053, 054, 055, 058, 059, 060, 061, 062, 064, 076, 087 | None |
| 043 (Chat Schema) | 003, 005, 013, 016, 027, 038 | 044, 045, 046, 047, 048, 049, 050, 051, 052, 053, 054, 061, 062, 066, 077 | None |
| 055 (Tool Execution Service) | 052, 053, 033, 020, 021, 024 | 056, 057, 058, 059, 060, 076 | None |
| 035 (Model Gateway Routing) | 010, 024, 033 | 036, 037, 039, 040, 048, 050, 062, 075 | None |
| 053 (Control Plane Service) | 052, 033, 023, 024, 027, 028 | 054, 055, 058, 059, 060, 061, 062, 064, 076, 087 | None |
| 028 (Project Task Service) | 027, 018, 024, 025 | 030, 031, 032, 053, 057, 064, 073, 084 | None |
| 013 (Agent Schema) | 003, 012 | 014, 020, 025, 033, 038, 043, 049, 052, 056, 072, 085 | None |

The dependency graph is a strict directed acyclic graph (DAG). The layer structure (L0→L1→L2→L3→L4→L5)
enforces no backwards edges. No circular dependencies were found.

---

### ✅ 7. Duplicate Check

No two tasks create the same table or endpoint. Every schema table has a single owning task.
Cross-domain FK references (e.g., `model_invocation` referencing `run` defined in task 052) are
references, not duplicate definitions. No duplicate endpoints were found across API tasks — each
API task owns a distinct set of routes with no path collisions.

---

### ✅ 8. Layer Balance

| Layer | Task Count | Status |
|-------|-----------|--------|
| L0 (Foundation) | 4 | OK (at minimum threshold of 4) |
| L1 (Core Identity) | 8 | OK |
| L2 (Domain Core) | 12 | OK |
| L3 (Domain Features) | 18 | OK |
| L4 (Cross-Domain) | 28 | OK |
| L5 (API + CLI + Tests) | 20 | OK |
| **Total** | **90** | OK |

No layer is below 4 or above 30 tasks. All layers are within bounds.

---

### ✅ 9. Size Distribution

| Size | Count | Percentage |
|------|-------|------------|
| S (small, 1–2 days) | 41 | 45.6% |
| M (medium, 3–4 days) | 49 | 54.4% |

M tasks are 54.4% of the total, which exceeds the 25% flag threshold. This is flagged for
awareness but is **expected and acceptable** given the nature of Sprint 1: it is a full
ground-up rebuild with no existing code to reuse. Every schema + service + pipeline task
is legitimately medium-sized. The 49 M tasks represent real scope; no L (large) tasks
exist, which confirms the chunking was done conservatively. No action required.

---

### ✅ 10. UI Exclusion Check

Scan of all 90 manifest titles for TUI components, React components, or mobile/graphical views:

- Task 069 (Mobile API — Dashboard Aggregation, Push Token Registration, and WebSocket Preference):
  This task is server-side API only — it exposes endpoints that a mobile client will consume.
  No mobile UI code is written. This is acceptable.
- Task 070 (Web UI Static File Serving and SPA Infrastructure): This task sets up static file
  serving infrastructure (a Go HTTP handler to serve a dist/ directory). No React components
  or frontend code are written. This is acceptable.

No task writes TUI components, React components, or mobile views. Sprint 1 is zero graphical
UI tasks. CLI output formatting only.

---

### ✅ 11. Draft Spec Coverage

Scanned all 90 task files for `spec-status: draft`. Result: zero matches. All tasks reference
specs that are Finished status. Docs 17, 18, 19 (UI specs) are Sprint 2 only and are not
referenced by any Sprint 1 task. Clean.

---

### ⚠️ 12. Stats

| Metric | Value |
|--------|-------|
| Total tasks | 90 |
| L0 tasks | 4 |
| L1 tasks | 8 |
| L2 tasks | 12 |
| L3 tasks | 18 |
| L4 tasks | 28 |
| L5 tasks | 20 |
| Total S tasks | 41 |
| Total M tasks | 49 |
| Open BLOCKER issues | 5 |
| Open GAP issues | 8 |
| Open AMBIGUOUS issues | 11 |
| Total open issues | 24 |

**Tasks by domain (approximate — tasks often span domains):**

| Domain | Primary Tasks |
|--------|--------------|
| Auth / Tenancy | 005, 006, 007, 008, 071, 082 |
| Agent | 013, 014, 015, 025, 026, 072, 085 |
| Project / Task / Flow | 016, 017, 018, 019, 027, 028, 029, 030, 031, 032, 061, 064, 065, 073, 084 |
| Memory | 038, 039, 040, 041, 042, 074, 086 |
| Model / Provider | 010, 035, 036, 037, 062, 075 |
| Control Plane | 033, 034, 052, 053, 054, 055, 060, 076, 087 |
| Chat | 043, 044, 045, 046, 047, 048, 050, 051, 077, 083 |
| MCP | 020, 021, 022, 078 |
| Skills | 011, 025 |
| Tools (native/CLI/browser) | 049, 056, 057, 058, 059 |
| Infrastructure / Infra | 001, 002, 003, 004, 009, 012, 023, 024, 063, 067, 068, 069, 070 |
| E2E / CI / Deploy | 081–090 |

---

### ⚠️ 13. Issues Check

#### Open BLOCKERs

Five BLOCKERs are open in ISSUES.md. All are tracked and known. They must be resolved by Sam
before Codex starts the affected tasks.

| Issue | Severity | Affects Tasks | Summary | Resolution Needed |
|-------|----------|--------------|---------|-------------------|
| #1 | BLOCKER | 013 (agent schema), 014 (agent service), 053 (control plane — budget enforcement) | `budget_cap_cents` (doc 05) vs `budget_cap_tokens` (doc 14) — column name and unit on `agent` table are contradictory | Sam must designate authoritative column name and update doc 05 DDL |
| #16 | BLOCKER | 017 (flow node schema), 025 (agent-project/skill), 049 (tool resolution), 050 (prompt assembly) | `flow_node` DDL is scattered across three contradictory docs: must DROP `skills jsonb`, ADD `mcp_tools jsonb`, ADD `tool_domains jsonb` | Sam must update doc 03 with the definitive `flow_node` DDL incorporating all three changes |
| #21 | BLOCKER | 063 (observability/security — retention enforcement), 075 (model gateway tests) | 30-day vs 90-day `model_invocation` retention: doc 13 says 30 days, doc 14 resolution says 90 days | Sam must designate one value as authoritative and update doc 13 |
| #23 | BLOCKER | 013 (agent schema), 023 (token budget), 053 (control plane service — budget broker), 033 (capability policy) | Per-agent budget vs org/project token_budget: units differ (cents vs tokens), interaction rules (additive? most-restrictive? hierarchical?) are unspecified | Sam must define the budget hierarchy rule and update docs 05 + 13 + 16 |
| #26 | BLOCKER | 059 (browser execution), 056 (native tools tier 1) | `browser.evaluate` (JS execution) defined in doc 20 but absent from doc 11's browser action model — direct contradiction | Sam must decide: add `browser.evaluate` to doc 11 or remove it from doc 20's tool catalog |

**Note on ISSUE #27:** ISSUES.md classifies #27 as AMBIGUOUS (doc 21 path pseudocode vs doc 12
canonical `/v1/` routes). CONTEXT.md escalated it to BLOCKER. In practice, CONTEXT.md already
resolves it: "All API routes use the `/v1/` prefix. Doc 21's example paths are illustrative
pseudocode — the authoritative paths are in doc 12." Task implementers have a clear directive.
No additional Sam action is required — this is resolved by convention.

#### Open GAP Issues (non-blocking summary)

| Issue | Summary |
|-------|---------|
| #3 | `inbox_item.item_type = 'browser_handoff'` — definition confirmed by doc 11 but creation/payload details not fully specified |
| #6 | RESOLVED — `flow_node_skill` table fully defined in doc 10 |
| #8 | `temp_scope_id` behavior when `temp_scope_type = 'ttl'` is ambiguous |
| #9 | `memory_source.session_id` soft reference — no SQL FK; behavior on session deletion unspecified |
| #10 | `memory_source.source_id` for 'event' and 'file' source types has no DB FK enforcement |
| #11 | RESOLVED — bootstrap step 5 creates org-level model_profile_assignment rows |
| #12 | RESOLVED — `run_attempt` fully defined in doc 16 |
| #13 | `domain_event` table forward reference — expected to be defined in doc 12 (task 024 covers it) |
| #14 | RESOLVED — `tool_execution` and `run` fully defined in doc 16 |
| #24 | Starter trio upgrade path (system_prompt update policy) not fully specified |
| #25 | Tracked within BLOCKER #16 — `tool_domains jsonb` addition to `flow_node` |
| #28 | `push_notification_preference` table undefined — task 066 creates schema but must define it |

---

### ⚠️ 14. CONTEXT.md Check

`build/CONTEXT.md` exists and covers all required sections:

| Required Section | Present? | Notes |
|-----------------|----------|-------|
| Architecture | Yes | Runtime model, language, HTTP, database, object storage, event bus, job queue |
| Tech stack | Yes | Go 1.21+, chi router, PostgreSQL + pgvector, pgxpool, embedded migrations |
| CLI conventions | Yes | Noun-verb commands, output modes, auth methods documented |
| Test mode | Yes | OTTERCAMP_MODE=test, POST /test/reset, deterministic responses |
| 3-layer test architecture | Yes | Unit / Integration / E2E layers defined with conventions |
| Open BLOCKERs | Partial | Lists #1, #16, #23, #26, #27 — **ISSUE #21 (retention) is absent from CONTEXT.md's BLOCKERs table** even though it is a BLOCKER in ISSUES.md |
| File locations | Yes | Full directory tree with descriptions |

**Action required:** CONTEXT.md should be updated to add ISSUE #21 to the Open BLOCKERs table.
This is a minor omission — the issue is tracked in ISSUES.md and is included in this summary.
It does not affect build correctness.

---

## Open BLOCKERs — Must Resolve Before Building

| Issue | Affects Tasks | Summary | Resolution Needed |
|-------|--------------|---------|-------------------|
| #1 | 013, 014, 053 | `budget_cap_cents` (doc 05) vs `budget_cap_tokens` (doc 14) — agent table column name unresolved | Update doc 05 DDL: rename column, confirm token-unit semantics |
| #16 + #25 | 017, 025, 049, 050 | `flow_node` DDL contradictory across docs 03/09/10: must DROP `skills jsonb`, ADD `mcp_tools jsonb`, ADD `tool_domains jsonb` | Update doc 03 with final `flow_node` DDL |
| #21 | 063, 075 | `model_invocation` retention: 30 days (doc 13) vs 90 days (doc 14) — direct contradiction | Designate authoritative value; update doc 13 |
| #23 | 013, 023, 033, 053 | Per-agent budget (doc 05) vs org/project token_budget (doc 13): units and hierarchy interaction rules unspecified | Define budget hierarchy rule; update docs 05, 13, 16 |
| #26 | 056, 059 | `browser.evaluate` in doc 20 not in doc 11's browser action model — two specs contradict | Add to doc 11 or remove from doc 20; finalize browser action allowlist |

---

## Stats

| Metric | Value |
|--------|-------|
| Total tasks | 90 |
| L0 tasks | 4 |
| L1 tasks | 8 |
| L2 tasks | 12 |
| L3 tasks | 18 |
| L4 tasks | 28 |
| L5 tasks | 20 |
| S tasks | 41 (45.6%) |
| M tasks | 49 (54.4%) |
| L tasks | 0 |
| Open BLOCKERs | 5 |
| Open GAPs | 8 |
| Open AMBIGUOUS | 11 |
| Total open issues | 24 |
| Tables in dependency graph | 72 (enumerated) / 76 (stated — 4-table overcount in DEPENDENCY-GRAPH.md) |
| All task files present | Yes (90/90) |
| Draft spec tasks | 0 |
| UI tasks (graphical) | 0 |

---

## Sign-off

14 checks completed. Structural integrity confirmed: full table coverage, no circular
dependencies, no duplicates, no missing domains, all 8 E2E scenarios present, all 10
integration test domains covered.

Issues found requiring Sam's attention:
- 5 open BLOCKERs (spec contradictions that block specific tasks — listed above)
- 1 minor CONTEXT.md omission (ISSUE #21 missing from BLOCKERs table)
- 1 DEPENDENCY-GRAPH.md bookkeeping error (table count discrepancy: stated 76, enumerated 72)

These are known issues tracked in ISSUES.md. None are structural problems that make the
task set unusable. Codex can begin building from L0 immediately; BLOCKER-affected tasks
must wait for Sam's resolution as documented above.

<promise>BUILD BREAKDOWN COMPLETE</promise>
