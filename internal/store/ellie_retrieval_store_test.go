package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieRetrievalStoreIncludesMemorySensitivity(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-memory-sensitivity-org")

	_, err := db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, sensitivity, status)
		 VALUES
		 ($1, 'fact', 'Normal Memory', 'normal memory content', 'normal', 'active'),
		 ($1, 'lesson', 'Sensitive Memory', 'sensitive memory content', 'sensitive', 'active')`,
		orgID,
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)
	memories, err := store.ListMemoriesForOrg(context.Background(), orgID, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(memories), 2)

	sensitivityByTitle := map[string]string{}
	for _, memory := range memories {
		sensitivityByTitle[memory.Title] = memory.Sensitivity
	}
	require.Equal(t, "normal", sensitivityByTitle["Normal Memory"])
	require.Equal(t, "sensitive", sensitivityByTitle["Sensitive Memory"])
}

func TestEllieRetrievalStoreIncludesConversationSensitivityInRoomAndHistory(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-conversation-sensitivity-org")
	projectID := createTestProject(t, db, orgID, "Ellie Retrieval Conversation Sensitivity Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-retrieval-conversation-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Ellie Retrieval Sensitivity Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 18, 0, 0, 0, time.UTC)

	var normalConversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES ($1, $2, 'Normal conversation', $3, $4, 'normal')
		 RETURNING id`,
		orgID,
		roomID,
		base,
		base.Add(2*time.Minute),
	).Scan(&normalConversationID)
	require.NoError(t, err)

	var sensitiveConversationID string
	err = db.QueryRow(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES ($1, $2, 'Sensitive conversation', $3, $4, 'sensitive')
		 RETURNING id`,
		orgID,
		roomID,
		base.Add(3*time.Minute),
		base.Add(5*time.Minute),
	).Scan(&sensitiveConversationID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, conversation_id, attachments)
		 VALUES
		 ($1, $2, $3, 'agent', 'normal conversation message', 'message', $4, $5, '[]'::jsonb),
		 ($1, $2, $3, 'agent', 'sensitive conversation message', 'message', $6, $7, '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
		base.Add(1*time.Minute),
		normalConversationID,
		base.Add(4*time.Minute),
		sensitiveConversationID,
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	conversations, err := store.ListRoomConversations(context.Background(), orgID, roomID, 10)
	require.NoError(t, err)
	require.Len(t, conversations, 2)

	sensitivityByTopic := map[string]string{}
	for _, conversation := range conversations {
		sensitivityByTopic[conversation.Topic] = conversation.Sensitivity
	}
	require.Equal(t, "normal", sensitivityByTopic["Normal conversation"])
	require.Equal(t, "sensitive", sensitivityByTopic["Sensitive conversation"])

	history, err := store.ListRoomConversationHistory(context.Background(), orgID, roomID, 10)
	require.NoError(t, err)
	require.Len(t, history, 2)

	sensitivityByBody := map[string]string{}
	for _, item := range history {
		require.NotNil(t, item.ConversationSensitivity)
		sensitivityByBody[item.Body] = *item.ConversationSensitivity
	}
	require.Equal(t, "normal", sensitivityByBody["normal conversation message"])
	require.Equal(t, "sensitive", sensitivityByBody["sensitive conversation message"])
}

func TestEllieRetrievalStoreProjectAndOrgScopes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "ellie-retrieval-scope-org-a")
	orgB := createTestOrganization(t, db, "ellie-retrieval-scope-org-b")

	projectA := createTestProject(t, db, orgA, "Ellie Retrieval Scope Project A")
	projectB := createTestProject(t, db, orgB, "Ellie Retrieval Scope Project B")

	var roomA string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Scope Room A', 'project', $2)
		 RETURNING id`,
		orgA,
		projectA,
	).Scan(&roomA)
	require.NoError(t, err)

	var roomB string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Scope Room B', 'project', $2)
		 RETURNING id`,
		orgB,
		projectB,
	).Scan(&roomB)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, sensitivity, status, source_project_id)
		 VALUES
		 ($1, 'fact', 'Org A project memory', 'org a memory', 'sensitive', 'active', $2),
		 ($3, 'fact', 'Org B project memory', 'org b memory', 'normal', 'active', $4)`,
		orgA,
		projectA,
		orgB,
		projectB,
	)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 19, 0, 0, 0, time.UTC)
	_, err = db.Exec(
		`INSERT INTO conversations (org_id, room_id, topic, started_at, ended_at, sensitivity)
		 VALUES
		 ($1, $2, 'Org A project conversation', $3, $4, 'sensitive'),
		 ($5, $6, 'Org B project conversation', $7, $8, 'normal')`,
		orgA,
		roomA,
		base,
		base.Add(3*time.Minute),
		orgB,
		roomB,
		base.Add(5*time.Minute),
		base.Add(8*time.Minute),
	)
	require.NoError(t, err)

	store := NewEllieRetrievalStore(db)

	memoriesA, err := store.ListProjectMemories(context.Background(), orgA, projectA, 10)
	require.NoError(t, err)
	require.Len(t, memoriesA, 1)
	require.Equal(t, "Org A project memory", memoriesA[0].Title)
	require.Equal(t, "sensitive", memoriesA[0].Sensitivity)

	conversationsA, err := store.ListProjectConversations(context.Background(), orgA, projectA, 10)
	require.NoError(t, err)
	require.Len(t, conversationsA, 1)
	require.Equal(t, "Org A project conversation", conversationsA[0].Topic)
	require.Equal(t, "sensitive", conversationsA[0].Sensitivity)

	memoriesCrossOrg, err := store.ListProjectMemories(context.Background(), orgA, projectB, 10)
	require.NoError(t, err)
	require.Empty(t, memoriesCrossOrg)

	conversationsCrossOrg, err := store.ListProjectConversations(context.Background(), orgA, projectB, 10)
	require.NoError(t, err)
	require.Empty(t, conversationsCrossOrg)
}
