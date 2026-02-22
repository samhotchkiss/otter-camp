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

## Guardrails

- Temp agents cannot exceed default org policy envelope.
- Restricted secret and connector access by default.
- Auto-revoke credentials after TTL expiry.

## Open Questions

- Can temp agents become staff agents (“promote” flow)?
- Should temps inherit memory context, and if so from where?
- How many concurrent temp agents per org/project do we allow?

