# Issue #164: Project Docs Ingestion (Layer 3)

> Priority: P1 (Layer 3 of three-layer memory system)
> Blocked by: #160 (needs 1536d embeddings), #163 (taxonomy nodes for classification)
> Blocks: nothing

## Summary

Build a worker that ingests authored project documentation (`docs/` directories) as authoritative Layer 3 memories. When a structured doc exists for a topic, it supersedes scattered conversation extractions.

## Background

The three-layer memory system:
1. **Extraction** — facts from conversations (breadth)
2. **Taxonomy** — classification for navigation (findability)
3. **Project docs** — authored markdown in `docs/` dirs (depth and authority)

Layer 3 is different from file-backed memory scanning. File scanning extracts facts FROM files. Layer 3 treats the doc itself as the authoritative memory — Ellie reads the doc directly when it's relevant.

Experimental findings: file-backed memories showed +36pp on current-state queries and +33pp on preferences. But using them exclusively hurt project queries (-29pp) because raw file extractions are shallower than conversation-derived context. The solution: Layer 3 docs are read at query time when they exist, not pre-extracted into memory rows.

## Requirements

### 1. Doc registry
**New table:**

```sql
CREATE TABLE IF NOT EXISTS ellie_project_docs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    file_path TEXT NOT NULL,           -- relative path within project repo (e.g., "docs/memories/overview.md")
    title TEXT,                        -- extracted from first H1 or filename
    summary TEXT,                      -- LLM-generated summary for retrieval matching
    summary_embedding vector(1536),    -- embedding of summary for similarity search
    content_hash TEXT NOT NULL,        -- MD5 for change detection
    last_scanned_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(org_id, project_id, file_path)
);
```

Key design: we store a **summary + embedding** for retrieval matching, but serve the **full doc content** at query time. This avoids the shallow-extraction problem.

### 2. Doc scanner
**New file:** `internal/memory/ellie_project_docs_scanner.go`

Scan each project's `docs/` directory:
- Find all `.md` files
- For each file: extract title, generate summary via LLM, embed summary at 1536d, hash content
- Upsert into `ellie_project_docs`
- Skip unchanged files (hash match)

### 3. Retrieval integration
**File:** `internal/memory/ellie_retrieval_cascade.go`

Add Layer 3 retrieval:
- When building context, search `ellie_project_docs` by summary embedding similarity
- For matches above threshold: read the full doc content from the project repo
- Inject full doc content into context (or relevant sections if doc is large)
- **Layer 3 results rank above Layer 1 conversation memories** for the same topic

### 4. Freshness
- Periodic re-scan detects changed files (hash mismatch)
- Changed files get re-summarized and re-embedded
- Deleted files get marked inactive

### 5. START-HERE.md awareness
If a project has `docs/START-HERE.md`, parse it to discover the doc tree structure. Use this to:
- Auto-register all linked docs
- Understand domain organization
- Weight docs referenced from START-HERE higher

### 6. Integration with migration runner
Add doc scanning as the final phase:
```
agents → history → ellie backfill → entity synthesis → dedup → taxonomy classification → project discovery → doc scanning
```

## Acceptance Criteria

- [ ] Doc registry table stores path, summary, embedding, content hash per doc
- [ ] Scanner discovers all `.md` files in project `docs/` directories
- [ ] LLM generates summaries, embedded at 1536d
- [ ] Unchanged files skipped on re-scan (hash check)
- [ ] Retrieval cascade searches doc summaries and serves full content
- [ ] Layer 3 docs rank above Layer 1 memories for same topic
- [ ] Changed files detected and re-processed on periodic scan
- [ ] Migration runner includes doc scanning as final phase
- [ ] START-HERE.md parsed for doc tree discovery

## Do NOT

- Extract memories FROM docs into the memories table — that's the old file-scanner approach. Layer 3 docs are served directly.
- Embed full doc content — too long, embeddings degrade. Embed the summary.
- Ignore large docs — for docs > N tokens, split into sections and summarize each section separately.

## Test Plan

1. Unit: scanner discovers .md files in docs/ directory
2. Unit: unchanged files skipped (hash match)
3. Unit: changed files re-summarized and re-embedded
4. Integration: scan → retrieve → full doc content served in context
5. Integration: Layer 3 result ranks above Layer 1 for same topic query
6. Edge: large doc (>4K tokens) split into sections correctly
