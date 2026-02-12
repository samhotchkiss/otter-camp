package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolsList(t *testing.T) {
	s := NewServer()
	err := s.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
		Handler: func(_ context.Context, _ Identity, _ map[string]any) (ToolCallResult, error) {
			return ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: "ok"}},
			}, nil
		},
	})
	require.NoError(t, err)

	resp := s.Handle(context.Background(), Identity{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})
	require.Nil(t, resp.Error)

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Tools, 1)
	require.Equal(t, "echo", result.Tools[0].Name)
	require.Equal(t, "Echo text", result.Tools[0].Description)
}

func TestToolsCall(t *testing.T) {
	s := NewServer()
	err := s.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echo text",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ Identity, args map[string]any) (ToolCallResult, error) {
			value, _ := args["value"].(string)
			return ToolCallResult{
				Content: []ToolContent{{Type: "text", Text: value}},
			}, nil
		},
	})
	require.NoError(t, err)

	resp := s.Handle(context.Background(), Identity{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"echo","arguments":{"value":"hello"}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	require.Equal(t, "hello", result.Content[0].Text)

	unknown := s.Handle(context.Background(), Identity{}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"unknown","arguments":{}}`),
	})
	require.NotNil(t, unknown.Error)
	require.Equal(t, -32602, unknown.Error.Code)
}
