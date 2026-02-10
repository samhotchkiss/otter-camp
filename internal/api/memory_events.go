package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type MemoryEventsHandler struct {
	Store *store.MemoryEventsStore
}

type memoryEventPayload struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

type memoryEventsListResponse struct {
	Items []memoryEventPayload `json:"items"`
	Total int                  `json:"total"`
}

func (h *MemoryEventsHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	limit := 100
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	var sincePtr *time.Time
	if rawSince := strings.TrimSpace(r.URL.Query().Get("since")); rawSince != "" {
		parsed, err := time.Parse(time.RFC3339, rawSince)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "since must be RFC3339"})
			return
		}
		utc := parsed.UTC()
		sincePtr = &utc
	}

	events, err := h.Store.List(r.Context(), store.ListMemoryEventsParams{
		Since: sincePtr,
		Types: splitCSV(r.URL.Query().Get("types")),
		Limit: limit,
	})
	if err != nil {
		handleMemoryEventsStoreError(w, err)
		return
	}

	items := make([]memoryEventPayload, 0, len(events))
	for _, event := range events {
		items = append(items, memoryEventPayload{
			ID:        event.ID,
			EventType: event.EventType,
			Payload:   event.Payload,
			CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	sendJSON(w, http.StatusOK, memoryEventsListResponse{
		Items: items,
		Total: len(items),
	})
}

func handleMemoryEventsStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrMemoryEventTypeInvalid):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory events operation failed"})
	}
}
