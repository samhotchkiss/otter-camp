//go:build integration

package native

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
)

func TestIntegrationFileReadRoundTripAndTraversal(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	out, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "hello.txt"})
	if err != nil {
		t.Fatalf("file.read: %v", err)
	}
	if out["content"] != "hello world" {
		t.Fatalf("content = %v, want hello world", out["content"])
	}

	escaped, err := executor.Execute(integrationExecCtx(), "file.read", map[string]any{"path": "../../../etc/passwd"})
	if err != nil {
		t.Fatalf("file.read escaped: %v", err)
	}
	if escaped["error"] != "path_traversal" {
		t.Fatalf("escaped error = %v, want path_traversal", escaped["error"])
	}
}

func TestIntegrationFileSearchDirectoryTree(t *testing.T) {
	root := t.TempDir()
	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("alpha\nneedle\nomega\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "b.txt"), []byte("beta\nneedle\ngamma\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "c.txt"), []byte("no match\n"), 0o644); err != nil {
		t.Fatalf("write c.txt: %v", err)
	}

	out, err := executor.Execute(integrationExecCtx(), "file.search", map[string]any{
		"path":         "docs",
		"pattern":      "needle",
		"file_pattern": "*.txt",
	})
	if err != nil {
		t.Fatalf("file.search: %v", err)
	}
	matches, _ := out["matches"].([]map[string]any)
	if len(matches) != 2 {
		t.Fatalf("matches length = %d, want 2", len(matches))
	}
}

func TestIntegrationGitStatusRealRepo(t *testing.T) {
	repoDir := t.TempDir()
	mustRunGit(t, repoDir, "init")

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: repoDir})
	clean, err := executor.Execute(integrationExecCtx(), "git.status", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("clean git.status: %v", err)
	}
	branch, _ := clean["branch"].(string)
	if branch == "" {
		t.Fatalf("branch should not be empty")
	}
	if clean["is_dirty"] != false {
		t.Fatalf("is_dirty = %v, want false", clean["is_dirty"])
	}

	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	dirty, err := executor.Execute(integrationExecCtx(), "git.status", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("dirty git.status: %v", err)
	}
	if dirty["is_dirty"] != true {
		t.Fatalf("is_dirty = %v, want true", dirty["is_dirty"])
	}
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(output))
	}
}

func integrationExecCtx() context.Context {
	orgID := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	agentID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}
