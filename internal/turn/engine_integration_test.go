//go:build integration

package turn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
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
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1 active dispatch", len(jobs))
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

func TestTurnEngineIntegrationTaskRecoveryTurnUsesAssignedAgentAndTaskContext(t *testing.T) {
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
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		Role:      "user",
		Content:   "supervisor recovery: resume task",
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery: %v", err)
	}

	var dispatched ToolCall
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		dispatched = call
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}, RunID: &runID}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:        "recovery-cli",
					Name:      "cli.execute",
					Tier:      "tier2",
					Arguments: map[string]any{"command": "echo recovered"},
				}},
			}, nil
		}
		return ModelResponse{Content: "recovered"}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if got := dispatched.Arguments["task_id"]; got != taskRecord.ID.String() {
		t.Fatalf("task_id = %v, want %s", got, taskRecord.ID)
	}
	if got := dispatched.Arguments["project_id"]; got != project.ID.String() {
		t.Fatalf("project_id = %v, want %s", got, project.ID)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected recovery turn")
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.RespondingID != assignedAgent.ID {
		t.Fatalf("turn responding_id = %s, want %s", lastTurn.RespondingID, assignedAgent.ID)
	}
}

func TestTurnEngineIntegrationCancelledMessageSuppressesLateClaimedRetryJob(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	session, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "organization",
		ScopeID:        fixture.org.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession async: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, session.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "cancel the queued retry",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	turnRepo := repo.NewChatTurnRepo(fixture.pool)
	sessionRepo := repo.NewChatSessionRepo(fixture.pool)
	startedAt := time.Now().UTC()
	activeCycleID := uuid.New()
	activeTurn, err := turnRepo.Create(ctx, repo.ChatTurn{
		SessionID:        session.ID,
		TurnNumber:       1,
		CycleID:          &activeCycleID,
		RespondingType:   "agent",
		RespondingID:     fixture.agent.ID,
		Status:           "in_progress",
		StartedAt:        &startedAt,
		TriggerMessageID: &userMessage.ID,
		RetryCount:       0,
	})
	if err != nil {
		t.Fatalf("create active turn: %v", err)
	}
	if _, err := sessionRepo.UpdateCurrentTurn(ctx, session.ID, &activeTurn.ID); err != nil {
		t.Fatalf("UpdateCurrentTurn: %v", err)
	}

	worker := jobqueue.New(fixture.pool, nil, jobqueue.Config{})
	jobID, err := worker.Enqueue(ctx, nil, AgentTurnJobType, 70, AgentTurnPayload{
		SessionID:  session.ID,
		MessageID:  userMessage.ID,
		RetryCount: 1,
	}, nil)
	if err != nil {
		t.Fatalf("enqueue retry: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'integration-worker',
		    claimed_at = now()
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("mark retry job claimed: %v", err)
	}

	var payload []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&payload); err != nil {
		t.Fatalf("load claimed job payload: %v", err)
	}
	claimedJob := jobqueue.Job{
		ID:      jobID,
		JobType: AgentTurnJobType,
		Payload: payload,
		Status:  "claimed",
	}

	if err := fixture.chatService.CancelTurn(ctx, activeTurn.ID, "integration-test"); err != nil {
		t.Fatalf("CancelTurn: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(ctx, claimedJob); err != nil {
		t.Fatalf("HandleTurnJob claimed retry: %v", err)
	}

	turns, err := turnRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1", len(turns))
	}
	if turns[0].Status != "cancelled" {
		t.Fatalf("turn status = %s, want cancelled", turns[0].Status)
	}

	var activeJobs int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE group_key = $1
		  AND status IN ('pending', 'claimed')
	`, jobqueue.AgentTurnGroupKey(session.ID, userMessage.ID)).Scan(&activeJobs); err != nil {
		t.Fatalf("count active jobs: %v", err)
	}
	if activeJobs != 0 {
		t.Fatalf("active jobs = %d, want 0", activeJobs)
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

func TestTurnEngineIntegrationRepeatedMalformedFileWritePayloadBlocksTask(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "write the file",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("write"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:   "write-1",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"_raw": `{"content":"hello"}`,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(ctx context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output:     map[string]any{"error": "path_required"},
			RunID:      &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	guard, ok := parseTaskValidationGuard(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.Count != validationLoopBlockThreshold {
		t.Fatalf("guard count = %d, want %d", guard.Count, validationLoopBlockThreshold)
	}
	if guard.FailureClass != "tool_validation" {
		t.Fatalf("guard failure_class = %q, want tool_validation", guard.FailureClass)
	}
	if guard.FailureCode != "path_required" {
		t.Fatalf("guard failure_code = %q, want path_required", guard.FailureCode)
	}
	if !strings.Contains(guard.FailureReason, "path_required") {
		t.Fatalf("guard failure_reason = %q, want contains path_required", guard.FailureReason)
	}
	if strings.TrimSpace(guard.AttemptFingerprint) == "" {
		t.Fatal("expected attempt fingerprint on validation guard")
	}

	projectTasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(projectTasks) != 1 {
		t.Fatalf("project task count = %d, want 1 without auto-created blocker resolution task", len(projectTasks))
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	foundBlockerMessage := false
	for _, item := range messages {
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			foundBlockerMessage = true
			break
		}
	}
	if !foundBlockerMessage {
		t.Fatal("missing validation loop blocker system message")
	}
}

func TestTurnEngineIntegrationResumeValidationBlockedTaskStopsSuppression(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	var logBuffer bytes.Buffer
	fixture.engine.logger = slog.New(slog.NewTextHandler(&logBuffer, nil))

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(currentTask.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   userMessage.ID.String(),
		Fingerprint:        "cli.execute:command_required",
		AttemptFingerprint: "cli.execute:command_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_required",
		FailureReason:      "command is required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	currentTask.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update guarded task: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, "deterministic tool validation loop blocked after 3 identical failures: cli.execute (command is required)", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(ctx, &taskSession, AgentTurnPayload{
		SessionID: taskSession.ID,
		MessageID: userMessage.ID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive blocked: %v", err)
	}
	if enqueued {
		t.Fatal("expected blocked task enqueue to be suppressed")
	}
	if !strings.Contains(logBuffer.String(), "skipping agent turn enqueue for blocked validation loop") {
		t.Fatalf("expected suppression log, got %q", logBuffer.String())
	}

	logBuffer.Reset()
	if _, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "supervisor recovery: resume task",
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery: %v", err)
	}

	enqueued, err = fixture.engine.enqueueAgentTurnIfActive(ctx, &taskSession, AgentTurnPayload{
		SessionID: taskSession.ID,
		MessageID: recoveryMessage.ID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive recovered: %v", err)
	}
	if !enqueued {
		t.Fatal("expected recovered task to enqueue a fresh agent turn")
	}
	if strings.Contains(logBuffer.String(), "skipping agent turn enqueue for blocked validation loop") {
		t.Fatalf("unexpected suppression log after recovery: %q", logBuffer.String())
	}
}

func TestTurnEngineIntegrationFileWriteRecoverRawArgumentsAcrossRepeatedWrites(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "write the task files",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{
				{
					ID:   "write-1",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"_raw": `{"path":"templates/index.html","content":"<html>alpha</html>","create_dirs":true`,
					},
				},
				{
					ID:   "write-2",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"_raw": `{"path":"templates/styles.css","content":"body { color: blue; }","create_dirs":true`,
					},
				},
				{
					ID:   "write-3",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"_raw": `{"path":"README.md","content":"launch notes"`,
					},
				},
			}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	expectedPaths := []string{"templates/index.html", "templates/styles.css", "README.md"}
	expectedContents := []string{"<html>alpha</html>", "body { color: blue; }", "launch notes"}
	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		if dispatched >= len(expectedPaths) {
			t.Fatalf("unexpected extra file.write dispatch: %+v", call)
		}
		if _, exists := call.Arguments["_raw"]; exists {
			t.Fatalf("expected normalized arguments without _raw: %+v", call.Arguments)
		}
		path, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		if path != expectedPaths[dispatched] {
			t.Fatalf("dispatch %d path = %q, want %q", dispatched, path, expectedPaths[dispatched])
		}
		if content != expectedContents[dispatched] {
			t.Fatalf("dispatch %d content = %q, want %q", dispatched, content, expectedContents[dispatched])
		}
		dispatched++
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != 3 {
		t.Fatalf("dispatched writes = %d, want 3", dispatched)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok && guard.Count > 0 {
		t.Fatalf("unexpected validation guard after successful writes: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path_required"`) {
			t.Fatalf("unexpected path_required tool_result: %s", item.Content)
		}
	}
}

func TestTurnEngineIntegrationRecoversMalformedCLIExecuteRawArgsForFileOutputEX307(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "recover by writing the template through cli.execute")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}

	const expectedCommand = "mkdir -p templates && cat <<'EOF' > templates/index.html\n<main>Recovered</main>\nEOF"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "cli-1",
				Name: "cli.execute",
				Tier: "tier2",
				Arguments: map[string]any{
					"_raw": `{"cmd":"mkdir -p templates && cat <<'EOF' > templates/index.html\n<main>Recovered</main>\nEOF","working_dir":"."`,
				},
			}}}, nil
		}
		return ModelResponse{Content: "template recovered"}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		if _, exists := call.Arguments["_raw"]; exists {
			t.Fatalf("expected normalized arguments without _raw: %+v", call.Arguments)
		}
		command, _ := call.Arguments["command"].(string)
		if command != expectedCommand {
			t.Fatalf("command = %q, want %q", command, expectedCommand)
		}
		if workingDirectory, _ := call.Arguments["working_directory"].(string); workingDirectory != "." {
			t.Fatalf("working_directory = %q, want .", workingDirectory)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"exit_code":   0,
				"duration_ms": 1,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != 1 {
		t.Fatalf("tier2 dispatches = %d, want 1", dispatched)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want not blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			t.Fatalf("unexpected command_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
}

func TestTurnEngineIntegrationRecoveryTurnRepairsEmptyCLIExecuteBeforeDispatchEX311(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}

	const expectedCommand = "cat > docs/content-strategy.md <<'EOF'\n# Content Strategy\n- Unify Sam's expertise into one durable editorial system.\nEOF"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{
				Content: "I'll compose the strategy and write it via cli_execute in one shot.",
				ToolCalls: []ModelToolCall{{
					ID:   "repair-1",
					Name: "cli_execute",
					Tier: "tier2",
				}},
			}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "cli-1",
				Name: "cli_execute",
				Tier: "tier2",
				Arguments: map[string]any{
					"command": expectedCommand,
				},
			}}}, nil
		default:
			return ModelResponse{Content: "strategy recovered"}, nil
		}
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		if command := stringValue(call.Arguments["command"]); command != expectedCommand {
			t.Fatalf("command = %q, want %q", command, expectedCommand)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      "docs/content-strategy.md",
				"byte_size": 96,
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 3 {
		t.Fatalf("model calls = %d, want 3", modelCalls)
	}
	if dispatched != 1 {
		t.Fatalf("tier2 dispatches = %d, want 1", dispatched)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want not blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			t.Fatalf("unexpected command_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("recovery correction messages = %d, want 1", correctionMessages)
	}
}

func TestTurnEngineIntegrationRecoveryTurnBoundsRepeatedEmptyCLIExecuteWithoutReblockingEX311(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: fmt.Sprintf("attempt %d", modelCalls),
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("empty-%d", modelCalls),
				Name: "cli_execute",
				Tier: "tier2",
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for malformed recovery call: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if got := gotStopReason; got != stopReasonRecoveryCLIRejected {
		t.Fatalf("turn stop_reason = %q, want %q", got, stopReasonRecoveryCLIRejected)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	var haltMessages int
	var blockedGuidanceMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			t.Fatalf("unexpected command_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery turn halted: cli.execute was retried without `command` after one correction.") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "The task is now blocked.") {
			blockedGuidanceMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("recovery correction messages = %d, want 1", correctionMessages)
	}
	if haltMessages != 1 {
		t.Fatalf("recovery halt messages = %d, want 1", haltMessages)
	}
	if blockedGuidanceMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedGuidanceMessages)
	}
}

func TestTurnEngineIntegrationNonRecoveryEmptyCLIExecuteStillBlocksEX311(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "write the strategy via cli.execute")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "I'll use cli_execute.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("bad-%d", modelCalls),
				Name: "cli_execute",
				Tier: "tier2",
			}},
		}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		if got := stringValue(call.Arguments["command"]); got != "" {
			t.Fatalf("command = %q, want empty malformed call", got)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Error:      "command is required",
			RunID:      &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 3 {
		t.Fatalf("model calls = %d, want 3 before validation blocker stops retries", modelCalls)
	}
	if dispatched != 3 {
		t.Fatalf("tier2 dispatches = %d, want 3", dispatched)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	guard, ok := parseTaskValidationGuard(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.Count != validationLoopBlockThreshold {
		t.Fatalf("guard count = %d, want %d", guard.Count, validationLoopBlockThreshold)
	}
	if guard.ToolName != "cli.execute" {
		t.Fatalf("guard tool_name = %q, want cli.execute", guard.ToolName)
	}
	if guard.FailureCode != "command_is_required" {
		t.Fatalf("guard failure_code = %q, want command_is_required", guard.FailureCode)
	}
	if strings.TrimSpace(guard.AttemptFingerprint) == "" {
		t.Fatal("expected attempt fingerprint on validation guard")
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var commandRequiredResults int
	var blockerMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			commandRequiredResults++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			blockerMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			t.Fatalf("unexpected recovery correction in non-recovery turn: %s", item.Content)
		}
	}
	if commandRequiredResults != 3 {
		t.Fatalf("command_required tool_results = %d, want 3", commandRequiredResults)
	}
	if blockerMessages != 1 {
		t.Fatalf("validation blocker system messages = %d, want 1", blockerMessages)
	}
}

func TestTurnEngineIntegrationRecoveryTurnGuidesDraftThenWritesDocumentEX312(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	expectedDraft := strings.TrimRight(`# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Family systems that reduce chaos and increase agency.
- Playbooks that turn values into weekly habits.
- Essays that connect parenting choices to long-term identity.

## Publishing Rhythm
Ship one flagship essay each week, one supporting checklist, and one short proof-of-work note tied to a real family experiment.
`, "\n")

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{
				Content: "I'll draft the file body first.",
				ToolCalls: []ModelToolCall{{
					ID:   "empty-write-1",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		case 2:
			return ModelResponse{
				Content: expectedDraft,
				ToolCalls: []ModelToolCall{{
					ID:   "write-retry",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		default:
			return ModelResponse{Content: "strategy draft recovered"}, nil
		}
	}

	dispatched := 0
	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		path := stringValue(call.Arguments["path"])
		content := stringValue(call.Arguments["content"])
		if path != targetPath {
			t.Fatalf("path = %q, want %q", path, targetPath)
		}
		if content != expectedDraft {
			t.Fatalf("content = %q, want recovered draft", content)
		}
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			t.Fatalf("mkdir target dir: %v", err)
		}
		if err := os.WriteFile(targetAbs, []byte(content), 0o644); err != nil {
			t.Fatalf("write target file: %v", err)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 3 {
		t.Fatalf("model calls = %d, want 3", modelCalls)
	}
	if dispatched != 1 {
		t.Fatalf("tier2 dispatches = %d, want 1", dispatched)
	}
	body, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if string(body) != expectedDraft {
		t.Fatalf("target file body = %q, want recovered draft", string(body))
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want not blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery turn halted: file.write for `docs/content-strategy.md`") {
			t.Fatalf("unexpected recovery halt message: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("file.write recovery correction messages = %d, want 1", correctionMessages)
	}
}

func TestTurnEngineIntegrationRecoveryTurnAcceptsLegacyWorkspaceWriteAsDurableEX322(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}
	legacyWorkspaceRoot, err := workspace.LegacyProjectRoot(dataDir, fixture.org.Slug, projectRecord.Slug)
	if err != nil {
		t.Fatalf("legacy workspace root: %v", err)
	}
	if err := os.MkdirAll(legacyWorkspaceRoot, 0o755); err != nil {
		t.Fatalf("mkdir legacy workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	expectedDraft := strings.TrimRight(`# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Family systems that reduce chaos and increase agency.
- Playbooks that turn values into weekly habits.
- Essays that connect parenting choices to long-term identity.
`, "\n")

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls > 2 {
			t.Fatalf("unexpected extra model call after durable legacy workspace write: %d", modelCalls)
		}
		if modelCalls == 2 {
			return ModelResponse{Content: "legacy workspace write recovered"}, nil
		}
		return ModelResponse{
			Content: expectedDraft,
			ToolCalls: []ModelToolCall{{
				ID:   "write-recovered",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}

	dispatched := 0
	legacyTargetAbs := filepath.Join(legacyWorkspaceRoot, filepath.FromSlash(targetPath))
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		path := stringValue(call.Arguments["path"])
		content := stringValue(call.Arguments["content"])
		if path != targetPath {
			t.Fatalf("path = %q, want %q", path, targetPath)
		}
		if content != expectedDraft {
			t.Fatalf("content = %q, want recovered draft", content)
		}
		if err := os.MkdirAll(filepath.Dir(legacyTargetAbs), 0o755); err != nil {
			t.Fatalf("mkdir legacy target dir: %v", err)
		}
		if err := os.WriteFile(legacyTargetAbs, []byte(content), 0o644); err != nil {
			t.Fatalf("write legacy target file: %v", err)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}
	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != 1 {
		t.Fatalf("tier2 dispatches = %d, want 1", dispatched)
	}

	body, err := os.ReadFile(legacyTargetAbs)
	if err != nil {
		t.Fatalf("read legacy target file: %v", err)
	}
	if string(body) != expectedDraft {
		t.Fatalf("legacy target file body = %q, want recovered draft", string(body))
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))); err == nil {
		t.Fatalf("target file unexpectedly exists in flattened workspace root")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat flattened workspace target: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want not blocked", updatedTask.WorkStatus)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata); ok {
		t.Fatal("unexpected recovery checkpoint after durable legacy workspace write")
	}
}

func TestTurnEngineIntegrationRecoveryTurnRejectsToolRepairNarrationDraftEX320(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "cli.execute", Tier: "tier2"},
		{Name: "file.write", Tier: "tier2"},
	}}

	const targetPath = "docs/content-strategy.md"
	const repairedNarration = "I can see the problem clearly from the conversation history. " +
		"Every `file_write` call has been emitted **without the `content` parameter populated**. " +
		"The system then captures nearby prose text as a fallback. I need to pass the full document text as the explicit `content` parameter value. Let me do this now."

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{
				Content: "I understand the problem now. My `file_write` calls have consistently been emitted without the `content` parameter populated. Let me use `cli_execute` with a heredoc to write the file instead.",
				ToolCalls: []ModelToolCall{{
					ID:   "recovery-cli",
					Name: "cli_execute",
					Tier: "tier2",
				}},
			}, nil
		case 2:
			return ModelResponse{
				Content: repairedNarration,
				ToolCalls: []ModelToolCall{{
					ID:   "recovery-write",
					Name: "file_write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		default:
			t.Fatalf("unexpected extra model call after rejecting tool-repair narration draft: %d", modelCalls)
			return ModelResponse{}, nil
		}
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		dispatched++
		t.Fatalf("unexpected tier2 dispatch for rejected recovery draft: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != 0 {
		t.Fatalf("tier2 dispatches = %d, want 0", dispatched)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))); err == nil {
		t.Fatalf("target file unexpectedly exists at %s", targetPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat target file: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if !strings.Contains(checkpoint.FailureReason, "tool-recovery troubleshooting") {
		t.Fatalf("checkpoint failure_reason = %q, want repair-narration guidance", checkpoint.FailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel)))
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, repairedNarration) {
		t.Fatalf("artifact missing rejected draft:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "tool-recovery troubleshooting") {
		t.Fatalf("artifact missing repair-narration failure reason:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var cliCorrections int
	var fileWriteCorrections int
	var haltMessages int
	var toolResults int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/content-strategy.md"`) {
			toolResults++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			cliCorrections++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			fileWriteCorrections++
		}
		if item.Role == "system" && strings.Contains(item.Content, "tool-recovery troubleshooting instead of the file body") {
			haltMessages++
		}
	}
	if cliCorrections != 1 {
		t.Fatalf("cli recovery correction messages = %d, want 1", cliCorrections)
	}
	if fileWriteCorrections != 0 {
		t.Fatalf("file.write recovery correction messages = %d, want 0", fileWriteCorrections)
	}
	if haltMessages != 1 {
		t.Fatalf("repair-narration halt messages = %d, want 1", haltMessages)
	}
	if toolResults != 0 {
		t.Fatalf("file.write tool_results = %d, want 0", toolResults)
	}
}

func TestTurnEngineIntegrationRecoveryResumeRejectsPlaceholderNarrationDraftEX326(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)
	seed := mustPersistRecoveryResumeFixture(t, ctx, fixture, taskRecord, recoveryMessage.ID)

	existingTargetBody := strings.TrimSpace(`# Content Strategy

## Durable Anchor
- Keep the on-disk strategy intact until a new substantive draft exists.
`) + "\n"
	if err := os.WriteFile(seed.targetAbs, []byte(existingTargetBody), 0o644); err != nil {
		t.Fatalf("rewrite durable target draft: %v", err)
	}

	assembler := &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 30}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const placeholderNarration = "Now I have everything I need. Let me write the comprehensive content strategy document. This needs to be the single deliverable that unblocks WS4 and serves as the strategic foundation for Sam.blog."

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls > 1 {
			t.Fatalf("unexpected extra model call after rejecting placeholder draft: %d", modelCalls)
		}
		return ModelResponse{
			Content: placeholderNarration,
			ToolCalls: []ModelToolCall{{
				ID:   "resume-write",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": seed.targetPath,
				},
			}},
		}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		dispatched++
		t.Fatalf("unexpected tier2 dispatch for rejected placeholder draft: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery resume: %v", err)
	}

	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1", modelCalls)
	}
	if assembler.calls != 1 {
		t.Fatalf("assemble calls = %d, want 1 because placeholder rejection should halt before continuation guardrails", assembler.calls)
	}
	if dispatched != 0 {
		t.Fatalf("tier2 dispatches = %d, want 0", dispatched)
	}

	outputBody, err := os.ReadFile(seed.targetAbs)
	if err != nil {
		t.Fatalf("read durable target file: %v", err)
	}
	if string(outputBody) != existingTargetBody {
		t.Fatalf("target file was clobbered:\n%s", string(outputBody))
	}
	if strings.Contains(string(outputBody), "Let me write the comprehensive content strategy document") {
		t.Fatalf("target file still contains placeholder narration:\n%s", string(outputBody))
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != seed.targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, seed.targetPath)
	}
	if checkpoint.ArtifactPath != seed.artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, seed.artifactRel)
	}
	if !strings.Contains(checkpoint.FailureReason, "intent to write the deliverable") {
		t.Fatalf("checkpoint failure_reason = %q, want placeholder rejection guidance", checkpoint.FailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(seed.artifactAbs)
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, placeholderNarration) {
		t.Fatalf("artifact missing rejected placeholder draft:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "intent to write the deliverable") {
		t.Fatalf("artifact missing placeholder failure reason:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var haltMessages int
	var toolResults int
	var continuationMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/content-strategy.md"`) {
			toolResults++
		}
		if item.Role == "system" && strings.Contains(item.Content, "intent to write the deliverable instead of the file body") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Prompt input exceeded 64000-token guardrail - continuing in a new turn.") {
			continuationMessages++
		}
	}
	if haltMessages != 1 {
		t.Fatalf("placeholder halt messages = %d, want 1", haltMessages)
	}
	if toolResults != 0 {
		t.Fatalf("file.write tool_results = %d, want 0", toolResults)
	}
	if continuationMessages != 0 {
		t.Fatalf("continuation guardrail messages = %d, want 0 after placeholder rejection", continuationMessages)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
}

func TestTurnEngineIntegrationClaimedRecoveryResumeRejectsShallowPlaceholderWriteEX328(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)
	seed := mustPersistRecoveryResumeFixture(t, ctx, fixture, taskRecord, recoveryMessage.ID)

	existingTargetBody := "Time to write the comprehensive content strategy document.\nThis is the critical deliverable that unblocks WS4.\n"
	if err := os.WriteFile(seed.targetAbs, []byte(existingTargetBody), 0o644); err != nil {
		t.Fatalf("rewrite prior placeholder target: %v", err)
	}

	assembler := &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 30}},
	}}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const shallowPlaceholder = "Time to write the comprehensive content strategy document for Sam.blog. This is the critical deliverable that unblocks WS4. The next step is to capture the audience, the pillars, the tone, and the publishing cadence so the rest of the project can move forward."

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{
				Content: shallowPlaceholder,
				ToolCalls: []ModelToolCall{{
					ID:   "resume-write",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": seed.targetPath,
					},
				}},
			}, nil
		default:
			return ModelResponse{}, fmt.Errorf("unexpected follow-up model call after shallow placeholder write")
		}
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		dispatched++
		t.Fatalf("unexpected tier2 dispatch for shallow placeholder draft: %+v", call)
		return ToolResult{}, nil
	}

	worker := jobqueue.New(fixture.pool, nil, jobqueue.Config{})
	jobID, err := worker.Enqueue(ctx, nil, AgentTurnJobType, defaultAgentTurnJobPriority, AgentTurnPayload{
		SessionID: taskSession.ID,
		MessageID: recoveryMessage.ID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueue claimed recovery job: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'integration-worker',
		    claimed_at = now()
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("mark recovery job claimed: %v", err)
	}

	var payload []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&payload); err != nil {
		t.Fatalf("load claimed recovery payload: %v", err)
	}
	claimedJob := jobqueue.Job{
		ID:      jobID,
		JobType: AgentTurnJobType,
		Payload: payload,
		Status:  "claimed",
	}

	if err := fixture.engine.HandleTurnJob(ctx, claimedJob); err != nil {
		t.Fatalf("HandleTurnJob claimed recovery resume: %v", err)
	}

	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1 bounded recovery turn", modelCalls)
	}
	if assembler.calls != 1 {
		t.Fatalf("assemble calls = %d, want 1 because placeholder rejection should halt before dispatch", assembler.calls)
	}
	if dispatched != 0 {
		t.Fatalf("tier2 dispatches = %d, want 0", dispatched)
	}

	outputBody, err := os.ReadFile(seed.targetAbs)
	if err != nil {
		t.Fatalf("read target file after claimed recovery resume: %v", err)
	}
	if string(outputBody) != existingTargetBody {
		t.Fatalf("target file was clobbered:\n%s", string(outputBody))
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != seed.targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, seed.targetPath)
	}
	if checkpoint.ArtifactPath != seed.artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, seed.artifactRel)
	}
	if !strings.Contains(checkpoint.FailureReason, "intent to write the deliverable") {
		t.Fatalf("checkpoint failure_reason = %q, want shallow-placeholder rejection guidance", checkpoint.FailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(seed.artifactAbs)
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, shallowPlaceholder) {
		t.Fatalf("artifact missing rejected shallow placeholder draft:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var haltMessages int
	var toolResults int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/content-strategy.md"`) {
			toolResults++
		}
		if item.Role == "system" && strings.Contains(item.Content, "intent to write the deliverable instead of the file body") {
			haltMessages++
		}
	}
	if haltMessages != 1 {
		t.Fatalf("placeholder halt messages = %d, want 1", haltMessages)
	}
	if toolResults != 0 {
		t.Fatalf("file.write tool_results = %d, want 0", toolResults)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
}

func TestTurnEngineIntegrationRecoveryTurnPersistsCheckpointForRepeatedEmptyCLIExecuteWithFileContextEX321(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, initialUserMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	const targetPath = "docs/content-strategy.md"
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	historicDraft := "I can see the problem clearly from the conversation history. Every `file_write` call has been emitted **without the `content` parameter populated**. The system then captures nearby prose text as a fallback. I need to pass the full document text as the explicit `content` parameter value. Let me do this now."

	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(historicDraft), 0o644); err != nil {
		t.Fatalf("write historic target: %v", err)
	}
	existingArtifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(existingArtifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(existingArtifactAbs, []byte("stale artifact"), 0o644); err != nil {
		t.Fatalf("write stale artifact: %v", err)
	}

	priorTurn, err := fixture.chatService.CreateTurn(ctx, taskSession.ID, fixture.agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn prior recovery: %v", err)
	}
	if err := fixture.chatService.StartTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("StartTurn prior recovery: %v", err)
	}
	assistantType := "agent"
	assistantMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		TurnID:     &priorTurn.ID,
		AuthorType: &assistantType,
		AuthorID:   &fixture.agent.ID,
		Role:       "assistant",
		Content:    historicDraft,
	})
	if err != nil {
		t.Fatalf("AppendMessage prior assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, assistantMessage.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior assistant: %v", err)
	}
	fileWriteResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		TurnID:    &priorTurn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.write", "output": map[string]any{"path": targetPath, "byte_size": len(historicDraft), "created": false}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage prior file.write tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileWriteResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior file.write tool_result: %v", err)
	}
	fileReadResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		TurnID:    &priorTurn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.read", "output": map[string]any{"path": targetPath, "content": historicDraft, "encoding": "utf8", "truncated": false, "byte_size": len(historicDraft)}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage prior file.read tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileReadResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior file.read tool_result: %v", err)
	}
	if err := fixture.chatService.CompleteTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("CompleteTurn prior recovery: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(currentTask.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   initialUserMessage.ID.String(),
		Fingerprint:        "cli.execute:command_is_required",
		AttemptFingerprint: "cli.execute:command_is_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_is_required",
		FailureReason:      "command is required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	currentTask.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update guarded task: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, "deterministic tool validation loop blocked after 3 identical failures: cli.execute (command is required)", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if _, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "supervisor recovery: resume task",
		Metadata:   mustJSON(t, map[string]any{"source": "supervisor", "stranded_execution": true}),
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "The `file_write` tool keeps failing because the `content` parameter isn't being populated. Let me use `cli_execute` with a heredoc instead — this is the reliable fallback path.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("empty-cli-%d", modelCalls),
				Name: "cli_execute",
				Tier: "tier2",
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for empty cli recovery: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after recovery: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if !strings.Contains(checkpoint.FailureReason, "cli.execute for "+targetPath+" was retried without `command`") {
		t.Fatalf("checkpoint failure_reason = %q, want cli.execute durable recovery guidance", checkpoint.FailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	lastTurn := turns[len(turns)-1]
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(existingArtifactAbs)
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, historicDraft) {
		t.Fatalf("artifact missing historic draft:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "cli.execute for "+targetPath+" was retried without `command`") {
		t.Fatalf("artifact missing cli recovery failure reason:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	var haltMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			t.Fatalf("unexpected command_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Resume from `.ottercamp/recovery/docs/content-strategy.md`") {
			haltMessages++
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("recovery correction messages = %d, want 1", correctionMessages)
	}
	if haltMessages != 1 {
		t.Fatalf("recovery halt messages = %d, want 1", haltMessages)
	}
}

func TestTurnEngineIntegrationRecoveryTurnMirrorsArtifactIntoLegacyWorkspaceRootEX322(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, initialUserMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}
	legacyWorkspaceRoot, err := workspace.LegacyProjectRoot(dataDir, fixture.org.Slug, projectRecord.Slug)
	if err != nil {
		t.Fatalf("legacy workspace root: %v", err)
	}

	const targetPath = "docs/content-strategy.md"
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	historicDraft := "I can see the problem clearly from the conversation history. Every `file_write` call has been emitted **without the `content` parameter populated**. The system then captures nearby prose text as a fallback. I need to pass the full document text as the explicit `content` parameter value. Let me do this now."

	legacyTargetAbs := filepath.Join(legacyWorkspaceRoot, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(legacyTargetAbs), 0o755); err != nil {
		t.Fatalf("mkdir legacy target dir: %v", err)
	}
	if err := os.WriteFile(legacyTargetAbs, []byte(historicDraft), 0o644); err != nil {
		t.Fatalf("write legacy historic target: %v", err)
	}
	legacyArtifactAbs := filepath.Join(legacyWorkspaceRoot, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(legacyArtifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir legacy artifact dir: %v", err)
	}
	if err := os.WriteFile(legacyArtifactAbs, []byte("stale artifact"), 0o644); err != nil {
		t.Fatalf("write stale legacy artifact: %v", err)
	}

	priorTurn, err := fixture.chatService.CreateTurn(ctx, taskSession.ID, fixture.agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn prior recovery: %v", err)
	}
	if err := fixture.chatService.StartTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("StartTurn prior recovery: %v", err)
	}
	assistantType := "agent"
	assistantMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		TurnID:     &priorTurn.ID,
		AuthorType: &assistantType,
		AuthorID:   &fixture.agent.ID,
		Role:       "assistant",
		Content:    historicDraft,
	})
	if err != nil {
		t.Fatalf("AppendMessage prior assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, assistantMessage.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior assistant: %v", err)
	}
	fileWriteResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		TurnID:    &priorTurn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.write", "output": map[string]any{"path": targetPath, "byte_size": len(historicDraft), "created": false}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage prior file.write tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileWriteResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior file.write tool_result: %v", err)
	}
	fileReadResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		TurnID:    &priorTurn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.read", "output": map[string]any{"path": targetPath, "content": historicDraft, "encoding": "utf8", "truncated": false, "byte_size": len(historicDraft)}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage prior file.read tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileReadResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior file.read tool_result: %v", err)
	}
	if err := fixture.chatService.CompleteTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("CompleteTurn prior recovery: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(currentTask.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   initialUserMessage.ID.String(),
		Fingerprint:        "cli.execute:command_is_required",
		AttemptFingerprint: "cli.execute:command_is_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_is_required",
		FailureReason:      "command is required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	currentTask.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update guarded task: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, "deterministic tool validation loop blocked after 3 identical failures: cli.execute (command is required)", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if _, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "supervisor recovery: resume task",
		Metadata:   mustJSON(t, map[string]any{"source": "supervisor", "stranded_execution": true}),
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "cli.execute", Tier: "tier2"}}}
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "The `file_write` tool keeps failing because the `content` parameter isn't being populated. Let me use `cli_execute` with a heredoc instead — this is the reliable fallback path.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("empty-cli-%d", modelCalls),
				Name: "cli_execute",
				Tier: "tier2",
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for empty cli recovery: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}
	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	flatArtifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if _, err := os.Stat(flatArtifactAbs); err != nil {
		t.Fatalf("expected recovery artifact in flattened workspace root: %v", err)
	}
	if _, err := os.Stat(legacyArtifactAbs); err != nil {
		t.Fatalf("expected recovery artifact in legacy workspace root: %v", err)
	}
	legacyArtifactBody, err := os.ReadFile(legacyArtifactAbs)
	if err != nil {
		t.Fatalf("read legacy recovery artifact: %v", err)
	}
	if !strings.Contains(string(legacyArtifactBody), historicDraft) {
		t.Fatalf("legacy recovery artifact missing historic draft:\n%s", string(legacyArtifactBody))
	}
}

func TestTurnEngineIntegrationRecoveryTurnPersistsArtifactAfterRepeatedEmptyFileWriteEX312(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "I'll write it next.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("empty-write-%d", modelCalls),
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for empty-content recovery write: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if checkpoint.HaltTurnID != lastTurn.ID.String() {
		t.Fatalf("checkpoint halt_turn_id = %q, want %q", checkpoint.HaltTurnID, lastTurn.ID)
	}
	artifactBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel)))
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, "Target Path: "+targetPath) {
		t.Fatalf("artifact missing target path:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "No concrete draft content was available") {
		t.Fatalf("artifact missing empty-draft placeholder:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	var haltMessages int
	var blockedGuidanceMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Resume from `.ottercamp/recovery/docs/content-strategy.md`") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "The task is now blocked.") {
			blockedGuidanceMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("file.write recovery correction messages = %d, want 1", correctionMessages)
	}
	if haltMessages != 1 {
		t.Fatalf("file.write recovery halt messages = %d, want 1", haltMessages)
	}
	if blockedGuidanceMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedGuidanceMessages)
	}
}

func TestTurnEngineIntegrationTaskQueueRecoveryTurnPersistsArtifactAfterRepeatedEmptyFileWriteEX313(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "I'll write it next.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("empty-write-%d", modelCalls),
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for empty-content recovery write: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if checkpoint.HaltTurnID != lastTurn.ID.String() {
		t.Fatalf("checkpoint halt_turn_id = %q, want %q", checkpoint.HaltTurnID, lastTurn.ID)
	}
	artifactBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel)))
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, "Target Path: "+targetPath) {
		t.Fatalf("artifact missing target path:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "No concrete draft content was available") {
		t.Fatalf("artifact missing empty-draft placeholder:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	var haltMessages int
	var blockedGuidanceMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Resume from `.ottercamp/recovery/docs/content-strategy.md`") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "The task is now blocked.") {
			blockedGuidanceMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("file.write recovery correction messages = %d, want 1", correctionMessages)
	}
	if haltMessages != 1 {
		t.Fatalf("file.write recovery halt messages = %d, want 1", haltMessages)
	}
	if blockedGuidanceMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedGuidanceMessages)
	}
}

func TestTurnEngineIntegrationTaskQueueRecoveryTurnFallsBackStopReasonForLegacyConstraintEX314(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)
	forceLegacyChatTurnStopReasonConstraint(t, ctx, fixture.pool)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{
			Content: "I'll write it next.",
			ToolCalls: []ModelToolCall{{
				ID:   fmt.Sprintf("legacy-empty-write-%d", modelCalls),
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for legacy empty-content recovery write: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery with legacy constraint: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileFallback {
		t.Fatalf("turn stop_reason = %q, want %q legacy fallback", gotStopReason, stopReasonRecoveryFileFallback)
	}
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if checkpoint.HaltTurnID != lastTurn.ID.String() {
		t.Fatalf("checkpoint halt_turn_id = %q, want %q", checkpoint.HaltTurnID, lastTurn.ID)
	}
	artifactBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel)))
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, "Target Path: "+targetPath) {
		t.Fatalf("artifact missing target path:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "No concrete draft content was available") {
		t.Fatalf("artifact missing empty-draft placeholder:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var correctionMessages int
	var haltMessages int
	var blockedGuidanceMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			correctionMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Resume from `.ottercamp/recovery/docs/content-strategy.md`") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "The task is now blocked.") {
			blockedGuidanceMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if correctionMessages != 1 {
		t.Fatalf("file.write recovery correction messages = %d, want 1", correctionMessages)
	}
	if haltMessages != 1 {
		t.Fatalf("file.write recovery halt messages = %d, want 1", haltMessages)
	}
	if blockedGuidanceMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedGuidanceMessages)
	}
}

func TestTurnEngineIntegrationTaskQueueRecoveryTurnBlocksOnProviderAuthFailureEX318(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		modelCalls++
		return ModelResponse{}, ErrAuthFailed
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}
	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1 bounded auth failure", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded turn", len(turns))
	}
	lastTurn := turns[0]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryCLIRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryCLIRejected)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var haltMessages int
	for _, item := range messages {
		if item.Role == "system" && strings.Contains(item.Content, "provider authentication failed and no healthy enabled fallback connection could continue the work") {
			haltMessages++
		}
	}
	if haltMessages != 1 {
		t.Fatalf("provider auth halt messages = %d, want 1", haltMessages)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 after bounded auth halt", fixture.model.continuationSummaryCalls)
	}
}

func TestTurnEngineIntegrationRecoveryFileWriteCheckpointPersistsAtConfiguredDataDirAndGuidesNextAttemptEX315(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", "")

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	prompts := make([]string, 0, 4)
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		prompts = append(prompts, flattenPrompt(req.Prompt))
		modelCalls++
		switch modelCalls {
		case 1, 2:
			return ModelResponse{
				Content: "I'll write it next.",
				ToolCalls: []ModelToolCall{{
					ID:   fmt.Sprintf("empty-write-%d", modelCalls),
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		case 3:
			return ModelResponse{
				Content: "Recovered draft ready.",
				ToolCalls: []ModelToolCall{{
					ID:   "recovered-write",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path":    targetPath,
						"content": "# Content Strategy\n\nRecovered from checkpoint.\n",
					},
				}},
			}, nil
		default:
			return ModelResponse{Content: "Recovered file written."}, nil
		}
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		target, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		if strings.TrimSpace(content) == "" {
			t.Fatalf("unexpected tier2 dispatch for empty-content recovery write: %+v", call)
		}
		targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(targetAbs, []byte(content), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			RunID:      &runID,
			Output: map[string]any{
				"path":      target,
				"byte_size": len(content),
				"created":   true,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery halt: %v", err)
	}

	artifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if _, err := os.Stat(artifactAbs); err != nil {
		t.Fatalf("expected recovery artifact in configured data dir: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after halt: %v", err)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected persisted recovery file.write checkpoint")
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if strings.TrimSpace(checkpoint.HistoryStartMessageID) == "" {
		t.Fatal("expected checkpoint history_start_message_id")
	}

	authorType := "human_user"
	resumeMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "resume from the recovery checkpoint and finish the file",
	})
	if err != nil {
		t.Fatalf("AppendMessage resume: %v", err)
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, resumeMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage resume: %v", err)
	}

	if len(prompts) < 3 {
		t.Fatalf("prompt count = %d, want at least 3", len(prompts))
	}
	resumePrompt := prompts[2]
	if !strings.Contains(resumePrompt, "Recovery Execution Strategy:") {
		t.Fatalf("resume prompt missing recovery strategy:\n%s", resumePrompt)
	}
	if !strings.Contains(resumePrompt, "Recovery artifact: "+artifactRel) {
		t.Fatalf("resume prompt missing recovery artifact path %q:\n%s", artifactRel, resumePrompt)
	}
	if !strings.Contains(resumePrompt, "Target file: "+targetPath) {
		t.Fatalf("resume prompt missing target path %q:\n%s", targetPath, resumePrompt)
	}

	outputBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(targetPath)))
	if err != nil {
		t.Fatalf("read recovered output: %v", err)
	}
	if !strings.Contains(string(outputBody), "Recovered from checkpoint.") {
		t.Fatalf("recovered output missing checkpoint content:\n%s", string(outputBody))
	}

	finalTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after recovery write: %v", err)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(finalTask.Metadata); ok {
		t.Fatal("expected recovery checkpoint to clear after successful file.write")
	}
}

func TestTurnEngineIntegrationRecoveryResumeSeedsDurableDiskContextEX323(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	seed := mustPersistRecoveryResumeFixture(t, ctx, fixture, taskRecord, recoveryMessage.ID)

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	promptBlob := ""
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			promptBlob = flattenPrompt(req.Prompt)
			return ModelResponse{
				Content: "Continuing from the durable recovery drafts.",
				ToolCalls: []ModelToolCall{{
					ID:   "resume-final-write",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": seed.targetPath,
						"content": strings.TrimSpace(`# Content Strategy

## Core Promise
Sam.blog publishes one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Practical family systems with clear routines and agency.
- Honest work, craft, and stewardship stories grounded in lived experience.
- Measured experiments that turn reflection into repeatable operating habits.
`) + "\n",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "Recovered document written."}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		target, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		targetAbs := filepath.Join(seed.workspaceRoot, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(targetAbs, []byte(content), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			RunID:      &runID,
			Output: map[string]any{
				"path":      target,
				"byte_size": len(content),
				"created":   true,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery resume: %v", err)
	}

	if !strings.Contains(promptBlob, "[Recovery resume state]") {
		t.Fatalf("prompt missing recovery resume state:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, "Resume order: target file draft, then recovery artifact draft, then checkpoint metadata/failure reason.") {
		t.Fatalf("prompt missing durable resume order:\n%s", promptBlob)
	}
	if strings.Contains(promptBlob, seed.targetDraft) {
		t.Fatalf("prompt should omit rejected placeholder target draft:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, "Existing target file draft: omitted because it matches the previously rejected non-substantive pattern.") {
		t.Fatalf("prompt missing rejected target-draft omission guidance:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, seed.artifactDraft) {
		t.Fatalf("prompt missing recovery artifact draft:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, seed.failureReason) {
		t.Fatalf("prompt missing checkpoint failure reason:\n%s", promptBlob)
	}

	outputBody, err := os.ReadFile(seed.targetAbs)
	if err != nil {
		t.Fatalf("read final output: %v", err)
	}
	if strings.Contains(string(outputBody), "Let me write the full document now.") {
		t.Fatalf("target file still contains low-value stub instead of substantive output:\n%s", string(outputBody))
	}
	if !strings.Contains(string(outputBody), "Sam.blog publishes one durable operating system") {
		t.Fatalf("final output missing substantive recovered content:\n%s", string(outputBody))
	}

	finalTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after durable resume: %v", err)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(finalTask.Metadata); ok {
		t.Fatal("expected recovery checkpoint to clear after durable recovery resume")
	}
}

func TestTurnEngineIntegrationRecoveryTurnHaltsWhenRecoveredFileWriteDoesNotLandDurablyEX319(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	const targetPath = "docs/content-strategy.md"
	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	expectedDraft := strings.TrimRight(`# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Family systems that reduce chaos and increase agency.
- Playbooks that turn values into weekly habits.
- Essays that connect parenting choices to long-term identity.
`, "\n")

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls > 1 {
			t.Fatalf("unexpected extra model call after recovered file.write failed durability check: %d", modelCalls)
		}
		return ModelResponse{
			Content: expectedDraft,
			ToolCalls: []ModelToolCall{{
				ID:   "write-recovered",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		dispatched++
		path := stringValue(call.Arguments["path"])
		content := stringValue(call.Arguments["content"])
		if path != targetPath {
			t.Fatalf("path = %q, want %q", path, targetPath)
		}
		if content != expectedDraft {
			t.Fatalf("content = %q, want recovered draft", content)
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1 bounded recovery turn", modelCalls)
	}
	if dispatched != 1 {
		t.Fatalf("tier2 dispatches = %d, want 1", dispatched)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))); err == nil {
		t.Fatalf("target file unexpectedly exists at %s", targetPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat target file: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint metadata")
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if !strings.Contains(checkpoint.FailureReason, "was not found on disk") {
		t.Fatalf("checkpoint failure_reason = %q, want missing-file guidance", checkpoint.FailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel)))
	if err != nil {
		t.Fatalf("read recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, "Last Write Failure") {
		t.Fatalf("artifact missing failure section:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "recovered file.write reported success but docs/content-strategy.md was not found on disk") {
		t.Fatalf("artifact missing durable failure reason:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, expectedDraft) {
		t.Fatalf("artifact missing recovered draft:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var recoveredWriteResults int
	var haltMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/content-strategy.md"`) {
			recoveredWriteResults++
		}
		if item.Role == "system" && strings.Contains(item.Content, "did not produce a durable file") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if recoveredWriteResults != 1 {
		t.Fatalf("recovered write tool_results = %d, want 1", recoveredWriteResults)
	}
	if haltMessages != 1 {
		t.Fatalf("durable halt messages = %d, want 1", haltMessages)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
}

func TestTurnEngineIntegrationRecoveryTurnBoundsCLIThenFileWriteRetryChainEX312(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "cli.execute", Tier: "tier2"},
		{Name: "file.write", Tier: "tier2"},
	}}

	const targetPath = "docs/content-strategy.md"
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{
				Content: "I'll use cli first.",
				ToolCalls: []ModelToolCall{{
					ID:   "empty-cli",
					Name: "cli_execute",
					Tier: "tier2",
				}},
			}, nil
		case 2:
			return ModelResponse{
				Content: "Still need the document body.",
				ToolCalls: []ModelToolCall{{
					ID:   "empty-write-1",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		default:
			return ModelResponse{
				Content: "Retrying the write.",
				ToolCalls: []ModelToolCall{{
					ID:   "empty-write-2",
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"path": targetPath,
					},
				}},
			}, nil
		}
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch in chained recovery test: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery: %v", err)
	}

	if modelCalls != 3 {
		t.Fatalf("model calls = %d, want 3", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok {
		t.Fatalf("unexpected validation guard persisted: %+v", guard)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected recovery turn")
	}
	lastTurn := turns[len(turns)-1]
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))); err != nil {
		t.Fatalf("expected chained recovery artifact: %v", err)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var cliCorrections int
	var fileCorrections int
	var haltMessages int
	var blockedGuidanceMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, "command is required") {
			t.Fatalf("unexpected command_required tool_result: %s", item.Content)
		}
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: cli.execute was emitted without `command`") {
			cliCorrections++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Recovery correction: file.write for `docs/content-strategy.md` was emitted without `content`") {
			fileCorrections++
		}
		if item.Role == "system" && strings.Contains(item.Content, "Resume from `.ottercamp/recovery/docs/content-strategy.md`") {
			haltMessages++
		}
		if item.Role == "system" && strings.Contains(item.Content, "The task is now blocked.") {
			blockedGuidanceMessages++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation blocker message: %s", item.Content)
		}
	}
	if cliCorrections != 1 {
		t.Fatalf("cli recovery correction messages = %d, want 1", cliCorrections)
	}
	if fileCorrections != 1 {
		t.Fatalf("file.write recovery correction messages = %d, want 1", fileCorrections)
	}
	if haltMessages != 1 {
		t.Fatalf("file.write recovery halt messages = %d, want 1", haltMessages)
	}
	if blockedGuidanceMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedGuidanceMessages)
	}
}

func TestTurnEngineIntegrationFileWriteRecoversQuotedContentAcrossContentMigrationWrites(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "persist the migration outputs in small verified batches",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	type expectedWrite struct {
		id      string
		path    string
		content string
		raw     string
	}
	writes := []expectedWrite{
		{
			id:   "write-post-1",
			path: "content/posts/stop-preparing-your-kids-for-jobs.md",
			content: "---\n" +
				"title: \"Stop Preparing Your Kids for Jobs\"\n" +
				"summary: \"Migrated from Sam.blog\"\n" +
				"---\n" +
				"First paragraph.\n",
			raw: `{"path":"content/posts/stop-preparing-your-kids-for-jobs.md","content":"---
title: "Stop Preparing Your Kids for Jobs"
summary: "Migrated from Sam.blog"
---
First paragraph.
","create_dirs":true}`,
		},
		{
			id:      "write-post-2",
			path:    "content/posts/let-kids-be-kids.md",
			content: "# Let Kids Be Kids\n\nHe said \"ship it\" and kept migrating.\n",
			raw: `{"path":"content/posts/let-kids-be-kids.md","content":"# Let Kids Be Kids

He said "ship it" and kept migrating.
","create_dirs":true}`,
		},
	}
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			toolCalls := make([]ModelToolCall, 0, len(writes))
			for _, write := range writes {
				toolCalls = append(toolCalls, ModelToolCall{
					ID:   write.id,
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"_raw": write.raw,
					},
				})
			}
			return ModelResponse{ToolCalls: toolCalls}, nil
		}
		return ModelResponse{Content: "migration batch persisted"}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		if dispatched >= len(writes) {
			t.Fatalf("unexpected extra file.write dispatch: %+v", call)
		}
		want := writes[dispatched]
		if _, exists := call.Arguments["_raw"]; exists {
			t.Fatalf("expected normalized arguments without _raw: %+v", call.Arguments)
		}
		path, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		if path != want.path {
			t.Fatalf("dispatch %d path = %q, want %q", dispatched, path, want.path)
		}
		if content != want.content {
			t.Fatalf("dispatch %d content = %q, want %q", dispatched, content, want.content)
		}
		dispatched++
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != len(writes) {
		t.Fatalf("dispatched writes = %d, want %d", dispatched, len(writes))
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want non-blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok && guard.Count > 0 {
		t.Fatalf("unexpected validation guard after successful migration writes: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation loop blocker after successful migration writes: %s", item.Content)
		}
	}
}

func TestTurnEngineIntegrationFileWriteRecoversQuotedContentAcrossTemplateGenerationWrites(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "generate the first template files and keep going",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	type expectedWrite struct {
		id      string
		path    string
		content string
		raw     string
	}
	writes := []expectedWrite{
		{
			id:   "write-home",
			path: "templates/home.html",
			content: "<section class=\"hero\">\n" +
				"  <h1>Sam.blog</h1>\n" +
				"  <p>Ship the first bold direction.</p>\n" +
				"</section>\n",
			raw: `{"path":"templates/home.html","content":"<section class="hero">
  <h1>Sam.blog</h1>
  <p>Ship the first bold direction.</p>
</section>
","create_dirs":true}`,
		},
		{
			id:      "write-theme",
			path:    "templates/theme.css",
			content: ".hero::before { content: \"→\"; }\nbody { font-family: \"Newsreader\", serif; }\n",
			raw: `{"path":"templates/theme.css","content":".hero::before { content: "→"; }
body { font-family: "Newsreader", serif; }
","create_dirs":true}`,
		},
	}
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls == 1 {
			toolCalls := make([]ModelToolCall, 0, len(writes))
			for _, write := range writes {
				toolCalls = append(toolCalls, ModelToolCall{
					ID:   write.id,
					Name: "file.write",
					Tier: "tier2",
					Arguments: map[string]any{
						"_raw": write.raw,
					},
				})
			}
			return ModelResponse{ToolCalls: toolCalls}, nil
		}
		return ModelResponse{Content: "template batch created"}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		if dispatched >= len(writes) {
			t.Fatalf("unexpected extra file.write dispatch: %+v", call)
		}
		want := writes[dispatched]
		if _, exists := call.Arguments["_raw"]; exists {
			t.Fatalf("expected normalized arguments without _raw: %+v", call.Arguments)
		}
		path, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		if path != want.path {
			t.Fatalf("dispatch %d path = %q, want %q", dispatched, path, want.path)
		}
		if content != want.content {
			t.Fatalf("dispatch %d content = %q, want %q", dispatched, content, want.content)
		}
		dispatched++
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", modelCalls)
	}
	if dispatched != len(writes) {
		t.Fatalf("dispatched writes = %d, want %d", dispatched, len(writes))
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus == "blocked" {
		t.Fatalf("task work_status = %q, want non-blocked", updatedTask.WorkStatus)
	}
	if guard, ok := parseTaskValidationGuard(updatedTask.Metadata); ok && guard.Count > 0 {
		t.Fatalf("unexpected validation guard after successful template writes: %+v", guard)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			t.Fatalf("unexpected content_required tool_result: %s", item.Content)
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			t.Fatalf("unexpected validation loop blocker after successful template writes: %s", item.Content)
		}
	}
}

func TestTurnEngineIntegrationFileWriteMissingPathRetryDoesNotResetAcrossAdjacentSuccessfulWrites(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "write everything for the task",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	missingRaw := map[string]any{"_raw": `{"content":"hello"}`}
	validCalls := []map[string]any{
		{"_raw": `{"path":"docs/alpha.txt","content":"alpha","create_dirs":true`},
		{"_raw": `{"path":"docs/beta.txt","content":"beta","create_dirs":true`},
	}
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-1", Name: "file.write", Tier: "tier2", Arguments: missingRaw}}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "good-1", Name: "file.write", Tier: "tier2", Arguments: validCalls[0]}}}, nil
		case 3:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-2", Name: "file.write", Tier: "tier2", Arguments: missingRaw}}}, nil
		case 4:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "good-2", Name: "file.write", Tier: "tier2", Arguments: validCalls[1]}}}, nil
		default:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-3", Name: "file.write", Tier: "tier2", Arguments: missingRaw}}}, nil
		}
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		if _, exists := call.Arguments["_raw"]; exists {
			t.Fatalf("expected normalized arguments without _raw: %+v", call.Arguments)
		}
		path, _ := call.Arguments["path"].(string)
		if strings.TrimSpace(path) == "" {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"error":   "path_required",
					"message": "file.write requires a non-empty path. Provide a workspace-relative file path in `path`.",
				},
				RunID: &runID,
			}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": 5,
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 5 {
		t.Fatalf("model calls = %d, want 5 before validation blocker stops retries", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	guard, ok := parseTaskValidationGuard(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.Count != validationLoopBlockThreshold {
		t.Fatalf("guard count = %d, want %d", guard.Count, validationLoopBlockThreshold)
	}
	if guard.FailureCode != "path_required" {
		t.Fatalf("guard failure_code = %q, want path_required", guard.FailureCode)
	}
	if strings.TrimSpace(guard.AttemptFingerprint) == "" {
		t.Fatal("expected attempt fingerprint on validation guard")
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var pathRequiredResults int
	var successfulWrites int
	var blockerMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"path_required"`) {
			pathRequiredResults++
		}
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/`) {
			successfulWrites++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			blockerMessages++
		}
	}
	if pathRequiredResults != 3 {
		t.Fatalf("path_required tool_results = %d, want 3", pathRequiredResults)
	}
	if successfulWrites != 2 {
		t.Fatalf("successful write tool_results = %d, want 2", successfulWrites)
	}
	if blockerMessages != 1 {
		t.Fatalf("validation blocker system messages = %d, want 1", blockerMessages)
	}
}

func TestTurnEngineIntegrationFileWriteMissingContentRetryStillBlocksTrulyInvalidCalls(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "write the outputs and keep going",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	missingContentRaw := map[string]any{"_raw": `{"path":"docs/outline.md","create_dirs":true}`}
	validCalls := []map[string]any{
		{"_raw": `{"path":"templates/home.html","content":"<section class="hero">
  <h1>Sam.blog</h1>
</section>
","create_dirs":true}`},
		{"_raw": `{"path":"content/posts/hello-world.md","content":"# Hello World

He said "ship it".
","create_dirs":true}`},
	}
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-1", Name: "file.write", Tier: "tier2", Arguments: missingContentRaw}}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "good-1", Name: "file.write", Tier: "tier2", Arguments: validCalls[0]}}}, nil
		case 3:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-2", Name: "file.write", Tier: "tier2", Arguments: missingContentRaw}}}, nil
		case 4:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "good-2", Name: "file.write", Tier: "tier2", Arguments: validCalls[1]}}}, nil
		default:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "missing-3", Name: "file.write", Tier: "tier2", Arguments: missingContentRaw}}}, nil
		}
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		content, ok := call.Arguments["content"].(string)
		if !ok || strings.TrimSpace(content) == "" {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"error":   "content_required",
					"message": "file.write requires content. Provide file contents in `content`.",
				},
				RunID: &runID,
			}, nil
		}
		path, _ := call.Arguments["path"].(string)
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path":      path,
				"byte_size": len(content),
				"created":   true,
			},
			RunID: &runID,
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if modelCalls != 5 {
		t.Fatalf("model calls = %d, want 5 before validation blocker stops retries", modelCalls)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	guard, ok := parseTaskValidationGuard(updatedTask.Metadata)
	if !ok {
		t.Fatal("expected validation guard metadata")
	}
	if !guard.Blocked {
		t.Fatal("expected blocked validation guard")
	}
	if guard.FailureCode != "content_required" {
		t.Fatalf("guard failure_code = %q, want content_required", guard.FailureCode)
	}
	if strings.TrimSpace(guard.AttemptFingerprint) == "" {
		t.Fatal("expected attempt fingerprint on validation guard")
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var contentRequiredResults int
	var successfulWrites int
	var blockerMessages int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"error":"content_required"`) {
			contentRequiredResults++
		}
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"`) {
			successfulWrites++
		}
		if item.Role == "system" && strings.Contains(strings.ToLower(strings.TrimSpace(item.Content)), "validation loop blocked") {
			blockerMessages++
		}
	}
	if contentRequiredResults != 3 {
		t.Fatalf("content_required tool_results = %d, want 3", contentRequiredResults)
	}
	if successfulWrites != 2 {
		t.Fatalf("successful write tool_results = %d, want 2", successfulWrites)
	}
	if blockerMessages != 1 {
		t.Fatalf("validation blocker system messages = %d, want 1", blockerMessages)
	}
}

func TestTurnEngineIntegrationValidationLoopBlockSuppressesFreshWork(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	guardMetadata, err := mergeTaskValidationGuardMetadata(nil, taskValidationGuardState{
		InitialMessageID:   uuid.NewString(),
		Fingerprint:        "file.write:path_required",
		AttemptFingerprint: "file.write:attempt",
		ToolName:           "file.write",
		FailureClass:       "tool_validation",
		FailureCode:        "path_required",
		FailureReason:      "path_required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
		FirstSeenAt:        time.Now().UTC().Format(time.RFC3339Nano),
		LastSeenAt:         time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("mergeTaskValidationGuardMetadata: %v", err)
	}
	taskRecord.WorkStatus = "blocked"
	taskRecord.Metadata = guardMetadata
	if _, err := repo.NewProjectTaskRepo(fixture.pool).Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task: %v", err)
	}

	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "try again",
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

	var queuedJobs int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = $1
	`, AgentTurnJobType).Scan(&queuedJobs); err != nil {
		t.Fatalf("count agent_turn jobs: %v", err)
	}
	if queuedJobs != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", queuedJobs)
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 0 {
		t.Fatalf("turn count = %d, want 0", len(turns))
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

func TestTurnEngineIntegrationFreshKickoffRetryKeepsSingleProjectAndSession(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	freshKickoff := "Restart Sam.blog from scratch as a fresh kickoff. Do not resume archived work."
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateContent(ctx, fixture.userMessage.ID, freshKickoff); err != nil {
		t.Fatalf("UpdateContent fresh kickoff request: %v", err)
	}
	fixture.userMessage.Content = freshKickoff
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "project.create", Tier: "tier1"},
		{Name: "session.create", Tier: "tier1"},
	}}

	projectRepo := repo.NewProjectRepo(fixture.pool)
	dispatched := make([]string, 0, 4)
	createdProjectID := uuid.Nil
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = append(dispatched, call.ID)
		switch strings.TrimSpace(call.Name) {
		case "project.create":
			created, err := projectRepo.Create(ctx, repo.Project{
				OrganizationID: fixture.org.ID,
				Slug:           "sam-blog-fresh",
				DisplayName:    "Sam Blog Fresh",
				DeliveryMode:   "gated",
				CreatedByType:  "system",
				CreatedByID:    uuid.Nil,
				Settings:       json.RawMessage(`{}`),
			})
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			createdProjectID = created.ID
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"project": map[string]any{
						"id":   created.ID,
						"slug": created.Slug,
						"name": created.DisplayName,
					},
				},
			}, nil
		case "session.create":
			scopeID, err := uuid.Parse(stringValue(call.Arguments["scope_id"]))
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: fmt.Sprintf("invalid scope_id: %v", err)}, nil
			}
			session, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
				OrganizationID: fixture.org.ID,
				ScopeType:      stringValue(call.Arguments["scope_type"]),
				ScopeID:        scopeID,
				Mode:           stringValue(call.Arguments["mode"]),
			})
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"session": map[string]any{
						"id":     session.ID,
						"mode":   session.Mode,
						"status": session.Status,
					},
				},
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
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-1", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog Fresh", "slug": "sam-blog-fresh"}}}}, nil
		case 2:
			if createdProjectID == uuid.Nil {
				t.Fatal("createdProjectID missing before session.create")
			}
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "session-1", Name: "session.create", Tier: "tier1", Arguments: map[string]any{"scope_type": "project", "scope_id": createdProjectID.String(), "mode": "async"}}}}, nil
		case 3:
			return ModelResponse{}, fmt.Errorf("simulated_fresh_kickoff_retry")
		case 4:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "create-retry", Name: "project.create", Tier: "tier1", Arguments: map[string]any{"name": "Sam Blog Fresh", "slug": "sam-blog-fresh"}}}}, nil
		case 5:
			if createdProjectID == uuid.Nil {
				t.Fatal("createdProjectID missing before retry session.create")
			}
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "session-retry", Name: "session.create", Tier: "tier1", Arguments: map[string]any{"scope_type": "project", "scope_id": createdProjectID.String(), "mode": "async"}}}}, nil
		default:
			return ModelResponse{Content: "kickoff stabilized"}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err == nil || !strings.Contains(err.Error(), "simulated_fresh_kickoff_retry") {
		t.Fatalf("first HandleUserMessage error = %v, want simulated retry failure", err)
	}
	if err := fixture.engine.handleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID, nil, 1, nil); err != nil {
		t.Fatalf("retry handleUserMessage: %v", err)
	}

	if strings.Join(dispatched, ",") != "create-1,session-1,session-retry" {
		t.Fatalf("dispatched tier1 calls = %v, want [create-1 session-1 session-retry]", dispatched)
	}
	if createdProjectID == uuid.Nil {
		t.Fatal("expected created project id")
	}

	var activeProjects int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project
		WHERE organization_id = $1
		  AND status = 'active'
	`, fixture.org.ID).Scan(&activeProjects); err != nil {
		t.Fatalf("count active projects: %v", err)
	}
	if activeProjects != 1 {
		t.Fatalf("active project count = %d, want 1", activeProjects)
	}

	var activeSessions int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE organization_id = $1
		  AND scope_type = 'project'
		  AND scope_id = $2
		  AND mode = 'async'
		  AND status = 'active'
	`, fixture.org.ID, createdProjectID).Scan(&activeSessions); err != nil {
		t.Fatalf("count active project sessions: %v", err)
	}
	if activeSessions != 1 {
		t.Fatalf("active project session count = %d, want 1", activeSessions)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	foundBlockedRetry := false
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") || message.ToolCallID == nil || *message.ToolCallID != "create-retry" {
			continue
		}
		if strings.Contains(message.Content, "project already created in this flow") {
			foundBlockedRetry = true
			break
		}
	}
	if !foundBlockedRetry {
		t.Fatal("missing blocked retry project.create tool result")
	}
}

func TestTurnEngineIntegrationFreshKickoffPromptExcludesArchivedPriorRunContext(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	oldContext := "Archived Sam.blog run referenced duplicate temp agents, old flow templates, and stale analysis."
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateContent(ctx, fixture.userMessage.ID, oldContext); err != nil {
		t.Fatalf("UpdateContent old context: %v", err)
	}
	oldAssistantType := "agent"
	oldAssistant, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  fixture.session.ID,
		AuthorType: &oldAssistantType,
		AuthorID:   &fixture.agent.ID,
		Role:       "assistant",
		Content:    "Archived project: duplicate temp agents and old flow templates.",
	})
	if err != nil {
		t.Fatalf("AppendMessage old assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, oldAssistant.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus old assistant: %v", err)
	}

	freshMessageText := "Start Sam.blog from scratch as a fresh kickoff. Do not resume the archived run."
	authorType := "human_user"
	freshMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  fixture.session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    freshMessageText,
	})
	if err != nil {
		t.Fatalf("AppendMessage fresh kickoff: %v", err)
	}

	retriever := &capturingMemoryRetriever{
		memories: []memory.RankedMemory{{
			Memory: repo.Memory{
				ID:      uuid.New(),
				Content: "archived prior-run analysis and tool results should not appear",
			},
			Score: 1,
		}},
	}
	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{
		Pool:            fixture.pool,
		MemoryRetriever: retriever,
	})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler

	promptBlob := ""
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		var builder strings.Builder
		builder.WriteString(strings.ToLower(strings.TrimSpace(req.Prompt.SystemPrompt)))
		builder.WriteString("\n")
		for _, message := range req.Prompt.Messages {
			builder.WriteString(strings.ToLower(strings.TrimSpace(message.Content)))
			builder.WriteString("\n")
		}
		promptBlob = builder.String()
		return ModelResponse{Content: "fresh kickoff only"}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, freshMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage fresh kickoff: %v", err)
	}

	if strings.Contains(promptBlob, "duplicate temp agents") || strings.Contains(promptBlob, "old flow templates") || strings.Contains(promptBlob, "archived sam.blog run") {
		t.Fatalf("fresh kickoff prompt unexpectedly included archived prior-run context: %q", promptBlob)
	}
	if !strings.Contains(promptBlob, strings.ToLower(freshMessageText)) {
		t.Fatalf("fresh kickoff prompt missing current request: %q", promptBlob)
	}
	if retriever.calls != 0 {
		t.Fatalf("memory retriever calls = %d, want 0 for fresh kickoff isolation", retriever.calls)
	}
}

func TestTurnEngineIntegrationFreshKickoffCompressionLimitSurfacesSingleBlocker(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	freshKickoff := "Start Sam.blog from scratch as a fresh kickoff."
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateContent(ctx, fixture.userMessage.ID, freshKickoff); err != nil {
		t.Fatalf("UpdateContent fresh kickoff request: %v", err)
	}
	fixture.userMessage.Content = freshKickoff
	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 16}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 16}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 16}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: 16}, err: prompt.ErrContextCompressed},
	}}

	if err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage fresh kickoff compression: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("turn count = %d, want 4 bounded continuation turns", len(turns))
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("final turn status = %q, want completed", turns[len(turns)-1].Status)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	blockerCount := 0
	continuationCount := 0
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "Fresh kickoff blocked:") {
			blockerCount++
		}
		if strings.Contains(message.Content, "Context compressed - continuing in a new turn.") {
			continuationCount++
		}
	}
	if blockerCount != 1 {
		t.Fatalf("fresh kickoff blocker count = %d, want 1", blockerCount)
	}
	if continuationCount != 3 {
		t.Fatalf("continuation notice count = %d, want 3 before blocker", continuationCount)
	}
}

func TestTurnEngineIntegrationRecoveryTurnBlocksAfterGuardrailContinuationDepthEX317(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery continuation depth: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata); ok {
		t.Fatal("unexpected recovery checkpoint metadata")
	}

	runnableJobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, taskSession.ID)
	if runnableJobs != 0 {
		t.Fatalf("runnable agent_turn jobs = %d, want 0", runnableJobs)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("turn count = %d, want 4 bounded continuation turns", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("final turn status = %q, want completed", lastTurn.Status)
	}
	if lastTurn.StopReason == nil || *lastTurn.StopReason != stopReasonRecoveryContinuation {
		t.Fatalf("final turn stop_reason = %v, want %q", lastTurn.StopReason, stopReasonRecoveryContinuation)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var continuationMessages int
	var blockedMessages int
	var queuedMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "Prompt input exceeded 64000-token guardrail - continuing in a new turn.") {
			continuationMessages++
		}
		if strings.Contains(message.Content, "The task is now blocked.") {
			blockedMessages++
		}
		if strings.Contains(message.Content, "follow-on wakeup is already queued") {
			queuedMessages++
		}
	}
	if continuationMessages != 3 {
		t.Fatalf("continuation notice count = %d, want 3", continuationMessages)
	}
	if blockedMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedMessages)
	}
	if queuedMessages != 0 {
		t.Fatalf("queued continuation messages = %d, want 0", queuedMessages)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	_, err = taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"})
	var resumeErr tasksvc.TaskResumeBlockedStateError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("ResumeValidationBlockedTask err = %v, want TaskResumeBlockedStateError", err)
	}
	if resumeErr.BlockerClass != tasksvc.RecoveryBlockerClassBlockedWithoutResumableState {
		t.Fatalf("resume blocker_class = %q, want %q", resumeErr.BlockerClass, tasksvc.RecoveryBlockerClassBlockedWithoutResumableState)
	}
}

func TestTurnEngineIntegrationRecoveryTurnKeepsQueuedContinuationAfterGuardrailContinuationDepthEX318(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}

	if _, err := fixture.engine.enqueuer.Enqueue(ctx, nil, AgentTurnJobType, defaultAgentTurnJobPriority, AgentTurnPayload{
		SessionID: taskSession.ID,
		MessageID: recoveryMessage.ID,
	}, nil); err != nil {
		t.Fatalf("enqueue queued continuation: %v", err)
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery continuation depth with queued wakeup: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress with queued continuation", updatedTask.WorkStatus)
	}

	runnableJobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, taskSession.ID)
	if runnableJobs != 1 {
		t.Fatalf("runnable agent_turn jobs = %d, want 1 queued continuation", runnableJobs)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("turn count = %d, want 4 bounded continuation turns", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("final turn status = %q, want completed", lastTurn.Status)
	}
	if lastTurn.StopReason == nil || *lastTurn.StopReason != stopReasonRecoveryContinuation {
		t.Fatalf("final turn stop_reason = %v, want %q", lastTurn.StopReason, stopReasonRecoveryContinuation)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var blockedMessages int
	var queuedMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "The task is now blocked.") {
			blockedMessages++
		}
		if strings.Contains(message.Content, "follow-on wakeup is already queued") {
			queuedMessages++
		}
	}
	if blockedMessages != 0 {
		t.Fatalf("blocked guidance messages = %d, want 0", blockedMessages)
	}
	if queuedMessages != 1 {
		t.Fatalf("queued continuation messages = %d, want 1", queuedMessages)
	}
}

func TestTurnEngineIntegrationRecoveryTurnPreservesResumableStateAfterGuardrailContinuationDepthEX327(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	const targetPath = "docs/content-strategy.md"
	const placeholderDraft = "Now I have everything I need. Let me write the comprehensive content strategy document. This needs to be the single deliverable that unblocks WS4 and serves as the strategic foundation for Sam.blog."

	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(placeholderDraft), 0o644); err != nil {
		t.Fatalf("write placeholder target: %v", err)
	}

	priorTurn, err := fixture.chatService.CreateTurn(ctx, taskSession.ID, fixture.agent.ID)
	if err != nil {
		t.Fatalf("CreateTurn prior recovery: %v", err)
	}
	if err := fixture.chatService.StartTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("StartTurn prior recovery: %v", err)
	}
	assistantType := "agent"
	assistantMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		TurnID:     &priorTurn.ID,
		AuthorType: &assistantType,
		AuthorID:   &fixture.agent.ID,
		Role:       "assistant",
		Content:    placeholderDraft,
	})
	if err != nil {
		t.Fatalf("AppendMessage prior assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, assistantMessage.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior assistant: %v", err)
	}
	fileWriteResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: taskSession.ID,
		TurnID:    &priorTurn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.write", "output": map[string]any{"path": targetPath, "byte_size": len(placeholderDraft), "created": false}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage prior file.write tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileWriteResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus prior file.write tool_result: %v", err)
	}
	if err := fixture.chatService.CompleteTurn(ctx, priorTurn.ID); err != nil {
		t.Fatalf("CompleteTurn prior recovery: %v", err)
	}

	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage recovery continuation depth: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatalf("expected recovery checkpoint metadata, metadata=%s", string(updatedTask.Metadata))
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if !strings.Contains(checkpoint.FailureReason, "prompt input kept exceeding the 64000-token guardrail across 3 continuation turns") {
		t.Fatalf("checkpoint failure_reason = %q, want continuation-depth guidance", checkpoint.FailureReason)
	}

	outputBody, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read placeholder target: %v", err)
	}
	if string(outputBody) != placeholderDraft {
		t.Fatalf("target file changed unexpectedly:\n%s", string(outputBody))
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var blockedMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "Continue from `docs/content-strategy.md`") {
			blockedMessages++
		}
	}
	if blockedMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1 with target-path recovery guidance", blockedMessages)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	resumed, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	resumedCheckpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(resumed.Metadata)
	if !ok {
		t.Fatalf("expected checkpoint to remain after resume, metadata=%s", string(resumed.Metadata))
	}
	if resumedCheckpoint.TargetPath != targetPath {
		t.Fatalf("resumed checkpoint target_path = %q, want %q", resumedCheckpoint.TargetPath, targetPath)
	}
}

func TestTurnEngineIntegrationRecoveryResumeHardensRepeatedIntentOnlyDraftEX329(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, initialUserMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	const targetPath = "docs/content-strategy.md"
	const priorPlaceholder = "Excellent. I now have a thorough understanding of the strategic direction for Sam.blog. Let me write the full document now."
	const priorFailureReason = "assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body"
	const repeatedPlaceholder = "Ready to draft the comprehensive content strategy for Sam.blog. This is the deliverable that unblocks WS4 and sets the strategic direction for the project."

	artifactDraft := strings.TrimSpace(`# Content Strategy

## Core Promise
Sam.blog publishes one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Family systems that reduce chaos and increase agency.
- Honest stories about work, stewardship, and craft.
- Experiments that turn reflection into repeatable practice.
`)

	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(priorPlaceholder), 0o644); err != nil {
		t.Fatalf("write placeholder target draft: %v", err)
	}

	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	artifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactDoc := buildRecoveryFileWriteArtifactDocument(buildTaskLabel(taskRecord), targetPath, artifactDraft, priorFailureReason, nil, time.Now().UTC())
	if err := os.WriteFile(artifactAbs, []byte(artifactDoc), 0o644); err != nil {
		t.Fatalf("write recovery artifact: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task before checkpoint seed: %v", err)
	}
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(currentTask.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            targetPath,
		ArtifactPath:          artifactRel,
		FailureReason:         priorFailureReason,
		HistoryStartMessageID: initialUserMessage.ID.String(),
		HaltTurnID:            uuid.NewString(),
		UpdatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	currentTask.Metadata = checkpointMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update task with recovery checkpoint: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	blockerReason := "recovery halted after assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body; resume from .ottercamp/recovery/docs/content-strategy.md and re-queue only after concrete content exists"
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, blockerReason, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	resumed, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	if resumed.WorkStatus != "queued" {
		t.Fatalf("resumed work_status = %q, want queued", resumed.WorkStatus)
	}
	resumedCheckpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(resumed.Metadata)
	if !ok {
		t.Fatalf("expected checkpoint to remain after resume, metadata=%s", string(resumed.Metadata))
	}
	if resumedCheckpoint.FailureReason != priorFailureReason {
		t.Fatalf("resumed checkpoint failure_reason = %q, want %q", resumedCheckpoint.FailureReason, priorFailureReason)
	}
	if resumedCheckpoint.ArtifactPath != artifactRel {
		t.Fatalf("resumed checkpoint artifact_path = %q, want %q", resumedCheckpoint.ArtifactPath, artifactRel)
	}

	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    buildTaskQueueKickoffMessageForTest(taskRecord),
		Metadata: mustJSON(t, map[string]any{
			"source":                    "task_queue_processor",
			"recovery_action":           "resume_validation_blocked_task",
			"validation_tool_name":      "cli.execute",
			"validation_failure_code":   "command_required",
			"validation_failure_reason": "command is required",
		}),
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery kickoff: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}

	promptBlob := ""
	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		if modelCalls > 1 {
			return ModelResponse{}, fmt.Errorf("unexpected follow-up model call after repeated intent-only recovery draft")
		}
		promptBlob = flattenPrompt(req.Prompt)
		return ModelResponse{
			Content: repeatedPlaceholder,
			ToolCalls: []ModelToolCall{{
				ID:   "resume-write",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}

	dispatched := 0
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		dispatched++
		t.Fatalf("unexpected tier2 dispatch for repeated intent-only recovery draft: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage repeated intent-only recovery resume: %v", err)
	}

	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1 bounded recovery turn", modelCalls)
	}
	if dispatched != 0 {
		t.Fatalf("tier2 dispatches = %d, want 0", dispatched)
	}
	if strings.Contains(promptBlob, priorPlaceholder) {
		t.Fatalf("prompt should omit rejected placeholder target draft:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, artifactDraft) {
		t.Fatalf("prompt missing substantive recovery artifact draft:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, "Prior recovery failure rejected a non-substantive draft.") {
		t.Fatalf("prompt missing hardened recovery contract:\n%s", promptBlob)
	}
	if !strings.Contains(promptBlob, "Existing target file draft: omitted because it matches the previously rejected non-substantive pattern.") {
		t.Fatalf("prompt missing rejected target-draft omission guidance:\n%s", promptBlob)
	}

	outputBody, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read target file after repeated intent-only recovery draft: %v", err)
	}
	if string(outputBody) != priorPlaceholder {
		t.Fatalf("target file changed unexpectedly:\n%s", string(outputBody))
	}

	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after repeated intent-only recovery draft: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatalf("expected repeated intent-only recovery checkpoint metadata, metadata=%s", string(updatedTask.Metadata))
	}
	if checkpoint.TargetPath != targetPath {
		t.Fatalf("checkpoint target_path = %q, want %q", checkpoint.TargetPath, targetPath)
	}
	if checkpoint.ArtifactPath != artifactRel {
		t.Fatalf("checkpoint artifact_path = %q, want %q", checkpoint.ArtifactPath, artifactRel)
	}
	if checkpoint.BlockerClass != taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint {
		t.Fatalf("checkpoint blocker_class = %q, want %q", checkpoint.BlockerClass, taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint)
	}
	if !strings.Contains(checkpoint.FailureReason, "repeated intent-only recovery drafts for docs/content-strategy.md") {
		t.Fatalf("checkpoint failure_reason = %q, want repeated intent-only blocker", checkpoint.FailureReason)
	}
	if len(checkpoint.PriorFailureReasons) != 1 || checkpoint.PriorFailureReasons[0] != priorFailureReason {
		t.Fatalf("checkpoint prior_failure_reasons = %v, want [%q]", checkpoint.PriorFailureReasons, priorFailureReason)
	}

	var blockedPayloadRaw []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&blockedPayloadRaw); err != nil {
		t.Fatalf("load blocked task payload: %v", err)
	}
	var blockedPayload map[string]any
	if err := json.Unmarshal(blockedPayloadRaw, &blockedPayload); err != nil {
		t.Fatalf("unmarshal blocked task payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", blockedPayload["to_status"])); got != "blocked" {
		t.Fatalf("blocked payload to_status = %q, want blocked", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", blockedPayload["recovery_blocker_class"])); got != tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint {
		t.Fatalf("blocked payload recovery_blocker_class = %q, want %q", got, tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint)
	}
	if got := anyStrings(blockedPayload["recovery_checkpoint_prior_failure_reasons"]); len(got) != 1 || got[0] != priorFailureReason {
		t.Fatalf("blocked payload prior_failure_reasons = %v, want [%q]", got, priorFailureReason)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	if lastTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", lastTurn.Status)
	}
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != stopReasonRecoveryFileRejected {
		t.Fatalf("turn stop_reason = %q, want %q", gotStopReason, stopReasonRecoveryFileRejected)
	}

	artifactBody, err := os.ReadFile(artifactAbs)
	if err != nil {
		t.Fatalf("read repeated recovery artifact: %v", err)
	}
	artifactText := string(artifactBody)
	if !strings.Contains(artifactText, repeatedPlaceholder) {
		t.Fatalf("artifact missing repeated rejected placeholder draft:\n%s", artifactText)
	}
	if !strings.Contains(artifactText, "repeated intent-only recovery drafts for docs/content-strategy.md") {
		t.Fatalf("artifact missing hardened repeated-intent blocker reason:\n%s", artifactText)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var haltMessages int
	var toolResults int
	for _, item := range messages {
		if item.Role == "tool_result" && strings.Contains(item.Content, `"path":"docs/content-strategy.md"`) {
			toolResults++
		}
		if item.Role == "system" && strings.Contains(item.Content, "hardened recovery checkpoint") {
			haltMessages++
		}
	}
	if haltMessages != 1 {
		t.Fatalf("hardened halt messages = %d, want 1", haltMessages)
	}
	if toolResults != 0 {
		t.Fatalf("file.write tool_results = %d, want 0", toolResults)
	}

	resumedAgain, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"})
	if err != nil {
		t.Fatalf("ResumeValidationBlockedTask after hardened blocker: %v", err)
	}
	if resumedAgain.WorkStatus != "queued" {
		t.Fatalf("resumedAgain work_status = %q, want queued", resumedAgain.WorkStatus)
	}
	resumedAgainCheckpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(resumedAgain.Metadata)
	if !ok {
		t.Fatalf("expected checkpoint after hardened resume, metadata=%s", string(resumedAgain.Metadata))
	}
	if resumedAgainCheckpoint.BlockerClass != taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint {
		t.Fatalf("resumedAgain checkpoint blocker_class = %q, want %q", resumedAgainCheckpoint.BlockerClass, taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint)
	}
	if len(resumedAgainCheckpoint.PriorFailureReasons) != 1 || resumedAgainCheckpoint.PriorFailureReasons[0] != priorFailureReason {
		t.Fatalf("resumedAgain checkpoint prior_failure_reasons = %v, want [%q]", resumedAgainCheckpoint.PriorFailureReasons, priorFailureReason)
	}

	var resumePayloadRaw []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&resumePayloadRaw); err != nil {
		t.Fatalf("load resume payload after hardened blocker: %v", err)
	}
	var resumePayload map[string]any
	if err := json.Unmarshal(resumePayloadRaw, &resumePayload); err != nil {
		t.Fatalf("unmarshal resume payload after hardened blocker: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", resumePayload["to_status"])); got != "queued" {
		t.Fatalf("resume payload to_status = %q, want queued", got)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", resumePayload["recovery_blocker_class"])); got != tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint {
		t.Fatalf("resume payload recovery_blocker_class = %q, want %q", got, tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint)
	}
	if got := anyStrings(resumePayload["recovery_checkpoint_prior_failure_reasons"]); len(got) != 1 || got[0] != priorFailureReason {
		t.Fatalf("resume payload prior_failure_reasons = %v, want [%q]", got, priorFailureReason)
	}
}

func TestTurnEngineIntegrationRepeatedIntentRecoveryLegacyStopReasonFallbackStillPersistsHardenedBlockerEX330(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	if _, err := fixture.pool.Exec(ctx, `
		ALTER TABLE chat_turn
			DROP CONSTRAINT IF EXISTS chat_turn_stop_reason_check;
		ALTER TABLE chat_turn
			ADD CONSTRAINT chat_turn_stop_reason_check
			CHECK (
				stop_reason IS NULL
				OR stop_reason IN (
					'max_tool_calls',
					'max_duration',
					'user_cancelled',
					'user_steered',
					'model_error',
					'session_closed',
					'validation_loop_blocked'
				)
			);
	`); err != nil {
		t.Fatalf("set legacy chat_turn stop_reason constraint: %v", err)
	}

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, initialUserMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	const targetPath = "docs/content-strategy.md"
	const priorPlaceholder = "Excellent. I now have a thorough understanding of the strategic direction for Sam.blog. Let me write the full document now."
	const priorFailureReason = "assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body"
	const repeatedPlaceholder = "Ready to draft the comprehensive content strategy for Sam.blog. This is the deliverable that unblocks WS4 and sets the strategic direction for the project."

	artifactDraft := strings.TrimSpace(`# Content Strategy

## Core Promise
Sam.blog publishes one durable operating system for thoughtful parents building resilient families and meaningful work.
`)

	targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(priorPlaceholder), 0o644); err != nil {
		t.Fatalf("write placeholder target draft: %v", err)
	}

	artifactRel := filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(targetPath)))
	artifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactDoc := buildRecoveryFileWriteArtifactDocument(buildTaskLabel(taskRecord), targetPath, artifactDraft, priorFailureReason, nil, time.Now().UTC())
	if err := os.WriteFile(artifactAbs, []byte(artifactDoc), 0o644); err != nil {
		t.Fatalf("write recovery artifact: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task before checkpoint seed: %v", err)
	}
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(currentTask.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            targetPath,
		ArtifactPath:          artifactRel,
		FailureReason:         priorFailureReason,
		HistoryStartMessageID: initialUserMessage.ID.String(),
		HaltTurnID:            uuid.NewString(),
		UpdatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	currentTask.Metadata = checkpointMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update task with recovery checkpoint: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	blockerReason := "recovery halted after assistant draft for docs/content-strategy.md described intent to write the deliverable instead of the file body; resume from .ottercamp/recovery/docs/content-strategy.md and re-queue only after concrete content exists"
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, blockerReason, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if _, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}

	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    buildTaskQueueKickoffMessageForTest(taskRecord),
		Metadata: mustJSON(t, map[string]any{
			"source":                    "task_queue_processor",
			"recovery_action":           "resume_validation_blocked_task",
			"validation_tool_name":      "cli.execute",
			"validation_failure_code":   "command_required",
			"validation_failure_reason": "command is required",
		}),
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery kickoff: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{
			Content: repeatedPlaceholder,
			ToolCalls: []ModelToolCall{{
				ID:   "resume-write",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path": targetPath,
				},
			}},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, _ func(runID uuid.UUID)) (ToolResult, error) {
		t.Fatalf("unexpected tier2 dispatch for legacy repeated intent-only recovery draft: %+v", call)
		return ToolResult{}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, recoveryMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage legacy repeated intent-only recovery resume: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("turn count = %d, want 1 bounded recovery turn", len(turns))
	}
	lastTurn := turns[len(turns)-1]
	gotStopReason := ""
	if lastTurn.StopReason != nil {
		gotStopReason = strings.TrimSpace(*lastTurn.StopReason)
	}
	if gotStopReason != "model_error" {
		t.Fatalf("legacy stop_reason = %q, want model_error fallback", gotStopReason)
	}

	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after legacy repeated intent-only recovery draft: %v", err)
	}
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updatedTask.Metadata)
	if !ok {
		t.Fatalf("expected checkpoint metadata after legacy repeated recovery draft, metadata=%s", string(updatedTask.Metadata))
	}
	if checkpoint.BlockerClass != taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint {
		t.Fatalf("checkpoint blocker_class = %q, want %q", checkpoint.BlockerClass, taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint)
	}
	if len(checkpoint.PriorFailureReasons) != 1 || checkpoint.PriorFailureReasons[0] != priorFailureReason {
		t.Fatalf("checkpoint prior_failure_reasons = %v, want [%q]", checkpoint.PriorFailureReasons, priorFailureReason)
	}

	var blockedPayloadRaw []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM project_task_event
		WHERE task_id = $1
		  AND event_type = 'status.changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskRecord.ID).Scan(&blockedPayloadRaw); err != nil {
		t.Fatalf("load legacy blocked payload: %v", err)
	}
	var blockedPayload map[string]any
	if err := json.Unmarshal(blockedPayloadRaw, &blockedPayload); err != nil {
		t.Fatalf("unmarshal legacy blocked payload: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", blockedPayload["recovery_blocker_class"])); got != tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint {
		t.Fatalf("legacy blocked payload recovery_blocker_class = %q, want %q", got, tasksvc.RecoveryBlockerClassRepeatedNonSubstantiveRecoveryCheckpoint)
	}
	if got := anyStrings(blockedPayload["recovery_checkpoint_prior_failure_reasons"]); len(got) != 1 || got[0] != priorFailureReason {
		t.Fatalf("legacy blocked payload prior_failure_reasons = %v, want [%q]", got, priorFailureReason)
	}
}

func TestTurnEngineIntegrationRecoveryGuardrailDoesNotCountCurrentClaimedJobAsQueuedWakeupEX323(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	dataDir := t.TempDir()
	fixture.engine.dataDir = dataDir

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	taskSession, recoveryMessage := mustCreateTaskQueueRecoveredValidationTaskSession(t, ctx, fixture, taskRecord)

	_ = mustPersistRecoveryResumeFixture(t, ctx, fixture, taskRecord, recoveryMessage.ID)
	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}

	worker := jobqueue.New(fixture.pool, nil, jobqueue.Config{})
	jobID, err := worker.Enqueue(ctx, nil, AgentTurnJobType, defaultAgentTurnJobPriority, AgentTurnPayload{
		SessionID: taskSession.ID,
		MessageID: recoveryMessage.ID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueue claimed recovery job: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		UPDATE job_queue
		SET status = 'claimed',
		    claimed_by = 'integration-worker',
		    claimed_at = now()
		WHERE id = $1
	`, jobID); err != nil {
		t.Fatalf("mark recovery job claimed: %v", err)
	}

	var payload []byte
	if err := fixture.pool.QueryRow(ctx, `
		SELECT payload
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&payload); err != nil {
		t.Fatalf("load claimed recovery payload: %v", err)
	}
	claimedJob := jobqueue.Job{
		ID:      jobID,
		JobType: AgentTurnJobType,
		Payload: payload,
		Status:  "claimed",
	}

	if err := fixture.engine.HandleTurnJob(ctx, claimedJob); err != nil {
		t.Fatalf("HandleTurnJob recovery continuation depth: %v", err)
	}

	updatedTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after claimed recovery run: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	var extraQueuedJobs int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = $1
		  AND status IN ('pending', 'claimed')
		  AND payload->>'session_id' = $2
		  AND id <> $3
	`, AgentTurnJobType, taskSession.ID.String(), jobID).Scan(&extraQueuedJobs); err != nil {
		t.Fatalf("count extra queued recovery jobs: %v", err)
	}
	if extraQueuedJobs != 0 {
		t.Fatalf("extra queued recovery jobs = %d, want 0", extraQueuedJobs)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var queuedMessages int
	var blockedMessages int
	var resumeStateMessages int
	var continuationSummaries int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "follow-on wakeup is already queued") {
			queuedMessages++
		}
		if strings.Contains(message.Content, "The task is now blocked.") {
			blockedMessages++
		}
		if strings.Contains(message.Content, "[Recovery resume state]") {
			resumeStateMessages++
		}
		if strings.Contains(message.Content, "[Continuation summary]") {
			continuationSummaries++
		}
	}
	if queuedMessages != 0 {
		t.Fatalf("queued continuation messages = %d, want 0 when only the current claimed job exists", queuedMessages)
	}
	if blockedMessages != 1 {
		t.Fatalf("blocked guidance messages = %d, want 1", blockedMessages)
	}
	if continuationSummaries != 0 {
		t.Fatalf("continuation summary messages = %d, want 0 for artifact-seeded recovery continuations", continuationSummaries)
	}
	if resumeStateMessages != 4 {
		t.Fatalf("recovery resume state messages = %d, want 4 bounded turns", resumeStateMessages)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 for artifact-seeded recovery continuations", fixture.model.continuationSummaryCalls)
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

func TestTurnEngineIntegrationProjectBootstrapQueuesFollowOnAfterLoriAcknowledgement(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start staffing and setup for this new project.")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage project bootstrap acknowledgement: %v", err)
	}

	latestTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": latestTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent bootstrap acknowledgement: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 1 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 1 follow-on job", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusActive {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusActive)
	}
	if bootstrapState.AutoTurnCount != 1 {
		t.Fatalf("bootstrap auto_turn_count = %d, want 1", bootstrapState.AutoTurnCount)
	}
	if bootstrapState.PlannedTaskCount != 0 || bootstrapState.PlannedFlowTemplateCount != 0 || bootstrapState.AssignmentCount != 0 {
		t.Fatalf("bootstrap progress = %+v, want zero persisted setup before follow-on turn", bootstrapState)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointStaffingPersisted)
	}
	if bootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("bootstrap last_successful_checkpoint = %q, want %q", bootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointProjectCreated); checkpoint.Status != projectBootstrapCheckpointStatusCompleted {
		t.Fatalf("project_created checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusCompleted)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointStaffingPersisted); checkpoint.Status != projectBootstrapCheckpointStatusPending {
		t.Fatalf("staffing_persisted checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusPending)
	}

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	projectBootstrapState := mustProjectBootstrapProjectState(t, projectRecord)
	if projectBootstrapState.CurrentPhase != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("project settings bootstrap current_phase = %q, want %q", projectBootstrapState.CurrentPhase, projectBootstrapCheckpointStaffingPersisted)
	}
	if projectBootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("project settings bootstrap last_successful_checkpoint = %q, want %q", projectBootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
}

func TestTurnEngineIntegrationProjectBootstrapPromotesFirstWaveBeforeBootstrapGateCompletes(t *testing.T) {
	fixture := newIntegrationFixture(t)
	enableTaskQueueProcessor(t, fixture)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-1",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	assignments, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject assignments: %v", err)
	}
	if len(assignments) == 0 {
		t.Fatal("expected persisted project assignment after self-start bootstrap")
	}

	tasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	var plannedTasks int
	firstWaveTaskIDs := make([]uuid.UUID, 0, len(tasks))
	for _, item := range tasks {
		var metadata map[string]any
		_ = json.Unmarshal(item.Metadata, &metadata)
		if raw, ok := metadata["bootstrap_gate"].(bool); ok && raw {
			continue
		}
		if raw, ok := metadata["bootstrap_setup_task"].(bool); ok && raw {
			continue
		}
		plannedTasks++
		firstWaveTaskIDs = append(firstWaveTaskIDs, item.ID)
	}
	if plannedTasks == 0 {
		t.Fatal("expected persisted non-bootstrap task after self-start bootstrap")
	}

	var flowTemplateCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE project_id = $1
	`, project.ID).Scan(&flowTemplateCount); err != nil {
		t.Fatalf("count flow templates: %v", err)
	}
	if flowTemplateCount < 2 {
		t.Fatalf("project flow template count = %d, want at least bootstrap + one planned flow", flowTemplateCount)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusActive {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusActive)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationPassed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationPassed)
	}
	if bootstrapState.AssignmentCount == 0 || bootstrapState.PlannedTaskCount == 0 || bootstrapState.PlannedFlowTemplateCount == 0 {
		t.Fatalf("bootstrap state missing persisted counts: %+v", bootstrapState)
	}
	if bootstrapState.FirstWaveTaskCount != 1 {
		t.Fatalf("bootstrap first_wave_task_count = %d, want 1 leaf child task", bootstrapState.FirstWaveTaskCount)
	}
	if bootstrapState.FirstWavePromotedCount == 0 {
		t.Fatalf("bootstrap first_wave_promoted_count = %d, want > 0 while gate is still open", bootstrapState.FirstWavePromotedCount)
	}
	if !bootstrapState.BootstrapTaskOutstanding {
		t.Fatal("bootstrap gate marked complete too early; want outstanding while setup sign-off is still pending")
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
	}
	if bootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("bootstrap last_successful_checkpoint = %q, want %q", bootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointFirstWaveSelected)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointFirstWaveExecutions); checkpoint.Status != projectBootstrapCheckpointStatusPending {
		t.Fatalf("first_wave_executions_created checkpoint status = %q, want %q before gate completion", checkpoint.Status, projectBootstrapCheckpointStatusPending)
	}

	parentStatus := ""
	childStatus := ""
	for _, item := range tasks {
		if item.Title == "First-wave orchestration parent" {
			parentStatus = item.WorkStatus
		}
		if item.Title == "Define the first execution slice" {
			childStatus = item.WorkStatus
		}
	}
	if parentStatus != "draft" {
		t.Fatalf("parent task work_status = %q, want draft while child execution starts", parentStatus)
	}
	if childStatus != "queued" && childStatus != "in_progress" && childStatus != "review" && childStatus != "done" {
		t.Fatalf("child task work_status = %q, want promoted out of draft before bootstrap gate completion", childStatus)
	}
	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap session jobs = %d, want 0 after first-wave child execution starts", jobs)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	projectBootstrapState := mustProjectBootstrapProjectState(t, storedProject)
	if projectBootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
		t.Fatalf("project settings bootstrap current_phase = %q, want %q", projectBootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
	}
	if projectBootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("project settings bootstrap last_successful_checkpoint = %q, want %q", projectBootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointFirstWaveSelected)
	}
	if storedProject.Status != "active" {
		t.Fatalf("project status = %q, want active", storedProject.Status)
	}
	if pauseState := projectpause.Parse(storedProject.Settings); pauseState.IsPaused {
		t.Fatalf("project pause state = %+v, want active execution without pause", pauseState)
	}
	if failureState := projectfailure.Parse(storedProject.Settings); failureState.Action != "" {
		t.Fatalf("automatic failure state = %+v, want empty during valid active execution", failureState)
	}
	if bootstrapState.LastCheckpoint != projectBootstrapCheckpointFirstWave {
		t.Fatalf("bootstrap last_checkpoint = %q, want %q", bootstrapState.LastCheckpoint, projectBootstrapCheckpointFirstWave)
	}
}

func TestTurnEngineIntegrationProjectBootstrapOpensFirstWaveAfterSetupTasksAndFrankSignOff(t *testing.T) {
	fixture := newIntegrationFixture(t)
	enableTaskQueueProcessor(t, fixture)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-open-gate",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "task.status_changed",
		Payload: mustJSON(t, map[string]any{
			"task_id":    signoffTask.ID.String(),
			"project_id": project.ID.String(),
			"to_status":  "done",
		}),
	}); err != nil {
		t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
	}

	tasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	firstWaveTaskIDs := make([]uuid.UUID, 0, len(tasks))
	for _, item := range tasks {
		metadata := messageMetadataMap(item.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			continue
		}
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); bootstrapSetupTask {
			continue
		}
		firstWaveTaskIDs = append(firstWaveTaskIDs, item.ID)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusCompleted {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusCompleted)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationPassed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationPassed)
	}
	if bootstrapState.FirstWavePromotedCount == 0 || bootstrapState.FirstWaveExecutionCount == 0 || bootstrapState.FirstWaveJobCount == 0 {
		t.Fatalf("bootstrap gate-open progress missing first-wave execution counts: %+v", bootstrapState)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveJobsClaimed {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveJobsClaimed)
	}
	if jobCount := waitForRunnableAgentTurnJobsForTasks(t, ctx, fixture.pool, firstWaveTaskIDs, 1); jobCount == 0 {
		t.Fatal("expected runnable first-wave agent_turn jobs after bootstrap gate opened")
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	projectBootstrapState := mustProjectBootstrapProjectState(t, storedProject)
	if projectBootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveJobsClaimed {
		t.Fatalf("project settings bootstrap current_phase = %q, want %q", projectBootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveJobsClaimed)
	}
	if storedProject.Status != "active" {
		t.Fatalf("project status = %q, want active", storedProject.Status)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsWhenSetupTaskOwnsExecutableChildren(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-hidden-child",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		setupTask := mustFindBootstrapSetupTaskBySlug(t, ctx, fixture.pool, project.ID, "decompose-workstreams")
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := "Write the launch brief for the selected first execution slice."
		metadata := mustJSON(t, map[string]any{
			"decomposition_parent_task_id": setupTask.ID.String(),
			"workstream_index":             1,
		})
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
			Metadata:        metadata,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureSetupTaskChildren {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureSetupTaskChildren)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "must stay orchestration-only") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want setup-task orchestration guidance", bootstrapState.ValidationFailureReason)
	}
	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after hidden bootstrap child failure", jobs)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsWhenSetupTaskViolatesBoundedScope(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-oversized",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	setupTask := mustFindBootstrapSetupTaskBySlug(t, ctx, fixture.pool, project.ID, "decompose-workstreams")
	oversizedDescription := "Define the long-term product strategy; create the messaging pillars and personas; write the launch brief and customer email; draft the implementation backlog and analytics plan."
	setupTask.Description = &oversizedDescription
	if _, err := taskRepo.Update(ctx, setupTask); err != nil {
		t.Fatalf("Update oversized bootstrap setup task: %v", err)
	}

	signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "task.status_changed",
		Payload: mustJSON(t, map[string]any{
			"task_id":    signoffTask.ID.String(),
			"project_id": project.ID.String(),
			"to_status":  "done",
		}),
	}); err != nil {
		t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureSetupTaskScope {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureSetupTaskScope)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "bootstrap setup") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want bootstrap setup bounded-scope guidance", bootstrapState.ValidationFailureReason)
	}
	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after setup-task scope failure", jobs)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
}

func TestTurnEngineIntegrationProjectBootstrapAllowsSetupSubtaskCheckpoints(t *testing.T) {
	fixture := newIntegrationFixture(t)
	enableTaskQueueProcessor(t, fixture)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-subtask-checkpoint",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	setupTask := mustFindBootstrapSetupTaskBySlug(t, ctx, fixture.pool, project.ID, "validate-task-shape")
	setupExecution := mustCreateBootstrapSetupTaskExecution(t, ctx, fixture.pool, setupTask)
	createdByID := lori.ID
	if _, err := repo.NewProjectSubtaskRepo(fixture.pool).Create(ctx, repo.ProjectSubtask{
		TaskID:              setupTask.ID,
		FlowNodeExecutionID: setupExecution.ID,
		Title:               "Confirm sizing checklist",
		WorkStatus:          "done",
		CreatedByType:       "agent",
		CreatedByID:         &createdByID,
	}); err != nil {
		t.Fatalf("Create bootstrap setup subtask checkpoint: %v", err)
	}

	signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "task.status_changed",
		Payload: mustJSON(t, map[string]any{
			"task_id":    signoffTask.ID.String(),
			"project_id": project.ID.String(),
			"to_status":  "done",
		}),
	}); err != nil {
		t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusCompleted {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusCompleted)
	}
	if bootstrapState.FirstWaveJobCount == 0 {
		t.Fatalf("bootstrap first_wave_job_count = %d, want > 0 after setup subtasks remain internal checkpoints", bootstrapState.FirstWaveJobCount)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsWhenPersistedSetupDoesNotCreateFirstWaveExecution(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-no-execution",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "task.status_changed",
		Payload: mustJSON(t, map[string]any{
			"task_id":    signoffTask.ID.String(),
			"project_id": project.ID.String(),
			"to_status":  "done",
		}),
	}); err != nil {
		t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureFirstWaveExecution)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "flow_node_execution") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want execution handoff detail", bootstrapState.ValidationFailureReason)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
	}
	if bootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("bootstrap last_successful_checkpoint = %q, want %q", bootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointFirstWaveSelected)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointFirstWaveExecutions); checkpoint.Status != projectBootstrapCheckpointStatusFailed {
		t.Fatalf("first_wave_executions_created checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusFailed)
	}
	if len(bootstrapState.ValidationFindings) != 1 {
		t.Fatalf("bootstrap validation_findings = %d, want 1", len(bootstrapState.ValidationFindings))
	}
	if bootstrapState.ValidationFindings[0].Category != projectBootstrapFindingCategoryExecutionShape {
		t.Fatalf("bootstrap validation finding category = %q, want %q", bootstrapState.ValidationFindings[0].Category, projectBootstrapFindingCategoryExecutionShape)
	}
	if bootstrapState.ValidationFindings[0].Code != "first_wave_executions_missing" {
		t.Fatalf("bootstrap validation finding code = %q, want %q", bootstrapState.ValidationFindings[0].Code, "first_wave_executions_missing")
	}

	tasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	var queuedFirstWaveTasks int
	firstWaveTaskIDs := make([]uuid.UUID, 0, len(tasks))
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			continue
		}
		firstWaveTaskIDs = append(firstWaveTaskIDs, task.ID)
		if task.WorkStatus == "queued" {
			queuedFirstWaveTasks++
		}
	}
	if queuedFirstWaveTasks == 0 {
		t.Fatal("expected bootstrap promotion attempt to move at least one first-wave task out of draft")
	}
	parentStatus := ""
	for _, task := range tasks {
		if task.Title == "First-wave orchestration parent" {
			parentStatus = task.WorkStatus
			break
		}
	}
	if parentStatus != "draft" {
		t.Fatalf("parent task work_status = %q, want draft when bootstrap fails to materialize child execution", parentStatus)
	}
	if jobs := countRunnableAgentTurnJobsForTasks(t, ctx, fixture.pool, firstWaveTaskIDs); jobs != 0 {
		t.Fatalf("runnable first-wave agent_turn jobs = %d, want 0 when bootstrap fails before execution handoff", jobs)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	projectBootstrapState := mustProjectBootstrapProjectState(t, storedProject)
	if projectBootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
		t.Fatalf("project settings bootstrap current_phase = %q, want %q", projectBootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
	}
	if projectBootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("project settings bootstrap last_successful_checkpoint = %q, want %q", projectBootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointFirstWaveSelected)
	}
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureFirstWaveExecution,
		projectBootstrapCheckpointFirstWaveExecutions,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsImmediatelyWhenFirstWavePromotionIsSkipped(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}
	fixture.engine.taskTransitions = &fakeTaskTransitionService{}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-promotion-skipped",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the first execution wave without doing the implementation work in the parent."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "First-wave orchestration parent",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		description := "Implement the first bounded child slice."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			Metadata:        mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 1}),
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"parent_task_id":   parentTask.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureFirstWaveExecution)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "no first-wave child task left draft") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want skipped promotion detail", bootstrapState.ValidationFailureReason)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveExecutions {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveExecutions)
	}
	if bootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("bootstrap last_successful_checkpoint = %q, want %q", bootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointFirstWaveSelected)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointFirstWaveExecutions); checkpoint.Status != projectBootstrapCheckpointStatusFailed {
		t.Fatalf("first_wave_executions_created checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusFailed)
	}

	tasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	for _, task := range tasks {
		if task.Title == "Define the first execution slice" && task.WorkStatus != "draft" {
			t.Fatalf("child task work_status = %q, want draft when promotion was skipped", task.WorkStatus)
		}
		if task.Title == "First-wave orchestration parent" && task.WorkStatus != "draft" {
			t.Fatalf("parent task work_status = %q, want draft orchestration-only state", task.WorkStatus)
		}
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureFirstWaveExecution,
		projectBootstrapCheckpointFirstWaveExecutions,
	)
}

func TestTurnEngineIntegrationProjectBootstrapPreflightStopsRacedPlanningArtifactTurn(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)
	projectRecord := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	workspaceRoot, err := workspace.ProjectRoot(fixture.engine.dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("ProjectRoot: %v", err)
	}
	artifactRel := "planning/prd-spec/oc-1-raced-parent-artifact.md"
	artifactAbs := filepath.Join(workspaceRoot, filepath.FromSlash(artifactRel))
	if err := os.Remove(artifactAbs); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale parent planning artifact: %v", err)
	}

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "bootstrap.setup.persist", Tier: "tier1"},
		{Name: "file.write", Tier: "tier1"},
	}}

	modelCalls := 0
	artifactTurnArmed := false
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-race-preflight",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			if !artifactTurnArmed {
				return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
			}
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "raced-parent-artifact",
				Name: "file.write",
				Tier: "tier1",
				Arguments: map[string]any{
					"path":    artifactRel,
					"content": "# Parent PRD artifact\n\nThis should never be written once bootstrap is known incomplete.\n",
				},
			}}}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		switch call.Name {
		case "bootstrap.setup.persist":
			if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
				AgentID:        pmAgent.ID,
				ProjectID:      project.ID,
				Role:           "pm",
				AssignedByType: "agent",
				AssignedByID:   &lori.ID,
			}); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
			description := "Initial scoped bootstrap workstream"
			taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           "Define the first execution slice",
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  &template.ID,
				AssignedAgentID: &pmAgent.ID,
				CreatedByType:   "agent",
				CreatedByID:     &lori.ID,
			})
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Output: map[string]any{
					"pm_agent_id":      pmAgent.ID.String(),
					"task_id":          taskRecord.ID.String(),
					"flow_template_id": template.ID.String(),
				},
			}, nil
		case "file.write":
			if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			if err := os.WriteFile(artifactAbs, []byte(stringValue(call.Arguments["content"])), 0o644); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"path": artifactRel}}, nil
		default:
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}
	modelCallsBeforePreflight := modelCalls

	completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	artifactTurnArmed = true
	progress, err := fixture.engine.loadProjectBootstrapProgress(ctx, project.ID)
	if err != nil {
		t.Fatalf("loadProjectBootstrapProgress: %v", err)
	}
	if !progress.BootstrapSetupComplete() {
		t.Fatalf("bootstrap setup complete = false, progress = %+v", progress)
	}
	if progress.WaitingForBootstrapGate() {
		t.Fatalf("bootstrap unexpectedly still waiting for gate before raced continuation, progress = %+v", progress)
	}
	continuationMessage, err := fixture.engine.appendProjectBootstrapContinuationMessage(ctx, projectSession.ID, lori.ID, handoff.ID.String(), 1)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessage: %v", err)
	}
	sessionSnapshot, err := fixture.chatService.GetSession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetSession project session: %v", err)
	}
	loriAgent, err := fixture.engine.agents.GetByID(ctx, lori.ID)
	if err != nil {
		t.Fatalf("GetByID lori agent: %v", err)
	}
	turnRecord, _, err := fixture.engine.turns.CreateForMessageAttempt(ctx, projectSession.ID, lori.ID, continuationMessage.ID, 0)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt continuation: %v", err)
	}
	startedTurn, started, err := fixture.engine.startInboundMessageTurn(ctx, turnRecord)
	if err != nil {
		t.Fatalf("startInboundMessageTurn continuation: %v", err)
	}
	if !started {
		t.Fatal("expected raced continuation turn to start")
	}
	handled, err := fixture.engine.handleProjectBootstrapPreflight(ctx, &turnRuntime{
		session:          sessionSnapshot,
		agent:            loriAgent,
		turn:             startedTurn,
		initialMessageID: continuationMessage.ID,
		startedAt:        fixture.engine.turnStartTime(startedTurn),
	})
	if err != nil {
		t.Fatalf("handleProjectBootstrapPreflight: %v", err)
	}
	if !handled {
		t.Fatal("expected raced bootstrap continuation to be blocked by bootstrap preflight")
	}

	if modelCalls != modelCallsBeforePreflight {
		t.Fatalf("model calls = %d, want %d because raced parent planning turn must fail in bootstrap preflight before an extra model round", modelCalls, modelCallsBeforePreflight)
	}
	if _, err := os.Stat(artifactAbs); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parent planning artifact stat err = %v, want not exists", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) == 0 {
		t.Fatal("expected bootstrap turns")
	}
	if turns[len(turns)-1].Status != "failed" {
		t.Fatalf("raced bootstrap turn status = %q, want failed", turns[len(turns)-1].Status)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureFirstWaveExecution)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureFirstWaveExecution,
		projectBootstrapCheckpointFirstWaveExecutions,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsValidationWithoutPersistedAssignments(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-missing-assignment",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := "Define the first reviewable landing page slice and prepare it for implementation."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Define the first execution slice",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after missing-assignment validation failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureMissingAssignments {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureMissingAssignments)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureMissingAssignments {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureMissingAssignments)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureMissingAssignments,
		projectBootstrapCheckpointTaskTree,
	)
}

func TestTurnEngineIntegrationProjectBootstrapPersistsProviderFailureClassification(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start bootstrap setup for this project.")

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{}, ErrAuthFailed
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage bootstrap provider failure: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureProviderAuth {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureProviderAuth)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureProviderAuth {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureProviderAuth)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointStaffingPersisted)
	}
	if bootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("bootstrap last_successful_checkpoint = %q, want %q", bootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointStaffingPersisted); checkpoint.Status != projectBootstrapCheckpointStatusFailed {
		t.Fatalf("staffing_persisted checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusFailed)
	}
	if len(bootstrapState.ValidationFindings) != 1 {
		t.Fatalf("bootstrap validation_findings = %d, want 1", len(bootstrapState.ValidationFindings))
	}
	if bootstrapState.ValidationFindings[0].Category != projectBootstrapFindingCategoryProviderAPI {
		t.Fatalf("bootstrap validation finding category = %q, want %q", bootstrapState.ValidationFindings[0].Category, projectBootstrapFindingCategoryProviderAPI)
	}
	if bootstrapState.ValidationFindings[0].Code != "authentication_failed" {
		t.Fatalf("bootstrap validation finding code = %q, want %q", bootstrapState.ValidationFindings[0].Code, "authentication_failed")
	}

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	projectBootstrapState := mustProjectBootstrapProjectState(t, projectRecord)
	if projectBootstrapState.CurrentPhase != projectBootstrapCheckpointStaffingPersisted {
		t.Fatalf("project settings bootstrap current_phase = %q, want %q", projectBootstrapState.CurrentPhase, projectBootstrapCheckpointStaffingPersisted)
	}
	if projectBootstrapState.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("project settings bootstrap last_successful_checkpoint = %q, want %q", projectBootstrapState.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsValidationWithoutRepoBinding(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	if _, err := fixture.pool.Exec(ctx, `
		DELETE FROM project_environment
		WHERE project_id = $1
	`, project.ID); err != nil {
		t.Fatalf("delete project environments: %v", err)
	}
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-missing-repo-binding",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := "Prepare the first reviewable landing page slice for implementation."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Landing page first slice",
			Description:    &description,
			WorkStatus:     "queued",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after missing-repo validation failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureRepoBinding {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureRepoBinding)
	}
	if bootstrapState.ValidationFailureClass != projectBootstrapFailureRepoBinding {
		t.Fatalf("bootstrap validation_failure_class = %q, want %q", bootstrapState.ValidationFailureClass, projectBootstrapFailureRepoBinding)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "repo/workspace binding") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want repo binding detail", bootstrapState.ValidationFailureReason)
	}
}

func TestTurnEngineIntegrationProjectBootstrapProviderFailurePausesInsteadOfArchiving(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{}, ErrAuthFailed
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage provider-auth bootstrap failure: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureCategory != projectFailureCategoryProvider {
		t.Fatalf("bootstrap failure_category = %q, want %q", bootstrapState.FailureCategory, projectFailureCategoryProvider)
	}
	if bootstrapState.FailureClass != projectFailureClassProviderAuth {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectFailureClassProviderAuth)
	}
	if bootstrapState.ProviderFailureClass != projectFailureClassProviderAuth {
		t.Fatalf("bootstrap provider_failure_class = %q, want %q", bootstrapState.ProviderFailureClass, projectFailureClassProviderAuth)
	}
	if bootstrapState.FailurePhase != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("bootstrap failure_phase = %q, want %q", bootstrapState.FailurePhase, projectBootstrapCheckpointProjectCreated)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "active" {
		t.Fatalf("project status = %q, want active", storedProject.Status)
	}
	pauseState := projectpause.Parse(storedProject.Settings)
	if !pauseState.IsPaused {
		t.Fatalf("project pause state = %+v, want paused", pauseState)
	}
	if !strings.Contains(pauseState.Reason, ErrAuthFailed.Error()) {
		t.Fatalf("project pause reason = %q, want provider-auth detail", pauseState.Reason)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionPause,
		projectFailureCategoryProvider,
		projectFailureClassProviderAuth,
		projectBootstrapCheckpointProjectCreated,
	)
}

func TestTurnEngineIntegrationBootstrapArchiveRestartUsesCanonicalBundleEX342(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Operator brief: launch the migration pipeline from the canonical repo binding and keep the operational handoff intact.")
	handoffMetadata := mustJSON(t, map[string]any{
		"operator_constraints": []string{
			"Keep main branch protected",
			"Reuse the existing production credential binding",
		},
	})
	if _, err := repo.NewChatMessageRepo(fixture.pool).UpdateMetadata(ctx, handoff.ID, handoffMetadata); err != nil {
		t.Fatalf("UpdateMetadata handoff constraints: %v", err)
	}

	assistantType := "agent"
	polluted, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  projectSession.ID,
		AuthorType: &assistantType,
		AuthorID:   &fixture.agent.ID,
		Role:       "assistant",
		Content:    "STALE CHATTER: reuse the old carry-over partial task tree and stale runtime owner.",
	})
	if err != nil {
		t.Fatalf("AppendMessage polluted assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, polluted.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus polluted assistant: %v", err)
	}

	environmentRepo := repo.NewProjectEnvironmentRepo(fixture.pool)
	remoteRepo := repo.NewProjectRemoteRepo(fixture.pool)
	environments, err := environmentRepo.ListByProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListByProject environments: %v", err)
	}
	if len(environments) == 0 {
		t.Fatal("expected canonical project environment binding")
	}
	remote, err := remoteRepo.Create(ctx, repo.ProjectRemote{
		ProjectID:     project.ID,
		Name:          "origin",
		URL:           "git@github.com:samhotchkiss/bootstrap-restart.git",
		IsDefault:     true,
		Transport:     "ssh",
		CredentialRef: stringPtr("github-bootstrap-token"),
	})
	if err != nil {
		t.Fatalf("Create remote: %v", err)
	}
	repoURL := remote.URL
	repoPath := "/tmp/bootstrap/canonical-restart"
	environment := environments[0]
	environment.DeliveryMode = "gated"
	environment.RemoteID = &remote.ID
	environment.RepoURL = &repoURL
	environment.RepoPath = &repoPath
	environment.TargetBranch = "release"
	environment.IsActive = true
	if _, err := environmentRepo.Update(ctx, environment); err != nil {
		t.Fatalf("Update environment: %v", err)
	}

	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start bootstrap setup."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-partial",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		case 3:
			return ModelResponse{Content: "Bootstrap setup was partially persisted."}, nil
		case 4:
			return ModelResponse{Content: "Restart bootstrap acknowledged."}, nil
		default:
			return ModelResponse{Content: "unexpected bootstrap restart call"}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := strings.Join([]string{
			"- Redesign the landing page narrative, responsive layout, and launch messaging for the new product release.",
			"- Wire the billing workflow, plan changes, and support operations into the product and internal tooling.",
			"- Stand up analytics instrumentation, dashboards, alerts, and reporting QA for the first launch wave.",
		}, "\n")
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Carry-over partial task",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}
	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage partial bootstrap turn: %v", err)
	}
	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent partial bootstrap turn: %v", err)
	}

	archivedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if archivedProject.Status != "archived" {
		t.Fatalf("archived project status = %q, want archived", archivedProject.Status)
	}
	bundle := mustProjectBootstrapRestartBundle(t, archivedProject)
	if bundle.OperatorBrief == "" || !strings.Contains(strings.ToLower(bundle.OperatorBrief), "launch the migration pipeline") {
		t.Fatalf("bootstrap restart bundle brief = %q, want canonical operator brief", bundle.OperatorBrief)
	}
	if len(bundle.OperatorConstraints) != 2 {
		t.Fatalf("bootstrap restart constraints = %v, want 2 constraints", bundle.OperatorConstraints)
	}
	if len(bundle.Environments) == 0 || bundle.Environments[0].RepoPath != repoPath || bundle.Environments[0].CredentialRef != "github-bootstrap-token" {
		t.Fatalf("bootstrap restart environment bundle = %+v, want persisted repo/credential binding", bundle.Environments)
	}
	if bundle.RestartProjectID == "" {
		t.Fatalf("expected archived project bundle to record restart_project_id; settings=%s", string(archivedProject.Settings))
	}

	restartedProjectID, err := uuid.Parse(bundle.RestartProjectID)
	if err != nil {
		t.Fatalf("Parse restart_project_id: %v", err)
	}
	restartedProject := mustGetProjectByID(t, ctx, fixture.pool, restartedProjectID)
	if restartedProject.Status != "active" {
		t.Fatalf("restarted project status = %q, want active", restartedProject.Status)
	}
	if restartedProject.ID == archivedProject.ID {
		t.Fatal("expected clean bootstrap restart to create a fresh project record")
	}

	restartedTasks, err := repo.NewProjectTaskRepo(fixture.pool).ListByProject(ctx, restartedProject.ID)
	if err != nil {
		t.Fatalf("ListByProject restarted tasks: %v", err)
	}
	for _, taskRecord := range restartedTasks {
		if taskRecord.Title == "Carry-over partial task" {
			t.Fatalf("restarted project carried forward partial bootstrap task: %+v", taskRecord)
		}
	}

	restartedEnvironments, err := environmentRepo.ListByProject(ctx, restartedProject.ID)
	if err != nil {
		t.Fatalf("ListByProject restarted environments: %v", err)
	}
	if len(restartedEnvironments) == 0 {
		t.Fatal("expected restarted project environment bindings")
	}
	restartedEnvironment := restartedEnvironments[0]
	if pointerString(restartedEnvironment.RepoPath) != repoPath || restartedEnvironment.TargetBranch != "release" {
		t.Fatalf("restarted environment = %+v, want copied repo path + target branch", restartedEnvironment)
	}
	if restartedEnvironment.RemoteID == nil || *restartedEnvironment.RemoteID == uuid.Nil {
		t.Fatalf("restarted environment remote_id = %v, want copied remote binding", restartedEnvironment.RemoteID)
	}
	restartedRemote, err := remoteRepo.GetByID(ctx, *restartedEnvironment.RemoteID)
	if err != nil {
		t.Fatalf("GetByID restarted remote: %v", err)
	}
	if pointerString(restartedRemote.CredentialRef) != "github-bootstrap-token" {
		t.Fatalf("restarted remote credential_ref = %q, want github-bootstrap-token", pointerString(restartedRemote.CredentialRef))
	}

	oldSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID archived project session: %v", err)
	}
	if oldSession.Status != "closed" {
		t.Fatalf("archived project session status = %q, want closed after restart", oldSession.Status)
	}

	restartedSession := mustFindProjectAsyncSession(t, ctx, fixture.pool, fixture.org.ID, restartedProject.ID)
	if restartedSession.ID == projectSession.ID {
		t.Fatal("expected clean bootstrap restart to use a fresh project session")
	}
	restartedMessages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, restartedSession.ID)
	if err != nil {
		t.Fatalf("ListBySession restarted session messages: %v", err)
	}
	restartPrompt := ""
	for _, message := range restartedMessages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			restartPrompt = strings.ToLower(strings.TrimSpace(message.Content))
			break
		}
	}
	if restartPrompt == "" {
		t.Fatal("expected canonical restart prompt message on the fresh bootstrap session")
	}

	restartJobID, restartPayload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, restartedSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, restartPayload.SessionID, restartPayload.MessageID, restartPayload.AgentID, restartPayload.RetryCount, &restartJobID); err != nil {
		t.Fatalf("handleUserMessage clean bootstrap restart: %v", err)
	}

	if strings.Contains(restartPrompt, "stale chatter: reuse the old carry-over partial task tree and stale runtime owner.") || strings.Contains(restartPrompt, "partial bootstrap work item that must not be replayed on restart") {
		t.Fatalf("restart prompt replayed failed project-session chatter: %q", restartPrompt)
	}
	if !strings.Contains(restartPrompt, "launch the migration pipeline") {
		t.Fatalf("restart prompt missing canonical operator brief: %q", restartPrompt)
	}
	if !strings.Contains(restartPrompt, "keep main branch protected") || !strings.Contains(restartPrompt, "github-bootstrap-token") || !strings.Contains(restartPrompt, repoPath) {
		t.Fatalf("restart prompt missing canonical constraints/bindings: %q", restartPrompt)
	}
}

func TestTurnEngineIntegrationBootstrapRetriesAreBoundedAndEscalateEX343(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start staffing and setup for this new project.")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "Acknowledged. I am still working through the bootstrap setup."}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial stalled bootstrap turn: %v", err)
	}
	runBootstrapFollowOnCycleToFailure(t, ctx, fixture, projectSession.ID)

	initialArchived := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if initialArchived.Status != "archived" {
		t.Fatalf("initial project status = %q, want archived", initialArchived.Status)
	}
	initialFailureState := projectfailure.Parse(initialArchived.Settings)
	if initialFailureState.RetryBudget != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("initial retry_budget = %d, want %d; settings=%s", initialFailureState.RetryBudget, defaultProjectBootstrapRestartRetryBudget, string(initialArchived.Settings))
	}
	if initialFailureState.RetryAttemptCount != 0 {
		t.Fatalf("initial retry_attempt_count = %d, want 0 before the first restart is consumed", initialFailureState.RetryAttemptCount)
	}
	if len(initialFailureState.FailureHistory) != 1 {
		t.Fatalf("initial failure_history len = %d, want 1", len(initialFailureState.FailureHistory))
	}
	if initialFailureState.FailureHistory[0].FailureClass != projectBootstrapFailureStalled {
		t.Fatalf("initial failure_history[0].failure_class = %q, want %q", initialFailureState.FailureHistory[0].FailureClass, projectBootstrapFailureStalled)
	}
	if initialFailureState.FailureHistory[0].LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("initial failure_history[0].last_successful_checkpoint = %q, want %q", initialFailureState.FailureHistory[0].LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if initialFailureState.FailureHistory[0].SetupPersisted {
		t.Fatal("initial failure_history[0].setup_persisted = true, want false")
	}

	initialBundle := mustProjectBootstrapRestartBundle(t, initialArchived)
	if initialBundle.RetryBudget != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("initial restart bundle retry_budget = %d, want %d", initialBundle.RetryBudget, defaultProjectBootstrapRestartRetryBudget)
	}
	if initialBundle.RetryAttemptCount != 1 {
		t.Fatalf("initial restart bundle retry_attempt_count = %d, want 1 after first restart launch", initialBundle.RetryAttemptCount)
	}
	if len(initialBundle.FailureHistory) != 1 {
		t.Fatalf("initial restart bundle failure_history len = %d, want 1", len(initialBundle.FailureHistory))
	}
	restart1ID, err := uuid.Parse(initialBundle.RestartProjectID)
	if err != nil {
		t.Fatalf("Parse restart1 project id: %v", err)
	}

	restart1Project := mustGetProjectByID(t, ctx, fixture.pool, restart1ID)
	if restart1Project.Status != "active" {
		t.Fatalf("restart1 project status = %q, want active", restart1Project.Status)
	}
	restart1Bundle := mustProjectBootstrapRestartBundle(t, restart1Project)
	if restart1Bundle.RetryAttemptCount != 1 {
		t.Fatalf("restart1 retry_attempt_count = %d, want 1", restart1Bundle.RetryAttemptCount)
	}
	if len(restart1Bundle.FailureHistory) != 1 {
		t.Fatalf("restart1 failure_history len = %d, want 1", len(restart1Bundle.FailureHistory))
	}
	restart1Session := mustFindProjectAsyncSession(t, ctx, fixture.pool, fixture.org.ID, restart1Project.ID)
	runQueuedBootstrapTurnCycleToFailure(t, ctx, fixture, restart1Session.ID)

	restart1Archived := mustGetProjectByID(t, ctx, fixture.pool, restart1Project.ID)
	if restart1Archived.Status != "archived" {
		t.Fatalf("restart1 archived status = %q, want archived", restart1Archived.Status)
	}
	restart1FailureState := projectfailure.Parse(restart1Archived.Settings)
	if restart1FailureState.RetryAttemptCount != 1 {
		t.Fatalf("restart1 retry_attempt_count = %d, want 1", restart1FailureState.RetryAttemptCount)
	}
	if len(restart1FailureState.FailureHistory) != 2 {
		t.Fatalf("restart1 failure_history len = %d, want 2", len(restart1FailureState.FailureHistory))
	}
	restart1Bundle = mustProjectBootstrapRestartBundle(t, restart1Archived)
	if restart1Bundle.RetryAttemptCount != 2 {
		t.Fatalf("restart1 restart bundle retry_attempt_count = %d, want 2 after second restart launch", restart1Bundle.RetryAttemptCount)
	}
	if len(restart1Bundle.FailureHistory) != 2 {
		t.Fatalf("restart1 restart bundle failure_history len = %d, want 2", len(restart1Bundle.FailureHistory))
	}
	restart2ID, err := uuid.Parse(restart1Bundle.RestartProjectID)
	if err != nil {
		t.Fatalf("Parse restart2 project id: %v", err)
	}

	restart2Project := mustGetProjectByID(t, ctx, fixture.pool, restart2ID)
	if restart2Project.Status != "active" {
		t.Fatalf("restart2 project status = %q, want active", restart2Project.Status)
	}
	restart2Bundle := mustProjectBootstrapRestartBundle(t, restart2Project)
	if restart2Bundle.RetryAttemptCount != 2 {
		t.Fatalf("restart2 retry_attempt_count = %d, want 2", restart2Bundle.RetryAttemptCount)
	}
	if len(restart2Bundle.FailureHistory) != 2 {
		t.Fatalf("restart2 failure_history len = %d, want 2", len(restart2Bundle.FailureHistory))
	}
	runQueuedBootstrapTurnCycleToFailure(t, ctx, fixture, mustFindProjectAsyncSession(t, ctx, fixture.pool, fixture.org.ID, restart2Project.ID).ID)

	finalArchived := mustGetProjectByID(t, ctx, fixture.pool, restart2Project.ID)
	if finalArchived.Status != "archived" {
		t.Fatalf("final archived project status = %q, want archived", finalArchived.Status)
	}
	finalBundle := mustProjectBootstrapRestartBundle(t, finalArchived)
	if finalBundle.RetryAttemptCount != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("final retry_attempt_count = %d, want %d", finalBundle.RetryAttemptCount, defaultProjectBootstrapRestartRetryBudget)
	}
	if strings.TrimSpace(finalBundle.RestartProjectID) != "" {
		t.Fatalf("final restart_project_id = %q, want empty after retry exhaustion", finalBundle.RestartProjectID)
	}
	if len(finalBundle.FailureHistory) != defaultProjectBootstrapRestartRetryBudget+1 {
		t.Fatalf("final failure_history len = %d, want %d", len(finalBundle.FailureHistory), defaultProjectBootstrapRestartRetryBudget+1)
	}

	finalFailureState := projectfailure.Parse(finalArchived.Settings)
	if finalFailureState.Action != projectFailureActionArchive {
		t.Fatalf("final automatic_failure action = %q, want %q", finalFailureState.Action, projectFailureActionArchive)
	}
	if finalFailureState.FailureClass != projectBootstrapFailureStalled {
		t.Fatalf("final automatic_failure failure_class = %q, want %q", finalFailureState.FailureClass, projectBootstrapFailureStalled)
	}
	if finalFailureState.FailurePhase != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("final automatic_failure failure_phase = %q, want %q", finalFailureState.FailurePhase, projectBootstrapCheckpointProjectCreated)
	}
	if finalFailureState.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("final automatic_failure last_successful_checkpoint = %q, want %q", finalFailureState.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if finalFailureState.SetupPersisted {
		t.Fatal("final automatic_failure setup_persisted = true, want false")
	}
	if finalFailureState.RetryBudget != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("final automatic_failure retry_budget = %d, want %d", finalFailureState.RetryBudget, defaultProjectBootstrapRestartRetryBudget)
	}
	if finalFailureState.RetryAttemptCount != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("final automatic_failure retry_attempt_count = %d, want %d", finalFailureState.RetryAttemptCount, defaultProjectBootstrapRestartRetryBudget)
	}
	if len(finalFailureState.FailureHistory) != defaultProjectBootstrapRestartRetryBudget+1 {
		t.Fatalf("final automatic_failure failure_history len = %d, want %d", len(finalFailureState.FailureHistory), defaultProjectBootstrapRestartRetryBudget+1)
	}
	lastFailure := finalFailureState.FailureHistory[len(finalFailureState.FailureHistory)-1]
	if lastFailure.RetryAttemptCount != defaultProjectBootstrapRestartRetryBudget {
		t.Fatalf("final failure_history last retry_attempt_count = %d, want %d", lastFailure.RetryAttemptCount, defaultProjectBootstrapRestartRetryBudget)
	}
	if lastFailure.FailureClass != projectBootstrapFailureStalled {
		t.Fatalf("final failure_history last failure_class = %q, want %q", lastFailure.FailureClass, projectBootstrapFailureStalled)
	}
	if lastFailure.LastCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("final failure_history last last_checkpoint = %q, want %q", lastFailure.LastCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if lastFailure.LastSuccessfulCheckpoint != projectBootstrapCheckpointProjectCreated {
		t.Fatalf("final failure_history last last_successful_checkpoint = %q, want %q", lastFailure.LastSuccessfulCheckpoint, projectBootstrapCheckpointProjectCreated)
	}
	if strings.TrimSpace(lastFailure.FailureReason) == "" {
		t.Fatal("expected final failure_history reason")
	}
	if lastFailure.SetupPersisted {
		t.Fatal("final failure_history last setup_persisted = true, want false")
	}

	if jobs := countRunnableAgentTurnJobsForProject(t, ctx, fixture.pool, restart2Project.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs after retry exhaustion = %d, want 0", jobs)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsValidationForBroadParentOnlySetup(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-parent-only",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := strings.Join([]string{
			"- Redesign the landing page narrative, responsive layout, and launch messaging for the new product release.",
			"- Wire the billing workflow, plan changes, and support operations into the product and internal tooling.",
			"- Stand up analytics instrumentation, dashboards, alerts, and reporting QA for the first launch wave.",
		}, "\n")
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Launch platform epic",
			Description:    &description,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after parent-only validation failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureCompoundParent)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "broad parent workstream") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want broad parent detail", bootstrapState.ValidationFailureReason)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureCompoundParent,
		projectBootstrapCheckpointTaskTree,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsImmediatelyWhenChildCreationDeadEnds(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "task.create", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-child-create-dead-end",
				Name: "task.create",
				Tier: "tier1",
				Arguments: map[string]any{
					"title":       "Landing page first slice",
					"description": "Implement the first bounded landing page slice.",
				},
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "task.create" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected tool call"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := strings.Join([]string{
			"- Redesign the landing page narrative and responsive layout for launch.",
			"- Wire billing handoff, launch analytics, and support readiness across the release.",
		}, "\n")
		if _, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Landing page orchestration gate",
			Description:    &parentDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"error": bootstrapChildTaskBoundednessError,
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if turns[len(turns)-1].Status != "failed" {
		t.Fatalf("bootstrap dead-end turn status = %q, want failed", turns[len(turns)-1].Status)
	}
	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after child creation dead-end", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureCompoundParent)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "broad parent workstream") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want broad parent validation detail", bootstrapState.ValidationFailureReason)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	var boundednessErrors int
	for _, message := range messages {
		if strings.Contains(message.Content, bootstrapChildTaskBoundednessError) {
			boundednessErrors++
		}
	}
	if boundednessErrors == 0 {
		t.Fatalf("bounded child-creation error messages = %d, want >= 1", boundednessErrors)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureCompoundParent,
		projectBootstrapCheckpointTaskTree,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsValidationWhenParentExecutesAheadOfChildren(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-parent-execution-ahead-of-child",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		parentDescription := "Coordinate the landing page implementation and review flow."
		parentTask, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Landing page orchestration gate",
			Description:    &parentDescription,
			WorkStatus:     "queued",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}

		childDescription := "Implement the first bounded landing page slice."
		if _, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Landing page first slice",
			Description:    &childDescription,
			WorkStatus:     "draft",
			FlowTemplateID: &template.ID,
			Metadata:       mustJSON(t, map[string]any{"decomposition_parent_task_id": parentTask.ID.String(), "workstream_index": 2}),
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}

		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"parent_task_id": parentTask.ID.String()}}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureCompoundParent)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "entered execution") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want parent execution detail", bootstrapState.ValidationFailureReason)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureCompoundParent,
		projectBootstrapCheckpointTaskTree,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsValidationForNonRunnableFirstWaveFlow(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: create the initial staffed work plan for this project.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-invalid-flow",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateNonRunnableExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := "Prepare the first reviewable landing page slice for implementation."
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID: fixture.org.ID,
			ProjectID:      project.ID,
			Title:          "Landing page first slice",
			Description:    &description,
			WorkStatus:     "queued",
			FlowTemplateID: &template.ID,
			CreatedByType:  "agent",
			CreatedByID:    &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after invalid-flow validation failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureFirstWaveFlow {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureFirstWaveFlow)
	}
	if !strings.Contains(bootstrapState.ValidationFailureReason, "work -> review -> completion path") {
		t.Fatalf("bootstrap validation_failure_reason = %q, want executable-flow detail", bootstrapState.ValidationFailureReason)
	}
}

func TestTurnEngineIntegrationProjectBootstrapFailsWhenSetupPersistsWithoutExecutableFirstWaveTasks(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, persist setup, and open the first execution wave.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)
	workerAgent := mustCreateAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-v5-shape",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is persisted."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		assignments := repo.NewAgentProjectAssignmentRepo(fixture.pool)
		for _, item := range []repo.AgentProjectAssignment{
			{
				AgentID:        pmAgent.ID,
				ProjectID:      project.ID,
				Role:           "pm",
				AssignedByType: "agent",
				AssignedByID:   &lori.ID,
			},
			{
				AgentID:        workerAgent.ID,
				ProjectID:      project.ID,
				Role:           "worker",
				AssignedByType: "agent",
				AssignedByID:   &lori.ID,
			},
		} {
			if _, err := assignments.Assign(ctx, item); err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
		}

		gateTask, err := repo.NewProjectTaskRepo(fixture.pool).GetByProjectAndNumber(ctx, project.ID, 1)
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		if gateTask.FlowTemplateID == nil || *gateTask.FlowTemplateID == uuid.Nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "bootstrap gate missing flow template"}, nil
		}
		taskRepo := repo.NewProjectTaskRepo(fixture.pool)
		for i := 0; i < 8; i++ {
			description := fmt.Sprintf("Extra bounded bootstrap setup checkpoint %d.", i+1)
			metadata := mustJSON(t, map[string]any{
				"bootstrap_setup_task":         true,
				"bootstrap_step_slug":          fmt.Sprintf("extra-v5-setup-%d", i+1),
				"bootstrap_step_order":         100 + i + 1,
				"decomposition_parent_task_id": gateTask.ID.String(),
				"workstream_index":             100 + i + 1,
			})
			taskRecord, err := taskRepo.Create(ctx, repo.ProjectTask{
				OrganizationID:  fixture.org.ID,
				ProjectID:       project.ID,
				Title:           fmt.Sprintf("Extra setup checkpoint %d", i+1),
				Description:     &description,
				WorkStatus:      "draft",
				FlowTemplateID:  gateTask.FlowTemplateID,
				AssignedAgentID: &pmAgent.ID,
				Metadata:        metadata,
				CreatedByType:   "agent",
				CreatedByID:     &lori.ID,
			})
			if err != nil {
				return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
			}
			gateTask.Metadata = taskdecomp.AppendChildTaskID(gateTask.Metadata, taskRecord.ID)
		}
		if _, err := taskRepo.Update(ctx, gateTask); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}

		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":     pmAgent.ID.String(),
				"worker_agent_id": workerAgent.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	var assignmentCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_project_assignment
		WHERE project_id = $1
		  AND is_active = true
	`, project.ID).Scan(&assignmentCount); err != nil {
		t.Fatalf("count project assignments: %v", err)
	}
	if assignmentCount != 2 {
		t.Fatalf("project assignments = %d, want 2", assignmentCount)
	}

	var taskCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM project_task
		WHERE project_id = $1
	`, project.ID).Scan(&taskCount); err != nil {
		t.Fatalf("count project tasks: %v", err)
	}
	if taskCount != 16 {
		t.Fatalf("project tasks = %d, want 16", taskCount)
	}

	var flowTemplateCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_template
		WHERE project_id = $1
		  AND is_current = true
	`, project.ID).Scan(&flowTemplateCount); err != nil {
		t.Fatalf("count project flow templates: %v", err)
	}
	if flowTemplateCount != 1 {
		t.Fatalf("project flow templates = %d, want 1", flowTemplateCount)
	}

	var flowExecutionCount int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_node_execution fne
		JOIN project_task pt ON pt.id = fne.task_id
		WHERE pt.project_id = $1
		  AND fne.status = 'active'
	`, project.ID).Scan(&flowExecutionCount); err != nil {
		t.Fatalf("count active flow executions: %v", err)
	}
	if flowExecutionCount != 0 {
		t.Fatalf("active flow executions = %d, want 0", flowExecutionCount)
	}

	if jobs := countRunnableAgentTurnJobsForProject(t, ctx, fixture.pool, project.ID); jobs != 0 {
		t.Fatalf("runnable project bootstrap jobs = %d, want 0 after explicit bootstrap failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.ValidationStatus != projectBootstrapValidationFailed {
		t.Fatalf("bootstrap validation_status = %q, want %q", bootstrapState.ValidationStatus, projectBootstrapValidationFailed)
	}
	if bootstrapState.AssignmentCount != 2 {
		t.Fatalf("bootstrap assignment_count = %d, want 2", bootstrapState.AssignmentCount)
	}
	if bootstrapState.PlannedTaskCount != 0 {
		t.Fatalf("bootstrap planned_task_count = %d, want 0 because only bootstrap setup tasks exist", bootstrapState.PlannedTaskCount)
	}
	if bootstrapState.PlannedFlowTemplateCount != 1 {
		t.Fatalf("bootstrap planned_flow_template_count = %d, want 1", bootstrapState.PlannedFlowTemplateCount)
	}
	if bootstrapState.FirstWaveTaskCount != 0 || bootstrapState.FirstWaveExecutionCount != 0 || bootstrapState.FirstWaveJobCount != 0 {
		t.Fatalf("bootstrap first-wave counts = %+v, want zero", bootstrapState)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureCompoundParent)
	}
	if bootstrapState.CurrentPhase != projectBootstrapCheckpointFirstWaveSelected {
		t.Fatalf("bootstrap current_phase = %q, want %q", bootstrapState.CurrentPhase, projectBootstrapCheckpointFirstWaveSelected)
	}
	if checkpoint := mustProjectBootstrapCheckpoint(t, bootstrapState, projectBootstrapCheckpointFirstWaveSelected); checkpoint.Status != projectBootstrapCheckpointStatusFailed {
		t.Fatalf("first_wave_selected checkpoint status = %q, want %q", checkpoint.Status, projectBootstrapCheckpointStatusFailed)
	}
	if len(bootstrapState.ValidationFindings) != 1 {
		t.Fatalf("bootstrap validation_findings = %d, want 1", len(bootstrapState.ValidationFindings))
	}
	if bootstrapState.ValidationFindings[0].Category != projectBootstrapFindingCategoryExecutionShape {
		t.Fatalf("bootstrap validation finding category = %q, want %q", bootstrapState.ValidationFindings[0].Category, projectBootstrapFindingCategoryExecutionShape)
	}
	if bootstrapState.ValidationFindings[0].Code != "first_wave_tasks_missing" {
		t.Fatalf("bootstrap validation finding code = %q, want %q", bootstrapState.ValidationFindings[0].Code, "first_wave_tasks_missing")
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureCompoundParent,
		projectBootstrapCheckpointFirstWave,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsExplicitlyAfterRepeatedNoProgressTurns(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start staffing and setup for this new project.")

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "Acknowledged. I am still working through the bootstrap setup."}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial stalled bootstrap turn: %v", err)
	}

	latestTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": latestTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial stalled bootstrap turn: %v", err)
	}

	for attempt := 1; attempt < maxProjectBootstrapAutoTurns; attempt++ {
		jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
		if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
			t.Fatalf("handleUserMessage stalled bootstrap follow-on %d: %v", attempt, err)
		}
		latestTurn = latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
		if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
			OrganizationID: fixture.org.ID,
			EventType:      "chat.turn.completed",
			Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": latestTurn.ID.String()}),
		}); err != nil {
			t.Fatalf("HandleTurnCompletedEvent stalled bootstrap follow-on %d: %v", attempt, err)
		}
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after explicit bootstrap failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureStalled {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureStalled)
	}
	if bootstrapState.FailureReason == "" {
		t.Fatal("expected bootstrap failure_reason after repeated no-progress turns")
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	var failureMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "Project bootstrap failed:") {
			failureMessages++
		}
	}
	if failureMessages != 1 {
		t.Fatalf("bootstrap failure system messages = %d, want 1", failureMessages)
	}
}

func TestTurnEngineIntegrationProjectBootstrapWatchdogFailsHungInFlightTurnAndReleasesClaim(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start staffing and setup for this new project.")

	fixture.engine.projectBootstrapTurnTimeout = 40 * time.Millisecond
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	followOnStarted := make(chan struct{}, 1)
	fixture.model.streamFn = func(ctx context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			select {
			case followOnStarted <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ModelResponse{}, ctx.Err()
		default:
			return ModelResponse{Content: "unexpected bootstrap watchdog call"}, nil
		}
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	var jobID uuid.UUID
	if err := fixture.pool.QueryRow(ctx, `
		SELECT id
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status = 'pending'
		  AND payload->>'session_id' = $1
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, projectSession.ID.String()).Scan(&jobID); err != nil {
		t.Fatalf("load pending bootstrap job: %v", err)
	}

	worker := jobqueue.New(fixture.pool, slog.New(slog.NewTextHandler(io.Discard, nil)), jobqueue.Config{
		WorkerID:             "bootstrap-watchdog-worker",
		PollInterval:         5 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})
	worker.Register(AgentTurnJobType, fixture.engine.HandleTurnJob)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	defer cancelWorker()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = worker.Start(workerCtx)
	}()
	defer func() {
		cancelWorker()
		_ = worker.Stop()
		<-workerDone
	}()

	select {
	case <-followOnStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for watchdog bootstrap turn to start")
	}

	waitForJobStatus(t, fixture.pool, jobID, "done", 3*time.Second)

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after watchdog timeout failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	if storedSession.CurrentTurnID != nil {
		t.Fatalf("project session current_turn_id = %v, want nil after watchdog timeout failure", storedSession.CurrentTurnID)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureStalled {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureStalled)
	}
	if !strings.Contains(bootstrapState.FailureReason, "watchdog timed out") {
		t.Fatalf("bootstrap failure_reason = %q, want watchdog timeout detail", bootstrapState.FailureReason)
	}
	if bootstrapState.AssignmentCount != 0 || bootstrapState.PlannedTaskCount != 0 || bootstrapState.PlannedFlowTemplateCount != 0 {
		t.Fatalf("bootstrap progress after watchdog timeout = %+v, want zero persisted setup counts", bootstrapState)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	failedTurn := turns[len(turns)-1]
	if failedTurn.Status != "failed" {
		t.Fatalf("watchdog bootstrap turn status = %q, want failed", failedTurn.Status)
	}
	if failedTurn.StopReason == nil || *failedTurn.StopReason != stopReasonMaxDuration {
		t.Fatalf("watchdog bootstrap turn stop_reason = %v, want %q", failedTurn.StopReason, stopReasonMaxDuration)
	}

	invocations, err := repo.NewModelInvocationRepo(fixture.pool).ListBySession(ctx, fixture.org.ID, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession invocations: %v", err)
	}
	var failedInvocation *repo.ModelInvocation
	for i := range invocations {
		item := invocations[i]
		if item.TurnID != nil && *item.TurnID == failedTurn.ID {
			failedInvocation = &item
			break
		}
	}
	if failedInvocation == nil {
		t.Fatal("expected model invocation row for watchdog-timed bootstrap turn")
	}
	if failedInvocation.Status != "failed" {
		t.Fatalf("watchdog invocation status = %q, want failed", failedInvocation.Status)
	}
	if failedInvocation.ErrorCode == nil || *failedInvocation.ErrorCode != "bootstrap_watchdog_timeout" {
		t.Fatalf("watchdog invocation error_code = %v, want bootstrap_watchdog_timeout", failedInvocation.ErrorCode)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	var failureMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "watchdog timed out") {
			failureMessages++
		}
	}
	if failureMessages != 1 {
		t.Fatalf("watchdog bootstrap failure system messages = %d, want 1", failureMessages)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureStalled,
		projectBootstrapCheckpointProjectCreated,
	)
}

func TestTurnEngineIntegrationProjectExecutionFailureAfterFirstWaveClaimPausesProject(t *testing.T) {
	fixture := newIntegrationFixture(t)
	enableTaskQueueProcessor(t, fixture)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: staff the project, create initial tasks, and attach flow templates.")
	pmAgent := mustCreateBootstrapPMAgent(t, ctx, fixture.pool, fixture.org.ID)

	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "bootstrap.setup.persist", Tier: "tier1"}}}

	modelCalls := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		modelCalls++
		switch modelCalls {
		case 1:
			return ModelResponse{Content: "I have the handoff and will start the bootstrap setup now."}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "bootstrap-setup-first-wave-claim",
				Name: "bootstrap.setup.persist",
				Tier: "tier1",
			}}}, nil
		default:
			return ModelResponse{Content: "Bootstrap setup is now persisted in project records."}, nil
		}
	}

	fixture.dispatcher.tier1Fn = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		if call.Name != "bootstrap.setup.persist" {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: "unexpected_tool"}, nil
		}
		if _, err := repo.NewAgentProjectAssignmentRepo(fixture.pool).Assign(ctx, repo.AgentProjectAssignment{
			AgentID:        pmAgent.ID,
			ProjectID:      project.ID,
			Role:           "pm",
			AssignedByType: "agent",
			AssignedByID:   &lori.ID,
		}); err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		template := mustCreateExecutionFlowTemplate(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID)
		description := "Initial scoped bootstrap workstream"
		taskRecord, err := repo.NewProjectTaskRepo(fixture.pool).Create(ctx, repo.ProjectTask{
			OrganizationID:  fixture.org.ID,
			ProjectID:       project.ID,
			Title:           "Define the first execution slice",
			Description:     &description,
			WorkStatus:      "draft",
			FlowTemplateID:  &template.ID,
			AssignedAgentID: &pmAgent.ID,
			CreatedByType:   "agent",
			CreatedByID:     &lori.ID,
		})
		if err != nil {
			return ToolResult{ToolCallID: call.ID, Name: call.Name, Error: err.Error()}, nil
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"pm_agent_id":      pmAgent.ID.String(),
				"task_id":          taskRecord.ID.String(),
				"flow_template_id": template.ID.String(),
			},
		}, nil
	}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage initial bootstrap acknowledgement: %v", err)
	}

	firstTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": firstTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent initial bootstrap acknowledgement: %v", err)
	}

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage follow-on bootstrap turn: %v", err)
	}

	secondTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, projectSession.ID)
	enableTurnEngineUserMessageEnqueue(t, fixture)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": projectSession.ID.String(), "turn_id": secondTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent follow-on bootstrap turn: %v", err)
	}

	signoffTask := completeBootstrapSetupTasks(t, ctx, fixture.pool, project.ID, "")
	if err := fixture.engine.HandleTaskStatusChangedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "task.status_changed",
		Payload: mustJSON(t, map[string]any{
			"task_id":    signoffTask.ID.String(),
			"project_id": project.ID.String(),
			"to_status":  "done",
		}),
	}); err != nil {
		t.Fatalf("HandleTaskStatusChangedEvent bootstrap sign-off: %v", err)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session after bootstrap completion: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusCompleted {
		t.Fatalf("bootstrap status = %q, want %q before runtime failure", bootstrapState.Status, projectBootstrapStatusCompleted)
	}
	if bootstrapState.LastCheckpoint != projectBootstrapCheckpointJobsClaimed {
		t.Fatalf("bootstrap last_checkpoint = %q, want %q", bootstrapState.LastCheckpoint, projectBootstrapCheckpointJobsClaimed)
	}

	taskRecord := mustFindFirstNonBootstrapTask(t, ctx, fixture.pool, project.ID)
	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "continue execution")

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{}, errors.New("simulated task runtime failure")
	}

	if err := fixture.engine.handleUserMessage(ctx, taskSession.ID, userMessage.ID, taskRecord.AssignedAgentID, 0, nil); err == nil || !strings.Contains(err.Error(), "simulated task runtime failure") {
		t.Fatalf("handleUserMessage task runtime failure err = %v, want simulated task runtime failure", err)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "active" {
		t.Fatalf("project status = %q, want active", storedProject.Status)
	}
	pauseState := projectpause.Parse(storedProject.Settings)
	if !pauseState.IsPaused {
		t.Fatalf("project pause state = %+v, want paused", pauseState)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionPause,
		projectFailureCategoryExecution,
		projectFailureClassExecutionRuntime,
		projectBootstrapCheckpointJobsClaimed,
	)
}

func TestTurnEngineIntegrationProjectBootstrapFailsExplicitlyOnGuardrailLoop(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	lori := mustCreateStarterLori(t, ctx, fixture.pool, fixture.org.ID)
	project := mustCreateBootstrapProject(t, ctx, fixture)
	projectSession := mustCreateProjectSession(t, ctx, fixture, project.ID, fixture.agent.ID, lori.ID)
	handoff := mustAppendProjectBootstrapHandoff(t, ctx, fixture, projectSession.ID, fixture.agent.ID, "Frank handoff: start staffing and setup for this new project.")

	fixture.engine.assembler = &fakeAssembler{results: []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "system"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}}

	if err := fixture.engine.handleUserMessage(ctx, projectSession.ID, handoff.ID, &lori.ID, 0, nil); err != nil {
		t.Fatalf("handleUserMessage project bootstrap guardrail loop: %v", err)
	}

	if jobs := countRunnableAgentTurnJobsForSession(t, ctx, fixture.pool, projectSession.ID); jobs != 0 {
		t.Fatalf("runnable bootstrap agent_turn jobs = %d, want 0 after guardrail bootstrap failure", jobs)
	}

	storedSession, err := repo.NewChatSessionRepo(fixture.pool).GetByID(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("GetByID project session: %v", err)
	}
	bootstrapState := projectBootstrapStateFromMetadata(storedSession.Metadata)
	if bootstrapState.Status != projectBootstrapStatusFailed {
		t.Fatalf("bootstrap status = %q, want %q", bootstrapState.Status, projectBootstrapStatusFailed)
	}
	if bootstrapState.FailureClass != projectBootstrapFailureGuardrail {
		t.Fatalf("bootstrap failure_class = %q, want %q", bootstrapState.FailureClass, projectBootstrapFailureGuardrail)
	}
	if !strings.Contains(bootstrapState.FailureReason, "prompt input kept exceeding the 64000-token guardrail across 3 continuation turns") {
		t.Fatalf("bootstrap failure_reason = %q, want continuation-depth guardrail guidance", bootstrapState.FailureReason)
	}
	if bootstrapState.AutoTurnCount != 0 {
		t.Fatalf("bootstrap auto_turn_count = %d, want 0 for same-turn guardrail failure", bootstrapState.AutoTurnCount)
	}

	turns, err := repo.NewChatTurnRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 4 {
		t.Fatalf("turn count = %d, want 4 bounded continuation turns", len(turns))
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("final turn status = %q, want completed", turns[len(turns)-1].Status)
	}

	messages, err := repo.NewChatMessageRepo(fixture.pool).ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project messages: %v", err)
	}
	var continuationMessages int
	var failureMessages int
	var genericFailureMessages int
	for _, message := range messages {
		if !strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		if strings.Contains(message.Content, "Prompt input exceeded 64000-token guardrail - continuing in a new turn.") {
			continuationMessages++
		}
		if strings.Contains(message.Content, "Project bootstrap failed:") {
			failureMessages++
		}
		if strings.Contains(message.Content, "[Turn failed:") {
			genericFailureMessages++
		}
	}
	if continuationMessages != 3 {
		t.Fatalf("continuation notice count = %d, want 3", continuationMessages)
	}
	if failureMessages != 1 {
		t.Fatalf("bootstrap failure system messages = %d, want 1", failureMessages)
	}
	if genericFailureMessages != 0 {
		t.Fatalf("generic turn failure system messages = %d, want 0", genericFailureMessages)
	}

	storedProject := mustGetProjectByID(t, ctx, fixture.pool, project.ID)
	if storedProject.Status != "archived" {
		t.Fatalf("project status = %q, want archived", storedProject.Status)
	}
	assertAutomaticFailureState(
		t,
		storedProject,
		projectFailureActionArchive,
		projectFailureCategoryBootstrap,
		projectBootstrapFailureGuardrail,
		projectBootstrapCheckpointProjectCreated,
	)
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

func TestTurnEngineIntegrationContentMigrationContinuationUsesWorkspaceCheckpoint(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	description := "Migrate Sam.blog blog content by scraping source pages, checkpointing manifests, and writing migrated posts incrementally."
	taskRecord.Title = "Migrate Sam.blog content"
	taskRecord.Description = &description
	var err error
	taskRecord, err = repo.NewProjectTaskRepo(fixture.pool).Update(ctx, taskRecord)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "continue the content migration")
	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.maxToolCalls = 1
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "network.fetch", Tier: "tier1"},
		{Name: "file.write", Tier: "tier2"},
	}}

	rawMarker := strings.Repeat("RAW_PAGE_SENTINEL ", 180)
	prompts := make([]string, 0, 2)
	round := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		prompts = append(prompts, flattenPrompt(req.Prompt))
		round++
		if round == 1 {
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "fetch-1", Name: "network.fetch", Tier: "tier1"}}}, nil
		}
		return ModelResponse{Content: "resume from the checkpoint"}, nil
	}
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		rawRel := "artifacts/raw/post-1.html"
		rawAbs := filepath.Join(workspaceRoot, filepath.FromSlash(rawRel))
		if err := os.MkdirAll(filepath.Dir(rawAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(rawAbs, []byte(rawMarker), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path": rawRel,
				"body": rawMarker,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(prompts) < 2 {
		t.Fatalf("prompt count = %d, want at least 2 rounds", len(prompts))
	}

	checkpointRel := taskcheckpoint.CheckpointRelativePath(taskRecord.TaskNumber, taskRecord.ID)
	secondPrompt := prompts[1]
	if strings.Contains(secondPrompt, rawMarker) {
		t.Fatal("continuation prompt replayed raw fetch body instead of checkpointed artifact state")
	}
	if !strings.Contains(secondPrompt, checkpointRel) {
		t.Fatalf("continuation prompt missing checkpoint path %q:\n%s", checkpointRel, secondPrompt)
	}
	if !strings.Contains(secondPrompt, "artifacts/raw/post-1.html") {
		t.Fatalf("continuation prompt missing persisted raw artifact path:\n%s", secondPrompt)
	}

	checkpointBody, err := os.ReadFile(filepath.Join(workspaceRoot, filepath.FromSlash(checkpointRel)))
	if err != nil {
		t.Fatalf("read checkpoint file: %v", err)
	}
	if !strings.Contains(string(checkpointBody), "artifacts/raw/post-1.html") {
		t.Fatalf("checkpoint file missing raw artifact path:\n%s", string(checkpointBody))
	}
}

func TestTurnEngineIntegrationContentMigrationResumeUsesPersistedCheckpointState(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	description := "Import Sam.blog posts from the legacy site, persist manifests, and write migrated markdown files incrementally."
	taskRecord.Title = "Import Sam.blog posts"
	taskRecord.Description = &description
	var err error
	taskRecord, err = repo.NewProjectTaskRepo(fixture.pool).Update(ctx, taskRecord)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "start the migration")
	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.maxToolCalls = 1
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "network.fetch", Tier: "tier1"},
		{Name: "file.write", Tier: "tier2"},
	}}

	rawMarker := strings.Repeat("RAW_PAGE_RESUME_SENTINEL ", 180)
	outputRel := "content/posts/hello-world.md"
	prompts := make([]string, 0, 4)
	round := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		prompts = append(prompts, flattenPrompt(req.Prompt))
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{ID: "fetch-1", Name: "network.fetch", Tier: "tier1"}}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "write-1",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path":    outputRel,
					"content": "# Hello World\n",
				},
			}}}, nil
		case 3:
			return ModelResponse{Content: "first run complete"}, nil
		default:
			return ModelResponse{Content: "resumed from persisted checkpoint"}, nil
		}
	}
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		rawRel := "artifacts/raw/post-1.html"
		rawAbs := filepath.Join(workspaceRoot, filepath.FromSlash(rawRel))
		if err := os.MkdirAll(filepath.Dir(rawAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(rawAbs, []byte(rawMarker), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path": rawRel,
				"body": rawMarker,
			},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		target, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(targetAbs, []byte(content), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			RunID:      &runID,
			Output: map[string]any{
				"path":      target,
				"byte_size": len(content),
				"created":   true,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage first run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(outputRel))); err != nil {
		t.Fatalf("expected migrated output file on disk: %v", err)
	}

	authorType := "human_user"
	resumeMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "resume from the latest checkpoint",
	})
	if err != nil {
		t.Fatalf("AppendMessage resume: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, resumeMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage resume: %v", err)
	}

	if len(prompts) < 4 {
		t.Fatalf("prompt count = %d, want at least 4 across both runs", len(prompts))
	}
	resumePrompt := prompts[3]
	checkpointRel := taskcheckpoint.CheckpointRelativePath(taskRecord.TaskNumber, taskRecord.ID)
	if strings.Contains(resumePrompt, rawMarker) {
		t.Fatal("resume prompt replayed raw fetch body instead of persisted checkpoint state")
	}
	if !strings.Contains(resumePrompt, checkpointRel) {
		t.Fatalf("resume prompt missing checkpoint path %q:\n%s", checkpointRel, resumePrompt)
	}
	if !strings.Contains(resumePrompt, outputRel) {
		t.Fatalf("resume prompt missing migrated output path %q:\n%s", outputRel, resumePrompt)
	}
}

func TestTurnEngineIntegrationContentMigrationCheckpointPushesFirstOutputBeforeMoreScaffolding(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	description := "Migrate Sam.blog posts from legacy HTML into markdown, persist helper scripts/manifests, and emit output files incrementally."
	taskRecord.Title = "Migrate Sam.blog posts"
	taskRecord.Description = &description
	var err error
	taskRecord, err = repo.NewProjectTaskRepo(fixture.pool).Update(ctx, taskRecord)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}

	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "continue the Sam.blog migration")
	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	assembler, err := prompt.NewPromptAssembler(prompt.AssemblerOptions{Pool: fixture.pool})
	if err != nil {
		t.Fatalf("NewPromptAssembler: %v", err)
	}
	fixture.engine.assembler = assembler
	fixture.engine.maxToolCalls = 1
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{
		{Name: "network.fetch", Tier: "tier1"},
		{Name: "file.write", Tier: "tier2"},
	}}

	rawMarker := strings.Repeat("SAM_BLOG_RAW_SENTINEL ", 2200)
	outputRel := "content/posts/stop-preparing-your-kids-for-jobs.md"
	prompts := make([]string, 0, 3)
	round := 0
	fixture.model.streamFn = func(_ context.Context, req ModelRequest, _ func(token string) error) (ModelResponse, error) {
		flattened := flattenPrompt(req.Prompt)
		prompts = append(prompts, flattened)
		round++
		switch round {
		case 1:
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "fetch-post",
				Name: "network.fetch",
				Tier: "tier1",
			}}}, nil
		case 2:
			if strings.Contains(flattened, rawMarker) {
				t.Fatal("continuation prompt replayed raw fetch body instead of persisted checkpoint state")
			}
			checkpointRel := taskcheckpoint.CheckpointRelativePath(taskRecord.TaskNumber, taskRecord.ID)
			if !strings.Contains(flattened, checkpointRel) {
				t.Fatalf("continuation prompt missing checkpoint path %q:\n%s", checkpointRel, flattened)
			}
			if !strings.Contains(flattened, "no migrated output files are on disk yet") {
				t.Fatalf("continuation prompt missing first-output directive:\n%s", flattened)
			}
			if !strings.Contains(flattened, "do not spend the next turn re-listing workspace state or creating replacement helper scripts") {
				t.Fatalf("continuation prompt missing anti-loop directive:\n%s", flattened)
			}
			if !strings.Contains(flattened, "artifacts/raw/post-1.html") {
				t.Fatalf("continuation prompt missing raw artifact path:\n%s", flattened)
			}
			if !strings.Contains(flattened, "scripts/migrate.py") {
				t.Fatalf("continuation prompt missing persisted script path:\n%s", flattened)
			}
			return ModelResponse{ToolCalls: []ModelToolCall{{
				ID:   "write-output",
				Name: "file.write",
				Tier: "tier2",
				Arguments: map[string]any{
					"path":    outputRel,
					"content": "# Stop Preparing Your Kids for Jobs",
				},
			}}}, nil
		default:
			return ModelResponse{Content: "migration resumed from the checkpoint and emitted the first output"}, nil
		}
	}
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		scriptFixtures := map[string]string{
			"scrape_posts.py":    "print('scrape existing archive')",
			"scripts/migrate.py": "print('convert persisted posts')",
		}
		for rel, body := range scriptFixtures {
			scriptAbs := filepath.Join(workspaceRoot, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(scriptAbs), 0o755); err != nil {
				return ToolResult{}, err
			}
			if err := os.WriteFile(scriptAbs, []byte(body), 0o644); err != nil {
				return ToolResult{}, err
			}
		}
		rawRel := "artifacts/raw/post-1.html"
		rawAbs := filepath.Join(workspaceRoot, filepath.FromSlash(rawRel))
		if err := os.MkdirAll(filepath.Dir(rawAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(rawAbs, []byte(rawMarker), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Output: map[string]any{
				"path": rawRel,
				"body": rawMarker,
			},
		}, nil
	}
	fixture.dispatcher.tier2Fn = func(_ context.Context, call ToolCall, onRunStarted func(runID uuid.UUID)) (ToolResult, error) {
		runID := uuid.New()
		onRunStarted(runID)
		target, _ := call.Arguments["path"].(string)
		content, _ := call.Arguments["content"].(string)
		targetAbs := filepath.Join(workspaceRoot, filepath.FromSlash(target))
		if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
			return ToolResult{}, err
		}
		if err := os.WriteFile(targetAbs, []byte(content), 0o644); err != nil {
			return ToolResult{}, err
		}
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			RunID:      &runID,
			Output: map[string]any{
				"path":      target,
				"byte_size": len(content),
				"created":   true,
			},
		}, nil
	}

	if err := fixture.engine.HandleUserMessage(ctx, taskSession.ID, userMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if len(prompts) < 2 {
		t.Fatalf("prompt count = %d, want at least 2 rounds", len(prompts))
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, filepath.FromSlash(outputRel))); err != nil {
		t.Fatalf("expected migrated output file on disk: %v", err)
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

func TestTurnEngineIntegrationCompletedWorkTurnAdvancesTaskToReview(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:   fixture.pool,
		Events: fixture.bus,
	})
	if err != nil {
		t.Fatalf("flow.NewService: %v", err)
	}
	if _, err := flowService.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}

	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant agent: %v", err)
	}

	_, turnID := mustCreateCompletedWorkTurn(t, ctx, fixture, taskSession.ID, fixture.agent.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "turn_id": turnID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}

	nodeRepo := repo.NewFlowNodeRepo(fixture.pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, *taskRecord.FlowTemplateID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("flow node count = %d, want >= 2", len(nodes))
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("current_flow_node_id = %v, want review node %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}

	var flowAdvancedEvents int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'flow.advanced'
		  AND payload->>'task_id' = $2
	`, fixture.org.ID, taskRecord.ID.String()).Scan(&flowAdvancedEvents); err != nil {
		t.Fatalf("count flow.advanced events: %v", err)
	}
	if flowAdvancedEvents != 1 {
		t.Fatalf("flow.advanced events = %d, want 1", flowAdvancedEvents)
	}
}

func TestTurnEngineIntegrationSmokeTaskWritesOutputAndAdvancesToReview(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	mustAssignProjectPM(t, ctx, fixture.pool, project.ID, fixture.agent.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:   fixture.pool,
		Events: fixture.bus,
	})
	if err != nil {
		t.Fatalf("flow.NewService: %v", err)
	}
	if _, err := flowService.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, project.ID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot("", projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}
	outputRel := "docs/content-strategy/pillar-taxonomy.md"
	outputAbs := filepath.Join(workspaceRoot, filepath.FromSlash(outputRel))
	if err := os.MkdirAll(filepath.Dir(outputAbs), 0o755); err != nil {
		t.Fatalf("mkdir output dir: %v", err)
	}
	if err := os.WriteFile(outputAbs, []byte("# Pillar taxonomy\n"), 0o644); err != nil {
		t.Fatalf("write output file: %v", err)
	}

	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant agent: %v", err)
	}

	_, turnID := mustCreateCompletedWorkTurn(t, ctx, fixture, taskSession.ID, fixture.agent.ID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "turn_id": turnID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	outputBody, err := os.ReadFile(outputAbs)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(outputBody), "Pillar taxonomy") {
		t.Fatalf("output file body = %q, want persisted content marker", string(outputBody))
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	updatedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}

	nodeRepo := repo.NewFlowNodeRepo(fixture.pool)
	nodes, err := nodeRepo.GetByTemplateOrdered(ctx, *taskRecord.FlowTemplateID)
	if err != nil {
		t.Fatalf("GetByTemplateOrdered: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("flow node count = %d, want >= 2", len(nodes))
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("current_flow_node_id = %v, want review node %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}
}

func TestTurnEngineIntegrationDuplicateCompletedWorkSignalDoesNotDuplicateFlowAdvancedEvents(t *testing.T) {
	fixture := newIntegrationFixture(t)
	ctx := context.Background()

	project := mustCreateProject(t, ctx, fixture.pool, fixture.org.ID, fixture.user.ID)
	taskRecord := mustCreateTask(t, ctx, fixture.pool, fixture.org.ID, project.ID, fixture.user.ID, fixture.agent.ID)
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:   fixture.pool,
		Events: fixture.bus,
	})
	if err != nil {
		t.Fatalf("flow.NewService: %v", err)
	}
	if _, err := flowService.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}

	taskSession, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, taskSession.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant agent: %v", err)
	}

	_, turnID := mustCreateCompletedWorkTurn(t, ctx, fixture, taskSession.ID, fixture.agent.ID)
	event := eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": taskSession.ID.String(), "turn_id": turnID.String()}),
	}
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, event); err != nil {
		t.Fatalf("first HandleTurnCompletedEvent: %v", err)
	}
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, event); err != nil {
		t.Fatalf("second HandleTurnCompletedEvent: %v", err)
	}

	var flowAdvancedEvents int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM domain_event
		WHERE organization_id = $1
		  AND event_type = 'flow.advanced'
		  AND payload->>'task_id' = $2
	`, fixture.org.ID, taskRecord.ID.String()).Scan(&flowAdvancedEvents); err != nil {
		t.Fatalf("count flow.advanced events: %v", err)
	}
	if flowAdvancedEvents != 1 {
		t.Fatalf("flow.advanced events = %d, want 1", flowAdvancedEvents)
	}
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

func enableTaskQueueProcessor(t *testing.T, fixture *integrationFixture) {
	t.Helper()

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("New task service: %v", err)
	}
	flowSessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  fixture.pool,
		Chats: fixture.chatService,
	})
	if err != nil {
		t.Fatalf("New flow session bridge: %v", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          fixture.pool,
		Events:        fixture.bus,
		TasksService:  taskService,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("New flow service: %v", err)
	}
	runService, err := controlplane.NewRunService(controlplane.RunServiceOptions{
		Pool:          fixture.pool,
		EventBus:      fixture.bus,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("New run service: %v", err)
	}
	queueRuns, ok := runService.(interface {
		CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error)
		CreateExecutionWakeup(ctx context.Context, input controlplane.ExecutionWakeupInput) (controlplane.ExecutionWakeupResult, error)
		StartRun(ctx context.Context, runID uuid.UUID) error
		CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
		FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
		ConfirmCancelled(ctx context.Context, runID uuid.UUID) error
		GetRun(ctx context.Context, runID uuid.UUID) (controlplane.Run, error)
		ListRunsByTask(ctx context.Context, organizationID, taskID uuid.UUID, status, triggerType string) ([]controlplane.Run, error)
		ReleaseExecutionOwner(ctx context.Context, taskID, sessionID uuid.UUID, reason string) (controlplane.ExecutionWakeupResult, error)
		RetireRuntimeStateForTask(ctx context.Context, taskID uuid.UUID, reason string) error
		RetireRuntimeStateForProject(ctx context.Context, projectID uuid.UUID, reason string) error
	})
	if !ok {
		t.Fatal("run service does not implement task queue wakeup contract")
	}

	processor, err := controlplane.NewTaskQueueProcessor(controlplane.TaskQueueProcessorOptions{
		Events:         fixture.bus,
		Tasks:          repo.NewProjectTaskRepo(fixture.pool),
		Projects:       repo.NewProjectRepo(fixture.pool),
		TaskService:    taskService,
		Flow:           flowService,
		FlowExecutions: repo.NewFlowNodeExecutionRepo(fixture.pool),
		FlowNodes:      repo.NewFlowNodeRepo(fixture.pool),
		Assignments:    repo.NewAgentProjectAssignmentRepo(fixture.pool),
		Runs:           queueRuns,
		Chats:          fixture.chatService,
		Sessions:       repo.NewChatSessionRepo(fixture.pool),
	})
	if err != nil {
		t.Fatalf("NewTaskQueueProcessor: %v", err)
	}

	subscription := processor.SubscribeTaskQueued(&fixture.org.ID)
	t.Cleanup(func() {
		fixture.bus.Unsubscribe(subscription)
	})

	// Give the subscription a moment to initialize its cursor before the test
	// publishes task.status_changed events for bootstrap promotion.
	time.Sleep(50 * time.Millisecond)
}

func enableTurnEngineUserMessageEnqueue(t *testing.T, fixture *integrationFixture) {
	t.Helper()

	subscription := fixture.bus.Subscribe("turn-user-enqueue-"+uuid.NewString(), &fixture.org.ID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "chat.message.user_sent" {
			return nil
		}
		payload, err := parseAgentTurnPayload(event.Payload)
		if err != nil {
			return nil
		}
		session, err := fixture.chatService.GetSession(ctx, payload.SessionID)
		if err != nil || session == nil {
			return nil
		}
		if !strings.EqualFold(strings.TrimSpace(session.ScopeType), "project_task") {
			return nil
		}
		return fixture.engine.HandleUserMessageEvent(ctx, event)
	})
	t.Cleanup(func() {
		fixture.bus.Unsubscribe(subscription)
	})

	time.Sleep(50 * time.Millisecond)
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

type persistedRecoveryResumeFixture struct {
	workspaceRoot string
	targetPath    string
	targetAbs     string
	targetDraft   string
	artifactRel   string
	artifactAbs   string
	artifactDraft string
	failureReason string
}

func mustPersistRecoveryResumeFixture(
	t *testing.T,
	ctx context.Context,
	fixture *integrationFixture,
	taskRecord repo.ProjectTask,
	historyStartMessageID uuid.UUID,
) persistedRecoveryResumeFixture {
	t.Helper()

	projectRecord, err := repo.NewProjectRepo(fixture.pool).GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	workspaceRoot, err := workspace.ProjectRoot(fixture.engine.dataDir, projectRecord.Slug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}

	seed := persistedRecoveryResumeFixture{
		workspaceRoot: workspaceRoot,
		targetPath:    "docs/content-strategy.md",
		targetDraft:   "Excellent. I now have a thorough understanding of the strategic direction for Sam.blog. Let me write the full document now.",
		artifactDraft: strings.TrimSpace(`# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.

## Editorial Pillars
- Family systems that reduce chaos and increase agency.
- Honest stories about work, stewardship, and craft.
- Experiments and operating notes that turn reflection into repeatable practice.
`),
		failureReason: "assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body",
	}
	seed.targetAbs = filepath.Join(workspaceRoot, filepath.FromSlash(seed.targetPath))
	seed.artifactRel = filepath.ToSlash(filepath.Join(recoveryArtifactDir, filepath.FromSlash(seed.targetPath)))
	seed.artifactAbs = filepath.Join(workspaceRoot, filepath.FromSlash(seed.artifactRel))

	if err := os.MkdirAll(filepath.Dir(seed.targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(seed.targetAbs, []byte(seed.targetDraft), 0o644); err != nil {
		t.Fatalf("write target draft: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(seed.artifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactDoc := buildRecoveryFileWriteArtifactDocument(buildTaskLabel(taskRecord), seed.targetPath, seed.artifactDraft, seed.failureReason, nil, time.Now().UTC())
	if err := os.WriteFile(seed.artifactAbs, []byte(artifactDoc), 0o644); err != nil {
		t.Fatalf("write recovery artifact: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task before checkpoint seed: %v", err)
	}
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(currentTask.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:            seed.targetPath,
		ArtifactPath:          seed.artifactRel,
		FailureReason:         seed.failureReason,
		HistoryStartMessageID: historyStartMessageID.String(),
		HaltTurnID:            uuid.NewString(),
		UpdatedAt:             time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	currentTask.Metadata = checkpointMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update task with recovery checkpoint: %v", err)
	}

	return seed
}

func mustCreateTaskSession(t *testing.T, ctx context.Context, fixture *integrationFixture, taskRecord repo.ProjectTask, userPrompt string) (repo.ChatSession, repo.ChatMessage) {
	t.Helper()

	session, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project_task",
		ScopeID:        taskRecord.ID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession task scope: %v", err)
	}
	if _, err := fixture.chatService.AddParticipant(ctx, session.ID, "agent", fixture.agent.ID, "member"); err != nil {
		t.Fatalf("AddParticipant Frank: %v", err)
	}
	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    userPrompt,
	})
	if err != nil {
		t.Fatalf("AppendMessage task user prompt: %v", err)
	}
	return repo.ChatSession(*session), repo.ChatMessage(*userMessage)
}

func mustCreateRecoveredValidationTaskSession(t *testing.T, ctx context.Context, fixture *integrationFixture, taskRecord repo.ProjectTask) (repo.ChatSession, repo.ChatMessage) {
	t.Helper()
	return mustCreateRecoveredValidationTaskSessionWithKickoff(t, ctx, fixture, taskRecord, "supervisor recovery: resume task", map[string]any{
		"source":             "supervisor",
		"stranded_execution": true,
	})
}

func mustCreateTaskQueueRecoveredValidationTaskSession(t *testing.T, ctx context.Context, fixture *integrationFixture, taskRecord repo.ProjectTask) (repo.ChatSession, repo.ChatMessage) {
	t.Helper()
	return mustCreateRecoveredValidationTaskSessionWithKickoff(t, ctx, fixture, taskRecord, buildTaskQueueKickoffMessageForTest(taskRecord), map[string]any{
		"source":                    "task_queue_processor",
		"recovery_action":           "resume_validation_blocked_task",
		"validation_tool_name":      "cli.execute",
		"validation_failure_code":   "command_required",
		"validation_failure_reason": "command is required",
	})
}

func mustCreateRecoveredValidationTaskSessionWithKickoff(
	t *testing.T,
	ctx context.Context,
	fixture *integrationFixture,
	taskRecord repo.ProjectTask,
	kickoffContent string,
	kickoffMetadata map[string]any,
) (repo.ChatSession, repo.ChatMessage) {
	t.Helper()

	taskSession, userMessage := mustCreateTaskSession(t, ctx, fixture, taskRecord, "operator recovery attempt")

	taskRepo := repo.NewProjectTaskRepo(fixture.pool)
	currentTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(currentTask.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   userMessage.ID.String(),
		Fingerprint:        "cli.execute:command_is_required",
		AttemptFingerprint: "cli.execute:command_is_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_is_required",
		FailureReason:      "command is required",
		Count:              validationLoopBlockThreshold,
		BlockThreshold:     validationLoopBlockThreshold,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	currentTask.Metadata = guardedMetadata
	if _, err := taskRepo.Update(ctx, currentTask); err != nil {
		t.Fatalf("Update guarded task: %v", err)
	}

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     fixture.pool,
		EventBus: fixture.bus,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := taskService.MarkBlocked(ctx, taskRecord.ID, "deterministic tool validation loop blocked after 3 identical failures: cli.execute (command is required)", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if _, err := taskService.ResumeValidationBlockedTask(ctx, taskRecord.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}
	resumedTask, err := taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID resumed task: %v", err)
	}
	resumedTask.WorkStatus = "in_progress"
	if _, err := taskRepo.Update(ctx, resumedTask); err != nil {
		t.Fatalf("Update resumed task in_progress: %v", err)
	}

	authorType := "human_user"
	recoveryMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  taskSession.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    kickoffContent,
		Metadata:   mustJSON(t, kickoffMetadata),
	})
	if err != nil {
		t.Fatalf("AppendMessage recovery: %v", err)
	}
	return taskSession, repo.ChatMessage(*recoveryMessage)
}

func buildTaskQueueKickoffMessageForTest(taskRecord repo.ProjectTask) string {
	title := strings.TrimSpace(taskRecord.Title)
	if title == "" {
		title = "Untitled task"
	}
	description := ""
	if taskRecord.Description != nil {
		description = strings.TrimSpace(*taskRecord.Description)
	}
	if description == "" {
		return "Start work on task: " + title
	}
	return "Start work on task: " + title + "\n\nTask description:\n" + description
}

func forceLegacyChatTurnStopReasonConstraint(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		ALTER TABLE chat_turn
			DROP CONSTRAINT IF EXISTS chat_turn_stop_reason_check
	`); err != nil {
		t.Fatalf("drop chat_turn stop_reason constraint: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE chat_turn
			ADD CONSTRAINT chat_turn_stop_reason_check
			CHECK (
				stop_reason IS NULL
				OR stop_reason IN (
					'max_tool_calls',
					'max_duration',
					'user_cancelled',
					'user_steered',
					'model_error',
					'session_closed',
					'validation_loop_blocked'
				)
			)
	`); err != nil {
		t.Fatalf("add legacy chat_turn stop_reason constraint: %v", err)
	}
}

func flattenPrompt(prompt *prompt.AssembledPrompt) string {
	if prompt == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(prompt.SystemPrompt))
	for _, message := range prompt.Messages {
		builder.WriteString("\n")
		builder.WriteString(strings.TrimSpace(message.Content))
	}
	return builder.String()
}

func countRunnableAgentTurnJobsForSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = 'agent_turn'
		  AND status IN ('pending', 'claimed')
		  AND payload->>'session_id' = $1
	`, sessionID.String()).Scan(&count); err != nil {
		t.Fatalf("count runnable agent_turn jobs: %v", err)
	}
	return count
}

func countRunnableAgentTurnJobsForTasks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskIDs []uuid.UUID) int {
	t.Helper()

	if len(taskIDs) == 0 {
		return 0
	}

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT s.scope_id)
		FROM job_queue jq
		JOIN chat_session s ON s.id::text = jq.payload->>'session_id'
		WHERE jq.job_type = 'agent_turn'
		  AND jq.status IN ('pending', 'claimed')
		  AND s.scope_type = 'project_task'
		  AND s.mode = 'async'
		  AND s.scope_id = ANY($1::uuid[])
	`, taskIDs).Scan(&count); err != nil {
		t.Fatalf("count runnable task agent_turn jobs: %v", err)
	}
	return count
}

func waitForRunnableAgentTurnJobsForTasks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskIDs []uuid.UUID, wantAtLeast int) int {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		count := countRunnableAgentTurnJobsForTasks(t, ctx, pool, taskIDs)
		if count >= wantAtLeast {
			return count
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := countRunnableAgentTurnJobsForTasks(t, ctx, pool, taskIDs)
	t.Fatalf("runnable first-wave agent_turn jobs = %d, want >= %d", got, wantAtLeast)
	return 0
}

func mustProjectBootstrapCheckpoint(t *testing.T, state projectBootstrapState, name string) projectBootstrapCheckpoint {
	t.Helper()
	for _, checkpoint := range state.Checkpoints {
		if checkpoint.Name == name {
			return checkpoint
		}
	}
	t.Fatalf("missing bootstrap checkpoint %q in %+v", name, state.Checkpoints)
	return projectBootstrapCheckpoint{}
}

func mustProjectBootstrapProjectState(t *testing.T, projectRecord repo.Project) projectBootstrapProjectState {
	t.Helper()
	state := projectBootstrapProjectStateFromSettings(projectRecord.Settings)
	if state.Status == "" && state.CurrentPhase == "" && state.LastSuccessfulCheckpoint == "" {
		t.Fatalf("missing mirrored project bootstrap state in project settings: %s", string(projectRecord.Settings))
	}
	return state
}

func mustProjectBootstrapRestartBundle(t *testing.T, projectRecord repo.Project) projectBootstrapRestartBundle {
	t.Helper()
	bundle := projectBootstrapRestartBundleFromSettings(projectRecord.Settings)
	if bundle.OperatorBrief == "" {
		t.Fatalf("missing bootstrap restart bundle in project settings: %s", string(projectRecord.Settings))
	}
	return bundle
}

func mustGetProjectByID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) repo.Project {
	t.Helper()

	projectRecord, err := repo.NewProjectRepo(pool).GetByID(ctx, projectID)
	if err != nil {
		t.Fatalf("GetByID project: %v", err)
	}
	return projectRecord
}

func mustFindProjectAsyncSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID, projectID uuid.UUID) repo.ChatSession {
	t.Helper()
	sessions, err := repo.NewChatSessionRepo(pool).ListByOrg(ctx, organizationID)
	if err != nil {
		t.Fatalf("ListByOrg sessions: %v", err)
	}
	var latest *repo.ChatSession
	for i := range sessions {
		item := sessions[i]
		if item.ScopeID != projectID || !strings.EqualFold(strings.TrimSpace(item.ScopeType), "project") || !strings.EqualFold(strings.TrimSpace(item.Mode), "async") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Status), "active") {
			continue
		}
		if latest == nil || item.CreatedAt.After(latest.CreatedAt) {
			copyItem := item
			latest = &copyItem
		}
	}
	if latest == nil {
		t.Fatalf("missing active async project session for project %s", projectID)
	}
	return *latest
}

func runBootstrapFollowOnCycleToFailure(t *testing.T, ctx context.Context, fixture *integrationFixture, sessionID uuid.UUID) {
	t.Helper()

	latestTurn := latestCompletedTurnForSession(t, ctx, fixture.pool, sessionID)
	if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
		OrganizationID: fixture.org.ID,
		EventType:      "chat.turn.completed",
		Payload:        mustJSON(t, map[string]any{"session_id": sessionID.String(), "turn_id": latestTurn.ID.String()}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent bootstrap turn: %v", err)
	}

	for attempt := 1; attempt < maxProjectBootstrapAutoTurns; attempt++ {
		jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, sessionID)
		if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, payload.AgentID, payload.RetryCount, &jobID); err != nil {
			t.Fatalf("handleUserMessage bootstrap follow-on %d: %v", attempt, err)
		}
		latestTurn = latestCompletedTurnForSession(t, ctx, fixture.pool, sessionID)
		if err := fixture.engine.HandleTurnCompletedEvent(ctx, eventbus.DomainEvent{
			OrganizationID: fixture.org.ID,
			EventType:      "chat.turn.completed",
			Payload:        mustJSON(t, map[string]any{"session_id": sessionID.String(), "turn_id": latestTurn.ID.String()}),
		}); err != nil {
			t.Fatalf("HandleTurnCompletedEvent bootstrap follow-on %d: %v", attempt, err)
		}
	}
}

func runQueuedBootstrapTurnCycleToFailure(t *testing.T, ctx context.Context, fixture *integrationFixture, sessionID uuid.UUID) {
	t.Helper()

	jobID, payload := dequeueNextAgentTurnForSession(t, ctx, fixture.pool, sessionID)
	routedAgentID := payload.AgentID
	if routedAgentID == nil {
		if loriID, err := fixture.engine.resolveLoriStarterID(ctx, fixture.org.ID); err == nil && loriID != uuid.Nil {
			routedAgentID = &loriID
		} else if frankID, err := fixture.engine.resolveFrankStarterID(ctx, fixture.org.ID); err == nil && frankID != uuid.Nil {
			routedAgentID = &frankID
		}
	}
	if err := fixture.engine.handleUserMessage(ctx, payload.SessionID, payload.MessageID, routedAgentID, payload.RetryCount, &jobID); err != nil {
		t.Fatalf("handleUserMessage queued bootstrap turn: %v", err)
	}
	runBootstrapFollowOnCycleToFailure(t, ctx, fixture, sessionID)
}

func countRunnableAgentTurnJobsForProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) int {
	t.Helper()

	var count int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue jq
		JOIN chat_session s ON s.id::text = jq.payload->>'session_id'
		WHERE jq.job_type = $1
		  AND jq.status IN ('pending', 'claimed')
		  AND s.scope_type = 'project'
		  AND s.mode = 'async'
		  AND s.scope_id = $2
	`, AgentTurnJobType, projectID).Scan(&count); err != nil {
		t.Fatalf("count project agent_turn jobs: %v", err)
	}
	return count
}

func assertAutomaticFailureState(t *testing.T, projectRecord repo.Project, action, category, class, phase string) projectfailure.State {
	t.Helper()

	failureState := projectfailure.Parse(projectRecord.Settings)
	if failureState.Action != action {
		t.Fatalf("automatic failure action = %q, want %q", failureState.Action, action)
	}
	if failureState.FailureCategory != category {
		t.Fatalf("automatic failure category = %q, want %q", failureState.FailureCategory, category)
	}
	if failureState.FailureClass != class {
		t.Fatalf("automatic failure class = %q, want %q", failureState.FailureClass, class)
	}
	if failureState.FailurePhase != phase {
		t.Fatalf("automatic failure phase = %q, want %q", failureState.FailurePhase, phase)
	}
	if failureState.LastCheckpoint != phase {
		t.Fatalf("automatic failure last_checkpoint = %q, want %q", failureState.LastCheckpoint, phase)
	}
	if strings.TrimSpace(failureState.FailureReason) == "" {
		t.Fatal("expected automatic failure reason")
	}
	if failureState.RecordedAt == nil {
		t.Fatal("expected automatic failure recorded_at")
	}
	return failureState
}

func mustFindFirstNonBootstrapTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID) repo.ProjectTask {
	t.Helper()

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapGate, _ := metadata["bootstrap_gate"].(bool); bootstrapGate {
			continue
		}
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); bootstrapSetupTask {
			continue
		}
		return task
	}
	t.Fatal("expected non-bootstrap project task")
	return repo.ProjectTask{}
}

func mustFindBootstrapSetupTaskBySlug(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, slug string) repo.ProjectTask {
	t.Helper()

	tasks, err := repo.NewProjectTaskRepo(pool).ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject bootstrap setup tasks: %v", err)
	}
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); !bootstrapSetupTask {
			continue
		}
		if strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"])) == strings.TrimSpace(slug) {
			return task
		}
	}
	t.Fatalf("expected bootstrap setup task with slug %q", slug)
	return repo.ProjectTask{}
}

func mustCreateBootstrapSetupTaskExecution(t *testing.T, ctx context.Context, pool *pgxpool.Pool, task repo.ProjectTask) repo.FlowNodeExecution {
	t.Helper()

	if task.FlowTemplateID == nil || *task.FlowTemplateID == uuid.Nil {
		t.Fatal("expected bootstrap setup task flow template")
	}
	template, err := repo.NewFlowTemplateRepo(pool).GetByID(ctx, *task.FlowTemplateID)
	if err != nil {
		t.Fatalf("GetByID bootstrap setup flow template: %v", err)
	}
	if template.StartNodeID == nil || *template.StartNodeID == uuid.Nil {
		t.Fatal("expected bootstrap setup flow template start node")
	}
	execution, err := repo.NewFlowNodeExecutionRepo(pool).Create(ctx, repo.FlowNodeExecution{
		TaskID:      task.ID,
		FlowNodeID:  *template.StartNodeID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create bootstrap setup flow execution: %v", err)
	}
	return execution
}

func completeBootstrapSetupTasks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID uuid.UUID, skipStepSlug string) repo.ProjectTask {
	t.Helper()

	taskRepo := repo.NewProjectTaskRepo(pool)
	tasks, err := taskRepo.ListByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListByProject bootstrap setup tasks: %v", err)
	}

	var triggerTask repo.ProjectTask
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for _, task := range tasks {
		metadata := messageMetadataMap(task.Metadata)
		if bootstrapSetupTask, _ := metadata["bootstrap_setup_task"].(bool); !bootstrapSetupTask {
			continue
		}
		slug := strings.TrimSpace(stringValue(metadata["bootstrap_step_slug"]))
		if slug == strings.TrimSpace(skipStepSlug) {
			continue
		}
		task.WorkStatus = "done"
		task.CompletedAt = &now
		updated, updateErr := taskRepo.Update(ctx, task)
		if updateErr != nil {
			t.Fatalf("Update bootstrap setup task %q: %v", slug, updateErr)
		}
		if slug == bootstrapFrankSignOffStepSlug {
			triggerTask = updated
		} else if triggerTask.ID == uuid.Nil {
			triggerTask = updated
		}
	}
	if triggerTask.ID == uuid.Nil {
		t.Fatal("expected at least one completed bootstrap setup task")
	}
	return triggerTask
}

func mustCreateCompletedWorkTurn(t *testing.T, ctx context.Context, fixture *integrationFixture, sessionID, agentID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()

	authorType := "human_user"
	userMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		AuthorType: &authorType,
		AuthorID:   &fixture.user.ID,
		Role:       "user",
		Content:    "finish the current work and commit the result",
	})
	if err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}

	turn, err := fixture.chatService.CreateTurn(ctx, sessionID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chatService.StartTurn(ctx, turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	assistantType := "agent"
	assistantMessage, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		TurnID:     &turn.ID,
		AuthorType: &assistantType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Content:    "Task complete and ready for review.",
	})
	if err != nil {
		t.Fatalf("AppendMessage assistant: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, assistantMessage.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus assistant: %v", err)
	}

	fileResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: sessionID,
		TurnID:    &turn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "file.write", "output": map[string]any{"path": "docs/content-strategy/pillar-taxonomy.md", "byte_size": 128, "created": true}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage file tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, fileResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus file tool_result: %v", err)
	}

	commitResult, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID: sessionID,
		TurnID:    &turn.ID,
		Role:      "tool_result",
		Content:   string(mustJSON(t, map[string]any{"tool_name": "git.commit", "output": map[string]any{"sha": "fa05fa0", "short_sha": "fa05fa0", "files_committed": 1}})),
	})
	if err != nil {
		t.Fatalf("AppendMessage commit tool_result: %v", err)
	}
	if err := fixture.chatService.UpdateMessageStatus(ctx, commitResult.ID, "final", ""); err != nil {
		t.Fatalf("UpdateMessageStatus commit tool_result: %v", err)
	}

	if err := fixture.chatService.CompleteTurn(ctx, turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	return userMessage.ID, turn.ID
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

type capturingMemoryRetriever struct {
	calls    int
	memories []memory.RankedMemory
}

func (r *capturingMemoryRetriever) Query(_ context.Context, _ memory.RetrievalRequest) (memory.RetrievalResult, error) {
	r.calls++
	return memory.RetrievalResult{Memories: append([]memory.RankedMemory(nil), r.memories...)}, nil
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

func waitForJobStatus(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status string
		if err := pool.QueryRow(context.Background(), `
			SELECT status
			FROM job_queue
			WHERE id = $1
		`, jobID).Scan(&status); err == nil && status == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	var status string
	if err := pool.QueryRow(context.Background(), `
		SELECT status
		FROM job_queue
		WHERE id = $1
	`, jobID).Scan(&status); err != nil {
		t.Fatalf("query job status: %v", err)
	}
	t.Fatalf("job %s status = %q, want %q", jobID, status, want)
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

func mustCreateBootstrapProject(t *testing.T, ctx context.Context, fixture *integrationFixture) repo.Project {
	t.Helper()

	svc, err := projectsvc.NewService(projectsvc.Options{
		Pool:   fixture.pool,
		Events: fixture.bus,
	})
	if err != nil {
		t.Fatalf("project.NewService: %v", err)
	}
	created, err := svc.Create(ctx, projectsvc.CreateProjectRequest{
		OrganizationID: fixture.org.ID,
		Slug:           "bootstrap-project-" + uuid.NewString()[:8],
		DisplayName:    "Bootstrap Project",
		DeliveryMode:   "gated",
		CreatedByType:  "human_user",
		CreatedByID:    fixture.user.ID,
	})
	if err != nil {
		t.Fatalf("project.Create bootstrap: %v", err)
	}
	return repo.Project(*created)
}

func mustInsertProjectRepoBinding(t *testing.T, ctx context.Context, pool *pgxpool.Pool, project repo.Project) {
	t.Helper()

	repoPath, err := workspace.ProjectRoot("", project.Slug)
	if err != nil {
		t.Fatalf("workspace.ProjectRoot: %v", err)
	}
	if _, err := repo.NewProjectEnvironmentRepo(pool).Create(ctx, repo.ProjectEnvironment{
		ProjectID:    project.ID,
		Name:         "workspace",
		DeliveryMode: project.DeliveryMode,
		RepoPath: func() *string {
			value := repoPath
			return &value
		}(),
		TargetBranch: "main",
		IsActive:     true,
	}); err != nil {
		t.Fatalf("create repo binding environment: %v", err)
	}
}

func mustCreateProjectSession(t *testing.T, ctx context.Context, fixture *integrationFixture, projectID uuid.UUID, participantAgentIDs ...uuid.UUID) repo.ChatSession {
	t.Helper()

	session, err := fixture.chatService.CreateSession(ctx, chat.CreateSessionInput{
		OrganizationID: fixture.org.ID,
		ScopeType:      "project",
		ScopeID:        projectID,
		Mode:           "async",
	})
	if err != nil {
		t.Fatalf("CreateSession project scope: %v", err)
	}
	for _, agentID := range participantAgentIDs {
		if agentID == uuid.Nil {
			continue
		}
		if _, err := fixture.chatService.AddParticipant(ctx, session.ID, "agent", agentID, "member"); err != nil {
			t.Fatalf("AddParticipant agent %s: %v", agentID, err)
		}
	}
	return repo.ChatSession(*session)
}

func mustAppendProjectBootstrapHandoff(t *testing.T, ctx context.Context, fixture *integrationFixture, sessionID, authorAgentID uuid.UUID, content string) repo.ChatMessage {
	t.Helper()

	authorType := "agent"
	message, err := fixture.chatService.AppendMessage(ctx, chat.AppendMessageInput{
		SessionID:  sessionID,
		AuthorType: &authorType,
		AuthorID:   &authorAgentID,
		Role:       "user",
		Content:    content,
	})
	if err != nil {
		t.Fatalf("AppendMessage project bootstrap handoff: %v", err)
	}
	return repo.ChatMessage(*message)
}

func mustCreateBootstrapPMAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) repo.Agent {
	t.Helper()

	agentRecord, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          "Bootstrap PM",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		SystemPrompt:         "You are the bootstrap PM.",
		OperatorInstructions: "",
		AgentType:            "pm",
		PrivateMemory:        false,
		MemoryReadScopes:     []string{"org", "project", "agent"},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create bootstrap PM agent: %v", err)
	}
	return agentRecord
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

func mustCreateNonRunnableExecutionFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID, userID uuid.UUID) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "turn-invalid-flow-" + uuid.NewString()[:8],
		DisplayName:    "Turn Invalid Flow",
		Description:    "Turn engine invalid flow test template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "human_user",
		CreatedByID:    userID,
	})
	if err != nil {
		t.Fatalf("create invalid flow template: %v", err)
	}

	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create invalid work flow node: %v", err)
	}
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create invalid merge flow node: %v", err)
	}

	workNode.NextNodeID = &mergeNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("update invalid work flow node: %v", err)
	}

	template.StartNodeID = &workNode.ID
	template, err = templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("update invalid flow template start node: %v", err)
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

func latestCompletedTurnForSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) repo.ChatTurn {
	t.Helper()

	turns, err := repo.NewChatTurnRepo(pool).ListBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	turn := latestCompletedTurn(turns)
	if turn == nil {
		t.Fatal("expected completed turn")
	}
	return *turn
}

func dequeueNextAgentTurnForSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) (uuid.UUID, AgentTurnPayload) {
	t.Helper()

	var (
		jobID   uuid.UUID
		payload []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT id, payload
		FROM job_queue
		WHERE job_type = $1
		  AND status = 'pending'
		  AND payload->>'session_id' = $2
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, AgentTurnJobType, sessionID.String()).Scan(&jobID, &payload); err != nil {
		t.Fatalf("load pending agent_turn job: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM job_queue WHERE id = $1`, jobID); err != nil {
		t.Fatalf("delete pending agent_turn job: %v", err)
	}
	var decoded AgentTurnPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal pending agent_turn payload: %v", err)
	}
	return jobID, decoded
}
