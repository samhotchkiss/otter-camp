ALTER TABLE projects
    ADD COLUMN workflow_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN workflow_schedule JSONB,
    ADD COLUMN workflow_template JSONB,
    ADD COLUMN workflow_agent_id UUID REFERENCES agents(id),
    ADD COLUMN workflow_last_run_at TIMESTAMPTZ,
    ADD COLUMN workflow_next_run_at TIMESTAMPTZ,
    ADD COLUMN workflow_run_count INT NOT NULL DEFAULT 0;

CREATE INDEX idx_projects_workflow
    ON projects(org_id)
    WHERE workflow_enabled = true;
