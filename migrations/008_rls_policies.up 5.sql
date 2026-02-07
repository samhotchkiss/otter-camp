-- Enable row-level security policies for org-scoped tables.

CREATE OR REPLACE FUNCTION current_org_id() RETURNS UUID
LANGUAGE sql STABLE AS $$
    SELECT NULLIF(current_setting('app.org_id', true), '')::uuid
$$;

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agents_org_isolation ON agents;
CREATE POLICY agents_org_isolation ON agents
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE projects FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS projects_org_isolation ON projects;
CREATE POLICY projects_org_isolation ON projects
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tasks_org_isolation ON tasks;
CREATE POLICY tasks_org_isolation ON tasks
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE tags FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tags_org_isolation ON tags;
CREATE POLICY tags_org_isolation ON tags
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());

ALTER TABLE activity_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_log FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS activity_log_org_isolation ON activity_log;
CREATE POLICY activity_log_org_isolation ON activity_log
    USING (org_id = current_org_id())
    WITH CHECK (org_id = current_org_id());
