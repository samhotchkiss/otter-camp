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
