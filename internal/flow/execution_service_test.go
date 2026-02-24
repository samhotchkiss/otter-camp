package flow

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
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

type fakeExecutionRepo struct {
	active repo.FlowNodeExecution
	byTask map[uuid.UUID][]repo.FlowNodeExecution
}

func (f *fakeExecutionRepo) Create(context.Context, repo.FlowNodeExecution) (repo.FlowNodeExecution, error) {
	return repo.FlowNodeExecution{}, nil
}
func (f *fakeExecutionRepo) GetByID(context.Context, uuid.UUID) (repo.FlowNodeExecution, error) {
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
	return f.active, nil
}
func (f *fakeExecutionRepo) Reject(_ context.Context, _ uuid.UUID) (repo.FlowNodeExecution, error) {
	return f.active, nil
}
func (f *fakeExecutionRepo) RecordCommitSHA(_ context.Context, _ uuid.UUID, _ string) (repo.FlowNodeExecution, error) {
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
	items map[uuid.UUID]repo.ProjectSubtask
}

func (f *fakeSubtaskRepo) Create(context.Context, repo.ProjectSubtask) (repo.ProjectSubtask, error) {
	return repo.ProjectSubtask{}, nil
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
