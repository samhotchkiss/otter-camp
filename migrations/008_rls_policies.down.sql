-- Disable row-level security policies

ALTER TABLE agents DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS agents_org_isolation ON agents;

ALTER TABLE projects DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS projects_org_isolation ON projects;

ALTER TABLE tasks DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tasks_org_isolation ON tasks;

ALTER TABLE tags DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tags_org_isolation ON tags;

ALTER TABLE activity_log DISABLE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS activity_log_org_isolation ON activity_log;

DROP FUNCTION IF EXISTS current_org_id();
