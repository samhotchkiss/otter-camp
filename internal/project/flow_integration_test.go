//go:build integration

package project

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestFlow_Progression_LinearChain(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)
	template, nodes := createLinearTemplate073(t, ctx, fx, 3, false, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "linear-progression", "in_progress", &template.ID)

	started, err := fx.flowService.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.FlowNodeID != nodes[0].ID {
		t.Fatalf("start flow_node_id = %s, want %s", started.FlowNodeID, nodes[0].ID)
	}

	// In production, advancement is triggered via the flow.advance native tool (task 057).
	if _, err := fx.flowService.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: fx.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 1: %v", err)
	}
	if _, err := fx.flowService.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: fx.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow step 2: %v", err)
	}
	if _, err := fx.flowService.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: fx.reviewerAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow terminal: %v", err)
	}

	updatedTask, err := fx.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "done" {
		t.Fatalf("task work_status = %q, want %q", updatedTask.WorkStatus, "done")
	}

	executions, err := fx.executionRepo.ListByTask(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("ListByTask executions: %v", err)
	}
	if len(executions) != 3 {
		t.Fatalf("execution row count = %d, want 3", len(executions))
	}
	if executions[0].FlowNodeID != nodes[0].ID || executions[1].FlowNodeID != nodes[1].ID || executions[2].FlowNodeID != nodes[2].ID {
		t.Fatalf("unexpected execution flow node order")
	}
}

func TestFlow_Rejection_VisitCounter(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)
	template, nodes := createLinearTemplate073(t, ctx, fx, 3, true, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "rejection-loop", "in_progress", &template.ID)

	if _, err := fx.flowService.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if _, err := fx.flowService.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: fx.pmAgent.ID}); err != nil {
		t.Fatalf("AdvanceFlow to rejectable node: %v", err)
	}

	rejected, err := fx.flowService.RejectFlowNode(ctx, taskRecord.ID, flowsvc.Actor{Type: "human_user", ID: fx.pmUser.ID})
	if err != nil {
		t.Fatalf("RejectFlowNode: %v", err)
	}
	if rejected.FlowNodeID != nodes[1].ID {
		t.Fatalf("rejected flow_node_id = %s, want %s", rejected.FlowNodeID, nodes[1].ID)
	}
	if rejected.VisitNumber != 2 {
		t.Fatalf("rejected visit_number = %d, want 2", rejected.VisitNumber)
	}

	taskAfterReject, err := fx.taskRepo.GetByID(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if taskAfterReject.CurrentFlowNodeID == nil || *taskAfterReject.CurrentFlowNodeID != nodes[1].ID {
		t.Fatalf("task current_flow_node_id = %v, want %s", taskAfterReject.CurrentFlowNodeID, nodes[1].ID)
	}
}

func TestFlow_PerNodeSession_AutoCreated(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)
	template, _ := createLinearTemplate073(t, ctx, fx, 2, false, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "per-node-session", "in_progress", &template.ID)

	started, err := fx.flowService.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	if started.SessionID == nil || *started.SessionID == uuid.Nil {
		t.Fatal("start execution session_id is nil")
	}

	next, err := fx.flowService.AdvanceFlow(ctx, taskRecord.ID, flowsvc.Actor{Type: "agent", ID: fx.pmAgent.ID})
	if err != nil {
		t.Fatalf("AdvanceFlow: %v", err)
	}
	if next.SessionID == nil || *next.SessionID == uuid.Nil {
		t.Fatal("next execution session_id is nil")
	}
}

func TestFlow_CommitSha_Recording(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)
	template, _ := createLinearTemplate073(t, ctx, fx, 2, false, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "commit-recording", "in_progress", &template.ID)

	if _, err := fx.flowService.StartFlow(ctx, taskRecord.ID); err != nil {
		t.Fatalf("StartFlow: %v", err)
	}

	updated, err := fx.flowService.RecordNodeCommit(ctx, taskRecord.ID, "abc1234", "feature/abc1234")
	if err != nil {
		t.Fatalf("RecordNodeCommit: %v", err)
	}
	if updated.CommitSHA == nil || *updated.CommitSHA == "" {
		t.Fatal("commit_sha is nil after RecordNodeCommit")
	}
}

func TestDependencyDAG_CycleRejection(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)

	taskA := createFlowTask073(t, ctx, fx, "dep-a", "in_progress", nil)
	taskB := createFlowTask073(t, ctx, fx, "dep-b", "in_progress", nil)
	taskC := createFlowTask073(t, ctx, fx, "dep-c", "in_progress", nil)

	if _, err := fx.flowService.AddDependency(ctx, flowsvc.AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}
	if _, err := fx.flowService.AddDependency(ctx, flowsvc.AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskB.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskC.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency B->C: %v", err)
	}

	var beforeCount int
	if err := fx.pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task_dependency`).Scan(&beforeCount); err != nil {
		t.Fatalf("count dependencies before cycle add: %v", err)
	}

	if _, err := fx.flowService.AddDependency(ctx, flowsvc.AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskC.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskA.ID,
		CreatedByType: "system",
	}); !errors.Is(err, flowsvc.ErrCyclicDependency) {
		t.Fatalf("AddDependency C->A err = %v, want ErrCyclicDependency", err)
	}

	var afterCount int
	if err := fx.pool.QueryRow(ctx, `SELECT COUNT(*) FROM project_task_dependency`).Scan(&afterCount); err != nil {
		t.Fatalf("count dependencies after cycle add: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("dependency row count changed on cycle rejection: before=%d after=%d", beforeCount, afterCount)
	}
}

func TestDependencyDAG_BlockedAutoResolutionTask(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)

	taskA := createFlowTask073(t, ctx, fx, "dep-blocked-a", "in_progress", nil)
	taskB := createFlowTask073(t, ctx, fx, "dep-blocked-b", "in_progress", nil)

	if _, err := fx.flowService.AddDependency(ctx, flowsvc.AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskA.ID,
		DependsOnType: "project_task",
		DependsOnID:   taskB.ID,
		CreatedByType: "system",
	}); err != nil {
		t.Fatalf("AddDependency A->B: %v", err)
	}

	if _, err := fx.taskService.TransitionStatus(ctx, taskB.ID, "cancelled", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus cancelled for task B: %v", err)
	}
	if err := fx.flowService.OnTaskCancelled(ctx, taskB.ID); err != nil {
		t.Fatalf("OnTaskCancelled: %v", err)
	}

	updatedA, err := fx.taskRepo.GetByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("GetByID task A: %v", err)
	}
	if updatedA.WorkStatus != "blocked" {
		t.Fatalf("task A work_status = %q, want %q", updatedA.WorkStatus, "blocked")
	}

	tasks, err := fx.taskRepo.ListByProject(ctx, fx.project.ID)
	if err != nil {
		t.Fatalf("ListByProject tasks: %v", err)
	}
	foundResolution := false
	for _, item := range tasks {
		if item.ID == taskA.ID || item.ID == taskB.ID {
			continue
		}
		if strings.HasPrefix(item.Title, "Resolve blocker for task "+fx.project.Slug+"-") && item.WorkStatus == "queued" {
			if item.AssignedAgentID == nil || *item.AssignedAgentID != fx.pmAgent.ID {
				t.Fatalf("resolution task assigned_agent_id = %v, want %s", item.AssignedAgentID, fx.pmAgent.ID)
			}
			foundResolution = true
			break
		}
	}
	if !foundResolution {
		t.Fatal("expected auto-created resolution task for cancelled dependency")
	}

	inboxItems, err := fx.inboxRepo.ListForUser(ctx, fx.org.ID, fx.pmUser.ID, repo.InboxListOptions{
		ItemType:     "blocker_filed",
		IncludeActed: true,
		Limit:        20,
	})
	if err != nil {
		t.Fatalf("ListForUser blocker_filed: %v", err)
	}
	if len(inboxItems) == 0 {
		t.Fatal("expected blocker_filed inbox item for PM")
	}
}

func TestDependencyDAG_SameLevelOnly(t *testing.T) {
	ctx := context.Background()
	fx := newFlowFixture073(t)

	template, _ := createLinearTemplate073(t, ctx, fx, 1, false, 5)
	taskRecord := createFlowTask073(t, ctx, fx, "same-level-root", "in_progress", &template.ID)
	started, err := fx.flowService.StartFlow(ctx, taskRecord.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}
	subtask, err := fx.subtaskRepo.Create(ctx, repo.ProjectSubtask{
		TaskID:              taskRecord.ID,
		FlowNodeExecutionID: started.ID,
		Title:               "child-subtask",
		WorkStatus:          "pending",
		SequenceNumber:      1,
		CreatedByType:       "system",
	})
	if err != nil {
		t.Fatalf("Create subtask: %v", err)
	}

	if _, err := fx.flowService.AddDependency(ctx, flowsvc.AddDependencyRequest{
		SourceType:    "project_task",
		SourceID:      taskRecord.ID,
		DependsOnType: "project_subtask",
		DependsOnID:   subtask.ID,
		CreatedByType: "system",
	}); !errors.Is(err, flowsvc.ErrCrossLevelDependency) {
		t.Fatalf("AddDependency cross-level err = %v, want ErrCrossLevelDependency", err)
	}

	if _, err := fx.dependencyRepo.Add(ctx, repo.ProjectTaskDependency{
		SourceType:    "project_task",
		SourceID:      taskRecord.ID,
		DependsOnType: "project_subtask",
		DependsOnID:   subtask.ID,
		CreatedByType: "system",
	}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("dependency repo add err = %v, want repo.ErrConflict", err)
	}
}

type flowFixture073 struct {
	pool           *pgxpool.Pool
	flowService    flowsvc.FlowExecutionService
	taskService    tasksvc.TaskService
	org            repo.Organization
	project        repo.Project
	pmUser         repo.HumanUser
	pmAgent        repo.Agent
	reviewerAgent  repo.Agent
	taskRepo       *repo.ProjectTaskRepo
	templateRepo   *repo.FlowTemplateRepo
	nodeRepo       *repo.FlowNodeRepo
	executionRepo  *repo.FlowNodeExecutionRepo
	dependencyRepo *repo.ProjectTaskDependencyRepo
	subtaskRepo    *repo.ProjectSubtaskRepo
	inboxRepo      *repo.InboxItemRepo
}

func newFlowFixture073(t *testing.T) flowFixture073 {
	t.Helper()

	ctx := context.Background()
	pool := testdb.New(t)
	org, projectRecord, _, pmUser, pmAgent := seedTaskFixtureData073(t, ctx, pool, false)
	reviewerAgent, err := repo.NewAgentRepo(pool).Create(ctx, repo.Agent{
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
		SystemPrompt:         "",
		OperatorInstructions: "",
	})
	if err != nil {
		t.Fatalf("create reviewer agent: %v", err)
	}
	if _, err := repo.NewAgentProjectAssignmentRepo(pool).Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        reviewerAgent.ID,
		ProjectID:      projectRecord.ID,
		Role:           "reviewer",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign reviewer: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := eventbus.New(pool, logger, eventbus.Config{})
	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("new task service: %v", err)
	}
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("new chat service: %v", err)
	}
	flowSessionBridge, err := NewFlowSessionBridge(FlowSessionBridgeOptions{
		Pool:  pool,
		Chats: chatService,
	})
	if err != nil {
		t.Fatalf("new flow session bridge: %v", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          pool,
		TasksService:  taskService,
		Events:        bus,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("new flow service: %v", err)
	}

	return flowFixture073{
		pool:           pool,
		flowService:    flowService,
		taskService:    taskService,
		org:            org,
		project:        projectRecord,
		pmUser:         pmUser,
		pmAgent:        pmAgent,
		reviewerAgent:  reviewerAgent,
		taskRepo:       repo.NewProjectTaskRepo(pool),
		templateRepo:   repo.NewFlowTemplateRepo(pool),
		nodeRepo:       repo.NewFlowNodeRepo(pool),
		executionRepo:  repo.NewFlowNodeExecutionRepo(pool),
		dependencyRepo: repo.NewProjectTaskDependencyRepo(pool),
		subtaskRepo:    repo.NewProjectSubtaskRepo(pool),
		inboxRepo:      repo.NewInboxItemRepo(pool),
	}
}

func createLinearTemplate073(t *testing.T, ctx context.Context, fx flowFixture073, nodeCount int, rejectLoop bool, maxVisits int) (repo.FlowTemplate, []repo.FlowNode) {
	t.Helper()

	if nodeCount < 2 {
		nodeCount = 2
	}
	if maxVisits < 1 {
		maxVisits = 1
	}

	template, err := fx.templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &fx.org.ID,
		ProjectID:      &fx.project.ID,
		Slug:           "flow-073-" + uuid.NewString()[:8],
		DisplayName:    "Flow 073",
		Description:    "integration flow",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}

	nodes := make([]repo.FlowNode, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		nodeType := "work"
		if i == 1 {
			nodeType = "review"
		} else if i > 1 {
			nodeType = "merge"
		}
		node, createErr := fx.nodeRepo.Create(ctx, repo.FlowNode{
			FlowTemplateID: template.ID,
			DisplayName:    "Node " + uuid.NewString()[:8],
			NodeType:       nodeType,
			Position:       i + 1,
			MCPTools:       []repo.FlowNodeMCPTool{},
			ToolDomains:    []string{},
			MaxVisits:      maxVisits,
			Metadata:       json.RawMessage(`{}`),
		})
		if createErr != nil {
			t.Fatalf("create flow node %d: %v", i+1, createErr)
		}
		nodes = append(nodes, node)
	}

	for i := 0; i < len(nodes)-1; i++ {
		nodes[i].NextNodeID = &nodes[i+1].ID
		if _, err := fx.nodeRepo.Update(ctx, nodes[i]); err != nil {
			t.Fatalf("update node %d next_node_id: %v", i+1, err)
		}
	}

	if rejectLoop && len(nodes) >= 2 {
		nodes[1].RejectNodeID = &nodes[1].ID
		if _, err := fx.nodeRepo.Update(ctx, nodes[1]); err != nil {
			t.Fatalf("update reject node pointer: %v", err)
		}
	}

	template.StartNodeID = &nodes[0].ID
	template, err = fx.templateRepo.Update(ctx, template)
	if err != nil {
		t.Fatalf("update template start node: %v", err)
	}

	return template, nodes
}

func createFlowTask073(t *testing.T, ctx context.Context, fx flowFixture073, title, status string, templateID *uuid.UUID) repo.ProjectTask {
	t.Helper()

	taskRecord, err := fx.taskRepo.Create(ctx, repo.ProjectTask{
		OrganizationID: fx.org.ID,
		ProjectID:      fx.project.ID,
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
