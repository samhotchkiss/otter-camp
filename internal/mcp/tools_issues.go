package mcp

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

type IssueStore interface {
	ListIssues(ctx context.Context, filter store.ProjectIssueFilter) ([]store.ProjectIssue, error)
	GetIssueByID(ctx context.Context, issueID string) (*store.ProjectIssue, error)
}

func RegisterIssueTools(s *Server) error {
	if s == nil {
		return fmt.Errorf("%w: server is required", ErrInvalidToolCall)
	}

	registerErr := s.RegisterTool(Tool{
		Name:        "issue_list",
		Description: "List issues in a project",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"status":   map[string]any{"type": "string"},
				"assignee": map[string]any{"type": "string"},
				"label":    map[string]any{"type": "string"},
				"priority": map[string]any{"type": "string"},
				"limit":    map[string]any{"type": "number"},
			},
		},
		Handler: s.handleIssueListTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "issue_get",
		Description: "Get issue details by project and issue number",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"number",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"number":  map[string]any{"type": "number"},
			},
		},
		Handler: s.handleIssueGetTool,
	})
}

func (s *Server) handleIssueListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.issues == nil {
		return ToolCallResult{}, fmt.Errorf("%w: issue store unavailable", ErrInvalidToolCall)
	}
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}

	var state *string
	if rawStatus, ok := args["status"]; ok {
		status, ok := rawStatus.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: status must be a string", ErrInvalidToolCall)
		}
		status = strings.ToLower(strings.TrimSpace(status))
		switch status {
		case "", "all":
			// leave nil
		case "open", "closed":
			state = &status
		default:
			return ToolCallResult{}, fmt.Errorf("%w: status must be open, closed, or all", ErrInvalidToolCall)
		}
	}

	var ownerAgentID *string
	if rawAssignee, ok := args["assignee"]; ok {
		assignee, ok := rawAssignee.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: assignee must be a string", ErrInvalidToolCall)
		}
		assignee = strings.TrimSpace(assignee)
		if assignee != "" {
			ownerAgentID = &assignee
		}
	}
	if rawLabel, ok := args["label"]; ok {
		label, ok := rawLabel.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: label must be a string", ErrInvalidToolCall)
		}
		if strings.TrimSpace(label) != "" {
			return ToolCallResult{}, fmt.Errorf("%w: label filtering is not implemented yet", ErrInvalidToolCall)
		}
	}

	var priority *string
	if rawPriority, ok := args["priority"]; ok {
		priorityText, ok := rawPriority.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: priority must be a string", ErrInvalidToolCall)
		}
		priorityText = strings.TrimSpace(priorityText)
		if priorityText != "" {
			priority = &priorityText
		}
	}

	limit := 100
	if rawLimit, ok := args["limit"]; ok {
		limitFloat, ok := rawLimit.(float64)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be a number", ErrInvalidToolCall)
		}
		limit = int(limitFloat)
		if limit <= 0 {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidToolCall)
		}
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	issues, err := s.issues.ListIssues(workspaceCtx, store.ProjectIssueFilter{
		ProjectID:    project.ID,
		State:        state,
		OwnerAgentID: ownerAgentID,
		Priority:     priority,
		Limit:        limit,
	})
	if err != nil {
		return ToolCallResult{}, err
	}

	items := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		if issue.OrgID != identity.OrgID {
			continue
		}
		items = append(items, toIssuePayload(issue))
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"issues": items,
					"total":  len(items),
				},
			},
		},
	}, nil
}

func (s *Server) handleIssueGetTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.issues == nil {
		return ToolCallResult{}, fmt.Errorf("%w: issue store unavailable", ErrInvalidToolCall)
	}
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}

	rawNumber, ok := args["number"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: number is required", ErrInvalidToolCall)
	}
	numberValue, ok := rawNumber.(float64)
	if !ok || numberValue <= 0 || math.Mod(numberValue, 1) != 0 {
		return ToolCallResult{}, fmt.Errorf("%w: number must be a positive integer", ErrInvalidToolCall)
	}
	issueNumber := int64(numberValue)

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	issues, err := s.issues.ListIssues(workspaceCtx, store.ProjectIssueFilter{
		ProjectID:   project.ID,
		IssueNumber: &issueNumber,
		Limit:       1,
	})
	if err != nil {
		return ToolCallResult{}, err
	}
	if len(issues) == 0 {
		return ToolCallResult{}, fmt.Errorf("%w: issue not found", ErrInvalidToolCall)
	}
	issue := issues[0]
	if issue.OrgID != identity.OrgID || issue.ProjectID != project.ID {
		return ToolCallResult{}, fmt.Errorf("%w: issue belongs to another workspace", ErrInvalidToolCall)
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"issue": toIssuePayload(issue),
				},
			},
		},
	}, nil
}

func toIssuePayload(issue store.ProjectIssue) map[string]any {
	payload := map[string]any{
		"id":         issue.ID,
		"project_id": issue.ProjectID,
		"number":     issue.IssueNumber,
		"title":      issue.Title,
		"state":      issue.State,
		"priority":   issue.Priority,
		"origin":     issue.Origin,
	}
	if issue.OwnerAgentID != nil {
		payload["owner_agent_id"] = *issue.OwnerAgentID
	}
	if issue.Body != nil {
		payload["body"] = *issue.Body
	}
	return payload
}
