# Memories: Vector Embedding

> Summary: Embedding providers, dimensions, experimental findings, and configuration. Includes head-to-head benchmark results.
> Last updated: 2026-02-16
> Audience: Agents tuning retrieval quality and embedder reliability.

## Providers

Embedder code: `internal/memory/embedder.go`

Supported providers:
- Ollama (default in local flows)
- OpenAI-compatible endpoint

## Experimental Findings (E12: Embedding Model Comparison)

Head-to-head benchmark on 20 retrieval queries against 12,000+ memories:

```
Model                           | Hit Rate | Avg Recall | Avg Top-1 Sim
--------------------------------|----------|------------|-------------
nomic-embed-text 768d (old)     |   60%    |   46.2%    |   0.736
text-embedding-3-small 768d     |   50%    |   32.9%    |   0.570
text-embedding-3-small 1536d    |   80%    |   52.9%    |   0.658
```

### Key Findings

1. **1536d is the clear winner.** 80% hit rate vs 60% for nomic baseline.
2. **Never truncate OpenAI embeddings to 768d.** Truncation makes them WORSE than nomic — 50% vs 60%. The information lost in truncation destroys retrieval quality.
3. **Similarity scores are not comparable across models.** Nomic has higher raw similarity (0.736) but worse actual retrieval. Don't use similarity thresholds calibrated on one model for another.

### Implication for Ellie

Ellie's embedding worker and retrieval cascade must use 1536d embeddings. The `embedding_1536` column is the primary search column. The 768d `embedding` column exists for backward compat but should not be used for search.

## Current Configuration

| Setting | Value |
|---|---|
| Primary model | OpenAI `text-embedding-3-small` |
| Primary dimension | 1536 |
| Primary column | `embedding_1536` (vector(1536)) |
| Legacy column | `embedding` (vector(768)) — do not use for search |
| Fallback | Keyword-only when embedding unavailable |

## Key Defaults (from `internal/config/config.go`)

Current defaults still reference Ollama. These should be updated to reflect the proven configuration:

- Provider: `ollama` → **should be `openai`**
- Model: `nomic-embed-text` → **should be `text-embedding-3-small`**
- Dimension: `768` → **should be `1536`**
- Ollama URL: `http://localhost:11434`

## Workers Using Embeddings

- Conversation embedding worker
- Ellie retrieval cascade (query embedding path)
- Ellie proactive context injection worker
- File scanner (new — embeds file-extracted memories)
- Memory MD indexer (new — embeds agent memory files)

Server wiring lives in `cmd/server/main.go`.

## Failure Strategy

- Retry/fallback behavior exists in embedder paths.
- Retrieval falls back to keyword-only when query embedding is unavailable.
- Ingestion fallback remains deterministic when LLM extraction fails.

## Reembedding Scripts (extraction repo)

When changing embedding models, these scripts handle bulk reembedding:
- `reembed-openai.mjs` — Re-embed all memories with OpenAI 768d
- `reembed-openai-1536.mjs` — Re-embed all memories with OpenAI 1536d
- `backfill-embeddings.mjs` — Backfill missing embeddings only

## Related Docs

- `docs/memories/recall.md`
- `docs/memories/ongoing-ingest.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Major rewrite with E12 benchmark results, 1536d adoption, and "never truncate" finding.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
