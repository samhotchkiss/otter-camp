package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

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
	ID             string  `json:"id"`
	OrgID          string  `json:"org_id,omitempty"`
	Name           string  `json:"name"`
	Description    string  `json:"description,omitempty"`
	RepoURL        string  `json:"repo_url,omitempty"`
	Status         string  `json:"status"`
	Lead           string  `json:"lead,omitempty"`
	PrimaryAgentID *string `json:"primary_agent_id,omitempty"`
	CreatedAt      string  `json:"created_at,omitempty"`
	TaskCount      int     `json:"taskCount"`
	CompletedCount int     `json:"completedCount"`
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

	usePrimaryAgent := supportsProjectPrimaryAgentColumn(r.Context(), h.DB)
	rows, err := h.DB.QueryContext(r.Context(), listProjectsQuery(usePrimaryAgent), workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list projects"})
		return
	}
	defer rows.Close()

	projects := make([]projectAPIResponse, 0)
	for rows.Next() {
		var p projectAPIResponse
		var createdAt interface{}
		if usePrimaryAgent {
			var primaryAgentID sql.NullString
			if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &createdAt, &primaryAgentID, &p.Lead, &p.TaskCount, &p.CompletedCount); err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to parse projects"})
				return
			}
			if primaryAgentID.Valid {
				p.PrimaryAgentID = &primaryAgentID.String
			}
		} else {
			if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &createdAt, &p.TaskCount, &p.CompletedCount); err != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to parse projects"})
				return
			}
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list projects"})
		return
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

	usePrimaryAgent := supportsProjectPrimaryAgentColumn(r.Context(), h.DB)

	var p projectAPIResponse
	var createdAt interface{}
	var err error
	if usePrimaryAgent {
		var primaryAgentID sql.NullString
		err = h.DB.QueryRowContext(r.Context(), getProjectQuery(true), projectID, workspaceID).Scan(
			&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &createdAt, &primaryAgentID, &p.Lead, &p.TaskCount, &p.CompletedCount)
		if primaryAgentID.Valid {
			p.PrimaryAgentID = &primaryAgentID.String
		}
	} else {
		err = h.DB.QueryRowContext(r.Context(), getProjectQuery(false), projectID, workspaceID).Scan(
			&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &createdAt, &p.TaskCount, &p.CompletedCount)
	}

	if err != nil {
		if err == sql.ErrNoRows {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to get project"})
		return
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
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Status      string  `json:"status"`
		RepoURL     *string `json:"repo_url"`
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

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, workspaceID)

	project, err := h.Store.Create(ctx, store.CreateProjectInput{
		Name:        input.Name,
		Description: input.Description,
		Status:      input.Status,
		RepoURL:     input.RepoURL,
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
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Status      *string `json:"status"`
		RepoURL     *string `json:"repo_url"`
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
		Name:        existing.Name,
		Description: existing.Description,
		Status:      existing.Status,
		RepoURL:     existing.RepoURL,
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

func listProjectsQuery(usePrimaryAgent bool) string {
	if usePrimaryAgent {
		return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
			COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at,
			p.primary_agent_id, COALESCE(a.display_name, '') as lead,
			COALESCE(t.task_count, 0) as task_count,
			COALESCE(t.completed_count, 0) as completed_count
			FROM projects p
			LEFT JOIN agents a ON a.id = p.primary_agent_id
			LEFT JOIN (
				SELECT project_id,
					COUNT(*) as task_count,
					COUNT(*) FILTER (WHERE status = 'done') as completed_count
				FROM tasks
				WHERE org_id = $1 AND project_id IS NOT NULL
				GROUP BY project_id
			) t ON t.project_id = p.id
			WHERE p.org_id = $1 ORDER BY p.created_at DESC`
	}

	return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
		COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at,
		COALESCE(t.task_count, 0) as task_count,
		COALESCE(t.completed_count, 0) as completed_count
		FROM projects p
		LEFT JOIN (
			SELECT project_id,
				COUNT(*) as task_count,
				COUNT(*) FILTER (WHERE status = 'done') as completed_count
			FROM tasks
			WHERE org_id = $1 AND project_id IS NOT NULL
			GROUP BY project_id
		) t ON t.project_id = p.id
		WHERE p.org_id = $1 ORDER BY p.created_at DESC`
}

func getProjectQuery(usePrimaryAgent bool) string {
	if usePrimaryAgent {
		return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
			COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at,
			p.primary_agent_id, COALESCE(a.display_name, '') as lead,
			COALESCE(t.task_count, 0) as task_count,
			COALESCE(t.completed_count, 0) as completed_count
			FROM projects p
			LEFT JOIN agents a ON a.id = p.primary_agent_id
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

	return `SELECT p.id, p.org_id, p.name, COALESCE(p.description, '') as description,
		COALESCE(p.repo_url, '') as repo_url, COALESCE(p.status, 'active') as status, p.created_at,
		COALESCE(t.task_count, 0) as task_count,
		COALESCE(t.completed_count, 0) as completed_count
		FROM projects p
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
