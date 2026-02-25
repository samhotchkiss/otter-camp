package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/eventbus"
	"github.com/samhotchkiss/otter-camp/internal/policy"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type ToolDescriptor struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"input_schema"`
	Tier            string          `json:"tier"`
	Domain          string          `json:"domain"`
	Capability      *string         `json:"capability"`
	Source          string          `json:"source"`
	MCPConnectionID *uuid.UUID      `json:"mcp_connection_id"`
	IsEnabled       bool            `json:"is_enabled"`
	Priority        int             `json:"priority"`
}

type sessionToolSetRepository interface {
	Create(ctx context.Context, sessionID, agentID uuid.UUID, toolSet json.RawMessage) (*repo.SessionToolSet, error)
	GetActive(ctx context.Context, sessionID, agentID uuid.UUID) (*repo.SessionToolSet, error)
	Invalidate(ctx context.Context, sessionID, agentID uuid.UUID) error
	ReplaceToolSet(ctx context.Context, sessionID, agentID uuid.UUID, newToolSet json.RawMessage) (*repo.SessionToolSet, error)
	ListActiveByOrganization(ctx context.Context, organizationID uuid.UUID) ([]repo.SessionToolSet, error)
}

type chatSessionRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ChatSession, error)
}

type agentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type projectTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type flowNodeExecutionRepository interface {
	GetActive(ctx context.Context, taskID, flowNodeID uuid.UUID) (repo.FlowNodeExecution, error)
}

type flowNodeRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.FlowNode, error)
}

type toolDefinitionRepository interface {
	ListEnabled(ctx context.Context) ([]repo.ToolDefinition, error)
}

type mcpConnectionRepository interface {
	ListEnabled(ctx context.Context) ([]repo.MCPConnection, error)
}

type mcpToolCatalogRepository interface {
	GetEnabled(ctx context.Context, connectionID uuid.UUID) ([]repo.MCPToolCatalogEntry, error)
}

type policyEvaluator interface {
	Evaluate(ctx context.Context, req policy.EvaluationRequest) policy.PolicyDecision
}

type eventSubscriber interface {
	Subscribe(consumerName string, orgID *uuid.UUID, handler eventbus.EventHandler) eventbus.Subscription
}

type ToolResolverOptions struct {
	Pool *pgxpool.Pool

	SessionToolSets sessionToolSetRepository
	Sessions        chatSessionRepository
	Agents          agentRepository
	Tasks           projectTaskRepository
	FlowExecutions  flowNodeExecutionRepository
	FlowNodes       flowNodeRepository
	ToolDefinitions toolDefinitionRepository
	MCPConnections  mcpConnectionRepository
	MCPCatalog      mcpToolCatalogRepository
	Policies        policyEvaluator
	Events          eventSubscriber
	Logger          *slog.Logger
}

type ToolResolver struct {
	sessionToolSets sessionToolSetRepository
	sessions        chatSessionRepository
	agents          agentRepository
	tasks           projectTaskRepository
	flowExecutions  flowNodeExecutionRepository
	flowNodes       flowNodeRepository
	toolDefinitions toolDefinitionRepository
	mcpConnections  mcpConnectionRepository
	mcpCatalog      mcpToolCatalogRepository
	policies        policyEvaluator
	events          eventSubscriber
	logger          *slog.Logger
}

func NewToolResolver(opts ToolResolverOptions) (*ToolResolver, error) {
	requiresPool := opts.SessionToolSets == nil ||
		opts.Sessions == nil ||
		opts.Agents == nil ||
		opts.Tasks == nil ||
		opts.FlowExecutions == nil ||
		opts.FlowNodes == nil ||
		opts.ToolDefinitions == nil ||
		opts.MCPConnections == nil ||
		opts.MCPCatalog == nil
	if requiresPool && opts.Pool == nil {
		return nil, fmt.Errorf("tool resolver requires a database pool")
	}

	r := &ToolResolver{
		policies: opts.Policies,
		events:   opts.Events,
		logger:   opts.Logger,
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}

	if opts.SessionToolSets != nil {
		r.sessionToolSets = opts.SessionToolSets
	} else {
		r.sessionToolSets = repo.NewSessionToolSetRepo(opts.Pool)
	}
	if opts.Sessions != nil {
		r.sessions = opts.Sessions
	} else {
		r.sessions = repo.NewChatSessionRepo(opts.Pool)
	}
	if opts.Agents != nil {
		r.agents = opts.Agents
	} else {
		r.agents = repo.NewAgentRepo(opts.Pool)
	}
	if opts.Tasks != nil {
		r.tasks = opts.Tasks
	} else {
		r.tasks = repo.NewProjectTaskRepo(opts.Pool)
	}
	if opts.FlowExecutions != nil {
		r.flowExecutions = opts.FlowExecutions
	} else {
		r.flowExecutions = repo.NewFlowNodeExecutionRepo(opts.Pool)
	}
	if opts.FlowNodes != nil {
		r.flowNodes = opts.FlowNodes
	} else {
		r.flowNodes = repo.NewFlowNodeRepo(opts.Pool)
	}
	if opts.ToolDefinitions != nil {
		r.toolDefinitions = opts.ToolDefinitions
	} else {
		r.toolDefinitions = repo.NewToolDefinitionRepo(opts.Pool)
	}
	if opts.MCPConnections != nil {
		r.mcpConnections = opts.MCPConnections
	} else {
		r.mcpConnections = repo.NewMCPConnectionRepo(opts.Pool)
	}
	if opts.MCPCatalog != nil {
		r.mcpCatalog = opts.MCPCatalog
	} else {
		r.mcpCatalog = repo.NewMCPToolCatalogRepo(opts.Pool)
	}

	return r, nil
}

func (r *ToolResolver) GetSessionToolSet(ctx context.Context, sessionID, agentID uuid.UUID) ([]ToolDescriptor, error) {
	if cached, err := r.sessionToolSets.GetActive(ctx, sessionID, agentID); err == nil {
		return decodeToolSet(cached.ToolSet)
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}

	session, err := r.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	toolSet, err := r.resolveToolSet(ctx, session, agentID)
	if err != nil {
		return nil, err
	}
	assignPriorities(toolSet)

	encoded, err := json.Marshal(toolSet)
	if err != nil {
		return nil, err
	}
	created, err := r.sessionToolSets.Create(ctx, sessionID, agentID, encoded)
	if err != nil {
		return nil, err
	}
	return decodeToolSet(created.ToolSet)
}

func (r *ToolResolver) HandlePolicyUpdate(ctx context.Context, orgID uuid.UUID) error {
	if orgID == uuid.Nil {
		return fmt.Errorf("organization id is required")
	}
	activeSets, err := r.sessionToolSets.ListActiveByOrganization(ctx, orgID)
	if err != nil {
		return err
	}
	for _, set := range activeSets {
		if err := r.sessionToolSets.Invalidate(ctx, set.SessionID, set.AgentID); err != nil {
			return err
		}
	}
	return nil
}

func (r *ToolResolver) SubscribePolicyUpdates(orgID *uuid.UUID) eventbus.Subscription {
	if r.events == nil {
		return eventbus.Subscription{}
	}

	return r.events.Subscribe("tools.policy-updates", orgID, func(ctx context.Context, event eventbus.DomainEvent) error {
		if event.EventType != "capability.policy.updated" {
			return nil
		}
		if event.OrganizationID == uuid.Nil {
			if orgID == nil || *orgID == uuid.Nil {
				return nil
			}
			return r.HandlePolicyUpdate(ctx, *orgID)
		}
		return r.HandlePolicyUpdate(ctx, event.OrganizationID)
	})
}

func (r *ToolResolver) resolveToolSet(ctx context.Context, session repo.ChatSession, agentID uuid.UUID) ([]ToolDescriptor, error) {
	sessionProjectID, err := r.resolveSessionProjectID(ctx, session)
	if err != nil {
		return nil, err
	}

	stage1, err := r.buildUniverse(ctx, session, sessionProjectID)
	if err != nil {
		return nil, err
	}
	agentRecord, err := r.agents.GetByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	stage2 := applyAgentFilters(stage1, agentRecord.ToolDenyList, agentRecord.ToolAllowList)
	stage3, err := r.applyFlowNodeOrdering(ctx, session, stage2)
	if err != nil {
		return nil, err
	}
	stage4, err := r.applyCapabilityGate(ctx, session.OrganizationID, sessionProjectID, agentID, stage3)
	if err != nil {
		return nil, err
	}
	return stage4, nil
}

func (r *ToolResolver) resolveSessionProjectID(ctx context.Context, session repo.ChatSession) (*uuid.UUID, error) {
	switch strings.TrimSpace(session.ScopeType) {
	case "project":
		projectID := session.ScopeID
		return &projectID, nil
	case "project_task":
		taskRecord, err := r.tasks.GetByID(ctx, session.ScopeID)
		if err != nil {
			return nil, err
		}
		projectID := taskRecord.ProjectID
		return &projectID, nil
	default:
		return nil, nil
	}
}

func (r *ToolResolver) buildUniverse(ctx context.Context, session repo.ChatSession, sessionProjectID *uuid.UUID) ([]ToolDescriptor, error) {
	nativeTools, err := r.toolDefinitions.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	resolved := make([]ToolDescriptor, 0, len(nativeTools))
	for _, tool := range nativeTools {
		domain := strings.TrimSpace(tool.ToolDomain)
		if domain == "" {
			domain = toolDomainFromName(tool.Name)
		}
		resolved = append(resolved, ToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: normalizedInputSchema(tool.InputSchema),
			Tier:        strings.TrimSpace(tool.ToolTier),
			Domain:      domain,
			Capability:  trimCapability(tool.RequiredCapability),
			Source:      "native",
			IsEnabled:   true,
		})
	}

	connections, err := r.mcpConnections.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	for _, connection := range connections {
		if connection.OrganizationID != session.OrganizationID {
			continue
		}
		if !connectionVisibleInSession(connection.ProjectID, sessionProjectID) {
			continue
		}
		catalogEntries, err := r.mcpCatalog.GetEnabled(ctx, connection.ID)
		if err != nil {
			return nil, err
		}
		for _, entry := range catalogEntries {
			connectionID := connection.ID
			resolved = append(resolved, ToolDescriptor{
				Name:            entry.ToolName,
				Description:     entry.Description,
				InputSchema:     normalizedInputSchema(entry.InputSchema),
				Tier:            "tier2",
				Domain:          "mcp",
				Capability:      nil,
				Source:          "mcp",
				MCPConnectionID: &connectionID,
				IsEnabled:       true,
			})
		}
	}

	return resolved, nil
}

func (r *ToolResolver) applyFlowNodeOrdering(ctx context.Context, session repo.ChatSession, tools []ToolDescriptor) ([]ToolDescriptor, error) {
	if strings.TrimSpace(session.ScopeType) != "project_task" {
		return tools, nil
	}

	taskRecord, err := r.tasks.GetByID(ctx, session.ScopeID)
	if err != nil {
		return nil, err
	}
	if taskRecord.CurrentFlowNodeID == nil {
		return tools, nil
	}

	if _, err := r.flowExecutions.GetActive(ctx, taskRecord.ID, *taskRecord.CurrentFlowNodeID); errors.Is(err, repo.ErrNotFound) {
		return tools, nil
	} else if err != nil {
		return nil, err
	}

	flowNode, err := r.flowNodes.GetByID(ctx, *taskRecord.CurrentFlowNodeID)
	if err != nil {
		return nil, err
	}
	if len(flowNode.ToolDomains) == 0 && len(flowNode.MCPTools) == 0 {
		return tools, nil
	}

	allowedDomains := make(map[string]struct{}, len(flowNode.ToolDomains))
	for _, domain := range flowNode.ToolDomains {
		d := strings.TrimSpace(strings.ToLower(domain))
		if d == "" {
			continue
		}
		allowedDomains[d] = struct{}{}
	}

	prioritizedMCP := make(map[string]struct{}, len(flowNode.MCPTools))
	for _, item := range flowNode.MCPTools {
		prioritizedMCP[mcpPriorityKey(item.ConnectionID, item.ToolName)] = struct{}{}
	}

	front := make([]ToolDescriptor, 0)
	middle := make([]ToolDescriptor, 0)
	back := make([]ToolDescriptor, 0)

	for _, tool := range tools {
		if isPrioritizedMCPTool(tool, prioritizedMCP) {
			front = append(front, tool)
			continue
		}
		if len(allowedDomains) > 0 {
			if _, ok := allowedDomains[strings.TrimSpace(strings.ToLower(tool.Domain))]; !ok {
				back = append(back, tool)
				continue
			}
		}
		middle = append(middle, tool)
	}

	out := make([]ToolDescriptor, 0, len(tools))
	out = append(out, front...)
	out = append(out, middle...)
	out = append(out, back...)
	return out, nil
}

func (r *ToolResolver) applyCapabilityGate(ctx context.Context, orgID uuid.UUID, projectID *uuid.UUID, agentID uuid.UUID, tools []ToolDescriptor) ([]ToolDescriptor, error) {
	if r.policies == nil {
		return tools, nil
	}

	filtered := make([]ToolDescriptor, 0, len(tools))
	for _, tool := range tools {
		capability := ""
		if tool.Capability != nil {
			capability = strings.TrimSpace(*tool.Capability)
		}
		if capability == "" {
			filtered = append(filtered, tool)
			continue
		}

		decision := r.policies.Evaluate(ctx, policy.EvaluationRequest{
			OrganizationID: orgID,
			ProjectID:      projectID,
			AgentID:        &agentID,
			Capability:     capability,
			Context: map[string]any{
				"tool_name":   tool.Name,
				"tool_domain": tool.Domain,
			},
		})
		denied := strings.EqualFold(decision.Effect, "deny")
		if denied && strings.EqualFold(tool.Tier, "tier2") {
			continue
		}
		if denied && strings.EqualFold(tool.Tier, "tier1") {
			r.logger.Debug("tier1 capability denied but retained",
				"tool_name", tool.Name,
				"capability", capability,
				"layer", decision.Layer,
				"reason", decision.Reason,
			)
		}
		filtered = append(filtered, tool)
	}
	return filtered, nil
}

func applyAgentFilters(input []ToolDescriptor, denyPatterns, allowPatterns []string) []ToolDescriptor {
	denies := normalizedPatterns(denyPatterns)
	allows := normalizedPatterns(allowPatterns)

	filtered := make([]ToolDescriptor, 0, len(input))
	for _, tool := range input {
		if matchesAnyPattern(denies, tool.Name) {
			continue
		}
		if len(allows) > 0 && !matchesAnyPattern(allows, tool.Name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func normalizedPatterns(patterns []string) []string {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func matchesAnyPattern(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matchToolGlob(pattern, name) {
			return true
		}
	}
	return false
}

// matchToolGlob implements dot-segment matching rules:
// "*" matches one segment and, when terminal, behaves as a prefix wildcard.
// "**" matches any remaining segment sequence.
func matchToolGlob(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	name = strings.TrimSpace(name)
	if pattern == "" || name == "" {
		return false
	}

	patternSeg := strings.Split(pattern, ".")
	nameSeg := strings.Split(name, ".")

	var walk func(pi, ni int) bool
	walk = func(pi, ni int) bool {
		if pi == len(patternSeg) {
			return ni == len(nameSeg)
		}
		segment := patternSeg[pi]
		switch segment {
		case "**":
			for next := ni; next <= len(nameSeg); next++ {
				if walk(pi+1, next) {
					return true
				}
			}
			return false
		case "*":
			if ni >= len(nameSeg) {
				return false
			}
			if pi == len(patternSeg)-1 {
				return true
			}
			return walk(pi+1, ni+1)
		default:
			if ni >= len(nameSeg) {
				return false
			}
			if segment != nameSeg[ni] {
				return false
			}
			return walk(pi+1, ni+1)
		}
	}

	return walk(0, 0)
}

func decodeToolSet(raw json.RawMessage) ([]ToolDescriptor, error) {
	if len(raw) == 0 {
		return []ToolDescriptor{}, nil
	}
	var descriptors []ToolDescriptor
	if err := json.Unmarshal(raw, &descriptors); err != nil {
		return nil, err
	}
	for i := range descriptors {
		descriptors[i].InputSchema = normalizedInputSchema(descriptors[i].InputSchema)
	}
	return descriptors, nil
}

func normalizedInputSchema(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}

func assignPriorities(tools []ToolDescriptor) {
	for i := range tools {
		tools[i].Priority = i + 1
	}
}

func connectionVisibleInSession(connectionProjectID, sessionProjectID *uuid.UUID) bool {
	if connectionProjectID == nil || *connectionProjectID == uuid.Nil {
		return true
	}
	if sessionProjectID == nil || *sessionProjectID == uuid.Nil {
		return false
	}
	return *connectionProjectID == *sessionProjectID
}

func mcpPriorityKey(connectionID uuid.UUID, toolName string) string {
	return connectionID.String() + "|" + strings.TrimSpace(toolName)
}

func isPrioritizedMCPTool(tool ToolDescriptor, prioritized map[string]struct{}) bool {
	if tool.Source != "mcp" || tool.MCPConnectionID == nil {
		return false
	}
	_, ok := prioritized[mcpPriorityKey(*tool.MCPConnectionID, tool.Name)]
	return ok
}

func trimCapability(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func toolDomainFromName(toolName string) string {
	trimmed := strings.TrimSpace(toolName)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, ".")
	return parts[0]
}
