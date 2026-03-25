//go:build integration

package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestFlowNodeExecutionRepoLifecycleAndGetActive(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	executionRepo := NewFlowNodeExecutionRepo(pool)

	firstStarted := time.Now().UTC().Add(-2 * time.Minute)
	first, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:      fixture.Task.ID,
		FlowNodeID:  fixture.FlowNode.ID,
		VisitNumber: 1,
		StartedAt:   firstStarted,
	})
	if err != nil {
		t.Fatalf("Create first execution: %v", err)
	}

	active, err := executionRepo.GetActive(ctx, fixture.Task.ID, fixture.FlowNode.ID)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if active.ID != first.ID {
		t.Fatalf("GetActive ID = %s, want %s", active.ID, first.ID)
	}
	if active.RuntimeSubstate == nil || *active.RuntimeSubstate != "waiting_for_turn" {
		t.Fatalf("GetActive runtime_substate = %v, want waiting_for_turn", active.RuntimeSubstate)
	}

	completed, err := executionRepo.Complete(ctx, first.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.CompletedAt == nil {
		t.Fatal("Complete completed_at = nil, want non-nil")
	}
	if completed.RuntimeSubstate != nil {
		t.Fatalf("Complete runtime_substate = %v, want nil", completed.RuntimeSubstate)
	}

	if _, err := executionRepo.GetActive(ctx, fixture.Task.ID, fixture.FlowNode.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActive after complete err = %v, want ErrNotFound", err)
	}
}

func TestFlowNodeExecutionRepoRejectsDuplicateActiveExecutionForTaskNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	executionRepo := NewFlowNodeExecutionRepo(pool)

	first, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:      fixture.Task.ID,
		FlowNodeID:  fixture.FlowNode.ID,
		VisitNumber: 1,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create first execution: %v", err)
	}

	_, err = executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:      fixture.Task.ID,
		FlowNodeID:  fixture.FlowNode.ID,
		VisitNumber: 2,
		Status:      "active",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Create duplicate active execution err = %v, want ErrConflict", err)
	}

	active, err := executionRepo.GetActive(ctx, fixture.Task.ID, fixture.FlowNode.ID)
	if err != nil {
		t.Fatalf("GetActive after duplicate conflict: %v", err)
	}
	if active.ID != first.ID {
		t.Fatalf("GetActive ID = %s, want %s", active.ID, first.ID)
	}
}

func TestFlowNodeExecutionRepoRejectAndVisitIncrement(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	executionRepo := NewFlowNodeExecutionRepo(pool)

	first, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:      fixture.Task.ID,
		FlowNodeID:  fixture.FlowNode.ID,
		VisitNumber: 1,
	})
	if err != nil {
		t.Fatalf("Create first execution: %v", err)
	}

	rejected, err := executionRepo.Reject(ctx, first.ID)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != "rejected" {
		t.Fatalf("Reject status = %q, want %q", rejected.Status, "rejected")
	}
	if rejected.CompletedAt == nil {
		t.Fatal("Reject completed_at = nil, want non-nil")
	}
	if rejected.RuntimeSubstate != nil {
		t.Fatalf("Reject runtime_substate = %v, want nil", rejected.RuntimeSubstate)
	}

	second, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:      fixture.Task.ID,
		FlowNodeID:  fixture.FlowNode.ID,
		VisitNumber: 2,
		Status:      "active",
	})
	if err != nil {
		t.Fatalf("Create second execution: %v", err)
	}
	if second.VisitNumber != 2 {
		t.Fatalf("second visit_number = %d, want 2", second.VisitNumber)
	}
	if second.RuntimeSubstate == nil || *second.RuntimeSubstate != "waiting_for_turn" {
		t.Fatalf("second runtime_substate = %v, want waiting_for_turn", second.RuntimeSubstate)
	}
}

func TestFlowNodeExecutionRepoUpdateRuntimeSubstate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	executionRepo := NewFlowNodeExecutionRepo(pool)

	execution, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:     fixture.Task.ID,
		FlowNodeID: fixture.FlowNode.ID,
	})
	if err != nil {
		t.Fatalf("Create execution: %v", err)
	}

	running := "running"
	updated, err := executionRepo.UpdateRuntimeSubstate(ctx, execution.ID, &running)
	if err != nil {
		t.Fatalf("UpdateRuntimeSubstate: %v", err)
	}
	if updated.RuntimeSubstate == nil || *updated.RuntimeSubstate != running {
		t.Fatalf("updated runtime_substate = %v, want %q", updated.RuntimeSubstate, running)
	}

	cleared, err := executionRepo.Complete(ctx, execution.ID)
	if err != nil {
		t.Fatalf("Complete execution: %v", err)
	}
	if cleared.RuntimeSubstate != nil {
		t.Fatalf("completed runtime_substate = %v, want nil", cleared.RuntimeSubstate)
	}

	recoveryPending := "recovery_pending"
	terminalUpdate, err := executionRepo.UpdateRuntimeSubstate(ctx, execution.ID, &recoveryPending)
	if err != nil {
		t.Fatalf("UpdateRuntimeSubstate terminal execution: %v", err)
	}
	if terminalUpdate.RuntimeSubstate != nil {
		t.Fatalf("terminal runtime_substate = %v, want nil", terminalUpdate.RuntimeSubstate)
	}
}

func TestProjectSubtaskRepoSequenceAndUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	executionRepo := NewFlowNodeExecutionRepo(pool)
	subtaskRepo := NewProjectSubtaskRepo(pool)

	execution, err := executionRepo.Create(ctx, FlowNodeExecution{
		TaskID:     fixture.Task.ID,
		FlowNodeID: fixture.FlowNode.ID,
	})
	if err != nil {
		t.Fatalf("Create execution: %v", err)
	}

	next, err := subtaskRepo.NextSequenceNumber(ctx, execution.ID)
	if err != nil {
		t.Fatalf("NextSequenceNumber empty: %v", err)
	}
	if next != 1 {
		t.Fatalf("NextSequenceNumber empty = %d, want 1", next)
	}

	if _, err := subtaskRepo.Create(ctx, ProjectSubtask{
		TaskID:              fixture.Task.ID,
		FlowNodeExecutionID: execution.ID,
		Title:               "First",
		WorkStatus:          "pending",
		SequenceNumber:      1,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("Create first subtask: %v", err)
	}

	if _, err := subtaskRepo.Create(ctx, ProjectSubtask{
		TaskID:              fixture.Task.ID,
		FlowNodeExecutionID: execution.ID,
		Title:               "Third",
		WorkStatus:          "pending",
		SequenceNumber:      3,
		CreatedByType:       "system",
	}); err != nil {
		t.Fatalf("Create third subtask: %v", err)
	}

	next, err = subtaskRepo.NextSequenceNumber(ctx, execution.ID)
	if err != nil {
		t.Fatalf("NextSequenceNumber max=3: %v", err)
	}
	if next != 4 {
		t.Fatalf("NextSequenceNumber max=3 = %d, want 4", next)
	}

	_, err = subtaskRepo.Create(ctx, ProjectSubtask{
		TaskID:              fixture.Task.ID,
		FlowNodeExecutionID: execution.ID,
		Title:               "Duplicate sequence",
		WorkStatus:          "pending",
		SequenceNumber:      1,
		CreatedByType:       "system",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate sequence err = %v, want ErrConflict", err)
	}
}

func TestProjectTaskDependencyRepoConstraintsAndCycleCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	taskRepo := NewProjectTaskRepo(pool)
	dependencyRepo := NewProjectTaskDependencyRepo(pool)

	taskB := seedFlowExecutionTask(t, ctx, taskRepo, fixture.Organization.ID, fixture.Project.ID, "Task B")
	taskC := seedFlowExecutionTask(t, ctx, taskRepo, fixture.Organization.ID, fixture.Project.ID, "Task C")
	taskD := seedFlowExecutionTask(t, ctx, taskRepo, fixture.Organization.ID, fixture.Project.ID, "Task D")

	if _, err := dependencyRepo.Add(ctx, ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      fixture.Task.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("Add A->B: %v", err)
	}
	if _, err := dependencyRepo.Add(ctx, ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      taskB.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskC.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("Add B->C: %v", err)
	}

	hasCycle, err := dependencyRepo.CheckCycle(ctx, "project_task", taskC.ID, fixture.Task.ID)
	if err != nil {
		t.Fatalf("CheckCycle C->A: %v", err)
	}
	if !hasCycle {
		t.Fatal("CheckCycle C->A = false, want true")
	}

	noCycle, err := dependencyRepo.CheckCycle(ctx, "project_task", taskD.ID, fixture.Task.ID)
	if err != nil {
		t.Fatalf("CheckCycle D->A: %v", err)
	}
	if noCycle {
		t.Fatal("CheckCycle D->A = true, want false")
	}

	_, err = dependencyRepo.Add(ctx, ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      fixture.Task.ID,
		DependsOnType: "project_subtask",
		DependsOnID:   uuid.New(),
		CreatedByType: "system",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-level dependency err = %v, want ErrConflict", err)
	}

	_, err = dependencyRepo.Add(ctx, ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      fixture.Task.ID,
		DependsOnType: "project_task",
		DependsOnID:   fixture.Task.ID,
		CreatedByType: "system",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("self-dependency err = %v, want ErrConflict", err)
	}
}

func TestProjectTaskParticipantRepoSoftRemoveAndLookup(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowExecutionFixture(t, ctx, pool)

	participantRepo := NewProjectTaskParticipantRepo(pool)
	participantID := uuid.New()

	created, err := participantRepo.Add(ctx, ProjectTaskParticipant{
		TaskID:          fixture.Task.ID,
		ParticipantType: "agent",
		ParticipantID:   participantID,
		Role:            "worker",
	})
	if err != nil {
		t.Fatalf("Add participant: %v", err)
	}

	_, err = participantRepo.Add(ctx, ProjectTaskParticipant{
		TaskID:          fixture.Task.ID,
		ParticipantType: "agent",
		ParticipantID:   participantID,
		Role:            "worker",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate participant err = %v, want ErrConflict", err)
	}

	activeBefore, err := participantRepo.ListActive(ctx, fixture.Task.ID)
	if err != nil {
		t.Fatalf("ListActive before remove: %v", err)
	}
	if len(activeBefore) != 1 || activeBefore[0].ID != created.ID {
		t.Fatalf("ListActive before remove = %+v, want only %s", activeBefore, created.ID)
	}

	removed, err := participantRepo.Remove(ctx, fixture.Task.ID, "agent", participantID)
	if err != nil {
		t.Fatalf("Remove participant: %v", err)
	}
	if removed.LeftAt == nil {
		t.Fatal("Remove left_at = nil, want non-nil")
	}

	activeAfter, err := participantRepo.ListActive(ctx, fixture.Task.ID)
	if err != nil {
		t.Fatalf("ListActive after remove: %v", err)
	}
	if len(activeAfter) != 0 {
		t.Fatalf("ListActive after remove len = %d, want 0", len(activeAfter))
	}

	found, err := participantRepo.GetByParticipant(ctx, fixture.Task.ID, "agent", participantID)
	if err != nil {
		t.Fatalf("GetByParticipant after remove: %v", err)
	}
	if found.ID != created.ID {
		t.Fatalf("GetByParticipant ID = %s, want %s", found.ID, created.ID)
	}
	if found.LeftAt == nil {
		t.Fatal("GetByParticipant left_at = nil, want non-nil")
	}
}

type flowExecutionFixture struct {
	Organization Organization
	Project      Project
	FlowTemplate FlowTemplate
	FlowNode     FlowNode
	Task         ProjectTask
}

func seedFlowExecutionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) flowExecutionFixture {
	t.Helper()

	orgRepo := NewOrgRepo(pool)
	projectRepo := NewProjectRepo(pool)
	flowTemplateRepo := NewFlowTemplateRepo(pool)
	flowNodeRepo := NewFlowNodeRepo(pool)
	taskRepo := NewProjectTaskRepo(pool)

	org, err := orgRepo.Create(ctx, Organization{
		Slug:        "flow-execution-org-" + uuid.NewString()[:8],
		DisplayName: "Flow Execution Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	project, err := projectRepo.Create(ctx, Project{
		OrganizationID: org.ID,
		Slug:           "flow-execution-project-" + uuid.NewString()[:8],
		DisplayName:    "Flow Execution Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	template, err := flowTemplateRepo.Create(ctx, FlowTemplate{
		OrganizationID: &org.ID,
		ProjectID:      &project.ID,
		Slug:           "flow-execution-template-" + uuid.NewString()[:8],
		DisplayName:    "Flow Execution Template",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	flowNode, err := flowNodeRepo.Create(ctx, FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Flow Execution Node",
		NodeType:       "work",
		Position:       1,
	})
	if err != nil {
		t.Fatalf("create flow node: %v", err)
	}

	task := seedFlowExecutionTask(t, ctx, taskRepo, org.ID, project.ID, "Flow Execution Task")

	return flowExecutionFixture{
		Organization: org,
		Project:      project,
		FlowTemplate: template,
		FlowNode:     flowNode,
		Task:         task,
	}
}

func seedFlowExecutionTask(t *testing.T, ctx context.Context, taskRepo *ProjectTaskRepo, orgID, projectID uuid.UUID, title string) ProjectTask {
	t.Helper()

	task, err := taskRepo.Create(ctx, ProjectTask{
		OrganizationID: orgID,
		ProjectID:      projectID,
		Title:          title,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}
