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
	importer "github.com/samhotchkiss/otter-camp/internal/import"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationImportHistoryBatchEndpointWorkspaceScoped(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-history-authz-org")
	ownerID := insertTestUserWithRole(t, db, orgID, "owner-openclaw-import-history", RoleOwner)
	memberID := insertTestUserWithRole(t, db, orgID, "member-openclaw-import-history", RoleMember)

	ownerToken := "oc_sess_openclaw_import_history_owner"
	memberToken := "oc_sess_openclaw_import_history_member"
	insertTestSession(t, db, orgID, ownerID, ownerToken, time.Now().UTC().Add(time.Hour))
	insertTestSession(t, db, orgID, memberID, memberToken, time.Now().UTC().Add(time.Hour))

	requireOpenClawImportHistoryAgent(t, db, orgID, "main", "Frank")

	body := []byte(`{"user_id":"` + ownerID + `","batch":{"id":"batch-1","index":1,"total":1},"events":[{"agent_slug":"main","role":"assistant","body":"hello","created_at":"2026-01-01T10:00:00Z"}]}`)

	router := chi.NewRouter()
	handler := NewOpenClawMigrationImportHandler(db)
	router.With(middleware.RequireWorkspace, RequireCapability(db, CapabilityOpenClawMigrationManage)).
		Post("/api/migrations/openclaw/import/history/batch", handler.ImportHistoryBatch)

	noWorkspaceReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/history/batch", bytes.NewReader(body))
	noWorkspaceReq.Header.Set("Authorization", "Bearer "+ownerToken)
	noWorkspaceRec := httptest.NewRecorder()
	router.ServeHTTP(noWorkspaceRec, noWorkspaceReq)
	require.Equal(t, http.StatusUnauthorized, noWorkspaceRec.Code)

	noAuthReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/history/batch", bytes.NewReader(body))
	noAuthReq.Header.Set("X-Workspace-ID", orgID)
	noAuthRec := httptest.NewRecorder()
	router.ServeHTTP(noAuthRec, noAuthReq)
	require.Equal(t, http.StatusUnauthorized, noAuthRec.Code)

	memberReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/history/batch", bytes.NewReader(body))
	memberReq.Header.Set("Authorization", "Bearer "+memberToken)
	memberReq.Header.Set("X-Workspace-ID", orgID)
	memberRec := httptest.NewRecorder()
	router.ServeHTTP(memberRec, memberReq)
	require.Equal(t, http.StatusForbidden, memberRec.Code)

	var forbidden forbiddenCapabilityResponse
	require.NoError(t, json.NewDecoder(memberRec.Body).Decode(&forbidden))
	require.Equal(t, CapabilityOpenClawMigrationManage, forbidden.Capability)

	ownerReq := httptest.NewRequest(http.MethodPost, "/api/migrations/openclaw/import/history/batch", bytes.NewReader(body))
	ownerReq.Header.Set("Authorization", "Bearer "+ownerToken)
	ownerReq.Header.Set("X-Workspace-ID", orgID)
	ownerRec := httptest.NewRecorder()
	router.ServeHTTP(ownerRec, ownerReq)
	require.Equal(t, http.StatusOK, ownerRec.Code)
}

func TestOpenClawMigrationImportHistoryBatchEndpointIdempotent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-history-idempotent-org")
	userID := insertTestUserWithRole(t, db, orgID, "user-openclaw-import-history-idempotent", RoleOwner)
	handler := NewOpenClawMigrationImportHandler(db)

	requireOpenClawImportHistoryAgent(t, db, orgID, "main", "Frank")

	payload := `{"user_id":"` + userID + `","batch":{"id":"batch-retry","index":1,"total":2},"events":[{"agent_slug":"main","role":"user","body":"hello","created_at":"2026-01-01T10:00:01Z"},{"agent_slug":"main","role":"assistant","body":"hi","created_at":"2026-01-01T10:00:02Z"}]}`

	first := callOpenClawImportHistoryEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, first.EventsReceived)
	require.Equal(t, 2, first.EventsProcessed)
	require.Equal(t, 2, first.MessagesInserted)
	require.Equal(t, 1, first.RoomsCreated)
	require.Equal(t, 2, first.ParticipantsAdded)
	require.Equal(t, 0, first.FailedItems)

	second := callOpenClawImportHistoryEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, second.EventsReceived)
	require.Equal(t, 2, second.EventsProcessed)
	require.Equal(t, 0, second.MessagesInserted)
	require.Equal(t, 0, second.RoomsCreated)
	require.Equal(t, 0, second.ParticipantsAdded)
	require.Equal(t, 0, second.FailedItems)
}

func TestOpenClawMigrationImportHistoryBatchUpdatesProgress(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-history-progress-org")
	userID := insertTestUserWithRole(t, db, orgID, "user-openclaw-import-history-progress", RoleOwner)
	handler := NewOpenClawMigrationImportHandler(db)

	requireOpenClawImportHistoryAgent(t, db, orgID, "main", "Frank")

	payload := `{"user_id":"` + userID + `","batch":{"id":"batch-progress","index":1,"total":3},"events":[{"agent_slug":"main","role":"assistant","body":"known","created_at":"2026-01-01T10:00:01Z"},{"agent_slug":"codex","role":"assistant","body":"unknown","created_at":"2026-01-01T10:00:02Z"}]}`
	response := callOpenClawImportHistoryEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, response.EventsReceived)
	require.Equal(t, 1, response.EventsProcessed)
	require.Equal(t, 1, response.EventsSkippedUnknownAgent)
	require.Equal(t, 0, response.FailedItems)

	var status string
	var processed int
	var failed int
	var total sql.NullInt64
	err := db.QueryRow(
		`SELECT status, processed_items, failed_items, total_items
		   FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type = 'history_backfill'`,
		orgID,
	).Scan(&status, &processed, &failed, &total)
	require.NoError(t, err)
	require.Equal(t, "running", status)
	require.Equal(t, 2, processed)
	require.Equal(t, 0, failed)
	require.True(t, total.Valid)
	require.EqualValues(t, 3, total.Int64)
}

func TestOpenClawMigrationImportHistoryBatchUpdatesProgressFailedItemsOnInsertFailure(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-import-history-progress-failed-items-org")
	userID := insertTestUserWithRole(t, db, orgID, "user-openclaw-import-history-progress-failed-items", RoleOwner)
	handler := NewOpenClawMigrationImportHandler(db)

	requireOpenClawImportHistoryAgent(t, db, orgID, "main", "Frank")
	installOpenClawHistoryInsertFailureTriggerForAPITest(t, db)

	payload := `{"user_id":"` + userID + `","batch":{"id":"batch-progress-failed","index":1,"total":2},"events":[{"agent_slug":"main","role":"assistant","body":"bad [FORCE_HISTORY_INSERT_FAILURE] payload","created_at":"2026-01-01T10:00:01Z"},{"agent_slug":"main","role":"assistant","body":"valid","created_at":"2026-01-01T10:00:02Z"}]}`
	response := callOpenClawImportHistoryEndpoint(t, handler, orgID, payload)
	require.Equal(t, 2, response.EventsReceived)
	require.Equal(t, 1, response.EventsProcessed)
	require.Equal(t, 1, response.MessagesInserted)
	require.Equal(t, 1, response.FailedItems)

	var processed int
	var failed int
	err := db.QueryRow(
		`SELECT processed_items, failed_items
		   FROM migration_progress
		  WHERE org_id = $1
		    AND migration_type = 'history_backfill'`,
		orgID,
	).Scan(&processed, &failed)
	require.NoError(t, err)
	require.Equal(t, 2, processed)
	require.Equal(t, 1, failed)
}

func installOpenClawHistoryInsertFailureTriggerForAPITest(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		CREATE OR REPLACE FUNCTION test_openclaw_fail_chat_message_insert()
		RETURNS trigger AS $$
		BEGIN
			IF NEW.body LIKE '%[FORCE_HISTORY_INSERT_FAILURE]%' THEN
				RAISE EXCEPTION 'forced history insert failure for testing';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql;
	`)
	require.NoError(t, err)

	_, err = db.Exec(`DROP TRIGGER IF EXISTS test_openclaw_fail_chat_message_insert_trg ON chat_messages`)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TRIGGER test_openclaw_fail_chat_message_insert_trg
		BEFORE INSERT ON chat_messages
		FOR EACH ROW
		EXECUTE FUNCTION test_openclaw_fail_chat_message_insert()
	`)
	require.NoError(t, err)

	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS test_openclaw_fail_chat_message_insert_trg ON chat_messages`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS test_openclaw_fail_chat_message_insert()`)
	})
}

func requireOpenClawImportHistoryAgent(t *testing.T, db *sql.DB, orgID, id, name string) {
	t.Helper()
	_, err := importer.ImportOpenClawAgentsFromPayload(context.Background(), db, importer.OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []importer.ImportedAgentIdentity{
			{
				ID:       id,
				Name:     name,
				Soul:     "Role",
				Identity: "Identity",
			},
		},
	})
	require.NoError(t, err)
}

func callOpenClawImportHistoryEndpoint(
	t *testing.T,
	handler *OpenClawMigrationImportHandler,
	orgID string,
	body string,
) openClawMigrationImportHistoryBatchResponse {
	t.Helper()

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/migrations/openclaw/import/history/batch",
		bytes.NewBufferString(body),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ImportHistoryBatch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload openClawMigrationImportHistoryBatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	return payload
}
