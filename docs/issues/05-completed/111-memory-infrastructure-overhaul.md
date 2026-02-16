## Reviewer Required Changes (2026-02-10 11:40 MST)
Reviewer: Claude Opus 4 (automated reviewer)

### P1

- [x] Knowledge Import handler missing request body size limit
  - Files: `internal/api/knowledge.go:95`
  - Required fix: Wrap `r.Body` in `http.MaxBytesReader(w, r.Body, 1<<20)` before JSON decoding, matching the pattern already used in `memory.go:72`.
  - Required test: Test that a request body exceeding 1 MiB returns 413 or 400.

- [x] Knowledge error handler leaks internal errors and misclassifies status codes
  - Files: `internal/api/knowledge.go:121-130`
  - Required fix: Change `handleKnowledgeStoreError` default case to return a generic message (e.g., `"knowledge operation failed"`) with HTTP 500, matching the pattern in `handleMemoryStoreError` (`memory.go:306`). Never return raw `err.Error()` to client.
  - Required test: Test that an unknown store error returns 500 with a generic message (not the raw error string).

- [x] Memory and knowledge routes use OptionalWorkspace instead of RequireWorkspace
  - Files: `internal/api/router.go:258-265`
  - Required fix: Switch all memory and knowledge routes from `middleware.OptionalWorkspace` to `middleware.RequireWorkspace` (or equivalent). These endpoints perform write operations (POST, DELETE) on org-scoped data and must reject requests without a valid workspace at the middleware layer, not just at the store layer.
  - Required test: Verify that a request with no workspace header/JWT to `POST /memory/entries` returns 401 at the middleware level (not dependent on store error mapping).

- [x] CLI client success-path response body is not size-limited
  - Files: `internal/ottercli/client.go:86`
  - Required fix: Wrap `resp.Body` in `io.LimitReader(resp.Body, maxClientResponseBodyBytes)` before passing to `json.NewDecoder`, matching the error-path pattern already at line 79. This prevents a malicious or buggy server from causing OOM via oversized JSON responses.
  - Required test: Test that a response body exceeding `maxClientResponseBodyBytes` (1 MiB) fails gracefully rather than consuming unbounded memory.

### P2

- [x] Knowledge Import with empty entries array silently deletes all entries
  - Files: `internal/api/knowledge.go:95-110`, `internal/store/knowledge_entry_store.go:130`
  - Required fix: Reject import requests where `len(entries) == 0` with a 400 error (e.g., "at least one entry required"). The current path runs `DELETE FROM knowledge_entries WHERE org_id = $1` then inserts zero rows.
  - Required test: Test that `POST /knowledge/import` with `{"entries": []}` returns 400.

- [x] Memory store missing Update/status-transition method for tiered lifecycle
  - Files: `internal/store/memory_store.go`
  - Required fix: Add an `UpdateStatus(ctx, id, status string) error` method to MemoryStore that validates the status transition (`active -> warm -> archived`) and scopes by org_id. Without this, the tiered hot/warm/cold compaction spec'd in the Beads additions section cannot function.
  - Required test: Test that status transitions follow the valid path and cross-org updates are rejected.

- [x] No upper-bound cap on `limit` in memory List and Search API handlers
  - Files: `internal/api/memory.go:130,168`
  - Required fix: Cap `limit` at the API handler level (e.g., `if limit > 200 { limit = 200 }` for List, `if limit > 100 { limit = 100 }` for Search) for defense-in-depth, rather than relying solely on the store layer.
  - Required test: Test that `?limit=999999` is clamped to the max.

- [x] CLI `memory create` does not validate importance/confidence/sensitivity ranges
  - Files: `cmd/otter/main.go:426-428`
  - Required fix: Add validation that `importance` is 1-5, `confidence` is 0.0-1.0, and `sensitivity` is one of `public|internal|restricted` before sending to the API.
  - Required test: Test that out-of-range values produce a clear error message.

- [x] No test file for MemoryEvaluationPage component
  - Files: `web/src/pages/MemoryEvaluationPage.tsx`
  - Required fix: Add `MemoryEvaluationPage.test.tsx` covering loading, error, and data (pass/fail + failed gates) states.
  - Required test: At minimum: renders loading state, renders error state, renders pass/fail status with metrics.

### P3

- [x] Content-hash dedup unique violation surfaces as raw Postgres error
  - Files: `internal/store/memory_store.go:232-234`
  - Required fix: Catch the unique constraint violation on `idx_memory_entries_dedup` and return a sentinel error (e.g., `ErrDuplicateMemory`) so callers can handle dedup gracefully.
  - Required test: Test that inserting a duplicate memory entry returns `ErrDuplicateMemory`.

- [x] CLI `knowledge import` reads entire file with no size guard
  - Files: `cmd/otter/main.go:731`
  - Required fix: Check file size before `os.ReadFile`; reject files larger than a reasonable limit (e.g., 10 MiB) with a clear error message.
  - Required test: Test that oversized file path produces an error.

- [x] Missing cross-org isolation test for knowledge endpoints
  - Files: `internal/api/knowledge_test.go`
  - Required fix: Add a test (similar to `TestMemoryHandlerOrgIsolation`) that verifies org B cannot read org A's knowledge entries.
  - Required test: The test itself.

# Issue #111 — Memory Infrastructure Overhaul

> STATUS: NOT READY
> Inspired by:
> - https://x.com/ericosiu/status/2020883003346714666 (Eric Osiu — 4-component memory system)
> - https://x.com/jumperz/status/2020850897635442786 (JUMPERZ — collective intelligence layer)
> - https://github.com/steveyegge/beads (Beads — distributed git-backed agent memory/issue tracker)

## Problem

Agent memory is flat markdown files (`MEMORY.md`, `memory/YYYY-MM-DD.md`) with a basic `memory_search` tool (OpenAI embeddings over those files). When context compacts, agents lose everything not captured in those files. There's no structured extraction, no vector search over conversations, no automatic recall, and no post-compaction recovery. An agent can read an article and forget it 6 minutes later after compaction.

But even if we fix per-agent memory, there's a bigger problem: **agents don't learn from each other.** One agent discovers something useful — the others have no idea. Knowledge stays locked in silos. Good insights sit next to outdated garbage in the same files. When an agent makes a mistake, you write it in their rules and hope they read it. There's no self-correction, no pattern recognition, no learning loops.

Coordination is solved. Intelligence isn't.

This spec adds a **6-layer memory system** to OtterCamp: per-agent memory (layers 1-4), shared intelligence (layer 5), and an autonomous evaluation/tuning layer (layer 6) so the system can improve itself without manual quality judging.

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────┐
│                         OTTER CAMP                            │
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌─────────────────────┐ │
│  │ memory_entries│  │memory_vectors│  │  shared_knowledge   │ │
│  │   (postgres) │  │  (pgvector)  │  │    (postgres)       │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬──────────────┘ │
│         │                 │                  │                │
│  ┌──────┴─────────────────┴──────────────────┴──────────────┐ │
│  │                   Memory Service (Go)                     │ │
│  │  - Per-agent CRUD + vector search                         │ │
│  │  - Shared knowledge pool (cross-agent)                    │ │
│  │  - Quality scoring + noise filtering                      │ │
│  │  - Self-correction loops                                  │ │
│  │  - Recall context builder (agent + shared)                │ │
│  └──────────────────────┬───────────────────────────────────┘ │
│                         │ API                                 │
├─────────────────────────┼─────────────────────────────────────┤
│                         │                                     │
│  ┌──────────────────────┴──────────────────────────────────┐  │
│  │                   Bridge (TypeScript)                     │  │
│  │  - Post-compaction detection + recovery                   │  │
│  │  - Automatic recall injection (agent + shared context)    │  │
│  │  - Dispatch-time context envelope handling                │  │
│  │  - Reliability controls (retry/backoff/idempotency)       │  │
│  └──────────────────────┬──────────────────────────────────┘  │
│                         │                                     │
│                  OpenClaw Sessions                            │
└──────────────────────────────────────────────────────────────┘
```

## Buildable Contract (v1)

To keep this implementable, v1 must ship with a strict contract:

1. **Core memory plane only**: structured memory entries, embeddings, semantic search, recall API, compaction recovery.
2. **Shared intelligence guarded**: cross-agent promotion is enabled behind quality gates and feature flags.
3. **Fail-closed behavior**:
   - If recall fails, deliver original user message (no synthetic context).
   - If recovery context cannot be built, log event and continue safely.
4. **No human-in-the-loop required for baseline quality**: CI + evaluator jobs decide pass/fail with objective metrics.
5. **Every iterative improvement is measurable**: changes ship only when benchmark deltas are positive and regression thresholds hold.

## Layer 1: Structured Memory Storage

### Database Schema

**Migration 048: pgvector extension + memory tables**

```sql
-- 048_create_memory_system.up.sql

-- Enable pgvector (Railway Postgres supports this)
CREATE EXTENSION IF NOT EXISTS vector;

-- Structured memory entries (the "diary")
CREATE TABLE memory_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    
    -- What kind of memory
    kind TEXT NOT NULL CHECK (kind IN (
        'summary',      -- hourly/daily session summary
        'decision',     -- a decision that was made (with reasoning)
        'action_item',  -- something to do (with status)
        'lesson',       -- mistake or learning
        'preference',   -- user preference discovered
        'fact',         -- factual info to remember
        'feedback',     -- approval/rejection with reasoning
        'context'       -- general context (catch-all)
    )),
    
    -- Content
    title TEXT NOT NULL,            -- short label ("Decided to use pgvector")
    content TEXT NOT NULL,          -- full detail
    metadata JSONB DEFAULT '{}',    -- flexible: {source_session, related_issue, tags, ...}
    importance SMALLINT NOT NULL DEFAULT 3 CHECK (importance BETWEEN 1 AND 5),
    confidence FLOAT NOT NULL DEFAULT 0.5 CHECK (confidence >= 0 AND confidence <= 1),
    sensitivity TEXT NOT NULL DEFAULT 'internal' CHECK (sensitivity IN ('public','internal','restricted')),
    
    -- Temporal
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), -- when the thing happened
    expires_at TIMESTAMPTZ,                          -- optional TTL (for temp context)
    
    -- Source tracking
    source_session TEXT,            -- OpenClaw session key
    source_project UUID REFERENCES projects(id),
    source_issue TEXT,              -- issue identifier/slug/uuid (project-system agnostic)
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_memory_entries_agent ON memory_entries(agent_id, occurred_at DESC);
CREATE INDEX idx_memory_entries_org ON memory_entries(org_id);
CREATE INDEX idx_memory_entries_kind ON memory_entries(agent_id, kind);
CREATE INDEX idx_memory_entries_source ON memory_entries(source_session);
CREATE INDEX idx_memory_entries_metadata ON memory_entries USING gin(metadata);

-- Vector embeddings for memory entries
CREATE TABLE memory_entry_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    memory_entry_id UUID NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,

    -- The text that was embedded (may be title+content or a chunk)
    chunk_text TEXT NOT NULL,
    chunk_index INT NOT NULL DEFAULT 0,  -- for multi-chunk entries

    -- Deployment-standardized vector dimension (default 768)
    embedding vector(768) NOT NULL,

    -- Which model produced this embedding
    model TEXT NOT NULL DEFAULT 'nomic-embed-text',

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(memory_entry_id, chunk_index)
);

CREATE INDEX idx_memory_entry_embeddings_entry ON memory_entry_embeddings(memory_entry_id);
-- IVFFlat index for fast similarity search
-- lists=100 is good for up to ~100K entries; increase for larger deployments
CREATE INDEX idx_memory_entry_embeddings_vector ON memory_entry_embeddings
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- Agent memory config (per-agent settings)
CREATE TABLE agent_memory_config (
    agent_id UUID PRIMARY KEY REFERENCES agents(id),
    org_id UUID NOT NULL REFERENCES organizations(id),
    
    auto_extract BOOLEAN NOT NULL DEFAULT true,       -- run extraction pipeline
    auto_recall BOOLEAN NOT NULL DEFAULT true,         -- inject recall on prompts
    recall_max_tokens INT NOT NULL DEFAULT 2000,       -- token budget for auto-recall
    recall_min_relevance FLOAT NOT NULL DEFAULT 0.7,   -- minimum cosine similarity
    recall_max_results INT NOT NULL DEFAULT 5,         -- max memories to inject
    extract_interval_minutes INT NOT NULL DEFAULT 5,   -- how often to extract
    
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Compaction events (track when agents lose context)
CREATE TABLE compaction_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    session_key TEXT NOT NULL,
    
    -- What was lost
    pre_compaction_tokens INT,
    post_compaction_tokens INT,
    summary_text TEXT,              -- OpenClaw's compaction summary
    
    -- Recovery
    recovery_injected BOOLEAN NOT NULL DEFAULT false,
    recovery_injected_at TIMESTAMPTZ,
    recovery_token_count INT,
    
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_compaction_events_agent ON compaction_events(agent_id, detected_at DESC);
```

**Down migration:**
```sql
-- 048_create_memory_system.down.sql
DROP TABLE IF EXISTS compaction_events;
DROP TABLE IF EXISTS agent_memory_config;
DROP TABLE IF EXISTS shared_knowledge_embeddings;
DROP TABLE IF EXISTS shared_knowledge;
DROP TABLE IF EXISTS agent_teams;
DROP TABLE IF EXISTS memory_entry_embeddings;
DROP TABLE IF EXISTS memory_entries;
DROP EXTENSION IF EXISTS vector;
```

### Go Store: `internal/store/memory_store.go`

```go
type MemoryEntry struct {
    ID             string     `json:"id"`
    OrgID          string     `json:"org_id"`
    AgentID        string     `json:"agent_id"`
    Kind           string     `json:"kind"`
    Title          string     `json:"title"`
    Content        string     `json:"content"`
    Metadata       JSONMap    `json:"metadata"`
    Importance     int        `json:"importance"`  // 1-5
    Confidence     float64    `json:"confidence"`  // 0-1
    Sensitivity    string     `json:"sensitivity"` // public|internal|restricted
    OccurredAt     time.Time  `json:"occurred_at"`
    ExpiresAt      *time.Time `json:"expires_at,omitempty"`
    SourceSession  *string    `json:"source_session,omitempty"`
    SourceProject  *string    `json:"source_project,omitempty"`
    SourceIssue    *string    `json:"source_issue,omitempty"`
    CreatedAt      time.Time  `json:"created_at"`
    UpdatedAt      time.Time  `json:"updated_at"`
    // Populated on search results
    Relevance      *float64   `json:"relevance,omitempty"`
}

type MemorySearchParams struct {
    AgentID        string
    Query          string   // semantic search query
    Kinds          []string // filter by kind
    MinRelevance   float64  // cosine similarity threshold
    MinImportance  int      // quality floor (1-5)
    AllowedScopes  []string // public/internal/restricted visibility
    Limit          int
    Since          *time.Time
    Until          *time.Time
    SourceProject  *string
}

type MemoryStore struct {
    db *sql.DB
}
```

**Key methods:**
- `Create(ctx, entry) → MemoryEntry` — insert + auto-embed
- `List(ctx, agentID, kind, limit, offset) → []MemoryEntry` — chronological list
- `Search(ctx, params MemorySearchParams) → []MemoryEntry` — vector similarity search
- `GetRecallContext(ctx, agentID, query, config) → string` — build recall injection text
- `Delete(ctx, id)` — remove entry + cascade embeddings
- `WriteBatch(ctx, entries []MemoryEntry)` — bulk insert (for extraction pipeline)
- `GetDailySummary(ctx, agentID, date) → string` — compile day's memories into text
- `Compact(ctx, agentID, olderThan time.Time)` — merge old granular entries into summaries

### Embedding Pipeline

Embedding happens server-side in the Go backend. On `Create()`:

1. Concatenate `title + "\n" + content`
2. If < 8000 tokens, embed as single chunk
3. If longer, split into ~1000 token chunks with 200 token overlap
4. Call configured embedder (default: Ollama `nomic-embed-text`, 768-dim)
5. Store chunks in `memory_entry_embeddings`

**Config**: embedding provider/model come from workspace settings. OpenAI key is only required when provider is `openai`.

**Search** uses pgvector's cosine distance:
```sql
WITH ranked AS (
  SELECT
    me.id AS memory_entry_id,
    MAX(1 - (emb.embedding <=> $1::vector)) AS relevance
  FROM memory_entries me
  JOIN memory_entry_embeddings emb ON emb.memory_entry_id = me.id
  WHERE me.agent_id = $2 AND me.org_id = $3
    AND me.importance >= $4
    AND 1 - (emb.embedding <=> $1::vector) >= $5
  GROUP BY me.id
)
SELECT me.*, r.relevance
FROM ranked r
JOIN memory_entries me ON me.id = r.memory_entry_id
ORDER BY r.relevance DESC
LIMIT $6;
```

## Layer 2: The Memory Agent

Instead of bridge scripts parsing logs, a **dedicated agent** handles all memory extraction and distribution. It reads session logs, understands context, makes judgment calls about what's valuable, scopes memories correctly, and distributes them to the right audience.

This is the intelligence layer — an LLM doing the work that dumb string parsing can't.

### Architecture

The Memory Agent runs as a dedicated OpenClaw agent slot (not Chameleon):

```
┌─────────────────────────────────────────────────────────┐
│                      OpenClaw                            │
│                                                          │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐     │
│  │   Frank     │  │   Derek     │  │    Nova     │ ... │
│  │  (sessions) │  │  (sessions) │  │  (sessions) │     │
│  │   ↓ JSONL   │  │   ↓ JSONL   │  │   ↓ JSONL   │     │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘     │
│         │                │                │              │
│         └────────────────┼────────────────┘              │
│                          ↓                               │
│                 ┌────────────────┐                       │
│                 │  Memory Agent  │  ← cron every 5 min   │
│                 │                │                       │
│                 │  Reads JSONL   │                       │
│                 │  Extracts      │                       │
│                 │  Scopes        │                       │
│                 │  Distributes   │                       │
│                 └───────┬────────┘                       │
│                         │                                │
└─────────────────────────┼────────────────────────────────┘
                          │ otter memory write
                          │ otter knowledge share
                          ↓
                    ┌────────────┐
                    │ Otter Camp │
                    │  Memory DB │
                    └────────────┘
```

### How It Works

**Every 5 minutes**, the Memory Agent:

1. **Scans active JSONL files** — checks `~/.openclaw/agents/*/sessions/*.jsonl` for files modified since last run. Tracks byte offsets per file (stored in its own workspace file) so it only reads new content.

2. **Reads new entries** — parses JSONL delta. Filters to `type: "message"` entries (skips model_change, thinking_level_change, etc.). Focuses on user messages and assistant responses.

3. **Extracts structured memories** — uses its own LLM context to understand what's valuable. This is the key advantage over a bridge script: the agent can understand nuance, context, and importance.

4. **Scopes each memory** — determines who needs to know:
   - `agent` — only the originating agent (e.g., a tool config detail)
   - `team` — the agent's team (e.g., "Sam prefers small commits" told to Derek → all engineering)
   - `org` — everyone (e.g., "We're using pgvector for memory storage")

5. **Writes via OtterCamp API** — `otter memory write` for agent-scoped, `otter knowledge share` for team/org-scoped. Tags with metadata: source agent, source session, team scope, entities mentioned.

### JSONL Processing

```
Session files: ~/.openclaw/agents/{agentId}/sessions/{sessionId}.jsonl

Structure per line:
  { type: "session", id, timestamp, cwd }
  { type: "model_change", modelId, provider }
  { type: "message", message: { role: "user"|"assistant", content: [...] } }
  { type: "custom", ... }

Active files: ~25 modified in the last hour across 9 agents
Total files: ~3,739 (most historical, ignored)
Largest session: 39MB (main agent)
```

**Delta tracking** — the agent maintains a state file in its workspace:
```json
// memory-agent-state.json
{
  "file_offsets": {
    "/Users/sam/.openclaw/agents/main/sessions/abc123.jsonl": {
      "byte_offset": 1048576,
      "last_processed": "2026-02-09T18:00:00Z",
      "line_count": 4521
    }
  },
  "last_run": "2026-02-09T18:00:00Z"
}
```

The agent reads from `byte_offset` to EOF, processes new lines, updates the offset. Files not modified since last run are skipped entirely.

### Extraction Prompt (the agent's core logic)

The Memory Agent's AGENTS.md / system prompt includes extraction rules:

```markdown
## Your Job

You are the Memory Agent. Every 5 minutes, you read new session activity from all agents
and extract structured memories. You decide what's worth remembering and who needs to know.

## What to Extract

- **Decisions** with reasoning (why, not just what)
- **Preferences** the operator expresses (style, workflow, priorities)
- **Lessons** from mistakes or discoveries
- **Facts** that persist (configs, URLs, names, relationships)
- **Action items** with status
- **Corrections** when the operator rejects something (capture the WHY)

## What to Skip

- Routine tool calls (file reads, searches)
- Acknowledgments ("ok", "thanks", "got it")
- Transient context (debugging sessions, one-off questions)
- Anything already captured in a previous extraction

## Scoping Rules

- **agent scope**: Tool configs, session-specific context, personal workflow details
- **team scope**: Style preferences told to one agent that apply to the team
  - Engineering team: Derek, Josh S, Jeremy H, Jeff G
  - Content team: Stone, Nova, Jeff G
  - Personal ops: Max, Penny
  Example: Sam tells Derek "I prefer small, incremental commits" → team:engineering
- **org scope**: Decisions that affect everyone, project-wide facts, org preferences
  Example: "We're using Otter Camp for all work management" → org

## Output Format

For each extraction, call:
- `otter memory write --agent <agent-slug> --kind <kind> --title "..." --content "..." --scope agent`
- `otter knowledge share --kind <kind> --title "..." --content "..." --scope <team|org> --teams <team-list>`
```

### Cost Model

**Input (reading JSONL):** Free — local filesystem reads, no API calls.

**Processing (LLM extraction):** This is the cost center.
- ~25 active sessions per hour
- Most sessions: small deltas (a few hundred tokens of new messages between 5-min cycles)
- Heavy sessions (Sam talking to Frank): maybe 2-5K tokens of new content per cycle
- Using Claude Max subscription: $0 marginal cost (we're already on Claude Max)
- If using API: Haiku at $0.25/1M input ≈ pennies per cycle

**Output (writing to OtterCamp):** HTTP calls to local API — free.

**Estimated per-cycle cost on Claude Max:** $0 (it's just another agent session)
**Estimated per-cycle cost on API:** ~$0.01-0.05 depending on delta size

### Optimization: Triage Pass

For cost-sensitive setups, add a two-pass approach:

1. **Triage** (cheap/fast): Quick scan of new content — "is there anything worth extracting here?" Most 5-min windows will have nothing interesting. Return early.
2. **Extract** (thorough): Only when triage says yes, do the full structured extraction with scoping.

This cuts LLM calls by ~80% since most cycles have no new valuable content.

### Daily Rollup

End of day (configurable, default midnight in operator's timezone), the Memory Agent:
1. Reads all memories created today for each agent
2. Synthesizes a daily summary (highlights, decisions, progress)
3. Writes a `summary` entry per agent
4. Writes an org-wide daily summary (what happened across the whole team today)

### Memory Agent Setup

### Dedicated OpenClaw Agent

The Memory Agent gets its own agent slot in OpenClaw. Memory is too critical to share a slot or run as a transient Chameleon identity.

**openclaw.json config:**
```json
{
  "id": "memory-agent",
  "name": "Memory Agent",
  "model": "anthropic/claude-sonnet-4-20250514",
  "workspace": "~/.openclaw/workspace-memory-agent",
  "thinking": "low",
  "channels": []
}
```

**Cron jobs:**
```json
{
  "name": "memory-extraction",
  "schedule": { "kind": "every", "everyMs": 300000 },
  "payload": {
    "kind": "agentTurn",
    "message": "Run memory extraction cycle. Read new JSONL entries from all agent sessions, extract structured memories, scope and distribute via otter CLI."
  },
  "sessionTarget": "isolated",
  "delivery": { "mode": "none" }
}
```

```json
{
  "name": "memory-evaluator",
  "schedule": { "kind": "cron", "expr": "0 3 * * *" },
  "payload": {
    "kind": "agentTurn",
    "message": "Run memory benchmark evaluation suite and store metrics. Do not change config in this run."
  },
  "sessionTarget": "isolated",
  "delivery": { "mode": "none" }
}
```

```json
{
  "name": "memory-tuner",
  "schedule": { "kind": "cron", "expr": "30 3 * * *" },
  "payload": {
    "kind": "agentTurn",
    "message": "If latest evaluator run passed all hard gates, run one bounded tuning attempt and apply only if score improves with no regressions; otherwise no-op."
  },
  "sessionTarget": "isolated",
  "delivery": { "mode": "none" }
}
```

**Workspace files:**

`~/.openclaw/workspace-memory-agent/SOUL.md`:
```markdown
# Memory Agent

You are the Memory Agent. Your job is to read agent session logs, extract what's worth
remembering, and distribute it to the right audience via Otter Camp.

You run every 5 minutes. Be fast, be accurate, be quiet.

## Extraction Rules
- Read JSONL deltas from ~/.openclaw/agents/*/sessions/*.jsonl
- Track byte offsets in memory-agent-state.json (only process new content)
- Extract: decisions, preferences, lessons, facts, action items, corrections
- Skip: routine tool calls, acks, small talk, transient debugging

## Scoping Rules
- agent scope: tool configs, session-specific context
- team scope: preferences/standards told to one agent that apply to the team
- org scope: decisions affecting everyone, project-wide facts

## Team Membership
Query team membership from Otter Camp: `otter teams list`
Do NOT hardcode team assignments.

## Output
- Agent-scoped: `otter memory write --agent <slug> --kind <kind> --title "..." --content "..."`
- Team/org-scoped: `otter knowledge share --kind <kind> --title "..." --content "..." --scope <team|org> --teams <list>`

## Quality Bar
If you're unsure whether something is worth extracting, it probably isn't. 
Err on the side of less noise, not more coverage.
When in doubt about scope, keep it agent-level. Promote later if it recurs.
```

`~/.openclaw/workspace-memory-agent/memory-agent-state.json`:
```json
{
  "file_offsets": {},
  "last_run": null,
  "extraction_stats": {
    "total_runs": 0,
    "total_memories_written": 0,
    "total_knowledge_shared": 0,
    "last_run_duration_ms": 0
  }
}
```

**Model choice:** Sonnet, not Opus. The Memory Agent does formulaic extraction work — it doesn't need the most powerful model. Sonnet is fast, cheap, and more than capable of structured extraction. On Claude Max, the cost difference is irrelevant, but for API users it matters.

**No Slack/Telegram channels.** The Memory Agent is invisible infrastructure. It doesn't chat. It reads, extracts, writes, and shuts up.

**Read access:** The Memory Agent's workspace is on the same machine as all other agents. It reads JSONL files via `exec` (cat/tail). No special permissions needed — all files are owned by the same user.

## Layer 3: Post-Compaction Recovery

### Detection

The bridge already tracks session state via sync. Compaction detection:

```typescript
function detectCompaction(
    current: OpenClawSession,
    previous: OpenClawSession,
    event?: OpenClawEvent
): boolean {
    // Primary signal: explicit compaction metadata/event from OpenClaw.
    if (event?.type === 'compaction' || current.compactionCount > previous.compactionCount) {
        return true;
    }

    // Secondary fallback (heuristic only): abrupt token drop.
    const prevTokens = getNumeric(previous.totalTokens) || 0;
    const currTokens = getNumeric(current.totalTokens) || 0;
    if (prevTokens > 10000 && currTokens < prevTokens * 0.5) {
        return true;
    }

    return false;
}
```

### Recovery Injection

When compaction detected:

1. Fetch last 24h of memories for this agent from OtterCamp
2. Build recovery context:
   - Agent identity (from Chameleon `whoami` — spec #110)
   - Today's memories (structured)
   - Yesterday's key decisions
   - Active project/issue context
   - Last 5 user messages (from session history)
3. Inject as system event via `chat.send`:

```typescript
async function injectRecoveryContext(
    agentID: string, 
    sessionKey: string
): Promise<void> {
    // Build recovery payload
    const memories = await fetchAgentMemories(agentID, { 
        since: dayjs().subtract(24, 'hours'),
        limit: 20,
        minImportance: 3 
    });
    
    const identity = await fetchAgentIdentity(agentID); // from #110
    
    const recoveryText = buildRecoveryText(identity, memories);
    
    // Inject into session
    await sendRequest('chat.send', {
        sessionKey,
        message: `[MEMORY RECOVERY — Context restored after compaction]\n\n${recoveryText}`,
        role: 'system'
    });
    
    // Log the compaction event
    await logCompactionEvent(agentID, sessionKey, recoveryText);
}
```

### Recovery Text Format

```markdown
## Memory Recovery — [Agent Name]

### Who You Are
[Identity from whoami]

### Today's Context (Feb 9, 2026)
**Decisions:**
- Chose pgvector over FAISS for memory storage (centralized, Postgres-native)
- Memory extraction runs every 5 min with triage-first filtering

**Active Work:**
- Issue #111: Memory infrastructure overhaul (in progress)
- Issue #110: Chameleon agent architecture (in review)

**Key Facts:**
- Eric Osiu article inspired memory redesign
- Sam wants casual message sent to Eric about OtterCamp
- Railway auto-deploys from main branch

**Lessons:**
- contextTokens ≠ totalTokens (caused all-agents-offline bug)
- Bridge schedule objects need flattening before storage

### Yesterday's Highlights
[Compressed summary of yesterday's memories]
```

## Layer 4: Automatic Recall

### How It Works

The bridge intercepts every user message before it reaches the agent. It:

1. Extracts the semantic intent of the message
2. Searches vector memory for relevant past context
3. Prepends relevant memories as a system note
4. Forwards the enhanced message to the agent

### Bridge Integration Point

The bridge already handles `dm.message` dispatch events. Add recall before forwarding:

```typescript
async function handleDMDispatchEvent(event: DMDispatchEvent): Promise<void> {
    const agentID = resolveAgentID(event);
    const config = await fetchMemoryConfig(agentID);

    let systemContext: string | undefined;

    if (config.auto_recall) {
        const recallContext = await buildRecallContext(agentID, event.content, config);
        if (recallContext) {
            // Inject as system/custom context, not user message text.
            systemContext = `[RECALLED CONTEXT]\n${recallContext}`;
        }
    }

    await sendMessageToSession(event.sessionKey, {
        userMessage: event.content,
        systemContext,
    });
}
```

### Recall Context Builder

```typescript
async function buildRecallContext(
    agentID: string,
    query: string,
    config: MemoryConfig
): Promise<string | null> {
    // Search vector memory
    const results = await searchMemory({
        agentID,
        query,
        minRelevance: config.recall_min_relevance,
        limit: config.recall_max_results
    });
    
    if (results.length === 0) return null;
    
    // Format as concise context block
    const lines = results.map(r => 
        `- [${r.kind}] ${r.title}: ${r.content} (${dayjs(r.occurred_at).fromNow()})`
    );
    
    // Trim to token budget
    return trimToTokenBudget(lines.join('\n'), config.recall_max_tokens);
}
```

### Important: Recall Should Be Invisible

The agent shouldn't have to think about recall. It just... knows things. The `[RECALLED CONTEXT]` block must be delivered as system/custom context (never concatenated into user text). The agent sees relevant memories appear naturally in context without prompt-injection side effects.

**Token budget matters.** Default 2000 tokens for recall injection. Configurable per-agent. Too much recall = wasted context window. Too little = might as well not have it.

## Layer 5: Shared Intelligence (Cross-Agent Learning)

> "No single agent can solve it alone. The whole network has to evolve together." — @jumperz

Per-agent memory (layers 1-4) solves amnesia. But agents still operate in silos — one agent discovers something useful and the others have no idea. Layer 5 makes agent knowledge **collective**.

### 5A: Shared Knowledge Pool

Not all memories are private. Some are org-wide facts that every agent should know.

**Database: `shared_knowledge` table**

```sql
CREATE TABLE shared_knowledge (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    
    -- Who contributed this
    source_agent_id UUID NOT NULL REFERENCES agents(id),
    source_memory_id UUID REFERENCES memory_entries(id),
    
    -- Content (same structure as memory_entries)
    kind TEXT NOT NULL CHECK (kind IN (
        'decision',     -- org-level decision
        'lesson',       -- mistake any agent should avoid
        'preference',   -- user preference (applies to all agents)
        'fact',         -- factual info everyone needs
        'pattern',      -- detected pattern across agents
        'correction'    -- self-correction rule
    )),
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    
    -- Scoping (who should see this)
    scope TEXT NOT NULL DEFAULT 'org' CHECK (scope IN ('team', 'org')),
    scope_teams TEXT[] DEFAULT '{}',  -- e.g. {'engineering','content'} — empty = org-wide
    
    -- Quality scoring
    quality_score FLOAT NOT NULL DEFAULT 0.5,  -- 0-1, decays over time
    confirmations INT NOT NULL DEFAULT 0,       -- how many agents have validated this
    contradictions INT NOT NULL DEFAULT 0,      -- how many agents have contradicted this
    last_accessed_at TIMESTAMPTZ,               -- for relevance decay
    
    -- Lifecycle
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'stale', 'superseded', 'archived')),
    superseded_by UUID REFERENCES shared_knowledge(id),
    
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_shared_knowledge_org ON shared_knowledge(org_id, quality_score DESC);
CREATE INDEX idx_shared_knowledge_kind ON shared_knowledge(org_id, kind);
CREATE INDEX idx_shared_knowledge_status ON shared_knowledge(org_id, status);
CREATE INDEX idx_shared_knowledge_scope ON shared_knowledge(org_id, scope);
CREATE INDEX idx_shared_knowledge_teams ON shared_knowledge USING gin(scope_teams);

-- Agent-to-team mapping (which agents belong to which teams)
-- This drives the Memory Agent's scoping decisions and recall filtering
CREATE TABLE agent_teams (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    team_name TEXT NOT NULL,  -- e.g. 'engineering', 'content', 'personal-ops'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(org_id, agent_id, team_name)
);

CREATE INDEX idx_agent_teams_agent ON agent_teams(agent_id);
CREATE INDEX idx_agent_teams_team ON agent_teams(org_id, team_name);

-- Shared knowledge gets its own embedding table (avoids nullable-XOR complexity)
CREATE TABLE shared_knowledge_embeddings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    shared_knowledge_id UUID NOT NULL REFERENCES shared_knowledge(id) ON DELETE CASCADE,
    chunk_text TEXT NOT NULL,
    chunk_index INT NOT NULL DEFAULT 0,
    embedding vector(768) NOT NULL,
    model TEXT NOT NULL DEFAULT 'nomic-embed-text',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(shared_knowledge_id, chunk_index)
);

CREATE INDEX idx_shared_knowledge_embeddings_entry ON shared_knowledge_embeddings(shared_knowledge_id);
CREATE INDEX idx_shared_knowledge_embeddings_vector ON shared_knowledge_embeddings
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);
```

### 5B: Promotion Pipeline (Agent Memory → Shared Knowledge)

Not every agent memory should be shared. The system needs a quality filter.

**Automatic promotion triggers:**
1. **Importance + confidence threshold**: Memory with `importance >= 4`, `confidence >= 0.7`, and `kind IN ('decision', 'lesson', 'preference', 'fact')` gets auto-promoted
2. **Cross-agent convergence**: When 2+ agents independently record similar memories (cosine similarity > 0.85), the overlapping insight gets promoted with boosted quality score
3. **Human approval (optional override)**: Operator can explicitly mark a memory as shared via UI or CLI
4. **Feedback-derived**: When the pipeline (#105) records an approval/rejection pattern, the lesson gets promoted

**Noise filtering:**
- Memories with `importance <= 2` or `confidence < 0.6` never auto-promote
- Agent-specific context (tool configs, session quirks) stays private
- Promoted entries start at `quality_score = 0.6` and must earn higher through confirmations

```typescript
async function evaluateForPromotion(entry: MemoryEntry): Promise<boolean> {
    // Rule 1: High importance + confidence + promotable kind
    if (
        entry.importance >= 4 &&
        entry.confidence >= 0.7 &&
        PROMOTABLE_KINDS.includes(entry.kind)
    ) {
        return true;
    }
    
    // Rule 2: Cross-agent convergence
    const similar = await searchSharedKnowledge({
        query: entry.title + ' ' + entry.content,
        minRelevance: 0.85,
        excludeAgent: entry.agentID
    });
    
    if (similar.length > 0) {
        // Another agent already knows something similar — confirm it
        await confirmSharedKnowledge(similar[0].id, entry.agentID);
        return false; // Don't duplicate, just confirm existing
    }
    
    // Check if another agent has a similar private memory
    const crossAgentSimilar = await searchAllAgentMemories({
        query: entry.title + ' ' + entry.content,
        minRelevance: 0.85,
        excludeAgent: entry.agentID,
        limit: 3
    });
    
    if (crossAgentSimilar.length >= 1) {
        // Multiple agents independently discovered the same thing — promote
        return true;
    }
    
    return false;
}
```

### 5C: Quality Scoring & Decay

Shared knowledge isn't static. It decays, gets confirmed, gets contradicted, gets superseded.

**Quality score formula:**
```
quality = base_score 
    + (confirmations * 0.1)      // agents validating it
    - (contradictions * 0.15)    // agents contradicting it
    - (days_since_accessed * 0.005)  // relevance decay
    - (days_since_created * 0.002)   // age decay
```

**Lifecycle transitions:**
- `active` → `stale`: quality drops below 0.3 with no access in 30 days
- `active` → `superseded`: newer entry covers same topic with higher quality
- `stale` → `archived`: no confirmations for 60 days
- Any → `active`: human or agent explicitly re-confirms

**Daily maintenance job:**
```sql
-- Mark stale entries
UPDATE shared_knowledge 
SET status = 'stale', updated_at = NOW()
WHERE status = 'active' 
  AND quality_score < 0.3 
  AND last_accessed_at < NOW() - INTERVAL '30 days';

-- Archive abandoned stale entries
UPDATE shared_knowledge 
SET status = 'archived', updated_at = NOW()
WHERE status = 'stale' 
  AND updated_at < NOW() - INTERVAL '60 days';
```

### 5D: Self-Correction Loops

When an agent makes a mistake, the system should learn — not just that agent, all of them.

**How it works:**

1. **Mistake detection**: Pipeline (#105) records a rejection with reasoning. The rejection reason becomes a `correction` entry.
2. **Pattern recognition**: If similar mistakes happen across 2+ agents, the correction gets boosted to shared knowledge.
3. **Proactive injection**: Corrections with high quality scores get injected into agent recall when the topic comes up. The agent doesn't have to "remember" the mistake — the system reminds them automatically.

```typescript
// When a rejection is recorded in the pipeline
async function handleRejection(params: {
    agentID: string,
    issueID: string,
    rejectionReason: string,
    originalContent: string
}): Promise<void> {
    // Create correction memory for the agent
    const correction = await createMemory({
        agentID: params.agentID,
        kind: 'lesson',
        title: `Rejection: ${summarize(params.rejectionReason)}`,
        content: `Original: ${params.originalContent}\n\nRejected because: ${params.rejectionReason}`,
        importance: 4,
        confidence: 0.9,
        metadata: { source_issue: params.issueID, type: 'correction' }
    });
    
    // Check for cross-agent patterns
    const similarCorrections = await searchAllAgentMemories({
        query: params.rejectionReason,
        kinds: ['lesson'],
        minRelevance: 0.8,
        excludeAgent: params.agentID
    });
    
    if (similarCorrections.length >= 1) {
        // Multiple agents making similar mistakes — promote to shared
        await promoteToShared({
            sourceAgent: params.agentID,
            sourceMemory: correction.id,
            kind: 'correction',
            title: `Common mistake: ${summarize(params.rejectionReason)}`,
            content: `${similarCorrections.length + 1} agents have been corrected on this.\n\n${params.rejectionReason}`,
            qualityScore: 0.8  // high because it's a confirmed pattern
        });
    }
}
```

### 5E: Cross-Agent Signal Detection

When the same entity (company, person, topic, URL) appears in 2+ agents' work, it's a signal.

**Implementation:**
- During memory extraction, entities are tagged in `metadata.entities[]`
- A periodic job scans for entity convergence across agents
- Convergent entities get promoted to shared knowledge with `kind: 'pattern'`

```typescript
async function detectCrossAgentSignals(): Promise<void> {
    // Find entities mentioned by multiple agents in the last 7 days
    const convergentEntities = await db.query(`
        SELECT 
            entity,
            COUNT(DISTINCT agent_id) as agent_count,
            ARRAY_AGG(DISTINCT agent_id) as agents,
            ARRAY_AGG(DISTINCT me.id) as memory_ids
        FROM memory_entries me,
             jsonb_array_elements_text(me.metadata->'entities') entity
        WHERE me.occurred_at > NOW() - INTERVAL '7 days'
          AND me.org_id = $1
        GROUP BY entity
        HAVING COUNT(DISTINCT agent_id) >= 2
    `, [orgID]);
    
    for (const signal of convergentEntities) {
        // Check if we already have this signal
        const existing = await findSharedKnowledge({
            kind: 'pattern',
            metadata: { entity: signal.entity }
        });
        
        if (!existing) {
            // New cross-agent signal — create shared knowledge entry
            const agentNames = await resolveAgentNames(signal.agents);
            await promoteToShared({
                kind: 'pattern',
                title: `Cross-agent signal: ${signal.entity}`,
                content: `${signal.entity} has appeared in work from ${agentNames.join(', ')}. This may warrant coordinated attention.`,
                qualityScore: 0.5 + (signal.agent_count * 0.1),
                metadata: { 
                    entity: signal.entity, 
                    agents: signal.agents,
                    memory_ids: signal.memory_ids
                }
            });
        } else {
            // Strengthen existing signal
            await confirmSharedKnowledge(existing.id);
        }
    }
}
```

### 5F: Recall Integration

The automatic recall system (Layer 4) now searches **both** agent-private memories AND shared knowledge:

```typescript
async function buildRecallContext(
    agentID: string,
    query: string,
    config: MemoryConfig
): Promise<string | null> {
    // Search agent's private memories
    const agentMemories = await searchMemory({
        agentID,
        query,
        minRelevance: config.recall_min_relevance,
        limit: config.recall_max_results
    });
    
    // Search shared knowledge pool (org-wide + agent's teams)
    const agentTeams = await fetchAgentTeams(agentID);
    const sharedKnowledge = await searchSharedKnowledge({
        query,
        teams: agentTeams,  // returns org-scope + entries scoped to agent's teams
        minRelevance: config.recall_min_relevance,
        limit: Math.ceil(config.recall_max_results / 2) // allocate some budget to shared
    });
    
    // Merge and rank by relevance, deduplicating
    const combined = mergeAndRank(agentMemories, sharedKnowledge);
    
    if (combined.length === 0) return null;
    
    // Format: private memories first, then shared context
    const sections = [];
    
    const privateItems = combined.filter(m => m.source === 'private');
    if (privateItems.length > 0) {
        sections.push('Your memories:\n' + formatMemories(privateItems));
    }
    
    const sharedItems = combined.filter(m => m.source === 'shared');
    if (sharedItems.length > 0) {
        sections.push('Team knowledge:\n' + formatMemories(sharedItems));
    }
    
    return trimToTokenBudget(sections.join('\n\n'), config.recall_max_tokens);
}
```

### 5G: Shared Knowledge API & CLI

**Additional API endpoints:**
```
GET    /api/knowledge                    — List shared knowledge (org-wide)
GET    /api/knowledge/search             — Semantic search shared pool
POST   /api/knowledge                    — Manually add shared knowledge
PATCH  /api/knowledge/{id}               — Update/confirm/contradict
DELETE /api/knowledge/{id}               — Remove (archive)
GET    /api/knowledge/signals            — Recent cross-agent signals
GET    /api/knowledge/corrections        — Active correction rules
POST   /api/knowledge/{id}/confirm       — Agent confirms this knowledge
POST   /api/knowledge/{id}/contradict    — Agent contradicts this knowledge

GET    /api/memory/eval/runs             — List evaluator runs + summary metrics
POST   /api/memory/eval/run              — Trigger benchmark run
GET    /api/memory/eval/runs/{id}        — Detailed metric breakdown
POST   /api/memory/eval/tune             — Run bounded tuning attempt (dry-run/apply)
```

**CLI:**
```bash
# Search shared knowledge
otter knowledge search "deployment process"

# List recent cross-agent signals
otter knowledge signals

# List active corrections (mistakes to avoid)
otter knowledge corrections

# Manually share something
otter knowledge share --title "Sam prefers small commits" \
    --content "Always use small, incremental commits. Never batch."

# Confirm or contradict
otter knowledge confirm <id>
otter knowledge contradict <id> --reason "This is outdated since Feb 7"

# Run memory evaluator
otter memory eval run
otter memory eval runs --limit 20
otter memory eval tune --apply
```

### 5H: Frontend — Shared Intelligence Dashboard

Add a top-level "Knowledge" page (already exists as concept in nav — flesh it out):

- **Shared knowledge feed**: All active shared entries, sorted by quality score
- **Cross-agent signals**: Visual showing which agents are converging on topics
- **Correction rules**: Active corrections with hit count (how many times injected)
- **Quality trends**: Chart of shared knowledge quality over time (is the system getting smarter?)
- **Promotion queue**: Entries flagged for optional human override/review (autopromotion remains primary path)
- **Agent memory health**: Per-agent stats (memories indexed, recall hit rate, extraction freshness)

## Layer 6: Autonomous Evaluation + Self-Improvement

The system should improve itself without requiring Sam to manually grade each iteration.

### 6A: Benchmark corpus

Maintain a versioned replay corpus (`internal/memory/benchmarks/*.jsonl`) with:
- representative user prompts
- expected recall targets (memory ids / shared knowledge ids)
- expected non-recall cases (should inject nothing)
- compaction recovery scenarios

This corpus is used in CI and scheduled evaluator runs.

### 6B: Objective quality metrics

Each run computes and stores:
- `recall_precision_at_k`
- `recall_recall_at_k`
- `false_injection_rate`
- `avg_injected_tokens`
- `p95_recall_latency_ms`
- `compaction_recovery_success_rate`
- `shared_promotion_precision`

### 6C: Autonomous tuning loop

A scheduled evaluator/tuner job runs daily:
1. Evaluate current config against benchmark corpus
2. Propose bounded parameter changes (e.g. `recall_min_relevance`, `recall_max_results`, promotion thresholds)
3. Re-run benchmarks with candidate config
4. Apply candidate only if all gates pass and score improves
5. Auto-rollback otherwise

### 6D: Hard quality gates (blocking)

No config change is applied unless all gates pass:
- `false_injection_rate <= 2%`
- `recall_precision_at_k >= 0.80`
- `compaction_recovery_success_rate >= 0.95`
- `p95_recall_latency_ms <= 500`
- `avg_injected_tokens <= configured budget`

### 6E: Safety constraints

Autonomous tuning is constrained:
- max one parameter-change set per day
- bounded step size per parameter
- never lowers sensitivity/scope protections
- all changes are reversible snapshots with audit trail

### 6F: Hardening requirements (security + performance + self-repair)

Security:
- strict org/agent/team scope enforcement on every read path
- sensitivity-aware filtering (`restricted` never leaves authorized scope)
- idempotency keys on ingestion/promotions to prevent duplicate side effects
- audit logs for promotion, contradiction, tuning apply/rollback

Performance:
- p95 recall path under 500ms at target load
- bounded recall token budget and truncation strategy
- benchmarked IVFFlat settings per deployment profile

Self-repair:
- embedding queue retries with capped exponential backoff
- dead-letter queue for failed extraction/promotions
- periodic consistency sweep: entries without embeddings, stale cursors, orphaned mappings
- automatic repair jobs for recoverable inconsistencies

### 6G: Autonomous iteration and escalation policy

Normal operation should not require human scoring:
- extraction runs continuously (5 min)
- evaluator runs daily on benchmark corpus
- tuner runs daily after evaluator with bounded change-set

Human escalation is only triggered when:
- 3 consecutive evaluator runs fail hard gates
- security/scope violation is detected
- p95 latency regresses by >25% for 2 consecutive runs

Escalation output includes a machine-generated incident summary, failing metrics, last good config, and recommended rollback/next action.

## API Endpoints

### Memory CRUD

```
POST   /api/agents/{id}/memory              — Create memory entry
GET    /api/agents/{id}/memory              — List memories (paginated, filterable)
GET    /api/agents/{id}/memory/search       — Semantic search
GET    /api/agents/{id}/memory/{entryId}    — Get single entry
DELETE /api/agents/{id}/memory/{entryId}    — Delete entry
POST   /api/agents/{id}/memory/batch        — Bulk create (extraction pipeline)
GET    /api/agents/{id}/memory/daily/{date} — Get daily summary
GET    /api/agents/{id}/memory/config       — Get memory config
PUT    /api/agents/{id}/memory/config       — Update memory config
GET    /api/agents/{id}/memory/compactions  — List compaction events
```

### Search Request/Response

```
GET /api/agents/{id}/memory/search?q=homepage+redesign&limit=5&min_relevance=0.7

Response:
{
  "results": [
    {
      "id": "uuid",
      "kind": "decision",
      "title": "Chose Tailwind over custom CSS for homepage",
      "content": "Sam wanted faster iteration...",
      "relevance": 0.89,
      "occurred_at": "2026-02-08T14:30:00Z",
      "source_session": "agent:main:main"
    }
  ],
  "query_embedding_ms": 45,
  "search_ms": 12,
  "total_indexed": 1847
}
```

### Recall Endpoint (for bridge)

```
POST /api/agents/{id}/memory/recall
Body: { "query": "user's message text", "max_tokens": 2000 }

Response:
{
  "context": "formatted recall text ready for injection",
  "memories_used": 3,
  "total_tokens": 847
}
```

## CLI Commands

```bash
# Write a memory (agents use this directly)
otter memory write --kind decision --title "Chose pgvector" \
    --content "Picked pgvector over FAISS because we're already on Postgres..."

# Search memories
otter memory search "what did we decide about the homepage"

# List recent memories
otter memory list --limit 20
otter memory list --kind decision --since 2026-02-01

# Get daily summary
otter memory daily 2026-02-09

# View/manage config
otter memory config
otter memory config --set recall_max_tokens=3000

# View compaction history
otter memory compactions --limit 10
```

## Frontend: Agent Memory View

Add a "Memory" tab to the agent detail page (`/agents/{id}`).

### Layout
- **Timeline view**: Memories in reverse chronological order, grouped by day
- **Kind filters**: Chips for decision/lesson/action_item/etc.
- **Search bar**: Semantic search across all memories
- **Daily summaries**: Collapsible daily rollup cards
- **Compaction log**: Shows when agent lost context and what was recovered
- **Config panel**: Toggle auto-extract, auto-recall, adjust thresholds

### Key UI Elements
- Each memory entry shows: kind badge, title, content (expandable), timestamp, source link
- Relevance score shown on search results
- "Memory health" indicator: how many memories indexed, last extraction time, embedding coverage

## Bridge Changes Summary

The bridge no longer does memory extraction (that's the Memory Agent's job). The bridge handles:

1. `detectCompaction()` — use explicit compaction metadata/events first; token-drop fallback second
2. `injectRecoveryContext()` — post-compaction memory injection via system/custom event
3. `buildRecallContext()` — semantic search OtterCamp API + format for injection
4. `fetchAgentMemories()` — call OtterCamp memory API
5. `fetchMemoryConfig()` — get per-agent memory settings

The bridge's role is **reactive** (compaction recovery, recall injection). The Memory Agent's role is **proactive** (extraction, scoping, distribution).

## Embedding Model Decision

Memory must work fully local — no external API dependency required. The embedding layer is pluggable.

### Option 1: Ollama (Local — Recommended Default)

```
Model: nomic-embed-text (768-dim) or all-minilm (384-dim)
Cost: $0
Latency: ~50ms per embedding on M1+
Dependency: Ollama running locally (already common in OpenClaw setups)
API: POST http://localhost:11434/api/embeddings
```

Ollama is the simplest path — it's already widely used in the OpenClaw ecosystem, runs on Mac/Linux, and the embedding models are tiny (~100MB). `nomic-embed-text` is the sweet spot: good quality, fast, 768 dimensions.

### Option 2: OpenAI API (Hosted)

```
Model: text-embedding-3-small (1536-dim)
Cost: $0.02/1M tokens
Latency: ~200ms per embedding (network round-trip)
Dependency: OpenAI API key
```

Better quality, but requires an API key and network. Good for hosted Tier 2-3 deployments.

### Option 3: Built-in Go Embeddings (No Dependencies)

```
Model: Embedded ONNX model in Go binary
Cost: $0
Latency: ~100ms per embedding
Dependency: None — ships with OtterCamp
```

Long-term goal: embed a small model (all-MiniLM-L6-v2, 384-dim) directly in the Go binary using ONNX Runtime. Zero external dependencies. This is the "it just works" option for self-hosted.

### Architecture: Pluggable Embedder Interface

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    Dimensions() int
    ModelName() string
}

// Implementations:
type OllamaEmbedder struct { ... }   // calls localhost:11434
type OpenAIEmbedder struct { ... }   // calls api.openai.com
type ONNXEmbedder struct { ... }     // in-process, no network

// Config in workspace settings:
// embedding_provider: "ollama" | "openai" | "builtin"
// embedding_model: "nomic-embed-text" | "text-embedding-3-small" | "all-minilm"
// ollama_url: "http://localhost:11434" (default)
```

### Vector Dimension Handling

v1 decision: **standardize on one embedding dimension per deployment**.

- Default deployment profile: `ollama + nomic-embed-text + vector(768)`
- Switching providers/models that change dimension requires reindexing all embeddings
- No per-row mixed-dimension vectors in v1 (keeps indexes/query paths simple and predictable)

Reindex command:

```bash
otter memory reindex --embedder ollama --model nomic-embed-text
```

### Decision

**Default: Ollama** (`nomic-embed-text`, 768-dim) — works locally, no API key, no cost.
**Override to OpenAI** via settings for hosted deployments that want higher quality.
**Built-in ONNX** as future enhancement for truly zero-dependency setups.

## Dependencies

- [x] Postgres (Railway for hosted, Docker for local — already in docker-compose)
- [ ] pgvector extension — use `pgvector/pgvector:pg16` Docker image (local) or enable on Railway
- [ ] Ollama with `nomic-embed-text` for local embeddings (or OpenAI API key for hosted)
- [ ] #110 Chameleon Architecture (for `whoami` identity in recovery — can ship independently)
- [ ] #105 Pipeline spec (for feedback/self-correction loops — can ship independently)

## Implementation Order

1. **Migration + Store** — database tables (`memory_entries`, `memory_entry_embeddings`, `shared_knowledge`, `shared_knowledge_embeddings`, `agent_memory_config`, `compaction_events`, `agent_teams`), Go stores with CRUD + vector search
2. **API endpoints** — REST routes for memory CRUD + search + recall + shared knowledge
3. **CLI commands** — `otter memory` + `otter knowledge` subcommands
4. **Memory Agent** — dedicated OpenClaw agent with JSONL reading, delta tracking, extraction, scoping, and distribution via CLI. Cron every 5 min.
5. **Compaction detection + recovery** — bridge uses explicit compaction signals first, heuristics second; injects recovery context
6. **Automatic recall** — bridge intercepts messages, searches private + shared, injects recall as system/custom context
7. **Shared intelligence** — promotion pipeline, quality scoring, cross-agent signals, self-correction loops
8. **Automated evaluation harness** — benchmark dataset, replay tests, regression scoring, and pass/fail gates
9. **Autonomous tuning loop** — evaluator job proposes bounded config changes; only applies improvements that pass gates
10. **Frontend** — memory tab on agent detail, shared knowledge dashboard, signals view
11. **Config UI** — per-agent memory settings + evaluator status

The system must be able to improve without human judging every iteration:
- Evaluator runs scheduled benchmark suites automatically
- Tuning applies only when objective metrics improve and safety thresholds hold
- Failed tuning attempts auto-rollback and are logged for later analysis

## Files to Create/Modify

### New Files
- `migrations/048_create_memory_system.up.sql`
- `migrations/048_create_memory_system.down.sql`
- `internal/store/memory_store.go`
- `internal/store/memory_store_test.go`
- `internal/store/shared_knowledge_store.go`
- `internal/store/shared_knowledge_store_test.go`
- `internal/api/memory.go`
- `internal/api/memory_test.go`
- `internal/api/knowledge.go`
- `internal/api/knowledge_test.go`
- `internal/memory/embedder.go` — pluggable embedding pipeline (ollama/openai/builtin)
- `internal/memory/embedder_test.go`
- `internal/memory/quality.go` — quality scoring, decay, lifecycle
- `internal/memory/quality_test.go`
- `internal/memory/evaluator.go` — benchmark runner + metric scoring
- `internal/memory/evaluator_test.go`
- `internal/memory/tuner.go` — bounded autonomous config tuning + rollback
- `internal/memory/tuner_test.go`
- `internal/memory/benchmarks/*.jsonl` — replay dataset for automated evaluation
- `web/src/pages/AgentMemoryPage.tsx` (or tab component)
- `web/src/pages/KnowledgePage.tsx` — shared intelligence dashboard
- `web/src/pages/MemoryEvalPage.tsx` — eval metrics + run history
- `web/src/components/MemoryTimeline.tsx`
- `web/src/components/MemorySearchBar.tsx`
- `web/src/components/SharedKnowledgeFeed.tsx`
- `web/src/components/CrossAgentSignals.tsx`

### Modified Files
- `internal/api/router.go` — add memory + knowledge + eval routes
- `internal/store/store.go` — register MemoryStore, SharedKnowledgeStore
- `bridge/openclaw-bridge.ts` — compaction detection, recall injection (extraction removed — Memory Agent handles that)
- `cmd/otter/main.go` — add `memory` + `knowledge` + `memory eval` subcommands
- `go.mod` — add pgvector-go dependency
- `web/src/pages/AgentDetailPage.tsx` — add Memory tab
- `web/src/App.tsx` — add Knowledge + Memory Eval routes

### OpenClaw Setup (not Codex — manual or separate spec)
- Add Memory Agent to `openclaw.json` agents list
- Create Memory Agent workspace with SOUL.md, AGENTS.md (extraction rules), state tracking
- Create 5-minute cron job for extraction cycle
- Create daily evaluator cron and daily bounded tuner cron
- Ensure Memory Agent has read access to all agent session directories

## Local / Self-Hosted Deployment

The entire memory system must run on a single machine with zero external dependencies. This is the Tier 1 reality: one Mac or Linux box running OpenClaw + OtterCamp + everything else.

### What "Runs Locally" Means

| Component | Hosted (Tier 2-3) | Local (Tier 1) |
|---|---|---|
| **Database** | Railway Postgres + pgvector | Docker Postgres + pgvector (or local install) |
| **Embeddings** | OpenAI API | Ollama (nomic-embed-text) — no API key needed |
| **Memory Agent** | Same | Same — reads JSONL from local disk |
| **Bridge** | Same | Same — connects to localhost OtterCamp |
| **OtterCamp API** | Railway | `localhost:8080` via Docker or `make dev` |
| **LLM for agent** | Anthropic API / Claude Max | Same (Memory Agent still needs an LLM) |

### Docker Compose Update

Add pgvector to the existing docker-compose setup:

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg16  # replaces standard postgres image
    environment:
      POSTGRES_DB: ottercamp
      POSTGRES_USER: ottercamp
      POSTGRES_PASSWORD: ottercamp
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
```

The `pgvector/pgvector` image is the standard Postgres image with the vector extension pre-installed. Drop-in replacement.

### Local Quick Start (Memory System)

```bash
# 1. Start Postgres with pgvector
docker-compose up -d

# 2. Install Ollama + embedding model (if not already)
brew install ollama        # or curl -fsSL https://ollama.com/install.sh | sh
ollama pull nomic-embed-text

# 3. Configure embedding provider
otter config set embedding_provider ollama
otter config set embedding_model nomic-embed-text

# 4. Run migrations (creates memory tables)
make migrate-up

# 5. Memory Agent setup (in openclaw.json)
# Add memory-agent to agents list, create extraction cron + evaluator cron + tuner cron
# (See Memory Agent Setup section)

# Done. Extraction + autonomous evaluation start on schedule.
```

### Config: `workspace_settings` Additions

```json
{
  "memory": {
    "enabled": true,
    "embedding_provider": "ollama",
    "embedding_model": "nomic-embed-text",
    "ollama_url": "http://localhost:11434",
    "openai_api_key": "",
    "auto_recall": true,
    "recall_max_tokens": 2000,
    "recall_min_relevance": 0.7,
    "extract_interval_minutes": 5,
    "eval_enabled": true,
    "eval_interval_minutes": 1440,
    "tuning_enabled": true,
    "tuning_max_step_pct": 0.1,
    "eval_gates": {
      "max_false_injection_rate": 0.02,
      "min_recall_precision_at_k": 0.80,
      "min_compaction_recovery_success_rate": 0.95,
      "max_p95_recall_latency_ms": 500
    }
  }
}
```

Stored in OtterCamp's `workspace_settings` or `settings/integrations`. The Go backend reads this to configure embedder, extraction cadence, evaluator cadence, and bounded tuning behavior.

## Resolved Decisions (from Sam)

1. **Memory Agent**: Dedicated OpenClaw agent slot (Sonnet, no channels, isolated 5-min cron). Fully spec'd above.
2. **Retention policy**: Keep all memories forever. Compact granular entries into daily summaries after 30 days.
3. **Recall injection point**: OtterCamp-routed interactions only (bridge dispatch path). Direct OpenClaw sessions (Slack/Telegram without OtterCamp) don't get recall benefits. The goal is that all interaction flows through OtterCamp — if you're not using OtterCamp, you don't get OtterCamp features.
4. **Extraction model cost**: Memory Agent runs on Claude Max — $0 marginal. For API users, Sonnet keeps extraction affordable.
5. **Entity extraction**: Memory Agent tags entities during extraction (already in the LLM call). No separate NER.
6. **Cross-agent search scope**: Agents see own memories + shared knowledge scoped to their teams + org-wide. Memory Agent cross-searches all sessions for signal detection.
7. **Shared knowledge moderation**: Memory Agent auto-promotes with scoping + quality scoring. Human review queue is optional override for high-impact entries, not a required quality gate.
8. **Team structure**: Data-driven via `agent_teams` table. Memory Agent queries OtterCamp, never hardcodes.
9. **Local-first**: Default config is fully local (Ollama embeddings, Docker Postgres + pgvector). No external API dependencies required.
10. **Autonomous quality loop**: Iterative improvements should be evaluated and gated automatically (benchmark/evaluator/tuner), not manually judged each time.

## Lessons from Beads (steveyegge/beads)

Beads is a 213K-line distributed, git-backed graph issue tracker built for AI coding agents. After reviewing it in depth, here's what's worth incorporating vs what we should skip.

### Incorporated into this spec:

**1. Tiered Memory Compaction (from Beads' "Memory Decay")**

Beads progressively compresses closed issues over time — recent = full fidelity, old = LLM-summarized, ancient = heavily compressed. Originals are recoverable from git history.

Our spec had `Compact(ctx, agentID, olderThan)` as a single-tier operation. Updated to three tiers:

- **Hot (0-7 days):** Full memory entries, no compression
- **Warm (7-30 days):** Daily summaries generated, granular entries retained but excluded from default recall (searchable on demand)
- **Cold (30+ days):** Granular entries archived, only daily/weekly summaries remain in active index. Originals preserved in Postgres (not deleted) — recoverable via explicit query

The `Compact()` method now runs tiered:
```go
func (s *MemoryStore) CompactTiered(ctx context.Context, agentID string) error {
    now := time.Now()
    // Warm tier: generate daily summaries for 7-30 day entries
    s.generateDailySummaries(ctx, agentID, now.AddDate(0,0,-30), now.AddDate(0,0,-7))
    // Cold tier: archive granular entries older than 30 days, keep summaries
    s.archiveGranularEntries(ctx, agentID, now.AddDate(0,0,-30))
    // Weekly rollups for cold tier
    s.generateWeeklySummaries(ctx, agentID, now.AddDate(0,0,-30))
    return nil
}
```

Add `status` column to `memory_entries`: `active | warm | archived` with index. Default recall queries filter to `status IN ('active', 'warm')`. Explicit deep-search can include `archived`.

**2. Ephemeral Working Memory ("Wisps" pattern)**

Beads has "wisps" — local-only execution traces that never sync, squashed into permanent "digests" when done. This maps directly to agent working memory.

Add a `working_memory` table — lightweight, per-session scratch entries that auto-expire:

```sql
CREATE TABLE working_memory (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    session_key TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

CREATE INDEX idx_working_memory_session ON working_memory(session_key);
CREATE INDEX idx_working_memory_expires ON working_memory(expires_at);
```

**How it works:**
- During a session, the Memory Agent can write ephemeral entries (intermediate findings, scratch context)
- When a session ends or compacts, the Memory Agent summarizes working memory into a permanent `summary` or `context` memory entry (the "digest")
- Working memory auto-expires after 24h via a cleanup job
- Working memory is included in compaction recovery for active sessions but excluded from cross-agent search

This prevents the noise problem — not every intermediate thought needs to be a permanent memory.

**3. Content-Hash Deduplication**

Beads uses content-based hashing to prevent collisions when multiple agents create entries concurrently. Our UUIDs prevent ID collisions, but we have no dedup for semantically identical memories created across extraction cycles.

Add a `content_hash` column to `memory_entries`:

```sql
ALTER TABLE memory_entries ADD COLUMN content_hash TEXT GENERATED ALWAYS AS (
    encode(sha256((kind || ':' || title || ':' || content)::bytea), 'hex')
) STORED;

CREATE UNIQUE INDEX idx_memory_entries_dedup 
    ON memory_entries(agent_id, content_hash) 
    WHERE status = 'active';
```

The Memory Agent's extraction pipeline checks this before writing — if a hash collision occurs, skip the write. This is cheap insurance against the extraction cron creating duplicate memories on retry or overlapping cycles.

**4. Event Emission on Memory Changes**

Beads uses `.beads/hooks/` scripts that fire on create/update/close. Our Memory Agent currently polls JSONL files but doesn't emit events when memories change.

Add a lightweight event system — when the Memory Agent writes/promotes/archives a memory, it publishes to a `memory_events` table (or PostgreSQL NOTIFY channel):

```sql
CREATE TABLE memory_events (
    id BIGSERIAL PRIMARY KEY,
    org_id UUID NOT NULL,
    event_type TEXT NOT NULL CHECK (event_type IN (
        'memory.created', 'memory.promoted', 'memory.archived',
        'knowledge.shared', 'knowledge.confirmed', 'knowledge.contradicted',
        'compaction.detected', 'compaction.recovered'
    )),
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_memory_events_type ON memory_events(event_type, created_at DESC);
-- Auto-expire after 7 days
CREATE INDEX idx_memory_events_cleanup ON memory_events(created_at);
```

**Why this matters:** Other agents or future systems can subscribe to memory changes without polling. The bridge can watch for `compaction.detected` events instead of heuristic token-drop detection. The frontend can show a live memory feed.

API endpoint: `GET /api/memory/events?since=<timestamp>&types=memory.created,knowledge.shared`

### Evaluated but NOT incorporated:

**SQLite + JSONL + Git three-layer model:** Beads uses SQLite for fast local queries, JSONL as git-tracked source of truth, and git for distribution. We're using Postgres + pgvector instead — centralized, simpler for our architecture (single OtterCamp instance, not distributed). Adding SQLite as a local cache would be premature optimization. If we need local-first later, we can add it then.

**3-Way Merge for distributed state:** Beads does field-specific merge strategies (LWW for scalars, union for labels, append for comments). Our architecture has a single writer per memory (the Memory Agent extracts, agents don't write concurrently to the same entry). Shared knowledge has confirm/contradict semantics instead of merge. If we move to multi-writer memory, revisit this.

**Dependency graphs for work execution:** Beads' `bd ready` (what's unblocked) is about issue tracking, not memory. Belongs in Otter Camp's issue system, not the memory spec.

**Molecules (workflow templates):** Interesting for Otter Camp's project management, but not a memory concern. Could be a separate issue.

### Summary of spec changes from Beads review:

| Change | Section Affected | Impact |
|---|---|---|
| Tiered compaction (hot/warm/cold) | Layer 1: Storage, Layer 2: Memory Agent | Memory entries now have lifecycle tiers, not binary active/archived |
| Working memory table | Layer 1: Storage (new table) | Ephemeral scratch entries with auto-expire and session-end digesting |
| Content-hash dedup | Layer 1: Storage, Layer 2: Extraction | Prevents duplicate memories across extraction cycles |
| Event emission | Layer 1: Storage (new table), API | Reactive integration point — replaces some polling patterns |
| `status` column expanded | Layer 1: Schema | `active → warm → archived` instead of binary |

### New files from Beads additions:
- `migrations/048_create_memory_system.up.sql` — updated with `working_memory`, `memory_events`, `content_hash`, expanded `status` enum
- `internal/store/working_memory_store.go` — ephemeral scratch CRUD
- `internal/store/working_memory_store_test.go`
- `internal/store/memory_events_store.go` — event publish + subscribe
- `internal/store/memory_events_store_test.go`
- `internal/api/memory_events.go` — SSE/polling endpoint for memory events

### Updated implementation order (beads additions in bold):

1. Migration + Store — **now includes working_memory, memory_events tables, content_hash, tiered status**
2. API endpoints — **now includes /memory/events endpoint**
3. CLI commands
4. Memory Agent — **extraction now deduplicates via content_hash, emits events on write**
5. Compaction detection + recovery — **can now listen to memory_events instead of heuristic-only**
6. Automatic recall — **tiered: hot+warm by default, cold on explicit deep-search**
7. Shared intelligence
8. Automated evaluation harness
9. Autonomous tuning loop — **working memory cleanup job added to daily maintenance**
10. Frontend — **memory events feed added to dashboard**
11. Config UI

## Open Questions (validation backlog)

1. **Railway pgvector**: Confirm Railway Postgres supports `CREATE EXTENSION vector;`. Fallback: cosine similarity via raw float arrays. For local, `pgvector/pgvector:pg16` Docker image handles it.
2. **Token budget default**: Validate whether 2000 tokens is the right default under real traffic and context pressure.
3. **IVFFlat index tuning**: Validate `lists = 100` at expected scale; tune based on observed recall latency/accuracy.
4. **Embedding dimension migration**: Confirm reindex UX and runtime impact when switching providers (Ollama → OpenAI).
5. **Autonomous tuning guardrails**: Validate bounded-step policy to ensure optimization does not overfit benchmark corpus.

## Execution Log
- [2026-02-10 08:51 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 111 from 01-ready to 02-in-progress and resumed execution on branch codex/spec-111-memory-infrastructure-overhaul | Tests: n/a
- [2026-02-10 08:55 MST] Issue #607 | Commit n/a | created | Core memory infrastructure migration issue opened with explicit migration test commands | Tests: make migrate-up; make migrate-status; make migrate-down; make migrate-up
- [2026-02-10 08:55 MST] Issue #608 | Commit n/a | created | Memory entry store issue opened (CRUD/search/recall context) with explicit Go tests | Tests: go test ./internal/store -run TestMemoryStore -count=1; go test ./internal/store -run TestMemoryStoreOrgIsolation -count=1
- [2026-02-10 08:55 MST] Issue #609 | Commit n/a | created | Working-memory and memory-events store issue opened with expiry/filter test coverage | Tests: go test ./internal/store -run TestWorkingMemoryStore -count=1; go test ./internal/store -run TestMemoryEventsStore -count=1
- [2026-02-10 08:55 MST] Issue #610 | Commit n/a | created | Shared knowledge store issue opened with quality/signal update tests | Tests: go test ./internal/store -run TestSharedKnowledgeStore -count=1; go test ./internal/store -run TestSharedKnowledgeStoreScopeFiltering -count=1
- [2026-02-10 08:55 MST] Issue #611 | Commit n/a | created | Embedding pipeline/chunking issue opened with explicit embedder tests | Tests: go test ./internal/memory -run TestEmbedder -count=1; go test ./internal/memory -run TestChunkTextForEmbedding -count=1
- [2026-02-10 08:55 MST] Issue #612 | Commit n/a | created | Memory API endpoints issue opened with handler and org isolation tests | Tests: go test ./internal/api -run TestMemoryHandler -count=1; go test ./internal/api -run TestMemoryHandlerOrgIsolation -count=1
- [2026-02-10 08:55 MST] Issue #613 | Commit n/a | created | Memory events API issue opened with since/types filtering tests | Tests: go test ./internal/api -run TestMemoryEventsHandler -count=1
- [2026-02-10 08:55 MST] Issue #614 | Commit n/a | created | CLI memory/knowledge issue opened with command handler tests | Tests: go test ./cmd/otter -run TestHandleMemory -count=1; go test ./cmd/otter -run TestHandleKnowledge -count=1
- [2026-02-10 08:55 MST] Issue #615 | Commit n/a | created | Bridge compaction detection/recovery issue opened with bridge test command | Tests: cd bridge && npm test -- --runInBand openclaw-bridge.compaction.test.ts
- [2026-02-10 08:55 MST] Issue #616 | Commit n/a | created | Bridge recall injection issue opened with bridge recall test command | Tests: cd bridge && npm test -- --runInBand openclaw-bridge.recall.test.ts
- [2026-02-10 08:55 MST] Issue #617 | Commit n/a | created | Evaluator harness issue opened with explicit evaluator test command | Tests: go test ./internal/memory -run TestEvaluator -count=1
- [2026-02-10 08:55 MST] Issue #619 | Commit n/a | created | Autonomous tuner issue opened with rollback test command | Tests: go test ./internal/memory -run TestTuner -count=1
- [2026-02-10 08:55 MST] Issue #620 | Commit n/a | created | Agent memory frontend issue opened with React test commands | Tests: cd web && npm test -- AgentDetailPage.test.tsx AgentMemoryBrowser.test.tsx
- [2026-02-10 08:55 MST] Issue #621 | Commit n/a | created | Shared knowledge/eval frontend dashboards issue opened with route/page tests | Tests: cd web && npm test -- KnowledgePage.test.tsx App.test.tsx
- [2026-02-10 08:55 MST] Issue #618 | Commit n/a | closed | Duplicate autonomous tuner issue closed as superseded by #619 | Tests: n/a
- [2026-02-10 08:59 MST] Issue #607 | Commit afe48ab | committed | Added migration 058 memory infrastructure schema plus schema tests for migration file contract and lifecycle columns/index | Tests: go test ./internal/store -run TestMigration058MemoryInfrastructureFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run 'TestMigration058MemoryInfrastructureFilesExistAndContainCoreDDL|TestSchemaMemoryInfrastructureTablesCreateAndRollback|TestSchemaMemoryEntriesLifecycleColumnsAndDedupIndex' -count=1; go test ./internal/store -count=1
- [2026-02-10 08:59 MST] Issue #607 | Commit afe48ab | pushed | Pushed branch codex/spec-111-memory-infrastructure-overhaul to origin with migration 058 changes | Tests: n/a
- [2026-02-10 08:59 MST] Issue #607 | Commit afe48ab | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/store -run TestMigration058MemoryInfrastructureFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run 'TestMigration058MemoryInfrastructureFilesExistAndContainCoreDDL|TestSchemaMemoryInfrastructureTablesCreateAndRollback|TestSchemaMemoryEntriesLifecycleColumnsAndDedupIndex' -count=1; go test ./internal/store -count=1
- [2026-02-10 09:03 MST] Issue #608 | Commit 91732d1 | committed | Added internal/store MemoryStore with scoped CRUD/search/delete plus recall context builder and validation | Tests: go test ./internal/store -run TestMemoryStore -count=1; go test ./internal/store -run TestMemoryStoreOrgIsolation -count=1; go test ./internal/store -count=1
- [2026-02-10 09:03 MST] Issue #608 | Commit 91732d1 | pushed | Pushed MemoryStore implementation and tests to branch codex/spec-111-memory-infrastructure-overhaul | Tests: n/a
- [2026-02-10 09:03 MST] Issue #608 | Commit 91732d1 | closed | Closed GitHub issue with commit and passing test evidence | Tests: go test ./internal/store -run TestMemoryStore -count=1; go test ./internal/store -run TestMemoryStoreOrgIsolation -count=1; go test ./internal/store -count=1
- [2026-02-10 09:05 MST] Issue #609 | Commit 8f8886b | committed | Added WorkingMemoryStore and MemoryEventsStore with workspace scoping, expiry cleanup, type/time filtered listing, and tests | Tests: go test ./internal/store -run TestWorkingMemoryStore -count=1; go test ./internal/store -run TestMemoryEventsStore -count=1; go test ./internal/store -count=1
- [2026-02-10 09:05 MST] Issue #609 | Commit 8f8886b | pushed | Pushed working memory and memory events store slice to origin branch | Tests: n/a
- [2026-02-10 09:05 MST] Issue #609 | Commit 8f8886b | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/store -run TestWorkingMemoryStore -count=1; go test ./internal/store -run TestMemoryEventsStore -count=1; go test ./internal/store -count=1
- [2026-02-10 09:07 MST] Issue #610 | Commit 25352d2 | committed | Added SharedKnowledgeStore with create/list/search and confirm/contradict quality updates plus scope filtering tests | Tests: go test ./internal/store -run TestSharedKnowledgeStore -count=1; go test ./internal/store -run TestSharedKnowledgeStoreScopeFiltering -count=1; go test ./internal/store -count=1
- [2026-02-10 09:07 MST] Issue #610 | Commit 25352d2 | pushed | Pushed shared knowledge store implementation slice to origin branch | Tests: n/a
- [2026-02-10 09:07 MST] Issue #610 | Commit 25352d2 | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/store -run TestSharedKnowledgeStore -count=1; go test ./internal/store -run TestSharedKnowledgeStoreScopeFiltering -count=1; go test ./internal/store -count=1
- [2026-02-10 09:09 MST] Issue #611 | Commit c706a43 | committed | Added internal/memory embedder abstraction with Ollama/OpenAI adapters and deterministic chunking helper + tests | Tests: go test ./internal/memory -run TestEmbedder -count=1; go test ./internal/memory -run TestChunkTextForEmbedding -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:09 MST] Issue #611 | Commit c706a43 | pushed | Pushed embedder pipeline slice to origin branch | Tests: n/a
- [2026-02-10 09:09 MST] Issue #611 | Commit c706a43 | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/memory -run TestEmbedder -count=1; go test ./internal/memory -run TestChunkTextForEmbedding -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:13 MST] Issue #612 | Commit 56b4873 | committed | Added structured memory API handlers/routes (create/list/search/recall/delete) with validation and workspace-aware error mapping + tests | Tests: go test ./internal/api -run TestMemoryHandler -count=1; go test ./internal/api -run TestMemoryHandlerOrgIsolation -count=1; go test ./internal/api -count=1
- [2026-02-10 09:13 MST] Issue #612 | Commit 56b4873 | pushed | Pushed memory API endpoint slice to origin branch | Tests: n/a
- [2026-02-10 09:13 MST] Issue #612 | Commit 56b4873 | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/api -run TestMemoryHandler -count=1; go test ./internal/api -run TestMemoryHandlerOrgIsolation -count=1; go test ./internal/api -count=1
- [2026-02-10 09:14 MST] Issue #613 | Commit 64fac2f | committed | Added /api/memory/events handler and route with since/types filtering, validation, and workspace-aware error mapping + tests | Tests: go test ./internal/api -run TestMemoryEventsHandler -count=1; go test ./internal/api -count=1
- [2026-02-10 09:14 MST] Issue #613 | Commit 64fac2f | pushed | Pushed memory events endpoint slice to origin branch | Tests: n/a
- [2026-02-10 09:14 MST] Issue #613 | Commit 64fac2f | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./internal/api -run TestMemoryEventsHandler -count=1; go test ./internal/api -count=1
- [2026-02-10 09:19 MST] Issue #614 | Commit b11550b | committed | Extended otter CLI with structured memory commands + new knowledge command; added client API helpers and handler/client tests | Tests: go test ./cmd/otter -run TestHandleMemory -count=1; go test ./cmd/otter -run TestHandleKnowledge -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 09:19 MST] Issue #614 | Commit b11550b | pushed | Pushed CLI and ottercli client updates to origin branch | Tests: n/a
- [2026-02-10 09:19 MST] Issue #614 | Commit b11550b | closed | Closed GitHub issue with commit hash and test evidence | Tests: go test ./cmd/otter -run TestHandleMemory -count=1; go test ./cmd/otter -run TestHandleKnowledge -count=1; go test ./cmd/otter -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 09:24 MST] Issue #615 | Commit e4fb11e | committed | Added bridge compaction detection (explicit then heuristic), fail-closed recovery injection flow, retry/backoff, and dedupe guards; added compaction-focused bridge tests | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; npm run test:bridge
- [2026-02-10 09:24 MST] Issue #615 | Commit e4fb11e | pushed | Pushed bridge compaction recovery slice to origin branch | Tests: n/a
- [2026-02-10 09:24 MST] Issue #615 | Commit e4fb11e | closed | Closed GitHub issue with commit hash and test evidence | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; npm run test:bridge
- [2026-02-10 09:28 MST] Issue #616 | Commit d4d0bcd | committed | Added dispatch-time auto recall injection with bounded quality-gate params, fail-closed behavior, and dedicated recall tests | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.recall.test.ts; npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; npm run test:bridge
- [2026-02-10 09:28 MST] Issue #616 | Commit d4d0bcd | pushed | Pushed bridge auto recall injection slice to origin branch | Tests: n/a
- [2026-02-10 09:28 MST] Issue #616 | Commit d4d0bcd | closed | Closed GitHub issue with commit hash and passing bridge test evidence | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.recall.test.ts; npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; npm run test:bridge
- [2026-02-10 09:31 MST] Issue #617 | Commit ba1069c | committed | Added evaluator harness with JSONL benchmark loader, core metric computation, gate evaluation, and fixture-backed tests | Tests: go test ./internal/memory -run TestEvaluator -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:31 MST] Issue #617 | Commit ba1069c | pushed | Pushed evaluator harness and benchmark fixture slice to origin branch | Tests: n/a
- [2026-02-10 09:31 MST] Issue #617 | Commit ba1069c | closed | Closed GitHub issue with commit hash and evaluator test evidence | Tests: go test ./internal/memory -run TestEvaluator -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:34 MST] Issue #619 | Commit 98bbd3a | committed | Added bounded autonomous tuner with evaluator comparison, apply/rollback decisioning, and JSONL audit sink plus branch tests | Tests: go test ./internal/memory -run TestTuner -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:34 MST] Issue #619 | Commit 98bbd3a | pushed | Pushed tuner module and tests slice to origin branch | Tests: n/a
- [2026-02-10 09:34 MST] Issue #619 | Commit 98bbd3a | closed | Closed GitHub issue with commit hash and tuner test evidence | Tests: go test ./internal/memory -run TestTuner -count=1; go test ./internal/memory -count=1
- [2026-02-10 09:37 MST] Issue #620 | Commit 81d98c2 | committed | Reworked Agent Memory UI to structured timeline/search/recall APIs with non-blocking error handling and updated page/component tests | Tests: cd web && npm test -- AgentDetailPage.test.tsx AgentMemoryBrowser.test.tsx --run
- [2026-02-10 09:37 MST] Issue #620 | Commit 81d98c2 | pushed | Pushed agent memory frontend slice to origin branch | Tests: n/a
- [2026-02-10 09:37 MST] Issue #620 | Commit 81d98c2 | closed | Closed GitHub issue with commit hash and frontend test evidence | Tests: cd web && npm test -- AgentDetailPage.test.tsx AgentMemoryBrowser.test.tsx --run
- [2026-02-10 09:40 MST] Issue #621 | Commit 932373f | committed | Added shared-knowledge evaluation summary states plus dedicated memory-evaluation dashboard route/page and app/router/knowledge tests | Tests: cd web && npm test -- KnowledgePage.test.tsx App.test.tsx --run; cd web && npm test -- router.test.tsx --run
- [2026-02-10 09:40 MST] Issue #621 | Commit 932373f | pushed | Pushed knowledge/evaluation dashboard frontend slice to origin branch | Tests: n/a
- [2026-02-10 09:40 MST] Issue #621 | Commit 932373f | closed | Closed GitHub issue with commit hash and frontend route/state test evidence | Tests: cd web && npm test -- KnowledgePage.test.tsx App.test.tsx --run; cd web && npm test -- router.test.tsx --run
- [2026-02-10 09:41 MST] Issue #622 | Commit n/a | closed | Closed duplicate issue as superseded by #620 to reconcile GitHub/local spec state | Tests: n/a
- [2026-02-10 09:41 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Implementation complete for all planned spec-111 micro-issues; moved spec from 02-in-progress to 03-needs-review for external validation | Tests: see per-issue log entries
- [2026-02-10 10:20 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Prioritized reviewer-required follow-up: moved spec 111 from 01-ready to 02-in-progress and created branch codex/spec-111-memory-infrastructure-overhaul-r2 from origin/main | Tests: n/a
- [2026-02-10 10:20 MST] Issue #638 | Commit n/a | created | Added follow-up micro-issue for reviewer-required CLI recall quality flags (`--min-relevance`, `--max-chars`) with explicit command-level tests | Tests: go test ./cmd/otter -run TestHandleMemoryRecallFlags -count=1
- [2026-02-10 10:20 MST] Issue #639 | Commit n/a | created | Added follow-up micro-issue for reviewer-required org-scoped `memory_events` index update in migration 058 with regression test plan | Tests: go test ./internal/store -count=1
- [2026-02-10 10:20 MST] Issue #633,#634,#635,#636,#637,#638,#639 | Commit n/a | planned-set-verified | Verified full reviewer-required change set for spec-111 is represented by open micro-issues before coding resumed | Tests: n/a
- [2026-02-10 10:25 MST] Issue #633 | Commit c849c90 | committed | Added pgcrypto extension declaration to migration 058 and schema contract assertion for digest dependency | Tests: go test ./internal/store -run TestMigration058 -count=1
- [2026-02-10 10:25 MST] Issue #633 | Commit c849c90 | pushed | Pushed reviewer-fix migration slice to branch codex/spec-111-memory-infrastructure-overhaul-r2 | Tests: n/a
- [2026-02-10 10:25 MST] Issue #633 | Commit c849c90 | closed | Closed GitHub issue with commit hash and targeted migration test evidence | Tests: go test ./internal/store -run TestMigration058 -count=1
- [2026-02-10 10:26 MST] Issue #639 | Commit 97b9fcd | committed | Updated migration 058 memory_events type index to (org_id, event_type, created_at DESC) and added contract assertion | Tests: go test ./internal/store -count=1
- [2026-02-10 10:26 MST] Issue #639 | Commit 97b9fcd | pushed | Pushed org-scoped memory_events index fix to codex/spec-111-memory-infrastructure-overhaul-r2 | Tests: n/a
- [2026-02-10 10:26 MST] Issue #639 | Commit 97b9fcd | closed | Closed GitHub issue with commit hash and full store test evidence | Tests: go test ./internal/store -count=1
- [2026-02-10 10:29 MST] Issue #634 | Commit a8a1d96 | committed | Added tuner 24h rate limit, hard relevance floor, guarded sensitivity/scope fields, and one-parameter bidirectional mutation with tests | Tests: go test ./internal/memory -run TestTunerRateLimit -count=1; go test ./internal/memory -run TestTunerNeverLowersSensitivity -count=1; go test ./internal/memory -run TestTunerBidirectional -count=1; go test ./internal/memory -run TestTuner -count=1
- [2026-02-10 10:29 MST] Issue #634 | Commit a8a1d96 | pushed | Pushed tuner safety/rate-limit reviewer fix slice to origin branch | Tests: n/a
- [2026-02-10 10:29 MST] Issue #634 | Commit a8a1d96 | closed | Closed GitHub issue with commit hash and targeted tuner test evidence | Tests: go test ./internal/memory -run TestTunerRateLimit -count=1; go test ./internal/memory -run TestTunerNeverLowersSensitivity -count=1; go test ./internal/memory -run TestTunerBidirectional -count=1; go test ./internal/memory -run TestTuner -count=1
- [2026-02-10 10:32 MST] Issue #635 | Commit dab4c4e | committed | Made embedder chunking UTF-8 rune-safe, added timeout/retry/backoff support, and fixed OpenAI trim normalization with targeted tests | Tests: go test ./internal/memory -run TestChunkUTF8 -count=1; go test ./internal/memory -run TestEmbedderTimeout -count=1; go test ./internal/memory -run TestEmbedderTrimming -count=1; go test ./internal/memory -run TestEmbedder -count=1
- [2026-02-10 10:32 MST] Issue #635 | Commit dab4c4e | pushed | Pushed embedder reliability and UTF-8 safety reviewer-fix slice to origin | Tests: n/a
- [2026-02-10 10:32 MST] Issue #635 | Commit dab4c4e | closed | Closed GitHub issue with commit hash and targeted embedder test evidence | Tests: go test ./internal/memory -run TestChunkUTF8 -count=1; go test ./internal/memory -run TestEmbedderTimeout -count=1; go test ./internal/memory -run TestEmbedderTrimming -count=1; go test ./internal/memory -run TestEmbedder -count=1
- [2026-02-10 10:35 MST] Issue #636 | Commit 37ab5d6 | committed | Expanded evaluator metrics (avg_injected_tokens/shared_promotion_precision), aligned metric names, fixed empty recovery rate, and expanded fixture to 22 cases | Tests: go test ./internal/memory -run TestEvaluatorAllMetrics -count=1; go test ./internal/memory -run TestEvaluatorEmptyRecovery -count=1; go test ./internal/memory -run TestEvaluator -count=1
- [2026-02-10 10:35 MST] Issue #636 | Commit 37ab5d6 | pushed | Pushed evaluator metrics/fixture reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 10:35 MST] Issue #636 | Commit 37ab5d6 | closed | Closed GitHub issue with commit hash and evaluator test evidence | Tests: go test ./internal/memory -run TestEvaluatorAllMetrics -count=1; go test ./internal/memory -run TestEvaluatorEmptyRecovery -count=1; go test ./internal/memory -run TestEvaluator -count=1
- [2026-02-10 10:38 MST] Issue #637 | Commit d074dd1 | committed | Added compaction recall quality-gate params/truncation in bridge plus CLI write/read and ottercli error-path test coverage | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; go test ./cmd/otter -run TestHandleMemoryWriteRead -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:38 MST] Issue #637 | Commit d074dd1 | pushed | Pushed bridge quality-gate and memory coverage reviewer-fix slice to origin | Tests: n/a
- [2026-02-10 10:38 MST] Issue #637 | Commit d074dd1 | closed | Closed GitHub issue with commit hash and bridge/CLI/client test evidence | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts; go test ./cmd/otter -run TestHandleMemoryWriteRead -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 10:40 MST] Issue #638 | Commit c280efb | committed | Added memory recall quality flags (--min-relevance, --max-chars), validation, and client query wiring with tests | Tests: go test ./cmd/otter -run TestHandleMemoryRecallFlags -count=1; go test ./internal/ottercli -run TestStructuredMemoryAndKnowledgeClientMethodsUseExpectedPaths -count=1
- [2026-02-10 10:40 MST] Issue #638 | Commit c280efb | pushed | Pushed CLI recall quality flag reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 10:40 MST] Issue #638 | Commit c280efb | closed | Closed GitHub issue with commit hash and CLI/client wiring test evidence | Tests: go test ./cmd/otter -run TestHandleMemoryRecallFlags -count=1; go test ./internal/ottercli -run TestStructuredMemoryAndKnowledgeClientMethodsUseExpectedPaths -count=1
- [2026-02-10 10:42 MST] Issue #633,#634,#635,#636,#637,#638,#639 | Commit c849c90,a8a1d96,dab4c4e,37ab5d6,d074dd1,c280efb,97b9fcd | resolved | Completed all reviewer-required follow-up micro-issues for spec-111 and removed top-level reviewer block per execution contract | Tests: see per-issue entries
- [2026-02-10 10:42 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Reviewer-required implementation complete; moved spec 111 from 02-in-progress to 03-needs-review for external validation | Tests: see per-issue entries
- [2026-02-10 11:14 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Prioritized new reviewer-required follow-up: moved spec 111 from 01-ready to 02-in-progress | Tests: n/a
- [2026-02-10 11:14 MST] Issue #n/a | Commit n/a | branch-created | Created branch codex/spec-111-memory-infrastructure-overhaul-r3 from codex/spec-111-memory-infrastructure-overhaul-r2 for reviewer-fix cycle | Tests: n/a
- [2026-02-10 11:14 MST] Issue #650,#651,#652,#653,#654,#655,#656,#657,#658 | Commit n/a | planned-set-verified | Verified all current reviewer-required items are represented by open micro-issues before coding resumed | Tests: n/a
- [2026-02-10 11:28 MST] Issue #650 | Commit 3c3e639 | committed | Corrected evaluator Precision@K denominator to use configured k and added variable-length retrieved_ids regression case with exact metric assertion | Tests: go test ./internal/memory -run TestEvaluatorPrecisionAtKUsesConfiguredKDenominator -count=1; go test ./internal/memory -run TestEvaluatorAllMetrics -count=1; go test ./internal/memory -run TestEvaluator -count=1
- [2026-02-10 11:28 MST] Issue #650 | Commit 3c3e639 | pushed | Pushed evaluator precision denominator reviewer-fix slice to origin branch codex/spec-111-memory-infrastructure-overhaul-r3 | Tests: n/a
- [2026-02-10 11:28 MST] Issue #650 | Commit 3c3e639 | closed | Closed GitHub issue with commit hash and evaluator precision test evidence | Tests: go test ./internal/memory -run TestEvaluatorPrecisionAtKUsesConfiguredKDenominator -count=1; go test ./internal/memory -run TestEvaluatorAllMetrics -count=1; go test ./internal/memory -run TestEvaluator -count=1
- [2026-02-10 11:28 MST] Issue #651 | Commit 601d5da | committed | Updated tuner status handling for Apply failure without rollback callback and added tests for nil rollback and apply+rollback double-fault paths | Tests: go test ./internal/memory -run TestTuner -count=1
- [2026-02-10 11:28 MST] Issue #651 | Commit 601d5da | pushed | Pushed tuner status semantics reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #651 | Commit 601d5da | closed | Closed GitHub issue with commit hash and tuner regression test evidence | Tests: go test ./internal/memory -run TestTuner -count=1
- [2026-02-10 11:28 MST] Issue #652 | Commit 23aa17e | committed | Introduced provider-summary guard for heuristic compaction detection and added no-summary large-drop regression coverage | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts
- [2026-02-10 11:28 MST] Issue #652 | Commit 23aa17e | pushed | Pushed bridge heuristic summary-gate reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #652 | Commit 23aa17e | closed | Closed GitHub issue with commit hash and bridge compaction test evidence | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts
- [2026-02-10 11:28 MST] Issue #653 | Commit 51cec02 | committed | Added MAX_TRACKED_COMPACTION_RECOVERY_KEYS cap and FIFO eviction for compaction dedupe map with MAX+1 eviction test | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts
- [2026-02-10 11:28 MST] Issue #653 | Commit 51cec02 | pushed | Pushed bridge compaction dedupe-map cap reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #653 | Commit 51cec02 | closed | Closed GitHub issue with commit hash and compaction map-cap test evidence | Tests: npx tsx --test bridge/__tests__/openclaw-bridge.compaction.test.ts
- [2026-02-10 11:28 MST] Issue #654 | Commit 64f8dca | committed | Filtered expired rows from WorkingMemoryStore ListBySession and updated cleanup test expectations to assert pre-cleanup filtering | Tests: go test ./internal/store -run TestWorkingMemoryStoreCreateListCleanup -count=1; go test ./internal/store -run TestWorkingMemoryStoreOrgIsolation -count=1; go test ./internal/store -count=1
- [2026-02-10 11:28 MST] Issue #654 | Commit 64f8dca | pushed | Pushed working-memory expiry-filter reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #654 | Commit 64f8dca | closed | Closed GitHub issue with commit hash and working-memory test evidence | Tests: go test ./internal/store -run TestWorkingMemoryStoreCreateListCleanup -count=1; go test ./internal/store -run TestWorkingMemoryStoreOrgIsolation -count=1; go test ./internal/store -count=1
- [2026-02-10 11:28 MST] Issue #655 | Commit 2dbf24c | committed | Implemented UTF-8-safe recall truncation helper for GetRecallContext and added split-rune regression test | Tests: go test ./internal/store -run TestGetRecallContextTruncatesUTF8Safely -count=1; go test ./internal/store -run TestMemoryStoreCreateListSearchRecallDelete -count=1; go test ./internal/store -count=1
- [2026-02-10 11:28 MST] Issue #655 | Commit 2dbf24c | pushed | Pushed recall UTF-8 truncation reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #655 | Commit 2dbf24c | closed | Closed GitHub issue with commit hash and UTF-8 truncation test evidence | Tests: go test ./internal/store -run TestGetRecallContextTruncatesUTF8Safely -count=1; go test ./internal/store -run TestMemoryStoreCreateListSearchRecallDelete -count=1; go test ./internal/store -count=1
- [2026-02-10 11:28 MST] Issue #656 | Commit bec3342 | committed | Applied 1MiB success-path embedder response limits and clamped retry attempts/backoff with normalization tests | Tests: go test ./internal/memory -run TestNormalizeRetryAttemptsCapsUpperBound -count=1; go test ./internal/memory -run TestEmbedder -count=1; go test ./internal/memory -run TestEmbedderTimeout -count=1; go test ./internal/memory -count=1
- [2026-02-10 11:28 MST] Issue #656 | Commit bec3342 | pushed | Pushed embedder limit/clamp reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #656 | Commit bec3342 | closed | Closed GitHub issue with commit hash and embedder regression test evidence | Tests: go test ./internal/memory -run TestNormalizeRetryAttemptsCapsUpperBound -count=1; go test ./internal/memory -run TestEmbedder -count=1; go test ./internal/memory -run TestEmbedderTimeout -count=1; go test ./internal/memory -count=1
- [2026-02-10 11:28 MST] Issue #657 | Commit 8380986 | committed | Capped memory-events handler limit to 1000, bounded ottercli error-body reads, and added oversized-limit handler test | Tests: go test ./internal/api -run TestMemoryEventsHandlerCapsLimit -count=1; go test ./internal/api -run TestMemoryEventsHandler -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 11:28 MST] Issue #657 | Commit 8380986 | pushed | Pushed memory-events/client error-body reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #657 | Commit 8380986 | closed | Closed GitHub issue with commit hash and API/client test evidence | Tests: go test ./internal/api -run TestMemoryEventsHandlerCapsLimit -count=1; go test ./internal/api -run TestMemoryEventsHandler -count=1; go test ./internal/ottercli -count=1
- [2026-02-10 11:28 MST] Issue #658 | Commit e5619d5 | committed | Guarded KnowledgePage against null tags and added shared knowledge org-isolation store test plus frontend regression coverage | Tests: go test ./internal/store -run TestSharedKnowledgeStoreOrgIsolation -count=1; go test ./internal/store -run TestSharedKnowledgeStoreScopeFiltering -count=1; go test ./internal/store -count=1; cd web && npm test -- KnowledgePage.test.tsx --run
- [2026-02-10 11:28 MST] Issue #658 | Commit e5619d5 | pushed | Pushed web/store null-tags and org-isolation reviewer-fix slice to origin branch | Tests: n/a
- [2026-02-10 11:28 MST] Issue #658 | Commit e5619d5 | closed | Closed GitHub issue with commit hash and frontend/store regression test evidence | Tests: go test ./internal/store -run TestSharedKnowledgeStoreOrgIsolation -count=1; go test ./internal/store -run TestSharedKnowledgeStoreScopeFiltering -count=1; go test ./internal/store -count=1; cd web && npm test -- KnowledgePage.test.tsx --run
- [2026-02-10 11:28 MST] Issue #650,#651,#652,#653,#654,#655,#656,#657,#658 | Commit 3c3e639,601d5da,23aa17e,51cec02,64f8dca,2dbf24c,bec3342,8380986,e5619d5 | resolved | Completed all current reviewer-required micro-issues for spec-111 and removed top-level reviewer block per execution contract | Tests: see per-issue entries
- [2026-02-10 11:28 MST] Issue #n/a | Commit n/a | reviewer-block-removed | Removed fully resolved top-level Reviewer Required Changes block; closure evidence retained in execution log entries above | Tests: n/a
- [2026-02-10 11:28 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Reviewer-required implementation complete; moved spec 111 from 02-in-progress to 03-needs-review for external validation | Tests: see per-issue entries
- [2026-02-10 11:40 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Moved spec 111 from 01-ready to 02-in-progress for newly posted reviewer-required follow-up | Tests: n/a
- [2026-02-10 11:41 MST] Issue #n/a | Commit n/a | branch-created | Created branch codex/spec-111-memory-infrastructure-overhaul-r4 from codex/spec-111-memory-infrastructure-overhaul-r3 for isolated reviewer cycle | Tests: n/a
- [2026-02-10 11:42 MST] Issue #679,#680,#681,#682,#683,#684,#685,#686,#687,#688,#689,#690 | Commit n/a | created | Created duplicate reviewer micro-issue set before discovering existing open set #667-#678 already covered all required items | Tests: n/a
- [2026-02-10 11:42 MST] Issue #679,#680,#681,#682,#683,#684,#685,#686,#687,#688,#689,#690 | Commit n/a | closed | Closed duplicate reviewer issues as superseded by existing open plan #667-#678 to maintain a single implementation queue | Tests: n/a
- [2026-02-10 11:43 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit n/a | planned-set-verified | Verified all current reviewer-required changes are represented by explicit open micro-issues before implementation resumed | Tests: gh issue list --state open --search "spec-111:" --json number,title,state --limit 30
- [2026-02-10 11:43 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit n/a | planned-set-verified | Reconciled reviewer issue queue and verified one complete open micro-issue set (#667-#678) before coding; closed duplicate planning issues #679-#690 as superseded | Tests: gh issue list --state open --search "spec-111:" --json number,title,state --limit 30
- [2026-02-10 11:46 MST] Issue #667 | Commit f3a138d | committed | Added 1MiB MaxBytesReader to knowledge import and oversize-body regression test | Tests: go test ./internal/api -run TestKnowledgeImportRejectsBodyOverLimit -count=1; go test ./internal/api -run TestKnowledgeHandlerImportReplacesEntries -count=1
- [2026-02-10 11:47 MST] Issue #668 | Commit a99cbd8 | committed | Sanitized unknown knowledge store errors to generic 500 response and added non-leak regression test | Tests: go test ./internal/api -run TestKnowledgeImportUnknownStoreErrorReturnsGeneric500 -count=1; go test ./internal/api -run TestKnowledgeHandlerImportReplacesEntries -count=1
- [2026-02-10 11:48 MST] Issue #669 | Commit e44767f | committed | Switched memory/knowledge routes to RequireWorkspace and added middleware-level 401 regression tests | Tests: go test ./internal/api -run TestMemoryRoutesRequireWorkspace -count=1; go test ./internal/api -run TestKnowledgeRoutesRequireWorkspace -count=1; go test ./internal/api -run TestMemoryHandler -count=1
- [2026-02-10 11:49 MST] Issue #670 | Commit 7fda9c6 | committed | Added success-path response size cap in ottercli JSON decode path with oversize regression coverage | Tests: go test ./internal/ottercli -run TestDoJSONRequestCapsSuccessResponseBody -count=1; go test ./internal/ottercli -run TestStructuredMemoryAndKnowledgeClientMethodsUseExpectedPaths -count=1
- [2026-02-10 11:50 MST] Issue #671 | Commit d330000 | committed | Rejected empty knowledge import payloads in handler to prevent destructive empty replace operation | Tests: go test ./internal/api -run TestKnowledgeImportRejectsEmptyEntries -count=1; go test ./internal/api -run TestKnowledgeHandlerImportReplacesEntries -count=1
- [2026-02-10 11:53 MST] Issue #675 | Commit f5bdfbf | committed | Added MemoryEvaluationPage.test.tsx covering loading, error, and fail-state metrics/failed-gates rendering | Tests: cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 11:55 MST] Issue #676 | Commit 82244ed | committed | Mapped memory dedup unique violations to ErrDuplicateMemory sentinel with duplicate-create regression coverage and API conflict mapping | Tests: go test ./internal/store -run TestMemoryStoreCreateDuplicateReturnsErrDuplicateMemory -count=1; go test ./internal/api -run TestMemoryHandler -count=1
- [2026-02-10 11:56 MST] Issue #677 | Commit 2c1b9ab | committed | Added 10 MiB CLI knowledge import pre-read size guard and oversize file regression test | Tests: go test ./cmd/otter -run TestHandleKnowledgeImportRejectsOversizeFile -count=1; go test ./cmd/otter -run TestHandleKnowledge -count=1
- [2026-02-10 11:57 MST] Issue #678 | Commit 62f5a25 | committed | Added knowledge API cross-org isolation regression test to verify org B cannot view org A entries | Tests: go test ./internal/api -run TestKnowledgeHandlerOrgIsolation -count=1; go test ./internal/api -run TestKnowledgeHandlerListEntries -count=1
- [2026-02-10 11:58 MST] Issue #672 | Commit c07539a | committed | Added MemoryStore.UpdateStatus lifecycle transition API (active->warm->archived) with org-scoped transition/cross-org tests | Tests: go test ./internal/store -run TestMemoryStoreUpdateStatus -count=1; go test ./internal/store -run TestMemoryStoreOrgIsolation -count=1
- [2026-02-10 11:58 MST] Issue #673 | Commit f531063 | committed | Clamped memory list/search limits at API handler level with deterministic clamp regression tests | Tests: go test ./internal/api -run TestMemoryHandlerCapsListAndSearchLimit -count=1; go test ./internal/api -run TestMemoryHandler -count=1
- [2026-02-10 11:58 MST] Issue #674 | Commit 49c22c3 | committed | Added CLI memory-create range validation helper and coverage for invalid importance/confidence/sensitivity values | Tests: go test ./cmd/otter -run TestHandleMemoryCreateValidatesRanges -count=1; go test ./cmd/otter -run TestHandleMemory -count=1
- [2026-02-10 11:59 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit c07539a,62f5a25,2c1b9ab,82244ed,f5bdfbf,49c22c3,f531063,d330000,7fda9c6,e44767f,a99cbd8,f3a138d | validated | Full validation sweep passed across backend/frontend reviewer fixes | Tests: go vet ./...; go build ./...; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 11:59 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit c07539a,62f5a25,2c1b9ab,82244ed,f5bdfbf,49c22c3,f531063,d330000,7fda9c6,e44767f,a99cbd8,f3a138d | blocked-push | Unable to push reviewer-cycle branch due environment policy rejecting git push; issue closures + move to 03-needs-review deferred pending external push from permissive environment | Tests: n/a
- [2026-02-10 12:08 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit c07539a,62f5a25,2c1b9ab,82244ed,f5bdfbf,49c22c3,f531063,d330000,7fda9c6,e44767f,a99cbd8,f3a138d | pushed | Published branch codex/spec-111-memory-infrastructure-overhaul-r4 to origin and set upstream tracking | Tests: n/a
- [2026-02-10 12:08 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit c07539a,62f5a25,2c1b9ab,82244ed,f5bdfbf,49c22c3,f531063,d330000,7fda9c6,e44767f,a99cbd8,f3a138d | closed | Closed all reviewer-cycle micro-issues with commit/test evidence (plus corrective comment after shell-escaping formatting issue) | Tests: go vet ./...; go build ./...; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 12:08 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Completed reviewer-cycle operational follow-through and moved spec 111 from 02-in-progress to 03-needs-review for external validation | Tests: see prior entry
- [2026-02-10 12:13 MST] Issue #667,#668,#669,#670,#671,#672,#673,#674,#675,#676,#677,#678 | Commit c07539a,62f5a25,2c1b9ab,82244ed,f5bdfbf,49c22c3,f531063,d330000,7fda9c6,e44767f,a99cbd8,f3a138d | pr-opened | Opened reviewer visibility PR #691 from codex/spec-111-memory-infrastructure-overhaul-r4 to main | Tests: go vet ./...; go build ./...; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 12:31 MST] Issue #n/a | Commit n/a | state-reconciled | Reconciled local spec phase with GitHub: PR #691 is still open, so spec moved from 05-completed back to 04-in-review pending external review/merge sign-off | Tests: n/a
- [2026-02-10 13:47 MST] Issue #n/a | Commit n/a | moved-to-in-progress | Re-queued spec 111 from 01-ready to 02-in-progress to resolve PR #691 merge-conflict blocker per branch-isolation policy | Tests: n/a
- [2026-02-10 13:48 MST] Issue #694 | Commit n/a | created | Created focused micro-issue for PR #691 conflict resolution with explicit backend/frontend regression test commands before implementation | Tests: go test ./internal/api -count=1; go test ./internal/store -count=1; go test ./internal/ottercli -count=1; go test ./cmd/otter -count=1; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 13:47 MST] Issue #n/a | Commit n/a | moved-to-ready | Moved spec 111 from 04-in-review to 01-ready because PR #691 was merge-conflicted (DIRTY), requiring a branch-isolated rework cycle | Tests: n/a
- [2026-02-10 13:50 MST] Issue #694 | Commit 2751248 | committed | Merged origin/main into spec-111 reviewer branch and resolved internal/ottercli/client_test conflict while preserving both success-size-cap and onboarding test coverage | Tests: go test ./internal/api -count=1; go test ./internal/store -count=1; go test ./internal/ottercli -count=1; go test ./cmd/otter -count=1; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 13:50 MST] Issue #694 | Commit 2751248 | pushed | Pushed merge-resolution commit to PR head branch codex/spec-111-memory-infrastructure-overhaul-r4 and confirmed PR #691 mergeStateStatus is CLEAN | Tests: gh pr view 691 --json mergeStateStatus
- [2026-02-10 13:50 MST] Issue #694 | Commit 2751248 | closed | Closed GitHub issue with commit hash and full regression evidence after clearing merge-conflict blocker | Tests: go test ./internal/api -count=1; go test ./internal/store -count=1; go test ./internal/ottercli -count=1; go test ./cmd/otter -count=1; go test ./...; cd web && npm test -- MemoryEvaluationPage.test.tsx --run
- [2026-02-10 13:50 MST] Issue #n/a | Commit n/a | moved-to-needs-review | Conflict-resolution implementation complete for PR #691; moved spec 111 from 02-in-progress to 03-needs-review awaiting external reviewer validation/merge | Tests: see Issue #694 entries above
