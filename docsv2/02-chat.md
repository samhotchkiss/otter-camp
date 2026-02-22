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

## Session Scoping

Every chat session has exactly one scope. The scope determines what context is injected, what agents are available, and what actions are possible.

### Scope Levels

- **Org scope**: General-purpose conversations not tied to a specific project or task. Brainstorming with Frank, asking Lori to hire a new staff agent, asking Ellie about org-wide knowledge. Context injected: org-level memory, project summaries.
- **Project scope**: Conversations about a project as a whole — architecture discussions, staffing decisions, progress reviews. Context injected: project memory + org memory, task summaries for the project.
- **Task scope**: Conversations about a specific unit of work. Working on a task, reviewing deliverables, discussing blockers. Context injected: task structured context (files, decisions, acceptance criteria) + project memory + org memory.

### Binding Rules

- A session's scope is set at creation and does not change.
- `chat_session` carries `scope_type` (org, project, task) and `scope_id` (the org, project, or task ID).
- A task can have **multiple sessions** over its lifecycle (e.g., a planning session, a work session, a review session — potentially one per flow node).
- A project can have multiple sessions — some task-scoped, some project-level.
- An org always has at least one org-scoped session available (the default home for talking to Frank).

### Scope and Agent Availability

- **Org-scoped sessions**: starter trio (Frank, Lori, Ellie) and any org-level staff agents.
- **Project-scoped sessions**: starter trio + project-assigned staff agents + project temps.
- **Task-scoped sessions**: agents assigned to the current flow node, plus Ellie (always available for context queries).

### Ellie's Dual Role

Ellie is both a background system and a conversational participant.

- **Passive**: the memory pipeline automatically injects relevant memories into session context based on scope. This happens on every turn without Ellie being explicitly invoked.
- **Active**: any agent or human can @mention Ellie to explicitly request deeper context, ask "what do we know about X?", or request memory from a scope they don't normally see. Ellie responds as a participant in the conversation.

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

## Context Management

- Per-session rolling context window.
- Periodic summarization checkpoints.
- Retrieval augmentation from project data, memory, and linked artifacts.
- Context injection is **scope-aware**: the session's scope determines which memory layers, project data, and task context are included.
- Context assembly is **mode-aware**: synchronous sessions pre-assemble context for low latency; asynchronous sessions allow tool-based discovery over multiple turns.
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

### Default Responder

Each session has a default responder determined by scope:

- **Org-scoped**: Frank (Chief of Staff — the human's primary touchpoint).
- **Project-scoped**: the project manager assigned to that project.
- **Task-scoped**: the agent assigned to the current flow node.

The default responder takes every turn unless another agent is explicitly @mentioned.

### @Mention Routing

Any participant can @mention another agent to direct a message to them. The @mentioned agent responds instead of (or in addition to) the default responder.

### Listening and Interjection

Agents who are present in a session but are not the default responder are in a **listening** state.

- Listening agents see all messages but do not respond by default.
- After the default responder completes their turn, listening agents get a lightweight evaluation pass (cheap model, e.g., Haiku-tier): "given this exchange, do you need to contribute something urgent?"
- If an agent flags yes, it gets a follow-up turn.
- If multiple agents flag, they respond in sequence.
- This keeps cost low while allowing natural interjections — like a meeting where someone can raise their hand when they have something critical to add.

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
- **Multi-agent coordination**: each scope has a default responder (Frank for org, PM for project, assigned agent for task). Other present agents listen and can interject via a lightweight eval pass when they have something urgent to contribute. No round-robin, no free-for-all.

## Open Questions

- Should every message have exactly one author, or support co-authored agent outputs?
- Should summaries be immutable snapshots or replaceable revisions?
- When a flow node completes and the next node begins, is the previous session closed/archived, or does it remain accessible for reference?

