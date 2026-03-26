package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
	"github.com/samhotchkiss/otter-camp/internal/taskdecomp"
	"github.com/samhotchkiss/otter-camp/internal/taskorchestration"
	"github.com/samhotchkiss/otter-camp/internal/taskplan"
)

func TestStatusTransitionMatrix(t *testing.T) {
	valid := [][2]string{
		{"draft", "queued"},
		{"draft", "cancelled"},
		{"queued", "in_progress"},
		{"queued", "review"},
		{"queued", "on_hold"},
		{"queued", "cancelled"},
		{"in_progress", "blocked"},
		{"in_progress", "on_hold"},
		{"in_progress", "review"},
		{"in_progress", "done"},
		{"in_progress", "cancelled"},
		{"blocked", "queued"},
		{"blocked", "in_progress"},
		{"blocked", "review"},
		{"blocked", "on_hold"},
		{"blocked", "cancelled"},
		{"on_hold", "queued"},
		{"on_hold", "cancelled"},
		{"review", "blocked"},
		{"review", "done"},
		{"review", "in_progress"},
		{"review", "cancelled"},
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
		{"on_hold", "in_progress"},
		{"on_hold", "done"},
		{"review", "queued"},
		{"in_progress", "draft"},
		{"in_progress", "queued"},
		{"done", "cancelled"},
		{"cancelled", "cancelled"},
	}
	for _, pair := range invalid {
		if isTransitionAllowed(pair[0], pair[1]) {
			t.Fatalf("expected transition %s -> %s to be invalid", pair[0], pair[1])
		}
	}
}

func TestTransitionStatusRejectsReviewMismatchWithCurrentFlowNode(t *testing.T) {
	taskID := uuid.New()
	workNodeID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:                taskID,
				OrganizationID:    uuid.New(),
				ProjectID:         uuid.New(),
				WorkStatus:        "in_progress",
				FlowTemplateID:    &flowTemplateID,
				CurrentFlowNodeID: &workNodeID,
				Title:             "Task",
				CreatedByType:     "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			workNodeID: {ID: workNodeID, NodeType: "work"},
		},
	}

	_, err := svc.TransitionStatus(context.Background(), taskID, "review", Actor{Type: "system"})
	var conflict ErrTaskFlowStateConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("TransitionStatus err = %v, want ErrTaskFlowStateConflict", err)
	}
	if conflict.TargetStatus != "review" || conflict.CurrentNodeType != "work" {
		t.Fatalf("flow conflict = %+v, want target=review current_node_type=work", conflict)
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

func TestRecoveryWorkspaceFileExistsIgnoresDirectoryPath(t *testing.T) {
	root := t.TempDir()
	dirPath := filepath.Join(root, ".ottercamp", "recovery", "test-cases")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir recovery dir: %v", err)
	}

	exists, err := recoveryWorkspaceFileExists([]string{root}, ".ottercamp/recovery/test-cases")
	if err != nil {
		t.Fatalf("recoveryWorkspaceFileExists err = %v, want nil", err)
	}
	if exists {
		t.Fatalf("recoveryWorkspaceFileExists = true, want false for directory path")
	}
}

func TestReadRecoveryWorkspaceFileIgnoresDirectoryPath(t *testing.T) {
	root := t.TempDir()
	dirPath := filepath.Join(root, ".ottercamp", "recovery", "test-cases")
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir recovery dir: %v", err)
	}

	body, exists, err := readRecoveryWorkspaceFile([]string{root}, ".ottercamp/recovery/test-cases")
	if err != nil {
		t.Fatalf("readRecoveryWorkspaceFile err = %v, want nil", err)
	}
	if exists {
		t.Fatalf("readRecoveryWorkspaceFile exists = true, want false for directory path")
	}
	if body != "" {
		t.Fatalf("readRecoveryWorkspaceFile body = %q, want empty", body)
	}
}

func TestTransitionStatusExpectedFromStatusRejectsStaleTransition(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "blocked",
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{Type: "system", ExpectedFromStatus: "queued"})
	if !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("TransitionStatus err = %v, want repo.ErrConflict", err)
	}
}

func TestTransitionStatusAllowsBootstrapGateAutoComplete(t *testing.T) {
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
				BlocksScope:    "all",
				Title:          "Bootstrap governance gate",
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{"bootstrap_gate":true}`),
			},
		},
	}

	svc := newUnitService(taskRepo)
	doneTask, err := svc.TransitionStatusWithPayload(context.Background(), taskID, "done", Actor{
		Type:                           "system",
		AllowBootstrapGateAutoComplete: true,
	}, map[string]any{"bootstrap_gate_auto_complete": true})
	if err != nil {
		t.Fatalf("TransitionStatusWithPayload done: %v", err)
	}
	if doneTask.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", doneTask.WorkStatus)
	}
	if doneTask.CompletedAt == nil {
		t.Fatal("completed_at is nil for bootstrap auto-complete")
	}
}

func TestTransitionStatusRejectsBootstrapAutoCompleteForNonBootstrapTask(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "draft",
				BlocksScope:    "all",
				Title:          "Ordinary gate",
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{"bootstrap_gate":false}`),
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.TransitionStatusWithPayload(context.Background(), taskID, "done", Actor{
		Type:                           "system",
		AllowBootstrapGateAutoComplete: true,
	}, map[string]any{"bootstrap_gate_auto_complete": true})
	var transitionErr ErrInvalidStatusTransition
	if !errors.As(err, &transitionErr) {
		t.Fatalf("TransitionStatusWithPayload err = %v, want ErrInvalidStatusTransition", err)
	}
	if transitionErr.From != "draft" || transitionErr.To != "done" {
		t.Fatalf("transition error = %+v, want from=draft to=done", transitionErr)
	}
}

func TestValidateProjectGateTaskRejectsNonBootstrapGateWithoutExecutionPath(t *testing.T) {
	err := ValidateProjectGateTask(repo.ProjectTask{
		ID:             uuid.New(),
		OrganizationID: uuid.New(),
		ProjectID:      uuid.New(),
		Title:          "Impossible gate",
		BlocksScope:    "all",
		WorkStatus:     "draft",
		CreatedByType:  "system",
	})
	if !errors.Is(err, ErrProjectGateExecutionPathRequired) {
		t.Fatalf("ValidateProjectGateTask err = %v, want ErrProjectGateExecutionPathRequired", err)
	}
}

func TestValidateProjectGateTaskAllowsHumanReviewGateWithoutFlow(t *testing.T) {
	err := ValidateProjectGateTask(repo.ProjectTask{
		ID:                  uuid.New(),
		OrganizationID:      uuid.New(),
		ProjectID:           uuid.New(),
		Title:               "Review gate",
		BlocksScope:         "all",
		WorkStatus:          "draft",
		RequiresHumanReview: true,
		CreatedByType:       "system",
	})
	if err != nil {
		t.Fatalf("ValidateProjectGateTask err = %v, want nil", err)
	}
}

func TestLowestOutstandingProjectGateIgnoresInvalidDraftGateWithoutExecutionPath(t *testing.T) {
	projectID := uuid.New()
	queuedTaskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			uuid.New(): {
				ID:             uuid.New(),
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     38,
				Title:          "Impossible gate",
				WorkStatus:     "draft",
				BlocksScope:    "all",
				CreatedByType:  "system",
			},
			queuedTaskID: {
				ID:             queuedTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     17,
				Title:          "Queued child",
				WorkStatus:     "queued",
				BlocksScope:    "none",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	gateTask, err := svc.lowestOutstandingProjectGate(context.Background(), projectID)
	if err != nil {
		t.Fatalf("lowestOutstandingProjectGate: %v", err)
	}
	if gateTask != nil {
		t.Fatalf("lowestOutstandingProjectGate = %+v, want nil", gateTask)
	}
}

func TestLowestOutstandingProjectGateIgnoresProjectContinuationMetaGate(t *testing.T) {
	projectID := uuid.New()
	realGateID := uuid.New()
	metaGateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			metaGateID: {
				ID:                  metaGateID,
				OrganizationID:      uuid.New(),
				ProjectID:           projectID,
				TaskNumber:          30,
				Title:               "Review and validate pipeline integration test results",
				WorkStatus:          "draft",
				BlocksScope:         "all",
				RequiresHumanReview: true,
				CreatedByType:       "system",
			},
			realGateID: {
				ID:                  realGateID,
				OrganizationID:      uuid.New(),
				ProjectID:           projectID,
				TaskNumber:          31,
				Title:               "Real launch gate",
				WorkStatus:          "draft",
				BlocksScope:         "all",
				RequiresHumanReview: true,
				CreatedByType:       "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	gateTask, err := svc.lowestOutstandingProjectGate(context.Background(), projectID)
	if err != nil {
		t.Fatalf("lowestOutstandingProjectGate: %v", err)
	}
	if gateTask == nil {
		t.Fatal("lowestOutstandingProjectGate = nil, want real gate")
	}
	if gateTask.ID != realGateID {
		t.Fatalf("lowestOutstandingProjectGate = %+v, want real gate %s", gateTask, realGateID)
	}
}

func TestTransitionStatusAllowsCompletedChildReopenToQueued(t *testing.T) {
	taskID := uuid.New()
	parentID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "done",
				FlowTemplateID: &flowTemplateID,
				Title:          "Completed child",
				CreatedByType:  "system",
				Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, 2),
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowTemplates = &fakeFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			flowTemplateID: {ID: flowTemplateID},
		},
	}
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	queuedTask, err := svc.TransitionStatusWithPayload(context.Background(), taskID, "queued", Actor{
		Type:                      "agent",
		ID:                        uuid.New(),
		AllowCompletedChildReopen: true,
	}, map[string]any{"parent_integration_feedback": "Fix integration issue"})
	if err != nil {
		t.Fatalf("TransitionStatusWithPayload queued: %v", err)
	}
	if queuedTask.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", queuedTask.WorkStatus)
	}
	if queuedTask.CompletedAt != nil {
		t.Fatalf("completed_at = %v, want nil after reopen", queuedTask.CompletedAt)
	}
	metadata := taskMetadataMap(queuedTask.Metadata)
	if got := strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback"])); got != "Fix integration issue" {
		t.Fatalf("parent_integration_feedback = %q, want %q", got, "Fix integration issue")
	}
	if strings.TrimSpace(fmt.Sprintf("%v", metadata["parent_integration_feedback_recorded_at"])) == "" {
		t.Fatal("expected parent_integration_feedback_recorded_at")
	}
}

func TestTransitionStatusCompletedChildReopenRequiresParentIntegrationFeedback(t *testing.T) {
	taskID := uuid.New()
	parentID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "done",
				FlowTemplateID: &flowTemplateID,
				Title:          "Completed child",
				CreatedByType:  "system",
				Metadata:       taskdecomp.ApplyChildMetadata(json.RawMessage(`{}`), parentID, 2),
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowTemplates = &fakeFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			flowTemplateID: {ID: flowTemplateID},
		},
	}
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}

	_, err := svc.TransitionStatusWithPayload(context.Background(), taskID, "queued", Actor{
		Type:                      "agent",
		ID:                        uuid.New(),
		AllowCompletedChildReopen: true,
	}, map[string]any{})
	if !errors.Is(err, ErrParentIntegrationFeedbackRequired) {
		t.Fatalf("TransitionStatusWithPayload err = %v, want ErrParentIntegrationFeedbackRequired", err)
	}
}

func TestResumeValidationBlockedTaskRequiresResumableBlockedState(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "blocked",
				Title:          "Blocked task",
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.ResumeValidationBlockedTask(context.Background(), taskID, Actor{Type: "system"})
	var resumeErr TaskResumeBlockedStateError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("ResumeValidationBlockedTask err = %v, want TaskResumeBlockedStateError", err)
	}
	if resumeErr.BlockerClass != RecoveryBlockerClassBlockedWithoutResumableState {
		t.Fatalf("resume blocker_class = %q, want %q", resumeErr.BlockerClass, RecoveryBlockerClassBlockedWithoutResumableState)
	}
}

func TestClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlockerWithoutGuard(t *testing.T) {
	decision := classifyTaskResumeDecision(repo.ProjectTask{
		ID:         uuid.New(),
		WorkStatus: "blocked",
		Metadata:   json.RawMessage(`{}`),
	}, "review turn completed without calling flow.review_decision")

	if !decision.resumable {
		t.Fatal("decision.resumable = false, want true")
	}
	if decision.blockerClass != RecoveryBlockerClassValidationLoop {
		t.Fatalf("blockerClass = %q, want %q", decision.blockerClass, RecoveryBlockerClassValidationLoop)
	}
	if !decision.clearValidationGuard {
		t.Fatal("clearValidationGuard = false, want true")
	}
	if decision.validationGuard == nil {
		t.Fatal("validationGuard = nil, want synthetic guard")
	}
	if decision.validationGuard.ToolName != "flow.review_decision" {
		t.Fatalf("validationGuard.ToolName = %q, want flow.review_decision", decision.validationGuard.ToolName)
	}
	if decision.validationGuard.FailureCode != "review_decision_required" {
		t.Fatalf("validationGuard.FailureCode = %q, want review_decision_required", decision.validationGuard.FailureCode)
	}
}

func TestClassifyTaskResumeDecisionAllowsHistoricalReviewDecisionBlockerWithDetailSuffix(t *testing.T) {
	decision := classifyTaskResumeDecision(repo.ProjectTask{
		ID:         uuid.New(),
		WorkStatus: "blocked",
		Metadata:   json.RawMessage(`{}`),
	}, "review turn completed without calling flow.review_decision: git worktree remove --force /tmp/task-10: exit status 128")

	if !decision.resumable {
		t.Fatal("decision.resumable = false, want true")
	}
	if decision.blockerClass != RecoveryBlockerClassValidationLoop {
		t.Fatalf("blockerClass = %q, want %q", decision.blockerClass, RecoveryBlockerClassValidationLoop)
	}
	if !decision.clearValidationGuard {
		t.Fatal("clearValidationGuard = false, want true")
	}
	if decision.validationGuard == nil {
		t.Fatal("validationGuard = nil, want synthetic guard")
	}
	if decision.validationGuard.ToolName != "flow.review_decision" {
		t.Fatalf("validationGuard.ToolName = %q, want flow.review_decision", decision.validationGuard.ToolName)
	}
}

func TestResumeValidationBlockedTaskRejectsNonBlockedTask(t *testing.T) {
	taskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "in_progress",
				Title:          "Active task",
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{}`),
			},
		},
	}

	svc := newUnitService(taskRepo)
	_, err := svc.ResumeValidationBlockedTask(context.Background(), taskID, Actor{Type: "system"})
	var resumeErr TaskResumeBlockedStateError
	if !errors.As(err, &resumeErr) {
		t.Fatalf("ResumeValidationBlockedTask err = %v, want TaskResumeBlockedStateError", err)
	}
	if resumeErr.BlockerClass != RecoveryBlockerClassNotBlocked {
		t.Fatalf("resume blocker_class = %q, want %q", resumeErr.BlockerClass, RecoveryBlockerClassNotBlocked)
	}
}

func TestClassifyTaskResumeDecisionAllowsFlowRejectionMaxVisitsWithCheckpoint(t *testing.T) {
	metadata, err := taskcheckpoint.MergeRecoveryFileWriteCheckpoint(json.RawMessage(`{}`), taskcheckpoint.RecoveryFileWriteCheckpoint{
		TargetPath:    "Work/report.md",
		ArtifactPath:  ".ottercamp/recovery/Work/report.md",
		FailureReason: "placeholder draft",
		HaltTurnID:    uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("MergeRecoveryFileWriteCheckpoint: %v", err)
	}

	decision := classifyTaskResumeDecision(repo.ProjectTask{
		ID:         uuid.New(),
		WorkStatus: "blocked",
		Metadata:   metadata,
	}, "flow rejection max visits exceeded")

	if !decision.resumable {
		t.Fatal("decision.resumable = false, want true")
	}
	if decision.blockerClass != RecoveryBlockerClassDurableRecoveryCheckpoint {
		t.Fatalf("blockerClass = %q, want %q", decision.blockerClass, RecoveryBlockerClassDurableRecoveryCheckpoint)
	}
	if decision.checkpoint == nil {
		t.Fatal("decision.checkpoint = nil, want durable recovery checkpoint")
	}
	if decision.blockerReason != "flow rejection max visits exceeded" {
		t.Fatalf("blockerReason = %q, want flow rejection max visits exceeded", decision.blockerReason)
	}
}

func TestTransitionStatusCompletedAtBehavior(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "in_progress",
				FlowTemplateID: &flowTemplateID,
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
		FlowTemplateID: &flowTemplateID,
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
		FlowTemplateID: &flowTemplateID,
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

func TestTransitionStatusAllowsSatisfiedDraftAutoComplete(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	description := "Document findings on sourcing channels, qualification criteria, and intake workflows."
	plan := taskplan.Analyze("Document sourcing findings", &description)
	metadata := taskplan.ApplyMetadata(json.RawMessage(`{}`), plan)
	metadata, _, _, err := taskplan.ApplyProcessUpdate(metadata, taskplan.ProcessUpdate{
		HasArtifactChanges: true,
		Artifacts: []taskplan.ArtifactEvidence{
			{Slug: "prd", Summary: "Scope complete.", Sections: []string{"goals", "non-goals", "scope", "constraints", "success metrics", "open questions"}},
		},
	})
	if err != nil {
		t.Fatalf("ApplyProcessUpdate: %v", err)
	}
	metadata, err = taskorchestration.Apply(metadata, taskorchestration.Update{
		OutcomeAssessment: taskorchestration.NewOutcomeAssessment(true, "The task is complete.", time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("taskorchestration.Apply: %v", err)
	}

	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Title:          "Document sourcing findings",
				CreatedByType:  "system",
				Metadata:       metadata,
			},
		},
	}
	svc := newUnitService(taskRepo)

	doneTask, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{
		Type:                            "system",
		AllowDoneBypass:                 true,
		AllowSatisfiedDraftAutoComplete: true,
	})
	if err != nil {
		t.Fatalf("TransitionStatus done: %v", err)
	}
	if doneTask.WorkStatus != "done" {
		t.Fatalf("work_status = %q, want done", doneTask.WorkStatus)
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

func TestTransitionStatusExecutionStatesRequireFlowTemplate(t *testing.T) {
	tests := []struct {
		name       string
		fromStatus string
		toStatus   string
	}{
		{name: "draft to queued", fromStatus: "draft", toStatus: "queued"},
		{name: "queued to in_progress", fromStatus: "queued", toStatus: "in_progress"},
		{name: "in_progress to review", fromStatus: "in_progress", toStatus: "review"},
		{name: "review to done", fromStatus: "review", toStatus: "done"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			taskID := uuid.New()
			taskRepo := &fakeTaskRepo{
				tasks: map[uuid.UUID]repo.ProjectTask{
					taskID: {
						ID:             taskID,
						OrganizationID: uuid.New(),
						ProjectID:      uuid.New(),
						WorkStatus:     tc.fromStatus,
						FlowTemplateID: nil,
						Title:          "Task",
						CreatedByType:  "system",
					},
				},
			}

			svc := newUnitService(taskRepo)
			if _, err := svc.TransitionStatus(context.Background(), taskID, tc.toStatus, Actor{Type: "system", AllowNoActiveFlow: true}); !errors.Is(err, ErrFlowTemplateRequired) {
				t.Fatalf("TransitionStatus %s->%s err = %v, want ErrFlowTemplateRequired", tc.fromStatus, tc.toStatus, err)
			}
		})
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
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	updated, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if updated.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", updated.WorkStatus)
	}
}

func TestTransitionStatusDraftToQueuedBlockedByOutstandingProjectGateEX256(t *testing.T) {
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	gateTaskID := uuid.New()
	regularTaskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			gateTaskID: {
				ID:             gateTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     1,
				Title:          "Bootstrap governance gate",
				WorkStatus:     "draft",
				BlocksScope:    "all",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
			},
			regularTaskID: {
				ID:             regularTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     2,
				Title:          "Regular task",
				WorkStatus:     "draft",
				BlocksScope:    "none",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	_, err := svc.TransitionStatus(context.Background(), regularTaskID, "queued", Actor{Type: "system"})
	if !errors.Is(err, ErrProjectGateBlockingQueue) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrProjectGateBlockingQueue", err)
	}
	if got := err.Error(); !strings.Contains(got, "Bootstrap governance gate") {
		t.Fatalf("error = %q, want gate title context", got)
	}
	if got := err.Error(); !strings.Contains(got, "bootstrap.setup.persist") {
		t.Fatalf("error = %q, want bootstrap setup persist guidance", got)
	}

	stored, getErr := taskRepo.GetByID(context.Background(), regularTaskID)
	if getErr != nil {
		t.Fatalf("GetByID regular task: %v", getErr)
	}
	if stored.WorkStatus != "draft" {
		t.Fatalf("work_status = %q, want draft after blocked queue attempt", stored.WorkStatus)
	}
}

func TestTransitionStatusDraftToQueuedAllowsOutstandingGateTaskEX256(t *testing.T) {
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	gateTaskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			gateTaskID: {
				ID:             gateTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     1,
				Title:          "Bootstrap governance gate",
				WorkStatus:     "draft",
				BlocksScope:    "all",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	updated, err := svc.TransitionStatus(context.Background(), gateTaskID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued gate: %v", err)
	}
	if updated.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", updated.WorkStatus)
	}
}

func TestTransitionStatusDraftToQueuedAllowsSystemGateBypass(t *testing.T) {
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	gateTaskID := uuid.New()
	regularTaskID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			gateTaskID: {
				ID:             gateTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     1,
				Title:          "Bootstrap governance gate",
				WorkStatus:     "draft",
				BlocksScope:    "all",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
			},
			regularTaskID: {
				ID:             regularTaskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				TaskNumber:     2,
				Title:          "First-wave child task",
				WorkStatus:     "draft",
				BlocksScope:    "none",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	updated, err := svc.TransitionStatus(context.Background(), regularTaskID, "queued", Actor{Type: "system", AllowGateBypass: true})
	if err != nil {
		t.Fatalf("TransitionStatus queued with gate bypass: %v", err)
	}
	if updated.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", updated.WorkStatus)
	}
}

func TestTransitionStatusDraftToQueuedWithFlowTemplateRequiresPMWhenProjectConfigured(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	svc.project = &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"requires_pm_assignment_before_queue":true}`),
			},
		},
	}
	if _, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrPMNotAssigned) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrPMNotAssigned", err)
	}
}

func TestTransitionStatusDraftToQueuedWithFlowTemplateAndPMSucceedsWhenProjectConfigured(t *testing.T) {
	taskID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				WorkStatus:     "draft",
				FlowTemplateID: &flowTemplateID,
				Title:          "Task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: validExecutableTemplateNodes(flowTemplateID),
	}
	svc.project = &fakeProjectRepo{
		projects: map[uuid.UUID]repo.Project{
			projectID: {
				ID:       projectID,
				Settings: json.RawMessage(`{"requires_pm_assignment_before_queue":true}`),
			},
		},
	}
	svc.assignments = &fakeAssignmentRepo{
		pmByProject: map[uuid.UUID]repo.AgentProjectAssignment{
			projectID: {ID: uuid.New(), ProjectID: projectID, Role: "pm", IsActive: true},
		},
	}

	updated, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("TransitionStatus queued: %v", err)
	}
	if updated.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued", updated.WorkStatus)
	}
}

func TestTransitionStatusInProgressRejectsDecomposedParentTaskEX332(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	parentMetadata := taskdecomp.ApplyMetadata(
		taskdecomp.ApplyQueueDecompositionMode(json.RawMessage(`{}`), taskdecomp.QueueDecompositionModeParallelChildren),
		taskdecomp.Plan{
			RequiresDecomposition: true,
			PrimaryDeliverable:    "Primary deliverable",
			Deliverables:          []string{"Primary deliverable", "Secondary deliverable"},
		},
		"source description",
		[]uuid.UUID{childID},
	)
	childMetadata := json.RawMessage(`{"decomposition_parent_task_id":"` + parentID.String() + `","workstream_index":2}`)
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			parentID: {
				ID:             parentID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
				Title:          "Parent workstream",
				CreatedByType:  "system",
				Metadata:       parentMetadata,
			},
			childID: {
				ID:             childID,
				OrganizationID: uuid.New(),
				ProjectID:      projectID,
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
				Title:          "Child workstream",
				CreatedByType:  "system",
				Metadata:       childMetadata,
			},
		},
	}

	svc := newUnitService(taskRepo)
	if _, err := svc.TransitionStatus(context.Background(), parentID, "in_progress", Actor{Type: "system", AllowNoActiveFlow: true}); !errors.Is(err, ErrTaskMustRemainOrchestrationOnly) {
		t.Fatalf("TransitionStatus in_progress err = %v, want ErrTaskMustRemainOrchestrationOnly", err)
	}

	stored, getErr := taskRepo.GetByID(context.Background(), parentID)
	if getErr != nil {
		t.Fatalf("GetByID parent task: %v", getErr)
	}
	if stored.WorkStatus != "queued" {
		t.Fatalf("work_status = %q, want queued after rejected execution", stored.WorkStatus)
	}
}

func TestTransitionStatusDraftToQueuedWithFlowTemplateWithoutReviewNodeFails(t *testing.T) {
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
	if _, err := svc.TransitionStatus(context.Background(), taskID, "queued", Actor{Type: "system"}); !errors.Is(err, ErrFlowTemplateReviewRequired) {
		t.Fatalf("TransitionStatus queued err = %v, want ErrFlowTemplateReviewRequired", err)
	}
}

func TestValidateExecutableFlowTemplateReturnsErrorWhenLookupFails(t *testing.T) {
	svc := newUnitService(&fakeTaskRepo{})
	svc.flowNodes = &fakeFlowNodeRepo{getByTemplateErr: errors.New("db unavailable")}

	err := svc.validateExecutableFlowTemplate(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("validateExecutableFlowTemplate err = nil, want error")
	}
}

func TestValidateExecutableFlowTemplateUsesConfiguredStartNodeForRejectLoop(t *testing.T) {
	flowTemplateID := uuid.New()
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
	doneNodeID := uuid.New()

	svc := newUnitService(&fakeTaskRepo{})
	svc.flowTemplates = &fakeFlowTemplateRepo{
		templates: map[uuid.UUID]repo.FlowTemplate{
			flowTemplateID: {
				ID:          flowTemplateID,
				StartNodeID: &workNodeID,
			},
		},
	}
	svc.flowNodes = &fakeFlowNodeRepo{
		nodes: map[uuid.UUID]repo.FlowNode{
			workNodeID: {
				ID:             workNodeID,
				FlowTemplateID: flowTemplateID,
				NodeType:       "work",
				NextNodeID:     &reviewNodeID,
			},
			reviewNodeID: {
				ID:             reviewNodeID,
				FlowTemplateID: flowTemplateID,
				NodeType:       "review",
				NextNodeID:     &doneNodeID,
				RejectNodeID:   &workNodeID,
			},
			doneNodeID: {
				ID:             doneNodeID,
				FlowTemplateID: flowTemplateID,
				NodeType:       "done",
			},
		},
	}

	if err := svc.validateExecutableFlowTemplate(context.Background(), flowTemplateID); err != nil {
		t.Fatalf("validateExecutableFlowTemplate err = %v, want nil", err)
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

func TestTransitionStatusDoneWithoutFlowTemplateReturnsFlowTemplateError(t *testing.T) {
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

	if _, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, ErrFlowTemplateRequired) {
		t.Fatalf("TransitionStatus done err = %v, want ErrFlowTemplateRequired", err)
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
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
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
			workNodeID:   {ID: workNodeID, NodeType: "work", NextNodeID: &reviewNodeID},
			reviewNodeID: {ID: reviewNodeID, NodeType: "review", NextNodeID: &flowNodeID},
			flowNodeID:   {ID: flowNodeID, NodeType: "merge", NextNodeID: nil},
		},
	}
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: workNodeID, Status: "completed"},
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: reviewNodeID, Status: "completed"},
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

func TestTransitionStatusDoneWithoutCompletedReviewStageReturnsError(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	workNodeID := uuid.New()
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
			workNodeID: {ID: workNodeID, NodeType: "work", NextNodeID: &flowNodeID},
			flowNodeID: {ID: flowNodeID, NodeType: "merge", NextNodeID: nil},
		},
	}
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: workNodeID, Status: "completed"},
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "completed"},
			},
		},
	}

	if _, err := svc.TransitionStatus(context.Background(), taskID, "done", Actor{Type: "agent", ID: uuid.New()}); !errors.Is(err, ErrDoneRequiresTerminalFlow) {
		t.Fatalf("TransitionStatus done err = %v, want ErrDoneRequiresTerminalFlow", err)
	}
}

func TestTransitionStatusInProgressWithoutActiveFlowReturnsError(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
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
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
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
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "queued",
				FlowTemplateID: &flowTemplateID,
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

func TestTransitionStatusBlockedToInProgressStillRequiresActiveFlow(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "blocked",
				FlowTemplateID: &flowTemplateID,
				Title:          "Blocked task",
				CreatedByType:  "system",
			},
		},
	}

	svc := newUnitService(taskRepo)

	_, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{
		Type: "human_user",
		ID:   uuid.New(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrActiveFlowRequired) {
		t.Fatalf("TransitionStatus err = %v, want ErrActiveFlowRequired", err)
	}
}

func TestTransitionStatusBlockedToInProgressRejectsAgentEvenWithActiveFlow(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "blocked",
				FlowTemplateID: &flowTemplateID,
				Title:          "Blocked task",
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

	_, err := svc.TransitionStatus(context.Background(), taskID, "in_progress", Actor{Type: "agent", ID: uuid.New()})
	if !errors.Is(err, ErrBlockedInProgressRequiresHumanActor) {
		t.Fatalf("TransitionStatus err = %v, want ErrBlockedInProgressRequiresHumanActor", err)
	}
}

func TestTransitionStatusBlockedToInProgressAllowsHumanUserWithActiveFlow(t *testing.T) {
	taskID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: uuid.New(),
				ProjectID:      uuid.New(),
				WorkStatus:     "blocked",
				FlowTemplateID: &flowTemplateID,
				Title:          "Blocked task",
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
		t.Fatalf("TransitionStatus err = %v, want nil", err)
	}
	if updated.WorkStatus != "in_progress" {
		t.Fatalf("work_status = %q, want in_progress", updated.WorkStatus)
	}
}

func TestMarkBlockedRetriesOnceOnRepoConflict(t *testing.T) {
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
				WorkStatus:     "in_progress",
				Title:          "Task",
				CreatedByType:  "system",
				Metadata:       json.RawMessage(`{}`),
			},
		},
		conflictUpdates: 1,
	}

	svc := newUnitService(taskRepo)
	svc.project = &fakeProjectRepo{projects: map[uuid.UUID]repo.Project{
		projectID: {ID: projectID, OrganizationID: orgID, Slug: "alpha"},
	}}
	svc.assignments = &fakeAssignmentRepo{pmByProject: map[uuid.UUID]repo.AgentProjectAssignment{
		projectID: {ProjectID: projectID, AgentID: pmAgentID, Role: "pm", IsActive: true},
	}}
	svc.agents = &fakeAgentRepo{agents: map[uuid.UUID]repo.Agent{
		pmAgentID: {ID: pmAgentID, OrganizationID: orgID, AgentClass: "staff", CreatedByType: "human_user", CreatedByID: pmUserID},
	}}
	svc.users = &fakeUserRepo{
		usersByID: map[uuid.UUID]repo.HumanUser{
			pmUserID: {ID: pmUserID, OrganizationID: orgID, Role: "admin", IsActive: true},
		},
		usersByOrg: map[uuid.UUID][]repo.HumanUser{
			orgID: {{ID: pmUserID, OrganizationID: orgID, Role: "admin", IsActive: true}},
		},
	}

	blocked, err := svc.MarkBlocked(context.Background(), taskID, "dependency missing", Actor{Type: "system"})
	if err != nil {
		t.Fatalf("MarkBlocked err = %v, want nil", err)
	}
	if blocked.WorkStatus != "blocked" {
		t.Fatalf("work_status = %q, want blocked", blocked.WorkStatus)
	}
	if taskRepo.updateCalls != 2 {
		t.Fatalf("update_calls = %d, want 2", taskRepo.updateCalls)
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

func TestMarkBlockedDoesNotCreateResolutionTaskByDefault(t *testing.T) {
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

	if len(taskRepo.createdTasks) != 0 {
		t.Fatalf("created resolution tasks = %d, want 0", len(taskRepo.createdTasks))
	}
}

func TestMarkBlockedCreatesResolutionTaskWhenPolicyRequiresIt(t *testing.T) {
	now := time.Date(2026, 2, 24, 12, 0, 0, 0, time.UTC)
	taskID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()
	flowTemplateID := uuid.New()
	pmAgentID := uuid.New()
	pmUserID := uuid.New()
	validNodes := validExecutableTemplateNodes(flowTemplateID)
	var startNodeID *uuid.UUID
	for _, node := range validNodes {
		if node.NodeType == "work" {
			id := node.ID
			startNodeID = &id
			break
		}
	}

	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				TaskNumber:     3,
				Title:          "Blocked task",
				WorkStatus:     "in_progress",
				FlowTemplateID: &flowTemplateID,
				CreatedByType:  "system",
				Metadata:       ApplyBlockerAutoResolutionTask(json.RawMessage(`{}`), true),
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
	svc.flowTemplates = &fakeFlowTemplateRepo{templates: map[uuid.UUID]repo.FlowTemplate{
		flowTemplateID: {
			ID:          flowTemplateID,
			StartNodeID: startNodeID,
		},
	}}
	svc.flowNodes = &fakeFlowNodeRepo{nodes: validNodes}

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
	if resolution.WorkStatus != "draft" {
		t.Fatalf("created resolution work_status = %q, want draft", resolution.WorkStatus)
	}
	stored, ok := taskRepo.tasks[resolution.ID]
	if !ok {
		t.Fatal("expected created resolution task to persist in repo")
	}
	if stored.WorkStatus != "queued" {
		t.Fatalf("stored resolution work_status = %q, want queued after canonical transition", stored.WorkStatus)
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

func TestActOnInboxItemTaskReviewApproveAdvancesFlowAndMarksActed(t *testing.T) {
	taskID := uuid.New()
	userID := uuid.New()
	itemID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()
	flowNodeID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				WorkStatus:     "review",
				FlowTemplateID: &flowTemplateID,
				Title:          "Review task",
				CreatedByType:  "system",
			},
		},
	}
	inboxRepo := &fakeInboxRepo{
		items: []repo.InboxItem{
			{
				ID:             itemID,
				OrganizationID: orgID,
				ItemType:       "task_review",
				SourceTaskID:   &taskID,
				ActionPayload:  json.RawMessage(`{"task_id":"` + taskID.String() + `"}`),
			},
		},
	}
	flowActions := &fakeFlowReviewActions{}
	svc := newUnitService(taskRepo)
	svc.inbox = inboxRepo
	svc.flowReviews = flowActions
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "active"},
			},
		},
	}

	if err := svc.ActOnInboxItem(context.Background(), itemID, userID, "approve", nil); err != nil {
		t.Fatalf("ActOnInboxItem approve: %v", err)
	}
	if flowActions.advanceCalls != 1 {
		t.Fatalf("advance calls = %d, want 1", flowActions.advanceCalls)
	}
	if flowActions.rejectCalls != 0 {
		t.Fatalf("reject calls = %d, want 0", flowActions.rejectCalls)
	}
	if flowActions.lastTaskID != taskID {
		t.Fatalf("advance task_id = %s, want %s", flowActions.lastTaskID, taskID)
	}
	if flowActions.lastActor.Type != "human_user" || flowActions.lastActor.ID != userID {
		t.Fatalf("advance actor = %+v, want human_user/%s", flowActions.lastActor, userID)
	}
	acted, err := inboxRepo.GetByID(context.Background(), itemID)
	if err != nil {
		t.Fatalf("GetByID inbox item: %v", err)
	}
	if !acted.IsActed {
		t.Fatal("inbox item is_acted = false, want true")
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review without direct service-layer status mutation", updatedTask.WorkStatus)
	}
}

func TestActOnInboxItemTaskReviewRejectCallsRejectWithReasonAndMarksActed(t *testing.T) {
	taskID := uuid.New()
	userID := uuid.New()
	itemID := uuid.New()
	orgID := uuid.New()
	projectID := uuid.New()
	flowNodeID := uuid.New()
	flowTemplateID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      projectID,
				WorkStatus:     "review",
				FlowTemplateID: &flowTemplateID,
				Title:          "Review task",
				CreatedByType:  "system",
			},
		},
	}
	inboxRepo := &fakeInboxRepo{
		items: []repo.InboxItem{
			{
				ID:             itemID,
				OrganizationID: orgID,
				ItemType:       "task_review",
				SourceTaskID:   &taskID,
				ActionPayload:  json.RawMessage(`{"task_id":"` + taskID.String() + `"}`),
			},
		},
	}
	flowActions := &fakeFlowReviewActions{}
	eventRepo := &fakeTaskEventRepo{}
	eventBus := &fakeEventBus{}
	svc := newUnitService(taskRepo)
	svc.inbox = inboxRepo
	svc.events = eventRepo
	svc.eventBus = eventBus
	svc.flowReviews = flowActions
	svc.executions = &fakeFlowExecutionRepo{
		byTask: map[uuid.UUID][]repo.FlowNodeExecution{
			taskID: {
				{ID: uuid.New(), TaskID: taskID, FlowNodeID: flowNodeID, Status: "active"},
			},
		},
	}

	if err := svc.ActOnInboxItem(context.Background(), itemID, userID, "reject", json.RawMessage(`{"reason":"needs more detail"}`)); err != nil {
		t.Fatalf("ActOnInboxItem reject: %v", err)
	}
	if flowActions.rejectCalls != 1 {
		t.Fatalf("reject calls = %d, want 1", flowActions.rejectCalls)
	}
	if flowActions.advanceCalls != 0 {
		t.Fatalf("advance calls = %d, want 0", flowActions.advanceCalls)
	}
	if flowActions.lastTaskID != taskID {
		t.Fatalf("reject task_id = %s, want %s", flowActions.lastTaskID, taskID)
	}
	if flowActions.lastReason != "needs more detail" {
		t.Fatalf("reject reason = %q, want %q", flowActions.lastReason, "needs more detail")
	}
	if flowActions.lastActor.Type != "human_user" || flowActions.lastActor.ID != userID {
		t.Fatalf("reject actor = %+v, want human_user/%s", flowActions.lastActor, userID)
	}
	acted, err := inboxRepo.GetByID(context.Background(), itemID)
	if err != nil {
		t.Fatalf("GetByID inbox item: %v", err)
	}
	if !acted.IsActed {
		t.Fatal("inbox item is_acted = false, want true")
	}
	updatedTask, err := taskRepo.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID task: %v", err)
	}
	if updatedTask.WorkStatus != "review" {
		t.Fatalf("task work_status = %q, want review without direct service-layer status mutation", updatedTask.WorkStatus)
	}

	foundReviewRejectedEvent := false
	for _, event := range eventRepo.events {
		if event.EventType == "task.review_rejected" {
			foundReviewRejectedEvent = true
			break
		}
	}
	if !foundReviewRejectedEvent {
		t.Fatal("expected task.review_rejected event to be recorded")
	}
	if len(eventBus.events) == 0 {
		t.Fatal("expected task.review_rejected domain event to be published")
	}
}

func TestActOnInboxItemTaskReviewDismissMarksActedOnly(t *testing.T) {
	taskID := uuid.New()
	userID := uuid.New()
	itemID := uuid.New()
	orgID := uuid.New()
	taskRepo := &fakeTaskRepo{
		tasks: map[uuid.UUID]repo.ProjectTask{
			taskID: {
				ID:             taskID,
				OrganizationID: orgID,
				ProjectID:      uuid.New(),
				WorkStatus:     "review",
				Title:          "Review task",
				CreatedByType:  "system",
			},
		},
	}
	inboxRepo := &fakeInboxRepo{
		items: []repo.InboxItem{
			{
				ID:             itemID,
				OrganizationID: orgID,
				ItemType:       "task_review",
				SourceTaskID:   &taskID,
				ActionPayload:  json.RawMessage(`{"task_id":"` + taskID.String() + `"}`),
			},
		},
	}
	flowActions := &fakeFlowReviewActions{}
	svc := newUnitService(taskRepo)
	svc.inbox = inboxRepo
	svc.flowReviews = flowActions

	if err := svc.ActOnInboxItem(context.Background(), itemID, userID, "dismiss", nil); err != nil {
		t.Fatalf("ActOnInboxItem dismiss: %v", err)
	}
	if flowActions.advanceCalls != 0 || flowActions.rejectCalls != 0 {
		t.Fatalf("flow calls advance=%d reject=%d, want 0/0", flowActions.advanceCalls, flowActions.rejectCalls)
	}
	acted, err := inboxRepo.GetByID(context.Background(), itemID)
	if err != nil {
		t.Fatalf("GetByID inbox item: %v", err)
	}
	if !acted.IsActed {
		t.Fatal("inbox item is_acted = false, want true")
	}
}

func newUnitService(taskRepo *fakeTaskRepo) *service {
	return &service{
		tasks:         taskRepo,
		events:        &fakeTaskEventRepo{},
		inbox:         &fakeInboxRepo{},
		queue:         &fakeQueueRepo{},
		project:       &fakeProjectRepo{projects: make(map[uuid.UUID]repo.Project)},
		assignments:   &fakeAssignmentRepo{},
		agents:        &fakeAgentRepo{},
		users:         &fakeUserRepo{},
		executions:    &fakeFlowExecutionRepo{},
		flowNodes:     &fakeFlowNodeRepo{nodes: map[uuid.UUID]repo.FlowNode{}},
		flowTemplates: &fakeFlowTemplateRepo{},
		eventBus:      &fakeEventBus{},
		clock:         clock.NewFake(time.Now().UTC()),
	}
}

type fakeTaskRepo struct {
	tasks           map[uuid.UUID]repo.ProjectTask
	createdTasks    []repo.ProjectTask
	conflictUpdates int
	updateCalls     int
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

func (f *fakeTaskRepo) ListByProject(_ context.Context, projectID uuid.UUID, statuses ...string) ([]repo.ProjectTask, error) {
	items := make([]repo.ProjectTask, 0)
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		normalized := strings.ToLower(strings.TrimSpace(status))
		if normalized == "" {
			continue
		}
		allowed[normalized] = struct{}{}
	}
	for _, task := range f.tasks {
		if task.ProjectID != projectID {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(strings.TrimSpace(task.WorkStatus))]; !ok {
				continue
			}
		}
		items = append(items, task)
	}
	return items, nil
}

func (f *fakeTaskRepo) Update(_ context.Context, task repo.ProjectTask) (repo.ProjectTask, error) {
	if _, ok := f.tasks[task.ID]; !ok {
		return repo.ProjectTask{}, repo.ErrNotFound
	}
	f.updateCalls++
	if f.conflictUpdates > 0 {
		f.conflictUpdates--
		return repo.ProjectTask{}, repo.ErrConflict
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
	nodes            map[uuid.UUID]repo.FlowNode
	getByTemplateErr error
}

type fakeFlowTemplateRepo struct {
	templates  map[uuid.UUID]repo.FlowTemplate
	current    []repo.FlowTemplate
	getByIDErr error
}

func validExecutableTemplateNodes(flowTemplateID uuid.UUID) map[uuid.UUID]repo.FlowNode {
	workNodeID := uuid.New()
	reviewNodeID := uuid.New()
	mergeNodeID := uuid.New()
	return map[uuid.UUID]repo.FlowNode{
		workNodeID: {
			ID:             workNodeID,
			FlowTemplateID: flowTemplateID,
			NodeType:       "work",
			NextNodeID:     &reviewNodeID,
		},
		reviewNodeID: {
			ID:             reviewNodeID,
			FlowTemplateID: flowTemplateID,
			NodeType:       "review",
			NextNodeID:     &mergeNodeID,
		},
		mergeNodeID: {
			ID:             mergeNodeID,
			FlowTemplateID: flowTemplateID,
			NodeType:       "merge",
		},
	}
}

type fakeFlowReviewActions struct {
	advanceCalls int
	rejectCalls  int
	lastTaskID   uuid.UUID
	lastActor    Actor
	lastReason   string
}

func (f *fakeFlowReviewActions) AdvanceTaskReview(_ context.Context, taskID uuid.UUID, actor Actor) error {
	f.advanceCalls++
	f.lastTaskID = taskID
	f.lastActor = actor
	return nil
}

func (f *fakeFlowReviewActions) RejectTaskReview(_ context.Context, taskID uuid.UUID, actor Actor, reason string) error {
	f.rejectCalls++
	f.lastTaskID = taskID
	f.lastActor = actor
	f.lastReason = strings.TrimSpace(reason)
	return nil
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

func (f *fakeFlowNodeRepo) GetByTemplateOrdered(_ context.Context, flowTemplateID uuid.UUID) ([]repo.FlowNode, error) {
	if f.getByTemplateErr != nil {
		return nil, f.getByTemplateErr
	}
	if f.nodes == nil {
		return []repo.FlowNode{}, nil
	}
	out := make([]repo.FlowNode, 0)
	for _, node := range f.nodes {
		if node.FlowTemplateID == flowTemplateID {
			out = append(out, node)
		}
	}
	return out, nil
}

func (f *fakeFlowTemplateRepo) GetByID(_ context.Context, id uuid.UUID) (repo.FlowTemplate, error) {
	if f.getByIDErr != nil {
		return repo.FlowTemplate{}, f.getByIDErr
	}
	if f.templates == nil {
		return repo.FlowTemplate{ID: id}, nil
	}
	if item, ok := f.templates[id]; ok {
		return item, nil
	}
	return repo.FlowTemplate{ID: id}, nil
}

func (f *fakeFlowTemplateRepo) ListCurrent(_ context.Context, _ *uuid.UUID, _ *uuid.UUID) ([]repo.FlowTemplate, error) {
	if len(f.current) == 0 {
		return nil, nil
	}
	return append([]repo.FlowTemplate(nil), f.current...), nil
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
