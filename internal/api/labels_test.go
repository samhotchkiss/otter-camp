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

func labelsTestRouter(handler *LabelsHandler) http.Handler {
	r := chi.NewRouter()
	r.With(middleware.OptionalWorkspace).Get("/api/labels", handler.List)
	r.With(middleware.OptionalWorkspace).Post("/api/labels", handler.Create)
	r.With(middleware.OptionalWorkspace).Patch("/api/labels/{id}", handler.Patch)
	r.With(middleware.OptionalWorkspace).Delete("/api/labels/{id}", handler.Delete)
	r.With(middleware.OptionalWorkspace).Get("/api/projects/{id}/labels", handler.ListProjectLabels)
	r.With(middleware.OptionalWorkspace).Post("/api/projects/{id}/labels", handler.AddProjectLabels)
	r.With(middleware.OptionalWorkspace).Delete("/api/projects/{id}/labels/{lid}", handler.RemoveProjectLabel)
	r.With(middleware.OptionalWorkspace).Get("/api/projects/{pid}/issues/{iid}/labels", handler.ListIssueLabels)
	r.With(middleware.OptionalWorkspace).Post("/api/projects/{pid}/issues/{iid}/labels", handler.AddIssueLabels)
	r.With(middleware.OptionalWorkspace).Delete("/api/projects/{pid}/issues/{iid}/labels/{lid}", handler.RemoveIssueLabel)
	return r
}

func insertLabelsTestIssue(t *testing.T, db *sql.DB, orgID, projectID string, number int, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES ($1, $2, $3, $4, 'open', 'local')
		 RETURNING id`,
		orgID,
		projectID,
		number,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestLabelsHandlerCreateListUpdateDelete(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "labels-handler-org")
	handler := &LabelsHandler{Store: store.NewLabelStore(db), DB: db}
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
	handler := &LabelsHandler{Store: store.NewLabelStore(db), DB: db}
	router := labelsTestRouter(handler)

	tests := []struct {
		method string
		target string
		body   []byte
	}{
		{method: http.MethodGet, target: "/api/labels"},
		{method: http.MethodGet, target: "/api/projects/11111111-1111-1111-1111-111111111111/labels"},
		{method: http.MethodPost, target: "/api/projects/11111111-1111-1111-1111-111111111111/labels", body: []byte(`{"label_ids":["11111111-1111-1111-1111-111111111111"]}`)},
		{method: http.MethodGet, target: "/api/projects/11111111-1111-1111-1111-111111111111/issues/11111111-1111-1111-1111-111111111111/labels"},
	}
	for _, tc := range tests {
		req := httptest.NewRequest(tc.method, tc.target, bytes.NewReader(tc.body))
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestProjectLabelsHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "labels-project-endpoint-org")
	otherOrgID := insertMessageTestOrganization(t, db, "labels-project-endpoint-other-org")
	projectID := insertProjectTestProject(t, db, orgID, "Labels API Project")
	otherOrgProjectID := insertProjectTestProject(t, db, otherOrgID, "Labels API Other Org Project")
	handler := &LabelsHandler{Store: store.NewLabelStore(db), DB: db}
	router := labelsTestRouter(handler)

	labelStore := store.NewLabelStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	labelA, err := labelStore.Create(ctx, "bug", "#ef4444")
	require.NoError(t, err)
	labelB, err := labelStore.Create(ctx, "feature", "#22c55e")
	require.NoError(t, err)
	otherOrgCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, otherOrgID)
	otherOrgLabel, err := labelStore.Create(otherOrgCtx, "other-org", "#6366f1")
	require.NoError(t, err)

	assignBody := []byte(`{"label_ids":["` + labelA.ID + `","` + labelB.ID + `"]}`)
	assignReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/labels?org_id="+orgID, bytes.NewReader(assignBody))
	assignRec := httptest.NewRecorder()
	router.ServeHTTP(assignRec, assignReq)
	require.Equal(t, http.StatusOK, assignRec.Code)

	var assignResp listLabelsResponse
	require.NoError(t, json.NewDecoder(assignRec.Body).Decode(&assignResp))
	require.Len(t, assignResp.Labels, 2)

	assignDupReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/labels?org_id="+orgID, bytes.NewReader([]byte(`{"label_ids":["`+labelA.ID+`"]}`)))
	assignDupRec := httptest.NewRecorder()
	router.ServeHTTP(assignDupRec, assignDupReq)
	require.Equal(t, http.StatusOK, assignDupRec.Code)

	var assignDupResp listLabelsResponse
	require.NoError(t, json.NewDecoder(assignDupRec.Body).Decode(&assignDupResp))
	require.Len(t, assignDupResp.Labels, 2)

	invalidProjectReq := httptest.NewRequest(http.MethodGet, "/api/projects/not-a-uuid/labels?org_id="+orgID, nil)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)

	invalidLabelReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/labels?org_id="+orgID, bytes.NewReader([]byte(`{"label_ids":["not-a-uuid"]}`)))
	invalidLabelRec := httptest.NewRecorder()
	router.ServeHTTP(invalidLabelRec, invalidLabelReq)
	require.Equal(t, http.StatusBadRequest, invalidLabelRec.Code)

	crossOrgReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/labels?org_id="+orgID, bytes.NewReader([]byte(`{"label_ids":["`+otherOrgLabel.ID+`"]}`)))
	crossOrgRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgRec, crossOrgReq)
	require.Equal(t, http.StatusNotFound, crossOrgRec.Code)

	crossOrgProjectReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+otherOrgProjectID+"/labels?org_id="+orgID, nil)
	crossOrgProjectRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgProjectRec, crossOrgProjectReq)
	require.Equal(t, http.StatusOK, crossOrgProjectRec.Code)

	var crossOrgProjectResp listLabelsResponse
	require.NoError(t, json.NewDecoder(crossOrgProjectRec.Body).Decode(&crossOrgProjectResp))
	require.Len(t, crossOrgProjectResp.Labels, 0)

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/labels?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp listLabelsResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Labels, 2)

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/labels/"+labelA.ID+"?org_id="+orgID, nil)
	removeRec := httptest.NewRecorder()
	router.ServeHTTP(removeRec, removeReq)
	require.Equal(t, http.StatusNoContent, removeRec.Code)

	removeAgainReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/labels/"+labelA.ID+"?org_id="+orgID, nil)
	removeAgainRec := httptest.NewRecorder()
	router.ServeHTTP(removeAgainRec, removeAgainReq)
	require.Equal(t, http.StatusNoContent, removeAgainRec.Code)

	listAfterReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/labels?org_id="+orgID, nil)
	listAfterRec := httptest.NewRecorder()
	router.ServeHTTP(listAfterRec, listAfterReq)
	require.Equal(t, http.StatusOK, listAfterRec.Code)

	var listAfterResp listLabelsResponse
	require.NoError(t, json.NewDecoder(listAfterRec.Body).Decode(&listAfterResp))
	require.Len(t, listAfterResp.Labels, 1)
	require.Equal(t, labelB.ID, listAfterResp.Labels[0].ID)
}

func TestIssueLabelsHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "labels-issue-endpoint-org")
	otherOrgID := insertMessageTestOrganization(t, db, "labels-issue-endpoint-other-org")
	projectID := insertProjectTestProject(t, db, orgID, "Labels API Issue Project")
	otherProjectID := insertProjectTestProject(t, db, orgID, "Labels API Other Project")
	otherOrgProjectID := insertProjectTestProject(t, db, otherOrgID, "Labels API Other Org Project")
	issueID := insertLabelsTestIssue(t, db, orgID, projectID, 1, "Issue Label Target")
	otherOrgIssueID := insertLabelsTestIssue(t, db, otherOrgID, otherOrgProjectID, 1, "Other Org Issue")
	handler := &LabelsHandler{Store: store.NewLabelStore(db), DB: db}
	router := labelsTestRouter(handler)

	labelStore := store.NewLabelStore(db)
	ctx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
	label, err := labelStore.Create(ctx, "needs-review", "#eab308")
	require.NoError(t, err)
	otherOrgCtx := context.WithValue(context.Background(), middleware.WorkspaceIDKey, otherOrgID)
	otherOrgLabel, err := labelStore.Create(otherOrgCtx, "other-org-needs-review", "#7c3aed")
	require.NoError(t, err)

	assignBody := []byte(`{"label_ids":["` + label.ID + `"]}`)
	assignReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/"+issueID+"/labels?org_id="+orgID, bytes.NewReader(assignBody))
	assignRec := httptest.NewRecorder()
	router.ServeHTTP(assignRec, assignReq)
	require.Equal(t, http.StatusOK, assignRec.Code)

	var assignResp listLabelsResponse
	require.NoError(t, json.NewDecoder(assignRec.Body).Decode(&assignResp))
	require.Len(t, assignResp.Labels, 1)
	require.Equal(t, label.ID, assignResp.Labels[0].ID)

	assignDupReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/"+issueID+"/labels?org_id="+orgID, bytes.NewReader(assignBody))
	assignDupRec := httptest.NewRecorder()
	router.ServeHTTP(assignDupRec, assignDupReq)
	require.Equal(t, http.StatusOK, assignDupRec.Code)

	var assignDupResp listLabelsResponse
	require.NoError(t, json.NewDecoder(assignDupRec.Body).Decode(&assignDupResp))
	require.Len(t, assignDupResp.Labels, 1)

	invalidIssueReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/issues/not-a-uuid/labels?org_id="+orgID, nil)
	invalidIssueRec := httptest.NewRecorder()
	router.ServeHTTP(invalidIssueRec, invalidIssueReq)
	require.Equal(t, http.StatusBadRequest, invalidIssueRec.Code)

	invalidLabelReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/"+issueID+"/labels?org_id="+orgID, bytes.NewReader([]byte(`{"label_ids":["not-a-uuid"]}`)))
	invalidLabelRec := httptest.NewRecorder()
	router.ServeHTTP(invalidLabelRec, invalidLabelReq)
	require.Equal(t, http.StatusBadRequest, invalidLabelRec.Code)

	crossOrgLabelReq := httptest.NewRequest(http.MethodPost, "/api/projects/"+projectID+"/issues/"+issueID+"/labels?org_id="+orgID, bytes.NewReader([]byte(`{"label_ids":["`+otherOrgLabel.ID+`"]}`)))
	crossOrgLabelRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgLabelRec, crossOrgLabelReq)
	require.Equal(t, http.StatusNotFound, crossOrgLabelRec.Code)

	listReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/issues/"+issueID+"/labels?org_id="+orgID, nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listResp listLabelsResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listResp))
	require.Len(t, listResp.Labels, 1)

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/issues/"+issueID+"/labels/"+label.ID+"?org_id="+orgID, nil)
	removeRec := httptest.NewRecorder()
	router.ServeHTTP(removeRec, removeReq)
	require.Equal(t, http.StatusNoContent, removeRec.Code)

	removeAgainReq := httptest.NewRequest(http.MethodDelete, "/api/projects/"+projectID+"/issues/"+issueID+"/labels/"+label.ID+"?org_id="+orgID, nil)
	removeAgainRec := httptest.NewRecorder()
	router.ServeHTTP(removeAgainRec, removeAgainReq)
	require.Equal(t, http.StatusNoContent, removeAgainRec.Code)

	mismatchReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+otherProjectID+"/issues/"+issueID+"/labels?org_id="+orgID, nil)
	mismatchRec := httptest.NewRecorder()
	router.ServeHTTP(mismatchRec, mismatchReq)
	require.Equal(t, http.StatusNotFound, mismatchRec.Code)

	crossOrgIssueReq := httptest.NewRequest(http.MethodGet, "/api/projects/"+otherOrgProjectID+"/issues/"+otherOrgIssueID+"/labels?org_id="+orgID, nil)
	crossOrgIssueRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgIssueRec, crossOrgIssueReq)
	require.Equal(t, http.StatusNotFound, crossOrgIssueRec.Code)
}
