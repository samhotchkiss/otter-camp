//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/chat"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	flowsvc "github.com/samhotchkiss/otter-camp/internal/flow"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	projectsvc "github.com/samhotchkiss/otter-camp/internal/project"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	tasksvc "github.com/samhotchkiss/otter-camp/internal/task"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

const testAgentTurnJobType = "agent_turn"

func TestTaskQueueProcessorIntegrationQueuedFlowTaskStartsFlowAndRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	stopTurnRuntime := startTaskQueueTurnRuntime(t, ctx, fx.pool, fx.bus, fx.org.ID)
	defer stopTurnRuntime()

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
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	participantRepo := repo.NewChatParticipantRepo(fx.pool)

	var (
		taskRecord      repo.ProjectTask
		execution       repo.FlowNodeExecution
		runRecord       Run
		agentTurnStatus string
		foundResponse   bool
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
		var hasInProgressRun bool
		for _, candidate := range runs {
			if candidate.FlowNodeID != nil && *candidate.FlowNodeID == *taskRecord.CurrentFlowNodeID && candidate.Status == "in_progress" {
				runRecord = candidate
				hasInProgressRun = true
				break
			}
		}
		if !hasInProgressRun {
			return false, nil
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return false, nil
		}

		err = fx.pool.QueryRow(ctx, `
			SELECT status
			FROM job_queue
			WHERE job_type = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, testAgentTurnJobType).Scan(&agentTurnStatus)
		if err != nil {
			if err == pgx.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		if agentTurnStatus == "dead_letter" {
			return false, fmt.Errorf("agent_turn moved to dead_letter")
		}
		if agentTurnStatus != "done" {
			return false, nil
		}

		messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
		if err != nil {
			return false, err
		}
		for _, message := range messages {
			if message.Role == "assistant" && message.Status == "final" && message.Content != "" {
				foundResponse = true
				break
			}
		}
		return foundResponse, nil
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
	if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
		t.Fatal("flow execution session_id is nil")
	}
	if agentTurnStatus != "done" {
		t.Fatalf("agent_turn status = %q, want done", agentTurnStatus)
	}

	messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
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
		t.Fatal("expected user kickoff message for flow run")
	}

	participants, err := participantRepo.ListBySession(ctx, *execution.SessionID)
	if err != nil {
		t.Fatalf("ListBySession participants: %v", err)
	}
	var foundAgentParticipant bool
	for _, participant := range participants {
		if participant.ParticipantType == "agent" && participant.ParticipantID == fx.agent.ID {
			foundAgentParticipant = true
			break
		}
	}
	if !foundAgentParticipant {
		t.Fatal("expected agent participant on flow node session")
	}
	if !foundResponse {
		t.Fatal("expected assistant response message for flow kickoff")
	}
}

func TestTaskQueueProcessorIntegrationQueuedAssignedAgentTaskStartsRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	stopTurnRuntime := startTaskQueueTurnRuntime(t, ctx, fx.pool, fx.bus, fx.org.ID)
	defer stopTurnRuntime()

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
	participantRepo := repo.NewChatParticipantRepo(fx.pool)

	var (
		taskRecord      repo.ProjectTask
		runRecord       Run
		agentTurnStatus string
		foundResponse   bool
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
		var hasInProgressRun bool
		for _, candidate := range runs {
			if candidate.FlowNodeID == nil && candidate.Status == "in_progress" {
				runRecord = candidate
				hasInProgressRun = true
				break
			}
		}
		if !hasInProgressRun {
			return false, nil
		}

		err = fx.pool.QueryRow(ctx, `
			SELECT status
			FROM job_queue
			WHERE job_type = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, testAgentTurnJobType).Scan(&agentTurnStatus)
		if err != nil {
			if err == pgx.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		if agentTurnStatus == "dead_letter" {
			return false, fmt.Errorf("agent_turn moved to dead_letter")
		}
		if agentTurnStatus != "done" {
			return false, nil
		}

		session, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil || session == nil {
			return false, err
		}
		messages, err := messageRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		for _, message := range messages {
			if message.Role == "assistant" && message.Status == "final" && message.Content != "" {
				foundResponse = true
				break
			}
		}
		return foundResponse, nil
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
	if agentTurnStatus != "done" {
		t.Fatalf("agent_turn status = %q, want done", agentTurnStatus)
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

	participants, err := participantRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListBySession participants: %v", err)
	}
	var foundAgentResponder bool
	for _, participant := range participants {
		if participant.ParticipantType == "agent" && participant.ParticipantID == fx.agent.ID {
			foundAgentResponder = true
			break
		}
	}
	if !foundAgentResponder {
		t.Fatal("expected active responder agent participant on task session")
	}
	if !foundResponse {
		t.Fatal("expected assistant response message after kickoff")
	}
}

func TestTaskQueueProcessorIntegrationRunCancellationRequestedConfirmsSchedulerAndSupervisor(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)

	runRepo := NewRunRepository(fx.pool)
	projectRecord, taskRecord := seedRunProjectTaskWithPM(t, ctx, fx.pool, fx.org.ID)
	flowNodeID := seedSupervisorFlowNode(t, ctx, fx.pool, fx.org.ID, projectRecord.ID)

	createCancellingRun := func(triggerType string) Run {
		t.Helper()
		runRecord, err := runRepo.Create(ctx, Run{
			OrganizationID: fx.org.ID,
			ProjectID:      &projectRecord.ID,
			TaskID:         &taskRecord.ID,
			FlowNodeID:     &flowNodeID,
			PrincipalType:  "system",
			PrincipalID:    uuid.Nil,
			Status:         "cancelling",
			TriggerType:    triggerType,
			Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test"}`),
		})
		if err != nil {
			t.Fatalf("create %s run: %v", triggerType, err)
		}
		return runRecord
	}

	schedulerRun := createCancellingRun("scheduler")
	supervisorRun := createCancellingRun("supervisor")
	agentToolRun := createCancellingRun("agent_tool")

	publishCancelRequested := func(runID uuid.UUID) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{"run_id": runID.String()})
		if err != nil {
			t.Fatalf("marshal cancellation payload: %v", err)
		}
		if err := fx.bus.Publish(ctx, nil, eventbus.DomainEvent{
			OrganizationID: fx.org.ID,
			EventType:      "run.cancellation_requested",
			ActorType:      "system",
			Payload:        payload,
		}); err != nil {
			t.Fatalf("publish run.cancellation_requested: %v", err)
		}
	}

	publishCancelRequested(schedulerRun.ID)
	publishCancelRequested(supervisorRun.ID)
	publishCancelRequested(agentToolRun.ID)

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		scheduler, err := runRepo.Get(ctx, schedulerRun.ID)
		if err != nil {
			return false, err
		}
		supervisor, err := runRepo.Get(ctx, supervisorRun.ID)
		if err != nil {
			return false, err
		}
		return scheduler.Status == "cancelled" && supervisor.Status == "cancelled", nil
	})

	agentTool, err := runRepo.Get(ctx, agentToolRun.ID)
	if err != nil {
		t.Fatalf("Get agent_tool run: %v", err)
	}
	if agentTool.Status != "cancelling" {
		t.Fatalf("agent_tool run status = %q, want cancelling", agentTool.Status)
	}
}

type taskQueueProcessorFixture struct {
	pool               *pgxpool.Pool
	bus                *eventbus.Bus
	taskQueuedSub      eventbus.Subscription
	taskCompletedSub   eventbus.Subscription
	runCancellationSub eventbus.Subscription
	tasks              tasksvc.TaskService
	org                repo.Organization
	project            repo.Project
	agent              repo.Agent
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
	taskCompletedSub := processor.SubscribeTaskCompleted(&org.ID)
	runCancellationSub := processor.SubscribeRunCancellationRequested(&org.ID)

	return taskQueueProcessorFixture{
		pool:               pool,
		bus:                bus,
		taskQueuedSub:      subscription,
		taskCompletedSub:   taskCompletedSub,
		runCancellationSub: runCancellationSub,
		tasks:              taskService,
		org:                org,
		project:            project,
		agent:              agent,
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

func startTaskQueueTurnRuntime(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bus *eventbus.Bus, orgID uuid.UUID) func() {
	t.Helper()

	chatService, err := chat.NewService(chat.Options{
		Pool:   pool,
		Events: bus,
	})
	if err != nil {
		t.Fatalf("new chat service: %v", err)
	}

	jqWorker := jobqueue.New(pool, nil, jobqueue.Config{
		PollInterval:         10 * time.Millisecond,
		StaleScanInterval:    time.Hour,
		CleanupEnqueuePeriod: time.Hour,
	})

	enqueueSub := bus.Subscribe("task-queue-test-agent-turn-enqueue", &orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "chat.message.user_sent" {
			return nil
		}
		var payload taskQueueAgentTurnPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil
		}
		if payload.SessionID == uuid.Nil || payload.MessageID == uuid.Nil {
			return nil
		}
		_, err := jqWorker.Enqueue(ctx, nil, testAgentTurnJobType, 70, payload, nil)
		return err
	})

	jqWorker.Register(testAgentTurnJobType, func(ctx context.Context, job jobqueue.Job) error {
		var payload taskQueueAgentTurnPayload
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return err
		}
		participants, err := chatService.ListParticipants(ctx, payload.SessionID)
		if err != nil {
			return err
		}

		var responderID uuid.UUID
		for _, participant := range participants {
			if participant == nil {
				continue
			}
			if participant.ParticipantType == "agent" {
				responderID = participant.ParticipantID
				break
			}
		}
		if responderID == uuid.Nil {
			return repo.ErrNotFound
		}

		authorType := "agent"
		message, err := chatService.AppendMessage(ctx, chat.AppendMessageInput{
			SessionID:  payload.SessionID,
			AuthorType: &authorType,
			AuthorID:   &responderID,
			Role:       "assistant",
			Content:    "Task started.",
		})
		if err != nil {
			return err
		}
		if err := chatService.UpdateMessageStatus(ctx, message.ID, "streaming", ""); err != nil {
			return err
		}
		return chatService.UpdateMessageStatus(ctx, message.ID, "final", "")
	})

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- jqWorker.Start(runCtx)
	}()

	return func() {
		bus.Unsubscribe(enqueueSub)
		cancel()
		_ = jqWorker.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("job worker stopped with error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for job worker shutdown")
		}
	}
}

type taskQueueAgentTurnPayload struct {
	SessionID uuid.UUID `json:"session_id"`
	MessageID uuid.UUID `json:"message_id"`
}

func stringPtr(value string) *string {
	return &value
}
