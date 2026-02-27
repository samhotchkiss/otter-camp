# Task 181: GET /v1/trace/spans returns 404 — trace inspection not accessible

Layer: L2
Effort: S
Depends on: none

## Context

The spec implies trace spans should be accessible for observability. `GET /v1/trace/spans` returns 404.

Trace spans ARE collected internally (the trace_span table exists in the database, and Issue #112 fixed the partition issue). But the API endpoint to query them is not implemented.

## Required Fix

1. Add `GET /v1/trace/spans` endpoint
2. Support filtering by: run_id, task_id, agent_id, time range
3. Return span list with: span_id, parent_span_id, operation, duration_ms, metadata, created_at

## Acceptance Criteria

- [ ] `GET /v1/trace/spans` returns 200 with span list
- [ ] Filter by `?run_id=` works
- [ ] Filter by `?task_id=` works
- [ ] Spans include timing and operation context
