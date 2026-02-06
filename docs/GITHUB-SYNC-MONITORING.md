# GitHub Sync Monitoring Runbook

## Health Endpoint
- `GET /api/github/sync/health`
- Query params:
  - `stuck_threshold` (duration, default `15m`)
- Protected by capability: `github.sync.manual`

Response includes:
- `queue_depth` by `job_type` and status (`queued`, `retrying`, `in_progress`, `dead_letter`)
- `stuck_jobs` count (in-progress jobs older than threshold)
- `metrics.jobs` counters:
  - `picked_total`
  - `success_total`
  - `failure_total`
  - `retry_total`
  - `dead_letter_total`
  - `replay_total`
  - `total_latency_millis`
- `metrics.quota` per job type:
  - `limit`, `remaining`, `reset_at`, `updated_at`, `throttle_events`

## Alert Rules (Suggested)
1. Stuck jobs
- Condition: `stuck_jobs > 0` for 10m
- Severity: high
- Action: inspect `/api/github/sync/health`, replay dead letters when safe, restart workers if needed.

2. Dead-letter growth
- Condition: `dead_letter_total` increases continuously for 15m
- Severity: high
- Action: inspect last errors via `/api/github/sync/dead-letters`, classify terminal vs retryable bugs.

3. Retry storm
- Condition: `retry_total / failure_total > 0.8` and `failure_total` high over 15m
- Severity: medium
- Action: verify GitHub API availability and auth, inspect throttling events.

4. Quota starvation
- Condition: `metrics.quota[*].remaining` near reserve and `throttle_events` increasing
- Severity: medium/high
- Action: pause bulk imports, resume after `reset_at`, reduce sync fanout.

## Triage Flow
1. Check `/api/github/sync/health` for stuck jobs and queue build-up.
2. If dead letters exist, inspect `/api/github/sync/dead-letters`.
3. Replay selected dead-letter jobs with `POST /api/github/sync/dead-letters/{id}/replay`.
4. If rate-limited, wait until `reset_at` and retry.
5. If terminal errors dominate, fix payload/auth/config before replaying.
