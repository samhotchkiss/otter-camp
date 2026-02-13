CREATE TABLE IF NOT EXISTS agent_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,

    name TEXT NOT NULL,
    description TEXT,

    schedule_kind TEXT NOT NULL CHECK (schedule_kind IN ('cron', 'interval', 'once')),
    cron_expr TEXT,
    interval_ms BIGINT,
    run_at TIMESTAMPTZ,
    timezone TEXT NOT NULL DEFAULT 'UTC',

    payload_kind TEXT NOT NULL CHECK (payload_kind IN ('message', 'system_event')),
    payload_text TEXT NOT NULL,

    room_id UUID REFERENCES rooms(id) ON DELETE SET NULL,

    enabled BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'completed', 'failed')),
    last_run_at TIMESTAMPTZ,
    last_run_status TEXT CHECK (last_run_status IN ('success', 'error', 'timeout', 'skipped')),
    last_run_error TEXT,
    next_run_at TIMESTAMPTZ,
    run_count INT NOT NULL DEFAULT 0,
    error_count INT NOT NULL DEFAULT 0,
    max_failures INT NOT NULL DEFAULT 5,
    consecutive_failures INT NOT NULL DEFAULT 0,

    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT agent_jobs_valid_cron CHECK (schedule_kind <> 'cron' OR cron_expr IS NOT NULL),
    CONSTRAINT agent_jobs_valid_interval CHECK (schedule_kind <> 'interval' OR interval_ms IS NOT NULL),
    CONSTRAINT agent_jobs_valid_once CHECK (schedule_kind <> 'once' OR run_at IS NOT NULL),
    CONSTRAINT agent_jobs_interval_positive CHECK (interval_ms IS NULL OR interval_ms > 0),
    CONSTRAINT agent_jobs_max_failures_positive CHECK (max_failures > 0)
);

-- Scheduler polling query uses FOR UPDATE SKIP LOCKED for safe leasing.
CREATE INDEX IF NOT EXISTS idx_agent_jobs_next_run_active
    ON agent_jobs (next_run_at)
    WHERE enabled = true AND status = 'active';
CREATE INDEX IF NOT EXISTS idx_agent_jobs_org ON agent_jobs (org_id);
CREATE INDEX IF NOT EXISTS idx_agent_jobs_agent ON agent_jobs (agent_id);

DROP TRIGGER IF EXISTS agent_jobs_updated_at_trg ON agent_jobs;
CREATE TRIGGER agent_jobs_updated_at_trg
BEFORE UPDATE ON agent_jobs
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TABLE IF NOT EXISTS agent_job_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES agent_jobs(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'error', 'timeout', 'skipped')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    duration_ms INT,
    error TEXT,

    payload_text TEXT NOT NULL,
    message_id UUID REFERENCES chat_messages(id) ON DELETE SET NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_job_runs_job_started
    ON agent_job_runs (job_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_job_runs_org ON agent_job_runs (org_id);

ALTER TABLE agent_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_jobs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_jobs_org_isolation ON agent_jobs;
CREATE POLICY agent_jobs_org_isolation ON agent_jobs
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE agent_job_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_job_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agent_job_runs_org_isolation ON agent_job_runs;
CREATE POLICY agent_job_runs_org_isolation ON agent_job_runs
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
