package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestGitToolsRead(t *testing.T) {
	repoPath, commits := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_read","arguments":{"project":"otter-camp","path":"README.md","ref":"` + commits["head"] + `"}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	require.Contains(t, payload["content"], "hello")
}

func TestGitToolsTree(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"tree_list","arguments":{"project":"otter-camp","path":"/","recursive":true}}`),
	})
	require.Nil(t, resp.Error)
}

func TestGitToolsCommitList(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"commit_list","arguments":{"project":"otter-camp","limit":2}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	require.Len(t, result.Content, 1)
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	commits, ok := payload["commits"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, commits)
}

func TestGitToolsDiff(t *testing.T) {
	repoPath, commits := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"diff","arguments":{"project":"otter-camp","base":"` + commits["base"] + `","head":"` + commits["head"] + `"}}`),
	})
	require.Nil(t, resp.Error)
}

func TestGitToolsBranchList(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"branch_list","arguments":{"project":"otter-camp"}}`),
	})
	require.Nil(t, resp.Error)

	var result ToolCallResult
	raw, err := json.Marshal(resp.Result)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &result))
	payload, ok := result.Content[0].Data.(map[string]any)
	require.True(t, ok)
	branches, ok := payload["branches"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, branches)
}

func TestGitToolsReadAreWorkspaceScoped(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	path := repoPath
	projectStore := &fakeProjectStore{
		projects: []store.Project{},
		getByID:  map[string]*store.Project{},
		getByName: map[string]*store.Project{
			"otter-camp": {
				ID:            "proj-1",
				OrgID:         "other-org",
				Name:          "otter-camp",
				Status:        "active",
				LocalRepoPath: &path,
			},
		},
	}

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`6`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_read","arguments":{"project":"otter-camp","path":"README.md"}}`),
	})
	require.NotNil(t, resp.Error)
	require.Equal(t, -32602, resp.Error.Code)
}

func TestGitToolsWrite(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`7`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_write","arguments":{"project":"otter-camp","path":"notes/new.txt","content":"new content","message":"add note"}}`),
	})
	require.Nil(t, resp.Error)
	require.Contains(t, string(runGitFixture(t, repoPath, "show", "HEAD:notes/new.txt")), "new content")
}

func TestGitToolsDelete(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "deleteme.txt"), []byte("bye\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "deleteme.txt")
	runGitFixture(t, repoPath, "commit", "-m", "add delete target")

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`8`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_delete","arguments":{"project":"otter-camp","path":"deleteme.txt","message":"remove file"}}`),
	})
	require.Nil(t, resp.Error)

	diff := string(runGitFixture(t, repoPath, "diff", "--name-status", "HEAD~1..HEAD"))
	require.Contains(t, diff, "D\tdeleteme.txt")
}

func TestGitToolsBranchCreate(t *testing.T) {
	repoPath, commits := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`9`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"branch_create","arguments":{"project":"otter-camp","name":"mcp-test-branch","from":"` + commits["base"] + `"}}`),
	})
	require.Nil(t, resp.Error)
	branches := string(runGitFixture(t, repoPath, "branch", "--format=%(refname:short)"))
	require.Contains(t, branches, "mcp-test-branch")
}

func TestValidateGitRef(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		wantErr bool
	}{
		{name: "rejects double-dash option", ref: "--upload-pack=x", wantErr: true},
		{name: "rejects single dash", ref: "-", wantErr: true},
		{name: "rejects exec option", ref: "--exec=x", wantErr: true},
		{name: "accepts branch name", ref: "main"},
		{name: "accepts relative head", ref: "HEAD~3"},
		{name: "accepts tag", ref: "v1.0.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGitRef(tc.ref)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestGitToolsRejectOptionLikeRefs(t *testing.T) {
	repoPath, commits := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	tests := []struct {
		name   string
		params string
	}{
		{
			name:   "file_read rejects option-like ref",
			params: `{"name":"file_read","arguments":{"project":"otter-camp","path":"README.md","ref":"--upload-pack=x"}}`,
		},
		{
			name:   "commit_list rejects dash ref",
			params: `{"name":"commit_list","arguments":{"project":"otter-camp","ref":"-"}}`,
		},
		{
			name:   "diff rejects option-like base and head",
			params: `{"name":"diff","arguments":{"project":"otter-camp","base":"--exec=x","head":"` + commits["head"] + `"}}`,
		},
		{
			name:   "file_write rejects option-like ref",
			params: `{"name":"file_write","arguments":{"project":"otter-camp","path":"notes/x.txt","content":"x","message":"x","ref":"--exec=x"}}`,
		},
		{
			name:   "file_delete rejects option-like ref",
			params: `{"name":"file_delete","arguments":{"project":"otter-camp","path":"README.md","message":"x","ref":"--exec=x"}}`,
		},
	}

	for idx, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
				JSONRPC: "2.0",
				ID:      json.RawMessage(strconv.Itoa(idx + 1)),
				Method:  "tools/call",
				Params:  json.RawMessage(tc.params),
			})
			require.NotNil(t, resp.Error)
			require.Equal(t, -32602, resp.Error.Code)
			require.Contains(t, strings.ToLower(resp.Error.Message), "ref")
		})
	}
}

func TestGitToolsAllowValidRefs(t *testing.T) {
	repoPath, commits := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	runGitFixture(t, repoPath, "tag", "-f", "v1.0.0", commits["head"])

	readmePath := filepath.Join(repoPath, "HEAD3.txt")
	require.NoError(t, os.WriteFile(readmePath, []byte("h1\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "HEAD3.txt")
	runGitFixture(t, repoPath, "commit", "-m", "head3-1")
	require.NoError(t, os.WriteFile(readmePath, []byte("h2\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "HEAD3.txt")
	runGitFixture(t, repoPath, "commit", "-m", "head3-2")
	require.NoError(t, os.WriteFile(readmePath, []byte("h3\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "HEAD3.txt")
	runGitFixture(t, repoPath, "commit", "-m", "head3-3")

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	requests := []rpcRequest{
		{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`100`),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"commit_list","arguments":{"project":"otter-camp","ref":"main","limit":1}}`),
		},
		{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`101`),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"file_read","arguments":{"project":"otter-camp","path":"README.md","ref":"v1.0.0"}}`),
		},
		{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`102`),
			Method:  "tools/call",
			Params:  json.RawMessage(`{"name":"commit_list","arguments":{"project":"otter-camp","ref":"HEAD~3","limit":1}}`),
		},
	}

	for _, req := range requests {
		resp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, req)
		require.Nil(t, resp.Error)
	}
}

func TestGitWriteToolsCreateCommits(t *testing.T) {
	repoPath, _ := setupGitRepoFixture(t)
	projectStore := gitFixtureProjectStore(repoPath)

	s := NewServer(WithProjectStore(projectStore))
	require.NoError(t, RegisterProjectTools(s))
	require.NoError(t, RegisterGitTools(s))

	before := strings.TrimSpace(string(runGitFixture(t, repoPath, "rev-parse", "HEAD")))

	writeResp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`10`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_write","arguments":{"project":"otter-camp","path":"notes/commit-check.txt","content":"commit one","message":"write commit check"}}`),
	})
	require.Nil(t, writeResp.Error)

	afterWrite := strings.TrimSpace(string(runGitFixture(t, repoPath, "rev-parse", "HEAD")))
	require.NotEqual(t, before, afterWrite)

	deleteResp := s.Handle(context.Background(), Identity{OrgID: "org-1", UserID: "user-1"}, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`11`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"file_delete","arguments":{"project":"otter-camp","path":"notes/commit-check.txt","message":"delete commit check"}}`),
	})
	require.Nil(t, deleteResp.Error)

	afterDelete := strings.TrimSpace(string(runGitFixture(t, repoPath, "rev-parse", "HEAD")))
	require.NotEqual(t, afterWrite, afterDelete)
}

func setupGitRepoFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoPath := t.TempDir()
	runGitFixture(t, repoPath, "init", "-b", "main")
	runGitFixture(t, repoPath, "config", "user.name", "Otter MCP")
	runGitFixture(t, repoPath, "config", "user.email", "otter-mcp@example.com")

	readmePath := filepath.Join(repoPath, "README.md")
	require.NoError(t, os.WriteFile(readmePath, []byte("hello\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "README.md")
	runGitFixture(t, repoPath, "commit", "-m", "initial")

	base := strings.TrimSpace(string(runGitFixture(t, repoPath, "rev-parse", "HEAD")))

	docsPath := filepath.Join(repoPath, "docs")
	require.NoError(t, os.MkdirAll(docsPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(docsPath, "a.txt"), []byte("doc\n"), 0o644))
	runGitFixture(t, repoPath, "add", "--", "docs/a.txt")
	runGitFixture(t, repoPath, "commit", "-m", "add docs")

	head := strings.TrimSpace(string(runGitFixture(t, repoPath, "rev-parse", "HEAD")))
	runGitFixture(t, repoPath, "branch", "feature")

	return repoPath, map[string]string{
		"base": base,
		"head": head,
	}
}

func gitFixtureProjectStore(repoPath string) *fakeProjectStore {
	path := repoPath
	return &fakeProjectStore{
		projects: []store.Project{},
		getByID:  map[string]*store.Project{},
		getByName: map[string]*store.Project{
			"otter-camp": {
				ID:            "proj-1",
				OrgID:         "org-1",
				Name:          "otter-camp",
				Status:        "active",
				LocalRepoPath: &path,
			},
		},
	}
}

func runGitFixture(t *testing.T, repoPath string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(output))
	return output
}
