package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var ErrInvalidCronExpression = errors.New("invalid cron expression")

type taskScheduleRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.TaskSchedule, error)
	Update(ctx context.Context, schedule repo.TaskSchedule) (repo.TaskSchedule, error)
}

type TaskScheduleServiceOptions struct {
	Pool       *pgxpool.Pool
	Schedules  taskScheduleRepository
	Events     eventPublisher
	Clock      clock.Clock
	CronParser *CronParser
}

type eventPublisher interface {
	Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error
}

type TaskScheduleService struct {
	schedules  taskScheduleRepository
	events     eventPublisher
	clock      clock.Clock
	cronParser *CronParser
}

func NewTaskScheduleService(opts TaskScheduleServiceOptions) (*TaskScheduleService, error) {
	if opts.Schedules == nil {
		if opts.Pool == nil {
			return nil, fmt.Errorf("task schedule service requires pool or schedules repository")
		}
		opts.Schedules = repo.NewTaskScheduleRepo(opts.Pool)
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("task schedule service requires event publisher")
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.CronParser == nil {
		opts.CronParser = NewCronParser()
	}

	return &TaskScheduleService{
		schedules:  opts.Schedules,
		events:     opts.Events,
		clock:      opts.Clock,
		cronParser: opts.CronParser,
	}, nil
}

func (s *TaskScheduleService) Enable(ctx context.Context, scheduleID uuid.UUID) (repo.TaskSchedule, error) {
	schedule, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return repo.TaskSchedule{}, err
	}

	nextRun := s.ComputeNextRun(schedule, s.clock.Now().UTC())
	if nextRun.IsZero() {
		return repo.TaskSchedule{}, fmt.Errorf("%w: %s", ErrInvalidCronExpression, schedule.CronExpression)
	}

	schedule.IsEnabled = true
	next := nextRun.UTC()
	schedule.NextFireAt = &next

	updated, err := s.schedules.Update(ctx, schedule)
	if err != nil {
		return repo.TaskSchedule{}, err
	}
	if err := s.publish(ctx, updated.OrganizationID, "system.schedule.enabled", map[string]any{
		"schedule_id": updated.ID,
	}); err != nil {
		return repo.TaskSchedule{}, err
	}

	return updated, nil
}

func (s *TaskScheduleService) Disable(ctx context.Context, scheduleID uuid.UUID) (repo.TaskSchedule, error) {
	schedule, err := s.schedules.GetByID(ctx, scheduleID)
	if err != nil {
		return repo.TaskSchedule{}, err
	}

	schedule.IsEnabled = false
	schedule.NextFireAt = nil

	updated, err := s.schedules.Update(ctx, schedule)
	if err != nil {
		return repo.TaskSchedule{}, err
	}
	if err := s.publish(ctx, updated.OrganizationID, "system.schedule.disabled", map[string]any{
		"schedule_id": updated.ID,
	}); err != nil {
		return repo.TaskSchedule{}, err
	}

	return updated, nil
}

func (s *TaskScheduleService) ComputeNextRun(schedule repo.TaskSchedule, from time.Time) time.Time {
	parsed, err := s.cronParser.ParseExpression(schedule.CronExpression)
	if err != nil {
		return time.Time{}
	}
	return parsed.Next(from.UTC())
}

func (s *TaskScheduleService) publish(ctx context.Context, organizationID uuid.UUID, eventType string, payload map[string]any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.events.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      eventType,
		ActorType:      "system",
		Payload:        encoded,
	})
}
