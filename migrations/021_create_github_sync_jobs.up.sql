CREATE TABLE github_sync_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    job_type TEXT NOT NULL CHECK (job_type IN ('repo_sync', 'issue_import', 'webhook_event')),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'in_progress', 'retrying', 'completed', 'dead_letter')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    source_event_id TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    last_error_class TEXT,
    attempt_history JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (org_id, job_type, source_event_id)
);

CREATE INDEX github_sync_jobs_org_status_next_idx ON github_sync_jobs (org_id, status, next_attempt_at);
CREATE INDEX github_sync_jobs_project_idx ON github_sync_jobs (project_id);
CREATE INDEX github_sync_jobs_job_type_status_idx ON github_sync_jobs (job_type, status, next_attempt_at);

CREATE TRIGGER github_sync_jobs_updated_at_trg
BEFORE UPDATE ON github_sync_jobs
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE github_sync_dead_letters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    job_id UUID NOT NULL UNIQUE REFERENCES github_sync_jobs(id) ON DELETE CASCADE,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    job_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    attempt_count INTEGER NOT NULL,
    max_attempts INTEGER NOT NULL,
    last_error TEXT,
    last_error_class TEXT,
    attempt_history JSONB NOT NULL DEFAULT '[]'::jsonb,
    failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    replayed_at TIMESTAMPTZ,
    replayed_by TEXT
);

CREATE INDEX github_sync_dead_letters_org_failed_idx ON github_sync_dead_letters (org_id, failed_at DESC) WHERE replayed_at IS NULL;
CREATE INDEX github_sync_dead_letters_job_type_idx ON github_sync_dead_letters (job_type, failed_at DESC);
