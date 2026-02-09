package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func insertPipelineRolesTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
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

func insertPipelineRolesTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
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

func pipelineRolesTestRouter(handler *PipelineRolesHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/pipeline-roles", handler.Get)
	r.With(middleware.OptionalWorkspace).Put("/api/projects/{id}/pipeline-roles", handler.Put)
	return r
}

func TestPipelineRolesHandlerGetAndPut(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-roles-org")
	projectID := insertPipelineRolesTestProject(t, db, orgID, "Pipeline API Project")
	plannerAgent := insertPipelineRolesTestAgent(t, db, orgID, "planner")
	workerAgent := insertPipelineRolesTestAgent(t, db, orgID, "worker")

	handler := &PipelineRolesHandler{
		Store: store.NewPipelineRoleStore(db),
	}
	router := pipelineRolesTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var initial map[string]map[string]*string
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&initial))
	require.Contains(t, initial, "planner")
	require.Contains(t, initial, "worker")
	require.Contains(t, initial, "reviewer")
	require.Nil(t, initial["planner"]["agentId"])
	require.Nil(t, initial["worker"]["agentId"])
	require.Nil(t, initial["reviewer"]["agentId"])

	putPayload := []byte(`{
	  "planner": {"agentId":"` + plannerAgent + `"},
	  "worker": {"agentId":"` + workerAgent + `"},
	  "reviewer": {"agentId":null}
	}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, bytes.NewReader(putPayload))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	require.Equal(t, http.StatusOK, putRec.Code)

	var updated map[string]map[string]*string
	require.NoError(t, json.NewDecoder(putRec.Body).Decode(&updated))
	require.NotNil(t, updated["planner"]["agentId"])
	require.Equal(t, plannerAgent, *updated["planner"]["agentId"])
	require.NotNil(t, updated["worker"]["agentId"])
	require.Equal(t, workerAgent, *updated["worker"]["agentId"])
	require.Nil(t, updated["reviewer"]["agentId"])
}

func TestPipelineRolesHandlerValidation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-roles-validate-org")
	projectID := insertPipelineRolesTestProject(t, db, orgID, "Pipeline API Validation Project")
	otherOrgID := insertMessageTestOrganization(t, db, "pipeline-roles-validate-other-org")
	otherAgentID := insertPipelineRolesTestAgent(t, db, otherOrgID, "other-agent")

	handler := &PipelineRolesHandler{
		Store: store.NewPipelineRoleStore(db),
	}
	router := pipelineRolesTestRouter(handler)

	invalidProjectReq := httptest.NewRequest(http.MethodGet, "/api/projects/not-a-uuid/pipeline-roles?org_id="+orgID, nil)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-roles", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusBadRequest, missingWorkspaceRec.Code)

	invalidBodyReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, bytes.NewReader([]byte(`{"planner":{"agentId":"bad-id"}}`)))
	invalidBodyReq.Header.Set("Content-Type", "application/json")
	invalidBodyRec := httptest.NewRecorder()
	router.ServeHTTP(invalidBodyRec, invalidBodyReq)
	require.Equal(t, http.StatusBadRequest, invalidBodyRec.Code)

	crossOrgBody := []byte(`{
	  "planner": {"agentId":"` + otherAgentID + `"},
	  "worker": {"agentId":null},
	  "reviewer": {"agentId":null}
	}`)
	crossOrgReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, bytes.NewReader(crossOrgBody))
	crossOrgReq.Header.Set("Content-Type", "application/json")
	crossOrgRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgRec, crossOrgReq)
	require.Equal(t, http.StatusNotFound, crossOrgRec.Code)
}

func TestPipelineRolesHandlerPutIsAtomicOnMixedValidity(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-roles-atomic-org")
	projectID := insertPipelineRolesTestProject(t, db, orgID, "Pipeline Atomic Project")
	plannerAgentID := insertPipelineRolesTestAgent(t, db, orgID, "atomic-planner")
	otherOrgID := insertMessageTestOrganization(t, db, "pipeline-roles-atomic-other-org")
	invalidWorkerAgentID := insertPipelineRolesTestAgent(t, db, otherOrgID, "atomic-invalid-worker")

	handler := &PipelineRolesHandler{
		Store: store.NewPipelineRoleStore(db),
	}
	router := pipelineRolesTestRouter(handler)

	putPayload := []byte(`{
	  "planner": {"agentId":"` + plannerAgentID + `"},
	  "worker": {"agentId":"` + invalidWorkerAgentID + `"},
	  "reviewer": {"agentId":null}
	}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, bytes.NewReader(putPayload))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	require.Equal(t, http.StatusNotFound, putRec.Code)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	var roles map[string]map[string]*string
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&roles))
	require.Nil(t, roles["planner"]["agentId"])
	require.Nil(t, roles["worker"]["agentId"])
	require.Nil(t, roles["reviewer"]["agentId"])
}

func TestRouterRegistersPipelineRolesRoutes(t *testing.T) {
	router := NewRouter()
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-roles?org_id=550e8400-e29b-41d4-a716-446655440001", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.NotEqual(t, http.StatusNotFound, getRec.Code)

	putReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-roles?org_id=550e8400-e29b-41d4-a716-446655440001", bytes.NewReader([]byte(`{
	  "planner": {"agentId": null},
	  "worker": {"agentId": null},
	  "reviewer": {"agentId": null}
	}`)))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)
	require.NotEqual(t, http.StatusNotFound, putRec.Code)
}

func TestPipelineRolesHandlerUnexpectedStoreErrorSanitized(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-roles-api-store-error-org")
	projectID := insertPipelineRolesTestProject(t, db, orgID, "Pipeline API Error Project")

	handler := &PipelineRolesHandler{
		Store: store.NewPipelineRoleStore(db),
	}
	router := pipelineRolesTestRouter(handler)

	_, err := db.Exec(`DROP TABLE issue_role_assignments`)
	require.NoError(t, err)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-roles?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusInternalServerError, getRec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&payload))
	require.Equal(t, "internal server error", payload.Error)
	require.False(t, strings.Contains(strings.ToLower(payload.Error), "issue_role_assignments"))
}

func TestPipelineRoleStoreErrorMessageSanitizesUnexpectedError(t *testing.T) {
	err := errors.New(`pq: relation "issue_role_assignments" does not exist`)
	require.Equal(t, http.StatusInternalServerError, pipelineRoleStoreErrorStatus(err))
	require.Equal(t, "internal server error", pipelineRoleStoreErrorMessage(err))
}

func TestPipelineRolesHandlerValidationErrorClassification(t *testing.T) {
	validationErr := fmt.Errorf("%w: invalid agent_id", store.ErrValidation)
	require.Equal(t, http.StatusBadRequest, pipelineRoleStoreErrorStatus(validationErr))

	falsePositiveErr := errors.New("postgres transaction invalidated unexpectedly")
	require.Equal(t, http.StatusInternalServerError, pipelineRoleStoreErrorStatus(falsePositiveErr))
}
