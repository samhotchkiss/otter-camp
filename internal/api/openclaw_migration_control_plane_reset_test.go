package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationResetEndpointRequiresConfirmAndCapability(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-reset-authz-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-openclaw-migration-reset", RoleOwner)
	memberID := insertTestUserWithRole(t, db, orgID, "member-openclaw-migration-reset", RoleMember)

	ownerToken := "oc_sess_openclaw_migration_reset_owner"
	memberToken := "oc_sess_openclaw_migration_reset_member"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))
	insertTestSession(t, db, orgID, memberID, memberToken, time.Now().UTC().Add(time.Hour))

	router := chi.NewRouter()
	handler := NewOpenClawMigrationControlPlaneHandler(db)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/reset", handler.Reset)

	validBody := []byte(`{"confirm":"RESET_OPENCLAW_MIGRATION"}`)

	noWorkspaceReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/reset", bytes.NewReader(validBody))
	noWorkspaceReq.Header.Set("Authorization", "Bearer "+ownerToken)
	noWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(noWorkspaceRec, noWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, noWorkspaceRec.Code)

	noAuthReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/reset", bytes.NewReader(validBody))
	noAuthReq.Header.Set("X-Workspace-ID", orgID)
	noAuthRec := httptest.NewRecorder()
	router.ServeHTTP(noAuthRec, noAuthReq)
	require.Equal(t, http.StatusUnauthorized, noAuthRec.Code)

	memberReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/reset", bytes.NewReader(validBody))
	memberReq.Header.Set("Authorization", "Bearer "+memberToken)
	memberReq.Header.Set("X-Workspace-ID", orgID)
	memberRec := httptest.NewRecorder()
	router.ServeHTTP(memberRec, memberReq)
	require.Equal(t, http.StatusForbidden, memberRec.Code)

	var forbidden forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(memberRec.Body).Decode(&forbidden))
	require.Equal(t, CapabilityOpenClawMigrationManage, forbidden.Capability)

	missingConfirmReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/reset", bytes.NewBufferString(`{}`))
	missingConfirmReq.Header.Set("Authorization", "Bearer "+ownerToken)
	missingConfirmReq.Header.Set("X-Workspace-ID", orgID)
	missingConfirmRec := httptest.NewRecorder()
	router.ServeHTTP(missingConfirmRec, missingConfirmReq)
	require.Equal(t, http.StatusBadRequest, missingConfirmRec.Code)

	wrongConfirmReq := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/reset",
		bytes.NewBufferString(`{"confirm":"WRONG_TOKEN"}`),
	)
	wrongConfirmReq.Header.Set("Authorization", "Bearer "+ownerToken)
	wrongConfirmReq.Header.Set("X-Workspace-ID", orgID)
	wrongConfirmRec := httptest.NewRecorder()
	router.ServeHTTP(wrongConfirmRec, wrongConfirmReq)
	require.Equal(t, http.StatusBadRequest, wrongConfirmRec.Code)

	ownerReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/reset", bytes.NewReader(validBody))
	ownerReq.Header.Set("Authorization", "Bearer "+ownerToken)
	ownerReq.Header.Set("X-Workspace-ID", orgID)
	ownerRec := httptest.NewRecorder()
	router.ServeHTTP(ownerRec, ownerReq)
	require.Equal(t, http.StatusOK, ownerRec.Code)
}

func TestOpenClawMigrationResetEndpointDeletesWorkspaceArtifactsOnly(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-reset-delete-org-a")
	otherOrgID := insertMessageTestOrganization(t, db, "openclaw-migration-reset-delete-org-b")

	seedOpenClawMigrationResetArtifactsAPI(t, db, orgID, "a")
	seedOpenClawMigrationResetArtifactsAPI(t, db, otherOrgID, "b")
	seedOpenClawMigrationResetProgressAPI(t, db, orgID)
	seedOpenClawMigrationResetProgressAPI(t, db, otherOrgID)

	handler := NewOpenClawMigrationControlPlaneHandler(db)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/reset",
		bytes.NewBufferString(`{"confirm":"RESET_OPENCLAW_MIGRATION"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Reset(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload openClawMigrationResetResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "reset", payload.Status)
	require.Equal(t, 8, payload.TotalDeleted)
	require.Equal(t, map[string]int{
		"chat_messages":         1,
		"conversations":         1,
		"room_participants":     1,
		"rooms":                 1,
		"memories":              1,
		"ellie_memory_taxonomy": 1,
		"ellie_taxonomy_nodes":  1,
		"ellie_project_docs":    1,
	}, payload.Deleted)

	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "chat_messages", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "conversations", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "room_participants", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "rooms", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "memories", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_memory_taxonomy", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_taxonomy_nodes", orgID))
	require.Equal(t, 0, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_project_docs", orgID))

	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "chat_messages", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "conversations", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "room_participants", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "rooms", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "memories", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_memory_taxonomy", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_taxonomy_nodes", otherOrgID))
	require.Equal(t, 1, countOpenClawMigrationResetScopedRowsAPI(t, db, "ellie_project_docs", otherOrgID))
}

func TestOpenClawMigrationResetEndpointClearsMigrationProgress(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-reset-progress-org")
	seedOpenClawMigrationResetProgressAPI(t, db, orgID)

	handler := NewOpenClawMigrationControlPlaneHandler(db)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/reset",
		bytes.NewBufferString(`{"confirm":"RESET_OPENCLAW_MIGRATION"}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Reset(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload openClawMigrationResetResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, 1, payload.PausedPhases)
	require.Equal(t, 3, payload.ProgressRowsDeleted)

	var openClawRows int
	err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type IN ('agent_import', 'history_backfill', 'memory_extraction')`,
		orgID,
	).Scan(&openClawRows)
	require.NoError(t, err)
	require.Equal(t, 0, openClawRows)

	var legacyRows int
	err = db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type = 'legacy_backfill'`,
		orgID,
	).Scan(&legacyRows)
	require.NoError(t, err)
	require.Equal(t, 1, legacyRows)
}

func seedOpenClawMigrationResetProgressAPI(t *testing.T, db *sql.DB, orgID string) {
	t.Helper()
	_, err := db.ExecContext(
		context.Background(),
		`INSERT INTO migration_progress (org_id, migration_type, status)
		 VALUES
		 ($1, 'agent_import', 'completed'),
		 ($1, 'history_backfill', 'running'),
		 ($1, 'memory_extraction', 'failed'),
		 ($1, 'legacy_backfill', 'running')`,
		orgID,
	)
	require.NoError(t, err)
}

func seedOpenClawMigrationResetArtifactsAPI(t *testing.T, db *sql.DB, orgID, seed string) {
	t.Helper()

	userID := insertTestUserWithRole(t, db, orgID, "user-openclaw-reset-"+seed, RoleOwner)

	var projectID string
	err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO projects (org_id, name, status)
		 VALUES ($1, $2, 'active')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Reset Project %s", seed),
	).Scan(&projectID)
	require.NoError(t, err)

	var roomID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO rooms (org_id, name, type)
		 VALUES ($1, $2, 'ad_hoc')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Reset Room %s", seed),
	).Scan(&roomID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'user')`,
		orgID,
		roomID,
		userID,
	)
	require.NoError(t, err)

	var conversationID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO conversations (org_id, room_id, topic, started_at)
		 VALUES ($1, $2, $3, NOW())
		 RETURNING id`,
		orgID,
		roomID,
		fmt.Sprintf("OpenClaw Conversation %s", seed),
	).Scan(&conversationID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, conversation_id)
		 VALUES ($1, $2, $3, 'user', $4, $5)`,
		orgID,
		roomID,
		userID,
		fmt.Sprintf("OpenClaw Message %s", seed),
		conversationID,
	)
	require.NoError(t, err)

	var memoryID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'fact', $2, $3, 'active')
		 RETURNING id`,
		orgID,
		fmt.Sprintf("OpenClaw Memory %s", seed),
		fmt.Sprintf("OpenClaw Memory Content %s", seed),
	).Scan(&memoryID)
	require.NoError(t, err)

	var nodeID string
	err = db.QueryRowContext(
		context.Background(),
		`INSERT INTO ellie_taxonomy_nodes (org_id, parent_id, slug, display_name)
		 VALUES ($1, NULL, $2, $3)
		 RETURNING id`,
		orgID,
		fmt.Sprintf("openclaw-reset-%s", seed),
		fmt.Sprintf("OpenClaw %s", seed),
	).Scan(&nodeID)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO ellie_memory_taxonomy (memory_id, node_id, confidence)
		 VALUES ($1, $2, 0.9)`,
		memoryID,
		nodeID,
	)
	require.NoError(t, err)

	_, err = db.ExecContext(
		context.Background(),
		`INSERT INTO ellie_project_docs (org_id, project_id, file_path, content_hash, is_active)
		 VALUES ($1, $2, $3, $4, true)`,
		orgID,
		projectID,
		fmt.Sprintf("/docs/%s.md", seed),
		fmt.Sprintf("hash-%s", seed),
	)
	require.NoError(t, err)
}

func countOpenClawMigrationResetScopedRowsAPI(t *testing.T, db *sql.DB, tableName, orgID string) int {
	t.Helper()

	var query string
	switch tableName {
	case "ellie_memory_taxonomy":
		query = `SELECT COUNT(*)
		           FROM ellie_memory_taxonomy emt
		           JOIN memories m ON m.id = emt.memory_id
		          WHERE m.org_id = $1`
	default:
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE org_id = $1", tableName)
	}

	var count int
	err := db.QueryRowContext(context.Background(), query, orgID).Scan(&count)
	require.NoError(t, err)
	return count
}
