ALTER TABLE project_issues
    ADD COLUMN owner_agent_id UUID REFERENCES agents(id),
    ADD COLUMN work_status TEXT NOT NULL DEFAULT 'queued',
    ADD COLUMN priority TEXT NOT NULL DEFAULT 'P2',
    ADD COLUMN due_at TIMESTAMPTZ,
    ADD COLUMN next_step TEXT,
    ADD COLUMN next_step_due_at TIMESTAMPTZ;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_work_status_check
    CHECK (work_status IN ('queued', 'in_progress', 'blocked', 'review', 'done', 'cancelled'));

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_priority_check
    CHECK (priority IN ('P0', 'P1', 'P2', 'P3'));

CREATE INDEX project_issues_project_work_status_idx
    ON project_issues (project_id, work_status, updated_at DESC);

CREATE INDEX project_issues_project_owner_agent_idx
    ON project_issues (project_id, owner_agent_id, updated_at DESC);

CREATE INDEX project_issues_project_priority_idx
    ON project_issues (project_id, priority, updated_at DESC);
