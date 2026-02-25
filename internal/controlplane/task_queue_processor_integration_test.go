//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTaskQueueProcessorIntegrationQueuedFlowTaskStartsFlowAndRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.subscription)

	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Queued flow task",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)

	var (
		taskRecord repo.ProjectTask
		execution  repo.FlowNodeExecution
		runRecord  Run
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var err error
		taskRecord, err = taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.WorkStatus != "in_progress" || taskRecord.CurrentFlowNodeID == nil {
			return false, nil
		}

		execution, err = executionRepo.GetActive(ctx, created.ID, *taskRecord.CurrentFlowNodeID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}

		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		for _, candidate := range runs {
			if candidate.FlowNodeID != nil && *candidate.FlowNodeID == *taskRecord.CurrentFlowNodeID && candidate.Status == "in_progress" {
				runRecord = candidate
				return true, nil
			}
		}
		return false, nil
	})

	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
	if taskRecord.CurrentFlowNodeID == nil {
		t.Fatal("task current_flow_node_id is nil")
	}
	if execution.ID == uuid.Nil {
		t.Fatal("flow execution id is nil")
	}
	if runRecord.ID == uuid.Nil {
		t.Fatal("run id is nil")
	}
	if runRecord.Status != "in_progress" {
		t.Fatalf("run status = %q, want in_progress", runRecord.Status)
	}
}

func TestTaskQueueProcessorIntegrationQueuedAssignedAgentTaskStartsRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.subscription)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Queued assigned-agent task",
		Description:     stringPtr("Investigate and start this queued task."),
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)
	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)

	var (
		taskRecord repo.ProjectTask
		runRecord  Run
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var err error
		taskRecord, err = taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.WorkStatus != "in_progress" {
			return false, nil
		}

		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		for _, candidate := range runs {
			if candidate.FlowNodeID == nil && candidate.Status == "in_progress" {
				runRecord = candidate
				return true, nil
			}
		}
		return false, nil
	})

	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
	if runRecord.ID == uuid.Nil {
		t.Fatal("run id is nil")
	}
	if runRecord.SessionID == nil || *runRecord.SessionID == uuid.Nil {
		t.Fatalf("run session_id = %v, want non-nil", runRecord.SessionID)
	}

	session, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
	if err != nil {
		t.Fatalf("GetByScopeAndMode async project_task session: %v", err)
	}
	if session == nil {
		t.Fatal("async project_task session is nil")
	}
	if runRecord.SessionID == nil || *runRecord.SessionID != session.ID {
		t.Fatalf("run session_id = %v, want %s", runRecord.SessionID, session.ID)
	}

	messages, err := messageRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	var foundKickoff bool
	for _, message := range messages {
		if message.Role != "user" || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if unmarshalErr := json.Unmarshal(message.Metadata, &metadata); unmarshalErr != nil {
			continue
		}
		if metadata["source"] == "task_queue_processor" {
			foundKickoff = true
			break
		}
	}
	if !foundKickoff {
		t.Fatal("expected user kickoff message from task_queue_processor")
	}
}

type taskQueueProcessorFixture struct {
	pool         *pgxpool.Pool
	bus          *eventbus.Bus
	subscription eventbus.Subscription
	tasks        tasksvc.TaskService
	org          repo.Organization
	project      repo.Project
	agent        repo.Agent
}

func seedTaskQueueProcessorFixture(t *testing.T, ctx context.Context) taskQueueProcessorFixture {
	t.Helper()

	pool := testdb.New(t)
	bus := eventbus.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), eventbus.Config{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    100,
	})

	taskService, err := tasksvc.NewService(tasksvc.Options{
		Pool:     pool,
		EventBus: bus,
	})
	if err != nil {
		t.Fatalf("New task service: %v", err)
	}
	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("New chat service: %v", err)
	}
	flowSessionBridge, err := projectsvc.NewFlowSessionBridge(projectsvc.FlowSessionBridgeOptions{
		Pool:  pool,
		Chats: chatService,
	})
	if err != nil {
		t.Fatalf("New flow session bridge: %v", err)
	}
	flowService, err := flowsvc.NewService(flowsvc.Options{
		Pool:          pool,
		Events:        bus,
		TasksService:  taskService,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("New flow service: %v", err)
	}
	runService, err := NewRunService(RunServiceOptions{
		Pool:          pool,
		EventBus:      bus,
		SessionBridge: flowSessionBridge,
	})
	if err != nil {
		t.Fatalf("New run service: %v", err)
	}

	org, project, agent := seedTaskQueueProjectWithAgent(t, ctx, pool)
	processor, err := NewTaskQueueProcessor(TaskQueueProcessorOptions{
		Events:         bus,
		Tasks:          repo.NewProjectTaskRepo(pool),
		TaskService:    taskService,
		Flow:           flowService,
		FlowExecutions: repo.NewFlowNodeExecutionRepo(pool),
		Runs:           runService,
		Chats:          chatService,
		Sessions:       repo.NewChatSessionRepo(pool),
	})
	if err != nil {
		t.Fatalf("NewTaskQueueProcessor: %v", err)
	}

	subscription := processor.SubscribeTaskQueued(&org.ID)

	return taskQueueProcessorFixture{
		pool:         pool,
		bus:          bus,
		subscription: subscription,
		tasks:        taskService,
		org:          org,
		project:      project,
		agent:        agent,
	}
}

func seedTaskQueueProjectWithAgent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (repo.Organization, repo.Project, repo.Agent) {
	t.Helper()

	orgRepo := repo.NewOrgRepo(pool)
	projectRepo := repo.NewProjectRepo(pool)
	agentRepo := repo.NewAgentRepo(pool)
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)
	userRepo := repo.NewHumanUserRepo(pool)

	org, err := orgRepo.Create(ctx, repo.Organization{
		Slug:        "queued-org-" + uuid.NewString()[:8],
		DisplayName: "Queued Org",
	})
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	project, err := projectRepo.Create(ctx, repo.Project{
		OrganizationID: org.ID,
		Slug:           "queued-project-" + uuid.NewString()[:8],
		DisplayName:    "Queued Project",
		DeliveryMode:   "gated",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	creator, err := userRepo.Create(ctx, repo.HumanUser{
		OrganizationID: org.ID,
		Email:          "queue-owner+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "Queue Owner",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       org.ID,
		DisplayName:          "Queue Agent",
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            "pm",
		CreatedByType:        "human_user",
		CreatedByID:          creator.ID,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agent.ID,
		ProjectID:      project.ID,
		Role:           "pm",
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign agent to project: %v", err)
	}
	return org, project, agent
}

func seedTaskQueueFlowTemplate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID, projectID uuid.UUID) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "queued-flow-" + uuid.NewString()[:8],
		DisplayName:    "Queued Flow",
		Description:    "Task queue processor test template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create flow template: %v", err)
	}
	startNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Start",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create start flow node: %v", err)
	}
	template.StartNodeID = &startNode.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update flow template start node: %v", err)
	}
	return template
}

func waitForTaskQueueCondition(t *testing.T, timeout time.Duration, check func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		ok, err := check()
		if err != nil {
			t.Fatalf("wait condition error: %v", err)
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func stringPtr(value string) *string {
	return &value
}
