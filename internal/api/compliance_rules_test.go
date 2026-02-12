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

func newComplianceRuleTestRouter(handler *ComplianceRulesHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/compliance/rules", handler.List)
	router.With(middleware.OptionalWorkspace).Post("/api/compliance/rules", handler.Create)
	router.With(middleware.OptionalWorkspace).Patch("/api/compliance/rules/{id}", handler.Patch)
	router.With(middleware.OptionalWorkspace).Post("/api/compliance/rules/{id}/disable", handler.Disable)
	return router
}

func TestComplianceRuleHandlersCreateUpdateDisable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "compliance-rules-handler-org")
	projectID := insertProjectTestProject(t, db, orgID, "Compliance Rules API Project")

	handler := &ComplianceRulesHandler{Store: store.NewComplianceRuleStore(db)}
	router := newComplianceRuleTestRouter(handler)

	invalidReq := httptest.NewRequest(
		http.MethodPost,
		"/api/compliance/rules?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"title":"Bad category",
			"description":"invalid category should fail",
			"check_instruction":"check",
			"category":"bad_category",
			"severity":"required"
		}`)),
	)
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	require.Equal(t, http.StatusBadRequest, invalidRec.Code)

	createReq := httptest.NewRequest(
		http.MethodPost,
		"/api/compliance/rules?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"project_id":"`+projectID+`",
			"title":"All PRs need tests",
			"description":"Backend changes require tests",
			"check_instruction":"Verify new backend behavior has tests",
			"category":"code_quality",
			"severity":"required"
		}`)),
	)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code)

	var created complianceRulePayload
	require.NoError(t, json.NewDecoder(createRec.Body).Decode(&created))
	require.Equal(t, "All PRs need tests", created.Title)
	require.Equal(t, store.ComplianceRuleSeverityRequired, created.Severity)
	require.NotNil(t, created.ProjectID)
	require.Equal(t, projectID, *created.ProjectID)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/compliance/rules?org_id="+orgID+"&project_id="+projectID,
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listPayload complianceRuleListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listPayload))
	require.Equal(t, 1, listPayload.Total)
	require.Len(t, listPayload.Items, 1)

	updateReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/compliance/rules/"+created.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{
			"description":"Backend and persistence changes require tests",
			"severity":"recommended"
		}`)),
	)
	updateRec := httptest.NewRecorder()
	router.ServeHTTP(updateRec, updateReq)
	require.Equal(t, http.StatusOK, updateRec.Code)

	var updated complianceRulePayload
	require.NoError(t, json.NewDecoder(updateRec.Body).Decode(&updated))
	require.Equal(t, "Backend and persistence changes require tests", updated.Description)
	require.Equal(t, store.ComplianceRuleSeverityRecommended, updated.Severity)

	invalidPatchReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/compliance/rules/"+created.ID+"?org_id="+orgID,
		bytes.NewReader([]byte(`{"severity":"definitely_not_valid"}`)),
	)
	invalidPatchRec := httptest.NewRecorder()
	router.ServeHTTP(invalidPatchRec, invalidPatchReq)
	require.Equal(t, http.StatusBadRequest, invalidPatchRec.Code)

	disableReq := httptest.NewRequest(
		http.MethodPost,
		"/api/compliance/rules/"+created.ID+"/disable?org_id="+orgID,
		nil,
	)
	disableRec := httptest.NewRecorder()
	router.ServeHTTP(disableRec, disableReq)
	require.Equal(t, http.StatusOK, disableRec.Code)

	var disabled complianceRulePayload
	require.NoError(t, json.NewDecoder(disableRec.Body).Decode(&disabled))
	require.False(t, disabled.Enabled)

	listAfterReq := httptest.NewRequest(
		http.MethodGet,
		"/api/compliance/rules?org_id="+orgID+"&project_id="+projectID,
		nil,
	)
	listAfterRec := httptest.NewRecorder()
	router.ServeHTTP(listAfterRec, listAfterReq)
	require.Equal(t, http.StatusOK, listAfterRec.Code)

	var afterPayload complianceRuleListResponse
	require.NoError(t, json.NewDecoder(listAfterRec.Body).Decode(&afterPayload))
	require.Equal(t, 0, afterPayload.Total)
	require.Len(t, afterPayload.Items, 0)
}

func TestComplianceRuleHandlersEnforceOrgScope(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "compliance-rules-scope-org-a")
	orgB := insertMessageTestOrganization(t, db, "compliance-rules-scope-org-b")
	projectA := insertProjectTestProject(t, db, orgA, "Compliance Scope Project A")
	projectB := insertProjectTestProject(t, db, orgB, "Compliance Scope Project B")

	ruleStore := store.NewComplianceRuleStore(db)
	ctxA := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgA)
	ctxB := context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgB)

	_, err := ruleStore.Create(ctxA, store.CreateComplianceRuleInput{
		OrgID:            orgA,
		ProjectID:        &projectA,
		Title:            "Org A Rule",
		Description:      "Org A only",
		CheckInstruction: "check org A",
		Category:         store.ComplianceRuleCategoryProcess,
		Severity:         store.ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)

	ruleB, err := ruleStore.Create(ctxB, store.CreateComplianceRuleInput{
		OrgID:            orgB,
		ProjectID:        &projectB,
		Title:            "Org B Rule",
		Description:      "Org B only",
		CheckInstruction: "check org B",
		Category:         store.ComplianceRuleCategorySecurity,
		Severity:         store.ComplianceRuleSeverityRequired,
	})
	require.NoError(t, err)

	handler := &ComplianceRulesHandler{Store: ruleStore}
	router := newComplianceRuleTestRouter(handler)

	crossOrgCreateReq := httptest.NewRequest(
		http.MethodPost,
		"/api/compliance/rules?org_id="+orgA,
		bytes.NewReader([]byte(`{
			"project_id":"`+projectB+`",
			"title":"Cross-org scope",
			"description":"should fail",
			"check_instruction":"should fail",
			"category":"scope",
			"severity":"required"
		}`)),
	)
	crossOrgCreateRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgCreateRec, crossOrgCreateReq)
	require.Equal(t, http.StatusBadRequest, crossOrgCreateRec.Code)

	crossOrgPatchReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/compliance/rules/"+ruleB.ID+"?org_id="+orgA,
		bytes.NewReader([]byte(`{"title":"Attempted cross-org patch"}`)),
	)
	crossOrgPatchRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgPatchRec, crossOrgPatchReq)
	require.Equal(t, http.StatusNotFound, crossOrgPatchRec.Code)

	crossOrgDisableReq := httptest.NewRequest(
		http.MethodPost,
		"/api/compliance/rules/"+ruleB.ID+"/disable?org_id="+orgA,
		nil,
	)
	crossOrgDisableRec := httptest.NewRecorder()
	router.ServeHTTP(crossOrgDisableRec, crossOrgDisableReq)
	require.Equal(t, http.StatusNotFound, crossOrgDisableRec.Code)

	listReq := httptest.NewRequest(
		http.MethodGet,
		"/api/compliance/rules?org_id="+orgA+"&project_id="+projectB,
		nil,
	)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	require.Equal(t, http.StatusOK, listRec.Code)

	var listPayload complianceRuleListResponse
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&listPayload))
	require.Equal(t, 0, listPayload.Total)
	require.Len(t, listPayload.Items, 0)
}
