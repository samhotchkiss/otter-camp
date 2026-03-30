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
- 2026-03-29 18:56 MDT - Picked up the first narrow implementation slice instead of widening into a schema migration. The PM/runtime continuation snapshot now distinguishes implementation-complete work from proof state in reporting only:
  - completed-task snapshot wording changes from `Recently completed...` to `Recently implementation-complete...`
  - completed task refs can now carry `proof_state=approved|rejected|unrecorded`
  - proof state is derived from the latest persisted `flow_node_execution.review_decision` metadata when it exists
- 2026-03-29 18:56 MDT - This keeps the change bounded but materially improves operator/PM semantics: `work_status=done` still means the implementation lane reached terminal completion, but the snapshot no longer implies that completion alone is proof. `proof_state=unrecorded` explicitly marks implementation-complete work that does not yet have a recorded approval signal.
- 2026-03-29 18:56 MDT - Focused coverage for the reporting slice is green:
  - `GOFLAGS='' go test ./internal/turn -run 'Test(ProjectExecutionContinuationSnapshotSummarizesProjectState|BuildProjectExecutionContinuationPromptIncludesCompletedBatchSupersessionGuidance|ProjectExecutionContinuationSnapshotIgnoresMalformedNoDecomposeChildren)$' -count=1`
  - `GOFLAGS='' go test ./internal/jobqueue -run 'TestBuildProjectExecutionContinuationPromptForWorkerIncludesCompleted(BatchSupersessionGuidance|CloseoutGuidance)$' -count=1`
  - `GOFLAGS='' go test -tags=integration ./internal/jobqueue -run 'TestJobWorkerProjectExecutionContinuationSnapshot(UsesReviewProofStateForCompletedTasks|KeepsHumanContinuationPolicyOverStaleReviewGuard)$' -count=1`
- Next step: push this reporting slice, then decide whether the next `add-0328d` cut should persist task-level proof/approval semantics more directly or whether the current reporting split is sufficient for manual-test readiness.
