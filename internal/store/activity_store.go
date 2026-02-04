package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

// Activity represents an activity log entry.
type Activity struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	TaskID    *string         `json:"task_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Action    string          `json:"action"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// ActivityStore provides workspace-isolated access to activity logs.
type ActivityStore struct {
	db *sql.DB
}

// NewActivityStore creates a new ActivityStore with the given database connection.
func NewActivityStore(db *sql.DB) *ActivityStore {
	return &ActivityStore{db: db}
}

const activitySelectColumns = "id, org_id, task_id, agent_id, action, metadata, created_at"

// ActivityFilter defines filtering options for listing activities.
type ActivityFilter struct {
	TaskID *string
	Action string
	Limit  int
	Offset int
}

const (
	defaultActivityLimit = 50
	maxActivityLimit     = 200
)

// List retrieves activity entries in the current workspace matching the filter.
func (s *ActivityStore) List(ctx context.Context, filter ActivityFilter) ([]Activity, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Apply defaults
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultActivityLimit
	}
	if limit > maxActivityLimit {
		limit = maxActivityLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query, args := buildActivityListQuery(workspaceID, filter.TaskID, filter.Action, limit, offset)
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list activities: %w", err)
	}
	defer rows.Close()

	activities := make([]Activity, 0)
	for rows.Next() {
		activity, err := scanActivity(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan activity: %w", err)
		}
		activities = append(activities, activity)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading activities: %w", err)
	}

	return activities, nil
}

// ListByTask retrieves activity entries for a specific task.
func (s *ActivityStore) ListByTask(ctx context.Context, taskID string, limit, offset int) ([]Activity, error) {
	return s.List(ctx, ActivityFilter{
		TaskID: &taskID,
		Limit:  limit,
		Offset: offset,
	})
}

// CreateActivityInput defines the input for creating a new activity entry.
type CreateActivityInput struct {
	TaskID   *string
	AgentID  *string
	Action   string
	Metadata json.RawMessage
}

// Create creates a new activity entry in the current workspace.
func (s *ActivityStore) Create(ctx context.Context, input CreateActivityInput) (*Activity, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO activity_log (
		org_id, task_id, agent_id, action, metadata
	) VALUES ($1, $2, $3, $4, $5)
	RETURNING ` + activitySelectColumns

	metadataBytes := normalizeMetadata(input.Metadata)

	args := []interface{}{
		workspaceID,
		nullableString(input.TaskID),
		nullableString(input.AgentID),
		input.Action,
		metadataBytes,
	}

	activity, err := scanActivity(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create activity: %w", err)
	}

	return &activity, nil
}

// CreateWithWorkspaceID creates a new activity entry with an explicit workspace ID.
// This is useful for webhook handlers that have the org_id in the payload.
func (s *ActivityStore) CreateWithWorkspaceID(ctx context.Context, workspaceID string, input CreateActivityInput) (*Activity, error) {
	if workspaceID == "" {
		return nil, ErrNoWorkspace
	}

	conn, err := WithWorkspaceID(ctx, s.db, workspaceID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `INSERT INTO activity_log (
		org_id, task_id, agent_id, action, metadata
	) VALUES ($1, $2, $3, $4, $5)
	RETURNING ` + activitySelectColumns

	metadataBytes := normalizeMetadata(input.Metadata)

	args := []interface{}{
		workspaceID,
		nullableString(input.TaskID),
		nullableString(input.AgentID),
		input.Action,
		metadataBytes,
	}

	activity, err := scanActivity(conn.QueryRowContext(ctx, query, args...))
	if err != nil {
		return nil, fmt.Errorf("failed to create activity: %w", err)
	}

	return &activity, nil
}

func buildActivityListQuery(workspaceID string, taskID *string, action string, limit, offset int) (string, []interface{}) {
	conditions := []string{"org_id = $1"}
	args := []interface{}{workspaceID}

	if taskID != nil {
		args = append(args, *taskID)
		conditions = append(conditions, fmt.Sprintf("task_id = $%d", len(args)))
	}
	if action != "" {
		args = append(args, action)
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(args)))
	}

	args = append(args, limit, offset)
	limitPos := len(args) - 1
	offsetPos := len(args)

	query := "SELECT " + activitySelectColumns + " FROM activity_log WHERE " +
		strings.Join(conditions, " AND ") +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", limitPos, offsetPos)

	return query, args
}

func scanActivity(scanner interface{ Scan(...any) error }) (Activity, error) {
	var activity Activity
	var taskID sql.NullString
	var agentID sql.NullString
	var metadataBytes []byte

	err := scanner.Scan(
		&activity.ID,
		&activity.OrgID,
		&taskID,
		&agentID,
		&activity.Action,
		&metadataBytes,
		&activity.CreatedAt,
	)
	if err != nil {
		return activity, err
	}

	if taskID.Valid {
		activity.TaskID = &taskID.String
	}
	if agentID.Valid {
		activity.AgentID = &agentID.String
	}

	if len(metadataBytes) == 0 {
		activity.Metadata = json.RawMessage("{}")
	} else {
		activity.Metadata = json.RawMessage(metadataBytes)
	}

	return activity, nil
}

func normalizeMetadata(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
