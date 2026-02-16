# Memories: Experiment Log

> Summary: Complete record of all memory system experiments, findings, and queued work.
> Last updated: 2026-02-16
> Audience: Agents continuing memory system development.

## Experiment Sprint Context

48-hour sprint (Feb 14-16 2026) on the extraction pipeline repo at `~/Documents/SamsBrain/OtterCamp/extraction/`. Ground truth: Day 1 (Jan 26) benchmark — 38 human-written claims against which extraction quality is measured.

The canonical raw experiment log lives at `extraction/EXPERIMENT-LOG.md`. This doc summarizes findings relevant to Ellie's design.

## Completed Experiments

### E01: Draft Wrapper Reclassification ✅
- Fixed: queued-announce wrappers being extracted as user biography
- 2 memories correctly reclassified (the exact ones GPT flagged as false)

### E02: Tighter Semantic Dedup (0.92 → 0.88) ✅
- 272 additional dupes found (9% reduction in 3K sample)
- Pure similarity insufficient — led to LLM dedup (E13)

### E03: Prompt Tuning ✅ (partial)
- Added file path/URL extraction, queued-wrapper awareness, multi-human awareness
- Validates on next fresh extraction run

### E04: Score Distribution Analysis ✅
- Bimodal distribution: peaks at 55-59 and 65-69
- Threshold 40 is appropriate
- Cross-validation: 42% strong, 55% weak, 3% missed
- Finding: extraction misses specific file paths/artifact locations
- Finding: 0.75 similarity threshold for "captured" may be too strict

### E10: Retrieval Quality Benchmark ✅ (the big one)

Four rounds tell the story of retrieval improvement:

```
Round 1 (nomic 768d baseline):           60% hit rate, 46.2% recall
Round 2 (post-dedup, same embeddings):   60% hit rate, 46.2% recall
Round 3 (OpenAI 1536d):                  80% hit rate, 52.9% recall
Round 4 (+ entity synthesis):            95% hit rate, 69.6% recall
```

**Key: dedup didn't help retrieval. 1536d embeddings helped a lot. Entity synthesis helped the most.**

### E12: Embedding Model Comparison ✅
- nomic 768d: 60% hit rate (baseline)
- OpenAI 768d (truncated): 50% — WORSE than nomic. Never truncate.
- OpenAI 1536d: 80% — clear winner.

### E13: LLM Dedup ✅
- 17,651 → 12,492 active memories (29% reduction)
- 5,159 deprecated via LLM review, 163 merged
- 26,395 pairs reviewed, zero false kills
- See `docs/memories/dedup.md` for details.

### Hybrid BM25 + Vector ✅ (tested, rejected)
- Hybrid RRF: 75% — regressed from 80% vector-only
- BM25 alone: 55%
- BM25 helps exact keywords but hurts semantic queries
- **Not worth the complexity.**

### Entity Synthesis ✅
- 30 definitional memories created
- 80% → 95% hit rate
- See `docs/memories/entity-synthesis.md` for details.

### File-Backed Memories Suite ✅
- 782 memories from 3 tools (file scanner, project summarizer, memory md indexer)
- +36pp on current-state, +33pp on preferences, -29pp on projects when used alone
- See `docs/memories/file-backed-memories.md` for details.

### Bridge Gap Investigation ✅
- `chat_messages` ingestion stopped after Feb 10
- Feb 11-15: all Slack conversations missing from DB
- Data exists in OpenClaw JSONL files but pipeline doesn't read them
- This is the #1 data gap — see `docs/memories/ongoing-ingest.md`

## Queued Experiments (Not Yet Run)

| ID | Experiment | Expected Impact |
|---|---|---|
| E05 | Window size optimization (10/20/30/50 msgs) | Extraction quality |
| E11 | Haiku vs Sonnet extraction quality comparison | Extraction quality |
| — | Cross-encoder re-ranking (expensive model re-scores top-20) | Retrieval precision |
| — | Fact conflict resolution (newer facts invalidate older) | Correctness |
| — | JSONL direct ingestion (bypass bridge) | Bridge gap fix |
| — | Query router (Haiku classifies → routes to strategy) | Retrieval across all categories |

## Key Scripts (extraction repo)

| Script | Purpose |
|---|---|
| `ingest.mjs` | Main orchestrator (chat_messages → Stage 1→2→2.5→DB) |
| `pipeline.mjs` | Stage 1 extraction (LLM, window-based) |
| `stage2v2.mjs` | Scoring/filtering |
| `stage25-projects.mjs` | LLM normalization |
| `retrieval-benchmark.mjs` | 20-query benchmark |
| `retrieval-v2.mjs` | Best retrieval strategy (combined BM25+vector+Haiku) |
| `memory-qa-test.mjs` | Multi-query retrieval + Sonnet QA |
| `reembed-openai-1536.mjs` | Re-embed all memories at 1536d |
| `llm-dedup.mjs` | Haiku-powered dedup |
| `synthesize-entities.mjs` | Entity synthesis |
| `file-scanner.mjs` | File → memory ingestion |
| `run-all-experiments.mjs` | 6-strategy benchmark suite |

## Related Docs

- `docs/memories/overview.md`
- Full raw log: `extraction/EXPERIMENT-LOG.md`
- Full improvement report: `extraction/IMPROVEMENT-REPORT.md`

## Change Log

- 2026-02-16: Created canonical experiment summary from extraction sprint findings.
