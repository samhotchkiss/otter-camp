package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type GitHubPullRequestsHandler struct {
	Store *store.GitHubIssuePRStore
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
	Items []githubPullRequestListItem `json:"items"`
	Total int                         `json:"total"`
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

	sendJSON(w, http.StatusOK, githubPullRequestListResponse{Items: items, Total: len(items)})
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
