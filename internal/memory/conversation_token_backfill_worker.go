package memory

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	defaultConversationTokenBackfillBatchSize    = 200
	defaultConversationTokenBackfillPollInterval = 5 * time.Second
)

type ConversationTokenBackfillQueue interface {
	BackfillMissingTokenCounts(ctx context.Context, limit int) (int, error)
}

type ConversationTokenBackfillWorkerConfig struct {
	BatchSize    int
	PollInterval time.Duration
}

type ConversationTokenBackfillWorker struct {
	Queue        ConversationTokenBackfillQueue
	BatchSize    int
	PollInterval time.Duration
	Logf         func(format string, args ...any)
}

func NewConversationTokenBackfillWorker(
	queue ConversationTokenBackfillQueue,
	cfg ConversationTokenBackfillWorkerConfig,
) *ConversationTokenBackfillWorker {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultConversationTokenBackfillBatchSize
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultConversationTokenBackfillPollInterval
	}

	return &ConversationTokenBackfillWorker{
		Queue:        queue,
		BatchSize:    batchSize,
		PollInterval: pollInterval,
		Logf:         log.Printf,
	}
}

func (w *ConversationTokenBackfillWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}

		processed, err := w.RunOnce(ctx)
		if err != nil {
			if w.Logf != nil {
				w.Logf("conversation token backfill worker run failed: %v", err)
			}
		}
		if processed > 0 {
			continue
		}
		if err := sleepWithContext(ctx, w.PollInterval); err != nil {
			return
		}
	}
}

func (w *ConversationTokenBackfillWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("conversation token backfill worker is nil")
	}
	if w.Queue == nil {
		return 0, fmt.Errorf("conversation token backfill queue is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = defaultConversationTokenBackfillBatchSize
	}
	if w.PollInterval <= 0 {
		w.PollInterval = defaultConversationTokenBackfillPollInterval
	}

	processed, err := w.Queue.BackfillMissingTokenCounts(ctx, w.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("backfill conversation tokens: %w", err)
	}
	return processed, nil
}
