package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
