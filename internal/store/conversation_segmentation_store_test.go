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

func TestConversationSegmentationStoreDefaultsSensitivityToNormal(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-sensitivity-default-org")
	projectID := createTestProject(t, db, orgID, "Conversation Sensitivity Default Project")
	agentID := insertSchemaAgent(t, db, orgID, "conversation-sensitivity-default-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Sensitivity Default Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 12, 14, 0, 0, 0, time.UTC)
	var messageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, created_at, type)
		 VALUES ($1, $2, $3, 'agent', 'sensitivity default message', $4, 'message')
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
		startedAt,
	).Scan(&messageID)
	require.NoError(t, err)

	store := NewConversationSegmentationStore(db)
	conversationID, err := store.CreateConversationSegment(context.Background(), CreateConversationSegmentInput{
		OrgID:      orgID,
		RoomID:     roomID,
		Topic:      "Sensitivity Default Topic",
		StartedAt:  startedAt,
		EndedAt:    startedAt.Add(2 * time.Minute),
		MessageIDs: []string{messageID},
	})
	require.NoError(t, err)
	require.NotEmpty(t, conversationID)

	var sensitivity string
	err = db.QueryRow(`SELECT sensitivity FROM conversations WHERE id = $1`, conversationID).Scan(&sensitivity)
	require.NoError(t, err)
	require.Equal(t, "normal", sensitivity)
}

func TestConversationSegmentationStorePersistsSensitiveConversations(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-sensitivity-sensitive-org")
	projectID := createTestProject(t, db, orgID, "Conversation Sensitivity Sensitive Project")
	agentID := insertSchemaAgent(t, db, orgID, "conversation-sensitivity-sensitive-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Sensitivity Sensitive Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 12, 16, 0, 0, 0, time.UTC)
	var messageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, created_at, type)
		 VALUES ($1, $2, $3, 'agent', 'sensitivity sensitive message', $4, 'message')
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
		startedAt,
	).Scan(&messageID)
	require.NoError(t, err)

	store := NewConversationSegmentationStore(db)
	conversationID, err := store.CreateConversationSegment(context.Background(), CreateConversationSegmentInput{
		OrgID:       orgID,
		RoomID:      roomID,
		Topic:       "Sensitivity Sensitive Topic",
		StartedAt:   startedAt,
		EndedAt:     startedAt.Add(2 * time.Minute),
		MessageIDs:  []string{messageID},
		Sensitivity: "sensitive",
	})
	require.NoError(t, err)
	require.NotEmpty(t, conversationID)

	var sensitivity string
	err = db.QueryRow(`SELECT sensitivity FROM conversations WHERE id = $1`, conversationID).Scan(&sensitivity)
	require.NoError(t, err)
	require.Equal(t, "sensitive", sensitivity)
}

func TestConversationSegmentationStoreRejectsInvalidSensitivity(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "conversation-sensitivity-invalid-org")
	projectID := createTestProject(t, db, orgID, "Conversation Sensitivity Invalid Project")
	agentID := insertSchemaAgent(t, db, orgID, "conversation-sensitivity-invalid-agent")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Sensitivity Invalid Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	startedAt := time.Date(2026, 2, 12, 17, 0, 0, 0, time.UTC)
	var messageID string
	err = db.QueryRow(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, created_at, type)
		 VALUES ($1, $2, $3, 'agent', 'sensitivity invalid message', $4, 'message')
		 RETURNING id`,
		orgID,
		roomID,
		agentID,
		startedAt,
	).Scan(&messageID)
	require.NoError(t, err)

	store := NewConversationSegmentationStore(db)
	_, err = store.CreateConversationSegment(context.Background(), CreateConversationSegmentInput{
		OrgID:       orgID,
		RoomID:      roomID,
		Topic:       "Sensitivity Invalid Topic",
		StartedAt:   startedAt,
		EndedAt:     startedAt.Add(2 * time.Minute),
		MessageIDs:  []string{messageID},
		Sensitivity: "top-secret",
	})
	require.ErrorContains(t, err, "invalid sensitivity")
}

func TestConversationSegmentationQueueListPendingBalancesAcrossOrgs(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "conversation-segmentation-fairness-org-a")
	orgB := createTestOrganization(t, db, "conversation-segmentation-fairness-org-b")
	projectA := createTestProject(t, db, orgA, "Segmentation Fairness Project A")
	projectB := createTestProject(t, db, orgB, "Segmentation Fairness Project B")
	agentA := insertSchemaAgent(t, db, orgA, "seg-fairness-agent-a")
	agentB := insertSchemaAgent(t, db, orgB, "seg-fairness-agent-b")

	roomAID := "00000000-0000-0000-0000-0000000000a1"
	_, err := db.Exec(
		`INSERT INTO rooms (id, org_id, name, type, context_id)
		 VALUES ($1, $2, 'Segmentation Fairness Room A', 'project', $3)`,
		roomAID,
		orgA,
		projectA,
	)
	require.NoError(t, err)

	roomBID := "00000000-0000-0000-0000-0000000000b1"
	_, err = db.Exec(
		`INSERT INTO rooms (id, org_id, name, type, context_id)
		 VALUES ($1, $2, 'Segmentation Fairness Room B', 'project', $3)`,
		roomBID,
		orgB,
		projectB,
	)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 11, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i += 1 {
		_, err = db.Exec(
			`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
			 VALUES ($1, $2, $3, 'agent', 'org-a pending segmentation', 'message', $4, '[]'::jsonb)`,
			orgA,
			roomAID,
			agentA,
			base.Add(time.Duration(i)*time.Minute),
		)
		require.NoError(t, err)
	}
	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'agent', 'org-b pending segmentation', 'message', $4, '[]'::jsonb)`,
		orgB,
		roomBID,
		agentB,
		base.Add(30*time.Minute),
	)
	require.NoError(t, err)

	queue := NewConversationSegmentationStore(db)
	pending, err := queue.ListPendingConversationMessages(context.Background(), 4)
	require.NoError(t, err)
	require.Len(t, pending, 4)

	orgs := map[string]bool{}
	for _, row := range pending {
		orgs[row.OrgID] = true
	}
	require.True(t, orgs[orgA])
	require.True(t, orgs[orgB])
}
