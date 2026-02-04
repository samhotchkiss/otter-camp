-- name: GetOrganization :one
SELECT id, name, slug, tier, created_at, updated_at
FROM organizations
WHERE id = sqlc.arg(id);

-- name: ListOrganizations :many
SELECT id, name, slug, tier, created_at, updated_at
FROM organizations
ORDER BY created_at DESC;

-- name: CreateOrganization :one
INSERT INTO organizations (
    name,
    slug,
    tier
) VALUES (
    sqlc.arg(name),
    sqlc.arg(slug),
    sqlc.arg(tier)
)
RETURNING id, name, slug, tier, created_at, updated_at;

-- name: UpdateOrganization :one
UPDATE organizations
SET
    name = sqlc.arg(name),
    slug = sqlc.arg(slug),
    tier = sqlc.arg(tier)
WHERE id = sqlc.arg(id)
RETURNING id, name, slug, tier, created_at, updated_at;
