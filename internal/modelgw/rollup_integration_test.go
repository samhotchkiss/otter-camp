//go:build integration

package modelgw

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/gateway"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
)

func TestTokenRollup_InvocationRecord(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "rollup-invocation-record")
	mockURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusOK,
			Body:       `{"metadata":{"input_tokens":100,"output_tokens":50}}`,
		}},
	})
	inputTokens, outputTokens := mustFetchUsageFromMock(t, ctx, mockURL)

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  mockURL,
		ConnectionName:      "invocation-record",
		ProviderSlug:        "invocation-record-" + uuid.NewString()[:8],
		ProviderDisplayName: "Invocation Record Provider",
	})

	runRepo := controlplane.NewRunRepository(pool)
	stepRepo := controlplane.NewRunStepRepository(pool)
	attemptRepo := controlplane.NewRunAttemptRepository(pool)

	runRecord, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: org.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := stepRepo.Create(ctx, controlplane.RunStep{RunID: runRecord.ID, StepNumber: 1, Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run_step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, controlplane.RunAttempt{RunStepID: step.ID, AttemptNumber: 1, Trigger: "initial", Status: "pending", Metadata: []byte(`{}`)})
	if err != nil {
		t.Fatalf("create run_attempt: %v", err)
	}

	recorder, err := model.NewInvocationRecorder(model.InvocationRecorderOptions{
		Invocations:  repo.NewModelInvocationRepo(pool),
		Attribution:  model.NewAttributionMiddleware(),
		Rollup:       model.NewRollupUpdater(pool),
		AsyncSpawner: func(fn func()) { fn() },
	})
	if err != nil {
		t.Fatalf("new invocation recorder: %v", err)
	}

	runCtx := model.WithInvocationContext(ctx, model.InvocationContext{
		OrganizationID:    org.ID,
		RunID:             &runRecord.ID,
		RunStepID:         &step.ID,
		RunAttemptID:      &attempt.ID,
		InvocationPurpose: "agent_turn",
	})

	created, err := recorder.Create(runCtx, model.ModelInvocationInput{
		ModelProviderID:      provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ModelName:            "gpt-4o-mini",
		Status:               "completed",
		InputTokens:          &inputTokens,
		OutputTokens:         &outputTokens,
	})
	if err != nil {
		t.Fatalf("create invocation: %v", err)
	}

	if created.InputTokens == nil || *created.InputTokens != 100 {
		t.Fatalf("input_tokens = %v, want 100", created.InputTokens)
	}
	if created.OutputTokens == nil || *created.OutputTokens != 50 {
		t.Fatalf("output_tokens = %v, want 50", created.OutputTokens)
	}
	if got := *created.InputTokens + *created.OutputTokens; got != 150 {
		t.Fatalf("total_tokens = %d, want 150", got)
	}

	updatedAttempt, err := attemptRepo.Get(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get run_attempt: %v", err)
	}
	if updatedAttempt.InputTokens != 100 || updatedAttempt.OutputTokens != 50 {
		t.Fatalf("run_attempt tokens = (%d,%d), want (100,50)", updatedAttempt.InputTokens, updatedAttempt.OutputTokens)
	}
}

func TestTokenRollup_DailyAggregation(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "rollup-daily")
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://rollup.example",
		ConnectionName:      "daily-rollup",
		ProviderSlug:        "daily-rollup-" + uuid.NewString()[:8],
		ProviderDisplayName: "Daily Rollup Provider",
	})

	day := time.Now().UTC().Truncate(24 * time.Hour)
	pairs := [][2]int{{10, 5}, {20, 10}, {30, 15}, {40, 20}, {50, 25}}
	for i, pair := range pairs {
		insertCompletedInvocation(t, pool, invocationInsert{
			OrganizationID:       org.ID,
			ProviderID:           provider.Provider.ID,
			ProviderConnectionID: &provider.Connection.ID,
			ModelName:            "gpt-4o-mini",
			InvocationPurpose:    "agent_turn",
			InputTokens:          pair[0],
			OutputTokens:         pair[1],
			CreatedAt:            day.Add(time.Duration(i+1) * time.Hour),
		})
	}

	worker := gateway.NewRollupWorker(pool, repo.NewModelUsageRollupRepo(pool), nil)
	if err := worker.RunRollupForDate(ctx, org.ID, day); err != nil {
		t.Fatalf("RunRollupForDate: %v", err)
	}

	var invocations int
	var inputTotal, outputTotal int64
	if err := pool.QueryRow(ctx, `
		SELECT total_invocations, total_input_tokens, total_output_tokens
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_date = $2
		  AND rollup_type = 'model_provider'
		  AND rollup_id IS NULL
	`, org.ID, day).Scan(&invocations, &inputTotal, &outputTotal); err != nil {
		t.Fatalf("query daily aggregate: %v", err)
	}
	if invocations != 5 {
		t.Fatalf("total_invocations = %d, want 5", invocations)
	}
	if got := inputTotal + outputTotal; got != 225 {
		t.Fatalf("total_tokens = %d, want 225", got)
	}
}

func TestTokenRollup_GroupByProvider(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "rollup-provider-group")

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://provider-group.example",
		ConnectionName:      "provider-primary",
		ProviderSlug:        "provider-group-" + uuid.NewString()[:8],
		ProviderDisplayName: "Provider Group",
	})
	connRepo := repo.NewProviderConnectionRepo(pool)
	secondaryURL := "https://provider-secondary.example"
	secondary, err := connRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.Provider.ID,
		DisplayName:        "provider-secondary",
		APIKeyRef:          "ref:provider-secondary",
		APIBaseURLOverride: &secondaryURL,
		FailoverPriority:   2,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})
	if err != nil {
		t.Fatalf("create secondary connection: %v", err)
	}

	day := time.Now().UTC().Truncate(24 * time.Hour)
	insertCompletedInvocation(t, pool, invocationInsert{
		OrganizationID:       org.ID,
		ProviderID:           provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ModelName:            "gpt-4o-mini",
		InvocationPurpose:    "agent_turn",
		InputTokens:          30,
		OutputTokens:         10,
		CreatedAt:            day.Add(1 * time.Hour),
	})
	insertCompletedInvocation(t, pool, invocationInsert{
		OrganizationID:       org.ID,
		ProviderID:           provider.Provider.ID,
		ProviderConnectionID: &secondary.ID,
		ModelName:            "gpt-4o-mini",
		InvocationPurpose:    "agent_turn",
		InputTokens:          20,
		OutputTokens:         5,
		CreatedAt:            day.Add(2 * time.Hour),
	})

	worker := gateway.NewRollupWorker(pool, repo.NewModelUsageRollupRepo(pool), nil)
	if err := worker.RunRollupForDate(ctx, org.ID, day); err != nil {
		t.Fatalf("RunRollupForDate: %v", err)
	}

	var rowCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_date = $2
		  AND rollup_type = 'provider_connection'
	`, org.ID, day).Scan(&rowCount); err != nil {
		t.Fatalf("count provider rows: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("provider rollup rows = %d, want 2", rowCount)
	}

	testServer := newUsageTestServer(t, pool, org.ID)
	resp, err := http.Get(testServer.URL + "/v1/usage?group_by=provider_connection&period=today")
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("usage status = %d, want 200 body=%s", resp.StatusCode, string(body))
	}
	var payload struct {
		Data struct {
			Data []any `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode usage response: %v", err)
	}
	if len(payload.Data.Data) != 2 {
		t.Fatalf("usage provider groups = %d, want 2", len(payload.Data.Data))
	}
}

func TestTokenRollup_GroupByProject(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	org := mustCreateOrg(t, pool, "rollup-project-group")

	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  "https://project-group.example",
		ConnectionName:      "project-group-conn",
		ProviderSlug:        "project-group-" + uuid.NewString()[:8],
		ProviderDisplayName: "Project Group Provider",
	})

	projectRepo := repo.NewProjectRepo(pool)
	projectA := mustCreateProject(t, projectRepo, org.ID, "rollup-a")
	projectB := mustCreateProject(t, projectRepo, org.ID, "rollup-b")
	projectC := mustCreateProject(t, projectRepo, org.ID, "rollup-c")

	day := time.Now().UTC().Truncate(24 * time.Hour)
	insertCompletedInvocation(t, pool, invocationInsert{
		OrganizationID:       org.ID,
		ProviderID:           provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ProjectID:            &projectA.ID,
		ModelName:            "gpt-4o-mini",
		InvocationPurpose:    "agent_turn",
		InputTokens:          10,
		OutputTokens:         2,
		CreatedAt:            day.Add(1 * time.Hour),
	})
	insertCompletedInvocation(t, pool, invocationInsert{
		OrganizationID:       org.ID,
		ProviderID:           provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ProjectID:            &projectB.ID,
		ModelName:            "gpt-4o-mini",
		InvocationPurpose:    "agent_turn",
		InputTokens:          20,
		OutputTokens:         4,
		CreatedAt:            day.Add(2 * time.Hour),
	})
	insertCompletedInvocation(t, pool, invocationInsert{
		OrganizationID:       org.ID,
		ProviderID:           provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ProjectID:            &projectC.ID,
		ModelName:            "gpt-4o-mini",
		InvocationPurpose:    "agent_turn",
		InputTokens:          30,
		OutputTokens:         6,
		CreatedAt:            day.Add(3 * time.Hour),
	})

	worker := gateway.NewRollupWorker(pool, repo.NewModelUsageRollupRepo(pool), nil)
	if err := worker.RunRollupForDate(ctx, org.ID, day); err != nil {
		t.Fatalf("RunRollupForDate: %v", err)
	}

	var rowCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM model_usage_rollup
		WHERE organization_id = $1
		  AND rollup_date = $2
		  AND rollup_type = 'project'
	`, org.ID, day).Scan(&rowCount); err != nil {
		t.Fatalf("count project rows: %v", err)
	}
	if rowCount != 3 {
		t.Fatalf("project rollup rows = %d, want 3", rowCount)
	}

	testServer := newUsageTestServer(t, pool, org.ID)
	resp, err := http.Get(testServer.URL + "/v1/usage?group_by=project&period=today")
	if err != nil {
		t.Fatalf("GET /v1/usage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("usage status = %d, want 200 body=%s", resp.StatusCode, string(body))
	}
	var payload struct {
		Data struct {
			Data []any `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode usage response: %v", err)
	}
	if len(payload.Data.Data) != 3 {
		t.Fatalf("usage project groups = %d, want 3", len(payload.Data.Data))
	}
}

func TestPromptCapture_ToObjectStorage(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrgWithSettings(t, pool, "capture-enabled", repo.OrganizationSettings{
		ModelCapture: repo.OrganizationModelCaptureSettings{
			CapturePrompts:   true,
			CaptureResponses: true,
		},
	})
	mockURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"metadata":{"input_tokens":10,"output_tokens":5}}`}},
	})
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  mockURL,
		ConnectionName:      "capture-enabled-conn",
		ProviderSlug:        "capture-enabled-" + uuid.NewString()[:8],
		ProviderDisplayName: "Capture Enabled Provider",
	})

	invocationRepo := repo.NewModelInvocationRepo(pool)
	modelProfileID := "capture-profile"
	invocation, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ModelProfileID:       &modelProfileID,
		InvocationPurpose:    "agent_turn",
		Status:               "in_flight",
		ModelName:            "gpt-4o-mini",
		Metadata:             []byte(`{"prompt":"hello"}`),
	})
	if err != nil {
		t.Fatalf("create model_invocation: %v", err)
	}

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewFS: %v", err)
	}
	capture := gateway.NewCaptureService(repo.NewOrgRepo(pool), store, nil)
	processor, err := gateway.NewStreamProcessor(gateway.StreamProcessorOptions{
		Invocations: invocationRepo,
		Providers:   repo.NewModelProviderRepo(pool),
		Awaiter:     mockAwaiter{BaseURL: mockURL, Client: &http.Client{Timeout: 2 * time.Second}},
		Capture:     capture,
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	if _, err := processor.AwaitResponse(ctx, invocation.ID); err != nil {
		t.Fatalf("AwaitResponse: %v", err)
	}
	stored, err := invocationRepo.GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.PromptStorageKey == nil || stored.ResponseStorageKey == nil {
		t.Fatalf("capture keys = (%v,%v), want both non-nil", stored.PromptStorageKey, stored.ResponseStorageKey)
	}
	if _, err := store.Get(ctx, *stored.PromptStorageKey); err != nil {
		t.Fatalf("Get(prompt): %v", err)
	}
	if _, err := store.Get(ctx, *stored.ResponseStorageKey); err != nil {
		t.Fatalf("Get(response): %v", err)
	}
}

func TestPromptCapture_RedactionPolicy(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrgWithSettings(t, pool, "capture-redacted", repo.OrganizationSettings{
		Redaction: repo.OrganizationRedactionSettings{Enabled: true, Policy: "strict"},
		ModelCapture: repo.OrganizationModelCaptureSettings{
			CapturePrompts:   false,
			CaptureResponses: false,
		},
	})
	mockURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK, Body: `{"metadata":{"input_tokens":10,"output_tokens":5}}`}},
	})
	provider := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  mockURL,
		ConnectionName:      "capture-redacted-conn",
		ProviderSlug:        "capture-redacted-" + uuid.NewString()[:8],
		ProviderDisplayName: "Capture Redacted Provider",
	})

	invocationRepo := repo.NewModelInvocationRepo(pool)
	modelProfileID := "capture-profile"
	invocation, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.Provider.ID,
		ProviderConnectionID: &provider.Connection.ID,
		ModelProfileID:       &modelProfileID,
		InvocationPurpose:    "agent_turn",
		Status:               "in_flight",
		ModelName:            "gpt-4o-mini",
		Metadata:             []byte(`{"prompt":"secret"}`),
	})
	if err != nil {
		t.Fatalf("create model_invocation: %v", err)
	}

	store, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewFS: %v", err)
	}
	capture := gateway.NewCaptureService(repo.NewOrgRepo(pool), store, nil)
	processor, err := gateway.NewStreamProcessor(gateway.StreamProcessorOptions{
		Invocations: invocationRepo,
		Providers:   repo.NewModelProviderRepo(pool),
		Awaiter:     mockAwaiter{BaseURL: mockURL, Client: &http.Client{Timeout: 2 * time.Second}},
		Capture:     capture,
	})
	if err != nil {
		t.Fatalf("NewStreamProcessor: %v", err)
	}

	if _, err := processor.AwaitResponse(ctx, invocation.ID); err != nil {
		t.Fatalf("AwaitResponse: %v", err)
	}
	stored, err := invocationRepo.GetByID(ctx, invocation.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.PromptStorageKey != nil || stored.ResponseStorageKey != nil {
		t.Fatalf("capture keys = (%v,%v), want both nil", stored.PromptStorageKey, stored.ResponseStorageKey)
	}
}

func TestInvocationPurpose_Routing(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, pool, "purpose-routing")
	profileRepo := repo.NewModelProfileRepo(pool)
	connRepo := repo.NewProviderConnectionRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)

	high := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  testutil.MockProviderServer(t, testutil.MockProviderFixture{Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK}}}),
		ConnectionName:      "high-capability",
		ProviderSlug:        "purpose-high-" + uuid.NewString()[:8],
		ProviderDisplayName: "High Capability Provider",
		FailoverPriority:    1,
	})
	haiku := testutil.MakeProvider(t, pool, testutil.MakeProviderOptions{
		OrganizationID:      org.ID,
		ProviderAPIBaseURL:  testutil.MockProviderServer(t, testutil.MockProviderFixture{Handlers: []testutil.MockHandler{{StatusCode: http.StatusOK}}}),
		ConnectionName:      "haiku",
		ProviderSlug:        "purpose-haiku-" + uuid.NewString()[:8],
		ProviderDisplayName: "Haiku Provider",
		FailoverPriority:    1,
	})

	orgID := org.ID
	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          high.Provider.ID,
		ModelName:           "gpt-4o",
		DisplayName:         "High Capability",
		ContextWindowTokens: 8192,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		InvocationPurpose:   "agent_turn",
	}); err != nil {
		t.Fatalf("create high profile: %v", err)
	}
	if _, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "haiku",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          haiku.Provider.ID,
		ModelName:           "claude-3-haiku",
		DisplayName:         "Haiku",
		ContextWindowTokens: 8192,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		InvocationPurpose:   "summarization",
	}); err != nil {
		t.Fatalf("create haiku profile: %v", err)
	}

	health := gateway.NewHealthChecker()
	router := gateway.NewRouter(profileRepo, connRepo, health)
	queue, err := gateway.NewPriorityQueue(gateway.QueueOptions{
		Concurrency: gateway.NewConcurrencyManager(1, map[uuid.UUID]int{
			high.Connection.ID:  1,
			haiku.Connection.ID: 1,
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

	response, err := queue.Enqueue(ctx, gateway.GatewayRequest{
		OrganizationID:    org.ID,
		ProfileID:         "high-capability",
		InvocationPurpose: "summarization",
		Priority:          gateway.PrioritySyncInteractive,
	})
	if err != nil {
		t.Fatalf("enqueue summarization: %v", err)
	}
	if response.ConnectionID != haiku.Connection.ID {
		t.Fatalf("selected connection = %s, want haiku connection %s", response.ConnectionID, haiku.Connection.ID)
	}

	modelProfileID := "haiku"
	created, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      haiku.Provider.ID,
		ProviderConnectionID: &response.ConnectionID,
		ModelProfileID:       &modelProfileID,
		InvocationPurpose:    "summarization",
		Status:               "completed",
		ModelName:            "claude-3-haiku",
	})
	if err != nil {
		t.Fatalf("create model_invocation: %v", err)
	}
	if created.InvocationPurpose != "summarization" {
		t.Fatalf("invocation_purpose = %q, want summarization", created.InvocationPurpose)
	}
}

type mockAwaiter struct {
	BaseURL string
	Client  *http.Client
}

func (a mockAwaiter) Await(ctx context.Context, _ uuid.UUID) ([]byte, error) {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/v1/messages", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mock provider status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

func mustFetchUsageFromMock(t *testing.T, ctx context.Context, baseURL string) (int, int) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("mock provider call: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("mock status = %d, want 200 body=%s", resp.StatusCode, string(body))
	}

	var payload struct {
		Metadata struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode mock payload: %v", err)
	}
	return payload.Metadata.InputTokens, payload.Metadata.OutputTokens
}

type invocationInsert struct {
	OrganizationID       uuid.UUID
	ProviderID           uuid.UUID
	ProviderConnectionID *uuid.UUID
	ProjectID            *uuid.UUID
	ModelName            string
	InvocationPurpose    string
	InputTokens          int
	OutputTokens         int
	CreatedAt            time.Time
}

func insertCompletedInvocation(t *testing.T, pool *pgxpool.Pool, in invocationInsert) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO model_invocation (
			organization_id,
			model_provider_id,
			provider_connection_id,
			project_id,
			invocation_purpose,
			status,
			model_name,
			input_tokens,
			output_tokens,
			created_at
		)
		VALUES ($1, $2, $3, $4, $5, 'completed', $6, $7, $8, $9)
	`, in.OrganizationID, in.ProviderID, in.ProviderConnectionID, in.ProjectID, in.InvocationPurpose, in.ModelName, in.InputTokens, in.OutputTokens, in.CreatedAt.UTC()); err != nil {
		t.Fatalf("insert model_invocation: %v", err)
	}
}

func newUsageTestServer(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := middleware.WithPrincipal(req.Context(), middleware.Principal{
				UserID:         uuid.New(),
				OrganizationID: orgID,
				Role:           "admin",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/v1", func(v1 chi.Router) {
		server.NewModelRouteRegistrar(pool).RegisterRoutes(v1)
	})
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

func mustCreateProject(t *testing.T, projectRepo *repo.ProjectRepo, orgID uuid.UUID, slugPrefix string) repo.Project {
	t.Helper()
	project, err := projectRepo.Create(context.Background(), repo.Project{
		OrganizationID: orgID,
		Slug:           slugPrefix + "-" + uuid.NewString()[:8],
		DisplayName:    "Project " + slugPrefix,
		Description:    "",
		DeliveryMode:   "gated",
		Settings:       []byte(`{}`),
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func mustCreateOrgWithSettings(t *testing.T, pool *pgxpool.Pool, slug string, settings repo.OrganizationSettings) repo.Organization {
	t.Helper()
	created, err := repo.NewOrgRepo(pool).Create(context.Background(), repo.Organization{
		Slug:        slug + "-" + uuid.NewString()[:8],
		DisplayName: "Model Gateway " + slug,
		Settings:    settings,
	})
	if err != nil {
		t.Fatalf("create org with settings: %v", err)
	}
	return created
}
