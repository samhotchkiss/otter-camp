ALTER TABLE agent_activity_events
    ADD COLUMN IF NOT EXISTS commit_sha TEXT,
    ADD COLUMN IF NOT EXISTS commit_branch TEXT,
    ADD COLUMN IF NOT EXISTS commit_remote TEXT,
    ADD COLUMN IF NOT EXISTS push_status TEXT;

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_commit_sha
    ON agent_activity_events (commit_sha)
    WHERE commit_sha IS NOT NULL;
