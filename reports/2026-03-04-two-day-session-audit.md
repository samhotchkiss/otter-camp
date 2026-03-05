# Otter Camp Two-Day Session Audit

Date: 2026-03-04 (America/Denver)
Window analyzed: 2026-03-02 through 2026-03-04 (inclusive)

## Scope and data reviewed

### Session logs
- 20 builder/autowork session JSONL runs from `.autowork/run-2026030*.jsonl`
- 76 MCP session JSONL files tied to Otter Camp runs:
  - `~/Library/Caches/claude-cli-nodejs/-Users-sam-dev-otter-camp/...` (8 files)
  - `~/Library/Caches/claude-cli-nodejs/-Users-sam-dev-otter-camp--autowork-repos-reviewer/...` (68 files)

### Aggregate metrics (builder runs)
- Run files: 20
- Completed commands: 2,565
- Failed commands: 200
- `go test` invocations: 344
- Turns completed: 18/20 (`run-20260303-120613` and `run-20260304-141712` were incomplete snapshots)
- Queue lane move commands: 98 total, 92 success, 6 failure
- Unique task IDs touched: 35 (`187` through `222`)

### Spec references used
- Task context sufficiency and queue lifecycle: `docsv2/03-projects-and-task-flow.md` (notably lines around 92, 117, 163, 922+)
- Autonomous execution expectation: `docsv2/14-open-questions-and-phasing.md` (notably lines 356-357, 409)
- Async/session recovery semantics: `docsv2/02-chat.md` (notably lines around 301, 602, 1136)
- Retry/backoff and dead-letter behavior: `docsv2/16-agent-control-plane.md` (notably lines around 687-690)
- Testing determinism and merge gating: `docsv2/21-testing.md` (notably lines 18, 30, 478)
- Data directory standard: `docsv2/08-deployment-and-self-hosting.md` (line 444)

## Assessment against your questions

## 1) Did tasks have the right context?

### What worked
- In almost all runs, the agent explicitly loaded required run docs before implementation.
- Task files were read directly from lane folders before coding in most task passes.
- High task coverage (35 tasks touched) indicates the workflow is context-rich enough to keep throughput moving.

### Where context handling was weak
- Context bootstrap was repeated excessively. In a representative Mar 3-4 subset, the same core docs were re-read every run, adding hundreds of KB of repeated context payload.
- Some investigations used stale file assumptions (`sed`/`rg` against non-existent paths), which indicates contextual drift within long runs.
- A small number of lane operations failed due stale state assumptions (file already moved).

### Conclusion
- Context availability was generally sufficient for forward progress, but context freshness and context loading efficiency were not smooth.

## 2) Were they able to operate without human intervention?

### What worked
- The system processed a large task batch autonomously: tasks were repeatedly moved 01-ready -> 02-in-progress -> 03-needs-review by session automation.
- Supervisor recovered at least one hard reviewer-blocker case (`gh` interactive flow) without manual intervention.

### Where autonomy broke down
- Reviewer MCP integrations repeatedly failed auth in unattended mode (Gmail/Calendar), creating recurring noise.
- Frequent runner restarts (`builder_start_events=10`, `reviewer_start_events=11` in the overnight/morning slice) indicate restart churn rather than smooth continuous flow.
- Several runs spent substantial effort recovering from test instability, quoting errors, or missing path assumptions rather than delivering task work.

### Conclusion
- The system can run unattended and move work, but it is not yet “smooth autonomous”; it is “autonomous with recurring operator-grade friction.”

## 3) Did they make good decisions?

### Positive decisions
- When GitHub returned HTTP 500 during push, the runner retried, logged blocker context, and continued with next actionable work.
- Lane discipline was mostly good (92/98 move ops succeeded).
- Work continued despite partial infra instability.

### Weak decisions observed
- Repeated broad test invocations (`go test ./...`) despite known unrelated failures reduced signal-to-noise.
- Some commands used unsafe shell quoting for long markdown payloads, causing shell evaluation side effects.
- Multiple stale-path probes consumed cycles before converging on correct files.

### Conclusion
- Decision quality was mixed: resilient at macro-level, inefficient at micro-level.

## 4) Did they follow set parameters?

### Mostly followed
- Lane order and task movement conventions were generally respected.
- Base branch targeting and per-task branch flows were commonly followed.
- Non-interactive behavior was mostly maintained.

### Deviations / rough edges
- A reviewer blocker was triggered by `gh` interactive behavior (then auto-recovered).
- Some lane move commands failed due race/stale assumptions.
- Quoting issues in PR command construction violated safe command hygiene.

### Conclusion
- Parameter compliance is acceptable but brittle under concurrency/retry pressure.

## Spec alignment summary

## Aligned
- Work moved through queue/lane lifecycle and autonomous execution loops in practice.
- Recovery mechanisms exist and were exercised (blocker detection + restart).

## Not fully aligned
- **Autonomous smoothness gap** vs expected “work progresses without human intervention”: too much churn in restart/recovery loop (`docsv2/14-open-questions-and-phasing.md`).
- **Async reliability gap**: repeated continuation/status-transition failures (`docsv2/02-chat.md`, `docsv2/16-agent-control-plane.md`).
- **Testing discipline gap**: broad flaky/unrelated failures repeatedly hit despite determinism requirements (`docsv2/21-testing.md`).
- **Data path standard gap**: session JSONLs still defaulting to repo-local `.autowork` instead of centralized data dir intent (`docsv2/08-deployment-and-self-hosting.md:444`).

## Issues filed from this audit

- `222` Standardize session JSONL storage path (`~/otter-data/sessions`)
- `223` Prevent queue claim races + idempotent lane moves
- `224` Disable/gate unauthenticated MCP connections in headless runs
- `225` Add path discovery guardrails for command execution
- `226` Enforce safe shell quoting for PR/note commands
- `227` Add scoped test gating policy to reduce unrelated fail noise
- `228` Cache immutable run context to reduce repeated doc rereads
- `229` Make turn continuation completion idempotent (invalid status transition race)

## Highest-impact changes before rerun later today

1. Disable unauthenticated MCP integrations for headless reviewer runs (Issue 224).
2. Apply scoped test gating to avoid full-suite noise during task runs (Issue 227).
3. Harden queue claim/move idempotence to eliminate lane race failures (Issue 223).
4. Fix turn continuation idempotency regression (Issue 229).
5. Move session logs to centralized data path and keep watchdog/runner in sync (Issue 222).

## Preflight checklist for today’s rerun

- Confirm no pending reviewer MCP auth errors in startup logs.
- Confirm lane claim test passes (parallel claim simulation).
- Confirm continuation regression tests pass in `internal/turn`.
- Confirm runner startup does not reprint full build/context docs when unchanged.
- Confirm task-scoped test matrix runs and reports scoped-vs-baseline failures separately.

