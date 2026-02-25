package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestPushWorkerRetryReenqueuesWithBackoff(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()
	remoteID := uuid.New()
	base := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)

	enqueuer := &fakePushEnqueuer{}
	worker := &PushWorker{
		git:          &fakePushGitService{pushErr: errors.New("push failed")},
		enqueuer:     enqueuer,
		clock:        clock.NewFake(base),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		projects:     &fakePushProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID, DeliveryMode: deliveryModeContinuous}},
		remotes:      &fakePushRemoteRepo{remoteByID: map[uuid.UUID]repo.ProjectRemote{remoteID: {ID: remoteID, ProjectID: projectID}}},
		environments: &fakePushEnvironmentRepo{},
		taskEvents:   &fakePushTaskEventRepo{},
		inbox:        &fakePushInboxRepo{},
		users:        &fakePushUserRepo{},
		jitter:       func(d time.Duration) time.Duration { return d },
	}

	payload, _ := json.Marshal(pushJobPayload{ProjectID: projectID, RemoteID: remoteID, CommitSHA: "deadbeef", Attempt: 1})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(enqueuer.jobs) != 1 {
		t.Fatalf("enqueued jobs = %d, want 1", len(enqueuer.jobs))
	}
	job := enqueuer.jobs[0]
	if job.jobType != pushExecuteJobType {
		t.Fatalf("job type = %q, want %q", job.jobType, pushExecuteJobType)
	}
	if job.runAfter == nil {
		t.Fatal("expected run_after for retry job")
	}
	wantRunAfter := base.Add(5 * time.Second)
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", job.runAfter, wantRunAfter)
	}
	retryPayload, ok := job.payload.(pushJobPayload)
	if !ok {
		t.Fatalf("payload type = %T, want pushJobPayload", job.payload)
	}
	if retryPayload.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", retryPayload.Attempt)
	}
}

func TestPushWorkerFinalFailureCreatesEscalation(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()
	remoteID := uuid.New()
	deployTaskID := uuid.New()
	adminID := uuid.New()

	enqueuer := &fakePushEnqueuer{}
	inbox := &fakePushInboxRepo{}
	taskEvents := &fakePushTaskEventRepo{}
	worker := &PushWorker{
		git:          &fakePushGitService{pushErr: errors.New("push failed terminal")},
		enqueuer:     enqueuer,
		clock:        clock.NewFake(time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)),
		logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		projects:     &fakePushProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID, DeliveryMode: deliveryModeContinuous}},
		remotes:      &fakePushRemoteRepo{remoteByID: map[uuid.UUID]repo.ProjectRemote{remoteID: {ID: remoteID, ProjectID: projectID}}},
		environments: &fakePushEnvironmentRepo{},
		taskEvents:   taskEvents,
		inbox:        inbox,
		users: &fakePushUserRepo{users: []repo.HumanUser{{
			ID:             adminID,
			OrganizationID: orgID,
			Role:           "admin",
			IsActive:       true,
		}}},
		jitter: func(d time.Duration) time.Duration { return d },
	}

	payload, _ := json.Marshal(pushJobPayload{ProjectID: projectID, RemoteID: remoteID, CommitSHA: "deadbeef", Attempt: 3, DeployTaskID: deployTaskID})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(enqueuer.jobs) != 0 {
		t.Fatalf("enqueued jobs = %d, want 0", len(enqueuer.jobs))
	}
	if len(inbox.created) != 1 {
		t.Fatalf("inbox items = %d, want 1", len(inbox.created))
	}
	if inbox.created[0].ItemType != "escalation" {
		t.Fatalf("inbox item type = %q, want escalation", inbox.created[0].ItemType)
	}
	if len(taskEvents.events) != 1 {
		t.Fatalf("task events = %d, want 1", len(taskEvents.events))
	}
	if taskEvents.events[0].EventType != "push_failed" {
		t.Fatalf("task event type = %q, want push_failed", taskEvents.events[0].EventType)
	}
}

type fakePushGitService struct {
	pushErr   error
	pushCalls int
}

func (f *fakePushGitService) Merge(context.Context, uuid.UUID, string, string) error { return nil }

func (f *fakePushGitService) Push(context.Context, repo.ProjectRemote) error {
	f.pushCalls++
	return f.pushErr
}

func (f *fakePushGitService) CommitExists(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

type fakePushEnqueuer struct {
	jobs []fakePushEnqueuedJob
}

type fakePushEnqueuedJob struct {
	jobType  string
	payload  any
	runAfter *time.Time
}

func (f *fakePushEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, jobType string, _ int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	entry := fakePushEnqueuedJob{jobType: jobType, payload: payload}
	if runAfter != nil {
		at := *runAfter
		entry.runAfter = &at
	}
	f.jobs = append(f.jobs, entry)
	return uuid.New(), nil
}

type fakePushProjectRepo struct {
	project repo.Project
}

func (f *fakePushProjectRepo) GetByID(context.Context, uuid.UUID) (repo.Project, error) {
	return f.project, nil
}

type fakePushRemoteRepo struct {
	remoteByID       map[uuid.UUID]repo.ProjectRemote
	defaultByProject map[uuid.UUID]repo.ProjectRemote
}

func (f *fakePushRemoteRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectRemote, error) {
	if remote, ok := f.remoteByID[id]; ok {
		return remote, nil
	}
	return repo.ProjectRemote{}, repo.ErrNotFound
}

func (f *fakePushRemoteRepo) GetDefault(_ context.Context, projectID uuid.UUID) (repo.ProjectRemote, error) {
	if remote, ok := f.defaultByProject[projectID]; ok {
		return remote, nil
	}
	return repo.ProjectRemote{}, repo.ErrNotFound
}

type fakePushEnvironmentRepo struct {
	environmentsByProject map[uuid.UUID][]repo.ProjectEnvironment
}

func (f *fakePushEnvironmentRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectEnvironment, error) {
	return repo.ProjectEnvironment{}, repo.ErrNotFound
}

func (f *fakePushEnvironmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error) {
	return append([]repo.ProjectEnvironment(nil), f.environmentsByProject[projectID]...), nil
}

type fakePushTaskEventRepo struct {
	events []repo.ProjectTaskEvent
}

func (f *fakePushTaskEventRepo) Record(_ context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error) {
	f.events = append(f.events, event)
	return event, nil
}

type fakePushInboxRepo struct {
	created []repo.InboxItem
}

func (f *fakePushInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	f.created = append(f.created, item)
	return item, nil
}

type fakePushUserRepo struct {
	users []repo.HumanUser
}

func (f *fakePushUserRepo) List(context.Context, uuid.UUID) ([]repo.HumanUser, error) {
	return append([]repo.HumanUser(nil), f.users...), nil
}
