# 041: Memory Compaction and Import

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 06 §SleepReflection, doc 06 §TaskConsolidation, doc 06 §MemoryDecay, doc 06 §MemoryImport, doc 06 §ScopePromotion |
| Spec status | finished |
| Depends on | 038, 039, 040, 004, 024 |
| Blocks | 042, 075, 086 |

## Scope

Build the memory compaction subsystem (sleep-time reflection job, task-completion
consolidation run, memory decay) and the JSONL zip import pipeline (CLI command +
`memory_import` status tracking). These are background jobs and data-loading utilities;
no API endpoints in this task.

### Must build

**Sleep-time reflection job (`internal/memory/compaction/sleep_reflection.go`):**
- Job type: `memory_sleep_reflection`; triggered by:
  - Periodic schedule: once per 6 hours for each active org (scheduled via `task_schedule` type=`system`)
  - Friction-signal trigger: when cumulative friction signals for an org exceed a threshold (5 signal events since last reflection run), enqueue an immediate reflection job
- `SleepReflector.Run(ctx, orgID uuid.UUID, compactionRunID uuid.UUID) error`
  - Opens a `memory_compaction_run` row (already created by the job scheduler before dispatching)
  - Fetches all `status='candidate'` memories older than 1 day that were not yet reviewed by the hardener job (7-day hold has not elapsed)
  - For each candidate batch (up to 50): calls Haiku-class LLM (`invocation_purpose='memory_dedup'`) to review the batch for:
    - Redundancy within the batch → merge or discard weaker member
    - Upgraded utility: if utility reasoning suggests a candidate should score higher, upgrade `extraction_score` (still must be ≥40 to stay)
  - Decays `confidence` on `status='active'` episodic memories older than the half-life threshold:
    - Episodic half-life: 30 days → `confidence *= 0.5`; if `confidence < 0.1`, set `status='archived'`
    - Procedural half-life: 180 days → `confidence *= 0.5`
    - Semantic memories: no decay
  - Updates `memory_compaction_run.memories_examined`, `memories_updated`, `memories_archived` counters
  - Marks run `status='completed'` or `status='failed'`

**Task-completion consolidation run (`internal/memory/compaction/task_consolidation.go`):**
- Triggered by: `task.completed` domain event (subscribed consumer in `internal/memory/event_consumer.go`)
- `TaskConsolidator.Consolidate(ctx, orgID, projectID, taskID uuid.UUID) error`
  - Creates a `memory_compaction_run` row with `run_type='task_consolidation'` and `scope_context={project_id, task_id}`
  - **Scope promotion**: fetches all `scope='task'` memories with `project_task_id = taskID` and `status='active'`; for each:
    - If `memory_type` is `episodic` → mark `status='archived'` (episodic task memories are not promoted; they contributed to the execution summary)
    - If `memory_type` is `semantic`, `procedural`, or `preference` → update `scope='project'`, clear `project_task_id` (promoted to project-level)
    - If `memory_type` is `entity_profile` → update `scope='project'` if the entity appears in ≥3 other tasks; otherwise keep task-scoped and archive
  - **Episodic distillation**: collect all episodic memories for the task; call LLM (`invocation_purpose='memory_synthesis'`) to produce a 1–3 paragraph execution summary; store as new `memory` row with `memory_type='execution_summary'`, `scope='project'`, linked to the task via `project_task_id`
  - **Entity synthesis trigger**: for each entity that appears in ≥5 memory mentions across this task's memories, call `EntitySynthesizer.SynthesizeProfile` (task 040)
  - Updates `memory_compaction_run` counters; sets `status='completed'`

**Memory decay scheduler:**
- The episodic/procedural decay logic runs within `SleepReflector.Run` (described above)
- Also runs standalone during task consolidation for task-scoped memories being archived
- Decay is applied at read time in the compaction run only, not at retrieval time (retrieval returns current `confidence` without decay; decay is a batch-update operation)

**JSONL zip import pipeline (`internal/memory/importer/importer.go`):**
- Entry point: `Importer.StartImport(ctx, orgID uuid.UUID, requestedBy uuid.UUID, fileKey string) (importID uuid.UUID, error)`
  - Creates a `memory_import` row with `status='pending'`, `file_key=fileKey`
  - Enqueues a `memory_import_process` job
  - Returns `importID` to caller immediately (non-blocking)
- Job processor: `Importer.ProcessImport(ctx, importID uuid.UUID) error`
  - Updates `memory_import.status='processing'`, `started_at=now()`
  - Downloads the zip file from object storage using the key from `memory_import.file_key`
  - Validates zip: must contain at least one `.jsonl` file; total uncompressed size ≤ 50MB; rejects non-JSONL content
  - Parses JSONL records; each record must have at minimum: `{content: string, memory_type: string}`; optional fields: `confidence`, `utility_score`, `entities: [{name, type}]`, `taxonomy_tags: [string]`
  - For each valid record: runs the standard 4-stage extraction pipeline via `Extractor.ExtractFromImport` with `trust_tier_cap=0.6`
  - Updates `memory_import.total_records`, `processed_records`, `imported_records`, `rejected_records` as processing proceeds (batch updates every 100 records)
  - On completion: sets `status='completed'`, `completed_at=now()`
  - On error: sets `status='failed'`, `error_message=err.Error()`

**CLI command (`ottercamp memory import`):**
- `ottercamp memory import --file <path> [--org-id <uuid>] [--wait]`
- Uploads the file to object storage at key `imports/{org_id}/{uuid}/{filename}` using the configured storage adapter
- Calls `Importer.StartImport` with the uploaded file key
- If `--wait` flag: polls `GET /v1/memory/imports/:id` every 5s until status is `completed` or `failed`; prints final counts
- If no `--wait`: prints the `importID` and exits immediately
- Requires auth (reads `~/.ottercamp/credentials` or `OTTERCAMP_API_KEY` env)

**Domain event subscriptions:**
- Subscribe to `task.completed` events on the domain event bus (task 024); triggers `TaskConsolidator.Consolidate`
- Subscribe to `agent.friction_signal` events (if defined); increments friction signal counter for sleep reflection

### Must NOT build
- Memory API endpoints (task 042) — `POST /v1/memory/import` handler calls `Importer.StartImport` but lives in task 042
- Memory extraction pipeline (task 039) — `Importer` delegates to `Extractor`; do not re-implement extraction here
- Memory retrieval pipeline (task 040) — accessed by task consolidation via `Retriever`; do not re-implement here

## Acceptance Criteria

- [ ] `SleepReflector.Run` applies episodic decay: active episodic memory with `created_at = 31 days ago` and `confidence=0.6` → after run, `confidence=0.3`
- [ ] `SleepReflector.Run` archives episodic memory when `confidence < 0.1` after decay: `status='archived'`, `archived_at` set
- [ ] Task consolidation promotes semantic memory from `scope='task'` to `scope='project'`; clears `project_task_id`
- [ ] Task consolidation archives episodic task memories (does not promote them)
- [ ] Task consolidation creates an `execution_summary` memory linked to the task
- [ ] Import pipeline: valid JSONL zip with 50 records → `memory_import.total_records=50`; records meeting threshold → `imported_records` count correct
- [ ] Import pipeline: zip with 60MB uncompressed content → rejected; `status='failed'`, `error_message` contains size limit message
- [ ] `memory_import` status progresses: `pending → processing → completed` (or `failed`)
- [ ] CLI `ottercamp memory import --wait` exits 0 on successful import; prints imported count

## Tests Required

**Unit tests:**
- Episodic decay: memory with `confidence=0.6`, age=31 days → after decay `confidence=0.3`; age=62 days (second half-life) → `confidence=0.15`; age=93 days → `confidence=0.075` (below 0.1 → archive)
- Scope promotion rules: table-driven test across all `memory_type` values against promotion logic (episodic → archived; semantic → project-promoted; entity_profile → conditional)
- JSONL validation: missing `content` field → record counted as rejected; invalid `memory_type` value → rejected; `trust_tier` in record capped at 0.6
- Import size limit: construct 51MB uncompressed content → validate returns error; 49MB → validate passes

**Integration tests:**
- Full import round-trip (deterministic extraction): upload JSONL zip with 5 valid records; run import processor; verify `memory_import.status='completed'`, `imported_records ≥ 1`, at least 1 `memory` row created
- Task consolidation: insert 5 task-scoped memories (mix of types); publish `task.completed` event; verify promotion/archival counts in `memory_compaction_run`; verify `execution_summary` memory created
- Sleep reflection decay: seed active episodic memory older than 30 days; run `SleepReflector.Run`; verify `confidence` updated in DB

**E2E tests:**
- None — covered by dedicated E2E task 075 and 086

## Implementer Notes

- The `task.completed` event subscription must be idempotent: if the consolidation job is enqueued twice (e.g., due to at-least-once delivery), the second run should check `memory_compaction_run` for an existing completed run for the same `task_id` and exit early with a no-op.
- The sleep reflection job runs once per 6 hours per org. For a multi-tenant instance with many orgs, stagger the job schedules by org (e.g., `org_hash % 6` hour offset) to avoid all orgs running compaction simultaneously. The `task_schedule` system job for sleep reflection should be created per-org during bootstrap (task 012).
- The import pipeline's in-progress counter update (every 100 records) allows the API endpoint (task 042) to return live progress on `GET /v1/memory/imports/:id`. The update must use an UPDATE statement, not a full row fetch-modify-save, to avoid overwriting concurrent updates.
- JSONL import records may include pre-computed `embedding` vectors (useful for re-importing memories from a backup). If a record includes `embedding: [float, ...]` with 1536 dimensions, skip the embedding generation step (Stage 4) for that record and use the provided vector directly. Validate that the embedding dimension matches (reject if not 1536).
- Memory decay during sleep reflection is a bulk UPDATE operation, not row-by-row. Use a single SQL UPDATE statement: `UPDATE memory SET confidence = confidence * 0.5, updated_at = now() WHERE organization_id = $1 AND memory_type = 'episodic' AND status = 'active' AND created_at < now() - interval '30 days'` followed by a separate `UPDATE memory SET status='archived', archived_at=now() WHERE ... AND confidence < 0.1`. Run both in a transaction.
