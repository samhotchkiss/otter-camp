ALTER TABLE projects
ADD COLUMN primary_agent_id UUID REFERENCES agents(id) ON DELETE SET NULL;

CREATE INDEX projects_org_primary_agent_idx ON projects(org_id, primary_agent_id);
