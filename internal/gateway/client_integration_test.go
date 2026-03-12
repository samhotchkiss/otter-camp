//go:build integration

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

func TestLiveModelGatewayStreamCompleteOpenAIUpdatesInvocation(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-stream-org",
		DisplayName: "Live Gateway Stream Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "openai-live", "OpenAI Live Key", "", "sk-live-test", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello "}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"world"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":2}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{
				StatusCode: http.StatusOK,
				Headers: map[string]string{
					"Content-Type": "text/event-stream",
				},
				Body: sseBody,
			},
		},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Primary",
		APIKeyRef:          "ref:openai-live",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "High Capability",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	chunks := make([]string, 0, 2)
	response, err := gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Say hello"},
			},
			TotalTokens: 11,
		},
	}, func(token string) error {
		chunks = append(chunks, token)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if response.Content != "Hello world" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "Hello world")
	}
	if got := strings.Join(chunks, ""); got != "Hello world" {
		t.Fatalf("streamed chunks = %q, want %q", got, "Hello world")
	}
	if response.Usage == nil {
		t.Fatal("response.Usage is nil")
	}
	if response.Usage.InputTokens != 11 {
		t.Fatalf("usage.input_tokens = %d, want 11", response.Usage.InputTokens)
	}
	if response.Usage.OutputTokens != 2 {
		t.Fatalf("usage.output_tokens = %d, want 2", response.Usage.OutputTokens)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	created := rows[0]
	if created.ProviderConnectionID == nil || *created.ProviderConnectionID != connection.ID {
		t.Fatalf("provider_connection_id = %v, want %s", created.ProviderConnectionID, connection.ID)
	}
	if created.Status != "completed" {
		t.Fatalf("status = %q, want %q", created.Status, "completed")
	}
	if created.InputTokens == nil || *created.InputTokens != 11 {
		t.Fatalf("input_tokens = %v, want 11", created.InputTokens)
	}
	if created.OutputTokens == nil || *created.OutputTokens != 2 {
		t.Fatalf("output_tokens = %v, want 2", created.OutputTokens)
	}
}

func TestLiveModelGatewayStreamCompleteReusesPrecreatedInvocation(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-precreated-invocation-org",
		DisplayName: "Live Gateway Precreated Invocation Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "openai-live-precreated", "OpenAI Live Key", "", "sk-live-test", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello "}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"again"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{
				StatusCode: http.StatusOK,
				Headers: map[string]string{
					"Content-Type": "text/event-stream",
				},
				Body: sseBody,
			},
		},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Primary",
		APIKeyRef:          "ref:openai-live-precreated",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "High Capability",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	precreated, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:       org.ID,
		ModelProviderID:      provider.ID,
		ProviderConnectionID: &connection.ID,
		ModelProfileID:       stringPtr(profile.LogicalProfileID),
		InvocationPurpose:    "agent_turn",
		Status:               "in_flight",
		ModelName:            profile.ModelName,
		IsStreaming:          true,
	})
	if err != nil {
		t.Fatalf("precreate invocation: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	invocationID := precreated.ID
	response, err := gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		InvocationID:   &invocationID,
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Say hello again"},
			},
			TotalTokens: 12,
		},
	}, nil)
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if response.Content != "Hello again" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "Hello again")
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	updated := rows[0]
	if updated.ID != precreated.ID {
		t.Fatalf("invocation id = %s, want %s", updated.ID, precreated.ID)
	}
	if updated.Status != "completed" {
		t.Fatalf("status = %q, want completed", updated.Status)
	}
	if updated.InputTokens == nil || *updated.InputTokens != 12 {
		t.Fatalf("input_tokens = %v, want 12", updated.InputTokens)
	}
	if updated.OutputTokens == nil || *updated.OutputTokens != 3 {
		t.Fatalf("output_tokens = %v, want 3", updated.OutputTokens)
	}
}

func TestLiveModelGatewayStreamCompleteBindsRoutingOnPrecreatedInvocationWithoutConnection(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-precreated-routing-org",
		DisplayName: "Live Gateway Precreated Routing Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "gateway-key", "Gateway Key", "", "sk-live-test", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello "}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"routed"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":2}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Content-Type": "text/event-stream",
			},
			Body: sseBody,
		}},
	})

	provider, err := providerRepo.Create(ctx, repo.ModelProvider{
		Slug:        "openai",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	connection, err := connectionRepo.Create(ctx, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Primary",
		APIKeyRef:          "ref:gateway-key",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Standard",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	precreated, err := invocationRepo.Create(ctx, repo.ModelInvocation{
		OrganizationID:    org.ID,
		ModelProviderID:   provider.ID,
		ModelProfileID:    stringPtr(profile.LogicalProfileID),
		InvocationPurpose: "agent_turn",
		Status:            "in_flight",
		ModelName:         profile.ModelName,
		IsStreaming:       true,
	})
	if err != nil {
		t.Fatalf("precreate invocation: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	invocationID := precreated.ID
	response, err := gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		InvocationID:   &invocationID,
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "Say hello with routing"},
			},
			TotalTokens: 9,
		},
	}, nil)
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if response.Content != "Hello routed" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "Hello routed")
	}

	stored, err := invocationRepo.GetByID(ctx, precreated.ID)
	if err != nil {
		t.Fatalf("GetByID invocation: %v", err)
	}
	if stored.ProviderConnectionID == nil || *stored.ProviderConnectionID != connection.ID {
		t.Fatalf("provider_connection_id = %v, want %s", stored.ProviderConnectionID, connection.ID)
	}
	if stored.ModelProviderID != provider.ID {
		t.Fatalf("model_provider_id = %s, want %s", stored.ModelProviderID, provider.ID)
	}
	if stored.Status != "completed" {
		t.Fatalf("status = %q, want completed", stored.Status)
	}
}

func TestLiveModelGatewayMapsAuthErrors(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-auth-org",
		DisplayName: "Live Gateway Auth Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "auth-key", "Auth Key", "", "sk-auth", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{
			{
				StatusCode: http.StatusUnauthorized,
				Body:       `{"error":"invalid_api_key"}`,
			},
		},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Primary",
		APIKeyRef:          "ref:auth-key",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "High Capability",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	_, err = gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		HumanMessages:  []string{"hello"},
	}, nil)
	if !errors.Is(err, turn.ErrAuthFailed) {
		t.Fatalf("err = %v, want turn.ErrAuthFailed", err)
	}
	if state := health.GetState(connection.ID); state != HealthStateUnavailable {
		t.Fatalf("connection health = %q, want %q", state, HealthStateUnavailable)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	if rows[0].FailureClass == nil || *rows[0].FailureClass != "provider_auth" {
		t.Fatalf("failure_class = %v, want provider_auth", rows[0].FailureClass)
	}
}

func TestLiveModelGatewayFailsOverAfterAuthFailure(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-auth-failover-org",
		DisplayName: "Live Gateway Auth Failover Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "auth-key-primary", "Auth Key Primary", "", "sk-primary", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set primary secret: %v", err)
	}
	if err := secretSvc.Set(ctx, org.ID, "auth-key-secondary", "Auth Key Secondary", "", "sk-secondary", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secondary secret: %v", err)
	}

	authFailServer := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusUnauthorized,
			Body:       `{"error":"invalid_api_key"}`,
		}},
	})
	successServer := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Content-Type": "text/event-stream",
			},
			Body: strings.Join([]string{
				`data: {"choices":[{"delta":{"content":"Recovered "}}]}`,
				"",
				`data: {"choices":[{"delta":{"content":"response"}}]}`,
				"",
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n"),
		}},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai-auth-failover",
		DisplayName: "OpenAI",
		APIBaseURL:  authFailServer,
		IsEnabled:   true,
	})
	primary := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Primary",
		APIKeyRef:          "ref:auth-key-primary",
		APIBaseURLOverride: &authFailServer,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})
	secondary := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Secondary",
		APIKeyRef:          "ref:auth-key-secondary",
		APIBaseURLOverride: &successServer,
		FailoverPriority:   2,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "high-capability",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "High Capability",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	response, err := gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		HumanMessages:  []string{"hello"},
	}, nil)
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if response.Content != "Recovered response" {
		t.Fatalf("response.Content = %q, want %q", response.Content, "Recovered response")
	}
	if state := health.GetState(primary.ID); state != HealthStateUnavailable {
		t.Fatalf("primary connection health = %q, want %q", state, HealthStateUnavailable)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("invocation rows = %d, want 2", len(rows))
	}
	if rows[0].ProviderConnectionID == nil || *rows[0].ProviderConnectionID != secondary.ID {
		t.Fatalf("latest provider_connection_id = %v, want %s", rows[0].ProviderConnectionID, secondary.ID)
	}
	if rows[0].Status != "completed" {
		t.Fatalf("latest status = %q, want completed", rows[0].Status)
	}
	if rows[1].ProviderConnectionID == nil || *rows[1].ProviderConnectionID != primary.ID {
		t.Fatalf("failed provider_connection_id = %v, want %s", rows[1].ProviderConnectionID, primary.ID)
	}
	if rows[1].Status != "failed" {
		t.Fatalf("failed status = %q, want failed", rows[1].Status)
	}
	if rows[1].FailureClass == nil || *rows[1].FailureClass != "provider_auth" {
		t.Fatalf("failed failure_class = %v, want provider_auth", rows[1].FailureClass)
	}
	storedPrimary, err := connectionRepo.GetByID(ctx, primary.ID)
	if err != nil {
		t.Fatalf("GetByID primary: %v", err)
	}
	if storedPrimary.HealthStatus != string(HealthStateUnavailable) {
		t.Fatalf("primary health_status = %q, want %q", storedPrimary.HealthStatus, HealthStateUnavailable)
	}
}

func TestLiveModelGatewayClassifiesRateLimitFailures(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-rate-limit-org",
		DisplayName: "Live Gateway Rate Limit Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "rate-limit-key", "Rate Limit Key", "", "sk-rate-limit", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusTooManyRequests,
			Headers:    map[string]string{"Retry-After": "12"},
			Body:       `{"error":"rate_limited"}`,
		}},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai-rate-limit",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Rate Limited",
		APIKeyRef:          "ref:rate-limit-key",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "rate-limit-profile",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Rate Limit Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	_, err = gw.Complete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		HumanMessages:  []string{"hello"},
	})
	if !errors.Is(err, turn.ErrRateLimited) {
		t.Fatalf("err = %v, want turn.ErrRateLimited", err)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	if rows[0].FailureClass == nil || *rows[0].FailureClass != "provider_rate_limit" {
		t.Fatalf("failure_class = %v, want provider_rate_limit", rows[0].FailureClass)
	}
	storedConnection, err := connectionRepo.GetByID(ctx, connection.ID)
	if err != nil {
		t.Fatalf("GetByID connection: %v", err)
	}
	if storedConnection.HealthStatus != string(HealthStateRateLimited) {
		t.Fatalf("health_status = %q, want %q", storedConnection.HealthStatus, HealthStateRateLimited)
	}
}

func TestLiveModelGatewayClassifiesTransientProviderFailures(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-transient-org",
		DisplayName: "Live Gateway Transient Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "transient-key", "Transient Key", "", "sk-transient", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	serverURL := testutil.MockProviderServer(t, testutil.MockProviderFixture{
		Handlers: []testutil.MockHandler{{
			StatusCode: http.StatusBadGateway,
			Body:       `{"error":"upstream_unavailable"}`,
		}},
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai-transient",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	_ = mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Transient",
		APIKeyRef:          "ref:transient-key",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "transient-profile",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Transient Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	_, err = gw.Complete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		HumanMessages:  []string{"hello"},
	})
	if !errors.Is(err, turn.ErrModelTransient) {
		t.Fatalf("err = %v, want turn.ErrModelTransient", err)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	if rows[0].FailureClass == nil || *rows[0].FailureClass != "provider_transient" {
		t.Fatalf("failure_class = %v, want provider_transient", rows[0].FailureClass)
	}
}

func TestLiveModelGatewayMarksInvocationFailedWhenRequestContextIsCanceled(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-canceled-cleanup-org",
		DisplayName: "Live Gateway Canceled Cleanup Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "cancel-key", "Cancel Key", "", "sk-cancel", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	serverURL := server.URL

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai-cancel-cleanup",
		DisplayName: "OpenAI",
		APIBaseURL:  serverURL,
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "OpenAI Cancel Cleanup",
		APIKeyRef:          "ref:cancel-key",
		APIBaseURLOverride: &serverURL,
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "cancel-cleanup-profile",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Cancel Cleanup Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, callErr := gw.StreamComplete(callCtx, turn.ModelRequest{
			OrganizationID: org.ID,
			Purpose:        "agent_turn",
			Profile:        profile,
			HumanMessages:  []string{"hello"},
		}, nil)
		errCh <- callErr
	}()

	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for provider request to start")
	}
	cancel()

	var callErr error
	select {
	case callErr = <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for canceled gateway call to return")
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Fatalf("call err = %v, want context canceled", callErr)
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	if rows[0].ProviderConnectionID == nil || *rows[0].ProviderConnectionID != connection.ID {
		t.Fatalf("provider_connection_id = %v, want %s", rows[0].ProviderConnectionID, connection.ID)
	}
	if rows[0].Status != "failed" {
		t.Fatalf("status = %q, want failed", rows[0].Status)
	}
	if rows[0].ErrorCode == nil || *rows[0].ErrorCode != "provider_transient_failure" {
		t.Fatalf("error_code = %v, want provider_transient_failure", rows[0].ErrorCode)
	}
}

func TestLiveModelGatewayClassifiesProductRuntimeFailures(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-product-runtime-org",
		DisplayName: "Live Gateway Product Runtime Org",
	})

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "openai-product-runtime",
		DisplayName: "OpenAI",
		APIBaseURL:  "https://provider.example",
		IsEnabled:   true,
	})
	connection := mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:   org.ID,
		ProviderID:       provider.ID,
		DisplayName:      "OpenAI Missing Secret",
		APIKeyRef:        "ref:missing-runtime-secret",
		FailoverPriority: 1,
		MaxConcurrent:    1,
		IsEnabled:        true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "product-runtime-profile",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "gpt-4o-mini",
		DisplayName:         "Product Runtime Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		HealthStore: connectionRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	_, err = gw.Complete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		HumanMessages:  []string{"hello"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	rows, err := invocationRepo.ListByOrg(ctx, org.ID)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("invocation rows = %d, want 1", len(rows))
	}
	if rows[0].FailureClass == nil || *rows[0].FailureClass != "product_runtime" {
		t.Fatalf("failure_class = %v, want product_runtime", rows[0].FailureClass)
	}
	storedConnection, err := connectionRepo.GetByID(ctx, connection.ID)
	if err != nil {
		t.Fatalf("GetByID connection: %v", err)
	}
	if storedConnection.HealthStatus != string(HealthStateHealthy) {
		t.Fatalf("health_status = %q, want %q", storedConnection.HealthStatus, HealthStateHealthy)
	}
}

func TestLiveModelGatewayCompleteAnthropicSubscriptionAuth(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        "live-gateway-anthropic-subscription-org",
		DisplayName: "Live Gateway Anthropic Subscription Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "anthropic-subscription-access", "Anthropic Subscription Access", "", "sk-ant-oat-access", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set access secret: %v", err)
	}
	if err := secretSvc.Set(ctx, org.ID, "anthropic-subscription-refresh", "Anthropic Subscription Refresh", "", "refresh-token", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set refresh secret: %v", err)
	}

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer sk-ant-oat-access" {
			http.Error(w, "invalid authorization", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("x-api-key"); got != "" {
			http.Error(w, "x-api-key must be empty in subscription mode", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("anthropic-beta"); !strings.Contains(got, "oauth-2025-04-20") {
			http.Error(w, "missing oauth beta header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":4,"output_tokens":1}}`))
	}))
	defer server.Close()

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        "anthropic",
		DisplayName: "Anthropic",
		APIBaseURL:  server.URL,
		IsEnabled:   true,
	})

	metadata, err := json.Marshal(map[string]any{
		providerConnectionMetadataAuthMode:                          anthropicAuthModeSubscription,
		providerConnectionMetadataSubscriptionAccessTokenSecretRef:  "ref:anthropic-subscription-access",
		providerConnectionMetadataSubscriptionRefreshTokenSecretRef: "ref:anthropic-subscription-refresh",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	_ = mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        "Anthropic Subscription",
		APIKeyRef:          "ref:anthropic-subscription-access",
		APIBaseURLOverride: stringPtr(server.URL),
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
		Metadata:           metadata,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "anthropic-subscription-profile",
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           "claude-3-5-sonnet",
		DisplayName:         "Anthropic Subscription Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}

	response, err := gw.Complete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "user", Content: "hello"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.TrimSpace(response.Content) != "ok" {
		t.Fatalf("response.Content = %q, want ok", response.Content)
	}
	if requests != 1 {
		t.Fatalf("provider request count = %d, want 1", requests)
	}
}

func TestLiveModelGatewayCompleteOpenAIToolNamesAreSanitized(t *testing.T) {
	serverURL := sanitizedToolNameValidationServer(t, "openai")
	gw, profile, orgID := newToolNameGatewayFixture(t, toolNameGatewayFixtureOptions{
		providerSlug:   "openai",
		displayName:    "OpenAI",
		modelName:      "gpt-4o-mini",
		orgSlug:        "live-gateway-openai-tools",
		orgDisplay:     "Live Gateway OpenAI Tools",
		secretName:     "openai-tools-key",
		secretValue:    "sk-openai-tools",
		profileID:      "openai-tools",
		connectionName: "OpenAI Tools Primary",
		serverURL:      serverURL,
	})

	response, err := gw.Complete(context.Background(), turn.ModelRequest{
		OrganizationID: orgID,
		Purpose:        "agent_turn",
		Profile:        profile,
		Prompt:         dottedToolPrompt(),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.TrimSpace(response.Content) != "ok" {
		t.Fatalf("response.Content = %q, want ok", response.Content)
	}
}

func TestLiveModelGatewayCompleteAnthropicToolNamesAreSanitized(t *testing.T) {
	serverURL := sanitizedToolNameValidationServer(t, "anthropic")
	gw, profile, orgID := newToolNameGatewayFixture(t, toolNameGatewayFixtureOptions{
		providerSlug:   "anthropic",
		displayName:    "Anthropic",
		modelName:      "claude-3-5-sonnet",
		orgSlug:        "live-gateway-anthropic-tools",
		orgDisplay:     "Live Gateway Anthropic Tools",
		secretName:     "anthropic-tools-key",
		secretValue:    "sk-anthropic-tools",
		profileID:      "anthropic-tools",
		connectionName: "Anthropic Tools Primary",
		serverURL:      serverURL,
	})

	response, err := gw.Complete(context.Background(), turn.ModelRequest{
		OrganizationID: orgID,
		Purpose:        "agent_turn",
		Profile:        profile,
		Prompt:         dottedToolPrompt(),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if strings.TrimSpace(response.Content) != "ok" {
		t.Fatalf("response.Content = %q, want ok", response.Content)
	}
}

type toolNameGatewayFixtureOptions struct {
	providerSlug   string
	displayName    string
	modelName      string
	orgSlug        string
	orgDisplay     string
	secretName     string
	secretValue    string
	profileID      string
	connectionName string
	serverURL      string
}

func newToolNameGatewayFixture(t *testing.T, opts toolNameGatewayFixtureOptions) (*LiveModelGateway, repo.ModelProfile, uuid.UUID) {
	t.Helper()
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(testMasterKey()))
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	ctx := context.Background()
	pool := testdb.New(t)

	orgRepo := repo.NewOrgRepo(pool)
	providerRepo := repo.NewModelProviderRepo(pool)
	connectionRepo := repo.NewProviderConnectionRepo(pool)
	profileRepo := repo.NewModelProfileRepo(pool)
	invocationRepo := repo.NewModelInvocationRepo(pool)
	secretSvc := secret.NewService(repo.NewSecretRepo(pool))

	org := mustCreateOrg(t, ctx, orgRepo, repo.Organization{
		Slug:        strings.TrimSpace(opts.orgSlug),
		DisplayName: strings.TrimSpace(opts.orgDisplay),
	})
	if err := secretSvc.Set(ctx, org.ID, strings.TrimSpace(opts.secretName), "Provider API Key", "", strings.TrimSpace(opts.secretValue), secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	provider := mustCreateProvider(t, ctx, providerRepo, repo.ModelProvider{
		Slug:        strings.TrimSpace(opts.providerSlug),
		DisplayName: strings.TrimSpace(opts.displayName),
		APIBaseURL:  strings.TrimSpace(opts.serverURL),
		IsEnabled:   true,
	})
	_ = mustCreateConnection(t, ctx, connectionRepo, repo.ProviderConnection{
		OrganizationID:     org.ID,
		ProviderID:         provider.ID,
		DisplayName:        strings.TrimSpace(opts.connectionName),
		APIKeyRef:          "ref:" + strings.TrimSpace(opts.secretName),
		APIBaseURLOverride: stringPtr(strings.TrimSpace(opts.serverURL)),
		FailoverPriority:   1,
		MaxConcurrent:      1,
		IsEnabled:          true,
	})

	orgID := org.ID
	profile, err := profileRepo.Create(ctx, repo.ModelProfile{
		LogicalProfileID:    strings.TrimSpace(opts.profileID),
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          provider.ID,
		ModelName:           strings.TrimSpace(opts.modelName),
		DisplayName:         "Tool Name Profile",
		ContextWindowTokens: 128000,
		MaxOutputTokens:     2048,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	health := NewHealthChecker()
	gw, err := NewLiveModelGateway(LiveModelGatewayOptions{
		Router:      NewRouter(profileRepo, connectionRepo, health),
		Providers:   providerRepo,
		Secrets:     secretSvc,
		Invocations: invocationRepo,
		Health:      health,
	})
	if err != nil {
		t.Fatalf("NewLiveModelGateway: %v", err)
	}
	return gw, profile, org.ID
}

func dottedToolPrompt() *prompt.AssembledPrompt {
	return &prompt.AssembledPrompt{
		Messages: []prompt.PromptMessage{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Use tools to answer."},
		},
		ToolDescriptors: []tools.ToolDescriptor{
			{
				Name:        "file.read",
				Description: "Read file contents",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
			{
				Name:        "git.status",
				Description: "Show git status",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
		TotalTokens: 32,
	}
}

func sanitizedToolNameValidationServer(t *testing.T, providerSlug string) string {
	t.Helper()
	validName := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			http.Error(w, "invalid json body", http.StatusBadRequest)
			return
		}

		names, err := providerToolNames(strings.TrimSpace(providerSlug), payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for _, name := range names {
			if !validName.MatchString(name) {
				http.Error(w, fmt.Sprintf("invalid tool name: %s", name), http.StatusBadRequest)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimSpace(providerSlug) {
		case "anthropic":
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":7,"output_tokens":1}}`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":1}}`))
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func providerToolNames(providerSlug string, payload map[string]any) ([]string, error) {
	toolsValue, ok := payload["tools"]
	if !ok {
		return nil, fmt.Errorf("missing tools payload")
	}
	toolItems, ok := toolsValue.([]any)
	if !ok || len(toolItems) == 0 {
		return nil, fmt.Errorf("tools payload is empty")
	}

	names := make([]string, 0, len(toolItems))
	switch providerSlug {
	case "anthropic":
		for _, item := range toolItems {
			toolMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid anthropic tool payload")
			}
			name, _ := toolMap["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("missing anthropic tool name")
			}
			names = append(names, name)
		}
	default:
		for _, item := range toolItems {
			toolMap, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid openai tool payload")
			}
			function, ok := toolMap["function"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("missing openai function payload")
			}
			name, _ := function["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("missing openai tool name")
			}
			names = append(names, name)
		}
	}
	return names, nil
}

func testMasterKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}
