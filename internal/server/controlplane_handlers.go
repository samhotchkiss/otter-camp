package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	controlDefaultLimit      = 50
	controlMaxLimit          = 200
	runEventsDefaultLimit    = 100
	runEventsMaxLimit        = 500
	runArtifactsDefaultLimit = 20
	runEventStreamPoll       = 250 * time.Millisecond
	runArtifactURLTTL        = time.Hour
	defaultCostSummaryPeriod = "30d"
)

var controlTerminalRunStatuses = map[string]struct{}{
	"completed":   {},
	"failed":      {},
	"cancelled":   {},
	"dead_letter": {},
	"timed_out":   {},
}

type controlRunService interface {
	CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error)
	RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy controlplane.CancelRequestActor) error
	CreateRetryAttempt(ctx context.Context, runStepID uuid.UUID, trigger string) (controlplane.RunAttempt, error)
	GetRun(ctx context.Context, runID uuid.UUID) (controlplane.Run, error)
}

type controlRunRepository interface {
	Get(ctx context.Context, id uuid.UUID) (controlplane.Run, error)
	GetByIdempotencyKey(ctx context.Context, organizationID uuid.UUID, key string) (controlplane.Run, error)
	List(ctx context.Context, filter controlplane.RunListFilter) ([]controlplane.Run, error)
}

type controlRunStepRepository interface {
	Get(ctx context.Context, id uuid.UUID) (controlplane.RunStep, error)
	ListByRun(ctx context.Context, runID uuid.UUID) ([]controlplane.RunStep, error)
}

type controlRunAttemptRepository interface {
	ListByStep(ctx context.Context, runStepID uuid.UUID) ([]controlplane.RunAttempt, error)
}

type controlRunEventRepository interface {
	ListByRun(ctx context.Context, runID uuid.UUID, fromSequence int) ([]controlplane.RunEvent, error)
}

type controlRunArtifactRepository interface {
	Get(ctx context.Context, id uuid.UUID) (controlplane.RunArtifact, error)
	ListByRun(ctx context.Context, runID uuid.UUID) ([]controlplane.RunArtifact, error)
}

type controlToolExecutionRepository interface {
	GetByOrganization(ctx context.Context, organizationID, id uuid.UUID) (controlplane.ToolExecution, error)
	ListByOrganization(ctx context.Context, filter controlplane.ToolExecutionListFilter) ([]controlplane.ToolExecution, error)
}

type controlArtifactURLSigner interface {
	PresignGetURL(ctx context.Context, storageKey string, ttl time.Duration) (string, error)
}

type staticArtifactURLSigner struct{}

func (staticArtifactURLSigner) PresignGetURL(_ context.Context, storageKey string, ttl time.Duration) (string, error) {
	expires := time.Now().UTC().Add(ttl).Unix()
	escaped := url.PathEscape(strings.TrimSpace(storageKey))
	if escaped == "" {
		escaped = "artifact"
	}
	return fmt.Sprintf("https://storage.local/%s?expires=%d", escaped, expires), nil
}

type ControlPlaneRouteOptions struct {
	Pool           *pgxpool.Pool
	RunService     controlRunService
	Runs           controlRunRepository
	RunSteps       controlRunStepRepository
	RunAttempts    controlRunAttemptRepository
	RunEvents      controlRunEventRepository
	RunArtifacts   controlRunArtifactRepository
	ToolExecutions controlToolExecutionRepository
	URLSigner      controlArtifactURLSigner
	Clock          clock.Clock
}

type ControlPlaneRouteRegistrar struct {
	handlers controlPlaneHandlers
}

type controlPlaneHandlers struct {
	pool           *pgxpool.Pool
	runService     controlRunService
	runs           controlRunRepository
	runSteps       controlRunStepRepository
	runAttempts    controlRunAttemptRepository
	runEvents      controlRunEventRepository
	runArtifacts   controlRunArtifactRepository
	toolExecutions controlToolExecutionRepository
	urlSigner      controlArtifactURLSigner
	clock          clock.Clock
}

func NewControlPlaneRouteRegistrar(opts ControlPlaneRouteOptions) *ControlPlaneRouteRegistrar {
	h := controlPlaneHandlers{
		pool:       opts.Pool,
		runService: opts.RunService,
		urlSigner:  opts.URLSigner,
		clock:      opts.Clock,
	}
	if h.clock == nil {
		h.clock = clock.Real{}
	}
	if h.urlSigner == nil {
		h.urlSigner = staticArtifactURLSigner{}
	}

	if opts.Runs != nil {
		h.runs = opts.Runs
	} else if opts.Pool != nil {
		h.runs = controlplane.NewRunRepository(opts.Pool)
	}
	if opts.RunSteps != nil {
		h.runSteps = opts.RunSteps
	} else if opts.Pool != nil {
		h.runSteps = controlplane.NewRunStepRepository(opts.Pool)
	}
	if opts.RunAttempts != nil {
		h.runAttempts = opts.RunAttempts
	} else if opts.Pool != nil {
		h.runAttempts = controlplane.NewRunAttemptRepository(opts.Pool)
	}
	if opts.RunEvents != nil {
		h.runEvents = opts.RunEvents
	} else if opts.Pool != nil {
		h.runEvents = controlplane.NewRunEventRepository(opts.Pool)
	}
	if opts.RunArtifacts != nil {
		h.runArtifacts = opts.RunArtifacts
	} else if opts.Pool != nil {
		h.runArtifacts = controlplane.NewRunArtifactRepository(opts.Pool)
	}
	if opts.ToolExecutions != nil {
		h.toolExecutions = opts.ToolExecutions
	} else if opts.Pool != nil {
		h.toolExecutions = controlplane.NewToolExecutionRepository(opts.Pool)
	}

	return &ControlPlaneRouteRegistrar{handlers: h}
}

func (r *ControlPlaneRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.Post("/control/runs", r.handlers.createRun)
	router.Get("/control/runs", r.handlers.listRuns)
	router.Get("/control/runs/{id}", r.handlers.getRun)
	router.Get("/control/runs/{id}/steps", r.handlers.listRunSteps)
	router.Get("/control/runs/{id}/steps/{step_id}/attempts", r.handlers.listRunAttempts)
	router.Get("/control/runs/{id}/events", r.handlers.listRunEvents)
	router.Get("/control/runs/{id}/events/stream", r.handlers.streamRunEvents)
	router.Get("/control/runs/{id}/artifacts", r.handlers.listRunArtifacts)
	router.Get("/control/runs/{id}/artifacts/{artifact_id}/download", r.handlers.downloadRunArtifact)
	router.Post("/control/runs/{id}/cancel", r.handlers.cancelRun)
	router.Post("/control/runs/{id}/retry", r.handlers.retryRun)

	router.Get("/control/tool-executions", r.handlers.listToolExecutions)
	router.Get("/control/tool-executions/{id}", r.handlers.getToolExecution)

	router.Get("/control/cost/summary", r.handlers.getCostSummary)
	router.Get("/control/health", r.handlers.getControlHealth)
}

type createRunRequest struct {
	TriggerType    string          `json:"trigger_type"`
	TaskID         *uuid.UUID      `json:"task_id"`
	FlowNodeID     *uuid.UUID      `json:"flow_node_id"`
	SessionID      *uuid.UUID      `json:"session_id"`
	TurnID         *uuid.UUID      `json:"turn_id"`
	IdempotencyKey *string         `json:"idempotency_key"`
	Metadata       json.RawMessage `json:"metadata"`
}

type retryRunRequest struct {
	RunStepID *uuid.UUID `json:"run_step_id"`
}

type runResponse struct {
	ID             uuid.UUID       `json:"id"`
	OrganizationID uuid.UUID       `json:"organization_id"`
	ProjectID      *uuid.UUID      `json:"project_id,omitempty"`
	TaskID         *uuid.UUID      `json:"task_id,omitempty"`
	FlowNodeID     *uuid.UUID      `json:"flow_node_id,omitempty"`
	SessionID      *uuid.UUID      `json:"session_id,omitempty"`
	TurnID         *uuid.UUID      `json:"turn_id,omitempty"`
	PrincipalType  string          `json:"principal_type"`
	PrincipalID    uuid.UUID       `json:"principal_id"`
	Status         string          `json:"status"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	TriggerType    string          `json:"trigger_type"`
	Version        int             `json:"version"`
	FailureReason  *string         `json:"failure_reason,omitempty"`
	FailureClass   *string         `json:"failure_class,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	StepCount      *int            `json:"step_count,omitempty"`
	LatestStatus   *string         `json:"latest_status,omitempty"`
	DurationMS     *int            `json:"duration_ms,omitempty"`
}

type runStepResponse struct {
	ID           uuid.UUID       `json:"id"`
	RunID        uuid.UUID       `json:"run_id"`
	StepNumber   int             `json:"step_number"`
	Status       string          `json:"status"`
	ToolName     *string         `json:"tool_name,omitempty"`
	ToolTier     *string         `json:"tool_tier,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	CompletedAt  *time.Time      `json:"completed_at,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	CreatedAt    time.Time       `json:"created_at"`
	AttemptCount int             `json:"attempt_count"`
}

type runAttemptResponse struct {
	ID            uuid.UUID       `json:"id"`
	RunStepID     uuid.UUID       `json:"run_step_id"`
	AttemptNumber int             `json:"attempt_number"`
	Trigger       string          `json:"trigger"`
	Status        string          `json:"status"`
	FailureReason *string         `json:"failure_reason,omitempty"`
	FailureClass  *string         `json:"failure_class,omitempty"`
	Output        json.RawMessage `json:"output,omitempty"`
	OutputSummary *string         `json:"output_summary,omitempty"`
	WorkerType    *string         `json:"worker_type,omitempty"`
	WorkerID      *string         `json:"worker_id,omitempty"`
	InputTokens   int             `json:"input_tokens"`
	OutputTokens  int             `json:"output_tokens"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	CompletedAt   *time.Time      `json:"completed_at,omitempty"`
	DurationMS    *int            `json:"duration_ms,omitempty"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedAt     time.Time       `json:"created_at"`
}

type runEventResponse struct {
	ID           uuid.UUID       `json:"id"`
	RunID        uuid.UUID       `json:"run_id"`
	RunStepID    *uuid.UUID      `json:"run_step_id,omitempty"`
	RunAttemptID *uuid.UUID      `json:"run_attempt_id,omitempty"`
	Sequence     int             `json:"sequence"`
	EventType    string          `json:"event_type"`
	ActorType    string          `json:"actor_type"`
	ActorID      *uuid.UUID      `json:"actor_id,omitempty"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}

type runArtifactResponse struct {
	ID            uuid.UUID       `json:"id"`
	RunID         uuid.UUID       `json:"run_id"`
	RunStepID     *uuid.UUID      `json:"run_step_id,omitempty"`
	RunAttemptID  *uuid.UUID      `json:"run_attempt_id,omitempty"`
	ArtifactType  string          `json:"artifact_type"`
	StorageKey    string          `json:"storage_key"`
	ContentType   string          `json:"content_type"`
	ByteSize      int             `json:"byte_size"`
	InlineContent *string         `json:"inline_content,omitempty"`
	Filename      *string         `json:"filename,omitempty"`
	Metadata      json.RawMessage `json:"metadata"`
	CreatedAt     time.Time       `json:"created_at"`
	DownloadURL   *string         `json:"download_url,omitempty"`
}

type toolExecutionResponse struct {
	ID             uuid.UUID       `json:"id"`
	RunID          *uuid.UUID      `json:"run_id,omitempty"`
	RunStepID      *uuid.UUID      `json:"run_step_id,omitempty"`
	RunAttemptID   *uuid.UUID      `json:"run_attempt_id,omitempty"`
	ToolName       string          `json:"tool_name"`
	ToolTier       string          `json:"tool_tier"`
	ToolDomain     string          `json:"tool_domain"`
	Capability     *string         `json:"capability,omitempty"`
	PolicyDecision string          `json:"policy_decision"`
	Input          json.RawMessage `json:"input"`
	Output         json.RawMessage `json:"output,omitempty"`
	Status         string          `json:"status"`
	ErrorMessage   *string         `json:"error_message,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	DurationMS     *int            `json:"duration_ms,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"created_at"`
}

type costSummaryResponse struct {
	TotalTokens int64              `json:"total_tokens"`
	ByGroup     []costSummaryGroup `json:"by_group"`
	PeriodStart time.Time          `json:"period_start"`
	PeriodEnd   time.Time          `json:"period_end"`
}

type costSummaryGroup struct {
	GroupKey string `json:"group_key"`
	Tokens   int64  `json:"tokens"`
}

type controlHealthResponse struct {
	Status             string                     `json:"status"`
	ActiveRuns         int                        `json:"active_runs"`
	SupervisorLastTick *time.Time                 `json:"supervisor_last_tick"`
	ToolExecutionAudit *controlHealthAuditSummary `json:"tool_execution_audit,omitempty"`
}

type controlHealthAuditSummary struct {
	RunsChecked int `json:"runs_checked"`
	Violations  int `json:"violations"`
}

func (h controlPlaneHandlers) createRun(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run service unavailable")
		return
	}

	var req createRunRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	idempotencyHit := false
	if req.IdempotencyKey != nil && h.runs != nil {
		existing, err := h.runs.GetByIdempotencyKey(r.Context(), principal.OrganizationID, strings.TrimSpace(*req.IdempotencyKey))
		if err == nil && existing.Status != "failed" && existing.Status != "dead_letter" {
			idempotencyHit = true
		}
	}

	created, err := h.runService.CreateRun(r.Context(), controlplane.CreateRunInput{
		OrganizationID: principal.OrganizationID,
		PrincipalType:  "human_user",
		PrincipalID:    principal.UserID,
		TriggerType:    strings.TrimSpace(req.TriggerType),
		ProjectID:      nil,
		TaskID:         req.TaskID,
		FlowNodeID:     req.FlowNodeID,
		SessionID:      req.SessionID,
		TurnID:         req.TurnID,
		IdempotencyKey: req.IdempotencyKey,
		Metadata:       req.Metadata,
	})
	if err != nil {
		switch {
		case errors.Is(err, controlplane.ErrPolicyDenied):
			responder.Error(w, http.StatusForbidden, "run_policy_denied", strings.TrimSpace(err.Error()))
			return
		case errors.Is(err, controlplane.ErrBudgetExceeded):
			responder.Error(w, http.StatusPaymentRequired, "budget_exceeded", "budget exceeded")
			return
		default:
			if strings.Contains(strings.ToLower(err.Error()), "required") || strings.Contains(strings.ToLower(err.Error()), "invalid") {
				responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, strings.TrimSpace(err.Error()))
				return
			}
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create run")
			return
		}
	}

	status := http.StatusCreated
	if idempotencyHit {
		w.Header().Set("Idempotency-Key-Hit", "true")
		status = http.StatusOK
	}
	writeControlResponse(w, r.Context(), status, toRunResponse(created, nil, nil, nil), nil)
}

func (h controlPlaneHandlers) listRuns(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runs == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run repository unavailable")
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	cursorCreatedAt, cursorID, err := decodePaginationCursor(params.Cursor)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	status := strings.TrimSpace(r.URL.Query().Get("status"))
	taskID, err := parseOptionalUUID(r.URL.Query().Get("task_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid task_id")
		return
	}
	flowNodeID, err := parseOptionalUUID(r.URL.Query().Get("flow_node_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid flow_node_id")
		return
	}
	principalID, err := parseOptionalUUID(r.URL.Query().Get("principal_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid principal_id")
		return
	}
	triggerType := strings.TrimSpace(r.URL.Query().Get("trigger_type"))

	items, err := h.runs.List(r.Context(), controlplane.RunListFilter{
		OrganizationID:  principal.OrganizationID,
		TaskID:          taskID,
		FlowNodeID:      flowNodeID,
		Status:          status,
		PrincipalID:     principalID,
		TriggerType:     triggerType,
		Limit:           params.Limit + 1,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list runs")
		return
	}

	nextCursor := ""
	if len(items) > params.Limit {
		items = items[:params.Limit]
		last := items[len(items)-1]
		nextCursor = (api.PaginationEncoder{}).Encode(last.CreatedAt, last.ID)
	}

	resp := make([]runResponse, 0, len(items))
	for _, item := range items {
		resp = append(resp, toRunResponse(item, nil, nil, nil))
	}

	meta := map[string]any{}
	if nextCursor != "" {
		meta["cursor"] = nextCursor
	}
	writeControlResponse(w, r.Context(), http.StatusOK, resp, meta)
}

func (h controlPlaneHandlers) getRun(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runs == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	runRecord, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID)
	if !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	var stepCount *int
	if h.runSteps != nil {
		steps, err := h.runSteps.ListByRun(r.Context(), runRecord.ID)
		if err == nil {
			count := len(steps)
			stepCount = &count
		}
	}

	latest := runRecord.Status
	latestStatus := &latest
	durationMS := runDurationMS(runRecord)

	writeControlResponse(w, r.Context(), http.StatusOK, toRunResponse(runRecord, stepCount, latestStatus, durationMS), nil)
}

func (h controlPlaneHandlers) listRunSteps(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runSteps == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run step repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	steps, err := h.runSteps.ListByRun(r.Context(), runID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list run steps")
		return
	}

	out := make([]runStepResponse, 0, len(steps))
	for _, step := range steps {
		attemptCount := 0
		if h.runAttempts != nil {
			attempts, attemptErr := h.runAttempts.ListByStep(r.Context(), step.ID)
			if attemptErr == nil {
				attemptCount = len(attempts)
			}
		}
		out = append(out, toRunStepResponse(step, attemptCount))
	}

	writeControlResponse(w, r.Context(), http.StatusOK, out, nil)
}

func (h controlPlaneHandlers) listRunAttempts(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runSteps == nil || h.runAttempts == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run attempt repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	stepID, ok := parsePathUUID(responder, w, r, "step_id")
	if !ok {
		return
	}
	step, err := h.runSteps.Get(r.Context(), stepID)
	if err != nil || step.RunID != runID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	attempts, err := h.runAttempts.ListByStep(r.Context(), step.ID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list attempts")
		return
	}

	out := make([]runAttemptResponse, 0, len(attempts))
	for _, attempt := range attempts {
		out = append(out, toRunAttemptResponse(attempt))
	}
	writeControlResponse(w, r.Context(), http.StatusOK, out, nil)
}

func (h controlPlaneHandlers) listRunEvents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runEvents == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run event repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	fromSequence, err := parseIntQuery(r.URL.Query().Get("from_sequence"), 0)
	if err != nil || fromSequence < 0 {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid from_sequence")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), runEventsDefaultLimit, runEventsMaxLimit)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid limit")
		return
	}
	eventTypes := parseCSVSet(r.URL.Query().Get("event_type"))

	events, err := h.runEvents.ListByRun(r.Context(), runID, fromSequence)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list run events")
		return
	}
	filtered := filterRunEventsByType(events, eventTypes)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	nextSequence := fromSequence
	if len(filtered) > 0 {
		nextSequence = filtered[len(filtered)-1].Sequence + 1
	}

	out := make([]runEventResponse, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, toRunEventResponse(item))
	}
	writeControlResponse(w, r.Context(), http.StatusOK, out, map[string]any{"next_sequence": nextSequence})
}

func (h controlPlaneHandlers) streamRunEvents(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runs == nil || h.runEvents == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run streaming unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	runRecord, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID)
	if !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	lastEventID, err := parseLastEventID(r, true)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid last-event-id")
		return
	}
	sequence := int(lastEventID)

	backlog, err := h.runEvents.ListByRun(r.Context(), runID, sequence)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load run events")
		return
	}
	if isTerminalRunStatus(runRecord.Status) && len(backlog) == 0 {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "streaming unsupported")
		return
	}
	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	for _, event := range backlog {
		if err := writeSSEEvent(w, strconv.Itoa(event.Sequence), "run_event", toRunEventResponse(event)); err != nil {
			return
		}
		sequence = event.Sequence
		flusher.Flush()
	}
	if isTerminalRunStatus(runRecord.Status) {
		return
	}

	if h.pool == nil {
		h.streamRunEventsByPolling(w, r, flusher, principal.OrganizationID, runID, sequence)
		return
	}
	h.streamRunEventsByListenNotify(w, r, flusher, principal.OrganizationID, runID, sequence)
}

func (h controlPlaneHandlers) streamRunEventsByPolling(w http.ResponseWriter, r *http.Request, flusher http.Flusher, organizationID, runID uuid.UUID, sequence int) {
	ticker := time.NewTicker(runEventStreamPoll)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			nextEvents, err := h.runEvents.ListByRun(r.Context(), runID, sequence)
			if err != nil {
				return
			}
			for _, event := range nextEvents {
				if err := writeSSEEvent(w, strconv.Itoa(event.Sequence), "run_event", toRunEventResponse(event)); err != nil {
					return
				}
				sequence = event.Sequence
				flusher.Flush()
			}

			runRecord, found := h.getRunForOrg(r.Context(), organizationID, runID)
			if !found {
				return
			}
			if isTerminalRunStatus(runRecord.Status) && len(nextEvents) == 0 {
				return
			}
		}
	}
}

func (h controlPlaneHandlers) streamRunEventsByListenNotify(w http.ResponseWriter, r *http.Request, flusher http.Flusher, organizationID, runID uuid.UUID, sequence int) {
	conn, err := h.pool.Acquire(r.Context())
	if err != nil {
		h.streamRunEventsByPolling(w, r, flusher, organizationID, runID, sequence)
		return
	}
	defer conn.Release()

	channel := controlplane.RunEventsChannel(runID)
	listenSQL := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	unlistenSQL := "UNLISTEN " + pgx.Identifier{channel}.Sanitize()
	if _, err := conn.Exec(r.Context(), listenSQL); err != nil {
		h.streamRunEventsByPolling(w, r, flusher, organizationID, runID, sequence)
		return
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), unlistenSQL)
	}()

	for {
		waitCtx, cancel := context.WithTimeout(r.Context(), runEventStreamPoll)
		_, waitErr := conn.Conn().WaitForNotification(waitCtx)
		cancel()
		if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) && !errors.Is(waitErr, context.Canceled) {
			return
		}

		nextEvents, err := h.runEvents.ListByRun(r.Context(), runID, sequence)
		if err != nil {
			return
		}
		for _, event := range nextEvents {
			if err := writeSSEEvent(w, strconv.Itoa(event.Sequence), "run_event", toRunEventResponse(event)); err != nil {
				return
			}
			sequence = event.Sequence
			flusher.Flush()
		}

		runRecord, found := h.getRunForOrg(r.Context(), organizationID, runID)
		if !found {
			return
		}
		if isTerminalRunStatus(runRecord.Status) && len(nextEvents) == 0 {
			return
		}
		if r.Context().Err() != nil {
			return
		}
	}
}

func (h controlPlaneHandlers) listRunArtifacts(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runArtifacts == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run artifact repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	artifactType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("artifact_type")))
	runStepID, err := parseOptionalUUID(r.URL.Query().Get("run_step_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid run_step_id")
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"), runArtifactsDefaultLimit, controlMaxLimit)
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid limit")
		return
	}

	items, err := h.runArtifacts.ListByRun(r.Context(), runID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list run artifacts")
		return
	}

	out := make([]runArtifactResponse, 0, len(items))
	for _, item := range items {
		if artifactType != "" && strings.ToLower(strings.TrimSpace(item.ArtifactType)) != artifactType {
			continue
		}
		if runStepID != nil {
			if item.RunStepID == nil || *item.RunStepID != *runStepID {
				continue
			}
		}
		resp := toRunArtifactResponse(item)
		if item.InlineContent == nil {
			downloadURL, signErr := h.urlSigner.PresignGetURL(r.Context(), item.StorageKey, runArtifactURLTTL)
			if signErr != nil {
				responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to sign artifact URL")
				return
			}
			resp.DownloadURL = &downloadURL
		}
		out = append(out, resp)
		if len(out) >= limit {
			break
		}
	}

	writeControlResponse(w, r.Context(), http.StatusOK, out, nil)
}

func (h controlPlaneHandlers) downloadRunArtifact(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runArtifacts == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run artifact repository unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	artifactID, ok := parsePathUUID(responder, w, r, "artifact_id")
	if !ok {
		return
	}
	artifact, err := h.runArtifacts.Get(r.Context(), artifactID)
	if err != nil || artifact.RunID != runID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	downloadURL, err := h.urlSigner.PresignGetURL(r.Context(), artifact.StorageKey, runArtifactURLTTL)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to sign artifact URL")
		return
	}
	http.Redirect(w, r, downloadURL, http.StatusFound)
}

func (h controlPlaneHandlers) cancelRun(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runService == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run service unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	if _, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID); !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if err := h.runService.RequestCancel(r.Context(), runID, controlplane.CancelRequestActor{Type: "human_user", ID: principal.UserID}); err != nil {
		switch {
		case errors.Is(err, controlplane.ErrTerminalState), errors.Is(err, controlplane.ErrInvalidTransition):
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "run is already in a terminal state")
		case errors.Is(err, controlplane.ErrNotFound):
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		default:
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to cancel run")
		}
		return
	}

	runRecord, err := h.runService.GetRun(r.Context(), runID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to fetch run")
		return
	}
	writeControlResponse(w, r.Context(), http.StatusAccepted, toRunResponse(runRecord, nil, nil, nil), nil)
}

func (h controlPlaneHandlers) retryRun(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.runService == nil || h.runSteps == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "run retry unavailable")
		return
	}

	runID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	runRecord, found := h.getRunForOrg(r.Context(), principal.OrganizationID, runID)
	if !found {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if runRecord.Status != "failed" {
		responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "run is not in failed state")
		return
	}

	req := retryRunRequest{}
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
			return
		}
	}

	var targetStepID uuid.UUID
	if req.RunStepID != nil {
		step, err := h.runSteps.Get(r.Context(), *req.RunStepID)
		if err != nil || step.RunID != runID {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		targetStepID = step.ID
	} else {
		steps, err := h.runSteps.ListByRun(r.Context(), runID)
		if err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list run steps")
			return
		}
		latest := controlplane.RunStep{}
		foundFailed := false
		for _, step := range steps {
			if step.Status != "failed" {
				continue
			}
			if !foundFailed || step.StepNumber > latest.StepNumber {
				latest = step
				foundFailed = true
			}
		}
		if !foundFailed {
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "no failed run step available for retry")
			return
		}
		targetStepID = latest.ID
	}

	attempt, err := h.runService.CreateRetryAttempt(r.Context(), targetStepID, "retry_transient")
	if err != nil {
		switch {
		case errors.Is(err, controlplane.ErrMaxAttemptsExceeded), errors.Is(err, controlplane.ErrInvalidTransition):
			responder.Error(w, http.StatusConflict, api.ErrCodeConflict, strings.TrimSpace(err.Error()))
		case errors.Is(err, controlplane.ErrNotFound), errors.Is(err, repo.ErrNotFound):
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		default:
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create retry attempt")
		}
		return
	}

	writeControlResponse(w, r.Context(), http.StatusCreated, toRunAttemptResponse(attempt), nil)
}

func (h controlPlaneHandlers) listToolExecutions(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.toolExecutions == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "tool execution repository unavailable")
		return
	}

	params := api.ParsePaginationParams(r.URL.Query())
	cursorCreatedAt, cursorID, err := decodePaginationCursor(params.Cursor)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
		return
	}

	runID, err := parseOptionalUUID(r.URL.Query().Get("run_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid run_id")
		return
	}

	items, err := h.toolExecutions.ListByOrganization(r.Context(), controlplane.ToolExecutionListFilter{
		OrganizationID:  principal.OrganizationID,
		RunID:           runID,
		ToolName:        strings.TrimSpace(r.URL.Query().Get("tool_name")),
		ToolDomain:      strings.TrimSpace(r.URL.Query().Get("tool_domain")),
		PolicyDecision:  strings.TrimSpace(r.URL.Query().Get("policy_decision")),
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:           params.Limit + 1,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list tool executions")
		return
	}

	nextCursor := ""
	if len(items) > params.Limit {
		items = items[:params.Limit]
		last := items[len(items)-1]
		nextCursor = (api.PaginationEncoder{}).Encode(last.CreatedAt, last.ID)
	}

	out := make([]toolExecutionResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toToolExecutionResponse(item))
	}

	meta := map[string]any{}
	if nextCursor != "" {
		meta["cursor"] = nextCursor
	}
	writeControlResponse(w, r.Context(), http.StatusOK, out, meta)
}

func (h controlPlaneHandlers) getToolExecution(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.toolExecutions == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "tool execution repository unavailable")
		return
	}

	executionID, ok := parsePathUUID(responder, w, r, "id")
	if !ok {
		return
	}
	item, err := h.toolExecutions.GetByOrganization(r.Context(), principal.OrganizationID, executionID)
	if err != nil {
		if errors.Is(err, controlplane.ErrNotFound) {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to fetch tool execution")
		return
	}
	writeControlResponse(w, r.Context(), http.StatusOK, toToolExecutionResponse(item), nil)
}

func (h controlPlaneHandlers) getCostSummary(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "cost summary unavailable")
		return
	}

	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = defaultCostSummaryPeriod
	}
	periodStart, periodEnd, err := parseCostSummaryPeriod(h.clock.Now().UTC(), period)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}

	groupBy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("group_by")))
	if groupBy == "" {
		groupBy = "project"
	}
	if groupBy != "project" && groupBy != "agent" && groupBy != "tool_domain" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "group_by must be one of project, agent, tool_domain")
		return
	}

	projectID, err := parseOptionalUUID(r.URL.Query().Get("project_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project_id")
		return
	}

	groups, err := h.queryCostSummaryRows(r.Context(), principal.OrganizationID, periodStart, periodEnd, groupBy, projectID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load cost summary")
		return
	}

	var total int64
	for _, row := range groups {
		total += row.Tokens
	}
	writeControlResponse(w, r.Context(), http.StatusOK, costSummaryResponse{
		TotalTokens: total,
		ByGroup:     groups,
		PeriodStart: periodStart,
		PeriodEnd:   periodEnd,
	}, nil)
}

func (h controlPlaneHandlers) getControlHealth(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		responder.Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "control health unavailable")
		return
	}

	var activeRuns int
	if err := h.pool.QueryRow(r.Context(), `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND status IN ('created', 'in_progress', 'paused', 'cancelling')
	`, principal.OrganizationID).Scan(&activeRuns); err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to query active runs")
		return
	}

	var supervisorLastTick *time.Time
	if err := h.pool.QueryRow(r.Context(), `
		SELECT MAX(re.created_at)
		FROM run_event re
		JOIN run r ON r.id = re.run_id
		WHERE r.organization_id = $1
		  AND re.actor_type = 'supervisor'
	`, principal.OrganizationID).Scan(&supervisorLastTick); err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to query supervisor health")
		return
	}

	auditSummary := &controlHealthAuditSummary{}
	verifier := controlplane.NewToolExecutionAuditVerifier(h.pool)
	rows, err := h.pool.Query(r.Context(), `
		SELECT id
		FROM run
		WHERE organization_id = $1
		  AND status IN ('created', 'in_progress', 'paused', 'cancelling')
		ORDER BY created_at DESC
		LIMIT 20
	`, principal.OrganizationID)
	if err == nil {
		for rows.Next() {
			var runID uuid.UUID
			if scanErr := rows.Scan(&runID); scanErr != nil {
				continue
			}
			report := verifier.Verify(r.Context(), runID)
			auditSummary.RunsChecked++
			auditSummary.Violations += len(report.Violations)
		}
		rows.Close()
	}

	writeControlResponse(w, r.Context(), http.StatusOK, controlHealthResponse{
		Status:             "ok",
		ActiveRuns:         activeRuns,
		SupervisorLastTick: supervisorLastTick,
		ToolExecutionAudit: auditSummary,
	}, nil)
}

func (h controlPlaneHandlers) queryCostSummaryRows(ctx context.Context, orgID uuid.UUID, periodStart, periodEnd time.Time, groupBy string, projectID *uuid.UUID) ([]costSummaryGroup, error) {
	baseQuery := `
		SELECT %s AS group_key,
		       COALESCE(SUM(ra.input_tokens + ra.output_tokens), 0)::bigint AS tokens
		FROM run_attempt ra
		JOIN run_step rs ON rs.id = ra.run_step_id
		JOIN run r ON r.id = rs.run_id
		%s
		WHERE r.organization_id = $1
		  AND ra.created_at >= $2
		  AND ra.created_at <= $3
		  AND ($4::uuid IS NULL OR r.project_id = $4)
		GROUP BY 1
		ORDER BY tokens DESC, group_key ASC`

	groupExpr := "COALESCE(r.project_id::text, 'unassigned')"
	joinExpr := ""
	switch groupBy {
	case "agent":
		groupExpr = "CASE WHEN r.principal_type = 'agent' THEN r.principal_id::text ELSE r.principal_type END"
	case "tool_domain":
		joinExpr = `
			LEFT JOIN LATERAL (
				SELECT te.tool_domain
				FROM tool_execution te
				WHERE te.run_attempt_id = ra.id
				ORDER BY te.created_at ASC
				LIMIT 1
			) td ON true`
		groupExpr = "COALESCE(td.tool_domain, 'unknown')"
	}

	query := fmt.Sprintf(baseQuery, groupExpr, joinExpr)
	rows, err := h.pool.Query(ctx, query, orgID, periodStart, periodEnd, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]costSummaryGroup, 0)
	for rows.Next() {
		var item costSummaryGroup
		if err := rows.Scan(&item.GroupKey, &item.Tokens); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return items, nil
}

func (h controlPlaneHandlers) getRunForOrg(ctx context.Context, orgID, runID uuid.UUID) (controlplane.Run, bool) {
	if h.runs == nil {
		return controlplane.Run{}, false
	}
	runRecord, err := h.runs.Get(ctx, runID)
	if err != nil {
		return controlplane.Run{}, false
	}
	if runRecord.OrganizationID != orgID {
		return controlplane.Run{}, false
	}
	return runRecord, true
}

func toRunResponse(item controlplane.Run, stepCount *int, latestStatus *string, durationMS *int) runResponse {
	return runResponse{
		ID:             item.ID,
		OrganizationID: item.OrganizationID,
		ProjectID:      item.ProjectID,
		TaskID:         item.TaskID,
		FlowNodeID:     item.FlowNodeID,
		SessionID:      item.SessionID,
		TurnID:         item.TurnID,
		PrincipalType:  item.PrincipalType,
		PrincipalID:    item.PrincipalID,
		Status:         item.Status,
		IdempotencyKey: item.IdempotencyKey,
		TriggerType:    item.TriggerType,
		Version:        item.Version,
		FailureReason:  item.FailureReason,
		FailureClass:   item.FailureClass,
		Metadata:       item.Metadata,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
		StartedAt:      item.StartedAt,
		CompletedAt:    item.CompletedAt,
		StepCount:      stepCount,
		LatestStatus:   latestStatus,
		DurationMS:     durationMS,
	}
}

func toRunStepResponse(item controlplane.RunStep, attemptCount int) runStepResponse {
	return runStepResponse{
		ID:           item.ID,
		RunID:        item.RunID,
		StepNumber:   item.StepNumber,
		Status:       item.Status,
		ToolName:     item.ToolName,
		ToolTier:     item.ToolTier,
		StartedAt:    item.StartedAt,
		CompletedAt:  item.CompletedAt,
		Metadata:     item.Metadata,
		CreatedAt:    item.CreatedAt,
		AttemptCount: attemptCount,
	}
}

func toRunAttemptResponse(item controlplane.RunAttempt) runAttemptResponse {
	return runAttemptResponse{
		ID:            item.ID,
		RunStepID:     item.RunStepID,
		AttemptNumber: item.AttemptNumber,
		Trigger:       item.Trigger,
		Status:        item.Status,
		FailureReason: item.FailureReason,
		FailureClass:  item.FailureClass,
		Output:        item.Output,
		OutputSummary: item.OutputSummary,
		WorkerType:    item.WorkerType,
		WorkerID:      item.WorkerID,
		InputTokens:   item.InputTokens,
		OutputTokens:  item.OutputTokens,
		StartedAt:     item.StartedAt,
		CompletedAt:   item.CompletedAt,
		DurationMS:    item.DurationMS,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
	}
}

func toRunEventResponse(item controlplane.RunEvent) runEventResponse {
	return runEventResponse{
		ID:           item.ID,
		RunID:        item.RunID,
		RunStepID:    item.RunStepID,
		RunAttemptID: item.RunAttemptID,
		Sequence:     item.Sequence,
		EventType:    item.EventType,
		ActorType:    item.ActorType,
		ActorID:      item.ActorID,
		Payload:      item.Payload,
		CreatedAt:    item.CreatedAt,
	}
}

func toRunArtifactResponse(item controlplane.RunArtifact) runArtifactResponse {
	return runArtifactResponse{
		ID:            item.ID,
		RunID:         item.RunID,
		RunStepID:     item.RunStepID,
		RunAttemptID:  item.RunAttemptID,
		ArtifactType:  item.ArtifactType,
		StorageKey:    item.StorageKey,
		ContentType:   item.ContentType,
		ByteSize:      item.ByteSize,
		InlineContent: item.InlineContent,
		Filename:      item.Filename,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
	}
}

func toToolExecutionResponse(item controlplane.ToolExecution) toolExecutionResponse {
	return toolExecutionResponse{
		ID:             item.ID,
		RunID:          item.RunID,
		RunStepID:      item.RunStepID,
		RunAttemptID:   item.RunAttemptID,
		ToolName:       item.ToolName,
		ToolTier:       item.ToolTier,
		ToolDomain:     item.ToolDomain,
		Capability:     item.Capability,
		PolicyDecision: item.PolicyDecision,
		Input:          item.Input,
		Output:         item.Output,
		Status:         item.Status,
		ErrorMessage:   item.ErrorMessage,
		StartedAt:      item.StartedAt,
		CompletedAt:    item.CompletedAt,
		DurationMS:     item.DurationMS,
		Metadata:       item.Metadata,
		CreatedAt:      item.CreatedAt,
	}
}

func runDurationMS(runRecord controlplane.Run) *int {
	if runRecord.StartedAt == nil || runRecord.CompletedAt == nil {
		return nil
	}
	value := int(runRecord.CompletedAt.Sub(*runRecord.StartedAt).Milliseconds())
	if value < 0 {
		value = 0
	}
	return &value
}

func filterRunEventsByType(events []controlplane.RunEvent, eventTypes map[string]struct{}) []controlplane.RunEvent {
	if len(eventTypes) == 0 {
		return events
	}
	filtered := make([]controlplane.RunEvent, 0, len(events))
	for _, event := range events {
		if _, ok := eventTypes[strings.ToLower(strings.TrimSpace(event.EventType))]; !ok {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func parseCSVSet(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		item := strings.ToLower(strings.TrimSpace(part))
		if item == "" {
			continue
		}
		out[item] = struct{}{}
	}
	return out
}

func parseLimit(raw string, def, max int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return def, nil
	}
	value, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return def, nil
	}
	if value > max {
		return max, nil
	}
	return value, nil
}

func parseIntQuery(raw string, def int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return def, nil
	}
	return strconv.Atoi(trimmed)
}

func decodePaginationCursor(raw string) (*time.Time, *uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, nil
	}
	createdAt, id, err := (api.PaginationDecoder{}).Decode(raw)
	if err != nil {
		return nil, nil, err
	}
	created := createdAt.UTC()
	cursorID := id
	return &created, &cursorID, nil
}

func parseCostSummaryPeriod(now time.Time, period string) (time.Time, time.Time, error) {
	period = strings.ToLower(strings.TrimSpace(period))
	if period == "" {
		period = defaultCostSummaryPeriod
	}
	if !strings.HasSuffix(period, "d") {
		return time.Time{}, time.Time{}, fmt.Errorf("period must be in Nd format, e.g. 7d")
	}
	daysRaw := strings.TrimSuffix(period, "d")
	days, err := strconv.Atoi(daysRaw)
	if err != nil || days <= 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("period must be in Nd format, e.g. 7d")
	}
	end := now.UTC()
	start := end.AddDate(0, 0, -days)
	return start, end, nil
}

func isTerminalRunStatus(status string) bool {
	_, ok := controlTerminalRunStatuses[strings.ToLower(strings.TrimSpace(status))]
	return ok
}

func writeControlResponse(w http.ResponseWriter, ctx context.Context, status int, data any, extraMeta map[string]any) {
	requestID, hasRequestID := api.RequestIDFromContext(ctx)
	if !hasRequestID || strings.TrimSpace(requestID) == "" {
		requestID = uuid.NewString()
	}
	w.Header().Set(api.HeaderRequestID, requestID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	meta := map[string]any{"request_id": requestID}
	for key, value := range extraMeta {
		meta[key] = value
	}

	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": data,
		"meta": meta,
	})
}
