package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type SharedKnowledgeHandler struct {
	Store       *store.SharedKnowledgeStore
	EventsStore *store.MemoryEventsStore
}

type createSharedKnowledgeRequest struct {
	SourceAgentID  string          `json:"source_agent_id"`
	SourceMemoryID string          `json:"source_memory_id,omitempty"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Scope          string          `json:"scope,omitempty"`
	ScopeTeams     []string        `json:"scope_teams,omitempty"`
	QualityScore   float64         `json:"quality_score,omitempty"`
	OccurredAt     string          `json:"occurred_at,omitempty"`
	ExpiresAt      string          `json:"expires_at,omitempty"`
}

type sharedKnowledgePayload struct {
	ID             string          `json:"id"`
	SourceAgentID  string          `json:"source_agent_id"`
	SourceMemoryID *string         `json:"source_memory_id,omitempty"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	Scope          string          `json:"scope"`
	ScopeTeams     []string        `json:"scope_teams"`
	QualityScore   float64         `json:"quality_score"`
	Confirmations  int             `json:"confirmations"`
	Contradictions int             `json:"contradictions"`
	LastAccessedAt *string         `json:"last_accessed_at,omitempty"`
	Status         string          `json:"status"`
	SupersededBy   *string         `json:"superseded_by,omitempty"`
	OccurredAt     string          `json:"occurred_at"`
	ExpiresAt      *string         `json:"expires_at,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	Relevance      *float64        `json:"relevance,omitempty"`
}

type sharedKnowledgeListResponse struct {
	Items []sharedKnowledgePayload `json:"items"`
	Total int                      `json:"total"`
}

func (h *SharedKnowledgeHandler) ListForAgent(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent_id is required"})
		return
	}
	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
		return
	}

	entries, err := h.Store.ListForAgent(r.Context(), agentID, limit)
	if err != nil {
		handleSharedKnowledgeStoreError(w, err)
		return
	}

	items := make([]sharedKnowledgePayload, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		items = append(items, mapSharedKnowledgeEntry(&copyEntry))
	}
	sendJSON(w, http.StatusOK, sharedKnowledgeListResponse{Items: items, Total: len(items)})
}

func (h *SharedKnowledgeHandler) Search(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "q is required"})
		return
	}
	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 20)
	if limit <= 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
		return
	}

	minQuality := 0.0
	if raw := strings.TrimSpace(r.URL.Query().Get("min_quality")); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "min_quality must be numeric"})
			return
		}
		minQuality = parsed
	}

	entries, err := h.Store.Search(r.Context(), store.SharedKnowledgeSearchParams{
		Query:      query,
		Kinds:      splitCSV(r.URL.Query().Get("kinds")),
		Statuses:   splitCSV(r.URL.Query().Get("statuses")),
		MinQuality: minQuality,
		Limit:      limit,
	})
	if err != nil {
		handleSharedKnowledgeStoreError(w, err)
		return
	}

	items := make([]sharedKnowledgePayload, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		items = append(items, mapSharedKnowledgeEntry(&copyEntry))
	}
	sendJSON(w, http.StatusOK, sharedKnowledgeListResponse{Items: items, Total: len(items)})
}

func (h *SharedKnowledgeHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	var req createSharedKnowledgeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	occurredAtPtr, err := parseOptionalRFC3339(optionalStringPtr(req.OccurredAt), "occurred_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	expiresAtPtr, err := parseOptionalRFC3339(optionalStringPtr(req.ExpiresAt), "expires_at")
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	occurredAt := time.Time{}
	if occurredAtPtr != nil {
		occurredAt = *occurredAtPtr
	}

	entry, err := h.Store.Create(r.Context(), store.CreateSharedKnowledgeInput{
		SourceAgentID:  req.SourceAgentID,
		SourceMemoryID: optionalStringPtr(req.SourceMemoryID),
		Kind:           req.Kind,
		Title:          req.Title,
		Content:        req.Content,
		Metadata:       req.Metadata,
		Scope:          req.Scope,
		ScopeTeams:     req.ScopeTeams,
		QualityScore:   req.QualityScore,
		OccurredAt:     occurredAt,
		ExpiresAt:      expiresAtPtr,
	})
	if err != nil {
		handleSharedKnowledgeStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, mapSharedKnowledgeEntry(entry))
	h.publishSharedKnowledgeEvent(r, store.MemoryEventTypeKnowledgeShared, map[string]any{
		"knowledge_id":    entry.ID,
		"source_agent_id": entry.SourceAgentID,
		"kind":            entry.Kind,
		"title":           entry.Title,
		"scope":           entry.Scope,
		"quality_score":   entry.QualityScore,
	})
}

func (h *SharedKnowledgeHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}

	entry, err := h.Store.Confirm(r.Context(), id)
	if err != nil {
		handleSharedKnowledgeStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, mapSharedKnowledgeEntry(entry))
	h.publishSharedKnowledgeEvent(r, store.MemoryEventTypeKnowledgeConfirmed, map[string]any{
		"knowledge_id":    entry.ID,
		"source_agent_id": entry.SourceAgentID,
		"confirmations":   entry.Confirmations,
		"quality_score":   entry.QualityScore,
	})
}

func (h *SharedKnowledgeHandler) Contradict(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}

	entry, err := h.Store.Contradict(r.Context(), id)
	if err != nil {
		handleSharedKnowledgeStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, mapSharedKnowledgeEntry(entry))
	h.publishSharedKnowledgeEvent(r, store.MemoryEventTypeKnowledgeContradict, map[string]any{
		"knowledge_id":    entry.ID,
		"source_agent_id": entry.SourceAgentID,
		"contradictions":  entry.Contradictions,
		"quality_score":   entry.QualityScore,
	})
}

func mapSharedKnowledgeEntry(entry *store.SharedKnowledgeEntry) sharedKnowledgePayload {
	payload := sharedKnowledgePayload{
		ID:             entry.ID,
		SourceAgentID:  entry.SourceAgentID,
		SourceMemoryID: entry.SourceMemoryID,
		Kind:           entry.Kind,
		Title:          entry.Title,
		Content:        entry.Content,
		Metadata:       entry.Metadata,
		Scope:          entry.Scope,
		ScopeTeams:     append([]string(nil), entry.ScopeTeams...),
		QualityScore:   entry.QualityScore,
		Confirmations:  entry.Confirmations,
		Contradictions: entry.Contradictions,
		Status:         entry.Status,
		SupersededBy:   entry.SupersededBy,
		OccurredAt:     entry.OccurredAt.UTC().Format(time.RFC3339),
		CreatedAt:      entry.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      entry.UpdatedAt.UTC().Format(time.RFC3339),
		Relevance:      entry.Relevance,
	}
	if entry.LastAccessedAt != nil {
		formatted := entry.LastAccessedAt.UTC().Format(time.RFC3339)
		payload.LastAccessedAt = &formatted
	}
	if entry.ExpiresAt != nil {
		formatted := entry.ExpiresAt.UTC().Format(time.RFC3339)
		payload.ExpiresAt = &formatted
	}
	return payload
}

func handleSharedKnowledgeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrSharedKnowledgeInvalidAgentID),
		errors.Is(err, store.ErrSharedKnowledgeInvalidID),
		errors.Is(err, store.ErrSharedKnowledgeInvalidKind),
		errors.Is(err, store.ErrSharedKnowledgeInvalidScope),
		errors.Is(err, store.ErrSharedKnowledgeTitleMissing),
		errors.Is(err, store.ErrSharedKnowledgeContentMissing),
		errors.Is(err, store.ErrSharedKnowledgeInvalidQuality),
		errors.Is(err, store.ErrSharedKnowledgeSearchRequired),
		errors.Is(err, store.ErrSharedKnowledgeInvalidTeamName),
		errors.Is(err, store.ErrInvalidWorkspace):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "shared knowledge operation failed"})
	}
}

func (h *SharedKnowledgeHandler) publishSharedKnowledgeEvent(r *http.Request, eventType string, payload map[string]any) {
	if h.EventsStore == nil {
		return
	}

	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		log.Printf("shared knowledge event marshal failed (%s): %v", eventType, err)
		return
	}

	if _, err := h.EventsStore.Publish(r.Context(), store.PublishMemoryEventInput{
		EventType: eventType,
		Payload:   encodedPayload,
	}); err != nil {
		log.Printf("shared knowledge event publish failed (%s): %v", eventType, err)
	}
}
