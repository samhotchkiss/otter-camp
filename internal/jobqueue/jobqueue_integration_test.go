//go:build integration

package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestJobQueue_Priority_Ordering(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "prio-worker",
		BatchSize:            10,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	idHigh := testdb.EnqueueJob(t, pool, "priority", 10, map[string]any{"name": "high"})
	idMid := testdb.EnqueueJob(t, pool, "priority", 5, map[string]any{"name": "mid"})
	idLow1 := testdb.EnqueueJob(t, pool, "priority", 1, map[string]any{"name": "low1"})
	idLow2 := testdb.EnqueueJob(t, pool, "priority", 1, map[string]any{"name": "low2"})

	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending: %v", err)
	}
	if len(jobs) != 4 {
		t.Fatalf("claimed jobs = %d, want 4", len(jobs))
	}

	got := []uuid.UUID{jobs[0].ID, jobs[1].ID, jobs[2].ID, jobs[3].ID}
	want := []uuid.UUID{idHigh, idMid, idLow1, idLow2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order mismatch at pos %d: got %v want %v", i, got, want)
		}
	}
}

func TestJobQueue_SKIP_LOCKED_Concurrency(t *testing.T) {
	pool := testdb.New(t)
	workerA := New(pool, nil, Config{
		WorkerID:             "skiplocked-a",
		PollInterval:         50 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	workerB := New(pool, nil, Config{
		WorkerID:             "skiplocked-b",
		PollInterval:         50 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var (
		mu     sync.Mutex
		counts = map[uuid.UUID]int{}
	)
	handler := func(_ context.Context, job Job) error {
		mu.Lock()
		counts[job.ID]++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	workerA.Register("skiplocked.job", handler)
	workerB.Register("skiplocked.job", handler)

	for i := 0; i < 10; i++ {
		testdb.EnqueueJob(t, pool, "skiplocked.job", 5, map[string]any{"n": i})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = workerA.Start(ctx) }()
	go func() { _ = workerB.Start(ctx) }()
	defer func() {
		_ = workerA.Stop()
		_ = workerB.Stop()
	}()

	waitForStatusCount079(t, pool, "done", 10, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 10 {
		t.Fatalf("unique processed jobs = %d, want 10", len(counts))
	}
	for jobID, n := range counts {
		if n != 1 {
			t.Fatalf("job %s processed %d times, want 1", jobID, n)
		}
	}
}

func TestJobQueue_Retry_OnTransientFailure(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "retry-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.Register("retry.transient", func(context.Context, Job) error {
		return errors.New("transient failure")
	})

	jobID := testdb.EnqueueJob(t, pool, "retry.transient", 5, map[string]any{"k": "v"})
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET max_attempts = 2
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("set max_attempts: %v", err)
	}

	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim first attempt: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("first claim count = %d, want 1", len(jobs))
	}
	if err := worker.executeClaimedJob(context.Background(), jobs[0]); err != nil {
		t.Fatalf("execute first attempt: %v", err)
	}

	var status string
	var attempts int
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempts
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query after first attempt: %v", err)
	}
	if status != "pending" || attempts != 1 {
		t.Fatalf("after first failure got status=%s attempts=%d, want pending/1", status, attempts)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET run_after = now() - interval '1 second'
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("make job claimable again: %v", err)
	}

	jobs, err = worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim second attempt: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("second claim count = %d, want 1", len(jobs))
	}
	if err := worker.executeClaimedJob(context.Background(), jobs[0]); err != nil {
		t.Fatalf("execute second attempt: %v", err)
	}

	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempts
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query after second attempt: %v", err)
	}
	if status != "dead_letter" || attempts != 2 {
		t.Fatalf("after retries got status=%s attempts=%d, want dead_letter/2", status, attempts)
	}
}

func TestJobQueue_DeadLetter_Promotion(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "deadletter-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.Register("deadletter.job", func(context.Context, Job) error {
		return errors.New("permanent failure")
	})

	jobID := testdb.EnqueueJob(t, pool, "deadletter.job", 5, map[string]any{"n": 1})
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET max_attempts = 1
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("set max_attempts=1: %v", err)
	}

	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(jobs))
	}
	if err := worker.executeClaimedJob(context.Background(), jobs[0]); err != nil {
		t.Fatalf("execute claimed deadletter job: %v", err)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query deadletter status: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status = %s, want dead_letter", status)
	}

	again, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending after dead letter: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("dead-lettered job should not be re-enqueued, claimed=%d", len(again))
	}
}

func TestJobQueue_StaleClaim_Recovery(t *testing.T) {
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC))
	worker := New(pool, nil, Config{
		WorkerID:             "stale-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  5 * time.Minute,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})

	jobID := testdb.EnqueueJob(t, pool, "stale.job", 5, map[string]any{"kind": "stale"})
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'crashed-worker',
		    claimed_at = $2,
		    attempts = 1
		WHERE id = $1
	`, jobID, fakeClock.Now().UTC()); err != nil {
		t.Fatalf("set stale claim state: %v", err)
	}

	fakeClock.Advance(6 * time.Minute)
	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered rows = %d, want 1", recovered)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy, &claimedAt); err != nil {
		t.Fatalf("query recovered job: %v", err)
	}
	if status != "pending" || claimedBy != nil || claimedAt != nil {
		t.Fatalf("recovered job state invalid: status=%s claimed_by=%v claimed_at=%v", status, claimedBy, claimedAt)
	}
}

func TestJobQueue_BootstrapStaleClaim_RecoveryUsesBootstrapThreshold(t *testing.T) {
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC))
	worker := New(pool, nil, Config{
		WorkerID:             "bootstrap-stale-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  5 * time.Minute,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})

	org := createJobQueueOrg079(t, pool, "bootstrap-stale")
	sessionID := uuid.New()
	projectID := uuid.New()
	messageID := uuid.New()
	creatorID := uuid.New()

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO chat_session (
			id, organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, $2, 'project', $3, 'async', 'active', 'system', $4, $5::jsonb)
	`, sessionID, org.ID, projectID, creatorID, `{"project_bootstrap":{"status":"active"}}`); err != nil {
		t.Fatalf("insert bootstrap session: %v", err)
	}

	jobID := testdb.EnqueueJob(t, pool, agentTurnJobType, 5, map[string]any{
		"session_id":  sessionID,
		"message_id":  messageID,
		"retry_count": 0,
	})
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'crashed-worker',
		    claimed_at = $2,
		    attempts = 1
		WHERE id = $1
	`, jobID, fakeClock.Now().UTC()); err != nil {
		t.Fatalf("set bootstrap stale claim state: %v", err)
	}

	fakeClock.Advance(projectBootstrapStaleThreshold + 30*time.Second)
	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered rows = %d, want 1", recovered)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status); err != nil {
		t.Fatalf("query recovered bootstrap job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("bootstrap recovered job status = %s, want pending", status)
	}
}

func TestJobQueue_BootstrapStaleClaim_RecoverySkipsNonBootstrapSessions(t *testing.T) {
	pool := testdb.New(t)
	fakeClock := clock.NewFake(time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC))
	worker := New(pool, nil, Config{
		WorkerID:             "bootstrap-skip-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  5 * time.Minute,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})

	org := createJobQueueOrg079(t, pool, "bootstrap-skip")
	sessionID := uuid.New()
	projectID := uuid.New()
	messageID := uuid.New()
	creatorID := uuid.New()

	if _, err := pool.Exec(context.Background(), `
		INSERT INTO chat_session (
			id, organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, $2, 'project', $3, 'async', 'active', 'system', $4, '{}'::jsonb)
	`, sessionID, org.ID, projectID, creatorID); err != nil {
		t.Fatalf("insert non-bootstrap session: %v", err)
	}

	jobID := testdb.EnqueueJob(t, pool, agentTurnJobType, 5, map[string]any{
		"session_id":  sessionID,
		"message_id":  messageID,
		"retry_count": 0,
	})
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'crashed-worker',
		    claimed_at = $2,
		    attempts = 1
		WHERE id = $1
	`, jobID, fakeClock.Now().UTC()); err != nil {
		t.Fatalf("set non-bootstrap stale claim state: %v", err)
	}

	fakeClock.Advance(projectBootstrapStaleThreshold + 30*time.Second)
	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered rows = %d, want 0 before generic threshold", recovered)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status); err != nil {
		t.Fatalf("query non-bootstrap job: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("non-bootstrap job status = %s, want claimed", status)
	}
}

func TestJobQueue_CloseSupersededCanonicalAsyncSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "task-session-cleanup-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	org := createJobQueueOrg079(t, pool, "task-session-cleanup")
	projectRecord, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "task-session-cleanup-" + uuid.NewString()[:8],
		DisplayName:    "Task Session Cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	description := "Verify worker startup cleanup closes older async task sessions."
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		TaskNumber:     1,
		Title:          "Cleanup duplicate task sessions",
		Description:    &description,
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    nil,
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}

	var (
		olderTaskID uuid.UUID
		newerTaskID uuid.UUID
		olderProjID uuid.UUID
		newerProjID uuid.UUID
		olderOrgID  uuid.UUID
		newerOrgID  uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata, created_at, updated_at
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'system', $3, $4::jsonb, now() - interval '2 minutes', now() - interval '2 minutes')
		RETURNING id
	`, org.ID, taskRecord.ID, uuid.Nil, `{"flow_node_execution_id":"older-execution"}`).Scan(&olderTaskID); err != nil {
		t.Fatalf("insert older task session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'system', $3, $4::jsonb)
		RETURNING id
	`, org.ID, taskRecord.ID, uuid.Nil, `{"flow_node_execution_id":"newer-execution"}`).Scan(&newerTaskID); err != nil {
		t.Fatalf("insert newer task session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata, created_at, updated_at
		)
		VALUES ($1, 'project', $2, 'async', 'active', 'system', $3, $4::jsonb, now() - interval '2 minutes', now() - interval '2 minutes')
		RETURNING id
	`, org.ID, projectRecord.ID, uuid.Nil, `{"source":"older-project-session"}`).Scan(&olderProjID); err != nil {
		t.Fatalf("insert older project session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'project', $2, 'async', 'active', 'system', $3, $4::jsonb)
		RETURNING id
	`, org.ID, projectRecord.ID, uuid.Nil, `{"source":"newer-project-session"}`).Scan(&newerProjID); err != nil {
		t.Fatalf("insert newer project session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata, created_at, updated_at
		)
		VALUES ($1, 'organization', $1, 'async', 'active', 'system', $2, $3::jsonb, now() - interval '2 minutes', now() - interval '2 minutes')
		RETURNING id
	`, org.ID, uuid.Nil, `{"source":"older-org-session"}`).Scan(&olderOrgID); err != nil {
		t.Fatalf("insert older org session: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'organization', $1, 'async', 'active', 'system', $2, $3::jsonb)
		RETURNING id
	`, org.ID, uuid.Nil, `{"source":"newer-org-session"}`).Scan(&newerOrgID); err != nil {
		t.Fatalf("insert newer org session: %v", err)
	}

	repaired, err := worker.CloseSupersededCanonicalAsyncSessions(ctx)
	if err != nil {
		t.Fatalf("CloseSupersededCanonicalAsyncSessions: %v", err)
	}
	if repaired != 3 {
		t.Fatalf("repaired rows = %d, want 3", repaired)
	}

	assertClosed := func(sessionID uuid.UUID, label string) {
		t.Helper()
		var status string
		var closedAt *time.Time
		if err := pool.QueryRow(ctx, `
			SELECT status, closed_at
			FROM chat_session
			WHERE id = $1
		`, sessionID).Scan(&status, &closedAt); err != nil {
			t.Fatalf("query %s session: %v", label, err)
		}
		if status != "closed" || closedAt == nil {
			t.Fatalf("%s session = status:%s closed_at:%v, want closed with closed_at", label, status, closedAt)
		}
	}

	assertActive := func(sessionID uuid.UUID, label string) {
		t.Helper()
		var status string
		if err := pool.QueryRow(ctx, `
			SELECT status
			FROM chat_session
			WHERE id = $1
		`, sessionID).Scan(&status); err != nil {
			t.Fatalf("query %s session: %v", label, err)
		}
		if status != "active" {
			t.Fatalf("%s session status = %s, want active", label, status)
		}
	}

	assertClosed(olderTaskID, "older task")
	assertClosed(olderProjID, "older project")
	assertClosed(olderOrgID, "older organization")
	assertActive(newerTaskID, "newer task")
	assertActive(newerProjID, "newer project")
	assertActive(newerOrgID, "newer organization")
}

func TestJobQueue_CloseArchivedProjectAsyncSessions(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "archived-project-session-cleanup-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	org := createJobQueueOrg079(t, pool, "archived-project-session-cleanup")
	projectRecord, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "archived-project-session-cleanup-" + uuid.NewString()[:8],
		DisplayName:    "Archived Project Session Cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	description := "Close archived project-scoped async sessions."
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      projectRecord.ID,
		TaskNumber:     1,
		Title:          "Archived cleanup task",
		Description:    &description,
		WorkStatus:     "draft",
		CreatedByType:  "system",
		CreatedByID:    nil,
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}

	var projectSessionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'project', $2, 'async', 'active', 'system', $3, '{}'::jsonb)
		RETURNING id
	`, org.ID, projectRecord.ID, uuid.Nil).Scan(&projectSessionID); err != nil {
		t.Fatalf("insert project session: %v", err)
	}

	var taskSessionID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO chat_session (
			organization_id, scope_type, scope_id, mode, status,
			created_by_type, created_by_id, metadata
		)
		VALUES ($1, 'project_task', $2, 'async', 'active', 'system', $3, '{}'::jsonb)
		RETURNING id
	`, org.ID, taskRecord.ID, uuid.Nil).Scan(&taskSessionID); err != nil {
		t.Fatalf("insert task session: %v", err)
	}

	if err := repo.NewProjectRepo(pool).Archive(ctx, projectRecord.ID); err != nil {
		t.Fatalf("archive project: %v", err)
	}

	repaired, err := worker.CloseArchivedProjectAsyncSessions(ctx)
	if err != nil {
		t.Fatalf("CloseArchivedProjectAsyncSessions: %v", err)
	}
	if repaired != 2 {
		t.Fatalf("repaired rows = %d, want 2", repaired)
	}

	assertClosed := func(sessionID uuid.UUID, label string) {
		t.Helper()
		var status string
		var closedAt *time.Time
		if err := pool.QueryRow(ctx, `
			SELECT status, closed_at
			FROM chat_session
			WHERE id = $1
		`, sessionID).Scan(&status, &closedAt); err != nil {
			t.Fatalf("query %s session: %v", label, err)
		}
		if status != "closed" || closedAt == nil {
			t.Fatalf("%s session = status:%s closed_at:%v, want closed with closed_at", label, status, closedAt)
		}
	}

	assertClosed(projectSessionID, "project")
	assertClosed(taskSessionID, "task")
}

func TestJobQueue_ListenNotify_Wakeup(t *testing.T) {
	pool := testdb.New(t)
	const wakeupTimeout = 8 * time.Second
	worker := New(pool, nil, Config{
		WorkerID:             "notify-worker",
		PollInterval:         30 * time.Second,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	done := make(chan struct{})
	worker.Register("notify.job", func(context.Context, Job) error {
		select {
		case <-done:
		default:
			close(done)
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = worker.Start(ctx) }()
	defer func() { _ = worker.Stop() }()

	start := time.Now()
	if _, err := worker.Enqueue(context.Background(), nil, "notify.job", 10, map[string]any{"wakeup": true}, nil); err != nil {
		t.Fatalf("enqueue notify job: %v", err)
	}

	select {
	case <-done:
	case <-time.After(wakeupTimeout):
		t.Fatal("worker did not wake from LISTEN/NOTIFY within timeout")
	}

	if elapsed := time.Since(start); elapsed > wakeupTimeout {
		t.Fatalf("notify wakeup too slow: %s", elapsed)
	}
}

func TestIdempotency_KeyUniqueness(t *testing.T) {
	pool := testdb.New(t)
	org := createJobQueueOrg079(t, pool, "idem-unique")
	idemRepo := repo.NewIdempotencyKeyRepo(pool)

	first, err := idemRepo.Store(context.Background(), repo.IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "idem-key-a",
		RequestHash:    "request-a",
		ResponseStatus: 200,
		ResponseBody:   json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("store first key: %v", err)
	}

	second, err := idemRepo.Store(context.Background(), repo.IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "idem-key-a",
		RequestHash:    "request-a",
		ResponseStatus: 200,
		ResponseBody:   json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("store duplicate key: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate key returned different row: first=%s second=%s", first.ID, second.ID)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM idempotency_key
		WHERE organization_id = $1
		  AND key_hash = 'idem-key-a'
	`, org.ID).Scan(&count); err != nil {
		t.Fatalf("count duplicate keys: %v", err)
	}
	if count != 1 {
		t.Fatalf("idempotency rows for key = %d, want 1", count)
	}
}

func TestIdempotency_KeyExpiry(t *testing.T) {
	pool := testdb.New(t)
	org := createJobQueueOrg079(t, pool, "idem-expiry")
	idemRepo := repo.NewIdempotencyKeyRepo(pool)
	fakeClock := clock.NewFake(time.Date(2026, 2, 24, 14, 0, 0, 0, time.UTC))
	worker := New(pool, nil, Config{
		WorkerID:             "idem-cleanup-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})

	if _, err := idemRepo.Store(context.Background(), repo.IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "idem-expired",
		RequestHash:    "hash-expired",
		ResponseStatus: 200,
		ExpiresAt:      fakeClock.Now().UTC().Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("store expired key: %v", err)
	}

	if err := worker.idempotencyCleanupHandler(context.Background(), Job{}); err != nil {
		t.Fatalf("run idempotency cleanup handler: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM idempotency_key
		WHERE organization_id = $1
		  AND key_hash = 'idem-expired'
	`, org.ID).Scan(&remaining); err != nil {
		t.Fatalf("count expired keys: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expired idempotency keys remaining = %d, want 0", remaining)
	}

	if _, err := idemRepo.Store(context.Background(), repo.IdempotencyKey{
		OrganizationID: org.ID,
		KeyHash:        "idem-expired",
		RequestHash:    "hash-expired",
		ResponseStatus: 200,
		ExpiresAt:      fakeClock.Now().UTC().Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("store key after expiry cleanup: %v", err)
	}
}

func TestIdempotency_RequestHashCollision(t *testing.T) {
	pool := testdb.New(t)
	org := createJobQueueOrg079(t, pool, "idem-collision")
	idemRepo := repo.NewIdempotencyKeyRepo(pool)

	status, code, err := recordIdempotentRequest079(context.Background(), idemRepo, org.ID, "idem-collision-key", "request-hash-A")
	if err != nil {
		t.Fatalf("record first request: %v", err)
	}
	if status != 200 || code != "ok" {
		t.Fatalf("first request result = (%d,%s), want (200,ok)", status, code)
	}

	status, code, err = recordIdempotentRequest079(context.Background(), idemRepo, org.ID, "idem-collision-key", "request-hash-B")
	if err != nil {
		t.Fatalf("record collision request: %v", err)
	}
	if status != 409 || code != "idempotency_collision" {
		t.Fatalf("collision result = (%d,%s), want (409,idempotency_collision)", status, code)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM idempotency_key
		WHERE organization_id = $1
		  AND key_hash = 'idem-collision-key'
	`, org.ID).Scan(&count); err != nil {
		t.Fatalf("count collision rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("collision should keep single row, got %d", count)
	}
}

func TestJobQueue_at_least_once_delivery(t *testing.T) {
	pool := testdb.New(t)
	org := createJobQueueOrg079(t, pool, "at-least-once")
	idemRepo := repo.NewIdempotencyKeyRepo(pool)
	// Keep fake clock near database now() because claim timestamps are written in SQL.
	fakeClock := clock.NewFake(time.Now().UTC())

	crashClaimer := New(pool, nil, Config{
		WorkerID:             "crash-claimer",
		BatchSize:            1,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  5 * time.Minute,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})
	worker := New(pool, nil, Config{
		WorkerID:             "healthy-worker",
		BatchSize:            2,
		PollInterval:         50 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  5 * time.Minute,
		CleanupEnqueuePeriod: time.Hour,
		Clock:                fakeClock,
	})

	var (
		mu         sync.Mutex
		deliveries = map[uuid.UUID]int{}
	)
	worker.Register("atleastonce.job", func(ctx context.Context, job Job) error {
		mu.Lock()
		deliveries[job.ID]++
		mu.Unlock()

		var payload struct {
			KeyHash     string `json:"key_hash"`
			RequestHash string `json:"request_hash"`
		}
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}

		_, err := idemRepo.Store(ctx, repo.IdempotencyKey{
			OrganizationID: org.ID,
			KeyHash:        payload.KeyHash,
			RequestHash:    payload.RequestHash,
			ResponseStatus: 200,
			ResponseBody:   json.RawMessage(`{"ok":true}`),
			ExpiresAt:      fakeClock.Now().UTC().Add(24 * time.Hour),
		})
		return err
	})

	for i := 0; i < 5; i++ {
		payload := map[string]any{
			"key_hash":     fmt.Sprintf("atleastonce-%d", i),
			"request_hash": fmt.Sprintf("request-%d", i),
		}
		if _, err := worker.Enqueue(context.Background(), nil, "atleastonce.job", 10, payload, nil); err != nil {
			t.Fatalf("enqueue job %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE job_queue
		SET run_after = now() - interval '1 second'
		WHERE job_type = 'atleastonce.job'
	`); err != nil {
		t.Fatalf("force atleastonce jobs claimable: %v", err)
	}

	crashedClaims, err := crashClaimer.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim crashed job: %v", err)
	}
	if len(crashedClaims) != 1 {
		t.Fatalf("expected one crashed claim, got %d", len(crashedClaims))
	}
	crashedJobID := crashedClaims[0].ID

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = worker.Start(ctx) }()
	defer func() { _ = worker.Stop() }()

	waitForStatusCount079(t, pool, "done", 4, 8*time.Second)

	fakeClock.Advance(6 * time.Minute)
	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("recover stale claim: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered stale rows = %d, want 1", recovered)
	}

	waitForStatusCount079(t, pool, "done", 5, 8*time.Second)

	var (
		status   string
		attempts int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempts
		FROM job_queue
		WHERE id = $1
	`, crashedJobID).Scan(&status, &attempts); err != nil {
		t.Fatalf("query crashed job: %v", err)
	}
	if status != "done" {
		t.Fatalf("crashed job final status = %s, want done", status)
	}
	if attempts != 2 {
		t.Fatalf("crashed job attempts = %d, want 2 (claimed once, recovered once)", attempts)
	}

	mu.Lock()
	if len(deliveries) != 5 {
		mu.Unlock()
		t.Fatalf("unique completion handler job IDs = %d, want 5", len(deliveries))
	}
	mu.Unlock()

	var idempotencyRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM idempotency_key
		WHERE organization_id = $1
	`, org.ID).Scan(&idempotencyRows); err != nil {
		t.Fatalf("count idempotency rows: %v", err)
	}
	if idempotencyRows != 5 {
		t.Fatalf("idempotent effects rows = %d, want 5", idempotencyRows)
	}
}

func createJobQueueOrg079(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()
	org, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        slug,
		DisplayName: slug,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func waitForStatusCount079(t *testing.T, pool *pgxpool.Pool, status string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var got int
		if err := pool.QueryRow(context.Background(), `
			SELECT COUNT(*)
			FROM job_queue
			WHERE status = $1
		`, status).Scan(&got); err != nil {
			t.Fatalf("count jobs by status: %v", err)
		}
		if got == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	var got int
	_ = pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE status = $1
	`, status).Scan(&got)
	t.Fatalf("timed out waiting for status=%s count=%d (got=%d)", status, want, got)
}

func recordIdempotentRequest079(
	ctx context.Context,
	idemRepo *repo.IdempotencyKeyRepo,
	orgID uuid.UUID,
	keyHash string,
	requestHash string,
) (int, string, error) {
	_, err := idemRepo.Store(ctx, repo.IdempotencyKey{
		OrganizationID: orgID,
		KeyHash:        keyHash,
		RequestHash:    requestHash,
		ResponseStatus: 200,
		ResponseBody:   json.RawMessage(`{"ok":true}`),
	})
	if errors.Is(err, repo.ErrIdempotencyConflict) {
		return 409, "idempotency_collision", nil
	}
	if err != nil {
		return 0, "", err
	}
	return 200, "ok", nil
}
