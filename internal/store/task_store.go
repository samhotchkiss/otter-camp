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

// Task represents a task entity.
type Task struct {
	ID              string          `json:"id"`
	OrgID           string          `json:"org_id"`
	ProjectID       *string         `json:"project_id,omitempty"`
	Number          int32           `json:"number"`
	Title           string          `json:"title"`
	Description     *string         `json:"description,omitempty"`
	Status          string          `json:"status"`
	Priority        string          `json:"priority"`
	Context         json.RawMessage `json:"context"`
	AssignedAgentID *string         `json:"assigned_agent_id,omitempty"`
	ParentTaskID    *string         `json:"parent_task_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// TaskStore provides workspace-isolated access to tasks.
type TaskStore struct {
	db *sql.DB
}

// NewTaskStore creates a new TaskStore with the given database connection.
func NewTaskStore(db *sql.DB) *TaskStore {
	return &TaskStore{db: db}
}

// TaskFilter defines filtering options for listing tasks.
type TaskFilter struct {
	Status    string
	ProjectID *string
	AgentID   *string
}

const taskSelectColumns = "id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at"

// GetByID retrieves a task by ID within the current workspace.
// The RLS policy ensures only tasks in the current workspace are visible.
func (s *TaskStore) GetByID(ctx context.Context, id string) (*Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + taskSelectColumns + " FROM tasks WHERE id = $1"
	task, err := scanTask(conn.QueryRowContext(ctx, query, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// Double-check workspace isolation at app layer (defense in depth)
	if task.OrgID != workspaceID {
		return nil, ErrForbidden
	}

	return &task, nil
}

// GetByNumber retrieves a task by its workspace-scoped number.
func (s *TaskStore) GetByNumber(ctx context.Context, number int32) (*Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "SELECT " + taskSelectColumns + " FROM tasks WHERE org_id = $1 AND number = $2"
	task, err := scanTask(conn.QueryRowContext(ctx, query, workspaceID, number))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get task by number: %w", err)
	}

	return &task, nil
}

// List retrieves all tasks in the current workspace matching the filter.
func (s *TaskStore) List(ctx context.Context, filter TaskFilter) ([]Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query, args := buildTaskListQuery(workspaceID, filter)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading tasks: %w", err)
	}

	return tasks, nil
}

// CreateTaskInput defines the input for creating a new task.
type CreateTaskInput struct {
	ProjectID       *string
	Title           string
	Description     *string
	Status          string
	Priority        string
	Context         json.RawMessage
	AssignedAgentID *string
	ParentTaskID    *string
}

// Create creates a new task in the current workspace.
func (s *TaskStore) Create(ctx context.Context, input CreateTaskInput) (*Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO tasks (
		org_id, project_id, title, description, status, priority, context, assigned_agent_id, parent_task_id
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	RETURNING ` + taskSelectColumns

	contextBytes := normalizeContext(input.Context)

	args := []interface{}{
		workspaceID,
		nullableString(input.ProjectID),
		input.Title,
		nullableString(input.Description),
		input.Status,
		input.Priority,
		contextBytes,
		nullableString(input.AssignedAgentID),
		nullableString(input.ParentTaskID),
	}

	task, err := scanTask(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return &task, nil
}

// UpdateTaskInput defines the input for updating a task.
type UpdateTaskInput struct {
	ProjectID       *string
	Title           string
	Description     *string
	Status          string
	Priority        string
	Context         json.RawMessage
	AssignedAgentID *string
	ParentTaskID    *string
}

// Update updates a task in the current workspace.
func (s *TaskStore) Update(ctx context.Context, id string, input UpdateTaskInput) (*Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `UPDATE tasks SET
		project_id = $1, title = $2, description = $3, status = $4,
		priority = $5, context = $6, assigned_agent_id = $7, parent_task_id = $8
	WHERE id = $9 AND org_id = $10
	RETURNING ` + taskSelectColumns

	contextBytes := normalizeContext(input.Context)

	args := []interface{}{
		nullableString(input.ProjectID),
		input.Title,
		nullableString(input.Description),
		input.Status,
		input.Priority,
		contextBytes,
		nullableString(input.AssignedAgentID),
		nullableString(input.ParentTaskID),
		id,
		workspaceID, // Defense in depth: explicit workspace check
	}

	task, err := scanTask(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update task: %w", err)
	}

	return &task, nil
}

// UpdateStatus updates only the status of a task.
func (s *TaskStore) UpdateStatus(ctx context.Context, id string, status string) (*Task, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := "UPDATE tasks SET status = $1 WHERE id = $2 AND org_id = $3 RETURNING " + taskSelectColumns
	task, err := scanTask(conn.QueryRowContext(ctx, query, status, id, workspaceID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to update task status: %w", err)
	}

	return &task, nil
}

// Delete deletes a task from the current workspace.
func (s *TaskStore) Delete(ctx context.Context, id string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Defense in depth: explicit workspace check in WHERE clause
	result, err := conn.ExecContext(ctx, "DELETE FROM tasks WHERE id = $1 AND org_id = $2", id, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
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

func buildTaskListQuery(workspaceID string, filter TaskFilter) (string, []interface{}) {
	conditions := []string{"org_id = $1"}
	args := []interface{}{workspaceID}

	if filter.Status != "" {
		args = append(args, filter.Status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.ProjectID != nil {
		args = append(args, *filter.ProjectID)
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", len(args)))
	}
	if filter.AgentID != nil {
		args = append(args, *filter.AgentID)
		conditions = append(conditions, fmt.Sprintf("assigned_agent_id = $%d", len(args)))
	}

	query := "SELECT " + taskSelectColumns + " FROM tasks WHERE " +
		strings.Join(conditions, " AND ") + " ORDER BY created_at DESC"

	return query, args
}

func scanTask(scanner interface{ Scan(...any) error }) (Task, error) {
	var task Task
	var projectID sql.NullString
	var description sql.NullString
	var assignedAgentID sql.NullString
	var parentTaskID sql.NullString
	var contextBytes []byte

	err := scanner.Scan(
		&task.ID,
		&task.OrgID,
		&projectID,
		&task.Number,
		&task.Title,
		&description,
		&task.Status,
		&task.Priority,
		&contextBytes,
		&assignedAgentID,
		&parentTaskID,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		return task, err
	}

	if projectID.Valid {
		task.ProjectID = &projectID.String
	}
	if description.Valid {
		task.Description = &description.String
	}
	if assignedAgentID.Valid {
		task.AssignedAgentID = &assignedAgentID.String
	}
	if parentTaskID.Valid {
		task.ParentTaskID = &parentTaskID.String
	}

	if len(contextBytes) == 0 {
		task.Context = json.RawMessage("{}")
	} else {
		task.Context = json.RawMessage(contextBytes)
	}

	return task, nil
}

func normalizeContext(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
