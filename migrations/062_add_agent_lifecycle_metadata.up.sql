ALTER TABLE agents
    ADD COLUMN IF NOT EXISTS is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES projects(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_agents_org_project_ephemeral_status
    ON agents (org_id, project_id, is_ephemeral, status)
    WHERE project_id IS NOT NULL;
