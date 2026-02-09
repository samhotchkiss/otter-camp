package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// LabelsHandler handles org-scoped label CRUD operations.
type LabelsHandler struct {
	Store *store.LabelStore
	DB    *sql.DB
}

type listLabelsResponse struct {
	Labels []store.Label `json:"labels"`
}

type createLabelRequest struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type updateLabelRequest struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

type assignLabelsRequest struct {
	LabelIDs []string `json:"label_ids"`
}

func (h *LabelsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	seedRequested, err := strconv.ParseBool(strings.TrimSpace(r.URL.Query().Get("seed")))
	if err == nil && seedRequested {
		if seedErr := h.Store.EnsurePresetLabels(r.Context()); seedErr != nil {
			sendJSON(w, labelStoreErrorStatus(seedErr), errorResponse{Error: labelStoreErrorMessage(seedErr)})
			return
		}
	}

	labels, err := h.Store.List(r.Context())
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusOK, listLabelsResponse{Labels: labels})
}

func (h *LabelsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	var req createLabelRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "name is required"})
		return
	}

	label, err := h.Store.Create(r.Context(), req.Name, req.Color)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusCreated, label)
}

func (h *LabelsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	labelID := strings.TrimSpace(chi.URLParam(r, "id"))
	if labelID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "label id is required"})
		return
	}

	var req updateLabelRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return
	}

	label, err := h.Store.Update(r.Context(), labelID, req.Name, req.Color)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	sendJSON(w, http.StatusOK, label)
}

func (h *LabelsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	labelID := strings.TrimSpace(chi.URLParam(r, "id"))
	if labelID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "label id is required"})
		return
	}

	if err := h.Store.Delete(r.Context(), labelID); err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *LabelsHandler) ListProjectLabels(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	labels, err := h.Store.ListForProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, listLabelsResponse{Labels: labels})
}

func (h *LabelsHandler) AddProjectLabels(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	if projectID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project id is required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}

	labelIDs, ok := parseAssignLabelIDs(w, r)
	if !ok {
		return
	}

	for _, labelID := range labelIDs {
		if err := h.Store.AddToProject(r.Context(), projectID, labelID); err != nil {
			sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
			return
		}
	}

	labels, err := h.Store.ListForProject(r.Context(), projectID)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, listLabelsResponse{Labels: labels})
}

func (h *LabelsHandler) RemoveProjectLabel(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "id"))
	labelID := strings.TrimSpace(chi.URLParam(r, "lid"))
	if projectID == "" || labelID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project and label ids are required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	if !uuidRegex.MatchString(labelID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid label id"})
		return
	}

	if err := h.Store.RemoveFromProject(r.Context(), projectID, labelID); err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *LabelsHandler) ListIssueLabels(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "pid"))
	issueID := strings.TrimSpace(chi.URLParam(r, "iid"))
	if projectID == "" || issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project and issue ids are required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}
	if !h.ensureIssueBelongsProject(w, r, projectID, issueID) {
		return
	}

	labels, err := h.Store.ListForIssue(r.Context(), issueID)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, listLabelsResponse{Labels: labels})
}

func (h *LabelsHandler) AddIssueLabels(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "pid"))
	issueID := strings.TrimSpace(chi.URLParam(r, "iid"))
	if projectID == "" || issueID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project and issue ids are required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}
	if !h.ensureIssueBelongsProject(w, r, projectID, issueID) {
		return
	}

	labelIDs, ok := parseAssignLabelIDs(w, r)
	if !ok {
		return
	}

	for _, labelID := range labelIDs {
		if err := h.Store.AddToIssue(r.Context(), issueID, labelID); err != nil {
			sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
			return
		}
	}

	labels, err := h.Store.ListForIssue(r.Context(), issueID)
	if err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}
	sendJSON(w, http.StatusOK, listLabelsResponse{Labels: labels})
}

func (h *LabelsHandler) RemoveIssueLabel(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
	}

	projectID := strings.TrimSpace(chi.URLParam(r, "pid"))
	issueID := strings.TrimSpace(chi.URLParam(r, "iid"))
	labelID := strings.TrimSpace(chi.URLParam(r, "lid"))
	if projectID == "" || issueID == "" || labelID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "project, issue, and label ids are required"})
		return
	}
	if !uuidRegex.MatchString(projectID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid project id"})
		return
	}
	if !uuidRegex.MatchString(issueID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid issue id"})
		return
	}
	if !uuidRegex.MatchString(labelID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid label id"})
		return
	}
	if !h.ensureIssueBelongsProject(w, r, projectID, issueID) {
		return
	}

	if err := h.Store.RemoveFromIssue(r.Context(), issueID, labelID); err != nil {
		sendJSON(w, labelStoreErrorStatus(err), errorResponse{Error: labelStoreErrorMessage(err)})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func labelStoreErrorStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "duplicate key value"):
		return http.StatusConflict
	case strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "cannot be empty"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func labelStoreErrorMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		return "missing org_id"
	case errors.Is(err, store.ErrNotFound):
		return "label not found"
	case strings.Contains(err.Error(), "duplicate key value"):
		return "label already exists"
	default:
		return "failed to process label request"
	}
}

func parseAssignLabelIDs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req assignLabelsRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON payload"})
		return nil, false
	}
	if len(req.LabelIDs) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "label_ids is required"})
		return nil, false
	}

	deduped := make([]string, 0, len(req.LabelIDs))
	seen := make(map[string]struct{}, len(req.LabelIDs))
	for _, raw := range req.LabelIDs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		if !uuidRegex.MatchString(trimmed) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid label_ids"})
			return nil, false
		}
		seen[trimmed] = struct{}{}
		deduped = append(deduped, trimmed)
	}
	if len(deduped) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "label_ids is required"})
		return nil, false
	}
	return deduped, true
}

func (h *LabelsHandler) ensureIssueBelongsProject(w http.ResponseWriter, r *http.Request, projectID, issueID string) bool {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return false
	}

	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "missing org_id"})
		return false
	}

	var exists bool
	err := h.DB.QueryRowContext(r.Context(), `
		SELECT EXISTS (
			SELECT 1 FROM project_issues
			WHERE id = $1
			  AND project_id = $2
			  AND org_id = $3
		)
	`, issueID, projectID, workspaceID).Scan(&exists)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to validate issue scope"})
		return false
	}
	if !exists {
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "issue not found"})
		return false
	}
	return true
}
