package githubsync

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const defaultRepoCloneRoot = "./data/repos"

type RepoCloneStateStore interface {
	UpdateLocalCloneState(
		ctx context.Context,
		projectID string,
		defaultBranch string,
		localRepoPath string,
	) (*store.ProjectRepoBinding, error)
	SetConflictState(
		ctx context.Context,
		projectID string,
		conflictState string,
		conflictDetails json.RawMessage,
	) (*store.ProjectRepoBinding, error)
}

type EnsureRepoCloneInput struct {
	ProjectID      string
	Repository     string
	DefaultBranch  string
	RepositoryHint string
}

type EnsureRepoCloneResult struct {
	ProjectID     string
	Repository    string
	CloneURL      string
	DefaultBranch string
	RepoPath      string
	Cloned        bool
}

type RepoCloneManager struct {
	RepoRoot     string
	StateStore   RepoCloneStateStore
	runGit       func(ctx context.Context, dir string, args ...string) error
	runGitOutput func(ctx context.Context, dir string, args ...string) (string, error)
}

func NewRepoCloneManager(repoRoot string, stateStore RepoCloneStateStore) *RepoCloneManager {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		repoRoot = defaultRepoCloneRoot
	}
	return &RepoCloneManager{
		RepoRoot:     repoRoot,
		StateStore:   stateStore,
		runGit:       runGitCommand,
		runGitOutput: runGitCommandOutput,
	}
}

func (m *RepoCloneManager) RepoPath(projectID, repository string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if !storeUUIDPattern.MatchString(projectID) {
		return "", fmt.Errorf("invalid project_id")
	}

	repository = strings.TrimSpace(repository)
	if repository == "" {
		return "", fmt.Errorf("repository is required")
	}

	slug := sanitizeRepoSlug(repository)
	if slug == "" {
		slug = "repo"
	}
	hash := sha256.Sum256([]byte(strings.ToLower(projectID + "|" + repository)))
	repoDir := fmt.Sprintf("%s-%x", slug, hash[:6])
	return filepath.Join(m.RepoRoot, repoDir), nil
}

func (m *RepoCloneManager) EnsureLocalClone(
	ctx context.Context,
	input EnsureRepoCloneInput,
) (*EnsureRepoCloneResult, error) {
	if m == nil {
		return nil, fmt.Errorf("repo clone manager is required")
	}

	projectID := strings.TrimSpace(input.ProjectID)
	if !storeUUIDPattern.MatchString(projectID) {
		return nil, fmt.Errorf("invalid project_id")
	}

	defaultBranch := strings.TrimSpace(input.DefaultBranch)
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	cloneURL, repositoryKey, err := resolveRepositoryCloneTarget(input.Repository)
	if err != nil {
		return nil, err
	}

	repoPath, err := m.RepoPath(projectID, repositoryKey)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return nil, fmt.Errorf("create repo root: %w", err)
	}

	cloned, err := m.cloneIfNeeded(ctx, repoPath, cloneURL)
	if err != nil {
		return nil, err
	}

	if err := m.runGit(ctx, repoPath, "fetch", "--prune", "origin"); err != nil {
		return nil, fmt.Errorf("fetch origin: %w", err)
	}

	conflict, err := m.checkoutBranch(ctx, repoPath, defaultBranch)
	if err != nil {
		return nil, err
	}
	if conflict != nil {
		if err := m.persistConflictState(ctx, projectID, defaultBranch, *conflict); err != nil {
			return nil, err
		}
		return nil, &SyncConflictError{
			ProjectID: projectID,
			Branch:    defaultBranch,
			LocalSHA:  conflict.LocalSHA,
			RemoteSHA: conflict.RemoteSHA,
		}
	}

	if m.StateStore != nil {
		if _, err := m.StateStore.SetConflictState(ctx, projectID, store.RepoConflictNone, json.RawMessage(`{}`)); err != nil {
			return nil, fmt.Errorf("clear conflict state: %w", err)
		}
		if _, err := m.StateStore.UpdateLocalCloneState(ctx, projectID, defaultBranch, repoPath); err != nil {
			return nil, fmt.Errorf("persist local clone state: %w", err)
		}
	}

	return &EnsureRepoCloneResult{
		ProjectID:     projectID,
		Repository:    repositoryKey,
		CloneURL:      cloneURL,
		DefaultBranch: defaultBranch,
		RepoPath:      repoPath,
		Cloned:        cloned,
	}, nil
}

func (m *RepoCloneManager) cloneIfNeeded(ctx context.Context, repoPath, cloneURL string) (bool, error) {
	info, err := os.Stat(repoPath)
	if err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("repo path %q exists but is not a directory", repoPath)
		}
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, fmt.Errorf("repo path %q exists but is not a git repository", repoPath)
			}
			return false, fmt.Errorf("verify git repository: %w", err)
		}
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("check repo path: %w", err)
	}

	if err := m.runGit(ctx, "", "clone", cloneURL, repoPath); err != nil {
		return false, fmt.Errorf("clone repository %q: %w", cloneURL, err)
	}
	return true, nil
}

type syncConflictInfo struct {
	LocalSHA  string
	RemoteSHA string
}

type SyncConflictError struct {
	ProjectID string
	Branch    string
	LocalSHA  string
	RemoteSHA string
}

func (e *SyncConflictError) Error() string {
	return fmt.Sprintf(
		"sync conflict detected for project %s on branch %s (local=%s remote=%s)",
		e.ProjectID,
		e.Branch,
		e.LocalSHA,
		e.RemoteSHA,
	)
}

func (m *RepoCloneManager) checkoutBranch(ctx context.Context, repoPath, defaultBranch string) (*syncConflictInfo, error) {
	if err := m.runGit(ctx, repoPath, "checkout", defaultBranch); err != nil {
		if retryErr := m.runGit(ctx, repoPath, "checkout", "-B", defaultBranch, "origin/"+defaultBranch); retryErr != nil {
			return nil, fmt.Errorf("checkout default branch %q: %w", defaultBranch, retryErr)
		}
	}

	if err := m.runGit(ctx, repoPath, "merge", "--ff-only", "origin/"+defaultBranch); err != nil {
		if !isFastForwardConflictError(err) {
			return nil, fmt.Errorf("fast-forward %q to origin: %w", defaultBranch, err)
		}

		localSHA, localErr := m.runGitOutput(ctx, repoPath, "rev-parse", "HEAD")
		if localErr != nil {
			return nil, fmt.Errorf("resolve local sha for conflict: %w", localErr)
		}
		remoteSHA, remoteErr := m.runGitOutput(ctx, repoPath, "rev-parse", "origin/"+defaultBranch)
		if remoteErr != nil {
			return nil, fmt.Errorf("resolve remote sha for conflict: %w", remoteErr)
		}

		return &syncConflictInfo{
			LocalSHA:  strings.TrimSpace(localSHA),
			RemoteSHA: strings.TrimSpace(remoteSHA),
		}, nil
	}
	return nil, nil
}

func isFastForwardConflictError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not possible to fast-forward") ||
		strings.Contains(text, "not possible to fast forward")
}

func (m *RepoCloneManager) persistConflictState(
	ctx context.Context,
	projectID string,
	branch string,
	conflict syncConflictInfo,
) error {
	if m.StateStore == nil {
		return nil
	}

	details, err := json.Marshal(map[string]any{
		"branch":      strings.TrimSpace(branch),
		"local_sha":   strings.TrimSpace(conflict.LocalSHA),
		"remote_sha":  strings.TrimSpace(conflict.RemoteSHA),
		"detected_at": time.Now().UTC(),
		"reason":      "non_fast_forward",
	})
	if err != nil {
		return fmt.Errorf("encode conflict details: %w", err)
	}

	if _, err := m.StateStore.SetConflictState(ctx, projectID, store.RepoConflictNeedsDecision, details); err != nil {
		return fmt.Errorf("persist conflict state: %w", err)
	}
	return nil
}

var ownerRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var slugNormalizer = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func resolveRepositoryCloneTarget(repository string) (cloneURL string, key string, err error) {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return "", "", fmt.Errorf("repository mapping is required")
	}

	if ownerRepoPattern.MatchString(repository) {
		canonical := strings.ToLower(repository)
		return "https://github.com/" + canonical + ".git", canonical, nil
	}

	if strings.HasPrefix(repository, "git@github.com:") {
		trimmed := strings.TrimPrefix(repository, "git@github.com:")
		trimmed = strings.Trim(strings.TrimSuffix(trimmed, ".git"), "/")
		if !ownerRepoPattern.MatchString(trimmed) {
			return "", "", fmt.Errorf("repository mapping %q must include owner/repo", repository)
		}
		canonical := strings.ToLower(trimmed)
		return "git@github.com:" + canonical + ".git", canonical, nil
	}

	if strings.HasPrefix(repository, "file://") {
		localPath := strings.TrimSpace(strings.TrimPrefix(repository, "file://"))
		if localPath == "" {
			return "", "", fmt.Errorf("repository mapping %q has an empty file:// path", repository)
		}
		return repository, filepath.Clean(localPath), nil
	}

	if filepath.IsAbs(repository) {
		cleaned := filepath.Clean(repository)
		return cleaned, cleaned, nil
	}

	parsed, parseErr := url.Parse(repository)
	if parseErr != nil || parsed.Scheme == "" {
		return "", "", fmt.Errorf("repository mapping %q must be owner/repo, a GitHub URL, or file:// path", repository)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		host := strings.ToLower(parsed.Hostname())
		if host != "github.com" && host != "www.github.com" {
			return "", "", fmt.Errorf("repository mapping %q must target github.com", repository)
		}
		repoPath := strings.Trim(strings.TrimSuffix(parsed.EscapedPath(), ".git"), "/")
		repoPath, unescapeErr := url.PathUnescape(repoPath)
		if unescapeErr != nil {
			return "", "", fmt.Errorf("repository mapping %q contains an invalid path", repository)
		}
		if !ownerRepoPattern.MatchString(repoPath) {
			return "", "", fmt.Errorf("repository mapping %q must include owner/repo", repository)
		}
		canonical := strings.ToLower(repoPath)
		return "https://github.com/" + canonical + ".git", canonical, nil
	default:
		return "", "", fmt.Errorf("unsupported repository URL scheme %q", parsed.Scheme)
	}
}

func sanitizeRepoSlug(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.TrimSuffix(normalized, ".git")
	normalized = strings.ReplaceAll(normalized, "git@github.com:", "")
	normalized = strings.ReplaceAll(normalized, "https://github.com/", "")
	normalized = strings.ReplaceAll(normalized, "http://github.com/", "")
	normalized = strings.ReplaceAll(normalized, "www.github.com/", "")
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, ":", "-")
	normalized = strings.ReplaceAll(normalized, string(filepath.Separator), "-")
	normalized = slugNormalizer.ReplaceAllString(normalized, "-")
	return strings.Trim(normalized, "-")
}

func runGitCommand(ctx context.Context, dir string, args ...string) error {
	_, err := runGitCommandOutput(ctx, dir, args...)
	return err
}

func runGitCommandOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, trimmedOutput)
	}
	return strings.TrimSpace(string(output)), nil
}
