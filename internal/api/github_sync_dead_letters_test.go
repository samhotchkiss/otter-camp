package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func createDeadLetterFixture(t *testing.T, db *sql.DB, orgID string) (string, string) {
	t.Helper()

	projectID := insertProjectTestProject(t, db, orgID, "Dead Letter Project")
	jobStore := store.NewGitHubSyncJobStore(db)
	workspaceCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)

	enqueued, err := jobStore.Enqueue(workspaceCtx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"repo":"samhotchkiss/otter-camp"}`),
		MaxAttempts: 1,
	})
	require.NoError(t, err)

	picked, err := jobStore.PickupNext(workspaceCtx, store.GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, picked)
	require.Equal(t, enqueued.ID, picked.ID)

	failure, err := jobStore.RecordFailure(workspaceCtx, picked.ID, store.RecordGitHubSyncFailureInput{
		ErrorClass:   "terminal",
		ErrorMessage: "unprocessable payload",
		Retryable:    false,
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotNil(t, failure.DeadLetter)

	return failure.DeadLetter.ID, picked.ID
}

func buildDeadLetterRouter(db *sql.DB, jobStore *store.GitHubSyncJobStore) http.Handler {
	handler := &GitHubSyncDeadLettersHandler{Store: jobStore}
	router := chi.NewRouter()
	router.With(RequireCapability(db, CapabilityGitHubManualSync)).Get("/api/github/sync/dead-letters", handler.List)
	router.With(RequireCapability(db, CapabilityGitHubManualSync)).Post("/api/github/sync/dead-letters/{id}/replay", handler.Replay)
	return router
}

func TestGitHubSyncDeadLettersListAndReplay(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dead-letter-org")
	deadLetterID, jobID := createDeadLetterFixture(t, db, orgID)
	jobStore := store.NewGitHubSyncJobStore(db)

	ownerID := insertTestUserWithRole(t, db, orgID, "owner-1", RoleOwner)
	ownerToken := "oc_sess_dead_letter_owner"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))

	router := buildDeadLetterRouter(db, jobStore)

	listReq := httptest.NewRequest(http.MethodGet, "/api/github/sync/dead-letters", nil)
	listReq.Header.Set("Authorization", "Bearer "+ownerToken)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)

	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp listDeadLettersResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Equal(t, 1, listResp.Total)
	require.Equal(t, deadLetterID, listResp.DeadLetters[0].ID)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/github/sync/dead-letters/"+deadLetterID+"/replay", nil)
	replayReq.Header.Set("Authorization", "Bearer "+ownerToken)
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)

	require.Equal(t, http.StatusOK, replayRec.Code)
	var replayed store.GitHubSyncJob
	require.NoError(t, json.NewDecoder(replayRec.Body).Decode(&replayed))
	require.Equal(t, jobID, replayed.ID)
	require.Equal(t, store.GitHubSyncJobStatusQueued, replayed.Status)
}

func TestGitHubSyncDeadLettersRequiresCapability(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dead-letter-forbidden-org")
	deadLetterID, _ := createDeadLetterFixture(t, db, orgID)
	jobStore := store.NewGitHubSyncJobStore(db)

	viewerID := insertTestUserWithRole(t, db, orgID, "viewer-1", RoleViewer)
	viewerToken := "oc_sess_dead_letter_viewer"
	insertTestSession(t, db, orgID, viewerID, viewerToken, time.Now().UTC().Add(time.Hour))

	router := buildDeadLetterRouter(db, jobStore)

	listReq := httptest.NewRequest(http.MethodGet, "/api/github/sync/dead-letters", nil)
	listReq.Header.Set("Authorization", "Bearer "+viewerToken)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusForbidden, listRec.Code)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/github/sync/dead-letters/"+deadLetterID+"/replay", nil)
	replayReq.Header.Set("Authorization", "Bearer "+viewerToken)
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)
	require.Equal(t, http.StatusForbidden, replayRec.Code)

	var payload forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(replayRec.Body).Decode(&payload))
	require.Equal(t, CapabilityGitHubManualSync, payload.Capability)
}

func TestGitHubSyncDeadLettersRequiresAuthentication(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "dead-letter-auth-org")
	deadLetterID, _ := createDeadLetterFixture(t, db, orgID)
	jobStore := store.NewGitHubSyncJobStore(db)

	router := buildDeadLetterRouter(db, jobStore)

	listReq := httptest.NewRequest(http.MethodGet, "/api/github/sync/dead-letters", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusUnauthorized, listRec.Code)

	replayReq := httptest.NewRequest(http.MethodPost, "/api/github/sync/dead-letters/"+deadLetterID+"/replay", nil)
	replayRec := httptest.NewRecorder()
	router.ServeHTTP(replayRec, replayReq)
	require.Equal(t, http.StatusUnauthorized, replayRec.Code)
}
