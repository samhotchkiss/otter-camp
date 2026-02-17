package api

import (
	"bytes"
	"context"
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

func TestOpenClawMigrationImportAgentsEndpointWorkspaceScoped(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-agents-authz-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-openclaw-import-agents", RoleOwner)
	memberID := insertTestUserWithRole(t, db, orgID, "member-openclaw-import-agents", RoleMember)

	ownerToken := "oc_sess_openclaw_import_agents_owner"
	memberToken := "oc_sess_openclaw_import_agents_member"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))
	insertTestSession(t, db, orgID, memberID, memberToken, time.Now().UTC().Add(time.Hour))

	router := chi.NewRouter()
	handler := NewOpenClawMigrationImportHandler(db)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/import/agents", handler.ImportAgents)

	body := []byte(`{"identities":[{"id":"main","name":"Frank","soul":"Chief of Staff","identity":"Frank Identity"}]}`)

	noWorkspaceReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/agents", bytes.NewReader(body))
	noWorkspaceReq.Header.Set("Authorization", "Bearer "+ownerToken)
	noWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(noWorkspaceRec, noWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, noWorkspaceRec.Code)

	noAuthReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/agents", bytes.NewReader(body))
	noAuthReq.Header.Set("X-Workspace-ID", orgID)
	noAuthRec := httptest.NewRecorder()
	router.ServeHTTP(noAuthRec, noAuthReq)
	require.Equal(t, http.StatusUnauthorized, noAuthRec.Code)

	memberReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/agents", bytes.NewReader(body))
	memberReq.Header.Set("Authorization", "Bearer "+memberToken)
	memberReq.Header.Set("X-Workspace-ID", orgID)
	memberRec := httptest.NewRecorder()
	router.ServeHTTP(memberRec, memberReq)
	require.Equal(t, http.StatusForbidden, memberRec.Code)

	var forbidden forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(memberRec.Body).Decode(&forbidden))
	require.Equal(t, CapabilityOpenClawMigrationManage, forbidden.Capability)

	ownerReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/agents", bytes.NewReader(body))
	ownerReq.Header.Set("Authorization", "Bearer "+ownerToken)
	ownerReq.Header.Set("X-Workspace-ID", orgID)
	ownerRec := httptest.NewRecorder()
	router.ServeHTTP(ownerRec, ownerReq)
	require.Equal(t, http.StatusOK, ownerRec.Code)
}

func TestOpenClawMigrationImportAgentsEndpointIdempotent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-agents-idempotent-org")
	handler := NewOpenClawMigrationImportHandler(db)

	payload := `{"identities":[{"id":"main","name":"Frank","soul":"Chief of Staff","identity":"Frank Identity"},{"id":"lori","name":"Lori","soul":"Agent Resources Director","identity":"Lori Identity"}]}`

	first := callOpenClawImportAgentsEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, first.Processed)
	require.Equal(t, 2, first.Inserted)
	require.Equal(t, 0, first.Updated)
	require.Equal(t, 0, first.Skipped)

	second := callOpenClawImportAgentsEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, second.Processed)
	require.Equal(t, 0, second.Inserted)
	require.Equal(t, 2, second.Updated)
	require.Equal(t, 0, second.Skipped)

	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM agents WHERE org_id = $1`, orgID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestOpenClawMigrationImportAgentsEndpointUpdatesProgress(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-agents-progress-org")
	handler := NewOpenClawMigrationImportHandler(db)

	payload := `{"identities":[{"id":"main","name":"Frank","soul":"Chief of Staff","identity":"Frank Identity"},{"name":"missing-id"}]}`
	response := callOpenClawImportAgentsEndpoint(t, handler, orgID, payload)
	require.Equal(t, 1, response.Processed)
	require.Equal(t, 1, response.Inserted)
	require.Equal(t, 0, response.Updated)
	require.Equal(t, 1, response.Skipped)

	var status string
	var processed int
	var failed int
	var total sql.NullInt64
	err := db.QueryRow(
		`SELECT status, processed_items, failed_items, total_items
		   FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type = 'agent_import'`,
		orgID,
	).Scan(&status, &processed, &failed, &total)
	require.NoError(t, err)
	require.Equal(t, "running", status)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, failed)
	require.True(t, total.Valid)
	require.EqualValues(t, 2, total.Int64)
}

func callOpenClawImportAgentsEndpoint(
	t *testing.T,
	handler *OpenClawMigrationImportHandler,
	orgID string,
	body string,
) openClawMigrationImportAgentsResponse {
	t.Helper()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/import/agents",
		bytes.NewBufferString(body),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ImportAgents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload openClawMigrationImportAgentsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	return payload
}
