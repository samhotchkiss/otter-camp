-- Performance indexes
CREATE INDEX tasks_project_status_idx ON tasks(project_id, status);
CREATE INDEX tasks_org_assigned_agent_idx ON tasks(org_id, assigned_agent_id);
CREATE INDEX tasks_parent_task_idx ON tasks(parent_task_id);

CREATE INDEX comments_task_created_at_idx ON comments(task_id, created_at DESC);

CREATE INDEX activity_log_task_idx ON activity_log(task_id);

CREATE INDEX tags_org_idx ON tags(org_id);

-- Add updated_at columns where missing
ALTER TABLE organizations ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE agents ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE projects ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- updated_at triggers
CREATE TRIGGER organizations_updated_at_trg
BEFORE UPDATE ON organizations
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER agents_updated_at_trg
BEFORE UPDATE ON agents
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER projects_updated_at_trg
BEFORE UPDATE ON projects
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER tasks_updated_at_trg
BEFORE UPDATE ON tasks
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER comments_updated_at_trg
BEFORE UPDATE ON comments
FOR EACH ROW
EXECUTE FUNCTION update_updated_at_column();

-- Full-text search prep
ALTER TABLE tasks ADD COLUMN search_vector tsvector;

UPDATE tasks
SET search_vector = to_tsvector('english', COALESCE(title, '') || ' ' || COALESCE(description, ''));

CREATE INDEX tasks_search_vector_gin_idx ON tasks USING GIN (search_vector);

CREATE OR REPLACE FUNCTION update_tasks_search_vector()
RETURNS TRIGGER AS $$
BEGIN
    NEW.search_vector = to_tsvector('english', COALESCE(NEW.title, '') || ' ' || COALESCE(NEW.description, ''));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER tasks_search_vector_trg
BEFORE INSERT OR UPDATE ON tasks
FOR EACH ROW
EXECUTE FUNCTION update_tasks_search_vector();
