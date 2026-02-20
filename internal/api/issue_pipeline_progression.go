package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type IssuePipelineProgressionService struct {
	PipelineStepStore *store.PipelineStepStore
	IssueStore        *store.ProjectIssueStore
	Now               func() time.Time
}

type IssuePipelineProgressionResult struct {
	IssueID               string  `json:"issue_id"`
	CompletedStepID       string  `json:"completed_step_id"`
	CurrentPipelineStepID *string `json:"current_pipeline_step_id,omitempty"`
	CompletedPipeline     bool    `json:"completed_pipeline"`
	ParkedForHumanReview  bool    `json:"parked_for_human_review"`
}

func (s *IssuePipelineProgressionService) CompleteCurrentStep(
	ctx context.Context,
	issueID string,
	agentID *string,
	notes string,
) (*IssuePipelineProgressionResult, error) {
	if s.PipelineStepStore == nil {
		return nil, fmt.Errorf("%w: pipeline step store unavailable", store.ErrValidation)
	}

	now := s.nowUTC()
	state, steps, currentStep, currentIndex, err := s.loadCurrentStep(ctx, issueID, true)
	if err != nil {
		return nil, err
	}

	historyAgentID := normalizePipelineHistoryAgentID(agentID, currentStep.AssignedAgentID)
	if _, err := s.PipelineStepStore.AppendIssuePipelineHistory(ctx, store.CreateIssuePipelineHistoryInput{
		IssueID:     state.IssueID,
		StepID:      currentStep.ID,
		AgentID:     historyAgentID,
		StartedAt:   now,
		CompletedAt: &now,
		Result:      store.IssuePipelineResultCompleted,
		Notes:       strings.TrimSpace(notes),
	}); err != nil {
		return nil, err
	}

	pipelineStartedAt := state.PipelineStartedAt
	if pipelineStartedAt == nil {
		pipelineStartedAt = &now
	}

	result := &IssuePipelineProgressionResult{
		IssueID:         state.IssueID,
		CompletedStepID: currentStep.ID,
	}

	nextIndex := currentIndex + 1
	hasNext := nextIndex >= 0 && nextIndex < len(steps)

	switch {
	case hasNext && currentStep.StepType != store.PipelineStepTypeHumanReview && currentStep.AutoAdvance:
		nextStep := steps[nextIndex]
		nextStepID := nextStep.ID
		if err := s.PipelineStepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               state.IssueID,
			CurrentPipelineStepID: &nextStepID,
			PipelineStartedAt:     pipelineStartedAt,
			PipelineCompletedAt:   nil,
		}); err != nil {
			return nil, err
		}
		result.CurrentPipelineStepID = &nextStepID
		if nextStep.StepType == store.PipelineStepTypeHumanReview {
			result.ParkedForHumanReview = true
		}
		if err := s.updateIssueWorkStatus(ctx, state.IssueID, nextStepWorkStatus(nextStep)); err != nil {
			return nil, err
		}
	case hasNext:
		currentStepID := currentStep.ID
		if err := s.PipelineStepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               state.IssueID,
			CurrentPipelineStepID: &currentStepID,
			PipelineStartedAt:     pipelineStartedAt,
			PipelineCompletedAt:   nil,
		}); err != nil {
			return nil, err
		}
		result.CurrentPipelineStepID = &currentStepID
		if currentStep.StepType == store.PipelineStepTypeHumanReview {
			result.ParkedForHumanReview = true
			if err := s.updateIssueWorkStatus(ctx, state.IssueID, store.IssueWorkStatusReview); err != nil {
				return nil, err
			}
		}
	default:
		completedAt := now
		if err := s.PipelineStepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
			IssueID:               state.IssueID,
			CurrentPipelineStepID: nil,
			PipelineStartedAt:     pipelineStartedAt,
			PipelineCompletedAt:   &completedAt,
		}); err != nil {
			return nil, err
		}
		result.CompletedPipeline = true
		if err := s.updateIssueWorkStatus(ctx, state.IssueID, store.IssueWorkStatusDone); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (s *IssuePipelineProgressionService) RejectCurrentStep(
	ctx context.Context,
	issueID string,
	agentID *string,
	reason string,
) (*IssuePipelineProgressionResult, error) {
	if s.PipelineStepStore == nil {
		return nil, fmt.Errorf("%w: pipeline step store unavailable", store.ErrValidation)
	}

	now := s.nowUTC()
	state, steps, currentStep, currentIndex, err := s.loadCurrentStep(ctx, issueID, false)
	if err != nil {
		return nil, err
	}
	if currentIndex <= 0 {
		return nil, fmt.Errorf("%w: no previous step available", store.ErrConflict)
	}

	previous := previousActionableStep(steps, currentIndex)
	if previous == nil {
		return nil, fmt.Errorf("%w: no previous actionable step available", store.ErrConflict)
	}

	historyAgentID := normalizePipelineHistoryAgentID(agentID, currentStep.AssignedAgentID)
	if _, err := s.PipelineStepStore.AppendIssuePipelineHistory(ctx, store.CreateIssuePipelineHistoryInput{
		IssueID:     state.IssueID,
		StepID:      currentStep.ID,
		AgentID:     historyAgentID,
		StartedAt:   now,
		CompletedAt: &now,
		Result:      store.IssuePipelineResultRejected,
		Notes:       strings.TrimSpace(reason),
	}); err != nil {
		return nil, err
	}

	pipelineStartedAt := state.PipelineStartedAt
	if pipelineStartedAt == nil {
		pipelineStartedAt = &now
	}
	previousID := previous.ID
	if err := s.PipelineStepStore.UpdateIssuePipelineState(ctx, store.UpdateIssuePipelineStateInput{
		IssueID:               state.IssueID,
		CurrentPipelineStepID: &previousID,
		PipelineStartedAt:     pipelineStartedAt,
		PipelineCompletedAt:   nil,
	}); err != nil {
		return nil, err
	}
	if err := s.updateIssueWorkStatus(ctx, state.IssueID, store.IssueWorkStatusInProgress); err != nil {
		return nil, err
	}

	return &IssuePipelineProgressionResult{
		IssueID:               state.IssueID,
		CompletedStepID:       currentStep.ID,
		CurrentPipelineStepID: &previousID,
	}, nil
}

func (s *IssuePipelineProgressionService) loadCurrentStep(
	ctx context.Context,
	issueID string,
	allowImplicitStart bool,
) (*store.IssuePipelineState, []store.PipelineStep, store.PipelineStep, int, error) {
	state, err := s.PipelineStepStore.GetIssuePipelineState(ctx, issueID)
	if err != nil {
		return nil, nil, store.PipelineStep{}, -1, err
	}
	steps, err := s.PipelineStepStore.ListStepsByProject(ctx, state.ProjectID)
	if err != nil {
		return nil, nil, store.PipelineStep{}, -1, err
	}
	if len(steps) == 0 {
		return nil, nil, store.PipelineStep{}, -1, fmt.Errorf("%w: project has no pipeline steps", store.ErrValidation)
	}

	if state.CurrentPipelineStepID == nil {
		if !allowImplicitStart {
			return nil, nil, store.PipelineStep{}, -1, fmt.Errorf("%w: issue has no current pipeline step", store.ErrValidation)
		}
		return state, steps, steps[0], 0, nil
	}

	for idx, step := range steps {
		if step.ID == *state.CurrentPipelineStepID {
			return state, steps, step, idx, nil
		}
	}

	return nil, nil, store.PipelineStep{}, -1, fmt.Errorf("%w: issue references unknown pipeline step", store.ErrConflict)
}

func (s *IssuePipelineProgressionService) updateIssueWorkStatus(ctx context.Context, issueID, status string) error {
	if s.IssueStore == nil {
		return nil
	}
	_, err := s.IssueStore.UpdateIssueWorkTracking(ctx, store.UpdateProjectIssueWorkTrackingInput{
		IssueID:       issueID,
		SetWorkStatus: true,
		WorkStatus:    status,
	})
	return err
}

func (s *IssuePipelineProgressionService) nowUTC() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func nextStepWorkStatus(step store.PipelineStep) string {
	if step.StepType == store.PipelineStepTypeHumanReview {
		return store.IssueWorkStatusReview
	}
	return store.IssueWorkStatusInProgress
}

func previousActionableStep(steps []store.PipelineStep, currentIndex int) *store.PipelineStep {
	for idx := currentIndex - 1; idx >= 0; idx-- {
		if steps[idx].StepType != store.PipelineStepTypeHumanReview {
			return &steps[idx]
		}
	}
	return nil
}

func normalizePipelineHistoryAgentID(explicitAgentID, stepAgentID *string) *string {
	if explicitAgentID != nil {
		trimmed := strings.TrimSpace(*explicitAgentID)
		if trimmed == "" {
			return nil
		}
		return &trimmed
	}
	if stepAgentID == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*stepAgentID)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
