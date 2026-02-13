package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieIngestionStore struct {
	mu sync.Mutex

	rooms     []store.EllieRoomIngestionCandidate
	messages  map[string][]store.EllieIngestionMessage
	cursors   map[string]store.EllieRoomCursor
	created   []store.CreateEllieExtractedMemoryInput
	listCalls []fakeEllieRoomMessagesCall

	listRoomsCalls int
	upsertErr      error
}

type fakeEllieRoomMessagesCall struct {
	OrgID          string
	RoomID         string
	AfterCreatedAt *time.Time
	AfterMessageID *string
	Limit          int
}

func newFakeEllieIngestionStore() *fakeEllieIngestionStore {
	return &fakeEllieIngestionStore{
		rooms:     make([]store.EllieRoomIngestionCandidate, 0),
		messages:  make(map[string][]store.EllieIngestionMessage),
		cursors:   make(map[string]store.EllieRoomCursor),
		created:   make([]store.CreateEllieExtractedMemoryInput, 0),
		listCalls: make([]fakeEllieRoomMessagesCall, 0),
	}
}

func roomCursorKey(orgID, roomID string) string {
	return orgID + ":" + roomID
}

func (f *fakeEllieIngestionStore) ListRoomsForIngestion(_ context.Context, _ int) ([]store.EllieRoomIngestionCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listRoomsCalls += 1
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

func (f *fakeEllieIngestionStore) ListRoomMessagesSince(_ context.Context, orgID, roomID string, afterCreatedAt *time.Time, afterMessageID *string, limit int) ([]store.EllieIngestionMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := fakeEllieRoomMessagesCall{
		OrgID:  orgID,
		RoomID: roomID,
		Limit:  limit,
	}
	if afterCreatedAt != nil {
		copyCreatedAt := afterCreatedAt.UTC()
		call.AfterCreatedAt = &copyCreatedAt
	}
	if afterMessageID != nil {
		copyMessageID := strings.TrimSpace(*afterMessageID)
		call.AfterMessageID = &copyMessageID
	}
	f.listCalls = append(f.listCalls, call)

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
	if f.upsertErr != nil {
		return f.upsertErr
	}
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
	cases := []struct {
		name          string
		body          string
		notExpected   string
		messageSuffix string
	}{
		{
			name:          "decision question does not become technical decision",
			body:          "Can we decide where to eat lunch?",
			notExpected:   "technical_decision",
			messageSuffix: "1",
		},
		{
			name:          "fact question does not become fact",
			body:          "Where is the bathroom in this building?",
			notExpected:   "fact",
			messageSuffix: "2",
		},
		{
			name:          "decided to non technical statement does not become technical decision",
			body:          "Someone decided to order pizza for tonight.",
			notExpected:   "technical_decision",
			messageSuffix: "3",
		},
		{
			name:          "latest substring does not trigger test keyword anti pattern",
			body:          "Don't forget the latest lunch menu before we leave.",
			notExpected:   "anti_pattern",
			messageSuffix: "4",
		},
		{
			name:          "contest substring does not trigger test keyword anti pattern",
			body:          "Do not enter the contest booth during lunch.",
			notExpected:   "anti_pattern",
			messageSuffix: "5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate, ok := deriveEllieMemoryCandidate(store.EllieIngestionMessage{
				ID:        "msg-" + tc.messageSuffix,
				OrgID:     "org-1",
				RoomID:    "room-1",
				Body:      tc.body,
				CreatedAt: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
			})
			require.True(t, ok)
			require.NotEqual(t, tc.notExpected, candidate.Kind)
		})
	}
}

func TestEllieIngestionWorkerBackfillModeStartsFromEpoch(t *testing.T) {
	fakeStore := newFakeEllieIngestionStore()
	first := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	fakeStore.rooms = []store.EllieRoomIngestionCandidate{{OrgID: "org-1", RoomID: "room-1"}}
	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{
			ID:        "msg-1",
			OrgID:     "org-1",
			RoomID:    "room-1",
			Body:      "Context note",
			CreatedAt: first,
		},
	}

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		BatchSize:          50,
		Interval:           time.Second,
		MaxPerRoom:         20,
		BackfillMaxPerRoom: 80,
		Mode:               EllieIngestionModeBackfill,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, fakeStore.listCalls, 1)
	require.Equal(t, 80, fakeStore.listCalls[0].Limit)
	require.NotNil(t, fakeStore.listCalls[0].AfterCreatedAt)
	require.Equal(t, time.Unix(0, 0).UTC(), fakeStore.listCalls[0].AfterCreatedAt.UTC())
	require.Nil(t, fakeStore.listCalls[0].AfterMessageID)
}

func TestEllieIngestionWorkerBackfillModeResumesNormalCursoring(t *testing.T) {
	fakeStore := newFakeEllieIngestionStore()
	first := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	second := first.Add(1 * time.Minute)
	fakeStore.rooms = []store.EllieRoomIngestionCandidate{{OrgID: "org-1", RoomID: "room-1"}}
	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{
			ID:        "msg-1",
			OrgID:     "org-1",
			RoomID:    "room-1",
			Body:      "Context note",
			CreatedAt: first,
		},
	}

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		BatchSize:          50,
		Interval:           time.Second,
		MaxPerRoom:         30,
		BackfillMaxPerRoom: 90,
		Mode:               EllieIngestionModeBackfill,
	})
	worker.Logf = nil

	_, err := worker.RunOnce(context.Background())
	require.NoError(t, err)

	fakeStore.messages[roomCursorKey("org-1", "room-1")] = []store.EllieIngestionMessage{
		{
			ID:        "msg-2",
			OrgID:     "org-1",
			RoomID:    "room-1",
			Body:      "Preference: keep SQL explicit",
			CreatedAt: second,
		},
	}
	worker.SetMode(EllieIngestionModeNormal)
	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	require.Len(t, fakeStore.listCalls, 2)
	require.Equal(t, 90, fakeStore.listCalls[0].Limit)
	require.Equal(t, 30, fakeStore.listCalls[1].Limit)
	require.NotNil(t, fakeStore.listCalls[1].AfterCreatedAt)
	require.Equal(t, first, fakeStore.listCalls[1].AfterCreatedAt.UTC())
	require.NotNil(t, fakeStore.listCalls[1].AfterMessageID)
	require.Equal(t, "msg-1", strings.TrimSpace(*fakeStore.listCalls[1].AfterMessageID))
}

func TestEllieIngestionWorkerStartSleepsAfterProcessedError(t *testing.T) {
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
	fakeStore.upsertErr = errors.New("cursor update failed")

	worker := NewEllieIngestionWorker(fakeStore, EllieIngestionWorkerConfig{
		Interval:   20 * time.Millisecond,
		BatchSize:  10,
		MaxPerRoom: 10,
	})
	worker.Logf = nil

	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()
	worker.Start(ctx)

	fakeStore.mu.Lock()
	defer fakeStore.mu.Unlock()
	require.LessOrEqual(t, fakeStore.listRoomsCalls, 5)
}
