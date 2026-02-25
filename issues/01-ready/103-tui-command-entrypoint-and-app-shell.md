# Task 103: TUI command entrypoint and Bubble Tea app shell

Layer: L2
Effort: M
Depends on: 068

## Context

Doc 17 defines the TUI as a first-class mode of the single CLI binary (`ottercamp tui`), not a separate executable. The runtime needs a stable foundation before feature views can be added: command wiring, root model lifecycle, panel focus control, and persisted local UI state.

## Required Fix

Implement the TUI runtime foundation:

- Add `ottercamp tui` command entrypoint in `cmd/ottercamp` and wire it into help output.
- Initialize a Bubble Tea root program with three panels (sidebar, main, chat) and a status bar.
- Implement global focus controls:
  - `Tab` / `Shift-Tab` cycle panels
  - `Alt-1/2/3` direct panel focus
  - `:focus sidebar|main|chat` fallback command path
- Add clean program shutdown (`Ctrl-C`, `:quit`) with state persistence.
- Persist/restore UI state to XDG config path (`~/.config/ottercamp/tui-state.json` fallback), including:
  - last active view
  - last active chat session
  - panel proportions
  - sidebar visibility

## Acceptance Criteria

- [ ] `ottercamp tui` is visible in CLI help and launches interactive mode
- [ ] TUI starts with three visible panes and status bar (no blank screen/panic)
- [ ] Focus controls (`Tab`, `Shift-Tab`, `Alt-1/2/3`, `:focus`) work deterministically
- [ ] Exiting TUI persists state; relaunch restores saved state
- [ ] `go build ./cmd/ottercamp` passes
- [ ] `go build ./...` passes

## Required Tests

- Unit: keymap/focus transition tests for all global focus actions
- Unit: state serialization/deserialization and XDG path resolution
- Integration: launch/quit cycle restores prior state in a temp config dir
- CLI smoke: `ottercamp tui --help` and non-interactive startup validation path
