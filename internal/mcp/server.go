package mcp

import (
	"context"
	"encoding/json"
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
	name    string
	version string
}

func NewServer() *Server {
	return &Server{
		name:    "otter-camp",
		version: "1.0.0",
	}
}

func (s *Server) Handle(_ context.Context, _ Identity, req rpcRequest) rpcResponse {
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
