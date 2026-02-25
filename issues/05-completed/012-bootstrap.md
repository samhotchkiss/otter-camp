# 012: Bootstrap Sequence

| Field | Value |
|-------|-------|
| Layer | L1 |
| Size | M (1–2 days) |
| Spec refs | doc 14 §BootstrapSequence, doc 08 §BootstrapCLI, doc 21 §TestReset |
| Spec status | finished |
| Depends on | 003, 005, 008, 009, 010, 011 |
| Blocks | 013, 014, 015, 016, 017, 018, 019, 020, 021, 022, 023, 024 |

## Scope

Implement the 10-step idempotent bootstrap sequence (doc 14 is authoritative — doc 04's
7-step sequence is stale per ISSUE #2). Implement the `ottercamp bootstrap` CLI command,
the `POST /test/reset` endpoint for test mode, and the integration test that verifies the
full sequence.

### Must build

**`internal/bootstrap/` package:**

`Bootstrapper` struct with `Run(ctx context.Context) error` method. The bootstrap is
**idempotent** — each step checks if the work is already done before doing it. Running
twice produces the same state as running once.

**10-step sequence** (doc 14 authoritative):

**Step 1: Run pending migrations**
- Call migration runner (from task 002)
- Idempotency: migrations are always idempotent (schema_migrations tracking)

**Step 2: Create organization**
- Check for existing org with `slug = OTTERCAMP_ORG_SLUG` (env var, default: `default`)
- If absent: create with `display_name = OTTERCAMP_ORG_NAME` (env var, default: `OtterCamp`)
- Output: `org.id` stored for use in subsequent steps

**Step 3: Create first human user**
- Check for existing `admin` role user in the org
- If absent: create with `email = OTTERCAMP_ADMIN_EMAIL`, `password = OTTERCAMP_ADMIN_PASSWORD` (bcrypt work factor 12), `role = admin`
- If both env vars absent: skip user creation (non-interactive mode, e.g. managed deployment)

**Step 4: Create org skills repository and seed default skills**
- Ensure `skills/` directory exists (object storage or local filesystem)
- Seed default skill files from embedded `defaults/skills/*.md` assets (`//go:embed defaults/skills`)
- `SkillRepo.BulkUpsertBySlug` for each default skill; `created_by_type='system'`
- Default skills list: at minimum `summarize`, `code-review`, `plan-task` (exact list from spec assets)

**Step 5: Seed model providers and model profiles; create org-level assignments**
- `ModelProviderRepo`: upsert Anthropic and OpenAI providers by slug
- `ModelProfileRepo`: upsert three org-level profiles: `high-capability` (claude-opus-4-5), `standard` (claude-sonnet-4-5), `haiku` (claude-haiku-3-5) — each with `is_current=true`
- Per ISSUE #11 (RESOLVED, doc 08): create `model_profile_assignment` rows at `scope_type='organization'` for each profile + purpose combination (at minimum `agent_turn` for all three; `listening_eval` → haiku; `summarization` → standard; memory purposes → haiku)
- Idempotency: `ModelProfileAssignmentRepo.Upsert` is idempotent by unique constraint

**Step 6: Seed default flow templates**
- Create two system flow templates (project_id=null, system-provided):
  - `default-single-agent`: one node, single agent actor
  - `default-review`: two nodes (work → review); review node requires human reviewer
- `FlowTemplateRepo.BulkUpsert` by template slug (deferred to task 016 when project schema exists)
- **Implementation note:** steps that depend on tables not yet created (flow templates, agents) are implemented as stubs in this task that log "step N skipped — table not yet created" and succeed. The real implementation is wired in the respective table tasks via the `Bootstrapper.RegisterStep` extension point.

**Step 7: Create starter trio agents** (stub — real implementation in task 013/014)
- Log "step 7 skipped — agent table not yet created" and succeed

**Step 8: Create General session + add participants** (stub — real implementation in task 043)
- Log "step 8 skipped — chat_session table not yet created" and succeed

**Step 9: Seed default org capability policy** (stub — real implementation in task 033)
- Log "step 9 skipped — capability_policy table not yet created" and succeed

**Step 10: Record bootstrap audit event**
- `AuditService.Record` with `event_type='system.bootstrap'`, `principal_type='system'`, principal_id = sentinel UUID, metadata: `{version: <app_version>, step_count: 10, timestamp: <now>}`
- Idempotency: check for existing `system.bootstrap` audit event before inserting; if found, log "already bootstrapped" and skip

**Extension point for stubs:**
```go
type Bootstrapper struct {
    steps []BootstrapStep
}
func (b *Bootstrapper) RegisterStep(name string, fn func(ctx context.Context, state *BootstrapState) error)
```
Agent, chat, and policy tasks call `bootstrapper.RegisterStep` to replace stub steps. This avoids the bootstrap package importing agent/chat/policy packages (circular dependency prevention).

**CLI command:**
- `ottercamp bootstrap` — runs the 10-step sequence, prints step-by-step progress to stdout, exits 0 on success

**`POST /test/reset` endpoint** (only active when `OTTERCAMP_MODE=test`):
- Truncates all application tables (order respects FK constraints; use `TRUNCATE ... CASCADE`)
- Runs the full bootstrap sequence on the empty database
- Returns `204 No Content` on success
- Registered as a route only when `OTTERCAMP_MODE=test`; returns `404` in production mode

### Must NOT build
- Agent, chat session, capability policy, or flow template creation (those are stubs here; real work in tasks 013, 043, 033, 016)
- Any seed data beyond what doc 14 steps 1–10 explicitly list
- Multi-org bootstrap (this is single-org bootstrap)

## Acceptance Criteria

- [ ] `ottercamp bootstrap` on a fresh database runs all 10 steps and exits 0; log shows each step with "done" or "skipped" status
- [ ] `ottercamp bootstrap` on an already-bootstrapped database runs without error; no duplicate rows created (idempotent)
- [ ] Step 2: `organization` row exists with correct slug and display_name after bootstrap
- [ ] Step 3: `human_user` row with `role='admin'` exists after bootstrap (when env vars set); absent when env vars not set
- [ ] Step 4: org-scoped skills exist in DB with `created_by_type='system'`; `skills/` directory populated in object storage
- [ ] Step 5: three `model_profile` rows exist (`high-capability`, `standard`, `haiku`) with `is_current=true`; org-level `model_profile_assignment` rows exist for each profile+purpose pair
- [ ] Step 10: `audit_event` with `event_type='system.bootstrap'` exists after bootstrap; second run does not create a second audit event
- [ ] `POST /test/reset` (in test mode): after call, all application tables empty except for bootstrap seed data; returns 204
- [ ] `POST /test/reset` in production mode: returns 404 (endpoint not registered)
- [ ] Registration of a stub step: calling `bootstrapper.RegisterStep("create-agents", fn)` replaces the stub for step 7 with the provided function

## Tests Required

Following the architecture in doc 21 (`OTTERCAMP_MODE=test`):

**Unit tests:**
- `Bootstrapper.RegisterStep`: verify registered steps override stubs; verify step order is preserved; verify a panic in a step is recovered and returned as an error
- Idempotency check functions: test each individual "check if already done" function in isolation

**Integration tests:**
- Full 10-step bootstrap against real PostgreSQL (via `testdb.New(t)`):
  - Fresh DB → bootstrap → verify all seed rows exist
  - Bootstrap twice → no duplicate rows in `organization`, `human_user`, `skill`, `model_profile`, `model_profile_assignment`, `audit_event`
  - Bootstrap with no `OTTERCAMP_ADMIN_EMAIL` set → step 3 skipped → no `human_user` row
  - `POST /test/reset` → fresh state + bootstrap seed data re-created

**E2E tests:**
- None — covered by dedicated E2E task 081 (org bootstrap E2E is the first E2E test)

## Implementer Notes

- ✅ ISSUE #2 (RESOLVED): Doc 04's 7-step bootstrap sequence is stale. Doc 14's 10-step sequence is authoritative. When reading doc 04, ignore its bootstrap section entirely. The correct steps are in doc 14.
- The `RegisterStep` extension point is necessary to prevent circular imports. The bootstrap package runs early (before most domain packages exist) and must not import them. Domain packages call `bootstrapper.RegisterStep(...)` during their own init or service construction.
- `TRUNCATE ... CASCADE` in `POST /test/reset` must truncate in an order that doesn't violate FK constraints, or use `CASCADE` which PostgreSQL handles automatically. Test: verify that `TRUNCATE organization CASCADE` removes all dependent rows across all tables.
- Stub steps log at INFO level with a consistent format: `"bootstrap step N (name): skipped — waiting for table %s to be registered"`. This makes it easy to grep for unregistered steps.
- The `system.bootstrap` audit event idempotency check: query `SELECT 1 FROM audit_event WHERE event_type='system.bootstrap' AND organization_id=$1 LIMIT 1`. If found, skip step 10. Do not compare timestamps or versions — any existing bootstrap event means the org was already bootstrapped.
- ✅ ISSUE #4 (RESOLVED): `private_memory_enabled=false` for all agents by default (staff, PMs, Frank, Lori, Ellie, temps). Private memory is explicit opt-in only for sensitive-personal-data agents.
- ✅ ISSUE #24 (RESOLVED): Starter trio upgrade path — **system_prompt only, on startup**. When the bootstrap detects an existing installation, compare the running binary version against the version recorded in the step 10 audit event metadata. If the version differs, update `system_prompt` for Frank, Lori, and Ellie from the shipped defaults. Do NOT overwrite `operator_instructions`, tool policy, model assignments, or skill attachments — those belong to the operator. The CLI command `ottercamp agent sync-defaults` lets operators manually apply new shipped defaults for all other fields when they want them.
