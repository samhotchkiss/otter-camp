package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newQuestionnaireTestRouter(handler *QuestionnaireHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/questionnaire", handler.CreateIssueQuestionnaire)
	router.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/chat/questionnaire", handler.CreateProjectChatQuestionnaire)
	router.With(middleware.OptionalWorkspace).Post("/api/questionnaires/{id}/response", handler.Respond)
	return router
}

func TestQuestionnaireHandlerCreateAndRespond(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "questionnaire-api-org")
	projectID := insertProjectTestProject(t, db, orgID, "Questionnaire API Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Questionnaire API Issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &QuestionnaireHandler{
		QuestionnaireStore: store.NewQuestionnaireStore(db),
	}
	router := newQuestionnaireTestRouter(handler)

	createBody := []byte(`{
		"author":"Planner",
		"title":"Issue clarifications",
		"questions":[
			{"id":"q1","text":"Protocol?","type":"select","options":["WebSocket","Polling"],"required":true},
			{"id":"q2","text":"Offline support?","type":"boolean","required":true}
		]
	}`)
	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader(createBody),
	)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created questionnairePayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "issue", created.ContextType)
	require.Equal(t, issue.ID, created.ContextID)
	require.Len(t, created.Questions, 2)
	require.Nil(t, created.Responses)

	respondBody := []byte(`{
		"responded_by":"Sam",
		"responses":{
			"q1":"WebSocket",
			"q2":true
		}
	}`)
	respondReq := httptest.NewRequest(
		http.MethodPost,
		"/api/questionnaires/"+created.ID+"/response?org_id="+orgID,
		bytes.NewReader(respondBody),
	)
	respondRec := httptest.NewRecorder()
	router.ServeHTTP(respondRec, respondReq)
	require.Equal(t, http.StatusOK, respondRec.Code)

	var responded questionnairePayload
	require.NoError(t, json.NewDecoder(respondRec.Body).Decode(&responded))
	require.NotNil(t, responded.Responses)
	require.Equal(t, "WebSocket", responded.Responses["q1"])
	require.Equal(t, true, responded.Responses["q2"])
	require.NotNil(t, responded.RespondedBy)
	require.Equal(t, "Sam", *responded.RespondedBy)
	require.NotNil(t, responded.RespondedAt)

	duplicateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/questionnaires/"+created.ID+"/response?org_id="+orgID,
		bytes.NewReader(respondBody),
	)
	duplicateRec := httptest.NewRecorder()
	router.ServeHTTP(duplicateRec, duplicateReq)
	require.Equal(t, http.StatusConflict, duplicateRec.Code)
}

func TestQuestionnaireHandlerRejectsInvalidPayload(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "questionnaire-api-invalid-org")
	projectID := insertProjectTestProject(t, db, orgID, "Questionnaire Invalid Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Questionnaire Invalid Issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &QuestionnaireHandler{
		QuestionnaireStore: store.NewQuestionnaireStore(db),
	}
	router := newQuestionnaireTestRouter(handler)

	missingQuestionsReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(`{"author":"Planner","title":"Bad payload"}`)),
	)
	missingQuestionsRec := httptest.NewRecorder()
	router.ServeHTTP(missingQuestionsRec, missingQuestionsReq)
	require.Equal(t, http.StatusBadRequest, missingQuestionsRec.Code)

	invalidSelectReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"author":"Planner",
			"questions":[
				{"id":"q1","text":"Protocol?","type":"select","required":true}
			]
		}`)),
	)
	invalidSelectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidSelectRec, invalidSelectReq)
	require.Equal(t, http.StatusBadRequest, invalidSelectRec.Code)
}

func TestQuestionnaireHandlerRejectsCrossOrgAccess(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "questionnaire-api-iso-a")
	orgB := insertMessageTestOrganization(t, db, "questionnaire-api-iso-b")
	projectID := insertProjectTestProject(t, db, orgA, "Questionnaire Isolation Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgA), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Questionnaire Isolation Issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &QuestionnaireHandler{
		QuestionnaireStore: store.NewQuestionnaireStore(db),
	}
	router := newQuestionnaireTestRouter(handler)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgB,
		bytes.NewReader([]byte(`{
			"author":"Planner",
			"questions":[{"id":"q1","text":"Question","type":"text","required":true}]
		}`)),
	)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, createRec.Code)
}
