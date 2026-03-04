# OtterCamp V2 — Build Instructions for Codex

This document is your entry point. Read it completely before touching any task file.

---

## What You Are Building

OtterCamp V2 is a chat-primary agent orchestration platform written in Go (1.21+). A human
operator manages projects, tasks, and AI agents through conversation. The system includes
a REST API server, a background worker process, a CLI binary, PostgreSQL (with pgvector) as
the primary datastore, and a PostgreSQL-backed event bus and job queue.

This is a clean-room rebuild. No V1 code, schema, or data is reused.

---

## Repository Layout

```
docsv2/                     ← Spec documents — read-only during build
build/
  INSTRUCTIONS.md           ← This file
  CONTEXT.md                ← Architecture conventions — read before every task
  DEPENDENCY-GRAPH.md       ← Complete table catalog, layer assignments, polymorphic patterns
  CHUNKING-PLAN.md          ← All 90 tasks with layer/size/domain groupings
  ISSUES.md                 ← Spec issues — all 28 are RESOLVED; no blockers remain
  tasks/                    ← 90 task files (001–090)
```

The `docsv2/` spec documents are the authoritative source of truth. Task files distill and
synthesize the spec into build-ready instructions. When a task file and a spec doc appear to
conflict, the task file wins — it reflects resolved decisions.

---

## Before You Start Any Task

**Read these two files first, in this order:**

1. `build/CONTEXT.md` — Architecture, tech stack, test structure, key invariants. This file
   is your standing context for every task in the project.

2. The task file itself (`build/tasks/NNN-name.md`) — Scope, must-build list, acceptance
   criteria, tests required, implementer notes.

**Do not** read the full spec documents unless a task file explicitly tells you to consult
a specific section for detail not reproduced in the task file. The task files are complete
enough to implement from.

## Command Discovery Guardrails (Autowork)

When investigating code paths during task execution, use a required `discover -> open` pattern:

1. **Discover path first** with `rg --files <root> | rg '<fragment>'` (or `find` if `rg` is unavailable).
2. **Verify existence** with `test -f <path>` / `ls <path>`.
3. **Open file** with `sed/cat` only after the path is verified.

If a path lookup fails, classify it as a recoverable `lookup_miss` and continue discovery.
Do not treat lookup misses as task-failing regressions. Keep failure summaries split between:
- `lookup_miss` (path/discovery misses)
- `build_or_test_failure` (actual implementation regressions)

## Startup Context Caching (Autowork)

Autowork runners may cache startup context for stable docs using file hashes:

- Cache target docs: `issues/instructions.md`, `build/INSTRUCTIONS.md`, `build/CONTEXT.md`
  (and reviewer equivalents for review runs).
- On cache hit: runners use cached briefing text and skip full doc rereads.
- On cache miss: runners re-read full docs and refresh cache metadata.
- Runner logs must explicitly show cache status (`hit`/`miss`) and changed-doc identifiers.

---

## How Tasks Are Organized

Tasks are numbered 001–090 and grouped into six build layers:

| Layer | Tasks | Description |
|-------|-------|-------------|
| L0 | 001–004 | Foundation: scaffold, database infrastructure, L0 tables, object storage |
| L1 | 005–012 | Core identity: auth, audit, secrets, model providers, skills, bootstrap |
| L2 | 013–024 | Domain core: agents, projects, MCP, budgets, event bus, job queue |
| L3 | 025–042 | Domain features: assignments, task flow, delivery, policies, memory |
| L4 | 043–070 | Cross-domain: chat, control plane, tools, CLI, browser, observability |
| L5 | 071–090 | Integration tests, E2E tests, CI pipeline, deployment docs |

Every task file has a `Depends on` and `Blocks` header listing task numbers. **Do not start
a task until all its dependencies are complete.**

Work through layers in order: L0 → L1 → L2 → L3 → L4 → L5. Within a layer, tasks can be
worked in parallel if their specific dependencies are satisfied.

---

## The Task File Format

Every task file has:

```
# NNN: Task Name
| Depends on | task numbers |
| Blocks     | task numbers |

## Scope
What to build. Exact API signatures, DDL, struct definitions, interface contracts.

## Must NOT build
Explicit scope exclusions. If something is listed here, leave it for the listed task.

## Acceptance Criteria
Checkbox list. Each box must be checkable before the task is done.

## Tests Required
Unit, integration, and E2E tests. All three layers must be satisfied.

## Implementer Notes
Resolved decisions, gotchas, reference pointers.
```

Build exactly what the task file specifies. Do not add features, refactor adjacent code, or
expand scope. If you find a genuine gap that would block the task, document it as a comment
in the code and implement a safe stub, then continue.

---

## Testing Requirements

Every task has three test layers. All are required:

**Unit tests**
- Test packages in isolation using mocks for external boundaries (model providers, MCP servers).
- Target: 95% coverage on critical paths (policy evaluation, prompt assembly, tool resolution,
  flow progression, event emission). 90% minimum everywhere else.
- File: `*_test.go` in the same package as the code.

**Integration tests**
- Run against a real PostgreSQL database via `testdb.New(t)` (defined in task 002).
- Build tag: `//go:build integration`
- Test actual SQL queries, FK constraints, migration correctness, event bus delivery.
- No mocked database — only mock external HTTP services.

**E2E tests**
- Defined only in tasks 081–088. Tasks 001–080 do not write E2E tests.
- E2E tests spin up a full OtterCamp instance with `OTTERCAMP_MODE=test`.
- Call `POST /test/reset` before each scenario.
- Exercise the system exclusively through the CLI binary and REST API.
- Build tag: `//go:build e2e`
- Must complete in < 10 minutes total across all E2E tasks.

---

## Code Conventions

**Language:** Go 1.21+. Use the standard library where possible.

**Logging:** `log/slog` for structured logging. Never use `fmt.Printf` in application code.

**HTTP router:** `chi` (or stdlib mux stub until chi is added in task 001).

**All API routes** use the `/v1/` prefix. Health endpoints (`/health/live`, `/health/ready`,
`/health`, `/ready`) are the only exception — they are not under `/v1/`.

**Error handling:** Return errors, do not panic in application code paths. The event bus consumer
goroutines have a panic recovery wrapper (task 024) — that's the only sanctioned use.

**Context:** Every function that does I/O takes `context.Context` as its first argument.

**Token and key hashing:** SHA-256 hex-encoded (64 chars). Never store raw tokens or keys.

**Password hashing:** bcrypt with work factor 12.

**UUIDs:** `github.com/google/uuid`. Use `gen_random_uuid()` in DDL.

**Migrations:** Numbered, forward-only, transactional. Each file is a single atomic DDL block.
The `schema_migrations` table tracks applied migrations. Auto-run on startup.

**Polymorphic patterns:** `(scope_type, scope_id)`, `(principal_type, principal_id)`,
`(created_by_type, created_by_id)` appear throughout. See `DEPENDENCY-GRAPH.md §Polymorphic
FK Catalog` for the complete list of patterns and their valid concrete types.

**System actor sentinel UUID:** `00000000-0000-0000-0000-000000000000`. Used when
`actor_type='system'` or `principal_type='system'` in lieu of a real entity ID.

---

## Critical Invariants — Never Violate These

1. **Secrets never appear in prompts, logs, API responses, audit events, or memory.**
   The log scrubbing layer (task 063) enforces this, but code must not create the exposure
   in the first place.

2. **Policy deny is absolute.** When the capability policy engine returns `deny`, the action
   does not execute. No bypass path exists. Instance safety rules (compiled at startup) cannot
   be overridden by any lower layer.

3. **Silence passes.** No policy row matching a capability = allow. Do not implement a
   default-deny posture unless the task file explicitly says to.

4. **Tokens and keys are stored as SHA-256 hashes.** The raw value is shown once at creation
   and never stored. This applies to `auth_session.token_hash`, `api_key.key_hash`, and
   `mcp_connection` credential fields.

5. **Audit events are append-only.** No UPDATE or DELETE on `audit_event` rows.

6. **Flow templates are immutable once a task starts.** Template edits create a new version
   row (`is_current=true`). The old row's `is_current` is set to false. Running tasks are
   never affected by template changes.

7. **Run attempts are never overwritten.** A retry creates a new `run_attempt` row
   (`attempt_number++`). The previous attempt row is preserved unchanged.

8. **`POST /test/reset` is only registered when `OTTERCAMP_MODE=test`.** In production mode
   this endpoint must return 404, not 403.

9. **Temp agents are project-scoped only.** `temp_project_id uuid NOT NULL references project(id)`.
   There is no `temp_scope_type` or `temp_scope_id`. Task completion and session close do NOT
   auto-retire temps — only the TTL scheduler or explicit retirement via PM/Lori.

10. **`supervisor` is excluded from `audit_event`.** Supervisor actions are tracked via
    `run_event` (actor_type='supervisor') and `domain_event` only. The `audit_event` table's
    `principal_type` check retains only ('human', 'agent', 'system').

---

## Key Resolved Decisions (All ISSUES Now Closed)

`build/ISSUES.md` contained 28 issues. **All 28 are now RESOLVED.** There are no open blockers.
The task files already reflect all resolutions. Key resolved decisions that affect many tasks:

- **Budget caps use tokens, not cents.** Column is `budget_cap_tokens` (int), not
  `budget_cap_cents`. Budget enforcement is hierarchical/additive: a single invocation is
  charged to agent + project + org simultaneously; any exceeded hard limit blocks it.

- **Bootstrap is 10 steps, not 7.** Doc 14 is authoritative. The 10-step sequence in task 012
  is correct. Ignore doc 04's 7-step sequence — it's stale (doc 04 now defers to doc 14).

- **Private memory defaults to false for all agents.** All agents — staff, Frank, Lori, Ellie,
  PMs, temps — have `private_memory_enabled=false` by default. It is an explicit opt-in for
  agents handling sensitive personal data.

- **`/v1/` prefix is authoritative for all API routes.** Doc 21's example paths (`/api/sessions/`)
  are pseudocode. Use `/v1/chat-sessions/`, `/v1/projects`, etc.

- **Starter trio upgrade path:** On startup, if binary version ≠ version in step 10 audit event,
  update `system_prompt` for Frank/Lori/Ellie from shipped defaults. Never overwrite
  `operator_instructions`, tool policy, model assignments, or skill attachments. The CLI command
  `ottercamp agent sync-defaults` lets operators manually apply other default updates.

- **Push preferences:** Stored in `human_user.settings` jsonb under keys `push_preferences` and
  `push_tokens`. No separate `push_notification_preference` table. No migration 0077.

- **Health endpoint canonical paths:**
  - `GET /health/live` (liveness) — also aliased as `GET /health`
  - `GET /health/ready` (readiness) — also aliased as `GET /ready`
  - NOT under `/v1/`

- **`flow_node` final DDL:** No `skills jsonb`. Has `mcp_tools jsonb` and `tool_domains jsonb`.
  Skills are via the `flow_node_skill` join table (task 017).

- **Model invocation retention:** 90 days (not 30). Task 063 implements this.

- **`run_attempt_id` on `model_invocation`:** NULL for standalone chat turn-loop calls. Set only
  for model calls within a control plane run_attempt. Token aggregation uses `WHERE run_id = $1`.

- **`browser.evaluate` is a valid browser action** (16 total actions, not 15). Requires
  `system.browser.interact` capability.

- **Instance policy write protection:** API-layer only. `POST /v1/control/policies` with
  `policy_layer='instance'` returns 403 (unless in bootstrap mode). No DB constraint.

---

## Note on CHUNKING-PLAN.md

`build/CHUNKING-PLAN.md` was generated before all issues were resolved. It contains stale
references to open issues (e.g., "FLAG: ISSUE #28", "FLAG: ISSUE #22"). Ignore these flags —
all issues are resolved and the task files are up to date. Use CHUNKING-PLAN.md only for the
layer assignments and task numbering; trust the individual task files for the authoritative
scope.

---

## Development Environment Setup

**Requirements:**
- Go 1.21+
- PostgreSQL 14+ with the `pgvector` extension
- Docker (for running the test database in CI and for E2E tests)

**Environment variables (minimum to run):**
```bash
OTTERCAMP_DATABASE_URL=postgres://localhost/ottercamp_dev
OTTERCAMP_MASTER_KEY=<32-byte hex string for secret encryption>
```

**To run in test mode:**
```bash
OTTERCAMP_MODE=test ottercamp serve
```

**Running tests:**
```bash
# Unit tests only
go test ./...

# Integration tests (requires PostgreSQL)
go test ./... -tags integration

# E2E tests (requires full server)
go test ./... -tags e2e
```

**Autowork test gating policy:**
- Execute scoped task tests first (touched packages only); these are the primary blocking signal.
- Use full-suite runs (`go test ./...`, `... -tags integration`) only after scoped tests and classify them as baseline checks.
- Baseline failures outside touched scope are non-blocking when clearly pre-existing/unrelated.
- Always report test outcomes in three buckets:
  - `task_scope` (blocking)
  - `baseline_unrelated` (non-blocking if unrelated)
  - final `decision` (`proceed` or `blocked`)

**tmux verification checklist (TUI work):**
- Run the TUI in tmux and confirm fallback-first paths remain usable:
  - `:focus sidebar|main|chat`
  - `:frank` (and key paths `Ctrl-G`, `0` outside chat input)
  - `:dashboard`, `:inbox`, and other explicit nav commands
  - chat commands `:send`, `:cancel-turn`, `:queue edit|steer|delete`
- Resize tmux panes through XS/S/M/L/XL widths and confirm focus/layout stay deterministic.
- Capture at least one tmux-mode command-palette workflow in integration/E2E validation before review.

**CI budget (task 089):**
- Lint: < 30s
- Unit tests: < 2 min
- Build: < 1 min
- Integration tests: < 5 min
- E2E tests: < 10 min
- Total: < 15 min

Coverage gate: 90% minimum everywhere; 95% on critical paths. PRs that regress coverage by
more than 1% do not merge.

## Shell Safety Guardrails (Autowork)

When constructing shell commands that include markdown or multi-line text:

- Do **not** inline markdown payloads in quoted one-liners (for example `--body "..."`).
- For PR descriptions, always write markdown to a file first and use `--body-file`.
- For notes/changelog append commands, use a single-quoted heredoc delimiter:

```bash
cat <<'EOF' >> issues/notes.md
<literal markdown>
EOF
```

This prevents command substitution and quote-break failures caused by backticks and `$` expansion.

---

## How to Pick Up a Task

1. Identify the lowest-numbered task whose `Depends on` tasks are all complete.
2. Read `build/CONTEXT.md` if you haven't in this session.
3. Read the task file (`build/tasks/NNN-name.md`) completely.
4. Read only the specific spec sections the task file references (e.g., "doc 14 §Bootstrap
   Dataset") — find them in `docsv2/`.
5. Implement exactly what the task file specifies, nothing more.
6. Write tests at all required layers before marking the task complete.
7. Verify every acceptance criteria checkbox.

---

## Where to Find Specific Decisions

| Question | Where to look |
|----------|--------------|
| Table DDL and FKs | Task file for the table's layer, then `docsv2/` for detail |
| Layer assignment for any table | `build/DEPENDENCY-GRAPH.md` §Layer sections |
| Polymorphic pattern concrete types | `build/DEPENDENCY-GRAPH.md` §Polymorphic FK Catalog |
| Cross-domain FK edges | `build/DEPENDENCY-GRAPH.md` §Cross-Domain Touch Points |
| Which task builds what | `build/CHUNKING-PLAN.md` §Domain Task Coverage Matrix |
| Resolved spec conflicts | `build/ISSUES.md` (all RESOLVED — read for decisions, not for open items) |
| API route paths | `docsv2/12-api-events-and-realtime.md` (authoritative) |
| Bootstrap sequence | `build/tasks/012-bootstrap.md` + `docsv2/14-build-phases.md §Bootstrap Dataset` |
| Test architecture | `build/CONTEXT.md` §Three-Layer Test Architecture |
| CLI command catalog | `docsv2/08-deployment-and-self-hosting.md §CLI` |
