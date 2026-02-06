package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/syncmetrics"
	"github.com/stretchr/testify/require"
)

func TestGitHubSyncHealthIncludesQueueDepthStuckJobsAndMetrics(t *testing.T) {
	syncmetrics.ResetForTests()

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "sync-health-org")
	projectID := insertProjectTestProject(t, db, orgID, "Sync Health Project")

	jobStore := store.NewGitHubSyncJobStore(db)
	workspaceCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)

	_, err := jobStore.Enqueue(workspaceCtx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeRepoSync,
		Payload:     json.RawMessage(`{"branch":"main"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	_, err = jobStore.Enqueue(workspaceCtx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"cursor":"page=1"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	picked, err := jobStore.PickupNext(workspaceCtx, store.GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, picked)

	_, err = db.Exec(
		`UPDATE github_sync_jobs SET updated_at = NOW() - interval '45 minutes' WHERE id = $1`,
		picked.ID,
	)
	require.NoError(t, err)

	syncmetrics.RecordQuota("issue_import", 5000, 4500, time.Now().UTC().Add(30*time.Minute))
	syncmetrics.RecordThrottle("issue_import")

	ownerID := insertTestUserWithRole(t, db, orgID, "owner-health", RoleOwner)
	token := "oc_sess_sync_health_owner"
	insertTestSession(t, db, orgID, ownerID, token, time.Now().UTC().Add(time.Hour))

	handler := &GitHubSyncHealthHandler{Store: jobStore}
	router := chi.NewRouter()
	router.With(RequireCapability(db, CapabilityGitHubManualSync)).Get("/api/github/sync/health", handler.Get)

	req := httptest.NewRequest(http.MethodGet, "/api/github/sync/health?stuck_threshold=10m", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp githubSyncHealthResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "10m0s", resp.StuckThreshold)
	require.Equal(t, 1, resp.StuckJobs)

	depthByType := map[string]store.GitHubSyncQueueDepth{}
	for _, row := range resp.QueueDepth {
		depthByType[row.JobType] = row
	}
	require.Equal(t, 1, depthByType[store.GitHubSyncJobTypeRepoSync].Queued)
	require.Equal(t, 1, depthByType[store.GitHubSyncJobTypeIssueImport].InProgress)

	issueMetrics, ok := resp.Metrics.Jobs[store.GitHubSyncJobTypeIssueImport]
	require.True(t, ok)
	require.Equal(t, int64(1), issueMetrics.PickedTotal)

	quotaMetrics, ok := resp.Metrics.Quota[store.GitHubSyncJobTypeIssueImport]
	require.True(t, ok)
	require.Equal(t, 5000, quotaMetrics.Limit)
	require.Equal(t, 4500, quotaMetrics.Remaining)
	require.Equal(t, int64(1), quotaMetrics.ThrottleEvents)
}
