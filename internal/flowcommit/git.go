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

func CommitEmptyFromBase(ctx context.Context, repoRoot, branchName, baseSHA, message string) (CommitResult, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return CommitResult{}, fmt.Errorf("repo root is required")
	}
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		trimmedBranch = "main"
	}
	if err := ensureGitWorkspace(ctx, root); err != nil {
		return CommitResult{}, err
	}
	if err := ensureIdentity(ctx, root); err != nil {
		return CommitResult{}, err
	}

	trimmedBase := strings.TrimSpace(baseSHA)
	if trimmedBase == "" {
		if err := checkoutBranch(ctx, root, trimmedBranch); err != nil {
			return CommitResult{}, err
		}
		if _, err := gitOutput(ctx, root, "commit", "--allow-empty", "-m", strings.TrimSpace(message)); err != nil {
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
			FilesStaged: 0,
			BranchName:  trimmedBranch,
		}, nil
	}

	treeSHA, err := gitOutput(ctx, root, "rev-parse", trimmedBase+"^{tree}")
	if err != nil {
		return CommitResult{}, err
	}
	sha, err := gitOutput(ctx, root, "commit-tree", strings.TrimSpace(treeSHA), "-p", trimmedBase, "-m", strings.TrimSpace(message))
	if err != nil {
		return CommitResult{}, err
	}
	trimmedSHA := strings.TrimSpace(sha)
	if _, err := gitOutput(ctx, root, "update-ref", "refs/heads/"+trimmedBranch, trimmedSHA); err != nil {
		return CommitResult{}, err
	}
	if err := checkoutBranch(ctx, root, trimmedBranch); err != nil {
		return CommitResult{}, err
	}
	shortSHA := trimmedSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	return CommitResult{
		SHA:         trimmedSHA,
		ShortSHA:    shortSHA,
		FilesStaged: 0,
		BranchName:  trimmedBranch,
	}, nil
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

func RefSHA(ctx context.Context, repoRoot, ref string) (string, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return "", fmt.Errorf("repo root is required")
	}
	trimmedRef := strings.TrimSpace(ref)
	if trimmedRef == "" {
		return "", fmt.Errorf("git ref is required")
	}
	if err := ensureGitWorkspace(ctx, root); err != nil {
		return "", err
	}
	sha, err := gitOutput(ctx, root, "rev-parse", trimmedRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

func CommitAllFromBase(ctx context.Context, repoRoot, branchName, baseSHA, message string, allowEmpty bool) (CommitResult, error) {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return CommitResult{}, fmt.Errorf("repo root is required")
	}
	trimmedBase := strings.TrimSpace(baseSHA)
	if trimmedBase == "" {
		return CommitAll(ctx, repoRoot, branchName, message, allowEmpty)
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
	currentBranch, err := currentBranchName(ctx, root)
	if err == nil && currentBranch != "" && currentBranch != trimmedBranch {
		dirty, dirtyErr := WorktreeDirty(ctx, root)
		if dirtyErr != nil {
			return CommitResult{}, dirtyErr
		}
		if dirty {
			return commitIndexSnapshotFromBase(ctx, root, trimmedBranch, trimmedBase, message, allowEmpty)
		}
	}
	if err := checkoutBranch(ctx, root, trimmedBranch); err != nil {
		return CommitResult{}, err
	}
	if headSHA, err := HeadSHA(ctx, root); err == nil && strings.TrimSpace(headSHA) == trimmedBase {
		return CommitAll(ctx, root, trimmedBranch, message, allowEmpty)
	}
	if _, err := gitOutput(ctx, root, "reset", "--soft", trimmedBase); err != nil {
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

func commitIndexSnapshotFromBase(ctx context.Context, root, branchName, baseSHA, message string, allowEmpty bool) (CommitResult, error) {
	if _, err := gitOutput(ctx, root, "add", "-A"); err != nil {
		return CommitResult{}, err
	}
	filesStaged, err := stagedFileCount(ctx, root)
	if err != nil {
		return CommitResult{}, err
	}
	treeSHA, err := gitOutput(ctx, root, "write-tree")
	if err != nil {
		return CommitResult{}, err
	}
	trimmedTree := strings.TrimSpace(treeSHA)
	trimmedBase := strings.TrimSpace(baseSHA)
	if trimmedBase != "" && !allowEmpty {
		baseTree, treeErr := gitOutput(ctx, root, "rev-parse", trimmedBase+"^{tree}")
		if treeErr != nil {
			return CommitResult{}, treeErr
		}
		if strings.TrimSpace(baseTree) == trimmedTree {
			return CommitResult{}, fmt.Errorf("git commit-tree: nothing to commit")
		}
	}
	args := []string{"commit-tree", trimmedTree}
	if trimmedBase != "" {
		args = append(args, "-p", trimmedBase)
	}
	args = append(args, "-m", strings.TrimSpace(message))
	sha, err := gitOutput(ctx, root, args...)
	if err != nil {
		return CommitResult{}, err
	}
	trimmedSHA := strings.TrimSpace(sha)
	if _, err := gitOutput(ctx, root, "update-ref", "refs/heads/"+branchName, trimmedSHA); err != nil {
		return CommitResult{}, err
	}
	if err := checkoutBranch(ctx, root, branchName); err != nil {
		return CommitResult{}, err
	}
	shortSHA := trimmedSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	return CommitResult{
		SHA:         trimmedSHA,
		ShortSHA:    shortSHA,
		FilesStaged: filesStaged,
		BranchName:  branchName,
	}, nil
}

func ensureGitWorkspace(ctx context.Context, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
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
	current, err := currentBranchName(ctx, root)
	if err == nil && current == branchName {
		return nil
	}
	if _, err := gitOutput(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName); err == nil {
		_, err = gitOutput(ctx, root, "checkout", branchName)
		return err
	}
	_, err = gitOutput(ctx, root, "checkout", "-b", branchName)
	return err
}

func currentBranchName(ctx context.Context, root string) (string, error) {
	current, err := gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(current), nil
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
