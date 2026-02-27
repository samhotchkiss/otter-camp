-- Add status column to project for soft-archiving
ALTER TABLE project ADD COLUMN status text NOT NULL DEFAULT 'active';
ALTER TABLE project ADD CONSTRAINT project_status_check CHECK (status = ANY(ARRAY['active', 'archived']));
CREATE INDEX project_org_status_idx ON project(organization_id, status);
