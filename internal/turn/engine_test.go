package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/tools"
)

func TestListeningEvalSkippedForSyncSinglePending(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("hello"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "hello"}, nil
	}
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for sync with single pending message")
		return ModelResponse{}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
}

func TestListeningEvalRunsForAsyncSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "continuation"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 1 {
		t.Fatalf("listening eval calls = %d, want 1", fixture.model.listeningEvalCalls)
	}
}

func TestListeningEvalWaitReenqueuesAndSkipsPhase2(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "wait"}, nil
		}
		return ModelResponse{Content: "respond"}, nil
	}
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		t.Fatal("phase 2 model call should be skipped when listening eval returns wait")
		return ModelResponse{}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.listeningEvalCalls != 1 {
		t.Fatalf("listening eval calls = %d, want 1", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.payload == nil {
		t.Fatal("agent_turn payload missing")
	}
	if job.payload.SessionID != fixture.session.ID || job.payload.MessageID != fixture.userMessageID {
		t.Fatalf("unexpected re-enqueue payload: %+v", *job.payload)
	}
	if job.runAfter == nil {
		t.Fatal("expected run_after to be set")
	}
	wantRunAfter := base.Add(fixture.engine.listeningEvalDelay)
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", job.runAfter, wantRunAfter)
	}
	if !job.runAfter.After(base) {
		t.Fatalf("run_after = %s, want > %s", job.runAfter, base)
	}
}

func TestTierRoutingExecutesTier1ParallelThenTier2Sequential(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var (
		mu         sync.Mutex
		tier1Start []time.Time
		tier2Start []time.Time
	)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		mu.Lock()
		tier1Start = append(tier1Start, time.Now())
		mu.Unlock()
		time.Sleep(40 * time.Millisecond)
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		mu.Lock()
		tier2Start = append(tier2Start, time.Now())
		mu.Unlock()
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"done": true}, RunID: &runID}, nil
	}

	callRound := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		_ = onChunk("token")
		callRound++
		if callRound == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "t1", Name: "file.read", Tier: "tier1"},
				{ID: "t2", Name: "memory.query", Tier: "tier1"},
				{ID: "t3", Name: "cli.execute", Tier: "tier2"},
			}}, nil
		}
		return ModelResponse{Content: "final"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(tier1Start) != 2 {
		t.Fatalf("tier1 calls = %d, want 2", len(tier1Start))
	}
	if len(tier2Start) != 1 {
		t.Fatalf("tier2 calls = %d, want 1", len(tier2Start))
	}
	latestTier1 := tier1Start[0]
	for _, at := range tier1Start[1:] {
		if at.After(latestTier1) {
			latestTier1 = at
		}
	}
	if tier2Start[0].Before(latestTier1) {
		t.Fatalf("tier2 started before tier1 completed: tier2=%s latest_tier1=%s", tier2Start[0], latestTier1)
	}
}

func TestTierRoutingTwoTier2ToolsAreSequential(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var (
		mu          sync.Mutex
		startedAt   = map[string]time.Time{}
		completedAt = map[string]time.Time{}
	)
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)

		start := time.Now()
		mu.Lock()
		startedAt[call.ID] = start
		mu.Unlock()

		if call.ID == "tier2-a" {
			time.Sleep(40 * time.Millisecond)
		}

		end := time.Now()
		mu.Lock()
		completedAt[call.ID] = end
		mu.Unlock()

		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "tier2-a", Name: "cli.execute", Tier: "tier2"},
				{ID: "tier2-b", Name: "run.deploy", Tier: "tier2"},
			}}, nil
		}
		return ModelResponse{Content: "final"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if len(startedAt) != 2 || len(completedAt) != 2 {
		t.Fatalf("tier2 calls recorded start=%d complete=%d, want 2 each", len(startedAt), len(completedAt))
	}
	if startedAt["tier2-b"].Before(completedAt["tier2-a"]) {
		t.Fatalf("tier2-b started before tier2-a completed: start=%s first_done=%s", startedAt["tier2-b"], completedAt["tier2-a"])
	}
}

func TestMaxToolCallsStopCondition(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.maxToolCalls = 3

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: "a", Name: "file.read", Tier: "tier1"},
			{ID: "b", Name: "memory.query", Tier: "tier1"},
			{ID: "c", Name: "chat.search", Tier: "tier1"},
			{ID: "d", Name: "task.list", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
}

func TestMaxToolCallsBudgetAccumulatesAcrossRounds(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.maxToolCalls = 3

	var (
		mu              sync.Mutex
		dispatchedTier1 []string
	)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		mu.Lock()
		dispatchedTier1 = append(dispatchedTier1, call.ID)
		mu.Unlock()
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "a", Name: "file.read", Tier: "tier1"},
				{ID: "b", Name: "memory.query", Tier: "tier1"},
			}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "c", Name: "chat.search", Tier: "tier1"},
				{ID: "d", Name: "task.list", Tier: "tier1"},
			}}, nil
		default:
			return ModelResponse{Content: "final"}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), dispatchedTier1...)
	mu.Unlock()
	sort.Strings(got)
	if len(got) != 3 {
		t.Fatalf("tier1 dispatches = %v, want 3 total tool calls", got)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("tier1 dispatch calls = %v, want [a b c]", got)
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
}

func TestMaxDurationStopCondition(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.syncMaxDuration = 1 * time.Millisecond
	fixture.engine.now = func() time.Time { return base.Add(5 * time.Millisecond) }
	fixture.chat.startedAt = base

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{{ID: "x", Name: "file.read", Tier: "tier1"}}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if !fixture.messages.containsContent("[Turn duration limit reached. Turn ended.]") {
		t.Fatal("missing max duration stop system message")
	}
}

func TestDispatchToolsReverseMapsSanitizedToolNames(t *testing.T) {
	fixture := newUnitFixture(t, "sync")

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			// The model returns the API-safe name; dispatch must map it back.
			return ModelResponse{
				ToolCalls: []ModelToolCall{{ID: "tool-1", Name: "file_read", Arguments: map[string]any{"path": "/tmp/data"}}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if dispatched.Name != "file.read" {
		t.Fatalf("dispatched tool name = %q, want file.read", dispatched.Name)
	}
	if dispatched.Tier != "tier1" {
		t.Fatalf("dispatched tier = %q, want tier1", dispatched.Tier)
	}
	if got, _ := dispatched.Arguments["path"].(string); got != "/tmp/data" {
		t.Fatalf("dispatched argument path = %v, want /tmp/data", dispatched.Arguments["path"])
	}
}

func TestCancellationDuringStreaming(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	streamStarted := make(chan struct{})

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("partial"); err != nil {
			return ModelResponse{}, err
		}
		close(streamStarted)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	}()

	select {
	case <-streamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream to start")
	}

	turnID := fixture.chat.waitForTurnID(t)
	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "turn_id": turnID.String()})
	if err := fixture.events.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "human",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish cancel event: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleUserMessage returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HandleUserMessage")
	}

	if !fixture.messages.containsStatus("assistant", "failed") {
		t.Fatal("expected assistant message in failed status")
	}
	if !fixture.messages.containsContent("[Turn cancelled by user.]") {
		t.Fatal("missing cancellation system message")
	}
	if fixture.chat.cancelCalls == 0 {
		t.Fatal("expected CancelTurn to be called")
	}
}

func TestCancellationDuringTier2DispatchRequestsRunCancel(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	runStarted := make(chan uuid.UUID, 1)
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		runStarted <- runID
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{{ID: "tier2", Name: "cli.execute", Tier: "tier2"}}}, nil
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	}()

	var runID uuid.UUID
	select {
	case runID = <-runStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for tier2 run start")
	}
	turnID := fixture.chat.waitForTurnID(t)
	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "turn_id": turnID.String()})
	_ = fixture.events.Publish(context.Background(), nil, eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "human",
		Payload:        payload,
	})

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("HandleUserMessage returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HandleUserMessage")
	}

	if len(fixture.runCanceler.calls) == 0 {
		t.Fatal("expected run cancel request")
	}
	if fixture.runCanceler.calls[0] != runID {
		t.Fatalf("cancelled run id = %s, want %s", fixture.runCanceler.calls[0], runID)
	}
}

func TestReactionFeedbackNoLinkedMemories(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	assistant := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "assistant",
		Status:    "final",
	})
	turnID := fixture.chat.waitForTurnIDOptional()
	if turnID == uuid.Nil {
		turnID = uuid.New()
	}
	assistant.TurnID = &turnID
	fixture.messages.upsert(assistant)

	payload, _ := json.Marshal(map[string]any{"session_id": fixture.session.ID.String(), "message_id": assistant.ID.String(), "emoji": "👍"})
	err := fixture.engine.HandleReactionEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.reaction.added",
		ActorType:      "human",
		Payload:        payload,
	})
	if err != nil {
		t.Fatalf("HandleReactionEvent: %v", err)
	}
	if fixture.memories.updateCalls != 0 {
		t.Fatalf("memory update calls = %d, want 0", fixture.memories.updateCalls)
	}
}

func TestContinuationTurnOnContextCompressed(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "summary"}, nil
		}
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	first := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	second := fixture.chat.turnByID(fixture.chat.turnOrder[1])
	if first == nil || second == nil || first.CycleID == nil || second.CycleID == nil {
		t.Fatal("expected two turns with cycle ids")
	}
	if *first.CycleID != *second.CycleID {
		t.Fatalf("continuation cycle id mismatch: %s != %s", *first.CycleID, *second.CycleID)
	}
}

type unitFixture struct {
	engine        *TurnEngine
	events        *fakeEventBus
	chat          *fakeChatService
	messages      *fakeMessageRepo
	model         *fakeModelGateway
	dispatcher    *fakeDispatcher
	runCanceler   *fakeRunCanceler
	enqueuer      *fakeEnqueuer
	assembler     *fakeAssembler
	memories      *fakeMemoryRepo
	session       *chat.ChatSession
	userMessageID uuid.UUID
}

func newUnitFixture(t *testing.T, mode string) *unitFixture {
	t.Helper()
	orgID := uuid.New()
	sessionID := uuid.New()
	agentID := uuid.New()

	session := &chat.ChatSession{ID: sessionID, OrganizationID: orgID, ScopeType: "organization", ScopeID: orgID, Mode: mode, Status: "active"}
	messages := newFakeMessageRepo()
	userMsg := messages.create(repo.ChatMessage{SessionID: sessionID, Role: "user", Status: "pending", Content: "hello"})

	chatSvc := &fakeChatService{
		session: session,
		participants: []*chat.ChatParticipant{{
			ID:              uuid.New(),
			SessionID:       sessionID,
			ParticipantType: "agent",
			ParticipantID:   agentID,
		}},
		messages: messages,
		turns:    map[uuid.UUID]*chat.ChatTurn{},
		turnCh:   make(chan uuid.UUID, 4),
	}

	profile := repo.ModelProfile{
		ID:                  uuid.New(),
		LogicalProfileID:    "test-profile",
		Version:             1,
		IsCurrent:           true,
		ProviderID:          uuid.New(),
		ModelName:           "test-model",
		InvocationPurpose:   "agent_turn",
		SupportsStreaming:   true,
		ContextWindowTokens: 4096,
		MaxOutputTokens:     1024,
	}

	toolResolver := &fakeToolResolver{}
	assembler := &fakeAssembler{results: []assembleResult{{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "s"}}, TotalTokens: 20}}}}
	modelGateway := &fakeModelGateway{}
	dispatcher := &fakeDispatcher{}
	events := &fakeEventBus{}
	enqueuer := &fakeEnqueuer{}
	invocations := &fakeInvocationRepo{}
	profiles := &fakeModelProfileRepo{profile: profile}
	resolver := &fakeProfileResolver{profile: profile}
	memSources := &fakeMemorySourceRepo{}
	memories := &fakeMemoryRepo{}

	engine, err := NewEngine(Options{
		Chat:          chatSvc,
		ToolResolver:  toolResolver,
		Assembler:     assembler,
		Summarization: &fakeSummarizationChecker{},
		ModelGateway:  modelGateway,
		Dispatcher:    dispatcher,
		RunCanceler:   &fakeRunCanceler{},
		Events:        events,
		Enqueuer:      enqueuer,
		Invocations:   invocations,
		ModelProfiles: profiles,
		Profiles:      resolver,
		Messages:      messages,
		Turns:         chatSvc,
		Sessions:      chatSvc,
		Agents:        &fakeAgentRepo{agent: repo.Agent{ID: agentID, OrganizationID: orgID}},
		Tasks:         &fakeTaskRepo{},
		MemorySources: memSources,
		Memories:      memories,
		Now:           func() time.Time { return time.Now().UTC() },
		Sleep:         func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	runCanceler := &fakeRunCanceler{}
	engine.runCanceler = runCanceler

	return &unitFixture{
		engine:        engine,
		events:        events,
		chat:          chatSvc,
		messages:      messages,
		model:         modelGateway,
		dispatcher:    dispatcher,
		runCanceler:   runCanceler,
		enqueuer:      enqueuer,
		assembler:     assembler,
		memories:      memories,
		session:       session,
		userMessageID: userMsg.ID,
	}
}

type fakeChatService struct {
	mu            sync.Mutex
	session       *chat.ChatSession
	participants  []*chat.ChatParticipant
	messages      *fakeMessageRepo
	turns         map[uuid.UUID]*chat.ChatTurn
	turnOrder     []uuid.UUID
	turnCh        chan uuid.UUID
	startedAt     time.Time
	cancelCalls   int
	completeCalls int
	failCalls     int
}

func (f *fakeChatService) GetSession(ctx context.Context, id uuid.UUID) (*chat.ChatSession, error) {
	if f.session.ID != id {
		return nil, repo.ErrNotFound
	}
	copySession := *f.session
	return &copySession, nil
}

func (f *fakeChatService) CreateTurn(ctx context.Context, sessionID, agentID uuid.UUID) (*chat.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cycleID := uuid.New()
	turnID := uuid.New()
	turn := &chat.ChatTurn{
		ID:             turnID,
		SessionID:      sessionID,
		TurnNumber:     len(f.turnOrder) + 1,
		CycleID:        &cycleID,
		RespondingType: "agent",
		RespondingID:   agentID,
		Status:         "pending",
	}
	f.turns[turnID] = turn
	f.turnOrder = append(f.turnOrder, turnID)
	select {
	case f.turnCh <- turnID:
	default:
	}
	copyTurn := *turn
	return &copyTurn, nil
}

func (f *fakeChatService) StartTurn(ctx context.Context, turnID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	turn.Status = "in_progress"
	at := f.startedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	turn.StartedAt = &at
	return nil
}

func (f *fakeChatService) CompleteTurn(ctx context.Context, turnID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	f.completeCalls++
	turn.Status = "completed"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	return nil
}

func (f *fakeChatService) CancelTurn(ctx context.Context, turnID uuid.UUID, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	f.cancelCalls++
	turn.Status = "cancelled"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	turn.CancelRequestedAt = &now
	return nil
}

func (f *fakeChatService) FailTurn(ctx context.Context, turnID uuid.UUID, errorMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return repo.ErrNotFound
	}
	f.failCalls++
	turn.Status = "failed"
	now := time.Now().UTC()
	turn.CompletedAt = &now
	return nil
}

func (f *fakeChatService) GetTurn(ctx context.Context, turnID uuid.UUID) (*chat.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[turnID]
	if turn == nil {
		return nil, repo.ErrNotFound
	}
	copyTurn := *turn
	return &copyTurn, nil
}

func (f *fakeChatService) ListParticipants(ctx context.Context, sessionID uuid.UUID) ([]*chat.ChatParticipant, error) {
	if sessionID != f.session.ID {
		return nil, repo.ErrNotFound
	}
	result := make([]*chat.ChatParticipant, 0, len(f.participants))
	for _, item := range f.participants {
		copyItem := *item
		result = append(result, &copyItem)
	}
	return result, nil
}

func (f *fakeChatService) AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error) {
	item := repo.ChatMessage{
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
		Role:          strings.TrimSpace(input.Role),
		Content:       input.Content,
		ContentFormat: "text",
		Status:        "pending",
		Metadata:      input.Metadata,
	}
	if input.AuthorType != nil {
		item.AuthorType = input.AuthorType
	}
	if input.AuthorID != nil {
		item.AuthorID = input.AuthorID
	}
	if input.ToolCallID != nil {
		item.ToolCallID = input.ToolCallID
	}
	created := f.messages.create(item)
	copyItem := chat.ChatMessage(created)
	return &copyItem, nil
}

func (f *fakeChatService) UpdateMessageStatus(ctx context.Context, messageID uuid.UUID, newStatus, errorMsg string) error {
	_, err := f.messages.UpdateStatus(ctx, messageID, newStatus, errorMsg)
	return err
}

func (f *fakeChatService) waitForTurnID(t *testing.T) uuid.UUID {
	t.Helper()
	select {
	case id := <-f.turnCh:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for turn id")
		return uuid.Nil
	}
}

func (f *fakeChatService) waitForTurnIDOptional() uuid.UUID {
	select {
	case id := <-f.turnCh:
		return id
	default:
		return uuid.Nil
	}
}

func (f *fakeChatService) turnByID(id uuid.UUID) *chat.ChatTurn {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn, ok := f.turns[id]
	if !ok {
		return nil
	}
	copyTurn := *turn
	return &copyTurn
}

func (f *fakeChatService) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]repo.ChatTurn, 0, len(f.turnOrder))
	for _, id := range f.turnOrder {
		turn := f.turns[id]
		if turn == nil || turn.SessionID != sessionID {
			continue
		}
		items = append(items, repo.ChatTurn(*turn))
	}
	return items, nil
}

func (f *fakeChatService) Create(ctx context.Context, turn repo.ChatTurn) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copyTurn := chat.ChatTurn(turn)
	if copyTurn.ID == uuid.Nil {
		copyTurn.ID = uuid.New()
	}
	f.turns[copyTurn.ID] = &copyTurn
	f.turnOrder = append(f.turnOrder, copyTurn.ID)
	select {
	case f.turnCh <- copyTurn.ID:
	default:
	}
	return repo.ChatTurn(copyTurn), nil
}

func (f *fakeChatService) UpdateCurrentTurn(ctx context.Context, id uuid.UUID, currentTurnID *uuid.UUID) (repo.ChatSession, error) {
	if id != f.session.ID {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	f.session.CurrentTurnID = currentTurnID
	return repo.ChatSession(*f.session), nil
}

func (f *fakeChatService) IncrementCounts(ctx context.Context, id uuid.UUID, turnDelta, messageDelta int) (repo.ChatSession, error) {
	if id != f.session.ID {
		return repo.ChatSession{}, repo.ErrNotFound
	}
	f.session.TurnCount += turnDelta
	f.session.MessageCount += messageDelta
	return repo.ChatSession(*f.session), nil
}

type fakeMessageRepo struct {
	mu      sync.Mutex
	items   map[uuid.UUID]repo.ChatMessage
	order   []uuid.UUID
	nextSeq int64
}

func newFakeMessageRepo() *fakeMessageRepo {
	return &fakeMessageRepo{items: map[uuid.UUID]repo.ChatMessage{}, nextSeq: 1}
}

func (f *fakeMessageRepo) create(message repo.ChatMessage) repo.ChatMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	if message.ID == uuid.Nil {
		message.ID = uuid.New()
	}
	if message.SequenceNumber == 0 {
		message.SequenceNumber = f.nextSeq
		f.nextSeq++
	}
	if strings.TrimSpace(message.Status) == "" {
		message.Status = "pending"
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	message.UpdatedAt = time.Now().UTC()
	f.items[message.ID] = message
	f.order = append(f.order, message.ID)
	return message
}

func (f *fakeMessageRepo) upsert(message repo.ChatMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.items[message.ID]; !ok {
		f.order = append(f.order, message.ID)
	}
	f.items[message.ID] = message
}

func (f *fakeMessageRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeMessageRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := make([]repo.ChatMessage, 0)
	for _, id := range f.order {
		item := f.items[id]
		if item.SessionID == sessionID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SequenceNumber < items[j].SequenceNumber })
	return items, nil
}

func (f *fakeMessageRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage string) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Status = strings.TrimSpace(status)
	if strings.TrimSpace(errorMessage) != "" {
		msg := strings.TrimSpace(errorMessage)
		item.ErrorMessage = &msg
	} else {
		item.ErrorMessage = nil
	}
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) UpdateContent(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Content = content
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) UpdateMetadata(ctx context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item, ok := f.items[id]
	if !ok {
		return repo.ChatMessage{}, repo.ErrNotFound
	}
	item.Metadata = metadata
	item.UpdatedAt = time.Now().UTC()
	f.items[id] = item
	return item, nil
}

func (f *fakeMessageRepo) containsContent(content string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if strings.TrimSpace(item.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}

func (f *fakeMessageRepo) containsStatus(role, status string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, item := range f.items {
		if strings.EqualFold(item.Role, role) && strings.EqualFold(item.Status, status) {
			return true
		}
	}
	return false
}

type fakeToolResolver struct {
	tools []tools.ToolDescriptor
}

func (f *fakeToolResolver) GetSessionToolSet(ctx context.Context, sessionID, agentID uuid.UUID) ([]tools.ToolDescriptor, error) {
	if len(f.tools) == 0 {
		return []tools.ToolDescriptor{{Name: "file.read", Tier: "tier1"}, {Name: "cli.execute", Tier: "tier2"}}, nil
	}
	return append([]tools.ToolDescriptor(nil), f.tools...), nil
}

type assembleResult struct {
	prompt *prompt.AssembledPrompt
	err    error
}

type fakeAssembler struct {
	mu      sync.Mutex
	results []assembleResult
	calls   int
}

func (f *fakeAssembler) Assemble(ctx context.Context, input prompt.AssemblyInput) (*prompt.AssembledPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if len(f.results) == 0 {
		return &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "default"}}, TotalTokens: 10}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.prompt, result.err
}

type fakeSummarizationChecker struct{}

func (*fakeSummarizationChecker) ShouldSummarize(context.Context, uuid.UUID, int) (bool, error) {
	return false, nil
}

type fakeModelGateway struct {
	streamFn   func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error)
	completeFn func(ctx context.Context, req ModelRequest) (ModelResponse, error)

	streamCalls              int
	listeningEvalCalls       int
	continuationSummaryCalls int
}

func (f *fakeModelGateway) StreamComplete(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
	f.streamCalls++
	if f.streamFn != nil {
		return f.streamFn(ctx, req, onChunk)
	}
	_ = onChunk("ok")
	return ModelResponse{Content: "ok"}, nil
}

func (f *fakeModelGateway) Complete(ctx context.Context, req ModelRequest) (ModelResponse, error) {
	if req.Purpose == "listening_eval" {
		f.listeningEvalCalls++
	}
	if req.Purpose == "continuation_summary" {
		f.continuationSummaryCalls++
	}
	if f.completeFn != nil {
		return f.completeFn(ctx, req)
	}
	return ModelResponse{Content: "respond"}, nil
}

type fakeDispatcher struct {
	tier1Fn func(ctx context.Context, call ToolCall) (ToolResult, error)
	tier2Fn func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error)
}

func (f *fakeDispatcher) DispatchTier1(ctx context.Context, call ToolCall) (ToolResult, error) {
	if f.tier1Fn != nil {
		return f.tier1Fn(ctx, call)
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
}

func (f *fakeDispatcher) DispatchTier2(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
	if f.tier2Fn != nil {
		return f.tier2Fn(ctx, call, onRunStarted)
	}
	runID := uuid.New()
	onRunStarted(runID)
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
}

type fakeRunCanceler struct {
	mu    sync.Mutex
	calls []uuid.UUID
}

func (f *fakeRunCanceler) RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy controlplane.CancelRequestActor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runID)
	return nil
}

type fakeEventBus struct {
	mu     sync.Mutex
	subs   []fakeSubscription
	events []eventbus.DomainEvent
}

type fakeSubscription struct {
	orgID   *uuid.UUID
	handler eventbus.EventHandler
}

func (f *fakeEventBus) Publish(ctx context.Context, tx pgx.Tx, event eventbus.DomainEvent) error {
	f.mu.Lock()
	f.events = append(f.events, event)
	subs := append([]fakeSubscription(nil), f.subs...)
	f.mu.Unlock()
	for _, sub := range subs {
		if sub.handler == nil {
			continue
		}
		if sub.orgID != nil && *sub.orgID != event.OrganizationID {
			continue
		}
		if err := sub.handler(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeEventBus) Subscribe(_ string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fakeSubscription{orgID: orgID, handler: handler})
	return eventbus.Subscription{}
}

func (f *fakeEventBus) Unsubscribe(_ eventbus.Subscription) {}

type fakeEnqueuer struct {
	mu   sync.Mutex
	jobs []enqueuedJob
}

type enqueuedJob struct {
	jobType  string
	payload  *AgentTurnPayload
	runAfter *time.Time
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	entry := enqueuedJob{jobType: strings.TrimSpace(jobType)}
	if runAfter != nil {
		at := *runAfter
		entry.runAfter = &at
	}
	if entry.jobType == AgentTurnJobType {
		typed, ok := payload.(AgentTurnPayload)
		if !ok {
			return uuid.Nil, fmt.Errorf("unexpected payload type %T", payload)
		}
		copyPayload := typed
		entry.payload = &copyPayload
	}
	f.mu.Lock()
	f.jobs = append(f.jobs, entry)
	f.mu.Unlock()
	return uuid.New(), nil
}

func (f *fakeEnqueuer) agentTurnJobs() []enqueuedJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]enqueuedJob, 0)
	for _, job := range f.jobs {
		if job.jobType != AgentTurnJobType {
			continue
		}
		copyJob := job
		if job.payload != nil {
			payload := *job.payload
			copyJob.payload = &payload
		}
		if job.runAfter != nil {
			runAfter := *job.runAfter
			copyJob.runAfter = &runAfter
		}
		out = append(out, copyJob)
	}
	return out
}

type fakeInvocationRepo struct {
	mu      sync.Mutex
	creates []repo.ModelInvocation
}

func (f *fakeInvocationRepo) Create(ctx context.Context, invocation repo.ModelInvocation) (repo.ModelInvocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	invocation.ID = uuid.New()
	f.creates = append(f.creates, invocation)
	return invocation, nil
}

func (f *fakeInvocationRepo) UpdateStatus(context.Context, uuid.UUID, string, *string, *string) (repo.ModelInvocation, error) {
	return repo.ModelInvocation{}, nil
}

func (f *fakeInvocationRepo) UpdateCompletion(context.Context, uuid.UUID, int, int, int, int, int, *string, *string) error {
	return nil
}

type fakeModelProfileRepo struct {
	profile repo.ModelProfile
}

func (f *fakeModelProfileRepo) GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	if strings.TrimSpace(logicalProfileID) == "" {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	copyProfile := f.profile
	copyProfile.LogicalProfileID = logicalProfileID
	return copyProfile, nil
}

type fakeProfileResolver struct {
	profile repo.ModelProfile
}

func (f *fakeProfileResolver) Resolve(ctx context.Context, orgID uuid.UUID, purpose string, scopes ...model.Scope) (*repo.ModelProfile, error) {
	copyProfile := f.profile
	return &copyProfile, nil
}

type fakeAgentRepo struct {
	agent repo.Agent
}

func (f *fakeAgentRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.agent.ID != id {
		return repo.Agent{}, repo.ErrNotFound
	}
	return f.agent, nil
}

type fakeTaskRepo struct{}

func (*fakeTaskRepo) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	return repo.ProjectTask{}, repo.ErrNotFound
}

type fakeMemorySourceRepo struct {
	sources []repo.MemorySource
}

func (f *fakeMemorySourceRepo) ListBySession(ctx context.Context, sessionID uuid.UUID) ([]repo.MemorySource, error) {
	return append([]repo.MemorySource(nil), f.sources...), nil
}

type fakeMemoryRepo struct {
	mu          sync.Mutex
	items       map[uuid.UUID]repo.Memory
	updateCalls int
}

func (f *fakeMemoryRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[uuid.UUID]repo.Memory{}
	}
	item, ok := f.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeMemoryRepo) UpdateConfidence(ctx context.Context, id uuid.UUID, confidence float64) (repo.Memory, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	item, ok := f.items[id]
	if !ok {
		return repo.Memory{}, repo.ErrNotFound
	}
	item.Confidence = confidence
	f.items[id] = item
	return item, nil
}
