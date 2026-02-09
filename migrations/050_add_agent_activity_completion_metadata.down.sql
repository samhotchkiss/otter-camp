DROP INDEX IF EXISTS idx_agent_activity_events_commit_sha;

ALTER TABLE agent_activity_events
    DROP COLUMN IF EXISTS push_status,
    DROP COLUMN IF EXISTS commit_remote,
    DROP COLUMN IF EXISTS commit_branch,
    DROP COLUMN IF EXISTS commit_sha;
