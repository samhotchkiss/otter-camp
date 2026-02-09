package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestQuestionnaireHandlerRejectsEmptyAuthorAndRespondedBy(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "questionnaire-api-validation-org")
	projectID := insertProjectTestProject(t, db, orgID, "Questionnaire Validation Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Questionnaire Validation Issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &QuestionnaireHandler{
		QuestionnaireStore: store.NewQuestionnaireStore(db),
	}
	router := newQuestionnaireTestRouter(handler)

	emptyAuthorReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"author":"   ",
			"questions":[{"id":"q1","text":"Protocol?","type":"text","required":true}]
		}`)),
	)
	emptyAuthorRec := httptest.NewRecorder()
	router.ServeHTTP(emptyAuthorRec, emptyAuthorReq)
	require.Equal(t, http.StatusBadRequest, emptyAuthorRec.Code)

	validCreateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"author":"Planner",
			"questions":[{"id":"q1","text":"Protocol?","type":"text","required":true}]
		}`)),
	)
	validCreateRec := httptest.NewRecorder()
	router.ServeHTTP(validCreateRec, validCreateReq)
	require.Equal(t, http.StatusCreated, validCreateRec.Code)

	var created questionnairePayload
	require.NoError(t, json.NewDecoder(validCreateRec.Body).Decode(&created))

	emptyRespondedByReq := httptest.NewRequest(
		http.MethodPost,
		"/api/questionnaires/"+created.ID+"/response?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"responded_by":"   ",
			"responses":{"q1":"WebSocket"}
		}`)),
	)
	emptyRespondedByRec := httptest.NewRecorder()
	router.ServeHTTP(emptyRespondedByRec, emptyRespondedByReq)
	require.Equal(t, http.StatusBadRequest, emptyRespondedByRec.Code)
}

func TestQuestionnaireHandlerRejectsExcessiveQuestionAndOptionCounts(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "questionnaire-api-limits-org")
	projectID := insertProjectTestProject(t, db, orgID, "Questionnaire Limits Project")

	issueStore := store.NewProjectIssueStore(db)
	issue, err := issueStore.CreateIssue(issueTestCtx(orgID), store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Questionnaire Limits Issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	handler := &QuestionnaireHandler{
		QuestionnaireStore: store.NewQuestionnaireStore(db),
	}
	router := newQuestionnaireTestRouter(handler)

	questionEntries := make([]string, 0, 101)
	for i := 1; i <= 101; i++ {
		questionEntries = append(
			questionEntries,
			fmt.Sprintf(`{"id":"q%d","text":"Question","type":"text","required":true}`, i),
		)
	}
	tooManyQuestionsBody := `{"author":"Planner","questions":[` + strings.Join(questionEntries, ",") + `]}`
	tooManyQuestionsReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(tooManyQuestionsBody)),
	)
	tooManyQuestionsRec := httptest.NewRecorder()
	router.ServeHTTP(tooManyQuestionsRec, tooManyQuestionsReq)
	require.Equal(t, http.StatusBadRequest, tooManyQuestionsRec.Code)

	optionEntries := make([]string, 0, 201)
	for i := 1; i <= 201; i++ {
		optionEntries = append(optionEntries, fmt.Sprintf(`"option %d"`, i))
	}
	tooManyOptionsBody := `{"author":"Planner","questions":[{"id":"q1","text":"Pick one","type":"select","required":true,"options":[` + strings.Join(optionEntries, ",") + `]}]}`
	tooManyOptionsReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/questionnaire?org_id="+orgID,
		bytes.NewReader([]byte(tooManyOptionsBody)),
	)
	tooManyOptionsRec := httptest.NewRecorder()
	router.ServeHTTP(tooManyOptionsRec, tooManyOptionsReq)
	require.Equal(t, http.StatusBadRequest, tooManyOptionsRec.Code)
}

func TestHandleQuestionnaireStoreErrorRedactsUnexpectedErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	handleQuestionnaireStoreError(rec, errors.New("sensitive internal detail"))
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "internal error", payload.Error)
}
