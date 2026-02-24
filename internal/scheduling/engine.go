package scheduling

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

const scheduledTaskType = "scheduled"

type taskCreator interface {
	CreateTask(ctx context.Context, req tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error)
}

type domainEventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type engineStore interface {
	TaskExistsInWindow(ctx context.Context, scheduleID uuid.UUID, windowStart time.Time) (bool, error)
	CountPendingTasks(ctx context.Context, scheduleID uuid.UUID) (int, error)
	UpdateScheduleFire(ctx context.Context, scheduleID uuid.UUID, firedAt time.Time, nextRun *time.Time) error
}

type ScheduleEngineOptions struct {
	Pool       *pgxpool.Pool
	Store      engineStore
	Tasks      taskCreator
	Events     domainEventPublisher
	Clock      clock.Clock
	Logger     *slog.Logger
	CronParser *CronParser
}

type ScheduleEngine struct {
	store      engineStore
	tasks      taskCreator
	events     domainEventPublisher
	clock      clock.Clock
	logger     *slog.Logger
	cronParser *CronParser
}

func NewScheduleEngine(opts ScheduleEngineOptions) (*ScheduleEngine, error) {
	if opts.Store == nil {
		if opts.Pool == nil {
			return nil, fmt.Errorf("schedule engine requires pool or store")
		}
		opts.Store = newSQLEngineStore(opts.Pool)
	}
	if opts.Tasks == nil {
		return nil, fmt.Errorf("schedule engine requires task creator")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("schedule engine requires domain event publisher")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.CronParser == nil {
		opts.CronParser = NewCronParser()
	}

	return &ScheduleEngine{
		store:      opts.Store,
		tasks:      opts.Tasks,
		events:     opts.Events,
		clock:      opts.Clock,
		logger:     opts.Logger,
		cronParser: opts.CronParser,
	}, nil
}

func (e *ScheduleEngine) MaybeFire(ctx context.Context, schedule repo.TaskSchedule) error {
	parsed, err := e.cronParser.ParseExpression(schedule.CronExpression)
	if err != nil {
		return err
	}

	now := e.clock.Now().UTC()
	lastScheduledRun, ok := mostRecentScheduledRun(parsed, now)
	if !ok {
		return nil
	}

	windowStart := lastScheduledRun.Add(-1 * time.Minute)
	exists, err := e.store.TaskExistsInWindow(ctx, schedule.ID, windowStart)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return e.Fire(ctx, schedule)
}

func (e *ScheduleEngine) Fire(ctx context.Context, schedule repo.TaskSchedule) error {
	overlapPolicy := strings.ToLower(strings.TrimSpace(schedule.OverlapPolicy))
	if overlapPolicy == "" {
		overlapPolicy = "skip"
	}

	if overlapPolicy == "skip" {
		pendingCount, err := e.store.CountPendingTasks(ctx, schedule.ID)
		if err != nil {
			return err
		}
		if pendingCount > 0 {
			return e.publishScheduleEvent(ctx, schedule.OrganizationID, "system.schedule.skipped", map[string]any{
				"schedule_id": schedule.ID,
				"reason":      "overlap_policy_skip",
			})
		}
	}

	title := strings.TrimSpace(schedule.DisplayName)
	if title == "" {
		title = fmt.Sprintf("Scheduled run - %s", schedule.ID.String())
	}

	flowTemplateID := schedule.FlowTemplateID
	scheduleID := schedule.ID
	taskMetadata, _ := json.Marshal(map[string]any{"task_type": scheduledTaskType})
	createdTask, err := e.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      schedule.ProjectID,
		Title:          title,
		FlowTemplateID: &flowTemplateID,
		ScheduleID:     &scheduleID,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       taskMetadata,
	})
	if err != nil {
		return err
	}

	now := e.clock.Now().UTC()
	nextRun := e.ComputeNextRun(schedule, now)
	var nextRunPtr *time.Time
	if !nextRun.IsZero() {
		next := nextRun.UTC()
		nextRunPtr = &next
	}
	if err := e.store.UpdateScheduleFire(ctx, schedule.ID, now, nextRunPtr); err != nil {
		return err
	}

	return e.publishScheduleEvent(ctx, schedule.OrganizationID, "system.schedule.fired", map[string]any{
		"schedule_id": schedule.ID,
		"task_id":     createdTask.ID,
		"project_id":  schedule.ProjectID,
	})
}

func (e *ScheduleEngine) ComputeNextRun(schedule repo.TaskSchedule, from time.Time) time.Time {
	parsed, err := e.cronParser.ParseExpression(schedule.CronExpression)
	if err != nil {
		return time.Time{}
	}
	return parsed.Next(from.UTC())
}

func (e *ScheduleEngine) publishScheduleEvent(ctx context.Context, organizationID uuid.UUID, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return e.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      eventType,
		ActorType:      "system",
		Payload:        encoded,
	})
}

func mostRecentScheduledRun(schedule CronSchedule, now time.Time) (time.Time, bool) {
	reference := now.UTC().Truncate(time.Minute)
	if reference.IsZero() {
		return time.Time{}, false
	}

	lookback := time.Minute
	for i := 0; i < 24; i++ {
		candidate := schedule.Next(reference.Add(-lookback))
		if candidate.After(reference) {
			lookback *= 2
			continue
		}

		last := candidate
		for {
			next := schedule.Next(last)
			if next.After(reference) {
				return last, true
			}
			last = next
		}
	}

	return time.Time{}, false
}

type sqlEngineStore struct {
	pool *pgxpool.Pool
}

func newSQLEngineStore(pool *pgxpool.Pool) *sqlEngineStore {
	return &sqlEngineStore{pool: pool}
}

func (s *sqlEngineStore) TaskExistsInWindow(ctx context.Context, scheduleID uuid.UUID, windowStart time.Time) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM project_task
			WHERE schedule_id = $1
			  AND created_at >= $2
			LIMIT 1
		)
	`, scheduleID, windowStart.UTC()).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *sqlEngineStore) CountPendingTasks(ctx context.Context, scheduleID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE schedule_id = $1
		  AND work_status IN ('draft', 'queued', 'in_progress', 'blocked', 'on_hold')
	`, scheduleID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *sqlEngineStore) UpdateScheduleFire(ctx context.Context, scheduleID uuid.UUID, firedAt time.Time, nextRun *time.Time) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE task_schedule
		SET
			last_fired_at = $2,
			next_fire_at = $3
		WHERE id = $1
	`, scheduleID, firedAt.UTC(), nextRun)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return repo.ErrNotFound
	}
	return nil
}
