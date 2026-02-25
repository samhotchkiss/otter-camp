package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrCapabilityDenied = errors.New("tool capability denied")
	ErrAgentDenyList    = errors.New("tool blocked by agent allow/deny rules")
	ErrToolNotSupported = errors.New("tool executor unavailable")
)

var (
	secretRefRegex = regexp.MustCompile(`\bref:([a-zA-Z0-9_]+(?:[-_][a-zA-Z0-9_]+)*)\b`)
	bearerRegex    = regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9\-._~+/]+=*`)
	apiKeyRegex    = regexp.MustCompile(`(?i)\b(?:sk-[a-z0-9]{16,}|xox[baprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z\-_]{35})\b`)
)

type CapabilityDecision struct {
	Allowed bool
	Reason  string
}

type CapabilityPolicyService interface {
	EvaluateCapability(ctx context.Context, organizationID uuid.UUID, projectID *uuid.UUID, agentID uuid.UUID, capability string) (CapabilityDecision, error)
}

type DispatchInput struct {
	RunID        *uuid.UUID
	RunStepID    *uuid.UUID
	RunAttemptID *uuid.UUID
	SessionID    *uuid.UUID
	AgentID      uuid.UUID
	ToolName     string
	ToolTier     string
	Input        map[string]any
}

type brokerToolExecutionRepository interface {
	Create(ctx context.Context, exec ToolExecution) (ToolExecution, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, output json.RawMessage, errorMessage *string) (ToolExecution, error)
}

type brokerRunRepository interface {
	Get(ctx context.Context, id uuid.UUID) (Run, error)
}

type brokerAgentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type brokerToolDefinitionRepository interface {
	GetByName(ctx context.Context, name string) (repo.ToolDefinition, error)
}

type NativeToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any) (map[string]any, error)
}

type CLIExecutor interface {
	Execute(ctx context.Context, input map[string]any) (map[string]any, error)
}

type BrowserExecutor interface {
	Execute(ctx context.Context, input map[string]any) (map[string]any, error)
}

type MCPToolExecutor interface {
	Execute(ctx context.Context, toolName string, input map[string]any, mcpConnectionID uuid.UUID) (map[string]any, error)
}

type ToolBrokerOptions struct {
	Pool            *pgxpool.Pool
	Executions      brokerToolExecutionRepository
	Runs            brokerRunRepository
	Agents          brokerAgentRepository
	ToolDefinitions brokerToolDefinitionRepository
	Policy          CapabilityPolicyService
	Native          NativeToolExecutor
	CLI             CLIExecutor
	Browser         BrowserExecutor
	MCP             MCPToolExecutor
	Now             func() time.Time
}

type ToolBroker struct {
	executions      brokerToolExecutionRepository
	runs            brokerRunRepository
	agents          brokerAgentRepository
	toolDefinitions brokerToolDefinitionRepository
	policy          CapabilityPolicyService
	native          NativeToolExecutor
	cli             CLIExecutor
	browser         BrowserExecutor
	mcp             MCPToolExecutor
	now             func() time.Time
}

func NewToolBroker(opts ToolBrokerOptions) (*ToolBroker, error) {
	requiresPool := opts.Executions == nil || opts.Runs == nil || opts.Agents == nil || opts.ToolDefinitions == nil
	if opts.Pool == nil && requiresPool {
		return nil, fmt.Errorf("tool broker requires a database pool")
	}

	broker := &ToolBroker{
		policy:  opts.Policy,
		native:  opts.Native,
		cli:     opts.CLI,
		browser: opts.Browser,
		mcp:     opts.MCP,
		now:     opts.Now,
	}
	if broker.now == nil {
		broker.now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Executions != nil {
		broker.executions = opts.Executions
	} else {
		broker.executions = NewToolExecutionRepository(opts.Pool)
	}
	if opts.Runs != nil {
		broker.runs = opts.Runs
	} else {
		broker.runs = NewRunRepository(opts.Pool)
	}
	if opts.Agents != nil {
		broker.agents = opts.Agents
	} else {
		broker.agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.ToolDefinitions != nil {
		broker.toolDefinitions = opts.ToolDefinitions
	} else {
		broker.toolDefinitions = repo.NewToolDefinitionRepo(opts.Pool)
	}
	return broker, nil
}

func (b *ToolBroker) Dispatch(ctx context.Context, input DispatchInput) (ToolExecution, error) {
	if input.AgentID == uuid.Nil {
		return ToolExecution{}, fmt.Errorf("agent_id is required")
	}
	toolName := strings.TrimSpace(input.ToolName)
	if toolName == "" {
		return ToolExecution{}, fmt.Errorf("tool_name is required")
	}
	toolTier := normalizeToolTier(input.ToolTier)
	if toolTier == "" {
		return ToolExecution{}, fmt.Errorf("tool_tier must be tier1 or tier2")
	}

	definition, err := b.toolDefinitions.GetByName(ctx, toolName)
	if err != nil {
		return ToolExecution{}, err
	}
	toolDomain := normalizeExecutionDomain(definition.ToolDomain)
	capability := trimmedPointer(definition.RequiredCapability)
	payload := redactInputForStorage(input.Input)
	metadata := brokerMetadata(input)

	agent, err := b.agents.GetByID(ctx, input.AgentID)
	if err != nil {
		return ToolExecution{}, err
	}

	var projectID *uuid.UUID
	if input.RunID != nil {
		runRecord, getErr := b.runs.Get(ctx, *input.RunID)
		if getErr != nil {
			return ToolExecution{}, getErr
		}
		if runRecord.OrganizationID != agent.OrganizationID {
			return ToolExecution{}, fmt.Errorf("run and agent organization mismatch")
		}
		projectID = runRecord.ProjectID
	}

	policyDecision := "allowed"
	if toolTier == "tier2" {
		if b.policy == nil {
			return ToolExecution{}, fmt.Errorf("policy service is required for tier2 dispatch")
		}
		if capability == nil {
			return ToolExecution{}, fmt.Errorf("tier2 tool %q missing required capability", toolName)
		}
		decision, evalErr := b.policy.EvaluateCapability(ctx, agent.OrganizationID, projectID, input.AgentID, *capability)
		if evalErr != nil {
			return ToolExecution{}, evalErr
		}
		if !decision.Allowed {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "capability denied"
			}
			denied, createErr := b.createDeniedExecution(ctx, input, toolName, toolTier, toolDomain, capability, payload, metadata, reason)
			if createErr != nil {
				return ToolExecution{}, createErr
			}
			return denied, ErrCapabilityDenied
		}
	} else {
		policyDecision = "not_checked"
	}

	if toolMatchesAnyPattern(toolName, agent.ToolDenyList) {
		denied, createErr := b.createDeniedExecution(ctx, input, toolName, toolTier, toolDomain, capability, payload, metadata, "blocked by agent deny list")
		if createErr != nil {
			return ToolExecution{}, createErr
		}
		return denied, ErrAgentDenyList
	}
	if len(agent.ToolAllowList) > 0 && !toolMatchesAnyPattern(toolName, agent.ToolAllowList) {
		denied, createErr := b.createDeniedExecution(ctx, input, toolName, toolTier, toolDomain, capability, payload, metadata, "not present in agent allow list")
		if createErr != nil {
			return ToolExecution{}, createErr
		}
		return denied, ErrAgentDenyList
	}

	startedAt := b.now()
	execution, err := b.executions.Create(ctx, ToolExecution{
		RunID:          input.RunID,
		RunStepID:      input.RunStepID,
		RunAttemptID:   input.RunAttemptID,
		ToolName:       toolName,
		ToolTier:       toolTier,
		ToolDomain:     toolDomain,
		Capability:     capability,
		PolicyDecision: policyDecision,
		Input:          payload,
		Status:         "in_progress",
		StartedAt:      &startedAt,
		Metadata:       metadata,
	})
	if err != nil {
		return ToolExecution{}, err
	}

	execCtx := mcp.WithExecutionContext(ctx, mcp.ExecutionContext{
		OrganizationID:  agent.OrganizationID,
		ToolExecutionID: &execution.ID,
		RunID:           input.RunID,
		AgentID:         &input.AgentID,
	})

	output, dispatchErr := b.dispatchToExecutor(execCtx, toolDomain, toolName, input.Input)
	if dispatchErr != nil {
		status := "failed"
		if errors.Is(dispatchErr, context.DeadlineExceeded) || errors.Is(dispatchErr, mcp.ErrTimeout) {
			status = "timed_out"
		}
		errMessage := dispatchErr.Error()
		updated, updateErr := b.executions.UpdateStatus(ctx, execution.ID, status, nil, &errMessage)
		if updateErr != nil {
			return ToolExecution{}, updateErr
		}
		return updated, dispatchErr
	}

	encodedOutput, err := json.Marshal(output)
	if err != nil {
		message := fmt.Sprintf("marshal tool output: %v", err)
		updated, updateErr := b.executions.UpdateStatus(ctx, execution.ID, "failed", nil, &message)
		if updateErr != nil {
			return ToolExecution{}, updateErr
		}
		return updated, err
	}

	updated, err := b.executions.UpdateStatus(ctx, execution.ID, "completed", encodedOutput, nil)
	if err != nil {
		return ToolExecution{}, err
	}
	return updated, nil
}

func (b *ToolBroker) dispatchToExecutor(ctx context.Context, domain, toolName string, input map[string]any) (map[string]any, error) {
	safeInput := cloneMap(input)
	switch domain {
	case "native":
		if b.native == nil {
			return nil, ErrToolNotSupported
		}
		return b.native.Execute(ctx, toolName, safeInput)
	case "cli":
		if b.cli == nil {
			return nil, ErrToolNotSupported
		}
		return b.cli.Execute(ctx, safeInput)
	case "browser":
		if b.browser == nil {
			return nil, ErrToolNotSupported
		}
		return b.browser.Execute(ctx, safeInput)
	case "mcp":
		if b.mcp == nil {
			return nil, ErrToolNotSupported
		}
		connectionID, err := extractMCPConnectionID(safeInput)
		if err != nil {
			return nil, err
		}
		return b.mcp.Execute(ctx, toolName, safeInput, connectionID)
	default:
		return nil, fmt.Errorf("unsupported tool domain %q", domain)
	}
}

func (b *ToolBroker) createDeniedExecution(ctx context.Context, input DispatchInput, toolName, toolTier, toolDomain string, capability *string, payload, metadata json.RawMessage, reason string) (ToolExecution, error) {
	started := b.now()
	completed := started
	duration := 0
	return b.executions.Create(ctx, ToolExecution{
		RunID:          input.RunID,
		RunStepID:      input.RunStepID,
		RunAttemptID:   input.RunAttemptID,
		ToolName:       toolName,
		ToolTier:       toolTier,
		ToolDomain:     toolDomain,
		Capability:     capability,
		PolicyDecision: "denied",
		Input:          payload,
		Status:         "policy_denied",
		ErrorMessage:   &reason,
		StartedAt:      &started,
		CompletedAt:    &completed,
		DurationMS:     &duration,
		Metadata:       metadata,
	})
}

func normalizeToolTier(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "tier1", "tier2":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeExecutionDomain(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "native", "mcp", "cli", "browser":
		return normalized
	case "":
		return "native"
	}

	if strings.HasPrefix(normalized, "mcp") {
		return "mcp"
	}
	if strings.Contains(normalized, "browser") {
		return "browser"
	}
	if strings.Contains(normalized, "cli") || strings.Contains(normalized, "shell") {
		return "cli"
	}
	return "native"
}

func toolMatchesAnyPattern(toolName string, patterns []string) bool {
	for _, pattern := range patterns {
		candidate := strings.TrimSpace(pattern)
		if candidate == "" {
			continue
		}
		candidate = strings.ReplaceAll(candidate, "**", "*")
		matched, err := path.Match(candidate, toolName)
		if err != nil {
			continue
		}
		if matched {
			return true
		}
	}
	return false
}

func redactInputForStorage(input map[string]any) json.RawMessage {
	redacted := redactValue(cloneMap(input))
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		clean := make(map[string]any, len(typed))
		for key, val := range typed {
			clean[key] = redactValue(val)
		}
		return clean
	case []any:
		clean := make([]any, 0, len(typed))
		for _, item := range typed {
			clean = append(clean, redactValue(item))
		}
		return clean
	case string:
		return redactStringSecrets(typed)
	default:
		return typed
	}
}

func redactStringSecrets(value string) string {
	redacted := secretRefRegex.ReplaceAllString(value, `[SECRET:$1]`)
	redacted = bearerRegex.ReplaceAllString(redacted, `[REDACTED]`)
	redacted = apiKeyRegex.ReplaceAllString(redacted, `[REDACTED]`)
	return redacted
}

func brokerMetadata(input DispatchInput) json.RawMessage {
	meta := map[string]any{}
	if input.SessionID != nil {
		meta["session_id"] = input.SessionID.String()
	}
	if input.RunID != nil {
		meta["run_id"] = input.RunID.String()
	}
	if input.RunStepID != nil {
		meta["run_step_id"] = input.RunStepID.String()
	}
	if input.RunAttemptID != nil {
		meta["run_attempt_id"] = input.RunAttemptID.String()
	}
	if len(meta) == 0 {
		return json.RawMessage(`{}`)
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func extractMCPConnectionID(input map[string]any) (uuid.UUID, error) {
	for _, key := range []string{"mcp_connection_id", "connection_id"} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		switch typed := raw.(type) {
		case string:
			parsed, err := uuid.Parse(strings.TrimSpace(typed))
			if err != nil {
				return uuid.Nil, fmt.Errorf("invalid %s: %w", key, err)
			}
			return parsed, nil
		case uuid.UUID:
			return typed, nil
		}
	}
	return uuid.Nil, fmt.Errorf("mcp_connection_id is required for mcp tools")
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		copyMap := make(map[string]any, len(input))
		for key, value := range input {
			copyMap[key] = value
		}
		return copyMap
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		copyMap := make(map[string]any, len(input))
		for key, value := range input {
			copyMap[key] = value
		}
		return copyMap
	}
	if decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func trimmedPointer(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

type ToolExecutionService struct {
	broker *ToolBroker
}

func NewToolExecutionService(broker *ToolBroker) *ToolExecutionService {
	return &ToolExecutionService{broker: broker}
}

func (s *ToolExecutionService) ExecuteTier1Tool(ctx context.Context, sessionID, agentID uuid.UUID, toolName string, input map[string]any) (map[string]any, error) {
	if s == nil || s.broker == nil {
		return nil, fmt.Errorf("tool execution service is not initialized")
	}
	var sessionIDPtr *uuid.UUID
	if sessionID != uuid.Nil {
		sessionIDPtr = &sessionID
	}
	result, err := s.broker.Dispatch(ctx, DispatchInput{
		SessionID: sessionIDPtr,
		AgentID:   agentID,
		ToolName:  toolName,
		ToolTier:  "tier1",
		Input:     input,
	})
	if err != nil {
		return nil, err
	}
	return decodeToolOutput(result.Output)
}

func (s *ToolExecutionService) ExecuteTier2Tool(ctx context.Context, runID, runStepID, runAttemptID, agentID uuid.UUID, toolName string, input map[string]any) (map[string]any, error) {
	if s == nil || s.broker == nil {
		return nil, fmt.Errorf("tool execution service is not initialized")
	}
	if runID == uuid.Nil || runStepID == uuid.Nil || runAttemptID == uuid.Nil {
		return nil, fmt.Errorf("run_id, run_step_id, and run_attempt_id are required")
	}
	result, err := s.broker.Dispatch(ctx, DispatchInput{
		RunID:        &runID,
		RunStepID:    &runStepID,
		RunAttemptID: &runAttemptID,
		AgentID:      agentID,
		ToolName:     toolName,
		ToolTier:     "tier2",
		Input:        input,
	})
	if err != nil {
		return nil, err
	}
	return decodeToolOutput(result.Output)
}

func decodeToolOutput(output json.RawMessage) (map[string]any, error) {
	if len(output) == 0 {
		return map[string]any{}, nil
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(output, &decoded); err != nil {
		return nil, fmt.Errorf("decode tool output: %w", err)
	}
	return decoded, nil
}
