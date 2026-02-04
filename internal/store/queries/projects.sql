-- name: GetProject :one
SELECT id, org_id, name, description, status, repo_url, created_at, updated_at
FROM projects
WHERE id = sqlc.arg(id);

-- name: ListProjectsByOrg :many
SELECT id, org_id, name, description, status, repo_url, created_at, updated_at
FROM projects
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC;

-- name: CreateProject :one
INSERT INTO projects (
    org_id,
    name,
    description,
    status,
    repo_url
) VALUES (
    sqlc.arg(org_id),
    sqlc.arg(name),
    sqlc.arg(description),
    sqlc.arg(status),
    sqlc.arg(repo_url)
)
RETURNING id, org_id, name, description, status, repo_url, created_at, updated_at;

-- name: UpdateProject :one
UPDATE projects
SET
    name = sqlc.arg(name),
    description = sqlc.arg(description),
    status = sqlc.arg(status),
    repo_url = sqlc.arg(repo_url)
WHERE id = sqlc.arg(id)
RETURNING id, org_id, name, description, status, repo_url, created_at, updated_at;
