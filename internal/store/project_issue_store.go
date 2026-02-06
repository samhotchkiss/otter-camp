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

type ProjectIssue struct {
	ID          string     `json:"id"`
	OrgID       string     `json:"org_id"`
	ProjectID   string     `json:"project_id"`
	IssueNumber int64      `json:"issue_number"`
	Title       string     `json:"title"`
	Body        *string    `json:"body,omitempty"`
	State       string     `json:"state"`
	Origin      string     `json:"origin"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

type CreateProjectIssueInput struct {
	ProjectID string
	Title     string
	Body      *string
	State     string
	Origin    string
	ClosedAt  *time.Time
}

type ProjectIssueFilter struct {
	ProjectID string
	State     *string
	Origin    *string
	Limit     int
}

type ProjectIssueGitHubLink struct {
	ID                 string    `json:"id"`
	OrgID              string    `json:"org_id"`
	IssueID            string    `json:"issue_id"`
	RepositoryFullName string    `json:"repository_full_name"`
	GitHubNumber       int64     `json:"github_number"`
	GitHubURL          *string   `json:"github_url,omitempty"`
	GitHubState        string    `json:"github_state"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

type UpsertProjectIssueGitHubLinkInput struct {
	IssueID            string
	RepositoryFullName string
	GitHubNumber       int64
	GitHubURL          *string
	GitHubState        string
}

type ProjectIssueSyncCheckpoint struct {
	ID                 string    `json:"id"`
	OrgID              string    `json:"org_id"`
	ProjectID          string    `json:"project_id"`
	RepositoryFullName string    `json:"repository_full_name"`
	Resource           string    `json:"resource"`
	Cursor             *string   `json:"cursor,omitempty"`
	LastSyncedAt       time.Time `json:"last_synced_at"`
}

type UpsertProjectIssueSyncCheckpointInput struct {
	ProjectID          string
	RepositoryFullName string
	Resource           string
	Cursor             *string
	LastSyncedAt       *time.Time
}

type ProjectIssueStore struct {
	db *sql.DB
}

func NewProjectIssueStore(db *sql.DB) *ProjectIssueStore {
	return &ProjectIssueStore{db: db}
}

func (s *ProjectIssueStore) CreateIssue(ctx context.Context, input CreateProjectIssueInput) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	state := normalizeIssueState(input.State)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid state")
	}

	origin := strings.TrimSpace(strings.ToLower(input.Origin))
	if origin == "" {
		origin = "local"
	}
	if origin != "local" && origin != "github" {
		return nil, fmt.Errorf("origin must be local or github")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}

	var nextIssueNumber int64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(issue_number), 0) + 1 FROM project_issues WHERE project_id = $1`,
		projectID,
	).Scan(&nextIssueNumber); err != nil {
		return nil, fmt.Errorf("failed to allocate issue number: %w", err)
	}

	record, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issues (
			org_id, project_id, issue_number, title, body, state, origin, closed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, org_id, project_id, issue_number, title, body, state, origin, created_at, updated_at, closed_at`,
		workspaceID,
		projectID,
		nextIssueNumber,
		title,
		nullableString(input.Body),
		state,
		origin,
		input.ClosedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue create: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListIssues(ctx context.Context, filter ProjectIssueFilter) ([]ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID := strings.TrimSpace(filter.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	query := `SELECT id, org_id, project_id, issue_number, title, body, state, origin, created_at, updated_at, closed_at
		FROM project_issues WHERE project_id = $1`
	args := []any{projectID}
	argPos := 2

	if filter.State != nil && strings.TrimSpace(*filter.State) != "" {
		state := normalizeIssueState(*filter.State)
		if !isValidIssueState(state) {
			return nil, fmt.Errorf("invalid state filter")
		}
		query += fmt.Sprintf(" AND state = $%d", argPos)
		args = append(args, state)
		argPos++
	}
	if filter.Origin != nil && strings.TrimSpace(*filter.Origin) != "" {
		origin := strings.TrimSpace(strings.ToLower(*filter.Origin))
		if origin != "local" && origin != "github" {
			return nil, fmt.Errorf("invalid origin filter")
		}
		query += fmt.Sprintf(" AND origin = $%d", argPos)
		args = append(args, origin)
		argPos++
	}

	query += fmt.Sprintf(" ORDER BY issue_number DESC LIMIT $%d", argPos)
	args = append(args, limit)

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssue, 0)
	for rows.Next() {
		issue, err := scanProjectIssue(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue row: %w", err)
		}
		if issue.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) UpsertGitHubLink(
	ctx context.Context,
	input UpsertProjectIssueGitHubLinkInput,
) (*ProjectIssueGitHubLink, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, fmt.Errorf("github_number must be greater than zero")
	}
	state := normalizeIssueState(input.GitHubState)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid github_state")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueGitHubLink(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_github_links (
			org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
		) VALUES ($1,$2,$3,$4,$5,$6,NOW())
		ON CONFLICT (issue_id)
		DO UPDATE SET
			repository_full_name = EXCLUDED.repository_full_name,
			github_number = EXCLUDED.github_number,
			github_url = EXCLUDED.github_url,
			github_state = EXCLUDED.github_state,
			last_synced_at = NOW()
		RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
		workspaceID,
		issueID,
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubURL),
		state,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert issue github link: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue github link upsert: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) UpsertSyncCheckpoint(
	ctx context.Context,
	input UpsertProjectIssueSyncCheckpointInput,
) (*ProjectIssueSyncCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	resource := strings.TrimSpace(strings.ToLower(input.Resource))
	if resource == "" {
		return nil, fmt.Errorf("resource is required")
	}

	lastSynced := time.Now().UTC()
	if input.LastSyncedAt != nil && !input.LastSyncedAt.IsZero() {
		lastSynced = input.LastSyncedAt.UTC()
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueSyncCheckpoint(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_sync_checkpoints (
			org_id, project_id, repository_full_name, resource, cursor, last_synced_at
		) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (project_id, repository_full_name, resource)
		DO UPDATE SET
			cursor = EXCLUDED.cursor,
			last_synced_at = EXCLUDED.last_synced_at
		RETURNING id, org_id, project_id, repository_full_name, resource, cursor, last_synced_at`,
		workspaceID,
		projectID,
		repo,
		resource,
		nullableString(input.Cursor),
		lastSynced,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert issue sync checkpoint: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue sync checkpoint upsert: %w", err)
	}
	return &record, nil
}

func ensureProjectVisible(ctx context.Context, q Querier, projectID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM projects WHERE id = $1`, projectID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func ensureIssueVisible(ctx context.Context, q Querier, issueID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM project_issues WHERE id = $1`, issueID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func scanProjectIssue(scanner interface{ Scan(...any) error }) (ProjectIssue, error) {
	var issue ProjectIssue
	var body sql.NullString
	var closedAt sql.NullTime

	err := scanner.Scan(
		&issue.ID,
		&issue.OrgID,
		&issue.ProjectID,
		&issue.IssueNumber,
		&issue.Title,
		&body,
		&issue.State,
		&issue.Origin,
		&issue.CreatedAt,
		&issue.UpdatedAt,
		&closedAt,
	)
	if err != nil {
		return issue, err
	}
	if body.Valid {
		issue.Body = &body.String
	}
	if closedAt.Valid {
		issue.ClosedAt = &closedAt.Time
	}
	return issue, nil
}

func scanProjectIssueGitHubLink(scanner interface{ Scan(...any) error }) (ProjectIssueGitHubLink, error) {
	var link ProjectIssueGitHubLink
	var githubURL sql.NullString

	err := scanner.Scan(
		&link.ID,
		&link.OrgID,
		&link.IssueID,
		&link.RepositoryFullName,
		&link.GitHubNumber,
		&githubURL,
		&link.GitHubState,
		&link.LastSyncedAt,
	)
	if err != nil {
		return link, err
	}
	if githubURL.Valid {
		link.GitHubURL = &githubURL.String
	}
	return link, nil
}

func scanProjectIssueSyncCheckpoint(scanner interface{ Scan(...any) error }) (ProjectIssueSyncCheckpoint, error) {
	var checkpoint ProjectIssueSyncCheckpoint
	var cursor sql.NullString

	err := scanner.Scan(
		&checkpoint.ID,
		&checkpoint.OrgID,
		&checkpoint.ProjectID,
		&checkpoint.RepositoryFullName,
		&checkpoint.Resource,
		&cursor,
		&checkpoint.LastSyncedAt,
	)
	if err != nil {
		return checkpoint, err
	}
	if cursor.Valid {
		checkpoint.Cursor = &cursor.String
	}
	return checkpoint, nil
}
