//go:build integration

package jobqueue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestJobWorkerProcessesEnqueuedJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var handled atomic.Int32
	worker.Register("test.process", func(context.Context, Job) error {
		handled.Add(1)
		return nil
	})

	for i := 0; i < 5; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.process", 100, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if handled.Load() == 5 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if handled.Load() != 5 {
		t.Fatalf("processed %d jobs, want 5", handled.Load())
	}

	var doneCount int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount); err != nil {
		t.Fatalf("count done jobs failed: %v", err)
	}
	if doneCount != 5 {
		t.Fatalf("done jobs = %d, want 5", doneCount)
	}
}

func TestJobWorkerClaimPendingLimitClaimsSingleJobEvenWhenBatchSizeIsLarger(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            10,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue fast failed: %v", err)
	}

	claimed, err := worker.claimPendingLimit(context.Background(), 1)
	if err != nil {
		t.Fatalf("claimPendingLimit failed: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}
	if claimed[0].JobType != "test.slow" {
		t.Fatalf("claimed job type = %s, want test.slow", claimed[0].JobType)
	}

	var (
		slowClaimed int
		fastPending int
		fastClaimed int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.slow' AND status = 'claimed'
	`).Scan(&slowClaimed); err != nil {
		t.Fatalf("count slow claimed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.fast' AND status = 'pending'
	`).Scan(&fastPending); err != nil {
		t.Fatalf("count fast pending: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM job_queue WHERE job_type = 'test.fast' AND status = 'claimed'
	`).Scan(&fastClaimed); err != nil {
		t.Fatalf("count fast claimed: %v", err)
	}
	if slowClaimed != 1 {
		t.Fatalf("slow claimed = %d, want 1", slowClaimed)
	}
	if fastPending != 1 {
		t.Fatalf("fast pending while slow blocked = %d, want 1", fastPending)
	}
	if fastClaimed != 0 {
		t.Fatalf("fast claimed while slow blocked = %d, want 0", fastClaimed)
	}
}

func TestAgentTurnEnqueueDedupesActiveAttempt(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	payload := map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first agent_turn: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate agent_turn: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate enqueue id = %s, want %s", secondID, firstID)
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, AgentTurnAttemptKey(sessionID, messageID, 0)).Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
	}
}

func TestAgentTurnDuplicateEnqueueWhileClaimedDoesNotCreateSecondLiveClaim(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "agent-turn-claim",
		BatchSize:            10,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	payload := map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first agent_turn: %v", err)
	}
	claimed, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending agent_turn: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed rows = %d, want 1", len(claimed))
	}
	if claimed[0].ID != firstID {
		t.Fatalf("claimed row id = %s, want %s", claimed[0].ID, firstID)
	}

	secondID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate while claimed: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("duplicate claimed enqueue id = %s, want %s", secondID, firstID)
	}

	again, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending after duplicate enqueue: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim count = %d, want 0", len(again))
	}

	var activeRows int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE dedupe_key = $1
		  AND status IN ('pending', 'claimed')
	`, AgentTurnAttemptKey(sessionID, messageID, 0)).Scan(&activeRows); err != nil {
		t.Fatalf("count claimed deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active claimed rows = %d, want 1", activeRows)
	}
}

func TestJobWorkerSkipLockedAcrossTwoWorkers(t *testing.T) {
	pool := testdb.New(t)

	workerA := New(pool, nil, Config{
		WorkerID:             "worker-a",
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	workerB := New(pool, nil, Config{
		WorkerID:             "worker-b",
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var (
		mu     sync.Mutex
		counts = make(map[uuid.UUID]int)
	)
	handler := func(_ context.Context, job Job) error {
		mu.Lock()
		counts[job.ID]++
		mu.Unlock()
		time.Sleep(25 * time.Millisecond)
		return nil
	}
	workerA.Register("test.locked", handler)
	workerB.Register("test.locked", handler)

	for i := 0; i < 20; i++ {
		if _, err := workerA.Enqueue(context.Background(), nil, "test.locked", 100, map[string]any{"i": i}, nil); err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(workerA, ctx)
	startWorker(workerB, ctx)
	defer func() {
		cancel()
		_ = workerA.Stop()
		_ = workerB.Stop()
	}()

	waitForDoneJobs(t, pool, 20, 10*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 20 {
		t.Fatalf("unique handled jobs = %d, want 20", len(counts))
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("job %s handled %d times, want 1", id, n)
		}
	}
}

func TestJobFailureTransitionsAndStaleRecovery(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	worker.Register("test.fail", func(context.Context, Job) error {
		return errors.New("transient")
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.fail", 100, nil, nil); err != nil {
		t.Fatalf("enqueue fail job: %v", err)
	}

	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}

	if err := worker.executeClaimedJob(context.Background(), jobs[0]); err != nil {
		t.Fatalf("executeClaimedJob transient failure path failed: %v", err)
	}

	var (
		status   string
		attempts int
		runAfter time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, attempts, run_after
		FROM job_queue
		WHERE id = $1
	`, jobs[0].ID).Scan(&status, &attempts, &runAfter); err != nil {
		t.Fatalf("query transient failure job failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status after transient failure = %q, want pending", status)
	}
	if attempts != 1 {
		t.Fatalf("attempts after first claim = %d, want 1", attempts)
	}
	if !runAfter.After(time.Now().Add(500 * time.Millisecond)) {
		t.Fatalf("run_after should be backed off into the future, got %s", runAfter)
	}

	var deadID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, max_attempts, status, attempts, run_after)
		VALUES ('test.dead', 1, 'pending', 0, now())
		RETURNING id
	`).Scan(&deadID); err != nil {
		t.Fatalf("insert dead-letter job failed: %v", err)
	}
	worker.Register("test.dead", func(context.Context, Job) error { return errors.New("permanent") })

	claimed, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claim pending for dead-letter test failed: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatal("expected claimed dead-letter test job")
	}

	found := false
	for _, job := range claimed {
		if job.ID == deadID {
			found = true
			if err := worker.executeClaimedJob(context.Background(), job); err != nil {
				t.Fatalf("executeClaimedJob dead-letter path failed: %v", err)
			}
			break
		}
	}
	if !found {
		t.Fatalf("did not claim expected job %s", deadID)
	}

	if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, deadID).Scan(&status); err != nil {
		t.Fatalf("query dead-letter status failed: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status after max-attempt failure = %q, want dead_letter", status)
	}

	var staleID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after)
		VALUES ('test.stale', 'claimed', 'dead-worker', now() - interval '10 minutes', 1, 3, now())
		RETURNING id
	`).Scan(&staleID); err != nil {
		t.Fatalf("insert stale-claim job failed: %v", err)
	}

	if _, err := worker.RecoverStaleClaims(context.Background()); err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
	}

	var (
		claimedBy *string
		claimedAt *time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at
		FROM job_queue
		WHERE id = $1
	`, staleID).Scan(&status, &claimedBy, &claimedAt); err != nil {
		t.Fatalf("query stale-claim row failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("stale-claim status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("stale claim fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
}

func TestJobWorkerHeartbeatPreventsStaleClaimRecoveryForRunningJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "heartbeat-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  45 * time.Millisecond,
		CleanupEnqueuePeriod: time.Hour,
	})

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var handled atomic.Int32
	worker.Register("test.heartbeat", func(context.Context, Job) error {
		handled.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	})

	jobID := testdb.EnqueueJob(t, pool, "test.heartbeat", 100, map[string]any{"n": 1})
	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(jobs))
	}

	done := make(chan error, 1)
	go func() {
		done <- worker.executeClaimedJob(context.Background(), jobs[0])
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for long-running handler to start")
	}

	time.Sleep(120 * time.Millisecond)

	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
	}
	if recovered != 0 {
		t.Fatalf("recovered rows = %d, want 0 for heartbeat-refreshed claim", recovered)
	}

	var status string
	var claimedBy *string
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy); err != nil {
		t.Fatalf("query running heartbeat job failed: %v", err)
	}
	if status != "claimed" {
		t.Fatalf("running job status = %q, want claimed", status)
	}
	if claimedBy == nil || *claimedBy != "heartbeat-worker" {
		t.Fatalf("running job claimed_by = %v, want heartbeat-worker", claimedBy)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("executeClaimedJob returned error: %v", err)
	}

	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status); err != nil {
		t.Fatalf("query completed heartbeat job failed: %v", err)
	}
	if status != "done" {
		t.Fatalf("completed heartbeat job status = %q, want done", status)
	}
	if handled.Load() != 1 {
		t.Fatalf("handled count = %d, want 1", handled.Load())
	}
}

func TestIdempotencyCleanupJob(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForJobQueue(t, pool, "cleanup-org")

	insertedExpired := 3
	for i := 0; i < insertedExpired; i++ {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO idempotency_key (
				organization_id, key_hash, request_hash, response_status, response_body, expires_at
			)
			VALUES ($1, $2, $3, 200, '{}'::jsonb, now() - interval '1 hour')
		`, org.ID, fmt.Sprintf("expired-%d", i), fmt.Sprintf("request-%d", i)); err != nil {
			t.Fatalf("insert expired idempotency key failed: %v", err)
		}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO idempotency_key (
			organization_id, key_hash, request_hash, response_status, response_body, expires_at
		)
		VALUES ($1, 'fresh-key', 'fresh-request', 200, '{}'::jsonb, now() + interval '12 hours')
	`, org.ID); err != nil {
		t.Fatalf("insert non-expired idempotency key failed: %v", err)
	}

	worker := New(pool, nil, Config{
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	if _, err := worker.Enqueue(context.Background(), nil, idempotencyCleanupJob, 200, map[string]any{"test": true}, nil); err != nil {
		t.Fatalf("enqueue idempotency cleanup job failed: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var expiredCount int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key WHERE expires_at < now()`).Scan(&expiredCount); err != nil {
			t.Fatalf("count expired keys failed: %v", err)
		}
		if expiredCount == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	var (
		expiredCount int
		totalCount   int
	)
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key WHERE expires_at < now()`).Scan(&expiredCount); err != nil {
		t.Fatalf("count expired keys failed: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM idempotency_key`).Scan(&totalCount); err != nil {
		t.Fatalf("count all idempotency keys failed: %v", err)
	}

	if expiredCount != 0 {
		t.Fatalf("expired idempotency keys remaining = %d, want 0", expiredCount)
	}
	if totalCount != 1 {
		t.Fatalf("idempotency key total = %d, want 1", totalCount)
	}
}

func startWorker(worker *Worker, ctx context.Context) {
	go func() {
		_ = worker.Start(ctx)
	}()
}

func waitForDoneJobs(t *testing.T, pool *pgxpool.Pool, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var doneCount int
		if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount); err != nil {
			t.Fatalf("count done jobs failed: %v", err)
		}
		if doneCount == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	var doneCount int
	_ = pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM job_queue WHERE status = 'done'`).Scan(&doneCount)
	t.Fatalf("timed out waiting for done jobs: got %d want %d", doneCount, want)
}

func createOrgForJobQueue(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	org, err := orgRepo.Create(context.Background(), repo.Organization{Slug: slug, DisplayName: slug})
	if err != nil {
		t.Fatalf("create org failed: %v", err)
	}
	return org
}
