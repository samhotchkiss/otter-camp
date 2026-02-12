package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

const protocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Server struct {
	name         string
	version      string
	tools        *ToolRegistry
	projects     ProjectStore
	issues       IssueStore
	agents       AgentStore
	agentEvents  AgentActivityStore
	resourceSubs *resourceSubscriptions
	stateMu      sync.Mutex
	memory       []memoryRecord
	workflowRuns map[string]workflowRunState
	prompts      map[string]promptTemplate
}

type ServerOption func(*Server)

func NewServer(opts ...ServerOption) *Server {
	s := &Server{
		name:         "otter-camp",
		version:      "1.0.0",
		tools:        NewToolRegistry(),
		resourceSubs: newResourceSubscriptions(),
		memory:       make([]memoryRecord, 0),
		workflowRuns: make(map[string]workflowRunState),
		prompts:      defaultPromptTemplates(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func WithProjectStore(projects ProjectStore) ServerOption {
	return func(s *Server) {
		s.projects = projects
	}
}

func WithIssueStore(issues IssueStore) ServerOption {
	return func(s *Server) {
		s.issues = issues
	}
}

func WithAgentStore(agents AgentStore) ServerOption {
	return func(s *Server) {
		s.agents = agents
	}
}

func WithAgentActivityStore(events AgentActivityStore) ServerOption {
	return func(s *Server) {
		s.agentEvents = events
	}
}

func (s *Server) RegisterTool(tool Tool) error {
	return s.tools.Register(tool)
}

func (s *Server) Handle(ctx context.Context, identity Identity, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": true,
					},
					"resources": map[string]any{
						"subscribe":   true,
						"listChanged": true,
					},
					"prompts": map[string]any{
						"listChanged": true,
					},
				},
				"serverInfo": map[string]any{
					"name":    s.name,
					"version": s.version,
				},
			},
		}
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.tools.List(),
			},
		}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return rpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    -32602,
						Message: "invalid params",
					},
				}
			}
		}
		if params.Name == "" {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    -32602,
					Message: "tool name is required",
				},
			}
		}
		arguments := map[string]any{}
		if len(params.Arguments) > 0 && string(params.Arguments) != "null" {
			if err := json.Unmarshal(params.Arguments, &arguments); err != nil {
				return rpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &rpcError{
						Code:    -32602,
						Message: "tool arguments must be an object",
					},
				}
			}
		}

		result, err := s.tools.Call(ctx, identity, params.Name, arguments)
		if err != nil {
			code := -32000
			msg := "tool execution failed"
			if errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrInvalidToolCall) {
				code = -32602
				msg = err.Error()
			}
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &rpcError{
					Code:    code,
					Message: msg,
				},
			}
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}
	case "resources/list":
		resp := s.handleResourcesList(ctx, identity)
		resp.ID = req.ID
		return resp
	case "resources/read":
		return s.handleResourcesRead(ctx, identity, req)
	case "resources/subscribe":
		return s.handleResourcesSubscribe(identity, req)
	case "resources/unsubscribe":
		return s.handleResourcesUnsubscribe(identity, req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32601,
				Message: "method not found",
			},
		}
	}
}
