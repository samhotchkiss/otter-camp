package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var (
	exportDB     *sql.DB
	exportDBErr  error
	exportDBOnce sync.Once
)

// ExportData represents the complete workspace export structure.
type ExportData struct {
	Version    string           `json:"version"`
	ExportedAt time.Time        `json:"exported_at"`
	OrgID      string           `json:"org_id"`
	Tasks      []ExportTask     `json:"tasks"`
	Projects   []ExportProject  `json:"projects"`
	Agents     []ExportAgent    `json:"agents"`
	Activities []ExportActivity `json:"activities"`
	TaskCount  int              `json:"task_count"`
	TotalItems int              `json:"total_items"`
}

// ExportTask represents a task in the export.
type ExportTask struct {
	ID              string          `json:"id"`
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

// ExportProject represents a project in the export.
type ExportProject struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    *string   `json:"description,omitempty"`
	Status         string    `json:"status"`
	RepoURL        *string   `json:"repo_url,omitempty"`
	PrimaryAgentID *string   `json:"primary_agent_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExportAgent represents an agent in the export.
type ExportAgent struct {
	ID             string    `json:"id"`
	Slug           string    `json:"slug"`
	DisplayName    string    `json:"display_name"`
	AvatarURL      *string   `json:"avatar_url,omitempty"`
	WebhookURL     *string   `json:"webhook_url,omitempty"`
	Status         string    `json:"status"`
	SessionPattern *string   `json:"session_pattern,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ExportActivity represents an activity log entry in the export.
type ExportActivity struct {
	ID        string          `json:"id"`
	TaskID    *string         `json:"task_id,omitempty"`
	AgentID   *string         `json:"agent_id,omitempty"`
	Action    string          `json:"action"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"created_at"`
}

// ImportRequest represents the import request body.
type ImportRequest struct {
	Data   ExportData `json:"data"`
	Mode   string     `json:"mode"` // "merge" or "replace"
	DryRun bool       `json:"dry_run"`
}

// ImportResult represents the result of an import operation.
type ImportResult struct {
	Success          bool     `json:"success"`
	DryRun           bool     `json:"dry_run"`
	TasksImported    int      `json:"tasks_imported"`
	TasksSkipped     int      `json:"tasks_skipped"`
	ProjectsImported int      `json:"projects_imported"`
	ProjectsSkipped  int      `json:"projects_skipped"`
	AgentsImported   int      `json:"agents_imported"`
	AgentsSkipped    int      `json:"agents_skipped"`
	Errors           []string `json:"errors,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

// ValidationResult represents the result of validating import data.
type ValidationResult struct {
	Valid         bool     `json:"valid"`
	Version       string   `json:"version"`
	OrgID         string   `json:"org_id"`
	ExportedAt    string   `json:"exported_at"`
	TaskCount     int      `json:"task_count"`
	ProjectCount  int      `json:"project_count"`
	AgentCount    int      `json:"agent_count"`
	ActivityCount int      `json:"activity_count"`
	TotalItems    int      `json:"total_items"`
	Errors        []string `json:"errors,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}

// HandleExport handles GET /api/export
func HandleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
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

	db, err := getExportDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	export, err := buildExport(r.Context(), db, orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to export data"})
		return
	}

	// Set headers for file download
	filename := fmt.Sprintf("otter-camp-export-%s.json", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	sendJSON(w, http.StatusOK, export)
}

// HandleImportValidate handles POST /api/import/validate
func HandleImportValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	var data ExportData
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	result := validateImportData(data)
	sendJSON(w, http.StatusOK, result)
}

// HandleImport handles POST /api/import
func HandleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
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

	var req ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	// Validate import data first
	validation := validateImportData(req.Data)
	if !validation.Valid {
		sendJSON(w, http.StatusBadRequest, ImportResult{
			Success: false,
			Errors:  validation.Errors,
		})
		return
	}

	// Default mode is merge
	if req.Mode == "" {
		req.Mode = "merge"
	}
	if req.Mode != "merge" && req.Mode != "replace" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid mode: must be 'merge' or 'replace'"})
		return
	}

	db, err := getExportDB()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}

	result, err := executeImport(r.Context(), db, orgID, req)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to import data"})
		return
	}

	sendJSON(w, http.StatusOK, result)
}

func buildExport(ctx context.Context, db *sql.DB, orgID string) (*ExportData, error) {
	export := &ExportData{
		Version:    "1.0",
		ExportedAt: time.Now().UTC(),
		OrgID:      orgID,
		Tasks:      make([]ExportTask, 0),
		Projects:   make([]ExportProject, 0),
		Agents:     make([]ExportAgent, 0),
		Activities: make([]ExportActivity, 0),
	}

	// Export tasks
	tasks, err := exportTasks(ctx, db, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to export tasks: %w", err)
	}
	export.Tasks = tasks
	export.TaskCount = len(tasks)

	// Export projects
	projects, err := exportProjects(ctx, db, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to export projects: %w", err)
	}
	export.Projects = projects

	// Export agents
	agents, err := exportAgents(ctx, db, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to export agents: %w", err)
	}
	export.Agents = agents

	// Export activities (limited to last 1000)
	activities, err := exportActivities(ctx, db, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to export activities: %w", err)
	}
	export.Activities = activities

	export.TotalItems = len(tasks) + len(projects) + len(agents) + len(activities)

	return export, nil
}

func exportTasks(ctx context.Context, db *sql.DB, orgID string) ([]ExportTask, error) {
	query := `SELECT id, project_id, number, title, description, status, priority, context, 
		assigned_agent_id, parent_task_id, created_at, updated_at 
		FROM tasks WHERE org_id = $1 ORDER BY created_at DESC`

	rows, err := db.QueryContext(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := make([]ExportTask, 0)
	for rows.Next() {
		var task ExportTask
		var projectID, description, assignedAgentID, parentTaskID sql.NullString
		var contextBytes []byte

		err := rows.Scan(
			&task.ID, &projectID, &task.Number, &task.Title, &description,
			&task.Status, &task.Priority, &contextBytes, &assignedAgentID,
			&parentTaskID, &task.CreatedAt, &task.UpdatedAt,
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
		if len(contextBytes) > 0 {
			task.Context = json.RawMessage(contextBytes)
		} else {
			task.Context = json.RawMessage("{}")
		}

		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

func exportProjects(ctx context.Context, db *sql.DB, orgID string) ([]ExportProject, error) {
	query := `SELECT id, name, description, status, repo_url, primary_agent_id, created_at, updated_at 
		FROM projects WHERE org_id = $1 ORDER BY created_at DESC`

	rows, err := db.QueryContext(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	projects := make([]ExportProject, 0)
	for rows.Next() {
		var project ExportProject
		var description, repoURL, primaryAgentID sql.NullString

		err := rows.Scan(
			&project.ID, &project.Name, &description, &project.Status,
			&repoURL, &primaryAgentID, &project.CreatedAt, &project.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if description.Valid {
			project.Description = &description.String
		}
		if repoURL.Valid {
			project.RepoURL = &repoURL.String
		}
		if primaryAgentID.Valid {
			project.PrimaryAgentID = &primaryAgentID.String
		}

		projects = append(projects, project)
	}

	return projects, rows.Err()
}

func exportAgents(ctx context.Context, db *sql.DB, orgID string) ([]ExportAgent, error) {
	query := `SELECT id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at 
		FROM agents WHERE org_id = $1 ORDER BY created_at DESC`

	rows, err := db.QueryContext(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := make([]ExportAgent, 0)
	for rows.Next() {
		var agent ExportAgent
		var avatarURL, webhookURL, sessionPattern sql.NullString

		err := rows.Scan(
			&agent.ID, &agent.Slug, &agent.DisplayName, &avatarURL,
			&webhookURL, &agent.Status, &sessionPattern,
			&agent.CreatedAt, &agent.UpdatedAt,
		)
		if err != nil {
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

		agents = append(agents, agent)
	}

	return agents, rows.Err()
}

func exportActivities(ctx context.Context, db *sql.DB, orgID string) ([]ExportActivity, error) {
	query := `SELECT id, task_id, agent_id, action, metadata, created_at 
		FROM activity_log WHERE org_id = $1 ORDER BY created_at DESC LIMIT 1000`

	rows, err := db.QueryContext(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	activities := make([]ExportActivity, 0)
	for rows.Next() {
		var activity ExportActivity
		var taskID, agentID sql.NullString
		var metadataBytes []byte

		err := rows.Scan(
			&activity.ID, &taskID, &agentID, &activity.Action,
			&metadataBytes, &activity.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if taskID.Valid {
			activity.TaskID = &taskID.String
		}
		if agentID.Valid {
			activity.AgentID = &agentID.String
		}
		if len(metadataBytes) > 0 {
			activity.Metadata = json.RawMessage(metadataBytes)
		} else {
			activity.Metadata = json.RawMessage("{}")
		}

		activities = append(activities, activity)
	}

	return activities, rows.Err()
}

func validateImportData(data ExportData) ValidationResult {
	result := ValidationResult{
		Valid:         true,
		Version:       data.Version,
		OrgID:         data.OrgID,
		ExportedAt:    data.ExportedAt.Format(time.RFC3339),
		TaskCount:     len(data.Tasks),
		ProjectCount:  len(data.Projects),
		AgentCount:    len(data.Agents),
		ActivityCount: len(data.Activities),
		TotalItems:    len(data.Tasks) + len(data.Projects) + len(data.Agents) + len(data.Activities),
		Errors:        make([]string, 0),
		Warnings:      make([]string, 0),
	}

	// Check version
	if data.Version == "" {
		result.Errors = append(result.Errors, "missing version field")
		result.Valid = false
	} else if data.Version != "1.0" {
		result.Warnings = append(result.Warnings, fmt.Sprintf("unknown version: %s (expected 1.0)", data.Version))
	}

	// Check org_id
	if data.OrgID == "" {
		result.Warnings = append(result.Warnings, "missing org_id in export (will use target workspace)")
	}

	// Validate tasks
	for i, task := range data.Tasks {
		if task.ID == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("task[%d]: missing id", i))
			result.Valid = false
		}
		if task.Title == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("task[%d]: missing title", i))
			result.Valid = false
		}
		if task.Status == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("task[%d]: missing status (will default to 'queued')", i))
		}
	}

	// Validate projects
	for i, project := range data.Projects {
		if project.ID == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("project[%d]: missing id", i))
			result.Valid = false
		}
		if project.Name == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("project[%d]: missing name", i))
			result.Valid = false
		}
	}

	// Validate agents
	for i, agent := range data.Agents {
		if agent.ID == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("agent[%d]: missing id", i))
			result.Valid = false
		}
		if agent.Slug == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("agent[%d]: missing slug", i))
			result.Valid = false
		}
		if agent.DisplayName == "" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("agent[%d]: missing display_name (will use slug)", i))
		}
	}

	return result
}

func executeImport(ctx context.Context, db *sql.DB, orgID string, req ImportRequest) (*ImportResult, error) {
	result := &ImportResult{
		Success:  true,
		DryRun:   req.DryRun,
		Errors:   make([]string, 0),
		Warnings: make([]string, 0),
	}

	if req.DryRun {
		// For dry run, just count what would be imported
		result.ProjectsImported = len(req.Data.Projects)
		result.AgentsImported = len(req.Data.Agents)
		result.TasksImported = len(req.Data.Tasks)
		return result, nil
	}

	// Start transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// If replace mode, delete existing data first
	if req.Mode == "replace" {
		if _, err := tx.ExecContext(ctx, "DELETE FROM activity_log WHERE org_id = $1", orgID); err != nil {
			return nil, fmt.Errorf("failed to clear activities: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM tasks WHERE org_id = $1", orgID); err != nil {
			return nil, fmt.Errorf("failed to clear tasks: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE org_id = $1", orgID); err != nil {
			return nil, fmt.Errorf("failed to clear projects: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM agents WHERE org_id = $1", orgID); err != nil {
			return nil, fmt.Errorf("failed to clear agents: %w", err)
		}
	}

	// Import projects first (tasks may reference them)
	for _, project := range req.Data.Projects {
		imported, err := importProject(ctx, tx, orgID, project, req.Mode)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("project %s: %v", project.ID, err))
			result.ProjectsSkipped++
		} else if imported {
			result.ProjectsImported++
		} else {
			result.ProjectsSkipped++
		}
	}

	// Import agents (tasks may reference them)
	for _, agent := range req.Data.Agents {
		imported, err := importAgent(ctx, tx, orgID, agent, req.Mode)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("agent %s: %v", agent.ID, err))
			result.AgentsSkipped++
		} else if imported {
			result.AgentsImported++
		} else {
			result.AgentsSkipped++
		}
	}

	// Import tasks
	for _, task := range req.Data.Tasks {
		imported, err := importTask(ctx, tx, orgID, task, req.Mode)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.ID, err))
			result.TasksSkipped++
		} else if imported {
			result.TasksImported++
		} else {
			result.TasksSkipped++
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	result.Success = len(result.Errors) == 0

	return result, nil
}

func importProject(ctx context.Context, tx *sql.Tx, orgID string, project ExportProject, mode string) (bool, error) {
	// Check if exists
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1)", project.ID).Scan(&exists)
	if err != nil {
		return false, err
	}

	if exists && mode == "merge" {
		// Skip existing in merge mode
		return false, nil
	}

	status := project.Status
	if status == "" {
		status = "active"
	}

	query := `INSERT INTO projects (id, org_id, name, description, status, repo_url, primary_agent_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			status = EXCLUDED.status,
			repo_url = EXCLUDED.repo_url,
			primary_agent_id = EXCLUDED.primary_agent_id,
			updated_at = EXCLUDED.updated_at`

	_, err = tx.ExecContext(ctx, query,
		project.ID, orgID, project.Name, nullableString(project.Description),
		status, nullableString(project.RepoURL), nullableString(project.PrimaryAgentID), project.CreatedAt, project.UpdatedAt,
	)

	return err == nil, err
}

func importAgent(ctx context.Context, tx *sql.Tx, orgID string, agent ExportAgent, mode string) (bool, error) {
	// Check if exists
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1)", agent.ID).Scan(&exists)
	if err != nil {
		return false, err
	}

	if exists && mode == "merge" {
		// Skip existing in merge mode
		return false, nil
	}

	status := agent.Status
	if status == "" {
		status = "active"
	}

	displayName := agent.DisplayName
	if displayName == "" {
		displayName = agent.Slug
	}

	query := `INSERT INTO agents (id, org_id, slug, display_name, avatar_url, webhook_url, status, session_pattern, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			slug = EXCLUDED.slug,
			display_name = EXCLUDED.display_name,
			avatar_url = EXCLUDED.avatar_url,
			webhook_url = EXCLUDED.webhook_url,
			status = EXCLUDED.status,
			session_pattern = EXCLUDED.session_pattern,
			updated_at = EXCLUDED.updated_at`

	_, err = tx.ExecContext(ctx, query,
		agent.ID, orgID, agent.Slug, displayName,
		nullableString(agent.AvatarURL), nullableString(agent.WebhookURL),
		status, nullableString(agent.SessionPattern),
		agent.CreatedAt, agent.UpdatedAt,
	)

	return err == nil, err
}

func importTask(ctx context.Context, tx *sql.Tx, orgID string, task ExportTask, mode string) (bool, error) {
	// Check if exists
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE id = $1)", task.ID).Scan(&exists)
	if err != nil {
		return false, err
	}

	if exists && mode == "merge" {
		// Skip existing in merge mode
		return false, nil
	}

	status := task.Status
	if status == "" {
		status = "queued"
	}

	priority := task.Priority
	if priority == "" {
		priority = "P2"
	}

	contextBytes := task.Context
	if len(contextBytes) == 0 {
		contextBytes = json.RawMessage("{}")
	}

	query := `INSERT INTO tasks (id, org_id, project_id, number, title, description, status, priority, context, assigned_agent_id, parent_task_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO UPDATE SET
			project_id = EXCLUDED.project_id,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			status = EXCLUDED.status,
			priority = EXCLUDED.priority,
			context = EXCLUDED.context,
			assigned_agent_id = EXCLUDED.assigned_agent_id,
			parent_task_id = EXCLUDED.parent_task_id,
			updated_at = EXCLUDED.updated_at`

	_, err = tx.ExecContext(ctx, query,
		task.ID, orgID, nullableString(task.ProjectID), task.Number,
		task.Title, nullableString(task.Description), status, priority,
		contextBytes, nullableString(task.AssignedAgentID),
		nullableString(task.ParentTaskID), task.CreatedAt, task.UpdatedAt,
	)

	return err == nil, err
}

func getExportDB() (*sql.DB, error) {
	exportDBOnce.Do(func() {
		dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
		if dbURL == "" {
			exportDBErr = errors.New("DATABASE_URL is not set")
			return
		}

		db, err := sql.Open("postgres", dbURL)
		if err != nil {
			exportDBErr = err
			return
		}

		if err := db.Ping(); err != nil {
			_ = db.Close()
			exportDBErr = err
			return
		}

		exportDB = db
	})

	return exportDB, exportDBErr
}
