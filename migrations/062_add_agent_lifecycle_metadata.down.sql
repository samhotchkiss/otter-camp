DROP INDEX IF EXISTS idx_agents_org_project_ephemeral_status;

ALTER TABLE agents
    DROP COLUMN IF EXISTS project_id,
    DROP COLUMN IF EXISTS is_ephemeral;
