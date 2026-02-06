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

const (
	RepoSyncModeSync = "sync"
	RepoSyncModePush = "push"
)

const (
	RepoConflictNone          = "none"
	RepoConflictNeedsDecision = "needs_decision"
	RepoConflictResolved      = "resolved"
)

type ProjectRepoBinding struct {
	ID                 string          `json:"id"`
	OrgID              string          `json:"org_id"`
	ProjectID          string          `json:"project_id"`
	RepositoryFullName string          `json:"repository_full_name"`
	DefaultBranch      string          `json:"default_branch"`
	Enabled            bool            `json:"enabled"`
	SyncMode           string          `json:"sync_mode"`
	AutoSync           bool            `json:"auto_sync"`
	LastSyncedSHA      *string         `json:"last_synced_sha,omitempty"`
	LastSyncedAt       *time.Time      `json:"last_synced_at,omitempty"`
	ConflictState      string          `json:"conflict_state"`
	ConflictDetails    json.RawMessage `json:"conflict_details"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type ProjectRepoActiveBranch struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	BranchName    string     `json:"branch_name"`
	LastSyncedSHA *string    `json:"last_synced_sha,omitempty"`
	LastSyncedAt  *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type UpsertProjectRepoBindingInput struct {
	ProjectID          string
	RepositoryFullName string
	DefaultBranch      string
	Enabled            bool
	SyncMode           string
	AutoSync           bool
	LastSyncedSHA      *string
	LastSyncedAt       *time.Time
	ConflictState      string
	ConflictDetails    json.RawMessage
}

type ProjectRepoStore struct {
	db *sql.DB
}

func NewProjectRepoStore(db *sql.DB) *ProjectRepoStore {
	return &ProjectRepoStore{db: db}
}

const projectRepoBindingColumns = `
	id,
	org_id,
	project_id,
	repository_full_name,
	default_branch,
	enabled,
	sync_mode,
	auto_sync,
	last_synced_sha,
	last_synced_at,
	conflict_state,
	conflict_details,
	created_at,
	updated_at
`

const projectRepoBranchColumns = `
	id,
	org_id,
	project_id,
	branch_name,
	last_synced_sha,
	last_synced_at,
	created_at,
	updated_at
`

func (s *ProjectRepoStore) UpsertBinding(
	ctx context.Context,
	input UpsertProjectRepoBindingInput,
) (*ProjectRepoBinding, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	if !uuidRegex.MatchString(strings.TrimSpace(input.ProjectID)) {
		return nil, fmt.Errorf("invalid project_id")
	}

	repoName := strings.TrimSpace(input.RepositoryFullName)
	if repoName == "" {
		return nil, fmt.Errorf("repository_full_name is required")
	}

	defaultBranch := strings.TrimSpace(input.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	syncMode := strings.TrimSpace(input.SyncMode)
	if syncMode == "" {
		syncMode = RepoSyncModeSync
	}
	if syncMode != RepoSyncModeSync && syncMode != RepoSyncModePush {
		return nil, fmt.Errorf("sync_mode must be one of %q or %q", RepoSyncModeSync, RepoSyncModePush)
	}

	conflictState := strings.TrimSpace(input.ConflictState)
	if conflictState == "" {
		conflictState = RepoConflictNone
	}
	switch conflictState {
	case RepoConflictNone, RepoConflictNeedsDecision, RepoConflictResolved:
	default:
		return nil, fmt.Errorf("invalid conflict_state")
	}

	conflictDetails := input.ConflictDetails
	if len(conflictDetails) == 0 {
		conflictDetails = json.RawMessage("{}")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `
		INSERT INTO project_repo_bindings (
			org_id,
			project_id,
			repository_full_name,
			default_branch,
			enabled,
			sync_mode,
			auto_sync,
			last_synced_sha,
			last_synced_at,
			conflict_state,
			conflict_details
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11
		)
		ON CONFLICT (project_id)
		DO UPDATE SET
			repository_full_name = EXCLUDED.repository_full_name,
			default_branch = EXCLUDED.default_branch,
			enabled = EXCLUDED.enabled,
			sync_mode = EXCLUDED.sync_mode,
			auto_sync = EXCLUDED.auto_sync,
			last_synced_sha = EXCLUDED.last_synced_sha,
			last_synced_at = EXCLUDED.last_synced_at,
			conflict_state = EXCLUDED.conflict_state,
			conflict_details = EXCLUDED.conflict_details
		RETURNING` + projectRepoBindingColumns

	binding, err := scanProjectRepoBinding(conn.QueryRowContext(
		ctx,
		query,
		workspaceID,
		input.ProjectID,
		repoName,
		defaultBranch,
		input.Enabled,
		syncMode,
		input.AutoSync,
		nullableString(input.LastSyncedSHA),
		input.LastSyncedAt,
		conflictState,
		conflictDetails,
	))
	if err != nil {
		return nil, fmt.Errorf("failed to upsert project repo binding: %w", err)
	}

	return &binding, nil
}

func (s *ProjectRepoStore) GetBinding(
	ctx context.Context,
	projectID string,
) (*ProjectRepoBinding, error) {
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

	query := `SELECT` + projectRepoBindingColumns + ` FROM project_repo_bindings WHERE project_id = $1`
	binding, err := scanProjectRepoBinding(conn.QueryRowContext(ctx, query, projectID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get project repo binding: %w", err)
	}

	if binding.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	return &binding, nil
}

func (s *ProjectRepoStore) SetActiveBranches(
	ctx context.Context,
	projectID string,
	branches []string,
) ([]ProjectRepoActiveBranch, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	tx, err := WithWorkspaceTx(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM project_repo_active_branches WHERE project_id = $1`,
		projectID,
	); err != nil {
		return nil, fmt.Errorf("failed to clear active branches: %w", err)
	}

	seen := make(map[string]struct{}, len(branches))
	insertQuery := `
		INSERT INTO project_repo_active_branches (
			org_id,
			project_id,
			branch_name
		) VALUES ($1,$2,$3)
		RETURNING` + projectRepoBranchColumns

	out := make([]ProjectRepoActiveBranch, 0, len(branches))
	for _, branch := range branches {
		name := strings.TrimSpace(branch)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		record, err := scanProjectRepoActiveBranch(tx.QueryRowContext(
			ctx,
			insertQuery,
			workspaceID,
			projectID,
			name,
		))
		if err != nil {
			return nil, fmt.Errorf("failed to insert active branch %q: %w", name, err)
		}
		out = append(out, record)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to save active branches: %w", err)
	}

	return out, nil
}

func (s *ProjectRepoStore) ListActiveBranches(
	ctx context.Context,
	projectID string,
) ([]ProjectRepoActiveBranch, error) {
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
		`SELECT`+projectRepoBranchColumns+` FROM project_repo_active_branches WHERE project_id = $1 ORDER BY branch_name ASC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list active branches: %w", err)
	}
	defer rows.Close()

	out := make([]ProjectRepoActiveBranch, 0)
	for rows.Next() {
		branch, err := scanProjectRepoActiveBranch(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan active branch: %w", err)
		}

		if branch.OrgID != workspaceID {
			return nil, ErrForbidden
		}

		out = append(out, branch)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read active branches: %w", err)
	}

	return out, nil
}

func scanProjectRepoBinding(scanner interface{ Scan(...any) error }) (ProjectRepoBinding, error) {
	var binding ProjectRepoBinding
	var lastSyncedSHA sql.NullString
	var lastSyncedAt sql.NullTime
	if err := scanner.Scan(
		&binding.ID,
		&binding.OrgID,
		&binding.ProjectID,
		&binding.RepositoryFullName,
		&binding.DefaultBranch,
		&binding.Enabled,
		&binding.SyncMode,
		&binding.AutoSync,
		&lastSyncedSHA,
		&lastSyncedAt,
		&binding.ConflictState,
		&binding.ConflictDetails,
		&binding.CreatedAt,
		&binding.UpdatedAt,
	); err != nil {
		return binding, err
	}

	if lastSyncedSHA.Valid {
		binding.LastSyncedSHA = &lastSyncedSHA.String
	}

	if lastSyncedAt.Valid {
		binding.LastSyncedAt = &lastSyncedAt.Time
	}

	if len(binding.ConflictDetails) == 0 {
		binding.ConflictDetails = json.RawMessage("{}")
	}

	return binding, nil
}

func scanProjectRepoActiveBranch(scanner interface{ Scan(...any) error }) (ProjectRepoActiveBranch, error) {
	var branch ProjectRepoActiveBranch
	var lastSyncedSHA sql.NullString
	var lastSyncedAt sql.NullTime
	if err := scanner.Scan(
		&branch.ID,
		&branch.OrgID,
		&branch.ProjectID,
		&branch.BranchName,
		&lastSyncedSHA,
		&lastSyncedAt,
		&branch.CreatedAt,
		&branch.UpdatedAt,
	); err != nil {
		return branch, err
	}

	if lastSyncedSHA.Valid {
		branch.LastSyncedSHA = &lastSyncedSHA.String
	}
	if lastSyncedAt.Valid {
		branch.LastSyncedAt = &lastSyncedAt.Time
	}
	return branch, nil
}
