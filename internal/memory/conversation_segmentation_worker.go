package memory

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultConversationSegmentationBatchSize    = 200
	defaultConversationSegmentationPollInterval = 5 * time.Second
	defaultConversationSegmentationGapThreshold = 30 * time.Minute
)

type ConversationSegmentationQueue interface {
	ListPendingConversationMessages(ctx context.Context, limit int) ([]store.PendingConversationSegmentationMessage, error)
	CreateConversationSegment(ctx context.Context, input store.CreateConversationSegmentInput) (string, error)
}

type ConversationSegmentationWorkerConfig struct {
	BatchSize    int
	PollInterval time.Duration
	GapThreshold time.Duration
}

type ConversationSegmentationWorker struct {
	Queue        ConversationSegmentationQueue
	BatchSize    int
	PollInterval time.Duration
	GapThreshold time.Duration
	Logf         func(format string, args ...any)
}

func NewConversationSegmentationWorker(
	queue ConversationSegmentationQueue,
	cfg ConversationSegmentationWorkerConfig,
) *ConversationSegmentationWorker {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultConversationSegmentationBatchSize
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultConversationSegmentationPollInterval
	}
	gapThreshold := cfg.GapThreshold
	if gapThreshold <= 0 {
		gapThreshold = defaultConversationSegmentationGapThreshold
	}

	return &ConversationSegmentationWorker{
		Queue:        queue,
		BatchSize:    batchSize,
		PollInterval: pollInterval,
		GapThreshold: gapThreshold,
		Logf:         log.Printf,
	}
}

func (w *ConversationSegmentationWorker) Start(ctx context.Context) {
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
				w.Logf("conversation segmentation worker run failed: %v", err)
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

func (w *ConversationSegmentationWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("conversation segmentation worker is nil")
	}
	if w.Queue == nil {
		return 0, fmt.Errorf("conversation segmentation queue is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = defaultConversationSegmentationBatchSize
	}
	if w.GapThreshold <= 0 {
		w.GapThreshold = defaultConversationSegmentationGapThreshold
	}

	pending, err := w.Queue.ListPendingConversationMessages(ctx, w.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("list pending conversation messages: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].RoomID != pending[j].RoomID {
			return pending[i].RoomID < pending[j].RoomID
		}
		if !pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].CreatedAt.Before(pending[j].CreatedAt)
		}
		return pending[i].ID < pending[j].ID
	})

	processed := 0
	segment := make([]store.PendingConversationSegmentationMessage, 0, len(pending))

	flush := func(rows []store.PendingConversationSegmentationMessage) error {
		if len(rows) == 0 {
			return nil
		}
		messageIDs := make([]string, 0, len(rows))
		for _, row := range rows {
			messageIDs = append(messageIDs, row.ID)
		}
		_, err := w.Queue.CreateConversationSegment(ctx, store.CreateConversationSegmentInput{
			OrgID:      rows[0].OrgID,
			RoomID:     rows[0].RoomID,
			Topic:      deriveConversationTopic(rows[0].Body),
			StartedAt:  rows[0].CreatedAt,
			EndedAt:    rows[len(rows)-1].CreatedAt,
			MessageIDs: messageIDs,
		})
		if err != nil {
			return err
		}
		processed += len(rows)
		return nil
	}

	for _, row := range pending {
		if len(segment) == 0 {
			segment = append(segment, row)
			continue
		}
		previous := segment[len(segment)-1]
		isSameRoom := row.RoomID == previous.RoomID && row.OrgID == previous.OrgID
		timeGap := row.CreatedAt.Sub(previous.CreatedAt)
		if !isSameRoom || timeGap > w.GapThreshold {
			if err := flush(segment); err != nil {
				return processed, fmt.Errorf("flush conversation segment: %w", err)
			}
			segment = segment[:0]
		}
		segment = append(segment, row)
	}

	if err := flush(segment); err != nil {
		return processed, fmt.Errorf("flush final conversation segment: %w", err)
	}

	return processed, nil
}

func deriveConversationTopic(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "Conversation"
	}
	runes := []rune(trimmed)
	if len(runes) <= 80 {
		return trimmed
	}
	return string(runes[:80])
}
