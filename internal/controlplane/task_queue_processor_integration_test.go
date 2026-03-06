//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
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
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

const testAgentTurnJobType = "agent_turn"

func TestTaskQueueProcessorIntegrationQueuedFlowTaskStartsFlowAndRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)
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

func TestTaskQueueProcessorIntegrationQueuedNonGateTaskWaitsForOutstandingGate(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	gateTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "Bootstrap gate",
		FlowTemplateID: &template.ID,
		BlocksScope:    "all",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask gate: %v", err)
	}
	regularTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "Regular queued task",
		FlowTemplateID: &template.ID,
		BlocksScope:    "none",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask regular: %v", err)
	}

	if _, err := fx.tasks.TransitionStatus(ctx, gateTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus gate queued: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		gate, err := taskRepo.GetByID(ctx, gateTask.ID)
		if err != nil {
			return false, err
		}
		return gate.WorkStatus == "in_progress", nil
	})

	if _, err := fx.tasks.TransitionStatus(ctx, regularTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus regular queued: %v", err)
	}

	time.Sleep(750 * time.Millisecond)
	regularAfter, err := taskRepo.GetByID(ctx, regularTask.ID)
	if err != nil {
		t.Fatalf("GetByID regular: %v", err)
	}
	if regularAfter.WorkStatus != "queued" {
		t.Fatalf("regular task work_status = %q, want queued while gate is outstanding", regularAfter.WorkStatus)
	}
}

func TestTaskQueueProcessorIntegrationCompletingGateStartsNextQueuedTask(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	gateTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "Bootstrap gate",
		FlowTemplateID: &template.ID,
		BlocksScope:    "all",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask gate: %v", err)
	}
	regularTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "Regular queued task",
		FlowTemplateID: &template.ID,
		BlocksScope:    "none",
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask regular: %v", err)
	}

	if _, err := fx.tasks.TransitionStatus(ctx, gateTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus gate queued: %v", err)
	}
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		gate, err := taskRepo.GetByID(ctx, gateTask.ID)
		if err != nil {
			return false, err
		}
		return gate.WorkStatus == "in_progress", nil
	})

	if _, err := fx.tasks.TransitionStatus(ctx, regularTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus regular queued: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	regularBefore, err := taskRepo.GetByID(ctx, regularTask.ID)
	if err != nil {
		t.Fatalf("GetByID regular before gate done: %v", err)
	}
	if regularBefore.WorkStatus != "queued" {
		t.Fatalf("regular task work_status before gate completion = %q, want queued", regularBefore.WorkStatus)
	}

	if _, err := fx.tasks.TransitionStatus(ctx, gateTask.ID, "done", tasksvc.Actor{Type: "system", AllowDoneBypass: true}); err != nil {
		t.Fatalf("TransitionStatus gate done: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		regular, err := taskRepo.GetByID(ctx, regularTask.ID)
		if err != nil {
			return false, err
		}
		return regular.WorkStatus == "in_progress", nil
	})
}

func TestTaskQueueProcessorIntegrationLowRiskAsyncDecisionContinuesAndEmitsReviewArtifactEX248(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Async Reviewer", "reviewer")
	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, reviewer.ID)
	description := "Use a reasonable assumption for the placeholder homepage copy, keep the choice low-risk, and confirm later."

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Draft homepage copy",
		Description:     &description,
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
	runRepo := NewRunRepository(fx.pool)
	inboxRepo := repo.NewInboxItemRepo(fx.pool)

	var (
		taskRecord repo.ProjectTask
		runRecord  Run
		artifact   repo.InboxItem
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var waitErr error
		taskRecord, waitErr = taskRepo.GetByID(ctx, created.ID)
		if waitErr != nil {
			return false, waitErr
		}
		if taskRecord.WorkStatus != "in_progress" || taskRecord.CurrentFlowNodeID == nil {
			return false, nil
		}

		runs, waitErr := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Status:         "in_progress",
			TriggerType:    taskQueueTriggerType,
			Limit:          10,
		})
		if waitErr != nil {
			return false, waitErr
		}
		if len(runs) == 0 {
			return false, nil
		}
		runRecord = runs[0]

		items, waitErr := inboxRepo.ListBroadcast(ctx, fx.org.ID, repo.InboxListOptions{
			IncludeActed: true,
			ItemType:     "system_alert",
			Limit:        50,
		})
		if waitErr != nil {
			return false, waitErr
		}
		var found bool
		artifact, found = findAsyncDecisionArtifact(items, created.ID, taskplan.AsyncDecisionProceedAndFlag)
		return found, nil
	})

	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
	if runRecord.ID == uuid.Nil {
		t.Fatal("expected async run to start for low-risk task")
	}
	if artifact.SourceTaskID == nil || *artifact.SourceTaskID != created.ID {
		t.Fatalf("artifact source_task_id = %v, want %s", artifact.SourceTaskID, created.ID)
	}
}

func TestTaskQueueProcessorIntegrationHighRiskAsyncDecisionPausesTaskEX248(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Async Reviewer", "reviewer")
	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, reviewer.ID)
	description := "This pricing decision is irreversible, affects billing, and must not be guessed."

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Resolve production pricing migration",
		Description:     &description,
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
	runRepo := NewRunRepository(fx.pool)
	inboxRepo := repo.NewInboxItemRepo(fx.pool)

	var (
		taskRecord repo.ProjectTask
		artifact   repo.InboxItem
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var waitErr error
		taskRecord, waitErr = taskRepo.GetByID(ctx, created.ID)
		if waitErr != nil {
			return false, waitErr
		}
		if taskRecord.WorkStatus != "review" {
			return false, nil
		}

		items, waitErr := inboxRepo.ListBroadcast(ctx, fx.org.ID, repo.InboxListOptions{
			IncludeActed: true,
			ItemType:     "system_alert",
			Limit:        50,
		})
		if waitErr != nil {
			return false, waitErr
		}
		var found bool
		artifact, found = findAsyncDecisionArtifact(items, created.ID, taskplan.AsyncDecisionHardStop)
		return found, nil
	})

	runs, err := runRepo.List(ctx, RunListFilter{
		OrganizationID: fx.org.ID,
		TaskID:         &created.ID,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}

	if taskRecord.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review", taskRecord.WorkStatus)
	}
	if taskRecord.CurrentFlowNodeID != nil {
		t.Fatalf("task current_flow_node_id = %v, want nil before execution starts", taskRecord.CurrentFlowNodeID)
	}
	if len(runs) != 0 {
		t.Fatalf("run count = %d, want 0 for hard-stop task", len(runs))
	}
	if artifact.SourceTaskID == nil || *artifact.SourceTaskID != created.ID {
		t.Fatalf("artifact source_task_id = %v, want %s", artifact.SourceTaskID, created.ID)
	}
}

func TestTaskQueueProcessorIntegrationReviewCheckpointDoesNotFreezeParallelWorkEX248(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Async Reviewer", "reviewer")
	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, reviewer.ID)
	reviewDescription := "Prepare the launch direction, then pause for review before finalizing the selected concept."

	gateTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Launch direction checkpoint",
		Description:     &reviewDescription,
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		BlocksScope:     "all",
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask gate: %v", err)
	}
	regularTask, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Implement onboarding API",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		BlocksScope:     "none",
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask regular: %v", err)
	}

	if _, err := fx.tasks.TransitionStatus(ctx, gateTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus gate queued: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, regularTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus regular queued: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)

	var (
		gateRecord    repo.ProjectTask
		regularRecord repo.ProjectTask
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var waitErr error
		gateRecord, waitErr = taskRepo.GetByID(ctx, gateTask.ID)
		if waitErr != nil {
			return false, waitErr
		}
		regularRecord, waitErr = taskRepo.GetByID(ctx, regularTask.ID)
		if waitErr != nil {
			return false, waitErr
		}
		if gateRecord.WorkStatus != "review" || regularRecord.WorkStatus != "in_progress" {
			return false, nil
		}

		runs, waitErr := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &regularTask.ID,
			Status:         "in_progress",
			TriggerType:    taskQueueTriggerType,
			Limit:          10,
		})
		if waitErr != nil {
			return false, waitErr
		}
		return len(runs) > 0, nil
	})

	if gateRecord.WorkStatus != "review" {
		t.Fatalf("gate task work_status = %q, want review", gateRecord.WorkStatus)
	}
	if regularRecord.WorkStatus != "in_progress" {
		t.Fatalf("regular task work_status = %q, want in_progress", regularRecord.WorkStatus)
	}
}

func TestTaskQueueProcessorIntegrationSchedulerRunCompletedOnTaskDone(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)
	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:      fx.project.ID,
		Title:          "Queued flow completion task",
		FlowTemplateID: &template.ID,
		CreatedByType:  "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}

	runRepo := NewRunRepository(fx.pool)
	var schedulerRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		runs, listErr := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Status:         "in_progress",
			TriggerType:    "scheduler",
			Limit:          20,
		})
		if listErr != nil {
			return false, listErr
		}
		if len(runs) == 0 {
			return false, nil
		}
		schedulerRun = runs[0]
		return true, nil
	})

	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "done", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		updated, getErr := runRepo.Get(ctx, schedulerRun.ID)
		if getErr != nil {
			return false, getErr
		}
		return updated.Status == "completed" && updated.CompletedAt != nil, nil
	})
}

func TestTaskQueueProcessorIntegrationQueuedAssignedAgentTaskStartsRun(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)
	stopTurnRuntime := startTaskQueueTurnRuntime(t, ctx, fx.pool, fx.bus, fx.org.ID)
	defer stopTurnRuntime()
	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Queued assigned-agent task",
		Description:     stringPtr("Investigate and start this queued task."),
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
			if candidate.Status == "in_progress" {
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

func TestTaskQueueProcessorIntegrationPausedProjectQueuedWorkStartsAfterResume(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)
	stopTurnRuntime := startTaskQueueTurnRuntime(t, ctx, fx.pool, fx.bus, fx.org.ID)
	defer stopTurnRuntime()

	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:   fx.pool,
		Events: fx.bus,
	})
	if err != nil {
		t.Fatalf("New project service: %v", err)
	}

	if _, err := projectService.Pause(ctx, fx.org.ID, fx.project.ID, projectsvc.PauseProjectRequest{
		Reason:       "operator pause",
		PausedByType: "system",
	}); err != nil {
		t.Fatalf("Pause project: %v", err)
	}

	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Pause Reviewer", "reviewer")
	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Paused queued task",
		Description:     stringPtr("Wait for project resume."),
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
	runRepo := NewRunRepository(fx.pool)
	sessionRepo := repo.NewChatSessionRepo(fx.pool)

	time.Sleep(750 * time.Millisecond)

	taskWhilePaused, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task while paused: %v", err)
	}
	if taskWhilePaused.WorkStatus != "queued" {
		t.Fatalf("task work_status while paused = %q, want queued", taskWhilePaused.WorkStatus)
	}

	sessionWhilePaused, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
	if err != nil {
		t.Fatalf("GetByScopeAndMode while paused: %v", err)
	}
	if sessionWhilePaused != nil {
		t.Fatalf("session while paused = %+v, want nil", sessionWhilePaused)
	}

	runsWhilePaused, err := runRepo.List(ctx, RunListFilter{
		OrganizationID: fx.org.ID,
		TaskID:         &created.ID,
		Limit:          20,
	})
	if err != nil {
		t.Fatalf("List runs while paused: %v", err)
	}
	if len(runsWhilePaused) != 0 {
		t.Fatalf("runs while paused = %d, want 0", len(runsWhilePaused))
	}

	var agentTurnJobs int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM job_queue
		WHERE job_type = $1
	`, testAgentTurnJobType).Scan(&agentTurnJobs); err != nil {
		t.Fatalf("count agent_turn jobs while paused: %v", err)
	}
	if agentTurnJobs != 0 {
		t.Fatalf("agent_turn jobs while paused = %d, want 0", agentTurnJobs)
	}

	if _, err := projectService.Resume(ctx, fx.org.ID, fx.project.ID, "system", uuid.Nil); err != nil {
		t.Fatalf("Resume project: %v", err)
	}

	var runRecord Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskAfterResume, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskAfterResume.WorkStatus != "in_progress" {
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
			if candidate.Status == "in_progress" {
				runRecord = candidate
				return true, nil
			}
		}
		return false, nil
	})

	if runRecord.ID == uuid.Nil {
		t.Fatal("expected in_progress run after resume")
	}
}

func TestTaskQueueProcessorIntegrationSupervisorRunCompletedOnTaskDone(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:     fx.project.ID,
		Title:         "Supervisor completion task",
		CreatedByType: "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	runRepo := NewRunRepository(fx.pool)
	supervisorRun, err := runRepo.Create(ctx, Run{
		OrganizationID: fx.org.ID,
		ProjectID:      &fx.project.ID,
		TaskID:         &created.ID,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		Status:         "in_progress",
		TriggerType:    "supervisor",
		Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test"}`),
	})
	if err != nil {
		t.Fatalf("create supervisor run: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":   created.ID.String(),
		"to_status": "done",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := fx.bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: fx.org.ID,
		EventType:      "task.status_changed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish task.status_changed: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		updated, getErr := runRepo.Get(ctx, supervisorRun.ID)
		if getErr != nil {
			return false, getErr
		}
		return updated.Status == "completed" && updated.CompletedAt != nil, nil
	})
}

func TestTaskQueueProcessorIntegrationRunCancellationRequestedConfirmsSchedulerAndSupervisor(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

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

func TestTaskQueueProcessorIntegrationFlowAdvancedTransitionsKickOffNextAgent(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Flow Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Flow Reviewer", "reviewer")
	template, nodeWorkA, nodeReview, nodeWorkB := seedTaskQueueTransitionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Flow transition task",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &worker.ID,
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

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		return taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID == nodeWorkA.ID, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		execution, err := executionRepo.GetActive(ctx, created.ID, nodeReview.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeReview.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if !hasRunForPrincipal(runs, reviewer.ID) {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", execution.ID), nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("AdvanceFlow review->work: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWorkB.ID {
			return false, nil
		}
		execution, err := executionRepo.GetActive(ctx, created.ID, nodeWorkB.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWorkB.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if !hasRunForPrincipal(runs, worker.ID) {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", execution.ID), nil
	})
}

func TestTaskQueueProcessorIntegrationTaskReviewApproveAdvancesAndKickOffsNextAgent(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Review Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Review Reviewer", "reviewer")
	template, nodeWorkA, nodeReview, nodeWorkB := seedTaskQueueHumanReviewFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	reviewActor, err := repo.NewHumanUserRepo(fx.pool).Create(ctx, repo.HumanUser{
		OrganizationID: fx.org.ID,
		Email:          "review-actor+" + uuid.NewString()[:8] + "@example.com",
		DisplayName:    "Review Actor",
		Role:           "admin",
		IsActive:       true,
		Settings:       json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create review actor: %v", err)
	}

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Task review approval flow",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &worker.ID,
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
	inboxRepo := repo.NewInboxItemRepo(fx.pool)

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		return taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID == nodeWorkA.ID, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	var reviewInbox repo.InboxItem
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "review") {
			return false, nil
		}
		items, err := inboxRepo.ListForUser(ctx, fx.org.ID, reviewActor.ID, repo.InboxListOptions{
			ItemType:     "task_review",
			IncludeActed: true,
			Limit:        50,
		})
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if item.SourceTaskID != nil && *item.SourceTaskID == created.ID {
				reviewInbox = item
				return true, nil
			}
		}
		return false, nil
	})

	if err := fx.tasks.ActOnInboxItem(ctx, reviewInbox.ID, reviewActor.ID, "approve", nil); err != nil {
		t.Fatalf("ActOnInboxItem approve: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWorkB.ID {
			return false, nil
		}
		execution, err := executionRepo.GetActive(ctx, created.ID, nodeWorkB.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWorkB.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if !hasRunForPrincipal(runs, worker.ID) {
			return false, nil
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", execution.ID), nil
	})

	updatedInbox, err := inboxRepo.GetByID(ctx, reviewInbox.ID)
	if err != nil {
		t.Fatalf("GetByID review inbox: %v", err)
	}
	if !updatedInbox.IsActed {
		t.Fatal("review inbox is_acted = false, want true")
	}
}

func TestTaskQueueProcessorIntegrationFlowRejectedKickOffsRejectPathAgent(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Reject Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Reject Reviewer", "reviewer")
	template, nodeWork, nodeReview := seedTaskQueueRejectFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Flow reject task",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &worker.ID,
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

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		return taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID == nodeWork.ID, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		return taskRecord.CurrentFlowNodeID != nil && *taskRecord.CurrentFlowNodeID == nodeReview.ID, nil
	})

	if _, err := fx.flow.RejectFlowNode(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("RejectFlowNode review->work: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		execution, err := executionRepo.GetActive(ctx, created.ID, nodeWork.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWork.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if !hasRunForPrincipal(runs, worker.ID) {
			return false, nil
		}
		if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.rejected", execution.ID), nil
	})
}

func hasRunForPrincipal(runs []Run, principalID uuid.UUID) bool {
	for _, run := range runs {
		if run.PrincipalType == "agent" && run.PrincipalID == principalID {
			return true
		}
	}
	return false
}

func hasFlowKickoffMessage(messages []repo.ChatMessage, eventType string, executionID uuid.UUID) bool {
	for _, message := range messages {
		if message.Role != "user" || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if err := json.Unmarshal(message.Metadata, &metadata); err != nil {
			continue
		}
		if metadata["source"] != "task_queue_processor" {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["flow_event_type"])) != strings.TrimSpace(eventType) {
			continue
		}
		if strings.TrimSpace(valueAsString(metadata["flow_node_execution_id"])) != executionID.String() {
			continue
		}
		return true
	}
	return false
}

type taskQueueProcessorFixture struct {
	pool               *pgxpool.Pool
	bus                *eventbus.Bus
	taskQueuedSub      eventbus.Subscription
	taskCompletedSub   eventbus.Subscription
	runCancellationSub eventbus.Subscription
	flowAdvancedSub    eventbus.Subscription
	tasks              tasksvc.TaskService
	flow               flowsvc.FlowExecutionService
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
		Projects:       repo.NewProjectRepo(pool),
		TaskService:    taskService,
		Flow:           flowService,
		FlowExecutions: repo.NewFlowNodeExecutionRepo(pool),
		FlowNodes:      repo.NewFlowNodeRepo(pool),
		Assignments:    repo.NewAgentProjectAssignmentRepo(pool),
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
	flowAdvancedSub := processor.SubscribeFlowAdvanced(&org.ID)
	projectResumedSub := processor.SubscribeProjectResumed(&org.ID)
	t.Cleanup(func() {
		bus.Unsubscribe(projectResumedSub)
	})

	return taskQueueProcessorFixture{
		pool:               pool,
		bus:                bus,
		taskQueuedSub:      subscription,
		taskCompletedSub:   taskCompletedSub,
		runCancellationSub: runCancellationSub,
		flowAdvancedSub:    flowAdvancedSub,
		tasks:              taskService,
		flow:               flowService,
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

func mustCreateTaskQueueAgentAssignment(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	role, displayName, agentType string,
) repo.Agent {
	t.Helper()

	agentRepo := repo.NewAgentRepo(pool)
	assignmentRepo := repo.NewAgentProjectAssignmentRepo(pool)

	agent, err := agentRepo.Create(ctx, repo.Agent{
		OrganizationID:       orgID,
		DisplayName:          displayName + "-" + strings.ToLower(uuid.NewString()[:8]),
		AgentClass:           "staff",
		LifecycleStatus:      "active",
		AgentType:            agentType,
		CreatedByType:        "system",
		CreatedByID:          uuid.Nil,
		MemoryReadScopes:     []string{},
		ToolAllowList:        []string{},
		ToolDenyList:         []string{},
		OperatorInstructions: "",
		SystemPrompt:         "",
	})
	if err != nil {
		t.Fatalf("create agent (%s): %v", role, err)
	}
	if _, err := assignmentRepo.Assign(ctx, repo.AgentProjectAssignment{
		AgentID:        agent.ID,
		ProjectID:      projectID,
		Role:           role,
		AssignedByType: "system",
	}); err != nil {
		t.Fatalf("assign agent role %s: %v", role, err)
	}
	return agent
}

func seedTaskQueueTransitionFlowTemplate(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	workerAgentID, reviewerAgentID uuid.UUID,
) (repo.FlowTemplate, repo.FlowNode, repo.FlowNode, repo.FlowNode) {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	actorAgent := "agent"

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-transition-" + uuid.NewString()[:8],
		DisplayName:    "Flow Transition",
		Description:    "transition template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create transition template: %v", err)
	}
	workA, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work A",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work A node: %v", err)
	}
	review, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		ActorType:      &actorAgent,
		ActorID:        &reviewerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	workB, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work B",
		NodeType:       "work",
		Position:       3,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work B node: %v", err)
	}

	workA.NextNodeID = &review.ID
	review.NextNodeID = &workB.ID
	if _, err := nodeRepo.Update(ctx, workA); err != nil {
		t.Fatalf("update work A edge: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, review); err != nil {
		t.Fatalf("update review edge: %v", err)
	}

	template.StartNodeID = &workA.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("set template start node: %v", err)
	}
	return template, workA, review, workB
}

func seedTaskQueueReviewCompletionFlowTemplate(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	workerAgentID, reviewerAgentID uuid.UUID,
) repo.FlowTemplate {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	actorAgent := "agent"

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-review-completion-" + uuid.NewString()[:8],
		DisplayName:    "Flow Review Completion",
		Description:    "review to completion template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create review completion template: %v", err)
	}
	work, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	review, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		ActorType:      &actorAgent,
		ActorID:        &reviewerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	completion, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create completion node: %v", err)
	}

	work.NextNodeID = &review.ID
	review.NextNodeID = &completion.ID
	if _, err := nodeRepo.Update(ctx, work); err != nil {
		t.Fatalf("update work edge: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, review); err != nil {
		t.Fatalf("update review edge: %v", err)
	}

	template.StartNodeID = &work.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("set review completion template start node: %v", err)
	}
	return template
}

func seedTaskQueueHumanReviewFlowTemplate(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	workerAgentID, reviewerAgentID uuid.UUID,
) (repo.FlowTemplate, repo.FlowNode, repo.FlowNode, repo.FlowNode) {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	actorAgent := "agent"

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-human-review-" + uuid.NewString()[:8],
		DisplayName:    "Flow Human Review",
		Description:    "human review template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create human review template: %v", err)
	}
	workA, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work A",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work A node: %v", err)
	}
	review, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID:      template.ID,
		DisplayName:         "Review",
		NodeType:            "review",
		Position:            2,
		ActorType:           &actorAgent,
		ActorID:             &reviewerAgentID,
		RequiresHumanReview: true,
		MaxVisits:           5,
	})
	if err != nil {
		t.Fatalf("create human review node: %v", err)
	}
	workB, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work B",
		NodeType:       "work",
		Position:       3,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work B node: %v", err)
	}

	workA.NextNodeID = &review.ID
	review.NextNodeID = &workB.ID
	if _, err := nodeRepo.Update(ctx, workA); err != nil {
		t.Fatalf("update work A edge: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, review); err != nil {
		t.Fatalf("update review edge: %v", err)
	}

	template.StartNodeID = &workA.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("set human review template start node: %v", err)
	}
	return template, workA, review, workB
}

func seedTaskQueueRejectFlowTemplate(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	orgID, projectID uuid.UUID,
	workerAgentID, reviewerAgentID uuid.UUID,
) (repo.FlowTemplate, repo.FlowNode, repo.FlowNode) {
	t.Helper()

	templateRepo := repo.NewFlowTemplateRepo(pool)
	nodeRepo := repo.NewFlowNodeRepo(pool)
	actorAgent := "agent"

	template, err := templateRepo.Create(ctx, repo.FlowTemplate{
		OrganizationID: &orgID,
		ProjectID:      &projectID,
		Slug:           "flow-reject-" + uuid.NewString()[:8],
		DisplayName:    "Flow Reject",
		Description:    "reject template",
		IsCurrent:      true,
		Version:        1,
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
	})
	if err != nil {
		t.Fatalf("create reject template: %v", err)
	}
	workNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		ActorType:      &actorAgent,
		ActorID:        &workerAgentID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create work node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		ActorType:      &actorAgent,
		ActorID:        &reviewerAgentID,
		RejectNodeID:   &workNode.ID,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create review node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	if _, err := nodeRepo.Update(ctx, workNode); err != nil {
		t.Fatalf("update work node edge: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("update review node edge: %v", err)
	}

	template.StartNodeID = &workNode.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("set reject template start node: %v", err)
	}
	return template, workNode, reviewNode
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
		DisplayName:    "Work",
		NodeType:       "work",
		Position:       1,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create work flow node: %v", err)
	}
	reviewNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Review",
		NodeType:       "review",
		Position:       2,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create review flow node: %v", err)
	}
	completionNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      3,
	})
	if err != nil {
		t.Fatalf("create completion flow node: %v", err)
	}

	startNode.NextNodeID = &reviewNode.ID
	reviewNode.NextNodeID = &completionNode.ID
	if _, err := nodeRepo.Update(ctx, startNode); err != nil {
		t.Fatalf("update work flow edge: %v", err)
	}
	if _, err := nodeRepo.Update(ctx, reviewNode); err != nil {
		t.Fatalf("update review flow edge: %v", err)
	}
	template.StartNodeID = &startNode.ID
	if _, err := templateRepo.Update(ctx, template); err != nil {
		t.Fatalf("update flow template start node: %v", err)
	}
	return template
}

func findAsyncDecisionArtifact(items []repo.InboxItem, taskID uuid.UUID, outcome string) (repo.InboxItem, bool) {
	for _, item := range items {
		if item.SourceTaskID == nil || *item.SourceTaskID != taskID {
			continue
		}
		if item.ItemType != "system_alert" {
			continue
		}
		var payload map[string]any
		if len(item.ActionPayload) == 0 {
			continue
		}
		if err := json.Unmarshal(item.ActionPayload, &payload); err != nil {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(payload["outcome"])) != outcome {
			continue
		}
		return item, true
	}
	return repo.InboxItem{}, false
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
