package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/memory/compaction"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const defaultFrictionSignalThreshold = 5

const (
	MemoryExtractTurnJobType    = "memory_extract_turn"
	memoryExtractTurnJobPrio    = 50
	memoryEmbeddingDimensions   = 1536
	jobStatusPending            = "pending"
	jobStatusClaimed            = "claimed"
	jobStatusDone               = "done"
	jobStatusDeadLetter         = "dead_letter"
	memoryTurnCompletedConsumer = "memory.turn-completed"
)

type memoryEventSubscriber interface {
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type memoryJobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type EventConsumerOptions struct {
	Pool              *pgxpool.Pool
	Events            memoryEventSubscriber
	Enqueuer          memoryJobEnqueuer
	Messages          memoryChatMessages
	Extractor         memoryTurnExtractor
	CompactionRuns    *repo.MemoryCompactionRunRepo
	FrictionThreshold int
}

type memoryChatMessages interface {
	ListByTurn(ctx context.Context, sessionID, turnID uuid.UUID) ([]repo.ChatMessage, error)
}

type memoryTurnExtractor interface {
	ExtractFromMessages(ctx context.Context, orgID uuid.UUID, msgs []ChatMessage, sourceContext ExtractionSourceContext) error
}

type extractTurnPayload struct {
	OrganizationID uuid.UUID `json:"organization_id"`
	SessionID      uuid.UUID `json:"session_id"`
	TurnID         uuid.UUID `json:"turn_id"`
}

type EventConsumer struct {
	pool   *pgxpool.Pool
	events memoryEventSubscriber
	enq    memoryJobEnqueuer
	msgs   memoryChatMessages
	extr   memoryTurnExtractor
	runs   *repo.MemoryCompactionRunRepo

	frictionThreshold int
	mu                sync.Mutex
	frictionCounts    map[uuid.UUID]int
}

func NewEventConsumer(opts EventConsumerOptions) (*EventConsumer, error) {
	if opts.Pool == nil {
		return nil, fmt.Errorf("memory event consumer requires database pool")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("memory event consumer requires event subscriber")
	}

	consumer := &EventConsumer{
		pool:              opts.Pool,
		events:            opts.Events,
		enq:               opts.Enqueuer,
		msgs:              opts.Messages,
		extr:              opts.Extractor,
		runs:              opts.CompactionRuns,
		frictionThreshold: opts.FrictionThreshold,
		frictionCounts:    make(map[uuid.UUID]int),
	}
	if consumer.enq == nil {
		consumer.enq = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
	}
	if consumer.msgs == nil {
		consumer.msgs = repo.NewChatMessageRepo(opts.Pool)
	}
	if consumer.extr == nil {
		extractor, err := NewExtractor(ExtractorOptions{
			Pool:                       opts.Pool,
			Model:                      deterministicTurnExtractionModel{},
			Embedder:                   deterministicTurnEmbeddingModel{},
			BehavioralOverridePatterns: []string{"ignore\\s+all\\s+previous\\s+instructions"},
		})
		if err != nil {
			return nil, fmt.Errorf("memory event consumer default extractor: %w", err)
		}
		consumer.extr = extractor
	}
	if consumer.runs == nil {
		consumer.runs = repo.NewMemoryCompactionRunRepo(opts.Pool)
	}
	if consumer.frictionThreshold <= 0 {
		consumer.frictionThreshold = defaultFrictionSignalThreshold
	}

	return consumer, nil
}

func (c *EventConsumer) RegisterJobs(registrar interface {
	Register(jobType string, handler jobqueue.JobHandler)
}) {
	if c == nil || registrar == nil {
		return
	}

	registrar.Register(MemoryExtractTurnJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload extractTurnPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return fmt.Errorf("decode memory turn extraction payload: %w", err)
		}
		if payload.OrganizationID == uuid.Nil || payload.SessionID == uuid.Nil || payload.TurnID == uuid.Nil {
			return fmt.Errorf("memory turn extraction payload missing required ids")
		}
		return c.handleExtractTurn(ctx, payload)
	})
}

func (c *EventConsumer) SubscribeTaskCompleted(orgID *uuid.UUID) eventbus.Subscription {
	return c.events.Subscribe("memory.task-completed", orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "task.completed" && event.EventType != "task.status_changed" {
			return nil
		}

		var payload struct {
			TaskID    uuid.UUID `json:"task_id"`
			ProjectID uuid.UUID `json:"project_id"`
			ToStatus  string    `json:"to_status"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil
		}
		if event.EventType == "task.status_changed" && payload.ToStatus != "done" {
			return nil
		}
		if payload.TaskID == uuid.Nil || payload.ProjectID == uuid.Nil {
			return nil
		}

		_, err := c.enq.Enqueue(ctx, nil, compaction.MemoryTaskConsolidationJobType, 60, compaction.TaskConsolidationPayload{
			OrganizationID: event.OrganizationID,
			ProjectID:      payload.ProjectID,
			TaskID:         payload.TaskID,
		}, nil)
		return err
	})
}

func (c *EventConsumer) SubscribeTurnCompleted(orgID *uuid.UUID) eventbus.Subscription {
	return c.events.Subscribe(memoryTurnCompletedConsumer, orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "chat.turn.completed" {
			return nil
		}

		var payload struct {
			SessionID uuid.UUID `json:"session_id"`
			TurnID    uuid.UUID `json:"turn_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil
		}
		if payload.SessionID == uuid.Nil || payload.TurnID == uuid.Nil {
			return nil
		}

		exists, err := c.extractTurnJobExists(ctx, event.OrganizationID, payload.TurnID)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}

		_, err = c.enq.Enqueue(ctx, nil, MemoryExtractTurnJobType, memoryExtractTurnJobPrio, extractTurnPayload{
			OrganizationID: event.OrganizationID,
			SessionID:      payload.SessionID,
			TurnID:         payload.TurnID,
		}, nil)
		return err
	})
}

func (c *EventConsumer) SubscribeFrictionSignals(orgID *uuid.UUID) eventbus.Subscription {
	return c.events.Subscribe("memory.friction-signal", orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "agent.friction_signal" {
			return nil
		}

		count := c.incrementFriction(event.OrganizationID)
		if count < c.frictionThreshold {
			return nil
		}

		run, err := c.runs.Create(ctx, repo.MemoryCompactionRun{
			OrganizationID: event.OrganizationID,
			RunType:        "sleep_reflection",
			Status:         "pending",
			ScopeContext:   `{"trigger":"friction_signal"}`,
		})
		if err != nil {
			return err
		}

		if _, err := c.enq.Enqueue(ctx, nil, compaction.MemorySleepReflectionJobType, 70, compaction.SleepReflectionPayload{
			OrganizationID:  event.OrganizationID,
			CompactionRunID: run.ID,
		}, nil); err != nil {
			return err
		}
		c.resetFriction(event.OrganizationID)
		return nil
	})
}

func (c *EventConsumer) handleExtractTurn(ctx context.Context, payload extractTurnPayload) error {
	if c.msgs == nil || c.extr == nil {
		return nil
	}

	completed, err := c.extractTurnJobCompleted(ctx, payload.OrganizationID, payload.TurnID)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}

	records, err := c.msgs.ListByTurn(ctx, payload.SessionID, payload.TurnID)
	if err != nil {
		return err
	}

	messages := make([]ChatMessage, 0, len(records))
	var agentID *uuid.UUID
	for _, record := range records {
		if strings.TrimSpace(record.Status) != "final" {
			continue
		}
		if record.AuthorType == nil || strings.TrimSpace(*record.AuthorType) != "agent" {
			continue
		}
		content := strings.TrimSpace(record.Content)
		if content == "" {
			continue
		}

		messages = append(messages, ChatMessage{
			Role:       strings.TrimSpace(record.Role),
			AuthorType: "agent",
			Content:    content,
		})
		if agentID == nil && record.AuthorID != nil && *record.AuthorID != uuid.Nil {
			clone := *record.AuthorID
			agentID = &clone
		}
	}
	if len(messages) == 0 {
		return nil
	}

	return c.extr.ExtractFromMessages(ctx, payload.OrganizationID, messages, ExtractionSourceContext{
		SessionID: &payload.SessionID,
		AgentID:   agentID,
	})
}

func (c *EventConsumer) extractTurnJobExists(ctx context.Context, organizationID, turnID uuid.UUID) (bool, error) {
	if c.pool == nil {
		return false, nil
	}

	var exists bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue
			WHERE job_type = $1
			  AND payload->>'organization_id' = $2
			  AND payload->>'turn_id' = $3
			  AND status IN ($4, $5, $6, $7)
		)
	`, MemoryExtractTurnJobType, organizationID.String(), turnID.String(), jobStatusPending, jobStatusClaimed, jobStatusDone, jobStatusDeadLetter).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (c *EventConsumer) extractTurnJobCompleted(ctx context.Context, organizationID, turnID uuid.UUID) (bool, error) {
	if c.pool == nil {
		return false, nil
	}

	var exists bool
	err := c.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue
			WHERE job_type = $1
			  AND payload->>'organization_id' = $2
			  AND payload->>'turn_id' = $3
			  AND status = $4
		)
	`, MemoryExtractTurnJobType, organizationID.String(), turnID.String(), jobStatusDone).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (c *EventConsumer) incrementFriction(orgID uuid.UUID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frictionCounts[orgID] = c.frictionCounts[orgID] + 1
	return c.frictionCounts[orgID]
}

func (c *EventConsumer) resetFriction(orgID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.frictionCounts, orgID)
}

type deterministicTurnExtractionModel struct{}

func (deterministicTurnExtractionModel) Extract(_ context.Context, req ExtractionRequest) ([]ExtractedMemory, error) {
	items := make([]ExtractedMemory, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		items = append(items, ExtractedMemory{
			Content:         content,
			MemoryType:      "semantic",
			Entities:        []ExtractedEntity{},
			Confidence:      0.85,
			UtilityEstimate: 0.80,
		})
	}
	return items, nil
}

type deterministicTurnEmbeddingModel struct{}

func (deterministicTurnEmbeddingModel) Embed(_ context.Context, req EmbeddingRequest) ([][]float32, error) {
	vectors := make([][]float32, 0, len(req.Inputs))
	for _, input := range req.Inputs {
		sum := sha256.Sum256([]byte(strings.TrimSpace(input)))
		vector := make([]float32, memoryEmbeddingDimensions)
		vector[0] = float32(sum[0]) / 255.0
		vector[1] = 1
		for i := 0; i < len(sum) && i+2 < len(vector); i++ {
			vector[i+2] = float32(sum[i]) / 255.0
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}
