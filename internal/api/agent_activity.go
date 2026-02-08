package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	maxActivityEventsBodySize = 2 << 20 // 2MB
	maxActivityEventsBatch    = 500
	activityListDefaultLimit  = 50
	activityListMaxLimit      = 200
)

var activityStatusValues = map[string]struct{}{
	"started":   {},
	"completed": {},
	"failed":    {},
	"timeout":   {},
}

var activityUUIDRegex = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

type AgentActivityHandler struct {
	DB    *sql.DB
	Store *store.AgentActivityEventStore
}

type ingestAgentActivityEventsRequest struct {
	OrgID  string                      `json:"org_id"`
	Events []ingestAgentActivityRecord `json:"events"`
}

type ingestAgentActivityScope struct {
	ProjectID   string `json:"project_id,omitempty"`
	IssueID     string `json:"issue_id,omitempty"`
	IssueNumber *int   `json:"issue_number,omitempty"`
	ThreadID    string `json:"thread_id,omitempty"`
}

type ingestAgentActivityRecord struct {
	ID          string                    `json:"id"`
	AgentID     string                    `json:"agent_id"`
	SessionKey  string                    `json:"session_key,omitempty"`
	Trigger     string                    `json:"trigger"`
	Channel     string                    `json:"channel,omitempty"`
	Summary     string                    `json:"summary"`
	Detail      string                    `json:"detail,omitempty"`
	Scope       *ingestAgentActivityScope `json:"scope,omitempty"`
	TokensUsed  int                       `json:"tokens_used"`
	ModelUsed   string                    `json:"model_used,omitempty"`
	DurationMs  int64                     `json:"duration_ms"`
	Status      string                    `json:"status"`
	StartedAt   time.Time                 `json:"started_at"`
	CompletedAt *time.Time                `json:"completed_at,omitempty"`
}

type ingestAgentActivityEventsResponse struct {
	OK       bool      `json:"ok"`
	Inserted int       `json:"inserted"`
	At       time.Time `json:"at"`
}

type listAgentActivityResponse struct {
	Items      []store.AgentActivityEvent `json:"items"`
	Total      int                        `json:"total"`
	NextBefore string                     `json:"next_before,omitempty"`
}

func (h *AgentActivityHandler) IngestEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}

	if status, err := requireOpenClawSyncAuth(r); err != nil {
		sendJSON(w, status, errorResponse{Error: err.Error()})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxActivityEventsBodySize+1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "failed to read body"})
		return
	}
	defer r.Body.Close()
	if len(body) > maxActivityEventsBodySize {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "payload too large"})
		return
	}

	var req ingestAgentActivityEventsRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON"})
		return
	}

	req.OrgID = strings.TrimSpace(req.OrgID)
	if req.OrgID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	if !activityUUIDRegex.MatchString(req.OrgID) {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id must be a UUID"})
		return
	}
	if len(req.Events) == 0 {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "events are required"})
		return
	}
	if len(req.Events) > maxActivityEventsBatch {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "too many events"})
		return
	}

	createInputs := make([]store.CreateAgentActivityEventInput, 0, len(req.Events))
	for idx, event := range req.Events {
		input, err := normalizeActivityIngestRecord(event)
		if err != nil {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("event %d: %s", idx, err.Error())})
			return
		}
		createInputs = append(createInputs, input)
	}

	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}

	ctx := context.WithValue(r.Context(), middleware.WorkspaceIDKey, req.OrgID)
	if err := activityStore.CreateEvents(ctx, createInputs); err != nil {
		if err == store.ErrNoWorkspace || err == store.ErrInvalidWorkspace {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to persist activity events"})
		return
	}

	sendJSON(w, http.StatusOK, ingestAgentActivityEventsResponse{
		OK:       true,
		Inserted: len(createInputs),
		At:       time.Now().UTC(),
	})
}

func normalizeActivityIngestRecord(event ingestAgentActivityRecord) (store.CreateAgentActivityEventInput, error) {
	id := strings.TrimSpace(event.ID)
	if id == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("id is required")
	}
	agentID := strings.TrimSpace(event.AgentID)
	if agentID == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("agent_id is required")
	}
	trigger := strings.TrimSpace(event.Trigger)
	if trigger == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("trigger is required")
	}
	summary := strings.TrimSpace(event.Summary)
	if summary == "" {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("summary is required")
	}
	if event.StartedAt.IsZero() {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("started_at is required")
	}
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "completed"
	}
	if _, ok := activityStatusValues[status]; !ok {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("status is invalid")
	}
	if event.TokensUsed < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("tokens_used must be >= 0")
	}
	if event.DurationMs < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("duration_ms must be >= 0")
	}

	input := store.CreateAgentActivityEventInput{
		ID:          id,
		AgentID:     agentID,
		SessionKey:  strings.TrimSpace(event.SessionKey),
		Trigger:     trigger,
		Channel:     strings.TrimSpace(event.Channel),
		Summary:     summary,
		Detail:      strings.TrimSpace(event.Detail),
		TokensUsed:  event.TokensUsed,
		ModelUsed:   strings.TrimSpace(event.ModelUsed),
		DurationMs:  event.DurationMs,
		Status:      status,
		StartedAt:   event.StartedAt.UTC(),
		CompletedAt: event.CompletedAt,
	}

	if event.Scope == nil {
		return input, nil
	}

	projectID := strings.TrimSpace(event.Scope.ProjectID)
	if projectID != "" && !activityUUIDRegex.MatchString(projectID) {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.project_id must be a UUID")
	}
	issueID := strings.TrimSpace(event.Scope.IssueID)
	if issueID != "" && !activityUUIDRegex.MatchString(issueID) {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.issue_id must be a UUID")
	}
	if event.Scope.IssueNumber != nil && *event.Scope.IssueNumber < 0 {
		return store.CreateAgentActivityEventInput{}, fmt.Errorf("scope.issue_number must be >= 0")
	}
	input.ProjectID = projectID
	input.IssueID = issueID
	if event.Scope.IssueNumber != nil {
		input.IssueNumber = *event.Scope.IssueNumber
	}
	input.ThreadID = strings.TrimSpace(event.Scope.ThreadID)

	return input, nil
}

func (h *AgentActivityHandler) ListByAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	agentID := strings.TrimSpace(chi.URLParam(r, "id"))
	if agentID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "agent id is required"})
		return
	}
	opts, err := parseAgentActivityListOptions(r.URL.Query())
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}
	items, err := activityStore.ListByAgent(r.Context(), agentID, opts)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list agent activity"})
		return
	}
	resp := buildListAgentActivityResponse(items, opts.Limit)
	sendJSON(w, http.StatusOK, resp)
}

func (h *AgentActivityHandler) ListRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
		return
	}
	workspaceID := middleware.WorkspaceFromContext(r.Context())
	if workspaceID == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "org_id is required"})
		return
	}
	opts, err := parseAgentActivityListOptions(r.URL.Query())
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	opts.AgentID = strings.TrimSpace(r.URL.Query().Get("agent_id"))

	activityStore, err := h.resolveStore()
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "database unavailable"})
		return
	}
	items, err := activityStore.ListRecent(r.Context(), opts)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list recent activity"})
		return
	}
	resp := buildListAgentActivityResponse(items, opts.Limit)
	sendJSON(w, http.StatusOK, resp)
}

func parseAgentActivityListOptions(values url.Values) (store.ListAgentActivityOptions, error) {
	opts := store.ListAgentActivityOptions{
		Limit: activityListDefaultLimit,
	}

	limitRaw := strings.TrimSpace(values.Get("limit"))
	if limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil || limit <= 0 {
			return opts, fmt.Errorf("limit must be a positive integer")
		}
		if limit > activityListMaxLimit {
			limit = activityListMaxLimit
		}
		opts.Limit = limit
	}

	beforeRaw := strings.TrimSpace(values.Get("before"))
	if beforeRaw != "" {
		before, err := time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			return opts, fmt.Errorf("before must be RFC3339")
		}
		opts.Before = &before
	}

	opts.Trigger = strings.TrimSpace(values.Get("trigger"))
	opts.Channel = strings.TrimSpace(values.Get("channel"))
	opts.Status = strings.TrimSpace(values.Get("status"))
	projectID := strings.TrimSpace(values.Get("project_id"))
	if projectID != "" && !activityUUIDRegex.MatchString(projectID) {
		return opts, fmt.Errorf("project_id must be a UUID")
	}
	opts.ProjectID = projectID
	return opts, nil
}

func buildListAgentActivityResponse(items []store.AgentActivityEvent, limit int) listAgentActivityResponse {
	resp := listAgentActivityResponse{
		Items: items,
		Total: len(items),
	}
	if len(items) == 0 {
		return resp
	}
	if limit <= 0 {
		limit = activityListDefaultLimit
	}
	if len(items) >= limit {
		last := items[len(items)-1].StartedAt.UTC()
		if !last.IsZero() {
			resp.NextBefore = last.Format(time.RFC3339)
		}
	}
	return resp
}

func (h *AgentActivityHandler) resolveStore() (*store.AgentActivityEventStore, error) {
	if h.Store != nil {
		return h.Store, nil
	}
	db := h.DB
	if db == nil {
		var err error
		db, err = store.DB()
		if err != nil {
			return nil, err
		}
	}
	h.Store = store.NewAgentActivityEventStore(db)
	return h.Store, nil
}
