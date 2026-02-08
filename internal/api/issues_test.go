package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func newIssueTestRouter(handler *IssuesHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/issues", handler.List)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}", handler.Get)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/sub-issues", handler.CreateSubIssuesBatch)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/comments", handler.CreateComment)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/approval-state", handler.TransitionApprovalState)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/approve", handler.Approve)
	router.With(middleware.OptionalWorkspace).Patch("/api/issues/{id}", handler.PatchIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/claim", handler.ClaimIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/release", handler.ReleaseIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/reviewer-decision", handler.ReviewerDecision)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/review/save", handler.SaveReview)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/review/address", handler.AddressReview)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/changes", handler.ReviewChanges)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/history", handler.ReviewHistory)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/history/{sha}", handler.ReviewVersion)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/participants", handler.AddParticipant)
	router.With(middleware.OptionalWorkspace).Delete("/api/issues/{id}/participants/{agentID}", handler.RemoveParticipant)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues", handler.CreateIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues/link", handler.CreateLinkedIssue)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/issues/queue", handler.RoleQueue)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues/queue/claim-next", handler.ClaimNextQueueIssue)
	router.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/issue-role-assignments", handler.ListRoleAssignments)
	router.With(middleware.OptionalWorkspace).Put("/api/projects/{id}/issue-role-assignments/{role}", handler.UpsertRoleAssignment)
	return router
}

func issueTestCtx(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}

func TestResolveIssueQueueRoleWorkStatus(t *testing.T) {
	testCases := []struct {
		name       string
		role       string
		wantStatus string
		wantErr    bool
	}{
		{name: "planner", role: "planner", wantStatus: store.IssueWorkStatusReady},
		{name: "worker", role: "worker", wantStatus: store.IssueWorkStatusReadyForWork},
		{name: "reviewer", role: "reviewer", wantStatus: store.IssueWorkStatusReview},
		{name: "trim and case normalize", role: "  Worker  ", wantStatus: store.IssueWorkStatusReadyForWork},
		{name: "invalid role", role: "observer", wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveIssueQueueRoleWorkStatus(tc.role)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, got)
		})
	}
}

func TestIssuesHandlerListAndGetIncludeOwnerParticipantsAndComments(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-list-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue API Project")
	ownerID := insertMessageTestAgent(t, db, orgID, "issue-owner")
	collabID := insertMessageTestAgent(t, db, orgID, "issue-collab")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue API title",
		Origin:    "github",
	})
	require.NoError(t, err)
	_, err = issueStore.UpsertGitHubLink(ctx, store.UpsertProjectIssueGitHubLinkInput{
		IssueID:            issue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       42,
		GitHubURL:          issueTestStringPtr("https://github.com/samhotchkiss/otter-camp/issues/42"),
		GitHubState:        "open",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerID,
		Role:    "owner",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: collabID,
		Role:    "collaborator",
	})
	require.NoError(t, err)
	_, err = issueStore.CreateComment(ctx, store.CreateProjectIssueCommentInput{
		IssueID:       issue.ID,
		AuthorAgentID: ownerID,
		Body:          "First comment",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	listReq := httptest.NewRequest(http.MethodGet, "/api/issues?org_id="+orgID+"&project_id="+projectID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp issueListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Equal(t, 1, listResp.Total)
	require.Equal(t, issue.ID, listResp.Items[0].ID)
	require.NotNil(t, listResp.Items[0].OwnerAgentID)
	require.Equal(t, ownerID, *listResp.Items[0].OwnerAgentID)
	require.Equal(t, "issue", listResp.Items[0].Kind)
	require.NotNil(t, listResp.Items[0].GitHubNumber)
	require.Equal(t, int64(42), *listResp.Items[0].GitHubNumber)
	require.NotNil(t, listResp.Items[0].GitHubURL)
	require.Equal(t, "https://github.com/samhotchkiss/otter-camp/issues/42", *listResp.Items[0].GitHubURL)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Equal(t, issue.ID, detail.Issue.ID)
	require.Len(t, detail.Participants, 2)
	require.Len(t, detail.Comments, 1)
	require.Equal(t, "First comment", detail.Comments[0].Body)
	require.Equal(t, "issue", detail.Issue.Kind)
	require.NotNil(t, detail.Issue.GitHubRepositoryFullName)
	require.Equal(t, "samhotchkiss/otter-camp", *detail.Issue.GitHubRepositoryFullName)
	require.NotEmpty(t, detail.Issue.LastActivityAt)
}

func TestIssuesHandlerCommentCreateValidatesAndPersists(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-comment-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Comment Project")
	authorID := insertMessageTestAgent(t, db, orgID, "issue-author")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue for comments",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	badReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorID+`"}`)),
	)
	badRec := httptest.NewRecorder()
	router.ServeHTTP(badRec, badReq)
	require.Equal(t, http.StatusBadRequest, badRec.Code)

	goodReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorID+`","body":"Looks good"}`)),
	)
	goodRec := httptest.NewRecorder()
	router.ServeHTTP(goodRec, goodReq)
	require.Equal(t, http.StatusCreated, goodRec.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Len(t, detail.Comments, 1)
	require.Equal(t, "Looks good", detail.Comments[0].Body)
}

func TestIssuesHandlerPatchIssueUpdatesAndClearsWorkTrackingFields(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-patch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Patch Project")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "issue-patch-owner")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Patchable issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore: issueStore,
	}
	router := newIssueTestRouter(handler)

	setReqBody := `{
		"owner_agent_id":"` + ownerAgentID + `",
		"work_status":"in_progress",
		"priority":"P0",
		"due_at":"2026-02-12T09:00:00Z",
		"next_step":"Prepare review notes",
		"next_step_due_at":"2026-02-11T09:00:00Z"
	}`
	setReq := httptest.NewRequest(http.MethodPatch, "/api/issues/"+issue.ID+"?org_id="+orgID, bytes.NewReader([]byte(setReqBody)))
	setRec := httptest.NewRecorder()
	router.ServeHTTP(setRec, setReq)
	require.Equal(t, http.StatusOK, setRec.Code)

	var updated issueSummaryPayload
	require.NoError(t, json.NewDecoder(setRec.Body).Decode(&updated))
	require.NotNil(t, updated.OwnerAgentID)
	require.Equal(t, ownerAgentID, *updated.OwnerAgentID)
	require.Equal(t, store.IssueWorkStatusInProgress, updated.WorkStatus)
	require.Equal(t, store.IssuePriorityP0, updated.Priority)
	require.NotNil(t, updated.DueAt)
	require.NotNil(t, updated.NextStepDueAt)
	require.NotNil(t, updated.NextStep)
	require.Equal(t, "Prepare review notes", *updated.NextStep)

	clearReqBody := `{
		"due_at":"",
		"next_step":"",
		"next_step_due_at":""
	}`
	clearReq := httptest.NewRequest(http.MethodPatch, "/api/issues/"+issue.ID+"?org_id="+orgID, bytes.NewReader([]byte(clearReqBody)))
	clearRec := httptest.NewRecorder()
	router.ServeHTTP(clearRec, clearReq)
	require.Equal(t, http.StatusOK, clearRec.Code)

	var cleared issueSummaryPayload
	require.NoError(t, json.NewDecoder(clearRec.Body).Decode(&cleared))
	require.Nil(t, cleared.DueAt)
	require.Nil(t, cleared.NextStep)
	require.Nil(t, cleared.NextStepDueAt)
}

func TestIssuesHandlerPatchIssueBranchTracking(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-patch-branch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Patch Branch Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Branch patchable issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	setReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"active_branch":"feature/spec-105","last_commit_sha":"abcdef1234567"}`)),
	)
	setRec := httptest.NewRecorder()
	router.ServeHTTP(setRec, setReq)
	require.Equal(t, http.StatusOK, setRec.Code)

	var updated issueSummaryPayload
	require.NoError(t, json.NewDecoder(setRec.Body).Decode(&updated))
	require.NotNil(t, updated.ActiveBranch)
	require.Equal(t, "feature/spec-105", *updated.ActiveBranch)
	require.NotNil(t, updated.LastCommitSHA)
	require.Equal(t, "abcdef1234567", *updated.LastCommitSHA)

	clearReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"active_branch":"","last_commit_sha":""}`)),
	)
	clearRec := httptest.NewRecorder()
	router.ServeHTTP(clearRec, clearReq)
	require.Equal(t, http.StatusOK, clearRec.Code)

	var cleared issueSummaryPayload
	require.NoError(t, json.NewDecoder(clearRec.Body).Decode(&cleared))
	require.Nil(t, cleared.ActiveBranch)
	require.Nil(t, cleared.LastCommitSHA)

	invalidBranchReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"active_branch":"bad branch"}`)),
	)
	invalidBranchRec := httptest.NewRecorder()
	router.ServeHTTP(invalidBranchRec, invalidBranchReq)
	require.Equal(t, http.StatusBadRequest, invalidBranchRec.Code)

	invalidSHAReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"last_commit_sha":"123xyz"}`)),
	)
	invalidSHARec := httptest.NewRecorder()
	router.ServeHTTP(invalidSHARec, invalidSHAReq)
	require.Equal(t, http.StatusBadRequest, invalidSHARec.Code)
}

func TestIssuesHandlerPatchIssueRejectsInvalidTransitionsAndValues(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-patch-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Patch Validate Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Patch validation issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	invalidTransitionReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"work_status":"done"}`)),
	)
	invalidTransitionRec := httptest.NewRecorder()
	router.ServeHTTP(invalidTransitionRec, invalidTransitionReq)
	require.Equal(t, http.StatusBadRequest, invalidTransitionRec.Code)

	invalidPriorityReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"priority":"P9"}`)),
	)
	invalidPriorityRec := httptest.NewRecorder()
	router.ServeHTTP(invalidPriorityRec, invalidPriorityReq)
	require.Equal(t, http.StatusBadRequest, invalidPriorityRec.Code)
}

func TestIssuesHandlerPatchIssueAllowsClosingFromQueuedOrInProgress(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-patch-close-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Patch Close Project")

	issueStore := store.NewProjectIssueStore(db)
	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	testCases := []struct {
		name           string
		initialStatus  string
		expectedStatus string
	}{
		{
			name:           "close from queued",
			initialStatus:  store.IssueWorkStatusQueued,
			expectedStatus: store.IssueWorkStatusDone,
		},
		{
			name:           "close from in progress",
			initialStatus:  store.IssueWorkStatusInProgress,
			expectedStatus: store.IssueWorkStatusDone,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
				ProjectID:  projectID,
				Title:      "Patch close " + tc.name,
				Origin:     "local",
				WorkStatus: tc.initialStatus,
			})
			require.NoError(t, err)

			req := httptest.NewRequest(
				http.MethodPatch,
				"/api/issues/"+issue.ID+"?org_id="+orgID,
				bytes.NewReader([]byte(`{"state":"closed","work_status":"done"}`)),
			)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			var payload issueSummaryPayload
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, "closed", payload.State)
			require.Equal(t, tc.expectedStatus, payload.WorkStatus)
		})
	}
}

func TestIssuesHandlerClaimIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-claim-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Claim API Project")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-claim-api-agent")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Claimable issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReadyForWork,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/claim?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"`+agentID+`"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload issueSummaryPayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.NotNil(t, payload.OwnerAgentID)
	require.Equal(t, agentID, *payload.OwnerAgentID)
	require.Equal(t, store.IssueWorkStatusInProgress, payload.WorkStatus)

	participants, err := issueStore.ListParticipants(issueTestCtx(orgID), issue.ID, false)
	require.NoError(t, err)
	require.Len(t, participants, 1)
	require.Equal(t, "owner", participants[0].Role)
	require.Equal(t, agentID, participants[0].AgentID)
}

func TestIssuesHandlerReleaseIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-release-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Release API Project")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-release-api-agent")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Releasable issue",
		Origin:       "local",
		OwnerAgentID: &agentID,
		WorkStatus:   store.IssueWorkStatusInProgress,
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(issueTestCtx(orgID), store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: agentID,
		Role:    "owner",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issue.ID+"/release?org_id="+orgID, bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload issueSummaryPayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Nil(t, payload.OwnerAgentID)
	require.Equal(t, store.IssueWorkStatusReadyForWork, payload.WorkStatus)

	participants, err := issueStore.ListParticipants(issueTestCtx(orgID), issue.ID, false)
	require.NoError(t, err)
	require.Len(t, participants, 0)
}

func TestIssuesHandlerClaimIssueAndReleaseIssueValidation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-claim-release-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Claim Release Validate Project")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-claim-release-validate-agent")

	issueStore := store.NewProjectIssueStore(db)
	readyIssue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Ready queue issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReady,
	})
	require.NoError(t, err)

	doneIssue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Done issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusDone,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	missingAgentReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+readyIssue.ID+"/claim?org_id="+orgID,
		bytes.NewReader([]byte(`{}`)),
	)
	missingAgentRec := httptest.NewRecorder()
	router.ServeHTTP(missingAgentRec, missingAgentReq)
	require.Equal(t, http.StatusBadRequest, missingAgentRec.Code)

	notClaimableReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+doneIssue.ID+"/claim?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"`+agentID+`"}`)),
	)
	notClaimableRec := httptest.NewRecorder()
	router.ServeHTTP(notClaimableRec, notClaimableReq)
	require.Equal(t, http.StatusBadRequest, notClaimableRec.Code)

	unclaimedReleaseReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+readyIssue.ID+"/release?org_id="+orgID,
		bytes.NewReader([]byte(`{}`)),
	)
	unclaimedReleaseRec := httptest.NewRecorder()
	router.ServeHTTP(unclaimedReleaseRec, unclaimedReleaseReq)
	require.Equal(t, http.StatusBadRequest, unclaimedReleaseRec.Code)
}

func TestIssuesHandlerReviewerDecision(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-reviewer-decision-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Reviewer Decision API Project")
	reviewerID := insertMessageTestAgent(t, db, orgID, "issue-reviewer-decision-reviewer")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	makeReviewIssue := func(title string) *store.ProjectIssue {
		issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
			ProjectID:    projectID,
			Title:        title,
			Origin:       "local",
			OwnerAgentID: &reviewerID,
			WorkStatus:   store.IssueWorkStatusReview,
		})
		require.NoError(t, err)
		_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
			IssueID: issue.ID,
			AgentID: reviewerID,
			Role:    "owner",
		})
		require.NoError(t, err)
		return issue
	}

	approveIssue := makeReviewIssue("Approve candidate")
	reworkIssue := makeReviewIssue("Rework candidate")
	escalateIssue := makeReviewIssue("Escalate candidate")

	handler := &IssuesHandler{IssueStore: issueStore, DB: db}
	router := newIssueTestRouter(handler)

	approveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+approveIssue.ID+"/reviewer-decision?org_id="+orgID,
		bytes.NewReader([]byte(`{"decision":"approve","reason":"LGTM"}`)),
	)
	approveRec := httptest.NewRecorder()
	router.ServeHTTP(approveRec, approveReq)
	require.Equal(t, http.StatusOK, approveRec.Code)

	var approveResp issueSummaryPayload
	require.NoError(t, json.NewDecoder(approveRec.Body).Decode(&approveResp))
	require.Equal(t, store.IssueWorkStatusDone, approveResp.WorkStatus)
	require.Equal(t, "closed", approveResp.State)
	require.Nil(t, approveResp.OwnerAgentID)

	reworkReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+reworkIssue.ID+"/reviewer-decision?org_id="+orgID,
		bytes.NewReader([]byte(`{"decision":"request_changes"}`)),
	)
	reworkRec := httptest.NewRecorder()
	router.ServeHTTP(reworkRec, reworkReq)
	require.Equal(t, http.StatusOK, reworkRec.Code)

	var reworkResp issueSummaryPayload
	require.NoError(t, json.NewDecoder(reworkRec.Body).Decode(&reworkResp))
	require.Equal(t, store.IssueWorkStatusInProgress, reworkResp.WorkStatus)
	require.Equal(t, "open", reworkResp.State)
	require.Nil(t, reworkResp.OwnerAgentID)

	escalateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+escalateIssue.ID+"/reviewer-decision?org_id="+orgID,
		bytes.NewReader([]byte(`{"decision":"escalate","reason":"needs human decision"}`)),
	)
	escalateRec := httptest.NewRecorder()
	router.ServeHTTP(escalateRec, escalateReq)
	require.Equal(t, http.StatusOK, escalateRec.Code)

	var escalateResp issueSummaryPayload
	require.NoError(t, json.NewDecoder(escalateRec.Body).Decode(&escalateResp))
	require.Equal(t, store.IssueWorkStatusFlagged, escalateResp.WorkStatus)
	require.Equal(t, "open", escalateResp.State)
	require.Nil(t, escalateResp.OwnerAgentID)

	invalidDecisionReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+makeReviewIssue("Invalid decision candidate").ID+"/reviewer-decision?org_id="+orgID,
		bytes.NewReader([]byte(`{"decision":"ship_it"}`)),
	)
	invalidDecisionRec := httptest.NewRecorder()
	router.ServeHTTP(invalidDecisionRec, invalidDecisionReq)
	require.Equal(t, http.StatusBadRequest, invalidDecisionRec.Code)

	missingDecisionReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+makeReviewIssue("Missing decision candidate").ID+"/reviewer-decision?org_id="+orgID,
		bytes.NewReader([]byte(`{}`)),
	)
	missingDecisionRec := httptest.NewRecorder()
	router.ServeHTTP(missingDecisionRec, missingDecisionReq)
	require.Equal(t, http.StatusBadRequest, missingDecisionRec.Code)
}

func TestIssuesHandlerListSupportsOwnerWorkStatusAndPriorityFilters(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-list-work-filters-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue List Work Filters Project")
	ownerA := insertMessageTestAgent(t, db, orgID, "issue-list-owner-a")
	ownerB := insertMessageTestAgent(t, db, orgID, "issue-list-owner-b")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)

	_, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Owner A in progress",
		Origin:       "local",
		OwnerAgentID: &ownerA,
		WorkStatus:   store.IssueWorkStatusInProgress,
		Priority:     store.IssuePriorityP1,
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Owner B blocked",
		Origin:       "local",
		OwnerAgentID: &ownerB,
		WorkStatus:   store.IssueWorkStatusBlocked,
		Priority:     store.IssuePriorityP3,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	filterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues?org_id="+orgID+"&project_id="+projectID+"&owner_agent_id="+ownerA+"&work_status=in_progress&priority=P1",
		nil,
	)
	filterRec := httptest.NewRecorder()
	router.ServeHTTP(filterRec, filterReq)
	require.Equal(t, http.StatusOK, filterRec.Code)

	var listResp issueListResponse
	require.NoError(t, json.NewDecoder(filterRec.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 1)
	require.Equal(t, "Owner A in progress", listResp.Items[0].Title)
	require.Equal(t, store.IssueWorkStatusInProgress, listResp.Items[0].WorkStatus)
	require.Equal(t, store.IssuePriorityP1, listResp.Items[0].Priority)
	require.NotNil(t, listResp.Items[0].OwnerAgentID)
	require.Equal(t, ownerA, *listResp.Items[0].OwnerAgentID)

	invalidFilterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues?org_id="+orgID+"&project_id="+projectID+"&priority=P9",
		nil,
	)
	invalidFilterRec := httptest.NewRecorder()
	router.ServeHTTP(invalidFilterRec, invalidFilterReq)
	require.Equal(t, http.StatusBadRequest, invalidFilterRec.Code)
}

func TestIssuesHandlerCreateIssueSupportsParentIssueID(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-create-parent-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Create Parent Project")

	issueStore := store.NewProjectIssueStore(db)
	parent, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Parent issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
	}
	router := newIssueTestRouter(handler)

	body := `{
		"title":"Child issue",
		"parent_issue_id":"` + parent.ID + `",
		"work_status":"ready_for_work"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues?org_id="+orgID, bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var summary issueSummaryPayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&summary))
	require.NotNil(t, summary.ParentIssueID)
	require.Equal(t, parent.ID, *summary.ParentIssueID)
	require.Equal(t, store.IssueWorkStatusReadyForWork, summary.WorkStatus)
}

func TestIssuesHandlerCreateSubIssuesBatch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-sub-batch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sub Batch API Project")

	issueStore := store.NewProjectIssueStore(db)
	parent, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Parent issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	reqBody := `{
		"items":[
			{"title":"Batch child 1","body":"First child","priority":"P1","work_status":"ready_for_work"},
			{"title":"Batch child 2"}
		]
	}`
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+parent.ID+"/sub-issues?org_id="+orgID,
		bytes.NewReader([]byte(reqBody)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var resp issueListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Items, 2)
	require.Equal(t, "Batch child 1", resp.Items[0].Title)
	require.NotNil(t, resp.Items[0].ParentIssueID)
	require.Equal(t, parent.ID, *resp.Items[0].ParentIssueID)
	require.Equal(t, store.IssuePriorityP1, resp.Items[0].Priority)
	require.Equal(t, store.IssueWorkStatusReadyForWork, resp.Items[0].WorkStatus)
	require.Equal(t, "Batch child 2", resp.Items[1].Title)
	require.NotNil(t, resp.Items[1].ParentIssueID)
	require.Equal(t, parent.ID, *resp.Items[1].ParentIssueID)
}

func TestIssuesHandlerCreateSubIssuesBatchValidatesAndAvoidsPartialWrites(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-sub-batch-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Sub Batch Validate API Project")

	issueStore := store.NewProjectIssueStore(db)
	parent, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Parent issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	invalidReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+parent.ID+"/sub-issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"items":[{"title":"Valid child"},{"title":"   "}]}`)),
	)
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)

	parentFilter := parent.ID
	children, err := issueStore.ListIssues(issueTestCtx(orgID), store.ProjectIssueFilter{
		ProjectID:     projectID,
		ParentIssueID: &parentFilter,
	})
	require.NoError(t, err)
	require.Len(t, children, 0)
}

func TestIssuesHandlerListSupportsParentIssueFilter(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-list-parent-filter-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue List Parent Filter Project")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	parent, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Parent issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Child A",
		Origin:        "local",
		ParentIssueID: &parent.ID,
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Child B",
		Origin:        "local",
		ParentIssueID: &parent.ID,
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Standalone",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/issues?org_id="+orgID+"&project_id="+projectID+"&parent_issue_id="+parent.ID,
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response issueListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	require.Len(t, response.Items, 2)
	for _, item := range response.Items {
		require.NotNil(t, item.ParentIssueID)
		require.Equal(t, parent.ID, *item.ParentIssueID)
	}
}

func TestIssuesHandlerRoleQueueReturnsRoleMappedStatuses(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-role-queue-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Role Queue Project")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	parent, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Planner parent",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReady,
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Planner child",
		Origin:        "local",
		ParentIssueID: &parent.ID,
		WorkStatus:    store.IssueWorkStatusReady,
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Worker issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReadyForWork,
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Reviewer issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReview,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	plannerReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID+"&role=planner&limit=10",
		nil,
	)
	plannerRec := httptest.NewRecorder()
	router.ServeHTTP(plannerRec, plannerReq)
	require.Equal(t, http.StatusOK, plannerRec.Code)

	var plannerResp issueListResponse
	require.NoError(t, json.NewDecoder(plannerRec.Body).Decode(&plannerResp))
	require.Len(t, plannerResp.Items, 2)
	for _, item := range plannerResp.Items {
		require.Equal(t, store.IssueWorkStatusReady, item.WorkStatus)
	}
	require.Equal(t, "Planner child", plannerResp.Items[0].Title)
	require.NotNil(t, plannerResp.Items[0].ParentIssueID)
	require.Equal(t, parent.ID, *plannerResp.Items[0].ParentIssueID)

	workerReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID+"&role=worker",
		nil,
	)
	workerRec := httptest.NewRecorder()
	router.ServeHTTP(workerRec, workerReq)
	require.Equal(t, http.StatusOK, workerRec.Code)

	var workerResp issueListResponse
	require.NoError(t, json.NewDecoder(workerRec.Body).Decode(&workerResp))
	require.Len(t, workerResp.Items, 1)
	require.Equal(t, "Worker issue", workerResp.Items[0].Title)
	require.Equal(t, store.IssueWorkStatusReadyForWork, workerResp.Items[0].WorkStatus)

	reviewerReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID+"&role=reviewer",
		nil,
	)
	reviewerRec := httptest.NewRecorder()
	router.ServeHTTP(reviewerRec, reviewerReq)
	require.Equal(t, http.StatusOK, reviewerRec.Code)

	var reviewerResp issueListResponse
	require.NoError(t, json.NewDecoder(reviewerRec.Body).Decode(&reviewerResp))
	require.Len(t, reviewerResp.Items, 1)
	require.Equal(t, "Reviewer issue", reviewerResp.Items[0].Title)
	require.Equal(t, store.IssueWorkStatusReview, reviewerResp.Items[0].WorkStatus)
}

func TestIssuesHandlerRoleQueueValidatesRoleAndLimit(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-role-queue-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Role Queue Validate Project")

	handler := &IssuesHandler{IssueStore: store.NewProjectIssueStore(db)}
	router := newIssueTestRouter(handler)

	missingRoleReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID,
		nil,
	)
	missingRoleRec := httptest.NewRecorder()
	router.ServeHTTP(missingRoleRec, missingRoleReq)
	require.Equal(t, http.StatusBadRequest, missingRoleRec.Code)

	invalidRoleReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID+"&role=invalid",
		nil,
	)
	invalidRoleRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRoleRec, invalidRoleReq)
	require.Equal(t, http.StatusBadRequest, invalidRoleRec.Code)

	invalidLimitReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issues/queue?org_id="+orgID+"&role=planner&limit=0",
		nil,
	)
	invalidLimitRec := httptest.NewRecorder()
	router.ServeHTTP(invalidLimitRec, invalidLimitReq)
	require.Equal(t, http.StatusBadRequest, invalidLimitRec.Code)
}

func TestIssuesHandlerClaimNextQueueIssue(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-claim-next-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Claim Next API Project")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-claim-next-api-agent")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	first, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "First worker queue issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReadyForWork,
		Priority:   store.IssuePriorityP0,
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:  projectID,
		Title:      "Second worker queue issue",
		Origin:     "local",
		WorkStatus: store.IssueWorkStatusReadyForWork,
		Priority:   store.IssuePriorityP1,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	claimReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/queue/claim-next?org_id="+orgID,
		bytes.NewReader([]byte(`{"role":"worker","agent_id":"`+agentID+`"}`)),
	)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	require.Equal(t, http.StatusOK, claimRec.Code)

	var claimResp issueSummaryPayload
	require.NoError(t, json.NewDecoder(claimRec.Body).Decode(&claimResp))
	require.Equal(t, first.ID, claimResp.ID)
	require.NotNil(t, claimResp.OwnerAgentID)
	require.Equal(t, agentID, *claimResp.OwnerAgentID)
	require.Equal(t, store.IssueWorkStatusInProgress, claimResp.WorkStatus)

	participants, err := issueStore.ListParticipants(issueTestCtx(orgID), first.ID, false)
	require.NoError(t, err)
	require.Len(t, participants, 1)
	require.Equal(t, "owner", participants[0].Role)
	require.Equal(t, agentID, participants[0].AgentID)

	claimAgainReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/queue/claim-next?org_id="+orgID,
		bytes.NewReader([]byte(`{"role":"reviewer","agent_id":"`+agentID+`"}`)),
	)
	claimAgainRec := httptest.NewRecorder()
	router.ServeHTTP(claimAgainRec, claimAgainReq)
	require.Equal(t, http.StatusNotFound, claimAgainRec.Code)

	invalidReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/queue/claim-next?org_id="+orgID,
		bytes.NewReader([]byte(`{"role":"invalid","agent_id":"`+agentID+`"}`)),
	)
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)
}

func TestIssueRoleAssignmentsHandler_UpsertAndList(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-role-assignment-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Role Assignment API Project")
	plannerID := insertMessageTestAgent(t, db, orgID, "issue-role-planner-api")
	workerID := insertMessageTestAgent(t, db, orgID, "issue-role-worker-api")

	handler := &IssuesHandler{IssueStore: store.NewProjectIssueStore(db)}
	router := newIssueTestRouter(handler)

	setPlannerReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/planner?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"`+plannerID+`"}`)),
	)
	setPlannerRec := httptest.NewRecorder()
	router.ServeHTTP(setPlannerRec, setPlannerReq)
	require.Equal(t, http.StatusOK, setPlannerRec.Code)

	var plannerResp issueRoleAssignmentPayload
	require.NoError(t, json.NewDecoder(setPlannerRec.Body).Decode(&plannerResp))
	require.Equal(t, "planner", plannerResp.Role)
	require.NotNil(t, plannerResp.AgentID)
	require.Equal(t, plannerID, *plannerResp.AgentID)

	setWorkerReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/worker?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"`+workerID+`"}`)),
	)
	setWorkerRec := httptest.NewRecorder()
	router.ServeHTTP(setWorkerRec, setWorkerReq)
	require.Equal(t, http.StatusOK, setWorkerRec.Code)

	clearPlannerReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/planner?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":null}`)),
	)
	clearPlannerRec := httptest.NewRecorder()
	router.ServeHTTP(clearPlannerRec, clearPlannerReq)
	require.Equal(t, http.StatusOK, clearPlannerRec.Code)

	var clearedPlannerResp issueRoleAssignmentPayload
	require.NoError(t, json.NewDecoder(clearPlannerRec.Body).Decode(&clearedPlannerResp))
	require.Equal(t, "planner", clearedPlannerResp.Role)
	require.Nil(t, clearedPlannerResp.AgentID)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/projects/"+projectID+"/issue-role-assignments?org_id="+orgID,
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp issueRoleAssignmentListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 2)
	require.Equal(t, "planner", listResp.Items[0].Role)
	require.Nil(t, listResp.Items[0].AgentID)
	require.Equal(t, "worker", listResp.Items[1].Role)
	require.NotNil(t, listResp.Items[1].AgentID)
	require.Equal(t, workerID, *listResp.Items[1].AgentID)
}

func TestIssueRoleAssignmentsHandler_ValidatesInputs(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-role-assignment-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Role Assignment Validate API Project")

	handler := &IssuesHandler{IssueStore: store.NewProjectIssueStore(db)}
	router := newIssueTestRouter(handler)

	invalidRoleReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/observer?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":null}`)),
	)
	invalidRoleRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRoleRec, invalidRoleReq)
	require.Equal(t, http.StatusBadRequest, invalidRoleRec.Code)

	invalidAgentReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/planner?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"bad-id"}`)),
	)
	invalidAgentRec := httptest.NewRecorder()
	router.ServeHTTP(invalidAgentRec, invalidAgentReq)
	require.Equal(t, http.StatusBadRequest, invalidAgentRec.Code)

	invalidJSONReq := httptest.NewRequest(
		http.MethodPut,
		"/api/projects/"+projectID+"/issue-role-assignments/worker?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"x","extra":true}`)),
	)
	invalidJSONRec := httptest.NewRecorder()
	router.ServeHTTP(invalidJSONRec, invalidJSONReq)
	require.Equal(t, http.StatusBadRequest, invalidJSONRec.Code)
}

func TestIssuesHandlerCreateIssueCreatesStandaloneIssueWithWorkTrackingFields(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-create-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Create Project")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "issue-create-owner")

	issueStore := store.NewProjectIssueStore(db)
	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
	}
	router := newIssueTestRouter(handler)

	dueAt := "2026-02-10T18:00:00Z"
	nextStepDueAt := "2026-02-09T18:00:00Z"
	body := `{
		"title":"Implement standalone issue create endpoint",
		"body":"Use issue-first workflow",
		"owner_agent_id":"` + ownerAgentID + `",
		"priority":"P1",
		"work_status":"in_progress",
		"due_at":"` + dueAt + `",
		"next_step":"Add API tests",
		"next_step_due_at":"` + nextStepDueAt + `"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues?org_id="+orgID, bytes.NewReader([]byte(body)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var summary issueSummaryPayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&summary))
	require.NotEmpty(t, summary.ID)
	require.Equal(t, "Implement standalone issue create endpoint", summary.Title)

	created, err := issueStore.GetIssueByID(issueTestCtx(orgID), summary.ID)
	require.NoError(t, err)
	require.NotNil(t, created.OwnerAgentID)
	require.Equal(t, ownerAgentID, *created.OwnerAgentID)
	require.Equal(t, store.IssueWorkStatusInProgress, created.WorkStatus)
	require.Equal(t, store.IssuePriorityP1, created.Priority)
	require.NotNil(t, created.DueAt)
	require.Equal(t, dueAt, created.DueAt.UTC().Format(time.RFC3339))
	require.NotNil(t, created.NextStep)
	require.Equal(t, "Add API tests", *created.NextStep)
	require.NotNil(t, created.NextStepDueAt)
	require.Equal(t, nextStepDueAt, created.NextStepDueAt.UTC().Format(time.RFC3339))

	participants, err := issueStore.ListParticipants(issueTestCtx(orgID), summary.ID, false)
	require.NoError(t, err)
	require.Len(t, participants, 1)
	require.Equal(t, ownerAgentID, participants[0].AgentID)
	require.Equal(t, "owner", participants[0].Role)
}

func TestIssuesHandlerCreateIssueValidatesPayload(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-create-validate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Create Validate Project")

	handler := &IssuesHandler{
		IssueStore:   store.NewProjectIssueStore(db),
		ProjectStore: store.NewProjectStore(db),
	}
	router := newIssueTestRouter(handler)

	missingTitleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"body":"missing title"}`)),
	)
	missingTitleRec := httptest.NewRecorder()
	router.ServeHTTP(missingTitleRec, missingTitleReq)
	require.Equal(t, http.StatusBadRequest, missingTitleRec.Code)

	invalidOwnerReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"title":"Invalid owner","owner_agent_id":"bad-id"}`)),
	)
	invalidOwnerRec := httptest.NewRecorder()
	router.ServeHTTP(invalidOwnerRec, invalidOwnerReq)
	require.Equal(t, http.StatusBadRequest, invalidOwnerRec.Code)

	invalidDueAtReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"title":"Invalid due","due_at":"not-a-date"}`)),
	)
	invalidDueAtRec := httptest.NewRecorder()
	router.ServeHTTP(invalidDueAtRec, invalidDueAtReq)
	require.Equal(t, http.StatusBadRequest, invalidDueAtRec.Code)
}

func TestIssuesHandlerQueueNotificationDispatch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-queue-notify-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Queue Notify Project")
	unassignedProjectID := insertProjectTestProject(t, db, orgID, "Issue Queue Notify Unassigned Project")
	plannerAgentID := insertMessageTestAgent(t, db, orgID, "issue-queue-notify-planner")
	workerAgentID := insertMessageTestAgent(t, db, orgID, "issue-queue-notify-worker")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	_, err := issueStore.UpsertIssueRoleAssignment(ctx, store.UpsertIssueRoleAssignmentInput{
		ProjectID: projectID,
		Role:      "planner",
		AgentID:   &plannerAgentID,
	})
	require.NoError(t, err)
	_, err = issueStore.UpsertIssueRoleAssignment(ctx, store.UpsertIssueRoleAssignmentInput{
		ProjectID: projectID,
		Role:      "worker",
		AgentID:   &workerAgentID,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:   issueStore,
		ProjectStore: store.NewProjectStore(db),
		DB:           db,
	}
	router := newIssueTestRouter(handler)

	createReadyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"title":"Planner queued issue","work_status":"ready"}`)),
	)
	createReadyRec := httptest.NewRecorder()
	router.ServeHTTP(createReadyRec, createReadyReq)
	require.Equal(t, http.StatusCreated, createReadyRec.Code)

	var readyIssue issueSummaryPayload
	require.NoError(t, json.NewDecoder(createReadyRec.Body).Decode(&readyIssue))

	createPlanningReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"title":"Worker queue candidate","work_status":"planning"}`)),
	)
	createPlanningRec := httptest.NewRecorder()
	router.ServeHTTP(createPlanningRec, createPlanningReq)
	require.Equal(t, http.StatusCreated, createPlanningRec.Code)

	var planningIssue issueSummaryPayload
	require.NoError(t, json.NewDecoder(createPlanningRec.Body).Decode(&planningIssue))

	patchReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+planningIssue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"work_status":"ready_for_work"}`)),
	)
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	require.Equal(t, http.StatusOK, patchRec.Code)

	unassignedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+unassignedProjectID+"/issues?org_id="+orgID,
		bytes.NewReader([]byte(`{"title":"No assignment issue","work_status":"ready"}`)),
	)
	unassignedRec := httptest.NewRecorder()
	router.ServeHTTP(unassignedRec, unassignedReq)
	require.Equal(t, http.StatusCreated, unassignedRec.Code)

	rows, err := db.Query(
		`SELECT event_type, payload
			FROM openclaw_dispatch_queue
			WHERE event_type = 'issue.queue.available'
			ORDER BY id ASC`,
	)
	require.NoError(t, err)
	defer rows.Close()

	var events []openClawIssueQueueDispatchEvent
	for rows.Next() {
		var eventType string
		var payload []byte
		require.NoError(t, rows.Scan(&eventType, &payload))
		require.Equal(t, "issue.queue.available", eventType)
		var event openClawIssueQueueDispatchEvent
		require.NoError(t, json.Unmarshal(payload, &event))
		events = append(events, event)
	}
	require.NoError(t, rows.Err())
	require.Len(t, events, 2)

	require.Equal(t, readyIssue.ID, events[0].Data.IssueID)
	require.Equal(t, readyIssue.IssueNumber, events[0].Data.IssueNumber)
	require.Equal(t, projectID, events[0].Data.ProjectID)
	require.Equal(t, "planner", events[0].Data.Role)
	require.Equal(t, store.IssueWorkStatusReady, events[0].Data.WorkStatus)
	require.Equal(t, plannerAgentID, events[0].Data.AgentID)

	require.Equal(t, planningIssue.ID, events[1].Data.IssueID)
	require.Equal(t, planningIssue.IssueNumber, events[1].Data.IssueNumber)
	require.Equal(t, projectID, events[1].Data.ProjectID)
	require.Equal(t, "worker", events[1].Data.Role)
	require.Equal(t, store.IssueWorkStatusReadyForWork, events[1].Data.WorkStatus)
	require.Equal(t, workerAgentID, events[1].Data.AgentID)
}

func TestIssuesHandlerCommentCreateDispatchesToOpenClawOwner(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-comment-dispatch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Dispatch Project")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "stone")
	authorAgentID := insertMessageTestAgent(t, db, orgID, "sam")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue for dispatch",
		Origin:    "local",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerAgentID,
		Role:    "owner",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &IssuesHandler{
		IssueStore:         issueStore,
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorAgentID+`","body":"Please take a look","sender_type":"user"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawIssueCommentDispatchEvent)
	require.True(t, ok)
	require.Equal(t, "issue.comment.message", event.Type)
	require.Equal(t, orgID, event.OrgID)
	require.Equal(t, issue.ID, event.Data.IssueID)
	require.Equal(t, projectID, event.Data.ProjectID)
	require.Equal(t, "Please take a look", event.Data.Content)
	require.Equal(t, "stone", event.Data.AgentID)
	require.Equal(t, "Agent stone", event.Data.AgentName)
	require.Equal(t, ownerAgentID, event.Data.ResponderAgentID)
	require.Equal(t, issueCommentSessionKey("stone", issue.ID), event.Data.SessionKey)
	require.Equal(t, authorAgentID, event.Data.AuthorAgentID)
	require.Equal(t, "user", event.Data.SenderType)

	var payload struct {
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Delivery.Attempted)
	require.True(t, payload.Delivery.Delivered)
}

func TestIssuesHandlerCommentCreateAgentSenderSkipsOpenClawDispatch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-comment-agent-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Agent Sender Project")
	authorAgentID := insertMessageTestAgent(t, db, orgID, "stone")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue for assistant reply",
		Origin:    "local",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &IssuesHandler{
		IssueStore:         issueStore,
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorAgentID+`","body":"Assistant reply","sender_type":"agent"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 0)

	var payload struct {
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.Delivery.Attempted)
	require.False(t, payload.Delivery.Delivered)
}

func TestIssuesHandlerCommentCreateWarnsWhenBridgeOffline(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-comment-offline-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Offline Project")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "stone")
	authorAgentID := insertMessageTestAgent(t, db, orgID, "sam")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue for offline bridge",
		Origin:    "local",
	})
	require.NoError(t, err)
	_, err = issueStore.AddParticipant(ctx, store.AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerAgentID,
		Role:    "owner",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: false}
	handler := &IssuesHandler{
		IssueStore:         issueStore,
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorAgentID+`","body":"Hello owner"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	var payload struct {
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Delivery.Attempted)
	require.False(t, payload.Delivery.Delivered)
	require.Equal(t, openClawDispatchQueuedWarning, payload.Delivery.Error)

	var queued int
	err = db.QueryRow(`SELECT COUNT(*) FROM openclaw_dispatch_queue WHERE event_type = 'issue.comment.message'`).Scan(&queued)
	require.NoError(t, err)
	require.Equal(t, 1, queued)
}

func TestIssuesHandlerCommentCreateWithoutOwnerStillPersistsAndWarns(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-comment-no-owner-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue No Owner Project")
	authorAgentID := insertMessageTestAgent(t, db, orgID, "sam")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue without owner",
		Origin:    "local",
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &IssuesHandler{
		IssueStore:         issueStore,
		DB:                 db,
		OpenClawDispatcher: dispatcher,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorAgentID+`","body":"No owner yet"}`)),
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.Len(t, dispatcher.calls, 0)

	var payload struct {
		Body     string           `json:"body"`
		Delivery dmDeliveryStatus `json:"delivery"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "No owner yet", payload.Body)
	require.False(t, payload.Delivery.Attempted)
	require.False(t, payload.Delivery.Delivered)
	require.Equal(t, "issue agent unavailable; message was saved but not delivered", payload.Delivery.Error)

	comments, err := issueStore.ListComments(ctx, issue.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, comments, 1)
	require.Equal(t, "No owner yet", comments[0].Body)
	require.Equal(t, authorAgentID, comments[0].AuthorAgentID)
}

func TestIssuesHandlerParticipantAddRemove(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-participants-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Participant Project")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-agent")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue for participants",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	addReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/participants?org_id="+orgID,
		bytes.NewReader([]byte(`{"agent_id":"`+agentID+`","role":"collaborator"}`)),
	)
	addRec := httptest.NewRecorder()
	router.ServeHTTP(addRec, addReq)
	require.Equal(t, http.StatusCreated, addRec.Code)

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/issues/"+issue.ID+"/participants/"+agentID+"?org_id="+orgID, nil)
	removeRec := httptest.NewRecorder()
	router.ServeHTTP(removeRec, removeReq)
	require.Equal(t, http.StatusOK, removeRec.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Len(t, detail.Participants, 1)
	require.NotNil(t, detail.Participants[0].RemovedAt)
}

func TestIssuesHandlerRejectsCrossOrgAccess(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "issues-api-iso-a")
	orgB := insertMessageTestOrganization(t, db, "issues-api-iso-b")
	projectA := insertProjectTestProject(t, db, orgA, "Issue API Project A")
	projectB := insertProjectTestProject(t, db, orgB, "Issue API Project B")
	agentA := insertMessageTestAgent(t, db, orgA, "iso-agent-a")

	issueStore := store.NewProjectIssueStore(db)
	issueA, err := issueStore.CreateIssue(issueTestCtx(orgA), store.CreateProjectIssueInput{
		ProjectID: projectA,
		Title:     "Org A issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	issueB, err := issueStore.CreateIssue(issueTestCtx(orgB), store.CreateProjectIssueInput{
		ProjectID: projectB,
		Title:     "Org B issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	require.NotEmpty(t, issueA.ID)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issueB.ID+"?org_id="+orgA, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, getRec.Code)

	commentReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issueB.ID+"/comments?org_id="+orgA,
		bytes.NewReader([]byte(`{"author_agent_id":"`+agentA+`","body":"cross org"}`)),
	)
	commentRec := httptest.NewRecorder()
	router.ServeHTTP(commentRec, commentReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, commentRec.Code)

	participantReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issueB.ID+"/participants?org_id="+orgA,
		bytes.NewReader([]byte(`{"agent_id":"`+agentA+`","role":"collaborator"}`)),
	)
	participantRec := httptest.NewRecorder()
	router.ServeHTTP(participantRec, participantReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, participantRec.Code)
}

func TestIssuesHandlerCommentBroadcastsToIssueChannelSubscribersOnly(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-ws-org")
	otherOrgID := insertMessageTestOrganization(t, db, "issues-api-ws-other-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue WS Project")
	authorID := insertMessageTestAgent(t, db, orgID, "issue-ws-author")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	issueA, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue A",
		Origin:    "local",
	})
	require.NoError(t, err)
	issueB, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue B",
		Origin:    "local",
	})
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()

	clientA := ws.NewClient(hub, nil)
	clientA.SetOrgID(orgID)
	clientA.SubscribeTopic(issueChannel(issueA.ID))
	hub.Register(clientA)

	clientB := ws.NewClient(hub, nil)
	clientB.SetOrgID(orgID)
	clientB.SubscribeTopic(issueChannel(issueB.ID))
	hub.Register(clientB)

	clientOtherOrg := ws.NewClient(hub, nil)
	clientOtherOrg.SetOrgID(otherOrgID)
	clientOtherOrg.SubscribeTopic(issueChannel(issueA.ID))
	hub.Register(clientOtherOrg)

	t.Cleanup(func() {
		hub.Unregister(clientA)
		hub.Unregister(clientB)
		hub.Unregister(clientOtherOrg)
	})

	time.Sleep(25 * time.Millisecond)

	handler := &IssuesHandler{IssueStore: issueStore, Hub: hub}
	router := newIssueTestRouter(handler)

	commentReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issueA.ID+"/comments?org_id="+orgID,
		bytes.NewReader([]byte(`{"author_agent_id":"`+authorID+`","body":"WS comment"}`)),
	)
	commentRec := httptest.NewRecorder()
	router.ServeHTTP(commentRec, commentReq)
	require.Equal(t, http.StatusCreated, commentRec.Code)

	select {
	case payload := <-clientA.Send:
		var event issueCommentCreatedEvent
		require.NoError(t, json.Unmarshal(payload, &event))
		require.Equal(t, ws.MessageIssueCommentCreated, event.Type)
		require.Equal(t, issueA.ID, event.IssueID)
		require.Equal(t, "WS comment", event.Comment.Body)
	case <-time.After(300 * time.Millisecond):
		t.Fatal("expected issue A subscriber to receive websocket event")
	}

	select {
	case payload := <-clientB.Send:
		t.Fatalf("expected issue B subscriber to receive no event, got %s", string(payload))
	case <-time.After(120 * time.Millisecond):
	}

	select {
	case payload := <-clientOtherOrg.Send:
		t.Fatalf("expected cross-org subscriber to receive no event, got %s", string(payload))
	case <-time.After(120 * time.Millisecond):
	}
}

func TestIssuesHandlerListSupportsKindFiltering(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-kind-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Kind Project")
	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)

	githubIssue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "GitHub Issue",
		Origin:    "github",
	})
	require.NoError(t, err)
	_, err = issueStore.UpsertGitHubLink(ctx, store.UpsertProjectIssueGitHubLinkInput{
		IssueID:            githubIssue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       101,
		GitHubURL:          issueTestStringPtr("https://github.com/samhotchkiss/otter-camp/issues/101"),
		GitHubState:        "open",
	})
	require.NoError(t, err)

	githubPR, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "GitHub PR",
		Origin:    "github",
	})
	require.NoError(t, err)
	_, err = issueStore.UpsertGitHubLink(ctx, store.UpsertProjectIssueGitHubLinkInput{
		IssueID:            githubPR.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       202,
		GitHubURL:          issueTestStringPtr("https://github.com/samhotchkiss/otter-camp/pull/202"),
		GitHubState:        "open",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	prReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues?org_id="+orgID+"&project_id="+projectID+"&origin=github&kind=pull_request",
		nil,
	)
	prRec := httptest.NewRecorder()
	router.ServeHTTP(prRec, prReq)
	require.Equal(t, http.StatusOK, prRec.Code)

	var prResp issueListResponse
	require.NoError(t, json.NewDecoder(prRec.Body).Decode(&prResp))
	require.Len(t, prResp.Items, 1)
	require.Equal(t, githubPR.ID, prResp.Items[0].ID)
	require.Equal(t, "pull_request", prResp.Items[0].Kind)

	issueReq := httptest.NewRequest(
		http.MethodGet,
		"/api/issues?org_id="+orgID+"&project_id="+projectID+"&origin=github&kind=issue",
		nil,
	)
	issueRec := httptest.NewRecorder()
	router.ServeHTTP(issueRec, issueReq)
	require.Equal(t, http.StatusOK, issueRec.Code)

	var issueResp issueListResponse
	require.NoError(t, json.NewDecoder(issueRec.Body).Decode(&issueResp))
	require.Len(t, issueResp.Items, 1)
	require.Equal(t, githubIssue.ID, issueResp.Items[0].ID)
	require.Equal(t, "issue", issueResp.Items[0].Kind)
}

func TestIssuesHandlerCreateLinkedIssueValidatesPathAndApprovalState(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-link-org")
	projectID := insertProjectTestProject(t, db, orgID, "Linked Issue Project")

	handler := &IssuesHandler{
		IssueStore:   store.NewProjectIssueStore(db),
		ProjectStore: store.NewProjectStore(db),
	}
	router := newIssueTestRouter(handler)

	invalidPathReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/link?org_id="+orgID,
		bytes.NewReader([]byte(`{"document_path":"/notes/not-a-post.md","title":"Bad path"}`)),
	)
	invalidPathRec := httptest.NewRecorder()
	router.ServeHTTP(invalidPathRec, invalidPathReq)
	require.Equal(t, http.StatusBadRequest, invalidPathRec.Code)

	invalidStateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/link?org_id="+orgID,
		bytes.NewReader([]byte(`{"document_path":"/posts/2026-02-06-launch-plan.md","approval_state":"invalid"}`)),
	)
	invalidStateRec := httptest.NewRecorder()
	router.ServeHTTP(invalidStateRec, invalidStateReq)
	require.Equal(t, http.StatusBadRequest, invalidStateRec.Code)

	validReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/issues/link?org_id="+orgID,
		bytes.NewReader([]byte(`{"document_path":"posts/2026-02-06-launch-plan.md","approval_state":"ready_for_review"}`)),
	)
	validRec := httptest.NewRecorder()
	router.ServeHTTP(validRec, validReq)
	require.Equal(t, http.StatusCreated, validRec.Code)

	var issue issueSummaryPayload
	require.NoError(t, json.NewDecoder(validRec.Body).Decode(&issue))
	require.NotEmpty(t, issue.ID)
	require.NotNil(t, issue.DocumentPath)
	require.Equal(t, "/posts/2026-02-06-launch-plan.md", *issue.DocumentPath)
	require.Equal(t, store.IssueApprovalStateReadyForReview, issue.ApprovalState)
	require.Equal(t, "Review: 2026 02 06 launch plan", issue.Title)
}

func TestIssuesHandlerGetIncludesLinkedDocumentContent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-linked-doc-org")
	projectID := insertProjectTestProject(t, db, orgID, "Linked Doc Project")

	root := t.TempDir()
	t.Setenv("OTTER_CONTENT_ROOT", root)
	documentPath := "/posts/2026-02-06-launch-plan.md"
	absolute := filepath.Join(root, projectID, "posts", "2026-02-06-launch-plan.md")
	writeProjectContentTestFile(t, absolute, "# Linked post\n\nBody text", time.Now().UTC())

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Linked post review",
		Origin:        "local",
		DocumentPath:  &documentPath,
		ApprovalState: store.IssueApprovalStateDraft,
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.NotNil(t, detail.Issue.DocumentPath)
	require.Equal(t, documentPath, *detail.Issue.DocumentPath)
	require.NotNil(t, detail.Issue.DocumentContent)
	require.Equal(t, "# Linked post\n\nBody text", *detail.Issue.DocumentContent)
	require.Equal(t, store.IssueApprovalStateDraft, detail.Issue.ApprovalState)
}

func TestIssuesHandlerTransitionApprovalStateEnforcesStateMachineAndEmitsActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-transition-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Transition API Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Transition API issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore: issueStore,
		DB:         db,
	}
	router := newIssueTestRouter(handler)

	invalidTransitionReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approval-state?org_id="+orgID,
		bytes.NewReader([]byte(`{"approval_state":"approved"}`)),
	)
	invalidTransitionRec := httptest.NewRecorder()
	router.ServeHTTP(invalidTransitionRec, invalidTransitionReq)
	require.Equal(t, http.StatusBadRequest, invalidTransitionRec.Code)

	validTransitionReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approval-state?org_id="+orgID,
		bytes.NewReader([]byte(`{"approval_state":"ready_for_review"}`)),
	)
	validTransitionRec := httptest.NewRecorder()
	router.ServeHTTP(validTransitionRec, validTransitionReq)
	require.Equal(t, http.StatusOK, validTransitionRec.Code)

	var updated issueSummaryPayload
	require.NoError(t, json.NewDecoder(validTransitionRec.Body).Decode(&updated))
	require.Equal(t, store.IssueApprovalStateReadyForReview, updated.ApprovalState)

	var activityCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'issue.approval_state_changed'`,
		orgID,
		projectID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 1, activityCount)

	var fromState string
	var toState string
	err = db.QueryRow(
		`SELECT metadata->>'from_state', metadata->>'to_state'
			FROM activity_log
			WHERE org_id = $1 AND project_id = $2 AND action = 'issue.approval_state_changed'
			ORDER BY created_at DESC
			LIMIT 1`,
		orgID,
		projectID,
	).Scan(&fromState, &toState)
	require.NoError(t, err)
	require.Equal(t, store.IssueApprovalStateDraft, fromState)
	require.Equal(t, store.IssueApprovalStateReadyForReview, toState)
}

func TestIssuesHandlerApproveRequiresReadyForReviewAndEmitsCompletionActivity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-approve-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Approve API Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Approve me",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore: issueStore,
		DB:         db,
	}
	router := newIssueTestRouter(handler)

	earlyApproveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	earlyApproveRec := httptest.NewRecorder()
	router.ServeHTTP(earlyApproveRec, earlyApproveReq)
	require.Equal(t, http.StatusBadRequest, earlyApproveRec.Code)

	moveReadyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approval-state?org_id="+orgID,
		bytes.NewReader([]byte(`{"approval_state":"ready_for_review"}`)),
	)
	moveReadyRec := httptest.NewRecorder()
	router.ServeHTTP(moveReadyRec, moveReadyReq)
	require.Equal(t, http.StatusOK, moveReadyRec.Code)

	approveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	approveRec := httptest.NewRecorder()
	router.ServeHTTP(approveRec, approveReq)
	require.Equal(t, http.StatusOK, approveRec.Code)

	var approved issueSummaryPayload
	require.NoError(t, json.NewDecoder(approveRec.Body).Decode(&approved))
	require.Equal(t, store.IssueApprovalStateApproved, approved.ApprovalState)
	require.Equal(t, "closed", approved.State)

	var approvedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'issue.approved'`,
		orgID,
		projectID,
	).Scan(&approvedCount)
	require.NoError(t, err)
	require.Equal(t, 1, approvedCount)

	var loggedIssueState string
	err = db.QueryRow(
		`SELECT metadata->>'issue_state'
			FROM activity_log
			WHERE org_id = $1 AND project_id = $2 AND action = 'issue.approved'
			ORDER BY created_at DESC
			LIMIT 1`,
		orgID,
		projectID,
	).Scan(&loggedIssueState)
	require.NoError(t, err)
	require.Equal(t, "closed", loggedIssueState)
}

func issueTestStringPtr(v string) *string {
	return &v
}
