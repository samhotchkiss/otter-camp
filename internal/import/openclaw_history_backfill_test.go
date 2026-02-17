package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func installOpenClawHistoryInsertFailureTrigger(t *testing.T, db *sql.DB) {
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

func TestOpenClawHistoryBackfillCreatesSingleRoomPerAgent(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-room-per-agent")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-room-per-agent")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentWorkspaceFixture(t, root, "lori", "Agent Resources Director", "Lori Identity", "tools-lori")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
		{"id": "lori", "name": "Lori"},
	})

	mainSessionDir := filepath.Join(root, "agents", "main", "sessions")
	loriSessionDir := filepath.Join(root, "agents", "lori", "sessions")
	require.NoError(t, os.MkdirAll(mainSessionDir, 0o755))
	require.NoError(t, os.MkdirAll(loriSessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(mainSessionDir, "main-a.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"main session a"}]}}`,
	})
	writeOpenClawSessionFixture(t, filepath.Join(mainSessionDir, "main-b.jsonl"), []string{
		`{"type":"message","id":"u2","timestamp":"2026-01-01T10:00:02Z","message":{"role":"user","content":[{"type":"text","text":"main session b"}]}}`,
	})
	writeOpenClawSessionFixture(t, filepath.Join(loriSessionDir, "lori-a.jsonl"), []string{
		`{"type":"message","id":"u3","timestamp":"2026-01-01T10:00:03Z","message":{"role":"user","content":[{"type":"text","text":"lori session a"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)

	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)

	result, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.RoomsCreated)
	require.Equal(t, 3, result.MessagesInserted)

	var roomCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM rooms
		  WHERE org_id = $1
		    AND type = 'ad_hoc'`,
		orgID,
	).Scan(&roomCount)
	require.NoError(t, err)
	require.Equal(t, 2, roomCount)

	var mainAgentID string
	err = db.QueryRow(`SELECT id::text FROM agents WHERE org_id = $1 AND slug = 'main'`, orgID).Scan(&mainAgentID)
	require.NoError(t, err)

	var mainRoomCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM rooms
		  WHERE org_id = $1
		    AND type = 'ad_hoc'
		    AND context_id = $2`,
		orgID,
		mainAgentID,
	).Scan(&mainRoomCount)
	require.NoError(t, err)
	require.Equal(t, 1, mainRoomCount)
}

func TestOpenClawHistoryBackfillAddsParticipantsAndMessagesChronologically(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-ordering")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-ordering")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	mainSessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(mainSessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(mainSessionDir, "main-ordering.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:05Z","message":{"role":"user","content":[{"type":"text","text":"Need a plan"}]}}`,
		`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:06Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"internal"},{"type":"text","text":"Here is the plan."}]}}`,
		`{"type":"message","id":"t1","timestamp":"2026-01-01T10:00:07Z","message":{"role":"toolResult","toolName":"read","content":[{"type":"text","text":"loaded 2 docs"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)

	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)

	_, err = BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)

	var roomID string
	err = db.QueryRow(
		`SELECT id::text
		   FROM rooms
		  WHERE org_id = $1
		    AND type = 'ad_hoc'
		  LIMIT 1`,
		orgID,
	).Scan(&roomID)
	require.NoError(t, err)

	participantRows, err := db.Query(
		`SELECT participant_id::text, participant_type
		   FROM room_participants
		  WHERE org_id = $1
		    AND room_id = $2
		  ORDER BY participant_type`,
		orgID,
		roomID,
	)
	require.NoError(t, err)
	defer participantRows.Close()

	participants := make([]string, 0, 2)
	for participantRows.Next() {
		var participantID string
		var participantType string
		require.NoError(t, participantRows.Scan(&participantID, &participantType))
		participants = append(participants, participantType)
	}
	require.NoError(t, participantRows.Err())
	require.Equal(t, []string{"agent", "user"}, participants)

	messageRows, err := db.Query(
		`SELECT sender_type, type, body, created_at
		   FROM chat_messages
		  WHERE org_id = $1
		    AND room_id = $2
		  ORDER BY created_at ASC, id ASC`,
		orgID,
		roomID,
	)
	require.NoError(t, err)
	defer messageRows.Close()

	type row struct {
		SenderType string
		Type       string
		Body       string
		CreatedAt  time.Time
	}
	messages := make([]row, 0, 3)
	for messageRows.Next() {
		var item row
		require.NoError(t, messageRows.Scan(&item.SenderType, &item.Type, &item.Body, &item.CreatedAt))
		messages = append(messages, item)
	}
	require.NoError(t, messageRows.Err())
	require.Len(t, messages, 3)

	require.Equal(t, "user", messages[0].SenderType)
	require.Equal(t, "message", messages[0].Type)
	require.Equal(t, "Need a plan", messages[0].Body)

	require.Equal(t, "agent", messages[1].SenderType)
	require.Equal(t, "message", messages[1].Type)
	require.Equal(t, "Here is the plan.", messages[1].Body)

	require.Equal(t, "system", messages[2].SenderType)
	require.Equal(t, "system", messages[2].Type)
	require.Contains(t, messages[2].Body, "Tool read result")
	require.Contains(t, messages[2].Body, "loaded 2 docs")

	require.True(t, messages[0].CreatedAt.Before(messages[1].CreatedAt))
	require.True(t, messages[1].CreatedAt.Before(messages[2].CreatedAt))
}

func TestOpenClawHistoryBackfillIsIdempotent(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-idempotent")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-idempotent")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	mainSessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(mainSessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(mainSessionDir, "main-idempotent.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"message","id":"a1","timestamp":"2026-01-01T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)

	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)

	first, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.RoomsCreated)
	require.Equal(t, 2, first.MessagesInserted)

	second, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)
	require.Equal(t, 0, second.RoomsCreated)
	require.Equal(t, 0, second.MessagesInserted)

	var roomCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE org_id = $1 AND type = 'ad_hoc'`, orgID).Scan(&roomCount)
	require.NoError(t, err)
	require.Equal(t, 1, roomCount)

	var participantCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM room_participants WHERE org_id = $1`, orgID).Scan(&participantCount)
	require.NoError(t, err)
	require.Equal(t, 2, participantCount)

	var messageCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE org_id = $1`, orgID).Scan(&messageCount)
	require.NoError(t, err)
	require.Equal(t, 2, messageCount)
}

func TestBackfillOpenClawHistoryFromBatchPayload(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-batch-payload")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-batch-payload")

	_, err := ImportOpenClawAgentsFromPayload(context.Background(), db, OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []ImportedAgentIdentity{
			{ID: "main", Name: "Frank", Soul: "Chief of Staff", Identity: "Frank Identity"},
		},
	})
	require.NoError(t, err)

	batchResult, err := BackfillOpenClawHistoryFromBatchPayload(
		context.Background(),
		db,
		OpenClawHistoryBatchPayloadOptions{
			OrgID:  orgID,
			UserID: userID,
			Batch: OpenClawHistoryBatch{
				ID:    "batch-1",
				Index: 1,
				Total: 3,
			},
			Events: []OpenClawSessionEvent{
				{
					AgentSlug: "main",
					Role:      OpenClawSessionEventRoleUser,
					Body:      "hello",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
				},
				{
					AgentSlug: "main",
					Role:      OpenClawSessionEventRoleAssistant,
					Body:      "hi",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC),
				},
				{
					AgentSlug: "codex",
					Role:      OpenClawSessionEventRoleAssistant,
					Body:      "unknown",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 3, 0, time.UTC),
				},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 3, batchResult.EventsReceived)
	require.Equal(t, 2, batchResult.EventsProcessed)
	require.Equal(t, 2, batchResult.MessagesInserted)
	require.Equal(t, 1, batchResult.RoomsCreated)
	require.Equal(t, 2, batchResult.ParticipantsAdded)
	require.Equal(t, 1, batchResult.EventsSkippedUnknownAgent)
	require.Equal(t, 0, batchResult.FailedItems)
}

func TestBackfillOpenClawHistoryFromBatchPayloadIdempotentRetry(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-batch-idempotent")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-batch-idempotent")

	_, err := ImportOpenClawAgentsFromPayload(context.Background(), db, OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []ImportedAgentIdentity{
			{ID: "main", Name: "Frank", Soul: "Chief of Staff", Identity: "Frank Identity"},
		},
	})
	require.NoError(t, err)

	events := []OpenClawSessionEvent{
		{
			AgentSlug: "main",
			Role:      OpenClawSessionEventRoleUser,
			Body:      "hello",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
		},
		{
			AgentSlug: "main",
			Role:      OpenClawSessionEventRoleAssistant,
			Body:      "hi",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC),
		},
	}

	first, err := BackfillOpenClawHistoryFromBatchPayload(
		context.Background(),
		db,
		OpenClawHistoryBatchPayloadOptions{
			OrgID:  orgID,
			UserID: userID,
			Batch: OpenClawHistoryBatch{
				ID:    "batch-retry",
				Index: 2,
				Total: 4,
			},
			Events: events,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 2, first.EventsReceived)
	require.Equal(t, 2, first.EventsProcessed)
	require.Equal(t, 2, first.MessagesInserted)
	require.Equal(t, 1, first.RoomsCreated)

	second, err := BackfillOpenClawHistoryFromBatchPayload(
		context.Background(),
		db,
		OpenClawHistoryBatchPayloadOptions{
			OrgID:  orgID,
			UserID: userID,
			Batch: OpenClawHistoryBatch{
				ID:    "batch-retry",
				Index: 2,
				Total: 4,
			},
			Events: events,
		},
	)
	require.NoError(t, err)
	require.Equal(t, 2, second.EventsReceived)
	require.Equal(t, 2, second.EventsProcessed)
	require.Equal(t, 0, second.MessagesInserted)
	require.Equal(t, 0, second.RoomsCreated)
}

func TestBackfillOpenClawHistoryFromBatchPayloadCountsFailedItems(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-batch-failed-items")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-batch-failed-items")

	_, err := ImportOpenClawAgentsFromPayload(context.Background(), db, OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []ImportedAgentIdentity{
			{ID: "main", Name: "Frank", Soul: "Chief of Staff", Identity: "Frank Identity"},
		},
	})
	require.NoError(t, err)

	result, err := BackfillOpenClawHistoryFromBatchPayload(
		context.Background(),
		db,
		OpenClawHistoryBatchPayloadOptions{
			OrgID:  orgID,
			UserID: userID,
			Batch: OpenClawHistoryBatch{
				ID:    "batch-failed-items",
				Index: 1,
				Total: 1,
			},
			Events: []OpenClawSessionEvent{
				{
					AgentSlug: "",
					Role:      OpenClawSessionEventRoleUser,
					Body:      "missing slug",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
				},
				{
					AgentSlug: "main",
					Role:      OpenClawSessionEventRoleAssistant,
					Body:      "",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC),
				},
				{
					AgentSlug: "main",
					Role:      OpenClawSessionEventRoleAssistant,
					Body:      "valid",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 3, 0, time.UTC),
				},
				{
					AgentSlug: "codex",
					Role:      OpenClawSessionEventRoleAssistant,
					Body:      "unknown",
					CreatedAt: time.Date(2026, 1, 1, 10, 0, 4, 0, time.UTC),
				},
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, 4, result.EventsReceived)
	require.Equal(t, 1, result.EventsProcessed)
	require.Equal(t, 1, result.MessagesInserted)
	require.Equal(t, 1, result.EventsSkippedUnknownAgent)
	require.Equal(t, 2, result.FailedItems)
	require.Len(t, result.Warnings, 2)
	require.Contains(t, result.Warnings[0], "missing agent slug")
	require.Contains(t, result.Warnings[1], "missing body")
}

func TestBackfillOpenClawHistoryContinuesAfterSingleMessageInsertFailure(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-continue-after-insert-failure")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-continue-after-insert-failure")

	_, err := ImportOpenClawAgentsFromPayload(context.Background(), db, OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []ImportedAgentIdentity{
			{ID: "main", Name: "Frank", Soul: "Chief of Staff", Identity: "Frank Identity"},
		},
	})
	require.NoError(t, err)
	installOpenClawHistoryInsertFailureTrigger(t, db)

	result, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:  orgID,
		UserID: userID,
		ParsedEvents: []OpenClawSessionEvent{
			{
				AgentSlug:   "main",
				SessionID:   "session-1",
				SessionPath: "/tmp/openclaw/agents/main/sessions/main-1.jsonl",
				EventID:     "event-fail",
				Role:        OpenClawSessionEventRoleAssistant,
				Body:        "bad [FORCE_HISTORY_INSERT_FAILURE] payload",
				CreatedAt:   time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
				Line:        11,
			},
			{
				AgentSlug:   "main",
				SessionID:   "session-1",
				SessionPath: "/tmp/openclaw/agents/main/sessions/main-1.jsonl",
				EventID:     "event-ok",
				Role:        OpenClawSessionEventRoleAssistant,
				Body:        "valid payload",
				CreatedAt:   time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC),
				Line:        12,
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.FailedItems)
	require.Equal(t, 1, result.MessagesInserted)
	require.Equal(t, 1, result.EventsProcessed)

	var ledgerCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM openclaw_history_import_failures
		  WHERE org_id = $1
		    AND migration_type = 'history_backfill'`,
		orgID,
	).Scan(&ledgerCount)
	require.NoError(t, err)
	require.Equal(t, 1, ledgerCount)

	var messageCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		   FROM chat_messages
		  WHERE org_id = $1`,
		orgID,
	).Scan(&messageCount)
	require.NoError(t, err)
	require.Equal(t, 1, messageCount)
}

func TestBackfillOpenClawHistoryRecordsFailureLedgerEntries(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-failure-ledger-recording")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-failure-ledger-recording")

	_, err := ImportOpenClawAgentsFromPayload(context.Background(), db, OpenClawAgentPayloadImportOptions{
		OrgID: orgID,
		Identities: []ImportedAgentIdentity{
			{ID: "main", Name: "Frank", Soul: "Chief of Staff", Identity: "Frank Identity"},
		},
	})
	require.NoError(t, err)
	installOpenClawHistoryInsertFailureTrigger(t, db)

	event := OpenClawSessionEvent{
		AgentSlug:   "main",
		SessionID:   "session-ledger",
		SessionPath: "/tmp/openclaw/agents/main/sessions/main-ledger.jsonl",
		EventID:     "event-fail-ledger",
		Role:        OpenClawSessionEventRoleAssistant,
		Body:        "bad [FORCE_HISTORY_INSERT_FAILURE] payload",
		CreatedAt:   time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
		Line:        21,
	}

	first, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: []OpenClawSessionEvent{event},
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.FailedItems)

	second, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: []OpenClawSessionEvent{event},
	})
	require.NoError(t, err)
	require.Equal(t, 1, second.FailedItems)

	var attemptCount int
	var messageIDCandidate string
	var errorReason string
	err = db.QueryRow(
		`SELECT attempt_count, message_id_candidate, error_reason
		   FROM openclaw_history_import_failures
		  WHERE org_id = $1
		    AND migration_type = 'history_backfill'
		    AND event_id = $2`,
		orgID,
		event.EventID,
	).Scan(&attemptCount, &messageIDCandidate, &errorReason)
	require.NoError(t, err)
	require.Equal(t, 2, attemptCount)
	require.NotEmpty(t, strings.TrimSpace(messageIDCandidate))
	require.Equal(t, "insert_chat_message", errorReason)
}

func TestOpenClawHistoryBackfillUsesUserDisplayNameInRoomName(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-display-name")
	userID := createOpenClawImportTestUserWithDisplayName(t, db, orgID, "history-display-name", "Alex Rivera")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})

	mainSessionDir := filepath.Join(root, "agents", "main", "sessions")
	require.NoError(t, os.MkdirAll(mainSessionDir, 0o755))
	writeOpenClawSessionFixture(t, filepath.Join(mainSessionDir, "main-display-name.jsonl"), []string{
		`{"type":"message","id":"u1","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
	})

	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)

	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events, err := ParseOpenClawSessionEvents(install)
	require.NoError(t, err)

	_, err = BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)

	var roomName string
	err = db.QueryRow(
		`SELECT name
		   FROM rooms
		  WHERE org_id = $1
		    AND type = 'ad_hoc'
		  LIMIT 1`,
		orgID,
	).Scan(&roomName)
	require.NoError(t, err)
	require.Equal(t, "Alex Rivera & Frank", roomName)
}

func TestStableOpenClawBackfillMessageIDUsesUUIDv5(t *testing.T) {
	event := OpenClawSessionEvent{
		AgentSlug:   "main",
		SessionID:   "session-1",
		EventID:     "event-1",
		Role:        OpenClawSessionEventRoleAssistant,
		CreatedAt:   time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
		Body:        "hello",
		Line:        7,
		SessionPath: "agents/main/sessions/session-1.jsonl",
	}

	first := stableOpenClawBackfillMessageID("org-1", event)
	second := stableOpenClawBackfillMessageID("org-1", event)
	require.Equal(t, first, second)

	parts := strings.Split(first, "-")
	require.Len(t, parts, 5)
	require.Len(t, parts[0], 8)
	require.Len(t, parts[1], 4)
	require.Len(t, parts[2], 4)
	require.Len(t, parts[3], 4)
	require.Len(t, parts[4], 12)
	require.Equal(t, "5", strings.ToLower(parts[2][:1]))
	require.Contains(t, "89ab", strings.ToLower(parts[3][:1]))
}

func TestOpenClawHistoryBackfillTracksUnknownAgentSkipCounts(t *testing.T) {
	connStr := getOpenClawImportTestDatabaseURL(t)
	db := setupOpenClawImportTestDatabase(t, connStr)

	orgID := createOpenClawImportTestOrganization(t, db, "openclaw-history-unknown-agent-skips")
	userID := createOpenClawImportTestUser(t, db, orgID, "sam-history-unknown-agent-skips")
	root := t.TempDir()

	writeOpenClawAgentWorkspaceFixture(t, root, "main", "Chief of Staff", "Frank Identity", "tools-main")
	writeOpenClawAgentConfigFixture(t, root, []map[string]any{
		{"id": "main", "name": "Frank"},
	})
	install, err := DetectOpenClawInstallation(DetectOpenClawOptions{HomeDir: root})
	require.NoError(t, err)
	_, err = ImportOpenClawAgents(context.Background(), db, OpenClawAgentImportOptions{
		OrgID:        orgID,
		Installation: install,
	})
	require.NoError(t, err)

	events := []OpenClawSessionEvent{
		{
			AgentSlug: "main",
			Role:      OpenClawSessionEventRoleUser,
			Body:      "known",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 1, 0, time.UTC),
		},
		{
			AgentSlug: "codex",
			Role:      OpenClawSessionEventRoleUser,
			Body:      "transient",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 2, 0, time.UTC),
		},
		{
			AgentSlug: "codex",
			Role:      OpenClawSessionEventRoleAssistant,
			Body:      "transient-assistant",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 3, 0, time.UTC),
		},
		{
			AgentSlug: "unknown-sub-agent",
			Role:      OpenClawSessionEventRoleUser,
			Body:      "other transient",
			CreatedAt: time.Date(2026, 1, 1, 10, 0, 4, 0, time.UTC),
		},
	}

	result, err := BackfillOpenClawHistory(context.Background(), db, OpenClawHistoryBackfillOptions{
		OrgID:        orgID,
		UserID:       userID,
		ParsedEvents: events,
	})
	require.NoError(t, err)
	require.Equal(t, 1, result.EventsProcessed)
	require.Equal(t, 1, result.MessagesInserted)
	require.Equal(t, 3, result.EventsSkippedUnknownAgent)
	require.Equal(t, map[string]int{
		"codex":             2,
		"unknown-sub-agent": 1,
	}, result.SkippedUnknownAgentCounts)
}

func createOpenClawImportTestUser(t *testing.T, db *sql.DB, orgID, key string) string {
	t.Helper()
	return createOpenClawImportTestUserWithDisplayName(t, db, orgID, key, "Sam "+key)
}

func createOpenClawImportTestUserWithDisplayName(t *testing.T, db *sql.DB, orgID, key, displayName string) string {
	t.Helper()
	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, subject, issuer, display_name, email)
		 VALUES ($1, $2, 'otter.dev', $3, $4)
		 RETURNING id::text`,
		orgID,
		"subject-"+key,
		displayName,
		key+"@example.com",
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}
