package turn

import (
	"context"
	"encoding/json"
	"errors"
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
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/model"
	"github.com/samhotchkiss/otter-camp/internal/prompt"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
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
	if job.runAfter == nil {
		t.Fatal("retry run_after missing")
	}
	wantRunAfter := base.Add(retryAfter)
	if !job.runAfter.Equal(wantRunAfter) {
		t.Fatalf("run_after = %s, want %s", *job.runAfter, wantRunAfter)
	}

	if !fixture.messages.containsContent("[Rate limited, retrying in 8m4s...]") {
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
	if projectBootstrapRecoverableMaxToolCallFailure(projectBootstrapProgress{
		ValidationFailureClass:  projectBootstrapFailureFirstWaveExecution,
		ValidationFailureReason: "kickoff validation failed: bootstrap setup persisted staffing but did not emit any executable non-bootstrap project tasks for the first wave",
	}) {
		t.Fatal("non-bounded first-wave execution failure should not be recoverable")
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

	cancelSubs := fixture.events.subscriptionNamesWithPrefix("turn-engine.cancel.")
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

func TestContinuationTurnUsesDeterministicBootstrapResumeState(t *testing.T) {
	fixture := newUnitFixture(t, "sync")
	fixture.session.ScopeType = "project"
	fixture.session.ScopeID = uuid.New()
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
		t.Fatalf("continuation summary calls = %d, want 0 for active bootstrap continuation", fixture.model.continuationSummaryCalls)
	}

	messages, err := fixture.messages.ListBySession(context.Background(), fixture.session.ID)
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	var sawResume bool
	for _, msg := range messages {
		if strings.Contains(msg.Content, "[Project bootstrap resume]") {
			sawResume = true
			break
		}
	}
	if !sawResume {
		t.Fatal("project bootstrap resume message missing")
	}
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

func TestRecoveryFileWriteDraftRejectReason(t *testing.T) {
	const targetPath = "docs/content-strategy.md"

	cases := []struct {
		name    string
		content string
		want    string
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recoveryFileWriteDraftRejectReason(tc.content, targetPath)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want contains %q", got, tc.want)
			}
			if !strings.Contains(got, targetPath) {
				t.Fatalf("recoveryFileWriteDraftRejectReason() = %q, want target path", got)
			}
		})
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

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}
