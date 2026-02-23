# 083: Chat Lifecycle E2E

| Field | Value |
|-------|-------|
| Layer | L5 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §ChatSession, doc 02 §ChatTurn, doc 02 §ChatMessage, doc 02 §Reactions, doc 02 §SSE, doc 21 §E2ETests |
| Spec status | finished |
| Depends on | 001–080 |
| Blocks | — |

## Scope

E2E test scenario for the full chat session lifecycle. Uses only the `ottercamp` CLI
binary and REST API, including SSE streaming. Verifies: session creation, agent
participant addition, message send, turn creation, agent response via SSE, turn
completion, follow-up message, conversation history, message reaction, reaction storage,
in-flight turn cancellation, and cancellation propagation.

### Must build

**Test file:** `e2e/chat_test.go`

Build tag: `//go:build e2e`

Test setup: calls `POST /v1/test/reset` and `ottercamp bootstrap` before each scenario.
Uses `e2e/testutil/` helpers including:
- `SSEClient(t, baseURL, path, token)` — opens an SSE connection and returns a channel
  of `SSEEvent{ID, Event, Data string}` plus a `Close()` function
- `WaitForSSEEvent(t, ch, eventType, timeout)` — reads from the SSE channel until an
  event matching `eventType` is received or timeout expires

**Scenario: `TestChat_CreateSessionAndSendMessage`**

Step 1 — Reset, bootstrap, get token:
```
POST /v1/test/reset → 204
ottercamp bootstrap → exit 0
admin token via POST /v1/auth/login
```

Step 2 — Create chat session:
```
POST /v1/chat-sessions
Authorization: Bearer <token>
{
  "scope_type": "organization",
  "mode": "sync"
}
→ 201
→ body.data.id is non-empty UUID (session_id)
→ body.data.mode == "sync"
→ body.data.scope_type == "organization"
```

Step 3 — Add agent participant (Frank):
```
GET /v1/agents?name=Frank → body.data[0].id (frank_id)

POST /v1/chat-sessions/<session_id>/participants
Authorization: Bearer <token>
{
  "participant_type": "agent",
  "participant_id": "<frank_id>"
}
→ 201
```

Step 4 — Open SSE stream before sending message:
```
GET /v1/events/stream?scopes=session:<session_id>
Authorization: Bearer <token>
→ 200 text/event-stream (connection held open)
```

Step 5 — Send a message:
```
POST /v1/chat-sessions/<session_id>/messages
Authorization: Bearer <token>
{
  "content": "Hello, what can you help me with today?",
  "role": "human"
}
→ 201
→ body.data.id is non-empty UUID (message_id)
→ body.data.state == "final" or "pending"
```

Step 6 — Verify turn created:
```
GET /v1/chat-sessions/<session_id>/turns
Authorization: Bearer <token>
→ 200
→ body.data length >= 1
→ body.data[0].status == "active" or "completed"
→ body.data[0].id is non-empty UUID (turn_id)
```

Step 7 — Wait for agent response via SSE:
```
Wait for SSE event with event type "message.final" or "turn.completed"
→ received within 30 seconds
→ event data contains session_id == <session_id>
```

Step 8 — Verify turn completed:
```
GET /v1/chat-sessions/<session_id>/turns
→ 200
→ body.data[0].status == "completed"
```

Step 9 — Verify agent response message exists:
```
GET /v1/chat-sessions/<session_id>/messages
Authorization: Bearer <token>
→ 200
→ body.data length >= 2  (human message + agent response)
→ at least one message has author_type == "agent"
→ agent message has state == "final"
→ agent message content is non-empty string
```

**Scenario: `TestChat_ConversationHistory`**

After `TestChat_CreateSessionAndSendMessage` completes (or in a fresh session):

Step 1 — Send a follow-up message:
```
POST /v1/chat-sessions/<session_id>/messages
{ "content": "Can you elaborate on that?", "role": "human" }
→ 201
```

Step 2 — Wait for second agent response:
```
Wait for SSE event "turn.completed" → within 30 seconds
```

Step 3 — Verify conversation history:
```
GET /v1/chat-sessions/<session_id>/messages
→ 200
→ body.data length >= 4  (2 human + 2 agent)
→ messages are in chronological order (sorted by created_at ascending)
→ body.meta.total >= 4
```

**Scenario: `TestChat_Reaction`**

Step 1 — Create session, send message, wait for agent response (reuse setup above).

Step 2 — Find agent response message:
```
GET /v1/chat-sessions/<session_id>/messages
→ find agent message: agent_message_id where author_type == "agent"
```

Step 3 — React to the message:
```
POST /v1/chat-sessions/<session_id>/messages/<agent_message_id>/reactions
Authorization: Bearer <token>
{
  "reaction": "thumbs_up"
}
→ 201
→ body.data.id is non-empty UUID (reaction_id)
→ body.data.reaction == "thumbs_up"
→ body.data.reactor_type == "human_user"
```

Step 4 — Verify reaction is stored:
```
GET /v1/chat-sessions/<session_id>/messages/<agent_message_id>
→ 200
→ body.data.reactions is an array
→ body.data.reactions length >= 1
→ body.data.reactions[0].reaction == "thumbs_up"
```

Step 5 — Delete reaction:
```
DELETE /v1/chat-sessions/<session_id>/messages/<agent_message_id>/reactions/<reaction_id>
→ 204
```

Step 6 — Verify reaction is gone:
```
GET /v1/chat-sessions/<session_id>/messages/<agent_message_id>
→ 200
→ body.data.reactions length == 0
```

**Scenario: `TestChat_CancelInFlightTurn`**

Step 1 — Reset, bootstrap, create session, add agent participant.

Step 2 — Open SSE stream.

Step 3 — Send a message that is expected to take time (in test mode, use a message
keyword that triggers a simulated delay, e.g., content starts with `[slow-response]`):
```
POST /v1/chat-sessions/<session_id>/messages
{ "content": "[slow-response] Analyze everything carefully.", "role": "human" }
→ 201
```

Step 4 — Wait for SSE event `turn.started`:
```
Wait for event type "turn.started" or "message.streaming" → within 10 seconds
→ extract turn_id from event data
```

Step 5 — Cancel the turn:
```
POST /v1/chat-sessions/<session_id>/cancel-turn
Authorization: Bearer <token>
{ "turn_id": "<turn_id>" }
→ 200 or 204
```

Step 6 — Wait for SSE cancellation event:
```
Wait for event type "turn.cancelled" → within 10 seconds
→ event data.turn_id == <turn_id>
```

Step 7 — Verify turn reaches cancelled state:
```
GET /v1/chat-sessions/<session_id>/turns
→ body.data includes entry with id == <turn_id> and status == "cancelled"
```

Step 8 — Verify session is still usable after cancellation:
```
POST /v1/chat-sessions/<session_id>/messages
{ "content": "Still there?", "role": "human" }
→ 201  (new message accepted)
```

### Must NOT build

- UI or TUI interactions
- Internal Go package calls
- Progressive summarization trigger tests (those require sending many messages and are
  tested in task 077 integration tests)
- Session mode switch tests (sync ↔ async) — separate from this scenario

## Acceptance Criteria

- [ ] `TestChat_CreateSessionAndSendMessage` passes: session created, agent added, message sent, turn created, agent responds via SSE, turn completes
- [ ] SSE stream delivers `turn.completed` event within 30 seconds of message send
- [ ] Agent response message has `state == "final"` and non-empty `content`
- [ ] `TestChat_ConversationHistory` passes: follow-up message produces second response; messages are in order
- [ ] `TestChat_Reaction` passes: reaction stored with correct `reactor_type`, deleted on request
- [ ] `TestChat_CancelInFlightTurn` passes: cancellation produces `turn.cancelled` SSE event; session remains usable
- [ ] All SSE events received within timeout; no test relies on sleep-based polling
- [ ] Full scenario completes in under 3 minutes

## Tests Required

**Unit tests:** None — this task IS the test suite.

**Integration tests:** None — this is an E2E test suite.

**E2E tests:**
- `TestChat_CreateSessionAndSendMessage` — full turn cycle with SSE delivery
- `TestChat_ConversationHistory` — follow-up message and chronological history
- `TestChat_Reaction` — reaction create and delete
- `TestChat_CancelInFlightTurn` — turn cancellation propagates via SSE; session remains usable

## Implementer Notes

**ISSUE #27 (path prefix):**
All API calls use `/v1/` paths and resource names from doc 12. Doc 21's `/api/sessions/`
examples are pseudocode only — use `/v1/chat-sessions/`.

**SSE client in tests:**
The `SSEClient` helper must open a real HTTP connection with `Accept: text/event-stream`.
Use Go's `net/http` client with `Response.Body` streamed line by line. Parse SSE fields
(id:, event:, data:) into the `SSEEvent` struct. The `WaitForSSEEvent` helper reads from
a buffered channel populated by a background goroutine reading the stream.

**Deterministic model responses in test mode:**
In `OTTERCAMP_MODE=test`, the model gateway returns deterministic responses without
calling a real provider. The `[slow-response]` prefix is a test-mode keyword that causes
the gateway to delay the response by 2 seconds, simulating an in-flight turn for the
cancellation test. The exact keyword syntax must be agreed with the model gateway
implementation (task 036).

**SSE scope filter:**
The SSE endpoint accepts `?scopes=session:<session_id>` to filter events to a specific
session. The test must include this filter to avoid receiving unrelated events from other
test scenarios running in parallel (if parallelism is enabled).

**Reaction uniqueness:**
Doc 02 says reactions are unique per participant per message. If the test sends the same
reaction twice from the same user, the second call should return 409 or be idempotent.
`TestChat_Reaction` only sends one reaction, so this edge case is not tested here.
