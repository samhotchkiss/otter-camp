# Task 112: trace_span DEFAULT partition blocks specific date partition creation

Layer: L2
Effort: S
Depends on: 101 (trace partition DDL fix)

## Context

After issue 101 fix (SQL literals in DDL), `trace_span_partition_create` jobs still dead-letter with a new error:

```
create trace span partition: ERROR: partition "trace_span_p_20260304" would overlap
partition "trace_span_p_default" (SQLSTATE 42P17)
```

The `trace_span` table was seeded with a `trace_span_p_default` DEFAULT partition (added by a migration). In PostgreSQL, a DEFAULT partition covers all values that are not covered by any other partition. A new specific RANGE partition `FOR VALUES FROM (...) TO (...)` overlaps with the DEFAULT partition's range and is rejected by PostgreSQL.

Evidence:
```sql
SELECT relname FROM pg_class c JOIN pg_inherits i ON c.oid = i.inhrelid
WHERE i.inhparent = 'trace_span'::regclass;
-- Returns: trace_span_p_default (only partition)
```

## Root Cause

The `TraceSpanPartitionJob` is designed to create date-specific `RANGE` partitions (e.g., `trace_span_p_20260304`) to improve query performance. However, the migration that creates `trace_span` also seeds a DEFAULT partition `trace_span_p_default`.

PostgreSQL's partitioning rules: when adding a new RANGE partition, PostgreSQL checks that the new range does not overlap with the DEFAULT partition. Since the DEFAULT partition covers all dates, any specific RANGE partition will conflict.

## Required Fix

Option A (Recommended): Remove the DEFAULT partition from the migration, or drop it in a new migration.

In PostgreSQL, if a DEFAULT partition exists and you want to add a specific RANGE partition:
1. Move data from default partition: `INSERT INTO trace_span SELECT * FROM trace_span_p_default WHERE created_at BETWEEN ...`
2. Drop default: `DROP TABLE trace_span_p_default`
3. Create specific: `CREATE TABLE trace_span_p_default ...`

OR (simpler): Just drop the DEFAULT partition if it's empty (no spans have been recorded yet in production):

```sql
-- New migration:
DROP TABLE IF EXISTS trace_span_p_default;
```

Option B: Update `TraceSpanPartitionJob.Run` to handle the overlap error gracefully (treat it as idempotent — partition effectively already exists via the default). This doesn't solve the long-term performance issue but prevents dead-lettering.

Option C: Update `TraceSpanPartitionJob.Run` to detect the DEFAULT partition and temporarily detach it, create the specific partition, then re-attach (complex, risky).

**Recommendation**: Option A — add a migration to drop `trace_span_p_default` (it's an empty table in any fresh install). Then `TraceSpanPartitionJob` can create date-specific partitions normally.

## Acceptance Criteria

- [ ] `trace_span_partition_create` jobs complete successfully (status: done)
- [ ] `trace_span_p_20260225` (or current date partition) is created
- [ ] New spans are inserted into the date-specific partition, not the default
- [ ] `go build ./...` passes

## Required Tests

- Integration: `TraceSpanPartitionJob.Run` creates partition without error when default partition doesn't exist
- Migration test: `trace_span_p_default` is dropped in the new migration
