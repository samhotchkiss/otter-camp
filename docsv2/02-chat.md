# 02. Chat Spec

## Core Requirements

- Any chat can contain any number of humans.
- Any chat can contain any number of agents.
- Session state and context are managed directly by OtterCamp.
- Preserve append-only trace semantics similar to JSONL logs.

## Core Entities

- `chat_session`
- `chat_participant`
- `chat_message`
- `chat_turn`
- `chat_artifact`
- `chat_summary`

## Message Model

- Roles: `human`, `agent`, `system`, `tool`
- Message states: `pending`, `streaming`, `final`, `failed`, `redacted`
- Attachments/artifacts linked via object storage references.

## Participant Model

- Human participants are identified by user ID.
- Agent participants are identified by agent ID.
- Permissions include read/write/mention/invite/remove/moderate.

## Session Log Format

- Canonical record in DB.
- Optional JSONL export/import format for debugging and portability.
- JSONL line types: `message`, `tool_call`, `tool_result`, `summary`, `checkpoint`, `event`.

## Context Management

- Per-session rolling context window.
- Periodic summarization checkpoints.
- Retrieval augmentation from project data, memory, and linked artifacts.
- Token budget policy per turn.

## Turn Execution Pipeline

1. Accept inbound message.
2. Resolve conversation participants and permissions.
3. Build model input context.
4. Route to model profile.
5. Stream output.
6. Execute allowed tool calls.
7. Persist final turn and artifacts.
8. Emit events to subscribers.

## Multi-Party Behaviors

- Mention routing: `@agent` and `@human` mentions.
- Round-robin or directed responder policy.
- Optional moderator policy for large chats.

## Safety and Moderation

- Configurable content policy checks before external model calls.
- Redaction pipeline for sensitive output.
- Admin controls for retention and legal hold.

## Open Questions

- Should every message have exactly one author, or support co-authored agent outputs?
- Should we support hard forks/branching inside a session?
- Should summaries be immutable snapshots or replaceable revisions?

