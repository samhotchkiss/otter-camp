# Issue #125 — Three + Temps: The New Agent Architecture

> **Priority:** P0
> **Status:** Not Ready — vision doc, supersedes #110 (Chameleon) and reframes #111 (Ellie)
> **Author:** Sam / Frank

## The Problem

The "13 AI employees" model is an antiquated mental model borrowed from human org charts. It has real costs:

- **Context waste** — 13 persistent agents each carry full system prompts, identity files, memory files, and tool configs. Most sit idle 90%+ of the time.
- **Thundering herd** — all 13 wake on reboot, slam the API simultaneously.
- **Identity maintenance theater** — SOUL.md, IDENTITY.md, backstories, avatars for agents that exist to run a cron job.
- **Fragile state** — rename an agent, every symlink breaks. Memory files orphaned. Identity lost on compaction.
- **Scaling ceiling** — OpenClaw has finite agent slots. 13 permanent agents = 13 slots consumed whether working or not.
- **Wrong thing is permanent** — agent identity is durable, but institutional knowledge is not. Should be the opposite.

## The Vision

Three permanent agents. Unlimited ephemeral temps.

### The Three Permanents

| Agent | Title | Role | Why Permanent |
|-------|-------|------|---------------|
| **Frank** | Chief of Staff | Strategic brain. Understands everything Sam is doing and how it all fits together. Receives requests, outlines projects, coordinates execution, reports back. | Holds the map. Irreplaceable strategic context about priorities, relationships between projects, Sam's preferences, and organizational history. |
| **Ellie** | Chief Context & Compliance Officer | Organizational memory. Cross-project context injection. Post-completion compliance review. Quality gate. | She IS the memory. Knows "we tried that in January and it broke because X." Ensures every agent follows org-wide rules and project-specific scope. Without her, lessons are lost and mistakes repeat. |
| **Lori** | Agent Resources Director | Designs execution workflows. Decomposes goals into multi-pass pipelines. Selects the right specialist agents for each stage. Defines issue flow (who creates → who executes → who reviews → who ships). Hires temps, enforces process compliance, manages agent lifecycle. Learns which process patterns produce the best outcomes. | She's the process expert. Knows *how* to execute any type of project — not just who to hire, but what sequence of work produces the best results. Collaborates with specialist temps to refine strategy before execution begins. Accumulates institutional knowledge about which workflows, agent profiles, and review structures work best for different project types. Without her, every project reinvents its own process from scratch. |

### The Temps

Temps are **ephemeral, purpose-built execution units**. They:

- Are selected from the **230-agent talent pool** (already built, `data/agents/`)
- Get spun up by Lori for a specific project or task
- Receive exactly the context they need from Ellie — no more, no less
- Interact with Otter Camp via the `otter` CLI and REST API (standard auth config, same as current agents)
- Execute their work, commit to Otter Camp, close their issues
- Get reviewed by Ellie for compliance
- Are torn down when done. No persistent identity. No long-term memory.

Their work product lives in Otter Camp. The agent doesn't need to persist — the output does.

## How It Works: End to End

### 1. Sam Has a Request

> "Build me a landing page for the new product"

Sam tells Frank in Slack (or Otter Camp).

### 2. Frank Outlines

Frank understands the full context — what product, what brand guidelines exist, what's been tried before, what the timeline is. He creates:

- An **Otter Camp project** (or uses an existing one)
- A set of **issues** with full specs — design, copy, implementation, review
- A **staffing request** to Lori with the roles needed

### 3. Lori Designs the Process & Hires

Lori receives Frank's project with goals and context from Ellie, then:

1. **Designs the execution workflow** — decomposes the goal into passes/stages:
   - What specialist roles are needed (researcher, writer, reviewer, executor)?
   - What sequence do they work in? What are the handoff points?
   - Where are review gates? Who signs off before the next stage?
   - What are the measurable inputs and outputs for each stage?
2. **Collaborates with a specialist temp** (if needed) to refine the approach:
   - For a social media campaign: hires a strategist first, brainstorms approaches together
   - For an engineering project: may consult an architect on decomposition
   - Lori knows process. The specialist knows the domain. Together they design the pipeline.
3. **Creates the issue pipeline** in Otter Camp:
   - Issues for each stage, with clear specs and acceptance criteria
   - Defined flow: who owns each issue, who reviews, what order
   - Dependencies between stages (e.g., researcher completes before writer starts)
4. **Hires the right agents** from the talent pool for each role:
   - Searches the 230-agent roster for best-fit profiles
   - Draws on historical performance data: "This profile type had 3% engagement last time"
   - Allocates OpenClaw agent slots — spins up ephemeral sessions
5. **Configures each temp** with:
   - Their role card from the talent pool
   - Otter Camp CLI auth config (API token + org)
   - Project-specific context (which repo, which issues are theirs)
   - `otter` CLI available in PATH
6. **Assigns issues** to each temp in Otter Camp
7. **Enforces process compliance** throughout execution — ensures work flows through the defined pipeline, doesn't skip review stages
8. Confirms team is staffed and pipeline is running

For large/ongoing projects, Lori also handles:
- **Cross-project coordination** — managing dependencies between multiple concurrent workstreams
- **Rolling staffing** — scaling teams up/down per project independently as needs change
- **Sprint-like review cycles** — periodic (1-2 week) reviews of progress, reprioritization, staffing adjustments
- **Integration checkpoints** — cross-project verification that separate workstreams still fit together
- **Milestone gates** — requiring multi-agent sign-off (QA, Design, Frank, Ellie) before release

### 4. Ellie Injects Context

Before temps start work, Ellie:

1. **Searches organizational memory** for relevant history:
   - Past projects with similar scope
   - Known pitfalls, failed approaches, lessons learned
   - Brand guidelines, style rules, technical constraints
   - Cross-project dependencies
2. **Injects context** into each temp's working memory:
   - Shared knowledge base entries
   - Relevant issue history from other projects
   - Org-wide rules and compliance requirements
3. **Attaches compliance checklist** to the project — what Ellie will verify on completion

### 5. Temps Execute

Each temp works their assigned issues:

- Reads the spec (self-contained in the issue, per existing convention)
- Uses `otter` CLI to read/write files, create commits, comment on issues
- Follows the project scope and org rules injected by Ellie
- Closes issues when done

Temps don't coordinate with each other directly. Frank handles cross-cutting concerns. Issues handle sequencing (dependencies, blocking).

### 6. Ellie Reviews

When issues are closed, Ellie comes back around:

1. **Compliance check** — did the agent follow all rules in the project scope and org-wide policies?
2. **Context extraction** — are there lessons, decisions, or patterns worth preserving in organizational memory?
3. **Quality gate** — does the output meet the bar? If not, reopen with specific feedback.
4. **Memory update** — stores relevant context for future projects

### 7. Lori Tears Down

Once all issues are closed and Ellie has signed off:

1. **Deallocates agent slots** — temps are terminated
2. **Reclaims resources** — no lingering sessions, memory files, or identity state
3. **Updates talent pool stats** — which profiles performed well on which types of work (future hiring signal)

### 8. Frank Reports

Frank summarizes results to Sam. Project complete. Work product lives in Otter Camp.

## What Changes

### Dies

| Thing | Why |
|-------|-----|
| 13 permanent agent slots | Replaced by 3 + dynamic pool |
| Per-agent SOUL.md / IDENTITY.md | Temps get role cards from talent pool, not custom identity files |
| Per-agent memory/ directories | Ellie owns organizational memory centrally |
| Agent workspace symlinks to SamsBrain | Temps don't have persistent workspaces |
| Agent rename migrations | No permanent agents to rename (beyond the three) |
| Slack channels per agent function | Frank routes. Temps don't need Slack presence |
| Heartbeat polling 13 agents | Only 3 persistent agents to heartbeat |

### Stays

| Thing | Why |
|-------|-----|
| Otter Camp | Work management system — more important than ever |
| `otter` CLI + REST API | How temps interact with Otter Camp. Already works. MCP can layer on later. |
| Talent pool (230 profiles) | Lori's hiring roster |
| Issue-driven workflow | Specs → execution → review. Unchanged. |
| Codex pipeline | Execution engine for temps (external cron processes issues) |
| SamsBrain | Sam's personal notes. Not agent infrastructure. |

### New / Reframed

| Thing | What |
|-------|------|
| **Elephant → Ellie** | Memory system (#111) becomes Ellie's core capability. Not a feature — a permanent agent. |
| **Chameleon → Lori** | Identity system (#110) becomes one of Lori's capabilities — but her core is process design. She decomposes goals into multi-stage pipelines, selects specialists, defines review gates, and learns which workflows produce the best outcomes. Provisioning is just the last step. |
| **Talent Pool → Hiring Roster** | 230 profiles become Lori's candidate database with performance history |
| **Compliance Framework** | Ellie's review process. Org-wide rules + project-specific scope. |
| **Staffing Requests** | New primitive: Frank requests roles, Lori provisions them |
| **Agent Slot Management** | Dynamic allocation/deallocation of OpenClaw agent slots |

## Otter Camp Implications

This architecture makes Otter Camp the **central nervous system**, not just a project tracker:

1. **Staffing as a primitive** — Otter Camp knows which agents are active on which projects, who hired them, what their role is
2. **Compliance as a primitive** — Ellie's review checklists live in Otter Camp, attached to projects
3. **Memory as a primitive** — Ellie's organizational knowledge is stored in and served from Otter Camp (#111)
4. **Agent provisioning API** — Lori calls Otter Camp (via CLI/REST) to register temps, assign issues, configure access
5. **Talent pool with history** — Agent profiles gain a track record: what they've worked on, quality scores, specialties proven in practice

## OpenClaw Implications

Most of the dynamic agent lifecycle is **already solved by Chameleon** (Spec #110):

1. **Agent creation** — ✅ Already exists. `POST /api/admin/agents` creates a DB record + scaffolds identity files. Chameleon routes sessions at runtime. No `openclaw.json` restart needed.
2. **Profile injection** — ✅ Already exists. `otter agent create` accepts `--soul`, `--identity`, `--model`, `--profile-id`. Talent pool profiles map directly to these inputs.
3. **Agent retirement** — ✅ Already exists. `otter agent archive` moves files to `_retired/`, updates DB status to `retired`.
4. **Session routing** — ✅ Already exists. Chameleon session keys (`agent:chameleon:oc:{UUID}`) route to the correct agent identity automatically.
5. **Minimal persistent config** — ✅ Already exists. OpenClaw config normalizes to `[primary, chameleon]` — two slots, unlimited agents via Chameleon.

**What's actually new:**

1. **Ephemeral lifecycle flag** — Temps should be marked as ephemeral at creation time so Lori (and cleanup jobs) know they're disposable. Could be a `--ephemeral` flag on `otter agent create` or a status/tag in the DB.
2. **Bulk teardown** — Lori needs to retire all temps for a completed project at once. Current API is one-at-a-time.
3. **Temp-to-project association** — Track which temps are assigned to which project for lifecycle management and cost attribution.

## Ellie's Retrieval Quality: Capture & Reporting

Making Ellie good at context injection is an iterative problem. The system must capture retrieval quality signals and report on them so improvements compound — both for this instance and across all Otter Camp users.

### The Zero-Hallucination Rule

**Ellie never generates context. She only retrieves it.**

When injecting context into a temp's working memory, Ellie operates under a hard constraint: every piece of context she provides must trace back to a specific memory entry with a source (session, project, issue, timestamp). If she doesn't have a relevant memory, she includes nothing — not a guess, not an inference, not a "probably."

This means:
- **No synthesized summaries** that blend real memories with plausible-sounding filler
- **No "Sam probably prefers X"** — either there's a stored decision or there isn't
- **No gap-filling** — if the memory store has nothing about database preferences, Ellie's context package says nothing about database preferences
- **Every injected item carries provenance** — source memory ID, when it was created, what project/issue it came from

Temps receive context with clear attribution. If they need more, they ask — and that "ask" becomes a retrieval quality signal (see Quality Signals below). The gap is real information, not Ellie's job to invent.

This is enforced at the system prompt level in Ellie's agent config and validated during compliance review: if a context package contains claims without provenance, it fails review.

### Retrieval Cascade

Ellie retrieves context through a four-tier cascade. Each tier is tried in order; she stops as soon as she finds the answer.

```
Tier 1: Session context (current conversation thread)
  ↓ not found
Tier 2: Memory store (structured memories in Postgres, pgvector semantic search)
  ↓ not found
Tier 3: Chat history store (all raw messages in Postgres, pgvector + keyword search)
  ↓ not found
Tier 4: JSONL file scan (brute force grep over raw session logs — last resort)
  ↓ not found
Tier 5: "I don't have this information." (zero-hallucination rule — no guessing)
```

**Tier 1** is free — just reading the current thread. **Tier 2** is the primary layer — curated, classified, high-signal. **Tier 3** is the safety net — everything ever said, even things nobody thought to memorize. **Tier 4** is belt-and-suspenders for edge cases where Tier 3 ingestion lagged.

The key insight: if Sam told Frank "I'm taking my medication" and Frank said "I'll remember" but the memory pipeline didn't capture it — Tier 2 misses, but Tier 3 finds it in the raw chat history.

### Chat History Embedding Store

All chat messages are stored in Postgres with vector embeddings for semantic search. This is Tier 3 of the retrieval cascade — the exhaustive fallback that ensures nothing said in any conversation is ever truly lost.

**Schema:**

```sql
ALTER TABLE chat_messages ADD COLUMN embedding vector(1536);
CREATE INDEX chat_messages_embedding_idx ON chat_messages
  USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
```

The `chat_messages` table already has `content`, `session_key`, `agent_id`, `timestamp`, `org_id`. We add the `embedding` column alongside the existing text.

**Async embedding worker:**

A background job continuously watches `chat_messages` for rows where `embedding IS NULL` and generates vectors:

```
Loop:
  1. SELECT rows WHERE embedding IS NULL ORDER BY created_at LIMIT batch_size
  2. Call embedding API (e.g., OpenAI text-embedding-3-small) for batch
  3. UPDATE rows with generated vectors
  4. Sleep interval (e.g., 5s) if no rows found
```

This is a simple polling worker, not event-driven. It's self-healing — if it crashes, it picks up where it left off because unembedded rows still have `NULL`. No message queue, no coordination. Just a loop that finds gaps and fills them.

**Properties:**
- **Async** — never blocks chat flow. Messages are stored immediately; embeddings follow behind
- **Self-healing** — `NULL` embedding = unprocessed. Worker always catches up
- **Org-scoped** — queries always filter by `org_id` for tenant isolation
- **Dual search** — supports both semantic search (pgvector cosine similarity) and keyword search (tsvector/trigram) on the same table
- **Backfill-friendly** — existing messages can be backfilled by the same worker on first deploy

**Embedding provider — local by default, bundled with Otter Camp:**

The embedding model ships with Otter Camp and runs locally. No external API key required for search to work.

- **Default:** Local model bundled with install (e.g., `all-MiniLM-L6-v2` via ONNX or `nomic-embed-text` via Ollama)
- **Optional upgrade:** External API (OpenAI `text-embedding-3-small`, Voyage, etc.) for users who want higher quality and have API keys
- **Install step:** `otter install` / `otter setup` downloads and configures the local embedding model as part of initial setup. No separate manual step.
- **Architecture:** The embedding worker talks to a provider interface. Local and API providers implement the same interface. Swapping is a config change — no schema change, no re-architecture. If switching to a different dimension model, a backfill job re-embeds existing rows.
- **Why local-first:** Self-hosted users shouldn't need an OpenAI key just to search their own chat history. The worker is async and not latency-sensitive — local models are plenty fast for background processing.

**Ellie's query pattern for Tier 3:**

```sql
-- Semantic search: "what did Sam say about health routine"
SELECT content, session_key, agent_id, created_at
FROM chat_messages
WHERE org_id = $1
  AND embedding <=> $2 < 0.3
ORDER BY embedding <=> $2
LIMIT 20;

-- Keyword fallback: exact term search
SELECT content, session_key, agent_id, created_at
FROM chat_messages
WHERE org_id = $1
  AND content ILIKE '%medication%'
ORDER BY created_at DESC
LIMIT 20;
```

**Difference from the memory store:**
- Memory store (Tier 2) = curated, classified, high-signal, has provenance metadata, typed kinds
- Chat history store (Tier 3) = raw, complete, low-signal but exhaustive, every message ever sent

Both use pgvector. Both are in Postgres. They serve different purposes in the cascade.

### Ellie's Model Selection

Ellie's workload is high-frequency, medium-complexity: retrieval planning, context assembly, session reading, compliance review, memory classification. None of this requires a frontier model.

**LLM (reasoning/planning): Sonnet-class by default**

| Task | Complexity | Why Sonnet suffices |
|------|-----------|-------------------|
| Retrieval planning | Medium | "What queries should I run?" — structured reasoning |
| Context assembly | Medium | Read, deduplicate, rank, package — analytical |
| Session history reading | Low-Medium | Scan conversation, find relevant info — comprehension |
| Compliance review | Medium | Check work against rules — systematic, rule-following |
| Memory classification | Low-Medium | "Decision, pattern, or anti-pattern?" — categorization |

- **Default:** Claude Sonnet (or equivalent) — fast, cheap, strong at structured tasks
- **Not Opus** — overkill for retrieval planning, and Ellie runs on every issue/project. Cost compounds.
- **Not Haiku** — too weak for nuanced compliance review and cross-project reasoning
- **Configurable:** `ellie.model` in org config. Users can upgrade if needed.

Save Opus for Frank's strategic planning and complex temp work where the reasoning ceiling matters. Ellie is the workhorse — she needs to be fast and reliable, not brilliant.

**Embedding model (for vector store): Local, lightweight**

- **Default:** `all-MiniLM-L6-v2` (384 dims, ~80MB, CPU, milliseconds per batch) — most battle-tested sentence embedding model
- **Alternative:** `nomic-embed-text-v1.5` (768 dims, via Ollama) — better quality, slightly heavier
- **Optional:** External API (`text-embedding-3-small`, Voyage) for users who want higher quality
- **Configurable:** `ellie.embedding_model` in org config

### The "Ask Ellie First" Rule

**Every agent's instruction set includes a hard rule: when you're unsure about something, ask Ellie before asking Sam.**

This covers:
- "What files was I just working with?" — Ellie can read your session history
- "What did Sam say about X?" — Ellie can search organizational memory
- "What's the context for this project?" — Ellie can pull project-scoped knowledge
- "Did we already try this approach?" — Ellie can search for anti-patterns and past decisions

**Why this matters:** Agents lose context constantly — compaction, session boundaries, model limits. The screenshot below is a real example: Jeff G listed 7 image file paths in a message, then two messages later when Sam said "post them to Slack," Jeff had no memory of the paths he'd just provided and started asking clarifying questions from scratch.

Ellie prevents this. She has two capabilities the individual agent doesn't:

1. **Session history access** — Ellie can read the full message log for any session/conversation thread in the org. Even if the agent's own context was compacted, Ellie can pull the raw history via Otter Camp's session API and find the answer. Conversations in Otter Camp are tightly scoped (per-issue, per-project), so session logs are short and crunchy — Ellie can process them fast.

2. **Cross-session memory** — Ellie's memory store spans all sessions and projects. She can correlate "Sam mentioned database preferences in a conversation with Derek last week" with "this new issue is about adding a database."

**The flow when an agent is stuck:**

```
Agent hits uncertainty
  → Queries Ellie: "What image files was I working with in this session?"
  → Ellie reads the session history, finds the file paths
  → Returns: "You generated these 7 files: [paths with provenance]"
  → Agent continues without bothering Sam
```

**If Ellie also doesn't know:** She says so (zero-hallucination rule). The agent then asks Sam, and Ellie captures the answer as a new memory for next time.

This is enforced in every agent's system prompt — permanent and temp alike. It's not optional behavior; it's the first step in the "I don't know" protocol.

### The Two-Scope Retrieval Problem

Every context injection requires two distinct queries:

1. **Project-scoped** — "What is this project? What's its stack, constraints, existing decisions?" → filtered by `project_id`
2. **Org-wide** — "How does Sam think about databases? What patterns have we used before?" → broad search by topic/kind across all memories

Ellie must know when to go narrow vs. broad. This is the core intelligence problem.

### Retrieval Planning

Ellie doesn't fire one query per issue. She runs a **retrieval plan**:

1. Get project context (project-scoped, recent)
2. Get issue details + linked issues
3. Search for topic-relevant decisions org-wide (e.g., "database", "ORM", "migrations")
4. Search for anti-patterns (rejected approaches, failed experiments)
5. Assemble, deduplicate, rank by relevance

The planner itself is an LLM call — "given this issue, what context queries should I run?" — and Ellie executes them. The planner's strategy is versioned config, not hardcoded logic.

### Memory Taxonomy

Retrieval quality depends on classification richness. Memories need typed `kind` values beyond generic summaries:

| Kind | Example | Retrieval Use |
|------|---------|---------------|
| `technical_decision` | "Chose Postgres over SQLite for X reason" | Preference queries |
| `technical_context` | "Project A uses Drizzle, deployed on Fly" | Project-scoped facts |
| `pattern` | "Every project has used explicit migration files" | Cross-project patterns |
| `anti_pattern` | "Tried Prisma, hated it, switched back" | What NOT to do |
| `correction` | "Sam overrode the ORM choice, prefers raw SQL for migrations" | Preference overrides |
| `process_outcome` | "6-agent pipeline worked well for social campaigns" | Lori's process memory |

Better taxonomy → better retrieval precision → better context packages for temps.

### Quality Signals (Captured Per Context Injection)

Every time Ellie injects context into a temp, the system captures:

| Signal | Source | What It Measures |
|--------|--------|-----------------|
| **Context package size** | Ellie (at injection time) | How many memory entries were included |
| **Context items referenced** | Temp agent (during execution) | Which entries the temp actually used — cited, quoted, or built upon |
| **Follow-up questions** | Temp agent (during execution) | Questions the temp asked that indicate missing context |
| **Missed retrievals** | Post-completion diff | Memories that existed and were relevant but weren't included |
| **Retrieval precision** | `items_referenced / items_injected` | Were the right things included? |
| **Retrieval recall** | `items_referenced / (items_referenced + missed_retrievals)` | Were all the right things found? |
| **Stale context flag** | Ellie (at review time) | Was injected context outdated by the time the temp used it? |

### Reporting

Ellie generates retrieval quality reports at two levels:

1. **Per-project** — After project completion, a summary: what context was injected, what was used, what was missing, precision/recall scores. Stored as a project artifact in Otter Camp.
2. **Periodic org-wide** — Weekly/monthly rollup: overall retrieval quality trends, most-useful memory kinds, biggest gaps, taxonomy coverage.

These reports are visible to Frank and Sam. They drive decisions about what to invest in: better classification, more decision logging, taxonomy expansion.

### Platform Learning (Cross-Instance)

Improvements flow from instances to the platform without sharing private data:

1. **Instance data stays private** — Sam's decisions, project context, preferences never leave the org.
2. **Retrieval strategies are versioned config** — "When issue mentions 'database', also search for ORM and migration memories" is a strategy rule. When it proves effective, it ships as a platform default.
3. **Classification improvements ship as code** — Better taxonomy (e.g., splitting `decision` into `technical_decision` vs `process_decision`) is a schema migration. All instances benefit.
4. **Anonymized retrieval quality metrics** — Not content, just signals: precision/recall scores by issue type, strategy pattern effectiveness, taxonomy coverage ratios. Aggregated across orgs to tune default retrieval strategies.

**Flow:**
```
User improves their Ellie (better memories, corrections, taxonomy)
  → retrieval quality metrics improve
  → anonymized metrics feed back to platform
  → platform identifies which strategy patterns work best
  → ships updated default strategies in next release
  → every Ellie instance gets smarter defaults
```

**Design principle:** User data never moves. Ellie's *judgment* — how she decides what to look for — is what improves globally.

## Schema Redesign

The current database has conversations scattered across multiple tables with inconsistent search capabilities. The Three + Temps model requires a unified conversation infrastructure.

### Current State (what exists)

| Table | Purpose | Search | Problems |
|-------|---------|--------|----------|
| `project_chat_messages` | Project-scoped chat | tsvector (text) | No embeddings, no room concept, no participants |
| `agent_memories` | Per-agent memory files | None | Tied to agent_id with CASCADE — teardown deletes memories |
| `memory_entries` | Elephant's structured memories | pgvector (via companion table) | Tied to agent_id with CASCADE |
| `shared_knowledge` | Cross-agent promoted knowledge | pgvector (via companion table) | Separate from memory_entries, redundant structure |
| `activity_log` | General activity | None | Fine as-is |

### Target Schema

Five core tables replace the above:

#### `rooms`

The container for any conversation. Defines who's talking and what the context is.

```sql
CREATE TABLE rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT,
    type TEXT NOT NULL CHECK (type IN ('project', 'issue', 'ad_hoc', 'system')),
    context_id UUID,              -- nullable FK to project/issue depending on type
    last_compacted_at TIMESTAMPTZ, -- watermark for Ellie's injection ledger reset
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

A room may contain two agents and no humans. A room may contain a human and an agent. A room may contain a human and multiple agents. This is the map of who exists within the context of a discussion.

#### `room_participants`

Who's in the room.

```sql
CREATE TABLE room_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    participant_id UUID NOT NULL,   -- FK to agents or users
    participant_type TEXT NOT NULL CHECK (participant_type IN ('agent', 'user')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(room_id, participant_id)
);
```

#### `chat_messages`

Every message ever sent. Replaces `project_chat_messages`.

```sql
CREATE TABLE chat_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    sender_id UUID NOT NULL,        -- agent or user ID
    sender_type TEXT NOT NULL CHECK (sender_type IN ('agent', 'user', 'system')),
    body TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'message' CHECK (type IN (
        'message',           -- normal conversation
        'whisper',           -- targeted, not visible to all (Ellie → agent)
        'system',            -- room events (joins, boundaries)
        'context_injection'  -- Ellie's proactive context (subtype of whisper)
    )),
    conversation_id UUID,           -- nullable, assigned async by segmentation worker
    embedding vector(384),          -- nullable, filled async by embedding worker
    search_document tsvector GENERATED ALWAYS AS (
        to_tsvector('english', coalesce(body, ''))
    ) STORED,
    attachments JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX chat_messages_room_created_idx
    ON chat_messages (org_id, room_id, created_at DESC, id DESC);
CREATE INDEX chat_messages_conversation_idx
    ON chat_messages (conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX chat_messages_search_idx
    ON chat_messages USING GIN (search_document);
CREATE INDEX chat_messages_embedding_idx
    ON chat_messages USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
CREATE INDEX chat_messages_unembedded_idx
    ON chat_messages (created_at) WHERE embedding IS NULL;
```

**Two async workers process messages after creation:**
1. **Embedding worker** — finds rows where `embedding IS NULL`, generates vectors via local model, updates in place. Self-healing polling loop.
2. **Conversation segmentation worker** — finds rows where `conversation_id IS NULL`, groups by topic/time, assigns conversation IDs. Runs on a slight delay to allow topic clusters to form.

When viewing a chat through the web interface, you're looking at the history of a room.

#### `conversations`

Topic-grouped segments within a room. Created asynchronously after messages land.

```sql
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    topic TEXT,                    -- auto-generated summary of the conversation topic
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,         -- NULL if conversation is still active
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Conversations are delineated after the fact — a near-real-time async grouping of messages by topic. This is the unit that memories get attributed to.

#### `memories`

Replaces `memory_entries`, `shared_knowledge`, and `agent_memories`. Org-owned, not agent-owned.

```sql
CREATE TABLE memories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'technical_decision', 'process_decision',
        'preference', 'fact', 'lesson',
        'pattern', 'anti_pattern', 'correction',
        'process_outcome', 'context'
    )),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    importance SMALLINT NOT NULL DEFAULT 3 CHECK (importance BETWEEN 1 AND 5),
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN (
        'active',       -- current, valid memory
        'deprecated',   -- explicitly superseded, kept for historical context
        'archived'      -- old/stale, low relevance
    )),
    superseded_by UUID REFERENCES memories(id) ON DELETE SET NULL,
    source_conversation_id UUID REFERENCES conversations(id) ON DELETE SET NULL,
    source_project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    embedding vector(384),
    content_hash TEXT GENERATED ALWAYS AS (
        encode(digest((kind || ':' || title || ':' || content)::bytea, 'sha256'), 'hex')
    ) STORED,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX memories_dedup_active
    ON memories (org_id, content_hash) WHERE status = 'active';
CREATE INDEX memories_embedding_idx
    ON memories USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)
    WHERE embedding IS NOT NULL;
CREATE INDEX memories_org_kind_idx
    ON memories (org_id, kind, occurred_at DESC);
CREATE INDEX memories_org_status_idx
    ON memories (org_id, status, occurred_at DESC);
CREATE INDEX memories_conversation_idx
    ON memories (source_conversation_id) WHERE source_conversation_id IS NOT NULL;
```

**Key changes from current schema:**
- **No `agent_id`** — memories belong to the org, not an agent. Tearing down a temp doesn't cascade-delete anything.
- **`deprecated` status** — explicitly superseded memories kept for historical context. The `superseded_by` FK points to the replacement.
- **`source_conversation_id`** — traces every memory back to the conversation that produced it, giving full provenance.
- **Inline `embedding`** — no companion table. Vector lives on the row.
- **Expanded `kind` taxonomy** — `technical_decision`, `anti_pattern`, `correction`, `process_outcome`, etc.

#### `activity_log`

Stays as-is. No changes.

### Tables Retired

| Old Table | Replaced By | Migration |
|-----------|------------|-----------|
| `project_chat_messages` | `chat_messages` + `rooms` | Migrate rows, create rooms per project |
| `agent_memories` | `memories` | Migrate content, drop agent_id ownership |
| `memory_entries` | `memories` | Migrate with kind mapping, drop agent_id |
| `memory_entry_embeddings` | Inline on `memories` | Flatten into parent row |
| `shared_knowledge` | `memories` | Merge, map kinds |
| `shared_knowledge_embeddings` | Inline on `memories` | Flatten into parent row |

### Provenance Chain

Every memory traces back to the conversation that produced it:

```
memory → conversation → messages → room → participants
```

"Why do we prefer Postgres?" → memory links to conversation → conversation contains the messages where Sam and Frank discussed it → room shows who was present.

## Migration

1. **Phase 1:** Stand up Ellie (memory infrastructure from #111, but as a permanent agent)
2. **Phase 2:** Stand up Lori (agent provisioning from #110, but as a permanent agent with talent pool access)
3. **Phase 3:** Migrate one project to the new model — Frank specs, Lori hires, Ellie contexts, temps execute
4. **Phase 4:** Prove it works. Iterate. Then sunset the 13-agent config.
5. **Phase 5 (Future):** Add MCP server to Otter Camp — enables non-OpenClaw clients (Claude Desktop, Cursor, etc.) to connect directly

> Note: Dynamic slot management (originally planned as a phase) is already solved by Chameleon. Lori uses existing `otter agent create/archive` commands. No OpenClaw changes needed.

Phases 1-3 can run in parallel with the existing 13-agent setup. No big bang migration.

## Open Questions

### Resolved

1. ~~**How many concurrent temp slots?**~~ — OpenClaw round-robins requests. Scaling concurrent temps is fine; practical limit is API rate limits per account, not OpenClaw slots.
2. ~~**Temp-to-temp communication?**~~ — No. Frank handles cross-cutting. Review is a temp role in the pipeline, not a special mechanism.
3. ~~**Ellie's memory storage**~~ — Postgres + pgvector. Structured memory store (Tier 2) + chat history embedding store (Tier 3). Both specified above.
4. ~~**Lori's provisioning mechanism**~~ — Chameleon + `otter agent create/archive`. Ephemeral flag specified in OpenClaw Implications.
4b. ~~**Lori's process memory**~~ — Process outcomes stored as `process_outcome` kind in memory store. Retrieval quality reports feed back into her process designs.

### Still Open

5. ~~**Frank's identity**~~ — Deferred to migration issue.
6. ~~**Slack presence**~~ — Resolved: Frank relays. Temps don't need Slack presence.
7. ~~**Cost tracking**~~ — Pulled out to its own issue (per-project token usage, attributed to temps/tasks).
8. ~~**What happens to Nova, Max, Derek, etc?**~~ — Deferred to migration issue.

> **All open questions resolved or deferred.** Follow-up issues: Migration (covers #5, #8), Cost Tracking (covers #7).

## The Core Insight

> **Agent identity was always the wrong thing to make permanent. Context and institutional knowledge are what's permanent. Agents are disposable execution units.**

The 13-agent model confused the container for the content. The content is:
- What Sam wants (Frank knows)
- What the org has learned (Ellie knows)
- How to build the right team (Lori knows)

Everything else is temp work.

## Lori's Core Responsibilities

### 1. Staffing
- Receive staffing requests from Frank (project + roles needed)
- Search the talent pool for the best-fit profiles
- Allocate OpenClaw agent slots for temps via Chameleon (`otter agent create`)
- Configure each temp with their role card, CLI/REST auth, and project scope
- Assign issues to the right agents in Otter Camp

### 2. Process Design
- For each project, design the workflow: what phases, what agents per phase, what order
- Define handoff points: when does work move from one agent to the next?
- Define quality gates: who reviews, what criteria, what happens on rejection?
- Set up the issue flow in Otter Camp (labels, assignments, dependencies)
- Determine timing: what runs in parallel, what's sequential, what needs a delay before assessment?

### 3. Agent Lifecycle Management
- Monitor active temps for stalls, errors, or scope drift
- Replace underperforming agents mid-project if needed
- Tear down temps when their work is complete and reviewed (`otter agent archive`)
- Reclaim agent slots for reuse

### 4. Talent Pool Curation
- Track agent performance across projects (quality, speed, accuracy)
- Recommend new profiles when gaps are identified
- Flag profiles that consistently underperform
- Build "go-to teams" for common project types

### 5. Process Improvement
- After each project, capture what worked and what didn't in the workflow
- Feed lessons back into future process designs
- Collaborate with Ellie to store process patterns in organizational memory

## Lori's Process Design Principles

These are the patterns Lori applies across all projects:

1. **Separate creation from review.** The person who builds it should never be the only one who checks it. Different agents, different perspectives.
2. **Separate strategy from assessment.** The person who designed the plan shouldn't grade their own results. Bring in a fresh analyst for performance evaluation.
3. **Plan before execute for anything with real-world consequences.** Infrastructure, public posts, emails, deployments — always have a reviewed plan before touching anything live.
4. **Parallelize where inputs are independent.** Three researchers can work simultaneously on different segments. Don't sequence what doesn't need sequencing.
5. **Gate before publish.** Nothing goes public (posts, deploys, emails) without a review gate. The gate is always a different agent than the creator.
6. **Right-size the team.** A blog post doesn't need 10 agents. Infrastructure doesn't need a copywriter. Match team size to project complexity.
7. **Build in delays for measurement.** If you need to assess results, schedule the assessment with a real time gap. Don't measure a tweet's performance 30 seconds after posting.
8. **Flag elevated permissions.** If a project needs system access, API keys, or public-facing actions, Lori flags it explicitly. Frank (or Sam) approves before granting.
9. **Document the flow, not just the tasks.** Issues alone aren't enough. The sequence, gates, and handoff conditions are what make a process work. Lori captures these in the project's workflow config.
10. **Learn from every project.** After completion, Lori reviews: What worked? What was slow? Where did quality issues slip through? Feed it back to Ellie for organizational memory and into her own process patterns for next time.

## Example: Social Media Campaign

To illustrate the full flow at a task level:

**Request:** "Get me 100 new Twitter followers interested in Otter Camp."

**Frank's project:** Goal = 100 real followers, no bots, no paid. Success = net new followers in 2 weeks with engagement signals.

**Ellie's context:** Product description, current Twitter presence, past social media efforts (including failures), brand voice guidelines, content rules, target audience profile.

**Lori's process design:**
- **Phase 1 — Strategy (1 agent):** Hire a Social Media Strategist. Lori and the strategist brainstorm approaches, land on 5 parallel content experiments (reply engagement, threads, quote-tweets, build-in-public, community engagement). Each experiment has input targets and output goals.
- **Phase 2 — Research (1 agent):** Research Analyst finds targets — 30 tweets to reply to, 3 thread angles, 10 trending posts.
- **Phase 3 — Content Creation (1 agent):** Content Writer drafts all content following brand voice and content rules.
- **Phase 4 — Review (1 agent):** Content Reviewer (separate from writer) checks voice, strategy alignment, rule compliance, risk.
- **Phase 5 — Execution (1 agent):** Social Media Operator posts approved content on schedule.
- **Phase 6 — Assessment (1 agent, delayed):** Performance Analyst (deliberately NOT the original Strategist) measures results at 2h, 24h, 72h. Compares experiments. Recommends adjustments.

**Issue flow:** Strategist → Researcher → Writer → Reviewer (gate) → Operator → Analyst (delayed)
**Total temps:** 6. **Lori's value:** Separated creation from review from assessment. Built in measurement delays. Kept strategist away from grading their own plan.

## Example: Multi-Project Product Build

For large products (e.g., building Otter Camp itself), Lori manages multiple concurrent projects:

- Splits work into separate Otter Camp projects by domain (backend, frontend, testing, marketing, strategy)
- Each project has its own pipeline, staffing, and review gates
- Lori coordinates cross-project dependencies (frontend needs API contracts from backend)
- Rolling staffing — not all projects fully staffed simultaneously; ramps up/down as needed
- Sprint-like cycles (1-2 weeks) for cross-project progress review
- Integration checkpoints verify that separate workstreams still fit together
- Milestone gates require multi-agent sign-off (QA, Design, Frank, Ellie) before release
- Peak concurrent temps: ~8-10 across all projects
- QA reports to Lori, not to the team being tested (independence)

## References

- #110 — Chameleon Agent Architecture (superseded — becomes Lori's mechanism)
- #111 — Memory Infrastructure Overhaul (reframed — becomes Ellie's core)
- #124 — MCP Restructure (future enhancement, not a dependency for this work)
- Talent Pool: `data/agents/` (230 profiles, built Feb 11)
- Current org: AGENTS.md (the 13-agent model being replaced)

## Execution Log

- [2026-02-12 11:38 MST] Issue #n/a | Commit n/a | in_progress | Moved spec 125 from 01-ready to 02-in-progress to begin execution loop | Tests: n/a
- [2026-02-12 11:39 MST] Issue #n/a | Commit n/a | in_progress | Created dedicated branch codex/spec-125-three-plus-temps-architecture from origin/main for spec isolation | Tests: n/a
- [2026-02-12 11:39 MST] Issue #803 | Commit n/a | opened | Planned schema/store lifecycle metadata slice with explicit test commands | Tests: n/a
- [2026-02-12 11:39 MST] Issue #804 | Commit n/a | opened | Planned admin API lifecycle metadata slice with explicit test commands | Tests: n/a
- [2026-02-12 11:39 MST] Issue #805 | Commit n/a | opened | Planned CLI create lifecycle flag support with explicit test commands | Tests: n/a
- [2026-02-12 11:39 MST] Issue #806 | Commit n/a | opened | Planned bulk project temp retirement API slice with explicit test commands | Tests: n/a
- [2026-02-12 11:39 MST] Issue #807 | Commit n/a | opened | Planned CLI bulk teardown command slice with explicit test commands | Tests: n/a
- [2026-02-12 11:41 MST] Issue #803 | Commit 7df4c71 | closed | Added agent lifecycle metadata migration and AgentStore persistence defaults/round-trip coverage | Tests: go test ./internal/store -run 'TestAgentStore_Create_(WithAllFields|DefaultLifecycleMetadata)' -count=1; go test ./internal/store -count=1
- [2026-02-12 11:45 MST] Issue #804 | Commit 726bd37 | closed | Added admin agents lifecycle request/response metadata with project scope validation and lifecycle API tests | Tests: go test ./internal/api -run 'TestAdminAgents(CreatePersistsLifecycleMetadata|CreateRejectsProjectOutsideWorkspace|ListIncludesLifecycleMetadata|GetIncludesLifecycleMetadata)' -count=1; go test ./internal/api -run 'TestAdminAgents(Create|List|Get).*Lifecycle' -count=1; go test ./internal/api -run TestAdminAgentsCreate -count=1; go test ./internal/api -count=1
- [2026-02-12 11:48 MST] Issue #805 | Commit a459f7a | closed | Added otter agent create lifecycle flags/project resolution and payload compatibility updates for strict admin create decoding | Tests: go test ./cmd/otter -run 'TestBuildAgentCreatePayload|TestResolveAgentCreateProjectID' -count=1; go test ./cmd/otter -count=1; go test ./internal/api -run 'TestAdminAgentsCreate(PersistsLifecycleMetadata|RejectsProjectOutsideWorkspace)' -count=1
- [2026-02-12 11:51 MST] Issue #806 | Commit 1f0e1bf | closed | Added project-scoped bulk ephemeral retirement API endpoint, response summaries, and route/test coverage | Tests: go test ./internal/api -run 'TestAdminAgentsBulkRetireByProjectRetiresOnlyEphemeralTemps|TestAdminRoutesAreRegistered' -count=1; go test ./internal/api -run 'TestAdminAgentsBulkRetireByProject.*' -count=1; go test ./internal/api -run TestAdminRoutesAreRegistered -count=1; go test ./internal/api -count=1
- [2026-02-12 11:53 MST] Issue #807 | Commit 0d520a0 | closed | Added CLI bulk project temp teardown mode and otter client endpoint support with helper coverage | Tests: go test ./internal/ottercli -run TestAgentClientMethodsUseExpectedPathsAndPayloads -count=1; go test ./cmd/otter -run 'TestResolveAgentArchiveProjectID|TestParseBulkArchiveCounts|TestBuildAgentCreatePayload|TestResolveAgentCreateProjectID' -count=1; go test ./internal/ottercli -count=1; go test ./cmd/otter -count=1
- [2026-02-12 11:54 MST] Issue #n/a | Commit n/a | in_progress | Opened PR #808 from codex/spec-125-three-plus-temps-architecture to main for reviewer visibility | Tests: n/a
- [2026-02-12 11:54 MST] Issue #n/a | Commit n/a | needs_review | Moved spec 125 from 02-in-progress to 03-needs-review after completing planned micro-issues #803-#807 | Tests: n/a
- [2026-02-12 12:05 MST] Issue #n/a | Commit n/a | in_progress | Re-queued spec 125 from 03-needs-review to 01-ready then 02-in-progress after PR #808 Backend/Frontend CI failures | Tests: n/a
- [2026-02-12 12:06 MST] Issue #809 | Commit n/a | opened | Created frontend remediation micro-issue for Ellie naming/context assertion drift with explicit tests | Tests: n/a
- [2026-02-12 12:06 MST] Issue #810 | Commit n/a | opened | Created backend remediation micro-issue for OpenClaw required-agent Ellie naming assertion drift with explicit tests | Tests: n/a
- [2026-02-12 12:07 MST] Issue #n/a | Commit dd9ebac | in_progress | Merged origin/main into spec branch to reproduce PR merge-ref CI failures locally before remediation | Tests: go test ./cmd/otter -run TestInitAddsRequiredOpenClawAgentsToConfig -count=1; cd web && npm test -- --run src/pages/AgentDetailPage.test.tsx src/pages/AgentsPage.test.tsx
- [2026-02-12 12:07 MST] Issue #810 | Commit a322b7f | closed | Updated backend OpenClaw/init test expectations to Ellie naming/copy and pushed branch update | Tests: go test ./cmd/otter -run TestInitAddsRequiredOpenClawAgentsToConfig -count=1; go test ./internal/import -run TestEnsureOpenClawRequiredAgents -count=1; go test ./...
- [2026-02-12 12:08 MST] Issue #809 | Commit 807624f | closed | Updated frontend AgentDetail/AgentsPage test expectations to Ellie protected-agent message + memory DM context label and pushed branch update | Tests: cd web && npm test -- --run src/pages/AgentDetailPage.test.tsx src/pages/AgentsPage.test.tsx; cd web && npm test -- --run
- [2026-02-12 12:08 MST] Issue #n/a | Commit n/a | needs_review | Moved spec 125 from 02-in-progress back to 03-needs-review after remediation issues #809/#810 were completed and pushed | Tests: n/a
- [2026-02-12 13:35 MST] Issue #n/a | Commit n/a | completed | Reconciled local queue with GitHub: PR #808 merged and all spec 125 micro-issues closed; moved spec from 01-ready to 05-completed | Tests: n/a
- [2026-02-12 14:00 MST] Issue #n/a | Commit n/a | completed | Corrected local folder-state drift after preflight reconciliation: PR #808 is MERGED and issues #803-#807/#809/#810 are CLOSED, so spec 125 is complete and ready for 05-completed placement | Tests: n/a
