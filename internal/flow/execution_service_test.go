package flow

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/projectpause"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
)

func TestNextVisitNumberUsesMaxPlusOne(t *testing.T) {
	taskID := uuid.New()
	nodeID := uuid.New()

	svc := &service{
		executions: &fakeExecutionRepo{
			byTask: map[uuid.UUID][]repo.FlowNodeExecution{
				taskID: {
					{TaskID: taskID, FlowNodeID: nodeID, VisitNumber: 1},
					{TaskID: taskID, FlowNodeID: nodeID, VisitNumber: 3},
					{TaskID: taskID, FlowNodeID: uuid.New(), VisitNumber: 9},
				},
			},
		},
	}

	next, err := svc.nextVisitNumber(context.Background(), taskID, nodeID)
	if err != nil {
		t.Fatalf("nextVisitNumber: %v", err)
	}
	if next != 4 {
		t.Fatalf("nextVisitNumber = %d, want 4", next)
	}
}

func TestCheckCycleDetectsCycleAndNonCycle(t *testing.T) {
	a := uuid.New()
	b := uuid.New()
	c := uuid.New()
	d := uuid.New()

	deps := &fakeDependencyRepo{
		outbound: map[uuid.UUID][]repo.ProjectTaskDependency{
			a: {{SourceType: "project_task", SourceID: a, DependsOnType: "project_task", DependsOnID: b}},
			b: {{SourceType: "project_task", SourceID: b, DependsOnType: "project_task", DependsOnID: c}},
		},
	}
	svc := &service{dependencies: deps}

	hasCycle, err := svc.checkCycle(context.Background(), "project_task", c, a)
	if err != nil {
		t.Fatalf("checkCycle C<-A: %v", err)
	}
	if !hasCycle {
		t.Fatal("expected cycle for C depends on A through A->B->C chain")
	}

	noCycle, err := svc.checkCycle(context.Background(), "project_task", d, a)
	if err != nil {
		t.Fatalf("checkCycle D<-A: %v", err)
	}
	if noCycle {
		t.Fatal("did not expect cycle for D depends on A")
	}
}

func TestUpdateSubtaskStatusTransitions(t *testing.T) {
	subtaskID := uuid.New()
	subtasks := &fakeSubtaskRepo{
		items: map[uuid.UUID]repo.ProjectSubtask{
			subtaskID: {
				ID:         subtaskID,
				WorkStatus: "pending",
			},
		},
	}
	svc := &service{subtasks: subtasks}

	if _, err := svc.UpdateSubtaskStatus(context.Background(), subtaskID, "in_progress"); err != nil {
		t.Fatalf("pending->in_progress: %v", err)
	}
	if _, err := svc.UpdateSubtaskStatus(context.Background(), subtaskID, "done"); err != nil {
		t.Fatalf("in_progress->done: %v", err)
	}

	subtasks.items[subtaskID] = repo.ProjectSubtask{ID: subtaskID, WorkStatus: "done"}
	if _, err := svc.UpdateSubtaskStatus(context.Background(), subtaskID, "in_progress"); !errors.Is(err, ErrInvalidSubtaskTransition) {
		t.Fatalf("done->in_progress err = %v, want ErrInvalidSubtaskTransition", err)
	}
}

func TestEnsureActiveExecutionBackfillsMissingCurrentNodeExecution(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	currentNodeID := uuid.New()

	executions := &fakeExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         uuid.New(),
					OrganizationID:    uuid.New(),
					FlowTemplateID:    &flowTemplateID,
					CurrentFlowNodeID: &currentNodeID,
					WorkStatus:        "review",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				currentNodeID: {ID: currentNodeID, NodeType: "review"},
			},
		},
		executions: executions,
	}

	activeExecution, err := svc.EnsureActiveExecution(context.Background(), taskID)
	if err != nil {
		t.Fatalf("EnsureActiveExecution: %v", err)
	}
	if activeExecution == nil {
		t.Fatal("EnsureActiveExecution returned nil execution")
	}
	if activeExecution.FlowNodeID != currentNodeID {
		t.Fatalf("active execution flow_node_id = %s, want %s", activeExecution.FlowNodeID, currentNodeID)
	}
	if executions.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", executions.createCalls)
	}
}

func TestEnsureActiveExecutionRejectsTaskRuntimeStatusMismatch(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	currentNodeID := uuid.New()

	executions := &fakeExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         uuid.New(),
					OrganizationID:    uuid.New(),
					FlowTemplateID:    &flowTemplateID,
					CurrentFlowNodeID: &currentNodeID,
					WorkStatus:        "in_progress",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				currentNodeID: {ID: currentNodeID, NodeType: "review"},
			},
		},
		executions: executions,
	}

	_, err := svc.EnsureActiveExecution(context.Background(), taskID)
	var conflict tasksvc.ErrTaskFlowStateConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("EnsureActiveExecution err = %v, want ErrTaskFlowStateConflict", err)
	}
	if conflict.TargetStatus != "in_progress" || conflict.CurrentNodeType != "review" {
		t.Fatalf("flow conflict = %+v, want target=in_progress current_node_type=review", conflict)
	}
	if executions.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", executions.createCalls)
	}
}

func TestRejectFlowNodeMaxVisitsExceeded(t *testing.T) {
	taskID := uuid.New()
	currentNodeID := uuid.New()
	rejectNodeID := uuid.New()
	execID := uuid.New()
	projectID := uuid.New()

	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         projectID,
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &currentNodeID,
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				currentNodeID: {ID: currentNodeID, RejectNodeID: &rejectNodeID},
				rejectNodeID:  {ID: rejectNodeID, MaxVisits: 10},
			},
		},
		executions: &fakeExecutionRepo{
			active: repo.FlowNodeExecution{
				ID:         execID,
				TaskID:     taskID,
				FlowNodeID: currentNodeID,
				Status:     "active",
			},
			byTask: map[uuid.UUID][]repo.FlowNodeExecution{
				taskID: {{TaskID: taskID, FlowNodeID: rejectNodeID, VisitNumber: 10}},
			},
		},
		taskService: &fakeTaskCoordinator{},
	}

	_, err := svc.RejectFlowNode(context.Background(), taskID, Actor{Type: "system"})
	if !errors.Is(err, ErrMaxVisitsExceeded) {
		t.Fatalf("RejectFlowNode err = %v, want ErrMaxVisitsExceeded", err)
	}
}

func TestRejectFlowNodeFallsBackToPreviousOrderedNodeForReviewWithoutExplicitRejectPath(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
	execID := uuid.New()
	projectID := uuid.New()

	taskRepo := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				ProjectID:         projectID,
				OrganizationID:    uuid.New(),
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &reviewNodeID,
				WorkStatus:        "review",
			},
		},
	}
	taskService := &fakeTaskCoordinator{tasks: taskRepo}
	svc := &service{
		tasks: taskRepo,
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID:   {ID: workNodeID, FlowTemplateID: flowTemplateID, NodeType: "work", Position: 1, MaxVisits: 10},
				reviewNodeID: {ID: reviewNodeID, FlowTemplateID: flowTemplateID, NodeType: "review", Position: 2, MaxVisits: 10},
			},
		},
		executions: &fakeExecutionRepo{
			active: repo.FlowNodeExecution{
				ID:         execID,
				TaskID:     taskID,
				FlowNodeID: reviewNodeID,
				Status:     "active",
			},
			byTask: map[uuid.UUID][]repo.FlowNodeExecution{
				taskID: {
					{ID: execID, TaskID: taskID, FlowNodeID: reviewNodeID, Status: "active", VisitNumber: 1},
				},
			},
		},
		taskService: taskService,
		taskEvents:  &fakeTaskEventRepo{},
		events:      &fakeEventBus{},
	}

	nextExecution, err := svc.RejectFlowNode(context.Background(), taskID, Actor{Type: "human_user", ID: uuid.New()})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if nextExecution.FlowNodeID != workNodeID {
		t.Fatalf("reject execution flow_node_id = %s, want %s", nextExecution.FlowNodeID, workNodeID)
	}
	taskRecord := taskRepo.items[taskID]
	if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != workNodeID {
		t.Fatalf("task current_flow_node_id = %v, want %s", taskRecord.CurrentFlowNodeID, workNodeID)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
}

func TestRejectFlowNodeRejectsTaskRuntimeStatusMismatchBeforeMutatingExecution(t *testing.T) {
	taskID := uuid.New()
	currentNodeID := uuid.New()
	rejectNodeID := uuid.New()
	execID := uuid.New()

	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         execID,
			TaskID:     taskID,
			FlowNodeID: currentNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{
					ID:         execID,
					TaskID:     taskID,
					FlowNodeID: currentNodeID,
					Status:     "active",
				},
			},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         uuid.New(),
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &currentNodeID,
					WorkStatus:        "review",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				currentNodeID: {ID: currentNodeID, NodeType: "work", RejectNodeID: &rejectNodeID},
				rejectNodeID:  {ID: rejectNodeID, NodeType: "work", MaxVisits: 3},
			},
		},
		executions:  executions,
		taskService: &fakeTaskCoordinator{},
	}

	_, err := svc.RejectFlowNode(context.Background(), taskID, Actor{Type: "agent", ID: uuid.New()})
	var conflict tasksvc.ErrTaskFlowStateConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("RejectFlowNode err = %v, want ErrTaskFlowStateConflict", err)
	}
	if conflict.TargetStatus != "review" || conflict.CurrentNodeType != "work" {
		t.Fatalf("flow conflict = %+v, want target=review current_node_type=work", conflict)
	}
	if executions.rejectCalls != 0 {
		t.Fatalf("reject calls = %d, want 0", executions.rejectCalls)
	}
	if executions.updateMetadataCalls != 0 {
		t.Fatalf("update metadata calls = %d, want 0", executions.updateMetadataCalls)
	}
}

func TestAdvanceFlowRejectsSelfReview(t *testing.T) {
	taskID := uuid.New()
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
	executionID := uuid.New()
	projectID := uuid.New()
	workerID := uuid.New()

	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         executionID,
			TaskID:     taskID,
			FlowNodeID: reviewNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{
					ID:         uuid.New(),
					TaskID:     taskID,
					FlowNodeID: workNodeID,
					Status:     "completed",
					Metadata:   executionMetadataWithCompletedBy(nil, Actor{Type: "agent", ID: workerID}),
				},
				{
					ID:         executionID,
					TaskID:     taskID,
					FlowNodeID: reviewNodeID,
					Status:     "active",
				},
			},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         projectID,
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &reviewNodeID,
					WorkStatus:        "review",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID:   {ID: workNodeID, NodeType: "work", NextNodeID: &reviewNodeID},
				reviewNodeID: {ID: reviewNodeID, NodeType: "review"},
			},
		},
		executions:  executions,
		taskService: &fakeTaskCoordinator{},
	}

	if _, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: workerID}); !errors.Is(err, ErrSelfReviewForbidden) {
		t.Fatalf("AdvanceFlow err = %v, want ErrSelfReviewForbidden", err)
	}
	if executions.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want 0", executions.completeCalls)
	}
	if executions.updateMetadataCalls != 0 {
		t.Fatalf("update metadata calls = %d, want 0", executions.updateMetadataCalls)
	}
}

func TestAdvanceFlowTransitionsTaskToReviewWhenNextNodeIsReview(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
	executionID := uuid.New()
	workerID := uuid.New()

	tasks := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				ProjectID:         projectID,
				OrganizationID:    uuid.New(),
				CurrentFlowNodeID: &workNodeID,
				WorkStatus:        "in_progress",
			},
		},
	}
	taskCoordinator := &fakeTaskCoordinator{tasks: tasks}
	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         executionID,
			TaskID:     taskID,
			FlowNodeID: workNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{
					ID:         executionID,
					TaskID:     taskID,
					FlowNodeID: workNodeID,
					Status:     "active",
				},
			},
		},
	}
	svc := &service{
		tasks: tasks,
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID:   {ID: workNodeID, NodeType: "work", NextNodeID: &reviewNodeID},
				reviewNodeID: {ID: reviewNodeID, NodeType: "review"},
			},
		},
		executions:  executions,
		taskService: taskCoordinator,
		taskEvents:  &fakeTaskEventRepo{},
		events:      &fakeEventBus{},
	}

	nextExecution, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: workerID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if len(taskCoordinator.transitions) != 1 {
		t.Fatalf("transition calls = %d, want 1", len(taskCoordinator.transitions))
	}
	if taskCoordinator.transitions[0].status != "review" {
		t.Fatalf("transition status = %q, want review", taskCoordinator.transitions[0].status)
	}

	updatedTask, err := tasks.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != reviewNodeID {
		t.Fatalf("current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, reviewNodeID)
	}
	if nextExecution == nil || nextExecution.FlowNodeID != reviewNodeID {
		t.Fatalf("next execution = %+v, want active review execution", nextExecution)
	}
}

func TestAdvanceFlowDoesNotFailAfterStateTransitionWhenSessionBindingFails(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	currentNodeID := uuid.New()
	nextNodeID := uuid.New()
	execID := uuid.New()

	tasks := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				ProjectID:         projectID,
				OrganizationID:    uuid.New(),
				CurrentFlowNodeID: &currentNodeID,
				WorkStatus:        "in_progress",
				FlowTemplateID: func() *uuid.UUID {
					id := uuid.New()
					return &id
				}(),
			},
		},
	}
	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         execID,
			TaskID:     taskID,
			FlowNodeID: currentNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {{
				ID:         execID,
				TaskID:     taskID,
				FlowNodeID: currentNodeID,
				Status:     "active",
			}},
		},
	}
	taskTransitions := &fakeTaskCoordinator{tasks: tasks}
	svc := &service{
		tasks:       tasks,
		flowNodes:   &fakeNodeRepo{items: map[uuid.UUID]repo.FlowNode{currentNodeID: {ID: currentNodeID, NodeType: "work", NextNodeID: &nextNodeID}, nextNodeID: {ID: nextNodeID, NodeType: "review"}}},
		executions:  executions,
		taskService: taskTransitions,
		taskEvents:  &fakeTaskEventRepo{},
		events:      &fakeEventBus{},
		sessionBridge: &fakeFlowSessionBridge{
			ensureErr: errors.New("session bind failed"),
		},
	}

	nextExecution, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: uuid.New()})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if nextExecution == nil {
		t.Fatal("AdvanceFlow returned nil execution")
	}
	if nextExecution.FlowNodeID != nextNodeID {
		t.Fatalf("next flow_node_id = %s, want %s", nextExecution.FlowNodeID, nextNodeID)
	}
	if nextExecution.SessionID != nil {
		t.Fatalf("next execution session_id = %v, want nil after recoverable bind failure", nextExecution.SessionID)
	}

	updatedTask := tasks.items[taskID]
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nextNodeID {
		t.Fatalf("task current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, nextNodeID)
	}
}

func TestRejectFlowNodeDoesNotFailAfterStateTransitionWhenSessionBindingFails(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	currentNodeID := uuid.New()
	rejectNodeID := uuid.New()
	execID := uuid.New()

	tasks := &fakeTaskRepo{
		items: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				ProjectID:         projectID,
				OrganizationID:    uuid.New(),
				CurrentFlowNodeID: &currentNodeID,
				WorkStatus:        "review",
				FlowTemplateID: func() *uuid.UUID {
					id := uuid.New()
					return &id
				}(),
			},
		},
	}
	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         execID,
			TaskID:     taskID,
			FlowNodeID: currentNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {{
				ID:         execID,
				TaskID:     taskID,
				FlowNodeID: currentNodeID,
				Status:     "active",
			}},
		},
	}
	taskTransitions := &fakeTaskCoordinator{tasks: tasks}
	svc := &service{
		tasks:       tasks,
		flowNodes:   &fakeNodeRepo{items: map[uuid.UUID]repo.FlowNode{currentNodeID: {ID: currentNodeID, NodeType: "review", RejectNodeID: &rejectNodeID}, rejectNodeID: {ID: rejectNodeID, NodeType: "work", MaxVisits: 10}}},
		executions:  executions,
		taskService: taskTransitions,
		taskEvents:  &fakeTaskEventRepo{},
		events:      &fakeEventBus{},
		sessionBridge: &fakeFlowSessionBridge{
			ensureErr: errors.New("session bind failed"),
		},
	}

	nextExecution, err := svc.RejectFlowNode(context.Background(), taskID, Actor{Type: "human_user", ID: uuid.New()})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if nextExecution == nil {
		t.Fatal("RejectFlowNode returned nil execution")
	}
	if nextExecution.FlowNodeID != rejectNodeID {
		t.Fatalf("reject flow_node_id = %s, want %s", nextExecution.FlowNodeID, rejectNodeID)
	}
	if nextExecution.SessionID != nil {
		t.Fatalf("reject execution session_id = %v, want nil after recoverable bind failure", nextExecution.SessionID)
	}

	updatedTask := tasks.items[taskID]
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != rejectNodeID {
		t.Fatalf("task current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, rejectNodeID)
	}
}

func TestAdvanceFlowRejectsPausedProject(t *testing.T) {
	taskID := uuid.New()
	workNodeID := uuid.New()
	projectID := uuid.New()

	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         projectID,
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &workNodeID,
					WorkStatus:        "in_progress",
				},
			},
		},
		projects: &fakeProjectRepo{
			items: map[uuid.UUID]repo.Project{
				projectID: {
					ID:       projectID,
					Settings: json.RawMessage(`{"pause":{"is_paused":true,"reason":"operator pause","metadata":{}}}`),
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID: {ID: workNodeID, NodeType: "work"},
			},
		},
		executions: &fakeExecutionRepo{
			active: repo.FlowNodeExecution{
				ID:         uuid.New(),
				TaskID:     taskID,
				FlowNodeID: workNodeID,
				Status:     "active",
			},
		},
		taskService: &fakeTaskCoordinator{},
	}

	if _, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, projectpause.ErrProjectPaused) {
		t.Fatalf("AdvanceFlow err = %v, want ErrProjectPaused", err)
	}
}

func TestAdvanceFlowRejectsTaskRuntimeStatusMismatchBeforeMutatingExecution(t *testing.T) {
	taskID := uuid.New()
	workNodeID := uuid.New()
	executionID := uuid.New()

	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         executionID,
			TaskID:     taskID,
			FlowNodeID: workNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{
					ID:         executionID,
					TaskID:     taskID,
					FlowNodeID: workNodeID,
					Status:     "active",
				},
			},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         uuid.New(),
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &workNodeID,
					WorkStatus:        "review",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID: {ID: workNodeID, NodeType: "work"},
			},
		},
		executions:  executions,
		taskService: &fakeTaskCoordinator{},
	}

	_, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: uuid.New()})
	var conflict tasksvc.ErrTaskFlowStateConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("AdvanceFlow err = %v, want ErrTaskFlowStateConflict", err)
	}
	if conflict.TargetStatus != "review" || conflict.CurrentNodeType != "work" {
		t.Fatalf("flow conflict = %+v, want target=review current_node_type=work", conflict)
	}
	if executions.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want 0", executions.completeCalls)
	}
	if executions.updateMetadataCalls != 0 {
		t.Fatalf("update metadata calls = %d, want 0", executions.updateMetadataCalls)
	}
}

func TestAdvanceFlowTerminalRequiresReviewedWorkBeforeMutatingExecution(t *testing.T) {
	taskID := uuid.New()
	workNodeID := uuid.New()
	executionID := uuid.New()

	executions := &fakeExecutionRepo{
		active: repo.FlowNodeExecution{
			ID:         executionID,
			TaskID:     taskID,
			FlowNodeID: workNodeID,
			Status:     "active",
		},
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{
					ID:         executionID,
					TaskID:     taskID,
					FlowNodeID: workNodeID,
					Status:     "active",
				},
			},
		},
	}
	svc := &service{
		tasks: &fakeTaskRepo{
			items: map[uuid.UUID]repo.ProjectTask{
				taskID: {
					ID:                taskID,
					ProjectID:         uuid.New(),
					OrganizationID:    uuid.New(),
					CurrentFlowNodeID: &workNodeID,
					WorkStatus:        "in_progress",
				},
			},
		},
		flowNodes: &fakeNodeRepo{
			items: map[uuid.UUID]repo.FlowNode{
				workNodeID: {ID: workNodeID, NodeType: "work"},
			},
		},
		executions:  executions,
		taskService: &fakeTaskCoordinator{},
	}

	if _, err := svc.AdvanceFlow(context.Background(), taskID, Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, tasksvc.ErrDoneRequiresTerminalFlow) {
		t.Fatalf("AdvanceFlow err = %v, want ErrDoneRequiresTerminalFlow", err)
	}
	if executions.completeCalls != 0 {
		t.Fatalf("complete calls = %d, want 0", executions.completeCalls)
	}
	if executions.updateMetadataCalls != 0 {
		t.Fatalf("update metadata calls = %d, want 0", executions.updateMetadataCalls)
	}
}

type fakeExecutionRepo struct {
	active              repo.FlowNodeExecution
	byTask              map[uuid.UUID][]repo.FlowNodeExecution
	byID                map[uuid.UUID]repo.FlowNodeExecution
	createCalls         int
	completeCalls       int
	rejectCalls         int
	updateMetadataCalls int
}

type fakeProjectRepo struct {
	items map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	if item, ok := f.items[id]; ok {
		return item, nil
	}
	return repo.Project{}, repo.ErrNotFound
}

func (f *fakeExecutionRepo) Create(_ context.Context, execution repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	f.createCalls++
	created := execution
	if created.ID == uuid.Nil {
		created.ID = uuid.New()
	}
	if f.byID == nil {
		f.byID = map[uuid.UUID]repo.FlowNodeExecution{}
	}
	if f.byTask == nil {
		f.byTask = map[uuid.UUID][]repo.FlowNodeExecution{}
	}
	f.byID[created.ID] = created
	f.byTask[created.TaskID] = append(f.byTask[created.TaskID], created)
	return created, nil
}
func (f *fakeExecutionRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNodeExecution, error) {
	if item, ok := f.byID[id]; ok {
		return item, nil
	}
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}
func (f *fakeExecutionRepo) GetActive(_ context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error) {
	if f.active.TaskID == taskID && f.active.FlowNodeID == flowNodeID {
		return f.active, nil
	}
	return repo.FlowNodeExecution{}, repo.ErrNotFound
}
func (f *fakeExecutionRepo) ListByTask(_ context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error) {
	return f.byTask[taskID], nil
}
func (f *fakeExecutionRepo) Complete(_ context.Context, _ uuid.UUID) (repo.FlowNodeExecution, error) {
	f.completeCalls++
	return f.active, nil
}
func (f *fakeExecutionRepo) Reject(_ context.Context, _ uuid.UUID) (repo.FlowNodeExecution, error) {
	f.rejectCalls++
	return f.active, nil
}
func (f *fakeExecutionRepo) RecordCommitSHA(_ context.Context, _ uuid.UUID, _ string) (repo.FlowNodeExecution, error) {
	return f.active, nil
}
func (f *fakeExecutionRepo) UpdateMetadata(_ context.Context, _ uuid.UUID, metadata json.RawMessage) (repo.FlowNodeExecution, error) {
	f.updateMetadataCalls++
	f.active.Metadata = metadata
	return f.active, nil
}

type fakeDependencyRepo struct {
	outbound map[uuid.UUID][]repo.ProjectTaskDependency
}

func (f *fakeDependencyRepo) Add(context.Context, repo.ProjectTaskDependency) (repo.ProjectTaskDependency, error) {
	return repo.ProjectTaskDependency{}, nil
}
func (f *fakeDependencyRepo) Remove(context.Context, uuid.UUID) error { return nil }
func (f *fakeDependencyRepo) ListOutbound(_ context.Context, _ string, sourceID uuid.UUID) ([]repo.ProjectTaskDependency, error) {
	return f.outbound[sourceID], nil
}
func (f *fakeDependencyRepo) ListInbound(context.Context, string, uuid.UUID) ([]repo.ProjectTaskDependency, error) {
	return nil, nil
}

type fakeSubtaskRepo struct {
	items       map[uuid.UUID]repo.ProjectSubtask
	createCalls int
}

func (f *fakeSubtaskRepo) Create(_ context.Context, item repo.ProjectSubtask) (repo.ProjectSubtask, error) {
	f.createCalls++
	return item, nil
}
func (f *fakeSubtaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectSubtask, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectSubtask{}, repo.ErrNotFound
	}
	return item, nil
}
func (f *fakeSubtaskRepo) ListByExecution(context.Context, uuid.UUID) ([]repo.ProjectSubtask, error) {
	return nil, nil
}
func (f *fakeSubtaskRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string) (repo.ProjectSubtask, error) {
	item := f.items[id]
	item.WorkStatus = status
	f.items[id] = item
	return item, nil
}
func (f *fakeSubtaskRepo) NextSequenceNumber(context.Context, uuid.UUID) (int, error) { return 1, nil }

type fakeAgentRepo struct {
	err error
}

func (f *fakeAgentRepo) GetByID(context.Context, uuid.UUID) (repo.Agent, error) {
	if f.err != nil {
		return repo.Agent{}, f.err
	}
	return repo.Agent{ID: uuid.New()}, nil
}

type fakeUserRepo struct {
	err error
}

func (f *fakeUserRepo) GetByID(context.Context, uuid.UUID) (repo.HumanUser, error) {
	if f.err != nil {
		return repo.HumanUser{}, f.err
	}
	return repo.HumanUser{ID: uuid.New()}, nil
}

type fakeTaskRepo struct {
	items map[uuid.UUID]repo.ProjectTask
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return item, nil
}
func (f *fakeTaskRepo) SetFlowNode(_ context.Context, id uuid.UUID, flowNodeID *uuid.UUID) (repo.ProjectTask, error) {
	item := f.items[id]
	item.CurrentFlowNodeID = flowNodeID
	f.items[id] = item
	return item, nil
}
func (f *fakeTaskRepo) SetBranch(_ context.Context, id uuid.UUID, branchName *string) (repo.ProjectTask, error) {
	item := f.items[id]
	item.BranchName = branchName
	f.items[id] = item
	return item, nil
}

type fakeNodeRepo struct {
	items map[uuid.UUID]repo.FlowNode
}

func (f *fakeNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	item, ok := f.items[id]
	if !ok {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeNodeRepo) GetByTemplateOrdered(_ context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error) {
	nodes := make([]repo.FlowNode, 0)
	for _, item := range f.items {
		if item.FlowTemplateID == flowTemplateID {
			nodes = append(nodes, item)
		}
	}
	slices.SortFunc(nodes, func(a, b repo.FlowNode) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return nodes, nil
}

type taskTransitionCall struct {
	taskID uuid.UUID
	status string
	actor  tasksvc.Actor
}

type fakeTaskCoordinator struct {
	tasks       *fakeTaskRepo
	transitions []taskTransitionCall
}

func (f *fakeTaskCoordinator) TransitionStatus(_ context.Context, taskID uuid.UUID, status string, actor tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	f.transitions = append(f.transitions, taskTransitionCall{
		taskID: taskID,
		status: strings.TrimSpace(status),
		actor:  actor,
	})
	if f.tasks != nil {
		if taskRecord, ok := f.tasks.items[taskID]; ok {
			taskRecord.WorkStatus = strings.TrimSpace(status)
			f.tasks.items[taskID] = taskRecord
			updated := tasksvc.ProjectTask(taskRecord)
			return &updated, nil
		}
	}
	return &tasksvc.ProjectTask{}, nil
}

func (f *fakeTaskCoordinator) TransitionStatusWithPayload(ctx context.Context, taskID uuid.UUID, status string, actor tasksvc.Actor, _ map[string]any) (*tasksvc.ProjectTask, error) {
	return f.TransitionStatus(ctx, taskID, status, actor)
}

func (f *fakeTaskCoordinator) MarkBlocked(context.Context, uuid.UUID, string, tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	return &tasksvc.ProjectTask{}, nil
}

type fakeTaskEventRepo struct{}

func (f *fakeTaskEventRepo) Record(context.Context, repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error) {
	return repo.ProjectTaskEvent{}, nil
}

type fakeEventBus struct{}

func (f *fakeEventBus) Publish(context.Context, pgx.Tx, eventbus.DomainEvent) error {
	return nil
}

func (f *fakeEventBus) Subscribe(string, *uuid.UUID, eventbus.EventHandler) eventbus.Subscription {
	return eventbus.Subscription{}
}

type fakeFlowSessionBridge struct {
	ensureErr error
}

func (f *fakeFlowSessionBridge) EnsureNodeSession(_ context.Context, _ repo.FlowNodeExecution) (repo.ChatSession, error) {
	if f.ensureErr != nil {
		return repo.ChatSession{}, f.ensureErr
	}
	return repo.ChatSession{ID: uuid.New()}, nil
}

func (f *fakeFlowSessionBridge) RecordCommitSHA(context.Context, uuid.UUID, string) error {
	return nil
}

func TestCreateSubtaskMissingAgentReturnsErrAgentNotFound(t *testing.T) {
	executionID := uuid.New()
	taskID := uuid.New()
	assigneeType := "agent"
	assigneeID := uuid.New()

	subtasks := &fakeSubtaskRepo{items: make(map[uuid.UUID]repo.ProjectSubtask)}
	svc := &service{
		executions: &fakeExecutionRepo{
			byID: map[uuid.UUID]repo.FlowNodeExecution{
				executionID: {
					ID:     executionID,
					TaskID: taskID,
				},
			},
		},
		subtasks: subtasks,
		agents:   &fakeAgentRepo{err: repo.ErrNotFound},
		users:    &fakeUserRepo{},
	}

	_, err := svc.CreateSubtask(context.Background(), executionID, "missing agent", nil, SubtaskAssignee{
		Type: &assigneeType,
		ID:   &assigneeID,
	})
	if !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("CreateSubtask error = %v, want ErrAgentNotFound", err)
	}
	if subtasks.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", subtasks.createCalls)
	}
}
