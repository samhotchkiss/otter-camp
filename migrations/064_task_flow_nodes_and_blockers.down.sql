DROP POLICY IF EXISTS project_issue_flow_blockers_org_isolation ON project_issue_flow_blockers;

ALTER TABLE IF EXISTS project_issue_flow_blockers NO FORCE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS project_issue_flow_blockers DISABLE ROW LEVEL SECURITY;

DROP TABLE IF EXISTS project_issue_flow_blockers;

ALTER TABLE project_issues
    DROP CONSTRAINT IF EXISTS project_issues_work_status_check;

ALTER TABLE project_issues
    ADD CONSTRAINT project_issues_work_status_check
    CHECK (work_status IN ('queued', 'in_progress', 'blocked', 'review', 'done', 'cancelled'));

DROP INDEX IF EXISTS project_flow_template_steps_reject_key_idx;
DROP INDEX IF EXISTS project_flow_template_steps_next_key_idx;

ALTER TABLE project_flow_template_steps
    DROP CONSTRAINT IF EXISTS project_flow_template_steps_actor_type_check,
    DROP CONSTRAINT IF EXISTS project_flow_template_steps_node_type_check;

ALTER TABLE project_flow_template_steps
    DROP COLUMN IF EXISTS reject_step_key,
    DROP COLUMN IF EXISTS next_step_key,
    DROP COLUMN IF EXISTS actor_value,
    DROP COLUMN IF EXISTS actor_type,
    DROP COLUMN IF EXISTS objective,
    DROP COLUMN IF EXISTS node_type;
