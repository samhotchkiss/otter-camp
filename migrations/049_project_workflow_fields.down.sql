DROP INDEX IF EXISTS idx_projects_workflow;

ALTER TABLE projects
    DROP COLUMN IF EXISTS workflow_enabled,
    DROP COLUMN IF EXISTS workflow_schedule,
    DROP COLUMN IF EXISTS workflow_template,
    DROP COLUMN IF EXISTS workflow_agent_id,
    DROP COLUMN IF EXISTS workflow_last_run_at,
    DROP COLUMN IF EXISTS workflow_next_run_at,
    DROP COLUMN IF EXISTS workflow_run_count;
