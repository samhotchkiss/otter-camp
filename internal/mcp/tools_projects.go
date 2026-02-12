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
	registerErr = s.RegisterTool(Tool{
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
	if registerErr != nil {
		return registerErr
	}

	registerErr = s.RegisterTool(Tool{
		Name:        "project_create",
		Description: "Create a project",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"name",
			},
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"visibility": map[string]any{
					"type": "string",
					"enum": []string{"public", "private"},
				},
			},
		},
		Handler: s.handleProjectCreateTool,
	})
	if registerErr != nil {
		return registerErr
	}

	return s.RegisterTool(Tool{
		Name:        "project_delete",
		Description: "Delete a project by ID or name",
		InputSchema: map[string]any{
			"type": "object",
			"required": []string{
				"project",
			},
			"properties": map[string]any{
				"project": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleProjectDeleteTool,
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
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
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

func (s *Server) handleProjectCreateTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.projects == nil {
		return ToolCallResult{}, fmt.Errorf("%w: project store unavailable", ErrInvalidToolCall)
	}
	rawName, ok := args["name"]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: name is required", ErrInvalidToolCall)
	}
	name, ok := rawName.(string)
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: name must be a string", ErrInvalidToolCall)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolCallResult{}, fmt.Errorf("%w: name is required", ErrInvalidToolCall)
	}

	var description *string
	if rawDescription, ok := args["description"]; ok {
		descriptionText, ok := rawDescription.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: description must be a string", ErrInvalidToolCall)
		}
		descriptionText = strings.TrimSpace(descriptionText)
		if descriptionText != "" {
			description = &descriptionText
		}
	}
	if rawVisibility, ok := args["visibility"]; ok {
		visibilityText, ok := rawVisibility.(string)
		if !ok {
			return ToolCallResult{}, fmt.Errorf("%w: visibility must be a string", ErrInvalidToolCall)
		}
		visibility := strings.ToLower(strings.TrimSpace(visibilityText))
		if visibility != "" && visibility != "public" && visibility != "private" {
			return ToolCallResult{}, fmt.Errorf("%w: visibility must be public or private", ErrInvalidToolCall)
		}
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	project, err := s.projects.Create(workspaceCtx, store.CreateProjectInput{
		Name:        name,
		Description: description,
		Status:      "active",
	})
	if err != nil {
		return ToolCallResult{}, err
	}
	if project.OrgID != "" && project.OrgID != identity.OrgID {
		return ToolCallResult{}, fmt.Errorf("%w: created project belongs to another workspace", ErrInvalidToolCall)
	}
	s.notifyResourceListChanged()

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

func (s *Server) handleProjectDeleteTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	if s.projects == nil {
		return ToolCallResult{}, fmt.Errorf("%w: project store unavailable", ErrInvalidToolCall)
	}
	project, err := s.resolveProject(ctx, identity, args)
	if err != nil {
		return ToolCallResult{}, err
	}

	workspaceCtx := context.WithValue(ctx, middleware.WorkspaceIDKey, identity.OrgID)
	if err := s.projects.Delete(workspaceCtx, project.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrForbidden) {
			return ToolCallResult{}, fmt.Errorf("%w: project not found", ErrInvalidToolCall)
		}
		return ToolCallResult{}, err
	}
	s.notifyResourceListChanged()

	return ToolCallResult{
		Content: []ToolContent{
			{
				Type: "json",
				Data: map[string]any{
					"deleted": true,
					"project": toProjectPayload(*project),
				},
			},
		},
	}, nil
}

func (s *Server) resolveProject(ctx context.Context, identity Identity, args map[string]any) (*store.Project, error) {
	rawProject, ok := args["project"]
	if !ok {
		return nil, fmt.Errorf("%w: project is required", ErrInvalidToolCall)
	}
	projectRef, ok := rawProject.(string)
	if !ok {
		return nil, fmt.Errorf("%w: project must be a string", ErrInvalidToolCall)
	}
	projectRef = strings.TrimSpace(projectRef)
	if projectRef == "" {
		return nil, fmt.Errorf("%w: project is required", ErrInvalidToolCall)
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
			return nil, fmt.Errorf("%w: project not found", ErrInvalidToolCall)
		}
		return nil, err
	}
	if project.OrgID != identity.OrgID {
		return nil, fmt.Errorf("%w: project belongs to another workspace", ErrInvalidToolCall)
	}
	return project, nil
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
