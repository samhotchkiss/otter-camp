package flowcommit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CommitResult struct {
	SHA         string
	ShortSHA    string
	FilesStaged int
	BranchName  string
}

func CommitAll(ctx context.Context, repoRoot, branchName, message string, allowEmpty bool) (CommitResult, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return CommitResult{}, fmt.Errorf("repo root is required")
	}
	if err := ensureGitWorkspace(ctx, root); err != nil {
		return CommitResult{}, err
	}
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		trimmedBranch = "main"
	}
	if err := ensureIdentity(ctx, root); err != nil {
		return CommitResult{}, err
	}
	if err := checkoutBranch(ctx, root, trimmedBranch); err != nil {
		return CommitResult{}, err
	}
	if _, err := gitOutput(ctx, root, "add", "-A"); err != nil {
		return CommitResult{}, err
	}

	filesStaged, err := stagedFileCount(ctx, root)
	if err != nil {
		return CommitResult{}, err
	}

	args := []string{"commit", "-m", strings.TrimSpace(message)}
	if allowEmpty {
		args = append(args, "--allow-empty")
	}
	if _, err := gitOutput(ctx, root, args...); err != nil {
		return CommitResult{}, err
	}

	sha, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	trimmedSHA := strings.TrimSpace(sha)
	shortSHA := trimmedSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	return CommitResult{
		SHA:         trimmedSHA,
		ShortSHA:    shortSHA,
		FilesStaged: filesStaged,
		BranchName:  trimmedBranch,
	}, nil
}

func WorktreeDirty(ctx context.Context, repoRoot string) (bool, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return false, fmt.Errorf("repo root is required")
	}
	if err := ensureGitWorkspace(ctx, root); err != nil {
		return false, err
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

func HeadSHA(ctx context.Context, repoRoot string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return "", fmt.Errorf("repo root is required")
	}
	if err := ensureGitWorkspace(ctx, root); err != nil {
		return "", err
	}
	sha, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func ensureGitWorkspace(ctx context.Context, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := gitOutput(ctx, root, "init", "-b", "main")
	return err
}

func ensureIdentity(ctx context.Context, root string) error {
	email, err := gitOutput(ctx, root, "config", "--get", "user.email")
	if err != nil || strings.TrimSpace(email) == "" {
		if _, err := gitOutput(ctx, root, "config", "user.email", "agent@ottercamp.internal"); err != nil {
			return err
		}
	}
	name, err := gitOutput(ctx, root, "config", "--get", "user.name")
	if err != nil || strings.TrimSpace(name) == "" {
		if _, err := gitOutput(ctx, root, "config", "user.name", "OtterCamp Runtime"); err != nil {
			return err
		}
	}
	return nil
}

func checkoutBranch(ctx context.Context, root, branchName string) error {
	current, err := gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil && strings.TrimSpace(current) == branchName {
		return nil
	}
	if _, err := gitOutput(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); err == nil {
		_, err = gitOutput(ctx, root, "checkout", branchName)
		return err
	}
	_, err = gitOutput(ctx, root, "checkout", "-b", branchName)
	return err
}

func stagedFileCount(ctx context.Context, root string) (int, error) {
	out, err := gitOutput(ctx, root, "diff", "--cached", "--name-only")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, trimmed)
	}
	return trimmed, nil
}
