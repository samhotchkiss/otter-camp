package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ProjectCommitsHandler struct {
	ProjectStore *store.ProjectStore
	CommitStore  *store.ProjectCommitStore
	ProjectRepos *store.ProjectRepoStore
}

type projectCommitPayload struct {
	ID                 string  `json:"id"`
	ProjectID          string  `json:"project_id"`
	RepositoryFullName string  `json:"repository_full_name"`
	BranchName         string  `json:"branch_name"`
	SHA                string  `json:"sha"`
	ParentSHA          *string `json:"parent_sha,omitempty"`
	AuthorName         string  `json:"author_name"`
	AuthorEmail        *string `json:"author_email,omitempty"`
	AuthoredAt         string  `json:"authored_at"`
	Subject            string  `json:"subject"`
	Body               *string `json:"body,omitempty"`
	Message            string  `json:"message"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

type projectCommitListResponse struct {
	Items      []projectCommitPayload `json:"items"`
	HasMore    bool                   `json:"has_more"`
	NextOffset *int                   `json:"next_offset,omitempty"`
	Limit      int                    `json:"limit"`
	Offset     int                    `json:"offset"`
	Total      int                    `json:"total"`
}

type projectCommitDiffFile struct {
	Path       string  `json:"path"`
	ChangeType string  `json:"change_type"`
	Patch      *string `json:"patch,omitempty"`
}

type projectCommitDiffResponse struct {
	SHA   string                  `json:"sha"`
	Files []projectCommitDiffFile `json:"files"`
	Total int                     `json:"total"`
}

func (h *ProjectCommitsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.CommitStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if _, err := h.ProjectStore.GetByID(r.Context(), projectID); err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 200 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		limit = parsed
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid offset"})
			return
		}
		offset = parsed
	}

	var branch *string
	if raw := strings.TrimSpace(r.URL.Query().Get("branch")); raw != "" {
		branch = &raw
	}

	rows, err := h.CommitStore.ListCommits(r.Context(), store.ProjectCommitFilter{
		ProjectID: projectID,
		Branch:    branch,
		Limit:     limit + 1,
		Offset:    offset,
	})
	if err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]projectCommitPayload, 0, len(rows))
	for _, row := range rows {
		items = append(items, toProjectCommitPayload(row))
	}

	var nextOffset *int
	if hasMore {
		value := offset + limit
		nextOffset = &value
	}

	sendJSON(w, http.StatusOK, projectCommitListResponse{
		Items:      items,
		HasMore:    hasMore,
		NextOffset: nextOffset,
		Limit:      limit,
		Offset:     offset,
		Total:      len(items),
	})
}

func (h *ProjectCommitsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.CommitStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if _, err := h.ProjectStore.GetByID(r.Context(), projectID); err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	sha := strings.TrimSpace(chi.URLParam(r, "sha"))
	if sha == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "sha is required"})
		return
	}

	commit, err := h.CommitStore.GetCommitBySHA(r.Context(), projectID, sha)
	if err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, toProjectCommitPayload(*commit))
}

func (h *ProjectCommitsHandler) Diff(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.CommitStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if _, err := h.ProjectStore.GetByID(r.Context(), projectID); err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	sha := strings.TrimSpace(chi.URLParam(r, "sha"))
	if sha == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "sha is required"})
		return
	}

	commit, err := h.CommitStore.GetCommitBySHA(r.Context(), projectID, sha)
	if err != nil {
		handleProjectCommitStoreError(w, err)
		return
	}

	files := commitDiffFilesFromMetadata(commit.Metadata)
	sendJSON(w, http.StatusOK, projectCommitDiffResponse{
		SHA:   commit.SHA,
		Files: files,
		Total: len(files),
	})
}

func toProjectCommitPayload(commit store.ProjectCommit) projectCommitPayload {
	return projectCommitPayload{
		ID:                 commit.ID,
		ProjectID:          commit.ProjectID,
		RepositoryFullName: commit.RepositoryFullName,
		BranchName:         commit.BranchName,
		SHA:                commit.SHA,
		ParentSHA:          commit.ParentSHA,
		AuthorName:         commit.AuthorName,
		AuthorEmail:        commit.AuthorEmail,
		AuthoredAt:         commit.AuthoredAt.UTC().Format(time.RFC3339),
		Subject:            commit.Subject,
		Body:               commit.Body,
		Message:            commit.Message,
		CreatedAt:          commit.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:          commit.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func commitDiffFilesFromMetadata(raw json.RawMessage) []projectCommitDiffFile {
	type metadataFile struct {
		Path       string  `json:"path"`
		Filename   string  `json:"filename"`
		ChangeType string  `json:"change_type"`
		Status     string  `json:"status"`
		Patch      *string `json:"patch"`
	}
	var metadata struct {
		Added    []string       `json:"added"`
		Modified []string       `json:"modified"`
		Removed  []string       `json:"removed"`
		Files    []metadataFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return []projectCommitDiffFile{}
	}

	files := make([]projectCommitDiffFile, 0, len(metadata.Added)+len(metadata.Modified)+len(metadata.Removed)+len(metadata.Files))
	for _, path := range metadata.Added {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			files = append(files, projectCommitDiffFile{Path: trimmed, ChangeType: "added"})
		}
	}
	for _, path := range metadata.Modified {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			files = append(files, projectCommitDiffFile{Path: trimmed, ChangeType: "modified"})
		}
	}
	for _, path := range metadata.Removed {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			files = append(files, projectCommitDiffFile{Path: trimmed, ChangeType: "removed"})
		}
	}
	for _, file := range metadata.Files {
		normalizedType := normalizeDiffChangeType(file.ChangeType)
		if normalizedType == "" {
			normalizedType = normalizeDiffChangeType(file.Status)
		}
		if normalizedType == "" {
			continue
		}
		path := strings.TrimSpace(file.Path)
		if path == "" {
			path = strings.TrimSpace(file.Filename)
		}
		if path == "" {
			continue
		}
		files = append(files, projectCommitDiffFile{
			Path:       path,
			ChangeType: normalizedType,
			Patch:      trimOptionalString(file.Patch),
		})
	}

	order := map[string]int{"added": 0, "modified": 1, "removed": 2, "renamed": 3}
	sort.SliceStable(files, func(i, j int) bool {
		left := files[i]
		right := files[j]
		if order[left.ChangeType] == order[right.ChangeType] {
			return left.Path < right.Path
		}
		return order[left.ChangeType] < order[right.ChangeType]
	})

	return files
}

func normalizeDiffChangeType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "added":
		return "added"
	case "modified", "changed":
		return "modified"
	case "removed", "deleted":
		return "removed"
	case "renamed":
		return "renamed"
	default:
		return ""
	}
}

func trimOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func handleProjectCommitStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	default:
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	}
}
