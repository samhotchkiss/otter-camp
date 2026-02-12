package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieIngestionStore struct {
	mu sync.Mutex

	rooms    []store.EllieRoomIngestionCandidate
	messages map[string][]store.EllieIngestionMessage
	cursors  map[string]store.EllieRoomCursor
	created  []store.CreateEllieExtractedMemoryInput
}

func newFakeEllieIngestionStore() *fakeEllieIngestionStore {
	return &fakeEllieIngestionStore{
		rooms:    make([]store.EllieRoomIngestionCandidate, 0),
		messages: make(map[string][]store.EllieIngestionMessage),
		cursors:  make(map[string]store.EllieRoomCursor),
		created:  make([]store.CreateEllieExtractedMemoryInput, 0),
	}
}

func roomCursorKey(orgID, roomID string) string {
	return orgID + ":" + roomID
}

func (f *fakeEllieIngestionStore) ListRoomsForIngestion(_ context.Context, _ int) ([]store.EllieRoomIngestionCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.EllieRoomIngestionCandidate, len(f.rooms))
	copy(out, f.rooms)
	return out, nil
}

func (f *fakeEllieIngestionStore) GetRoomCursor(_ context.Context, orgID, roomID string) (*store.EllieRoomCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cursor, ok := f.cursors[roomCursorKey(orgID, roomID)]
	if !ok {
		return nil, nil
	}
	copy := cursor
	return &copy, nil
}

func (f *fakeEllieIngestionStore) ListRoomMessagesSince(_ context.Context, orgID, roomID string, _ *time.Time, _ *string, _ int) ([]store.EllieIngestionMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.messages[roomCursorKey(orgID, roomID)]
	out := make([]store.EllieIngestionMessage, len(rows))
	copy(out, rows)
	return out, nil
}

func (f *fakeEllieIngestionStore) InsertExtractedMemory(_ context.Context, input store.CreateEllieExtractedMemoryInput) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, input)
	return true, nil
}

func (f *fakeEllieIngestionStore) UpsertRoomCursor(_ context.Context, input store.UpsertEllieRoomCursorInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors[roomCursorKey(input.OrgID, input.RoomID)] = store.EllieRoomCursor{
		OrgID:                input.OrgID,
		RoomID:               input.RoomID,
		LastMessageID:        input.LastMessageID,
		LastMessageCreatedAt: input.LastMessageCreatedAt,
	}
	return nil
}

func TestEllieIngestionWorkerExtractsMemoriesFromChatMessages(t *testing.T) {
	fakeStore := newFakeEllieIngestionStore()
	base := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	fakeStore.rooms = []store.EllieRoomIngestionCandidate{{OrgID: "org-1", RoomID: "room-1"}}
	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{
			ID:        "msg-1",
			OrgID:     "org-1",
			RoomID:    "room-1",
			Body:      "We decided to use Postgres for this project.",
			CreatedAt: base,
		},
	}

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		BatchSize:  50,
		Interval:   time.Second,
		MaxPerRoom: 50,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, fakeStore.created, 1)
	require.Equal(t, "technical_decision", fakeStore.created[0].Kind)
	require.Equal(t, "org-1", fakeStore.created[0].OrgID)
}

func TestEllieIngestionWorkerAdvancesCursorAfterSuccessfulWrite(t *testing.T) {
	fakeStore := newFakeEllieIngestionStore()
	first := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	second := first.Add(2 * time.Minute)
	fakeStore.rooms = []store.EllieRoomIngestionCandidate{{OrgID: "org-1", RoomID: "room-1"}}
	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{ID: "msg-1", OrgID: "org-1", RoomID: "room-1", Body: "Context note", CreatedAt: first},
		{ID: "msg-2", OrgID: "org-1", RoomID: "room-1", Body: "Preference: use explicit SQL migrations", CreatedAt: second},
	}

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		BatchSize:  50,
		Interval:   time.Second,
		MaxPerRoom: 50,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, processed)

	cursor, ok := fakeStore.cursors[roomCursorKey("org-1", "room-1")]
	require.True(t, ok)
	require.Equal(t, "msg-2", cursor.LastMessageID)
	require.Equal(t, second, cursor.LastMessageCreatedAt)
}

func TestEllieIngestionWorkerGroupsMessagesWithinTimeWindow(t *testing.T) {
	fakeStore := newFakeEllieIngestionStore()
	base := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	fakeStore.rooms = []store.EllieRoomIngestionCandidate{{OrgID: "org-1", RoomID: "room-1"}}
	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{ID: "msg-1", OrgID: "org-1", RoomID: "room-1", Body: "During planning we reviewed the tradeoffs.", CreatedAt: base},
		{ID: "msg-2", OrgID: "org-1", RoomID: "room-1", Body: "We decided to keep Postgres for consistency.", CreatedAt: base.Add(5 * time.Minute)},
		{ID: "msg-3", OrgID: "org-1", RoomID: "room-1", Body: "This decision applies to the current migration work.", CreatedAt: base.Add(10 * time.Minute)},
	}

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		BatchSize:  50,
		Interval:   time.Second,
		MaxPerRoom: 50,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, processed)
	require.Len(t, fakeStore.created, 1)
	require.Equal(t, "technical_decision", fakeStore.created[0].Kind)
	require.Contains(t, fakeStore.created[0].Content, "We decided to keep Postgres")
}

func TestEllieIngestionWorkerAvoidsFalsePositiveDecisionAndFactClassification(t *testing.T) {
	messageDecisionQuestion := store.EllieIngestionMessage{
		ID:        "msg-1",
		OrgID:     "org-1",
		RoomID:    "room-1",
		Body:      "Can we decide where to eat lunch?",
		CreatedAt: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
	}
	candidate, ok := deriveEllieMemoryCandidate(messageDecisionQuestion)
	require.True(t, ok)
	require.NotEqual(t, "technical_decision", candidate.Kind)

	messageFactQuestion := store.EllieIngestionMessage{
		ID:        "msg-2",
		OrgID:     "org-1",
		RoomID:    "room-1",
		Body:      "Where is the bathroom in this building?",
		CreatedAt: time.Date(2026, 2, 12, 12, 1, 0, 0, time.UTC),
	}
	candidate, ok = deriveEllieMemoryCandidate(messageFactQuestion)
	require.True(t, ok)
	require.NotEqual(t, "fact", candidate.Kind)
}
