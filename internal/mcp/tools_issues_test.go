package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeIssueStore struct {
	issues          []store.ProjectIssue
	lastFilter      *store.ProjectIssueFilter
	lastWorkspace   string
	listIssuesErr   error
	getIssueByID    map[string]*store.ProjectIssue
	getIssueByIDErr error
}

func (f *fakeIssueStore) ListIssues(ctx context.Context, filter store.ProjectIssueFilter) ([]store.ProjectIssue, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastFilter = &filter
	if f.listIssuesErr != nil {
		return nil, f.listIssuesErr
	}
	return f.issues, nil
}

func (f *fakeIssueStore) GetIssueByID(ctx context.Context, issueID string) (*store.ProjectIssue, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	if f.getIssueByIDErr != nil {
		return nil, f.getIssueByIDErr
	}
	issue, ok := f.getIssueByID[issueID]
	if !ok {
		return nil, store.ErrNotFound
	}
	return issue, nil
}

func TestIssueToolsList(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1"},
		},
		getIssueByID: map[string]*store.ProjectIssue{},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_list","arguments":{"project":"otter-camp","status":"open","priority":"P1","limit":1}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastFilter)
	require.Equal(t, "proj-1", issueStore.lastFilter.ProjectID)
	require.NotNil(t, issueStore.lastFilter.State)
	require.Equal(t, "open", *issueStore.lastFilter.State)
	require.Equal(t, "org-1", issueStore.lastWorkspace)
}

func TestIssueToolsGet(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 12, Title: "Implement issue_get", State: "open", Priority: "P1"},
		},
		getIssueByID: map[string]*store.ProjectIssue{},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_get","arguments":{"project":"otter-camp","number":12}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	issueData, ok := payload["issue"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(12), issueData["number"])
}

func TestIssueToolsWorkspaceIsolation(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "other-org", ProjectID: "proj-1", IssueNumber: 12, Title: "Cross org", State: "open", Priority: "P1"},
		},
		getIssueByID: map[string]*store.ProjectIssue{},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_get","arguments":{"project":"otter-camp","number":12}}`),
	})
	require.NotNil(t, resp.Error)
	require.Equal(t, -32602, resp.Error.Code)
}
