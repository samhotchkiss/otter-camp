CREATE TABLE IF NOT EXISTS agent_activity_events (
    id TEXT PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id TEXT NOT NULL,
    session_key TEXT,
    trigger TEXT NOT NULL,
    channel TEXT,
    summary TEXT NOT NULL,
    detail TEXT,
    project_id UUID REFERENCES projects(id) ON DELETE SET NULL,
    issue_id UUID REFERENCES project_issues(id) ON DELETE SET NULL,
    issue_number INTEGER,
    thread_id TEXT,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    model_used TEXT,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'completed',
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_agent_started
    ON agent_activity_events (agent_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_org_started
    ON agent_activity_events (org_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_project_started
    ON agent_activity_events (project_id, started_at DESC)
    WHERE project_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_trigger_started
    ON agent_activity_events (trigger, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_activity_events_status
    ON agent_activity_events (status)
    WHERE status <> 'completed';

ALTER TABLE agent_activity_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_activity_events FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS agent_activity_events_org_isolation ON agent_activity_events;
CREATE POLICY agent_activity_events_org_isolation ON agent_activity_events
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
