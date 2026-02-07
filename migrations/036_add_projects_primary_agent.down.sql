DROP INDEX IF EXISTS projects_org_primary_agent_idx;

ALTER TABLE projects
DROP COLUMN IF EXISTS primary_agent_id;
