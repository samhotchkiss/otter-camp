package store

import (
	"context"
	"testing"

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
