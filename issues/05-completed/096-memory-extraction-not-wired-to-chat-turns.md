# Task 096: Wire memory extraction to chat.turn.completed event

Layer: L2
Effort: S
Depends on: none

## Context

Per the memory spec (doc 06), Ellie's primary memory capture path is implicit: she subscribes to the event bus and extracts structured memory candidates from conversations after each turn. The spec states: "Ellie subscribes to the event bus and extracts structured memory candidates from system events without any agent needing to decide what to save."

The turn engine already publishes the right event:
```go
// internal/turn/engine.go:736
return e.publishEvent(ctx, rt.session.OrganizationID, "chat.turn.completed", ...)
```

The memory extractor (`internal/memory/extractor.go`) has `ExtractFromMessages` which takes messages and extracts memory candidates via LLM.

However, `internal/memory/event_consumer.go` only subscribes to:
1. `task.completed` / `task.status_changed` (→ `to_status = done`) — triggers task consolidation
2. `agent.friction_signal` — triggers sleep reflection

There is NO subscription to `chat.turn.completed`. Memory extraction never runs after agent turns. Verified live: 0 memory items after Frank responded to a message successfully.

## Required Fix

Add a `SubscribeTurnCompleted` method to `memory.EventConsumer` that:

1. Subscribes to `chat.turn.completed` events
2. On each event, enqueues a job (e.g., `memory_extract_turn`) with the turn/session info
3. The job handler fetches the turn's messages, calls `extractor.ExtractFromMessages`, stores any extracted memories

The job should be idempotent (skip if already extracted for this turn).

Wire up the subscription in `internal/worker/worker.go` alongside the existing `SubscribeTaskCompleted` and `SubscribeFrictionSignals` calls.

Example subscription in `event_consumer.go`:

```go
func (c *EventConsumer) SubscribeTurnCompleted(orgID *uuid.UUID) eventbus.Subscription {
    return c.events.Subscribe("memory.turn-completed", orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
        if event.EventType != "chat.turn.completed" {
            return nil
        }
        // parse session_id, turn_id from payload
        // enqueue memory_extract_turn job
        return c.enq.Enqueue(ctx, nil, MemoryExtractTurnJobType, 50, payload, nil)
    })
}
```

The job handler should:
1. Fetch messages for the turn from `chat_message` (role=agent, status=final)
2. Build `[]memory.ChatMessage` from those records
3. Call `extractor.ExtractFromMessages(ctx, orgID, msgs, ExtractionSourceContext{...})`
4. Handle errors gracefully (don't fail the job if LLM extraction returns 0 candidates)

## Acceptance Criteria

- [ ] After an agent turn completes (Frank responds to a message), a `memory_extract_turn` job is enqueued
- [ ] The job runs and calls `ExtractFromMessages` with the turn's messages
- [ ] If extraction yields candidates, they appear in `SELECT * FROM memory ORDER BY created_at DESC LIMIT 5`
- [ ] Empty turns (model returned no text) are handled gracefully (no error)
- [ ] `go build ./...` passes

## Required Tests

- Integration: send a message, get a response, wait for job processing, assert memory rows are created
- Unit: event consumer subscription filters non-turn events correctly
