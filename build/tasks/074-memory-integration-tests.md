# 074: Memory Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1-2 days) |
| Spec refs | doc 06 §ExtractionPipeline, doc 06 §RetrievalPipeline, doc 06 §DedupDetection, doc 06 §EntitySynthesis, doc 06 §CompactionTrigger, doc 06 §ImportPipeline, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 038, 039, 040, 041, 042 |
| Blocks | 089 |

## Scope

Integration test suite for the memory domain: extraction pipeline stages with a mock LLM
extractor, pgvector similarity retrieval, dedup detection, entity normalization,
compaction triggers, and the JSONL import pipeline. All tests use a real PostgreSQL
database (including the pgvector extension) via `testdb.New(t)`. The LLM extractor is
replaced with a deterministic fixture-based mock; no real model provider calls are made.

### Must build

**Test file:** `internal/memory/extraction_integration_test.go`

**Test file:** `internal/memory/retrieval_integration_test.go`

**Test file:** `internal/memory/compaction_integration_test.go`

**Test file:** `internal/memory/import_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/memory.go`:
- `MakeMemory(t, db, orgID, opts)` — inserts a `memory` row with optional embedding vector;
  returns `*memory.Memory`
- `MakeMemoryEntity(t, db, orgID)` — creates a `memory_entity` row; returns ID
- `MockLLMExtractor(fixtures)` — returns a `memory.Extractor` implementation that returns
  scripted extraction results without making model API calls
- `RandomEmbedding(dims int)` — returns a random unit vector of length `dims` for test
  embedding rows

**Test scenarios in extraction_integration_test.go:**

`TestExtraction_Stage0_GarbageRejection` — submit a message that is deterministically
garbage (e.g., pure punctuation, empty string, known behavioral override pattern
"Ignore all previous instructions..."); extraction pipeline Stage 0 rejects it; no
`memory` row created.

`TestExtraction_Stage1_LLMExtraction` — submit a well-formed message; mock extractor
returns a fixture with 2 memory candidates; Stage 1 produces 2 candidate rows with
`is_hardened=false`, `confidence=0` (candidates start unhardened).

`TestExtraction_Stage2_ScoreThreshold` — mock extractor returns a candidate with score=35
(below threshold of 40); candidate is dropped; no memory row created. Second candidate
with score=45 passes; one memory row in DB.

`TestExtraction_Stage3_Normalization` — mock extractor returns entity name "sam smith";
Stage 3 normalizes to "Sam Smith"; `memory_entity` row uses normalized name; duplicate
entity lookup by name (case-insensitive) matches existing entity.

`TestExtraction_Stage4_EmbedAndStore` — mock extractor returns candidate with content;
after Stage 4, `memory` row has non-null `embedding` vector(1536); `content_hash` set;
`memory_source` row created with correct `source_type`.

`TestExtraction_TrustTierCapping` — create temp agent (trust tier lower); extraction via
that agent caps memory confidence at trust tier ceiling; resulting memory row has
`confidence` no higher than the tier cap.

`TestExtraction_CandidateHold` — extraction produces candidate with is_hardened=false;
7 days pass (clock.Fake); compaction job promotes candidates with positive friction
signals; candidate becomes active (is_hardened=true).

**Test scenarios in retrieval_integration_test.go:**

`TestRetrieval_ScopeFilter` — insert memories for org A (project P1) and org A (project
P2); retrieval query scoped to P1; only P1 memories returned (scope filter is hard WHERE,
not ranked); P2 memories not surfaced even if more similar.

`TestRetrieval_VectorSimilarity` — insert 5 memory rows with known embeddings; query
with a vector close to memories 2 and 4; results ranked by cosine similarity; memories
2 and 4 appear in top positions. Uses real pgvector `<=>` operator.

`TestRetrieval_FallbackToFullCorpus` — insert 2 memories in scope; query returns them
both; if fewer than 3 results, fallback to full-corpus retrieval kicks in (adds memories
from broader org scope); assert fallback_used flag or additional results present.

`TestRetrieval_SensitivityGating` — insert memory with `sensitivity='restricted'`; passive
injection query (mode='passive_injection') does NOT return it; direct agent query
(mode='agent_query') DOES return it (sensitivity gating only blocks passive injection).

`TestRetrieval_InjectionOrdering` — retrieval returns N memories; assert they are ordered
most-relevant-last (attention-aware ordering for prompt injection: least relevant is first
so most relevant is freshest in context window).

`TestRetrieval_EntitySynthesis` — insert 3 memory rows all mentioning entity "Project Alpha";
run entity synthesis; a `memory_entity` row for "Project Alpha" exists with aggregated
metadata from the 3 memories; `memory_entity_mention` rows link entity to each memory.

`TestDedup_CosinePrescreenAndLLM` — insert memory M1; attempt to insert M2 with cosine
similarity ≥ 0.88 to M1; dedup pre-screen flags M2 as potential duplicate; mock LLM
dedup cluster judgment returns "duplicate"; M2 is NOT stored as a new memory; M1 is kept
and confidence score is updated (reinforced).

`TestDedup_CosineBelowThreshold` — M2 with cosine similarity 0.75 to M1; pre-screen does
not flag; M2 stored normally as a new memory row.

`TestContradiction_Detection` — insert memory M1 "Project Alpha launches in March"; insert
M2 "Project Alpha launches in June"; mock extractor flags contradiction; M1 gets
`superseded_by=M2.id`; M2 is active; M1 is superseded (not deleted).

**Test scenarios in compaction_integration_test.go:**

`TestCompaction_TaskCompletion_ScopePromotion` — create project_task with memories scoped
to that task; emit `task.completed` event; compaction service runs scope consolidation;
memories with `task_id` set gain a `project_id` copy (scope promotion to project level);
original task-scoped memories remain.

`TestCompaction_EpisodicDistillation` — task completion compaction runs distillation mock;
produces a new `memory` row with `memory_type='episodic_summary'`; links to original
memories via supersession chain.

`TestCompaction_DecayHalfLife` — episodic memories older than half-life window have
`confidence` reduced; use `clock.Fake` to advance time past half-life; run decay job;
assert confidence values are reduced.

**Test scenarios in import_integration_test.go:**

`TestImport_JSONL_ValidFile` — POST /v1/memory/import with a valid JSONL zip file (from
`testdata/import/valid_sample.jsonl.zip`); `memory_import` row created with
`status='processing'`; after processing completes, `status='completed'`;
`processed_count` matches number of JSONL lines; each imported memory has
`source_type='memory_import'` and `trust_tier=0.6`.

`TestImport_JSONL_InvalidRecord` — JSONL file with 3 valid records and 1 malformed JSON
line; import completes with `error_count=1`; 3 memories imported successfully; error
detail recorded in `memory_import.error_log`.

`TestImport_JSONL_StatusTracking` — GET /v1/memory/imports/:id while import is processing
returns `status='processing'` with `processed_count` incrementing; after completion
returns `status='completed'`.

### Must NOT build

- E2E tests for memory pipeline (task 086)
- Tests that make real model provider API calls (all LLM calls use mock extractor)
- Tests for prompt assembly / memory injection into turns (task 083)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/memory/... -tags integration`
- [ ] pgvector extension is active in testdb; `TestRetrieval_VectorSimilarity` uses real `<=>` operator
- [ ] `TestExtraction_Stage0_GarbageRejection` tests at least 3 distinct garbage patterns
- [ ] `TestDedup_CosinePrescreenAndLLM` asserts that the superseded memory M1 still exists in the DB (not deleted)
- [ ] `TestImport_JSONL_ValidFile` uses fixture file at `testdata/import/valid_sample.jsonl.zip`; fixture must be committed with the task
- [ ] `TestCompaction_TaskCompletion_ScopePromotion` verifies scope promotion at the DB level (checks project_id column set on memory rows)
- [ ] All LLM calls use `MockLLMExtractor` with scripted fixture responses; no real HTTP calls to model providers

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestExtraction_Stage0_GarbageRejection`
- `TestExtraction_Stage1_LLMExtraction`
- `TestExtraction_Stage2_ScoreThreshold`
- `TestExtraction_Stage3_Normalization`
- `TestExtraction_Stage4_EmbedAndStore`
- `TestExtraction_TrustTierCapping`
- `TestExtraction_CandidateHold`
- `TestRetrieval_ScopeFilter`
- `TestRetrieval_VectorSimilarity`
- `TestRetrieval_FallbackToFullCorpus`
- `TestRetrieval_SensitivityGating`
- `TestRetrieval_InjectionOrdering`
- `TestRetrieval_EntitySynthesis`
- `TestDedup_CosinePrescreenAndLLM`
- `TestDedup_CosineBelowThreshold`
- `TestContradiction_Detection`
- `TestCompaction_TaskCompletion_ScopePromotion`
- `TestCompaction_EpisodicDistillation`
- `TestCompaction_DecayHalfLife`
- `TestImport_JSONL_ValidFile`
- `TestImport_JSONL_InvalidRecord`
- `TestImport_JSONL_StatusTracking`

**E2E tests:** None — covered by task 086.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL + pgvector: real, via `testdb.New(t)`
- LLM extraction (Stage 1): `MockLLMExtractor` with scripted fixture responses
- LLM dedup cluster judgment: `MockLLMExtractor` (same interface, scripted)
- LLM entity synthesis: `MockLLMExtractor`
- Embeddings: pre-computed fixtures (random unit vectors stored in testdata); no real
  embedding API calls
- Clock: injected `clock.Fake` for decay half-life and candidate hold tests

**Test fixtures required:**
- `testdata/import/valid_sample.jsonl.zip` — 10 valid JSONL memory records; commit this file
- `testdata/import/invalid_record.jsonl.zip` — 3 valid + 1 malformed record; commit this file
- `testdata/embeddings/sample_vectors.json` — 20 pre-computed 1536-dim unit vectors for
  retrieval ranking tests; commit this file

**ISSUES #9, #10 (memory_source soft references):**
`TestExtraction_Stage4_EmbedAndStore` verifies `memory_source` row creation. The
`session_id` column is a soft reference (no SQL FK per ISSUE #9). Test must NOT assert
a FK constraint; instead assert the UUID value is set correctly.

**pgvector index:**
Tests that use vector similarity require the pgvector index to exist. The `testdb.New(t)`
setup runs all migrations (including pgvector index creation from task 038). If migrations
are slow, consider using `HNSW_EF_SEARCH=1` for test environments to reduce index
overhead.
