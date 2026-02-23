# 038: Memory Schema

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 06 §MemorySchema, doc 06 §MemoryTaxonomy, doc 06 §MemoryEntity, doc 06 §MemorySource, doc 06 §MemoryImport, doc 06 §MemoryCompaction |
| Spec status | finished |
| Depends on | 003, 005, 016, 013 |
| Blocks | 039, 040, 041, 042 |

## Scope

Build all 9 memory subsystem tables and their repositories. Covers DDL migrations,
pgvector index creation, and the repository layer for every table. No pipeline logic,
no API endpoints — only schema and data access.

### Must build

**Migrations (in order):**
- `0045_memory.sql` — `memory` table + indexes
- `0046_memory_tags_entities.sql` — `memory_taxonomy_tag` + `memory_entity_mention` tables
- `0047_memory_import.sql` — `memory_import` table
- `0048_memory_compaction.sql` — `memory_compaction_run` table
- `0049_memory_dedup.sql` — `memory_dedup_reviewed` table

Note: `memory_entity` (L2) and `memory_taxonomy_node` (L0) already exist from task 003 and 013. `memory_source` is L4 (references `chat_session`) and is created in its own migration in the L4 layer. This task covers only the L3 tables.

**`memory` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `project_id uuid references project(id) on delete set null` — nullable; project-scoped memory
- `project_task_id uuid references project_task(id) on delete set null` — nullable; task-scoped memory
- `agent_id uuid references agent(id) on delete set null` — nullable; agent-private memory
- `memory_type text not null check (memory_type in ('episodic','semantic','procedural','preference','entity_profile','execution_summary'))` — doc 06 memory types
- `scope text not null check (scope in ('org','project','task','agent'))` — determines retrieval scope; not always derived from which FKs are set (a promoted memory may change scope without changing FKs)
- `content text not null` — plain text content of the memory
- `content_hash text not null` — SHA-256 of `content` for exact-duplicate detection
- `embedding vector(1536)` — pgvector embedding; null until embedding job runs
- `confidence numeric(4,3) not null default 0.500 check (confidence >= 0 and confidence <= 1)` — 0.0–1.0 range
- `utility_score numeric(4,3) not null default 0.500 check (utility_score >= 0 and utility_score <= 1)` — separate from confidence; used for retention decisions
- `extraction_score integer check (extraction_score >= 0 and extraction_score <= 100)` — Stage 2 score (0–100); threshold 40 required to graduate from candidate
- `status text not null check (status in ('candidate','active','superseded','archived'))` default `'candidate'`
- `is_hardened boolean not null default false` — hardened after 7-day hold + positive friction signals; hardened memories require stronger evidence to supersede
- `sensitivity text not null check (sensitivity in ('normal','restricted'))` default `'normal'` — 'restricted' memories excluded from passive injection
- `trust_tier numeric(3,2) not null default 0.80 check (trust_tier >= 0 and trust_tier <= 1)` — 0.0–1.0; capped by source trust tier at extraction time
- `file_backed boolean not null default false` — true if this memory is backed by a file in the repo
- `file_path text` — relative path within the project repo (non-null only when `file_backed=true`)
- `file_last_scanned_at timestamptz` — last time the backing file was checked for freshness
- `superseded_by uuid references memory(id)` — self-ref; null unless this memory was superseded
- `superseded_at timestamptz` — null unless superseded
- `archived_at timestamptz` — null unless archived
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id, status, scope)` — primary retrieval filter
- Index: `(project_id, status) WHERE project_id IS NOT NULL`
- Index: `(agent_id, status) WHERE agent_id IS NOT NULL`
- Index: `(content_hash, organization_id)` — exact-duplicate detection
- Index: `(status, file_backed) WHERE file_backed = true` — freshness scan
- pgvector HNSW index: `USING hnsw (embedding vector_cosine_ops)` with `m=16, ef_construction=64` — created CONCURRENTLY after table creation to avoid blocking migrations

**`memory_taxonomy_tag` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `memory_id uuid not null references memory(id) on delete cascade`
- `taxonomy_node_id uuid not null references memory_taxonomy_node(id) on delete cascade`
- `assigned_by text not null check (assigned_by in ('extraction','manual','inference'))` — how the tag was assigned
- `confidence numeric(4,3) not null default 1.000` — assignment confidence
- `created_at timestamptz not null default now()`
- Unique index: `(memory_id, taxonomy_node_id)` — one tag per node per memory

**`memory_entity_mention` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `memory_id uuid not null references memory(id) on delete cascade`
- `entity_id uuid not null references memory_entity(id) on delete cascade`
- `mention_text text not null` — the raw text span that triggered this mention (for context)
- `confidence numeric(4,3) not null default 1.000`
- `created_at timestamptz not null default now()`
- Unique index: `(memory_id, entity_id)` — one mention record per entity per memory

**`memory_import` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `requested_by uuid references human_user(id) on delete set null`
- `status text not null check (status in ('pending','processing','completed','failed'))` default `'pending'`
- `file_key text not null` — object storage key of the uploaded JSONL zip file
- `total_records integer` — null until processing begins
- `processed_records integer not null default 0`
- `imported_records integer not null default 0` — records that passed extraction threshold
- `rejected_records integer not null default 0`
- `error_message text` — null unless status='failed'
- `started_at timestamptz`
- `completed_at timestamptz`
- `created_at timestamptz not null default now()`
- Index: `(organization_id, status)`

**`memory_compaction_run` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `run_type text not null check (run_type in ('sleep_reflection','task_consolidation','manual'))` — what triggered the compaction
- `scope_context jsonb not null default '{}'` — e.g. `{project_id: "...", task_id: "..."}` for task_consolidation runs
- `status text not null check (status in ('pending','running','completed','failed'))` default `'pending'`
- `memories_examined integer not null default 0`
- `memories_updated integer not null default 0`
- `memories_archived integer not null default 0`
- `memories_created integer not null default 0` — new synthesis memories
- `error_message text`
- `started_at timestamptz`
- `completed_at timestamptz`
- `created_at timestamptz not null default now()`
- Index: `(organization_id, run_type, created_at DESC)`

**`memory_dedup_reviewed` table** (doc 06):
- `id uuid primary key default gen_random_uuid()`
- `memory_id_a uuid not null references memory(id) on delete cascade`
- `memory_id_b uuid not null references memory(id) on delete cascade`
- `cosine_similarity numeric(5,4) not null` — cosine similarity score that triggered the dedup review
- `decision text not null check (decision in ('keep_both','merge','supersede_a','supersede_b','deferred'))` — outcome of dedup review
- `reviewed_by text not null check (reviewed_by in ('llm','human'))` — who made the decision
- `reviewed_at timestamptz not null default now()`
- `created_at timestamptz not null default now()`
- Check constraint: `memory_id_a <> memory_id_b` — no self-dedup
- Unique index: `(LEAST(memory_id_a::text, memory_id_b::text), GREATEST(memory_id_a::text, memory_id_b::text))` — prevents recording the same pair twice regardless of A/B order

**Repository layer:**
- `MemoryRepo`: `Create`, `GetByID`, `UpdateStatus`, `UpdateEmbedding`, `UpdateConfidence`, `ListForRetrieval`, `ListCandidates`, `GetByContentHash`, `ListFileBacked`, `Archive`, `Supersede`
  - `ListForRetrieval(ctx, filter RetrievalFilter) ([]Memory, error)` — accepts scope/status/sensitivity filters; used by retrieval pipeline
  - `ListCandidates(ctx, orgID uuid.UUID, olderThan time.Time) ([]Memory, error)` — returns `status='candidate'` rows older than threshold for 7-day promotion evaluation
- `MemoryTaxonomyTagRepo`: `Create`, `ListByMemory`, `DeleteByMemory`
- `MemoryEntityMentionRepo`: `Create`, `ListByMemory`, `ListByEntity`
- `MemoryImportRepo`: `Create`, `GetByID`, `UpdateStatus`, `UpdateProgress`
- `MemoryCompactionRunRepo`: `Create`, `GetByID`, `UpdateStatus`, `UpdateCounts`
- `MemoryDedupReviewedRepo`: `Create`, `GetByPair`, `ListPendingReview`

### Must NOT build
- Extraction pipeline logic (task 039)
- Retrieval pipeline logic (task 040)
- Compaction/import service logic (task 041)
- Memory API endpoints (task 042)
- `memory_source` table (L4 — references `chat_session`; task TBD in L4)
- `memory_entity` table (already created at L2 in task 013)

## Acceptance Criteria

- [ ] All 5 migrations apply cleanly in order; rolling back the last migration does not break the previous ones
- [ ] `memory` table: pgvector HNSW index created; `embedding` column accepts `vector(1536)` values
- [ ] `content_hash` unique-per-org: two `memory` rows with identical `content_hash` and same `organization_id` are allowed (not unique constraint) — duplicate detection is application-layer (exact duplicates get `status='superseded'`)
- [ ] `memory_dedup_reviewed` pair uniqueness: inserting `(A, B)` and then `(B, A)` for the same pair returns unique constraint violation on the second insert
- [ ] `memory.superseded_by` self-reference: update `superseded_by` to point to a different memory in the same org; cascade delete not triggered (parent is not deleted)
- [ ] `MemoryRepo.ListForRetrieval` with `sensitivity='normal'` filter excludes `sensitivity='restricted'` rows
- [ ] pgvector cosine similarity query executes against HNSW index (verify via `EXPLAIN` in integration test: index scan used)
- [ ] `memory_import.requested_by` FK set to null on user deletion (ON DELETE SET NULL); import record preserved

## Tests Required

**Unit tests:**
- `MemoryRepo.GetByContentHash`: insert two memories with different content_hash values; query each hash → correct row returned; query unknown hash → not found
- `MemoryDedupReviewedRepo`: insert pair (A, B); attempt insert (B, A) → unique violation error
- `ListCandidates`: insert 3 candidate rows with different `created_at` values; call with threshold of 7 days → returns only rows older than threshold

**Integration tests:**
- pgvector round-trip: insert memory with embedding `[0.1, 0.2, ...]` (1536 dims); retrieve by ID; embedding matches (within float precision)
- HNSW index scan: insert 100 memory rows with random embeddings; run `SELECT ... ORDER BY embedding <=> $1 LIMIT 10`; verify `EXPLAIN` shows index scan
- Supersession chain: insert memory A; insert memory B with `superseded_by=A.id`; `GetByID(A.id).superseded_by = B.id`; soft delete does not cascade
- Dedup pair constraint: verify both orderings of the same UUID pair are rejected as duplicates

**E2E tests:**
- None — covered by dedicated E2E task 075 and 086

## Implementer Notes

- The `embedding vector(1536)` column uses OpenAI-compatible embedding dimensions. If the configured embedding provider returns a different dimension, the column DDL must be updated. V2 assumes 1536 dimensions throughout the memory subsystem. Document this assumption in a DDL comment.
- The HNSW index is created with `CREATE INDEX CONCURRENTLY` to avoid a table lock during migration. Because `CREATE INDEX CONCURRENTLY` cannot run inside a transaction block, the migration runner (task 002) must detect HNSW index creation statements and execute them outside the transaction envelope. This is a known edge case for the migration runner — document it with a `-- HNSW_INDEX: must run outside transaction; migration runner handles this automatically` comment on the index DDL.
- `memory.scope` is a denormalized field: it should reflect the most specific scope of the memory at query time. However, after scope promotion (e.g., a task-scoped memory is promoted to project scope after task completion), `scope` is updated directly. The `project_id` and `project_task_id` FKs may still be set even after scope promotion — they track provenance, not current scope. Retrieval queries filter on `scope`, not on FK nullity.
- `memory_source` (which records which chat session, message, or import produced a memory) is L4 because it has a soft FK to `chat_session`. It is not in this migration set. This task's repositories should include a stub `GetSources(ctx, memoryID) ([]MemorySource, error)` that returns an error `"not yet implemented — see task 075"` so callers in the extraction and retrieval pipelines have the interface defined before the L4 task ships.
- The `memory_entity` table (L2) is expected to already exist when these migrations run. If running migrations in order, task 013 (L2 agent schema) creates `memory_entity`. If `memory_entity` does not exist when `0045_memory.sql` runs, migration will fail. This ordering dependency must be documented in the migration file header comment.
