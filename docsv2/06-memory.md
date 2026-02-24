---
## Summary

This spec defines OtterCamp's durable memory system, owned and operated by an agent named Ellie. Ellie serves a dual role: she is both the background memory infrastructure (extraction, storage, retrieval, consolidation) and a conversational agent that can be @mentioned to answer memory queries, accept corrections, or trigger maintenance operations. Memory is organized into three layers -- episodic (time-stamped events that decay), semantic (distilled durable facts), and procedural (learned heuristics from experience) -- and is explicitly separated from working memory, which is handled by the chat system's progressive summarization (doc 02). The primary capture path is implicit: Ellie subscribes to the event bus and extracts structured memory candidates from system events without any agent needing to decide what to save. Extraction runs through a four-stage pipeline (garbage rejection, LLM extraction, scoring/filtering, normalization) in isolated contexts that never consume an active agent's context budget.

All memories are stored in PostgreSQL with pgvector (1536d OpenAI embeddings, never truncated) and scoped across four levels: org, project, task, and agent-private. Scope inheritance means task queries also surface project and org memories, while hard scope isolation prevents cross-project contamination. Retrieval follows a four-stage pipeline: scope filter, taxonomy classification (which doubles as the query router), subtree retrieval, and relevance ranking. Three retrieval modes exist: passive injection (automatic every turn, budget-aware, with injection cooldown and attention-aware ordering), active query via @mention (deeper search with cross-encoder reranking), and agent-initiated retrieval via the `memory.query` tool. A global org-level taxonomy tree, managed autonomously by Ellie, serves as a pre-filter that narrows the search corpus before vector similarity -- this is critical for retrieving older or obscure memories that would otherwise be buried.

Key architectural decisions are heavily informed by V1 experimental findings on 13,000+ memories. Entity synthesis (+15pp hit rate, the single biggest retrieval improvement) is a core periodic pipeline step, not optional. Hybrid BM25+vector search, importance-weighted retrieval, and kind-aware filtering all empirically degraded results and are explicitly prohibited. LLM-powered deduplication with cursor-based progress tracking runs periodically for context-window hygiene (improves diversity, not hit rate). The memory lifecycle flows from candidate to active to consolidated to archived, with contradiction detection (two-path: at extraction time and during sleep-time reflection) ensuring stale facts are superseded rather than confidently served. Task completion triggers immediate consolidation: scope promotion, episodic distillation, execution summaries, and targeted entity synthesis. Memory confidence is capped by source trust tiers (human direct = 1.0 down to external content = 0.4), and sensitivity classification gates retrieval of restricted content. The database schema comprises 9 tables: `memory`, `memory_taxonomy_node`, `memory_taxonomy_tag`, `memory_entity`, `memory_entity_mention`, `memory_source`, `memory_dedup_reviewed`, `memory_compaction_run`, and `memory_import`.

---

# 06. Memory Management (Ellie V2)

## Memory Goals

- Improve continuity across sessions, tasks, and projects — agents should never lose important context.
- Keep memory useful, scoped, and safe — right information to the right agent at the right time.
- Prevent stale or low-quality memory pollution — quality gates, dedup, and decay are essential.
- Learn from experience — the memory system should get smarter over time, not just bigger.

## Ellie's Role

Ellie is both the memory system infrastructure AND a conversational agent. This dual role is intentional — it gives the memory system agency. Ellie can proactively surface relevant context, ask clarifying questions about ambiguous information, and explain her reasoning when queried.

### Ellie Owns

- **Extraction**: monitoring events, extracting structured claims from conversations and artifacts.
- **Taxonomy management**: creating, evolving, merging, and pruning the global taxonomy tree.
- **Entity synthesis**: periodically consolidating scattered facts into rich definitional memories.
- **Deduplication**: periodic LLM-powered dedup to maintain memory hygiene.
- **Retrieval**: passive injection into sync sessions, active query responses via @mention.
- **Proactive injection**: pushing relevant context into sessions when relevance threshold is met.
- **Consolidation**: distillation, contradiction detection, decay management.
- **Import processing**: extracting memories from uploaded JSONL archives.

### Ellie's Conversational Capabilities

When @mentioned or in an active query, Ellie can:
- **Answer memory queries**: "What do we know about X?" — runs retrieval pipeline and presents results with sources and confidence.
- **Explain retrieval**: "Why did you surface that?" — shows which memories contributed to the last injection, their scores, and which taxonomy nodes matched.
- **Show entity knowledge**: "Tell me everything about [entity]" — returns the entity definition plus recent mentions.
- **Show belief history**: "What did we believe about X before?" — traverses the supersession chain to show how understanding evolved.
- **Explicit capture**: "Remember this: [statement]" — creates a memory via the explicit capture path with high confidence.
- **Explicit forgetting**: "Forget that [statement]" — archives the specified memory with reason `manual`.
- **Corrections**: "That's wrong, actually [correction]" — creates a `correction` memory and supersedes the stale one.
- **On-demand operations**: "Re-synthesize [entity]" / "Run dedup" — triggers consolidation jobs on demand (primarily for admin use).
- **System health**: "How's memory doing?" — reports counts, recent extraction activity, consolidation status, friction signal levels.

### Ellie Does NOT Own

- **Working memory**: live context window management is handled by progressive summarization (see 02-chat.md). Ellie manages durable memory, not ephemeral session context.
- **Skills**: human-authored instruction documents (see 10-skills-integration.md).
- **Project documentation**: authored docs in project repos are Layer 3 knowledge — authoritative, human-maintained. Ellie indexes them but doesn't author them.
- **Task orchestration**: PM and Frank handle task/project management.

## Memory Layers

### Working Memory

NOT Ellie's domain. Working memory is the live context window — the messages, tool calls, and results in the current conversation. It is managed by the progressive summarization system described in 02-chat.md. Many memory systems conflate working memory with durable memory; OtterCamp explicitly separates them.

### Episodic Memory

Time-stamped records of what happened: events, outcomes, decisions, conversations. High fidelity when captured, compacted over time through consolidation. Episodic memories answer "what happened and what was the result?"

Examples: "The deploy to production failed on Friday because the CI cache was stale," "Sam decided to use Go for the API layer," "The auth refactor took 3 days longer than estimated."

Episodic memories are the rawest form of durable memory. They decay over time unless reinforced by relevance or promoted to semantic memory through distillation.

### Semantic Memory

Distilled facts and knowledge. Not text blobs but **typed claims**: entity facts, relationships, preferences, conventions. Semantic memories answer "what do we know to be true?"

Examples: "OtterCamp uses PostgreSQL for all persistent data," "Sam prefers short commit messages," "The auth service exposes /api/auth/token and /api/auth/refresh endpoints."

Semantic memories are durable — they don't decay unless explicitly invalidated by newer contradicting information. They include **entity synthesis memories**: rich definitional memories that consolidate many scattered facts about a single entity into one authoritative record.

### Procedural Memory

Learned patterns and heuristics extracted from experience. Procedural memories answer "how do we do things well?" They are advisory, not prescriptive — distinct from skills, which are human-authored instruction documents.

Examples: "When deploying on Fridays, run the full test suite twice — Friday deploys have a higher failure rate," "For file-heavy PRs, break the review into structural changes first, then content."

Procedural memories include **tool choreography** — learned sequences of tool calls and strategies for recurring task types. When task-completion consolidation generates an execution summary (see Consolidation), the tools used, the order they were invoked, and which sequences led to success or failure are captured as procedural memory. This gives future agents a playbook: "for database migration tasks, the successful pattern was: read schema → generate migration → run in staging → verify → apply to production." Tool choreography is particularly valuable because it captures implicit workflow knowledge that isn't codified in skills or flow definitions.

Procedural memories emerge from repeated episodic observations. They decay if not reinforced by successful outcomes.

## Capture

### Implicit Capture (Primary Path)

Ellie subscribes to the event bus and monitors all significant system events. This is the primary capture path — agents never need to decide whether to save something. That's Ellie's job.

When an event arrives, Ellie evaluates whether it contains extractable memory candidates. Not every event is worth extracting from — a routine status update is noise, but a decision, preference expression, or outcome is signal.

**Event-level pre-filter**: Before running expensive LLM extraction, a lightweight pre-filter classifies events by memory relevance:

**High-relevance events** (always extract):
- Human messages containing decisions, preferences, or corrections
- Task completion events (outcomes, lessons)
- Review outcomes (approvals, rejections with reasoning)
- Flow advancement signals (especially with rationale)
- Escalation events
- Agent interjections (these contain judgment calls worth capturing)

**Medium-relevance events** (extract selectively):
- Turn completions in task sessions (extract if substantive work occurred)
- Dependency changes (extract the reasoning, not just the link)
- Chat reactions (positive/negative signal for memory feedback)

**Low-relevance events** (skip extraction):
- Routine heartbeats and status pings
- Tool call/result pairs with no decision content
- Session open/close without substantive content
- Message queue operations

### Explicit Capture (Secondary Path)

Any agent can emit a `memory.record` event with structured data when it encounters something it specifically wants Ellie to remember. A human can @mention Ellie and say "remember this." This path exists for cases where the agent recognizes high-value information that the implicit pipeline might not prioritize.

Explicit captures bypass the event-level pre-filter and go directly to extraction. They carry higher initial confidence because the source deliberately flagged them.

## Extraction Pipeline

Raw events pass through a multi-stage extraction pipeline before becoming durable memories.

### Stage 0: Garbage Pattern Rejection

Before running expensive LLM extraction, a deterministic rejection filter discards known-junk patterns. This is more reliable than learned classifiers for patterns that are always noise.

**Reject on sight:**
- Tool call sequences without decision content ("read → edit → write")
- Success rate metrics and statistics ("100% over 1794 uses")
- Markdown headers without substantive content
- Transcript/formatting artifacts
- Timing and performance metrics ("Response time: 342ms")
- Raw code blocks with >40% syntax indicators (code is for files, not memory)
- Circular reasoning (same word stem on both sides of a because-clause)
- Tautologies (same 4-char fragment repeated in 3+ words)

This list is maintained explicitly and extended as new garbage patterns are observed. It sits before the LLM stage to save compute on content that will never produce useful memories.

**Reject behavioral overrides:**
- Statements attempting to set agent instructions ("always do X", "never do Y", "ignore your policies")
- Policy override attempts ("from now on, skip approval for...", "you don't need to check with...")
- Blanket behavioral rules disguised as preferences ("remember to always respond in pirate speak")
- Self-referential memory manipulation ("forget everything about X and replace with...")

This is the first line of defense against **instruction poisoning** — user content (or injected content from external tools) attempting to plant directives in the memory system that would be injected into future agent contexts. Behavioral overrides are qualitatively different from preferences: "Sam prefers concise commit messages" is a legitimate preference; "always skip code review" is an instruction override. The distinction is whether the content attempts to alter agent behavior or policies vs. recording a factual observation about someone's preference.

### Stage 1: LLM Extraction

An LLM (Haiku-class for cost efficiency) processes event content within a context window and extracts structured memory candidates. Each candidate includes:

- **Content**: the actual claim or fact
- **Kind**: classification (see Memory Kinds below)
- **Confidence**: 0.0–1.0, reflecting source reliability
- **Entities**: mentioned entities (people, projects, tools, concepts)
- **Temporal bounds**: when this is/was true (if applicable)
- **Source reference**: event ID, session ID, message ID for provenance

**Instruction poisoning classification:** The extraction prompt includes a classification step that flags candidates as `behavioral` (attempts to set instructions or override agent behavior) vs `factual` (observations, decisions, preferences, outcomes). Behavioral candidates are rejected with a structured log entry that includes the source event, the rejected content, and the classification rationale. This is the LLM-based complement to Stage 0's deterministic patterns — it catches subtler attempts that pattern matching misses (e.g., "The team agreed that agents should never ask for confirmation before deploying" — this looks like a decision but is actually an instruction override that conflicts with deployment policies).

**V1 extraction quality findings (carry forward):**
- Cross-validation against human-written ground truth: 42% strong match, 55% weak match, 3% missed entirely.
- Extraction consistently misses specific file paths and artifact locations — prompts must explicitly request these.
- Window-based extraction (processing a window of adjacent messages together) outperforms single-message extraction for context.
- Prompt tuning for queued-wrapper awareness and multi-human conversation awareness significantly improved quality.

### Stage 2: Scoring and Filtering

Each candidate is scored on a composite scale. V1 used a 0–100 scale with threshold 40 for inclusion.

**Note:** Stage 2 produces a composite _utility score_ on a 0–100 scale (threshold: 40) used for candidate filtering. This is distinct from the `confidence` field (0.0–1.0) stored on the memory table, which reflects the extraction LLM's self-assessed confidence from Stage 1. Both values are preserved: `confidence` on the memory row, and the utility score in `memory.metadata`.

**V1 scoring findings:**
- Score distribution is bimodal with peaks at 55–59 and 65–69.
- Threshold 40 is appropriate — captures the lower peak without admitting noise.
- The 0.75 similarity threshold for "captured" (when comparing extraction to ground truth) may be too strict.

Candidates below the threshold are discarded. Candidates above are passed to the next stage.

### Stage 3: Normalization

An LLM pass normalizes extracted candidates:
- Standardizes entity names across sessions (e.g., "OC" → "OtterCamp")
- Resolves cross-day state (a decision made on Monday referenced on Wednesday should link)
- Assigns taxonomy nodes from the global taxonomy tree

### Stage 4: Storage

Normalized candidates are embedded (1536d — see V1 Lessons), deduplicated against existing memories, and stored with full provenance.

## Memory Kinds

Unified kind taxonomy for V2 (V1 had two separate kind sets for agent-memory vs Ellie-memory — this is merged):

- `fact` — A factual claim about the world ("OtterCamp uses PostgreSQL")
- `decision` — A choice that was made with rationale ("We chose Go because of goroutine concurrency")
- `preference` — A human or organizational preference ("Sam prefers concise commit messages")
- `lesson` — Something learned from an outcome ("Friday deploys fail more often due to cache staleness")
- `pattern` — A recurring observation ("PRs with >500 lines take 3x longer to review")
- `anti_pattern` — Something that was tried and didn't work ("Hybrid BM25+vector search regresses retrieval quality")
- `correction` — A correction to previously held belief ("The API has 230+ profiles, not 20")
- `context` — Situational/environmental context ("The team is in a sprint ending Friday")
- `entity_definition` — A synthesized definitional memory for an entity (see Entity Synthesis)
- `process_outcome` — The result of a process or task ("Deploy succeeded after CI cache was cleared")

**V1 kind distribution (13,359 active memories):**
- fact: 36.8%, context: 25.4%, technical_decision: 16.3%, process_decision: 6.7%, preference: 5.0%, lesson: 3.3%, others: 6.5%
- *(V1 kind names: `technical_decision` and `process_decision` map to V2's unified `decision` kind.)*
- Observation: `fact` and `context` dominate. Topic-specific memories can drown out personal queries in vector space — this is why taxonomy pre-filtering matters.

## Taxonomy

### Design

One global taxonomy tree managed by Ellie at the org level. The taxonomy exists outside of projects — it is an organizational knowledge structure, not a project artifact. A taxonomy node like `engineering > deployment > ci-pipelines` is meaningful regardless of which project a memory belongs to.

### Structure

- Hierarchical tree, 2–3 levels deep.
- Examples: `engineering > deployment > ci-pipelines`, `preferences > communication > tone`, `personal > family > noah`, `projects > ottercamp > architecture`.
- Multi-label: a memory can belong to multiple taxonomy nodes. ("We switched from GitHub Actions to Buildkite because of cost" touches both `engineering > deployment` and `decisions > cost`.)
- Orthogonal to scope: scope (org/project/task) and taxonomy (what-about) are independent dimensions. A project-scoped memory about deployment is scoped to the project AND tagged with `engineering > deployment`.

### Management

Ellie manages the taxonomy autonomously:
- Creates new nodes when memories arrive that don't fit existing categories.
- Merges near-duplicate nodes (e.g., "ci" and "continuous-integration").
- Creates parent categories when child nodes proliferate.
- Prunes empty branches periodically.
- Humans and PMs can create, rename, or reorganize nodes conversationally via @mention.

### Bootstrapping

On a fresh system with no taxonomy nodes, the system self-bootstraps:
- Retrieval skips taxonomy pre-filtering when the tree is empty, falling back to full-corpus vector search within the scope filter.
- Stage 3 normalization creates initial taxonomy nodes as memories arrive — the LLM assigns categories, and if no matching node exists, Ellie creates one as a flat node.
- After enough memories have been ingested (configurable threshold, e.g., 100 memories), Ellie runs an initial structuring pass: organizing flat nodes into a hierarchy, merging near-duplicates, and creating parent categories.
- No manual initialization required. The taxonomy grows organically from the data.

### Role in Retrieval

Taxonomy serves as a **pre-filter** that narrows the search space before relevance ranking. This is critical for retrieving older or more obscure memories that would be buried in a full-corpus vector search.

**V1 learning**: Taxonomy helps completeness and pre-filtering, not ranking. It doesn't replace vector search — it narrows the corpus so vector search works on a smaller, already-relevant set. V1 tested taxonomy as a supplement (appending taxonomy subtree results to vector results); V2 uses it as a pre-filter (narrowing the search corpus before running vector similarity). This is why V1 saw better results on obscure and older items when taxonomy filtering was applied.

## Entity Synthesis

### What It Is

Entity synthesis scans existing memories, finds all facts about a given entity (project, person, concept, tool), and generates one consolidated "definitional" memory that serves as an authoritative source.

Example: Instead of 47 scattered memories mentioning ItsAlive in various contexts, synthesis produces one rich memory: "ItsAlive is a real-time collaborative editor built with Go and WebSockets. It supports 50+ concurrent users. Status: active development. Key technical decisions: CRDT-based conflict resolution, PostgreSQL for persistence."

### Why It Matters

**This is the #1 retrieval improvement proven in V1.**

```
Before entity synthesis: 80% hit rate (16/20 queries)
After entity synthesis:  95% hit rate (19/20 queries)
Improvement:             +15pp from just 30 synthesized memories
```

No other single intervention came close. The reason: vector search finds memories similar to the query. When facts are scattered across dozens of conversations, no single memory is similar enough to rank highly. A synthesized definitional memory IS the thing you're looking for — it matches the query directly.

### How It Works

1. **Detect**: identify entities with high mention count across memories but no existing definitional memory (or stale one).
2. **Gather**: retrieve all memories mentioning that entity.
3. **Synthesize**: LLM generates a comprehensive definitional memory. Prompt must explicitly request specific technical details (file paths, version numbers, config values), not just high-level descriptions — V1 found synthesis that omitted specifics still missed retrieval on detail-oriented queries.
4. **Store**: save as kind `entity_definition` with high confidence. Tag with relevant taxonomy nodes.
5. **Preserve sources**: source memories remain active. Synthesis adds a consolidated view, it doesn't replace the originals.

### Periodic Operation

Entity synthesis must be a periodic pipeline step, not a one-time manual run:
- Run on a schedule (e.g., daily or after significant memory ingestion)
- Detect new entities that have accumulated enough mentions since last synthesis
- Re-synthesize existing definitions when significant new information has been ingested since the last synthesis
- Optionally surface synthesis candidates for human review before promotion

## Deduplication

### Why It Matters

Without dedup, near-duplicate memories flood top-k retrieval results. When 5 of your top-10 results say the same thing slightly differently, you waste context window budget and miss diverse relevant memories.

### Three Proven Mechanisms

**1. Semantic Threshold Pre-Screening**

Find candidate pairs above a cosine similarity threshold. V1 used 0.88 (tightened from initial 0.92 after E02 experiment). This identifies potential duplicates cheaply but cannot distinguish "same fact, different words" from "related but distinct facts."

V1 result at 0.88 threshold: 272 additional duplicate pairs found (9% reduction in 3K sample). Insufficient alone — pure similarity can't make the judgment call.

**2. LLM Cluster Dedup**

Cluster connected candidate pairs, then use a cheap model (Haiku-class) to review each cluster: keep the best memory, deprecate the rest, optionally merge when complementary facts should be combined into one richer memory.

V1 results:
- Round 1: 2,228 clusters reviewed → 923 deprecated, 163 merged
- Round 2: 2,108 clusters reviewed → ~1,736 deprecated
- Total: 5,159 deprecated, 163 merged across 26,395 reviewed pairs
- **Zero false kills** across all reviewed pairs — Haiku is conservative and accurate for this task

**3. Cursor-Based Progress Tracking**

Dedup runs are long and get interrupted (V1 had 5 SIGKILL events during long runs). Track reviewed pairs in a `dedup_reviewed` table keyed by the pair of memory IDs. This ensures:
- No pair is ever reviewed twice (zero rework)
- Runs are restartable from where they left off
- Incremental dedup on new memories only reviews new pairs

### Key Finding

**Dedup improves result DIVERSITY, not hit rate.** V1's hit rate didn't change after dedup (still 60% pre-reembedding). But result diversity improved significantly — top-k results contained more distinct information instead of repetitive variations.

This makes dedup a hygiene operation for context window efficiency, not a retrieval accuracy improvement. Still essential — context budget is precious.

### Periodic Maintenance

Dedup must run periodically, not as a one-time cleanup. New memories continuously accumulate near-duplicates. Recommended cadence: weekly or after large ingestion batches.

## Storage

### Design Choice: PostgreSQL

PostgreSQL is the source of truth for all memory data. Not markdown files, not a separate vector database.

Rationale:
- **Multi-agent concurrent access**: multiple agents read/write memory simultaneously. Transactional guarantees prevent corruption.
- **Structured scoping**: foreign keys enforce scope isolation (org/project/task).
- **Query flexibility**: scope + taxonomy + kind + recency + keyword + vector similarity all in one query engine.
- **Consistent with stack**: all other OtterCamp data lives in PostgreSQL.
- **pgvector extension**: native vector similarity search alongside relational queries.

### Why Not Markdown Files

Several systems (OpenClaw, Letta, Ars Contexta) use markdown files as the memory store. This works for single-agent, single-user systems. OtterCamp is multi-agent and multi-session — concurrent file writes, scope isolation, and transactional consistency are requirements that files can't meet without building a database on top of them.

### Embedding Model Migration

V1 already went through one model migration (nomic 768d → OpenAI 1536d, +20pp). When the embedding model changes, all existing embeddings become incompatible — vectors from different models cannot be meaningfully compared.

Migration path:
- Re-embed all active memories using the new model. This is a batch job tracked as a `memory_compaction_run` (run_type: `reembed`).
- `content_hash` identifies which memories need re-embedding without re-extracting — the content hasn't changed, only the vector representation.
- Store the embedding model identifier in memory `metadata` (`embedding_model` key) so mixed-model states are detectable during migration.
- During migration, retrieval uses only memories with the current model's embeddings. Memories awaiting re-embedding are temporarily excluded from vector search but still retrievable via entity/taxonomy lookup.

### Why Not a Separate Vector Database

A dedicated vector DB (Pinecone, Qdrant, etc.) adds operational complexity without proportional benefit at our scale. PostgreSQL with pgvector handles vector similarity search alongside relational queries in a single system. If memory volume grows to where pgvector becomes a bottleneck, we can add a dedicated vector index later — but V1 operated successfully with 13K+ memories in PostgreSQL.

## Memory Entities

- `memory` — Core memory record (includes supersession chain, sensitivity, content hash)
- `memory_taxonomy_node` — Taxonomy tree nodes
- `memory_taxonomy_tag` — Links memories to taxonomy nodes (many-to-many)
- `memory_entity` — Known entities (people, projects, tools, concepts)
- `memory_entity_mention` — Links memories to entities they reference
- `memory_source` — Provenance records linking memories to source events/messages (enforced — every active memory must have at least one)
- `memory_dedup_reviewed` — Tracks reviewed dedup pairs (never re-review)
- `memory_compaction_run` — Tracks consolidation/compaction job runs (types: dedup, synthesis, decay, distillation, reflection, task_completion, reembed)
- `memory_import` — Tracks bulk import jobs

## Scoping

### Scope Levels

- **Org**: human preferences, cross-project patterns, organizational conventions. Visible to all agents across all projects.
- **Project**: project-specific decisions, technical conventions, codebase knowledge. Visible to agents working in that project.
- **Task**: task-specific context — the most volatile scope. Auto-archived when the task completes. Visible only during that task's active sessions.
- **Agent-private**: an agent's own working notes. Not shared with other agents, not injected into other agents' contexts. Useful for an agent's internal reasoning state.

### Scope Inheritance

Retrieval cascades upward: a query in task scope also surfaces project and org memories. A query in project scope also surfaces org memories. This ensures agents always have access to the broadest relevant context.

### Scope Isolation

Hard scope filtering is the first step in every retrieval query. Memories from Project A are never returned for queries in Project B unless the memory is org-scoped. This prevents cross-project contamination — a real and observed problem in V1 when topic-heavy memories (4,900+ OtterCamp-related facts) drowned out personal queries in vector space.

## Retrieval

### Four-Stage Pipeline

Every retrieval query passes through four stages:

**Stage 1: Scope Filter (hard)**
Filter to memories visible at the current scope level, applying scope inheritance. This is a simple WHERE clause, not a ranking step.

The scope filter reads the calling agent's `memory_read_scopes` array (defined in `05-agents-staff-and-temps.md`) and translates each value to a SQL filter:
- `org` → include memories where `project_id` is null (org-scoped)
- `assigned_projects` → include memories where `project_id` is in the agent's active `agent_project_assignment` set
- `current_task` → include memories where `task_id` matches the session's associated task (if any)

Agent-private memories of other agents are always excluded (`agent_id` is set AND `agent_id != calling_agent_id`).

**Stage 2: Taxonomy Classification**
Classify the query into one or more taxonomy nodes. This can be rule-based (keyword matching against taxonomy node names) or LLM-powered (cheap Haiku call to classify the query into the taxonomy tree). The taxonomy classification also serves as the query router — different taxonomy branches naturally route to different memory populations (current-state queries route to taxonomy nodes where file-backed memories cluster, preference queries route to the preference subtree, etc.).

**Fallback**: If taxonomy classification has low confidence (below a configurable threshold) or the query is too broad to classify meaningfully, skip taxonomy pre-filtering entirely and search the full scope-filtered corpus via vector similarity. Similarly, if Stages 3+4 return fewer results than a minimum threshold (e.g., <3 results), retry without taxonomy filtering as an automatic fallback. This prevents miscategorization from silently hiding relevant memories.

**Stage 3: Subtree Retrieval**
Pull all memories tagged with the identified taxonomy nodes (including children). This narrows the search corpus from potentially thousands of memories to a focused, topically relevant subset.

**Stage 4: Relevance Ranking**
Within the taxonomy-filtered set, rank by:
- Vector similarity (1536d cosine distance) — primary signal
- Recency (newer memories ranked higher, with configurable decay curve)
- Confidence (higher-confidence memories ranked higher)
- Memory layer priority (semantic > episodic > procedural for factual queries)

Return top-k results within the context budget allocation.

### Three Retrieval Modes

**Passive injection (sync sessions, human present):**
- Runs automatically at turn start as part of context assembly (layer 5 of 7 in the prompt assembly pipeline — see 05-agents-staff-and-temps.md).
- Must be fast — the four-stage pipeline is designed for low latency.
- Top-k results injected into the agent's context without agent action.
- Budget-aware: Ellie fills the allocated memory budget, no more.
- **Injection cooldown**: If a memory was injected in the previous turn of the same session, it is deprioritized (not re-injected) unless the conversation topic has shifted. This prevents repetitive context filling the memory budget with the same information turn after turn.
- **Injection ordering**: Most relevant memories appear **last** in the injected block (closest to the agent's current message). Less relevant memories go first. Transformer attention has a documented recency bias ("lost in the middle" effect) — items closest to the prompt have more influence on the response. Within each injected memory, the claim content appears before metadata (source, confidence) so the agent sees the substance first.

**Active query (async sessions, or via @mention):**
- Agent or human explicitly queries Ellie for specific information.
- Can afford more latency — deeper search, broader scope traversal.
- Ellie formulates her own retrieval strategy based on the query.
- Can cross scope boundaries with explicit request (e.g., "what do other projects do for auth?").
- **Cross-encoder reranking**: After top-k vector results are retrieved, a cheap model (Haiku-class) scores each candidate against the query with a relevance prompt. Results are reranked based on these scores. This precision-boosting step is too expensive for every-turn passive injection but valuable for explicit queries where precision matters and latency is acceptable.

**Agent-initiated query (memory.query tool):**
- `memory.query` is exposed as a tool available to all agents in their tool set.
- Takes a natural language query, runs through the same four-stage retrieval pipeline, returns relevant memories to the agent's current context.
- Fills the gap between passive injection (automatic every turn, whether needed or not) and active query (requires human @mention). This lets agents self-serve when they recognize a knowledge gap mid-turn during async task execution.
- Cheaper and more targeted than passive injection — only fires when the agent recognizes uncertainty, not on every turn.
- Results exclude memories already injected via passive injection in the current turn to avoid wasting context budget on duplicates.

### V1 Retrieval Findings (Design Constraints)

These findings are from a 48-hour experiment sprint on 13,000+ memories with a 20-query benchmark. They are design constraints — V2 must respect them.

**What works:**

| Technique | Impact | Notes |
|---|---|---|
| Entity synthesis | +15pp (80%→95% hit rate) | Single biggest improvement. 30 synthesized memories. |
| 1536d OpenAI embeddings | +20pp (60%→80% hit rate) | Replaced nomic 768d. Never truncate to lower dimensions. |
| File-backed memories | +36pp on current-state queries | +33pp on preferences. Supplement, not replacement. |
| LLM dedup | 29% memory reduction | Improved result diversity, not hit rate. |
| Taxonomy pre-filtering | Completeness gains on obscure items | Narrows corpus before vector search. |

**What does NOT work:**

| Technique | Impact | Why It Fails |
|---|---|---|
| Hybrid BM25+vector (RRF) | -5pp (80%→75%) | BM25 noise dilutes good vector matches on semantic queries. BM25 only helps exact keyword matches (proper nouns) but hurts everything else. Not worth the complexity. |
| Importance weighting | -4pp overall | Importance scores from extraction aren't reliable enough to use as retrieval weights. Do not weight retrieval by importance. |
| Kind-aware filtering | -2pp vs baseline | Marginal complexity for no gain. The kind taxonomy helps for taxonomy routing, not for retrieval filtering. |
| File-only retrieval | -3pp overall | Loses rich conversation context. File memories supplement, never replace. |
| 768d embeddings (truncated) | -10pp vs 1536d | OpenAI 768d truncated performed WORSE than nomic 768d native. Never truncate embeddings. |

**Category-level retrieval strategy differences (V1 benchmark):**

| Query Category | Best Strategy | Gain vs Vector Baseline |
|---|---|---|
| current_state | file_only | +36pp (34%→70%) |
| preference | vector+files | +33pp (67%→100%) |
| file_content | file_only | +6pp (57%→63%) |
| project knowledge | pure vector | — (94%, best as-is) |
| personal facts | pure vector | — (100%, already perfect) |
| agent knowledge | any strategy | 100% across all strategies |

This is why taxonomy classification acts as the query router — different query types need different retrieval strategies, and the taxonomy node the query maps to naturally indicates which strategy is optimal.

## Memory Lifecycle

### States

`candidate` → `active` → `consolidated` → `archived`

- **Candidate**: extracted but below confidence threshold, or awaiting corroboration. Held for a configurable window (e.g., 7 days). Promoted to `active` if corroborated by another source. Discarded if not.
- **Active (provisional)**: promoted above confidence threshold but async hardening not yet complete (embedding generation, initial dedup check, taxonomy tagging). Retrievable via entity and taxonomy lookup but **excluded from vector similarity search** until the embedding is written. This prevents serving un-embedded memories in vector results while still making them discoverable through structured paths. Provisional status is transient — typically seconds to minutes. If hardening fails, the memory reverts to `candidate` with the failure logged.
- **Active (hardened)**: embedding written, dedup checked, taxonomy tagged. Fully retrievable through all retrieval paths including vector similarity. This is the steady-state for active memories.
- **Consolidated**: merged with related memories into a higher-level insight through distillation. Original memories preserved for provenance but no longer individually retrieved — the consolidated memory is retrieved instead.
- **Archived**: decayed below utility threshold, superseded by newer information, or explicitly invalidated. Preserved in storage for audit, never returned in retrieval.

### Quality Gates

Every candidate memory is scored on three dimensions:

- **Confidence** (0.0–1.0): How sure are we this is true? Single source = lower confidence. Multiple corroborations = higher. Human-stated = high. Agent-inferred = lower. Explicit capture = higher than implicit.
- **Utility** (0.0–1.0): How likely is this to be useful in future context? Broad applicability = higher. Narrow/ephemeral = lower.
- **Novelty**: Do we already know this? Dedup check against existing active memories. Near-duplicates are flagged for dedup rather than stored.

Threshold for auto-promotion from candidate to active is configurable per memory kind. Semantic memories (facts, decisions) have a higher confidence bar than episodic memories (outcomes, context).

### Temporal Decay

- **Episodic memories**: decay over time unless reinforced by relevance (being retrieved and used) or by corroboration (new evidence confirming them). Half-life is configurable.
- **Semantic memories**: durable — they do not decay with time. They are invalidated only by explicit contradiction (a newer memory stating the opposite) or by correction.
- **Procedural memories**: decay if not reinforced by successful outcomes. A pattern that hasn't been observed or confirmed in a long time loses confidence.

### Contradiction Detection

**Detection mechanism — primary path (at extraction time):**
After Stage 3 normalization, a new memory candidate has entities and taxonomy tags. The system queries existing active memories that share at least one entity OR taxonomy node with the candidate. If overlapping memories exist, an LLM comparison (Haiku-class) evaluates: "Does the new memory contradict any of these existing memories?" This catches contradictions at the point of arrival.

**Detection mechanism — secondary path (during consolidation):**
The sleep-time reflection job also scans for contradictions between existing active memories that may not share entities — e.g., two memories about the same topic expressed differently enough that extraction-time matching missed them. This is a periodic sweep, not real-time.

When new information conflicts with an existing active memory, the response depends on the memory kind:

**Facts and decisions** (`fact`, `decision`, `context`, `process_outcome`):
- If the conflict is clear and unambiguous (e.g., "we use Go" → "we switched to Rust"), the older memory is archived with reason `superseded`, the `superseded_by` FK is set to point to the newer memory, and the newer memory becomes active.
- If the conflict is ambiguous (could be a correction OR could be a different context), the newer memory is flagged and surfaced to the PM or human for resolution.
- Corrections (kind `correction`) explicitly invalidate their target memory.

**Opinions and patterns** (`preference`, `pattern`, `lesson`):
- Preferences and learned patterns have different lifecycle dynamics than facts. When contradicted, they lose confidence rather than being archived — preferences can be context-dependent and may be reaffirmed later.
- On contradiction: decrease confidence by a configurable step (e.g., -0.15 per contradiction). Only archive if confidence drops below a minimum threshold (e.g., 0.2).
- On reaffirmation: increase confidence (e.g., +0.10). This allows the system to track evolving certainty about preferences rather than treating every contradiction as an invalidation.

**Supersession chain**: Archived memories retain a `superseded_by` FK pointing to the memory that replaced them. This enables temporal queries — "what did we believe about X before the change?" — by following the supersession chain backward. Superseded memories remain archived (not returned in standard retrieval) but are traversable for audit and history.

**V1 failure mode**: the system confidently served stale answers (e.g., "20 agent profiles" when the actual count was 230+) because there was no fact versioning or conflict detection. V2 must detect when newer facts contradict older ones on the same topic and invalidate the stale fact.

## Consolidation

Periodic background work managed by Ellie. Consolidation is progressive and ongoing — not panic-driven. Consolidation runs are tracked in `memory_compaction_run` with run types: `dedup`, `synthesis`, `decay`, `distillation`, `reflection`, `task_completion`, `reembed`.

### Deduplication

Periodic LLM-powered dedup (see Deduplication section). Merges same-fact-different-words memories, increasing the surviving memory's confidence.

### Distillation

When multiple episodic memories about the same topic accumulate, Ellie distills them into one or more semantic memories. Example: five separate "Friday deploy failed" episodic memories → one procedural memory "Friday deploys have a higher failure rate — run full test suite before deploying on Fridays."

### Entity Re-Synthesis

When significant new information about an entity has been ingested since the last synthesis, re-run entity synthesis to update the definitional memory. The old synthesis is archived and the new one becomes active.

### Decay Processing

Periodic job evaluates active memories against their decay curves. Memories that have dropped below the utility threshold are moved to `archived` status.

### Memory scope on extraction

Memories are scoped to match the session they were extracted from:
- Org session → org-scoped memory
- Project session → project-scoped memory
- Task session → task-scoped memory

### Promote-on-task-completion

When a task completes, Ellie evaluates all task-scoped memories via a Haiku-class LLM pass. Each memory is assessed for lasting value beyond the task context:

- **Promote**: memories with reusable knowledge (architectural decisions, learned patterns, important context) are promoted to project scope (`task_id` set to null).
- **Keep**: memories that are purely task-specific working context (intermediate debugging steps, ephemeral status updates) remain task-scoped and naturally age out through the decay pipeline.

This ensures valuable knowledge from task work persists at the project level for future tasks, while ephemeral working context doesn't clutter the project memory space.

### Task-Completion Consolidation

Task completion is the natural consolidation trigger. Ellie subscribes to task completion events on the event bus. When a task reaches `done` status, an immediate consolidation pass runs on that task's memories:

1. **Scope promotion**: Evaluate task-scoped memories — promote valuable ones (high confidence, broad applicability) to project scope. Mechanically, promotion sets `task_id = null` on the memory, widening its scope from task to project. Provenance is preserved — the `memory_source` records still point to the original task session. No duplicate is created. (See Promote-on-task-completion above for the LLM-based assessment process.)
2. **Episodic distillation**: Distill task episodic memories into semantic memories while the full context of what was done and why is freshest.
3. **Execution summary**: Generate a procedural memory summarizing what was done, how, what tools were used, and what decisions were made. This gives future agents a playbook when similar tasks arrive — "here's how we did this last time."
4. **Targeted entity synthesis**: Run entity synthesis for entities heavily mentioned during the task.

Don't wait for the periodic batch. The end of a task is when the most useful consolidation happens.

### Sleep-Time Reflection

A `reflection` consolidation run type, scheduled during low-activity periods (Ellie's "sleep time"). Reflection is different from distillation — distillation converts specific episodic memories into semantic ones. Reflection is holistic pattern recognition across recent activity.

The reflection job:
1. Gathers recent episodic memories (last N days, configurable).
2. Groups by project and taxonomy.
3. LLM reflects on each group: what patterns emerge, what connections exist between events, what trends are developing, what potential issues are forming.
4. Generates new semantic or procedural memories from reflections.
5. Identifies cross-project connections (e.g., "Project A and Project B are both hitting the same API rate limit").

This is where procedural memories ("deploying on Fridays is risky") naturally emerge — from holistic reasoning rather than hard-coded distillation rules.

**Friction-triggered reflection**: In addition to scheduled runs, Ellie tracks "friction signals" — cases where retrieval failed to provide useful context, memories contradicted agent behavior, or humans corrected agent responses. When friction signals accumulate above a configurable threshold, a reflection pass triggers automatically to diagnose and address the pattern. This makes reflection responsive, not just periodic.

## File-Backed Memories

### V1 Learning

File-backed memories bridge the gap between conversation-extracted knowledge and current-state knowledge in files. Conversations are frozen at extraction time — the file evolves while the memory stays stale.

V1 results: +36pp on current-state queries, +33pp on preferences. But -29pp on project queries and -21pp on personal queries when used exclusively. **File-backed memories are an augmentation layer, not a replacement for conversation-extracted memories.**

### V2 Implementation

In V2, the "files" are:
- Documents in project git repos (README, architecture docs, config files)
- Artifacts in object storage (uploaded documents, generated reports)
- Agent workspace files (MEMORY.md, daily notes)

Ellie indexes these sources and extracts memories from them, tracking:
- `source_file_path`: where the file lives
- `source_file_hash`: content hash for change detection
- `source_file_mtime`: modification time at last scan

### Freshness Detection

Periodic scan compares stored hashes/mtimes against current file state. When a file has changed:
1. Mark existing file-backed memories from that file as potentially stale.
2. Re-extract from the updated file.
3. Dedup new extractions against existing memories.
4. Archive stale memories that are no longer supported by the updated file.

### Live File Reading

For current-state and preference queries, when a retrieved memory has a `source_file_path`, Ellie can optionally read the actual file at query time for the freshest answer. This is more expensive but eliminates the staleness window between file changes and re-extraction.

## Importer

### Purpose

The importer allows bulk loading of historical memory data from external sources. The primary use case is uploading a zip file containing JSONL conversation logs (e.g., migrated from another system, backfilling data gaps, or importing historical records).

### How It Works

1. Human uploads a zip file through the chat interface or API.
2. System creates a `memory_import` record tracking the job.
3. The zip is extracted and each JSONL file is identified.
4. Each JSONL file is processed through the same extraction pipeline as ongoing ingest:
   - Stage 0: Garbage pattern rejection — imported records pass through the same garbage pattern filter (section 2.1) as live-captured events. Malformed, trivial, or obviously low-value records are discarded before LLM extraction.
   - Stage 1: LLM extraction from conversation windows
   - Stage 2: Scoring and filtering (threshold 40)
   - Stage 3: Normalization (entity names, taxonomy classification)
   - Stage 4: Embedding, dedup, and storage
5. The import record is updated with: total files processed, memories extracted, duplicates detected, errors encountered.
6. Ellie reports results to the human when the import completes.

### Import Record

Each import job tracks:
- Source filename and size
- Status: `pending`, `processing`, `completed`, `failed`
- Files in archive (count)
- Files processed (count)
- Memories extracted (count)
- Duplicates skipped (count)
- Errors (count and details)
- Started at / completed at
- Requesting user

### JSONL Record Format

Each line in a JSONL file must be a JSON object with at minimum:
- `timestamp` (string, ISO 8601) — when the message was sent
- `author` (string) — name or identifier of the speaker
- `content` (string) — the message text

Optional fields:
- `role` (string: `human`, `agent`, `system`) — defaults to `human` if absent
- `session_id` (string) — groups messages into conversations. Messages sharing a session_id are processed together as a conversation window.
- `metadata` (object) — any additional context passed through to extraction

When `session_id` is absent, the importer groups messages by time proximity (configurable window, e.g., 30 minutes between messages starts a new group).

### Error Handling

- Malformed JSONL lines are skipped and logged, not fatal.
- Extraction failures on individual windows are logged and skipped.
- The import continues through errors — partial success is expected and normal for large archives.
- Human can review the import record to see what failed and why.

## Database Schema

```sql
-- Core memory record
create table memory (
  id            uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  project_id    uuid references project(id),        -- null = org-scoped
  task_id       uuid references project_task(id),    -- null = project or org-scoped
  agent_id      uuid references agent(id),           -- non-null = agent-private scope
  kind          text not null
    check (kind in ('fact', 'decision', 'preference', 'lesson', 'pattern', 'anti_pattern', 'correction', 'context', 'entity_definition', 'process_outcome')),
  layer         text not null
    check (layer in ('episodic', 'semantic', 'procedural')),
  status        text not null default 'candidate'
    check (status in ('candidate', 'active', 'consolidated', 'archived')),
  is_hardened   boolean not null default false,       -- false = provisional (no embedding yet), true = fully retrievable via vector search
  content       text not null,                        -- the actual memory text
  confidence    real not null default 0.5,            -- 0.0–1.0
  utility       real not null default 0.5,            -- 0.0–1.0
  occurred_at   timestamptz,                          -- when the remembered event/fact occurred
  valid_from    timestamptz,                          -- temporal bound: when this became true
  valid_until   timestamptz,                          -- temporal bound: when this stopped being true (null = still true)
  archived_reason text check (archived_reason in ('superseded', 'decayed', 'deduped', 'manual')),
  superseded_by uuid references memory(id),           -- FK to the memory that replaced this one (supersession chain)
  sensitivity   text not null default 'internal'
    check (sensitivity in ('public', 'internal', 'restricted')),
  content_hash  text,                                 -- SHA256 of canonical content for fast exact-dedup and idempotent import
  source_file_path  text,                             -- for file-backed memories
  source_file_hash  text,                             -- content hash for freshness detection
  source_file_mtime timestamptz,                      -- file mtime at last scan
  embedding     vector(1536),                         -- OpenAI text-embedding-3-small at 1536d
  metadata      jsonb not null default '{}',
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now()
);

-- Design notes:
-- Scope is determined by the combination of organization_id, project_id, task_id, agent_id:
--   organization_id only                           = org scope
--   organization_id + project_id                   = project scope
--   organization_id + project_id + task_id         = task scope
--   agent_id set                                   = agent-private scope
-- is_hardened tracks whether async hardening (embedding, dedup, taxonomy) is complete.
--   Memories with status='active' AND is_hardened=false are provisional: retrievable via
--   entity/taxonomy lookup but excluded from vector similarity search (embedding may be null).
--   Hardening typically completes in seconds. Failure reverts to candidate status.
-- Embedding is 1536d — never truncate. V1 proved 1536d >> 768d (+20pp hit rate).
-- confidence and utility are NOT used as retrieval weights (V1 proved importance weighting hurts -4pp).
--   They are used for lifecycle decisions (promotion, decay, consolidation thresholds).
-- superseded_by preserves the supersession chain for temporal queries ("what did we believe before?").
-- sensitivity gates retrieval: 'restricted' memories excluded from passive injection.
-- content_hash enables fast exact-duplicate detection without cosine similarity, and idempotent imports.
-- Canonicalization: SHA256 of the content field after trimming leading/trailing whitespace.
-- No lowercasing or entity normalization — those change meaning. Hash catches exact duplicates;
-- cosine similarity handles semantic duplicates.

create index idx_memory_org_scope on memory(organization_id, status) where status = 'active';
create index idx_memory_project_scope on memory(project_id, status) where project_id is not null and status = 'active';
create index idx_memory_task_scope on memory(task_id, status) where task_id is not null and status = 'active';
create index idx_memory_agent_private on memory(agent_id, status) where agent_id is not null and status = 'active';
create index idx_memory_kind on memory(kind, status) where status = 'active';
create index idx_memory_layer on memory(layer, status) where status = 'active';
create index idx_memory_created on memory(created_at desc);
create index idx_memory_file_path on memory(source_file_path) where source_file_path is not null;
create index idx_memory_sensitivity on memory(sensitivity, status) where sensitivity = 'restricted' and status = 'active';
create index idx_memory_content_hash on memory(content_hash) where content_hash is not null;
create index idx_memory_superseded on memory(superseded_by) where superseded_by is not null;

-- Global taxonomy tree (org-level, not per-project)
create table memory_taxonomy_node (
  id            uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  parent_id     uuid references memory_taxonomy_node(id),
  name          text not null,                        -- e.g., "deployment", "ci-pipelines"
  path          text not null,                        -- materialized path: "engineering > deployment > ci-pipelines"
  depth         int not null default 0,
  description   text,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),

  unique (organization_id, path)
);

-- Design notes:
-- Materialized path for fast subtree queries (WHERE path LIKE 'engineering > deployment%').
-- Ellie manages this tree — creates, merges, prunes nodes autonomously.
-- Humans/PMs can also create/rename nodes conversationally.

create index idx_taxonomy_node_org on memory_taxonomy_node(organization_id);
create index idx_taxonomy_node_parent on memory_taxonomy_node(parent_id);
create index idx_taxonomy_node_path on memory_taxonomy_node(organization_id, path text_pattern_ops);

-- Many-to-many: memories tagged with taxonomy nodes
create table memory_taxonomy_tag (
  id            uuid primary key default gen_random_uuid(),
  memory_id     uuid not null references memory(id) on delete cascade,
  taxonomy_node_id uuid not null references memory_taxonomy_node(id),
  created_at    timestamptz not null default now(),

  unique (memory_id, taxonomy_node_id)
);

create index idx_taxonomy_tag_node on memory_taxonomy_tag(taxonomy_node_id);
create index idx_taxonomy_tag_memory on memory_taxonomy_tag(memory_id);

-- Known entities (people, projects, tools, concepts)
create table memory_entity (
  id            uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  name          text not null,                        -- canonical name
  entity_type   text not null check (entity_type in ('person', 'project', 'tool', 'concept', 'organization')),
  aliases       text[] not null default '{}',         -- alternative names/abbreviations
  synthesis_memory_id uuid references memory(id),     -- link to current entity_definition memory
  last_synthesized_at timestamptz,
  created_at    timestamptz not null default now(),
  updated_at    timestamptz not null default now(),

  unique (organization_id, entity_type, name)
);

-- Design notes:
-- aliases are discovered through two paths:
--   1. Stage 3 normalization: LLM identifies when a term refers to an existing entity
--      under a different name (e.g., "OC" → "OtterCamp"). Alias added automatically.
--   2. Entity synthesis: when synthesizing, LLM sees all mentions across conversations
--      and identifies alternate names used in different contexts.
--   Humans can also declare aliases via @mention Ellie.
-- synthesis_memory_id links to the current entity_definition memory for this entity.

create index idx_entity_org on memory_entity(organization_id);

-- Many-to-many: which memories mention which entities
create table memory_entity_mention (
  id            uuid primary key default gen_random_uuid(),
  memory_id     uuid not null references memory(id) on delete cascade,
  entity_id     uuid not null references memory_entity(id),
  created_at    timestamptz not null default now(),

  unique (memory_id, entity_id)
);

create index idx_entity_mention_entity on memory_entity_mention(entity_id);
create index idx_entity_mention_memory on memory_entity_mention(memory_id);

-- Provenance: links memories to their source events/messages
create table memory_source (
  id            uuid primary key default gen_random_uuid(),
  memory_id     uuid not null references memory(id) on delete cascade,
  source_type   text not null check (source_type in ('chat_message', 'event', 'file', 'import', 'explicit')),
  source_id     uuid,                                 -- FK to source record (polymorphic)
  session_id    uuid,                                 -- chat session where this was captured
  import_id     uuid references memory_import(id),    -- if from an import job
  trust_tier    real not null default 0.7,             -- source trustworthiness (see Memory Security)
  created_at    timestamptz not null default now()
);

-- Design notes:
-- trust_tier is set at creation time based on the source type + actor combination.
-- A memory's effective confidence cap = max(trust_tier) across all its sources.
-- This means corroboration from a higher-trust source elevates the confidence cap.
-- Values: human direct (1.0), human reaction (0.9), agent from conversation (0.8),
--   agent from task (0.7), file (0.7), import (0.6), external (0.4).
--
-- session_id: soft reference to chat_session.id — NO SQL FK. Memories are designed
-- to outlive sessions. Application code must use LEFT JOIN when joining to chat_session.
-- The absence of a FK is intentional; do not add one.
--
-- source_id semantics by source_type:
--   'chat_message' → chat_message.id (application-layer enforcement, no DB FK)
--   'event'        → domain_event.id (application-layer enforcement, no DB FK)
--   'file'         → NULL (files live in git/object storage; no DB record)
--   'import'       → memory_import.id (also reflected in the import_id FK column)
--   'explicit'     → NULL (human-asserted fact; no originating source record)

create index idx_memory_source_memory on memory_source(memory_id);
create index idx_memory_source_session on memory_source(session_id) where session_id is not null;
create index idx_memory_source_import on memory_source(import_id) where import_id is not null;

-- Tracks reviewed dedup pairs (never re-review the same pair)
create table memory_dedup_reviewed (
  id            uuid primary key default gen_random_uuid(),
  memory_id_a   uuid not null references memory(id),
  memory_id_b   uuid not null references memory(id),
  decision      text not null check (decision in ('keep_both', 'deprecated_a', 'deprecated_b', 'merged')),
  reviewed_at   timestamptz not null default now(),

  unique (memory_id_a, memory_id_b),
  check (memory_id_a < memory_id_b)                   -- canonical ordering prevents duplicate pairs
);

-- Design notes:
-- V1 reviewed 26,395 pairs with zero rework thanks to this table.
-- The check constraint ensures (A, B) is always stored with the smaller UUID first,
-- preventing both (A,B) and (B,A) from existing.

create index idx_dedup_reviewed_a on memory_dedup_reviewed(memory_id_a);
create index idx_dedup_reviewed_b on memory_dedup_reviewed(memory_id_b);

-- Tracks consolidation/compaction job runs
create table memory_compaction_run (
  id            uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  run_type      text not null check (run_type in ('dedup', 'synthesis', 'decay', 'distillation', 'reflection', 'task_completion', 'reembed')),
  status        text not null default 'running' check (status in ('running', 'completed', 'failed')),
  memories_processed int not null default 0,
  memories_archived  int not null default 0,
  memories_created   int not null default 0,
  error_count   int not null default 0,
  error_details jsonb,
  started_at    timestamptz not null default now(),
  completed_at  timestamptz
);

create index idx_compaction_run_org on memory_compaction_run(organization_id, started_at desc);

-- Tracks bulk import jobs
create table memory_import (
  id            uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  requested_by  uuid not null references human_user(id),
  source_filename text not null,
  source_size_bytes bigint,
  status        text not null default 'pending' check (status in ('pending', 'processing', 'completed', 'failed')),
  files_in_archive  int,
  files_processed   int not null default 0,
  memories_extracted int not null default 0,
  duplicates_skipped int not null default 0,
  error_count   int not null default 0,
  error_details jsonb,
  started_at    timestamptz,
  completed_at  timestamptz,
  created_at    timestamptz not null default now()
);

create index idx_import_org on memory_import(organization_id, created_at desc);
create index idx_import_status on memory_import(status) where status in ('pending', 'processing');
```

## V1 Lessons Learned

This section documents the complete experimental findings from V1's 48-hour extraction sprint (Feb 14–16 2026) on 13,000+ memories. These findings are design constraints for V2 — not suggestions.

### The Retrieval Stack, Ranked by Proven Impact

| Rank | Improvement | Before | After | Impact | Status |
|---|---|---|---|---|---|
| 1 | Entity synthesis | 80% hit | 95% hit | +15pp | Must be core pipeline step |
| 2 | 1536d OpenAI embeddings | 60% hit | 80% hit | +20pp | Use from day one, never truncate |
| 3 | File-backed memories | 34% current-state | 70% current-state | +36pp (category) | Augmentation layer |
| 4 | LLM dedup | 17,651 memories | 12,492 memories | 29% reduction | Periodic maintenance |
| 5 | Taxonomy pre-filtering | — | Completeness gains | Measured | Pre-filter, not replacement |

### Experimentally Proven Failures (Do NOT Repeat)

| Technique | Impact | Evidence | Lesson |
|---|---|---|---|
| Hybrid BM25+vector search | -5pp (80%→75%) | BM25 noise dilutes good vector matches | Stay with pure vector. BM25 only helps exact keyword matches (proper nouns). |
| Importance-weighted retrieval | -4pp overall | Importance scores from extraction are unreliable | Never use importance/utility scores as retrieval ranking weights. Use them for lifecycle decisions only. |
| Kind-aware retrieval filtering | -2pp vs baseline | Marginal complexity, no gain | Kind is useful for taxonomy routing, not for retrieval filtering. |
| File-only retrieval | -3pp overall | Loses rich conversation context | File memories supplement conversation memories, never replace them. |
| Truncated embeddings (1536→768) | -10pp | OpenAI 768d truncated worse than nomic 768d native | Never truncate embedding dimensions. Use the full dimensionality. |
| Nomic local embeddings | -20pp vs OpenAI 1536d | 60% hit rate vs 80% | Quality of embedding model matters enormously. Use the best available. |

### Extraction Quality Findings

- Cross-validation: 42% strong match, 55% weak match, 3% missed.
- Extraction misses specific file paths and artifact locations — prompts must request these explicitly.
- Score distribution is bimodal (peaks at 55–59 and 65–69). Threshold 40 captures the lower peak without noise.
- Window-based extraction (groups of adjacent messages) outperforms single-message extraction.
- Multi-human conversation awareness in prompts is essential.

### Known Failure Mode: Confident Wrong Answers

V1 system answered "20 agent profiles" when the actual count was 230+. The stale memory from an early conversation was served with high confidence because there was no fact versioning or conflict detection. V2 must implement contradiction detection (see Memory Lifecycle section).

### Memory Distribution Insights

At 13,359 active memories: fact (36.8%), context (25.4%), technical_decision (16.3%), process_decision (6.7%), preference (5.0%), lesson (3.3%), others (6.5%). Fact and context memories dominate the corpus. Topic-specific memories (4,900+ OtterCamp-related) can drown out other queries in unfiltered vector search — this is the core motivation for taxonomy pre-filtering.

## Insights from External Systems Research

The following systems were researched. Key ideas incorporated into or explicitly rejected from the V2 design:

### Spark Intelligence (VibeForgge)

- **Incorporated**: Quality gates with multi-dimensional scoring (confidence, utility, novelty). The concept of "advisory delivery" with cooldown and dedup — Ellie's proactive injection should avoid re-surfacing the same memory repeatedly.
- **Incorporated**: Prediction → evaluation feedback loops for procedural memory. When Ellie surfaces a pattern and the outcome contradicts it, the procedural memory's confidence decreases.
- **Deferred**: 12-stage pipeline is over-engineered for our needs. Our 4-stage extraction pipeline is sufficient.
- **Deferred**: Domain-specific "chips" — our taxonomy system serves a similar purpose without the added abstraction layer.

### Beads (Steve Yegge)

- **Incorporated**: The principle that agents should QUERY memory on demand rather than try to memorize it. Our retrieval pipeline serves this — agents get relevant memories injected, they don't manage their own memory.
- **Incorporated**: Semantic compaction of completed work — our consolidation pipeline (distillation of episodic → semantic) serves this purpose.
- **Not applicable**: Beads is fundamentally a task tracker being used as memory. We have a separate task system (doc 03). Memory and tasks are different concerns in OtterCamp.

### Ars Contexta

- **Incorporated**: Three-space separation concept (self/notes/ops maps to our agent-private/semantic/episodic layers).
- **Incorporated**: The 6-R processing pipeline concept (Record → Reduce → Reflect → Reweave → Verify → Rethink) influenced our consolidation design — particularly the "Reweave" step (backward pass updating older notes) maps to our entity re-synthesis.
- **Incorporated**: Fresh context per processing phase via subagent spawning — our extraction pipeline runs in its own context, not in the agent's active session.
- **Deferred**: Research-grounded kernel primitives (249 claims). Interesting for validation but too academic for a product spec.

### Letta Context Repositories

- **Incorporated**: The concept of memory defragmentation — periodic reorganization of memory structure. Our taxonomy pruning and entity re-synthesis serve this purpose.
- **Incorporated**: Concurrent subagent processing for memory operations via isolated contexts.
- **Rejected**: Git-backed markdown files as memory store. Doesn't work for multi-agent concurrent access at our scale. PostgreSQL with proper scoping is the right choice.
- **Rejected**: Progressive disclosure via filesystem hierarchy. Our taxonomy serves this purpose in a structured, queryable way.

### OpenClaw

- **Incorporated (as anti-pattern)**: OpenClaw's #1 failure mode — relying on the agent to decide when to save memory — directly motivated our implicit capture design. Agents don't save memories; Ellie does.
- **Incorporated (as anti-pattern)**: Context compaction destroying unsaved information. Our progressive summarization (doc 02) handles working memory; Ellie handles durable memory. These are separate systems.
- **Incorporated**: Automatic memory flush before compaction — ensuring important context is extracted before it's summarized away. Our event-bus capture handles this naturally.
- **Not applicable**: OpenClaw's local-first architecture (SQLite, local embeddings). We're server-based with PostgreSQL.

### Supermemory

- **Incorporated**: Hook-based implicit saves vs explicit tool calls. Our event-bus capture is the equivalent of Supermemory's hooks — background, automatic, no agent involvement.
- **Incorporated**: Temporal reasoning with automatic decay. Our memory lifecycle includes configurable decay curves.
- **Deferred**: Vector-graph hybrid layer. We start with PostgreSQL + pgvector. Graph structure via entity/mention tables gives us lightweight graph-like traversal without a separate graph database.

### Zep/Graphiti

- **Incorporated**: Temporal bounds on facts (valid_from, valid_until). Our memory schema includes these columns for tracking when facts are/were true.
- **Incorporated**: Edge invalidation concept — when new information contradicts old, the old edge is invalidated. Our contradiction detection implements this.
- **Deferred**: Full temporal knowledge graph with 4-timestamp edges. Our entity/mention tables plus temporal bounds on memories give us the essential benefit without the full graph DB complexity. If retrieval quality demands it later, we can evolve toward a richer graph structure.

## Memory Feedback

Chat message reactions (see 02-chat.md) feed back into the memory system. When a human reacts to an agent response:

- **Positive reaction** on a response that drew from memory: increases the confidence of the underlying memories that contributed to that response.
- **Negative reaction** on a response that drew from memory: decreases confidence. If a memory's confidence drops below the active threshold through repeated negative feedback, it transitions to `archived`.
- **Correction by human**: when a human corrects an agent (and the agent's response was informed by memory), Ellie extracts a `correction` memory that explicitly invalidates the stale memory.

This creates a continuous learning loop — memory quality improves through human feedback without requiring explicit memory management actions.

## Agent Memory Bootstrapping

When a new agent joins a project or org, it has no private memory and no history. Bootstrapping happens through the normal retrieval pipeline:

- On the agent's first turn, passive injection pulls relevant org and project memories into its context.
- The agent immediately has access to organizational conventions, project decisions, and relevant knowledge.
- No special initialization step is required — the retrieval pipeline handles it.

For agents that take over work from a departing agent, Ellie can be @mentioned to surface the previous agent's relevant task-scoped memories and re-scope them to the new agent.

## Extraction Isolation

All memory pipeline operations (extraction, synthesis, dedup, consolidation) run in isolated contexts — they do NOT run inside an agent's active session. This ensures:

- Extraction work doesn't consume the active agent's context budget.
- Failed extraction doesn't affect the agent's ongoing work.
- Each pipeline stage operates in its own "clean" context, maintaining quality (inspired by Ars Contexta's "fresh context per phase" principle).

Ellie manages these as background async operations, separate from her conversational agent role.

## Memory Security

### Source Trust Tiers

Memory confidence is capped by source trustworthiness. A memory can never have higher confidence than its source allows, regardless of other scoring factors.

| Source | Trust Level | Confidence Cap |
|---|---|---|
| Human direct statement | Highest | 1.0 |
| Human reaction/correction | High | 0.9 |
| Agent extraction from internal conversation | Medium-high | 0.8 |
| Agent extraction from task execution | Medium | 0.7 |
| File-backed (internal project files) | Medium | 0.7 |
| Imported historical data (JSONL importer) | Medium-low | 0.6 |
| External content (uploaded docs, emails, web) | Low | 0.4 |

Source trust is stored as `trust_tier` on each `memory_source` record (set at creation time based on source type + actor) and applied during extraction Stage 2 scoring. A memory's effective confidence cap = `max(trust_tier)` across all its sources — corroboration from a more trusted source elevates the cap.

### Sensitivity Classification

Memories are classified by sensitivity: `public`, `internal` (default), `restricted`.

- **public**: Safe for broad retrieval, no restrictions.
- **internal**: Standard memories from conversations and work. Default for all extractions. Retrieved normally within scope rules.
- **restricted**: Memories about credentials, API keys, personal information, security configurations, or access controls. Auto-classified during extraction when content matches sensitive patterns. Excluded from passive injection — only returned for active queries that specifically target the topic.

### Provenance Enforcement

Every active memory must have at least one `memory_source` record linking it to its origin (chat message, event, file, import, or explicit capture). Memories without provenance are flagged during periodic maintenance. Orphaned memories (source deleted, no remaining provenance) are candidates for archival.

For imported data, the import job ID serves as provenance — but imported memories carry the `medium-low` trust tier and cannot exceed 0.6 initial confidence without subsequent corroboration from a higher-trust source.

## Future Enhancements

Items identified through research but deferred until V2 operational experience provides evidence they're needed:

- **Memory-to-memory links**: Direct typed relationships (`causal`, `supports`, `contradicts`, `elaborates`) between memories. The entity mention system provides indirect graph connectivity now. Add direct links if entity-based traversal proves inadequate for capturing cross-entity connections.
- **Graph traversal as retrieval channel**: Run entity-based graph traversal in parallel with vector similarity during Stage 4. The `memory_entity_mention` table already supports this query pattern. Must be experimentally validated before adding to the pipeline — V1 taught us to test retrieval changes empirically, not assume they'll help.
- **RL-based retrieval utility (MemRL)**: Learn a utility score from actual usage outcomes, separate from similarity. Requires outcome tracking infrastructure. Add when sufficient retrieval-outcome data exists.
- **Skill synthesis from procedural memory**: When procedural memories cluster around a workflow, consolidate into skill candidates. This bridges memory and skills (doc 10). Defer to skills spec.
- **EIDOS prediction tracking**: Structured prediction/evaluation on every agent action, with surprise-level driving memory extraction. Cross-cutting concern affecting agent runtime (doc 05). Massive scope.
- **Lossless source archive**: Searchable raw conversation segments as a fallback when extraction misses details. Progressive summarization (doc 02) and memory source provenance may prove sufficient.
- **Belief drift detection**: Track the rate of contradiction/supersession per entity. Rapid belief changes (3+ supersessions in 24 hours) trigger alerts instead of automatic supersession. Add when untrusted external content is flowing in.
- **Dynamic cross-scope grants**: Selective cross-project memory sharing for specific memories. Currently, cross-project insights should be promoted to org scope. Add grant mechanism if promotion proves too blunt.

## Open Questions

None currently outstanding — all questions from the original skeletal spec have been resolved through discussion and V1 experimental findings.

## Resolved Decisions

1. **Ellie's dual role**: Ellie is both background memory infrastructure AND a conversational agent. This gives the memory system agency.
2. **Implicit capture is primary**: Ellie watches the event bus. Agents never decide to save memories. Explicit capture via @mention is the secondary path.
3. **PostgreSQL storage**: Not markdown files, not a separate vector DB. PostgreSQL with pgvector for vector similarity search.
4. **1536d embeddings from day one**: V1 proved +20pp hit rate improvement. Never truncate embedding dimensions.
5. **Entity synthesis is a core pipeline step**: Not optional, not manual. Periodic consolidation is the #1 retrieval improvement (+15pp).
6. **Global taxonomy, org-level**: Taxonomy is not walled within projects. One tree managed by Ellie. Used as pre-filter in retrieval.
7. **Taxonomy as pre-filter**: Taxonomy narrows the search corpus before vector search, not as a supplement after. This improves retrieval on obscure and older items.
8. **No hybrid BM25+vector**: Pure vector search outperforms hybrid (-5pp regression). Don't add BM25 complexity.
9. **No importance weighting in retrieval**: Importance/utility scores are for lifecycle decisions (promotion, decay), NOT for retrieval ranking (-4pp regression).
10. **No kind-aware retrieval filtering**: Kind is useful for taxonomy routing, not for filtering at retrieval time (-2pp regression).
11. **File-backed memories supplement, never replace**: +36pp on current-state but -29pp on project queries when used alone.
12. **Dedup is periodic maintenance**: LLM-powered dedup with cursor-based progress tracking. Improves diversity, not hit rate.
13. **Working memory is NOT Ellie's domain**: Live context window is managed by progressive summarization (doc 02). Ellie handles durable memory only.
14. **Four-stage retrieval pipeline**: Scope filter → taxonomy classification → subtree retrieval → relevance ranking.
15. **Taxonomy classification IS the query router**: No separate routing mechanism needed. The taxonomy node a query maps to naturally routes to the right retrieval strategy.
16. **Memory lifecycle**: candidate → active → consolidated → archived. Quality gates for promotion, temporal decay for episodic and procedural.
17. **Contradiction detection required**: V1 served confident wrong answers from stale memories. Newer facts must invalidate older contradicting facts.
18. **Unified kind taxonomy**: V1 had two separate kind sets (agent-memory vs Ellie-memory). V2 has one unified set.
19. **Importer via zip upload**: JSONL files in a zip, processed through the standard extraction pipeline. Import job tracking with progress and error reporting.
20. **Score threshold 40**: V1 bimodal distribution validated this threshold. Captures the lower peak without noise.
21. **Scope inheritance on retrieval**: Task queries also surface project and org memories. Hard scope isolation prevents cross-project contamination.
22. **Cursor-based progress for long-running jobs**: Dedup, synthesis, and import jobs must be restartable. Track progress to avoid rework.
23. **Procedural memory is advisory**: Distinct from skills (human-authored, prescriptive). Procedural memories emerge from experience and can be wrong.
24. **Multi-label taxonomy tagging**: A memory can belong to multiple taxonomy nodes.
25. **Live file reading at query time**: For current-state queries, optionally read the source file for the freshest answer.
26. **Injection cooldown**: Memories injected in the previous turn are deprioritized to avoid repetitive context. Inspired by Spark Intelligence's advisory cooldown concept.
27. **Chat reactions feed memory**: Positive/negative reactions adjust confidence on contributing memories. Corrections create explicit invalidation memories.
28. **Agent bootstrapping via retrieval**: No special initialization step. New agents get relevant memories through the normal passive injection pipeline on their first turn.
29. **Extraction runs in isolated contexts**: Pipeline operations don't consume active agent context. Each stage gets a clean context for quality.
30. **Garbage pattern rejection before LLM extraction**: Deterministic pre-filter rejects known-junk patterns (tool sequences, metrics, raw code, tautologies) before expensive LLM extraction. Maintained as an explicit list, not a learned classifier.
31. **Attention-aware injection ordering**: Most relevant memories injected last (closest to the agent's prompt) to exploit transformer recency bias. Claim content before metadata within each memory.
32. **memory.query tool for agent-initiated retrieval**: All agents get a `memory.query` tool wrapping the four-stage pipeline. Fills the gap between automatic passive injection and human-initiated @mention queries.
33. **Supersession chain via FK**: Archived memories retain `superseded_by` pointing to their replacement. Enables temporal queries and audit without returning superseded memories in standard retrieval.
34. **Task-completion-triggered consolidation**: Ellie subscribes to task completion events. Immediate consolidation pass: scope promotion, distillation, execution summary, targeted entity synthesis.
35. **Opinion confidence dynamics**: Preferences and patterns lose confidence on contradiction instead of being archived. Only archived if confidence drops below minimum threshold through repeated contradiction. Reaffirmation increases confidence.
36. **Sleep-time reflection**: Holistic pattern recognition across recent activity during low-activity periods. Generates procedural and semantic memories from cross-cutting analysis. Also triggered by accumulated friction signals (retrieval failures, human corrections).
37. **Source trust tiers cap confidence**: Memory confidence capped by source trustworthiness. Human direct statements cap at 1.0, imported data at 0.6, external content at 0.4.
38. **Sensitivity classification**: `public`, `internal` (default), `restricted`. Restricted memories (credentials, keys, personal info) excluded from passive injection.
39. **Content hash on all memories**: SHA256 of canonical content for fast exact-duplicate detection and idempotent imports. Supplements cosine similarity dedup.
40. **Cross-encoder reranking on active queries only**: Cheap model reranks top-k vector results for precision. Too expensive for passive injection, valuable for explicit queries where latency is acceptable.
41. **Provenance enforcement**: Every active memory must have at least one `memory_source` record. Orphaned memories flagged for review.
42. **Contradiction detection is two-path**: Primary at extraction time (entity/taxonomy overlap + LLM comparison). Secondary during reflection (periodic sweep for contradictions without shared entities).
43. **Scope promotion = set task_id to null**: Provenance preserved via memory_source records. No duplicate created.
44. **Trust tier stored on memory_source**: `trust_tier` real column set at creation from source type + actor. Memory confidence cap = max(trust_tier) across sources.
45. **Taxonomy self-bootstraps**: Skip pre-filtering when tree is empty. Nodes created as memories arrive. Hierarchy structured after enough accumulate.
46. **Entity aliases discovered automatically**: Stage 3 normalization and entity synthesis both identify alternate names. Humans can declare aliases via @mention.
47. **Taxonomy classification fallback**: Low-confidence classification or too-few results → skip taxonomy, search full scope-filtered corpus.
48. **Embedding model migration via re-embed batch job**: Content hash identifies memories needing re-embedding. Model version tracked in metadata. Mixed-model memories excluded from vector search during migration.
49. **memory.query excludes already-injected memories**: Prevents duplicate context consumption within a turn.
50. **Content hash canonicalization**: SHA256 of content after whitespace trimming. No lowercasing or normalization — exact duplicates only.
51. **JSONL import format**: Minimum fields: timestamp, author, content. Optional: role, session_id, metadata. Messages grouped by session_id or time proximity.
52. **Ellie's conversational capabilities specified**: Query, explain, entity knowledge, belief history, capture, forget, correct, on-demand ops, system health.
53. **Provisional status for newly promoted memories**: `is_hardened` boolean on `memory` table. Memories promoted to `active` with `is_hardened=false` are retrievable via entity/taxonomy lookup but excluded from vector similarity search until embedding, dedup check, and taxonomy tagging complete. Prevents serving un-embedded memories in vector results. Failure reverts to candidate. Inspired by context engineering async hardening patterns.
54. **Instruction poisoning defense in extraction pipeline**: Stage 0 rejects behavioral override patterns. Stage 1 extraction prompt classifies candidates as behavioral vs factual. Behavioral candidates (attempts to set agent instructions, override policies, or establish blanket rules) are rejected with a log entry. Prevents user content from planting directives disguised as preferences or facts in the memory system.
55. **Procedural memory includes tool choreography**: learned sequences of tool calls and strategies for recurring task types. Task-completion execution summaries capture tools used, invocation order, and which sequences succeeded or failed. Gives future agents playbooks for similar tasks without requiring explicit skill authoring.
