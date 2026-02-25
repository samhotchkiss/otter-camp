package delivery

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
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestMergeWorkerAdvisoryLockAcquiredCallsMerge(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	entryID := uuid.New()

	git := &mergeWorkerGitStub{}
	enqueuer := &mergeWorkerEnqueuer{}
	worker := &MergeWorker{
		git:      git,
		enqueuer: enqueuer,
		clock:    clock.NewFake(time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	worker.lockProject = func(context.Context, uuid.UUID) (pgx.Tx, bool, error) {
		return nil, true, nil
	}
	worker.executeLocked = func(ctx context.Context, _ pgx.Tx, payload mergeExecutePayload) error {
		return worker.git.Merge(ctx, payload.ProjectID, "feature/one", "main")
	}

	payload, _ := json.Marshal(mergeExecutePayload{MergeQueueEntryID: entryID, ProjectID: projectID})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if git.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", git.mergeCalls)
	}
	if len(enqueuer.jobs) != 0 {
		t.Fatalf("reenqueue jobs = %d, want 0", len(enqueuer.jobs))
	}
}

func TestMergeWorkerAdvisoryLockNotAcquiredReenqueues(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	entryID := uuid.New()
	base := time.Date(2026, 2, 25, 12, 0, 0, 0, time.UTC)

	git := &mergeWorkerGitStub{}
	enqueuer := &mergeWorkerEnqueuer{}
	worker := &MergeWorker{
		git:      git,
		enqueuer: enqueuer,
		clock:    clock.NewFake(base),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	worker.lockProject = func(context.Context, uuid.UUID) (pgx.Tx, bool, error) {
		return nil, false, nil
	}
	worker.executeLocked = func(context.Context, pgx.Tx, mergeExecutePayload) error {
		t.Fatal("executeLocked should not be called when advisory lock is unavailable")
		return nil
	}

	payload, _ := json.Marshal(mergeExecutePayload{MergeQueueEntryID: entryID, ProjectID: projectID})
	if err := worker.Execute(ctx, jobqueue.Job{Payload: payload}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if git.mergeCalls != 0 {
		t.Fatalf("merge calls = %d, want 0", git.mergeCalls)
	}
	if len(enqueuer.jobs) != 1 {
		t.Fatalf("reenqueue jobs = %d, want 1", len(enqueuer.jobs))
	}
	retry := enqueuer.jobs[0]
	if retry.jobType != MergeExecuteJobType {
		t.Fatalf("job type = %q, want %q", retry.jobType, MergeExecuteJobType)
	}
	if retry.runAfter == nil {
		t.Fatal("expected run_after for re-enqueue")
	}
	wantRunAfter := base.Add(mergeRetryDelay)
	if !retry.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", retry.runAfter, wantRunAfter)
	}
}

type mergeWorkerGitStub struct {
	mergeCalls int
}

func (f *mergeWorkerGitStub) Merge(context.Context, uuid.UUID, string, string) error {
	f.mergeCalls++
	return nil
}

func (*mergeWorkerGitStub) Push(context.Context, repo.ProjectRemote) error {
	return nil
}

func (*mergeWorkerGitStub) CommitExists(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}

type mergeWorkerEnqueuer struct {
	jobs []mergeWorkerQueuedJob
}

type mergeWorkerQueuedJob struct {
	jobType  string
	payload  any
	runAfter *time.Time
}

func (f *mergeWorkerEnqueuer) Enqueue(_ context.Context, _ pgx.Tx, jobType string, _ int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	entry := mergeWorkerQueuedJob{jobType: jobType, payload: payload}
	if runAfter != nil {
		at := *runAfter
		entry.runAfter = &at
	}
	f.jobs = append(f.jobs, entry)
	return uuid.New(), nil
}
