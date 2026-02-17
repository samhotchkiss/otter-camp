package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

func TestOpenClawMigrationReportEndpointReturnsCompletenessBreakdown(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-report-org")
	otherOrgID := insertMessageTestOrganization(t, db, "openclaw-migration-report-other-org")

	_, err := db.Exec(
		`INSERT INTO migration_progress (
			org_id,
			migration_type,
			status,
			total_items,
			processed_items,
			failed_items
		) VALUES (
			$1,
			'history_backfill',
			'completed',
			10,
			10,
			2
		)`,
		orgID,
	)
	require.NoError(t, err)

	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		orgID,
		"history_backfill",
		"batch-1",
		"main",
		"session-1",
		"event-1",
		"/tmp/main/session-1.jsonl",
		11,
		"insert_chat_message",
		"forced insert error",
		2,
	)
	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		orgID,
		"history_backfill",
		"batch-1",
		"codex",
		"session-2",
		"event-2",
		"/tmp/codex/session-2.jsonl",
		22,
		"skipped_unknown_agent",
		"unknown agent",
		1,
	)
	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		otherOrgID,
		"history_backfill",
		"batch-9",
		"other",
		"session-9",
		"event-9",
		"/tmp/other/session-9.jsonl",
		99,
		"insert_chat_message",
		"other org failure",
		1,
	)

	handler := NewOpenClawMigrationControlPlaneHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/api/migrations/openclaw/report", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Report(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		EventsExpected       int     `json:"events_expected"`
		EventsProcessed      int     `json:"events_processed"`
		MessagesInserted     int     `json:"messages_inserted"`
		EventsSkippedUnknown int     `json:"events_skipped_unknown_agent"`
		FailedItems          int     `json:"failed_items"`
		CompletenessRatio    float64 `json:"completeness_ratio"`
		IsComplete           bool    `json:"is_complete"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, 10, payload.EventsExpected)
	require.Equal(t, 7, payload.EventsProcessed)
	require.Equal(t, 7, payload.MessagesInserted)
	require.Equal(t, 1, payload.EventsSkippedUnknown)
	require.Equal(t, 2, payload.FailedItems)
	require.Equal(t, 1.0, payload.CompletenessRatio)
	require.True(t, payload.IsComplete)
}

func TestOpenClawMigrationFailuresEndpointWorkspaceScoped(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-failures-org")
	otherOrgID := insertMessageTestOrganization(t, db, "openclaw-migration-failures-other-org")

	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		orgID,
		"history_backfill",
		"batch-1",
		"main",
		"session-1",
		"event-failure",
		"/tmp/main/session-1.jsonl",
		11,
		"insert_chat_message",
		"forced insert error",
		3,
	)
	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		orgID,
		"history_backfill",
		"batch-1",
		"codex",
		"session-2",
		"event-skipped",
		"/tmp/codex/session-2.jsonl",
		12,
		"skipped_unknown_agent",
		"unknown agent",
		2,
	)
	insertOpenClawHistoryFailureRowForAPITest(
		t,
		db,
		otherOrgID,
		"history_backfill",
		"batch-9",
		"other",
		"session-9",
		"event-other-org",
		"/tmp/other/session-9.jsonl",
		99,
		"insert_chat_message",
		"other org failure",
		1,
	)

	handler := NewOpenClawMigrationControlPlaneHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/api/migrations/openclaw/failures?limit=10", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Failures(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []struct {
			OrgID        string `json:"org_id"`
			EventID      string `json:"event_id"`
			ErrorReason  string `json:"error_reason"`
			AttemptCount int    `json:"attempt_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, 1, payload.Total)
	require.Len(t, payload.Items, 1)
	require.Equal(t, orgID, payload.Items[0].OrgID)
	require.Equal(t, "event-failure", payload.Items[0].EventID)
	require.Equal(t, "insert_chat_message", payload.Items[0].ErrorReason)
	require.Equal(t, 3, payload.Items[0].AttemptCount)
}

func insertOpenClawHistoryFailureRowForAPITest(
	t *testing.T,
	db *sql.DB,
	orgID string,
	migrationType string,
	batchID string,
	agentSlug string,
	sessionID string,
	eventID string,
	sessionPath string,
	line int,
	errorReason string,
	errorMessage string,
	attemptCount int,
) {
	t.Helper()
	now := time.Now().UTC()
	_, err := db.Exec(
		`INSERT INTO openclaw_history_import_failures (
			org_id,
			migration_type,
			batch_id,
			agent_slug,
			session_id,
			event_id,
			session_path,
			line,
			message_id_candidate,
			error_reason,
			error_message,
			first_seen_at,
			last_seen_at,
			attempt_count
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			'00000000-0000-0000-0000-000000000777',
			$9, $10, $11, $12, $13
		)`,
		orgID,
		migrationType,
		batchID,
		agentSlug,
		sessionID,
		eventID,
		sessionPath,
		line,
		errorReason,
		errorMessage,
		now,
		now,
		attemptCount,
	)
	require.NoError(t, err)
}
