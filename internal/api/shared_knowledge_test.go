package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func newSharedKnowledgeTestRouter(handler *SharedKnowledgeHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.RequireWorkspace).Get("/api/shared-knowledge", handler.ListForAgent)
	r.With(middleware.RequireWorkspace).Get("/api/shared-knowledge/search", handler.Search)
	r.With(middleware.RequireWorkspace).Post("/api/shared-knowledge", handler.Create)
	r.With(middleware.RequireWorkspace).Post("/api/shared-knowledge/{id}/confirm", handler.Confirm)
	r.With(middleware.RequireWorkspace).Post("/api/shared-knowledge/{id}/contradict", handler.Contradict)
	return r
}

func TestSharedKnowledgeHandlerCreateListAndSearch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "shared-knowledge-api-org")
	sourceAgentID := insertMessageTestAgent(t, db, orgID, "marcus")
	targetAgentID := insertMessageTestAgent(t, db, orgID, "elephant")

	handler := &SharedKnowledgeHandler{Store: store.NewSharedKnowledgeStore(db)}
	router := newSharedKnowledgeTestRouter(handler)

	createBody, err := json.Marshal(map[string]any{
		"source_agent_id": sourceAgentID,
		"kind":            "decision",
		"title":           "Use OtterCamp issues as source of truth",
		"content":         "Always treat OtterCamp issue status as canonical for workflow routing.",
		"scope":           "org",
		"quality_score":   0.9,
	})
	require.NoError(t, err)

	createReq := httptest.NewRequest(http.MethodPost, "/api/shared-knowledge?org_id="+orgID, bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created sharedKnowledgePayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, sourceAgentID, created.SourceAgentID)
	require.Equal(t, "decision", created.Kind)
	require.Equal(t, "Use OtterCamp issues as source of truth", created.Title)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/shared-knowledge?org_id="+orgID+"&agent_id="+targetAgentID+"&limit=10",
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp sharedKnowledgeListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 1)
	require.Equal(t, created.ID, listResp.Items[0].ID)

	searchReq := httptest.NewRequest(
		http.MethodGet,
		"/api/shared-knowledge/search?org_id="+orgID+"&q=source+of+truth&limit=5",
		nil,
	)
	searchRec := httptest.NewRecorder()
	router.ServeHTTP(searchRec, searchReq)
	require.Equal(t, http.StatusOK, searchRec.Code)

	var searchResp sharedKnowledgeListResponse
	require.NoError(t, json.NewDecoder(searchRec.Body).Decode(&searchResp))
	require.Len(t, searchResp.Items, 1)
	require.Equal(t, created.ID, searchResp.Items[0].ID)
}

func TestSharedKnowledgeHandlerCreateConfirmContradictPublishesEvents(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "shared-knowledge-events-org")
	sourceAgentID := insertMessageTestAgent(t, db, orgID, "stone")

	handler := &SharedKnowledgeHandler{
		Store:       store.NewSharedKnowledgeStore(db),
		EventsStore: store.NewMemoryEventsStore(db),
	}
	router := newSharedKnowledgeTestRouter(handler)

	createBody, err := json.Marshal(map[string]any{
		"source_agent_id": sourceAgentID,
		"kind":            "lesson",
		"title":           "Always reference OtterCamp issue IDs",
		"content":         "When creating links, include project + issue number to avoid ambiguity.",
		"scope":           "org",
		"quality_score":   0.8,
	})
	require.NoError(t, err)

	createReq := httptest.NewRequest(http.MethodPost, "/api/shared-knowledge?org_id="+orgID, bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created sharedKnowledgePayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))

	confirmReq := httptest.NewRequest(
		http.MethodPost,
		"/api/shared-knowledge/"+created.ID+"/confirm?org_id="+orgID,
		nil,
	)
	confirmRec := httptest.NewRecorder()
	router.ServeHTTP(confirmRec, confirmReq)
	require.Equal(t, http.StatusOK, confirmRec.Code)

	contradictReq := httptest.NewRequest(
		http.MethodPost,
		"/api/shared-knowledge/"+created.ID+"/contradict?org_id="+orgID,
		nil,
	)
	contradictRec := httptest.NewRecorder()
	router.ServeHTTP(contradictRec, contradictReq)
	require.Equal(t, http.StatusOK, contradictRec.Code)

	events, err := store.NewMemoryEventsStore(db).List(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		store.ListMemoryEventsParams{Limit: 10},
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 3)
	require.Equal(t, store.MemoryEventTypeKnowledgeContradict, events[0].EventType)
	require.Equal(t, store.MemoryEventTypeKnowledgeConfirmed, events[1].EventType)
	require.Equal(t, store.MemoryEventTypeKnowledgeShared, events[2].EventType)
}

func TestSharedKnowledgeHandlerRequiresWorkspace(t *testing.T) {
	handler := &SharedKnowledgeHandler{Store: store.NewSharedKnowledgeStore(nil)}
	router := newSharedKnowledgeTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/shared-knowledge?agent_id=123", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSharedKnowledgeHandlerRejectsInvalidCreatePayload(t *testing.T) {
	handler := &SharedKnowledgeHandler{Store: store.NewSharedKnowledgeStore(nil)}
	router := newSharedKnowledgeTestRouter(handler)

	body := []byte(`{"source_agent_id":"not-a-uuid","kind":"decision","title":"x","content":"y"}`)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/shared-knowledge?org_id=00000000-0000-0000-0000-000000000001",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "shared knowledge source_agent_id is invalid")
}
