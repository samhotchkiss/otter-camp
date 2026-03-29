## Separate Work Complete From Work Proven

### Goal

Prevent the system from treating implementation activity as equivalent to successful completion.

### Core Principle

"Done" should mean:

- implementation finished
- acceptance gate passed
- review decision recorded

Those are different states and should not collapse into one vague notion of completion.

### Why This Matters

Agents can produce code, content, or artifacts that look complete but have not been validated against the actual goal.

If OtterCamp does not separate "work complete" from "work proven," it will keep shipping false positives.

### Direction

- Model implementation completion separately from validation completion.
- Make review / validation outcomes part of closure semantics.
- Ensure the runtime can show whether a task is built, proven, and approved independently.

### Working Notes

- 2026-03-29 03:31 MDT - Triaged as a data-model / closure-semantics change. Current `work_status` and review flow still collapse implementation and proof in a few important places.
- Likely touchpoints: task state transitions in `internal/flow/execution_service.go`, review decision handling, and delivery / merge closeout semantics.
- Integration plan: split implementation-complete vs validation-proven semantics first, then widen reporting and PM logic so "done" does not mean merely "work was attempted."
- Status: staged behind acceptance-gate design because the "proven" half needs an explicit gate model.
