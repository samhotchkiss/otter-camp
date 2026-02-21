package api

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func pipelineStaffingTestRouter(handler *PipelineStaffingHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/pipeline-staffing", handler.Apply)
	return r
}

func TestPipelineStaffingHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-staffing-org")
	projectID := insertPipelineStepsTestProject(t, db, orgID, "Pipeline Staffing Project")
	agentOneID := insertPipelineStepsTestAgent(t, db, orgID, "staffing-a1")
	agentTwoID := insertPipelineStepsTestAgent(t, db, orgID, "staffing-a2")

	ctx := issueTestCtx(orgID)
	stepStore := store.NewPipelineStepStore(db)
	stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  1,
		Name:        "Draft",
		StepType:    store.PipelineStepTypeAgentWork,
		AutoAdvance: true,
	})
	require.NoError(t, err)
	stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  2,
		Name:        "Review",
		StepType:    store.PipelineStepTypeAgentReview,
		AutoAdvance: true,
	})
	require.NoError(t, err)

	handler := &PipelineStaffingHandler{Store: stepStore, DB: db}
	router := pipelineStaffingTestRouter(handler)

	body := []byte(`{"assignments":[{"step_id":"` + stepOne.ID + `","agent_id":"` + agentOneID + `"},{"step_id":"` + stepTwo.ID + `","agent_id":"` + agentTwoID + `"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-staffing?org_id="+orgID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), agentOneID)
	require.Contains(t, rec.Body.String(), agentTwoID)

	updatedSteps, err := stepStore.ListStepsByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, updatedSteps, 2)
	require.NotNil(t, updatedSteps[0].AssignedAgentID)
	require.Equal(t, agentOneID, *updatedSteps[0].AssignedAgentID)
	require.NotNil(t, updatedSteps[1].AssignedAgentID)
	require.Equal(t, agentTwoID, *updatedSteps[1].AssignedAgentID)

	var activityCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND action = 'pipeline.staffing_plan_applied'`,
		orgID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 1, activityCount)
}

func TestPipelineStaffingHandlerAtomicity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-staffing-atomic-org")
	projectID := insertPipelineStepsTestProject(t, db, orgID, "Pipeline Staffing Atomic")
	agentID := insertPipelineStepsTestAgent(t, db, orgID, "staffing-atomic-agent")
	otherOrgID := insertMessageTestOrganization(t, db, "pipeline-staffing-atomic-other-org")
	otherOrgAgentID := insertPipelineStepsTestAgent(t, db, otherOrgID, "staffing-atomic-outside")

	ctx := issueTestCtx(orgID)
	stepStore := store.NewPipelineStepStore(db)
	stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  1,
		Name:        "Draft",
		StepType:    store.PipelineStepTypeAgentWork,
		AutoAdvance: true,
	})
	require.NoError(t, err)
	stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  2,
		Name:        "Review",
		StepType:    store.PipelineStepTypeAgentReview,
		AutoAdvance: true,
	})
	require.NoError(t, err)

	handler := &PipelineStaffingHandler{Store: stepStore, DB: db}
	router := pipelineStaffingTestRouter(handler)

	body := []byte(`{"assignments":[{"step_id":"` + stepOne.ID + `","agent_id":"` + agentID + `"},{"step_id":"` + stepTwo.ID + `","agent_id":"` + otherOrgAgentID + `"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-staffing?org_id="+orgID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	stepsAfter, err := stepStore.ListStepsByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, stepsAfter, 2)
	require.Nil(t, stepsAfter[0].AssignedAgentID)
	require.Nil(t, stepsAfter[1].AssignedAgentID)

	var activityCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND action = 'pipeline.staffing_plan_applied'`,
		orgID,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.Equal(t, 0, activityCount)
}

func TestPipelineStaffingHandlerIsolation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "pipeline-staffing-iso-org-a")
	orgB := insertMessageTestOrganization(t, db, "pipeline-staffing-iso-org-b")
	projectA := insertPipelineStepsTestProject(t, db, orgA, "Pipeline Staffing Isolation")
	agentB := insertPipelineStepsTestAgent(t, db, orgB, "pipeline-staffing-iso-agent-b")

	stepStore := store.NewPipelineStepStore(db)
	_, err := stepStore.CreateStep(issueTestCtx(orgA), store.CreatePipelineStepInput{
		ProjectID:   projectA,
		StepNumber:  1,
		Name:        "Draft",
		StepType:    store.PipelineStepTypeAgentWork,
		AutoAdvance: true,
	})
	require.NoError(t, err)

	var stepID string
	err = db.QueryRow(`SELECT id FROM pipeline_steps WHERE project_id = $1 LIMIT 1`, projectA).Scan(&stepID)
	require.NoError(t, err)

	handler := &PipelineStaffingHandler{Store: stepStore, DB: db}
	router := pipelineStaffingTestRouter(handler)

	body := []byte(`{"assignments":[{"step_id":"` + stepID + `","agent_id":"` + agentB + `"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectA+"/pipeline-staffing?org_id="+orgB, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	stepsOrgA, err := stepStore.ListStepsByProject(issueTestCtx(orgA), projectA)
	require.NoError(t, err)
	require.Len(t, stepsOrgA, 1)
	require.Nil(t, stepsOrgA[0].AssignedAgentID)

	var activityCount sql.NullInt64
	err = db.QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND action = 'pipeline.staffing_plan_applied'`,
		orgB,
	).Scan(&activityCount)
	require.NoError(t, err)
	require.True(t, activityCount.Valid)
	require.EqualValues(t, 0, activityCount.Int64)
}
