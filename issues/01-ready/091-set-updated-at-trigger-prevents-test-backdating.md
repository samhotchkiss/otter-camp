# Task 091: Fix set_updated_at trigger to allow explicit updated_at overrides

Layer: L1
Effort: S
Depends on: none

## Context

The `set_updated_at()` PostgreSQL trigger function (defined in `migrations/0006_shared_triggers.sql`) unconditionally sets `NEW.updated_at = now()` on every UPDATE, regardless of whether the caller explicitly set `updated_at` to a different value.

This causes 3 integration tests to fail in `internal/controlplane`:

- `TestSupervisor_StuckRun_Detection` (`supervisor_integration_test.go:65`)
- `TestSupervisor_MaxRecoveryAttempts` (`supervisor_integration_test.go:232`)
- `TestRun_Lifecycle_TimedOut` (`run_integration_test.go:359`)

All three tests backdate `run.updated_at` via:
```sql
UPDATE run SET updated_at = $2 WHERE id = $1
```
But the `run_set_updated_at` trigger (installed in `migrations/0066_run.sql`) fires and overrides `updated_at` with `now()`, so the backdate is silently discarded. The supervisor's `ListInProgressUpdatedBefore` query then finds no candidates because the timestamp is fresh, and no supervisor events are written.

The `TestSupervisor_OrphanedRun_Recovery` test passes because `ListOrphanedInProgress` queries `run_event.created_at`, not `run.updated_at`. The `run_event` table does not have a trigger on `created_at`.

## Required Fix

Add a migration (`migrations/0087_set_updated_at_honor_explicit.sql`) that replaces the trigger function to only auto-set `updated_at` if the caller did NOT explicitly change it:

```sql
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.updated_at IS NOT DISTINCT FROM OLD.updated_at THEN
        NEW.updated_at = now();
    END IF;
    RETURN NEW;
END;
$$;
```

`IS NOT DISTINCT FROM` handles NULL values correctly. No trigger definitions need to change — only the function body.

## Acceptance Criteria

- [ ] Migration `migrations/0087_set_updated_at_honor_explicit.sql` exists and applies cleanly
- [ ] `TestSupervisor_StuckRun_Detection` passes
- [ ] `TestSupervisor_MaxRecoveryAttempts` passes
- [ ] `TestRun_Lifecycle_TimedOut` passes
- [ ] Existing tests that rely on auto-update of `updated_at` still pass (no regression)

## Required Tests

- Integration: Run `go test ./internal/controlplane/... -tags integration -count=1 -run "TestSupervisor_StuckRun_Detection|TestSupervisor_MaxRecoveryAttempts|TestRun_Lifecycle_TimedOut"` — all must pass
- Integration (regression): Run full `go test ./... -tags integration -count=1 -timeout 8m` — no new failures
