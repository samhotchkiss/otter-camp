package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func issuePipelineActionsTestRouter(handler *IssuePipelineActionsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/pipeline/step-complete", handler.StepComplete)
	r.With(middleware.OptionalWorkspace).Post("/api/issues/{id}/pipeline/step-reject", handler.StepReject)
	r.With(middleware.OptionalWorkspace).Get("/api/issues/{id}/pipeline/status", handler.Status)
	return r
}

func TestIssuePipelineActionsHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issue-pipeline-actions-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Pipeline Actions")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-agent")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	progression := &IssuePipelineProgressionService{
		PipelineStepStore: stepStore,
		IssueStore:        issueStore,
		Now: func() time.Time {
			return time.Date(2026, 2, 20, 9, 0, 0, 0, time.UTC)
		},
	}
	handler := &IssuePipelineActionsHandler{
		IssueStore:         issueStore,
		PipelineStepStore:  stepStore,
		ProgressionService: progression,
	}
	router := issuePipelineActionsTestRouter(handler)

	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Pipeline action issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	invalidRejectIssue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Invalid reject issue",
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
	stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      2,
		Name:            "Review",
		AssignedAgentID: &agentID,
		StepType:        store.PipelineStepTypeAgentReview,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 20, 8, 45, 0, 0, time.UTC)
	require.NoError(t, stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
		IssueID:               issue.ID,
		CurrentPipelineStepID: &stepOne.ID,
		PipelineStartedAt:     &startedAt,
	}))

	completeReq := httptest.NewRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pipeline/step-complete?org_id="+orgID, bytes.NewReader([]byte(`{"notes":"Draft complete"}`)))
	completeReq.Header.Set("Content-Type", "application/json")
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, completeReq)
	require.Equal(t, http.StatusOK, completeRec.Code)
	require.Contains(t, completeRec.Body.String(), stepTwo.ID)
	require.Contains(t, completeRec.Body.String(), `"completed_pipeline":false`)

	rejectReq := httptest.NewRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pipeline/step-reject?org_id="+orgID, bytes.NewReader([]byte(`{"reason":"Needs more detail"}`)))
	rejectReq.Header.Set("Content-Type", "application/json")
	rejectRec := httptest.NewRecorder()
	router.ServeHTTP(rejectRec, rejectReq)
	require.Equal(t, http.StatusOK, rejectRec.Code)
	require.Contains(t, rejectRec.Body.String(), stepOne.ID)

	stateAfterReject, err := stepStore.GetIssuePipelineState(ctx, issue.ID)
	require.NoError(t, err)
	require.NotNil(t, stateAfterReject.CurrentPipelineStepID)
	require.Equal(t, stepOne.ID, *stateAfterReject.CurrentPipelineStepID)

	invalidReasonReq := httptest.NewRequest(http.MethodPost, "/api/issues/"+issue.ID+"/pipeline/step-reject?org_id="+orgID, bytes.NewReader([]byte(`{"reason":" "}`)))
	invalidReasonReq.Header.Set("Content-Type", "application/json")
	invalidReasonRec := httptest.NewRecorder()
	router.ServeHTTP(invalidReasonRec, invalidReasonReq)
	require.Equal(t, http.StatusBadRequest, invalidReasonRec.Code)

	rejectWithoutCurrentReq := httptest.NewRequest(http.MethodPost, "/api/issues/"+invalidRejectIssue.ID+"/pipeline/step-reject?org_id="+orgID, bytes.NewReader([]byte(`{"reason":"no active step"}`)))
	rejectWithoutCurrentReq.Header.Set("Content-Type", "application/json")
	rejectWithoutCurrentRec := httptest.NewRecorder()
	router.ServeHTTP(rejectWithoutCurrentRec, rejectWithoutCurrentReq)
	require.Equal(t, http.StatusBadRequest, rejectWithoutCurrentRec.Code)

	otherOrgID := insertMessageTestOrganization(t, db, "issue-pipeline-actions-other-org")
	otherProjectID := insertProjectTestProject(t, db, otherOrgID, "Other Org Pipeline Actions")
	otherIssue, err := issueStore.CreateIssue(issueTestCtx(otherOrgID), store.CreateProjectIssueInput{
		ProjectID: otherProjectID,
		Title:     "Other org issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	crossOrgReq := httptest.NewRequest(http.MethodPost, "/api/issues/"+otherIssue.ID+"/pipeline/step-complete?org_id="+orgID, bytes.NewReader([]byte(`{"notes":"cross org"}`)))
	crossOrgReq.Header.Set("Content-Type", "application/json")
	crossOrgRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgRec, crossOrgReq)
	require.Equal(t, http.StatusNotFound, crossOrgRec.Code)
}

func TestIssuePipelineStatusHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issue-pipeline-status-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Pipeline Status")
	agentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-status-agent")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	handler := &IssuePipelineActionsHandler{
		IssueStore:        issueStore,
		PipelineStepStore: stepStore,
		ProgressionService: &IssuePipelineProgressionService{
			PipelineStepStore: stepStore,
			IssueStore:        issueStore,
		},
	}
	router := issuePipelineActionsTestRouter(handler)

	issue, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Pipeline status issue",
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
	stepTwo, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      2,
		Name:            "Review",
		AssignedAgentID: &agentID,
		StepType:        store.PipelineStepTypeAgentReview,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	require.NoError(t, stepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
		IssueID:               issue.ID,
		CurrentPipelineStepID: &stepTwo.ID,
		PipelineStartedAt:     &startedAt,
	}))
	completedAt := startedAt.Add(30 * time.Minute)
	_, err = stepStore.AppendIssuePipelineHistory(ctx, store.CreateIssuePipelineHistoryInput{
		IssueID:     issue.ID,
		StepID:      stepOne.ID,
		AgentID:     &agentID,
		StartedAt:   startedAt,
		CompletedAt: &completedAt,
		Result:      store.IssuePipelineResultCompleted,
		Notes:       "Draft done",
	})
	require.NoError(t, err)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"/pipeline/status?org_id="+orgID, nil)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	require.Equal(t, http.StatusOK, statusRec.Code)
	require.Contains(t, statusRec.Body.String(), `"issue":{"id":"`+issue.ID+`"`)
	require.Contains(t, statusRec.Body.String(), `"current_step_id":"`+stepTwo.ID+`"`)
	require.Contains(t, statusRec.Body.String(), `"history":[`)
	require.Contains(t, statusRec.Body.String(), `"steps":[`)

	invalidIDReq := httptest.NewRequest(http.MethodGet, "/api/issues/not-a-uuid/pipeline/status?org_id="+orgID, nil)
	invalidIDRec := httptest.NewRecorder()
	router.ServeHTTP(invalidIDRec, invalidIDReq)
	require.Equal(t, http.StatusBadRequest, invalidIDRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issue.ID+"/pipeline/status", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusBadRequest, missingWorkspaceRec.Code)
}
