package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/clock"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

const (
	testModeToolPrefixFileWrite = "[tool-call:file.write]"
	testModeToolPrefixNetFetch  = "[tool-call:network.fetch]"
	testModeSlowRunPrefix       = "[slow-run]"
)

type TestModeRunServiceOptions struct {
	Base            RunService
	Pool            *pgxpool.Pool
	PolicyEvaluator *policy.PolicyEvaluator
	Logger          *slog.Logger
	Clock           clock.Clock
	Sleep           func(context.Context, time.Duration) error
}

type testModeRunService struct {
	RunService

	tasks       *repo.ProjectTaskRepo
	runAttempts *RunAttemptRepository
	toolExecs   *ToolExecutionRepository
	audits      *repo.AuditEventRepo
	policy      *policy.PolicyEvaluator
	logger      *slog.Logger
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

func NewTestModeRunService(opts TestModeRunServiceOptions) (RunService, error) {
	if opts.Base == nil {
		return nil, errors.New("test mode run service requires base service")
	}
	if opts.Pool == nil {
		return nil, errors.New("test mode run service requires database pool")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	nowFn := time.Now
	if opts.Clock != nil {
		nowFn = opts.Clock.Now
	}
	sleepFn := opts.Sleep
	if sleepFn == nil {
		sleepFn = sleepWithContext
	}
	return &testModeRunService{
		RunService:  opts.Base,
		tasks:       repo.NewProjectTaskRepo(opts.Pool),
		runAttempts: NewRunAttemptRepository(opts.Pool),
		toolExecs:   NewToolExecutionRepository(opts.Pool),
		audits:      repo.NewAuditEventRepo(opts.Pool),
		policy:      opts.PolicyEvaluator,
		logger:      logger,
		now:         nowFn,
		sleep:       sleepFn,
	}, nil
}

func (s *testModeRunService) CreateRun(ctx context.Context, input CreateRunInput) (Run, error) {
	runRecord, err := s.RunService.CreateRun(ctx, input)
	if err != nil {
		return runRecord, err
	}
	if runRecord.TaskID == nil {
		return runRecord, nil
	}

	runID := runRecord.ID
	go s.simulateTaskRun(runID)
	return runRecord, nil
}

func (s *testModeRunService) RequestCancel(ctx context.Context, runID uuid.UUID, requestedBy CancelRequestActor) error {
	if err := s.RunService.RequestCancel(ctx, runID, requestedBy); err != nil {
		return err
	}

	go s.autoConfirmCancellation(runID)
	return nil
}

func (s *testModeRunService) simulateTaskRun(runID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	runRecord, err := s.RunService.GetRun(ctx, runID)
	if err != nil {
		s.logger.Warn("test mode run simulation get run failed", "run_id", runID, "error", err)
		return
	}
	if runRecord.TaskID == nil {
		return
	}
	taskRecord, err := s.tasks.GetByID(ctx, *runRecord.TaskID)
	if err != nil {
		s.logger.Warn("test mode run simulation get task failed", "run_id", runID, "task_id", *runRecord.TaskID, "error", err)
		return
	}

	if err := s.RunService.StartRun(ctx, runRecord.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		s.logger.Warn("test mode run simulation start run failed", "run_id", runID, "error", err)
		return
	}

	title := strings.ToLower(strings.TrimSpace(taskRecord.Title))
	switch {
	case strings.HasPrefix(title, testModeToolPrefixFileWrite):
		s.simulateTier2ToolCall(ctx, runRecord, taskRecord, "file.write", "system.file.write")
	case strings.HasPrefix(title, testModeToolPrefixNetFetch):
		s.simulateTier2ToolCall(ctx, runRecord, taskRecord, "network.fetch", "system.network.outbound")
	case strings.HasPrefix(title, testModeSlowRunPrefix):
		if _, err := s.RunService.CreateStep(ctx, runRecord.ID, "slow-run", "tier1"); err != nil {
			s.logger.Warn("test mode slow run create step failed", "run_id", runRecord.ID, "error", err)
		}
	default:
		if _, err := s.RunService.CreateStep(ctx, runRecord.ID, "task.start", "tier1"); err != nil {
			s.logger.Warn("test mode lifecycle create step failed", "run_id", runRecord.ID, "error", err)
		}
	}
}

func (s *testModeRunService) simulateTier2ToolCall(ctx context.Context, runRecord Run, taskRecord repo.ProjectTask, toolName, capability string) {
	step, err := s.RunService.CreateStep(ctx, runRecord.ID, toolName, "tier2")
	if err != nil {
		s.logger.Warn("test mode tool simulation create step failed", "run_id", runRecord.ID, "tool", toolName, "error", err)
		return
	}
	if err := s.RunService.StartStep(ctx, step.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		s.logger.Warn("test mode tool simulation start step failed", "run_id", runRecord.ID, "step_id", step.ID, "error", err)
		return
	}

	workerType := "internal"
	attempt, err := s.runAttempts.Create(ctx, RunAttempt{
		RunStepID:     step.ID,
		AttemptNumber: 1,
		Trigger:       "initial",
		Status:        "completed",
		WorkerType:    &workerType,
		Metadata:      json.RawMessage(`{"test_mode":true}`),
	})
	if err != nil {
		s.logger.Warn("test mode tool simulation create attempt failed", "run_id", runRecord.ID, "step_id", step.ID, "error", err)
		return
	}

	allowed, reason := s.policyDecision(ctx, runRecord.OrganizationID, taskRecord.ProjectID, taskRecord.AssignedAgentID, capability)
	policyDecision := "allowed"
	toolStatus := "completed"
	auditEventType := "capability_allowed"
	auditDecision := "allow"
	var output json.RawMessage
	if allowed {
		output = json.RawMessage(`{"ok":true,"test_mode":true}`)
	} else {
		policyDecision = "denied"
		toolStatus = "policy_denied"
		auditEventType = "capability_denied"
		auditDecision = "deny"
	}

	now := s.now().UTC()
	capabilityCopy := capability
	metadata, _ := json.Marshal(map[string]any{
		"reason":    reason,
		"test_mode": true,
	})
	_, err = s.toolExecs.Create(ctx, ToolExecution{
		RunID:          &runRecord.ID,
		RunStepID:      &step.ID,
		RunAttemptID:   &attempt.ID,
		ToolName:       toolName,
		ToolTier:       "tier2",
		ToolDomain:     "native",
		Capability:     &capabilityCopy,
		PolicyDecision: policyDecision,
		Input:          json.RawMessage(`{"test_mode":true}`),
		Output:         output,
		Status:         toolStatus,
		StartedAt:      &now,
		CompletedAt:    &now,
		Metadata:       metadata,
	})
	if err != nil {
		s.logger.Warn("test mode tool simulation create execution failed", "run_id", runRecord.ID, "tool", toolName, "error", err)
		return
	}

	if err := s.RunService.CompleteStep(ctx, step.ID); err != nil && !errors.Is(err, ErrInvalidTransition) {
		s.logger.Warn("test mode tool simulation complete step failed", "run_id", runRecord.ID, "step_id", step.ID, "error", err)
	}

	targetType := "run"
	runID := runRecord.ID
	if err := s.audits.Insert(ctx, repo.AuditEvent{
		OrganizationID: runRecord.OrganizationID,
		EventType:      auditEventType,
		PrincipalType:  "system",
		PrincipalID:    uuid.Nil,
		TargetType:     &targetType,
		TargetID:       &runID,
		Metadata: map[string]any{
			"capability": capability,
			"decision":   auditDecision,
			"tool_name":  toolName,
			"reason":     reason,
		},
	}); err != nil {
		s.logger.Warn("test mode tool simulation audit insert failed", "run_id", runRecord.ID, "event_type", auditEventType, "error", err)
	}
}

func (s *testModeRunService) policyDecision(ctx context.Context, orgID, projectID uuid.UUID, agentID *uuid.UUID, capability string) (bool, string) {
	if s.policy == nil {
		return true, "policy evaluator unavailable"
	}
	var projectIDPtr *uuid.UUID
	if projectID != uuid.Nil {
		projectIDCopy := projectID
		projectIDPtr = &projectIDCopy
	}
	decision := s.policy.Evaluate(ctx, policy.EvaluationRequest{
		OrganizationID: orgID,
		ProjectID:      projectIDPtr,
		AgentID:        agentID,
		Capability:     capability,
	})
	if strings.EqualFold(strings.TrimSpace(decision.Effect), "deny") {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "policy denied"
		}
		return false, reason
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "allowed"
	}
	return true, reason
}

func (s *testModeRunService) autoConfirmCancellation(runID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	deadline := s.now().Add(3 * time.Second)
	for s.now().Before(deadline) {
		runRecord, err := s.RunService.GetRun(ctx, runID)
		if err != nil {
			s.logger.Warn("test mode cancel confirm get run failed", "run_id", runID, "error", err)
			return
		}
		if runRecord.Status == "cancelled" {
			return
		}
		if runRecord.Status == "cancelling" {
			if err := s.RunService.ConfirmCancelled(ctx, runID); err != nil && !errors.Is(err, ErrInvalidTransition) {
				s.logger.Warn("test mode cancel confirm failed", "run_id", runID, "error", err)
			}
			return
		}
		if _, terminal := runTerminalStatuses[runRecord.Status]; terminal {
			return
		}
		if err := s.sleep(ctx, 100*time.Millisecond); err != nil {
			return
		}
	}
}
