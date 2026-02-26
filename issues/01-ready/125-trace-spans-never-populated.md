# Issue 125: Trace spans never populated

## Problem
`trace_span` table always has 0 rows despite active model invocations and tool executions.
`GET /v1/observability/trace-spans` (if exposed) would return empty.

## Root Cause
`observability.TraceSpanService` exists with a `Create()` method backed by `repo.TraceSpanRepo`,
but it is never called from:
- The turn engine (`internal/turn/`)
- The model gateway (`internal/gateway/`)
- The worker (`internal/worker/`)
- Any server handler

The service is only used in integration tests (`internal/observability/trace_span_integration_test.go`).

The DB partition job does run (creates `trace_span_p_YYYYMMDD` partitions) but no data is ever inserted.

## DB Evidence
```sql
SELECT tablename FROM pg_tables WHERE tablename LIKE 'trace_span%';
-- trace_span, trace_span_p_20260304, trace_span_p_20260305

SELECT COUNT(*) FROM trace_span;
-- 0
```

## Fix Options
1. **Wire TraceSpanService into gateway**: Record a span per model invocation with trace_id from turn/session context
2. **Wire TraceSpanService into turn engine**: Record spans for each agent turn lifecycle step
3. **Stub/defer**: Trace spans are observability infrastructure; if the feature isn't critical for v1, document as future work

## Priority: LOW
No API endpoint exposes trace spans to users yet. Primary observability is through /metrics and /v1/audit.
