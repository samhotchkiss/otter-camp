# 11. System Integration (CLI and Browser Control)

## Goal

- Give agents controlled execution surfaces for terminal and browser operations.

## CLI Integration

- Command execution API with policy checks.
- Workspace-scoped file and process permissions.
- Streaming stdout/stderr and structured exit metadata.

## Browser Integration

- Browser session management.
- Navigation, interaction, extraction, screenshots.
- Optional human handoff/approval for sensitive actions.

## Shared Guardrails

- Capability permissions by org/project/agent.
- Sensitive command/domain denylist.
- Action logging with replay metadata.

## Reliability

- Bounded execution time.
- Cancellation support.
- Retry rules only for safe idempotent actions.

## UX Requirements

- Users can inspect action traces.
- Users can revoke agent control at runtime.
- Artifacts (screenshots/logs/files) linked to sessions/tasks.

## Open Questions

- Should browser execution be isolated per task or reusable per session?
- What is the minimum required sandbox model for CLI?
- Which actions require human pre-approval by default?

