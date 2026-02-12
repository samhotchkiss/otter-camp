package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func setupGitRepoFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoPath := t.TempDir()
	runGitFixture(t, repoPath, "init")
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
