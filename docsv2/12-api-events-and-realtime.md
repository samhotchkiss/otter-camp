# 12. API, Events, and Realtime Contracts

## API Shape

- REST for CRUD and command endpoints.
- Realtime channel (WebSocket or SSE) for session/task updates.
- Internal job API for asynchronous orchestration.

## Contract Principles

- Stable IDs and explicit versioning.
- Idempotency keys for mutation endpoints.
- Consistent error shape and error codes.

## Key Endpoint Families

- Auth and org membership
- Chat sessions/messages/participants
- Projects/tasks/flow/templates/blockers
- Agents and role assignments
- Memory query and feedback
- Model profiles and routing policy
- MCP connections and tool execution
- Skills registry and attachments
- System execution (CLI/browser)

## Event Types (Examples)

- `chat.message.created`
- `chat.turn.completed`
- `task.flow.advanced`
- `task.blocker.raised`
- `task.approval.changed`
- `agent.run.started`
- `agent.run.completed`
- `model.request.failed`

## Job Model

- Durable run records with status.
- Retry policy with backoff.
- Dead-letter queue with operator tooling.

## Open Questions

- WebSocket vs SSE for default realtime transport?
- Do we expose a public webhook system in v1 or v2.1?
- How strict should API version compatibility be between self-host and cloud?

