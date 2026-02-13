package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConversationTokenBackfillStore(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-token-backfill-store")
	roomID := insertConversationTokenTestRoom(t, db, orgID, "Backfill Store Room")
	conversationID := insertConversationTokenTestConversation(t, db, orgID, roomID, "Backfill Store Conversation")

	_, err := db.Exec(`ALTER TABLE chat_messages DISABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	})

	for _, body := range []string{
		"first message needs backfill",
		"second message needs backfill",
		"third message needs backfill",
	} {
		_, err = db.Exec(
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
			) VALUES ($1, $2, $3, gen_random_uuid(), 'user', $4, 'message', NULL, '[]'::jsonb)`,
			orgID,
			roomID,
			conversationID,
			body,
		)
		require.NoError(t, err)
	}

	_, err = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)

	tokenStore := NewConversationTokenStore(db)

	processed, err := tokenStore.BackfillMissingTokenCounts(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, 2, processed)

	processed, err = tokenStore.BackfillMissingTokenCounts(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	processed, err = tokenStore.BackfillMissingTokenCounts(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, 0, processed)

	var missing int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages
		 WHERE org_id = $1
		   AND token_count IS NULL`,
		orgID,
	).Scan(&missing)
	require.NoError(t, err)
	require.Equal(t, 0, missing)

	var roomTotal int64
	err = db.QueryRow(`SELECT total_tokens FROM rooms WHERE id = $1`, roomID).Scan(&roomTotal)
	require.NoError(t, err)
	require.Greater(t, roomTotal, int64(0))

	var conversationTotal int64
	err = db.QueryRow(`SELECT total_tokens FROM conversations WHERE id = $1`, conversationID).Scan(&conversationTotal)
	require.NoError(t, err)
	require.Greater(t, conversationTotal, int64(0))
}

func TestConversationTokenBackfillStoreOrgPartitionFairness(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "conversation-token-fairness-a")
	orgB := createTestOrganization(t, db, "conversation-token-fairness-b")
	roomA := insertConversationTokenTestRoom(t, db, orgA, "Fairness Room A")
	roomB := insertConversationTokenTestRoom(t, db, orgB, "Fairness Room B")
	conversationA := insertConversationTokenTestConversation(t, db, orgA, roomA, "Fairness Conversation A")
	conversationB := insertConversationTokenTestConversation(t, db, orgB, roomB, "Fairness Conversation B")

	_, err := db.Exec(`ALTER TABLE chat_messages DISABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	})

	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	insertNullTokenMessage := func(id, senderID, orgID, roomID, conversationID, body string) {
		t.Helper()
		_, insertErr := db.Exec(
			`INSERT INTO chat_messages (
				id,
				org_id,
				room_id,
				conversation_id,
				sender_id,
				sender_type,
				body,
				type,
				token_count,
				created_at,
				attachments
			) VALUES ($1, $2, $3, $4, $5, 'user', $6, 'message', NULL, $7, '[]'::jsonb)`,
			id,
			orgID,
			roomID,
			conversationID,
			senderID,
			body,
			base,
		)
		require.NoError(t, insertErr)
	}

	insertNullTokenMessage("00000000-0000-0000-0000-000000000101", "00000000-0000-0000-0000-000000000201", orgA, roomA, conversationA, "org A message 1")
	insertNullTokenMessage("00000000-0000-0000-0000-000000000102", "00000000-0000-0000-0000-000000000202", orgA, roomA, conversationA, "org A message 2")
	insertNullTokenMessage("00000000-0000-0000-0000-000000000103", "00000000-0000-0000-0000-000000000203", orgA, roomA, conversationA, "org A message 3")
	insertNullTokenMessage("ffffffff-ffff-ffff-ffff-ffffffffffff", "00000000-0000-0000-0000-000000000204", orgB, roomB, conversationB, "org B message 1")

	_, err = db.Exec(`ALTER TABLE chat_messages ENABLE TRIGGER chat_messages_token_rollup_trg`)
	require.NoError(t, err)

	tokenStore := NewConversationTokenStore(db)
	processed, err := tokenStore.BackfillMissingTokenCounts(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, 2, processed)

	var filledA int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages
		 WHERE org_id = $1
		   AND token_count IS NOT NULL`,
		orgA,
	).Scan(&filledA)
	require.NoError(t, err)
	require.Equal(t, 1, filledA)

	var filledB int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM chat_messages
		 WHERE org_id = $1
		   AND token_count IS NOT NULL`,
		orgB,
	).Scan(&filledB)
	require.NoError(t, err)
	require.Equal(t, 1, filledB)
}
