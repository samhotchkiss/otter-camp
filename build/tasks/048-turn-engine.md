# 048: Turn Execution Engine

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | M (1–2 days) |
| Spec refs | doc 02 §TurnCycle, doc 02 §TurnPhases, doc 02 §TurnCancellation, doc 02 §ListeningEval, doc 16 §AgentTurnLoop, doc 20 §TierDispatch |
| Spec status | finished |
| Depends on | 043, 044, 045, 047, 049, 050, 035, 036, 033, 024 |
| Blocks | 051, 072, 083, 087 |

## Scope

Build the agent turn execution engine: the orchestrator that receives a user message event,
runs the agent's turn through its full lifecycle (prompt assembly → model call → tool dispatch
loop → turn completion), handles cancellation and errors, and feeds memory confidence updates
from reactions. This is the heart of the system.

### Must build

**Turn Engine entry point:**
- `TurnEngine.HandleUserMessage(ctx, sessionID, messageID) error`
  - Triggered by consuming the `chat.message.user_sent` domain event from task 044.
  - Calls `ChatService.CreateTurn` to create a `chat_turn` row.
  - Starts the turn execution loop.
  - On any unrecoverable error: calls `ChatService.FailTurn` and appends a system message with
    the error summary.
  - This method runs in a dedicated worker goroutine; it must not block the event consumer loop.

**Turn execution loop:**
- Phase 1 — Context loading:
  1. Resolve the agent for the session (via `chat_participant` where `participant_type='agent'`).
  2. Load the resolved tool set (call `ToolResolver.GetSessionToolSet`, task 049).
  3. Assemble the prompt (call `PromptAssembler.Assemble`, task 050).
  4. Check if summarization is needed (call `SummarizationChecker.ShouldSummarize`, task 045);
     if true, enqueue a `chat_summarize` job and continue (do not block the turn on summarization).
  5. Append a `status='pending'` assistant message to the session as a placeholder for the response.

- Phase 1.5 — Listening eval (Haiku-class):
  - Before the main model call, if the agent's `mode='async'` OR the session has more than one
    pending human message in the queue, run a lightweight "listening eval":
    - Use the Haiku-class model profile (`invocation_purpose='listening_eval'`).
    - Prompt: "Is there more context incoming, or should I respond now?" with the last 3 human messages.
    - If the eval returns "wait", re-enqueue the turn after a short delay (500ms) and return.
    - If the eval returns "respond", continue to Phase 2.
  - For sync sessions with a single pending message, skip Phase 1.5.

- Phase 2 — Main model call (streaming):
  - Call `ModelGateway.StreamComplete` (task 035/036) with the assembled prompt, tool definitions,
    and the session's resolved model profile.
  - `invocation_purpose='agent_turn'`.
  - Update the placeholder assistant message: set `status='streaming'` as the first token arrives.
  - Forward each token to SSE fan-out via `domain_event`: publish `chat.message.chunk` events
    with `{message_id, delta: token_string, sequence: chunk_sequence}`.
  - **Steer detection**: after each model response chunk, check for new `role='user'` messages
    in the session with `created_at > turn.started_at` (steer messages from `ChatService.SteerTurn`).
    If a steer message exists, append it to the conversation context and continue streaming
    (do not restart the model call; inject the steer as a mid-stream user turn).
  - On streaming completion: finalize the assistant message (`status='final'`, full content set).
  - Emit `chat.turn.model_call_done` domain event.

- Phase 2 — Tool call dispatch:
  - If the model response includes tool calls, enter the tool dispatch loop:
    - **Stop conditions** (exit tool loop immediately):
      - `max_tool_calls` reached (configurable per agent, default 25). Append a system message:
        `"[Max tool calls reached. Turn ended.]"` and proceed to Phase 3.
      - `max_duration_ms` elapsed (configurable per agent, default 5 minutes for sync,
        30 minutes for async). Append: `"[Turn duration limit reached. Turn ended.]"`.
      - Token count is NOT a stop condition (doc 02 explicit). Context compression + continuation
        turn is used instead (see Continuation Turn below).
      - Cancellation requested (see Turn Cancellation below).
    - **Tier-1 tools** (chat-layer, low-risk): executed in parallel within a single tool call batch.
      - Call `ToolDispatcher.DispatchTier1(ctx, toolCalls)` which calls native tool implementations directly.
      - All tier-1 results collected, then fed back to the model as `role='tool_result'` messages.
    - **Tier-2 tools** (broker-dispatched, high-risk): executed sequentially, one at a time.
      - Call `ToolDispatcher.DispatchTier2(ctx, toolCall)` which creates a control plane `run`
        (task 051/052) and waits for completion.
      - Tier-2 dispatch is sequential: the next tier-2 call is not started until the previous
        `run` completes or fails.
    - If a single model response contains a mix of tier-1 and tier-2 tools: execute all tier-1
      first (parallel), then tier-2 one at a time.
    - After each tool result batch, re-invoke the model (go back to Phase 2 main model call)
      with updated conversation context.

- Phase 3 — Turn completion:
  1. Finalize the turn: call `ChatService.CompleteTurn`.
  2. Publish `chat.turn.completed` domain event.
  3. Trigger memory extraction asynchronously: publish `chat.turn.completed` event; the memory
     extraction worker (task 039) picks it up via job queue.
  4. Decrement `chat_session.turn_count` tracking (already incremented in CreateTurn — no action).

**Continuation turn** (context overflow recovery):
- Triggered when the prompt assembler (task 050) returns `ErrContextCompressed` indicating that
  even after compression the context is close to the model's maximum.
- Steps:
  1. Append a system message: `"[Context compressed — continuing in a new turn.]"`.
  2. Call `ChatService.CreateTurn` with the same `cycle_id` as the current turn (links the turns).
  3. Query Ellie (or the org's designated supervisor agent) with a summary request:
     `invocation_purpose='continuation_summary'` — a short summary of the work done so far and
     what remains. This summary is injected as a memory into the new turn's context.
  4. Begin the new turn's execution loop from Phase 1.

**Turn cancellation:**
- Cancellation is signaled by `chat.turn.cancelled` domain event (published by `ChatService.CancelCurrentTurn`).
- The turn engine listens for this event on a per-turn cancellation channel.
- On cancellation signal:
  1. Stop the model streaming call (cancel the HTTP request context).
  2. Stop any in-progress tier-1 tool calls.
  3. For any in-progress tier-2 tool call (control plane run): send a cancel request to the run
     (calls `RunService.CancelRun`, task 052).
  4. Finalize the current assistant message as `status='failed'` if it is still streaming;
     otherwise leave it as is if it already reached `status='final'`.
  5. Append a system message: `"[Turn cancelled by user.]"`.
  6. Call `ChatService.CancelTurn` to set the turn `status='cancelled'`.

**Reactions and memory confidence feedback:**
- Subscribe to `chat.reaction.added` domain events.
- When a reaction is added to a message that has `role='assistant'`:
  - Retrieve the `memory_source` records linked to the session (via `MemorySourceRepo.ListBySession`).
  - For memories extracted from messages in the same turn as the reacted message:
    - Positive reactions (👍, ✅, ❤️): increase `memory.confidence` by 0.05 (cap at 1.0).
    - Negative reactions (👎, ❌): decrease `memory.confidence` by 0.10 (floor at 0.0).
  - This update is best-effort: if no matching memories exist, no-op. No error returned.
  - Update is applied directly via `MemoryRepo.UpdateConfidence` (no new model call).

**Error handling and recovery:**
- Transient model gateway errors (network timeout, rate limit): retry up to 3 times with
  exponential backoff (1s, 2s, 4s). On third failure: fail the turn.
- Model streaming interrupted mid-stream: the partial content is preserved in the assistant
  message (whatever was received before the error), status set to `'failed'`.
- Tool dispatch failure (tier-1): the tool result message carries `{error: "tool_name failed: details"}`;
  model sees the error and can decide how to proceed or give up.
- Tool dispatch failure (tier-2): the control plane run status is `'failed'`; the run's
  `failure_reason` is returned as the tool result error; the model sees it.
- Maximum retry budget: 3 model-call retries per turn total (not per tool round-trip).

### Must NOT build

- Tool resolution pipeline (task 049)
- Prompt assembly (task 050)
- Control plane run creation (task 051)
- Memory extraction pipeline (task 039)
- Model gateway implementation (tasks 035, 036)
- Native tool implementations (tasks 056, 057)
- Chat service CRUD (task 044)

## Acceptance Criteria

- [ ] Turn engine processes `chat.message.user_sent` event, creates a `chat_turn` row, runs through Phases 1–3, and emits `chat.turn.completed` event for a simple (no-tool) exchange
- [ ] Tier-1 tools are dispatched in parallel within a single batch; a test with 3 tier-1 tools in one model response shows all 3 dispatched concurrently (verify via timing or mocked call order)
- [ ] Tier-2 tools are dispatched sequentially; a test with 2 tier-2 tools shows the second dispatch does not start until the first control plane run completes
- [ ] `max_tool_calls` stop condition: configure `max_tool_calls=3`, dispatch 4 tool calls → turn ends after 3 with system message `"[Max tool calls reached. Turn ended.]"`
- [ ] Cancellation during model streaming: cancel signal received mid-stream → model HTTP request cancelled, assistant message set to `status='failed'`, system message appended, turn `status='cancelled'`
- [ ] Cancellation during tier-2 tool run: cancel signal → `RunService.CancelRun` called with the in-progress run ID
- [ ] Reaction confidence feedback: add 👍 reaction to an assistant message → `memory.confidence` increased by 0.05 for linked memories; add 👎 → decreased by 0.10
- [ ] Phase 1.5 listening eval returning "wait" causes the turn to be re-enqueued; the turn does NOT proceed to Phase 2 immediately

## Tests Required

**Unit tests:**
- Phase detection: sync session with 1 pending human message → Phase 1.5 skipped; async session → Phase 1.5 runs
- Tool tier routing: model response with 2 tier-1 + 1 tier-2 tool calls → `DispatchTier1` called with 2 calls, `DispatchTier2` called once after tier-1 completes
- Stop condition — max_tool_calls: mock model to always return tool calls; verify engine exits after `max_tool_calls` iterations with correct system message
- Stop condition — max_duration: mock clock to advance past `max_duration_ms` after 2 tool rounds; verify engine exits
- Cancellation channel: send cancel signal on the per-turn channel during mocked model streaming; verify streaming cancelled and turn set to cancelled
- Reaction feedback — no linked memories: add reaction where no `memory_source` records exist → no error, no DB update
- Continuation turn: mock `PromptAssembler.Assemble` returning `ErrContextCompressed` → verify new turn created with same `cycle_id`, Ellie queried for summary

**Integration tests:**
- Full turn cycle with real DB: seed session + agent + message; run `HandleUserMessage`; verify: `chat_turn` created and completed, assistant message created with `status='final'`, `chat.turn.completed` event in `domain_event` table
- Tier-1 tool dispatch end-to-end: seed session; mock model to return a `memory.query` tool call; verify tool result appended and model called again with tool result
- Retry on transient error: mock model gateway to fail twice then succeed; verify turn completes and `model_invocation` shows 3 attempts
- Turn cancellation with active run: create a run in 'in_progress' state; send cancel signal; verify `RunService.CancelRun` called with correct run ID

**E2E tests:**
- None — covered by dedicated E2E task 072, 083, and 087

## Implementer Notes

- The turn engine worker model: there should be one `TurnEngine` goroutine pool consuming from the job queue (job_type: 'agent_turn'). The `chat.message.user_sent` event handler enqueues a `job_queue` row (job_type='agent_turn', payload={session_id, message_id}); the turn engine worker picks it up via SKIP LOCKED. This decouples the event consumer from the turn execution and provides natural backpressure.
- Per-turn cancellation: implement a `CancellationWatcher` goroutine that subscribes to `chat.turn.cancelled` events for the specific turn_id. Use a Go `context.WithCancel` derived from the session context. When the watcher receives the event, it cancels the derived context, which propagates to the model streaming HTTP request and all tool dispatch calls.
- The steer detection check inside the model streaming loop is a DB poll: `SELECT id FROM chat_message WHERE session_id=$1 AND role='user' AND created_at > $2 ORDER BY sequence_number DESC LIMIT 1`. This runs after each streaming chunk. To avoid DB hammering, debounce: only poll once every 10 chunks (approximately every second at typical streaming speeds).
- `DispatchTier1` and `DispatchTier2` are interfaces defined here; their implementations live in tasks 049 (tool resolution) and 055/056/057 (native tools + control plane). The turn engine depends on the interfaces; implementations are injected.
- Tier-2 tool dispatch creates a control plane `run`. The turn engine waits for the run to complete by polling `RunService.GetRun` every 500ms (or subscribing to `run.completed` / `run.failed` domain events, whichever is available when task 052 is built). Document both approaches and pick whichever the task 052 implementation exposes.

> ⚠️ ISSUE #18 (AMBIGUOUS): When a model invocation occurs during the agent turn loop (Phase 2, not inside a tier-2 control plane run), it is unclear whether `model_invocation.run_attempt_id` should be set. The turn loop may not have a `run_attempt_id` — it has only a `chat_turn.id`. For now, set `run_attempt_id=NULL` and `turn_id=chat_turn.id` for turn-loop model calls. Do not finalize this behavior until Sam resolves ISSUE #18.
