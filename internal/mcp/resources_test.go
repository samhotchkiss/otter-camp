package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestResourcesList(t *testing.T) {
	s := NewServer()
	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/list",
	})
	require.Nil(t, resp.Error)

	var result struct {
		Resources []map[string]any `json:"resources"`
	}
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.NotEmpty(t, result.Resources)
}

func TestResourcesRead(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects: []store.Project{
			{ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"},
		},
		getByID: map[string]*store.Project{},
		getByName: map[string]*store.Project{
			"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"},
		},
	}
	issueStore := &fakeIssueStore{
		issues:            []store.ProjectIssue{{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 1, Title: "Issue", State: "open"}},
		getIssueByID:      map[string]*store.ProjectIssue{},
		createIssueResult: nil,
	}
	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "resources/read",
		Params:  json.RawMessage(`{"uri":"otter://projects"}`),
	})
	require.Nil(t, resp.Error)
}

func TestResourceURIsWorkspaceIsolation(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects: []store.Project{},
		getByID:  map[string]*store.Project{},
		getByName: map[string]*store.Project{
			"otter-camp": {ID: "proj-1", OrgID: "other-org", Name: "otter-camp", Status: "active"},
		},
	}

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "resources/read",
		Params:  json.RawMessage(`{"uri":"otter://projects/otter-camp"}`),
	})
	require.NotNil(t, resp.Error)
	require.Equal(t, -32602, resp.Error.Code)
}
