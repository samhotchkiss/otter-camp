# Task 099: RollupWorker not registered — rollup_update jobs dead-letter

Layer: L2
Effort: S
Depends on: none

## Context

Token usage rollup jobs (`rollup_update`) dead-letter with "no handler registered" because `gateway.RollupWorker.RegisterJobs` is never called in `internal/worker/worker.go`.

Evidence from live session:
- `rollup_update` jobs visible in job_queue with status `dead_letter`, error: "no handler registered for job type rollup_update"
- `gateway.RollupWorker` exists in `internal/gateway/rollup_worker.go` with `RegisterJobs` method
- `internal/worker/worker.go` wires many job handlers but never calls `gateway.NewRollupWorker(...).RegisterJobs(jqWorker)`
- Token usage rollups are permanently broken — no usage metrics are stored

## Root Cause

In `internal/worker/worker.go`, the worker setup registers many job handlers (turn engine, supervisor, task queue processor, etc.) but omits wiring the rollup worker:

```go
// Present in worker.go:
dailyRollupJob.RegisterJobs(scheduler)  // cron schedule only

// Missing:
gateway.NewRollupWorker(pool.Raw(), gatewayLogger, logger).RegisterJobs(jqWorker)
```

`RollupWorker.RegisterJobs` registers the handler for the `rollup_update` job type so the job queue can execute it.

## Required Fix

In `internal/worker/worker.go`, after initializing the gateway components, construct a `gateway.RollupWorker` and call `RegisterJobs(jqWorker)`:

```go
rollupWorker := gateway.NewRollupWorker(pool.Raw(), nil, logger)
rollupWorker.RegisterJobs(jqWorker)
```

Verify that `gateway.NewRollupWorker` constructor signature matches (it may accept a `*pgxpool.Pool`, logger, etc.).

## Acceptance Criteria

- [ ] `gateway.RollupWorker.RegisterJobs` called in `internal/worker/worker.go`
- [ ] `rollup_update` jobs no longer dead-letter
- [ ] After a chat turn completes, a `rollup_update` job is created and successfully processed
- [ ] Token usage records are stored in the DB after processing
- [ ] `go build ./...` passes

## Required Tests

- Integration: after an agent turn completes, a `rollup_update` job is enqueued and processed (status: done)
- Unit: `RollupWorker.RegisterJobs` registers handler for `rollup_update` job type
