package delivery

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestAdvisoryLockKeyInputDeterministic(t *testing.T) {
	projectID := uuid.New()
	environmentID := uuid.New()

	first := advisoryLockKeyInput(projectID, environmentID)
	second := advisoryLockKeyInput(projectID, environmentID)
	if first != second {
		t.Fatalf("same project/environment pair should produce identical lock key input: %q != %q", first, second)
	}

	other := advisoryLockKeyInput(projectID, uuid.New())
	if other == first {
		t.Fatalf("different project/environment pair should not produce identical key input: %q", other)
	}
}

func TestPushRetryDelay(t *testing.T) {
	if got := pushRetryDelay(0); got != 30*time.Second {
		t.Fatalf("attempt 0 delay = %s, want %s", got, 30*time.Second)
	}
	if got := pushRetryDelay(1); got != 120*time.Second {
		t.Fatalf("attempt 1 delay = %s, want %s", got, 120*time.Second)
	}
	if got := pushRetryDelay(2); got != 480*time.Second {
		t.Fatalf("attempt 2 delay = %s, want %s", got, 480*time.Second)
	}
}

func TestCreateDeployTaskTitleFormatTruncatesRemoteURL(t *testing.T) {
	environmentID := uuid.New()
	projectID := uuid.New()
	organizationID := uuid.New()
	remoteID := uuid.New()
	pmAgentID := uuid.New()

	remoteURL := "https://git.example.com/very/long/repository/path/that/is/over/sixty-four/characters"
	trimmedURL := remoteURL[:64]

	environmentRepo := &fakeEnvironmentRepo{
		environments: map[uuid.UUID]repo.ProjectEnvironment{
			environmentID: {
				ID:           environmentID,
				ProjectID:    projectID,
				Name:         "production",
				DeliveryMode: "gated",
				RemoteID:     &remoteID,
				IsActive:     true,
			},
		},
	}
	remoteRepo := &fakeRemoteRepo{
		byID: map[uuid.UUID]repo.ProjectRemote{
			remoteID: {
				ID:        remoteID,
				ProjectID: projectID,
				Name:      "origin",
				URL:       remoteURL,
			},
		},
	}
	taskRepo := &fakeTaskRepo{}
	service := &Service{
		remotes:      remoteRepo,
		environments: environmentRepo,
		tasks:        taskRepo,
		projects: &fakeProjectRepo{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, OrganizationID: organizationID},
			},
		},
		assignments: &fakeAssignmentRepo{
			assignments: map[uuid.UUID]repo.AgentProjectAssignment{
				projectID: {ProjectID: projectID, AgentID: pmAgentID, Role: "pm", IsActive: true},
			},
		},
		clock: clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)),
	}

	created, err := service.CreateDeployTask(context.Background(), environmentID, "abc123")
	if err != nil {
		t.Fatalf("CreateDeployTask: %v", err)
	}
	wantTitle := "Deploy production to " + trimmedURL
	if created.Title != wantTitle {
		t.Fatalf("created title = %q, want %q", created.Title, wantTitle)
	}
	if environmentRepo.lastSetDeployTaskID == nil || *environmentRepo.lastSetDeployTaskID != created.ID {
		t.Fatalf("deploy_task_id not set on environment to created task id")
	}
}

type fakeRemoteRepo struct {
	byID     map[uuid.UUID]repo.ProjectRemote
	defaults map[uuid.UUID]repo.ProjectRemote
}

func (f *fakeRemoteRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectRemote, error) {
	item, ok := f.byID[id]
	if !ok {
		return repo.ProjectRemote{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeRemoteRepo) GetDefault(_ context.Context, projectID uuid.UUID) (repo.ProjectRemote, error) {
	item, ok := f.defaults[projectID]
	if !ok {
		return repo.ProjectRemote{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeEnvironmentRepo struct {
	environments        map[uuid.UUID]repo.ProjectEnvironment
	lastSetDeployTaskID *uuid.UUID
	lastDeploymentSHA   string
	lastDeploymentTime  time.Time
}

func (f *fakeEnvironmentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectEnvironment, error) {
	item, ok := f.environments[id]
	if !ok {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeEnvironmentRepo) SetDeployTask(_ context.Context, environmentID uuid.UUID, deployTaskID *uuid.UUID) (repo.ProjectEnvironment, error) {
	item, ok := f.environments[environmentID]
	if !ok {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	item.DeployTaskID = deployTaskID
	f.environments[environmentID] = item
	f.lastSetDeployTaskID = deployTaskID
	return item, nil
}

func (f *fakeEnvironmentRepo) RecordDeployment(_ context.Context, environmentID uuid.UUID, commitSHA string, deployedAt time.Time) (repo.ProjectEnvironment, error) {
	item, ok := f.environments[environmentID]
	if !ok {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	item.LastDeployedCommit = stringPointer(commitSHA)
	item.LastDeployedAt = &deployedAt
	f.environments[environmentID] = item
	f.lastDeploymentSHA = commitSHA
	f.lastDeploymentTime = deployedAt
	return item, nil
}

func (f *fakeEnvironmentRepo) GetActiveByMode(_ context.Context, mode string) ([]repo.ProjectEnvironment, error) {
	items := make([]repo.ProjectEnvironment, 0)
	for _, item := range f.environments {
		if item.DeliveryMode != mode || !item.IsActive {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

type fakeTaskRepo struct {
	created []repo.ProjectTask
}

func (f *fakeTaskRepo) Create(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	created := task
	if created.ID == uuid.Nil {
		created.ID = uuid.New()
	}
	if len(created.Metadata) == 0 {
		created.Metadata = json.RawMessage(`{}`)
	}
	f.created = append(f.created, created)
	return created, nil
}

type fakeProjectRepo struct {
	projects map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	item, ok := f.projects[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeAssignmentRepo struct {
	assignments map[uuid.UUID]repo.AgentProjectAssignment
}

func (f *fakeAssignmentRepo) GetPM(_ context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error) {
	item, ok := f.assignments[projectID]
	if !ok {
		return repo.AgentProjectAssignment{}, repo.ErrNotFound
	}
	return item, nil
}

func stringPointer(value string) *string {
	v := value
	return &v
}
