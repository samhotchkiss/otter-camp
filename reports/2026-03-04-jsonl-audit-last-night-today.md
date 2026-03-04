# JSONL Run Audit (Mar 3-4, 2026)

## Scope
- Files audited: `/Users/sam/dev/otter-camp/.autowork/run-20260303-*.jsonl`, `/Users/sam/dev/otter-camp/.autowork/run-20260304-*.jsonl`
- Validator: `scripts/lib/run-jsonl-audit.sh`
- Date: 2026-03-04

## Before Repair
- `summary total=21 ok=19 missing=2 repaired=0 remaining_missing=2`
- Missing terminal runs:
  - `run-20260303-120613.jsonl`
  - `run-20260304-162412.jsonl`

## Repair Pass
- Command: `scripts/lib/run-jsonl-audit.sh --repair ...`
- `summary total=21 ok=19 missing=2 repaired=2 remaining_missing=0`
- Synthetic terminal event appended:
  - `type=run.interrupted`
  - `reason=jsonl_audit_missing_terminal`
  - `source=run-jsonl-audit.sh`

## Final Verification
- `summary total=21 ok=21 missing=0 repaired=0 remaining_missing=0`
- Result: zero ambiguous `turn.started`-without-terminal runs in the Mar 3-4 sample set.

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
