-- name: ListActivityByOrg :many
SELECT id, org_id, task_id, agent_id, action, metadata, created_at
FROM activity_log
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC;

-- name: ListActivityByTask :many
SELECT id, org_id, task_id, agent_id, action, metadata, created_at
FROM activity_log
WHERE task_id = sqlc.arg(task_id)
ORDER BY created_at DESC;

-- name: CreateActivity :one
INSERT INTO activity_log (
    org_id,
    task_id,
    agent_id,
    action,
    metadata
) VALUES (
    sqlc.arg(org_id),
    sqlc.arg(task_id),
    sqlc.arg(agent_id),
    sqlc.arg(action),
    sqlc.arg(metadata)
)
RETURNING id, org_id, task_id, agent_id, action, metadata, created_at;
