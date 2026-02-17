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
	"strconv"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var openClawMigrationPhaseOrder = []string{
	"agent_import",
	"history_backfill",
	"history_embedding_1536",
	"memory_extraction",
	"entity_synthesis",
	"memory_dedup",
	"taxonomy_classification",
	"project_discovery",
	"project_docs_scanning",
}

const openClawMigrationResetConfirmToken = "RESET_OPENCLAW_MIGRATION"

const (
	defaultOpenClawMigrationFailuresLimit = 100
	maxOpenClawMigrationFailuresLimit     = 1000
)

type openClawMigrationStatusResponse struct {
	Active bool                           `json:"active"`
	Phases []migrationPhaseStatusResponse `json:"phases"`
}

type openClawMigrationRunRequest struct {
	AgentsOnly        bool   `json:"agents_only"`
	HistoryOnly       bool   `json:"history_only"`
	StartPhase        string `json:"start_phase"`
	ForceResumePaused bool   `json:"force_resume_paused"`
}

type openClawMigrationRunResponse struct {
	Accepted               bool     `json:"accepted"`
	SelectedPhases         []string `json:"selected_phases,omitempty"`
	StartedPhases          []string `json:"started_phases,omitempty"`
	ResumedPhases          []string `json:"resumed_phases,omitempty"`
	SkippedCompletedPhases []string `json:"skipped_completed_phases,omitempty"`
	AlreadyRunningPhases   []string `json:"already_running_phases,omitempty"`
	PausedPhases           []string `json:"paused_phases,omitempty"`
}

type openClawMigrationReportResponse struct {
	EventsExpected            int     `json:"events_expected"`
	EventsProcessed           int     `json:"events_processed"`
	MessagesInserted          int     `json:"messages_inserted"`
	EventsSkippedUnknownAgent int     `json:"events_skipped_unknown_agent"`
	FailedItems               int     `json:"failed_items"`
	CompletenessRatio         float64 `json:"completeness_ratio"`
	IsComplete                bool    `json:"is_complete"`
}

type openClawMigrationFailureItem struct {
	OrgID              string    `json:"org_id"`
	MigrationType      string    `json:"migration_type"`
	BatchID            string    `json:"batch_id"`
	AgentSlug          string    `json:"agent_slug"`
	SessionID          string    `json:"session_id"`
	EventID            string    `json:"event_id"`
	SessionPath        string    `json:"session_path"`
	Line               int       `json:"line"`
	MessageIDCandidate string    `json:"message_id_candidate"`
	ErrorReason        string    `json:"error_reason"`
	ErrorMessage       string    `json:"error_message"`
	FirstSeenAt        time.Time `json:"first_seen_at"`
	LastSeenAt         time.Time `json:"last_seen_at"`
	AttemptCount       int       `json:"attempt_count"`
}

type openClawMigrationFailuresResponse struct {
	Items []openClawMigrationFailureItem `json:"items"`
	Total int                            `json:"total"`
}

type openClawMigrationMutationResponse struct {
	Status        string `json:"status"`
	UpdatedPhases int    `json:"updated_phases"`
}

type openClawMigrationResetRequest struct {
	Confirm string `json:"confirm"`
}

type openClawMigrationResetResponse struct {
	Status              string         `json:"status"`
	PausedPhases        int            `json:"paused_phases"`
	ProgressRowsDeleted int            `json:"progress_rows_deleted"`
	Deleted             map[string]int `json:"deleted"`
	TotalDeleted        int            `json:"total_deleted"`
}

type openClawMigrationProgressStore interface {
	ListByOrg(ctx context.Context, orgID string) ([]store.MigrationProgress, error)
	GetByType(ctx context.Context, orgID, migrationType string) (*store.MigrationProgress, error)
	StartPhase(ctx context.Context, input store.StartMigrationProgressInput) (*store.MigrationProgress, error)
	SetStatus(ctx context.Context, input store.SetMigrationProgressStatusInput) (*store.MigrationProgress, error)
	UpdateStatusByOrg(
		ctx context.Context,
		orgID string,
		fromStatus store.MigrationProgressStatus,
		toStatus store.MigrationProgressStatus,
	) (int, error)
}

type openClawHistoryFailureStore interface {
	ListByOrg(
		ctx context.Context,
		orgID string,
		opts store.ListOpenClawHistoryFailureOptions,
	) ([]store.OpenClawHistoryImportFailure, error)
}

type openClawMigrationResetStore interface {
	Reset(ctx context.Context, input store.OpenClawMigrationResetInput) (store.OpenClawMigrationResetResult, error)
}

type openClawMigrationControlPlaneService interface {
	Status(ctx context.Context, orgID string) (openClawMigrationStatusResponse, error)
	Report(ctx context.Context, orgID string) (openClawMigrationReportResponse, error)
	Failures(ctx context.Context, orgID string, limit int) (openClawMigrationFailuresResponse, error)
	Run(ctx context.Context, orgID string, input openClawMigrationRunRequest) (openClawMigrationRunResponse, error)
	Pause(ctx context.Context, orgID string) (openClawMigrationMutationResponse, error)
	Resume(ctx context.Context, orgID string) (openClawMigrationMutationResponse, error)
	Reset(ctx context.Context, orgID string) (openClawMigrationResetResponse, error)
}

type defaultOpenClawMigrationControlPlaneService struct {
	progressStore openClawMigrationProgressStore
	resetStore    openClawMigrationResetStore
	failureStore  openClawHistoryFailureStore
}

func newOpenClawMigrationControlPlaneServiceWithStore(
	progressStore openClawMigrationProgressStore,
) *defaultOpenClawMigrationControlPlaneService {
	return newOpenClawMigrationControlPlaneServiceWithStores(progressStore, nil, nil)
}

func newOpenClawMigrationControlPlaneServiceWithStores(
	progressStore openClawMigrationProgressStore,
	resetStore openClawMigrationResetStore,
	failureStore openClawHistoryFailureStore,
) *defaultOpenClawMigrationControlPlaneService {
	return &defaultOpenClawMigrationControlPlaneService{
		progressStore: progressStore,
		resetStore:    resetStore,
		failureStore:  failureStore,
	}
}

func newOpenClawMigrationControlPlaneService(db *sql.DB) *defaultOpenClawMigrationControlPlaneService {
	if db == nil {
		return newOpenClawMigrationControlPlaneServiceWithStores(nil, nil, nil)
	}
	return newOpenClawMigrationControlPlaneServiceWithStores(
		store.NewMigrationProgressStore(db),
		store.NewOpenClawMigrationResetStore(db),
		store.NewOpenClawHistoryFailureLedgerStore(db),
	)
}

func (s *defaultOpenClawMigrationControlPlaneService) Status(
	ctx context.Context,
	orgID string,
) (openClawMigrationStatusResponse, error) {
	if s == nil || s.progressStore == nil {
		return openClawMigrationStatusResponse{}, fmt.Errorf("migration progress store not configured")
	}

	progressRows, err := s.progressStore.ListByOrg(ctx, orgID)
	if err != nil {
		return openClawMigrationStatusResponse{}, err
	}

	progressByType := make(map[string]store.MigrationProgress, len(progressRows))
	for _, row := range progressRows {
		phaseType := strings.TrimSpace(row.MigrationType)
		if phaseType == "" {
			continue
		}
		progressByType[phaseType] = row
	}

	phases := make([]migrationPhaseStatusResponse, 0, len(openClawMigrationPhaseOrder))
	active := false
	for _, phaseType := range openClawMigrationPhaseOrder {
		row, ok := progressByType[phaseType]
		if !ok {
			phases = append(phases, migrationPhaseStatusResponse{
				MigrationType: phaseType,
				Status:        string(store.MigrationProgressStatusPending),
			})
			continue
		}

		if row.Status == store.MigrationProgressStatusRunning || row.Status == store.MigrationProgressStatusPaused {
			active = true
		}

		phases = append(phases, migrationPhaseStatusResponse{
			MigrationType:  phaseType,
			Status:         string(row.Status),
			TotalItems:     row.TotalItems,
			ProcessedItems: row.ProcessedItems,
			FailedItems:    row.FailedItems,
			CurrentLabel:   row.CurrentLabel,
			Error:          row.Error,
		})
	}

	return openClawMigrationStatusResponse{
		Active: active,
		Phases: phases,
	}, nil
}

func (s *defaultOpenClawMigrationControlPlaneService) Report(
	ctx context.Context,
	orgID string,
) (openClawMigrationReportResponse, error) {
	if s == nil || s.progressStore == nil || s.failureStore == nil {
		return openClawMigrationReportResponse{}, fmt.Errorf("migration report store not configured")
	}

	progress, err := s.progressStore.GetByType(ctx, orgID, "history_backfill")
	if err != nil {
		return openClawMigrationReportResponse{}, err
	}
	failures, err := s.failureStore.ListByOrg(ctx, orgID, store.ListOpenClawHistoryFailureOptions{
		MigrationType: "history_backfill",
		Limit:         maxOpenClawMigrationFailuresLimit,
	})
	if err != nil {
		return openClawMigrationReportResponse{}, err
	}

	eventsExpected := 0
	failedItems := 0
	if progress != nil {
		eventsExpected = progress.ProcessedItems
		if eventsExpected < 0 {
			eventsExpected = 0
		}
		failedItems = progress.FailedItems
		if failedItems < 0 {
			failedItems = 0
		}
	}

	skippedUnknownAttempts := 0
	for _, failure := range failures {
		if strings.EqualFold(strings.TrimSpace(failure.ErrorReason), "skipped_unknown_agent") {
			if failure.AttemptCount > 0 {
				skippedUnknownAttempts += failure.AttemptCount
			}
		}
	}

	messagesInserted := eventsExpected - skippedUnknownAttempts - failedItems
	if messagesInserted < 0 {
		messagesInserted = 0
	}
	eventsProcessed := messagesInserted
	numerator := messagesInserted + skippedUnknownAttempts + failedItems

	completenessRatio := 1.0
	if eventsExpected > 0 {
		completenessRatio = float64(numerator) / float64(eventsExpected)
		if completenessRatio < 0 {
			completenessRatio = 0
		}
		if completenessRatio > 1 {
			completenessRatio = 1
		}
	}

	return openClawMigrationReportResponse{
		EventsExpected:            eventsExpected,
		EventsProcessed:           eventsProcessed,
		MessagesInserted:          messagesInserted,
		EventsSkippedUnknownAgent: skippedUnknownAttempts,
		FailedItems:               failedItems,
		CompletenessRatio:         completenessRatio,
		IsComplete:                numerator == eventsExpected,
	}, nil
}

func (s *defaultOpenClawMigrationControlPlaneService) Failures(
	ctx context.Context,
	orgID string,
	limit int,
) (openClawMigrationFailuresResponse, error) {
	if s == nil || s.failureStore == nil {
		return openClawMigrationFailuresResponse{}, fmt.Errorf("migration failure store not configured")
	}

	if limit <= 0 {
		limit = defaultOpenClawMigrationFailuresLimit
	}
	if limit > maxOpenClawMigrationFailuresLimit {
		limit = maxOpenClawMigrationFailuresLimit
	}

	rows, err := s.failureStore.ListByOrg(ctx, orgID, store.ListOpenClawHistoryFailureOptions{
		MigrationType: "history_backfill",
		Limit:         limit,
	})
	if err != nil {
		return openClawMigrationFailuresResponse{}, err
	}

	items := make([]openClawMigrationFailureItem, 0, len(rows))
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.ErrorReason), "skipped_unknown_agent") {
			continue
		}
		items = append(items, openClawMigrationFailureItem{
			OrgID:              row.OrgID,
			MigrationType:      row.MigrationType,
			BatchID:            row.BatchID,
			AgentSlug:          row.AgentSlug,
			SessionID:          row.SessionID,
			EventID:            row.EventID,
			SessionPath:        row.SessionPath,
			Line:               row.Line,
			MessageIDCandidate: row.MessageIDCandidate,
			ErrorReason:        row.ErrorReason,
			ErrorMessage:       row.ErrorMessage,
			FirstSeenAt:        row.FirstSeenAt,
			LastSeenAt:         row.LastSeenAt,
			AttemptCount:       row.AttemptCount,
		})
	}

	return openClawMigrationFailuresResponse{
		Items: items,
		Total: len(items),
	}, nil
}

type openClawMigrationValidationError struct {
	message string
}

func (e openClawMigrationValidationError) Error() string {
	return e.message
}

type openClawMigrationConflictError struct {
	message string
}

func (e openClawMigrationConflictError) Error() string {
	return e.message
}

func (s *defaultOpenClawMigrationControlPlaneService) Run(
	ctx context.Context,
	orgID string,
	input openClawMigrationRunRequest,
) (openClawMigrationRunResponse, error) {
	if s == nil || s.progressStore == nil {
		return openClawMigrationRunResponse{}, fmt.Errorf("migration progress store not configured")
	}
	if input.AgentsOnly && input.HistoryOnly {
		return openClawMigrationRunResponse{}, openClawMigrationValidationError{
			message: "agents_only and history_only cannot both be true",
		}
	}

	selectedPhases, err := selectOpenClawPhases(input)
	if err != nil {
		return openClawMigrationRunResponse{}, err
	}

	response := openClawMigrationRunResponse{
		Accepted:       true,
		SelectedPhases: selectedPhases,
	}

	for _, phaseType := range selectedPhases {
		progress, progressErr := s.progressStore.GetByType(ctx, orgID, phaseType)
		if progressErr != nil {
			return openClawMigrationRunResponse{}, progressErr
		}

		if progress == nil {
			_, startErr := s.progressStore.StartPhase(ctx, store.StartMigrationProgressInput{
				OrgID:         orgID,
				MigrationType: phaseType,
				CurrentLabel:  openClawMigrationStringPtr("started via api run"),
			})
			if startErr != nil {
				return openClawMigrationRunResponse{}, startErr
			}
			response.StartedPhases = append(response.StartedPhases, phaseType)
			continue
		}

		switch progress.Status {
		case store.MigrationProgressStatusCompleted:
			response.SkippedCompletedPhases = append(response.SkippedCompletedPhases, phaseType)
		case store.MigrationProgressStatusRunning:
			response.AlreadyRunningPhases = append(response.AlreadyRunningPhases, phaseType)
		case store.MigrationProgressStatusPaused:
			if !input.ForceResumePaused {
				response.PausedPhases = append(response.PausedPhases, phaseType)
				continue
			}
			_, setErr := s.progressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
				OrgID:         orgID,
				MigrationType: phaseType,
				Status:        store.MigrationProgressStatusRunning,
				CurrentLabel:  openClawMigrationStringPtr("resumed via api run"),
			})
			if setErr != nil {
				return openClawMigrationRunResponse{}, setErr
			}
			response.ResumedPhases = append(response.ResumedPhases, phaseType)
		case store.MigrationProgressStatusFailed:
			return openClawMigrationRunResponse{}, openClawMigrationConflictError{
				message: fmt.Sprintf("migration phase %q is failed; reset status before rerun", phaseType),
			}
		default:
			_, setErr := s.progressStore.SetStatus(ctx, store.SetMigrationProgressStatusInput{
				OrgID:         orgID,
				MigrationType: phaseType,
				Status:        store.MigrationProgressStatusRunning,
				CurrentLabel:  openClawMigrationStringPtr("started via api run"),
			})
			if setErr != nil {
				return openClawMigrationRunResponse{}, setErr
			}
			response.StartedPhases = append(response.StartedPhases, phaseType)
		}
	}

	return response, nil
}

func (s *defaultOpenClawMigrationControlPlaneService) Pause(
	ctx context.Context,
	orgID string,
) (openClawMigrationMutationResponse, error) {
	if s == nil || s.progressStore == nil {
		return openClawMigrationMutationResponse{}, fmt.Errorf("migration progress store not configured")
	}
	updated, err := s.progressStore.UpdateStatusByOrg(
		ctx,
		orgID,
		store.MigrationProgressStatusRunning,
		store.MigrationProgressStatusPaused,
	)
	if err != nil {
		return openClawMigrationMutationResponse{}, err
	}
	return openClawMigrationMutationResponse{
		Status:        string(store.MigrationProgressStatusPaused),
		UpdatedPhases: updated,
	}, nil
}

func (s *defaultOpenClawMigrationControlPlaneService) Resume(
	ctx context.Context,
	orgID string,
) (openClawMigrationMutationResponse, error) {
	if s == nil || s.progressStore == nil {
		return openClawMigrationMutationResponse{}, fmt.Errorf("migration progress store not configured")
	}
	updated, err := s.progressStore.UpdateStatusByOrg(
		ctx,
		orgID,
		store.MigrationProgressStatusPaused,
		store.MigrationProgressStatusRunning,
	)
	if err != nil {
		return openClawMigrationMutationResponse{}, err
	}
	return openClawMigrationMutationResponse{
		Status:        string(store.MigrationProgressStatusRunning),
		UpdatedPhases: updated,
	}, nil
}

func (s *defaultOpenClawMigrationControlPlaneService) Reset(
	ctx context.Context,
	orgID string,
) (openClawMigrationResetResponse, error) {
	if s == nil || s.resetStore == nil {
		return openClawMigrationResetResponse{}, fmt.Errorf("migration reset store not configured")
	}

	result, err := s.resetStore.Reset(ctx, store.OpenClawMigrationResetInput{
		OrgID:              orgID,
		OpenClawPhaseTypes: openClawMigrationPhaseOrder,
	})
	if err != nil {
		return openClawMigrationResetResponse{}, err
	}

	deleted := result.Deleted
	if deleted == nil {
		deleted = map[string]int{}
	}

	return openClawMigrationResetResponse{
		Status:              "reset",
		PausedPhases:        result.PausedPhases,
		ProgressRowsDeleted: result.ProgressRowsDeleted,
		Deleted:             deleted,
		TotalDeleted:        result.TotalDeleted,
	}, nil
}

func selectOpenClawPhases(input openClawMigrationRunRequest) ([]string, error) {
	phases := make([]string, 0, len(openClawMigrationPhaseOrder))
	switch {
	case input.AgentsOnly:
		phases = append(phases, "agent_import")
	case input.HistoryOnly:
		phases = append(phases, "history_backfill")
	default:
		phases = append(phases, openClawMigrationPhaseOrder...)
	}

	startPhase := strings.TrimSpace(input.StartPhase)
	if startPhase == "" {
		return phases, nil
	}
	if !isKnownOpenClawMigrationPhase(startPhase) {
		return nil, openClawMigrationValidationError{
			message: fmt.Sprintf("start_phase %q is not recognized", startPhase),
		}
	}

	index := -1
	for i, phaseType := range phases {
		if phaseType == startPhase {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, openClawMigrationValidationError{
			message: fmt.Sprintf("start_phase %q is incompatible with selected mode", startPhase),
		}
	}
	return phases[index:], nil
}

func isKnownOpenClawMigrationPhase(phaseType string) bool {
	trimmed := strings.TrimSpace(phaseType)
	for _, known := range openClawMigrationPhaseOrder {
		if known == trimmed {
			return true
		}
	}
	return false
}

func openClawMigrationStringPtr(value string) *string {
	return &value
}

type OpenClawMigrationControlPlaneHandler struct {
	service openClawMigrationControlPlaneService
}

func NewOpenClawMigrationControlPlaneHandler(db *sql.DB) *OpenClawMigrationControlPlaneHandler {
	return &OpenClawMigrationControlPlaneHandler{
		service: newOpenClawMigrationControlPlaneService(db),
	}
}

func newOpenClawMigrationControlPlaneHandlerWithService(
	service openClawMigrationControlPlaneService,
) *OpenClawMigrationControlPlaneHandler {
	return &OpenClawMigrationControlPlaneHandler{service: service}
}

func (h *OpenClawMigrationControlPlaneHandler) Status(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	status, err := h.service.Status(r.Context(), orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list migration progress"})
		return
	}
	sendJSON(w, http.StatusOK, status)
}

func (h *OpenClawMigrationControlPlaneHandler) Report(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	report, err := h.service.Report(r.Context(), orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to build migration report"})
		return
	}
	sendJSON(w, http.StatusOK, report)
}

func (h *OpenClawMigrationControlPlaneHandler) Failures(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	limit := defaultOpenClawMigrationFailuresLimit
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be a positive integer"})
			return
		}
		limit = parsedLimit
	}

	failures, err := h.service.Failures(r.Context(), orgID, limit)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list migration failures"})
		return
	}
	sendJSON(w, http.StatusOK, failures)
}

func (h *OpenClawMigrationControlPlaneHandler) Run(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	request := openClawMigrationRunRequest{}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&request)
		if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	response, err := h.service.Run(r.Context(), orgID, request)
	if err != nil {
		var validationErr openClawMigrationValidationError
		if errors.As(err, &validationErr) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: validationErr.Error()})
			return
		}
		var conflictErr openClawMigrationConflictError
		if errors.As(err, &conflictErr) {
			sendJSON(w, http.StatusConflict, errorResponse{Error: conflictErr.Error()})
			return
		}
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to start migration run"})
		return
	}

	sendJSON(w, http.StatusAccepted, response)
}

func (h *OpenClawMigrationControlPlaneHandler) Pause(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	response, err := h.service.Pause(r.Context(), orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to pause migration"})
		return
	}
	sendJSON(w, http.StatusOK, response)
}

func (h *OpenClawMigrationControlPlaneHandler) Resume(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	response, err := h.service.Resume(r.Context(), orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to resume migration"})
		return
	}
	sendJSON(w, http.StatusOK, response)
}

func (h *OpenClawMigrationControlPlaneHandler) Reset(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.service == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration control plane unavailable"})
		return
	}

	request := openClawMigrationResetRequest{}
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&request)
		if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	if strings.TrimSpace(request.Confirm) != openClawMigrationResetConfirmToken {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "confirm token required"})
		return
	}

	response, err := h.service.Reset(r.Context(), orgID)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to reset migration state"})
		return
	}

	log.Printf(
		"openclaw migration reset completed: org_id=%s paused_phases=%d progress_rows_deleted=%d total_deleted=%d deleted=%v",
		orgID,
		response.PausedPhases,
		response.ProgressRowsDeleted,
		response.TotalDeleted,
		response.Deleted,
	)

	sendJSON(w, http.StatusOK, response)
}
