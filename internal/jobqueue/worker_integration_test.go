//go:build integration

package jobqueue

import (
	"context"
	"encoding/json"
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

func TestJobWorkerProcessesBatchConcurrently(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            3,
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	release := make(chan struct{})
	var started atomic.Int32
	worker.Register("test.concurrent", func(context.Context, Job) error {
		started.Add(1)
		<-release
		return nil
	})

	for i := 0; i < 3; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.concurrent", 100, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue concurrent job %d failed: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	released := false
	defer func() {
		cancel()
		if !released {
			close(release)
		}
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if started.Load() == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if started.Load() != 3 {
		t.Fatalf("started concurrent jobs = %d, want 3 claimed/executing together", started.Load())
	}

	close(release)
	released = true
	waitForDoneJobs(t, pool, 3, 5*time.Second)
}

func TestJobWorkerRefillsFreedSlotBeforeWholeBatchFinishes(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            2,
		PollInterval:         100 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	slowRelease := make(chan struct{})
	firstFastStarted := make(chan struct{}, 1)
	secondFastStarted := make(chan struct{}, 1)
	var fastCount atomic.Int32

	worker.Register("test.refill.slow", func(context.Context, Job) error {
		<-slowRelease
		return nil
	})
	worker.Register("test.refill.fast", func(context.Context, Job) error {
		switch fastCount.Add(1) {
		case 1:
			firstFastStarted <- struct{}{}
		case 2:
			secondFastStarted <- struct{}{}
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue first fast failed: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, "test.refill.fast", 80, map[string]any{"n": 3}, nil); err != nil {
		t.Fatalf("enqueue second fast failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		close(slowRelease)
		_ = worker.Stop()
	}()

	select {
	case <-firstFastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first fast job to start")
	}

	select {
	case <-secondFastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second fast job did not start while slow batch peer was still running")
	}
}

func TestJobWorkerPollsNewJobsWhileEarlierClaimedJobStillRunning(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            2,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	slowRelease := make(chan struct{})
	slowStarted := make(chan struct{}, 1)
	fastStarted := make(chan struct{}, 1)

	worker.Register("test.arrival.slow", func(context.Context, Job) error {
		select {
		case slowStarted <- struct{}{}:
		default:
		}
		<-slowRelease
		return nil
	})
	worker.Register("test.arrival.fast", func(context.Context, Job) error {
		select {
		case fastStarted <- struct{}{}:
		default:
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.arrival.slow", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue slow failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		close(slowRelease)
		_ = worker.Stop()
	}()

	select {
	case <-slowStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow job to start")
	}

	if _, err := worker.Enqueue(context.Background(), nil, "test.arrival.fast", 90, map[string]any{"n": 2}, nil); err != nil {
		t.Fatalf("enqueue fast after slow start failed: %v", err)
	}

	select {
	case <-fastStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("fast job did not start while earlier claimed job was still running")
	}
}

func TestJobWorkerReservesSlotsForAgentTurnsOverBackgroundJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		BatchSize:            6,
		PollInterval:         20 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	backgroundRelease := make(chan struct{})
	agentRelease := make(chan struct{})
	agentStarted := make(chan struct{}, 1)
	initialAgentStarted := make(chan struct{}, 1)
	var backgroundCount atomic.Int32
	var agentCount atomic.Int32

	worker.Register("test.background", func(context.Context, Job) error {
		backgroundCount.Add(1)
		<-backgroundRelease
		return nil
	})
	worker.Register(agentTurnJobType, func(context.Context, Job) error {
		switch agentCount.Add(1) {
		case 1:
			select {
			case initialAgentStarted <- struct{}{}:
			default:
			}
			<-agentRelease
		default:
			select {
			case agentStarted <- struct{}{}:
			default:
			}
		}
		return nil
	})

	sessionID := uuid.New()
	messageID := uuid.New()
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, map[string]any{
		"session_id": sessionID,
		"message_id": messageID,
	}, nil); err != nil {
		t.Fatalf("enqueue initial agent_turn failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := worker.Enqueue(context.Background(), nil, "test.background", 60, map[string]any{"n": i}, nil); err != nil {
			t.Fatalf("enqueue background %d failed: %v", i+1, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	backgroundClosed := false
	agentClosed := false
	defer func() {
		cancel()
		if !backgroundClosed {
			close(backgroundRelease)
		}
		if !agentClosed {
			close(agentRelease)
		}
		_ = worker.Stop()
	}()

	select {
	case <-initialAgentStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("initial agent_turn did not start")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && backgroundCount.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	if backgroundCount.Load() != 2 {
		t.Fatalf("background started = %d, want 2 with reserved agent slots", backgroundCount.Load())
	}

	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, map[string]any{
		"session_id": uuid.New(),
		"message_id": uuid.New(),
	}, nil); err != nil {
		t.Fatalf("enqueue agent_turn failed: %v", err)
	}

	close(agentRelease)
	agentClosed = true
	select {
	case <-agentStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("agent_turn did not start while background jobs were occupying reserved slots")
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

func TestAgentTurnRetryEnqueueCollapsesPendingGroupToNewestAttempt(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	messageID := uuid.New()

	firstRunAfter := time.Now().UTC().Add(30 * time.Minute)
	secondRunAfter := firstRunAfter.Add(30 * time.Minute)
	thirdRunAfter := secondRunAfter.Add(30 * time.Minute)

	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 0,
	}, &firstRunAfter); err != nil {
		t.Fatalf("enqueue first retry attempt: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 1,
	}, &secondRunAfter); err != nil {
		t.Fatalf("enqueue second retry attempt: %v", err)
	}
	if _, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 70, agentTurnKeyPayload{
		SessionID:  sessionID,
		MessageID:  messageID,
		RetryCount: 2,
	}, &thirdRunAfter); err != nil {
		t.Fatalf("enqueue third retry attempt: %v", err)
	}

	var (
		activeRows int
		rawPayload []byte
		runAfter   time.Time
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM job_queue
		WHERE group_key = $1
		  AND status = 'pending'
	`, AgentTurnGroupKey(sessionID, messageID)).Scan(&activeRows); err != nil {
		t.Fatalf("count pending group rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("pending group rows = %d, want 1", activeRows)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT payload, run_after
		FROM job_queue
		WHERE group_key = $1
		  AND status = 'pending'
	`, AgentTurnGroupKey(sessionID, messageID)).Scan(&rawPayload, &runAfter); err != nil {
		t.Fatalf("load collapsed pending row: %v", err)
	}

	var payload agentTurnKeyPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatalf("unmarshal collapsed pending payload: %v", err)
	}
	if payload.RetryCount != 2 {
		t.Fatalf("retry_count = %d, want 2", payload.RetryCount)
	}
	if !runAfter.Equal(thirdRunAfter) {
		t.Fatalf("run_after = %s, want %s", runAfter, thirdRunAfter)
	}
}

func TestChatSummarizeEnqueueDedupesActiveSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	sessionID := uuid.New()
	payload := map[string]any{
		"session_id":          sessionID,
		"layer_budget_tokens": 130000,
	}

	firstID, err := worker.Enqueue(context.Background(), nil, "chat_summarize", 60, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first chat_summarize: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, "chat_summarize", 60, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate chat_summarize: %v", err)
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
	`, "chat_summarize:"+sessionID.String()).Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
	}
}

func TestRollupUpdateEnqueueDedupesByOrgAndDate(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	orgID := uuid.New()
	payload := map[string]any{
		"org_id":      orgID,
		"rollup_date": "2026-03-22",
	}

	firstID, err := worker.Enqueue(context.Background(), nil, "rollup_update", 50, payload, nil)
	if err != nil {
		t.Fatalf("enqueue first rollup_update: %v", err)
	}
	secondID, err := worker.Enqueue(context.Background(), nil, "rollup_update", 50, payload, nil)
	if err != nil {
		t.Fatalf("enqueue duplicate rollup_update: %v", err)
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
	`, "rollup_update:"+orgID.String()+":2026-03-22").Scan(&activeRows); err != nil {
		t.Fatalf("count active deduped rows: %v", err)
	}
	if activeRows != 1 {
		t.Fatalf("active deduped rows = %d, want 1", activeRows)
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

func TestJobWorkerRecoverStaleClaimsReleasesForeignAgentTurnClaimsQuickly(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "new-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var claimedID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after, payload)
		VALUES (
			'agent_turn',
			'claimed',
			'dead-worker',
			now() - interval '45 seconds',
			1,
			3,
			now(),
			'{"session_id":"11111111-1111-1111-1111-111111111111","message_id":"22222222-2222-2222-2222-222222222222"}'::jsonb
		)
		RETURNING id
	`).Scan(&claimedID); err != nil {
		t.Fatalf("insert foreign claimed agent_turn failed: %v", err)
	}

	recovered, err := worker.RecoverStaleClaims(context.Background())
	if err != nil {
		t.Fatalf("RecoverStaleClaims failed: %v", err)
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
	`, claimedID).Scan(&status, &claimedBy, &claimedAt); err != nil {
		t.Fatalf("query recovered foreign claim failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("claimed fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
}

func TestJobWorkerReleaseClaimsForWorkerRequeuesGracefulShutdownClaims(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "graceful-stop-worker",
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	jobID := testdb.EnqueueJob(t, pool, "test.release", 100, map[string]any{"n": 1})
	jobs, err := worker.claimPending(context.Background())
	if err != nil {
		t.Fatalf("claimPending failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(jobs))
	}
	if jobs[0].ID != jobID {
		t.Fatalf("claimed job id = %s, want %s", jobs[0].ID, jobID)
	}

	released, err := worker.releaseClaimsForWorker(context.Background())
	if err != nil {
		t.Fatalf("releaseClaimsForWorker failed: %v", err)
	}
	if released != 1 {
		t.Fatalf("released claims = %d, want 1", released)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
		lastError *string
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT status, claimed_by, claimed_at, last_error
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy, &claimedAt, &lastError); err != nil {
		t.Fatalf("query released job failed: %v", err)
	}
	if status != "pending" {
		t.Fatalf("released job status = %q, want pending", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("released claim fields should be cleared, got claimed_by=%v claimed_at=%v", claimedBy, claimedAt)
	}
	if lastError == nil || *lastError != "released on worker shutdown" {
		t.Fatalf("released job last_error = %v, want release marker", lastError)
	}
}

func TestJobWorkerProcessAvailableJobsRecoversStaleClaimsBeforeClaiming(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		WorkerID:             "recover-before-claim",
		BatchSize:            2,
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	var staleID uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, attempts, max_attempts, run_after, payload)
		VALUES ('test.stale', 'claimed', 'dead-worker', now() - interval '10 minutes', 1, 3, now(), '{}'::jsonb)
		RETURNING id
	`).Scan(&staleID); err != nil {
		t.Fatalf("insert stale claim failed: %v", err)
	}

	started := make(chan struct{}, 1)
	worker.Register("test.stale", func(context.Context, Job) error { return nil })
	worker.Register("test.fresh", func(context.Context, Job) error {
		select {
		case started <- struct{}{}:
		default:
		}
		return nil
	})

	if _, err := worker.Enqueue(context.Background(), nil, "test.fresh", 100, map[string]any{"n": 1}, nil); err != nil {
		t.Fatalf("enqueue fresh job failed: %v", err)
	}

	if err := worker.processAvailableJobs(context.Background()); err != nil {
		t.Fatalf("processAvailableJobs failed: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fresh job did not start after stale claim recovery")
	}

	var status string
	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, staleID).Scan(&status); err != nil {
		t.Fatalf("query stale claim status failed: %v", err)
	}
	if status != "pending" && status != "done" {
		t.Fatalf("stale claim status = %q, want pending or done", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsClearsClaimedClosedSessions(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-closed-session",
		DisplayName: "Purge Closed Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).Close(ctx, session.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, claimed_by, claimed_at, payload, run_after)
		VALUES ('agent_turn', 'claimed', 'dead-worker', now(), $1::jsonb, now())
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s"}`, session.ID, uuid.New())).Scan(&jobID); err != nil {
		t.Fatalf("insert claimed closed-session job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var (
		status    string
		claimedBy *string
		claimedAt *time.Time
		lastError *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, claimed_by, claimed_at, last_error
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status, &claimedBy, &claimedAt, &lastError); err != nil {
		t.Fatalf("query purged job: %v", err)
	}
	if status != "dead_letter" {
		t.Fatalf("status after purge = %q, want dead_letter", status)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("claimed fields after purge = claimed_by:%v claimed_at:%v, want nil", claimedBy, claimedAt)
	}
	if lastError == nil || *lastError != "purged at worker startup: session closed" {
		t.Fatalf("last_error = %v, want session closed purge message", lastError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsCollapsesSupersededBootstrapContinuations(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-bootstrap-duplicates",
		DisplayName: "Purge Bootstrap Duplicates",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "bootstrap-project",
		DisplayName:    "Bootstrap Project",
		Description:    "Project for bootstrap continuation cleanup",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cancelledTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   uuid.New(),
		Status:         "cancelled",
	})
	if err != nil {
		t.Fatalf("create cancelled turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_session
		SET current_turn_id = $2
		WHERE id = $1
	`, session.ID, cancelledTurn.ID); err != nil {
		t.Fatalf("set current_turn_id: %v", err)
	}

	messageRepo := repo.NewChatMessageRepo(pool)
	messageIDs := make([]uuid.UUID, 0, 3)
	for i := 0; i < 3; i++ {
		msg, err := messageRepo.Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   fmt.Sprintf("Bootstrap continuation %d", i+1),
			Status:    "pending",
			Metadata:  json.RawMessage(`{"source":"project_bootstrap"}`),
		})
		if err != nil {
			t.Fatalf("create bootstrap continuation message %d: %v", i+1, err)
		}
		messageIDs = append(messageIDs, msg.ID)
	}

	jobIDs := make([]uuid.UUID, 0, len(messageIDs))
	base := time.Now().UTC().Add(-time.Minute)
	for i, messageID := range messageIDs {
		var jobID uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO job_queue (job_type, status, payload, run_after, priority)
			VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70)
			RETURNING id
		`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s"}`, session.ID, messageID), base.Add(time.Duration(i)*time.Second)).Scan(&jobID); err != nil {
			t.Fatalf("insert bootstrap continuation job %d: %v", i+1, err)
		}
		jobIDs = append(jobIDs, jobID)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged jobs = %d, want 2 superseded bootstrap continuations", purged)
	}

	for i := 0; i < len(jobIDs)-1; i++ {
		var status string
		var lastError *string
		if err := pool.QueryRow(ctx, `
			SELECT status, last_error
			FROM job_queue
			WHERE id = $1
		`, jobIDs[i]).Scan(&status, &lastError); err != nil {
			t.Fatalf("query purged bootstrap continuation %d: %v", i+1, err)
		}
		if status != "dead_letter" {
			t.Fatalf("bootstrap continuation %d status = %q, want dead_letter", i+1, status)
		}
		if lastError == nil || *lastError != "purged at worker startup: superseded bootstrap continuation" {
			t.Fatalf("bootstrap continuation %d last_error = %v, want superseded-bootstrap marker", i+1, lastError)
		}
	}

	var newestStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobIDs[len(jobIDs)-1]).Scan(&newestStatus); err != nil {
		t.Fatalf("query newest bootstrap continuation: %v", err)
	}
	if newestStatus != "pending" {
		t.Fatalf("newest bootstrap continuation status = %q, want pending", newestStatus)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsLiveSupervisorRecoveryTurn(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-live-supervisor-recovery",
		DisplayName: "Purge Live Supervisor Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert supervisor recovery job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("supervisor recovery job status = %q, want pending", status)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsKeepsSupervisorRetryJob(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-supervisor-retry-job",
		DisplayName: "Purge Supervisor Retry Job",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}

	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, now() + interval '15 minutes', 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID)).Scan(&jobID); err != nil {
		t.Fatalf("insert supervisor retry job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged jobs = %d, want 0", purged)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("query supervisor retry job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("supervisor retry job status = %q, want pending", status)
	}
}

func TestJobWorkerRejitterPendingRateLimitedAgentTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "rejitter-pending-rate-limited-agent-turns",
		DisplayName: "Rejitter Pending Rate Limited Agent Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "assistant",
		Content:   "[Rate limited, retrying in 15m...]",
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("create rate limit message: %v", err)
	}

	originalRunAfter := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	var jobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority)
		VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID), originalRunAfter).Scan(&jobID); err != nil {
		t.Fatalf("insert pending retry job: %v", err)
	}

	rejittered, err := worker.RejitterPendingRateLimitedAgentTurns(ctx)
	if err != nil {
		t.Fatalf("RejitterPendingRateLimitedAgentTurns: %v", err)
	}
	if rejittered != 1 {
		t.Fatalf("rejittered jobs = %d, want 1", rejittered)
	}

	var (
		runAfter      time.Time
		jitterApplied bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT run_after,
		       COALESCE((payload->>'rate_limit_jitter_applied')::boolean, false)
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&runAfter, &jitterApplied); err != nil {
		t.Fatalf("query rejittered job: %v", err)
	}
	wantRunAfter := rejitteredRateLimitedRunAfter(worker.clock.Now().UTC(), originalRunAfter, session.ID, message.ID, 1)
	if !runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", runAfter, wantRunAfter)
	}
	if !jitterApplied {
		t.Fatal("expected rate_limit_jitter_applied flag to be set")
	}

	rejittered, err = worker.RejitterPendingRateLimitedAgentTurns(ctx)
	if err != nil {
		t.Fatalf("RejitterPendingRateLimitedAgentTurns second pass: %v", err)
	}
	if rejittered != 0 {
		t.Fatalf("rejittered jobs on second pass = %d, want 0", rejittered)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsCollapsesSupersededProjectTaskContinuations(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-project-task-continuations",
		DisplayName: "Purge Project Task Continuations",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	messageIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, messageID := range messageIDs {
		runAfter := time.Now().UTC().Add(time.Duration(i+1) * time.Hour)
		createdAt := time.Now().UTC().Add(time.Duration(i) * time.Minute)
		if _, err := pool.Exec(ctx, `
			INSERT INTO chat_message (id, session_id, role, content, status, created_at, updated_at)
			VALUES ($1, $2, 'system', $3, 'completed', $4, $4)
		`, messageID, session.ID, fmt.Sprintf("[Continuation %d]", i+1), createdAt); err != nil {
			t.Fatalf("insert continuation message %d: %v", i+1, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO job_queue (job_type, status, payload, run_after, priority, created_at, updated_at, group_key, dedupe_key)
			VALUES ('agent_turn', 'pending', $1::jsonb, $2, 70, $3, $3, $4, $5)
		`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, messageID), runAfter, createdAt,
			fmt.Sprintf("agent_turn:%s:%s", session.ID, messageID),
			fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, messageID, 1),
		); err != nil {
			t.Fatalf("insert pending continuation job %d: %v", i+1, err)
		}
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged jobs = %d, want 2", purged)
	}

	rows, err := pool.Query(ctx, `
		SELECT status, COALESCE(last_error, '')
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at ASC
	`, session.ID)
	if err != nil {
		t.Fatalf("query continuation jobs: %v", err)
	}
	defer rows.Close()

	var statuses []string
	var errors []string
	for rows.Next() {
		var status, lastError string
		if err := rows.Scan(&status, &lastError); err != nil {
			t.Fatalf("scan continuation jobs: %v", err)
		}
		statuses = append(statuses, status)
		errors = append(errors, lastError)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate continuation jobs: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("continuation jobs = %d, want 3", len(statuses))
	}
	if statuses[0] != "dead_letter" || errors[0] != "purged stale project_task continuation" {
		t.Fatalf("oldest job = (%q, %q), want dead_letter/purged stale project_task continuation", statuses[0], errors[0])
	}
	if statuses[1] != "dead_letter" || errors[1] != "purged stale project_task continuation" {
		t.Fatalf("middle job = (%q, %q), want dead_letter/purged stale project_task continuation", statuses[1], errors[1])
	}
	if statuses[2] != "pending" {
		t.Fatalf("newest job status = %q, want pending", statuses[2])
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-recovery",
		DisplayName: "Requeue Stranded Supervisor Recovery",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the stranded task execution.",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status    string
		messageID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &messageID); err != nil {
		t.Fatalf("query requeued supervisor recovery job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if messageID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", messageID, message.ID)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsRemovesTerminalMessageAttemptDispatches(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-terminal-message-attempt-dispatch",
		DisplayName: "Purge Terminal Message Attempt Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Terminal Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle stale dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "resume task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	if _, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
		RetryCount:       1,
	}); err != nil {
		t.Fatalf("Create completed turn: %v", err)
	}

	liveMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "current live task turn",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create live message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &liveMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var staleJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":1}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 1),
	).Scan(&staleJobID); err != nil {
		t.Fatalf("insert stale job: %v", err)
	}

	var liveJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":2}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 2),
	).Scan(&liveJobID); err != nil {
		t.Fatalf("insert live job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var staleStatus, liveStatus string
	var staleError, liveError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, staleJobID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, liveJobID).Scan(&liveStatus, &liveError); err != nil {
		t.Fatalf("query live job: %v", err)
	}
	staleErrorValue := "<nil>"
	if staleError != nil {
		staleErrorValue = *staleError
	}
	if staleStatus != "dead_letter" || staleError == nil || *staleError != "purged stale terminal message-attempt dispatch" {
		t.Fatalf("stale job = (%q, %q), want dead_letter/purged stale terminal message-attempt dispatch", staleStatus, staleErrorValue)
	}
	if liveStatus != "pending" || liveError != nil {
		t.Fatalf("live job = (%q, %v), want pending/<nil>", liveStatus, liveError)
	}
}

func TestJobWorkerPurgeStaleAgentTurnJobsRemovesDuplicateLiveMessageAttemptDispatches(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "purge-live-message-attempt-dispatch",
		DisplayName: "Purge Live Message Attempt Dispatch",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Live Dispatch Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle duplicate live dispatch cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "continue task",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create message: %v", err)
	}

	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	var staleJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, message.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, message.ID, 0),
	).Scan(&staleJobID); err != nil {
		t.Fatalf("insert stale job: %v", err)
	}

	otherMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "another task prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create other message: %v", err)
	}
	var otherJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, otherMessage.ID, 0),
	).Scan(&otherJobID); err != nil {
		t.Fatalf("insert other job: %v", err)
	}

	purged, err := worker.PurgeStaleAgentTurnJobs(ctx)
	if err != nil {
		t.Fatalf("PurgeStaleAgentTurnJobs: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged jobs = %d, want 1", purged)
	}

	var staleStatus, otherStatus string
	var staleError, otherError *string
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, staleJobID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatalf("query stale job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status, last_error
		FROM job_queue
		WHERE id = $1
	`, otherJobID).Scan(&otherStatus, &otherError); err != nil {
		t.Fatalf("query other job: %v", err)
	}
	staleErrorValue := "<nil>"
	if staleError != nil {
		staleErrorValue = *staleError
	}
	if staleStatus != "dead_letter" || staleError == nil || *staleError != "purged duplicate live message-attempt dispatch" {
		t.Fatalf("stale job = (%q, %q), want dead_letter/purged duplicate live message-attempt dispatch", staleStatus, staleErrorValue)
	}
	if otherStatus != "pending" || otherError != nil {
		t.Fatalf("other job = (%q, %v), want pending/<nil>", otherStatus, otherError)
	}
}

func TestJobWorkerClaimPendingAgentTurnsSkipsDuplicateLiveMessageAttemptDispatch(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-skip-duplicate-live-message-attempt",
		DisplayName: "Claim Skip Duplicate Live Message Attempt",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Claim Guard Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You handle duplicate claim cleanup.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	duplicateMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "duplicate task continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create duplicate message: %v", err)
	}
	liveTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &duplicateMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("Create live turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &liveTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	otherMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "fresh task continuation",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create other message: %v", err)
	}

	var duplicateJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, duplicateMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, duplicateMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, duplicateMessage.ID, 0),
	).Scan(&duplicateJobID); err != nil {
		t.Fatalf("insert duplicate job: %v", err)
	}
	var freshJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, otherMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, otherMessage.ID, 0),
	).Scan(&freshJobID); err != nil {
		t.Fatalf("insert fresh job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed jobs = %d, want 0 while session has live turn", len(claimed))
	}

	var duplicateStatus, freshStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, duplicateJobID).Scan(&duplicateStatus); err != nil {
		t.Fatalf("query duplicate job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, freshJobID).Scan(&freshStatus); err != nil {
		t.Fatalf("query fresh job: %v", err)
	}
	if duplicateStatus != "pending" {
		t.Fatalf("duplicate job status = %q, want pending", duplicateStatus)
	}
	if freshStatus != "pending" {
		t.Fatalf("fresh job status = %q, want pending", freshStatus)
	}
}

func TestJobWorkerClaimPendingAgentTurnsClaimsOnlyOneJobPerSession(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "claim-one-job-per-session",
		DisplayName: "Claim One Job Per Session",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        uuid.New(),
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	firstMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "first pending prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create first message: %v", err)
	}
	secondMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "second pending prompt",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("Create second message: %v", err)
	}

	var firstJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, firstMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, firstMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, firstMessage.ID, 0),
	).Scan(&firstJobID); err != nil {
		t.Fatalf("insert first job: %v", err)
	}
	var secondJobID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, status, payload, run_after, priority, group_key, dedupe_key)
		VALUES ('agent_turn', 'pending', $1::jsonb, now(), 70, $2, $3)
		RETURNING id
	`, fmt.Sprintf(`{"session_id":"%s","message_id":"%s","retry_count":0}`, session.ID, secondMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s", session.ID, secondMessage.ID),
		fmt.Sprintf("agent_turn:%s:%s:%d", session.ID, secondMessage.ID, 0),
	).Scan(&secondJobID); err != nil {
		t.Fatalf("insert second job: %v", err)
	}

	claimed, err := worker.claimPendingAgentTurns(ctx, 10)
	if err != nil {
		t.Fatalf("claimPendingAgentTurns: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed jobs = %d, want 1", len(claimed))
	}

	var firstStatus, secondStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, firstJobID).Scan(&firstStatus); err != nil {
		t.Fatalf("query first job: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM job_queue WHERE id = $1`, secondJobID).Scan(&secondStatus); err != nil {
		t.Fatalf("query second job: %v", err)
	}
	claimedID := claimed[0].ID
	if claimedID != firstJobID {
		t.Fatalf("claimed job = %s, want first job %s", claimedID, firstJobID)
	}
	if firstStatus != "claimed" {
		t.Fatalf("first job status = %q, want claimed", firstStatus)
	}
	if secondStatus != "pending" {
		t.Fatalf("second job status = %q, want pending", secondStatus)
	}
}

func TestJobWorkerRequeueStrandedSupervisorRecoveryTurnsSkipsPausedAndArchivedProjects(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-supervisor-skip-paused",
		DisplayName: "Requeue Stranded Supervisor Skip Paused",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Supervisor Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover stranded supervisor turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	createStrandedSupervisorSession := func(projectID uuid.UUID) uuid.UUID {
		t.Helper()
		task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
			OrganizationID: org.ID,
			ProjectID:      projectID,
			Title:          "Recover stranded supervisor task",
			WorkStatus:     "draft",
			BlocksScope:    "task",
			CreatedByType:  "system",
			CreatedByID:    &agent.ID,
		})
		if err != nil {
			t.Fatalf("create project task: %v", err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project_task",
			ScopeID:        task.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   "supervisor recovery: resume task",
			Status:    "pending",
			Metadata:  json.RawMessage(`{"source":"supervisor"}`),
		})
		if err != nil {
			t.Fatalf("create supervisor message: %v", err)
		}
		turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
			SessionID:        session.ID,
			TurnNumber:       1,
			RespondingType:   "agent",
			RespondingID:     agent.ID,
			Status:           "pending",
			TriggerMessageID: &message.ID,
		})
		if err != nil {
			t.Fatalf("create pending turn: %v", err)
		}
		if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
			t.Fatalf("update current turn: %v", err)
		}
		return session.ID
	}

	pausedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "stranded-paused-project",
		DisplayName:    "Stranded Paused Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create paused project: %v", err)
	}
	archivedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "stranded-archived-project",
		DisplayName:    "Stranded Archived Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create archived project: %v", err)
	}
	if err := repo.NewProjectRepo(pool).Archive(ctx, archivedProject.ID); err != nil {
		t.Fatalf("archive project: %v", err)
	}

	pausedSessionID := createStrandedSupervisorSession(pausedProject.ID)
	archivedSessionID := createStrandedSupervisorSession(archivedProject.ID)

	requeued, err := worker.RequeueStrandedSupervisorRecoveryTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedSupervisorRecoveryTurns: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0", requeued)
	}

	for _, sessionID := range []uuid.UUID{pausedSessionID, archivedSessionID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM job_queue
			WHERE job_type = 'agent_turn'
			  AND (payload->>'session_id')::uuid = $1
		`, sessionID).Scan(&count); err != nil {
			t.Fatalf("count requeued jobs for session %s: %v", sessionID, err)
		}
		if count != 0 {
			t.Fatalf("requeued jobs for session %s = %d, want 0", sessionID, count)
		}
	}
}

func TestJobWorkerRequeueStrandedUserMessageTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-stranded-user-message",
		DisplayName: "Requeue Stranded User Message",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "human_user",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cancelledMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Old cancelled kickoff",
		Status:    "pending",
		Metadata:  json.RawMessage(fmt.Sprintf(`{"agent_turn_dispatch":{"cancelled_at":%q,"cancel_reason":"user_cancelled"}}`, time.Now().UTC().Format(time.RFC3339Nano))),
	})
	if err != nil {
		t.Fatalf("create cancelled message: %v", err)
	}
	cancelledTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "cancelled",
		TriggerMessageID: &cancelledMessage.ID,
	})
	if err != nil {
		t.Fatalf("create cancelled turn: %v", err)
	}
	if cancelledTurn.TriggerMessageID == nil || *cancelledTurn.TriggerMessageID != cancelledMessage.ID {
		t.Fatalf("cancelled turn trigger_message_id = %v, want %s", cancelledTurn.TriggerMessageID, cancelledMessage.ID)
	}

	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Start a fresh Sam.blog project from scratch.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create stranded user message: %v", err)
	}

	requeued, err := worker.RequeueStrandedUserMessageTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueStrandedUserMessageTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued user message job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobs(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-without-jobs",
		DisplayName: "Requeue Pending Turns Without Jobs",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-pending-turns-project",
		DisplayName:    "Requeue Pending Turns Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsSkipsPausedAndArchivedProjects(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-skip-paused",
		DisplayName: "Requeue Pending Turns Skip Paused",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	createPendingTaskSession := func(projectID uuid.UUID) uuid.UUID {
		t.Helper()
		taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
			OrganizationID: org.ID,
			ProjectID:      projectID,
			Title:          "Pending task turn",
			WorkStatus:     "draft",
			BlocksScope:    "task",
			CreatedByType:  "system",
			CreatedByID:    &agent.ID,
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
			OrganizationID: org.ID,
			ScopeType:      "project_task",
			ScopeID:        taskRecord.ID,
			Mode:           "async",
			Status:         "active",
			CreatedByType:  "system",
			CreatedByID:    uuid.New(),
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
			SessionID: session.ID,
			Role:      "user",
			Content:   "Resume the pending task turn.",
			Status:    "pending",
		})
		if err != nil {
			t.Fatalf("create user message: %v", err)
		}
		turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
			SessionID:        session.ID,
			TurnNumber:       1,
			RespondingType:   "agent",
			RespondingID:     agent.ID,
			Status:           "pending",
			TriggerMessageID: &message.ID,
		})
		if err != nil {
			t.Fatalf("create pending turn: %v", err)
		}
		if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
			t.Fatalf("update current turn: %v", err)
		}
		return session.ID
	}

	pausedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "paused-project",
		DisplayName:    "Paused Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create paused project: %v", err)
	}
	archivedProject, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "archived-project",
		DisplayName:    "Archived Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create archived project: %v", err)
	}
	if err := repo.NewProjectRepo(pool).Archive(ctx, archivedProject.ID); err != nil {
		t.Fatalf("archive project: %v", err)
	}

	pausedSessionID := createPendingTaskSession(pausedProject.ID)
	archivedSessionID := createPendingTaskSession(archivedProject.ID)

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns = %d, want 0", requeued)
	}

	for _, sessionID := range []uuid.UUID{pausedSessionID, archivedSessionID} {
		var count int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM job_queue
			WHERE job_type = 'agent_turn'
			  AND (payload->>'session_id')::uuid = $1
		`, sessionID).Scan(&count); err != nil {
			t.Fatalf("count requeued jobs for session %s: %v", sessionID, err)
		}
		if count != 0 {
			t.Fatalf("requeued jobs for session %s = %d, want 0", sessionID, count)
		}
	}
}

func TestJobWorkerRequeuePendingTurnsWithoutJobsRequeuesAfterProjectResume(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-pending-turns-after-resume",
		DisplayName: "Requeue Pending Turns After Resume",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "paused-then-resumed-project",
		DisplayName:    "Paused Then Resumed Project",
		DeliveryMode:   "gated",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
		Settings:       []byte(`{"pause":{"is_paused":true,"reason":"operator pause"}}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID: org.ID,
		ProjectID:      project.ID,
		Title:          "Pending task turn",
		WorkStatus:     "draft",
		BlocksScope:    "task",
		CreatedByType:  "system",
		CreatedByID:    &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Resume the pending task turn.",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create user message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "pending",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create pending turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("update current turn: %v", err)
	}

	requeued, err := worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs while paused: %v", err)
	}
	if requeued != 0 {
		t.Fatalf("requeued turns while paused = %d, want 0", requeued)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE project
		SET settings = '{}'::jsonb
		WHERE id = $1
	`, project.ID); err != nil {
		t.Fatalf("resume project: %v", err)
	}

	requeued, err = worker.RequeuePendingTurnsWithoutJobs(ctx)
	if err != nil {
		t.Fatalf("RequeuePendingTurnsWithoutJobs after resume: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued turns after resume = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, (payload->>'message_id')::uuid, (payload->>'session_id')::uuid
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID); err != nil {
		t.Fatalf("query requeued pending turn job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-sessions-without-turns",
		DisplayName: "Requeue Active Execution Sessions Without Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover pending work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-project",
		DisplayName:    "Requeue Active Execution Project",
		Description:    "Project for active execution task-session repair",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-template",
		DisplayName:    "Requeue Active Execution Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stranded active execution session",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor kickoff message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create completed recovery turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed recovery turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued active execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressContinuationTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-continuation-turns",
		DisplayName: "Recover Stale Continuation Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Continuation Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked continuation turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-continuation-project",
		DisplayName:    "Recover Stale Continuation Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-continuation-template",
		DisplayName:    "Recover Stale Continuation Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Execute",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale continuation turn",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rootMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "Continue the task from the compressed context.",
		Status:    "final",
	})
	if err != nil {
		t.Fatalf("create root message: %v", err)
	}
	cycleID := uuid.New()
	rootTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		CycleID:          &cycleID,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &rootMessage.ID,
	})
	if err != nil {
		t.Fatalf("create root turn: %v", err)
	}
	continuationTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:      session.ID,
		TurnNumber:     2,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agent.ID,
		Status:         "in_progress",
	})
	if err != nil {
		t.Fatalf("create continuation turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &continuationTurn.ID); err != nil {
		t.Fatalf("set current continuation turn: %v", err)
	}
	if _, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      taskRecord.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	}); err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, continuationTurn.ID); err != nil {
		t.Fatalf("age continuation turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-continuation-provider",
		DisplayName: "Recover Stale Continuation Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &continuationTurn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &continuationTurn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "streaming",
	})
	if err != nil {
		t.Fatalf("create streaming assistant message: %v", err)
	}
	if rootTurn.TriggerMessageID == nil || *rootTurn.TriggerMessageID != rootMessage.ID {
		t.Fatalf("root turn trigger_message_id = %v, want %s", rootTurn.TriggerMessageID, rootMessage.ID)
	}

	repaired, err := worker.RecoverStaleInProgressContinuationTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressContinuationTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired continuations = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, continuationTurn.ID)
	if err != nil {
		t.Fatalf("reload continuation turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("continuation turn status = %q, want failed", storedTurn.Status)
	}
	if storedTurn.CompletedAt == nil {
		t.Fatalf("continuation turn completed_at = nil, want set")
	}
	if storedTurn.ErrorMessage == nil || *storedTurn.ErrorMessage == "" {
		t.Fatalf("continuation turn error_message = %v, want non-empty", storedTurn.ErrorMessage)
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	if refreshedInvocation.ErrorCode == nil || *refreshedInvocation.ErrorCode != "stale_turn_recovered" {
		t.Fatalf("invocation error_code = %v, want stale_turn_recovered", refreshedInvocation.ErrorCode)
	}
	if refreshedInvocation.CompletedAt == nil {
		t.Fatalf("invocation completed_at = nil, want set")
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}
	if refreshedAssistant.ErrorMessage == nil || *refreshedAssistant.ErrorMessage == "" {
		t.Fatalf("assistant message error_message = %v, want non-empty", refreshedAssistant.ErrorMessage)
	}

	var (
		jobStatus     string
		requeuedMsgID uuid.UUID
		requeuedSess  uuid.UUID
		retryCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsgID, &requeuedSess, &retryCount); err != nil {
		t.Fatalf("query requeued continuation retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued continuation job status = %q, want pending", jobStatus)
	}
	if requeuedSess != session.ID {
		t.Fatalf("requeued continuation session_id = %s, want %s", requeuedSess, session.ID)
	}
	if requeuedMsgID != rootMessage.ID {
		t.Fatalf("requeued continuation message_id = %s, want %s", requeuedMsgID, rootMessage.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued continuation retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRecoverStaleInProgressTriggeredTurns(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "recover-stale-triggered-turns",
		DisplayName: "Recover Stale Triggered Turns",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Triggered Turn Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You recover leaked triggered turns.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "recover-stale-triggered-project",
		DisplayName:    "Recover Stale Triggered Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "recover-stale-triggered-template",
		DisplayName:    "Recover Stale Triggered Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover stale triggered turn",
		WorkStatus:      "review",
		BlocksScope:     "task",
		FlowTemplateID:  &template.ID,
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
		Status:    "pending",
		Metadata:  json.RawMessage(`{"source":"supervisor","reason":"active execution lost live task turn"}`),
	})
	if err != nil {
		t.Fatalf("create trigger message: %v", err)
	}
	turn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "in_progress",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create triggered turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, &turn.ID); err != nil {
		t.Fatalf("set current turn: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE chat_turn
		SET started_at = now() - interval '1 hour'
		WHERE id = $1
	`, turn.ID); err != nil {
		t.Fatalf("age triggered turn: %v", err)
	}

	provider, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:        "recover-stale-triggered-provider",
		DisplayName: "Recover Stale Triggered Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create model provider: %v", err)
	}
	invocation, err := repo.NewModelInvocationRepo(pool).Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         "test-model",
		AgentID:           &agent.ID,
		ProjectID:         &project.ID,
		ProjectTaskID:     &taskRecord.ID,
		SessionID:         &session.ID,
		TurnID:            &turn.ID,
	})
	if err != nil {
		t.Fatalf("create in-flight invocation: %v", err)
	}
	assistantMessage, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		TurnID:    &turn.ID,
		Role:      "assistant",
		Content:   "",
		Status:    "pending",
	})
	if err != nil {
		t.Fatalf("create pending assistant message: %v", err)
	}

	repaired, err := worker.RecoverStaleInProgressTriggeredTurns(ctx)
	if err != nil {
		t.Fatalf("RecoverStaleInProgressTriggeredTurns: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired triggered turns = %d, want 1", repaired)
	}

	storedTurn, err := repo.NewChatTurnRepo(pool).GetByID(ctx, turn.ID)
	if err != nil {
		t.Fatalf("reload triggered turn: %v", err)
	}
	if storedTurn.Status != "failed" {
		t.Fatalf("triggered turn status = %q, want failed", storedTurn.Status)
	}
	if storedTurn.CompletedAt == nil {
		t.Fatalf("triggered turn completed_at = nil, want set")
	}

	refreshedSession, err := repo.NewChatSessionRepo(pool).GetByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if refreshedSession.CurrentTurnID != nil {
		t.Fatalf("current_turn_id = %v, want nil", *refreshedSession.CurrentTurnID)
	}

	refreshedInvocation, err := repo.NewModelInvocationRepo(pool).GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("reload invocation: %v", err)
	}
	if refreshedInvocation.Status != "failed" {
		t.Fatalf("invocation status = %q, want failed", refreshedInvocation.Status)
	}
	if refreshedInvocation.ErrorCode == nil || *refreshedInvocation.ErrorCode != "stale_turn_recovered" {
		t.Fatalf("invocation error_code = %v, want stale_turn_recovered", refreshedInvocation.ErrorCode)
	}

	refreshedAssistant, err := repo.NewChatMessageRepo(pool).GetByID(ctx, assistantMessage.ID)
	if err != nil {
		t.Fatalf("reload assistant message: %v", err)
	}
	if refreshedAssistant.Status != "failed" {
		t.Fatalf("assistant message status = %q, want failed", refreshedAssistant.Status)
	}

	var (
		jobStatus     string
		requeuedMsgID uuid.UUID
		requeuedSess  uuid.UUID
		retryCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&jobStatus, &requeuedMsgID, &requeuedSess, &retryCount); err != nil {
		t.Fatalf("query requeued triggered retry job: %v", err)
	}
	if jobStatus != "pending" {
		t.Fatalf("requeued triggered job status = %q, want pending", jobStatus)
	}
	if requeuedSess != session.ID {
		t.Fatalf("requeued triggered session_id = %s, want %s", requeuedSess, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued triggered message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued triggered retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerRequeueActiveExecutionSessionsWithoutTurnsForTaskQueueKickoff(t *testing.T) {
	pool := testdb.New(t)
	worker := New(pool, nil, Config{
		PollInterval:         time.Hour,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	ctx := context.Background()
	org, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{
		Slug:        "requeue-active-execution-task-queue-kickoff",
		DisplayName: "Requeue Active Execution Task Queue Kickoff",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	agent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:  org.ID,
		DisplayName:     "Queue Recovery Agent",
		AgentClass:      "staff",
		LifecycleStatus: "active",
		SystemPrompt:    "You resume queued task work.",
		AgentType:       "general",
		CreatedByType:   "system",
		CreatedByID:     uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "requeue-active-execution-task-queue-project",
		DisplayName:    "Requeue Active Execution Task Queue Project",
		Description:    "Project for task-queue wake repair",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	template, err := repo.NewFlowTemplateRepo(pool).Create(ctx, repo.FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "requeue-active-execution-task-queue-template",
		DisplayName:    "Requeue Active Execution Task Queue Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	flowNode, err := repo.NewFlowNodeRepo(pool).Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      1,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}
	task, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  org.ID,
		ProjectID:       project.ID,
		Title:           "Recover task-queue execution kickoff",
		WorkStatus:      "draft",
		BlocksScope:     "task",
		CreatedByType:   "system",
		CreatedByID:     &agent.ID,
		AssignedAgentID: &agent.ID,
		Metadata:        json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project task: %v", err)
	}
	session, err := repo.NewChatSessionRepo(pool).Create(ctx, repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "project_task",
		ScopeID:        task.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.New(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  flowNode.ID,
		VisitNumber: 1,
		Status:      "active",
		SessionID:   &session.ID,
	})
	if err != nil {
		t.Fatalf("create active flow node execution: %v", err)
	}
	metadata := json.RawMessage(fmt.Sprintf(`{"source":"task_queue_processor","flow_node_execution_id":"%s","flow_event_type":"flow.advanced"}`, execution.ID))
	message, err := repo.NewChatMessageRepo(pool).Create(ctx, repo.ChatMessage{
		SessionID: session.ID,
		Role:      "user",
		Content:   "task queue wakeup",
		Status:    "pending",
		Metadata:  metadata,
	})
	if err != nil {
		t.Fatalf("create task queue kickoff message: %v", err)
	}
	completedTurn, err := repo.NewChatTurnRepo(pool).Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agent.ID,
		Status:           "completed",
		TriggerMessageID: &message.ID,
	})
	if err != nil {
		t.Fatalf("create completed task queue turn: %v", err)
	}
	if _, err := repo.NewChatSessionRepo(pool).UpdateCurrentTurn(ctx, session.ID, nil); err != nil {
		t.Fatalf("clear current turn: %v", err)
	}
	if completedTurn.TriggerMessageID == nil || *completedTurn.TriggerMessageID != message.ID {
		t.Fatalf("completed task queue turn trigger_message_id = %v, want %s", completedTurn.TriggerMessageID, message.ID)
	}

	requeued, err := worker.RequeueActiveExecutionSessionsWithoutTurns(ctx)
	if err != nil {
		t.Fatalf("RequeueActiveExecutionSessionsWithoutTurns: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("requeued sessions = %d, want 1", requeued)
	}

	var (
		status         string
		requeuedMsgID  uuid.UUID
		requeuedSessID uuid.UUID
		retryCount     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT status,
		       (payload->>'message_id')::uuid,
		       (payload->>'session_id')::uuid,
		       COALESCE((payload->>'retry_count')::int, 0)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND (payload->>'session_id')::uuid = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, session.ID).Scan(&status, &requeuedMsgID, &requeuedSessID, &retryCount); err != nil {
		t.Fatalf("query requeued task queue execution job: %v", err)
	}
	if status != "pending" {
		t.Fatalf("requeued job status = %q, want pending", status)
	}
	if requeuedSessID != session.ID {
		t.Fatalf("requeued session_id = %s, want %s", requeuedSessID, session.ID)
	}
	if requeuedMsgID != message.ID {
		t.Fatalf("requeued message_id = %s, want %s", requeuedMsgID, message.ID)
	}
	if retryCount != 1 {
		t.Fatalf("requeued retry_count = %d, want 1", retryCount)
	}
}

func TestJobWorkerCancelsClaimedAgentTurnWhenSessionClosesMidExecution(t *testing.T) {
	pool := testdb.New(t)
	org := createOrgForJobQueue(t, pool, "agent-turn-close-mid-execution")
	session, err := repo.NewChatSessionRepo(pool).Create(context.Background(), repo.ChatSession{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "sync",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create chat session: %v", err)
	}

	worker := New(pool, nil, Config{
		WorkerID:             "close-mid-execution",
		PollInterval:         10 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		StaleClaimThreshold:  30 * time.Millisecond,
		CleanupEnqueuePeriod: time.Hour,
	})

	released := make(chan struct{})
	worker.Register(agentTurnJobType, func(ctx context.Context, job Job) error {
		select {
		case <-ctx.Done():
			close(released)
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("timed out waiting for session-close cancellation")
		}
	})

	payload := map[string]any{
		"session_id": session.ID,
		"message_id": uuid.New(),
	}
	jobID, err := worker.Enqueue(context.Background(), nil, agentTurnJobType, 100, payload, nil)
	if err != nil {
		t.Fatalf("enqueue claimed-close agent_turn: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	startWorker(worker, ctx)
	defer func() {
		cancel()
		_ = worker.Stop()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
			t.Fatalf("load claimed job status: %v", err)
		}
		if status == "claimed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := repo.NewChatSessionRepo(pool).Close(context.Background(), session.ID); err != nil {
		t.Fatalf("close claimed session: %v", err)
	}

	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler cancellation after session close")
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status); err != nil {
			t.Fatalf("load finished job status: %v", err)
		}
		if status == "done" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	var status string
	_ = pool.QueryRow(context.Background(), `SELECT status FROM job_queue WHERE id = $1`, jobID).Scan(&status)
	t.Fatalf("job status after session close = %q, want done", status)
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
