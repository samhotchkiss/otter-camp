package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

func (s *Server) handleResourcesList(_ context.Context, _ Identity) rpcResponse {
	resources := []map[string]any{
		{"uri": "otter://projects", "name": "Projects"},
		{"uriTemplate": "otter://projects/{name}", "name": "Project details"},
		{"uriTemplate": "otter://projects/{name}/issues", "name": "Project issues"},
		{"uriTemplate": "otter://projects/{name}/tree", "name": "Project tree"},
		{"uriTemplate": "otter://projects/{name}/files/{path}", "name": "Project file"},
	}
	return rpcResponse{
		JSONRPC: "2.0",
		Result: map[string]any{
			"resources": resources,
		},
	}
}

func (s *Server) handleResourcesRead(ctx context.Context, identity Identity, req rpcRequest) rpcResponse {
	var params struct {
		URI string `json:"uri"`
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
	params.URI = strings.TrimSpace(params.URI)
	if params.URI == "" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32602,
				Message: "uri is required",
			},
		}
	}

	content, err := s.readResourceURI(ctx, identity, params.URI)
	if err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32602,
				Message: err.Error(),
			},
		}
	}

	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"contents": []map[string]any{
				{
					"uri":      params.URI,
					"mimeType": "application/json",
					"text":     content,
				},
			},
		},
	}
}

func (s *Server) handleResourcesSubscribe(identity Identity, req rpcRequest) rpcResponse {
	var params struct {
		URI string `json:"uri"`
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
	params.URI = strings.TrimSpace(params.URI)
	if params.URI == "" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32602,
				Message: "uri is required",
			},
		}
	}
	s.resourceSubs.subscribe(resourceSubscriberID(identity), params.URI)
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"subscribed": true,
			"uri":        params.URI,
		},
	}
}

func (s *Server) handleResourcesUnsubscribe(identity Identity, req rpcRequest) rpcResponse {
	var params struct {
		URI string `json:"uri"`
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
	params.URI = strings.TrimSpace(params.URI)
	if params.URI == "" {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &rpcError{
				Code:    -32602,
				Message: "uri is required",
			},
		}
	}
	s.resourceSubs.unsubscribe(resourceSubscriberID(identity), params.URI)
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"unsubscribed": true,
			"uri":          params.URI,
		},
	}
}

func (s *Server) readResourceURI(ctx context.Context, identity Identity, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("%w: invalid uri", ErrInvalidToolCall)
	}
	if parsed.Scheme != "otter" {
		return "", fmt.Errorf("%w: unsupported scheme", ErrInvalidToolCall)
	}

	switch parsed.Host {
	case "projects":
		segments := splitResourceSegments(parsed.Path)
		switch {
		case len(segments) == 0:
			result, err := s.handleProjectListTool(ctx, identity, map[string]any{})
			if err != nil {
				return "", err
			}
			return marshalResourceData(result)
		case len(segments) == 1:
			result, err := s.handleProjectGetTool(ctx, identity, map[string]any{"project": segments[0]})
			if err != nil {
				return "", err
			}
			return marshalResourceData(result)
		case len(segments) == 2 && segments[1] == "issues":
			result, err := s.handleIssueListTool(ctx, identity, map[string]any{"project": segments[0], "status": "open"})
			if err != nil {
				return "", err
			}
			return marshalResourceData(result)
		case len(segments) == 2 && segments[1] == "tree":
			result, err := s.handleTreeListTool(ctx, identity, map[string]any{"project": segments[0], "path": "/", "recursive": false})
			if err != nil {
				return "", err
			}
			return marshalResourceData(result)
		case len(segments) >= 3 && segments[1] == "files":
			filePath := strings.Join(segments[2:], "/")
			result, err := s.handleFileReadTool(ctx, identity, map[string]any{"project": segments[0], "path": filePath})
			if err != nil {
				return "", err
			}
			return marshalResourceData(result)
		default:
			return "", fmt.Errorf("%w: unsupported resource uri", ErrInvalidToolCall)
		}
	default:
		return "", fmt.Errorf("%w: unsupported resource host", ErrInvalidToolCall)
	}
}

func splitResourceSegments(pathValue string) []string {
	trimmed := strings.Trim(pathValue, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, strings.TrimSpace(part))
		}
	}
	return out
}

func marshalResourceData(result ToolCallResult) (string, error) {
	if len(result.Content) == 0 {
		return "{}", nil
	}
	data := result.Content[0].Data
	if data == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("%w: failed to encode resource", ErrInvalidToolCall)
	}
	return string(raw), nil
}

func (s *Server) notifyResourceListChanged() {
	if s.resourceSubs == nil {
		return
	}
	s.resourceSubs.notify()
}

func resourceSubscriberID(identity Identity) string {
	return identity.OrgID + ":" + identity.UserID
}
