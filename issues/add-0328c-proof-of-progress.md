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
- 2026-03-29 11:12 MDT - Picked up the first narrow implementation slice in `internal/jobqueue/worker.go`: repeated PM continuation suppression now checks the latest blocked continuation turn for explicit progress-bearing tool results before classifying it as "repeat with no progress."
- Initial progress signals for this slice are intentionally narrow and durable: successful `task.create`, successful `task.update`, and successful `flow.review_decision` tool results on the latest terminal continuation turn.
- Integration coverage added in `internal/jobqueue/worker_integration_test.go` for both entry points that matter here: direct `ensureProjectContinuationMessageDecision(...)` retries and worker-side `RequeueActiveProjectSessionsWithoutTurns(...)` retries.
- 2026-03-29 12:58 MDT - Picked up the next narrow proof-of-progress slice in `internal/jobqueue/worker.go`: recovery-session progress now treats successful `file.edit` the same as successful `file.write` when deciding whether a consumed recovery-resume already produced a durable artifact mutation.
- Why this slice matters: the worker previously suppressed stale recovery resumes only after completed `file.write` turns, even though many bounded repair lanes make real forward progress through `file.edit` on an existing deliverable.
- This slice widens the durable-mutation classifier used by:
  - `RequeueActiveExecutionSessionsWithoutTurns(...)`
  - `RequeueTerminalRecoveryResumeSessionsWithoutLiveExecution(...)`
  - stale `agent_turn` purge for consumed recovery resumes
- Focused integration coverage is in `internal/jobqueue/worker_integration_test.go` for successful `file.edit` paths across active requeue, terminal requeue, and stale-job purge.
- 2026-03-29 15:09 MDT - Picked up the next proof-of-progress follow-on in `internal/turn/engine.go`: task lanes that inherit a shared parent single-file deliverable now stop immediately after a successful `file.edit` against that inherited file, instead of continuing into verification churn and blocked `git.commit` handoff attempts.
- Live driver: SamBot child tasks `180` and `181` both made real durable edits to `planning/sambot-architecture.md`, but the turn-level stop logic only recognized explicit task deliverables / recovery checkpoint targets, so the lanes kept spending extra tool calls after progress was already proven.
- This slice intentionally stays narrow. It only recognizes successful `file.edit` with `replacements_made > 0` on the inherited shared single-file deliverable path resolved from the decomposition parent; it does not widen shared-path handling for unrelated writes.
- Focused turn coverage is in `internal/turn/engine_test.go` via `TestShouldStopAfterExecutionDeliverableWriteStopsForInheritedSharedSingleFileEdit`.
- 2026-03-29 15:23 MDT - Fresh live proof on `repo_version=3609` is good enough to keep the slice, but it also narrowed the next seam. SamBot task `180` turn `68f64ed4-80d8-4ba1-8ec2-378cca571612` still spent `git.status` / `git.diff` / blocked `git.commit` before the final newline fixup, but once it emitted the inherited shared-file `file.edit planning/sambot-architecture.md -> replacements_made=1`, the turn completed immediately with no further tool calls after that edit.
- Follow-on seam: the remaining waste is now earlier in the same lane family, where task sessions that already have a dirty inherited shared deliverable still try to verify and hand off through git before they make their last bounded edit.
