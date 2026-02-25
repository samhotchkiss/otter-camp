package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestDeadLetterHandlerHandleEmitsDomainAndRunEventsAndCreatesInbox(t *testing.T) {
	repos := newFakeRunDeps()
	taskEvents := &fakeTaskEventRecorder{}
	inbox := &fakeInboxCreator{}
	svc := newRunServiceForDeadLetterTests(t, repos, taskEvents, inbox)

	projectID := uuid.New()
	taskID := uuid.New()
	runRecord, err := repos.runs.Create(context.Background(), Run{
		OrganizationID: uuid.New(),
		ProjectID:      &projectID,
		TaskID:         &taskID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "dead_letter",
		TriggerType:    "api",
		FailureReason:  strPtr("max attempts exceeded"),
		FailureClass:   strPtr("transient"),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	step := repos.seedStep(runRecord.ID, 1)
	worker := "mcp"
	repos.attempts.seed(step.ID, RunAttempt{RunStepID: step.ID, AttemptNumber: 3, Trigger: "retry_transient", Status: "failed", WorkerType: &worker})

	handler := NewDeadLetterHandler(svc.(*runService))
	if err := handler.Handle(context.Background(), runRecord.ID, "max attempts exceeded"); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	foundRunEvent := false
	for _, event := range repos.events.appended {
		if event.RunID == runRecord.ID && event.EventType == "run_failed" {
			foundRunEvent = true
			break
		}
	}
	if !foundRunEvent {
		t.Fatal("expected run_failed run_event")
	}

	foundDomainEvent := false
	for _, event := range repos.bus.events {
		if event.OrganizationID == runRecord.OrganizationID && event.EventType == "run.dead_lettered" {
			foundDomainEvent = true
			break
		}
	}
	if !foundDomainEvent {
		t.Fatal("expected run.dead_lettered domain_event")
	}

	if len(taskEvents.recorded) == 0 {
		t.Fatal("expected project_task_event record")
	}
	if taskEvents.recorded[0].EventType != "run_dead_lettered" {
		t.Fatalf("task event type = %q, want run_dead_lettered", taskEvents.recorded[0].EventType)
	}
	if inbox.calls == 0 {
		t.Fatal("expected inbox create call")
	}
}

func TestDeadLetterHandlerHandleInboxFailureIsNonFatal(t *testing.T) {
	repos := newFakeRunDeps()
	taskEvents := &fakeTaskEventRecorder{}
	inbox := &fakeInboxCreator{err: errors.New("inbox unavailable")}
	svc := newRunServiceForDeadLetterTests(t, repos, taskEvents, inbox)

	projectID := uuid.New()
	taskID := uuid.New()
	runRecord, err := repos.runs.Create(context.Background(), Run{
		OrganizationID: uuid.New(),
		ProjectID:      &projectID,
		TaskID:         &taskID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "dead_letter",
		TriggerType:    "api",
		FailureReason:  strPtr("max attempts exceeded"),
		FailureClass:   strPtr("transient"),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("seed run: %v", err)
	}
	step := repos.seedStep(runRecord.ID, 1)
	worker := "mcp"
	repos.attempts.seed(step.ID, RunAttempt{RunStepID: step.ID, AttemptNumber: 3, Trigger: "retry_transient", Status: "failed", WorkerType: &worker})

	handler := NewDeadLetterHandler(svc.(*runService))
	if err := handler.Handle(context.Background(), runRecord.ID, "max attempts exceeded"); err != nil {
		t.Fatalf("Handle error = %v, want nil", err)
	}

	if len(repos.bus.events) == 0 {
		t.Fatal("expected domain events even when inbox create fails")
	}
	if len(taskEvents.recorded) == 0 {
		t.Fatal("expected project task event even when inbox create fails")
	}
}

func newRunServiceForDeadLetterTests(t *testing.T, deps *fakeRunDeps, taskEvents taskEventRecorder, inbox inboxCreator) RunService {
	t.Helper()
	svc, err := NewRunService(RunServiceOptions{
		Runs:        deps.runs,
		RunSteps:    deps.steps,
		Attempts:    deps.attempts,
		RunEvent:    deps.events,
		TaskEvents:  taskEvents,
		Inbox:       inbox,
		Assignments: &noopAssignments{},
		Agents:      &noopAgentReader{},
		Users:       &noopUserReader{},
		EventBus:    deps.bus,
		Clock:       clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewRunService: %v", err)
	}
	return svc
}

type fakeTaskEventRecorder struct {
	recorded []repo.ProjectTaskEvent
}

func (f *fakeTaskEventRecorder) Record(_ context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error) {
	f.recorded = append(f.recorded, event)
	return event, nil
}

type fakeInboxCreator struct {
	calls int
	err   error
}

func (f *fakeInboxCreator) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	f.calls++
	if f.err != nil {
		return repo.InboxItem{}, f.err
	}
	return item, nil
}
