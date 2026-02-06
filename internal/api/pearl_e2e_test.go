package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/githubsync"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type pearlStaticBranchHeadClient struct {
	heads map[string]string
}

func (c pearlStaticBranchHeadClient) GetBranchHeadSHA(
	_ context.Context,
	repositoryFullName string,
	branch string,
) (string, error) {
	key := repositoryFullName + "#" + branch
	sha, ok := c.heads[key]
	if !ok {
		return "", fmt.Errorf("missing head for %s", key)
	}
	return sha, nil
}

func TestPearlE2EWebhookPushPollFallbackAndCommitAPIs(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pearl-e2e-webhook-poll-org")
	projectID := insertProjectTestProject(t, db, orgID, "Pearl E2E Webhook Poll Project")
	handler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, handler, orgID, projectID, "samhotchkiss/otter-camp", 5501)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	pushPayload := []byte(`{
		"ref":"refs/heads/main",
		"before":"1111111111111111111111111111111111111111",
		"after":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":5501},
		"commits":[
			{
				"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"message":"Pearl sync commit",
				"timestamp":"2026-02-06T12:00:00Z",
				"url":"https://github.com/samhotchkiss/otter-camp/commit/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				"author":{"name":"Pearl Agent","email":"pearl@example.com"},
				"added":["posts/2026-02-06-pearl-sync.md"],
				"removed":[],
				"modified":[]
			}
		]
	}`)
	sendGitHubWebhook(t, handler, "push", "delivery-pearl-e2e-push-1", pushPayload)
	require.Equal(t, 1, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeRepoSync))

	commitsHandler := &ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
	}
	commitsRouter := newProjectCommitsTestRouter(commitsHandler)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		nil,
	)
	listRec := httptest.NewRecorder()
	commitsRouter.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResponse projectCommitListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResponse))
	require.Len(t, listResponse.Items, 1)
	require.Equal(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", listResponse.Items[0].SHA)

	diffReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/commits/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/diff?org_id="+orgID,
		nil,
	)
	diffRec := httptest.NewRecorder()
	commitsRouter.ServeHTTP(diffRec, diffReq)
	require.Equal(t, http.StatusOK, diffRec.Code)

	var diffResponse projectCommitDiffResponse
	require.NoError(t, json.NewDecoder(diffRec.Body).Decode(&diffResponse))
	require.Equal(t, 1, diffResponse.Total)
	require.Equal(t, "posts/2026-02-06-pearl-sync.md", diffResponse.Files[0].Path)
	require.Equal(t, "added", diffResponse.Files[0].ChangeType)

	poller := githubsync.NewRepoDriftPoller(
		store.NewProjectRepoStore(db),
		store.NewGitHubSyncJobStore(db),
		pearlStaticBranchHeadClient{
			heads: map[string]string{
				"samhotchkiss/otter-camp#main": "cccccccccccccccccccccccccccccccccccccccc",
			},
		},
		time.Hour,
	)
	result, err := poller.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, result.JobsEnqueued)
	require.Equal(t, 2, countSyncJobs(t, db, orgID, store.GitHubSyncJobTypeRepoSync))
}

func TestPearlE2EManualIssueImportAndWebhookUpdateRemainConsistent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pearl-e2e-issue-consistency-org")
	projectID := insertProjectTestProject(t, db, orgID, "Pearl E2E Issue Consistency Project")
	githubHandler := NewGitHubIntegrationHandler(db)
	setupWebhookRepoBinding(t, githubHandler, orgID, projectID, "samhotchkiss/otter-camp", 5502)
	t.Setenv("GITHUB_WEBHOOK_SECRET", "webhook-secret")

	issueSyncHandler := &ProjectIssueSyncHandler{
		Projects:      store.NewProjectStore(db),
		ProjectRepos:  store.NewProjectRepoStore(db),
		Installations: store.NewGitHubInstallationStore(db),
		SyncJobs:      store.NewGitHubSyncJobStore(db),
		IssueStore:    store.NewProjectIssueStore(db),
	}
	issueSyncRouter := newProjectIssueSyncTestRouter(issueSyncHandler)

	importReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/import?org_id="+orgID,
		bytes.NewReader([]byte(`{}`)),
	)
	importRec := httptest.NewRecorder()
	issueSyncRouter.ServeHTTP(importRec, importReq)
	require.Equal(t, http.StatusAccepted, importRec.Code)

	var importResponse projectIssueImportResponse
	require.NoError(t, json.NewDecoder(importRec.Body).Decode(&importResponse))
	require.Equal(t, store.GitHubSyncJobTypeIssueImport, importResponse.Job.Type)

	issueStore := store.NewProjectIssueStore(db)
	workspaceCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	importedBody := "Imported by manual issue sync worker."
	importedURL := "https://github.com/samhotchkiss/otter-camp/issues/88"
	imported, _, err := issueStore.UpsertIssueFromGitHub(workspaceCtx, store.UpsertProjectIssueFromGitHubInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       88,
		Title:              "Imported title",
		Body:               &importedBody,
		State:              "open",
		GitHubURL:          &importedURL,
	})
	require.NoError(t, err)

	issueWebhookPayload := []byte(`{
		"action":"edited",
		"repository":{"full_name":"samhotchkiss/otter-camp"},
		"installation":{"id":5502},
		"issue":{
			"number":88,
			"title":"Edited by webhook",
			"body":"Webhook-updated body",
			"state":"open",
			"html_url":"https://github.com/samhotchkiss/otter-camp/issues/88"
		}
	}`)
	sendGitHubWebhook(t, githubHandler, "issues", "delivery-pearl-e2e-issue-88", issueWebhookPayload)

	issues := listProjectIssuesForTest(t, db, orgID, projectID)
	require.Len(t, issues, 1)

	updatedIssue, link := loadIssueByGitHubNumber(t, db, orgID, projectID, 88)
	require.Equal(t, imported.ID, updatedIssue.ID)
	require.Equal(t, "Edited by webhook", updatedIssue.Title)
	require.NotNil(t, updatedIssue.Body)
	require.Equal(t, "Webhook-updated body", *updatedIssue.Body)
	require.Equal(t, "open", link.GitHubState)
}
