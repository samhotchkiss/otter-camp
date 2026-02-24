package gateway

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const defaultQueuePollInterval = 10 * time.Millisecond

type QueueOptions struct {
	Concurrency   *ConcurrencyManager
	Router        ConnectionSelector
	Executor      GatewayExecutor
	Health        *HealthChecker
	RetryRecorder InvocationRetryRecorder

	Timeouts     map[PriorityTier]time.Duration
	PollInterval time.Duration
	Sleep        func(ctx context.Context, delay time.Duration) error
	Now          func() time.Time
}

type PriorityQueue struct {
	concurrency   *ConcurrencyManager
	router        ConnectionSelector
	executor      GatewayExecutor
	health        *HealthChecker
	retryRecorder InvocationRetryRecorder
	timeouts      map[PriorityTier]time.Duration
	pollInterval  time.Duration
	sleep         func(ctx context.Context, delay time.Duration) error
	now           func() time.Time

	mu      sync.Mutex
	pending map[PriorityTier][]*queueEntry
	active  map[uuid.UUID]activeInvocation

	notify chan struct{}
	stop   chan struct{}
	done   chan struct{}
}

type queueEntry struct {
	id         uuid.UUID
	req        GatewayRequest
	ctx        context.Context
	connection repo.ProviderConnection
	deadline   time.Time
	result     chan queueResult
}

type queueResult struct {
	response GatewayResponse
	err      error
}

type activeInvocation struct {
	tier      PriorityTier
	startedAt time.Time
	cancel    context.CancelFunc
}

func NewPriorityQueue(opts QueueOptions) (*PriorityQueue, error) {
	if opts.Concurrency == nil {
		return nil, fmt.Errorf("concurrency manager is required")
	}
	if opts.Router == nil {
		return nil, fmt.Errorf("router is required")
	}
	if opts.Executor == nil {
		return nil, fmt.Errorf("executor is required")
	}
	if opts.Health == nil {
		opts.Health = NewHealthChecker()
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultQueuePollInterval
	}
	if opts.Sleep == nil {
		opts.Sleep = defaultSleep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	timeouts := make(map[PriorityTier]time.Duration, len(defaultQueueTimeouts))
	for key, value := range defaultQueueTimeouts {
		timeouts[key] = value
	}
	for key, value := range opts.Timeouts {
		if key.IsValid() && value > 0 {
			timeouts[key] = value
		}
	}

	queue := &PriorityQueue{
		concurrency:   opts.Concurrency,
		router:        opts.Router,
		executor:      opts.Executor,
		health:        opts.Health,
		retryRecorder: opts.RetryRecorder,
		timeouts:      timeouts,
		pollInterval:  opts.PollInterval,
		sleep:         opts.Sleep,
		now:           opts.Now,
		pending: map[PriorityTier][]*queueEntry{
			PrioritySyncInteractive: {},
			PrioritySyncSystem:      {},
			PriorityAsyncAgent:      {},
			PriorityAsyncSystem:     {},
		},
		active: make(map[uuid.UUID]activeInvocation),
		notify: make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	go queue.dispatchLoop()
	return queue, nil
}

func (q *PriorityQueue) Close() {
	close(q.stop)
	<-q.done
}

func (q *PriorityQueue) Enqueue(ctx context.Context, req GatewayRequest) (GatewayResponse, error) {
	if q == nil {
		return GatewayResponse{}, fmt.Errorf("priority queue is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.OrganizationID == uuid.Nil {
		return GatewayResponse{}, fmt.Errorf("organization id is required")
	}
	if !req.Priority.IsValid() {
		return GatewayResponse{}, fmt.Errorf("priority tier %q is invalid", req.Priority)
	}

	connection, err := q.router.SelectConnection(ctx, req.OrganizationID, req.ProfileID, req.Priority)
	if err != nil {
		return GatewayResponse{}, err
	}

	entry := &queueEntry{
		id:         uuid.New(),
		req:        req,
		ctx:        ctx,
		connection: *connection,
		deadline:   q.now().UTC().Add(q.timeouts[req.Priority]),
		result:     make(chan queueResult, 1),
	}

	q.mu.Lock()
	q.pending[req.Priority] = append(q.pending[req.Priority], entry)
	if req.Priority == PrioritySyncInteractive {
		q.preemptLatestAsyncLocked()
	}
	q.mu.Unlock()
	q.signal()

	select {
	case <-ctx.Done():
		q.removePending(entry.id)
		return GatewayResponse{}, ctx.Err()
	case result := <-entry.result:
		return result.response, result.err
	}
}

func (q *PriorityQueue) dispatchLoop() {
	defer close(q.done)

	ticker := time.NewTicker(q.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-q.stop:
			return
		case <-q.notify:
		case <-ticker.C:
		}

		q.expirePending()
		for {
			entry, release, runCtx, cancel := q.nextDispatchable()
			if entry == nil {
				break
			}
			go q.runEntry(entry, release, runCtx, cancel)
		}
	}
}

func (q *PriorityQueue) runEntry(entry *queueEntry, release func(), runCtx context.Context, cancel context.CancelFunc) {
	defer func() {
		release()
		cancel()

		q.mu.Lock()
		delete(q.active, entry.id)
		q.mu.Unlock()
		q.signal()
	}()

	response, err := q.executeWithRetry(runCtx, entry.req, entry.connection)
	entry.result <- queueResult{response: response, err: err}
}

func (q *PriorityQueue) executeWithRetry(ctx context.Context, req GatewayRequest, connection repo.ProviderConnection) (GatewayResponse, error) {
	attempt := 1
	previousInvocationID := req.InvocationID

	for {
		response, err := q.executor.Execute(ctx, req, connection)
		if err == nil {
			q.health.RecordSuccess(connection.ID)
			response.ConnectionID = connection.ID
			response.ProviderID = connection.ProviderID
			response.AttemptNumber = attempt
			return response, nil
		}

		q.health.RecordFailure(connection.ID, err)
		if !isRetryableGatewayError(err) || attempt >= 3 {
			return GatewayResponse{}, err
		}

		attempt++
		if q.retryRecorder != nil && previousInvocationID != uuid.Nil {
			nextInvocationID, recordErr := q.retryRecorder.RecordRetry(ctx, previousInvocationID, attempt)
			if recordErr != nil {
				return GatewayResponse{}, recordErr
			}
			previousInvocationID = nextInvocationID
			req.InvocationID = nextInvocationID
		}

		delay := q.withJitter(retryDelay(err, attempt-1))
		if sleepErr := q.sleep(ctx, delay); sleepErr != nil {
			return GatewayResponse{}, sleepErr
		}
	}
}

func (q *PriorityQueue) withJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}

	scale := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(base) * scale)
}

func (q *PriorityQueue) nextDispatchable() (*queueEntry, func(), context.Context, context.CancelFunc) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, tier := range priorityOrder {
		items := q.pending[tier]
		if len(items) == 0 {
			continue
		}

		entry := items[0]
		release, ok := q.concurrency.TryReserve(entry.connection.ID)
		if !ok {
			continue
		}

		q.pending[tier] = items[1:]
		runCtx, cancel := context.WithCancel(entry.ctx)
		q.active[entry.id] = activeInvocation{
			tier:      tier,
			startedAt: q.now().UTC(),
			cancel:    cancel,
		}

		return entry, release, runCtx, cancel
	}

	return nil, nil, nil, nil
}

func (q *PriorityQueue) expirePending() {
	now := q.now().UTC()
	expired := make([]*queueEntry, 0)

	q.mu.Lock()
	for _, tier := range priorityOrder {
		if len(q.pending[tier]) == 0 {
			continue
		}

		next := q.pending[tier][:0]
		for _, entry := range q.pending[tier] {
			if now.After(entry.deadline) {
				expired = append(expired, entry)
				continue
			}
			next = append(next, entry)
		}
		q.pending[tier] = next
	}
	q.mu.Unlock()

	for _, entry := range expired {
		entry.result <- queueResult{err: ErrQueueTimeout}
	}
}

func (q *PriorityQueue) removePending(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, tier := range priorityOrder {
		items := q.pending[tier]
		for idx, entry := range items {
			if entry.id != id {
				continue
			}
			q.pending[tier] = append(items[:idx], items[idx+1:]...)
			return
		}
	}
}

func (q *PriorityQueue) preemptLatestAsyncLocked() {
	if !q.concurrency.IsAtGlobalCapacity() {
		return
	}

	var (
		targetID uuid.UUID
		target   activeInvocation
		found    bool
	)

	for id, invocation := range q.active {
		if invocation.tier != PriorityAsyncAgent && invocation.tier != PriorityAsyncSystem {
			continue
		}
		if !found || invocation.startedAt.After(target.startedAt) {
			found = true
			targetID = id
			target = invocation
		}
	}

	if found {
		target.cancel()
		delete(q.active, targetID)
	}
}

func (q *PriorityQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func defaultSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
