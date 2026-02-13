package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieIngestionCursorStoreReadWrite(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-cursor-org")
	projectID := createTestProject(t, db, orgID, "Ellie Cursor Project")

	var roomID string
	err := db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Cursor Room', 'project', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&roomID)
	require.NoError(t, err)

	store := NewEllieIngestionStore(db)

	cursor, err := store.GetRoomCursor(context.Background(), orgID, roomID)
	require.NoError(t, err)
	require.Nil(t, cursor)

	lastMessageID := "11111111-1111-1111-1111-111111111111"
	lastMessageAt := time.Date(2026, 2, 12, 10, 0, 0, 0, time.UTC)

	err = store.UpsertRoomCursor(context.Background(), UpsertEllieRoomCursorInput{
		OrgID:                orgID,
		RoomID:               roomID,
		LastMessageID:        lastMessageID,
		LastMessageCreatedAt: lastMessageAt,
	})
	require.NoError(t, err)

	cursor, err = store.GetRoomCursor(context.Background(), orgID, roomID)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	require.Equal(t, lastMessageID, cursor.LastMessageID)
	require.Equal(t, lastMessageAt, cursor.LastMessageCreatedAt)
}

func TestEllieIngestionStoreListRoomsForIngestionSkipsUpToDateRooms(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-list-rooms-org")
	projectID := createTestProject(t, db, orgID, "Ellie List Rooms Project")

	createRoom := func(name string) string {
		t.Helper()
		var roomID string
		err := db.QueryRow(
			`INSERT INTO rooms (org_id, name, type, context_id)
			 VALUES ($1, $2, 'project', $3)
			 RETURNING id`,
			orgID,
			name,
			projectID,
		).Scan(&roomID)
		require.NoError(t, err)
		return roomID
	}

	insertMessage := func(roomID, body string, createdAt time.Time) string {
		t.Helper()
		var messageID string
		err := db.QueryRow(
			`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at)
			 VALUES ($1, $2, $3, 'agent', $4, 'message', $5)
			 RETURNING id`,
			orgID,
			roomID,
			"11111111-1111-1111-1111-111111111111",
			body,
			createdAt,
		).Scan(&messageID)
		require.NoError(t, err)
		return messageID
	}

	upToDateRoomID := createRoom("Up to date room")
	staleRoomID := createRoom("Stale room")
	noCursorRoomID := createRoom("No cursor room")

	base := time.Date(2026, 2, 12, 8, 0, 0, 0, time.UTC)
	upToDateMessageID := insertMessage(upToDateRoomID, "Current room message", base.Add(1*time.Minute))
	staleMessageID := insertMessage(staleRoomID, "Older stale room message", base.Add(2*time.Minute))
	_ = insertMessage(staleRoomID, "New stale room message", base.Add(4*time.Minute))
	_ = insertMessage(noCursorRoomID, "No cursor room message", base.Add(3*time.Minute))

	store := NewEllieIngestionStore(db)

	err := store.UpsertRoomCursor(context.Background(), UpsertEllieRoomCursorInput{
		OrgID:                orgID,
		RoomID:               upToDateRoomID,
		LastMessageID:        upToDateMessageID,
		LastMessageCreatedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	err = store.UpsertRoomCursor(context.Background(), UpsertEllieRoomCursorInput{
		OrgID:                orgID,
		RoomID:               staleRoomID,
		LastMessageID:        staleMessageID,
		LastMessageCreatedAt: base.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	candidates, err := store.ListRoomsForIngestion(context.Background(), 20)
	require.NoError(t, err)
	require.ElementsMatch(t, []EllieRoomIngestionCandidate{
		{OrgID: orgID, RoomID: staleRoomID},
		{OrgID: orgID, RoomID: noCursorRoomID},
	}, candidates)
}
