DROP INDEX IF EXISTS project_issues_project_parent_issue_idx;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_work_status_check;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_work_status_check
    CHECK (
        work_status IN (
            'queued',
            'in_progress',
            'blocked',
            'review',
            'done',
            'cancelled'
        )
    );

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS parent_issue_id;
