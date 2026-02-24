# 042: Memory API Endpoints

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | S (≤1 day) |
| Spec refs | doc 06 §MemoryAPI, doc 06 §MemoryQueryAPI, doc 06 §MemoryImportAPI, doc 06 §MemoryConsolidateAPI |
| Spec status | finished |
| Depends on | 041, 040, 007 |
| Blocks | 075, 086 |

## Scope

Build all HTTP endpoints for the memory subsystem: query, item management, entity browsing,
taxonomy retrieval, import submission, import status, and manual consolidation trigger. No new
tables — this task wires the services from tasks 039, 040, and 041 into HTTP handlers.

### Must build

**Memory query endpoint:**
- `POST /v1/memory/query` — invoke the retrieval pipeline for the authenticated agent or user
  - Body:
    ```json
    {
      "query": "string",
      "mode": "passive|mention|agent_query",
      "project_id": "uuid (optional)",
      "task_id": "uuid (optional)",
      "max_results": 20,
      "include_restricted": false
    }
    ```
  - Response: `{data: {memories: [{id, content, memory_type, scope, confidence, trust_tier, cosine_similarity, composite_score, created_at}], fallback_used: bool, entity_profiles: [{entity_id, name, synthesis_memory_id}]}}`
  - Calls `Retriever.Query` with the authenticated agent_id/user context
  - `include_restricted=true` only allowed when mode is `mention` or `agent_query`; passive mode always forces `include_restricted=false`
  - If called by a human user (not an agent), `SensitivityGate=false` always (humans can see all their org's memories)

**Memory item endpoints:**
- `GET /v1/memory/items` — list memory rows for the caller's org; query params:
  - `status` (active/candidate/superseded/archived, default=active)
  - `scope` (org/project/task/agent)
  - `memory_type`
  - `project_id`
  - `agent_id`
  - `search` (full-text substring match on `content`)
  - `limit` (default 50, max 200), `cursor` (opaque pagination cursor)
- `GET /v1/memory/items/:id` — get a single memory row by ID; includes linked taxonomy tags and entity mentions in response
  - Response also includes `supersession_chain` array if the memory is superseded (walk `superseded_by` chain up to 10 hops)

**Memory entity endpoints:**
- `GET /v1/memory/entities` — list `memory_entity` rows for the org; query params: `search` (name match), `type`, `limit`, `cursor`
- `GET /v1/memory/entities/:id` — get a single entity; includes `synthesis_memory_id` if a profile exists; includes top 10 recent mentions with their memory content

**Taxonomy endpoint:**
- `GET /v1/memory/taxonomy` — return the full `memory_taxonomy_node` tree for the org; structured as a nested JSON tree (not a flat list)
  - Query param: `flat=true` returns a flat array instead of nested
  - Each node includes: `{id, name, parent_id, path, depth, memory_count}` where `memory_count` is the count of active memories tagged with this node

**Import endpoints:**
- `POST /v1/memory/import` — submit a new JSONL zip import
  - Accepts `multipart/form-data` with a `file` field (the JSONL zip) OR a JSON body with `{file_key: "storage_key"}` (if the file was pre-uploaded to object storage)
  - If multipart: uploads the file to object storage first, then calls `Importer.StartImport`
  - Response: `{data: {import_id, status: "pending", created_at}}`
- `GET /v1/memory/imports/:id` — get import status and progress
  - Response: `{data: {id, status, total_records, processed_records, imported_records, rejected_records, error_message, started_at, completed_at, created_at}}`
- `GET /v1/memory/imports` — list recent imports for the org; query params: `status`, `limit`, `cursor`

**Manual consolidation trigger:**
- `POST /v1/memory/consolidate` — trigger an on-demand `sleep_reflection` compaction run for the org
  - Body: `{run_type: "sleep_reflection"|"task_consolidation", task_id?: "uuid"}`
  - For `task_consolidation`: `task_id` is required; the task must belong to the caller's org; the task must be in `status='done'`
  - Response: `{data: {compaction_run_id, status: "pending"}}`
  - Enqueues the appropriate job via the job queue (task 024); returns immediately
  - Requires org-admin role

**Auth and RBAC:**
- `POST /v1/memory/query`: available to authenticated agents and users; agents are scope-filtered by `memory_read_scopes`; users see all org memories (human admins only)
- All `GET /v1/memory/items*`, `GET /v1/memory/entities*`, `GET /v1/memory/taxonomy`: available to org-admin and agent (agent sees only its accessible scope)
- Import endpoints: `POST /v1/memory/import` requires org-admin; `GET /v1/memory/imports` requires org-admin
- `POST /v1/memory/consolidate`: requires org-admin

**Request/response conventions:**
- All list endpoints use cursor-based pagination (opaque cursors, default 50, max 200) per task 067 conventions
- Memory content is returned as plain text; no truncation in item endpoints; `POST /v1/memory/query` truncates at 2000 characters per memory with `truncated: true` flag if exceeded

### Must NOT build
- Extraction pipeline logic (task 039)
- Retrieval pipeline logic (task 040)
- Compaction / import service logic (task 041)
- `memory_source` endpoints (L4, to be added when `memory_source` table is created)
- Memory native tools (`memory.query`, `memory.record` — task 057)

## Acceptance Criteria

- [ ] `POST /v1/memory/query` with `mode='passive'` and `include_restricted=true` returns 422 (passive mode cannot include restricted)
- [ ] `GET /v1/memory/items` with no auth returns 401
- [ ] `GET /v1/memory/items?status=active` returns only `status='active'` rows for the caller's org; other org's rows not present
- [ ] `GET /v1/memory/items/:id` for a superseded memory includes `supersession_chain` with the chain ending at the current active memory
- [ ] `GET /v1/memory/taxonomy` returns a nested tree; each node has `memory_count` reflecting current active memory count
- [ ] `POST /v1/memory/import` with multipart file: uploads to object storage, creates `memory_import` row with `status='pending'`, returns `import_id`
- [ ] `GET /v1/memory/imports/:id` returns live progress (processed_records increments while job runs)
- [ ] `POST /v1/memory/consolidate` with `run_type='task_consolidation'` and missing `task_id` returns 422
- [ ] Non-admin calling `POST /v1/memory/consolidate` returns 403

## Tests Required

**Unit tests:**
- Route registration: all 10+ endpoints registered with correct HTTP methods
- `POST /v1/memory/query` passive + include_restricted validation: 422 returned correctly
- Taxonomy tree builder: flat list of nodes with parent_id links → nested JSON tree output; orphan nodes (parent_id not in list) handled gracefully
- Supersession chain: memory A superseded by B superseded by C → `GET /v1/memory/items/A` returns chain [A, B, C]

**Integration tests (95% coverage target per doc 21):**
- 4-stage pipeline via API: call `POST /v1/memory/query` (deterministic mode); verify ranked results in correct order (most relevant last)
- Trust tiers: seed two memories with different `trust_tier` values; `GET /v1/memory/items?trust_tier_min=0.8` returns only the high-trust one (if this filter is supported; otherwise verify `trust_tier` field in response)
- Dedup via API: seed two near-duplicate memories with `memory_dedup_reviewed.decision='supersede_a'`; `GET /v1/memory/items?status=superseded` returns the superseded one; `GET /v1/memory/items?status=active` does not include it
- Passive injection dedup: call `POST /v1/memory/query` with `mode='passive'` 6 times with the same session context; same memory appears in turn 1 and turn 6 results, but NOT in turns 2–5 (cooldown logic)
- Import round-trip: `POST /v1/memory/import` → poll `GET /v1/memory/imports/:id` until completed → `GET /v1/memory/items?status=active` returns imported memories

**E2E tests:**
- None — covered by dedicated E2E task 075 and 086

## Implementer Notes

- The `POST /v1/memory/query` endpoint is the primary interface used by the `memory.query` native tool (task 057). The tool makes an authenticated call to this endpoint with `mode='agent_query'`. The endpoint must accept API key auth (not just session token auth) because tool calls are dispatched by the broker without a user session context.
- The taxonomy tree response (`GET /v1/memory/taxonomy`) must be built from a single DB query fetching all nodes for the org, then assembled into a nested structure in-memory (do not make recursive DB calls per node). The `memory_count` field can be computed with a JOIN or a separate COUNT query; if the org has many nodes, use a single `GROUP BY` query to get all counts, then merge into the tree.
- The multipart import upload is limited to 100MB compressed file size (validated before uploading to object storage). The uncompressed size limit (50MB) is validated by the import processor after download. Return 413 if the compressed upload exceeds 100MB.
- `GET /v1/memory/items` with `search=` parameter performs a case-insensitive substring match on `content` using `ILIKE '%query%'`. This is intentionally simple for V2 — full-text search is a future enhancement. For orgs with many memories, this query may be slow; document the known limitation and recommend using `POST /v1/memory/query` (vector search) for relevance-ranked search.
- The `memory_count` in the taxonomy tree should only count `status='active'` memories. Do not count candidates, superseded, or archived memories in the display count.

> ✅ ISSUE #9 (RESOLVED): `memory_source.session_id` is a permanent soft reference (no FK). The `GET /v1/memory/items/:id` response cannot include source provenance until task 075 (L4) builds `memory_source`. Add a `"sources": null` field with a handler comment: `// TODO: populate sources from memory_source table after task 075 (L4)`.
