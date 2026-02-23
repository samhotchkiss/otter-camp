# 035: Model Gateway — Routing, Fallback, and Concurrency

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 07 §ModelGateway, doc 07 §ProviderRouting, doc 07 §FallbackChains, doc 07 §ConcurrencyManager, doc 07 §HealthStateMachine |
| Spec status | finished |
| Depends on | 010, 024, 033 |
| Blocks | 036, 037, 062, 076 |

## Scope

Build the model gateway's core routing machinery: concurrency manager, priority queue,
provider health state machine, connection selection, and retry/fallback logic. Also build
the `model_invocation` DDL and repository. Streaming mode, token tracking, and rollup
aggregation are covered separately in task 036.

### Must build

**Migrations:**
- `0043_model_invocation.sql`

**`model_invocation` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `model_provider_id uuid not null references model_provider(id)`
- `provider_connection_id uuid references provider_connection(id)` — nullable; null if routed to a provider-level default
- `model_profile_id text` — logical_profile_id snapshot at invocation time (not a FK; profiles are versioned)
- `invocation_purpose text not null` — one of: `agent_turn`, `listening_eval`, `summarization`, `skill_summarization`, `memory_extraction`, `memory_retrieval`, `memory_dedup`, `memory_synthesis`, `replay`
- `status text not null check (status in ('pending','in_flight','completed','failed','cancelled'))` default `'pending'`
- `prompt_storage_key text` — object storage key for captured prompt (null if redacted)
- `response_storage_key text` — object storage key for captured response (null if redacted)
- `input_tokens integer` — populated after completion
- `output_tokens integer` — populated after completion
- `cache_read_tokens integer` — provider cache hit tokens (if supported)
- `model_name text not null` — the actual model string sent to the provider (e.g. `claude-3-haiku-20240307`)
- `is_streaming boolean not null default false`
- `latency_ms integer` — duration from dispatch to first token (streaming) or full completion
- `total_duration_ms integer` — total wall clock time including queue wait
- `attempt_number integer not null default 1` — increments on retry within same invocation record
- `fallback_from_invocation_id uuid references model_invocation(id)` — self-ref; set when this invocation is a fallback for a failed one
- `error_code text` — provider error code if failed
- `error_message text`
- `metadata jsonb not null default '{}'` — per-layer token counts, memory injection manifest, other structured context
- `agent_id uuid references agent(id)` — nullable attribution
- `project_id uuid references project(id)` — nullable attribution
- `project_task_id uuid references project_task(id)` — nullable attribution
- `session_id uuid` — soft reference to chat_session (no FK; session may be deleted); annotated `-- soft ref`
- `turn_id uuid` — soft reference to chat_turn (no FK); annotated `-- soft ref`
- `run_id uuid` — nullable FK to run (L4 forward reference; applied as migration in task 062)
- `run_step_id uuid` — nullable FK to run_step (L4 forward reference; applied in task 062)
- `run_attempt_id uuid` — nullable FK to run_attempt (L4 forward reference; applied in task 062)
- `created_at timestamptz not null default now()`
- `completed_at timestamptz`
- Index: `(organization_id, created_at DESC)` — org-level usage listing
- Index: `(agent_id, created_at DESC) WHERE agent_id IS NOT NULL`
- Index: `(project_id, created_at DESC) WHERE project_id IS NOT NULL`
- Index: `(session_id, created_at DESC) WHERE session_id IS NOT NULL` — soft ref index (no FK)
- Note: `run_id`, `run_step_id`, `run_attempt_id` columns and their FK constraints are added in migration `0062_model_invocation_run_fks.sql` (task 062), not here, because `run` is L4.

**Repository layer:**
- `ModelInvocationRepo`: `Create`, `GetByID`, `UpdateStatus`, `UpdateCompletion`, `ListByOrg`, `ListByAgent`, `ListByProject`, `ListBySession`, `ListByRun`
- `UpdateCompletion(ctx, id, input_tokens, output_tokens, cache_tokens, latency_ms, total_duration_ms, prompt_key, response_key) error`

**Concurrency manager (`internal/gateway/concurrency.go`):**
- `ConcurrencyManager` — global slot pool + per-provider slot pools
- Global slots: configurable (default 50); per-provider slots: configurable per `provider_connection` row (`max_concurrent` column)
- `Reserve(ctx, providerID) (releaseFunc, error)` — blocks until slot available or context cancelled; returns a release closure
- Slot reservation is synchronous; callers must call release when the invocation completes or fails
- `ActiveCount() int` — observable for metrics

**Priority queue (`internal/gateway/queue.go`):**
- Four priority tiers (doc 07), evaluated highest-first:
  1. `sync_interactive` — user-facing synchronous turns
  2. `sync_system` — system-driven synchronous calls (e.g., listening eval)
  3. `async_agent` — agent-initiated async work
  4. `async_system` — background jobs (e.g., memory extraction, rollups)
- FIFO ordering within each tier
- Soft preemption: on `sync_interactive` arrival, the most-recently-started `async_agent` or `async_system` call is flagged for pause (doc 07); paused callers receive a cancellable context; they finish their current atomic unit and yield
- Queue timeout by tier: `sync_interactive` = 30s wait, `sync_system` = 60s, `async_agent` = 300s, `async_system` = 600s; exceed → return `ErrQueueTimeout`
- `Enqueue(ctx, req GatewayRequest) (GatewayResponse, error)` — main entry point; handles slot reservation, routing, retries, fallback

**Health state machine per provider_connection (`internal/gateway/health.go`):**
- States: `healthy` → `degraded` → `rate_limited` → `unavailable`
- Transitions:
  - `healthy` → `degraded`: ≥2 consecutive failures within 60s
  - `degraded` → `rate_limited`: HTTP 429 received
  - `degraded` / `rate_limited` → `unavailable`: ≥5 failures or provider returns 5xx for >2 min
  - Any state → `healthy`: successful invocation (with exponential backoff probe before re-admitting)
- Health state stored in-memory per connection (not persisted; resets on process restart)
- `HealthChecker.GetState(connectionID) HealthState`
- `HealthChecker.RecordSuccess(connectionID)`
- `HealthChecker.RecordFailure(connectionID, err error)`

**Connection selection (`internal/gateway/router.go`):**
- `SelectConnection(ctx, orgID, profileID, priority PriorityTier) (*ProviderConnection, error)`
- Resolves `model_profile` → `model_provider` → active `provider_connection` rows for that provider in the org
- Health-aware: skips `unavailable` connections; prefers `healthy` over `degraded`/`rate_limited`
- `failover_priority` ordering: connections with lower `failover_priority` value are tried first
- If all connections for the primary provider are unhealthy → attempt fallback via `model_profile.fallback_profile_id` chain (application-layer resolution, max 3 hops to prevent infinite loops)
- Returns `ErrNoHealthyConnection` if no connection available after fallback exhaustion

**Retry logic:**
- Transient errors (5xx, network timeout): retry up to 2 times with exponential backoff (500ms, 1s base); jitter ±20%
- Rate limit (429): respect `Retry-After` header if present; else 2s backoff; count as 1 retry attempt
- Non-retryable errors (4xx except 429, auth errors): fail immediately, no retry
- Retry creates a new invocation record row with `fallback_from_invocation_id` pointing to the failed attempt

### Must NOT build
- Streaming mode response handling (task 036)
- Token counting and daily rollup aggregation job (task 036)
- Prompt/response capture to object storage (task 036)
- Model API endpoints (task 037)
- `run_id`/`run_step_id`/`run_attempt_id` FK constraints (task 062; those are L4 FKs)
- Token budget enforcement (task 033)

## Acceptance Criteria

- [ ] Migration `0043_model_invocation.sql` applies cleanly; `run_id`, `run_step_id`, `run_attempt_id` columns exist as plain `uuid` with no FK constraint (FK constraints are added in task 062)
- [ ] `ConcurrencyManager.Reserve` blocks when global slots exhausted; unblocks after a release call; context cancellation unblocks with error
- [ ] Priority queue serves `sync_interactive` requests before queued `async_agent` requests when both are waiting for the same slot
- [ ] Health state machine transitions from `healthy` → `degraded` after 2 consecutive failures within 60s window
- [ ] `SelectConnection` skips `unavailable` connections and selects the next healthiest by `failover_priority`
- [ ] Fallback chain: if primary profile's connection is unavailable, `SelectConnection` follows `fallback_profile_id` up to 3 hops
- [ ] Retry on 5xx: two retries with backoff; `attempt_number` increments on the same `model_invocation` record; third failure returns error to caller
- [ ] `ErrQueueTimeout` returned when a `sync_interactive` request waits more than 30s without getting a slot

## Tests Required

**Unit tests:**
- `ConcurrencyManager`: fill slots to capacity → new reserve call blocks; release one → blocked call proceeds; context cancel → unblocks with error
- Priority queue ordering: enqueue `async_agent` first, then `sync_interactive`; drain queue → `sync_interactive` is dequeued first
- Soft preemption: active `async_agent` invocation receives cancel signal when `sync_interactive` arrives and all slots are full
- Health state machine: 2 failures → degraded; success → healthy; 5xx stream → unavailable; success probe → healthy
- `SelectConnection`: all connections `unavailable` for primary → follows `fallback_profile_id`; max 3 hops prevents infinite loop
- Retry: non-retryable 4xx → immediate failure, no retry; 5xx → retried twice; `fallback_from_invocation_id` set on retry rows

**Integration tests:**
- `ModelInvocationRepo.Create` + `UpdateCompletion`: round-trip; `completed_at` set; `input_tokens` updated
- `ListByOrg`: returns only the calling org's invocations; other org's rows not visible
- Health state machine integration: `RecordFailure` × 2 within 60s → `GetState` returns `degraded`

**E2E tests:**
- None — covered by dedicated E2E task 076

## Implementer Notes

> ⚠️ ISSUE #18 (AMBIGUOUS): `run_attempt_id` on `model_invocation` is unclear for agent turn-loop calls. When a model call happens during a conversational agent turn (not inside a worker-dispatched run), `run_attempt_id` will be null. The column exists and is populated for worker-domain calls. Do not error on null `run_attempt_id`; the aggregation query in task 062 uses `WHERE run_attempt_id = $1` and will only count invocations with the FK set. Add a comment: `-- null for non-worker model calls (e.g. chat turns); see ISSUE #18`.

> ⚠️ ISSUE #21 (BLOCKER): `model_invocation` retention policy is unresolved — doc 13 says 30 days, doc 14 says 90 days. The retention enforcement job is in task 063. This task builds only the schema; do not hardcode a retention value here. Leave a `// TODO: ISSUE #21 — retention duration TBD (30d vs 90d); enforced in task 063` comment in the migration file.

- The `session_id` and `turn_id` columns are soft references (no SQL FK). They are annotated with `-- soft ref: chat_session.id` in the DDL comment. This is intentional: sessions may be deleted or purged while invocation records must be kept for billing/audit purposes.
- The `run_id`, `run_step_id`, and `run_attempt_id` columns in this migration exist as plain `uuid` columns with no FK constraint. The FK constraints are added in migration `0062_model_invocation_run_fks.sql` (task 062) after the `run`, `run_step`, and `run_attempt` tables are created at L4. Document this split in a DDL comment: `-- FK constraints for run_id, run_step_id, run_attempt_id added in 0062_model_invocation_run_fks.sql`.
- The `invocation_purpose` field drives which model profile is selected (e.g., `listening_eval` uses Haiku-class, `agent_turn` uses the agent's assigned profile). The routing logic in `SelectConnection` must accept `invocation_purpose` as an input to the profile resolution step.
- `metadata jsonb` should record per-layer token counts (populated by the prompt assembly engine in task 050) and the memory injection manifest (populated by the memory retrieval pipeline in task 040). At this layer, `metadata` is stored as-is from the caller; this task does not interpret it.
- Deterministic mode for tests: when `OTTERCAMP_MODE=test` and `GATEWAY_DETERMINISTIC=true`, the gateway returns a canned response fixture from `testdata/responses/{provider}/{model}.json` instead of making a real network call. This makes unit and integration tests hermetic without requiring mock HTTP servers.
