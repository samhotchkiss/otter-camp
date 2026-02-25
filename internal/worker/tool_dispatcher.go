package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/controlplane"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/turn"
)

type brokerDispatcher interface {
	Dispatch(ctx context.Context, input controlplane.DispatchInput) (controlplane.ToolExecution, error)
}

type runLifecycleService interface {
	CreateRun(ctx context.Context, input controlplane.CreateRunInput) (controlplane.Run, error)
	StartRun(ctx context.Context, runID uuid.UUID) error
	CompleteRun(ctx context.Context, runID uuid.UUID, output json.RawMessage) error
	FailRun(ctx context.Context, runID uuid.UUID, reason, failureClass string) error
	CreateStep(ctx context.Context, runID uuid.UUID, toolName, toolTier string) (controlplane.RunStep, error)
	StartStep(ctx context.Context, stepID uuid.UUID) error
	CompleteStep(ctx context.Context, stepID uuid.UUID) error
	FailStep(ctx context.Context, stepID uuid.UUID, reason string) error
}

type agentLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type liveToolDispatcher struct {
	broker brokerDispatcher
	runs   runLifecycleService
	agents agentLookup
}

func newLiveToolDispatcher(broker brokerDispatcher, runs runLifecycleService, agents agentLookup) (*liveToolDispatcher, error) {
	if broker == nil {
		return nil, fmt.Errorf("tool broker is required")
	}
	if runs == nil {
		return nil, fmt.Errorf("run service is required")
	}
	if agents == nil {
		return nil, fmt.Errorf("agent repository is required")
	}
	return &liveToolDispatcher{
		broker: broker,
		runs:   runs,
		agents: agents,
	}, nil
}

func (d *liveToolDispatcher) DispatchTier1(ctx context.Context, call turn.ToolCall) (turn.ToolResult, error) {
	result := turn.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
	}

	input := cloneToolInput(call)
	agentID, err := requiredUUID(input, "agent_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	sessionID, err := optionalUUID(input, "session_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	exec, dispatchErr := d.broker.Dispatch(ctx, controlplane.DispatchInput{
		SessionID: sessionID,
		AgentID:   agentID,
		ToolName:  strings.TrimSpace(call.Name),
		ToolTier:  "tier1",
		Input:     input,
	})
	if dispatchErr != nil {
		result.Error = dispatchErr.Error()
		return result, nil
	}

	output, err := decodeOutput(exec.Output)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Output = output
	return result, nil
}

func (d *liveToolDispatcher) DispatchTier2(ctx context.Context, call turn.ToolCall, onRunStarted func(runID uuid.UUID)) (turn.ToolResult, error) {
	result := turn.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
	}

	input := cloneToolInput(call)
	agentID, err := requiredUUID(input, "agent_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	orgID, err := d.resolveOrganizationID(ctx, agentID, input)
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	projectID, err := optionalUUID(input, "project_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	taskID, err := optionalUUID(input, "task_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	sessionID, err := optionalUUID(input, "session_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	turnID, err := optionalUUID(input, "turn_id")
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	metadata, _ := json.Marshal(map[string]any{
		"tool_name":    strings.TrimSpace(call.Name),
		"tool_call_id": strings.TrimSpace(call.ID),
	})

	runRecord, err := d.runs.CreateRun(ctx, controlplane.CreateRunInput{
		OrganizationID: orgID,
		PrincipalType:  "agent",
		PrincipalID:    agentID,
		TriggerType:    "agent_tool",
		ProjectID:      projectID,
		TaskID:         taskID,
		SessionID:      sessionID,
		TurnID:         turnID,
		Metadata:       metadata,
	})
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}

	result.RunID = &runRecord.ID
	if onRunStarted != nil {
		onRunStarted(runRecord.ID)
	}

	if err := d.runs.StartRun(ctx, runRecord.ID); err != nil {
		result.Error = err.Error()
		return result, nil
	}

	step, err := d.runs.CreateStep(ctx, runRecord.ID, call.Name, "tier2")
	if err != nil {
		_ = d.runs.FailRun(ctx, runRecord.ID, err.Error(), "transient")
		result.Error = err.Error()
		return result, nil
	}
	if err := d.runs.StartStep(ctx, step.ID); err != nil {
		_ = d.runs.FailRun(ctx, runRecord.ID, err.Error(), "transient")
		result.Error = err.Error()
		return result, nil
	}

	exec, dispatchErr := d.broker.Dispatch(ctx, controlplane.DispatchInput{
		RunID:     &runRecord.ID,
		RunStepID: &step.ID,
		AgentID:   agentID,
		ToolName:  strings.TrimSpace(call.Name),
		ToolTier:  "tier2",
		Input:     input,
		SessionID: sessionID,
	})
	if dispatchErr != nil {
		_ = d.runs.FailStep(ctx, step.ID, dispatchErr.Error())
		_ = d.runs.FailRun(ctx, runRecord.ID, dispatchErr.Error(), failureClassForToolError(dispatchErr))
		result.Error = dispatchErr.Error()
		return result, nil
	}

	output, err := decodeOutput(exec.Output)
	if err != nil {
		_ = d.runs.FailStep(ctx, step.ID, err.Error())
		_ = d.runs.FailRun(ctx, runRecord.ID, err.Error(), "transient")
		result.Error = err.Error()
		return result, nil
	}
	result.Output = output

	if err := d.runs.CompleteStep(ctx, step.ID); err != nil {
		_ = d.runs.FailRun(ctx, runRecord.ID, err.Error(), "transient")
		result.Error = err.Error()
		return result, nil
	}

	outputRaw, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		_ = d.runs.FailRun(ctx, runRecord.ID, marshalErr.Error(), "transient")
		result.Error = marshalErr.Error()
		return result, nil
	}
	if err := d.runs.CompleteRun(ctx, runRecord.ID, outputRaw); err != nil {
		result.Error = err.Error()
		return result, nil
	}

	return result, nil
}

func (d *liveToolDispatcher) resolveOrganizationID(ctx context.Context, agentID uuid.UUID, input map[string]any) (uuid.UUID, error) {
	if orgID, err := optionalUUID(input, "organization_id"); err != nil {
		return uuid.Nil, err
	} else if orgID != nil {
		return *orgID, nil
	}

	agentRecord, err := d.agents.GetByID(ctx, agentID)
	if err != nil {
		return uuid.Nil, err
	}
	if agentRecord.OrganizationID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("organization_id is required")
	}
	return agentRecord.OrganizationID, nil
}

func cloneToolInput(call turn.ToolCall) map[string]any {
	input := cloneMap(call.Arguments)
	if call.MCPConnectionID != nil {
		if _, ok := input["mcp_connection_id"]; !ok {
			input["mcp_connection_id"] = call.MCPConnectionID.String()
		}
	}
	return input
}

func decodeOutput(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, fmt.Errorf("decode tool output: %w", err)
	}
	if output == nil {
		return map[string]any{}, nil
	}
	return output, nil
}

func requiredUUID(input map[string]any, key string) (uuid.UUID, error) {
	id, err := optionalUUID(input, key)
	if err != nil {
		return uuid.Nil, err
	}
	if id == nil || *id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s is required", key)
	}
	return *id, nil
}

func optionalUUID(input map[string]any, key string) (*uuid.UUID, error) {
	raw, ok := input[strings.TrimSpace(key)]
	if !ok || raw == nil {
		return nil, nil
	}

	switch typed := raw.(type) {
	case uuid.UUID:
		if typed == uuid.Nil {
			return nil, nil
		}
		value := typed
		return &value, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, nil
		}
		parsed, err := uuid.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", key, err)
		}
		return &parsed, nil
	default:
		return nil, fmt.Errorf("invalid %s", key)
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		copyMap := make(map[string]any, len(input))
		for key, value := range input {
			copyMap[key] = value
		}
		return copyMap
	}
	copyMap := map[string]any{}
	if err := json.Unmarshal(raw, &copyMap); err != nil {
		copyMap = make(map[string]any, len(input))
		for key, value := range input {
			copyMap[key] = value
		}
	}
	return copyMap
}

func failureClassForToolError(err error) string {
	switch {
	case errors.Is(err, controlplane.ErrCapabilityDenied), errors.Is(err, controlplane.ErrAgentDenyList):
		return "policy_denied"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, mcp.ErrTimeout):
		return "timeout"
	case errors.Is(err, controlplane.ErrToolNotSupported):
		return "permanent"
	default:
		return "transient"
	}
}

type capabilityPolicyEvaluator struct {
	evaluator *policy.PolicyEvaluator
}

func (s *capabilityPolicyEvaluator) EvaluateCapability(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, agentID uuid.UUID, capability string) (controlplane.CapabilityDecision, error) {
	if s == nil || s.evaluator == nil {
		return controlplane.CapabilityDecision{Allowed: true}, nil
	}

	agentIDCopy := agentID
	allowed, reason := s.evaluator.CheckBudgetGate(ctx, organizationID, projectID, &agentIDCopy)
	if !allowed {
		return controlplane.CapabilityDecision{
			Allowed: false,
			Reason:  strings.TrimSpace(reason),
		}, nil
	}

	decision := s.evaluator.Evaluate(ctx, policy.EvaluationRequest{
		OrganizationID: organizationID,
		ProjectID:      projectID,
		AgentID:        &agentIDCopy,
		Capability:     strings.TrimSpace(capability),
	})
	if strings.EqualFold(decision.Effect, "deny") {
		return controlplane.CapabilityDecision{
			Allowed: false,
			Reason:  strings.TrimSpace(decision.Reason),
		}, nil
	}
	return controlplane.CapabilityDecision{
		Allowed: true,
		Reason:  strings.TrimSpace(decision.Reason),
	}, nil
}
