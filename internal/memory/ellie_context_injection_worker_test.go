package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeEllieContextInjectionQueue struct {
	mu sync.Mutex

	pending  []store.EllieContextInjectionPendingMessage
	memories []store.EllieContextInjectionMemoryCandidate

	updatedEmbeddingMessageIDs []string
	createdMessages            []store.CreateEllieContextInjectionMessageInput
	recordedMemoryIDs          []string
	embedCallCount             int
}

func (f *fakeEllieContextInjectionQueue) ListPendingMessagesSince(
	_ context.Context,
	_ *time.Time,
	_ *string,
	_ int,
) ([]store.EllieContextInjectionPendingMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.EllieContextInjectionPendingMessage, len(f.pending))
	copy(out, f.pending)
	return out, nil
}

func (f *fakeEllieContextInjectionQueue) UpdateMessageEmbedding(_ context.Context, messageID string, _ []float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updatedEmbeddingMessageIDs = append(f.updatedEmbeddingMessageIDs, messageID)
	return nil
}

func (f *fakeEllieContextInjectionQueue) SearchMemoryCandidatesByEmbedding(
	_ context.Context,
	_ string,
	_ []float64,
	_ int,
) ([]store.EllieContextInjectionMemoryCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.EllieContextInjectionMemoryCandidate, len(f.memories))
	copy(out, f.memories)
	return out, nil
}

func (f *fakeEllieContextInjectionQueue) WasInjectedSinceCompaction(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}

func (f *fakeEllieContextInjectionQueue) RecordInjection(_ context.Context, _, _, memoryID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedMemoryIDs = append(f.recordedMemoryIDs, memoryID)
	return nil
}

func (f *fakeEllieContextInjectionQueue) CreateInjectionMessage(
	_ context.Context,
	input store.CreateEllieContextInjectionMessageInput,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createdMessages = append(f.createdMessages, input)
	return "injection-message-1", nil
}

func (f *fakeEllieContextInjectionQueue) CountMessagesSinceLastContextInjection(_ context.Context, _, _ string) (int, error) {
	return 99, nil
}

type fakeEllieContextInjectionEmbedder struct {
	calls int
}

func testEllieContextEmbeddingVector(value float64) []float64 {
	vector := make([]float64, 384)
	for i := range vector {
		vector[i] = value
	}
	return vector
}

func (f *fakeEllieContextInjectionEmbedder) Dimension() int {
	return 384
}

func (f *fakeEllieContextInjectionEmbedder) Embed(_ context.Context, input []string) ([][]float64, error) {
	f.calls += 1
	out := make([][]float64, 0, len(input))
	for range input {
		out = append(out, testEllieContextEmbeddingVector(0.01))
	}
	return out, nil
}

func TestEllieContextInjectionWorkerFastTracksMessageEmbedding(t *testing.T) {
	queue := &fakeEllieContextInjectionQueue{
		pending: []store.EllieContextInjectionPendingMessage{
			{
				MessageID:    "msg-1",
				OrgID:        "11111111-1111-1111-1111-111111111111",
				RoomID:       "22222222-2222-2222-2222-222222222222",
				SenderID:     "33333333-3333-3333-3333-333333333333",
				SenderType:   "user",
				Body:         "Should we add a database now?",
				MessageType:  "message",
				HasEmbedding: false,
				CreatedAt:    time.Date(2026, 2, 12, 15, 0, 0, 0, time.UTC),
			},
		},
		memories: []store.EllieContextInjectionMemoryCandidate{
			{
				MemoryID:   "44444444-4444-4444-4444-444444444444",
				Title:      "Database policy",
				Content:    "Prefer Postgres with explicit migration files.",
				Similarity: 0.95,
				Importance: 5,
				Confidence: 0.9,
				OccurredAt: time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
			},
		},
	}
	embedder := &fakeEllieContextInjectionEmbedder{}
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
		Threshold: 0.5,
		MaxItems:  3,
	})

	worker := NewEllieContextInjectionWorker(queue, embedder, service, EllieContextInjectionWorkerConfig{
		BatchSize:         10,
		PollInterval:      time.Second,
		Threshold:         0.5,
		MaxMemoriesPerMsg: 3,
		CooldownMessages:  1,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, embedder.calls)
	require.Equal(t, []string{"msg-1"}, queue.updatedEmbeddingMessageIDs)
	require.Len(t, queue.createdMessages, 1)
	require.Equal(t, "context_injection", queue.createdMessages[0].MessageType)
	require.Len(t, queue.recordedMemoryIDs, 1)
	require.Equal(t, "44444444-4444-4444-4444-444444444444", queue.recordedMemoryIDs[0])
}

func TestEllieContextInjectionWorkerSkipsSystemAndContextInjectionTypes(t *testing.T) {
	queue := &fakeEllieContextInjectionQueue{
		pending: []store.EllieContextInjectionPendingMessage{
			{
				MessageID:   "msg-sys",
				OrgID:       "11111111-1111-1111-1111-111111111111",
				RoomID:      "22222222-2222-2222-2222-222222222222",
				SenderID:    "33333333-3333-3333-3333-333333333333",
				SenderType:  "system",
				Body:        "system event",
				MessageType: "system",
				CreatedAt:   time.Date(2026, 2, 12, 15, 1, 0, 0, time.UTC),
			},
			{
				MessageID:   "msg-ci",
				OrgID:       "11111111-1111-1111-1111-111111111111",
				RoomID:      "22222222-2222-2222-2222-222222222222",
				SenderID:    "33333333-3333-3333-3333-333333333333",
				SenderType:  "agent",
				Body:        "prior injected context",
				MessageType: "context_injection",
				CreatedAt:   time.Date(2026, 2, 12, 15, 2, 0, 0, time.UTC),
			},
		},
	}
	embedder := &fakeEllieContextInjectionEmbedder{}
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{})

	worker := NewEllieContextInjectionWorker(queue, embedder, service, EllieContextInjectionWorkerConfig{
		BatchSize:         10,
		PollInterval:      time.Second,
		Threshold:         0.5,
		MaxMemoriesPerMsg: 3,
		CooldownMessages:  1,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, processed)
	require.Equal(t, 0, embedder.calls)
	require.Empty(t, queue.updatedEmbeddingMessageIDs)
	require.Empty(t, queue.createdMessages)
	require.Empty(t, queue.recordedMemoryIDs)
}

func TestEllieContextInjectionWorkerIncludesSupersessionNoteWhenCandidateSupersedesPriorMemory(t *testing.T) {
	supersededMemoryID := "55555555-5555-5555-5555-555555555555"
	queue := &fakeEllieContextInjectionQueue{
		pending: []store.EllieContextInjectionPendingMessage{
			{
				MessageID:    "msg-supersession",
				OrgID:        "11111111-1111-1111-1111-111111111111",
				RoomID:       "22222222-2222-2222-2222-222222222222",
				SenderID:     "33333333-3333-3333-3333-333333333333",
				SenderType:   "user",
				Body:         "What is the current database preference?",
				MessageType:  "message",
				HasEmbedding: false,
				CreatedAt:    time.Date(2026, 2, 12, 15, 30, 0, 0, time.UTC),
			},
		},
		memories: []store.EllieContextInjectionMemoryCandidate{
			{
				MemoryID:     "44444444-4444-4444-4444-444444444444",
				Title:        "Database policy",
				Content:      "Use MySQL with explicit migrations.",
				Similarity:   0.92,
				Importance:   5,
				Confidence:   0.9,
				OccurredAt:   time.Date(2026, 2, 12, 12, 0, 0, 0, time.UTC),
				SupersededBy: &supersededMemoryID,
			},
		},
	}
	embedder := &fakeEllieContextInjectionEmbedder{}
	service := NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
		Threshold: 0.5,
		MaxItems:  3,
	})

	worker := NewEllieContextInjectionWorker(queue, embedder, service, EllieContextInjectionWorkerConfig{
		BatchSize:         10,
		PollInterval:      time.Second,
		Threshold:         0.5,
		MaxMemoriesPerMsg: 3,
		CooldownMessages:  1,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Len(t, queue.createdMessages, 1)
	require.Contains(t, queue.createdMessages[0].Body, "Updated context: previous decision")
	require.Contains(t, queue.createdMessages[0].Body, supersededMemoryID)
}
