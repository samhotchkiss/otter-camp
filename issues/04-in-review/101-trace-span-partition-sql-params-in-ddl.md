# Task 101: trace_span_partition_create dead-letters — SQL params not supported in DDL partition bounds

Layer: L2
Effort: XS
Depends on: none

## Context

The `trace_span_partition_create` job dead-letters with "mismatched param and argument count" (or similar PostgreSQL error) because `TraceSpanPartitionJob.Run` uses positional parameters `$1`/`$2` in a DDL `CREATE TABLE ... PARTITION OF ... FOR VALUES FROM ($1) TO ($2)` statement. PostgreSQL does not support parameters in DDL partition bounds.

Evidence from live session:
- `trace_span_partition_create` jobs visible in `job_queue` with status `dead_letter`
- Error: "mismatched param and argument count" or "cannot use query parameters in DDL"
- Root cause: `internal/jobs/trace_partition_job.go` uses `$1`/`$2` placeholders in DDL

## Root Cause

In `internal/jobs/trace_partition_job.go` (~line 50):

```go
j.pool.Exec(ctx,
    fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF trace_span
        FOR VALUES FROM ($1) TO ($2)`, partitionName),
    start, end)
```

PostgreSQL DDL statements (`CREATE TABLE`, `ALTER TABLE`, etc.) do not support parameterized queries. The partition bounds must be literal values embedded in the SQL string.

## Required Fix

Replace parameterized DDL with string-formatted values:

```go
j.pool.Exec(ctx,
    fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s PARTITION OF trace_span
        FOR VALUES FROM ('%s') TO ('%s')`,
        partitionName,
        start.Format(time.RFC3339),
        end.Format(time.RFC3339),
    ),
)
```

Or use the appropriate time format for the partition column type (likely `TIMESTAMPTZ`, formatted as `'2024-01-01 00:00:00+00'`).

Note: `partitionName` should already be safe (generated from date values, no user input) so string formatting is acceptable here.

## Acceptance Criteria

- [ ] `trace_span_partition_create` jobs no longer dead-letter
- [ ] Partition table created successfully for the current month
- [ ] No SQL syntax errors in partition creation
- [ ] `go build ./...` passes

## Required Tests

- Integration: `TraceSpanPartitionJob.Run` executes without error, creates partition table
- Unit: partition SQL uses string-formatted timestamps, not `$1`/`$2` placeholders
