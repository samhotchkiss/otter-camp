package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testEmbeddingVector(value float64) []float64 {
	vector := make([]float64, 384)
	for i := range vector {
		vector[i] = value
	}
	return vector
}

func TestEllieContextInjectionStoreListPendingMessages(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-context-injection-pending-org")
	projectID := createTestProject(t, db, orgID, "Ellie Context Injection Pending Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-context-injection-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Agent Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO room_participants (org_id, room_id, participant_id, participant_type)
		 VALUES ($1, $2, $3, 'agent')`,
		orgID,
		roomID,
		agentID,
	)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 15, 0, 0, 0, time.UTC)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'user', 'Need database context', 'message', $4, '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
		base,
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'system', 'system housekeeping', 'system', $4, '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
		base.Add(1*time.Minute),
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'agent', 'already injected', 'context_injection', $4, '[]'::jsonb)`,
		orgID,
		roomID,
		agentID,
		base.Add(2*time.Minute),
	)
	require.NoError(t, err)

	otherOrgID := createTestOrganization(t, db, "ellie-context-injection-pending-other-org")
	otherProjectID := createTestProject(t, db, otherOrgID, "Other Room")
	otherAgentID := insertSchemaAgent(t, db, otherOrgID, "other-agent")

	var humanOnlyRoomID string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Human Room', 'project', $2)
		 RETURNING id`,
		otherOrgID,
		otherProjectID,
	).Scan(&humanOnlyRoomID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'user', 'human only message', 'message', $4, '[]'::jsonb)`,
		otherOrgID,
		humanOnlyRoomID,
		otherAgentID,
		base,
	)
	require.NoError(t, err)

	store := NewEllieContextInjectionStore(db)
	pending, err := store.ListPendingMessagesSince(context.Background(), nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "Need database context", pending[0].Body)
	require.Equal(t, "message", pending[0].MessageType)
	require.False(t, pending[0].HasEmbedding)

	afterCreatedAt := pending[0].CreatedAt
	afterID := pending[0].MessageID
	pending, err = store.ListPendingMessagesSince(context.Background(), &afterCreatedAt, &afterID, 10)
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestEllieContextInjectionStoreSearchMemoryCandidatesByEmbedding(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-context-memory-search-org")

	var nearMemoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, importance)
		 VALUES ($1, 'technical_decision', 'Near memory', 'Use Postgres', 'active', 5)
		 RETURNING id`,
		orgID,
	).Scan(&nearMemoryID)
	require.NoError(t, err)

	var farMemoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, importance)
		 VALUES ($1, 'fact', 'Far memory', 'Use flat files', 'active', 2)
		 RETURNING id`,
		orgID,
	).Scan(&farMemoryID)
	require.NoError(t, err)

	queryVector := testEmbeddingVector(0.01)
	nearVector := testEmbeddingVector(0.01)
	farVector := testEmbeddingVector(-0.01)
	nearLiteral, err := formatVectorLiteral(nearVector)
	require.NoError(t, err)
	farLiteral, err := formatVectorLiteral(farVector)
	require.NoError(t, err)

	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, nearMemoryID, nearLiteral)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE memories SET embedding = $2::vector WHERE id = $1`, farMemoryID, farLiteral)
	require.NoError(t, err)

	store := NewEllieContextInjectionStore(db)
	candidates, err := store.SearchMemoryCandidatesByEmbedding(context.Background(), orgID, queryVector, 5)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	require.Equal(t, nearMemoryID, candidates[0].MemoryID)
	require.Equal(t, farMemoryID, candidates[1].MemoryID)
	require.Greater(t, candidates[0].Similarity, candidates[1].Similarity)
}

func TestEllieContextInjectionStoreCompactionAwareDedupe(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-context-dedupe-org")
	projectID := createTestProject(t, db, orgID, "Ellie Context Dedupe Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-context-dedupe-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id, last_compacted_at)
		 VALUES ($1, 'Dedupe Room', 'project', $2, $3)
		 RETURNING id`,
		orgID,
		projectID,
		time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC),
	).Scan(&roomID)
	require.NoError(t, err)

	var memoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'fact', 'Dedupe memory', 'Remember this preference', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&memoryID)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO context_injections (org_id, room_id, memory_id, injected_at)
		 VALUES ($1, $2, $3, $4)`,
		orgID,
		roomID,
		memoryID,
		time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	store := NewEllieContextInjectionStore(db)

	wasInjected, err := store.WasInjectedSinceCompaction(context.Background(), orgID, roomID, memoryID)
	require.NoError(t, err)
	require.False(t, wasInjected)

	err = store.RecordInjection(context.Background(), orgID, roomID, memoryID, time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	wasInjected, err = store.WasInjectedSinceCompaction(context.Background(), orgID, roomID, memoryID)
	require.NoError(t, err)
	require.True(t, wasInjected)

	_, err = db.Exec(`UPDATE rooms SET last_compacted_at = $2 WHERE id = $1`, roomID, time.Date(2026, 2, 12, 13, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	wasInjected, err = store.WasInjectedSinceCompaction(context.Background(), orgID, roomID, memoryID)
	require.NoError(t, err)
	require.False(t, wasInjected)

	err = store.RecordInjection(context.Background(), orgID, roomID, memoryID, time.Date(2026, 2, 12, 14, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	wasInjected, err = store.WasInjectedSinceCompaction(context.Background(), orgID, roomID, memoryID)
	require.NoError(t, err)
	require.True(t, wasInjected)

	var rowCount int
	err = db.QueryRow(`SELECT COUNT(*) FROM context_injections WHERE room_id = $1 AND memory_id = $2`, roomID, memoryID).Scan(&rowCount)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount)

	_ = agentID
}

func TestEllieContextInjectionStoreCreateInjectionMessage(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-context-create-msg-org")
	projectID := createTestProject(t, db, orgID, "Ellie Context Create Message Project")
	agentID := insertSchemaAgent(t, db, orgID, "ellie-context-create-msg-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Create Message Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	store := NewEllieContextInjectionStore(db)
	createdAt := time.Date(2026, 2, 12, 15, 5, 0, 0, time.UTC)
	messageID, err := store.CreateInjectionMessage(context.Background(), CreateEllieContextInjectionMessageInput{
		OrgID:      orgID,
		RoomID:     roomID,
		SenderID:   agentID,
		Body:       "📎 Context: Use Postgres with explicit migrations.",
		CreatedAt:  createdAt,
		MessageType: "context_injection",
	})
	require.NoError(t, err)
	require.NotEmpty(t, messageID)

	var (
		storedType       string
		storedSenderType string
		storedBody       string
		storedCreatedAt  time.Time
	)
	err = db.QueryRow(
		`SELECT type, sender_type, body, created_at
		 FROM chat_messages
		 WHERE id = $1`,
		messageID,
	).Scan(&storedType, &storedSenderType, &storedBody, &storedCreatedAt)
	require.NoError(t, err)
	require.Equal(t, "context_injection", storedType)
	require.Equal(t, "agent", storedSenderType)
	require.Equal(t, "📎 Context: Use Postgres with explicit migrations.", storedBody)
	require.True(t, storedCreatedAt.UTC().Equal(createdAt))
}
