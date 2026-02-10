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

type KnowledgeHandler struct {
	Store *store.KnowledgeEntryStore
}

type knowledgeEntryPayload struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

type knowledgeListResponse struct {
	Items []knowledgeEntryPayload `json:"items"`
	Total int                     `json:"total"`
}

type knowledgeImportRequest struct {
	Entries []knowledgeImportEntry `json:"entries"`
}

type knowledgeImportEntry struct {
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags"`
	CreatedBy string   `json:"created_by"`
}

type knowledgeImportResponse struct {
	Inserted int `json:"inserted"`
}

func (h *KnowledgeHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	entries, err := h.Store.ListEntries(r.Context(), limit)
	if err != nil {
		handleKnowledgeStoreError(w, err)
		return
	}

	items := make([]knowledgeEntryPayload, 0, len(entries))
	for _, entry := range entries {
		items = append(items, knowledgeEntryPayload{
			ID:        entry.ID,
			Title:     entry.Title,
			Content:   entry.Content,
			Tags:      entry.Tags,
			CreatedBy: entry.CreatedBy,
			CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: entry.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	sendJSON(w, http.StatusOK, knowledgeListResponse{
		Items: items,
		Total: len(items),
	})
}

func (h *KnowledgeHandler) Import(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	var req knowledgeImportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	input := make([]store.ReplaceKnowledgeEntryInput, 0, len(req.Entries))
	for _, entry := range req.Entries {
		input = append(input, store.ReplaceKnowledgeEntryInput{
			Title:     entry.Title,
			Content:   entry.Content,
			Tags:      entry.Tags,
			CreatedBy: entry.CreatedBy,
		})
	}

	inserted, err := h.Store.ReplaceEntries(r.Context(), input)
	if err != nil {
		handleKnowledgeStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, knowledgeImportResponse{Inserted: inserted})
}

func handleKnowledgeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrForbidden):
		sendJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "knowledge operation failed"})
	}
}
