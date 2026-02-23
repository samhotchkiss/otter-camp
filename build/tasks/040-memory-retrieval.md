# 040: Memory Retrieval Pipeline

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 06 §RetrievalPipeline, doc 06 §RetrievalModes, doc 06 §SensitivityGating, doc 06 §EntitySynthesis, doc 06 §DedupAndContradiction, doc 06 §SupersessionChain, doc 06 §FileBackedFreshness |
| Spec status | finished |
| Depends on | 038, 035, 013 |
| Blocks | 041, 042, 075, 086 |

## Scope

Build the 4-stage memory retrieval pipeline, the three retrieval modes (passive injection,
active @mention, agent-initiated query), entity synthesis, dedup review (LLM cluster dedup),
contradiction detection, supersession, and file-backed memory freshness scan.

### Must build

**Retrieval pipeline (`internal/memory/retriever.go`):**

`Retriever.Query(ctx, req RetrievalRequest) (RetrievalResult, error)`:
```go
type RetrievalRequest struct {
    OrganizationID uuid.UUID
    ProjectID      *uuid.UUID
    AgentID        *uuid.UUID
    TaskID         *uuid.UUID
    Query          string          // the turn text or explicit query
    Mode           RetrievalMode   // passive, mention, agent_query
    MaxResults     int             // default 20
    SensitivityGate bool           // false = include 'restricted'; true = exclude 'restricted'
}

type RetrievalResult struct {
    Memories    []RankedMemory
    FallbackUsed bool  // true if fell back to full-corpus scan
    EntityProfiles []EntityProfile
}

type RankedMemory struct {
    Memory     Memory
    Score      float64  // composite relevance score
    CosineSim  float64
}
```

**Stage 1 — Scope filter (hard WHERE clause):**
- Builds a SQL WHERE clause based on `memory_read_scopes` from the requesting agent's profile (doc 05 `memory_read_scopes` array)
- Scope filter hierarchy (always applied; cannot be bypassed):
  - `'org'` scope: `WHERE organization_id = $1 AND scope IN ('org') AND status = 'active'`
  - `'project'` scope: adds `OR (scope = 'project' AND project_id = $project_id)`
  - `'task'` scope: adds `OR (scope = 'task' AND project_task_id = $task_id)`
  - `'agent_private'`: adds `OR (agent_id = $agent_id)` regardless of scope column
- Sensitivity gating: if `SensitivityGate=true`, add `AND sensitivity = 'normal'` (passive injection always sets `SensitivityGate=true`; active modes may set false)
- Output: candidate set from DB (up to 500 rows before semantic ranking)

**Stage 2 — Taxonomy classification:**
- Two sub-modes:
  - **Rule-based** (default for passive injection): classify the query into 1–3 taxonomy nodes using a keyword matcher against `memory_taxonomy_node.name` values; fast, no LLM call
  - **LLM-based** (for active @mention and agent_query): call the Haiku-class model with `invocation_purpose='memory_retrieval'` to classify the query; more accurate, slightly slower
- If taxonomy classification produces 0 matching nodes: skip Stage 3, go directly to Stage 4 with the full Stage 1 candidate set

**Stage 3 — Subtree retrieval:**
- Using the classified taxonomy nodes, retrieve the subtree of `memory_taxonomy_node` rooted at each classified node (walk `parent_id` links downward)
- Filter Stage 1 candidates to those with a `memory_taxonomy_tag` matching any node in the subtree
- If fewer than 3 memories remain after taxonomy filter: fallback to full Stage 1 candidate set (set `FallbackUsed=true`)

**Stage 4 — Relevance ranking:**
- Score each candidate memory using a composite of:
  - **Cosine similarity** (weight 0.5): `1 - (embedding <=> query_embedding)` — query is embedded using the same model as memories; `invocation_purpose='memory_retrieval'`
  - **Recency** (weight 0.3): exponential decay on `created_at`; half-life = 30 days for episodic, 180 days for semantic/procedural
  - **Confidence** (weight 0.2): `memory.confidence`
- Final score = weighted sum; sort descending; return top `MaxResults`
- Fallback trigger: if top score < 0.35 after ranking: use full Stage 1 candidate set (ignore taxonomy filter); re-rank; set `FallbackUsed=true`
- Injection ordering: return memories ordered most-relevant-LAST (so the most relevant memory is closest to the turn boundary in the prompt; doc 06)

**Three retrieval modes:**
- **Passive injection** (`mode=passive`): called automatically by the prompt assembly engine (task 050) before each agent turn; `SensitivityGate=true`; result injected into Layer 5 of the prompt; cooldown mechanism: same memory not injected more than once per 5 turns in a session (tracked in-memory per session)
- **Active @mention** (`mode=mention`): triggered when the agent's turn contains `@memory:query` syntax; `SensitivityGate=false`; LLM taxonomy classification; result returned inline to the agent
- **Agent-initiated query** (`mode=agent_query`): triggered by the `memory.query` native tool call; full 4-stage pipeline with LLM classification; returns structured result as tool output

**Entity synthesis pipeline (`internal/memory/entity_synth.go`):**
- `EntitySynthesizer.SynthesizeProfile(ctx, entityID uuid.UUID) (*EntityProfile, error)`
- Triggered: when `memory_entity` accumulates ≥5 `memory_entity_mention` rows OR on explicit compaction run
- Process:
  1. Collect all active memories that mention this entity (via `memory_entity_mention`)
  2. Call Haiku-class LLM (`invocation_purpose='memory_synthesis'`) with the collected memories to produce a synthesized entity profile summary
  3. Store the synthesis as a new `memory` row with `memory_type='entity_profile'`, `scope` matching the most specific scope across source memories
  4. Update `memory_entity.synthesis_memory_id` to point to the new memory row (this is the deferred L2→L3 forward reference from the dep graph)
- `EntitySynthesizer.GetProfile(ctx, entityID uuid.UUID) (*Memory, error)` — returns the current synthesis memory for the entity (or nil if not yet synthesized)

**LLM cluster dedup (`internal/memory/deduper.go`):**
- `Deduper.ReviewCluster(ctx, pairs []DedupPair) error`
- Called by the dedup review job (job_type: `memory_dedup_review`; triggered by extraction pipeline and compaction runs)
- Pre-screen (already done at extraction time by Stage 4): cosine ≥ 0.88 threshold
- LLM review: batch pairs (up to 10 per LLM call) to Haiku-class with `invocation_purpose='memory_dedup'`; LLM returns decision for each pair: `keep_both`, `merge`, `supersede_a`, `supersede_b`
- After LLM decision:
  - `keep_both`: update `memory_dedup_reviewed.decision='keep_both'`; no memory changes
  - `merge`: call `Merger.Merge(ctx, a, b)` which calls LLM to produce merged content; stores as new memory row; supersedes both a and b
  - `supersede_a` / `supersede_b`: set loser's `status='superseded'`, `superseded_by=winner.id`, `superseded_at=now()`

**Contradiction detection (`internal/memory/contradiction.go`):**
- `ContradictionDetector.Check(ctx, newMemory Memory) ([]Memory, error)` — returns memories that potentially contradict the new one
- Triggered: at end of Stage 4 extraction for each new candidate, AND periodically during compaction runs
- Pre-screen: retrieve memories with cosine similarity 0.7–0.87 (below near-duplicate threshold but potentially contradictory) with same entity mentions
- LLM classification: Haiku-class call determines if pair is contradictory, redundant, or independent
- If contradictory: the newer memory's `confidence` is reduced by 0.1; if confidence drops below 0.2, the new memory stays candidate (not promoted); if an existing active memory is contradicted by a high-confidence new memory (`confidence > 0.85`), existing memory gets `status='superseded'`

**Supersession chain:**
- `SupersessionChain.Resolve(ctx, memoryID uuid.UUID) (*Memory, error)` — follows `superseded_by` chain to find the current non-superseded memory; max 10 hops to prevent infinite loops
- Retrieval pipeline always resolves supersession before returning results: superseded memories are excluded from Stage 1 (`status='active'` filter covers this), but callers can request the full chain via `GetSupersessionChain(ctx, memoryID)` for audit purposes

**File-backed memory freshness scan (`internal/memory/file_scanner.go`):**
- `FileScanner.Scan(ctx, orgID uuid.UUID) error`
- Called by a daily scheduled job (job_type: `memory_file_scan`)
- Fetches all `memory` rows where `file_backed=true` and `status='active'`
- For each file-backed memory:
  - Read the file at `file_path` from the project's repo (via the file system tool abstraction)
  - If file content has changed (hash differs from `content_hash`): create a new memory row with updated content; supersede the old one
  - If file no longer exists: set old memory `status='archived'`, `archived_at=now()`
  - Update `file_last_scanned_at` on the old row regardless

### Must NOT build
- Memory compaction / sleep reflection (task 041)
- Memory API endpoints (task 042)
- Prompt assembly engine integration (task 050 uses `Retriever.Query` — do not implement that integration here)
- `memory_source` table (L4)

## Acceptance Criteria

- [ ] Stage 1 scope filter excludes memories from other orgs; agent with `memory_read_scopes=['org']` does not see project-scoped or agent-private memories
- [ ] Sensitivity gating: passive retrieval (`SensitivityGate=true`) excludes `sensitivity='restricted'` memories; agent_query mode (`SensitivityGate=false`) includes them
- [ ] Fallback to full-corpus: if taxonomy filter leaves fewer than 3 results, fallback is triggered and `FallbackUsed=true` in response
- [ ] Injection ordering: retrieved memories returned in ascending composite score order (most relevant last)
- [ ] Passive injection cooldown: same memory not returned in results if it was returned within the last 5 turns of the same session (in-memory tracking)
- [ ] Entity synthesis: ≥5 `memory_entity_mention` rows for an entity triggers synthesis; result stored as `memory_type='entity_profile'`; `memory_entity.synthesis_memory_id` updated
- [ ] LLM dedup: pair with `decision='supersede_a'` sets memory A's `status='superseded'`, `superseded_by=B.id`
- [ ] Contradiction detection: new memory with contradicting evidence reduces existing active memory confidence; if existing confidence falls to 0, existing memory is archived

## Tests Required

**Unit tests:**
- Stage 1 scope filter builder: agent with `memory_read_scopes=['org','project']` → WHERE clause includes both scopes; sensitivity gate adds `AND sensitivity='normal'`
- Relevance ranking formula: fixed cosine+recency+confidence values → expected composite score (within 0.001 tolerance)
- Supersession chain: chain A→B→C; `Resolve(A.id)` returns C; chain length 11 (exceeds max hops) returns error
- Cooldown tracking: memory M returned in turn 1; subsequent Query calls for turns 2–5 exclude M; turn 6 includes M again

**Integration tests:**
- Full 4-stage retrieval (deterministic): insert 20 active memories with known embeddings; query with a test embedding; verify top-5 results are the 5 most cosine-similar
- Taxonomy subtree filter: tag memory A with node `workflow.git`; tag memory B with node `preferences`; query with taxonomy=`workflow` → A returned, B not returned
- Entity synthesis trigger: insert 5 `memory_entity_mention` rows for entity X; run synthesizer; verify new `memory_type='entity_profile'` row created and `memory_entity.synthesis_memory_id` updated
- LLM dedup (deterministic model): insert 2 near-duplicate memories; run `ReviewCluster`; fixture returns `supersede_a`; verify memory A is superseded

**E2E tests:**
- None — covered by dedicated E2E task 075 and 086

## Implementer Notes

- The passive injection cooldown is tracked in-memory per session using a `map[sessionID]map[memoryID]turnNumber`. This data is not persisted; the cooldown resets on service restart. This is acceptable per the spec — the cost of re-injecting the same memory after a restart is low.
- The composite relevance score recency half-life differs by `memory_type`: 30 days for episodic, 180 days for semantic/procedural/preference/entity_profile. The scorer must look up the memory type before applying the half-life. `execution_summary` type uses episodic half-life (30 days).
- The file-backed freshness scan requires access to the project's repository file system. In V2, this is done through the file tool abstraction (task 057). The `FileScanner` depends on `FileReader` interface, which is injected. In tests, use a mock `FileReader` that returns predefined file contents.
- Entity synthesis is the only pipeline stage that writes back to `memory_entity` (updating `synthesis_memory_id`). All other pipeline stages only write to `memory` and related tables. The `memory_entity` update must be in the same transaction as the new `memory` row creation to ensure consistency.
- When running multiple extraction and compaction jobs concurrently, the dedup review job may race with new memory creation. The `memory_dedup_reviewed` unique constraint on pair order prevents duplicate pair records. The dedup job should use `SELECT ... FOR UPDATE SKIP LOCKED` when fetching `memory_dedup_reviewed` rows with `decision='deferred'` to prevent two workers from reviewing the same pair simultaneously.

> ⚠️ ISSUE #9 (AMBIGUOUS): `memory_source.session_id` is a soft reference (no SQL FK). The retrieval pipeline does not query `memory_source` directly — it queries `memory` rows filtered by scope. The soft reference only matters for the API layer (task 042). Do not add a hard FK here; document in the entity synthesis and retrieval pipeline that `session_id` integrity is application-layer only.

> ⚠️ ISSUE #10 (AMBIGUOUS): `memory_source.source_id` for `source_type='event'` or `source_type='file'` has no DB FK enforcement. The retrieval pipeline does not depend on `source_id` for any query logic. This is noted here for awareness; the gap is fully in the API layer (task 042) and provenance reporting.
