package memory

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieContextInjectionPollInterval     = 3 * time.Second
	defaultEllieContextInjectionBatchSize        = 50
	defaultEllieContextInjectionMaxMemories      = 5
	defaultEllieContextInjectionCooldownMessages = 4
)

type EllieContextInjectionQueue interface {
	ListPendingMessagesSince(ctx context.Context, afterCreatedAt *time.Time, afterMessageID *string, limit int) ([]store.EllieContextInjectionPendingMessage, error)
	UpdateMessageEmbedding(ctx context.Context, messageID string, embedding []float64) error
	SearchMemoryCandidatesByEmbedding(ctx context.Context, orgID string, embedding []float64, limit int) ([]store.EllieContextInjectionMemoryCandidate, error)
	WasInjectedSinceCompaction(ctx context.Context, orgID, roomID, memoryID string) (bool, error)
	RecordInjection(ctx context.Context, orgID, roomID, memoryID string, injectedAt time.Time) error
	CreateInjectionMessage(ctx context.Context, input store.CreateEllieContextInjectionMessageInput) (string, error)
	CountMessagesSinceLastContextInjection(ctx context.Context, orgID, roomID string) (int, error)
}

type EllieContextInjectionWorkerConfig struct {
	BatchSize         int
	PollInterval      time.Duration
	Threshold         float64
	MaxMemoriesPerMsg int
	CooldownMessages  int
}

type EllieContextInjectionWorker struct {
	Queue    EllieContextInjectionQueue
	Embedder Embedder
	Service  *EllieProactiveInjectionService

	BatchSize         int
	PollInterval      time.Duration
	MaxMemoriesPerMsg int
	CooldownMessages  int

	lastCreatedAt *time.Time
	lastMessageID *string
	Logf          func(format string, args ...any)
}

func NewEllieContextInjectionWorker(
	queue EllieContextInjectionQueue,
	embedder Embedder,
	service *EllieProactiveInjectionService,
	cfg EllieContextInjectionWorkerConfig,
) *EllieContextInjectionWorker {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultEllieContextInjectionBatchSize
	}
	pollInterval := cfg.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultEllieContextInjectionPollInterval
	}
	maxMemories := cfg.MaxMemoriesPerMsg
	if maxMemories <= 0 {
		maxMemories = defaultEllieContextInjectionMaxMemories
	}
	cooldownMessages := cfg.CooldownMessages
	if cooldownMessages < 0 {
		cooldownMessages = 0
	}
	if cooldownMessages == 0 {
		cooldownMessages = defaultEllieContextInjectionCooldownMessages
	}
	if service == nil {
		service = NewEllieProactiveInjectionService(EllieProactiveInjectionConfig{
			Threshold: cfg.Threshold,
			MaxItems:  maxMemories,
		})
	}

	return &EllieContextInjectionWorker{
		Queue:             queue,
		Embedder:          embedder,
		Service:           service,
		BatchSize:         batchSize,
		PollInterval:      pollInterval,
		MaxMemoriesPerMsg: maxMemories,
		CooldownMessages:  cooldownMessages,
		Logf:              log.Printf,
	}
}

func (w *EllieContextInjectionWorker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		processed, err := w.RunOnce(ctx)
		if err != nil && w.Logf != nil {
			w.Logf("ellie context injection worker run failed: %v", err)
		}
		if processed > 0 {
			continue
		}
		if err := sleepWithContext(ctx, w.PollInterval); err != nil {
			return
		}
	}
}

func (w *EllieContextInjectionWorker) RunOnce(ctx context.Context) (int, error) {
	if w == nil {
		return 0, fmt.Errorf("ellie context injection worker is nil")
	}
	if w.Queue == nil {
		return 0, fmt.Errorf("ellie context injection queue is required")
	}
	if w.Embedder == nil {
		return 0, fmt.Errorf("ellie context injection embedder is required")
	}
	if w.Service == nil {
		return 0, fmt.Errorf("ellie context injection service is required")
	}
	if w.BatchSize <= 0 {
		w.BatchSize = defaultEllieContextInjectionBatchSize
	}
	if w.MaxMemoriesPerMsg <= 0 {
		w.MaxMemoriesPerMsg = defaultEllieContextInjectionMaxMemories
	}
	if w.CooldownMessages < 0 {
		w.CooldownMessages = 0
	}

	pending, err := w.Queue.ListPendingMessagesSince(ctx, w.lastCreatedAt, w.lastMessageID, w.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("list pending context injection messages: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	processed := 0
	for _, message := range pending {
		createdAt := message.CreatedAt.UTC()
		messageID := strings.TrimSpace(message.MessageID)
		w.lastCreatedAt = &createdAt
		w.lastMessageID = &messageID

		if message.MessageType == "system" || message.MessageType == "context_injection" {
			continue
		}

		if w.CooldownMessages > 0 {
			messagesSinceLastInjection, err := w.Queue.CountMessagesSinceLastContextInjection(ctx, message.OrgID, message.RoomID)
			if err != nil {
				return processed, fmt.Errorf("count messages since last context injection: %w", err)
			}
			if messagesSinceLastInjection <= w.CooldownMessages {
				continue
			}
		}

		vectors, err := w.Embedder.Embed(ctx, []string{message.Body})
		if err != nil {
			return processed, fmt.Errorf("embed context injection message %s: %w", message.MessageID, err)
		}
		if len(vectors) != 1 {
			return processed, fmt.Errorf("embed context injection message returned %d vectors", len(vectors))
		}
		vector := vectors[0]
		if !message.HasEmbedding {
			if err := w.Queue.UpdateMessageEmbedding(ctx, message.MessageID, vector); err != nil {
				return processed, fmt.Errorf("update context injection message embedding %s: %w", message.MessageID, err)
			}
		}

		candidates, err := w.Queue.SearchMemoryCandidatesByEmbedding(ctx, message.OrgID, vector, w.MaxMemoriesPerMsg)
		if err != nil {
			return processed, fmt.Errorf("search context injection memory candidates for %s: %w", message.MessageID, err)
		}
		if len(candidates) == 0 {
			continue
		}

		scoringCandidates := make([]EllieProactiveInjectionCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			alreadyInjected, err := w.Queue.WasInjectedSinceCompaction(ctx, message.OrgID, message.RoomID, candidate.MemoryID)
			if err != nil {
				return processed, fmt.Errorf("check context injection dedupe for memory %s: %w", candidate.MemoryID, err)
			}
			if alreadyInjected {
				continue
			}
			scoringCandidates = append(scoringCandidates, EllieProactiveInjectionCandidate{
				MemoryID:           candidate.MemoryID,
				Title:              candidate.Title,
				Content:            candidate.Content,
				Similarity:         candidate.Similarity,
				Importance:         candidate.Importance,
				Confidence:         candidate.Confidence,
				OccurredAt:         candidate.OccurredAt,
				SupersedesMemoryID: candidate.SupersededBy,
			})
		}

		if len(scoringCandidates) == 0 {
			continue
		}

		bundle := w.Service.BuildBundle(EllieProactiveInjectionBuildInput{
			Now:              time.Now().UTC(),
			RoomMessageCount: 0,
			PriorInjections:  0,
			Candidates:       scoringCandidates,
		})
		if len(bundle.Items) == 0 || strings.TrimSpace(bundle.Body) == "" {
			continue
		}

		_, err = w.Queue.CreateInjectionMessage(ctx, store.CreateEllieContextInjectionMessageInput{
			OrgID:          message.OrgID,
			RoomID:         message.RoomID,
			SenderID:       deterministicEllieSenderID(message.OrgID),
			Body:           bundle.Body,
			MessageType:    "context_injection",
			ConversationID: message.ConversationID,
			CreatedAt:      time.Now().UTC(),
		})
		if err != nil {
			return processed, fmt.Errorf("create context injection message for %s: %w", message.MessageID, err)
		}

		for _, item := range bundle.Items {
			if err := w.Queue.RecordInjection(ctx, message.OrgID, message.RoomID, item.MemoryID, time.Now().UTC()); err != nil {
				return processed, fmt.Errorf("record context injection ledger for memory %s: %w", item.MemoryID, err)
			}
		}

		processed += 1
	}

	return processed, nil
}

func deterministicEllieSenderID(orgID string) string {
	normalizedOrgID := strings.TrimSpace(orgID)
	if normalizedOrgID == "" {
		normalizedOrgID = "unknown-org"
	}
	sum := md5.Sum([]byte(normalizedOrgID + ":ellie"))
	encoded := hex.EncodeToString(sum[:])
	return fmt.Sprintf(
		"%s-%s-%s-%s-%s",
		encoded[0:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:32],
	)
}
