package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConversationSegmentationQueueListAndAssign(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-segmentation-queue-org")
	projectID := createTestProject(t, db, orgID, "Conversation Segmentation Queue Project")
	agentID := insertSchemaAgent(t, db, orgID, "conversation-segmentation-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Segmentation Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 14, 0, 0, 0, time.UTC)

	var firstMessageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, created_at, type)
		 VALUES ($1, $2, $3, 'agent', 'first pending segmentation message', $4, 'message')
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
		base,
	).Scan(&firstMessageID)
	require.NoError(t, err)

	var secondMessageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, created_at, type)
		 VALUES ($1, $2, $3, 'agent', 'second pending segmentation message', $4, 'message')
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
		base.Add(5*time.Minute),
	).Scan(&secondMessageID)
	require.NoError(t, err)

	queue := NewConversationSegmentationStore(db)

	pending, err := queue.ListPendingConversationMessages(context.Background(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pending), 2)

	conversationID, err := queue.CreateConversationSegment(context.Background(), CreateConversationSegmentInput{
		OrgID:      orgID,
		RoomID:     roomID,
		Topic:      "Segmentation Topic",
		StartedAt:  base,
		EndedAt:    base.Add(5 * time.Minute),
		MessageIDs: []string{firstMessageID, secondMessageID},
	})
	require.NoError(t, err)
	require.NotEmpty(t, conversationID)

	var assignedConversationID string
	err = db.QueryRow(`SELECT conversation_id::text FROM chat_messages WHERE id = $1`, firstMessageID).Scan(&assignedConversationID)
	require.NoError(t, err)
	require.Equal(t, conversationID, assignedConversationID)

	err = db.QueryRow(`SELECT conversation_id::text FROM chat_messages WHERE id = $1`, secondMessageID).Scan(&assignedConversationID)
	require.NoError(t, err)
	require.Equal(t, conversationID, assignedConversationID)
}
