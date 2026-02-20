DROP POLICY IF EXISTS issue_pipeline_history_org_isolation ON issue_pipeline_history;
DROP TRIGGER IF EXISTS issue_pipeline_history_updated_at_trg ON issue_pipeline_history;
DROP INDEX IF EXISTS issue_pipeline_history_step_idx;
DROP INDEX IF EXISTS issue_pipeline_history_org_issue_started_idx;
DROP TABLE IF EXISTS issue_pipeline_history;

DROP INDEX IF EXISTS project_issues_project_current_pipeline_step_idx;

ALTER TABLE project_issues
    DROP COLUMN IF EXISTS pipeline_completed_at,
    DROP COLUMN IF EXISTS pipeline_started_at,
    DROP COLUMN IF EXISTS current_pipeline_step_id;

DROP POLICY IF EXISTS pipeline_steps_org_isolation ON pipeline_steps;
DROP TRIGGER IF EXISTS pipeline_steps_updated_at_trg ON pipeline_steps;
DROP INDEX IF EXISTS pipeline_steps_org_project_step_idx;
DROP TABLE IF EXISTS pipeline_steps;
