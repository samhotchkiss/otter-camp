package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestIssuesHandlerReviewChangesUsesCheckpointRange(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-changes-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Changes")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Review issue",
		Origin:       "local",
		DocumentPath: issueReviewStringPtr("/posts/2026-02-06-review.md"),
	})
	require.NoError(t, err)

	commitRouter := newProjectCommitsTestRouter(&ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	})

	firstSHA := createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-review.md",
		"content":        "# Draft\n\nalpha",
		"commit_subject": "First review draft",
		"commit_body":    "Adds the initial reviewable post draft with baseline outline and context.",
	})
	secondSHA := createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-review.md",
		"content":        "# Draft\n\nbeta",
		"commit_subject": "Second review draft",
		"commit_body":    "Updates the review document with iteration two changes and clearer structure.",
	})
	require.NotEqual(t, firstSHA, secondSHA)

	_, err = issueStore.UpsertReviewCheckpoint(testCtxWithWorkspace(orgID), issue.ID, secondSHA)
	require.NoError(t, err)

	thirdSHA := createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-review.md",
		"content":        "# Draft\n\nthird",
		"commit_subject": "Third review draft",
		"commit_body":    "Captures post-review updates to address inline feedback and tighten language.",
	})

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/issues/"+issue.ID+"/review/changes?org_id="+orgID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload issueReviewChangesResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, issue.ID, payload.IssueID)
	require.Equal(t, secondSHA, payload.BaseSHA)
	require.Equal(t, thirdSHA, payload.HeadSHA)
	require.False(t, payload.FallbackToFirstCommit)
	require.Len(t, payload.Files, 1)
	require.Equal(t, "/posts/2026-02-06-review.md", payload.Files[0].Path)
	require.NotNil(t, payload.Files[0].Patch)
	require.Contains(t, *payload.Files[0].Patch, "+third")
}

func TestIssuesHandlerReviewHistoryAndVersion(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-history-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review History")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "History issue",
		Origin:       "local",
		DocumentPath: issueReviewStringPtr("/posts/2026-02-06-history.md"),
	})
	require.NoError(t, err)

	commitRouter := newProjectCommitsTestRouter(&ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	})

	firstSHA := createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-history.md",
		"content":        "# Title\n\nBody {>>AB: old comment<<}",
		"commit_subject": "History v1",
		"commit_body":    "Introduces the first version containing CriticMarkup for review context.",
	})
	secondSHA := createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-history.md",
		"content":        "# Title\n\nBody {>>AB: new comment<<}",
		"commit_subject": "History v2",
		"commit_body":    "Adds updated CriticMarkup comments while preserving markdown structure.",
	})
	_, err = issueStore.UpsertReviewCheckpoint(testCtxWithWorkspace(orgID), issue.ID, firstSHA)
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newIssueTestRouter(handler)

	historyReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues/"+issue.ID+"/review/history?org_id="+orgID,
		nil,
	)
	historyRec := httptest.NewRecorder()
	router.ServeHTTP(historyRec, historyReq)
	require.Equal(t, http.StatusOK, historyRec.Code)

	var history issueReviewHistoryResponse
	require.NoError(t, json.NewDecoder(historyRec.Body).Decode(&history))
	require.Equal(t, issue.ID, history.IssueID)
	require.Len(t, history.Items, 2)
	require.Equal(t, secondSHA, history.Items[0].SHA)
	require.Equal(t, firstSHA, history.Items[1].SHA)
	require.True(t, history.Items[1].IsReviewCheckpoint)

	versionReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues/"+issue.ID+"/review/history/"+firstSHA+"?org_id="+orgID,
		nil,
	)
	versionRec := httptest.NewRecorder()
	router.ServeHTTP(versionRec, versionReq)
	require.Equal(t, http.StatusOK, versionRec.Code)

	var version issueReviewVersionResponse
	require.NoError(t, json.NewDecoder(versionRec.Body).Decode(&version))
	require.Equal(t, firstSHA, version.SHA)
	require.True(t, version.ReadOnly)
	require.Contains(t, version.Content, "{>>AB: old comment<<}")
}

func TestResolveReviewDiffBaseSHAFallback(t *testing.T) {
	commits := []store.ProjectCommit{
		{SHA: "head"},
		{SHA: "middle"},
		{SHA: "first"},
	}

	base, fallback := resolveReviewDiffBaseSHA(commits, nil)
	require.True(t, fallback)
	require.Equal(t, "first", base)

	base, fallback = resolveReviewDiffBaseSHA(commits, &store.ProjectIssueReviewCheckpoint{
		LastReviewCommitSHA: "middle",
	})
	require.False(t, fallback)
	require.Equal(t, "middle", base)

	base, fallback = resolveReviewDiffBaseSHA(commits, &store.ProjectIssueReviewCheckpoint{
		LastReviewCommitSHA: "missing",
	})
	require.True(t, fallback)
	require.Equal(t, "first", base)
}

func createIssueReviewCommit(
	t *testing.T,
	router http.Handler,
	projectID string,
	orgID string,
	payload map[string]any,
) string {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/commits?org_id="+orgID,
		bytes.NewReader(body),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var response projectCommitCreateResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	return response.Commit.SHA
}

func issueReviewStringPtr(value string) *string {
	return &value
}
