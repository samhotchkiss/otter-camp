package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
)

const (
	jobEnqueuedChannel       = "job_enqueued"
	idempotencyCleanupJob    = "idempotency.cleanup"
	agentTurnJobType         = "agent_turn"
	defaultBatchSize         = 10
	defaultPollInterval      = 5 * time.Second
	defaultStaleScanInterval = 60 * time.Second
	defaultStaleThreshold    = 5 * time.Minute
	// Bootstrap turns have their own watchdog budget; stale job recovery should
	// not wait for the generic five-minute claim threshold before retrying them.
	projectBootstrapStaleThreshold = 2 * time.Minute
	defaultCleanupInterval         = 24 * time.Hour

	agentTurnRateLimitMinBackoff = 30 * time.Second
	agentTurnRateLimitBackoffCap = 30 * time.Minute
	agentTurnRateLimitMaxRetries = 6
)

type JobWorker interface {
	Start(ctx context.Context) error
	Stop() error
	Register(jobType string, handler JobHandler)
}

type JobHandler func(ctx context.Context, job Job) error

type agentTurnKeyPayload struct {
	SessionID  uuid.UUID `json:"session_id"`
	MessageID  uuid.UUID `json:"message_id"`
	RetryCount int       `json:"retry_count"`
}

type Job struct {
	ID          uuid.UUID
	JobType     string
	Priority    int
	Payload     json.RawMessage
	Status      string
	ClaimedBy   *string
	ClaimedAt   *time.Time
	Attempts    int
	MaxAttempts int
	LastError   *string
	RunAfter    time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Config struct {
	WorkerID             string
	BatchSize            int
	PollInterval         time.Duration
	StaleScanInterval    time.Duration
	StaleClaimThreshold  time.Duration
	CleanupEnqueuePeriod time.Duration
	ListenReconnectDelay time.Duration
	Clock                clock.Clock
}

type Worker struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	workerID             string
	batchSize            int
	pollInterval         time.Duration
	staleScanInterval    time.Duration
	staleClaimThreshold  time.Duration
	cleanupEnqueuePeriod time.Duration
	listenReconnectDelay time.Duration
	clock                clock.Clock

	handlersMu sync.RWMutex
	handlers   map[string]JobHandler

	stateMu sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

type queryExecutor interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func New(pool *pgxpool.Pool, logger *slog.Logger, cfg Config) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = buildWorkerID()
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.StaleScanInterval <= 0 {
		cfg.StaleScanInterval = defaultStaleScanInterval
	}
	if cfg.StaleClaimThreshold <= 0 {
		cfg.StaleClaimThreshold = defaultStaleThreshold
	}
	if cfg.CleanupEnqueuePeriod <= 0 {
		cfg.CleanupEnqueuePeriod = defaultCleanupInterval
	}
	if cfg.ListenReconnectDelay <= 0 {
		cfg.ListenReconnectDelay = time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}

	w := &Worker{
		pool:                 pool,
		logger:               logger,
		workerID:             cfg.WorkerID,
		batchSize:            cfg.BatchSize,
		pollInterval:         cfg.PollInterval,
		staleScanInterval:    cfg.StaleScanInterval,
		staleClaimThreshold:  cfg.StaleClaimThreshold,
		cleanupEnqueuePeriod: cfg.CleanupEnqueuePeriod,
		listenReconnectDelay: cfg.ListenReconnectDelay,
		clock:                cfg.Clock,
		handlers:             make(map[string]JobHandler),
	}

	w.Register(idempotencyCleanupJob, w.idempotencyCleanupHandler)
	return w
}

func (w *Worker) Start(ctx context.Context) error {
	if w == nil || w.pool == nil {
		return fmt.Errorf("job worker is not configured")
	}

	w.stateMu.Lock()
	if w.running {
		w.stateMu.Unlock()
		return fmt.Errorf("job worker already running")
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	w.running = true
	w.cancel = cancel
	w.done = done
	w.stateMu.Unlock()

	defer func() {
		w.stateMu.Lock()
		w.running = false
		w.cancel = nil
		close(done)
		w.stateMu.Unlock()
	}()

	wake := make(chan struct{}, 1)
	var bg sync.WaitGroup
	bg.Add(3)
	go func() {
		defer bg.Done()
		w.listenForEnqueue(runCtx, wake)
	}()
	go func() {
		defer bg.Done()
		w.runStaleClaimRecovery(runCtx)
	}()
	go func() {
		defer bg.Done()
		w.runCleanupEnqueueLoop(runCtx)
	}()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	signal(wake)

	for {
		select {
		case <-runCtx.Done():
			bg.Wait()
			return nil
		case <-wake:
			if err := w.processAvailableJobs(runCtx); err != nil && runCtx.Err() == nil {
				w.logger.Error("job processing failed", "error", err)
			}
		case <-ticker.C:
			if err := w.processAvailableJobs(runCtx); err != nil && runCtx.Err() == nil {
				w.logger.Error("job poll failed", "error", err)
			}
		}
	}
}

func (w *Worker) Stop() error {
	w.stateMu.Lock()
	running := w.running
	cancel := w.cancel
	done := w.done
	w.stateMu.Unlock()

	if !running {
		return nil
	}

	cancel()
	if done != nil {
		<-done
	}
	return nil
}

func (w *Worker) Register(jobType string, handler JobHandler) {
	name := strings.TrimSpace(jobType)
	if name == "" || handler == nil {
		return
	}

	w.handlersMu.Lock()
	w.handlers[name] = handler
	w.handlersMu.Unlock()
}

func AgentTurnAttemptKey(sessionID, messageID uuid.UUID, retryCount int) string {
	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return ""
	}
	if retryCount < 0 {
		retryCount = 0
	}
	return fmt.Sprintf("%s:%s:%s:%d", agentTurnJobType, sessionID, messageID, retryCount)
}

func AgentTurnGroupKey(sessionID, messageID uuid.UUID) string {
	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s", agentTurnJobType, sessionID, messageID)
}

func (w *Worker) Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	if strings.TrimSpace(jobType) == "" {
		return uuid.Nil, fmt.Errorf("job_type is required")
	}
	if priority <= 0 {
		priority = 100
	}

	payloadJSON, err := marshalPayload(payload)
	if err != nil {
		return uuid.Nil, err
	}

	when := w.clock.Now().UTC()
	if runAfter != nil {
		when = runAfter.UTC()
	}

	if tx != nil {
		return w.enqueueWithExecutor(ctx, tx, jobType, priority, payloadJSON, when)
	}

	started, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin enqueue transaction: %w", err)
	}
	defer func() {
		_ = started.Rollback(ctx)
	}()

	jobID, err := w.enqueueWithExecutor(ctx, started, jobType, priority, payloadJSON, when)
	if err != nil {
		return uuid.Nil, err
	}

	if err := started.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit enqueue transaction: %w", err)
	}

	return jobID, nil
}

func (w *Worker) CancelGroup(ctx context.Context, tx pgx.Tx, groupKey, reason string) (int64, error) {
	groupKey = strings.TrimSpace(groupKey)
	if groupKey == "" {
		return 0, nil
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "cancelled queued agent_turn dispatch"
	}

	var executor queryExecutor
	if tx != nil {
		executor = tx
	} else {
		executor = w.pool
	}

	tag, err := executor.Exec(ctx, `
		UPDATE job_queue
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = $2,
		    updated_at = now()
		WHERE group_key = $1
		  AND status IN ('pending', 'claimed')
	`, groupKey, reason)
	if err != nil {
		return 0, fmt.Errorf("cancel queued job group: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeStaleAgentTurnJobs dead-letters agent_turn jobs whose sessions are
// closed or archived. This prevents wasting LLM calls on stale turns after
// a worker restart and immediately clears closed-session claimed jobs left
// behind by a dead worker.
func (w *Worker) PurgeStaleAgentTurnJobs(ctx context.Context) (int64, error) {
	// Condition 1: session is closed/archived
	ct1, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged at worker startup: session closed',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1 FROM chat_session cs
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.status IN ('closed', 'archived')
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (closed sessions): %w", err)
	}

	// Condition 2: the triggering message was injected by supervisor recovery.
	// These agent_turn jobs were created as part of supervisor recovery cascade
	// and will just waste LLM calls repeating "resume task" on stale sessions.
	ct2, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged at worker startup: supervisor recovery message',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1 FROM chat_message cm
		    WHERE cm.id = (jq.payload->>'message_id')::uuid
		      AND cm.metadata->>'source' = 'supervisor'
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (supervisor messages): %w", err)
	}

	total := ct1.RowsAffected() + ct2.RowsAffected()
	return total, nil
}

func (w *Worker) RecoverStaleClaims(ctx context.Context) (int64, error) {
	staleBefore := w.clock.Now().UTC().Add(-w.staleClaimThreshold)
	bootstrapStaleBefore := w.clock.Now().UTC().Add(-projectBootstrapStaleThreshold)
	ct, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = CASE WHEN attempts < max_attempts THEN 'pending' ELSE 'dead_letter' END,
		    claimed_by = NULL,
		    claimed_at = NULL,
		    run_after = CASE WHEN attempts < max_attempts THEN now() ELSE run_after END,
		    last_error = CASE
		        WHEN attempts < max_attempts THEN last_error
		        ELSE COALESCE(last_error, 'stale claim exceeded max attempts')
		    END,
		    updated_at = now()
		WHERE status = 'claimed'
		  AND (
			claimed_at < $1
			OR (
				job_type = $2
				AND claimed_at < $3
				AND EXISTS (
					SELECT 1
					FROM chat_session cs
					WHERE cs.id = (job_queue.payload->>'session_id')::uuid
					  AND cs.scope_type = 'project'
					  AND cs.status = 'active'
					  AND COALESCE(NULLIF(cs.metadata->'project_bootstrap'->>'status', ''), '') = 'active'
				)
			)
		  )
	`, staleBefore, agentTurnJobType, bootstrapStaleBefore)
	if err != nil {
		return 0, fmt.Errorf("recover stale claims: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) processAvailableJobs(ctx context.Context) error {
	for {
		w.logger.Debug("job queue: claiming pending jobs")
		jobs, err := w.claimPendingLimit(ctx, 1)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				w.logger.Error("job queue: claim failed", "error", err)
			}
			return err
		}
		if len(jobs) == 0 {
			w.logger.Debug("job queue: no pending jobs")
			return nil
		}
		types := make([]string, len(jobs))
		for i, j := range jobs {
			types[i] = j.JobType
		}
		w.logger.Info("job queue: claimed", "count", len(jobs), "types", strings.Join(types, ","))

		for _, job := range jobs {
			w.logger.Info("job queue: executing", "job_id", job.ID, "job_type", job.JobType, "attempts", job.Attempts)
			if err := w.executeClaimedJob(ctx, job); err != nil {
				if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
					w.logger.Error("failed to execute claimed job", "job_id", job.ID, "job_type", job.JobType, "error", err)
				}
			}
		}
	}
}

func (w *Worker) claimPending(ctx context.Context) ([]Job, error) {
	return w.claimPendingLimit(ctx, w.batchSize)
}

func (w *Worker) claimPendingLimit(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := w.pool.Query(ctx, `
		WITH claimable AS (
			SELECT id
			FROM job_queue
			WHERE status = 'pending'
			  AND run_after <= now()
			ORDER BY priority DESC, run_after ASC, created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE job_queue jq
		SET status = 'claimed',
		    claimed_by = $2,
		    claimed_at = now(),
		    attempts = jq.attempts + 1,
		    updated_at = now()
		FROM claimable
		WHERE jq.id = claimable.id
		RETURNING jq.id, jq.job_type, jq.priority, jq.payload, jq.status, jq.claimed_by, jq.claimed_at,
		          jq.attempts, jq.max_attempts, jq.last_error, jq.run_after, jq.created_at, jq.updated_at
	`, limit, w.workerID)
	if err != nil {
		return nil, fmt.Errorf("claim pending jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if rows.Err() != nil {
		return nil, fmt.Errorf("iterate claimed jobs: %w", rows.Err())
	}

	return jobs, nil
}

func (w *Worker) executeClaimedJob(ctx context.Context, job Job) error {
	handler := w.handlerFor(job.JobType)
	if handler == nil {
		return w.markFailure(ctx, job, fmt.Errorf("no handler registered for %q", job.JobType))
	}

	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.maintainClaim(heartbeatCtx, job)
	}()

	err := callJobHandler(ctx, handler, job)
	stopHeartbeat()
	<-heartbeatDone
	if err == nil {
		return w.markDone(ctx, job.ID)
	}
	return w.markFailure(ctx, job, err)
}

func (w *Worker) maintainClaim(ctx context.Context, job Job) {
	interval := w.claimHeartbeatInterval(job)
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := w.touchClaim(ctx, job.ID)
			if err != nil {
				if ctx.Err() == nil {
					w.logger.Warn("job queue: claim heartbeat failed", "job_id", job.ID, "job_type", job.JobType, "error", err)
				}
				return
			}
			if !ok {
				return
			}
		}
	}
}

func (w *Worker) claimHeartbeatInterval(job Job) time.Duration {
	threshold := w.staleClaimThreshold
	if strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) && projectBootstrapStaleThreshold < threshold {
		threshold = projectBootstrapStaleThreshold
	}
	if threshold <= 0 {
		return 0
	}

	interval := threshold / 3
	if interval < 10*time.Millisecond {
		return 10 * time.Millisecond
	}
	return interval
}

func (w *Worker) touchClaim(ctx context.Context, jobID uuid.UUID) (bool, error) {
	tag, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET claimed_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND status = 'claimed'
		  AND claimed_by = $2
	`, jobID, w.workerID)
	if err != nil {
		return false, fmt.Errorf("touch claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (w *Worker) markDone(ctx context.Context, id uuid.UUID) error {
	_, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'done',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    updated_at = now()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("mark job done: %w", err)
	}
	return nil
}

func (w *Worker) markFailure(ctx context.Context, job Job, jobErr error) error {
	errText := strings.TrimSpace(jobErr.Error())
	if errText == "" {
		errText = "unknown job failure"
	}

	retryAfterHint, rateLimited := rateLimitRetryAfter(jobErr)
	retryDelay := backoffDelay(job.Attempts)
	retryMaxAttempts := retryAttemptLimit(job, rateLimited)
	if strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) && rateLimited {
		retryDelay = agentTurnRateLimitDelay(job.Attempts, retryAfterHint)
		w.logger.Info(
			"job queue: scheduling rate-limited retry",
			"job_id", job.ID,
			"job_type", job.JobType,
			"attempt", job.Attempts,
			"max_attempts", retryMaxAttempts,
			"backoff", retryDelay.String(),
			"retry_after", retryAfterHint.String(),
		)
	}

	if job.Attempts < retryMaxAttempts {
		runAfter := w.clock.Now().UTC().Add(retryDelay)
		_, err := w.pool.Exec(ctx, `
			UPDATE job_queue
			SET status = 'pending',
			    claimed_by = NULL,
			    claimed_at = NULL,
			    run_after = $2,
			    last_error = $3,
			    updated_at = now()
			WHERE id = $1
		`, job.ID, runAfter, errText)
		if err != nil {
			return fmt.Errorf("mark job pending: %w", err)
		}
		return nil
	}

	_, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = $2,
		    updated_at = now()
		WHERE id = $1
	`, job.ID, errText)
	if err != nil {
		return fmt.Errorf("mark job dead_letter: %w", err)
	}
	return nil
}

func (w *Worker) enqueueWithExecutor(
	ctx context.Context,
	executor queryExecutor,
	jobType string,
	priority int,
	payload json.RawMessage,
	runAfter time.Time,
) (uuid.UUID, error) {
	dedupeKey, groupKey := deriveJobKeys(strings.TrimSpace(jobType), payload)
	var (
		id        uuid.UUID
		createdAt time.Time
		updatedAt time.Time
	)
	err := executor.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, priority, payload, run_after, dedupe_key, group_key)
		VALUES ($1, $2, $3::jsonb, $4, NULLIF($5, ''), NULLIF($6, ''))
		ON CONFLICT (dedupe_key)
		WHERE dedupe_key IS NOT NULL
		  AND status IN ('pending', 'claimed')
		DO UPDATE
		SET priority = GREATEST(job_queue.priority, EXCLUDED.priority),
		    run_after = LEAST(job_queue.run_after, EXCLUDED.run_after),
		    updated_at = now()
		RETURNING id, created_at, updated_at
	`, jobType, priority, payload, runAfter, dedupeKey, groupKey).Scan(&id, &createdAt, &updatedAt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert job: %w", err)
	}

	if dedupeKey != "" && !createdAt.Equal(updatedAt) {
		w.logger.Info(
			"job queue: suppressed duplicate active dispatch",
			"job_id", id,
			"job_type", jobType,
			"dedupe_key", dedupeKey,
			"group_key", groupKey,
		)
		metrics.RecordAgentTurnDispatchSuppressed("duplicate_enqueue")
	}

	if _, err := executor.Exec(ctx, `SELECT pg_notify($1, $2)`, jobEnqueuedChannel, id.String()); err != nil {
		return uuid.Nil, fmt.Errorf("notify job enqueue: %w", err)
	}

	return id, nil
}

func deriveJobKeys(jobType string, payload json.RawMessage) (string, string) {
	if strings.TrimSpace(jobType) != agentTurnJobType {
		return "", ""
	}
	var parsed agentTurnKeyPayload
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return "", ""
	}
	return AgentTurnAttemptKey(parsed.SessionID, parsed.MessageID, parsed.RetryCount), AgentTurnGroupKey(parsed.SessionID, parsed.MessageID)
}

func (w *Worker) listenForEnqueue(ctx context.Context, wake chan<- struct{}) {
	for {
		if ctx.Err() != nil {
			return
		}

		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Error("acquire job listen connection failed", "error", err)
				sleepContext(ctx, w.listenReconnectDelay)
			}
			continue
		}

		pgConn := conn.Conn()
		if _, err := pgConn.Exec(ctx, `LISTEN `+jobEnqueuedChannel); err != nil {
			conn.Release()
			if ctx.Err() == nil {
				w.logger.Error("job LISTEN failed", "error", err)
				sleepContext(ctx, w.listenReconnectDelay)
			}
			continue
		}
		// Drain any jobs that were enqueued before LISTEN was fully established.
		signal(wake)

		for {
			_, err := pgConn.WaitForNotification(ctx)
			if err != nil {
				break
			}
			signal(wake)
		}

		conn.Release()
		if ctx.Err() == nil {
			sleepContext(ctx, w.listenReconnectDelay)
		}
	}
}

func (w *Worker) runStaleClaimRecovery(ctx context.Context) {
	ticker := time.NewTicker(w.staleScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.RecoverStaleClaims(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale claim recovery failed", "error", err)
			}
		}
	}
}

func (w *Worker) runCleanupEnqueueLoop(ctx context.Context) {
	ticker := time.NewTicker(w.cleanupEnqueuePeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.Enqueue(ctx, nil, idempotencyCleanupJob, 200, map[string]any{"source": "timer"}, nil); err != nil && ctx.Err() == nil {
				w.logger.Error("enqueue idempotency cleanup failed", "error", err)
			}
		}
	}
}

func (w *Worker) idempotencyCleanupHandler(ctx context.Context, _ Job) error {
	for {
		ct, err := w.pool.Exec(ctx, `
			DELETE FROM idempotency_key
			WHERE id IN (
				SELECT id
				FROM idempotency_key
				WHERE expires_at < $1
				ORDER BY expires_at ASC
				LIMIT 1000
			)
		`, w.clock.Now().UTC())
		if err != nil {
			return fmt.Errorf("delete expired idempotency keys: %w", err)
		}

		if ct.RowsAffected() < 1000 {
			return nil
		}
	}
}

func (w *Worker) handlerFor(jobType string) JobHandler {
	w.handlersMu.RLock()
	handler := w.handlers[jobType]
	w.handlersMu.RUnlock()
	return handler
}

func scanJob(row pgx.Row) (Job, error) {
	var job Job
	if err := row.Scan(
		&job.ID,
		&job.JobType,
		&job.Priority,
		&job.Payload,
		&job.Status,
		&job.ClaimedBy,
		&job.ClaimedAt,
		&job.Attempts,
		&job.MaxAttempts,
		&job.LastError,
		&job.RunAfter,
		&job.CreatedAt,
		&job.UpdatedAt,
	); err != nil {
		return Job{}, fmt.Errorf("scan job: %w", err)
	}

	if len(job.Payload) == 0 {
		job.Payload = json.RawMessage(`{}`)
	}
	return job, nil
}

func marshalPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal job payload: %w", err)
	}
	if len(encoded) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(encoded), nil
}

func callJobHandler(ctx context.Context, handler JobHandler, job Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("job handler panic: %v\n%s", recovered, string(debug.Stack()))
		}
	}()
	return handler(ctx, job)
}

func backoffDelay(attempts int) time.Duration {
	if attempts <= 1 {
		return time.Second
	}

	delay := time.Second
	for i := 1; i < attempts; i++ {
		if delay >= (5 * time.Minute / 2) {
			return 5 * time.Minute
		}
		delay *= 2
	}

	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

type rateLimitRetryAfterProvider interface {
	RateLimitRetryAfter() time.Duration
}

func rateLimitRetryAfter(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}

	var provider rateLimitRetryAfterProvider
	if errors.As(err, &provider) {
		retryAfter := provider.RateLimitRetryAfter()
		if retryAfter < 0 {
			return 0, true
		}
		return retryAfter, true
	}

	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(text, "rate limit") {
		return 0, true
	}
	return 0, false
}

func retryAttemptLimit(job Job, rateLimited bool) int {
	maxAttempts := job.MaxAttempts
	if rateLimited && strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) && maxAttempts < agentTurnRateLimitMaxRetries {
		return agentTurnRateLimitMaxRetries
	}
	return maxAttempts
}

func agentTurnRateLimitDelay(attempts int, retryAfterHint time.Duration) time.Duration {
	if attempts <= 1 {
		attempts = 1
	}

	delay := agentTurnRateLimitMinBackoff
	for i := 1; i < attempts; i++ {
		if delay >= (agentTurnRateLimitBackoffCap / 2) {
			delay = agentTurnRateLimitBackoffCap
			break
		}
		delay *= 2
	}

	if retryAfterHint > delay {
		return retryAfterHint
	}
	return delay
}

func buildWorkerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), uuid.NewString())
}

func signal(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func sleepContext(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
