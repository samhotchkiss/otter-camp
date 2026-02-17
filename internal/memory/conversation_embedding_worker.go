package memory

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultConversationEmbeddingBatchSize    = 50
	defaultConversationEmbeddingPollInterval = 5 * time.Second
	maxConversationEmbeddingBackoff          = 5 * time.Minute
)

type ConversationEmbeddingQueue interface {
	ListPendingChatMessages(ctx context.Context, limit int) ([]store.PendingChatMessageEmbedding, error)
	UpdateChatMessageEmbedding(ctx context.Context, messageID string, embedding []float64) error
	ListPendingMemories(ctx context.Context, limit int) ([]store.PendingMemoryEmbedding, error)
	UpdateMemoryEmbedding(ctx context.Context, memoryID string, embedding []float64) error
}

type ConversationEmbeddingWorkerConfig struct {
	BatchSize    int
	PollInterval time.Duration
}

type ConversationEmbeddingWorker struct {
	Queue        ConversationEmbeddingQueue
	Embedder     Embedder
	BatchSize    int
	PollInterval time.Duration
	Logf         func(format string, args ...any)
	sleep        func(ctx context.Context, duration time.Duration) error
}

func NewConversationEmbeddingWorker(
	queue ConversationEmbeddingQueue,
	embedder Embedder,
	cfg ConversationEmbeddingWorkerConfig,
) *ConversationEmbeddingWorker {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultConversationEmbeddingBatchSize
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultConversationEmbeddingPollInterval
	}

	return &ConversationEmbeddingWorker{
		Queue:        queue,
		Embedder:     embedder,
		BatchSize:    batchSize,
		PollInterval: pollInterval,
		Logf:         log.Printf,
		sleep:        sleepWithContext,
	}
}

func (w *ConversationEmbeddingWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	consecutiveFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		processed, err := w.RunOnce(ctx)
		if err != nil {
			consecutiveFailures += 1
			if w.Logf != nil {
				w.Logf("conversation embedding worker run failed: %v", err)
			}
		} else {
			consecutiveFailures = 0
		}
		if err == nil && processed > 0 {
			continue
		}

		delay := w.PollInterval
		if err != nil {
			delay = conversationEmbeddingFailureBackoff(w.PollInterval, consecutiveFailures)
		}

		sleeper := w.sleep
		if sleeper == nil {
			sleeper = sleepWithContext
		}
		if err := sleeper(ctx, delay); err != nil {
			return
		}
	}
}

func (w *ConversationEmbeddingWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("conversation embedding worker is nil")
	}
	if w.Queue == nil {
		return 0, fmt.Errorf("conversation embedding queue is required")
	}
	if w.Embedder == nil {
		return 0, fmt.Errorf("conversation embedding embedder is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = defaultConversationEmbeddingBatchSize
	}
	if w.PollInterval <= 0 {
		w.PollInterval = defaultConversationEmbeddingPollInterval
	}

	processed := 0

	chatMessages, err := w.Queue.ListPendingChatMessages(ctx, w.BatchSize)
	if err != nil {
		return processed, fmt.Errorf("list pending chat message embeddings: %w", err)
	}
	if len(chatMessages) > 0 {
		inputs := make([]string, 0, len(chatMessages))
		for _, row := range chatMessages {
			inputs = append(inputs, row.Body)
		}
		vectors, err := w.Embedder.Embed(ctx, inputs)
		if err != nil {
			return processed, fmt.Errorf("embed chat messages: %w", err)
		}
		if len(vectors) != len(chatMessages) {
			return processed, fmt.Errorf("embed chat messages returned %d vectors for %d rows", len(vectors), len(chatMessages))
		}
		for i, row := range chatMessages {
			if err := w.Queue.UpdateChatMessageEmbedding(ctx, row.ID, vectors[i]); err != nil {
				return processed, fmt.Errorf("update chat message embedding %s: %w", row.ID, err)
			}
			processed += 1
		}
	}

	memories, err := w.Queue.ListPendingMemories(ctx, w.BatchSize)
	if err != nil {
		return processed, fmt.Errorf("list pending memory embeddings: %w", err)
	}
	if len(memories) > 0 {
		inputs := make([]string, 0, len(memories))
		for _, row := range memories {
			inputs = append(inputs, row.Content)
		}
		vectors, err := w.Embedder.Embed(ctx, inputs)
		if err != nil {
			return processed, fmt.Errorf("embed memories: %w", err)
		}
		if len(vectors) != len(memories) {
			return processed, fmt.Errorf("embed memories returned %d vectors for %d rows", len(vectors), len(memories))
		}
		for i, row := range memories {
			if err := w.Queue.UpdateMemoryEmbedding(ctx, row.ID, vectors[i]); err != nil {
				return processed, fmt.Errorf("update memory embedding %s: %w", row.ID, err)
			}
			processed += 1
		}
	}

	return processed, nil
}

func conversationEmbeddingFailureBackoff(base time.Duration, consecutiveFailures int) time.Duration {
	if base <= 0 {
		base = defaultConversationEmbeddingPollInterval
	}
	if base > maxConversationEmbeddingBackoff {
		base = maxConversationEmbeddingBackoff
	}
	if consecutiveFailures <= 1 {
		return base
	}

	delay := base
	for i := 1; i < consecutiveFailures; i += 1 {
		if delay >= maxConversationEmbeddingBackoff {
			return maxConversationEmbeddingBackoff
		}
		if delay > maxConversationEmbeddingBackoff/2 {
			return maxConversationEmbeddingBackoff
		}
		delay *= 2
	}
	if delay > maxConversationEmbeddingBackoff {
		return maxConversationEmbeddingBackoff
	}
	return delay
}
