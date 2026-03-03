package task

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestStatusTransitionMatrix(t *testing.T) {
	valid := [][2]string{
		{"draft", "queued"},
		{"draft", "cancelled"},
		{"queued", "in_progress"},
		{"queued", "cancelled"},
		{"in_progress", "blocked"},
		{"in_progress", "on_hold"},
		{"in_progress", "review"},
		{"in_progress", "done"},
		{"in_progress", "cancelled"},
		{"blocked", "queued"},
		{"blocked", "cancelled"},
		{"on_hold", "in_progress"},
		{"on_hold", "cancelled"},
		{"review", "done"},
		{"review", "in_progress"},
		{"review", "cancelled"},
		{"done", "cancelled"},
		{"cancelled", "cancelled"},
	}
	for _, pair := range valid {
		if !isTransitionAllowed(pair[0], pair[1]) {
			t.Fatalf("expected transition %s -> %s to be valid", pair[0], pair[1])
		}
	}

	invalid := [][2]string{
		{"done", "queued"},
		{"done", "draft"},
		{"cancelled", "queued"},
		{"cancelled", "done"},
		{"draft", "in_progress"},
		{"draft", "review"},
		{"queued", "done"},
		{"queued", "review"},
		{"blocked", "in_progress"},
		{"blocked", "review"},
		{"on_hold", "queued"},
		{"on_hold", "done"},
		{"review", "queued"},
		{"review", "blocked"},
		{"in_progress", "draft"},
		{"in_progress", "queued"},
	}
	for _, pair := range invalid {
		if isTransitionAllowed(pair[0], pair[1]) {
			t.Fatalf("expected transition %s -> %s to be invalid", pair[0], pair[1])
		}
	}
}

func TestTransitionStatusInvalidReturnsTypedError(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "done",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"})
	if err == nil {
		t.Fatal("expected error")
	}
	var transitionErr ErrInvalidStatusTransition
	if !errors.As(err, &transitionErr) {
		t.Fatalf("error = %v, want ErrInvalidStatusTransition", err)
	}
	if transitionErr.From != "done" || transitionErr.To != "queued" {
		t.Fatalf("transition error = %+v, want from=done to=queued", transitionErr)
	}
}

func TestTransitionStatusCompletedAtBehavior(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "in_progress",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}
	svc := newUnitService(taskRepo)
	svc.clock = clock.NewFake(now)

	doneTask, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "system", AllowDoneBypass: true})
	if err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}
	if doneTask.CompletedAt == nil {
		t.Fatal("completed_at is nil for done transition")
	}

	taskRepo.tasks[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: doneTask.OrganizationID,
		ProjectID:      doneTask.ProjectID,
		WorkStatus:     "in_progress",
		Title:          "Task",
		CreatedByType:  "system",
	}
	holdTask, err := svc.TransitionStatus(context.Background(), taskID, "on_hold", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus on_hold: %v", err)
	}
	if holdTask.CompletedAt != nil {
		t.Fatalf("completed_at = %v, want nil for on_hold transition", holdTask.CompletedAt)
	}

	taskRepo.tasks[taskID] = repo.ProjectTask{
		ID:             taskID,
		OrganizationID: doneTask.OrganizationID,
		ProjectID:      doneTask.ProjectID,
		WorkStatus:     "in_progress",
		Title:          "Task",
		CreatedByType:  "system",
	}
	cancelledTask, err := svc.TransitionStatus(context.Background(), taskID, "cancelled", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus cancelled: %v", err)
	}
	if cancelledTask.CompletedAt == nil {
		t.Fatal("completed_at is nil for cancelled transition")
	}
}

func TestTransitionStatusRequiresHumanApprovalGate(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                  taskID,
				OrganizationID:      uuid.New(),
				ProjectID:           uuid.New(),
				WorkStatus:          "draft",
				Title:               "Task",
				CreatedByType:       "system",
				RequiresHumanReview: true,
			},
		},
	}

	svc := newUnitService(taskRepo)
	if _, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrRequiresHumanApproval) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrRequiresHumanApproval", err)
	}
}

func TestTransitionStatusDraftToQueuedRequiresFlowTemplate(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "draft",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	if _, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrFlowTemplateRequired) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrFlowTemplateRequired", err)
	}
}

func TestTransitionStatusDraftToQueuedWithFlowTemplateSucceeds(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	updated, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if updated.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", updated.WorkStatus)
	}
}

func TestTransitionStatusDraftToCancelledWithoutFlowTemplateSucceeds(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "draft",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	updated, err := svc.TransitionStatus(context.Background(), taskID, "cancelled", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus cancelled: %v", err)
	}
	if updated.WorkStatus != "cancelled" {
		t.Fatalf("work_status = %q, want cancelled", updated.WorkStatus)
	}
}

func TestTransitionStatusDoneWithoutFlowTemplateReturnsError(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "review",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}
	svc := newUnitService(taskRepo)

	if _, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, ErrDoneRequiresTerminalFlow) {
		t.Fatalf("TransitionStatus done err = %v, want ErrDoneRequiresTerminalFlow", err)
	}
}

func TestTransitionStatusDoneWithNonTerminalFlowNodeReturnsError(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	nextNodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    uuid.New(),
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &flowNodeID,
				Title:             "Task",
				CreatedByType:     "system",
			},
		},
	}
	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			flowNodeID: {ID: flowNodeID, NextNodeID: &nextNodeID},
		},
	}
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "active"},
			},
		},
	}

	if _, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, ErrDoneRequiresTerminalFlow) {
		t.Fatalf("TransitionStatus done err = %v, want ErrDoneRequiresTerminalFlow", err)
	}
}

func TestTransitionStatusDoneWithCompletedTerminalFlowNodeSucceeds(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	flowNodeID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    uuid.New(),
				ProjectID:         uuid.New(),
				WorkStatus:        "review",
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &flowNodeID,
				Title:             "Task",
				CreatedByType:     "system",
			},
		},
	}
	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			flowNodeID: {ID: flowNodeID, NextNodeID: nil},
		},
	}
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "completed"},
			},
		},
	}

	updated, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "agent", ID: uuid.New()})
	if err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}
	if updated.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", updated.WorkStatus)
	}
}

func TestTransitionStatusInProgressWithoutActiveFlowReturnsError(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{Type: "human_user", ID: uuid.New()})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrActiveFlowRequired) {
		t.Fatalf("TransitionStatus err = %v, want ErrActiveFlowRequired", err)
	}
	var typedErr ErrInProgressRequiresActiveFlow
	if !errors.As(err, &typedErr) {
		t.Fatalf("TransitionStatus err = %v, want ErrInProgressRequiresActiveFlow", err)
	}
	if typedErr.TaskID != taskID {
		t.Fatalf("typed error task_id = %s, want %s", typedErr.TaskID, taskID)
	}
}

func TestTransitionStatusInProgressWithActiveFlowSucceeds(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	flowRepo := &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: uuid.New(), Status: "active"},
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.executions = flowRepo

	updated, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{Type: "human_user", ID: uuid.New()})
	if err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}
	if updated.WorkStatus != "in_progress" {
		t.Fatalf("work_status = %q, want %q", updated.WorkStatus, "in_progress")
	}
}

func TestTransitionStatusInProgressSystemOverrideBypassesFlowValidation(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)

	updated, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{
		Type:              "system",
		AllowNoActiveFlow: true,
	})
	if err != nil {
		t.Fatalf("TransitionStatus in_progress with override: %v", err)
	}
	if updated.WorkStatus != "in_progress" {
		t.Fatalf("work_status = %q, want %q", updated.WorkStatus, "in_progress")
	}
}

func TestTransitionStatusCancelledArchivesActiveMergeQueueEntries(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	taskID := uuid.New()
	otherTaskID := uuid.New()
	projectID := uuid.New()

	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				WorkStatus:     "review",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}
	queueRepo := &fakeQueueRepo{
		entries: []repo.MergeQueueEntry{
			{ID: uuid.New(), ProjectID: projectID, TaskID: taskID, Status: "queued"},
			{ID: uuid.New(), ProjectID: projectID, TaskID: otherTaskID, Status: "queued"},
		},
	}

	svc := newUnitService(taskRepo)
	svc.queue = queueRepo
	svc.clock = clock.NewFake(now)

	if _, err := svc.TransitionStatus(context.Background(), taskID, "cancelled", Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus cancelled: %v", err)
	}

	if queueRepo.entries[0].ArchivedAt == nil {
		t.Fatal("task merge queue entry archived_at = nil, want non-nil")
	}
	if queueRepo.entries[1].ArchivedAt != nil {
		t.Fatalf("other task merge queue entry archived_at = %v, want nil", queueRepo.entries[1].ArchivedAt)
	}
}

func TestMarkBlockedCreatesResolutionTaskTitle(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	taskID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()
	pmAgentID := uuid.New()
	pmUserID := uuid.New()

	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     3,
				Title:          "Blocked task",
				WorkStatus:     "in_progress",
				CreatedByType:  "system",
			},
		},
	}
	projectRepo := &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:             projectID,
				OrganizationID: orgID,
				Slug:           "alpha",
			},
		},
	}
	assignments := &fakeAssignmentRepo{
		pmByProject: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {
				ProjectID: projectID,
				AgentID:   pmAgentID,
				IsActive:  true,
				Role:      "pm",
			},
		},
	}
	agents := &fakeAgentRepo{
		agents: map[uuid.UUID]repo.Agent{
			pmAgentID: {
				ID:             pmAgentID,
				OrganizationID: orgID,
				AgentClass:     "staff",
				CreatedByType:  "human_user",
				CreatedByID:    pmUserID,
			},
		},
	}
	users := &fakeUserRepo{
		usersByID: map[uuid.UUID]repo.HumanUser{
			pmUserID: {ID: pmUserID, OrganizationID: orgID, Role: "admin", IsActive: true},
		},
		usersByOrg: map[uuid.UUID][]repo.HumanUser{
			orgID: {{ID: pmUserID, OrganizationID: orgID, Role: "admin", IsActive: true}},
		},
	}

	svc := newUnitService(taskRepo)
	svc.project = projectRepo
	svc.assignments = assignments
	svc.agents = agents
	svc.users = users
	svc.clock = clock.NewFake(now)

	_, err := svc.MarkBlocked(context.Background(), taskID, "blocked by dependency", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	if len(taskRepo.createdTasks) == 0 {
		t.Fatal("expected resolution task to be created")
	}
	resolution := taskRepo.createdTasks[len(taskRepo.createdTasks)-1]
	if resolution.Title != "Resolve blocker for task alpha-3" {
		t.Fatalf("resolution title = %q, want %q", resolution.Title, "Resolve blocker for task alpha-3")
	}
}

func TestEnqueueForMergeUsesSequentialPosition(t *testing.T) {
	projectID := uuid.New()
	taskAID := uuid.New()
	taskBID := uuid.New()
	branchA := "feature/a"
	branchB := "feature/b"

	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskAID: {ID: taskAID, ProjectID: projectID, BranchName: &branchA},
			taskBID: {ID: taskBID, ProjectID: projectID, BranchName: &branchB},
		},
	}
	queueRepo := &fakeQueueRepo{}
	svc := newUnitService(taskRepo)
	svc.queue = queueRepo

	first, err := svc.EnqueueForMerge(context.Background(), taskAID)
	if err != nil {
		t.Fatalf("EnqueueForMerge first: %v", err)
	}
	second, err := svc.EnqueueForMerge(context.Background(), taskBID)
	if err != nil {
		t.Fatalf("EnqueueForMerge second: %v", err)
	}
	if first.Position != 1 {
		t.Fatalf("first position = %d, want 1", first.Position)
	}
	if second.Position != 2 {
		t.Fatalf("second position = %d, want 2", second.Position)
	}
}

func newUnitService(taskRepo *fakeTaskRepo) *service {
	return &service{
		tasks:       taskRepo,
		events:      &fakeTaskEventRepo{},
		inbox:       &fakeInboxRepo{},
		queue:       &fakeQueueRepo{},
		project:     &fakeProjectRepo{projects: make(map[uuid.UUID]repo.Project)},
		assignments: &fakeAssignmentRepo{},
		agents:      &fakeAgentRepo{},
		users:       &fakeUserRepo{},
		executions:  &fakeFlowExecutionRepo{},
		flowNodes:   &fakeFlowNodeRepo{nodes: map[uuid.UUID]repo.FlowNode{}},
		eventBus:    &fakeEventBus{},
		clock:       clock.NewFake(time.Now().UTC()),
	}
}

type fakeTaskRepo struct {
	tasks        map[uuid.UUID]repo.ProjectTask
	createdTasks []repo.ProjectTask
}

func (f *fakeTaskRepo) Create(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if f.tasks == nil {
		f.tasks = make(map[uuid.UUID]repo.ProjectTask)
	}
	created := task
	if created.ID == uuid.Nil {
		created.ID = uuid.New()
	}
	if created.TaskNumber == 0 {
		created.TaskNumber = len(f.tasks) + 1
	}
	f.tasks[created.ID] = created
	f.createdTasks = append(f.createdTasks, created)
	return created, nil
}

func (f *fakeTaskRepo) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectTask, error) {
	task, ok := f.tasks[id]
	if !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	return task, nil
}

func (f *fakeTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if _, ok := f.tasks[task.ID]; !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	f.tasks[task.ID] = task
	return task, nil
}

type fakeTaskEventRepo struct {
	events []repo.ProjectTaskEvent
}

func (f *fakeTaskEventRepo) Record(_ context.Context, event repo.ProjectTaskEvent) (repo.ProjectTaskEvent, error) {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}
	f.events = append(f.events, event)
	return event, nil
}

type fakeInboxRepo struct {
	items []repo.InboxItem
}

func (f *fakeInboxRepo) Create(_ context.Context, item repo.InboxItem) (repo.InboxItem, error) {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	f.items = append(f.items, item)
	return item, nil
}

func (f *fakeInboxRepo) GetByID(_ context.Context, id uuid.UUID) (repo.InboxItem, error) {
	for _, item := range f.items {
		if item.ID == id {
			return item, nil
		}
	}
	return repo.InboxItem{}, repo.ErrNotFound
}

func (f *fakeInboxRepo) ListForUser(_ context.Context, _, _ uuid.UUID, _ repo.InboxListOptions) ([]repo.InboxItem, error) {
	cloned := make([]repo.InboxItem, 0, len(f.items))
	cloned = append(cloned, f.items...)
	return cloned, nil
}

func (f *fakeInboxRepo) MarkActed(_ context.Context, id, actedByID uuid.UUID) (repo.InboxItem, error) {
	for i := range f.items {
		if f.items[i].ID != id {
			continue
		}
		now := time.Now().UTC()
		f.items[i].IsActed = true
		f.items[i].ActedAt = &now
		f.items[i].ActedByID = &actedByID
		return f.items[i], nil
	}
	return repo.InboxItem{}, repo.ErrNotFound
}

type fakeQueueRepo struct {
	entries []repo.MergeQueueEntry
}

type fakeFlowExecutionRepo struct {
	byTask map[uuid.UUID][]repo.FlowNodeExecution
}

func (f *fakeFlowExecutionRepo) ListByTask(_ context.Context, taskID uuid.UUID) ([]repo.FlowNodeExecution, error) {
	if f.byTask == nil {
		return []repo.FlowNodeExecution{}, nil
	}
	items, ok := f.byTask[taskID]
	if !ok {
		return []repo.FlowNodeExecution{}, nil
	}
	out := make([]repo.FlowNodeExecution, 0, len(items))
	out = append(out, items...)
	return out, nil
}

type fakeFlowNodeRepo struct {
	nodes map[uuid.UUID]repo.FlowNode
}

func (f *fakeFlowNodeRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowNode, error) {
	if f.nodes == nil {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	item, ok := f.nodes[id]
	if !ok {
		return repo.FlowNode{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeQueueRepo) Enqueue(_ context.Context, entry repo.MergeQueueEntry) (repo.MergeQueueEntry, error) {
	created := entry
	if created.ID == uuid.Nil {
		created.ID = uuid.New()
	}
	created.Position = len(f.entries) + 1
	f.entries = append(f.entries, created)
	return created, nil
}

func (f *fakeQueueRepo) ListActive(_ context.Context, projectID uuid.UUID) ([]repo.MergeQueueEntry, error) {
	out := make([]repo.MergeQueueEntry, 0, len(f.entries))
	for _, entry := range f.entries {
		if entry.ProjectID == projectID && entry.ArchivedAt == nil {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *fakeQueueRepo) UpdateStatus(_ context.Context, id uuid.UUID, status string, failureReason *string, mergedAt *time.Time) (repo.MergeQueueEntry, error) {
	for i := range f.entries {
		if f.entries[i].ID != id {
			continue
		}
		f.entries[i].Status = status
		f.entries[i].FailureReason = failureReason
		f.entries[i].MergedAt = mergedAt
		return f.entries[i], nil
	}
	return repo.MergeQueueEntry{}, repo.ErrNotFound
}

func (f *fakeQueueRepo) Archive(_ context.Context, id uuid.UUID, archivedAt time.Time) (repo.MergeQueueEntry, error) {
	for i := range f.entries {
		if f.entries[i].ID != id {
			continue
		}
		ts := archivedAt.UTC()
		f.entries[i].ArchivedAt = &ts
		return f.entries[i], nil
	}
	return repo.MergeQueueEntry{}, repo.ErrNotFound
}

type fakeProjectRepo struct {
	projects map[uuid.UUID]repo.Project
}

func (f *fakeProjectRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	projectRecord, ok := f.projects[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return projectRecord, nil
}

type fakeAssignmentRepo struct {
	assignmentsByAgent map[uuid.UUID]repo.AgentProjectAssignment
	pmByProject        map[uuid.UUID]repo.AgentProjectAssignment
}

func (f *fakeAssignmentRepo) GetByAgentAndProject(_ context.Context, agentID, _ uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.assignmentsByAgent == nil {
		return repo.AgentProjectAssignment{}, repo.ErrNotFound
	}
	item, ok := f.assignmentsByAgent[agentID]
	if !ok {
		return repo.AgentProjectAssignment{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeAssignmentRepo) GetPM(_ context.Context, projectID uuid.UUID) (repo.AgentProjectAssignment, error) {
	if f.pmByProject == nil {
		return repo.AgentProjectAssignment{}, repo.ErrNotFound
	}
	item, ok := f.pmByProject[projectID]
	if !ok {
		return repo.AgentProjectAssignment{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeAgentRepo struct {
	agents map[uuid.UUID]repo.Agent
}

func (f *fakeAgentRepo) GetByID(_ context.Context, id uuid.UUID) (repo.Agent, error) {
	if f.agents == nil {
		return repo.Agent{}, repo.ErrNotFound
	}
	item, ok := f.agents[id]
	if !ok {
		return repo.Agent{}, repo.ErrNotFound
	}
	return item, nil
}

type fakeUserRepo struct {
	usersByID  map[uuid.UUID]repo.HumanUser
	usersByOrg map[uuid.UUID][]repo.HumanUser
}

func (f *fakeUserRepo) GetByID(_ context.Context, id uuid.UUID) (repo.HumanUser, error) {
	if f.usersByID == nil {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	item, ok := f.usersByID[id]
	if !ok {
		return repo.HumanUser{}, repo.ErrNotFound
	}
	return item, nil
}

func (f *fakeUserRepo) List(_ context.Context, organizationID uuid.UUID) ([]repo.HumanUser, error) {
	if f.usersByOrg == nil {
		return nil, nil
	}
	out := make([]repo.HumanUser, 0, len(f.usersByOrg[organizationID]))
	out = append(out, f.usersByOrg[organizationID]...)
	return out, nil
}

type fakeEventBus struct {
	events []eventbus.DomainEvent
}

func (f *fakeEventBus) Publish(_ context.Context, _ pgx.Tx, event eventbus.DomainEvent) error {
	f.events = append(f.events, event)
	return nil
}

func TestExtractReason(t *testing.T) {
	if got := extractReason(json.RawMessage(`{"reason":"  blocked  "}`)); got != "blocked" {
		t.Fatalf("extractReason = %q, want %q", got, "blocked")
	}
}
