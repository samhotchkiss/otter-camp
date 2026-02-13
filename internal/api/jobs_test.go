package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func jobsTestRouter(handler *JobsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Post("/api/v1/jobs", handler.Create)
	r.With(middleware.OptionalWorkspace).Get("/api/v1/jobs", handler.List)
	r.With(middleware.OptionalWorkspace).Get("/api/v1/jobs/{id}", handler.Get)
	r.With(middleware.OptionalWorkspace).Patch("/api/v1/jobs/{id}", handler.Patch)
	r.With(middleware.OptionalWorkspace).Delete("/api/v1/jobs/{id}", handler.Delete)
	r.With(middleware.OptionalWorkspace).Post("/api/v1/jobs/{id}/run", handler.RunNow)
	r.With(middleware.OptionalWorkspace).Get("/api/v1/jobs/{id}/runs", handler.ListRuns)
	r.With(middleware.OptionalWorkspace).Post("/api/v1/jobs/{id}/pause", handler.Pause)
	r.With(middleware.OptionalWorkspace).Post("/api/v1/jobs/{id}/resume", handler.Resume)
	return r
}

func TestJobsHandlerCreateListGetPatchPauseResumeRunDelete(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "jobs-handler-crud")
	agentID := insertMessageTestAgent(t, db, orgID, "jobs-handler-agent")

	handler := &JobsHandler{Store: store.NewAgentJobStore(db), DB: db}
	router := jobsTestRouter(handler)

	createBody := []byte(`{
		"agent_id":"` + agentID + `",
		"name":"Heartbeat",
		"schedule_kind":"interval",
		"interval_ms":60000,
		"timezone":"UTC",
		"payload_kind":"message",
		"payload_text":"run heartbeat"
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created jobPayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, "Heartbeat", created.Name)
	require.Equal(t, agentID, created.AgentID)
	require.Equal(t, "interval", created.ScheduleKind)
	require.NotNil(t, created.NextRunAt)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp jobsListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 1)
	require.Equal(t, created.ID, listResp.Items[0].ID)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	patchBody := []byte(`{"name":"Heartbeat Updated","payload_text":"run heartbeat now"}`)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/jobs/"+created.ID+"?org_id="+orgID, bytes.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	require.Equal(t, http.StatusOK, patchRec.Code)
	var patched jobPayload
	require.NoError(t, json.NewDecoder(patchRec.Body).Decode(&patched))
	require.Equal(t, "Heartbeat Updated", patched.Name)

	pauseReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/pause?org_id="+orgID, nil)
	pauseRec := httptest.NewRecorder()
	router.ServeHTTP(pauseRec, pauseReq)
	require.Equal(t, http.StatusOK, pauseRec.Code)

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/resume?org_id="+orgID, nil)
	resumeRec := httptest.NewRecorder()
	router.ServeHTTP(resumeRec, resumeReq)
	require.Equal(t, http.StatusOK, resumeRec.Code)

	runReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+created.ID+"/run?org_id="+orgID, nil)
	runRec := httptest.NewRecorder()
	router.ServeHTTP(runRec, runReq)
	require.Equal(t, http.StatusOK, runRec.Code)
	var runTriggered jobPayload
	require.NoError(t, json.NewDecoder(runRec.Body).Decode(&runTriggered))
	require.Equal(t, "active", runTriggered.Status)
	require.NotNil(t, runTriggered.NextRunAt)

	runsReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID+"/runs?org_id="+orgID, nil)
	runsRec := httptest.NewRecorder()
	router.ServeHTTP(runsRec, runsReq)
	require.Equal(t, http.StatusOK, runsRec.Code)
	var runsResp jobRunsResponse
	require.NoError(t, json.NewDecoder(runsRec.Body).Decode(&runsResp))
	require.Len(t, runsResp.Items, 0)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+created.ID+"?org_id="+orgID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code)

	getAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID+"?org_id="+orgID, nil)
	getAfterDeleteRec := httptest.NewRecorder()
	router.ServeHTTP(getAfterDeleteRec, getAfterDeleteReq)
	require.Equal(t, http.StatusNotFound, getAfterDeleteRec.Code)
}

func TestJobsHandlerAgentSelfScoping(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "jobs-handler-scope")
	agentA := insertMessageTestAgent(t, db, orgID, "jobs-scope-a")
	agentB := insertMessageTestAgent(t, db, orgID, "jobs-scope-b")
	sessionA := "agent:chameleon:oc:" + agentA

	handler := &JobsHandler{Store: store.NewAgentJobStore(db), DB: db}
	router := jobsTestRouter(handler)

	createBBody := []byte(`{
		"agent_id":"` + agentB + `",
		"name":"B Job",
		"schedule_kind":"interval",
		"interval_ms":60000,
		"timezone":"UTC",
		"payload_kind":"message",
		"payload_text":"b"
	}`)
	createBReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader(createBBody))
	createBReq.Header.Set("Content-Type", "application/json")
	createBRec := httptest.NewRecorder()
	router.ServeHTTP(createBRec, createBReq)
	require.Equal(t, http.StatusCreated, createBRec.Code)
	var createdB jobPayload
	require.NoError(t, json.NewDecoder(createBRec.Body).Decode(&createdB))

	createABody := []byte(`{
		"agent_id":"` + agentA + `",
		"name":"A Job",
		"schedule_kind":"interval",
		"interval_ms":60000,
		"timezone":"UTC",
		"payload_kind":"message",
		"payload_text":"a",
		"session_key":"` + sessionA + `"
	}`)
	createAReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader(createABody))
	createAReq.Header.Set("Content-Type", "application/json")
	createARec := httptest.NewRecorder()
	router.ServeHTTP(createARec, createAReq)
	require.Equal(t, http.StatusCreated, createARec.Code)

	createMismatchBody := []byte(`{
		"agent_id":"` + agentB + `",
		"name":"Mismatch",
		"schedule_kind":"interval",
		"interval_ms":60000,
		"timezone":"UTC",
		"payload_kind":"message",
		"payload_text":"x",
		"session_key":"` + sessionA + `"
	}`)
	createMismatchReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader(createMismatchBody))
	createMismatchReq.Header.Set("Content-Type", "application/json")
	createMismatchRec := httptest.NewRecorder()
	router.ServeHTTP(createMismatchRec, createMismatchReq)
	require.Equal(t, http.StatusForbidden, createMismatchRec.Code)

	scopedListReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?org_id="+orgID+"&session_key="+sessionA, nil)
	scopedListRec := httptest.NewRecorder()
	router.ServeHTTP(scopedListRec, scopedListReq)
	require.Equal(t, http.StatusOK, scopedListRec.Code)
	var scoped jobsListResponse
	require.NoError(t, json.NewDecoder(scopedListRec.Body).Decode(&scoped))
	require.Len(t, scoped.Items, 1)
	require.Equal(t, agentA, scoped.Items[0].AgentID)

	scopedGetOtherReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+createdB.ID+"?org_id="+orgID+"&session_key="+sessionA, nil)
	scopedGetOtherRec := httptest.NewRecorder()
	router.ServeHTTP(scopedGetOtherRec, scopedGetOtherReq)
	require.Equal(t, http.StatusForbidden, scopedGetOtherRec.Code)
}

func TestJobsHandlerValidation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "jobs-handler-validation")
	agentID := insertMessageTestAgent(t, db, orgID, "jobs-validate-agent")
	handler := &JobsHandler{Store: store.NewAgentJobStore(db), DB: db}
	router := jobsTestRouter(handler)

	invalidJSONReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader([]byte(`{"bad"`)))
	invalidJSONReq.Header.Set("Content-Type", "application/json")
	invalidJSONRec := httptest.NewRecorder()
	router.ServeHTTP(invalidJSONRec, invalidJSONReq)
	require.Equal(t, http.StatusBadRequest, invalidJSONRec.Code)

	missingOrgReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", bytes.NewReader([]byte(`{}`)))
	missingOrgReq.Header.Set("Content-Type", "application/json")
	missingOrgRec := httptest.NewRecorder()
	router.ServeHTTP(missingOrgRec, missingOrgReq)
	require.Equal(t, http.StatusBadRequest, missingOrgRec.Code)

	invalidScheduleBody := []byte(`{
		"agent_id":"` + agentID + `",
		"name":"Bad Schedule",
		"schedule_kind":"yearly",
		"timezone":"UTC",
		"payload_kind":"message",
		"payload_text":"bad"
	}`)
	invalidScheduleReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?org_id="+orgID, bytes.NewReader(invalidScheduleBody))
	invalidScheduleReq.Header.Set("Content-Type", "application/json")
	invalidScheduleRec := httptest.NewRecorder()
	router.ServeHTTP(invalidScheduleRec, invalidScheduleReq)
	require.Equal(t, http.StatusBadRequest, invalidScheduleRec.Code)

	invalidSessionReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?org_id="+orgID+"&session_key=not-a-session-key", nil)
	invalidSessionRec := httptest.NewRecorder()
	router.ServeHTTP(invalidSessionRec, invalidSessionReq)
	require.Equal(t, http.StatusBadRequest, invalidSessionRec.Code)
}

func TestRouterRegistersJobsRoutes(t *testing.T) {
	router := NewRouter()
	orgID := "550e8400-e29b-41d4-a716-446655440001"
	jobID := "550e8400-e29b-41d4-a716-446655440002"

	reqs := []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodGet, path: "/api/v1/jobs?org_id=" + orgID},
		{method: http.MethodPost, path: "/api/v1/jobs?org_id=" + orgID, body: []byte(`{}`)},
		{method: http.MethodGet, path: "/api/v1/jobs/" + jobID + "?org_id=" + orgID},
		{method: http.MethodPatch, path: "/api/v1/jobs/" + jobID + "?org_id=" + orgID, body: []byte(`{}`)},
		{method: http.MethodDelete, path: "/api/v1/jobs/" + jobID + "?org_id=" + orgID},
		{method: http.MethodPost, path: "/api/v1/jobs/" + jobID + "/run?org_id=" + orgID},
		{method: http.MethodGet, path: "/api/v1/jobs/" + jobID + "/runs?org_id=" + orgID},
		{method: http.MethodPost, path: "/api/v1/jobs/" + jobID + "/pause?org_id=" + orgID},
		{method: http.MethodPost, path: "/api/v1/jobs/" + jobID + "/resume?org_id=" + orgID},
	}

	for _, tc := range reqs {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
		if tc.body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusNotFound, rec.Code, tc.method+" "+tc.path)
	}
}
