package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// ProjectsHandler handles project-related API requests.
type ProjectsHandler struct {
	Store *store.ProjectStore
	DB    *sql.DB
}

type projectAPIResponse struct {
	ID                string          `json:"id"`
	OrgID             string          `json:"org_id,omitempty"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	RepoURL           string          `json:"repo_url,omitempty"`
	Status            string          `json:"status"`
	Labels            []store.Label   `json:"labels"`
	Lead              string          `json:"lead,omitempty"`
	PrimaryAgentID    *string         `json:"primary_agent_id,omitempty"`
	WorkflowEnabled   bool            `json:"workflow_enabled"`
	WorkflowSchedule  json.RawMessage `json:"workflow_schedule,omitempty"`
	WorkflowTemplate  json.RawMessage `json:"workflow_template,omitempty"`
	WorkflowAgentID   *string         `json:"workflow_agent_id,omitempty"`
	WorkflowLastRunAt *string         `json:"workflow_last_run_at,omitempty"`
	WorkflowNextRunAt *string         `json:"workflow_next_run_at,omitempty"`
	WorkflowRunCount  int             `json:"workflow_run_count"`
	CreatedAt         string          `json:"created_at,omitempty"`
	TaskCount         int             `json:"taskCount"`
	CompletedCount    int             `json:"completedCount"`
}

type projectWorkflowRunResponse struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id"`
	IssueNumber  int64   `json:"issue_number"`
	Title        string  `json:"title"`
	State        string  `json:"state"`
	WorkStatus   string  `json:"work_status"`
	Priority     string  `json:"priority"`
	OwnerAgentID *string `json:"owner_agent_id,omitempty"`
	CreatedAt    string  `json:"created_at"`
	ClosedAt     *string `json:"closed_at,omitempty"`
}

type projectWorkflowTemplate struct {
	TitlePattern string   `json:"title_pattern"`
	Body         string   `json:"body"`
	Priority     string   `json:"priority"`
	Labels       []string `json:"labels"`
	AutoClose    bool     `json:"auto_close"`
	Pipeline     string   `json:"pipeline"`
}

// Demo projects for when database is unavailable
var demoProjects = []map[string]interface{}{
	{
		"id":             "proj-1",
		"name":           "Pearl Proxy",
		"description":    "Memory and routing infrastructure",
		"status":         "active",
		"repo_url":       "https://github.com/The-Trawl/pearl",
		"lead":           "Derek",
		"taskCount":      12,
		"completedCount": 5,
	},
	{
		"id":             "proj-2",
		"name":           "Otter Camp",
		"description":    "Task management for AI-assisted workflows",
		"status":         "active",
		"repo_url":       "https://github.com/samhotchkiss/otter-camp",
		"lead":           "Derek",
		"taskCount":      24,
		"completedCount": 18,
	},
	{
		"id":             "proj-3",
		"name":           "ItsAlive",
		"description":    "Static site deployment platform",
		"status":         "active",
		"repo_url":       "https://github.com/The-Trawl/itsalive",
		"lead":           "Ivy",
		"taskCount":      8,
		"completedCount": 8,
	},
	{
		"id":             "proj-4",
		"name":           "Three Stones",
		"description":    "Educational content and presentations",
		"status":         "archived",
		"repo_url":       nil,
		"lead":           "Stone",
		"taskCount":      15,
		"completedCount": 10,
	},
}

var embeddedDescriptionMarkers = []string{
	"--description",
	"—description",
	"–description",
	"−description",
}

func normalizeProjectCreateNameAndDescription(name string, description *string) (string, *string) {
	trimmedName := strings.TrimSpace(name)

	if description != nil {
		trimmedDescription := strings.TrimSpace(*description)
		if trimmedDescription != "" {
			return trimmedName, &trimmedDescription
		}
		description = nil
	}

	lowerName := strings.ToLower(trimmedName)
	markerIndex := -1
	markerValue := ""
	for _, marker := range embeddedDescriptionMarkers {
		idx := strings.Index(lowerName, marker)
		if idx <= 0 {
			continue
		}
		if markerIndex == -1 || idx < markerIndex {
			markerIndex = idx
			markerValue = marker
		}
	}
	if markerIndex == -1 {
		return trimmedName, nil
	}

	rawDescription := strings.TrimSpace(trimmedName[markerIndex+len(markerValue):])
	if strings.HasPrefix(rawDescription, "=") {
		rawDescription = strings.TrimSpace(strings.TrimPrefix(rawDescription, "="))
	}
	if rawDescription == "" {
		return trimmedName, nil
	}

	normalizedName := strings.TrimSpace(trimmedName[:markerIndex])
	if normalizedName == "" {
		return trimmedName, nil
	}

	return normalizedName, &rawDescription
}

// List returns all projects for the authenticated workspace.
func (h *ProjectsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Check for demo mode or missing database
	if h.DB == nil {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"projects": demoProjects,
			"total":    len(demoProjects),
		})
		return
	}

	// Get workspace from context (set by middleware) or query param
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("org_id")
	}
	// Fall back to session token's org if available
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}

	if workspaceID == "" {
		if r.URL.Query().Get("demo") == "true" {
			sendJSON(w, http.StatusOK, map[string]interface{}{
				"projects": demoProjects,
				"total":    len(demoProjects),
			})
			return
		}
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	labelFilterIDs, err := parseProjectLabelFilterIDs(r.URL.Query()["label"])
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	var allowedProjectIDs map[string]struct{}
	if len(labelFilterIDs) > 0 {
		projectStore := h.Store
		if projectStore == nil {
			projectStore = store.NewProjectStore(h.DB)
		}
		filteredProjects, listErr := projectStore.ListWithLabels(ctx, labelFilterIDs)
		if listErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to filter projects"})
			return
		}
		if len(filteredProjects) == 0 {
			sendJSON(w, http.StatusOK, map[string]interface{}{
				"projects": []projectAPIResponse{},
				"total":    0,
			})
			return
		}
		allowedProjectIDs = make(map[string]struct{}, len(filteredProjects))
		for _, project := range filteredProjects {
			allowedProjectIDs[project.ID] = struct{}{}
		}
	}

	workflowOnly, err := parseWorkflowFilter(r.URL.Query().Get("workflow"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	usePrimaryAgent := supportsProjectPrimaryAgentColumn(r.Context(), h.DB)
	useWorkflow := supportsProjectWorkflowColumns(r.Context(), h.DB)
	rows, err := h.DB.QueryContext(r.Context(), listProjectsQuery(usePrimaryAgent, useWorkflow, workflowOnly), workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list projects"})
		return
	}
	defer rows.Close()

	projects := make([]projectAPIResponse, 0)
	projectIDs := make([]string, 0)
	for rows.Next() {
		p, scanErr := scanProjectAPIResponse(rows, usePrimaryAgent, useWorkflow)
		if scanErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to parse projects"})
			return
		}
		if allowedProjectIDs != nil {
			if _, ok := allowedProjectIDs[p.ID]; !ok {
				continue
			}
		}
		p.Labels = []store.Label{}
		projects = append(projects, p)
		projectIDs = append(projectIDs, p.ID)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list projects"})
		return
	}
	if len(projectIDs) > 0 {
		labelMap, mapErr := store.NewLabelStore(h.DB).MapForProjects(ctx, projectIDs)
		if mapErr != nil {
			// Non-fatal: labels table may not exist yet (missing migration).
			// Log and continue — projects should still load without labels.
			log.Printf("WARN: failed to load project labels: %v", mapErr)
		} else {
			for idx := range projects {
				projects[idx].Labels = labelMap[projects[idx].ID]
			}
		}
	}

	sendJSON(w, http.StatusOK, map[string]interface{}{
		"projects": projects,
		"total":    len(projects),
	})
}

// Get returns a single project by ID.
func (h *ProjectsHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "id")
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	// Check for demo mode
	if h.DB == nil {
		// Return demo project if ID matches
		for _, p := range demoProjects {
			if p["id"] == projectID {
				sendJSON(w, http.StatusOK, p)
				return
			}
		}
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("org_id")
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}

	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)

	usePrimaryAgent := supportsProjectPrimaryAgentColumn(r.Context(), h.DB)
	useWorkflow := supportsProjectWorkflowColumns(r.Context(), h.DB)

	var p projectAPIResponse
	var err error
	p, err = scanProjectAPIResponse(
		h.DB.QueryRowContext(r.Context(), getProjectQuery(usePrimaryAgent, useWorkflow), projectID, workspaceID),
		usePrimaryAgent,
		useWorkflow,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to get project"})
		return
	}
	labels, labelsErr := store.NewLabelStore(h.DB).ListForProject(ctx, p.ID)
	if labelsErr != nil {
		// Non-fatal: labels table may not exist yet (missing migration).
		log.Printf("WARN: failed to load project labels for %s: %v", p.ID, labelsErr)
		p.Labels = []store.Label{}
	} else {
		p.Labels = labels
	}

	sendJSON(w, http.StatusOK, p)
}

// Create creates a new project.
func (h *ProjectsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	var input struct {
		Name              string           `json:"name"`
		Description       *string          `json:"description"`
		Status            string           `json:"status"`
		RepoURL           *string          `json:"repo_url"`
		WorkflowEnabled   bool             `json:"workflow_enabled"`
		WorkflowSchedule  *json.RawMessage `json:"workflow_schedule"`
		WorkflowTemplate  *json.RawMessage `json:"workflow_template"`
		WorkflowAgentID   *string          `json:"workflow_agent_id"`
		WorkflowLastRunAt *string          `json:"workflow_last_run_at"`
		WorkflowNextRunAt *string          `json:"workflow_next_run_at"`
		WorkflowRunCount  *int             `json:"workflow_run_count"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	if strings.TrimSpace(input.Name) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}
	input.Name, input.Description = normalizeProjectCreateNameAndDescription(input.Name, input.Description)

	if input.Status == "" {
		input.Status = "active"
	}

	workflowSchedule := json.RawMessage(nil)
	if input.WorkflowSchedule != nil {
		var normalizeErr error
		workflowSchedule, normalizeErr = normalizeWorkflowPatchJSON(*input.WorkflowSchedule, "workflow_schedule")
		if normalizeErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: normalizeErr.Error()})
			return
		}
	}
	workflowTemplate := json.RawMessage(nil)
	if input.WorkflowTemplate != nil {
		var normalizeErr error
		workflowTemplate, normalizeErr = normalizeWorkflowPatchJSON(*input.WorkflowTemplate, "workflow_template")
		if normalizeErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: normalizeErr.Error()})
			return
		}
	}

	workflowAgentID := input.WorkflowAgentID
	if workflowAgentID != nil {
		trimmed := strings.TrimSpace(*workflowAgentID)
		if trimmed == "" {
			workflowAgentID = nil
		} else {
			workflowAgentID = &trimmed
		}
	}
	if workflowAgentID != nil {
		if !uuidRegex.MatchString(*workflowAgentID) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_agent_id must be a UUID"})
			return
		}
		var exists bool
		err := h.DB.QueryRowContext(
			r.Context(),
			"SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND org_id = $2)",
			*workflowAgentID,
			workspaceID,
		).Scan(&exists)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate workflow agent"})
			return
		}
		if !exists {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_agent_id not found in workspace"})
			return
		}
	}

	workflowLastRunAt, err := parseProjectOptionalRFC3339(input.WorkflowLastRunAt, "workflow_last_run_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	workflowNextRunAt, err := parseProjectOptionalRFC3339(input.WorkflowNextRunAt, "workflow_next_run_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	workflowRunCount := 0
	if input.WorkflowRunCount != nil {
		if *input.WorkflowRunCount < 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_run_count must be non-negative"})
			return
		}
		workflowRunCount = *input.WorkflowRunCount
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)

	project, err := h.Store.Create(ctx, store.CreateProjectInput{
		Name:              input.Name,
		Description:       input.Description,
		Status:            input.Status,
		RepoURL:           input.RepoURL,
		WorkflowEnabled:   input.WorkflowEnabled,
		WorkflowSchedule:  workflowSchedule,
		WorkflowTemplate:  workflowTemplate,
		WorkflowAgentID:   workflowAgentID,
		WorkflowLastRunAt: workflowLastRunAt,
		WorkflowNextRunAt: workflowNextRunAt,
		WorkflowRunCount:  workflowRunCount,
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create project"})
		return
	}

	// Auto-provision a git repo for the new project
	if initErr := h.Store.InitProjectRepo(ctx, project.ID); initErr != nil {
		log.Printf("[projects] auto-init repo failed for %s (%s): %v", project.Name, project.ID, initErr)
	} else if project.RepoURL == nil || strings.TrimSpace(*project.RepoURL) == "" {
		// Set repo_url to the built-in git server
		gitURL := fmt.Sprintf("https://api.otter.camp/git/%s/%s.git", workspaceID, project.ID)
		if _, updateErr := h.DB.ExecContext(ctx, `UPDATE projects SET repo_url = $1 WHERE id = $2 AND org_id = $3`,
			gitURL, project.ID, workspaceID); updateErr != nil {
			log.Printf("[projects] auto-set repo_url failed for %s: %v", project.ID, updateErr)
		} else {
			project.RepoURL = &gitURL
		}
	}

	sendJSON(w, http.StatusCreated, project)
}

// Patch updates mutable project fields.
func (h *ProjectsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	var input struct {
		Name              *string          `json:"name"`
		Description       *string          `json:"description"`
		Status            *string          `json:"status"`
		RepoURL           *string          `json:"repo_url"`
		WorkflowEnabled   *bool            `json:"workflow_enabled"`
		WorkflowSchedule  *json.RawMessage `json:"workflow_schedule"`
		WorkflowTemplate  *json.RawMessage `json:"workflow_template"`
		WorkflowAgentID   *string          `json:"workflow_agent_id"`
		WorkflowLastRunAt *string          `json:"workflow_last_run_at"`
		WorkflowNextRunAt *string          `json:"workflow_next_run_at"`
		WorkflowRunCount  *int             `json:"workflow_run_count"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	existing, err := h.Store.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to get project"})
		return
	}

	updateInput := store.UpdateProjectInput{
		Name:              existing.Name,
		Description:       existing.Description,
		Status:            existing.Status,
		RepoURL:           existing.RepoURL,
		WorkflowEnabled:   existing.WorkflowEnabled,
		WorkflowSchedule:  existing.WorkflowSchedule,
		WorkflowTemplate:  existing.WorkflowTemplate,
		WorkflowAgentID:   existing.WorkflowAgentID,
		WorkflowLastRunAt: existing.WorkflowLastRunAt,
		WorkflowNextRunAt: existing.WorkflowNextRunAt,
		WorkflowRunCount:  existing.WorkflowRunCount,
	}

	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
			return
		}
		updateInput.Name = name
	}
	if input.Description != nil {
		description := strings.TrimSpace(*input.Description)
		if description == "" {
			updateInput.Description = nil
		} else {
			updateInput.Description = &description
		}
	}
	if input.Status != nil {
		status := strings.ToLower(strings.TrimSpace(*input.Status))
		if !isSupportedProjectStatus(status) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "status must be active, archived, or completed"})
			return
		}
		updateInput.Status = status
	}
	if input.RepoURL != nil {
		repoURL := strings.TrimSpace(*input.RepoURL)
		if repoURL == "" {
			updateInput.RepoURL = nil
		} else {
			updateInput.RepoURL = &repoURL
		}
	}
	if input.WorkflowEnabled != nil {
		updateInput.WorkflowEnabled = *input.WorkflowEnabled
	}
	if input.WorkflowSchedule != nil {
		normalized, normalizeErr := normalizeWorkflowPatchJSON(*input.WorkflowSchedule, "workflow_schedule")
		if normalizeErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: normalizeErr.Error()})
			return
		}
		updateInput.WorkflowSchedule = normalized
	}
	if input.WorkflowTemplate != nil {
		normalized, normalizeErr := normalizeWorkflowPatchJSON(*input.WorkflowTemplate, "workflow_template")
		if normalizeErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: normalizeErr.Error()})
			return
		}
		updateInput.WorkflowTemplate = normalized
	}
	if input.WorkflowAgentID != nil {
		workflowAgentID := strings.TrimSpace(*input.WorkflowAgentID)
		if workflowAgentID == "" {
			updateInput.WorkflowAgentID = nil
		} else {
			updateInput.WorkflowAgentID = &workflowAgentID
		}
	}
	if input.WorkflowLastRunAt != nil {
		lastRunAt, parseErr := parseProjectOptionalRFC3339(input.WorkflowLastRunAt, "workflow_last_run_at")
		if parseErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: parseErr.Error()})
			return
		}
		updateInput.WorkflowLastRunAt = lastRunAt
	}
	if input.WorkflowNextRunAt != nil {
		nextRunAt, parseErr := parseProjectOptionalRFC3339(input.WorkflowNextRunAt, "workflow_next_run_at")
		if parseErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: parseErr.Error()})
			return
		}
		updateInput.WorkflowNextRunAt = nextRunAt
	}
	if input.WorkflowRunCount != nil {
		if *input.WorkflowRunCount < 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_run_count must be non-negative"})
			return
		}
		updateInput.WorkflowRunCount = *input.WorkflowRunCount
	}
	if updateInput.WorkflowAgentID != nil && !uuidRegex.MatchString(strings.TrimSpace(*updateInput.WorkflowAgentID)) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_agent_id must be a UUID"})
		return
	}
	if updateInput.WorkflowAgentID != nil {
		var exists bool
		existsErr := h.DB.QueryRowContext(
			r.Context(),
			"SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND org_id = $2)",
			*updateInput.WorkflowAgentID,
			workspaceID,
		).Scan(&exists)
		if existsErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate workflow agent"})
			return
		}
		if !exists {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workflow_agent_id not found in workspace"})
			return
		}
	}

	updated, err := h.Store.Update(ctx, projectID, updateInput)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update project"})
		return
	}

	sendJSON(w, http.StatusOK, updated)
}

// Delete removes a project.
func (h *ProjectsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	if err := h.Store.Delete(ctx, projectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete project"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// UpdateSettings updates project-scoped settings.
func (h *ProjectsHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if !supportsProjectPrimaryAgentColumn(r.Context(), h.DB) {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "project settings migration pending; primary agent not available yet"})
		return
	}

	var req struct {
		PrimaryAgentID *string `json:"primary_agent_id"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	if req.PrimaryAgentID != nil {
		trimmed := strings.TrimSpace(*req.PrimaryAgentID)
		if trimmed == "" {
			req.PrimaryAgentID = nil
		} else {
			req.PrimaryAgentID = &trimmed
		}
	}
	if err := validateOptionalUUID(req.PrimaryAgentID, "primary_agent_id"); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if req.PrimaryAgentID != nil {
		var exists bool
		err := h.DB.QueryRowContext(
			r.Context(),
			"SELECT EXISTS(SELECT 1 FROM agents WHERE id = $1 AND org_id = $2)",
			*req.PrimaryAgentID,
			workspaceID,
		).Scan(&exists)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate primary agent"})
			return
		}
		if !exists {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "primary_agent_id not found in workspace"})
			return
		}
	}

	result, err := h.DB.ExecContext(
		r.Context(),
		`UPDATE projects
		 SET primary_agent_id = $1
		 WHERE id = $2 AND org_id = $3`,
		nullableString(req.PrimaryAgentID),
		projectID,
		workspaceID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update project settings"})
		return
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to confirm project settings update"})
		return
	}
	if rowsAffected == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
		return
	}

	h.Get(w, r)
}

// ListRuns returns workflow run history for a project.
func (h *ProjectsHandler) ListRuns(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	limit := 20
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	if _, err := h.Store.GetByID(ctx, projectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load project"})
		return
	}

	issueStore := store.NewProjectIssueStore(h.DB)
	issues, err := issueStore.ListIssues(ctx, store.ProjectIssueFilter{
		ProjectID: projectID,
		Limit:     limit,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	runs := make([]projectWorkflowRunResponse, 0, len(issues))
	for _, issue := range issues {
		runs = append(runs, workflowRunFromIssue(issue))
	}

	sendJSON(w, http.StatusOK, map[string]any{
		"runs":  runs,
		"total": len(runs),
	})
}

// GetLatestRun returns the latest workflow run issue for a project.
func (h *ProjectsHandler) GetLatestRun(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	if _, err := h.Store.GetByID(ctx, projectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load project"})
		return
	}

	issueStore := store.NewProjectIssueStore(h.DB)
	issues, err := issueStore.ListIssues(ctx, store.ProjectIssueFilter{
		ProjectID: projectID,
		Limit:     1,
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}
	if len(issues) == 0 {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "no workflow runs found"})
		return
	}

	sendJSON(w, http.StatusOK, workflowRunFromIssue(issues[0]))
}

// TriggerRun creates a workflow run issue immediately for a project.
func (h *ProjectsHandler) TriggerRun(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project ID is required"})
		return
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(r.URL.Query().Get("org_id"))
	}
	if workspaceID == "" {
		if identity, err := requireSessionIdentity(r.Context(), h.DB, r); err == nil {
			workspaceID = identity.OrgID
		}
	}
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)
	project, err := h.Store.GetByID(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		if errors.Is(err, store.ErrForbidden) {
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load project"})
		return
	}

	runNumber := project.WorkflowRunCount + 1
	agentName := workflowAgentDisplayName(ctx, h.DB, project.WorkflowAgentID)
	template := workflowTemplateForProject(project, runNumber, agentName)

	issueStore := store.NewProjectIssueStore(h.DB)
	issueBody := strings.TrimSpace(template.Body)
	var issueBodyPtr *string
	if issueBody != "" {
		issueBodyPtr = &issueBody
	}
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:    project.ID,
		Title:        template.TitlePattern,
		Body:         issueBodyPtr,
		Origin:       "local",
		Priority:     template.Priority,
		OwnerAgentID: project.WorkflowAgentID,
		WorkStatus:   store.IssueWorkStatusQueued,
		State:        "open",
	})
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	if issue.OwnerAgentID != nil {
		if _, err := issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
			IssueID: issue.ID,
			AgentID: *issue.OwnerAgentID,
			Role:    "owner",
		}); err != nil {
			handleIssueStoreError(w, err)
			return
		}
	}

	if len(template.Labels) > 0 {
		labelStore := store.NewLabelStore(h.DB)
		for _, raw := range template.Labels {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			label, err := labelStore.GetByName(ctx, name)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					label, err = labelStore.Create(ctx, name, "")
				}
				if err != nil {
					sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create workflow label"})
					return
				}
			}
			if err := labelStore.AddToIssue(ctx, issue.ID, label.ID); err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to apply workflow labels"})
				return
			}
		}
	}

	now := time.Now().UTC()
	updateInput := store.UpdateProjectInput{
		Name:              project.Name,
		Description:       project.Description,
		Status:            project.Status,
		RepoURL:           project.RepoURL,
		WorkflowEnabled:   project.WorkflowEnabled,
		WorkflowSchedule:  project.WorkflowSchedule,
		WorkflowTemplate:  project.WorkflowTemplate,
		WorkflowAgentID:   project.WorkflowAgentID,
		WorkflowLastRunAt: &now,
		WorkflowNextRunAt: project.WorkflowNextRunAt,
		WorkflowRunCount:  runNumber,
	}
	if _, err := h.Store.Update(ctx, project.ID, updateInput); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update workflow run metadata"})
		return
	}

	sendJSON(w, http.StatusCreated, map[string]any{
		"run":        workflowRunFromIssue(*issue),
		"run_number": runNumber,
	})
}

func workflowTemplateForProject(project *store.Project, runNumber int, agentName string) projectWorkflowTemplate {
	template := projectWorkflowTemplate{
		TitlePattern: strings.TrimSpace(project.Name) + " — {{datetime}}",
		Body:         "",
		Priority:     store.IssuePriorityP3,
		Labels:       []string{"automated"},
		Pipeline:     "none",
	}

	if len(project.WorkflowTemplate) > 0 {
		_ = json.Unmarshal(project.WorkflowTemplate, &template)
	}
	if strings.TrimSpace(template.TitlePattern) == "" {
		template.TitlePattern = strings.TrimSpace(project.Name) + " — {{datetime}}"
	}
	if strings.TrimSpace(template.Priority) == "" {
		template.Priority = store.IssuePriorityP3
	}
	if len(template.Labels) == 0 {
		template.Labels = []string{"automated"}
	}

	vars := map[string]string{
		"date":       time.Now().Format("Jan 2, 2006"),
		"datetime":   time.Now().Format(time.RFC3339),
		"run_number": strconv.Itoa(runNumber),
		"agent_name": strings.TrimSpace(agentName),
	}
	template.TitlePattern = renderWorkflowTemplateText(template.TitlePattern, vars)
	template.Body = renderWorkflowTemplateText(template.Body, vars)
	return template
}

func renderWorkflowTemplateText(value string, vars map[string]string) string {
	updated := value
	for key, replacement := range vars {
		updated = strings.ReplaceAll(updated, "{{"+key+"}}", replacement)
	}
	return strings.TrimSpace(updated)
}

func workflowAgentDisplayName(ctx context.Context, db *sql.DB, agentID *string) string {
	if agentID == nil || strings.TrimSpace(*agentID) == "" || db == nil {
		return ""
	}
	var displayName string
	err := db.QueryRowContext(
		ctx,
		"SELECT COALESCE(display_name, name, '') FROM agents WHERE id = $1",
		*agentID,
	).Scan(&displayName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(displayName)
}

func workflowRunFromIssue(issue store.ProjectIssue) projectWorkflowRunResponse {
	run := projectWorkflowRunResponse{
		ID:           issue.ID,
		ProjectID:    issue.ProjectID,
		IssueNumber:  issue.IssueNumber,
		Title:        issue.Title,
		State:        issue.State,
		WorkStatus:   issue.WorkStatus,
		Priority:     issue.Priority,
		OwnerAgentID: issue.OwnerAgentID,
		CreatedAt:    issue.CreatedAt.UTC().Format(time.RFC3339),
	}
	if issue.ClosedAt != nil {
		closedAt := issue.ClosedAt.UTC().Format(time.RFC3339)
		run.ClosedAt = &closedAt
	}
	return run
}

func isSupportedProjectStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "archived", "completed":
		return true
	default:
		return false
	}
}

func supportsProjectPrimaryAgentColumn(ctx context.Context, db *sql.DB) bool {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'projects'
			  AND column_name = 'primary_agent_id'
		)
	`).Scan(&exists)
	return err == nil && exists
}

func supportsProjectWorkflowColumns(ctx context.Context, db *sql.DB) bool {
	var exists bool
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'projects'
			  AND column_name = 'workflow_enabled'
		)
	`).Scan(&exists)
	return err == nil && exists
}

func parseProjectLabelFilterIDs(rawValues []string) ([]string, error) {
	if len(rawValues) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(rawValues))
	labelIDs := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if !uuidRegex.MatchString(trimmed) {
			return nil, fmt.Errorf("invalid label filter")
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		labelIDs = append(labelIDs, trimmed)
	}
	return labelIDs, nil
}

func listProjectsQuery(usePrimaryAgent bool, useWorkflow bool, workflowOnly bool) string {
	workflowSelect := ""
	workflowFilter := ""
	if useWorkflow {
		workflowSelect = `,
			p.workflow_enabled,
			p.workflow_schedule,
			p.workflow_template,
			p.workflow_agent_id,
			p.workflow_last_run_at,
			p.workflow_next_run_at,
			p.workflow_run_count`
		if workflowOnly {
			workflowFilter = " AND p.workflow_enabled = true"
		}
	}

	leadJoin := ""
	leadSelect := ""
	if usePrimaryAgent {
		leadJoin = "LEFT JOIN agents a ON a.id = p.primary_agent_id"
		leadSelect = ", p.primary_agent_id, COALESCE(a.display_name, '') as lead"
	}

	return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
		COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at` + leadSelect + workflowSelect + `,
		COALESCE(t.task_count, 0) as task_count,
		COALESCE(t.completed_count, 0) as completed_count
		FROM projects p
		` + leadJoin + `
		LEFT JOIN (
			SELECT project_id,
				COUNT(*) as task_count,
				COUNT(*) FILTER (WHERE status = 'done') as completed_count
			FROM tasks
			WHERE org_id = $1 AND project_id IS NOT NULL
			GROUP BY project_id
		) t ON t.project_id = p.id
		WHERE p.org_id = $1` + workflowFilter + ` ORDER BY p.created_at DESC`
}

func getProjectQuery(usePrimaryAgent bool, useWorkflow bool) string {
	workflowSelect := ""
	if useWorkflow {
		workflowSelect = `,
			p.workflow_enabled,
			p.workflow_schedule,
			p.workflow_template,
			p.workflow_agent_id,
			p.workflow_last_run_at,
			p.workflow_next_run_at,
			p.workflow_run_count`
	}

	leadJoin := ""
	leadSelect := ""
	if usePrimaryAgent {
		leadJoin = "LEFT JOIN agents a ON a.id = p.primary_agent_id"
		leadSelect = ", p.primary_agent_id, COALESCE(a.display_name, '') as lead"
	}

	return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
		COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at` + leadSelect + workflowSelect + `,
		COALESCE(t.task_count, 0) as task_count,
		COALESCE(t.completed_count, 0) as completed_count
		FROM projects p
		` + leadJoin + `
		LEFT JOIN (
			SELECT project_id,
				COUNT(*) as task_count,
				COUNT(*) FILTER (WHERE status = 'done') as completed_count
			FROM tasks
			WHERE org_id = $2 AND project_id IS NOT NULL
			GROUP BY project_id
		) t ON t.project_id = p.id
		WHERE p.id = $1 AND p.org_id = $2`
}

func scanProjectAPIResponse(scanner interface{ Scan(...any) error }, usePrimaryAgent bool, useWorkflow bool) (projectAPIResponse, error) {
	var p projectAPIResponse
	var createdAt interface{}
	var primaryAgentID sql.NullString
	var workflowSchedule []byte
	var workflowTemplate []byte
	var workflowAgentID sql.NullString
	var workflowLastRunAt sql.NullTime
	var workflowNextRunAt sql.NullTime

	baseDest := []any{
		&p.ID,
		&p.OrgID,
		&p.Name,
		&p.Description,
		&p.RepoURL,
		&p.Status,
		&createdAt,
	}
	if usePrimaryAgent {
		baseDest = append(baseDest, &primaryAgentID, &p.Lead)
	}
	if useWorkflow {
		baseDest = append(
			baseDest,
			&p.WorkflowEnabled,
			&workflowSchedule,
			&workflowTemplate,
			&workflowAgentID,
			&workflowLastRunAt,
			&workflowNextRunAt,
			&p.WorkflowRunCount,
		)
	}
	baseDest = append(baseDest, &p.TaskCount, &p.CompletedCount)

	if err := scanner.Scan(baseDest...); err != nil {
		return p, err
	}

	if primaryAgentID.Valid {
		p.PrimaryAgentID = &primaryAgentID.String
	}
	if useWorkflow {
		if len(workflowSchedule) > 0 {
			p.WorkflowSchedule = append(json.RawMessage(nil), workflowSchedule...)
		}
		if len(workflowTemplate) > 0 {
			p.WorkflowTemplate = append(json.RawMessage(nil), workflowTemplate...)
		}
		if workflowAgentID.Valid {
			p.WorkflowAgentID = &workflowAgentID.String
		}
		if workflowLastRunAt.Valid {
			formatted := workflowLastRunAt.Time.UTC().Format(time.RFC3339)
			p.WorkflowLastRunAt = &formatted
		}
		if workflowNextRunAt.Valid {
			formatted := workflowNextRunAt.Time.UTC().Format(time.RFC3339)
			p.WorkflowNextRunAt = &formatted
		}
	}

	return p, nil
}

func parseWorkflowFilter(raw string) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(trimmed)
	if err != nil {
		return false, fmt.Errorf("workflow filter must be true or false")
	}
	return parsed, nil
}

func normalizeWorkflowPatchJSON(value json.RawMessage, fieldName string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	raw := json.RawMessage(trimmed)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s must be valid JSON", fieldName)
	}
	return raw, nil
}

func parseProjectOptionalRFC3339(value *string, fieldName string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must be RFC3339 timestamp", fieldName)
	}
	utc := parsed.UTC()
	return &utc, nil
}
