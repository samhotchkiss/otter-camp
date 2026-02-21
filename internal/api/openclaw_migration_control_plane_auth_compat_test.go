package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationEndpointsRequireWorkspaceAndCapability(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-authz-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-openclaw-migration", RoleOwner)
	memberID := insertTestUserWithRole(t, db, orgID, "member-openclaw-migration", RoleMember)

	ownerToken := "oc_sess_openclaw_migration_owner"
	memberToken := "oc_sess_openclaw_migration_member"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))
	insertTestSession(t, db, orgID, memberID, memberToken, time.Now().UTC().Add(time.Hour))

	router := chi.NewRouter()
	handler := NewOpenClawMigrationControlPlaneHandler(db)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/run", handler.Run)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/pause", handler.Pause)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/resume", handler.Resume)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/reset", handler.Reset)

	routes := []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodPost, path: "/api/migrations/openclaw/run", body: []byte(`{}`)},
		{method: http.MethodPost, path: "/api/migrations/openclaw/pause", body: nil},
		{method: http.MethodPost, path: "/api/migrations/openclaw/resume", body: nil},
		{method: http.MethodPost, path: "/api/migrations/openclaw/reset", body: []byte(`{"confirm":"RESET_OPENCLAW_MIGRATION"}`)},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			noWorkspaceReq := httptest.NewRequest(route.method, route.path, bytes.NewReader(route.body))
			noWorkspaceReq.Header.Set("Authorization", "Bearer "+ownerToken)
			noWorkspaceRec := httptest.NewRecorder()
			router.ServeHTTP(noWorkspaceRec, noWorkspaceReq)
			require.Equal(t, http.StatusUnauthorized, noWorkspaceRec.Code)

			noAuthReq := httptest.NewRequest(route.method, route.path, bytes.NewReader(route.body))
			noAuthReq.Header.Set("X-Workspace-ID", orgID)
			noAuthRec := httptest.NewRecorder()
			router.ServeHTTP(noAuthRec, noAuthReq)
			require.Equal(t, http.StatusUnauthorized, noAuthRec.Code)

			memberReq := httptest.NewRequest(route.method, route.path, bytes.NewReader(route.body))
			memberReq.Header.Set("Authorization", "Bearer "+memberToken)
			memberReq.Header.Set("X-Workspace-ID", orgID)
			memberRec := httptest.NewRecorder()
			router.ServeHTTP(memberRec, memberReq)
			require.Equal(t, http.StatusForbidden, memberRec.Code)

			var forbiddenPayload forbiddenCapabilityResponse
			require.NoError(t, json.NewDecoder(memberRec.Body).Decode(&forbiddenPayload))
			require.Equal(t, CapabilityOpenClawMigrationManage, forbiddenPayload.Capability)

			ownerReq := httptest.NewRequest(route.method, route.path, bytes.NewReader(route.body))
			ownerReq.Header.Set("Authorization", "Bearer "+ownerToken)
			ownerReq.Header.Set("X-Workspace-ID", orgID)
			ownerRec := httptest.NewRecorder()
			router.ServeHTTP(ownerRec, ownerReq)
			require.NotEqual(t, http.StatusUnauthorized, ownerRec.Code)
			require.NotEqual(t, http.StatusForbidden, ownerRec.Code)
		})
	}
}

func TestLegacyMigrationStatusEndpointCompatibility(t *testing.T) {
	sourceBytes, err := os.ReadFile("router.go")
	require.NoError(t, err)

	source := string(sourceBytes)
	require.Contains(
		t,
		source,
		`r.With(middleware.RequireWorkspace).Get("/migrations/status", handleMigrationStatus(db))`,
	)
}
