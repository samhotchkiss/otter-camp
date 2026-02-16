# Memories: Taxonomy and Structure

> Summary: Memory kinds, sensitivity, status, field-level structure, and the taxonomy routing experiment.
> Last updated: 2026-02-16
> Audience: Agents writing memory data or building schema-dependent logic.

## Agent Memory Entry Taxonomy

From `internal/store/memory_store.go`:

Kinds:
- `summary`, `decision`, `action_item`, `lesson`, `preference`, `fact`, `feedback`, `context`

Sensitivity:
- `public`, `internal`, `restricted`

Status:
- `active`, `warm`, `archived`

## Ellie Extracted Memory Taxonomy

From `internal/store/ellie_ingestion_store.go`:

Kinds:
- `technical_decision`, `process_decision`, `preference`, `fact`
- `lesson`, `pattern`, `anti_pattern`, `correction`
- `process_outcome`, `context`

Status:
- `active`, `deprecated`, `archived`

## Current Kind Distribution (13,359 active)

```
fact:                4,911  (36.8%)
context:             3,390  (25.4%)
technical_decision:  2,181  (16.3%)
process_decision:      893  ( 6.7%)
preference:            673  ( 5.0%)
lesson:                442  ( 3.3%)
(others):              869  ( 6.5%)
```

**Observation:** `fact` and `context` dominate. OtterCamp-related memories (4,900+) can drown out personal queries in vector space.

## Shared Structural Fields

Common fields across paths:
- `importance` (1-5) — **Note: importance weighting hurts retrieval (-4pp). Do not use as a search weight.**
- `confidence` (0-1)
- `occurred_at`
- `source_conversation_id`
- `source_project_id` / `source_project`
- `file_path` (file-backed memories only)
- `file_content_hash`, `file_mtime` (freshness tracking)
- metadata JSON

## Taxonomy-Based Retrieval Routing (Experiment)

Sam proposed a hierarchical mind map for retrieval routing:
- Build a taxonomy tree (e.g., personal→vehicles, personal→family→noah)
- Navigate tree first, pull all memories under that node
- Deterministic, complete, cheap

### Prototype Built
- `build-taxonomy-v2.mjs` → generates `taxonomy.json` from memory DB
- `taxonomy-explorer.html` → interactive web explorer (served on port 8899)
- Tailscale IP: 100.67.2.108

### Benchmark Results
Taxonomy-based retrieval was tested but results were mixed. The taxonomy helps with categorical completeness (getting ALL facts about a topic) but doesn't outperform vector search for relevance ranking.

### Design Implication
Taxonomy is useful as a **completeness check** (did we miss any memories about X?) rather than a primary retrieval strategy. It complements vector search rather than replacing it.

## Notes

- Taxonomy split currently reflects two pipelines (agent-memory vs Ellie-memory), not one merged contract.
- Kind-aware filtering was tested in the 6-strategy benchmark and showed marginal benefit (-2pp vs baseline). Not worth the complexity.

## Related Docs

- `docs/memories/recall.md`
- `docs/memories/overview.md`
- `docs/memories/experiment-log.md`

## Change Log

- 2026-02-16: Added kind distribution, taxonomy routing experiment, importance weighting warning, and file-backed fields.
- 2026-02-16: Created canonical documentation file and migrated relevant legacy content.
