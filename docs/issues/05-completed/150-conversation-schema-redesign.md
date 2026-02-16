# Issue #150 — Conversation Schema Redesign

> **Priority:** P0
> **Status:** Not Ready — design spec
> **Depends on:** #125 (Three + Temps Architecture — completed, merged as PR #808)
> **Blocks:** #151 (Ellie Memory Infrastructure), #152 (Ellie Proactive Context Injection)
> **Author:** Josh S / Sam

## The Problem

The current database has conversations scattered across multiple tables with inconsistent search capabilities. The Three + Temps model (#125) requires a unified conversation infrastructure where:

- Memories belong to the org, not to agents (so temp teardown doesn't cascade-delete knowledge)
- Every message gets embedded for semantic search (Ellie's Tier 3 retrieval)
- Conversations are topic-segmented for memory attribution and provenance
- Rooms model who's in a conversation (agents, humans, mixed)
- Deprecated memories are preserved for historical context, not lost

## Current State

| Table | Purpose | Search | Problems |
|-------|---------|--------|----------|
| `project_chat_messages` | Project-scoped chat | tsvector (text) | No embeddings, no room concept, no participants |
| `agent_memories` | Per-agent memory files | None | Tied to agent_id with CASCADE — teardown deletes memories |
| `memory_entries` | Elephant's structured memories | pgvector (via companion table) | Tied to agent_id with CASCADE |
| `shared_knowledge` | Cross-agent promoted knowledge | pgvector (via companion table) | Separate from memory_entries, redundant structure |
| `activity_log` | General activity | None | Fine as-is |

## Target Schema

Five core tables replace the above.

### `rooms`

The container for any conversation. Defines who's talking and what the context is.

```sql
CREATE TABLE rooms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT,
    type TEXT NOT NULL CHECK (type IN ('project', 'issue', 'ad_hoc', 'system')),
    context_id UUID,              -- nullable FK to project/issue depending on type
    last_compacted_at TIMESTAMPTZ, -- watermark for Ellie's injection ledger reset (#152)
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

A room may contain two agents and no humans. A room may contain a human and an agent. A room may contain a human and multiple agents. This is the map of who exists within the context of a discussion. When viewing a chat through the web interface, you're looking at the history of a room.

### `room_participants`

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

### `chat_messages`

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

### Async Workers

Two background workers process messages after creation:

**1. Embedding worker** — finds rows where `embedding IS NULL`, generates vectors via local model, updates in place. Self-healing polling loop.

```
Loop:
  1. SELECT rows WHERE embedding IS NULL ORDER BY created_at LIMIT batch_size
  2. Generate embeddings via local model (all-MiniLM-L6-v2 or nomic-embed-text)
  3. UPDATE rows with generated vectors
  4. Sleep interval (e.g., 5s) if no rows found
```

- Bundled with Otter Camp install — no external API key required
- Local-first: `all-MiniLM-L6-v2` (384 dims, ~80MB, CPU) default
- Optional: external API (OpenAI `text-embedding-3-small`) via pluggable provider interface
- Configurable via `ellie.embedding_model` in org config
- Backfill-friendly — same worker handles existing messages on first deploy

**2. Conversation segmentation worker** — finds rows where `conversation_id IS NULL`, groups by topic/time, assigns conversation IDs. Runs on a slight delay to allow topic clusters to form.

### `conversations`

Topic-grouped segments within a room. Created asynchronously after messages land.

```sql
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    room_id UUID NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    topic TEXT,                    -- auto-generated summary of the conversation topic
    sensitivity TEXT NOT NULL DEFAULT 'normal' CHECK (sensitivity IN (
        'normal',       -- standard visibility
        'sensitive'     -- flagged during async processing for future access control
    )),
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,         -- NULL if conversation is still active
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Conversations are delineated after the fact — a near-real-time async grouping of messages by topic. This is the unit that memories get attributed to.

### `memories`

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
    sensitivity TEXT NOT NULL DEFAULT 'normal' CHECK (sensitivity IN (
        'normal',       -- visible to all agents in the org
        'sensitive'     -- restricted visibility (future: room-scoped or allow-list only)
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

**Key design decisions:**
- **No `agent_id`** — memories belong to the org, not an agent. Tearing down a temp doesn't cascade-delete anything.
- **`deprecated` status** — explicitly superseded memories kept for historical context. The `superseded_by` FK points to the replacement. "We used to prefer Postgres" is still queryable for historical context, but Ellie won't inject it as a current preference.
- **`source_conversation_id`** — traces every memory back to the conversation that produced it, giving full provenance.
- **Inline `embedding`** — no companion table. Vector lives on the row.
- **Expanded `kind` taxonomy** — `technical_decision`, `anti_pattern`, `correction`, `process_outcome`, etc. Better taxonomy = better retrieval precision.

### `activity_log`

Stays as-is. No changes.

## Tables Retired

| Old Table | Replaced By | Migration Path |
|-----------|------------|----------------|
| `project_chat_messages` | `chat_messages` + `rooms` | Create rooms per project, migrate rows |
| `agent_memories` | `memories` | Migrate content, drop agent_id ownership |
| `memory_entries` | `memories` | Migrate with kind mapping, drop agent_id |
| `memory_entry_embeddings` | Inline on `memories` | Flatten into parent row |
| `shared_knowledge` | `memories` | Merge, map kinds |
| `shared_knowledge_embeddings` | Inline on `memories` | Flatten into parent row |

## Provenance Chain

Every memory traces back to the conversation that produced it:

```
memory → conversation → messages → room → participants
```

"Why do we prefer Postgres?" → memory links to conversation → conversation contains the messages where Sam and Frank discussed it → room shows who was present.

## Ellie's Retrieval Cascade

This schema enables Ellie's five-tier retrieval (defined in #125):

```
Tier 1: Session context (current conversation thread — query chat_messages by room_id)
  ↓ not found
Tier 2: Memory store (memories table, pgvector semantic search)
  ↓ not found
Tier 3: Chat history store (chat_messages table, pgvector + keyword search)
  ↓ not found
Tier 4: JSONL file scan (brute force grep over raw session logs — last resort)
  ↓ not found
Tier 5: "I don't have this information." (zero-hallucination rule)
```

## Message Types

Messages in a room have a type field supporting Ellie's proactive context injection (#152):

| Type | Visibility | Purpose |
|------|-----------|---------|
| `message` | All participants | Normal conversation |
| `whisper` | Targeted participants only | Ellie → agent private context |
| `system` | All participants | Room events (joins, conversation boundaries) |
| `context_injection` | Agents + optionally Sam | Ellie's proactive context (subtype of whisper) |

## Open Questions

1. **Conversation segmentation algorithm** — Time-based gaps? Embedding similarity clustering? LLM-based topic detection? Start simple (time gaps + keyword shift), iterate.
2. **Room-to-project cardinality** — 1:1 (one room per project) or many:1 (multiple rooms per project, e.g., separate rooms for design vs. engineering)? Probably many:1.
3. **Migration order** — Can we create the new tables alongside the old ones and migrate incrementally, or is it a hard cutover?

## References

- #125 — Three + Temps Architecture (vision doc, original scope merged as PR #808)
- #151 — Ellie Memory Infrastructure (depends on this schema)
- #152 — Ellie Proactive Context Injection (depends on this schema + rooms + message types)
- Migration 023 — current `project_chat_messages`
- Migration 054 — current `agent_memories`
- Migration 058 — current `memory_entries`, `shared_knowledge`, `memory_entry_embeddings`, etc.

## Execution Log

- [2026-02-12 14:11 MST] Issue #821 | Commit n/a | in_progress | Moved spec 150 from 01-ready to 02-in-progress and created branch codex/spec-150-conversation-schema-redesign | Tests: n/a
- [2026-02-12 14:13 MST] Issue #821 | Commit n/a | created | Created micro-issue for conversation-core schema migration, constraints, indexes, and RLS coverage | Tests: n/a
- [2026-02-12 14:13 MST] Issue #822 | Commit n/a | created | Created micro-issue for project chat backfill into rooms/chat_messages with idempotency | Tests: n/a
- [2026-02-12 14:13 MST] Issue #823 | Commit n/a | created | Created micro-issue for project chat dual-write path into conversation tables | Tests: n/a
- [2026-02-12 14:13 MST] Issue #824 | Commit n/a | created | Created micro-issue for legacy memory backfill into org-owned memories table | Tests: n/a
- [2026-02-12 14:13 MST] Issue #825 | Commit n/a | created | Created micro-issue for embedding worker over chat_messages and memories | Tests: n/a
- [2026-02-12 14:13 MST] Issue #826 | Commit n/a | created | Created micro-issue for conversation segmentation worker and assignment logic | Tests: n/a
- [2026-02-12 14:16 MST] Issue #821 | Commit 130f515 | closed | Added migration 063 conversation-core schema (rooms/participants/conversations/chat_messages/memories) with RLS/index/runtime tests and pushed branch updates | Tests: go test ./internal/store -run TestMigration063ConversationSchemaFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run TestSchemaConversationCoreTablesCreateAndRollback -count=1; go test ./internal/store -run TestSchemaConversationCoreRLSAndIndexes -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1; go test ./internal/store -count=1
- [2026-02-12 14:19 MST] Issue #822 | Commit 83cc558 | closed | Added migration 064 to seed project rooms and backfill legacy project_chat_messages into chat_messages with parity/idempotency tests | Tests: go test ./internal/store -run TestMigration064ProjectChatBackfillFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run TestSchemaProjectChatBackfillCreatesProjectRooms -count=1; go test ./internal/store -run TestSchemaProjectChatBackfillCopiesMessagesWithParity -count=1; go test ./internal/store -run TestSchemaProjectChatBackfillIsIdempotent -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1; go test ./internal/store -count=1
- [2026-02-12 14:22 MST] Issue #823 | Commit bbba759 | closed | Added transactional project chat dual-write path to conversation tables with room bootstrap, rollback guarantees, and store/API regression tests | Tests: go test ./internal/store -run TestProjectChatStoreCreateDualWritesConversationMessage -count=1; go test ./internal/store -run TestProjectChatStoreCreateDualWriteRollbackOnConversationFailure -count=1; go test ./internal/api -run TestProjectChatCreatePersistsToConversationTables -count=1; go test ./internal/api -run TestProjectChatHandlerCreateAndList -count=1; go test ./internal/store -count=1; go test ./internal/api -count=1
- [2026-02-12 14:24 MST] Issue #824 | Commit 4a9ae49 | closed | Added migration 065 to backfill legacy memory tables into org-owned memories with status/kind mapping, superseded linkage reconciliation, and idempotent schema tests | Tests: go test ./internal/store -run TestMigration065MemoriesBackfillFilesExistAndContainCoreDDL -count=1; go test ./internal/store -run TestSchemaMemoriesBackfillCopiesLegacyRows -count=1; go test ./internal/store -run TestSchemaMemoriesBackfillMapsStatusesAndKinds -count=1; go test ./internal/store -run TestSchemaMemoriesBackfillIsIdempotent -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1; go test ./internal/store -count=1
- [2026-02-12 14:29 MST] Issue #825 | Commit 376f91d | closed | Added conversation embedding queue store + worker loop, config/startup wiring, and worker/config/server tests; pushed branch updates | Tests: go test ./internal/store -run TestConversationEmbeddingQueueListAndUpdate -count=1; go test ./internal/memory -run TestConversationEmbeddingWorkerProcessesPendingRows -count=1; go test ./internal/memory -run TestConversationEmbeddingWorkerRetriesAndContinues -count=1; go test ./cmd/server -run TestMainStartsConversationEmbeddingWorkerWhenConfigured -count=1; go test ./internal/config -count=1; go test ./internal/memory -count=1; go test ./internal/store -count=1; go test ./cmd/server -count=1; go test ./... -count=1
- [2026-02-12 14:33 MST] Issue #826 | Commit c712650 | closed | Added conversation segmentation queue store/worker, config and startup wiring, and segmentation worker tests; pushed branch updates | Tests: go test ./internal/store -run TestConversationSegmentationQueueListAndAssign -count=1; go test ./internal/memory -run TestConversationSegmentationWorkerSplitsOnTimeGap -count=1; go test ./internal/memory -run TestConversationSegmentationWorkerIdempotent -count=1; go test ./cmd/server -run TestMainStartsConversationSegmentationWorkerWhenConfigured -count=1; go test ./internal/config -count=1; go test ./internal/memory -count=1; go test ./internal/store -count=1; go test ./cmd/server -count=1; go test ./... -count=1
- [2026-02-12 14:33 MST] Issue #826 | Commit n/a | opened | Opened PR #827 for spec 150 implementation on branch codex/spec-150-conversation-schema-redesign | Tests: go test ./... -count=1
- [2026-02-12 14:33 MST] Issue #826 | Commit n/a | moved | Moved spec 150 from 02-in-progress to 03-needs-review after completing planned micro-issues #821-#826 and opening PR #827 | Tests: n/a
- [2026-02-12 14:59 MST] Issue #838 | Commit n/a | in_progress | Re-queued spec 150 reviewer-required changes by moving spec from 01-ready to 02-in-progress on branch codex/spec-150-conversation-schema-redesign | Tests: n/a
- [2026-02-12 15:00 MST] Issue #838 | Commit n/a | created | Created micro-issue for embedding queue org isolation/fairness remediation with explicit multi-org tests | Tests: n/a
- [2026-02-12 15:00 MST] Issue #839 | Commit n/a | created | Created micro-issue for conversations.sensitivity follow-up migration and schema constraint coverage | Tests: n/a
- [2026-02-12 15:00 MST] Issue #840 | Commit n/a | created | Created micro-issue for cancellable worker lifecycle and graceful shutdown coordination tests | Tests: n/a
- [2026-02-12 15:00 MST] Issue #841 | Commit n/a | created | Created micro-issue for segmentation queue org-aware selection and multi-org fairness tests | Tests: n/a
- [2026-02-12 15:03 MST] Issue #838 | Commit 2a6600e | closed | Added org-interleaved embedding queue selection for chat/memories plus multi-org fairness tests (store + worker integration) and pushed branch updates | Tests: go test ./internal/store -run TestConversationEmbeddingQueueListPendingBalancesAcrossOrgs -count=1; go test ./internal/memory -run TestConversationEmbeddingWorkerProcessesMultipleOrgsFairly -count=1; go test ./internal/store -count=1; go test ./internal/memory -count=1; go test ./... -count=1
- [2026-02-12 15:05 MST] Issue #839 | Commit 62d18b3 | closed | Added migration 069 for conversations.sensitivity (`normal|sensitive`) and schema tests covering migration files + DB constraint/default behavior; pushed branch updates | Tests: go test ./internal/store -run TestMigration069ConversationSensitivityFilesExistAndContainConstraint -count=1; go test ./internal/store -run TestSchemaConversationsSensitivityColumnAndConstraint -count=1; go test ./internal/store -run TestSchemaMigrationsUpDown -count=1; go test ./internal/store -count=1; go test ./... -count=1
- [2026-02-12 15:07 MST] Issue #840 | Commit 7682d85 | closed | Added signal-derived cancellable worker context + sync.WaitGroup shutdown coordination in cmd/server and cancellation-stop tests for embedding/segmentation workers; pushed branch updates | Tests: go test ./cmd/server -run TestMainWorkersStopOnContextCancel -count=1; go test ./internal/memory -run 'TestConversationEmbeddingWorkerStopsOnContextCancel|TestConversationSegmentationWorkerStopsOnContextCancel' -count=1; go test ./cmd/server -count=1; go test ./internal/memory -count=1; go test ./... -count=1
- [2026-02-12 15:09 MST] Issue #841 | Commit 49f25bf | closed | Added org-interleaved segmentation pending selection query plus multi-org fairness tests (store + worker integration) and pushed branch updates | Tests: go test ./internal/store -run TestConversationSegmentationQueueListPendingBalancesAcrossOrgs -count=1; go test ./internal/memory -run TestConversationSegmentationWorkerProcessesMultipleOrgsFairly -count=1; go test ./internal/store -count=1; go test ./internal/memory -count=1; go test ./... -count=1
- [2026-02-12 15:10 MST] Issue #841 | Commit 49f25bf | resolved | Completed all reviewer-required remediation issues (#838-#841), removed top-level Reviewer Required Changes block, and kept remediation summary in execution log entries | Tests: n/a
- [2026-02-12 15:10 MST] Issue #841 | Commit 49f25bf | moved | Moved spec 150 from 02-in-progress to 03-needs-review after closing remediation issues #838-#841 and pushing branch updates to PR #827 | Tests: n/a
