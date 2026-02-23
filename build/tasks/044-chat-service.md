# 044: Chat Service

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §SessionLifecycle, doc 02 §TurnCycle, doc 02 §MessageModel, doc 02 §ParticipantModel, doc 02 §Reactions, doc 02 §MultiHumanQueue |
| Spec status | finished |
| Depends on | 043, 006, 014, 024, 033 |
| Blocks | 045, 046, 047, 048, 072, 083 |

## Scope

Build the chat service layer: session lifecycle, participant management, message CRUD, the
turn cycle state machine, message editing for queued messages, reactions, and notification
preference management. This is pure service logic wiring the repositories from task 043 —
no HTTP handlers, no SSE streaming (task 047), no turn engine execution (task 048).

### Must build

**Session lifecycle (`ChatService`):**
- `CreateSession(ctx, input CreateSessionInput) (*ChatSession, error)`
  - Enforces per-scope sync uniqueness: if a `status='active'` sync session already exists for
    `(scope_type, scope_id)`, returns `ErrActiveSyncSessionExists`. Async sessions are exempt.
  - Sets `created_by_type` and `created_by_id` from the authenticated principal.
  - If `scope_type='project_task'`, validates that the task exists in the caller's org (application-layer scope_id check).
  - Publishes `chat.session.created` domain event via task 024 event bus.
- `GetSession(ctx, id) (*ChatSession, error)` — scoped to org
- `ListSessions(ctx, filter SessionFilter) ([]*ChatSession, error)` — filter by scope, status, mode, cursor pagination
- `SwitchMode(ctx, sessionID, newMode) error`
  - Allowed transitions: `sync → async` (always), `async → sync` (only if no in-progress turn)
  - Publishes `chat.session.mode_changed` domain event
- `CloseSession(ctx, sessionID) error`
  - Sets `status='closed'`, `closed_at=now()`
  - Triggers extraction pipeline (publishes `chat.session.closed` domain event; extraction worker picks it up)
  - Deferred daily purge for ephemeral async sessions (schedule via job queue)
  - Blocks if there is an in-progress turn (returns `ErrTurnInProgress`)
- `GetOrCreateNodeSession(ctx, flowNodeExecutionID, agentID) (*ChatSession, error)`
  - Per-node async session auto-creation: called by flow execution when a node starts.
  - Creates a new async session scoped to `scope_type='project_task'` with `mode='async'`.
  - Multiple nodes → multiple sessions; no uniqueness enforcement for async.
  - Stores `flow_node_execution_id` in `metadata` for traceability.

**Participant management:**
- `AddParticipant(ctx, sessionID, participantType, participantID, role) (*ChatParticipant, error)`
  - Validates participant exists (human_user or agent) in the caller's org.
  - Returns `ErrAlreadyParticipant` if active participation record exists.
- `RemoveParticipant(ctx, sessionID, participantID) error` — soft-deletes by setting `removed_at`
- `ListParticipants(ctx, sessionID) ([]*ChatParticipant, error)`
- `UpdateNotificationPreference(ctx, sessionID, userID, preference) error`
  - Only human_user participants can update their own notification preference.
  - `preference` must be one of `all|mentions|none`.

**Message CRUD:**
- `AppendMessage(ctx, input AppendMessageInput) (*ChatMessage, error)`
  - Assigns `sequence_number` (transaction-safe increment, see task 043 implementer notes).
  - Sets initial `status='pending'`.
  - Updates `chat_session.last_message_at` and increments `message_count`.
  - Publishes `chat.message.created` domain event for SSE fan-out (task 047).
- `UpdateMessageStatus(ctx, messageID, newStatus, errorMsg) error`
  - Enforces allowed transitions: `pending→streaming`, `streaming→final`, `streaming→failed`,
    `pending→failed`. No other transitions permitted. Returns `ErrInvalidStatusTransition`.
- `RedactMessage(ctx, messageID) error`
  - Sets `content=''`, `is_redacted=true`, `status='redacted'`.
  - Only org-admin or the author may redact.
  - Publishes `chat.message.redacted` domain event.
- `EditQueuedMessage(ctx, messageID, newContent) error`
  - Only allowed on messages with `status='pending'` authored by a human_user.
  - Returns `ErrMessageNotEditable` if not pending or not a human message.
  - Multi-human shared queue rule: if there are multiple humans queued (pending messages from
    different human_users in the same session), the agent processes them FIFO. An edit changes
    the content of the specific queued message, not its queue position.
- `GetMessage(ctx, messageID) (*ChatMessage, error)`
- `ListMessages(ctx, sessionID, filter MessageFilter) ([]*ChatMessage, error)` — cursor-based pagination on `sequence_number`

**Turn cycle state machine:**
- `CreateTurn(ctx, sessionID, agentID) (*ChatTurn, error)`
  - Assigns `turn_number` (MAX + 1 within session, inside transaction).
  - Sets `status='pending'`.
  - Optionally assigns `cycle_id` (provided by caller or generated).
  - Updates `chat_session.current_turn_id` and increments `turn_count`.
- `StartTurn(ctx, turnID) error` — `pending → in_progress`, sets `started_at`
- `CompleteTurn(ctx, turnID) error` — `in_progress → completed`, sets `completed_at`, `duration_ms`
- `CancelTurn(ctx, turnID, reason) error`
  - `in_progress → cancelled`, sets `cancel_requested_at`, `completed_at`
  - Publishes `chat.turn.cancelled` domain event
  - The turn engine (task 048) listens for this event to abort in-flight model calls
- `FailTurn(ctx, turnID, errorMsg) error` — `in_progress → failed`
- `GetTurn(ctx, turnID) (*ChatTurn, error)`
- `ListTurns(ctx, sessionID) ([]*ChatTurn, error)`

**Turn cancellation modes** (doc 02 §TurnCancellation):
- `CancelCurrentTurn(ctx, sessionID) error` — cancels the current in-progress turn; returns `ErrNoActiveTurn` if no turn in progress
- `CancelAndQueueNew(ctx, sessionID, newMessage) error` — cancels current turn and immediately queues a new user message
- `SteerTurn(ctx, sessionID, messageID, steerContent) error`
  - Creates a new `user` role message with the steer content referencing the in-progress message.
  - Does NOT cancel the current turn; the turn engine polls for steer events and adapts.
  - Only valid when a turn is `in_progress`; returns `ErrNoActiveTurn` otherwise.

**Reactions:**
- `AddReaction(ctx, messageID, reactorType, reactorID, emoji) (*ChatMessageReaction, error)`
  - Returns `ErrDuplicateReaction` on unique constraint violation (same emoji, same actor, same message).
  - Publishes `chat.reaction.added` domain event (triggers memory confidence feedback in task 048).
- `RemoveReaction(ctx, reactionID) error`
  - Only the reactor or an org-admin may remove a reaction.
  - Publishes `chat.reaction.removed` domain event.
- `ListReactions(ctx, messageID) ([]*ChatMessageReaction, error)`

**Error types** (define as typed errors for clean handler mapping):
- `ErrActiveSyncSessionExists` → 409 Conflict
- `ErrTurnInProgress` → 409 Conflict
- `ErrNoActiveTurn` → 409 Conflict
- `ErrAlreadyParticipant` → 409 Conflict
- `ErrDuplicateReaction` → 409 Conflict
- `ErrMessageNotEditable` → 422 Unprocessable
- `ErrInvalidStatusTransition` → 422 Unprocessable
- `ErrSessionClosed` → 422 Unprocessable

### Must NOT build

- HTTP handlers / API endpoints (task 046)
- SSE event streaming (task 047)
- Turn execution engine (prompt assembly, model calls — task 048)
- Progressive summarization (task 045)
- Tool resolution (task 049)
- Capability policy evaluation (task 033, already built)
- Memory extraction triggered by session close (just publish the domain event here; task 039 worker handles it)

## Acceptance Criteria

- [ ] `CreateSession` with `mode='sync'` on a scope that already has an active sync session returns `ErrActiveSyncSessionExists`; creating an async session on the same scope succeeds
- [ ] `CreateSession` publishes `chat.session.created` domain event readable via the event bus
- [ ] `SwitchMode` from `async→sync` while a turn is `in_progress` returns `ErrTurnInProgress`
- [ ] `CloseSession` while a turn is `in_progress` returns `ErrTurnInProgress`
- [ ] `AppendMessage` assigns monotonically increasing `sequence_number` values under concurrent inserts (no duplicates, no gaps under contention — verify with a concurrent test)
- [ ] `EditQueuedMessage` on a message with `status='final'` returns `ErrMessageNotEditable`
- [ ] `AddReaction` with the same `(message_id, reactor_type, reactor_id, emoji)` twice returns `ErrDuplicateReaction` on the second call
- [ ] `CancelCurrentTurn` publishes `chat.turn.cancelled` domain event with the turn ID

## Tests Required

**Unit tests:**
- `CreateSession` sync uniqueness: mock repo to simulate existing active sync session → `ErrActiveSyncSessionExists`
- `SwitchMode` invalid async→sync with in-progress turn: mock repo returns turn with status='in_progress' → `ErrTurnInProgress`
- `UpdateMessageStatus` invalid transition `final→streaming`: verify `ErrInvalidStatusTransition`
- `CancelCurrentTurn` with no active turn: mock repo returns no in-progress turn → `ErrNoActiveTurn`
- `EditQueuedMessage` on a final message: mock repo returns status='final' → `ErrMessageNotEditable`
- Turn number assignment: `CreateTurn` called on session with 3 existing turns → assigns turn_number=4
- Reaction dedup: `AddReaction` wraps unique constraint violation from repo into `ErrDuplicateReaction`

**Integration tests:**
- Session close triggers `chat.session.closed` event: create session → close → poll event bus → event present with correct session_id
- Concurrent `AppendMessage`: 10 goroutines appending messages to same session simultaneously → all sequence_numbers unique and form a contiguous sequence
- Multi-human queue FIFO: append 3 pending messages from 3 different human_users → `ListMessages(status=pending)` returns in sequence_number order
- Participant removal: `AddParticipant` → `RemoveParticipant` → `ListParticipants` → removed participant absent; `AddParticipant` for same actor again succeeds (new row created)

**E2E tests:**
- None — covered by dedicated E2E task 072 and 083

## Implementer Notes

- The `GetOrCreateNodeSession` method is called from the flow execution service (task 060, L4). It must be idempotent: if an async session already exists for the given `flow_node_execution_id` in `metadata`, return the existing one. Use a SELECT-then-INSERT-on-conflict pattern or an application-level lock (advisory lock on the flow_node_execution_id) to avoid duplicates.
- `AppendMessage` must update `chat_session.last_message_at` and `message_count` in the same transaction as the message insert. Use `UPDATE chat_session SET last_message_at=now(), message_count=message_count+1 WHERE id=$1` rather than a trigger, to keep behavior explicit.
- The `SteerTurn` method does not cancel the current turn — the turn engine (task 048) must poll for new messages during its tool loop and detect steer messages by their `role='user'` and their `created_at` being newer than `chat_turn.started_at`. Document this polling contract so task 048 implementers know to check for steers.
- Multi-human shared queue: when multiple humans are participants in the same session, their `AppendMessage` calls all create `status='pending'` messages. The turn engine (task 048) drains pending human messages in `sequence_number` order at the start of each turn. This is FIFO by design. The service layer does not need to enforce ordering beyond assigning monotonic sequence numbers.
- Notification preference (`UpdateNotificationPreference`) affects how the SSE/push layer filters events for each user. The preference is stored on `chat_participant.notification_preference`. The SSE layer (task 047) reads this at event dispatch time.
