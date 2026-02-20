package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func insertPipelineStepsTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
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

func insertPipelineStepsTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
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

func pipelineStepsTestRouter(handler *PipelineStepsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/pipeline-steps", handler.List)
	r.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/pipeline-steps", handler.Create)
	r.With(middleware.OptionalWorkspace).Put("/api/projects/{id}/pipeline-steps/reorder", handler.Reorder)
	r.With(middleware.OptionalWorkspace).Patch("/api/projects/{id}/pipeline-steps/{stepID}", handler.Patch)
	r.With(middleware.OptionalWorkspace).Delete("/api/projects/{id}/pipeline-steps/{stepID}", handler.Delete)
	return r
}

func TestPipelineStepsHandlerListCreatePatchDeleteAndReorder(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-steps-org")
	projectID := insertPipelineStepsTestProject(t, db, orgID, "Pipeline Steps API Project")
	agentID := insertPipelineStepsTestAgent(t, db, orgID, "writer")

	handler := &PipelineStepsHandler{Store: store.NewPipelineStepStore(db)}
	router := pipelineStepsTestRouter(handler)

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)
	var initial struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(getRec.Body).Decode(&initial))
	require.Len(t, initial.Items, 0)

	firstBody := []byte(`{
	  "step_number": 1,
	  "name": "Write draft",
	  "description": "Create first draft",
	  "assigned_agent_id": "` + agentID + `",
	  "step_type": "agent_work",
	  "auto_advance": true
	}`)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, bytes.NewReader(firstBody))
	firstReq.Header.Set("Content-Type", "application/json")
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	require.Equal(t, http.StatusCreated, firstRec.Code)

	var first map[string]any
	require.NoError(t, json.NewDecoder(firstRec.Body).Decode(&first))
	firstID := fmt.Sprintf("%v", first["id"])
	require.NotEmpty(t, firstID)

	secondBody := []byte(`{
	  "step_number": 2,
	  "name": "Review",
	  "description": "",
	  "assigned_agent_id": null,
	  "step_type": "agent_review",
	  "auto_advance": true
	}`)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, bytes.NewReader(secondBody))
	secondReq.Header.Set("Content-Type", "application/json")
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	require.Equal(t, http.StatusCreated, secondRec.Code)

	var second map[string]any
	require.NoError(t, json.NewDecoder(secondRec.Body).Decode(&second))
	secondID := fmt.Sprintf("%v", second["id"])
	require.NotEmpty(t, secondID)

	reorderBody := []byte(`{"step_ids":["` + secondID + `","` + firstID + `"]}`)
	reorderReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-steps/reorder?org_id="+orgID, bytes.NewReader(reorderBody))
	reorderReq.Header.Set("Content-Type", "application/json")
	reorderRec := httptest.NewRecorder()
	router.ServeHTTP(reorderRec, reorderReq)
	require.Equal(t, http.StatusOK, reorderRec.Code)
	var reordered struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(reorderRec.Body).Decode(&reordered))
	require.Len(t, reordered.Items, 2)
	require.Equal(t, secondID, fmt.Sprintf("%v", reordered.Items[0]["id"]))
	require.EqualValues(t, 1, reordered.Items[0]["step_number"])

	patchBody := []byte(`{"name":"Final review","auto_advance":false}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/pipeline-steps/"+secondID+"?org_id="+orgID, bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	require.Equal(t, http.StatusOK, patchRec.Code)
	var patched map[string]any
	require.NoError(t, json.NewDecoder(patchRec.Body).Decode(&patched))
	require.Equal(t, "Final review", patched["name"])
	require.Equal(t, false, patched["auto_advance"])

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/pipeline-steps/"+firstID+"?org_id="+orgID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code)

	finalListReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, nil)
	finalListRec := httptest.NewRecorder()
	router.ServeHTTP(finalListRec, finalListReq)
	require.Equal(t, http.StatusOK, finalListRec.Code)
	var finalList struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(finalListRec.Body).Decode(&finalList))
	require.Len(t, finalList.Items, 1)
	require.Equal(t, secondID, fmt.Sprintf("%v", finalList.Items[0]["id"]))
}

func TestPipelineStepsHandlerValidationAndIsolation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "pipeline-steps-validate-org")
	projectID := insertPipelineStepsTestProject(t, db, orgID, "Pipeline Steps Validation Project")
	otherOrgID := insertMessageTestOrganization(t, db, "pipeline-steps-validate-other-org")
	otherOrgAgentID := insertPipelineStepsTestAgent(t, db, otherOrgID, "outside")

	handler := &PipelineStepsHandler{Store: store.NewPipelineStepStore(db)}
	router := pipelineStepsTestRouter(handler)

	invalidProjectReq := httptest.NewRequest(http.MethodGet, "/api/projects/not-a-uuid/pipeline-steps?org_id="+orgID, nil)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)

	missingWorkspaceReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-steps", nil)
	missingWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(missingWorkspaceRec, missingWorkspaceReq)
	require.Equal(t, http.StatusBadRequest, missingWorkspaceRec.Code)

	badStepTypeReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, bytes.NewReader([]byte(`{
	  "step_number": 1,
	  "name": "Bad",
	  "step_type": "observer",
	  "auto_advance": true
	}`)))
	badStepTypeReq.Header.Set("Content-Type", "application/json")
	badStepTypeRec := httptest.NewRecorder()
	router.ServeHTTP(badStepTypeRec, badStepTypeReq)
	require.Equal(t, http.StatusBadRequest, badStepTypeRec.Code)

	crossOrgReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, bytes.NewReader([]byte(`{
	  "step_number": 1,
	  "name": "Cross org",
	  "assigned_agent_id": "`+otherOrgAgentID+`",
	  "step_type": "agent_work",
	  "auto_advance": true
	}`)))
	crossOrgReq.Header.Set("Content-Type", "application/json")
	crossOrgRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgRec, crossOrgReq)
	require.Equal(t, http.StatusNotFound, crossOrgRec.Code)

	createReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id="+orgID, bytes.NewReader([]byte(`{
	  "step_number": 1,
	  "name": "Only step",
	  "step_type": "agent_work",
	  "auto_advance": true
	}`)))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created map[string]any
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	stepID := fmt.Sprintf("%v", created["id"])
	require.NotEmpty(t, stepID)

	reorderReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-steps/reorder?org_id="+orgID, bytes.NewReader([]byte(`{"step_ids":[]}`)))
	reorderReq.Header.Set("Content-Type", "application/json")
	reorderRec := httptest.NewRecorder()
	router.ServeHTTP(reorderRec, reorderReq)
	require.Equal(t, http.StatusBadRequest, reorderRec.Code)

	notFoundPatchReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/pipeline-steps/550e8400-e29b-41d4-a716-446655440222?org_id="+orgID, bytes.NewReader([]byte(`{"name":"x"}`)))
	notFoundPatchReq.Header.Set("Content-Type", "application/json")
	notFoundPatchRec := httptest.NewRecorder()
	router.ServeHTTP(notFoundPatchRec, notFoundPatchReq)
	require.Equal(t, http.StatusNotFound, notFoundPatchRec.Code)

	notFoundDeleteReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/pipeline-steps/550e8400-e29b-41d4-a716-446655440222?org_id="+orgID, nil)
	notFoundDeleteRec := httptest.NewRecorder()
	router.ServeHTTP(notFoundDeleteRec, notFoundDeleteReq)
	require.Equal(t, http.StatusNotFound, notFoundDeleteRec.Code)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/pipeline-steps/"+stepID+"?org_id="+orgID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code)
}

func TestRouterRegistersPipelineStepRoutes(t *testing.T) {
	router := NewRouter()
	projectID := "550e8400-e29b-41d4-a716-446655440000"
	stepID := "550e8400-e29b-41d4-a716-446655440111"

	getReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/pipeline-steps?org_id=550e8400-e29b-41d4-a716-446655440001", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.NotEqual(t, http.StatusNotFound, getRec.Code)

	postReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/pipeline-steps?org_id=550e8400-e29b-41d4-a716-446655440001", bytes.NewReader([]byte(`{
	  "step_number": 1,
	  "name": "Write",
	  "step_type": "agent_work",
	  "auto_advance": true
	}`)))
	postReq.Header.Set("Content-Type", "application/json")
	postRec := httptest.NewRecorder()
	router.ServeHTTP(postRec, postReq)
	require.NotEqual(t, http.StatusNotFound, postRec.Code)

	reorderReq := httptest.NewRequest(http.MethodPut, "/api/projects/"+projectID+"/pipeline-steps/reorder?org_id=550e8400-e29b-41d4-a716-446655440001", bytes.NewReader([]byte(`{"step_ids":["`+stepID+`"]}`)))
	reorderReq.Header.Set("Content-Type", "application/json")
	reorderRec := httptest.NewRecorder()
	router.ServeHTTP(reorderRec, reorderReq)
	require.NotEqual(t, http.StatusNotFound, reorderRec.Code)

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/projects/"+projectID+"/pipeline-steps/"+stepID+"?org_id=550e8400-e29b-41d4-a716-446655440001", bytes.NewReader([]byte(`{"name":"Edit"}`)))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	require.NotEqual(t, http.StatusNotFound, patchRec.Code)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/pipeline-steps/"+stepID+"?org_id=550e8400-e29b-41d4-a716-446655440001", nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.NotEqual(t, http.StatusNotFound, deleteRec.Code)
}
