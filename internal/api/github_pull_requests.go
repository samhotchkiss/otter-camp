package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type GitHubPullRequestsHandler struct {
	Store        *store.GitHubIssuePRStore
	ProjectRepos *store.ProjectRepoStore
}

type githubPullRequestListItem struct {
	Kind               string  `json:"kind"`
	ID                 string  `json:"id"`
	RepositoryFullName string  `json:"repository_full_name"`
	Number             int64   `json:"number"`
	Title              string  `json:"title"`
	State              string  `json:"state"`
	Badge              string  `json:"badge"`
	Draft              bool    `json:"draft"`
	Merged             bool    `json:"merged"`
	HeadRef            string  `json:"head_ref"`
	BaseRef            string  `json:"base_ref"`
	MergedCommitSHA    *string `json:"merged_commit_sha,omitempty"`
	AuthorLogin        *string `json:"author_login,omitempty"`
}

type githubPullRequestListResponse struct {
	Items           []githubPullRequestListItem `json:"items"`
	Total           int                         `json:"total"`
	Mode            string                      `json:"mode"`
	GitHubPREnabled bool                        `json:"github_pr_enabled"`
}

func (h *GitHubPullRequestsHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	mode, err := h.resolveProjectReviewMode(r.Context(), projectID)
	if err != nil {
		handlePullRequestStoreError(w, err)
		return
	}
	if !mode.GitHubPREnabled {
		sendJSON(w, http.StatusOK, githubPullRequestListResponse{
			Items:           []githubPullRequestListItem{},
			Total:           0,
			Mode:            mode.Mode,
			GitHubPREnabled: false,
		})
		return
	}

	var stateFilter *string
	if raw := strings.TrimSpace(r.URL.Query().Get("state")); raw != "" {
		stateFilter = &raw
	}

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	records, err := h.Store.ListPullRequests(r.Context(), projectID, stateFilter, limit)
	if err != nil {
		handlePullRequestStoreError(w, err)
		return
	}

	items := make([]githubPullRequestListItem, 0, len(records))
	for _, record := range records {
		items = append(items, githubPullRequestListItem{
			Kind:               "pull_request",
			ID:                 record.ID,
			RepositoryFullName: record.RepositoryFullName,
			Number:             record.GitHubNumber,
			Title:              record.Title,
			State:              record.State,
			Badge:              pullRequestBadge(record),
			Draft:              record.Draft,
			Merged:             record.Merged,
			HeadRef:            record.HeadRef,
			BaseRef:            record.BaseRef,
			MergedCommitSHA:    record.MergedCommitSHA,
			AuthorLogin:        record.AuthorLogin,
		})
	}

	sendJSON(w, http.StatusOK, githubPullRequestListResponse{
		Items:           items,
		Total:           len(items),
		Mode:            mode.Mode,
		GitHubPREnabled: true,
	})
}

func (h *GitHubPullRequestsHandler) CreateForProject(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}

	mode, err := h.resolveProjectReviewMode(r.Context(), projectID)
	if err != nil {
		handlePullRequestStoreError(w, err)
		return
	}

	if !mode.GitHubPREnabled {
		sendJSON(w, http.StatusConflict, map[string]any{
			"error":             "github PR creation is disabled for local issue-review mode",
			"mode":              mode.Mode,
			"github_pr_enabled": false,
		})
		return
	}

	sendJSON(w, http.StatusNotImplemented, map[string]any{
		"error":             "github PR creation endpoint is not implemented yet",
		"mode":              mode.Mode,
		"github_pr_enabled": true,
	})
}

func (h *GitHubPullRequestsHandler) resolveProjectReviewMode(ctx context.Context, projectID string) (reviewModeDecision, error) {
	defaultMode := resolveReviewMode(store.RepoSyncModeSync)
	if h.ProjectRepos == nil {
		return defaultMode, nil
	}

	binding, err := h.ProjectRepos.GetBinding(ctx, projectID)
	switch {
	case err == nil:
		return resolveReviewMode(binding.SyncMode), nil
	case errors.Is(err, store.ErrNotFound):
		return defaultMode, nil
	default:
		return reviewModeDecision{}, err
	}
}

func handlePullRequestStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}

func pullRequestBadge(record store.GitHubPullRequestRecord) string {
	if record.Merged {
		return "merged"
	}
	if record.Draft {
		return "draft"
	}
	if strings.EqualFold(record.State, "closed") {
		return "closed"
	}
	return "open"
}
