ALTER TABLE project_issues
    ADD COLUMN parent_issue_id UUID REFERENCES project_issues(id) ON DELETE SET NULL;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_work_status_check;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_work_status_check
    CHECK (
        work_status IN (
            'queued',
            'ready',
            'planning',
            'ready_for_work',
            'in_progress',
            'blocked',
            'review',
            'flagged',
            'done',
            'cancelled'
        )
    );

CREATE INDEX project_issues_project_parent_issue_idx
    ON project_issues (project_id, parent_issue_id, updated_at DESC);
