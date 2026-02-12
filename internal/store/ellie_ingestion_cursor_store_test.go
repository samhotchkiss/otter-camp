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
