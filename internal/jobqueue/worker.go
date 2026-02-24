package jobqueue

import (
	"context"
	"encoding/json"
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
)

const (
	jobEnqueuedChannel       = "job_enqueued"
	idempotencyCleanupJob    = "idempotency.cleanup"
	defaultBatchSize         = 10
	defaultPollInterval      = 5 * time.Second
	defaultStaleScanInterval = 60 * time.Second
	defaultStaleThreshold    = 5 * time.Minute
	defaultCleanupInterval   = 24 * time.Hour
)

type JobWorker interface {
	Start(ctx context.Context) error
	Stop() error
	Register(jobType string, handler JobHandler)
}

type JobHandler func(ctx context.Context, job Job) error

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

	when := time.Now().UTC()
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

func (w *Worker) RecoverStaleClaims(ctx context.Context) (int64, error) {
	staleBefore := time.Now().UTC().Add(-w.staleClaimThreshold)
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
		  AND claimed_at < $1
	`, staleBefore)
	if err != nil {
		return 0, fmt.Errorf("recover stale claims: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) processAvailableJobs(ctx context.Context) error {
	for {
		jobs, err := w.claimPending(ctx)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}

		for _, job := range jobs {
			if err := w.executeClaimedJob(ctx, job); err != nil {
				w.logger.Error("failed to execute claimed job", "job_id", job.ID, "job_type", job.JobType, "error", err)
			}
		}
	}
}

func (w *Worker) claimPending(ctx context.Context) ([]Job, error) {
	rows, err := w.pool.Query(ctx, `
		WITH claimable AS (
			SELECT id
			FROM job_queue
			WHERE status = 'pending'
			  AND run_after <= now()
			ORDER BY priority ASC, run_after ASC
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
	`, w.batchSize, w.workerID)
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

	err := callJobHandler(ctx, handler, job)
	if err == nil {
		return w.markDone(ctx, job.ID)
	}
	return w.markFailure(ctx, job, err)
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

	if job.Attempts < job.MaxAttempts {
		runAfter := time.Now().UTC().Add(backoffDelay(job.Attempts))
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
	var id uuid.UUID
	err := executor.QueryRow(ctx, `
		INSERT INTO job_queue (job_type, priority, payload, run_after)
		VALUES ($1, $2, $3::jsonb, $4)
		RETURNING id
	`, jobType, priority, payload, runAfter).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert job: %w", err)
	}

	if _, err := executor.Exec(ctx, `SELECT pg_notify($1, $2)`, jobEnqueuedChannel, id.String()); err != nil {
		return uuid.Nil, fmt.Errorf("notify job enqueue: %w", err)
	}

	return id, nil
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
				WHERE expires_at < now()
				ORDER BY expires_at ASC
				LIMIT 1000
			)
		`)
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
