## Validation Outputs As First-Class Artifacts

### Goal

Preserve validation evidence as durable project state, not just chat conclusions.

### Core Principle

When validation happens, the system should store:

- what was tested
- what passed
- what failed
- what evidence was used
- which acceptance gate the result maps to

### Why This Matters

If validation only lives in transcript text, it is harder to:

- recover correctly
- reason about failures
- review prior decisions
- audit what was actually proven

### Direction

- Persist structured validation summaries.
- Tie them to tasks, reviews, and acceptance gates.
- Make them available to later review and recovery flows.

### Working Notes

- 2026-03-29 03:31 MDT - Strong fit with the review / recovery hardening work. Current validation evidence still lives mostly in transcript text and ad hoc checkpoint metadata.
- Likely touchpoints: review decision payloads, checkpoint metadata in `internal/turn/engine.go`, and any future acceptance-gate records.
- Integration plan: persist structured validation summaries keyed by task + gate + evidence refs, then feed them into review and recovery prompts instead of re-deriving proof from transcript text.
- Status: staged behind acceptance-gate shape definition so the stored artifacts map to stable gate identifiers.
- 2026-03-29 16:12 MDT - Picked up the first narrow implementation slice on the existing `review_decision` metadata path instead of introducing a new table. `internal/repo/flow_execution_metadata.go` now carries structured `validation_summary`, `acceptance_criteria`, and `evidence_refs` fields on `FlowExecutionReviewDecision`.
- 2026-03-29 16:12 MDT - `internal/tools/native/mutation_tools.go` now persists those fields from `flow.review_decision`:
  - `validation_summary` is derived from `findings` first, then `reason`
  - `acceptance_criteria` can be passed explicitly on the tool call and falls back to task-description extraction when absent
  - `evidence_refs` is stored as a deduped string list
- 2026-03-29 16:12 MDT - Focused native integration coverage is green in `internal/tools/native/native_integration_test.go`:
  - `GOFLAGS='' go test -tags=integration ./internal/tools/native -run 'TestIntegrationFlowReviewDecision(ApproveWithEmptyReviewCommit|RejectCreatesCanonicalRejectionCommit)$' -count=1`
- 2026-03-29 16:12 MDT - This is intentionally the first persistence slice, not the full artifact model. The next likely widening is to feed the stored validation summary/evidence back into recovery and PM continuation prompts so later lanes stop re-deriving proof from transcript text.
