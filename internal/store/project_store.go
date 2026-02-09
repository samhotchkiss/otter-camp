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

// Project represents a project entity.
type Project struct {
	ID                string          `json:"id"`
	OrgID             string          `json:"org_id"`
	Name              string          `json:"name"`
	Description       *string         `json:"description,omitempty"`
	Status            string          `json:"status"`
	RepoURL           *string         `json:"repo_url,omitempty"`
	RequireHumanReview bool           `json:"require_human_review"`
	Labels            []Label         `json:"labels,omitempty"`
	PrimaryAgentID    *string         `json:"primary_agent_id,omitempty"`
	LocalRepoPath     *string         `json:"local_repo_path,omitempty"`
	WorkflowEnabled   bool            `json:"workflow_enabled"`
	WorkflowSchedule  json.RawMessage `json:"workflow_schedule,omitempty"`
	WorkflowTemplate  json.RawMessage `json:"workflow_template,omitempty"`
	WorkflowAgentID   *string         `json:"workflow_agent_id,omitempty"`
	WorkflowLastRunAt *time.Time      `json:"workflow_last_run_at,omitempty"`
	WorkflowNextRunAt *time.Time      `json:"workflow_next_run_at,omitempty"`
	WorkflowRunCount  int             `json:"workflow_run_count"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// ProjectStore provides workspace-isolated access to projects.
type ProjectStore struct {
	db *sql.DB
}

// NewProjectStore creates a new ProjectStore with the given database connection.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{db: db}
}

const projectSelectColumns = "id, org_id, name, description, status, repo_url, require_human_review, local_repo_path, workflow_enabled, workflow_schedule, workflow_template, workflow_agent_id, workflow_last_run_at, workflow_next_run_at, workflow_run_count, created_at, updated_at"

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
	labels, err := NewLabelStore(s.db).ListForProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list labels for project: %w", err)
	}
	project.Labels = labels

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
	labels, err := NewLabelStore(s.db).ListForProject(ctx, project.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list labels for project: %w", err)
	}
	project.Labels = labels

	return &project, nil
}

// List retrieves all projects in the current workspace.
func (s *ProjectStore) List(ctx context.Context) ([]Project, error) {
	return s.ListWithLabels(ctx, nil)
}

// ListWithLabels retrieves all projects in the current workspace, filtered by
// the provided label IDs with AND semantics when one or more filters are set.
func (s *ProjectStore) ListWithLabels(ctx context.Context, labelIDs []string) ([]Project, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}
	normalizedLabelIDs := normalizeIDList(labelIDs)
	for _, labelID := range normalizedLabelIDs {
		if !uuidRegex.MatchString(labelID) {
			return nil, fmt.Errorf("invalid label filter")
		}
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	queryBuilder := strings.Builder{}
	queryBuilder.WriteString("SELECT ")
	queryBuilder.WriteString(projectSelectColumns)
	queryBuilder.WriteString(" FROM projects p WHERE p.org_id = $1")
	args := []any{workspaceID}
	argPos := 2
	for _, labelID := range normalizedLabelIDs {
		queryBuilder.WriteString(fmt.Sprintf(`
			AND EXISTS (
				SELECT 1
				FROM project_labels pl
				INNER JOIN labels l ON l.id = pl.label_id
				WHERE pl.project_id = p.id
				  AND pl.label_id = $%d
				  AND l.org_id = $1
			)`, argPos))
		args = append(args, labelID)
		argPos++
	}
	queryBuilder.WriteString(" ORDER BY p.created_at DESC")

	rows, err := conn.QueryContext(ctx, queryBuilder.String(), args...)
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
	if err := s.attachLabels(ctx, projects); err != nil {
		return nil, err
	}

	return projects, nil
}

// CreateProjectInput defines the input for creating a new project.
type CreateProjectInput struct {
	Name              string
	Description       *string
	Status            string
	RepoURL           *string
	WorkflowEnabled   bool
	WorkflowSchedule  json.RawMessage
	WorkflowTemplate  json.RawMessage
	WorkflowAgentID   *string
	WorkflowLastRunAt *time.Time
	WorkflowNextRunAt *time.Time
	WorkflowRunCount  int
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

	workflowSchedule, err := normalizeProjectWorkflowJSON(input.WorkflowSchedule, "workflow_schedule")
	if err != nil {
		return nil, err
	}
	workflowTemplate, err := normalizeProjectWorkflowJSON(input.WorkflowTemplate, "workflow_template")
	if err != nil {
		return nil, err
	}
	workflowAgentID, err := normalizeProjectWorkflowAgentID(input.WorkflowAgentID)
	if err != nil {
		return nil, err
	}
	if input.WorkflowRunCount < 0 {
		return nil, fmt.Errorf("workflow_run_count must be non-negative")
	}

	query := `INSERT INTO projects (
		org_id, name, description, status, repo_url,
		workflow_enabled, workflow_schedule, workflow_template, workflow_agent_id,
		workflow_last_run_at, workflow_next_run_at, workflow_run_count
	) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12)
	RETURNING ` + projectSelectColumns

	args := []interface{}{
		workspaceID,
		input.Name,
		nullableString(input.Description),
		input.Status,
		nullableString(input.RepoURL),
		input.WorkflowEnabled,
		nullableJSON(workflowSchedule),
		nullableJSON(workflowTemplate),
		nullableString(workflowAgentID),
		input.WorkflowLastRunAt,
		input.WorkflowNextRunAt,
		input.WorkflowRunCount,
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
	Name              string
	Description       *string
	Status            string
	RepoURL           *string
	RequireHumanReview bool
	WorkflowEnabled   bool
	WorkflowSchedule  json.RawMessage
	WorkflowTemplate  json.RawMessage
	WorkflowAgentID   *string
	WorkflowLastRunAt *time.Time
	WorkflowNextRunAt *time.Time
	WorkflowRunCount  int
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

	workflowSchedule, err := normalizeProjectWorkflowJSON(input.WorkflowSchedule, "workflow_schedule")
	if err != nil {
		return nil, err
	}
	workflowTemplate, err := normalizeProjectWorkflowJSON(input.WorkflowTemplate, "workflow_template")
	if err != nil {
		return nil, err
	}
	workflowAgentID, err := normalizeProjectWorkflowAgentID(input.WorkflowAgentID)
	if err != nil {
		return nil, err
	}
	if input.WorkflowRunCount < 0 {
		return nil, fmt.Errorf("workflow_run_count must be non-negative")
	}

	query := `UPDATE projects SET
		name = $1, description = $2, status = $3, repo_url = $4,
		require_human_review = $5, workflow_enabled = $6, workflow_schedule = $7::jsonb, workflow_template = $8::jsonb,
		workflow_agent_id = $9, workflow_last_run_at = $10, workflow_next_run_at = $11, workflow_run_count = $12
	WHERE id = $13 AND org_id = $14
	RETURNING ` + projectSelectColumns

	args := []interface{}{
		input.Name,
		nullableString(input.Description),
		input.Status,
		nullableString(input.RepoURL),
		input.RequireHumanReview,
		input.WorkflowEnabled,
		nullableJSON(workflowSchedule),
		nullableJSON(workflowTemplate),
		nullableString(workflowAgentID),
		input.WorkflowLastRunAt,
		input.WorkflowNextRunAt,
		input.WorkflowRunCount,
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
	var workflowSchedule []byte
	var workflowTemplate []byte
	var workflowAgentID sql.NullString
	var workflowLastRunAt sql.NullTime
	var workflowNextRunAt sql.NullTime

	err := scanner.Scan(
		&project.ID,
		&project.OrgID,
		&project.Name,
		&description,
		&project.Status,
		&repoURL,
		&project.RequireHumanReview,
		&localRepoPath,
		&project.WorkflowEnabled,
		&workflowSchedule,
		&workflowTemplate,
		&workflowAgentID,
		&workflowLastRunAt,
		&workflowNextRunAt,
		&project.WorkflowRunCount,
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
	if len(workflowSchedule) > 0 {
		project.WorkflowSchedule = append(json.RawMessage(nil), workflowSchedule...)
	}
	if len(workflowTemplate) > 0 {
		project.WorkflowTemplate = append(json.RawMessage(nil), workflowTemplate...)
	}
	if workflowAgentID.Valid {
		project.WorkflowAgentID = &workflowAgentID.String
	}
	if workflowLastRunAt.Valid {
		t := workflowLastRunAt.Time
		project.WorkflowLastRunAt = &t
	}
	if workflowNextRunAt.Valid {
		t := workflowNextRunAt.Time
		project.WorkflowNextRunAt = &t
	}

	return project, nil
}

func normalizeProjectWorkflowJSON(value json.RawMessage, fieldName string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" {
		return nil, nil
	}
	raw := json.RawMessage(trimmed)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s must be valid JSON", fieldName)
	}
	return raw, nil
}

func normalizeProjectWorkflowAgentID(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	if !uuidRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("workflow_agent_id must be a UUID")
	}
	return &trimmed, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func (s *ProjectStore) attachLabels(ctx context.Context, projects []Project) error {
	if len(projects) == 0 {
		return nil
	}
	projectIDs := make([]string, 0, len(projects))
	for _, project := range projects {
		projectIDs = append(projectIDs, project.ID)
	}
	labelMap, err := NewLabelStore(s.db).MapForProjects(ctx, projectIDs)
	if err != nil {
		return fmt.Errorf("failed to map labels for projects: %w", err)
	}
	for idx := range projects {
		projects[idx].Labels = labelMap[projects[idx].ID]
	}
	return nil
}
