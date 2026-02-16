# Memories: Entity Synthesis

> Summary: Consolidating scattered conversation facts into rich definitional memories. Single biggest retrieval improvement.
> Last updated: 2026-02-16
> Audience: Agents working on memory quality and retrieval.

## What It Is

Entity synthesis scans existing memories, finds all facts about a given entity (project, person, concept), and generates one consolidated "definitional" memory that serves as the authoritative source.

Example: Instead of 47 scattered memories mentioning ItsAlive in various contexts, synthesis produces one rich memory: "ItsAlive is a ___. It does ___. Built with ___. Status: ___."

## Why It Matters

**This is the #1 retrieval improvement we've found.**

```
Before synthesis: 80% hit rate (16/20 queries)
After synthesis:  95% hit rate (19/20 queries)
```

+15pp from 30 synthesized memories. No other single intervention came close.

### Why It Works

Vector search finds memories similar to the query. When facts are scattered across dozens of conversations, no single memory is similar enough to rank highly. A synthesized definitional memory IS the thing you're looking for — it matches the query directly.

## Current State

- **30 synthesized entity memories** created via `synthesize-entities.mjs`
- Covers: ItsAlive, Three Stones, Pearl, OtterCamp, parenting philosophy, writing style, Essie, Noah, vehicles, agent roles, etc.
- Script: `~/Documents/SamsBrain/OtterCamp/extraction/synthesize-entities.mjs`
- Also: `synthesize-entities-v2.mjs` (improved version)

## How It Works

1. Identify high-value entities (projects, people, concepts) from memory distribution
2. Retrieve all memories mentioning that entity
3. LLM synthesizes a comprehensive definitional memory
4. Store with high importance (4-5), kind `fact` or `context`
5. Source memories remain active (synthesis adds, doesn't replace)

## Remaining Gap

One query still missed after synthesis: "What embedding model does Pearl use?" — the synthesized Pearl memory focused on architecture, not the specific embedding model name. This shows synthesis prompts need to include specific technical details, not just high-level descriptions.

## Implications for Ellie

Entity synthesis should be a **periodic pipeline step**, not a one-time manual run:
1. Detect entities with high mention count but no definitional memory
2. Auto-generate synthesis candidates
3. Optionally: human review before promotion to active

This is the clearest path from 95% to near-100% retrieval.

## Related Docs

- `docs/memories/overview.md`
- `docs/memories/recall.md`
- `docs/memories/dedup.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Created with full experimental findings from E10 Round 4 and synthesis pipeline details.
