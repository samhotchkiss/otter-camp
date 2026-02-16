# Issue #160: Update Embedder Defaults to OpenAI 1536d

> Priority: P0 (blocks all other memory work)
> Blocked by: nothing
> Blocks: #161, #162, #163, #164

## Summary

Change embedding defaults from Ollama nomic-embed-text 768d to OpenAI text-embedding-3-small 1536d. This is the proven winner from benchmark testing (60% → 80% hit rate).

## Background

Benchmark E12 tested three embedding configurations:
- nomic-embed-text 768d: 60% hit rate, 46.2% recall
- text-embedding-3-small 768d (truncated): 50% hit rate — WORSE than nomic
- text-embedding-3-small 1536d: 80% hit rate, 52.9% recall

The 1536d configuration is the clear winner and must be the default for all new installs and migrations.

## Requirements

### 1. Update config defaults
**File:** `internal/config/config.go`

Change defaults:
```
Provider:  "ollama"  → "openai"
Model:     "nomic-embed-text" → "text-embedding-3-small"
Dimension: 768 → 1536
```

### 2. Update embedder to use 1536d column
**File:** `internal/memory/embedder.go`

Ensure the embedder writes to a 1536-dimension vector column. If the DB schema currently only has a 768d `embedding` column, add a migration for `embedding_1536 vector(1536)`.

### 3. Update retrieval to search 1536d column
**Files:**
- `internal/memory/ellie_retrieval_cascade.go`
- `internal/store/ellie_retrieval_store.go`

Query embedding and similarity search must use the 1536d column, not the 768d column.

### 4. Add migration if needed
**Dir:** `migrations/`

If a new column is needed:
```sql
ALTER TABLE memories ADD COLUMN IF NOT EXISTS embedding_1536 vector(1536);
CREATE INDEX IF NOT EXISTS idx_memories_embedding_1536 ON memories USING ivfflat (embedding_1536 vector_cosine_ops) WITH (lists = 100);
```

### 5. Require OpenAI API key for embedding
**File:** `internal/config/config.go`

Add `CONVERSATION_EMBEDDER_OPENAI_API_KEY` as a required env var when provider is openai. Fail fast on startup if missing.

## Acceptance Criteria

- [ ] Default embedding config is openai / text-embedding-3-small / 1536d
- [ ] New memories are embedded at 1536 dimensions
- [ ] Retrieval queries use 1536d embeddings for similarity search
- [ ] Migration adds 1536d column and index if not present
- [ ] Startup fails with clear error if OpenAI API key is missing and provider is openai
- [ ] Existing tests pass (update any hardcoded 768d expectations)
- [ ] Ollama/768d still works if explicitly configured (backward compat)

## Do NOT

- Remove the 768d column or Ollama provider support — they're backward compat
- Add BM25 or hybrid search — proven to regress results
- Change similarity thresholds without benchmarking — thresholds are model-dependent

## Test Plan

1. Unit: embedder returns 1536d vectors when configured for openai
2. Unit: retrieval queries use 1536d column
3. Integration: full embed → store → retrieve roundtrip at 1536d
4. Migration: runs cleanly on fresh DB and on DB with existing 768d data
