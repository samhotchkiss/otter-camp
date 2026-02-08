package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

// LabelsHandler handles org-scoped label CRUD operations.
type LabelsHandler struct {
	Store *store.LabelStore
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

func (h *LabelsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "labels store unavailable"})
		return
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
