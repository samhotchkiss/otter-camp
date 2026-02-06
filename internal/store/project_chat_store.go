package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type ProjectChatMessage struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateProjectChatMessageInput struct {
	ProjectID string
	Author    string
	Body      string
}

type SearchProjectChatInput struct {
	ProjectID string
	Query     string
	Author    *string
	From      *time.Time
	To        *time.Time
	Limit     int
}

type ProjectChatSearchResult struct {
	Message   ProjectChatMessage `json:"message"`
	Relevance float64            `json:"relevance"`
	Snippet   string             `json:"snippet"`
}

type ProjectChatStore struct {
	db *sql.DB
}

func NewProjectChatStore(db *sql.DB) *ProjectChatStore {
	return &ProjectChatStore{db: db}
}

const projectChatColumns = `
	id,
	org_id,
	project_id,
	author,
	body,
	created_at,
	updated_at
`

func (s *ProjectChatStore) Create(ctx context.Context, input CreateProjectChatMessageInput) (*ProjectChatMessage, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	author := strings.TrimSpace(input.Author)
	if author == "" {
		return nil, fmt.Errorf("author is required")
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureProjectInWorkspace(ctx, conn, workspaceID, projectID); err != nil {
		return nil, err
	}

	message, err := scanProjectChatMessage(conn.QueryRowContext(
		ctx,
		`INSERT INTO project_chat_messages (
			org_id,
			project_id,
			author,
			body
		) VALUES ($1, $2, $3, $4)
		RETURNING `+projectChatColumns,
		workspaceID,
		projectID,
		author,
		body,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create project chat message: %w", err)
	}
	return &message, nil
}

func (s *ProjectChatStore) List(
	ctx context.Context,
	projectID string,
	limit int,
	beforeCreatedAt *time.Time,
	beforeID *string,
) ([]ProjectChatMessage, bool, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, false, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, false, fmt.Errorf("invalid project_id")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	if err := ensureProjectInWorkspace(ctx, conn, workspaceID, projectID); err != nil {
		return nil, false, err
	}

	where := `WHERE org_id = $1 AND project_id = $2`
	args := []any{workspaceID, projectID}
	if beforeCreatedAt != nil && beforeID != nil && strings.TrimSpace(*beforeID) != "" {
		where += ` AND (created_at, id) < ($3, $4)`
		args = append(args, beforeCreatedAt.UTC(), strings.TrimSpace(*beforeID))
	}
	args = append(args, limit+1)

	rows, err := conn.QueryContext(
		ctx,
		`SELECT `+projectChatColumns+` FROM project_chat_messages
		 `+where+`
		 ORDER BY created_at DESC, id DESC
		 LIMIT $`+fmt.Sprint(len(args)),
		args...,
	)
	if err != nil {
		return nil, false, fmt.Errorf("failed to list project chat messages: %w", err)
	}
	defer rows.Close()

	out := make([]ProjectChatMessage, 0, limit+1)
	for rows.Next() {
		message, err := scanProjectChatMessage(rows)
		if err != nil {
			return nil, false, fmt.Errorf("failed to scan project chat message: %w", err)
		}
		if message.OrgID != workspaceID {
			return nil, false, ErrForbidden
		}
		out = append(out, message)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("failed reading project chat rows: %w", err)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (s *ProjectChatStore) Search(ctx context.Context, input SearchProjectChatInput) ([]ProjectChatSearchResult, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ensureProjectInWorkspace(ctx, conn, workspaceID, projectID); err != nil {
		return nil, err
	}

	where := []string{
		`org_id = $1`,
		`project_id = $2`,
		`search_document @@ plainto_tsquery('english', $3)`,
	}
	args := []any{workspaceID, projectID, query}

	if input.Author != nil && strings.TrimSpace(*input.Author) != "" {
		where = append(where, fmt.Sprintf("author = $%d", len(args)+1))
		args = append(args, strings.TrimSpace(*input.Author))
	}
	if input.From != nil {
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)+1))
		args = append(args, input.From.UTC())
	}
	if input.To != nil {
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)+1))
		args = append(args, input.To.UTC())
	}

	args = append(args, limit)
	limitArg := fmt.Sprintf("$%d", len(args))

	querySQL := `
		SELECT
			` + projectChatColumns + `,
			ts_rank(search_document, plainto_tsquery('english', $3)) AS relevance,
			ts_headline(
				'english',
				body,
				plainto_tsquery('english', $3),
				'StartSel=<mark>,StopSel=</mark>,MaxFragments=2,MinWords=4,MaxWords=18'
			) AS snippet
		FROM project_chat_messages
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY
			(CASE WHEN position(lower($3) IN lower(body)) > 0 THEN 1 ELSE 0 END) DESC,
			relevance DESC,
			created_at DESC,
			id DESC
		LIMIT ` + limitArg

	rows, err := conn.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search project chat: %w", err)
	}
	defer rows.Close()

	results := make([]ProjectChatSearchResult, 0, limit)
	for rows.Next() {
		result, err := scanProjectChatSearchResult(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan project chat result: %w", err)
		}
		if result.Message.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading project chat search rows: %w", err)
	}
	return results, nil
}

func ensureProjectInWorkspace(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	workspaceID, projectID string,
) error {
	var projectOrgID string
	err := q.QueryRowContext(ctx, `SELECT org_id FROM projects WHERE id = $1`, projectID).Scan(&projectOrgID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return fmt.Errorf("failed to verify project ownership: %w", err)
	}
	if projectOrgID != workspaceID {
		return ErrForbidden
	}
	return nil
}

func scanProjectChatMessage(scanner interface{ Scan(dest ...any) error }) (ProjectChatMessage, error) {
	var message ProjectChatMessage
	err := scanner.Scan(
		&message.ID,
		&message.OrgID,
		&message.ProjectID,
		&message.Author,
		&message.Body,
		&message.CreatedAt,
		&message.UpdatedAt,
	)
	return message, err
}

func scanProjectChatSearchResult(scanner interface{ Scan(dest ...any) error }) (ProjectChatSearchResult, error) {
	var (
		result  ProjectChatSearchResult
		snippet sql.NullString
	)
	err := scanner.Scan(
		&result.Message.ID,
		&result.Message.OrgID,
		&result.Message.ProjectID,
		&result.Message.Author,
		&result.Message.Body,
		&result.Message.CreatedAt,
		&result.Message.UpdatedAt,
		&result.Relevance,
		&snippet,
	)
	if err != nil {
		return result, err
	}
	if snippet.Valid {
		result.Snippet = snippet.String
	}
	return result, nil
}
