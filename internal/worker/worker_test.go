package worker

import (
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

func TestDeterministicQueryEmbedderUsesStableHashProjection(t *testing.T) {
	embedder := deterministicQueryEmbedder{}

	vectors, err := embedder.Embed(context.Background(), uuid.New(), "memory_retrieval", []string{"  hello world  "})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 1 {
		t.Fatalf("vector count = %d, want 1", len(vectors))
	}

	vector := vectors[0]
	if len(vector) != deterministicQueryEmbeddingDimensions {
		t.Fatalf("vector dimensions = %d, want %d", len(vector), deterministicQueryEmbeddingDimensions)
	}

	sum := sha256.Sum256([]byte("hello world"))
	if vector[0] != float32(sum[0])/255.0 {
		t.Fatalf("vector[0] = %f, want %f", vector[0], float32(sum[0])/255.0)
	}
	if vector[1] != 1 {
		t.Fatalf("vector[1] = %f, want 1", vector[1])
	}
	for i := 0; i < len(sum); i++ {
		want := float32(sum[i]) / 255.0
		if vector[i+2] != want {
			t.Fatalf("vector[%d] = %f, want %f", i+2, vector[i+2], want)
		}
	}
}

func TestDeterministicQueryEmbedderTrimsInput(t *testing.T) {
	embedder := deterministicQueryEmbedder{}

	vectors, err := embedder.Embed(context.Background(), uuid.New(), "memory_retrieval", []string{"hello", "  hello  "})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("vector count = %d, want 2", len(vectors))
	}

	if len(vectors[0]) != len(vectors[1]) {
		t.Fatalf("vector dimensions differ: %d != %d", len(vectors[0]), len(vectors[1]))
	}
	for i := range vectors[0] {
		if vectors[0][i] != vectors[1][i] {
			t.Fatalf("trimmed and untrimmed embeddings differ at index %d", i)
		}
	}
}

func TestWorkerTurnEngineOptionsIncludeBootstrapPromotionDependencies(t *testing.T) {
	pool := testdb.New(t)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("NewService task: %v", err)
	}
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("NewService chat: %v", err)
	}
	flowSessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  pool,
		Chats: chatService,
	})
	if err != nil {
		t.Fatalf("NewFlowSessionBridge: %v", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          pool,
		Events:        bus,
		TasksService:  taskService,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("NewService flow: %v", err)
	}

	opts := workerTurnEngineOptions(turn.Options{}, pool, taskService, flowService)
	if opts.TaskTransitions == nil {
		t.Fatal("TaskTransitions is nil, want bootstrap task promotion enabled")
	}
	if opts.FlowNodes == nil {
		t.Fatal("FlowNodes is nil, want bootstrap execution validation enabled")
	}
	if opts.FlowAdvancer == nil {
		t.Fatal("FlowAdvancer is nil, want bootstrap flow advancement enabled")
	}
	if opts.Assignments == nil {
		t.Fatal("Assignments is nil, want bootstrap staffing validation enabled")
	}
	if opts.Projects == nil {
		t.Fatal("Projects is nil, want bootstrap project-state updates enabled")
	}
	if opts.Environments == nil {
		t.Fatal("Environments is nil, want repo-binding validation enabled")
	}
	if opts.Organizations == nil {
		t.Fatal("Organizations is nil, want bootstrap org lookup enabled")
	}
}
