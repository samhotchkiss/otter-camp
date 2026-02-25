package delivery

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

var (
	ErrGitServiceNotConfigured = errors.New("git service is not configured")
	ErrMergeConflict           = errors.New("merge conflict")
)

// GitService defines delivery-facing git operations.
// Concrete git execution is intentionally out of scope for this task.
type GitService interface {
	Merge(ctx context.Context, projectID uuid.UUID, branchName, targetBranch string) error
	Push(ctx context.Context, remote repo.ProjectRemote) error
	CommitExists(ctx context.Context, projectID uuid.UUID, commitSHA string) (bool, error)
}

// UnavailableGitService returns ErrGitServiceNotConfigured for merge/push
// and reports missing commits for validation calls.
type UnavailableGitService struct{}

func (UnavailableGitService) Merge(context.Context, uuid.UUID, string, string) error {
	return ErrGitServiceNotConfigured
}

func (UnavailableGitService) Push(context.Context, repo.ProjectRemote) error {
	return ErrGitServiceNotConfigured
}

func (UnavailableGitService) CommitExists(context.Context, uuid.UUID, string) (bool, error) {
	return false, ErrGitServiceNotConfigured
}

// AllowAllCommitGitService is a minimal fallback for rollback-only paths when
// no real git adapter is wired yet.
type AllowAllCommitGitService struct{}

func (AllowAllCommitGitService) Merge(context.Context, uuid.UUID, string, string) error {
	return ErrGitServiceNotConfigured
}

func (AllowAllCommitGitService) Push(context.Context, repo.ProjectRemote) error {
	return ErrGitServiceNotConfigured
}

func (AllowAllCommitGitService) CommitExists(context.Context, uuid.UUID, string) (bool, error) {
	return true, nil
}
