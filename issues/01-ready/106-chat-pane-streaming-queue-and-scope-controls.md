# Task 106: Chat pane streaming, queue controls, and scope switching

Layer: L2
Effort: L
Depends on: 103, 105

## Context

Doc 17 defines chat as the primary interaction surface: streaming token deltas, inline tool-call visibility, queued message controls while turns are active, and scope switching between org/project/task sessions.

## Required Fix

Implement core chat-pane behavior:

- Render message history with role-aware styling and markdown output (Glamour).
- Implement message input behaviors:
  - `Enter` send
  - `Shift-Enter`/`Alt-Enter` newline
  - history recall (`Up` when input empty)
  - `@` mention autocomplete
- Implement realtime streaming:
  - apply `chat.message.delta` incrementally
  - finalize on `chat.message.finalized`
  - show active-turn indicator and inline tool-call status
- Implement queued message workflow when a turn is active:
  - queue additional sends
  - actions: `e` edit, `s` steer/promote, `d` delete
  - `Escape` cancels current turn
- Implement scope switching controls for active chat:
  - `[` / `]` switch scope level
  - `:scope org|project|task` command palette equivalent

## Acceptance Criteria

- [ ] Chat deltas stream smoothly and finalize correctly
- [ ] Tool call lines render inline with compact status
- [ ] Queue actions (`edit`, `steer`, `delete`) work while turn is in progress
- [ ] Cancel (`Escape`) interrupts active turn and state remains consistent
- [ ] Scope switching updates active session without changing main-content view
- [ ] `go build ./...` passes

## Required Tests

- Unit: chat reducer tests for delta/finalize sequencing
- Unit: queued-message state machine tests (`send`, `edit`, `steer`, `delete`, `cancel`)
- Integration: end-to-end chat stream simulation with tool-call events
- Integration: scope switch preserves main-view state while changing chat session
