# Task 102: Flow run kickoff missing — no agent_turn triggered for flow template tasks

Layer: L2
Effort: M
Depends on: 097 (task queue processor), 100 (agent participant fix)

## Context

When a task with a `flow_template_id` is queued, `TaskQueueProcessor.ensureFlowRun` correctly:
1. Calls `StartFlow` to create a `flow_node_execution`
2. Creates a run with `CreateRun` (which calls `RouteRunToSession` to append a `tool_result` log-link message)
3. Calls `StartRun` (which publishes `run.started`)

However, **no `agent_turn` job is ever triggered** for the flow task. The run advances to `in_progress` and stays there indefinitely.

Evidence from live session:
- Task created with `flow_template_id=d30bcce9` (`default-single-agent`), `assigned_agent_id=Frank`
- Task transitions to `in_progress` ✓
- `flow_node_execution` created ✓
- Run created (status: `in_progress`) ✓
- Session created (no participants) ✓
- NO `agent_turn` jobs created — zero rows in `job_queue` for this session
- `run.started` event published but **no subscriber** in the worker

Two root causes:

### Root Cause 1: `run.started` event has no subscriber

`internal/controlplane/service.go:StartRun` publishes `run.started`, but no component in the worker subscribes to this event. The event is dead.

### Root Cause 2: No kickoff message sent for flow path

`ensureFlowRun` calls `RouteRunToSession` (via `CreateRun`) which appends a `tool_result`-role message to the session. The turn engine (`SubscribeUserMessageEnqueue`) only triggers `agent_turn` jobs for `user`-role messages. So the `tool_result` message does not trigger any agent work.

Contrast with `ensureAssignedAgentRun` which explicitly sends a `user`-role kickoff message.

### Root Cause 3: No agent participant added to flow session

`EnsureNodeSession` (called by `RouteRunToSession`) creates an async chat session but never adds an agent as a participant. Even if a `user` message were sent, `resolveSessionAgent` would return `ErrNotFound`.

## Required Fix

In `internal/controlplane/task_queue_processor.go`, `ensureFlowRun` must:

1. After creating the flow run, resolve the agent for the current flow node:
   - If `taskRecord.AssignedAgentID` is set and the flow node's `actor_type` is `"role"` or `"agent"`, use `taskRecord.AssignedAgentID`
   - Otherwise look up the flow node's actor configuration to find the assigned role, then resolve to an agent ID

2. Get or create the node session (via `flowSessionBridge.EnsureNodeSession`):
   ```go
   session, err := p.flowSessionBridge.EnsureNodeSession(ctx, execution)
   ```

3. Add the agent as a participant to the session:
   ```go
   p.chats.AddParticipant(ctx, session.ID, "agent", agentID, "responder")
   ```

4. Send a kickoff `user`-role message to the session (similar to `ensureAssignedAgentRun`):
   ```go
   p.chats.AppendMessage(ctx, chat.AppendMessageInput{
       SessionID: session.ID,
       Role:      "user",
       Content:   buildFlowKickoffMessage(taskRecord, flowNodeExecution),
       Metadata:  ...,
   })
   ```

`TaskQueueProcessor` needs access to `FlowSessionBridge` or a similar interface. Add it to `TaskQueueProcessorOptions`.

Note: `ensureFlowRun` already has the execution record and `taskRecord.AssignedAgentID` (set when the task has an assigned agent, even with a flow template). For simplicity, use `AssignedAgentID` when available.

## Acceptance Criteria

- [ ] Flow template task queued → in_progress → `agent_turn` job created and completed
- [ ] Session has at least one agent participant before kickoff message is sent
- [ ] Agent responds to kickoff message in the flow node session
- [ ] Idempotent: re-processing same task event doesn't create duplicate sessions or participants
- [ ] Non-flow (assigned-agent-only) tasks still work correctly (regression test)
- [ ] `go build ./...` passes

## Required Tests

- Integration: task with `flow_template_id` queued → `agent_turn` job completed (not dead_letter)
- Integration: flow node session has agent participant after task queue processing
- Unit: `ensureFlowRun` adds agent participant and sends kickoff message
- Unit: kickoff message is idempotent (existing kickoff for same run is not re-sent)
