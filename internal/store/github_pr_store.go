package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type GitHubIssueRecord struct {
	ID                 string     `json:"id"`
	OrgID              string     `json:"org_id"`
	ProjectID          string     `json:"project_id"`
	RepositoryFullName string     `json:"repository_full_name"`
	GitHubNumber       int64      `json:"github_number"`
	GitHubNodeID       *string    `json:"github_node_id,omitempty"`
	Title              string     `json:"title"`
	State              string     `json:"state"`
	Body               *string    `json:"body,omitempty"`
	AuthorLogin        *string    `json:"author_login,omitempty"`
	IsPullRequest      bool       `json:"is_pull_request"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	LastSyncedAt       time.Time  `json:"last_synced_at"`
}

type GitHubPullRequestRecord struct {
	ID                 string     `json:"id"`
	OrgID              string     `json:"org_id"`
	ProjectID          string     `json:"project_id"`
	IssueID            *string    `json:"issue_id,omitempty"`
	RepositoryFullName string     `json:"repository_full_name"`
	GitHubNumber       int64      `json:"github_number"`
	GitHubNodeID       *string    `json:"github_node_id,omitempty"`
	Title              string     `json:"title"`
	State              string     `json:"state"`
	Draft              bool       `json:"draft"`
	Mergeable          *bool      `json:"mergeable,omitempty"`
	MergeableState     *string    `json:"mergeable_state,omitempty"`
	HeadRef            string     `json:"head_ref"`
	HeadSHA            string     `json:"head_sha"`
	BaseRef            string     `json:"base_ref"`
	BaseSHA            *string    `json:"base_sha,omitempty"`
	Merged             bool       `json:"merged"`
	MergedAt           *time.Time `json:"merged_at,omitempty"`
	MergedCommitSHA    *string    `json:"merged_commit_sha,omitempty"`
	AuthorLogin        *string    `json:"author_login,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
	LastSyncedAt       time.Time  `json:"last_synced_at"`
}

type UpsertGitHubIssueInput struct {
	ProjectID          string
	RepositoryFullName string
	GitHubNumber       int64
	GitHubNodeID       *string
	Title              string
	State              string
	Body               *string
	AuthorLogin        *string
	IsPullRequest      bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ClosedAt           *time.Time
}

type UpsertGitHubPullRequestInput struct {
	ProjectID          string
	IssueID            *string
	RepositoryFullName string
	GitHubNumber       int64
	GitHubNodeID       *string
	Title              string
	State              string
	Draft              bool
	Mergeable          *bool
	MergeableState     *string
	HeadRef            string
	HeadSHA            string
	BaseRef            string
	BaseSHA            *string
	Merged             bool
	MergedAt           *time.Time
	MergedCommitSHA    *string
	AuthorLogin        *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ClosedAt           *time.Time
}

type GitHubIssuePRStore struct {
	db *sql.DB
}

func NewGitHubIssuePRStore(db *sql.DB) *GitHubIssuePRStore {
	return &GitHubIssuePRStore{db: db}
}

const githubIssueColumns = `
	id,
	org_id,
	project_id,
	repository_full_name,
	github_number,
	github_node_id,
	title,
	state,
	body,
	author_login,
	is_pull_request,
	created_at,
	updated_at,
	closed_at,
	last_synced_at
`

const githubPullRequestColumns = `
	id,
	org_id,
	project_id,
	issue_id,
	repository_full_name,
	github_number,
	github_node_id,
	title,
	state,
	draft,
	mergeable,
	mergeable_state,
	head_ref,
	head_sha,
	base_ref,
	base_sha,
	merged,
	merged_at,
	merged_commit_sha,
	author_login,
	created_at,
	updated_at,
	closed_at,
	last_synced_at
`

func (s *GitHubIssuePRStore) UpsertIssue(ctx context.Context, input UpsertGitHubIssueInput) (*GitHubIssueRecord, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	if !uuidRegex.MatchString(strings.TrimSpace(input.ProjectID)) {
		return nil, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, fmt.Errorf("github_number must be greater than zero")
	}
	state := normalizeIssueState(input.State)
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid state")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}

	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := input.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanGitHubIssueRecord(conn.QueryRowContext(
		ctx,
		`INSERT INTO project_github_issues (
			org_id,
			project_id,
			repository_full_name,
			github_number,
			github_node_id,
			title,
			state,
			body,
			author_login,
			is_pull_request,
			created_at,
			updated_at,
			closed_at,
			last_synced_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NOW())
		ON CONFLICT (project_id, repository_full_name, github_number)
		DO UPDATE SET
			github_node_id = EXCLUDED.github_node_id,
			title = EXCLUDED.title,
			state = EXCLUDED.state,
			body = EXCLUDED.body,
			author_login = EXCLUDED.author_login,
			is_pull_request = EXCLUDED.is_pull_request,
			updated_at = EXCLUDED.updated_at,
			closed_at = EXCLUDED.closed_at,
			last_synced_at = NOW()
		RETURNING`+githubIssueColumns,
		workspaceID,
		input.ProjectID,
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubNodeID),
		title,
		state,
		nullableString(input.Body),
		nullableString(input.AuthorLogin),
		input.IsPullRequest,
		createdAt,
		updatedAt,
		input.ClosedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert github issue: %w", err)
	}

	return &record, nil
}

func (s *GitHubIssuePRStore) UpsertPullRequest(
	ctx context.Context,
	input UpsertGitHubPullRequestInput,
) (*GitHubPullRequestRecord, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	if !uuidRegex.MatchString(strings.TrimSpace(input.ProjectID)) {
		return nil, fmt.Errorf("invalid project_id")
	}
	if input.IssueID != nil && !uuidRegex.MatchString(strings.TrimSpace(*input.IssueID)) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, fmt.Errorf("github_number must be greater than zero")
	}
	state := normalizeIssueState(input.State)
	if !isValidIssueState(state) {
		return nil, fmt.Errorf("invalid state")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	headRef := strings.TrimSpace(input.HeadRef)
	if headRef == "" {
		return nil, fmt.Errorf("head_ref is required")
	}
	headSHA := strings.TrimSpace(input.HeadSHA)
	if headSHA == "" {
		return nil, fmt.Errorf("head_sha is required")
	}
	baseRef := strings.TrimSpace(input.BaseRef)
	if baseRef == "" {
		return nil, fmt.Errorf("base_ref is required")
	}

	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := input.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	record, err := scanGitHubPullRequestRecord(conn.QueryRowContext(
		ctx,
		`INSERT INTO project_github_pull_requests (
			org_id,
			project_id,
			issue_id,
			repository_full_name,
			github_number,
			github_node_id,
			title,
			state,
			draft,
			mergeable,
			mergeable_state,
			head_ref,
			head_sha,
			base_ref,
			base_sha,
			merged,
			merged_at,
			merged_commit_sha,
			author_login,
			created_at,
			updated_at,
			closed_at,
			last_synced_at
		)
		VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,NOW()
		)
		ON CONFLICT (project_id, repository_full_name, github_number)
		DO UPDATE SET
			issue_id = EXCLUDED.issue_id,
			github_node_id = EXCLUDED.github_node_id,
			title = EXCLUDED.title,
			state = EXCLUDED.state,
			draft = EXCLUDED.draft,
			mergeable = EXCLUDED.mergeable,
			mergeable_state = EXCLUDED.mergeable_state,
			head_ref = EXCLUDED.head_ref,
			head_sha = EXCLUDED.head_sha,
			base_ref = EXCLUDED.base_ref,
			base_sha = EXCLUDED.base_sha,
			merged = EXCLUDED.merged,
			merged_at = EXCLUDED.merged_at,
			merged_commit_sha = EXCLUDED.merged_commit_sha,
			author_login = EXCLUDED.author_login,
			updated_at = EXCLUDED.updated_at,
			closed_at = EXCLUDED.closed_at,
			last_synced_at = NOW()
		RETURNING`+githubPullRequestColumns,
		workspaceID,
		input.ProjectID,
		nullableString(input.IssueID),
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubNodeID),
		title,
		state,
		input.Draft,
		input.Mergeable,
		nullableString(input.MergeableState),
		headRef,
		headSHA,
		baseRef,
		nullableString(input.BaseSHA),
		input.Merged,
		input.MergedAt,
		nullableString(input.MergedCommitSHA),
		nullableString(input.AuthorLogin),
		createdAt,
		updatedAt,
		input.ClosedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert github pull request: %w", err)
	}

	return &record, nil
}

func (s *GitHubIssuePRStore) ListPullRequests(
	ctx context.Context,
	projectID string,
	stateFilter *string,
	limit int,
) ([]GitHubPullRequestRecord, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	args := []any{projectID, limit}
	query := `SELECT` + githubPullRequestColumns + `
		FROM project_github_pull_requests
		WHERE project_id = $1`
	if stateFilter != nil && strings.TrimSpace(*stateFilter) != "" {
		normalized := normalizeIssueState(*stateFilter)
		if !isValidIssueState(normalized) {
			return nil, fmt.Errorf("invalid state filter")
		}
		query += ` AND state = $3`
		args = append(args, normalized)
	}
	query += ` ORDER BY updated_at DESC LIMIT $2`

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list github pull requests: %w", err)
	}
	defer rows.Close()

	out := make([]GitHubPullRequestRecord, 0)
	for rows.Next() {
		record, err := scanGitHubPullRequestRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan github pull request row: %w", err)
		}
		if record.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read github pull request rows: %w", err)
	}

	return out, nil
}

func normalizeIssueState(state string) string {
	return strings.TrimSpace(strings.ToLower(state))
}

func isValidIssueState(state string) bool {
	switch normalizeIssueState(state) {
	case "open", "closed":
		return true
	default:
		return false
	}
}

func scanGitHubIssueRecord(scanner interface{ Scan(...any) error }) (GitHubIssueRecord, error) {
	var record GitHubIssueRecord
	var githubNodeID sql.NullString
	var body sql.NullString
	var authorLogin sql.NullString
	var closedAt sql.NullTime

	err := scanner.Scan(
		&record.ID,
		&record.OrgID,
		&record.ProjectID,
		&record.RepositoryFullName,
		&record.GitHubNumber,
		&githubNodeID,
		&record.Title,
		&record.State,
		&body,
		&authorLogin,
		&record.IsPullRequest,
		&record.CreatedAt,
		&record.UpdatedAt,
		&closedAt,
		&record.LastSyncedAt,
	)
	if err != nil {
		return record, err
	}

	if githubNodeID.Valid {
		record.GitHubNodeID = &githubNodeID.String
	}
	if body.Valid {
		record.Body = &body.String
	}
	if authorLogin.Valid {
		record.AuthorLogin = &authorLogin.String
	}
	if closedAt.Valid {
		record.ClosedAt = &closedAt.Time
	}

	return record, nil
}

func scanGitHubPullRequestRecord(scanner interface{ Scan(...any) error }) (GitHubPullRequestRecord, error) {
	var record GitHubPullRequestRecord
	var issueID sql.NullString
	var githubNodeID sql.NullString
	var mergeable sql.NullBool
	var mergeableState sql.NullString
	var baseSHA sql.NullString
	var mergedAt sql.NullTime
	var mergedCommitSHA sql.NullString
	var authorLogin sql.NullString
	var closedAt sql.NullTime

	err := scanner.Scan(
		&record.ID,
		&record.OrgID,
		&record.ProjectID,
		&issueID,
		&record.RepositoryFullName,
		&record.GitHubNumber,
		&githubNodeID,
		&record.Title,
		&record.State,
		&record.Draft,
		&mergeable,
		&mergeableState,
		&record.HeadRef,
		&record.HeadSHA,
		&record.BaseRef,
		&baseSHA,
		&record.Merged,
		&mergedAt,
		&mergedCommitSHA,
		&authorLogin,
		&record.CreatedAt,
		&record.UpdatedAt,
		&closedAt,
		&record.LastSyncedAt,
	)
	if err != nil {
		return record, err
	}

	if issueID.Valid {
		record.IssueID = &issueID.String
	}
	if githubNodeID.Valid {
		record.GitHubNodeID = &githubNodeID.String
	}
	if mergeable.Valid {
		record.Mergeable = &mergeable.Bool
	}
	if mergeableState.Valid {
		record.MergeableState = &mergeableState.String
	}
	if baseSHA.Valid {
		record.BaseSHA = &baseSHA.String
	}
	if mergedAt.Valid {
		record.MergedAt = &mergedAt.Time
	}
	if mergedCommitSHA.Valid {
		record.MergedCommitSHA = &mergedCommitSHA.String
	}
	if authorLogin.Valid {
		record.AuthorLogin = &authorLogin.String
	}
	if closedAt.Valid {
		record.ClosedAt = &closedAt.Time
	}

	return record, nil
}
