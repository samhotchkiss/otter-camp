ALTER TABLE project_issues
    DROP COLUMN IF EXISTS flow_step_index,
    DROP COLUMN IF EXISTS flow_step_key,
    DROP COLUMN IF EXISTS flow_template_id;

DROP TABLE IF EXISTS project_flow_template_steps;
DROP TABLE IF EXISTS project_flow_templates;
