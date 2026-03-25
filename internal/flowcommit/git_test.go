package flowcommit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitAllFromBaseCommitsDirtyCrossBranchWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	base, err := CommitAll(ctx, root, "task/13", "base task state", true)
	if err != nil {
		t.Fatalf("base commit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "deliverable.txt"), []byte("task 13 base\n"), 0o644); err != nil {
		t.Fatalf("write base deliverable: %v", err)
	}
	if _, err := CommitAll(ctx, root, "task/13", "seed task 13 deliverable", false); err != nil {
		t.Fatalf("seed task 13 deliverable: %v", err)
	}

	if _, err := gitOutput(ctx, root, "checkout", "-b", "task/14"); err != nil {
		t.Fatalf("checkout task/14: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "deliverable.txt"), []byte("task 14 committed\n"), 0o644); err != nil {
		t.Fatalf("write task 14 deliverable: %v", err)
	}
	if _, err := CommitAll(ctx, root, "task/14", "seed task 14 deliverable", false); err != nil {
		t.Fatalf("seed task 14 deliverable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "deliverable.txt"), []byte("task 13 review content\n"), 0o644); err != nil {
		t.Fatalf("write dirty deliverable: %v", err)
	}

	result, err := CommitAllFromBase(ctx, root, "task/13", base.SHA, "canonical review commit", true)
	if err != nil {
		t.Fatalf("CommitAllFromBase: %v", err)
	}
	if result.BranchName != "task/13" {
		t.Fatalf("branch = %q, want task/13", result.BranchName)
	}

	currentBranch, err := currentBranchName(ctx, root)
	if err != nil {
		t.Fatalf("current branch: %v", err)
	}
	if currentBranch != "task/13" {
		t.Fatalf("current branch = %q, want task/13", currentBranch)
	}

	body, err := os.ReadFile(filepath.Join(root, "deliverable.txt"))
	if err != nil {
		t.Fatalf("read deliverable: %v", err)
	}
	if string(body) != "task 13 review content\n" {
		t.Fatalf("deliverable = %q, want dirty cross-branch content", string(body))
	}

	logOutput, err := exec.Command("git", "-C", root, "log", "-1", "--pretty=%B").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, strings.TrimSpace(string(logOutput)))
	}
	if strings.TrimSpace(string(logOutput)) != "canonical review commit" {
		t.Fatalf("latest commit message = %q, want canonical review commit", strings.TrimSpace(string(logOutput)))
	}
}
