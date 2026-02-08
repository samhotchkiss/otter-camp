package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuestionnaireStoreCreateListRespond(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "questionnaire-store-org")
	projectID := createQuestionnaireTestProject(t, db, orgID, "Questionnaire Project")
	issueID := createQuestionnaireTestIssue(t, db, orgID, projectID, "Questionnaire Issue")

	store := NewQuestionnaireStore(db)
	ctx := ctxWithWorkspace(orgID)

	issueQuestions := json.RawMessage(`[
		{"id":"q1","text":"Protocol?","type":"select","options":["WebSocket","Polling"],"required":true},
		{"id":"q2","text":"Offline support?","type":"boolean","required":true}
	]`)
	issueRecord, err := store.Create(ctx, CreateQuestionnaireInput{
		ContextType: QuestionnaireContextIssue,
		ContextID:   issueID,
		Author:      "Planner",
		Title:       stringPointer("Issue clarifications"),
		Questions:   issueQuestions,
	})
	require.NoError(t, err)
	require.NotEmpty(t, issueRecord.ID)
	require.Equal(t, QuestionnaireContextIssue, issueRecord.ContextType)
	require.Equal(t, issueID, issueRecord.ContextID)
	require.Equal(t, "Planner", issueRecord.Author)
	require.Nil(t, issueRecord.Responses)
	require.Nil(t, issueRecord.RespondedBy)
	require.Nil(t, issueRecord.RespondedAt)

	projectQuestions := json.RawMessage(`[
		{"id":"q1","text":"Target launch date?","type":"date","required":true}
	]`)
	projectRecord, err := store.Create(ctx, CreateQuestionnaireInput{
		ContextType: QuestionnaireContextProjectChat,
		ContextID:   projectID,
		Author:      "Planner",
		Questions:   projectQuestions,
	})
	require.NoError(t, err)
	require.NotEmpty(t, projectRecord.ID)
	require.Equal(t, QuestionnaireContextProjectChat, projectRecord.ContextType)

	issueList, err := store.ListByContext(ctx, QuestionnaireContextIssue, issueID)
	require.NoError(t, err)
	require.Len(t, issueList, 1)
	require.Equal(t, issueRecord.ID, issueList[0].ID)

	projectList, err := store.ListByContext(ctx, QuestionnaireContextProjectChat, projectID)
	require.NoError(t, err)
	require.Len(t, projectList, 1)
	require.Equal(t, projectRecord.ID, projectList[0].ID)

	responded, err := store.Respond(ctx, RespondQuestionnaireInput{
		QuestionnaireID: issueRecord.ID,
		RespondedBy:     "Sam",
		Responses:       json.RawMessage(`{"q1":"WebSocket","q2":true}`),
	})
	require.NoError(t, err)
	require.NotNil(t, responded.Responses)
	require.NotNil(t, responded.RespondedBy)
	require.Equal(t, "Sam", *responded.RespondedBy)
	require.NotNil(t, responded.RespondedAt)

	_, err = store.Respond(ctx, RespondQuestionnaireInput{
		QuestionnaireID: issueRecord.ID,
		RespondedBy:     "Another",
		Responses:       json.RawMessage(`{"q1":"Polling","q2":false}`),
	})
	require.ErrorIs(t, err, ErrQuestionnaireAlreadyResponded)

	loaded, err := store.GetByID(ctx, issueRecord.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.RespondedBy)
	require.Equal(t, "Sam", *loaded.RespondedBy)
}

func TestQuestionnaireStoreRejectsCrossOrgAndInvalidContext(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "questionnaire-store-iso-a")
	orgB := createTestOrganization(t, db, "questionnaire-store-iso-b")
	projectA := createQuestionnaireTestProject(t, db, orgA, "Project A")
	issueA := createQuestionnaireTestIssue(t, db, orgA, projectA, "Issue A")

	store := NewQuestionnaireStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	created, err := store.Create(ctxA, CreateQuestionnaireInput{
		ContextType: QuestionnaireContextIssue,
		ContextID:   issueA,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Question","type":"text","required":true}]`),
	})
	require.NoError(t, err)

	_, err = store.Create(ctxB, CreateQuestionnaireInput{
		ContextType: QuestionnaireContextIssue,
		ContextID:   issueA,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Question","type":"text","required":true}]`),
	})
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.ListByContext(ctxB, QuestionnaireContextIssue, issueA)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.GetByID(ctxB, created.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.Respond(ctxB, RespondQuestionnaireInput{
		QuestionnaireID: created.ID,
		RespondedBy:     "Other",
		Responses:       json.RawMessage(`{"q1":"Answer"}`),
	})
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.Create(ctxA, CreateQuestionnaireInput{
		ContextType: "invalid",
		ContextID:   issueA,
		Author:      "Planner",
		Questions:   json.RawMessage(`[{"id":"q1","text":"Question","type":"text","required":true}]`),
	})
	require.Error(t, err)
}

func createQuestionnaireTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func createQuestionnaireTestIssue(t *testing.T, db *sql.DB, orgID, projectID, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, 1, $3, 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func stringPointer(value string) *string {
	return &value
}
