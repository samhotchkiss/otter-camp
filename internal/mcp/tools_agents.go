package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type AgentStore interface {
	List(ctx context.Context) ([]store.Agent, error)
	GetByID(ctx context.Context, id string) (*store.Agent, error)
	GetBySlug(ctx context.Context, slug string) (*store.Agent, error)
}

type AgentActivityStore interface {
	ListByAgent(ctx context.Context, agentID string, opts store.ListAgentActivityOptions) ([]store.AgentActivityEvent, error)
}

func RegisterAgentTools(s *Server) error {
	if s == nil {
		return fmt.Errorf("%w: server is required", ErrInvalidToolCall)
	}

	registerErr := s.RegisterTool(Tool{
		Name:        "agent_list",
		Description: "List agents in the workspace",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleAgentListTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "agent_get",
		Description: "Get an agent by ID or slug",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"agent"},
			"properties": map[string]any{
				"agent": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleAgentGetTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "agent_activity",
		Description: "Get recent activity for an agent",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"agent"},
			"properties": map[string]any{
				"agent": map[string]any{"type": "string"},
				"limit": map[string]any{"type": "number"},
			},
		},
		Handler: s.handleAgentActivityTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "whoami",
		Description: "Get current authenticated identity",
		InputSchema: map[string]any{
			"type": "object",
		},
		Handler: s.handleWhoAmITool,
	})
}

func (s *Server) handleAgentListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.agents == nil {
		return ToolCallResult{}, fmt.Errorf("%w: agent store unavailable", ErrInvalidToolCall)
	}
	status := readOptionalStringArg(args, "status", "all")
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "all" && status != "online" && status != "offline" {
		return ToolCallResult{}, fmt.Errorf("%w: status must be online, offline, or all", ErrInvalidToolCall)
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	agents, err := s.agents.List(workspaceCtx)
	if err != nil {
		return ToolCallResult{}, err
	}

	items := make([]map[string]any, 0, len(agents))
	for _, agent := range agents {
		if agent.OrgID != identity.OrgID {
			continue
		}
		if status != "all" && strings.ToLower(strings.TrimSpace(agent.Status)) != status {
			continue
		}
		items = append(items, map[string]any{
			"id":           agent.ID,
			"slug":         agent.Slug,
			"display_name": agent.DisplayName,
			"status":       agent.Status,
		})
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"agents": items,
					"total":  len(items),
				},
			},
		},
	}, nil
}

func (s *Server) handleAgentGetTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	agent, err := s.resolveAgent(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"agent": map[string]any{
						"id":           agent.ID,
						"slug":         agent.Slug,
						"display_name": agent.DisplayName,
						"status":       agent.Status,
					},
				},
			},
		},
	}, nil
}

func (s *Server) handleAgentActivityTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	agent, err := s.resolveAgent(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	limit := 20
	if rawLimit, ok := args["limit"]; ok {
		value, ok := rawLimit.(float64)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be a number", ErrInvalidToolCall)
		}
		limit = int(value)
		if limit <= 0 {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidToolCall)
		}
	}
	if s.agentEvents == nil {
		return ToolCallResult{
			Content: []ToolContent{
				{
					Type: "json",
					Data: map[string]any{
						"agent_id": agent.ID,
						"events":   []any{},
						"total":    0,
					},
				},
			},
		}, nil
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	events, err := s.agentEvents.ListByAgent(workspaceCtx, agent.ID, store.ListAgentActivityOptions{Limit: limit})
	if err != nil {
		return ToolCallResult{}, err
	}
	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, map[string]any{
			"id":         event.ID,
			"summary":    event.Summary,
			"status":     event.Status,
			"started_at": event.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"agent_id": agent.ID,
					"events":   items,
					"total":    len(items),
				},
			},
		},
	}, nil
}

func (s *Server) handleWhoAmITool(_ context.Context, identity Identity, _ map[string]any) (ToolCallResult, error) {
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"org_id":  identity.OrgID,
					"user_id": identity.UserID,
					"role":    identity.Role,
				},
			},
		},
	}, nil
}

func (s *Server) resolveAgent(ctx context.Context, identity Identity, args map[string]any) (*store.Agent, error) {
	if s.agents == nil {
		return nil, fmt.Errorf("%w: agent store unavailable", ErrInvalidToolCall)
	}
	rawAgent, ok := args["agent"]
	if !ok {
		return nil, fmt.Errorf("%w: agent is required", ErrInvalidToolCall)
	}
	agentRef, ok := rawAgent.(string)
	if !ok {
		return nil, fmt.Errorf("%w: agent must be a string", ErrInvalidToolCall)
	}
	agentRef = strings.TrimSpace(agentRef)
	if agentRef == "" {
		return nil, fmt.Errorf("%w: agent is required", ErrInvalidToolCall)
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	var (
		agent *store.Agent
		err   error
	)
	if projectUUIDRegex.MatchString(agentRef) {
		agent, err = s.agents.GetByID(workspaceCtx, agentRef)
	} else {
		agent, err = s.agents.GetBySlug(workspaceCtx, strings.ToLower(agentRef))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: agent not found", ErrInvalidToolCall)
	}
	if agent.OrgID != identity.OrgID {
		return nil, fmt.Errorf("%w: agent belongs to another workspace", ErrInvalidToolCall)
	}
	return agent, nil
}
