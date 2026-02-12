package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConversationEmbeddingQueueListAndUpdate(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-embedding-queue-org")
	projectID := createTestProject(t, db, orgID, "Conversation Embedding Queue Project")
	agentID := insertSchemaAgent(t, db, orgID, "conversation-embedding-queue-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Queue Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var chatMessageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, attachments)
		 VALUES ($1, $2, $3, 'agent', 'pending chat embedding', 'message', '[]'::jsonb)
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
	).Scan(&chatMessageID)
	require.NoError(t, err)

	var memoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'fact', 'Pending memory title', 'pending memory embedding', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&memoryID)
	require.NoError(t, err)

	queue := NewConversationEmbeddingStore(db)

	chatPending, err := queue.ListPendingChatMessages(context.Background(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, chatPending)

	memoryPending, err := queue.ListPendingMemories(context.Background(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, memoryPending)

	vector := make([]float64, 384)
	for i := range vector {
		vector[i] = float64(i+1) / 1000.0
	}

	err = queue.UpdateChatMessageEmbedding(context.Background(), chatMessageID, vector)
	require.NoError(t, err)

	err = queue.UpdateMemoryEmbedding(context.Background(), memoryID, vector)
	require.NoError(t, err)

	var chatHasEmbedding bool
	err = db.QueryRow(`SELECT embedding IS NOT NULL FROM chat_messages WHERE id = $1`, chatMessageID).Scan(&chatHasEmbedding)
	require.NoError(t, err)
	require.True(t, chatHasEmbedding)

	var memoryHasEmbedding bool
	err = db.QueryRow(`SELECT embedding IS NOT NULL FROM memories WHERE id = $1`, memoryID).Scan(&memoryHasEmbedding)
	require.NoError(t, err)
	require.True(t, memoryHasEmbedding)
}
