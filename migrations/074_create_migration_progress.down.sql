DROP POLICY IF EXISTS migration_progress_org_isolation ON migration_progress;
DROP TRIGGER IF EXISTS migration_progress_updated_at_trg ON migration_progress;
DROP INDEX IF EXISTS migration_progress_org_status_idx;
DROP INDEX IF EXISTS migration_progress_org_type_uidx;
DROP TABLE IF EXISTS migration_progress;
