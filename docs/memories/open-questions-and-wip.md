# Memories: Open Questions and WIP

> Summary: Remaining work, open decisions, and prioritized next steps based on experimental findings.
> Last updated: 2026-02-16
> Audience: Agents planning next memory work.

## Priority 1: Build Now

### Query Router
The #1 unbuilt improvement. Data proves no single strategy wins everywhere. Need:
- Cheap Haiku call to classify query type (current_state, preference, project, personal, file_content)
- Route to optimal retrieval strategy per classification
- Expected to beat 80% baseline across all categories

### Fix Bridge Gap (Feb 11-15)
5 days of conversations invisible to memory system. Need:
- JSONL direct ingestion path (read OpenClaw session logs directly)
- Backfill Feb 11-15
- Monitoring/alerting for bridge health going forward

### Finish File Scanning
535 of 635 files still unscanned. Run `file-scanner.mjs` to completion.

## Priority 2: Short-Term

### Live File Reading at Query Time
When a memory has `file_path`, optionally read current file content instead of relying on stale extraction. Critical for current-state queries.

### Periodic Entity Re-Synthesis
Entity synthesis is the biggest win but currently manual. Should run periodically:
1. Detect entities with high mention count but no/stale definitional memory
2. Auto-generate synthesis candidates
3. Optional human review before promotion

### Fact Conflict Resolution
System confidently serves wrong answers when source data is stale (e.g., "20 profiles" when actual is 230+). Need:
- Detect when newer facts contradict older memories on the same topic
- Version or deprecate stale facts

### Scheduled File Re-Scan
Detect when file-backed memories are stale (file changed since last scan). `freshness-check.mjs` exists but needs to be scheduled.

## Priority 3: Medium-Term

### Queued Experiments
- **E05:** Window size optimization (10/20/30/50 msgs) — impacts extraction quality
- **E11:** Haiku vs Sonnet extraction quality comparison
- **Cross-encoder re-ranking** — expensive model re-scores top-20 vector results

### Unify Agent-Memory and Ellie-Memory Taxonomy
Two separate kind/status/sensitivity contracts exist for agent-scoped vs Ellie memories. Should be unified.

### Retrieval Explainability
Return "why this memory was retrieved" metadata alongside each result.

## Decisions Needed (from Sam)

- Should ingestion always run LLM-first in production, or remain opt-in by environment?
- Should JSONL fallback stay enabled by default once migration backfill is complete?
- Should memory status models be unified (`warm` vs `deprecated`) across tables?
- Should entity synthesis be fully automated or require human review?

## Not Worth Doing (Experimentally Proven)

| Idea | Why Not |
|---|---|
| Hybrid BM25+vector search | Regressed 80% → 75%. BM25 noise dilutes vector. |
| Importance-weighted retrieval | -4pp overall. Importance scores not reliable enough. |
| Kind-aware filtering | -2pp vs baseline. Marginal complexity for no gain. |
| File-only retrieval | -3pp overall. Loses rich conversation context. |

## Related Docs

- `docs/memories/experiment-log.md`
- `docs/memories/recall.md`
- `docs/memories/entity-synthesis.md`
- `docs/memories/file-backed-memories.md`

## Change Log

- 2026-02-16: Complete rewrite with prioritized roadmap based on experimental findings. Added "not worth doing" section.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
