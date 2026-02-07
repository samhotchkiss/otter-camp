package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type GitHubSyncDeadLettersHandler struct {
	Store *store.GitHubSyncJobStore
}

type listDeadLettersResponse struct {
	DeadLetters []store.GitHubSyncDeadLetter `json:"dead_letters"`
	Total       int                          `json:"total"`
}

func (h *GitHubSyncDeadLettersHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	deadLetters, err := h.Store.ListDeadLetters(r.Context(), limit)
	if err != nil {
		handleDeadLetterStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, listDeadLettersResponse{
		DeadLetters: deadLetters,
		Total:       len(deadLetters),
	})
}

func (h *GitHubSyncDeadLettersHandler) Replay(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	deadLetterID := strings.TrimSpace(chi.URLParam(r, "id"))
	if deadLetterID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "dead letter id is required"})
		return
	}

	replayedBy := middleware.UserFromContext(r.Context())
	var replayedByPtr *string
	if replayedBy != "" {
		replayedByPtr = &replayedBy
	}

	job, err := h.Store.ReplayDeadLetter(r.Context(), deadLetterID, replayedByPtr)
	if err != nil {
		handleDeadLetterStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, job)
}

func handleDeadLetterStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "dead letter not found"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "github dead-letter operation failed"})
	}
}
