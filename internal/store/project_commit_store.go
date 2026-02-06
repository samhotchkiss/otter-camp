package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type ProjectCommit struct {
	ID                 string          `json:"id"`
	OrgID              string          `json:"org_id"`
	ProjectID          string          `json:"project_id"`
	RepositoryFullName string          `json:"repository_full_name"`
	BranchName         string          `json:"branch_name"`
	SHA                string          `json:"sha"`
	ParentSHA          *string         `json:"parent_sha,omitempty"`
	AuthorName         string          `json:"author_name"`
	AuthorEmail        *string         `json:"author_email,omitempty"`
	AuthoredAt         time.Time       `json:"authored_at"`
	Subject            string          `json:"subject"`
	Body               *string         `json:"body,omitempty"`
	Message            string          `json:"message"`
	Metadata           json.RawMessage `json:"metadata"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type UpsertProjectCommitInput struct {
	ProjectID          string
	RepositoryFullName string
	BranchName         string
	SHA                string
	ParentSHA          *string
	AuthorName         string
	AuthorEmail        *string
	AuthoredAt         *time.Time
	Subject            string
	Body               *string
	Message            string
	Metadata           json.RawMessage
}

type ProjectCommitFilter struct {
	ProjectID string
	Branch    *string
	Limit     int
	Offset    int
}

type ProjectCommitStore struct {
	db *sql.DB
}

func NewProjectCommitStore(db *sql.DB) *ProjectCommitStore {
	return &ProjectCommitStore{db: db}
}

const projectCommitColumns = `
	id,
	org_id,
	project_id,
	repository_full_name,
	branch_name,
	sha,
	parent_sha,
	author_name,
	author_email,
	authored_at,
	subject,
	body,
	message,
	metadata,
	created_at,
	updated_at
`

func (s *ProjectCommitStore) UpsertCommit(
	ctx context.Context,
	input UpsertProjectCommitInput,
) (*ProjectCommit, bool, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, false, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, false, fmt.Errorf("invalid project_id")
	}

	repositoryFullName := strings.TrimSpace(input.RepositoryFullName)
	if repositoryFullName == "" {
		return nil, false, fmt.Errorf("repository_full_name is required")
	}

	branchName := strings.TrimSpace(input.BranchName)
	if branchName == "" {
		return nil, false, fmt.Errorf("branch_name is required")
	}

	sha := strings.TrimSpace(input.SHA)
	if sha == "" {
		return nil, false, fmt.Errorf("sha is required")
	}

	authorName := strings.TrimSpace(input.AuthorName)
	if authorName == "" {
		authorName = "Unknown"
	}

	message := strings.TrimSpace(input.Message)
	if message == "" {
		message = strings.TrimSpace(input.Subject)
	}
	if message == "" {
		return nil, false, fmt.Errorf("message is required")
	}

	subject := strings.TrimSpace(input.Subject)
	if subject == "" {
		subject = message
		if newline := strings.Index(subject, "\n"); newline >= 0 {
			subject = strings.TrimSpace(subject[:newline])
		}
	}

	body := input.Body
	if body == nil {
		if newline := strings.Index(message, "\n"); newline >= 0 {
			rest := strings.TrimSpace(message[newline+1:])
			if rest != "" {
				body = &rest
			}
		}
	}

	authoredAt := time.Now().UTC()
	if input.AuthoredAt != nil {
		authoredAt = input.AuthoredAt.UTC()
	}

	metadata := input.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	var existingID string
	existingErr := conn.QueryRowContext(
		ctx,
		`SELECT id
			FROM project_commits
			WHERE project_id = $1 AND repository_full_name = $2 AND branch_name = $3 AND sha = $4`,
		projectID,
		repositoryFullName,
		branchName,
		sha,
	).Scan(&existingID)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("failed to check existing commit: %w", existingErr)
	}
	created := errors.Is(existingErr, sql.ErrNoRows)

	commit, err := scanProjectCommit(conn.QueryRowContext(
		ctx,
		`INSERT INTO project_commits (
			org_id,
			project_id,
			repository_full_name,
			branch_name,
			sha,
			parent_sha,
			author_name,
			author_email,
			authored_at,
			subject,
			body,
			message,
			metadata
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13
		)
		ON CONFLICT (project_id, repository_full_name, branch_name, sha)
		DO UPDATE SET
			parent_sha = EXCLUDED.parent_sha,
			author_name = EXCLUDED.author_name,
			author_email = EXCLUDED.author_email,
			authored_at = EXCLUDED.authored_at,
			subject = EXCLUDED.subject,
			body = EXCLUDED.body,
			message = EXCLUDED.message,
			metadata = EXCLUDED.metadata,
			updated_at = NOW()
		RETURNING`+projectCommitColumns,
		workspaceID,
		projectID,
		repositoryFullName,
		branchName,
		sha,
		nullableString(input.ParentSHA),
		authorName,
		nullableString(input.AuthorEmail),
		authoredAt,
		subject,
		nullableString(body),
		message,
		metadata,
	))
	if err != nil {
		return nil, false, fmt.Errorf("failed to upsert project commit: %w", err)
	}

	return &commit, created, nil
}

func (s *ProjectCommitStore) ListCommits(ctx context.Context, filter ProjectCommitFilter) ([]ProjectCommit, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(filter.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := `SELECT` + projectCommitColumns + `
		FROM project_commits
		WHERE project_id = $1`
	args := []any{projectID}
	argPos := 2

	if filter.Branch != nil && strings.TrimSpace(*filter.Branch) != "" {
		query += fmt.Sprintf(" AND branch_name = $%d", argPos)
		args = append(args, strings.TrimSpace(*filter.Branch))
		argPos++
	}

	query += fmt.Sprintf(" ORDER BY authored_at DESC, created_at DESC LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, limit, offset)

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectCommit, 0)
	for rows.Next() {
		item, err := scanProjectCommit(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan commit: %w", err)
		}
		if item.OrgID != workspaceID {
			continue
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading commit rows: %w", err)
	}
	return items, nil
}

func (s *ProjectCommitStore) GetCommitBySHA(ctx context.Context, projectID, sha string) (*ProjectCommit, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil, fmt.Errorf("sha is required")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	item, err := scanProjectCommit(conn.QueryRowContext(
		ctx,
		`SELECT`+projectCommitColumns+`
			FROM project_commits
			WHERE project_id = $1 AND sha = $2
			ORDER BY authored_at DESC, created_at DESC
			LIMIT 1`,
		projectID,
		sha,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get commit by sha: %w", err)
	}
	if item.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &item, nil
}

func scanProjectCommit(scanner interface{ Scan(...any) error }) (ProjectCommit, error) {
	var commit ProjectCommit
	var parentSHA sql.NullString
	var authorEmail sql.NullString
	var body sql.NullString
	var metadata []byte

	err := scanner.Scan(
		&commit.ID,
		&commit.OrgID,
		&commit.ProjectID,
		&commit.RepositoryFullName,
		&commit.BranchName,
		&commit.SHA,
		&parentSHA,
		&commit.AuthorName,
		&authorEmail,
		&commit.AuthoredAt,
		&commit.Subject,
		&body,
		&commit.Message,
		&metadata,
		&commit.CreatedAt,
		&commit.UpdatedAt,
	)
	if err != nil {
		return commit, err
	}

	if parentSHA.Valid {
		commit.ParentSHA = &parentSHA.String
	}
	if authorEmail.Valid {
		commit.AuthorEmail = &authorEmail.String
	}
	if body.Valid {
		commit.Body = &body.String
	}
	if len(metadata) == 0 {
		commit.Metadata = json.RawMessage(`{}`)
	} else {
		commit.Metadata = json.RawMessage(metadata)
	}

	return commit, nil
}
