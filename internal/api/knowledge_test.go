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

func newKnowledgeTestRouter(handler *KnowledgeHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/knowledge", handler.List)
	r.With(middleware.OptionalWorkspace).Post("/api/knowledge/import", handler.Import)
	return r
}

func TestKnowledgeHandlerListEntries(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "knowledge-api-list-org")
	knowledgeStore := store.NewKnowledgeEntryStore(db)
	_, err := knowledgeStore.ReplaceEntries(
		context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID),
		[]store.ReplaceKnowledgeEntryInput{
			{
				Title:     "Stone knowledge 1",
				Content:   "Real dataset entry one",
				Tags:      []string{"writing", "stone"},
				CreatedBy: "Stone",
			},
			{
				Title:     "Stone knowledge 2",
				Content:   "Real dataset entry two",
				Tags:      []string{"ops"},
				CreatedBy: "Stone",
			},
		},
	)
	require.NoError(t, err)

	handler := &KnowledgeHandler{Store: knowledgeStore}
	router := newKnowledgeTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/knowledge?org_id="+orgID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var response knowledgeListResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&response))
	require.Equal(t, 2, response.Total)
	require.Len(t, response.Items, 2)
	require.Equal(t, "Stone knowledge 2", response.Items[0].Title)
}

func TestKnowledgeHandlerImportReplacesEntries(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "knowledge-api-import-org")
	handler := &KnowledgeHandler{Store: store.NewKnowledgeEntryStore(db)}
	router := newKnowledgeTestRouter(handler)

	firstImport := []byte(`{
		"entries": [
			{
				"title": "Dataset A",
				"content": "Alpha",
				"tags": ["data", "stone"],
				"created_by": "Stone"
			},
			{
				"title": "Dataset B",
				"content": "Beta",
				"tags": ["ops"],
				"created_by": "Stone"
			}
		]
	}`)
	firstReq := httptest.NewRequest(http.MethodPost, "/api/knowledge/import?org_id="+orgID, bytes.NewReader(firstImport))
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	require.Equal(t, http.StatusCreated, firstRec.Code)

	var firstResp knowledgeImportResponse
	require.NoError(t, json.NewDecoder(firstRec.Body).Decode(&firstResp))
	require.Equal(t, 2, firstResp.Inserted)

	secondImport := []byte(`{
		"entries": [
			{
				"title": "Dataset C",
				"content": "Gamma",
				"tags": ["writing"],
				"created_by": "Stone"
			}
		]
	}`)
	secondReq := httptest.NewRequest(http.MethodPost, "/api/knowledge/import?org_id="+orgID, bytes.NewReader(secondImport))
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	require.Equal(t, http.StatusCreated, secondRec.Code)

	listReq := httptest.NewRequest(http.MethodGet, "/api/knowledge?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp knowledgeListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Equal(t, 1, listResp.Total)
	require.Equal(t, "Dataset C", listResp.Items[0].Title)
}

func TestKnowledgeHandlerRequiresWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := &KnowledgeHandler{Store: store.NewKnowledgeEntryStore(db)}
	router := newKnowledgeTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/knowledge", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
