package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type OpenClawMigrationImportHandler struct {
	db            *sql.DB
	progressStore *store.MigrationProgressStore
}

type openClawMigrationImportAgentsResponse struct {
	Processed      int      `json:"processed"`
	Inserted       int      `json:"inserted"`
	Updated        int      `json:"updated"`
	Skipped        int      `json:"skipped"`
	ActiveAgents   int      `json:"active_agents"`
	InactiveAgents int      `json:"inactive_agents"`
	Warnings       []string `json:"warnings,omitempty"`
}

type openClawMigrationImportHistoryBatchResponse struct {
	EventsReceived            int      `json:"events_received"`
	EventsProcessed           int      `json:"events_processed"`
	MessagesInserted          int      `json:"messages_inserted"`
	RoomsCreated              int      `json:"rooms_created"`
	ParticipantsAdded         int      `json:"participants_added"`
	EventsSkippedUnknownAgent int      `json:"events_skipped_unknown_agent"`
	FailedItems               int      `json:"failed_items"`
	Warnings                  []string `json:"warnings,omitempty"`
}

func NewOpenClawMigrationImportHandler(db *sql.DB) *OpenClawMigrationImportHandler {
	return &OpenClawMigrationImportHandler{
		db:            db,
		progressStore: store.NewMigrationProgressStore(db),
	}
}

func (h *OpenClawMigrationImportHandler) ImportAgents(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.db == nil || h.progressStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration import unavailable"})
		return
	}

	var req openClawMigrationImportAgentsRequest
	status, err := decodeOpenClawMigrationImportRequest(w, r, &req)
	if err != nil {
		if status == http.StatusRequestEntityTooLarge {
			sendJSON(w, status, errorResponse{Error: "request body too large"})
			return
		}
		sendJSON(w, status, errorResponse{Error: "invalid request body"})
		return
	}
	if err := validateOpenClawMigrationImportAgentsRequest(req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	result, err := importer.ImportOpenClawAgentsFromPayload(r.Context(), h.db, importer.OpenClawAgentPayloadImportOptions{
		OrgID:      orgID,
		Identities: mapOpenClawMigrationImportIdentities(req.Identities),
	})
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to import openclaw agents"})
		return
	}

	totalItems := len(req.Identities)
	if err := h.advanceMigrationProgress(
		r,
		orgID,
		"agent_import",
		&totalItems,
		result.Processed,
		result.Skipped,
		"imported agents batch via api",
	); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update migration progress"})
		return
	}

	sendJSON(w, http.StatusOK, openClawMigrationImportAgentsResponse{
		Processed:      result.Processed,
		Inserted:       result.Inserted,
		Updated:        result.Updated,
		Skipped:        result.Skipped,
		ActiveAgents:   result.ActiveAgents,
		InactiveAgents: result.InactiveAgents,
		Warnings:       result.Warnings,
	})
}

func (h *OpenClawMigrationImportHandler) ImportHistoryBatch(w http.ResponseWriter, r *http.Request) {
	orgID := middleware.WorkspaceFromContext(r.Context())
	if strings.TrimSpace(orgID) == "" {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
		return
	}
	if h == nil || h.db == nil || h.progressStore == nil {
		sendJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "migration import unavailable"})
		return
	}

	var req openClawMigrationImportHistoryBatchRequest
	status, err := decodeOpenClawMigrationImportRequest(w, r, &req)
	if err != nil {
		if status == http.StatusRequestEntityTooLarge {
			sendJSON(w, status, errorResponse{Error: "request body too large"})
			return
		}
		sendJSON(w, status, errorResponse{Error: "invalid request body"})
		return
	}
	if err := validateOpenClawMigrationImportHistoryBatchRequest(req); err != nil {
		sendJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	result, err := importer.BackfillOpenClawHistoryFromBatchPayload(
		r.Context(),
		h.db,
		importer.OpenClawHistoryBatchPayloadOptions{
			OrgID:  orgID,
			UserID: req.UserID,
			Batch: importer.OpenClawHistoryBatch{
				ID:    req.Batch.ID,
				Index: req.Batch.Index,
				Total: req.Batch.Total,
			},
			Events: mapOpenClawMigrationImportEvents(req.Events),
		},
	)
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to import openclaw history batch"})
		return
	}

	totalItems := req.Batch.Total
	processedDelta := result.EventsProcessed + result.EventsSkippedUnknownAgent + result.FailedItems
	if err := h.advanceMigrationProgress(
		r,
		orgID,
		"history_backfill",
		&totalItems,
		processedDelta,
		result.FailedItems,
		fmt.Sprintf("imported history batch %d/%d", req.Batch.Index, req.Batch.Total),
	); err != nil {
		sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to update migration progress"})
		return
	}

	sendJSON(w, http.StatusOK, openClawMigrationImportHistoryBatchResponse{
		EventsReceived:            result.EventsReceived,
		EventsProcessed:           result.EventsProcessed,
		MessagesInserted:          result.MessagesInserted,
		RoomsCreated:              result.RoomsCreated,
		ParticipantsAdded:         result.ParticipantsAdded,
		EventsSkippedUnknownAgent: result.EventsSkippedUnknownAgent,
		FailedItems:               result.FailedItems,
		Warnings:                  result.Warnings,
	})
}

func (h *OpenClawMigrationImportHandler) advanceMigrationProgress(
	r *http.Request,
	orgID string,
	migrationType string,
	totalItems *int,
	processedDelta int,
	failedDelta int,
	currentLabel string,
) error {
	progress, err := h.progressStore.GetByType(r.Context(), orgID, migrationType)
	if err != nil {
		return err
	}

	label := strings.TrimSpace(currentLabel)
	if progress == nil {
		_, err := h.progressStore.StartPhase(r.Context(), store.StartMigrationProgressInput{
			OrgID:         orgID,
			MigrationType: migrationType,
			TotalItems:    totalItems,
			CurrentLabel:  openClawMigrationStringPtr(label),
		})
		if err != nil {
			return err
		}
	} else if progress.Status != store.MigrationProgressStatusRunning {
		_, err := h.progressStore.SetStatus(r.Context(), store.SetMigrationProgressStatusInput{
			OrgID:         orgID,
			MigrationType: migrationType,
			Status:        store.MigrationProgressStatusRunning,
			CurrentLabel:  openClawMigrationStringPtr(label),
		})
		if err != nil {
			return err
		}
	}

	if processedDelta == 0 && failedDelta == 0 {
		return nil
	}
	_, err = h.progressStore.Advance(r.Context(), store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  migrationType,
		ProcessedDelta: processedDelta,
		FailedDelta:    failedDelta,
		CurrentLabel:   openClawMigrationStringPtr(label),
	})
	return err
}
