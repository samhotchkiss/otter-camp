package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

var errBranchAttachedToMainWorktree = errors.New("branch attached to main worktree")

func (e *NativeToolExecutor) projectWorkspaceRoot(ctx context.Context, projectID uuid.UUID) (string, error) {
	if trimmed := strings.TrimSpace(e.explicitRoot); trimmed != "" {
		return filepath.Clean(trimmed), nil
	}
	if e.environments != nil {
		environments, err := e.environments.ListByProject(ctx, projectID)
		if err != nil {
			return "", err
		}
		if repoPath := activeProjectRepoPathForNative(environments); repoPath != "" {
			return repoPath, nil
		}
	}
	projectRecord, err := e.projects.GetByID(ctx, projectID)
	if err != nil {
		return "", err
	}
	return workspace.ProjectRoot(e.dataDir, projectRecord.Slug)
}

func activeProjectRepoPathForNative(environments []repo.ProjectEnvironment) string {
	fallback := ""
	for _, environment := range environments {
		repoPath := filepath.Clean(strings.TrimSpace(pointerStringValue(environment.RepoPath)))
		if repoPath == "" {
			continue
		}
		if environment.IsActive {
			return repoPath
		}
		if fallback == "" {
			fallback = repoPath
		}
	}
	return fallback
}

func pointerStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (e *NativeToolExecutor) taskWorkspaceRoot(ctx context.Context, taskRecord repo.ProjectTask) (string, error) {
	projectRecord, err := e.projects.GetByID(ctx, taskRecord.ProjectID)
	if err != nil {
		return "", err
	}
	projectRoot, err := e.projectWorkspaceRoot(ctx, taskRecord.ProjectID)
	if err != nil {
		return "", err
	}
	if trimmed := strings.TrimSpace(e.explicitRoot); trimmed != "" {
		return filepath.Clean(trimmed), nil
	}
	branchName := taskBranchName(taskRecord)
	worktreeRoot := filepath.Join(workspace.ResolveDataDir(e.dataDir), "task-worktrees", strings.TrimSpace(projectRecord.Slug), "task-"+strconv.Itoa(taskRecord.TaskNumber))
	if err := ensureTaskWorktree(ctx, projectRoot, worktreeRoot, branchName, "main", e.command); err != nil {
		if errors.Is(err, errBranchAttachedToMainWorktree) {
			if ownsBranch, branchErr := mainWorktreeOwnsBranch(ctx, projectRoot, branchName, e.command); branchErr == nil && ownsBranch {
				return projectRoot, nil
			}
		}
		return "", err
	}
	return worktreeRoot, nil
}

func taskBranchName(taskRecord repo.ProjectTask) string {
	branchName := strings.TrimSpace(taskBranchString(taskRecord.BranchName))
	if branchName == "" {
		branchName = fmt.Sprintf("task/%d", taskRecord.TaskNumber)
	}
	return branchName
}

func ensureTaskWorktree(ctx context.Context, projectRoot, worktreeRoot, branchName, baseBranch string, command commandContextFunc) error {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	worktreeRoot = filepath.Clean(strings.TrimSpace(worktreeRoot))
	branchName = strings.TrimSpace(branchName)
	baseBranch = strings.TrimSpace(baseBranch)
	if projectRoot == "" || worktreeRoot == "" || branchName == "" {
		return fmt.Errorf("project root, worktree root, and branch name are required")
	}
	if command == nil {
		command = exec.CommandContext
	}
	if err := pruneTaskWorktrees(ctx, projectRoot, command); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(worktreeRoot), 0o755); err != nil {
		return err
	}
	if isGitDirOrFile(filepath.Join(worktreeRoot, ".git")) {
		currentBranch, err := gitBranchName(ctx, worktreeRoot, command)
		if err == nil && currentBranch == branchName {
			return nil
		}
		if _, err := runGitCommand(ctx, worktreeRoot, command, "checkout", branchName); err == nil {
			return nil
		}
		if err := removeTaskWorktree(ctx, projectRoot, worktreeRoot, command); err != nil {
			return err
		}
	}

	if err := os.RemoveAll(worktreeRoot); err != nil {
		return err
	}
	if err := removeExistingBranchWorktree(ctx, projectRoot, worktreeRoot, branchName, baseBranch, command); err != nil {
		return err
	}
	if branchExists(ctx, projectRoot, branchName, command) {
		_, err := runGitCommand(ctx, projectRoot, command, "worktree", "add", "--force", worktreeRoot, branchName)
		if isRecoverableWorktreeAddError(err) {
			if removeErr := os.RemoveAll(worktreeRoot); removeErr != nil {
				return removeErr
			}
			_, err = runGitCommand(ctx, projectRoot, command, "worktree", "add", "--force", worktreeRoot, branchName)
		}
		return err
	}

	args := []string{"worktree", "add", "--force", "-b", branchName, worktreeRoot}
	if baseBranch != "" && branchExists(ctx, projectRoot, baseBranch, command) {
		args = append(args, baseBranch)
	} else if !gitHeadExists(ctx, projectRoot, command) {
		args = []string{"worktree", "add", "--force", "--orphan", "-b", branchName, worktreeRoot}
	}
	_, err := runGitCommand(ctx, projectRoot, command, args...)
	if isRecoverableWorktreeAddError(err) {
		if removeErr := os.RemoveAll(worktreeRoot); removeErr != nil {
			return removeErr
		}
		_, err = runGitCommand(ctx, projectRoot, command, args...)
	}
	return err
}

func removeExistingBranchWorktree(ctx context.Context, projectRoot, keepPath, branchName, baseBranch string, command commandContextFunc) error {
	listing, err := runGitCommand(ctx, projectRoot, command, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	keepPath = filepath.Clean(strings.TrimSpace(keepPath))
	needle := "refs/heads/" + strings.TrimSpace(branchName)
	currentPath := ""
	currentBranch := ""
	flush := func() error {
		if currentPath == "" || currentBranch == "" {
			return nil
		}
		if currentBranch != needle {
			return nil
		}
		if sameFilesystemPath(currentPath, keepPath) {
			return nil
		}
		if sameFilesystemPath(currentPath, projectRoot) {
			released, releaseErr := releaseMainWorktreeBranch(ctx, projectRoot, baseBranch, command)
			if releaseErr != nil {
				return releaseErr
			}
			if !released {
				return errBranchAttachedToMainWorktree
			}
			return nil
		}
		return removeTaskWorktree(ctx, projectRoot, currentPath, command)
	}
	for _, line := range strings.Split(listing, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "worktree "):
			if err := flush(); err != nil {
				return err
			}
			currentPath = strings.TrimSpace(strings.TrimPrefix(trimmed, "worktree "))
			currentBranch = ""
		case strings.HasPrefix(trimmed, "branch "):
			currentBranch = strings.TrimSpace(strings.TrimPrefix(trimmed, "branch "))
		case trimmed == "":
			if err := flush(); err != nil {
				return err
			}
			currentPath = ""
			currentBranch = ""
		}
	}
	return flush()
}

func sameFilesystemPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == right {
		return true
	}
	if resolvedLeft, err := filepath.EvalSymlinks(left); err == nil {
		left = filepath.Clean(resolvedLeft)
	}
	if resolvedRight, err := filepath.EvalSymlinks(right); err == nil {
		right = filepath.Clean(resolvedRight)
	}
	return left == right
}

func mainWorktreeOwnsBranch(ctx context.Context, projectRoot, branchName string, command commandContextFunc) (bool, error) {
	currentBranch, err := gitBranchName(ctx, projectRoot, command)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(currentBranch) == strings.TrimSpace(branchName), nil
}

func releaseMainWorktreeBranch(ctx context.Context, projectRoot, baseBranch string, command commandContextFunc) (bool, error) {
	status, err := runGitCommand(ctx, projectRoot, command, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, nil
	}
	for _, line := range strings.Split(status, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		path := strings.TrimSpace(trimmed)
		if len(path) > 3 {
			path = strings.TrimSpace(path[3:])
		}
		path = filepath.ToSlash(path)
		if path == ".ottercamp/worktrees" || strings.HasPrefix(path, ".ottercamp/worktrees/") {
			continue
		}
		return false, nil
	}
	_ = os.RemoveAll(filepath.Join(projectRoot, ".ottercamp", "worktrees"))
	if strings.TrimSpace(baseBranch) == "" || !branchExists(ctx, projectRoot, baseBranch, command) {
		return false, nil
	}
	if _, err := runGitCommand(ctx, projectRoot, command, "checkout", baseBranch); err != nil {
		return false, nil
	}
	return true, nil
}

func branchExists(ctx context.Context, repoRoot, branchName string, command commandContextFunc) bool {
	_, err := runGitCommand(ctx, repoRoot, command, "show-ref", "--verify", "--quiet", "refs/heads/"+strings.TrimSpace(branchName))
	return err == nil
}

func gitBranchName(ctx context.Context, repoRoot string, command commandContextFunc) (string, error) {
	out, err := runGitCommand(ctx, repoRoot, command, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		out, symbolicErr := runGitCommand(ctx, repoRoot, command, "symbolic-ref", "--short", "HEAD")
		if symbolicErr != nil {
			return "", err
		}
		return strings.TrimSpace(out), nil
	}
	return strings.TrimSpace(out), nil
}

func gitHeadExists(ctx context.Context, repoRoot string, command commandContextFunc) bool {
	_, err := runGitCommand(ctx, repoRoot, command, "rev-parse", "--verify", "HEAD")
	return err == nil
}

func removeTaskWorktree(ctx context.Context, projectRoot, worktreeRoot string, command commandContextFunc) error {
	_, err := runGitCommand(ctx, projectRoot, command, "worktree", "remove", "--force", worktreeRoot)
	if isRecoverableWorktreeRemoveError(err) {
		return os.RemoveAll(worktreeRoot)
	}
	return err
}

func isRecoverableWorktreeRemoveError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "is not a working tree") ||
		(strings.Contains(lower, "validation failed, cannot remove working tree") &&
			(strings.Contains(lower, "is not a .git file") ||
				strings.Contains(lower, "not a git repository") ||
				strings.Contains(lower, ".git' does not exist")))
}

func isRecoverableWorktreeAddError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "already exists")
}

func pruneTaskWorktrees(ctx context.Context, projectRoot string, command commandContextFunc) error {
	_, err := runGitCommand(ctx, projectRoot, command, "worktree", "prune", "--expire", "now")
	return err
}

func runGitCommand(ctx context.Context, dir string, command commandContextFunc, args ...string) (string, error) {
	cmd := command(ctx, "git", args...)
	cmd.Dir = dir
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

func isGitDirOrFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}
