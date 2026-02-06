package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

// Project represents a project entity.
type Project struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	Name          string    `json:"name"`
	Description   *string   `json:"description,omitempty"`
	Status        string    `json:"status"`
	RepoURL       *string   `json:"repo_url,omitempty"`
	LocalRepoPath *string   `json:"local_repo_path,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProjectStore provides workspace-isolated access to projects.
type ProjectStore struct {
	db *sql.DB
}

// NewProjectStore creates a new ProjectStore with the given database connection.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{db: db}
}

const projectSelectColumns = "id, org_id, name, description, status, repo_url, local_repo_path, created_at, updated_at"

// GetByID retrieves a project by ID within the current workspace.
func (s *ProjectStore) GetByID(ctx context.Context, id string) (*Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + projectSelectColumns + " FROM projects WHERE id = $1"
	project, err := scanProject(conn.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get project: %w", err)
	}

	// Defense in depth
	if project.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	return &project, nil
}

// GetByName retrieves a project by name within the current workspace.
func (s *ProjectStore) GetByName(ctx context.Context, name string) (*Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + projectSelectColumns + " FROM projects WHERE org_id = $1 AND name = $2"
	project, err := scanProject(conn.QueryRowContext(ctx, query, workspaceID, name))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get project by name: %w", err)
	}

	return &project, nil
}

// List retrieves all projects in the current workspace.
func (s *ProjectStore) List(ctx context.Context) ([]Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + projectSelectColumns + " FROM projects WHERE org_id = $1 ORDER BY created_at DESC"
	rows, err := conn.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		projects = append(projects, project)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading projects: %w", err)
	}

	return projects, nil
}

// CreateProjectInput defines the input for creating a new project.
type CreateProjectInput struct {
	Name        string
	Description *string
	Status      string
	RepoURL     *string
}

// Create creates a new project in the current workspace.
func (s *ProjectStore) Create(ctx context.Context, input CreateProjectInput) (*Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO projects (
		org_id, name, description, status, repo_url
	) VALUES ($1, $2, $3, $4, $5)
	RETURNING ` + projectSelectColumns

	args := []interface{}{
		workspaceID,
		input.Name,
		nullableString(input.Description),
		input.Status,
		nullableString(input.RepoURL),
	}

	project, err := scanProject(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create project: %w", err)
	}

	if err := s.InitProjectRepo(ctx, project.ID); err != nil {
		_ = s.Delete(ctx, project.ID)
		return nil, fmt.Errorf("failed to initialize project repo: %w", err)
	}

	if repoPath, err := s.GetRepoPath(ctx, project.ID); err == nil {
		project.LocalRepoPath = &repoPath
	}

	return &project, nil
}

// UpdateProjectInput defines the input for updating a project.
type UpdateProjectInput struct {
	Name        string
	Description *string
	Status      string
	RepoURL     *string
}

// Update updates a project in the current workspace.
func (s *ProjectStore) Update(ctx context.Context, id string, input UpdateProjectInput) (*Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `UPDATE projects SET
		name = $1, description = $2, status = $3, repo_url = $4
	WHERE id = $5 AND org_id = $6
	RETURNING ` + projectSelectColumns

	args := []interface{}{
		input.Name,
		nullableString(input.Description),
		input.Status,
		nullableString(input.RepoURL),
		id,
		workspaceID,
	}

	project, err := scanProject(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update project: %w", err)
	}

	return &project, nil
}

// Delete deletes a project from the current workspace.
func (s *ProjectStore) Delete(ctx context.Context, id string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	result, err := conn.ExecContext(ctx, "DELETE FROM projects WHERE id = $1 AND org_id = $2", id, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete project: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check delete result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

func scanProject(scanner interface{ Scan(...any) error }) (Project, error) {
	var project Project
	var description sql.NullString
	var repoURL sql.NullString
	var localRepoPath sql.NullString

	err := scanner.Scan(
		&project.ID,
		&project.OrgID,
		&project.Name,
		&description,
		&project.Status,
		&repoURL,
		&localRepoPath,
		&project.CreatedAt,
		&project.UpdatedAt,
	)
	if err != nil {
		return project, err
	}

	if description.Valid {
		project.Description = &description.String
	}
	if repoURL.Valid {
		project.RepoURL = &repoURL.String
	}
	if localRepoPath.Valid {
		project.LocalRepoPath = &localRepoPath.String
	}

	return project, nil
}
