## Runtime Quality Metrics

### Goal

Measure whether the system is producing efficient, reliable work rather than just eventually succeeding.

### Core Principle

OtterCamp should track a small set of operational quality signals as first-class runtime metrics.

### Recommended Metrics

- retries per successful task close
- blocked-review resumes per eventual approval or reject
- turns per accepted deliverable
- acceptance-gate failure rate by task type
- repeated synthetic prompt count per lane

### Why This Matters

Without a few hard metrics, the system can look successful while still wasting huge amounts of time and tokens.

### Direction

- collect a small high-signal set
- tie them to task type / lane type / failure family
- use them to identify where the runtime is noisy or inefficient

### Working Notes

- 2026-03-29 03:31 MDT - Triaged as the measurement layer for `add-0328c`, `add-0328e`, and `add-0328h`, not as a standalone first patch.
- Likely touchpoints: existing rollup / reporting tables, jobqueue logs, model-usage reporting, and any normalized failure-family metadata we promote from the turn engine.
- Integration plan: start with a small derived metric set from existing turn/job data once proof-of-progress and loop-threshold event shapes are stable.
- Status: staged after the first progress / loop / supervisory-stop slices land so the metrics report on durable semantics instead of transient patch-specific signals.
- 2026-03-29 14:18 MDT - Picked up the first narrow implementation slice using the existing `db token-usage` report instead of introducing a new schema.
- 2026-03-29 14:18 MDT - Added a derived `repeated_synthetic_prompts` section in [`cmd/ottercamp/main.go`](/Users/sam/dev/otter-camp/cmd/ottercamp/main.go). It groups synthetic user prompts by session + source, counts how many fired in the window, how many were consumed by a terminal turn, and how many of those ended `validation_loop_blocked`. This is the first concrete metric from the note's recommended set: repeated synthetic prompt count per lane. Focused CLI integration coverage is being widened in [`cmd/ottercamp/main_db_integration_test.go`](/Users/sam/dev/otter-camp/cmd/ottercamp/main_db_integration_test.go).
- 2026-03-29 14:19 MDT - Focused CLI integration coverage is green:
  - `GOFLAGS='' go test -tags=integration ./cmd/ottercamp -run '^TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
  - the report now exposes `repeated_synthetic_prompts` in JSON, including `source`, total synthetic prompt count, consumed prompt count, and validation-loop-blocked terminal turns for repeated synthetic lanes
- 2026-03-29 17:00 MDT - Picked up the next narrow metrics slice in the same `db token-usage` report. The first metric told us where repeated synthetic prompts were happening, but it still did not answer the review-specific efficiency question from this note: how many blocked review resumes does a lane burn before it finally approves or rejects.
- 2026-03-29 17:00 MDT - [`cmd/ottercamp/main.go`](/Users/sam/dev/otter-camp/cmd/ottercamp/main.go) now exposes a derived `review_resume_outcomes` section. It groups synthetic `task_review_action` prompts by session, counts total/consumed/validation-blocked review resumes, and joins that against the latest assistant `flow.review_decision` tool call so the report shows the eventual decision (`approve` / `reject`) alongside the retry cost that led to it.
- 2026-03-29 17:00 MDT - Focused CLI integration coverage is green in [`cmd/ottercamp/main_db_integration_test.go`](/Users/sam/dev/otter-camp/cmd/ottercamp/main_db_integration_test.go):
  - `GOFLAGS='' go test -tags=integration ./cmd/ottercamp -run '^TestDBTokenUsageJSONIncludesCacheReadsAndAttribution$' -count=1`
  - the JSON report now exposes both `repeated_synthetic_prompts` and `review_resume_outcomes`, with the latter proving a lane that spent one blocked review resume before an eventual `reject`
