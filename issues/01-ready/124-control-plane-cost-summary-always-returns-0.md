# Issue 124: Control plane cost summary always returns 0 tokens

## Problem
`GET /v1/control/cost/summary` always returns `total_tokens: 0` and empty `by_group` despite heavy model usage. The endpoint queries `run_attempt.input_tokens + run_attempt.output_tokens` but `run_attempt` has 0 rows — all 15 run_steps have `attempt_count = 0`.

The actual token usage is in `model_usage_rollup` (4.9M input tokens, 95K output), queried correctly by `GET /v1/usage/summary`.

## Root Cause
Two separate token tracking systems:
1. **Turn engine path** (always used): records to `model_usage_rollup` via `RecordInvocation`. Gives correct data at `/v1/usage`.
2. **Control plane path** (never used for token tracking): `run_attempt` would receive tokens if the worker recorded them there. Currently workers complete runs/steps but don't populate `run_attempt` rows at all.

## DB Evidence
```sql
SELECT COUNT(*) FROM run;         -- 2341
SELECT COUNT(*) FROM run_step;    -- 15
SELECT COUNT(*) FROM run_attempt; -- 0
SELECT COUNT(*) FROM model_usage_rollup; -- 24 rows with real data
```

## Fix Options
1. **Quick win**: Make `/v1/control/cost/summary` query `model_usage_rollup` instead of `run_attempt` (same data, consistent with `/v1/usage`)
2. **Proper fix**: Have the worker record a `run_attempt` when it runs tool executions, populating `input_tokens`/`output_tokens` from the associated turn invocation

## Priority: LOW
`/v1/usage/summary` provides accurate cost data. The control cost endpoint is an additional view for run-scoped cost attribution.
