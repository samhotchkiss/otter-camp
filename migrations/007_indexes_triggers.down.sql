-- Full-text search cleanup
DROP TRIGGER IF EXISTS tasks_search_vector_trg ON tasks;
DROP FUNCTION IF EXISTS update_tasks_search_vector();
DROP INDEX IF EXISTS tasks_search_vector_gin_idx;
ALTER TABLE tasks DROP COLUMN IF EXISTS search_vector;

-- updated_at triggers cleanup
DROP TRIGGER IF EXISTS comments_updated_at_trg ON comments;
DROP TRIGGER IF EXISTS tasks_updated_at_trg ON tasks;
DROP TRIGGER IF EXISTS projects_updated_at_trg ON projects;
DROP TRIGGER IF EXISTS agents_updated_at_trg ON agents;
DROP TRIGGER IF EXISTS organizations_updated_at_trg ON organizations;

DROP FUNCTION IF EXISTS update_updated_at_column();

-- Remove updated_at columns added in this migration
ALTER TABLE projects DROP COLUMN IF EXISTS updated_at;
ALTER TABLE agents DROP COLUMN IF EXISTS updated_at;
ALTER TABLE organizations DROP COLUMN IF EXISTS updated_at;

-- Performance indexes cleanup
DROP INDEX IF EXISTS tags_org_idx;
DROP INDEX IF EXISTS activity_log_task_idx;
DROP INDEX IF EXISTS comments_task_created_at_idx;
DROP INDEX IF EXISTS tasks_parent_task_idx;
DROP INDEX IF EXISTS tasks_org_assigned_agent_idx;
DROP INDEX IF EXISTS tasks_project_status_idx;
