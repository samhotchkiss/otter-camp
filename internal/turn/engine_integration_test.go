//go:build integration

package turn

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

func TestTurnEngineIntegrationFullTurnCycle(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("hello"); err != nil {
			return ModelResponse{}, err
		}
		if err := onChunk(" world"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "hello world"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected a created turn")
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("turn status = %s, want completed", turns[len(turns)-1].Status)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if !hasAssistantFinal(messages) {
		t.Fatal("expected final assistant message")
	}

	var completedEvents int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'chat.turn.completed'
	`, fixture.org.ID).Scan(&completedEvents); err != nil {
		t.Fatalf("count completed events: %v", err)
	}
	if completedEvents == 0 {
		t.Fatal("expected chat.turn.completed event")
	}
}

func TestTurnEngineIntegrationTier1ToolDispatchRoundTrip(t *testing.T) {
	fixture := newIntegrationFixture(t)
	modelCalls := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "tool-1", Name: "memory.query", Tier: "tier1", Arguments: map[string]any{"q": "hello"}}}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}
	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"items": []string{"a", "b"}}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	foundToolResult := false
	for _, item := range messages {
		if item.Role != "tool_result" {
			continue
		}
		if item.ToolCallID != nil && *item.ToolCallID == "tool-1" {
			foundToolResult = true
			break
		}
	}
	if !foundToolResult {
		t.Fatal("expected tool_result message for tool-1")
	}
}

func TestTurnEngineIntegrationRetryTransientErrorRecordsThreeAttempts(t *testing.T) {
	fixture := newIntegrationFixture(t)
	attempt := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		attempt++
		if attempt < 3 {
			return ModelResponse{}, ErrModelTransient
		}
		return ModelResponse{Content: "recovered"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if attempt != 3 {
		t.Fatalf("attempts = %d, want 3", attempt)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	turnID := turns[len(turns)-1].ID

	rows, err := repo.NewModelInvocationRepo(fixture.pool).ListBySession(context.Background(), fixture.org.ID, fixture.session.ID)
	if err != nil {
		t.Fatalf("list invocations: %v", err)
	}
	count := 0
	for _, item := range rows {
		if item.TurnID != nil && *item.TurnID == turnID {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("turn invocation count = %d, want 3", count)
	}
}

func TestTurnEngineIntegrationCancelDuringTier2RequestsRunCancel(t *testing.T) {
	fixture := newIntegrationFixture(t)
	runStarted := make(chan uuid.UUID, 1)
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{{ID: "tool-t2", Name: "cli.execute", Tier: "tier2"}}}, nil
	}
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		runStarted <- runID
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID)
	}()

	var runID uuid.UUID
	select {
	case runID = <-runStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tier2 run start")
	}

	turnID := waitForTurnID(t, fixture.pool, fixture.session.ID)
	payload := mustJSON(t, map[string]any{"session_id": fixture.session.ID.String(), "turn_id": turnID.String()})
	if err := fixture.bus.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish cancel: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleUserMessage returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for HandleUserMessage")
	}

	if len(fixture.runCanceler.calls) == 0 {
		t.Fatal("expected RequestCancel call")
	}
	if fixture.runCanceler.calls[0] != runID {
		t.Fatalf("cancel run id = %s, want %s", fixture.runCanceler.calls[0], runID)
	}
}

func TestTurnEngineIntegrationReactionFeedbackAdjustsMemoryConfidence(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("assistant"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "assistant response"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("list session messages: %v", err)
	}
	assistant, ok := latestAssistantMessage(messages)
	if !ok {
		t.Fatal("expected assistant message")
	}

	memoryRepo := repo.NewMemoryRepo(fixture.pool)
	memoryRow, err := memoryRepo.Create(context.Background(), repo.Memory{
		OrganizationID: fixture.org.ID,
		MemoryType:     "episodic",
		Scope:          "org",
		Content:        "assistant-derived memory",
		ContentHash:    uuid.NewString(),
		Confidence:     0.50,
		UtilityScore:   0.50,
		Status:         "active",
		TrustTier:      0.8,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}

	assistantID := assistant.ID
	sessionID := fixture.session.ID
	if _, err := repo.NewMemorySourceRepo(fixture.pool).Create(context.Background(), repo.MemorySource{
		MemoryID:   memoryRow.ID,
		SourceType: "chat_message",
		SourceID:   &assistantID,
		SessionID:  &sessionID,
	}); err != nil {
		t.Fatalf("create memory source: %v", err)
	}

	sub := fixture.engine.SubscribeReactionFeedback(&fixture.org.ID)
	defer fixture.bus.Unsubscribe(sub)

	actorID := fixture.user.ID
	if err := fixture.bus.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.reaction.added",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": fixture.session.ID.String(), "message_id": assistant.ID.String(), "emoji": "👍"}),
	}); err != nil {
		t.Fatalf("publish thumbs-up reaction: %v", err)
	}
	waitForMemoryConfidence(t, memoryRepo, memoryRow.ID, 0.55)

	if err := fixture.bus.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.reaction.added",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": fixture.session.ID.String(), "message_id": assistant.ID.String(), "emoji": "👎"}),
	}); err != nil {
		t.Fatalf("publish thumbs-down reaction: %v", err)
	}
	waitForMemoryConfidence(t, memoryRepo, memoryRow.ID, 0.45)
}

type integrationFixture struct {
	pool       *pgxpool.Pool
	org        repo.Organization
	user       repo.HumanUser
	agent      repo.Agent
	session    repo.ChatSession
	userMessage repo.ChatMessage

	bus        *eventbus.Bus
	engine     *TurnEngine
	model      *fakeModelGateway
	dispatcher *fakeDispatcher
	runCanceler *fakeRunCanceler
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool)
	user := mustCreateUser(t, ctx, pool, org.ID)
	agent := mustCreateAgent(t, ctx, pool, org.ID)
	provider := mustCreateProvider(t, ctx, pool)
	profile := mustCreateProfile(t, ctx, pool, org.ID, provider.ID)

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{PollInterval: 10 * time.Millisecond})
	chatService, err := chat.NewService(chat.Options{Pool: pool, Events: bus})
	if err != nil {
		t.Fatalf("chat.NewService: %v", err)
	}

	session, err := chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: org.ID,
		ScopeType:      "organization",
		ScopeID:        org.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := chatService.AddParticipant(ctx, session.ID, "agent", agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}
	authorType := "human_user"
	userMessage, err := chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &user.ID,
		Role:       "user",
		Content:    "hello",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	modelGateway := &fakeModelGateway{}
	dispatcher := &fakeDispatcher{}
	runCanceler := &fakeRunCanceler{}

	engine, err := NewEngine(Options{
		Pool:          pool,
		Chat:          chatService,
		ToolResolver:  &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "memory.query", Tier: "tier1"}, {Name: "cli.execute", Tier: "tier2"}}},
		Assembler:     &fakeAssembler{results: []assembleResult{{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 30}}}},
		Summarization: &fakeSummarizationChecker{},
		ModelGateway:  modelGateway,
		Dispatcher:    dispatcher,
		RunCanceler:   runCanceler,
		Events:        bus,
		Enqueuer:      jobqueue.New(pool, nil, jobqueue.Config{}),
		Invocations:   repo.NewModelInvocationRepo(pool),
		ModelProfiles: repo.NewModelProfileRepo(pool),
		Profiles:      &fakeProfileResolver{profile: profile},
		Messages:      repo.NewChatMessageRepo(pool),
		Turns:         repo.NewChatTurnRepo(pool),
		Sessions:      repo.NewChatSessionRepo(pool),
		Agents:        repo.NewAgentRepo(pool),
		Tasks:         repo.NewProjectTaskRepo(pool),
		MemorySources: repo.NewMemorySourceRepo(pool),
		Memories:      repo.NewMemoryRepo(pool),
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	return &integrationFixture{
		pool:        pool,
		org:         org,
		user:        user,
		agent:       agent,
		session:     repo.ChatSession(*session),
		userMessage: repo.ChatMessage(*userMessage),
		bus:         bus,
		engine:      engine,
		model:       modelGateway,
		dispatcher:  dispatcher,
		runCanceler: runCanceler,
	}
}

func hasAssistantFinal(messages []repo.ChatMessage) bool {
	for _, item := range messages {
		if item.Role == "assistant" && item.Status == "final" {
			return true
		}
	}
	return false
}

func latestAssistantMessage(messages []repo.ChatMessage) (repo.ChatMessage, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		item := messages[i]
		if item.Role == "assistant" && item.TurnID != nil {
			return item, true
		}
	}
	return repo.ChatMessage{}, false
}

func waitForMemoryConfidence(t *testing.T, memories *repo.MemoryRepo, memoryID uuid.UUID, want float64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		row, err := memories.GetByID(context.Background(), memoryID)
		if err == nil {
			got := row.Confidence
			if diff := got - want; diff < 0.001 && diff > -0.001 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	row, err := memories.GetByID(context.Background(), memoryID)
	if err != nil {
		t.Fatalf("get memory confidence: %v", err)
	}
	t.Fatalf("memory confidence = %.3f, want %.3f", row.Confidence, want)
}

func waitForTurnID(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		turns, err := repo.NewChatTurnRepo(pool).ListBySession(context.Background(), sessionID)
		if err == nil && len(turns) > 0 {
			return turns[len(turns)-1].ID
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for turn")
	return uuid.Nil
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func mustCreateOrg(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.Organization {
	t.Helper()
	item, err := repo.NewOrgRepo(pool).Create(ctx, repo.Organization{Slug: "turn-org-" + uuid.NewString()[:8], DisplayName: "Turn Org"})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	return item
}

func mustCreateUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.HumanUser {
	t.Helper()
	item, err := repo.NewHumanUserRepo(pool).Create(ctx, repo.HumanUser{
		OrganizationID: orgID,
		Email:          "turn-" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "Turn User",
		Role:           "member",
		IsActive:       true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return item
}

func mustCreateAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	item, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Turn Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are helpful.",
		OperatorInstructions: "",
		AgentType:            "worker",
		IsStarterTrio:        false,
		PrivateMemory:        false,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return item
}

func mustCreateProvider(t *testing.T, ctx context.Context, pool *pgxpool.Pool) repo.ModelProvider {
	t.Helper()
	item, err := repo.NewModelProviderRepo(pool).Create(ctx, repo.ModelProvider{
		Slug:         "turn-provider-" + uuid.NewString()[:8],
		DisplayName:  "Turn Provider",
		APIBaseURL:   "https://example.invalid",
		IsEnabled:    true,
		Metadata:     []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return item
}

func mustCreateProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, providerID uuid.UUID) repo.ModelProfile {
	t.Helper()
	item, err := repo.NewModelProfileRepo(pool).Create(ctx, repo.ModelProfile{
		LogicalProfileID:    "turn-profile-" + uuid.NewString()[:8],
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           "test-model",
		DisplayName:         "Test Model",
		ContextWindowTokens: 8192,
		MaxOutputTokens:     1024,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	return item
}
