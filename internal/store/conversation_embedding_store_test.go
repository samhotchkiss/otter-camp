package store

import (
	"context"
	"testing"
	"time"

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

func TestConversationEmbeddingQueueListPendingBalancesAcrossOrgs(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "conversation-embedding-fairness-org-a")
	orgB := createTestOrganization(t, db, "conversation-embedding-fairness-org-b")

	projectA := createTestProject(t, db, orgA, "Fairness Project A")
	projectB := createTestProject(t, db, orgB, "Fairness Project B")
	agentA := insertSchemaAgent(t, db, orgA, "embedding-fairness-agent-a")
	agentB := insertSchemaAgent(t, db, orgB, "embedding-fairness-agent-b")

	var roomA string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Fairness Room A', 'project', $2)
		 RETURNING id`,
		orgA,
		projectA,
	).Scan(&roomA)
	require.NoError(t, err)

	var roomB string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Fairness Room B', 'project', $2)
		 RETURNING id`,
		orgB,
		projectB,
	).Scan(&roomB)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i += 1 {
		_, err = db.Exec(
			`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
			 VALUES ($1, $2, $3, 'agent', $4, 'message', $5, '[]'::jsonb)`,
			orgA,
			roomA,
			agentA,
			"org-a pending chat",
			base.Add(time.Duration(i)*time.Minute),
		)
		require.NoError(t, err)
	}
	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'agent', 'org-b pending chat', 'message', $4, '[]'::jsonb)`,
		orgB,
		roomB,
		agentB,
		base.Add(30*time.Minute),
	)
	require.NoError(t, err)

	for i := 0; i < 5; i += 1 {
		_, err = db.Exec(
			`INSERT INTO memories (org_id, kind, title, content, status, created_at)
			 VALUES ($1, 'fact', $2, 'org-a pending memory', 'active', $3)`,
			orgA,
			"Org A memory",
			base.Add(time.Duration(i)*time.Minute),
		)
		require.NoError(t, err)
	}
	_, err = db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, status, created_at)
		 VALUES ($1, 'fact', 'Org B memory', 'org-b pending memory', 'active', $2)`,
		orgB,
		base.Add(30*time.Minute),
	)
	require.NoError(t, err)

	queue := NewConversationEmbeddingStore(db)

	chatPending, err := queue.ListPendingChatMessages(context.Background(), 4)
	require.NoError(t, err)
	require.Len(t, chatPending, 4)
	chatOrgs := map[string]bool{}
	for _, row := range chatPending {
		chatOrgs[row.OrgID] = true
	}
	require.True(t, chatOrgs[orgA])
	require.True(t, chatOrgs[orgB])

	memoryPending, err := queue.ListPendingMemories(context.Background(), 4)
	require.NoError(t, err)
	require.Len(t, memoryPending, 4)
	memoryOrgs := map[string]bool{}
	for _, row := range memoryPending {
		memoryOrgs[row.OrgID] = true
	}
	require.True(t, memoryOrgs[orgA])
	require.True(t, memoryOrgs[orgB])
}
