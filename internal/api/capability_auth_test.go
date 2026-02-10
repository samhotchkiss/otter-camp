package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

func insertTestUserWithRole(t *testing.T, db *sql.DB, orgID, subject, role string) string {
	t.Helper()

	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, subject, issuer, display_name, email, role)
		 VALUES ($1, $2, 'openclaw', $3, $4, $5)
		 RETURNING id::text`,
		orgID,
		subject,
		"User "+subject,
		subject+"@example.com",
		role,
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}

func TestRequireCapabilityRejectsMissingAuthentication(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := RequireCapability(db, CapabilityGitHubPublish)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var payload map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "missing authentication", payload["error"])
}

func TestRequireCapabilityRejectsForbiddenRole(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "authz-forbidden-org")
	viewerID := insertTestUserWithRole(t, db, orgID, "viewer-1", RoleViewer)
	token := "oc_sess_authz_viewer"
	insertTestSession(t, db, orgID, viewerID, token, time.Now().UTC().Add(time.Hour))

	handler := RequireCapability(db, CapabilityGitHubPublish)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var payload forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "forbidden", payload.Error)
	require.Equal(t, CapabilityGitHubPublish, payload.Capability)
}

func TestRequireCapabilityAllowsExpectedRoles(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		capability string
		wantCode   int
	}{
		{name: "owner can manage integration", role: RoleOwner, capability: CapabilityGitHubIntegrationAdmin, wantCode: http.StatusNoContent},
		{name: "maintainer can publish", role: RoleMaintainer, capability: CapabilityGitHubPublish, wantCode: http.StatusNoContent},
		{name: "member can run manual sync", role: RoleMember, capability: CapabilityGitHubManualSync, wantCode: http.StatusNoContent},
		{name: "member cannot publish", role: RoleMember, capability: CapabilityGitHubPublish, wantCode: http.StatusForbidden},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db := setupMessageTestDB(t)
			orgID := insertMessageTestOrganization(t, db, "authz-allowed-org")
			userID := insertTestUserWithRole(t, db, orgID, "user-"+tc.role, tc.role)
			token := "oc_sess_authz_" + tc.role
			insertTestSession(t, db, orgID, userID, token, time.Now().UTC().Add(time.Hour))

			handler := RequireCapability(db, tc.capability)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, tc.wantCode, rec.Code)
			if tc.wantCode == http.StatusForbidden {
				var payload forbiddenCapabilityResponse
				require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
				require.Equal(t, tc.capability, payload.Capability)
			}
		})
	}
}

func TestRequireCapabilityRejectsWorkspaceMismatch(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "authz-workspace-org")
	otherOrgID := insertMessageTestOrganization(t, db, "authz-other-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-1", RoleOwner)
	token := "oc_sess_authz_owner"
	insertTestSession(t, db, orgID, ownerID, token, time.Now().UTC().Add(time.Hour))

	handler := middleware.OptionalWorkspace(RequireCapability(db, CapabilityGitHubManualSync)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Workspace-ID", otherOrgID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var payload forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "workspace mismatch", payload.Error)
	require.Equal(t, CapabilityGitHubManualSync, payload.Capability)
}

func TestAdminConfigMutationRoutesRequireAuthenticationAndAdminCapability(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-authz-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-admin-config", RoleOwner)
	viewerID := insertTestUserWithRole(t, db, orgID, "viewer-admin-config", RoleViewer)

	ownerToken := "oc_sess_admin_config_owner"
	viewerToken := "oc_sess_admin_config_viewer"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))
	insertTestSession(t, db, orgID, viewerID, viewerToken, time.Now().UTC().Add(time.Hour))

	router := chi.NewRouter()
	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPatch, path: "/api/admin/config"},
		{method: http.MethodPost, path: "/api/admin/config/release-gate"},
		{method: http.MethodPost, path: "/api/admin/config/cutover"},
		{method: http.MethodPost, path: "/api/admin/config/rollback"},
	} {
		router.With(middleware.OptionalWorkspace, RequireCapability(db, CapabilityAdminConfigManage)).MethodFunc(route.method, route.path, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPatch, path: "/api/admin/config"},
		{method: http.MethodPost, path: "/api/admin/config/release-gate"},
		{method: http.MethodPost, path: "/api/admin/config/cutover"},
		{method: http.MethodPost, path: "/api/admin/config/rollback"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			noAuthReq := httptest.NewRequest(route.method, route.path, nil)
			noAuthReq.Header.Set("X-Workspace-ID", orgID)
			noAuthRec := httptest.NewRecorder()
			router.ServeHTTP(noAuthRec, noAuthReq)
			require.Equal(t, http.StatusUnauthorized, noAuthRec.Code)

			viewerReq := httptest.NewRequest(route.method, route.path, nil)
			viewerReq.Header.Set("Authorization", "Bearer "+viewerToken)
			viewerReq.Header.Set("X-Workspace-ID", orgID)
			viewerRec := httptest.NewRecorder()
			router.ServeHTTP(viewerRec, viewerReq)
			require.Equal(t, http.StatusForbidden, viewerRec.Code)

			var forbiddenPayload forbiddenCapabilityResponse
			require.NoError(t, json.NewDecoder(viewerRec.Body).Decode(&forbiddenPayload))
			require.Equal(t, CapabilityAdminConfigManage, forbiddenPayload.Capability)

			ownerReq := httptest.NewRequest(route.method, route.path, nil)
			ownerReq.Header.Set("Authorization", "Bearer "+ownerToken)
			ownerReq.Header.Set("X-Workspace-ID", orgID)
			ownerRec := httptest.NewRecorder()
			router.ServeHTTP(ownerRec, ownerReq)
			require.Equal(t, http.StatusNoContent, ownerRec.Code)
		})
	}
}
