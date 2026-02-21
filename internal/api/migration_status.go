package api

import (
	"database/sql"
	"net/http"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type migrationPhaseStatusResponse struct {
	MigrationType  string  `json:"migration_type"`
	Status         string  `json:"status"`
	TotalItems     *int    `json:"total_items,omitempty"`
	ProcessedItems int     `json:"processed_items"`
	FailedItems    int     `json:"failed_items"`
	CurrentLabel   string  `json:"current_label,omitempty"`
	Error          *string `json:"error,omitempty"`
}

func handleMigrationStatus(db *sql.DB) http.HandlerFunc {
	progressStore := store.NewMigrationProgressStore(db)

	return func(w http.ResponseWriter, r *http.Request) {
		orgID := middleware.WorkspaceFromContext(r.Context())
		if orgID == "" {
			sendJSON(w, http.StatusBadRequest, errorResponse{Error: "workspace context required"})
			return
		}

		progressRows, err := progressStore.ListByOrg(r.Context(), orgID)
		if err != nil {
			sendJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list migration progress"})
			return
		}

		phases := make([]migrationPhaseStatusResponse, 0, len(progressRows))
		active := false
		for _, row := range progressRows {
			status := string(row.Status)
			if row.Status == store.MigrationProgressStatusRunning || row.Status == store.MigrationProgressStatusPaused {
				active = true
			}

			phases = append(phases, migrationPhaseStatusResponse{
				MigrationType:  row.MigrationType,
				Status:         status,
				TotalItems:     row.TotalItems,
				ProcessedItems: row.ProcessedItems,
				FailedItems:    row.FailedItems,
				CurrentLabel:   row.CurrentLabel,
				Error:          row.Error,
			})
		}

		sendJSON(w, http.StatusOK, map[string]any{
			"active": active,
			"phases": phases,
		})
	}
}
