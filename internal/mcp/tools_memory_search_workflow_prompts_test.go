package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryTools(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterMemorySearchWorkflowTools(s))
	identity := Identity{OrgID: "org-1", UserID: "user-1"}

	writeResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"memory_write","arguments":{"key":"k1","value":"hello memory"}}`),
	})
	require.Nil(t, writeResp.Error)

	readResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"memory_read","arguments":{"key":"k1"}}`),
	})
	require.Nil(t, readResp.Error)

	searchResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"memory_search","arguments":{"query":"hello"}}`),
	})
	require.Nil(t, searchResp.Error)

	knowledgeResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"knowledge_search","arguments":{"query":"hello"}}`),
	})
	require.Nil(t, knowledgeResp.Error)
}

func TestSearchTools(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterMemorySearchWorkflowTools(s))
	identity := Identity{OrgID: "org-1", UserID: "user-1"}

	_ = s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"memory_write","arguments":{"key":"project","value":"otter camp"}}`),
	})
	resp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`6`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"search","arguments":{"query":"otter","scope":"all"}}`),
	})
	require.Nil(t, resp.Error)
}

func TestWorkflowTools(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterMemorySearchWorkflowTools(s))
	identity := Identity{OrgID: "org-1", UserID: "user-1"}

	listResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`7`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"workflow_list","arguments":{"project":"otter-camp"}}`),
	})
	require.Nil(t, listResp.Error)

	runResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`8`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"workflow_run","arguments":{"project":"otter-camp","workflow":"default"}}`),
	})
	require.Nil(t, runResp.Error)

	var runResult ToolCallResult
	raw, err := json.Marshal(runResp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &runResult))
	payload, ok := runResult.Content[0].Data.(map[string]any)
	require.True(t, ok)
	runID, _ := payload["runId"].(string)
	require.NotEmpty(t, runID)

	statusResp := s.Handle(context.Background(), identity, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`9`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"workflow_status","arguments":{"runId":"` + runID + `"}}`),
	})
	require.Nil(t, statusResp.Error)
}

func TestPrompts(t *testing.T) {
	s := NewServer()

	listResp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`10`),
		Method:  "prompts/list",
	})
	require.Nil(t, listResp.Error)

	getResp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`11`),
		Method:  "prompts/get",
		Params:  json.RawMessage(`{"name":"create_spec"}`),
	})
	require.Nil(t, getResp.Error)
}

func TestMemoryAndKnowledgeSearchWorkspaceIsolation(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterMemorySearchWorkflowTools(s))

	_ = s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`12`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"memory_write","arguments":{"key":"shared","value":"org1 secret"}}`),
	})
	resp := s.Handle(context.Background(), Identity{OrgID: "org-2", UserID: "user-2"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`13`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"knowledge_search","arguments":{"query":"secret"}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	total, ok := payload["total"].(float64)
	require.True(t, ok)
	require.Equal(t, float64(0), total)
}
