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

func TestEnvUpdaterOnDeployCompletedUpdatesEnvironmentAndArchivesEntries(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	deployTaskID := uuid.New()
	mergeEntryID := uuid.New()

	metadata, err := json.Marshal(map[string]any{
		"task_type":  deliveryTaskTypeDeploy,
		"commit_sha": "sha-deploy-1",
		"delivery": map[string]any{
			"merge_queue_entry_id": mergeEntryID.String(),
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	fakeNow := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)
	state := &fakeDeployCompletionState{mergeArchived: map[uuid.UUID]bool{}}
	updater := &EnvUpdater{
		clock: clock.NewFake(fakeNow),
		tasks: &fakeEnvUpdaterTaskRepo{task: repo.ProjectTask{
			ID:             deployTaskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
			Metadata:       metadata,
		}},
		executions:      &fakeEnvUpdaterExecutionRepo{},
		applyCompletion: state.apply,
	}

	if err := updater.OnDeployCompleted(ctx, deployTaskID); err != nil {
		t.Fatalf("OnDeployCompleted: %v", err)
	}

	if state.projectID != projectID {
		t.Fatalf("updated project_id = %s, want %s", state.projectID, projectID)
	}
	if state.commitSHA != "sha-deploy-1" {
		t.Fatalf("updated commit = %q, want sha-deploy-1", state.commitSHA)
	}
	if state.deployTaskID != deployTaskID {
		t.Fatalf("updated deploy_task_id = %s, want %s", state.deployTaskID, deployTaskID)
	}
	if !state.completedAt.Equal(fakeNow) {
		t.Fatalf("completed_at = %s, want %s", state.completedAt, fakeNow)
	}
	if !state.mergeArchived[mergeEntryID] {
		t.Fatalf("merge entry %s was not archived", mergeEntryID)
	}
}

type fakeEnvUpdaterTaskRepo struct {
	task repo.ProjectTask
}

func (f *fakeEnvUpdaterTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	return f.task, nil
}

type fakeEnvUpdaterExecutionRepo struct{}

func (*fakeEnvUpdaterExecutionRepo) ListByTask(context.Context, uuid.UUID) ([]repo.FlowNodeExecution, error) {
	return nil, nil
}

type fakeDeployCompletionState struct {
	projectID     uuid.UUID
	deployTaskID  uuid.UUID
	commitSHA     string
	completedAt   time.Time
	mergeArchived map[uuid.UUID]bool
}

func (f *fakeDeployCompletionState) apply(_ context.Context, update deployCompletionUpdate) error {
	f.projectID = update.ProjectID
	f.deployTaskID = update.DeployTaskID
	f.commitSHA = update.CommitSHA
	f.completedAt = update.CompletedAt
	for _, id := range update.MergeEntryIDs {
		f.mergeArchived[id] = true
	}
	return nil
}
