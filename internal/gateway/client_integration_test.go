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
