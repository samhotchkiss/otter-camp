//go:build integration

package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/secret"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/testutil"
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

func TestLiveModelGatewayStreamCompleteOpenAIParsesToolCalls(t *testing.T) {
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
		Slug:        "live-gateway-tool-call-org",
		DisplayName: "Live Gateway Tool Call Org",
	})
	if err := secretSvc.Set(ctx, org.ID, "openai-live-tool-call", "OpenAI Live Tool Call Key", "", "sk-live-tool-call", secret.Principal{Type: "human", ID: uuid.Nil}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	sseBody := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file_read","arguments":"{\"path\":\"/tmp/"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"readme.md\"}"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":13,"completion_tokens":7}}`,
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
		DisplayName:        "OpenAI Tool Call Connection",
		APIKeyRef:          "ref:openai-live-tool-call",
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

	response, err := gw.StreamComplete(ctx, turn.ModelRequest{
		OrganizationID: org.ID,
		Purpose:        "agent_turn",
		Profile:        profile,
		Prompt: &prompt.AssembledPrompt{
			Messages: []prompt.PromptMessage{
				{Role: "system", Content: "You can use tools."},
				{Role: "user", Content: "Read /tmp/readme.md"},
			},
			TotalTokens: 13,
		},
	}, nil)
	if err != nil {
		t.Fatalf("StreamComplete: %v", err)
	}
	if response.Content != "" {
		t.Fatalf("response.Content = %q, want empty content for tool-call turn", response.Content)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(response.ToolCalls))
	}
	toolCall := response.ToolCalls[0]
	if toolCall.ID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", toolCall.ID)
	}
	if toolCall.Name != "file_read" {
		t.Fatalf("tool call name = %q, want file_read", toolCall.Name)
	}
	if got := toolCall.Arguments["path"]; got != "/tmp/readme.md" {
		t.Fatalf("tool call arguments.path = %v, want /tmp/readme.md", got)
	}
	if response.Usage == nil {
		t.Fatal("response.Usage is nil")
	}
	if response.Usage.InputTokens != 13 {
		t.Fatalf("usage.input_tokens = %d, want 13", response.Usage.InputTokens)
	}
	if response.Usage.OutputTokens != 7 {
		t.Fatalf("usage.output_tokens = %d, want 7", response.Usage.OutputTokens)
	}
	if state := health.GetState(connection.ID); state != HealthStateHealthy {
		t.Fatalf("connection health = %q, want %q", state, HealthStateHealthy)
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
}

func testMasterKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}
