package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func projectStrPtr(value string) *string {
	return &value
}

func insertProjectTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id",
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertProjectTestTask(t *testing.T, db *sql.DB, orgID string, projectID *string, status string) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO tasks (org_id, project_id, title, status, priority) VALUES ($1, $2, $3, $4, 'P2')",
		orgID,
		projectID,
		"Task for "+status,
		status,
	)
	require.NoError(t, err)
}

func TestProjectsHandlerListIncludesTaskCounts(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-org")

	projectOne := insertProjectTestProject(t, db, orgID, "Project One")
	projectTwo := insertProjectTestProject(t, db, orgID, "Project Two")

	insertProjectTestTask(t, db, orgID, &projectOne, "done")
	insertProjectTestTask(t, db, orgID, &projectOne, "done")
	insertProjectTestTask(t, db, orgID, &projectOne, "queued")
	insertProjectTestTask(t, db, orgID, nil, "done")

	handler := &ProjectsHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Projects []struct {
			ID             string `json:"id"`
			TaskCount      int    `json:"taskCount"`
			CompletedCount int    `json:"completedCount"`
		} `json:"projects"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	projectCounts := make(map[string]struct {
		TaskCount      int
		CompletedCount int
	})
	for _, project := range resp.Projects {
		projectCounts[project.ID] = struct {
			TaskCount      int
			CompletedCount int
		}{
			TaskCount:      project.TaskCount,
			CompletedCount: project.CompletedCount,
		}
	}

	require.Equal(t, 3, projectCounts[projectOne].TaskCount)
	require.Equal(t, 2, projectCounts[projectOne].CompletedCount)
	require.Equal(t, 0, projectCounts[projectTwo].TaskCount)
	require.Equal(t, 0, projectCounts[projectTwo].CompletedCount)
}

func TestProjectsHandlerLabelFilter(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-label-filter-org")

	projectOne := insertProjectTestProject(t, db, orgID, "Project Label One")
	projectTwo := insertProjectTestProject(t, db, orgID, "Project Label Two")
	projectThree := insertProjectTestProject(t, db, orgID, "Project Label Three")

	labelStore := store.NewLabelStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	bugLabel, err := labelStore.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	backendLabel, err := labelStore.Create(ctx, "backend", "#22c55e")
	require.NoError(t, err)
	opsLabel, err := labelStore.Create(ctx, "ops", "#3b82f6")
	require.NoError(t, err)

	require.NoError(t, labelStore.AddToProject(ctx, projectOne, bugLabel.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectOne, backendLabel.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectTwo, bugLabel.ID))
	require.NoError(t, labelStore.AddToProject(ctx, projectThree, opsLabel.ID))

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	handler.List(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp struct {
		Projects []struct {
			ID     string        `json:"id"`
			Labels []store.Label `json:"labels"`
		} `json:"projects"`
	}
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Projects, 3)

	labelsByProject := make(map[string]int, len(listResp.Projects))
	for _, project := range listResp.Projects {
		labelsByProject[project.ID] = len(project.Labels)
	}
	require.Equal(t, 2, labelsByProject[projectOne])
	require.Equal(t, 1, labelsByProject[projectTwo])
	require.Equal(t, 1, labelsByProject[projectThree])

	filterReq := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID+"&label="+bugLabel.ID+"&label="+backendLabel.ID, nil)
	filterRec := httptest.NewRecorder()
	handler.List(filterRec, filterReq)
	require.Equal(t, http.StatusOK, filterRec.Code)

	var filterResp struct {
		Projects []struct {
			ID     string        `json:"id"`
			Labels []store.Label `json:"labels"`
		} `json:"projects"`
	}
	require.NoError(t, json.NewDecoder(filterRec.Body).Decode(&filterResp))
	require.Len(t, filterResp.Projects, 1)
	require.Equal(t, projectOne, filterResp.Projects[0].ID)
	require.Len(t, filterResp.Projects[0].Labels, 2)

	invalidFilterReq := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID+"&label=not-a-uuid", nil)
	invalidFilterRec := httptest.NewRecorder()
	handler.List(invalidFilterRec, invalidFilterReq)
	require.Equal(t, http.StatusBadRequest, invalidFilterRec.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectOne+"?org_id="+orgID, nil)
	getReq = addRouteParam(getReq, "id", projectOne)
	getRec := httptest.NewRecorder()
	handler.Get(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var getResp struct {
		ID     string        `json:"id"`
		Labels []store.Label `json:"labels"`
	}
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&getResp))
	require.Equal(t, projectOne, getResp.ID)
	require.Len(t, getResp.Labels, 2)
}

func TestNormalizeProjectCreateNameAndDescription(t *testing.T) {
	t.Run("splits embedded description flag from name", func(t *testing.T) {
		name, description := normalizeProjectCreateNameAndDescription(
			"Agent Avatars --description Animal avatar generation for agent identities",
			nil,
		)

		require.Equal(t, "Agent Avatars", name)
		require.NotNil(t, description)
		require.Equal(t, "Animal avatar generation for agent identities", *description)
	})

	t.Run("supports equals delimiter", func(t *testing.T) {
		name, description := normalizeProjectCreateNameAndDescription(
			"Agent Avatars --description=Animal avatar generation for agent identities",
			nil,
		)

		require.Equal(t, "Agent Avatars", name)
		require.NotNil(t, description)
		require.Equal(t, "Animal avatar generation for agent identities", *description)
	})

	t.Run("keeps explicit description over embedded token", func(t *testing.T) {
		name, description := normalizeProjectCreateNameAndDescription(
			"Agent Avatars --description wrong value",
			projectStrPtr("Correct description"),
		)

		require.Equal(t, "Agent Avatars --description wrong value", name)
		require.NotNil(t, description)
		require.Equal(t, "Correct description", *description)
	})
}

func TestProjectsHandlerGetIncludesTaskCounts(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-org-get")
	projectID := insertProjectTestProject(t, db, orgID, "Project Get")

	insertProjectTestTask(t, db, orgID, &projectID, "done")
	insertProjectTestTask(t, db, orgID, &projectID, "review")

	handler := &ProjectsHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"?org_id="+orgID, nil)
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var project struct {
		ID             string `json:"id"`
		TaskCount      int    `json:"taskCount"`
		CompletedCount int    `json:"completedCount"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&project))
	require.Equal(t, projectID, project.ID)
	require.Equal(t, 2, project.TaskCount)
	require.Equal(t, 1, project.CompletedCount)
}

func TestProjectsHandlerUpdateSettingsSetsPrimaryAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-settings-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Settings")
	agentID := insertMessageTestAgent(t, db, orgID, "stone-settings")

	handler := &ProjectsHandler{DB: db}
	body := []byte(`{"primary_agent_id":"` + agentID + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/settings?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID             string  `json:"id"`
		PrimaryAgentID *string `json:"primary_agent_id"`
		Lead           string  `json:"lead"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, projectID, resp.ID)
	require.NotNil(t, resp.PrimaryAgentID)
	require.Equal(t, agentID, *resp.PrimaryAgentID)
	require.Equal(t, "Agent stone-settings", resp.Lead)
}

func TestProjectsHandlerUpdateSettingsRejectsCrossWorkspaceAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-settings-auth-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Settings Auth")
	otherOrgID := insertMessageTestOrganization(t, db, "projects-settings-auth-other-org")
	otherAgentID := insertMessageTestAgent(t, db, otherOrgID, "other-agent")

	handler := &ProjectsHandler{DB: db}
	body := []byte(`{"primary_agent_id":"` + otherAgentID + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/settings?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var projectPrimary sql.NullString
	err := db.QueryRow("SELECT primary_agent_id FROM projects WHERE id = $1", projectID).Scan(&projectPrimary)
	require.NoError(t, err)
	require.False(t, projectPrimary.Valid)
}

func TestProjectsHandlerUpdateSettingsRejectsNonUUIDPrimaryAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-settings-invalid-id-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Settings Invalid ID")

	handler := &ProjectsHandler{DB: db}
	body := []byte(`{"primary_agent_id":"stone"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/settings?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var projectPrimary sql.NullString
	err := db.QueryRow("SELECT primary_agent_id FROM projects WHERE id = $1", projectID).Scan(&projectPrimary)
	require.NoError(t, err)
	require.False(t, projectPrimary.Valid)
}

func TestProjectsHandlerUpdateSettingsClearsPrimaryAgent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-settings-clear-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Settings Clear")
	agentID := insertMessageTestAgent(t, db, orgID, "stone-clear")

	_, err := db.Exec("UPDATE projects SET primary_agent_id = $1 WHERE id = $2", agentID, projectID)
	require.NoError(t, err)

	handler := &ProjectsHandler{DB: db}
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/settings?org_id="+orgID, bytes.NewReader([]byte(`{"primary_agent_id":null}`)))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.UpdateSettings(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		PrimaryAgentID *string `json:"primary_agent_id"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Nil(t, resp.PrimaryAgentID)
}

func TestProjectsHandlerPatchUpdatesProjectFields(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-patch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Patch")

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}
	body := []byte(`{"status":"archived","repo_url":"https://example.com/repo.git","require_human_review":true}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()

	handler.Patch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID                 string `json:"id"`
		Status             string `json:"status"`
		RepoURL            string `json:"repo_url"`
		RequireHumanReview bool   `json:"require_human_review"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, projectID, resp.ID)
	require.Equal(t, "archived", resp.Status)
	require.Equal(t, "https://example.com/repo.git", resp.RepoURL)
	require.True(t, resp.RequireHumanReview)
}

func TestProjectsHandlerPatchRequireHumanReviewJSONCasing(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-patch-casing-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Patch Casing")

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	camelBody := []byte(`{"requireHumanReview":true}`)
	camelReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(camelBody))
	camelReq = addRouteParam(camelReq, "id", projectID)
	camelRec := httptest.NewRecorder()
	handler.Patch(camelRec, camelReq)
	require.Equal(t, http.StatusOK, camelRec.Code)

	snakeBody := []byte(`{"require_human_review":false}`)
	snakeReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(snakeBody))
	snakeReq = addRouteParam(snakeReq, "id", projectID)
	snakeRec := httptest.NewRecorder()
	handler.Patch(snakeRec, snakeReq)
	require.Equal(t, http.StatusOK, snakeRec.Code)

	var resp struct {
		RequireHumanReview bool `json:"require_human_review"`
	}
	require.NoError(t, json.NewDecoder(snakeRec.Body).Decode(&resp))
	require.False(t, resp.RequireHumanReview)

	conflictBody := []byte(`{"require_human_review":true,"requireHumanReview":false}`)
	conflictReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(conflictBody))
	conflictReq = addRouteParam(conflictReq, "id", projectID)
	conflictRec := httptest.NewRecorder()
	handler.Patch(conflictRec, conflictReq)
	require.Equal(t, http.StatusBadRequest, conflictRec.Code)
}

func TestResolveRequireHumanReviewPatch(t *testing.T) {
	one := true
	zero := false

	v, err := resolveRequireHumanReviewPatch(nil, nil)
	require.NoError(t, err)
	require.Nil(t, v)

	v, err = resolveRequireHumanReviewPatch(&one, nil)
	require.NoError(t, err)
	require.NotNil(t, v)
	require.True(t, *v)

	v, err = resolveRequireHumanReviewPatch(nil, &zero)
	require.NoError(t, err)
	require.NotNil(t, v)
	require.False(t, *v)

	v, err = resolveRequireHumanReviewPatch(&one, &one)
	require.NoError(t, err)
	require.NotNil(t, v)
	require.True(t, *v)

	v, err = resolveRequireHumanReviewPatch(&one, &zero)
	require.Error(t, err)
	require.Nil(t, v)
}

func TestProjectsHandlerDeleteRemovesProject(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-delete-org")
	projectID := insertProjectTestProject(t, db, orgID, "Project Delete")

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"?org_id="+orgID, nil)
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()

	handler.Delete(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM projects WHERE id = $1`, projectID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestProjectsHandlerListWorkflowFilter(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-workflow-filter-org")

	workflowProjectID := insertProjectTestProject(t, db, orgID, "Workflow Project")
	nonWorkflowProjectID := insertProjectTestProject(t, db, orgID, "Regular Project")

	_, err := db.Exec(`UPDATE projects SET workflow_enabled = true, workflow_run_count = 3 WHERE id = $1`, workflowProjectID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE projects SET workflow_enabled = false WHERE id = $1`, nonWorkflowProjectID)
	require.NoError(t, err)

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/projects?org_id="+orgID+"&workflow=true", nil)
	rec := httptest.NewRecorder()
	handler.List(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Projects []struct {
			ID               string `json:"id"`
			WorkflowEnabled  bool   `json:"workflow_enabled"`
			WorkflowRunCount int    `json:"workflow_run_count"`
		} `json:"projects"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Projects, 1)
	require.Equal(t, workflowProjectID, resp.Projects[0].ID)
	require.True(t, resp.Projects[0].WorkflowEnabled)
	require.Equal(t, 3, resp.Projects[0].WorkflowRunCount)
}

func TestProjectsHandlerPatchWorkflowFields(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-workflow-patch-org")
	projectID := insertProjectTestProject(t, db, orgID, "Workflow Patch Project")
	agentID := insertMessageTestAgent(t, db, orgID, "workflow-patch-agent")

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	body := []byte(`{
		"workflow_enabled": true,
		"workflow_schedule": {"kind":"cron","expr":"0 6 * * *","tz":"America/Denver"},
		"workflow_template": {"title_pattern":"Morning Briefing — {{date}}","body":"Generate briefing","pipeline":"auto_close"},
		"workflow_agent_id": "` + agentID + `",
		"workflow_run_count": 4
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()

	handler.Patch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID               string          `json:"id"`
		WorkflowEnabled  bool            `json:"workflow_enabled"`
		WorkflowSchedule json.RawMessage `json:"workflow_schedule"`
		WorkflowTemplate json.RawMessage `json:"workflow_template"`
		WorkflowAgentID  *string         `json:"workflow_agent_id"`
		WorkflowRunCount int             `json:"workflow_run_count"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, projectID, resp.ID)
	require.True(t, resp.WorkflowEnabled)
	require.NotNil(t, resp.WorkflowAgentID)
	require.Equal(t, agentID, *resp.WorkflowAgentID)
	require.Equal(t, 4, resp.WorkflowRunCount)
	require.JSONEq(t, `{"kind":"cron","expr":"0 6 * * *","tz":"America/Denver"}`, string(resp.WorkflowSchedule))
	require.JSONEq(t, `{"title_pattern":"Morning Briefing — {{date}}","body":"Generate briefing","pipeline":"auto_close"}`, string(resp.WorkflowTemplate))
}

func TestProjectsHandlerPatchWorkflowScheduleValidation(t *testing.T) {
	testCases := []struct {
		name          string
		scheduleJSON  string
		wantErrorLike string
	}{
		{
			name:          "invalid kind",
			scheduleJSON:  `{"kind":"nonsense"}`,
			wantErrorLike: "workflow_schedule.kind",
		},
		{
			name:          "invalid cron expression",
			scheduleJSON:  `{"kind":"cron","expr":"not-a-cron","tz":"America/Denver"}`,
			wantErrorLike: "workflow_schedule.expr",
		},
		{
			name:          "invalid timezone",
			scheduleJSON:  `{"kind":"cron","expr":"0 6 * * *","tz":"Mars/Olympus"}`,
			wantErrorLike: "workflow_schedule.tz",
		},
		{
			name:          "negative everyMs",
			scheduleJSON:  `{"kind":"every","everyMs":-1}`,
			wantErrorLike: "workflow_schedule.everyMs",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupMessageTestDB(t)
			orgID := insertMessageTestOrganization(t, db, "projects-workflow-schedule-validation-org")
			projectID := insertProjectTestProject(t, db, orgID, "Workflow Validation Project")

			handler := &ProjectsHandler{
				DB:    db,
				Store: store.NewProjectStore(db),
			}

			body := fmt.Sprintf(`{"workflow_schedule":%s}`, tc.scheduleJSON)
			req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader([]byte(body)))
			req = addRouteParam(req, "id", projectID)
			rec := httptest.NewRecorder()

			handler.Patch(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)

			var resp errorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			require.Contains(t, resp.Error, tc.wantErrorLike)
		})
	}
}

func TestNormalizeWorkflowPatchJSONScheduleValidation(t *testing.T) {
	testCases := []struct {
		name          string
		raw           string
		wantErrorLike string
	}{
		{
			name:          "invalid kind",
			raw:           `{"kind":"nonsense"}`,
			wantErrorLike: "workflow_schedule.kind",
		},
		{
			name:          "invalid cron",
			raw:           `{"kind":"cron","expr":"not-a-cron","tz":"America/Denver"}`,
			wantErrorLike: "workflow_schedule.expr",
		},
		{
			name:          "invalid timezone",
			raw:           `{"kind":"cron","expr":"0 6 * * *","tz":"Mars/Olympus"}`,
			wantErrorLike: "workflow_schedule.tz",
		},
		{
			name:          "negative everyMs",
			raw:           `{"kind":"every","everyMs":-1}`,
			wantErrorLike: "workflow_schedule.everyMs",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeWorkflowPatchJSON(json.RawMessage(tc.raw), "workflow_schedule")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErrorLike)
		})
	}
}

func TestProjectsHandlerTriggerRunCreatesIssueAndIncrementsRunCount(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-workflow-run-trigger-org")
	projectID := insertProjectTestProject(t, db, orgID, "Morning Briefing")
	agentID := insertMessageTestAgent(t, db, orgID, "workflow-run-agent")
	_, err := db.Exec(`
		UPDATE projects
		SET workflow_enabled = true,
			workflow_agent_id = $1,
			workflow_template = $2::jsonb,
			workflow_run_count = 2
		WHERE id = $3
	`, agentID, `{"title_pattern":"Briefing {{run_number}}","body":"Agent {{agent_name}} run {{run_number}}","priority":"P1","labels":["automated"]}`, projectID)
	require.NoError(t, err)

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/runs/trigger?org_id="+orgID, nil)
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()
	handler.TriggerRun(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var resp struct {
		Run struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			IssueNumber int64  `json:"issue_number"`
		} `json:"run"`
		RunNumber int `json:"run_number"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 3, resp.RunNumber)
	require.Contains(t, resp.Run.Title, "Briefing 3")
	require.NotEmpty(t, resp.Run.ID)
	require.Greater(t, resp.Run.IssueNumber, int64(0))
}

func TestWorkflowTemplateForProjectRendersVariables(t *testing.T) {
	runCount := 7
	agentID := "550e8400-e29b-41d4-a716-446655440111"
	project := &store.Project{
		Name:            "Morning Briefing",
		WorkflowAgentID: &agentID,
		WorkflowTemplate: json.RawMessage(
			`{"title_pattern":"Morning — {{date}} #{{run_number}}","body":"At {{datetime}} by {{agent_name}}","priority":"P1","labels":["automated"]}`,
		),
	}

	template := workflowTemplateForProject(project, runCount, "Frank")
	require.Contains(t, template.TitlePattern, "Morning — ")
	require.Contains(t, template.TitlePattern, "#"+strconv.Itoa(runCount))
	require.NotContains(t, template.TitlePattern, "{{")
	require.Contains(t, template.Body, "by Frank")
	require.NotContains(t, template.Body, "{{")
	require.Equal(t, "P1", template.Priority)
}

func TestWorkflowRunFromIssueFormatsClosedAt(t *testing.T) {
	created := time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC)
	closed := created.Add(30 * time.Minute)
	issue := store.ProjectIssue{
		ID:          "issue-1",
		ProjectID:   "project-1",
		IssueNumber: 11,
		Title:       "Run",
		State:       "closed",
		WorkStatus:  "done",
		Priority:    "P2",
		CreatedAt:   created,
		ClosedAt:    &closed,
	}

	run := workflowRunFromIssue(issue)
	require.Equal(t, "issue-1", run.ID)
	require.Equal(t, "2026-02-09T12:00:00Z", run.CreatedAt)
	require.NotNil(t, run.ClosedAt)
	require.Equal(t, "2026-02-09T12:30:00Z", *run.ClosedAt)
}

func TestProjectsHandlerListRunsAndLatest(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "projects-workflow-runs-list-org")
	projectID := insertProjectTestProject(t, db, orgID, "Workflow History Project")
	agentID := insertMessageTestAgent(t, db, orgID, "workflow-history-agent")

	issueStore := store.NewProjectIssueStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	first, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Run 1",
		Origin:       "local",
		OwnerAgentID: &agentID,
		Priority:     store.IssuePriorityP2,
	})
	require.NoError(t, err)
	second, err := issueStore.CreateIssue(ctx, store.CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Run 2",
		Origin:       "local",
		OwnerAgentID: &agentID,
		Priority:     store.IssuePriorityP2,
	})
	require.NoError(t, err)
	require.Greater(t, second.IssueNumber, first.IssueNumber)

	handler := &ProjectsHandler{
		DB:    db,
		Store: store.NewProjectStore(db),
	}

	reqList := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runs?org_id="+orgID+"&limit=10", nil)
	reqList = addRouteParam(reqList, "id", projectID)
	recList := httptest.NewRecorder()
	handler.ListRuns(recList, reqList)
	require.Equal(t, http.StatusOK, recList.Code)

	var listResp struct {
		Runs []struct {
			ID          string `json:"id"`
			IssueNumber int64  `json:"issue_number"`
		} `json:"runs"`
		Total int `json:"total"`
	}
	require.NoError(t, json.NewDecoder(recList.Body).Decode(&listResp))
	require.Len(t, listResp.Runs, 2)
	require.Equal(t, 2, listResp.Total)
	require.Equal(t, second.ID, listResp.Runs[0].ID)
	require.Equal(t, first.ID, listResp.Runs[1].ID)

	reqLatest := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runs/latest?org_id="+orgID, nil)
	reqLatest = addRouteParam(reqLatest, "id", projectID)
	recLatest := httptest.NewRecorder()
	handler.GetLatestRun(recLatest, reqLatest)
	require.Equal(t, http.StatusOK, recLatest.Code)

	var latest struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(recLatest.Body).Decode(&latest))
	require.Equal(t, second.ID, latest.ID)
}
