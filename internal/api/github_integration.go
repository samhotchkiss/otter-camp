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
	"os/exec"
	"path/filepath"
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
	"push":          {},
	"issues":        {},
	"issue_comment": {},
	"pull_request":  {},
}

type GitHubIntegrationHandler struct {
	DB                *sql.DB
	Installations     *store.GitHubInstallationStore
	ProjectRepos      *store.ProjectRepoStore
	Commits           *store.ProjectCommitStore
	SyncJobs          *store.GitHubSyncJobStore
	IssueStore        *store.ProjectIssueStore
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
	Connected       bool                      `json:"connected"`
	Installation    *store.GitHubInstallation `json:"installation,omitempty"`
	ConfiguredRepos int                       `json:"configured_repos"`
	ConfiguredCount int                       `json:"configured_projects"`
	LastConnectedAt *time.Time                `json:"last_connected_at,omitempty"`
}

type githubProjectSettingView struct {
	ProjectID       string     `json:"project_id"`
	ProjectName     string     `json:"project_name"`
	Description     *string    `json:"description,omitempty"`
	Enabled         bool       `json:"enabled"`
	RepoFullName    *string    `json:"repo_full_name,omitempty"`
	DefaultBranch   string     `json:"default_branch"`
	SyncMode        string     `json:"sync_mode"`
	AutoSync        bool       `json:"auto_sync"`
	ActiveBranches  []string   `json:"active_branches"`
	LastSyncedSHA   *string    `json:"last_synced_sha,omitempty"`
	LastSyncedAt    *time.Time `json:"last_synced_at,omitempty"`
	ConflictState   string     `json:"conflict_state"`
	WorkflowMode    string     `json:"workflow_mode"`
	GitHubPREnabled bool       `json:"github_pr_enabled"`
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
	ProjectID       string                          `json:"project_id"`
	DefaultBranch   string                          `json:"default_branch"`
	LastSyncedSHA   *string                         `json:"last_synced_sha,omitempty"`
	LastSyncedAt    *time.Time                      `json:"last_synced_at,omitempty"`
	ConflictState   string                          `json:"conflict_state"`
	ConflictDetails json.RawMessage                 `json:"conflict_details"`
	ActiveBranches  []store.ProjectRepoActiveBranch `json:"active_branches"`
}

type githubManualSyncResponse struct {
	JobID              string     `json:"job_id"`
	Status             string     `json:"status"`
	ProjectID          string     `json:"project_id"`
	RepositoryFullName string     `json:"repository_full_name"`
	LastSyncedSHA      *string    `json:"last_synced_sha,omitempty"`
	LastSyncedAt       *time.Time `json:"last_synced_at,omitempty"`
	ConflictState      string     `json:"conflict_state"`
}

type githubConflictResolutionRequest struct {
	Action string `json:"action"`
}

type githubConflictResolutionResponse struct {
	ProjectID       string          `json:"project_id"`
	Action          string          `json:"action"`
	ConflictState   string          `json:"conflict_state"`
	ConflictDetails json.RawMessage `json:"conflict_details"`
	LocalHeadSHA    *string         `json:"local_head_sha,omitempty"`
	ResolvedAt      string          `json:"resolved_at"`
}

type githubPublishRequest struct {
	DryRun bool `json:"dry_run"`
}

type githubPublishCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Blocking bool   `json:"blocking"`
}

type githubPublishResponse struct {
	ProjectID     string               `json:"project_id"`
	DryRun        bool                 `json:"dry_run"`
	Status        string               `json:"status"`
	Checks        []githubPublishCheck `json:"checks"`
	LocalHeadSHA  *string              `json:"local_head_sha,omitempty"`
	RemoteHeadSHA *string              `json:"remote_head_sha,omitempty"`
	CommitsAhead  int                  `json:"commits_ahead"`
	PublishedAt   *string              `json:"published_at,omitempty"`
}

type githubWebhookPayload struct {
	Ref        string                       `json:"ref"`
	Before     string                       `json:"before"`
	After      string                       `json:"after"`
	Commits    []githubWebhookCommitPayload `json:"commits"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

type githubWebhookCommitPayload struct {
	ID        string   `json:"id"`
	Message   string   `json:"message"`
	Timestamp string   `json:"timestamp"`
	URL       string   `json:"url"`
	Added     []string `json:"added"`
	Removed   []string `json:"removed"`
	Modified  []string `json:"modified"`
	Author    struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Username string `json:"username"`
	} `json:"author"`
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
		handler.Commits = store.NewProjectCommitStore(db)
		handler.SyncJobs = store.NewGitHubSyncJobStore(db)
		handler.IssueStore = store.NewProjectIssueStore(db)
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
		mode := resolveReviewMode(item.SyncMode)
		item.WorkflowMode = mode.Mode
		item.GitHubPREnabled = mode.GitHubPREnabled
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
	var localRepoPath *string
	if existing != nil {
		localRepoPath = existing.LocalRepoPath
	}

	upserted, err := h.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: finalRepo,
		DefaultBranch:      finalDefaultBranch,
		LocalRepoPath:      localRepoPath,
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
		ProjectID:       projectID,
		DefaultBranch:   binding.DefaultBranch,
		LastSyncedSHA:   binding.LastSyncedSHA,
		LastSyncedAt:    binding.LastSyncedAt,
		ConflictState:   binding.ConflictState,
		ConflictDetails: binding.ConflictDetails,
		ActiveBranches:  activeBranches,
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

func (h *GitHubIntegrationHandler) ResolveProjectConflict(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var request githubConflictResolutionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	action := strings.TrimSpace(strings.ToLower(request.Action))
	if action != "keep_github" && action != "keep_ottercamp" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "action must be one of keep_github or keep_ottercamp"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}
	if binding.ConflictState != store.RepoConflictNeedsDecision {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "project does not have unresolved sync conflicts"})
		return
	}
	if binding.LocalRepoPath == nil || strings.TrimSpace(*binding.LocalRepoPath) == "" {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "project has no local repo path to resolve against"})
		return
	}

	localRepoPath := strings.TrimSpace(*binding.LocalRepoPath)
	branch := binding.DefaultBranch
	remoteSHA := ""
	if parsedBranch, parsedRemote := parseConflictContext(binding.DefaultBranch, binding.ConflictDetails); parsedBranch != "" {
		branch = parsedBranch
		remoteSHA = parsedRemote
	}

	var localHeadSHA string
	switch action {
	case "keep_github":
		localHeadSHA, err = resolveConflictKeepGitHub(r.Context(), localRepoPath, branch, remoteSHA)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		if strings.TrimSpace(localHeadSHA) != "" {
			_, _ = h.ProjectRepos.UpdateBranchCheckpoint(ctx, projectID, branch, localHeadSHA, time.Now().UTC())
		}
	case "keep_ottercamp":
		localHeadSHA, err = resolveConflictKeepOtterCamp(r.Context(), localRepoPath)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
	}

	resolvedAt := time.Now().UTC()
	details := mergeConflictResolutionDetails(
		binding.ConflictDetails,
		action,
		branch,
		localHeadSHA,
		remoteSHA,
		resolvedAt,
	)
	updated, err := h.ProjectRepos.SetConflictState(ctx, projectID, store.RepoConflictResolved, details)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}

	_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.repo_conflict_resolved", map[string]any{
		"project_id":      projectID,
		"resolution":      action,
		"branch":          branch,
		"local_head_sha":  localHeadSHA,
		"remote_head_sha": remoteSHA,
		"resolved_at":     resolvedAt,
	})

	var localHeadPtr *string
	if strings.TrimSpace(localHeadSHA) != "" {
		localHeadPtr = &localHeadSHA
	}
	sendJSON(w, http.StatusOK, githubConflictResolutionResponse{
		ProjectID:       projectID,
		Action:          action,
		ConflictState:   updated.ConflictState,
		ConflictDetails: updated.ConflictDetails,
		LocalHeadSHA:    localHeadPtr,
		ResolvedAt:      resolvedAt.Format(time.RFC3339),
	})
}

func (h *GitHubIntegrationHandler) PublishProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" || !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	var request githubPublishRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&request)
	if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, orgID)
	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	if err != nil {
		handleRepoStoreError(w, err)
		return
	}
	if !binding.Enabled {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "github integration is disabled for this project"})
		return
	}
	if binding.ConflictState == store.RepoConflictNeedsDecision {
		checks := []githubPublishCheck{
			{
				Name:     "conflict_state",
				Status:   "fail",
				Detail:   "Publish blocked: unresolved sync conflicts require resolution.",
				Blocking: true,
			},
		}
		_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_blocked", map[string]any{
			"project_id": projectID,
			"reason":     "unresolved_conflict",
			"dry_run":    request.DryRun,
		})
		sendJSON(w, http.StatusConflict, githubPublishResponse{
			ProjectID: projectID,
			DryRun:    request.DryRun,
			Status:    "blocked",
			Checks:    checks,
		})
		return
	}

	if binding.LocalRepoPath == nil || strings.TrimSpace(*binding.LocalRepoPath) == "" {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "project has no local repo path configured"})
		return
	}
	localRepoPath := strings.TrimSpace(*binding.LocalRepoPath)
	branch := strings.TrimSpace(binding.DefaultBranch)
	if branch == "" {
		branch = "main"
	}

	preflight, err := runPublishPreflightChecks(r.Context(), localRepoPath, branch)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	response := githubPublishResponse{
		ProjectID:     projectID,
		DryRun:        request.DryRun,
		Checks:        preflight.Checks,
		LocalHeadSHA:  optionalStringPtr(preflight.LocalHeadSHA),
		RemoteHeadSHA: optionalStringPtr(preflight.RemoteHeadSHA),
		CommitsAhead:  preflight.CommitsAhead,
		Status:        "dry_run",
	}

	if request.DryRun {
		_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_dry_run", map[string]any{
			"project_id":      projectID,
			"branch":          branch,
			"checks":          preflight.Checks,
			"commits_ahead":   preflight.CommitsAhead,
			"local_head_sha":  preflight.LocalHeadSHA,
			"remote_head_sha": preflight.RemoteHeadSHA,
		})
		sendJSON(w, http.StatusOK, response)
		return
	}

	if preflight.BlockingCount > 0 {
		response.Status = "blocked"
		_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_blocked", map[string]any{
			"project_id":      projectID,
			"branch":          branch,
			"checks":          preflight.Checks,
			"commits_ahead":   preflight.CommitsAhead,
			"local_head_sha":  preflight.LocalHeadSHA,
			"remote_head_sha": preflight.RemoteHeadSHA,
		})
		sendJSON(w, http.StatusConflict, response)
		return
	}

	if preflight.CommitsAhead == 0 {
		response.Status = "no_changes"
		_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_no_changes", map[string]any{
			"project_id":      projectID,
			"branch":          branch,
			"checks":          preflight.Checks,
			"commits_ahead":   preflight.CommitsAhead,
			"local_head_sha":  preflight.LocalHeadSHA,
			"remote_head_sha": preflight.RemoteHeadSHA,
		})
		sendJSON(w, http.StatusOK, response)
		return
	}

	if _, err := runGitInRepo(r.Context(), localRepoPath, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_failed", map[string]any{
			"project_id":      projectID,
			"branch":          branch,
			"error":           err.Error(),
			"checks":          preflight.Checks,
			"commits_ahead":   preflight.CommitsAhead,
			"local_head_sha":  preflight.LocalHeadSHA,
			"remote_head_sha": preflight.RemoteHeadSHA,
		})
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("publish push failed: %v", err)})
		return
	}

	publishedAt := time.Now().UTC().Format(time.RFC3339)
	response.Status = "published"
	response.PublishedAt = &publishedAt
	_ = logGitHubActivity(r.Context(), h.DB, orgID, &projectID, "github.publish_succeeded", map[string]any{
		"project_id":      projectID,
		"branch":          branch,
		"checks":          preflight.Checks,
		"commits_ahead":   preflight.CommitsAhead,
		"local_head_sha":  preflight.LocalHeadSHA,
		"remote_head_sha": preflight.RemoteHeadSHA,
		"published_at":    publishedAt,
	})
	sendJSON(w, http.StatusOK, response)
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
		"job_id":               job.ID,
		"repository_full_name": binding.RepositoryFullName,
		"branches":             branches,
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
	commitsIngested := 0
	if eventType == "push" && projectID != nil {
		if h.Commits != nil {
			ingested, ingestErr := h.ingestPushCommits(ctx, orgID, *projectID, payload)
			if ingestErr != nil {
				sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to ingest push commits"})
				return
			}
			commitsIngested = ingested
		}

		queued, queueErr := h.enqueuePushRepoSync(ctx, *projectID, payload, deliveryID)
		if queueErr != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to enqueue repo sync from push event"})
			return
		}
		repoSyncQueued = queued
	}

	issueSyncProcessed := false
	if eventType == "issues" || eventType == "pull_request" || eventType == "issue_comment" {
		if err := h.handleIssueWebhookEvent(ctx, orgID, projectID, eventType, body, deliveryID); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to process issue webhook event"})
			return
		}
		issueSyncProcessed = true
	}

	_ = logGitHubActivity(r.Context(), h.DB, orgID, projectID, "github.webhook."+eventType, map[string]any{
		"delivery_id":    deliveryID,
		"repository":     payload.Repository.FullName,
		"webhook_job_id": webhookJob.ID,
		"repo_sync":      repoSyncQueued,
		"commits":        commitsIngested,
		"issue_sync":     issueSyncProcessed,
	})

	sendJSON(w, http.StatusAccepted, map[string]any{
		"ok":               true,
		"event":            eventType,
		"delivery_id":      deliveryID,
		"webhook_job_id":   webhookJob.ID,
		"repo_sync_queued": repoSyncQueued,
		"commits_ingested": commitsIngested,
		"issue_sync":       issueSyncProcessed,
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

func (h *GitHubIntegrationHandler) ingestPushCommits(
	ctx context.Context,
	orgID string,
	projectID string,
	payload githubWebhookPayload,
) (int, error) {
	if h.Commits == nil || h.ProjectRepos == nil {
		return 0, nil
	}

	repositoryFullName := strings.TrimSpace(payload.Repository.FullName)
	branch := strings.TrimPrefix(strings.TrimSpace(payload.Ref), "refs/heads/")
	if repositoryFullName == "" || branch == "" {
		return 0, nil
	}

	createdCount := 0
	for _, item := range payload.Commits {
		sha := strings.TrimSpace(item.ID)
		if sha == "" {
			continue
		}

		subject, body, message := splitCommitMessage(item.Message)
		if message == "" {
			continue
		}

		authoredAt := time.Now().UTC()
		if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(item.Timestamp)); err == nil {
			authoredAt = parsed.UTC()
		}

		authorName := strings.TrimSpace(item.Author.Name)
		if authorName == "" {
			authorName = strings.TrimSpace(item.Author.Username)
		}
		if authorName == "" {
			authorName = "GitHub"
		}

		authorEmail := strings.TrimSpace(item.Author.Email)
		var authorEmailPtr *string
		if authorEmail != "" {
			authorEmailPtr = &authorEmail
		}

		metadata, err := json.Marshal(map[string]any{
			"url":      strings.TrimSpace(item.URL),
			"before":   strings.TrimSpace(payload.Before),
			"after":    strings.TrimSpace(payload.After),
			"added":    item.Added,
			"removed":  item.Removed,
			"modified": item.Modified,
		})
		if err != nil {
			return 0, err
		}

		commit, created, err := h.Commits.UpsertCommit(ctx, store.UpsertProjectCommitInput{
			ProjectID:          projectID,
			RepositoryFullName: repositoryFullName,
			BranchName:         branch,
			SHA:                sha,
			AuthorName:         authorName,
			AuthorEmail:        authorEmailPtr,
			AuthoredAt:         &authoredAt,
			Subject:            subject,
			Body:               body,
			Message:            message,
			Metadata:           metadata,
		})
		if err != nil {
			return 0, err
		}
		if created {
			createdCount++
			_ = logGitHubActivity(ctx, h.DB, orgID, &projectID, "github.commit.ingested", map[string]any{
				"project_id": projectID,
				"branch":     branch,
				"sha":        commit.SHA,
				"subject":    commit.Subject,
			})
		}
	}

	afterSHA := strings.TrimSpace(payload.After)
	if afterSHA != "" {
		if _, err := h.ProjectRepos.UpdateBranchCheckpoint(ctx, projectID, branch, afterSHA, time.Now().UTC()); err != nil {
			return createdCount, err
		}
	}

	return createdCount, nil
}

func splitCommitMessage(message string) (string, *string, string) {
	normalized := strings.TrimSpace(message)
	if normalized == "" {
		return "", nil, ""
	}
	subject := normalized
	var body *string
	if newline := strings.Index(normalized, "\n"); newline >= 0 {
		subject = strings.TrimSpace(normalized[:newline])
		remainder := strings.TrimSpace(normalized[newline+1:])
		if remainder != "" {
			body = &remainder
		}
	}
	if subject == "" {
		subject = normalized
	}
	return subject, body, normalized
}

func parseConflictContext(defaultBranch string, raw json.RawMessage) (string, string) {
	branch := strings.TrimSpace(defaultBranch)
	if branch == "" {
		branch = "main"
	}
	remoteSHA := ""
	if len(raw) == 0 {
		return branch, remoteSHA
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return branch, remoteSHA
	}
	if value, ok := payload["branch"].(string); ok && strings.TrimSpace(value) != "" {
		branch = strings.TrimSpace(value)
	}
	if value, ok := payload["remote_sha"].(string); ok && strings.TrimSpace(value) != "" {
		remoteSHA = strings.TrimSpace(value)
	}
	return branch, remoteSHA
}

func mergeConflictResolutionDetails(
	existing json.RawMessage,
	action string,
	branch string,
	localHeadSHA string,
	remoteSHA string,
	resolvedAt time.Time,
) json.RawMessage {
	payload := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &payload)
	}
	payload["branch"] = strings.TrimSpace(branch)
	payload["resolution"] = action
	payload["resolved_at"] = resolvedAt.UTC().Format(time.RFC3339)
	payload["local_head_sha"] = strings.TrimSpace(localHeadSHA)
	if strings.TrimSpace(remoteSHA) != "" {
		payload["remote_sha"] = strings.TrimSpace(remoteSHA)
	}
	payload["ready_to_publish"] = action == "keep_ottercamp"

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func resolveConflictKeepGitHub(
	ctx context.Context,
	localRepoPath string,
	branch string,
	remoteSHA string,
) (string, error) {
	if err := ensureGitRepoPath(localRepoPath); err != nil {
		return "", err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "main"
	}

	if _, err := runGitInRepo(ctx, localRepoPath, "fetch", "--prune", "origin"); err != nil {
		return "", fmt.Errorf("fetch origin failed: %w", err)
	}
	if _, err := runGitInRepo(ctx, localRepoPath, "checkout", branch); err != nil {
		if _, retryErr := runGitInRepo(ctx, localRepoPath, "checkout", "-B", branch, "origin/"+branch); retryErr != nil {
			return "", fmt.Errorf("checkout branch %q failed: %w", branch, retryErr)
		}
	}

	target := strings.TrimSpace(remoteSHA)
	if target == "" {
		target = "origin/" + branch
	}
	if _, err := runGitInRepo(ctx, localRepoPath, "reset", "--hard", target); err != nil {
		return "", fmt.Errorf("reset branch to %q failed: %w", target, err)
	}

	headSHA, err := runGitInRepo(ctx, localRepoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve local head failed: %w", err)
	}
	return strings.TrimSpace(headSHA), nil
}

func resolveConflictKeepOtterCamp(ctx context.Context, localRepoPath string) (string, error) {
	if err := ensureGitRepoPath(localRepoPath); err != nil {
		return "", err
	}
	headSHA, err := runGitInRepo(ctx, localRepoPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve local head failed: %w", err)
	}
	return strings.TrimSpace(headSHA), nil
}

func ensureGitRepoPath(localRepoPath string) error {
	localRepoPath = strings.TrimSpace(localRepoPath)
	if localRepoPath == "" {
		return fmt.Errorf("local repo path is required")
	}
	info, err := os.Stat(localRepoPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local repo path %q does not exist", localRepoPath)
		}
		return fmt.Errorf("local repo path %q is not accessible: %w", localRepoPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local repo path %q is not a directory", localRepoPath)
	}
	if _, err := os.Stat(filepath.Join(localRepoPath, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local repo path %q is not a git repository", localRepoPath)
		}
		return fmt.Errorf("local repo path %q failed git metadata check: %w", localRepoPath, err)
	}
	return nil
}

func runGitInRepo(ctx context.Context, localRepoPath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = localRepoPath
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %w (%s)", strings.Join(args, " "), err, trimmed)
	}
	return strings.TrimSpace(string(output)), nil
}

type publishPreflightResult struct {
	Checks        []githubPublishCheck
	LocalHeadSHA  string
	RemoteHeadSHA string
	CommitsAhead  int
	BlockingCount int
}

func runPublishPreflightChecks(
	ctx context.Context,
	localRepoPath string,
	branch string,
) (publishPreflightResult, error) {
	if err := ensureGitRepoPath(localRepoPath); err != nil {
		return publishPreflightResult{}, err
	}

	checks := make([]githubPublishCheck, 0, 5)
	if _, err := runGitInRepo(ctx, localRepoPath, "fetch", "--prune", "origin"); err != nil {
		checks = append(checks, githubPublishCheck{
			Name:     "fetch_origin",
			Status:   "fail",
			Detail:   err.Error(),
			Blocking: true,
		})
		return publishPreflightResult{
			Checks:        checks,
			BlockingCount: 1,
		}, nil
	}
	checks = append(checks, githubPublishCheck{
		Name:     "fetch_origin",
		Status:   "pass",
		Detail:   "Fetched latest remote refs.",
		Blocking: false,
	})

	localHeadSHA, err := runGitInRepo(ctx, localRepoPath, "rev-parse", "HEAD")
	if err != nil {
		return publishPreflightResult{}, fmt.Errorf("resolve local head failed: %w", err)
	}
	localHeadSHA = strings.TrimSpace(localHeadSHA)

	remoteHeadSHA := ""
	remoteHeadOutput, remoteErr := runGitInRepo(ctx, localRepoPath, "rev-parse", "origin/"+branch)
	if remoteErr == nil {
		remoteHeadSHA = strings.TrimSpace(remoteHeadOutput)
	}

	blockingCount := 0
	if remoteHeadSHA != "" {
		_, ffErr := runGitInRepo(ctx, localRepoPath, "merge-base", "--is-ancestor", remoteHeadSHA, localHeadSHA)
		if ffErr != nil {
			blockingCount++
			checks = append(checks, githubPublishCheck{
				Name:     "fast_forward",
				Status:   "fail",
				Detail:   "Remote branch is not an ancestor of local head.",
				Blocking: true,
			})
		} else {
			checks = append(checks, githubPublishCheck{
				Name:     "fast_forward",
				Status:   "pass",
				Detail:   "Remote branch can be fast-forwarded to local head.",
				Blocking: false,
			})
		}
	} else {
		checks = append(checks, githubPublishCheck{
			Name:     "fast_forward",
			Status:   "pass",
			Detail:   "Remote branch ref does not exist yet.",
			Blocking: false,
		})
	}

	commitsAhead := 0
	countOutput, countErr := runGitInRepo(ctx, localRepoPath, "rev-list", "--count", "origin/"+branch+"..HEAD")
	if countErr != nil {
		countOutput, countErr = runGitInRepo(ctx, localRepoPath, "rev-list", "--count", "HEAD")
	}
	if countErr == nil {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(countOutput)); parseErr == nil {
			commitsAhead = parsed
		}
	}
	checks = append(checks, githubPublishCheck{
		Name:     "commits_ahead",
		Status:   "info",
		Detail:   fmt.Sprintf("Local branch is %d commit(s) ahead of remote.", commitsAhead),
		Blocking: false,
	})

	_, dryRunPushErr := runGitInRepo(ctx, localRepoPath, "push", "--dry-run", "origin", "HEAD:refs/heads/"+branch)
	if dryRunPushErr != nil {
		blockingCount++
		checks = append(checks, githubPublishCheck{
			Name:     "push_dry_run",
			Status:   "fail",
			Detail:   dryRunPushErr.Error(),
			Blocking: true,
		})
	} else {
		checks = append(checks, githubPublishCheck{
			Name:     "push_dry_run",
			Status:   "pass",
			Detail:   "Dry-run push succeeded.",
			Blocking: false,
		})
	}

	return publishPreflightResult{
		Checks:        checks,
		LocalHeadSHA:  localHeadSHA,
		RemoteHeadSHA: remoteHeadSHA,
		CommitsAhead:  commitsAhead,
		BlockingCount: blockingCount,
	}, nil
}

func optionalStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
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
