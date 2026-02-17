package api

import (
	"context"
	"database/sql"
	"fmt"
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

type openClawMigrationProgressStore interface {
	ListByOrg(ctx context.Context, orgID string) ([]store.MigrationProgress, error)
}

type openClawMigrationControlPlaneService interface {
	Status(ctx context.Context, orgID string) (openClawMigrationStatusResponse, error)
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

func (h *OpenClawMigrationControlPlaneHandler) Run(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}

func (h *OpenClawMigrationControlPlaneHandler) Pause(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}

func (h *OpenClawMigrationControlPlaneHandler) Resume(w http.ResponseWriter, _ *http.Request) {
	sendJSON(w, http.StatusNotImplemented, errorResponse{Error: "not implemented"})
}
