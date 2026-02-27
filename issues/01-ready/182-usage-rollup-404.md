# Task 182: GET /v1/model/usage-rollup returns 404

Layer: L2
Effort: S
Depends on: none

## Context

The spec lists `GET /v1/model/usage-rollup` as an endpoint for aggregated model usage by agent/project/period. The endpoint returns 404 Not Found.

`GET /v1/usage` works correctly (returns per-agent token counts by period + group_by). But the `/v1/model/usage-rollup` endpoint, which should provide a different aggregation view, is missing.

## Required Fix

1. Implement `GET /v1/model/usage-rollup` endpoint
2. Support aggregation by: agent, project, model_provider, period
3. Return: token counts + cost breakdown per group

## Acceptance Criteria

- [ ] `GET /v1/model/usage-rollup` returns 200 with aggregated usage data
- [ ] Supports filtering by period and group dimension
- [ ] Returns cost breakdown (even if microcents are 0 for now)
