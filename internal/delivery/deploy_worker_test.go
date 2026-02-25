package delivery

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestDeployWorkerContinuousNoInFlightCreatesTask(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()

	store := &fakeDeployWorkerStore{locked: true}
	tasks := &fakeDeployTaskRepo{}
	worker := &DeployWorker{
		clock:       clock.NewFake(time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       store,
		projects:    &fakeDeployProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		tasks:       tasks,
		assignments: &fakeDeployAssignmentRepo{err: repo.ErrNotFound},
		inbox:       &fakeDeployInboxRepo{},
		users:       &fakeDeployUserRepo{},
	}

	payload, _ := json.Marshal(deployTaskCreatePayload{ProjectID: projectID, CommitSHA: "abc123", DeliveryMode: deliveryModeContinuous})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tasks.created) != 1 {
		t.Fatalf("created tasks = %d, want 1", len(tasks.created))
	}
	if len(store.deployTaskUpdates) != 1 {
		t.Fatalf("deploy task updates = %d, want 1", len(store.deployTaskUpdates))
	}
	if store.deployTaskUpdates[0].projectID != projectID {
		t.Fatalf("updated project id = %s, want %s", store.deployTaskUpdates[0].projectID, projectID)
	}
	if store.deployTaskUpdates[0].deployTaskID != tasks.created[0].ID {
		t.Fatalf("updated deploy_task_id = %s, want %s", store.deployTaskUpdates[0].deployTaskID, tasks.created[0].ID)
	}

	var metadata map[string]any
	if err := json.Unmarshal(tasks.created[0].Metadata, &metadata); err != nil {
		t.Fatalf("unmarshal task metadata: %v", err)
	}
	if metadata["task_type"] != deliveryTaskTypeDeploy {
		t.Fatalf("task_type = %v, want %q", metadata["task_type"], deliveryTaskTypeDeploy)
	}
}

func TestDeployWorkerContinuousInFlightSkips(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()

	store := &fakeDeployWorkerStore{locked: true, hasInFlight: true}
	tasks := &fakeDeployTaskRepo{}
	worker := &DeployWorker{
		clock:       clock.NewFake(time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       store,
		projects:    &fakeDeployProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		tasks:       tasks,
		assignments: &fakeDeployAssignmentRepo{err: repo.ErrNotFound},
		inbox:       &fakeDeployInboxRepo{},
		users:       &fakeDeployUserRepo{},
	}

	payload, _ := json.Marshal(deployTaskCreatePayload{ProjectID: projectID, CommitSHA: "abc123", DeliveryMode: deliveryModeContinuous})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tasks.created) != 0 {
		t.Fatalf("created tasks = %d, want 0", len(tasks.created))
	}
	if len(store.deployTaskUpdates) != 0 {
		t.Fatalf("deploy task updates = %d, want 0", len(store.deployTaskUpdates))
	}
}

func TestDeployWorkerGatedCreatesInboxItemNoTask(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()
	adminID := uuid.New()

	inbox := &fakeDeployInboxRepo{}
	tasks := &fakeDeployTaskRepo{}
	worker := &DeployWorker{
		clock:       clock.NewFake(time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       &fakeDeployWorkerStore{},
		projects:    &fakeDeployProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		tasks:       tasks,
		assignments: &fakeDeployAssignmentRepo{err: repo.ErrNotFound},
		inbox:       inbox,
		users: &fakeDeployUserRepo{users: []repo.HumanUser{{
			ID:             adminID,
			OrganizationID: orgID,
			Role:           "admin",
			IsActive:       true,
		}}},
	}

	payload, _ := json.Marshal(deployTaskCreatePayload{ProjectID: projectID, CommitSHA: "abc123", DeliveryMode: deliveryModeGated})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tasks.created) != 0 {
		t.Fatalf("created tasks = %d, want 0", len(tasks.created))
	}
	if len(inbox.created) != 1 {
		t.Fatalf("inbox items = %d, want 1", len(inbox.created))
	}
	if inbox.created[0].ItemType != "deploy_approval" {
		t.Fatalf("inbox item type = %q, want deploy_approval", inbox.created[0].ItemType)
	}
}

func TestDeployWorkerScheduledOutsideWindowSkips(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	orgID := uuid.New()
	base := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	outsideWindow := base.Add(30 * time.Minute)

	store := &fakeDeployWorkerStore{nextFireAt: &outsideWindow}
	tasks := &fakeDeployTaskRepo{}
	worker := &DeployWorker{
		clock:       clock.NewFake(base),
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       store,
		projects:    &fakeDeployProjectRepo{project: repo.Project{ID: projectID, OrganizationID: orgID}},
		tasks:       tasks,
		assignments: &fakeDeployAssignmentRepo{err: repo.ErrNotFound},
		inbox:       &fakeDeployInboxRepo{},
		users:       &fakeDeployUserRepo{},
	}

	payload, _ := json.Marshal(deployTaskCreatePayload{ProjectID: projectID, CommitSHA: "abc123", DeliveryMode: deliveryModeScheduled})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tasks.created) != 0 {
		t.Fatalf("created tasks = %d, want 0", len(tasks.created))
	}
	if len(store.deployTaskUpdates) != 0 {
		t.Fatalf("deploy task updates = %d, want 0", len(store.deployTaskUpdates))
	}
}

type fakeDeployWorkerStore struct {
	locked            bool
	hasInFlight       bool
	nextFireAt        *time.Time
	deployTaskUpdates []fakeDeployTaskUpdate
}

type fakeDeployTaskUpdate struct {
	projectID    uuid.UUID
	deployTaskID uuid.UUID
}

func (f *fakeDeployWorkerStore) TryProjectLock(context.Context, uuid.UUID) (bool, error) {
	return f.locked, nil
}

func (f *fakeDeployWorkerStore) UnlockProjectLock(context.Context, uuid.UUID) error {
	return nil
}

func (f *fakeDeployWorkerStore) HasInFlightDeployTask(context.Context, uuid.UUID) (bool, error) {
	return f.hasInFlight, nil
}

func (f *fakeDeployWorkerStore) SetActiveEnvironmentDeployTask(_ context.Context, projectID, deployTaskID uuid.UUID) error {
	f.deployTaskUpdates = append(f.deployTaskUpdates, fakeDeployTaskUpdate{projectID: projectID, deployTaskID: deployTaskID})
	return nil
}

func (f *fakeDeployWorkerStore) NextScheduledFireAt(context.Context, uuid.UUID) (*time.Time, error) {
	return f.nextFireAt, nil
}

type fakeDeployProjectRepo struct {
	project repo.Project
	err     error
}

func (f *fakeDeployProjectRepo) GetByID(context.Context, uuid.UUID) (repo.Project, error) {
	if f.err != nil {
		return repo.Project{}, f.err
	}
	return f.project, nil
}

type fakeDeployTaskRepo struct {
	created []repo.ProjectTask
}

func (f *fakeDeployTaskRepo) Create(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if task.ID == uuid.Nil {
		task.ID = uuid.New()
	}
	f.created = append(f.created, task)
	return task, nil
}

type fakeDeployAssignmentRepo struct {
	assignment repo.AgentProjectAssignment
	err        error
}

func (f *fakeDeployAssignmentRepo) GetPM(context.Context, uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return repo.AgentProjectAssignment{}, f.err
	}
	return f.assignment, nil
}

type fakeDeployInboxRepo struct {
	created []repo.InboxItem
	err     error
}

func (f *fakeDeployInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	if f.err != nil {
		return repo.InboxItem{}, f.err
	}
	f.created = append(f.created, item)
	return item, nil
}

type fakeDeployUserRepo struct {
	users []repo.HumanUser
	err   error
}

func (f *fakeDeployUserRepo) List(context.Context, uuid.UUID) ([]repo.HumanUser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]repo.HumanUser(nil), f.users...), nil
}

var _ projectRepository = (*fakeDeployProjectRepo)(nil)
var _ projectTaskRepository = (*fakeDeployTaskRepo)(nil)
var _ assignmentRepository = (*fakeDeployAssignmentRepo)(nil)
var _ inboxRepository = (*fakeDeployInboxRepo)(nil)
var _ userRepository = (*fakeDeployUserRepo)(nil)
var _ deployWorkerStore = (*fakeDeployWorkerStore)(nil)
