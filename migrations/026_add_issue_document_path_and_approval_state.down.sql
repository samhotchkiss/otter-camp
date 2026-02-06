DROP INDEX IF EXISTS project_issues_project_approval_state_idx;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_approval_state_check;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_document_path_check;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS approval_state;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS document_path;
