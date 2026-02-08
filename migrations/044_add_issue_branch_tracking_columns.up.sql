ALTER TABLE project_issues
    ADD COLUMN active_branch TEXT,
    ADD COLUMN last_commit_sha TEXT;

CREATE INDEX project_issues_project_active_branch_idx
    ON project_issues (project_id, active_branch, updated_at DESC);

CREATE INDEX project_issues_project_last_commit_sha_idx
    ON project_issues (project_id, last_commit_sha, updated_at DESC);
