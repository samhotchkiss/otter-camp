package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
)

const defaultGitRepoRoot = "./data/repos"

func gitRepoRoot() string {
	root := strings.TrimSpace(os.Getenv("GIT_REPO_ROOT"))
	if root == "" {
		root = defaultGitRepoRoot
	}
	return filepath.Clean(root)
}

func gitRepoArchiveRoot() string {
	root := gitRepoRoot()
	root = strings.TrimRight(root, string(os.PathSeparator))
	if root == "" {
		return "-archive"
	}
	return root + "-archive"
}

func projectRepoPath(orgID, projectID string) (string, error) {
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return "", fmt.Errorf("invalid project_id")
	}
	return filepath.Join(gitRepoRoot(), orgID, projectID+".git"), nil
}

func projectArchivePath(orgID, projectID string) (string, error) {
	orgID = strings.TrimSpace(orgID)
	if !uuidRegex.MatchString(orgID) {
		return "", fmt.Errorf("invalid org_id")
	}
	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return "", fmt.Errorf("invalid project_id")
	}
	return filepath.Join(gitRepoArchiveRoot(), orgID, projectID+".git"), nil
}

// InitProjectRepo ensures a bare repository exists for the project and records its path.
func (s *ProjectStore) InitProjectRepo(ctx context.Context, projectID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	var orgID string
	var localRepoPath sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT org_id, local_repo_path FROM projects WHERE id = $1`, projectID).Scan(&orgID, &localRepoPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to load project: %w", err)
	}

	if orgID != workspaceID {
		return ErrForbidden
	}

	repoPath, err := projectRepoPath(orgID, projectID)
	if err != nil {
		return err
	}

	if localRepoPath.Valid && strings.TrimSpace(localRepoPath.String) != "" {
		stored := filepath.Clean(strings.TrimSpace(localRepoPath.String))
		if stored != repoPath {
			return fmt.Errorf("project repo path already set")
		}
	}

	if err := ensureBareRepo(ctx, repoPath); err != nil {
		return err
	}

	result, err := conn.ExecContext(ctx, `UPDATE projects SET local_repo_path = $1 WHERE id = $2 AND org_id = $3`, repoPath, projectID, orgID)
	if err != nil {
		return fmt.Errorf("failed to update project repo path: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to update project repo path: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

// GetRepoPath returns the local repository path for the project.
func (s *ProjectStore) GetRepoPath(ctx context.Context, projectID string) (string, error) {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return "", ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return "", fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	var orgID string
	var localRepoPath sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT org_id, local_repo_path FROM projects WHERE id = $1`, projectID).Scan(&orgID, &localRepoPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("failed to load project: %w", err)
	}

	if orgID != workspaceID {
		return "", ErrForbidden
	}

	if localRepoPath.Valid && strings.TrimSpace(localRepoPath.String) != "" {
		return strings.TrimSpace(localRepoPath.String), nil
	}

	if err := s.InitProjectRepo(ctx, projectID); err != nil {
		return "", err
	}

	repoPath, err := projectRepoPath(orgID, projectID)
	if err != nil {
		return "", err
	}
	return repoPath, nil
}

// ArchiveProjectRepo moves a project repository to the archive root.
func (s *ProjectStore) ArchiveProjectRepo(ctx context.Context, projectID string) error {
	workspaceID := middleware.WorkspaceFromContext(ctx)
	if workspaceID == "" {
		return ErrNoWorkspace
	}

	projectID = strings.TrimSpace(projectID)
	if !uuidRegex.MatchString(projectID) {
		return fmt.Errorf("invalid project_id")
	}

	conn, err := WithWorkspace(ctx, s.db)
	if err != nil {
		return err
	}
	defer conn.Close()

	var orgID string
	var localRepoPath sql.NullString
	err = conn.QueryRowContext(ctx, `SELECT org_id, local_repo_path FROM projects WHERE id = $1`, projectID).Scan(&orgID, &localRepoPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to load project: %w", err)
	}

	if orgID != workspaceID {
		return ErrForbidden
	}

	archivePath, err := projectArchivePath(orgID, projectID)
	if err != nil {
		return err
	}

	sourcePath := strings.TrimSpace(localRepoPath.String)
	if sourcePath == "" {
		sourcePath, err = projectRepoPath(orgID, projectID)
		if err != nil {
			return err
		}
	}
	sourcePath = filepath.Clean(sourcePath)

	if filepath.Clean(archivePath) == sourcePath {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return fmt.Errorf("create archive root: %w", err)
	}

	if _, err := os.Stat(sourcePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("project repo does not exist: %w", ErrNotFound)
		}
		return fmt.Errorf("inspect project repo: %w", err)
	}

	if err := os.Rename(sourcePath, archivePath); err != nil {
		return fmt.Errorf("archive project repo: %w", err)
	}

	result, err := conn.ExecContext(ctx, `UPDATE projects SET local_repo_path = $1 WHERE id = $2 AND org_id = $3`, archivePath, projectID, orgID)
	if err != nil {
		return fmt.Errorf("failed to update archived repo path: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to update archived repo path: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}

	return nil
}

func ensureBareRepo(ctx context.Context, repoPath string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("repo path is required")
	}

	info, err := os.Stat(repoPath)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("repo path %q exists but is not a directory", repoPath)
		}
		isBare, err := isBareRepo(repoPath)
		if err != nil {
			return err
		}
		if !isBare {
			return fmt.Errorf("repo path %q exists but is not a bare git repository", repoPath)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check repo path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(repoPath), 0o755); err != nil {
		return fmt.Errorf("create repo root: %w", err)
	}

	if err := initBareRepo(ctx, repoPath); err != nil {
		return err
	}

	return nil
}

func initBareRepo(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, "git", "init", "--bare", repoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("git init --bare %q failed: %w (%s)", repoPath, err, message)
		}
		return fmt.Errorf("git init --bare %q failed: %w", repoPath, err)
	}
	return nil
}

func isBareRepo(repoPath string) (bool, error) {
	headInfo, err := os.Stat(filepath.Join(repoPath, "HEAD"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect HEAD: %w", err)
	}
	if headInfo.IsDir() {
		return false, nil
	}

	objectsInfo, err := os.Stat(filepath.Join(repoPath, "objects"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect objects: %w", err)
	}
	if !objectsInfo.IsDir() {
		return false, nil
	}

	return true, nil
}
