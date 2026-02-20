package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestIssuePipelineEllieContextGate(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "issue-pipeline-ellie-gate-org")
	projectID := insertProjectTestProject(t, db, orgID, "Issue Pipeline Ellie Gate")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "issue-pipeline-ellie-owner")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	projectStore := store.NewProjectStore(db)

	_, err := stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       projectID,
		StepNumber:      1,
		Name:            "Draft",
		AssignedAgentID: &ownerAgentID,
		StepType:        store.PipelineStepTypeAgentWork,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	statusRouter := issuePipelineActionsTestRouter(&IssuePipelineActionsHandler{
		IssueStore:        issueStore,
		PipelineStepStore: stepStore,
	})

	runCreate := func(t *testing.T, title string, trigger func(*store.ProjectIssue) error) (string, *fakeOpenClawDispatcher) {
		t.Helper()
		dispatcher := &fakeOpenClawDispatcher{connected: true}
		handler := &IssuesHandler{
			IssueStore:         issueStore,
			ProjectStore:       projectStore,
			AgentStore:         store.NewAgentStore(db),
			PipelineStepStore:  stepStore,
			DB:                 db,
			OpenClawDispatcher: dispatcher,
			EllieContextTrigger: func(_ context.Context, issue store.ProjectIssue) error {
				if trigger == nil {
					return nil
				}
				return trigger(&issue)
			},
		}
		router := newIssueTestRouter(handler)
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/projects/"+projectID+"/issues?org_id="+orgID,
			bytes.NewReader([]byte(`{"title":"`+title+`","owner_agent_id":"`+ownerAgentID+`"}`)),
		)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)

		var summary issueSummaryPayload
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&summary))
		return summary.ID, dispatcher
	}

	fetchStatus := func(t *testing.T, issueID string) string {
		t.Helper()
		statusReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+issueID+"/pipeline/status?org_id="+orgID, nil)
		statusRec := httptest.NewRecorder()
		statusRouter.ServeHTTP(statusRec, statusReq)
		require.Equal(t, http.StatusOK, statusRec.Code)
		return statusRec.Body.String()
	}

	t.Run("success", func(t *testing.T) {
		issueID, dispatcher := runCreate(t, "Gate success issue", nil)
		require.Len(t, dispatcher.calls, 1)
		require.Contains(t, fetchStatus(t, issueID), `"ellie_context_gate":{"status":"succeeded"`)
	})

	t.Run("failure", func(t *testing.T) {
		issueID, dispatcher := runCreate(t, "Gate failure issue", func(_ *store.ProjectIssue) error {
			return fmt.Errorf("ellie trigger failed")
		})
		require.Len(t, dispatcher.calls, 0)
		statusBody := fetchStatus(t, issueID)
		require.Contains(t, statusBody, `"ellie_context_gate":{"status":"failed"`)
		require.Contains(t, statusBody, `"error":"ellie trigger failed"`)
	})

	t.Run("bypass", func(t *testing.T) {
		t.Setenv("OTTER_PIPELINE_ELLIE_CONTEXT_GATE_BYPASS", "true")
		triggerCalls := 0
		issueID, dispatcher := runCreate(t, "Gate bypass issue", func(_ *store.ProjectIssue) error {
			triggerCalls++
			return fmt.Errorf("should not run")
		})
		require.Zero(t, triggerCalls)
		require.Len(t, dispatcher.calls, 1)
		require.Contains(t, fetchStatus(t, issueID), `"ellie_context_gate":{"status":"bypassed"`)
	})
}

func TestProjectKickoffEllieContextGate(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "project-kickoff-ellie-gate-org")
	pipelineProjectID := insertProjectTestProject(t, db, orgID, "Project Kickoff Pipeline")
	nonPipelineProjectID := insertProjectTestProject(t, db, orgID, "Project Kickoff Non Pipeline")
	ownerAgentID := insertMessageTestAgent(t, db, orgID, "project-kickoff-ellie-owner")
	ctx := issueTestCtx(orgID)

	issueStore := store.NewProjectIssueStore(db)
	stepStore := store.NewPipelineStepStore(db)
	projectStore := store.NewProjectStore(db)

	_, err := db.Exec(`UPDATE projects SET primary_agent_id = $1 WHERE id IN ($2, $3)`, ownerAgentID, pipelineProjectID, nonPipelineProjectID)
	require.NoError(t, err)

	_, err = stepStore.CreateStep(ctx, store.CreatePipelineStepInput{
		ProjectID:       pipelineProjectID,
		StepNumber:      1,
		Name:            "Kickoff",
		AssignedAgentID: &ownerAgentID,
		StepType:        store.PipelineStepTypeAgentWork,
		AutoAdvance:     true,
	})
	require.NoError(t, err)

	dispatcher := &fakeOpenClawDispatcher{connected: true}
	handler := &IssuesHandler{
		IssueStore:         issueStore,
		ProjectStore:       projectStore,
		AgentStore:         store.NewAgentStore(db),
		PipelineStepStore:  stepStore,
		DB:                 db,
		OpenClawDispatcher: dispatcher,
		EllieContextTrigger: func(_ context.Context, _ store.ProjectIssue) error {
			return fmt.Errorf("kickoff ellie unavailable")
		},
	}
	router := newIssueTestRouter(handler)
	statusRouter := issuePipelineActionsTestRouter(&IssuePipelineActionsHandler{
		IssueStore:        issueStore,
		PipelineStepStore: stepStore,
	})

	pipelineReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+pipelineProjectID+"/issues/link?org_id="+orgID,
		bytes.NewReader([]byte(`{"document_path":"/posts/pipeline-kickoff.md","title":"Pipeline kickoff"}`)),
	)
	pipelineRec := httptest.NewRecorder()
	router.ServeHTTP(pipelineRec, pipelineReq)
	require.Equal(t, http.StatusCreated, pipelineRec.Code)

	var pipelineIssue issueSummaryPayload
	require.NoError(t, json.NewDecoder(pipelineRec.Body).Decode(&pipelineIssue))
	require.Len(t, dispatcher.calls, 0)

	pipelineStatusReq := httptest.NewRequest(http.MethodGet, "/api/issues/"+pipelineIssue.ID+"/pipeline/status?org_id="+orgID, nil)
	pipelineStatusRec := httptest.NewRecorder()
	statusRouter.ServeHTTP(pipelineStatusRec, pipelineStatusReq)
	require.Equal(t, http.StatusOK, pipelineStatusRec.Code)
	require.Contains(t, pipelineStatusRec.Body.String(), `"ellie_context_gate":{"status":"failed"`)

	nonPipelineReq := httptest.NewRequest(
		http.MethodPost,
		"/api/projects/"+nonPipelineProjectID+"/issues/link?org_id="+orgID,
		bytes.NewReader([]byte(`{"document_path":"/posts/non-pipeline-kickoff.md","title":"No pipeline kickoff"}`)),
	)
	nonPipelineRec := httptest.NewRecorder()
	router.ServeHTTP(nonPipelineRec, nonPipelineReq)
	require.Equal(t, http.StatusCreated, nonPipelineRec.Code)
	require.Len(t, dispatcher.calls, 1)
}
