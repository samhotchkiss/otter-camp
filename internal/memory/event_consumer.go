package memory

import (
	"context"
	"encoding/json"
	"fmt"
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
	CompactionRuns    *repo.MemoryCompactionRunRepo
	FrictionThreshold int
}

type EventConsumer struct {
	events memoryEventSubscriber
	enq    memoryJobEnqueuer
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
		events:            opts.Events,
		enq:               opts.Enqueuer,
		runs:              opts.CompactionRuns,
		frictionThreshold: opts.FrictionThreshold,
		frictionCounts:    make(map[uuid.UUID]int),
	}
	if consumer.enq == nil {
		consumer.enq = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
	}
	if consumer.runs == nil {
		consumer.runs = repo.NewMemoryCompactionRunRepo(opts.Pool)
	}
	if consumer.frictionThreshold <= 0 {
		consumer.frictionThreshold = defaultFrictionSignalThreshold
	}

	return consumer, nil
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
