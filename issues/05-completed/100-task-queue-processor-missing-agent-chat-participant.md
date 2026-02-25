# Task 100: TaskQueueProcessor missing agent chat participant — agent_turn dead-letters

Layer: L2
Effort: S
Depends on: 097 (task queue processor)

## Context

When a project task with `assigned_agent_id` (no flow template) is queued, the `TaskQueueProcessor` correctly transitions it to `in_progress`, creates a run, and sends a kickoff message. However, the kickoff message triggers an `agent_turn` job that immediately dead-letters with `repo: not found` because the session has no agent participant.

Evidence from live session:
- Task created with `assigned_agent_id=<frank>`, status transitions to `in_progress` ✓
- Run created (status: `in_progress`) ✓
- `agent_turn` job created ✓
- `agent_turn` job dead-letters with error: `repo: not found`
- Root cause: `resolveSessionAgent` in `internal/turn/engine.go` queries `chat_participant` table for `participant_type='agent'` — returns `ErrNotFound` because no participant was added

## Root Cause

In `internal/controlplane/task_queue_processor.go`, `ensureAssignedAgentRun`:

```go
// Creates session ✓
session, err := p.chats.CreateSession(ctx, chat.CreateSessionInput{...})

// Creates run ✓
runRecord, err := p.runs.CreateRun(ctx, CreateRunInput{...})

// Sends kickoff message ✓ (triggers agent_turn job)
p.chats.AppendMessage(ctx, chat.AppendMessageInput{Role: "user", ...})

// MISSING: add agent as participant ✗
// p.chats.AddParticipant(ctx, session.ID, "agent", *taskRecord.AssignedAgentID, "responder")
```

`resolveSessionAgent` in `internal/turn/engine.go` (~line 1227) requires a `chat_participant` record with `participant_type='agent'` in order to determine which agent should respond.

## Required Fix

In `ensureAssignedAgentRun`, after creating (or reusing) the session, add the assigned agent as a participant with role `"responder"`:

```go
if _, err := p.chats.AddParticipant(ctx, session.ID, "agent", *taskRecord.AssignedAgentID, "responder"); err != nil {
    // ignore already-exists errors
    if !errors.Is(err, chat.ErrAlreadyExists) {
        return err
    }
}
```

The `taskQueueChatService` interface needs to include `AddParticipant`. The `TaskQueueProcessorOptions.Chats` field should accept a service that implements this method.

## Acceptance Criteria

- [ ] `ensureAssignedAgentRun` calls `AddParticipant(session.ID, "agent", assignedAgentID, "responder")` before sending kickoff message
- [ ] `agent_turn` job completes successfully (status: done) for task-triggered sessions
- [ ] Agent responds to the kickoff message in the task session
- [ ] Idempotent: re-running for an existing session with an existing participant doesn't fail
- [ ] `go build ./...` passes

## Required Tests

- Integration: task with assigned_agent_id queued → in_progress → agent_turn completes (not dead_letter)
- Unit: `ensureAssignedAgentRun` adds agent participant before sending kickoff message
