## Explicit Stop Conditions For PM And Reviewer Lanes

### Goal

Keep supervisory lanes from turning into narration engines.

### Core Principle

PM and reviewer lanes should know when to stop thinking and take the next concrete action.

### Stop / Act Rules

- if the next action is clear, act
- if the same rediscovery family repeats, stop and hand off
- if missing information is truly blocking, ask one targeted question
- do not keep expanding context after the lane has already identified the right next move

### Why This Matters

Supervisory lanes can burn a lot of time on:

- rereads
- rediscovery
- narration
- repeated task inspection

They need sharper stopping rules than ordinary execution lanes.

### Direction

- Define PM/reviewer-specific early-stop conditions.
- Convert repeated rediscovery into deterministic transitions.
- Keep supervisory lanes focused on steering, not re-browsing.

### Working Notes

- 2026-03-29 03:31 MDT - Immediate leverage and partially underway. A large share of the Sam.blog hardening work has already been PM/reviewer stop conditions, but the rules are still scattered across family-specific guards.
- Likely touchpoints: PM/reviewer prompt builders and `shouldStop...` guard families in `internal/turn/engine.go`, plus continuation suppression in `internal/jobqueue/worker.go`.
- Integration plan: consolidate supervisory stop families into explicit lane-level rules and metrics so PM/reviewer behavior is intentionally bounded rather than only patched case by case.
- Status: first implementation candidate alongside bounded task contracts.
