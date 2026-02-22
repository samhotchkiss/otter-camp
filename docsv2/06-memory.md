# 06. Memory Management (Ellie V2 from First Principles)

## Memory Goals

- Improve continuity across sessions and tasks.
- Keep memory useful, scoped, and safe.
- Prevent stale or low-quality memory pollution.

## Memory Layers

- Working memory: short-lived session/task context.
- Episodic memory: time-stamped events and outcomes.
- Semantic memory: distilled facts/knowledge.
- Procedural memory: reusable playbooks and heuristics.

## Core Memory Entities

- `memory_item`
- `memory_source`
- `memory_scope`
- `memory_feedback`
- `memory_compaction_run`

## Scoping

- Org scope
- Project scope
- Chat/session scope
- Agent-private scope

## Pipeline

1. Ingest candidate memory from events/messages/artifacts.
2. Extract structured claims.
3. Score for utility and confidence.
4. Apply policy filters.
5. Store with provenance.
6. Retrieve by relevance and scope.
7. Learn via feedback loops.

## Retrieval Strategy

- Hybrid retrieval (semantic + lexical + recency boost).
- Hard scope filtering before ranking.
- Budget-aware memory injection into prompts.

## Memory Hygiene

- TTL and decay policies by memory type.
- Compaction and deduplication jobs.
- Contradiction detection and conflict resolution state.

## Open Questions

- What confidence threshold is needed for auto-promotion to semantic memory?
- Should humans be required to confirm certain memory classes?
- How aggressive should decay be for operational memory?

