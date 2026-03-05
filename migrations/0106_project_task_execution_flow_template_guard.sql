WITH unresolved_execution_tasks AS (
    SELECT
        task.id,
        COALESCE(
            project.deploy_flow_template_id,
            project_template.id,
            org_default_review_template.id,
            org_any_template.id
        ) AS resolved_flow_template_id
    FROM project_task AS task
    JOIN project ON project.id = task.project_id
    LEFT JOIN LATERAL (
        SELECT template.id
        FROM flow_template AS template
        WHERE template.project_id = task.project_id
          AND template.is_current = true
        ORDER BY template.created_at ASC, template.id ASC
        LIMIT 1
    ) AS project_template ON true
    LEFT JOIN LATERAL (
        SELECT template.id
        FROM flow_template AS template
        WHERE template.organization_id = task.organization_id
          AND template.project_id IS NULL
          AND template.is_current = true
          AND template.slug = 'default-review'
        ORDER BY template.created_at ASC, template.id ASC
        LIMIT 1
    ) AS org_default_review_template ON true
    LEFT JOIN LATERAL (
        SELECT template.id
        FROM flow_template AS template
        WHERE template.organization_id = task.organization_id
          AND template.project_id IS NULL
          AND template.is_current = true
        ORDER BY template.created_at ASC, template.id ASC
        LIMIT 1
    ) AS org_any_template ON true
    WHERE task.work_status IN ('queued', 'in_progress', 'review', 'done')
      AND task.flow_template_id IS NULL
)
UPDATE project_task AS task
SET
    flow_template_id = unresolved_execution_tasks.resolved_flow_template_id,
    updated_at = now()
FROM unresolved_execution_tasks
WHERE task.id = unresolved_execution_tasks.id
  AND unresolved_execution_tasks.resolved_flow_template_id IS NOT NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM project_task
        WHERE work_status IN ('queued', 'in_progress', 'review', 'done')
          AND flow_template_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'project_task rows in execution states must define flow_template_id; repair data and rerun migrations';
    END IF;
END;
$$;

ALTER TABLE project_task
    ADD CONSTRAINT project_task_execution_requires_flow_template_chk
    CHECK (
        work_status NOT IN ('queued', 'in_progress', 'review', 'done')
        OR flow_template_id IS NOT NULL
    );
