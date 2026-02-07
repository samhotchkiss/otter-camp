package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

type projectContentRenameRequest struct {
	FromPath string `json:"from_path"`
	ToPath   string `json:"to_path"`
}

type projectContentDeleteRequest struct {
	Path       string `json:"path"`
	HardDelete bool   `json:"hard_delete"`
}

type projectContentRenameResponse struct {
	FromPath            string `json:"from_path"`
	ToPath              string `json:"to_path"`
	LinkedIssuesUpdated int    `json:"linked_issues_updated"`
}

type projectContentDeleteResponse struct {
	Path           string  `json:"path"`
	Deleted        bool    `json:"deleted"`
	DetachedIssues int     `json:"detached_issues"`
	Warning        *string `json:"warning,omitempty"`
}

func (h *ProjectChatHandler) RenameContent(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	var req projectContentRenameRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	fromPath, err := validateContentReadPath(req.FromPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	toPath, err := validateContentWritePath(req.ToPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if !strings.HasPrefix(fromPath, "/posts/") || !strings.HasPrefix(toPath, "/posts/") {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "rename currently supports /posts paths only"})
		return
	}
	if fromPath == toPath {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "from_path and to_path must differ"})
		return
	}

	fromAbsolute := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(fromPath, "/")))
	toAbsolute := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(toPath, "/")))

	info, statErr := os.Stat(fromAbsolute)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "source file not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to inspect source file"})
		return
	}
	if info.IsDir() {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "from_path must point to a file"})
		return
	}

	if err := os.MkdirAll(filepath.Dir(toAbsolute), 0o755); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to prepare destination path"})
		return
	}
	if err := os.Rename(fromAbsolute, toAbsolute); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to rename content file"})
		return
	}

	linkedIssues, err := h.IssueStore.ListIssuesByDocumentPath(r.Context(), projectID, fromPath)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	updatedCount := 0
	for _, issue := range linkedIssues {
		if _, err := h.IssueStore.UpdateIssueDocumentPath(r.Context(), issue.ID, &toPath); err != nil {
			handleIssueStoreError(w, err)
			return
		}
		updatedCount++
		h.logIssueLinkageActivity(r.Context(), &projectID, "issue.document_link_renamed", map[string]any{
			"issue_id":   issue.ID,
			"project_id": projectID,
			"from_path":  fromPath,
			"to_path":    toPath,
		})
	}

	sendJSON(w, http.StatusOK, projectContentRenameResponse{
		FromPath:            fromPath,
		ToPath:              toPath,
		LinkedIssuesUpdated: updatedCount,
	})
}

func (h *ProjectChatHandler) DeleteContent(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil || h.IssueStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if err := h.requireProjectAccess(r.Context(), projectID); err != nil {
		handleProjectChatStoreError(w, err)
		return
	}

	var req projectContentDeleteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	documentPath, err := validateContentReadPath(req.Path)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if !strings.HasPrefix(documentPath, "/posts/") {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "delete currently supports /posts paths only"})
		return
	}

	absolutePath := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(documentPath, "/")))
	info, statErr := os.Stat(absolutePath)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "file not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to inspect content file"})
		return
	}
	if info.IsDir() {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path must point to a file"})
		return
	}

	linkedIssues, err := h.IssueStore.ListIssuesByDocumentPath(r.Context(), projectID, documentPath)
	if err != nil {
		handleIssueStoreError(w, err)
		return
	}

	if req.HardDelete {
		for _, issue := range linkedIssues {
			if issue.State == "open" {
				sendJSON(w, http.StatusConflict, errorResponse{Error: "cannot hard-delete post linked to an active review issue"})
				return
			}
		}
	}

	if err := os.Remove(absolutePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			sendJSON(w, http.StatusNotFound, errorResponse{Error: "file not found"})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to delete content file"})
		return
	}

	detachedCount := 0
	var warning *string
	if len(linkedIssues) > 0 {
		message := "linked review issues were detached because this post was deleted"
		warning = &message
		for _, issue := range linkedIssues {
			if _, err := h.IssueStore.UpdateIssueDocumentPath(r.Context(), issue.ID, nil); err != nil {
				handleIssueStoreError(w, err)
				return
			}
			detachedCount++
			h.logIssueLinkageActivity(r.Context(), &projectID, "issue.document_link_detached", map[string]any{
				"issue_id":   issue.ID,
				"project_id": projectID,
				"path":       documentPath,
				"reason":     "document_deleted",
				"warning":    message,
			})
		}
	}

	sendJSON(w, http.StatusOK, projectContentDeleteResponse{
		Path:           documentPath,
		Deleted:        true,
		DetachedIssues: detachedCount,
		Warning:        warning,
	})
}

func (h *ProjectChatHandler) logIssueLinkageActivity(ctx context.Context, projectID *string, action string, metadata map[string]any) {
	if h.DB == nil {
		return
	}
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return
	}
	_ = logGitHubActivity(ctx, h.DB, workspaceID, projectID, action, metadata)
}
