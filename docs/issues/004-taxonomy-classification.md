# Issue #004: Taxonomy Classification for Extracted Memories

> Priority: P1 (Layer 2 of three-layer memory system)
> Blocked by: #001 (needs 1536d embeddings)
> Blocks: nothing

## Summary

Build a classification step that assigns extracted memories to nodes in a hierarchical taxonomy tree. This is Layer 2 of the three-layer memory system: extraction (Layer 1) → taxonomy classification (Layer 2) → project docs (Layer 3).

## Background

The taxonomy enables deterministic, complete retrieval by category. Instead of relying solely on vector similarity (which can miss relevant memories), taxonomy navigation finds everything under a topic node. It complements vector search — it doesn't replace it.

Sam's design: hierarchical mind map where you navigate to the right node (e.g., `personal→vehicles`, `projects→otter-camp→memory-system`) and get all memories under it.

## Requirements

### 1. Taxonomy tree schema
**New migration + store:**

```sql
CREATE TABLE IF NOT EXISTS ellie_taxonomy_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    parent_id UUID REFERENCES ellie_taxonomy_nodes(id),
    slug TEXT NOT NULL,
    display_name TEXT NOT NULL,
    description TEXT,
    depth INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, parent_id, slug)
);

CREATE TABLE IF NOT EXISTS ellie_memory_taxonomy (
    memory_id UUID NOT NULL REFERENCES memories(id),
    node_id UUID NOT NULL REFERENCES ellie_taxonomy_nodes(id),
    confidence FLOAT NOT NULL DEFAULT 1.0,
    classified_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (memory_id, node_id)
);
```

A memory can belong to multiple taxonomy nodes (e.g., a memory about "Pearl's embedding model" belongs to both `projects→pearl` and `technical→embeddings`).

### 2. Seed taxonomy
On migration/onboarding, seed a default taxonomy tree. Top-level nodes:

```
personal/        — family, vehicles, health, location, preferences
projects/        — one child per discovered project
technical/       — decisions, patterns, infrastructure
agents/          — one child per agent definition
process/         — workflows, pipelines, engineering practices
```

Project discovery (existing phase) should auto-create `projects/{project-slug}` nodes.

### 3. Classification worker
**New file:** `internal/memory/ellie_taxonomy_classifier.go`

For each unclassified memory:
- Use LLM (Haiku — cheap and fast) to classify into 1-3 taxonomy nodes
- Store classification with confidence score
- Mark memory as classified

Prompt: given the taxonomy tree and the memory content, return the best-matching node paths.

### 4. Retrieval integration
**File:** `internal/memory/ellie_retrieval_cascade.go`

Add a taxonomy-based retrieval path:
- Given a query, classify it into taxonomy nodes (same classifier)
- Retrieve all memories under those nodes
- Use as a supplementary retrieval tier (after vector search, before fallback)

### 5. Integration with migration runner
Add classification as a phase after dedup:
```
agents → history → ellie backfill → entity synthesis → dedup → taxonomy classification → project discovery
```

## Acceptance Criteria

- [ ] Taxonomy tree schema with parent-child relationships
- [ ] Default taxonomy seeded on onboarding
- [ ] Project discovery auto-creates project taxonomy nodes
- [ ] LLM classifier assigns memories to 1-3 taxonomy nodes with confidence
- [ ] Memories queryable by taxonomy node (return all under node + children)
- [ ] Retrieval cascade includes taxonomy-based tier
- [ ] Migration runner includes classification as a phase
- [ ] Taxonomy tree is viewable/editable (API endpoints for CRUD)

## Do NOT

- Replace vector search with taxonomy — it's supplementary
- Over-classify — 1-3 nodes per memory is enough
- Use taxonomy for ranking — it's for completeness, not relevance ordering

## Test Plan

1. Unit: taxonomy tree CRUD (create node, reparent, delete leaf)
2. Unit: classifier assigns plausible nodes for test memories
3. Unit: retrieval by node returns all memories under subtree
4. Integration: extract → classify → retrieve-by-node roundtrip
5. Migration: seed taxonomy runs cleanly on fresh DB
