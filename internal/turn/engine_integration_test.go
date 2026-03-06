//go:build integration

package turn

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
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

func TestTurnEngineIntegrationRunAttributionUpdatesRunTokenTotals(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	runRepo := controlplane.NewRunRepository(fixture.pool)
	stepRepo := controlplane.NewRunStepRepository(fixture.pool)
	attemptRepo := controlplane.NewRunAttemptRepository(fixture.pool)

	runRecord, err := runRepo.Create(ctx, controlplane.Run{
		OrganizationID: fixture.org.ID,
		PrincipalType:  "agent",
		PrincipalID:    fixture.agent.ID,
		TriggerType:    "api",
		Status:         "created",
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	step, err := stepRepo.Create(ctx, controlplane.RunStep{
		RunID:      runRecord.ID,
		StepNumber: 1,
		Status:     "pending",
		Metadata:   []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run step: %v", err)
	}
	attempt, err := attemptRepo.Create(ctx, controlplane.RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "pending",
		Metadata:      []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create run attempt: %v", err)
	}

	msgMeta := mustJSON(t, map[string]any{
		"run_id":         runRecord.ID.String(),
		"run_step_id":    step.ID.String(),
		"run_attempt_id": attempt.ID.String(),
	})
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateMetadata(ctx, fixture.userMessage.ID, msgMeta); err != nil {
		t.Fatalf("set user message metadata: %v", err)
	}

	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{
			Content: "ok",
			Usage: &ModelUsage{
				InputTokens:  120,
				OutputTokens: 30,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	updatedRun, err := runRepo.Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updatedRun.InputTokens != 120 || updatedRun.OutputTokens != 30 {
		t.Fatalf("run token totals = (%d,%d), want (120,30)", updatedRun.InputTokens, updatedRun.OutputTokens)
	}
	updatedStep, err := stepRepo.Get(ctx, step.ID)
	if err != nil {
		t.Fatalf("get run step: %v", err)
	}
	if updatedStep.InputTokens != 120 || updatedStep.OutputTokens != 30 {
		t.Fatalf("run_step token totals = (%d,%d), want (120,30)", updatedStep.InputTokens, updatedStep.OutputTokens)
	}
	updatedAttempt, err := attemptRepo.Get(ctx, attempt.ID)
	if err != nil {
		t.Fatalf("get run attempt: %v", err)
	}
	if updatedAttempt.InputTokens != 120 || updatedAttempt.OutputTokens != 30 {
		t.Fatalf("run_attempt token totals = (%d,%d), want (120,30)", updatedAttempt.InputTokens, updatedAttempt.OutputTokens)
	}

	rows, err := repo.NewModelInvocationRepo(fixture.pool).ListBySession(ctx, fixture.org.ID, fixture.session.ID)
	if err != nil {
		t.Fatalf("list invocations: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one model invocation row")
	}
	latest := rows[0]
	if latest.RunID == nil || *latest.RunID != runRecord.ID {
		t.Fatalf("invocation run_id = %v, want %s", latest.RunID, runRecord.ID)
	}
	if latest.RunStepID == nil || *latest.RunStepID != step.ID {
		t.Fatalf("invocation run_step_id = %v, want %s", latest.RunStepID, step.ID)
	}
	if latest.RunAttemptID == nil || *latest.RunAttemptID != attempt.ID {
		t.Fatalf("invocation run_attempt_id = %v, want %s", latest.RunAttemptID, attempt.ID)
	}
}

func TestTurnEngineIntegrationCancelConsumerCursorReusedAcrossTurns(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage first turn: %v", err)
	}
	authorType := "human_user"
	secondMessage, err := fixture.chatService.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID:  fixture.session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "second",
	})
	if err != nil {
		t.Fatalf("AppendMessage second user: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, secondMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage second turn: %v", err)
	}

	var count int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM consumer_cursor
		WHERE organization_id = $1
		  AND consumer_name LIKE 'turn-engine.cancel.%'
	`, fixture.org.ID).Scan(&count); err != nil {
		t.Fatalf("count turn cancel cursors: %v", err)
	}
	if count != 1 {
		t.Fatalf("turn cancel consumer_cursor rows = %d, want 1", count)
	}
}

func TestTurnEngineIntegrationTaskSessionEventRoutesJobToAssignedAgent(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	assignedAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	frank := mustCreateStarterFrank(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, assignedAgent.ID)

	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", frank.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "route this message",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	actorID := fixture.user.ID
	if err := fixture.engine.HandleUserMessageEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.message.user_sent",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "message_id": userMessage.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	var rawPayload []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE job_type = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, AgentTurnJobType).Scan(&rawPayload); err != nil {
		t.Fatalf("load agent_turn job payload: %v", err)
	}

	var payload AgentTurnPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatalf("unmarshal agent_turn payload: %v", err)
	}
	if payload.AgentID == nil {
		t.Fatalf("payload.agent_id = nil, want %s", assignedAgent.ID)
	}
	if *payload.AgentID != assignedAgent.ID {
		t.Fatalf("payload.agent_id = %s, want %s", *payload.AgentID, assignedAgent.ID)
	}
}

func TestTurnEngineIntegrationProjectSessionEventRoutesJobToPMAndAddsParticipant(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	pmAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	frank := mustCreateStarterFrank(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, pmAgent.ID, fixture.user.ID)

	projectSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession project scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, projectSession.ID, "agent", frank.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  projectSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "hello pm",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	actorID := fixture.user.ID
	if err := fixture.engine.HandleUserMessageEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.message.user_sent",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "message_id": userMessage.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	var rawPayload []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE job_type = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, AgentTurnJobType).Scan(&rawPayload); err != nil {
		t.Fatalf("load agent_turn job payload: %v", err)
	}

	var payload AgentTurnPayload
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatalf("unmarshal agent_turn payload: %v", err)
	}
	if payload.AgentID == nil {
		t.Fatalf("payload.agent_id = nil, want %s", pmAgent.ID)
	}
	if *payload.AgentID != pmAgent.ID {
		t.Fatalf("payload.agent_id = %s, want %s", *payload.AgentID, pmAgent.ID)
	}

	participants, err := fixture.chatService.ListParticipants(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasPM := false
	for _, participant := range participants {
		if participant != nil && participant.ParticipantType == "agent" && participant.ParticipantID == pmAgent.ID {
			hasPM = true
			break
		}
	}
	if !hasPM {
		t.Fatalf("expected PM %s in session participants", pmAgent.ID)
	}
}

func TestTurnEngineIntegrationAsyncExecutionSessionSingleMessageCreatesSingleTurn(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	assignedAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, assignedAgent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}

	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "run exactly once",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("once"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "once"}, nil
	}

	actorID := fixture.user.ID
	event := eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.message.user_sent",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "message_id": userMessage.ID.String()}),
	}
	if err := fixture.engine.HandleUserMessageEvent(ctx, event); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	jobs := loadAgentTurnJobsForMessage(t, ctx, fixture.pool, taskSession.ID, userMessage.ID)
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	for _, job := range jobs {
		if err := fixture.engine.HandleTurnJob(ctx, job); err != nil {
			t.Fatalf("HandleTurnJob: %v", err)
		}
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1", len(turns))
	}
	if turns[0].TriggerMessageID == nil || *turns[0].TriggerMessageID != userMessage.ID {
		t.Fatalf("trigger_message_id = %v, want %s", turns[0].TriggerMessageID, userMessage.ID)
	}
	if turns[0].RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", turns[0].RetryCount)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if got := countAssistantFinalMessages(messages); got != 1 {
		t.Fatalf("assistant final messages = %d, want 1", got)
	}
}

func TestTurnEngineIntegrationAsyncExecutionSessionDuplicateDeliveryDoesNotCreateSecondTurn(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	assignedAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, assignedAgent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}

	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "do not duplicate",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("single"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "single"}, nil
	}

	actorID := fixture.user.ID
	event := eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.message.user_sent",
		ActorType:      "human",
		ActorID:        &actorID,
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "message_id": userMessage.ID.String()}),
	}
	for i := 0; i < 2; i++ {
		if err := fixture.engine.HandleUserMessageEvent(ctx, event); err != nil {
			t.Fatalf("HandleUserMessageEvent[%d]: %v", i, err)
		}
	}

	jobs := loadAgentTurnJobsForMessage(t, ctx, fixture.pool, taskSession.ID, userMessage.ID)
	if len(jobs) != 2 {
		t.Fatalf("agent_turn jobs = %d, want 2 duplicate deliveries", len(jobs))
	}
	for i, job := range jobs {
		if err := fixture.engine.HandleTurnJob(ctx, job); err != nil {
			t.Fatalf("HandleTurnJob[%d]: %v", i, err)
		}
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1", len(turns))
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if got := countAssistantFinalMessages(messages); got != 1 {
		t.Fatalf("assistant final messages = %d, want 1", got)
	}
}

func TestTurnEngineIntegrationAsyncExecutionSessionRetryAttemptIsDistinctFromDuplicateDelivery(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	assignedAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, assignedAgent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}

	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "retry me distinctly",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		token := "retry-0"
		if req.TurnID != uuid.Nil {
			token = "retry-" + req.TurnID.String()[:8]
		}
		if err := onChunk(token); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: token}, nil
	}

	firstJob := jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: mustJSON(t, AgentTurnPayload{
			SessionID: taskSession.ID,
			MessageID: userMessage.ID,
		}),
	}
	duplicateJob := jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: mustJSON(t, AgentTurnPayload{
			SessionID: taskSession.ID,
			MessageID: userMessage.ID,
		}),
	}
	retryJob := jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: mustJSON(t, AgentTurnPayload{
			SessionID:  taskSession.ID,
			MessageID:  userMessage.ID,
			RetryCount: 1,
		}),
	}

	if err := fixture.engine.HandleTurnJob(ctx, firstJob); err != nil {
		t.Fatalf("HandleTurnJob first: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(ctx, duplicateJob); err != nil {
		t.Fatalf("HandleTurnJob duplicate: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(ctx, retryJob); err != nil {
		t.Fatalf("HandleTurnJob retry: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turn count = %d, want 2", len(turns))
	}
	if turns[0].RetryCount != 0 || turns[1].RetryCount != 1 {
		t.Fatalf("retry counts = [%d %d], want [0 1]", turns[0].RetryCount, turns[1].RetryCount)
	}
	if turns[0].TriggerMessageID == nil || turns[1].TriggerMessageID == nil {
		t.Fatal("trigger_message_id should be set on both turns")
	}
	if *turns[0].TriggerMessageID != userMessage.ID || *turns[1].TriggerMessageID != userMessage.ID {
		t.Fatalf("trigger_message_ids = [%v %v], want %s", turns[0].TriggerMessageID, turns[1].TriggerMessageID, userMessage.ID)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	if got := countAssistantFinalMessages(messages); got != 2 {
		t.Fatalf("assistant final messages = %d, want 2", got)
	}
	if !containsSystemMessage(messages, "[Retry attempt 1 started.]") {
		t.Fatal("missing visible retry state marker")
	}
}

func TestTurnEngineIntegrationProjectCreateConflictThenSuccessLocksIdentity(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	projectID := uuid.New()
	projectSlug := "sam-blog-v2"

	dispatched := make([]string, 0, 3)
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = append(dispatched, call.ID)
		switch call.ID {
		case "create-conflict":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "slug_conflict_archive_or_reuse_required",
			}, nil
		case "create-success":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"project": map[string]any{
						"id":   projectID,
						"slug": projectSlug,
						"name": "Sam Blog V2",
					},
				},
			}, nil
		case "create-reopen":
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "unexpected_reopen_dispatch",
			}, nil
		default:
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
		}
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-conflict", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog", "slug": "sam-blog"}}}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-success", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog V2", "slug": projectSlug}}}}, nil
		case 3:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-reopen", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog V2", "slug": projectSlug}}}}, nil
		default:
			return ModelResponse{Content: "Using project sam-blog-v2 for kickoff."}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if strings.Join(dispatched, ",") != "create-conflict,create-success" {
		t.Fatalf("dispatched project.create calls = %v, want [create-conflict create-success]", dispatched)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	wantLock := "Project identity locked: slug=" + projectSlug + " project_id=" + projectID.String()
	hasLock := false
	hasBlockedReopen := false
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") && strings.Contains(message.Content, wantLock) {
			hasLock = true
		}
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "create-reopen" &&
			strings.Contains(message.Content, "project already created in this flow") {
			hasBlockedReopen = true
		}
	}
	if !hasLock {
		t.Fatalf("missing project identity lock system message containing %q", wantLock)
	}
	if !hasBlockedReopen {
		t.Fatal("missing blocked project.create tool_result for create-reopen call")
	}
}

func TestTurnEngineIntegrationProjectKickoffResponderOrderFrankThenLori(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	frank := fixture.agent
	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)

	projectSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        project.ID,
		Mode:           "sync",
	})
	if err != nil {
		t.Fatalf("CreateSession project scope: %v", err)
	}

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("kickoff"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "kickoff"}, nil
	}

	authorType := "human_user"
	firstUserMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  projectSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "Create kickoff handoff summary for this project.",
	})
	if err != nil {
		t.Fatalf("AppendMessage first project user message: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(ctx, projectSession.ID, firstUserMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage first project turn: %v", err)
	}

	secondUserMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  projectSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "Continue kickoff staffing planning.",
	})
	if err != nil {
		t.Fatalf("AppendMessage second project user message: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(ctx, projectSession.ID, secondUserMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage second project turn: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("project turn count = %d, want 2", len(turns))
	}
	if turns[0].RespondingID != frank.ID {
		t.Fatalf("first project turn responding_id = %s, want Frank %s", turns[0].RespondingID, frank.ID)
	}
	if turns[1].RespondingID != lori.ID {
		t.Fatalf("second project turn responding_id = %s, want Lori %s", turns[1].RespondingID, lori.ID)
	}
	if turns[0].RespondingID == turns[1].RespondingID {
		t.Fatalf("project kickoff responder repeated %s before handoff", turns[0].RespondingID)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	assistantAuthors := make([]uuid.UUID, 0, 2)
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Status), "final") {
			continue
		}
		if message.AuthorID == nil || *message.AuthorID == uuid.Nil {
			continue
		}
		assistantAuthors = append(assistantAuthors, *message.AuthorID)
	}
	if len(assistantAuthors) < 2 {
		t.Fatalf("assistant project message authors = %v, want [Frank Lori]", assistantAuthors)
	}
	if assistantAuthors[0] != frank.ID {
		t.Fatalf("first assistant project author = %s, want Frank %s", assistantAuthors[0], frank.ID)
	}
	if assistantAuthors[1] != lori.ID {
		t.Fatalf("second assistant project author = %s, want Lori %s", assistantAuthors[1], lori.ID)
	}
}

func TestTurnEngineIntegrationKickoffSummaryCarriesOriginatingWorkstreams(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	workstreamRequest := "Create project sam-blog-v2 with workstreams for landing page, Stripe billing integration, and analytics dashboard setup."
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateContent(ctx, fixture.userMessage.ID, workstreamRequest); err != nil {
		t.Fatalf("UpdateContent originating user request: %v", err)
	}
	fixture.engine.assembler = &sessionHistoryAssembler{messages: repo.NewChatMessageRepo(fixture.pool)}

	projectID := uuid.New()
	projectSlug := "sam-blog-v2"
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		if call.ID != "create-1" {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Error:      "unexpected_tool_call",
			}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"project": map[string]any{
					"id":   projectID,
					"slug": projectSlug,
					"name": "Sam Blog V2",
				},
			},
		}, nil
	}

	round := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "create-1", Name: "project.create", Tier: "tier1", Arguments: map[string]any{
					"name": projectSlug,
					"slug": projectSlug,
				}},
			}}, nil
		}
		promptText := strings.Builder{}
		for _, message := range req.Prompt.Messages {
			promptText.WriteString(strings.ToLower(strings.TrimSpace(message.Content)))
			promptText.WriteString("\n")
		}
		promptBlob := promptText.String()
		if strings.Contains(promptBlob, "kickoff handoff requirement") &&
			strings.Contains(promptBlob, "landing page") &&
			strings.Contains(promptBlob, "stripe billing integration") &&
			strings.Contains(promptBlob, "analytics dashboard setup") {
			return ModelResponse{Content: "Handoff to Lori: workstreams are landing page, Stripe billing integration, and analytics dashboard setup."}, nil
		}
		return ModelResponse{Content: "Handoff to Lori: missing required workstreams."}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	finalAssistant := ""
	for _, item := range messages {
		if !strings.EqualFold(strings.TrimSpace(item.Role), "assistant") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Status), "final") {
			continue
		}
		finalAssistant = strings.TrimSpace(item.Content)
	}
	if !strings.Contains(strings.ToLower(finalAssistant), "landing page") ||
		!strings.Contains(strings.ToLower(finalAssistant), "stripe billing integration") ||
		!strings.Contains(strings.ToLower(finalAssistant), "analytics dashboard setup") {
		t.Fatalf("kickoff handoff summary missing originating workstreams: %q", finalAssistant)
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

func TestTurnEngineIntegrationMaxToolCallsSetsStopReason(t *testing.T) {
	fixture := newIntegrationFixture(t)
	fixture.engine.maxToolCalls = 1
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: "tool-1", Name: "memory.query", Tier: "tier1"},
			{ID: "tool-2", Name: "memory.query", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected at least one turn")
	}
	last := turns[len(turns)-1]
	if last.StopReason == nil || *last.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("turn stop_reason = %v, want %q", last.StopReason, stopReasonMaxToolCalls)
	}
}

func TestTurnEngineIntegrationContinuationRecoversFromExternallyCompletedTurn(t *testing.T) {
	fixture := newIntegrationFixture(t)

	assembler := &fakeAssembler{
		results: []assembleResult{
			{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
			{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
		},
	}
	var completeErr error
	var completeOnce sync.Once
	assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 {
			return
		}
		completeOnce.Do(func() {
			completeErr = fixture.chatService.CompleteTurn(context.Background(), input.TurnID)
		})
	}
	fixture.engine.assembler = assembler
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

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if completeErr != nil {
		t.Fatalf("external CompleteTurn err = %v, want nil", completeErr)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(turns))
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("last turn status = %s, want completed", turns[len(turns)-1].Status)
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

func TestTurnEngineIntegrationWorkerDefaultsStandardAndEscalatesAfterTransientFailure(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	if _, err := fixture.pool.Exec(ctx, `UPDATE agent SET agent_type = 'worker' WHERE id = $1`, fixture.agent.ID); err != nil {
		t.Fatalf("set fixture agent type worker: %v", err)
	}
	fixture.engine.resolver = nil

	providerID := mustFirstProviderID(t, ctx, fixture.pool)
	mustCreateNamedProfile(t, ctx, fixture.pool, fixture.org.ID, providerID, "standard")
	mustCreateNamedProfile(t, ctx, fixture.pool, fixture.org.ID, providerID, "high-capability")

	attempt := 0
	streamProfiles := make([]string, 0, 2)
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		streamProfiles = append(streamProfiles, strings.TrimSpace(req.Profile.LogicalProfileID))
		attempt++
		if attempt == 1 {
			return ModelResponse{}, ErrModelTransient
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(streamProfiles) < 2 {
		t.Fatalf("stream profile calls = %v, want at least 2 calls", streamProfiles)
	}
	if streamProfiles[0] != "standard" {
		t.Fatalf("first profile = %q, want %q", streamProfiles[0], "standard")
	}
	if streamProfiles[1] != "high-capability" {
		t.Fatalf("second profile = %q, want %q", streamProfiles[1], "high-capability")
	}
}

func TestTurnEngineIntegrationPromptGuardrailPreventsRunawayInput(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	if _, err := fixture.pool.Exec(ctx, `UPDATE agent SET agent_type = 'worker' WHERE id = $1`, fixture.agent.ID); err != nil {
		t.Fatalf("set fixture agent type worker: %v", err)
	}
	fixture.engine.resolver = nil

	providerID := mustFirstProviderID(t, ctx, fixture.pool)
	mustCreateNamedProfile(t, ctx, fixture.pool, fixture.org.ID, providerID, "standard")
	mustCreateNamedProfile(t, ctx, fixture.pool, fixture.org.ID, providerID, "high-capability")

	fixture.engine.assembler = &fakeAssembler{
		results: []assembleResult{
			{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "oversized"}}, TotalTokens: workerPromptTokenGuardrail + 1000}},
			{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "small"}}, TotalTokens: 64}},
		},
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
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1 after guardrail continuation", fixture.model.streamCalls)
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
	pool        *pgxpool.Pool
	org         repo.Organization
	user        repo.HumanUser
	agent       repo.Agent
	session     repo.ChatSession
	userMessage repo.ChatMessage

	chatService chat.ChatService
	bus         *eventbus.Bus
	engine      *TurnEngine
	model       *fakeModelGateway
	dispatcher  *fakeDispatcher
	runCanceler *fakeRunCanceler
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)

	org := mustCreateOrg(t, ctx, pool)
	user := mustCreateUser(t, ctx, pool, org.ID)
	agent := mustCreateStarterFrank(t, ctx, pool, org.ID)
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
		chatService: chatService,
		bus:         bus,
		engine:      engine,
		model:       modelGateway,
		dispatcher:  dispatcher,
		runCanceler: runCanceler,
	}
}

type sessionHistoryAssembler struct {
	messages *repo.ChatMessageRepo
}

func (a *sessionHistoryAssembler) Assemble(ctx context.Context, input prompt.AssemblyInput) (*prompt.AssembledPrompt, error) {
	if a == nil || a.messages == nil {
		return &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "history unavailable"}}, TotalTokens: 16}, nil
	}
	rows, err := a.messages.ListBySession(ctx, input.SessionID)
	if err != nil {
		return nil, err
	}
	promptMessages := make([]prompt.PromptMessage, 0, len(rows))
	for _, row := range rows {
		role := strings.TrimSpace(row.Role)
		if role == "" {
			continue
		}
		promptMessages = append(promptMessages, prompt.PromptMessage{
			Role:    role,
			Content: strings.TrimSpace(row.Content),
		})
	}
	if len(promptMessages) == 0 {
		promptMessages = append(promptMessages, prompt.PromptMessage{Role: "system", Content: "history unavailable"})
	}
	return &prompt.AssembledPrompt{Messages: promptMessages, TotalTokens: 64}, nil
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

func countAssistantFinalMessages(messages []repo.ChatMessage) int {
	count := 0
	for _, item := range messages {
		if item.Role == "assistant" && item.Status == "final" {
			count++
		}
	}
	return count
}

func containsSystemMessage(messages []repo.ChatMessage, content string) bool {
	for _, item := range messages {
		if item.Role == "system" && strings.TrimSpace(item.Content) == strings.TrimSpace(content) {
			return true
		}
	}
	return false
}

func loadAgentTurnJobsForMessage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID, messageID uuid.UUID) []jobqueue.Job {
	t.Helper()

	rows, err := pool.Query(ctx, `
		SELECT payload
		FROM job_queue
		WHERE job_type = $1
		  AND payload->>'session_id' = $2
		  AND payload->>'message_id' = $3
		ORDER BY created_at ASC, id ASC
	`, AgentTurnJobType, sessionID.String(), messageID.String())
	if err != nil {
		t.Fatalf("query agent_turn jobs: %v", err)
	}
	defer rows.Close()

	jobs := make([]jobqueue.Job, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan agent_turn payload: %v", err)
		}
		jobs = append(jobs, jobqueue.Job{
			JobType: AgentTurnJobType,
			Payload: payload,
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate agent_turn jobs: %v", err)
	}
	return jobs
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
		Slug:        "turn-provider-" + uuid.NewString()[:8],
		DisplayName: "Turn Provider",
		APIBaseURL:  "https://example.invalid",
		IsEnabled:   true,
		Metadata:    []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return item
}

func mustFirstProviderID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var providerID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT id
		FROM model_provider
		ORDER BY created_at
		LIMIT 1
	`).Scan(&providerID); err != nil {
		t.Fatalf("select first model provider: %v", err)
	}
	return providerID
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

func mustCreateNamedProfile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, providerID uuid.UUID, logicalProfileID string) repo.ModelProfile {
	t.Helper()
	item, err := repo.NewModelProfileRepo(pool).Create(ctx, repo.ModelProfile{
		LogicalProfileID:    logicalProfileID,
		OrganizationID:      &orgID,
		Version:             1,
		IsCurrent:           true,
		ProviderID:          providerID,
		ModelName:           "test-model-" + strings.ReplaceAll(strings.TrimSpace(logicalProfileID), " ", "-"),
		DisplayName:         "Test Model " + strings.TrimSpace(logicalProfileID),
		ContextWindowTokens: 131072,
		MaxOutputTokens:     4096,
		SupportsStreaming:   true,
		SupportsVision:      false,
		InvocationPurpose:   "agent_turn",
	})
	if err != nil {
		t.Fatalf("create %q profile: %v", logicalProfileID, err)
	}
	return item
}

func mustCreateStarterFrank(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	item, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Frank",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are Frank.",
		OperatorInstructions: "",
		AgentType:            "general",
		IsStarterTrio:        true,
		PrivateMemory:        false,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create Frank starter agent: %v", err)
	}
	return item
}

func mustCreateStarterLori(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()
	item, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Lori",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are Lori.",
		OperatorInstructions: "",
		AgentType:            "pm",
		IsStarterTrio:        true,
		PrivateMemory:        false,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create Lori starter agent: %v", err)
	}
	return item
}

func mustCreateProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, userID uuid.UUID) repo.Project {
	t.Helper()
	project, err := repo.NewProjectRepo(pool).Create(ctx, repo.Project{
		OrganizationID: orgID,
		Slug:           "turn-project-" + uuid.NewString()[:8],
		DisplayName:    "Turn Project",
		Description:    "",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    userID,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func mustCreateExecutionFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, userID uuid.UUID) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "turn-flow-" + uuid.NewString()[:8],
		DisplayName:    "Turn Flow",
		Description:    "Turn engine test flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    userID,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create work flow node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create review flow node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create merge flow node: %v", err)
	}

	workNode.NextNodeID = &reviewNode.ID
	reviewNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("update work flow node: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("update review flow node: %v", err)
	}

	template.StartNodeID = &workNode.ID
	template, err = templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("update flow template start node: %v", err)
	}
	return template
}

func mustCreateTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, userID, assignedAgentID uuid.UUID) repo.ProjectTask {
	t.Helper()
	assigned := assignedAgentID
	createdBy := userID
	flowTemplate := mustCreateExecutionFlowTemplate(t, ctx, pool, orgID, projectID, userID)
	taskRecord, err := repo.NewProjectTaskRepo(pool).Create(ctx, repo.ProjectTask{
		OrganizationID:  orgID,
		ProjectID:       projectID,
		Title:           "Route me",
		WorkStatus:      "in_progress",
		FlowTemplateID:  &flowTemplate.ID,
		CreatedByType:   "human_user",
		CreatedByID:     &createdBy,
		AssignedAgentID: &assigned,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskRecord
}

func mustAssignProjectPM(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID, agentID, userID uuid.UUID) repo.AgentProjectAssignment {
	t.Helper()
	assignment, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agentID,
		ProjectID:      projectID,
		Role:           "pm",
		AssignedByType: "human_user",
		AssignedByID:   &userID,
	})
	if err != nil {
		t.Fatalf("assign project PM: %v", err)
	}
	return assignment
}
