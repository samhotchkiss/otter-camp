//go:build integration

package delivery

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

type blockingGitService struct {
	mergeStarted chan struct{}
	releaseMerge chan struct{}

	mu         sync.Mutex
	mergeCalls int
}

func (g *blockingGitService) Merge(_ context.Context, _ uuid.UUID, _, _ string) error {
	g.mu.Lock()
	g.mergeCalls++
	callIndex := g.mergeCalls
	g.mu.Unlock()

	if callIndex == 1 {
		close(g.mergeStarted)
		<-g.releaseMerge
	}
	return nil
}

func (g *blockingGitService) Push(context.Context, repo.ProjectRemote) error {
	return nil
}

func (g *blockingGitService) CommitExists(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

func (g *blockingGitService) MergeCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.mergeCalls
}

func TestMergeWorkerIntegrationAdvisoryLockReenqueue(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	_ = org
	_ = seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "continuous")

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "merge-source",
		WorkStatus:     "done",
		FlowTemplateID: project.DeployFlowTemplateID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"task_type":"work"}`),
		BranchName:     stringPtr("feature/one"),
	})
	if err != nil {
		t.Fatalf("create source task: %v", err)
	}

	entry, err := repo.NewMergeQueueEntryRepo(pool).Enqueue(ctx, repo.MergeQueueEntry{
		ProjectID:  project.ID,
		TaskID:     taskRecord.ID,
		BranchName: "feature/one",
		Status:     "queued",
	})
	if err != nil {
		t.Fatalf("enqueue merge entry: %v", err)
	}

	git := &blockingGitService{
		mergeStarted: make(chan struct{}),
		releaseMerge: make(chan struct{}),
	}
	worker, err := NewMergeWorker(MergeWorkerOptions{
		Pool: pool,
		Git:  git,
	})
	if err != nil {
		t.Fatalf("NewMergeWorker: %v", err)
	}

	payload, _ := json.Marshal(mergeExecutePayload{MergeQueueEntryID: entry.ID, ProjectID: project.ID})
	job := jobqueue.Job{Payload: payload}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- worker.Execute(ctx, job)
	}()

	select {
	case <-git.mergeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first merge call did not start")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- worker.Execute(ctx, job)
	}()

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second execute returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second merge execute timed out")
	}

	close(git.releaseMerge)
	if err := <-firstDone; err != nil {
		t.Fatalf("first execute returned error: %v", err)
	}

	if git.MergeCalls() != 1 {
		t.Fatalf("git merge calls = %d, want 1", git.MergeCalls())
	}

	stored, err := repo.NewMergeQueueEntryRepo(pool).GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("load merge entry: %v", err)
	}
	if stored.Status != "merged" {
		t.Fatalf("merge status = %q, want merged", stored.Status)
	}
	if stored.MergedAt == nil {
		t.Fatal("merged_at not set")
	}

	var mergeRetryJobs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM job_queue WHERE job_type = 'merge_execute'`).Scan(&mergeRetryJobs); err != nil {
		t.Fatalf("count merge_execute jobs: %v", err)
	}
	if mergeRetryJobs != 1 {
		t.Fatalf("merge_execute jobs = %d, want 1", mergeRetryJobs)
	}

	var pushJobs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM job_queue WHERE job_type = 'push_execute'`).Scan(&pushJobs); err != nil {
		t.Fatalf("count push_execute jobs: %v", err)
	}
	if pushJobs != 1 {
		t.Fatalf("push_execute jobs = %d, want 1", pushJobs)
	}
}

func TestMergeWorkerIntegrationWorkspaceGitServiceMergesWithoutRemote(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	writeWorkspaceGitFile(t, repoRoot, "content/posts/example.md", "base\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "base")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "-b", "task/21")
	writeWorkspaceGitFile(t, repoRoot, "content/posts/example.md", "merged from task\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "task output")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "main")

	environmentRepo := repo.NewProjectEnvironmentRepo(pool)
	if _, err := environmentRepo.Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: "gated",
		RepoPath:     workspaceGitStringPointer(repoRoot),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create workspace environment: %v", err)
	}

	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "merge-source",
		WorkStatus:     "done",
		FlowTemplateID: project.DeployFlowTemplateID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"task_type":"work"}`),
		BranchName:     stringPtr("task/21"),
	})
	if err != nil {
		t.Fatalf("create source task: %v", err)
	}

	entry, err := repo.NewMergeQueueEntryRepo(pool).Enqueue(ctx, repo.MergeQueueEntry{
		ProjectID:  project.ID,
		TaskID:     taskRecord.ID,
		BranchName: "task/21",
		Status:     "queued",
	})
	if err != nil {
		t.Fatalf("enqueue merge entry: %v", err)
	}

	gitService, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}
	worker, err := NewMergeWorker(MergeWorkerOptions{
		Pool: pool,
		Git:  gitService,
	})
	if err != nil {
		t.Fatalf("NewMergeWorker: %v", err)
	}

	payload, _ := json.Marshal(mergeExecutePayload{MergeQueueEntryID: entry.ID, ProjectID: project.ID})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored, err := repo.NewMergeQueueEntryRepo(pool).GetByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("load merge entry: %v", err)
	}
	if stored.Status != "merged" {
		t.Fatalf("merge status = %q, want merged", stored.Status)
	}
	if stored.MergedAt == nil {
		t.Fatal("merged_at not set")
	}

	got := readWorkspaceGitFile(t, repoRoot+"/content/posts/example.md")
	if got != "merged from task\n" {
		t.Fatalf("workspace file content = %q, want %q", got, "merged from task\n")
	}

	var pushJobs int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM job_queue WHERE job_type = 'push_execute'`).Scan(&pushJobs); err != nil {
		t.Fatalf("count push_execute jobs: %v", err)
	}
	if pushJobs != 0 {
		t.Fatalf("push_execute jobs = %d, want 0 without remote", pushJobs)
	}
}

func TestEnvUpdaterIntegrationDeployCompletionCascade(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org, project := seedDeliveryOrgProject(t, ctx, pool)
	env := seedDeliveryEnvironment(t, ctx, pool, project.ID, "production", "continuous")

	sourceTask, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "source-task",
		WorkStatus:     "done",
		FlowTemplateID: project.DeployFlowTemplateID,
		CreatedByType:  "system",
		Metadata:       json.RawMessage(`{"task_type":"work"}`),
	})
	if err != nil {
		t.Fatalf("create source task: %v", err)
	}

	mergeEntry, err := repo.NewMergeQueueEntryRepo(pool).Enqueue(ctx, repo.MergeQueueEntry{
		ProjectID:  project.ID,
		TaskID:     sourceTask.ID,
		BranchName: "feature/source",
		Status:     "merged",
	})
	if err != nil {
		t.Fatalf("enqueue merge entry: %v", err)
	}

	metadata, err := json.Marshal(map[string]any{
		"task_type":  "deploy",
		"commit_sha": "sha-deploy-1",
		"delivery": map[string]any{
			"triggered_by_task_id": sourceTask.ID.String(),
			"merge_queue_entry_id": mergeEntry.ID.String(),
		},
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	deployTask, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "deploy",
		WorkStatus:     "done",
		FlowTemplateID: project.DeployFlowTemplateID,
		CreatedByType:  "system",
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("create deploy task: %v", err)
	}

	updater, err := NewEnvUpdater(EnvUpdaterOptions{Pool: pool})
	if err != nil {
		t.Fatalf("NewEnvUpdater: %v", err)
	}
	if err := updater.OnDeployCompleted(ctx, deployTask.ID); err != nil {
		t.Fatalf("OnDeployCompleted: %v", err)
	}

	updatedEnv, err := repo.NewProjectEnvironmentRepo(pool).GetByID(ctx, env.ID)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	if updatedEnv.LastDeployedCommit == nil || *updatedEnv.LastDeployedCommit != "sha-deploy-1" {
		t.Fatalf("last_deployed_commit = %v, want sha-deploy-1", updatedEnv.LastDeployedCommit)
	}
	if updatedEnv.LastDeployedAt == nil {
		t.Fatal("last_deployed_at not set")
	}
	if updatedEnv.DeployTaskID == nil || *updatedEnv.DeployTaskID != deployTask.ID {
		t.Fatalf("deploy_task_id = %v, want %s", updatedEnv.DeployTaskID, deployTask.ID)
	}

	updatedEntry, err := repo.NewMergeQueueEntryRepo(pool).GetByTask(ctx, sourceTask.ID)
	if err != nil {
		t.Fatalf("get merge entry: %v", err)
	}
	if updatedEntry.ArchivedAt == nil {
		t.Fatal("merge_queue_entry.archived_at not set")
	}
}

func stringPtr(value string) *string {
	copy := value
	return &copy
}
