-- name: GetTask :one
SELECT id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at
FROM tasks
WHERE id = sqlc.arg(id);

-- name: GetTaskByOrgAndNumber :one
SELECT id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at
FROM tasks
WHERE org_id = sqlc.arg(org_id)
  AND number = sqlc.arg(number);

-- name: ListTasksByOrg :many
SELECT id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at
FROM tasks
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC;

-- name: ListTasksByProject :many
SELECT id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at
FROM tasks
WHERE project_id = sqlc.arg(project_id)
ORDER BY created_at DESC;

-- name: ListTasksByAgent :many
SELECT id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at
FROM tasks
WHERE assigned_agent_id = sqlc.arg(assigned_agent_id)
ORDER BY created_at DESC;

-- name: CreateTask :one
INSERT INTO tasks (
    org_id,
    project_id,
    title,
    description,
    status,
    priority,
    context,
    assigned_agent_id,
    parent_task_id
) VALUES (
    sqlc.arg(org_id),
    sqlc.arg(project_id),
    sqlc.arg(title),
    sqlc.arg(description),
    sqlc.arg(status),
    sqlc.arg(priority),
    sqlc.arg(context),
    sqlc.arg(assigned_agent_id),
    sqlc.arg(parent_task_id)
)
RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at;

-- name: UpdateTask :one
UPDATE tasks
SET
    project_id = sqlc.arg(project_id),
    title = sqlc.arg(title),
    description = sqlc.arg(description),
    status = sqlc.arg(status),
    priority = sqlc.arg(priority),
    context = sqlc.arg(context),
    assigned_agent_id = sqlc.arg(assigned_agent_id),
    parent_task_id = sqlc.arg(parent_task_id)
WHERE id = sqlc.arg(id)
RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at;

-- name: UpdateTaskStatus :one
UPDATE tasks
SET status = sqlc.arg(status)
WHERE id = sqlc.arg(id)
RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at;
