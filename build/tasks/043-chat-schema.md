# 043: Chat Session Schema

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §ChatSchema, doc 02 §SessionLifecycle, doc 02 §MessageModel, doc 02 §TurnModel, doc 06 §MemorySource |
| Spec status | finished |
| Depends on | 003, 005, 013, 016, 027, 038 |
| Blocks | 044, 045, 046, 047, 048, 049, 050, 051, 062, 072, 083 |

## Scope

Build all chat domain tables and their repositories. This is the foundational schema for the
entire chat subsystem. Covers DDL migrations and the full repository layer for every table.
No service logic, no API endpoints — only schema and data access.

### Must build

**Migrations (in order):**
- `0050_chat_session.sql` — `chat_session` table + indexes
- `0051_chat_participant.sql` — `chat_participant` table
- `0052_chat_turn.sql` — `chat_turn` table
- `0053_chat_message.sql` — `chat_message` table + indexes
- `0054_chat_artifact.sql` — `chat_artifact` table
- `0055_chat_summary.sql` — `chat_summary` table
- `0056_chat_read_cursor.sql` — `chat_read_cursor` table
- `0057_chat_message_reaction.sql` — `chat_message_reaction` table
- `0058_memory_source.sql` — `memory_source` table (L4 — soft FK to chat_session; deferred from task 038)

**`chat_session` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `organization_id uuid not null references organization(id) on delete cascade`
- `scope_type text not null check (scope_type in ('organization','project','project_task'))` — polymorphic scope
- `scope_id uuid not null` — no SQL FK; application-layer enforcement (see ISSUE #9 pattern)
- `mode text not null check (mode in ('sync','async'))` — mutable; sync = real-time chat, async = background agent session
- `status text not null check (status in ('active','closed','archived'))` default `'active'`
- `title text` — nullable; user-visible session name; may be null for auto-created sessions
- `created_by_type text not null check (created_by_type in ('human_user','agent','system'))` — polymorphic creator
- `created_by_id uuid` — null for system sentinel (use 00000000-0000-0000-0000-000000000000 sentinel UUID, not SQL NULL)
- `current_turn_id uuid` — nullable; FK set after `chat_turn` table exists (deferred constraint or application-layer); points to the in-progress turn
- `last_message_at timestamptz` — null until first message; used for sorting sessions
- `turn_count integer not null default 0`
- `message_count integer not null default 0`
- `metadata jsonb not null default '{}'` — extension point; e.g. flow_node_id for async sessions
- `closed_at timestamptz`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(organization_id, status, last_message_at DESC)` — session list
- Index: `(scope_type, scope_id, status)` — per-scope uniqueness queries and lookups
- Index: `(created_by_type, created_by_id)` where both non-null — creator reverse lookup

**Per-scope uniqueness rule** (application-layer, not DB unique constraint): only one active
sync session per scope_id is enforced by the service layer. Multiple async sessions per scope
are allowed. This is enforced in `ChatService.CreateSession` (task 044), not by a DB unique
index, because the rule is mode-conditional.

**`chat_participant` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `participant_type text not null check (participant_type in ('human_user','agent'))` — polymorphic
- `participant_id uuid not null` — no SQL FK; application-layer enforcement
- `role text not null check (role in ('owner','member','observer'))` default `'member'`
- `notification_preference text not null check (notification_preference in ('all','mentions','none'))` default `'all'`
- `joined_at timestamptz not null default now()`
- `removed_at timestamptz` — null unless participant was removed; soft delete
- Unique index: `(session_id, participant_type, participant_id) WHERE removed_at IS NULL` — one active slot per actor per session

**`chat_turn` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `turn_number integer not null` — sequential within session; assigned at turn creation
- `cycle_id uuid` — nullable; groups a multi-phase turn cycle (phases 1, 1.5, 2, 3); all turns sharing a cycle_id belong to the same logical exchange
- `responding_type text not null check (responding_type in ('agent'))` — always 'agent' per doc 02; future-proofed column
- `responding_id uuid not null` — the agent.id that owns this turn
- `status text not null check (status in ('pending','in_progress','completed','cancelled','failed'))` default `'pending'`
- `cancel_requested_at timestamptz` — null unless cancellation was requested
- `started_at timestamptz`
- `completed_at timestamptz`
- `duration_ms integer` — populated on completion
- `error_message text`
- `created_at timestamptz not null default now()`
- Unique index: `(session_id, turn_number)` — monotonic ordering
- Index: `(session_id, status)` — find in-progress turns for a session

**`chat_message` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `turn_id uuid references chat_turn(id) on delete set null` — nullable; not all messages are turn-scoped (e.g. system messages, queued human messages awaiting turn)
- `sequence_number bigint not null` — global sequence within session; used for SSE ordering; assigned via sequence or row ordering
- `author_type text check (author_type in ('human_user','agent'))` — nullable for system/tool_result/tool_call messages
- `author_id uuid` — null when author_type is null
- `role text not null check (role in ('user','assistant','tool_call','tool_result','system'))` — maps to LLM role conventions
- `content text not null default ''` — message body; set to '' (empty string) on redaction; NOT NULL
- `content_format text not null check (content_format in ('text','markdown','tool_json'))` default `'text'`
- `status text not null check (status in ('pending','streaming','final','failed','redacted'))` default `'pending'`
- `is_redacted boolean not null default false` — true after redaction; content set to ''
- `redacted_at timestamptz`
- `tool_call_id text` — non-null for role='tool_call' and role='tool_result'; correlates call/result pairs
- `metadata jsonb not null default '{}'` — e.g. streaming token counts, tool execution ids
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`
- Index: `(session_id, sequence_number)` — chronological message fetch (primary read path)
- Index: `(session_id, turn_id)` where turn_id is not null — turn-scoped message fetch
- Index: `(session_id, status)` — find pending/streaming messages

**Append-only rule**: messages with `status='final'` or `status='redacted'` cannot have their
`content` updated. The repository `UpdateContent` method must check status and return an error
if the message is already finalized. Redaction zeroes the content field and sets `is_redacted=true`.

**`chat_artifact` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `message_id uuid not null references chat_message(id) on delete cascade`
- `artifact_type text not null check (artifact_type in ('file','image','code','data','link'))` — classification
- `filename text` — nullable; original filename if file-type
- `content_type text` — MIME type; nullable
- `storage_key text` — object storage key; null for link-type artifacts
- `url text` — direct URL; null for non-link artifacts; for link type, the external URL
- `byte_size bigint` — file size in bytes; null for link artifacts
- `created_at timestamptz not null default now()`
- Index: `(session_id, message_id)` — artifacts per message

**`chat_summary` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `from_sequence bigint not null` — inclusive start of summarized range
- `to_sequence bigint not null` — inclusive end of summarized range
- `summary_text text not null` — the generated summary content
- `summarized_turn_count integer not null` — number of turns covered by this summary
- `model_invocation_id uuid` — nullable; the model call that generated this summary (for attribution)
- `created_at timestamptz not null default now()`
- Unique index: `(session_id, from_sequence)` — no overlapping summaries per session
- Check constraint: `to_sequence >= from_sequence`

**`chat_read_cursor` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `session_id uuid not null references chat_session(id) on delete cascade`
- `user_id uuid not null references human_user(id) on delete cascade`
- `last_read_sequence bigint not null default 0` — the sequence_number of the last message the user has read
- `updated_at timestamptz not null default now()`
- Unique index: `(session_id, user_id)` — one cursor per user per session

**`chat_message_reaction` table** (doc 02):
- `id uuid primary key default gen_random_uuid()`
- `message_id uuid not null references chat_message(id) on delete cascade`
- `session_id uuid not null references chat_session(id) on delete cascade` — denormalized for query efficiency
- `reactor_type text not null check (reactor_type in ('human_user','agent'))` — polymorphic
- `reactor_id uuid not null` — no SQL FK; application-layer enforcement
- `emoji text not null` — the reaction emoji (e.g. '👍', ':thumbsup:', or short-code form)
- `created_at timestamptz not null default now()`
- Unique index: `(message_id, reactor_type, reactor_id, emoji)` — one reaction per emoji per actor per message

**`memory_source` table** (doc 06 — L4 placement):
- `id uuid primary key default gen_random_uuid()`
- `memory_id uuid not null references memory(id) on delete cascade`
- `source_type text not null check (source_type in ('chat_message','event','file','memory_import','explicit'))` — polymorphic source
- `source_id uuid` — nullable; references `chat_message.id` when `source_type='chat_message'`; null for 'event', 'file', 'explicit' types
- `import_id uuid references memory_import(id) on delete set null` — non-null when `source_type='memory_import'`
- `session_id uuid` — soft reference to `chat_session.id`; NO SQL FK (see ISSUE #9); nullable; application-layer integrity only
- `created_at timestamptz not null default now()`
- Index: `(memory_id)` — sources per memory
- Index: `(session_id) WHERE session_id IS NOT NULL` — soft-ref reverse lookup (partial index)
- Index: `(source_type, source_id) WHERE source_id IS NOT NULL` — reverse lookup from chat_message

**Repository layer:**
- `ChatSessionRepo`: `Create`, `GetByID`, `GetByScopeAndMode`, `ListByOrg`, `UpdateMode`, `UpdateStatus`, `UpdateCurrentTurn`, `IncrementCounts`, `UpdateLastMessageAt`, `Close`
  - `GetByScopeAndMode(ctx, scopeType, scopeID, mode) (*ChatSession, error)` — used by service to enforce per-scope uniqueness
- `ChatParticipantRepo`: `Create`, `GetBySessionAndActor`, `ListBySession`, `ListByActor`, `UpdateNotificationPreference`, `Remove`
- `ChatTurnRepo`: `Create`, `GetByID`, `GetBySessionAndNumber`, `ListBySession`, `UpdateStatus`, `SetStarted`, `SetCompleted`, `SetCancelled`, `SetFailed`
- `ChatMessageRepo`: `Create`, `GetByID`, `GetBySequence`, `ListBySession`, `ListByTurn`, `UpdateStatus`, `UpdateContent`, `Redact`, `GetPending`, `GetByToolCallID`
  - `UpdateContent` must reject updates to messages with status in ('final', 'redacted')
- `ChatArtifactRepo`: `Create`, `GetByID`, `ListBySession`, `ListByMessage`
- `ChatSummaryRepo`: `Create`, `GetLatestForSession`, `ListBySession`
- `ChatReadCursorRepo`: `Upsert`, `GetBySessionAndUser`, `ListBySession`
- `ChatMessageReactionRepo`: `Create`, `GetByID`, `ListByMessage`, `DeleteByID`, `GetByActorAndEmoji`
- `MemorySourceRepo`: `Create`, `GetByID`, `ListByMemory`, `ListBySession`, `ListBySourceMessage`

### Must NOT build

- Session lifecycle service logic (task 044)
- Progressive summarization service (task 045)
- Chat API endpoints (task 046)
- SSE/WebSocket realtime layer (task 047)
- Turn execution engine (task 048)
- Tool resolution pipeline / `session_tool_set` table (task 049)
- Prompt assembly engine (task 050)
- `session_tool_set` DDL (task 049)

## Acceptance Criteria

- [ ] All 9 migrations apply cleanly in order on a fresh database; `schema_migrations` table records all 9 versions
- [ ] `chat_session` per-scope uniqueness is NOT a DB unique constraint — two rows with identical `(scope_type, scope_id)` can coexist in the DB (the constraint is service-layer only)
- [ ] `chat_message.content` is empty string `''` after redaction, not SQL NULL; `is_redacted=true`; `status='redacted'`
- [ ] `ChatMessageRepo.UpdateContent` returns an error when called on a message with `status='final'` or `status='redacted'`
- [ ] `chat_message_reaction` unique index prevents duplicate reactions: inserting the same `(message_id, reactor_type, reactor_id, emoji)` tuple twice raises a unique constraint violation
- [ ] `chat_summary` unique index on `(session_id, from_sequence)` prevents two summaries starting at the same sequence
- [ ] `memory_source.session_id` column has no FK constraint in the DDL; inserting a `session_id` that does not exist in `chat_session` succeeds at the DB level
- [ ] `chat_read_cursor` upsert: calling `Upsert` twice with the same `(session_id, user_id)` results in a single row with the latest `last_read_sequence`

## Tests Required

**Unit tests:**
- `ChatMessageRepo.UpdateContent` on a final message: assert error returned, content unchanged
- `ChatMessageRepo.Redact`: assert `content=''`, `is_redacted=true`, `status='redacted'` after call
- `ChatMessageRepo.GetBySequence`: insert 3 messages with sequence 1, 2, 3; fetch by sequence 2 → correct row
- `MemorySourceRepo.ListBySession`: insert 2 memory_source rows with same session_id, 1 with different; fetch by session_id → 2 rows returned

**Integration tests:**
- `chat_participant` remove: `Remove(participant_id)` sets `removed_at`; `ListBySession` with default filter excludes removed participants; `GetBySessionAndActor` returns not-found after removal
- `chat_summary` range check: attempt insert with `from_sequence=10, to_sequence=5` → check constraint violation
- `memory_source` soft-ref: insert `memory_source` row with `session_id` pointing to non-existent session UUID → succeeds at DB level; no FK error
- `chat_turn` turn_number ordering: insert turns 1, 2, 3; `ListBySession(orderBy=turn_number)` returns in order; unique constraint on `(session_id, turn_number)` blocks duplicate

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- `chat_session.current_turn_id` is a forward reference to `chat_turn`. To avoid a circular FK (session → turn → session), implement `current_turn_id` as a plain `uuid` column with no SQL FK constraint. The service layer (task 044) is responsible for keeping it accurate. Add a DDL comment: `-- soft ref to chat_turn.id; no FK to avoid circular dependency`.
- `chat_message.sequence_number` must be monotonically increasing per session with no gaps. Use a PostgreSQL sequence scoped to the session, or assign via `SELECT COALESCE(MAX(sequence_number), 0) + 1 FROM chat_message WHERE session_id = $1` inside a transaction. The latter is simpler but holds a row-level lock; document the chosen approach.
- `chat_turn.responding_type` is always `'agent'` per doc 02 spec. The check constraint is `check (responding_type in ('agent'))`. This seems redundant but is intentional: it documents the constraint and makes future extension (if human turns are ever modeled) explicit.
- `chat_message.role` values map to LLM conventions: `'user'` for human input, `'assistant'` for agent output, `'tool_call'` for the agent's invocation of a tool, `'tool_result'` for the tool's response, `'system'` for injected system-level messages. `author_type` and `author_id` are null for `role IN ('tool_call', 'tool_result', 'system')`.
- The `memory_source` table belongs here (L4) because it has a soft reference to `chat_session`. Its migration stub in task 038 (`GetSources` returning "not yet implemented") can now be replaced with the real implementation once this migration runs.

> ✅ ISSUE #9 (RESOLVED): `memory_source.session_id` is a permanent soft reference — no SQL FK, by design. Memories outlive sessions. Application-layer must always use a LEFT JOIN guard when joining to `chat_session`. Never add a FK constraint.
