package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func setupSchedulerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("OTTER_TEST_DATABASE_URL")
	if connStr == "" {
		t.Skip("set OTTER_TEST_DATABASE_URL to run scheduler integration tests")
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	m, err := migrate.New("file://"+migrationsDir, connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
		_ = db.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	return db
}

func schedulerCtxWithWorkspace(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}

func schedulerCreateOrg(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		"Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func schedulerCreateAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, $2, $3, 'active')
		 RETURNING id`,
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestJobSchedulerWorkerStopsOnContextCancellation(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-stop")
	ctx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval: 10 * time.Millisecond,
		MaxPerPoll:   5,
		RunTimeout:   1 * time.Minute,
	})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		worker.Start(runCtx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}
}

func TestJobSchedulerWorkerRunOnceExecutesOnceJobAndCompletes(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-once")
	agentID := schedulerCreateAgent(t, db, orgID, "scheduler-once-agent")
	ctx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 20, 0, 0, 0, time.UTC)
	runAt := now.Add(-1 * time.Minute)
	nextRunAt := now.Add(-10 * time.Second)
	job, err := jobStore.Create(ctx, store.CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "One Shot Reminder",
		ScheduleKind: store.AgentJobScheduleOnce,
		RunAt:        &runAt,
		Timezone:     strPtr("UTC"),
		PayloadKind:  store.AgentJobPayloadMessage,
		PayloadText:  "remind me",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)

	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval:  10 * time.Millisecond,
		MaxPerPoll:    5,
		RunTimeout:    1 * time.Minute,
		MaxRunHistory: 100,
	})
	worker.Now = func() time.Time { return now }

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	updatedJob, err := jobStore.GetByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, store.AgentJobStatusCompleted, updatedJob.Status)
	require.NotNil(t, updatedJob.RoomID)
	require.NotNil(t, updatedJob.LastRunStatus)
	require.Equal(t, store.AgentJobRunStatusSuccess, *updatedJob.LastRunStatus)

	runs, err := jobStore.ListRuns(ctx, job.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, store.AgentJobRunStatusSuccess, runs[0].Status)
	require.NotNil(t, runs[0].MessageID)

	conn, err := store.WithWorkspace(ctx, db)
	require.NoError(t, err)
	defer conn.Close()

	var messageCount int
	err = conn.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM chat_messages WHERE room_id = $1`,
		*updatedJob.RoomID,
	).Scan(&messageCount)
	require.NoError(t, err)
	require.Equal(t, 1, messageCount)
}

func TestJobSchedulerWorkerAutoPausesAfterConsecutiveFailures(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-autopause")
	agentID := schedulerCreateAgent(t, db, orgID, "scheduler-autopause-agent")
	ctx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 20, 30, 0, 0, time.UTC)
	nextRunAt := now.Add(-10 * time.Second)
	maxFailures := 2
	job, err := jobStore.Create(ctx, store.CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Failing Job",
		ScheduleKind: store.AgentJobScheduleInterval,
		IntervalMS:   int64Ptr(60000),
		Timezone:     strPtr("Not/ARealTimezone"),
		PayloadKind:  store.AgentJobPayloadMessage,
		PayloadText:  "will fail before success completion",
		NextRunAt:    &nextRunAt,
		MaxFailures:  &maxFailures,
	})
	require.NoError(t, err)

	current := now
	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval:  10 * time.Millisecond,
		MaxPerPoll:    5,
		RunTimeout:    1 * time.Minute,
		MaxRunHistory: 100,
	})
	worker.Now = func() time.Time { return current }

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	jobAfterFirstRun, err := jobStore.GetByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, store.AgentJobStatusActive, jobAfterFirstRun.Status)
	require.Equal(t, 1, jobAfterFirstRun.ConsecutiveFailures)

	current = current.Add(2 * time.Minute)
	processed, err = worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	jobAfterSecondRun, err := jobStore.GetByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, store.AgentJobStatusPaused, jobAfterSecondRun.Status)
	require.Equal(t, 2, jobAfterSecondRun.ConsecutiveFailures)
	require.NotNil(t, jobAfterSecondRun.LastRunStatus)
	require.Equal(t, store.AgentJobRunStatusError, *jobAfterSecondRun.LastRunStatus)
}

func TestJobSchedulerWorkerCleanupStaleRuns(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-stale")
	agentID := schedulerCreateAgent(t, db, orgID, "scheduler-stale-agent")
	ctx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 21, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(10 * time.Minute)
	job, err := jobStore.Create(ctx, store.CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Stale Run Job",
		ScheduleKind: store.AgentJobScheduleInterval,
		IntervalMS:   int64Ptr(60000),
		Timezone:     strPtr("UTC"),
		PayloadKind:  store.AgentJobPayloadMessage,
		PayloadText:  "cleanup stale runs",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)

	_, err = jobStore.StartRun(ctx, store.StartAgentJobRunInput{
		JobID:       job.ID,
		PayloadText: "stale",
		StartedAt:   now.Add(-10 * time.Minute),
	})
	require.NoError(t, err)

	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval:  10 * time.Millisecond,
		MaxPerPoll:    5,
		RunTimeout:    2 * time.Minute,
		MaxRunHistory: 100,
	})
	worker.Now = func() time.Time { return now }

	processed, err := worker.RunOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, processed)

	runs, err := jobStore.ListRuns(ctx, job.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, store.AgentJobRunStatusTimeout, runs[0].Status)
}

func TestJobSchedulerWorkerPrunesRunHistory(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-prune")
	agentID := schedulerCreateAgent(t, db, orgID, "scheduler-prune-agent")
	ctx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	start := time.Date(2026, 2, 12, 21, 30, 0, 0, time.UTC)
	nextRunAt := start.Add(-10 * time.Second)
	job, err := jobStore.Create(ctx, store.CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Pruned Runs Job",
		ScheduleKind: store.AgentJobScheduleInterval,
		IntervalMS:   int64Ptr(60000),
		Timezone:     strPtr("UTC"),
		PayloadKind:  store.AgentJobPayloadMessage,
		PayloadText:  "prune history",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)

	current := start
	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval:  10 * time.Millisecond,
		MaxPerPoll:    5,
		RunTimeout:    1 * time.Minute,
		MaxRunHistory: 2,
	})
	worker.Now = func() time.Time { return current }

	for i := 0; i < 3; i++ {
		processed, runErr := worker.RunOnce(ctx)
		require.NoError(t, runErr)
		require.Equal(t, 1, processed)
		current = current.Add(2 * time.Minute)
	}

	runs, err := jobStore.ListRuns(ctx, job.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
}

func TestJobSchedulerWorkerRunOnceInjectsConfiguredWorkspaceWithoutHTTPContext(t *testing.T) {
	db := setupSchedulerTestDB(t)
	orgID := schedulerCreateOrg(t, db, "scheduler-configured-workspace")
	agentID := schedulerCreateAgent(t, db, orgID, "scheduler-configured-workspace-agent")
	scopedCtx := schedulerCtxWithWorkspace(orgID)
	jobStore := store.NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 22, 0, 0, 0, time.UTC)
	runAt := now.Add(-1 * time.Minute)
	nextRunAt := now.Add(-10 * time.Second)
	job, err := jobStore.Create(scopedCtx, store.CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Configured Workspace Job",
		ScheduleKind: store.AgentJobScheduleOnce,
		RunAt:        &runAt,
		Timezone:     strPtr("UTC"),
		PayloadKind:  store.AgentJobPayloadMessage,
		PayloadText:  "execute from background context",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)

	worker := NewAgentJobWorker(jobStore, AgentJobWorkerConfig{
		PollInterval:  10 * time.Millisecond,
		MaxPerPoll:    5,
		RunTimeout:    1 * time.Minute,
		MaxRunHistory: 100,
		WorkspaceID:   orgID,
	})
	worker.Now = func() time.Time { return now }

	processed, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processed)

	updatedJob, err := jobStore.GetByID(scopedCtx, job.ID)
	require.NoError(t, err)
	require.Equal(t, store.AgentJobStatusCompleted, updatedJob.Status)
}

func TestAgentJobWorkerWorkspaceContext(t *testing.T) {
	t.Parallel()

	t.Run("preserves existing workspace from context", func(t *testing.T) {
		t.Parallel()

		worker := NewAgentJobWorker(nil, AgentJobWorkerConfig{
			WorkspaceID: "11111111-2222-3333-4444-555555555555",
		})
		input := schedulerCtxWithWorkspace("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")

		ctx := worker.workspaceContext(input)
		require.Equal(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", middleware.WorkspaceFromContext(ctx))
	})

	t.Run("injects configured workspace when missing", func(t *testing.T) {
		t.Parallel()

		worker := NewAgentJobWorker(nil, AgentJobWorkerConfig{
			WorkspaceID: "11111111-2222-3333-4444-555555555555",
		})

		ctx := worker.workspaceContext(context.Background())
		require.Equal(t, "11111111-2222-3333-4444-555555555555", middleware.WorkspaceFromContext(ctx))
	})
}
