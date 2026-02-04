-- name: ListCommentsByTask :many
SELECT id, task_id, author_id, content, created_at, updated_at
FROM comments
WHERE task_id = sqlc.arg(task_id)
ORDER BY created_at ASC;

-- name: CreateComment :one
INSERT INTO comments (
    task_id,
    author_id,
    content
) VALUES (
    sqlc.arg(task_id),
    sqlc.arg(author_id),
    sqlc.arg(content)
)
RETURNING id, task_id, author_id, content, created_at, updated_at;
