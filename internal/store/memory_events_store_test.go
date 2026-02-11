package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryEventsStorePublishAndListFilters(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "memory-events-store-org")
	ctx := ctxWithWorkspace(orgID)

	store := NewMemoryEventsStore(db)

	first, err := store.Publish(ctx, PublishMemoryEventInput{
		EventType: MemoryEventTypeMemoryCreated,
		Payload:   []byte(`{"memory_id":"a1"}`),
	})
	require.NoError(t, err)
	require.Equal(t, MemoryEventTypeMemoryCreated, first.EventType)

	time.Sleep(5 * time.Millisecond)

	second, err := store.Publish(ctx, PublishMemoryEventInput{
		EventType: MemoryEventTypeCompactionDetected,
		Payload:   []byte(`{"session_key":"s1"}`),
	})
	require.NoError(t, err)
	require.Equal(t, MemoryEventTypeCompactionDetected, second.EventType)

	typeFiltered, err := store.List(ctx, ListMemoryEventsParams{
		Types: []string{MemoryEventTypeCompactionDetected},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, typeFiltered, 1)
	require.Equal(t, second.ID, typeFiltered[0].ID)

	sinceFiltered, err := store.List(ctx, ListMemoryEventsParams{
		Since: &first.CreatedAt,
		Limit: 10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, sinceFiltered)
	require.Equal(t, second.ID, sinceFiltered[0].ID)
}

func TestMemoryEventsStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "memory-events-org-a")
	orgB := createTestOrganization(t, db, "memory-events-org-b")

	store := NewMemoryEventsStore(db)

	_, err := store.Publish(ctxWithWorkspace(orgA), PublishMemoryEventInput{
		EventType: MemoryEventTypeKnowledgeShared,
		Payload:   []byte(`{"title":"Org A"}`),
	})
	require.NoError(t, err)
	_, err = store.Publish(ctxWithWorkspace(orgB), PublishMemoryEventInput{
		EventType: MemoryEventTypeKnowledgeShared,
		Payload:   []byte(`{"title":"Org B"}`),
	})
	require.NoError(t, err)

	orgAEvents, err := store.List(ctxWithWorkspace(orgA), ListMemoryEventsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, orgAEvents, 1)
	require.Contains(t, string(orgAEvents[0].Payload), "Org A")

	orgBEvents, err := store.List(ctxWithWorkspace(orgB), ListMemoryEventsParams{Limit: 10})
	require.NoError(t, err)
	require.Len(t, orgBEvents, 1)
	require.Contains(t, string(orgBEvents[0].Payload), "Org B")
}

func TestMemoryEventsStoreSupportsEvaluatorAndTunerEventTypes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "memory-events-new-types-org")
	ctx := ctxWithWorkspace(orgID)

	store := NewMemoryEventsStore(db)
	_, err := store.Publish(ctx, PublishMemoryEventInput{
		EventType: MemoryEventTypeMemoryEvaluated,
		Payload:   []byte(`{"evaluation_id":"eval-1"}`),
	})
	require.NoError(t, err)

	_, err = store.Publish(ctx, PublishMemoryEventInput{
		EventType: MemoryEventTypeMemoryTuned,
		Payload:   []byte(`{"attempt_id":"attempt-1"}`),
	})
	require.NoError(t, err)

	events, err := store.List(ctx, ListMemoryEventsParams{
		Types: []string{MemoryEventTypeMemoryEvaluated, MemoryEventTypeMemoryTuned},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
}
