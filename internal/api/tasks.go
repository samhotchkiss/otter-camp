package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

const taskSelectColumns = "id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at"

const (
	getTaskByIDSQL = "SELECT " + taskSelectColumns + " FROM tasks WHERE id = $1"
	createTaskSQL  = `INSERT INTO tasks (
		org_id,
		project_id,
		title,
		description,
		status,
		priority,
		context,
		assigned_agent_id,
		parent_task_id
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	RETURNING ` + taskSelectColumns
	updateTaskSQL = `UPDATE tasks
	SET
		project_id = $1,
		title = $2,
		description = $3,
		status = $4,
		priority = $5,
		context = $6,
		assigned_agent_id = $7,
		parent_task_id = $8
	WHERE id = $9
	RETURNING ` + taskSelectColumns
	updateTaskStatusSQL = "UPDATE tasks SET status = $1 WHERE id = $2 RETURNING " + taskSelectColumns
)

var (
	tasksDB     *sql.DB
	tasksDBErr  error
	tasksDBOnce sync.Once
)

var allowedTaskStatuses = map[string]struct{}{
	"queued":      {},
	"dispatched":  {},
	"in_progress": {},
	"blocked":     {},
	"review":      {},
	"done":        {},
	"cancelled":   {},
}

var allowedTaskPriorities = map[string]struct{}{
	"P0": {},
	"P1": {},
	"P2": {},
	"P3": {},
}

// Task represents a task API payload.
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

// TaskHandler manages task endpoints.
type TaskHandler struct {
	Hub *ws.Hub
}

type TasksResponse struct {
	OrgID     string  `json:"org_id"`
	Status    string  `json:"status,omitempty"`
	ProjectID *string `json:"project_id,omitempty"`
	AgentID   *string `json:"agent_id,omitempty"`
	Tasks     []Task  `json:"tasks"`
}

type CreateTaskRequest struct {
	OrgID           string          `json:"org_id"`
	ProjectID       *string         `json:"project_id,omitempty"`
	Title           string          `json:"title"`
	Description     *string         `json:"description,omitempty"`
	Status          string          `json:"status,omitempty"`
	Priority        string          `json:"priority,omitempty"`
	Context         json.RawMessage `json:"context,omitempty"`
	AssignedAgentID *string         `json:"assigned_agent_id,omitempty"`
	ParentTaskID    *string         `json:"parent_task_id,omitempty"`
}

type TaskStatusRequest struct {
	Status string `json:"status"`
}

// ListTasks handles GET /api/tasks
func (h *TaskHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	// Demo mode: return sample tasks only when explicitly requested.
	if r.URL.Query().Get("demo") == "true" {
		demoTasks := []map[string]interface{}{
			{
				"id":       "demo-1",
				"title":    "Deploy OtterCamp v1.0",
				"status":   "in_progress",
				"priority": "P1",
				"agent":    "Derek",
				"project":  "OtterCamp",
			},
			{
				"id":       "demo-2",
				"title":    "Review blog post draft",
				"status":   "review",
				"priority": "P2",
				"agent":    "Stone",
				"project":  "Content",
			},
			{
				"id":       "demo-3",
				"title":    "Schedule social media posts",
				"status":   "done",
				"priority": "P3",
				"agent":    "Nova",
				"project":  "Marketing",
			},
		}
		sendJSON(w, http.StatusOK, demoTasks)
		return
	}

	orgID := strings.TrimSpace(r.URL.Query().Get("org_id"))
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing query parameter: org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if status != "" {
		if !isValidStatus(status) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
			return
		}
	}

	projectID := firstNonEmpty(
		strings.TrimSpace(r.URL.Query().Get("project")),
		strings.TrimSpace(r.URL.Query().Get("project_id")),
	)
	var projectPtr *string
	if projectID != "" {
		if !uuidRegex.MatchString(projectID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project"})
			return
		}
		projectPtr = &projectID
	}

	agentID := firstNonEmpty(
		strings.TrimSpace(r.URL.Query().Get("agent")),
		strings.TrimSpace(r.URL.Query().Get("agent_id")),
	)
	var agentPtr *string
	if agentID != "" {
		if !uuidRegex.MatchString(agentID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid agent"})
			return
		}
		agentPtr = &agentID
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	query, args := buildListTasksQuery(orgID, status, projectPtr, agentPtr)
	rows, err := db.QueryContext(r.Context(), query, args...)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list tasks"})
		return
	}
	defer rows.Close()

	tasks := make([]Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read tasks"})
			return
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read tasks"})
		return
	}

	sendJSON(w, http.StatusOK, TasksResponse{
		OrgID:     orgID,
		Status:    status,
		ProjectID: projectPtr,
		AgentID:   agentPtr,
		Tasks:     tasks,
	})
}

// CreateTask handles POST /api/tasks
func (h *TaskHandler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	orgID := strings.TrimSpace(req.OrgID)
	if orgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return
	}
	if !uuidRegex.MatchString(orgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid org_id"})
		return
	}

	if err := validateOptionalUUID(req.ProjectID, "project_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := validateOptionalUUID(req.AssignedAgentID, "assigned_agent_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := validateOptionalUUID(req.ParentTaskID, "parent_task_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing title"})
		return
	}

	status := normalizeStatus(req.Status)
	if status == "" {
		status = "queued"
	}
	if !isValidStatus(status) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
		return
	}

	priority := normalizePriority(req.Priority)
	if priority == "" {
		priority = "P2"
	}
	if !isValidPriority(priority) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid priority"})
		return
	}

	contextBytes := normalizeContext(req.Context)
	if !json.Valid(contextBytes) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid context"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	args := []interface{}{
		orgID,
		nullableString(req.ProjectID),
		title,
		nullableString(req.Description),
		status,
		priority,
		contextBytes,
		nullableString(req.AssignedAgentID),
		nullableString(req.ParentTaskID),
	}

	task, err := scanTask(db.QueryRowContext(r.Context(), createTaskSQL, args...))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create task"})
		return
	}

	broadcastTaskCreated(h.Hub, task)
	sendJSON(w, http.StatusOK, task)
}

// UpdateTask handles PATCH /api/tasks/:id
func (h *TaskHandler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task id"})
		return
	}
	if !uuidRegex.MatchString(taskID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task id"})
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	existing, err := scanTask(db.QueryRowContext(r.Context(), getTaskByIDSQL, taskID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "task not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load task"})
		return
	}

	projectID, projectSet, err := parseOptionalStringField(raw, "project_id")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project_id"})
		return
	}
	if projectSet {
		if err := validateOptionalUUID(projectID, "project_id"); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
	}

	title, titleSet, err := parseOptionalStringField(raw, "title")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid title"})
		return
	}
	if titleSet {
		if title == nil || strings.TrimSpace(*title) == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "title cannot be empty"})
			return
		}
	}

	description, descriptionSet, err := parseOptionalStringField(raw, "description")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid description"})
		return
	}

	status, statusSet, err := parseOptionalStringField(raw, "status")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
		return
	}
	if statusSet {
		if status == nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "status cannot be null"})
			return
		}
		normalized := normalizeStatus(*status)
		if normalized == "" || !isValidStatus(normalized) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
			return
		}
		*status = normalized
	}

	priority, prioritySet, err := parseOptionalStringField(raw, "priority")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid priority"})
		return
	}
	if prioritySet {
		if priority == nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "priority cannot be null"})
			return
		}
		normalized := normalizePriority(*priority)
		if normalized == "" || !isValidPriority(normalized) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid priority"})
			return
		}
		*priority = normalized
	}

	context, contextSet, err := parseOptionalRawField(raw, "context")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid context"})
		return
	}
	if contextSet && len(context) > 0 && !json.Valid(context) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid context"})
		return
	}

	assignedAgentID, assignedSet, err := parseOptionalStringField(raw, "assigned_agent_id")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid assigned_agent_id"})
		return
	}
	if assignedSet {
		if err := validateOptionalUUID(assignedAgentID, "assigned_agent_id"); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
	}

	parentTaskID, parentSet, err := parseOptionalStringField(raw, "parent_task_id")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid parent_task_id"})
		return
	}
	if parentSet {
		if err := validateOptionalUUID(parentTaskID, "parent_task_id"); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
	}

	updated := existing
	if projectSet {
		updated.ProjectID = projectID
	}
	if titleSet {
		updated.Title = strings.TrimSpace(*title)
	}
	if descriptionSet {
		updated.Description = description
	}
	if statusSet {
		updated.Status = *status
	}
	if prioritySet {
		updated.Priority = *priority
	}
	if contextSet {
		updated.Context = normalizeContext(context)
	}
	if assignedSet {
		updated.AssignedAgentID = assignedAgentID
	}
	if parentSet {
		updated.ParentTaskID = parentTaskID
	}

	contextBytes := normalizeContext(updated.Context)
	if !json.Valid(contextBytes) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid context"})
		return
	}

	args := []interface{}{
		nullableString(updated.ProjectID),
		updated.Title,
		nullableString(updated.Description),
		updated.Status,
		updated.Priority,
		contextBytes,
		nullableString(updated.AssignedAgentID),
		nullableString(updated.ParentTaskID),
		updated.ID,
	}

	result, err := scanTask(db.QueryRowContext(r.Context(), updateTaskSQL, args...))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update task"})
		return
	}

	broadcastTaskUpdated(h.Hub, result)
	sendJSON(w, http.StatusOK, result)
}

// UpdateTaskStatus handles PATCH /api/tasks/:id/status
func (h *TaskHandler) UpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing task id"})
		return
	}
	if !uuidRegex.MatchString(taskID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid task id"})
		return
	}

	var req TaskStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	status := normalizeStatus(req.Status)
	if status == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing status"})
		return
	}
	if !isValidStatus(status) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid status"})
		return
	}

	db, err := getTasksDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	existing, err := scanTask(db.QueryRowContext(r.Context(), getTaskByIDSQL, taskID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "task not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load task"})
		return
	}

	result, err := scanTask(db.QueryRowContext(r.Context(), updateTaskStatusSQL, status, taskID))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update task status"})
		return
	}

	broadcastTaskStatusChanged(h.Hub, result, existing.Status)
	sendJSON(w, http.StatusOK, result)
}

func getTasksDB() (*sql.DB, error) {
	tasksDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			tasksDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			tasksDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			tasksDBErr = err
			return
		}

		tasksDB = db
	})

	return tasksDB, tasksDBErr
}

func buildListTasksQuery(orgID, status string, projectID, agentID *string) (string, []interface{}) {
	conditions := []string{"org_id = $1"}
	args := []interface{}{orgID}

	if status != "" {
		args = append(args, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if projectID != nil {
		args = append(args, *projectID)
		conditions = append(conditions, fmt.Sprintf("project_id = $%d", len(args)))
	}
	if agentID != nil {
		args = append(args, *agentID)
		conditions = append(conditions, fmt.Sprintf("assigned_agent_id = $%d", len(args)))
	}

	query := "SELECT " + taskSelectColumns + " FROM tasks WHERE " + strings.Join(conditions, " AND ") + " ORDER BY created_at DESC"
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

func validateOptionalUUID(value *string, field string) error {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if !uuidRegex.MatchString(trimmed) {
		return fmt.Errorf("invalid %s", field)
	}
	*value = trimmed
	return nil
}

func nullableString(value *string) interface{} {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func normalizeContext(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizePriority(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func isValidStatus(status string) bool {
	_, ok := allowedTaskStatuses[status]
	return ok
}

func isValidPriority(priority string) bool {
	_, ok := allowedTaskPriorities[priority]
	return ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseOptionalStringField(raw map[string]json.RawMessage, key string) (*string, bool, error) {
	value, ok := raw[key]
	if !ok {
		return nil, false, nil
	}
	if len(value) == 0 || string(value) == "null" {
		return nil, true, nil
	}
	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return nil, true, err
	}
	return &parsed, true, nil
}

func parseOptionalRawField(raw map[string]json.RawMessage, key string) (json.RawMessage, bool, error) {
	value, ok := raw[key]
	if !ok {
		return nil, false, nil
	}
	if len(value) == 0 || string(value) == "null" {
		return nil, true, nil
	}
	return value, true, nil
}
