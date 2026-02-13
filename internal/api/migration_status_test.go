package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestMigrationStatusEndpointReturnsPhaseProgress(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "migration-status-endpoint")
	otherOrgID := insertMessageTestOrganization(t, db, "migration-status-endpoint-other")

	progressStore := store.NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "agent_import",
		TotalItems:    migrationStatusIntPtr(10),
		CurrentLabel:  migrationStatusStringPtr("starting agent import"),
	})
	require.NoError(t, err)
	_, err = progressStore.Advance(context.Background(), store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "agent_import",
		ProcessedDelta: 10,
	})
	require.NoError(t, err)
	_, err = progressStore.SetStatus(context.Background(), store.SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "agent_import",
		Status:        store.MigrationProgressStatusCompleted,
		CurrentLabel:  migrationStatusStringPtr("agent import complete"),
	})
	require.NoError(t, err)

	_, err = progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "history_backfill",
		TotalItems:    migrationStatusIntPtr(20),
		CurrentLabel:  migrationStatusStringPtr("starting history backfill"),
	})
	require.NoError(t, err)
	_, err = progressStore.Advance(context.Background(), store.AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "history_backfill",
		ProcessedDelta: 7,
	})
	require.NoError(t, err)

	_, err = progressStore.StartPhase(context.Background(), store.StartMigrationProgressInput{
		OrgID:         otherOrgID,
		MigrationType: "agent_import",
		TotalItems:    migrationStatusIntPtr(1),
	})
	require.NoError(t, err)

	handler := handleMigrationStatus(db)

	req := httptest.NewRequest(http.MethodGet, "/api/migrations/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Active bool `json:"active"`
		Phases []struct {
			MigrationType  string `json:"migration_type"`
			Status         string `json:"status"`
			ProcessedItems int    `json:"processed_items"`
			FailedItems    int    `json:"failed_items"`
			CurrentLabel   string `json:"current_label"`
			TotalItems     *int   `json:"total_items"`
		} `json:"phases"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Active)
	require.Len(t, payload.Phases, 2)

	require.Equal(t, "agent_import", payload.Phases[0].MigrationType)
	require.Equal(t, "completed", payload.Phases[0].Status)
	require.Equal(t, 10, payload.Phases[0].ProcessedItems)
	require.NotNil(t, payload.Phases[0].TotalItems)
	require.Equal(t, 10, *payload.Phases[0].TotalItems)

	require.Equal(t, "history_backfill", payload.Phases[1].MigrationType)
	require.Equal(t, "running", payload.Phases[1].Status)
	require.Equal(t, 7, payload.Phases[1].ProcessedItems)
	require.NotNil(t, payload.Phases[1].TotalItems)
	require.Equal(t, 20, *payload.Phases[1].TotalItems)
}

func TestMigrationStatusEndpointReturnsBadRequestWithoutWorkspaceContext(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := handleMigrationStatus(db)

	req := httptest.NewRequest(http.MethodGet, "/api/migrations/status", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func migrationStatusIntPtr(value int) *int {
	return &value
}

func migrationStatusStringPtr(value string) *string {
	return &value
}
