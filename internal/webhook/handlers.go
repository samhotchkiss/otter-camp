// Package webhook provides handlers for OpenClaw webhook events.
package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/models"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

// Event types supported by the handler.
const (
	EventTaskStarted   = "task.started"
	EventTaskCompleted = "task.completed"
	EventTaskFailed    = "task.failed"
	EventTaskUpdated   = "task.updated"
	EventTaskProgress  = "task.progress"
	EventAgentStatus   = "agent.status"
)

// StatusEvent represents an incoming OpenClaw status event.
type StatusEvent struct {
	Event   string           `json:"event"`
	OrgID   string           `json:"org_id"`
	TaskID  string           `json:"task_id,omitempty"`
	AgentID string           `json:"agent_id,omitempty"`
	Task    *TaskPayload     `json:"task,omitempty"`
	Agent   *AgentPayload    `json:"agent,omitempty"`
	Percent *int             `json:"percent,omitempty"`
	Message string           `json:"message,omitempty"`
	RawBody json.RawMessage  `json:"-"`
}

// TaskPayload contains task details from the webhook.
type TaskPayload struct {
	ID             string `json:"id"`
	Status         string `json:"status,omitempty"`
	PreviousStatus string `json:"previous_status,omitempty"`
	Percent        *int   `json:"percent,omitempty"`
	Message        string `json:"message,omitempty"`
}

// AgentPayload contains agent details from the webhook.
type AgentPayload struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
}

// StatusHandler processes OpenClaw status events.
type StatusHandler struct {
	db  *sql.DB
	hub *ws.Hub
}

var validTaskTransitions = map[string]map[string]struct{}{
	models.TaskStatusQueued: {
		models.TaskStatusDispatched: {},
		models.TaskStatusInProgress: {}, // Allow direct claim when dispatch state is skipped.
		models.TaskStatusCancelled:  {},
	},
	models.TaskStatusDispatched: {
		models.TaskStatusInProgress: {},
		models.TaskStatusQueued:     {},
		models.TaskStatusCancelled:  {},
	},
	models.TaskStatusInProgress: {
		models.TaskStatusDone:       {},
		models.TaskStatusReview:     {},
		models.TaskStatusBlocked:    {},
		models.TaskStatusQueued:     {},
		models.TaskStatusCancelled:  {},
	},
	models.TaskStatusBlocked: {
		models.TaskStatusQueued:    {},
		models.TaskStatusInProgress: {},
		models.TaskStatusCancelled:  {},
	},
	models.TaskStatusReview: {
		models.TaskStatusDone:       {},
		models.TaskStatusInProgress: {},
		models.TaskStatusCancelled:  {},
	},
	models.TaskStatusDone: {
		models.TaskStatusQueued:    {},
		models.TaskStatusCancelled: {},
	},
	models.TaskStatusCancelled: {
		models.TaskStatusQueued: {},
	},
}

var errInvalidTaskTransition = errors.New("invalid task status transition")

// NewStatusHandler creates a new StatusHandler.
func NewStatusHandler(db *sql.DB, hub *ws.Hub) *StatusHandler {
	return &StatusHandler{
		db:  db,
		hub: hub,
	}
}

// HandleEvent processes a status event, updating records and broadcasting.
func (h *StatusHandler) HandleEvent(ctx context.Context, event StatusEvent) error {
	switch event.Event {
	case EventTaskStarted:
		return h.handleTaskStarted(ctx, event)
	case EventTaskCompleted:
		return h.handleTaskCompleted(ctx, event)
	case EventTaskFailed:
		return h.handleTaskFailed(ctx, event)
	case EventTaskUpdated, EventTaskProgress:
		return h.handleTaskUpdated(ctx, event)
	case EventAgentStatus:
		return h.handleAgentStatus(ctx, event)
	default:
		return fmt.Errorf("unsupported event type: %s", event.Event)
	}
}

// handleTaskStarted processes task.started events.
func (h *StatusHandler) handleTaskStarted(ctx context.Context, event StatusEvent) error {
	taskID := h.resolveTaskID(event)
	if taskID == "" {
		return errors.New("missing task ID")
	}

	task, prevStatus, err := h.updateTaskStatus(ctx, event.OrgID, taskID, models.TaskStatusInProgress)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	if err := h.logActivity(ctx, event); err != nil {
		// Log error but don't fail the request
		fmt.Printf("failed to log activity: %v\n", err)
	}

	h.broadcastTaskStatusChanged(task, prevStatus)
	return nil
}

// handleTaskCompleted processes task.completed events.
func (h *StatusHandler) handleTaskCompleted(ctx context.Context, event StatusEvent) error {
	taskID := h.resolveTaskID(event)
	if taskID == "" {
		return errors.New("missing task ID")
	}

	task, prevStatus, err := h.updateTaskStatus(ctx, event.OrgID, taskID, models.TaskStatusDone)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	if err := h.logActivity(ctx, event); err != nil {
		fmt.Printf("failed to log activity: %v\n", err)
	}

	h.broadcastTaskStatusChanged(task, prevStatus)
	return nil
}

// handleTaskFailed processes task.failed events.
func (h *StatusHandler) handleTaskFailed(ctx context.Context, event StatusEvent) error {
	taskID := h.resolveTaskID(event)
	if taskID == "" {
		return errors.New("missing task ID")
	}

	task, prevStatus, err := h.updateTaskStatus(ctx, event.OrgID, taskID, models.TaskStatusBlocked)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}

	if err := h.logActivity(ctx, event); err != nil {
		fmt.Printf("failed to log activity: %v\n", err)
	}

	h.broadcastTaskStatusChanged(task, prevStatus)
	return nil
}

// handleAgentStatus processes agent.status events.
func (h *StatusHandler) handleAgentStatus(ctx context.Context, event StatusEvent) error {
	agentID := h.resolveAgentID(event)
	if agentID == "" {
		return errors.New("missing agent ID")
	}

	newStatus := ""
	if event.Agent != nil {
		newStatus = event.Agent.Status
	}
	if newStatus == "" {
		newStatus = models.AgentStatusActive
	}

	agent, err := h.updateAgentStatus(ctx, event.OrgID, agentID, newStatus)
	if err != nil {
		return fmt.Errorf("failed to update agent status: %w", err)
	}

	if err := h.logActivity(ctx, event); err != nil {
		fmt.Printf("failed to log activity: %v\n", err)
	}

	h.broadcastAgentStatusChanged(event.OrgID, agent)
	return nil
}

func (h *StatusHandler) handleTaskUpdated(ctx context.Context, event StatusEvent) error {
	taskID := h.resolveTaskID(event)
	if taskID == "" {
		return errors.New("missing task ID")
	}

	var (
		updatedTask *store.Task
		prevStatus  string
	)

	status := ""
	if event.Task != nil {
		status = strings.TrimSpace(event.Task.Status)
	}

	if status != "" {
		task, prev, err := h.updateTaskStatus(ctx, event.OrgID, taskID, status)
		if err != nil {
			return fmt.Errorf("failed to update task status: %w", err)
		}
		updatedTask = task
		prevStatus = prev
	}

	if percent, ok := h.extractProgressPercent(event); ok {
		task, err := h.updateTaskProgress(ctx, event.OrgID, taskID, percent)
		if err != nil {
			return fmt.Errorf("failed to update task progress: %w", err)
		}
		updatedTask = task
	}

	if err := h.logActivity(ctx, event); err != nil {
		fmt.Printf("failed to log activity: %v\n", err)
	}

	if updatedTask == nil {
		return nil
	}

	if status != "" {
		h.broadcastTaskStatusChanged(updatedTask, prevStatus)
		return nil
	}

	h.broadcastTaskUpdated(updatedTask)
	return nil
}

// resolveTaskID extracts the task ID from the event.
func (h *StatusHandler) resolveTaskID(event StatusEvent) string {
	if event.Task != nil && event.Task.ID != "" {
		return strings.TrimSpace(event.Task.ID)
	}
	return strings.TrimSpace(event.TaskID)
}

// resolveAgentID extracts the agent ID from the event.
func (h *StatusHandler) resolveAgentID(event StatusEvent) string {
	if event.Agent != nil && event.Agent.ID != "" {
		return strings.TrimSpace(event.Agent.ID)
	}
	return strings.TrimSpace(event.AgentID)
}

// updateTaskStatus updates a task's status in the database.
func (h *StatusHandler) updateTaskStatus(ctx context.Context, orgID, taskID, status string) (*store.Task, string, error) {
	conn, err := store.WithWorkspaceID(ctx, h.db, orgID)
	if err != nil {
		return nil, "", err
	}
	defer conn.Close()

	var currentStatus string
	if err := conn.QueryRowContext(ctx, "SELECT status FROM tasks WHERE id = $1 AND org_id = $2", taskID, orgID).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", store.ErrNotFound
		}
		return nil, "", err
	}

	if !isValidTaskTransition(currentStatus, status) {
		return nil, currentStatus, fmt.Errorf("%w: %s -> %s", errInvalidTaskTransition, currentStatus, status)
	}

	query := `UPDATE tasks SET status = $1, updated_at = NOW() 
		WHERE id = $2 AND org_id = $3 
		RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at`

	var task store.Task
	var projectID sql.NullString
	var description sql.NullString
	var assignedAgentID sql.NullString
	var parentTaskID sql.NullString
	var contextBytes []byte

	err = conn.QueryRowContext(ctx, query, status, taskID, orgID).Scan(
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, currentStatus, store.ErrNotFound
		}
		return nil, currentStatus, err
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
	if len(contextBytes) > 0 {
		task.Context = json.RawMessage(contextBytes)
	} else {
		task.Context = json.RawMessage("{}")
	}

	return &task, currentStatus, nil
}

func (h *StatusHandler) updateTaskProgress(ctx context.Context, orgID, taskID string, percent int) (*store.Task, error) {
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("progress percent out of range: %d", percent)
	}

	conn, err := store.WithWorkspaceID(ctx, h.db, orgID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var contextBytes []byte
	if err := conn.QueryRowContext(ctx, "SELECT context FROM tasks WHERE id = $1 AND org_id = $2", taskID, orgID).Scan(&contextBytes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	mergedContext, err := mergeProgressContext(contextBytes, percent)
	if err != nil {
		return nil, err
	}

	query := `UPDATE tasks SET context = $1, updated_at = NOW()
		WHERE id = $2 AND org_id = $3
		RETURNING id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at`

	var task store.Task
	var projectID sql.NullString
	var description sql.NullString
	var assignedAgentID sql.NullString
	var parentTaskID sql.NullString
	var updatedContextBytes []byte

	err = conn.QueryRowContext(ctx, query, mergedContext, taskID, orgID).Scan(
		&task.ID,
		&task.OrgID,
		&projectID,
		&task.Number,
		&task.Title,
		&description,
		&task.Status,
		&task.Priority,
		&updatedContextBytes,
		&assignedAgentID,
		&parentTaskID,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
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
	if len(updatedContextBytes) > 0 {
		task.Context = json.RawMessage(updatedContextBytes)
	} else {
		task.Context = json.RawMessage("{}")
	}

	return &task, nil
}

// updateAgentStatus updates an agent's status in the database.
func (h *StatusHandler) updateAgentStatus(ctx context.Context, orgID, agentID, status string) (*store.Agent, error) {
	conn, err := store.WithWorkspaceID(ctx, h.db, orgID)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	query := `UPDATE agents SET status = $1, updated_at = NOW() 
		WHERE id = $2 AND org_id = $3 
		RETURNING id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at`

	var agent store.Agent
	var avatarURL sql.NullString
	var webhookURL sql.NullString
	var sessionPattern sql.NullString

	err = conn.QueryRowContext(ctx, query, status, agentID, orgID).Scan(
		&agent.ID,
		&agent.OrgID,
		&agent.Slug,
		&agent.DisplayName,
		&avatarURL,
		&webhookURL,
		&agent.Status,
		&sessionPattern,
		&agent.CreatedAt,
		&agent.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	if avatarURL.Valid {
		agent.AvatarURL = &avatarURL.String
	}
	if webhookURL.Valid {
		agent.WebhookURL = &webhookURL.String
	}
	if sessionPattern.Valid {
		agent.SessionPattern = &sessionPattern.String
	}

	return &agent, nil
}

// logActivity logs the event to the activity feed.
func (h *StatusHandler) logActivity(ctx context.Context, event StatusEvent) error {
	var taskArg interface{}
	if id := h.resolveTaskID(event); id != "" {
		taskArg = id
	}
	var agentArg interface{}
	if id := h.resolveAgentID(event); id != "" {
		agentArg = id
	}

	metadata := event.RawBody
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}

	_, err := h.db.ExecContext(
		ctx,
		"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata) VALUES ($1, $2, $3, $4, $5)",
		event.OrgID,
		taskArg,
		agentArg,
		event.Event,
		metadata,
	)
	return err
}

// WebSocket message types for status events.
const (
	MessageTaskStatusChanged  ws.MessageType = "TaskStatusChanged"
	MessageAgentStatusChanged ws.MessageType = "AgentStatusChanged"
)

// taskStatusBroadcast is the WebSocket payload for task status changes.
type taskStatusBroadcast struct {
	Type           ws.MessageType `json:"type"`
	Task           *store.Task    `json:"task"`
	PreviousStatus string         `json:"previous_status,omitempty"`
	Timestamp      time.Time      `json:"timestamp"`
}

type taskUpdatedBroadcast struct {
	Type      ws.MessageType `json:"type"`
	Task      *store.Task    `json:"task"`
	Timestamp time.Time      `json:"timestamp"`
}

// agentStatusBroadcast is the WebSocket payload for agent status changes.
type agentStatusBroadcast struct {
	Type      ws.MessageType `json:"type"`
	Agent     *store.Agent   `json:"agent"`
	Timestamp time.Time      `json:"timestamp"`
}

// broadcastTaskStatusChanged broadcasts a task status change to connected clients.
func (h *StatusHandler) broadcastTaskStatusChanged(task *store.Task, previousStatus string) {
	if h.hub == nil || task == nil {
		return
	}

	payload, err := json.Marshal(taskStatusBroadcast{
		Type:           MessageTaskStatusChanged,
		Task:           task,
		PreviousStatus: previousStatus,
		Timestamp:      time.Now().UTC(),
	})
	if err != nil {
		return
	}

	h.hub.Broadcast(task.OrgID, payload)
}

func (h *StatusHandler) broadcastTaskUpdated(task *store.Task) {
	if h.hub == nil || task == nil {
		return
	}

	payload, err := json.Marshal(taskUpdatedBroadcast{
		Type:      ws.MessageTaskUpdated,
		Task:      task,
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		return
	}

	h.hub.Broadcast(task.OrgID, payload)
}

// broadcastAgentStatusChanged broadcasts an agent status change to connected clients.
func (h *StatusHandler) broadcastAgentStatusChanged(orgID string, agent *store.Agent) {
	if h.hub == nil || agent == nil {
		return
	}

	payload, err := json.Marshal(agentStatusBroadcast{
		Type:      MessageAgentStatusChanged,
		Agent:     agent,
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		return
	}

	h.hub.Broadcast(orgID, payload)
}

// ParseStatusEvent parses a raw webhook payload into a StatusEvent.
func ParseStatusEvent(body []byte) (*StatusEvent, error) {
	var event StatusEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	// Normalize org_id
	if event.OrgID == "" {
		var raw map[string]interface{}
		_ = json.Unmarshal(body, &raw)
		if orgID, ok := raw["organization_id"].(string); ok {
			event.OrgID = orgID
		}
	}

	event.RawBody = body
	return &event, nil
}

// IsSupportedEvent returns true if the event type is supported.
func IsSupportedEvent(eventType string) bool {
	switch eventType {
	case EventTaskStarted, EventTaskCompleted, EventTaskFailed, EventTaskUpdated, EventTaskProgress, EventAgentStatus:
		return true
	default:
		return false
	}
}

func (h *StatusHandler) extractProgressPercent(event StatusEvent) (int, bool) {
	if event.Task != nil && event.Task.Percent != nil {
		return *event.Task.Percent, true
	}
	if event.Percent != nil {
		return *event.Percent, true
	}
	return 0, false
}

func isValidTaskTransition(from, to string) bool {
	if from == "" || to == "" {
		return false
	}
	if from == to {
		return true
	}
	allowed, ok := validTaskTransitions[from]
	if !ok {
		return false
	}
	_, ok = allowed[to]
	return ok
}

func mergeProgressContext(raw []byte, percent int) (json.RawMessage, error) {
	if percent < 0 || percent > 100 {
		return nil, fmt.Errorf("progress percent out of range: %d", percent)
	}

	context := map[string]interface{}{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &context); err != nil {
			return nil, err
		}
	}

	context["progress_percent"] = percent

	updated, err := json.Marshal(context)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(updated), nil
}
