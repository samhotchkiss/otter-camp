package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
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

func TestStatusChangeOwnedByFlowTransition(t *testing.T) {
	executionID := uuid.New()
	cases := []struct {
		name             string
		transitionSource string
		flowEventType    string
		executionID      *uuid.UUID
		want             bool
	}{
		{
			name:             "flow advanced transition",
			transitionSource: "flow_transition",
			flowEventType:    "flow.advanced",
			executionID:      &executionID,
			want:             true,
		},
		{
			name:             "flow rejected transition",
			transitionSource: "flow_transition",
			flowEventType:    "flow.rejected",
			executionID:      &executionID,
			want:             true,
		},
		{
			name:             "missing execution id",
			transitionSource: "flow_transition",
			flowEventType:    "flow.rejected",
			want:             false,
		},
		{
			name:             "wrong source",
			transitionSource: "manual",
			flowEventType:    "flow.rejected",
			executionID:      &executionID,
			want:             false,
		},
		{
			name:             "unknown flow event",
			transitionSource: "flow_transition",
			flowEventType:    "flow.started",
			executionID:      &executionID,
			want:             false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statusChangeOwnedByFlowTransition(tc.transitionSource, tc.flowEventType, tc.executionID)
			if got != tc.want {
				t.Fatalf("statusChangeOwnedByFlowTransition(%q, %q, %v) = %v, want %v", tc.transitionSource, tc.flowEventType, tc.executionID, got, tc.want)
			}
		})
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

func TestSelectNextQueuedTaskUnderProjectGateReviewCheckpointStillBlocksEX248(t *testing.T) {
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
	if selected != nil {
		t.Fatalf("selected queued task = %v, want nil while review checkpoint gate is outstanding", selected)
	}
}

func TestSelectNextQueuedTaskUnderProjectGateBlocksQueuedChildrenBehindBootstrapGate(t *testing.T) {
	projectID := uuid.New()
	bootstrapGate := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  1,
		Title:       "Bootstrap governance gate",
		WorkStatus:  "draft",
		BlocksScope: "all",
		Metadata:    json.RawMessage(`{"bootstrap_gate":true}`),
	}
	child := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  2,
		Title:       "First-wave child",
		WorkStatus:  "queued",
		BlocksScope: "none",
	}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{bootstrapGate, child})
	if selected != nil {
		t.Fatalf("selected queued task = %v, want nil while bootstrap gate is outstanding", selected)
	}
}

func TestSelectNextQueuedTaskUnderProjectGateIgnoresInvalidDraftGateWithoutExecutionPath(t *testing.T) {
	projectID := uuid.New()
	assignedAgentID := uuid.New()
	invalidGate := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  38,
		Title:       "Late impossible gate",
		WorkStatus:  "draft",
		BlocksScope: "all",
	}
	queued := repo.ProjectTask{
		ID:              uuid.New(),
		ProjectID:       projectID,
		TaskNumber:      15,
		Title:           "Queued work",
		WorkStatus:      "queued",
		BlocksScope:     "none",
		AssignedAgentID: &assignedAgentID,
	}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{invalidGate, queued})
	if selected == nil || selected.ID != queued.ID {
		t.Fatalf("selected queued task = %v, want queued task %s", selected, queued.ID)
	}
}

func TestSelectNextQueuedTaskUnderProjectGateIgnoresSupersededBootstrapGateCopies(t *testing.T) {
	projectID := uuid.New()
	doneGate := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  1,
		Title:       "Bootstrap governance gate",
		WorkStatus:  "done",
		BlocksScope: "all",
		Metadata:    json.RawMessage(`{"bootstrap_gate":true}`),
	}
	doneSetup := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  2,
		Title:       "Bind repo and environment",
		WorkStatus:  "done",
		BlocksScope: "none",
		Metadata:    json.RawMessage(`{"bootstrap_step_slug":"bind-repo-environment"}`),
	}
	duplicateDraftGate := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  9,
		Title:       "Bootstrap governance gate",
		WorkStatus:  "draft",
		BlocksScope: "all",
		Metadata:    json.RawMessage(`{"bootstrap_gate":true}`),
	}
	child := repo.ProjectTask{
		ID:          uuid.New(),
		ProjectID:   projectID,
		TaskNumber:  20,
		Title:       "First-wave child",
		WorkStatus:  "queued",
		BlocksScope: "none",
	}

	selected := selectNextQueuedTaskUnderProjectGate([]repo.ProjectTask{doneGate, doneSetup, duplicateDraftGate, child})
	if selected == nil || selected.ID != child.ID {
		t.Fatalf("selected queued task = %v, want queued child %s after canonical bootstrap gate is already done", selected, child.ID)
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

func TestProcessQueuedTaskSkipsArchivedProject(t *testing.T) {
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
					ID:     projectID,
					Status: "archived",
				},
			},
		},
	}

	if err := processor.processQueuedTask(ctx, eventbus.DomainEvent{ID: uuid.New()}, taskID); err != nil {
		t.Fatalf("processQueuedTask: %v", err)
	}
}

func TestProcessQueuedTaskSuppressesStaleQueuedConflict(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	taskID := uuid.New()

	taskRepo := &fakeTaskQueueTaskRepository{
		taskLookupSequence: []repo.ProjectTask{
			{
				ID:         taskID,
				ProjectID:  projectID,
				WorkStatus: "queued",
			},
			{
				ID:         taskID,
				ProjectID:  projectID,
				WorkStatus: "blocked",
			},
		},
	}
	taskService := &fakeTaskQueueStatusTransitioner{
		transitionErr: repo.ErrConflict,
	}
	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks:       taskRepo,
		projects:    &fakeTaskQueueProjectRepository{items: map[uuid.UUID]repo.Project{projectID: {ID: projectID}}},
		taskService: taskService,
		flow:        &fakeTaskQueueFlowStarter{},
		runs:        runService,
	}

	if err := processor.processQueuedTask(ctx, eventbus.DomainEvent{ID: uuid.New()}, taskID); err != nil {
		t.Fatalf("processQueuedTask: %v", err)
	}
	if len(taskService.transitionCalls) != 1 {
		t.Fatalf("TransitionStatus calls = %d, want 1", len(taskService.transitionCalls))
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0 after stale queued conflict", len(runService.createRunInputs))
	}
}

func TestProcessQueuedTaskRequestsHumanApprovalAndHoldsLegacyApprovalGatedTask(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	taskID := uuid.New()

	taskRepo := &fakeTaskQueueTaskRepository{
		taskLookupSequence: []repo.ProjectTask{
			{
				ID:                  taskID,
				ProjectID:           projectID,
				WorkStatus:          "queued",
				RequiresHumanReview: true,
			},
			{
				ID:                  taskID,
				ProjectID:           projectID,
				WorkStatus:          "on_hold",
				RequiresHumanReview: true,
			},
		},
	}
	taskService := &fakeTaskQueueStatusTransitioner{
		onTransition: func(_ uuid.UUID, toStatus string, _ tasksvc.Actor) error {
			if toStatus == "in_progress" {
				return tasksvc.ErrRequiresHumanApproval
			}
			return nil
		},
	}
	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks:       taskRepo,
		projects:    &fakeTaskQueueProjectRepository{items: map[uuid.UUID]repo.Project{projectID: {ID: projectID}}},
		taskService: taskService,
		flow:        &fakeTaskQueueFlowStarter{},
		runs:        runService,
	}

	if err := processor.processQueuedTask(ctx, eventbus.DomainEvent{ID: uuid.New()}, taskID); err != nil {
		t.Fatalf("processQueuedTask: %v", err)
	}
	if len(taskService.approvalCalls) != 1 || taskService.approvalCalls[0] != taskID {
		t.Fatalf("RequestHumanApproval calls = %v, want [%s]", taskService.approvalCalls, taskID)
	}
	if len(taskService.transitionCalls) != 2 {
		t.Fatalf("TransitionStatus calls = %d, want 2", len(taskService.transitionCalls))
	}
	if taskService.transitionCalls[0].toStatus != "in_progress" {
		t.Fatalf("first TransitionStatus = %q, want in_progress", taskService.transitionCalls[0].toStatus)
	}
	if taskService.transitionCalls[1].toStatus != "on_hold" {
		t.Fatalf("second TransitionStatus = %q, want on_hold", taskService.transitionCalls[1].toStatus)
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0 for approval-gated queued task", len(runService.createRunInputs))
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

func TestTaskQueueProcessorHandleFlowAdvancedEventAutoAdvancesTerminalMergeNode(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	sessionID := uuid.New()

	flowService := &fakeTaskQueueFlowStarter{}
	runService := &fakeTaskQueueRunStarter{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:                taskID,
				ProjectID:         projectID,
				WorkStatus:        "in_progress",
				CurrentFlowNodeID: &nodeID,
			},
		},
		flow: flowService,
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:       nodeID,
				NodeType: "merge",
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

	if flowService.advanceCalls != 1 {
		t.Fatalf("AdvanceFlow calls = %d, want 1", flowService.advanceCalls)
	}
	if flowService.lastAdvanceTask != taskID {
		t.Fatalf("AdvanceFlow task_id = %s, want %s", flowService.lastAdvanceTask, taskID)
	}
	if flowService.lastAdvanceActor.Type != "system" {
		t.Fatalf("AdvanceFlow actor.type = %q, want system", flowService.lastAdvanceActor.Type)
	}
	if len(runService.createRunInputs) != 0 {
		t.Fatalf("CreateRun calls = %d, want 0", len(runService.createRunInputs))
	}
}

func TestTaskQueueProcessorHandleFlowAdvancedEventDefaultsBlankReviewActorToReviewerAssignment(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	reviewerID := uuid.New()
	sessionID := uuid.New()

	runService := &fakeTaskQueueRunStarter{}
	chatService := &fakeTaskQueueChatService{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:                taskID,
				OrganizationID:    orgID,
				ProjectID:         projectID,
				Title:             "Review draft",
				WorkStatus:        "review",
				CurrentFlowNodeID: &nodeID,
			},
		},
		assignments: &fakeTaskQueueAssignmentRepository{
			assignments: []repo.AgentProjectAssignment{
				{ProjectID: projectID, AgentID: reviewerID, Role: "reviewer", IsActive: true},
			},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{
				ID:       nodeID,
				NodeType: "review",
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
	if createInput.PrincipalType != "agent" || createInput.PrincipalID != reviewerID {
		t.Fatalf("CreateRun principal = (%s,%s), want (agent,%s)", createInput.PrincipalType, createInput.PrincipalID, reviewerID)
	}
	if len(chatService.appendMessages) != 1 {
		t.Fatalf("AppendMessage calls = %d, want 1", len(chatService.appendMessages))
	}
	if !strings.Contains(chatService.appendMessages[0].Content, "Start work on task: Review draft") {
		t.Fatalf("kickoff content missing task details: %q", chatService.appendMessages[0].Content)
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
	activeExecutionID := uuid.New()
	flowExecutions := &fakeTaskQueueFlowExecutionRepository{
		executionsByTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: activeExecutionID, TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"},
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "completed"},
			},
		},
	}
	processor := &TaskQueueProcessor{runs: runService, flowExecutions: flowExecutions}

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
	activeExecutionID := uuid.New()
	flowExecutions := &fakeTaskQueueFlowExecutionRepository{
		executionsByTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: activeExecutionID, TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"},
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "completed"},
			},
		},
	}
	processor := &TaskQueueProcessor{runs: runService, flowExecutions: flowExecutions}

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
	activeExecutionID := uuid.New()
	flowExecutions := &fakeTaskQueueFlowExecutionRepository{
		executionsByTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: activeExecutionID, TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"},
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "completed"},
			},
		},
	}
	processor := &TaskQueueProcessor{
		runs:           runService,
		flowExecutions: flowExecutions,
	}

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
	if len(runService.retireRuntimeTaskCalls) != 1 || runService.retireRuntimeTaskCalls[0].taskID != taskID || runService.retireRuntimeTaskCalls[0].reason != "blocked" {
		t.Fatalf("RetireRuntimeStateForTask calls = %+v, want blocked task retirement", runService.retireRuntimeTaskCalls)
	}
	if len(flowExecutions.abandonCalls) != 1 || flowExecutions.abandonCalls[0] != activeExecutionID {
		t.Fatalf("Abandon calls = %+v, want execution %s", flowExecutions.abandonCalls, activeExecutionID)
	}
}

func TestTaskQueueProcessorHandleTaskCompletedEventAutoCompletesParentTask(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	parentID := uuid.New()
	taskID := uuid.New()

	runService := &fakeTaskQueueRunStarter{}
	taskService := &fakeTaskQueueStatusTransitioner{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, 1),
			},
		},
		taskService: taskService,
		runs:        runService,
	}

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

	if len(taskService.transitionCalls) != 1 {
		t.Fatalf("TransitionStatus calls = %d, want 1", len(taskService.transitionCalls))
	}
	call := taskService.transitionCalls[0]
	if call.taskID != parentID || call.toStatus != "done" {
		t.Fatalf("transition call = %+v, want parent done", call)
	}
	if call.actor.Type != "system" || !call.actor.AllowOrchestrationAutoComplete {
		t.Fatalf("actor = %+v, want system orchestration auto-complete", call.actor)
	}
	if len(runService.retireRuntimeTaskCalls) != 1 || runService.retireRuntimeTaskCalls[0].taskID != taskID {
		t.Fatalf("RetireRuntimeStateForTask calls = %+v, want child task %s", runService.retireRuntimeTaskCalls, taskID)
	}
}

func TestTaskQueueProcessorHandleTaskCompletedEventCatchesUpDormantParentTasks(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	completedTaskID := uuid.New()
	parentID := uuid.New()
	grandparentID := uuid.New()
	childID := uuid.New()

	parentMetadata := taskdecomp.AppendChildTaskID(json.RawMessage(`{}`), childID)
	grandparentMetadata := taskdecomp.AppendChildTaskID(json.RawMessage(`{}`), parentID)
	taskRepo := &fakeTaskQueueTaskRepository{
		task: repo.ProjectTask{
			ID:             completedTaskID,
			OrganizationID: orgID,
			ProjectID:      projectID,
		},
		tasksByProject: []repo.ProjectTask{
			{
				ID:             grandparentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				WorkStatus:     "draft",
				Metadata:       grandparentMetadata,
			},
			{
				ID:             parentID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				WorkStatus:     "draft",
				Metadata:       parentMetadata,
			},
		},
	}

	runService := &fakeTaskQueueRunStarter{}
	taskService := &fakeTaskQueueStatusTransitioner{}
	taskService.onTransition = func(taskID uuid.UUID, toStatus string, actor tasksvc.Actor) error {
		if toStatus != "done" || !actor.AllowOrchestrationAutoComplete {
			return nil
		}
		switch taskID {
		case grandparentID:
			for _, task := range taskRepo.tasksByProject {
				if task.ID == parentID && strings.EqualFold(strings.TrimSpace(task.WorkStatus), "done") {
					for i := range taskRepo.tasksByProject {
						if taskRepo.tasksByProject[i].ID == grandparentID {
							taskRepo.tasksByProject[i].WorkStatus = "done"
						}
					}
					return nil
				}
			}
			return taskorchestration.ErrParentCompletionRequirements
		case parentID:
			for i := range taskRepo.tasksByProject {
				if taskRepo.tasksByProject[i].ID == parentID {
					taskRepo.tasksByProject[i].WorkStatus = "done"
				}
			}
		}
		return nil
	}
	processor := &TaskQueueProcessor{
		tasks:       taskRepo,
		taskService: taskService,
		runs:        runService,
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":    completedTaskID,
		"project_id": projectID,
		"to_status":  "done",
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

	if len(taskService.transitionCalls) != 3 {
		t.Fatalf("TransitionStatus calls = %d, want 3 (grandparent retry, parent, grandparent success)", len(taskService.transitionCalls))
	}
	if taskService.transitionCalls[0].taskID != grandparentID {
		t.Fatalf("first transition task = %s, want grandparent %s", taskService.transitionCalls[0].taskID, grandparentID)
	}
	if taskService.transitionCalls[1].taskID != parentID {
		t.Fatalf("second transition task = %s, want parent %s", taskService.transitionCalls[1].taskID, parentID)
	}
	if taskService.transitionCalls[2].taskID != grandparentID {
		t.Fatalf("third transition task = %s, want grandparent retry %s", taskService.transitionCalls[2].taskID, grandparentID)
	}
	for _, task := range taskRepo.tasksByProject {
		if task.WorkStatus != "done" {
			t.Fatalf("task %+v remained non-done after dormant parent catch-up", task)
		}
	}
}

func TestTaskQueueProcessorHandleTaskCompletedEventAutoCompletesBootstrapPlanningTasks(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	bootstrapTaskID := uuid.New()

	runService := &fakeTaskQueueRunStarter{}
	taskService := &fakeTaskQueueStatusTransitioner{}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
			},
			tasksByProject: []repo.ProjectTask{
				{
					ID:             bootstrapTaskID,
					OrganizationID: orgID,
					ProjectID:      projectID,
					Title:          "Bootstrap: Site Strategy & Direction",
					WorkStatus:     "draft",
				},
			},
		},
		taskService: taskService,
		runs:        runService,
	}

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

	if len(taskService.transitionCalls) != 1 {
		t.Fatalf("TransitionStatus calls = %d, want 1", len(taskService.transitionCalls))
	}
	call := taskService.transitionCalls[0]
	if call.taskID != bootstrapTaskID || call.toStatus != "done" {
		t.Fatalf("transition call = %+v, want bootstrap task done", call)
	}
	if call.actor.Type != "system" || !call.actor.AllowBootstrapPlanningAutoComplete {
		t.Fatalf("actor = %+v, want system bootstrap planning auto-complete", call.actor)
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

func TestEnsureFlowRunRepairsMissingNodeSessionBeforeCreatingWakeup(t *testing.T) {
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

	flowRepo := &fakeTaskQueueFlowExecutionRepository{
		execution: repo.FlowNodeExecution{ID: executionID, FlowNodeID: flowNodeID},
	}
	chatService := &fakeTaskQueueChatService{
		session: &chat.ChatSession{ID: sessionID, Status: "active", Mode: "async"},
		onGetOrCreateNodeSession: func(session *chat.ChatSession) {
			flowRepo.execution.SessionID = &session.ID
		},
	}
	runService := &fakeTaskQueueRunStarter{
		run: Run{ID: runID, SessionID: &sessionID},
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
		flowExecutions: flowRepo,
		runs:           runService,
		chats:          chatService,
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

	if len(chatService.getOrCreateNodeCalls) != 1 {
		t.Fatalf("GetOrCreateNodeSession calls = %d, want 1", len(chatService.getOrCreateNodeCalls))
	}
	if chatService.getOrCreateNodeCalls[0].flowNodeExecutionID != executionID {
		t.Fatalf("GetOrCreateNodeSession execution_id = %s, want %s", chatService.getOrCreateNodeCalls[0].flowNodeExecutionID, executionID)
	}
	if chatService.getOrCreateNodeCalls[0].agentID != agentID {
		t.Fatalf("GetOrCreateNodeSession agent_id = %s, want %s", chatService.getOrCreateNodeCalls[0].agentID, agentID)
	}
	if len(runService.createRunInputs) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runService.createRunInputs))
	}
	if runService.createRunInputs[0].SessionID == nil || *runService.createRunInputs[0].SessionID != sessionID {
		t.Fatalf("CreateRun session_id = %v, want %s", runService.createRunInputs[0].SessionID, sessionID)
	}
}

func TestEnsureFlowTransitionRunRepairsMissingNodeSessionBeforeCreatingWakeup(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nextNodeID := uuid.New()
	nextExecutionID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()

	flowRepo := &fakeTaskQueueFlowExecutionRepository{
		execution: repo.FlowNodeExecution{ID: nextExecutionID, TaskID: taskID, FlowNodeID: nextNodeID, SessionID: &sessionID},
	}
	chatService := &fakeTaskQueueChatService{
		session: &chat.ChatSession{ID: sessionID, Status: "active", Mode: "async"},
		onGetOrCreateNodeSession: func(session *chat.ChatSession) {
			flowRepo.execution.SessionID = &session.ID
		},
	}
	runService := &fakeTaskQueueRunStarter{}
	sessionRepo := &fakeTaskQueueSessionRepository{
		session: &repo.ChatSession{
			ID:             sessionID,
			OrganizationID: orgID,
			ScopeType:      "project_task",
			ScopeID:        taskID,
			Mode:           "async",
			Status:         "active",
		},
	}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				Title:          "Flow transition task",
				FlowTemplateID: &nextNodeID,
			},
		},
		flowExecutions: flowRepo,
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{ID: nextNodeID, NodeType: "review"},
		},
		runs:     runService,
		chats:    chatService,
		sessions: sessionRepo,
	}

	taskRecord := repo.ProjectTask{
		ID:             taskID,
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          "Flow transition task",
	}
	nextNode := repo.FlowNode{ID: nextNodeID, NodeType: "review"}
	nextExecution := repo.FlowNodeExecution{ID: nextExecutionID, TaskID: taskID, FlowNodeID: nextNodeID}

	err := processor.ensureFlowTransitionRun(ctx, eventbus.DomainEvent{
		ID:        uuid.New(),
		EventType: "flow.advanced",
	}, taskRecord, nextNode, nextExecution, agentID, "")
	if err != nil {
		t.Fatalf("ensureFlowTransitionRun() error = %v", err)
	}

	if len(chatService.getOrCreateNodeCalls) != 1 {
		t.Fatalf("GetOrCreateNodeSession calls = %d, want 1", len(chatService.getOrCreateNodeCalls))
	}
	if chatService.getOrCreateNodeCalls[0].flowNodeExecutionID != nextExecutionID {
		t.Fatalf("GetOrCreateNodeSession execution_id = %s, want %s", chatService.getOrCreateNodeCalls[0].flowNodeExecutionID, nextExecutionID)
	}
	if chatService.getOrCreateNodeCalls[0].agentID != agentID {
		t.Fatalf("GetOrCreateNodeSession agent_id = %s, want %s", chatService.getOrCreateNodeCalls[0].agentID, agentID)
	}
	if len(runService.createRunInputs) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runService.createRunInputs))
	}
	if runService.createRunInputs[0].SessionID == nil || *runService.createRunInputs[0].SessionID != sessionID {
		t.Fatalf("CreateRun session_id = %v, want %s", runService.createRunInputs[0].SessionID, sessionID)
	}
}

func TestDispatchTaskQueueWakeupFlowCurrentUsesExecutionSession(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	agentID := uuid.New()
	genericSessionID := uuid.New()
	executionSessionID := uuid.New()

	chatService := &fakeTaskQueueChatService{
		createSessionResult: &chat.ChatSession{ID: genericSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "active"},
		nodeSession:         &chat.ChatSession{ID: executionSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "active"},
	}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:              taskID,
				OrganizationID:  orgID,
				ProjectID:       projectID,
				Title:           "Flow dispatch task",
				FlowTemplateID:  &nodeID,
				AssignedAgentID: &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, SessionID: &executionSessionID, Status: "active"},
		},
		chats: chatService,
	}

	metadata, err := json.Marshal(map[string]any{
		"execution_wakeup": map[string]any{
			"source": "task_queue_processor",
			"kind":   "flow_current",
		},
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	err = processor.dispatchTaskQueueWakeup(ctx, Run{
		ID:             runID,
		OrganizationID: orgID,
		PrincipalType:  "agent",
		PrincipalID:    agentID,
		TaskID:         &taskID,
		FlowNodeID:     &nodeID,
		SessionID:      &executionSessionID,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("dispatchTaskQueueWakeup() error = %v", err)
	}

	if len(chatService.appendMessages) != 1 {
		t.Fatalf("appendMessage calls = %d, want 1", len(chatService.appendMessages))
	}
	if chatService.appendMessages[0].SessionID != executionSessionID {
		t.Fatalf("kickoff session_id = %s, want execution session %s", chatService.appendMessages[0].SessionID, executionSessionID)
	}
	if len(chatService.createSessionInputs) != 0 {
		t.Fatalf("CreateSession calls = %d, want 0 for flow wakeup dispatch", len(chatService.createSessionInputs))
	}
}

func TestDispatchTaskQueueWakeupFlowCurrentRepairsClosedExecutionSession(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	agentID := uuid.New()
	closedSessionID := uuid.New()
	repairedSessionID := uuid.New()

	chatService := &fakeTaskQueueChatService{
		session:     &chat.ChatSession{ID: closedSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "closed"},
		nodeSession: &chat.ChatSession{ID: repairedSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "active"},
	}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:              taskID,
				OrganizationID:  orgID,
				ProjectID:       projectID,
				Title:           "Flow dispatch task",
				FlowTemplateID:  &nodeID,
				AssignedAgentID: &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, SessionID: &closedSessionID, Status: "active"},
		},
		chats: chatService,
	}

	metadata, err := json.Marshal(map[string]any{
		"execution_wakeup": map[string]any{
			"source": "task_queue_processor",
			"kind":   "flow_current",
		},
		"flow_node_execution_id": executionID.String(),
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	err = processor.dispatchTaskQueueWakeup(ctx, Run{
		ID:             runID,
		OrganizationID: orgID,
		PrincipalType:  "agent",
		PrincipalID:    agentID,
		TaskID:         &taskID,
		FlowNodeID:     &nodeID,
		SessionID:      &closedSessionID,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("dispatchTaskQueueWakeup() error = %v", err)
	}

	if len(chatService.getOrCreateNodeCalls) != 1 {
		t.Fatalf("GetOrCreateNodeSession calls = %d, want 1", len(chatService.getOrCreateNodeCalls))
	}
	if len(chatService.appendMessages) != 1 {
		t.Fatalf("appendMessage calls = %d, want 1", len(chatService.appendMessages))
	}
	if chatService.appendMessages[0].SessionID != repairedSessionID {
		t.Fatalf("kickoff session_id = %s, want repaired execution session %s", chatService.appendMessages[0].SessionID, repairedSessionID)
	}
}

func TestDispatchTaskQueueWakeupFlowTransitionUsesExecutionSession(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	nodeID := uuid.New()
	executionID := uuid.New()
	runID := uuid.New()
	agentID := uuid.New()
	genericSessionID := uuid.New()
	executionSessionID := uuid.New()

	chatService := &fakeTaskQueueChatService{
		createSessionResult: &chat.ChatSession{ID: genericSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "active"},
		nodeSession:         &chat.ChatSession{ID: executionSessionID, OrganizationID: orgID, ScopeType: "project_task", ScopeID: taskID, Mode: "async", Status: "active"},
	}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:              taskID,
				OrganizationID:  orgID,
				ProjectID:       projectID,
				Title:           "Flow transition task",
				FlowTemplateID:  &nodeID,
				AssignedAgentID: &agentID,
			},
		},
		flowExecutions: &fakeTaskQueueFlowExecutionRepository{
			execution: repo.FlowNodeExecution{ID: executionID, TaskID: taskID, FlowNodeID: nodeID, SessionID: &executionSessionID, Status: "active"},
		},
		flowNodes: &fakeTaskQueueFlowNodeRepository{
			node: repo.FlowNode{ID: nodeID, DisplayName: "Review", NodeType: "review"},
		},
		chats: chatService,
	}

	metadata, err := json.Marshal(map[string]any{
		"execution_wakeup": map[string]any{
			"source": "task_queue_processor",
			"kind":   "flow_transition",
		},
		"flow_node_execution_id": executionID.String(),
		"flow_event_type":        "flow.advanced",
	})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	err = processor.dispatchTaskQueueWakeup(ctx, Run{
		ID:             runID,
		OrganizationID: orgID,
		PrincipalType:  "agent",
		PrincipalID:    agentID,
		TaskID:         &taskID,
		FlowNodeID:     &nodeID,
		SessionID:      &executionSessionID,
		Metadata:       metadata,
	})
	if err != nil {
		t.Fatalf("dispatchTaskQueueWakeup() error = %v", err)
	}

	if len(chatService.appendMessages) != 1 {
		t.Fatalf("appendMessage calls = %d, want 1", len(chatService.appendMessages))
	}
	if chatService.appendMessages[0].SessionID != executionSessionID {
		t.Fatalf("kickoff session_id = %s, want execution session %s", chatService.appendMessages[0].SessionID, executionSessionID)
	}
	if len(chatService.createSessionInputs) != 0 {
		t.Fatalf("CreateSession calls = %d, want 0 for flow transition dispatch", len(chatService.createSessionInputs))
	}
}

func TestHandleTurnTerminalEventReleasesSpecificRunResolvedFromTurn(t *testing.T) {
	ctx := context.Background()
	taskID := uuid.New()
	sessionID := uuid.New()
	turnID := uuid.New()
	messageID := uuid.New()
	runID := uuid.New()

	payload, err := json.Marshal(map[string]any{
		"session_id": sessionID.String(),
		"turn_id":    turnID.String(),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	runService := &fakeTaskQueueRunStarter{}
	chatService := &fakeTaskQueueChatService{
		session: &chat.ChatSession{
			ID:        sessionID,
			ScopeType: "project_task",
			ScopeID:   taskID,
		},
		turn: &chat.ChatTurn{
			ID:               turnID,
			SessionID:        sessionID,
			Status:           "completed",
			TriggerMessageID: &messageID,
		},
		listMessages: []*chat.ChatMessage{
			{
				ID:        messageID,
				SessionID: sessionID,
				Role:      "user",
				Metadata:  json.RawMessage(`{"run_id":"` + runID.String() + `"}`),
			},
		},
	}
	processor := &TaskQueueProcessor{
		tasks: &fakeTaskQueueTaskRepository{
			task: repo.ProjectTask{
				ID:         taskID,
				WorkStatus: "in_progress",
			},
		},
		runs:  runService,
		chats: chatService,
	}

	if err := processor.handleTurnTerminalEvent(ctx, eventbus.DomainEvent{
		EventType: "chat.turn.completed",
		Payload:   payload,
	}); err != nil {
		t.Fatalf("handleTurnTerminalEvent: %v", err)
	}

	if len(runService.releaseExecutionCalls) != 1 {
		t.Fatalf("releaseExecutionCalls = %d, want 1", len(runService.releaseExecutionCalls))
	}
	if runService.releaseExecutionCalls[0].runID != runID {
		t.Fatalf("released run_id = %s, want %s", runService.releaseExecutionCalls[0].runID, runID)
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
	task               repo.ProjectTask
	tasksByProject     []repo.ProjectTask
	taskLookupSequence []repo.ProjectTask
	taskLookupIndex    int
	err                error
}

func (f *fakeTaskQueueTaskRepository) GetByID(context.Context, uuid.UUID) (repo.ProjectTask, error) {
	if f.err != nil {
		return repo.ProjectTask{}, f.err
	}
	if len(f.taskLookupSequence) > 0 {
		idx := f.taskLookupIndex
		if idx >= len(f.taskLookupSequence) {
			idx = len(f.taskLookupSequence) - 1
		}
		f.taskLookupIndex++
		return f.taskLookupSequence[idx], nil
	}
	return f.task, nil
}

func (f *fakeTaskQueueTaskRepository) ListByProject(_ context.Context, _ uuid.UUID, statuses ...string) ([]repo.ProjectTask, error) {
	if f.err != nil {
		return nil, f.err
	}
	if len(f.tasksByProject) > 0 {
		statusFilter := map[string]struct{}{}
		for _, status := range statuses {
			normalized := strings.ToLower(strings.TrimSpace(status))
			if normalized != "" {
				statusFilter[normalized] = struct{}{}
			}
		}
		out := make([]repo.ProjectTask, 0, len(f.tasksByProject))
		for _, task := range f.tasksByProject {
			if len(statusFilter) > 0 {
				if _, ok := statusFilter[strings.ToLower(strings.TrimSpace(task.WorkStatus))]; !ok {
					continue
				}
			}
			out = append(out, task)
		}
		return out, nil
	}
	if len(f.taskLookupSequence) > 0 {
		last := f.taskLookupSequence[len(f.taskLookupSequence)-1]
		return []repo.ProjectTask{last}, nil
	}
	if f.task.ID == uuid.Nil {
		return nil, nil
	}
	return []repo.ProjectTask{f.task}, nil
}

type transitionTaskQueueCall struct {
	taskID   uuid.UUID
	toStatus string
	actor    tasksvc.Actor
}

type fakeTaskQueueStatusTransitioner struct {
	transitionCalls []transitionTaskQueueCall
	transitionTask  *tasksvc.ProjectTask
	transitionErr   error
	onTransition    func(taskID uuid.UUID, toStatus string, actor tasksvc.Actor) error
	approvalCalls   []uuid.UUID
	approvalErr     error
}

func (f *fakeTaskQueueStatusTransitioner) TransitionStatus(_ context.Context, taskID uuid.UUID, toStatus string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	f.transitionCalls = append(f.transitionCalls, transitionTaskQueueCall{
		taskID:   taskID,
		toStatus: toStatus,
		actor:    actor,
	})
	if f.onTransition != nil {
		if err := f.onTransition(taskID, toStatus, actor); err != nil {
			return nil, err
		}
	}
	if f.transitionErr != nil {
		return nil, f.transitionErr
	}
	if f.transitionTask != nil {
		return f.transitionTask, nil
	}
	return &tasksvc.ProjectTask{ID: taskID, WorkStatus: toStatus}, nil
}

func (f *fakeTaskQueueStatusTransitioner) CreateInboxItem(context.Context, tasksvc.CreateInboxItemRequest) (*tasksvc.InboxItem, error) {
	return &tasksvc.InboxItem{}, nil
}

func (f *fakeTaskQueueStatusTransitioner) RequestHumanApproval(_ context.Context, taskID uuid.UUID) (*tasksvc.InboxItem, error) {
	f.approvalCalls = append(f.approvalCalls, taskID)
	if f.approvalErr != nil {
		return nil, f.approvalErr
	}
	return &tasksvc.InboxItem{}, nil
}

type fakeTaskQueueFlowStarter struct {
	execution        repo.FlowNodeExecution
	err              error
	advanceCalls     int
	lastAdvanceTask  uuid.UUID
	lastAdvanceActor flowsvc.Actor
}

func (f *fakeTaskQueueFlowStarter) StartFlow(context.Context, uuid.UUID) (*repo.FlowNodeExecution, error) {
	if f.err != nil {
		return nil, f.err
	}
	execution := f.execution
	return &execution, nil
}

func (f *fakeTaskQueueFlowStarter) EnsureActiveExecution(context.Context, uuid.UUID) (*repo.FlowNodeExecution, error) {
	if f.err != nil {
		return nil, f.err
	}
	execution := f.execution
	return &execution, nil
}

func (f *fakeTaskQueueFlowStarter) PauseAtReviewCheckpoint(context.Context, uuid.UUID, flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	if f.err != nil {
		return nil, f.err
	}
	execution := f.execution
	return &execution, nil
}

func (f *fakeTaskQueueFlowStarter) AdvanceFlow(_ context.Context, taskID uuid.UUID, actor flowsvc.Actor) (*repo.FlowNodeExecution, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.advanceCalls++
	f.lastAdvanceTask = taskID
	f.lastAdvanceActor = actor
	execution := f.execution
	return &execution, nil
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
	runID     uuid.UUID
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

func (f *fakeTaskQueueRunStarter) ReleaseExecutionOwnerForRun(_ context.Context, taskID, sessionID, runID uuid.UUID, reason string) (executionWakeupResult, error) {
	f.releaseExecutionCalls = append(f.releaseExecutionCalls, releaseExecutionCall{
		taskID:    taskID,
		sessionID: sessionID,
		reason:    reason,
		runID:     runID,
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
	execution        repo.FlowNodeExecution
	executionsByTask map[uuid.UUID][]repo.FlowNodeExecution
	abandonCalls     []uuid.UUID
	err              error
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

func (f *fakeTaskQueueFlowExecutionRepository) ListByTask(_ context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.executionsByTask != nil {
		return append([]repo.FlowNodeExecution(nil), f.executionsByTask[taskID]...), nil
	}
	if f.execution.ID == uuid.Nil {
		return nil, nil
	}
	return []repo.FlowNodeExecution{f.execution}, nil
}

func (f *fakeTaskQueueFlowExecutionRepository) Abandon(_ context.Context, id uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.err != nil {
		return repo.FlowNodeExecution{}, f.err
	}
	f.abandonCalls = append(f.abandonCalls, id)
	if f.execution.ID == id {
		f.execution.Status = "abandoned"
		return f.execution, nil
	}
	if f.executionsByTask != nil {
		for taskID, executions := range f.executionsByTask {
			for i := range executions {
				if executions[i].ID == id {
					executions[i].Status = "abandoned"
					f.executionsByTask[taskID] = executions
					return executions[i], nil
				}
			}
		}
	}
	return repo.FlowNodeExecution{ID: id, Status: "abandoned"}, nil
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
	calls                []string
	session              *chat.ChatSession
	createSessionResult  *chat.ChatSession
	nodeSession          *chat.ChatSession
	turn                 *chat.ChatTurn
	createSessionInputs  []chat.CreateSessionInput
	createSessionErr     error
	getOrCreateNodeCalls []struct {
		flowNodeExecutionID uuid.UUID
		agentID             uuid.UUID
	}
	getOrCreateNodeErr       error
	onGetOrCreateNodeSession func(*chat.ChatSession)
	onCreateSession          func(*chat.ChatSession)
	addParticipantCalls      []addParticipantCall
	addParticipantErr        error
	appendMessageErr         error
	appendMessages           []chat.AppendMessageInput
	listMessages             []*chat.ChatMessage
	listMessagesErr          error
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
	if f.createSessionResult != nil {
		if f.onCreateSession != nil {
			f.onCreateSession(f.createSessionResult)
		}
		return f.createSessionResult, nil
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

func (f *fakeTaskQueueChatService) GetOrCreateNodeSession(_ context.Context, flowNodeExecutionID, agentID uuid.UUID) (*chat.ChatSession, error) {
	f.calls = append(f.calls, "get_or_create_node_session")
	f.getOrCreateNodeCalls = append(f.getOrCreateNodeCalls, struct {
		flowNodeExecutionID uuid.UUID
		agentID             uuid.UUID
	}{
		flowNodeExecutionID: flowNodeExecutionID,
		agentID:             agentID,
	})
	if f.getOrCreateNodeErr != nil {
		return nil, f.getOrCreateNodeErr
	}
	if f.nodeSession != nil {
		if f.onGetOrCreateNodeSession != nil {
			f.onGetOrCreateNodeSession(f.nodeSession)
		}
		return f.nodeSession, nil
	}
	if f.session != nil {
		if f.onGetOrCreateNodeSession != nil {
			f.onGetOrCreateNodeSession(f.session)
		}
		return f.session, nil
	}
	session := &chat.ChatSession{
		ID:     uuid.New(),
		Status: "active",
		Mode:   "async",
	}
	f.session = session
	if f.onGetOrCreateNodeSession != nil {
		f.onGetOrCreateNodeSession(session)
	}
	return session, nil
}

func (f *fakeTaskQueueChatService) GetTurn(_ context.Context, id uuid.UUID) (*chat.ChatTurn, error) {
	if f.turn != nil && f.turn.ID == id {
		return f.turn, nil
	}
	return nil, repo.ErrNotFound
}

func (f *fakeTaskQueueChatService) GetMessage(_ context.Context, id uuid.UUID) (*chat.ChatMessage, error) {
	for _, message := range f.listMessages {
		if message != nil && message.ID == id {
			return message, nil
		}
	}
	return nil, repo.ErrNotFound
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
