package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/syncmetrics"
	"github.com/stretchr/testify/require"
)

func TestGitHubSyncJobStore_EnqueuePickupRetryDeadLetterAndReplay(t *testing.T) {
	syncmetrics.ResetForTests()

	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-sync-jobs-org")
	projectID := createTestProject(t, db, orgID, "github-sync-project")

	store := NewGitHubSyncJobStore(db)
	ctx := ctxWithWorkspace(orgID)

	enqueued, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"repo":"samhotchkiss/otter-camp","cursor":"page=1"}`),
		MaxAttempts: 2,
	})
	require.NoError(t, err)
	require.Equal(t, GitHubSyncJobStatusQueued, enqueued.Status)

	firstPickup, err := store.PickupNext(ctx, GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, firstPickup)
	require.Equal(t, enqueued.ID, firstPickup.ID)
	require.Equal(t, 1, firstPickup.AttemptCount)
	require.Equal(t, GitHubSyncJobStatusInProgress, firstPickup.Status)

	now := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	nextAttempt := now.Add(5 * time.Minute)

	firstFailure, err := store.RecordFailure(ctx, firstPickup.ID, RecordGitHubSyncFailureInput{
		ErrorClass:   "timeout",
		ErrorMessage: "timeout while importing issues",
		Retryable:    true,
		NextAttempt:  &nextAttempt,
		OccurredAt:   now,
	})
	require.NoError(t, err)
	require.NotNil(t, firstFailure.Job)
	require.Nil(t, firstFailure.DeadLetter)
	require.Equal(t, GitHubSyncJobStatusRetrying, firstFailure.Job.Status)

	secondPickup, err := store.PickupNext(ctx, GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, secondPickup)
	require.Equal(t, 2, secondPickup.AttemptCount)
	require.Equal(t, GitHubSyncJobStatusInProgress, secondPickup.Status)

	secondFailure, err := store.RecordFailure(ctx, secondPickup.ID, RecordGitHubSyncFailureInput{
		ErrorClass:   "timeout",
		ErrorMessage: "timeout while importing issues",
		Retryable:    false,
		OccurredAt:   now.Add(1 * time.Minute),
	})
	require.NoError(t, err)
	require.NotNil(t, secondFailure.Job)
	require.NotNil(t, secondFailure.DeadLetter)
	require.Equal(t, GitHubSyncJobStatusDeadLetter, secondFailure.Job.Status)
	require.Equal(t, secondFailure.Job.ID, secondFailure.DeadLetter.JobID)
	require.Equal(t, 2, secondFailure.DeadLetter.AttemptCount)
	require.Equal(t, "timeout while importing issues", derefString(t, secondFailure.DeadLetter.LastError))

	var attempts []map[string]any
	require.NoError(t, json.Unmarshal(secondFailure.DeadLetter.AttemptHistory, &attempts))
	require.Len(t, attempts, 2)

	deadLetters, err := store.ListDeadLetters(ctx, 10)
	require.NoError(t, err)
	require.Len(t, deadLetters, 1)
	require.Equal(t, secondFailure.DeadLetter.ID, deadLetters[0].ID)
	require.Nil(t, deadLetters[0].ReplayedAt)

	replayedBy := "tester@example.com"
	replayedJob, err := store.ReplayDeadLetter(ctx, secondFailure.DeadLetter.ID, &replayedBy)
	require.NoError(t, err)
	require.Equal(t, GitHubSyncJobStatusQueued, replayedJob.Status)
	require.Equal(t, 0, replayedJob.AttemptCount)
	require.JSONEq(t, `[]`, string(replayedJob.AttemptHistory))
	require.Nil(t, replayedJob.LastError)
	require.Nil(t, replayedJob.LastErrorClass)

	replayedAgain, err := store.ReplayDeadLetter(ctx, secondFailure.DeadLetter.ID, &replayedBy)
	require.NoError(t, err)
	require.Equal(t, replayedJob.ID, replayedAgain.ID)
	require.Equal(t, GitHubSyncJobStatusQueued, replayedAgain.Status)

	deadLetters, err = store.ListDeadLetters(ctx, 10)
	require.NoError(t, err)
	require.Len(t, deadLetters, 1)
	require.NotNil(t, deadLetters[0].ReplayedAt)
	require.Equal(t, replayedBy, derefString(t, deadLetters[0].ReplayedBy))

	snapshot := syncmetrics.SnapshotNow()
	metrics := snapshot.Jobs[GitHubSyncJobTypeIssueImport]
	require.Equal(t, int64(2), metrics.PickedTotal)
	require.Equal(t, int64(2), metrics.FailureTotal)
	require.Equal(t, int64(1), metrics.RetryTotal)
	require.Equal(t, int64(1), metrics.DeadLetterTotal)
	require.Equal(t, int64(1), metrics.ReplayTotal)
}

func TestGitHubSyncJobStore_QueueDepthAndStuckJobSignal(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-sync-depth-org")
	projectID := createTestProject(t, db, orgID, "github-sync-depth-project")

	store := NewGitHubSyncJobStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     GitHubSyncJobTypeRepoSync,
		Payload:     json.RawMessage(`{"branch":"main"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	job, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"cursor":"page=1"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	picked, err := store.PickupNext(ctx, GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, picked)

	_, err = db.Exec(
		`UPDATE github_sync_jobs SET updated_at = NOW() - interval '1 hour' WHERE id = $1`,
		picked.ID,
	)
	require.NoError(t, err)

	depth, err := store.QueueDepth(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, depth)

	depthByType := map[string]GitHubSyncQueueDepth{}
	for _, row := range depth {
		depthByType[row.JobType] = row
	}

	require.Equal(t, 1, depthByType[GitHubSyncJobTypeRepoSync].Queued)
	require.Equal(t, 1, depthByType[GitHubSyncJobTypeIssueImport].InProgress)
	require.Equal(t, 0, depthByType[GitHubSyncJobTypeIssueImport].DeadLetter)

	stuckJobs, err := store.CountStuckJobs(ctx, 15*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, stuckJobs)

	require.Equal(t, job.ID, picked.ID)
}

func TestGitHubSyncJobStore_PickupSkipsConcurrentJobsForSameProject(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-sync-locking-org")
	projectA := createTestProject(t, db, orgID, "github-sync-locking-project-a")
	projectB := createTestProject(t, db, orgID, "github-sync-locking-project-b")

	store := NewGitHubSyncJobStore(db)
	ctx := ctxWithWorkspace(orgID)

	jobA1, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectA,
		JobType:     GitHubSyncJobTypeRepoSync,
		Payload:     json.RawMessage(`{"branch":"main","seq":1}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	jobA2, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectA,
		JobType:     GitHubSyncJobTypeRepoSync,
		Payload:     json.RawMessage(`{"branch":"main","seq":2}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	jobB, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectB,
		JobType:     GitHubSyncJobTypeRepoSync,
		Payload:     json.RawMessage(`{"branch":"main","seq":3}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	firstPickup, err := store.PickupNext(ctx, GitHubSyncJobTypeRepoSync)
	require.NoError(t, err)
	require.NotNil(t, firstPickup)
	require.Equal(t, jobA1.ID, firstPickup.ID)
	require.Equal(t, GitHubSyncJobStatusInProgress, firstPickup.Status)

	// While project A has an in-progress job, the next pickup should skip A's queued job.
	secondPickup, err := store.PickupNext(ctx, GitHubSyncJobTypeRepoSync)
	require.NoError(t, err)
	require.NotNil(t, secondPickup)
	require.Equal(t, jobB.ID, secondPickup.ID)
	require.Equal(t, GitHubSyncJobStatusInProgress, secondPickup.Status)

	// Mark both in-progress jobs complete, then project A's queued follow-up can run.
	_, err = store.MarkCompleted(ctx, firstPickup.ID)
	require.NoError(t, err)
	_, err = store.MarkCompleted(ctx, secondPickup.ID)
	require.NoError(t, err)

	thirdPickup, err := store.PickupNext(ctx, GitHubSyncJobTypeRepoSync)
	require.NoError(t, err)
	require.NotNil(t, thirdPickup)
	require.Equal(t, jobA2.ID, thirdPickup.ID)
	require.Equal(t, GitHubSyncJobStatusInProgress, thirdPickup.Status)
}

func TestGitHubSyncJobStore_GetLatestByProjectAndType(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-sync-latest-org")
	projectID := createTestProject(t, db, orgID, "github-sync-latest-project")

	store := NewGitHubSyncJobStore(db)
	ctx := ctxWithWorkspace(orgID)

	first, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"cursor":"page=1"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)

	second, err := store.Enqueue(ctx, EnqueueGitHubSyncJobInput{
		ProjectID:   &projectID,
		JobType:     GitHubSyncJobTypeIssueImport,
		Payload:     json.RawMessage(`{"cursor":"page=2"}`),
		MaxAttempts: 3,
	})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	latest, err := store.GetLatestByProjectAndType(ctx, projectID, GitHubSyncJobTypeIssueImport)
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, second.ID, latest.ID)
}

func derefString(t *testing.T, value *string) string {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
