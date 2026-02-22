# 17. Terminal UI (TUI)

## Status: Stub — to be fleshed out.

## Purpose

A terminal-based interface for interacting with OtterCamp. Primary interface for developers and operators who prefer keyboard-driven workflows.

## Key Capabilities (to be designed)

- Chat with agents (sync mode — org, project, and task scoped sessions).
- View and manage projects, tasks, and flow status.
- Monitor agent activity and run status.
- Quick task creation and triage.
- Queue and concurrency status at a glance.

## Design Considerations

- Should feel native to terminal workflows (piping, scripting, integration with shell).
- Real-time updates for active sessions (streaming agent responses, run progress).
- Keyboard-driven navigation and commands.

## Open Questions

- What framework/library for the TUI (Bubble Tea, etc.)?
- How much of the full feature set is available via TUI vs web-only?
- Does the TUI connect directly to the API, or does it go through a local CLI layer?
- Relationship to the existing `otter` CLI — is the TUI a mode of the CLI, or a separate binary?
