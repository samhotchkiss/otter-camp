# 051: Chat CLI Commands

| Field | Value |
|-------|-------|
| Layer | L4 |
| Size | S (≤1 day) |
| Spec refs | doc 11 §CLIChatCommands, doc 12 §ChatAPI, doc 08 §CLIReference |
| Spec status | finished |
| Depends on | 046, 047, 068 |
| Blocks | 072, 083 |

## Scope

Build the `ottercamp chat` subcommand group: `start`, `send`, `list`, and `history`.
These are thin CLI wrappers over the chat API endpoints (task 046); no direct DB access.
All output follows the three output modes: table, json, quiet.

### Must build

**`ottercamp chat start`** — create a new chat session and optionally open an interactive session:
- Flags:
  - `--scope-type string` — one of `organization`, `project`, `project_task` (default `organization`)
  - `--scope-id uuid` — the scope entity UUID; required if `--scope-type` is not `organization`
  - `--mode string` — `sync` or `async` (default `sync`)
  - `--title string` — optional session title
  - `--interactive` (`-i`) — after creating the session, enter interactive chat mode (see below)
- Calls: `POST /v1/chat-sessions`
- Output (table mode):
  ```
  Session created
  ID:    <uuid>
  Scope: project/<uuid>
  Mode:  sync
  Title: (none)
  ```
- Output (json mode): raw `{data: ChatSession}` response from the API
- Interactive mode (`--interactive`):
  - Prints a prompt `You: ` and reads lines from stdin.
  - Each line is sent via `POST /v1/chat-sessions/:id/messages`.
  - Connects to `GET /v1/events/stream?scopes=session:<id>` to stream the agent's response
    in real-time. Prints agent responses prefixed with `Agent: ` as SSE `chat.message.chunk`
    events arrive.
  - `Ctrl+C` sends `POST /v1/chat-sessions/:id/cancel-turn` then exits interactive mode.
  - `Ctrl+D` (EOF) exits interactive mode cleanly (no cancellation).
  - Interactive mode is only available in sync mode sessions. If `--mode=async`, exit with error:
    `"Interactive mode requires --mode=sync"`.
  - Not available in `--output=json` or `--output=quiet` mode.

**`ottercamp chat send`** — send a single message to an existing session:
- Args: `<session-id>` (required), `<message>` (required, positional; can also be piped via stdin)
- Flags:
  - `--wait` — block until the agent's turn completes and print the response (polls SSE until
    `chat.turn.completed` event); default: false (fire-and-forget)
  - `--timeout duration` — timeout for `--wait` mode; default 5 minutes
- Calls: `POST /v1/chat-sessions/:id/messages`
- Output (table mode, without --wait):
  ```
  Message sent
  Message ID: <uuid>
  Sequence:   <n>
  Status:     pending
  ```
- Output (table mode, with --wait):
  ```
  Message sent. Waiting for response...

  Agent: <full response text>

  Turn completed in 3.2s
  ```
- Output (json mode): the `{data: ChatMessage}` response (the sent message); if `--wait`, also
  include `{response_message: ChatMessage}` in the data object.
- Stdin piping: `echo "What is 2+2?" | ottercamp chat send <session-id>` reads the message
  from stdin if no positional `<message>` arg is provided.

**`ottercamp chat list`** — list chat sessions for the org:
- Flags:
  - `--status string` — `active`, `closed`, `archived`, or `all` (default `active`)
  - `--scope-type string` — filter by scope type
  - `--scope-id uuid` — filter by scope entity
  - `--limit int` — max results (default 20)
- Calls: `GET /v1/chat-sessions?status=...&scope_type=...&scope_id=...&limit=...`
- Output (table mode):
  ```
  ID                                   SCOPE           MODE   STATUS  MESSAGES  LAST ACTIVITY
  <uuid>                               project/<uuid>  sync   active  42        2 minutes ago
  <uuid>                               org             sync   active  7         1 hour ago
  ```
  - Uses relative time for `LAST ACTIVITY` (e.g. "2 minutes ago", "yesterday")
- Output (json mode): raw `{data: [ChatSession], meta: {...}}` response

**`ottercamp chat history`** — fetch and display message history for a session:
- Args: `<session-id>` (required)
- Flags:
  - `--limit int` — max messages to fetch (default 50)
  - `--before-sequence int` — fetch messages before this sequence number (for pagination)
  - `--format string` — display format: `compact` (default) or `full` (includes metadata)
  - `--roles string` — comma-separated role filter: `user,assistant` (default all roles)
- Calls: `GET /v1/chat-sessions/:id/messages?limit=...&before_sequence=...`
- Output (table mode, compact format):
  ```
  #  AUTHOR         ROLE       CONTENT (first 120 chars)
  1  human@email    user       What is the status of the deployment?
  2  Frank (agent)  assistant  The deployment completed successfully at 14:32 UTC. The service is...
  3  (system)       system     [Turn cancelled by user.]
  ```
  - Agent names resolved by fetching participant list (`GET /v1/chat-sessions/:id/participants`)
  - Redacted messages shown as: `[redacted]`
- Output (table mode, full format): each message on multiple lines with all fields
- Output (json mode): raw `{data: [ChatMessage], meta: {...}}` response

**Auth and configuration:**
- All commands use the standard auth from task 068: `--api-key` flag, `OTTERCAMP_API_KEY` env
  var, or `~/.ottercamp/credentials` file.
- Default server URL: `http://localhost:4110` (from task 068 config).
- `--server` flag overrides the default URL (inherited from global flags in task 068).

**Error handling:**
- 409 from `chat start` (active sync session exists) → human-readable: `"Error: An active sync session already exists for this scope. Use 'ottercamp chat list' to find it, or use --mode=async."`
- 404 from `chat send` (session not found) → `"Error: Session <id> not found."`
- `--wait` timeout → `"Error: Timed out waiting for agent response after 5m0s. The turn may still be in progress; use 'ottercamp chat history <id>' to check."`

### Must NOT build

- Chat service logic (task 044)
- Chat API handlers (task 046)
- SSE implementation (task 047) — `chat start --interactive` and `chat send --wait` are consumers of SSE, not implementers
- General CLI framework (task 068)
- Other subcommand groups (ottercamp agent, ottercamp project, etc.)

## Acceptance Criteria

- [ ] `ottercamp chat start --scope-type project --scope-id <uuid>` creates a session and prints the session ID in table mode
- [ ] `ottercamp chat start --interactive` enters interactive mode and prints `You: ` prompt; Ctrl+C sends a cancel-turn request before exiting
- [ ] `ottercamp chat start --mode=async --interactive` exits with error `"Interactive mode requires --mode=sync"`
- [ ] `ottercamp chat send <id> "hello"` returns immediately with status='pending' (does not block)
- [ ] `ottercamp chat send <id> "hello" --wait` blocks until `chat.turn.completed` SSE event and prints agent response text
- [ ] `echo "hello" | ottercamp chat send <id>` reads message from stdin
- [ ] `ottercamp chat list --output=json` returns valid JSON matching the `{data, meta}` envelope
- [ ] `ottercamp chat history <id>` shows redacted messages as `[redacted]` in table mode

## Tests Required

**Unit tests:**
- Flag parsing: `chat start --scope-type=project` with no `--scope-id` → validation error before API call
- Interactive mode guard: `chat start --mode=async --interactive` → error before any API call
- Error mapping: mock API returning 409 with `error_code='active_sync_session_exists'` → human-readable error string output
- `chat history` redacted display: mock message with `is_redacted=true` → output shows `[redacted]`
- `chat list` relative time: mock `last_message_at = now() - 2 minutes` → displays "2 minutes ago"

**Integration tests:**
- `chat start` round-trip: run against a real test server (OTTERCAMP_MODE=test); create session → verify `GET /v1/chat-sessions` includes it; run `ottercamp chat list` → session appears in table output
- `chat send --wait` SSE consumer: run against test server; send message; mock turn engine to publish `chat.turn.completed` event; verify `--wait` unblocks and prints response
- `chat history` pagination: seed session with 75 messages; run `ottercamp chat history <id> --limit=50` → 50 messages returned; run with `--before-sequence=50` → 25 messages returned

**E2E tests:**
- None — covered by dedicated E2E task 083

## Implementer Notes

- Interactive mode SSE connection: use the standard `http.Get` with `Accept: text/event-stream`
  header. Parse SSE frames manually (read lines, parse `id:`, `event:`, `data:` fields). The
  Go standard library does not have an SSE client; write a minimal one (50–80 lines). For
  `chat.message.chunk` events, print the `delta` field directly to stdout without a newline.
  Print a newline after `chat.turn.completed` to end the response line.
- `chat send --wait` timeout: use `context.WithTimeout(ctx, timeout)`. When the context
  deadline is exceeded while waiting for the SSE event, print the timeout error and exit 1.
- The `chat history` command resolves agent names by calling `GET /v1/chat-sessions/:id/participants`
  once before listing messages, then building a `map[uuid.UUID]string` (participant_id → display name).
  Participant fetch failure is non-fatal: fall back to displaying `agent/<uuid>` as the name.
- `--interactive` mode should disable the global `--output=json` and `--output=quiet` flags (or
  ignore them) because interactive mode always uses the interactive terminal format. Print a
  warning if the user passes both `--interactive` and `--output=json`.
- For the table output of `ottercamp chat history`, truncate message content at 120 characters
  and append `...` if truncated. Use `--format=full` to see the entire content.
