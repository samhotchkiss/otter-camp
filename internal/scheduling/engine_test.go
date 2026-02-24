package scheduling

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

func TestScheduleEngineMaybeFireSkipCreatesTaskAndFiredEvent(t *testing.T) {
	store := &fakeEngineStore{taskExistsResults: []bool{false}, pendingCount: 0}
	tasks := &fakeTaskCreator{}
	events := &fakeEventPublisher{}
	now := time.Date(2026, time.February, 24, 10, 0, 30, 0, time.UTC)

	engine := &ScheduleEngine{
		store:      store,
		tasks:      tasks,
		events:     events,
		clock:      clock.NewFake(now),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cronParser: NewCronParser(),
	}

	schedule := buildTestSchedule("skip")
	if err := engine.MaybeFire(context.Background(), schedule); err != nil {
		t.Fatalf("MaybeFire: %v", err)
	}

	if tasks.createCalls != 1 {
		t.Fatalf("create task calls = %d, want 1", tasks.createCalls)
	}
	if tasks.lastRequest.ScheduleID == nil || *tasks.lastRequest.ScheduleID != schedule.ID {
		t.Fatalf("schedule_id not propagated to task create request")
	}
	if store.updateCalls != 1 {
		t.Fatalf("update schedule calls = %d, want 1", store.updateCalls)
	}
	if len(events.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.events))
	}
	if events.events[0].EventType != "system.schedule.fired" {
		t.Fatalf("event type = %q, want system.schedule.fired", events.events[0].EventType)
	}
}

func TestScheduleEngineMaybeFireSkipWithPendingTaskEmitsSkipped(t *testing.T) {
	store := &fakeEngineStore{taskExistsResults: []bool{false}, pendingCount: 2}
	tasks := &fakeTaskCreator{}
	events := &fakeEventPublisher{}

	engine := &ScheduleEngine{
		store:      store,
		tasks:      tasks,
		events:     events,
		clock:      clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 30, 0, time.UTC)),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cronParser: NewCronParser(),
	}

	schedule := buildTestSchedule("skip")
	if err := engine.MaybeFire(context.Background(), schedule); err != nil {
		t.Fatalf("MaybeFire: %v", err)
	}

	if tasks.createCalls != 0 {
		t.Fatalf("create task calls = %d, want 0", tasks.createCalls)
	}
	if len(events.events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events.events))
	}
	if events.events[0].EventType != "system.schedule.skipped" {
		t.Fatalf("event type = %q, want system.schedule.skipped", events.events[0].EventType)
	}
}

func TestScheduleEngineMaybeFireQueueCreatesTaskWhenPendingExists(t *testing.T) {
	store := &fakeEngineStore{taskExistsResults: []bool{false}, pendingCount: 5}
	tasks := &fakeTaskCreator{}
	events := &fakeEventPublisher{}

	engine := &ScheduleEngine{
		store:      store,
		tasks:      tasks,
		events:     events,
		clock:      clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 30, 0, time.UTC)),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cronParser: NewCronParser(),
	}

	schedule := buildTestSchedule("queue")
	if err := engine.MaybeFire(context.Background(), schedule); err != nil {
		t.Fatalf("MaybeFire: %v", err)
	}

	if tasks.createCalls != 1 {
		t.Fatalf("create task calls = %d, want 1", tasks.createCalls)
	}
	if store.pendingCountCalls != 0 {
		t.Fatalf("pending count calls = %d, want 0 for overlap=queue", store.pendingCountCalls)
	}
}

func TestScheduleEngineMaybeFireIdempotentWithinWindow(t *testing.T) {
	store := &fakeEngineStore{taskExistsResults: []bool{false, true}, pendingCount: 0}
	tasks := &fakeTaskCreator{}
	events := &fakeEventPublisher{}
	fakeClock := clock.NewFake(time.Date(2026, time.February, 24, 10, 0, 30, 0, time.UTC))

	engine := &ScheduleEngine{
		store:      store,
		tasks:      tasks,
		events:     events,
		clock:      fakeClock,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		cronParser: NewCronParser(),
	}

	schedule := buildTestSchedule("skip")
	if err := engine.MaybeFire(context.Background(), schedule); err != nil {
		t.Fatalf("first MaybeFire: %v", err)
	}
	if err := engine.MaybeFire(context.Background(), schedule); err != nil {
		t.Fatalf("second MaybeFire: %v", err)
	}

	if tasks.createCalls != 1 {
		t.Fatalf("create task calls = %d, want 1", tasks.createCalls)
	}
}

func TestScheduleEngineComputeNextRun(t *testing.T) {
	engine := &ScheduleEngine{cronParser: NewCronParser()}
	schedule := buildTestSchedule("skip")
	from := time.Date(2026, time.February, 24, 10, 2, 0, 0, time.UTC)

	next := engine.ComputeNextRun(schedule, from)
	if !next.After(from) {
		t.Fatalf("next run = %s, want after %s", next, from)
	}
}

func buildTestSchedule(overlap string) repo.TaskSchedule {
	return repo.TaskSchedule{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		ProjectID:      uuid.New(),
		FlowTemplateID: uuid.New(),
		DisplayName:    "Daily Standup",
		CronExpression: "* * * * *",
		OverlapPolicy:  overlap,
		IsEnabled:      true,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	}
}

type fakeEngineStore struct {
	taskExistsResults []bool
	taskExistsCalls   int
	pendingCount      int
	pendingCountCalls int
	updateCalls       int
}

func (f *fakeEngineStore) TaskExistsInWindow(context.Context, uuid.UUID, time.Time) (bool, error) {
	if f.taskExistsCalls < len(f.taskExistsResults) {
		value := f.taskExistsResults[f.taskExistsCalls]
		f.taskExistsCalls++
		return value, nil
	}
	f.taskExistsCalls++
	return false, nil
}

func (f *fakeEngineStore) CountPendingTasks(context.Context, uuid.UUID) (int, error) {
	f.pendingCountCalls++
	return f.pendingCount, nil
}

func (f *fakeEngineStore) UpdateScheduleFire(context.Context, uuid.UUID, time.Time, *time.Time) error {
	f.updateCalls++
	return nil
}

type fakeTaskCreator struct {
	createCalls  int
	lastRequest  tasksvc.CreateTaskRequest
	nextTaskID   uuid.UUID
	projectTasks []tasksvc.ProjectTask
}

func (f *fakeTaskCreator) CreateTask(_ context.Context, req tasksvc.CreateTaskRequest) (*tasksvc.ProjectTask, error) {
	f.createCalls++
	f.lastRequest = req
	id := f.nextTaskID
	if id == uuid.Nil {
		id = uuid.New()
	}
	taskRecord := tasksvc.ProjectTask{ID: id, ProjectID: req.ProjectID}
	f.projectTasks = append(f.projectTasks, taskRecord)
	return &taskRecord, nil
}

type fakeEventPublisher struct {
	events []eventbus.DomainEvent
}

func (f *fakeEventPublisher) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	copied := event
	if len(event.Payload) > 0 {
		copied.Payload = append(json.RawMessage(nil), event.Payload...)
	}
	f.events = append(f.events, copied)
	return nil
}
