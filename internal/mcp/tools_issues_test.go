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

type fakeIssueStore struct {
	issues                 []store.ProjectIssue
	lastFilter             *store.ProjectIssueFilter
	lastWorkspace          string
	listIssuesErr          error
	getIssueByID           map[string]*store.ProjectIssue
	getIssueByIDErr        error
	lastCreateInput        *store.CreateProjectIssueInput
	createIssueResult      *store.ProjectIssue
	lastUpdateInput        *store.UpdateProjectIssueWorkTrackingInput
	updateIssueResult      *store.ProjectIssue
	lastCreateCommentInput *store.CreateProjectIssueCommentInput
	createCommentResult    *store.ProjectIssueComment
	lastParticipantInput   *store.AddProjectIssueParticipantInput
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

func (f *fakeIssueStore) CreateIssue(ctx context.Context, input store.CreateProjectIssueInput) (*store.ProjectIssue, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastCreateInput = &input
	if f.createIssueResult != nil {
		return f.createIssueResult, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeIssueStore) UpdateIssueWorkTracking(ctx context.Context, input store.UpdateProjectIssueWorkTrackingInput) (*store.ProjectIssue, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastUpdateInput = &input
	if f.updateIssueResult != nil {
		return f.updateIssueResult, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeIssueStore) CreateComment(ctx context.Context, input store.CreateProjectIssueCommentInput) (*store.ProjectIssueComment, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastCreateCommentInput = &input
	if f.createCommentResult != nil {
		return f.createCommentResult, nil
	}
	return nil, store.ErrNotFound
}

func (f *fakeIssueStore) AddParticipant(ctx context.Context, input store.AddProjectIssueParticipantInput) (*store.ProjectIssueParticipant, error) {
	f.lastWorkspace = middleware.WorkspaceFromContext(ctx)
	f.lastParticipantInput = &input
	participant := &store.ProjectIssueParticipant{
		ID:      "participant-1",
		IssueID: input.IssueID,
		AgentID: input.AgentID,
		Role:    input.Role,
	}
	return participant, nil
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

func TestIssueToolsCreate(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues:            []store.ProjectIssue{},
		getIssueByID:      map[string]*store.ProjectIssue{},
		createIssueResult: &store.ProjectIssue{ID: "i-2", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 13, Title: "New issue", State: "open", Priority: "P2", Origin: "local"},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_create","arguments":{"project":"otter-camp","title":"New issue","body":"body","priority":"P2","assignee":"agent-1"}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastCreateInput)
	require.Equal(t, "New issue", issueStore.lastCreateInput.Title)
	require.Equal(t, "org-1", issueStore.lastWorkspace)
}

func TestIssueToolsUpdate(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
		},
		getIssueByID:      map[string]*store.ProjectIssue{},
		updateIssueResult: &store.ProjectIssue{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "closed", Priority: "P0", Origin: "local"},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_update","arguments":{"project":"otter-camp","number":7,"status":"closed","priority":"P0","assignee":"agent-2"}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastUpdateInput)
	require.True(t, issueStore.lastUpdateInput.SetState)
	require.Equal(t, "closed", issueStore.lastUpdateInput.State)
	require.True(t, issueStore.lastUpdateInput.SetPriority)
	require.Equal(t, "P0", issueStore.lastUpdateInput.Priority)
	require.True(t, issueStore.lastUpdateInput.SetOwnerAgentID)
}

func TestIssueToolsClose(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
		},
		getIssueByID:      map[string]*store.ProjectIssue{},
		updateIssueResult: &store.ProjectIssue{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "closed", Priority: "P1", Origin: "local"},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`6`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_close","arguments":{"project":"otter-camp","number":7}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastUpdateInput)
	require.Equal(t, "closed", issueStore.lastUpdateInput.State)
}

func TestIssueToolsReopen(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "closed", Priority: "P1", Origin: "local"},
		},
		getIssueByID:      map[string]*store.ProjectIssue{},
		updateIssueResult: &store.ProjectIssue{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`7`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_reopen","arguments":{"project":"otter-camp","number":7}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastUpdateInput)
	require.Equal(t, "open", issueStore.lastUpdateInput.State)
}

func TestIssueToolsComment(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
		},
		getIssueByID: map[string]*store.ProjectIssue{},
		createCommentResult: &store.ProjectIssueComment{
			ID:            "c-1",
			IssueID:       "i-1",
			AuthorAgentID: "agent-1",
			Body:          "hello",
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`8`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_comment","arguments":{"project":"otter-camp","number":7,"body":"hello"}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastCreateCommentInput)
	require.Equal(t, "i-1", issueStore.lastCreateCommentInput.IssueID)
	require.Equal(t, "agent-1", issueStore.lastCreateCommentInput.AuthorAgentID)
}

func TestIssueToolsAssign(t *testing.T) {
	projectStore := &fakeProjectStore{
		projects:  []store.Project{},
		getByID:   map[string]*store.Project{},
		getByName: map[string]*store.Project{"otter-camp": {ID: "proj-1", OrgID: "org-1", Name: "otter-camp", Status: "active"}},
	}
	issueStore := &fakeIssueStore{
		issues: []store.ProjectIssue{
			{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
		},
		getIssueByID:      map[string]*store.ProjectIssue{},
		updateIssueResult: &store.ProjectIssue{ID: "i-1", OrgID: "org-1", ProjectID: "proj-1", IssueNumber: 7, Title: "Fix MCP", State: "open", Priority: "P1", Origin: "local"},
	}

	s := NewServer(WithProjectStore(projectStore), WithIssueStore(issueStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterIssueTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "agent-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`9`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"issue_assign","arguments":{"project":"otter-camp","number":7,"assignee":"agent-2"}}`),
	})
	require.Nil(t, resp.Error)
	require.NotNil(t, issueStore.lastUpdateInput)
	require.NotNil(t, issueStore.lastParticipantInput)
	require.Equal(t, "agent-2", issueStore.lastParticipantInput.AgentID)
}
