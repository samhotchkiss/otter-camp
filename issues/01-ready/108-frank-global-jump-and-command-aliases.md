# Task 108: Frank global jump and command aliases

Layer: L2
Effort: S
Depends on: 103, 106

## Context

Frank is the primary point-of-contact and doc 17 makes this a normative requirement: instant routing to Frank from any view/state, with keybinding and command fallback paths that preserve current main-content context.

## Required Fix

Implement Frank fast-path controls end-to-end:

- Global keybinding `Ctrl-G`: switch active chat to Frank (`General` org session) from any focused panel.
- Fallback keybinding `0` (zero): same action when text input is not active.
- Command alias `:frank` mapping to existing org-session behavior (`:general`).
- Preserve current main-content view/state when switching to Frank.
- Ensure sidebar keeps `General` as pinned top session and visibly highlights active Frank session.
- If Frank session load fails, display explicit toast/error with retry action; no silent failure.
- Add first-run key-strip hint for Frank jump path.

## Acceptance Criteria

- [ ] `Ctrl-G` always routes chat pane to Frank without changing main view
- [ ] `0` fallback works outside active text-input contexts
- [ ] `:frank` command works and is equivalent to `:general`
- [ ] Frank session appears pinned and active highlight updates immediately
- [ ] Failure case surfaces actionable error feedback
- [ ] `go build ./...` passes

## Required Tests

- Unit: keymap precedence tests (`Ctrl-G`, `0`, input-active guard)
- Unit: command parser alias test (`:frank` -> org session jump)
- Integration: route-to-Frank preserves main-view navigation state
- Integration: failed session load triggers error toast path
