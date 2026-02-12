package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeConversationSegmentationQueue struct {
	mu sync.Mutex

	pending   []store.PendingConversationSegmentationMessage
	assigned  map[string]bool
	segments  []store.CreateConversationSegmentInput
	nextIndex int
}

func newFakeConversationSegmentationQueue(pending []store.PendingConversationSegmentationMessage) *fakeConversationSegmentationQueue {
	return &fakeConversationSegmentationQueue{
		pending:  append([]store.PendingConversationSegmentationMessage(nil), pending...),
		assigned: make(map[string]bool),
		segments: make([]store.CreateConversationSegmentInput, 0),
	}
}

func (f *fakeConversationSegmentationQueue) ListPendingConversationMessages(_ context.Context, limit int) ([]store.PendingConversationSegmentationMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 {
		limit = len(f.pending)
	}
	out := make([]store.PendingConversationSegmentationMessage, 0, limit)
	for _, row := range f.pending {
		if f.assigned[row.ID] {
			continue
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeConversationSegmentationQueue) CreateConversationSegment(_ context.Context, input store.CreateConversationSegmentInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextIndex += 1
	for _, messageID := range input.MessageIDs {
		f.assigned[messageID] = true
	}
	f.segments = append(f.segments, input)
	return "conversation-" + time.Now().Format("150405") + "-" + string(rune('a'+f.nextIndex-1)), nil
}

func TestConversationSegmentationWorkerSplitsOnTimeGap(t *testing.T) {
	base := time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC)
	queue := newFakeConversationSegmentationQueue([]store.PendingConversationSegmentationMessage{
		{ID: "m1", OrgID: "org-1", RoomID: "room-1", Body: "first body", CreatedAt: base},
		{ID: "m2", OrgID: "org-1", RoomID: "room-1", Body: "second body", CreatedAt: base.Add(5 * time.Minute)},
		{ID: "m3", OrgID: "org-1", RoomID: "room-1", Body: "third body", CreatedAt: base.Add(50 * time.Minute)},
	})

	worker := NewConversationSegmentationWorker(queue, ConversationSegmentationWorkerConfig{
		BatchSize:    10,
		PollInterval: time.Second,
		GapThreshold: 20 * time.Minute,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, processed)
	require.Len(t, queue.segments, 2)
	require.Equal(t, []string{"m1", "m2"}, queue.segments[0].MessageIDs)
	require.Equal(t, []string{"m3"}, queue.segments[1].MessageIDs)
}

func TestConversationSegmentationWorkerIdempotent(t *testing.T) {
	base := time.Date(2026, 2, 12, 13, 0, 0, 0, time.UTC)
	queue := newFakeConversationSegmentationQueue([]store.PendingConversationSegmentationMessage{
		{ID: "m1", OrgID: "org-1", RoomID: "room-1", Body: "first body", CreatedAt: base},
		{ID: "m2", OrgID: "org-1", RoomID: "room-1", Body: "second body", CreatedAt: base.Add(2 * time.Minute)},
	})

	worker := NewConversationSegmentationWorker(queue, ConversationSegmentationWorkerConfig{
		BatchSize:    10,
		PollInterval: time.Second,
		GapThreshold: 20 * time.Minute,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, processed)
	require.Len(t, queue.segments, 1)

	processed, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Len(t, queue.segments, 1)
}

func TestConversationSegmentationWorkerStopsOnContextCancel(t *testing.T) {
	queue := newFakeConversationSegmentationQueue(nil)

	worker := NewConversationSegmentationWorker(queue, ConversationSegmentationWorkerConfig{
		BatchSize:    10,
		PollInterval: time.Hour,
		GapThreshold: 20 * time.Minute,
	})
	worker.Logf = nil

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("segmentation worker did not stop after context cancellation")
	}
}
