package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var openClawMigrationPhaseOrder = []string{
	"agent_import",
	"history_backfill",
	"memory_extraction",
	"entity_synthesis",
	"memory_dedup",
	"taxonomy_classification",
	"project_discovery",
	"project_docs_scanning",
}

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

type openClawMigrationMutationResponse struct {
	Status        string `json:"status"`
	UpdatedPhases int    `json:"updated_phases"`
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

type openClawMigrationControlPlaneService interface {
	Status(ctx context.Context, orgID string) (openClawMigrationStatusResponse, error)
	Run(ctx context.Context, orgID string, input openClawMigrationRunRequest) (openClawMigrationRunResponse, error)
	Pause(ctx context.Context, orgID string) (openClawMigrationMutationResponse, error)
	Resume(ctx context.Context, orgID string) (openClawMigrationMutationResponse, error)
}

type defaultOpenClawMigrationControlPlaneService struct {
	progressStore openClawMigrationProgressStore
}

func newOpenClawMigrationControlPlaneServiceWithStore(
	progressStore openClawMigrationProgressStore,
) *defaultOpenClawMigrationControlPlaneService {
	return &defaultOpenClawMigrationControlPlaneService{progressStore: progressStore}
}

func newOpenClawMigrationControlPlaneService(db *sql.DB) *defaultOpenClawMigrationControlPlaneService {
	if db == nil {
		return newOpenClawMigrationControlPlaneServiceWithStore(nil)
	}
	return newOpenClawMigrationControlPlaneServiceWithStore(store.NewMigrationProgressStore(db))
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
