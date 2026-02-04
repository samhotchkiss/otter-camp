package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/models"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// Task represents a task pulled from the dispatch queue.
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

// Queue provides DB-backed dispatch queue operations.
type Queue struct {
	db *sql.DB
}

// NewQueue creates a new dispatch queue instance.
func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// Pickup selects the next queued task by priority and FIFO order, and marks it as dispatched.
// Returns nil, nil when no queued tasks are available.
func (q *Queue) Pickup(ctx context.Context) (*Task, error) {
	if q.db == nil {
		return nil, errors.New("dispatch queue requires a database connection")
	}

	tx, err := store.WithWorkspaceTx(ctx, q.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, pickupTaskSQL, models.TaskStatusQueued, models.TaskStatusDispatched)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to pick up task: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit dispatch pickup: %w", err)
	}

	return task, nil
}

const pickupTaskSQL = `
WITH next_task AS (
	SELECT id
	FROM tasks
	WHERE status = $1
	ORDER BY
		CASE priority
			WHEN 'P0' THEN 0
			WHEN 'P1' THEN 1
			WHEN 'P2' THEN 2
			WHEN 'P3' THEN 3
			ELSE 4
		END,
		created_at
	FOR UPDATE SKIP LOCKED
	LIMIT 1
)
UPDATE tasks
SET status = $2, updated_at = NOW()
FROM next_task
WHERE tasks.id = next_task.id
RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at;
`

func scanTask(scanner interface{ Scan(...any) error }) (*Task, error) {
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
		return nil, err
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

	return &task, nil
}
