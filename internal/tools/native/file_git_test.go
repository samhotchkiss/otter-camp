package native

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/mcp"
)

func TestFileReadTruncation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 300)), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.read", map[string]any{
		"path":      "large.txt",
		"max_bytes": 128,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true (output=%v)", out["truncated"], out)
	}
	content, _ := out["content"].(string)
	if len(content) != 128 {
		t.Fatalf("content length = %d, want 128", len(content))
	}
}

func TestFileReadSymlinkEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsidePath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkPath := filepath.Join(root, "escape.txt")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.read", map[string]any{"path": "escape.txt"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := out["error"].(string); got != "path_traversal" {
		t.Fatalf("error = %q, want path_traversal", got)
	}
}

func TestFileListPatternFilter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bar.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write bar.txt: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.list", map[string]any{"pattern": "*.go"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	entries, _ := out["entries"].([]map[string]any)
	if len(entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(entries))
	}
	if entries[0]["name"] != "foo.go" {
		t.Fatalf("entry name = %v, want foo.go", entries[0]["name"])
	}
}

func TestFileListRecursiveTruncatesAt1000(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 1005; i++ {
		name := filepath.Join(root, fmt.Sprintf("f-%04d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.list", map[string]any{"recursive": true})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	entries, _ := out["entries"].([]map[string]any)
	if len(entries) != defaultListMaxEntries {
		t.Fatalf("entries length = %d, want %d", len(entries), defaultListMaxEntries)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
}

func TestFileSearchContextLines(t *testing.T) {
	root := t.TempDir()
	content := strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"target line",
		"line 6",
		"line 7",
		"line 8",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.search", map[string]any{
		"pattern": "target",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	matches, _ := out["matches"].([]map[string]any)
	if len(matches) != 1 {
		t.Fatalf("matches length = %d, want 1 (output=%v)", len(matches), out)
	}
	before, _ := matches[0]["context_before"].([]string)
	after, _ := matches[0]["context_after"].([]string)
	if strings.Join(before, ",") != "line 3,line 4" {
		t.Fatalf("before context = %v, want [line 3 line 4]", before)
	}
	if strings.Join(after, ",") != "line 6,line 7" {
		t.Fatalf("after context = %v, want [line 6 line 7]", after)
	}
}

func TestFileSearchHardCap500(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		lines = append(lines, fmt.Sprintf("needle %d", i))
	}
	if err := os.WriteFile(filepath.Join(root, "many.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("write many.txt: %v", err)
	}

	executor := NewExecutor(ExecutorOptions{WorkspaceRoot: root})
	out, err := executor.Execute(testExecCtx(), "file.search", map[string]any{
		"pattern":     "needle",
		"max_results": 600,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	matches, _ := out["matches"].([]map[string]any)
	if len(matches) != hardSearchMaxResult {
		t.Fatalf("matches length = %d, want %d", len(matches), hardSearchMaxResult)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
}

func TestGitStatusParsesPorcelainOutput(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) > 0 && args[0] == "status" {
				return helperCommand(ctx, "git-status", nil)
			}
			t.Fatalf("unexpected git args: %v", args)
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["branch"] != "main" {
		t.Fatalf("branch = %v, want main", out["branch"])
	}
	if out["ahead"] != 2 || out["behind"] != 1 {
		t.Fatalf("ahead/behind = %v/%v, want 2/1", out["ahead"], out["behind"])
	}
	files, _ := out["files"].([]map[string]any)
	if len(files) != 2 {
		t.Fatalf("files length = %d, want 2", len(files))
	}
	if files[0]["status"] != "M" || files[1]["status"] != "?" {
		t.Fatalf("parsed statuses = %v", files)
	}
}

func TestGitStatusNotRepoReturnsPayloadError(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return helperCommand(ctx, "not-git", nil)
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.status", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := out["error"].(string); got != "not_a_git_repo" {
		t.Fatalf("error = %q, want not_a_git_repo", got)
	}
}

func TestGitDiffTruncatesOutput(t *testing.T) {
	env := map[string]string{"NATIVE_TEST_DIFF_SIZE": strconv.Itoa(diffOutputMaxBytes + 4096)}
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if containsArg(args, "--numstat") {
				return helperCommand(ctx, "git-numstat", nil)
			}
			return helperCommand(ctx, "git-diff", env)
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.diff", map[string]any{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out["truncated"] != true {
		t.Fatalf("truncated = %v, want true", out["truncated"])
	}
	diffText, _ := out["diff"].(string)
	if len(diffText) != diffOutputMaxBytes {
		t.Fatalf("diff length = %d, want %d", len(diffText), diffOutputMaxBytes)
	}
}

func TestGitLogLimitAtMostFive(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{
		WorkspaceRoot: t.TempDir(),
		Command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if len(args) > 0 && args[0] == "log" {
				return helperCommand(ctx, "git-log", map[string]string{"NATIVE_TEST_LOG_LINES": "10"})
			}
			t.Fatalf("unexpected git args: %v", args)
			return nil
		},
	})

	out, err := executor.Execute(testExecCtx(), "git.log", map[string]any{"limit": 5})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	commits, _ := out["commits"].([]map[string]any)
	if len(commits) > 5 {
		t.Fatalf("commits length = %d, want at most 5", len(commits))
	}
}

func containsArg(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func helperCommand(ctx context.Context, mode string, extraEnv map[string]string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--", mode)
	env := append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	for key, value := range extraEnv {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.Env = env
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	mode := ""
	for i := range args {
		if args[i] == "--" && i+1 < len(args) {
			mode = args[i+1]
			break
		}
	}

	switch mode {
	case "git-status":
		fmt.Fprint(os.Stdout, "## main...origin/main [ahead 2, behind 1]\n M app.go\n?? new.txt\n")
		os.Exit(0)
	case "git-branch-main":
		fmt.Fprint(os.Stdout, "main\n")
		os.Exit(0)
	case "git-rev-list-2":
		fmt.Fprint(os.Stdout, "2\n")
		os.Exit(0)
	case "git-push-ok":
		fmt.Fprint(os.Stdout, "ok\n")
		os.Exit(0)
	case "not-git":
		fmt.Fprint(os.Stdout, "fatal: not a git repository\n")
		os.Exit(128)
	case "git-diff":
		size, _ := strconv.Atoi(os.Getenv("NATIVE_TEST_DIFF_SIZE"))
		if size <= 0 {
			size = diffOutputMaxBytes + 1024
		}
		fmt.Fprint(os.Stdout, strings.Repeat("d", size))
		os.Exit(0)
	case "git-numstat":
		fmt.Fprint(os.Stdout, "5\t3\tmain.go\n")
		os.Exit(0)
	case "git-log":
		total, _ := strconv.Atoi(os.Getenv("NATIVE_TEST_LOG_LINES"))
		if total <= 0 {
			total = 10
		}
		lines := make([]string, 0, total)
		for i := 0; i < total; i++ {
			lines = append(lines, fmt.Sprintf("0000000000000000000000000000000000000%02d\x1fTest User\x1ftest@example.com\x1f2026-01-01T00:00:00Z\x1fCommit %02d", i, i))
		}
		fmt.Fprint(os.Stdout, strings.Join(lines, "\n"))
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode: %s", mode)
		os.Exit(2)
	}
}

func testExecCtx() context.Context {
	orgID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	agentID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	return mcp.WithExecutionContext(context.Background(), mcp.ExecutionContext{
		OrganizationID: orgID,
		AgentID:        &agentID,
	})
}
