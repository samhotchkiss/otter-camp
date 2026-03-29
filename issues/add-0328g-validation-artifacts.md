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
