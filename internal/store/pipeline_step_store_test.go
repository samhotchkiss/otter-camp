package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createPipelineStepTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
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

func createPipelineStepTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func createPipelineStepTestIssue(t *testing.T, db *sql.DB, orgID, projectID, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, COALESCE((SELECT MAX(issue_number) + 1 FROM project_issues WHERE project_id = $2), 1), $3, 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestPipelineStepStore_CreateListReorderAndValidation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "pipeline-step-org")
	otherOrgID := createTestOrganization(t, db, "pipeline-step-other-org")
	projectID := createPipelineStepTestProject(t, db, orgID, "Pipeline Step Project")
	agentID := createPipelineStepTestAgent(t, db, orgID, "writer")
	otherAgentID := createPipelineStepTestAgent(t, db, otherOrgID, "outside")
	ctx := ctxWithWorkspace(orgID)
	store := NewPipelineStepStore(db)

	_, err := store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      1,
		Name:            "Write draft",
		Description:     "Create first draft",
		AssignedAgentID: &agentID,
		StepType:        PipelineStepTypeAgentWork,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	second, err := store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  2,
		Name:        "Fact check",
		StepType:    PipelineStepTypeAgentReview,
		AutoAdvance: true,
	})
	require.NoError(t, err)

	third, err := store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  3,
		Name:        "Human review",
		StepType:    PipelineStepTypeHumanReview,
		AutoAdvance: false,
	})
	require.NoError(t, err)

	steps, err := store.ListStepsByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, steps, 3)
	require.Equal(t, 1, steps[0].StepNumber)
	require.Equal(t, "Write draft", steps[0].Name)
	require.Equal(t, 2, steps[1].StepNumber)
	require.Equal(t, second.ID, steps[1].ID)
	require.Equal(t, 3, steps[2].StepNumber)
	require.Equal(t, third.ID, steps[2].ID)

	err = store.ReorderSteps(ctx, projectID, []string{third.ID, steps[0].ID, second.ID})
	require.NoError(t, err)

	reordered, err := store.ListStepsByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, reordered, 3)
	require.Equal(t, third.ID, reordered[0].ID)
	require.Equal(t, 1, reordered[0].StepNumber)
	require.Equal(t, steps[0].ID, reordered[1].ID)
	require.Equal(t, 2, reordered[1].StepNumber)
	require.Equal(t, second.ID, reordered[2].ID)
	require.Equal(t, 3, reordered[2].StepNumber)

	_, err = store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  4,
		Name:        "Bad",
		StepType:    "observer",
		AutoAdvance: true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrValidation)

	_, err = store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      4,
		Name:            "Cross-org",
		StepType:        PipelineStepTypeAgentWork,
		AssignedAgentID: &otherAgentID,
		AutoAdvance:     true,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIssuePipelineHistoryStore_AppendAndList(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "pipeline-history-org")
	projectID := createPipelineStepTestProject(t, db, orgID, "Pipeline History Project")
	agentID := createPipelineStepTestAgent(t, db, orgID, "history-agent")
	issueID := createPipelineStepTestIssue(t, db, orgID, projectID, "Pipeline issue")
	ctx := ctxWithWorkspace(orgID)
	store := NewPipelineStepStore(db)

	step, err := store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      1,
		Name:            "Write",
		StepType:        PipelineStepTypeAgentWork,
		AssignedAgentID: &agentID,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	startedAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	completedAt := startedAt.Add(90 * time.Second)
	entry, err := store.AppendIssuePipelineHistory(ctx, CreateIssuePipelineHistoryInput{
		IssueID:     issueID,
		StepID:      step.ID,
		AgentID:     &agentID,
		StartedAt:   startedAt,
		CompletedAt: &completedAt,
		Result:      IssuePipelineResultCompleted,
		Notes:       "Draft completed",
	})
	require.NoError(t, err)
	require.Equal(t, issueID, entry.IssueID)
	require.Equal(t, step.ID, entry.StepID)
	require.Equal(t, IssuePipelineResultCompleted, entry.Result)
	require.Equal(t, "Draft completed", entry.Notes)

	history, err := store.ListIssuePipelineHistory(ctx, issueID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, entry.ID, history[0].ID)
	require.Equal(t, step.ID, history[0].StepID)
}

func TestProjectIssueStorePipelineColumns_UpdateIssuePipelineState(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "pipeline-columns-org")
	projectID := createPipelineStepTestProject(t, db, orgID, "Pipeline Columns Project")
	issueID := createPipelineStepTestIssue(t, db, orgID, projectID, "Pipeline state issue")
	ctx := ctxWithWorkspace(orgID)
	store := NewPipelineStepStore(db)

	step, err := store.CreateStep(ctx, CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  1,
		Name:        "Write",
		StepType:    PipelineStepTypeAgentWork,
		AutoAdvance: true,
	})
	require.NoError(t, err)

	startedAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	completedAt := startedAt.Add(4 * time.Minute)

	err = store.UpdateIssuePipelineState(ctx, UpdateIssuePipelineStateInput{
		IssueID:               issueID,
		CurrentPipelineStepID: &step.ID,
		PipelineStartedAt:     &startedAt,
		PipelineCompletedAt:   &completedAt,
	})
	require.NoError(t, err)

	var gotStepID sql.NullString
	var gotStartedAt sql.NullTime
	var gotCompletedAt sql.NullTime
	err = db.QueryRowContext(
		ctx,
		`SELECT current_pipeline_step_id, pipeline_started_at, pipeline_completed_at
		 FROM project_issues WHERE id = $1`,
		issueID,
	).Scan(&gotStepID, &gotStartedAt, &gotCompletedAt)
	require.NoError(t, err)
	require.True(t, gotStepID.Valid)
	require.Equal(t, step.ID, gotStepID.String)
	require.True(t, gotStartedAt.Valid)
	require.True(t, gotCompletedAt.Valid)
	require.True(t, gotStartedAt.Time.Equal(startedAt))
	require.True(t, gotCompletedAt.Time.Equal(completedAt))
}
