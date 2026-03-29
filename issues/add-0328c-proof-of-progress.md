## Proof Of Progress Requirements

### Goal

Do not treat an active lane as healthy merely because it is still running.

OtterCamp should require regular proof that a lane is producing real progress, not just consuming turns and tokens.

### Core Principle

A lane should periodically produce at least one of the following:

- a durable artifact change
- a passed acceptance check
- a narrowed blocker with actionable next step
- a real flow transition

If none of those are happening, the lane should be treated as suspect even if it is technically still active.

### Why This Matters

Without explicit proof-of-progress rules, the system can spend long periods:

- rereading
- re-explaining
- retrying the same pattern
- producing narrative summaries
- staying "busy" without moving the project forward

### Direction

- Define what counts as durable progress by node type.
- Track recent progress signals per active execution.
- Use absence of progress as an input to watchdog / PM / replanning decisions.

### Working Notes

- 2026-03-29 03:31 MDT - Immediate hardening leverage. The runtime already detects many failure families, but it still does not require periodic proof beyond "the lane is active" or "a checkpoint exists."
- Likely touchpoints: turn-engine checkpoint metadata, tool-validation results in `internal/turn/engine.go`, and watchdog / recovery decisions in `internal/jobqueue/worker.go`.
- Integration plan: define lane-specific progress signals (artifact mutation, acceptance result, blocker narrowing, flow transition) and let missing progress feed supervisor / replan decisions.
- Status: active follow-on candidate after the bounded-contract and supervisory-stop slices.
