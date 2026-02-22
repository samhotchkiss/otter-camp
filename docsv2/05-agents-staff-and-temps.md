# 05. Agents (Staff and Temps)

## Agent Classes

- Staff agents: durable, reusable, project-level or org-level defaults.
- Temp agents: ephemeral, task- or session-scoped, auto-expiring.

## Agent Profile Shape

- Identity metadata (name, slug, role, description).
- Prompt pack (system prompt, policies, defaults).
- Tool policy (allow/deny lists).
- Model policy (allowed model profiles, budget caps).
- Memory policy (read/write scopes).

## Lifecycle

- `draft` -> `active` -> `paused` -> `retired`
- Temp lifecycle includes automatic cleanup and archival summary.

## Assignment Rules

- Project can assign planner/worker/reviewer staff agents.
- Chat can invite staff or temp agents.
- Task can override owner agent per flow step.

## Temp Agent Use Cases

- Specialized one-off execution.
- Burst support in large workflows.
- Controlled experiments with custom prompts.

## Agent Runtime Model (Prompt Assembly)

The agent profile defines what an agent IS. The runtime model defines what prompt an agent actually receives when it takes a turn in a session. This is the core of the system.

### Prompt Layers

When an agent gets a turn, OtterCamp assembles a prompt from these layers, in priority order (highest priority = last to be cut if token budget is tight):

1. **Agent identity** (highest priority — never cut)
   - Source: prompt pack from agent profile.
   - The agent's core system prompt — who it is, how it thinks, its role.

2. **Policies and constraints** (never cut)
   - Source: org policy + project policy + agent tool policy + control plane capabilities.
   - What the agent is and isn't allowed to do. Safety-critical.

3. **Scope context**
   - Source: determined by session scope (see 02-chat.md Session Scoping).
   - Org scope: project portfolio summary, org-level priorities.
   - Project scope: project description, task summaries, architecture decisions.
   - Task scope: the task's structured context (files, decisions, acceptance criteria, dependencies).
   - Always present in sync mode. In async mode, serves as the starting context — the agent can discover more via tools.

4. **Skills instructions**
   - Source: resolved from active skills only (see 10-skills-integration.md Activation vs Availability).
   - Only skills relevant to the current flow node are loaded. An agent writing a blog post doesn't get deployment instructions.
   - Org/project default skills (e.g., coding standards, safety policies) are always included.

5. **Memory** (budget-dependent)
   - Source: Ellie's passive retrieval, scoped to the session's scope.
   - In sync mode: top-k results injected automatically. k shrinks if budget is tight.
   - In async mode: initial injection, supplemented by active Ellie queries on subsequent turns.

6. **Conversation history** (budget-dependent)
   - Source: session messages.
   - Recent turns in full, older turns summarized at checkpoints.
   - Compressed further if budget requires it.

7. **Tool descriptions** (budget-dependent, lowest priority)
   - Source: available tools filtered by agent's tool policy + control plane capabilities.
   - Rarely an issue in practice — tool descriptions are small relative to other layers.

### Assembly Process

1. **Reserve**: calculate fixed-cost layers (identity + policies). These always go in.
2. **Allocate**: remaining token budget splits between scope context, skills, memory, conversation, and tools.
3. **Fill**: populate each layer up to its allocation. Unused budget flows to lower-priority layers.
4. **Compress**: if still over budget, summarize conversation history further. If still over, reduce memory injection. Identity and policies are never cut.

### Sync vs Async Differences

- In **sync mode**, the full prompt is pre-assembled before the model call. Latency matters.
- In **async mode**, the first turn gets an assembled prompt, but the agent can then spend subsequent turns using tools to discover more context (reading files, querying Ellie, exploring the codebase). The initial prompt is a starting point, not the full picture.

## Guardrails

- Temp agents cannot exceed default org policy envelope.
- Restricted secret and connector access by default.
- Auto-revoke credentials after TTL expiry.

## Open Questions

- Can temp agents become staff agents (“promote” flow)?
- Should temps inherit memory context, and if so from where?
- How many concurrent temp agents per org/project do we allow?

