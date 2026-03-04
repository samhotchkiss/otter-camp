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
