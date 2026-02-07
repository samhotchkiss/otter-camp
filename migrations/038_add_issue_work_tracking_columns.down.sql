DROP INDEX IF EXISTS project_issues_project_priority_idx;
DROP INDEX IF EXISTS project_issues_project_owner_agent_idx;
DROP INDEX IF EXISTS project_issues_project_work_status_idx;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_priority_check;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_work_status_check;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS next_step_due_at,
    DROP COLUMN IF EXISTS next_step,
    DROP COLUMN IF EXISTS due_at,
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS work_status,
    DROP COLUMN IF EXISTS owner_agent_id;
