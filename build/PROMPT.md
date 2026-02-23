# Ralph Loop: V2 Build Breakdown — Sprint 1 (Backend + API + CLI + Tests)

You are breaking down the OtterCamp V2 spec suite into 1-2 day implementation task files that a Claude Code session can pick up and build. You will read all 22 specs, derive a dependency-driven build order from the actual schemas and FK relationships, and produce 60-120 task files in `build/tasks/`.

## Sprint 1 Scope — CRITICAL

This sprint builds **everything except UI**. No TUI (doc 17), no Web UI (doc 18), no Mobile UI (doc 19). Those are Sprint 2.

What Sprint 1 delivers:
- **All database schemas and migrations** from every spec
- **All service layers** (business logic, domain rules)
- **Complete REST API surface** — every endpoint from doc 12, plus domain-specific endpoints
- **CLI tool** — a command-line interface that exercises every API capability (create orgs, manage agents, chat, create projects, run tasks, etc.)
- **SSE/realtime event streaming** — the server side, consumable by CLI and future UIs
- **Comprehensive tests** — unit tests for service layers, integration tests for API endpoints, and end-to-end tests that exercise full workflows through API and CLI
- **API documentation** — OpenAPI/Swagger spec or equivalent for every endpoint
- **Deployment packaging** — Docker Compose, bootstrap, seed data

**Read docs 17, 18, 19 for cross-references** (they mention API contracts, event subscriptions, data shapes the backend must support), but **do not create tasks for UI components, views, layouts, or frontend code.**

Every domain task should think in terms of: schema → service → API endpoints → CLI commands → tests. The end state is a fully functional system you operate entirely via `otter` CLI and `curl`.

---

## Ground Rules

1. **No task files until you have read every spec.** This is the #1 failure mode. Premature task creation leads to missed dependencies, wrong layer assignments, and rework. Phase 1 exists for a reason.
2. **Derive build order from FK analysis, not doc 14's phases.** Doc 14 is useful for intuition but its phase boundaries are product-facing, not implementation-facing. Your layers come from "what tables reference what other tables."
3. **No Large tasks.** Every task must be completable in 1-2 days. If a task feels bigger, split it immediately. Sizing: S = half day to one day, M = one to two days. If you catch yourself writing "L" — stop and split.
4. **Commit after every batch.** Each iteration that produces files must end with a git commit so the next iteration sees the latest state.
5. **Three state files are your working memory.** You persist context across iterations via `build/PROGRESS.md`, `build/MANIFEST.md`, and `build/DEPENDENCY-GRAPH.md`. Read them at the start of every iteration after Phase 1.
6. **Every domain gets tests.** Schema tasks get migration tests. Service tasks get unit tests. API tasks get integration tests. And there are dedicated E2E test tasks that exercise full workflows (create org → create agent → start chat → send message → get response → create project → create task → run flow → verify memory extraction).
7. **Every API endpoint gets a CLI command.** The CLI is not an afterthought — it's the primary interface for Sprint 1. If you can't do it from the CLI, it's not done.

---

## Spec Landscape

All specs live in `docsv2/`. Here is the current status:

| Doc | Name | Lines | Status | Sprint 1 |
|-----|------|-------|--------|----------|
| 01 | Architecture and Domain | 114 | In Process | Reference only |
| 02 | Chat | 1144 | **Finished** | Full build |
| 03 | Projects and Task Flow | 948 | **Finished** | Full build |
| 03a | Shipping and Delivery | 361 | **Finished** | Full build |
| 04 | Auth, Tenancy, and Identity | 631 | **Finished** | Full build |
| 05 | Agents, Staff, and Temps | 892 | **Finished** | Full build |
| 06 | Memory | 1135 | **Finished** | Full build |
| 07 | Models and Inference | 818 | Draft | Full build |
| 08 | Deployment and Self-Hosting | 960 | **Finished** | Full build |
| 09 | MCP Integration | 821 | Draft | Full build |
| 10 | Skills Integration | 556 | Draft | Full build |
| 11 | System Integration (CLI/Browser) | 748 | Draft | CLI yes, browser deferred |
| 12 | API, Events, and Realtime | 1112 | Draft | Full build |
| 13 | Security, Observability, Costs | 840 | Draft | Full build |
| 14 | Open Questions and Phasing | 791 | Draft | Reference only |
| 15 | Migration and Backward Compat | 381 | Draft | Full build |
| 16 | Agent Control Plane | 1201 | **Finished** | Full build |
| 17 | TUI | 824 | Draft | **Read only — no tasks** |
| 18 | Web UI | 895 | Draft | **Read only — no tasks** |
| 19 | Mobile UI | 470 | Draft | **Read only — no tasks** |
| 20 | Tools and Tool Policy | 613 | Draft | Full build |

**Finished specs** (02, 03, 03a, 04, 05, 06, 08, 16) have complete schemas with CREATE TABLE statements, resolved open questions, and full behavioral rules. These get thorough task coverage: 5-10 tasks each (schema + service + API + CLI + tests).

**Draft specs with full build** (07, 09, 10, 11, 12, 13, 15, 20) have schema sketches and behavioral descriptions but may have open questions. These get moderate coverage: 3-6 tasks each. Mark draft-sourced tasks with `spec-status: draft` so the implementer knows the spec may evolve.

**Doc 11 (System Integration):** Build CLI sandboxing and command execution. Defer browser automation to Sprint 2 (it's a UI-adjacent concern).

**Docs 17, 18, 19 (UI specs):** Read these to understand what API contracts the backend must support, but create zero tasks from them. Any API shape requirements discovered in UI specs should be noted in the relevant API/service task's implementer notes.

---

## Phase 1: Read All Specs (iterations 1-3)

**Goal:** Build a complete mental model of the system before creating any tasks.

### Iteration 1
Read docs 01, 02, 03, 03a, 04, 05, 14. These are the core domain + architecture + phasing docs. After reading, create `build/PROGRESS.md` with:
- For each doc read: title, status, table names found, FK relationships noted, key behavioral rules
- A running list of every table encountered so far
- Cross-references spotted (e.g., "chat_session.scope_id references project or project_task")
- API endpoints mentioned or implied

### Iteration 2
Read docs 06, 07, 08, 09, 10, 16. These are memory, models, deployment, MCP, skills, and control plane. Update `build/PROGRESS.md` with the same structure.

### Iteration 3
Read docs 11, 12, 13, 15, 17, 18, 19, 20. These are system integration, API, security, migration, and the three UI specs. Update `build/PROGRESS.md` to completion. **For docs 17-19, note only the API contracts and data shapes they require from the backend** — do not plan any UI tasks. At the end of this iteration, PROGRESS.md should list every table from every spec and every FK relationship.

**Phase 1 exit gate:** PROGRESS.md contains a complete table catalog with FK relationships from all 22 specs. Do not proceed to Phase 2 until this is true.

---

## Phase 2: Dependency Graph + Chunking Plan (iteration 4)

### Build the dependency graph

Create `build/DEPENDENCY-GRAPH.md` containing:

1. **Table catalog** — every table from every spec, grouped by domain:
   ```
   ## Auth & Tenancy (doc 04)
   - organization (no FKs — root entity)
   - human_user → organization
   - auth_session → human_user
   - api_key → organization, human_user|agent (polymorphic)
   - audit_event → organization
   ```

2. **Layer assignment** — assign each table to a build layer based on FK depth:
   - **L0 (Foundation):** Tables with no FKs or only self-references. Root entities. (organization, human_user, etc.)
   - **L1 (Core Identity):** Tables that reference only L0 tables. (agent, auth_session, etc.)
   - **L2 (Domain Core):** Tables that reference L0 + L1. (chat_session, project, etc.)
   - **L3 (Domain Features):** Tables that reference L0-L2. (chat_message, project_task, memory, etc.)
   - **L4 (Cross-Domain):** Tables that reference multiple domains. (flow_node_execution, tool_execution, etc.)
   - **L5 (API + CLI + E2E Tests):** No new tables — REST endpoints, CLI commands, integration/E2E tests, deployment packaging, API docs.

3. **Polymorphic FK notes** — identify every polymorphic pattern (scope_type + scope_id, principal_type + principal_id, etc.) and document which concrete tables each points to.

4. **Cross-domain touch points** — where domains interact (e.g., control plane reads agent policy, memory pipeline reads chat messages, etc.)

### Build the chunking plan

Create `build/CHUNKING-PLAN.md` with:
- Proposed task groups organized by layer and domain
- Estimated task count per group
- Running total (should land 60-120)
- Flag any areas where the spec is too thin to write good tasks
- **Ensure every domain has: schema tasks, service tasks, API tasks, CLI tasks, and test tasks**

**Phase 2 exit gate:** DEPENDENCY-GRAPH.md has every table assigned to a layer. CHUNKING-PLAN.md has a total in the 60-120 range.

---

## Phase 3: Write Task Files (iterations 5+)

Write 5-10 task files per iteration. Work through layers in order: L0 first, then L1, etc. Within a layer, group by domain.

### Task file format

Each task file goes in `build/tasks/NNN-short-slug.md` where NNN is a zero-padded sequence number (001, 002, ...).

```markdown
# NNN: Short Descriptive Title

| Field | Value |
|-------|-------|
| Layer | L0 / L1 / L2 / L3 / L4 / L5 |
| Size | S (≤1 day) or M (1-2 days) |
| Spec refs | doc 04 §Auth Session, doc 12 §REST Endpoints |
| Spec status | finished / draft / mixed |
| Depends on | NNN, NNN (task numbers this depends on) |
| Blocks | NNN, NNN (tasks that depend on this) |

## Scope

What this task builds. Be specific — name the tables, the API endpoints, the
CLI commands, the test files. An implementer should know exactly what to
create after reading this section.

### Must build
- (bullet list of concrete deliverables)

### Must NOT build
- (bullet list of things that are adjacent but belong to a different task)

## Acceptance Criteria

- [ ] (testable criteria — each one should be verifiable by running a test or checking a behavior)
- [ ] (aim for 3-8 criteria per task)
- [ ] (for API tasks: specific endpoints with methods, paths, request/response shapes)
- [ ] (for CLI tasks: specific commands with flags and expected output)
- [ ] (for test tasks: specific scenarios that must pass)

## Implementer Notes

Context the implementer needs: relevant schema excerpts, behavioral rules from
the spec, gotchas, polymorphic patterns to handle, etc. Reference spec sections
by doc number and section name so the implementer can look things up.
```

### Task naming conventions

- L0 tasks: `001-org-and-tenant-schema`, `002-human-user-auth`, etc.
- L1 tasks: `020-agent-identity-schema`, `021-model-provider-abstraction`, etc.
- L2 tasks: `040-chat-session-schema`, `041-project-schema`, etc.
- L3 tasks: `070-chat-message-service`, `071-task-flow-engine`, etc.
- L4 tasks: `100-control-plane-execution`, `101-memory-pipeline`, etc.
- L5 tasks: `120-auth-api-endpoints`, `121-auth-cli-commands`, `122-chat-api-endpoints`, etc.
- Number ranges by layer: L0=001-019, L1=020-039, L2=040-069, L3=070-099, L4=100-119, L5=120+
- Leave gaps for insertion — don't use every number

### Domain task pattern

For each major domain, expect this pattern of tasks:

1. **Schema + migrations** (L0-L3) — CREATE TABLE, indexes, constraints
2. **Service layer** (same layer or +1) — business logic, domain rules, validation
3. **API endpoints** (L5) — REST routes, request validation, response serialization
4. **CLI commands** (L5) — `otter <domain> <action>` commands that call the API
5. **Tests** (L5) — unit tests for service, integration tests for API, CLI smoke tests

Not every domain needs all 5 as separate tasks — small domains can combine service + API into one task. But every domain MUST have API endpoints, CLI commands, and tests by the end.

### After each batch

1. Update `build/MANIFEST.md` — a running index of all task files:
   ```
   | # | File | Title | Layer | Size | Depends | Status |
   |---|------|-------|-------|------|---------|--------|
   | 001 | 001-org-and-tenant-schema.md | Org and Tenant Schema | L0 | S | — | written |
   ```
2. Git commit with message: `build: add tasks NNN-NNN (Layer LX — domain description)`
3. Review what's left in the chunking plan and adjust if needed.

### Sizing calibration

- **Schema + migration task** for 1-3 related tables: S
- **Schema + migration task** for 4-6 related tables with complex constraints: M
- **Service layer** (CRUD, business logic) for a domain: M
- **API endpoints** for a domain (5-10 endpoints): S-M
- **CLI commands** for a domain (5-10 commands): S
- **Unit tests** for a service layer: S
- **Integration tests** for API endpoints of a domain: S-M
- **E2E test scenario** (multi-domain workflow): M
- **Integration plumbing** (event bus wiring, MCP adapter): S-M
- **Bootstrap/seed data**: S
- **API documentation** (OpenAPI spec for a domain): S

If a single schema has 6+ tables with complex inter-relationships, split into "core tables" and "extension tables" tasks.

### Required E2E test tasks

The following end-to-end test scenarios MUST each be a task (these go in L5):

1. **Org bootstrap E2E** — bootstrap fresh install, verify org + default agents + default policies + model profiles all created correctly
2. **Auth flow E2E** — register user, login, get session, API key auth, verify org isolation
3. **Chat lifecycle E2E** — create session, add participants, send messages, verify turn cycling, agent response, streaming
4. **Project + task flow E2E** — create project, create task with flow template, progress through flow nodes, verify state transitions, shipping/delivery
5. **Agent management E2E** — create staff agent, create temp agent with project scope, verify policy evaluation, lifecycle states
6. **Memory pipeline E2E** — chat produces messages, memory extraction runs, facts stored, retrieval returns relevant memories
7. **Control plane E2E** — trigger tool execution, verify policy check (allow/deny), audit trail, capability templates
8. **Full workflow E2E** — human creates org → creates project → staffs agents → starts chat → agent creates tasks → tasks flow through lifecycle → memory accumulates → verify everything via API and CLI

---

## Phase 4: Validate (final iteration)

Create `build/SUMMARY.md` with:

1. **Coverage check:** For every table in DEPENDENCY-GRAPH.md, which task creates it? Flag any uncovered tables.
2. **API coverage check:** For every domain, verify there are API endpoint tasks. List any domains missing API tasks.
3. **CLI coverage check:** For every API endpoint group, verify there's a corresponding CLI task. List gaps.
4. **Test coverage check:** For every domain, verify there are test tasks. List any domains missing tests.
5. **E2E check:** Verify all 8 required E2E test scenarios have tasks.
6. **Cycle check:** Walk the dependency graph — no task should have a circular dependency chain.
7. **Duplicate check:** No two tasks should create the same table or endpoint.
8. **Layer balance:** Roughly how many tasks per layer? If any layer has <5 or >30, investigate.
9. **Size distribution:** Count S vs M. If >20% are M, consider splitting some.
10. **UI exclusion check:** Verify zero tasks reference TUI components, React components, or mobile views. Any task that says "UI" should be about CLI output formatting, not graphical UI.
11. **Draft spec coverage:** For each draft spec, list the tasks derived from it and flag where spec gaps may cause implementation ambiguity.
12. **Stats:** Total tasks, tasks per layer, tasks per domain, finished-spec tasks vs draft-spec tasks.

After validation, signal completion:

<promise>BUILD BREAKDOWN COMPLETE</promise>

---

## State File Reference

| File | Created | Purpose |
|------|---------|---------|
| `build/PROGRESS.md` | Phase 1, iter 1 | Reading notes — tables, FKs, behavioral rules per spec |
| `build/DEPENDENCY-GRAPH.md` | Phase 2, iter 4 | Complete table catalog with layer assignments |
| `build/CHUNKING-PLAN.md` | Phase 2, iter 4 | Proposed task groups with counts |
| `build/MANIFEST.md` | Phase 3, first batch | Running index of all task files |
| `build/SUMMARY.md` | Phase 4 | Final validation report |
| `build/tasks/*.md` | Phase 3 | The actual task files (60-120 of them) |

---

## Failure Modes to Avoid

1. **Creating tasks before reading all specs.** You will miss cross-domain dependencies. Phase gates exist for this reason.
2. **Using doc 14 phases as layers.** Doc 14's phases are product milestones, not build dependencies. A table in "Phase 2" might have FKs to "Phase 3" tables. Use FK analysis.
3. **Tasks that are too vague.** "Build the chat system" is not a task. "Create chat_session, chat_participant, chat_turn tables with migrations and basic CRUD" is a task.
4. **Tasks that are too large.** If it takes more than 2 days, split it. The whole point is that each task fits in a single Claude Code session.
5. **Missing the polymorphic patterns.** OtterCamp uses scope_type+scope_id and principal_type+principal_id extensively. Every task that touches these must document which concrete types are valid.
6. **Forgetting non-schema work.** Not everything is a CREATE TABLE. Tasks also cover: service layers, API endpoints, CLI commands, tests, seed data, event bus wiring, MCP adapters, deployment configs, migration tooling, API documentation.
7. **Skipping the commit.** If you don't commit after each batch, the next iteration won't see your work. Always commit.
8. **Creating UI tasks.** This is Sprint 1. No TUI, no Web UI, no Mobile UI. If you catch yourself writing a task that mentions Bubble Tea, React, React Native, or any visual component — stop. That's Sprint 2.
9. **Schema without tests.** Every schema task should note that the corresponding test task will verify migrations run cleanly. Every service task should note test expectations. Every API task should define expected request/response shapes that integration tests will verify.
10. **API without CLI.** If there's an API endpoint, there must be a CLI command that calls it. The CLI is how we verify the system works before any UI exists.

---

## Iteration Pacing

| Iteration | Phase | What happens |
|-----------|-------|-------------|
| 1 | 1 | Read docs 01-05, 14. Create PROGRESS.md |
| 2 | 1 | Read docs 06-10, 16. Update PROGRESS.md |
| 3 | 1 | Read docs 11-13, 15, 17-20. Complete PROGRESS.md |
| 4 | 2 | Build DEPENDENCY-GRAPH.md + CHUNKING-PLAN.md |
| 5-6 | 3 | Write L0 + L1 tasks (foundation + core identity) |
| 7-9 | 3 | Write L2 + L3 tasks (domain core + features) |
| 10-12 | 3 | Write L4 tasks (cross-domain integration) |
| 13-16 | 3 | Write L5 tasks (API endpoints, CLI commands, tests, E2E, deployment) |
| 17+ | 3 | Remaining tasks, fill gaps, adjust dependencies |
| final | 4 | Validate, write SUMMARY.md, signal completion |

Expect 15-20 total iterations. Do not rush — thoroughness matters more than speed.
