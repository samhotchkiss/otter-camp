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
