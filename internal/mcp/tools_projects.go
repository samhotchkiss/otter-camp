package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
)

var projectUUIDRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type ProjectStore interface {
	List(ctx context.Context) ([]store.Project, error)
	GetByID(ctx context.Context, id string) (*store.Project, error)
	GetByName(ctx context.Context, name string) (*store.Project, error)
	Create(ctx context.Context, input store.CreateProjectInput) (*store.Project, error)
	Delete(ctx context.Context, id string) error
}

func RegisterProjectTools(s *Server) error {
	if s == nil {
		return fmt.Errorf("%w: server is required", ErrInvalidToolCall)
	}

	registerErr := s.RegisterTool(Tool{
		Name:        "project_list",
		Description: "List projects in the current workspace",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{"type": "string"},
				"limit":  map[string]any{"type": "number"},
			},
		},
		Handler: s.handleProjectListTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "project_get",
		Description: "Get a project by ID or name",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleProjectGetTool,
	})
}

func (s *Server) handleProjectListTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.projects == nil {
		return ToolCallResult{}, fmt.Errorf("%w: project store unavailable", ErrInvalidToolCall)
	}
	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	projects, err := s.projects.List(workspaceCtx)
	if err != nil {
		return ToolCallResult{}, err
	}

	filter := ""
	if raw, ok := args["filter"]; ok {
		filterText, ok := raw.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: filter must be a string", ErrInvalidToolCall)
		}
		filter = strings.ToLower(strings.TrimSpace(filterText))
	}

	limit := len(projects)
	if raw, ok := args["limit"]; ok {
		value, ok := raw.(float64)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be a number", ErrInvalidToolCall)
		}
		limit = int(value)
		if limit <= 0 {
			return ToolCallResult{}, fmt.Errorf("%w: limit must be positive", ErrInvalidToolCall)
		}
	}

	items := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		if project.OrgID != identity.OrgID {
			continue
		}
		if filter != "" {
			desc := ""
			if project.Description != nil {
				desc = strings.ToLower(strings.TrimSpace(*project.Description))
			}
			name := strings.ToLower(strings.TrimSpace(project.Name))
			if !strings.Contains(name, filter) && !strings.Contains(desc, filter) {
				continue
			}
		}
		items = append(items, toProjectPayload(project))
		if len(items) >= limit {
			break
		}
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"projects": items,
					"total":    len(items),
				},
			},
		},
	}, nil
}

func (s *Server) handleProjectGetTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.projects == nil {
		return ToolCallResult{}, fmt.Errorf("%w: project store unavailable", ErrInvalidToolCall)
	}
	rawProject, ok := args["project"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: project is required", ErrInvalidToolCall)
	}
	projectRef, ok := rawProject.(string)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: project must be a string", ErrInvalidToolCall)
	}
	projectRef = strings.TrimSpace(projectRef)
	if projectRef == "" {
		return ToolCallResult{}, fmt.Errorf("%w: project is required", ErrInvalidToolCall)
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	var (
		project *store.Project
		err     error
	)
	if projectUUIDRegex.MatchString(projectRef) {
		project, err = s.projects.GetByID(workspaceCtx, projectRef)
	} else {
		project, err = s.projects.GetByName(workspaceCtx, projectRef)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
			return ToolCallResult{}, fmt.Errorf("%w: project not found", ErrInvalidToolCall)
		}
		return ToolCallResult{}, err
	}
	if project.OrgID != identity.OrgID {
		return ToolCallResult{}, fmt.Errorf("%w: project belongs to another workspace", ErrInvalidToolCall)
	}

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"project": toProjectPayload(*project),
				},
			},
		},
	}, nil
}

func toProjectPayload(project store.Project) map[string]any {
	payload := map[string]any{
		"id":     project.ID,
		"org_id": project.OrgID,
		"name":   project.Name,
		"status": project.Status,
	}
	if project.Description != nil {
		payload["description"] = *project.Description
	}
	if project.RepoURL != nil {
		payload["repo_url"] = *project.RepoURL
	}
	return payload
}
