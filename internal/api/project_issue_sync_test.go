package api

import (
	"bytes"
	"context"
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

func newProjectIssueSyncTestRouter(handler *ProjectIssueSyncHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues/import", handler.ManualImport)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/issues/status", handler.Status)
	return router
}

func issueSyncTestCtx(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}

func TestProjectIssueSyncHandlerManualImportEnqueuesJob(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-issue-sync-import-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sync Project")

	projectStore := store.NewProjectStore(db)
	repoStore := store.NewProjectRepoStore(db)
	installationStore := store.NewGitHubInstallationStore(db)
	syncJobStore := store.NewGitHubSyncJobStore(db)
	issueStore := store.NewProjectIssueStore(db)

	ctx := issueSyncTestCtx(orgID)
	_, err := installationStore.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: 901,
		AccountLogin:   "samhotchkiss",
		AccountType:    "Organization",
	})
	require.NoError(t, err)
	_, err = repoStore.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
	})
	require.NoError(t, err)

	handler := &ProjectIssueSyncHandler{
		Projects:      projectStore,
		ProjectRepos:  repoStore,
		Installations: installationStore,
		SyncJobs:      syncJobStore,
		IssueStore:    issueStore,
	}
	router := newProjectIssueSyncTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/import?org_id="+orgID, bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var response projectIssueImportResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	require.Equal(t, projectID, response.ProjectID)
	require.Equal(t, "samhotchkiss/otter-camp", response.RepositoryFullName)
	require.Equal(t, store.GitHubSyncJobTypeIssueImport, response.Job.Type)
	require.Equal(t, store.GitHubSyncJobStatusQueued, response.Job.Status)

	job, err := syncJobStore.GetLatestByProjectAndType(ctx, projectID, store.GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, response.Job.ID, job.ID)
}

func TestProjectIssueSyncHandlerStatusReturnsCountsAndMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-issue-sync-status-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sync Status Project")

	projectStore := store.NewProjectStore(db)
	repoStore := store.NewProjectRepoStore(db)
	installationStore := store.NewGitHubInstallationStore(db)
	syncJobStore := store.NewGitHubSyncJobStore(db)
	issueStore := store.NewProjectIssueStore(db)

	ctx := issueSyncTestCtx(orgID)
	_, err := installationStore.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: 902,
		AccountLogin:   "samhotchkiss",
		AccountType:    "Organization",
	})
	require.NoError(t, err)
	_, err = repoStore.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Open issue",
		Origin:    "local",
		State:     "open",
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Closed issue",
		Origin:    "github",
		State:     "closed",
	})
	require.NoError(t, err)

	syncedAt := time.Now().UTC().Add(-5 * time.Minute)
	_, err = issueStore.UpsertSyncCheckpoint(ctx, store.UpsertProjectIssueSyncCheckpointInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		Resource:           "issues",
		Cursor:             issueTestStringPtr("page=2"),
		LastSyncedAt:       &syncedAt,
	})
	require.NoError(t, err)

	job, err := syncJobStore.Enqueue(ctx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"project_id":"` + projectID + `"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)
	picked, err := syncJobStore.PickupNext(ctx, store.GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.Equal(t, job.ID, picked.ID)
	_, err = syncJobStore.MarkCompleted(ctx, picked.ID)
	require.NoError(t, err)

	handler := &ProjectIssueSyncHandler{
		Projects:      projectStore,
		ProjectRepos:  repoStore,
		Installations: installationStore,
		SyncJobs:      syncJobStore,
		IssueStore:    issueStore,
	}
	router := newProjectIssueSyncTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/issues/status?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response projectIssueSyncStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	require.Equal(t, projectID, response.ProjectID)
	require.Equal(t, "samhotchkiss/otter-camp", response.RepositoryFullName)
	require.Equal(t, 2, response.Counts.Total)
	require.Equal(t, 1, response.Counts.Open)
	require.Equal(t, 1, response.Counts.Closed)
	require.Len(t, response.Checkpoints, 1)
	require.NotNil(t, response.Job)
	require.Equal(t, store.GitHubSyncJobStatusCompleted, response.Job.Status)
	require.Nil(t, response.SyncError)
	require.NotNil(t, response.LastSyncedAt)
}

func TestProjectIssueSyncHandlerStatusSurfacesImportFailures(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-issue-sync-failure-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sync Failure Project")

	projectStore := store.NewProjectStore(db)
	repoStore := store.NewProjectRepoStore(db)
	installationStore := store.NewGitHubInstallationStore(db)
	syncJobStore := store.NewGitHubSyncJobStore(db)
	issueStore := store.NewProjectIssueStore(db)

	ctx := issueSyncTestCtx(orgID)
	_, err := installationStore.Upsert(ctx, store.UpsertGitHubInstallationInput{
		InstallationID: 903,
		AccountLogin:   "samhotchkiss",
		AccountType:    "Organization",
	})
	require.NoError(t, err)
	_, err = repoStore.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
	})
	require.NoError(t, err)

	job, err := syncJobStore.Enqueue(ctx, store.EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     store.GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"project_id":"` + projectID + `"}`),
		MaxAttempts: 1,
	})
	require.NoError(t, err)
	picked, err := syncJobStore.PickupNext(ctx, store.GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.Equal(t, job.ID, picked.ID)

	_, err = syncJobStore.RecordFailure(ctx, picked.ID, store.RecordGitHubSyncFailureInput{
		ErrorClass:   "rate_limit",
		ErrorMessage: "github api quota exceeded",
		Retryable:    false,
		OccurredAt:   time.Now().UTC(),
	})
	require.NoError(t, err)

	handler := &ProjectIssueSyncHandler{
		Projects:      projectStore,
		ProjectRepos:  repoStore,
		Installations: installationStore,
		SyncJobs:      syncJobStore,
		IssueStore:    issueStore,
	}
	router := newProjectIssueSyncTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/issues/status?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response projectIssueSyncStatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	require.NotNil(t, response.Job)
	require.Equal(t, store.GitHubSyncJobStatusDeadLetter, response.Job.Status)
	require.NotNil(t, response.SyncError)
	require.Equal(t, "github api quota exceeded", *response.SyncError)
}

func TestProjectIssueSyncHandlerRejectsUnauthorizedProjectAccess(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "project-issue-sync-iso-a")
	orgB := insertMessageTestOrganization(t, db, "project-issue-sync-iso-b")
	projectB := insertProjectTestProject(t, db, orgB, "Issue Sync Iso Project")

	projectStore := store.NewProjectStore(db)
	repoStore := store.NewProjectRepoStore(db)
	installationStore := store.NewGitHubInstallationStore(db)
	syncJobStore := store.NewGitHubSyncJobStore(db)
	issueStore := store.NewProjectIssueStore(db)

	ctxB := issueSyncTestCtx(orgB)
	_, err := installationStore.Upsert(ctxB, store.UpsertGitHubInstallationInput{
		InstallationID: 904,
		AccountLogin:   "samhotchkiss",
		AccountType:    "Organization",
	})
	require.NoError(t, err)
	_, err = repoStore.UpsertBinding(ctxB, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectB,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
	})
	require.NoError(t, err)

	handler := &ProjectIssueSyncHandler{
		Projects:      projectStore,
		ProjectRepos:  repoStore,
		Installations: installationStore,
		SyncJobs:      syncJobStore,
		IssueStore:    issueStore,
	}
	router := newProjectIssueSyncTestRouter(handler)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectB+"/issues/status?org_id="+orgA, nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, statusRec.Code)

	importReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectB+"/issues/import?org_id="+orgA, nil)
	importRec := httptest.NewRecorder()
	router.ServeHTTP(importRec, importReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, importRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectB+"/issues/status", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, missingWorkspaceRec.Code)
}

func TestProjectIssueSyncHandlerManualImportReturnsSafeErrors(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-issue-sync-safe-error-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sync Safe Error Project")

	projectStore := store.NewProjectStore(db)
	repoStore := store.NewProjectRepoStore(db)
	installationStore := store.NewGitHubInstallationStore(db)
	syncJobStore := store.NewGitHubSyncJobStore(db)
	issueStore := store.NewProjectIssueStore(db)

	handler := &ProjectIssueSyncHandler{
		Projects:      projectStore,
		ProjectRepos:  repoStore,
		Installations: installationStore,
		SyncJobs:      syncJobStore,
		IssueStore:    issueStore,
	}
	router := newProjectIssueSyncTestRouter(handler)

	// Missing repo mapping.
	noRepoReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/import?org_id="+orgID, nil)
	noRepoRec := httptest.NewRecorder()
	router.ServeHTTP(noRepoRec, noRepoReq)
	require.Equal(t, http.StatusBadRequest, noRepoRec.Code)
	require.Contains(t, noRepoRec.Body.String(), "project repository not mapped")

	// Repo mapping exists but installation is still missing.
	ctx := issueSyncTestCtx(orgID)
	_, err := repoStore.UpsertBinding(ctx, store.UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           store.RepoSyncModeSync,
		AutoSync:           true,
	})
	require.NoError(t, err)

	noInstallReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/import?org_id="+orgID, nil)
	noInstallRec := httptest.NewRecorder()
	router.ServeHTTP(noInstallRec, noInstallReq)
	require.Equal(t, http.StatusBadRequest, noInstallRec.Code)
	require.Contains(t, noInstallRec.Body.String(), "github installation not connected")
}
