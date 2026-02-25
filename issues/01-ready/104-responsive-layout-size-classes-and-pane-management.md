# Task 104: Responsive layout size classes and pane management

Layer: L2
Effort: M
Depends on: 103

## Context

Doc 17 requires the TUI to remain fully operable across XS/S/M/L/XL terminal sizes (phone through 32" monitor), with explicit rules for pane visibility and recoverability. Without this, keyboard flows and panel access break on narrow terminals.

## Required Fix

Implement responsive layout behavior based on terminal dimensions:

- Add deterministic size class selector:
  - XS: `<=69 cols` or `<=22 rows`
  - S: `70-99 cols`
  - M: `100-139 cols`
  - L: `140-199 cols`
  - XL: `>=200 cols`
- Implement layout modes:
  - XS: single-pane mode with fast pane switching
  - S: main+chat with sidebar drawer
  - M: compact three-pane with collapsed-by-default sidebar
  - L: standard 20/40/40
  - XL: wide layout with readability gutters/max line width
- Ensure hidden panes always expose a discoverable reopen path via keybindings/command palette.
- Add status-bar hints that identify hidden pane state and recovery action.
- Enforce accessibility rule: main and chat reachable within <=2 key actions from any state.

## Acceptance Criteria

- [ ] All size classes switch automatically on terminal resize
- [ ] XS/S layouts keep app fully operable without modifier-heavy shortcuts
- [ ] Hidden panes display explicit reopen hints in status/help surfaces
- [ ] Panel focus and navigation remain valid across size class transitions
- [ ] No view becomes unreachable due to width constraints
- [ ] `go build ./...` passes

## Required Tests

- Unit: size-class selector boundary tests (all threshold edges)
- Unit: pane visibility/focus invariants per size class
- Golden render: snapshot tests for XS/S/M/L/XL board + chat + inbox shells
- Integration: resize-event tests validating layout transitions without panic
