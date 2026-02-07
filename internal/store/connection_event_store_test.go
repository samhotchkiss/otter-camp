package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConnectionEventStoreCreateAndList(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "connection-events-org")
	ctx := ctxWithWorkspace(orgID)

	eventStore := NewConnectionEventStore(db)
	created, err := eventStore.Create(ctx, CreateConnectionEventInput{
		EventType: "bridge.connected",
		Severity:  ConnectionEventSeverityInfo,
		Message:   "Bridge connected",
		Metadata:  json.RawMessage(`{"source":"ws"}`),
	})
	require.NoError(t, err)
	require.Equal(t, orgID, created.OrgID)
	require.Equal(t, "bridge.connected", created.EventType)
	require.Equal(t, ConnectionEventSeverityInfo, created.Severity)
	require.JSONEq(t, `{"source":"ws"}`, string(created.Metadata))

	time.Sleep(10 * time.Millisecond)

	_, err = eventStore.Create(ctx, CreateConnectionEventInput{
		EventType: "sync.failed",
		Severity:  ConnectionEventSeverityError,
		Message:   "Sync push failed",
		Metadata:  json.RawMessage(`{"status":502}`),
	})
	require.NoError(t, err)

	events, err := eventStore.List(ctx, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "sync.failed", events[0].EventType)
	require.Equal(t, "bridge.connected", events[1].EventType)
}

func TestConnectionEventStoreWorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "connection-events-org-a")
	orgB := createTestOrganization(t, db, "connection-events-org-b")

	store := NewConnectionEventStore(db)
	_, err := store.Create(ctxWithWorkspace(orgA), CreateConnectionEventInput{
		EventType: "bridge.connected",
		Message:   "A connected",
	})
	require.NoError(t, err)
	_, err = store.Create(ctxWithWorkspace(orgB), CreateConnectionEventInput{
		EventType: "bridge.connected",
		Message:   "B connected",
	})
	require.NoError(t, err)

	eventsA, err := store.List(ctxWithWorkspace(orgA), 10)
	require.NoError(t, err)
	require.Len(t, eventsA, 1)
	require.Equal(t, "A connected", eventsA[0].Message)

	eventsB, err := store.List(ctxWithWorkspace(orgB), 10)
	require.NoError(t, err)
	require.Len(t, eventsB, 1)
	require.Equal(t, "B connected", eventsB[0].Message)
}
