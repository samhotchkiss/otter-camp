package delivery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const defaultDeliveryRemoteName = "origin"
const defaultDeliveryTargetBranch = "main"

type WorkspaceGitServiceOptions struct {
	Pool         *pgxpool.Pool
	Projects     projectRepository
	Environments pushEnvironmentRepository
	DataDir      string
}

type WorkspaceGitService struct {
	projects     projectRepository
	environments pushEnvironmentRepository
	dataDir      string
}

func NewWorkspaceGitService(opts WorkspaceGitServiceOptions) (*WorkspaceGitService, error) {
	projects := opts.Projects
	environments := opts.Environments
	if projects == nil || environments == nil {
		if opts.Pool == nil {
			return nil, fmt.Errorf("workspace git service requires a pool or explicit repositories")
		}
		if projects == nil {
			projects = repo.NewProjectRepo(opts.Pool)
		}
		if environments == nil {
			environments = repo.NewProjectEnvironmentRepo(opts.Pool)
		}
	}
	return &WorkspaceGitService{
		projects:     projects,
		environments: environments,
		dataDir:      strings.TrimSpace(opts.DataDir),
	}, nil
}

func (g *WorkspaceGitService) Merge(ctx context.Context, projectID uuid.UUID, branchName, targetBranch string) error {
	if g == nil {
		return ErrGitServiceNotConfigured
	}
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		return fmt.Errorf("branch name is required")
	}
	trimmedTarget := strings.TrimSpace(targetBranch)
	if trimmedTarget == "" {
		trimmedTarget = deliveryDefaultTargetBranch("")
	}
	repoRoot, _, err := g.projectRepoRootAndBranch(ctx, projectID, nil)
	if err != nil {
		return err
	}
	if err := ensureDeliveryGitWorkspace(ctx, repoRoot); err != nil {
		return err
	}
	if err := ensureDeliveryGitIdentity(ctx, repoRoot); err != nil {
		return err
	}
	if _, err := deliveryGitOutput(ctx, repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+trimmedBranch); err != nil {
		return err
	}
	if err := deliveryCheckoutBranch(ctx, repoRoot, trimmedTarget); err != nil {
		return err
	}
	if strings.EqualFold(trimmedBranch, trimmedTarget) {
		return nil
	}
	targetHasCommit, err := deliveryGitBranchHasCommit(ctx, repoRoot, trimmedTarget)
	if err != nil {
		return err
	}
	if !targetHasCommit {
		return deliverySeedTargetBranchFromSource(ctx, repoRoot, trimmedTarget, trimmedBranch)
	}
	mergeArgs := []string{"merge", "--no-ff", "--no-edit", trimmedBranch}
	if related, err := deliveryGitBranchesShareHistory(ctx, repoRoot, trimmedTarget, trimmedBranch); err != nil {
		return err
	} else if !related {
		mergeArgs = []string{"merge", "--allow-unrelated-histories", "--no-ff", "--no-edit", trimmedBranch}
	}
	if _, err := deliveryGitOutput(ctx, repoRoot, mergeArgs...); err != nil {
		if deliveryGitMergeConflict(err) {
			_, _ = deliveryGitOutput(ctx, repoRoot, "merge", "--abort")
			return ErrMergeConflict
		}
		return err
	}
	return nil
}

func (g *WorkspaceGitService) Push(ctx context.Context, remote repo.ProjectRemote) error {
	if g == nil {
		return ErrGitServiceNotConfigured
	}
	remoteURL := strings.TrimSpace(remote.URL)
	if remote.ProjectID == uuid.Nil || remoteURL == "" {
		return fmt.Errorf("project remote with project_id and url is required")
	}
	repoRoot, targetBranch, err := g.projectRepoRootAndBranch(ctx, remote.ProjectID, &remote.ID)
	if err != nil {
		return err
	}
	if err := ensureDeliveryGitWorkspace(ctx, repoRoot); err != nil {
		return err
	}
	if err := ensureDeliveryGitIdentity(ctx, repoRoot); err != nil {
		return err
	}
	remoteName := strings.TrimSpace(remote.Name)
	if remoteName == "" {
		remoteName = defaultDeliveryRemoteName
	}
	if err := ensureDeliveryGitRemote(ctx, repoRoot, remoteName, remoteURL); err != nil {
		return err
	}
	if err := deliveryCheckoutBranch(ctx, repoRoot, targetBranch); err != nil {
		return err
	}
	_, err = deliveryGitOutput(ctx, repoRoot, "push", "--set-upstream", remoteName, targetBranch)
	return err
}

func (g *WorkspaceGitService) CommitExists(ctx context.Context, projectID uuid.UUID, commitSHA string) (bool, error) {
	if g == nil {
		return false, ErrGitServiceNotConfigured
	}
	trimmedSHA := strings.TrimSpace(commitSHA)
	if projectID == uuid.Nil || trimmedSHA == "" {
		return false, nil
	}
	repoRoot, _, err := g.projectRepoRootAndBranch(ctx, projectID, nil)
	if err != nil {
		return false, err
	}
	if err := ensureDeliveryGitWorkspace(ctx, repoRoot); err != nil {
		return false, err
	}
	if _, err := deliveryGitOutput(ctx, repoRoot, "cat-file", "-e", trimmedSHA+"^{commit}"); err != nil {
		if deliveryGitMissingRef(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (g *WorkspaceGitService) projectRepoRootAndBranch(ctx context.Context, projectID uuid.UUID, remoteID *uuid.UUID) (string, string, error) {
	if g == nil || g.projects == nil {
		return "", "", ErrGitServiceNotConfigured
	}
	targetBranch := deliveryDefaultTargetBranch("")
	if g.environments != nil {
		if repoRoot, branch, ok, err := g.boundRepoRootAndBranch(ctx, projectID, remoteID); err != nil {
			return "", "", err
		} else if ok {
			return repoRoot, branch, nil
		}
	}
	projectRecord, err := g.projects.GetByID(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	repoRoot, err := workspace.ProjectRoot(g.dataDir, strings.TrimSpace(projectRecord.Slug))
	if err != nil {
		return "", "", err
	}
	return repoRoot, targetBranch, nil
}

func (g *WorkspaceGitService) boundRepoRootAndBranch(ctx context.Context, projectID uuid.UUID, remoteID *uuid.UUID) (string, string, bool, error) {
	environments, err := g.environments.ListByProject(ctx, projectID)
	if err != nil {
		return "", "", false, err
	}
	var fallback *repo.ProjectEnvironment
	for i := range environments {
		env := environments[i]
		repoPath := strings.TrimSpace(optionalStringValue(env.RepoPath))
		if repoPath == "" {
			continue
		}
		if fallback == nil && env.IsActive {
			envCopy := env
			fallback = &envCopy
		}
		if remoteID != nil && *remoteID != uuid.Nil && env.RemoteID != nil && *env.RemoteID == *remoteID && env.IsActive {
			return filepath.Clean(repoPath), deliveryDefaultTargetBranch(env.TargetBranch), true, nil
		}
	}
	if fallback != nil {
		return filepath.Clean(strings.TrimSpace(optionalStringValue(fallback.RepoPath))), deliveryDefaultTargetBranch(fallback.TargetBranch), true, nil
	}
	return "", "", false, nil
}

func ensureDeliveryGitWorkspace(ctx context.Context, repoRoot string) error {
	root := filepath.Clean(strings.TrimSpace(repoRoot))
	if root == "" {
		return fmt.Errorf("repo root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := deliveryGitOutput(ctx, root, "init", "-b", deliveryDefaultTargetBranch(""))
	return err
}

func ensureDeliveryGitIdentity(ctx context.Context, repoRoot string) error {
	if email, err := deliveryGitOutput(ctx, repoRoot, "config", "--get", "user.email"); err != nil || strings.TrimSpace(email) == "" {
		if _, err := deliveryGitOutput(ctx, repoRoot, "config", "user.email", "agent@ottercamp.internal"); err != nil {
			return err
		}
	}
	if name, err := deliveryGitOutput(ctx, repoRoot, "config", "--get", "user.name"); err != nil || strings.TrimSpace(name) == "" {
		if _, err := deliveryGitOutput(ctx, repoRoot, "config", "user.name", "OtterCamp Runtime"); err != nil {
			return err
		}
	}
	return nil
}

func deliveryCheckoutBranch(ctx context.Context, repoRoot, branchName string) error {
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		return fmt.Errorf("branch name is required")
	}
	if current, err := deliveryGitOutput(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && strings.TrimSpace(current) == trimmedBranch {
		return nil
	}
	if _, err := deliveryGitOutput(ctx, repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+trimmedBranch); err == nil {
		_, err = deliveryGitOutput(ctx, repoRoot, "checkout", trimmedBranch)
		return err
	}
	_, err := deliveryGitOutput(ctx, repoRoot, "checkout", "-b", trimmedBranch)
	return err
}

func ensureDeliveryGitRemote(ctx context.Context, repoRoot, remoteName, remoteURL string) error {
	trimmedName := strings.TrimSpace(remoteName)
	trimmedURL := strings.TrimSpace(remoteURL)
	if trimmedName == "" || trimmedURL == "" {
		return fmt.Errorf("remote name and url are required")
	}
	currentURL, err := deliveryGitOutput(ctx, repoRoot, "remote", "get-url", trimmedName)
	if err != nil {
		_, err = deliveryGitOutput(ctx, repoRoot, "remote", "add", trimmedName, trimmedURL)
		return err
	}
	if strings.TrimSpace(currentURL) == trimmedURL {
		return nil
	}
	_, err = deliveryGitOutput(ctx, repoRoot, "remote", "set-url", trimmedName, trimmedURL)
	return err
}

func deliveryGitBranchHasCommit(ctx context.Context, repoRoot, branchName string) (bool, error) {
	trimmed := strings.TrimSpace(branchName)
	if trimmed == "" {
		return false, fmt.Errorf("branch name is required")
	}
	if _, err := deliveryGitOutput(ctx, repoRoot, "rev-parse", "--verify", trimmed+"^{commit}"); err != nil {
		if deliveryGitMissingRef(err) || deliveryGitUnknownRevision(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func deliverySeedTargetBranchFromSource(ctx context.Context, repoRoot, targetBranch, sourceBranch string) error {
	_, err := deliveryGitOutput(ctx, repoRoot, "checkout", "-f", "-B", strings.TrimSpace(targetBranch), strings.TrimSpace(sourceBranch))
	return err
}

func deliveryGitBranchesShareHistory(ctx context.Context, repoRoot, targetBranch, sourceBranch string) (bool, error) {
	_, err := deliveryGitOutput(ctx, repoRoot, "merge-base", strings.TrimSpace(targetBranch), strings.TrimSpace(sourceBranch))
	if err == nil {
		return true, nil
	}
	if deliveryGitNoMergeBase(err) {
		return false, nil
	}
	return false, err
}

func deliveryDefaultTargetBranch(branch string) string {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return defaultDeliveryTargetBranch
	}
	return trimmed
}

func deliveryGitMergeConflict(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "merge conflict") ||
		strings.Contains(normalized, "automatic merge failed") ||
		strings.Contains(normalized, "conflict (")
}

func deliveryGitMissingRef(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "not a valid object name") ||
		strings.Contains(normalized, "unknown revision") ||
		strings.Contains(normalized, "bad object")
}

func deliveryGitUnknownRevision(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "ambiguous argument 'head'") ||
		strings.Contains(normalized, "needed a single revision")
}

func deliveryGitNoMergeBase(err error) bool {
	if err == nil {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "no merge base found") ||
		strings.Contains(normalized, "exit status 1")
}

func deliveryGitOutput(ctx context.Context, repoRoot string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = filepath.Clean(strings.TrimSpace(repoRoot))
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

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
