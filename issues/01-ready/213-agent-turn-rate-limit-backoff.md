# 213: agent_turn job needs rate-limit-aware retry backoff

## Problem

When the model provider (Anthropic) returns a rate limit error (429), the `agent_turn` job retries with ~5-second intervals, exhausting all 3 attempts in ~15 seconds. This causes:

1. All 3 retry attempts fail immediately (rate limits don't clear in 5s)
2. Job dead-letters permanently
3. The task's run gets stuck in `in_progress` with no agent processing it
4. Supervisor recovery creates new runs, but those also fail the same way
5. System enters permanent deadlock: tasks stuck, no recovery path

This is especially severe when multiple tasks start simultaneously (e.g. 11 tasks queued at once), creating a burst of concurrent API calls that triggers rate limits across all sessions.

## Root Cause

The job queue retry interval is a fixed short duration (~5s). Rate limit errors from model providers typically require 30-120 seconds before retrying. The agent_turn handler doesn't distinguish rate limit errors from other failures for retry scheduling.

## Expected Behavior

When an agent_turn fails with `turn.ErrRateLimited`:
1. Parse the `Retry-After` header from the provider response (if available)
2. Schedule the next retry with exponential backoff: minimum 30s, then 60s, then 120s
3. Do NOT count rate-limit retries toward the 3-attempt dead-letter threshold (or increase max attempts for rate-limited errors)
4. Log the backoff duration for observability

## Files to Investigate

- `internal/gateway/errors.go` — `isRateLimitedError()` already parses `Retry-After` duration
- `internal/gateway/client.go:461` — marks connection as rate-limited, returns `turn.ErrRateLimited`
- `internal/worker/` — job queue retry logic (where retry interval is set)
- `internal/turn/engine.go` — where agent_turn job is processed

## Acceptance Criteria

- [ ] agent_turn job retries with >=30s backoff on rate limit errors
- [ ] Rate-limit retries use exponential backoff (30s, 60s, 120s minimum)
- [ ] Rate-limit failures do not count toward the dead-letter threshold (or threshold is increased to >=6 for rate-limited errors)
- [ ] Retry-After header from provider is respected when available
- [ ] Unit test: rate-limited agent_turn retries with correct backoff
