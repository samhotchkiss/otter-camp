DROP INDEX IF EXISTS project_issues_project_last_commit_sha_idx;
DROP INDEX IF EXISTS project_issues_project_active_branch_idx;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS last_commit_sha,
    DROP COLUMN IF EXISTS active_branch;
