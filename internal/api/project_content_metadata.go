package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

type projectContentMetadataResponse struct {
	Path         string             `json:"path"`
	Exists       bool               `json:"exists"`
	EditorMode   editorMode         `json:"editor_mode"`
	Capabilities editorCapabilities `json:"capabilities"`
	Extension    string             `json:"extension"`
	MimeType     string             `json:"mime_type,omitempty"`
	SizeBytes    int64              `json:"size_bytes,omitempty"`
	ModifiedAt   string             `json:"modified_at,omitempty"`
}

func (h *ProjectChatHandler) GetContentMetadata(w http.ResponseWriter, r *http.Request) {
	if h.ProjectStore == nil {
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

	requestedPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if requestedPath == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path is required"})
		return
	}

	normalizedPath, err := validateContentReadPath(requestedPath)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	resolution := resolveEditorForPath(normalizedPath)
	absolutePath := filepath.Join(contentRootPath(), projectID, filepath.FromSlash(strings.TrimPrefix(normalizedPath, "/")))
	info, statErr := os.Stat(absolutePath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to inspect content file"})
		return
	}
	if statErr == nil && info.IsDir() {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "path must point to a file"})
		return
	}

	response := projectContentMetadataResponse{
		Path:         normalizedPath,
		Exists:       statErr == nil,
		EditorMode:   resolution.Mode,
		Capabilities: resolution.Capabilities,
		Extension:    resolution.Extension,
		MimeType:     resolution.MimeType,
	}
	if statErr == nil {
		response.SizeBytes = info.Size()
		response.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}

	sendJSON(w, http.StatusOK, response)
}
