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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	workspaceCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err = handler.ProjectRepos.SetConflictState(
		workspaceCtx,
		projectID,
		store.RepoConflictNeedsDecision,
		json.RawMessage(`{"branch":"main","local_sha":"sha-local","remote_sha":"sha-remote"}`),
	)
	require.NoError(t, err)

	branchesReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/repo/branches", nil)
	branchesReq = addRouteParam(branchesReq, "id", projectID)
	branchesReq = withWorkspaceContext(branchesReq, orgID)
	branchesRec := httptest.NewRecorder()
	handler.GetProjectBranches(branchesRec, branchesReq)
	require.Equal(t, http.StatusOK, branchesRec.Code)

	var branchesResp githubProjectBranchesResponse
	require.NoError(t, json.NewDecoder(branchesRec.Body).Decode(&branchesResp))
	require.Equal(t, "main", branchesResp.DefaultBranch)
	require.Equal(t, store.RepoConflictNeedsDecision, branchesResp.ConflictState)
	require.JSONEq(t, `{"branch":"main","local_sha":"sha-local","remote_sha":"sha-remote"}`, string(branchesResp.ConflictDetails))
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

func TestResolveProjectConflictRejectsInvalidAction(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-conflict-invalid-org")
	projectID := insertProjectTestProject(t, db, orgID, "Conflict Project")
	handler := NewGitHubIntegrationHandler(db)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/repo/conflicts/resolve",
		bytes.NewReader([]byte(`{"action":"do_something_else"}`)),
	)
	req = addRouteParam(req, "id", projectID)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.ResolveProjectConflict(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResolveProjectConflictKeepGitHubResetsLocalBranchAndLogsActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-conflict-keep-gh-org")
	projectID := insertProjectTestProject(t, db, orgID, "Conflict Keep GitHub Project")
	handler := NewGitHubIntegrationHandler(db)

	fixture := newConflictRepoFixture(t)
	localPath := fixture.LocalPath
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		LocalRepoPath:      &localPath,
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNeedsDecision,
		ConflictDetails:    json.RawMessage(`{"branch":"main","local_sha":"` + fixture.LocalSHA + `","remote_sha":"` + fixture.RemoteSHA + `"}`),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/repo/conflicts/resolve",
		bytes.NewReader([]byte(`{"action":"keep_github"}`)),
	)
	req = addRouteParam(req, "id", projectID)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.ResolveProjectConflict(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	localHead := runGitTestOutput(t, fixture.LocalPath, "rev-parse", "HEAD")
	require.Equal(t, fixture.RemoteSHA, localHead)

	binding, err := handler.ProjectRepos.GetBinding(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, store.RepoConflictResolved, binding.ConflictState)
	require.Contains(t, string(binding.ConflictDetails), `"resolution":"keep_github"`)

	var activityCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.repo_conflict_resolved'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 1, activityCount)
}

func TestResolveProjectConflictKeepOtterCampPreservesLocalBranchAndMarksReadyToPublish(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-conflict-keep-local-org")
	projectID := insertProjectTestProject(t, db, orgID, "Conflict Keep Local Project")
	handler := NewGitHubIntegrationHandler(db)

	fixture := newConflictRepoFixture(t)
	localPath := fixture.LocalPath
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		LocalRepoPath:      &localPath,
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNeedsDecision,
		ConflictDetails:    json.RawMessage(`{"branch":"main","local_sha":"` + fixture.LocalSHA + `","remote_sha":"` + fixture.RemoteSHA + `"}`),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/repo/conflicts/resolve",
		bytes.NewReader([]byte(`{"action":"keep_ottercamp"}`)),
	)
	req = addRouteParam(req, "id", projectID)
	req = withWorkspaceContext(req, orgID)
	rec := httptest.NewRecorder()
	handler.ResolveProjectConflict(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	localHead := runGitTestOutput(t, fixture.LocalPath, "rev-parse", "HEAD")
	require.Equal(t, fixture.LocalSHA, localHead)

	binding, err := handler.ProjectRepos.GetBinding(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, store.RepoConflictResolved, binding.ConflictState)
	require.Contains(t, string(binding.ConflictDetails), `"resolution":"keep_ottercamp"`)
	require.Contains(t, string(binding.ConflictDetails), `"ready_to_publish":true`)

	var metadataRaw []byte
	err = db.QueryRow(
		`SELECT metadata
			FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.repo_conflict_resolved'
			ORDER BY created_at DESC
			LIMIT 1`,
		orgID,
		projectID,
	).Scan(&metadataRaw)
	require.NoError(t, err)

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(metadataRaw, &metadata))
	require.Equal(t, "keep_ottercamp", metadata["resolution"])
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

func TestGitHubWebhookIssuesOpenedEditedClosedUpsert(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-issues-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook Issues Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 4322)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	openedPayload := []byte(`{
		"action":"opened",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4322},
		"issue":{
			"number":44,
			"title":"Issue from webhook",
			"body":"initial body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/44"
		}
	}`)
	sendGitHubWebhook(t, handler, "issues", "delivery-issues-opened", openedPayload)

	editedPayload := []byte(`{
		"action":"edited",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4322},
		"issue":{
			"number":44,
			"title":"Issue from webhook (edited)",
			"body":"edited body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/44"
		}
	}`)
	sendGitHubWebhook(t, handler, "issues", "delivery-issues-edited", editedPayload)

	closedPayload := []byte(`{
		"action":"closed",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4322},
		"issue":{
			"number":44,
			"title":"Issue from webhook (edited)",
			"body":"edited body",
			"state":"closed",
			"closed_at":"2026-02-06T12:10:00Z",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/44"
		}
	}`)
	sendGitHubWebhook(t, handler, "issues", "delivery-issues-closed", closedPayload)

	issue, link := loadIssueByGitHubNumber(t, db, orgID, projectID, 44)
	require.Equal(t, "Issue from webhook (edited)", issue.Title)
	require.Equal(t, "closed", issue.State)
	require.NotNil(t, issue.ClosedAt)
	require.NotNil(t, link.GitHubURL)
	require.Contains(t, *link.GitHubURL, "/issues/44")
}

func TestGitHubWebhookPullRequestLifecycleUpsert(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-pr-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook PR Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 4323)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	openedPayload := []byte(`{
		"action":"opened",
		"number":88,
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4323},
		"pull_request":{
			"number":88,
			"title":"PR from webhook",
			"body":"initial",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/pull/88",
			"merged":false
		}
	}`)
	sendGitHubWebhook(t, handler, "pull_request", "delivery-pr-opened", openedPayload)

	syncPayload := []byte(`{
		"action":"synchronize",
		"number":88,
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4323},
		"pull_request":{
			"number":88,
			"title":"PR from webhook (sync)",
			"body":"sync body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/pull/88",
			"merged":false
		}
	}`)
	sendGitHubWebhook(t, handler, "pull_request", "delivery-pr-sync", syncPayload)

	closedPayload := []byte(`{
		"action":"closed",
		"number":88,
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4323},
		"pull_request":{
			"number":88,
			"title":"PR from webhook (sync)",
			"body":"sync body",
			"state":"closed",
			"closed_at":"2026-02-06T12:20:00Z",
			"html_url":"https://github.com/samhotchkiss/otter-camp/pull/88",
			"merged":true
		}
	}`)
	sendGitHubWebhook(t, handler, "pull_request", "delivery-pr-closed", closedPayload)

	issue, link := loadIssueByGitHubNumber(t, db, orgID, projectID, 88)
	require.Equal(t, "PR from webhook (sync)", issue.Title)
	require.Equal(t, "closed", issue.State)
	require.NotNil(t, link.GitHubURL)
	require.Contains(t, *link.GitHubURL, "/pull/88")
}

func TestGitHubWebhookIssueCommentCreatesActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-comment-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook Comment Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 4324)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	payload := []byte(`{
		"action":"created",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4324},
		"issue":{
			"number":91,
			"title":"Commented issue",
			"body":"issue body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/91"
		},
		"comment":{
			"body":"Looks good",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/91#issuecomment-1",
			"user":{"login":"octocat"}
		}
	}`)
	sendGitHubWebhook(t, handler, "issue_comment", "delivery-comment-created", payload)

	_, _ = loadIssueByGitHubNumber(t, db, orgID, projectID, 91)

	var activityCount int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.issue_comment.created'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 1, activityCount)
}

func TestGitHubWebhookDuplicateDeliveryDoesNotDuplicateIssueWrites(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-dup-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook Duplicate Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 4325)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	payload := []byte(`{
		"action":"opened",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4325},
		"issue":{
			"number":77,
			"title":"Deduped issue",
			"body":"body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/77"
		}
	}`)

	sendGitHubWebhook(t, handler, "issues", "delivery-dup-77", payload)
	sendGitHubWebhook(t, handler, "issues", "delivery-dup-77", payload)

	issues := listProjectIssuesForTest(t, db, orgID, projectID)
	require.Len(t, issues, 1)

	var activityCount int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.issue.opened'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 1, activityCount)
}

func TestGitHubWebhookPushIngestsCommitsAndUpdatesBranchCheckpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "github-webhook-push-commits-org")
	projectID := insertProjectTestProject(t, db, orgID, "Webhook Push Commits Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 4326)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	payload := []byte(`{
		"ref":"refs/heads/main",
		"before":"0000000000000000000000000000000000000000",
		"after":"9999999999999999999999999999999999999999",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":4326},
		"commits":[
			{
				"id":"1111111111111111111111111111111111111111",
				"message":"First commit subject\n\nFirst commit body",
				"timestamp":"2026-02-06T11:00:00Z",
				"url":"https://github.com/samhotchkiss/otter-camp/commit/1111111111111111111111111111111111111111",
				"author":{"name":"Sam","email":"sam@example.com"}
			},
			{
				"id":"2222222222222222222222222222222222222222",
				"message":"Second commit subject",
				"timestamp":"2026-02-06T11:05:00Z",
				"url":"https://github.com/samhotchkiss/otter-camp/commit/2222222222222222222222222222222222222222",
				"author":{"name":"Stone","email":"stone@example.com"}
			}
		]
	}`)
	sendGitHubWebhook(t, handler, "push", "delivery-push-commits-1", payload)

	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	commits, err := handler.Commits.ListCommits(ctx, store.ProjectCommitFilter{ProjectID: projectID, Limit: 20})
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, "2222222222222222222222222222222222222222", commits[0].SHA)
	require.Equal(t, "Second commit subject", commits[0].Subject)
	require.Equal(t, "1111111111111111111111111111111111111111", commits[1].SHA)
	require.NotNil(t, commits[1].Body)
	require.Equal(t, "First commit body", *commits[1].Body)

	branches, err := handler.ProjectRepos.ListActiveBranches(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, branches, 1)
	require.Equal(t, "main", branches[0].BranchName)
	require.NotNil(t, branches[0].LastSyncedSHA)
	require.Equal(t, "9999999999999999999999999999999999999999", *branches[0].LastSyncedSHA)

	var activityCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.commit.ingested'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 2, activityCount)

	sendGitHubWebhook(t, handler, "push", "delivery-push-commits-2", payload)
	commits, err = handler.Commits.ListCommits(ctx, store.ProjectCommitFilter{ProjectID: projectID, Limit: 20})
	require.NoError(t, err)
	require.Len(t, commits, 2)

	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log
			WHERE org_id = $1
			  AND project_id = $2
			  AND action = 'github.commit.ingested'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 2, activityCount)
}

func setupWebhookRepoBinding(
	t *testing.T,
	handler *GitHubIntegrationHandler,
	orgID, projectID, repositoryFullName string,
	installationID int64,
) {
	t.Helper()
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	_, err := handler.Installations.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: installationID,
		AccountLogin:   "the-trawl",
		AccountType:    "Organization",
	})
	require.NoError(t, err)

	_, err = handler.ProjectRepos.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: repositoryFullName,
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      store.RepoConflictNone,
	})
	require.NoError(t, err)
}

func sendGitHubWebhook(
	t *testing.T,
	handler *GitHubIntegrationHandler,
	eventType, deliveryID string,
	payload []byte,
) {
	t.Helper()
	signature := signGitHubPayload("webhook-secret", payload)

	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(payload))
	req.Header.Set(githubSignatureHeader, signature)
	req.Header.Set(githubEventHeader, eventType)
	req.Header.Set(githubDeliveryHeader, deliveryID)
	rec := httptest.NewRecorder()
	handler.GitHubWebhook(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
}

func listProjectIssuesForTest(
	t *testing.T,
	db *sql.DB,
	orgID, projectID string,
) []store.ProjectIssue {
	t.Helper()
	issueStore := store.NewProjectIssueStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	issues, err := issueStore.ListIssues(ctx, store.ProjectIssueFilter{
		ProjectID: projectID,
		Limit:     200,
	})
	require.NoError(t, err)
	return issues
}

func loadIssueByGitHubNumber(
	t *testing.T,
	db *sql.DB,
	orgID, projectID string,
	githubNumber int64,
) (store.ProjectIssue, store.ProjectIssueGitHubLink) {
	t.Helper()
	issueStore := store.NewProjectIssueStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)

	issues := listProjectIssuesForTest(t, db, orgID, projectID)
	issueIDs := make([]string, 0, len(issues))
	for _, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
	}
	links, err := issueStore.ListGitHubLinksByIssueIDs(ctx, issueIDs)
	require.NoError(t, err)
	for _, issue := range issues {
		link, ok := links[issue.ID]
		if !ok {
			continue
		}
		if link.GitHubNumber == githubNumber {
			return issue, link
		}
	}
	t.Fatalf("github issue number %d not found", githubNumber)
	return store.ProjectIssue{}, store.ProjectIssueGitHubLink{}
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

type conflictRepoFixture struct {
	RemotePath string
	WorkPath   string
	LocalPath  string
	LocalSHA   string
	RemoteSHA  string
}

func newConflictRepoFixture(t *testing.T) conflictRepoFixture {
	t.Helper()

	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	workPath := filepath.Join(root, "work")
	localPath := filepath.Join(root, "local")

	runGitTest(t, "", "init", "--bare", remotePath)
	runGitTest(t, "", "init", "--initial-branch=main", workPath)
	runGitTest(t, workPath, "config", "user.email", "fixture@example.com")
	runGitTest(t, workPath, "config", "user.name", "Fixture User")
	runGitTest(t, workPath, "remote", "add", "origin", remotePath)

	require.NoError(t, os.WriteFile(filepath.Join(workPath, "README.md"), []byte("initial\n"), 0o644))
	runGitTest(t, workPath, "add", "README.md")
	runGitTest(t, workPath, "commit", "-m", "initial commit")
	runGitTest(t, workPath, "push", "-u", "origin", "main")

	runGitTest(t, "", "clone", remotePath, localPath)
	runGitTest(t, localPath, "config", "user.email", "local@example.com")
	runGitTest(t, localPath, "config", "user.name", "Local User")

	require.NoError(t, os.WriteFile(filepath.Join(localPath, "README.md"), []byte("local\n"), 0o644))
	runGitTest(t, localPath, "add", "README.md")
	runGitTest(t, localPath, "commit", "-m", "local commit")
	localSHA := runGitTestOutput(t, localPath, "rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(filepath.Join(workPath, "README.md"), []byte("remote\n"), 0o644))
	runGitTest(t, workPath, "add", "README.md")
	runGitTest(t, workPath, "commit", "-m", "remote commit")
	runGitTest(t, workPath, "push", "origin", "main")
	remoteSHA := runGitTestOutput(t, workPath, "rev-parse", "HEAD")

	return conflictRepoFixture{
		RemotePath: remotePath,
		WorkPath:   workPath,
		LocalPath:  localPath,
		LocalSHA:   localSHA,
		RemoteSHA:  remoteSHA,
	}
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runGitTestOutput(t, dir, args...)
}

func runGitTestOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(output))
	return strings.TrimSpace(string(output))
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
