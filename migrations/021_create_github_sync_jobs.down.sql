DROP INDEX IF EXISTS github_sync_dead_letters_job_type_idx;
DROP INDEX IF EXISTS github_sync_dead_letters_org_failed_idx;
DROP TABLE IF EXISTS github_sync_dead_letters;

DROP TRIGGER IF EXISTS github_sync_jobs_updated_at_trg ON github_sync_jobs;
DROP INDEX IF EXISTS github_sync_jobs_job_type_status_idx;
DROP INDEX IF EXISTS github_sync_jobs_project_idx;
DROP INDEX IF EXISTS github_sync_jobs_org_status_next_idx;
DROP TABLE IF EXISTS github_sync_jobs;
