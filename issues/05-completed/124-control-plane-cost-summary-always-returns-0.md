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

## Reviewer Required Changes (2026-02-26 08:15 UTC)
Reviewer: Claude Sonnet 4.5 (reviewer agent)

PR #1533 (`task-124-control-plane-cost-summary-rollups` → `v2`) — changes required, do not merge.

### P1
- [ ] SQL argument count mismatch causes runtime error for `group_by=agent` without `project_id`
  - Files: `internal/server/controlplane_handlers.go:queryCostSummaryRowsFromRollups` (~line 1285)
  - Required fix: When `groupBy == "agent"` the `fmt.Sprintf` produces a query with only `$1–$4` placeholders (no `$5`), but `pool.Query` is called with 5 arguments (`orgID, rollupType, periodStart, periodEnd, projectID`). PostgreSQL extended query protocol returns "bind message supplies too many parameters". Fix by building the args slice dynamically:
    ```go
    args := []any{orgID, rollupType, periodStart, periodEnd}
    if projectFilter != "" {
        args = append(args, projectID)
    }
    rows, err := h.pool.Query(ctx, fmt.Sprintf(`...`, groupExpr, projectFilter), args...)
    ```
  - Required test: Integration test calling `GET /v1/control/cost/summary?group_by=agent` (no `project_id`) after seeding `model_usage_rollup` rows with `rollup_type='agent'`; assert HTTP 200, `total_tokens > 0`, and `by_group` contains entries.

### P2
- [ ] No integration test coverage for `group_by=agent` rollup path
  - Files: `internal/server/controlplane_integration_test.go` (existing `TestControlPlaneAPICostSummaryTotals`)
  - Required fix: Extend `TestControlPlaneAPICostSummaryTotals` (or add a sibling test) to seed rows with `rollup_type='agent'` and assert that `GET /v1/control/cost/summary?group_by=agent` returns non-zero `total_tokens` and at least one `by_group` entry. Both the unfiltered agent path (which previously triggered the P1 crash) and the default `group_by=project` path must have passing integration assertions.
  - Required test: Same as fix description above.
