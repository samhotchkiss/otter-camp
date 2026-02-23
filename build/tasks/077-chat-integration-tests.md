# 077: Chat Integration Tests

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1-2 days) |
| Spec refs | doc 02 §SessionLifecycle, doc 02 §TurnStateMachine, doc 02 §MessageCRUD, doc 02 §ProgressiveSummarization, doc 02 §Reactions, doc 21 §IntegrationTests |
| Spec status | finished |
| Depends on | 043, 044, 045, 046, 047, 048 |
| Blocks | 089 |

## Scope

Integration test suite for the chat domain: session creation and participant management,
turn state machine, message CRUD and state transitions, progressive summarization trigger,
reaction recording and memory confidence feedback, read cursor management, and session
cleanup lifecycle. All tests use a real PostgreSQL database via `testdb.New(t)`.

### Must build

**Test file:** `internal/chat/session_integration_test.go`

**Test file:** `internal/chat/message_integration_test.go`

**Test file:** `internal/chat/summarization_integration_test.go`

Build tag: `//go:build integration`

Test setup helpers in `internal/testutil/chat.go`:
- `MakeSession(t, db, orgID, scopeType, scopeID)` — creates a `chat_session` row;
  returns `*chat.Session`
- `MakeMessage(t, db, sessionID, authorType, authorID, content)` — creates a
  `chat_message` row in 'final' state; returns `*chat.Message`
- `MakeTurn(t, db, sessionID)` — creates a `chat_turn` row; returns turn

**Test scenarios in session_integration_test.go:**

`TestSession_Create_OrgScope` — POST /v1/chat-sessions with scope_type='organization';
session row created with correct org_id and scope; GET /v1/chat-sessions/:id returns it.

`TestSession_Create_ProjectScope` — create with scope_type='project', scope_id set to a
real project; per-scope uniqueness: attempt to create a second session with same scope;
service returns existing session (not a duplicate row).

`TestSession_Create_TaskScope` — scope_type='project_task'; session created; GET returns it.

`TestSession_PerNodeAsync_AutoCreated` — flow node execution starts (via flow execution
service); a chat_session row is auto-created with scope_type='project_task',
session_mode='async'; `flow_node_execution.session_id` FK is set. This verifies the
cross-domain wiring from task 061.

`TestSession_ParticipantManagement` — POST /v1/chat-sessions/:id/participants to add
human user; verify `chat_participant` row; POST again with same participant returns 409;
POST to add agent participant; GET /v1/chat-sessions/:id returns both participants.

`TestSession_ModeSwitch` — create session with session_mode='sync'; PATCH to switch to
'async' (mode is mutable); GET confirms mode updated; message send behavior differs
(sync: streaming; async: no streaming).

`TestSession_ReadCursor` — create 5 messages; GET /v1/chat-sessions/:id/read-cursor
returns cursor at last read message; PUT /v1/chat-sessions/:id/read-cursor updates to
message 3; subsequent GET shows updated cursor.

**Test scenarios in message_integration_test.go:**

`TestMessage_StateMachine_PendingToFinal` — create message in 'pending' state; advance
to 'streaming'; advance to 'final'; assert each state is persisted; once 'final', no
further state transition is accepted (append-only after final).

`TestMessage_StateMachine_Failed` — message in 'streaming'; transition to 'failed';
message row has status='failed'; content preserved (not zeroed).

`TestMessage_Redaction` — message in 'final' state; call redaction service;
message `content` field becomes empty string (zeroed); `status='redacted'`; `redacted_at`
set; the row is preserved (not deleted); GET returns row with empty content.

`TestMessage_AppendOnlyAfterFinal` — attempt to modify content of a 'final' message
via PATCH; service returns error; content unchanged in DB (append-only semantics enforced).

`TestMessage_ToolCallAndResult` — create message with role='tool_call' (author is null);
create corresponding tool_result message; verify author_type is null on both; messages
appear in session history in correct order.

`TestMessage_QueuedEdit` — create a queued message (turn in progress); PATCH to edit
content while queued; edit succeeds; when turn completes, the edited content is used.

`TestReaction_Recording` — POST /v1/chat-sessions/:id/messages/:mid/reactions with
reactor_type='human_user', reaction='👍'; `chat_message_reaction` row created; unique
constraint: same user cannot react with same emoji twice (409 on duplicate); DELETE
reaction removes the row.

`TestReaction_MemoryFeedback` — POST reaction on a message; assert a memory confidence
feedback signal is emitted (domain event or direct service call); memory record
associated with the message has confidence updated.

`TestTurn_StateMachine` — create turn in status='pending'; transition to 'active' (Phase 1
starts); to 'completing' (Phase 3); to 'completed'; assert turn `completed_at` set;
`responding_type` is always 'agent' (spec invariant).

`TestTurn_Cancellation` — turn in 'active'; POST /v1/chat-sessions/:id/cancel-turn;
turn status becomes 'cancelled'; if a run was associated, run transitions to 'cancelling'.

`TestTurn_Steer` — turn in 'active'; POST /v1/chat-sessions/:id/messages/:mid/steer with
new direction; current turn is cancelled; new turn is queued with the steer input;
original message marked 'redacted'.

`TestMultiHuman_MessageQueue` — create session with 2 human participants; both send
messages in quick succession; messages are queued (not dropped); agent processes them
in FIFO order; both get turn responses.

**Test scenarios in summarization_integration_test.go:**

`TestSummarization_Trigger_ThresholdCheck` — create session; add enough turns to reach
~50-60% of layer 6 budget threshold (use a small test budget to avoid creating thousands
of messages); summarization is triggered; a `chat_summary` row is created.

`TestSummarization_ImmutableRow` — create a summary row; attempt to update it; service
returns error (immutable); assert `from_sequence` and `to_sequence` are unchanged.

`TestSummarization_PreservesArtifactRefs` — session has messages with file path
references and chat_artifact rows; after summarization, the summary content includes
the file path references verbatim (not compressed away).

`TestSummarization_SummarizesOldestTurns` — 10 turns in session; trigger summarization;
assert that the OLDEST 25-30% of turns are summarized (not the most recent); most recent
turns remain as full messages.

`TestRetention_SessionCleanup` — close a session (via session close event); assert:
(1) immediate: extraction pipeline triggered (domain event emitted);
(2) within test: ephemeral messages scheduled for deferred purge;
(3) summary consolidation job scheduled.
Assert all 3 jobs are enqueued in `job_queue` with correct job_types.

### Must NOT build

- E2E tests for chat lifecycle (task 083)
- Turn execution engine tests (task 048) — those test tool dispatch during turns
- SSE delivery tests (task 047)

## Acceptance Criteria

- [ ] All tests pass with `go test ./internal/chat/... -tags integration`
- [ ] `TestMessage_Redaction` verifies content is zeroed AND status='redacted' AND the row is preserved (not deleted)
- [ ] `TestMessage_AppendOnlyAfterFinal` confirms the error is returned at the service layer before any DB write
- [ ] `TestSummarization_SummarizesOldestTurns` explicitly asserts that the MOST RECENT turns are NOT summarized
- [ ] `TestSummarization_PreservesArtifactRefs` uses at least 2 artifact/file references and verifies both appear in the summary
- [ ] `TestSession_PerNodeAsync_AutoCreated` verifies the FK `flow_node_execution.session_id` is non-null
- [ ] `TestReaction_MemoryFeedback` verifies a memory service call is made (use a mock or assert domain event emission)

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:**
- `TestSession_Create_OrgScope`
- `TestSession_Create_ProjectScope`
- `TestSession_Create_TaskScope`
- `TestSession_PerNodeAsync_AutoCreated`
- `TestSession_ParticipantManagement`
- `TestSession_ModeSwitch`
- `TestSession_ReadCursor`
- `TestMessage_StateMachine_PendingToFinal`
- `TestMessage_StateMachine_Failed`
- `TestMessage_Redaction`
- `TestMessage_AppendOnlyAfterFinal`
- `TestMessage_ToolCallAndResult`
- `TestMessage_QueuedEdit`
- `TestReaction_Recording`
- `TestReaction_MemoryFeedback`
- `TestTurn_StateMachine`
- `TestTurn_Cancellation`
- `TestTurn_Steer`
- `TestMultiHuman_MessageQueue`
- `TestSummarization_Trigger_ThresholdCheck`
- `TestSummarization_ImmutableRow`
- `TestSummarization_PreservesArtifactRefs`
- `TestSummarization_SummarizesOldestTurns`
- `TestRetention_SessionCleanup`

**E2E tests:** None — covered by task 083.

## Implementer Notes

**What is real vs mocked:**
- PostgreSQL: real, via `testdb.New(t)`
- Model gateway (summarization): `MockProviderServer` (scripted summary response)
- Clock: injected `clock.Fake` for session lifecycle timing
- Domain event bus: real in-process dispatch

**Summarization token budget:**
`TestSummarization_Trigger_ThresholdCheck` would require thousands of real messages to
hit the production layer-6 token threshold. Override the summarization trigger threshold
via a test configuration option (e.g., `summarization.threshold_override=10_turns`) to
keep the test fast and deterministic.

**Steer behavior note:**
`TestTurn_Steer` invokes the steer endpoint which cancels the current turn and queues
a new one. The test verifies the DB state (turn cancelled, new turn created, original
message redacted) but does not execute the new turn end-to-end.

**Session per-scope uniqueness:**
`TestSession_Create_ProjectScope` tests the uniqueness enforcement. The rule is: at most
one non-closed session per scope. The test must close the first session before creating
a second one for the same scope (else the second creation should return the existing one).
