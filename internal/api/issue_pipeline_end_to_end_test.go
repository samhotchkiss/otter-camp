package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func pipelineEndToEndTestRouter(staffing *PipelineStaffingHandler, actions *IssuePipelineActionsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/pipeline-staffing", staffing.Apply)
	r.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/pipeline/step-complete", actions.StepComplete)
	r.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/pipeline/step-reject", actions.StepReject)
	r.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/pipeline/status", actions.Status)
	return r
}

func TestIssuePipelineEndToEnd(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issue-pipeline-e2e-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Pipeline E2E")
	writerAgentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-e2e-writer")
	reviewerAgentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-e2e-reviewer")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	progression := &IssuePipelineProgressionService{
		PipelineStepStore: stepStore,
		IssueStore:        issueStore,
		Now: func() time.Time {
			return time.Date(2026, 2, 20, 9, 30, 0, 0, time.UTC)
		},
	}

	staffingHandler := &PipelineStaffingHandler{Store: stepStore, DB: db}
	actionsHandler := &IssuePipelineActionsHandler{
		IssueStore:         issueStore,
		PipelineStepStore:  stepStore,
		ProgressionService: progression,
	}
	router := pipelineEndToEndTestRouter(staffingHandler, actionsHandler)

	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Pipeline E2E issue",
		Origin:    "local",
	})
	require.NoError(t, err)

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
		Name:        "Edit",
		StepType:    store.PipelineStepTypeAgentReview,
		AutoAdvance: true,
	})
	require.NoError(t, err)
	stepThree, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  3,
		Name:        "Human approval",
		StepType:    store.PipelineStepTypeHumanReview,
		AutoAdvance: false,
	})
	require.NoError(t, err)

	staffingReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+projectID+"/pipeline-staffing?org_id="+orgID,
		bytes.NewReader([]byte(`{"assignments":[{"step_id":"`+stepOne.ID+`","agent_id":"`+writerAgentID+`"},{"step_id":"`+stepTwo.ID+`","agent_id":"`+reviewerAgentID+`"}]}`)),
	)
	staffingReq.Header.Set("Content-Type", "application/json")
	staffingRec := httptest.NewRecorder()
	router.ServeHTTP(staffingRec, staffingReq)
	require.Equal(t, http.StatusOK, staffingRec.Code)

	stepsAfterStaffing, err := stepStore.ListStepsByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, stepsAfterStaffing, 3)
	require.NotNil(t, stepsAfterStaffing[0].AssignedAgentID)
	require.Equal(t, writerAgentID, *stepsAfterStaffing[0].AssignedAgentID)
	require.NotNil(t, stepsAfterStaffing[1].AssignedAgentID)
	require.Equal(t, reviewerAgentID, *stepsAfterStaffing[1].AssignedAgentID)

	startedAt := time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC)
	require.NoError(t, stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
		IssueID:               issue.ID,
		CurrentPipelineStepID: &stepOne.ID,
		PipelineStartedAt:     &startedAt,
	}))

	completeOneReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/pipeline/step-complete?org_id="+orgID,
		bytes.NewReader([]byte(`{"notes":"Draft complete"}`)),
	)
	completeOneReq.Header.Set("Content-Type", "application/json")
	completeOneRec := httptest.NewRecorder()
	router.ServeHTTP(completeOneRec, completeOneReq)
	require.Equal(t, http.StatusOK, completeOneRec.Code)
	require.Contains(t, completeOneRec.Body.String(), `"current_pipeline_step_id":"`+stepTwo.ID+`"`)

	completeTwoReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/pipeline/step-complete?org_id="+orgID,
		bytes.NewReader([]byte(`{"notes":"Edit complete"}`)),
	)
	completeTwoReq.Header.Set("Content-Type", "application/json")
	completeTwoRec := httptest.NewRecorder()
	router.ServeHTTP(completeTwoRec, completeTwoReq)
	require.Equal(t, http.StatusOK, completeTwoRec.Code)
	require.Contains(t, completeTwoRec.Body.String(), `"current_pipeline_step_id":"`+stepThree.ID+`"`)
	require.Contains(t, completeTwoRec.Body.String(), `"parked_for_human_review":true`)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"/pipeline/status?org_id="+orgID, nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	require.Equal(t, http.StatusOK, statusRec.Code)

	var status issuePipelineStatusResponse
	require.NoError(t, json.NewDecoder(statusRec.Body).Decode(&status))
	require.NotNil(t, status.Pipeline.CurrentStepID)
	require.Equal(t, stepThree.ID, *status.Pipeline.CurrentStepID)
	require.Len(t, status.Pipeline.History, 2)
	require.Equal(t, stepOne.ID, status.Pipeline.History[0].StepID)
	require.Equal(t, stepTwo.ID, status.Pipeline.History[1].StepID)

	issueAfter, err := issueStore.GetIssueByID(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, store.IssueWorkStatusReview, issueAfter.WorkStatus)
}

func TestIssuePipelineRejectBackflow(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issue-pipeline-reject-backflow-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Pipeline Reject Backflow")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-reject-backflow-agent")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	progression := &IssuePipelineProgressionService{
		PipelineStepStore: stepStore,
		IssueStore:        issueStore,
		Now: func() time.Time {
			return time.Date(2026, 2, 20, 11, 0, 0, 0, time.UTC)
		},
	}

	router := pipelineEndToEndTestRouter(
		&PipelineStaffingHandler{Store: stepStore, DB: db},
		&IssuePipelineActionsHandler{
			IssueStore:         issueStore,
			PipelineStepStore:  stepStore,
			ProgressionService: progression,
		},
	)

	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Pipeline reject issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	stepOne, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      1,
		Name:            "Draft",
		AssignedAgentID: &agentID,
		StepType:        store.PipelineStepTypeAgentWork,
		AutoAdvance:     true,
	})
	require.NoError(t, err)
	_, err = stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:   projectID,
		StepNumber:  2,
		Name:        "Human review",
		StepType:    store.PipelineStepTypeHumanReview,
		AutoAdvance: false,
	})
	require.NoError(t, err)
	stepThree, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      3,
		Name:            "Finalize",
		AssignedAgentID: &agentID,
		StepType:        store.PipelineStepTypeAgentReview,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	require.NoError(t, stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
		IssueID:               issue.ID,
		CurrentPipelineStepID: &stepThree.ID,
		PipelineStartedAt:     &startedAt,
	}))

	rejectReq := httptest.NewRequest(
		http.MethodPost,
		"/api/issues/"+issue.ID+"/pipeline/step-reject?org_id="+orgID,
		bytes.NewReader([]byte(`{"reason":"Needs source links"}`)),
	)
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectRec := httptest.NewRecorder()
	router.ServeHTTP(rejectRec, rejectReq)
	require.Equal(t, http.StatusOK, rejectRec.Code)
	require.Contains(t, rejectRec.Body.String(), `"current_pipeline_step_id":"`+stepOne.ID+`"`)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"/pipeline/status?org_id="+orgID, nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	require.Equal(t, http.StatusOK, statusRec.Code)

	var status issuePipelineStatusResponse
	require.NoError(t, json.NewDecoder(statusRec.Body).Decode(&status))
	require.NotNil(t, status.Pipeline.CurrentStepID)
	require.Equal(t, stepOne.ID, *status.Pipeline.CurrentStepID)
	require.Len(t, status.Pipeline.History, 1)
	require.Equal(t, stepThree.ID, status.Pipeline.History[0].StepID)
	require.Equal(t, store.IssuePipelineResultRejected, status.Pipeline.History[0].Result)
	require.Equal(t, "Needs source links", status.Pipeline.History[0].Notes)

	issueAfter, err := issueStore.GetIssueByID(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, store.IssueWorkStatusInProgress, issueAfter.WorkStatus)
}
