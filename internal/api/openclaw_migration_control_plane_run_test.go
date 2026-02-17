package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationRunEndpointStartsAndSkipsCompletedPhases(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000411"
	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:         orgID,
					MigrationType: "agent_import",
					Status:        store.MigrationProgressStatusCompleted,
				},
				{
					OrgID:         orgID,
					MigrationType: "history_backfill",
					Status:        store.MigrationProgressStatusRunning,
				},
				{
					OrgID:         orgID,
					MigrationType: "taxonomy_classification",
					Status:        store.MigrationProgressStatusCompleted,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	req := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/run", bytes.NewBufferString(`{}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Run(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload openClawMigrationRunResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Accepted)
	require.Equal(t, openClawMigrationPhaseOrder, payload.SelectedPhases)
	require.Equal(t, []string{"agent_import", "taxonomy_classification"}, payload.SkippedCompletedPhases)
	require.Equal(t, []string{"history_backfill"}, payload.AlreadyRunningPhases)
	require.ElementsMatch(
		t,
		[]string{"history_embedding_1536", "memory_extraction", "entity_synthesis", "memory_dedup", "project_discovery", "project_docs_scanning"},
		payload.StartedPhases,
	)
	require.Empty(t, payload.ResumedPhases)

	require.Len(t, progressStore.startPhaseInputs, 6)
	require.Len(t, progressStore.setStatusInputs, 0)
}

func TestOpenClawMigrationRunEndpointValidatesOptions(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000422"
	progressStore := newFakeOpenClawMigrationProgressStore(map[string][]store.MigrationProgress{})
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	tests := []struct {
		name string
		body string
	}{
		{
			name: "rejects conflicting mode flags",
			body: `{"agents_only":true,"history_only":true}`,
		},
		{
			name: "rejects unknown start phase",
			body: `{"start_phase":"not_a_phase"}`,
		},
		{
			name: "rejects incompatible start phase and mode",
			body: `{"history_only":true,"start_phase":"entity_synthesis"}`,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/run", bytes.NewBufferString(tc.body))
			req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
			rec := httptest.NewRecorder()
			handler.Run(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestOpenClawMigrationRunEndpointForceResumePaused(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000433"
	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:         orgID,
					MigrationType: "history_backfill",
					Status:        store.MigrationProgressStatusPaused,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/run",
		bytes.NewBufferString(`{"history_only":true,"force_resume_paused":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Run(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload openClawMigrationRunResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, []string{"history_backfill"}, payload.SelectedPhases)
	require.Equal(t, []string{"history_backfill"}, payload.ResumedPhases)
	require.Empty(t, payload.PausedPhases)
	require.Empty(t, payload.StartedPhases)

	require.Len(t, progressStore.setStatusInputs, 1)
	require.Equal(t, store.MigrationProgressStatusRunning, progressStore.setStatusInputs[0].Status)
}
