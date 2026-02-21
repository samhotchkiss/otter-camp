package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationTokenRollups(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-token-rollup")
	roomID := insertConversationTokenTestRoom(t, db, orgID, "Token Room")
	conversationA := insertConversationTokenTestConversation(t, db, orgID, roomID, "Conversation A")
	conversationB := insertConversationTokenTestConversation(t, db, orgID, roomID, "Conversation B")

	body := "Token rollup should stay accurate across assignment transitions."
	expectedTokens := estimateConversationTestTokens(t, db, body)

	var messageID string
	err := db.QueryRow(
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			sender_id,
			sender_type,
			body,
			type,
			attachments
		) VALUES ($1, $2, gen_random_uuid(), 'user', $3, 'message', '[]'::jsonb)
		RETURNING id::text`,
		orgID,
		roomID,
		body,
	).Scan(&messageID)
	require.NoError(t, err)

	var tokenCount int
	err = db.QueryRow(`SELECT token_count FROM chat_messages WHERE id = $1`, messageID).Scan(&tokenCount)
	require.NoError(t, err)
	require.Equal(t, expectedTokens, tokenCount)
	require.Equal(t, int64(expectedTokens), loadConversationTokenRoomTotal(t, db, roomID))
	require.Equal(t, int64(0), loadConversationTokenConversationTotal(t, db, conversationA))

	_, err = db.Exec(`UPDATE chat_messages SET conversation_id = $1 WHERE id = $2`, conversationA, messageID)
	require.NoError(t, err)
	require.Equal(t, int64(expectedTokens), loadConversationTokenConversationTotal(t, db, conversationA))

	_, err = db.Exec(`UPDATE chat_messages SET conversation_id = $1 WHERE id = $2`, conversationB, messageID)
	require.NoError(t, err)
	require.Equal(t, int64(0), loadConversationTokenConversationTotal(t, db, conversationA))
	require.Equal(t, int64(expectedTokens), loadConversationTokenConversationTotal(t, db, conversationB))

	_, err = db.Exec(`DELETE FROM chat_messages WHERE id = $1`, messageID)
	require.NoError(t, err)
	require.Equal(t, int64(0), loadConversationTokenRoomTotal(t, db, roomID))
	require.Equal(t, int64(0), loadConversationTokenConversationTotal(t, db, conversationB))
}

func TestConversationTokenRollupsBackfillUpdateFromNullTokenCount(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-token-backfill")
	roomID := insertConversationTokenTestRoom(t, db, orgID, "Backfill Room")
	conversationID := insertConversationTokenTestConversation(t, db, orgID, roomID, "Backfill Conversation")

	body := "Backfill update should roll totals exactly once."
	expectedTokens := estimateConversationTestTokens(t, db, body)

	_, err := db.Exec(`ALTER TABLE chat_messages DISABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	})

	var messageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (
			org_id,
			room_id,
			conversation_id,
			sender_id,
			sender_type,
			body,
			type,
			token_count,
			attachments
		) VALUES ($1, $2, $3, gen_random_uuid(), 'user', $4, 'message', NULL, '[]'::jsonb)
		RETURNING id::text`,
		orgID,
		roomID,
		conversationID,
		body,
	).Scan(&messageID)
	require.NoError(t, err)

	_, err = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE chat_messages SET token_count = otter_estimate_token_count(body) WHERE id = $1`, messageID)
	require.NoError(t, err)
	require.Equal(t, int64(expectedTokens), loadConversationTokenRoomTotal(t, db, roomID))
	require.Equal(t, int64(expectedTokens), loadConversationTokenConversationTotal(t, db, conversationID))

	// Re-applying the same value should not double count.
	_, err = db.Exec(`UPDATE chat_messages SET token_count = otter_estimate_token_count(body) WHERE id = $1`, messageID)
	require.NoError(t, err)
	require.Equal(t, int64(expectedTokens), loadConversationTokenRoomTotal(t, db, roomID))
	require.Equal(t, int64(expectedTokens), loadConversationTokenConversationTotal(t, db, conversationID))
}

func insertConversationTokenTestRoom(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type)
		 VALUES ($1, $2, 'ad_hoc')
		 RETURNING id::text`,
		orgID,
		name,
	).Scan(&roomID)
	require.NoError(t, err)
	return roomID
}

func insertConversationTokenTestConversation(t *testing.T, db *sql.DB, orgID, roomID, topic string) string {
	t.Helper()
	var conversationID string
	err := db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at)
		 VALUES ($1, $2, $3, NOW())
		 RETURNING id::text`,
		orgID,
		roomID,
		topic,
	).Scan(&conversationID)
	require.NoError(t, err)
	return conversationID
}

func estimateConversationTestTokens(t *testing.T, db *sql.DB, body string) int {
	t.Helper()
	var count int
	err := db.QueryRow(`SELECT otter_estimate_token_count($1)`, body).Scan(&count)
	require.NoError(t, err)
	return count
}

func loadConversationTokenRoomTotal(t *testing.T, db *sql.DB, roomID string) int64 {
	t.Helper()
	var total int64
	err := db.QueryRow(`SELECT total_tokens FROM rooms WHERE id = $1`, roomID).Scan(&total)
	require.NoError(t, err)
	return total
}

func loadConversationTokenConversationTotal(t *testing.T, db *sql.DB, conversationID string) int64 {
	t.Helper()
	var total int64
	err := db.QueryRow(`SELECT total_tokens FROM conversations WHERE id = $1`, conversationID).Scan(&total)
	require.NoError(t, err)
	return total
}
