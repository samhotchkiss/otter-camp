# Memories: Deduplication

> Summary: LLM-powered deduplication strategy, results, and lessons learned.
> Last updated: 2026-02-16
> Audience: Agents working on memory quality and DB hygiene.

## Why Dedup Matters

At 17,651 memories, near-duplicates flood top-k retrieval results. When 5 of your top-10 results say the same thing slightly differently, you waste context window and miss diverse relevant memories.

## Approach: LLM Dedup (`llm-dedup.mjs`)

Semantic similarity alone can't distinguish "same fact, different words" from "related but distinct facts." We use Haiku to make the judgment call.

### Process
1. Find candidate pairs above similarity threshold
2. Cluster connected pairs
3. LLM (Haiku) reviews each cluster: keep best, deprecate rest, optionally merge
4. Track reviewed pairs in `dedup_reviewed` table to avoid re-reviewing

### Results

| Round | Clusters | Deprecated | Merged |
|---|---|---|---|
| Round 1 | 2,228 | 923 | 163 |
| Round 2 | 1,786 + 322 | ~1,736 | — |
| **Total** | — | **5,159** | **163** |

**Before:** 17,651 active memories
**After:** 12,492 active memories (29% reduction)
**Total deprecated (all methods):** 8,674

### Operational Notes
- 26,395 pairs reviewed via `dedup_reviewed` table (zero rework)
- 5 SIGKILL events during long runs — all recovered cleanly due to cursor-based progress tracking
- Dedup did NOT improve retrieval hit rate (still 60% pre-reembedding) — but it improved result diversity

## Earlier Approach: Semantic Threshold (E02)

Before LLM dedup, we tested tighter cosine similarity thresholds:
- Baseline threshold: 0.92
- Test threshold: 0.88
- Result: 272 additional dupes found (9% reduction in 3K sample)
- Problem: Pure similarity can't distinguish "same fact" from "related facts"

## Implications for Ellie

1. **LLM dedup should be periodic maintenance** — new memories accumulate duplicates over time
2. **Track reviewed pairs** — `dedup_reviewed` table prevents re-reviewing the same pairs
3. **Dedup improves result diversity**, not hit rate — still valuable for context window efficiency
4. **Cursor-based progress** is essential — dedup runs are long and may be interrupted

## Related Docs

- `docs/memories/overview.md`
- `docs/memories/entity-synthesis.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Created with E02 and E13 dedup results.
