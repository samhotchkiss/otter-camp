package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type ProjectIssue struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	IssueNumber   int64      `json:"issue_number"`
	Title         string     `json:"title"`
	Body          *string    `json:"body,omitempty"`
	State         string     `json:"state"`
	Origin        string     `json:"origin"`
	DocumentPath  *string    `json:"document_path,omitempty"`
	ApprovalState string     `json:"approval_state"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

type CreateProjectIssueInput struct {
	ProjectID     string
	Title         string
	Body          *string
	State         string
	Origin        string
	DocumentPath  *string
	ApprovalState string
	ClosedAt      *time.Time
}

type UpsertProjectIssueFromGitHubInput struct {
	ProjectID          string
	RepositoryFullName string
	GitHubNumber       int64
	Title              string
	Body               *string
	State              string
	GitHubURL          *string
	ClosedAt           *time.Time
}

type ProjectIssueFilter struct {
	ProjectID string
	State     *string
	Origin    *string
	Kind      *string
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

type ProjectIssueCounts struct {
	Total        int `json:"total"`
	Open         int `json:"open"`
	Closed       int `json:"closed"`
	GitHubOrigin int `json:"github_origin"`
	LocalOrigin  int `json:"local_origin"`
	PullRequests int `json:"pull_requests"`
}

type UpsertProjectIssueSyncCheckpointInput struct {
	ProjectID          string
	RepositoryFullName string
	Resource           string
	Cursor             *string
	LastSyncedAt       *time.Time
}

type ProjectIssueParticipant struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"org_id"`
	IssueID   string     `json:"issue_id"`
	AgentID   string     `json:"agent_id"`
	Role      string     `json:"role"`
	JoinedAt  time.Time  `json:"joined_at"`
	RemovedAt *time.Time `json:"removed_at,omitempty"`
}

type AddProjectIssueParticipantInput struct {
	IssueID string
	AgentID string
	Role    string
}

type ProjectIssueComment struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	IssueID       string    `json:"issue_id"`
	AuthorAgentID string    `json:"author_agent_id"`
	Body          string    `json:"body"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CreateProjectIssueCommentInput struct {
	IssueID       string
	AuthorAgentID string
	Body          string
}

type ProjectIssueStore struct {
	db *sql.DB
}

func NewProjectIssueStore(db *sql.DB) *ProjectIssueStore {
	return &ProjectIssueStore{db: db}
}

const (
	IssueApprovalStateDraft          = "draft"
	IssueApprovalStateReadyForReview = "ready_for_review"
	IssueApprovalStateNeedsChanges   = "needs_changes"
	IssueApprovalStateApproved       = "approved"
)

func normalizeIssueApprovalState(state string) string {
	return strings.TrimSpace(strings.ToLower(state))
}

func isValidIssueApprovalState(state string) bool {
	switch normalizeIssueApprovalState(state) {
	case IssueApprovalStateDraft, IssueApprovalStateReadyForReview, IssueApprovalStateNeedsChanges, IssueApprovalStateApproved:
		return true
	default:
		return false
	}
}

func canTransitionIssueApprovalState(currentState, nextState string) bool {
	current := normalizeIssueApprovalState(currentState)
	next := normalizeIssueApprovalState(nextState)
	if current == next {
		return true
	}

	switch current {
	case IssueApprovalStateDraft:
		return next == IssueApprovalStateReadyForReview
	case IssueApprovalStateReadyForReview:
		return next == IssueApprovalStateNeedsChanges || next == IssueApprovalStateApproved
	case IssueApprovalStateNeedsChanges:
		return next == IssueApprovalStateReadyForReview
	case IssueApprovalStateApproved:
		return false
	default:
		return false
	}
}

func defaultApprovalStateForLegacyState(issueState string) string {
	if normalizeIssueState(issueState) == "closed" {
		return IssueApprovalStateApproved
	}
	return IssueApprovalStateDraft
}

func normalizeIssueDocumentPath(documentPath *string) (*string, error) {
	if documentPath == nil {
		return nil, nil
	}

	value := strings.TrimSpace(strings.ReplaceAll(*documentPath, "\\", "/"))
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}

	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return nil, fmt.Errorf("document_path must not traverse directories")
		}
	}

	clean := path.Clean(value)
	if !strings.HasPrefix(clean, "/posts/") || !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return nil, fmt.Errorf("document_path must point to /posts/*.md")
	}

	return &clean, nil
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

	documentPath, err := normalizeIssueDocumentPath(input.DocumentPath)
	if err != nil {
		return nil, err
	}

	approvalState := normalizeIssueApprovalState(input.ApprovalState)
	if approvalState == "" {
		approvalState = defaultApprovalStateForLegacyState(state)
	}
	if !isValidIssueApprovalState(approvalState) {
		return nil, fmt.Errorf("invalid approval_state")
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
			org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, closed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at`,
		workspaceID,
		projectID,
		nextIssueNumber,
		title,
		nullableString(input.Body),
		state,
		origin,
		nullableString(documentPath),
		approvalState,
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

func (s *ProjectIssueStore) TransitionApprovalState(
	ctx context.Context,
	issueID string,
	nextState string,
) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	normalizedNext := normalizeIssueApprovalState(nextState)
	if !isValidIssueApprovalState(normalizedNext) {
		return nil, fmt.Errorf("invalid approval_state")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1
			FOR UPDATE`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load issue: %w", err)
	}
	if current.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	currentState := normalizeIssueApprovalState(current.ApprovalState)
	if currentState == "" {
		currentState = defaultApprovalStateForLegacyState(current.State)
	}
	if !canTransitionIssueApprovalState(currentState, normalizedNext) {
		return nil, fmt.Errorf("invalid approval_state transition")
	}

	updated := current
	if currentState != normalizedNext {
		updateQuery := `UPDATE project_issues
				SET approval_state = $2
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at`
		if normalizedNext == IssueApprovalStateApproved {
			updateQuery = `UPDATE project_issues
				SET approval_state = $2,
					state = 'closed',
					closed_at = COALESCE(closed_at, NOW())
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at`
		}

		updated, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			updateQuery,
			issueID,
			normalizedNext,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to update approval_state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *ProjectIssueStore) UpsertIssueFromGitHub(
	ctx context.Context,
	input UpsertProjectIssueFromGitHubInput,
) (*ProjectIssue, bool, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, false, ErrNoWorkspace
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, false, fmt.Errorf("invalid project_id")
	}
	repo := strings.TrimSpace(input.RepositoryFullName)
	if repo == "" {
		return nil, false, fmt.Errorf("repository_full_name is required")
	}
	if input.GitHubNumber <= 0 {
		return nil, false, fmt.Errorf("github_number must be greater than zero")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, false, fmt.Errorf("title is required")
	}
	state := normalizeIssueState(input.State)
	if state == "" {
		state = "open"
	}
	if !isValidIssueState(state) {
		return nil, false, fmt.Errorf("invalid state")
	}
	approvalState := defaultApprovalStateForLegacyState(state)

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureProjectVisible(ctx, tx, projectID); err != nil {
		return nil, false, err
	}

	existingIssue, err := scanProjectIssue(tx.QueryRowContext(
		ctx,
		`SELECT i.id, i.org_id, i.project_id, i.issue_number, i.title, i.body, i.state, i.origin, i.document_path, i.approval_state, i.created_at, i.updated_at, i.closed_at
			FROM project_issues i
			JOIN project_issue_github_links l ON l.issue_id = i.id
			WHERE i.project_id = $1 AND l.repository_full_name = $2 AND l.github_number = $3
			LIMIT 1`,
		projectID,
		repo,
		input.GitHubNumber,
	))
	created := false
	var issue ProjectIssue
	switch {
	case err == nil:
		issue, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			`UPDATE project_issues
				SET title = $2,
					body = $3,
					state = $4,
					origin = 'github',
					approval_state = $5,
					closed_at = $6
				WHERE id = $1
				RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at`,
			existingIssue.ID,
			title,
			nullableString(input.Body),
			state,
			approvalState,
			input.ClosedAt,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to update github issue: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		created = true
		var nextIssueNumber int64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COALESCE(MAX(issue_number), 0) + 1 FROM project_issues WHERE project_id = $1`,
			projectID,
		).Scan(&nextIssueNumber); err != nil {
			return nil, false, fmt.Errorf("failed to allocate issue number: %w", err)
		}

		issue, err = scanProjectIssue(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_issues (
				org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, closed_at
			) VALUES ($1,$2,$3,$4,$5,$6,'github',NULL,$7,$8)
			RETURNING id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at`,
			workspaceID,
			projectID,
			nextIssueNumber,
			title,
			nullableString(input.Body),
			state,
			approvalState,
			input.ClosedAt,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to insert github issue: %w", err)
		}
	default:
		return nil, false, fmt.Errorf("failed to load github issue mapping: %w", err)
	}

	link, err := scanProjectIssueGitHubLink(tx.QueryRowContext(
		ctx,
		`UPDATE project_issue_github_links
			SET repository_full_name = $2,
				github_number = $3,
				github_url = $4,
				github_state = $5,
				last_synced_at = NOW()
			WHERE issue_id = $1
			RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
		issue.ID,
		repo,
		input.GitHubNumber,
		nullableString(input.GitHubURL),
		state,
	))
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("failed to update issue github link: %w", err)
		}
		link, err = scanProjectIssueGitHubLink(tx.QueryRowContext(
			ctx,
			`INSERT INTO project_issue_github_links (
				org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
			) VALUES ($1,$2,$3,$4,$5,$6,NOW())
			ON CONFLICT (org_id, repository_full_name, github_number)
			DO UPDATE SET
				issue_id = EXCLUDED.issue_id,
				github_url = EXCLUDED.github_url,
				github_state = EXCLUDED.github_state,
				last_synced_at = NOW()
			RETURNING id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at`,
			workspaceID,
			issue.ID,
			repo,
			input.GitHubNumber,
			nullableString(input.GitHubURL),
			state,
		))
		if err != nil {
			return nil, false, fmt.Errorf("failed to insert issue github link: %w", err)
		}
	}

	if link.OrgID != workspaceID {
		return nil, false, ErrForbidden
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("failed to commit github issue upsert: %w", err)
	}
	return &issue, created, nil
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

	query := `SELECT i.id, i.org_id, i.project_id, i.issue_number, i.title, i.body, i.state, i.origin, i.document_path, i.approval_state, i.created_at, i.updated_at, i.closed_at
		FROM project_issues i
		LEFT JOIN project_issue_github_links l ON l.issue_id = i.id
		WHERE i.project_id = $1`
	args := []any{projectID}
	argPos := 2

	if filter.State != nil && strings.TrimSpace(*filter.State) != "" {
		state := normalizeIssueState(*filter.State)
		if !isValidIssueState(state) {
			return nil, fmt.Errorf("invalid state filter")
		}
		query += fmt.Sprintf(" AND i.state = $%d", argPos)
		args = append(args, state)
		argPos++
	}
	if filter.Origin != nil && strings.TrimSpace(*filter.Origin) != "" {
		origin := strings.TrimSpace(strings.ToLower(*filter.Origin))
		if origin != "local" && origin != "github" {
			return nil, fmt.Errorf("invalid origin filter")
		}
		query += fmt.Sprintf(" AND i.origin = $%d", argPos)
		args = append(args, origin)
		argPos++
	}
	if filter.Kind != nil && strings.TrimSpace(*filter.Kind) != "" {
		kind := strings.TrimSpace(strings.ToLower(*filter.Kind))
		switch kind {
		case "issue":
			query += ` AND (l.github_url IS NULL OR l.github_url NOT ILIKE '%/pull/%')`
		case "pull_request":
			query += ` AND l.github_url ILIKE '%/pull/%'`
		default:
			return nil, fmt.Errorf("invalid kind filter")
		}
	}

	query += fmt.Sprintf(" ORDER BY i.issue_number DESC LIMIT $%d", argPos)
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

func (s *ProjectIssueStore) ListGitHubLinksByIssueIDs(
	ctx context.Context,
	issueIDs []string,
) (map[string]ProjectIssueGitHubLink, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	normalizedIDs := make([]string, 0, len(issueIDs))
	seen := make(map[string]struct{}, len(issueIDs))
	for _, raw := range issueIDs {
		issueID := strings.TrimSpace(raw)
		if !uuidRegex.MatchString(issueID) {
			return nil, fmt.Errorf("invalid issue_id")
		}
		if _, ok := seen[issueID]; ok {
			continue
		}
		seen[issueID] = struct{}{}
		normalizedIDs = append(normalizedIDs, issueID)
	}

	links := make(map[string]ProjectIssueGitHubLink, len(normalizedIDs))
	if len(normalizedIDs) == 0 {
		return links, nil
	}

	placeholders := make([]string, len(normalizedIDs))
	args := make([]any, len(normalizedIDs))
	for i, issueID := range normalizedIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = issueID
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, repository_full_name, github_number, github_url, github_state, last_synced_at
			FROM project_issue_github_links
			WHERE issue_id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue github links: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		link, err := scanProjectIssueGitHubLink(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue github link row: %w", err)
		}
		if link.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		links[link.IssueID] = link
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue github link rows: %w", err)
	}
	return links, nil
}

func (s *ProjectIssueStore) GetIssueByID(ctx context.Context, issueID string) (*ProjectIssue, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	issue, err := scanProjectIssue(conn.QueryRowContext(
		ctx,
		`SELECT id, org_id, project_id, issue_number, title, body, state, origin, document_path, approval_state, created_at, updated_at, closed_at
			FROM project_issues
			WHERE id = $1`,
		issueID,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}
	if issue.OrgID != workspaceID {
		return nil, ErrForbidden
	}
	return &issue, nil
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

func (s *ProjectIssueStore) ListSyncCheckpoints(
	ctx context.Context,
	projectID string,
) ([]ProjectIssueSyncCheckpoint, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, project_id, repository_full_name, resource, cursor, last_synced_at
			FROM project_issue_sync_checkpoints
			WHERE project_id = $1
			ORDER BY last_synced_at DESC, repository_full_name ASC, resource ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue sync checkpoints: %w", err)
	}
	defer rows.Close()

	items := make([]ProjectIssueSyncCheckpoint, 0)
	for rows.Next() {
		record, err := scanProjectIssueSyncCheckpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue sync checkpoint row: %w", err)
		}
		if record.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue sync checkpoint rows: %w", err)
	}
	return items, nil
}

func (s *ProjectIssueStore) GetProjectIssueCounts(
	ctx context.Context,
	projectID string,
) (*ProjectIssueCounts, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var counts ProjectIssueCounts
	err = conn.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*)::int AS total,
			COUNT(*) FILTER (WHERE i.state = 'open')::int AS open_count,
			COUNT(*) FILTER (WHERE i.state = 'closed')::int AS closed_count,
			COUNT(*) FILTER (WHERE i.origin = 'github')::int AS github_origin_count,
			COUNT(*) FILTER (WHERE i.origin = 'local')::int AS local_origin_count,
			COUNT(*) FILTER (
				WHERE l.github_url IS NOT NULL AND l.github_url ILIKE '%/pull/%'
			)::int AS pull_request_count
		FROM project_issues i
		LEFT JOIN project_issue_github_links l ON l.issue_id = i.id
		WHERE i.project_id = $1`,
		projectID,
	).Scan(
		&counts.Total,
		&counts.Open,
		&counts.Closed,
		&counts.GitHubOrigin,
		&counts.LocalOrigin,
		&counts.PullRequests,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load issue counts: %w", err)
	}

	if err := ensureProjectVisible(ctx, conn, projectID); err != nil {
		return nil, err
	}

	return &counts, nil
}

func (s *ProjectIssueStore) AddParticipant(
	ctx context.Context,
	input AddProjectIssueParticipantInput,
) (*ProjectIssueParticipant, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	agentID := strings.TrimSpace(input.AgentID)
	if !uuidRegex.MatchString(agentID) {
		return nil, fmt.Errorf("invalid agent_id")
	}

	role := strings.TrimSpace(strings.ToLower(input.Role))
	if role == "" {
		role = "collaborator"
	}
	if role != "owner" && role != "collaborator" {
		return nil, fmt.Errorf("role must be owner or collaborator")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if err := ensureAgentVisible(ctx, tx, agentID); err != nil {
		return nil, err
	}

	existing, err := loadActiveIssueParticipant(ctx, tx, issueID, agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to load existing participant: %w", err)
	}
	if err == nil {
		if existing.Role == role {
			return &existing, nil
		}
		updated, err := scanProjectIssueParticipant(tx.QueryRowContext(
			ctx,
			`UPDATE project_issue_participants
				SET role = $1
			  WHERE id = $2
			  RETURNING id, org_id, issue_id, agent_id, role, joined_at, removed_at`,
			role,
			existing.ID,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to update participant role: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit participant update: %w", err)
		}
		return &updated, nil
	}

	record, err := scanProjectIssueParticipant(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_participants (
			org_id, issue_id, agent_id, role, joined_at, removed_at
		) VALUES ($1,$2,$3,$4,NOW(),NULL)
		RETURNING id, org_id, issue_id, agent_id, role, joined_at, removed_at`,
		workspaceID,
		issueID,
		agentID,
		role,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to add issue participant: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit participant add: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) RemoveParticipant(ctx context.Context, issueID, agentID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return fmt.Errorf("invalid issue_id")
	}
	agentID = strings.TrimSpace(agentID)
	if !uuidRegex.MatchString(agentID) {
		return fmt.Errorf("invalid agent_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(
		ctx,
		`UPDATE project_issue_participants
			SET removed_at = NOW()
		  WHERE org_id = $1 AND issue_id = $2 AND agent_id = $3 AND removed_at IS NULL`,
		workspaceID,
		issueID,
		agentID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove participant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect participant removal result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *ProjectIssueStore) ListParticipants(
	ctx context.Context,
	issueID string,
	includeRemoved bool,
) ([]ProjectIssueParticipant, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `SELECT id, org_id, issue_id, agent_id, role, joined_at, removed_at
		FROM project_issue_participants
		WHERE issue_id = $1`
	if !includeRemoved {
		query += ` AND removed_at IS NULL`
	}
	query += ` ORDER BY CASE WHEN role = 'owner' THEN 0 ELSE 1 END ASC, joined_at ASC`

	rows, err := conn.QueryContext(ctx, query, issueID)
	if err != nil {
		return nil, fmt.Errorf("failed to list participants: %w", err)
	}
	defer rows.Close()

	participants := make([]ProjectIssueParticipant, 0)
	for rows.Next() {
		participant, err := scanProjectIssueParticipant(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan participant row: %w", err)
		}
		if participant.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		participants = append(participants, participant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read participant rows: %w", err)
	}
	return participants, nil
}

func (s *ProjectIssueStore) CreateComment(
	ctx context.Context,
	input CreateProjectIssueCommentInput,
) (*ProjectIssueComment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID := strings.TrimSpace(input.IssueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	authorID := strings.TrimSpace(input.AuthorAgentID)
	if !uuidRegex.MatchString(authorID) {
		return nil, fmt.Errorf("invalid author_agent_id")
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		return nil, fmt.Errorf("body is required")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureIssueVisible(ctx, tx, issueID); err != nil {
		return nil, err
	}
	if err := ensureAgentVisible(ctx, tx, authorID); err != nil {
		return nil, err
	}

	record, err := scanProjectIssueComment(tx.QueryRowContext(
		ctx,
		`INSERT INTO project_issue_comments (
			org_id, issue_id, author_agent_id, body
		) VALUES ($1,$2,$3,$4)
		RETURNING id, org_id, issue_id, author_agent_id, body, created_at, updated_at`,
		workspaceID,
		issueID,
		authorID,
		body,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue comment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit issue comment create: %w", err)
	}
	return &record, nil
}

func (s *ProjectIssueStore) ListComments(
	ctx context.Context,
	issueID string,
	limit int,
	offset int,
) ([]ProjectIssueComment, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	issueID = strings.TrimSpace(issueID)
	if !uuidRegex.MatchString(issueID) {
		return nil, fmt.Errorf("invalid issue_id")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(
		ctx,
		`SELECT id, org_id, issue_id, author_agent_id, body, created_at, updated_at
			FROM project_issue_comments
			WHERE issue_id = $1
			ORDER BY created_at ASC, id ASC
			LIMIT $2 OFFSET $3`,
		issueID,
		limit,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list issue comments: %w", err)
	}
	defer rows.Close()

	comments := make([]ProjectIssueComment, 0)
	for rows.Next() {
		comment, err := scanProjectIssueComment(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan issue comment row: %w", err)
		}
		if comment.OrgID != workspaceID {
			return nil, ErrForbidden
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read issue comment rows: %w", err)
	}
	return comments, nil
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

func ensureAgentVisible(ctx context.Context, q Querier, agentID string) error {
	var visible bool
	err := q.QueryRowContext(ctx, `SELECT TRUE FROM agents WHERE id = $1`, agentID).Scan(&visible)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func loadActiveIssueParticipant(
	ctx context.Context,
	q Querier,
	issueID string,
	agentID string,
) (ProjectIssueParticipant, error) {
	return scanProjectIssueParticipant(q.QueryRowContext(
		ctx,
		`SELECT id, org_id, issue_id, agent_id, role, joined_at, removed_at
			FROM project_issue_participants
			WHERE issue_id = $1 AND agent_id = $2 AND removed_at IS NULL`,
		issueID,
		agentID,
	))
}

func scanProjectIssue(scanner interface{ Scan(...any) error }) (ProjectIssue, error) {
	var issue ProjectIssue
	var body sql.NullString
	var documentPath sql.NullString
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
		&documentPath,
		&issue.ApprovalState,
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
	if documentPath.Valid {
		issue.DocumentPath = &documentPath.String
	}
	issue.ApprovalState = normalizeIssueApprovalState(issue.ApprovalState)
	if issue.ApprovalState == "" {
		issue.ApprovalState = defaultApprovalStateForLegacyState(issue.State)
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

func scanProjectIssueParticipant(scanner interface{ Scan(...any) error }) (ProjectIssueParticipant, error) {
	var participant ProjectIssueParticipant
	var removedAt sql.NullTime

	err := scanner.Scan(
		&participant.ID,
		&participant.OrgID,
		&participant.IssueID,
		&participant.AgentID,
		&participant.Role,
		&participant.JoinedAt,
		&removedAt,
	)
	if err != nil {
		return participant, err
	}
	if removedAt.Valid {
		participant.RemovedAt = &removedAt.Time
	}
	return participant, nil
}

func scanProjectIssueComment(scanner interface{ Scan(...any) error }) (ProjectIssueComment, error) {
	var comment ProjectIssueComment
	err := scanner.Scan(
		&comment.ID,
		&comment.OrgID,
		&comment.IssueID,
		&comment.AuthorAgentID,
		&comment.Body,
		&comment.CreatedAt,
		&comment.UpdatedAt,
	)
	if err != nil {
		return comment, err
	}
	return comment, nil
}
