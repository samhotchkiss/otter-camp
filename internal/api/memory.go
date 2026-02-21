package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type MemoryHandler struct {
	Store *store.MemoryStore
	DB    *sql.DB
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
		ElliePrecision      *float64 `json:"ellie_retrieval_precision,omitempty"`
		EllieRecall         *float64 `json:"ellie_retrieval_recall,omitempty"`
	} `json:"metrics,omitempty"`
}

type memoryEvaluationLatestResponse struct {
	Run *memoryEvaluationRunPayload `json:"run"`
}

type memoryEvaluationRunsResponse struct {
	Items []memoryEvaluationRunPayload `json:"items"`
	Total int                          `json:"total"`
}

type memoryEvaluationRunRequest struct {
	FixturePath string `json:"fixture_path,omitempty"`
}

type memoryEvaluationMetricsRecord struct {
	PrecisionAtK        float64 `json:"precision_at_k"`
	FalseInjectionRate  float64 `json:"false_injection_rate"`
	RecoverySuccessRate float64 `json:"recovery_success_rate"`
	P95LatencyMs        float64 `json:"p95_latency_ms"`
	ElliePrecision      float64 `json:"ellie_retrieval_precision"`
	EllieRecall         float64 `json:"ellie_retrieval_recall"`
}

type memoryEvaluationRunRecord struct {
	ID          string                        `json:"id"`
	CreatedAt   string                        `json:"created_at"`
	Passed      bool                          `json:"passed"`
	FailedGates []string                      `json:"failed_gates,omitempty"`
	Metrics     memoryEvaluationMetricsRecord `json:"metrics"`
	FixturePath string                        `json:"fixture_path,omitempty"`
}

type memoryTuneRequest struct {
	Apply bool `json:"apply"`
}

type memoryTuningResponse struct {
	AttemptID       string                 `json:"attempt_id"`
	Status          string                 `json:"status"`
	Reason          string                 `json:"reason,omitempty"`
	Applied         bool                   `json:"applied"`
	RolledBack      bool                   `json:"rolled_back"`
	ApplyRequested  bool                   `json:"apply_requested"`
	BaselineConfig  memory.TunerConfig     `json:"baseline_config"`
	CandidateConfig memory.TunerConfig     `json:"candidate_config"`
	BaselineResult  memory.EvaluatorResult `json:"baseline_result"`
	CandidateResult memory.EvaluatorResult `json:"candidate_result"`
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
	if h.DB == nil {
		sendJSON(w, http.StatusOK, memoryEvaluationLatestResponse{Run: nil})
		return
	}

	workspaceID, ok := memoryWorkspaceIDFromRequest(r)
	if !ok {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	record, err := h.loadLatestEvaluationRun(r, workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory evaluation operation failed"})
		return
	}
	if record == nil {
		sendJSON(w, http.StatusOK, memoryEvaluationLatestResponse{Run: nil})
		return
	}
	payload := mapMemoryEvaluationRunRecord(*record)
	sendJSON(w, http.StatusOK, memoryEvaluationLatestResponse{Run: &payload})
}

func (h *MemoryHandler) ListEvaluations(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		sendJSON(w, http.StatusOK, memoryEvaluationRunsResponse{Items: []memoryEvaluationRunPayload{}, Total: 0})
		return
	}

	workspaceID, ok := memoryWorkspaceIDFromRequest(r)
	if !ok {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	limit := parseIntOrDefault(r.URL.Query().Get("limit"), 20)
	if limit <= 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be positive"})
		return
	}
	if limit > 100 {
		limit = 100
	}

	runs, err := h.loadEvaluationRuns(r, workspaceID, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory evaluation operation failed"})
		return
	}

	items := make([]memoryEvaluationRunPayload, 0, len(runs))
	for _, run := range runs {
		items = append(items, mapMemoryEvaluationRunRecord(run))
	}
	sendJSON(w, http.StatusOK, memoryEvaluationRunsResponse{Items: items, Total: len(items)})
}

func (h *MemoryHandler) RunEvaluation(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	workspaceID, ok := memoryWorkspaceIDFromRequest(r)
	if !ok {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req memoryEvaluationRunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	fixturePath := resolveMemoryEvaluationFixturePath(req.FixturePath)
	result, err := memory.Evaluator{
		Config: defaultMemoryEvaluatorConfig(),
	}.RunFromJSONL(fixturePath)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: fmt.Sprintf("memory evaluator failed: %v", err)})
		return
	}

	runRecord := memoryEvaluationRunRecord{
		ID:          fmt.Sprintf("eval-%d", time.Now().UnixNano()),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Passed:      result.Passed,
		FailedGates: append([]string(nil), result.FailedGates...),
		Metrics: memoryEvaluationMetricsRecord{
			PrecisionAtK:        result.Metrics.PrecisionAtK,
			FalseInjectionRate:  result.Metrics.FalseInjectionRate,
			RecoverySuccessRate: result.Metrics.RecoverySuccessRate,
			P95LatencyMs:        result.Metrics.P95LatencyMs,
			ElliePrecision:      result.Metrics.EllieRetrievalPrecision,
			EllieRecall:         result.Metrics.EllieRetrievalRecall,
		},
		FixturePath: fixturePath,
	}
	if err := h.persistEvaluationRun(r, workspaceID, runRecord); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory evaluation operation failed"})
		return
	}
	h.publishMemoryOpsEvent(r, store.MemoryEventTypeMemoryEvaluated, map[string]any{
		"evaluation_id": runRecord.ID,
		"passed":        runRecord.Passed,
		"failed_gates":  runRecord.FailedGates,
		"fixture_path":  runRecord.FixturePath,
	})

	sendJSON(w, http.StatusCreated, mapMemoryEvaluationRunRecord(runRecord))
}

func (h *MemoryHandler) TuneEvaluation(w http.ResponseWriter, r *http.Request) {
	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database not available"})
		return
	}

	workspaceID, ok := memoryWorkspaceIDFromRequest(r)
	if !ok {
		sendJSON(w, http.StatusUnauthorized, errorResponse{Error: "missing workspace"})
		return
	}

	var req memoryTuneRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	latest, err := h.loadLatestEvaluationRun(r, workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory evaluation operation failed"})
		return
	}
	if latest == nil {
		sendJSON(w, http.StatusConflict, errorResponse{Error: "run memory evaluation first"})
		return
	}

	baseMetrics := memory.EvaluatorMetrics{
		PrecisionAtK:            latest.Metrics.PrecisionAtK,
		FalseInjectionRate:      latest.Metrics.FalseInjectionRate,
		RecoverySuccessRate:     latest.Metrics.RecoverySuccessRate,
		P95LatencyMs:            latest.Metrics.P95LatencyMs,
		EllieRetrievalPrecision: latest.Metrics.ElliePrecision,
		EllieRetrievalRecall:    latest.Metrics.EllieRecall,
	}
	baselineConfig, err := h.loadMemoryTunerConfig(r, workspaceID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory tuning operation failed"})
		return
	}

	tuner := memory.Tuner{
		Bounds: memory.DefaultTunerBounds(),
		Evaluator: func(_ context.Context, cfg memory.TunerConfig) (memory.EvaluatorResult, error) {
			return synthesizeMemoryEvaluatorResult(baseMetrics, cfg), nil
		},
		Apply: func(_ context.Context, cfg memory.TunerConfig) error {
			if !req.Apply {
				return nil
			}
			return h.persistMemoryTunerConfig(r, workspaceID, cfg)
		},
		LastAppliedAtFn: func(_ context.Context) (time.Time, bool, error) {
			return h.loadMemoryTunerLastApplied(r, workspaceID)
		},
	}

	decision, err := tuner.RunOnce(r.Context(), baselineConfig)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: fmt.Sprintf("memory tuning failed: %v", err)})
		return
	}

	response := memoryTuningResponse{
		AttemptID:       decision.AttemptID,
		Status:          decision.Status,
		Reason:          decision.Reason,
		Applied:         decision.Applied,
		RolledBack:      decision.RolledBack,
		ApplyRequested:  req.Apply,
		BaselineConfig:  decision.BaselineConfig,
		CandidateConfig: decision.CandidateConfig,
		BaselineResult:  decision.BaselineResult,
		CandidateResult: decision.CandidateResult,
	}
	if !req.Apply && response.Applied {
		response.Applied = false
		response.Status = "would_apply"
		response.Reason = ""
	}
	if req.Apply && response.Applied {
		if err := h.persistMemoryTunerLastApplied(r, workspaceID, time.Now().UTC()); err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory tuning operation failed"})
			return
		}
	}
	if err := h.persistLatestTuningDecision(r, workspaceID, response); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "memory tuning operation failed"})
		return
	}
	h.publishMemoryOpsEvent(r, store.MemoryEventTypeMemoryTuned, map[string]any{
		"attempt_id":      response.AttemptID,
		"status":          response.Status,
		"apply_requested": response.ApplyRequested,
		"applied":         response.Applied,
		"rolled_back":     response.RolledBack,
	})

	sendJSON(w, http.StatusOK, response)
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

func defaultMemoryEvaluatorConfig() memory.EvaluatorConfig {
	return memory.EvaluatorConfig{
		K:                      5,
		MinPrecisionAtK:        0.80,
		MaxFalseInjectionRate:  0.02,
		MinRecoverySuccessRate: 0.95,
		MaxP95LatencyMs:        500,
	}
}

func resolveMemoryEvaluationFixturePath(rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed != "" {
		return trimmed
	}
	if fromEnv := strings.TrimSpace(os.Getenv("MEMORY_EVAL_FIXTURE")); fromEnv != "" {
		return fromEnv
	}
	return filepath.Join("internal", "memory", "testdata", "evaluator_benchmark_v1.jsonl")
}

func mapMemoryEvaluationRunRecord(record memoryEvaluationRunRecord) memoryEvaluationRunPayload {
	precision := record.Metrics.PrecisionAtK
	falseInjection := record.Metrics.FalseInjectionRate
	recovery := record.Metrics.RecoverySuccessRate
	latency := record.Metrics.P95LatencyMs
	elliePrecision := record.Metrics.ElliePrecision
	ellieRecall := record.Metrics.EllieRecall
	return memoryEvaluationRunPayload{
		ID:          record.ID,
		Passed:      record.Passed,
		FailedGates: append([]string(nil), record.FailedGates...),
		Metrics: struct {
			PrecisionAtK        *float64 `json:"precision_at_k,omitempty"`
			FalseInjectionRate  *float64 `json:"false_injection_rate,omitempty"`
			RecoverySuccessRate *float64 `json:"recovery_success_rate,omitempty"`
			P95LatencyMs        *float64 `json:"p95_latency_ms,omitempty"`
			ElliePrecision      *float64 `json:"ellie_retrieval_precision,omitempty"`
			EllieRecall         *float64 `json:"ellie_retrieval_recall,omitempty"`
		}{
			PrecisionAtK:        &precision,
			FalseInjectionRate:  &falseInjection,
			RecoverySuccessRate: &recovery,
			P95LatencyMs:        &latency,
			ElliePrecision:      &elliePrecision,
			EllieRecall:         &ellieRecall,
		},
	}
}

func memoryWorkspaceIDFromRequest(r *http.Request) (string, bool) {
	workspaceID := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
	if workspaceID == "" {
		return "", false
	}
	return workspaceID, true
}

func (h *MemoryHandler) loadLatestEvaluationRun(r *http.Request, workspaceID string) (*memoryEvaluationRunRecord, error) {
	value, ok, err := h.loadSyncMetadataValue(r.Context(), memoryEvaluationLatestMetadataKey(workspaceID))
	if err != nil || !ok {
		return nil, err
	}

	var record memoryEvaluationRunRecord
	if err := json.Unmarshal([]byte(value), &record); err != nil {
		return nil, fmt.Errorf("decode latest memory evaluation run: %w", err)
	}
	return &record, nil
}

func (h *MemoryHandler) loadEvaluationRuns(r *http.Request, workspaceID string, limit int) ([]memoryEvaluationRunRecord, error) {
	value, ok, err := h.loadSyncMetadataValue(r.Context(), memoryEvaluationHistoryMetadataKey(workspaceID))
	if err != nil || !ok {
		return []memoryEvaluationRunRecord{}, err
	}
	var history []memoryEvaluationRunRecord
	if err := json.Unmarshal([]byte(value), &history); err != nil {
		return nil, fmt.Errorf("decode memory evaluation history: %w", err)
	}
	if limit > 0 && len(history) > limit {
		history = history[:limit]
	}
	return history, nil
}

func (h *MemoryHandler) persistEvaluationRun(r *http.Request, workspaceID string, run memoryEvaluationRunRecord) error {
	encodedRun, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal evaluation run: %w", err)
	}
	if err := h.upsertSyncMetadataValue(r.Context(), memoryEvaluationLatestMetadataKey(workspaceID), string(encodedRun)); err != nil {
		return err
	}

	history, err := h.loadEvaluationRuns(r, workspaceID, 0)
	if err != nil {
		return err
	}
	history = append([]memoryEvaluationRunRecord{run}, history...)
	if len(history) > 100 {
		history = history[:100]
	}
	encodedHistory, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("marshal evaluation history: %w", err)
	}
	return h.upsertSyncMetadataValue(r.Context(), memoryEvaluationHistoryMetadataKey(workspaceID), string(encodedHistory))
}

func (h *MemoryHandler) loadMemoryTunerConfig(r *http.Request, workspaceID string) (memory.TunerConfig, error) {
	value, ok, err := h.loadSyncMetadataValue(r.Context(), memoryTunerConfigMetadataKey(workspaceID))
	if err != nil {
		return memory.TunerConfig{}, err
	}
	if !ok {
		return defaultMemoryTunerConfig(), nil
	}

	var cfg memory.TunerConfig
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return memory.TunerConfig{}, fmt.Errorf("decode memory tuner config: %w", err)
	}
	return cfg, nil
}

func (h *MemoryHandler) persistMemoryTunerConfig(r *http.Request, workspaceID string, cfg memory.TunerConfig) error {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal memory tuner config: %w", err)
	}
	return h.upsertSyncMetadataValue(r.Context(), memoryTunerConfigMetadataKey(workspaceID), string(encoded))
}

func defaultMemoryTunerConfig() memory.TunerConfig {
	return memory.TunerConfig{
		RecallMinRelevance: 0.70,
		RecallMaxResults:   5,
		RecallMaxChars:     2000,
		Sensitivity:        "internal",
		Scope:              "org",
	}
}

func (h *MemoryHandler) loadMemoryTunerLastApplied(r *http.Request, workspaceID string) (time.Time, bool, error) {
	value, ok, err := h.loadSyncMetadataValue(r.Context(), memoryTunerLastAppliedMetadataKey(workspaceID))
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false, nil
	}
	return parsed.UTC(), true, nil
}

func (h *MemoryHandler) persistMemoryTunerLastApplied(r *http.Request, workspaceID string, ts time.Time) error {
	return h.upsertSyncMetadataValue(
		r.Context(),
		memoryTunerLastAppliedMetadataKey(workspaceID),
		ts.UTC().Format(time.RFC3339),
	)
}

func (h *MemoryHandler) persistLatestTuningDecision(r *http.Request, workspaceID string, decision memoryTuningResponse) error {
	encoded, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("marshal memory tuning decision: %w", err)
	}
	return h.upsertSyncMetadataValue(r.Context(), memoryTunerLatestMetadataKey(workspaceID), string(encoded))
}

func (h *MemoryHandler) loadSyncMetadataValue(ctx context.Context, key string) (string, bool, error) {
	var value string
	if err := h.DB.QueryRowContext(ctx, `SELECT value FROM sync_metadata WHERE key = $1`, key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load sync metadata %s: %w", key, err)
	}
	return value, true, nil
}

func (h *MemoryHandler) upsertSyncMetadataValue(ctx context.Context, key, value string) error {
	_, err := h.DB.ExecContext(
		ctx,
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key)
		 DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key,
		value,
	)
	if err != nil {
		return fmt.Errorf("upsert sync metadata %s: %w", key, err)
	}
	return nil
}

func synthesizeMemoryEvaluatorResult(
	baseMetrics memory.EvaluatorMetrics,
	cfg memory.TunerConfig,
) memory.EvaluatorResult {
	targetMinRelevance := 0.75
	targetMaxResults := 4
	targetMaxChars := 1800

	precision := clampUnitInterval(
		baseMetrics.PrecisionAtK -
			absFloat(cfg.RecallMinRelevance-targetMinRelevance)*0.20 -
			absFloat(float64(cfg.RecallMaxResults-targetMaxResults))*0.015 -
			absFloat(float64(cfg.RecallMaxChars-targetMaxChars))*0.00001,
	)
	falseInjection := clampUnitInterval(
		baseMetrics.FalseInjectionRate +
			absFloat(cfg.RecallMinRelevance-targetMinRelevance)*0.18 +
			absFloat(float64(cfg.RecallMaxResults-targetMaxResults))*0.01 +
			absFloat(float64(cfg.RecallMaxChars-targetMaxChars))*0.000008,
	)
	recovery := clampUnitInterval(
		baseMetrics.RecoverySuccessRate -
			absFloat(cfg.RecallMinRelevance-targetMinRelevance)*0.10,
	)
	latency := baseMetrics.P95LatencyMs +
		absFloat(float64(cfg.RecallMaxResults-targetMaxResults))*18 +
		absFloat(float64(cfg.RecallMaxChars-targetMaxChars))*0.015
	if latency < 1 {
		latency = 1
	}

	metrics := memory.EvaluatorMetrics{
		PrecisionAtK:            precision,
		FalseInjectionRate:      falseInjection,
		RecoverySuccessRate:     recovery,
		P95LatencyMs:            latency,
		AvgInjectedTokens:       float64(cfg.RecallMaxChars) * 0.35,
		EllieRetrievalPrecision: baseMetrics.EllieRetrievalPrecision,
		EllieRetrievalRecall:    baseMetrics.EllieRetrievalRecall,
		CaseCount:               maxInt(baseMetrics.CaseCount, 1),
	}

	return buildMemoryEvaluatorResult(metrics, defaultMemoryEvaluatorConfig())
}

func buildMemoryEvaluatorResult(metrics memory.EvaluatorMetrics, cfg memory.EvaluatorConfig) memory.EvaluatorResult {
	gates := []memory.EvaluatorGateResult{
		{
			Name:       "recall_precision_at_k",
			Comparator: ">=",
			Actual:     metrics.PrecisionAtK,
			Threshold:  cfg.MinPrecisionAtK,
			Passed:     metrics.PrecisionAtK >= cfg.MinPrecisionAtK,
		},
		{
			Name:       "false_injection_rate",
			Comparator: "<=",
			Actual:     metrics.FalseInjectionRate,
			Threshold:  cfg.MaxFalseInjectionRate,
			Passed:     metrics.FalseInjectionRate <= cfg.MaxFalseInjectionRate,
		},
		{
			Name:       "compaction_recovery_success_rate",
			Comparator: ">=",
			Actual:     metrics.RecoverySuccessRate,
			Threshold:  cfg.MinRecoverySuccessRate,
			Passed:     metrics.RecoverySuccessRate >= cfg.MinRecoverySuccessRate,
		},
		{
			Name:       "p95_recall_latency_ms",
			Comparator: "<=",
			Actual:     metrics.P95LatencyMs,
			Threshold:  cfg.MaxP95LatencyMs,
			Passed:     metrics.P95LatencyMs <= cfg.MaxP95LatencyMs,
		},
	}

	failed := make([]string, 0)
	for _, gate := range gates {
		if !gate.Passed {
			failed = append(failed, gate.Name)
		}
	}

	return memory.EvaluatorResult{
		Metrics:     metrics,
		Gates:       gates,
		Passed:      len(failed) == 0,
		FailedGates: failed,
	}
}

func memoryEvaluationLatestMetadataKey(workspaceID string) string {
	return "memory_eval_latest:" + workspaceID
}

func memoryEvaluationHistoryMetadataKey(workspaceID string) string {
	return "memory_eval_history:" + workspaceID
}

func memoryTunerConfigMetadataKey(workspaceID string) string {
	return "memory_tuner_config:" + workspaceID
}

func memoryTunerLastAppliedMetadataKey(workspaceID string) string {
	return "memory_tuner_last_applied:" + workspaceID
}

func memoryTunerLatestMetadataKey(workspaceID string) string {
	return "memory_tuner_latest:" + workspaceID
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func clampUnitInterval(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (h *MemoryHandler) publishMemoryOpsEvent(r *http.Request, eventType string, payload map[string]any) {
	if h.DB == nil {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Printf("memory event marshal failed (%s): %v", eventType, err)
		return
	}
	if _, err := store.NewMemoryEventsStore(h.DB).Publish(r.Context(), store.PublishMemoryEventInput{
		EventType: eventType,
		Payload:   encoded,
	}); err != nil {
		log.Printf("memory event publish failed (%s): %v", eventType, err)
	}
}
