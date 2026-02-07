// Package gitserver implements Smart HTTP git protocol endpoints.
package gitserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
)

var uuidRegex = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Handler handles git Smart HTTP requests.
type Handler struct {
	// RepoResolver maps (orgID, projectID) to local repo path.
	RepoResolver func(ctx context.Context, orgID, projectID string) (string, error)
	// ActivityStore records git push activity.
	ActivityStore ActivityLogger
	// ProjectRepos provides access to GitHub repo bindings.
	ProjectRepos RepoBindingStore
	// SyncJobs enqueues GitHub sync jobs.
	SyncJobs GitHubSyncJobEnqueuer
	// Hub broadcasts websocket notifications.
	Hub *ws.Hub
}

// ActivityLogger writes activity log entries.
type ActivityLogger interface {
	CreateWithWorkspaceID(ctx context.Context, workspaceID string, input store.CreateActivityInput) (*store.Activity, error)
}

// RepoBindingStore loads repo bindings for projects.
type RepoBindingStore interface {
	GetBinding(ctx context.Context, projectID string) (*store.ProjectRepoBinding, error)
}

// RepoBranchLister optionally lists active branches for a project.
type RepoBranchLister interface {
	ListActiveBranches(ctx context.Context, projectID string) ([]store.ProjectRepoActiveBranch, error)
}

// GitHubSyncJobEnqueuer queues GitHub sync jobs.
type GitHubSyncJobEnqueuer interface {
	Enqueue(ctx context.Context, input store.EnqueueGitHubSyncJobInput) (*store.GitHubSyncJob, error)
}

// Routes returns a chi router for git endpoints.
// Mount at /git
func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()

	r.Get("/{org}/{project}.git/info/refs", h.InfoRefs)
	r.Post("/{org}/{project}.git/git-upload-pack", h.UploadPack)
	r.Post("/{org}/{project}.git/git-receive-pack", h.ReceivePack)

	return r
}

// InfoRefs handles GET /info/refs?service=git-upload-pack|git-receive-pack
func (h *Handler) InfoRefs(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")
	service := r.URL.Query().Get("service")

	if !uuidRegex.MatchString(org) || !uuidRegex.MatchString(project) {
		http.Error(w, "Invalid org or project", http.StatusBadRequest)
		return
	}

	serviceCmd, err := resolveGitService(service)
	if err != nil {
		http.Error(w, "Invalid service", http.StatusBadRequest)
		return
	}

	if authOrg := OrgIDFromContext(r.Context()); authOrg == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	} else if authOrg != org {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !permissionAllowsRead(r.Context(), project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoPath, err := h.RepoResolver(r.Context(), org, project)
	if err != nil {
		handleRepoError(w, err)
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.WriteHeader(http.StatusOK)

	pktLine := fmt.Sprintf("# service=%s\n", service)
	fmt.Fprintf(w, "%04x%s0000", len(pktLine)+4, pktLine)

	cmd := exec.Command("git", serviceCmd, "--stateless-rpc", "--advertise-refs", repoPath)
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		http.Error(w, "git service failed", http.StatusInternalServerError)
		return
	}
}

// UploadPack handles POST /git-upload-pack (clone/fetch)
func (h *Handler) UploadPack(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")

	if !uuidRegex.MatchString(org) || !uuidRegex.MatchString(project) {
		http.Error(w, "Invalid org or project", http.StatusBadRequest)
		return
	}

	if authOrg := OrgIDFromContext(r.Context()); authOrg == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	} else if authOrg != org {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !permissionAllowsRead(r.Context(), project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoPath, err := h.RepoResolver(r.Context(), org, project)
	if err != nil {
		handleRepoError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-upload-pack-result")

	cmd := exec.Command("git", "upload-pack", "--stateless-rpc", repoPath)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		http.Error(w, "git upload-pack failed", http.StatusInternalServerError)
		return
	}
}

// ReceivePack handles POST /git-receive-pack (push)
func (h *Handler) ReceivePack(w http.ResponseWriter, r *http.Request) {
	org := chi.URLParam(r, "org")
	project := chi.URLParam(r, "project")

	if !uuidRegex.MatchString(org) || !uuidRegex.MatchString(project) {
		http.Error(w, "Invalid org or project", http.StatusBadRequest)
		return
	}

	if authOrg := OrgIDFromContext(r.Context()); authOrg == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="OtterCamp Git"`)
		http.Error(w, "Authentication required", http.StatusUnauthorized)
		return
	} else if authOrg != org {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !permissionAllowsWrite(r.Context(), project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoPath, err := h.RepoResolver(r.Context(), org, project)
	if err != nil {
		handleRepoError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")

	cmd := exec.Command("git", "receive-pack", "--stateless-rpc", repoPath)
	cmd.Stdin = r.Body
	cmd.Stdout = w
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		http.Error(w, "git receive-pack failed", http.StatusInternalServerError)
		return
	}

	userID := UserIDFromContext(r.Context())
	h.postReceive(r.Context(), org, project, userID)
}

func resolveGitService(service string) (string, error) {
	switch strings.TrimSpace(service) {
	case "git-upload-pack":
		return "upload-pack", nil
	case "git-receive-pack":
		return "receive-pack", nil
	default:
		return "", fmt.Errorf("invalid git service")
	}
}

func (h *Handler) postReceive(ctx context.Context, orgID, projectID, userID string) {
	h.logPushActivity(ctx, orgID, projectID, userID)
	h.broadcastPush(ctx, orgID, projectID, userID)
	h.enqueueGitHubSync(ctx, orgID, projectID)
}

func (h *Handler) logPushActivity(ctx context.Context, orgID, projectID, userID string) {
	if h.ActivityStore == nil {
		return
	}
	metadata, err := json.Marshal(map[string]any{
		"project_id": strings.TrimSpace(projectID),
		"user_id":    strings.TrimSpace(userID),
		"pushed_at":  time.Now().UTC(),
	})
	if err != nil {
		return
	}
	if _, err := h.ActivityStore.CreateWithWorkspaceID(ctx, orgID, store.CreateActivityInput{
		Action:   "git.push",
		Metadata: metadata,
	}); err != nil {
		log.Printf("[gitserver] activity log failed: %v", err)
	}
}

func (h *Handler) broadcastPush(ctx context.Context, orgID, projectID, userID string) {
	if h.Hub == nil {
		return
	}
	payload, err := json.Marshal(gitPushEvent{
		Type:      ws.MessageGitPush,
		OrgID:     strings.TrimSpace(orgID),
		ProjectID: strings.TrimSpace(projectID),
		UserID:    strings.TrimSpace(userID),
	})
	if err != nil {
		return
	}
	h.Hub.Broadcast(orgID, payload)
}

func (h *Handler) enqueueGitHubSync(ctx context.Context, orgID, projectID string) {
	if h.ProjectRepos == nil || h.SyncJobs == nil {
		return
	}
	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, orgID)
	binding, err := h.ProjectRepos.GetBinding(workspaceCtx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		log.Printf("[gitserver] repo binding lookup failed: %v", err)
		return
	}
	if !binding.Enabled {
		return
	}

	branches := []string{}
	branchSet := map[string]struct{}{}
	defaultBranch := strings.TrimSpace(binding.DefaultBranch)
	if defaultBranch != "" {
		branchSet[defaultBranch] = struct{}{}
		branches = append(branches, defaultBranch)
	}

	if lister, ok := h.ProjectRepos.(RepoBranchLister); ok {
		active, err := lister.ListActiveBranches(workspaceCtx, projectID)
		if err != nil {
			log.Printf("[gitserver] active branch lookup failed: %v", err)
		} else {
			for _, entry := range active {
				branch := strings.TrimSpace(entry.BranchName)
				if branch == "" {
					continue
				}
				if _, exists := branchSet[branch]; exists {
					continue
				}
				branchSet[branch] = struct{}{}
				branches = append(branches, branch)
			}
		}
	}

	payload, err := json.Marshal(map[string]any{
		"reason":               "git_push",
		"requested_at":         time.Now().UTC(),
		"repository_full_name": binding.RepositoryFullName,
		"default_branch":       binding.DefaultBranch,
		"branches":             branches,
	})
	if err != nil {
		return
	}

	if _, err := h.SyncJobs.Enqueue(workspaceCtx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeRepoSync,
		Payload:     payload,
		MaxAttempts: 5,
	}); err != nil {
		log.Printf("[gitserver] github sync enqueue failed: %v", err)
	}
}

type gitPushEvent struct {
	Type      ws.MessageType `json:"type"`
	OrgID     string         `json:"org_id"`
	ProjectID string         `json:"project_id"`
	UserID    string         `json:"user_id"`
}

func handleRepoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrForbidden):
		http.Error(w, "Forbidden", http.StatusForbidden)
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "Repository not found", http.StatusNotFound)
	default:
		http.Error(w, "Repository error", http.StatusInternalServerError)
	}
}

func permissionAllowsRead(ctx context.Context, projectID string) bool {
	perm, ok := ProjectPermissionFor(ctx, projectID)
	if !ok {
		return false
	}
	return perm == PermissionRead || perm == PermissionWrite
}

func permissionAllowsWrite(ctx context.Context, projectID string) bool {
	perm, ok := ProjectPermissionFor(ctx, projectID)
	if !ok {
		return false
	}
	return perm == PermissionWrite
}
