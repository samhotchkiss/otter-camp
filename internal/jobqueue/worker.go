package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
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
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	versionpkg "github.com/samhotchkiss/otter-camp/internal/version"
)

const (
	jobEnqueuedChannel                    = "job_enqueued"
	idempotencyCleanupJob                 = "idempotency.cleanup"
	agentTurnJobType                      = "agent_turn"
	memoryExtractTurnJobType              = "memory_extract_turn"
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
	postModelOrphanTurnThreshold   = 30 * time.Second
	claimedAgentTurnHeartbeatGrace = 30 * time.Second
	slowProjectAsyncModelThreshold = 20 * time.Minute
	overtakenLocalProjectThreshold = 8 * time.Minute

	agentTurnRateLimitMinBackoff    = 30 * time.Second
	agentTurnRateLimitBackoffCap    = 30 * time.Minute
	agentTurnRateLimitMaxRetries    = 6
	agentTurnTransientMinBackoff    = 15 * time.Second
	agentTurnTransientBackoffCap    = 5 * time.Minute
	legacyRateLimitJitterFloor      = 5 * time.Minute
	legacyRateLimitJitterMax        = 30 * time.Second
	maxInFlightProjectContinuations = 4

	projectContinuationSnapshotFingerprintKey  = "continuation_snapshot_fingerprint"
	projectContinuationRediscoveryGuardPrefix  = "[Project continuation rediscovery guard blocked only broad rereads."
	projectContinuationActiveReplacementPrefix = "[Project continuation found that prerequisite artifact `"
	projectContinuationActiveReplacementMarker = "already has active replacement work in the tree:"
	projectContinuationBoundedSizePrefix       = "[Project continuation found that the remaining draft work still violates the bounded size policy."
	projectContinuationDraftBoundedSizePrefix  = "[Project continuation found remaining draft "
	projectContinuationBoundedSizeMarker       = "violates the bounded size policy:"
	projectContinuationSuppressedErrorMessage  = "suppressed repeated identical project continuation after repeated validation block"
)

var explicitDeliverablePathPatternsForWorker = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:deliverable|output):\s*([^\s,;]+)`),
	regexp.MustCompile(`(?i)\boutput\b[^.;:\n]{0,80}?\s+(?:at|to)\s+([^\s,;]+)`),
	regexp.MustCompile(`(?i)\bsave\s+as\s+([^\s,;]+)`),
}

var projectContinuationBatchRangePatternForWorker = regexp.MustCompile(`(?i)\bposts?\s+(\d{1,3})\s*[–-]\s*(\d{1,3})\b`)

var preferredDeliverableRootPatternsForWorker = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:under|in)\s+/?((?:content(?:/[A-Za-z0-9._-]+)+)/?)`),
}

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

func staleModelInvocationThresholdForProjectModel(modelName string) time.Duration {
	lowerModel := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(lowerModel, "qwen"),
		strings.Contains(lowerModel, "mistral"),
		strings.Contains(lowerModel, "llama"),
		strings.Contains(lowerModel, "gemma"),
		strings.Contains(lowerModel, "deepseek"):
		return slowProjectAsyncModelThreshold
	default:
		return staleContinuationThreshold
	}
}

func (w *Worker) maxExecutionSessionRecoveryBatch() int {
	if w == nil || w.batchSize <= 1 {
		return 1
	}
	return max(1, min(2, w.batchSize/4))
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
	startupAt            time.Time

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
		startupAt:            cfg.Clock.Now().UTC(),
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
	if recovered, err := w.RecoverClaimedAgentTurnsWithoutLiveOwnership(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup non-heartbeating claimed agent_turn recovery failed", "error", err)
		}
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered non-heartbeating claimed agent_turn jobs on startup", "count", recovered)
	}
	if recovered, err := w.RecoverStaleInProgressProjectTurnsWithoutOwnership(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale in-progress project turn recovery failed", "error", err)
		}
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered stale in-progress project turns on startup", "count", recovered)
	}
	if repaired, err := w.CloseTerminalProjectTaskAsyncSessions(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup terminal project_task session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed terminal project_task async sessions on startup", "count", repaired)
	}
	if repaired, err := w.CloseBlockedProjectTaskAsyncSessionsWithoutLiveExecution(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup blocked project_task non-live execution session cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: closed blocked project_task async sessions without live execution on startup", "count", repaired)
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
	if repaired, err := w.RetireClosedAsyncSessionRuns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup closed async session run cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: retired closed async session runs on startup", "count", repaired)
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
	if repaired, err := w.ClearCompletedSessionCurrentTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup post-invocation current turn cleanup failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: cleared completed session current turns after invocation cleanup on startup", "count", repaired)
	}
	if repaired, err := w.RecoverStaleInProgressContinuationTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale continuation recovery failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: recovered stale in-progress continuation turns on startup", "count", repaired)
	}
	if repaired, err := w.RecoverStaleInProgressTriggeredTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup stale triggered-turn recovery failed", "error", err)
		}
	} else if repaired > 0 {
		w.logger.Info("job queue: recovered stale in-progress triggered turns on startup", "count", repaired)
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
	if requeued, err := w.RequeueActiveProjectSessionsWithoutTurns(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup active project continuation requeue failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued active project sessions without turns on startup", "count", requeued)
	}
	if requeued, err := w.RequeueActiveProjectSessionsMissingContinuation(runCtx); err != nil {
		if runCtx.Err() == nil {
			w.logger.Error("startup idle project continuation recovery failed", "error", err)
		}
	} else if requeued > 0 {
		w.logger.Info("job queue: requeued active project sessions missing continuation on startup", "count", requeued)
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
		WHERE jq.status = 'pending'
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
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN flow_node_execution e
		      ON e.session_id = cs.id
		     AND e.status = 'active'
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project_task'
		      AND cs.mode = 'async'
		      AND cs.status = 'active'
		      AND NOT EXISTS (
		        SELECT 1
		        FROM chat_turn live
		        WHERE live.session_id = cs.id
		          AND live.status IN ('pending', 'in_progress')
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN chat_message cm
		      ON cm.id = (jq.payload->>'message_id')::uuid
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project'
		      AND cs.mode = 'async'
		      AND cs.status = 'active'
		      AND COALESCE(cm.metadata->>'supervisor_pm_recovery', '') = 'true'
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
			           ORDER BY CASE
			                        WHEN COALESCE(cm.metadata->>'source', '') IN ('task_queue_processor', 'task_review_action', 'task_recovery_resume')
			                         AND COALESCE(jq.payload->>'flow_node_execution_id', '') <> ''
			                         AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = COALESCE(jq.payload->>'flow_node_execution_id', '')
			                         AND EXISTS (
			                         	SELECT 1
			                         	FROM flow_node_execution e
			                         	WHERE e.session_id = cs.id
			                         	  AND e.status = 'active'
			                         	  AND e.id::text = COALESCE(jq.payload->>'flow_node_execution_id', '')
			                         )
			                        THEN 0
			                        WHEN EXISTS (
			                         	SELECT 1
			                         	FROM flow_node_execution e
			                         	WHERE e.session_id = cs.id
			                         	  AND e.status = 'active'
			                        )
			                        THEN 1
			                        ELSE 0
			                    END ASC,
			                    jq.created_at DESC, jq.run_after DESC, jq.id DESC
			       ) AS rn
			FROM job_queue jq
			JOIN chat_session cs ON cs.id = (jq.payload->>'session_id')::uuid
			LEFT JOIN chat_message cm ON cm.id = (jq.payload->>'message_id')::uuid
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

	// Condition 5b: a consumed recovery-resume message that already produced a
	// successful clean file.write in a completed turn should not be
	// re-dispatched. Validation-loop-blocked turns can still contain a
	// successful file.write before the recovery attempt fails again, so those
	// stale retries must remain eligible for requeue.
	ct5b, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    last_error = 'purged stale recovery resume after successful file.write',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = 'agent_turn'
		  AND EXISTS (
		    SELECT 1
		    FROM chat_message cm
		    WHERE cm.id = (jq.payload->>'message_id')::uuid
		      AND COALESCE(cm.metadata->>'source', '') = 'task_recovery_resume'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn live
		    WHERE live.session_id = (jq.payload->>'session_id')::uuid
		      AND live.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND live.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		      AND live.status IN ('pending', 'in_progress')
		  )
		  AND EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    JOIN chat_message tool_result
		      ON tool_result.session_id = ct.session_id
		     AND tool_result.turn_id = ct.id
		     AND tool_result.role = 'tool_result'
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.status = 'completed'
		      AND COALESCE(ct.stop_reason, '') = ''
		      AND COALESCE(tool_result.content::jsonb->>'tool_name', '') = 'file.write'
		      AND COALESCE(tool_result.content::jsonb->>'error', '') = ''
		      AND COALESCE(tool_result.content::jsonb->'output'->>'error', '') = ''
		      AND COALESCE((tool_result.content::jsonb->'output'->>'byte_size')::int, 0) > 0
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("purge stale agent_turn jobs (successful recovery resumes): %w", err)
	}

	// Condition 6: if the exact session/message/retry attempt already has a
	// live pending/in-progress turn recorded, keep exactly one backing
	// dispatch job for that live attempt and dead-letter only true duplicates.
	// A lone dispatch is still needed to run or heartbeat the live turn.
	ct6, err := w.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT jq.id,
			       jq.status,
			       ROW_NUMBER() OVER (
			           PARTITION BY jq.payload->>'session_id',
			                        COALESCE(jq.payload->>'retry_count', '0'),
			                        CASE
			                            WHEN COALESCE(jq.payload->>'flow_node_execution_id', '') <> ''
			                                THEN 'flow:' || (jq.payload->>'flow_node_execution_id')
			                            ELSE 'msg:' || COALESCE(jq.payload->>'message_id', '')
			                        END
			           ORDER BY CASE WHEN jq.status = 'claimed' THEN 0 ELSE 1 END,
			                    jq.created_at DESC,
			                    jq.id DESC
			       ) AS rn
			FROM job_queue jq
			WHERE jq.status IN ('pending', 'claimed')
			  AND jq.job_type = 'agent_turn'
			  AND EXISTS (
			    SELECT 1
			    FROM chat_turn ct
			    LEFT JOIN chat_message cm ON cm.id = ct.trigger_message_id
			    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
			      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
			      AND ct.status IN ('pending', 'in_progress')
			      AND (
			        ct.trigger_message_id = (jq.payload->>'message_id')::uuid
			        OR (
			          COALESCE(jq.payload->>'flow_node_execution_id', '') <> ''
			          AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = (jq.payload->>'flow_node_execution_id')
			        )
			      )
			  )
		)
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged duplicate live message-attempt dispatch',
		    updated_at = now()
		FROM ranked
		WHERE jq.id = ranked.id
		  AND ranked.rn > 1
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

	total := ct1.RowsAffected() + ct2.RowsAffected() + ct3.RowsAffected() + ct4.RowsAffected() + ct5.RowsAffected() + ct5b.RowsAffected() + ct6.RowsAffected() + ct7.RowsAffected() + ct8.RowsAffected()
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
		      AND newer.role = 'user'
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
		retired, err := w.retireSettledProjectContinuationMessage(ctx, sessionID, messageID)
		if err != nil {
			return repaired, fmt.Errorf("retire settled project continuation user message for session %s: %w", sessionID, err)
		}
		if retired {
			repaired++
			continue
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

func (w *Worker) retireSettledProjectContinuationMessage(ctx context.Context, sessionID, messageID uuid.UUID) (bool, error) {
	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return false, nil
	}

	var (
		scopeType     string
		source        string
		openTaskCount int
	)
	if err := w.pool.QueryRow(ctx, `
		SELECT cs.scope_type,
		       COALESCE(cm.metadata->>'source', ''),
		       COUNT(*) FILTER (WHERE pt.work_status NOT IN ('done', 'cancelled'))
		FROM chat_session cs
		JOIN chat_message cm
		  ON cm.id = $2
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project'
		 AND pt.project_id = cs.scope_id
		WHERE cs.id = $1
		GROUP BY cs.scope_type, cm.metadata
	`, sessionID, messageID).Scan(&scopeType, &source, &openTaskCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	normalizedSource := strings.TrimSpace(source)
	if !strings.EqualFold(strings.TrimSpace(scopeType), "project") ||
		(!strings.EqualFold(normalizedSource, "project_execution_continuation") &&
			!strings.EqualFold(normalizedSource, "project_continuation_resume")) ||
		openTaskCount > 0 {
		return false, nil
	}

	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'project continuation no longer needed; all project tasks settled',
		    updated_at = now()
		WHERE jq.job_type = $1
		  AND jq.status IN ('pending', 'claimed')
		  AND (jq.payload->>'session_id')::uuid = $2
		  AND (jq.payload->>'message_id')::uuid = $3
	`, agentTurnJobType, sessionID, messageID); err != nil {
		return false, err
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE chat_message
		SET status = 'failed',
		    error_message = 'project continuation no longer needed; all project tasks settled'
		WHERE id = $1
		  AND session_id = $2
		  AND role = 'user'
		  AND status = 'pending'
		  AND COALESCE(metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume')
	`, messageID, sessionID); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Worker) failPendingProjectContinuationResumeMessages(ctx context.Context, sessionID, keepMessageID uuid.UUID, reason string) error {
	if w == nil || w.pool == nil || sessionID == uuid.Nil {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "superseded by newer project continuation resume"
	}
	query := `
		UPDATE chat_message
		SET status = 'failed',
		    error_message = $2
		WHERE session_id = $1
		  AND role = 'user'
		  AND status = 'pending'
		  AND COALESCE(metadata->>'source', '') = 'project_continuation_resume'
	`
	args := []any{sessionID, reason}
	if keepMessageID != uuid.Nil {
		query += " AND id <> $3"
		args = append(args, keepMessageID)
	}
	_, err := w.pool.Exec(ctx, query, args...)
	return err
}

func (w *Worker) RequeueActiveExecutionSessionsWithoutTurns(ctx context.Context) (int64, error) {
	limit := w.maxExecutionSessionRecoveryBatch()
	rows, err := w.pool.Query(ctx, `
		WITH candidates AS (
			SELECT DISTINCT ON (cs.id) cs.id,
			       e.id AS execution_id,
			       COALESCE(cm.id, '00000000-0000-0000-0000-000000000000'::uuid) AS message_id,
			       COALESCE(cm.source, '') AS message_source,
			       COALESCE(cm.message_consumed, false) AS message_consumed,
			       CASE
			         WHEN cm.id IS NULL THEN 0
			         ELSE COALESCE((
			         SELECT MAX(ct.retry_count) + 1
			         FROM chat_turn ct
			         WHERE ct.session_id = cs.id
			           AND ct.trigger_message_id = cm.id
			       ), 0)
			       END AS next_retry_count,
			       COALESCE((
			         SELECT ct.error_message
			         FROM chat_turn ct
			         WHERE ct.session_id = cs.id
			           AND ct.trigger_message_id = cm.id
			         ORDER BY ct.turn_number DESC, ct.retry_count DESC, ct.created_at DESC, ct.id DESC
			         LIMIT 1
			       ), '') AS latest_turn_error,
			       COALESCE((
			         SELECT ct.completed_at
			         FROM chat_turn ct
			         WHERE ct.session_id = cs.id
			           AND ct.trigger_message_id = cm.id
			         ORDER BY ct.turn_number DESC, ct.retry_count DESC, ct.created_at DESC, ct.id DESC
			         LIMIT 1
			       ), COALESCE(cm.created_at, e.started_at)) AS latest_turn_completed_at,
			       e.started_at AS execution_started_at,
			       COALESCE(cm.created_at, e.started_at) AS message_created_at
			FROM chat_session cs
			JOIN flow_node_execution e
			  ON e.session_id = cs.id
			 AND e.status = 'active'
			JOIN project_task pt
			  ON pt.id = cs.scope_id
			JOIN project p
			  ON p.id = pt.project_id
			JOIN flow_node fn
			  ON fn.id = e.flow_node_id
			LEFT JOIN LATERAL (
				SELECT cm.id,
				       cm.created_at,
				       COALESCE(cm.metadata->>'source', '') AS source,
				       EXISTS (
				         SELECT 1
				         FROM chat_turn ct
				         WHERE ct.session_id = cs.id
				           AND ct.trigger_message_id = cm.id
				           AND ct.status IN ('completed', 'failed', 'cancelled')
				       ) AS message_consumed,
				       cm.metadata,
				       cm.content
				FROM chat_message cm
				WHERE cm.session_id = cs.id
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
				    OR (
				      COALESCE(cm.metadata->>'source', '') = 'task_recovery_resume'
				      AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
				    )
				    OR (
				      COALESCE(cm.metadata->>'source', '') = 'task_review_action'
				      AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
				    )
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
				ORDER BY CASE
				           WHEN COALESCE(cm.metadata->>'source', '') IN ('task_queue_processor', 'task_review_action', 'task_recovery_resume')
				            AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
				           THEN 0
				           ELSE 1
				         END ASC,
				         cm.created_at DESC, cm.id DESC
				LIMIT 1
			) cm ON true
			WHERE cs.scope_type = 'project_task'
			  AND cs.mode = 'async'
			  AND cs.status = 'active'
			  AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
			  AND cs.current_turn_id IS NULL
			  AND NOT EXISTS (
			    SELECT 1
			    FROM chat_turn ct
			    WHERE ct.session_id = cs.id
			      AND ct.status IN ('pending', 'in_progress')
			  )
			  AND (
			    cm.id IS NULL
			    OR COALESCE(fn.node_type, '') = 'review'
			    OR NOT EXISTS (
			      SELECT 1
			      FROM chat_turn halted
			      WHERE halted.session_id = cs.id
			        AND halted.trigger_message_id = cm.id
			        AND halted.status = 'completed'
			        AND COALESCE(halted.stop_reason, '') = 'recovery_content_required'
			    )
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM project_task blocked_task
			    WHERE blocked_task.id = cs.scope_id
			      AND blocked_task.work_status = 'blocked'
			      AND COALESCE(blocked_task.metadata->'agent_turn_validation_guard'->>'blocked', '') = 'true'
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM job_queue jq
			    LEFT JOIN chat_message queued_message
			      ON queued_message.id = (jq.payload->>'message_id')::uuid
			    WHERE jq.job_type = $1
			      AND jq.status IN ('pending', 'claimed')
			      AND (jq.payload->>'session_id')::uuid = cs.id
			      AND NOT (
			            cm.id IS NOT NULL
			        AND (
			              (
			                (
			                  COALESCE(cm.metadata->>'source', '') IN ('task_review_action', 'task_recovery_resume')
			                  OR (
			                       COALESCE(cm.metadata->>'source', '') = 'supervisor'
			                   AND cm.content = 'supervisor recovery: resume task'
			                     )
			                )
			                AND queued_message.created_at < cm.created_at
			              )
			           OR (
			                COALESCE(cm.metadata->>'source', '') = 'task_queue_processor'
			                AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = e.id::text
			                AND COALESCE(queued_message.metadata->>'source', '') = 'supervisor'
			                AND queued_message.content = 'supervisor recovery: resume task'
			              )
			            )
			          )
			  )
			ORDER BY cs.id, COALESCE(cm.created_at, e.started_at) DESC, COALESCE(cm.id, '00000000-0000-0000-0000-000000000000'::uuid) DESC
		)
		SELECT id, execution_id, message_id, message_source, message_consumed, next_retry_count, latest_turn_error, latest_turn_completed_at
		FROM candidates
		ORDER BY execution_started_at ASC, message_created_at ASC, id ASC
		LIMIT $2
	`, agentTurnJobType, limit)
	if err != nil {
		return 0, fmt.Errorf("list active execution sessions without turns: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var executionID uuid.UUID
		var messageID uuid.UUID
		var messageSource string
		var messageConsumed bool
		var retryCount int
		var latestTurnError string
		var latestTurnCompletedAt time.Time
		if err := rows.Scan(&sessionID, &executionID, &messageID, &messageSource, &messageConsumed, &retryCount, &latestTurnError, &latestTurnCompletedAt); err != nil {
			return repaired, fmt.Errorf("scan active execution session without turn: %w", err)
		}
		if messageConsumed && strings.EqualFold(strings.TrimSpace(messageSource), "task_recovery_resume") {
			successfulRecoveryWrite, recoveryErr := w.recoveryResumeMessageCompletedWithSuccessfulFileWrite(ctx, sessionID, messageID)
			if recoveryErr != nil {
				return repaired, fmt.Errorf("check completed recovery resume for session %s: %w", sessionID, recoveryErr)
			}
			if successfulRecoveryWrite {
				continue
			}
		}
		if messageID == uuid.Nil || (strings.EqualFold(strings.TrimSpace(messageSource), "supervisor") && messageConsumed) {
			synthMessageID, synthErr := w.ensureTaskExecutionKickoffMessage(ctx, sessionID, executionID)
			if synthErr != nil {
				return repaired, fmt.Errorf("ensure task execution kickoff message for session %s: %w", sessionID, synthErr)
			}
			if synthMessageID == uuid.Nil {
				continue
			}
			messageID = synthMessageID
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE job_queue jq
			SET status = 'dead_letter',
			    claimed_by = NULL,
			    claimed_at = NULL,
			    last_error = 'superseded stale message-attempt dispatch during task requeue',
			    updated_at = now()
			WHERE jq.job_type = $1
			  AND jq.status IN ('pending', 'claimed')
			  AND (jq.payload->>'session_id')::uuid = $2
			  AND EXISTS (
			    SELECT 1
			    FROM chat_message selected_message
			    WHERE selected_message.id = $3
			      AND (
			            COALESCE(selected_message.metadata->>'source', '') IN ('task_review_action', 'task_recovery_resume')
			         OR (
			              COALESCE(selected_message.metadata->>'source', '') = 'supervisor'
			          AND selected_message.content = 'supervisor recovery: resume task'
			            )
			         OR (
			              COALESCE(selected_message.metadata->>'source', '') = 'task_queue_processor'
			          AND COALESCE(selected_message.metadata->>'flow_node_execution_id', '') = $4::text
			            )
			          )
			  )
			  AND (jq.payload->>'message_id')::uuid <> $3
		`, agentTurnJobType, sessionID, messageID, executionID); err != nil {
			return repaired, fmt.Errorf("purge superseded agent_turn dispatches for session %s: %w", sessionID, err)
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE run
			SET status = 'failed',
			    failure_class = 'transient',
			    failure_reason = 'recovered active execution session without live task turn ownership',
			    completed_at = COALESCE(completed_at, now()),
			    updated_at = now()
			WHERE session_id = $1
			  AND status IN ('created', 'in_progress', 'cancelling')
			  AND turn_id IS NULL
		`, sessionID); err != nil {
			return repaired, fmt.Errorf("retire stale active execution runs for session %s: %w", sessionID, err)
		}
		var runAfter *time.Time
		payload := agentTurnKeyPayload{
			SessionID:           sessionID,
			MessageID:           messageID,
			RetryCount:          retryCount,
			FlowNodeExecutionID: &executionID,
		}
		if retryAfterHint, ok := parseRateLimitRetryAfterFromText(latestTurnError); ok {
			retryDelay := agentTurnRateLimitDelay(max(1, retryCount), retryAfterHint)
			completedAt := latestTurnCompletedAt.UTC()
			scheduled := completedAt.Add(retryDelay)
			if scheduled.Before(w.clock.Now().UTC()) {
				scheduled = w.clock.Now().UTC()
			}
			runAfter = &scheduled
			payload.RateLimitJitterApplied = true
		} else if retryAfterHint, transient := parseTransientModelRetryAfterFromText(latestTurnError); transient || looksLikeTransientModelFailureText(latestTurnError) {
			retryDelay := agentTurnTransientDelay(max(1, retryCount), retryAfterHint)
			completedAt := latestTurnCompletedAt.UTC()
			scheduled := completedAt.Add(retryDelay)
			if scheduled.Before(w.clock.Now().UTC()) {
				scheduled = w.clock.Now().UTC()
			}
			runAfter = &scheduled
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, payload, runAfter); err != nil {
			return repaired, fmt.Errorf("requeue active execution session without turn for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate active execution sessions without turns: %w", err)
	}
	return repaired, nil
}

func (w *Worker) recoveryResumeMessageCompletedWithSuccessfulFileWrite(ctx context.Context, sessionID, messageID uuid.UUID) (bool, error) {
	if w == nil || w.pool == nil || sessionID == uuid.Nil || messageID == uuid.Nil {
		return false, nil
	}
	var successful bool
	if err := w.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM chat_turn ct
			JOIN chat_message tool_result
			  ON tool_result.session_id = ct.session_id
			 AND tool_result.turn_id = ct.id
			 AND tool_result.role = 'tool_result'
			WHERE ct.session_id = $1
			  AND ct.trigger_message_id = $2
			  AND ct.status = 'completed'
			  AND COALESCE(ct.stop_reason, '') = ''
			  AND COALESCE(tool_result.content::jsonb->>'tool_name', '') = 'file.write'
			  AND COALESCE(tool_result.content::jsonb->>'error', '') = ''
			  AND COALESCE(tool_result.content::jsonb->'output'->>'error', '') = ''
			  AND COALESCE((tool_result.content::jsonb->'output'->>'byte_size')::int, 0) > 0
		)
	`, sessionID, messageID).Scan(&successful); err != nil {
		return false, err
	}
	return successful, nil
}

func (w *Worker) RequeueActiveProjectSessionsWithoutTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
	SELECT DISTINCT ON (cs.id) cs.id,
	       cm.id,
	       COALESCE(cm.metadata->>'source', '') AS source,
	       COALESCE(cs.metadata->'project_bootstrap'->>'status', '') AS project_bootstrap_status,
	       COUNT(*) FILTER (WHERE pt.work_status NOT IN ('done', 'cancelled')) AS open_task_count,
	       COALESCE((
	         SELECT MAX(ct.retry_count) + 1
		         FROM chat_turn ct
		         WHERE ct.session_id = cs.id
		           AND ct.trigger_message_id = cm.id
		       ), 0) AS next_retry_count,
		       COALESCE(latest_turn.error_message, '') AS latest_turn_error,
		       COALESCE(latest_turn.completed_at, latest_turn.created_at, cm.created_at) AS latest_turn_completed_at
		FROM chat_session cs
		JOIN project p
		  ON p.id = cs.scope_id
		JOIN chat_message cm
		  ON cm.session_id = cs.id
		LEFT JOIN project_task pt
		  ON pt.project_id = cs.scope_id
		LEFT JOIN LATERAL (
		  SELECT ct.error_message,
		         ct.completed_at,
		         ct.created_at
		  FROM chat_turn ct
		  WHERE ct.session_id = cs.id
		    AND ct.trigger_message_id = cm.id
		    AND ct.status IN ('completed', 'cancelled', 'failed')
		  ORDER BY ct.retry_count DESC,
		           COALESCE(ct.completed_at, ct.created_at) DESC,
		           ct.id DESC
		  LIMIT 1
		) latest_turn ON true
		WHERE cs.scope_type = 'project'
		  AND cs.mode = 'async'
		  AND cs.status = 'active'
		  AND p.status = 'active'
		  AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		  AND cs.current_turn_id IS NULL
		  AND cm.role = 'user'
		  AND cm.status = 'pending'
		  AND COALESCE(cm.metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		  AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume', 'project_bootstrap')
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.status IN ('pending', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    LEFT JOIN chat_message queued_message
		      ON queued_message.id = (jq.payload->>'message_id')::uuid
		    WHERE jq.job_type = $1
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		      AND NOT EXISTS (
		        SELECT 1
		        FROM chat_turn terminal_ct
		        WHERE terminal_ct.session_id = (jq.payload->>'session_id')::uuid
		          AND terminal_ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		          AND terminal_ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		          AND terminal_ct.status IN ('completed', 'cancelled', 'failed')
		      )
		      AND NOT (
		            COALESCE(queued_message.metadata->>'source', '') = 'project_bootstrap'
		        AND COALESCE(cs.metadata->'project_bootstrap'->>'status', '') = 'completed'
		      )
		  )
		GROUP BY cs.id, cm.id, cm.created_at, cm.metadata, latest_turn.error_message, latest_turn.completed_at, latest_turn.created_at
		ORDER BY cs.id, cm.created_at DESC, cm.id DESC
	`, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list active project sessions without turns: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		var messageID uuid.UUID
		var source string
		var projectBootstrapStatus string
		var openTaskCount int
		var retryCount int
		var latestTurnError string
		var latestTurnCompletedAt time.Time
		if err := rows.Scan(&sessionID, &messageID, &source, &projectBootstrapStatus, &openTaskCount, &retryCount, &latestTurnError, &latestTurnCompletedAt); err != nil {
			return repaired, fmt.Errorf("scan active project session without turn: %w", err)
		}
		trimmedSource := strings.TrimSpace(source)
		bootstrapActive := strings.EqualFold(strings.TrimSpace(projectBootstrapStatus), "active")
		if strings.EqualFold(trimmedSource, "project_bootstrap") && !bootstrapActive {
			if err := w.failPendingProjectBootstrapMessages(ctx, sessionID, "project bootstrap already complete; continuing project execution"); err != nil {
				return repaired, fmt.Errorf("retire stale project bootstrap messages for session %s: %w", sessionID, err)
			}
			if openTaskCount == 0 {
				repaired++
				continue
			}
		}
		if (strings.EqualFold(trimmedSource, "project_execution_continuation") ||
			strings.EqualFold(trimmedSource, "project_continuation_resume")) && openTaskCount == 0 && !bootstrapActive {
			retired, err := w.retireSettledProjectContinuationMessage(ctx, sessionID, messageID)
			if err != nil {
				return repaired, fmt.Errorf("retire settled project continuation message for session %s: %w", sessionID, err)
			}
			if !retired {
				return repaired, fmt.Errorf("expected settled project continuation message %s for session %s to retire", messageID, sessionID)
			}
			repaired++
			continue
		}
		if strings.EqualFold(trimmedSource, "project_continuation_resume") {
			if retryCount > 0 {
				if err := w.failPendingProjectContinuationResumeMessages(ctx, sessionID, uuid.Nil, "superseded after prior continuation resume turn completed"); err != nil {
					return repaired, fmt.Errorf("retire consumed project continuation resume messages for session %s: %w", sessionID, err)
				}
				synthMessageID, suppressed, err := w.ensureProjectContinuationMessageDecision(ctx, sessionID)
				if err != nil {
					return repaired, fmt.Errorf("refresh project continuation after resume for session %s: %w", sessionID, err)
				}
				if suppressed {
					repaired++
					continue
				}
				if synthMessageID == uuid.Nil {
					repaired++
					continue
				}
				messageID = synthMessageID
				retryCount = 0
				trimmedSource = "project_execution_continuation"
			} else if err := w.failPendingProjectContinuationResumeMessages(ctx, sessionID, messageID, "superseded by newer project continuation resume"); err != nil {
				return repaired, fmt.Errorf("collapse stale project continuation resume messages for session %s: %w", sessionID, err)
			}
		}
		shouldRefreshContinuation := strings.EqualFold(trimmedSource, "project_execution_continuation") ||
			(strings.EqualFold(trimmedSource, "project_bootstrap") && openTaskCount > 0)
		if shouldRefreshContinuation {
			synthMessageID, suppressed, err := w.ensureProjectContinuationMessageDecision(ctx, sessionID)
			if err != nil {
				return repaired, fmt.Errorf("ensure project continuation message for session %s: %w", sessionID, err)
			}
			if suppressed {
				repaired++
				continue
			}
			if synthMessageID != uuid.Nil {
				if synthMessageID != messageID {
					retryCount = 0
				}
				messageID = synthMessageID
			}
		}
		var runAfter *time.Time
		payload := agentTurnKeyPayload{
			SessionID:  sessionID,
			MessageID:  messageID,
			RetryCount: retryCount,
		}
		if retryAfterHint, ok := parseRateLimitRetryAfterFromText(latestTurnError); ok {
			retryDelay := agentTurnRateLimitDelay(max(1, retryCount), retryAfterHint)
			scheduled := latestTurnCompletedAt.UTC().Add(retryDelay)
			if scheduled.Before(w.clock.Now().UTC()) {
				scheduled = w.clock.Now().UTC()
			}
			runAfter = &scheduled
			payload.RateLimitJitterApplied = true
		} else if retryAfterHint, transient := parseTransientModelRetryAfterFromText(latestTurnError); transient || looksLikeTransientModelFailureText(latestTurnError) {
			retryDelay := agentTurnTransientDelay(max(1, retryCount), retryAfterHint)
			scheduled := latestTurnCompletedAt.UTC().Add(retryDelay)
			if scheduled.Before(w.clock.Now().UTC()) {
				scheduled = w.clock.Now().UTC()
			}
			runAfter = &scheduled
		}
		if _, err := w.pool.Exec(ctx, `
			UPDATE job_queue jq
			SET status = 'dead_letter',
			    claimed_by = NULL,
			    claimed_at = NULL,
			    last_error = 'superseded stale terminal message-attempt dispatch during project requeue',
			    updated_at = now()
			WHERE jq.job_type = $1
			  AND jq.status IN ('pending', 'claimed')
			  AND (jq.payload->>'session_id')::uuid = $2
			  AND EXISTS (
			    SELECT 1
			    FROM chat_turn terminal_ct
			    WHERE terminal_ct.session_id = (jq.payload->>'session_id')::uuid
			      AND terminal_ct.trigger_message_id = (jq.payload->>'message_id')::uuid
			      AND terminal_ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
			      AND terminal_ct.status IN ('completed', 'cancelled', 'failed')
			  )
		`, agentTurnJobType, sessionID); err != nil {
			return repaired, fmt.Errorf("purge stale terminal agent_turn dispatches for session %s: %w", sessionID, err)
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, payload, runAfter); err != nil {
			return repaired, fmt.Errorf("requeue active project session without turn for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate active project sessions without turns: %w", err)
	}
	return repaired, nil
}

func (w *Worker) RequeueActiveProjectSessionsMissingContinuation(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT cs.id
		FROM chat_session cs
		JOIN project p
		  ON p.id = cs.scope_id
		WHERE cs.scope_type = 'project'
		  AND cs.mode = 'async'
		  AND cs.status = 'active'
		  AND p.status = 'active'
		  AND COALESCE(p.settings->'pause'->>'is_paused', 'false') <> 'true'
		  AND cs.current_turn_id IS NULL
		  AND COALESCE(cs.metadata->'project_bootstrap'->>'status', '') <> 'active'
		  AND EXISTS (
		    SELECT 1
		    FROM project_task pt
		    WHERE pt.project_id = cs.scope_id
		      AND lower(trim(pt.work_status)) = 'draft'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM project_task pt
		    WHERE pt.project_id = cs.scope_id
		      AND lower(trim(pt.work_status)) IN ('queued', 'in_progress', 'review')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM flow_node_execution e
		    JOIN project_task pt
		      ON pt.id = e.task_id
		    WHERE pt.project_id = cs.scope_id
		      AND lower(trim(pt.work_status)) NOT IN ('done', 'cancelled')
		      AND e.status = 'active'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_session task_session
		    JOIN project_task pt
		      ON pt.id = task_session.scope_id
		    WHERE task_session.scope_type = 'project_task'
		      AND task_session.mode = 'async'
		      AND task_session.status = 'active'
		      AND pt.project_id = cs.scope_id
		      AND lower(trim(pt.work_status)) NOT IN ('done', 'cancelled')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.status IN ('pending', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_message cm
		    WHERE cm.session_id = cs.id
		      AND cm.role = 'user'
		      AND cm.status = 'pending'
		      AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume', 'project_bootstrap')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $1
		      AND jq.status IN ('pending', 'claimed')
		      AND (jq.payload->>'session_id')::uuid = cs.id
		      AND NOT EXISTS (
		        SELECT 1
		        FROM chat_turn terminal_ct
		        WHERE terminal_ct.session_id = (jq.payload->>'session_id')::uuid
		          AND terminal_ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		          AND terminal_ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		          AND terminal_ct.status IN ('completed', 'cancelled', 'failed')
		      )
		  )
	`, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list active project sessions missing continuation: %w", err)
	}
	defer rows.Close()

	var repaired int64
	for rows.Next() {
		var sessionID uuid.UUID
		if err := rows.Scan(&sessionID); err != nil {
			return repaired, fmt.Errorf("scan active project session missing continuation: %w", err)
		}
		messageID, suppressed, err := w.ensureProjectContinuationMessageDecision(ctx, sessionID)
		if err != nil {
			return repaired, fmt.Errorf("ensure missing project continuation message for session %s: %w", sessionID, err)
		}
		if suppressed || messageID == uuid.Nil {
			repaired++
			continue
		}
		payload := agentTurnKeyPayload{
			SessionID: sessionID,
			MessageID: messageID,
		}
		if _, err := w.enqueueAgentTurnDispatch(ctx, nil, payload, nil); err != nil {
			return repaired, fmt.Errorf("enqueue missing project continuation dispatch for session %s: %w", sessionID, err)
		}
		repaired++
	}
	if err := rows.Err(); err != nil {
		return repaired, fmt.Errorf("iterate active project sessions missing continuation: %w", err)
	}
	return repaired, nil
}

func (w *Worker) failPendingProjectBootstrapMessages(ctx context.Context, sessionID uuid.UUID, reason string) error {
	if w == nil || w.pool == nil || sessionID == uuid.Nil {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "project bootstrap already complete"
	}
	_, err := w.pool.Exec(ctx, `
		UPDATE chat_message
		SET status = 'failed',
		    error_message = $2
		WHERE session_id = $1
		  AND role = 'user'
		  AND status = 'pending'
		  AND COALESCE(metadata->>'source', '') = 'project_bootstrap'
	`, sessionID, reason)
	return err
}

func (w *Worker) ensureProjectContinuationMessage(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, error) {
	messageID, _, err := w.ensureProjectContinuationMessageDecision(ctx, sessionID)
	return messageID, err
}

func (w *Worker) ensureProjectContinuationMessageDecision(ctx context.Context, sessionID uuid.UUID) (uuid.UUID, bool, error) {
	if w == nil || w.pool == nil || sessionID == uuid.Nil {
		return uuid.Nil, false, nil
	}

	var (
		bootstrapStatus string
		projectID       uuid.UUID
	)
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->'project_bootstrap'->>'status', ''),
		       CASE
		         WHEN scope_type = 'project' THEN scope_id
		         ELSE NULL
		       END
		FROM chat_session
		WHERE id = $1
	`, sessionID).Scan(&bootstrapStatus, &projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, fmt.Errorf("load project session bootstrap status: %w", err)
	}
	activeBootstrap := strings.EqualFold(strings.TrimSpace(bootstrapStatus), "active")

	source := "project_execution_continuation"
	content := strings.Join([]string{
		"Continue the active project execution now.",
		"Bootstrap is complete and draft project work remains.",
		"Your next response must take direct project action instead of generic chat.",
		"Do not ask what to do next, do not restate the project, and do not reread broad context before acting.",
		"Inspect the current task tree and immediately queue or otherwise advance the next runnable bounded draft task.",
	}, " ")
	metadataMap := map[string]any{
		"source":                 source,
		"auto_continue":          true,
		"synthetic_user_message": true,
	}
	expectedCompletedTaskID := ""
	expectedSnapshotFingerprint := ""
	if activeBootstrap {
		source = "project_bootstrap"
		content = strings.Join([]string{
			"Continue the active project bootstrap from the persisted state above.",
			"Bootstrap is not complete yet; do not treat this session as post-bootstrap project execution.",
			"Your next response must take direct bootstrap action instead of generic chat.",
			"Do not ask what to do next, do not restate the project, and do not reread broad context before acting.",
			"Inspect the persisted bootstrap task tree and immediately repair staffing, bounded task decomposition, assignment, or flow attachment using bootstrap-compatible task mutations.",
		}, " ")
		metadataMap["source"] = source
	} else if projectID != uuid.Nil {
		var (
			completedTaskID    uuid.UUID
			completedTaskNum   int
			completedTaskTitle string
			remainingDrafts    int
			err                error
		)
		if err := w.pool.QueryRow(ctx, `
			WITH latest_completed AS (
				SELECT id, task_number, title
				FROM project_task
				WHERE project_id = $1
				  AND work_status = 'done'
				ORDER BY updated_at DESC, task_number DESC, id DESC
				LIMIT 1
			)
			SELECT COALESCE((SELECT id FROM latest_completed), '00000000-0000-0000-0000-000000000000'::uuid),
			       COALESCE((SELECT task_number FROM latest_completed), 0),
			       COALESCE((SELECT title FROM latest_completed), '')
		`, projectID).Scan(&completedTaskID, &completedTaskNum, &completedTaskTitle); err != nil {
			return uuid.Nil, false, fmt.Errorf("load latest completed project task: %w", err)
		}
		remainingDrafts, err = w.countActionableProjectDraftTasks(ctx, projectID)
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("count actionable draft project tasks: %w", err)
		}
		if completedTaskID != uuid.Nil {
			snapshot, snapshotErr := w.projectExecutionContinuationSnapshot(ctx, projectID)
			if snapshotErr != nil {
				return uuid.Nil, false, fmt.Errorf("build project continuation snapshot: %w", snapshotErr)
			}
			expectedCompletedTaskID = completedTaskID.String()
			expectedSnapshotFingerprint = projectExecutionContinuationFingerprintForWorker(completedTaskID, remainingDrafts, snapshot)
			metadataMap["completed_task_id"] = expectedCompletedTaskID
			metadataMap[projectContinuationSnapshotFingerprintKey] = expectedSnapshotFingerprint
			metadataMap["repo_version"] = strings.TrimSpace(versionpkg.RepoVersion)
			content = buildProjectExecutionContinuationPromptForWorker(completedTaskNum, completedTaskTitle, remainingDrafts, snapshot)
		}
	}

	var latestConsumedMatchingMessageID uuid.UUID
	if source == "project_execution_continuation" && expectedCompletedTaskID != "" && expectedSnapshotFingerprint != "" {
		err := w.pool.QueryRow(ctx, `
			SELECT cm.id
			FROM chat_message cm
			WHERE cm.session_id = $1
			  AND cm.role = 'user'
			  AND COALESCE(cm.metadata->>'source', '') = $2
			  AND COALESCE(cm.metadata->>'completed_task_id', '') = $3
			  AND COALESCE(cm.metadata->>$4, '') = $5
			  AND EXISTS (
			        SELECT 1
			        FROM chat_turn ct
			        WHERE ct.trigger_message_id = cm.id
			          AND ct.status IN ('completed', 'failed', 'cancelled')
			  )
			ORDER BY cm.created_at DESC, cm.id DESC
			LIMIT 1
		`, sessionID, source, expectedCompletedTaskID, projectContinuationSnapshotFingerprintKey, expectedSnapshotFingerprint).Scan(&latestConsumedMatchingMessageID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, fmt.Errorf("query latest consumed matching project continuation message: %w", err)
		}
	}

	var (
		existingMessageID               uuid.UUID
		existingCompletedTaskIDText     string
		existingSnapshotFingerprintText string
		existingMessageConsumed         bool
	)
	err := w.pool.QueryRow(ctx, `
		SELECT id,
		       COALESCE(metadata->>'completed_task_id', ''),
		       COALESCE(metadata->>$3, ''),
		       EXISTS (
		         SELECT 1
		         FROM chat_turn ct
		         WHERE ct.trigger_message_id = chat_message.id
		           AND ct.status IN ('completed', 'failed', 'cancelled')
		       )
		FROM chat_message
		WHERE session_id = $1
		  AND role = 'user'
		  AND status = 'pending'
		  AND COALESCE(metadata->>'source', '') = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, source, projectContinuationSnapshotFingerprintKey).Scan(
		&existingMessageID,
		&existingCompletedTaskIDText,
		&existingSnapshotFingerprintText,
		&existingMessageConsumed,
	)
	if err == nil {
		if source != "project_execution_continuation" {
			return existingMessageID, false, nil
		}
		existingCompletedTaskIDText = strings.TrimSpace(existingCompletedTaskIDText)
		existingSnapshotFingerprintText = strings.TrimSpace(existingSnapshotFingerprintText)
		sameCompletedTask := expectedCompletedTaskID != "" && expectedCompletedTaskID == existingCompletedTaskIDText
		sameSnapshot := expectedSnapshotFingerprint != "" && expectedSnapshotFingerprint == existingSnapshotFingerprintText
		if !existingMessageConsumed && sameCompletedTask && (expectedSnapshotFingerprint == "" || sameSnapshot) {
			if latestConsumedMatchingMessageID != uuid.Nil {
				suppressed, suppressErr := w.suppressRepeatedIdenticalPendingProjectContinuation(ctx, latestConsumedMatchingMessageID, existingMessageID)
				if suppressErr != nil {
					return uuid.Nil, false, fmt.Errorf("check stale pending project continuation suppression: %w", suppressErr)
				}
				if suppressed {
					return uuid.Nil, true, nil
				}
			}
			return existingMessageID, false, nil
		}
		if existingMessageConsumed && sameCompletedTask && sameSnapshot {
			suppressed, suppressErr := w.suppressRepeatedIdenticalProjectContinuation(ctx, existingMessageID)
			if suppressErr != nil {
				return uuid.Nil, false, fmt.Errorf("check repeated project continuation suppression: %w", suppressErr)
			}
			if suppressed {
				return uuid.Nil, true, nil
			}
		}
		if _, execErr := w.pool.Exec(ctx, `
			UPDATE chat_message
			SET status = 'failed',
			    error_message = CASE
			      WHEN $3 THEN 'superseded after prior continuation turn completed'
			      ELSE 'superseded by newer completed project task'
			    END
			WHERE session_id = $1
			  AND role = 'user'
			  AND status = 'pending'
			  AND COALESCE(metadata->>'source', '') = $2
		`, sessionID, source, existingMessageConsumed); execErr != nil {
			return uuid.Nil, false, fmt.Errorf("fail stale project continuation messages: %w", execErr)
		}
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("query existing project continuation message: %w", err)
	}

	if source == "project_execution_continuation" && latestConsumedMatchingMessageID != uuid.Nil {
		suppressed, suppressErr := w.suppressRepeatedIdenticalProjectContinuation(ctx, latestConsumedMatchingMessageID)
		if suppressErr != nil {
			return uuid.Nil, false, fmt.Errorf("check repeated project continuation suppression: %w", suppressErr)
		}
		if suppressed {
			return uuid.Nil, true, nil
		}
	}
	metadata, err := json.Marshal(metadataMap)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("marshal project continuation metadata: %w", err)
	}

	message, err := repo.NewChatMessageRepo(w.pool).Create(ctx, repo.ChatMessage{
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		Status:    "pending",
		Metadata:  metadata,
	})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("create project continuation message: %w", err)
	}
	return message.ID, false, nil
}

func (w *Worker) suppressRepeatedIdenticalProjectContinuation(ctx context.Context, messageID uuid.UUID) (bool, error) {
	return w.suppressRepeatedIdenticalPendingProjectContinuation(ctx, messageID, messageID)
}

func (w *Worker) suppressRepeatedIdenticalPendingProjectContinuation(ctx context.Context, referenceMessageID, pendingMessageID uuid.UUID) (bool, error) {
	if w == nil || w.pool == nil || referenceMessageID == uuid.Nil || pendingMessageID == uuid.Nil {
		return false, nil
	}

	var blocked bool
	if err := w.pool.QueryRow(ctx, `
		WITH latest_turn AS (
			SELECT ct.id,
			       COALESCE(ct.stop_reason, '') AS stop_reason
			FROM chat_turn ct
			WHERE ct.trigger_message_id = $1
			  AND ct.status IN ('completed', 'failed', 'cancelled')
			ORDER BY ct.retry_count DESC,
			         COALESCE(ct.completed_at, ct.created_at) DESC,
			         ct.id DESC
			LIMIT 1
		)
		SELECT EXISTS (
			SELECT 1
			FROM latest_turn lt
			JOIN chat_message sm
			  ON sm.turn_id = lt.id
			 AND sm.role = 'system'
			WHERE lt.stop_reason = 'validation_loop_blocked'
			  AND (
			       sm.content LIKE $2
			    OR sm.content LIKE $3
			    OR sm.content LIKE $4
			    OR (sm.content LIKE $5 AND sm.content LIKE $6)
			  )
		)
	`, referenceMessageID,
		projectContinuationRediscoveryGuardPrefix+"%",
		projectContinuationActiveReplacementPrefix+"%"+projectContinuationActiveReplacementMarker+"%",
		projectContinuationBoundedSizePrefix+"%",
		projectContinuationDraftBoundedSizePrefix+"%",
		"%"+projectContinuationBoundedSizeMarker+"%",
	).Scan(&blocked); err != nil {
		return false, err
	}
	if !blocked {
		return false, nil
	}

	if _, err := w.pool.Exec(ctx, `
		UPDATE chat_message
		SET status = 'failed',
		    error_message = $2
		WHERE id = $1
		  AND status = 'pending'
	`, pendingMessageID, projectContinuationSuppressedErrorMessage); err != nil {
		return false, err
	}
	return true, nil
}

func (w *Worker) ensureTaskExecutionKickoffMessage(ctx context.Context, sessionID, executionID uuid.UUID) (uuid.UUID, error) {
	if w == nil || w.pool == nil || sessionID == uuid.Nil || executionID == uuid.Nil {
		return uuid.Nil, nil
	}

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, fmt.Errorf("begin task execution kickoff repair: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1::text))`, executionID.String()); err != nil {
		return uuid.Nil, fmt.Errorf("lock task execution kickoff repair: %w", err)
	}

	var existingMessageID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM chat_message
		WHERE session_id = $1
		  AND role = 'user'
		  AND status = 'pending'
		  AND COALESCE(metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		  AND COALESCE(metadata->>'source', '') = 'task_queue_processor'
		  AND COALESCE(metadata->>'flow_node_execution_id', '') = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID, executionID.String()).Scan(&existingMessageID)
	if err == nil {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return uuid.Nil, fmt.Errorf("commit existing task execution kickoff repair: %w", commitErr)
		}
		return existingMessageID, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("query existing task execution kickoff message: %w", err)
	}

	var (
		taskRecord repo.ProjectTask
		node       repo.FlowNode
		execution  repo.FlowNodeExecution
	)
	if err := tx.QueryRow(ctx, `
		SELECT pt.organization_id,
		       pt.project_id,
		       pt.id,
		       pt.title,
		       pt.description,
		       pt.work_status,
		       pt.blocks_scope,
		       pt.flow_template_id,
		       pt.current_flow_node_id,
		       pt.metadata,
		       fn.id,
		       fn.display_name,
		       fn.node_type,
		       fn.requires_human_review,
		       e.visit_number
		FROM flow_node_execution e
		JOIN project_task pt
		  ON pt.id = e.task_id
		JOIN chat_session cs
		  ON cs.id = e.session_id
		JOIN flow_node fn
		  ON fn.id = e.flow_node_id
		WHERE e.id = $1
		  AND e.status = 'active'
		  AND e.session_id = $2
		  AND cs.status = 'active'
		  AND cs.scope_type = 'project_task'
	`, executionID, sessionID).Scan(
		&taskRecord.OrganizationID,
		&taskRecord.ProjectID,
		&taskRecord.ID,
		&taskRecord.Title,
		&taskRecord.Description,
		&taskRecord.WorkStatus,
		&taskRecord.BlocksScope,
		&taskRecord.FlowTemplateID,
		&taskRecord.CurrentFlowNodeID,
		&taskRecord.Metadata,
		&node.ID,
		&node.DisplayName,
		&node.NodeType,
		&node.RequiresHumanReview,
		&execution.VisitNumber,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, fmt.Errorf("load task execution kickoff repair state: %w", err)
	}
	execution.ID = executionID
	execution.TaskID = taskRecord.ID
	execution.FlowNodeID = node.ID
	execution.SessionID = &sessionID
	execution.Status = "active"

	sequenceNumber := 1
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence_number), 0) + 1
		FROM chat_message
		WHERE session_id = $1
	`, sessionID).Scan(&sequenceNumber); err != nil {
		return uuid.Nil, fmt.Errorf("next task execution kickoff sequence number: %w", err)
	}

	metadata, err := json.Marshal(map[string]any{
		"source":                    "task_queue_processor",
		"flow_node_execution_id":    executionID.String(),
		"synthetic_user_message":    true,
		"recovered_missing_kickoff": true,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal task execution kickoff metadata: %w", err)
	}

	content := buildRecoveredTaskExecutionKickoffMessage(taskRecord, execution, &node)
	var messageID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO chat_message (
			session_id,
			turn_id,
			sequence_number,
			author_type,
			author_id,
			role,
			content,
			content_format,
			status,
			is_redacted,
			redacted_at,
			tool_call_id,
			error_message,
			metadata
		)
		VALUES ($1, NULL, $2, NULL, NULL, 'user', $3, 'text', 'pending', false, NULL, NULL, NULL, $4::jsonb)
		RETURNING id
	`, sessionID, sequenceNumber, content, metadata).Scan(&messageID); err != nil {
		return uuid.Nil, fmt.Errorf("create recovered task execution kickoff message: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("commit task execution kickoff repair: %w", err)
	}
	return messageID, nil
}

func buildRecoveredTaskExecutionKickoffMessage(taskRecord repo.ProjectTask, execution repo.FlowNodeExecution, node *repo.FlowNode) string {
	base := buildRecoveredTaskQueueKickoffMessage(taskRecord)
	if node != nil && (strings.EqualFold(strings.TrimSpace(node.NodeType), "review") || node.RequiresHumanReview) {
		title := strings.TrimSpace(taskRecord.Title)
		if title == "" {
			title = "Untitled task"
		}
		description := strings.TrimSpace(valueOrEmpty(taskRecord.Description))
		base = "Start review on task: " + title
		if description != "" {
			base += "\n\nTask description:\n" + description
		}
		base += "\n\nReview instruction:\nInspect the current deliverables and use flow.review_decision to approve or reject this review step. Approval closes with an empty review commit. Rejection may add review-scoped CriticMarkup notes."
	}
	if execution.ID == uuid.Nil {
		return base
	}
	return base + "\n\nFlow node execution: " + execution.ID.String()
}

func buildRecoveredTaskQueueKickoffMessage(taskRecord repo.ProjectTask) string {
	title := strings.TrimSpace(taskRecord.Title)
	if title == "" {
		title = "Untitled task"
	}

	description := strings.TrimSpace(valueOrEmpty(taskRecord.Description))
	base := "Start work on task: " + title
	if description != "" {
		base += "\n\nTask description:\n" + description
	}
	if recoveredTaskLooksLikeOrchestrationOnlyParent(taskRecord) {
		base += "\n\nExecution instruction:\nThis task is an orchestration-only parent container. Do not execute the parent deliverable directly. Inspect the current child-task set and create or repair bounded executable child tasks beneath this parent. Do not begin by rereading planning artifacts unless a concrete blocker names one."
	}
	return base
}

func recoveredTaskLooksLikeOrchestrationOnlyParent(taskRecord repo.ProjectTask) bool {
	if len(taskRecord.Metadata) != 0 && json.Valid(taskRecord.Metadata) {
		var payload map[string]any
		if err := json.Unmarshal(taskRecord.Metadata, &payload); err == nil {
			if decomp, _ := payload["decomposition"].(map[string]any); decomp != nil {
				if orchestrationOnly, _ := decomp["orchestration_only"].(bool); orchestrationOnly {
					return true
				}
			}
		}
	}

	titleText := strings.ToLower(strings.TrimSpace(taskRecord.Title))
	descriptionText := strings.ToLower(strings.TrimSpace(valueOrEmpty(taskRecord.Description)))
	text := titleText
	if descriptionText != "" {
		text += "\n" + descriptionText
	}

	titleLooksLikeWorkstream := strings.HasPrefix(titleText, "workstream ") || strings.HasPrefix(titleText, "ws")
	signals := []string{
		"parent orchestration task",
		"parent/orchestration task",
		"parent orchestration container",
		"orchestration container",
		"does not do execution work itself",
		"does not perform execution work itself",
		"does not perform execution work directly",
		"validates that child tasks",
		"validates child task outputs",
		"validates child outputs",
		"owns integration verification of its children",
	}
	matches := 0
	for _, signal := range signals {
		if strings.Contains(text, signal) {
			matches++
		}
	}
	if matches >= 2 {
		return true
	}
	return titleLooksLikeWorkstream && matches >= 1
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (w *Worker) countActionableProjectDraftTasks(ctx context.Context, projectID uuid.UUID) (int, error) {
	projectTasks, err := repo.NewProjectTaskRepo(w.pool).ListByProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	taskHintsByTask := buildProjectContinuationTaskHintsForWorker(projectTasks, nil)
	childActivity := projectContinuationChildTaskActivityForWorker(projectTasks, taskHintsByTask)
	malformedChildTaskIDs := projectContinuationMalformedChildTaskIDsForWorker(projectTasks)
	count := 0
	for _, task := range projectTasks {
		if _, skip := malformedChildTaskIDs[task.ID]; skip {
			continue
		}
		if isProjectContinuationActionableDraftTaskForWorker(task, childActivity[task.ID]) {
			count++
		}
	}
	return count, nil
}

func isActionableProjectDraftTaskForWorker(task repo.ProjectTask) bool {
	if !strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
		return false
	}
	var metadata map[string]any
	if err := json.Unmarshal(task.Metadata, &metadata); err == nil {
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			return false
		}
		if setupTask, _ := metadata["bootstrap_setup_task"].(bool); setupTask {
			return false
		}
	}
	return !looksLikeProjectContinuationMetaDraftForWorker(task.Title, task.Description)
}

func looksLikeProjectContinuationMetaDraftForWorker(title string, description *string) bool {
	descriptionText := ""
	if description != nil {
		descriptionText = *description
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(title+" "+descriptionText)), " "))
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "review and promote draft tasks") {
		return true
	}
	if strings.Contains(normalized, "review and validate integration test results") {
		return true
	}
	if strings.Contains(normalized, "analyze results of integration test") {
		return true
	}
	if strings.Contains(normalized, "review and update pipeline test results") {
		return true
	}
	if strings.Contains(normalized, "review and validate pipeline integration test results") {
		return true
	}
	if strings.Contains(normalized, "analyze test results and document findings") {
		return true
	}
	if strings.Contains(normalized, "select and decompose next bounded task") {
		return true
	}
	if strings.Contains(normalized, "review and prepare for next phase") {
		return true
	}
	if strings.Contains(normalized, "review and promote task ") {
		return true
	}
	if strings.Contains(normalized, "prepare for next phase of project execution") {
		return true
	}
	if strings.Contains(normalized, "end to end pipeline integration test") &&
		(strings.Contains(normalized, "review") ||
			strings.Contains(normalized, "validate") ||
			strings.Contains(normalized, "verify the outcomes") ||
			strings.Contains(normalized, "analyze") ||
			strings.Contains(normalized, "inspect") ||
			strings.Contains(normalized, "update") ||
			strings.Contains(normalized, "document findings") ||
			strings.Contains(normalized, "document detailed findings") ||
			strings.Contains(normalized, "identify any issues or anomalies") ||
			strings.Contains(normalized, "identify any issues or failures") ||
			strings.Contains(normalized, "document any findings") ||
			strings.Contains(normalized, "required updates") ||
			strings.Contains(normalized, "all components are functioning correctly") ||
			strings.Contains(normalized, "results")) {
		return true
	}
	if strings.Contains(normalized, "next runnable bounded task") &&
		(strings.Contains(normalized, "decompose") ||
			strings.Contains(normalized, "assign") ||
			strings.Contains(normalized, "queue it for execution")) {
		return true
	}
	if strings.Contains(normalized, "prepare the next set of tasks") &&
		(strings.Contains(normalized, "inspect the results") ||
			strings.Contains(normalized, "integration test") ||
			strings.Contains(normalized, "next phase")) {
		return true
	}
	if strings.Contains(normalized, "transition it to the next phase") &&
		(strings.Contains(normalized, "mark as complete") ||
			strings.Contains(normalized, "review the output of task")) {
		return true
	}
	if strings.Contains(normalized, "remaining draft task") &&
		(strings.Contains(normalized, "review") ||
			strings.Contains(normalized, "promote") ||
			strings.Contains(normalized, "runnable") ||
			strings.Contains(normalized, "next phase")) {
		return true
	}
	if strings.Contains(normalized, "draft project task") &&
		(strings.Contains(normalized, "inspect") || strings.Contains(normalized, "promote")) {
		return true
	}
	return false
}

type projectExecutionContinuationSnapshotForWorker struct {
	ProjectLine          string
	ActiveTaskLine       string
	CompletedTaskLine    string
	LeafActiveTaskLine   string
	DraftTaskLine        string
	ReplacementDraftLine string
	ChildActiveDraftLine string
	FocusTaskLine        string
}

type projectContinuationTaskHintsForWorker struct {
	BlockedReason   string
	ResumePolicy    string
	DeliverablePath string
	DeliverableRoot string
	DependsOnPath   string
	BatchRange      string
}

const projectContinuationBlockerExcerptLimitForWorker = 120

type projectContinuationChildActivityForWorker struct {
	childTaskCount                   int
	activeChildTaskCount             int
	malformedChildTaskCount          int
	replaceableBlockedChildTaskCount int
}

func isProjectContinuationActionableDraftTaskForWorker(task repo.ProjectTask, activity projectContinuationChildActivityForWorker) bool {
	return isActionableProjectDraftTaskForWorker(task) && activity.childTaskCount == 0
}

func projectContinuationDraftTaskNeedsFreshReplacementChildWorkForWorker(activity projectContinuationChildActivityForWorker) bool {
	if activity.activeChildTaskCount != 0 {
		return false
	}
	if activity.malformedChildTaskCount > 0 && activity.childTaskCount == 0 {
		return true
	}
	return activity.childTaskCount > 0 &&
		activity.replaceableBlockedChildTaskCount > 0 &&
		activity.replaceableBlockedChildTaskCount == activity.childTaskCount
}

func (w *Worker) projectExecutionContinuationSnapshot(ctx context.Context, projectID uuid.UUID) (projectExecutionContinuationSnapshotForWorker, error) {
	snapshot := projectExecutionContinuationSnapshotForWorker{}
	if w == nil || w.pool == nil || projectID == uuid.Nil {
		return snapshot, nil
	}
	snapshot.ProjectLine = "Active project id: " + projectID.String()
	projectTasks, err := repo.NewProjectTaskRepo(w.pool).ListByProject(ctx, projectID)
	if err != nil {
		return snapshot, err
	}
	taskHintsByTask, err := w.projectContinuationTaskHintsByTask(ctx, projectTasks)
	if err != nil {
		return snapshot, err
	}
	childActivity := projectContinuationChildTaskActivityForWorker(projectTasks, taskHintsByTask)
	malformedChildTaskIDs := projectContinuationMalformedChildTaskIDsForWorker(projectTasks)
	activeTasks := make([]string, 0, 4)
	completedTasks := make([]string, 0, 4)
	leafActiveTasks := make([]string, 0, 4)
	draftTasks := make([]string, 0, 4)
	replacementDraftTasks := make([]string, 0, 4)
	childActiveDraftTasks := make([]string, 0, 4)
	completedBatchFamilies := projectContinuationRelevantCompletedBatchFamiliesForWorker(projectTasks, taskHintsByTask, malformedChildTaskIDs)
	focusTask := ""
	for _, task := range projectTasks {
		if _, skip := malformedChildTaskIDs[task.ID]; skip {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(task.WorkStatus))
		if status == "" || status == "cancelled" {
			continue
		}
		activity := childActivity[task.ID]
		hints := taskHintsByTask[task.ID]
		taskRef := projectExecutionContinuationTaskRefForWorker(task, activity, hints)
		if status == "done" {
			if projectContinuationTaskMatchesCompletedBatchFamiliesForWorker(hints, completedBatchFamilies) && len(completedTasks) < 4 {
				completedTasks = append(completedTasks, taskRef)
			}
			continue
		}
		if isActionableProjectDraftTaskForWorker(task) {
			if activity.childTaskCount > 0 || activity.malformedChildTaskCount > 0 {
				if projectContinuationDraftTaskNeedsFreshReplacementChildWorkForWorker(activity) {
					if len(replacementDraftTasks) < 4 {
						replacementDraftTasks = append(replacementDraftTasks, taskRef)
					}
					if focusTask == "" {
						focusTask = taskRef
					}
					continue
				}
				if activity.childTaskCount > 0 && len(childActiveDraftTasks) < 4 {
					childActiveDraftTasks = append(childActiveDraftTasks, taskRef)
				}
				continue
			}
			if len(draftTasks) < 4 {
				draftTasks = append(draftTasks, taskRef)
			}
			if focusTask == "" {
				focusTask = taskRef
			}
			continue
		}
		if len(activeTasks) < 4 {
			activeTasks = append(activeTasks, taskRef)
		}
		if activity.childTaskCount == 0 && len(leafActiveTasks) < 4 {
			leafActiveTasks = append(leafActiveTasks, fmt.Sprintf("%s leaf_task_id=%s", projectTaskLabelForWorker(task), task.ID.String()))
		}
	}
	if len(activeTasks) > 0 {
		snapshot.ActiveTaskLine = "Already-active non-terminal tasks in the tree: " + strings.Join(activeTasks, "; ")
	}
	if len(completedTasks) > 0 {
		snapshot.CompletedTaskLine = "Recently completed bounded tasks already in the tree: " + strings.Join(completedTasks, "; ")
	}
	if len(leafActiveTasks) > 0 {
		snapshot.LeafActiveTaskLine = "Active leaf tasks already have no child tasks to inspect: " + strings.Join(leafActiveTasks, "; ")
	}
	if len(draftTasks) > 0 {
		snapshot.DraftTaskLine = "Actionable draft tasks already in the tree: " + strings.Join(draftTasks, "; ")
	}
	if len(replacementDraftTasks) > 0 {
		snapshot.ReplacementDraftLine = "Draft parent tasks need fresh replacement child work: " + strings.Join(replacementDraftTasks, "; ")
	}
	if len(childActiveDraftTasks) > 0 {
		snapshot.ChildActiveDraftLine = "Draft parent tasks already have child work: " + strings.Join(childActiveDraftTasks, "; ")
	}
	if focusTask != "" {
		snapshot.FocusTaskLine = "Start from this existing actionable draft before broad rediscovery if it is still the next bounded step: " + focusTask
	}
	return snapshot, nil
}

func projectContinuationRelevantCompletedBatchFamiliesForWorker(
	projectTasks []repo.ProjectTask,
	taskHintsByTask map[uuid.UUID]projectContinuationTaskHintsForWorker,
	malformedChildTaskIDs map[uuid.UUID]struct{},
) map[string]struct{} {
	families := make(map[string]struct{})
	for _, task := range projectTasks {
		if _, skip := malformedChildTaskIDs[task.ID]; skip {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(task.WorkStatus))
		if status == "" || status == "done" || status == "cancelled" {
			continue
		}
		hints := taskHintsByTask[task.ID]
		if strings.TrimSpace(hints.BatchRange) == "" {
			continue
		}
		for _, key := range projectContinuationBatchFamilyKeysForWorker(hints) {
			families[key] = struct{}{}
		}
	}
	return families
}

func projectContinuationTaskMatchesCompletedBatchFamiliesForWorker(hints projectContinuationTaskHintsForWorker, families map[string]struct{}) bool {
	if len(families) == 0 || strings.TrimSpace(hints.BatchRange) == "" {
		return false
	}
	for _, key := range projectContinuationBatchFamilyKeysForWorker(hints) {
		if _, ok := families[key]; ok {
			return true
		}
	}
	return false
}

func projectContinuationBatchFamilyKeysForWorker(hints projectContinuationTaskHintsForWorker) []string {
	keys := make([]string, 0, 4)
	if deliverablePath := strings.ToLower(strings.TrimSpace(hints.DeliverablePath)); deliverablePath != "" {
		keys = append(keys, "path:"+deliverablePath)
	}
	if deliverableRoot := strings.ToLower(strings.TrimSpace(hints.DeliverableRoot)); deliverableRoot != "" {
		keys = append(keys, "root:"+deliverableRoot)
	}
	if dependsOnPath := strings.ToLower(strings.TrimSpace(hints.DependsOnPath)); dependsOnPath != "" {
		keys = append(keys, "depends:"+dependsOnPath)
	}
	if batchRange := strings.TrimSpace(hints.BatchRange); batchRange != "" {
		keys = append(keys, "batch:"+batchRange)
	}
	return keys
}

func (w *Worker) projectContinuationTaskHintsByTask(ctx context.Context, tasks []repo.ProjectTask) (map[uuid.UUID]projectContinuationTaskHintsForWorker, error) {
	blockedReasons, err := w.projectContinuationBlockedReasonsByTask(ctx, tasks)
	if err != nil {
		return nil, err
	}
	return buildProjectContinuationTaskHintsForWorker(tasks, blockedReasons), nil
}

func (w *Worker) projectContinuationBlockedReasonsByTask(ctx context.Context, tasks []repo.ProjectTask) (map[uuid.UUID]string, error) {
	reasons := make(map[uuid.UUID]string)
	if w == nil || w.pool == nil || len(tasks) == 0 {
		return reasons, nil
	}

	blockedTaskIDs := make([]uuid.UUID, 0, len(tasks))
	for _, task := range tasks {
		if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "blocked") {
			blockedTaskIDs = append(blockedTaskIDs, task.ID)
		}
	}
	if len(blockedTaskIDs) == 0 {
		return reasons, nil
	}
	return repo.NewProjectTaskEventRepo(w.pool).LatestBlockedReasonsByTask(ctx, blockedTaskIDs)
}

func buildProjectContinuationTaskHintsForWorker(tasks []repo.ProjectTask, blockedReasons map[uuid.UUID]string) map[uuid.UUID]projectContinuationTaskHintsForWorker {
	hintsByTask := make(map[uuid.UUID]projectContinuationTaskHintsForWorker, len(tasks))
	tasksByID := make(map[uuid.UUID]repo.ProjectTask, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	for _, task := range tasks {
		blockedReason := strings.TrimSpace(blockedReasons[task.ID])
		deliverablePath, deliverableRoot := projectContinuationTaskDeliverableHintsForWorker(task, tasksByID)
		hintsByTask[task.ID] = projectContinuationTaskHintsForWorker{
			BlockedReason:   blockedReason,
			ResumePolicy:    projectContinuationTaskResumePolicyForWorker(task, blockedReason),
			DeliverablePath: deliverablePath,
			DeliverableRoot: deliverableRoot,
			DependsOnPath:   projectContinuationTaskDependencyHintPathForWorker(task, tasks),
			BatchRange:      projectContinuationTaskBatchRangeForWorker(task),
		}
	}
	return hintsByTask
}

func projectContinuationTaskBatchRangeForWorker(task repo.ProjectTask) string {
	text := strings.TrimSpace(task.Title)
	if task.Description != nil {
		description := strings.TrimSpace(*task.Description)
		if description != "" {
			if text != "" {
				text += "\n"
			}
			text += description
		}
	}
	return projectContinuationBatchRangeFromTextForWorker(text)
}

func projectContinuationBatchRangeFromTextForWorker(text string) string {
	matches := projectContinuationBatchRangePatternForWorker.FindStringSubmatch(text)
	if len(matches) < 3 {
		return ""
	}
	start := strings.TrimSpace(matches[1])
	end := strings.TrimSpace(matches[2])
	if start == "" || end == "" {
		return ""
	}
	return start + "-" + end
}

func projectContinuationTaskDeliverableHintsForWorker(task repo.ProjectTask, tasksByID map[uuid.UUID]repo.ProjectTask) (string, string) {
	if explicit := strings.TrimSpace(explicitDeliverablePathForWorker(task)); explicit != "" {
		return explicit, ""
	}
	if root := strings.TrimSpace(preferredTaskDeliverableRootForWorker(task)); root != "" {
		return "", root
	}
	var metadata map[string]any
	if err := json.Unmarshal(task.Metadata, &metadata); err != nil {
		return "", ""
	}
	parentIDText := strings.TrimSpace(fmt.Sprint(metadata["decomposition_parent_task_id"]))
	parentID, err := uuid.Parse(parentIDText)
	if err != nil || parentID == uuid.Nil {
		return "", ""
	}
	parentTask, ok := tasksByID[parentID]
	if !ok {
		return "", ""
	}
	if explicit := strings.TrimSpace(explicitDeliverablePathForWorker(parentTask)); explicit != "" {
		return explicit, ""
	}
	if root := strings.TrimSpace(preferredTaskDeliverableRootForWorker(parentTask)); root != "" {
		return "", root
	}
	return "", ""
}

func projectContinuationTaskDependencyHintPathForWorker(task repo.ProjectTask, tasks []repo.ProjectTask) string {
	if !projectContinuationTaskReferencesURLIndexForWorker(task) {
		return ""
	}
	for _, candidate := range tasks {
		if candidate.ID == task.ID {
			continue
		}
		explicit := strings.TrimSpace(explicitDeliverablePathForWorker(candidate))
		if explicit == "" {
			continue
		}
		if !projectContinuationTaskReferencesURLIndexForWorker(candidate) && !strings.Contains(strings.ToLower(explicit), "index") {
			continue
		}
		return explicit
	}
	return ""
}

func projectContinuationTaskReferencesURLIndexForWorker(task repo.ProjectTask) bool {
	text := strings.ToLower(strings.TrimSpace(task.Title))
	if task.Description != nil {
		text += " " + strings.ToLower(strings.TrimSpace(*task.Description))
	}
	return strings.Contains(text, "url index") || strings.Contains(text, "technonymous-index.json")
}

func projectContinuationChildTaskActivityForWorker(tasks []repo.ProjectTask, hintsByTask map[uuid.UUID]projectContinuationTaskHintsForWorker) map[uuid.UUID]projectContinuationChildActivityForWorker {
	activityByParentID := make(map[uuid.UUID]projectContinuationChildActivityForWorker)
	malformedChildTaskIDs := projectContinuationMalformedChildTaskIDsForWorker(tasks)
	for _, task := range tasks {
		var metadata map[string]any
		if err := json.Unmarshal(task.Metadata, &metadata); err != nil {
			continue
		}
		rawParentID, ok := metadata["decomposition_parent_task_id"]
		if !ok {
			continue
		}
		parentIDText := strings.TrimSpace(fmt.Sprint(rawParentID))
		parentID, err := uuid.Parse(parentIDText)
		if err != nil || parentID == uuid.Nil {
			continue
		}
		if _, skip := malformedChildTaskIDs[task.ID]; skip {
			activity := activityByParentID[parentID]
			activity.malformedChildTaskCount++
			activityByParentID[parentID] = activity
			continue
		}
		activity := activityByParentID[parentID]
		activity.childTaskCount++
		if projectTaskExecutionActiveForWorker(task.WorkStatus) {
			activity.activeChildTaskCount++
		}
		if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "blocked") {
			switch strings.TrimSpace(hintsByTask[task.ID].ResumePolicy) {
			case "terminal_keep_blocked", "needs_replacement_work":
				activity.replaceableBlockedChildTaskCount++
			}
		}
		activityByParentID[parentID] = activity
	}
	return activityByParentID
}

func projectContinuationMalformedChildTaskIDsForWorker(tasks []repo.ProjectTask) map[uuid.UUID]struct{} {
	if len(tasks) == 0 {
		return nil
	}
	tasksByID := make(map[uuid.UUID]repo.ProjectTask, len(tasks))
	for _, task := range tasks {
		tasksByID[task.ID] = task
	}
	var malformed map[uuid.UUID]struct{}
	for _, task := range tasks {
		var metadata map[string]any
		if err := json.Unmarshal(task.Metadata, &metadata); err != nil {
			continue
		}
		parentIDText := strings.TrimSpace(fmt.Sprint(metadata["decomposition_parent_task_id"]))
		parentID, err := uuid.Parse(parentIDText)
		if err != nil || parentID == uuid.Nil {
			continue
		}
		parentTask, ok := tasksByID[parentID]
		if !ok {
			continue
		}
		if !projectContinuationParentForbidsDecompositionForWorker(parentTask) &&
			!taskdecomp.TaskLooksProceduralInstructionArtifact(task.Title, task.Description) {
			continue
		}
		if malformed == nil {
			malformed = make(map[uuid.UUID]struct{})
		}
		malformed[task.ID] = struct{}{}
	}
	return malformed
}

func projectContinuationParentForbidsDecompositionForWorker(task repo.ProjectTask) bool {
	var metadata map[string]any
	if err := json.Unmarshal(task.Metadata, &metadata); err != nil {
		return false
	}
	decomposition, _ := metadata["decomposition"].(map[string]any)
	sourceDescription := strings.TrimSpace(fmt.Sprint(decomposition["source_description"]))
	return sourceDescription != "" && taskdecomp.DescriptionForbidsDecomposition(sourceDescription)
}

func projectTaskExecutionActiveForWorker(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "in_progress", "review":
		return true
	default:
		return false
	}
}

func projectExecutionContinuationTaskRefForWorker(task repo.ProjectTask, activity projectContinuationChildActivityForWorker, hints projectContinuationTaskHintsForWorker) string {
	parts := []string{projectTaskLabelForWorker(task), "id=" + task.ID.String()}
	if title := strings.TrimSpace(task.Title); title != "" && task.TaskNumber > 0 {
		parts = append(parts, "title="+strconv.Quote(title))
	}
	if status := strings.TrimSpace(task.WorkStatus); status != "" {
		parts = append(parts, "work_status="+status)
	}
	if deliverablePath := strings.TrimSpace(hints.DeliverablePath); deliverablePath != "" {
		parts = append(parts, "deliverable_path="+deliverablePath)
	} else if deliverableRoot := strings.TrimSpace(hints.DeliverableRoot); deliverableRoot != "" {
		parts = append(parts, "deliverable_root="+deliverableRoot)
	}
	if dependsOnPath := strings.TrimSpace(hints.DependsOnPath); dependsOnPath != "" &&
		(strings.TrimSpace(hints.DeliverablePath) == "" || !sameWorkspaceRelativePathForWorker(dependsOnPath, hints.DeliverablePath)) {
		parts = append(parts, "depends_on_path="+dependsOnPath)
	}
	if batchRange := strings.TrimSpace(hints.BatchRange); batchRange != "" {
		parts = append(parts, "batch_range="+batchRange)
	}
	if task.AssignedAgentID != nil && *task.AssignedAgentID != uuid.Nil {
		parts = append(parts, "assigned_agent_id="+task.AssignedAgentID.String())
	} else if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
		parts = append(parts, "assigned_agent_id=missing")
	}
	if task.FlowTemplateID != nil && *task.FlowTemplateID != uuid.Nil {
		parts = append(parts, "flow_template_id="+task.FlowTemplateID.String())
	} else if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "draft") {
		parts = append(parts, "flow_template_id=missing")
	}
	if task.RequiresHumanReview {
		parts = append(parts, "requires_human_review=true")
	}
	if strings.EqualFold(strings.TrimSpace(task.WorkStatus), "blocked") {
		if hints.ResumePolicy != "" {
			parts = append(parts, "resume_policy="+hints.ResumePolicy)
		}
		if excerpt := projectContinuationBlockedReasonExcerptForWorker(hints.BlockedReason); excerpt != "" {
			parts = append(parts, "blocker="+strconv.Quote(excerpt))
		}
	}
	if activity.activeChildTaskCount > 0 {
		parts = append(parts, "active_child_tasks="+strconv.Itoa(activity.activeChildTaskCount))
	} else if activity.childTaskCount > 0 {
		parts = append(parts, "child_tasks="+strconv.Itoa(activity.childTaskCount))
		if activity.replaceableBlockedChildTaskCount > 0 {
			parts = append(parts, "replaceable_blocked_child_tasks="+strconv.Itoa(activity.replaceableBlockedChildTaskCount))
		}
	}
	if activity.malformedChildTaskCount > 0 {
		parts = append(parts, "malformed_child_tasks="+strconv.Itoa(activity.malformedChildTaskCount))
	}
	return strings.Join(parts, " ")
}

func projectContinuationBlockedReasonExcerptForWorker(reason string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(reason)), " ")
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= projectContinuationBlockerExcerptLimitForWorker {
		return normalized
	}
	cut := strings.TrimSpace(string(runes[:projectContinuationBlockerExcerptLimitForWorker]))
	if idx := strings.LastIndexAny(cut, " ,.;:"); idx >= projectContinuationBlockerExcerptLimitForWorker/2 {
		cut = strings.TrimSpace(cut[:idx])
	}
	if cut == "" {
		cut = strings.TrimSpace(string(runes[:projectContinuationBlockerExcerptLimitForWorker]))
	}
	return cut + "..."
}

func projectContinuationBlockedTaskResumePolicyForWorker(reason string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(reason)), " "))
	switch {
	case strings.Contains(normalized, "flow rejection max visits exceeded"):
		return "terminal_keep_blocked"
	case strings.Contains(normalized, "recovery halted after recovered file.write"):
		return "manual_recovery_repair"
	case strings.Contains(normalized, "review turn completed without calling flow.review_decision"):
		return "resume_review_decision"
	case strings.Contains(normalized, "review turn repeatedly hit"):
		return "needs_replacement_work"
	default:
		return ""
	}
}

func projectContinuationTaskResumePolicyForWorker(task repo.ProjectTask, blockedReason string) string {
	if failureCode, blocked := projectContinuationValidationGuardFailureCodeForWorker(task.Metadata); blocked {
		switch strings.ToLower(strings.TrimSpace(failureCode)) {
		case "review_action_required", "review_decision_required":
			return "resume_review_decision"
		}
	}
	return projectContinuationBlockedTaskResumePolicyForWorker(blockedReason)
}

func projectContinuationValidationGuardFailureCodeForWorker(metadata json.RawMessage) (string, bool) {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return "", false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &payload); err != nil {
		return "", false
	}
	raw := payload["agent_turn_validation_guard"]
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var guard struct {
		FailureCode string `json:"failure_code"`
		Blocked     bool   `json:"blocked"`
		Count       int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &guard); err != nil {
		return "", false
	}
	return guard.FailureCode, guard.Blocked || guard.Count > 0
}

func explicitDeliverablePathForWorker(task repo.ProjectTask) string {
	if task.Description == nil {
		return ""
	}
	description := strings.TrimSpace(*task.Description)
	for _, pattern := range explicitDeliverablePathPatternsForWorker {
		matches := pattern.FindStringSubmatch(description)
		if len(matches) < 2 {
			continue
		}
		rawCandidate := strings.TrimSpace(matches[1])
		candidate := normalizeExplicitDeliverablePathCandidateForWorker(rawCandidate)
		if !looksLikeExplicitDeliverablePathForWorker(candidate, rawCandidate) {
			continue
		}
		return candidate
	}
	return ""
}

func preferredTaskDeliverableRootForWorker(task repo.ProjectTask) string {
	if task.Description == nil {
		return ""
	}
	description := strings.TrimSpace(*task.Description)
	for _, pattern := range preferredDeliverableRootPatternsForWorker {
		matches := pattern.FindStringSubmatch(description)
		if len(matches) < 2 {
			continue
		}
		root := normalizeExplicitDeliverablePathCandidateForWorker(matches[1])
		if !looksLikePreferredDeliverableRootPathForWorker(root) {
			continue
		}
		return root
	}
	return ""
}

func normalizeExplicitDeliverablePathCandidateForWorker(raw string) string {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.Trim(trimmed, "`'\"“”‘’()[]{}")
	trimmed = strings.TrimRight(trimmed, ".,:;!?")
	trimmed = strings.Trim(trimmed, "`'\"“”‘’()[]{}")
	return normalizeWorkspaceRelativePathForWorker(trimmed)
}

func looksLikeExplicitDeliverablePathForWorker(normalized, raw string) bool {
	if normalized == "" {
		return false
	}
	if strings.Contains(normalized, "/") || strings.Contains(filepath.Base(normalized), ".") {
		return true
	}
	trimmedRaw := strings.TrimSpace(raw)
	if trimmedRaw == "" {
		return false
	}
	for _, r := range trimmedRaw {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func looksLikePreferredDeliverableRootPathForWorker(normalized string) bool {
	normalized = normalizeWorkspaceRelativePathForWorker(normalized)
	if normalized == "" {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(normalized), "content/") {
		return false
	}
	return !strings.Contains(filepath.Base(normalized), ".")
}

func normalizeWorkspaceRelativePathForWorker(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(trimmed)))
}

func sameWorkspaceRelativePathForWorker(left, right string) bool {
	left = normalizeWorkspaceRelativePathForWorker(left)
	right = normalizeWorkspaceRelativePathForWorker(right)
	return left != "" && right != "" && left == right
}

func projectTaskLabelForWorker(task repo.ProjectTask) string {
	title := strings.TrimSpace(task.Title)
	switch {
	case task.TaskNumber > 0 && title != "":
		return fmt.Sprintf("task %d (%s)", task.TaskNumber, title)
	case task.TaskNumber > 0:
		return fmt.Sprintf("task %d", task.TaskNumber)
	case title != "":
		return title
	default:
		return task.ID.String()
	}
}

func projectExecutionContinuationFingerprintForWorker(completedTaskID uuid.UUID, remainingDraftTasks int, snapshot projectExecutionContinuationSnapshotForWorker) string {
	hasher := fnv.New64a()
	parts := []string{
		strings.TrimSpace(versionpkg.RepoVersion),
		completedTaskID.String(),
		strconv.Itoa(remainingDraftTasks),
		strings.TrimSpace(snapshot.ProjectLine),
		strings.TrimSpace(snapshot.ActiveTaskLine),
		strings.TrimSpace(snapshot.DraftTaskLine),
		strings.TrimSpace(snapshot.ReplacementDraftLine),
		strings.TrimSpace(snapshot.ChildActiveDraftLine),
		strings.TrimSpace(snapshot.FocusTaskLine),
	}
	for _, part := range parts {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return strconv.FormatUint(hasher.Sum64(), 16)
}

func appendProjectExecutionSnapshotGuidanceForWorker(lines []string, snapshot projectExecutionContinuationSnapshotForWorker) []string {
	if projectLine := strings.TrimSpace(snapshot.ProjectLine); projectLine != "" {
		lines = append(lines, projectLine)
		lines = append(lines, "The active project id above is not a task_id. Do not pass it to task.get; use only the named task ids from the snapshot below when a task-specific action is actually required.")
	}
	if activeLine := strings.TrimSpace(snapshot.ActiveTaskLine); activeLine != "" {
		lines = append(lines, activeLine)
		lines = append(lines, "Do not begin with session.list, task.list, task.get, file.list on the workspace root, git.log, or git.status just to rediscover what the named active tasks are already doing.")
		if strings.Contains(activeLine, "blocker=") {
			lines = append(lines, "If a named active task above already includes blocker=..., act directly on that blocker summary instead of rereading task.get just to rediscover the same reason.")
		}
		if strings.Contains(activeLine, "resume_policy=terminal_keep_blocked") {
			lines = append(lines, "If a named blocked task above already shows resume_policy=terminal_keep_blocked, leave it blocked from the project lane and work around it instead of rereading or retrying it.")
		}
		if strings.Contains(activeLine, "resume_policy=needs_replacement_work") {
			lines = append(lines, "If a named blocked task above already shows resume_policy=needs_replacement_work, create or queue the smallest replacement or follow-on work needed to recover it instead of broad rediscovery.")
		}
		if strings.Contains(activeLine, "resume_policy=resume_review_decision") {
			lines = append(lines, "If a named blocked task above already shows resume_policy=resume_review_decision, resume or requeue that exact review lane so it can call flow.review_decision; do not create replacement work for it.")
		}
		if strings.Contains(activeLine, "resume_policy=manual_recovery_repair") {
			lines = append(lines, "If a named blocked task above already shows resume_policy=manual_recovery_repair, queue only the targeted manual repair needed for that deliverable instead of broader session or project listing.")
		}
		if strings.Contains(activeLine, "deliverable_path=") {
			lines = append(lines, "If a named active task above already shows deliverable_path=..., inspect or write that exact path instead of broad workspace-root or artifact-root browsing.")
		}
		if strings.Contains(activeLine, "deliverable_root=") {
			lines = append(lines, "If a named active task above already shows deliverable_root=..., stay inside that exact root instead of broad content, templates, or planning rediscovery.")
		}
		if strings.Contains(activeLine, "depends_on_path=") {
			lines = append(lines, "If a named active task above already shows depends_on_path=..., inspect that prerequisite artifact first instead of broad search.")
		}
		if strings.Contains(activeLine, "flow_template_id=") && !strings.Contains(activeLine, "flow_template_id=missing") {
			lines = append(lines, "If the named tasks above already show concrete flow_template_id values, do not call flow.list_templates just to reconfirm template availability.")
		}
	}
	if completedLine := strings.TrimSpace(snapshot.CompletedTaskLine); completedLine != "" {
		lines = append(lines, completedLine)
		lines = append(lines, "Do not create or queue replacement work for a batch_range already listed in the completed-task snapshot above unless that completed task failed to produce its deliverables.")
	}
	if leafLine := strings.TrimSpace(snapshot.LeafActiveTaskLine); leafLine != "" {
		lines = append(lines, leafLine)
		lines = append(lines, "Do not call task.list(parent_task_id=...) for those named leaf tasks. They already have no child tasks to inspect, so act on the task's exact deliverable or blocker instead.")
	}
	if draftLine := strings.TrimSpace(snapshot.DraftTaskLine); draftLine != "" {
		lines = append(lines, draftLine)
		lines = append(lines, "Do not begin with broad project.get, task.list, task.get, or flow.get_execution rediscovery when the actionable draft tasks above already identify the remaining bounded work.")
		lines = append(lines, "If a named draft task above shows assigned_agent_id=missing, flow_template_id=missing, or requires_human_review=true, repair that exact prerequisite before trying to queue it.")
		if strings.Contains(draftLine, "deliverable_path=") {
			lines = append(lines, "If a named draft task above already shows deliverable_path=..., inspect or write that exact path instead of reopening broad workspace context.")
		}
		if strings.Contains(draftLine, "deliverable_root=") {
			lines = append(lines, "If a named draft task above already shows deliverable_root=..., stay inside that exact root instead of broad content or template browsing.")
		}
		if strings.Contains(draftLine, "depends_on_path=") {
			lines = append(lines, "If a named draft task above already shows depends_on_path=..., inspect that prerequisite artifact before broader search.")
		}
	}
	if replacementLine := strings.TrimSpace(snapshot.ReplacementDraftLine); replacementLine != "" {
		lines = append(lines, replacementLine)
		lines = append(lines, "Those draft parents no longer have active child execution. Their existing child lanes are terminally blocked or malformed, so create or queue the smallest replacement child task under the correct parent now instead of broad rediscovery.")
		lines = append(lines, "Do not browse broad workspace roots, task trees, or flow templates first when the replacement parent already names the dependency or deliverable path.")
	}
	if parentLine := strings.TrimSpace(snapshot.ChildActiveDraftLine); parentLine != "" {
		lines = append(lines, parentLine)
		lines = append(lines, "Do not queue, re-decompose, or broadly rediscover those parent draft tasks again from the project lane while those child tasks already exist. Let active child lanes continue, or inspect only that parent's direct children with parent_task_id if a concrete blocker must be verified.")
	}
	if focusLine := strings.TrimSpace(snapshot.FocusTaskLine); focusLine != "" {
		lines = append(lines, focusLine)
		if strings.Contains(focusLine, "child_tasks=") && strings.TrimSpace(snapshot.ActiveTaskLine) != "" {
			lines = append(lines, "If that focus draft already has child tasks and the active-task snapshot above already names those child lanes, do not reread the parent or child task records first. Queue the draft only if it is still runnable as-is, split it directly into smaller reviewable work if bounded-size policy still blocks it, or report one concrete blocker sentence.")
		}
		if strings.Contains(focusLine, "deliverable_path=") || strings.Contains(focusLine, "deliverable_root=") || strings.Contains(focusLine, "depends_on_path=") {
			lines = append(lines, "When the focus task already includes exact deliverable or dependency hints, use those paths directly before any broader workspace search.")
		}
		if strings.Contains(focusLine, "replaceable_blocked_child_tasks=") {
			lines = append(lines, "Because that focus parent only has terminally blocked child lanes, create or queue the smallest fresh replacement child task under it now instead of rereading broad task trees, workspace roots, or flow templates.")
		}
		if strings.Contains(focusLine, "malformed_child_tasks=") {
			lines = append(lines, "Because that focus parent only has malformed or stale child artifact lanes, do not queue the parent again from the project lane. Create the smallest fresh replacement child task under it now instead.")
		}
	}
	return lines
}

func projectExecutionSnapshotContainsBatchRangeForWorker(snapshot projectExecutionContinuationSnapshotForWorker, batchRange string) bool {
	batchRange = strings.TrimSpace(batchRange)
	if batchRange == "" {
		return false
	}
	needle := "batch_range=" + batchRange
	for _, line := range []string{
		snapshot.ActiveTaskLine,
		snapshot.CompletedTaskLine,
		snapshot.LeafActiveTaskLine,
		snapshot.DraftTaskLine,
		snapshot.ReplacementDraftLine,
		snapshot.ChildActiveDraftLine,
		snapshot.FocusTaskLine,
	} {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func buildProjectExecutionContinuationPromptForWorker(completedTaskNumber int, completedTaskTitle string, remainingDraftTasks int, snapshot projectExecutionContinuationSnapshotForWorker) string {
	lines := []string{
		"Continue the active project execution now.",
		"Recently completed work may have unlocked the next wave of bounded tasks.",
	}
	if completedTaskNumber > 0 || strings.TrimSpace(completedTaskTitle) != "" {
		label := strings.TrimSpace(completedTaskTitle)
		if completedTaskNumber > 0 && label != "" {
			label = fmt.Sprintf("task %d (%s)", completedTaskNumber, label)
		} else if completedTaskNumber > 0 {
			label = fmt.Sprintf("task %d", completedTaskNumber)
		}
		lines = append(lines, fmt.Sprintf("The latest completed task was %s.", label))
	}
	completedBatchRange := projectContinuationBatchRangeFromTextForWorker(completedTaskTitle)
	if completedBatchRange != "" {
		lines = append(lines, "That completed task covers batch_range="+completedBatchRange+".")
	}
	if remainingDraftTasks > 0 {
		lines = append(lines, fmt.Sprintf("There are %d remaining draft project tasks that still need selection, orchestration, or promotion.", remainingDraftTasks))
	}
	lines = append(lines,
		"Your next response must take direct project action instead of generic chat.",
		"Do not ask what to do next, do not restate the project, and do not reread broad context before acting.",
		"Do not ask which function to call, do not offer to format JSON or tool calls, and do not ask the user for parameters that are already in the project session.",
		"This project already exists. Do not call project.create again unless the user explicitly asks to start a different new project.",
		"Inspect the current task tree and immediately move the next bounded work forward by selecting, decomposing, assigning, or queueing the correct tasks.",
		"Do not treat a completed gate-review or sign-off task as proof that the whole project is complete while any draft tasks still remain.",
		"Do not use task.update to mark untouched draft tasks done; queue or otherwise advance the next runnable task instead.",
		"If the remaining work is blocked on a concrete prerequisite, report that blocker in one sentence instead of narrating intent.",
	)
	lines = appendProjectExecutionSnapshotGuidanceForWorker(lines, snapshot)
	if completedBatchRange != "" && projectExecutionSnapshotContainsBatchRangeForWorker(snapshot, completedBatchRange) {
		lines = append(lines,
			"If older blocked, replacement-eligible, or already-active tasks above also show batch_range="+completedBatchRange+", treat those older lanes as superseded by the latest completed batch unless the completed task itself failed to produce the batch deliverables.",
			"Do not create another replacement task for batch_range="+completedBatchRange+" just because earlier lanes for that same batch are still blocked or stale.",
			"If the named tasks above still share a prerequisite artifact like depends_on_path=..., do not reread that prerequisite just to verify batch_range="+completedBatchRange+". Treat the completed task plus the named task refs as sufficient evidence and move directly to the next unresolved batch or blocker.",
			"Do not call task.list with status=done or other broad project filters just to verify batch_range="+completedBatchRange+". The named task refs above already show which batches are complete, blocked, or still actionable.",
		)
	}
	return strings.Join(lines, " ")
}

func (w *Worker) FailStaleModelInvocations(ctx context.Context) (int64, error) {
	startedBefore := w.clock.Now().UTC().Add(-claimedAgentTurnHeartbeatGrace)
	if !w.startupAt.IsZero() {
		startedBefore = w.startupAt.Add(-claimedAgentTurnHeartbeatGrace)
	}
	rows, err := w.pool.Query(ctx, `
		SELECT mi.id,
		       mi.turn_id,
		       mi.session_id,
		       (
		         SELECT cs.scope_type
		         FROM chat_session cs
		         WHERE cs.id = mi.session_id
		       ) AS scope_type,
		       ct.trigger_message_id,
		       CASE
		         WHEN mi.session_id IS NULL OR ct.trigger_message_id IS NULL THEN 0
		         ELSE COALESCE((
		           SELECT MAX(retry_turn.retry_count)
		           FROM chat_turn retry_turn
		           WHERE retry_turn.session_id = mi.session_id
		             AND retry_turn.trigger_message_id = ct.trigger_message_id
		         ), 0) + 1
		       END AS next_retry_count
		FROM model_invocation mi
		LEFT JOIN chat_turn ct ON ct.id = mi.turn_id
		WHERE mi.status = 'in_flight'
		  AND (
		    (
		      mi.created_at < CASE
		        WHEN EXISTS (
		           SELECT 1
		           FROM chat_session cs_orphan
		           WHERE cs_orphan.id = mi.session_id
		             AND cs_orphan.mode = 'async'
		             AND cs_orphan.scope_type = 'project_task'
		         )
		        THEN $3::timestamptz
		        WHEN EXISTS (
		           SELECT 1
		           FROM chat_session cs_orphan
		           WHERE cs_orphan.id = mi.session_id
		             AND cs_orphan.mode = 'async'
		             AND cs_orphan.scope_type = 'project'
		             AND (
		                   POSITION('qwen' IN lower(COALESCE(mi.model_name, ''))) > 0
		                OR POSITION('mistral' IN lower(COALESCE(mi.model_name, ''))) > 0
		                OR POSITION('llama' IN lower(COALESCE(mi.model_name, ''))) > 0
		                OR POSITION('gemma' IN lower(COALESCE(mi.model_name, ''))) > 0
		                OR POSITION('deepseek' IN lower(COALESCE(mi.model_name, ''))) > 0
		             )
		         )
		        THEN $6::timestamptz
		        WHEN EXISTS (
		           SELECT 1
		           FROM chat_session cs_orphan
		           WHERE cs_orphan.id = mi.session_id
		             AND cs_orphan.mode = 'async'
		             AND cs_orphan.scope_type = 'project'
		         )
		        THEN $4::timestamptz
		        ELSE $1::timestamptz
		      END
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
		      EXISTS (
		        SELECT 1
		        FROM chat_turn ct
		        JOIN chat_session cs ON cs.id = ct.session_id
		        WHERE ct.id = mi.turn_id
		          AND ct.status = 'in_progress'
		          AND cs.status = 'active'
		          AND cs.current_turn_id = ct.id
		          AND (
		            (
		              cs.scope_type = 'project_task'
		              AND (
		                NOT EXISTS (
		                  SELECT 1
		                  FROM flow_node_execution e
		                  WHERE e.session_id = cs.id
		                    AND e.status = 'active'
		                )
		                OR mi.created_at < $3
		              )
		            )
		            OR (
		              cs.scope_type <> 'project_task'
		              AND (
		                (
		                  cs.mode = 'async'
		                  AND mi.created_at < CASE
		                        WHEN cs.scope_type = 'project'
		                         AND (
		                               POSITION('qwen' IN lower(COALESCE(mi.model_name, ''))) > 0
		                            OR POSITION('mistral' IN lower(COALESCE(mi.model_name, ''))) > 0
		                            OR POSITION('llama' IN lower(COALESCE(mi.model_name, ''))) > 0
		                            OR POSITION('gemma' IN lower(COALESCE(mi.model_name, ''))) > 0
		                            OR POSITION('deepseek' IN lower(COALESCE(mi.model_name, ''))) > 0
		                         )
		                        THEN $6::timestamptz
		                        ELSE $4::timestamptz
		                      END
		                )
		                OR (cs.mode <> 'async' AND mi.created_at < $2)
		              )
		            )
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'claimed'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		          )
		      )
		    )
		    OR (
		      EXISTS (
		        SELECT 1
		        FROM chat_turn ct
		        JOIN chat_session cs ON cs.id = ct.session_id
		        WHERE ct.id = mi.turn_id
		          AND ct.status = 'in_progress'
		          AND cs.status = 'active'
		          AND cs.mode = 'async'
		          AND cs.current_turn_id = ct.id
		          AND mi.created_at < $5
		      )
		    )
		    OR (
		      EXISTS (
		        SELECT 1
		        FROM chat_turn ct
		        JOIN chat_session cs ON cs.id = ct.session_id
		        WHERE ct.id = mi.turn_id
		          AND ct.status = 'in_progress'
		          AND cs.status = 'active'
		          AND cs.mode = 'async'
		          AND cs.scope_type = 'project'
		          AND cs.current_turn_id = ct.id
		          AND mi.created_at < $7
		          AND (
		                POSITION('qwen' IN lower(COALESCE(mi.model_name, ''))) > 0
		             OR POSITION('mistral' IN lower(COALESCE(mi.model_name, ''))) > 0
		             OR POSITION('llama' IN lower(COALESCE(mi.model_name, ''))) > 0
		             OR POSITION('gemma' IN lower(COALESCE(mi.model_name, ''))) > 0
		             OR POSITION('deepseek' IN lower(COALESCE(mi.model_name, ''))) > 0
		          )
		          AND (
		            SELECT COUNT(*)
		            FROM model_invocation newer
		            WHERE newer.status = 'completed'
		              AND newer.created_at > mi.created_at
		              AND newer.created_at > $7
		              AND newer.model_provider_id = mi.model_provider_id
		              AND COALESCE(newer.model_name, '') = COALESCE(mi.model_name, '')
		              AND COALESCE(newer.provider_connection_id, '00000000-0000-0000-0000-000000000000'::uuid) =
		                  COALESCE(mi.provider_connection_id, '00000000-0000-0000-0000-000000000000'::uuid)
		          ) >= 2
		      )
		    )
		  )
	`, w.clock.Now().UTC().Add(-30*time.Minute), w.clock.Now().UTC().Add(-15*time.Second), w.clock.Now().UTC().Add(-staleContinuationThresholdForScope("project_task")), w.clock.Now().UTC().Add(-staleContinuationThreshold), startedBefore, w.clock.Now().UTC().Add(-slowProjectAsyncModelThreshold), w.clock.Now().UTC().Add(-overtakenLocalProjectThreshold))
	if err != nil {
		return 0, fmt.Errorf("list stale model invocations: %w", err)
	}
	defer rows.Close()

	type staleInvocationCandidate struct {
		invocationID uuid.UUID
		turnID       *uuid.UUID
		sessionID    *uuid.UUID
		scopeType    *string
		messageID    *uuid.UUID
		retryCount   int
	}
	var candidates []staleInvocationCandidate
	for rows.Next() {
		var item staleInvocationCandidate
		if err := rows.Scan(&item.invocationID, &item.turnID, &item.sessionID, &item.scopeType, &item.messageID, &item.retryCount); err != nil {
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
				UPDATE run r
				SET status = 'failed',
				    failure_class = 'permanent',
				    failure_reason = $3,
				    completed_at = COALESCE(completed_at, now()),
				    updated_at = now()
				WHERE session_id = $1
				  AND status = 'in_progress'
				  AND (turn_id = $2 OR turn_id IS NULL)
				  AND NOT EXISTS (
				    SELECT 1
				    FROM model_invocation mi_live
				    WHERE mi_live.run_id = r.id
				      AND mi_live.status = 'in_flight'
				      AND mi_live.turn_id IS DISTINCT FROM $2
				  )
			`, *item.sessionID, *item.turnID, failureReason); err != nil {
				return repaired, fmt.Errorf("fail stale model invocation runs for session %s turn %s: %w", *item.sessionID, *item.turnID, err)
			}
		}
		if item.sessionID != nil && *item.sessionID != uuid.Nil {
			if _, err := w.pool.Exec(ctx, `
				UPDATE chat_session
				SET current_turn_id = NULL
				WHERE id = $1
				  AND current_turn_id = $2
				  AND EXISTS (
				    SELECT 1
				    FROM chat_turn ct
				    WHERE ct.id = $2
				      AND ct.status NOT IN ('pending', 'in_progress')
				  )
			`, *item.sessionID, *item.turnID); err != nil {
				return repaired, fmt.Errorf("clear current turn for stale model invocation session %s: %w", *item.sessionID, err)
			}
			if item.messageID != nil && *item.messageID != uuid.Nil {
				scopeType := ""
				if item.scopeType != nil {
					scopeType = *item.scopeType
				}
				shouldRequeue, err := w.shouldRequeueRecoveredProjectTrigger(ctx, *item.sessionID, scopeType, *item.messageID)
				if err != nil {
					return repaired, fmt.Errorf("check recovered stale model invocation retry eligibility for session %s: %w", *item.sessionID, err)
				}
				if !shouldRequeue {
					continue
				}
				hasQueued, err := w.sessionHasQueuedOrClaimedAgentTurn(ctx, *item.sessionID)
				if err != nil {
					return repaired, fmt.Errorf("check queued recovered stale model invocation retry for session %s: %w", *item.sessionID, err)
				}
				if !hasQueued {
					if _, err := w.enqueueAgentTurnDispatch(ctx, nil, agentTurnKeyPayload{
						SessionID:  *item.sessionID,
						MessageID:  *item.messageID,
						RetryCount: item.retryCount,
					}, nil); err != nil {
						return repaired, fmt.Errorf("enqueue recovered stale model invocation retry for session %s: %w", *item.sessionID, err)
					}
				}
			}
		}
	}
	return repaired, nil
}

func (w *Worker) RecoverStaleInProgressContinuationTurns(ctx context.Context) (int64, error) {
	rows, err := w.pool.Query(ctx, `
		SELECT cs.id,
		       COALESCE(live_turn.id, ct.id) AS turn_id,
		       cs.scope_type,
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
		LEFT JOIN LATERAL (
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
		    OR p.id IS NOT NULL
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
		scopeType  string
		messageID  uuid.UUID
		retryCount int
	}
	candidates := make([]staleContinuationCandidate, 0)
	for rows.Next() {
		var item staleContinuationCandidate
		if err := rows.Scan(&item.sessionID, &item.turnID, &item.scopeType, &item.messageID, &item.retryCount); err != nil {
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
		if strings.EqualFold(strings.TrimSpace(item.scopeType), "project") && item.messageID == uuid.Nil {
			synthMessageID, suppressed, err := w.ensureProjectContinuationMessageDecision(ctx, item.sessionID)
			if err != nil {
				return repaired, fmt.Errorf("ensure project continuation retry message for session %s: %w", item.sessionID, err)
			}
			if suppressed {
				repaired++
				continue
			}
			if synthMessageID != uuid.Nil {
				item.messageID = synthMessageID
				item.retryCount = 0
			}
		}
		shouldRequeue, err := w.shouldRequeueRecoveredProjectTrigger(ctx, item.sessionID, item.scopeType, item.messageID)
		if err != nil {
			return repaired, fmt.Errorf("check recovered continuation retry eligibility for session %s: %w", item.sessionID, err)
		}
		if !shouldRequeue {
			if w.logger != nil {
				w.logger.Info("job queue: suppressed stale project continuation requeue",
					"session_id", item.sessionID,
					"turn_id", item.turnID,
					"scope_type", strings.TrimSpace(item.scopeType),
				)
			}
			repaired++
			continue
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
		        (
		          cs.scope_type = 'project_task'
		          AND EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'completed'
		              AND COALESCE(mi.completed_at, mi.created_at) < $2
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'claimed'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		          )
		        )
		     OR (
		          EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'claimed'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		              AND (jq.payload->>'message_id')::uuid = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		              AND COALESCE((jq.payload->>'retry_count')::int, 0) = COALESCE(live_turn.retry_count, ct.retry_count, 0)
		              AND jq.claimed_at < $3
		              AND jq.updated_at <= jq.claimed_at
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'in_flight'
		          )
		          AND (
		            cs.scope_type <> 'project_task'
		            OR NOT EXISTS (
		              SELECT 1
		              FROM run r
		              WHERE r.turn_id = COALESCE(live_turn.id, ct.id)
		                AND r.status IN ('created', 'in_progress')
		            )
		          )
		        )
		     OR (
		          EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status IN ('pending', 'claimed')
		              AND (jq.payload->>'session_id')::uuid = cs.id
		              AND (jq.payload->>'message_id')::uuid = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		              AND (
		                    COALESCE((jq.payload->>'retry_count')::int, 0) > COALESCE(live_turn.retry_count, ct.retry_count, 0)
		                 OR (
		                      jq.updated_at > COALESCE(live_turn.started_at, ct.started_at)
		                  AND COALESCE((jq.payload->>'retry_count')::int, 0) = 0
		                    )
		                  )
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'in_flight'
		          )
		          AND (
		            cs.scope_type <> 'project_task'
		            OR NOT EXISTS (
		              SELECT 1
		              FROM run r
		              WHERE r.turn_id = COALESCE(live_turn.id, ct.id)
		                AND r.status IN ('created', 'in_progress')
		            )
		          )
		        )
		     OR (
		          EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'pending'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		              AND (jq.payload->>'message_id')::uuid = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		              AND COALESCE((jq.payload->>'retry_count')::int, 0) = COALESCE(live_turn.retry_count, ct.retry_count, 0)
		              AND jq.updated_at > COALESCE(live_turn.started_at, ct.started_at)
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'in_flight'
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'completed'
		              AND COALESCE(mi.completed_at, mi.created_at) >= $2
		          )
		          AND (
		            cs.scope_type <> 'project_task'
		            OR NOT EXISTS (
		              SELECT 1
		              FROM run r
		              WHERE r.turn_id = COALESCE(live_turn.id, ct.id)
		                AND r.status IN ('created', 'in_progress')
		            )
		          )
		        )
		     OR (
		          cs.scope_type = 'project_task'
		          AND EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'completed'
		              AND COALESCE(mi.completed_at, mi.created_at) < $2
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status = 'claimed'
		              AND (jq.payload->>'session_id')::uuid = cs.id
		          )
		        )
		     OR (
		          cs.scope_type = 'project_task'
		          AND COALESCE(live_turn.started_at, ct.started_at) < $3
		          AND NOT EXISTS (
		            SELECT 1
		            FROM run r
		            WHERE r.turn_id = COALESCE(live_turn.id, ct.id)
		              AND r.status IN ('created', 'in_progress')
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status IN ('in_flight', 'completed')
		          )
		          AND NOT EXISTS (
		            SELECT 1
		            FROM job_queue jq
		            WHERE jq.job_type = 'agent_turn'
		              AND jq.status IN ('pending', 'claimed')
		              AND (jq.payload->>'session_id')::uuid = cs.id
		              AND (jq.payload->>'message_id')::uuid = COALESCE(live_turn.trigger_message_id, ct.trigger_message_id)
		              AND COALESCE((jq.payload->>'retry_count')::int, 0) = COALESCE(live_turn.retry_count, ct.retry_count, 0)
		          )
		        )
		     OR (
		          cs.scope_type <> 'project_task'
		          AND COALESCE(live_turn.started_at, ct.started_at) < $1
		          AND NOT EXISTS (
		            SELECT 1
		            FROM model_invocation mi
		            WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		              AND mi.status = 'in_flight'
		          )
		        )
		      )
		  AND (
		    cs.scope_type NOT IN ('project', 'project_task')
		    OR p.id IS NOT NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM run r
		    WHERE r.session_id = cs.id
		      AND r.turn_id = COALESCE(live_turn.id, ct.id)
		      AND r.status IN ('created', 'in_progress')
		      AND (
		            r.status = 'created'
		         OR EXISTS (
		              SELECT 1
		              FROM model_invocation mi
		              WHERE mi.turn_id = COALESCE(live_turn.id, ct.id)
		                AND mi.status = 'in_flight'
		            )
		          )
		  )
	`, w.clock.Now().UTC().Add(-staleContinuationThreshold), w.clock.Now().UTC().Add(-postModelOrphanTurnThreshold), w.clock.Now().UTC().Add(-claimedAgentTurnHeartbeatGrace))
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
		if strings.EqualFold(strings.TrimSpace(item.scopeType), "project_task") {
			if _, err := w.pool.Exec(ctx, `
				UPDATE run
				SET status = 'failed',
				    failure_class = 'transient',
				    failure_reason = $2,
				    completed_at = COALESCE(completed_at, now())
				WHERE turn_id = $1
				  AND status IN ('created', 'in_progress', 'cancelling')
			`, item.turnID, failureReason); err != nil {
				return repaired, fmt.Errorf("fail stale triggered-turn runs for turn %s: %w", item.turnID, err)
			}
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
		retryMessageID, err := w.resolveStaleTriggeredRetryMessageID(ctx, item.sessionID, item.scopeType, item.messageID)
		if err != nil {
			return repaired, fmt.Errorf("resolve stale triggered retry message for session %s: %w", item.sessionID, err)
		}
		var (
			retryRunAfter *time.Time
			retryPayload  = agentTurnKeyPayload{
				SessionID:  item.sessionID,
				MessageID:  retryMessageID,
				RetryCount: item.retryCount,
			}
		)
		if retryAfterHint, ok, err := w.loadLatestTurnRateLimitRetryAfter(ctx, item.turnID); err != nil {
			return repaired, fmt.Errorf("load stale triggered retry backoff for turn %s: %w", item.turnID, err)
		} else if ok {
			retryDelay := agentTurnRateLimitDelay(max(1, item.retryCount), retryAfterHint)
			runAfter := w.clock.Now().UTC().Add(retryDelay)
			retryRunAfter = &runAfter
			retryPayload.RateLimitJitterApplied = true
		}
		shouldRequeue, err := w.shouldRequeueRecoveredProjectTrigger(ctx, item.sessionID, item.scopeType, retryMessageID)
		if err != nil {
			return repaired, fmt.Errorf("check recovered trigger requeue eligibility for session %s: %w", item.sessionID, err)
		}
		if !shouldRequeue {
			if w.logger != nil {
				w.logger.Info("job queue: suppressed stale project trigger requeue",
					"session_id", item.sessionID,
					"turn_id", item.turnID,
					"scope_type", strings.TrimSpace(item.scopeType),
				)
			}
			repaired++
			continue
		}
		hasQueued, err := w.sessionHasQueuedOrClaimedAgentTurn(ctx, item.sessionID)
		if err != nil {
			return repaired, fmt.Errorf("check queued recovered triggered retry for session %s: %w", item.sessionID, err)
		}
		if !hasQueued {
			if _, err := w.enqueueAgentTurnDispatch(ctx, nil, retryPayload, retryRunAfter); err != nil {
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

func (w *Worker) loadLatestTurnRateLimitRetryAfter(ctx context.Context, turnID uuid.UUID) (time.Duration, bool, error) {
	if turnID == uuid.Nil {
		return 0, false, nil
	}
	var errorText string
	err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(error_message, '')
		FROM model_invocation
		WHERE turn_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, turnID).Scan(&errorText)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	retryAfter, ok := parseRateLimitRetryAfterFromText(errorText)
	return retryAfter, ok, nil
}

func (w *Worker) shouldRequeueRecoveredProjectTrigger(ctx context.Context, sessionID uuid.UUID, scopeType string, messageID uuid.UUID) (bool, error) {
	if !strings.EqualFold(strings.TrimSpace(scopeType), "project") {
		return true, nil
	}

	var (
		projectStatus          string
		projectPaused          bool
		source                 string
		projectBootstrapStatus string
		openTaskCount          int
	)
	if err := w.pool.QueryRow(ctx, `
		SELECT COALESCE(p.status, ''),
		       COALESCE(p.settings->'pause'->>'is_paused', 'false') = 'true',
		       COALESCE(cm.metadata->>'source', ''),
		       COALESCE(cs.metadata->'project_bootstrap'->>'status', ''),
		       COUNT(*) FILTER (WHERE pt.work_status NOT IN ('done', 'cancelled'))
		FROM chat_session cs
		JOIN project p
		  ON p.id = cs.scope_id
		LEFT JOIN chat_message cm
		  ON cm.id = $2
		LEFT JOIN project_task pt
		  ON pt.project_id = cs.scope_id
		WHERE cs.id = $1
		GROUP BY p.status, p.settings, cm.metadata, cs.metadata
	`, sessionID, messageID).Scan(&projectStatus, &projectPaused, &source, &projectBootstrapStatus, &openTaskCount); err != nil {
		return false, fmt.Errorf("load recovered project trigger state: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(projectStatus), "active") || projectPaused {
		return false, nil
	}

	switch strings.ToLower(strings.TrimSpace(source)) {
	case "project_bootstrap":
		return strings.EqualFold(strings.TrimSpace(projectBootstrapStatus), "active"), nil
	case "project_execution_continuation", "project_continuation_resume":
		return openTaskCount > 0, nil
	default:
		return true, nil
	}
}

func (w *Worker) resolveStaleTriggeredRetryMessageID(ctx context.Context, sessionID uuid.UUID, scopeType string, messageID uuid.UUID) (uuid.UUID, error) {
	if sessionID == uuid.Nil || messageID == uuid.Nil {
		return messageID, nil
	}
	scopeType = strings.TrimSpace(scopeType)
	if strings.EqualFold(scopeType, "project") {
		var (
			source          string
			bootstrapStatus string
		)
		if err := w.pool.QueryRow(ctx, `
			SELECT COALESCE(cm.metadata->>'source', ''),
			       COALESCE(cs.metadata->'project_bootstrap'->>'status', '')
			FROM chat_session cs
			LEFT JOIN chat_message cm ON cm.id = $2
			WHERE cs.id = $1
		`, sessionID, messageID).Scan(&source, &bootstrapStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return messageID, nil
			}
			return uuid.Nil, err
		}
		if strings.EqualFold(strings.TrimSpace(bootstrapStatus), "active") &&
			!strings.EqualFold(strings.TrimSpace(source), "project_bootstrap") {
			retryMessageID, err := w.ensureProjectContinuationMessage(ctx, sessionID)
			if err != nil {
				return uuid.Nil, err
			}
			if retryMessageID != uuid.Nil {
				return retryMessageID, nil
			}
		}
		return messageID, nil
	}
	if !strings.EqualFold(scopeType, "organization") {
		return messageID, nil
	}

	var role string
	if err := w.pool.QueryRow(ctx, `
		SELECT role
		FROM chat_message
		WHERE id = $1
	`, messageID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return messageID, nil
		}
		return uuid.Nil, err
	}
	if strings.EqualFold(strings.TrimSpace(role), "user") {
		return messageID, nil
	}

	var retryMessageID uuid.UUID
	if err := w.pool.QueryRow(ctx, `
		SELECT cm.id
		FROM chat_message cm
		WHERE cm.session_id = $1
		  AND cm.role = 'user'
		  AND cm.status = 'pending'
		  AND COALESCE(cm.metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		  AND COALESCE(cm.metadata->>'synthetic_user_message', 'false') = 'true'
		  AND COALESCE(cm.metadata->>'source', '') = 'organization_continuation_resume'
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT 1
	`, sessionID).Scan(&retryMessageID); err == nil {
		return retryMessageID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	if err := w.pool.QueryRow(ctx, `
		SELECT cm.id
		FROM chat_message cm
		WHERE cm.session_id = $1
		  AND cm.role = 'user'
		  AND cm.status = 'pending'
		  AND COALESCE(cm.metadata->'agent_turn_dispatch'->>'cancelled_at', '') = ''
		ORDER BY cm.created_at DESC, cm.id DESC
		LIMIT 1
	`, sessionID).Scan(&retryMessageID); err == nil {
		return retryMessageID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	return messageID, nil
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
		       jq.run_after,
		       COALESCE((jq.payload->>'rate_limit_jitter_applied')::boolean, false)
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
		  AND jq.run_after >= now() + interval '5 minutes'
		  AND (
		        (
		          COALESCE(jq.payload->>'rate_limit_jitter_applied', 'false') <> 'true'
		          AND latest_msg.content LIKE '[Rate limited, retrying in %'
		        )
		     OR jq.run_after > now() + $1::interval
		      )
	`, agentTurnRateLimitBackoffCap.String())
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
			jittered   bool
		)
		if err := rows.Scan(&jobID, &sessionID, &messageID, &retryCount, &runAfter, &jittered); err != nil {
			return repaired, fmt.Errorf("scan pending rate-limited agent turn: %w", err)
		}
		rejitteredRunAfter := rejitteredRateLimitedRunAfter(w.clock.Now().UTC(), runAfter.UTC(), sessionID, messageID, retryCount, jittered)
		tag, err := w.pool.Exec(ctx, `
			UPDATE job_queue
			SET run_after = $2,
			    payload = jsonb_set(payload, '{rate_limit_jitter_applied}', 'true'::jsonb, true),
			    updated_at = now()
			WHERE id = $1
			  AND status = 'pending'
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

func rejitteredRateLimitedRunAfter(now, runAfter time.Time, sessionID, messageID uuid.UUID, retryCount int, jittered bool) time.Time {
	if jittered {
		return clampRateLimitedRunAfter(now, runAfter)
	}
	if runAfter.Sub(now) < legacyRateLimitJitterFloor {
		return clampRateLimitedRunAfter(now, runAfter)
	}
	return clampRateLimitedRunAfter(now, runAfter.Add(rateLimitRetryJitterOffset(sessionID, messageID, retryCount)))
}

func clampRateLimitedRunAfter(now, runAfter time.Time) time.Time {
	maxRunAfter := now.Add(agentTurnRateLimitBackoffCap)
	if runAfter.After(maxRunAfter) {
		return maxRunAfter
	}
	return runAfter
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
		  AND NOT (
			job_type = $2
			AND EXISTS (
				SELECT 1
				FROM chat_session cs
				JOIN chat_turn ct
				  ON ct.id = cs.current_turn_id
				 AND ct.status = 'in_progress'
				WHERE cs.id = (job_queue.payload->>'session_id')::uuid
				  AND cs.status = 'active'
				  AND ct.trigger_message_id = (job_queue.payload->>'message_id')::uuid
				  AND ct.retry_count = COALESCE((job_queue.payload->>'retry_count')::int, 0)
				  AND (
					NOT EXISTS (
						SELECT 1
						FROM model_invocation mi_any
						WHERE mi_any.turn_id = ct.id
					)
					OR EXISTS (
						SELECT 1
						FROM model_invocation mi
						WHERE mi.turn_id = ct.id
						  AND mi.status = 'in_flight'
						  AND mi.created_at >= CASE
							WHEN cs.scope_type = 'project_task' THEN $6::timestamptz
							WHEN cs.scope_type = 'project'
							 AND (
							       POSITION('qwen' IN lower(COALESCE(mi.model_name, ''))) > 0
							    OR POSITION('mistral' IN lower(COALESCE(mi.model_name, ''))) > 0
							    OR POSITION('llama' IN lower(COALESCE(mi.model_name, ''))) > 0
							    OR POSITION('gemma' IN lower(COALESCE(mi.model_name, ''))) > 0
							    OR POSITION('deepseek' IN lower(COALESCE(mi.model_name, ''))) > 0
							 )
							THEN $8::timestamptz
							ELSE $7::timestamptz
						  END
					)
				  )
			)
		  )
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
	`, staleBefore, agentTurnJobType, bootstrapStaleBefore, w.workerID, foreignAgentTurnStaleBefore, w.clock.Now().UTC().Add(-staleContinuationThresholdForScope("project_task")), w.clock.Now().UTC().Add(-staleContinuationThreshold), w.clock.Now().UTC().Add(-slowProjectAsyncModelThreshold))
	if err != nil {
		return 0, fmt.Errorf("recover stale claims: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx context.Context) (int64, error) {
	recoverBefore := w.clock.Now().UTC().Add(-claimedAgentTurnHeartbeatGrace)
	postModelGraceBefore := w.clock.Now().UTC().Add(-postModelOrphanTurnThreshold)
	tag, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = CASE WHEN attempts < max_attempts THEN 'pending' ELSE 'dead_letter' END,
		    claimed_by = NULL,
		    claimed_at = NULL,
		    run_after = CASE WHEN attempts < max_attempts THEN now() ELSE run_after END,
		    last_error = CASE
		        WHEN attempts < max_attempts THEN COALESCE(NULLIF(last_error, ''), 'recovered non-heartbeating claimed agent_turn without live ownership')
		        ELSE COALESCE(NULLIF(last_error, ''), 'non-heartbeating claimed agent_turn exceeded max attempts')
		    END,
		    updated_at = now()
		WHERE jq.status = 'claimed'
		  AND jq.job_type = $1
		  AND jq.claimed_at < $2
		  AND jq.updated_at <= jq.claimed_at
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.status = 'active'
		      AND cs.mode = 'async'
		      AND (
		        cs.current_turn_id IS NULL
		        OR NOT EXISTS (
		          SELECT 1
		          FROM chat_turn current_turn
		          WHERE current_turn.id = cs.current_turn_id
		            AND current_turn.status IN ('pending', 'in_progress')
		            AND current_turn.trigger_message_id = (jq.payload->>'message_id')::uuid
		            AND current_turn.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		        )
		        OR EXISTS (
		          SELECT 1
		          FROM chat_turn current_turn
		          WHERE current_turn.id = cs.current_turn_id
		            AND current_turn.status = 'in_progress'
		            AND current_turn.trigger_message_id = (jq.payload->>'message_id')::uuid
		            AND current_turn.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		            AND NOT EXISTS (
		              SELECT 1
		              FROM model_invocation mi
		              WHERE mi.turn_id = current_turn.id
		                AND mi.status = 'in_flight'
		            )
		            AND NOT EXISTS (
		              SELECT 1
		              FROM model_invocation mi
		              WHERE mi.turn_id = current_turn.id
		                AND mi.status = 'completed'
		                AND COALESCE(mi.completed_at, mi.created_at) >= $3
		            )
		            AND (
		              cs.scope_type <> 'project_task'
		              OR NOT EXISTS (
		                SELECT 1
		                FROM run r
		                WHERE r.turn_id = current_turn.id
		                  AND r.status IN ('created', 'in_progress')
		              )
		            )
		        )
		      )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    JOIN model_invocation mi ON mi.turn_id = ct.id
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		      AND mi.status = 'in_flight'
		  )
	`, agentTurnJobType, recoverBefore, postModelGraceBefore)
	if err != nil {
		return 0, fmt.Errorf("recover claimed agent_turn jobs without live ownership: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (w *Worker) RecoverStaleInProgressProjectTurnsWithoutOwnership(ctx context.Context) (int64, error) {
	postModelGraceBefore := w.clock.Now().UTC().Add(-postModelOrphanTurnThreshold)
	rows, err := w.pool.Query(ctx, `
		SELECT cs.id,
		       ct.id,
		       ct.trigger_message_id,
		       ct.retry_count
		FROM chat_session cs
		JOIN chat_turn ct
		  ON ct.id = cs.current_turn_id
		WHERE cs.status = 'active'
		  AND cs.mode = 'async'
		  AND cs.scope_type = 'project'
		  AND ct.status = 'in_progress'
		  AND ct.trigger_message_id IS NOT NULL
		  AND NOT EXISTS (
		    SELECT 1
		    FROM model_invocation mi
		    WHERE mi.turn_id = ct.id
		      AND (
		            mi.status = 'in_flight'
		         OR (
		              mi.status = 'completed'
		          AND COALESCE(mi.completed_at, mi.created_at) >= $1
		            )
		          )
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM run r
		    WHERE r.session_id = cs.id
		      AND r.turn_id = ct.id
		      AND r.status IN ('created', 'queued', 'in_progress')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM job_queue jq
		    WHERE jq.job_type = $2
		      AND jq.status = 'claimed'
		      AND (jq.payload->>'session_id')::uuid = cs.id
		  )
	`, postModelGraceBefore, agentTurnJobType)
	if err != nil {
		return 0, fmt.Errorf("list stale in-progress project turns without ownership: %w", err)
	}
	defer rows.Close()

	type candidate struct {
		sessionID  uuid.UUID
		turnID     uuid.UUID
		messageID  uuid.UUID
		retryCount int
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.sessionID, &item.turnID, &item.messageID, &item.retryCount); err != nil {
			return 0, fmt.Errorf("scan stale in-progress project turn candidate: %w", err)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale in-progress project turn candidates: %w", err)
	}

	const failureReason = "recovered stale in-progress project turn without active invocation ownership"
	var recovered int64
	for _, item := range candidates {
		tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return recovered, fmt.Errorf("begin stale in-progress project turn recovery tx: %w", err)
		}
		committed := false
		func() {
			defer func() {
				if !committed {
					_ = tx.Rollback(ctx)
				}
			}()

			tag, execErr := tx.Exec(ctx, `
				UPDATE chat_turn
				SET status = 'failed',
				    error_message = $3,
				    completed_at = COALESCE(completed_at, now())
				WHERE id = $1
				  AND session_id = $2
				  AND status = 'in_progress'
			`, item.turnID, item.sessionID, failureReason)
			if execErr != nil {
				err = fmt.Errorf("fail stale in-progress project turn %s: %w", item.turnID, execErr)
				return
			}
			if tag.RowsAffected() == 0 {
				if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
					err = fmt.Errorf("rollback skipped stale in-progress project turn recovery tx: %w", rollbackErr)
				}
				committed = true
				return
			}

			if _, execErr := tx.Exec(ctx, `
				UPDATE chat_message
				SET status = 'failed',
				    error_message = $2
				WHERE turn_id = $1
				  AND role = 'assistant'
				  AND status IN ('pending', 'streaming')
			`, item.turnID, failureReason); execErr != nil {
				err = fmt.Errorf("fail assistant messages for stale in-progress project turn %s: %w", item.turnID, execErr)
				return
			}

			if _, execErr := tx.Exec(ctx, `
				UPDATE chat_session
				SET current_turn_id = NULL
				WHERE id = $1
				  AND current_turn_id = $2
			`, item.sessionID, item.turnID); execErr != nil {
				err = fmt.Errorf("clear current_turn_id for stale in-progress project turn %s: %w", item.turnID, execErr)
				return
			}

			if _, execErr := tx.Exec(ctx, `
				UPDATE job_queue jq
				SET status = 'dead_letter',
				    claimed_by = NULL,
				    claimed_at = NULL,
				    last_error = 'superseded stale in-progress project turn before retry',
				    updated_at = now()
				WHERE jq.job_type = $1
				  AND jq.status IN ('pending', 'claimed')
				  AND (jq.payload->>'session_id')::uuid = $2
				  AND (jq.payload->>'message_id')::uuid = $3
				  AND COALESCE((jq.payload->>'retry_count')::int, 0) = $4
			`, agentTurnJobType, item.sessionID, item.messageID, item.retryCount); execErr != nil {
				err = fmt.Errorf("retire stale agent_turn dispatches for project turn %s: %w", item.turnID, execErr)
				return
			}

			if _, execErr := w.enqueueAgentTurnDispatch(ctx, tx, agentTurnKeyPayload{
				SessionID:  item.sessionID,
				MessageID:  item.messageID,
				RetryCount: item.retryCount + 1,
			}, nil); execErr != nil {
				err = fmt.Errorf("enqueue fresh retry for stale in-progress project turn %s: %w", item.turnID, execErr)
				return
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				err = fmt.Errorf("commit stale in-progress project turn recovery tx: %w", commitErr)
				return
			}
			committed = true
			recovered++
		}()
		if err != nil {
			return recovered, err
		}
	}
	return recovered, nil
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

func (w *Worker) CloseBlockedProjectTaskAsyncSessionsWithoutLiveExecution(ctx context.Context) (int64, error) {
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
			  AND pt.work_status = 'blocked'
			  AND EXISTS (
			    SELECT 1
			    FROM flow_node_execution fne_terminal
			    WHERE fne_terminal.session_id = cs.id
			      AND fne_terminal.status IN ('abandoned', 'rejected')
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM flow_node_execution fne_active
			    WHERE fne_active.session_id = cs.id
			      AND fne_active.status = 'active'
			  )
			  AND NOT EXISTS (
			    SELECT 1
			    FROM job_queue jq
			    WHERE jq.job_type = $1
			      AND jq.status IN ('pending', 'claimed')
			      AND (jq.payload->>'session_id')::uuid = cs.id
			  )
		)
		  AND status IN ('pending', 'in_progress')
	`, agentTurnJobType); err != nil {
		return 0, fmt.Errorf("cancel blocked project_task session turns without live execution: %w", err)
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
		  AND pt.work_status = 'blocked'
		  AND EXISTS (
		    SELECT 1
		    FROM flow_node_execution fne_terminal
		    WHERE fne_terminal.session_id = cs.id
		      AND fne_terminal.status IN ('abandoned', 'rejected')
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM flow_node_execution fne_active
		    WHERE fne_active.session_id = cs.id
		      AND fne_active.status = 'active'
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = cs.id
		      AND ct.status IN ('pending', 'in_progress')
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
		return 0, fmt.Errorf("close blocked project_task async sessions without live execution: %w", err)
	}
	return ct.RowsAffected(), nil
}

func (w *Worker) RetireClosedAsyncSessionRuns(ctx context.Context) (int64, error) {
	ct, err := w.pool.Exec(ctx, `
		UPDATE run r
		SET status = CASE
		        WHEN cs.scope_type = 'project_task' AND pt.work_status = 'done' THEN 'completed'
		        WHEN cs.scope_type = 'project_task' AND pt.work_status = 'cancelled' THEN 'cancelled'
		        ELSE 'failed'
		    END,
		    failure_class = CASE
		        WHEN cs.scope_type = 'project_task' AND pt.work_status IN ('done', 'cancelled') THEN NULL
		        ELSE 'transient'
		    END,
		    failure_reason = CASE
		        WHEN cs.scope_type = 'project_task' AND pt.work_status IN ('done', 'cancelled') THEN NULL
		        ELSE 'async session closed without a live task turn'
		    END,
		    completed_at = COALESCE(r.completed_at, now()),
		    updated_at = now()
		FROM chat_session cs
		LEFT JOIN project_task pt
		  ON cs.scope_type = 'project_task'
		 AND pt.id = cs.scope_id
		WHERE r.session_id = cs.id
		  AND cs.mode = 'async'
		  AND cs.status = 'closed'
		  AND r.status IN ('created', 'in_progress', 'paused', 'cancelling')
		  AND (
		    r.turn_id IS NULL
		    OR NOT EXISTS (
		      SELECT 1
		      FROM chat_turn ct_live
		      WHERE ct_live.id = r.turn_id
		        AND ct_live.status IN ('pending', 'in_progress')
		    )
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("retire closed async session runs: %w", err)
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
	if recovered, err := w.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx); err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("job queue: inline non-heartbeating claimed agent_turn recovery failed", "error", err)
		}
		return err
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered non-heartbeating claimed agent_turn jobs before claim", "count", recovered)
	}
	if recovered, err := w.RecoverStaleInProgressProjectTurnsWithoutOwnership(ctx); err != nil {
		if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("job queue: inline stale in-progress project turn recovery failed", "error", err)
		}
		return err
	} else if recovered > 0 {
		w.logger.Info("job queue: recovered stale in-progress project turns before claim", "count", recovered)
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
	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged closed-session agent_turn dispatch during claim',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = $1
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.status IN ('closed', 'archived')
		  )
		`, agentTurnJobType); err != nil {
		return nil, fmt.Errorf("dead-letter closed-session agent_turn jobs before claim: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged paused project dispatch during claim',
		    updated_at = now()
		WHERE jq.status = 'pending'
		  AND jq.job_type = $1
		  AND jq.run_after <= now()
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    LEFT JOIN project_task pt
		      ON cs.scope_type = 'project_task'
		     AND pt.id = cs.scope_id
		    LEFT JOIN project p_direct
		      ON cs.scope_type = 'project'
		     AND p_direct.id = cs.scope_id
		    LEFT JOIN project p_task
		      ON cs.scope_type = 'project_task'
		     AND p_task.id = pt.project_id
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.mode = 'async'
		      AND cs.status = 'active'
		      AND COALESCE(
		            p_direct.settings->'pause'->>'is_paused',
		            p_task.settings->'pause'->>'is_paused',
		            'false'
		          ) = 'true'
		  )
	`, agentTurnJobType); err != nil {
		return nil, fmt.Errorf("dead-letter paused project agent_turn jobs before claim: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged stale inactive project bootstrap dispatch during claim',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = $1
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN chat_message cm
		      ON cm.id = (jq.payload->>'message_id')::uuid
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project'
		      AND COALESCE(cm.metadata->>'source', '') = 'project_bootstrap'
		      AND COALESCE(cs.metadata->'project_bootstrap'->>'status', '') <> 'active'
		  )
	`, agentTurnJobType); err != nil {
		return nil, fmt.Errorf("dead-letter stale inactive project bootstrap dispatches before claim: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged stale settled project continuation dispatch during claim',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = $1
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN chat_message cm
		      ON cm.id = (jq.payload->>'message_id')::uuid
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project'
		      AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume')
		      AND NOT EXISTS (
		        SELECT 1
		        FROM project_task pt
		        WHERE pt.project_id = cs.scope_id
		          AND pt.work_status NOT IN ('done', 'cancelled')
		      )
		  )
	`, agentTurnJobType); err != nil {
		return nil, fmt.Errorf("dead-letter stale settled project continuation dispatches before claim: %w", err)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged stale terminal message-attempt dispatch during claim',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = $1
		  AND EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		      AND ct.status IN ('completed', 'cancelled', 'failed')
		  )
	`, agentTurnJobType); err != nil {
		return nil, fmt.Errorf("dead-letter stale terminal agent_turn jobs before claim: %w", err)
	}
	stalePendingOrphanBefore := w.clock.Now().UTC().Add(-w.staleClaimThreshold)
	purgedOrphanDispatches, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'purged stale orphan task execution dispatch during claim',
		    updated_at = now()
		WHERE jq.status IN ('pending', 'claimed')
		  AND jq.job_type = $1
		  AND jq.run_after <= $2
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    JOIN flow_node_execution e
		      ON e.session_id = cs.id
		     AND e.status = 'active'
		    WHERE cs.id = (jq.payload->>'session_id')::uuid
		      AND cs.scope_type = 'project_task'
		      AND cs.mode = 'async'
		      AND cs.status = 'active'
		      AND cs.current_turn_id IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1
		    FROM chat_turn ct
		    WHERE ct.session_id = (jq.payload->>'session_id')::uuid
		      AND ct.trigger_message_id = (jq.payload->>'message_id')::uuid
		      AND ct.retry_count = COALESCE((jq.payload->>'retry_count')::int, 0)
		  )
	`, agentTurnJobType, stalePendingOrphanBefore)
	if err != nil {
		return nil, fmt.Errorf("dead-letter stale orphan task execution dispatches before claim: %w", err)
	}
	if purgedOrphanDispatches.RowsAffected() > 0 {
		if w.logger != nil {
			w.logger.Info("job queue: dead-lettered stale orphan task execution dispatches before claim", "count", purgedOrphanDispatches.RowsAffected())
		}
		if _, err := w.RequeueActiveExecutionSessionsWithoutTurns(ctx); err != nil {
			return nil, fmt.Errorf("requeue active execution sessions after orphan dispatch cleanup: %w", err)
		}
	}
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
		"job_type <> $3 AND job_type IN ('%s', '%s', '%s', '%s', '%s')",
		memoryExtractTurnJobType,
		rollupUpdateJobType,
		modelUsageRollupDailyJobType,
		retentionEnforceJobType,
		traceSpanPartitionCreateJobType,
	)
}

func isMaintenanceJobType(jobType string) bool {
	switch strings.TrimSpace(jobType) {
	case memoryExtractTurnJobType, rollupUpdateJobType, modelUsageRollupDailyJobType, retentionEnforceJobType, traceSpanPartitionCreateJobType:
		return true
	default:
		return false
	}
}

func nonMaintenanceBackgroundJobFilter() string {
	return fmt.Sprintf(
		"job_type <> $3 AND job_type NOT IN ('%s', '%s', '%s', '%s', '%s')",
		memoryExtractTurnJobType,
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
		WITH project_async_budget AS (
			SELECT GREATEST(0, %d - COUNT(*)) AS available
			FROM model_invocation mi
			JOIN chat_session cs
			  ON cs.id = mi.session_id
			LEFT JOIN chat_turn ct
			  ON ct.id = mi.turn_id
			WHERE mi.status = 'in_flight'
			  AND cs.scope_type = 'project'
			  AND (
			        mi.turn_id IS NULL
			     OR ct.status IN ('pending', 'in_progress')
			      )
		),
		ranked AS (
			SELECT id,
			       job_type,
			       priority,
			       run_after,
			       created_at,
			       CASE
			           WHEN job_type = '%s'
			             AND EXISTS (
			                 SELECT 1
			                 FROM chat_session cs
			                 JOIN chat_message cm
			                   ON cm.id = (job_queue.payload->>'message_id')::uuid
			                 WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			                   AND cs.scope_type = 'project'
			                   AND cs.mode = 'async'
			                   AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_bootstrap')
			             )
			           THEN 1
			           ELSE 0
			       END AS project_async_continuation,
			       CASE
			           WHEN job_type = '%s'
			             AND EXISTS (
			                 SELECT 1
			                 FROM chat_session cs
			                 JOIN chat_message cm
			                   ON cm.id = (job_queue.payload->>'message_id')::uuid
			                 WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			                   AND cs.scope_type = 'project'
			                   AND cs.mode = 'async'
			                   AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_bootstrap')
			             )
			           THEN 1
			           ELSE 0
			           END AS agent_turn_claim_bias,
			       ROW_NUMBER() OVER (
			           PARTITION BY CASE
			               WHEN job_type = '%s' THEN COALESCE(payload->>'session_id', id::text)
			               ELSE id::text
			           END
			           ORDER BY priority DESC,
			                    CASE
			                        WHEN job_type = '%s'
			                          AND EXISTS (
			                              SELECT 1
			                              FROM chat_session cs
			                              JOIN chat_message cm
			                                ON cm.id = (job_queue.payload->>'message_id')::uuid
			                              JOIN flow_node_execution e
			                                ON e.session_id = cs.id
			                               AND e.status = 'active'
			                               AND e.id::text = COALESCE(job_queue.payload->>'flow_node_execution_id', '')
			                              WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			                                AND cs.scope_type = 'project_task'
			                                AND cs.mode = 'async'
			                                AND COALESCE(cm.metadata->>'source', '') IN ('task_queue_processor', 'task_review_action', 'task_recovery_resume')
			                                AND COALESCE(cm.metadata->>'flow_node_execution_id', '') = COALESCE(job_queue.payload->>'flow_node_execution_id', '')
			                          )
			                        THEN 0
			                        WHEN job_type = '%s'
			                          AND EXISTS (
			                              SELECT 1
			                              FROM chat_session cs
			                              JOIN flow_node_execution e
			                                ON e.session_id = cs.id
			                               AND e.status = 'active'
			                              WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			                                AND cs.scope_type = 'project_task'
			                                AND cs.mode = 'async'
			                          )
			                        THEN 1
			                        ELSE 0
			                    END ASC,
			                    CASE
			                        WHEN job_type = '%s'
			                          AND EXISTS (
			                              SELECT 1
			                              FROM chat_session cs
			                              JOIN chat_message cm
			                                ON cm.id = (job_queue.payload->>'message_id')::uuid
			                              WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			                                AND cs.scope_type = 'project'
			                                AND cs.mode = 'async'
			                                AND COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_bootstrap')
			                          )
			                        THEN 1
			                        ELSE 0
			                    END ASC,
			                    CASE WHEN job_type = '%s' THEN run_after END DESC,
			                    CASE WHEN job_type <> '%s' THEN run_after END ASC,
			                    CASE WHEN job_type = '%s' THEN created_at END DESC,
			                    CASE WHEN job_type <> '%s' THEN created_at END ASC,
			                    CASE WHEN job_type = '%s' THEN id END DESC,
			                    CASE WHEN job_type <> '%s' THEN id END ASC
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
			      FROM chat_session cs
			      LEFT JOIN project p_direct
			        ON cs.scope_type = 'project'
			       AND p_direct.id = cs.scope_id
			      LEFT JOIN project_task pt
			        ON cs.scope_type = 'project_task'
			       AND pt.id = cs.scope_id
			      LEFT JOIN project p_task
			        ON p_task.id = pt.project_id
			      WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			        AND cs.status = 'active'
			        AND COALESCE(
			              p_direct.settings->'pause'->>'is_paused',
			              p_task.settings->'pause'->>'is_paused',
			              'false'
			            ) = 'true'
			    )
			  )
			  AND (
			    job_type <> '%s'
			    OR NOT EXISTS (
			      SELECT 1
			      FROM job_queue sibling_claim
			      WHERE sibling_claim.id <> job_queue.id
			        AND sibling_claim.job_type = '%s'
			        AND sibling_claim.status = 'claimed'
			        AND COALESCE(sibling_claim.payload->>'session_id', '') = COALESCE(job_queue.payload->>'session_id', '')
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
			      JOIN chat_message cm
			        ON cm.id = (job_queue.payload->>'message_id')::uuid
			      WHERE cs.id = (job_queue.payload->>'session_id')::uuid
			        AND cs.scope_type = 'project'
			        AND (
			              (
			                COALESCE(cm.metadata->>'source', '') = 'project_bootstrap'
			                AND COALESCE(cs.metadata->'project_bootstrap'->>'status', '') <> 'active'
			              )
			           OR (
			                COALESCE(cm.metadata->>'source', '') IN ('project_execution_continuation', 'project_continuation_resume')
			                AND NOT EXISTS (
			                  SELECT 1
			                  FROM project_task pt
			                  WHERE pt.project_id = cs.scope_id
			                    AND pt.work_status NOT IN ('done', 'cancelled')
			                )
			              )
			            )
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
			          (
			            current_turn.status = 'in_progress'
			            AND (
			              cs.scope_type <> 'project_task'
			              OR EXISTS (
			                SELECT 1
			                FROM run r
			                WHERE r.session_id = cs.id
			                  AND r.turn_id = current_turn.id
			                  AND r.status IN ('created', 'in_progress')
			                  AND (
			                        r.status = 'created'
			                     OR EXISTS (
			                          SELECT 1
			                          FROM model_invocation mi
			                          WHERE mi.turn_id = current_turn.id
			                            AND mi.status = 'in_flight'
			                        )
			                  )
			              )
			            )
			          )
			          OR (
			            current_turn.status = 'pending'
			            AND (
			              EXISTS (
			                SELECT 1
			                FROM job_queue blocking_jq
			                WHERE blocking_jq.job_type = '%s'
			                  AND blocking_jq.status IN ('pending', 'claimed')
			                  AND (blocking_jq.payload->>'session_id')::uuid = cs.id
			                  AND (blocking_jq.payload->>'message_id')::uuid = current_turn.trigger_message_id
			                  AND COALESCE((blocking_jq.payload->>'retry_count')::int, 0) = current_turn.retry_count
			              )
			              OR EXISTS (
			                SELECT 1
			                FROM flow_node_execution blocking_execution
			                WHERE blocking_execution.session_id = cs.id
			                  AND blocking_execution.status = 'active'
			                  AND COALESCE(blocking_execution.metadata->>'live_turn_id', '') ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
			                  AND (blocking_execution.metadata->>'live_turn_id')::uuid = current_turn.id
			              )
			            )
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
		deduped AS (
			SELECT *,
			       CASE
			           WHEN project_async_continuation = 1
			           THEN ROW_NUMBER() OVER (
			               ORDER BY priority DESC,
			                        agent_turn_claim_bias ASC,
			                        CASE WHEN job_type = 'agent_turn' THEN run_after END DESC,
			                        CASE WHEN job_type <> 'agent_turn' THEN run_after END ASC,
			                        CASE WHEN job_type = 'agent_turn' THEN created_at END DESC,
			                        CASE WHEN job_type <> 'agent_turn' THEN created_at END ASC
			           )
			           ELSE NULL
			       END AS project_async_rank
			FROM ranked
			WHERE session_rn = 1
		),
		claimable AS (
			SELECT id
			FROM deduped
			WHERE (
			        project_async_continuation = 0
			     OR project_async_rank <= (SELECT available FROM project_async_budget)
			      )
			ORDER BY priority DESC,
			         agent_turn_claim_bias ASC,
			         CASE
			             WHEN job_type = 'agent_turn' AND project_async_continuation = 1
			             THEN run_after
			         END DESC,
			         CASE
			             WHEN job_type = 'agent_turn' AND project_async_continuation = 0
			             THEN run_after
			         END ASC,
			         CASE WHEN job_type <> 'agent_turn' THEN run_after END ASC,
			         CASE
			             WHEN job_type = 'agent_turn' AND project_async_continuation = 1
			             THEN created_at
			         END DESC,
			         CASE
			             WHEN job_type = 'agent_turn' AND project_async_continuation = 0
			             THEN created_at
			         END ASC,
			         CASE WHEN job_type <> 'agent_turn' THEN created_at END ASC
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
		`, maxInFlightProjectContinuations, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, agentTurnJobType, whereFilter), args...)
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

	for _, job := range jobs {
		if !strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) {
			continue
		}
		if err := w.deadLetterSupersededSameSessionAgentTurnJobs(ctx, job); err != nil {
			return nil, err
		}
	}

	return jobs, nil
}

func (w *Worker) deadLetterSupersededSameSessionAgentTurnJobs(ctx context.Context, job Job) error {
	if w == nil || w.pool == nil || !strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) {
		return nil
	}
	var payload agentTurnKeyPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode claimed %s payload for same-session purge: %w", agentTurnJobType, err)
	}
	if payload.SessionID == uuid.Nil {
		return nil
	}

	tag, err := w.pool.Exec(ctx, `
		UPDATE job_queue jq
		SET status = 'dead_letter',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    last_error = 'superseded by newer claimed same-session dispatch',
		    updated_at = now()
		WHERE jq.job_type = $1
		  AND jq.status IN ('pending', 'claimed')
		  AND jq.id <> $2
		  AND (jq.payload->>'session_id')::uuid = $3
		  AND EXISTS (
		    SELECT 1
		    FROM chat_session cs
		    WHERE cs.id = $3
		      AND cs.mode = 'async'
		      AND cs.scope_type IN ('project', 'project_task')
		  )
	`, agentTurnJobType, job.ID, payload.SessionID)
	if err != nil {
		return fmt.Errorf("dead-letter superseded same-session agent_turn jobs for session %s: %w", payload.SessionID, err)
	}
	if tag.RowsAffected() > 0 {
		w.logger.Info("job queue: dead-lettered superseded same-session agent_turn jobs", "job_id", job.ID, "session_id", payload.SessionID, "count", tag.RowsAffected())
	}
	return nil
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
		if markErr := w.markDone(ctx, job.ID); markErr != nil {
			return markErr
		}
		if strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) {
			w.launchPostAgentTurnRepairs(job.ID)
		}
		return nil
	}
	return w.markFailure(ctx, job, err)
}

func (w *Worker) launchPostAgentTurnRepairs(jobID uuid.UUID) {
	if w == nil {
		return
	}
	go func() {
		repairCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if requeued, repairErr := w.RequeueActiveExecutionSessionsWithoutTurns(repairCtx); repairErr != nil {
			if repairCtx.Err() == nil {
				w.logger.Warn("job queue: async active execution session repair failed", "job_id", jobID, "error", repairErr)
			}
		} else if requeued > 0 {
			w.logger.Info("job queue: requeued active execution sessions without turns after agent_turn completion", "job_id", jobID, "count", requeued)
		}
	}()
}

func (w *Worker) maintainClaim(ctx context.Context, job Job, cancelExecution context.CancelFunc) {
	heartbeatInterval := w.claimHeartbeatInterval(job)
	if heartbeatInterval <= 0 {
		return
	}

	ticker := time.NewTicker(heartbeatInterval)
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
	if strings.EqualFold(strings.TrimSpace(job.JobType), agentTurnJobType) && claimedAgentTurnHeartbeatGrace < threshold {
		threshold = claimedAgentTurnHeartbeatGrace
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
	var deferred *DeferredJobError
	if errors.As(jobErr, &deferred) {
		return w.markDeferred(ctx, job, deferred)
	}

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

func (w *Worker) markDeferred(ctx context.Context, job Job, deferred *DeferredJobError) error {
	if deferred == nil {
		return fmt.Errorf("deferred job error is required")
	}

	runAfter := deferred.RunAfter.UTC()
	now := w.clock.Now().UTC()
	if runAfter.IsZero() || !runAfter.After(now) {
		runAfter = now
	}

	reason := strings.TrimSpace(deferred.Reason)
	if reason == "" {
		reason = deferred.Error()
	}

	_, err := w.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'pending',
		    claimed_by = NULL,
		    claimed_at = NULL,
		    attempts = GREATEST(attempts - 1, 0),
		    run_after = $2,
		    last_error = $3,
		    updated_at = now()
		WHERE id = $1
	`, job.ID, runAfter, reason)
	if err != nil {
		return fmt.Errorf("mark job deferred: %w", err)
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
			if repaired, err := w.CloseBlockedProjectTaskAsyncSessionsWithoutLiveExecution(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("blocked project_task non-live execution session cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: closed blocked project_task async sessions without live execution", "count", repaired)
			}
			if repaired, err := w.RetireClosedAsyncSessionRuns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("closed async session run cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: retired closed async session runs", "count", repaired)
			}
			if repaired, err := w.FailStaleModelInvocations(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("stale model invocation cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: failed stale model invocations", "count", repaired)
			}
			if repaired, err := w.ClearCompletedSessionCurrentTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("post-invocation current turn cleanup failed", "error", err)
			} else if repaired > 0 {
				w.logger.Info("job queue: cleared completed session current turns after invocation cleanup", "count", repaired)
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
			if recovered, err := w.RecoverClaimedAgentTurnsWithoutLiveOwnership(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("non-heartbeating claimed agent_turn recovery failed", "error", err)
			} else if recovered > 0 {
				w.logger.Info("job queue: recovered non-heartbeating claimed agent_turn jobs", "count", recovered)
			}
			if requeued, err := w.RequeueActiveExecutionSessionsWithoutTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("active execution session repair failed", "error", err)
			} else if requeued > 0 {
				w.logger.Info("job queue: requeued active execution sessions without turns", "count", requeued)
			}
			if requeued, err := w.RequeueActiveProjectSessionsWithoutTurns(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("active project session repair failed", "error", err)
			} else if requeued > 0 {
				w.logger.Info("job queue: requeued active project sessions without turns", "count", requeued)
			}
			if requeued, err := w.RequeueActiveProjectSessionsMissingContinuation(ctx); err != nil && ctx.Err() == nil {
				w.logger.Error("idle project continuation recovery failed", "error", err)
			} else if requeued > 0 {
				w.logger.Info("job queue: requeued active project sessions missing continuation", "count", requeued)
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

func parseRateLimitRetryAfterFromText(text string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.Contains(strings.ToLower(trimmed), "model provider rate limited") {
		return 0, false
	}
	const marker = "retry_after="
	idx := strings.Index(trimmed, marker)
	if idx < 0 {
		return 0, false
	}
	value := trimmed[idx+len(marker):]
	if end := strings.IndexAny(value, "):, ]"); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, false
	}
	if d < 0 {
		d = 0
	}
	return d, true
}

func parseTransientModelRetryAfterFromText(text string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !looksLikeTransientModelFailureText(trimmed) {
		return 0, false
	}
	const marker = "retry_after="
	idx := strings.Index(trimmed, marker)
	if idx < 0 {
		return 0, false
	}
	value := trimmed[idx+len(marker):]
	if end := strings.IndexAny(value, "):, ]"); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, false
	}
	if d < 0 {
		d = 0
	}
	return d, true
}

func looksLikeTransientModelFailureText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "model provider rate limited") {
		return false
	}
	if strings.Contains(lower, "transient model failure") {
		return true
	}
	if strings.Contains(lower, "all provider connections are temporarily unavailable") {
		return true
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "temporar") {
		return true
	}
	if strings.Contains(lower, "stream error") && (strings.Contains(lower, "received from peer") || strings.Contains(lower, "internal_error") || strings.Contains(lower, "internal error")) {
		return true
	}
	if strings.Contains(lower, "received from peer") && (strings.Contains(lower, "internal_error") || strings.Contains(lower, "internal error")) {
		return true
	}
	return false
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
		if retryAfterHint > agentTurnRateLimitBackoffCap {
			return agentTurnRateLimitBackoffCap
		}
		return retryAfterHint
	}
	return delay
}

func agentTurnTransientDelay(attempts int, retryAfterHint time.Duration) time.Duration {
	if attempts <= 1 {
		attempts = 1
	}

	delay := agentTurnTransientMinBackoff
	for i := 1; i < attempts; i++ {
		if delay >= (agentTurnTransientBackoffCap / 2) {
			delay = agentTurnTransientBackoffCap
			break
		}
		delay *= 2
	}

	if retryAfterHint > delay {
		if retryAfterHint > agentTurnTransientBackoffCap {
			return agentTurnTransientBackoffCap
		}
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
