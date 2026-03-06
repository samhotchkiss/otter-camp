---
## Summary

This spec defines OtterCamp's chat system, which is the primary interface for all human-agent and agent-agent interaction. Chat sessions are scoped to one of three levels -- org, project, or task -- and each scope gets exactly one persistent human-facing session (org has "General" with Frank, each project has one PM session, each task has one sync discussion session). Additionally, per-node async sessions are auto-created when agentic flow nodes execute, serving as agent work logs rather than human-facing chat. Sessions operate in either synchronous mode (human present, latency-sensitive, pre-assembled context) or asynchronous mode (autonomous agent work, quality-focused, tool-based discovery). Messages use a rich content block model (text, code, image, file_ref, artifact) stored as jsonb, with roles of human, agent, system, tool_call, and tool_result. Messages are append-only and follow strict state transitions (pending, streaming, final, failed, redacted).

The core mechanic is the turn loop: a unified tool-call loop with two entry points (sync via human message, async via system kick). An agent reasons and acts through tool calls until it produces a final text response with no tool calls. Tools are split into two tiers -- Tier 1 (read-only, chat-layer, fast) and Tier 2 (mutations/external via control plane with full policy evaluation). Policy checks never block mid-turn; they return allow or deny immediately. Communication tools that stage drafts do so as their designed tool behavior, not as a policy interception. Multi-party turn sequencing follows an atomic turn cycle: Phase 1 (responder turn), Phase 1.5 (agent-to-agent @mentions, one round only), Phase 2 (lightweight listening eval for non-responding agents), and Phase 3 (interjection turns from agents flagged in eval). Each session has a default responder (Frank for org, PM for project/task), and @mentions override who responds.

The database schema centers on six tables: chat_session (scoped, with sync/async mode), chat_participant (with roles: default_responder, participant, listener), chat_turn (with cycle_id grouping turns per human message, trigger types, stop conditions), chat_message (one row per logical unit with sequence ordering and ephemeral flag for coordination noise), chat_artifact (object storage references for files/images), and chat_summary (progressive summarization covering message ranges). Supporting tables include chat_read_cursor for unread tracking and chat_message_reaction for bidirectional positive/negative sentiment signals that feed into Ellie's memory pipeline. Context assembly uses a 7-layer prompt pipeline run once per turn start, with progressive summarization to manage conversation history as sessions grow. The UI features a persistent chat pane (right panel) with a scope pill for zooming between task/project/org levels, a session sidebar (left), and main content (center), all independently navigable.

---

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

- Roles: `human`, `agent`, `system`, `tool_call`, `tool_result`
- Message states: `pending`, `streaming`, `final`, `failed`, `redacted`
- Each message has exactly one author (no co-authored outputs).
- Content is stored as jsonb. Structure varies by role — the application layer interprets based on role.
- Attachments/artifacts linked via object storage references.

### Rich Content

Messages support structured content blocks, not just plain text. The `content` jsonb field contains a `blocks` array:

```json
{
  "blocks": [
    {"type": "text", "text": "Here's the implementation:"},
    {"type": "code", "language": "go", "text": "func main() {...}"},
    {"type": "text", "text": "I've generated an architecture diagram:"},
    {"type": "image", "artifact_id": "uuid-of-diagram"},
    {"type": "file_ref", "path": "src/auth/handler.go", "ref": "abc123"}
  ]
}
```

**Block types:**

- `text` — markdown text, rendered with formatting.
- `code` — syntax-highlighted code with language tag.
- `image` — reference to a `chat_artifact` (generated diagram, chart, screenshot). Rendered inline.
- `file_ref` — reference to a file in the project's git repo, optionally at a specific commit/ref. Rendered as a clickable link with preview. (Projects are git repos — see 03-projects-and-task-flow.md.)
- `artifact` — reference to a `chat_artifact` (a produced file, document, etc.). Rendered as a downloadable/previewable embed.

**Human messages** use the same block model. A message with an uploaded file has a `text` block plus an `artifact` block referencing the upload. The uploaded file is stored in object storage via `chat_artifact`.

**Agent messages** produce blocks during streaming. Text blocks stream token by token. Other block types (code, image, file_ref, artifact) are emitted as complete blocks.

**Tool call and tool result messages** continue to use their existing content structure (not blocks). Rich content is for human-facing messages, not for the tool protocol.

### File Uploads

Humans can upload files into chat (drag-and-drop or attach button):

1. File uploads to object storage.
2. A `chat_artifact` record is created (name, content type, size, storage path).
3. The human's message content includes an `artifact` block referencing it.
4. When the turn processes, the prompt assembly layer decides how to include the file based on type and model capabilities:
   - **Images**: included via vision if the model supports it.
   - **Text files** (code, docs, config): contents extracted and included as context.
   - **Binary files** (PDFs, spreadsheets): referenced but may not be directly readable depending on tool support.

## Participant Model

- Human participants are identified by user ID.
- Agent participants are identified by agent ID.
- Participant roles: `default_responder`, `participant`, `listener`.
- Permissions include read/write/mention/invite/remove/moderate.

## Session Log Format

- Canonical record is PostgreSQL (see Storage and Database Schema sections below).
- Optional JSONL export/import format for debugging and portability.
- JSONL line types: `message`, `tool_call`, `tool_result`, `summary`, `checkpoint`, `event`.

## Session Scoping

Every chat session has exactly one scope. The scope determines what context is injected, what agents are available, and what actions are possible.

### Scope Levels

- **Org scope**: General-purpose conversations not tied to a specific project or task. Brainstorming with Frank, asking Lori to hire a new staff agent, asking Ellie about org-wide knowledge. Context injected: org-level memory, project summaries.
- **Project scope**: Conversations about a project as a whole — blocker triage, progress reviews, staffing decisions, quick questions to the PM. Context injected: project memory + org memory, task summaries for the project. Substantive discussions (architecture, sprint planning, design reviews) should be tasks, not project-level chat.
- **Task scope**: Conversations about a specific unit of work. Discussing requirements, reviewing deliverables, giving feedback, resolving blockers. Context injected: task structured context (files, decisions, acceptance criteria) + project memory + org memory.

### Session Hierarchy

Each scope has exactly one human-facing session. No ambiguity about "which session to open."

- **Org**: one persistent session ("General"). Always there. Talk to Frank.
- **Project**: one persistent session per project. PM's home base for that project. Triage, oversight, quick questions.
- **Task**: one persistent sync session per task. Where the human discusses the work — requirements, feedback, review. Spans the full task lifecycle.

In addition, **per-node async sessions** are created automatically when a flow node begins execution. These are the agent's workspace — where autonomous work happens. They are NOT human-facing chat. They are execution traces (work logs) viewable from the task detail view. They close when the flow node completes.

### Session Titles

Since there is one session per scope, the title is derived from the scope:

- Org session: "General" (or the org name)
- Project session: the project name
- Task sync session: the task title

No auto-generation or model calls needed. The `chat_session` table has an optional `title` field (nullable) — when null, the UI displays the scope name. The human can override the title if they want.

Discoverability beyond the sidebar is handled by the command bar (Superhuman-style fuzzy search across sessions, projects, and tasks).

### Binding Rules

- A session's scope is set at creation and does not change.
- `chat_session` carries `scope_type` (org, project, task) and `scope_id` (the org, project, or task ID).
- Each task has one persistent sync session (the discussion session) plus zero or more per-node async sessions (work logs).
- Each project has one persistent session.
- Each org has one persistent session ("General").
- UI clients must preserve the task discussion binding when the operator opens a task from its existing sidebar task chat. If a task detail payload omits `discussion_session_id`, the client falls back to the already-known task-scoped sync session instead of inventing an empty state.
- Empty-state copy claiming a task has no discussion session is only valid when neither task detail data nor prior task-scoped session context can resolve a sync discussion session.

### Scope and Agent Availability

- **Org session**: starter trio (Frank, Lori, Ellie) and any org-level staff agents.
- **Project session**: starter trio + project-assigned staff agents + project temps.
- **Task sync session**: PM (default responder) + agents assigned to the task + Ellie. Specific agents can be @mentioned.
- **Per-node async sessions**: the agent assigned to that flow node + Ellie.

### Ellie's Dual Role

Ellie is both a background system and a conversational participant.

- **Passive**: the memory pipeline automatically injects relevant memories into session context based on scope. This happens on every turn without Ellie being explicitly invoked.
- **Active**: any agent or human can @mention Ellie to explicitly request deeper context, ask "what do we know about X?", or request memory from a scope they don't normally see. Ellie responds as a participant in the conversation.

### Agent Concurrency Across Sessions

Agents can participate in multiple sessions and handle turns concurrently. Each turn is an independent model call — there is no shared mutable state between sessions. Two simultaneous Frank turns in different sessions are just two model calls using the same agent profile.

Concurrency is governed by the existing infrastructure (see 07-models-and-inference.md):

- **Global concurrency limits**: cap on total concurrent LLM calls across all agents and sessions.
- **Per-provider limits**: rate limits for the underlying model provider.
- **Priority**: sync sessions (human waiting) get priority over async (autonomous work). If the system is at capacity, async turns queue.

No per-agent serialization is needed. If an agent has private memory and two concurrent turns both write to it, the memory pipeline handles concurrent writes (see 06-memory.md).

### Task and Async Session Ownership

Cross-session concurrency is allowed, but a single task/session execution boundary still has exactly one active execution owner at a time. For task-scoped async work and per-node async sessions:

- Repeated wakeups for the same owner merge into the active execution instead of starting another overlapping run.
- Wakeups for a different owner are recorded and deferred until the current owner exits, yields, or is declared stale by the control plane.
- Deferred wakeups resume by promoting the queued run and appending exactly one kickoff message for the resumed owner.
- The control plane persists a runtime-state contract for that boundary with the task/session/flow binding ids, last progress timestamp, pending deferred wakeup info, and whether the current failure is resumable or terminal.
- Task completion, task cancellation, and project archive retire the runtime-state record so archived or finished work cannot be resumed accidentally.

This means "agents can work concurrently" applies across different sessions, not as parallel overlapping execution inside the same task runtime boundary.

## Session Modes

Sessions operate in one of two modes, which determines context assembly strategy and latency expectations.

### Synchronous (human-in-the-loop)

Used when a human is actively participating — chatting with Frank, reviewing work with an agent, making decisions. Latency matters.

- **Identity/personality**: pre-loaded, always in the prompt.
- **Memory**: Ellie injects top-k results automatically per turn (passive retrieval, must be fast).
- **Task/project context**: pre-assembled structured context included at session start.
- **Token budget**: tight — context must be curated to avoid waste.

### Asynchronous (autonomous agent work)

Used when an agent is executing a task autonomously. The human is not waiting. Quality and thoroughness matter more than speed.

- **Identity/personality**: pre-loaded, always in the prompt.
- **Memory**: agent can actively query Ellie across multiple turns, doing deeper retrieval as needed.
- **Task/project context**: agent can discover context on its own via tools — reading files, searching code, exploring the codebase. No need to pre-assemble everything.
- **Token budget**: relaxed — agent can use many turns, and context resets between flow steps.

### Mode Assignment

- Org-scoped and project-scoped sessions default to **synchronous** (human is likely present).
- Task-scoped sessions created by agentic flow nodes default to **asynchronous**.
- Mode can be overridden (e.g., a human joins a task-scoped session to pair with the agent — switches to synchronous behavior).

## Turn Loop

The tool-call loop IS the agentic loop. There is one loop with two entry points (sync vs async). The mechanics are identical regardless of mode.

### Core Loop

```
1. Message arrives (human message, or system kick for async)
2. Assemble context for responding agent (7-layer pipeline, see Context Assembly)
3. Call model
4. Stream response
5. If response contains tool calls:
   a. For each tool call: policy check → execute → capture result
   b. Append tool calls + results to conversation history
   c. → Go to step 3
6. If response is final text (no tool calls):
   → Turn complete
```

The agent keeps reasoning and acting until it produces a response with no tool calls.

### Sync Entry Point

Human message triggers entry. The agent loops through tools until it has a text response for the human. Turn ends, human sees the response.

### Async Entry Point

The system kicks off the turn with a task prompt ("You're assigned to task X, here's your context, begin."). The agent loops through tools — reading files, writing code, querying Ellie, executing commands — until it either signals "step done" (calls the flow advancement tool) or produces a final message with no tool calls. If the context window fills up, the system checkpoints (summarizes conversation so far) and sends a continuation message so the agent can keep going.

Async kicks are modeled as execution wakeups, not "always create a new run." The control plane first checks the current execution owner for that task/session boundary:

- same owner: coalesce onto the active run
- different owner: persist a deferred wakeup and wait
- stale or released owner: promote the oldest deferred wakeup and resume execution once

Resume decisions are driven from the persisted runtime-state contract first, not by reconstructing state from chat rows alone. The chat layer only appends a new kickoff message when a wakeup actually starts or is promoted. Coalesced wakeups are visible in control-plane history but do not create duplicate kickoff chatter.

### Tool Call Policy (No Mid-Turn Approval)

Permissions are pre-configured, never asked mid-turn. By the time the agent is in the loop, the answer to "can I do this?" is always immediate — never "hold on, let me ask."

Policy check outcomes at runtime:

- **allow**: execute the tool immediately, return result.
- **deny**: return "not permitted" as the tool result. The agent sees this and can adapt.

In both cases, the loop continues immediately. Policy is binary — there is no runtime approval gating.

Communication tools (e.g., `email.compose`, `slack.post`) create drafts as their **designed behavior** when the policy allows execution. The tool succeeds and stages the action in the human's inbox (see 03-projects-and-task-flow.md Inbox section). This is tool behavior, not policy interception.

The agent is told in its prompt assembly (layer 2, policies & constraints) what its permissions are. A well-behaved agent won't attempt denied actions because it knows the policy. The deny path is a guardrail, not the primary mechanism.

### Tool Execution Tiers

Not all tool calls carry the same weight. Tools are split into two tiers based on whether they have side effects. A tool's tier is determined at registration — it is never a runtime decision.

**Tier 1: Chat-layer tools (read-only, internal)**

How the agent understands its environment. No side effects. Executed directly in the chat layer with a basic permission check (does this agent have access to this scope's data?). Logged as `tool_call`/`tool_result` chat messages — that IS the audit trail for reads.

- Read task description, acceptance criteria, project context
- Query Ellie (memory retrieval)
- Read file contents in workspace
- Search code
- List files
- Read conversation history from other sessions (within permitted scope)

**Tier 2: Control-plane tools (mutations, external, side effects)**

These change state or reach outside OtterCamp. Full control plane path (see 16-agent-control-plane.md): policy evaluation (allow/deny), execution via broker, Run audit trail, artifact capture.

- **OtterCamp mutations**: create task (file blocker), update task status, advance flow, create memory items, invite agent to session. Policy-gated, but execution is fast (DB writes).
- **System tools**: CLI execution, browser control, file writes. Sandboxed, potentially long-running, full execution tracking.
- **External tools (MCP)**: calls to external services via MCP connections. Policy-gated per connection and per tool, schema validation, timeout/retry handling.
- **Communication tools**: compose email, post to Slack, create PR. Communication tools create drafts as their designed behavior — the tool stages the action in the human's inbox for review.

**Routing within the turn loop:**

The turn loop does not know the difference between tiers. It calls "execute tool" and gets a result. The tool execution layer routes based on the tool's registered tier:

```
Agent requests tool call
│
├─ Tool registry lookup: which tier?
│
├─ Tier 1 (chat-layer):
│   → Permission check (scope access)
│   → Execute directly
│   → Return result
│   → Log as chat_message (tool_call + tool_result)
│
└─ Tier 2 (control-plane):
    → Policy evaluation (allow / deny)
    → If deny: return "not permitted"
    → If allow: dispatch to broker → sandbox → execute
    → Return result
    → Log as chat_message AND as RunStep in control plane
```

**Why two tiers instead of routing everything through the control plane?**

In a typical async turn, an agent might do 30 reads and 3 writes. Running 30 reads through the full broker pipeline (create RunStep, evaluate policy, dispatch, capture, finalize) is substantial overhead for operations that will always be allowed. The two-tier model keeps reads fast and lightweight while preserving full control plane rigor for actions with consequences.

**Tradeoffs acknowledged:**

- Two execution paths to maintain and test instead of one.
- The control plane's Run/RunStep timeline has gaps where tier 1 calls happened — a complete agent activity view requires stitching chat messages and RunSteps together.
- Tools may need to migrate from tier 1 to tier 2 if requirements change (e.g., adding rate limiting or compliance logging to file reads).

**Mitigation:** Keep tier 1 minimal. Only pure reads on data the agent is already scoped to access. The moment there's any question about whether a tool has side effects or needs policy gating, it's tier 2.

### Stop Conditions

Two hard stop conditions, both configurable at the org/project/agent level:

1. **Max tool calls per turn** — simple counter. Hard cap. Prevents runaway loops.
2. **Max turn duration** — wall clock time from turn start. Enforced at loop boundaries: before each model call or tool execution, check remaining time. If exceeded, finalize the turn. Current operation always finishes — never kills a model call or tool execution mid-stream.

Token usage is **tracked per turn** for observability (input tokens, output tokens, total) but is NOT used as a stop condition. Cost controls live at the org/project budget level (see 13-security-observability-costs.md).

When a stop condition is hit:
- In sync: the agent's partial response is finalized with a system note explaining the limit.
- In async: the turn is marked `stopped` with the relevant `stop_reason`. The supervisor process (see 16-agent-control-plane.md) picks it up for retry, escalation, or blocker filing.

### Turn Duration Enforcement

Enforcement is soft at the loop boundary — it never interrupts a model call or tool execution in progress:

```
turn_start = now()

loop:
  remaining = max_duration - (now() - turn_start)
  if remaining <= 0:
    → finalize turn (stop_reason: max_duration)
    → break

  call model (with per-call timeout from model profile)

  for each tool call in response:
    remaining = max_duration - (now() - turn_start)
    if remaining <= 0:
      → skip remaining tool calls
      → finalize turn (stop_reason: max_duration)
      → break
    execute tool (with per-tool timeout)

  if no tool calls:
    → turn complete
    → break
```

Individual model call timeouts and tool execution timeouts come from their own configs (doc 07 for models, control plane for tools). The turn duration is the outer envelope.

## Context Assembly Per Turn

The 7-layer prompt assembly pipeline (defined in 05-agents-staff-and-temps.md) runs **once at the start of each turn**. Within the turn's tool loop, each iteration just appends tool calls and results to the conversation — the model sees its own previous actions accumulating. The full pipeline is not re-run on every loop iteration.

### Within a Single Turn

```
[Start of turn]
  Layer 1: Agent identity          ← set once
  Layer 2: Policies & constraints  ← set once
  Layer 3: Scope context           ← set once
  Layer 4: Skills                  ← set once
  Layer 5: Memory (Ellie passive)  ← injected once
  Layer 6: Conversation history    ← set once, then grows
  Layer 7: Tool descriptions       ← set once

[Tool loop iteration 1]
  Model sees: all 7 layers + human message
  Model responds: text + tool call
  → append tool call + tool result to conversation

[Tool loop iteration 2]
  Model sees: same 7 layers + human message + prior tool call/result
  Model responds: text + tool call
  → append

[... until no tool calls]
```

Layer 6 (conversation) is the only thing that grows within a turn.

### Across Turns (Progressive Summarization)

On turn 1 of a session, conversation history is empty — all budget goes to context, skills, and memory. By turn 47, conversation history competes for budget.

Summarization is **progressive, not panic-driven**:

- After each turn completes, evaluate whether conversation history exceeds a threshold.
- If it does, the oldest unsummarized turns get summarized. The summary replaces the full content **in the context window only** — storage always keeps full messages.
- The context window always looks like: `[summaries of old turns] + [recent turns in full]`.
- Summaries are `chat_summary` records that cover a range of messages (from_sequence to to_sequence).

### Sync vs Async Differences

- **Sync**: scope context (layer 3) re-injected every turn — it's small and the human may have context-switched between turns.
- **Async**: scope context injected on the first turn only. The agent has internalized it and builds on top of it via tools across subsequent turns.

## Multi-Party Turn Sequencing

### Turn Cycle

When a human sends a message, the system runs a **turn cycle**. The cycle is atomic — it completes fully before the next human message is processed.

```
Human sends message
│
├─ Is there an @mention in the human's message?
│   ├─ Yes: @mentioned agent(s) are the responder(s)
│   └─ No: default responder takes it
│
├─ PHASE 1: Responder turn(s)
│   For each responder (usually one, multiple if multiple @mentions):
│     → Create chat_turn (trigger: human_message)
│     → Assemble context, run tool loop, stream response
│     → Finalize turn
│
├─ PHASE 1.5: Agent @mentions
│   If the responder's output contained @mentions of other agents:
│     For each mentioned agent (in mention order):
│       → Create chat_turn (trigger: agent_mention)
│       → Full context assembly, tool loop, stream response
│       → Finalize turn
│   One round only — @mentions in these responses do NOT trigger more turns.
│
├─ PHASE 2: Listening eval
│   For each agent that hasn't already responded in this cycle:
│     → Lightweight model call (Haiku-tier):
│       "Here's the latest exchange. Do you need to contribute?"
│     → Returns: yes/no + relevance score if yes
│     → May also produce a reaction (see Reactions) at no extra cost
│
├─ PHASE 3: Interjection turns (if any flagged yes)
│   For each agent that flagged, in descending relevance order:
│     → Create chat_turn (trigger: interjection)
│     → Full context assembly, tool loop, stream response
│     → Finalize turn
│
└─ Cycle complete. Next queued message can be processed.
```

### Sequencing Rules

- **One round of listening evals per human message.** Interjections do NOT trigger another round of evals. This prevents infinite recursion. One round keeps it bounded and predictable.
- **@mention flips who's listening.** When you @mention Agent X, Agent X becomes the responder and the default responder becomes a listener. The default responder could interject if they have something relevant via the eval pass.
- **Multiple @mentions get sequential turns** in the order mentioned. These are explicit invitations, not interjections. The listening eval still runs afterward for everyone who wasn't mentioned.
- **Multiple interjecting agents respond in sequence**, ordered by relevance score from the eval.
- **Agents can @mention other agents** in their response text. The system detects @mentions after the turn and gives mentioned agents turns (Phase 1.5). One round only — @mentions in those responses do NOT trigger further turns. This prevents unbounded recursion while allowing one level of agent-to-agent delegation within a cycle.

### What the Human Sees (Sync)

```
[Human sends message]
[Default responder streams a response]
[Brief pause — 1-2 seconds while listening eval runs]
[If interjection: another response appears from a different agent]
[If no interjection: nothing, cycle done]
```

The listening eval is fast — Haiku-tier model, small prompt (just the latest exchange + agent identity, not full context).

### Default Responder

Each session has a default responder determined by scope:

- **Org session**: Frank (Chief of Staff — the human's primary touchpoint).
- **Project session**: the project manager assigned to that project.
- **Task sync session**: the PM (or task lead). Not the agent doing the work — the agent is a participant that can be @mentioned.
- **Per-node async session**: the agent assigned to that flow node (these are work sessions, not human-facing).

The default responder takes every turn unless another agent is explicitly @mentioned.

## Streaming & Message Lifecycle

### How Streaming Maps to Messages

Each **segment** of agent text between tool calls is its own message. The model streams tokens — text and tool calls — and we map them to discrete messages as they arrive:

```
Model starts streaming text: "Let me check that file..."
  → Create agent message (state: streaming), emit deltas to client

Model emits a tool call: read_file(src/main.go)
  → Finalize the agent text message (state: final)
  → Create tool_call message (state: final — tool calls are discrete)
  → Execute tool
  → Create tool_result message (state: final)
  → Feed result back to model

Model starts streaming again: "I see the issue. Let me also look at..."
  → NEW agent message (state: streaming), emit deltas

Model emits another tool call: read_file(src/auth.go)
  → Finalize that agent message
  → tool_call message, tool_result message

Model streams final response: "Here's what I found..."
  → NEW agent message (state: streaming → final)
  → No tool calls, turn complete
```

A turn with 3 tool calls might produce 4 agent messages (text segments between and after tools), 3 tool_call messages, and 3 tool_result messages. Each has a clear lifecycle.

### Edge Cases

- **Model emits only tool calls, no text**: no agent text message for that iteration. Just tool_call and tool_result messages. Common when the agent is silently gathering information.
- **Model emits multiple tool calls in one response**: each becomes its own tool_call message. Tier 1 tools can execute in parallel; tier 2 tools execute sequentially. When a model response contains a mix of tier 1 and tier 2 tool calls, all tier 1 calls execute first (in parallel), then tier 2 calls execute sequentially. Each gets its own tool_result.
- **Model starts streaming text, then emits a tool call**: the partial text is finalized as-is. The human sees "Let me check..." and then the tool call — natural and expected.

### Message State Transitions by Role

```
human:       pending → final
             (pending while queued, final once it triggers a turn)

agent:       pending → streaming → final
             (pending briefly at creation, streaming while tokens arrive,
              final when generation completes or tool call starts)

system:      → final  (immediate, no streaming)

tool_call:   → final  (immediate, tool calls are discrete)

tool_result:  → final  (immediate, written once)

Any message: * → failed  (if something goes wrong)
             final → redacted  (content zeroed, row preserved)
```

### Realtime Event Types and Payloads

What the realtime channel emits during a turn (transport — WebSocket vs SSE — is a doc 12 decision; the chat spec defines what's emitted and payload shapes):

```
chat.turn.started
  {turn_id, session_id, trigger, responding_type, responding_id}

chat.message.created
  {message_id, session_id, turn_id, sequence, role,
   author_type, author_id, state, content?}
  — for agent messages: state is "streaming", content arrives via deltas
  — for tool_call/tool_result/system: state is "final", content included

chat.message.delta
  {message_id, delta: {text: "..."}}
  — streaming text chunks, only for agent messages in "streaming" state

chat.message.finalized
  {message_id, state: "final", finalized_at}

chat.turn.completed
  {turn_id, status, stop_reason, tool_call_count,
   input_tokens, output_tokens, duration_ms}

chat.listening_eval.completed
  {session_id, after_turn_id,
   interjections: [{agent_id, relevance_score}]}
  — tells the client whether interjection turns are coming
```

The server emits everything. The **client** decides how to render — the TUI might show a compact tool call log, the web UI might show collapsible cards, large tool results get expandable detail. This is a presentation concern, not a protocol concern.

For async turns where the human wasn't watching, the stored messages provide a complete replay — every tool call, result, and agent response is persisted and reviewable after the fact.

## Session Continuity

### When Summaries Fire

After each turn completes, the system evaluates whether conversation history exceeds a **token threshold** — a percentage of the budget allocated to layer 6 (conversation) in the prompt assembly pipeline.

When conversation history exceeds ~50-60% of its allocated budget, summarize the oldest ~25-30% of unsummarized turns. This is a starting point — tuned based on real usage.

When triggered:

1. Select the oldest batch of unsummarized turns (batched for efficiency, not one at a time).
2. Generate a summary via a lightweight model call (Haiku-tier): "Summarize these turns, preserving key decisions, outcomes, tool results, and open threads." The summarization prompt has two additional explicit preservation rules:
   - **Preserve restorable references**: all file paths, URLs, artifact IDs, git refs, and entity names must survive summarization verbatim. Dropping content is acceptable; dropping the pointer to that content is not. This enables agents (or Ellie) to restore full context from references when needed, without re-reading the original turns.
   - **Preserve failure evidence**: tool call failures, error messages, rejected approaches, and dead ends must be captured in summaries with enough detail that the agent understands what was tried and why it failed. Erasing failure evidence causes agents to repeat failed approaches; preserving it enables adaptation.
3. Store as a `chat_summary` record covering that message sequence range.
4. On the next turn's context assembly, the summary replaces the full messages for that range.

### Async Continuation After Context Reset

In long async sessions, the context window fills up repeatedly. Summarization handles this progressively:

```
Turn 30: context approaching limit
  → Summarize turns 1-20
  → Context: [summary of 1-20] + [turns 21-30 in full]

Turn 50: approaching limit again
  → Summarize turns 21-40
  → Context: [summary of 1-20] + [summary of 21-40] + [turns 41-50 in full]

Turn 60: summaries accumulating
  → Consolidate older summaries (summarize the summaries)
  → Context: [consolidated summary of 1-40] + [turns 41-60 in full]
```

For extremely long sessions, the system creates a **continuation turn**:

1. Summarize everything into a comprehensive checkpoint.
2. Create a system message (event_type: `continuation`) that explicitly tells the agent its context was compressed and instructs it to query Ellie if it feels it's missing information rather than filling in gaps from assumption. Contains:
   - Summary of all prior work
   - Current task state (what's done, what's remaining)
   - Key decisions and context needed going forward
3. Start a new turn (trigger: `continuation`) with the compressed context.

The agent is **always told when context is compressed** — no pretending the full history is there. The continuation message directs the agent to reach out to Ellie for anything that feels uncertain or incomplete. This prevents hallucination to fill in gaps from summarized-away details.

### Crash Recovery

If an agent's turn fails mid-execution (model call fails, service crashes, timeout):

1. The turn is marked `failed` with the appropriate stop_reason.
2. All messages up to that point are preserved — every tool call and result was persisted the moment it happened.
3. The supervisor (see 16-agent-control-plane.md) detects the failed turn:
   - **Transient failure** (rate limit, network blip): start a new turn (trigger: `continuation`) with context from the failed turn's messages. No work is lost.
   - **Permanent failure** (budget exhausted, repeated errors): escalate — file a blocker task.

Because every step is persisted as a message immediately, recovery never loses work. The retry turn sees everything the agent did before the crash.

### Long Gaps Between Turns

An async agent works for 10 turns, then waits hours for a blocker to resolve. When it resumes:

- A new turn starts (trigger: `system_kick` — the dependency resolved).
- Context is assembled fresh via the 7-layer pipeline.
- Conversation history is there (summaries for old turns, recent turns in full).
- Scope context (layer 3) may have updated — the blocker task is done, reviewer feedback is available.
- Ellie's memory injection picks up anything new that's relevant.

No special handling needed for gaps. The existing model handles it.

### Session-Level Recovery

If a session is irrecoverably broken (agent stuck in a loop, corrupted state, nonsensical context):

- Close the session.
- Create a new session at the same scope.
- The new session gets context via standard mechanisms — scope context and Ellie's memory include outcomes from the closed session.
- This is the "always close, always open new" philosophy applied to recovery.

## Navigation and Chat Pane

Chat is not a separate page — it is a **persistent pane** that is always visible. The UI has three panels:

- **Session sidebar** (left): lists all sessions the human participates in, grouped by scope. Shows unread indicators.
- **Main content** (center): dashboard, project overview, task board, task detail — whatever the human is looking at.
- **Chat pane** (right): the active session, always present. Changes based on context.

### Sidebar Structure

```
General                          ← org session (Frank)

OtterCamp V2                     ← project session (PM)
  Define auth architecture       ← task sync session
  Implement auth             •   ← task sync session (unread)
  Write chat spec                ← task sync session

Client Portal                    ← project session (PM)
  Fix login bug              •   ← task sync session (unread)
  Plan Sprint 3                  ← task sync session
```

Every entry is one session. Per-node async sessions (agent work logs) do not appear in the sidebar — they are viewable from the task detail view in the main content area.

Unread indicators bubble up: if any task session in a project has unread messages, the project group shows an indicator too.

### Scope Pill

At the top of the chat pane, a segmented pill lets the human zoom in and out of scope without leaving their current context in the main content:

```
Viewing a task:     [Task] [Project] [Org]    ← all three segments
Viewing a project:         [Project] [Org]    ← two segments
Viewing dashboard:                   [Org]    ← org only
```

Clicking a segment switches the chat pane to the session at that scope level. The main content does not change. The pill always reflects what scope levels are available given the current main content context.

This is the primary way to "zoom out" — the human is looking at a task, wants to tell Frank something at the org level, clicks [Org], talks to Frank, clicks [Task] to come back. The pill also provides the natural input for the viewing context hint: when [Org] is active while the main content shows a task, the system knows exactly which task the human was looking at.

### Navigation Binding

The chat pane and main content are **independently navigable** via two mechanisms:

- **Scope pill** (top of chat pane): switches the chat pane between scope levels within the current context. Main content stays put. This is for "zoom in/out."
- **Session sidebar** (left panel): switches to a different project/task entirely. Main content stays put. Chat pane loads the clicked session. This is for "jump to something else."
- **Main content navigation** (click into a project, open a task): the chat pane defaults to the most specific scope available (task if on a task, project if on a project). The pill resets to match. This is the implicit default.

This means the human can be looking at Task: Implement Auth in the main content while talking to Frank (org scope) in the chat pane — achieved by clicking [Org] on the pill or "General" in the sidebar.

### Viewing Context

When the human is in a session whose scope differs from what they're looking at in the main content, the system provides a **viewing context hint** to the agent. This is not a message — it's ephemeral metadata sent with the human's message by the client.

Example: Human is viewing Task: Implement Auth, switches chat pane to General (Frank). The human says "I want to write a blog post about this feature." Frank's prompt assembly includes: "Human is currently viewing Task: Implement Auth (Project: OtterCamp V2)." Frank knows what "this feature" refers to without the human needing to explain.

The viewing context is:
- Sent by the client with each message as metadata: `{viewing_scope_type, viewing_scope_id}`
- Included in the agent's prompt assembly (layer 3, scope context) when the viewing scope differs from the session scope
- Not persisted as a message — it's ephemeral context, not part of the conversation record

## Notifications

Notifications are a **consumer of the event bus** (see 12-api-events-and-realtime.md). The event bus emits events; the notification layer subscribes, applies the human's preferences, and delivers.

### What Generates Notifications

**Urgent** — needs attention now:
- Escalation reached the human (PM → Frank → human)
- Agent turn failed and couldn't recover
- Blocker filed that requires human judgment

**Normal** — needs attention soon:
- Task ready for review
- Draft pending in inbox (communication tool staged an action for review)
- @mention in any session
- Invited to a session

**Low** — informational:
- Agent finished a task (moved to `done`)
- Task status changed
- Agent started work on a queued task

### Notifications vs Inbox

- **Inbox** = items requiring the human to **act** (approve draft, triage blocker, review task). Action-required queue. See 03-projects-and-task-flow.md.
- **Notifications** = events the human should **know** about. May or may not require action.

Every inbox item generates a notification. Not every notification is an inbox item.

### Delivery

For now, delivery is **in-app only** — a notification center accessible from the sidebar. Clicking a notification navigates to the relevant context (main content updates, chat pane loads the appropriate session).

Push notifications (mobile) and email delivery are deferred until those platforms exist.

### Preferences

Configurable per human:
- **Per urgency tier**: which tiers show in the notification center vs. activity feed only
- **Per scope**: "Notify me about everything in Project X, only escalations in Project Y"
- **Per event type**: "Always tell me about blockers, skip routine task status changes"

Defaults: urgent shows prominently, normal shows in notification center, low shows in activity feed only.

### Batching

- **Urgent**: always real-time, always individual.
- **Normal**: real-time but can be grouped ("3 tasks ready for review in Project X").
- **Low**: batched into periodic digests (frequency configurable).

## Unread Tracking

To support sidebar unread indicators, the system tracks what each human has seen per session.

```sql
create table chat_read_cursor (
  session_id    uuid not null references chat_session(id),
  user_id       uuid not null references human_user(id),
  last_read_seq int not null default 0,
  updated_at    timestamptz not null default now(),

  primary key (session_id, user_id)
);
```

- One row per human per session.
- `last_read_seq` is the sequence number of the last message the human has seen.
- Unread count = `max(chat_message.sequence for session) - last_read_seq`.
- Updated when the chat pane is focused for that session.
- Unread indicators in the sidebar bubble up from tasks to project groups.

## Reactions

Lightweight signals on messages, from humans or agents. Not full responses — reactions are a way to acknowledge, agree, disagree, or flag without taking a turn.

### How Reactions Work

- **Human reacts to agent message**: quality feedback. "This was helpful" (positive) or "This was wrong/unhelpful" (negative). Optional note for context ("factually incorrect" vs "not what I asked").
- **Agent reacts to human message**: acknowledgment or signal. "I noted this" (positive) or "I disagree / this may be problematic" (negative). Produced during the listening eval — an agent that doesn't need to interject can still leave a reaction at near-zero additional cost.
- **Agent reacts to another agent's message**: agreement or disagreement. Useful for surfacing conflicting views without requiring a full interjection.

### Sentiment Model

Simple: `positive` or `negative`, with an optional `note` for context. This is deliberately minimal — it's a signal mechanism, not social media. Richer reaction types can be added later if needed.

### Memory Signal

All reactions feed into Ellie's memory pipeline (see 06-memory.md):

- Human thumbs-up on an architecture decision → strong signal to capture as semantic memory.
- Human thumbs-down on a factual claim → signal to flag or correct that memory.
- Agent flagging a human's requirement as important → capture it.
- Agent disagreeing with another agent → potential contradiction to investigate.

### Constraints

- One reaction per participant per message (unique constraint). Can be changed but not duplicated.
- Only applies to messages in `final` state.
- Reactions on your own messages are not allowed.

### Data Model

```sql
create table chat_message_reaction (
  id            uuid primary key default gen_random_uuid(),
  message_id    uuid not null references chat_message(id),
  reactor_type  text not null check (reactor_type in ('human', 'agent')),
  reactor_id    uuid not null,
  sentiment     text not null check (sentiment in ('positive', 'negative')),
  note          text,
  created_at    timestamptz not null default now(),

  unique (message_id, reactor_type, reactor_id)
);

create index on chat_message_reaction (message_id);
```

## Cancel and Steer

### Cancel

Cancel is **explicit** — the human presses Escape or clicks a Cancel button. Sending a new message does NOT implicitly cancel the in-progress turn.

When the human cancels:
1. The current model call is aborted.
2. In-flight tool executions finish (don't kill side effects mid-execution).
3. The turn is marked: `status=stopped`, `stop_reason=user_cancelled`.
4. All messages up to that point are preserved (state: `final`).
5. Partial streaming agent text (if any) is finalized as-is.

### Message Queue

When the human sends messages while a turn is in progress, they are **queued** — not dropped, not injected mid-turn.

- Queued messages are persisted as `chat_message` rows with state `pending`.
- Queue processing is **serial**: one message at a time, each gets a full turn cycle (responder → listening eval → interjections).
- The oldest queued message is processed first when the current turn cycle completes.
- **Queued messages are editable** while waiting. The human can revise content, add context, or delete messages before they trigger a turn.

### Steer

Each queued message has a **Steer** button. Hitting Steer on a message:

1. Cancels the current in-progress turn (`stop_reason: user_steered`).
2. Promotes **that specific message** to the front of the queue.
3. Immediately triggers a new turn cycle with it.
4. Other queued messages stay in the queue in their original order.

The distinction between `user_cancelled` and `user_steered` as stop reasons is intentional — useful for analytics (how often humans redirect vs abort) and for agent context (a steer signal in history indicates the prior direction was wrong, not just slow).

### Multi-Human Message Queue

In sessions with multiple humans, there is a **single shared queue** ordered by submission time regardless of which human sent what. This keeps processing fair and deterministic. Edit and Steer controls only apply to your own queued messages — you cannot edit or steer another human's message.

## Storage

### PostgreSQL as Source of Truth

All chat data is stored in PostgreSQL. This is the canonical record.

- Messages are **append-only**: content is never edited after reaching `final` state. The `redacted` state zeroes out content but preserves the row for audit trail integrity.
- Transactional consistency: when a turn completes, messages, tool call records, artifact references, and token counts all commit together.
- Queryable: find messages by session, agent, time range, role, tool name. Cross-session queries for audit, analytics, and review.

### Ephemeral Messages

Some messages are internal coordination noise — scheduling decisions, heartbeat checks, health pings, internal handoff metadata between agents. These are useful in the moment but don't need to be preserved permanently.

Messages can be flagged `ephemeral = true`. Ephemeral messages are stored normally and participate in context assembly during the session, but a daily cleanup job purges them. This keeps the permanent record clean while allowing agents to use the message bus for coordination.

What qualifies as ephemeral:
- Agent health check pings and responses
- Internal scheduling/coordination signals between agents
- PM supervision check-ins that found nothing actionable
- System-level heartbeat and status messages

What is NOT ephemeral:
- Any message in a human-facing sync session
- Agent work output (tool calls, results, artifacts)
- Blocker filings, escalations, decisions
- Anything the human or an agent might want to reference later

### Session Lifecycle Cleanup

When a session closes (flow node completion, manual close, or session-level recovery), the following cleanup occurs:

**Immediate (at close time):**
- Session status transitions to `closed`, `closed_at` is set.
- Ellie's extraction pipeline processes any remaining unprocessed events from the session (extraction is event-driven, but this ensures nothing is missed).
- Active turns are finalized — any `in_progress` turn is marked `failed` with stop_reason `session_closed`.

**Deferred (daily cleanup job):**
- **Ephemeral message purge**: messages with `ephemeral = true` in closed sessions are deleted. Ephemeral messages in active sessions are retained until the session closes.
- **Progressive summary consolidation**: for closed sessions with multiple `chat_summary` records, consolidate into a single final summary covering the entire session. This is the canonical compressed representation of the session, used by scope context (layer 3) when injecting session outcomes into future contexts.
- **Tool result compaction**: tool call/result message pairs in closed sessions older than the retention threshold can have their `content` field truncated to metadata-only (tool name, status, output summary). Full content is preserved in object storage via the prompt capture on `model_invocation` if it was part of a model call.

**Retention enforcement (via doc 13 daily retention job):**
- Chat session transcripts follow org retention policy (default 90 days). After retention expiry, messages are archived to object storage (if archival is enabled) then deleted from PostgreSQL.
- `chat_summary` records are retained indefinitely — they are the compressed knowledge of what happened, not raw transcript.
- `chat_artifact` records follow object storage retention (default 90 days), except artifacts linked to active tasks which are exempt until the task completes.

The session lifecycle ensures that valuable context is extracted (via Ellie's memory pipeline) and compressed (via progressive summarization) before raw data ages out. After retention expiry, the system retains: memories extracted from the session, the consolidated session summary, and aggregate metrics — but not the raw transcript.

### JSONL as Export Format

JSONL is an optional export/import format for debugging and portability. It is NOT the storage layer. Format matches the line types: `message`, `tool_call`, `tool_result`, `summary`, `checkpoint`, `event`.

### Object Storage for Artifacts

Files, screenshots, logs, and other binary content are stored in object storage (S3-compatible). The `chat_artifact` table holds references (storage path, content type, size). The message that produced the artifact links to it.

## Database Schema

### chat_session

```sql
create table chat_session (
  id              uuid primary key default gen_random_uuid(),
  organization_id uuid not null references organization(id),
  scope_type      text not null check (scope_type in ('org', 'project', 'task')),
  scope_id        uuid not null,
  title           text,
  mode            text not null check (mode in ('sync', 'async')) default 'sync',
  status          text not null check (status in ('active', 'closed')) default 'active',
  created_by_type text not null check (created_by_type in ('human', 'agent', 'system')), -- agent: when a PM or agent creates a per-node async session during flow execution
  created_by_id   uuid not null,           -- sentinel UUID for system-created sessions
  message_seq     int not null default 0,
  turn_seq        int not null default 0, -- atomic counter for chat_turn.sequence
  created_at      timestamptz not null default now(),
  closed_at       timestamptz,
  metadata        jsonb not null default '{}'
);

create index on chat_session (organization_id);
create index on chat_session (scope_type, scope_id);
```

- `scope_type` + `scope_id` — polymorphic FK. The app layer enforces that scope_id points to the right table (organization, project, or project_task). Avoids three nullable FK columns.
- `mode` is mutable — a human joining an async session switches it to sync.
- `message_seq` — monotonic counter for assigning message sequence numbers. Atomically incremented via `UPDATE ... RETURNING`. Avoids gaps and race conditions.
- `title` — nullable. When null, the UI displays the scope name (org name, project name, or task title). Human can override.
- `status` is just active/closed. Sessions are cheap — close when done, create a new one if needed.
- `created_by_type` + `created_by_id` — who created the session. For system-created sessions (auto-created for flow nodes), `created_by_type` is `'system'` and `created_by_id` is the sentinel UUID (`00000000-0000-0000-0000-000000000000`).

### chat_participant

```sql
create table chat_participant (
  id               uuid primary key default gen_random_uuid(),
  session_id       uuid not null references chat_session(id),
  participant_type text not null check (participant_type in ('human', 'agent')),
  participant_id   uuid not null,
  role             text not null check (role in ('default_responder', 'participant', 'listener')),
  permissions      text[] not null default '{}',
  joined_at        timestamptz not null default now(),
  left_at          timestamptz,

  unique (session_id, participant_type, participant_id)
);

create index on chat_participant (session_id);
create index on chat_participant (participant_type, participant_id);
```

- `role`: `default_responder` (one per session, takes every turn), `participant` (active, can be @mentioned), `listener` (sees everything, gets interjection eval pass).
- `permissions` is a text array: `{'read', 'write', 'mention', 'invite', 'remove', 'moderate'}`.
- `left_at` — nullable. When set, participant has left. Record preserved (append-only), not deleted.
- Unique constraint prevents the same human/agent from being added twice to a session.

### chat_turn

```sql
create table chat_turn (
  id                  uuid primary key default gen_random_uuid(),
  session_id          uuid not null references chat_session(id),
  cycle_id            uuid,            -- groups all turns triggered by the same human message
  sequence            int not null,
  trigger             text not null check (trigger in ('human_message', 'system_kick', 'agent_mention', 'interjection', 'continuation')),
  responding_type     text not null check (responding_type in ('agent')), -- humans trigger turns but do not "respond" in the turn-loop sense
  responding_id       uuid not null,
  status              text not null check (status in ('in_progress', 'completed', 'failed', 'stopped')) default 'in_progress',
  stop_reason         text check (stop_reason in ('max_tool_calls', 'max_duration', 'user_cancelled', 'user_steered', 'model_error', 'session_closed')),
  tool_call_count     int not null default 0,
  model_id            text,
  input_tokens        int,
  output_tokens       int,
  duration_ms         int,
  started_at          timestamptz not null default now(),
  completed_at        timestamptz,
  metadata            jsonb not null default '{}'
);

create unique index on chat_turn (session_id, sequence);
create index on chat_turn (session_id, status);
```

- Turns group messages into complete cycles: triggering message → tool loop → final response.
- `cycle_id` — shared UUID across all turns in a single turn cycle (Phases 1, 1.5, 2, 3). All turns triggered by the same human message share a `cycle_id`. Null for async turns (`system_kick`, `continuation`) that aren't part of a human-triggered cycle.
- `trigger` values: `human_message` (human sent a message), `system_kick` (system started an async agent turn), `agent_mention` (agent was @mentioned by another agent, Phase 1.5), `interjection` (listening agent flagged it had something to contribute, Phase 3), `continuation` (context was checkpointed, agent continues).
- `status`: `in_progress` while the tool loop runs, `completed` on success, `failed` on error, `stopped` when a limit or cancel was hit.
- `stop_reason` — null unless stopped/failed. Values: `max_tool_calls`, `max_duration`, `user_cancelled`, `user_steered`, `model_error`, `session_closed`.
- Token counts and duration are rollups across all model calls within this turn. Tracked for observability.
- `chat_turn` is created at turn start and updated at completion.

### chat_message

```sql
create table chat_message (
  id              uuid primary key default gen_random_uuid(),
  session_id      uuid not null references chat_session(id),
  turn_id         uuid references chat_turn(id),
  sequence        int not null,
  role            text not null check (role in ('human', 'agent', 'system', 'tool_call', 'tool_result')),
  author_type     text check (author_type in ('human', 'agent')),
  author_id       uuid,
  content         jsonb not null,
  state           text not null check (state in ('pending', 'streaming', 'final', 'failed', 'redacted')) default 'pending',
  tool_call_id    text,
  ephemeral       boolean not null default false,  -- coordination noise, purged by daily cleanup
  created_at      timestamptz not null default now(),
  finalized_at    timestamptz,
  metadata        jsonb not null default '{}'
);

create unique index on chat_message (session_id, sequence);
create index on chat_message (turn_id);
create index on chat_message (session_id, role);
```

- **One row per logical unit.** A human text message, an agent text response, a tool call, and a tool result are each their own row. Maximum queryability, matches append-only log semantics.
- `role` values: `human` (human text), `agent` (agent text response), `system` (system-generated: session start, checkpoint, limit notice), `tool_call` (agent requested a tool), `tool_result` (result from tool execution).
- `content` is jsonb, structure varies by role:
  - human/agent: `{"blocks": [{"type": "text", "text": "..."}, ...]}` — see Rich Content section for block types
  - tool_call: `{"name": "read_file", "arguments": {"path": "src/main.go"}, "call_id": "tc_abc123"}`
  - tool_result: `{"call_id": "tc_abc123", "output": "...", "status": "success"}`
  - system: `{"text": "...", "event_type": "session_start"}`
- `state` transitions: `pending → streaming → final` (happy path), `pending → failed`, `final → redacted`. Content is never edited — `redacted` zeroes the content field but preserves the row.
- `turn_id` — nullable for queued human messages (state: `pending`) that haven't triggered a turn yet. Set when the message triggers or becomes part of a turn.
- `tool_call_id` — for `tool_result` messages, links back to the `call_id` in the corresponding `tool_call` message.
- `author_type` + `author_id` — nullable for system messages and tool_results. For human/agent roles, identifies the author.
- `sequence` — per-session monotonic ordering assigned from `chat_session.message_seq`.

### chat_artifact

```sql
create table chat_artifact (
  id            uuid primary key default gen_random_uuid(),
  session_id    uuid not null references chat_session(id),
  message_id    uuid not null references chat_message(id),
  name          text not null,
  content_type  text not null,
  size_bytes    bigint not null,
  storage_path  text not null,
  created_at    timestamptz not null default now(),
  metadata      jsonb not null default '{}'
);

create index on chat_artifact (session_id);
create index on chat_artifact (message_id);
```

- Artifacts are files, screenshots, logs — binary content in object storage (S3-compatible).
- `storage_path` points to the object storage location.
- `content_type` is MIME type (e.g., `image/png`, `text/plain`).
- `session_id` denormalized for querying all artifacts in a session without joining through messages.

### chat_summary

```sql
create table chat_summary (
  id             uuid primary key default gen_random_uuid(),
  session_id     uuid not null references chat_session(id),
  from_sequence  int not null,
  to_sequence    int not null,
  content        text not null,
  model_id       text not null,
  token_count    int not null,
  created_at     timestamptz not null default now(),
  metadata       jsonb not null default '{}'
);

create index on chat_summary (session_id, from_sequence);
```

- Summaries support progressive context management. They cover a range of messages (from_sequence to to_sequence) and replace those messages in context assembly.
- `token_count` — how many tokens this summary consumes in context. Used for budget calculation during prompt assembly.
- Summaries are **immutable**. If a better summary is needed, create a new one covering the same or overlapping range. Context assembly uses the most recent summary for a given range.
- Full messages are always preserved in `chat_message`. Summaries are a context-window optimization, not a replacement for storage.

### Cross-Entity Relationships

- `chat_session.scope_id` → `organization.id` (org scope), `project.id` (project scope), or `project_task.id` (task scope).
- `chat_participant.participant_id` → `human_user.id` or `agent.id`.
- `chat_turn` links to control plane runs via `run.session_id` + `run.turn_id` in the control plane schema (doc 16).
- Task-scoped sessions link to flow nodes: when a flow node begins execution, a session is auto-created. This link lives on the flow execution side (`flow_node_execution → session_id`), not on the chat_session table.

### What's NOT in the Chat Schema

- **Tool execution details** (sandbox info, policy decisions, timing) — live in the control plane's `run`/`tool_execution` tables. The chat `tool_call`/`tool_result` messages are the conversation-level view.
- **Memory items** — Ellie's memory system (doc 06) has its own schema. Chat messages may be sources for memory extraction, managed by the memory pipeline.
- **Model invocation details** (retries, fallbacks, per-call latency) — live in the model gateway (doc 07). Chat turns carry aggregate token counts.

## Safety and Moderation

- Configurable content policy checks before external model calls.
- Redaction pipeline for sensitive output.
- Admin controls for retention and legal hold.

## Resolved Decisions

- **Session-to-task binding**: one session has one scope (org, project, or task). A task can have multiple sessions. Scope is immutable after creation.
- **Session branching**: no hard forks within a session. If work diverges, create a new session at the same scope. Sessions are cheap.
- **Agentic flow nodes get sessions**: when a flow node with an agent actor begins execution, a task-scoped session is automatically created for that node. This is the agent's workspace for that step.
- **Agents don't escalate sessions**: if an agent discovers a concern beyond its task scope, it files a new task (assigned to the project manager) with a dependency link back to its own task. The PM triages in a project-scoped session, escalating to Frank or the human if needed. Agents stay within their session scope.
- **Sync/async session modes**: synchronous sessions (human present) pre-assemble context for low latency. Asynchronous sessions (autonomous agent work) allow tool-based discovery over multiple turns. Latency is acceptable in async; quality and thoroughness are the priority.
- **Multi-agent coordination**: each scope has a default responder (Frank for org, PM for project, PM (or task lead) for task). Other present agents listen and can interject via a lightweight eval pass when they have something urgent to contribute. No round-robin, no free-for-all.
- **One loop, two entry points**: the tool-call loop is the agentic loop. Same mechanics sync and async, different triggers. Sync: human message. Async: system kick.
- **No mid-turn approval**: permissions are pre-configured. Policy check at runtime returns allow or deny — always immediate and binary. The turn never blocks waiting for human input. Communication tools that create drafts do so as their designed behavior (tool-level, not policy-level).
- **Stop conditions**: max tool calls (counter) and max turn duration (soft enforcement at loop boundaries). Tokens tracked but not a gate.
- **Turn cycle is atomic**: three phases (responder → listening eval → interjections). One round of evals per human message. Interjections don't trigger more evals.
- **Cancel is explicit**: sending a new message does not cancel the in-progress turn. New messages queue up and are processed serially.
- **Steer promotes one message**: cancels current turn and promotes a specific queued message to the front. Other queued messages keep their order.
- **Queued messages are editable** while waiting to be processed.
- **Multi-human shared queue**: single queue ordered by submission time. Edit/steer controls apply to your own messages only.
- **Storage**: PostgreSQL is the source of truth. JSONL is export only. Artifacts in object storage.
- **Messages are append-only**: content never edited after `final`. `redacted` zeroes content, preserves row.
- **Progressive summarization**: automatic, not panic-driven. Summaries replace message ranges in context assembly. Full messages always preserved in storage.
- **Each message has one author**: no co-authored outputs.
- **Summaries are immutable**: create new ones rather than replacing. Context assembly uses the most recent for a given range.
- **One session per scope, not many**: org gets one ("General"), each project gets one, each task gets one sync session. Per-node async sessions are agent work logs, not human-facing chat. No "which session?" ambiguity.
- **Task sync session default responder is the PM**: the persistent task session is for discussing work, not doing it. PM (or task lead) is the default responder. Agents assigned to the task are participants that can be @mentioned.
- **Substantive discussions are tasks**: architecture decisions, sprint planning, design reviews — these are tasks with their own sessions and flow, not project-level chat. The project session stays slim (PM oversight, triage, quick questions).
- **Chat pane is always visible**: persistent right pane, context-bound by default to current navigation. Human can independently switch via sidebar without changing main content.
- **Scope pill for zoom in/out**: three-segment pill at the top of the chat pane ([Task] [Project] [Org]) lets the human switch scope levels without changing main content. Segments are contextually available — only Org if on dashboard, Project+Org if on a project, all three if on a task.
- **Viewing context hint**: when the chat pane scope differs from the main content, the client sends viewing context metadata with each message. Agents see what the human is looking at. Ephemeral, not persisted.
- **Notifications are event bus consumers**: subscribe to events, apply human preferences, deliver in-app. Urgency tiers: urgent (real-time), normal (real-time, groupable), low (batched digest). Push and email deferred until those platforms exist.
- **Reactions are bidirectional**: humans react to agent messages (quality feedback), agents react to human messages (acknowledgment/signal), agents react to other agents (agreement/disagreement). All feed into Ellie's memory pipeline. Simple positive/negative sentiment with optional note.
- **Agents can @mention other agents**: detected in response text after the turn completes. Mentioned agents get turns in Phase 1.5 of the turn cycle. One round only — no chaining. This enables agent-to-agent delegation within a cycle.
- **Session titles from scope**: org session = "General", project session = project name, task session = task title. No auto-generation needed. Human can override via optional title field.
- **Agents handle multiple sessions concurrently**: each turn is an independent model call with no shared state. Global and per-provider concurrency limits (doc 07) govern the load. No per-agent serialization.
- **Rich content via blocks**: message content is an array of typed blocks (text, code, image, file_ref, artifact), not just plain text. Supports inline images, syntax-highlighted code, repo file references, and artifact embeds. Same model for human and agent messages.
- **File uploads**: human drags/attaches a file → object storage → chat_artifact → referenced in message as an artifact block. Prompt assembly handles inclusion based on file type and model capabilities (vision for images, extraction for text, reference-only for binary).
- **Reaction UX**: double-click to thumbs-up (quick positive), right-click for thumbs-down or feedback with note (deliberate negative).
- **Agent reactions during listening eval**: agents that don't need to interject can still leave a reaction during the existing eval pass. Near-zero additional cost.
- **Notifications and inbox are distinct**: inbox is action-required items. Notifications are awareness. Every inbox item generates a notification, not vice versa.
- **Each text segment is its own message**: agent text between tool calls becomes a separate message. Tool calls and results are discrete messages. Gives clean lifecycles and maps naturally to the streaming protocol.
- **Agent is told when context is compressed**: continuation messages explicitly inform the agent that context was summarized and direct it to query Ellie for anything uncertain. Never pretend full history is present. Prevents hallucination to fill gaps.
- **Summary threshold starts at 50-60%**: when conversation history exceeds ~50-60% of its layer 6 budget, summarize the oldest ~25-30% of unsummarized turns. Starting point, tuned from real usage.
- **Crash recovery preserves all work**: every tool call and result is persisted immediately. A retry turn after a crash sees everything the agent did. No work lost.
- **Two-tier tool execution**: read-only internal tools (file reads, memory queries, context lookups) execute in the chat layer with basic permission checks. Mutations, external calls, system tools, and communications go through the full control plane (policy evaluation is binary: allow/deny). Tier is set at tool registration, never a runtime decision. Keep tier 1 minimal — when in doubt, it's tier 2.
- **Sessions close when flow nodes complete**: always close the session, always open a new one for the next node. Valuable content from the closed session is extracted and injected into the new session's context via scope context (layer 3) and Ellie's memory — not by reopening the old session. If a reviewer rejects work and the flow loops back, the rework agent gets a fresh session with curated context (reviewer feedback, relevant extracts from the prior attempt) rather than inheriting a stale conversation.
- **Session lifecycle cleanup is three-phase**: immediate (finalize turns, trigger extraction), deferred daily (ephemeral purge, summary consolidation, tool result compaction), and retention-enforced (doc 13 daily job, default 90 days). Summaries retained indefinitely. Valuable context extracted to memory before raw data ages out. Inspired by context engineering lifecycle garbage collection patterns.
- **Summarization preserves restorable references and failure evidence**: the summarization prompt explicitly preserves file paths, URLs, artifact IDs, git refs, and entity names verbatim (enabling context restoration from references). Failures, errors, and rejected approaches are also preserved so agents don't repeat dead ends. Inspired by Manus restorable compression and failure evidence patterns.

## Open Questions

_None currently outstanding._
