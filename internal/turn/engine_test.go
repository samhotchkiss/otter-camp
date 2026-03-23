package turn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/projectfailure"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/tools"
	"log/slog"
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

func TestLogicalMessageCancelledUsesLatestAttemptForTriggerMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	messageID := uuid.New()
	sessionID := fixture.session.ID
	agentID := fixture.chat.participants[0].ParticipantID

	cancelledTurnID := uuid.New()
	completedRetryTurnID := uuid.New()
	trigger := messageID
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:               cancelledTurnID,
		SessionID:        sessionID,
		TurnNumber:       1,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "cancelled",
		TriggerMessageID: &trigger,
		RetryCount:       0,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)
	fixture.chat.turns[completedRetryTurnID] = &chat.ChatTurn{
		ID:               completedRetryTurnID,
		SessionID:        sessionID,
		TurnNumber:       2,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "completed",
		TriggerMessageID: &trigger,
		RetryCount:       1,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, completedRetryTurnID)

	cancelled, err := fixture.engine.logicalMessageCancelled(context.Background(), sessionID, messageID)
	if err != nil {
		t.Fatalf("logicalMessageCancelled: %v", err)
	}
	if cancelled {
		t.Fatal("logicalMessageCancelled = true, want false when later retry is not cancelled")
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

func TestListeningEvalSkippedForAsyncProjectTaskSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for async project_task sessions")
		return ModelResponse{}, nil
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
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestListeningEvalSkippedForActiveProjectBootstrapSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active","current_phase":"staffing_persisted"}}`)
	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		t.Fatal("listening eval should be skipped for active project bootstrap sessions")
		return ModelResponse{}, nil
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
	if fixture.model.listeningEvalCalls != 0 {
		t.Fatalf("listening eval calls = %d, want 0", fixture.model.listeningEvalCalls)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestHandleUserMessageFailsWhenInvocationCompletionFails(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.invocations.updateCompletionErr = errors.New("update completion failed")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "continue"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage err = nil, want invocation completion failure")
	}
	if !strings.Contains(err.Error(), "update completion failed") {
		t.Fatalf("HandleUserMessage err = %v, want update completion failed", err)
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

func TestHandleUserMessageSummarizeEnqueueUsesBackgroundPriority(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.summarization = &fakeSummarizationChecker{shouldSummarize: true}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
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

	agentJobs := fixture.enqueuer.agentTurnJobs()
	if len(agentJobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(agentJobs))
	}
	summarizeJobs := fixture.enqueuer.jobsByType(chat.ChatSummarizeJobType)
	if len(summarizeJobs) != 1 {
		t.Fatalf("chat_summarize jobs = %d, want 1", len(summarizeJobs))
	}
	if summarizeJobs[0].priority != backgroundSummarizeJobPriority {
		t.Fatalf("chat_summarize priority = %d, want %d", summarizeJobs[0].priority, backgroundSummarizeJobPriority)
	}
	if summarizeJobs[0].priority >= fixture.engine.jobPriority {
		t.Fatalf("chat_summarize priority = %d, want below agent_turn priority %d", summarizeJobs[0].priority, fixture.engine.jobPriority)
	}
}

func TestHandleUserMessagePartialStreamContextCanceledFailsTurnWithoutCancelling(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("partial"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{}, context.Canceled
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleUserMessage err = %v, want context.Canceled", err)
	}
	if fixture.chat.cancelCalls != 0 {
		t.Fatalf("cancel calls = %d, want 0", fixture.chat.cancelCalls)
	}
	if fixture.chat.failCalls != 1 {
		t.Fatalf("fail calls = %d, want 1", fixture.chat.failCalls)
	}
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed", turn.Status)
	}
}

func TestHandleUserMessageTaskSessionClosedDuringToolDispatchCancelsTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "task.inspect", Tier: "tier1"}}}
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{
			ToolCalls: []ModelToolCall{{
				ID:        "call_1",
				Name:      "task.inspect",
				Tier:      "tier1",
				Arguments: map[string]any{},
			}},
		}, nil
	}
	fixture.dispatcher.tier1Fn = func(context.Context, ToolCall) (ToolResult, error) {
		fixture.chat.session.Status = "closed"
		return ToolResult{}, context.Canceled
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if !errors.Is(err, errTurnCancelled) {
		t.Fatalf("HandleUserMessage err = %v, want %v", err, errTurnCancelled)
	}
	if fixture.chat.cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want 1", fixture.chat.cancelCalls)
	}
	if fixture.chat.failCalls != 0 {
		t.Fatalf("fail calls = %d, want 0", fixture.chat.failCalls)
	}
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.Status != "cancelled" {
		t.Fatalf("turn status = %q, want cancelled", turn.Status)
	}
}

func TestHandleUserMessageAsyncExecutionSessionIdempotentForStableMessageKey(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("first HandleUserMessage: %v", err)
	}
	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("second HandleUserMessage: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 1 {
		t.Fatalf("turn count = %d, want 1", got)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}

	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.TriggerMessageID == nil || *turn.TriggerMessageID != fixture.userMessageID {
		t.Fatalf("trigger_message_id = %v, want %s", turn.TriggerMessageID, fixture.userMessageID)
	}
	if turn.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", turn.RetryCount)
	}
}

func TestHandleTurnJobDuplicateDeliveryDoesNotCreateSecondTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
			JobType: AgentTurnJobType,
			Payload: payload,
		}); err != nil {
			t.Fatalf("HandleTurnJob[%d]: %v", i, err)
		}
	}

	if got := len(fixture.chat.turnOrder); got != 1 {
		t.Fatalf("turn count = %d, want 1", got)
	}
	if fixture.model.streamCalls != 1 {
		t.Fatalf("stream calls = %d, want 1", fixture.model.streamCalls)
	}
}

func TestHandleTurnJobCancelledMessageDoesNotCreateTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	metadata, err := chat.MergeAgentTurnDispatchCancelledMetadata(message.Metadata, "unit-test", time.Now().UTC())
	if err != nil {
		t.Fatalf("merge cancelled metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, metadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 0 {
		t.Fatalf("turn count = %d, want 0", got)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestHandleTurnJobRetryAttemptCreatesDistinctTurnAndRecordsRetryState(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("retry-" + req.TurnID.String()[:8]); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	firstPayload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal first payload: %v", err)
	}
	retryPayload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: 1,
	})
	if err != nil {
		t.Fatalf("marshal retry payload: %v", err)
	}

	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: firstPayload}); err != nil {
		t.Fatalf("HandleTurnJob first: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: firstPayload}); err != nil {
		t.Fatalf("HandleTurnJob duplicate: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{JobType: AgentTurnJobType, Payload: retryPayload}); err != nil {
		t.Fatalf("HandleTurnJob retry: %v", err)
	}

	if got := len(fixture.chat.turnOrder); got != 2 {
		t.Fatalf("turn count = %d, want 2", got)
	}
	if fixture.model.streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", fixture.model.streamCalls)
	}
	if !fixture.messages.containsContent("[Retry attempt 1 started.]") {
		t.Fatal("missing retry state system message")
	}

	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	secondTurn := fixture.chat.turnByID(fixture.chat.turnOrder[1])
	if firstTurn == nil || secondTurn == nil {
		t.Fatal("expected both turns to be present")
	}
	if firstTurn.RetryCount != 0 || secondTurn.RetryCount != 1 {
		t.Fatalf("retry counts = [%d %d], want [0 1]", firstTurn.RetryCount, secondTurn.RetryCount)
	}
	if firstTurn.TriggerMessageID == nil || secondTurn.TriggerMessageID == nil {
		t.Fatal("trigger_message_id should be set on both turns")
	}
	if *firstTurn.TriggerMessageID != fixture.userMessageID || *secondTurn.TriggerMessageID != fixture.userMessageID {
		t.Fatalf("trigger_message_ids = [%v %v], want %s", firstTurn.TriggerMessageID, secondTurn.TriggerMessageID, fixture.userMessageID)
	}
}

func TestHandleUserMessageEventProjectScopeFrankHandoffRoutesToExistingParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := fixture.chat.participants[0].ParticipantID
	workerID := uuid.New()
	loriID := uuid.New()
	actorID := frankID

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   frankID,
		},
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   workerID,
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID:  {ID: frankID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Frank", AgentType: "general"},
			workerID: {ID: workerID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Builder", AgentType: "worker"},
			loriID:   {ID: loriID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Lori", AgentType: "pm"},
		},
		starter: []repo.Agent{
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
		},
	}

	err := fixture.engine.HandleUserMessageEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.message.user_sent",
		ActorType:      "agent",
		ActorID:        &actorID,
		Payload: mustRawJSON(t, map[string]any{
			"session_id": fixture.session.ID,
			"message_id": fixture.userMessageID,
		}),
	})
	if err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.AgentID == nil {
		t.Fatal("agent_turn payload missing routed agent")
	}
	if *jobs[0].payload.AgentID != workerID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, workerID)
	}
}

func TestHandleUserMessageEventProjectScopeFrankHandoffFallsBackToLori(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := fixture.chat.participants[0].ParticipantID
	loriID := uuid.New()
	actorID := frankID

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{
		{
			ID:              uuid.New(),
			SessionID:       fixture.session.ID,
			ParticipantType: "agent",
			ParticipantID:   frankID,
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Frank", AgentType: "general"},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID, DisplayName: "Lori", AgentType: "pm"},
		},
		starter: []repo.Agent{
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
		},
	}

	err := fixture.engine.HandleUserMessageEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.message.user_sent",
		ActorType:      "agent",
		ActorID:        &actorID,
		Payload: mustRawJSON(t, map[string]any{
			"session_id": fixture.session.ID,
			"message_id": fixture.userMessageID,
		}),
	})
	if err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.AgentID == nil {
		t.Fatal("agent_turn payload missing routed agent")
	}
	if *jobs[0].payload.AgentID != loriID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, loriID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasLori := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == loriID {
			hasLori = true
			break
		}
	}
	if !hasLori {
		t.Fatalf("expected Lori %s to be added as a session participant", loriID)
	}
}

func TestHandleTurnJobRateLimitedEnqueuesRetryUsingProviderHint(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	retryAfter := 8*time.Minute + 4*time.Second
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(retryAfter, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.payload == nil {
		t.Fatal("retry payload missing")
	}
	if job.payload.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", job.payload.RetryCount)
	}
	if !job.payload.RateLimitJitterApplied {
		t.Fatal("expected rate limit retry payload to be marked as jittered")
	}
	if job.runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(jitteredRateLimitRetryDelay(retryAfter, fixture.session.ID, fixture.userMessageID, 0))
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *job.runAfter, wantRunAfter)
	}

	wantDelayMessage := fmt.Sprintf("[Rate limited, retrying in %s...]", formatRetryDelay(jitteredRateLimitRetryDelay(retryAfter, fixture.session.ID, fixture.userMessageID, 0)))
	if !fixture.messages.containsContent(wantDelayMessage) {
		t.Fatal("missing rate-limited retry status message")
	}
	if fixture.messages.containsContentSubstring("[Turn failed:") {
		t.Fatal("unexpected generic turn failed message for rate-limited retry")
	}
}

func TestHandleTurnJobRateLimitedUsesBackoffWhenNoRetryHint(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(0, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	if !jobs[0].payload.RateLimitJitterApplied {
		t.Fatal("expected rate limit retry payload to be marked as jittered")
	}
	if jobs[0].runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(defaultRateLimitBackoff)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
	if !fixture.messages.containsContent("[Rate limited, retrying in 30s...]") {
		t.Fatal("missing backoff retry status message")
	}
}

func TestHandleTurnJobRateLimitedRetryCapStopsRequeue(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.modelRetryBudget = 1
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, NewRateLimitedError(45*time.Second, errors.New("http status 429"))
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: maxRateLimitRetries,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn retries = %d, want 0", len(jobs))
	}
	if !fixture.messages.containsContent("[Turn failed: model retries exhausted after 5 attempts.]") {
		t.Fatal("missing retries exhausted status message")
	}
}

func TestHandleTurnJobTransientInfrastructureEnqueuesRetry(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }
	fixture.engine.modelRetryBudget = 1

	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, errors.New("failed to connect to database: FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute (SQLSTATE 53300)")
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn retries = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("retry payload missing")
	}
	if jobs[0].payload.RetryCount != 1 {
		t.Fatalf("retry_count = %d, want 1", jobs[0].payload.RetryCount)
	}
	if jobs[0].runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(defaultTransientInfraBackoff)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
	if !fixture.messages.containsContent("[Infrastructure temporarily unavailable, retrying in 15s...]") {
		t.Fatal("missing transient infrastructure retry status message")
	}
	if fixture.messages.containsContentSubstring("[Turn failed:") {
		t.Fatal("unexpected generic turn failed message for transient infrastructure retry")
	}
}

func TestHandleTurnJobTransientInfrastructureRetryCapStopsRequeue(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.modelRetryBudget = 1
	fixture.model.streamFn = func(context.Context, ModelRequest, func(string) error) (ModelResponse, error) {
		return ModelResponse{}, errors.New("pq: sorry, too many clients already")
	}

	payload, err := json.Marshal(AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: maxTransientInfraRetries,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fixture.engine.HandleTurnJob(context.Background(), jobqueue.Job{
		JobType: AgentTurnJobType,
		Payload: payload,
	}); err != nil {
		t.Fatalf("HandleTurnJob: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn retries = %d, want 0", len(jobs))
	}
	if !fixture.messages.containsContent("[Turn failed: temporary infrastructure retries exhausted after 5 attempts.]") {
		t.Fatal("missing transient infrastructure retries exhausted status message")
	}
}

func TestHandleTurnCompletedEventEnqueuesAutoContinuation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700000000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Investigating the next step.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("auto continuation payload missing")
	}
	if jobs[0].payload.SessionID != fixture.session.ID {
		t.Fatalf("payload.session_id = %s, want %s", jobs[0].payload.SessionID, fixture.session.ID)
	}
	if jobs[0].payload.MessageID != fixture.userMessageID {
		t.Fatalf("payload.message_id = %s, want %s", jobs[0].payload.MessageID, fixture.userMessageID)
	}
	if jobs[0].payload.AgentID == nil || *jobs[0].payload.AgentID != agentID {
		t.Fatalf("payload.agent_id = %v, want %s", jobs[0].payload.AgentID, agentID)
	}
	if jobs[0].runAfter == nil {
		t.Fatal("auto continuation run_after missing")
	}
	wantRunAfter := base.Add(defaultAutoContinueDelay)
	if !jobs[0].runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *jobs[0].runAfter, wantRunAfter)
	}
}

func TestHandleTurnCompletedEventSkipsWhenCompletionMessagePresent(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventAdvancesFlowFromSuccessfulGitCommit(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "git.commit",
		"output": map[string]any{
			"sha":             "fa05fa0abc123456",
			"files_committed": 1,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
	if fixture.flowAdvancer.lastCommitSHA != "fa05fa0abc123456" {
		t.Fatalf("last commit sha = %q, want fa05fa0abc123456", fixture.flowAdvancer.lastCommitSHA)
	}
	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	if fixture.flowAdvancer.lastAdvanceActor.Type != "agent" || fixture.flowAdvancer.lastAdvanceActor.ID != agentID {
		t.Fatalf("advance actor = %+v, want agent %s", fixture.flowAdvancer.lastAdvanceActor, agentID)
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review after successful completion", updatedTask.WorkStatus)
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventAdvancesFlowFromDurableRecoveryWrite(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Let me write the complete migration plan now:")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "file.write",
		"output": map[string]any{
			"path":      "docs/migration-plan/oc-15-content-migration-plan.md",
			"byte_size": 13720,
			"created":   false,
		},
	})

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review after durable recovery write", updatedTask.WorkStatus)
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventDuplicateCompletionSignalAdvancesExactlyOnce(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.flowAdvancer.tasks = taskRepo
	fixture.engine.flowAdvancer = fixture.flowAdvancer
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Task complete and ready for review.")
	appendToolResultMessage(t, fixture, turnID, map[string]any{
		"tool_name": "git.commit",
		"output": map[string]any{
			"sha":             "fa05fa0abc123456",
			"files_committed": 1,
		},
	})

	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}
	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), event); err != nil {
		t.Fatalf("first HandleTurnCompletedEvent: %v", err)
	}
	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), event); err != nil {
		t.Fatalf("second HandleTurnCompletedEvent: %v", err)
	}

	if fixture.flowAdvancer.advanceFlowCalls != 1 {
		t.Fatalf("advance flow calls = %d, want 1", fixture.flowAdvancer.advanceFlowCalls)
	}
	if fixture.flowAdvancer.recordNodeCommitCalls != 1 {
		t.Fatalf("record node commit calls = %d, want 1", fixture.flowAdvancer.recordNodeCommitCalls)
	}
}

func TestHandleTurnCompletedEventSkipsAtAutoContinuationCap(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	var latestTurnID uuid.UUID
	for i := 0; i < maxConsecutiveAutoTurns+1; i++ {
		latestTurnID = createCompletedTurnWithAssistantMessage(t, fixture, agentID, fmt.Sprintf("loop step %d", i+1))
	}

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": latestTurnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventRetriesGenericRecoveryReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700001000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help. What do you need?")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to assist. What would you like me to do?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericRecoveryReplyHelpWithVariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Metadata = mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready. What would you like me to help with for this task?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventRetriesGenericTaskContinuationReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	base := time.Unix(1700002000, 0).UTC()
	fixture.engine.now = func() time.Time { return base }

	taskID := uuid.New()
	nodeID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Continue the active task now from the continuation summary above."
	message.Metadata = taskContinuationResumeMessageMetadata(1)
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help with OC-24. What would you like me to do?")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent_turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.RetryCount != 1 {
		t.Fatalf("payload.retry_count = %#v, want 1", jobs[0].payload)
	}
	if jobs[0].payload.MessageID == fixture.userMessageID {
		t.Fatalf("payload.message_id = %s, want fresh retry prompt message", jobs[0].payload.MessageID)
	}
	if jobs[0].runAfter == nil || !jobs[0].runAfter.Equal(base.Add(defaultAutoContinueDelay)) {
		t.Fatalf("run_after = %v, want %s", jobs[0].runAfter, base.Add(defaultAutoContinueDelay))
	}
	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	latest := messages[len(messages)-1]
	if latest.ID != jobs[0].payload.MessageID {
		t.Fatalf("latest message id = %s, want %s", latest.ID, jobs[0].payload.MessageID)
	}
	if latest.Role != "user" {
		t.Fatalf("latest role = %q, want user", latest.Role)
	}
	if latest.Content != message.Content {
		t.Fatalf("latest content = %q, want retry prompt copy", latest.Content)
	}
}

func TestHandleTurnCompletedEventBlocksRepeatedGenericTaskContinuationReply(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.taskTransitions = blocker

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID user message: %v", err)
	}
	message.Content = "Continue the active task now from the continuation summary above."
	message.Metadata = taskContinuationResumeMessageMetadata(1)
	fixture.messages.upsert(message)

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "I'm ready to help with OC-24. What would you like me to do?")
	fixture.chat.mu.Lock()
	fixture.chat.turns[turnID].RetryCount = maxGenericRecoveryReplyRetries
	fixture.chat.mu.Unlock()

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
	if len(blocker.calls) != 1 {
		t.Fatalf("blocked calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "generic non-action replies") {
		t.Fatalf("blocked reason = %q, want generic non-action reply reason", blocker.calls[0].reason)
	}
	updated, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updated.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updated.WorkStatus)
	}
}

func TestHandleTurnCompletedEventSkipsAutoContinuationForRecoveryHaltMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
				Metadata:          json.RawMessage(`{}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	authorType := "human_user"
	userMessage, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID:  fixture.session.ID,
		AuthorType: &authorType,
		AuthorID:   &fixture.session.OrganizationID,
		Role:       "user",
		Content:    "supervisor recovery: resume task",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	recoveryMetadata := mustRawJSON(t, map[string]any{"recovery_action": recoveryActionValidationResume})
	if _, err := fixture.messages.UpdateMetadata(context.Background(), userMessage.ID, recoveryMetadata); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnRecord, _, err := fixture.engine.turns.CreateForMessageAttempt(context.Background(), fixture.session.ID, agentID, userMessage.ID, 3)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	authorType = "agent"
	fixture.messages.create(repo.ChatMessage{
		SessionID:  fixture.session.ID,
		TurnID:     &turnRecord.ID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Status:     "final",
		Content:    "Perfect. I have the substantive draft and now I'll write it to the target file.",
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnRecord.ID,
		Role:      "system",
		Status:    "final",
		Content:   "[Recovery turn halted: recovered file.write for `docs/target.md` produced another non-substantive draft after the prior checkpoint already rejected placeholder narration.]",
	})

	metadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRepo.items[taskID].Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/target.md",
		ArtifactPath:  ".ottercamp/recovery/docs/target.md",
		FailureReason: "repeated non-substantive recovery drafts for docs/target.md across explicit resume attempts; latest assistant draft for docs/target.md described intent to write the deliverable instead of the file body",
		HaltTurnID:    turnRecord.ID.String(),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	record := taskRepo.items[taskID]
	record.Metadata = metadata
	taskRepo.items[taskID] = record

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnRecord.ID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestHandleTurnCompletedEventSkipsWhenProjectPaused(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	nodeID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    fixture.session.OrganizationID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
			},
		},
	}
	fixture.engine.flowNodes = &fakeFlowNodeRepo{
		items: map[uuid.UUID]repo.FlowNode{
			nodeID: {ID: nodeID, NodeType: "work"},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnID := createCompletedTurnWithAssistantMessage(t, fixture, agentID, "Investigating the next step.")

	if err := fixture.engine.HandleTurnCompletedEvent(context.Background(), eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.completed",
		Payload:        mustRawJSON(t, map[string]any{"session_id": fixture.session.ID, "turn_id": turnID}),
	}); err != nil {
		t.Fatalf("HandleTurnCompletedEvent: %v", err)
	}

	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent_turn jobs = %d, want 0", len(jobs))
	}
}

func TestIsAlreadyQueuedTaskTransition(t *testing.T) {
	if !isAlreadyQueuedTaskTransition(tasksvc.ErrInvalidStatusTransition{From: "queued", To: "queued"}) {
		t.Fatal("queued -> queued transition should be treated as already queued")
	}
	if isAlreadyQueuedTaskTransition(tasksvc.ErrInvalidStatusTransition{From: "draft", To: "queued"}) {
		t.Fatal("draft -> queued transition should not be treated as already queued")
	}
	if isAlreadyQueuedTaskTransition(errors.New("other error")) {
		t.Fatal("non-transition errors should not be treated as already queued")
	}
}

func TestAppendProjectBootstrapContinuationMessageWithoutAuthorUsesSystem(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	message, err := fixture.engine.appendProjectBootstrapContinuationMessageWithContent(
		context.Background(),
		fixture.session.ID,
		uuid.Nil,
		fixture.userMessageID.String(),
		1,
		"Continue bootstrap.",
	)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessageWithContent: %v", err)
	}

	stored, err := fixture.messages.GetByID(context.Background(), message.ID)
	if err != nil {
		t.Fatalf("GetByID continuation message: %v", err)
	}
	if stored.AuthorType != nil {
		t.Fatalf("author_type = %v, want nil for system-authored continuation message", *stored.AuthorType)
	}
	if stored.AuthorID != nil {
		t.Fatalf("author_id = %v, want nil", stored.AuthorID)
	}
	if got := strings.TrimSpace(stored.Role); got != "user" {
		t.Fatalf("role = %q, want user", got)
	}
}

func TestAppendProjectBootstrapRecoveryContinuationMessageIncludesNamedTaskFromProgress(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	pmID := uuid.New()
	workerID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active","current_phase":"first_wave_executions_created"}}`)
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: pmID, Role: "project_manager"},
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Lori"},
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				ProjectID:       projectID,
				TaskNumber:      23,
				Title:           "Draft second blog post",
				WorkStatus:      "draft",
				AssignedAgentID: nil,
			},
		},
	}

	msg, err := fixture.engine.appendProjectBootstrapRecoveryContinuationMessage(
		context.Background(),
		fixture.session.ID,
		fixture.chat.participants[0].ParticipantID,
		fixture.userMessageID.String(),
		3,
		projectBootstrapProgress{
			ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
			ValidationFailureReason: "kickoff validation failed: first-wave task 23 (Draft second blog post) has no assigned agent, so bootstrap cannot queue runnable execution.",
		},
	)
	if err != nil {
		t.Fatalf("appendProjectBootstrapRecoveryContinuationMessage: %v", err)
	}
	if !strings.Contains(msg.Content, "Named blocked task: task 23 id="+taskID.String()) {
		t.Fatalf("message = %q, want named blocked task line with exact id", msg.Content)
	}
	if !strings.Contains(msg.Content, "workers=Dev") {
		t.Fatalf("message = %q, want assignment roster", msg.Content)
	}
}

func TestContinueTurnStopsWhenProjectPaused(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	taskID := uuid.New()
	projectID := uuid.New()
	agentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
			},
		},
	}

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	rt := &turnRuntime{
		session:          fixture.session,
		agent:            repo.Agent{ID: agentID, OrganizationID: fixture.session.OrganizationID},
		turn:             turn,
		initialMessageID: fixture.userMessageID,
	}
	if err := fixture.engine.continueTurn(context.Background(), rt); !errors.Is(err, errTurnPaused) {
		t.Fatalf("continueTurn err = %v, want errTurnPaused", err)
	}

	if len(fixture.chat.turnOrder) != 1 {
		t.Fatalf("turn count = %d, want 1", len(fixture.chat.turnOrder))
	}
	currentTurn, err := fixture.chat.GetTurn(context.Background(), turn.ID)
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if currentTurn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed", currentTurn.Status)
	}
	if !fixture.messages.containsContentSubstring("Project paused - continuation deferred until resume") {
		t.Fatal("missing paused continuation message")
	}
}

func TestHandleUserMessageEventSkipsClosedSession(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.Status = "closed"

	event := eventbus.DomainEvent{
		EventType: "chat.message.user_sent",
	}
	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event.Payload = payload
	if err := fixture.engine.HandleUserMessageEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleUserMessageEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 0 {
		t.Fatalf("agent turn jobs = %d, want 0 for closed session", got)
	}
}

func TestHandleUserMessageProjectBootstrapWatchdogCancelsBlockedChunkPersistence(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	projectID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active"}}`)
	fixture.engine.projectBootstrapTurnTimeout = 40 * time.Millisecond
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Status:         "active",
			},
		},
	}

	updateContentCalled := make(chan struct{}, 1)
	fixture.messages.updateContentFn = func(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
		select {
		case updateContentCalled <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return repo.ChatMessage{}, ctx.Err()
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("blocked "); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "unexpected success"}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessageID)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("HandleUserMessage err = %v, want nil with watchdog failure handled in-turn", err)
	}
	if fixture.model.streamCalls == 0 {
		t.Fatal("expected model stream call")
	}
	select {
	case <-updateContentCalled:
	default:
		t.Fatal("expected streamed chunk persistence to be attempted")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("HandleUserMessage elapsed = %s, want watchdog cancellation before outer context timeout", elapsed)
	}
	latestTurn := fixture.chat.turns[fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]]
	if latestTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed after bootstrap watchdog timeout", latestTurn.Status)
	}
	if latestTurn.StopReason == nil || *latestTurn.StopReason != stopReasonMaxDuration {
		t.Fatalf("turn stop_reason = %v, want %q", latestTurn.StopReason, stopReasonMaxDuration)
	}
	if !fixture.messages.containsContentSubstring("watchdog timed out") {
		t.Fatal("missing watchdog timeout message")
	}
}

func TestHandleUserMessageProjectBootstrapWatchdogReturnsWhenModelIgnoresCancellation(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	projectID := uuid.New()
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = json.RawMessage(`{"project_bootstrap":{"status":"active"}}`)
	fixture.engine.projectBootstrapTurnTimeout = 40 * time.Millisecond
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: fixture.session.OrganizationID,
				Status:         "active",
			},
		},
	}

	release := make(chan struct{})
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		<-release
		return ModelResponse{}, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	started := time.Now()
	err := fixture.engine.HandleUserMessage(ctx, fixture.session.ID, fixture.userMessageID)
	elapsed := time.Since(started)
	close(release)
	if err != nil {
		t.Fatalf("HandleUserMessage err = %v, want nil with watchdog failure handled in-turn", err)
	}
	if fixture.model.streamCalls == 0 {
		t.Fatal("expected model stream call")
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("HandleUserMessage elapsed = %s, want watchdog cancellation before outer context timeout", elapsed)
	}
	latestTurn := fixture.chat.turns[fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]]
	if latestTurn.Status != "failed" {
		t.Fatalf("turn status = %q, want failed after bootstrap watchdog timeout", latestTurn.Status)
	}
	if latestTurn.StopReason == nil || *latestTurn.StopReason != stopReasonMaxDuration {
		t.Fatalf("turn stop_reason = %v, want %q", latestTurn.StopReason, stopReasonMaxDuration)
	}
	if !fixture.messages.containsContentSubstring("watchdog timed out") {
		t.Fatal("missing watchdog timeout message")
	}
}

func TestHandleUserMessageTaskScopeRoutesToAssignedAgent(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			assignedID: {ID: assignedID, OrganizationID: fixture.session.OrganizationID},
			frankID:    {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != assignedID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, assignedID)
	}
}

func TestHandleUserMessageTaskScopeSyncRoutesToProjectPMAndAddsParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	taskID := uuid.New()
	projectID := uuid.New()
	pmID := uuid.New()
	assignedID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:       {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			assignedID: {ID: assignedID, OrganizationID: fixture.session.OrganizationID},
			frankID:    {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != pmID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, pmID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasPM := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == pmID {
			hasPM = true
			break
		}
	}
	if !hasPM {
		t.Fatalf("expected project PM %s to be added as a session participant", pmID)
	}
}

func TestHandleUserMessageTaskScopeRequiresAssignedAgent(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	pmID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:    {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage error = nil, want missing assigned agent invariant")
	}
	if !strings.Contains(err.Error(), "internal invariant: task-scoped session is missing assigned agent") {
		t.Fatalf("HandleUserMessage error = %v, want missing assigned agent invariant", err)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestResolveSessionAgentForSessionRecoversMissingTaskAssigneeFromSessionParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	workerID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   workerID,
	}}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{{
			ProjectID: projectID,
			AgentID:   workerID,
			IsActive:  true,
			Role:      "worker",
		}},
	}

	agentID, err := fixture.engine.resolveSessionAgentForSession(context.Background(), fixture.session)
	if err != nil {
		t.Fatalf("resolveSessionAgentForSession: %v", err)
	}
	if agentID != workerID {
		t.Fatalf("agent_id = %s, want %s", agentID, workerID)
	}

	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updatedTask.AssignedAgentID == nil || *updatedTask.AssignedAgentID != workerID {
		t.Fatalf("assigned_agent_id = %v, want %s", updatedTask.AssignedAgentID, workerID)
	}
}

func TestResolveSessionAgentForSessionRoutesReviewTaskToDistinctReviewer(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	workerID := uuid.New()
	pmID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				WorkStatus:      "review",
				AssignedAgentID: &workerID,
			},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, IsActive: true, Role: "worker"},
			{ProjectID: projectID, AgentID: pmID, IsActive: true, Role: "project_manager"},
		},
	}

	agentID, err := fixture.engine.resolveSessionAgentForSession(context.Background(), fixture.session)
	if err != nil {
		t.Fatalf("resolveSessionAgentForSession: %v", err)
	}
	if agentID != pmID {
		t.Fatalf("agent_id = %s, want reviewer/PM %s", agentID, pmID)
	}
}

func TestShouldAppendSyntheticUserPromptSkipsDuplicatePendingSource(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "Continue the active task now from the continuation summary above.",
		Metadata:  syntheticContinuationActionMessageMetadata(taskContinuationResumeMessageSource),
	})

	shouldAppend, err := fixture.engine.shouldAppendSyntheticUserPrompt(context.Background(), fixture.session.ID, taskContinuationResumeMessageSource)
	if err != nil {
		t.Fatalf("shouldAppendSyntheticUserPrompt: %v", err)
	}
	if shouldAppend {
		t.Fatal("shouldAppendSyntheticUserPrompt = true, want false for duplicate pending synthetic prompt")
	}
}

func TestNormalizeRoutedAgentForSessionDropsStaleAsyncTaskOverride(t *testing.T) {
	agentID := uuid.New()
	session := &chat.ChatSession{ScopeType: "project_task", Mode: "async"}
	if got := normalizeRoutedAgentForSession(session, &agentID); got != nil {
		t.Fatalf("normalizeRoutedAgentForSession returned %v, want nil for async task session", *got)
	}
}

func TestHandleUserMessageProjectScopeRoutesToProjectPMAndAddsParticipant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	pmID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{
		items: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ProjectID: projectID, AgentID: pmID, IsActive: true},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			pmID:    {ID: pmID, OrganizationID: fixture.session.OrganizationID},
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{{ID: frankID, DisplayName: "Frank", AgentType: "general"}},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != pmID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, pmID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasPM := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == pmID {
			hasPM = true
			break
		}
	}
	if !hasPM {
		t.Fatalf("expected project PM %s to be added as a session participant", pmID)
	}
}

func TestProjectCreateStateMachinePreventsConflictReentryAfterSuccess(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
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
			return ModelResponse{Content: "Proceeding with the created project."}, nil
		}
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if strings.Join(dispatched, ",") != "create-conflict,create-success" {
		t.Fatalf("dispatched project.create calls = %v, want [create-conflict create-success]", dispatched)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	wantLock := fmt.Sprintf("Project identity locked: slug=%s project_id=%s", projectSlug, projectID)
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

func TestHandleUserMessageProjectScopeKickoffStartsWithFrank(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageProjectScopeBootstrapScaffoldStartsWithLori(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"staff-project"}`)},
			uuid.New(): {ProjectID: projectID, Metadata: json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"decompose-workstreams"}`)},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != loriID {
		t.Fatalf("turn responding_id = %s, want Lori %s", turn.RespondingID, loriID)
	}
}

func TestShouldBlockFreshKickoffPreCreateTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "organization",
		},
		agent: repo.Agent{
			ID:          uuid.New(),
			DisplayName: "Frank",
		},
	}
	if !shouldBlockFreshKickoffPreCreateTool(rt, "project.list") {
		t.Fatal("fresh kickoff should block pre-create project browsing")
	}
	if !shouldBlockFreshKickoffPreCreateTool(rt, "memory.search") {
		t.Fatal("fresh kickoff should block pre-create memory browsing")
	}
	if shouldBlockFreshKickoffPreCreateTool(rt, "project.create") {
		t.Fatal("fresh kickoff should allow project.create")
	}
	rt.projectIdentity = &projectIdentity{id: uuid.New(), slug: "sam-blog-fresh"}
	if shouldBlockFreshKickoffPreCreateTool(rt, "project.list") {
		t.Fatal("fresh kickoff pre-create block should clear after project identity is established")
	}
}

func TestShouldBlockFreshKickoffMemoryTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "project",
		},
	}
	if !shouldBlockFreshKickoffMemoryTool(rt, "memory.search") {
		t.Fatal("fresh kickoff should block project-scope memory search")
	}
	if !shouldBlockFreshKickoffMemoryTool(rt, "memory.list") {
		t.Fatal("fresh kickoff should block project-scope memory list")
	}
	if shouldBlockFreshKickoffMemoryTool(rt, "project.get") {
		t.Fatal("fresh kickoff should not block non-memory tools here")
	}
	rt.freshKickoff = false
	if shouldBlockFreshKickoffMemoryTool(rt, "memory.search") {
		t.Fatal("non-fresh kickoff should not block memory tools")
	}
}

func TestHandleUserMessageRecoveryMessageWithFreshKickoffMetadataDisablesMemory(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	metadata, err := json.Marshal(map[string]any{"fresh_kickoff": true, "source": projectBootstrapSource})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	fixture.messages.items[fixture.userMessageID] = repo.ChatMessage{
		ID:             fixture.userMessageID,
		SessionID:      fixture.session.ID,
		SequenceNumber: 1,
		Role:           "user",
		Status:         "pending",
		Content:        "Continue the bootstrap recovery now.",
		Metadata:       metadata,
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if !input.DisableMemory {
			t.Fatalf("assemble call %d DisableMemory = false, want true", call)
		}
	}
	fixture.model.completeFn = func(_ context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
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
}

func TestShouldBlockFreshKickoffAgentBrowseTool(t *testing.T) {
	rt := &turnRuntime{
		freshKickoff: true,
		session: &repo.ChatSession{
			ScopeType: "project",
		},
	}
	if !shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("fresh kickoff should block project-scope agent.list")
	}
	if !shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.get") {
		t.Fatal("fresh kickoff should block project-scope agent.get")
	}
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.create_draft") {
		t.Fatal("fresh kickoff should not block creating fresh staff")
	}
	rt.session.ScopeType = "organization"
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("organization-scope fresh kickoff should already be covered by pre-create tool guard, not agent browse guard")
	}
	rt.freshKickoff = false
	rt.session.ScopeType = "project"
	if shouldBlockFreshKickoffAgentBrowseTool(rt, "agent.list") {
		t.Fatal("non-fresh kickoff should not block agent browsing")
	}
}

func TestAppendProjectBootstrapContinuationMessagePreservesFreshKickoffMetadata(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	initialID := uuid.New()
	initial := fixture.messages.create(repo.ChatMessage{
		ID:        initialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "Start Sam.blog from scratch as a fresh kickoff. Do not reuse archived work.",
	})

	msg, err := fixture.engine.appendProjectBootstrapContinuationMessage(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID, initial.ID.String(), 2)
	if err != nil {
		t.Fatalf("appendProjectBootstrapContinuationMessage: %v", err)
	}
	metadata := messageMetadataMap(msg.Metadata)
	if fresh, _ := metadata["fresh_kickoff"].(bool); !fresh {
		t.Fatalf("fresh_kickoff metadata = %v, want true", metadata["fresh_kickoff"])
	}
}

func TestBuildSyntheticProjectKickoffHandoffPrefersFreshProjectContext(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	content := "Start a brand-new Sam.blog project from scratch. Do not reuse archived chains."
	if _, err := fixture.messages.UpdateContent(context.Background(), fixture.userMessageID, content); err != nil {
		t.Fatalf("UpdateContent user message: %v", err)
	}

	rt := &turnRuntime{
		initialMessageID: fixture.userMessageID,
		projectIdentity: &projectIdentity{
			slug: "sam-blog-fresh-test",
			id:   uuid.New(),
		},
	}

	handoff := fixture.engine.buildSyntheticProjectKickoffHandoff(context.Background(), rt)
	if !strings.Contains(handoff, "Treat this as a fresh project bootstrap.") {
		t.Fatalf("handoff = %q, want fresh bootstrap guidance", handoff)
	}
	if !strings.Contains(handoff, "Do not assume architecture, CMS choice, or workflow from archived/restart chains or org memory") {
		t.Fatalf("handoff = %q, want anti-reuse guidance", handoff)
	}
	if !strings.Contains(handoff, "Prefer the current project description and live tool results over prior-project memory.") {
		t.Fatalf("handoff = %q, want current-project preference", handoff)
	}
	if !strings.Contains(handoff, "Do not call memory.query, memory.list, or other memory tools during this bootstrap handoff") {
		t.Fatalf("handoff = %q, want explicit no-memory guidance", handoff)
	}
	if !strings.Contains(handoff, "Frank, Lori, and Ellie are starter-trio governance agents") {
		t.Fatalf("handoff = %q, want starter-trio staffing guidance", handoff)
	}
	if !strings.Contains(handoff, "Keep staffing discovery bounded.") {
		t.Fatalf("handoff = %q, want bounded staffing discovery guidance", handoff)
	}
	if !strings.Contains(handoff, "Use at most one staffing.browse_profiles pass per needed category") {
		t.Fatalf("handoff = %q, want explicit staffing discovery budget", handoff)
	}
	if !strings.Contains(handoff, "Do not spend a turn writing a staffing plan, rationale memo, or markdown table before you materialize staff.") {
		t.Fatalf("handoff = %q, want no-staffing-memo guidance", handoff)
	}
	if !strings.Contains(handoff, "Once enough candidates are known, do not emit another assistant planning summary about staffing.") {
		t.Fatalf("handoff = %q, want direct-tool-action staffing guidance", handoff)
	}
	if !strings.Contains(handoff, "If you need project docs, files, planning artifacts, or current task state during bootstrap, inspect them directly with tools.") {
		t.Fatalf("handoff = %q, want direct-doc-inspection guidance", handoff)
	}
	if !strings.Contains(handoff, "Before you pause to read scaffold artifacts or persist setup, every persisted broad workstream parent must either have bounded executable child tasks under it") {
		t.Fatalf("handoff = %q, want all-parents-decomposed guidance", handoff)
	}
	if !strings.Contains(handoff, content) {
		t.Fatalf("handoff = %q, want originating request content", handoff)
	}
}

func TestBuildProjectBootstrapRestartPromptRequiresFinishingAllBroadParents(t *testing.T) {
	prompt := buildProjectBootstrapRestartPrompt(
		projectBootstrapRestartBundle{OperatorBrief: "Frank handoff: create the initial staffed bootstrap for this project."},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-old"},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-new"},
		false,
	)
	if !strings.Contains(prompt, "Do not answer with a standalone acknowledgement or status note.") {
		t.Fatalf("prompt = %q, want no-acknowledgement guidance", prompt)
	}
	if !strings.Contains(prompt, "This first restart turn must contain the concrete staffing/task mutation tool calls needed to recreate staffed executable work") {
		t.Fatalf("prompt = %q, want direct restart action guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with broad rereads like project.get, task.list, flow.list_templates, file.list, git.log, or memory tools.") {
		t.Fatalf("prompt = %q, want no-broad-rereads guidance", prompt)
	}
	if !strings.Contains(prompt, "Before you pause to read scaffold artifacts or persist setup, every persisted broad workstream parent must either have bounded executable child tasks under it") {
		t.Fatalf("prompt = %q, want all-parents-decomposed guidance", prompt)
	}
}

func TestBuildProjectBootstrapRestartPromptForSeededScaffold(t *testing.T) {
	prompt := buildProjectBootstrapRestartPrompt(
		projectBootstrapRestartBundle{OperatorBrief: "Frank handoff: repair the archived bootstrap scaffold."},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-old"},
		repo.Project{ID: uuid.New(), Slug: "sam-blog-new"},
		true,
	)
	if !strings.Contains(prompt, "already been seeded with the archived project's persisted assignments and draft task scaffold") {
		t.Fatalf("prompt = %q, want seeded scaffold guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not recreate that scaffold from scratch") {
		t.Fatalf("prompt = %q, want no-recreate guidance", prompt)
	}
}

func TestProjectBootstrapRestartSeededFailureProgressFallsBackToRecoverableHistory(t *testing.T) {
	progress, ok := projectBootstrapRestartSeededFailureProgress(
		projectBootstrapProgress{},
		projectAutomaticFailureRecord{
			FailureClass:  projectBootstrapFailureStalled,
			FailureReason: `kickoff validation failed: automatic bootstrap restart replied with narrative only and never persisted staffed executable work; reply="I'll inspect the seeded scaffold first."`,
		},
		projectBootstrapRestartBundle{
			FailureHistory: []projectfailure.FailureHistoryEntry{
				{
					FailureClass:  projectBootstrapFailureFirstWaveExecution,
					FailureReason: "kickoff validation failed: first-wave task 76 (Use a topic from the editorial calendar) has no assigned agent, so bootstrap cannot queue runnable execution",
				},
			},
		},
	)
	if !ok {
		t.Fatal("projectBootstrapRestartSeededFailureProgress ok = false, want recoverable history fallback")
	}
	if got := strings.TrimSpace(progress.ValidationFailureReason); !strings.Contains(got, "Use a topic from the editorial calendar") {
		t.Fatalf("validation_failure_reason = %q, want recoverable history reason", got)
	}
	if got := strings.TrimSpace(progress.ValidationFailureClass); got != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("validation_failure_class = %q, want %q", got, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestBootstrapRestartSlugCandidateCollapsesRestartChains(t *testing.T) {
	got := bootstrapRestartSlugCandidate("sam-blog-110-restart-restart-restart-13", 1)
	if got != "sam-blog-110-restart" {
		t.Fatalf("bootstrapRestartSlugCandidate collapse = %q, want %q", got, "sam-blog-110-restart")
	}
}

func TestBootstrapRestartSlugCandidateStaysWithinProjectSlugLimit(t *testing.T) {
	base := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm-restart-12"
	got := bootstrapRestartSlugCandidate(base, 123)
	if len(got) > maxProjectSlugLength {
		t.Fatalf("bootstrapRestartSlugCandidate len = %d, want <= %d (%q)", len(got), maxProjectSlugLength, got)
	}
	if !strings.HasSuffix(got, "-restart-123") {
		t.Fatalf("bootstrapRestartSlugCandidate suffix = %q, want -restart-123", got)
	}
}

func TestShouldBlockProjectBootstrapRestaffingToolAfterTaskTreePersisted(t *testing.T) {
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          7,
		PlannedTaskCount:         12,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRestaffingTool(rt, "staffing.browse_profiles") {
		t.Fatal("expected staffing.browse_profiles to be blocked after task tree persisted")
	}
	if !shouldBlockProjectBootstrapRestaffingTool(rt, "agent.create_staff") {
		t.Fatal("expected agent.create_staff to be blocked after task tree persisted")
	}
	if shouldBlockProjectBootstrapRestaffingTool(rt, "agent.assign_project") {
		t.Fatal("agent.assign_project should not be blocked by restaffing guard")
	}
}

func TestShouldBlockProjectBootstrapExcessStaffingDiscoveryTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
		},
		toolCallsUsed: projectBootstrapStaffingDiscoveryBudget,
	}
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{Status: projectBootstrapStatusActive})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt.session.Metadata = metadata
	if !shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("expected staffing browse to be blocked after discovery budget is exhausted")
	}
	if !shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.get_profile") {
		t.Fatal("expected staffing get_profile to be blocked after discovery budget is exhausted")
	}
	rt.toolCallsUsed = projectBootstrapStaffingDiscoveryBudget - 1
	if shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("unexpected staffing browse block before discovery budget is exhausted")
	}
	metadata, err = projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:          projectBootstrapStatusActive,
		AssignmentCount: 1,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON assignments: %v", err)
	}
	rt.session.Metadata = metadata
	rt.toolCallsUsed = projectBootstrapStaffingDiscoveryBudget
	if shouldBlockProjectBootstrapExcessStaffingDiscoveryTool(rt, "staffing.browse_profiles") {
		t.Fatal("unexpected staffing browse block once assignments already exist")
	}
}

func TestBuildProjectBootstrapExcessStaffingDiscoveryGuardError(t *testing.T) {
	msg := buildProjectBootstrapExcessStaffingDiscoveryGuardError()
	if !strings.Contains(msg, "stop browsing profiles and create/assign the concrete PM, workers, and reviewers now") {
		t.Fatalf("guard error = %q, want direct staffing action guidance", msg)
	}
}

func TestShouldBlockProjectBootstrapRestaffingToolAllowsMissingPMRecovery(t *testing.T) {
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                 projectBootstrapStatusActive,
		AssignmentCount:        2,
		CurrentPhase:           projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:       projectBootstrapValidationFailed,
		ValidationFailureClass: projectBootstrapFailureMissingPM,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue bootstrap. Named blocked task: task 12 id=1234 title=\"Content Creation\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if shouldBlockProjectBootstrapRestaffingTool(rt, "agent.create_staff") {
		t.Fatal("missing-PM recovery should allow agent.create_staff")
	}
	if shouldBlockProjectBootstrapRestaffingTool(rt, "staffing.browse_profiles") {
		t.Fatal("missing-PM recovery should allow staffing.browse_profiles")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksBroadRereads(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            1,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason:  "kickoff validation failed: first-wave tasks violate the bounded task-size policy",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.list", nil) {
		t.Fatal("expected project.list to be blocked during late bootstrap validation recovery")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.get", nil) {
		t.Fatal("expected project.get to be blocked during late bootstrap validation recovery")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected first flow.list_templates pass to remain available before the turn burns context budget")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected first task.list pass to remain available for targeted recovery lookup")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for targeted recovery inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksLateCompactResumeRereads(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		BootstrapTaskOutstanding: true,
		BootstrapTaskID:          uuid.New().String(),
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "project.list", nil) {
		t.Fatal("expected project.list to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked during late compact bootstrap resume")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/spec.md"}) {
		t.Fatal("expected planning file.read to be blocked during late compact bootstrap resume")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for a single specific late-phase blocker inspection")
	}
}

func TestBuildProjectBootstrapRecoveryRereadToolGuardErrorLateCompactResume(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		BootstrapTaskOutstanding: true,
		BootstrapTaskID:          uuid.New().String(),
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationPassed,
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	msg := buildProjectBootstrapRecoveryRereadToolGuardError(rt, "task.list")
	if !strings.Contains(msg, "call bootstrap.setup.persist now") {
		t.Fatalf("guard error = %q, want bootstrap.setup.persist guidance", msg)
	}
}

func TestBuildProjectBootstrapRecoveryRereadToolGuardErrorScaffoldOnlyRecovery(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         0,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointStaffingPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureRuntime,
		ValidationFailureReason:  buildProjectBootstrapScaffoldOnlyFailureReason(),
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	msg := buildProjectBootstrapRecoveryRereadToolGuardError(rt, "project.list")
	if !strings.Contains(msg, "direct task.create or subtask.create calls") {
		t.Fatalf("guard error = %q, want direct task creation guidance", msg)
	}
	if !strings.Contains(msg, "Do not reread project state first") {
		t.Fatalf("guard error = %q, want no-reread guidance", msg)
	}
}

func TestShouldRequireDirectBootstrapRepairActionForBoundedSizeFailure(t *testing.T) {
	state := projectBootstrapState{
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 105 (SEO, Performance, and Analytics Integration) violates the bounded task-size policy",
	}
	snapshot := projectBootstrapResumeSnapshot{
		FailedTaskLine: `Named blocked task: task 105 id=1234 title="SEO, Performance, and Analytics Integration" work_status=draft assigned_agent_id=06ff.`,
	}

	if !shouldRequireDirectBootstrapRepairAction(state, snapshot) {
		t.Fatal("expected bounded-size failures with a named task to require direct repair action")
	}
}

func TestBuildProjectBootstrapResumeStateMessageRequiresDirectBoundedSplit(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 105 (SEO, Performance, and Analytics Integration) violates the bounded task-size policy",
		AssignmentCount:          4,
		PlannedTaskCount:         35,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       30,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      "028a3f39-be56-45bc-b9f8-5978ab9cf28f",
		ProjectSlug:    "sam-blog-110-restart-9",
		FailedTaskLine: `Named blocked task: task 105 id=1234 title="SEO, Performance, and Analytics Integration" work_status=draft assigned_agent_id=06ff.`,
		RepairTaskLine: "Direct repair for the named oversized task: keep task 105 orchestration-only, do not call task.get on task id=1234 first, create 2-4 bounded executable child tasks directly beneath task id=1234, keep each child to a single concrete deliverable under 60 minutes, and assign each child to an existing active project assignee before resuming bootstrap.setup.persist.",
	}

	msg := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if !strings.Contains(msg, "next acceptable bootstrap action is direct bounded child-task creation") {
		t.Fatalf("resume state message = %q, want direct bounded child-task creation guidance", msg)
	}
	if !strings.Contains(msg, "Direct repair for the named oversized task") {
		t.Fatalf("resume state message = %q, want explicit oversized-task repair line", msg)
	}
	if !strings.Contains(msg, "do not call task.get on task id=1234 first") {
		t.Fatalf("resume state message = %q, want direct no-task.get oversized-task guidance", msg)
	}
}

func TestRequireTurnInProgressReturnsCancelledSentinel(t *testing.T) {
	turnID := uuid.New()
	engine := &TurnEngine{
		chat: &fakeChatService{
			turns: map[uuid.UUID]*chat.ChatTurn{
				turnID: {
					ID:     turnID,
					Status: "cancelled",
				},
			},
		},
	}
	rt := &turnRuntime{
		turn: &chat.ChatTurn{ID: turnID},
	}

	err := engine.requireTurnInProgress(context.Background(), rt)
	if !errors.Is(err, errTurnCancelled) {
		t.Fatalf("requireTurnInProgress error = %v, want errTurnCancelled", err)
	}
}

func TestRequireTurnInProgressReturnsCancelledForOtherTerminalStatuses(t *testing.T) {
	for _, status := range []string{"failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			turnID := uuid.New()
			engine := &TurnEngine{
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				chat: &fakeChatService{
					turns: map[uuid.UUID]*chat.ChatTurn{
						turnID: {
							ID:     turnID,
							Status: status,
						},
					},
				},
			}
			rt := &turnRuntime{
				session: &chat.ChatSession{ID: uuid.New()},
				turn:    &chat.ChatTurn{ID: turnID},
			}

			err := engine.requireTurnInProgress(context.Background(), rt)
			if !errors.Is(err, errTurnCancelled) {
				t.Fatalf("requireTurnInProgress error = %v, want errTurnCancelled", err)
			}
		})
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksNamedTaskListImmediately(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            1,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue bootstrap. Named blocked task: task 12 id=1234 title=\"Content Creation\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("expected task.get to be blocked when recovery already carries the exact task id and direct task.update instructions")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected flow.list_templates to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked immediately when recovery already names the exact failing task")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-73.md"}) {
		t.Fatal("expected planning file.read to be blocked immediately when recovery already names the exact failing task")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "src/components/PostCard.tsx"}) {
		t.Fatal("non-planning file.read should remain available for targeted implementation inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksNamedTaskListFromRecoveryMessage(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            3,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent.",
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected task.list to be blocked immediately when the recovery message already names the exact failing task")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.get", nil) {
		t.Fatal("task.get should remain available for targeted recovery inspection")
	}
}

func TestShouldBlockProjectBootstrapRecoveryRereadToolBlocksRepeatedTaskList(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AutoTurnCount:            2,
		AssignmentCount:          9,
		PlannedTaskCount:         47,
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 51 has not materialized execution",
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		toolCallsUsed: 1,
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "task.list", nil) {
		t.Fatal("expected repeated task.list to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "flow.list_templates", nil) {
		t.Fatal("expected late flow.list_templates reread to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.search", nil) {
		t.Fatal("expected file.search to be blocked after recovery already spent tool budget in this turn")
	}
	if !shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-73.md"}) {
		t.Fatal("expected scaffold planning file.read to be blocked after recovery already spent tool budget in this turn")
	}
	if shouldBlockProjectBootstrapRecoveryRereadTool(rt, "file.read", map[string]any{"path": "src/components/PostCard.tsx"}) {
		t.Fatal("non-planning file.read should remain available for targeted code inspection")
	}
}

func TestShouldStopAfterBlockedProjectBootstrapRecoveryReread(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project",
		},
	}

	if !shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("expected blocked late bootstrap reread to stop the current turn")
	}
	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, false) {
		t.Fatal("unexpected stop without a blocked reread")
	}

	rt.session.ScopeType = "organization"
	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("unexpected stop outside project scope")
	}
}

func TestShouldBlockTaskRecoveryStatusPathTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
		},
	}

	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md"}) {
		t.Fatal("expected task recovery-state read to be blocked")
	}
	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.write", map[string]any{"path": ".ottercamp/recovery/docs/migration-plan/oc-15.md"}) {
		t.Fatal("expected task recovery artifact write to be blocked")
	}
	if !shouldBlockTaskRecoveryStatusPathTool(rt, "file.list", map[string]any{"path": "planning/checkpoint"}) {
		t.Fatal("expected task checkpoint listing to be blocked")
	}
	if shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/oc-24-infrastructure-spec.md"}) {
		t.Fatal("deliverable file read should remain available")
	}

	rt.session.Mode = "sync"
	if shouldBlockTaskRecoveryStatusPathTool(rt, "file.read", map[string]any{"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md"}) {
		t.Fatal("expected sync task session to bypass async recovery-state guard")
	}
}

func TestShouldBlockTaskStatusMessageTool(t *testing.T) {
	rt := &turnRuntime{
		session: &chat.ChatSession{
			ScopeType: "project_task",
			Mode:      "async",
			ID:        uuid.New(),
		},
	}

	if !shouldBlockTaskStatusMessageTool(rt, "message.send", map[string]any{"content": "Notifying the team that the task is complete."}) {
		t.Fatal("expected task status message without target session to be blocked")
	}
	if shouldBlockTaskStatusMessageTool(rt, "message.send", map[string]any{"session_id": uuid.NewString(), "content": "handoff"}) {
		t.Fatal("explicit target session should remain available")
	}
	if shouldBlockTaskStatusMessageTool(rt, "file.read", map[string]any{"path": "planning/prd-spec/spec.md"}) {
		t.Fatal("non-message tool should not trigger status-message guard")
	}
}

func TestShouldNotStopAfterBlockedProjectBootstrapRecoveryRereadScaffoldOnlyRecovery(t *testing.T) {
	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		AssignmentCount:          3,
		PlannedTaskCount:         0,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointStaffingPersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureClass:   projectBootstrapFailureRuntime,
		ValidationFailureReason:  buildProjectBootstrapScaffoldOnlyFailureReason(),
		StartedAt:                &now,
		UpdatedAt:                &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	rt := &turnRuntime{
		initialMessageText: buildProjectBootstrapScaffoldOnlyFailureReason(),
		session: &chat.ChatSession{
			ScopeType: "project",
			Metadata:  metadata,
		},
	}

	if shouldStopAfterBlockedProjectBootstrapRecoveryReread(rt, true) {
		t.Fatal("expected scaffold-only recovery reread block to continue the current turn")
	}
}

func TestIsTransientInfrastructureError(t *testing.T) {
	if !isTransientInfrastructureError(errors.New("failed to connect to database: FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute (SQLSTATE 53300)")) {
		t.Fatal("expected SQLSTATE 53300 connection exhaustion to classify as transient infrastructure error")
	}
	if !isTransientInfrastructureError(errors.New("pq: sorry, too many clients already")) {
		t.Fatal("expected too many clients error to classify as transient infrastructure error")
	}
	if isTransientInfrastructureError(errors.New("provider auth failed")) {
		t.Fatal("unexpected infrastructure classification for unrelated error")
	}
}

func TestIsRecoverableExecutionContinuationDepthError(t *testing.T) {
	if !isRecoverableExecutionContinuationDepthError(errContextCompressionContinuationDepthExceeded) {
		t.Fatal("context compression continuation depth sentinel should be recoverable")
	}
	if !isRecoverableExecutionContinuationDepthError(errAgentTurnPromptGuardrailDepthExceeded) {
		t.Fatal("prompt guardrail continuation depth sentinel should be recoverable")
	}
	if isRecoverableExecutionContinuationDepthError(errors.New("agent turn prompt exceeded guardrail continuation depth")) {
		t.Fatal("plain string-matched errors should not be treated as recoverable continuation depth failures")
	}
	if isRecoverableExecutionContinuationDepthError(nil) {
		t.Fatal("nil error should not be treated as recoverable continuation depth failure")
	}
}

func TestIsRecoverableProjectExecutionFailure(t *testing.T) {
	if !isRecoverableProjectExecutionFailure(ErrModelTransient) {
		t.Fatal("transient model failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(ErrRateLimited) {
		t.Fatal("rate limit failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(errContextCompressionContinuationDepthExceeded) {
		t.Fatal("continuation depth failures should be recoverable for project execution")
	}
	if !isRecoverableProjectExecutionFailure(errors.New("pq: sorry, too many clients already")) {
		t.Fatal("transient infrastructure failures should be recoverable for project execution")
	}
	if isRecoverableProjectExecutionFailure(errors.New("provider auth failed")) {
		t.Fatal("provider auth failures should not be treated as recoverable project execution failures")
	}
	if isRecoverableProjectExecutionFailure(nil) {
		t.Fatal("nil should not be treated as recoverable project execution failure")
	}
}

func TestHandleUserMessageProjectScopeKickoffHandoffRoutesToLoriAfterFrank(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	completedAt := time.Now().UTC()
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "completed",
		StartedAt:      &completedAt,
		CompletedAt:    &completedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != loriID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, loriID)
	}

	participants, err := fixture.chat.ListParticipants(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	hasLori := false
	for _, participant := range participants {
		if participant != nil && strings.EqualFold(strings.TrimSpace(participant.ParticipantType), "agent") && participant.ParticipantID == loriID {
			hasLori = true
			break
		}
	}
	if !hasLori {
		t.Fatalf("expected Lori %s to be added as a session participant", loriID)
	}
}

func TestHandleUserMessageProjectScopeKickoffDoesNotHandoffOnInProgressFrankTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	startedAt := time.Now().UTC()
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "in_progress",
		StartedAt:      &startedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageProjectScopeKickoffDoesNotHandoffOnFailedFrankTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	frankID := uuid.New()
	loriID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   frankID,
	}}
	fixture.engine.assignments = &fakeAssignmentRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			frankID: {ID: frankID, OrganizationID: fixture.session.OrganizationID},
			loriID:  {ID: loriID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: loriID, DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	startedAt := time.Now().UTC()
	completedAt := startedAt.Add(1 * time.Second)
	seededTurnID := uuid.New()
	fixture.chat.turns[seededTurnID] = &chat.ChatTurn{
		ID:             seededTurnID,
		SessionID:      fixture.session.ID,
		TurnNumber:     1,
		RespondingType: "agent",
		RespondingID:   frankID,
		Status:         "failed",
		StartedAt:      &startedAt,
		CompletedAt:    &completedAt,
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, seededTurnID)
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	turnID := fixture.chat.waitForTurnID(t)
	turn := fixture.chat.turnByID(turnID)
	if turn == nil {
		t.Fatal("expected created turn")
	}
	if turn.RespondingID != frankID {
		t.Fatalf("turn responding_id = %s, want %s", turn.RespondingID, frankID)
	}
}

func TestHandleUserMessageTaskScopeMissingTaskBindingReturnsInvariant(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	otherParticipantID := uuid.New()
	frankID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.participants = []*chat.ChatParticipant{{
		ID:              uuid.New(),
		SessionID:       fixture.session.ID,
		ParticipantType: "agent",
		ParticipantID:   otherParticipantID,
	}}
	fixture.engine.tasks = &fakeTaskRepo{err: repo.ErrNotFound}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			otherParticipantID: {ID: otherParticipantID, OrganizationID: fixture.session.OrganizationID},
			frankID:            {ID: frankID, OrganizationID: fixture.session.OrganizationID},
		},
		starter: []repo.Agent{
			{ID: uuid.New(), DisplayName: "Lori", AgentType: "pm"},
			{ID: frankID, DisplayName: "Frank", AgentType: "general"},
		},
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok"}, nil
	}

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil {
		t.Fatal("HandleUserMessage error = nil, want missing task binding invariant")
	}
	if !strings.Contains(err.Error(), "internal invariant: task-scoped session task binding was not found") {
		t.Fatalf("HandleUserMessage error = %v, want missing task binding invariant", err)
	}
	if fixture.model.streamCalls != 0 {
		t.Fatalf("stream calls = %d, want 0", fixture.model.streamCalls)
	}
}

func TestDispatchToolsTaskScopeInjectsBoundProjectAndTaskIDs(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
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
					ID:        "tier2",
					Name:      "cli.execute",
					Tier:      "tier2",
					Arguments: map[string]any{"command": "echo hello"},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["task_id"]; got != taskID.String() {
		t.Fatalf("task_id = %v, want %s", got, taskID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
}

func TestDispatchToolsProjectScopeOverridesStaleBoundIDs(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	staleProjectID := uuid.New()
	staleSessionID := uuid.New()
	staleTaskID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "task.list",
					Arguments: map[string]any{
						"project_id": staleProjectID.String(),
						"session_id": staleSessionID.String(),
						"task_id":    staleTaskID.String(),
						"agent_id":   uuid.New().String(),
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
	if got := dispatched.Arguments["agent_id"]; got != fixture.chat.participants[0].ParticipantID.String() {
		t.Fatalf("agent_id = %v, want %s", got, fixture.chat.participants[0].ParticipantID)
	}
	if _, exists := dispatched.Arguments["task_id"]; exists {
		t.Fatalf("task_id = %v, want stale task binding cleared for project-scoped dispatch", dispatched.Arguments["task_id"])
	}
}

func TestDispatchToolsMessageSendPreservesExplicitTargetSessionID(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetSessionID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "message.send",
					Arguments: map[string]any{
						"session_id": targetSessionID.String(),
						"role":       "user",
						"content":    "handoff",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["session_id"]; got != targetSessionID.String() {
		t.Fatalf("session_id = %v, want explicit target %s", got, targetSessionID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["agent_id"]; got != fixture.chat.participants[0].ParticipantID.String() {
		t.Fatalf("agent_id = %v, want %s", got, fixture.chat.participants[0].ParticipantID)
	}
}

func TestDispatchToolsBlocksTaskMessageSendWithoutSessionID(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}

	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		t.Fatalf("unexpected dispatched tool call: %s", call.Name)
		return ToolResult{}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "notify-team",
					Name: "message.send",
					Arguments: map[string]any{
						"role":    "user",
						"content": "Notify the team this task is complete.",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var blocked bool
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "notify-team" &&
			strings.Contains(message.Content, "status or notification messages without an explicit destination session") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("missing blocked message.send tool_result for task status-message guard")
	}
}

func TestDispatchToolsBlocksTaskRecoveryStateRead(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}

	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		t.Fatalf("unexpected dispatched tool call: %s", call.Name)
		return ToolResult{}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "read-recovery",
					Name: "file.read",
					Arguments: map[string]any{
						"path": "planning/recovery-state/OC-20-WORK-COMPLETE-SUMMARY.md",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var blocked bool
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool_result") &&
			message.ToolCallID != nil &&
			*message.ToolCallID == "read-recovery" &&
			strings.Contains(message.Content, "should not reread recovery-state or checkpoint files") {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("missing blocked recovery-state tool_result for async task session")
	}
}

func TestDispatchToolsAssignProjectPreservesExplicitTargetAgentID(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetAgentID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "agent.assign_project",
					Arguments: map[string]any{
						"agent_id": targetAgentID.String(),
						"role":     "worker",
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["agent_id"]; got != targetAgentID.String() {
		t.Fatalf("agent_id = %v, want explicit target %s", got, targetAgentID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
	}
	if got := dispatched.Arguments["session_id"]; got != fixture.session.ID.String() {
		t.Fatalf("session_id = %v, want %s", got, fixture.session.ID)
	}
}

func TestDispatchToolsTaskGetPreservesExplicitTargetTaskIDInProjectScope(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	projectID := uuid.New()
	targetTaskID := uuid.New()

	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID

	var dispatched ToolCall
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched = call
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	round := 0
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(string) error) (ModelResponse, error) {
		round++
		if round == 1 {
			return ModelResponse{
				ToolCalls: []ModelToolCall{{
					ID:   "tier1",
					Name: "task.get",
					Arguments: map[string]any{
						"task_id": targetTaskID.String(),
					},
				}},
			}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if got := dispatched.Arguments["task_id"]; got != targetTaskID.String() {
		t.Fatalf("task_id = %v, want explicit target %s", got, targetTaskID)
	}
	if got := dispatched.Arguments["project_id"]; got != projectID.String() {
		t.Fatalf("project_id = %v, want %s", got, projectID)
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

func TestMaxToolCallsSyncStops(t *testing.T) {
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
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 1 {
		t.Fatalf("turn count = %d, want 1", len(fixture.chat.turnOrder))
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil || firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxToolCallsAsyncContinuation(t *testing.T) {
	fixture := newUnitFixture(t, "async")
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
				{ID: "c", Name: "chat.search", Tier: "tier1"},
				{ID: "d", Name: "task.list", Tier: "tier1"},
			}}, nil
		case 2:
			return ModelResponse{ToolCalls: []ModelToolCall{
				{ID: "e", Name: "chat.search", Tier: "tier1"},
				{ID: "f", Name: "task.list", Tier: "tier1"},
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
	if len(got) != 5 {
		t.Fatalf("tier1 dispatches = %v, want 5 total tool calls across continuation", got)
	}
	if strings.Join(got, ",") != "a,b,c,e,f" {
		t.Fatalf("tier1 dispatch calls = %v, want [a b c e f]", got)
	}
	if fixture.model.continuationSummaryCalls != 1 {
		t.Fatalf("continuation summary calls = %d, want 1", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 2 {
		t.Fatalf("turn count = %d, want 2", len(fixture.chat.turnOrder))
	}
	if !fixture.messages.containsContent("[Max tool calls reached. Turn ended.]") {
		t.Fatal("missing max tool calls stop system message")
	}
	if !fixture.messages.containsContent("[Continuation summary] respond") {
		t.Fatal("missing continuation summary message")
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil || firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
}

func TestMaxToolCallsAsyncContinuationRecoversLeakedInProgressTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.maxToolCalls = 3
	fixture.chat.completeNoop = true

	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
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
	if len(fixture.chat.turnOrder) == 0 {
		t.Fatal("expected at least one turn")
	}
	firstTurn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if firstTurn == nil {
		t.Fatal("missing first turn")
	}
	if firstTurn.Status != "failed" {
		t.Fatalf("first turn status = %q, want failed after leak recovery", firstTurn.Status)
	}
	if firstTurn.StopReason == nil || *firstTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("first turn stop_reason = %v, want %q", firstTurn.StopReason, stopReasonMaxToolCalls)
	}
	if fixture.chat.failCalls < 1 {
		t.Fatalf("fail calls = %d, want >= 1", fixture.chat.failCalls)
	}
	if !fixture.messages.containsContent("[Recovered leaked in-progress turn after max-tool-calls handoff - allowing queued continuation to proceed.]") {
		t.Fatal("missing leaked turn recovery system message")
	}
	for _, turnID := range fixture.chat.turnOrder {
		turn := fixture.chat.turnByID(turnID)
		if turn == nil {
			continue
		}
		if turn.Status == "in_progress" {
			t.Fatalf("turn %s still in_progress after leak recovery", turn.ID)
		}
	}
	for _, job := range fixture.enqueuer.agentTurnJobs() {
		if job.payload == nil {
			t.Fatal("expected agent_turn payload")
		}
		if job.payload.MessageID == fixture.userMessageID {
			t.Fatal("expected continuation payload to target a follow-on message, not the original user message")
		}
	}
}

func TestProjectBootstrapRecoverableMaxToolCallFailure(t *testing.T) {
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass: projectBootstrapFailureCompoundParent,
	}) {
		t.Fatal("compound parent failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass: projectBootstrapFailureFirstWaveSize,
	}) {
		t.Fatal("first-wave size failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "task exceeds bounded size policy (estimated 35 minutes > 30 minute limit): split the work",
	}) {
		t.Fatal("bounded-size first-wave execution failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 39 (Write photography + brand post titles) has no assigned agent, so bootstrap cannot queue runnable execution",
	}) {
		t.Fatal("unassigned first-wave execution failure should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	}) {
		t.Fatal("partial first-wave execution materialization should be recoverable")
	}
	if !projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureRuntime,
		ValidationFailureReason: buildProjectBootstrapRestartScaffoldFailureReason(),
	}) {
		t.Fatal("restart scaffold runtime failure should be recoverable")
	}
	if projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: bootstrap setup persisted staffing but did not emit any executable non-bootstrap project tasks for the first wave",
	}) {
		t.Fatal("non-bounded first-wave execution failure should not be recoverable")
	}
}

func TestProjectBootstrapProgressAdvancedBeyondState(t *testing.T) {
	state := projectBootstrapState{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   17,
		FirstWaveExecutionCount:  17,
		FirstWaveJobCount:        16,
	}
	progress := projectBootstrapProgress{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   18,
		FirstWaveExecutionCount:  18,
		FirstWaveJobCount:        17,
	}
	if !projectBootstrapProgressAdvancedBeyondState(state, progress) {
		t.Fatal("later first-wave counts should reset recoverable continuation budget")
	}
	if projectBootstrapProgressAdvancedBeyondState(state, projectBootstrapProgress{
		AssignmentCount:          4,
		PlannedTaskCount:         20,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       18,
		FirstWavePromotedCount:   17,
		FirstWaveExecutionCount:  17,
		FirstWaveJobCount:        16,
	}) {
		t.Fatal("unchanged progress should not reset recoverable continuation budget")
	}
}

func TestBuildProjectBootstrapValidationRecoveryPrompt(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 12 (WS4: Blog Post Ideation) violates the bounded task-size policy",
	})
	if !strings.Contains(prompt, "automatic follow-on bootstrap turn 2") {
		t.Fatalf("prompt = %q, want turn count", prompt)
	}
	if !strings.Contains(prompt, "WS4: Blog Post Ideation") {
		t.Fatalf("prompt = %q, want validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Do not repeat the same oversized task definitions") {
		t.Fatalf("prompt = %q, want anti-repeat guidance", prompt)
	}
	if !strings.Contains(prompt, "splitting the offending broad parent or first-wave task into narrower executable child tasks") {
		t.Fatalf("prompt = %q, want bounded child-splitting guidance", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action should be a tool call, not a narrative reply") {
		t.Fatalf("prompt = %q, want no-narrative bounded repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get on the named oversized task first") {
		t.Fatalf("prompt = %q, want direct no-task.get bounded repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not return to bootstrap.setup.persist until that structural repair is complete") {
		t.Fatalf("prompt = %q, want setup persist deferral guidance", prompt)
	}
	if !strings.Contains(prompt, "Every task.create or subtask.create call must include a concrete non-empty title") {
		t.Fatalf("prompt = %q, want title guidance", prompt)
	}
	if !strings.Contains(prompt, "The bootstrap governance gate task is system-managed") {
		t.Fatalf("prompt = %q, want governance gate guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	})
	if !strings.Contains(prompt, "has no assigned agent") {
		t.Fatalf("prompt = %q, want validation reason detail", prompt)
	}
	if !strings.Contains(prompt, "Do not call bootstrap.setup.persist until every selected first-wave task has an assigned active project agent") {
		t.Fatalf("prompt = %q, want first-wave assignment guidance", prompt)
	}
	if !strings.Contains(prompt, "Repair the named persisted first-wave task directly") {
		t.Fatalf("prompt = %q, want direct task repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.get, task.list, task.children, flow.list_templates, agent.list") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get with the bare task number from the validation error") {
		t.Fatalf("prompt = %q, want no bare task.get guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get on the named blocked task first") {
		t.Fatalf("prompt = %q, want direct no task.get guidance for named blocked task", prompt)
	}
	if !strings.Contains(prompt, "Your next assistant action should be a tool call, not a narrative reply") {
		t.Fatalf("prompt = %q, want direct tool-call guidance for named blocked task", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTaskSkipsFlowRereadAfterRepair(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:   projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
		PlannedFlowTemplateCount: 2,
		BootstrapSetupDoneCount:  5,
		BootstrapTaskOutstanding: true,
	})
	if !strings.Contains(prompt, "do not go back to flow.list_templates") {
		t.Fatalf("prompt = %q, want no flow reread guidance after direct repair", prompt)
	}
	if !strings.Contains(prompt, "return straight to bootstrap.setup.persist with canonical step slugs") {
		t.Fatalf("prompt = %q, want persist-after-repair guidance", prompt)
	}
	if !strings.Contains(prompt, "attach-validate-flow-templates, select-first-wave, and record-frank-sign-off") {
		t.Fatalf("prompt = %q, want canonical remaining-step guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedFirstWaveTaskSkipsProjectRereadAfterAssignmentFix(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(3, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	})
	if !strings.Contains(prompt, "After you fix the assignment, do not call project.list, project.get, task.list, or flow.list_templates") {
		t.Fatalf("prompt = %q, want no broad reread guidance after assignment fix", prompt)
	}
	if !strings.Contains(prompt, "If that same named task still looks broad, keep it orchestration-only and split it directly") {
		t.Fatalf("prompt = %q, want direct split guidance after assignment fix", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForUnassignedWaveParent(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 11 (Wave 3: Polish, Deploy & Content) has no assigned agent",
	})
	if !strings.Contains(prompt, "keep the parent orchestration-only and immediately create bounded executable child tasks beneath it") {
		t.Fatalf("prompt = %q, want parent-splitting guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call file.read on planning artifacts just to decide the child split") {
		t.Fatalf("prompt = %q, want no planning-artifact reread guidance", prompt)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForPartialFirstWaveMaterialization(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	})
	if !strings.Contains(prompt, "Shrink the selected first wave to a smaller bounded subset of the already-created child tasks") {
		t.Fatalf("prompt = %q, want first-wave narrowing guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not rewrite planning artifacts or regenerate the broader scaffold") {
		t.Fatalf("prompt = %q, want no planning rewrite guidance", prompt)
	}
	if !strings.Contains(prompt, "repair the selected runnable subset with direct task and flow mutations only") {
		t.Fatalf("prompt = %q, want direct repair guidance", prompt)
	}
}

func TestBuildProjectBootstrapAdditionalRepairTaskLineListsOtherUnassignedFirstWaveTasks(t *testing.T) {
	progress := projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 23 (Build base layout with header and footer) has no assigned agent",
		FirstWaveTasks: []repo.ProjectTask{
			{ID: uuid.MustParse("268af254-cc71-487c-9852-8841a0e84238"), TaskNumber: 23, Title: "Build base layout with header and footer"},
			{ID: uuid.MustParse("04d0f869-dfe2-4e87-b52d-0f82b6b8ea5f"), TaskNumber: 22, Title: "Configure Tailwind and design tokens"},
			{ID: uuid.MustParse("c0fbed7a-d298-4efc-84ba-6102f7d20766"), TaskNumber: 31, Title: "Responsive styling for CTA banner section"},
		},
	}
	line := buildProjectBootstrapAdditionalRepairTaskLine(progress)
	if !strings.Contains(line, "task 22 id=04d0f869-dfe2-4e87-b52d-0f82b6b8ea5f") {
		t.Fatalf("line = %q, want remaining unassigned first-wave task", line)
	}
	if !strings.Contains(line, "task 31 id=c0fbed7a-d298-4efc-84ba-6102f7d20766") {
		t.Fatalf("line = %q, want second unassigned first-wave task", line)
	}
	if strings.Contains(line, "task 23 id=268af254-cc71-487c-9852-8841a0e84238") {
		t.Fatalf("line = %q, should not repeat the named blocked task", line)
	}
}

func TestBuildProjectBootstrapAdditionalRepairTaskLineListsFullRemainingRoster(t *testing.T) {
	progress := projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 50 (Blocked task) has no assigned agent",
		FirstWaveTasks: []repo.ProjectTask{
			{ID: uuid.MustParse("6d95d68c-87cc-47f2-a4f6-d74695d0c2f3"), TaskNumber: 50, Title: "Blocked task"},
			{ID: uuid.MustParse("7ec50721-96a7-4f2d-a2f1-a8ac578a8941"), TaskNumber: 49, Title: "Task A"},
			{ID: uuid.MustParse("f5a10df9-7f3e-4fd4-8ee0-a4a45fd936db"), TaskNumber: 48, Title: "Task B"},
			{ID: uuid.MustParse("5c74f97c-c856-42e2-904c-bc85eb03a9c3"), TaskNumber: 47, Title: "Task C"},
			{ID: uuid.MustParse("5c0852ca-1503-4bff-a837-bfdbd4429f02"), TaskNumber: 46, Title: "Task D"},
			{ID: uuid.MustParse("7bb2a8a4-e34e-47fa-b4d5-2ad85da9ad34"), TaskNumber: 45, Title: "Task E"},
		},
	}

	line := buildProjectBootstrapAdditionalRepairTaskLine(progress)
	for _, snippet := range []string{
		`task 49 id=7ec50721-96a7-4f2d-a2f1-a8ac578a8941 title="Task A"`,
		`task 48 id=f5a10df9-7f3e-4fd4-8ee0-a4a45fd936db title="Task B"`,
		`task 47 id=5c74f97c-c856-42e2-904c-bc85eb03a9c3 title="Task C"`,
		`task 46 id=5c0852ca-1503-4bff-a837-bfdbd4429f02 title="Task D"`,
		`task 45 id=7bb2a8a4-e34e-47fa-b4d5-2ad85da9ad34 title="Task E"`,
	} {
		if !strings.Contains(line, snippet) {
			t.Fatalf("line = %q, want %q", line, snippet)
		}
	}
	if strings.Contains(line, "plus ") {
		t.Fatalf("line = %q, should not truncate remaining repair targets", line)
	}
}

func TestBuildProjectBootstrapValidationRecoveryPromptForRestartScaffoldFailure(t *testing.T) {
	prompt := buildProjectBootstrapValidationRecoveryPrompt(2, projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureRuntime,
		ValidationFailureReason: buildProjectBootstrapRestartScaffoldFailureReason(),
	})
	if !strings.Contains(prompt, "did not materialize staffed executable project work") {
		t.Fatalf("prompt = %q, want scaffold recovery guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not begin with project.get, task.list, flow.list_templates, file.list, git.log") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Reuse the dedicated project staff already created in this session") {
		t.Fatalf("prompt = %q, want direct staffing reuse guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not answer with a standalone acknowledgement or status note") {
		t.Fatalf("prompt = %q, want no-acknowledgement guidance", prompt)
	}
}

func TestEnsureTurnRunExitInvariantRejectsLeakedInProgressTurn(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, fixture.chat.participants[0].ParticipantID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}

	rt := &turnRuntime{session: fixture.session, turn: turn}
	if err := fixture.engine.ensureTurnRunExitInvariant(context.Background(), rt); err == nil {
		t.Fatal("expected leaked in-progress turn invariant error")
	}

	if err := fixture.chat.CompleteTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := fixture.engine.ensureTurnRunExitInvariant(context.Background(), rt); err != nil {
		t.Fatalf("ensureTurnRunExitInvariant completed turn: %v", err)
	}
}

func TestProjectBootstrapHasPriorMaxToolCallsContinuation(t *testing.T) {
	cycleID := uuid.New()
	previousStop := stopReasonMaxToolCalls
	current := chat.ChatTurn{
		ID:         uuid.New(),
		TurnNumber: 2,
		CycleID:    &cycleID,
	}
	turns := []repo.ChatTurn{
		{
			ID:         uuid.New(),
			TurnNumber: 1,
			CycleID:    &cycleID,
			Status:     "completed",
			StopReason: &previousStop,
		},
		{
			ID:         current.ID,
			TurnNumber: current.TurnNumber,
			CycleID:    current.CycleID,
			Status:     "pending",
		},
	}
	if !projectBootstrapHasPriorMaxToolCallsContinuation(turns, current) {
		t.Fatal("expected prior max-tool-calls continuation to be detected")
	}

	otherCycle := uuid.New()
	current.CycleID = &otherCycle
	if projectBootstrapHasPriorMaxToolCallsContinuation(turns, current) {
		t.Fatal("did not expect continuation detection across different cycles")
	}
}

func TestProjectBootstrapHasNewerLiveContinuationTurn(t *testing.T) {
	cycleID := uuid.New()
	completedStop := stopReasonMaxToolCalls
	completed := repo.ChatTurn{
		ID:         uuid.New(),
		TurnNumber: 1,
		CycleID:    &cycleID,
		Status:     "completed",
		StopReason: &completedStop,
	}
	turns := []repo.ChatTurn{
		completed,
		{
			ID:         uuid.New(),
			TurnNumber: 2,
			CycleID:    &cycleID,
			Status:     "in_progress",
		},
	}
	if !projectBootstrapHasNewerLiveContinuationTurn(turns, completed) {
		t.Fatal("expected newer live continuation turn")
	}

	turns[1].Status = "failed"
	if projectBootstrapHasNewerLiveContinuationTurn(turns, completed) {
		t.Fatal("did not expect failed turn to count as live continuation")
	}
}

func TestMaxToolCallsContinuationDepthLimit(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.engine.maxToolCalls = 1

	var dispatched int
	fixture.dispatcher.tier1Fn = func(_ context.Context, call ToolCall) (ToolResult, error) {
		dispatched++
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Output: map[string]any{"ok": true}}, nil
	}
	fixture.model.streamFn = func(_ context.Context, _ ModelRequest, _ func(token string) error) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ModelToolCall{
			{ID: uuid.NewString(), Name: "file.read", Tier: "tier1"},
			{ID: uuid.NewString(), Name: "task.list", Tier: "tier1"},
		}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 3 {
		t.Fatalf("continuation summary calls = %d, want 3", fixture.model.continuationSummaryCalls)
	}
	if len(fixture.chat.turnOrder) != 4 {
		t.Fatalf("turn count = %d, want 4 (original + 3 continuations)", len(fixture.chat.turnOrder))
	}
	if dispatched != 4 {
		t.Fatalf("dispatched tool calls = %d, want 4", dispatched)
	}
	lastTurn := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if lastTurn == nil || lastTurn.StopReason == nil || *lastTurn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("last turn stop_reason = %v, want %q", lastTurn.StopReason, stopReasonMaxToolCalls)
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
	turn := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if turn == nil || turn.StopReason == nil || *turn.StopReason != stopReasonMaxToolCalls {
		t.Fatalf("turn stop_reason = %v, want %q", turn.StopReason, stopReasonMaxToolCalls)
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

func TestHandleTurnCancelledEventEnqueuesBootstrapRecovery(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.CurrentTurnID = nil

	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:           projectBootstrapStatusActive,
		InitialMessageID: fixture.userMessageID.String(),
		StartedAt:        &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata

	cancelledTurnID := uuid.New()
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:           cancelledTurnID,
		SessionID:    fixture.session.ID,
		TurnNumber:   2,
		RespondingID: uuid.New(),
		Status:       "cancelled",
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)

	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"turn_id":    cancelledTurnID.String(),
	})
	if err != nil {
		t.Fatalf("marshal cancel payload: %v", err)
	}
	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "system",
		Payload:        payload,
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleTurnCancelledEvent: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("bootstrap recovery jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil || jobs[0].payload.SessionID != fixture.session.ID {
		t.Fatalf("recovery payload = %+v, want session %s", jobs[0].payload, fixture.session.ID)
	}
	if jobs[0].payload.MessageID == fixture.userMessageID {
		t.Fatal("expected recovery to target a fresh bootstrap continuation message")
	}
	if !fixture.messages.containsContent("[Recovered cancelled bootstrap turn - retrying in a fresh turn.]") {
		t.Fatal("missing bootstrap cancellation recovery system message")
	}
	if !fixture.messages.containsContentSubstring("Continue the bounded project bootstrap setup workflow now.") {
		t.Fatal("missing bootstrap continuation message")
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("second HandleTurnCancelledEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 1 {
		t.Fatalf("bootstrap recovery jobs after duplicate event = %d, want 1", got)
	}
}

func TestHandleTurnCancelledEventSkipsBootstrapRecoveryWhenValidationAlreadyFailed(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.session.CurrentTurnID = nil

	now := time.Now().UTC()
	metadata, err := projectBootstrapMetadataJSON(nil, projectBootstrapState{
		Status:                  projectBootstrapStatusActive,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureMissingAssignments,
		ValidationFailureReason: "kickoff validation failed: planned tasks were created before any active project assignments were persisted",
		InitialMessageID:        fixture.userMessageID.String(),
		StartedAt:               &now,
	})
	if err != nil {
		t.Fatalf("projectBootstrapMetadataJSON: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	cancelledTurnID := uuid.New()
	fixture.chat.turns[cancelledTurnID] = &chat.ChatTurn{
		ID:           cancelledTurnID,
		SessionID:    fixture.session.ID,
		TurnNumber:   2,
		RespondingID: uuid.New(),
		Status:       "cancelled",
	}
	fixture.chat.turnOrder = append(fixture.chat.turnOrder, cancelledTurnID)

	payload, err := json.Marshal(map[string]any{
		"session_id": fixture.session.ID.String(),
		"turn_id":    cancelledTurnID.String(),
	})
	if err != nil {
		t.Fatalf("marshal cancel payload: %v", err)
	}
	event := eventbus.DomainEvent{
		OrganizationID: fixture.session.OrganizationID,
		EventType:      "chat.turn.cancelled",
		ActorType:      "system",
		Payload:        payload,
	}

	if err := fixture.engine.HandleTurnCancelledEvent(context.Background(), event); err != nil {
		t.Fatalf("HandleTurnCancelledEvent: %v", err)
	}
	if got := len(fixture.enqueuer.agentTurnJobs()); got != 0 {
		t.Fatalf("bootstrap recovery jobs = %d, want 0 when validation already failed", got)
	}
	if fixture.messages.containsContent("[Recovered cancelled bootstrap turn - retrying in a fresh turn.]") {
		t.Fatal("unexpected bootstrap cancellation recovery message")
	}
}

func TestProjectBootstrapCancelledRecoveryProgressPrefersRecoverableValidation(t *testing.T) {
	progress, recoverable := projectBootstrapCancelledRecoveryProgress(
		projectBootstrapState{
			ValidationStatus:        projectBootstrapValidationFailed,
			ValidationFailureClass:  projectBootstrapFailureCompoundParent,
			ValidationFailureReason: "kickoff validation failed: task 34 is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
		},
		projectBootstrapProgress{},
	)
	if !recoverable {
		t.Fatal("recoverable = false, want true")
	}
	if progress.ValidationFailureClass != projectBootstrapFailureCompoundParent {
		t.Fatalf("validation failure class = %q, want %q", progress.ValidationFailureClass, projectBootstrapFailureCompoundParent)
	}
	if !strings.Contains(progress.ValidationFailureReason, "task 34 is still a broad parent workstream") {
		t.Fatalf("validation failure reason = %q, want recoverable compound parent reason", progress.ValidationFailureReason)
	}
}

func TestProjectBootstrapCancelledRecoveryProgressRejectsNonRecoverableValidation(t *testing.T) {
	progress, recoverable := projectBootstrapCancelledRecoveryProgress(
		projectBootstrapState{
			ValidationStatus:        projectBootstrapValidationFailed,
			ValidationFailureClass:  projectBootstrapFailureMissingAssignments,
			ValidationFailureReason: "kickoff validation failed: planned tasks were created before any active project assignments were persisted",
		},
		projectBootstrapProgress{},
	)
	if recoverable {
		t.Fatal("recoverable = true, want false")
	}
	if progress.ValidationFailureClass != projectBootstrapFailureMissingAssignments {
		t.Fatalf("validation failure class = %q, want %q", progress.ValidationFailureClass, projectBootstrapFailureMissingAssignments)
	}
}

func TestProjectBootstrapAutoContinueMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	autoMetadata, err := json.Marshal(map[string]any{
		"source":        projectBootstrapSource,
		"auto_continue": true,
	})
	if err != nil {
		t.Fatalf("marshal auto metadata: %v", err)
	}
	autoMsg, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "Continue the bounded project bootstrap setup workflow now.",
		Metadata:  autoMetadata,
	})
	if err != nil {
		t.Fatalf("AppendMessage auto: %v", err)
	}
	if !fixture.engine.projectBootstrapAutoContinueMessage(context.Background(), autoMsg.ID) {
		t.Fatal("projectBootstrapAutoContinueMessage = false, want true")
	}

	plainMsg, err := fixture.chat.AppendMessage(context.Background(), chat.AppendMessageInput{
		SessionID: fixture.session.ID,
		Role:      "user",
		Content:   "continue bootstrap",
	})
	if err != nil {
		t.Fatalf("AppendMessage plain: %v", err)
	}
	if fixture.engine.projectBootstrapAutoContinueMessage(context.Background(), plainMsg.ID) {
		t.Fatal("projectBootstrapAutoContinueMessage = true, want false for plain user message")
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

func TestCancellationWatchReusesConsumerNameAcrossTurns(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage first turn: %v", err)
	}
	second := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   "second message",
	})
	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, second.ID); err != nil {
		t.Fatalf("HandleUserMessage second turn: %v", err)
	}

	cancelSubs := fixture.events.subscriptionNamesWithPrefix("turn-engine.cancel")
	if len(cancelSubs) != 2 {
		t.Fatalf("cancel subscription count = %d, want 2", len(cancelSubs))
	}
	if cancelSubs[0] != cancelSubs[1] {
		t.Fatalf("cancel consumer name differs across turns: %q != %q", cancelSubs[0], cancelSubs[1])
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
	var secondAssembleHistoryStart *uuid.UUID
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondAssembleHistoryStart = &copied
		}
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
	if secondAssembleHistoryStart == nil || *secondAssembleHistoryStart == uuid.Nil {
		t.Fatal("second assemble HistoryStartID is nil, want continuation summary to become history root")
	}
}

func TestContinuationTurnCapsSummaryBeforeReusingAsHistoryRoot(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: strings.Repeat("very long summary line\n", 2000)}, nil
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

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if len(summaryMessage.Content) > 5000 {
		t.Fatalf("continuation summary length = %d, want <= 5000 chars", len(summaryMessage.Content))
	}
}

func TestAsyncProjectTaskGuardrailContinuationDepthRequeuesFromSyntheticContinuationRoot(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: defaultPromptTokenGuardrail + 1}},
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	jobs := fixture.enqueuer.agentTurnJobs()
	if len(jobs) != 1 {
		t.Fatalf("agent turn jobs = %d, want 1", len(jobs))
	}
	if jobs[0].payload == nil {
		t.Fatal("expected queued agent_turn payload")
	}
	if jobs[0].runAfter == nil {
		t.Fatal("expected delayed continuation retry")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var actionMessage *repo.ChatMessage
	var retryNoticeCount int
	for i := range messages {
		message := messages[i]
		if strings.Contains(message.Content, "retrying from a narrowed continuation root") {
			retryNoticeCount++
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Content), buildTaskContinuationActionPrompt("")) {
			continue
		}
		if taskContinuationResumeMessageRootsHistory(message) {
			copied := message
			actionMessage = &copied
		}
	}
	if retryNoticeCount != 1 {
		t.Fatalf("retry notice count = %d, want 1", retryNoticeCount)
	}
	if actionMessage == nil {
		t.Fatal("expected synthetic continuation root message")
	}
	if jobs[0].payload.MessageID != actionMessage.ID {
		t.Fatalf("queued message id = %s, want %s", jobs[0].payload.MessageID, actionMessage.ID)
	}
}

func TestTaskContinuationRootMessageStartsAssemblyAtTriggerMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	rootMessage := fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "pending",
		Content:   buildTaskContinuationActionPrompt(""),
		Metadata:  taskContinuationResumeMessageMetadata(1),
	})

	var assembledHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || input.HistoryStartID == nil {
			return
		}
		copied := *input.HistoryStartID
		assembledHistoryStart = &copied
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "ok"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, rootMessage.ID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if assembledHistoryStart == nil {
		t.Fatal("HistoryStartID is nil, want continuation root message id")
	}
	if *assembledHistoryStart != rootMessage.ID {
		t.Fatalf("HistoryStartID = %s, want %s", *assembledHistoryStart, rootMessage.ID)
	}
}

func TestContinuationTurnNormalizesGenericNoContextSummary(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I don't have a continuation summary or prior context about an active task. Please provide the task details and current progress."}, nil
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

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Continuation summary unavailable.") {
		t.Fatalf("continuation summary = %q, want normalized unavailable message", summaryMessage.Content)
	}
}

func TestContinuationTurnUsesTaskFallbackSummaryForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "I don't have a continuation summary or prior context about an active task. Please provide the task details and current progress."}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessage *repo.ChatMessage
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			msgCopy := msg
			summaryMessage = &msgCopy
		}
	}
	if summaryMessage == nil {
		t.Fatal("continuation summary message missing")
	}
	if !strings.Contains(summaryMessage.Content, "Task execution is already underway.") {
		t.Fatalf("continuation summary = %q, want actionable task fallback summary", summaryMessage.Content)
	}
}

func TestContinuationTurnAppendsDirectActionPromptForAsyncProjectTask(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var secondHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "continuation_summary" {
			return ModelResponse{Content: "Task is mid-flight and should continue from the existing workspace state."}, nil
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
	if secondHistoryStart == nil || *secondHistoryStart == uuid.Nil {
		t.Fatal("second assemble HistoryStartID is nil, want continuation summary to remain history root")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var summaryMessageID uuid.UUID
	var actionPromptFound bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Continuation summary]") {
			summaryMessageID = msg.ID
		}
		if strings.Contains(msg.Content, "Continue the active task now from the continuation summary above.") {
			actionPromptFound = true
			if !strings.Contains(msg.Content, "Your next response must take direct action on the task instead of generic chat.") {
				t.Fatalf("action prompt = %q, want direct action guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you are ready, ask what to do next, or ask the user what they need.") {
				t.Fatalf("action prompt = %q, want anti-generic-chat guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you lack context or ask the user to restate the task when this continuation turn already includes the task session history and continuation summary.") {
				t.Fatalf("action prompt = %q, want anti-no-context guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
		}
	}
	if summaryMessageID == uuid.Nil {
		t.Fatal("continuation summary message missing")
	}
	if !actionPromptFound {
		t.Fatal("task continuation action prompt missing")
	}
	if *secondHistoryStart != summaryMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want continuation summary %s", *secondHistoryStart, summaryMessageID)
	}
	turns, err := fixture.chat.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turn count = %d, want 2", len(turns))
	}
	last := turns[len(turns)-1]
	if last.TriggerMessageID == nil || *last.TriggerMessageID != summaryMessageID {
		t.Fatalf("continuation turn trigger_message_id = %v, want %s", last.TriggerMessageID, summaryMessageID)
	}
}

func TestBuildTaskContinuationActionPromptTreatsDocumentSummaryAsDraft(t *testing.T) {
	summary := "# Visual Direction\n\n- Kind: strategy_artifact\n\n## Design Principles\nConcrete draft body."

	prompt := buildTaskContinuationActionPrompt(summary)

	if !strings.Contains(prompt, "The continuation summary above already contains draft deliverable content. Treat it as the working artifact draft for this turn.") {
		t.Fatalf("prompt = %q, want draft-summary guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not reopen broad workspace context or search for more source material before using that draft.") {
		t.Fatalf("prompt = %q, want anti-reread draft guidance", prompt)
	}
	if !strings.Contains(prompt, "If a target file is in scope, revise the draft directly and write the file with concrete content instead of re-deriving the document from scratch.") {
		t.Fatalf("prompt = %q, want direct-write guidance", prompt)
	}
}

func TestBuildProjectContinuationActionPrompt(t *testing.T) {
	prompt := buildProjectContinuationActionPrompt("Project execution should continue directly from the current task tree.")

	if !strings.Contains(prompt, "Continue the active project execution now from the continuation summary above.") {
		t.Fatalf("prompt = %q, want project continuation lead-in", prompt)
	}
	if !strings.Contains(prompt, "Your next response must take direct project action instead of generic chat.") {
		t.Fatalf("prompt = %q, want direct project action guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not say that you are ready, ask what to do next, or ask the user what they need.") {
		t.Fatalf("prompt = %q, want anti-ready guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not say that you lack context or ask the user to restate the project when this continuation turn already includes the project session history and continuation summary.") {
		t.Fatalf("prompt = %q, want anti-restatement guidance", prompt)
	}
	if !strings.Contains(prompt, "Use the existing task tree, workspace state, planning artifacts, and recent tool results to continue execution directly.") {
		t.Fatalf("prompt = %q, want direct execution guidance", prompt)
	}
}

func TestProjectExecutionContinuationFallbackSummary(t *testing.T) {
	summary := projectExecutionContinuationFallbackSummary()

	if !strings.Contains(summary, "Project execution is already underway.") {
		t.Fatalf("summary = %q, want execution-underway guidance", summary)
	}
	if !strings.Contains(summary, "Reuse the existing project task tree, workspace artifacts, planning files, and recent tool results") {
		t.Fatalf("summary = %q, want concrete project-state reuse guidance", summary)
	}
}

func TestRecoveryTurnAppendsDirectActionPromptForAsyncProjectTaskWithoutCheckpoint(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				AssignedAgentID: &assignedID,
			},
		},
	}
	if _, err := fixture.messages.UpdateContent(context.Background(), fixture.userMessageID, "supervisor recovery: resume task"); err != nil {
		t.Fatalf("UpdateContent recovery kickoff: %v", err)
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var actionPromptFound bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "Continue the active task recovery now.") {
			actionPromptFound = true
			if !strings.Contains(msg.Content, "Your next response must take direct recovery action on the task instead of generic chat.") {
				t.Fatalf("action prompt = %q, want direct recovery guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not say that you lack context or ask the user to restate the task when this recovery turn already includes the task session history and recovery kickoff.") {
				t.Fatalf("action prompt = %q, want anti-no-context guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not start with project.list, project.get, task.list, task.get, task_get, flow.list_templates, flow.get_execution, file.read, file_list, or agent.list unless a specific blocker names that exact record.") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
		}
	}
	if !actionPromptFound {
		t.Fatal("task recovery action prompt missing")
	}
}

func TestBuildRecoveryResumeActionPromptHardensIntentOnlyCheckpointWithoutDraft(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:    "docs/sitemap-and-navigation.md",
		blockerClass:  taskcheckpoint.RecoveryFileWriteBlockerClassRepeatedNonSubstantiveCheckpoint,
		failureReason: "repeated intent-only recovery drafts for docs/sitemap-and-navigation.md across explicit resume attempts; latest assistant draft for docs/sitemap-and-navigation.md described intent to write the deliverable instead of the file body",
	})

	if !strings.Contains(prompt, "must begin with the concrete file body for docs/sitemap-and-navigation.md itself") {
		t.Fatalf("prompt = %q, want direct begin-with-file-body guidance", prompt)
	}
	if !strings.Contains(prompt, "The first non-whitespace character of your next assistant message must be the first character of the deliverable body itself.") {
		t.Fatalf("prompt = %q, want first-character guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start with phrases like 'I', 'I'll', 'I will', 'Now I'll', 'Let me', 'Here is', or 'Below is' before the file body.") {
		t.Fatalf("prompt = %q, want anti-preface guidance", prompt)
	}
	if !strings.Contains(prompt, "No substantive durable draft is available.") {
		t.Fatalf("prompt = %q, want no-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "If the target is Markdown, start immediately with a heading and real section content.") {
		t.Fatalf("prompt = %q, want markdown-start guidance", prompt)
	}
	if !strings.Contains(prompt, "already hardened after repeated non-substantive drafts") {
		t.Fatalf("prompt = %q, want repeated-draft hardening guidance", prompt)
	}
}

func TestBuildRecoveryResumeActionPromptUsesAvailableDraftDirectly(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:    "planning/prd-spec/oc-24-infrastructure-spec.md",
		artifactDraft: "# Infrastructure Specification\n\n## Hosting\nConcrete content.",
	})

	if !strings.Contains(prompt, "A substantive durable draft is already available above. Reuse that draft body directly") {
		t.Fatalf("prompt = %q, want substantive-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "your next assistant message should begin with the first line of the best available draft") {
		t.Fatalf("prompt = %q, want first-line draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Your entire next assistant message must be either the concrete file body for the target deliverable or one concrete blocker sentence.") {
		t.Fatalf("prompt = %q, want body-or-blocker guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not ask 'What do you need?', 'What would you like me to do?', or any equivalent recovery question.") {
		t.Fatalf("prompt = %q, want anti-recovery-question guidance", prompt)
	}
}

func TestBuildRecoveryResumeActionPromptUsesContinuationSummaryDraftDirectly(t *testing.T) {
	t.Parallel()

	prompt := buildRecoveryResumeActionPrompt(recoveryResumeState{
		targetPath:   "planning/prd-spec/oc-24-infrastructure-spec.md",
		summaryDraft: "# Infrastructure Specification\n\n## Hosting\nConcrete content.",
	})

	if !strings.Contains(prompt, "A substantive durable draft is already available above. Reuse that draft body directly") {
		t.Fatalf("prompt = %q, want substantive-draft guidance", prompt)
	}
	if !strings.Contains(prompt, "your next assistant message should begin with the first line of the best available draft") {
		t.Fatalf("prompt = %q, want first-line draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not browse .ottercamp/recovery broadly or read recovery artifacts for other tasks") {
		t.Fatalf("prompt = %q, want same-task recovery scope guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not reread strategy artifacts, planning files, or workspace listings before writing") {
		t.Fatalf("prompt = %q, want anti-reread draft guidance", prompt)
	}
	if !strings.Contains(prompt, "Your entire next assistant message must be either the concrete file body for the target deliverable or one concrete blocker sentence.") {
		t.Fatalf("prompt = %q, want body-or-blocker guidance", prompt)
	}
}

func TestLooksLikeRecoveryIntentNarrationPlaceholderDetectsNowIllWritePreface(t *testing.T) {
	t.Parallel()

	if !looksLikeRecoveryIntentNarrationPlaceholder("Now I'll write the substantive blog post template design specification:") {
		t.Fatal("expected now-I'll-write preface to be rejected as intent narration")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsContextSummaryReadyReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to help. I'm Alex, Technical Lead for the SAM.blog rebuild. Based on the context, I can see the target file and current blocker."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected context-summary ready reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsIllHelpStub(t *testing.T) {
	t.Parallel()

	if !looksLikeGenericTaskRecoveryReply("I'll help") {
		t.Fatal("expected I'll-help stub to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsFocusQuestionMenu(t *testing.T) {
	t.Parallel()

	content := "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm ready to work on OC-24. " +
		"I have access to the planning artifacts for this task. What would you like me to focus on first? " +
		"Or is there a specific decision or constraint I should know about before I start?"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected focus-question menu to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsMoveForwardStatusReply(t *testing.T) {
	t.Parallel()

	content := "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm ready to help you move forward on OC-24: Plan Hosting and Infrastructure.\n\n" +
		"Current Status:\n" +
		"- Strategy phase is complete\n" +
		"- Task is in progress\n" +
		"- Next deliverable is the infrastructure spec"
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected move-forward status reply to be treated as generic recovery output")
	}
}

func TestLooksLikeGenericTaskRecoveryReplyDetectsDraftingConfirmationReply(t *testing.T) {
	t.Parallel()

	content := "I'm ready to continue on OC-24: Plan hosting and infrastructure. " +
		"Before I proceed with drafting the infrastructure spec, I need to confirm the locked decisions are still valid."
	if !looksLikeGenericTaskRecoveryReply(content) {
		t.Fatal("expected drafting confirmation reply to be treated as generic recovery output")
	}
}

func TestLooksLikeRecoveryQuestionEchoDetectsClarificationMenu(t *testing.T) {
	t.Parallel()

	content := "I'm ready to help with the SAM.blog rebuild infrastructure task (OC-24).\n\n" +
		"**Quick clarification:**\n" +
		"- Continue drafting the success narrative?\n" +
		"- Move to the decision log or tradeoff matrix?\n\n" +
		"Please let me know the most useful next step."
	if !looksLikeRecoveryQuestionEcho(content) {
		t.Fatal("expected recovery clarification menu to be treated as a question echo")
	}
}

func TestContinuationTurnUsesDeterministicBootstrapResumeState(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	pmID := uuid.New()
	workerAID := uuid.New()
	workerBID := uuid.New()
	reviewerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "bootstrap_planning",
			"assignment_count":            6,
			"planned_task_count":          18,
			"planned_flow_template_count": 1,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata
	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:       {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerAID:  {ID: workerAID, DisplayName: "André Kowalski"},
			workerBID:  {ID: workerBID, DisplayName: "Ananya Webb"},
			reviewerID: {ID: reviewerID, DisplayName: "Vivian Cho"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerAID, Role: "worker", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerBID, Role: "worker", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: reviewerID, Role: "reviewer", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	var secondHistoryStart *uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 for active bootstrap continuation", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	var resumeContent string
	var resumeMessageID uuid.UUID
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
			resumeContent = msg.Content
			resumeMessageID = msg.ID
			break
		}
	}
	if !sawResume {
		t.Fatal("project bootstrap resume message missing")
	}
	if !strings.Contains(resumeContent, "Existing PM: Sam.blog PM") {
		t.Fatalf("resume message = %q, want existing PM line", resumeContent)
	}
	if !strings.Contains(resumeContent, "id="+pmID.String()) || !strings.Contains(resumeContent, "class=staff") || !strings.Contains(resumeContent, "type=pm") {
		t.Fatalf("resume message = %q, want PM identity details", resumeContent)
	}
	if !strings.Contains(resumeContent, "Active project id: "+fixture.session.ScopeID.String()) {
		t.Fatalf("resume message = %q, want active project id line", resumeContent)
	}
	if !strings.Contains(resumeContent, "Existing active assignments:") ||
		!strings.Contains(resumeContent, "Vivian Cho (id=") ||
		!strings.Contains(resumeContent, "Ananya Webb (id=") ||
		!strings.Contains(resumeContent, "André Kowalski (id=") {
		t.Fatalf("resume message = %q, want assignment roster", resumeContent)
	}
	if !strings.Contains(resumeContent, "Do not create duplicate agents or another PM.") {
		t.Fatalf("resume message = %q, want duplicate-agent guardrail", resumeContent)
	}
	if !strings.Contains(resumeContent, "The persisted task tree already has runnable flow templates.") {
		t.Fatalf("resume message = %q, want first-wave promotion guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "The bootstrap governance gate task is system-managed") {
		t.Fatalf("resume message = %q, want governance gate guidance", resumeContent)
	}
	if secondHistoryStart == nil {
		t.Fatal("second assemble HistoryStartID is nil, want bootstrap continuation to root at resume message")
	}
	if resumeMessageID == uuid.Nil {
		t.Fatal("resume message id missing")
	}
	if *secondHistoryStart != resumeMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want resume message %s", *secondHistoryStart, resumeMessageID)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want at least 2 after continuation", len(fixture.chat.turnOrder))
	}
}

func TestBootstrapAutoContinueTurnAppendsResumeStateBeforeFirstModelCall(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               projectBootstrapCheckpointFirstWaveExecutions,
			"last_successful_checkpoint":  projectBootstrapCheckpointFirstWaveSelected,
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
			"first_wave_task_count":       4,
			"validation_status":           projectBootstrapValidationPassed,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}
	fixture.session.Metadata = metadata
	fixture.chat.session.Metadata = metadata

	messageMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal message metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, messageMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz", AgentClass: "staff", AgentType: "worker"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	var sawAction bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
		}
		if strings.Contains(msg.Content, "Continue the active project bootstrap from the persisted state above.") {
			sawAction = true
		}
	}
	if !sawResume {
		t.Fatal("project bootstrap resume message missing for bootstrap auto-continue turn")
	}
	if !sawAction {
		t.Fatal("bootstrap resume action prompt missing for bootstrap auto-continue turn")
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0", fixture.model.continuationSummaryCalls)
	}
}

func TestBuildProjectBootstrapResumeStateMessageUsesCompactRosterForLateFirstWaveState(t *testing.T) {
	state := projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		AssignmentCount:          6,
		PlannedTaskCount:         18,
		PlannedFlowTemplateCount: 1,
		FirstWaveTaskCount:       8,
		FirstWavePromotedCount:   0,
		FirstWaveJobCount:        0,
		ValidationStatus:         projectBootstrapValidationPassed,
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      uuid.NewString(),
		ProjectSlug:    "sam-blog-compact",
		ExistingPM:     "Sam.blog PM (id=" + uuid.NewString() + ", class=staff, type=pm)",
		AssignmentLine: "workers=Ananya Webb (id=" + uuid.NewString() + "), André Kowalski (id=" + uuid.NewString() + ")",
	}

	resumeContent := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if strings.Contains(resumeContent, "Existing active assignments:") {
		if !strings.Contains(resumeContent, "Existing active assignments: workers=Ananya Webb") {
			t.Fatalf("resume message = %q, want compact assignment roster summary", resumeContent)
		}
	}
	if !strings.Contains(resumeContent, "Existing staffing is already persisted for 6 active project assignments.") {
		t.Fatalf("resume message = %q, want compact staffing summary", resumeContent)
	}
	if !strings.Contains(resumeContent, "Do not create more agents, parent tasks, or broad child-task batches") {
		t.Fatalf("resume message = %q, want late first-wave guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "Reuse the existing active project assignees, including temp agents") {
		t.Fatalf("resume message = %q, want persisted-assignee reuse guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not spend tools re-reading scaffold planning artifacts or re-listing the full task tree and template catalog before acting") {
		t.Fatalf("resume message = %q, want anti-reread guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "Prefer direct task assignment, first-wave selection, and bootstrap.setup.persist") {
		t.Fatalf("resume message = %q, want direct action guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "keep the bootstrap governance gate task untouched") {
		t.Fatalf("resume message = %q, want compact governance guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "bootstrap.setup.persist tool") {
		t.Fatalf("resume message = %q, want setup persist tool guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "not raw task.update status changes") {
		t.Fatalf("resume message = %q, want raw task.update warning", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not manually queue or start first-wave execution tasks") {
		t.Fatalf("resume message = %q, want no manual first-wave promotion guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not call task.list or flow.list_templates before trying bootstrap.setup.persist") {
		t.Fatalf("resume message = %q, want setup-persist-first guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist") {
		t.Fatalf("resume message = %q, want repo-binding anti-drift guidance", resumeContent)
	}
	if !strings.Contains(resumeContent, "completed_step_slugs for bind-repo-environment, staff-project, decompose-workstreams, validate-task-shape, attach-validate-flow-templates, select-first-wave, and record-frank-sign-off") {
		t.Fatalf("resume message = %q, want canonical bootstrap step slugs", resumeContent)
	}
	if !strings.Contains(resumeContent, "include sign_off_summary when recording Frank approval") {
		t.Fatalf("resume message = %q, want Frank sign-off guidance", resumeContent)
	}
}

func TestProjectBootstrapResumeNeedsSetupPersist(t *testing.T) {
	state := projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		FirstWaveTaskCount:       4,
		ValidationStatus:         projectBootstrapValidationPassed,
	}
	if !projectBootstrapResumeNeedsSetupPersist(state) {
		t.Fatal("resume state should require setup persist guidance when validation passed but bootstrap gate is still outstanding")
	}

	state.FirstWavePromotedCount = 1
	if projectBootstrapResumeNeedsSetupPersist(state) {
		t.Fatal("resume state should not require setup persist guidance after first-wave promotion has started")
	}
}

func TestBuildProjectBootstrapResumeActionPromptForSetupPersist(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		FirstWaveTaskCount:       3,
		ValidationStatus:         projectBootstrapValidationPassed,
	})
	if !strings.Contains(prompt, "Do not call task.list, flow.list_templates, or file.read on scaffold planning artifacts") {
		t.Fatalf("prompt = %q, want tool-specific anti-reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "call bootstrap.setup.persist immediately with completed_step_slugs for staff-project, decompose-workstreams, and validate-task-shape") {
		t.Fatalf("prompt = %q, want early completed-step guidance", prompt)
	}
	if !strings.Contains(prompt, "finish first-wave selection, and then call bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want setup persist sequencing", prompt)
	}
	if !strings.Contains(prompt, "Before any task.list or flow.list_templates call, try bootstrap.setup.persist first") {
		t.Fatalf("prompt = %q, want setup-persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use git.commit or ad hoc cli.execute commands just to satisfy the bootstrap checklist") {
		t.Fatalf("prompt = %q, want repo-binding anti-drift guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use raw task.update to queue or start first-wave execution tasks") {
		t.Fatalf("prompt = %q, want no manual first-wave queue guidance", prompt)
	}
	if !strings.Contains(prompt, "do not edit, assign, queue, or complete the gate task manually") {
		t.Fatalf("prompt = %q, want governance gate protection", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForValidationFailure(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFlowTemplatesPersisted,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveSize,
		ValidationFailureReason: "kickoff validation failed: first-wave task 35 (List blog personality traits) violates the bounded task-size policy: split it into smaller reviewable tasks before queueing",
	})
	if !strings.Contains(prompt, "Bootstrap is currently blocked on this validation failure: kickoff validation failed: first-wave task 35") {
		t.Fatalf("prompt = %q, want concrete validation failure", prompt)
	}
	if !strings.Contains(prompt, "Do not start with bootstrap.setup.persist on this turn") {
		t.Fatalf("prompt = %q, want no-persist-first guidance", prompt)
	}
	if !strings.Contains(prompt, "split that exact persisted task into narrower executable child tasks") {
		t.Fatalf("prompt = %q, want exact-task split guidance", prompt)
	}
	if !strings.Contains(prompt, "Only after the named blocker is repaired should you call bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want repair-before-persist guidance", prompt)
	}
	if !strings.Contains(prompt, "Current phase names like first_wave_executions_created are not valid completed_step_slugs") {
		t.Fatalf("prompt = %q, want canonical step slug guidance", prompt)
	}
	if !strings.Contains(prompt, "do not use raw task.update to force draft first-wave tasks into queued or in_progress") {
		t.Fatalf("prompt = %q, want no-manual-promotion guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForPartialFirstWaveMaterialization(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: only 12 of 20 selected first-wave child tasks created flow_node_execution rows, so bootstrap never materialized the full runnable child wave",
	})
	if !strings.Contains(prompt, "selected first wave is too large or too broad to materialize cleanly in one pass") {
		t.Fatalf("prompt = %q, want first-wave narrowing guidance", prompt)
	}
	if !strings.Contains(prompt, "Reduce the first wave to a smaller bounded subset of the already-created child tasks") {
		t.Fatalf("prompt = %q, want smaller first-wave guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start that repair with project.list, project.get, task.list, flow.list_templates, flow.get_execution, file.read, file.write, agent.list, or staffing discovery") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not rewrite planning artifacts or restaff the project") {
		t.Fatalf("prompt = %q, want no planning rewrite guidance", prompt)
	}
	if !strings.Contains(prompt, "repair the runnable subset with direct task and flow mutations") {
		t.Fatalf("prompt = %q, want direct repair guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedFirstWaveTask(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	})
	if !strings.Contains(prompt, "already names the exact unassigned first-wave task") {
		t.Fatalf("prompt = %q, want exact-task repair guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not start with project.get, task.list, task.children, flow.list_templates, or agent.list") {
		t.Fatalf("prompt = %q, want no broad reread guidance", prompt)
	}
	if !strings.Contains(prompt, "Do not call task.get with the bare task number from the validation error") {
		t.Fatalf("prompt = %q, want direct task id guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedWaveParent(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		CurrentPhase:            projectBootstrapCheckpointFirstWaveExecutions,
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 11 (Wave 3: Polish, Deploy & Content) has no assigned agent",
	})
	if !strings.Contains(prompt, "If the named task is a broad wave/workstream parent, do not read planning artifacts first") {
		t.Fatalf("prompt = %q, want no planning reread guidance", prompt)
	}
	if !strings.Contains(prompt, "create bounded executable child tasks directly beneath it") {
		t.Fatalf("prompt = %q, want child-task guidance", prompt)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesValidationFailure(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		CurrentPhase:             projectBootstrapCheckpointFlowTemplatesPersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 35 (List blog personality traits) violates the bounded task-size policy",
	}, projectBootstrapResumeSnapshot{
		ProjectID:   uuid.NewString(),
		ProjectSlug: "sam-blog-test",
	})
	if !strings.Contains(message, "Current validation failure: kickoff validation failed: first-wave task 35") {
		t.Fatalf("message = %q, want validation failure line", message)
	}
}

func TestBuildProjectBootstrapResumeStateMessageIncludesNamedBlockedTask(t *testing.T) {
	message := buildProjectBootstrapResumeStateMessage(projectBootstrapState{
		ValidationStatus:        projectBootstrapValidationFailed,
		ValidationFailureReason: "kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent",
	}, projectBootstrapResumeSnapshot{
		ProjectID:      uuid.NewString(),
		ProjectSlug:    "sam-blog-test",
		FailedTaskLine: "Named blocked task: task 19 id=1234 title=\"Draft homepage hero\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
	})
	if !strings.Contains(message, "Named blocked task: task 19 id=1234") {
		t.Fatalf("message = %q, want blocked task line", message)
	}
	if !strings.Contains(message, "Use task.update directly on this task id instead of task.get with the bare task number") {
		t.Fatalf("message = %q, want direct update guidance", message)
	}
}

func TestProjectBootstrapFailureTaskNumber(t *testing.T) {
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: first-wave task 19 (Draft homepage hero) has no assigned agent"); got != 19 {
		t.Fatalf("task number = %d, want 19", got)
	}
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: task 21 (Build the individual blog post template) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete"); got != 21 {
		t.Fatalf("task number = %d, want 21", got)
	}
	if got := projectBootstrapFailureTaskNumber("kickoff validation failed: bootstrap setup persisted staffing but did not emit any executable tasks"); got != 0 {
		t.Fatalf("task number = %d, want 0", got)
	}
}

func TestProjectBootstrapRecoveryTargetFromMessage(t *testing.T) {
	message := "Continue the bounded project bootstrap setup workflow now. This is automatic follow-on bootstrap turn 3. Recovery target: kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent, so bootstrap cannot queue runnable execution. Do not repeat the same oversized task definitions."
	if got := projectBootstrapRecoveryTargetFromMessage(message); got != "kickoff validation failed: first-wave task 12 (Content Creation) has no assigned agent, so bootstrap cannot queue runnable execution." {
		t.Fatalf("recovery target = %q", got)
	}
	if got := projectBootstrapRecoveryTargetFromMessage("Continue bootstrap."); got != "" {
		t.Fatalf("recovery target = %q, want empty", got)
	}
}

func TestProjectBootstrapBlockedRecoveryFailureFallsBackToRecentRecoveryMessage(t *testing.T) {
	reason, class := projectBootstrapBlockedRecoveryFailure([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution. Do not stop at acknowledgement."},
		{Role: "assistant", Content: "I'll first reread the state."},
	}, projectBootstrapState{})
	if reason != "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution." {
		t.Fatalf("reason = %q", reason)
	}
	if class != projectBootstrapFailureFirstWaveExecution {
		t.Fatalf("class = %q, want %q", class, projectBootstrapFailureFirstWaveExecution)
	}
}

func TestProjectBootstrapAckOnlyRecoveryReply(t *testing.T) {
	if !projectBootstrapAckOnlyReply(&repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("expected bare acknowledgement to be detected")
	}
	if projectBootstrapAckOnlyReply(&repo.ChatMessage{Role: "assistant", Content: "Acknowledged. I will assign the task now."}) {
		t.Fatal("did not expect substantive acknowledgement to be treated as ack-only")
	}
	if !projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("expected ack-only recovery reply to be detected")
	}
	if projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue bootstrap."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged."}) {
		t.Fatal("did not expect non-recovery ack to be detected")
	}
	if projectBootstrapAckOnlyRecoveryReply([]repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}, &repo.ChatMessage{Role: "assistant", Content: "Acknowledged. I will assign the task now."}) {
		t.Fatal("did not expect substantive reply to be treated as ack-only")
	}
}

func TestProjectBootstrapNarrativeOnlyRecoveryReply(t *testing.T) {
	messages := []repo.ChatMessage{
		{Role: "user", Content: "Continue the bounded project bootstrap setup workflow now. Recovery target: kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution."},
	}
	if !projectBootstrapNarrativeOnlyRecoveryReply(messages, &repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."}) {
		t.Fatal("expected memory-only recovery reply to be detected")
	}
	if projectBootstrapNarrativeOnlyRecoveryReply(messages, &repo.ChatMessage{Role: "assistant", Content: "I cannot continue because the repo credential is missing."}) {
		t.Fatal("did not expect a concrete blocker to be treated as narrative-only")
	}
	if projectBootstrapNarrativeOnlyRecoveryReply(append(messages, repo.ChatMessage{Role: "tool", Content: "{}"}), &repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."}) {
		t.Fatal("did not expect tool-backed recovery reply to be treated as narrative-only")
	}
}

func TestBuildProjectBootstrapAckOnlyRecoveryFailureReason(t *testing.T) {
	got := buildProjectBootstrapAckOnlyRecoveryFailureReason("kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent")
	if !strings.Contains(got, "acknowledgement only") {
		t.Fatalf("expected acknowledgement-only wording, got %q", got)
	}
	if !strings.Contains(got, "first-wave task 11 (Site Build) has no assigned agent") {
		t.Fatalf("expected target in failure reason, got %q", got)
	}
}

func TestBuildProjectBootstrapAckOnlyRestartFailureReason(t *testing.T) {
	got := buildProjectBootstrapAckOnlyRestartFailureReason()
	if !strings.Contains(got, "automatic bootstrap restart") {
		t.Fatalf("expected restart wording, got %q", got)
	}
	if !strings.Contains(got, "acknowledgement only") {
		t.Fatalf("expected acknowledgement-only wording, got %q", got)
	}
}

func TestBuildProjectBootstrapNarrativeOnlyRecoveryFailureReason(t *testing.T) {
	got := buildProjectBootstrapNarrativeOnlyRecoveryFailureReason(
		"kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent",
		&repo.ChatMessage{Role: "assistant", Content: "I recall prior infrastructure details from memory."},
	)
	if !strings.Contains(got, "narrative only") {
		t.Fatalf("expected narrative-only wording, got %q", got)
	}
	if !strings.Contains(got, "I recall prior infrastructure details from memory.") {
		t.Fatalf("expected reply content in failure reason, got %q", got)
	}
}

func TestBuildProjectBootstrapResumeActionPromptForUnassignedTaskRequiresImmediateToolAction(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution",
	}
	got := buildProjectBootstrapResumeActionPrompt(state)
	if !strings.Contains(got, "Your next assistant action should be a tool call, not a narrative reply.") {
		t.Fatalf("expected immediate tool action guidance, got %q", got)
	}
	if !strings.Contains(got, "call task.update on that task now") {
		t.Fatalf("expected direct task.update guidance, got %q", got)
	}
}

func TestBuildProjectBootstrapResumeStateMessageRequiresDirectRepairAction(t *testing.T) {
	state := projectBootstrapState{
		Status:                   projectBootstrapStatusActive,
		CurrentPhase:             projectBootstrapCheckpointFirstWaveExecutions,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointFirstWaveSelected,
		AssignmentCount:          3,
		ValidationStatus:         projectBootstrapValidationFailed,
		ValidationFailureReason:  "kickoff validation failed: first-wave task 11 (Site Build) has no assigned agent, so bootstrap cannot queue runnable execution",
	}
	snapshot := projectBootstrapResumeSnapshot{
		ProjectID:      uuid.New().String(),
		ProjectSlug:    "sam-blog-test",
		FailedTaskLine: "Named blocked task: task 11 id=abc title=\"Site Build\" work_status=draft assigned_agent_id=unassigned.",
		AssignmentLine: "workers=Dev (id=worker-1), Rina (id=worker-2)",
	}
	got := buildProjectBootstrapResumeStateMessage(state, snapshot)
	if !strings.Contains(got, "The next acceptable bootstrap action is a direct task.update") {
		t.Fatalf("expected direct repair contract, got %q", got)
	}
}

func TestLoadProjectBootstrapResumeSnapshotIncludesBroadParentBlockedTaskLine(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	workerID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  projectID,
				TaskNumber: 21,
				Title:      "Build the individual blog post template",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureReason: "kickoff validation failed: task 21 (Build the individual blog post template) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if !strings.Contains(snapshot.FailedTaskLine, "Named blocked task: task 21 id="+taskID.String()) {
		t.Fatalf("FailedTaskLine = %q, want task id", snapshot.FailedTaskLine)
	}
}

func TestLoadProjectBootstrapResumeSnapshotWarnsWhenBlockedTaskIDCannotBeResolved(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	workerID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: workerID, Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{
			workerID: {ID: workerID, DisplayName: "Dev"},
		},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			uuid.New(): {
				ID:         uuid.New(),
				ProjectID:  projectID,
				TaskNumber: 92,
				Title:      "Draft first blog post from editorial calendar topic",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: first-wave task 28 (Use a topic from the editorial calendar) has no assigned agent",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if snapshot.FailedTaskLine != "" {
		t.Fatalf("FailedTaskLine = %q, want empty when stale task id cannot be resolved", snapshot.FailedTaskLine)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "Do not fabricate a UUID from the bare task number.") {
		t.Fatalf("RepairTaskLine = %q, want anti-fabrication guidance", snapshot.RepairTaskLine)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "call bootstrap.setup.persist with canonical completed_step_slugs") {
		t.Fatalf("RepairTaskLine = %q, want setup.persist fallback guidance", snapshot.RepairTaskLine)
	}
}

func TestProjectBootstrapRestartScaffoldFailureReasonMatch(t *testing.T) {
	if !projectBootstrapRestartScaffoldFailureReason(buildProjectBootstrapRestartScaffoldFailureReason()) {
		t.Fatal("expected canonical restart scaffold reason to match")
	}
	if projectBootstrapRestartScaffoldFailureReason("kickoff validation failed: first-wave task 19 has no assigned agent") {
		t.Fatal("unexpected scaffold reason match")
	}
}

func TestBuildProjectBootstrapRecoveryContinuationContext(t *testing.T) {
	context := buildProjectBootstrapRecoveryContinuationContext(projectBootstrapResumeSnapshot{
		FailedTaskLine: "Named blocked task: task 19 id=1234 title=\"Draft homepage hero\" work_status=draft assigned_agent_id=unassigned. Use task.update directly on this task id instead of task.get with the bare task number.",
		RepairTaskLine: "Other still-unassigned first-wave tasks you can repair in this same turn without rereading the task tree: task 22 id=5678 title=\"Configure Tailwind and design tokens\".",
		AssignmentLine: "workers=Ananya Webb (id=worker-1), Naomi Baptiste (id=worker-2)",
	})
	if !strings.Contains(context, "Named blocked task: task 19 id=1234") {
		t.Fatalf("context = %q, want blocked task line", context)
	}
	if !strings.Contains(context, "task 22 id=5678") {
		t.Fatalf("context = %q, want additional repair targets", context)
	}
	if !strings.Contains(context, "Existing active assignments: workers=Ananya Webb") {
		t.Fatalf("context = %q, want assignment roster", context)
	}
	if !strings.Contains(context, "do not call agent.list unless the persisted roster itself is inconsistent") {
		t.Fatalf("context = %q, want no-agent-list guidance", context)
	}
}

func TestBuildProjectBootstrapCompoundParentRepairTaskLineListsTopLevelDrafts(t *testing.T) {
	blockedID := uuid.New()
	childParentID := uuid.New()
	line := buildProjectBootstrapCompoundParentRepairTaskLine([]repo.ProjectTask{
		{
			ID:         blockedID,
			TaskNumber: 11,
			Title:      "Page Build",
			WorkStatus: "draft",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 13,
			Title:      "Homepage layout",
			WorkStatus: "draft",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 14,
			Title:      "Blog index layout",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"decomposition_parent_task_id":"` + childParentID.String() + `"}`),
		},
		{
			ID:         uuid.New(),
			TaskNumber: 15,
			Title:      "Post template",
			WorkStatus: "queued",
		},
		{
			ID:         uuid.New(),
			TaskNumber: 16,
			Title:      "Navigation shell",
			WorkStatus: "draft",
		},
	}, repo.ProjectTask{
		ID:         blockedID,
		TaskNumber: 11,
		Title:      "Page Build",
		WorkStatus: "draft",
	})
	if !strings.Contains(line, "task 13") || !strings.Contains(line, "Homepage layout") {
		t.Fatalf("line = %q, want top-level draft task", line)
	}
	if !strings.Contains(line, "task 16") || !strings.Contains(line, "Navigation shell") {
		t.Fatalf("line = %q, want second top-level draft task", line)
	}
	if strings.Contains(line, "task 14") {
		t.Fatalf("line = %q, should skip existing child task", line)
	}
	if strings.Contains(line, "task 15") {
		t.Fatalf("line = %q, should skip non-draft task", line)
	}
}

func TestCanonicalProjectBootstrapSetupTasksPrefersCompletedCanonicalSet(t *testing.T) {
	projectID := uuid.New()
	doneBindID := uuid.New()
	draftBindID := uuid.New()
	doneSignoffID := uuid.New()
	draftSignoffID := uuid.New()

	canonical, byID := canonicalProjectBootstrapSetupTasks([]repo.ProjectTask{
		{
			ID:         draftBindID,
			ProjectID:  projectID,
			TaskNumber: 10,
			Title:      "Bind repo and environment",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         doneBindID,
			ProjectID:  projectID,
			TaskNumber: 2,
			Title:      "Bind repo and environment",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         draftSignoffID,
			ProjectID:  projectID,
			TaskNumber: 16,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "draft",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
		{
			ID:         doneSignoffID,
			ProjectID:  projectID,
			TaskNumber: 8,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
	})

	if len(canonical) != 2 {
		t.Fatalf("canonical setup task count = %d, want 2", len(canonical))
	}
	if canonical[0].ID != doneBindID || canonical[1].ID != doneSignoffID {
		t.Fatalf("canonical setup task ids = [%s %s], want [%s %s]", canonical[0].ID, canonical[1].ID, doneBindID, doneSignoffID)
	}
	if _, ok := byID[doneBindID]; !ok {
		t.Fatal("done bind task missing from canonical byID map")
	}
	if _, ok := byID[draftBindID]; ok {
		t.Fatal("draft duplicate bind task should not remain in canonical byID map")
	}
}

func TestCanonicalProjectBootstrapGateTaskMatchesCompletedSetupBatch(t *testing.T) {
	projectID := uuid.New()
	earlyGateID := uuid.New()
	lateGateID := uuid.New()

	setupTasks := []repo.ProjectTask{
		{
			ID:         uuid.New(),
			ProjectID:  projectID,
			TaskNumber: 10,
			Title:      "Bind repo and environment",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"bind-repo-environment"}`),
		},
		{
			ID:         uuid.New(),
			ProjectID:  projectID,
			TaskNumber: 16,
			Title:      "Request and record Frank sign-off",
			WorkStatus: "done",
			Metadata:   json.RawMessage(`{"bootstrap_setup_task":true,"bootstrap_step_slug":"record-frank-sign-off"}`),
		},
	}

	gate, ok := canonicalProjectBootstrapGateTask([]repo.ProjectTask{
		{ID: earlyGateID, ProjectID: projectID, TaskNumber: 1, Title: "Bootstrap governance gate", WorkStatus: "draft", Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
		{ID: lateGateID, ProjectID: projectID, TaskNumber: 9, Title: "Bootstrap governance gate", WorkStatus: "draft", Metadata: json.RawMessage(`{"bootstrap_gate":true}`)},
	}, setupTasks)
	if !ok {
		t.Fatal("canonicalProjectBootstrapGateTask ok = false, want true")
	}
	if gate.ID != lateGateID {
		t.Fatalf("canonical gate id = %s, want %s", gate.ID, lateGateID)
	}
}

func TestLoadProjectBootstrapResumeSnapshotAddsCompoundParentRepairTargets(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	taskID := uuid.New()
	task13ID := uuid.New()
	task14ID := uuid.New()
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: projectID, AgentID: uuid.New(), Role: "worker"},
		},
	}
	fixture.engine.agents = &fakeAgentRepo{
		items: map[uuid.UUID]repo.Agent{},
	}
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {ID: projectID, Slug: "sam-blog-test"},
		},
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:         taskID,
				ProjectID:  projectID,
				TaskNumber: 11,
				Title:      "Page Build",
				WorkStatus: "draft",
			},
			task13ID: {
				ID:         task13ID,
				ProjectID:  projectID,
				TaskNumber: 13,
				Title:      "Homepage layout",
				WorkStatus: "draft",
			},
			task14ID: {
				ID:         task14ID,
				ProjectID:  projectID,
				TaskNumber: 14,
				Title:      "Blog index layout",
				WorkStatus: "draft",
			},
		},
	}

	snapshot, err := fixture.engine.loadProjectBootstrapResumeSnapshot(context.Background(), projectID, projectBootstrapState{
		ValidationFailureClass:  projectBootstrapFailureCompoundParent,
		ValidationFailureReason: "kickoff validation failed: task 11 (Page Build) is still a broad parent workstream and must be split into bounded executable child tasks before bootstrap can complete",
	})
	if err != nil {
		t.Fatalf("loadProjectBootstrapResumeSnapshot: %v", err)
	}
	if !strings.Contains(snapshot.RepairTaskLine, "task 13") || !strings.Contains(snapshot.RepairTaskLine, "task 14") {
		t.Fatalf("RepairTaskLine = %q, want top-level repair targets", snapshot.RepairTaskLine)
	}
}

func TestBuildProjectBootstrapResumeActionPromptStartsWithPersistAfterTaskTree(t *testing.T) {
	prompt := buildProjectBootstrapResumeActionPrompt(projectBootstrapState{
		BootstrapTaskID:          uuid.NewString(),
		BootstrapTaskOutstanding: true,
		AssignmentCount:          4,
		PlannedTaskCount:         18,
		CurrentPhase:             projectBootstrapCheckpointTaskTreePersisted,
		LastSuccessfulCheckpoint: projectBootstrapCheckpointTaskTreePersisted,
	})
	if !strings.Contains(prompt, "Your first tool call in this resume turn should be bootstrap.setup.persist") {
		t.Fatalf("prompt = %q, want persist-first guidance after task tree", prompt)
	}
	if !strings.Contains(prompt, "Record any already-complete checklist steps before reading more tasks, templates, or scaffold artifacts") {
		t.Fatalf("prompt = %q, want no-reread-first guidance after task tree", prompt)
	}
}

func TestShouldStopAfterBootstrapPersistWhenToolResultCompletesChecklist(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	projectID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status": projectBootstrapStatusActive,
		},
	})
	if err != nil {
		t.Fatalf("marshal session metadata: %v", err)
	}
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = projectID
	fixture.session.Metadata = metadata
	fixture.engine.projects = &fakeProjectRepo{
		items: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"project_bootstrap":{"status":"active"}}`),
			},
		},
	}

	stop, err := fixture.engine.shouldStopAfterBootstrapPersist(context.Background(), &turnRuntime{
		session: fixture.session,
	}, []ToolResult{{
		Name: "bootstrap.setup.persist",
		Output: map[string]any{
			"setup_checklist_complete": true,
		},
	}})
	if err != nil {
		t.Fatalf("shouldStopAfterBootstrapPersist err = %v, want nil", err)
	}
	if !stop {
		t.Fatal("shouldStopAfterBootstrapPersist stop = false, want true")
	}
}

func TestContinuationTurnReloadsSessionBeforeBootstrapResumeState(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "task_tree_persisted",
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 1 {
			fixture.chat.session.Metadata = metadata
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 after session reload", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want refreshed PM line", msg.Content)
			}
			return
		}
	}
	t.Fatal("project bootstrap resume message missing after session reload")
}

func TestContinuationTurnAppendsCompactBootstrapActionPromptAfterCompression(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	metadata, err := json.Marshal(map[string]any{
		projectBootstrapMetadataKey: map[string]any{
			"status":                      projectBootstrapStatusActive,
			"current_phase":               "first_wave_executions_created",
			"last_successful_checkpoint":  projectBootstrapCheckpointFirstWaveSelected,
			"assignment_count":            2,
			"planned_task_count":          9,
			"planned_flow_template_count": 1,
			"first_wave_task_count":       4,
		},
	})
	if err != nil {
		t.Fatalf("Marshal metadata: %v", err)
	}

	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM", AgentClass: "staff", AgentType: "pm"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz", AgentClass: "temp", AgentType: "general"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	messageMetadata, err := json.Marshal(map[string]any{
		"source":                       projectBootstrapSource,
		"auto_continue":                true,
		"bootstrap_initial_message_id": fixture.userMessageID.String(),
	})
	if err != nil {
		t.Fatalf("Marshal message metadata: %v", err)
	}
	if _, err := fixture.messages.UpdateMetadata(context.Background(), fixture.userMessageID, messageMetadata); err != nil {
		t.Fatalf("UpdateMetadata user message: %v", err)
	}
	fixture.chat.session.Metadata = metadata
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	var secondHistoryStart *uuid.UUID
	var actionMessageID uuid.UUID
	var resumeMessageID uuid.UUID
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call == 2 && input.HistoryStartID != nil {
			copied := *input.HistoryStartID
			secondHistoryStart = &copied
		}
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if secondHistoryStart == nil {
		t.Fatal("second assemble HistoryStartID is nil, want resume-rooted bootstrap continuation")
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			resumeMessageID = msg.ID
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want PM line", msg.Content)
			}
			continue
		}
		if strings.Contains(msg.Content, "Continue the active project bootstrap from the persisted state above.") {
			actionMessageID = msg.ID
			if !strings.Contains(msg.Content, "Do not restate the project state or re-read scaffold artifacts") {
				t.Fatalf("action prompt = %q, want anti-reread guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not call task.list, flow.list_templates, or file.read on scaffold planning artifacts") {
				t.Fatalf("action prompt = %q, want tool-specific anti-reread guidance", msg.Content)
			}
			if !strings.Contains(msg.Content, "Do not ask the user what they want. Continue the bootstrap workflow now.") {
				t.Fatalf("action prompt = %q, want direct bootstrap action guidance", msg.Content)
			}
		}
	}
	if actionMessageID == uuid.Nil {
		t.Fatal("compact bootstrap action prompt missing after compressed auto-continuation")
	}
	if resumeMessageID == uuid.Nil {
		t.Fatal("bootstrap resume message missing after compressed auto-continuation")
	}
	if *secondHistoryStart != resumeMessageID && *secondHistoryStart != actionMessageID {
		t.Fatalf("second assemble HistoryStartID = %s, want bootstrap resume %s or action prompt %s", *secondHistoryStart, resumeMessageID, actionMessageID)
	}
}

func TestContinuationTurnSynthesizesBootstrapResumeStateWhenMetadataIsStale(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
	fixture.chat.session.ScopeType = fixture.session.ScopeType
	fixture.chat.session.ScopeID = fixture.session.ScopeID

	pmID := uuid.New()
	workerID := uuid.New()
	baseAgents := fixture.engine.agents.(*fakeAgentRepo)
	fixture.engine.agents = &fakeAgentRepo{
		agent:   baseAgents.agent,
		starter: append([]repo.Agent(nil), baseAgents.starter...),
		items: map[uuid.UUID]repo.Agent{
			pmID:     {ID: pmID, DisplayName: "Sam.blog PM"},
			workerID: {ID: workerID, DisplayName: "Maya Ortiz"},
		},
	}
	fixture.engine.assignments = &fakeAssignmentRepo{
		list: []repo.AgentProjectAssignment{
			{ProjectID: fixture.session.ScopeID, AgentID: pmID, Role: "project_manager", IsActive: true},
			{ProjectID: fixture.session.ScopeID, AgentID: workerID, Role: "worker", IsActive: true},
		},
	}
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{Content: "should not be used"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		return ModelResponse{Content: "done"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if fixture.model.continuationSummaryCalls != 0 {
		t.Fatalf("continuation summary calls = %d, want 0 with synthesized bootstrap resume", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			if !strings.Contains(msg.Content, "Active project id: "+fixture.session.ScopeID.String()) {
				t.Fatalf("resume message = %q, want project id line", msg.Content)
			}
			if !strings.Contains(msg.Content, "Existing PM: Sam.blog PM") {
				t.Fatalf("resume message = %q, want synthesized PM line", msg.Content)
			}
			return
		}
	}
	t.Fatal("project bootstrap resume message missing with stale metadata")
}

func TestResolveModelProfileWorkerDefaultsToStandardWithoutOverrides(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.resolver = nil
	agentRepo := fixture.engine.agents.(*fakeAgentRepo)
	agentRepo.agent.AgentType = "worker"

	profile, err := fixture.engine.resolveModelProfile(context.Background(), fixture.session, agentRepo.agent, "agent_turn", 0, false)
	if err != nil {
		t.Fatalf("resolveModelProfile: %v", err)
	}
	if profile.LogicalProfileID != "standard" {
		t.Fatalf("logical_profile_id = %q, want %q", profile.LogicalProfileID, "standard")
	}
}

func TestWorkerModelEscalatesToHighCapabilityAfterTransientRetry(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.engine.resolver = nil
	agentRepo := fixture.engine.agents.(*fakeAgentRepo)
	agentRepo.agent.AgentType = "worker"

	streamProfiles := make([]string, 0, 2)
	attempt := 0
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		streamProfiles = append(streamProfiles, strings.TrimSpace(req.Profile.LogicalProfileID))
		attempt++
		if attempt == 1 {
			return ModelResponse{}, errors.New("rate limit exceeded")
		}
		return ModelResponse{Content: "ok"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(streamProfiles) < 2 {
		t.Fatalf("stream profile calls = %v, want at least 2 calls", streamProfiles)
	}
	if streamProfiles[0] != "standard" {
		t.Fatalf("first stream profile = %q, want %q", streamProfiles[0], "standard")
	}
	if streamProfiles[1] != "high-capability" {
		t.Fatalf("second stream profile = %q, want %q", streamProfiles[1], "high-capability")
	}
}

func TestContinuationTurnRecoversWhenTurnAlreadyCompleted(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var completeErr error
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || completeErr != nil {
			return
		}
		completeErr = fixture.chat.CompleteTurn(context.Background(), input.TurnID)
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
	if completeErr != nil {
		t.Fatalf("external completion err = %v, want nil", completeErr)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "completed" {
		t.Fatalf("last turn status = %v, want completed", last)
	}
}

func TestContinuationTurnRecoversWhenTurnAlreadyCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.assembler.results = []assembleResult{
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: prompt.ErrContextCompressed},
		{prompt: &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "x"}}, TotalTokens: 10}, err: nil},
	}

	var cancelErr error
	fixture.assembler.onAssemble = func(input prompt.AssemblyInput, call int) {
		if call != 1 || cancelErr != nil {
			return
		}
		cancelErr = fixture.chat.CancelTurn(context.Background(), input.TurnID, "steer_turn")
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
	if cancelErr != nil {
		t.Fatalf("external cancellation err = %v, want nil", cancelErr)
	}
	if len(fixture.chat.turnOrder) < 2 {
		t.Fatalf("turn count = %d, want >= 2", len(fixture.chat.turnOrder))
	}
	first := fixture.chat.turnByID(fixture.chat.turnOrder[0])
	if first == nil || first.Status != "cancelled" {
		t.Fatalf("first turn status = %v, want cancelled", first)
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "completed" {
		t.Fatalf("last turn status = %v, want completed", last)
	}
}

func TestHandleUserMessageIgnoresImmutableAssistantWriteAfterTurnCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.messages.updateContentFn = func(ctx context.Context, id uuid.UUID, content string) (repo.ChatMessage, error) {
		turnID := fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1]
		if err := fixture.chat.CancelTurn(context.Background(), turnID, "steer_turn"); err != nil {
			t.Fatalf("CancelTurn: %v", err)
		}
		return repo.ChatMessage{}, repo.ErrMessageContentImmutable
	}
	fixture.model.completeFn = func(ctx context.Context, req ModelRequest) (ModelResponse, error) {
		if req.Purpose == "listening_eval" {
			return ModelResponse{Content: "respond"}, nil
		}
		return ModelResponse{}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if err := onChunk("#"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "# draft"}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}
	if len(fixture.chat.turnOrder) == 0 {
		t.Fatal("expected at least one turn")
	}
	last := fixture.chat.turnByID(fixture.chat.turnOrder[len(fixture.chat.turnOrder)-1])
	if last == nil || last.Status != "cancelled" {
		t.Fatalf("last turn status = %v, want cancelled", last)
	}
}

func TestHandleUserMessageCarriesRunAttributionIntoInvocationAndModelRequest(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	runID := uuid.New()
	runStepID := uuid.New()
	runAttemptID := uuid.New()

	metadata, err := json.Marshal(map[string]any{
		"run_id":         runID.String(),
		"run_step_id":    runStepID.String(),
		"run_attempt_id": runAttemptID.String(),
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	message.Metadata = metadata
	fixture.messages.upsert(message)

	fixture.model.completeFn = func(context.Context, ModelRequest) (ModelResponse, error) {
		return ModelResponse{Content: "respond"}, nil
	}
	fixture.model.streamFn = func(ctx context.Context, req ModelRequest, onChunk func(token string) error) (ModelResponse, error) {
		if req.InvocationID == nil || *req.InvocationID == uuid.Nil {
			t.Fatalf("req.InvocationID = %v, want non-nil", req.InvocationID)
		}
		if req.RunID == nil || *req.RunID != runID {
			t.Fatalf("req.RunID = %v, want %s", req.RunID, runID)
		}
		if req.RunStepID == nil || *req.RunStepID != runStepID {
			t.Fatalf("req.RunStepID = %v, want %s", req.RunStepID, runStepID)
		}
		if req.RunAttemptID == nil || *req.RunAttemptID != runAttemptID {
			t.Fatalf("req.RunAttemptID = %v, want %s", req.RunAttemptID, runAttemptID)
		}
		if err := onChunk("ok"); err != nil {
			return ModelResponse{}, err
		}
		return ModelResponse{Content: "ok", Usage: &ModelUsage{InputTokens: 12, OutputTokens: 2}}, nil
	}

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	if len(fixture.invocations.creates) == 0 {
		t.Fatal("expected at least one invocation create")
	}
	created := fixture.invocations.creates[0]
	if created.RunID == nil || *created.RunID != runID {
		t.Fatalf("created.RunID = %v, want %s", created.RunID, runID)
	}
	if created.RunStepID == nil || *created.RunStepID != runStepID {
		t.Fatalf("created.RunStepID = %v, want %s", created.RunStepID, runStepID)
	}
	if created.RunAttemptID == nil || *created.RunAttemptID != runAttemptID {
		t.Fatalf("created.RunAttemptID = %v, want %s", created.RunAttemptID, runAttemptID)
	}
}

func TestHandleUserMessageBlocksRepeatedToolValidationFailures(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				Title:           "Write file",
				WorkStatus:      "in_progress",
				AssignedAgentID: &assignedAgentID,
				Metadata:        json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	blocker := &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = blocker
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
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

	if err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID); err != nil {
		t.Fatalf("HandleUserMessage: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if taskRecord.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", taskRecord.WorkStatus)
	}

	guard, ok := parseTaskValidationGuard(taskRecord.Metadata)
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
	if len(blocker.calls) != 1 {
		t.Fatalf("blocker calls = %d, want 1", len(blocker.calls))
	}
	if !strings.Contains(blocker.calls[0].reason, "file.write") {
		t.Fatalf("block reason = %q, want contains file.write", blocker.calls[0].reason)
	}
	if fixture.chat.failCalls != 0 {
		t.Fatalf("failCalls = %d, want 0", fixture.chat.failCalls)
	}
	if fixture.chat.completeCalls != 1 {
		t.Fatalf("completeCalls = %d, want 1", fixture.chat.completeCalls)
	}
	if !fixture.messages.containsContentSubstring("validation loop blocked") {
		t.Fatal("expected validation loop system message")
	}
}

func TestHandleUserMessageFailsWhenValidationLoopBlockLacksTaskTransitions(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:              taskID,
				OrganizationID:  fixture.session.OrganizationID,
				ProjectID:       projectID,
				Title:           "Write file",
				WorkStatus:      "in_progress",
				AssignedAgentID: &assignedAgentID,
				Metadata:        json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = nil
	fixture.engine.toolResolver = &fakeToolResolver{tools: []tools.ToolDescriptor{{Name: "file.write", Tier: "tier2"}}}
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

	err := fixture.engine.HandleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID)
	if err == nil || !strings.Contains(err.Error(), errMissingTaskTransitionServiceForValidationBlock) {
		t.Fatalf("HandleUserMessage error = %v, want contains %q", err, errMissingTaskTransitionServiceForValidationBlock)
	}

	taskRecord, getErr := taskRepo.GetByID(context.Background(), taskID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
}

func TestHandleUserMessageSkipsBlockedValidationLoop(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.session.Mode = "async"

	guard := taskValidationGuardState{
		InitialMessageID:   fixture.userMessageID.String(),
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
	}
	metadata, err := mergeTaskValidationGuardMetadata(json.RawMessage(`{"existing":"value"}`), guard)
	if err != nil {
		t.Fatalf("mergeTaskValidationGuardMetadata: %v", err)
	}
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      uuid.New(),
				Title:          "Blocked task",
				WorkStatus:     "blocked",
				Metadata:       metadata,
			},
		},
	}

	if err := fixture.engine.handleUserMessage(context.Background(), fixture.session.ID, fixture.userMessageID, nil, 0, nil); err != nil {
		t.Fatalf("handleUserMessage: %v", err)
	}
	if got := len(fixture.chat.turnOrder); got != 0 {
		t.Fatalf("created turns = %d, want 0", got)
	}

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID: fixture.session.ID,
		MessageID: fixture.userMessageID,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if enqueued {
		t.Fatal("expected enqueue to be suppressed")
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent turn jobs = %d, want 0", len(jobs))
	}
}

func TestEnsureRecoveryTurnDurableTaskStateFailsWithoutTaskTransitions(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      projectID,
				Title:          "Recovery blocked task",
				WorkStatus:     "in_progress",
				Metadata:       json.RawMessage(`{"existing":"value"}`),
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = nil

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID: uuid.New(),
		},
		recoveryBlockReason: "provider auth failed",
	}

	err := fixture.engine.ensureRecoveryTurnDurableTaskState(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), errMissingTaskTransitionServiceForRecoveryBlock) {
		t.Fatalf("ensureRecoveryTurnDurableTaskState error = %v, want contains %q", err, errMissingTaskTransitionServiceForRecoveryBlock)
	}

	taskRecord, getErr := taskRepo.GetByID(context.Background(), taskID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
}

func TestEnqueueAgentTurnIfActiveSuppressesRepeatedRecoveryRetryForSameMessage(t *testing.T) {
	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID
	fixture.engine.tasks = &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: fixture.session.OrganizationID,
				ProjectID:      uuid.New(),
				WorkStatus:     "review",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}

	agentID := fixture.chat.participants[0].ParticipantID
	turnRecord, _, err := fixture.engine.turns.CreateForMessageAttempt(context.Background(), fixture.session.ID, agentID, fixture.userMessageID, 4)
	if err != nil {
		t.Fatalf("CreateForMessageAttempt: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	stopReason := stopReasonRecoveryFileRejected
	if _, err := fixture.engine.turns.SetStopReason(context.Background(), turnRecord.ID, &stopReason); err != nil {
		t.Fatalf("SetStopReason: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turnRecord.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	enqueued, err := fixture.engine.enqueueAgentTurnIfActive(context.Background(), fixture.session, AgentTurnPayload{
		SessionID:  fixture.session.ID,
		MessageID:  fixture.userMessageID,
		RetryCount: 5,
	}, nil)
	if err != nil {
		t.Fatalf("enqueueAgentTurnIfActive: %v", err)
	}
	if enqueued {
		t.Fatal("expected enqueue to be suppressed for completed recovery halt")
	}
	if jobs := fixture.enqueuer.agentTurnJobs(); len(jobs) != 0 {
		t.Fatalf("agent turn jobs = %d, want 0", len(jobs))
	}
}

func TestCancelRecoveryResumeDispatchMarksInitialMessageCancelled(t *testing.T) {
	fixture := newUnitFixture(t, "async")

	rt := &turnRuntime{
		initialMessageID: fixture.userMessageID,
	}
	if err := fixture.engine.cancelRecoveryResumeDispatch(context.Background(), rt, "recovery halted after repeated non-substantive drafts"); err != nil {
		t.Fatalf("cancelRecoveryResumeDispatch: %v", err)
	}

	message, err := fixture.messages.GetByID(context.Background(), fixture.userMessageID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !chat.AgentTurnDispatchCancelled(message.Metadata) {
		t.Fatalf("expected cancelled dispatch metadata, got %s", string(message.Metadata))
	}
}

func TestRecoveryFileWriteDraftRejectReason(t *testing.T) {
	const targetPath = "docs/content-strategy.md"

	cases := []struct {
		name       string
		content    string
		targetPath string
		want       string
	}{
		{
			name: "rejects tool repair narration",
			content: "I can see the problem clearly from the conversation history. " +
				"Every `file_write` call has been emitted without the `content` parameter populated. " +
				"Let me do this now.",
			want: "tool-recovery troubleshooting",
		},
		{
			name: "rejects intent narration placeholder",
			content: "Now I have everything I need. Let me write the comprehensive content strategy document. " +
				"This needs to be the single deliverable that unblocks WS4 and serves as the strategic foundation for Sam.blog.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects shallow placeholder progress narration",
			content: "Time to write the comprehensive content strategy document for Sam.blog. " +
				"This is the critical deliverable that unblocks WS4. " +
				"The next step is to capture the audience, pillars, tone, and publishing cadence so the project can move forward.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovery context placeholder narration",
			content: "Excellent. I now have the full strategy context. " +
				"The recovery state indicates I should write the actual migration plan deliverable (the main output document). " +
				"Let me check what target file was specified:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects complete picture placeholder narration",
			content: "Excellent! Now I have a complete picture. " +
				"The draft document exists but is incomplete. " +
				"Let me resume work by completing the accessibility standards document. " +
				"I'll continue from where it was cut off and finish all remaining sections:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects task context placeholder narration",
			content: "Perfect! Now I understand the task context and I need to resume OC-28 correctly. " +
				"Let me now create the comprehensive accessibility standards document that unblocks the design system workstream.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects full context workspace check placeholder narration",
			content: "Perfect. Now I have the full context. The OC-15 strategy work is complete with four locked artifacts. " +
				"Now I need to create the actual content migration plan deliverable. Let me check what's in the workspace currently and then write the migration plan document:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects resume by completing placeholder narration",
			content: "Perfect. Now I have full context. Let me resume the task by completing the migration plan document. " +
				"Based on the strategy artifacts and the checkpoint directive, I need to write a comprehensive migration plan that operationalizes these decisions.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects recovered full context planning placeholder narration",
			content: "Perfect. I have recovered the full context. The planning artifacts are complete and comprehensive. " +
				"Now I need to create the infrastructure spec and deployment checklist. Let me draft the comprehensive infrastructure specification:",
			want: "intent to write the deliverable",
		},
		{
			name:    "rejects placeholder file reread narration",
			content: "The target file contains only a placeholder stub. I need to read the checkpoint artifacts to understand what content model and editorial structure has been defined, then write the substantive migration plan.",
			want:    "intent to write the deliverable",
		},
		{
			name:    "rejects recovery artifact reread narration",
			content: "I'll read the target file and recovery artifact to assess the current state, then write the substantive migration plan content.",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects structured context summary placeholder narration",
			content: "Excellent. Now I have enough context. The strategy for OC-15 is locked with:\n" +
				"- **Staged cutover** (Tier 1 -> Tier 2 -> Archives)\n" +
				"- **Tier classification** based on voice audit (OC-13)\n" +
				"- **Extraction -> Transformation -> Validation** workflow\n" +
				"- **Markdown + YAML front-matter** target format\n" +
				"- **301 redirects** for SEO preservation\n\n" +
				"Now I'll write the comprehensive migration plan document. This is the deliverable that translates strategy into executability.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects generic recovery question echo",
			content: "I'm ready to help with the SAM.blog rebuild infrastructure task (OC-24).\n\n" +
				"I can see this is a durable recovery checkpoint with the success narrative partially drafted. What would you like me to do?\n\n" +
				"**Quick clarification:**\n" +
				"- Continue drafting the success narrative (complete the cutover scenario)?\n" +
				"- Move to the decision log or tradeoff matrix?\n" +
				"- Read existing planning artifacts to ground the infrastructure spec?\n\n" +
				"Please let me know the most useful next step.",
			want: "generic recovery reply",
		},
		{
			name: "rejects structured status report placeholder narration",
			content: "Perfect! I can see:\n" +
				"- **Current flow node:** 8cc3df4d (Work)\n" +
				"- **Status:** in_progress\n" +
				"- **Strategy artifacts:** All 4 required artifacts are present and locked\n" +
				"- **Target file:** planning/prd-spec/oc-24-infrastructure-spec.md (partially drafted)\n\n" +
				"The task is in the **Work phase**. I need to complete the infrastructure spec file. Let me check the full file to see what sections still need content:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects locked decisions task flow placeholder narration",
			content: "Perfect. I have all four strategy artifacts. The decisions are locked and clear. " +
				"Now I need to move forward to the infrastructure spec deliverable. " +
				"Let me check the task flow and understand what step we're on:",
			want: "generic recovery reply",
		},
		{
			name: "rejects drafting confirmation placeholder narration",
			content: "I'm ready to continue on OC-24: Plan hosting and infrastructure. " +
				"Before I proceed with drafting the infrastructure spec, I need to confirm the locked decisions are still valid.",
			want: "generic recovery reply",
		},
		{
			name: "rejects current state examination placeholder narration",
			content: "I'll help you resume work on OC-24: Plan hosting and infrastructure. " +
				"Let me first examine the current state of the project and task.",
			want: "generic recovery reply",
		},
		{
			name: "rejects full context acceptance criteria placeholder narration",
			content: "Excellent! Now I understand the full context. I need to complete OC-28 by writing the comprehensive accessibility standards document. " +
				"Let me check the acceptance criteria and dependency log as well:",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects work node execution placeholder narration",
			content: "Good. I have the context. OC-15 (content migration) is in the Work node and needs execution. " +
				"Now write the migration plan:",
			want: "intent to write the deliverable",
		},
		{
			name:    "rejects short imperative write stub",
			content: "Now write the full migration plan:",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects structured checkpoint list placeholder",
			content: "Good. I have the context. OC-15 (content migration) is in the **Work** node and needs execution. The checkpoint indicates I should:\n\n" +
				"1. **Not** re-list workspace state or create helper scripts (they exist)\n" +
				"2. **Read** persisted artifacts directly\n" +
				"3. **Write at least one migrated output file** before expanding\n" +
				"4. Start from the recovery checkpoints and target file draft\n\n" +
				"Let me read the OC-15 strategy artifacts to understand the editorial direction, then write the first complete output file: the migration plan.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects structured locked decisions placeholder",
			content: "Perfect. I have all four strategy artifacts. The decisions are **locked and clear**:\n\n" +
				"- **Hosting:** Vercel Pro\n" +
				"- **CDN:** Vercel's integrated global CDN\n" +
				"- **DNS:** Vercel DNS\n\n" +
				"Now I need to move forward to the **infrastructure spec** deliverable. Let me check the task flow and understand what step we're on:",
			want: "instead of the file body",
		},
		{
			name: "rejects complete context execution placeholder",
			content: "Excellent. Now I have the complete context. The strategy phase for OC-15 is complete with all 4 artifacts locked. " +
				"Now I need to read the tradeoff matrix to understand the full decision context, then resume execution by creating the actual migration plan document.",
			want: "intent to write the deliverable",
		},
		{
			name: "rejects flow state status placeholder",
			content: "The task shows `current_flow_node_id: 8cc3df4d-91c3-4af7-9b45-a15adf0054dc` but the execution doesn't exist yet. " +
				"This means the task is in progress but hasn't yet entered the active flow node execution state.\n\n" +
				"Let me check what flow template is assigned:",
			want: "runtime status analysis instead of the file body",
		},
		{
			name: "rejects flow execution id request placeholder",
			content: "I need the flow node execution ID to continue. Can you provide the flow_node_execution_id for the current task node?\n\n" +
				"Alternatively, if you'd like me to check the current task state, I can retrieve OC-24 and determine the active flow node:",
			want: "runtime control-plane input instead of the file body",
		},
		{
			name: "rejects operator choice question placeholder",
			content: "I'm Alex, Technical Lead for the SAM.blog rebuild. I'm actively working on **OC-24: Plan hosting and infrastructure**, currently in the Work phase.\n\n" +
				"What is the current state you need me to continue from?\n\n" +
				"- Do you want me to complete the deployment checklist that was partially drafted?\n" +
				"- Or do you need me to start fresh on the full infrastructure specification?\n\n" +
				"The recovery context shows the deployment checklist draft exists, but I should confirm whether that's the priority.",
			want: "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects recovery context reread placeholder",
			content: "Perfect. I now have the recovery context. Let me check the strategy artifacts to understand the locked decisions before proceeding:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects durable draft preface placeholder",
			content: "I'll write the deployment checklist to the target file now using the substantive draft provided above.",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name: "rejects continuation summary spec preface",
			content: "I'll continue the OC-24 infrastructure planning task by writing the concrete deliverables directly based on the continuation summary draft.\n\n" +
				"Let me write the infrastructure specification first:",
			want: "intent to write the deliverable",
		},
		{
			name:    "rejects deployment checklist progress preface",
			content: "Good. The spec file is partially complete. Now I need to write the complete deployment checklist, which is the critical missing deliverable. Let me create that:",
			want:    "intent to write the deliverable",
		},
		{
			name:    "rejects migration plan status placeholder",
			content: "Perfect! Now I see the situation clearly:\n\n1. **OC-15 has strategy artifacts already locked** (strategy brief, decision log, tradeoff matrix, success narrative)\n2. **The migration plan file is stubbed but incomplete**\n3. **I need to deliver the full migration plan** based on the locked strategy\n\nLet me read the strategy artifacts first, then write the complete migration plan:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects replace stub imperative",
			content: "Now let me replace it with the complete migration plan:",
			want:    "asked the operator to choose the next step instead of the file body",
		},
		{
			name:    "rejects delete recreate imperative",
			content: "Let me try a different approach - delete and recreate:",
			want:    "intent to write the deliverable",
		},
		{
			name: "rejects implementation preface with build list",
			content: "Since the prior tasks are still in draft and I don't have the data schema yet, I'll design the reporting script based on the task description requirements and best practices for pipeline analytics. Let me create a comprehensive, well-structured reporting script that's ready to integrate with upstream data pipelines.\n\n" +
				"I'll build:\n" +
				"1. **Core reporting module** with data validation\n" +
				"2. **Report generators** for each metric type\n" +
				"3. **HTML and Markdown templates** with styling\n" +
				"4. **Example outputs** demonstrating all report types\n" +
				"5. **Clear documentation** for operators\n\n" +
				"Let me start:",
			targetPath: "src/generate_reports.py",
			want:       "implementation plan instead of the file body",
		},
		{
			name:    "accepts first-person file body",
			content: "I will write at dawn because the house is quiet and the work still matters.",
			want:    "",
		},
		{
			name: "accepts substantive draft body",
			content: `# Content Strategy

## Core Promise
Sam.blog should publish one durable operating system for thoughtful parents building resilient families and meaningful work.
`,
			want: "",
		},
		{
			name: "rejects mismatched deliverable heading",
			content: `# Core Page Structure Specification

## Overview
This specification defines the global page shell for the site.`,
			targetPath: "design/06-search-discovery-wireframes.md",
			want:       "different deliverable",
		},
		{
			name: "accepts matching deliverable heading",
			content: `# Search and Discovery Wireframes

## Overview
This document defines archive, results, and related-post wireframes.`,
			targetPath: "design/06-search-discovery-wireframes.md",
			want:       "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseTargetPath := targetPath
			if strings.TrimSpace(tc.targetPath) != "" {
				caseTargetPath = tc.targetPath
			}
			got := recoveryFileWriteDraftRejectReason(tc.content, caseTargetPath)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want contains %q", got, tc.want)
			}
			if !strings.Contains(got, caseTargetPath) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want target path", got)
			}
		})
	}
}

func TestLooksLikeRecoveryFileDraftRejectsLongConversationalLeadIn(t *testing.T) {
	t.Parallel()

	content := "I now have the strategy artifacts. These define the migration approach: staged cutover, extraction strategy, transformation to Markdown+YAML, validation gates, redirects, and rollback. I'll write the complete migration plan to the target file next."
	if looksLikeRecoveryFileDraft(content) {
		t.Fatalf("looksLikeRecoveryFileDraft(%q) = true, want false", content)
	}
}

func TestLooksLikeRecoveryFileDraftRejectsLongImperativeNarration(t *testing.T) {
	t.Parallel()

	content := "Excellent. I now have all four locked strategy artifacts for OC-15. The immediate directive is clear: write at least one migrated output file before expanding the plan.\n\nBased on the strategy, I need to write the Content Migration Plan (target file: docs/migration-plan/oc-15-content-migration-plan.md). This is the operative document that translates the locked decisions into a step-by-step execution guide.\n\nLet me write the comprehensive migration plan:"
	if looksLikeRecoveryFileDraft(content) {
		t.Fatalf("looksLikeRecoveryFileDraft(%q) = true, want false", content)
	}
}

type unitFixture struct {
	engine        *TurnEngine
	events        *fakeEventBus
	chat          *fakeChatService
	messages      *fakeMessageRepo
	invocations   *fakeInvocationRepo
	model         *fakeModelGateway
	dispatcher    *fakeDispatcher
	flowAdvancer  *fakeFlowAdvancer
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
	agentRecord := repo.Agent{ID: agentID, OrganizationID: orgID, DisplayName: "Frank"}

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
		messages:      messages,
		turns:         map[uuid.UUID]*chat.ChatTurn{},
		turnCh:        make(chan uuid.UUID, 4),
		enforceStatus: true,
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
	taskRepo := &fakeTaskRepo{}
	taskTransitions := &fakeTaskTransitionService{repo: taskRepo}
	flowAdvancer := &fakeFlowAdvancer{tasks: taskRepo}

	engine, err := NewEngine(Options{
		Chat:            chatSvc,
		ToolResolver:    toolResolver,
		Assembler:       assembler,
		Summarization:   &fakeSummarizationChecker{},
		ModelGateway:    modelGateway,
		Dispatcher:      dispatcher,
		RunCanceler:     &fakeRunCanceler{},
		Events:          events,
		Enqueuer:        enqueuer,
		Invocations:     invocations,
		ModelProfiles:   profiles,
		Profiles:        resolver,
		Messages:        messages,
		Turns:           chatSvc,
		Sessions:        chatSvc,
		Agents:          &fakeAgentRepo{agent: agentRecord, starter: []repo.Agent{agentRecord}},
		Tasks:           taskRepo,
		TaskTransitions: taskTransitions,
		Projects:        &fakeProjectRepo{},
		FlowNodes:       &fakeFlowNodeRepo{},
		FlowAdvancer:    flowAdvancer,
		MemorySources:   memSources,
		Memories:        memories,
		Now:             func() time.Time { return time.Now().UTC() },
		Sleep:           func(context.Context, time.Duration) error { return nil },
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
		invocations:   invocations,
		model:         modelGateway,
		dispatcher:    dispatcher,
		flowAdvancer:  flowAdvancer,
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
	enforceStatus bool
	completeNoop  bool
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

func (f *fakeChatService) CreateSession(_ context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error) {
	session := chat.ChatSession{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		ScopeType:      input.ScopeType,
		ScopeID:        input.ScopeID,
		Mode:           input.Mode,
		Status:         "active",
		Metadata:       input.Metadata,
	}
	return &session, nil
}

func (f *fakeChatService) CreateForMessageAttempt(ctx context.Context, sessionID, agentID, messageID uuid.UUID, retryCount int) (repo.ChatTurn, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if retryCount < 0 {
		retryCount = 0
	}
	for _, turnID := range f.turnOrder {
		turn := f.turns[turnID]
		if turn == nil || turn.SessionID != sessionID {
			continue
		}
		if turn.TriggerMessageID != nil && *turn.TriggerMessageID == messageID && turn.RetryCount == retryCount {
			return repo.ChatTurn(*turn), false, nil
		}
	}

	cycleID := uuid.New()
	triggerMessageID := messageID
	turnID := uuid.New()
	turn := &chat.ChatTurn{
		ID:               turnID,
		SessionID:        sessionID,
		TurnNumber:       len(f.turnOrder) + 1,
		CycleID:          &cycleID,
		RespondingType:   "agent",
		RespondingID:     agentID,
		Status:           "pending",
		TriggerMessageID: &triggerMessageID,
		RetryCount:       retryCount,
	}
	f.turns[turnID] = turn
	f.turnOrder = append(f.turnOrder, turnID)
	f.session.CurrentTurnID = &turnID
	f.session.TurnCount++
	select {
	case f.turnCh <- turnID:
	default:
	}
	return repo.ChatTurn(*turn), true, nil
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
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "pending") {
		return chat.ErrInvalidStatusTransition
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
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
	}
	f.completeCalls++
	if f.completeNoop {
		return nil
	}
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
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
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
	if f.enforceStatus && !strings.EqualFold(strings.TrimSpace(turn.Status), "in_progress") {
		return chat.ErrInvalidStatusTransition
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
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *fakeChatService) AddParticipant(ctx context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sessionID != f.session.ID {
		return nil, repo.ErrNotFound
	}
	if participantID == uuid.Nil {
		return nil, fmt.Errorf("participant_id is required")
	}
	normalizedType := strings.TrimSpace(strings.ToLower(participantType))
	normalizedRole := strings.TrimSpace(strings.ToLower(role))
	if normalizedRole == "" {
		normalizedRole = "member"
	}
	for _, existing := range f.participants {
		if existing == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(existing.ParticipantType), normalizedType) && existing.ParticipantID == participantID {
			return nil, chat.ErrAlreadyParticipant
		}
	}
	added := &chat.ChatParticipant{
		ID:              uuid.New(),
		SessionID:       sessionID,
		ParticipantType: normalizedType,
		ParticipantID:   participantID,
		Role:            normalizedRole,
	}
	f.participants = append(f.participants, added)
	copyItem := *added
	return &copyItem, nil
}

func (f *fakeChatService) AppendMessage(ctx context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error) {
	if !strings.EqualFold(strings.TrimSpace(f.session.Status), "active") {
		return nil, chat.ErrSessionClosed
	}
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

func (f *fakeChatService) SetStopReason(ctx context.Context, id uuid.UUID, stopReason *string) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[id]
	if turn == nil {
		return repo.ChatTurn{}, repo.ErrNotFound
	}
	if stopReason == nil || strings.TrimSpace(*stopReason) == "" {
		turn.StopReason = nil
	} else {
		reason := strings.TrimSpace(*stopReason)
		turn.StopReason = &reason
	}
	return repo.ChatTurn(*turn), nil
}

func (f *fakeChatService) SetTriggerMessageID(ctx context.Context, id uuid.UUID, triggerMessageID *uuid.UUID) (repo.ChatTurn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	turn := f.turns[id]
	if turn == nil {
		return repo.ChatTurn{}, repo.ErrNotFound
	}
	if triggerMessageID == nil || *triggerMessageID == uuid.Nil {
		turn.TriggerMessageID = nil
	} else {
		copyID := *triggerMessageID
		turn.TriggerMessageID = &copyID
	}
	return repo.ChatTurn(*turn), nil
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
	mu              sync.Mutex
	items           map[uuid.UUID]repo.ChatMessage
	order           []uuid.UUID
	nextSeq         int64
	updateContentFn func(context.Context, uuid.UUID, string) (repo.ChatMessage, error)
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
	if f.updateContentFn != nil {
		return f.updateContentFn(ctx, id, content)
	}
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

func (f *fakeMessageRepo) containsContentSubstring(content string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	needle := strings.TrimSpace(content)
	for _, item := range f.items {
		if strings.Contains(strings.TrimSpace(item.Content), needle) {
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
	mu         sync.Mutex
	results    []assembleResult
	calls      int
	onAssemble func(input prompt.AssemblyInput, call int)
}

func (f *fakeAssembler) Assemble(ctx context.Context, input prompt.AssemblyInput) (*prompt.AssembledPrompt, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	hook := f.onAssemble
	if len(f.results) == 0 {
		f.mu.Unlock()
		if hook != nil {
			hook(input, call)
		}
		return &prompt.AssembledPrompt{Messages: []prompt.PromptMessage{{Role: "system", Content: "default"}}, TotalTokens: 10}, nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	f.mu.Unlock()
	if hook != nil {
		hook(input, call)
	}
	return result.prompt, result.err
}

type fakeSummarizationChecker struct {
	shouldSummarize bool
}

func (f *fakeSummarizationChecker) ShouldSummarize(context.Context, uuid.UUID, int) (bool, error) {
	return f.shouldSummarize, nil
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
	consumerName string
	orgID        *uuid.UUID
	handler      eventbus.EventHandler
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

func (f *fakeEventBus) Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, fakeSubscription{consumerName: strings.TrimSpace(consumerName), orgID: orgID, handler: handler})
	return eventbus.Subscription{}
}

func (f *fakeEventBus) Unsubscribe(_ eventbus.Subscription) {}

func (f *fakeEventBus) subscriptionNamesWithPrefix(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.subs))
	for _, sub := range f.subs {
		if !strings.HasPrefix(sub.consumerName, prefix) {
			continue
		}
		out = append(out, sub.consumerName)
	}
	return out
}

type fakeEnqueuer struct {
	mu   sync.Mutex
	jobs []enqueuedJob
}

type enqueuedJob struct {
	jobType  string
	priority int
	payload  *AgentTurnPayload
	runAfter *time.Time
}

func (f *fakeEnqueuer) Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error) {
	entry := enqueuedJob{jobType: strings.TrimSpace(jobType), priority: priority}
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
	return f.jobsByType(AgentTurnJobType)
}

func (f *fakeEnqueuer) jobsByType(jobType string) []enqueuedJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]enqueuedJob, 0)
	for _, job := range f.jobs {
		if job.jobType != jobType {
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
	mu                  sync.Mutex
	creates             []repo.ModelInvocation
	updateCompletionErr error
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
	return f.updateCompletionErr
}

type fakeModelProfileRepo struct {
	profile   repo.ModelProfile
	requested []string
}

func (f *fakeModelProfileRepo) GetCurrentByLogicalID(ctx context.Context, organizationID uuid.UUID, logicalProfileID string) (repo.ModelProfile, error) {
	if strings.TrimSpace(logicalProfileID) == "" {
		return repo.ModelProfile{}, repo.ErrNotFound
	}
	f.requested = append(f.requested, strings.TrimSpace(logicalProfileID))
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
	agent      repo.Agent
	items      map[uuid.UUID]repo.Agent
	starter    []repo.Agent
	starterErr error
}

func (f *fakeAgentRepo) GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error) {
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	if f.agent.ID != id {
		return repo.Agent{}, repo.ErrNotFound
	}
	return f.agent, nil
}

func (f *fakeAgentRepo) GetStarterTrio(context.Context, uuid.UUID) ([]repo.Agent, error) {
	if f.starterErr != nil {
		return nil, f.starterErr
	}
	return append([]repo.Agent(nil), f.starter...), nil
}

type fakeTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
	err   error
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (f *fakeTaskRepo) GetByProjectAndNumber(_ context.Context, projectID uuid.UUID, taskNumber int) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	for _, item := range f.items {
		if item.ProjectID == projectID && item.TaskNumber == taskNumber {
			return item, nil
		}
	}
	return repo.ProjectTask{}, repo.ErrNotFound
}

func (f *fakeTaskRepo) ListByProject(_ context.Context, projectID uuid.UUID, _ ...string) ([]repo.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	items := make([]repo.ProjectTask, 0, len(f.items))
	for _, item := range f.items {
		if item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TaskNumber == items[j].TaskNumber {
			return items[i].ID.String() < items[j].ID.String()
		}
		return items[i].TaskNumber < items[j].TaskNumber
	})
	return items, nil
}

func (f *fakeTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	if f.items == nil {
		f.items = map[uuid.UUID]repo.ProjectTask{}
	}
	f.items[task.ID] = task
	return task, nil
}

func (f *fakeTaskRepo) UpdateMetadata(_ context.Context, id uuid.UUID, metadata json.RawMessage) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	item.Metadata = append(json.RawMessage(nil), metadata...)
	f.items[id] = item
	return item, nil
}

type fakeTaskTransitionService struct {
	repo            *fakeTaskRepo
	calls           []blockedTaskCall
	transitionCalls []transitionTaskCall
	err             error
}

type blockedTaskCall struct {
	taskID uuid.UUID
	reason string
	actor  tasksvc.Actor
}

type transitionTaskCall struct {
	taskID   uuid.UUID
	toStatus string
	actor    tasksvc.Actor
}

func (f *fakeTaskTransitionService) TransitionStatus(_ context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	target := strings.TrimSpace(toStatus)
	f.transitionCalls = append(f.transitionCalls, transitionTaskCall{
		taskID:   taskID,
		toStatus: target,
		actor:    actor,
	})
	if f.repo != nil {
		taskRecord, err := f.repo.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = target
			updated, updateErr := f.repo.Update(context.Background(), taskRecord)
			if updateErr == nil {
				transitioned := tasksvc.ProjectTask(updated)
				return &transitioned, nil
			}
		}
	}
	transitioned := tasksvc.ProjectTask{ID: taskID, WorkStatus: target}
	return &transitioned, nil
}

func (f *fakeTaskTransitionService) TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor, _ map[string]any) (*tasksvc.ProjectTask, error) {
	return f.TransitionStatus(ctx, taskID, toStatus, actor)
}

func (f *fakeTaskTransitionService) MarkBlocked(_ context.Context, taskID uuid.UUID, reason string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls = append(f.calls, blockedTaskCall{
		taskID: taskID,
		reason: strings.TrimSpace(reason),
		actor:  actor,
	})
	if f.repo != nil {
		taskRecord, err := f.repo.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = "blocked"
			updated, updateErr := f.repo.Update(context.Background(), taskRecord)
			if updateErr == nil {
				blocked := tasksvc.ProjectTask(updated)
				return &blocked, nil
			}
		}
	}
	blocked := tasksvc.ProjectTask{ID: taskID, WorkStatus: "blocked"}
	return &blocked, nil
}

type fakeFlowNodeRepo struct {
	items map[uuid.UUID]repo.FlowNode
	err   error
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if f.err != nil {
		return repo.FlowNode{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.FlowNode{}, repo.ErrNotFound
}

type fakeFlowAdvancer struct {
	tasks                 *fakeTaskRepo
	recordNodeCommitCalls int
	advanceFlowCalls      int
	lastCommitSHA         string
	lastAdvanceActor      flowsvc.Actor
}

func (f *fakeFlowAdvancer) RecordNodeCommit(_ context.Context, taskID uuid.UUID, commitSHA, _ string) (*repo.FlowNodeExecution, error) {
	f.recordNodeCommitCalls++
	f.lastCommitSHA = strings.TrimSpace(commitSHA)
	commit := f.lastCommitSHA
	return &repo.FlowNodeExecution{TaskID: taskID, CommitSHA: &commit}, nil
}

func (f *fakeFlowAdvancer) AdvanceFlow(_ context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	f.advanceFlowCalls++
	f.lastAdvanceActor = actor
	if f.tasks != nil {
		taskRecord, err := f.tasks.GetByID(context.Background(), taskID)
		if err == nil {
			taskRecord.WorkStatus = "review"
			if _, updateErr := f.tasks.Update(context.Background(), taskRecord); updateErr != nil {
				return nil, updateErr
			}
		}
	}
	return &repo.FlowNodeExecution{TaskID: taskID, Status: "completed"}, nil
}

type fakeAssignmentRepo struct {
	items map[uuid.UUID]repo.AgentProjectAssignment
	list  []repo.AgentProjectAssignment
	err   error
}

func (f *fakeAssignmentRepo) GetPM(_ context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return repo.AgentProjectAssignment{}, f.err
	}
	if item, ok := f.items[projectID]; ok {
		return item, nil
	}
	return repo.AgentProjectAssignment{}, repo.ErrNotFound
}

func (f *fakeAssignmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.AgentProjectAssignment, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.list) > 0 {
		items := make([]repo.AgentProjectAssignment, 0, len(f.list))
		for _, item := range f.list {
			if item.ProjectID == projectID {
				items = append(items, item)
			}
		}
		return items, nil
	}
	items := make([]repo.AgentProjectAssignment, 0, len(f.items))
	for _, item := range f.items {
		if item.ProjectID == projectID {
			items = append(items, item)
		}
	}
	return items, nil
}

type fakeProjectRepo struct {
	items map[uuid.UUID]repo.Project
	err   error
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if f.err != nil {
		return repo.Project{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

type fakeOrganizationRepo struct {
	items map[uuid.UUID]repo.Organization
	err   error
}

func (f *fakeOrganizationRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Organization, error) {
	if f.err != nil {
		return repo.Organization{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Organization{}, repo.ErrNotFound
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

func createCompletedTurnWithAssistantMessage(t *testing.T, fixture *unitFixture, agentID uuid.UUID, assistantContent string) uuid.UUID {
	t.Helper()

	turn, err := fixture.chat.CreateTurn(context.Background(), fixture.session.ID, agentID)
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	if err := fixture.chat.StartTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if err := fixture.chat.CompleteTurn(context.Background(), turn.ID); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	authorType := "agent"
	fixture.messages.create(repo.ChatMessage{
		SessionID:  fixture.session.ID,
		TurnID:     &turn.ID,
		AuthorType: &authorType,
		AuthorID:   &agentID,
		Role:       "assistant",
		Status:     "final",
		Content:    assistantContent,
	})
	return turn.ID
}

func appendToolResultMessage(t *testing.T, fixture *unitFixture, turnID uuid.UUID, payload map[string]any) {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal tool result: %v", err)
	}
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content:   string(raw),
	})
}

func TestShouldSuppressAutoContinuationForStopReasonIncludesRecoveryFallback(t *testing.T) {
	t.Parallel()

	recoveryFileRejected := stopReasonRecoveryFileRejected
	legacyFallback := stopReasonRecoveryFileFallback
	validationBlocked := stopReasonValidationBlocked

	tests := []struct {
		name       string
		stopReason *string
		want       bool
	}{
		{name: "nil", stopReason: nil, want: false},
		{name: "preferred recovery file stop reason", stopReason: &recoveryFileRejected, want: true},
		{name: "legacy recovery fallback stop reason", stopReason: &legacyFallback, want: true},
		{name: "validation blocked", stopReason: &validationBlocked, want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldSuppressAutoContinuationForStopReason(tc.stopReason); got != tc.want {
				t.Fatalf("shouldSuppressAutoContinuationForStopReason(%v) = %t, want %t", tc.stopReason, got, tc.want)
			}
		})
	}
}

func TestCompleteTurnNoOpForFailedTurn(t *testing.T) {
	t.Parallel()

	turnID := uuid.New()
	sessionID := uuid.New()
	taskID := uuid.New()
	fakeChat := &fakeChatService{
		enforceStatus: true,
		session: &chat.ChatSession{
			ID:        sessionID,
			ScopeType: "project_task",
			ScopeID:   taskID,
			Status:    "active",
		},
		turns: map[uuid.UUID]*chat.ChatTurn{
			turnID: {
				ID:        turnID,
				SessionID: sessionID,
				Status:    "failed",
			},
		},
	}
	engine := &TurnEngine{
		chat:   fakeChat,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rt := &turnRuntime{
		session: fakeChat.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: sessionID,
			Status:    "in_progress",
		},
	}

	if err := engine.completeTurn(context.Background(), rt); err != nil {
		t.Fatalf("completeTurn failed terminal no-op: %v", err)
	}
	if rt.turn == nil || !strings.EqualFold(strings.TrimSpace(rt.turn.Status), "failed") {
		t.Fatalf("rt.turn status = %v, want failed", rt.turn)
	}
}

func TestLatestSubstantiveAssistantFinalForTurnSkipsLaterIntentPlaceholder(t *testing.T) {
	t.Parallel()

	turnID := uuid.New()
	messages := []repo.ChatMessage{
		{
			ID:             uuid.New(),
			TurnID:         &turnID,
			Role:           "assistant",
			Status:         "final",
			SequenceNumber: 10,
			Content: strings.TrimSpace(`# Deliverable

## Section
Real file body content that should be reused for the write fallback.
`),
		},
		{
			ID:             uuid.New(),
			TurnID:         &turnID,
			Role:           "assistant",
			Status:         "final",
			SequenceNumber: 11,
			Content:        "Now I'll create the acceptance criteria:",
		},
	}

	got := latestSubstantiveAssistantFinalForTurn(messages, turnID, "docs/target.md")
	if got == nil {
		t.Fatal("expected substantive assistant draft")
	}
	if got.SequenceNumber != 10 {
		t.Fatalf("sequence_number = %d, want 10", got.SequenceNumber)
	}
}

func TestHandleTaskFileWriteWithoutContentUsesRecoveryArtifactDraftAfterIntentReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	turnID := uuid.New()
	targetPath := "design-system/03-accessibility-standards.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	recoveryArtifact := strings.TrimSpace(`# Recovery file.write artifact

Task: OC-28
Target Path: design-system/03-accessibility-standards.md
Generated: 2026-03-23T06:21:48Z

## Draft Content

# Accessibility Standards

## Contrast Requirements
- Body copy must meet WCAG 2.2 AA contrast thresholds.
- Interactive focus states must remain visible on every background.
`)
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    ".ottercamp/recovery/design-system/03-accessibility-standards.md",
				"content": recoveryArtifact,
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Now I'll write the full accessibility standards document:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": targetPath,
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWithoutContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWithoutContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "# Accessibility Standards") {
		t.Fatalf("content = %q, want hydrated recovery artifact draft", got)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func TestHandleTaskFileWriteWithoutContentPrefersPriorSubstantiveDraftOverIntentReply(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "planning/prd-spec/oc-24-infrastructure-spec.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Infrastructure Specification

## Hosting
- Vercel Pro with edge caching.
`),
	})

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &turnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "I need to provide the content parameter. Let me write the full infrastructure specification:",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path": targetPath,
		},
	}

	handled, abort, err := fixture.engine.handleTaskFileWriteWithoutContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskFileWriteWithoutContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Hosting") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
}

func TestHandleTaskRejectedFileWriteContentRewritesPlaceholderFromPriorDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	turnID := uuid.New()
	targetPath := "docs/migration-plan/oc-15-content-migration-plan.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Content Migration Plan

## Migration Strategy
- Audit and classify all legacy posts.
- Migrate Tier 1 before Tier 2.
`),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:   "write-1",
		Name: "file.write",
		Arguments: map[string]any{
			"path":    targetPath,
			"content": "Now write the full migration plan:",
		},
	}

	handled, abort, err := fixture.engine.handleTaskRejectedFileWriteContent(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("handleTaskRejectedFileWriteContent: %v", err)
	}
	if handled || abort {
		t.Fatalf("handled=%v abort=%v, want false false", handled, abort)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Migration Strategy") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
}

func TestRecoveryFileWriteDraftContentUsesPriorTurnDraftAfterCurrentNarration(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	priorTurnID := uuid.New()
	currentTurnID := uuid.New()
	targetPath := "design-system/03-accessibility-standards.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Accessibility Standards

## Keyboard Navigation
- Every interactive control must be reachable without a mouse.
- Skip links must be visible on focus.
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "Excellent. Now I have all the context needed and will write the document.",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	draft, rejectReason, ok := fixture.engine.recoveryFileWriteDraftContent(context.Background(), rt, targetPath)
	if !ok {
		t.Fatal("expected recovery file write draft")
	}
	if rejectReason != "" {
		t.Fatalf("rejectReason = %q, want empty", rejectReason)
	}
	if !strings.Contains(draft, "## Keyboard Navigation") {
		t.Fatalf("draft = %q, want prior substantive draft", draft)
	}
}

func TestRecoveryFileWriteDraftContentUsesContinuationSummaryDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	currentTurnID := uuid.New()
	targetPath := "planning/prd-spec/oc-24-infrastructure-spec.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "system",
		Status:    "final",
		Content: strings.TrimSpace(`[Continuation summary] # Infrastructure Specification

## Hosting Provider Selection
- Primary platform: Vercel
- CDN: Edge Network with caching rules

## Monitoring
- Uptime and error-rate alerts wired to PagerDuty
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &currentTurnID,
		Role:      "assistant",
		Status:    "final",
		Content:   "I'm ready to work on OC-24. What would you like me to focus on first?",
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	draft, rejectReason, ok := fixture.engine.recoveryFileWriteDraftContent(context.Background(), rt, targetPath)
	if !ok {
		t.Fatal("expected recovery file write draft")
	}
	if rejectReason != "" {
		t.Fatalf("rejectReason = %q, want empty", rejectReason)
	}
	if !strings.Contains(draft, "## Hosting Provider Selection") {
		t.Fatalf("draft = %q, want continuation summary draft", draft)
	}
}

func TestRecoveryFileOutputContextPrefersOlderSubstantiveReadOverNewerStubPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	newTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "docs/migration-plan/oc-15-content-migration-plan.md",
				"content": strings.TrimSpace(`# OC-15: Content Migration Plan

## Executive Summary
This migration plan operationalizes the staged cutover and validation strategy.
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &newTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path":    "docs/migration-plan/oc-15-complete-migration-plan.md",
				"content": "Perfect. Now I have full context. Let me resume the task by completing the migration plan document.",
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryFileOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected recovery file output context")
	}
	if targetPath != "docs/migration-plan/oc-15-content-migration-plan.md" {
		t.Fatalf("targetPath = %q, want older substantive path", targetPath)
	}
	if !strings.Contains(draft, "## Executive Summary") {
		t.Fatalf("draft = %q, want substantive draft", draft)
	}
}

func TestRecoveryHistoricalSubstantiveOutputContextSkipsNewerStrategyArtifacts(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	oldTurnID := uuid.New()
	newTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/prd-spec/oc-24-infrastructure-spec.md",
				"content": strings.TrimSpace(`# Infrastructure Specification

## Hosting Provider
- Vercel
`),
			},
		})),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &newTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "planning/strategy-artifact/oc-24-success-narrative.md",
				"content": strings.TrimSpace(`# Success Narrative

## Scenario
- Launch day goes well.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        uuid.New(),
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	targetPath, draft, ok := fixture.engine.recoveryHistoricalSubstantiveOutputContext(context.Background(), rt)
	if !ok {
		t.Fatal("expected historical substantive output context")
	}
	if targetPath != "planning/prd-spec/oc-24-infrastructure-spec.md" {
		t.Fatalf("targetPath = %q, want deliverable path", targetPath)
	}
	if !strings.Contains(draft, "## Hosting Provider") {
		t.Fatalf("draft = %q, want deliverable draft", draft)
	}
}

func TestRecoveryFileWriteCheckpointCandidatePrefersHistoricalSubstantivePathOverInitialMessageMetadata(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	poisonedInitialID := uuid.New()
	oldTurnID := uuid.New()
	currentTurnID := uuid.New()

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	fixture.messages.create(repo.ChatMessage{
		ID:        poisonedInitialID,
		SessionID: fixture.session.ID,
		Role:      "user",
		Status:    "final",
		Content:   "supervisor recovery: resume task",
		Metadata: mustRawJSON(t, map[string]any{
			"source":                             "supervisor",
			"recovery_action":                    "resume",
			"recovery_checkpoint_target_path":    "docs/migration-plan/oc-15-complete-migration-plan.md",
			"recovery_checkpoint_artifact_path":  ".ottercamp/recovery/docs/migration-plan/oc-15-complete-migration-plan.md",
			"recovery_checkpoint_failure_reason": "placeholder",
		}),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &oldTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.read",
			"output": map[string]any{
				"path": "docs/migration-plan/oc-15-content-migration-plan.md",
				"content": strings.TrimSpace(`# OC-15: Content Migration Plan

## Executive Summary
This migration plan operationalizes the staged cutover and validation strategy.
`),
			},
		})),
	})

	rt := &turnRuntime{
		session:          fixture.session,
		initialMessageID: poisonedInitialID,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	checkpoint, ok := fixture.engine.recoveryFileWriteCheckpointCandidate(context.Background(), rt, "placeholder")
	if !ok {
		t.Fatal("expected recovery checkpoint candidate")
	}
	if checkpoint.TargetPath != "docs/migration-plan/oc-15-content-migration-plan.md" {
		t.Fatalf("TargetPath = %q, want historical substantive path", checkpoint.TargetPath)
	}
	if checkpoint.ArtifactPath != "" {
		t.Fatalf("ArtifactPath = %q, want empty after poisoned checkpoint override", checkpoint.ArtifactPath)
	}
}

func TestPersistRecoveryFileWriteCheckpointKeepsExistingSubstantiveTargetPath(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	turnID := uuid.New()
	dataDir := t.TempDir()
	projectSlug := "sam-blog"
	orgSlug := "default"
	workspaceRoot := filepath.Join(dataDir, "workspaces", projectSlug)
	existingTarget := "docs/migration-plan/oc-15-content-migration-plan.md"
	driftTarget := "docs/migration-plan/oc-15-complete-migration-plan.md"

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	existingMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(nil, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:   existingTarget,
		ArtifactPath: ".ottercamp/recovery/docs/migration-plan/oc-15-content-migration-plan.md",
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				ProjectID:      projectID,
				OrganizationID: fixture.session.OrganizationID,
				WorkStatus:     "in_progress",
				Metadata:       existingMetadata,
			},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}
	fixture.engine.projects = &fakeProjectRepo{items: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: fixture.session.OrganizationID, Slug: projectSlug},
	}}
	fixture.engine.organizations = &fakeOrganizationRepo{items: map[uuid.UUID]repo.Organization{
		fixture.session.OrganizationID: {ID: fixture.session.OrganizationID, Slug: orgSlug},
	}}
	fixture.engine.dataDir = dataDir

	existingAbs := filepath.Join(workspaceRoot, filepath.FromSlash(existingTarget))
	if err := os.MkdirAll(filepath.Dir(existingAbs), 0o755); err != nil {
		t.Fatalf("mkdir existing target: %v", err)
	}
	if err := os.WriteFile(existingAbs, []byte("# Migration Plan\n\n## Executive Summary\nSubstantive body.\n"), 0o644); err != nil {
		t.Fatalf("write existing target: %v", err)
	}

	rt := &turnRuntime{
		session: fixture.session,
		turn: &chat.ChatTurn{
			ID:        turnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}

	if err := fixture.engine.persistRecoveryFileWriteCheckpoint(context.Background(), rt, driftTarget, ".ottercamp/recovery/docs/migration-plan/oc-15-complete-migration-plan.md", "placeholder", uuid.New()); err != nil {
		t.Fatalf("persistRecoveryFileWriteCheckpoint: %v", err)
	}

	updated := taskRepo.items[taskID]
	checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(updated.Metadata)
	if !ok {
		t.Fatal("expected recovery checkpoint")
	}
	if checkpoint.TargetPath != existingTarget {
		t.Fatalf("TargetPath = %q, want %q", checkpoint.TargetPath, existingTarget)
	}
}

func TestRewriteRecoveryCLIExecuteWithoutCommandToFileWriteUsesPriorTurnDraft(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, "async")
	taskID := uuid.New()
	projectID := uuid.New()
	targetPath := "docs/content-strategy.md"
	priorTurnID := uuid.New()
	currentTurnID := uuid.New()
	assignedAgentID := fixture.chat.participants[0].ParticipantID

	fixture.session.ScopeType = "project_task"
	fixture.session.ScopeID = taskID

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {ID: taskID, ProjectID: projectID, WorkStatus: "in_progress", AssignedAgentID: &assignedAgentID},
		},
	}
	fixture.engine.tasks = taskRepo
	fixture.engine.taskTransitions = &fakeTaskTransitionService{repo: taskRepo}

	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "assistant",
		Status:    "final",
		Content: strings.TrimSpace(`# Content Strategy

## Migration Approach
- Audit legacy content types before import.
- Map each retained post to the new information architecture.
`),
	})
	fixture.messages.create(repo.ChatMessage{
		SessionID: fixture.session.ID,
		TurnID:    &priorTurnID,
		Role:      "tool_result",
		Status:    "final",
		Content: string(mustRawJSON(t, map[string]any{
			"tool_name": "file.write",
			"output": map[string]any{
				"path":      targetPath,
				"byte_size": 128,
				"created":   false,
			},
		})),
	})

	rt := &turnRuntime{
		recoveryTurn: true,
		session:      fixture.session,
		turn: &chat.ChatTurn{
			ID:        currentTurnID,
			SessionID: fixture.session.ID,
			Status:    "in_progress",
		},
	}
	call := &ToolCall{
		ID:        "cli-1",
		Name:      "cli.execute",
		Arguments: map[string]any{},
	}

	rewritten, err := fixture.engine.rewriteRecoveryCLIExecuteWithoutCommandToFileWrite(context.Background(), rt, call)
	if err != nil {
		t.Fatalf("rewriteRecoveryCLIExecuteWithoutCommandToFileWrite: %v", err)
	}
	if rewritten {
		t.Fatal("expected rewrite helper to continue with mutated call")
	}
	if call.Name != "file.write" {
		t.Fatalf("call.Name = %q, want file.write", call.Name)
	}
	if got := stringValue(call.Arguments["path"]); got != targetPath {
		t.Fatalf("path = %q, want %q", got, targetPath)
	}
	if got := stringValue(call.Arguments["content"]); !strings.Contains(got, "## Migration Approach") {
		t.Fatalf("content = %q, want prior substantive draft", got)
	}
	if got, ok := call.Arguments["create_dirs"].(bool); !ok || !got {
		t.Fatalf("create_dirs = %v, want true", call.Arguments["create_dirs"])
	}
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
