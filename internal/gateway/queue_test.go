package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type stubRouter struct {
	connection repo.ProviderConnection
	err        error
}

func (s stubRouter) SelectConnection(_ context.Context, _ uuid.UUID, _ string, _ PriorityTier) (*repo.ProviderConnection, error) {
	if s.err != nil {
		return nil, s.err
	}
	connection := s.connection
	return &connection, nil
}

type stubExecutor struct {
	run func(ctx context.Context, req GatewayRequest, connection repo.ProviderConnection) (GatewayResponse, error)
}

func (s stubExecutor) Execute(ctx context.Context, req GatewayRequest, connection repo.ProviderConnection) (GatewayResponse, error) {
	return s.run(ctx, req, connection)
}

type retryCall struct {
	failedInvocationID uuid.UUID
	attemptNumber      int
}

type retryRecorder struct {
	mu      sync.Mutex
	calls   []retryCall
	nextIDs []uuid.UUID
}

func (r *retryRecorder) RecordRetry(_ context.Context, failedInvocationID uuid.UUID, attemptNumber int) (uuid.UUID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, retryCall{failedInvocationID: failedInvocationID, attemptNumber: attemptNumber})

	if len(r.nextIDs) == 0 {
		return uuid.New(), nil
	}
	next := r.nextIDs[0]
	r.nextIDs = r.nextIDs[1:]
	return next, nil
}

func TestPriorityQueueOrdersSyncInteractiveBeforeAsync(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	holdStarted := make(chan struct{})
	holdRelease := make(chan struct{})

	var (
		mu    sync.Mutex
		order []string
	)

	queue, err := NewPriorityQueue(QueueOptions{
		Concurrency: NewConcurrencyManager(1, map[uuid.UUID]int{connectionID: 1}),
		Router:      stubRouter{connection: repo.ProviderConnection{ID: connectionID, ProviderID: uuid.New(), MaxConcurrent: 1, IsEnabled: true}},
		Executor: stubExecutor{
			run: func(ctx context.Context, req GatewayRequest, _ repo.ProviderConnection) (GatewayResponse, error) {
				if req.InvocationPurpose == "hold" {
					close(holdStarted)
					select {
					case <-holdRelease:
					case <-ctx.Done():
						return GatewayResponse{}, ctx.Err()
					}
					return GatewayResponse{}, nil
				}
				mu.Lock()
				order = append(order, req.InvocationPurpose)
				mu.Unlock()
				return GatewayResponse{}, nil
			},
		},
		PollInterval: 2 * time.Millisecond,
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(context.Background(), GatewayRequest{
			OrganizationID:    orgID,
			ProfileID:         "standard",
			InvocationPurpose: "hold",
			Priority:          PrioritySyncSystem,
		})
	}()

	select {
	case <-holdStarted:
	case <-time.After(time.Second):
		t.Fatal("hold invocation did not start")
	}

	asyncDone := make(chan error, 1)
	go func() {
		_, enqueueErr := queue.Enqueue(context.Background(), GatewayRequest{
			OrganizationID:    orgID,
			ProfileID:         "standard",
			InvocationPurpose: "async-queued",
			Priority:          PriorityAsyncAgent,
		})
		asyncDone <- enqueueErr
	}()

	syncDone := make(chan error, 1)
	go func() {
		_, enqueueErr := queue.Enqueue(context.Background(), GatewayRequest{
			OrganizationID:    orgID,
			ProfileID:         "standard",
			InvocationPurpose: "sync",
			Priority:          PrioritySyncInteractive,
		})
		syncDone <- enqueueErr
	}()

	time.Sleep(25 * time.Millisecond)

	close(holdRelease)

	if enqueueErr := <-syncDone; enqueueErr != nil {
		t.Fatalf("sync enqueue error: %v", enqueueErr)
	}
	if enqueueErr := <-asyncDone; enqueueErr != nil {
		t.Fatalf("async enqueue error: %v", enqueueErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("order length = %d, want 2", len(order))
	}
	if order[0] != "sync" || order[1] != "async-queued" {
		t.Fatalf("execution order = %v, want [sync async-queued]", order)
	}
}

func TestPriorityQueueSoftPreemptionCancelsAsync(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	started := make(chan struct{})
	cancelled := make(chan struct{})

	queue, err := NewPriorityQueue(QueueOptions{
		Concurrency: NewConcurrencyManager(1, map[uuid.UUID]int{connectionID: 1}),
		Router:      stubRouter{connection: repo.ProviderConnection{ID: connectionID, ProviderID: uuid.New(), MaxConcurrent: 1, IsEnabled: true}},
		Executor: stubExecutor{
			run: func(ctx context.Context, req GatewayRequest, _ repo.ProviderConnection) (GatewayResponse, error) {
				if req.InvocationPurpose == "async-long" {
					close(started)
					<-ctx.Done()
					close(cancelled)
					return GatewayResponse{}, ctx.Err()
				}
				return GatewayResponse{}, nil
			},
		},
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(context.Background(), GatewayRequest{
			OrganizationID:    orgID,
			ProfileID:         "standard",
			InvocationPurpose: "async-long",
			Priority:          PriorityAsyncAgent,
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("async-long invocation did not start")
	}

	_, err = queue.Enqueue(context.Background(), GatewayRequest{
		OrganizationID:    orgID,
		ProfileID:         "standard",
		InvocationPurpose: "sync",
		Priority:          PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("sync enqueue failed: %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("async invocation was not cancelled by preemption")
	}
}

func TestPriorityQueueReturnsErrQueueTimeout(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	hold := make(chan struct{})
	holdStarted := make(chan struct{})

	queue, err := NewPriorityQueue(QueueOptions{
		Concurrency: NewConcurrencyManager(1, map[uuid.UUID]int{connectionID: 1}),
		Router:      stubRouter{connection: repo.ProviderConnection{ID: connectionID, ProviderID: uuid.New(), MaxConcurrent: 1, IsEnabled: true}},
		Executor: stubExecutor{
			run: func(ctx context.Context, req GatewayRequest, _ repo.ProviderConnection) (GatewayResponse, error) {
				if req.InvocationPurpose == "hold" {
					close(holdStarted)
					<-hold
				}
				return GatewayResponse{}, nil
			},
		},
		Timeouts: map[PriorityTier]time.Duration{
			PrioritySyncInteractive: 60 * time.Millisecond,
		},
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(context.Background(), GatewayRequest{
			OrganizationID:    orgID,
			ProfileID:         "standard",
			InvocationPurpose: "hold",
			Priority:          PriorityAsyncAgent,
		})
	}()

	select {
	case <-holdStarted:
	case <-time.After(time.Second):
		t.Fatal("hold invocation did not start")
	}

	_, err = queue.Enqueue(context.Background(), GatewayRequest{
		OrganizationID:    orgID,
		ProfileID:         "standard",
		InvocationPurpose: "sync",
		Priority:          PrioritySyncInteractive,
	})
	if !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("enqueue error = %v, want ErrQueueTimeout", err)
	}

	close(hold)
}

func TestPriorityQueueRetryBehavior(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	initialInvocationID := uuid.New()
	firstRetryID := uuid.New()
	secondRetryID := uuid.New()

	recorder := &retryRecorder{
		nextIDs: []uuid.UUID{firstRetryID, secondRetryID},
	}

	attempt := 0
	queue, err := NewPriorityQueue(QueueOptions{
		Concurrency:   NewConcurrencyManager(1, map[uuid.UUID]int{connectionID: 1}),
		Router:        stubRouter{connection: repo.ProviderConnection{ID: connectionID, ProviderID: uuid.New(), MaxConcurrent: 1, IsEnabled: true}},
		RetryRecorder: recorder,
		Executor: stubExecutor{
			run: func(_ context.Context, _ GatewayRequest, _ repo.ProviderConnection) (GatewayResponse, error) {
				attempt++
				if attempt < 3 {
					return GatewayResponse{}, ProviderHTTPError{StatusCode: 500}
				}
				return GatewayResponse{}, nil
			},
		},
		PollInterval: 2 * time.Millisecond,
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	response, err := queue.Enqueue(context.Background(), GatewayRequest{
		OrganizationID:    orgID,
		ProfileID:         "standard",
		InvocationPurpose: "retry",
		Priority:          PrioritySyncInteractive,
		InvocationID:      initialInvocationID,
	})
	if err != nil {
		t.Fatalf("enqueue retry request: %v", err)
	}
	if response.AttemptNumber != 3 {
		t.Fatalf("attempt number = %d, want 3", response.AttemptNumber)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.calls) != 2 {
		t.Fatalf("retry calls = %d, want 2", len(recorder.calls))
	}
	if recorder.calls[0].failedInvocationID != initialInvocationID || recorder.calls[0].attemptNumber != 2 {
		t.Fatalf("first retry call = %+v", recorder.calls[0])
	}
	if recorder.calls[1].failedInvocationID != firstRetryID || recorder.calls[1].attemptNumber != 3 {
		t.Fatalf("second retry call = %+v", recorder.calls[1])
	}
}

func TestPriorityQueueDoesNotRetryNonRetryableErrors(t *testing.T) {
	connectionID := uuid.New()
	orgID := uuid.New()
	attempts := 0

	queue, err := NewPriorityQueue(QueueOptions{
		Concurrency: NewConcurrencyManager(1, map[uuid.UUID]int{connectionID: 1}),
		Router:      stubRouter{connection: repo.ProviderConnection{ID: connectionID, ProviderID: uuid.New(), MaxConcurrent: 1, IsEnabled: true}},
		Executor: stubExecutor{
			run: func(_ context.Context, _ GatewayRequest, _ repo.ProviderConnection) (GatewayResponse, error) {
				attempts++
				return GatewayResponse{}, ProviderHTTPError{StatusCode: 400}
			},
		},
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	_, err = queue.Enqueue(context.Background(), GatewayRequest{
		OrganizationID:    orgID,
		ProfileID:         "standard",
		InvocationPurpose: "non-retryable",
		Priority:          PrioritySyncInteractive,
	})
	var providerErr ProviderHTTPError
	if !errors.As(err, &providerErr) {
		t.Fatalf("enqueue error = %v, want ProviderHTTPError", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}
