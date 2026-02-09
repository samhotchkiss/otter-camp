package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	body := []byte(`{"status":"archived","repo_url":"https://example.com/repo.git"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"?org_id="+orgID, bytes.NewReader(body))
	req = addRouteParam(req, "id", projectID)
	rec := httptest.NewRecorder()

	handler.Patch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		RepoURL string `json:"repo_url"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, projectID, resp.ID)
	require.Equal(t, "archived", resp.Status)
	require.Equal(t, "https://example.com/repo.git", resp.RepoURL)
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
