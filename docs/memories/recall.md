# Memories: Recall

> Summary: Retrieval cascade, ranking strategy, hybrid search findings, and the case for query routing.
> Last updated: 2026-02-16
> Audience: Agents modifying recall relevance or debugging missing context.

## Retrieval Service

Main service: `internal/memory/ellie_retrieval_cascade.go`

Tier order:
1. Room context
2. Project/org memory search
3. Chat history
4. JSONL fallback scanner
5. No-information response

## Ranking Strategy

Store implementation: `internal/store/ellie_retrieval_store.go`

- Keyword + semantic blended ranking when embedding is available
- Keyword fallback when embedding unavailable
- Dedupe and limit enforcement before response

## Experimental Findings: What Works and What Doesn't

### Pure Vector Search Wins (for now)

Benchmark (20 queries, 12,000+ memories):

```
Method                          | Hit Rate | Avg Recall
--------------------------------|----------|----------
Vector only (1536d)             |   80%    |   52.9%
BM25 only                       |   55%    |   40.8%
Hybrid RRF (vector + BM25)     |   75%    |   52.9%
```

**Hybrid BM25+vector regressed from 80% → 75%.** BM25 noise dilutes good vector matches on semantic queries. BM25 only helps with exact keyword matches (proper nouns like "nomic-embed-text", "samhotchkiss") but hurts everything else.

**Recommendation:** Stay with pure vector search at 1536d. Do not add BM25 unless the query router specifically routes to it.

### BUT: No Single Strategy Wins Everywhere

6-strategy × 20-query experiment revealed dramatic category-level differences:

| Query Category | Best Strategy | Gain vs Vector Baseline |
|---|---|---|
| current_state | file_only | +36pp (34% → 70%) |
| preference | vector+files | +33pp (67% → 100%) |
| file_content | file_only | +6pp (57% → 63%) |
| project | vector (baseline) | — (94%, best as-is) |
| personal | vector (baseline) | — (100%, perfect) |
| agent | anything | 100% across all strategies |

### The Query Router (Not Yet Built)

The data clearly shows we need a **query classifier** that routes to the optimal strategy:

1. **"What's happening now?" / current-state** → file_only or file-augmented
2. **"What does Sam prefer?" / preferences** → vector + live file reading
3. **"What is [project]?" / project knowledge** → pure vector (conversation memories are richer)
4. **"Who is [person]?" / personal facts** → pure vector (already perfect)
5. **"What does [file] say?" / file content** → file_only

Implementation: Cheap Haiku call classifies query → routes to strategy → retrieves.

This is the #1 unbuilt improvement. With routing, we should beat 80% across all categories.

### Importance Weighting Hurts

Weighting retrieval by the `importance` field (0.5 + importance × 0.1) degraded results by 4pp overall. The importance scores from extraction aren't reliable enough to use as retrieval weights. **Don't weight by importance.**

## Quality Signals

Retrieval quality can be recorded through quality sink hooks for tuning/evaluation workflows.

## Practical Debug Checklist

1. Confirm query embedding is being generated (1536d, not 768d).
2. Confirm memory rows are `active` and scoped to org/project.
3. Confirm tier transitions in logs/telemetry.
4. Confirm fallback scanner root/path safety settings.
5. Check if query is a current-state question — if so, file-backed memories may be needed.
6. Check for stale/contradictory memories — the system confidently serves wrong answers when source data is stale.

## Known Failure Mode: Confident Wrong Answers

When tested with "How many agent profiles exist?", the system answered "20" when the actual count was 230+. The stale memory from an early conversation was served with high confidence.

**Root cause:** Memory system has no fact versioning or conflict detection. Newer facts don't invalidate older ones.

**Mitigation (not yet built):** Fact conflict resolution — detect when newer memories contradict older ones on the same topic.

## Related Docs

- `docs/memories/vector-embedding.md`
- `docs/memories/file-backed-memories.md`
- `docs/memories/entity-synthesis.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Major rewrite with hybrid search findings, query routing design, importance weighting results, and confident-wrong-answer failure mode.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
