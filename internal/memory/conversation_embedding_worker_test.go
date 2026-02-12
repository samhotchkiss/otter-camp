package memory

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeConversationEmbeddingQueue struct {
	mu sync.Mutex

	pendingChat     []store.PendingChatMessageEmbedding
	pendingMemories []store.PendingMemoryEmbedding

	updatedChatEmbeddings   map[string][]float64
	updatedMemoryEmbeddings map[string][]float64
}

func newFakeConversationEmbeddingQueue(
	chat []store.PendingChatMessageEmbedding,
	memories []store.PendingMemoryEmbedding,
) *fakeConversationEmbeddingQueue {
	return &fakeConversationEmbeddingQueue{
		pendingChat:             append([]store.PendingChatMessageEmbedding(nil), chat...),
		pendingMemories:         append([]store.PendingMemoryEmbedding(nil), memories...),
		updatedChatEmbeddings:   make(map[string][]float64),
		updatedMemoryEmbeddings: make(map[string][]float64),
	}
}

func (f *fakeConversationEmbeddingQueue) ListPendingChatMessages(_ context.Context, limit int) ([]store.PendingChatMessageEmbedding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > len(f.pendingChat) {
		limit = len(f.pendingChat)
	}
	out := make([]store.PendingChatMessageEmbedding, 0, limit)
	for _, row := range f.pendingChat[:limit] {
		if _, done := f.updatedChatEmbeddings[row.ID]; done {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeConversationEmbeddingQueue) UpdateChatMessageEmbedding(_ context.Context, messageID string, embedding []float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updatedChatEmbeddings[messageID] = append([]float64(nil), embedding...)
	return nil
}

func (f *fakeConversationEmbeddingQueue) ListPendingMemories(_ context.Context, limit int) ([]store.PendingMemoryEmbedding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > len(f.pendingMemories) {
		limit = len(f.pendingMemories)
	}
	out := make([]store.PendingMemoryEmbedding, 0, limit)
	for _, row := range f.pendingMemories[:limit] {
		if _, done := f.updatedMemoryEmbeddings[row.ID]; done {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeConversationEmbeddingQueue) UpdateMemoryEmbedding(_ context.Context, memoryID string, embedding []float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updatedMemoryEmbeddings[memoryID] = append([]float64(nil), embedding...)
	return nil
}

type fakeConversationEmbedder struct {
	mu sync.Mutex

	calls     int
	failUntil int
	vector    []float64
}

func (f *fakeConversationEmbedder) Dimension() int {
	return len(f.vector)
}

func (f *fakeConversationEmbedder) Embed(_ context.Context, inputs []string) ([][]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls += 1
	if f.calls <= f.failUntil {
		return nil, errors.New("forced embed failure")
	}
	out := make([][]float64, 0, len(inputs))
	for range inputs {
		out = append(out, append([]float64(nil), f.vector...))
	}
	return out, nil
}

type mismatchConversationEmbedder struct{}

func (m *mismatchConversationEmbedder) Dimension() int {
	return 1
}

func (m *mismatchConversationEmbedder) Embed(_ context.Context, _ []string) ([][]float64, error) {
	return [][]float64{}, nil
}

func TestConversationEmbeddingWorkerProcessesPendingRows(t *testing.T) {
	queue := newFakeConversationEmbeddingQueue(
		[]store.PendingChatMessageEmbedding{{ID: "chat-1", Body: "chat body"}},
		[]store.PendingMemoryEmbedding{{ID: "memory-1", Content: "memory content"}},
	)
	embedder := &fakeConversationEmbedder{vector: []float64{0.1, 0.2, 0.3}}

	worker := NewConversationEmbeddingWorker(queue, embedder, ConversationEmbeddingWorkerConfig{
		BatchSize:    10,
		PollInterval: 1,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, processed)
	require.Contains(t, queue.updatedChatEmbeddings, "chat-1")
	require.Contains(t, queue.updatedMemoryEmbeddings, "memory-1")
	require.Equal(t, []float64{0.1, 0.2, 0.3}, queue.updatedChatEmbeddings["chat-1"])
	require.Equal(t, []float64{0.1, 0.2, 0.3}, queue.updatedMemoryEmbeddings["memory-1"])
}

func TestConversationEmbeddingWorkerRetriesAndContinues(t *testing.T) {
	queue := newFakeConversationEmbeddingQueue(
		[]store.PendingChatMessageEmbedding{{ID: "chat-1", Body: "chat body"}},
		nil,
	)
	embedder := &fakeConversationEmbedder{vector: []float64{0.1, 0.2}, failUntil: 1}

	worker := NewConversationEmbeddingWorker(queue, embedder, ConversationEmbeddingWorkerConfig{
		BatchSize:    10,
		PollInterval: 1,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Equal(t, 0, processed)
	require.Contains(t, err.Error(), "embed chat messages")
	require.Empty(t, queue.updatedChatEmbeddings)

	processed, err = worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.Contains(t, queue.updatedChatEmbeddings, "chat-1")
	require.Equal(t, 2, embedder.calls)
}

func TestConversationEmbeddingWorkerRequiresDependencies(t *testing.T) {
	worker := &ConversationEmbeddingWorker{}
	_, err := worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Equal(t, "conversation embedding queue is required", err.Error())

	worker.Queue = newFakeConversationEmbeddingQueue(nil, nil)
	_, err = worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Equal(t, "conversation embedding embedder is required", err.Error())
}

func TestConversationEmbeddingWorkerDetectsVectorCountMismatch(t *testing.T) {
	queue := newFakeConversationEmbeddingQueue(
		[]store.PendingChatMessageEmbedding{{ID: "chat-1", Body: "chat body"}},
		nil,
	)
	embedder := &mismatchConversationEmbedder{}

	worker := NewConversationEmbeddingWorker(queue, embedder, ConversationEmbeddingWorkerConfig{BatchSize: 10, PollInterval: 1})
	worker.Logf = nil

	_, err := worker.RunOnce(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "returned 0 vectors for 1 rows")
}
