package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/metrics"
)

const (
	jobEnqueuedChannel                    = "job_enqueued"
	idempotencyCleanupJob                 = "idempotency.cleanup"
	agentTurnJobType                      = "agent_turn"
	rollupUpdateJobType                   = "rollup_update"
	modelUsageRollupDailyJobType          = "model_usage_rollup_daily"
	retentionEnforceJobType               = "retention_enforce"
	traceSpanPartitionCreateJobType       = "trace_span_partition_create"
	defaultBatchSize                      = 10
	defaultPollInterval                   = 5 * time.Second
	defaultStaleScanInterval              = 60 * time.Second
	defaultStaleThreshold                 = 5 * time.Minute
	staleContinuationThreshold            = 15 * time.Minute
	startupForeignAgentTurnClaimThreshold = 30 * time.Second
	// Bootstrap turns have their own watchdog budget; stale job recovery should
	// not wait for the generic five-minute claim threshold before retrying them.
	projectBootstrapStaleThreshold = 2 * time.Minute
	defaultCleanupInterval         = 24 * time.Hour

	agentTurnRateLimitMinBackoff = 30 * time.Second
	agentTurnRateLimitBackoffCap = 30 * time.Minute
	agentTurnRateLimitMaxRetries = 6
	legacyRateLimitJitterFloor   = 5 * time.Minute
	legacyRateLimitJitterMax     = 30 * time.Second
)

func staleTriggeredTurnThreshold(scopeType string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(scopeType), "project_task") {
		return defaultStaleThreshold
	}
	return staleContinuationThreshold
}

func staleContinuationThresholdForScope(scopeType string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(scopeType), "project_task") {
		return defaultStaleThreshold
	}
	return staleContinuationThreshold
}

type JobWorker interface {
	Start(ctx context.Context) error
	Stop() error
	Register(jobType string, handler JobHandler)
}

type JobHandler func(ctx context.Context, job Job) error

type agentTurnKeyPayload struct {
	SessionID              uuid.UUID  `json:"session_id"`
	MessageID              uuid.UUID  `json:"message_id"`
	RetryCount             int        `json:"retry_count"`
	FlowNodeExecutionID    *uuid.UUID `json:"flow_node_execution_id,omitempty"`
	RateLimitJitterApplied bool       `json:"rate_limit_jitter_applied,omitempty"`
}

type chatSummarizeKeyPayload struct {
	SessionID uuid.UUID `json:"session_id"`
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
	slots   chan struct{}

	agentTurnInFlight   atomic.Int32
	backgroundInFlight  atomic.Int32
	maintenanceInFlight atomic.Int32
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
		slots:                make(chan struct{}, cfg.BatchSize),
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
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		if released, err := w.releaseClaimsForWorker(releaseCtx); err != nil {
			w.logger.Warn("job queue: release claims on worker stop failed", "worker_id", w.workerID, "error", err)
		} else if released > 0 {
			w.logger.Info("job queue: released claimed jobs on worker stop", "worker_id", w.workerID, "count", released)
		}
		w.stateMu.Lock()
		w.running = false
		w.cancel = nil
		close(done)
		w.stateMu.Unlock()
	}()

	if recovered, err := w.RecoverStaleClaims(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale claim recovery failed", "error", err)
		}
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered stale claims on startup", "count", recovered)
	}
	if repaired, err := w.CloseTerminalProjectTaskAsyncSessions(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup terminal project_task session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed terminal project_task async sessions on startup", "count", repaired)
	}
	if purged, err := w.PurgeStaleAgentTurnJobs(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale agent_turn purge failed", "error", err)
		}
	} else if purged > 0 {
		w.logger.Info("job queue: purged stale agent_turn jobs on startup", "count", purged)
	}
	if repaired, err := w.CloseSupersededCanonicalAsyncSessions(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup task session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed superseded canonical async sessions on startup", "count", repaired)
	}
	if repaired, err := w.CloseArchivedProjectAsyncSessions(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup archived project session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed archived project async sessions on startup", "count", repaired)
	}
	if repaired, err := w.CloseOrphanedProjectTaskAsyncSessions(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup orphaned project_task session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed orphaned project_task async sessions on startup", "count", repaired)
	}
	if repaired, err := w.ClearInactiveSessionCurrentTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup inactive session current turn cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: cleared inactive session current turns on startup", "count", repaired)
	}
	if repaired, err := w.ClearCompletedSessionCurrentTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup completed session current turn cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: cleared completed session current turns on startup", "count", repaired)
	}
	if repaired, err := w.BackfillCancelledTurnStopReasons(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup cancelled turn stop_reason backfill failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: backfilled cancelled turn stop reasons on startup", "count", repaired)
	}
	if repaired, err := w.FailStaleModelInvocations(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale model invocation cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: failed stale model invocations on startup", "count", repaired)
	}
	if repaired, err := w.RecoverStaleInProgressContinuationTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale continuation recovery failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: recovered stale in-progress continuation turns on startup", "count", repaired)
	}
	if requeued, err := w.RequeueStrandedSupervisorRecoveryTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup supervisor recovery requeue failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued stranded supervisor recovery turns on startup", "count", requeued)
	}
	if requeued, err := w.RequeueStrandedUserMessageTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup user message requeue failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued stranded user message turns on startup", "count", requeued)
	}
	if requeued, err := w.RequeuePendingTurnsWithoutJobs(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup pending turn requeue failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued pending turns without jobs on startup", "count", requeued)
	}
	if requeued, err := w.RequeueActiveExecutionSessionsWithoutTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup active execution requeue failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued active execution sessions without turns on startup", "count", requeued)
	}
	if rejittered, err := w.RejitterPendingRateLimitedAgentTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup rate-limit retry rejitter failed", "error", err)
		}
	} else if rejittered > 0 {
		w.logger.Info("job queue: rejittered pending rate-limited agent turns on startup", "count", rejittered)
	}

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

func (w *Worker) enqueueAgentTurnDispatch(ctx context.Context, tx pgx.Tx, payload agentTurnKeyPayload, runAfter *time.Time) (uuid.UUID, error) {
	if payload.FlowNodeExecutionID == nil && payload.SessionID != uuid.Nil {
		executionID, err := w.lookupActiveFlowExecutionForSession(ctx, tx, payload.SessionID)
		if err != nil {
			return uuid.Nil, err
		}
		payload.FlowNodeExecutionID = executionID
	}
	return w.Enqueue(ctx, tx, agentTurnJobType, 70, payload, runAfter)
}

func (w *Worker) lookupActiveFlowExecutionForSession(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (*uuid.UUID, error) {
	if sessionID == uuid.Nil {
		return nil, nil
	}
	var executor queryExecutor = w.pool
	if tx != nil {
		executor = tx
	}
	var executionID uuid.UUID
	if err := executor.QueryRow(ctx, `
		SELECT id
		FROM flow_node_execution
		WHERE session_id = $1
		  AND status = 'active'
		ORDER BY started_at DESC, id DESC
		LIMIT 1
	`, sessionID).Scan(&executionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load active flow execution for session %s: %w", sessionID, err)
	}
	return &executionID, nil
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
	// Keep the job if it is still the live backing dispatch for the session's
	// current pending turn.
	ct2, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged at worker startup: supervisor recovery message',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = 'agent_turn'
		  AND COALESCE((jq.payload->>'retry_count')::int, 0) = 0
		  AND EXISTS (
		    SELECT 1 FROM chat_message cm
		    WHERE cm.id = (jq.payload->>'message_id')::uuid
		      AND cm.metadata->>'source' = 'supervisor'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN chat_turn ct ON ct.id = cs.current_turn_id
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND ct.status = 'pending'
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (supervisor messages): %w", err)
	}

	// Condition 3: active async project bootstrap sessions with no live turn
	// should have at most one pending bootstrap continuation. Older queued
	// follow-ons are superseded once a newer continuation message exists.
	ct3, err := w.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT jq.id,
			       ROW_NUMBER() OVER (
			           PARTITION BY jq.payload->>'session_id'
			           ORDER BY jq.run_after DESC, jq.created_at DESC, jq.id DESC
			       ) AS rn
			FROM job_queue jq
			JOIN chat_session cs ON cs.id = (jq.payload->>'session_id')::uuid
			LEFT JOIN chat_turn current_turn ON current_turn.id = cs.current_turn_id
			JOIN chat_message cm ON cm.id = (jq.payload->>'message_id')::uuid
			WHERE jq.status = 'pending'
			  AND jq.job_type = 'agent_turn'
			  AND cs.scope_type = 'project'
			  AND cs.mode = 'async'
			  AND cs.status = 'active'
			  AND (
			    cs.current_turn_id IS NULL
			    OR current_turn.id IS NULL
			    OR current_turn.status NOT IN ('pending', 'in_progress')
			  )
			  AND COALESCE(cm.metadata->>'source', '') = 'project_bootstrap'
		)
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged at worker startup: superseded bootstrap continuation',
		    updated_at = now()
		FROM ranked
		WHERE jq.id = ranked.id
		  AND ranked.rn > 1
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (superseded bootstrap continuations): %w", err)
	}

	// Condition 4: active async project-task sessions with no live turn should
	// also have at most one pending continuation. Prompt compression and retry
	// recovery can leave stacked pending jobs for superseded continuation
	// messages; keep only the newest pending dispatch for the session.
	ct4, err := w.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT jq.id,
			       ROW_NUMBER() OVER (
			           PARTITION BY jq.payload->>'session_id'
			           ORDER BY jq.created_at DESC, jq.run_after DESC, jq.id DESC
			       ) AS rn
			FROM job_queue jq
			JOIN chat_session cs ON cs.id = (jq.payload->>'session_id')::uuid
			WHERE jq.status = 'pending'
			  AND jq.job_type = 'agent_turn'
			  AND cs.scope_type = 'project_task'
			  AND cs.mode = 'async'
			  AND cs.status = 'active'
			  AND (
			    NOT EXISTS (
			    	SELECT 1
			    	FROM chat_turn current_turn
			    	WHERE current_turn.id = cs.current_turn_id
			    	  AND current_turn.status IN ('pending', 'in_progress')
			    )
			    AND NOT EXISTS (
			    	SELECT 1
			    	FROM flow_node_execution e
			    	JOIN chat_turn live_turn ON live_turn.id = CASE
			    		WHEN COALESCE(e.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			    		THEN (e.metadata->>'live_turn_id')::uuid
			    		ELSE NULL
			    	END
			    	WHERE e.session_id = cs.id
			    	  AND e.status = 'active'
			    	  AND live_turn.status IN ('pending', 'in_progress')
			    )
			  )
		)
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged stale project_task continuation',
		    updated_at = now()
		FROM ranked
		WHERE jq.id = ranked.id
		  AND ranked.rn > 1
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (superseded project_task continuations): %w", err)
	}

	// Condition 5: if the exact session/message/retry attempt already has a
	// terminal turn recorded, any leftover pending dispatch job for that same
	// attempt is stale and should not consume execution slots.
	ct5, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged stale terminal message-attempt dispatch',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		      AND ct.status IN ('completed', 'cancelled', 'failed')
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (terminal message attempts): %w", err)
	}

	// Condition 6: if the exact session/message/retry attempt already has a
	// live pending/in-progress turn recorded, any extra queued dispatch job
	// for that same attempt is also stale. Keep the live turn; drop the
	// duplicate queue entry before it burns a worker slot on should_run=false.
	ct6, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged duplicate live message-attempt dispatch',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		      AND ct.status IN ('pending', 'in_progress')
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (live message attempts): %w", err)
	}

	// Condition 7: synthetic continuation/recovery prompts should retain only
	// one live dispatch per session/source. Older pending copies are
	// superseded once a newer pending/claimed synthetic dispatch exists.
	ct7, err := w.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT jq.id,
			       jq.status,
			       ROW_NUMBER() OVER (
			           PARTITION BY jq.payload->>'session_id', COALESCE(cm.metadata->>'source', '')
			           ORDER BY CASE WHEN jq.status = 'claimed' THEN 0 ELSE 1 END,
			                    jq.created_at DESC,
			                    jq.id DESC
			       ) AS rn
			FROM job_queue jq
			JOIN chat_message cm ON cm.id = (jq.payload->>'message_id')::uuid
			WHERE jq.status IN ('pending', 'claimed')
			  AND jq.job_type = 'agent_turn'
			  AND cm.role = 'user'
			  AND COALESCE(cm.metadata->>'synthetic_user_message', 'false') = 'true'
			  AND COALESCE(cm.metadata->>'source', '') <> ''
		)
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged duplicate synthetic continuation dispatch',
		    updated_at = now()
		FROM ranked
		WHERE jq.id = ranked.id
		  AND ranked.status = 'pending'
		  AND ranked.rn > 1
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (duplicate synthetic dispatches): %w", err)
	}

	// Condition 8: legacy task-lane dispatches with no execution identity are
	// stale once the bound session already has an active execution with a
	// newer live turn. Keep execution-owned dispatches; drop only pre-rework
	// queue rows that no longer represent the live lane.
	ct8, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged stale legacy task dispatch without execution ownership',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = 'agent_turn'
		  AND COALESCE(jq.payload->>'flow_node_execution_id', '') = ''
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN flow_node_execution e
		      ON e.session_id = cs.id
		     AND e.status = 'active'
		    JOIN chat_turn live_turn ON live_turn.id = CASE
		      WHEN COALESCE(e.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
		      THEN (e.metadata->>'live_turn_id')::uuid
		      ELSE NULL
		    END
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project_task'
		      AND cs.status = 'active'
		      AND (
		        live_turn.trigger_message_id IS DISTINCT FROM (jq.payload->>'message_id')::uuid
		        OR live_turn.retry_count <> COALESCE((jq.payload->>'retry_count')::int, 0)
		      )
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (legacy task dispatches): %w", err)
	}

	total := ct1.RowsAffected() + ct2.RowsAffected() + ct3.RowsAffected() + ct4.RowsAffected() + ct5.RowsAffected() + ct6.RowsAffected() + ct7.RowsAffected() + ct8.RowsAffected()
	return total, nil
}

func (w *Worker) RequeueStrandedSupervisorRecoveryTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT DISTINCT cs.id, cm.id
		FROM chat_session cs
		JOIN project_task pt ON pt.id = cs.scope_id
		JOIN project p ON p.id = pt.project_id
		LEFT JOIN LATERAL (
			SELECT e.metadata
			FROM flow_node_execution e
			WHERE e.session_id = cs.id
			  AND e.status = 'active'
			ORDER BY e.started_at DESC, e.id DESC
			LIMIT 1
		) execution_owner ON true
		LEFT JOIN chat_turn ct ON ct.id = cs.current_turn_id
		LEFT JOIN chat_turn live_turn ON live_turn.id = CASE
			WHEN COALESCE(execution_owner.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (execution_owner.metadata->>'live_turn_id')::uuid
			ELSE NULL
		END
		JOIN chat_message cm ON cm.id = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		LEFT JOIN model_invocation mi ON mi.turn_id = COALESCE(live_turn.id, ct.id)
		WHERE cs.scope_type = 'project_task'
		  AND cs.status = 'active'
		  AND p.status = 'active'
		  AND pt.work_status <> 'blocked'
		  AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		  AND COALESCE(live_turn.status, ct.status, '') = 'pending'
		  AND COALESCE(cm.metadata->>'source', '') = 'supervisor'
		  AND mi.id IS NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = 'agent_turn'
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		      AND (jq.payload->>'message_id')::uuid = cm.id
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("list stranded supervisor recovery turns: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			return repaired, fmt.Errorf("scan stranded supervisor recovery turn: %w", err)
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
			SessionID:  sessionID,
			MessageID:  messageID,
			RetryCount: 0,
		}, nil); err != nil {
			return repaired, fmt.Errorf("requeue stranded supervisor recovery turn for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate stranded supervisor recovery turns: %w", err)
	}
	return repaired, nil
}

func (w *Worker) RequeueStrandedUserMessageTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT DISTINCT ON (cs.id) cs.id, cm.id
		FROM chat_session cs
		JOIN chat_message cm ON cm.session_id = cs.id
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND cm.role = 'user'
		  AND COALESCE(cm.metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_message newer
		    WHERE newer.session_id = cs.id
		      AND (
		        newer.created_at > cm.created_at
		        OR (newer.created_at = cm.created_at AND newer.id > cm.id)
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.status IN ('pending', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.trigger_message_id = cm.id
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $1
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		  )
		ORDER BY cs.id, cm.created_at DESC, cm.id DESC
	`, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list stranded user message turns: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			return repaired, fmt.Errorf("scan stranded user message turn: %w", err)
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
			SessionID:  sessionID,
			MessageID:  messageID,
			RetryCount: 0,
		}, nil); err != nil {
			return repaired, fmt.Errorf("requeue stranded user message turn for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate stranded user message turns: %w", err)
	}
	return repaired, nil
}

func (w *Worker) RequeuePendingTurnsWithoutJobs(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT DISTINCT cs.id, CASE
			WHEN COALESCE(live_turn.status, '') = 'pending' THEN live_turn.trigger_message_id
			ELSE ct.trigger_message_id
		END
		FROM chat_session cs
		LEFT JOIN LATERAL (
			SELECT e.metadata
			FROM flow_node_execution e
			WHERE e.session_id = cs.id
			  AND e.status = 'active'
			ORDER BY e.started_at DESC, e.id DESC
			LIMIT 1
		) execution_owner ON cs.scope_type = 'project_task'
		LEFT JOIN chat_turn live_turn ON live_turn.id = CASE
			WHEN COALESCE(execution_owner.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (execution_owner.metadata->>'live_turn_id')::uuid
			ELSE NULL
		END
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		LEFT JOIN project p
		  ON (
		       cs.scope_type = 'project'
		   AND p.id = cs.scope_id
		  )
		  OR (
		       cs.scope_type = 'project_task'
		   AND p.id = pt.project_id
		  )
		LEFT JOIN chat_turn ct ON ct.id = cs.current_turn_id
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND CASE
		    WHEN COALESCE(live_turn.status, '') = 'pending' THEN live_turn.status
		    ELSE ct.status
		  END = 'pending'
		  AND CASE
		    WHEN COALESCE(live_turn.status, '') = 'pending' THEN live_turn.trigger_message_id
		    ELSE ct.trigger_message_id
		  END IS NOT NULL
		  AND (
		    cs.scope_type <> 'project_task'
		    OR COALESCE(pt.work_status, '') <> 'blocked'
		  )
		  AND (
		    cs.scope_type NOT IN ('project', 'project_task')
		    OR (
		      p.id IS NOT NULL
		      AND p.status = 'active'
		      AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		    )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $1
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		  )
	`, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list pending turns without jobs: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		if err := rows.Scan(&sessionID, &messageID); err != nil {
			return repaired, fmt.Errorf("scan pending turn without job: %w", err)
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
			SessionID:  sessionID,
			MessageID:  messageID,
			RetryCount: 0,
		}, nil); err != nil {
			return repaired, fmt.Errorf("requeue pending turn without job for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate pending turns without jobs: %w", err)
	}
	return repaired, nil
}

func (w *Worker) RequeueActiveExecutionSessionsWithoutTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT DISTINCT ON (cs.id) cs.id,
		       cm.id,
		       COALESCE((
		         SELECT MAX(ct.retry_count) + 1
		         FROM chat_turn ct
		         WHERE ct.session_id = cs.id
		           AND ct.trigger_message_id = cm.id
		       ), 0) AS next_retry_count
		FROM chat_session cs
		JOIN flow_node_execution e
		  ON e.session_id = cs.id
		 AND e.status = 'active'
		JOIN chat_message cm
		  ON cm.session_id = cs.id
		WHERE cs.scope_type = 'project_task'
		  AND cs.mode = 'async'
		  AND cs.status = 'active'
		  AND cs.current_turn_id IS NULL
		  AND cm.role = 'user'
		  AND COALESCE(cm.metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		  AND (
		    (
		      cm.content = 'supervisor recovery: resume task'
		      AND COALESCE(cm.metadata->>'source', '') = 'supervisor'
		    )
		    OR (
		      COALESCE(cm.metadata->>'source', '') = 'task_queue_processor'
		      AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
		    )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.status IN ('pending', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn latest
		    WHERE latest.session_id = cs.id
		      AND latest.trigger_message_id = cm.id
		      AND latest.status = 'cancelled'
		      AND NOT EXISTS (
		        SELECT 1
		        FROM chat_turn newer
		        WHERE newer.session_id = latest.session_id
		          AND newer.trigger_message_id = latest.trigger_message_id
		          AND (
		            newer.turn_number > latest.turn_number
		            OR (newer.turn_number = latest.turn_number AND newer.retry_count > latest.retry_count)
		          )
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn halted
		    WHERE halted.session_id = cs.id
		      AND halted.trigger_message_id = cm.id
		      AND halted.status = 'completed'
		      AND COALESCE(halted.stop_reason, '') = 'recovery_content_required'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $1
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		  )
		ORDER BY cs.id, cm.created_at DESC, cm.id DESC
	`, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list active execution sessions without turns: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		var retryCount int
		if err := rows.Scan(&sessionID, &messageID, &retryCount); err != nil {
			return repaired, fmt.Errorf("scan active execution session without turn: %w", err)
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
			SessionID:  sessionID,
			MessageID:  messageID,
			RetryCount: retryCount,
		}, nil); err != nil {
			return repaired, fmt.Errorf("requeue active execution session without turn for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate active execution sessions without turns: %w", err)
	}
	return repaired, nil
}

func (w *Worker) FailStaleModelInvocations(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT mi.id,
		       mi.turn_id,
		       mi.session_id
		FROM model_invocation mi
		WHERE mi.status = 'in_flight'
		  AND (
		    (
		      mi.created_at < $1
		      AND NOT EXISTS (
		        SELECT 1
		        FROM chat_turn ct
		        JOIN chat_session cs ON cs.id = ct.session_id
		        WHERE ct.id = mi.turn_id
		          AND ct.status = 'in_progress'
		          AND cs.status = 'active'
		          AND cs.current_turn_id = ct.id
		      )
		    )
		    OR (
		      mi.created_at < $2
		      AND EXISTS (
		        SELECT 1
		        FROM chat_turn ct
		        JOIN chat_session cs ON cs.id = ct.session_id
		        WHERE ct.id = mi.turn_id
		          AND ct.status = 'in_progress'
		          AND cs.status = 'active'
		          AND cs.current_turn_id = ct.id
		          AND NOT EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'claimed'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		          )
		      )
		    )
		  )
	`, w.clock.Now().UTC().Add(-30*time.Minute), w.clock.Now().UTC().Add(-15*time.Second))
	if err != nil {
		return 0, fmt.Errorf("list stale model invocations: %w", err)
	}
	defer rows.Close()

	type staleInvocationCandidate struct {
		invocationID uuid.UUID
		turnID       *uuid.UUID
		sessionID    *uuid.UUID
	}
	var candidates []staleInvocationCandidate
	for rows.Next() {
		var item staleInvocationCandidate
		if err := rows.Scan(&item.invocationID, &item.turnID, &item.sessionID); err != nil {
			return 0, fmt.Errorf("scan stale model invocation: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale model invocations: %w", err)
	}

	const failureReason = "worker cleanup failed stale in_flight model invocation without live in-progress turn"
	var repaired int64
	for _, item := range candidates {
		ct, err := w.pool.Exec(ctx, `
			UPDATE model_invocation
			SET status = 'failed',
			    failure_class = 'product_runtime',
			    error_code = 'stale_model_invocation',
			    error_message = $2,
			    completed_at = COALESCE(completed_at, now())
			WHERE id = $1
			  AND status = 'in_flight'
		`, item.invocationID, failureReason)
		if err != nil {
			return repaired, fmt.Errorf("fail stale model invocation %s: %w", item.invocationID, err)
		}
		if ct.RowsAffected() == 0 {
			continue
		}
		repaired++
		if item.turnID == nil || *item.turnID == uuid.Nil {
			continue
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_message
			SET status = 'failed',
			    error_message = $2
			WHERE turn_id = $1
			  AND role = 'assistant'
			  AND status IN ('pending', 'streaming')
		`, *item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail assistant messages for stale model invocation turn %s: %w", *item.turnID, err)
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_turn
			SET status = 'failed',
			    error_message = $2,
			    completed_at = COALESCE(completed_at, now())
			WHERE id = $1
			  AND status = 'in_progress'
		`, *item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail stale model invocation turn %s: %w", *item.turnID, err)
		}
		if item.sessionID != nil && *item.sessionID != uuid.Nil {
			if _, err := w.pool.Exec(ctx, `
				UPDATE chat_session
				SET current_turn_id = NULL
				WHERE id = $1
				  AND current_turn_id = $2
			`, *item.sessionID, *item.turnID); err != nil {
				return repaired, fmt.Errorf("clear current turn for stale model invocation session %s: %w", *item.sessionID, err)
			}
		}
	}
	return repaired, nil
}

func (w *Worker) RecoverStaleInProgressContinuationTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT cs.id,
		       COALESCE(live_turn.id, ct.id) AS turn_id,
		       prior.trigger_message_id,
		       COALESCE((
		         SELECT MAX(retry_turn.retry_count)
		         FROM chat_turn retry_turn
		         WHERE retry_turn.session_id = cs.id
		           AND retry_turn.trigger_message_id = prior.trigger_message_id
		       ), 0) + 1 AS next_retry_count
		FROM chat_session cs
		LEFT JOIN chat_turn ct ON ct.id = cs.current_turn_id
		LEFT JOIN LATERAL (
			SELECT e.metadata
			FROM flow_node_execution e
			WHERE e.session_id = cs.id
			  AND e.status = 'active'
			ORDER BY e.started_at DESC, e.id DESC
			LIMIT 1
		) execution_owner ON cs.scope_type = 'project_task'
		LEFT JOIN chat_turn live_turn ON live_turn.id = CASE
			WHEN COALESCE(execution_owner.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (execution_owner.metadata->>'live_turn_id')::uuid
			ELSE NULL
		END
		JOIN LATERAL (
			SELECT prior_turn.trigger_message_id
			FROM chat_turn prior_turn
			WHERE prior_turn.session_id = cs.id
			  AND prior_turn.turn_number < COALESCE(live_turn.turn_number, ct.turn_number)
			  AND prior_turn.trigger_message_id IS NOT NULL
			ORDER BY prior_turn.turn_number DESC
			LIMIT 1
		) prior ON true
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		LEFT JOIN project p
		  ON (
		       cs.scope_type = 'project'
		   AND p.id = cs.scope_id
		  )
		  OR (
		       cs.scope_type = 'project_task'
		   AND p.id = pt.project_id
		  )
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND COALESCE(live_turn.status, ct.status, '') = 'in_progress'
		  AND COALESCE(live_turn.trigger_message_id, ct.trigger_message_id) IS NULL
		  AND COALESCE(live_turn.started_at, ct.started_at) IS NOT NULL
		  AND COALESCE(live_turn.started_at, ct.started_at) < CASE
		    WHEN cs.scope_type = 'project_task' THEN $1::timestamptz
		    ELSE $2::timestamptz
		  END
		  AND (
		    cs.scope_type NOT IN ('project', 'project_task')
		    OR (
		      p.id IS NOT NULL
		      AND p.status = 'active'
		      AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		    )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM run r
		    WHERE r.session_id = cs.id
		      AND r.turn_id = COALESCE(live_turn.id, ct.id)
		      AND r.status IN ('created', 'in_progress')
		  )
	`, w.clock.Now().UTC().Add(-staleContinuationThresholdForScope("project_task")), w.clock.Now().UTC().Add(-staleContinuationThreshold))
	if err != nil {
		return 0, fmt.Errorf("list stale in-progress continuation turns: %w", err)
	}
	defer rows.Close()

	type staleContinuationCandidate struct {
		sessionID  uuid.UUID
		turnID     uuid.UUID
		messageID  uuid.UUID
		retryCount int
	}
	candidates := make([]staleContinuationCandidate, 0)
	for rows.Next() {
		var item staleContinuationCandidate
		if err := rows.Scan(&item.sessionID, &item.turnID, &item.messageID, &item.retryCount); err != nil {
			return 0, fmt.Errorf("scan stale in-progress continuation turn: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale in-progress continuation turns: %w", err)
	}

	const failureReason = "recovered stale in-progress continuation turn without live job or execution; scheduling a fresh retry"
	var repaired int64
	for _, item := range candidates {
		if _, err := w.pool.Exec(ctx, `
			UPDATE model_invocation
			SET status = 'failed',
			    failure_class = 'product_runtime',
			    error_code = 'stale_turn_recovered',
			    error_message = $2,
			    completed_at = COALESCE(completed_at, now())
			WHERE turn_id = $1
			  AND status = 'in_flight'
		`, item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail stale continuation model invocations for turn %s: %w", item.turnID, err)
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_message
			SET status = 'failed',
			    error_message = $2
			WHERE turn_id = $1
			  AND role = 'assistant'
			  AND status IN ('pending', 'streaming')
		`, item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail stale continuation assistant messages for turn %s: %w", item.turnID, err)
		}
		ct, err := w.pool.Exec(ctx, `
			UPDATE chat_turn
			SET status = 'failed',
			    error_message = $2,
			    completed_at = now()
			WHERE id = $1
			  AND status = 'in_progress'
		`, item.turnID, failureReason)
		if err != nil {
			return repaired, fmt.Errorf("fail stale continuation turn %s: %w", item.turnID, err)
		}
		if ct.RowsAffected() == 0 {
			continue
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_session
			SET current_turn_id = NULL
			WHERE id = $1
			  AND current_turn_id = $2
		`, item.sessionID, item.turnID); err != nil {
			return repaired, fmt.Errorf("clear stale continuation current turn for session %s: %w", item.sessionID, err)
		}
		hasQueued, err := w.sessionHasQueuedOrClaimedAgentTurn(ctx, item.sessionID)
		if err != nil {
			return repaired, fmt.Errorf("check queued recovered continuation retry for session %s: %w", item.sessionID, err)
		}
		if !hasQueued {
			if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
				SessionID:  item.sessionID,
				MessageID:  item.messageID,
				RetryCount: item.retryCount,
			}, nil); err != nil {
				return repaired, fmt.Errorf("enqueue recovered stale continuation retry for session %s: %w", item.sessionID, err)
			}
		}
		repaired++
	}
	return repaired, nil
}

func (w *Worker) RecoverStaleInProgressTriggeredTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT cs.id,
		       COALESCE(live_turn.id, ct.id) AS turn_id,
		       cs.scope_type,
		       COALESCE(live_turn.trigger_message_id, ct.trigger_message_id) AS trigger_message_id,
		       COALESCE((
		         SELECT MAX(retry_turn.retry_count)
		         FROM chat_turn retry_turn
		         WHERE retry_turn.session_id = cs.id
		           AND retry_turn.trigger_message_id = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		       ), 0) + 1 AS next_retry_count
		FROM chat_session cs
		LEFT JOIN chat_turn ct ON ct.id = cs.current_turn_id
		LEFT JOIN LATERAL (
			SELECT e.metadata
			FROM flow_node_execution e
			WHERE e.session_id = cs.id
			  AND e.status = 'active'
			ORDER BY e.started_at DESC, e.id DESC
			LIMIT 1
		) execution_owner ON cs.scope_type = 'project_task'
		LEFT JOIN chat_turn live_turn ON live_turn.id = CASE
			WHEN COALESCE(execution_owner.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			THEN (execution_owner.metadata->>'live_turn_id')::uuid
			ELSE NULL
		END
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		LEFT JOIN project p
		  ON (
		       cs.scope_type = 'project'
		   AND p.id = cs.scope_id
		  )
		  OR (
		       cs.scope_type = 'project_task'
		   AND p.id = pt.project_id
		  )
		WHERE cs.mode = 'async'
		  AND cs.status = 'active'
		  AND COALESCE(live_turn.status, ct.status, '') = 'in_progress'
		  AND COALESCE(live_turn.trigger_message_id, ct.trigger_message_id) IS NOT NULL
		  AND COALESCE(live_turn.started_at, ct.started_at) IS NOT NULL
		  AND (
		        (cs.scope_type = 'project_task' AND COALESCE(live_turn.started_at, ct.started_at) < $1)
		     OR (cs.scope_type <> 'project_task' AND COALESCE(live_turn.started_at, ct.started_at) < $2)
		      )
		  AND (
		    cs.scope_type NOT IN ('project', 'project_task')
		    OR (
		      p.id IS NOT NULL
		      AND p.status = 'active'
		      AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		    )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM run r
		    WHERE r.session_id = cs.id
		      AND r.turn_id = COALESCE(live_turn.id, ct.id)
		      AND r.status IN ('created', 'in_progress')
		  )
	`, w.clock.Now().UTC().Add(-defaultStaleThreshold), w.clock.Now().UTC().Add(-staleContinuationThreshold))
	if err != nil {
		return 0, fmt.Errorf("list stale in-progress triggered turns: %w", err)
	}
	defer rows.Close()

	type staleTriggeredCandidate struct {
		sessionID  uuid.UUID
		turnID     uuid.UUID
		scopeType  string
		messageID  uuid.UUID
		retryCount int
	}
	candidates := make([]staleTriggeredCandidate, 0)
	for rows.Next() {
		var item staleTriggeredCandidate
		if err := rows.Scan(&item.sessionID, &item.turnID, &item.scopeType, &item.messageID, &item.retryCount); err != nil {
			return 0, fmt.Errorf("scan stale in-progress triggered turn: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale in-progress triggered turns: %w", err)
	}

	const failureReason = "recovered stale in-progress message turn without live job or execution; scheduling a fresh retry"
	var repaired int64
	for _, item := range candidates {
		if _, err := w.pool.Exec(ctx, `
			UPDATE model_invocation
			SET status = 'failed',
			    failure_class = 'product_runtime',
			    error_code = 'stale_turn_recovered',
			    error_message = $2,
			    completed_at = COALESCE(completed_at, now())
			WHERE turn_id = $1
			  AND status = 'in_flight'
		`, item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail stale triggered-turn model invocations for turn %s: %w", item.turnID, err)
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_message
			SET status = 'failed',
			    error_message = $2
			WHERE turn_id = $1
			  AND role = 'assistant'
			  AND status IN ('pending', 'streaming')
		`, item.turnID, failureReason); err != nil {
			return repaired, fmt.Errorf("fail stale triggered-turn assistant messages for turn %s: %w", item.turnID, err)
		}
		ct, err := w.pool.Exec(ctx, `
			UPDATE chat_turn
			SET status = 'failed',
			    error_message = $2,
			    completed_at = now()
			WHERE id = $1
			  AND status = 'in_progress'
		`, item.turnID, failureReason)
		if err != nil {
			return repaired, fmt.Errorf("fail stale triggered turn %s: %w", item.turnID, err)
		}
		if ct.RowsAffected() == 0 {
			continue
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE chat_session
			SET current_turn_id = NULL
			WHERE id = $1
			  AND current_turn_id = $2
		`, item.sessionID, item.turnID); err != nil {
			return repaired, fmt.Errorf("clear stale triggered current turn for session %s: %w", item.sessionID, err)
		}
		hasQueued, err := w.sessionHasQueuedOrClaimedAgentTurn(ctx, item.sessionID)
		if err != nil {
			return repaired, fmt.Errorf("check queued recovered triggered retry for session %s: %w", item.sessionID, err)
		}
		if !hasQueued {
			if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
				SessionID:  item.sessionID,
				MessageID:  item.messageID,
				RetryCount: item.retryCount,
			}, nil); err != nil {
				return repaired, fmt.Errorf("enqueue recovered stale triggered retry for session %s: %w", item.sessionID, err)
			}
		}
		if w.logger != nil {
			w.logger.Info("job queue: recovered stale in-progress triggered turn",
				"session_id", item.sessionID,
				"turn_id", item.turnID,
				"scope_type", strings.TrimSpace(item.scopeType),
				"threshold", staleTriggeredTurnThreshold(item.scopeType).String(),
			)
		}
		repaired++
	}
	return repaired, nil
}

func (w *Worker) sessionHasQueuedOrClaimedAgentTurn(ctx context.Context, sessionID uuid.UUID) (bool, error) {
	if sessionID == uuid.Nil {
		return false, nil
	}

	executionID, err := w.lookupActiveFlowExecutionForSession(ctx, nil, sessionID)
	if err != nil {
		return false, err
	}
	if executionID != nil && *executionID != uuid.Nil {
		var exists bool
		if err := w.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM job_queue jq
				WHERE jq.job_type = $1
				  AND jq.status IN ('pending', 'claimed')
				  AND (jq.payload->>'session_id')::uuid = $2
				  AND COALESCE(jq.payload->>'flow_node_execution_id', '') = $3
			)
		`, agentTurnJobType, sessionID, executionID.String()).Scan(&exists); err != nil {
			return false, err
		}
		return exists, nil
	}

	var exists bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM job_queue jq
			WHERE jq.job_type = $1
			  AND jq.status IN ('pending', 'claimed')
			  AND (jq.payload->>'session_id')::uuid = $2
		)
	`, agentTurnJobType, sessionID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (w *Worker) RejitterPendingRateLimitedAgentTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT jq.id,
		       (jq.payload->>'session_id')::uuid,
		       (jq.payload->>'message_id')::uuid,
		       COALESCE((jq.payload->>'retry_count')::int, 0),
		       jq.run_after
		FROM job_queue jq
		JOIN chat_session cs ON cs.id = (jq.payload->>'session_id')::uuid
		JOIN LATERAL (
			SELECT cm.content
			FROM chat_message cm
			WHERE cm.session_id = cs.id
			ORDER BY cm.created_at DESC, cm.id DESC
			LIMIT 1
		) latest_msg ON true
		WHERE jq.job_type = 'agent_turn'
		  AND jq.status = 'pending'
		  AND COALESCE((jq.payload->>'retry_count')::int, 0) > 0
		  AND COALESCE(jq.payload->>'rate_limit_jitter_applied', 'false') <> 'true'
		  AND jq.run_after >= now() + interval '5 minutes'
		  AND latest_msg.content LIKE '[Rate limited, retrying in %'
	`)
	if err != nil {
		return 0, fmt.Errorf("list pending rate-limited agent turns for rejitter: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var (
			jobID      uuid.UUID
			sessionID  uuid.UUID
			messageID  uuid.UUID
			retryCount int
			runAfter   time.Time
		)
		if err := rows.Scan(&jobID, &sessionID, &messageID, &retryCount, &runAfter); err != nil {
			return repaired, fmt.Errorf("scan pending rate-limited agent turn: %w", err)
		}
		rejitteredRunAfter := rejitteredRateLimitedRunAfter(w.clock.Now().UTC(), runAfter.UTC(), sessionID, messageID, retryCount)
		tag, err := w.pool.Exec(ctx, `
			UPDATE job_queue
			SET run_after = $2,
			    payload = jsonb_set(payload, '{rate_limit_jitter_applied}', 'true'::jsonb, true),
			    updated_at = now()
			WHERE id = $1
			  AND status = 'pending'
			  AND COALESCE(payload->>'rate_limit_jitter_applied', 'false') <> 'true'
		`, jobID, rejitteredRunAfter)
		if err != nil {
			return repaired, fmt.Errorf("update pending rate-limited agent turn %s: %w", jobID, err)
		}
		repaired += tag.RowsAffected()
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate pending rate-limited agent turns: %w", err)
	}
	return repaired, nil
}

func rejitteredRateLimitedRunAfter(now, runAfter time.Time, sessionID, messageID uuid.UUID, retryCount int) time.Time {
	if runAfter.Sub(now) < legacyRateLimitJitterFloor {
		return runAfter
	}
	return runAfter.Add(rateLimitRetryJitterOffset(sessionID, messageID, retryCount))
}

func rateLimitRetryJitterOffset(sessionID, messageID uuid.UUID, retryCount int) time.Duration {
	if legacyRateLimitJitterMax <= 0 {
		return 0
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write(sessionID[:])
	_, _ = hasher.Write(messageID[:])
	_, _ = hasher.Write([]byte(strconv.Itoa(retryCount)))
	jitterRange := uint64(legacyRateLimitJitterMax / time.Second)
	if jitterRange == 0 {
		return 0
	}
	jitterSeconds := hasher.Sum64() % (jitterRange + 1)
	return time.Duration(jitterSeconds) * time.Second
}

func (w *Worker) RecoverStaleClaims(ctx context.Context) (int64, error) {
	staleBefore := w.clock.Now().UTC().Add(-w.staleClaimThreshold)
	bootstrapStaleBefore := w.clock.Now().UTC().Add(-projectBootstrapStaleThreshold)
	foreignAgentTurnStaleBefore := w.clock.Now().UTC().Add(-startupForeignAgentTurnClaimThreshold)
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
			OR (
				job_type = $2
				AND claimed_by IS NOT NULL
				AND claimed_by <> $4
				AND claimed_at < $5
			)
		  )
	`, staleBefore, agentTurnJobType, bootstrapStaleBefore, w.workerID, foreignAgentTurnStaleBefore)
	if err != nil {
		return 0, fmt.Errorf("recover stale claims: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) CloseSupersededCanonicalAsyncSessions(ctx context.Context) (int64, error) {
	ct, err := w.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id,
			       ROW_NUMBER() OVER (
			           PARTITION BY organization_id, scope_type, scope_id
			           ORDER BY created_at DESC, id DESC
			       ) AS rn
			FROM chat_session
			WHERE scope_type IN ('organization', 'project', 'project_task')
			  AND mode = 'async'
			  AND status = 'active'
		)
		UPDATE chat_session cs
		SET status = 'closed',
		    closed_at = now(),
		    current_turn_id = NULL
		FROM ranked
		WHERE cs.id = ranked.id
		  AND ranked.rn > 1
	`)
	if err != nil {
		return 0, fmt.Errorf("close superseded canonical async sessions: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) CloseArchivedProjectAsyncSessions(ctx context.Context) (int64, error) {
	ct, err := w.pool.Exec(ctx, `
		UPDATE chat_session cs
		SET status = 'closed',
		    closed_at = now(),
		    current_turn_id = NULL
		WHERE cs.status = 'active'
		  AND cs.mode = 'async'
		  AND (
		    (cs.scope_type = 'project' AND EXISTS (
		      SELECT 1
		      FROM project p
		      WHERE p.id = cs.scope_id
		        AND p.status = 'archived'
		    ))
		    OR
		    (cs.scope_type = 'project_task' AND EXISTS (
		      SELECT 1
		      FROM project_task pt
		      JOIN project p ON p.id = pt.project_id
		      WHERE pt.id = cs.scope_id
		        AND p.status = 'archived'
		    ))
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("close archived project async sessions: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) CloseOrphanedProjectTaskAsyncSessions(ctx context.Context) (int64, error) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE chat_turn
		SET status = 'cancelled',
		    cancel_requested_at = now(),
		    completed_at = now(),
		    stop_reason = 'session_closed'
		WHERE session_id IN (
			SELECT cs.id
			FROM chat_session cs
			WHERE cs.status = 'active'
			  AND cs.mode = 'async'
			  AND cs.scope_type = 'project_task'
			  AND NOT EXISTS (
				SELECT 1
				FROM project_task pt
				WHERE pt.id = cs.scope_id
			  )
		)
		  AND status IN ('pending', 'in_progress')
	`); err != nil {
		return 0, fmt.Errorf("cancel orphaned project_task session turns: %w", err)
	}

	ct, err := w.pool.Exec(ctx, `
		UPDATE chat_session cs
		SET status = 'closed',
		    closed_at = now(),
		    current_turn_id = NULL
		WHERE cs.status = 'active'
		  AND cs.mode = 'async'
		  AND cs.scope_type = 'project_task'
		  AND NOT EXISTS (
			SELECT 1
			FROM project_task pt
			WHERE pt.id = cs.scope_id
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("close orphaned project_task async sessions: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) CloseTerminalProjectTaskAsyncSessions(ctx context.Context) (int64, error) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE chat_turn
		SET status = 'cancelled',
		    cancel_requested_at = now(),
		    completed_at = now(),
		    stop_reason = 'session_closed'
		WHERE session_id IN (
			SELECT cs.id
			FROM chat_session cs
			JOIN project_task pt ON pt.id = cs.scope_id
			WHERE cs.status = 'active'
			  AND cs.mode = 'async'
			  AND cs.scope_type = 'project_task'
			  AND pt.work_status IN ('done', 'cancelled')
		)
		  AND status IN ('pending', 'in_progress')
	`); err != nil {
		return 0, fmt.Errorf("cancel terminal project_task session turns: %w", err)
	}

	ct, err := w.pool.Exec(ctx, `
		UPDATE chat_session cs
		SET status = 'closed',
		    closed_at = now(),
		    current_turn_id = NULL
		FROM project_task pt
		WHERE cs.status = 'active'
		  AND cs.mode = 'async'
		  AND cs.scope_type = 'project_task'
		  AND pt.id = cs.scope_id
		  AND pt.work_status IN ('done', 'cancelled')
	`)
	if err != nil {
		return 0, fmt.Errorf("close terminal project_task async sessions: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) ClearInactiveSessionCurrentTurns(ctx context.Context) (int64, error) {
	if _, err := w.pool.Exec(ctx, `
		UPDATE chat_turn
		SET status = 'cancelled',
		    cancel_requested_at = now(),
		    completed_at = now(),
		    stop_reason = 'session_closed'
		WHERE session_id IN (
			SELECT id
			FROM chat_session
			WHERE status IN ('closed', 'archived')
		)
		  AND status IN ('pending', 'in_progress')
	`); err != nil {
		return 0, fmt.Errorf("cancel inactive session turns: %w", err)
	}

	ct, err := w.pool.Exec(ctx, `
		UPDATE chat_session
		SET current_turn_id = NULL
		WHERE status IN ('closed', 'archived')
		  AND current_turn_id IS NOT NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("clear inactive session current turns: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) ClearCompletedSessionCurrentTurns(ctx context.Context) (int64, error) {
	ct, err := w.pool.Exec(ctx, `
		UPDATE chat_session cs
		SET current_turn_id = NULL
		FROM chat_turn ct
		WHERE cs.current_turn_id = ct.id
		  AND cs.status = 'active'
		  AND cs.mode = 'async'
		  AND ct.status NOT IN ('pending', 'in_progress')
	`)
	if err != nil {
		return 0, fmt.Errorf("clear completed session current turns: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) BackfillCancelledTurnStopReasons(ctx context.Context) (int64, error) {
	ct1, err := w.pool.Exec(ctx, `
		WITH mapped AS (
			SELECT DISTINCT ON (de.payload->>'turn_id')
			       (de.payload->>'turn_id')::uuid AS turn_id,
			       CASE de.payload->>'reason'
			           WHEN 'cancel_current_turn' THEN 'user_cancelled'
			           WHEN 'user_cancelled' THEN 'user_cancelled'
			           WHEN 'steer_turn' THEN 'user_steered'
			           WHEN 'session_closed' THEN 'session_closed'
			           ELSE NULL
			       END AS stop_reason
			FROM domain_event de
			WHERE de.event_type = 'chat.turn.cancelled'
			  AND de.payload ? 'turn_id'
			ORDER BY de.payload->>'turn_id', de.created_at DESC, de.id DESC
		)
		UPDATE chat_turn t
		SET stop_reason = mapped.stop_reason
		FROM mapped
		WHERE t.id = mapped.turn_id
		  AND t.status = 'cancelled'
		  AND t.stop_reason IS NULL
		  AND mapped.stop_reason IS NOT NULL
	`)
	if err != nil {
		return 0, fmt.Errorf("backfill cancelled turn stop reasons: %w", err)
	}

	ct2, err := w.pool.Exec(ctx, `
		UPDATE chat_turn t
		SET stop_reason = 'superseded_live_turn'
		WHERE t.status = 'cancelled'
		  AND t.stop_reason IS NULL
		  AND EXISTS (
			SELECT 1
			FROM chat_turn later
			WHERE later.session_id = t.session_id
			  AND later.turn_number > t.turn_number
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM domain_event de
			WHERE de.event_type = 'chat.turn.cancelled'
			  AND de.payload->>'turn_id' = t.id::text
			  AND de.payload ? 'reason'
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("backfill superseded cancelled turns: %w", err)
	}

	return ct1.RowsAffected() + ct2.RowsAffected(), nil
}

func (w *Worker) processAvailableJobs(ctx context.Context) error {
	if recovered, err := w.RecoverStaleClaims(ctx); err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("job queue: inline stale claim recovery failed", "error", err)
		}
		return err
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered stale claims before claim", "count", recovered)
	}

	for {
		slots := w.availableExecutionSlots()
		if slots > 0 {
			w.logger.Debug("job queue: claiming pending jobs", "slots", slots, "inflight", w.inflightJobs())
			claimedAny := false

			agentJobs, err := w.claimPendingAgentTurns(ctx, slots)
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
					w.logger.Error("job queue: claim failed", "job_class", "agent_turn", "error", err)
				}
				return err
			}
			if len(agentJobs) > 0 {
				claimedAny = true
				w.logClaimedJobs(agentJobs)
				w.launchClaimedJobs(ctx, agentJobs)
				continue
			}

			backgroundSlots := w.availableExecutionSlots()
			if w.agentTurnInFlight.Load() > 0 {
				backgroundSlots = min(backgroundSlots, w.availableBackgroundSlots())
			}
			if backgroundSlots > 0 {
				backgroundJobs, err := w.claimPendingNonMaintenanceJobs(ctx, backgroundSlots)
				if err != nil {
					if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
						w.logger.Error("job queue: claim failed", "job_class", "background_non_maintenance", "error", err)
					}
					return err
				}
				if len(backgroundJobs) > 0 {
					claimedAny = true
					w.logClaimedJobs(backgroundJobs)
					w.launchClaimedJobs(ctx, backgroundJobs)
					continue
				}

				maintenanceSlots := min(backgroundSlots, w.availableMaintenanceSlots())
				if maintenanceSlots <= 0 {
					if !claimedAny {
						w.logger.Debug("job queue: no pending jobs")
						return nil
					}
					continue
				}
				maintenanceJobs, err := w.claimPendingMaintenanceJobs(ctx, maintenanceSlots)
				if err != nil {
					if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
						w.logger.Error("job queue: claim failed", "job_class", "background_maintenance", "error", err)
					}
					return err
				}
				if len(maintenanceJobs) > 0 {
					claimedAny = true
					w.logClaimedJobs(maintenanceJobs)
					w.launchClaimedJobs(ctx, maintenanceJobs)
					continue
				}
			}

			if !claimedAny {
				w.logger.Debug("job queue: no pending jobs")
				return nil
			}
		}
		w.logger.Debug("job queue: no execution slots available", "inflight", w.inflightJobs(), "capacity", cap(w.slots))
		return nil
	}
}

func (w *Worker) logClaimedJobs(jobs []Job) {
	if len(jobs) == 0 {
		return
	}
	types := make([]string, len(jobs))
	for i, j := range jobs {
		types[i] = j.JobType
	}
	w.logger.Info("job queue: claimed", "count", len(jobs), "types", strings.Join(types, ","))
}

func (w *Worker) launchClaimedJobs(ctx context.Context, jobs []Job) {
	for _, job := range jobs {
		job := job
		w.acquireExecutionSlot(job.JobType)
		go func() {
			defer func() {
				w.releaseExecutionSlot(job.JobType)
			}()
			w.logger.Info("job queue: executing", "job_id", job.ID, "job_type", job.JobType, "attempts", job.Attempts)
			if err := w.executeClaimedJob(ctx, job); err != nil {
				if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
					w.logger.Error("failed to execute claimed job", "job_id", job.ID, "job_type", job.JobType, "error", err)
				}
			}
		}()
	}
}

func (w *Worker) availableExecutionSlots() int {
	if w == nil || w.batchSize <= 0 {
		return 1
	}
	inflight := w.inflightJobs()
	slots := max(1, w.batchSize) - inflight
	if slots < 0 {
		return 0
	}
	return slots
}

func (w *Worker) maxBackgroundInFlight() int {
	if w == nil || w.batchSize <= 1 {
		return 1
	}
	return max(1, w.batchSize/3)
}

func (w *Worker) availableBackgroundSlots() int {
	background := int(w.backgroundInFlight.Load())
	remaining := w.maxBackgroundInFlight() - background
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (w *Worker) maxMaintenanceInFlight() int {
	if w == nil || w.batchSize <= 1 {
		return 1
	}
	return max(1, w.batchSize-1)
}

func (w *Worker) availableMaintenanceSlots() int {
	maintenance := int(w.maintenanceInFlight.Load())
	remaining := w.maxMaintenanceInFlight() - maintenance
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (w *Worker) inflightJobs() int {
	if w == nil || w.slots == nil {
		return 0
	}
	return len(w.slots)
}

func (w *Worker) acquireExecutionSlot(jobType string) {
	if w == nil || w.slots == nil {
		return
	}
	w.slots <- struct{}{}
	if strings.EqualFold(strings.TrimSpace(jobType), agentTurnJobType) {
		w.agentTurnInFlight.Add(1)
		return
	}
	w.backgroundInFlight.Add(1)
	if isMaintenanceJobType(jobType) {
		w.maintenanceInFlight.Add(1)
	}
}

func (w *Worker) releaseExecutionSlot(jobType string) {
	if w == nil || w.slots == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(jobType), agentTurnJobType) {
		w.agentTurnInFlight.Add(-1)
	} else {
		w.backgroundInFlight.Add(-1)
		if isMaintenanceJobType(jobType) {
			w.maintenanceInFlight.Add(-1)
		}
	}
	select {
	case <-w.slots:
	default:
	}
}

func (w *Worker) claimPending(ctx context.Context) ([]Job, error) {
	return w.claimPendingLimit(ctx, w.batchSize)
}

func (w *Worker) claimPendingLimit(ctx context.Context, limit int) ([]Job, error) {
	return w.claimPendingByFilter(ctx, limit, "")
}

func (w *Worker) claimPendingAgentTurns(ctx context.Context, limit int) ([]Job, error) {
	return w.claimPendingByFilter(ctx, limit, "job_type = $3")
}

func (w *Worker) claimPendingNonAgentJobs(ctx context.Context, limit int) ([]Job, error) {
	return w.claimPendingByFilter(ctx, limit, "job_type <> $3")
}

func (w *Worker) claimPendingNonMaintenanceJobs(ctx context.Context, limit int) ([]Job, error) {
	return w.claimPendingByFilter(ctx, limit, nonMaintenanceBackgroundJobFilter())
}

func (w *Worker) claimPendingMaintenanceJobs(ctx context.Context, limit int) ([]Job, error) {
	return w.claimPendingByFilter(ctx, limit, maintenanceBackgroundJobFilter())
}

func maintenanceBackgroundJobFilter() string {
	return fmt.Sprintf(
		"job_type <> $3 AND job_type IN ('%s', '%s', '%s', '%s')",
		rollupUpdateJobType,
		modelUsageRollupDailyJobType,
		retentionEnforceJobType,
		traceSpanPartitionCreateJobType,
	)
}

func isMaintenanceJobType(jobType string) bool {
	switch strings.TrimSpace(jobType) {
	case rollupUpdateJobType, modelUsageRollupDailyJobType, retentionEnforceJobType, traceSpanPartitionCreateJobType:
		return true
	default:
		return false
	}
}

func nonMaintenanceBackgroundJobFilter() string {
	return fmt.Sprintf(
		"job_type <> $3 AND job_type NOT IN ('%s', '%s', '%s', '%s')",
		rollupUpdateJobType,
		modelUsageRollupDailyJobType,
		retentionEnforceJobType,
		traceSpanPartitionCreateJobType,
	)
}

func (w *Worker) claimPendingByFilter(ctx context.Context, limit int, filter string) ([]Job, error) {
	if limit <= 0 {
		limit = 1
	}
	whereFilter := ""
	args := []any{limit, w.workerID}
	if strings.TrimSpace(filter) != "" {
		whereFilter = "AND " + filter
		args = append(args, agentTurnJobType)
	}
	rows, err := w.pool.Query(ctx, fmt.Sprintf(`
		WITH ranked AS (
			SELECT id,
			       job_type,
			       priority,
			       run_after,
			       created_at,
			       ROW_NUMBER() OVER (
			           PARTITION BY CASE
			               WHEN job_type = '%s' THEN COALESCE(payload->>'session_id', id::text)
			               ELSE id::text
			           END
			           ORDER BY priority DESC, run_after ASC, created_at ASC, id ASC
			       ) AS session_rn
			FROM job_queue
			WHERE status = 'pending'
			  AND run_after <= now()
			  AND (
			    job_type <> '%s'
			    OR EXISTS (
			      SELECT 1
			      FROM chat_session cs
			      WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			        AND cs.status = 'active'
			    )
			  )
			  AND (
			    job_type <> '%s'
			    OR NOT EXISTS (
			      SELECT 1
			      FROM chat_turn ct
			      WHERE ct.session_id = (job_queue.payload->>'session_id')::uuid
			        AND ct.trigger_message_id = (job_queue.payload->>'message_id')::uuid
			        AND ct.retry_count = COALESCE((job_queue.payload->>'retry_count')::int, 0)
			        AND ct.status <> 'pending'
			    )
			  )
			  AND (
			    job_type <> '%s'
			    OR NOT EXISTS (
			      SELECT 1
			      FROM chat_session cs
			      JOIN chat_turn current_turn ON current_turn.id = cs.current_turn_id
			      WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			        AND (
			          cs.scope_type <> 'project_task'
			          OR EXISTS (
			            SELECT 1
			            FROM flow_node_execution e
			            WHERE e.session_id = cs.id
			              AND e.status = 'active'
			          )
			        )
			        AND (
			          current_turn.status = 'in_progress'
			          OR (
			            current_turn.status = 'pending'
			            AND (
			              current_turn.trigger_message_id IS DISTINCT FROM (job_queue.payload->>'message_id')::uuid
			              OR current_turn.retry_count <> COALESCE((job_queue.payload->>'retry_count')::int, 0)
			            )
			          )
			        )
			    )
			  )
			  %s
		),
		claimable AS (
			SELECT id
			FROM ranked
			WHERE session_rn = 1
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
	`, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, whereFilter), args...)
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

	jobCtx, cancelJob := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		w.maintainClaim(jobCtx, job, cancelJob)
	}()

	err := callJobHandler(jobCtx, handler, job)
	cancelJob()
	<-heartbeatDone
	if err == nil {
		return w.markDone(ctx, job.ID)
	}
	return w.markFailure(ctx, job, err)
}

func (w *Worker) maintainClaim(ctx context.Context, job Job, cancelExecution context.CancelFunc) {
	heartbeatInterval := w.claimHeartbeatInterval(job)
	if heartbeatInterval <= 0 {
		return
	}
	interval := heartbeatInterval
	if strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) && interval > 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastHeartbeat := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if closed, err := w.agentTurnSessionClosed(ctx, job); err != nil {
				if ctx.Err() == nil {
					w.logger.Warn("job queue: closed-session monitor failed", "job_id", job.ID, "job_type", job.JobType, "error", err)
				}
				return
			} else if closed {
				w.logger.Info("job queue: cancelling claimed agent_turn for closed session", "job_id", job.ID, "job_type", job.JobType)
				cancelExecution()
				return
			}
			if !lastHeartbeat.IsZero() && time.Since(lastHeartbeat) < heartbeatInterval {
				continue
			}
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
			lastHeartbeat = time.Now()
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

func (w *Worker) agentTurnSessionClosed(ctx context.Context, job Job) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) {
		return false, nil
	}
	var payload agentTurnKeyPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return false, fmt.Errorf("decode %s payload for session monitor: %w", agentTurnJobType, err)
	}
	if payload.SessionID == uuid.Nil {
		return false, nil
	}
	var status string
	if err := w.pool.QueryRow(ctx, `SELECT status FROM chat_session WHERE id = $1`, payload.SessionID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, fmt.Errorf("load chat session for claimed agent_turn: %w", err)
	}
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "closed" || status == "archived", nil
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

func (w *Worker) releaseClaimsForWorker(ctx context.Context) (int64, error) {
	if w == nil || w.pool == nil || strings.TrimSpace(w.workerID) == "" {
		return 0, nil
	}
	tag, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = CASE WHEN attempts < max_attempts THEN 'pending' ELSE 'dead_letter' END,
		    claimed_by = NULL,
		    claimed_at = NULL,
		    run_after = CASE WHEN attempts < max_attempts THEN now() ELSE run_after END,
		    last_error = CASE
		        WHEN attempts < max_attempts THEN COALESCE(NULLIF(last_error, ''), 'released on worker shutdown')
		        ELSE COALESCE(NULLIF(last_error, ''), 'worker shutdown released final claimed attempt')
		    END,
		    updated_at = now()
		WHERE status = 'claimed'
		  AND claimed_by = $1
	`, w.workerID)
	if err != nil {
		return 0, fmt.Errorf("release worker claims: %w", err)
	}
	return tag.RowsAffected(), nil
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
	query := `
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
	`
	args := []any{jobType, priority, payload, runAfter, dedupeKey, groupKey}
	if jobType == agentTurnJobType && groupKey != "" {
		query = `
			WITH pending_ranked AS (
				SELECT id,
				       ROW_NUMBER() OVER (
				           ORDER BY run_after DESC, created_at DESC, id DESC
				       ) AS rn
				FROM job_queue
				WHERE group_key = $1
				  AND status = 'pending'
			),
			pruned AS (
				UPDATE job_queue jq
				SET status = 'dead_letter',
				    last_error = 'superseded queued agent_turn dispatch',
				    updated_at = now()
				FROM pending_ranked ranked
				WHERE jq.id = ranked.id
				  AND ranked.rn > 1
			),
			updated AS (
				UPDATE job_queue jq
				SET priority = GREATEST(jq.priority, $3),
				    payload = $4::jsonb,
				    run_after = $5,
				    dedupe_key = NULLIF($6, ''),
				    updated_at = now()
				FROM pending_ranked ranked
				WHERE jq.id = ranked.id
				  AND ranked.rn = 1
				RETURNING jq.id, jq.created_at, jq.updated_at
			),
			inserted AS (
				INSERT INTO job_queue (job_type, priority, payload, run_after, dedupe_key, group_key)
				SELECT $2, $3, $4::jsonb, $5, NULLIF($6, ''), NULLIF($1, '')
				WHERE NOT EXISTS (SELECT 1 FROM updated)
				ON CONFLICT (dedupe_key)
				WHERE dedupe_key IS NOT NULL
				  AND status IN ('pending', 'claimed')
				DO UPDATE
				SET priority = GREATEST(job_queue.priority, EXCLUDED.priority),
				    run_after = LEAST(job_queue.run_after, EXCLUDED.run_after),
				    updated_at = now()
				RETURNING id, created_at, updated_at
			)
			SELECT id, created_at, updated_at FROM updated
			UNION ALL
			SELECT id, created_at, updated_at FROM inserted
			LIMIT 1
		`
		args = []any{groupKey, jobType, priority, payload, runAfter, dedupeKey}
	}
	err := executor.QueryRow(ctx, query, args...).Scan(&id, &createdAt, &updatedAt)
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
	switch strings.TrimSpace(jobType) {
	case agentTurnJobType:
		var parsed agentTurnKeyPayload
		if err := json.Unmarshal(payload, &parsed); err != nil {
			return "", ""
		}
		return AgentTurnAttemptKey(parsed.SessionID, parsed.MessageID, parsed.RetryCount), AgentTurnGroupKey(parsed.SessionID, parsed.MessageID)
	case "chat_summarize":
		var parsed chatSummarizeKeyPayload
		if err := json.Unmarshal(payload, &parsed); err != nil {
			return "", ""
		}
		if parsed.SessionID == uuid.Nil {
			return "", ""
		}
		key := fmt.Sprintf("chat_summarize:%s", parsed.SessionID)
		return key, key
	case "rollup_update":
		var parsed struct {
			OrgID      uuid.UUID `json:"org_id"`
			RollupDate string    `json:"rollup_date"`
		}
		if err := json.Unmarshal(payload, &parsed); err != nil {
			return "", ""
		}
		if parsed.OrgID == uuid.Nil || strings.TrimSpace(parsed.RollupDate) == "" {
			return "", ""
		}
		key := fmt.Sprintf("rollup_update:%s:%s", parsed.OrgID, strings.TrimSpace(parsed.RollupDate))
		return key, key
	default:
		return "", ""
	}
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
			if purged, err := w.PurgeStaleAgentTurnJobs(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale agent_turn purge failed", "error", err)
			} else if purged > 0 {
				w.logger.Info("job queue: purged stale agent_turn jobs", "count", purged)
			}
			if repaired, err := w.ClearCompletedSessionCurrentTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("completed session current turn cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: cleared completed session current turns", "count", repaired)
			}
			if repaired, err := w.CloseTerminalProjectTaskAsyncSessions(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("terminal project_task session cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: closed terminal project_task async sessions", "count", repaired)
			}
			if repaired, err := w.FailStaleModelInvocations(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale model invocation cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: failed stale model invocations", "count", repaired)
			}
			if requeued, err := w.RequeuePendingTurnsWithoutJobs(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("pending turn repair failed", "error", err)
			} else if requeued > 0 {
				w.logger.Info("job queue: requeued pending turns without jobs", "count", requeued)
			}
			if repaired, err := w.RecoverStaleInProgressContinuationTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale continuation recovery failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: recovered stale in-progress continuation turns", "count", repaired)
			}
			if repaired, err := w.RecoverStaleInProgressTriggeredTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale triggered-turn recovery failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: recovered stale in-progress triggered turns", "count", repaired)
			}
			if requeued, err := w.RequeueActiveExecutionSessionsWithoutTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("active execution session repair failed", "error", err)
			} else if requeued > 0 {
				w.logger.Info("job queue: requeued active execution sessions without turns", "count", requeued)
			}
			if rejittered, err := w.RejitterPendingRateLimitedAgentTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("rate-limit retry rejitter failed", "error", err)
			} else if rejittered > 0 {
				w.logger.Info("job queue: rejittered pending rate-limited agent turns", "count", rejittered)
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
