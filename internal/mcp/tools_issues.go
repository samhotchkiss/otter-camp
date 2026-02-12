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
	CreateIssue(ctx context.Context, input store.CreateProjectIssueInput) (*store.ProjectIssue, error)
	UpdateIssueWorkTracking(ctx context.Context, input store.UpdateProjectIssueWorkTrackingInput) (*store.ProjectIssue, error)
	CreateComment(ctx context.Context, input store.CreateProjectIssueCommentInput) (*store.ProjectIssueComment, error)
	AddParticipant(ctx context.Context, input store.AddProjectIssueParticipantInput) (*store.ProjectIssueParticipant, error)
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

	registerErr = s.RegisterTool(Tool{
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
	if registerErr != nil {
		return registerErr
	}

	return s.registerIssueMutationTools()
}

func (s *Server) registerIssueMutationTools() error {
	registerErr := s.RegisterTool(Tool{
		Name:        "issue_create",
		Description: "Create an issue in a project",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"title",
			},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"title":    map[string]any{"type": "string"},
				"body":     map[string]any{"type": "string"},
				"priority": map[string]any{"type": "string"},
				"assignee": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleIssueCreateTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "issue_update",
		Description: "Update issue state/priority/assignee",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"number",
			},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"number":   map[string]any{"type": "number"},
				"status":   map[string]any{"type": "string"},
				"priority": map[string]any{"type": "string"},
				"assignee": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleIssueUpdateTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "issue_close",
		Description: "Close an issue",
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
		Handler: s.handleIssueCloseTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "issue_reopen",
		Description: "Reopen an issue",
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
		Handler: s.handleIssueReopenTool,
	})
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "issue_comment",
		Description: "Add a comment to an issue",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"number",
				"body",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
				"number":  map[string]any{"type": "number"},
				"body":    map[string]any{"type": "string"},
			},
		},
		Handler: s.handleIssueCommentTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "issue_assign",
		Description: "Assign an issue to an agent",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
				"number",
				"assignee",
			},
			"properties": map[string]any{
				"project":  map[string]any{"type": "string"},
				"number":   map[string]any{"type": "number"},
				"assignee": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleIssueAssignTool,
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

func (s *Server) handleIssueCreateTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}

	rawTitle, ok := args["title"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: title is required", ErrInvalidToolCall)
	}
	title, ok := rawTitle.(string)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: title must be a string", ErrInvalidToolCall)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return ToolCallResult{}, fmt.Errorf("%w: title is required", ErrInvalidToolCall)
	}

	var body *string
	if rawBody, ok := args["body"]; ok {
		bodyText, ok := rawBody.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: body must be a string", ErrInvalidToolCall)
		}
		bodyText = strings.TrimSpace(bodyText)
		body = &bodyText
	}

	priority := ""
	if rawPriority, ok := args["priority"]; ok {
		priorityText, ok := rawPriority.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: priority must be a string", ErrInvalidToolCall)
		}
		priority = strings.TrimSpace(priorityText)
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

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	issue, err := s.issues.CreateIssue(workspaceCtx, store.CreateProjectIssueInput{
		ProjectID:    project.ID,
		Title:        title,
		Body:         body,
		State:        "open",
		Origin:       "local",
		OwnerAgentID: ownerAgentID,
		Priority:     priority,
	})
	if err != nil {
		return ToolCallResult{}, err
	}
	if issue.OrgID != identity.OrgID || issue.ProjectID != project.ID {
		return ToolCallResult{}, fmt.Errorf("%w: created issue belongs to another workspace", ErrInvalidToolCall)
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"issue": toIssuePayload(*issue),
				},
			},
		},
	}, nil
}

func (s *Server) handleIssueUpdateTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	updated, err := s.updateIssueFromArgs(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{"issue": toIssuePayload(*updated)},
			},
		},
	}, nil
}

func (s *Server) handleIssueCloseTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	argsCopy := cloneMap(args)
	argsCopy["status"] = "closed"
	updated, err := s.updateIssueFromArgs(ctx, identity, argsCopy)
	if err != nil {
		return ToolCallResult{}, err
	}
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{"issue": toIssuePayload(*updated)},
			},
		},
	}, nil
}

func (s *Server) handleIssueReopenTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	argsCopy := cloneMap(args)
	argsCopy["status"] = "open"
	updated, err := s.updateIssueFromArgs(ctx, identity, argsCopy)
	if err != nil {
		return ToolCallResult{}, err
	}
	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{"issue": toIssuePayload(*updated)},
			},
		},
	}, nil
}

func (s *Server) handleIssueCommentTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	issue, err := s.resolveIssueByNumber(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}
	rawBody, ok := args["body"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: body is required", ErrInvalidToolCall)
	}
	body, ok := rawBody.(string)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: body must be a string", ErrInvalidToolCall)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return ToolCallResult{}, fmt.Errorf("%w: body is required", ErrInvalidToolCall)
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	comment, err := s.issues.CreateComment(workspaceCtx, store.CreateProjectIssueCommentInput{
		IssueID:       issue.ID,
		AuthorAgentID: identity.UserID,
		Body:          body,
	})
	if err != nil {
		return ToolCallResult{}, err
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"comment": map[string]any{
						"id":              comment.ID,
						"issue_id":        comment.IssueID,
						"author_agent_id": comment.AuthorAgentID,
						"body":            comment.Body,
					},
				},
			},
		},
	}, nil
}

func (s *Server) handleIssueAssignTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	rawAssignee, ok := args["assignee"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: assignee is required", ErrInvalidToolCall)
	}
	assignee, ok := rawAssignee.(string)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: assignee must be a string", ErrInvalidToolCall)
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		return ToolCallResult{}, fmt.Errorf("%w: assignee is required", ErrInvalidToolCall)
	}

	argsCopy := cloneMap(args)
	argsCopy["assignee"] = assignee
	updated, err := s.updateIssueFromArgs(ctx, identity, argsCopy)
	if err != nil {
		return ToolCallResult{}, err
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	_, _ = s.issues.AddParticipant(workspaceCtx, store.AddProjectIssueParticipantInput{
		IssueID: updated.ID,
		AgentID: assignee,
		Role:    "owner",
	})

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{"issue": toIssuePayload(*updated)},
			},
		},
	}, nil
}

func (s *Server) updateIssueFromArgs(ctx context.Context, identity Identity, args map[string]any) (*store.ProjectIssue, error) {
	issue, err := s.resolveIssueByNumber(ctx, identity, args)
	if err != nil {
		return nil, err
	}
	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)

	input := store.UpdateProjectIssueWorkTrackingInput{IssueID: issue.ID}
	hasUpdate := false
	if rawStatus, ok := args["status"]; ok {
		status, ok := rawStatus.(string)
		if !ok {
			return nil, fmt.Errorf("%w: status must be a string", ErrInvalidToolCall)
		}
		status = strings.ToLower(strings.TrimSpace(status))
		if status != "open" && status != "closed" {
			return nil, fmt.Errorf("%w: status must be open or closed", ErrInvalidToolCall)
		}
		input.SetState = true
		input.State = status
		hasUpdate = true
	}
	if rawPriority, ok := args["priority"]; ok {
		priority, ok := rawPriority.(string)
		if !ok {
			return nil, fmt.Errorf("%w: priority must be a string", ErrInvalidToolCall)
		}
		input.SetPriority = true
		input.Priority = strings.TrimSpace(priority)
		hasUpdate = true
	}
	if rawAssignee, ok := args["assignee"]; ok {
		assignee, ok := rawAssignee.(string)
		if !ok {
			return nil, fmt.Errorf("%w: assignee must be a string", ErrInvalidToolCall)
		}
		assignee = strings.TrimSpace(assignee)
		input.SetOwnerAgentID = true
		if assignee == "" {
			input.OwnerAgentID = nil
		} else {
			input.OwnerAgentID = &assignee
		}
		hasUpdate = true
	}
	if _, exists := args["title"]; exists {
		return nil, fmt.Errorf("%w: title updates are not implemented", ErrInvalidToolCall)
	}
	if _, exists := args["body"]; exists {
		return nil, fmt.Errorf("%w: body updates are not implemented", ErrInvalidToolCall)
	}
	if _, exists := args["labels"]; exists {
		return nil, fmt.Errorf("%w: label updates are not implemented", ErrInvalidToolCall)
	}
	if !hasUpdate {
		return nil, fmt.Errorf("%w: no mutable fields provided", ErrInvalidToolCall)
	}

	updated, err := s.issues.UpdateIssueWorkTracking(workspaceCtx, input)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Server) resolveIssueByNumber(ctx context.Context, identity Identity, args map[string]any) (*store.ProjectIssue, error) {
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return nil, err
	}
	rawNumber, ok := args["number"]
	if !ok {
		return nil, fmt.Errorf("%w: number is required", ErrInvalidToolCall)
	}
	numberValue, ok := rawNumber.(float64)
	if !ok || numberValue <= 0 || math.Mod(numberValue, 1) != 0 {
		return nil, fmt.Errorf("%w: number must be a positive integer", ErrInvalidToolCall)
	}
	issueNumber := int64(numberValue)

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	issues, err := s.issues.ListIssues(workspaceCtx, store.ProjectIssueFilter{
		ProjectID:   project.ID,
		IssueNumber: &issueNumber,
		Limit:       1,
	})
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("%w: issue not found", ErrInvalidToolCall)
	}
	issue := issues[0]
	if issue.OrgID != identity.OrgID || issue.ProjectID != project.ID {
		return nil, fmt.Errorf("%w: issue belongs to another workspace", ErrInvalidToolCall)
	}
	return &issue, nil
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
