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

func TestOpenClawMigrationPauseResumeEndpointsWorkspaceScoped(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000511"
	otherOrgID := "00000000-0000-0000-0000-000000000522"
	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:         orgID,
					MigrationType: "history_backfill",
					Status:        store.MigrationProgressStatusRunning,
				},
			},
			otherOrgID: {
				{
					OrgID:         otherOrgID,
					MigrationType: "history_backfill",
					Status:        store.MigrationProgressStatusRunning,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/pause", nil)
	pauseReq = pauseReq.WithContext(context.WithValue(pauseReq.Context(), middleware.WorkspaceIDKey, orgID))
	pauseRec := httptest.NewRecorder()
	handler.Pause(pauseRec, pauseReq)
	require.Equal(t, http.StatusOK, pauseRec.Code)

	var pausePayload openClawMigrationMutationResponse
	require.NoError(t, json.NewDecoder(pauseRec.Body).Decode(&pausePayload))
	require.Equal(t, "paused", pausePayload.Status)
	require.Equal(t, 1, pausePayload.UpdatedPhases)

	orgProgress, err := progressStore.GetByType(context.Background(), orgID, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, orgProgress)
	require.Equal(t, store.MigrationProgressStatusPaused, orgProgress.Status)

	otherProgress, err := progressStore.GetByType(context.Background(), otherOrgID, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, otherProgress)
	require.Equal(t, store.MigrationProgressStatusRunning, otherProgress.Status)

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/resume", nil)
	resumeReq = resumeReq.WithContext(context.WithValue(resumeReq.Context(), middleware.WorkspaceIDKey, orgID))
	resumeRec := httptest.NewRecorder()
	handler.Resume(resumeRec, resumeReq)
	require.Equal(t, http.StatusOK, resumeRec.Code)

	var resumePayload openClawMigrationMutationResponse
	require.NoError(t, json.NewDecoder(resumeRec.Body).Decode(&resumePayload))
	require.Equal(t, "running", resumePayload.Status)
	require.Equal(t, 1, resumePayload.UpdatedPhases)

	orgProgress, err = progressStore.GetByType(context.Background(), orgID, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, orgProgress)
	require.Equal(t, store.MigrationProgressStatusRunning, orgProgress.Status)
}

func TestOpenClawMigrationPauseResumeEndpointsAreIdempotentNoop(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000533"
	progressStore := newFakeOpenClawMigrationProgressStore(
		map[string][]store.MigrationProgress{
			orgID: {
				{
					OrgID:         orgID,
					MigrationType: "agent_import",
					Status:        store.MigrationProgressStatusCompleted,
				},
			},
		},
	)
	service := newOpenClawMigrationControlPlaneServiceWithStore(progressStore)
	handler := newOpenClawMigrationControlPlaneHandlerWithService(service)

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/pause", nil)
	pauseReq = pauseReq.WithContext(context.WithValue(pauseReq.Context(), middleware.WorkspaceIDKey, orgID))
	pauseRec := httptest.NewRecorder()
	handler.Pause(pauseRec, pauseReq)
	require.Equal(t, http.StatusOK, pauseRec.Code)

	var pausePayload openClawMigrationMutationResponse
	require.NoError(t, json.NewDecoder(pauseRec.Body).Decode(&pausePayload))
	require.Equal(t, "paused", pausePayload.Status)
	require.Equal(t, 0, pausePayload.UpdatedPhases)

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/resume", nil)
	resumeReq = resumeReq.WithContext(context.WithValue(resumeReq.Context(), middleware.WorkspaceIDKey, orgID))
	resumeRec := httptest.NewRecorder()
	handler.Resume(resumeRec, resumeReq)
	require.Equal(t, http.StatusOK, resumeRec.Code)

	var resumePayload openClawMigrationMutationResponse
	require.NoError(t, json.NewDecoder(resumeRec.Body).Decode(&resumePayload))
	require.Equal(t, "running", resumePayload.Status)
	require.Equal(t, 0, resumePayload.UpdatedPhases)
}
