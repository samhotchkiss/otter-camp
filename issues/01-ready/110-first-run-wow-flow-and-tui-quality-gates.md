# Task 110: First-run wow flow and TUI quality gates

Layer: L2
Effort: M
Depends on: 104, 105, 106, 107, 108, 109

## Context

Doc 17 defines a normative first-run experience (cold-open, operator-ready dashboard, guided tour, proof-of-life events) plus release-level quality bars for latency, memory, reconnect resilience, and terminal matrix coverage.

## Required Fix

Implement first-run UX and quality gating:

- First-run detection based on absent persisted TUI state.
- Add first-run sequence:
  1. branded cold-open frame (`<=1200ms`)
  2. operator-ready dashboard landing
  3. non-blocking 2-minute tour overlay
  4. proof-of-life feed lines (`realtime connected`, `replay synced`)
- Ensure every panel has meaningful loading/empty/error/stale states on initial launch.
- Add degraded-mode banner and recovery guidance for unavailable upstream dependencies.
- Implement performance instrumentation/hooks for:
  - initial interactive paint
  - keypress-to-visible latency
  - SSE delta render latency
  - memory steady-state bound
- Enforce TUI release gate checks in CI or scripted verification harness.

## Acceptance Criteria

- [ ] First launch executes full guided sequence without blocking normal interaction
- [ ] No blank primary panel states after initial paint (placeholders or data always visible)
- [ ] Degraded-mode conditions show explicit banner and recovery action
- [ ] Instrumented metrics can verify doc 17 latency/memory budgets
- [ ] Release validation includes terminal matrix (native + tmux + XS/S + XL)
- [ ] `go build ./...` passes

## Required Tests

- End-to-end: clean-config first-run path test (cold-open -> dashboard -> tour -> proof-of-life)
- Integration: degraded upstream mode produces stale/error UX and recovery hints
- Integration: reconnect/replay stability over extended synthetic event stream
- Soak: 60-minute run with synthetic traffic shows no panic/crash
- Performance: scripted checks for first-paint, keypress latency, stream latency, memory ceiling
