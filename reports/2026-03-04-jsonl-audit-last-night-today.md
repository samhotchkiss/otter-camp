# Autowork Run Audit (Mar 3-4, 2026)

## Scope
- Files audited: `/Users/sam/dev/otter-camp/.autowork/run-20260303-*.jsonl`, `/Users/sam/dev/otter-camp/.autowork/run-20260304-*.jsonl`
- Date: 2026-03-04

## Run JSONL Terminal-State Audit
- Validator: `scripts/lib/run-jsonl-audit.sh`

### Before Repair
- `summary total=21 ok=19 missing=2 repaired=0 remaining_missing=2`
- Missing terminal runs:
  - `run-20260303-120613.jsonl`
  - `run-20260304-162412.jsonl`

### Repair Pass
- Command: `scripts/lib/run-jsonl-audit.sh --repair ...`
- `summary total=21 ok=19 missing=2 repaired=2 remaining_missing=0`
- Synthetic terminal event appended:
  - `type=run.interrupted`
  - `reason=jsonl_audit_missing_terminal`
  - `source=run-jsonl-audit.sh`

### Final Verification
- `summary total=21 ok=21 missing=0 repaired=0 remaining_missing=0`
- Result: zero ambiguous `turn.started`-without-terminal runs in the Mar 3-4 sample set.

## Command Outcome Taxonomy Audit
- Classifier: `scripts/lib/command-outcome.sh`
- Audit tool: `scripts/lib/command-outcome-audit.sh`
- Taxonomy:
  - `lookup_miss`
  - `search_miss`
  - `build_or_test_failure`
  - `infra_failure`

### Aggregate Result
From `scripts/lib/command-outcome-audit.sh ...`:

`aggregate files=21 total=2237 success=2046 nonzero=191 lookup_miss=40 search_miss=13 build_or_test_failure=35 infra_failure=103 hard_blockers=138`

Interpretation:
- Exploratory misses (`lookup_miss + search_miss`) = `53`
- Hard blockers (`build_or_test_failure + infra_failure`) = `138`

### Sample Per-Run Evidence
- `run-20260303-114413.jsonl`:
  - `total=141 success=120 nonzero=21 lookup_miss=3 search_miss=4 build_or_test_failure=8 infra_failure=6 hard_blockers=14`
- `run-20260303-123615.jsonl`:
  - `total=300 success=274 nonzero=26 lookup_miss=4 search_miss=1 build_or_test_failure=2 infra_failure=19 hard_blockers=21`
- `run-20260304-162412.jsonl`:
  - `total=66 success=61 nonzero=5 lookup_miss=3 search_miss=0 build_or_test_failure=1 infra_failure=1 hard_blockers=2`

### Runner Policy Updates
- Runner prompts require safe non-blocking exploratory search forms (`|| true`) where appropriate.
- Runner emits end-of-run `command-outcome-summary ...` with taxonomy counters.
- Exploratory misses are separated from hard blockers in structured run output.

## GitHub Retry Hardening (Task 232)
- Added shared retry wrapper: `scripts/lib/github-retry.sh`
- Supported wrapped commands:
  - `scripts/lib/github-retry.sh git push ...`
  - `scripts/lib/github-retry.sh gh pr create ...`
  - `scripts/lib/github-retry.sh gh pr edit ...`
- Retry behavior:
  - Bounded exponential backoff + jitter for transient classes (`transient_http_5xx`, `transient_network`)
  - Fail-fast for non-retryable classes (`permanent_auth_or_permission`, `permanent_invalid_args`)
- Structured retry logging format (attempt counts + terminal reason):
  - `github_retry attempt=1/5 action=retry classification=transient_http_5xx backoff_seconds=... exit_code=...`
  - `github_retry attempt=3/5 action=success terminal_reason=success`
  - `github_retry attempt=1/5 action=fail_fast classification=permanent_auth_or_permission terminal_reason=non_retryable exit_code=...`
- Regression test: `scripts/lib/github-retry-test.sh` (pass)

## Queue Reconciliation Hardening (Task 234)
- Added shared reconciliation primitive in queue helpers:
  - `scripts/issue-lane.sh reconcile <issues_dir> <src-lane> <dst-lane> <task-file>`
  - Outputs structured outcome: `queue_reconciled` or `queue_conflict_hard_stop`
- `scripts/codex-autowork.sh` claim path now:
  - snapshots lane counts before/after claim,
  - reconciles external lane races idempotently,
  - emits structured `queue_reconciled` vs `queue_conflict_hard_stop` logs.
- `scripts/autowork-supervisor-watchdog.sh` lane move logs now emit structured reconcile outcomes with raw queue status.
- Prompt contract now includes an explicit queue-mutation reconciliation protocol (continue on `queue_reconciled`, escalate only on `queue_conflict_hard_stop`).

Validation:
- `bash -n scripts/codex-autowork.sh scripts/autowork-supervisor-watchdog.sh scripts/lib/issue-queue.sh scripts/issue-lane.sh scripts/replay-queue-ops-test.sh`
- `scripts/replay-queue-ops-test.sh`

## TestDB Drop Timeout Hardening (Task 233)
- `internal/testdb/testdb.go` teardown now:
  - terminates open sessions before each drop attempt,
  - retries drop with bounded backoff for retryable errors (`SQLSTATE 55006`, timeout/deadline variants),
  - surfaces active session PID telemetry in terminal drop errors.
- Added targeted regression coverage in `internal/testdb/testdb_test.go` for:
  - transient retry-to-success behavior,
  - fail-fast permanent errors,
  - retry exhaustion with attempt + PID telemetry in error output.

Validation:
- `go test ./internal/testdb`
- `go test ./internal/testdb -tags integration`
- `go test ./internal/testdb -tags integration -run TestNewReturnsIsolatedDatabaseAndDropsOnCleanup -count=20`

## Baseline Test Health Gate (Task 235)
- Added versioned baseline artifacts:
  - `config/autowork-baseline-test-matrix.json`
  - `config/autowork-flake-registry.json`
- Added gate evaluator consumed by autowork runner:
  - `scripts/lib/baseline-health-gate.sh`
- Added regression coverage for gate evaluator:
  - `scripts/lib/baseline-health-gate-test.sh`
- Runner output now includes:
  - `baseline-health-summary gate_status=... baseline_health_status=...`
  - `task_scope_regressions=<n>`
  - `waived_known_flakes=<n> waived_flake_refs=<flake-id-list>`
