//go:build integration

package modelgw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/gateway"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

type codedError struct {
	Code string
	Err  error
}

func (e codedError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

func (e codedError) Unwrap() error {
	return e.Err
}

type providerHTTPExecutor struct {
	client *http.Client
}

func (e providerHTTPExecutor) Execute(ctx context.Context, _ gateway.GatewayRequest, connection repo.ProviderConnection) (gateway.GatewayResponse, error) {
	baseURL := strings.TrimSpace(connectionURL(connection))
	if baseURL == "" {
		return gateway.GatewayResponse{}, gateway.ProviderHTTPError{
			StatusCode: http.StatusServiceUnavailable,
			Err:        errors.New("provider endpoint not configured"),
		}
	}

	client := e.client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/messages", nil)
	if err != nil {
		return gateway.GatewayResponse{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return gateway.GatewayResponse{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= http.StatusBadRequest {
		return gateway.GatewayResponse{}, gateway.ProviderHTTPError{
			StatusCode: resp.StatusCode,
			RetryAfter: retryAfter(resp),
		}
	}

	return gateway.GatewayResponse{}, nil
}

func connectionURL(connection repo.ProviderConnection) string {
	if connection.APIBaseURLOverride != nil {
		return strings.TrimSpace(*connection.APIBaseURLOverride)
	}
	return ""
}

func retryAfter(resp *http.Response) time.Duration {
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	seconds, err := time.ParseDuration(raw + "s")
	if err != nil {
		return 0
	}
	return seconds
}

func TestRouting_HealthAwareSelection(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "routing-health-aware")
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	serverA := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{StatusCode: http.StatusInternalServerError, Body: `{"error":"degraded"}`},
			{StatusCode: http.StatusInternalServerError, Body: `{"error":"degraded"}`},
			{StatusCode: http.StatusInternalServerError, Body: `{"error":"degraded"}`},
		},
	})
	fixture := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  serverA,
		ConnectionName:      "conn-A",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "routing-health-a-" + uuid.NewString()[:8],
		ProviderDisplayName: "Routing Health A",
	})

	serverB := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"ok":true}`}},
	})
	connBURL := strings.TrimSpace(serverB)
	connB, err := connectionRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         fixture.Provider.ID,
		DisplayName:        "conn-B",
		APIKeyRef:          "ref:conn-b",
		APIBaseURLOverride: &connBURL,
		FailoverPriority:   2,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})
	if err != nil {
		t.Fatalf("create conn-B: %v", err)
	}

	profile := testutil.MakeModelProfile(t, pool, org.ID, fixture.Provider.ID)
	health := gateway.NewHealthChecker()
	// Prime one failure so the next 500 transitions conn-A to degraded.
	health.RecordFailure(fixture.Connection.ID, gateway.ProviderHTTPError{StatusCode: http.StatusInternalServerError})

	router := gateway.NewRouter(profileRepo, connectionRepo, health)
	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency: gateway.NewConcurrencyManager(1, map[uuid.UUID]int{
			fixture.Connection.ID: 1,
			connB.ID:              1,
		}),
		Router:       router,
		Executor:     providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
		Health:       health,
		HealthStore:  connectionRepo,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	_, err = queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err == nil {
		t.Fatal("first enqueue unexpectedly succeeded; expected conn-A failure")
	}
	if state := health.GetState(fixture.Connection.ID); state != gateway.HealthStateDegraded {
		t.Fatalf("conn-A health state = %q, want %q", state, gateway.HealthStateDegraded)
	}

	response, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if response.ConnectionID != connB.ID {
		t.Fatalf("selected connection = %s, want %s", response.ConnectionID, connB.ID)
	}

	modelProfileID := profile.LogicalProfileID
	created, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      fixture.Provider.ID,
		ProviderConnectionID: &response.ConnectionID,
		ModelProfileID:       &modelProfileID,
		InvocationPurpose:    "agent_turn",
		Status:               "completed",
		ModelName:            profile.ModelName,
	})
	if err != nil {
		t.Fatalf("create model_invocation: %v", err)
	}
	if created.ProviderConnectionID == nil || *created.ProviderConnectionID != connB.ID {
		t.Fatalf("model_invocation.provider_connection_id = %v, want %s", created.ProviderConnectionID, connB.ID)
	}
}

func TestRouting_FallbackChain_OnFailure(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "routing-fallback-success")
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	providerA := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  testutil.MockProviderServer(t, testutil.MockProviderFixture{Handlers: []testutil.MockHandler{{StatusCode: http.StatusInternalServerError}}}),
		ConnectionName:      "fallback-a",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "fallback-a-" + uuid.NewString()[:8],
		ProviderDisplayName: "Fallback A",
	})
	providerB := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  testutil.MockProviderServer(t, testutil.MockProviderFixture{Handlers: []testutil.MockHandler{{StatusCode: http.StatusInternalServerError}}}),
		ConnectionName:      "fallback-b",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "fallback-b-" + uuid.NewString()[:8],
		ProviderDisplayName: "Fallback B",
	})
	providerC := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  testutil.MockProviderServer(t, testutil.MockProviderFixture{Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"ok":true}`}}}),
		ConnectionName:      "fallback-c",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "fallback-c-" + uuid.NewString()[:8],
		ProviderDisplayName: "Fallback C",
	})

	orgID := org.ID
	fallbackB := "profile-b"
	fallbackC := "profile-c"
	mustCreateProfileWithFallback(t, profileRepo, orgID, "profile-a", providerA.Provider.ID, &fallbackB)
	mustCreateProfileWithFallback(t, profileRepo, orgID, "profile-b", providerB.Provider.ID, &fallbackC)
	mustCreateProfileWithFallback(t, profileRepo, orgID, "profile-c", providerC.Provider.ID, nil)

	health := gateway.NewHealthChecker()
	router := gateway.NewRouter(profileRepo, connRepo, health)

	rows, invokeErr := invokeWithFallbackChain(ctx, invokeFallbackOptions{
		OrgID:          org.ID,
		StartProfileID: "profile-a",
		ProfileRepo:    profileRepo,
		InvocationRepo: invocationRepo,
		Router:         router,
		Executor:       providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
	})
	if invokeErr != nil {
		t.Fatalf("invokeWithFallbackChain: %v", invokeErr)
	}
	if len(rows) != 3 {
		t.Fatalf("model_invocation rows = %d, want 3", len(rows))
	}
	if rows[0].Status != "failed" || rows[1].Status != "failed" || rows[2].Status != "completed" {
		t.Fatalf("invocation statuses = [%s %s %s], want [failed failed completed]", rows[0].Status, rows[1].Status, rows[2].Status)
	}
}

func TestRouting_FallbackChain_Exhausted(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "routing-fallback-exhausted")
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	failServer := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{StatusCode: http.StatusInternalServerError},
			{StatusCode: http.StatusInternalServerError},
			{StatusCode: http.StatusInternalServerError},
		},
	})
	providerA := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  failServer,
		ConnectionName:      "exhaust-a",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "exhaust-a-" + uuid.NewString()[:8],
		ProviderDisplayName: "Exhaust A",
	})
	providerB := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  failServer,
		ConnectionName:      "exhaust-b",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "exhaust-b-" + uuid.NewString()[:8],
		ProviderDisplayName: "Exhaust B",
	})
	providerC := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  failServer,
		ConnectionName:      "exhaust-c",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "exhaust-c-" + uuid.NewString()[:8],
		ProviderDisplayName: "Exhaust C",
	})

	orgID := org.ID
	fallbackB := "exhaust-profile-b"
	fallbackC := "exhaust-profile-c"
	mustCreateProfileWithFallback(t, profileRepo, orgID, "exhaust-profile-a", providerA.Provider.ID, &fallbackB)
	mustCreateProfileWithFallback(t, profileRepo, orgID, "exhaust-profile-b", providerB.Provider.ID, &fallbackC)
	mustCreateProfileWithFallback(t, profileRepo, orgID, "exhaust-profile-c", providerC.Provider.ID, nil)

	health := gateway.NewHealthChecker()
	router := gateway.NewRouter(profileRepo, connRepo, health)

	_, invokeErr := invokeWithFallbackChain(ctx, invokeFallbackOptions{
		OrgID:          org.ID,
		StartProfileID: "exhaust-profile-a",
		ProfileRepo:    profileRepo,
		InvocationRepo: invocationRepo,
		Router:         router,
		Executor:       providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
	})
	if invokeErr == nil {
		t.Fatal("expected model.all_providers_failed error")
	}
	if code := modelGatewayErrorCode(invokeErr); code != "model.all_providers_failed" {
		t.Fatalf("error code = %q, want %q", code, "model.all_providers_failed")
	}

	var completed int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM model_invocation
		WHERE organization_id = $1
		  AND status = 'completed'
	`, org.ID).Scan(&completed); err != nil {
		t.Fatalf("count completed invocations: %v", err)
	}
	if completed != 0 {
		t.Fatalf("completed invocations = %d, want 0", completed)
	}
}

func TestRouting_HealthState_DegradedTransition(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "routing-health-transition")
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)

	primaryServer := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{StatusCode: http.StatusTooManyRequests},
			{StatusCode: http.StatusTooManyRequests},
			{StatusCode: http.StatusTooManyRequests},
			{StatusCode: http.StatusOK},
		},
	})
	fixture := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  primaryServer,
		ConnectionName:      "primary",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "transition-a-" + uuid.NewString()[:8],
		ProviderDisplayName: "Transition A",
	})

	fallbackServer := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK}, {StatusCode: http.StatusOK}},
	})
	fallbackURL := strings.TrimSpace(fallbackServer)
	connB, err := connRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         fixture.Provider.ID,
		DisplayName:        "secondary",
		APIKeyRef:          "ref:secondary",
		APIBaseURLOverride: &fallbackURL,
		FailoverPriority:   2,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})
	if err != nil {
		t.Fatalf("create fallback connection: %v", err)
	}

	profile := testutil.MakeModelProfile(t, pool, org.ID, fixture.Provider.ID)
	health := gateway.NewHealthChecker()
	router := gateway.NewRouter(profileRepo, connRepo, health)
	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency: gateway.NewConcurrencyManager(1, map[uuid.UUID]int{
			fixture.Connection.ID: 1,
			connB.ID:              1,
		}),
		Router:       router,
		Executor:     providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
		Health:       health,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	_, _ = queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})

	state := health.GetState(fixture.Connection.ID)
	if state != gateway.HealthStateRateLimited {
		t.Fatalf("health state after 429s = %q, want %q", state, gateway.HealthStateRateLimited)
	}
	if _, err := connRepo.SetHealthStatus(ctx, fixture.Connection.ID, string(state)); err != nil {
		t.Fatalf("SetHealthStatus rate_limited: %v", err)
	}

	response, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("enqueue after rate_limited: %v", err)
	}
	if response.ConnectionID != connB.ID {
		t.Fatalf("selected connection after rate-limit = %s, want %s", response.ConnectionID, connB.ID)
	}

	time.Sleep(2200 * time.Millisecond)
	health.RecordSuccess(fixture.Connection.ID)
	recovered := health.GetState(fixture.Connection.ID)
	if recovered != gateway.HealthStateHealthy {
		t.Fatalf("recovered state = %q, want %q", recovered, gateway.HealthStateHealthy)
	}
	if _, err := connRepo.SetHealthStatus(ctx, fixture.Connection.ID, string(recovered)); err != nil {
		t.Fatalf("SetHealthStatus healthy: %v", err)
	}

	response, err = queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("enqueue after recovery: %v", err)
	}
	if response.ConnectionID != fixture.Connection.ID {
		t.Fatalf("selected connection after recovery = %s, want %s", response.ConnectionID, fixture.Connection.ID)
	}
}

func TestPriorityQueue_PersistsHealthyStatusAfterSuccessfulRecovery(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "queue-persists-recovery")
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)

	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"ok":true}`}},
	})
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  serverURL,
		ConnectionName:      "recovery-connection",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "queue-recovery-" + uuid.NewString()[:8],
		ProviderDisplayName: "Queue Recovery",
	})
	if _, err := connRepo.SetHealthStatus(ctx, provider.Connection.ID, string(gateway.HealthStateUnavailable)); err != nil {
		t.Fatalf("SetHealthStatus unavailable: %v", err)
	}

	profile := testutil.MakeModelProfile(t, pool, org.ID, provider.Provider.ID)
	health := gateway.NewHealthChecker()
	health.MarkDegraded(provider.Connection.ID)

	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency: gateway.NewConcurrencyManager(1, map[uuid.UUID]int{
			provider.Connection.ID: 1,
		}),
		Router:       gateway.NewRouter(profileRepo, connRepo, health),
		Executor:     providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
		Health:       health,
		HealthStore:  connRepo,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	response, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if response.ConnectionID != provider.Connection.ID {
		t.Fatalf("response connection = %s, want %s", response.ConnectionID, provider.Connection.ID)
	}

	stored, err := connRepo.GetByID(ctx, provider.Connection.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.HealthStatus != string(gateway.HealthStateHealthy) {
		t.Fatalf("health_status = %q, want %q", stored.HealthStatus, gateway.HealthStateHealthy)
	}
}

func TestPriorityQueue_SelectsExpiredPersistedUnavailableConnectionOnColdStart(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "queue-cold-start-unavailable-recovery")
	connRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)

	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"ok":true}`}},
	})
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  serverURL,
		ConnectionName:      "cold-start-recovery",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "queue-cold-start-" + uuid.NewString()[:8],
		ProviderDisplayName: "Queue Cold Start Recovery",
	})

	if _, err := connRepo.SetHealthStatus(ctx, provider.Connection.ID, string(gateway.HealthStateUnavailable)); err != nil {
		t.Fatalf("SetHealthStatus unavailable: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE provider_connection
		SET updated_at = $2
		WHERE id = $1
	`, provider.Connection.ID, time.Now().UTC().Add(-2*time.Minute)); err != nil {
		t.Fatalf("age persisted unavailable connection: %v", err)
	}

	profile := testutil.MakeModelProfile(t, pool, org.ID, provider.Provider.ID)
	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency: gateway.NewConcurrencyManager(1, map[uuid.UUID]int{
			provider.Connection.ID: 1,
		}),
		Router:       gateway.NewRouter(profileRepo, connRepo, gateway.NewHealthChecker()),
		Executor:     providerHTTPExecutor{client: &http.Client{Timeout: 2 * time.Second}},
		Health:       gateway.NewHealthChecker(),
		HealthStore:  connRepo,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	response, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "agent_turn",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if response.ConnectionID != provider.Connection.ID {
		t.Fatalf("response connection = %s, want %s", response.ConnectionID, provider.Connection.ID)
	}

	stored, err := connRepo.GetByID(ctx, provider.Connection.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.HealthStatus != string(gateway.HealthStateHealthy) {
		t.Fatalf("health_status = %q, want %q", stored.HealthStatus, gateway.HealthStateHealthy)
	}
}

func TestPriorityQueue_OrderingUnderLoad(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "priority-order")
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://unused.example",
		ConnectionName:      "priority-connection",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "priority-order-" + uuid.NewString()[:8],
		ProviderDisplayName: "Priority Order",
	})
	profile := testutil.MakeModelProfile(t, pool, org.ID, provider.Provider.ID)

	startedHold := make(chan struct{})
	releaseHold := make(chan struct{})
	var (
		mu    sync.Mutex
		order []gateway.PriorityTier
	)

	executor := stubExecutorFunc(func(ctx context.Context, req gateway.GatewayRequest, _ repo.ProviderConnection) (gateway.GatewayResponse, error) {
		if req.InvocationPurpose == "hold" {
			select {
			case <-startedHold:
			default:
				close(startedHold)
			}
			select {
			case <-releaseHold:
			case <-ctx.Done():
				return gateway.GatewayResponse{}, ctx.Err()
			}
			return gateway.GatewayResponse{}, nil
		}
		mu.Lock()
		order = append(order, req.Priority)
		mu.Unlock()
		return gateway.GatewayResponse{}, nil
	})

	health := gateway.NewHealthChecker()
	selected := make(chan gateway.PriorityTier, 4)
	releaseSelect := make(chan struct{})
	router := notifyingRouter{
		inner: gateway.NewRouter(profileRepo, connectionRepo, health),
		onSelect: func(invocationPurpose string, priority gateway.PriorityTier) {
			if invocationPurpose == "hold" {
				return
			}
			selected <- priority
			<-releaseSelect
		},
	}

	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency:  gateway.NewConcurrencyManager(1, map[uuid.UUID]int{provider.Connection.ID: 1}),
		Router:       router,
		Executor:     executor,
		Health:       health,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(ctx, gateway.GatewayRequest{
			OrganizationID:    org.ID,
			ProfileID:         profile.LogicalProfileID,
			InvocationPurpose: "hold",
			Priority:          gateway.PrioritySyncSystem,
		})
	}()
	select {
	case <-startedHold:
	case <-time.After(2 * time.Second):
		t.Fatal("hold request did not start")
	}

	tiers := []gateway.PriorityTier{
		gateway.PriorityAsyncSystem,
		gateway.PriorityAsyncAgent,
		gateway.PrioritySyncSystem,
		gateway.PrioritySyncInteractive,
	}
	done := make(chan error, len(tiers))
	for _, tier := range tiers {
		tier := tier
		go func() {
			_, err := queue.Enqueue(ctx, gateway.GatewayRequest{
				OrganizationID:    org.ID,
				ProfileID:         profile.LogicalProfileID,
				InvocationPurpose: string(tier),
				Priority:          tier,
			})
			done <- err
		}()
	}

	for range tiers {
		<-selected
	}
	close(releaseSelect)
	time.Sleep(10 * time.Millisecond)
	close(releaseHold)
	for range tiers {
		if err := <-done; err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	want := []gateway.PriorityTier{
		gateway.PrioritySyncInteractive,
		gateway.PrioritySyncSystem,
		gateway.PriorityAsyncAgent,
		gateway.PriorityAsyncSystem,
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != len(want) {
		t.Fatalf("execution order size = %d, want %d", len(order), len(want))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, order[i], want[i], order)
		}
	}
}

func TestPriorityQueue_SoftPreemption(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "priority-soft-preemption")
	invocationRepo := repo.NewModelInvocationRepo(pool)

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://unused.example",
		ConnectionName:      "preempt-connection",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "preempt-" + uuid.NewString()[:8],
		ProviderDisplayName: "Preempt Provider",
	})
	profile := testutil.MakeModelProfile(t, pool, org.ID, provider.Provider.ID)

	started := make(chan struct{})
	cancelled := make(chan struct{})

	executor := stubExecutorFunc(func(ctx context.Context, req gateway.GatewayRequest, connection repo.ProviderConnection) (gateway.GatewayResponse, error) {
		if req.InvocationPurpose == "async-long" {
			modelProfileID := profile.LogicalProfileID
			created, err := invocationRepo.Create(context.Background(), repo.ModelInvocation{
				OrganizationID:       org.ID,
				ModelProviderID:      provider.Provider.ID,
				ProviderConnectionID: &connection.ID,
				ModelProfileID:       &modelProfileID,
				InvocationPurpose:    "async_agent",
				Status:               "in_flight",
				ModelName:            profile.ModelName,
			})
			if err != nil {
				return gateway.GatewayResponse{}, err
			}
			select {
			case <-started:
			default:
				close(started)
			}
			<-ctx.Done()
			errCode := "queue.preempted"
			errMsg := "soft preempted"
			if _, err := invocationRepo.UpdateStatus(context.Background(), created.ID, "cancelled", &errCode, &errMsg); err != nil {
				return gateway.GatewayResponse{}, err
			}
			close(cancelled)
			return gateway.GatewayResponse{}, ctx.Err()
		}

		modelProfileID := profile.LogicalProfileID
		_, err := invocationRepo.Create(context.Background(), repo.ModelInvocation{
			OrganizationID:       org.ID,
			ModelProviderID:      provider.Provider.ID,
			ProviderConnectionID: &connection.ID,
			ModelProfileID:       &modelProfileID,
			InvocationPurpose:    "sync_interactive",
			Status:               "completed",
			ModelName:            profile.ModelName,
		})
		if err != nil {
			return gateway.GatewayResponse{}, err
		}
		return gateway.GatewayResponse{}, nil
	})

	queue := mustNewQueue(t, pool, queueFixture{Connection: provider.Connection, Executor: executor, Global: 1})
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(ctx, gateway.GatewayRequest{
			OrganizationID:    org.ID,
			ProfileID:         profile.LogicalProfileID,
			InvocationPurpose: "async-long",
			Priority:          gateway.PriorityAsyncAgent,
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("async request did not start")
	}

	if _, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "sync-now",
		Priority:          gateway.PrioritySyncInteractive,
	}); err != nil {
		t.Fatalf("sync enqueue failed: %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("async invocation was not cancelled by preemption")
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg invocations: %v", err)
	}
	var asyncRow, syncRow *repo.ModelInvocation
	for i := range rows {
		switch rows[i].InvocationPurpose {
		case "async_agent":
			asyncRow = &rows[i]
		case "sync_interactive":
			syncRow = &rows[i]
		}
	}
	if asyncRow == nil || syncRow == nil {
		t.Fatalf("expected async and sync invocation rows, got async=%v sync=%v", asyncRow != nil, syncRow != nil)
	}
	if syncRow.CreatedAt.Before(asyncRow.CreatedAt) {
		t.Fatalf("sync invocation created_at (%s) before async (%s)", syncRow.CreatedAt, asyncRow.CreatedAt)
	}
}

func TestPriorityQueue_Timeout_AsyncSystem(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "priority-timeout")

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://unused.example",
		ConnectionName:      "timeout-connection",
		FailoverPriority:    1,
		MaxConcurrent:       1,
		ProviderSlug:        "timeout-" + uuid.NewString()[:8],
		ProviderDisplayName: "Timeout Provider",
	})
	profile := testutil.MakeModelProfile(t, pool, org.ID, provider.Provider.ID)

	started := make(chan struct{})
	release := make(chan struct{})
	executor := stubExecutorFunc(func(ctx context.Context, req gateway.GatewayRequest, _ repo.ProviderConnection) (gateway.GatewayResponse, error) {
		if req.InvocationPurpose == "hold" {
			select {
			case <-started:
			default:
				close(started)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return gateway.GatewayResponse{}, ctx.Err()
			}
		}
		return gateway.GatewayResponse{}, nil
	})

	queue := mustNewQueueWithTimeouts(t, pool, queueFixture{Connection: provider.Connection, Executor: executor, Global: 1}, map[gateway.PriorityTier]time.Duration{
		gateway.PriorityAsyncSystem: 50 * time.Millisecond,
	})
	defer queue.Close()

	go func() {
		_, _ = queue.Enqueue(ctx, gateway.GatewayRequest{
			OrganizationID:    org.ID,
			ProfileID:         profile.LogicalProfileID,
			InvocationPurpose: "hold",
			Priority:          gateway.PriorityAsyncAgent,
		})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("hold request did not start")
	}

	_, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         profile.LogicalProfileID,
		InvocationPurpose: "will-timeout",
		Priority:          gateway.PriorityAsyncSystem,
	})
	if !errors.Is(err, gateway.ErrQueueTimeout) {
		t.Fatalf("enqueue error = %v, want ErrQueueTimeout", err)
	}

	close(release)
}

func TestProfileScope_FlowNodeOverridesAgent(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "profile-flow-node")

	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := model.NewProfileResolver(assignmentRepo, profileRepo)

	fixture := makeProfileScopeFixture(t, pool, org.ID)
	assignProfile(t, assignmentRepo, org.ID, "organization", org.ID, fixture.orgProfile.LogicalProfileID)
	assignProfile(t, assignmentRepo, org.ID, "project", fixture.projectID, fixture.projectProfile.LogicalProfileID)
	assignProfile(t, assignmentRepo, org.ID, "agent", fixture.agentID, fixture.agentProfile.LogicalProfileID)
	assignProfile(t, assignmentRepo, org.ID, "flow_node", fixture.flowNodeID, fixture.nodeProfile.LogicalProfileID)

	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		model.Scope{Type: "flow_node", ID: fixture.flowNodeID},
		model.Scope{Type: "agent", ID: fixture.agentID},
		model.Scope{Type: "project", ID: fixture.projectID},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != fixture.nodeProfile.LogicalProfileID {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, fixture.nodeProfile.LogicalProfileID)
	}
}

func TestProfileScope_AgentOverridesProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "profile-agent-overrides")

	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := model.NewProfileResolver(assignmentRepo, profileRepo)

	fixture := makeProfileScopeFixture(t, pool, org.ID)
	assignProfile(t, assignmentRepo, org.ID, "organization", org.ID, fixture.orgProfile.LogicalProfileID)
	assignProfile(t, assignmentRepo, org.ID, "project", fixture.projectID, fixture.projectProfile.LogicalProfileID)
	assignProfile(t, assignmentRepo, org.ID, "agent", fixture.agentID, fixture.agentProfile.LogicalProfileID)

	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		model.Scope{Type: "agent", ID: fixture.agentID},
		model.Scope{Type: "project", ID: fixture.projectID},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != fixture.agentProfile.LogicalProfileID {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, fixture.agentProfile.LogicalProfileID)
	}
}

func TestProfileScope_OrgFallback(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "profile-org-fallback")

	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := model.NewProfileResolver(assignmentRepo, profileRepo)

	fixture := makeProfileScopeFixture(t, pool, org.ID)
	assignProfile(t, assignmentRepo, org.ID, "organization", org.ID, fixture.orgProfile.LogicalProfileID)

	resolved, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		model.Scope{Type: "agent", ID: fixture.agentID},
		model.Scope{Type: "project", ID: fixture.projectID},
	)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.LogicalProfileID != fixture.orgProfile.LogicalProfileID {
		t.Fatalf("resolved profile = %q, want %q", resolved.LogicalProfileID, fixture.orgProfile.LogicalProfileID)
	}
}

func TestProfileScope_NoAssignment_Fails(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "profile-no-assignment")

	profileRepo := repo.NewModelProfileRepo(pool)
	assignmentRepo := repo.NewModelProfileAssignmentRepo(pool)
	resolver := model.NewProfileResolver(assignmentRepo, profileRepo)

	_, err := resolver.Resolve(ctx, org.ID, "agent_turn",
		model.Scope{Type: "agent", ID: uuid.New()},
		model.Scope{Type: "project", ID: uuid.New()},
	)
	if err == nil {
		t.Fatal("expected no-assignment error")
	}
	if code := modelGatewayErrorCode(err); code != "model.no_profile_found" {
		t.Fatalf("error code = %q, want %q", code, "model.no_profile_found")
	}
}

type stubExecutorFunc func(ctx context.Context, req gateway.GatewayRequest, connection repo.ProviderConnection) (gateway.GatewayResponse, error)

func (f stubExecutorFunc) Execute(ctx context.Context, req gateway.GatewayRequest, connection repo.ProviderConnection) (gateway.GatewayResponse, error) {
	return f(ctx, req, connection)
}

type queueFixture struct {
	Connection repo.ProviderConnection
	Executor   gateway.GatewayExecutor
	Global     int
}

type notifyingRouter struct {
	inner    gateway.ConnectionSelector
	onSelect func(invocationPurpose string, priority gateway.PriorityTier)
}

func (r notifyingRouter) SelectConnection(ctx context.Context, orgID uuid.UUID, profileID, invocationPurpose string, priority gateway.PriorityTier) (*repo.ProviderConnection, error) {
	conn, err := r.inner.SelectConnection(ctx, orgID, profileID, invocationPurpose, priority)
	if err != nil {
		return nil, err
	}
	if r.onSelect != nil {
		r.onSelect(invocationPurpose, priority)
	}
	return conn, nil
}

func mustNewQueue(t *testing.T, pool *pgxpool.Pool, fixture queueFixture) *gateway.PriorityQueue {
	t.Helper()
	return mustNewQueueWithTimeouts(t, pool, fixture, nil)
}

func mustNewQueueWithTimeouts(t *testing.T, pool *pgxpool.Pool, fixture queueFixture, timeouts map[gateway.PriorityTier]time.Duration) *gateway.PriorityQueue {
	t.Helper()
	if fixture.Executor == nil {
		t.Fatal("queue fixture executor is required")
	}
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	health := gateway.NewHealthChecker()
	router := gateway.NewRouter(profileRepo, connectionRepo, health)
	global := fixture.Global
	if global <= 0 {
		global = 1
	}

	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency:  gateway.NewConcurrencyManager(global, map[uuid.UUID]int{fixture.Connection.ID: 1}),
		Router:       router,
		Executor:     fixture.Executor,
		Health:       health,
		Timeouts:     timeouts,
		PollInterval: 2 * time.Millisecond,
		Sleep:        func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewPriorityQueue: %v", err)
	}
	return queue
}

type invokeFallbackOptions struct {
	OrgID          uuid.UUID
	StartProfileID string
	ProfileRepo    *repo.ModelProfileRepo
	InvocationRepo *repo.ModelInvocationRepo
	Router         *gateway.Router
	Executor       gateway.GatewayExecutor
}

func invokeWithFallbackChain(ctx context.Context, opts invokeFallbackOptions) ([]repo.ModelInvocation, error) {
	current := strings.TrimSpace(opts.StartProfileID)
	if current == "" {
		return nil, codedError{Code: "model.no_profile_found", Err: errors.New("missing start profile")}
	}
	rows := make([]repo.ModelInvocation, 0, 3)
	var previous *uuid.UUID

	for hop := 0; hop < 3 && current != ""; hop++ {
		profile, err := opts.ProfileRepo.GetCurrentByLogicalID(ctx, opts.OrgID, current)
		if err != nil {
			return rows, err
		}
		connection, err := opts.Router.SelectConnection(ctx, opts.OrgID, current, "agent_turn", gateway.PrioritySyncInteractive)
		if err != nil {
			return rows, err
		}

		modelProfileID := profile.LogicalProfileID
		created, err := opts.InvocationRepo.Create(ctx, repo.ModelInvocation{
			OrganizationID:           opts.OrgID,
			ModelProviderID:          profile.ProviderID,
			ProviderConnectionID:     &connection.ID,
			ModelProfileID:           &modelProfileID,
			InvocationPurpose:        "agent_turn",
			Status:                   "in_flight",
			ModelName:                profile.ModelName,
			FallbackFromInvocationID: previous,
		})
		if err != nil {
			return rows, err
		}

		_, execErr := opts.Executor.Execute(ctx, gateway.GatewayRequest{
			OrganizationID:    opts.OrgID,
			ProfileID:         current,
			InvocationPurpose: "agent_turn",
			Priority:          gateway.PrioritySyncInteractive,
		}, *connection)
		if execErr == nil {
			if err := opts.InvocationRepo.UpdateCompletion(ctx, created.ID, 11, 7, 0, 1, 1, nil, nil); err != nil {
				return rows, err
			}
			updated, err := opts.InvocationRepo.GetByID(ctx, created.ID)
			if err != nil {
				return rows, err
			}
			rows = append(rows, updated)
			return rows, nil
		}

		code := "provider.http_error"
		msg := execErr.Error()
		failed, err := opts.InvocationRepo.UpdateStatus(ctx, created.ID, "failed", &code, &msg)
		if err != nil {
			return rows, err
		}
		rows = append(rows, failed)
		previous = &created.ID

		if profile.FallbackProfileID == nil || strings.TrimSpace(*profile.FallbackProfileID) == "" {
			break
		}
		current = strings.TrimSpace(*profile.FallbackProfileID)
	}

	return rows, codedError{Code: "model.all_providers_failed", Err: errors.New("all fallback providers failed")}
}

func modelGatewayErrorCode(err error) string {
	var coded codedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	switch {
	case errors.Is(err, model.ErrNoProfileAssigned):
		return "model.no_profile_found"
	case errors.Is(err, gateway.ErrNoHealthyConnection):
		return "model.all_providers_failed"
	default:
		return ""
	}
}

type profileScopeFixture struct {
	orgProfile     repo.ModelProfile
	projectProfile repo.ModelProfile
	agentProfile   repo.ModelProfile
	nodeProfile    repo.ModelProfile
	projectID      uuid.UUID
	agentID        uuid.UUID
	flowNodeID     uuid.UUID
}

func makeProfileScopeFixture(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) profileScopeFixture {
	t.Helper()
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      orgID,
		ProviderAPIBaseURL:  "https://profile-scope.example",
		ConnectionName:      "profile-scope",
		ProviderSlug:        "scope-" + uuid.NewString()[:8],
		ProviderDisplayName: "Scope Provider",
	})
	profileRepo := repo.NewModelProfileRepo(pool)
	orgIDValue := orgID

	create := func(logical, modelName string) repo.ModelProfile {
		item, err := profileRepo.Create(context.Background(), repo.ModelProfile{
			LogicalProfileID:    logical,
			OrganizationID:      &orgIDValue,
			Version:             1,
			IsCurrent:           true,
			ProviderID:          provider.Provider.ID,
			ModelName:           modelName,
			DisplayName:         logical,
			ContextWindowTokens: 8192,
			MaxOutputTokens:     1024,
			SupportsStreaming:   true,
			InvocationPurpose:   "agent_turn",
		})
		if err != nil {
			t.Fatalf("create profile %s: %v", logical, err)
		}
		return item
	}

	return profileScopeFixture{
		orgProfile:     create("org-profile-"+uuid.NewString()[:8], "org-model"),
		projectProfile: create("project-profile-"+uuid.NewString()[:8], "project-model"),
		agentProfile:   create("agent-profile-"+uuid.NewString()[:8], "agent-model"),
		nodeProfile:    create("node-profile-"+uuid.NewString()[:8], "node-model"),
		projectID:      uuid.New(),
		agentID:        uuid.New(),
		flowNodeID:     uuid.New(),
	}
}

func assignProfile(t *testing.T, assignmentRepo *repo.ModelProfileAssignmentRepo, orgID uuid.UUID, scopeType string, scopeID uuid.UUID, logicalProfileID string) {
	t.Helper()
	if _, err := assignmentRepo.Upsert(context.Background(), repo.ModelProfileAssignment{
		OrganizationID:    orgID,
		ScopeType:         scopeType,
		ScopeID:           scopeID,
		LogicalProfileID:  logicalProfileID,
		InvocationPurpose: "agent_turn",
	}); err != nil {
		t.Fatalf("upsert assignment %s: %v", scopeType, err)
	}
}

func mustCreateProfileWithFallback(t *testing.T, profileRepo *repo.ModelProfileRepo, orgID uuid.UUID, logicalID string, providerID uuid.UUID, fallback *string) {
	t.Helper()
	org := orgID
	if _, err := profileRepo.Create(context.Background(), repo.ModelProfile{
		LogicalProfileID:    logicalID,
		OrganizationID:      &org,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           logicalID + "-model",
		DisplayName:         logicalID,
		ContextWindowTokens: 8192,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		InvocationPurpose:   "agent_turn",
		FallbackProfileID:   fallback,
	}); err != nil {
		t.Fatalf("create profile %s: %v", logicalID, err)
	}
}

func mustCreateOrg(t *testing.T, pool *pgxpool.Pool, slug string) repo.Organization {
	t.Helper()
	created, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        slug + "-" + uuid.NewString()[:8],
		DisplayName: "Model Gateway " + slug,
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return created
}
