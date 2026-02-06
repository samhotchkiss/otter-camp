package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newIssueTestRouter(handler *IssuesHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/issues", handler.List)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}", handler.Get)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/comments", handler.CreateComment)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/participants", handler.AddParticipant)
	router.With(middleware.OptionalWorkspace).Delete("/api/issues/{id}/participants/{agentID}", handler.RemoveParticipant)
	return router
}

func issueTestCtx(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
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
		Origin:    "local",
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
