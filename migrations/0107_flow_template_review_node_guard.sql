WITH violating_templates AS (
    SELECT template.id
    FROM flow_template AS template
    WHERE NOT EXISTS (
        SELECT 1
        FROM flow_node AS node
        WHERE node.flow_template_id = template.id
          AND node.node_type = 'review'
    )
),
replacement_by_project AS (
    SELECT
        project.id AS project_id,
        COALESCE(
            project_review_template.id,
            org_default_review_template.id,
            org_any_review_template.id
        ) AS replacement_template_id
    FROM project
    JOIN violating_templates ON violating_templates.id = project.deploy_flow_template_id
    LEFT JOIN LATERAL (
        SELECT candidate.id
        FROM flow_template AS candidate
        WHERE candidate.project_id = project.id
          AND candidate.id <> project.deploy_flow_template_id
          AND candidate.is_current = true
          AND EXISTS (
              SELECT 1
              FROM flow_node AS node
              WHERE node.flow_template_id = candidate.id
                AND node.node_type = 'review'
          )
        ORDER BY candidate.created_at DESC, candidate.id DESC
        LIMIT 1
    ) AS project_review_template ON true
    LEFT JOIN LATERAL (
        SELECT candidate.id
        FROM flow_template AS candidate
        WHERE candidate.organization_id = project.organization_id
          AND candidate.project_id IS NULL
          AND candidate.is_current = true
          AND candidate.slug = 'default-review'
          AND EXISTS (
              SELECT 1
              FROM flow_node AS node
              WHERE node.flow_template_id = candidate.id
                AND node.node_type = 'review'
          )
        ORDER BY candidate.created_at DESC, candidate.id DESC
        LIMIT 1
    ) AS org_default_review_template ON true
    LEFT JOIN LATERAL (
        SELECT candidate.id
        FROM flow_template AS candidate
        WHERE candidate.organization_id = project.organization_id
          AND candidate.project_id IS NULL
          AND candidate.is_current = true
          AND EXISTS (
              SELECT 1
              FROM flow_node AS node
              WHERE node.flow_template_id = candidate.id
                AND node.node_type = 'review'
          )
        ORDER BY candidate.created_at DESC, candidate.id DESC
        LIMIT 1
    ) AS org_any_review_template ON true
)
UPDATE project
SET
    deploy_flow_template_id = replacement_by_project.replacement_template_id,
    updated_at = now()
FROM replacement_by_project
WHERE project.id = replacement_by_project.project_id;

UPDATE flow_template AS template
SET
    is_current = false,
    updated_at = now()
WHERE template.is_current = true
  AND NOT EXISTS (
      SELECT 1
      FROM flow_node AS node
      WHERE node.flow_template_id = template.id
        AND node.node_type = 'review'
  );
