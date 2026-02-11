package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type MemoryHandler struct {
	Store *store.MemoryStore
}

type createMemoryEntryRequest struct {
	AgentID       string          `json:"agent_id"`
	Kind          string          `json:"kind"`
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	Metadata      json.RawMessage `json:"metadata"`
	Importance    int             `json:"importance"`
	Confidence    float64         `json:"confidence"`
	Sensitivity   string          `json:"sensitivity"`
	OccurredAt    string          `json:"occurred_at,omitempty"`
	ExpiresAt     string          `json:"expires_at,omitempty"`
	SourceSession string          `json:"source_session,omitempty"`
	SourceProject string          `json:"source_project,omitempty"`
	SourceIssue   string          `json:"source_issue,omitempty"`
}

type memoryEntryPayload struct {
	ID             string          `json:"id"`
	AgentID        string          `json:"agent_id"`
	Kind           string          `json:"kind"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	Importance     int             `json:"importance"`
	Confidence     float64         `json:"confidence"`
	Sensitivity    string          `json:"sensitivity"`
	Status         string          `json:"status"`
	OccurredAt     string          `json:"occurred_at"`
	ExpiresAt      *string         `json:"expires_at,omitempty"`
	SourceSession  *string         `json:"source_session,omitempty"`
	SourceProject  *string         `json:"source_project,omitempty"`
	SourceIssue    *string         `json:"source_issue,omitempty"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	RelevanceScore *float64        `json:"relevance,omitempty"`
}

type memoryListResponse struct {
	Items []memoryEntryPayload `json:"items"`
	Total int                  `json:"total"`
}

type memoryRecallResponse struct {
	Context string `json:"context"`
}

type memoryEvaluationRunPayload struct {
	ID          string   `json:"id"`
	Passed      bool     `json:"passed"`
	FailedGates []string `json:"failed_gates,omitempty"`
	Metrics     struct {
		PrecisionAtK        *float64 `json:"precision_at_k,omitempty"`
		FalseInjectionRate  *float64 `json:"false_injection_rate,omitempty"`
		RecoverySuccessRate *float64 `json:"recovery_success_rate,omitempty"`
		P95LatencyMs        *float64 `json:"p95_latency_ms,omitempty"`
	} `json:"metrics,omitempty"`
}

type memoryEvaluationLatestResponse struct {
	Run *memoryEvaluationRunPayload `json:"run"`
}

func (h *MemoryHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	var req createMemoryEntryRequest
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

	entry, err := h.Store.Create(r.Context(), store.CreateMemoryEntryInput{
		AgentID:       req.AgentID,
		Kind:          req.Kind,
		Title:         req.Title,
		Content:       req.Content,
		Metadata:      req.Metadata,
		Importance:    req.Importance,
		Confidence:    req.Confidence,
		Sensitivity:   req.Sensitivity,
		OccurredAt:    occurredAt,
		ExpiresAt:     expiresAtPtr,
		SourceSession: optionalStringPtr(req.SourceSession),
		SourceProject: optionalStringPtr(req.SourceProject),
		SourceIssue:   optionalStringPtr(req.SourceIssue),
	})
	if err != nil {
		handleMemoryStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusCreated, mapMemoryEntry(entry))
}

func (h *MemoryHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent_id is required"})
		return
	}

	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 20)
	offset := parseIntOrDefault(r.URL.Query().Get("offset"), 0)
	if limit <= 0 || offset < 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit and offset must be non-negative integers"})
		return
	}
	limit = clampMemoryListLimit(limit)

	entries, err := h.Store.ListByAgent(r.Context(), agentID, kind, limit, offset)
	if err != nil {
		handleMemoryStoreError(w, err)
		return
	}

	items := make([]memoryEntryPayload, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		items = append(items, mapMemoryEntry(&copyEntry))
	}
	sendJSON(w, http.StatusOK, memoryListResponse{Items: items, Total: len(items)})
}

func (h *MemoryHandler) Search(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent_id is required"})
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "q is required"})
		return
	}

	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 20)
	if limit <= 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be positive"})
		return
	}
	limit = clampMemorySearchLimit(limit)
	minImportance := parseIntOrDefault(r.URL.Query().Get("min_importance"), 1)
	minRelevance, err := parseFloatOrDefault(r.URL.Query().Get("min_relevance"), 0)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "min_relevance must be numeric"})
		return
	}

	var sincePtr *time.Time
	if rawSince := strings.TrimSpace(r.URL.Query().Get("since")); rawSince != "" {
		parsed, parseErr := time.Parse(time.RFC3339, rawSince)
		if parseErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "since must be RFC3339"})
			return
		}
		since := parsed.UTC()
		sincePtr = &since
	}

	var untilPtr *time.Time
	if rawUntil := strings.TrimSpace(r.URL.Query().Get("until")); rawUntil != "" {
		parsed, parseErr := time.Parse(time.RFC3339, rawUntil)
		if parseErr != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "until must be RFC3339"})
			return
		}
		until := parsed.UTC()
		untilPtr = &until
	}

	entries, err := h.Store.Search(r.Context(), store.MemorySearchParams{
		AgentID:       agentID,
		Query:         query,
		Kinds:         splitCSV(r.URL.Query().Get("kinds")),
		AllowedScopes: splitCSV(r.URL.Query().Get("scopes")),
		MinRelevance:  minRelevance,
		MinImportance: minImportance,
		Limit:         limit,
		Since:         sincePtr,
		Until:         untilPtr,
		SourceProject: optionalStringPtr(r.URL.Query().Get("source_project")),
	})
	if err != nil {
		handleMemoryStoreError(w, err)
		return
	}

	items := make([]memoryEntryPayload, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		items = append(items, mapMemoryEntry(&copyEntry))
	}
	sendJSON(w, http.StatusOK, memoryListResponse{Items: items, Total: len(items)})
}

func (h *MemoryHandler) Recall(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent_id is required"})
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "q is required"})
		return
	}

	maxResults := parseIntOrDefault(r.URL.Query().Get("max_results"), 5)
	minImportance := parseIntOrDefault(r.URL.Query().Get("min_importance"), 1)
	maxChars := parseIntOrDefault(r.URL.Query().Get("max_chars"), 2000)
	minRelevance, err := parseFloatOrDefault(r.URL.Query().Get("min_relevance"), 0)
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "min_relevance must be numeric"})
		return
	}

	contextText, err := h.Store.GetRecallContext(r.Context(), agentID, query, store.RecallContextConfig{
		MaxResults:    maxResults,
		MinRelevance:  minRelevance,
		MinImportance: minImportance,
		AllowedScopes: splitCSV(r.URL.Query().Get("scopes")),
		MaxChars:      maxChars,
	})
	if err != nil {
		handleMemoryStoreError(w, err)
		return
	}

	sendJSON(w, http.StatusOK, memoryRecallResponse{Context: contextText})
}

func (h *MemoryHandler) LatestEvaluation(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, http.StatusOK, memoryEvaluationLatestResponse{Run: nil})
}

func (h *MemoryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "id is required"})
		return
	}

	if err := h.Store.Delete(r.Context(), id); err != nil {
		handleMemoryStoreError(w, err)
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleMemoryStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNoWorkspace):
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
	case errors.Is(err, store.ErrNotFound):
		sendJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
	case errors.Is(err, store.ErrDuplicateMemory):
		sendJSON(w, http.StatusConflict, errorResponse{Error: "duplicate memory entry"})
	case errors.Is(err, store.ErrMemoryInvalidAgentID),
		errors.Is(err, store.ErrMemoryInvalidEntryID),
		errors.Is(err, store.ErrMemoryInvalidKind),
		errors.Is(err, store.ErrMemoryInvalidSensitivity),
		errors.Is(err, store.ErrMemoryTitleMissing),
		errors.Is(err, store.ErrMemoryContentMissing),
		errors.Is(err, store.ErrMemoryInvalidImportance),
		errors.Is(err, store.ErrMemoryInvalidConfidence),
		errors.Is(err, store.ErrMemoryQueryMissing),
		errors.Is(err, store.ErrMemoryInvalidRelevance),
		errors.Is(err, store.ErrInvalidWorkspace):
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory operation failed"})
	}
}

func mapMemoryEntry(entry *store.MemoryEntry) memoryEntryPayload {
	payload := memoryEntryPayload{
		ID:             entry.ID,
		AgentID:        entry.AgentID,
		Kind:           entry.Kind,
		Title:          entry.Title,
		Content:        entry.Content,
		Metadata:       entry.Metadata,
		Importance:     entry.Importance,
		Confidence:     entry.Confidence,
		Sensitivity:    entry.Sensitivity,
		Status:         entry.Status,
		OccurredAt:     entry.OccurredAt.UTC().Format(time.RFC3339),
		SourceSession:  entry.SourceSession,
		SourceProject:  entry.SourceProject,
		SourceIssue:    entry.SourceIssue,
		CreatedAt:      entry.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:      entry.UpdatedAt.UTC().Format(time.RFC3339),
		RelevanceScore: entry.Relevance,
	}
	if entry.ExpiresAt != nil {
		formatted := entry.ExpiresAt.UTC().Format(time.RFC3339)
		payload.ExpiresAt = &formatted
	}
	return payload
}

func parseIntOrDefault(raw string, fallback int) int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback
	}
	return value
}

func clampMemoryListLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func clampMemorySearchLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func parseFloatOrDefault(raw string, fallback float64) (float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func splitCSV(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}
