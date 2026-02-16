package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddingRoundTripAcross1536And768Dimensions(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "embedding-roundtrip-org")
	projectID := createTestProject(t, db, orgID, "Embedding Roundtrip Project")
	agentID := insertSchemaAgent(t, db, orgID, "embedding-roundtrip-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Embedding Roundtrip Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	var memory1536ID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Storage direction', 'The team selected Postgres for production persistence.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&memory1536ID)
	require.NoError(t, err)

	var message1536ID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, attachments)
		 VALUES ($1, $2, $3, 'agent', 'We selected Postgres after comparing storage options.', 'message', '[]'::jsonb)
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
	).Scan(&message1536ID)
	require.NoError(t, err)

	var memory768ID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Prototype persistence', 'Initial prototype used SQLite for fast local iteration.', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&memory768ID)
	require.NoError(t, err)

	var message768ID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, attachments)
		 VALUES ($1, $2, $3, 'agent', 'For the prototype, SQLite kept setup simple.', 'message', '[]'::jsonb)
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
	).Scan(&message768ID)
	require.NoError(t, err)

	queue1536 := NewConversationEmbeddingStoreWithDimension(db, 1536)
	queue768 := NewConversationEmbeddingStoreWithDimension(db, 768)

	vector1536 := ellieRetrievalEmbeddingVector1536(1, 0)
	vector768 := ellieRetrievalEmbeddingVector(1, 0)

	err = queue1536.UpdateMemoryEmbedding(context.Background(), memory1536ID, vector1536)
	require.NoError(t, err)
	err = queue1536.UpdateChatMessageEmbedding(context.Background(), message1536ID, vector1536)
	require.NoError(t, err)

	err = queue768.UpdateMemoryEmbedding(context.Background(), memory768ID, vector768)
	require.NoError(t, err)
	err = queue768.UpdateChatMessageEmbedding(context.Background(), message768ID, vector768)
	require.NoError(t, err)

	var (
		memory1536HasLegacy bool
		memory1536Has1536   bool
		memory768HasLegacy  bool
		memory768Has1536    bool
	)
	err = db.QueryRow(`SELECT embedding IS NOT NULL, embedding_1536 IS NOT NULL FROM memories WHERE id = $1`, memory1536ID).Scan(&memory1536HasLegacy, &memory1536Has1536)
	require.NoError(t, err)
	err = db.QueryRow(`SELECT embedding IS NOT NULL, embedding_1536 IS NOT NULL FROM memories WHERE id = $1`, memory768ID).Scan(&memory768HasLegacy, &memory768Has1536)
	require.NoError(t, err)
	require.False(t, memory1536HasLegacy)
	require.True(t, memory1536Has1536)
	require.True(t, memory768HasLegacy)
	require.False(t, memory768Has1536)

	retrievalStore := NewEllieRetrievalStore(db)

	mem1536Results, err := retrievalStore.SearchMemoriesByProjectWithEmbedding(context.Background(), orgID, projectID, "database choice", vector1536, 10)
	require.NoError(t, err)
	require.NotEmpty(t, mem1536Results)
	require.Equal(t, memory1536ID, mem1536Results[0].MemoryID)

	chat1536Results, err := retrievalStore.SearchChatHistoryWithEmbedding(context.Background(), orgID, "database choice", vector1536, 10)
	require.NoError(t, err)
	require.NotEmpty(t, chat1536Results)
	require.Equal(t, message1536ID, chat1536Results[0].MessageID)

	mem768Results, err := retrievalStore.SearchMemoriesByProjectWithEmbedding(context.Background(), orgID, projectID, "prototype storage", vector768, 10)
	require.NoError(t, err)
	require.NotEmpty(t, mem768Results)
	require.Equal(t, memory768ID, mem768Results[0].MemoryID)

	chat768Results, err := retrievalStore.SearchChatHistoryWithEmbedding(context.Background(), orgID, "prototype storage", vector768, 10)
	require.NoError(t, err)
	require.NotEmpty(t, chat768Results)
	require.Equal(t, message768ID, chat768Results[0].MessageID)
}
