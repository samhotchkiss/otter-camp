package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestTaskQueueProcessorHandleTaskQueuedEventIgnoresIrrelevantStatusesAndEvents(t *testing.T) {
	processor := &TaskQueueProcessor{}

	payload, err := json.Marshal(map[string]any{
		"task_id":   uuid.New(),
		"to_status": "done",
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

func TestTaskQueueProcessorHandleTaskQueuedEventCreatesOrReusesCanonicalTaskSessionForInProgressTask(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()

	sessionRepo := &fakeTaskQueueSessionRepository{}
	chatService := &fakeTaskQueueChatService{
		onCreateSession: func(session *chat.ChatSession) {
			sessionRepo.StoreSession(session)
		},
	}
	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:              taskID,
				OrganizationID:  orgID,
				ProjectID:       projectID,
				WorkStatus:      "in_progress",
				Title:           "Start task work",
				AssignedAgentID: &agentID,
			},
		},
		runs:     runService,
		chats:    chatService,
		sessions: sessionRepo,
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":    taskID,
		"project_id": projectID,
		"to_status":  "in_progress",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := processor.handleTaskQueuedEvent(ctx, eventbus.DomainEvent{
		ID:        uuid.New(),
		EventType: "task.status_changed",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("handleTaskQueuedEvent: %v", err)
	}

	if len(chatService.createSessionInputs) == 0 {
		t.Fatal("expected CreateSession to canonicalize a task-scoped async session")
	}
	for _, createdInput := range chatService.createSessionInputs {
		if createdInput.ScopeType != "project_task" {
			t.Fatalf("CreateSession scope_type = %q, want project_task", createdInput.ScopeType)
		}
		if createdInput.ScopeID != taskID {
			t.Fatalf("CreateSession scope_id = %s, want %s", createdInput.ScopeID, taskID)
		}
		if createdInput.Mode != "async" {
			t.Fatalf("CreateSession mode = %q, want async", createdInput.Mode)
		}
	}

	session, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", taskID, "async")
	if err != nil {
		t.Fatalf("GetByScopeAndMode: %v", err)
	}
	if session == nil {
		t.Fatal("expected persisted task-scoped async session")
	}

	if len(runService.createRunInputs) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runService.createRunInputs))
	}
	if runService.createRunInputs[0].SessionID == nil || *runService.createRunInputs[0].SessionID != session.ID {
		t.Fatalf("CreateRun session_id = %v, want %s", runService.createRunInputs[0].SessionID, session.ID)
	}
}

func TestTaskQueueProcessorHandleTaskQueuedEventRepeatedStartReusesCanonicalTaskSession(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	agentID := uuid.New()

	sessionRepo := &fakeTaskQueueSessionRepository{}
	chatService := &fakeTaskQueueChatService{
		onCreateSession: func(session *chat.ChatSession) {
			sessionRepo.StoreSession(session)
		},
	}
	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:              taskID,
				OrganizationID:  orgID,
				ProjectID:       projectID,
				WorkStatus:      "in_progress",
				Title:           "Resume task work",
				AssignedAgentID: &agentID,
			},
		},
		runs:     runService,
		chats:    chatService,
		sessions: sessionRepo,
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":    taskID,
		"project_id": projectID,
		"to_status":  "in_progress",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := processor.handleTaskQueuedEvent(ctx, eventbus.DomainEvent{
			ID:        uuid.New(),
			EventType: "task.status_changed",
			Payload:   payload,
		}); err != nil {
			t.Fatalf("handleTaskQueuedEvent call %d: %v", i+1, err)
		}
	}

	if len(chatService.createSessionInputs) == 0 {
		t.Fatal("expected CreateSession to canonicalize the task-scoped async session")
	}
	if len(runService.createRunInputs) != 2 {
		t.Fatalf("CreateRun calls = %d, want 2", len(runService.createRunInputs))
	}
	firstSessionID := runService.createRunInputs[0].SessionID
	secondSessionID := runService.createRunInputs[1].SessionID
	if firstSessionID == nil || secondSessionID == nil {
		t.Fatalf("CreateRun session_ids = (%v, %v), want both non-nil", firstSessionID, secondSessionID)
	}
	if *firstSessionID != *secondSessionID {
		t.Fatalf("CreateRun session_ids = (%s, %s), want same task session", *firstSessionID, *secondSessionID)
	}
}

func TestSelectNextQueuedTaskUnderProjectGatePrefersLowestOutstandingGate(t *testing.T) {
	projectID := uuid.New()
	gateOne := repo.ProjectTask{ID: uuid.New(), ProjectID: projectID, TaskNumber: 1, WorkStatus: "queued", BlocksScope: "all"}
	gateTwo := repo.ProjectTask{ID: uuid.New(), ProjectID: projectID, TaskNumber: 2, WorkStatus: "queued", BlocksScope: "all"}
	normal := repo.ProjectTask{ID: uuid.New(), ProjectID: projectID, TaskNumber: 3, WorkStatus: "queued", BlocksScope: "none"}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{normal, gateTwo, gateOne})
	if selected == nil || selected.ID != gateOne.ID {
		t.Fatalf("selected queued task = %v, want gate task %s", selected, gateOne.ID)
	}

	gateOneDone := gateOne
	gateOneDone.WorkStatus = "done"
	selected = selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{normal, gateTwo, gateOneDone})
	if selected == nil || selected.ID != gateTwo.ID {
		t.Fatalf("selected queued task after first gate done = %v, want gate task %s", selected, gateTwo.ID)
	}

	gateTwoDone := gateTwo
	gateTwoDone.WorkStatus = "done"
	selected = selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{normal, gateTwoDone, gateOneDone})
	if selected == nil || selected.ID != normal.ID {
		t.Fatalf("selected queued task after gates complete = %v, want task %s", selected, normal.ID)
	}
}

func TestSelectNextQueuedTaskUnderProjectGateSkipsReviewCheckpointEX248(t *testing.T) {
	projectID := uuid.New()
	description := "Pause for review before finalizing the chosen launch direction."
	reviewCheckpoint := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  1,
		Title:       "Prepare launch direction",
		Description: &description,
		WorkStatus:  "review",
		BlocksScope: "all",
	}
	normal := repo.ProjectTask{ID: uuid.New(), ProjectID: projectID, TaskNumber: 2, WorkStatus: "queued", BlocksScope: "none"}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{reviewCheckpoint, normal})
	if selected == nil || selected.ID != normal.ID {
		t.Fatalf("selected queued task = %v, want task %s", selected, normal.ID)
	}
}

func TestSelectNextQueuedTaskUnderProjectGateAllowsBootstrapPhasedChild(t *testing.T) {
	projectID := uuid.New()
	gate := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  1,
		WorkStatus:  "queued",
		BlocksScope: "all",
		Metadata:    json.RawMessage(`{"bootstrap_gate":true}`),
	}
	parentID := uuid.New()
	child := repo.ProjectTask{
		ID:         uuid.New(),
		ProjectID:  projectID,
		TaskNumber: 3,
		WorkStatus: "queued",
		Metadata:   json.RawMessage(fmt.Sprintf(`{"decomposition_parent_task_id":"%s"}`, parentID)),
	}
	normal := repo.ProjectTask{
		ID:         uuid.New(),
		ProjectID:  projectID,
		TaskNumber: 4,
		WorkStatus: "queued",
	}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{normal, child, gate})
	if selected == nil || selected.ID != child.ID {
		t.Fatalf("selected queued task = %v, want bootstrap child %s", selected, child.ID)
	}
}

func TestProcessQueuedTaskSkipsPausedProject(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	taskID := uuid.New()

	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:         taskID,
				ProjectID:  projectID,
				WorkStatus: "queued",
			},
		},
		projects: &fakeTaskQueueProjectRepository{
			items: map[uuid.UUID]repo.Project{
				projectID: {
					ID:       projectID,
					Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
				},
			},
		},
	}

	if err := processor.processQueuedTask(ctx, eventbus.DomainEvent{ID: uuid.New()}, taskID); err != nil {
		t.Fatalf("processQueuedTask: %v", err)
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventCreatesRunForAgentNode(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		run: Run{ID: runID, SessionID: &sessionID},
	}
	chatService := &fakeTaskQueueChatService{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Flow advanced task",
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:          nodeID,
				DisplayName: "Review",
				NodeType:    "review",
				ActorType:   strPtr("agent"),
				ActorID:     &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "active", SessionID: &sessionID},
		},
		runs:  runService,
		chats: chatService,
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":              taskID,
		"project_id":           projectID,
		"to_flow_node_id":      nodeID,
		"to_flow_execution_id": executionID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := processor.handleFlowAdvancedEvent(ctx, eventbus.DomainEvent{EventType: "flow.advanced", Payload: payload}); err != nil {
		t.Fatalf("handleFlowAdvancedEvent: %v", err)
	}

	if len(runService.createRunInputs) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runService.createRunInputs))
	}
	createInput := runService.createRunInputs[0]
	if createInput.PrincipalType != "agent" || createInput.PrincipalID != agentID {
		t.Fatalf("CreateRun principal = (%s,%s), want (agent,%s)", createInput.PrincipalType, createInput.PrincipalID, agentID)
	}
	if createInput.IdempotencyKey == nil || *createInput.IdempotencyKey != "flow-transition:"+executionID.String() {
		t.Fatalf("CreateRun idempotency_key = %v, want flow-transition:%s", createInput.IdempotencyKey, executionID)
	}
	if len(chatService.addParticipantCalls) != 1 {
		t.Fatalf("AddParticipant calls = %d, want 1", len(chatService.addParticipantCalls))
	}
	if len(chatService.appendMessages) != 1 {
		t.Fatalf("AppendMessage calls = %d, want 1", len(chatService.appendMessages))
	}
	if !strings.Contains(chatService.appendMessages[0].Content, "Flow node: Review") {
		t.Fatalf("kickoff content missing flow node details: %q", chatService.appendMessages[0].Content)
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventIgnoresHumanActor(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	sessionID := uuid.New()

	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:        taskID,
				ProjectID: projectID,
			},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:        nodeID,
				NodeType:  "review",
				ActorType: strPtr("human"),
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "active", SessionID: &sessionID},
		},
		runs:  runService,
		chats: &fakeTaskQueueChatService{},
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":              taskID,
		"project_id":           projectID,
		"to_flow_node_id":      nodeID,
		"to_flow_execution_id": executionID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := processor.handleFlowAdvancedEvent(ctx, eventbus.DomainEvent{EventType: "flow.advanced", Payload: payload}); err != nil {
		t.Fatalf("handleFlowAdvancedEvent: %v", err)
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0", len(runService.createRunInputs))
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventIgnoresTerminalTransition(t *testing.T) {
	ctx := context.Background()

	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		runs: runService,
	}
	payload, err := json.Marshal(map[string]any{
		"task_id":                  uuid.New(),
		"project_id":               uuid.New(),
		"to_flow_node_id":          uuid.New(),
		"to_flow_execution_id":     uuid.New(),
		"terminal_transition_done": true,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := processor.handleFlowAdvancedEvent(ctx, eventbus.DomainEvent{EventType: "flow.advanced", Payload: payload}); err != nil {
		t.Fatalf("handleFlowAdvancedEvent: %v", err)
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0", len(runService.createRunInputs))
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventIgnoresTaskFlowRuntimeMismatch(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()

	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Stale flow task",
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:        nodeID,
				NodeType:  "work",
				ActorType: strPtr("agent"),
				ActorID:   &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "active", SessionID: &sessionID},
		},
		runs:  runService,
		chats: &fakeTaskQueueChatService{},
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":              taskID,
		"project_id":           projectID,
		"to_flow_node_id":      nodeID,
		"to_flow_execution_id": executionID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := processor.handleFlowAdvancedEvent(ctx, eventbus.DomainEvent{EventType: "flow.advanced", Payload: payload}); err != nil {
		t.Fatalf("handleFlowAdvancedEvent: %v", err)
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0", len(runService.createRunInputs))
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventDuplicateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		run:                 Run{ID: runID, SessionID: &sessionID},
		dedupeByIdempotency: true,
	}
	chatService := &fakeTaskQueueChatService{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Flow duplicate task",
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:        nodeID,
				NodeType:  "work",
				ActorType: strPtr("agent"),
				ActorID:   &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, Status: "active", SessionID: &sessionID},
		},
		runs:  runService,
		chats: chatService,
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":              taskID,
		"project_id":           projectID,
		"to_flow_node_id":      nodeID,
		"to_flow_execution_id": executionID,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	first := eventbus.DomainEvent{ID: uuid.New(), EventType: "flow.advanced", Payload: payload}
	second := eventbus.DomainEvent{ID: uuid.New(), EventType: "flow.advanced", Payload: payload}
	if err := processor.handleFlowAdvancedEvent(ctx, first); err != nil {
		t.Fatalf("first handleFlowAdvancedEvent: %v", err)
	}
	if err := processor.handleFlowAdvancedEvent(ctx, second); err != nil {
		t.Fatalf("second handleFlowAdvancedEvent: %v", err)
	}

	if runService.uniqueCreateRunCount != 1 {
		t.Fatalf("unique CreateRun calls = %d, want 1", runService.uniqueCreateRunCount)
	}
	if len(chatService.appendMessages) != 1 {
		t.Fatalf("AppendMessage calls = %d, want 1", len(chatService.appendMessages))
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

func TestTaskQueueProcessorHandleTaskCompletedEventCompletesSupervisorAndCancelledSchedulerRuns(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	taskID := uuid.New()
	schedulerRunID := uuid.New()
	supervisorRunID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		listRunsByTaskResponses: map[string][]Run{
			"in_progress|scheduler": {
				{ID: schedulerRunID, TriggerType: "scheduler", Status: "in_progress"},
			},
			"in_progress|supervisor": {
				{ID: supervisorRunID, TriggerType: "supervisor", Status: "in_progress"},
			},
		},
	}
	processor := &TaskQueueProcessor{runs: runService}

	payload, err := json.Marshal(map[string]any{
		"task_id":   taskID,
		"to_status": "cancelled",
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

	if len(runService.completeRunCalls) != 2 {
		t.Fatalf("CompleteRun calls = %d, want 2", len(runService.completeRunCalls))
	}
	var sawScheduler bool
	var sawSupervisor bool
	for _, call := range runService.completeRunCalls {
		if call.runID == schedulerRunID {
			sawScheduler = true
		}
		if call.runID == supervisorRunID {
			sawSupervisor = true
		}
	}
	if !sawScheduler {
		t.Fatalf("missing scheduler completion call: %+v", runService.completeRunCalls)
	}
	if !sawSupervisor {
		t.Fatalf("missing supervisor completion call: %+v", runService.completeRunCalls)
	}
}

func TestTaskQueueProcessorHandleTaskCompletedEventFailsBlockedTrackingRuns(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	taskID := uuid.New()
	schedulerRunID := uuid.New()
	supervisorRunID := uuid.New()
	cancellingRunID := uuid.New()

	runService := &fakeTaskQueueRunStarter{
		listRunsByTaskResponses: map[string][]Run{
			"in_progress|scheduler": {
				{ID: schedulerRunID, TriggerType: "scheduler", Status: "in_progress"},
			},
			"in_progress|supervisor": {
				{ID: supervisorRunID, TriggerType: "supervisor", Status: "in_progress"},
			},
			"cancelling|scheduler": {
				{ID: cancellingRunID, TriggerType: "scheduler", Status: "cancelling"},
			},
		},
	}
	processor := &TaskQueueProcessor{runs: runService}

	payload, err := json.Marshal(map[string]any{
		"task_id":        taskID,
		"to_status":      "blocked",
		"blocker_reason": "provider authentication failed",
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

	if len(runService.completeRunCalls) != 0 {
		t.Fatalf("CompleteRun calls = %+v, want none for blocked task", runService.completeRunCalls)
	}
	if len(runService.failRunCalls) != 2 {
		t.Fatalf("FailRun calls = %d, want 2", len(runService.failRunCalls))
	}
	for _, call := range runService.failRunCalls {
		if call.failureClass != string(FailureClassPermanent) {
			t.Fatalf("failureClass = %q, want %q", call.failureClass, FailureClassPermanent)
		}
		if call.reason != "provider authentication failed" {
			t.Fatalf("reason = %q, want blocker reason", call.reason)
		}
	}
	if len(runService.confirmCancelledCalls) != 1 || runService.confirmCancelledCalls[0] != cancellingRunID {
		t.Fatalf("ConfirmCancelled calls = %+v, want run %s", runService.confirmCancelledCalls, cancellingRunID)
	}
	if len(runService.retireRuntimeTaskCalls) != 0 {
		t.Fatalf("RetireRuntimeStateForTask calls = %+v, want none for blocked task", runService.retireRuntimeTaskCalls)
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

	canonicalSession := &chat.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
		Mode:           "async",
		Status:         "active",
	}
	runService := &fakeTaskQueueRunStarter{
		run: Run{ID: runID, SessionID: &sessionID},
	}
	chatService := &fakeTaskQueueChatService{session: canonicalSession}

	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Queued task",
			},
		},
		runs:  runService,
		chats: chatService,
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
	canonicalSession := &chat.ChatSession{
		ID:             sessionID,
		OrganizationID: orgID,
		ScopeType:      "project_task",
		ScopeID:        taskID,
		Mode:           "async",
		Status:         "active",
	}

	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Queued task",
			},
		},
		runs: &fakeTaskQueueRunStarter{
			run: Run{ID: runID, SessionID: &sessionID},
		},
		chats: &fakeTaskQueueChatService{
			session:           canonicalSession,
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
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	executionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	runID := uuid.New()

	chatService := &fakeTaskQueueChatService{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				FlowTemplateID: &flowTemplateID,
				Title:          "Flow task",
			},
		},
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
		FlowTemplateID:    &flowTemplateID,
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
	flowTemplateID := uuid.New()
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
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				FlowTemplateID: &flowTemplateID,
				Title:          "Flow task",
			},
		},
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
		FlowTemplateID:    &flowTemplateID,
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
	session  *repo.ChatSession
	sessions map[string]*repo.ChatSession
	lookups  []taskQueueSessionLookup
	err      error
}

type taskQueueSessionLookup struct {
	scopeType string
	scopeID   uuid.UUID
	mode      string
}

func (f *fakeTaskQueueSessionRepository) GetByScopeAndMode(_ context.Context, scopeType string, scopeID uuid.UUID, mode string) (*repo.ChatSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lookups = append(f.lookups, taskQueueSessionLookup{
		scopeType: strings.TrimSpace(scopeType),
		scopeID:   scopeID,
		mode:      strings.TrimSpace(mode),
	})
	if f.sessions != nil {
		if session, ok := f.sessions[f.sessionKey(scopeType, scopeID, mode)]; ok {
			cloned := *session
			return &cloned, nil
		}
		return nil, nil
	}
	return f.session, nil
}

func (f *fakeTaskQueueSessionRepository) StoreSession(session *repo.ChatSession) {
	if session == nil {
		return
	}
	if f.sessions == nil {
		f.sessions = map[string]*repo.ChatSession{}
	}
	cloned := *session
	f.session = &cloned
	f.sessions[f.sessionKey(session.ScopeType, session.ScopeID, session.Mode)] = &cloned
}

func (f *fakeTaskQueueSessionRepository) sessionKey(scopeType string, scopeID uuid.UUID, mode string) string {
	return strings.TrimSpace(scopeType) + "|" + scopeID.String() + "|" + strings.TrimSpace(mode)
}

type fakeTaskQueueTaskRepository struct {
	task           repo.ProjectTask
	tasksByProject []repo.ProjectTask
	err            error
}

func (f *fakeTaskQueueTaskRepository) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	return f.task, nil
}

func (f *fakeTaskQueueTaskRepository) ListByProject(context.Context, uuid.UUID, ...string) ([]repo.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.tasksByProject) > 0 {
		return append([]repo.ProjectTask(nil), f.tasksByProject...), nil
	}
	if f.task.ID == uuid.Nil {
		return nil, nil
	}
	return []repo.ProjectTask{f.task}, nil
}

type fakeTaskQueueProjectRepository struct {
	items map[uuid.UUID]repo.Project
	err   error
}

func (f *fakeTaskQueueProjectRepository) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if f.err != nil {
		return repo.Project{}, f.err
	}
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

type fakeTaskQueueRunStarter struct {
	run                     Run
	createErr               error
	startErr                error
	completeErr             error
	failErr                 error
	confirmCancelledErr     error
	getRunErr               error
	runByID                 map[uuid.UUID]Run
	listRunsByTaskResponses map[string][]Run
	completeRunCalls        []completeRunCall
	failRunCalls            []failRunCall
	confirmCancelledCalls   []uuid.UUID
	listRunsByTaskCalls     []listRunsByTaskCall
	releaseExecutionCalls   []releaseExecutionCall
	retireRuntimeTaskCalls  []retireRuntimeTaskCall
	retireRuntimeProjCalls  []retireRuntimeProjectCall
	createRunInputs         []CreateRunInput
	dedupeByIdempotency     bool
	idempotentRuns          map[string]Run
	uniqueCreateRunCount    int
}

type completeRunCall struct {
	runID  uuid.UUID
	output json.RawMessage
}

type failRunCall struct {
	runID        uuid.UUID
	reason       string
	failureClass string
}

type listRunsByTaskCall struct {
	organizationID uuid.UUID
	taskID         uuid.UUID
	status         string
	triggerType    string
}

type releaseExecutionCall struct {
	taskID    uuid.UUID
	sessionID uuid.UUID
	reason    string
}

type retireRuntimeTaskCall struct {
	taskID uuid.UUID
	reason string
}

type retireRuntimeProjectCall struct {
	projectID uuid.UUID
	reason    string
}

func (f *fakeTaskQueueRunStarter) CreateRun(_ context.Context, input CreateRunInput) (Run, error) {
	if f.createErr != nil {
		return Run{}, f.createErr
	}
	f.createRunInputs = append(f.createRunInputs, input)
	if f.dedupeByIdempotency && input.IdempotencyKey != nil {
		if f.idempotentRuns == nil {
			f.idempotentRuns = map[string]Run{}
		}
		if existing, ok := f.idempotentRuns[*input.IdempotencyKey]; ok {
			return existing, nil
		}
	}
	run := f.run
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	if run.OrganizationID == uuid.Nil {
		run.OrganizationID = input.OrganizationID
	}
	if run.ProjectID == nil {
		run.ProjectID = input.ProjectID
	}
	if run.TaskID == nil {
		run.TaskID = input.TaskID
	}
	if run.FlowNodeID == nil {
		run.FlowNodeID = input.FlowNodeID
	}
	if run.SessionID == nil {
		run.SessionID = input.SessionID
	}
	if run.TurnID == nil {
		run.TurnID = input.TurnID
	}
	if run.PrincipalType == "" {
		run.PrincipalType = input.PrincipalType
	}
	if run.PrincipalID == uuid.Nil {
		run.PrincipalID = input.PrincipalID
	}
	if run.TriggerType == "" {
		run.TriggerType = input.TriggerType
	}
	if run.IdempotencyKey == nil {
		run.IdempotencyKey = input.IdempotencyKey
	}
	if len(run.Metadata) == 0 {
		run.Metadata = append(json.RawMessage(nil), input.Metadata...)
	}
	if f.dedupeByIdempotency && input.IdempotencyKey != nil {
		f.uniqueCreateRunCount++
		f.idempotentRuns[*input.IdempotencyKey] = run
	}
	return run, nil
}

func (f *fakeTaskQueueRunStarter) StartRun(context.Context, uuid.UUID) error {
	if f.startErr != nil {
		return f.startErr
	}
	return nil
}

func (f *fakeTaskQueueRunStarter) CreateExecutionWakeup(ctx context.Context, input executionWakeupInput) (executionWakeupResult, error) {
	createInput := input.CreateRunInput
	if scope, ok := executionScopeFromInput(createInput); ok {
		createInput.Metadata = buildExecutionWakeupMetadata(
			createInput.Metadata,
			scope,
			input.WakeupSource,
			input.WakeupKind,
			input.WakeupPayload,
			"started",
			nil,
		)
	}
	runRecord, err := f.CreateRun(ctx, createInput)
	if err != nil {
		return executionWakeupResult{}, err
	}
	if err := f.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		return executionWakeupResult{}, err
	}
	return executionWakeupResult{
		Run:      runRecord,
		Decision: executionWakeupStarted,
	}, nil
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

func (f *fakeTaskQueueRunStarter) FailRun(_ context.Context, runID uuid.UUID, reason, failureClass string) error {
	f.failRunCalls = append(f.failRunCalls, failRunCall{
		runID:        runID,
		reason:       reason,
		failureClass: failureClass,
	})
	if f.failErr != nil {
		return f.failErr
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

func (f *fakeTaskQueueRunStarter) ReleaseExecutionOwner(_ context.Context, taskID, sessionID uuid.UUID, reason string) (executionWakeupResult, error) {
	f.releaseExecutionCalls = append(f.releaseExecutionCalls, releaseExecutionCall{
		taskID:    taskID,
		sessionID: sessionID,
		reason:    reason,
	})
	return executionWakeupResult{}, nil
}

func (f *fakeTaskQueueRunStarter) RetireRuntimeStateForTask(_ context.Context, taskID uuid.UUID, reason string) error {
	f.retireRuntimeTaskCalls = append(f.retireRuntimeTaskCalls, retireRuntimeTaskCall{
		taskID: taskID,
		reason: reason,
	})
	return nil
}

func (f *fakeTaskQueueRunStarter) RetireRuntimeStateForProject(_ context.Context, projectID uuid.UUID, reason string) error {
	f.retireRuntimeProjCalls = append(f.retireRuntimeProjCalls, retireRuntimeProjectCall{
		projectID: projectID,
		reason:    reason,
	})
	return nil
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

func (f *fakeTaskQueueFlowExecutionRepository) GetByID(context.Context, uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.err != nil {
		return repo.FlowNodeExecution{}, f.err
	}
	return f.execution, nil
}

type fakeTaskQueueFlowNodeRepository struct {
	node repo.FlowNode
	err  error
}

func (f *fakeTaskQueueFlowNodeRepository) GetByID(context.Context, uuid.UUID) (repo.FlowNode, error) {
	if f.err != nil {
		return repo.FlowNode{}, f.err
	}
	return f.node, nil
}

type fakeTaskQueueAssignmentRepository struct {
	pm          repo.AgentProjectAssignment
	pmErr       error
	assignments []repo.AgentProjectAssignment
	listErr     error
}

func (f *fakeTaskQueueAssignmentRepository) GetPM(context.Context, uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.pmErr != nil {
		return repo.AgentProjectAssignment{}, f.pmErr
	}
	return f.pm, nil
}

func (f *fakeTaskQueueAssignmentRepository) ListByProject(context.Context, uuid.UUID) ([]repo.AgentProjectAssignment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]repo.AgentProjectAssignment(nil), f.assignments...), nil
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
	createSessionInputs []chat.CreateSessionInput
	createSessionErr    error
	onCreateSession     func(*chat.ChatSession)
	addParticipantCalls []addParticipantCall
	addParticipantErr   error
	appendMessageErr    error
	appendMessages      []chat.AppendMessageInput
	listMessages        []*chat.ChatMessage
	listMessagesErr     error
}

func (f *fakeTaskQueueChatService) GetSession(_ context.Context, id uuid.UUID) (*chat.ChatSession, error) {
	if f.session != nil && f.session.ID == id {
		return f.session, nil
	}
	for _, message := range f.listMessages {
		if message != nil && message.SessionID == id && f.session != nil {
			return f.session, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (f *fakeTaskQueueChatService) CreateSession(_ context.Context, input chat.CreateSessionInput) (*chat.ChatSession, error) {
	f.calls = append(f.calls, "create_session")
	f.createSessionInputs = append(f.createSessionInputs, input)
	if f.createSessionErr != nil {
		return nil, f.createSessionErr
	}
	if f.session != nil {
		if f.onCreateSession != nil {
			f.onCreateSession(f.session)
		}
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
	f.session = session
	if f.onCreateSession != nil {
		f.onCreateSession(session)
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
	message := &chat.ChatMessage{
		ID:        uuid.New(),
		SessionID: input.SessionID,
		Role:      input.Role,
		Content:   input.Content,
		Metadata:  input.Metadata,
	}
	f.listMessages = append(f.listMessages, message)
	return message, nil
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
var _ taskQueueTaskRepository = (*fakeTaskQueueTaskRepository)(nil)
var _ taskQueueProjectRepository = (*fakeTaskQueueProjectRepository)(nil)
var _ taskQueueFlowNodeRepository = (*fakeTaskQueueFlowNodeRepository)(nil)
var _ taskQueueAssignmentRepository = (*fakeTaskQueueAssignmentRepository)(nil)
