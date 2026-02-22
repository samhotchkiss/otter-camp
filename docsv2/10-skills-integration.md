# 10. Skills Integration

## Goal

Skills are markdown instruction documents that get loaded into an agent's prompt when relevant. They shape how the agent behaves for a specific type of work.

Skills are simple by design. They are not code packages, they don't have scripts or hooks, and they don't need signing or sandboxing. They're documents. This can be refined as we learn from practice.

## What a Skill Is

A skill is a markdown file containing instructions. Examples:

- "Blog Writing Guidelines" — voice, structure, length, style rules.
- "Go Coding Standards" — conventions, error handling patterns, testing expectations.
- "Code Review Checklist" — what to look for, how to give feedback.
- "API Design Conventions" — naming, versioning, error response format.

A skill has:

- Metadata: name, owner, scope.
- Content: the markdown instructions themselves.

That's it.

## Skill Attachment Points

- Org default skills (apply to all work in the org).
- Project-level skills (apply to all work in the project).
- Agent-level skills (part of the agent's profile).
- Flow node skills (declared on the flow node in the flow template).

## Activation vs Availability

An agent may have many skills available across its profile, project, and org. But only skills relevant to the current work are **activated** and loaded into the prompt.

- **Flow node skills**: when a flow node declares required skills, only those skills (plus any mandatory org/project defaults) are activated for that step. An agent writing a blog post doesn't get deployment instructions. An agent building a CLI doesn't get copy guidelines.
- **Agent-level skills**: always available as a pool, but only activated when the flow node references them or when no flow node skills are declared (fallback to full agent skill set).
- **Org/project defaults**: always activated (e.g., safety policies, coding standards). These are mandatory regardless of flow node.

## Resolution Order (for conflicts)

When multiple activated skills conflict, precedence follows:

1. Flow node skills
2. Agent-level skills
3. Project-level skills
4. Org defaults

## Skills vs Procedural Memory

Skills and procedural memory both tell an agent "how to do things," but they differ in origin and authority:

- **Skills** are human-authored and prescriptive. Someone deliberately wrote the instructions. They go in the skills prompt layer and are treated as directives.
- **Procedural memory** is system-extracted and advisory. Ellie observed what worked in practice and stored it as a learned pattern. It goes in the memory prompt layer and the agent can use it or ignore it.

If they conflict, the skill wins — it's the explicit policy. Procedural memory fills the gaps that skills don't cover.

## Open Questions

- Do we need skill versioning, or is "latest content wins" sufficient to start?
- Should there be a maximum size for a skill document (token budget impact)?
