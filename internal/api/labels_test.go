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

func labelsTestRouter(handler *LabelsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/labels", handler.List)
	r.With(middleware.OptionalWorkspace).Post("/api/labels", handler.Create)
	r.With(middleware.OptionalWorkspace).Patch("/api/labels/{id}", handler.Patch)
	r.With(middleware.OptionalWorkspace).Delete("/api/labels/{id}", handler.Delete)
	return r
}

func TestLabelsHandlerCreateListUpdateDelete(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "labels-handler-org")
	handler := &LabelsHandler{Store: store.NewLabelStore(db)}
	router := labelsTestRouter(handler)

	t.Run("list empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/labels?org_id="+orgID, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var payload listLabelsResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.Len(t, payload.Labels, 0)
	})

	t.Run("create validation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/labels?org_id="+orgID, bytes.NewReader([]byte(`{"color":"#ef4444"}`)))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	var created store.Label
	t.Run("create success", func(t *testing.T) {
		body := []byte(`{"name":"bug","color":"#ef4444"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/labels?org_id="+orgID, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.Equal(t, "bug", created.Name)
		require.Equal(t, "#ef4444", created.Color)
		require.Equal(t, orgID, created.OrgID)
	})

	t.Run("create duplicate", func(t *testing.T) {
		body := []byte(`{"name":"bug","color":"#ef4444"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/labels?org_id="+orgID, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("patch update", func(t *testing.T) {
		body := []byte(`{"name":"type:bug","color":"#dc2626"}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/labels/"+created.ID+"?org_id="+orgID, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var updated store.Label
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&updated))
		require.Equal(t, created.ID, updated.ID)
		require.Equal(t, "type:bug", updated.Name)
		require.Equal(t, "#dc2626", updated.Color)
	})

	t.Run("list returns updated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/labels?org_id="+orgID, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var payload listLabelsResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.Len(t, payload.Labels, 1)
		require.Equal(t, "type:bug", payload.Labels[0].Name)
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/labels/"+created.ID+"?org_id="+orgID, nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("patch missing label", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/labels/"+created.ID+"?org_id="+orgID, bytes.NewReader([]byte(`{"name":"missing"}`)))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestLabelsHandlerRequiresWorkspace(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := &LabelsHandler{Store: store.NewLabelStore(db)}
	router := labelsTestRouter(handler)

	req := httptest.NewRequest(http.MethodGet, "/api/labels", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
