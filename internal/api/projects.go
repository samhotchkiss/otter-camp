package api

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Demo projects for when database is unavailable
var demoProjects = []map[string]interface{}{
	{
		"id":          "proj-1",
		"name":        "Pearl Proxy",
		"description": "Memory and routing infrastructure",
		"status":      "active",
		"repo_url":    "https://github.com/The-Trawl/pearl",
		"lead":        "Derek",
	},
	{
		"id":          "proj-2",
		"name":        "Otter Camp",
		"description": "Task management for AI-assisted workflows",
		"status":      "active",
		"repo_url":    "https://github.com/samhotchkiss/otter-camp",
		"lead":        "Derek",
	},
	{
		"id":          "proj-3",
		"name":        "ItsAlive",
		"description": "Static site deployment platform",
		"status":      "active",
		"repo_url":    "https://github.com/The-Trawl/itsalive",
		"lead":        "Ivy",
	},
	{
		"id":          "proj-4",
		"name":        "Three Stones",
		"description": "Educational content and presentations",
		"status":      "archived",
		"repo_url":    nil,
		"lead":        "Stone",
	},
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

	if workspaceID == "" {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"projects": demoProjects,
			"total":    len(demoProjects),
		})
		return
	}

	// Query database directly (bypassing RLS for reliability)
	query := `SELECT id, org_id, name, COALESCE(description, '') as description, 
		COALESCE(repo_url, '') as repo_url, COALESCE(status, 'active') as status, 
		COALESCE(lead, '') as lead, created_at 
		FROM projects WHERE org_id = $1 ORDER BY created_at DESC`
	
	rows, err := h.DB.QueryContext(r.Context(), query, workspaceID)
	if err != nil {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"projects": demoProjects,
			"total":    len(demoProjects),
		})
		return
	}
	defer rows.Close()

	type Project struct {
		ID          string `json:"id"`
		OrgID       string `json:"org_id,omitempty"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		RepoURL     string `json:"repo_url,omitempty"`
		Status      string `json:"status"`
		Lead        string `json:"lead,omitempty"`
		CreatedAt   string `json:"created_at,omitempty"`
	}

	projects := make([]Project, 0)
	for rows.Next() {
		var p Project
		var createdAt interface{}
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &p.Lead, &createdAt); err != nil {
			continue
		}
		projects = append(projects, p)
	}

	if len(projects) == 0 {
		sendJSON(w, http.StatusOK, map[string]interface{}{
			"projects": demoProjects,
			"total":    len(demoProjects),
		})
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
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	// Query database directly (bypassing RLS for reliability)
	query := `SELECT id, org_id, name, COALESCE(description, '') as description, 
		COALESCE(repo_url, '') as repo_url, COALESCE(status, 'active') as status, 
		COALESCE(lead, '') as lead, created_at 
		FROM projects WHERE id = $1 AND org_id = $2`
	
	type Project struct {
		ID          string `json:"id"`
		OrgID       string `json:"org_id,omitempty"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		RepoURL     string `json:"repo_url,omitempty"`
		Status      string `json:"status"`
		Lead        string `json:"lead,omitempty"`
		CreatedAt   string `json:"created_at,omitempty"`
	}

	var p Project
	var createdAt interface{}
	err := h.DB.QueryRowContext(r.Context(), query, projectID, workspaceID).Scan(
		&p.ID, &p.OrgID, &p.Name, &p.Description, &p.RepoURL, &p.Status, &p.Lead, &createdAt)
	
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

	sendJSON(w, http.StatusCreated, project)
}
