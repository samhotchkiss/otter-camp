package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/comments", handler.CreateComment)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/approval-state", handler.TransitionApprovalState)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/approve", handler.Approve)
	router.With(middleware.OptionalWorkspace).Patch("/api/issues/{id}", handler.PatchIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/review/save", handler.SaveReview)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/review/address", handler.AddressReview)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/changes", handler.ReviewChanges)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/history", handler.ReviewHistory)
	router.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/review/history/{sha}", handler.ReviewVersion)
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/participants", handler.AddParticipant)
	router.With(middleware.OptionalWorkspace).Delete("/api/issues/{id}/participants/{agentID}", handler.RemoveParticipant)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues", handler.CreateIssue)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/issues/link", handler.CreateLinkedIssue)
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

func TestIssuesHandlerListFiltersByIssueNumber(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-list-number-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Number Filter Project")

	issueStore := store.NewProjectIssueStore(db)
	ctx := issueTestCtx(orgID)
	_, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue One",
		Origin:    "local",
	})
	require.NoError(t, err)
	second, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue Two",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{IssueStore: issueStore}
	router := newIssueTestRouter(handler)

	listReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/issues?org_id=%s&project_id=%s&issue_number=%d", orgID, projectID, second.IssueNumber),
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp issueListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Equal(t, 1, listResp.Total)
	require.Len(t, listResp.Items, 1)
	require.Equal(t, second.ID, listResp.Items[0].ID)
	require.Equal(t, second.IssueNumber, listResp.Items[0].IssueNumber)
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

func TestIssuesHandlerGetIncludesQuestionnaires(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-questionnaire-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Questionnaire Project")

	issueStore := store.NewProjectIssueStore(db)
	questionnaireStore := store.NewQuestionnaireStore(db)
	ctx := issueTestCtx(orgID)

	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Issue with questionnaire",
		Origin:    "local",
	})
	require.NoError(t, err)

	issueQuestionnaire, err := questionnaireStore.Create(ctx, store.CreateQuestionnaireInput{
		ContextType: store.QuestionnaireContextIssue,
		ContextID:   issue.ID,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Protocol?","type":"select","options":["WebSocket","Polling"],"required":true}]`),
	})
	require.NoError(t, err)

	_, err = questionnaireStore.Create(ctx, store.CreateQuestionnaireInput{
		ContextType: store.QuestionnaireContextProjectChat,
		ContextID:   projectID,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Ignore","type":"text","required":false}]`),
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:         issueStore,
		QuestionnaireStore: questionnaireStore,
	}
	router := newIssueTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Len(t, detail.Questionnaires, 1)
	require.Equal(t, issueQuestionnaire.ID, detail.Questionnaires[0].ID)
	require.Equal(t, store.QuestionnaireContextIssue, detail.Questionnaires[0].ContextType)
}

func TestIssuesHandlerQuestionnaireContextIsolation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-questionnaire-iso-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Questionnaire Isolation Project")

	issueStore := store.NewProjectIssueStore(db)
	questionnaireStore := store.NewQuestionnaireStore(db)
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

	_, err = questionnaireStore.Create(ctx, store.CreateQuestionnaireInput{
		ContextType: store.QuestionnaireContextIssue,
		ContextID:   issueA.ID,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Issue A question","type":"text","required":true}]`),
	})
	require.NoError(t, err)
	_, err = questionnaireStore.Create(ctx, store.CreateQuestionnaireInput{
		ContextType: store.QuestionnaireContextIssue,
		ContextID:   issueB.ID,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Issue B question","type":"text","required":true}]`),
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore:         issueStore,
		QuestionnaireStore: questionnaireStore,
	}
	router := newIssueTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issueA.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Len(t, detail.Questionnaires, 1)
	require.Equal(t, issueA.ID, detail.Questionnaires[0].ContextID)
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
	require.Equal(t, http.StatusConflict, invalidTransitionRec.Code)

	invalidPriorityReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/issues/"+issue.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"priority":"P9"}`)),
	)
	invalidPriorityRec := httptest.NewRecorder()
	router.ServeHTTP(invalidPriorityRec, invalidPriorityReq)
	require.Equal(t, http.StatusBadRequest, invalidPriorityRec.Code)
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

func TestIssuesHandlerUnexpectedStoreErrorReturns500(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-store-error-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Store Error Project")

	handler := &IssuesHandler{
		IssueStore: store.NewProjectIssueStore(db),
	}
	router := newIssueTestRouter(handler)

	_, err := db.Exec(`DROP TABLE project_issues`)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/issues?org_id="+orgID+"&project_id="+projectID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "internal server error", payload.Error)
}

func TestHandleIssueStoreErrorUnexpectedReturns500(t *testing.T) {
	rec := httptest.NewRecorder()
	handleIssueStoreError(rec, errors.New(`pq: relation "project_issues" does not exist`))

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "internal server error", payload.Error)
}

func TestHandleIssueStoreErrorTransitionValidationMapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()
	handleIssueStoreError(rec, fmt.Errorf("%w: invalid approval_state transition", store.ErrConflict))

	require.Equal(t, http.StatusConflict, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "invalid state transition", payload.Error)
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
	require.Equal(t, http.StatusConflict, invalidTransitionRec.Code)

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
	require.Equal(t, http.StatusConflict, earlyApproveRec.Code)

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

func TestIssuesHandlerApproveUsesReviewerGateWhenProjectRequiresHumanReview(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-human-review-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Human Review Gate Project")

	_, err := db.Exec(`UPDATE projects SET require_human_review = true WHERE id = $1`, projectID)
	require.NoError(t, err)

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Gate me",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore: issueStore,
		DB:         db,
	}
	router := newIssueTestRouter(handler)

	moveReadyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approval-state?org_id="+orgID,
		bytes.NewReader([]byte(`{"approval_state":"ready_for_review"}`)),
	)
	moveReadyRec := httptest.NewRecorder()
	router.ServeHTTP(moveReadyRec, moveReadyReq)
	require.Equal(t, http.StatusOK, moveReadyRec.Code)

	reviewerApproveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	reviewerApproveRec := httptest.NewRecorder()
	router.ServeHTTP(reviewerApproveRec, reviewerApproveReq)
	require.Equal(t, http.StatusOK, reviewerApproveRec.Code)

	var reviewerApproved issueSummaryPayload
	require.NoError(t, json.NewDecoder(reviewerApproveRec.Body).Decode(&reviewerApproved))
	require.Equal(t, store.IssueApprovalStateApprovedByReviewer, reviewerApproved.ApprovalState)
	require.Equal(t, "open", reviewerApproved.State)

	var approvedCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND project_id = $2 AND action = 'issue.approved'`,
		orgID,
		projectID,
	).Scan(&approvedCount)
	require.NoError(t, err)
	require.Equal(t, 0, approvedCount)

	humanApproveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	humanApproveReq = humanApproveReq.WithContext(context.WithValue(humanApproveReq.Context(), middleware.UserIDKey, "human-reviewer"))
	humanApproveRec := httptest.NewRecorder()
	router.ServeHTTP(humanApproveRec, humanApproveReq)
	require.Equal(t, http.StatusOK, humanApproveRec.Code)

	var finalApproved issueSummaryPayload
	require.NoError(t, json.NewDecoder(humanApproveRec.Body).Decode(&finalApproved))
	require.Equal(t, store.IssueApprovalStateApproved, finalApproved.ApprovalState)
	require.Equal(t, "closed", finalApproved.State)
}

func TestIssuesHandlerApproveReturnsErrorWhenDBNil(t *testing.T) {
	handler := &IssuesHandler{
		IssueStore: store.NewProjectIssueStore(nil),
		DB:         nil,
	}
	router := newIssueTestRouter(handler)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/11111111-1111-1111-1111-111111111111/approve?org_id=22222222-2222-2222-2222-222222222222",
		nil,
	)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		router.ServeHTTP(rec, req)
	})

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "database not available", resp.Error)
}

func TestIssuesHandlerApproveFromDraftWithHumanReviewReturns409(t *testing.T) {
	targetState, statusCode, message := resolveApproveTargetApprovalState(
		store.IssueApprovalStateDraft,
		true,
		"reviewer-actor",
	)

	require.Equal(t, "", targetState)
	require.Equal(t, http.StatusConflict, statusCode)
	require.Equal(t, "issue must be ready_for_review for reviewer approval", message)
}

func TestIssuesHandlerApproveRequiresHumanActorForSecondApproval(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issues-api-human-review-caller-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Human Review Caller Project")

	_, err := db.Exec(`UPDATE projects SET require_human_review = true WHERE id = $1`, projectID)
	require.NoError(t, err)

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Human gate caller check",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &IssuesHandler{
		IssueStore: issueStore,
		DB:         db,
	}
	router := newIssueTestRouter(handler)

	moveReadyReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approval-state?org_id="+orgID,
		bytes.NewReader([]byte(`{"approval_state":"ready_for_review"}`)),
	)
	moveReadyRec := httptest.NewRecorder()
	router.ServeHTTP(moveReadyRec, moveReadyReq)
	require.Equal(t, http.StatusOK, moveReadyRec.Code)

	reviewerApproveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	reviewerApproveRec := httptest.NewRecorder()
	router.ServeHTTP(reviewerApproveRec, reviewerApproveReq)
	require.Equal(t, http.StatusOK, reviewerApproveRec.Code)

	secondApproveReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/approve?org_id="+orgID,
		nil,
	)
	secondApproveRec := httptest.NewRecorder()
	router.ServeHTTP(secondApproveRec, secondApproveReq)
	require.Equal(t, http.StatusForbidden, secondApproveRec.Code)

	var denied errorResponse
	require.NoError(t, json.NewDecoder(secondApproveRec.Body).Decode(&denied))
	require.Equal(t, "human approval required", denied.Error)

	getReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var detail issueDetailPayload
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&detail))
	require.Equal(t, store.IssueApprovalStateApprovedByReviewer, detail.Issue.ApprovalState)
	require.Equal(t, "open", detail.Issue.State)
}

func issueTestStringPtr(v string) *string {
	return &v
}
