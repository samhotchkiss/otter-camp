//go:build integration

package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
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

func TestTaskQueueProcessorIntegrationResumeValidationBlockedTaskStartsFreshTurn(t *testing.T) {
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
		Title:           "Resume validation blocked task",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	guardedMetadata, err := tasksvc.MergeValidationGuardMetadata(taskRecord.Metadata, tasksvc.ValidationGuardState{
		InitialMessageID:   uuid.NewString(),
		Fingerprint:        "cli.execute:command_required",
		AttemptFingerprint: "cli.execute:command_required:attempt",
		ToolName:           "cli.execute",
		FailureClass:       "tool_validation",
		FailureCode:        "command_required",
		FailureReason:      "command is required",
		Count:              3,
		BlockThreshold:     3,
		Blocked:            true,
	})
	if err != nil {
		t.Fatalf("MergeValidationGuardMetadata: %v", err)
	}
	taskRecord.Metadata = guardedMetadata
	taskRecord.WorkStatus = "blocked"
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update blocked task: %v", err)
	}

	if _, err := fx.tasks.ResumeValidationBlockedTask(ctx, created.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	turnRepo := repo.NewChatTurnRepo(fx.pool)

	var (
		session       *repo.ChatSession
		turns         []repo.ChatTurn
		kickoffMeta   map[string]any
		foundKickoff  bool
		foundResponse bool
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		current, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if current.WorkStatus != "in_progress" {
			return false, nil
		}
		if _, ok := tasksvc.ParseValidationGuard(current.Metadata); ok {
			return false, fmt.Errorf("validation guard still present after resume")
		}

		session, err = sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}

		messages, err := messageRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		foundKickoff = false
		foundResponse = false
		kickoffMeta = nil
		for _, message := range messages {
			if message.Role == "user" && len(message.Metadata) > 0 {
				var metadata map[string]any
				if err := json.Unmarshal(message.Metadata, &metadata); err == nil && metadata["source"] == "task_queue_processor" {
					foundKickoff = true
					kickoffMeta = metadata
				}
			}
			if message.Role == "assistant" && message.Status == "final" && message.Content == "Task started." {
				foundResponse = true
			}
		}
		if !foundKickoff || !foundResponse {
			return false, nil
		}

		turns, err = turnRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		if len(turns) == 0 {
			return false, nil
		}
		return turns[len(turns)-1].Status == "completed", nil
	})

	if session == nil || session.ID == uuid.Nil {
		t.Fatal("expected canonical async task session after resume")
	}
	if len(turns) == 0 {
		t.Fatal("expected a fresh task turn after resume")
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("latest turn status = %q, want completed", turns[len(turns)-1].Status)
	}
	if kickoffMeta == nil {
		t.Fatal("expected kickoff metadata after validation resume")
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["recovery_action"])); got != "resume_validation_blocked_task" {
		t.Fatalf("kickoff recovery_action = %q, want %q", got, "resume_validation_blocked_task")
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["validation_failure_code"])); got != "command_required" {
		t.Fatalf("kickoff validation_failure_code = %q, want %q", got, "command_required")
	}
}

func TestTaskQueueProcessorIntegrationResumeDurableRecoveryCheckpointCreatesFollowOnTurnEX324(t *testing.T) {
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
		Title:           "Resume durable checkpoint task",
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	targetPath := "docs/content-strategy.md"
	artifactPath := ".ottercamp/recovery/docs/content-strategy.md"
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    targetPath,
		ArtifactPath:  artifactPath,
		FailureReason: "assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body",
		HaltTurnID:    uuid.NewString(),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRecord.Metadata = checkpointMetadata
	taskRecord.WorkStatus = "blocked"
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update blocked task: %v", err)
	}

	if _, err := fx.tasks.ResumeValidationBlockedTask(ctx, created.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	turnRepo := repo.NewChatTurnRepo(fx.pool)

	var (
		session       *repo.ChatSession
		turns         []repo.ChatTurn
		kickoffMeta   map[string]any
		foundKickoff  bool
		foundResponse bool
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		current, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if current.WorkStatus != "in_progress" {
			return false, nil
		}
		if checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(current.Metadata); !ok || checkpoint.ArtifactPath != artifactPath {
			return false, fmt.Errorf("durable recovery checkpoint missing after resume")
		}

		session, err = sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}

		messages, err := messageRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		foundKickoff = false
		foundResponse = false
		kickoffMeta = nil
		for _, message := range messages {
			if message.Role == "user" && len(message.Metadata) > 0 {
				var metadata map[string]any
				if err := json.Unmarshal(message.Metadata, &metadata); err == nil && metadata["source"] == "task_queue_processor" {
					foundKickoff = true
					kickoffMeta = metadata
				}
			}
			if message.Role == "assistant" && message.Status == "final" && message.Content == "Task started." {
				foundResponse = true
			}
		}
		if !foundKickoff || !foundResponse {
			return false, nil
		}

		turns, err = turnRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		if len(turns) == 0 {
			return false, nil
		}
		return turns[len(turns)-1].Status == "completed", nil
	})

	if session == nil || session.ID == uuid.Nil {
		t.Fatal("expected canonical async task session after checkpoint resume")
	}
	if len(turns) == 0 {
		t.Fatal("expected a fresh task turn after checkpoint resume")
	}
	if turns[len(turns)-1].Status != "completed" {
		t.Fatalf("latest turn status = %q, want completed", turns[len(turns)-1].Status)
	}
	if kickoffMeta == nil {
		t.Fatal("expected kickoff metadata after checkpoint resume")
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["recovery_action"])); got != tasksvc.RecoveryActionResumeBlockedTask {
		t.Fatalf("kickoff recovery_action = %q, want %q", got, tasksvc.RecoveryActionResumeBlockedTask)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["recovery_blocker_class"])); got != tasksvc.RecoveryBlockerClassDurableRecoveryCheckpoint {
		t.Fatalf("kickoff recovery_blocker_class = %q, want %q", got, tasksvc.RecoveryBlockerClassDurableRecoveryCheckpoint)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["recovery_checkpoint_artifact_path"])); got != artifactPath {
		t.Fatalf("kickoff recovery_checkpoint_artifact_path = %q, want %q", got, artifactPath)
	}
}

func TestTaskQueueProcessorIntegrationResumeRepairedDurableCheckpointCreatesFollowOnTurnEX325(t *testing.T) {
	ctx := context.Background()
	t.Setenv("OTTERCAMP_DATA_DIR", t.TempDir())
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
		Title:           "Resume repaired checkpoint task",
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
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	const (
		targetPath    = "docs/content-strategy.md"
		artifactPath  = ".ottercamp/recovery/docs/content-strategy.md"
		failureReason = "assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body"
	)
	targetBody := "# Content Strategy\n\n- Resume from the repaired durable checkpoint.\n"
	writeTaskQueueRecoveryWorkspaceFiles(t, fx.project.Slug, targetPath, artifactPath, targetBody, failureReason)

	blockerReason := "recovery halted after assistant draft for docs/content-strategy.md described tool-recovery troubleshooting instead of the file body; resume from .ottercamp/recovery/docs/content-strategy.md and re-queue only after concrete content exists"
	if _, err := fx.tasks.MarkBlocked(ctx, created.ID, blockerReason, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	blocked, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID blocked task: %v", err)
	}
	if _, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(blocked.Metadata); ok {
		t.Fatalf("expected blocked task to start without checkpoint metadata, metadata=%s", string(blocked.Metadata))
	}

	if _, err := fx.tasks.ResumeValidationBlockedTask(ctx, created.ID, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("ResumeValidationBlockedTask: %v", err)
	}

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	turnRepo := repo.NewChatTurnRepo(fx.pool)

	var (
		session       *repo.ChatSession
		turns         []repo.ChatTurn
		kickoffMeta   map[string]any
		foundKickoff  bool
		foundResponse bool
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		current, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if current.WorkStatus != "in_progress" {
			return false, nil
		}
		checkpoint, ok := taskcheckpoint.ParseRecoveryFileWriteCheckpoint(current.Metadata)
		if !ok || checkpoint.ArtifactPath != artifactPath {
			return false, fmt.Errorf("repaired durable recovery checkpoint missing after resume")
		}

		session, err = sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return false, nil
			}
			return false, err
		}

		messages, err := messageRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		foundKickoff = false
		foundResponse = false
		kickoffMeta = nil
		for _, message := range messages {
			if message.Role == "user" && len(message.Metadata) > 0 {
				var metadata map[string]any
				if err := json.Unmarshal(message.Metadata, &metadata); err == nil && metadata["source"] == "task_queue_processor" {
					foundKickoff = true
					kickoffMeta = metadata
				}
			}
			if message.Role == "assistant" && message.Status == "final" && message.Content == "Task started." {
				foundResponse = true
			}
		}
		if !foundKickoff || !foundResponse {
			return false, nil
		}

		turns, err = turnRepo.ListBySession(ctx, session.ID)
		if err != nil {
			return false, err
		}
		if len(turns) == 0 {
			return false, nil
		}
		return turns[len(turns)-1].Status == "completed", nil
	})

	if session == nil || session.ID == uuid.Nil {
		t.Fatal("expected canonical async task session after repaired checkpoint resume")
	}
	if len(turns) == 0 {
		t.Fatal("expected a fresh task turn after repaired checkpoint resume")
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", kickoffMeta["recovery_checkpoint_artifact_path"])); got != artifactPath {
		t.Fatalf("kickoff recovery_checkpoint_artifact_path = %q, want %q", got, artifactPath)
	}
}

func TestTaskQueueProcessorIntegrationQueueRejectsNonGateTaskWhileOutstandingGateExistsEX256(t *testing.T) {
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

	if _, err := fx.tasks.TransitionStatus(ctx, regularTask.ID, "queued", tasksvc.Actor{Type: "system"}); !errors.Is(err, tasksvc.ErrProjectGateBlockingQueue) {
		t.Fatalf("TransitionStatus regular queued err = %v, want ErrProjectGateBlockingQueue", err)
	}
	regularAfter, err := taskRepo.GetByID(ctx, regularTask.ID)
	if err != nil {
		t.Fatalf("GetByID regular: %v", err)
	}
	if regularAfter.WorkStatus != "draft" {
		t.Fatalf("regular task work_status = %q, want draft while gate is outstanding", regularAfter.WorkStatus)
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

	if _, err := fx.tasks.TransitionStatus(ctx, gateTask.ID, "done", tasksvc.Actor{Type: "system", AllowDoneBypass: true}); err != nil {
		t.Fatalf("TransitionStatus gate done: %v", err)
	}

	if _, err := fx.tasks.TransitionStatus(ctx, regularTask.ID, "queued", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("TransitionStatus regular queued after gate done: %v", err)
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
	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)
	inboxRepo := repo.NewInboxItemRepo(fx.pool)

	var (
		taskRecord repo.ProjectTask
		execution  repo.FlowNodeExecution
		artifact   repo.InboxItem
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var waitErr error
		taskRecord, waitErr = taskRepo.GetByID(ctx, created.ID)
		if waitErr != nil {
			return false, waitErr
		}
		if taskRecord.WorkStatus != "review" || taskRecord.CurrentFlowNodeID == nil {
			return false, nil
		}
		execution, waitErr = executionRepo.GetActive(ctx, created.ID, *taskRecord.CurrentFlowNodeID)
		if waitErr != nil {
			if errors.Is(waitErr, repo.ErrNotFound) {
				return false, nil
			}
			return false, waitErr
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
	if taskRecord.CurrentFlowNodeID == nil {
		t.Fatal("task current_flow_node_id = nil, want active flow node while paused in review")
	}
	if execution.ID == uuid.Nil {
		t.Fatal("active flow execution id = nil, want self-healed execution state")
	}
	if execution.FlowNodeID != *taskRecord.CurrentFlowNodeID {
		t.Fatalf("active flow execution node = %s, want %s", execution.FlowNodeID, *taskRecord.CurrentFlowNodeID)
	}
	if len(runs) != 0 {
		t.Fatalf("run count = %d, want 0 for hard-stop task", len(runs))
	}
	if artifact.SourceTaskID == nil || *artifact.SourceTaskID != created.ID {
		t.Fatalf("artifact source_task_id = %v, want %s", artifact.SourceTaskID, created.ID)
	}
}

func TestTaskQueueProcessorIntegrationDurableRecoveryBlockKeepsRuntimeResumableEX331(t *testing.T) {
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
		Title:           "Blocked recovery task should stay resumable",
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

	runRepo := NewRunRepository(fx.pool)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	var runRecord Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
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

	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	checkpointMetadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(taskRecord.Metadata, taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    "docs/blog-post-ideas.md",
		ArtifactPath:  ".ottercamp/recovery/docs/blog-post-ideas.md",
		FailureReason: "assistant draft for docs/blog-post-ideas.md described intent to write the deliverable instead of the file body",
		HaltTurnID:    uuid.NewString(),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}
	taskRecord.Metadata = checkpointMetadata
	if _, err := taskRepo.Update(ctx, taskRecord); err != nil {
		t.Fatalf("Update task checkpoint metadata: %v", err)
	}

	blockReason := "recovery halted after assistant draft for docs/blog-post-ideas.md described intent to write the deliverable instead of the file body; resume from .ottercamp/recovery/docs/blog-post-ideas.md and re-queue only after concrete content exists"
	if _, err := fx.tasks.MarkBlocked(ctx, created.ID, blockReason, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		updatedRun, err := runRepo.Get(ctx, runRecord.ID)
		if err != nil {
			return false, err
		}
		if updatedRun.Status != "failed" {
			return false, nil
		}
		contract := loadTaskRuntimeState(t, ctx, fx.pool, created.ID).Contract()
		return contract.Status == "resumable", nil
	})

	updatedRun, err := runRepo.Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("run status = %q, want failed", updatedRun.Status)
	}
	if updatedRun.FailureClass == nil || *updatedRun.FailureClass != string(FailureClassTransient) {
		t.Fatalf("failure_class = %v, want %q", updatedRun.FailureClass, FailureClassTransient)
	}

	contract := loadTaskRuntimeState(t, ctx, fx.pool, created.ID).Contract()
	if contract.Status != "resumable" {
		t.Fatalf("runtime status = %q, want resumable", contract.Status)
	}
	if contract.ResumeDisposition != "resumable" {
		t.Fatalf("runtime resume_disposition = %q, want resumable", contract.ResumeDisposition)
	}
	if contract.FailureClass != string(FailureClassTransient) {
		t.Fatalf("runtime failure_class = %q, want %q", contract.FailureClass, FailureClassTransient)
	}
	if contract.FailureReason != blockReason {
		t.Fatalf("runtime failure_reason = %q, want %q", contract.FailureReason, blockReason)
	}
}

func TestTaskQueueProcessorIntegrationAsyncDecisionDoesNotBypassActiveFlowNodeEX330(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Async Reviewer", "reviewer")
	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, reviewer.ID)
	description := "Human review before continuing, but this task is already running inside an explicit flow step."

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Resume async review without bypassing flow",
		Description:     &description,
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	started, err := fx.flow.StartFlow(ctx, created.ID)
	if err != nil {
		t.Fatalf("StartFlow: %v", err)
	}

	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	if _, err := taskRepo.UpdateStatus(ctx, created.ID, "queued"); err != nil {
		t.Fatalf("UpdateStatus queued: %v", err)
	}

	event := eventbus.DomainEvent{
		EventType:      "task.status_changed",
		OrganizationID: fx.org.ID,
	}
	if err := fx.processor.processQueuedTask(ctx, event, created.ID); err != nil {
		t.Fatalf("processQueuedTask: %v", err)
	}

	taskRecord, err := taskRepo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if taskRecord.WorkStatus != "in_progress" {
		t.Fatalf("task work_status = %q, want in_progress", taskRecord.WorkStatus)
	}
	if taskRecord.CurrentFlowNodeID == nil {
		t.Fatal("task current_flow_node_id = nil, want existing work node")
	}
	if *taskRecord.CurrentFlowNodeID != started.FlowNodeID {
		t.Fatalf("task current_flow_node_id = %s, want %s", *taskRecord.CurrentFlowNodeID, started.FlowNodeID)
	}

	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	execution, err := executionRepo.GetActive(ctx, created.ID, started.FlowNodeID)
	if err != nil {
		t.Fatalf("GetActive execution: %v", err)
	}
	if execution.ID != started.ID {
		t.Fatalf("active execution = %s, want %s", execution.ID, started.ID)
	}

	runRepo := NewRunRepository(fx.pool)
	runs, err := runRepo.List(ctx, RunListFilter{
		OrganizationID: fx.org.ID,
		TaskID:         &created.ID,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected queued flow-backed task to keep running after async review artifact")
	}

	inboxRepo := repo.NewInboxItemRepo(fx.pool)
	items, err := inboxRepo.ListBroadcast(ctx, fx.org.ID, repo.InboxListOptions{
		IncludeActed: true,
		ItemType:     "system_alert",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("ListBroadcast inbox: %v", err)
	}
	artifact, found := findAsyncDecisionArtifact(items, created.ID, taskplan.AsyncDecisionPrepareForReview)
	if !found {
		t.Fatal("expected async review artifact for active flow-backed task")
	}
	if artifact.SourceTaskID == nil || *artifact.SourceTaskID != created.ID {
		t.Fatalf("artifact source_task_id = %v, want %s", artifact.SourceTaskID, created.ID)
	}
}

func TestTaskQueueProcessorIntegrationReviewEventSelfHealsMissingExecutionStateEX293(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	template := seedTaskQueueReviewCompletionFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, fx.agent.ID, fx.agent.ID)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Corrupted review task",
		Description:     stringPtr("Repair missing flow execution state before keeping this task in review."),
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskRepo.UpdateStatus(ctx, created.ID, "review"); err != nil {
		t.Fatalf("UpdateStatus review: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":    created.ID.String(),
		"project_id": fx.project.ID.String(),
		"to_status":  "review",
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
		t.Fatalf("publish task.status_changed review: %v", err)
	}

	var (
		taskRecord repo.ProjectTask
		execution  repo.FlowNodeExecution
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var waitErr error
		taskRecord, waitErr = taskRepo.GetByID(ctx, created.ID)
		if waitErr != nil {
			return false, waitErr
		}
		if taskRecord.WorkStatus != "review" || taskRecord.CurrentFlowNodeID == nil {
			return false, nil
		}
		execution, waitErr = executionRepo.GetActive(ctx, created.ID, *taskRecord.CurrentFlowNodeID)
		if waitErr != nil {
			if errors.Is(waitErr, repo.ErrNotFound) {
				return false, nil
			}
			return false, waitErr
		}
		return true, nil
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
	if taskRecord.CurrentFlowNodeID == nil {
		t.Fatal("task current_flow_node_id = nil after repair")
	}
	if execution.ID == uuid.Nil {
		t.Fatal("active flow execution id = nil after repair")
	}
	if execution.FlowNodeID != *taskRecord.CurrentFlowNodeID {
		t.Fatalf("active flow execution node = %s, want %s", execution.FlowNodeID, *taskRecord.CurrentFlowNodeID)
	}
	if len(runs) != 0 {
		t.Fatalf("run count = %d, want 0 while task remains in review", len(runs))
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

func TestTaskQueueProcessorIntegrationInProgressAssignedAgentTaskCreatesTaskSessionAndKickoff(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	participantRepo := repo.NewChatParticipantRepo(fx.pool)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)
	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	projectSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: fx.org.ID,
		ScopeType:      "project",
		ScopeID:        fx.project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test"}`),
	})
	if err != nil {
		t.Fatalf("Create project-scoped async session: %v", err)
	}

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Direct in-progress assigned-agent task",
		Description:     stringPtr("Start work immediately in the task-scoped async session."),
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskRepo.UpdateStatus(ctx, created.ID, "on_hold"); err != nil {
		t.Fatalf("UpdateStatus on_hold: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	var (
		taskSession repo.ChatSession
		runRecord   Run
	)
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		session, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			return false, err
		}
		if session == nil {
			return false, nil
		}
		taskSession = *session

		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		var boundRun Run
		for _, candidate := range runs {
			if candidate.Status == "in_progress" && candidate.SessionID != nil && *candidate.SessionID == taskSession.ID {
				boundRun = candidate
				break
			}
		}
		if boundRun.ID == uuid.Nil {
			return false, nil
		}
		runRecord = boundRun

		messages, err := messageRepo.ListBySession(ctx, taskSession.ID)
		if err != nil {
			return false, err
		}
		return hasTaskQueueKickoffMessage(messages, created.ID), nil
	})

	if runRecord.ID == uuid.Nil {
		t.Fatal("expected in_progress run bound to task session")
	}
	if runRecord.SessionID == nil || *runRecord.SessionID != taskSession.ID {
		t.Fatalf("run session_id = %v, want %s", runRecord.SessionID, taskSession.ID)
	}
	if taskSession.ID == projectSession.ID {
		t.Fatalf("task session reused project-scoped session %s", projectSession.ID)
	}

	var taskSessionCount int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM chat_session
		WHERE scope_type = 'project_task'
		  AND scope_id = $1
		  AND mode = 'async'
	`, created.ID).Scan(&taskSessionCount); err != nil {
		t.Fatalf("count task-scoped async sessions: %v", err)
	}
	if taskSessionCount != 1 {
		t.Fatalf("task-scoped async session count = %d, want 1", taskSessionCount)
	}

	participants, err := participantRepo.ListBySession(ctx, taskSession.ID)
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

	projectMessages, err := messageRepo.ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project session messages: %v", err)
	}
	for _, message := range projectMessages {
		if message.Role == "assistant" && message.Status == "final" {
			t.Fatalf("project-scoped session captured assistant task work: %+v", message)
		}
		if message.Role != "user" || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if unmarshalErr := json.Unmarshal(message.Metadata, &metadata); unmarshalErr != nil {
			continue
		}
		if metadata["source"] == "task_queue_processor" {
			t.Fatalf("project-scoped session captured task_queue_processor kickoff: %+v", message)
		}
	}
}

func TestTaskQueueProcessorIntegrationBlockedTaskFailsTrackingRunAndMarksRuntimeTerminalEX318(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	runRepo := NewRunRepository(fx.pool)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Provider auth blocked task",
		Description:     stringPtr("Bounded auth failure should stop runtime truthfully."),
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

	var runRecord Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
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
			if candidate.Status == "in_progress" {
				runRecord = candidate
				return true, nil
			}
		}
		return false, nil
	})

	blockReason := "provider authentication failed on every eligible model connection"
	if _, err := fx.tasks.MarkBlocked(ctx, created.ID, blockReason, tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		updatedRun, err := runRepo.Get(ctx, runRecord.ID)
		if err != nil {
			return false, err
		}
		if updatedRun.Status != "failed" {
			return false, nil
		}
		contract := loadTaskRuntimeState(t, ctx, fx.pool, created.ID).Contract()
		return contract.Status == "terminal", nil
	})

	updatedRun, err := runRepo.Get(ctx, runRecord.ID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if updatedRun.Status != "failed" {
		t.Fatalf("run status = %q, want failed", updatedRun.Status)
	}
	if updatedRun.FailureClass == nil || *updatedRun.FailureClass != string(FailureClassPermanent) {
		t.Fatalf("failure_class = %v, want %q", updatedRun.FailureClass, FailureClassPermanent)
	}
	if updatedRun.FailureReason == nil || *updatedRun.FailureReason != blockReason {
		t.Fatalf("failure_reason = %v, want %q", updatedRun.FailureReason, blockReason)
	}

	contract := loadTaskRuntimeState(t, ctx, fx.pool, created.ID).Contract()
	if contract.Status != "terminal" {
		t.Fatalf("runtime status = %q, want terminal", contract.Status)
	}
	if contract.ResumeDisposition != "terminal" {
		t.Fatalf("runtime resume_disposition = %q, want terminal", contract.ResumeDisposition)
	}
	if contract.FailureClass != string(FailureClassPermanent) {
		t.Fatalf("runtime failure_class = %q, want %q", contract.FailureClass, FailureClassPermanent)
	}
	if contract.FailureReason != blockReason {
		t.Fatalf("runtime failure_reason = %q, want %q", contract.FailureReason, blockReason)
	}
}

func TestTaskQueueProcessorIntegrationAssignedWakeupIgnoresProjectScopedRunSessionEX294(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	participantRepo := repo.NewChatParticipantRepo(fx.pool)

	projectSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: fx.org.ID,
		ScopeType:      "project",
		ScopeID:        fx.project.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test"}`),
	})
	if err != nil {
		t.Fatalf("Create project-scoped async session: %v", err)
	}

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Canonical task session dispatch",
		Description:     stringPtr("Dispatch task work through the canonical task-scoped session even when a project-scoped PM session exists."),
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	taskSession, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: fx.org.ID,
		ScopeType:      "project_task",
		ScopeID:        created.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test","canonical":true}`),
	})
	if err != nil {
		t.Fatalf("Create canonical task session: %v", err)
	}
	if _, err := sessionRepo.IncrementCounts(ctx, taskSession.ID, 0, 1); err != nil {
		t.Fatalf("IncrementCounts canonical task session: %v", err)
	}

	blankDuplicate, err := sessionRepo.Create(ctx, repo.ChatSession{
		OrganizationID: fx.org.ID,
		ScopeType:      "project_task",
		ScopeID:        created.ID,
		Mode:           "async",
		Status:         "active",
		CreatedByType:  "system",
		CreatedByID:    uuid.Nil,
		Metadata:       json.RawMessage(`{"source":"task_queue_processor_integration_test","duplicate_blank":true}`),
	})
	if err != nil {
		t.Fatalf("Create blank duplicate task session: %v", err)
	}

	runRecord := Run{
		ID:             uuid.New(),
		OrganizationID: fx.org.ID,
		ProjectID:      &fx.project.ID,
		TaskID:         &created.ID,
		SessionID:      &projectSession.ID,
		PrincipalType:  "agent",
		PrincipalID:    fx.agent.ID,
		Status:         "in_progress",
		TriggerType:    taskQueueTriggerType,
		Metadata: buildExecutionWakeupMetadata(nil, executionScope{Type: "task", ID: created.ID}, "task_queue_processor", "assigned_task", map[string]any{
			"source":   "task_queue_processor",
			"run_mode": "async",
		}, "started", nil),
	}

	if err := fx.processor.dispatchTaskQueueWakeup(ctx, runRecord); err != nil {
		t.Fatalf("dispatchTaskQueueWakeup: %v", err)
	}

	taskMessages, err := messageRepo.ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession task session: %v", err)
	}
	if !hasTaskQueueKickoffMessage(taskMessages, created.ID) {
		t.Fatal("expected kickoff message on canonical task session")
	}

	projectMessages, err := messageRepo.ListBySession(ctx, projectSession.ID)
	if err != nil {
		t.Fatalf("ListBySession project session: %v", err)
	}
	for _, message := range projectMessages {
		if message.Role != "user" || len(message.Metadata) == 0 {
			continue
		}
		var metadata map[string]any
		if unmarshalErr := json.Unmarshal(message.Metadata, &metadata); unmarshalErr != nil {
			continue
		}
		if metadata["source"] == "task_queue_processor" {
			t.Fatalf("project-scoped session captured task wakeup: %+v", message)
		}
	}

	participants, err := participantRepo.ListBySession(ctx, taskSession.ID)
	if err != nil {
		t.Fatalf("ListBySession task participants: %v", err)
	}
	var foundAgentResponder bool
	for _, participant := range participants {
		if participant.ParticipantType == "agent" && participant.ParticipantID == fx.agent.ID {
			foundAgentResponder = true
			break
		}
	}
	if !foundAgentResponder {
		t.Fatal("expected responder participant on canonical task session")
	}

	duplicateStored, err := sessionRepo.GetByID(ctx, blankDuplicate.ID)
	if err != nil {
		t.Fatalf("GetByID blank duplicate: %v", err)
	}
	if duplicateStored.Status != "closed" || duplicateStored.ClosedAt == nil {
		t.Fatalf("blank duplicate task session = %+v, want closed with closed_at", duplicateStored)
	}
}

func TestTaskQueueProcessorIntegrationRepeatedInProgressEventsReuseTaskSession(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	sessionRepo := repo.NewChatSessionRepo(fx.pool)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	runRepo := NewRunRepository(fx.pool)
	runEventRepo := NewRunEventRepository(fx.pool)
	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Repeated in-progress session reuse",
		Description:     stringPtr("Reuse the existing task session on restart or recovery."),
		FlowTemplateID:  &template.ID,
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := taskRepo.UpdateStatus(ctx, created.ID, "on_hold"); err != nil {
		t.Fatalf("UpdateStatus on_hold: %v", err)
	}
	if _, err := fx.tasks.TransitionStatus(ctx, created.ID, "in_progress", tasksvc.Actor{Type: "system", AllowNoActiveFlow: true}); err != nil {
		t.Fatalf("TransitionStatus in_progress: %v", err)
	}

	var initialSession repo.ChatSession
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		session, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			return false, err
		}
		if session == nil {
			return false, nil
		}
		initialSession = *session

		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		return len(runs) >= 1, nil
	})

	payload, err := json.Marshal(map[string]any{
		"task_id":    created.ID.String(),
		"project_id": fx.project.ID.String(),
		"to_status":  "in_progress",
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

	var activeRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		current, err := sessionRepo.GetByScopeAndMode(ctx, "project_task", created.ID, "async")
		if err != nil {
			return false, err
		}
		if current == nil || current.ID != initialSession.ID {
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
		if len(runs) != 1 || runs[0].Status != "in_progress" {
			return false, nil
		}
		activeRun = runs[0]

		var taskSessionCount int
		if err := fx.pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM chat_session
			WHERE scope_type = 'project_task'
			  AND scope_id = $1
			  AND mode = 'async'
		`, created.ID).Scan(&taskSessionCount); err != nil {
			return false, err
		}
		if taskSessionCount != 1 {
			return false, nil
		}

		events, err := runEventRepo.ListByRun(ctx, activeRun.ID, 0)
		if err != nil {
			return false, err
		}
		for _, event := range events {
			if event.EventType == "wakeup_coalesced" {
				return true, nil
			}
		}
		return false, nil
	})

	if activeRun.SessionID == nil || *activeRun.SessionID != initialSession.ID {
		t.Fatalf("run %s session_id = %v, want %s", activeRun.ID, activeRun.SessionID, initialSession.ID)
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
	var workExecution repo.FlowNodeExecution

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		workExecution, err = executionRepo.GetActive(ctx, created.ID, nodeWork.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		return workExecution.SessionID != nil && *workExecution.SessionID != uuid.Nil, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	var reviewExecution repo.FlowNodeExecution
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		reviewExecution, err = executionRepo.GetActive(ctx, created.ID, nodeReview.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if reviewExecution.SessionID == nil || *reviewExecution.SessionID == uuid.Nil {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeReview.ID,
			Status:         "created",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		reviewDeferred := false
		for _, runRecord := range runs {
			if runRecord.PrincipalType == "agent" && runRecord.PrincipalID == reviewer.ID && runRecord.SessionID != nil && *runRecord.SessionID == *reviewExecution.SessionID {
				reviewDeferred = true
				break
			}
		}
		return reviewDeferred, nil
	})

	publishTaskTurnCompleted(t, ctx, fx.bus, fx.org.ID, *workExecution.SessionID)

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeReview.ID,
			Status:         "in_progress",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		reviewStarted := false
		for _, runRecord := range runs {
			if runRecord.PrincipalType == "agent" && runRecord.PrincipalID == reviewer.ID && runRecord.SessionID != nil && *runRecord.SessionID == *reviewExecution.SessionID {
				reviewStarted = true
				break
			}
		}
		if !reviewStarted {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *reviewExecution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", reviewExecution.ID), nil
	})

	if _, err := fx.flow.RejectFlowNode(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("RejectFlowNode review->work: %v", err)
	}

	var rejectExecution repo.FlowNodeExecution
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		if strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "blocked") {
			return false, nil
		}
		rejectExecution, err = executionRepo.GetActive(ctx, created.ID, nodeWork.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if rejectExecution.SessionID == nil || *rejectExecution.SessionID == uuid.Nil {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWork.ID,
			Status:         "created",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		for _, runRecord := range runs {
			if runRecord.PrincipalType == "agent" && runRecord.PrincipalID == worker.ID && runRecord.SessionID != nil && *runRecord.SessionID == *rejectExecution.SessionID {
				return true, nil
			}
		}
		return false, nil
	})

	publishTaskTurnCompleted(t, ctx, fx.bus, fx.org.ID, *reviewExecution.SessionID)

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWork.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		workerStarted := false
		for _, runRecord := range runs {
			if runRecord.Status == "in_progress" && runRecord.PrincipalType == "agent" && runRecord.PrincipalID == worker.ID && runRecord.SessionID != nil && *runRecord.SessionID == *rejectExecution.SessionID {
				workerStarted = true
				break
			}
		}
		if !workerStarted {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *rejectExecution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.rejected", rejectExecution.ID), nil
	})
}

func TestTaskQueueProcessorIntegrationRejectFlowDispatchesFromStaleBlockedReviewState(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Blocked Review Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Blocked Review Reviewer", "reviewer")
	template, nodeWork, nodeReview := seedTaskQueueRejectFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Stale blocked review reject",
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

	var reviewExecution repo.FlowNodeExecution
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		reviewExecution, err = executionRepo.GetActive(ctx, created.ID, nodeReview.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		return reviewExecution.SessionID != nil && *reviewExecution.SessionID != uuid.Nil, nil
	})

	if _, err := fx.tasks.MarkBlocked(ctx, created.ID, "stale blocked review state", tasksvc.Actor{Type: "system"}); err != nil {
		t.Fatalf("MarkBlocked: %v", err)
	}
	if _, err := fx.flow.RejectFlowNode(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: reviewer.ID}); err != nil {
		t.Fatalf("RejectFlowNode review->work: %v", err)
	}

	var rejectExecution repo.FlowNodeExecution
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		if taskRecord.WorkStatus == "blocked" {
			return false, nil
		}
		rejectExecution, err = executionRepo.GetActive(ctx, created.ID, nodeWork.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if rejectExecution.SessionID == nil || *rejectExecution.SessionID == uuid.Nil {
			return false, nil
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
		for _, runRecord := range runs {
			if runRecord.Status == "in_progress" && runRecord.PrincipalType == "agent" && runRecord.PrincipalID == worker.ID && runRecord.SessionID != nil && *runRecord.SessionID == *rejectExecution.SessionID {
				messages, err := messageRepo.ListBySession(ctx, *rejectExecution.SessionID)
				if err != nil {
					return false, err
				}
				return hasFlowKickoffMessage(messages, "flow.rejected", rejectExecution.ID), nil
			}
		}
		return false, nil
	})
}

func TestTaskQueueProcessorIntegrationDifferentOwnerWakeupDefersUntilTurnExit(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Deferred Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Deferred Reviewer", "reviewer")
	template, nodeWork, nodeReview := seedTaskQueueRejectFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Deferred reviewer wakeup",
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

	var workExecution repo.FlowNodeExecution
	var workRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		workExecution, err = executionRepo.GetActive(ctx, created.ID, nodeWork.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		if workExecution.SessionID == nil || *workExecution.SessionID == uuid.Nil {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWork.ID,
			Status:         "in_progress",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		for _, candidate := range runs {
			if candidate.PrincipalType == "agent" && candidate.PrincipalID == worker.ID {
				workRun = candidate
				return true, nil
			}
		}
		return false, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	var reviewExecution repo.FlowNodeExecution
	var reviewRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		reviewExecution, err = executionRepo.GetActive(ctx, created.ID, nodeReview.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeReview.ID,
			Status:         "created",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if len(runs) != 1 {
			return false, nil
		}
		reviewRun = runs[0]
		return true, nil
	})

	if reviewRun.Status != "created" {
		t.Fatalf("review run status = %q, want created while worker is active", reviewRun.Status)
	}
	state := loadTaskRuntimeState(t, ctx, fx.pool, created.ID)
	contract := state.Contract()
	if contract.Status != "active" {
		t.Fatalf("runtime status before release = %q, want active", contract.Status)
	}
	if contract.DeferredRunID == nil || *contract.DeferredRunID != reviewRun.ID {
		t.Fatalf("runtime deferred_run_id = %v, want %s", contract.DeferredRunID, reviewRun.ID)
	}

	payload, err := json.Marshal(map[string]any{
		"session_id": workExecution.SessionID.String(),
		"turn_id":    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("marshal turn completed payload: %v", err)
	}
	if err := fx.bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: fx.org.ID,
		EventType:      "chat.turn.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish chat.turn.completed: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		runRecord, err := runRepo.Get(ctx, reviewRun.ID)
		if err != nil {
			return false, err
		}
		if runRecord.Status != "in_progress" {
			return false, nil
		}
		refreshedExecution, err := executionRepo.GetByID(ctx, reviewExecution.ID)
		if err != nil {
			return false, err
		}
		reviewExecution = refreshedExecution
		if reviewExecution.SessionID == nil || *reviewExecution.SessionID == uuid.Nil {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *reviewExecution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", reviewExecution.ID), nil
	})

	updatedWorkRun, err := runRepo.Get(ctx, workRun.ID)
	if err != nil {
		t.Fatalf("Get worker run: %v", err)
	}
	if updatedWorkRun.Status != "in_progress" {
		t.Fatalf("worker run status = %q, want in_progress tracking state", updatedWorkRun.Status)
	}
	state = loadTaskRuntimeState(t, ctx, fx.pool, created.ID)
	contract = state.Contract()
	if contract.Status != "active" {
		t.Fatalf("runtime status after release = %q, want active", contract.Status)
	}
	if contract.LastProgressEvent != "wakeup_promoted" {
		t.Fatalf("runtime last_progress_event = %q, want wakeup_promoted", contract.LastProgressEvent)
	}
}

func TestTaskQueueProcessorIntegrationDuplicateWakeupsCoalesceToOneActiveExecution(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	template := seedTaskQueueFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID)
	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Duplicate wakeups coalesce",
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

	runRepo := NewRunRepository(fx.pool)
	taskRepo := repo.NewProjectTaskRepo(fx.pool)
	executionRepo := repo.NewFlowNodeExecutionRepo(fx.pool)
	messageRepo := repo.NewChatMessageRepo(fx.pool)
	var taskRecord repo.ProjectTask

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		var err error
		taskRecord, err = taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Status:         "in_progress",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		return len(runs) == 1, nil
	})

	payload, err := json.Marshal(map[string]any{
		"task_id":    created.ID,
		"project_id": fx.project.ID,
		"to_status":  "in_progress",
	})
	if err != nil {
		t.Fatalf("marshal duplicate payload: %v", err)
	}
	if err := fx.bus.Publish(ctx, nil, eventbus.DomainEvent{
		ID:             uuid.New(),
		OrganizationID: fx.org.ID,
		EventType:      "task.status_changed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish duplicate status event: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		active := 0
		for _, runRecord := range runs {
			if runRecord.Status == "in_progress" {
				active++
			}
		}
		if active != 1 {
			return false, nil
		}
		events, err := NewRunEventRepository(fx.pool).ListByRun(ctx, runs[0].ID, 0)
		if err != nil {
			return false, err
		}
		for _, event := range events {
			if event.EventType == "wakeup_coalesced" {
				return true, nil
			}
		}
		return false, nil
	})

	if taskRecord.CurrentFlowNodeID == nil {
		t.Fatal("current_flow_node_id is nil")
	}
	execution, err := executionRepo.GetActive(ctx, created.ID, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		t.Fatalf("GetActive execution: %v", err)
	}
	if execution.SessionID == nil || *execution.SessionID == uuid.Nil {
		t.Fatal("execution session_id is nil")
	}
	messages, err := messageRepo.ListBySession(ctx, *execution.SessionID)
	if err != nil {
		t.Fatalf("ListBySession messages: %v", err)
	}
	kickoffCount := 0
	for _, message := range messages {
		if message.Role == "user" && len(message.Metadata) > 0 {
			var metadata map[string]any
			if unmarshalErr := json.Unmarshal(message.Metadata, &metadata); unmarshalErr == nil && metadata["source"] == "task_queue_processor" {
				kickoffCount++
			}
		}
	}
	if kickoffCount != 1 {
		t.Fatalf("kickoff message count = %d, want 1", kickoffCount)
	}
}

func TestTaskQueueProcessorIntegrationStaleOwnerPromotesDeferredWakeupOnce(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	worker := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "worker", "Stale Worker", "worker")
	reviewer := mustCreateTaskQueueAgentAssignment(t, ctx, fx.pool, fx.org.ID, fx.project.ID, "reviewer", "Stale Reviewer", "reviewer")
	template, nodeWork, nodeReview := seedTaskQueueRejectFlowTemplate(t, ctx, fx.pool, fx.org.ID, fx.project.ID, worker.ID, reviewer.ID)

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Stale owner promotion",
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

	var workRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeWork.ID {
			return false, nil
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeWork.ID,
			Status:         "in_progress",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if len(runs) == 0 {
			return false, nil
		}
		workRun = runs[0]
		return true, nil
	})

	if _, err := fx.flow.AdvanceFlow(ctx, created.ID, flowsvc.Actor{Type: "agent", ID: worker.ID}); err != nil {
		t.Fatalf("AdvanceFlow work->review: %v", err)
	}

	var reviewExecution repo.FlowNodeExecution
	var reviewRun Run
	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		taskRecord, err := taskRepo.GetByID(ctx, created.ID)
		if err != nil {
			return false, err
		}
		if taskRecord.CurrentFlowNodeID == nil || *taskRecord.CurrentFlowNodeID != nodeReview.ID {
			return false, nil
		}
		reviewExecution, err = executionRepo.GetActive(ctx, created.ID, nodeReview.ID)
		if err != nil {
			if err == repo.ErrNotFound {
				return false, nil
			}
			return false, err
		}
		runs, err := runRepo.List(ctx, RunListFilter{
			OrganizationID: fx.org.ID,
			TaskID:         &created.ID,
			FlowNodeID:     &nodeReview.ID,
			Status:         "created",
			Limit:          20,
		})
		if err != nil {
			return false, err
		}
		if len(runs) != 1 {
			return false, nil
		}
		reviewRun = runs[0]
		return true, nil
	})

	if _, err := fx.pool.Exec(ctx, `
		UPDATE run
		SET updated_at = now() - interval '10 minutes'
		WHERE id = $1
	`, workRun.ID); err != nil {
		t.Fatalf("backdate worker run updated_at: %v", err)
	}

	payload, err := json.Marshal(map[string]any{
		"task_id":              created.ID,
		"project_id":           fx.project.ID,
		"to_flow_node_id":      nodeReview.ID,
		"to_flow_execution_id": reviewExecution.ID,
	})
	if err != nil {
		t.Fatalf("marshal duplicate flow event payload: %v", err)
	}
	if err := fx.bus.Publish(ctx, nil, eventbus.DomainEvent{
		ID:             uuid.New(),
		OrganizationID: fx.org.ID,
		EventType:      "flow.advanced",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish duplicate flow.advanced: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		promoted, err := runRepo.Get(ctx, reviewRun.ID)
		if err != nil {
			return false, err
		}
		if promoted.Status != "in_progress" {
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
		if len(runs) != 1 {
			return false, nil
		}
		refreshedExecution, err := executionRepo.GetByID(ctx, reviewExecution.ID)
		if err != nil {
			return false, err
		}
		reviewExecution = refreshedExecution
		if reviewExecution.SessionID == nil || *reviewExecution.SessionID == uuid.Nil {
			return false, nil
		}
		messages, err := messageRepo.ListBySession(ctx, *reviewExecution.SessionID)
		if err != nil {
			return false, err
		}
		return hasFlowKickoffMessage(messages, "flow.advanced", reviewExecution.ID), nil
	})

	updatedWorkerRun, err := runRepo.Get(ctx, workRun.ID)
	if err != nil {
		t.Fatalf("Get stale worker run: %v", err)
	}
	if updatedWorkerRun.Status != "failed" {
		t.Fatalf("stale worker run status = %q, want failed", updatedWorkerRun.Status)
	}
	state := loadTaskRuntimeState(t, ctx, fx.pool, created.ID)
	contract := state.Contract()
	if contract.Status != "active" {
		t.Fatalf("runtime status = %q, want active", contract.Status)
	}
	if contract.LastProgressEvent != "wakeup_promoted" {
		t.Fatalf("runtime last_progress_event = %q, want wakeup_promoted", contract.LastProgressEvent)
	}
}

func TestTaskQueueProcessorIntegrationProjectArchiveRetiresRuntimeStateAndBlocksResume(t *testing.T) {
	ctx := context.Background()
	fx := seedTaskQueueProcessorFixture(t, ctx)
	defer fx.bus.Unsubscribe(fx.taskQueuedSub)
	defer fx.bus.Unsubscribe(fx.taskCompletedSub)
	defer fx.bus.Unsubscribe(fx.runCancellationSub)
	defer fx.bus.Unsubscribe(fx.flowAdvancedSub)

	runService, err := NewRunService(RunServiceOptions{
		Pool:     fx.pool,
		EventBus: fx.bus,
	})
	if err != nil {
		t.Fatalf("New run service: %v", err)
	}
	wakeSvc := runService.(interface {
		CreateExecutionWakeup(context.Context, executionWakeupInput) (executionWakeupResult, error)
	})

	created, err := fx.tasks.CreateTask(ctx, tasksvc.CreateTaskRequest{
		ProjectID:       fx.project.ID,
		Title:           "Archive retires runtime state",
		AssignedAgentID: &fx.agent.ID,
		CreatedByType:   "system",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	started, err := wakeSvc.CreateExecutionWakeup(ctx, executionWakeupInput{
		CreateRunInput: CreateRunInput{
			OrganizationID: fx.org.ID,
			ProjectID:      &fx.project.ID,
			TaskID:         &created.ID,
			PrincipalType:  "agent",
			PrincipalID:    fx.agent.ID,
			TriggerType:    "scheduler",
			Metadata:       json.RawMessage(`{"run_mode":"async"}`),
		},
		WakeupSource: "task_queue_processor",
		WakeupKind:   "assigned_task",
	})
	if err != nil {
		t.Fatalf("CreateExecutionWakeup: %v", err)
	}

	projectService, err := projectsvc.NewService(projectsvc.Options{
		Pool:         fx.pool,
		Events:       fx.bus,
		ChatSessions: repo.NewChatSessionRepo(fx.pool),
	})
	if err != nil {
		t.Fatalf("New project service: %v", err)
	}
	if _, err := projectService.Archive(ctx, fx.org.ID, fx.project.ID); err != nil {
		t.Fatalf("Archive project: %v", err)
	}

	waitForTaskQueueCondition(t, 10*time.Second, func() (bool, error) {
		state := loadTaskRuntimeState(t, ctx, fx.pool, created.ID)
		return state.Contract().Status == "retired", nil
	})

	state := loadTaskRuntimeState(t, ctx, fx.pool, created.ID)
	contract := state.Contract()
	if contract.Status != "retired" {
		t.Fatalf("runtime status = %q, want retired", contract.Status)
	}
	if contract.RetireReason != "project_archived" {
		t.Fatalf("runtime retire_reason = %q, want project_archived", contract.RetireReason)
	}

	supervisor, err := NewSupervisor(SupervisorOptions{
		Pool:       fx.pool,
		RunService: runService,
		EventBus:   fx.bus,
		Logger:     newDiscardLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := supervisor.recoverRun(ctx, started.Run, "heartbeat silence exceeded"); err != nil {
		t.Fatalf("recoverRun: %v", err)
	}

	var recoveryCount int
	if err := fx.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM run
		WHERE organization_id = $1
		  AND trigger_type = 'supervisor'
		  AND metadata->>'supervisor_recovery_from' = $2
	`, fx.org.ID, started.Run.ID.String()).Scan(&recoveryCount); err != nil {
		t.Fatalf("count supervisor recovery runs: %v", err)
	}
	if recoveryCount != 0 {
		t.Fatalf("supervisor recovery run count = %d, want 0", recoveryCount)
	}
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

func hasTaskQueueKickoffMessage(messages []repo.ChatMessage, taskID uuid.UUID) bool {
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
		if strings.TrimSpace(valueAsString(metadata["task_id"])) != taskID.String() {
			continue
		}
		return true
	}
	return false
}

func publishTaskTurnCompleted(t *testing.T, ctx context.Context, bus *eventbus.Bus, organizationID, sessionID uuid.UUID) {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"session_id": sessionID.String(),
		"turn_id":    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("marshal turn completed payload: %v", err)
	}
	if err := bus.Publish(ctx, nil, eventbus.DomainEvent{
		OrganizationID: organizationID,
		EventType:      "chat.turn.completed",
		ActorType:      "system",
		Payload:        payload,
	}); err != nil {
		t.Fatalf("publish chat.turn.completed: %v", err)
	}
}

type taskQueueProcessorFixture struct {
	pool               *pgxpool.Pool
	bus                *eventbus.Bus
	taskQueuedSub      eventbus.Subscription
	taskCompletedSub   eventbus.Subscription
	runCancellationSub eventbus.Subscription
	flowAdvancedSub    eventbus.Subscription
	processor          *TaskQueueProcessor
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
	queueRuns, ok := runService.(taskQueueRunStarter)
	if !ok {
		t.Fatal("run service does not implement task queue wakeup contract")
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
		Dependencies:   repo.NewProjectTaskDependencyRepo(pool),
		Runs:           queueRuns,
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
	projectArchivedSub := processor.SubscribeProjectArchived(&org.ID)
	turnCompletedSub := processor.SubscribeTurnCompletedWakeups(&org.ID)
	turnCancelledSub := processor.SubscribeTurnCancelledWakeups(&org.ID)
	t.Cleanup(func() {
		bus.Unsubscribe(projectResumedSub)
		bus.Unsubscribe(projectArchivedSub)
		bus.Unsubscribe(turnCompletedSub)
		bus.Unsubscribe(turnCancelledSub)
	})

	return taskQueueProcessorFixture{
		pool:               pool,
		bus:                bus,
		taskQueuedSub:      subscription,
		taskCompletedSub:   taskCompletedSub,
		runCancellationSub: runCancellationSub,
		flowAdvancedSub:    flowAdvancedSub,
		processor:          processor,
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

func loadTaskRuntimeState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID) RuntimeState {
	t.Helper()
	state, err := NewRuntimeStateRepository(pool).GetByScope(ctx, "task", taskID)
	if err != nil {
		t.Fatalf("GetByScope runtime state: %v", err)
	}
	return state
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
	mergeNode, err := nodeRepo.Create(ctx, repo.FlowNode{
		FlowTemplateID: template.ID,
		DisplayName:    "Merge",
		NodeType:       "merge",
		Position:       3,
		MaxVisits:      5,
	})
	if err != nil {
		t.Fatalf("create merge node: %v", err)
	}
	workNode.NextNodeID = &reviewNode.ID
	reviewNode.NextNodeID = &mergeNode.ID
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

func writeTaskQueueRecoveryWorkspaceFiles(t *testing.T, projectSlug, targetPath, artifactPath, targetBody, failureReason string) {
	t.Helper()

	root, err := workspace.ProjectRoot("", projectSlug)
	if err != nil {
		t.Fatalf("workspace root: %v", err)
	}
	targetAbs := filepath.Join(root, filepath.FromSlash(targetPath))
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte(targetBody), 0o644); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	artifactAbs := filepath.Join(root, filepath.FromSlash(artifactPath))
	if err := os.MkdirAll(filepath.Dir(artifactAbs), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	artifactBody := strings.Join([]string{
		"# Recovery file.write artifact",
		"",
		"Task: WS3",
		"Target Path: " + targetPath,
		"Generated: " + time.Now().UTC().Format(time.RFC3339Nano),
		"Reason: Recovery turn halted with a durable file-output checkpoint instead of retrying without a concrete final write.",
		"",
		"## Last Write Failure",
		"",
		failureReason,
		"",
		"## Draft Content",
		"",
		strings.TrimRight(targetBody, "\n"),
	}, "\n")
	if err := os.WriteFile(artifactAbs, []byte(artifactBody), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
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

		turn, err := chatService.CreateTurn(ctx, payload.SessionID, responderID)
		if err != nil {
			return err
		}
		if err := chatService.StartTurn(ctx, turn.ID); err != nil {
			return err
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
		if err := chatService.UpdateMessageStatus(ctx, message.ID, "final", ""); err != nil {
			return err
		}
		return chatService.CompleteTurn(ctx, turn.ID)
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
