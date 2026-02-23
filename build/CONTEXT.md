# OtterCamp V2 — Build Context

This file is included alongside every task file when Codex picks up a phase to build.
Read it first. It describes what you are building, how the codebase is structured, and
the conventions every task must follow.

---

## What Is OtterCamp?

OtterCamp V2 is a **chat-primary agent orchestration platform** for a single human operator
who manages projects, tasks, and AI agents through conversation. The human directs all work
through chat — agents create tasks, run flows, make tool calls, and accumulate memory. The
operator never touches a task board directly; they talk to agents who do.

This is a **clean-room rebuild**. No V1 runtime code, schema, or data is reused.

---

## Architecture

**Runtime model:** Modular monolith — one codebase, two processes:
- `ottercamp serve` — HTTP API server (REST + SSE/WebSocket)
- `ottercamp worker` — Background job processor (scheduled jobs, async pipelines)

**Language:** Go (1.21+). Use stdlib where possible. `log/slog` for structured logging.
No `fmt.Printf` in application code paths.

**HTTP:** `chi` router (or stdlib mux stub until chi is added). All routes under `/v1/`.

**Database:** PostgreSQL with `pgvector` extension. One database per organization
(`db-per-org` isolation). A catalog database holds org routing metadata. Connection
pooling via `pgxpool`. Migrations are embedded in the binary, forward-only, numbered,
transactional per migration, auto-run on startup. The `schema_migrations` table tracks
applied migrations.

**Object storage:** Abstracted behind a `Store` interface. Local filesystem adapter
for single-node deployments, S3-compatible adapter for cloud. Artifacts, run outputs,
and backups go through this interface.

**Job queue:** PostgreSQL-backed (`job_queue` table) with `SKIP LOCKED`, priority tiers,
retry with backoff, and dead-letter handling. No external queue dependency.

**Event bus:** PostgreSQL `LISTEN/NOTIFY` for internal domain events. The `domain_event`
table provides durability and replay. Defined in doc 12.

---

## CLI

The primary interface for Sprint 1. The `ottercamp` binary uses noun-verb commands:

```
ottercamp serve
ottercamp bootstrap
ottercamp migrate
ottercamp org create --name "Acme"
ottercamp agent list
ottercamp chat start --agent frank
ottercamp project create --name "OC V2"
ottercamp secret set --name openai-key --value sk-...
```

Three output modes: `--output table` (default), `--output json`, `--output quiet` (IDs only).

Auth: `--api-key` flag, `OTTERCAMP_API_KEY` env var, or `~/.ottercamp/credentials` file.

If it can't be done from the CLI, it's not done.

---

## Authentication and Tenancy

- Bearer token auth (`Authorization: Bearer <token>`) and API key auth (`X-API-Key: <key>`)
- All tokens and keys are stored as SHA-256 hashes — never plaintext
- Every request is scoped to an organization via the authenticated principal
- RBAC roles are on `human_user` (owner | admin | member | viewer)
- Audit events are written for every mutating action

---

## Test Mode

Controlled by a single env var: `OTTERCAMP_MODE=test`

Test mode enables:
- Deterministic model responses (recorded fixtures, not live API calls)
- `POST /test/reset` endpoint — wipes all data, re-runs bootstrap. Called before each E2E test.
- Synthetic time control for testing TTLs and schedules
- Relaxed timeouts for CI environments

Test mode does **NOT** disable auth, policy evaluation, sandboxing, control plane enforcement,
or audit logging. A test instance is functionally identical to production except for the
model response layer.

---

## Three-Layer Test Architecture (doc 21)

Every task specifies tests at three layers:

1. **Unit tests** — test packages in isolation. Mock external boundaries (model providers,
   MCP servers, filesystem for non-system tests). Cover important logic exhaustively:
   policy evaluation, tool resolution, prompt assembly, flow progression, event emission.
   Target: full coverage on critical paths.

2. **Integration tests** — run against a real PostgreSQL database using `testdb.New(t)`
   (spins up a clone of the test schema). Test service interactions, DB queries, event bus
   delivery, migration correctness. No mocked DB.

3. **E2E tests** — spin up a full OtterCamp instance in test mode. Exercise the system
   exclusively through the CLI binary and REST API. Call `POST /test/reset` before each
   scenario. Must complete in < 10 minutes total. Defined in tasks 081–088.

---

## Key Architectural Decisions

- **Policy is binary: allow or deny.** No mid-turn blocking. Tier 2 tool calls check policy
  before execution; deny is absolute. Instance safety rules are hardcoded and cannot be
  overridden. Silence (no matching policy) is not a deny.

- **Polymorphic FKs are common.** `scope_type + scope_id`, `principal_type + principal_id`,
  `created_by_type + created_by_id` appear throughout. The DEPENDENCY-GRAPH.md §Polymorphic
  Patterns section documents every pattern and its valid concrete types.

- **Secrets never appear in prompts or tool params.** They are injected at runtime by the
  connector layer. A response sanitizer strips any leaked secrets before returning output.

- **Flow nodes are immutable once a task starts.** The flow template is snapshot-copied onto
  the task at creation. Edits to the template do not affect running tasks.

- **Temp agents are project-scoped and short-lived.** They are created immediately active
  (no draft review), and are auto-retired when their project or task scope ends.

- **All API routes use the `/v1/` prefix.** Doc 21's example paths (`/api/sessions/`) are
  illustrative pseudocode — the authoritative paths are in doc 12. See ISSUE #27.

---

## Open BLOCKERs (Must Not Be Finalized Until Resolved)

These issues in `build/ISSUES.md` affect implementation decisions. Tasks call them out
inline. Do not finalize the flagged design decision until Sam resolves the issue.

| Issue | Severity | Affects | Summary |
|-------|----------|---------|---------|
| #1 | BLOCKER | `agent` | `budget_cap_cents` (doc 05) vs `budget_cap_tokens` (doc 14) — column name unresolved |
| #16 | BLOCKER | `flow_node` | `skills jsonb` must be dropped; `mcp_tools jsonb` + `tool_domains jsonb` must be added — docs 03/09/10 contradict |
| #23 | BLOCKER | `agent`, token budget | Per-agent budget enforcement mechanism unspecified |
| #26 | BLOCKER | browser tool catalog | Browser action allowlist/denylist schema not defined |
| #27 | BLOCKER | all API tasks | `/api/sessions/` (doc 21) vs `/v1/chat-sessions/` (doc 12) path prefix contradiction |

---

## Where Things Live

```
docsv2/             — spec documents (reference only — do not modify during build)
build/
  CONTEXT.md        — this file
  PROGRESS.md       — spec reading notes (tables, FKs, behavioral rules)
  DEPENDENCY-GRAPH.md — complete table catalog with layer assignments + polymorphic patterns
  CHUNKING-PLAN.md  — all 90 tasks with layer/size/domain groupings
  MANIFEST.md       — running index of all written task files
  ISSUES.md         — spec inconsistencies for Sam to resolve
  SUMMARY.md        — final validation report (written last)
  tasks/            — the 90 phase files Codex builds from
```

---

## Spec References

When a task says "doc 04 §Auth Session" it means `docsv2/04-auth-tenancy-and-identity.md`,
section "Auth Session". All specs are **Finished** (fully reviewed, open questions resolved)
except docs 17, 18, 19 (UI specs — Sprint 2 only).
