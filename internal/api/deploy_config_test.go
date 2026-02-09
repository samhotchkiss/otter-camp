package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func insertDeployConfigTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
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

func deployConfigTestRouter(handler *DeployConfigHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/deploy-config", handler.Get)
	r.With(middleware.OptionalWorkspace).Put("/api/projects/{id}/deploy-config", handler.Put)
	return r
}

func TestDeployConfigHandlerGetAndPut(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "deploy-config-api-org")
	projectID := insertDeployConfigTestProject(t, db, orgID, "Deploy API Project")

	handler := &DeployConfigHandler{
		Store: store.NewDeployConfigStore(db),
	}
	router := deployConfigTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/deploy-config?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var initial map[string]interface{}
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&initial))
	require.Equal(t, "none", initial["deployMethod"])
	require.Equal(t, "main", initial["githubBranch"])

	putGithubPayload := []byte(`{
	  "deployMethod": "github_push",
	  "githubRepoUrl": "https://github.com/example/repo.git",
	  "githubBranch": "release"
	}`)
	putGithubReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/deploy-config?org_id="+orgID, bytes.NewReader(putGithubPayload))
	putGithubReq.Header.Set("Content-Type", "application/json")
	putGithubRec := httptest.NewRecorder()
	router.ServeHTTP(putGithubRec, putGithubReq)
	require.Equal(t, http.StatusOK, putGithubRec.Code)

	var githubResp map[string]interface{}
	require.NoError(t, json.NewDecoder(putGithubRec.Body).Decode(&githubResp))
	require.Equal(t, "github_push", githubResp["deployMethod"])
	require.Equal(t, "https://github.com/example/repo.git", githubResp["githubRepoUrl"])
	require.Equal(t, "release", githubResp["githubBranch"])
	require.Nil(t, githubResp["cliCommand"])

	putCommandPayload := []byte(`{
	  "deployMethod": "cli_command",
	  "cliCommand": "npx itsalive-co"
	}`)
	putCommandReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/deploy-config?org_id="+orgID, bytes.NewReader(putCommandPayload))
	putCommandReq.Header.Set("Content-Type", "application/json")
	putCommandRec := httptest.NewRecorder()
	router.ServeHTTP(putCommandRec, putCommandReq)
	require.Equal(t, http.StatusOK, putCommandRec.Code)

	var commandResp map[string]interface{}
	require.NoError(t, json.NewDecoder(putCommandRec.Body).Decode(&commandResp))
	require.Equal(t, "cli_command", commandResp["deployMethod"])
	require.Equal(t, "npx itsalive-co", commandResp["cliCommand"])
	require.Nil(t, commandResp["githubRepoUrl"])
}

func TestDeployConfigHandlerValidation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "deploy-config-api-validate-org")
	projectID := insertDeployConfigTestProject(t, db, orgID, "Deploy API Validation Project")
	otherOrgID := insertMessageTestOrganization(t, db, "deploy-config-api-validate-other-org")
	otherProjectID := insertDeployConfigTestProject(t, db, otherOrgID, "Deploy API Other Project")

	handler := &DeployConfigHandler{
		Store: store.NewDeployConfigStore(db),
	}
	router := deployConfigTestRouter(handler)

	invalidProjectReq := httptest.NewRequest(http.MethodGet, "/api/projects/not-a-uuid/deploy-config?org_id="+orgID, nil)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/deploy-config", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusBadRequest, missingWorkspaceRec.Code)

	invalidBodyReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/deploy-config?org_id="+orgID, bytes.NewReader([]byte(`{"deployMethod":"wat"}`)))
	invalidBodyReq.Header.Set("Content-Type", "application/json")
	invalidBodyRec := httptest.NewRecorder()
	router.ServeHTTP(invalidBodyRec, invalidBodyReq)
	require.Equal(t, http.StatusBadRequest, invalidBodyRec.Code)

	missingCommandReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/deploy-config?org_id="+orgID, bytes.NewReader([]byte(`{"deployMethod":"cli_command"}`)))
	missingCommandReq.Header.Set("Content-Type", "application/json")
	missingCommandRec := httptest.NewRecorder()
	router.ServeHTTP(missingCommandRec, missingCommandReq)
	require.Equal(t, http.StatusBadRequest, missingCommandRec.Code)

	crossOrgReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+otherProjectID+"/deploy-config?org_id="+orgID, nil)
	crossOrgRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgRec, crossOrgReq)
	require.Equal(t, http.StatusNotFound, crossOrgRec.Code)
}

func TestRouterRegistersDeployConfigRoutes(t *testing.T) {
	router := NewRouter()
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/deploy-config?org_id=550e8400-e29b-41d4-a716-446655440001", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.NotEqual(t, http.StatusNotFound, getRec.Code)

	putReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/deploy-config?org_id=550e8400-e29b-41d4-a716-446655440001", bytes.NewReader([]byte(`{"deployMethod":"none"}`)))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	require.NotEqual(t, http.StatusNotFound, putRec.Code)
}
