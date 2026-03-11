package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/workspace"
)

const (
	defaultWorkspaceEnvironmentName = "workspace"
	defaultWorkspaceTargetBranch    = "main"
)

type RepoBindingEnvironmentRepository interface {
	Create(ctx context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error)
	ListByProject(ctx context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error)
}

func EnsureCanonicalRepoBinding(ctx context.Context, environments RepoBindingEnvironmentRepository, dataDir string, projectRecord repo.Project) (repo.ProjectEnvironment, bool, error) {
	if environments == nil || projectRecord.ID == uuid.Nil {
		return repo.ProjectEnvironment{}, false, nil
	}

	repoPath, err := workspace.ProjectRoot(dataDir, strings.TrimSpace(projectRecord.Slug))
	if err != nil {
		return repo.ProjectEnvironment{}, false, err
	}
	return EnsureRepoBindingAtPath(ctx, environments, projectRecord, repoPath)
}

func EnsureRepoBindingAtPath(ctx context.Context, environments RepoBindingEnvironmentRepository, projectRecord repo.Project, repoPath string) (repo.ProjectEnvironment, bool, error) {
	if environments == nil || projectRecord.ID == uuid.Nil {
		return repo.ProjectEnvironment{}, false, nil
	}
	trimmedRepoPath := strings.TrimSpace(repoPath)
	if trimmedRepoPath == "" {
		return repo.ProjectEnvironment{}, false, nil
	}
	if err := ensureGitWorkspace(trimmedRepoPath); err != nil {
		return repo.ProjectEnvironment{}, false, err
	}

	environmentsByProject, err := environments.ListByProject(ctx, projectRecord.ID)
	if err != nil {
		return repo.ProjectEnvironment{}, false, err
	}
	for _, environment := range environmentsByProject {
		if sameProjectRepoPath(environment.RepoPath, trimmedRepoPath) {
			return environment, false, nil
		}
	}

	created, err := environments.Create(ctx, repo.ProjectEnvironment{
		ProjectID:    projectRecord.ID,
		Name:         nextRepoBindingEnvironmentName(environmentsByProject),
		DeliveryMode: defaultRepoBindingDeliveryMode(projectRecord.DeliveryMode),
		RepoPath:     stringPointer(trimmedRepoPath),
		TargetBranch: defaultWorkspaceTargetBranch,
		IsActive:     true,
	})
	if err != nil {
		return repo.ProjectEnvironment{}, false, err
	}
	return created, true, nil
}

func ensureGitWorkspace(repoPath string) error {
	trimmed := strings.TrimSpace(repoPath)
	if trimmed == "" {
		return nil
	}
	if err := os.MkdirAll(trimmed, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(filepath.Join(trimmed, ".git")); err == nil && info.IsDir() {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	cmd := exec.Command("git", "init", "-b", defaultWorkspaceTargetBranch)
	cmd.Dir = trimmed
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize git repo at %s: %w: %s", trimmed, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func HasProjectRepoBinding(environments []repo.ProjectEnvironment) bool {
	for _, environment := range environments {
		if strings.TrimSpace(pointerValue(environment.RepoPath)) != "" {
			return true
		}
	}
	return false
}

func sameProjectRepoPath(existing *string, want string) bool {
	trimmed := strings.TrimSpace(pointerValue(existing))
	if trimmed == "" {
		return false
	}
	return filepath.Clean(trimmed) == filepath.Clean(strings.TrimSpace(want))
}

func defaultRepoBindingDeliveryMode(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "gated"
	}
	return trimmed
}

func nextRepoBindingEnvironmentName(environments []repo.ProjectEnvironment) string {
	if !projectEnvironmentNameExists(environments, defaultWorkspaceEnvironmentName) {
		return defaultWorkspaceEnvironmentName
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d", defaultWorkspaceEnvironmentName, index)
		if !projectEnvironmentNameExists(environments, candidate) {
			return candidate
		}
	}
}

func projectEnvironmentNameExists(environments []repo.ProjectEnvironment, candidate string) bool {
	trimmedCandidate := strings.TrimSpace(candidate)
	for _, environment := range environments {
		if strings.EqualFold(strings.TrimSpace(environment.Name), trimmedCandidate) {
			return true
		}
	}
	return false
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	out := value
	return &out
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
