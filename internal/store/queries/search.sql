-- name: SearchTasks :many
WITH query AS (
    SELECT plainto_tsquery('english', sqlc.arg(query)) AS q
)
SELECT
    id,
    org_id,
    project_id,
    number,
    title,
    description,
    status,
    priority,
    context,
    assigned_agent_id,
    parent_task_id,
    created_at,
    updated_at,
    ts_rank(search_vector, query.q) AS rank,
    ts_headline('english', title, query.q, 'StartSel=<mark>,StopSel=</mark>') AS title_highlight,
    ts_headline('english', COALESCE(description, ''), query.q, 'StartSel=<mark>,StopSel=</mark>,MaxFragments=2,MaxWords=24,MinWords=8') AS description_highlight
FROM tasks, query
WHERE org_id = sqlc.arg(org_id)
  AND search_vector @@ query.q
ORDER BY rank DESC, updated_at DESC
LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);
