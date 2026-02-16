# Issue #161: Entity Synthesis Worker

> Priority: P0 (biggest retrieval improvement: +15pp)
> Blocked by: #160 (needs 1536d embeddings)
> Blocks: nothing

## Summary

Build a post-extraction worker that identifies high-mention entities and consolidates scattered facts into rich definitional memories. This is the single biggest retrieval improvement found in testing (80% → 95% hit rate).

## Background

Vector search finds memories similar to the query. When facts about "ItsAlive" are scattered across 47 different conversation memories, no single memory is similar enough to rank highly for "What is ItsAlive?". A synthesized definitional memory matches the query directly.

In testing, 30 synthesized entity memories improved hit rate from 80% to 95% (+15pp). No other single intervention came close.

## Requirements

### 1. Entity detection
**New file:** `internal/memory/ellie_entity_synthesis_worker.go`

Detect entities that have:
- High mention count across memories (threshold: configurable, default 5+)
- No existing definitional/summary memory (or stale one)
- Kinds: projects, people, tools, concepts, locations

Detection query: group memories by entity references in content/title, count occurrences, filter by threshold.

### 2. LLM synthesis
For each detected entity, retrieve all related memories and generate a single comprehensive definitional memory via LLM.

Prompt requirements:
- Include ALL known facts (don't summarize away specific details like version numbers, model names, file paths)
- Structure: what it is, what it does, current status, key technical details
- Output: single memory with kind `fact`, importance 4-5

### 3. Storage
- Store synthesized memory with `source_type: 'synthesis'` or equivalent marker
- Link to source memories via metadata (array of source memory IDs)
- Embed at 1536d
- Do NOT deprecate source memories — synthesis adds, doesn't replace

### 4. Staleness detection
- Track synthesis timestamp
- Re-synthesize when source memory count increases significantly (e.g., 20%+ new memories since last synthesis)
- Expose a `needs_resynthesis` check

### 5. Integration with migration runner
Add entity synthesis as a phase that runs after Ellie backfill extraction:
```
agents → history → ellie backfill → entity synthesis → project discovery
```

**File:** `internal/import/migration_runner.go`

## Acceptance Criteria

- [ ] Worker identifies entities with 5+ memory mentions and no definitional memory
- [ ] LLM generates comprehensive definitional memories including specific details
- [ ] Synthesized memories are stored with source linkage and embedded at 1536d
- [ ] Source memories remain active (not deprecated)
- [ ] Staleness detection flags entities needing re-synthesis
- [ ] Migration runner includes synthesis as a post-extraction phase
- [ ] Worker is idempotent — re-running doesn't create duplicate syntheses

## Do NOT

- Deprecate or archive source memories after synthesis
- Use a cheap/small model — synthesis quality matters. Use the configured LLM extraction model.
- Synthesize entities with fewer than 5 mentions — noise

## Test Plan

1. Unit: entity detection query returns correct candidates
2. Unit: synthesis prompt includes all source facts
3. Unit: idempotency — second run with same data creates no new memories
4. Integration: extract → synthesize → retrieve roundtrip shows definitional memory ranking first
5. Staleness: add new source memories, verify `needs_resynthesis` triggers
