package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type memoryRecord struct {
	OrgID string
	Key   string
	Value string
}

type workflowRunState struct {
	RunID    string
	Project  string
	Workflow string
	Status   string
}

type promptTemplate struct {
	Name        string
	Description string
	Template    string
	Arguments   []map[string]any
}

func RegisterMemorySearchWorkflowTools(s *Server) error {
	if s == nil {
		return fmt.Errorf("%w: server is required", ErrInvalidToolCall)
	}

	tools := []Tool{
		{
			Name:        "memory_read",
			Description: "Read memory entries by key",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleMemoryReadTool,
		},
		{
			Name:        "memory_write",
			Description: "Write a memory entry",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleMemoryWriteTool,
		},
		{
			Name:        "memory_search",
			Description: "Search memory entries",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleMemorySearchTool,
		},
		{
			Name:        "knowledge_search",
			Description: "Search shared knowledge",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleKnowledgeSearchTool,
		},
		{
			Name:        "search",
			Description: "Search across available scopes",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleSearchTool,
		},
		{
			Name:        "workflow_list",
			Description: "List workflows",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleWorkflowListTool,
		},
		{
			Name:        "workflow_run",
			Description: "Run a workflow",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleWorkflowRunTool,
		},
		{
			Name:        "workflow_status",
			Description: "Get workflow run status",
			InputSchema: map[string]any{"type": "object"},
			Handler:     s.handleWorkflowStatusTool,
		},
	}

	for _, tool := range tools {
		if err := s.RegisterTool(tool); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleMemoryReadTool(_ context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	key := strings.TrimSpace(readOptionalStringArg(args, "key", ""))
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	items := make([]map[string]any, 0)
	for _, entry := range s.memory {
		if entry.OrgID != identity.OrgID {
			continue
		}
		if key != "" && entry.Key != key {
			continue
		}
		items = append(items, map[string]any{"key": entry.Key, "value": entry.Value})
	}
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"items": items, "total": len(items)}}},
	}, nil
}

func (s *Server) handleMemoryWriteTool(_ context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	key := strings.TrimSpace(readOptionalStringArg(args, "key", ""))
	value := strings.TrimSpace(readOptionalStringArg(args, "value", ""))
	if key == "" || value == "" {
		return ToolCallResult{}, fmt.Errorf("%w: key and value are required", ErrInvalidToolCall)
	}
	s.stateMu.Lock()
	s.memory = append(s.memory, memoryRecord{OrgID: identity.OrgID, Key: key, Value: value})
	s.stateMu.Unlock()

	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"key": key, "value": value}}},
	}, nil
}

func (s *Server) handleMemorySearchTool(_ context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	query := strings.ToLower(strings.TrimSpace(readOptionalStringArg(args, "query", "")))
	if query == "" {
		return ToolCallResult{}, fmt.Errorf("%w: query is required", ErrInvalidToolCall)
	}
	limit := 20
	if rawLimit, ok := args["limit"]; ok {
		if limitFloat, ok := rawLimit.(float64); ok {
			limit = int(limitFloat)
		}
	}
	if limit <= 0 {
		limit = 20
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	items := make([]map[string]any, 0)
	for _, entry := range s.memory {
		if entry.OrgID != identity.OrgID {
			continue
		}
		if strings.Contains(strings.ToLower(entry.Key), query) || strings.Contains(strings.ToLower(entry.Value), query) {
			items = append(items, map[string]any{"key": entry.Key, "value": entry.Value})
		}
		if len(items) >= limit {
			break
		}
	}
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"items": items, "total": len(items)}}},
	}, nil
}

func (s *Server) handleKnowledgeSearchTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	return s.handleMemorySearchTool(ctx, identity, args)
}

func (s *Server) handleSearchTool(ctx context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	query := strings.TrimSpace(readOptionalStringArg(args, "query", ""))
	if query == "" {
		return ToolCallResult{}, fmt.Errorf("%w: query is required", ErrInvalidToolCall)
	}
	result, err := s.handleMemorySearchTool(ctx, identity, map[string]any{"query": query, "limit": args["limit"]})
	if err != nil {
		return ToolCallResult{}, err
	}
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"query": query, "scope": readOptionalStringArg(args, "scope", "all"), "results": result.Content[0].Data}}},
	}, nil
}

func (s *Server) handleWorkflowListTool(_ context.Context, _ Identity, args map[string]any) (ToolCallResult, error) {
	project := readOptionalStringArg(args, "project", "")
	workflows := []map[string]any{{"name": "default", "project": project}}
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"workflows": workflows, "total": len(workflows)}}},
	}, nil
}

func (s *Server) handleWorkflowRunTool(_ context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	project := readOptionalStringArg(args, "project", "")
	workflow := readOptionalStringArg(args, "workflow", "")
	if project == "" || workflow == "" {
		return ToolCallResult{}, fmt.Errorf("%w: project and workflow are required", ErrInvalidToolCall)
	}
	runID := fmt.Sprintf("run_%d", time.Now().UnixNano())
	state := workflowRunState{RunID: runID, Project: project, Workflow: workflow, Status: "completed"}
	s.stateMu.Lock()
	s.workflowRuns[identity.OrgID+":"+runID] = state
	s.stateMu.Unlock()
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"runId": runID, "status": state.Status}}},
	}, nil
}

func (s *Server) handleWorkflowStatusTool(_ context.Context, identity Identity, args map[string]any) (ToolCallResult, error) {
	runID := readOptionalStringArg(args, "runId", "")
	if runID == "" {
		return ToolCallResult{}, fmt.Errorf("%w: runId is required", ErrInvalidToolCall)
	}
	s.stateMu.Lock()
	state, ok := s.workflowRuns[identity.OrgID+":"+runID]
	s.stateMu.Unlock()
	if !ok {
		return ToolCallResult{}, fmt.Errorf("%w: workflow run not found", ErrInvalidToolCall)
	}
	return ToolCallResult{
		Content: []ToolContent{{Type: "json", Data: map[string]any{"runId": runID, "status": state.Status}}},
	}, nil
}

func defaultPromptTemplates() map[string]promptTemplate {
	return map[string]promptTemplate{
		"create_spec": {
			Name:        "create_spec",
			Description: "Template for creating a spec",
			Template:    "Create a spec for {{title}} with context {{context}}.",
			Arguments:   []map[string]any{{"name": "title"}, {"name": "context"}},
		},
		"code_review": {
			Name:        "code_review",
			Description: "Template for code review",
			Template:    "Review changes for {{project}} PR {{pr}}.",
			Arguments:   []map[string]any{{"name": "project"}, {"name": "pr"}},
		},
		"daily_summary": {
			Name:        "daily_summary",
			Description: "Template for daily summary",
			Template:    "Summarize progress for {{date}} and {{agent}}.",
			Arguments:   []map[string]any{{"name": "date"}, {"name": "agent"}},
		},
		"issue_triage": {
			Name:        "issue_triage",
			Description: "Template for issue triage",
			Template:    "Triage issues for {{project}}.",
			Arguments:   []map[string]any{{"name": "project"}},
		},
	}
}

func (s *Server) handlePromptsList(req rpcRequest) rpcResponse {
	items := make([]map[string]any, 0, len(s.prompts))
	for _, prompt := range s.prompts {
		items = append(items, map[string]any{
			"name":        prompt.Name,
			"description": prompt.Description,
			"arguments":   prompt.Arguments,
		})
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"prompts": items},
	}
}

func (s *Server) handlePromptsGet(req rpcRequest) rpcResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "invalid params"},
			}
		}
	}
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "name is required"},
		}
	}
	prompt, ok := s.prompts[name]
	if !ok {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "prompt not found"},
		}
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"name":        prompt.Name,
			"description": prompt.Description,
			"template":    prompt.Template,
			"arguments":   prompt.Arguments,
		},
	}
}
