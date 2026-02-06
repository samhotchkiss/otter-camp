package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	githubSignatureHeader = "X-Hub-Signature-256"
	githubDeliveryHeader  = "X-GitHub-Delivery"
	githubEventHeader     = "X-GitHub-Event"
)

var allowedGitHubEvents = map[string]struct{}{
	"push":         {},
	"issues":       {},
	"issue_comment": {},
	"pull_request": {},
}

type GitHubIntegrationHandler struct {
	DB                *sql.DB
	Installations     *store.GitHubInstallationStore
	ProjectRepos      *store.ProjectRepoStore
	SyncJobs          *store.GitHubSyncJobStore
	ConnectStates     *githubConnectStateStore
	WebhookDeliveries *githubDeliveryStore
}

type githubConnectStateStore struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	state map[string]githubConnectState
}

type githubConnectState struct {
	OrgID     string
	ExpiresAt time.Time
}

type githubDeliveryStore struct {
	ttl   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	items map[string]time.Time
}

type githubRepoOption struct {
	ID            string `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

type githubIntegrationStatusResponse struct {
	Connected        bool                       `json:"connected"`
	Installation     *store.GitHubInstallation  `json:"installation,omitempty"`
	ConfiguredRepos  int                        `json:"configured_repos"`
	ConfiguredCount  int                        `json:"configured_projects"`
	LastConnectedAt  *time.Time                 `json:"last_connected_at,omitempty"`
}

type githubProjectSettingView struct {
	ProjectID        string     `json:"project_id"`
	ProjectName      string     `json:"project_name"`
	Description      *string    `json:"description,omitempty"`
	Enabled          bool       `json:"enabled"`
	RepoFullName     *string    `json:"repo_full_name,omitempty"`
	DefaultBranch    string     `json:"default_branch"`
	SyncMode         string     `json:"sync_mode"`
	AutoSync         bool       `json:"auto_sync"`
	ActiveBranches   []string   `json:"active_branches"`
	LastSyncedSHA    *string    `json:"last_synced_sha,omitempty"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	ConflictState    string     `json:"conflict_state"`
}

type githubSettingsListResponse struct {
	Projects []githubProjectSettingView `json:"projects"`
	Total    int                        `json:"total"`
}

type githubSettingsUpdateRequest struct {
	Enabled        *bool    `json:"enabled"`
	RepoFullName   *string  `json:"repo_full_name"`
	DefaultBranch  *string  `json:"default_branch"`
	SyncMode       *string  `json:"sync_mode"`
	AutoSync       *bool    `json:"auto_sync"`
	ActiveBranches []string `json:"active_branches"`
}

type githubConnectStartResponse struct {
	InstallURL      string `json:"install_url"`
	State           string `json:"state"`
	ExpiresInSecond int    `json:"expires_in_seconds"`
}

type githubConnectCallbackResponse struct {
	Connected      bool   `json:"connected"`
	OrgID          string `json:"org_id"`
	InstallationID int64  `json:"installation_id"`
	AccountLogin   string `json:"account_login"`
	AccountType    string `json:"account_type"`
}

type githubProjectBranchesResponse struct {
	ProjectID      string                             `json:"project_id"`
	DefaultBranch  string                             `json:"default_branch"`
	LastSyncedSHA  *string                            `json:"last_synced_sha,omitempty"`
	LastSyncedAt   *time.Time                         `json:"last_synced_at,omitempty"`
	ActiveBranches []store.ProjectRepoActiveBranch    `json:"active_branches"`
}

type githubManualSyncResponse struct {
	JobID             string     `json:"job_id"`
	Status            string     `json:"status"`
	ProjectID         string     `json:"project_id"`
	RepositoryFullName string    `json:"repository_full_name"`
	LastSyncedSHA     *string    `json:"last_synced_sha,omitempty"`
	LastSyncedAt      *time.Time `json:"last_synced_at,omitempty"`
	ConflictState     string     `json:"conflict_state"`
}

type githubWebhookPayload struct {
	Ref string `json:"ref"`
	Before string `json:"before"`
	After string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func NewGitHubIntegrationHandler(db *sql.DB) *GitHubIntegrationHandler {
	handler := &GitHubIntegrationHandler{
		DB:                db,
		ConnectStates:     newGitHubConnectStateStore(10 * time.Minute),
		WebhookDeliveries: newGitHubDeliveryStore(24 * time.Hour),
	}
	if db != nil {
		handler.Installations = store.NewGitHubInstallationStore(db)
		handler.ProjectRepos = store.NewProjectRepoStore(db)
		handler.SyncJobs = store.NewGitHubSyncJobStore(db)
	}
	return handler
}

func newGitHubConnectStateStore(ttl time.Duration) *githubConnectStateStore {
	return &githubConnectStateStore{
		ttl:   ttl,
		now:   time.Now,
		state: make(map[string]githubConnectState),
	}
}

func (s *githubConnectStateStore) Create(orgID string) (string, time.Time, error) {
	if strings.TrimSpace(orgID) == "" {
		return "", time.Time{}, fmt.Errorf("org_id is required")
	}
	token, err := generateSecureToken(32)
	if err != nil {
		return "", time.Time{}, err
	}

	now := s.now().UTC()
	expiresAt := now.Add(s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)
	s.state[token] = githubConnectState{OrgID: orgID, ExpiresAt: expiresAt}
	return token, expiresAt, nil
}

func (s *githubConnectStateStore) Consume(state string) (string, bool) {
	state = strings.TrimSpace(state)
	if state == "" {
		return "", false
	}

	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)

	entry, ok := s.state[state]
	if !ok {
		return "", false
	}
	delete(s.state, state)
	if now.After(entry.ExpiresAt) {
		return "", false
	}
	return entry.OrgID, true
}

func (s *githubConnectStateStore) cleanupLocked(now time.Time) {
	for key, value := range s.state {
		if now.After(value.ExpiresAt) {
			delete(s.state, key)
		}
	}
}

func newGitHubDeliveryStore(ttl time.Duration) *githubDeliveryStore {
	return &githubDeliveryStore{
		ttl:   ttl,
		now:   time.Now,
		items: make(map[string]time.Time),
	}
}

// MarkIfNew returns true when the delivery ID has not been seen in the replay window.
func (s *githubDeliveryStore) MarkIfNew(deliveryID string) bool {
	deliveryID = strings.TrimSpace(deliveryID)
	if deliveryID == "" {
		return false
	}
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(now)
	if seenAt, exists := s.items[deliveryID]; exists && now.Sub(seenAt) <= s.ttl {
		return false
	}
	s.items[deliveryID] = now
	return true
}

func (s *githubDeliveryStore) cleanupLocked(now time.Time) {
	for key, seenAt := range s.items {
		if now.Sub(seenAt) > s.ttl {
			delete(s.items, key)
		}
	}
}

func (h *GitHubIntegrationHandler) ConnectStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.Installations == nil || h.ConnectStates == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	state, expiresAt, err := h.ConnectStates.Create(orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to create connect state"})
		return
	}

	installURL, err := githubInstallURL(state)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	sendJSON(w, http.StatusOK, githubConnectStartResponse{
		InstallURL:      installURL,
		State:           state,
		ExpiresInSecond: int(time.Until(expiresAt).Seconds()),
	})
}

func (h *GitHubIntegrationHandler) ConnectCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.Installations == nil || h.ConnectStates == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	state := strings.TrimSpace(r.URL.Query().Get("state"))
	orgID, ok := h.ConnectStates.Consume(state)
	if !ok {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid or expired state"})
		return
	}

	installationID, err := parsePositiveInt64(r.URL.Query().Get("installation_id"))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "installation_id is required"})
		return
	}

	accountLogin := strings.TrimSpace(r.URL.Query().Get("account_login"))
	if accountLogin == "" {
		accountLogin = fmt.Sprintf("installation-%d", installationID)
	}
	accountType := strings.TrimSpace(r.URL.Query().Get("account_type"))
	if accountType == "" {
		accountType = "Organization"
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	installation, err := h.Installations.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: installationID,
		AccountLogin:   accountLogin,
		AccountType:    accountType,
		Permissions:    json.RawMessage("{}"),
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist github installation"})
		return
	}

	sendJSON(w, http.StatusOK, githubConnectCallbackResponse{
		Connected:      true,
		OrgID:          orgID,
		InstallationID: installation.InstallationID,
		AccountLogin:   installation.AccountLogin,
		AccountType:    installation.AccountType,
	})
}

func (h *GitHubIntegrationHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	_, err := h.DB.ExecContext(r.Context(), `DELETE FROM github_installations WHERE org_id = $1`, orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to disconnect github integration"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]any{
		"disconnected": true,
	})
}

func (h *GitHubIntegrationHandler) IntegrationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.Installations == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	installation, err := h.Installations.GetByOrg(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load integration status"})
		return
	}

	configuredProjects, configuredRepos, countErr := countConfiguredRepos(r.Context(), h.DB, orgID)
	if countErr != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load repository settings"})
		return
	}

	response := githubIntegrationStatusResponse{
		Connected:       installation != nil,
		Installation:    installation,
		ConfiguredCount: configuredProjects,
		ConfiguredRepos: configuredRepos,
	}
	if installation != nil {
		response.LastConnectedAt = &installation.ConnectedAt
	}
	sendJSON(w, http.StatusOK, response)
}

func (h *GitHubIntegrationHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	repoMap := make(map[string]githubRepoOption)
	rows, err := h.DB.QueryContext(
		r.Context(),
		`SELECT DISTINCT repository_full_name, default_branch
		 FROM project_repo_bindings
		 WHERE org_id = $1
		 ORDER BY repository_full_name ASC`,
		orgID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list repositories"})
		return
	}
	defer rows.Close()

	for rows.Next() {
		var fullName, defaultBranch string
		if err := rows.Scan(&fullName, &defaultBranch); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to scan repositories"})
			return
		}
		fullName = strings.TrimSpace(fullName)
		if fullName == "" {
			continue
		}
		if strings.TrimSpace(defaultBranch) == "" {
			defaultBranch = "main"
		}
		repoMap[fullName] = githubRepoOption{
			ID:            fullName,
			FullName:      fullName,
			DefaultBranch: defaultBranch,
			Private:       false,
		}
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read repositories"})
		return
	}

	for _, repo := range parseConfiguredRepoEnv() {
		if _, exists := repoMap[repo.FullName]; exists {
			continue
		}
		repoMap[repo.FullName] = repo
	}

	repos := make([]githubRepoOption, 0, len(repoMap))
	for _, repo := range repoMap {
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].FullName < repos[j].FullName
	})

	sendJSON(w, http.StatusOK, map[string]any{
		"repos": repos,
		"total": len(repos),
	})
}

func (h *GitHubIntegrationHandler) ListSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	branchesByProject, err := loadActiveBranchesByProject(r.Context(), h.DB, orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load active branches"})
		return
	}

	rows, err := h.DB.QueryContext(
		r.Context(),
		`SELECT
			p.id,
			p.name,
			p.description,
			b.repository_full_name,
			COALESCE(b.default_branch, 'main') AS default_branch,
			COALESCE(b.enabled, false) AS enabled,
			COALESCE(b.sync_mode, 'sync') AS sync_mode,
			COALESCE(b.auto_sync, true) AS auto_sync,
			b.last_synced_sha,
			b.last_synced_at,
			COALESCE(b.conflict_state, 'none') AS conflict_state
		FROM projects p
		LEFT JOIN project_repo_bindings b ON b.project_id = p.id
		WHERE p.org_id = $1
		ORDER BY p.created_at DESC`,
		orgID,
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load github settings"})
		return
	}
	defer rows.Close()

	projects := make([]githubProjectSettingView, 0)
	for rows.Next() {
		var item githubProjectSettingView
		var repo sql.NullString
		if err := rows.Scan(
			&item.ProjectID,
			&item.ProjectName,
			&item.Description,
			&repo,
			&item.DefaultBranch,
			&item.Enabled,
			&item.SyncMode,
			&item.AutoSync,
			&item.LastSyncedSHA,
			&item.LastSyncedAt,
			&item.ConflictState,
		); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read github settings"})
			return
		}
		if repo.Valid && strings.TrimSpace(repo.String) != "" {
			repoName := strings.TrimSpace(repo.String)
			item.RepoFullName = &repoName
		}
		item.ActiveBranches = branchesByProject[item.ProjectID]
		projects = append(projects, item)
	}
	if err := rows.Err(); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to read github settings"})
		return
	}

	sendJSON(w, http.StatusOK, githubSettingsListResponse{
		Projects: projects,
		Total:    len(projects),
	})
}

func (h *GitHubIntegrationHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.ProjectRepos == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	if !projectBelongsToOrg(r.Context(), h.DB, orgID, projectID) {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "project not found"})
		return
	}

	var request githubSettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	existing, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load existing settings"})
		return
	}

	finalRepo := firstNonEmptyFromPtr(request.RepoFullName)
	if finalRepo == "" && existing != nil {
		finalRepo = existing.RepositoryFullName
	}
	if finalRepo == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "repo_full_name is required"})
		return
	}

	finalDefaultBranch := firstNonEmptyFromPtr(request.DefaultBranch)
	if finalDefaultBranch == "" && existing != nil {
		finalDefaultBranch = existing.DefaultBranch
	}
	if finalDefaultBranch == "" {
		finalDefaultBranch = "main"
	}

	finalSyncMode := firstNonEmptyFromPtr(request.SyncMode)
	if finalSyncMode == "" && existing != nil {
		finalSyncMode = existing.SyncMode
	}
	if finalSyncMode == "" {
		finalSyncMode = store.RepoSyncModeSync
	}

	finalEnabled := false
	if existing != nil {
		finalEnabled = existing.Enabled
	}
	if request.Enabled != nil {
		finalEnabled = *request.Enabled
	}

	finalAutoSync := true
	if existing != nil {
		finalAutoSync = existing.AutoSync
	}
	if request.AutoSync != nil {
		finalAutoSync = *request.AutoSync
	}

	upserted, err := h.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: finalRepo,
		DefaultBranch:      finalDefaultBranch,
		Enabled:            finalEnabled,
		SyncMode:           finalSyncMode,
		AutoSync:           finalAutoSync,
		ConflictState:      store.RepoConflictNone,
	})
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if request.ActiveBranches != nil {
		if err := validateBranches(request.ActiveBranches); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		if _, err := h.ProjectRepos.SetActiveBranches(ctx, projectID, request.ActiveBranches); err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
	}

	activeBranches, err := h.ProjectRepos.ListActiveBranches(ctx, projectID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load active branches"})
		return
	}

	sendJSON(w, http.StatusOK, map[string]any{
		"binding":         upserted,
		"active_branches": activeBranches,
	})
}

func (h *GitHubIntegrationHandler) GetProjectBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}
	activeBranches, err := h.ProjectRepos.ListActiveBranches(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, githubProjectBranchesResponse{
		ProjectID:      projectID,
		DefaultBranch:  binding.DefaultBranch,
		LastSyncedSHA:  binding.LastSyncedSHA,
		LastSyncedAt:   binding.LastSyncedAt,
		ActiveBranches: activeBranches,
	})
}

func (h *GitHubIntegrationHandler) UpdateProjectBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.ProjectRepos == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var payload struct {
		Branches []string `json:"branches"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}
	if err := validateBranches(payload.Branches); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	branches, err := h.ProjectRepos.SetActiveBranches(ctx, projectID, payload.Branches)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, map[string]any{
		"project_id":      projectID,
		"active_branches": branches,
	})
}

func (h *GitHubIntegrationHandler) ManualRepoSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.ProjectRepos == nil || h.SyncJobs == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}
	orgID := workspaceIDFromRequest(r)
	if orgID == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}
	activeBranches, err := h.ProjectRepos.ListActiveBranches(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}

	branchSet := map[string]struct{}{binding.DefaultBranch: {}}
	branches := []string{binding.DefaultBranch}
	for _, branch := range activeBranches {
		if _, exists := branchSet[branch.BranchName]; exists {
			continue
		}
		branchSet[branch.BranchName] = struct{}{}
		branches = append(branches, branch.BranchName)
	}

	payloadBytes, err := json.Marshal(map[string]any{
		"reason":               "manual",
		"requested_at":         time.Now().UTC(),
		"repository_full_name": binding.RepositoryFullName,
		"default_branch":       binding.DefaultBranch,
		"branches":             branches,
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode sync payload"})
		return
	}

	job, err := h.SyncJobs.Enqueue(ctx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeRepoSync,
		Payload:     payloadBytes,
		MaxAttempts: 5,
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue sync job"})
		return
	}

	_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.repo_sync_requested", map[string]any{
		"job_id":              job.ID,
		"repository_full_name": binding.RepositoryFullName,
		"branches":            branches,
	})

	sendJSON(w, http.StatusAccepted, githubManualSyncResponse{
		JobID:              job.ID,
		Status:             job.Status,
		ProjectID:          projectID,
		RepositoryFullName: binding.RepositoryFullName,
		LastSyncedSHA:      binding.LastSyncedSHA,
		LastSyncedAt:       binding.LastSyncedAt,
		ConflictState:      binding.ConflictState,
	})
}

func (h *GitHubIntegrationHandler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	if h.SyncJobs == nil || h.Installations == nil || h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	secret := strings.TrimSpace(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	if secret == "" {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "github webhook secret not configured"})
		return
	}

	eventType := strings.TrimSpace(r.Header.Get(githubEventHeader))
	if _, ok := allowedGitHubEvents[eventType]; !ok {
		sendJSON(w, http.StatusAccepted, map[string]any{
			"ok":      true,
			"ignored": true,
			"reason":  "unsupported event",
		})
		return
	}

	signature := strings.TrimSpace(r.Header.Get(githubSignatureHeader))
	if signature == "" {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing github signature"})
		return
	}

	deliveryID := strings.TrimSpace(r.Header.Get(githubDeliveryHeader))
	if deliveryID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing github delivery id"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "unable to read request body"})
		return
	}
	if !verifyGitHubSignature(secret, body, signature) {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid github signature"})
		return
	}

	if h.WebhookDeliveries != nil && !h.WebhookDeliveries.MarkIfNew(deliveryID) {
		sendJSON(w, http.StatusAccepted, map[string]any{
			"ok":        true,
			"duplicate": true,
		})
		return
	}

	var payload githubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid webhook payload"})
		return
	}

	orgID, err := h.resolveWebhookOrgID(r.Context(), payload)
	if err != nil {
		sendJSON(w, http.StatusAccepted, map[string]any{
			"ok":      true,
			"ignored": true,
			"reason":  err.Error(),
		})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	projectID, _ := lookupProjectByRepo(r.Context(), h.DB, orgID, payload.Repository.FullName)

	webhookPayload, err := json.Marshal(map[string]any{
		"event":       eventType,
		"delivery_id": deliveryID,
		"payload":     json.RawMessage(body),
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to encode webhook job payload"})
		return
	}

	jobInput := store.EnqueueGitHubSyncJobInput{
		ProjectID:     projectID,
		JobType:       store.GitHubSyncJobTypeWebhook,
		Payload:       webhookPayload,
		SourceEventID: &deliveryID,
		MaxAttempts:   8,
	}
	webhookJob, err := h.SyncJobs.Enqueue(ctx, jobInput)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue webhook job"})
		return
	}

	repoSyncQueued := false
		if eventType == "push" && projectID != nil {
			queued, queueErr := h.enqueuePushRepoSync(ctx, *projectID, payload, deliveryID)
		if queueErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue repo sync from push event"})
			return
		}
		repoSyncQueued = queued
	}

	_ = logGitHubActivity(r.Context(), h.DB, orgID, projectID, "github.webhook."+eventType, map[string]any{
		"delivery_id":   deliveryID,
		"repository":    payload.Repository.FullName,
		"webhook_job_id": webhookJob.ID,
		"repo_sync":     repoSyncQueued,
	})

	sendJSON(w, http.StatusAccepted, map[string]any{
		"ok":               true,
		"event":            eventType,
		"delivery_id":      deliveryID,
		"webhook_job_id":   webhookJob.ID,
		"repo_sync_queued": repoSyncQueued,
		"project_id":       projectID,
	})
}

func (h *GitHubIntegrationHandler) resolveWebhookOrgID(ctx context.Context, payload githubWebhookPayload) (string, error) {
	if payload.Installation.ID > 0 {
		installation, err := h.Installations.GetByInstallationID(ctx, payload.Installation.ID)
		if err == nil {
			return installation.OrgID, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
	}

	if strings.TrimSpace(payload.Repository.FullName) != "" {
		resolvedOrgID, _ := lookupProjectByRepo(ctx, h.DB, "", payload.Repository.FullName)
		if resolvedOrgID != nil && strings.TrimSpace(*resolvedOrgID) != "" {
			return *resolvedOrgID, nil
		}
	}

	return "", fmt.Errorf("webhook not mapped to an installation or project")
}

func (h *GitHubIntegrationHandler) enqueuePushRepoSync(
	ctx context.Context,
	projectID string,
	payload githubWebhookPayload,
	deliveryID string,
) (bool, error) {
	if strings.TrimSpace(projectID) == "" {
		return false, nil
	}
	if strings.TrimSpace(payload.Ref) == "" {
		return false, nil
	}

	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	branch := strings.TrimPrefix(strings.TrimSpace(payload.Ref), "refs/heads/")
	if branch == "" {
		return false, nil
	}

	allowedBranches := map[string]struct{}{binding.DefaultBranch: {}}
	active, err := h.ProjectRepos.ListActiveBranches(ctx, projectID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	for _, entry := range active {
		allowedBranches[entry.BranchName] = struct{}{}
	}
	if _, allowed := allowedBranches[branch]; !allowed {
		return false, nil
	}

	payloadBytes, err := json.Marshal(map[string]any{
		"reason":               "push_webhook",
		"delivery_id":          deliveryID,
		"repository_full_name": payload.Repository.FullName,
		"branch":               branch,
		"before":               payload.Before,
		"after":                payload.After,
		"received_at":          time.Now().UTC(),
	})
	if err != nil {
		return false, err
	}

	sourceID := deliveryID + ":repo_sync:" + branch
	_, err = h.SyncJobs.Enqueue(ctx, store.EnqueueGitHubSyncJobInput{
		ProjectID:     &projectID,
		JobType:       store.GitHubSyncJobTypeRepoSync,
		Payload:       payloadBytes,
		SourceEventID: &sourceID,
		MaxAttempts:   5,
	})
	if err != nil {
		return false, err
	}

	return true, nil
}

func workspaceIDFromRequest(r *http.Request) string {
	if id := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())); id != "" && uuidRegex.MatchString(id) {
		return id
	}
	if id := strings.TrimSpace(r.URL.Query().Get("org_id")); id != "" && uuidRegex.MatchString(id) {
		return id
	}
	if id := strings.TrimSpace(r.Header.Get("X-Org-ID")); id != "" && uuidRegex.MatchString(id) {
		return id
	}
	if id := strings.TrimSpace(r.Header.Get("X-Workspace-ID")); id != "" && uuidRegex.MatchString(id) {
		return id
	}
	return ""
}

func githubInstallURL(state string) (string, error) {
	base := strings.TrimSpace(os.Getenv("GITHUB_APP_INSTALL_URL"))
	if base == "" {
		slug := strings.TrimSpace(os.Getenv("GITHUB_APP_SLUG"))
		if slug == "" {
			return "", fmt.Errorf("GITHUB_APP_SLUG or GITHUB_APP_INSTALL_URL is required")
		}
		base = fmt.Sprintf("https://github.com/apps/%s/installations/new", slug)
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid install url: %w", err)
	}
	query := parsed.Query()
	query.Set("state", state)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func parseConfiguredRepoEnv() []githubRepoOption {
	raw := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORIES"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	repos := make([]githubRepoOption, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		fullName := item
		defaultBranch := "main"
		if strings.Contains(item, ":") {
			segments := strings.SplitN(item, ":", 2)
			fullName = strings.TrimSpace(segments[0])
			if strings.TrimSpace(segments[1]) != "" {
				defaultBranch = strings.TrimSpace(segments[1])
			}
		}
		if fullName == "" {
			continue
		}
		repos = append(repos, githubRepoOption{
			ID:            fullName,
			FullName:      fullName,
			DefaultBranch: defaultBranch,
			Private:       false,
		})
	}
	return repos
}

func parsePositiveInt64(raw string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return parsed, nil
}

func firstNonEmptyFromPtr(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func validateBranches(branches []string) error {
	seen := make(map[string]struct{}, len(branches))
	for _, raw := range branches {
		branch := strings.TrimSpace(raw)
		if branch == "" {
			continue
		}
		if strings.Contains(branch, "..") || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
			return fmt.Errorf("invalid branch name %q", branch)
		}
		for _, c := range branch {
			switch {
			case c >= 'a' && c <= 'z':
			case c >= 'A' && c <= 'Z':
			case c >= '0' && c <= '9':
			case c == '-' || c == '_' || c == '/' || c == '.':
			default:
				return fmt.Errorf("invalid branch name %q", branch)
			}
		}
		if _, exists := seen[branch]; exists {
			continue
		}
		seen[branch] = struct{}{}
	}
	return nil
}

func loadActiveBranchesByProject(ctx context.Context, db *sql.DB, orgID string) (map[string][]string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_id, branch_name
		 FROM project_repo_active_branches
		 WHERE org_id = $1
		 ORDER BY project_id ASC, branch_name ASC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string)
	for rows.Next() {
		var projectID, branch string
		if err := rows.Scan(&projectID, &branch); err != nil {
			return nil, err
		}
		out[projectID] = append(out[projectID], branch)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func projectBelongsToOrg(ctx context.Context, db *sql.DB, orgID, projectID string) bool {
	var exists bool
	err := db.QueryRowContext(
		ctx,
		`SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND org_id = $2)`,
		projectID,
		orgID,
	).Scan(&exists)
	return err == nil && exists
}

func countConfiguredRepos(ctx context.Context, db *sql.DB, orgID string) (int, int, error) {
	var configuredProjects int
	var configuredRepos int
	err := db.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*) FILTER (WHERE repository_full_name IS NOT NULL AND repository_full_name <> ''),
			COUNT(DISTINCT repository_full_name) FILTER (WHERE repository_full_name IS NOT NULL AND repository_full_name <> '')
		FROM project_repo_bindings
		WHERE org_id = $1`,
		orgID,
	).Scan(&configuredProjects, &configuredRepos)
	if err != nil {
		return 0, 0, err
	}
	return configuredProjects, configuredRepos, nil
}

func lookupProjectByRepo(ctx context.Context, db *sql.DB, orgID, repoFullName string) (*string, *string) {
	repoFullName = strings.TrimSpace(repoFullName)
	if repoFullName == "" {
		return nil, nil
	}

	baseQuery := `SELECT org_id, project_id FROM project_repo_bindings WHERE repository_full_name = $1`
	args := []any{repoFullName}
	if strings.TrimSpace(orgID) != "" {
		baseQuery += ` AND org_id = $2`
		args = append(args, orgID)
	}
	baseQuery += ` ORDER BY updated_at DESC LIMIT 1`

	var resolvedOrgID, projectID string
	if err := db.QueryRowContext(ctx, baseQuery, args...).Scan(&resolvedOrgID, &projectID); err != nil {
		return nil, nil
	}
	return &resolvedOrgID, &projectID
}

func handleRepoStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "project repository settings not found"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}

func verifyGitHubSignature(secret string, payload []byte, header string) bool {
	if strings.TrimSpace(secret) == "" {
		return false
	}
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	providedHex := strings.TrimPrefix(header, "sha256=")
	provided, err := hex.DecodeString(providedHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)
	return hmac.Equal(provided, expected)
}

func generateSecureToken(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("length must be positive")
	}
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func logGitHubActivity(ctx context.Context, db *sql.DB, orgID string, projectID *string, action string, metadata map[string]any) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(
		ctx,
		`INSERT INTO activity_log (org_id, project_id, action, metadata) VALUES ($1, $2, $3, $4)`,
		orgID,
		nullableString(projectID),
		action,
		metadataJSON,
	)
	return err
}
