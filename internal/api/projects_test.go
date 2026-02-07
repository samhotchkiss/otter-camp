package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

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
