package controlplane

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestTaskQueueProcessorHandleTaskQueuedEventIgnoresNonQueuedEvents(t *testing.T) {
	processor := &TaskQueueProcessor{}

	payload, err := json.Marshal(map[string]any{
		"task_id":   uuid.New(),
		"to_status": "in_progress",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	cases := []eventbus.DomainEvent{
		{
			EventType: "task.created",
			Payload:   payload,
		},
		{
			EventType: "task.status_changed",
			Payload:   payload,
		},
		{
			EventType: "task.status_changed",
			Payload:   []byte(`not-json`),
		},
	}

	for _, event := range cases {
		if err := processor.handleTaskQueuedEvent(context.Background(), event); err != nil {
			t.Fatalf("handleTaskQueuedEvent(%s) error = %v, want nil", event.EventType, err)
		}
	}
}

func TestTaskQueueProcessorHandleTaskCompletedEventConfirmsCancellingSchedulerRuns(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	taskID := uuid.New()
	inProgressRunID := uuid.New()
	cancellingRunID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		listRunsByTaskResponses: map[string][]Run{
			"in_progress|scheduler": {
				{ID: inProgressRunID, TriggerType: "scheduler", Status: "in_progress"},
			},
			"cancelling|scheduler": {
				{ID: cancellingRunID, TriggerType: "scheduler", Status: "cancelling"},
			},
		},
	}
	processor := &TaskQueueProcessor{runs: runService}

	payload, err := json.Marshal(map[string]any{
		"task_id":   taskID,
		"to_status": "done",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := eventbus.DomainEvent{
		OrganizationID: orgID,
		EventType:      "task.status_changed",
		Payload:        payload,
	}
	if err := processor.handleTaskCompletedEvent(ctx, event); err != nil {
		t.Fatalf("handleTaskCompletedEvent: %v", err)
	}

	if len(runService.completeRunCalls) != 1 || runService.completeRunCalls[0].runID != inProgressRunID {
		t.Fatalf("CompleteRun calls = %+v, want run %s", runService.completeRunCalls, inProgressRunID)
	}
	if len(runService.confirmCancelledCalls) != 1 || runService.confirmCancelledCalls[0] != cancellingRunID {
		t.Fatalf("ConfirmCancelled calls = %+v, want run %s", runService.confirmCancelledCalls, cancellingRunID)
	}
}

func TestTaskQueueProcessorHandleRunCancellationRequestedEventAutoConfirmsSchedulerAndSupervisor(t *testing.T) {
	ctx := context.Background()
	schedulerRunID := uuid.New()
	supervisorRunID := uuid.New()
	agentToolRunID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		runByID: map[uuid.UUID]Run{
			schedulerRunID:  {ID: schedulerRunID, TriggerType: "scheduler", Status: "cancelling"},
			supervisorRunID: {ID: supervisorRunID, TriggerType: "supervisor", Status: "cancelling"},
			agentToolRunID:  {ID: agentToolRunID, TriggerType: "agent_tool", Status: "cancelling"},
		},
	}
	processor := &TaskQueueProcessor{runs: runService}

	for _, runID := range []uuid.UUID{schedulerRunID, supervisorRunID, agentToolRunID} {
		payload, err := json.Marshal(map[string]any{"run_id": runID.String()})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := processor.handleRunCancellationRequestedEvent(ctx, eventbus.DomainEvent{
			EventType: "run.cancellation_requested",
			Payload:   payload,
		}); err != nil {
			t.Fatalf("handleRunCancellationRequestedEvent(%s): %v", runID, err)
		}
	}

	if len(runService.confirmCancelledCalls) != 2 {
		t.Fatalf("ConfirmCancelled calls = %d, want 2", len(runService.confirmCancelledCalls))
	}
	if runService.confirmCancelledCalls[0] != schedulerRunID && runService.confirmCancelledCalls[1] != schedulerRunID {
		t.Fatalf("missing scheduler run confirm: %+v", runService.confirmCancelledCalls)
	}
	if runService.confirmCancelledCalls[0] != supervisorRunID && runService.confirmCancelledCalls[1] != supervisorRunID {
		t.Fatalf("missing supervisor run confirm: %+v", runService.confirmCancelledCalls)
	}
}

func TestEnsureAssignedAgentRunAddsParticipantBeforeKickoff(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	sessionRepo := &fakeTaskQueueSessionRepository{
		session: &repo.ChatSession{ID: sessionID},
	}
	runService := &fakeTaskQueueRunStarter{
		run: Run{ID: runID, SessionID: &sessionID},
	}
	chatService := &fakeTaskQueueChatService{}

	processor := &TaskQueueProcessor{
		sessions: sessionRepo,
		runs:     runService,
		chats:    chatService,
	}

	err := processor.ensureAssignedAgentRun(ctx, eventbus.DomainEvent{ID: uuid.New()}, repo.ProjectTask{
		ID:              taskID,
		OrganizationID:  orgID,
		ProjectID:       projectID,
		Title:           "Queued task",
		AssignedAgentID: &agentID,
	})
	if err != nil {
		t.Fatalf("ensureAssignedAgentRun() error = %v", err)
	}

	if len(chatService.addParticipantCalls) != 1 {
		t.Fatalf("addParticipant calls = %d, want 1", len(chatService.addParticipantCalls))
	}
	call := chatService.addParticipantCalls[0]
	if call.sessionID != sessionID {
		t.Fatalf("addParticipant session_id = %s, want %s", call.sessionID, sessionID)
	}
	if call.participantType != "agent" {
		t.Fatalf("addParticipant participant_type = %q, want agent", call.participantType)
	}
	if call.participantID != agentID {
		t.Fatalf("addParticipant participant_id = %s, want %s", call.participantID, agentID)
	}
	if call.role != "responder" {
		t.Fatalf("addParticipant role = %q, want responder", call.role)
	}

	addIdx := indexOfCall(chatService.calls, "add_participant")
	appendIdx := indexOfCall(chatService.calls, "append_message")
	if addIdx == -1 || appendIdx == -1 || addIdx > appendIdx {
		t.Fatalf("call order = %v, want add_participant before append_message", chatService.calls)
	}
}

func TestEnsureAssignedAgentRunIgnoresDuplicateParticipant(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	processor := &TaskQueueProcessor{
		sessions: &fakeTaskQueueSessionRepository{
			session: &repo.ChatSession{ID: sessionID},
		},
		runs: &fakeTaskQueueRunStarter{
			run: Run{ID: runID, SessionID: &sessionID},
		},
		chats: &fakeTaskQueueChatService{
			addParticipantErr: chat.ErrAlreadyParticipant,
		},
	}

	err := processor.ensureAssignedAgentRun(ctx, eventbus.DomainEvent{ID: uuid.New()}, repo.ProjectTask{
		ID:              taskID,
		OrganizationID:  orgID,
		ProjectID:       projectID,
		Title:           "Queued task",
		AssignedAgentID: &agentID,
	})
	if err != nil {
		t.Fatalf("ensureAssignedAgentRun() error = %v, want nil", err)
	}
}

func TestEnsureFlowRunAddsParticipantAndKickoffMessage(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	chatService := &fakeTaskQueueChatService{}
	processor := &TaskQueueProcessor{
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, FlowNodeID: flowNodeID, SessionID: &sessionID},
		},
		runs: &fakeTaskQueueRunStarter{
			run: Run{ID: runID, SessionID: &sessionID},
		},
		chats: chatService,
	}

	err := processor.ensureFlowRun(ctx, eventbus.DomainEvent{ID: uuid.New()}, repo.ProjectTask{
		ID:                taskID,
		OrganizationID:    orgID,
		ProjectID:         projectID,
		WorkStatus:        "in_progress",
		Title:             "Flow task",
		CurrentFlowNodeID: &flowNodeID,
		AssignedAgentID:   &agentID,
	})
	if err != nil {
		t.Fatalf("ensureFlowRun() error = %v", err)
	}

	if len(chatService.addParticipantCalls) != 1 {
		t.Fatalf("addParticipant calls = %d, want 1", len(chatService.addParticipantCalls))
	}
	if len(chatService.appendMessages) != 1 {
		t.Fatalf("appendMessage calls = %d, want 1", len(chatService.appendMessages))
	}

	addIdx := indexOfCall(chatService.calls, "add_participant")
	appendIdx := indexOfCall(chatService.calls, "append_message")
	if addIdx == -1 || appendIdx == -1 || addIdx > appendIdx {
		t.Fatalf("call order = %v, want add_participant before append_message", chatService.calls)
	}

	appended := chatService.appendMessages[0]
	if appended.Role != "user" {
		t.Fatalf("kickoff role = %q, want user", appended.Role)
	}
	if !strings.Contains(appended.Content, "Flow node execution: "+executionID.String()) {
		t.Fatalf("kickoff content = %q, want flow execution id", appended.Content)
	}
}

func TestEnsureFlowRunKickoffIsIdempotent(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()
	existingMetadata, err := json.Marshal(map[string]any{
		"source": "task_queue_processor",
		"run_id": runID.String(),
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	chatService := &fakeTaskQueueChatService{
		listMessages: []*chat.ChatMessage{
			{
				ID:        uuid.New(),
				SessionID: sessionID,
				Role:      "user",
				Metadata:  existingMetadata,
			},
		},
	}
	processor := &TaskQueueProcessor{
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, FlowNodeID: flowNodeID, SessionID: &sessionID},
		},
		runs: &fakeTaskQueueRunStarter{
			run: Run{ID: runID, SessionID: &sessionID},
		},
		chats: chatService,
	}

	err = processor.ensureFlowRun(ctx, eventbus.DomainEvent{ID: uuid.New()}, repo.ProjectTask{
		ID:                taskID,
		OrganizationID:    orgID,
		ProjectID:         projectID,
		WorkStatus:        "in_progress",
		Title:             "Flow task",
		CurrentFlowNodeID: &flowNodeID,
		AssignedAgentID:   &agentID,
	})
	if err != nil {
		t.Fatalf("ensureFlowRun() error = %v", err)
	}
	if len(chatService.appendMessages) != 0 {
		t.Fatalf("appendMessage calls = %d, want 0 for existing kickoff", len(chatService.appendMessages))
	}
}

func indexOfCall(calls []string, target string) int {
	for idx, call := range calls {
		if call == target {
			return idx
		}
	}
	return -1
}

type fakeTaskQueueSessionRepository struct {
	session *repo.ChatSession
	err     error
}

func (f *fakeTaskQueueSessionRepository) GetByScopeAndMode(context.Context, string, uuid.UUID, string) (*repo.ChatSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

type fakeTaskQueueRunStarter struct {
	run                     Run
	createErr               error
	startErr                error
	completeErr             error
	confirmCancelledErr     error
	getRunErr               error
	runByID                 map[uuid.UUID]Run
	listRunsByTaskResponses map[string][]Run
	completeRunCalls        []completeRunCall
	confirmCancelledCalls   []uuid.UUID
	listRunsByTaskCalls     []listRunsByTaskCall
}

type completeRunCall struct {
	runID  uuid.UUID
	output json.RawMessage
}

type listRunsByTaskCall struct {
	organizationID uuid.UUID
	taskID         uuid.UUID
	status         string
	triggerType    string
}

func (f *fakeTaskQueueRunStarter) CreateRun(_ context.Context, input CreateRunInput) (Run, error) {
	if f.createErr != nil {
		return Run{}, f.createErr
	}
	run := f.run
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	if run.SessionID == nil {
		run.SessionID = input.SessionID
	}
	return run, nil
}

func (f *fakeTaskQueueRunStarter) StartRun(context.Context, uuid.UUID) error {
	if f.startErr != nil {
		return f.startErr
	}
	return nil
}

func (f *fakeTaskQueueRunStarter) CompleteRun(_ context.Context, runID uuid.UUID, output json.RawMessage) error {
	f.completeRunCalls = append(f.completeRunCalls, completeRunCall{
		runID:  runID,
		output: append(json.RawMessage(nil), output...),
	})
	if f.completeErr != nil {
		return f.completeErr
	}
	return nil
}

func (f *fakeTaskQueueRunStarter) ConfirmCancelled(_ context.Context, runID uuid.UUID) error {
	f.confirmCancelledCalls = append(f.confirmCancelledCalls, runID)
	if f.confirmCancelledErr != nil {
		return f.confirmCancelledErr
	}
	return nil
}

func (f *fakeTaskQueueRunStarter) GetRun(_ context.Context, runID uuid.UUID) (Run, error) {
	if f.getRunErr != nil {
		return Run{}, f.getRunErr
	}
	if runRecord, ok := f.runByID[runID]; ok {
		return runRecord, nil
	}
	return Run{}, ErrNotFound
}

func (f *fakeTaskQueueRunStarter) ListRunsByTask(_ context.Context, organizationID, taskID uuid.UUID, status, triggerType string) ([]Run, error) {
	f.listRunsByTaskCalls = append(f.listRunsByTaskCalls, listRunsByTaskCall{
		organizationID: organizationID,
		taskID:         taskID,
		status:         status,
		triggerType:    triggerType,
	})
	if f.listRunsByTaskResponses != nil {
		key := status + "|" + triggerType
		if runs, ok := f.listRunsByTaskResponses[key]; ok {
			return append([]Run(nil), runs...), nil
		}
	}
	return nil, nil
}

type fakeTaskQueueFlowExecutionRepository struct {
	execution repo.FlowNodeExecution
	err       error
}

func (f *fakeTaskQueueFlowExecutionRepository) GetActive(context.Context, uuid.UUID, uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.err != nil {
		return repo.FlowNodeExecution{}, f.err
	}
	return f.execution, nil
}

type addParticipantCall struct {
	sessionID       uuid.UUID
	participantType string
	participantID   uuid.UUID
	role            string
}

type fakeTaskQueueChatService struct {
	calls               []string
	session             *chat.ChatSession
	createSessionErr    error
	addParticipantCalls []addParticipantCall
	addParticipantErr   error
	appendMessageErr    error
	appendMessages      []chat.AppendMessageInput
	listMessages        []*chat.ChatMessage
	listMessagesErr     error
}

func (f *fakeTaskQueueChatService) CreateSession(_ context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error) {
	f.calls = append(f.calls, "create_session")
	if f.createSessionErr != nil {
		return nil, f.createSessionErr
	}
	if f.session != nil {
		return f.session, nil
	}
	session := &chat.ChatSession{
		ID:             uuid.New(),
		OrganizationID: input.OrganizationID,
		ScopeType:      input.ScopeType,
		ScopeID:        input.ScopeID,
		Mode:           input.Mode,
		Status:         "active",
	}
	return session, nil
}

func (f *fakeTaskQueueChatService) AddParticipant(_ context.Context, sessionID uuid.UUID, participantType string, participantID uuid.UUID, role string) (*chat.ChatParticipant, error) {
	f.calls = append(f.calls, "add_participant")
	f.addParticipantCalls = append(f.addParticipantCalls, addParticipantCall{
		sessionID:       sessionID,
		participantType: participantType,
		participantID:   participantID,
		role:            role,
	})
	if f.addParticipantErr != nil {
		return nil, f.addParticipantErr
	}
	return &chat.ChatParticipant{
		ID:              uuid.New(),
		SessionID:       sessionID,
		ParticipantType: participantType,
		ParticipantID:   participantID,
		Role:            role,
	}, nil
}

func (f *fakeTaskQueueChatService) AppendMessage(_ context.Context, input chat.AppendMessageInput) (*chat.ChatMessage, error) {
	f.calls = append(f.calls, "append_message")
	f.appendMessages = append(f.appendMessages, input)
	if f.appendMessageErr != nil {
		return nil, f.appendMessageErr
	}
	return &chat.ChatMessage{
		ID:        uuid.New(),
		SessionID: input.SessionID,
		Role:      input.Role,
		Content:   input.Content,
		Metadata:  input.Metadata,
	}, nil
}

func (f *fakeTaskQueueChatService) ListMessages(context.Context, uuid.UUID, chat.MessageFilter) ([]*chat.ChatMessage, error) {
	if f.listMessagesErr != nil {
		return nil, f.listMessagesErr
	}
	return f.listMessages, nil
}

var _ taskQueueChatService = (*fakeTaskQueueChatService)(nil)
var _ taskQueueSessionRepository = (*fakeTaskQueueSessionRepository)(nil)
var _ taskQueueRunStarter = (*fakeTaskQueueRunStarter)(nil)
var _ taskQueueFlowExecutionRepository = (*fakeTaskQueueFlowExecutionRepository)(nil)
