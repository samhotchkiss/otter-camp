package flow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

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
					WorkStatus:        "in_progress",
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

func (f *fakeExecutionRepo) Create(context.Context, repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, nil
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

type fakeTaskCoordinator struct{}

func (f *fakeTaskCoordinator) TransitionStatus(context.Context, uuid.UUID, string, tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	return &tasksvc.ProjectTask{}, nil
}
func (f *fakeTaskCoordinator) MarkBlocked(context.Context, uuid.UUID, string, tasksvc.Actor) (*tasksvc.ProjectTask, error) {
	return &tasksvc.ProjectTask{}, nil
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
