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

func TestOpenClawMigrationStatusEndpointWorkspaceScoped(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000111"
	otherOrgID := "00000000-0000-0000-0000-000000000222"

	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:          orgID,
					MigrationType:  "agent_import",
					Status:         store.MigrationProgressStatusCompleted,
					TotalItems:     migrationStatusIntPtr(10),
					ProcessedItems: 10,
				},
				{
					OrgID:          orgID,
					MigrationType:  "history_backfill",
					Status:         store.MigrationProgressStatusRunning,
					TotalItems:     migrationStatusIntPtr(20),
					ProcessedItems: 7,
					FailedItems:    1,
					CurrentLabel:   "processed 7/20 events",
				},
			},
			otherOrgID: {
				{
					OrgID:          otherOrgID,
					MigrationType:  "history_backfill",
					Status:         store.MigrationProgressStatusRunning,
					TotalItems:     migrationStatusIntPtr(99),
					ProcessedItems: 42,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	req := httptest.NewRequest(http.MethodGet, "/api/migrations/openclaw/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Status(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{orgID}, progressStore.listByOrgCalls)

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
	require.Len(t, payload.Phases, 8)

	require.Equal(t, "agent_import", payload.Phases[0].MigrationType)
	require.Equal(t, "completed", payload.Phases[0].Status)
	require.Equal(t, 10, payload.Phases[0].ProcessedItems)
	require.NotNil(t, payload.Phases[0].TotalItems)
	require.Equal(t, 10, *payload.Phases[0].TotalItems)

	require.Equal(t, "history_backfill", payload.Phases[1].MigrationType)
	require.Equal(t, "running", payload.Phases[1].Status)
	require.Equal(t, 7, payload.Phases[1].ProcessedItems)
	require.Equal(t, 1, payload.Phases[1].FailedItems)
	require.NotNil(t, payload.Phases[1].TotalItems)
	require.Equal(t, 20, *payload.Phases[1].TotalItems)
	require.Equal(t, "processed 7/20 events", payload.Phases[1].CurrentLabel)

	require.Equal(t, "memory_extraction", payload.Phases[2].MigrationType)
	require.Equal(t, "pending", payload.Phases[2].Status)
}

func TestOpenClawMigrationStatusEndpointReturnsKnownPhaseOrder(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000333"
	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:         orgID,
					MigrationType: "taxonomy_classification",
					Status:        store.MigrationProgressStatusRunning,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	req := httptest.NewRequest(http.MethodGet, "/api/migrations/openclaw/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Status(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Phases []struct {
			MigrationType string `json:"migration_type"`
		} `json:"phases"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(
		t,
		[]string{
			"agent_import",
			"history_backfill",
			"memory_extraction",
			"entity_synthesis",
			"memory_dedup",
			"taxonomy_classification",
			"project_discovery",
			"project_docs_scanning",
		},
		[]string{
			payload.Phases[0].MigrationType,
			payload.Phases[1].MigrationType,
			payload.Phases[2].MigrationType,
			payload.Phases[3].MigrationType,
			payload.Phases[4].MigrationType,
			payload.Phases[5].MigrationType,
			payload.Phases[6].MigrationType,
			payload.Phases[7].MigrationType,
		},
	)
}
