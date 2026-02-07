package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func TestIssuesHandlerSaveReviewCreatesSingleCommitAndCheckpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-save-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Save")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	ownerID := insertMessageTestAgent(t, db, orgID, "issue-review-owner")
	reviewerID := insertMessageTestAgent(t, db, orgID, "issue-review-reviewer")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Save review",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-save-review.md"),
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: reviewerID,
		Role:    "collaborator",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
		DB:           db,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/save?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"reviewer_agent_id":"`+reviewerID+`",
			"content":"# Draft\n\nBody {>>AB: tighten this<<}",
			"commit_subject":"Save review comments"
		}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var payload issueReviewSaveResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, issue.ID, payload.IssueID)
	require.Equal(t, projectID, payload.ProjectID)
	require.Equal(t, reviewerID, payload.ReviewerAgentID)
	require.NotNil(t, payload.OwnerAgentID)
	require.Equal(t, ownerID, *payload.OwnerAgentID)
	require.NotEmpty(t, payload.ReviewCommitSHA)
	require.Equal(t, payload.ReviewCommitSHA, payload.Commit.SHA)

	commits, err := handler.CommitStore.ListCommits(testCtxWithWorkspace(orgID), store.ProjectCommitFilter{
		ProjectID: projectID,
		Limit:     20,
	})
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, "Save review comments", commits[0].Subject)
	files := commitDiffFilesFromMetadata(commits[0].Metadata)
	require.Len(t, files, 1)
	require.Equal(t, "/posts/2026-02-06-save-review.md", files[0].Path)

	checkpoint, err := issueStore.GetReviewCheckpoint(testCtxWithWorkspace(orgID), issue.ID)
	require.NoError(t, err)
	require.Equal(t, payload.ReviewCommitSHA, checkpoint.LastReviewCommitSHA)

	var ownerNotificationCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM project_issue_review_notifications
			WHERE org_id = $1
			  AND issue_id = $2
			  AND notification_type = $3
			  AND target_agent_id = $4
			  AND review_commit_sha = $5`,
		orgID,
		issue.ID,
		store.IssueReviewNotificationSavedForOwner,
		ownerID,
		payload.ReviewCommitSHA,
	).Scan(&ownerNotificationCount)
	require.NoError(t, err)
	require.Equal(t, 1, ownerNotificationCount)
}

func TestIssuesHandlerSaveReviewBroadcastsOwnerNotificationEvent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-save-ws-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Save WS")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	ownerID := insertMessageTestAgent(t, db, orgID, "issue-review-owner-ws")
	reviewerID := insertMessageTestAgent(t, db, orgID, "issue-review-reviewer-ws")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Save review ws",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-save-review-ws.md"),
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: reviewerID,
		Role:    "collaborator",
	})
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()
	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	client.SubscribeTopic(issueChannel(issue.ID))
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })
	time.Sleep(20 * time.Millisecond)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
		DB:           db,
		Hub:          hub,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/save?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"reviewer_agent_id":"`+reviewerID+`",
			"content":"# Draft\n\nBody {>>AB: tighten this<<}",
			"commit_subject":"Save review comments"
		}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	select {
	case raw := <-client.Send:
		var event issueReviewSavedEvent
		require.NoError(t, json.Unmarshal(raw, &event))
		require.Equal(t, ws.MessageIssueReviewSaved, event.Type)
		require.Equal(t, issue.ID, event.IssueID)
		require.Equal(t, projectID, event.ProjectID)
		require.Equal(t, "/posts/2026-02-06-save-review-ws.md", event.DocumentPath)
		require.Equal(t, reviewerID, event.ReviewerAgentID)
		require.NotNil(t, event.OwnerAgentID)
		require.Equal(t, ownerID, *event.OwnerAgentID)
		require.NotEmpty(t, event.ReviewCommitSHA)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected review-saved websocket event")
	}
}

func TestIssuesHandlerSaveReviewRejectsUnauthorizedReviewer(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-save-auth-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Save Auth")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	ownerID := insertMessageTestAgent(t, db, orgID, "issue-review-owner-auth")
	outsiderID := insertMessageTestAgent(t, db, orgID, "issue-review-outsider-auth")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Save review auth",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-save-review-auth.md"),
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/save?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"reviewer_agent_id":"`+outsiderID+`",
			"content":"# Draft\n\nUnauthorized review"
		}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRegularDocumentCommitDoesNotEmitReviewSavedActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-save-regression-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Save Regression")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	commitRouter := newProjectCommitsTestRouter(&ProjectCommitsHandler{
		ProjectStore: store.NewProjectStore(db),
		CommitStore:  store.NewProjectCommitStore(db),
		ProjectRepos: store.NewProjectRepoStore(db),
	})
	_ = createIssueReviewCommit(t, commitRouter, projectID, orgID, map[string]any{
		"path":           "/posts/2026-02-06-regular.md",
		"content":        "# Draft\n\nRegular commit",
		"commit_subject": "Regular browser commit",
		"commit_body":    "Provides enough detail for policy while not being a review-save operation.",
	})

	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND action = 'issue.review_saved'`,
		orgID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func issueReviewSaveStringPtr(value string) *string {
	return &value
}
