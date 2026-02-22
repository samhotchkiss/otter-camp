# 10. Skills Integration

## Goal

- Treat skills as reusable behavior packages that can shape agent execution.

## Skill Definition

A skill contains:

- Metadata (name, version, owner)
- Instructions/policy layer
- Optional templates/assets
- Optional scripts/hooks
- Compatibility declarations

## Skill Attachment Points

- Org default skills
- Project-level skills
- Agent-level skills
- Task/session temporary skills

## Resolution Order

1. Task/session overrides
2. Agent-level skills
3. Project-level skills
4. Org defaults

## Execution Rules

- Skill effects must be explicit in execution trace.
- Conflicts use deterministic precedence rules.
- Skill activation can be policy-gated.

## Distribution and Trust

- Local/private skill registry.
- Optional signed skills for managed deployments.
- Version pinning to avoid drift.

## Open Questions

- Do we allow arbitrary scripts in managed mode?
- How strict should skill sandboxing be?
- Should skills be typed by domain or capability?

