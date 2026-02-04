-- name: GetAgent :one
SELECT id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at
FROM agents
WHERE id = sqlc.arg(id);

-- name: GetAgentBySessionPattern :one
SELECT id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at
FROM agents
WHERE session_pattern IS NOT NULL
  AND sqlc.arg(session) LIKE session_pattern
ORDER BY created_at DESC
LIMIT 1;

-- name: ListAgentsByOrg :many
SELECT id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at
FROM agents
WHERE org_id = sqlc.arg(org_id)
ORDER BY created_at DESC;

-- name: CreateAgent :one
INSERT INTO agents (
    org_id,
    slug,
    display_name,
    avatar_url,
    webhook_url,
    status,
    session_pattern
) VALUES (
    sqlc.arg(org_id),
    sqlc.arg(slug),
    sqlc.arg(display_name),
    sqlc.arg(avatar_url),
    sqlc.arg(webhook_url),
    sqlc.arg(status),
    sqlc.arg(session_pattern)
)
RETURNING id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at;

-- name: UpdateAgent :one
UPDATE agents
SET
    slug = sqlc.arg(slug),
    display_name = sqlc.arg(display_name),
    avatar_url = sqlc.arg(avatar_url),
    webhook_url = sqlc.arg(webhook_url),
    status = sqlc.arg(status),
    session_pattern = sqlc.arg(session_pattern)
WHERE id = sqlc.arg(id)
RETURNING id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at;
