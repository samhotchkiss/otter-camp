package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeProjectStore struct {
	projects      []store.Project
	getByID       map[string]*store.Project
	getByName     map[string]*store.Project
	lastWorkspace string
}

func (f *fakeProjectStore) List(ctx context.Context) ([]store.Project, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	return f.projects, nil
}

func (f *fakeProjectStore) GetByID(ctx context.Context, id string) (*store.Project, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	if project, ok := f.getByID[id]; ok {
		return project, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeProjectStore) GetByName(ctx context.Context, name string) (*store.Project, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	if project, ok := f.getByName[name]; ok {
		return project, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeProjectStore) Create(context.Context, store.CreateProjectInput) (*store.Project, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeProjectStore) Delete(context.Context, string) error {
	return errors.New("not implemented")
}

func TestProjectToolsList(t *testing.T) {
	desc := "main project"
	projectStore := &fakeProjectStore{
		projects: []store.Project{
			{ID: "p1", OrgID: "org-1", Name: "otter-camp", Description: &desc, Status: "active"},
			{ID: "p2", OrgID: "org-1", Name: "another-project", Status: "active"},
		},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{},
	}

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"project_list","arguments":{"filter":"otter","limit":1}}`),
	})
	require.Nil(t, resp.Error)
	require.Equal(t, "org-1", projectStore.lastWorkspace)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	require.Equal(t, "json", result.Content[0].Type)

	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	projects, ok := payload["projects"].([]any)
	require.True(t, ok)
	require.Len(t, projects, 1)
}

func TestProjectToolsGet(t *testing.T) {
	project := &store.Project{ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{"proj-1": project},
		getByName: map[string]*store.Project{"otter-camp": project},
	}

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"project_get","arguments":{"project":"otter-camp"}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	data, ok := payload["project"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "proj-1", data["id"])
}

func TestProjectToolsWorkspaceIsolation(t *testing.T) {
	project := &store.Project{ID: "proj-1", OrgID: "other-org", Name: "otter-camp", Status: "active"}
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": project},
	}

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"project_get","arguments":{"project":"otter-camp"}}`),
	})
	require.NotNil(t, resp.Error)
	require.Equal(t, -32602, resp.Error.Code)
}
