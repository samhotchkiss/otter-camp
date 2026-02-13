package memory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
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
const embeddingWorkerTestDBURLKey = "OTTER_TEST_DATABASE_URL"

func setupEmbeddingWorkerTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv(embeddingWorkerTestDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to run embedding worker integration tests", embeddingWorkerTestDBURLKey)
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	migrator, err := migrate.New("file://"+migrationsDir, connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = migrator.Close()
		_ = db.Close()
	})

	err = migrator.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
	err = migrator.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	return db
}

func TestConversationEmbeddingWorkerProcessesMultipleOrgsFairly(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)
	queue := store.NewConversationEmbeddingStore(db)

	var orgA string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier) VALUES ('Embedding Fairness A', 'embedding-fairness-a', 'free') RETURNING id`,
	).Scan(&orgA)
	require.NoError(t, err)
	var orgB string
	err = db.QueryRow(
		`INSERT INTO organizations (name, slug, tier) VALUES ('Embedding Fairness B', 'embedding-fairness-b', 'free') RETURNING id`,
	).Scan(&orgB)
	require.NoError(t, err)

	var projectA string
	err = db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Embedding Fairness Project A', 'active') RETURNING id`,
		orgA,
	).Scan(&projectA)
	require.NoError(t, err)
	var projectB string
	err = db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, 'Embedding Fairness Project B', 'active') RETURNING id`,
		orgB,
	).Scan(&projectB)
	require.NoError(t, err)

	var agentA string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, 'embed-fairness-a', 'Embed Fairness A', 'active') RETURNING id`,
		orgA,
	).Scan(&agentA)
	require.NoError(t, err)
	var agentB string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, 'embed-fairness-b', 'Embed Fairness B', 'active') RETURNING id`,
		orgB,
	).Scan(&agentB)
	require.NoError(t, err)

	var roomA string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Embedding Fairness Room A', 'project', $2)
		 RETURNING id`,
		orgA,
		projectA,
	).Scan(&roomA)
	require.NoError(t, err)
	var roomB string
	err = db.QueryRow(
		`INSERT INTO rooms (org_id, name, type, context_id)
		 VALUES ($1, 'Embedding Fairness Room B', 'project', $2)
		 RETURNING id`,
		orgB,
		projectB,
	).Scan(&roomB)
	require.NoError(t, err)

	base := time.Date(2026, 2, 12, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i += 1 {
		_, err = db.Exec(
			`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
			 VALUES ($1, $2, $3, 'agent', 'org-a embedding pending', 'message', $4, '[]'::jsonb)`,
			orgA,
			roomA,
			agentA,
			base.Add(time.Duration(i)*time.Minute),
		)
		require.NoError(t, err)
	}
	_, err = db.Exec(
		`INSERT INTO chat_messages (org_id, room_id, sender_id, sender_type, body, type, created_at, attachments)
		 VALUES ($1, $2, $3, 'agent', 'org-b embedding pending', 'message', $4, '[]'::jsonb)`,
		orgB,
		roomB,
		agentB,
		base.Add(25*time.Minute),
	)
	require.NoError(t, err)

	vector := make([]float64, 384)
	for i := range vector {
		vector[i] = 0.01
	}
	worker := NewConversationEmbeddingWorker(queue, &fakeConversationEmbedder{vector: vector}, ConversationEmbeddingWorkerConfig{
		BatchSize:    4,
		PollInterval: time.Second,
	})
	worker.Logf = nil

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 4, processed)

	var orgAEmbedded int
	err = db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE org_id = $1 AND embedding IS NOT NULL`, orgA).Scan(&orgAEmbedded)
	require.NoError(t, err)
	var orgBEmbedded int
	err = db.QueryRow(`SELECT COUNT(*) FROM chat_messages WHERE org_id = $1 AND embedding IS NOT NULL`, orgB).Scan(&orgBEmbedded)
	require.NoError(t, err)

	require.GreaterOrEqual(t, orgAEmbedded, 1)
	require.Equal(t, 1, orgBEmbedded)
}
func TestConversationEmbeddingWorkerStopsOnContextCancel(t *testing.T) {
	queue := newFakeConversationEmbeddingQueue(nil, nil)
	embedder := &fakeConversationEmbedder{vector: []float64{0.1, 0.2}}

	worker := NewConversationEmbeddingWorker(queue, embedder, ConversationEmbeddingWorkerConfig{
		BatchSize:    10,
		PollInterval: time.Hour,
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
		t.Fatal("embedding worker did not stop after context cancellation")
	}
}
