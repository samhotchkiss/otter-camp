# 036: Model Gateway — Streaming, Token Tracking, and Rollups

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 07 §StreamingMode, doc 07 §TokenTracking, doc 07 §UsageRollup, doc 07 §PromptCapture, doc 13 §RetentionPolicy |
| Spec status | finished |
| Depends on | 035, 004, 003 |
| Blocks | 037, 062, 076 |

## Scope

Build the second half of the model gateway: streaming response handling, token count
recording, daily usage rollup aggregation, prompt/response capture to object storage,
and deterministic test mode. The `model_usage_rollup` table is created here.

### Must build

**Migrations:**
- `0044_model_usage_rollup.sql`

**`model_usage_rollup` table** (doc 07):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `rollup_date date not null` — UTC date for the aggregation window (daily granularity)
- `rollup_type text not null check (rollup_type in ('provider_connection','model_provider','agent','project'))` — grouping dimension
- `rollup_id uuid` — FK target depends on `rollup_type`; application-layer FK only (no SQL FK constraint); null for org-level totals
- `model_name text` — null means "all models for this rollup_id"
- `invocation_purpose text` — null means "all purposes"
- `total_invocations integer not null default 0`
- `total_input_tokens bigint not null default 0`
- `total_output_tokens bigint not null default 0`
- `total_cache_read_tokens bigint not null default 0`
- `total_latency_ms bigint not null default 0` — sum; divide by total_invocations for average
- `total_cost_microcents bigint not null default 0` — estimated cost at time of rollup; 0 if provider pricing not configured
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Unique index: `(organization_id, rollup_date, rollup_type, rollup_id, model_name, invocation_purpose)` — upsert target; `rollup_id`, `model_name`, `invocation_purpose` may be null so use `NULLS NOT DISTINCT` or a surrogate expression index

**Repository layer:**
- `ModelUsageRollupRepo`: `Upsert`, `ListByOrg`, `ListByRollupID`, `GetForDate`
- `Upsert` uses `INSERT ... ON CONFLICT ... DO UPDATE SET total_invocations = ..., updated_at = now()`

**Streaming mode (`internal/gateway/streaming.go`):**
- `sync` sessions: streaming enabled by default — SSE/server push chunks as they arrive from the provider
- `async` sessions: streaming disabled — gateway waits for full response before returning
- `StreamResponse(ctx, invocationID, chunkChan <-chan []byte, writer ResponseWriter) error`
  - Reads chunks from `chunkChan` as the provider streams them
  - Writes each chunk to `writer` immediately (for SSE fan-out; writer is injected by the chat layer)
  - Records `latency_ms` as time-to-first-chunk
  - Accumulates full response for token counting and prompt capture after stream ends
- Non-streaming path: `AwaitResponse(ctx, invocationID) ([]byte, error)` — blocks for full response, then records

**Token counting and completion recording:**
- After response (streaming: after stream end; non-streaming: after response received):
  - Call `ModelInvocationRepo.UpdateCompletion` with `input_tokens`, `output_tokens`, `cache_read_tokens`, `latency_ms`, `total_duration_ms`
  - If provider returns token counts in response headers/body, use those; else count locally (approximate BPE tokenizer for Anthropic models; tiktoken for OpenAI-compatible models)
  - Enqueue a `job_queue` job of type `rollup_update` to trigger the daily aggregation worker (do not block the calling goroutine on rollup)

**Prompt/response capture (`internal/gateway/capture.go`):**
- Controlled by org-level redaction policy: `organization.settings.model_capture` (boolean per field: `capture_prompts`, `capture_responses`)
- If capture enabled:
  - Store prompt as gzipped JSON to object storage at key `invocations/{org_id}/{year}/{month}/{invocation_id}/prompt.json.gz`
  - Store response at key `invocations/{org_id}/{year}/{month}/{invocation_id}/response.json.gz`
  - Update `model_invocation.prompt_storage_key` and `response_storage_key`
- If capture disabled or org has opted out: set both columns to null; do not write to object storage
- The capture worker runs synchronously in the gateway goroutine (after token counting) but object storage writes are fire-and-forget (errors logged, not propagated to caller)

**Prompt metadata recording:**
- `metadata jsonb` on `model_invocation` is populated by the caller (prompt assembly engine, task 050) before the invocation record is created
- Gateway does not modify `metadata`; it stores it as-is
- Per-layer token counts (populated by task 050) are stored under `metadata.layer_token_counts`
- Memory injection manifest (populated by task 040) is stored under `metadata.memory_manifest`

**Daily rollup aggregation job (`internal/gateway/rollup_worker.go`):**
- Job type: `rollup_update`; processed by the job worker from task 024
- `RunRollupForDate(ctx, orgID uuid.UUID, date time.Time) error`
  - Aggregates `model_invocation` rows for the org for the given UTC date
  - Groups by `rollup_type` × `rollup_id` × `model_name` × `invocation_purpose`
  - Upserts into `model_usage_rollup` for each grouping
  - Also writes org-level totals row (`rollup_id=null`, `rollup_type='model_provider'`, aggregated across all providers)
- Triggered by: `rollup_update` job enqueued by token counting path after each completed invocation
- Daily full re-rollup also runs as a scheduled job at 02:00 UTC via `task_schedule` (type `system`) to correct any missed incremental updates

**Deterministic test mode:**
- When `OTTERCAMP_MODE=test` and `GATEWAY_DETERMINISTIC=true`:
  - `SelectProvider` always returns the configured test provider stub
  - Response fixtures loaded from `testdata/responses/{provider}/{model}.json`
  - Token counts read from fixture `metadata.input_tokens` / `metadata.output_tokens`
  - Streaming simulated: fixture response chunked into 20-byte pieces with 1ms delay between chunks
  - Prompt/response capture still runs (writes to test object storage adapter from task 004)

**`invocation_purpose` routing to system profile:**
- `listening_eval`, `summarization`, `memory_extraction`, `memory_retrieval`, `memory_dedup` → always use the Haiku-class system profile (bypasses per-agent/per-project assignment; these are infrastructure calls)
- `agent_turn`, `skill_summarization` → use normal profile resolution hierarchy (flow_node > agent > project > org)
- `replay` → use the model_profile recorded in the original invocation's `model_profile_id`
- This routing is implemented in `SelectConnection` (task 035) and passed down; documented here for clarity

### Must NOT build
- Gateway routing, concurrency manager, fallback chains (task 035)
- Model API endpoints (task 037)
- Run FK constraints on `model_invocation` (task 062)
- Retention enforcement job (task 063)

## Acceptance Criteria

- [ ] Migration `0044_model_usage_rollup.sql` applies cleanly; unique index with `NULLS NOT DISTINCT` allows multiple null-dimension rollup rows but prevents duplicate (org, date, type, id, model, purpose) tuples
- [ ] Streaming path: first chunk written to `writer` before full response arrives; `latency_ms` = time from dispatch to first chunk; `total_duration_ms` = time from dispatch to stream end
- [ ] Non-streaming path: `AwaitResponse` blocks until full response; token counts recorded correctly
- [ ] Prompt captured to object storage when `organization.settings.model_capture.capture_prompts=true`; `prompt_storage_key` updated on `model_invocation` row
- [ ] Prompt NOT captured when `capture_prompts=false`; `prompt_storage_key` remains null
- [ ] `RunRollupForDate` upserts correct totals: two invocations with 100+200 input tokens → rollup row has `total_input_tokens=300`
- [ ] `invocation_purpose='listening_eval'` routes to Haiku-class system profile regardless of agent's assigned profile
- [ ] Deterministic test mode returns fixture response; streaming simulation delivers multiple chunks; `input_tokens` matches fixture value

## Tests Required

**Unit tests:**
- Streaming accumulation: feed 5 chunks into `StreamResponse`; verify writer received all 5 in order; verify accumulated response matches concatenated chunks
- Token counting: verify local BPE approximation is within 5% of known token count for a fixed prompt string (test vector from `testdata/`)
- Rollup aggregation: mock `model_invocation` rows; `RunRollupForDate` produces correct sums per grouping dimension
- `invocation_purpose` routing: `listening_eval` → system Haiku profile; `agent_turn` → agent profile hierarchy
- Capture: `capture_prompts=false` → object storage `Put` never called; `capture_prompts=true` → `Put` called with correct key prefix

**Integration tests:**
- Full round-trip (deterministic mode): create invocation record → call `StreamResponse` with fixture → verify `model_invocation.input_tokens` updated → verify rollup row upserted
- Rollup upsert idempotency: run `RunRollupForDate` twice for same date → row counts remain correct (no double-count); `updated_at` changes on second run
- Object storage capture: end-to-end with filesystem adapter; verify gzipped JSON retrievable at expected key

**E2E tests:**
- None — covered by dedicated E2E task 076

## Implementer Notes

> ✅ ISSUE #21 (RESOLVED): `model_invocation` retention is **90 days**, enforced in task 063. Rollup rows are kept indefinitely. No TODO needed in this task.

- The `model_usage_rollup` unique index must handle null `rollup_id`, null `model_name`, and null `invocation_purpose` dimensions. PostgreSQL treats NULLs as distinct in unique indexes by default, which would allow duplicate org-level total rows. Use `CREATE UNIQUE INDEX ... ON model_usage_rollup (organization_id, rollup_date, rollup_type, COALESCE(rollup_id::text,''), COALESCE(model_name,''), COALESCE(invocation_purpose,''))` or a generated column to avoid this.
- Prompt/response capture writes are fire-and-forget: the gateway goroutine does not block waiting for object storage to confirm the write. Errors are logged with `level=warn` and the invocation is not retried due to a capture failure. The `prompt_storage_key` update is attempted with a short timeout (5s); if it fails, the column stays null.
- The `rollup_update` job payload must include `org_id` and `rollup_date` (UTC date of the invocation). Using a job queue entry rather than a direct DB write prevents blocking the hot path (model call → response → caller unblocked) on aggregation work.
- Cost estimation in `total_cost_microcents` requires a pricing table per model/provider. V2 does not specify a pricing config schema. Implement as: read optional `model_provider.pricing_config jsonb` (if column exists) for input/output token rates; if not configured, leave `total_cost_microcents=0`. Add a comment noting this is display-only and not used for budget enforcement (budget enforcement uses token counts, not cost).
