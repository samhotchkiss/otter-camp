package api

import (
	"bytes"
	"context"
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

func TestTaxonomyCRUD(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	db := openFeedDatabase(t, connStr)
	orgID := insertTaxonomyTestOrganization(t, db, "taxonomy-crud-org")

	handler := &TaxonomyHandler{Store: store.NewEllieTaxonomyStore(db)}
	router := taxonomyTestRouter(handler)

	rootReq := httptest.NewRequest(http.MethodPost, "/api/taxonomy/nodes?org_id="+orgID, bytes.NewBufferString(`{"slug":"personal","display_name":"Personal"}`))
	rootReq.Header.Set("Content-Type", "application/json")
	rootRec := httptest.NewRecorder()
	router.ServeHTTP(rootRec, rootReq)
	require.Equal(t, http.StatusCreated, rootRec.Code)

	var root taxonomyNodeResponse
	require.NoError(t, json.NewDecoder(rootRec.Body).Decode(&root))
	require.Equal(t, "personal", root.Slug)

	childPayload := map[string]any{
		"parent_id":    root.ID,
		"slug":         "vehicles",
		"display_name": "Vehicles",
	}
	childRaw, err := json.Marshal(childPayload)
	require.NoError(t, err)
	childReq := httptest.NewRequest(http.MethodPost, "/api/taxonomy/nodes?org_id="+orgID, bytes.NewReader(childRaw))
	childReq.Header.Set("Content-Type", "application/json")
	childRec := httptest.NewRecorder()
	router.ServeHTTP(childRec, childReq)
	require.Equal(t, http.StatusCreated, childRec.Code)

	var child taxonomyNodeResponse
	require.NoError(t, json.NewDecoder(childRec.Body).Decode(&child))
	require.Equal(t, "vehicles", child.Slug)

	getReq := httptest.NewRequest(http.MethodGet, "/api/taxonomy/nodes/"+child.ID+"?org_id="+orgID, nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	require.Equal(t, http.StatusOK, getRec.Code)

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/taxonomy/nodes/"+child.ID+"?org_id="+orgID, bytes.NewBufferString(`{"display_name":"Cars"}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	require.Equal(t, http.StatusOK, patchRec.Code)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/taxonomy/nodes/"+child.ID+"?org_id="+orgID, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code)

	missingReq := httptest.NewRequest(http.MethodGet, "/api/taxonomy/nodes/"+child.ID+"?org_id="+orgID, nil)
	missingRec := httptest.NewRecorder()
	router.ServeHTTP(missingRec, missingReq)
	require.Equal(t, http.StatusNotFound, missingRec.Code)
}

func TestTaxonomyReparentPreventsCycles(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	db := openFeedDatabase(t, connStr)
	orgID := insertTaxonomyTestOrganization(t, db, "taxonomy-cycle-org")

	handler := &TaxonomyHandler{Store: store.NewEllieTaxonomyStore(db)}
	router := taxonomyTestRouter(handler)

	root := createTaxonomyNodeViaAPI(t, router, orgID, map[string]any{
		"slug":         "projects",
		"display_name": "Projects",
	})
	child := createTaxonomyNodeViaAPI(t, router, orgID, map[string]any{
		"parent_id":    root.ID,
		"slug":         "otter-camp",
		"display_name": "Otter Camp",
	})

	cycleReq := httptest.NewRequest(http.MethodPatch, "/api/taxonomy/nodes/"+root.ID+"?org_id="+orgID, bytes.NewBufferString(`{"parent_id":"`+child.ID+`"}`))
	cycleReq.Header.Set("Content-Type", "application/json")
	cycleRec := httptest.NewRecorder()
	router.ServeHTTP(cycleRec, cycleReq)
	require.Equal(t, http.StatusConflict, cycleRec.Code)
}

func TestTaxonomyRetrieveMemoriesBySubtree(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	db := openFeedDatabase(t, connStr)
	orgID := insertTaxonomyTestOrganization(t, db, "taxonomy-subtree-org")

	handler := &TaxonomyHandler{Store: store.NewEllieTaxonomyStore(db)}
	router := taxonomyTestRouter(handler)

	root := createTaxonomyNodeViaAPI(t, router, orgID, map[string]any{
		"slug":         "technical",
		"display_name": "Technical",
	})
	child := createTaxonomyNodeViaAPI(t, router, orgID, map[string]any{
		"parent_id":    root.ID,
		"slug":         "embeddings",
		"display_name": "Embeddings",
	})

	memoryID := insertTaxonomyMemory(t, db, orgID, "Embedding migration", "We switched to 1536d embeddings.")
	taxonomyStore := store.NewEllieTaxonomyStore(db)
	err := taxonomyStore.UpsertMemoryClassification(context.Background(), store.UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgID,
		MemoryID:   memoryID,
		NodeID:     child.ID,
		Confidence: 0.84,
	})
	require.NoError(t, err)

	memoriesReq := httptest.NewRequest(http.MethodGet, "/api/taxonomy/nodes/"+root.ID+"/memories?org_id="+orgID, nil)
	memoriesRec := httptest.NewRecorder()
	router.ServeHTTP(memoriesRec, memoriesReq)
	require.Equal(t, http.StatusOK, memoriesRec.Code)

	var payload struct {
		Memories []taxonomySubtreeMemoryResponse `json:"memories"`
	}
	require.NoError(t, json.NewDecoder(memoriesRec.Body).Decode(&payload))
	require.Len(t, payload.Memories, 1)
	require.Equal(t, memoryID, payload.Memories[0].MemoryID)
}

func taxonomyTestRouter(handler *TaxonomyHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Post("/api/taxonomy/nodes", handler.CreateNode)
	r.With(middleware.OptionalWorkspace).Get("/api/taxonomy/nodes", handler.ListNodes)
	r.With(middleware.OptionalWorkspace).Get("/api/taxonomy/nodes/{id}", handler.GetNode)
	r.With(middleware.OptionalWorkspace).Patch("/api/taxonomy/nodes/{id}", handler.PatchNode)
	r.With(middleware.OptionalWorkspace).Delete("/api/taxonomy/nodes/{id}", handler.DeleteNode)
	r.With(middleware.OptionalWorkspace).Get("/api/taxonomy/nodes/{id}/memories", handler.ListSubtreeMemories)
	return r
}

func createTaxonomyNodeViaAPI(t *testing.T, router http.Handler, orgID string, payload map[string]any) taxonomyNodeResponse {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/taxonomy/nodes?org_id="+orgID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)

	var node taxonomyNodeResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&node))
	return node
}

func insertTaxonomyTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()

	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		"Org "+slug,
		slug,
	).Scan(&orgID)
	require.NoError(t, err)
	return orgID
}

func insertTaxonomyMemory(t *testing.T, db *sql.DB, orgID, title, content string) string {
	t.Helper()

	var memoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, metadata, status)
		 VALUES ($1, 'fact', $2, $3, '{}'::jsonb, 'active')
		 RETURNING id`,
		orgID,
		title,
		content,
	).Scan(&memoryID)
	require.NoError(t, err)
	return memoryID
}
