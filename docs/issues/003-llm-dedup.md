# Issue #003: LLM-Powered Deduplication Worker

> Priority: P1 (quality improvement, not blocking)
> Blocked by: #001 (needs 1536d embeddings for similarity)
> Blocks: nothing

## Summary

Build a post-extraction worker that finds near-duplicate memories and uses LLM judgment to keep the best version, deprecate the rest, and optionally merge. In testing, this reduced 17,651 memories to 12,492 (29% reduction) with zero false kills.

## Background

Semantic similarity alone can't distinguish "same fact, different words" from "related but distinct facts." Pure cosine threshold dedup at 0.88 found 272 dupes but had false positives. LLM (Haiku) reviewing candidate pairs achieves near-perfect precision.

Dedup didn't directly improve hit rate, but it improves result diversity — when 5 of your top-10 results aren't saying the same thing, you get more useful context in the same token budget.

## Requirements

### 1. Candidate pair detection
**New file:** `internal/memory/ellie_dedup_worker.go`

Find memory pairs with cosine similarity above threshold (configurable, default 0.88) using the 1536d embedding column. Cluster connected pairs into groups.

### 2. LLM review
For each cluster, send to LLM with prompt:
- Are these the same fact or related-but-distinct?
- If same: which is the best version? (most complete, most accurate)
- Should any be merged into a single better memory?

Output per cluster:
- `keep`: memory ID to keep
- `deprecate`: memory IDs to mark deprecated
- `merge`: optional merged content (creates new memory, deprecates all originals)

### 3. Review tracking
**New table or column:** Track reviewed pairs to avoid re-reviewing.

Schema:
```sql
CREATE TABLE IF NOT EXISTS ellie_dedup_reviewed (
    memory_id_a UUID NOT NULL,
    memory_id_b UUID NOT NULL,
    decision TEXT NOT NULL,  -- 'keep_both', 'deprecated_b', 'merged', etc.
    reviewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (memory_id_a, memory_id_b)
);
```

### 4. Cursor-based progress
Dedup runs are long (thousands of clusters). Must support:
- Pause/resume via cursor
- Recovery from interruption (SIGKILL, crash)
- Progress reporting (clusters processed / total)

### 5. Integration with migration runner
Add dedup as a phase after entity synthesis:
```
agents → history → ellie backfill → entity synthesis → dedup → project discovery
```

## Acceptance Criteria

- [ ] Finds candidate pairs above configurable similarity threshold
- [ ] LLM reviews clusters and makes keep/deprecate/merge decisions
- [ ] Reviewed pairs tracked — re-running skips already-reviewed pairs
- [ ] Cursor-based progress — survives interruption and resumes
- [ ] Deprecated memories have status `deprecated`, not deleted
- [ ] Migration runner includes dedup as a post-synthesis phase
- [ ] Progress reporting (processed/total clusters)

## Do NOT

- Delete memories — always deprecate (recoverable)
- Use pure similarity threshold without LLM review — too many false positives
- Run dedup before entity synthesis — synthesis creates new memories that may need dedup

## Test Plan

1. Unit: candidate detection finds pairs above threshold
2. Unit: LLM review returns valid keep/deprecate/merge decisions
3. Unit: reviewed pairs are tracked and skipped on re-run
4. Integration: interrupt mid-run, resume, verify no data loss
5. Integration: deprecated memories excluded from retrieval results
