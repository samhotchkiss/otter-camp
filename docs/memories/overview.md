# Memories: Overview

> Summary: End-to-end memory architecture — capture, embedding, retrieval, injection, evaluation — with experimental findings that should drive Ellie's implementation.
> Last updated: 2026-02-16
> Audience: Agents building/debugging Ellie + memory behavior.

## System Shape

Otter Camp has two related memory layers:
- **Agent-scoped memory entries** (`memory_entries`, managed via `MemoryStore`)
- **Ellie knowledge memories** (`memories`, used by retrieval cascade and proactive injection)

Primary code:
- API: `internal/api/memory.go`
- Agent memory store: `internal/store/memory_store.go`
- Ellie ingestion: `internal/memory/ellie_ingestion_worker.go`
- Ellie retrieval: `internal/memory/ellie_retrieval_cascade.go`
- Ellie injection: `internal/memory/ellie_context_injection_worker.go`

## Runtime Pipeline

1. Chat and project conversation data lands in conversation tables.
2. Embedding worker generates vectors for eligible content.
3. Segmentation worker groups chat messages into conversations.
4. Ellie ingestion extracts structured memories (LLM path + deterministic fallback).
5. **Entity synthesis** consolidates scattered facts into definitional memories (biggest retrieval win — see below).
6. **File-backed ingestion** captures current-state knowledge from workspace files.
7. Retrieval cascade serves memory context — strategy depends on query type (see `recall.md`).
8. Proactive injection worker pushes context back into rooms when relevance threshold is met.

## What We've Proven (Extraction Sprint, Feb 14-16 2026)

These findings come from a 48-hour experiment sprint on 13,000+ memories. They should be treated as design constraints for Ellie.

### The Retrieval Stack, Ranked by Impact

| Improvement | Impact | Status |
|---|---|---|
| **Entity synthesis** | 80% → 95% hit rate (+15pp) | ✅ Proven. 30 synthesized memories. |
| **1536d OpenAI embeddings** | 60% → 80% hit rate (+20pp) | ✅ Proven. Replaced nomic 768d. |
| **File-backed memories** | +36pp on current-state queries | ✅ Proven. 782 memories from files. |
| **LLM dedup** | 17,651 → 12,492 active (29% reduction) | ✅ Proven. Cleaner retrieval. |
| **Query routing** | Category-dependent strategy selection | 🔲 Designed, not implemented. |
| Hybrid BM25+vector | Regressed from 80% → 75% | ❌ Not worth it for this dataset. |
| Importance weighting | -4pp overall | ❌ Hurts. Don't use. |

### What This Means for Ellie

1. **Entity synthesis must be a core pipeline step.** Scattered conversation facts about a topic need to be periodically consolidated into rich definitional memories. This is the single biggest retrieval improvement.
2. **Use 1536d embeddings. Never truncate to 768d.** OpenAI `text-embedding-3-small` at 1536d outperforms nomic and its own 768d truncation.
3. **File-backed memories fill the recency gap.** Conversation extraction is frozen at ingestion time. Files get updated. For "what's happening now" queries, file-backed memories are essential.
4. **Pure vector search beats hybrid BM5+vector.** BM25 noise dilutes good vector matches. Keep it simple.
5. **No single strategy wins everywhere.** Need a query router (see `recall.md`).

## Memory Safety Controls

- Org scoping via workspace middleware and DB session context
- Sensitivity fields on memory/conversation paths
- Status lifecycle for memories (active/warm/archived or active/deprecated/archived by table)
- Source references (`source_conversation_id`, `source_project_id`) for traceability
- File-backed memories include `file_path`, `file_content_hash`, `file_mtime` for staleness detection

## Current DB State (as of 2026-02-16)

- **13,359 active memories** (was 17,651 before dedup)
- **8,674 deprecated** (mostly dedup kills)
- Primary embedding: OpenAI `text-embedding-3-small` 1536d
- Memory kind distribution: fact (4911), context (3390), technical_decision (2181), process_decision (893), preference (673), lesson (442)
- File-backed: 630 (file_scanner) + 125 (memory_md_indexer) + 27 (project_summarizer)
- Synthesized entities: 30

## Related Docs

- `docs/memories/initial-ingest.md` — Migration and bootstrap
- `docs/memories/ongoing-ingest.md` — Continuous ingestion behavior
- `docs/memories/vector-embedding.md` — Embedding providers and experimental findings
- `docs/memories/recall.md` — Retrieval cascade and query routing
- `docs/memories/entity-synthesis.md` — Consolidation of scattered facts
- `docs/memories/file-backed-memories.md` — File scanning and freshness
- `docs/memories/taxonomy-structure.md` — Memory kinds, status, sensitivity
- `docs/memories/ellies-role.md` — Ellie's responsibilities
- `docs/memories/dedup.md` — Deduplication strategies and results
- `docs/memories/experiment-log.md` — Full experiment history
- `docs/memories/open-questions-and-wip.md` — Remaining work
- `docs/memories/secret-management.md` — Secrets and environment

## Change Log

- 2026-02-16: Major rewrite incorporating extraction sprint findings (E01-E13), file-backed memories, entity synthesis results, and retrieval benchmark data.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
