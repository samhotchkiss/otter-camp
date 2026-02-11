package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type AdminAgentsHandler struct {
	DB              *sql.DB
	Store           *store.AgentStore
	ProjectStore    *store.ProjectStore
	ProjectRepos    *store.ProjectRepoStore
	OpenClawHandler openClawConnectionStatus
	EventStore      *store.ConnectionEventStore

	writeTemplatesFn func(ctx context.Context, repoPath, slot string, input adminAgentTemplateInput) error
}

type adminAgentSummary struct {
	ID               string `json:"id"`
	WorkspaceAgentID string `json:"workspace_agent_id"`
	Name             string `json:"name"`
	Status           string `json:"status"`
	Model            string `json:"model,omitempty"`
	HeartbeatEvery   string `json:"heartbeat_every,omitempty"`
	Channel          string `json:"channel,omitempty"`
	SessionKey       string `json:"session_key,omitempty"`
	LastSeen         string `json:"last_seen,omitempty"`
}

type adminAgentSyncDetails struct {
	CurrentTask   string     `json:"current_task,omitempty"`
	ContextTokens int        `json:"context_tokens,omitempty"`
	TotalTokens   int        `json:"total_tokens,omitempty"`
	LastSeen      string     `json:"last_seen,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

type adminAgentsListResponse struct {
	Agents []adminAgentSummary `json:"agents"`
	Total  int                 `json:"total"`
}

type adminAgentDetailResponse struct {
	Agent adminAgentSummary      `json:"agent"`
	Sync  *adminAgentSyncDetails `json:"sync,omitempty"`
}

type adminAgentFilesListResponse struct {
	ProjectID string             `json:"project_id,omitempty"`
	Ref       string             `json:"ref"`
	Path      string             `json:"path"`
	Entries   []projectTreeEntry `json:"entries"`
}

type adminAgentCreateRequest struct {
	DisplayName       string `json:"displayName"`
	DisplayNameLegacy string `json:"display_name,omitempty"`
	ProfileID         string `json:"profileId,omitempty"`
	Soul              string `json:"soul,omitempty"`
	Identity          string `json:"identity,omitempty"`
	Model             string `json:"model"`
	Avatar            string `json:"avatar,omitempty"`
}

type adminAgentTemplateInput struct {
	DisplayName string
	Soul        string
	Identity    string
	Avatar      string
}

type builtInAgentProfileTemplate struct {
	ID       string
	Soul     string
	Identity string
	Avatar   string
}

type adminAgentRow struct {
	WorkspaceAgentID string
	Slug             string
	DisplayName      string
	WorkspaceStatus  string
	HeartbeatEvery   sql.NullString
	SyncName         sql.NullString
	SyncModel        sql.NullString
	SyncChannel      sql.NullString
	SyncSessionKey   sql.NullString
	SyncLastSeen     sql.NullString
	SyncCurrentTask  sql.NullString
	SyncStatus       sql.NullString
	SyncUpdatedAt    sql.NullTime
	ContextTokens    sql.NullInt64
	TotalTokens      sql.NullInt64
}

var errAdminAgentForbidden = errors.New("agent belongs to a different workspace")

var (
	errAgentFilesProjectNotConfigured = errors.New("agent files project is not configured")
	memoryDatePattern                 = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	agentSlotPattern                  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	errAgentFilesDirMissing           = errors.New("agent files directory is missing")
	errRetiredAgentFilesDirMissing    = errors.New("retired agent files directory is missing")
	errAgentFilesDirAlreadyExists     = errors.New("agent files directory already exists")
	errRetiredAgentFilesDirExists     = errors.New("retired agent files directory already exists")
)

const agentFilesProjectName = "Agent Files"

var builtInAgentProfiles = map[string]builtInAgentProfileTemplate{
	"marcus": {
		ID: "marcus",
		Soul: strings.TrimSpace(`
# SOUL.md - {{name}}

- Voice: Calm, structured, and decisive.
- Values: Clarity, ownership, and momentum.
- Working style: Turns ambiguity into plans and keeps priorities moving.
`),
		Identity: strings.TrimSpace(`
# IDENTITY.md - {{name}}

- **Name:** {{name}}
- **Role:** Chief of Staff
- **Vibe:** Calm operator, detail-aware, strategic.
- **Avatar:** /assets/agent-profiles/marcus.webp
`),
		Avatar: "/assets/agent-profiles/marcus.webp",
	},
	"rory": {
		ID: "rory",
		Soul: strings.TrimSpace(`
# SOUL.md - {{name}}

- Voice: Direct, precise, and evidence-first.
- Values: Correctness, readability, and long-term maintainability.
- Working style: Finds edge cases, demands test coverage, and removes ambiguity.
`),
		Identity: strings.TrimSpace(`
# IDENTITY.md - {{name}}

- **Name:** {{name}}
- **Role:** Code Reviewer
- **Vibe:** Opinionated, sharp, and quality obsessed.
- **Avatar:** /assets/agent-profiles/rory.webp
`),
		Avatar: "/assets/agent-profiles/rory.webp",
	},
	"sage": {
		ID: "sage",
		Soul: strings.TrimSpace(`
# SOUL.md - {{name}}

- Voice: Curious, methodical, and grounded.
- Values: Truth-seeking, source quality, and synthesis.
- Working style: Explores deeply and cites what matters.
`),
		Identity: strings.TrimSpace(`
# IDENTITY.md - {{name}}

- **Name:** {{name}}
- **Role:** Research Analyst
- **Vibe:** Thorough, calm, and insight-driven.
- **Avatar:** /assets/agent-profiles/sage.webp
`),
		Avatar: "/assets/agent-profiles/sage.webp",
	},
}

func (h *AdminAgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.DB == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	rows, err := h.listRows(r.Context(), workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin agents"})
		return
	}

	items := make([]adminAgentSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, rowToAgentSummary(row))
	}

	sendJSON(w, http.StatusOK, adminAgentsListResponse{
		Agents: items,
		Total:  len(items),
	})
}

func (h *AdminAgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.DB == nil || h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	identifier := strings.TrimSpace(chi.URLParam(r, "id"))
	if identifier == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}

	row, err := h.getRow(r.Context(), workspaceID, identifier)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "agent not found"})
		case errors.Is(err, errAdminAgentForbidden):
			sendJSON(w, http.StatusForbidden, errorResponse{Error: "agent belongs to a different workspace"})
		default:
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin agent"})
		}
		return
	}

	payload := adminAgentDetailResponse{
		Agent: rowToAgentSummary(*row),
	}
	if row.SyncUpdatedAt.Valid || strings.TrimSpace(row.SyncCurrentTask.String) != "" || row.ContextTokens.Valid || row.TotalTokens.Valid {
		var updatedAt *time.Time
		if row.SyncUpdatedAt.Valid {
			ts := row.SyncUpdatedAt.Time.UTC()
			updatedAt = &ts
		}
		payload.Sync = &adminAgentSyncDetails{
			CurrentTask:   strings.TrimSpace(row.SyncCurrentTask.String),
			ContextTokens: int(row.ContextTokens.Int64),
			TotalTokens:   int(row.TotalTokens.Int64),
			LastSeen:      strings.TrimSpace(row.SyncLastSeen.String),
			UpdatedAt:     updatedAt,
		}
	}

	sendJSON(w, http.StatusOK, payload)
}

func (h *AdminAgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}
	if h.DB == nil || h.Store == nil || h.ProjectStore == nil || h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	var req adminAgentCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	req.DisplayName = strings.TrimSpace(firstNonEmpty(req.DisplayName, req.DisplayNameLegacy))
	req.Model = strings.TrimSpace(req.Model)
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	req.Soul = strings.TrimSpace(req.Soul)
	req.Identity = strings.TrimSpace(req.Identity)
	req.Avatar = strings.TrimSpace(req.Avatar)

	if req.DisplayName == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "displayName is required"})
		return
	}
	if req.Model == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "model is required"})
		return
	}

	slot, err := resolveAvailableAgentSlot(agentSlotFromDisplayName(req.DisplayName), func(candidate string) (bool, error) {
		existing, lookupErr := h.Store.GetBySlug(r.Context(), candidate)
		if lookupErr == nil && existing != nil {
			return true, nil
		}
		if errors.Is(lookupErr, store.ErrNotFound) {
			return false, nil
		}
		if lookupErr != nil {
			return false, lookupErr
		}
		return false, nil
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate agent slot"})
		return
	}

	repoPath, _, _, _, err := h.ensureAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}

	createdAgent, err := h.Store.Create(r.Context(), store.CreateAgentInput{
		Slug:        slot,
		DisplayName: req.DisplayName,
		Status:      "active",
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create agent"})
		return
	}

	writeTemplates := h.writeTemplatesFn
	if writeTemplates == nil {
		writeTemplates = h.writeAgentTemplates
	}
	templateInput, err := buildCreateAgentTemplateInput(req)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := writeTemplates(r.Context(), repoPath, slot, templateInput); err != nil {
		_ = h.Store.Delete(r.Context(), createdAgent.ID)
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to scaffold agent files"})
		return
	}

	configPatch, err := buildCreateAgentConfigPatch(slot, req.Model)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to build config patch"})
		return
	}

	dispatcher := &AdminConnectionsHandler{
		DB:              h.DB,
		OpenClawHandler: h.OpenClawHandler,
		EventStore:      h.EventStore,
	}
	dispatcher.dispatchAdminCommand(
		w,
		r,
		adminCommandActionConfigPatch,
		adminCommandDispatchInput{
			ConfigPatch: configPatch,
			Confirm:     true,
			DryRun:      false,
		},
	)
}

func (h *AdminAgentsHandler) Retire(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	repoPath, _, _, _, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if err := h.moveAgentFilesAndCommit(
		r.Context(),
		repoPath,
		path.Join("agents", row.Slug),
		path.Join("agents", "_retired", row.Slug),
		fmt.Sprintf("Retire agent %s", row.Slug),
		errAgentFilesDirMissing,
		errRetiredAgentFilesDirExists,
	); err != nil {
		h.writeLifecycleMoveError(w, err)
		return
	}
	if err := h.updateAgentStatus(r.Context(), workspaceID, row.Slug, "retired"); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update agent status"})
		return
	}
	h.dispatchAgentEnablePatch(w, r, row.Slug, false)
}

func (h *AdminAgentsHandler) Reactivate(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	repoPath, _, _, _, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if err := h.moveAgentFilesAndCommit(
		r.Context(),
		repoPath,
		path.Join("agents", "_retired", row.Slug),
		path.Join("agents", row.Slug),
		fmt.Sprintf("Reactivate agent %s", row.Slug),
		errRetiredAgentFilesDirMissing,
		errAgentFilesDirAlreadyExists,
	); err != nil {
		h.writeLifecycleMoveError(w, err)
		return
	}
	if err := h.updateAgentStatus(r.Context(), workspaceID, row.Slug, "active"); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update agent status"})
		return
	}
	h.dispatchAgentEnablePatch(w, r, row.Slug, true)
}

func (h *AdminAgentsHandler) ListFiles(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	relativePath, err := normalizeRepositoryPath(r.URL.Query().Get("path"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	root := path.Join("agents", row.Slug)
	targetPath := root
	if relativePath != "" {
		targetPath = path.Join(root, relativePath)
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	repoPath, projectID, repoMode, defaultRef, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	resolvedRef, output, err := readTreeListingForBrowse(r.Context(), repoPath, repoMode, ref, targetPath, !refProvided)
	if err != nil {
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}
	entries, err := parseTreeEntries(output, targetPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to parse tree listing"})
		return
	}
	entries = trimAgentRootEntries(entries, root)

	responsePath := "/"
	if relativePath != "" {
		responsePath = "/" + relativePath
	}
	sendJSON(w, http.StatusOK, adminAgentFilesListResponse{
		ProjectID: projectID,
		Ref:       resolvedRef,
		Path:      responsePath,
		Entries:   entries,
	})
}

func (h *AdminAgentsHandler) GetFile(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	filePath, err := normalizeRepositoryPath(chi.URLParam(r, "path"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if filePath == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path must point to a file"})
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	repoPath, _, repoMode, defaultRef, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	root := path.Join("agents", row.Slug)
	targetPath := path.Join(root, filePath)
	resolvedRef, contentBytes, err := readBlobForBrowse(r.Context(), repoPath, repoMode, ref, targetPath, !refProvided)
	if err != nil {
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}

	encoding := "base64"
	content := base64.StdEncoding.EncodeToString(contentBytes)
	if utf8.Valid(contentBytes) && !bytes.Contains(contentBytes, []byte{0}) {
		encoding = "utf-8"
		content = string(contentBytes)
	}

	sendJSON(w, http.StatusOK, projectBlobResponse{
		Ref:      resolvedRef,
		Path:     "/" + filePath,
		Content:  content,
		Size:     int64(len(contentBytes)),
		Encoding: encoding,
	})
}

func (h *AdminAgentsHandler) ListMemoryFiles(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	repoPath, _, repoMode, defaultRef, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	root := path.Join("agents", row.Slug)
	memoryRoot := path.Join(root, "memory")
	resolvedRef, output, err := readTreeListingForBrowse(r.Context(), repoPath, repoMode, ref, memoryRoot, !refProvided)
	if err != nil {
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}
	entries, err := parseTreeEntries(output, memoryRoot)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to parse tree listing"})
		return
	}
	entries = trimAgentRootEntries(entries, memoryRoot)

	sendJSON(w, http.StatusOK, adminAgentFilesListResponse{
		Ref:     resolvedRef,
		Path:    "/memory",
		Entries: entries,
	})
}

func (h *AdminAgentsHandler) GetMemoryFileByDate(w http.ResponseWriter, r *http.Request) {
	row, workspaceID, err := h.resolveFileAgentRow(r)
	if err != nil {
		h.writeAgentLookupError(w, err)
		return
	}

	date := strings.TrimSpace(chi.URLParam(r, "date"))
	if !memoryDatePattern.MatchString(date) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "date must use YYYY-MM-DD"})
		return
	}
	if _, parseErr := time.Parse("2006-01-02", date); parseErr != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "date must use YYYY-MM-DD"})
		return
	}

	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	refProvided := ref != ""
	repoPath, _, repoMode, defaultRef, err := h.resolveAgentFilesRepository(r.Context(), workspaceID)
	if err != nil {
		h.writeAgentFilesResolveError(w, err)
		return
	}
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		ref = "HEAD"
	}

	targetPath := path.Join("agents", row.Slug, "memory", date+".md")
	resolvedRef, contentBytes, err := readBlobForBrowse(r.Context(), repoPath, repoMode, ref, targetPath, !refProvided)
	if err != nil {
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
		return
	}

	encoding := "base64"
	content := base64.StdEncoding.EncodeToString(contentBytes)
	if utf8.Valid(contentBytes) && !bytes.Contains(contentBytes, []byte{0}) {
		encoding = "utf-8"
		content = string(contentBytes)
	}

	sendJSON(w, http.StatusOK, projectBlobResponse{
		Ref:      resolvedRef,
		Path:     fmt.Sprintf("/memory/%s.md", date),
		Content:  content,
		Size:     int64(len(contentBytes)),
		Encoding: encoding,
	})
}

func (h *AdminAgentsHandler) listRows(ctx context.Context, workspaceID string) ([]adminAgentRow, error) {
	query := `
		SELECT
			a.id::text AS workspace_agent_id,
			a.slug,
			COALESCE(a.display_name, '') AS display_name,
			COALESCE(a.status, '') AS workspace_status,
			c.heartbeat_every,
			s.name,
			s.model,
			s.channel,
			s.session_key,
			s.last_seen,
			s.current_task,
			s.status,
			s.updated_at,
			s.context_tokens,
			s.total_tokens
		FROM agents a
		LEFT JOIN openclaw_agent_configs c ON c.id = a.slug
		LEFT JOIN agent_sync_state s ON s.id = a.slug AND s.org_id = a.org_id
		WHERE a.org_id = $1
		ORDER BY LOWER(COALESCE(NULLIF(s.name, ''), NULLIF(a.display_name, ''), a.slug)) ASC, a.slug ASC`
	rows, err := h.DB.QueryContext(ctx, query, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]adminAgentRow, 0, 16)
	for rows.Next() {
		row, err := scanAdminAgentRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *AdminAgentsHandler) getRow(ctx context.Context, workspaceID, identifier string) (*adminAgentRow, error) {
	query := `
		SELECT
			a.id::text AS workspace_agent_id,
			a.slug,
			COALESCE(a.display_name, '') AS display_name,
			COALESCE(a.status, '') AS workspace_status,
			c.heartbeat_every,
			s.name,
			s.model,
			s.channel,
			s.session_key,
			s.last_seen,
			s.current_task,
			s.status,
			s.updated_at,
			s.context_tokens,
			s.total_tokens
		FROM agents a
		LEFT JOIN openclaw_agent_configs c ON c.id = a.slug
		LEFT JOIN agent_sync_state s ON s.id = a.slug AND s.org_id = a.org_id
		WHERE a.org_id = $1
		  AND (a.id::text = $2 OR a.slug = $2)
		LIMIT 1`
	row, err := scanAdminAgentRow(h.DB.QueryRowContext(ctx, query, workspaceID, identifier))
	if err == nil {
		return &row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var exists bool
	if err := h.DB.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM agents WHERE id::text = $1 OR slug = $1)`,
		identifier,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errAdminAgentForbidden
	}
	return nil, store.ErrNotFound
}

func (h *AdminAgentsHandler) resolveAgentFilesRepository(
	ctx context.Context,
	workspaceID string,
) (string, string, gitRepoMode, string, error) {
	if h.ProjectStore == nil || h.ProjectRepos == nil {
		return "", "", "", "", errAgentFilesProjectNotConfigured
	}

	agentFilesProject, err := h.ProjectStore.GetByName(ctx, agentFilesProjectName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", "", "", "", errAgentFilesProjectNotConfigured
		}
		if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNoWorkspace) {
			return "", "", "", "", errAdminAgentForbidden
		}
		return "", "", "", "", err
	}
	if strings.TrimSpace(agentFilesProject.OrgID) != strings.TrimSpace(workspaceID) {
		return "", "", "", "", errAdminAgentForbidden
	}

	treeHandler := &ProjectTreeHandler{
		ProjectStore: h.ProjectStore,
		ProjectRepos: h.ProjectRepos,
	}
	repoPath, repoMode, defaultRef, err := treeHandler.resolveBrowseRepository(ctx, agentFilesProject.ID)
	if err != nil {
		if errors.Is(err, errProjectRepoNotConfigured) {
			return "", "", "", "", errAgentFilesProjectNotConfigured
		}
		return "", "", "", "", err
	}
	return repoPath, strings.TrimSpace(agentFilesProject.ID), repoMode, defaultRef, nil
}

func (h *AdminAgentsHandler) ensureAgentFilesRepository(
	ctx context.Context,
	workspaceID string,
) (string, string, gitRepoMode, string, error) {
	repoPath, projectID, repoMode, defaultRef, err := h.resolveAgentFilesRepository(ctx, workspaceID)
	if err == nil {
		return repoPath, projectID, repoMode, defaultRef, nil
	}
	if !errors.Is(err, errAgentFilesProjectNotConfigured) {
		return "", "", "", "", err
	}
	if h.ProjectStore == nil || h.ProjectRepos == nil {
		return "", "", "", "", errAgentFilesProjectNotConfigured
	}

	agentFilesProject, getErr := h.ProjectStore.GetByName(ctx, agentFilesProjectName)
	if getErr != nil {
		if !errors.Is(getErr, store.ErrNotFound) {
			return "", "", "", "", getErr
		}
		agentFilesProject, getErr = h.ProjectStore.Create(ctx, store.CreateProjectInput{
			Name:   agentFilesProjectName,
			Status: "active",
		})
		if getErr != nil {
			return "", "", "", "", getErr
		}
	}
	if strings.TrimSpace(agentFilesProject.OrgID) != strings.TrimSpace(workspaceID) {
		return "", "", "", "", errAdminAgentForbidden
	}

	bareRepoPath, pathErr := h.ProjectStore.GetRepoPath(ctx, agentFilesProject.ID)
	if pathErr != nil {
		return "", "", "", "", pathErr
	}
	workingRepoPath, cloneErr := ensureAgentFilesWorkingRepo(ctx, bareRepoPath)
	if cloneErr != nil {
		return "", "", "", "", cloneErr
	}

	if _, bindErr := h.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          strings.TrimSpace(agentFilesProject.ID),
		RepositoryFullName: syntheticAgentFilesRepositoryFullName(agentFilesProject.ID),
		DefaultBranch:      "main",
		LocalRepoPath:      &workingRepoPath,
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
		ConflictDetails:    json.RawMessage("{}"),
	}); bindErr != nil {
		return "", "", "", "", bindErr
	}

	return h.resolveAgentFilesRepository(ctx, workspaceID)
}

func syntheticAgentFilesRepositoryFullName(projectID string) string {
	trimmed := strings.TrimSpace(projectID)
	if trimmed == "" {
		return "ottercamp/agent-files"
	}
	return "ottercamp/" + trimmed
}

func agentFilesWorkingRepoPath(bareRepoPath string) string {
	clean := filepath.Clean(strings.TrimSpace(bareRepoPath))
	if strings.HasSuffix(clean, ".git") {
		return strings.TrimSuffix(clean, ".git") + "-working"
	}
	return clean + "-working"
}

func ensureAgentFilesWorkingRepo(ctx context.Context, bareRepoPath string) (string, error) {
	barePath := filepath.Clean(strings.TrimSpace(bareRepoPath))
	if barePath == "" {
		return "", fmt.Errorf("agent files bare repo path is required")
	}
	if _, err := os.Stat(barePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("agent files bare repo path %q does not exist", barePath)
		}
		return "", fmt.Errorf("failed to inspect bare repo path: %w", err)
	}

	workingPath := agentFilesWorkingRepoPath(barePath)
	if info, err := os.Stat(workingPath); err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("agent files working repo path %q is not a directory", workingPath)
		}
		if err := ensureGitRepoPath(workingPath); err != nil {
			return "", err
		}
		return workingPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("failed to inspect agent files working repo path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(workingPath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create agent files working repo parent: %w", err)
	}
	cloneCommand := exec.CommandContext(ctx, "git", "clone", barePath, workingPath)
	cloneCommand.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := cloneCommand.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	// Ensure first commit uses main for deterministic defaults.
	if _, err := runGitInRepo(ctx, workingPath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return "", err
	}
	return workingPath, nil
}

func trimAgentRootEntries(entries []projectTreeEntry, root string) []projectTreeEntry {
	root = strings.Trim(strings.TrimSpace(root), "/")
	prefix := root + "/"
	out := make([]projectTreeEntry, 0, len(entries))
	for _, entry := range entries {
		trimmed := strings.Trim(strings.TrimSpace(entry.Path), "/")
		if root != "" {
			if !strings.HasPrefix(trimmed, prefix) {
				continue
			}
			trimmed = strings.TrimPrefix(trimmed, prefix)
		}
		if trimmed == "" {
			continue
		}
		entry.Path = trimmed
		if entry.Type == "dir" && !strings.HasSuffix(entry.Path, "/") {
			entry.Path += "/"
		}
		out = append(out, entry)
	}
	return out
}

func (h *AdminAgentsHandler) resolveFileAgentRow(r *http.Request) (*adminAgentRow, string, error) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		return nil, "", store.ErrNoWorkspace
	}
	if h.DB == nil || h.Store == nil {
		return nil, "", sql.ErrConnDone
	}

	identifier := strings.TrimSpace(chi.URLParam(r, "id"))
	if identifier == "" {
		return nil, "", fmt.Errorf("agent id is required")
	}
	row, err := h.getRow(r.Context(), workspaceID, identifier)
	if err != nil {
		return nil, "", err
	}
	return row, workspaceID, nil
}

func (h *AdminAgentsHandler) writeAgentLookupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "agent not found"})
	case errors.Is(err, errAdminAgentForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "agent belongs to a different workspace"})
	case errors.Is(err, sql.ErrConnDone):
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
	case strings.Contains(strings.ToLower(err.Error()), "agent id is required"):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load admin agent"})
	}
}

func (h *AdminAgentsHandler) writeAgentFilesResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAgentFilesProjectNotConfigured):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "Agent Files project is not configured"})
	case errors.Is(err, errAdminAgentForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "agent belongs to a different workspace"})
	default:
		status, message := classifyGitBrowseError(err)
		sendJSON(w, status, errorResponse{Error: message})
	}
}

func rowToAgentSummary(row adminAgentRow) adminAgentSummary {
	status := normalizeWorkspaceAgentStatus(row.WorkspaceStatus)
	if row.SyncUpdatedAt.Valid {
		status = deriveAgentStatus(row.SyncUpdatedAt.Time.UTC(), int(row.TotalTokens.Int64))
	}

	name := strings.TrimSpace(row.SyncName.String)
	if name == "" {
		name = strings.TrimSpace(row.DisplayName)
	}
	if name == "" {
		name = strings.TrimSpace(row.Slug)
	}

	return adminAgentSummary{
		ID:               strings.TrimSpace(row.Slug),
		WorkspaceAgentID: strings.TrimSpace(row.WorkspaceAgentID),
		Name:             name,
		Status:           status,
		Model:            strings.TrimSpace(row.SyncModel.String),
		HeartbeatEvery:   strings.TrimSpace(row.HeartbeatEvery.String),
		Channel:          strings.TrimSpace(row.SyncChannel.String),
		SessionKey:       strings.TrimSpace(row.SyncSessionKey.String),
		LastSeen:         strings.TrimSpace(row.SyncLastSeen.String),
	}
}

func normalizeWorkspaceAgentStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "online", "active":
		return "online"
	case "busy", "working":
		return "busy"
	default:
		return "offline"
	}
}

func scanAdminAgentRow(scanner interface{ Scan(...any) error }) (adminAgentRow, error) {
	var row adminAgentRow
	err := scanner.Scan(
		&row.WorkspaceAgentID,
		&row.Slug,
		&row.DisplayName,
		&row.WorkspaceStatus,
		&row.HeartbeatEvery,
		&row.SyncName,
		&row.SyncModel,
		&row.SyncChannel,
		&row.SyncSessionKey,
		&row.SyncLastSeen,
		&row.SyncCurrentTask,
		&row.SyncStatus,
		&row.SyncUpdatedAt,
		&row.ContextTokens,
		&row.TotalTokens,
	)
	return row, err
}

func (h *AdminAgentsHandler) writeAgentTemplates(
	ctx context.Context,
	repoPath string,
	slot string,
	input adminAgentTemplateInput,
) error {
	if err := ensureGitRepoPath(repoPath); err != nil {
		return err
	}

	agentDir := filepath.Join(repoPath, "agents", slot)
	if !isPathWithinRoot(repoPath, agentDir) {
		return fmt.Errorf("invalid agent template path")
	}
	if _, err := os.Stat(agentDir); err == nil {
		return fmt.Errorf("agent directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "memory"), 0o755); err != nil {
		return err
	}

	soul := strings.TrimSpace(input.Soul)
	if soul == "" {
		soul = renderNewAgentSoulTemplate("New Agent")
	}
	identity := strings.TrimSpace(input.Identity)
	if identity == "" {
		identity = renderNewAgentIdentityTemplate("New Agent", strings.TrimSpace(input.Avatar))
	}

	files := map[string]string{
		filepath.Join("agents", slot, "SOUL.md"):     soul,
		filepath.Join("agents", slot, "IDENTITY.md"): identity,
		filepath.Join("agents", slot, "TOOLS.md"):    renderNewAgentToolsTemplate(),
		filepath.Join("agents", slot, "MEMORY.md"):   renderNewAgentMemoryTemplate(strings.TrimSpace(input.DisplayName)),
	}
	for relativePath, content := range files {
		absolutePath := filepath.Join(repoPath, relativePath)
		if !isPathWithinRoot(repoPath, absolutePath) {
			return fmt.Errorf("invalid agent template path")
		}
		if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
			return err
		}
	}

	gitPath := path.Join("agents", slot)
	if _, err := runGitInRepo(ctx, repoPath, "add", "--", gitPath); err != nil {
		return err
	}
	hasChanges, err := gitHasCachedChangesForPath(ctx, repoPath, gitPath)
	if err != nil {
		return err
	}
	if !hasChanges {
		return fmt.Errorf("agent templates produced no git changes")
	}
	if _, err := runGitInRepo(
		ctx,
		repoPath,
		"-c", "user.name=OtterCamp Admin",
		"-c", "user.email=ottercamp-admin@localhost",
		"commit",
		"-m", fmt.Sprintf("Bootstrap agent files for %s", slot),
		"--",
		gitPath,
	); err != nil {
		return err
	}
	return nil
}

func buildCreateAgentTemplateInput(req adminAgentCreateRequest) (adminAgentTemplateInput, error) {
	displayName := strings.TrimSpace(req.DisplayName)
	profileID := strings.ToLower(strings.TrimSpace(req.ProfileID))

	profile := builtInAgentProfileTemplate{}
	hasProfile := false
	if profileID != "" {
		resolved, ok := builtInAgentProfiles[profileID]
		if !ok {
			return adminAgentTemplateInput{}, fmt.Errorf("profileId %q is not recognized", req.ProfileID)
		}
		profile = resolved
		hasProfile = true
	}

	avatar := strings.TrimSpace(req.Avatar)
	if avatar == "" && hasProfile {
		avatar = strings.TrimSpace(profile.Avatar)
	}

	soul := strings.TrimSpace(req.Soul)
	if soul == "" {
		if hasProfile && strings.TrimSpace(profile.Soul) != "" {
			soul = renderAgentProfileTemplate(profile.Soul, displayName, avatar)
		} else {
			soul = renderNewAgentSoulTemplate(displayName)
		}
	}

	identity := strings.TrimSpace(req.Identity)
	if identity == "" {
		if hasProfile && strings.TrimSpace(profile.Identity) != "" {
			identity = renderAgentProfileTemplate(profile.Identity, displayName, avatar)
		} else {
			identity = renderNewAgentIdentityTemplate(displayName, avatar)
		}
	}

	return adminAgentTemplateInput{
		DisplayName: displayName,
		Soul:        soul,
		Identity:    identity,
		Avatar:      avatar,
	}, nil
}

func renderAgentProfileTemplate(templateBody string, displayName string, avatar string) string {
	out := strings.TrimSpace(templateBody)
	out = strings.ReplaceAll(out, "{{name}}", strings.TrimSpace(displayName))
	out = strings.ReplaceAll(out, "{{avatar}}", strings.TrimSpace(avatar))
	if out == "" {
		return ""
	}
	return out + "\n"
}

func agentSlotFromDisplayName(displayName string) string {
	name := strings.ToLower(strings.TrimSpace(displayName))
	if name == "" {
		return "agent"
	}
	var b strings.Builder
	lastWasDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if b.Len() >= 63 {
				break
			}
			b.WriteRune(r)
			lastWasDash = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if b.Len() == 0 || lastWasDash {
			continue
		}
		if b.Len() >= 63 {
			break
		}
		b.WriteByte('-')
		lastWasDash = true
	}
	slot := strings.Trim(b.String(), "-")
	if len(slot) < 2 || !agentSlotPattern.MatchString(slot) {
		return "agent"
	}
	return slot
}

func resolveAvailableAgentSlot(baseSlot string, existsFn func(slot string) (bool, error)) (string, error) {
	base := strings.TrimSpace(baseSlot)
	if !agentSlotPattern.MatchString(base) {
		base = "agent"
	}
	for i := 1; ; i++ {
		candidate := base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			maxBaseLen := 63 - len(suffix)
			if maxBaseLen < 2 {
				maxBaseLen = 2
			}
			prefix := base
			if len(prefix) > maxBaseLen {
				prefix = prefix[:maxBaseLen]
			}
			prefix = strings.TrimRight(prefix, "-")
			if len(prefix) < 2 {
				prefix = "agent"
				if len(prefix) > maxBaseLen {
					prefix = prefix[:maxBaseLen]
				}
			}
			candidate = prefix + suffix
			if !agentSlotPattern.MatchString(candidate) {
				continue
			}
		}
		exists, err := existsFn(candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
}

func buildCreateAgentConfigPatch(slot string, model string) (json.RawMessage, error) {
	agentPatch := map[string]interface{}{
		"enabled": true,
		"model": map[string]interface{}{
			"primary": model,
		},
	}

	patch := map[string]interface{}{
		"agents": map[string]interface{}{
			slot: agentPatch,
		},
	}
	return canonicalizeOpenClawConfigData(patch)
}

func renderNewAgentSoulTemplate(displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "New Agent"
	}
	return fmt.Sprintf(
		"# SOUL.md - Who You Are\n\n*You're not a chatbot. You're becoming someone.*\n\n- Name: %s\n- Voice: Clear, grounded, and direct.\n- Values: Ownership, honesty, and useful outcomes.\n\n---\n\n_This file is yours to evolve. As you learn who you are, update it._\n",
		name,
	)
}

func renderNewAgentIdentityTemplate(displayName string, avatar string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "New Agent"
	}
	avatarValue := strings.TrimSpace(avatar)
	if avatarValue == "" {
		avatarValue = "*(workspace-relative path or URL)*"
	}
	return fmt.Sprintf(
		"# IDENTITY.md - Who Am I?\n\n- **Name:** %s\n- **Creature:** *(to be determined)*\n- **Vibe:** *(to be determined)*\n- **Emoji:** *(pick one)*\n- **Avatar:** %s\n",
		name, avatarValue,
	)
}

func renderNewAgentToolsTemplate() string {
	return "# TOOLS.md\n\n- Add local tool notes, credentials, and integration constraints here.\n"
}

func renderNewAgentMemoryTemplate(displayName string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = "Agent"
	}
	return fmt.Sprintf("# MEMORY.md\n\nLong-term memory for %s.\n", name)
}

func (h *AdminAgentsHandler) moveAgentFilesAndCommit(
	ctx context.Context,
	repoPath string,
	fromRelative string,
	toRelative string,
	commitMessage string,
	missingErr error,
	alreadyExistsErr error,
) error {
	if err := ensureGitRepoPath(repoPath); err != nil {
		return err
	}

	fromPath := filepath.Join(repoPath, filepath.FromSlash(fromRelative))
	toPath := filepath.Join(repoPath, filepath.FromSlash(toRelative))
	if !isPathWithinRoot(repoPath, fromPath) || !isPathWithinRoot(repoPath, toPath) {
		return fmt.Errorf("invalid lifecycle move path")
	}

	if _, err := os.Stat(fromPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return missingErr
		}
		return err
	}
	if _, err := os.Stat(toPath); err == nil {
		return alreadyExistsErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(toPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(fromPath, toPath); err != nil {
		return err
	}

	if _, err := runGitInRepo(ctx, repoPath, "add", "-A", "--", path.Dir(fromRelative)); err != nil {
		return err
	}
	hasChanges, err := gitHasCachedChangesForPath(ctx, repoPath, "agents")
	if err != nil {
		return err
	}
	if !hasChanges {
		return fmt.Errorf("agent lifecycle move produced no git changes")
	}
	if _, err := runGitInRepo(
		ctx,
		repoPath,
		"-c", "user.name=OtterCamp Admin",
		"-c", "user.email=ottercamp-admin@localhost",
		"commit",
		"-m", commitMessage,
	); err != nil {
		return err
	}
	return nil
}

func (h *AdminAgentsHandler) updateAgentStatus(
	ctx context.Context,
	workspaceID string,
	slug string,
	status string,
) error {
	result, err := h.DB.ExecContext(
		ctx,
		`UPDATE agents
		 SET status = $1,
		     updated_at = NOW()
		 WHERE org_id = $2
		   AND slug = $3`,
		status,
		workspaceID,
		slug,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (h *AdminAgentsHandler) dispatchAgentEnablePatch(
	w http.ResponseWriter,
	r *http.Request,
	slug string,
	enabled bool,
) {
	patch, err := canonicalizeOpenClawConfigData(map[string]interface{}{
		"agents": map[string]interface{}{
			slug: map[string]interface{}{
				"enabled": enabled,
			},
		},
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to build config patch"})
		return
	}

	dispatcher := &AdminConnectionsHandler{
		DB:              h.DB,
		OpenClawHandler: h.OpenClawHandler,
		EventStore:      h.EventStore,
	}
	dispatcher.dispatchAdminCommand(
		w,
		r,
		adminCommandActionConfigPatch,
		adminCommandDispatchInput{
			ConfigPatch: patch,
			Confirm:     true,
			DryRun:      false,
		},
	)
}

func (h *AdminAgentsHandler) writeLifecycleMoveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAgentFilesDirMissing):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "agent files directory is missing"})
	case errors.Is(err, errRetiredAgentFilesDirMissing):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "retired agent archive is missing"})
	case errors.Is(err, errAgentFilesDirAlreadyExists):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "agent files directory already exists"})
	case errors.Is(err, errRetiredAgentFilesDirExists):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "retired agent archive already exists"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update agent lifecycle"})
	}
}
