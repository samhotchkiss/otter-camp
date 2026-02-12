package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeAgentStore struct {
	agents        []store.Agent
	getByID       map[string]*store.Agent
	getBySlug     map[string]*store.Agent
	lastWorkspace string
}

func (f *fakeAgentStore) List(ctx context.Context) ([]store.Agent, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	return f.agents, nil
}

func (f *fakeAgentStore) GetByID(ctx context.Context, id string) (*store.Agent, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	if agent, ok := f.getByID[id]; ok {
		return agent, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeAgentStore) GetBySlug(ctx context.Context, slug string) (*store.Agent, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	if agent, ok := f.getBySlug[slug]; ok {
		return agent, nil
	}
	return nil, store.ErrNotFound
}

type fakeAgentActivityStore struct {
	events        []store.AgentActivityEvent
	lastWorkspace string
	lastAgentID   string
	lastLimit     int
}

func (f *fakeAgentActivityStore) ListByAgent(ctx context.Context, agentID string, opts store.ListAgentActivityOptions) ([]store.AgentActivityEvent, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastAgentID = agentID
	f.lastLimit = opts.Limit
	return f.events, nil
}

func TestAgentToolsList(t *testing.T) {
	agentStore := &fakeAgentStore{
		agents: []store.Agent{
			{ID: "a-1", OrgID: "org-1", Slug: "otter", DisplayName: "Otter", Status: "online"},
			{ID: "a-2", OrgID: "org-1", Slug: "fox", DisplayName: "Fox", Status: "offline"},
		},
		getByID:   map[string]*store.Agent{},
		getBySlug: map[string]*store.Agent{},
	}
	s := NewServer(WithAgentStore(agentStore))
	require.NoError(t, RegisterAgentTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"agent_list","arguments":{"status":"online"}}`),
	})
	require.Nil(t, resp.Error)
	require.Equal(t, "org-1", agentStore.lastWorkspace)
}

func TestAgentToolsGet(t *testing.T) {
	agent := &store.Agent{ID: "a-1", OrgID: "org-1", Slug: "otter", DisplayName: "Otter", Status: "online"}
	agentStore := &fakeAgentStore{
		agents:    []store.Agent{},
		getByID:   map[string]*store.Agent{"a-1": agent},
		getBySlug: map[string]*store.Agent{"otter": agent},
	}
	s := NewServer(WithAgentStore(agentStore))
	require.NoError(t, RegisterAgentTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"agent_get","arguments":{"agent":"otter"}}`),
	})
	require.Nil(t, resp.Error)
}

func TestAgentToolsActivity(t *testing.T) {
	agent := &store.Agent{ID: "a-1", OrgID: "org-1", Slug: "otter", DisplayName: "Otter", Status: "online"}
	agentStore := &fakeAgentStore{
		agents:    []store.Agent{},
		getByID:   map[string]*store.Agent{"a-1": agent},
		getBySlug: map[string]*store.Agent{"otter": agent},
	}
	activityStore := &fakeAgentActivityStore{
		events: []store.AgentActivityEvent{
			{ID: "e-1", OrgID: "org-1", AgentID: "a-1", Summary: "Did work", Status: "completed", StartedAt: time.Now().UTC()},
		},
	}
	s := NewServer(WithAgentStore(agentStore), WithAgentActivityStore(activityStore))
	require.NoError(t, RegisterAgentTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"agent_activity","arguments":{"agent":"otter","limit":5}}`),
	})
	require.Nil(t, resp.Error)
	require.Equal(t, "org-1", activityStore.lastWorkspace)
	require.Equal(t, "a-1", activityStore.lastAgentID)
	require.Equal(t, 5, activityStore.lastLimit)
}

func TestAgentToolsWhoAmI(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterAgentTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1", Role: "owner"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"whoami","arguments":{}}`),
	})
	require.Nil(t, resp.Error)
}

func TestWhoAmIUsesTokenIdentity(t *testing.T) {
	s := NewServer()
	require.NoError(t, RegisterAgentTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-abc", UserID: "user-xyz", Role: "viewer"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"whoami","arguments":{}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "org-abc", payload["org_id"])
	require.Equal(t, "user-xyz", payload["user_id"])
}
