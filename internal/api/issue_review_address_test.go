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

func TestIssuesHandlerAddressReviewMarksLatestReviewVersionAsAddressed(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-address-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Address")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	ownerID := insertMessageTestAgent(t, db, orgID, "issue-review-address-owner")
	reviewerID := insertMessageTestAgent(t, db, orgID, "issue-review-address-reviewer")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Address review",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-address-review.md"),
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

	saveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/save?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"reviewer_agent_id":"`+reviewerID+`",
			"content":"# Draft\n\nBody {>>AB: address this<<}",
			"commit_subject":"Save review comments"
		}`)),
	)
	saveRec := httptest.NewRecorder()
	router.ServeHTTP(saveRec, saveReq)
	require.Equal(t, http.StatusCreated, saveRec.Code)

	var saveResp issueReviewSaveResponse
	require.NoError(t, json.NewDecoder(saveRec.Body).Decode(&saveResp))
	require.NotEmpty(t, saveResp.ReviewCommitSHA)

	addressReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/address?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"author_agent_id":"`+ownerID+`",
			"content":"# Draft\n\nAddressed feedback in this revision.",
			"commit_subject":"Address review comments",
			"commit_body":"Updates the draft to address the latest review comments and clarify the argument."
		}`)),
	)
	addressRec := httptest.NewRecorder()
	router.ServeHTTP(addressRec, addressReq)
	require.Equal(t, http.StatusCreated, addressRec.Code)

	var addressResp issueReviewAddressResponse
	require.NoError(t, json.NewDecoder(addressRec.Body).Decode(&addressResp))
	require.Equal(t, issue.ID, addressResp.IssueID)
	require.Equal(t, ownerID, addressResp.AuthorAgentID)
	require.Equal(t, saveResp.ReviewCommitSHA, addressResp.AddressedReviewCommitSHA)
	require.NotEmpty(t, addressResp.AddressedInCommitSHA)

	versions, err := issueStore.ListReviewVersions(testCtxWithWorkspace(orgID), issue.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	require.NotNil(t, versions[0].AddressedInCommitSHA)
	require.Equal(t, addressResp.AddressedInCommitSHA, *versions[0].AddressedInCommitSHA)

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
	var found bool
	for _, item := range history.Items {
		if item.SHA == saveResp.ReviewCommitSHA {
			found = true
			require.NotNil(t, item.AddressedInCommitSHA)
			require.Equal(t, addressResp.AddressedInCommitSHA, *item.AddressedInCommitSHA)
		}
	}
	require.True(t, found, "expected review commit to appear in history")

	var reviewerNotificationCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM project_issue_review_notifications
			WHERE org_id = $1
			  AND issue_id = $2
			  AND notification_type = $3
			  AND target_agent_id = $4
			  AND review_commit_sha = $5
			  AND addressed_in_commit_sha = $6`,
		orgID,
		issue.ID,
		store.IssueReviewNotificationAddressedForReviewer,
		reviewerID,
		saveResp.ReviewCommitSHA,
		addressResp.AddressedInCommitSHA,
	).Scan(&reviewerNotificationCount)
	require.NoError(t, err)
	require.Equal(t, 1, reviewerNotificationCount)
}

func TestIssuesHandlerAddressReviewBroadcastsReviewerNotificationEvent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-address-ws-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Review Address WS")
	fixture := newPublishRepoFixture(t)
	seedProjectCommitRepoBinding(t, db, orgID, projectID, fixture.LocalPath)

	ownerID := insertMessageTestAgent(t, db, orgID, "issue-review-address-owner-ws")
	reviewerID := insertMessageTestAgent(t, db, orgID, "issue-review-address-reviewer-ws")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Address review ws",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-address-review-ws.md"),
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

	saveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/save?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"reviewer_agent_id":"`+reviewerID+`",
			"content":"# Draft\n\nBody {>>AB: address this<<}",
			"commit_subject":"Save review comments"
		}`)),
	)
	saveRec := httptest.NewRecorder()
	router.ServeHTTP(saveRec, saveReq)
	require.Equal(t, http.StatusCreated, saveRec.Code)

	var saveResp issueReviewSaveResponse
	require.NoError(t, json.NewDecoder(saveRec.Body).Decode(&saveResp))
	require.NotEmpty(t, saveResp.ReviewCommitSHA)

	select {
	case <-client.Send:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected initial review-saved websocket event")
	}

	addressReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/review/address?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"author_agent_id":"`+ownerID+`",
			"content":"# Draft\n\nAddressed feedback in this revision.",
			"commit_subject":"Address review comments",
			"commit_body":"Updates the draft to address the latest review comments and clarify the argument."
		}`)),
	)
	addressRec := httptest.NewRecorder()
	router.ServeHTTP(addressRec, addressReq)
	require.Equal(t, http.StatusCreated, addressRec.Code)

	var addressResp issueReviewAddressResponse
	require.NoError(t, json.NewDecoder(addressRec.Body).Decode(&addressResp))

	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case raw := <-client.Send:
			var event issueReviewAddressedEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				continue
			}
			if event.Type != ws.MessageIssueReviewAddressed {
				continue
			}
			require.Equal(t, issue.ID, event.IssueID)
			require.Equal(t, projectID, event.ProjectID)
			require.Equal(t, addressResp.AddressedReviewCommitSHA, event.AddressedReviewCommitSHA)
			require.Equal(t, addressResp.AddressedInCommitSHA, event.AddressedInCommitSHA)
			require.NotNil(t, event.ReviewerAgentID)
			require.Equal(t, reviewerID, *event.ReviewerAgentID)
			return
		case <-deadline:
			t.Fatal("expected review-addressed websocket event")
		}
	}
}

func TestTechnonymousModeHasNoPerCommentResolveEndpoint(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-review-resolve-regression-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Resolve Regression")
	ownerID := insertMessageTestAgent(t, db, orgID, "issue-resolve-regression-owner")
	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(testCtxWithWorkspace(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "No resolve endpoint",
		Origin:       "local",
		DocumentPath: issueReviewSaveStringPtr("/posts/2026-02-06-no-resolve.md"),
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(testCtxWithWorkspace(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments/comment-1/resolve?org_id="+orgID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
