//go:build integration

package flow

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestFlowExecutionServiceStartFlowWithDefaultSingleAgentTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)

	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:      pool,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		SkillsDir: t.TempDir(),
		OrgSlug:   "default",
		OrgName:   "OtterCamp",
		Version:   "test-version",
	})
	if err := bootstrapper.Run(ctx); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, err := fixture.templateRepo.GetCurrentBySlug(ctx, nil, nil, "default-single-agent")
	if err != nil {
		t.Fatalf("GetCurrentBySlug default-single-agent: %v", err)
	}
	if template.StartNodeID == nil {
		t.Fatal("default-single-agent start_node_id is nil")
	}

	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task (default template)", "in_progress", &template.ID)

	started, err := fixture.service.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.FlowNodeID != *template.StartNodeID {
		t.Fatalf("start flow_node_id = %s, want %s", started.FlowNodeID, *template.StartNodeID)
	}

	var executionCount int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flow_node_execution
		WHERE task_id = $1
	`, taskRecord.ID).Scan(&executionCount); err != nil {
		t.Fatalf("count flow_node_execution rows: %v", err)
	}
	if executionCount != 1 {
		t.Fatalf("flow_node_execution count = %d, want 1", executionCount)
	}
}

func TestFlowExecutionServiceAdvanceThroughTerminalNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task", "in_progress", &template.ID)

	started, err := fixture.service.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.VisitNumber != 1 {
		t.Fatalf("start visit_number = %d, want 1", started.VisitNumber)
	}

	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 1: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 2: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow terminal step: %v", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want done", updatedTask.WorkStatus)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 3 {
		t.Fatalf("execution row count = %d, want 3", len(executions))
	}
	for i, execution := range executions {
		if execution.Status != "completed" {
			t.Fatalf("execution[%d] status = %q, want completed", i, execution.Status)
		}
	}

	if nodes[0].ID != executions[0].FlowNodeID || nodes[1].ID != executions[1].FlowNodeID || nodes[2].ID != executions[2].FlowNodeID {
		t.Fatalf("unexpected flow node execution order")
	}
}

func TestFlowExecutionServiceRejectsSelfReview(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Self review task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); !errors.Is(err, ErrSelfReviewForbidden) {
		t.Fatalf("AdvanceFlow self-review err = %v, want ErrSelfReviewForbidden", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil {
		t.Fatal("current_flow_node_id = nil, want active review node")
	}
	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[1].Status != "active" {
		t.Fatalf("review execution status = %q, want active", executions[1].Status)
	}
}

func TestFlowExecutionServiceTerminalAdvanceRequiresCompletedReview(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, err := fixture.templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &fixture.organization.ID,
		ProjectID:      &fixture.project.ID,
		Slug:           "work-only-" + uuid.NewString()[:8],
		DisplayName:    "Work Only",
		Description:    "missing internal review",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	workNode, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	template.StartNodeID = &workNode.ID
	if _, err := fixture.templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	taskRecord := seedFlowTask(t, ctx, fixture, "Terminal review required", "in_progress", &template.ID)
	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); !errors.Is(err, tasksvc.ErrDoneRequiresTerminalFlow) {
		t.Fatalf("AdvanceFlow terminal err = %v, want ErrDoneRequiresTerminalFlow", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", updatedTask.WorkStatus)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != workNode.ID {
		t.Fatalf("current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, workNode.ID)
	}
	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 1 {
		t.Fatalf("execution row count = %d, want 1", len(executions))
	}
	if executions[0].Status != "active" {
		t.Fatalf("execution status = %q, want active", executions[0].Status)
	}
}

func TestFlowExecutionServiceAdvanceFlowBackfillsMissingExecutionFromTemplate(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task missing execution", "in_progress", &template.ID)

	advanced, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if advanced.FlowNodeID != nodes[1].ID {
		t.Fatalf("advanced flow_node_id = %s, want %s", advanced.FlowNodeID, nodes[1].ID)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("current_flow_node_id = %v, want %s", updatedTask.CurrentFlowNodeID, nodes[1].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[0].FlowNodeID != nodes[0].ID || executions[0].Status != "completed" {
		t.Fatalf("execution[0] = %+v, want node=%s status=completed", executions[0], nodes[0].ID)
	}
	if executions[1].FlowNodeID != nodes[1].ID || executions[1].Status != "active" {
		t.Fatalf("execution[1] = %+v, want node=%s status=active", executions[1], nodes[1].ID)
	}
}

func TestFlowExecutionServiceAdvanceFlowBackfillsMissingExecutionForCurrentNode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Flow task missing active row", "in_progress", &template.ID)
	if _, err := fixture.taskRepo.SetFlowNode(ctx, taskRecord.ID, &nodes[0].ID); err != nil {
		t.Fatalf("SetFlowNode: %v", err)
	}

	advanced, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if advanced.FlowNodeID != nodes[1].ID {
		t.Fatalf("advanced flow_node_id = %s, want %s", advanced.FlowNodeID, nodes[1].ID)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("execution row count = %d, want 2", len(executions))
	}
	if executions[0].FlowNodeID != nodes[0].ID || executions[0].Status != "completed" {
		t.Fatalf("execution[0] = %+v, want node=%s status=completed", executions[0], nodes[0].ID)
	}
	if executions[1].FlowNodeID != nodes[1].ID || executions[1].Status != "active" {
		t.Fatalf("execution[1] = %+v, want node=%s status=active", executions[1], nodes[1].ID)
	}
}

func TestFlowExecutionServiceRejectionLoopVisitIncrements(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Rejection loop task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review node: %v", err)
	}

	firstReject, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode first: %v", err)
	}
	if firstReject.VisitNumber != 2 {
		t.Fatalf("first rejection visit_number = %d, want 2", firstReject.VisitNumber)
	}

	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow back to review: %v", err)
	}
	secondReject, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode second: %v", err)
	}
	if secondReject.VisitNumber != 3 {
		t.Fatalf("second rejection visit_number = %d, want 3", secondReject.VisitNumber)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	visitCount := 0
	for _, execution := range executions {
		if execution.FlowNodeID == nodes[0].ID {
			visitCount++
		}
	}
	if visitCount != 3 {
		t.Fatalf("reject target visit count = %d, want 3", visitCount)
	}
}

func TestFlowExecutionServiceRejectFlowNodeMaxVisits(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)
	taskTransitions, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
	})
	if err != nil {
		t.Fatalf("task.NewService: %v", err)
	}

	template, _ := seedLinearTemplate(t, ctx, fixture, true, 2)
	taskRecord := seedFlowTask(t, ctx, fixture, "Max visits task", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err != nil {
		t.Fatalf("RejectFlowNode first: %v", err)
	}
	afterFirstReject, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task after first reject: %v", err)
	}
	if afterFirstReject.WorkStatus == "queued" {
		if _, err := taskTransitions.TransitionStatus(ctx, taskRecord.ID, "in_progress", tasksvc.Actor{Type: "system"}); err != nil {
			t.Fatalf("TransitionStatus queued->in_progress: %v", err)
		}
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review again: %v", err)
	}

	if _, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID}); err == nil || err != ErrMaxVisitsExceeded {
		t.Fatalf("RejectFlowNode max visits err = %v, want ErrMaxVisitsExceeded", err)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "blocked" {
		t.Fatalf("task work_status = %q, want blocked", updatedTask.WorkStatus)
	}

	executions, err := fixture.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	visitsToRejectTarget := 0
	for _, execution := range executions {
		if execution.VisitNumber >= 3 {
			visitsToRejectTarget++
		}
	}
	if visitsToRejectTarget != 0 {
		t.Fatalf("unexpected execution created beyond max visits")
	}
}

func TestFlowExecutionServiceRejectFlowNodeLeavesBlockedReviewStateDispatchable(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)
	taskTransitions, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{}),
	})
	if err != nil {
		t.Fatalf("task.NewService: %v", err)
	}

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Blocked review reject", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}
	if _, err := taskTransitions.MarkBlocked(ctx, taskRecord.ID, "stale blocked review state", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	rejected, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "human_user", ID: fixture.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if rejected.FlowNodeID != nodes[0].ID {
		t.Fatalf("reject flow_node_id = %s, want %s", rejected.FlowNodeID, nodes[0].ID)
	}

	updatedTask, err := fixture.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.CurrentFlowNodeID == nil || *updatedTask.CurrentFlowNodeID != nodes[0].ID {
		t.Fatalf("current_flow_node_id = %v, want work node %s", updatedTask.CurrentFlowNodeID, nodes[0].ID)
	}
	if updatedTask.WorkStatus != "queued" {
		t.Fatalf("task work_status = %q, want queued", updatedTask.WorkStatus)
	}
}

func TestFlowExecutionServiceEnsureActiveExecutionRepairsMismatchedSessionBinding(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, nodes := seedLinearTemplate(t, ctx, fixture, true, 5)
	taskRecord := seedFlowTask(t, ctx, fixture, "Repair mismatched session binding", "in_progress", &template.ID)

	if _, err := fixture.service.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fixture.service.AdvanceFlow(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to review: %v", err)
	}

	reviewExecution, err := fixture.executionRepo.GetActive(ctx, taskRecord.ID, nodes[1].ID)
	if err != nil {
		t.Fatalf("GetActive review execution: %v", err)
	}
	if reviewExecution.SessionID == nil {
		t.Fatal("review execution session_id is nil")
	}

	rejected, err := fixture.service.RejectFlowNode(ctx, taskRecord.ID, Actor{Type: "agent", ID: fixture.reviewerAgent.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if rejected.SessionID == nil {
		t.Fatal("rejected work execution session_id is nil")
	}
	if *rejected.SessionID == *reviewExecution.SessionID {
		t.Fatalf("rejected work execution reused review session %s", *reviewExecution.SessionID)
	}

	if _, err := fixture.executionRepo.SetSessionID(ctx, rejected.ID, *reviewExecution.SessionID); err != nil {
		t.Fatalf("SetSessionID mismatched binding: %v", err)
	}

	repaired, err := fixture.service.EnsureActiveExecution(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("EnsureActiveExecution: %v", err)
	}
	if repaired.SessionID == nil {
		t.Fatal("repaired execution session_id is nil")
	}
	if *repaired.SessionID == *reviewExecution.SessionID {
		t.Fatalf("repaired execution still uses review session %s", *reviewExecution.SessionID)
	}

	var flowNodeExecutionID string
	if err := pool.QueryRow(ctx, `
		SELECT metadata->>'flow_node_execution_id'
		FROM chat_session
		WHERE id = $1
	`, *repaired.SessionID).Scan(&flowNodeExecutionID); err != nil {
		t.Fatalf("load repaired session metadata: %v", err)
	}
	if flowNodeExecutionID != repaired.ID.String() {
		t.Fatalf("repaired session flow_node_execution_id = %q, want %q", flowNodeExecutionID, repaired.ID.String())
	}
}

func TestFlowExecutionServiceDependencyCycleDetection(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskA := seedFlowTask(t, ctx, fixture, "Task A", "in_progress", &template.ID)
	taskB := seedFlowTask(t, ctx, fixture, "Task B", "in_progress", &template.ID)

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskB.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskA.ID,
		CreatedByType: "system",
	}); err == nil || err != ErrCyclicDependency {
		t.Fatalf("AddDependency B->A err = %v, want ErrCyclicDependency", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task_dependency`).Scan(&count); err != nil {
		t.Fatalf("count dependencies: %v", err)
	}
	if count != 1 {
		t.Fatalf("dependency row count = %d, want 1", count)
	}
}

func TestFlowExecutionServiceOnTaskCancelledMarksDependentsBlocked(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	fixture := seedFlowIntegrationFixture(t, ctx, pool)

	template, _ := seedLinearTemplate(t, ctx, fixture, false, 5)
	taskA := seedFlowTask(t, ctx, fixture, "Task A", "in_progress", &template.ID)
	taskB := seedFlowTask(t, ctx, fixture, "Task B", "in_progress", &template.ID)

	if _, err := fixture.service.AddDependency(ctx, AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}

	if _, err := fixture.taskRepo.UpdateStatus(ctx, taskB.ID, "cancelled"); err != nil {
		t.Fatalf("UpdateStatus task B cancelled: %v", err)
	}
	if err := fixture.service.OnTaskCancelled(ctx, taskB.ID); err != nil {
		t.Fatalf("OnTaskCancelled: %v", err)
	}

	updatedA, err := fixture.taskRepo.GetByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("GetByID task A: %v", err)
	}
	if updatedA.WorkStatus != "blocked" {
		t.Fatalf("task A work_status = %q, want blocked", updatedA.WorkStatus)
	}

	tasks, err := fixture.taskRepo.ListByProject(ctx, fixture.project.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("project task count = %d, want 2 without auto-created blocker resolution tasks", len(tasks))
	}
}

type integrationFixture struct {
	service       FlowExecutionService
	organization  repo.Organization
	project       repo.Project
	pmUser        repo.HumanUser
	pmAgent       repo.Agent
	reviewerAgent repo.Agent
	taskRepo      *repo.ProjectTaskRepo
	templateRepo  *repo.FlowTemplateRepo
	nodeRepo      *repo.FlowNodeRepo
	executionRepo *repo.FlowNodeExecutionRepo
}

func seedFlowIntegrationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) integrationFixture {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	taskRepo := repo.NewProjectTaskRepo(pool)
	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "flow-org-" + uuid.NewString()[:8],
		DisplayName: "Flow Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "flow-project-" + uuid.NewString()[:8],
		DisplayName:    "Flow Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	pmUser, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "pm+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "PM User",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create pm user: %v", err)
	}
	pmAgent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "PM Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create pm agent: %v", err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        pmAgent.ID,
		ProjectID:      project.ID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign pm: %v", err)
	}
	reviewerAgent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Reviewer Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "reviewer",
		CreatedByType:        "human_user",
		CreatedByID:          pmUser.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create reviewer agent: %v", err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        reviewerAgent.ID,
		ProjectID:      project.ID,
		Role:           "reviewer",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign reviewer: %v", err)
	}

	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{})
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("chat.NewService: %v", err)
	}
	sessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  pool,
		Chats: chatService,
	})
	if err != nil {
		t.Fatalf("project.NewFlowSessionBridge: %v", err)
	}
	svc, err := NewService(Options{
		Pool:          pool,
		Events:        bus,
		SessionBridge: sessionBridge,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return integrationFixture{
		service:       svc,
		organization:  org,
		project:       project,
		pmUser:        pmUser,
		pmAgent:       pmAgent,
		reviewerAgent: reviewerAgent,
		taskRepo:      taskRepo,
		templateRepo:  templateRepo,
		nodeRepo:      nodeRepo,
		executionRepo: executionRepo,
	}
}

func seedLinearTemplate(t *testing.T, ctx context.Context, fixture integrationFixture, withReject bool, maxVisits int) (repo.FlowTemplate, []repo.FlowNode) {
	t.Helper()

	template, err := fixture.templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &fixture.organization.ID,
		ProjectID:      &fixture.project.ID,
		Slug:           "flow-template-" + uuid.NewString()[:8],
		DisplayName:    "Flow Template",
		Description:    "integration template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	node1, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node 1",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node1: %v", err)
	}
	node2, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Node 2",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node2: %v", err)
	}
	node3, err := fixture.nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      maxVisits,
	})
	if err != nil {
		t.Fatalf("create node3: %v", err)
	}

	node1.NextNodeID = &node2.ID
	if withReject {
		node2.RejectNodeID = &node1.ID
	}
	node2.NextNodeID = &node3.ID

	if _, err := fixture.nodeRepo.Update(ctx, node1); err != nil {
		t.Fatalf("update node1: %v", err)
	}
	if _, err := fixture.nodeRepo.Update(ctx, node2); err != nil {
		t.Fatalf("update node2: %v", err)
	}

	template.StartNodeID = &node1.ID
	if _, err := fixture.templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	return template, []repo.FlowNode{node1, node2, node3}
}

func seedFlowTask(t *testing.T, ctx context.Context, fixture integrationFixture, title string, status string, templateID *uuid.UUID) repo.ProjectTask {
	t.Helper()
	taskRecord, err := fixture.taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: fixture.organization.ID,
		ProjectID:      fixture.project.ID,
		Title:          title,
		WorkStatus:     status,
		FlowTemplateID: templateID,
		CreatedByType:  "system",
		CreatedByID:    nil,
		Metadata:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return taskRecord
}
