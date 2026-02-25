# Task 109: tmux compatibility and keybinding fallback hardening

Layer: L2
Effort: M
Depends on: 104, 106, 107, 108

## Context

Doc 17 explicitly includes tmux in the terminal matrix. Key combinations (Alt/Ctrl variants), resize behavior, and redraw cadence can differ in multiplexers and cause feature regressions unless explicitly hardened.

## Required Fix

Harden TUI behavior for tmux and similar multiplexed terminals:

- Validate and harden core actions in tmux:
  - panel focus movement
  - chat send/cancel/queue actions
  - sidebar/main navigation
  - command palette access
  - Frank jump paths (`Ctrl-G`, `0`, `:frank`)
- Add/verify fallback-first command paths for actions vulnerable to terminal key translation differences (`:focus`, palette commands, explicit nav commands).
- Ensure resize events under tmux panes update size class/layout deterministically.
- Ensure status/help surfaces expose fallback commands whenever modifier key reliability is uncertain.
- Add tmux execution notes/checklist to implementation docs used by builders/reviewers.

## Acceptance Criteria

- [ ] Core navigation and chat workflows are fully keyboard-operable inside tmux
- [ ] No required action depends solely on unreliable modifier behavior
- [ ] Resize/layout transitions work correctly when tmux pane size changes
- [ ] Frank fast path remains available in tmux via at least two independent routes
- [ ] tmux compatibility checks are part of required verification flow
- [ ] `go build ./...` passes

## Required Tests

- Integration: scripted tmux session validates core navigation + chat + Frank jump
- Integration: tmux pane resize events trigger correct size-class transitions
- End-to-end: command-palette fallback workflow in tmux-only key path
- Regression: non-tmux terminal behavior remains unchanged
