# Memory System Research Report

> Generated 2026-02-22 from deep research across 7+ systems, academic papers, and V1 empirical data.
> Purpose: Identify additions to docsv2/06-memory.md based on external research.
> Status: Pending review — items to be discussed and selectively applied to the spec.

## Sources Researched

### Primary Sources (user-provided)
1. **Spark Intelligence** (vibeforge1111/vibeship-spark-intelligence) — 20,600-line Python local-first AI memory/advisory system
2. **Beads** (steveyegge/beads) — Graph-based issue tracker as structured agent memory, Dolt-backed
3. **Ars Contexta** (agenticnotetaking/arscontexta) — Research-grounded knowledge management via derivation engine
4. **web4.ai** — Autonomous AI agent framework (limited relevance to memory)
5. **KSimback tweet** — OpenClaw memory guide (three failure modes, configuration fixes)
6. **thejayden tweet** — Could not access content

### Secondary Sources (discovered during research)
7. **Letta Context Repositories / MemFS** — Git-backed agent memory with sleep-time compute
8. **OpenClaw Memory Docs** — Two-layer markdown + vector search architecture
9. **Supermemory** — Hook-based implicit saves, vector-graph hybrid, temporal reasoning
10. **Zep/Graphiti** — Temporal knowledge graph with bi-temporal model
11. **Google Context Engineering Whitepaper** — Sessions & memory patterns for agents
12. **Mem0** — Universal memory layer for AI agents
13. **A-Mem** (NeurIPS 2025) — Zettelkasten-style interconnected memory notes for agents
14. **MemRL** (Jan 2026) — RL-trained retrieval utility scoring
15. **LEGOMem** (AAMAS 2026) — Modular procedural memory with execution traces
16. **GAM** — General Agentic Memory via lossless storage + JIT deep research
17. **Tempr/Cara** ("Hindsight is 20/20") — Four-network memory with graph retrieval + RRF
18. **Mem-alpha** (2025) — RL-trained memory construction agent
19. **MINJA / ZombieAgent / Microsoft Security** — Memory injection attack research
20. **gskill/GEPA** (Feb 2026) — Automatic skill generation from procedural memory

---

## Items Already Incorporated into Spec (doc 06)

The following were already applied to `docsv2/06-memory.md` before this report was generated:

- Implicit capture via event bus (anti-pattern from OpenClaw's explicit-save failure)
- Global org-level taxonomy as retrieval pre-filter
- Entity synthesis as periodic pipeline step (#1 V1 improvement)
- LLM-powered dedup with cursor-based progress tracking
- Contradiction detection with supersession
- Temporal bounds (valid_from, valid_until) from Zep/Graphiti
- Quality gates with multi-dimensional scoring from Spark
- Injection cooldown for passive retrieval (from Spark's advisory cooldown)
- Chat reaction feedback loop
- Agent bootstrapping via normal retrieval pipeline
- Extraction isolation (fresh context per phase, from Ars Contexta)
- File-backed memories as augmentation layer
- JSONL importer for bulk historical data
- Four-stage retrieval: scope → taxonomy → subtree → vector ranking
- All V1 experimental findings (what worked, what didn't)

---

## Proposed Additions — Tier 1 (High Impact, Discuss Now)

### 1. Memory-to-Memory Links

**Source:** A-Mem (NeurIPS 2025), Tempr/Cara paper

**What we have:** Entity mentions as indirect connections (Memory A → Entity X ← Memory B).

**What's missing:** Direct memory-to-memory links with typed relationships: `causal`, `supports`, `contradicts`, `elaborates`, `consequence_of`. These capture connections that don't share entities — "this pattern is a consequence of that decision."

**Proposed schema addition:**
```sql
create table memory_link (
  id                uuid primary key default gen_random_uuid(),
  source_memory_id  uuid not null references memory(id) on delete cascade,
  target_memory_id  uuid not null references memory(id) on delete cascade,
  link_type         text not null,  -- causal, supports, contradicts, elaborates, consequence_of
  confidence        real not null default 0.5,
  created_at        timestamptz not null default now(),

  unique (source_memory_id, target_memory_id, link_type)
);

create index idx_memory_link_source on memory_link(source_memory_id);
create index idx_memory_link_target on memory_link(target_memory_id);
```

**When links are created:**
- During extraction: when a new memory relates to existing ones
- During consolidation/reflection: when Ellie identifies connections between existing memories
- During entity synthesis: when synthesizing reveals causal chains

**Retrieval use:** When a memory is retrieved, optionally pull linked memories as additional context. Also enables graph traversal as a retrieval channel (see item 7).

**Effort:** Low-medium. One new table, linking logic in extraction and consolidation.

---

### 2. Memory Security / Injection Defense

**Source:** MINJA, ZombieAgent (Radware late 2025), Microsoft AI Security (Feb 2026), A-MemGuard, Palo Alto Unit 42

**What we have:** Nothing. The spec has zero security mechanisms.

**The threat:** Malicious content in processed emails, imported data, or external documents can plant memories that poison future agent behavior. Real documented attacks:
- Planted memories that exfiltrate data via URL-encoded side channels
- Injected memories that establish false delegation authority
- Regular user queries that poison long-term memory through seemingly benign interactions

**Proposed additions:**

**A. Source trustworthiness tiers:**

| Source | Trust Level | Initial Confidence Cap |
|---|---|---|
| Human direct statement | Highest | 1.0 |
| Human reaction/correction | High | 0.9 |
| Agent extraction from internal conversation | Medium-high | 0.8 |
| Agent extraction from task execution | Medium | 0.7 |
| File-backed (internal project files) | Medium | 0.7 |
| Imported historical data | Medium-low | 0.6 |
| External content (emails, web, uploaded docs) | Low | 0.4 |

Memories from low-trust sources should have mandatory TTL unless explicitly reinforced by a higher-trust source.

**B. Belief drift detection:**
Track the rate at which memories about an entity change. If an entity's facts are being rapidly contradicted and superseded (e.g., 3+ supersessions in 24 hours), trigger an alert instead of automatic supersession. This prevents an attack where an adversary gradually shifts an entity's definition through repeated contradictions.

**C. Memory provenance chain:**
Already partially covered by `memory_source` table. Ensure every memory has at least one source record. Memories without provenance should be flagged. For imported data, the import job ID is the provenance — but imported memories should carry the lower trust tier.

**D. Sensitive memory classification:**
Add a `sensitivity` field to the memory table: `public`, `internal`, `restricted`. Memories extracted from conversations about credentials, API keys, personal information, or security configurations should be auto-classified as `restricted` and excluded from broad retrieval unless specifically queried.

**Effort:** Medium. Schema additions, trust tier logic in extraction, drift detection in consolidation.

---

### 3. Sleep-Time Reflection Agent

**Source:** Letta sleep-time compute, Spark Intelligence's EIDOS distillation

**What we have:** Periodic consolidation jobs: dedup, entity synthesis, decay, distillation.

**What's missing:** Open-ended reflection — an LLM reasoning holistically about recent activity, identifying patterns and connections the extraction pipeline wouldn't catch. This is different from distillation (which turns specific episodic memories into semantic ones). Reflection is broad: "looking back at this week, what patterns emerge? What connections exist between events in different projects? What should we be paying attention to?"

**Proposed addition:**
Add a `reflection` consolidation run type. Scheduled during low-activity periods (Ellie's "sleep time"). The reflection job:

1. Gathers recent episodic memories (last N days, configurable)
2. Groups by project and taxonomy
3. LLM reflects holistically on each group: patterns, connections, emerging trends, potential issues
4. Generates new semantic or procedural memories from reflections
5. Identifies cross-project connections (e.g., "Project A and Project B are both hitting the same API rate limit")
6. Generates new memory-to-memory links discovered through reflection

This is where procedural memories ("deploying on Fridays is risky") naturally emerge, rather than waiting for the same episodic pattern to hit a hard threshold in distillation.

**Effort:** Medium. New consolidation job type, LLM prompting, scheduling logic.

---

### 4. Attention-Aware Injection Ordering

**Source:** Google Context Engineering whitepaper, transformer attention research

**What we have:** Top-k results injected into context. Ordering not specified.

**What's missing:** Transformer attention has a known recency bias — items at the end of context (closest to the query/prompt) have more influence on the response than items at the beginning. This is well-documented in the "lost in the middle" research.

**Proposed addition:**
When injecting memories via passive injection, order them so the **most relevant memories appear last** (closest to the agent's current message). Less relevant memories go first. This is a trivial change to injection ordering that provides a free improvement in memory influence on agent responses.

Also: within injected memories, place the memory content before the metadata (source, confidence, etc.), not after. The agent should see the claim first, attribution second.

**Effort:** Trivial. Sorting order in the injection builder.

---

### 5. Memory-as-a-Tool Pattern

**Source:** Google Context Engineering whitepaper, OpenAI Cookbook

**What we have:** Two retrieval modes: passive injection (automatic, every turn) and active query (@mention Ellie, human-initiated).

**What's missing:** Agent-initiated retrieval. When an agent encounters uncertainty mid-turn, it should be able to query memory directly without requiring a human @mention.

**Proposed addition:**
Expose `memory.query` as a tool available to all agents in their tool set. The tool:
- Takes a natural language query
- Runs through the same four-stage retrieval pipeline
- Returns relevant memories to the agent's current context
- Is cheaper/faster than full passive injection (which runs every turn regardless)

This sits between the two existing modes: it's automatic (no human needed) but targeted (only fires when the agent recognizes a knowledge gap). Particularly valuable during async task execution where agents encounter unexpected situations.

**Effort:** Low. New tool definition wrapping existing retrieval pipeline.

---

## Proposed Additions — Tier 2 (Important, Worth Adding)

### 6. Supersession Chain for Temporal Queries

**Source:** Zep/Graphiti bi-temporal model

**What we have:** Memories archived with `archived_reason = 'superseded'` when contradicted. Archived memories are not retrievable.

**What's missing:** The ability to answer "what did we believe about X before the change?" or "show me the history of our understanding of Y."

**Proposed schema addition:**
Add to `memory` table:
```sql
superseded_by uuid references memory(id)  -- FK to the memory that replaced this one
```

Superseded memories remain archived (not returned in standard retrieval) but are traversable via the supersession chain. A temporal query can follow the chain backward to reconstruct belief history.

**Effort:** Low. One column, update supersession logic.

---

### 7. Graph Traversal as a Retrieval Channel

**Source:** Tempr/Cara "Hindsight is 20/20" paper, A-Mem

**What we have:** Pure vector similarity within taxonomy-filtered set.

**What's missing:** V1 showed BM25+vector hurts (-5pp). But graph traversal is fundamentally different from BM25. It finds memories that aren't semantically similar to the query but are structurally connected to relevant entities.

**Proposed addition:**
After taxonomy filtering (Stage 3), run two parallel retrieval channels:
- **Vector similarity** (existing): cosine distance on embeddings
- **Graph traversal** (new): identify entities in the query → find all memories mentioning those entities via `memory_entity_mention` → optionally one hop to related entities → collect those memories

Fuse results via Reciprocal Rank Fusion (RRF) or simple union with dedup.

**Key difference from BM25:** BM25 matches keywords, which dilutes semantic queries. Graph traversal matches structural connections, which captures "memories about the same things" without requiring textual similarity.

**Should be experimentally validated** — test whether vector + graph outperforms pure vector, unlike BM25 + vector which regressed.

**Effort:** Medium. Entity extraction from queries, traversal logic, RRF fusion.

---

### 8. Task-Completion-Triggered Consolidation

**Source:** Google Context Engineering whitepaper, general pattern

**What we have:** Periodic batch consolidation. Task-scoped memories auto-archived when task completes.

**What's missing:** Task completion is the natural consolidation trigger. When a task completes:
1. Evaluate task-scoped memories — promote valuable ones to project scope
2. Distill episodic memories from the task into semantic memories
3. Extract procedural memories from the task execution pattern
4. Run entity synthesis for entities heavily mentioned in the task

Don't wait for the periodic batch. The end of a task is when the most useful consolidation happens — the full context of what was done and why is freshest.

**Proposed addition:**
Subscribe to task completion events on the event bus. When a task reaches `done` status, trigger an immediate consolidation pass on that task's memories.

**Effort:** Low. Event subscription, consolidation job parameterized by task_id.

---

### 9. Opinion Confidence Dynamics

**Source:** Tempr/Cara four-network memory model

**What we have:** Contradiction detection that archives superseded memories uniformly.

**What's missing:** Preferences/opinions should have different lifecycle dynamics than facts. When a fact is contradicted, supersession is correct. When an opinion/preference is contradicted, it should lose confidence but not be archived — preferences can be context-dependent and reaffirmed later.

**Proposed addition:**
In contradiction detection, check the memory kind:
- Facts (`fact`, `decision`, `context`): supersede on contradiction (existing behavior)
- Opinions (`preference`, `pattern`, `lesson`): decrease confidence by configurable step (e.g., -0.15 per contradiction). Only archive if confidence drops below threshold (e.g., 0.2). Increase confidence on reaffirmation (+0.10).

This prevents the system from over-reacting to a single contradictory statement about a preference.

**Effort:** Low. Conditional logic in contradiction detection.

---

### 10. Cross-Encoder Reranking on Active Queries

**Source:** Tempr/Cara, Frontiers in Psychology 2025 paper on cross-attention for memory retrieval

**What we have:** Vector similarity ranking for all retrieval.

**What's missing:** For active queries (where latency is acceptable), a precision-boosting reranking step.

**Proposed addition:**
After top-k vector results in an active query (@mention or memory.query tool), run a reranking step: use a cheap model (Haiku-class) to score each candidate against the query with a relevance prompt. Rerank based on these scores.

Too expensive for every-turn passive injection but valuable for explicit queries where precision matters. The V1 experiment log noted cross-encoder reranking as a queued experiment (never run).

**Effort:** Low. Additional LLM call in the active query path.

---

## Proposed Additions — Tier 3 (Longer Term / Higher Effort)

### 11. RL-Based Retrieval Utility (MemRL)

**Source:** MemRL (January 2026)

**Core insight:** Semantic similarity ≠ utility. Two memories can be equally similar to a query, but one produces good outcomes and the other doesn't. V1 decision #9 says "don't use importance in retrieval" — but that was about static importance scores set at extraction time. MemRL proposes a *separate* utility score learned from actual usage outcomes.

**Mechanism:** Add a `retrieval_utility` score that's updated whenever a memory is retrieved AND the downstream interaction receives feedback. Score formula: `(1-λ) × similarity + λ × Q_value`, where Q-values update via exponential moving average: `Q_new = Q_old + α × (reward - Q_old)`.

**Why it's different from V1's importance weighting:** V1's scores were static predictions at extraction time. MemRL's scores evolve with observed outcomes. The feedback loop is: memory retrieved → agent uses it → outcome measured → utility score updated.

**Effort:** High. Requires outcome tracking, score update logic, integration into ranking.

---

### 12. Execution Trace Storage (LEGOMem)

**Source:** LEGOMem (AAMAS 2026)

**Insight:** Procedural memory shouldn't just be "lessons learned" — it should include "here's how we did this last time." Store task execution traces (or summaries) as a distinct memory kind.

**Mechanism:** When a task completes successfully, generate a summarized execution trace: what steps were taken, in what order, what tools were used, what decisions were made. Store as kind `execution_trace` with embeddings. When a similar task arrives, retrieve the trace to give the agent a playbook.

**This is the concrete mechanism for procedural memory bootstrapping** — one of the open questions from the original proposal discussion.

**Effort:** Medium. New memory kind, trace generation on task completion, similarity-based retrieval for new tasks.

---

### 13. Skill Synthesis from Procedural Memory

**Source:** gskill/GEPA (February 2026)

**Insight:** When procedural memories accumulate around a topic (multiple patterns/lessons about the same workflow), they should consolidate into a structured procedure — a "skill candidate" that could eventually graduate to a formal skill (doc 10-skills-integration.md).

**Mechanism:** Parallel to entity synthesis: detect when multiple procedural memories cluster around the same topic/workflow. Consolidate into a structured step-by-step procedure. Add a `skill_candidate` status. Track retrieval frequency and outcome success rate. High-frequency, high-success candidates are surfaced to PM for promotion to formal skills.

**This bridges the gap between advisory procedural memory and prescriptive skills.**

**Effort:** High. New synthesis pipeline, integration with skills system.

---

### 14. Spark's EIDOS Step Struct (Prediction Tracking)

**Source:** Spark Intelligence EIDOS system

**Insight:** Every atomic agent action has a mandatory pre-commit structure: `intent`, `hypothesis`, `prediction`, `assumptions` — and a mandatory post-result structure: `result`, `evaluation`, `surprise_level`, `lesson`. This makes feedback loops deterministic.

**Key mechanism:** `surprise_level` (delta between predicted and actual outcome) automatically identifies what's worth distilling. You don't manually curate learning candidates — the prediction-evaluation gap signals them.

**Implication:** This requires changes to the agent runtime (doc 05), not just memory. Each tool call in the agentic loop would carry prediction metadata. Ellie would extract from these structured records rather than from free-form conversation.

**Effort:** Very high. Cross-cutting concern affecting agent runtime, tool execution, and memory extraction.

---

### 15. Lossless Source Archive with JIT Deep Research (GAM)

**Source:** GAM (General Agentic Memory)

**Insight:** All extraction approaches inevitably lose details. Keep raw conversation segments indexed and searchable as a "source archive" alongside extracted memories. When complex queries yield low-confidence results from standard retrieval, fall back to deep research over the source archive.

**This is your safety net against extraction loss.** The extraction pipeline (42% strong match, 55% weak, 3% missed from V1) will always miss things. A lossless archive ensures those missed details are still findable.

**Mechanism:** Index raw conversation segments (or progressive summaries from doc 02) in a searchable store. Expose as a fallback retrieval path for Ellie's active query mode.

**Effort:** High. Requires separate indexing of raw/summarized conversations, deep search agent logic.

---

### 16. Cross-Session Text Fingerprint Dedup

**Source:** Spark Intelligence advisory engine

**Insight:** When multiple agents are working in parallel sessions, the same memory can be injected into all of them simultaneously. Block the same memory text from being injected across parallel sessions for 600 seconds.

**Mechanism:** Text fingerprint (hash of memory content) with a global TTL cache. Before injection, check if this fingerprint was recently injected into another session. If so, deprioritize.

**Note:** This is different from the per-session injection cooldown already in the spec. This is *cross-session* dedup to prevent the same high-relevance memory from dominating all concurrent agents.

**Effort:** Low. Hash cache with TTL.

---

### 17. Dynamic Cross-Scope Access Grants

**Source:** Collaborative Memory (ICML 2025)

**Insight:** Current scope model is static (set at creation, immutable). But sometimes an agent in Project A discovers something relevant to Project B. There should be a mechanism to grant cross-scope access for specific memories.

**Proposed schema addition:**
```sql
create table memory_access_grant (
  id            uuid primary key default gen_random_uuid(),
  memory_id     uuid not null references memory(id) on delete cascade,
  granted_to_project_id uuid references project(id),
  granted_to_agent_id   uuid references agent(id),
  granted_by    uuid not null,  -- agent or human who granted access
  expires_at    timestamptz,    -- optional TTL
  created_at    timestamptz not null default now()
);
```

Also consider: when a task completes, some task-scoped memories should be auto-promoted to project scope if they contain broadly applicable learnings (covered by item #8 above).

**Effort:** Medium. New table, grant/revoke logic, retrieval integration.

---

## Detailed Technical Notes from Spark Intelligence

These are specific mechanisms from Spark's implementation worth recording for reference:

### Quality Gate Dimensions (Meta-Ralph)
6 dimensions scored 0-2 each (max 12):
- Actionability (specific action with verb+object)
- Novelty (genuine insight vs. already obvious)
- Reasoning (explicit because/causality)
- Specificity (context-specific vs. generic)
- Outcome-Linked (validated outcome vs. none)
- Ethics (positive-sum vs. harmful)

Pass threshold: ≥4. Score 2-3: eligible for auto-refinement. <2: rejected.

### Garbage Pattern Auto-Detection
Explicit rejection list (more reliable than learned classifiers):
- Tool sequences ("read → edit → write")
- Success rate metrics ("100% over 1794 uses")
- Markdown headers without content
- Transcript artifacts
- Timing metrics ("Response time: 342ms")
- Raw code (>40% syntax indicators)
- Circular reasoning (same word stem on both sides of because-clause)
- Tautologies (same 4-char fragment in 3+ words)

### Source Quality Tiers in Retrieval Ranking
```
score = 0.45 × relevance + 0.30 × quality + 0.25 × trust
```
Trust tiers: eidos distillations (0.90), replay (0.85), self-awareness (0.80), trigger (0.75), opportunity (0.72), conversation/mind/chip (0.65), bank (0.40).

### Advisory Authority Levels
- ≥0.95 BLOCK: prevents the tool call entirely
- ≥0.80 WARNING: prominent advisory
- ≥0.42 NOTE: standard note
- ≥0.30 WHISPER: low-key suggestion
- <0.30 SILENT: log only

### Temporal Half-Life Decay by Category
45–180 day half-lives depending on insight category. Self-awareness insights decay faster (45 days) than established reasoning patterns (180 days).

### The Open-Loop Problem (Their Own Finding)
Spark's feedback files are write-only — data accumulates but doesn't flow back into the hot retrieval path in real-time. The prediction loop runs at batch/interval cadence. Recommendation for OtterCamp: design reliability updates to be synchronously available to retrieval (even if eventually consistent).

---

## Detailed Technical Notes from Beads

### Compaction Tiers
- Tier 1 at 30 days closed: full text → 3-section summary (Summary, Key Decisions, Resolution). Size check: compaction that makes content larger is rejected.
- Tier 2 at 90 days closed: further reduction from Tier 1 output.
- Original content preserved in `issue_snapshots` table.

### Wisp System (Ephemeral Records)
Wisps are stored in `dolt_ignored` tables — excluded from version history. Types: heartbeat, ping, patrol, gc_report, recovery, error, escalation. Each type has different TTL semantics.

**Relevance to OtterCamp:** High-frequency agent coordination signals (status updates, health checks) should NOT be stored in the same tables as durable memories. Consider a separate ephemeral store with TTL policies.

### Content-Hash for Idempotent Import
SHA256 of canonical content on every record. Enables change detection and dedup without full content comparison. Useful for our importer.

### Adaptive ID Length
Birthday paradox calculation: `P(collision) ≈ 1 - e^(-n²/2N)` where N = 36^length. Hash length auto-scales with database size. At 25% collision threshold: 3 chars for ~160 items, 4 for ~980, 5 for ~5.9K.

---

## Detailed Technical Notes from Ars Contexta

### Derivation Engine Signal Weights
| Level | Weight | Trigger |
|---|---|---|
| HIGH | 1.0 | Explicit statement, domain-specific language |
| MEDIUM | 0.6 | Implicit tone, general preference, domain defaults |
| LOW | 0.3 | Ambiguous phrasing, contradicted by others |
| INFERRED | 0.2 | Cascade from resolved dimensions |

Dimension resolved when cumulative confidence > 1.5.

### 8 Configuration Dimensions
Granularity, Organization, Linking, Processing, Navigation, Maintenance, Schema, Automation. Each has 2-3 positions. Cascade resolution propagates constraints between dimensions.

### Operational Learning Loop
- Observations (friction signals): threshold 10 → surface `/rethink` suggestion
- Tensions (contradictions): threshold 5 → surface `/rethink` suggestion
- Session count: threshold 5 → surface session mining suggestion

**Relevance to OtterCamp:** Ellie should track "friction signals" — cases where retrieval failed, memories contradicted agent behavior, or humans corrected agent responses. When friction accumulates above a threshold, trigger a reflection/rethink pass.

### Three-Space Architecture Failure Modes
Six documented failures from conflating spaces:
1. Ops into Notes → search returns processing debris
2. Self into Notes → schema confusion
3. Notes into Ops → insights lost when ops archived
4. Self into Ops → identity drifts
5. Ops into Self → self too large to load
6. Notes into Self → self bloats, search misses content

**Relevance:** Validates our scope separation. Particularly: agent-private memory (self) must be separate from shared memory (notes) and operational state (ops).

### Discovery-First Invariant
"If an agent can't find a note, the note doesn't exist." Everything created must be optimized for future agent discovery using only title and description.

**Relevance:** Every memory should be discoverable by a fresh agent that has never seen it before. This means taxonomy tagging, entity mentions, and embedding quality all matter for discoverability — not just for relevance ranking.

---

## Summary: Priority-Ranked Additions

| # | Addition | Effort | Expected Impact | Spec Section Affected |
|---|---|---|---|---|
| 1 | Memory-to-memory links | Low-Med | High — enables graph retrieval, multi-hop reasoning | New table, extraction, consolidation |
| 2 | Memory security / injection defense | Medium | Critical for production | New section, schema additions |
| 3 | Sleep-time reflection agent | Medium | High — pattern detection, cross-project connections | Consolidation section |
| 4 | Attention-aware injection ordering | Trivial | Free improvement | Retrieval section |
| 5 | Memory-as-a-tool for agents | Low | Medium — agent-initiated retrieval | Retrieval section, tool definition |
| 6 | Supersession chain | Low | Medium — temporal query support | Schema addition |
| 7 | Graph traversal retrieval channel | Medium | Medium-High — catches structural connections | Retrieval section |
| 8 | Task-completion consolidation | Low | Medium — timely scope promotion | Consolidation section |
| 9 | Opinion confidence dynamics | Low | Low-Med — better preference lifecycle | Lifecycle section |
| 10 | Cross-encoder reranking | Low | Medium — precision on active queries | Retrieval section |
| 11 | RL-based retrieval utility | High | High (long-term) — learns what helps | New concept |
| 12 | Execution trace storage | Medium | High — repeated task performance | New memory kind |
| 13 | Skill synthesis from procedural | High | Medium — bridges memory to skills | New pipeline |
| 14 | EIDOS step struct | Very High | High — deterministic feedback | Agent runtime (doc 05) |
| 15 | Lossless source archive | High | Medium — safety net for extraction loss | New concept |
| 16 | Cross-session text fingerprint dedup | Low | Low — prevents parallel injection | Retrieval section |
| 17 | Dynamic cross-scope grants | Medium | Medium — selective sharing | New table |
