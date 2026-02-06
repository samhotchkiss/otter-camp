package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func withWorkspaceContext(req *http.Request, orgID string) *http.Request {
	ctx := context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID)
	return req.WithContext(ctx)
}

func signGitHubPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func countSyncJobs(t *testing.T, db *sql.DB, orgID, jobType string) int {
	t.Helper()
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM github_sync_jobs WHERE org_id = $1 AND job_type = $2`,
		orgID,
		jobType,
	).Scan(&count)
	require.NoError(t, err)
	return count
}

func TestGitHubIntegrationConnectStartAndCallback(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-connect-org")
	handler := NewGitHubIntegrationHandler(db)

	t.Setenv("GITHUB_APP_SLUG", "otter-camp")

	startReq := httptest.NewRequest(http.MethodPost, "/api/github/connect/start", nil)
	startReq = withWorkspaceContext(startReq, orgID)
	startRec := httptest.NewRecorder()
	handler.ConnectStart(startRec, startReq)
	require.Equal(t, http.StatusOK, startRec.Code)

	var startResp githubConnectStartResponse
	require.NoError(t, json.NewDecoder(startRec.Body).Decode(&startResp))
	require.NotEmpty(t, startResp.State)
	require.Contains(t, startResp.InstallURL, "state=")

	callbackReq := httptest.NewRequest(
		http.MethodGet,
		"/api/github/connect/callback?state="+startResp.State+"&installation_id=12345&account_login=the-trawl&account_type=Organization",
		nil,
	)
	callbackRec := httptest.NewRecorder()
	handler.ConnectCallback(callbackRec, callbackReq)
	require.Equal(t, http.StatusOK, callbackRec.Code)

	var callbackResp githubConnectCallbackResponse
	require.NoError(t, json.NewDecoder(callbackRec.Body).Decode(&callbackResp))
	require.True(t, callbackResp.Connected)
	require.Equal(t, orgID, callbackResp.OrgID)
	require.Equal(t, int64(12345), callbackResp.InstallationID)

	installationStore := store.NewGitHubInstallationStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	installation, err := installationStore.GetByOrg(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(12345), installation.InstallationID)
	require.Equal(t, "the-trawl", installation.AccountLogin)
}

func TestGitHubIntegrationSettingsBranchesAndManualSync(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-settings-org")
	projectID := insertProjectTestProject(t, db, orgID, "Repo Project")

	handler := NewGitHubIntegrationHandler(db)

	updatePayload := githubSettingsUpdateRequest{
		Enabled:        ghBoolPtr(true),
		RepoFullName:   ghStringPtr("samhotchkiss/otter-camp"),
		DefaultBranch:  ghStringPtr("main"),
		SyncMode:       ghStringPtr(store.RepoSyncModeSync),
		AutoSync:       ghBoolPtr(true),
		ActiveBranches: []string{"main", "feature/writing"},
	}
	updateBody, err := json.Marshal(updatePayload)
	require.NoError(t, err)

	updateReq := httptest.NewRequest(http.MethodPut, "/api/github/integration/settings/"+projectID, bytes.NewReader(updateBody))
	updateReq = addRouteParam(updateReq, "projectID", projectID)
	updateReq = withWorkspaceContext(updateReq, orgID)
	updateRec := httptest.NewRecorder()
	handler.UpdateSettings(updateRec, updateReq)
	require.Equal(t, http.StatusOK, updateRec.Code)

	branchesReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/repo/branches", nil)
	branchesReq = addRouteParam(branchesReq, "id", projectID)
	branchesReq = withWorkspaceContext(branchesReq, orgID)
	branchesRec := httptest.NewRecorder()
	handler.GetProjectBranches(branchesRec, branchesReq)
	require.Equal(t, http.StatusOK, branchesRec.Code)

	var branchesResp githubProjectBranchesResponse
	require.NoError(t, json.NewDecoder(branchesRec.Body).Decode(&branchesResp))
	require.Equal(t, "main", branchesResp.DefaultBranch)
	require.Len(t, branchesResp.ActiveBranches, 2)

	syncReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/repo/sync", nil)
	syncReq = addRouteParam(syncReq, "id", projectID)
	syncReq = withWorkspaceContext(syncReq, orgID)
	syncRec := httptest.NewRecorder()
	handler.ManualRepoSync(syncRec, syncReq)
	require.Equal(t, http.StatusAccepted, syncRec.Code)

	var syncResp githubManualSyncResponse
	require.NoError(t, json.NewDecoder(syncRec.Body).Decode(&syncResp))
	require.NotEmpty(t, syncResp.JobID)
	require.Equal(t, store.GitHubSyncJobStatusQueued, syncResp.Status)
	require.Equal(t, "samhotchkiss/otter-camp", syncResp.RepositoryFullName)

	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeRepoSync))
}

func TestGitHubIntegrationListSettingsIncludesWorkflowModePolicy(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-settings-mode-org")
	projectID := insertProjectTestProject(t, db, orgID, "Writing Project")
	handler := NewGitHubIntegrationHandler(db)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModePush,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/github/integration/settings", nil)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.ListSettings(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload githubSettingsListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.NotEmpty(t, payload.Projects)

	var found *githubProjectSettingView
	for i := range payload.Projects {
		if payload.Projects[i].ProjectID == projectID {
			found = &payload.Projects[i]
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, reviewWorkflowModeLocalIssuePR, found.WorkflowMode)
	require.False(t, found.GitHubPREnabled)
}

func TestGitHubWebhookEnqueueAndReplayProtection(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook Project")

	handler := NewGitHubIntegrationHandler(db)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.Installations.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: 4321,
		AccountLogin:   "the-trawl",
		AccountType:    "Organization",
	})
	require.NoError(t, err)

	_, err = handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)
	_, err = handler.ProjectRepos.SetActiveBranches(ctx, projectID, []string{"main"})
	require.NoError(t, err)

	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	payload := []byte(`{
		"ref":"refs/heads/main",
		"before":"abc123",
		"after":"def456",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4321}
	}`)
	signature := signGitHubPayload("webhook-secret", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(payload))
	req.Header.Set(githubSignatureHeader, signature)
	req.Header.Set(githubEventHeader, "push")
	req.Header.Set(githubDeliveryHeader, "delivery-1")
	rec := httptest.NewRecorder()
	handler.GitHubWebhook(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeWebhook))
	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeRepoSync))

	dupReq := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(payload))
	dupReq.Header.Set(githubSignatureHeader, signature)
	dupReq.Header.Set(githubEventHeader, "push")
	dupReq.Header.Set(githubDeliveryHeader, "delivery-1")
	dupRec := httptest.NewRecorder()
	handler.GitHubWebhook(dupRec, dupReq)
	require.Equal(t, http.StatusAccepted, dupRec.Code)

	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeWebhook))
	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeRepoSync))
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := NewGitHubIntegrationHandler(db)

	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")
	payload := []byte(`{"repository":{"full_name":"samhotchkiss/otter-camp"}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(payload))
	req.Header.Set(githubSignatureHeader, "sha256=deadbeef")
	req.Header.Set(githubEventHeader, "push")
	req.Header.Set(githubDeliveryHeader, "delivery-invalid")
	rec := httptest.NewRecorder()
	handler.GitHubWebhook(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func ghStringPtr(value string) *string {
	return &value
}

func ghBoolPtr(value bool) *bool {
	return &value
}

func TestGitHubConnectStateExpires(t *testing.T) {
	store := newGitHubConnectStateStore(time.Minute)
	base := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }

	state, _, err := store.Create("123e4567-e89b-12d3-a456-426614174000")
	require.NoError(t, err)
	require.NotEmpty(t, state)

	store.now = func() time.Time { return base.Add(2 * time.Minute) }
	orgID, ok := store.Consume(state)
	require.False(t, ok)
	require.Empty(t, orgID)
}

func TestGitHubDeliveryStoreReplayWindow(t *testing.T) {
	deliveryStore := newGitHubDeliveryStore(time.Minute)
	base := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	deliveryStore.now = func() time.Time { return base }

	require.True(t, deliveryStore.MarkIfNew("delivery-abc"))
	require.False(t, deliveryStore.MarkIfNew("delivery-abc"))

	deliveryStore.now = func() time.Time { return base.Add(2 * time.Minute) }
	require.True(t, deliveryStore.MarkIfNew("delivery-abc"))
}

func TestGitHubConnectStartRequiresWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := NewGitHubIntegrationHandler(db)
	t.Setenv("GITHUB_APP_SLUG", "otter-camp")

	req := httptest.NewRequest(http.MethodPost, "/api/github/connect/start", nil)
	rec := httptest.NewRecorder()
	handler.ConnectStart(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestManualSyncLogsActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "manual-sync-activity-org")
	projectID := insertProjectTestProject(t, db, orgID, "Manual Sync Activity Project")
	handler := NewGitHubIntegrationHandler(db)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/repo/sync", nil)
	req = addRouteParam(req, "id", projectID)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.ManualRepoSync(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'github.repo_sync_requested'`,
		orgID,
		projectID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestGitHubWebhookIgnoredEventType(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := NewGitHubIntegrationHandler(db)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	payload := []byte(`{"repository":{"full_name":"samhotchkiss/otter-camp"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(payload))
	req.Header.Set(githubSignatureHeader, signGitHubPayload("webhook-secret", payload))
	req.Header.Set(githubEventHeader, "ping")
	req.Header.Set(githubDeliveryHeader, "delivery-ping")
	rec := httptest.NewRecorder()
	handler.GitHubWebhook(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
}

func TestIntegrationStatusDisconnectedByDefault(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "integration-status-org")
	handler := NewGitHubIntegrationHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/api/github/integration/status", nil)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.IntegrationStatus(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload githubIntegrationStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.Connected)
}

func TestGitHubIntegrationDisconnect(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "integration-disconnect-org")
	handler := NewGitHubIntegrationHandler(db)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.Installations.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: 777,
		AccountLogin:   "the-trawl",
		AccountType:    "Organization",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodDelete, "/api/github/integration/connection", nil)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.Disconnect(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	_, err = handler.Installations.GetByOrg(ctx)
	require.Error(t, err)
}

func TestUpdateProjectBranchesRejectsInvalidName(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "invalid-branch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Invalid Branch Project")
	handler := NewGitHubIntegrationHandler(db)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)

	body := []byte(`{"branches":["../../main"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/repo/branches", bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.UpdateProjectBranches(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestParsePositiveInt64(t *testing.T) {
	value, err := parsePositiveInt64("42")
	require.NoError(t, err)
	require.Equal(t, int64(42), value)

	_, err = parsePositiveInt64("0")
	require.Error(t, err)
}

func TestVerifyGitHubSignature(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	signature := signGitHubPayload("secret", payload)
	require.True(t, verifyGitHubSignature("secret", payload, signature))
	require.False(t, verifyGitHubSignature("secret", payload, "sha256=deadbeef"))
}

func TestLogGitHubActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-activity-org")
	projectID := insertProjectTestProject(t, db, orgID, "Activity Project")

	err := logGitHubActivity(context.Background(), db, orgID, &projectID, "github.test", map[string]any{
		"value": "ok",
	})
	require.NoError(t, err)

	var count int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'github.test'`,
		orgID,
		projectID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestWebhookDeliveryDedupUsesStoreWindow(t *testing.T) {
	store := newGitHubDeliveryStore(10 * time.Second)
	base := time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return base }
	require.True(t, store.MarkIfNew("delivery-1"))
	require.False(t, store.MarkIfNew("delivery-1"))
	store.now = func() time.Time { return base.Add(11 * time.Second) }
	require.True(t, store.MarkIfNew("delivery-1"))
}
