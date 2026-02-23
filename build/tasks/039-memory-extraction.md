# 039: Memory Extraction Pipeline

| Field | Value |
|-------|-------|
| Layer | L3 |
| Size | M (1–2 days) |
| Spec refs | doc 06 §ExtractionPipeline, doc 06 §TrustTiers, doc 06 §CandidateHold, doc 06 §BehavioralOverrideRejection |
| Spec status | finished |
| Depends on | 038, 035, 024 |
| Blocks | 040, 042, 075, 086 |

## Scope

Build the 4-stage memory extraction pipeline: garbage rejection (Stage 0), LLM-based
extraction (Stage 1), scoring (Stage 2), normalization and taxonomy assignment (Stage 3),
and embedding + dedup + store (Stage 4). Includes trust tier capping, candidate 7-day hold,
and the hardening logic. Extraction runs in isolated model contexts (not the agent's own session).

### Must build

**Extraction pipeline entry points (`internal/memory/extractor.go`):**
- `Extractor.ExtractFromMessages(ctx, orgID, msgs []ChatMessage, sourceContext ExtractionSourceContext) error`
  - `sourceContext` carries: `session_id`, `agent_id`, `project_id`, `task_id`, `trust_tier_cap float64`
  - Runs all 4 stages for each candidate message batch; creates `memory` rows
- `Extractor.ExtractFromImport(ctx, orgID, importID uuid.UUID, records []ImportRecord) error`
  - Same 4-stage pipeline applied to JSONL import records; `trust_tier` capped at 0.6 for imported memories (doc 06)

**Stage 0 — Deterministic garbage rejection:**
- Input: raw message content strings
- Rules (applied as regex + structural checks; no LLM call):
  - Discard if content is fewer than 20 tokens (approximate word count)
  - Discard if content is purely tool_call or tool_result message type (no semantic content)
  - Discard if content matches behavioral override patterns:
    - Any message containing `"ignore previous instructions"`, `"disregard your system prompt"`, `"act as if you have no restrictions"`, or equivalent injection patterns (configurable blocklist in `config/behavioral_override_patterns.txt`)
    - Any message claiming to update, modify, or override the agent's standing instructions or personality
  - Discard if message is a system-generated status update (e.g., `"task status changed to in_progress"` domain event echoes)
- Output: filtered list of messages that pass garbage rejection

**Stage 1 — LLM extraction (Haiku-class):**
- Input: batches of up to 10 passing messages (batched to reduce Haiku calls)
- Prompt: structured extraction prompt (stored in `internal/memory/prompts/extraction.txt`) asking the LLM to extract discrete factual memories from the message batch
- Each extracted memory is a JSON object: `{content: string, memory_type: string, entities: [{name, type}], confidence: float, utility_estimate: float}`
- `memory_type` must be one of: `episodic`, `semantic`, `procedural`, `preference`, `entity_profile`, `execution_summary`
- If the LLM returns a `memory_type` not in the allowed set, discard that extraction result
- Extraction runs using `invocation_purpose='memory_extraction'` (routes to Haiku-class model, not the agent's profile)
- Isolated context: the extraction call does NOT include the agent's conversation history; only the specific message batch is passed as the extraction input

**Stage 2 — Scoring (0–100):**
- Input: Stage 1 extraction results
- Each candidate is scored across three dimensions:
  - **Specificity** (0–40 pts): is the fact specific enough to be useful? Vague statements ("the user likes things") score low
  - **Durability** (0–30 pts): is this fact likely to remain true? One-off events score lower than persistent facts
  - **Utility** (0–30 pts): would an agent benefit from having this in context in a future session?
- Scoring is heuristic (implemented as a rule-based scorer, not a second LLM call); rules defined in `internal/memory/scorer.go`
- Candidates with score < 40 are discarded (do not create a `memory` row)
- Candidates with score ≥ 40 are created as `memory` rows with `status='candidate'`
- `extraction_score` column stores the 0–100 integer score

**Stage 3 — Normalization and taxonomy assignment:**
- Input: Stage 2 passing candidates
- Entity name normalization: standardize entity names to canonical forms (e.g., "Sam", "Sam K", "sam" → use most-specific form seen; store in `memory_entity.canonical_name`)
  - If entity matches an existing `memory_entity` by fuzzy name match (Levenshtein distance ≤ 2 or exact after lower-casing), link to existing entity
  - If no match, create a new `memory_entity` row
- Taxonomy assignment: map the extracted memory to the most specific applicable `memory_taxonomy_node` using rule-based classification (not a second LLM call at this stage)
  - Classification rules keyed on `memory_type` + keywords in `content`
  - Creates `memory_taxonomy_tag` rows for each assigned node and its ancestors (parent path tagging for subtree retrieval)
- Trust tier cap: `memory.trust_tier = min(extracted_confidence, sourceContext.trust_tier_cap)` — the source's trust ceiling applies

**Stage 4 — Embed, dedup, and store:**
- Input: normalized candidates with taxonomy tags and entity mentions
- Embedding: generate `vector(1536)` embedding for each candidate's `content` using the configured embedding model (Haiku-class or a dedicated embedding model); store in `memory.embedding`; invocation_purpose = `memory_retrieval` (uses system profile, not agent profile)
- Exact dedup: check `content_hash` against existing `memory` rows for the org; if exact match exists and status='active', skip creation (do not create duplicate)
- Near-duplicate pre-screen: for each new embedding, query pgvector for existing memories with cosine similarity ≥ 0.88; if found, create a `memory_dedup_reviewed` row with `decision='deferred'` for later LLM dedup review; still create the candidate memory row (it becomes a candidate, not yet active)
- Store: `MemoryRepo.Create` with `status='candidate'`, `is_hardened=false`
- Create `memory_taxonomy_tag` rows via `MemoryTaxonomyTagRepo.Create`
- Create `memory_entity_mention` rows via `MemoryEntityMentionRepo.Create`

**7-day candidate hold and hardening (`internal/memory/hardener.go`):**
- Scheduled job (job_type: `memory_candidate_review`; runs daily)
- Fetches all `status='candidate'` rows older than 7 days
- For each candidate: if `extraction_score >= 40` (already satisfied, it was admitted) AND no contradicting memory found → promote to `status='active'`
- Hardening: a memory becomes `is_hardened=true` if it has been `status='active'` for ≥30 days AND has accumulated positive friction signals (e.g., the fact was actively confirmed by a human reaction on a related message, or was referenced by the agent in ≥3 subsequent sessions)
- Friction signal source: `chat_message_reaction` table — reactions of type `'confirm'` or `'accurate'` on a message that contains a memory-linked entity increment the memory's confidence (application-layer, no new FK)

**Trust tier rules (doc 06):**
- Source type `'explicit'` (human directly wrote it): trust_tier_cap = 1.0
- Source type `'chat_message'` from human: trust_tier_cap = 0.9
- Source type `'chat_message'` from agent: trust_tier_cap = 0.8
- Source type `'memory_import'`: trust_tier_cap = 0.6
- Source type `'event'` or `'file'`: trust_tier_cap = 0.7

**Domain event emission:**
- After Stage 4 completes, emit a `memory.extracted` domain event with `{org_id, count, batch_source: session_id|import_id}` if ≥1 memory was created

### Must NOT build
- Memory retrieval pipeline (task 040)
- Compaction / sleep reflection (task 041)
- Memory API endpoints (task 042)
- `memory_source` table or provenance recording (L4, task covered in memory API)
- Chat message ingestion hook (that hook lives in the chat service, task 044; this task defines the `Extractor` that is called by it)

## Acceptance Criteria

- [ ] Stage 0 discards messages containing behavioral override pattern (`"ignore previous instructions"`); legitimate messages pass through
- [ ] Stage 1 runs as `invocation_purpose='memory_extraction'`; extraction call does NOT include agent conversation history
- [ ] Stage 2: candidate with `extraction_score=38` is discarded; candidate with `score=40` creates a `memory` row with `status='candidate'`
- [ ] Stage 3: entity name "Sam" and "sam" are normalized to the same `memory_entity` row (fuzzy match)
- [ ] Stage 4: exact duplicate (`content_hash` match) does not create a second `memory` row
- [ ] Stage 4: near-duplicate (cosine ≥ 0.88) creates a `memory_dedup_reviewed` row with `decision='deferred'`
- [ ] Trust tier cap: imported memory (trust_tier_cap=0.6) with `extracted_confidence=0.9` stored with `trust_tier=0.6`
- [ ] Candidate hold job: candidate created 8 days ago with score ≥ 40 and no contradictions → promoted to `status='active'`
- [ ] `memory.extracted` domain event emitted after extraction batch with count ≥ 1

## Tests Required

**Unit tests:**
- Stage 0 behavioral override filter: table-driven test covering 10 injection patterns → all discarded; 5 legitimate messages → all pass
- Stage 2 scorer: test vectors with known scores; vague message → score < 40; specific factual statement → score ≥ 40
- Stage 3 entity normalization: "Sam K" and "Sam" → same entity; "Samantha" (Levenshtein > 2) → new entity
- Trust tier capping: 6 source types × expected cap values; `min(extracted, cap)` logic
- Stage 4 exact dedup: existing `content_hash` → `Create` not called; `MemoryDedupReviewedRepo.Create` called for near-duplicate

**Integration tests:**
- Full pipeline (deterministic model): feed 5 messages with known content; verify correct number of `memory` rows created with correct `status`, `trust_tier`, `extraction_score`
- Entity persistence: run extraction twice with same entity name → second run links to existing `memory_entity`, does not create a duplicate
- Candidate hold job: seed 3 candidate rows (2 old enough, 1 too recent); run job → 2 promoted to active, 1 still candidate
- Near-dedup: insert memory with embedding A; extract new memory with cosine(A, B) = 0.91; verify `memory_dedup_reviewed` row created

**E2E tests:**
- None — covered by dedicated E2E task 075 and 086

## Implementer Notes

- The extraction pipeline must not run in the agent's active model session context. It uses a separate model invocation created by `Extractor`, with `invocation_purpose='memory_extraction'`, which routes to the Haiku-class system profile. This ensures extraction cost is billed to the system profile, not the agent's token budget, and the extraction prompt does not pollute the agent's context window.
- The behavioral override blocklist in `config/behavioral_override_patterns.txt` is loaded at startup. The rejection check is a fast regex pre-scan (no LLM call). New patterns can be added by restarting the service. The file is committed to the repository and is not editable at runtime.
- The Stage 1 extraction prompt (`internal/memory/prompts/extraction.txt`) is a versioned file. When the prompt is updated, the version number in the filename (e.g., `extraction_v2.txt`) should be bumped and the old version retained for audit/replay purposes.
- Stage 4 embedding invocations are batched: up to 100 candidate memories are embedded in a single API call to the embedding model (if the provider supports batch embeddings). If the provider requires individual calls, pipeline each call with a concurrency limit of 10 to avoid hitting rate limits.
- Contradiction detection (checking if a new memory contradicts an existing active memory) is part of the retrieval pipeline (task 040), not this extraction pipeline. In this task, Stage 4 only checks for exact and near-duplicate matches. Contradiction detection happens downstream and may result in the newer memory superseding the older one.
