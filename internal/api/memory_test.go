package api

import (
	"bytes"
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

func newMemoryTestRouter(handler *MemoryHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.RequireWorkspace).Post("/api/memory/entries", handler.Create)
	r.With(middleware.RequireWorkspace).Get("/api/memory/entries", handler.List)
	r.With(middleware.RequireWorkspace).Delete("/api/memory/entries/{id}", handler.Delete)
	r.With(middleware.RequireWorkspace).Get("/api/memory/search", handler.Search)
	r.With(middleware.RequireWorkspace).Get("/api/memory/recall", handler.Recall)
	r.With(middleware.RequireWorkspace).Get("/api/memory/evaluations/latest", handler.LatestEvaluation)
	return r
}

func TestMemoryHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "memory-handler-org")
	agentID := insertMessageTestAgent(t, db, orgID, "memory-handler-agent")

	handler := &MemoryHandler{Store: store.NewMemoryStore(db)}
	router := newMemoryTestRouter(handler)

	createBody := []byte(`{
		"agent_id":"` + agentID + `",
		"kind":"decision",
		"title":"Adopt semantic recall",
		"content":"Use memory search to inject relevant context.",
		"importance":4,
		"confidence":0.8,
		"sensitivity":"internal",
		"source_issue":"#111"
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/memory/entries?org_id="+orgID, bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created memoryEntryPayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, "decision", created.Kind)
	require.NotEmpty(t, created.ID)

	listReq := httptest.NewRequest(http.MethodGet, "/api/memory/entries?org_id="+orgID+"&agent_id="+agentID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp memoryListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Equal(t, 1, listResp.Total)

	searchReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/search?org_id="+orgID+"&agent_id="+agentID+"&q=semantic+recall",
		nil,
	)
	searchRec := httptest.NewRecorder()
	router.ServeHTTP(searchRec, searchReq)
	require.Equal(t, http.StatusOK, searchRec.Code)

	var searchResp memoryListResponse
	require.NoError(t, json.NewDecoder(searchRec.Body).Decode(&searchResp))
	require.Equal(t, 1, searchResp.Total)
	require.NotNil(t, searchResp.Items[0].RelevanceScore)

	recallReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/recall?org_id="+orgID+"&agent_id="+agentID+"&q=semantic+recall",
		nil,
	)
	recallRec := httptest.NewRecorder()
	router.ServeHTTP(recallRec, recallReq)
	require.Equal(t, http.StatusOK, recallRec.Code)

	var recallResp memoryRecallResponse
	require.NoError(t, json.NewDecoder(recallRec.Body).Decode(&recallResp))
	require.Contains(t, recallResp.Context, "[RECALLED CONTEXT]")

	deleteReq := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/memory/entries/%s?org_id=%s", created.ID, orgID),
		nil,
	)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code)

	listAfterDeleteReq := httptest.NewRequest(http.MethodGet, "/api/memory/entries?org_id="+orgID+"&agent_id="+agentID, nil)
	listAfterDeleteRec := httptest.NewRecorder()
	router.ServeHTTP(listAfterDeleteRec, listAfterDeleteReq)
	require.Equal(t, http.StatusOK, listAfterDeleteRec.Code)

	var listAfterDelete memoryListResponse
	require.NoError(t, json.NewDecoder(listAfterDeleteRec.Body).Decode(&listAfterDelete))
	require.Equal(t, 0, listAfterDelete.Total)
}

func TestMemoryHandlerOrgIsolation(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "memory-handler-org-a")
	orgB := insertMessageTestOrganization(t, db, "memory-handler-org-b")
	agentA := insertMessageTestAgent(t, db, orgA, "memory-handler-agent-a")
	_ = insertMessageTestAgent(t, db, orgB, "memory-handler-agent-b")

	handler := &MemoryHandler{Store: store.NewMemoryStore(db)}
	router := newMemoryTestRouter(handler)

	createBody := []byte(`{
		"agent_id":"` + agentA + `",
		"kind":"fact",
		"title":"Org A secret",
		"content":"Org A only memory entry."
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/memory/entries?org_id="+orgA, bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created memoryEntryPayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))

	orgBSearchReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/search?org_id="+orgB+"&agent_id="+agentA+"&q=secret",
		nil,
	)
	orgBSearchRec := httptest.NewRecorder()
	router.ServeHTTP(orgBSearchRec, orgBSearchReq)
	require.Equal(t, http.StatusOK, orgBSearchRec.Code)

	var searchResp memoryListResponse
	require.NoError(t, json.NewDecoder(orgBSearchRec.Body).Decode(&searchResp))
	require.Equal(t, 0, searchResp.Total)

	orgBDeleteReq := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf("/api/memory/entries/%s?org_id=%s", created.ID, orgB),
		nil,
	)
	orgBDeleteRec := httptest.NewRecorder()
	router.ServeHTTP(orgBDeleteRec, orgBDeleteReq)
	require.Equal(t, http.StatusNotFound, orgBDeleteRec.Code)

	noWorkspaceReq := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/entries?agent_id="+agentA,
		nil,
	)
	noWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(noWorkspaceRec, noWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, noWorkspaceRec.Code)
}

func TestMemoryRoutesRequireWorkspace(t *testing.T) {
	router := newMemoryTestRouter(&MemoryHandler{})

	req := httptest.NewRequest(http.MethodGet, "/api/memory/entries?agent_id=00000000-0000-0000-0000-000000000001", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMemoryHandlerCapsListAndSearchLimit(t *testing.T) {
	require.Equal(t, 200, clampMemoryListLimit(999999))
	require.Equal(t, 100, clampMemorySearchLimit(999999))
	require.Equal(t, 20, clampMemoryListLimit(0))
	require.Equal(t, 20, clampMemorySearchLimit(0))
}

func TestMemoryEvaluationLatestReturnsNoRunWhenUnavailable(t *testing.T) {
	router := newMemoryTestRouter(&MemoryHandler{})

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/memory/evaluations/latest?org_id=00000000-0000-0000-0000-000000000001",
		nil,
	)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload memoryEvaluationLatestResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Nil(t, payload.Run)
}
