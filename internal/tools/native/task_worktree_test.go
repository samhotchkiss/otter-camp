package native

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

func TestIsRecoverableWorktreeRemoveError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "not a working tree",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: '/tmp/task-10' is not a working tree"),
			want: true,
		},
		{
			name: "legacy git file corruption",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' is not a .git file, error code 7"),
			want: true,
		},
		{
			name: "legacy not a git repository",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' not a git repository"),
			want: true,
		},
		{
			name: "missing dot git",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: validation failed, cannot remove working tree: '/tmp/task-10/.git' does not exist"),
			want: true,
		},
		{
			name: "unrelated git error",
			err:  errors.New("git worktree remove --force /tmp/task-10: exit status 128: fatal: branch is currently checked out"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRecoverableWorktreeRemoveError(tc.err); got != tc.want {
				t.Fatalf("isRecoverableWorktreeRemoveError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsRecoverableWorktreeAddError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "path already exists",
			err:  errors.New("git worktree add --force -b task/15 /tmp/task-15: exit status 128: fatal: '/tmp/task-15' already exists"),
			want: true,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "unrelated",
			err:  errors.New("git worktree add --force -b task/15 /tmp/task-15: exit status 128: fatal: not a git repository"),
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isRecoverableWorktreeAddError(tc.err); got != tc.want {
				t.Fatalf("isRecoverableWorktreeAddError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestEnsureTaskWorktreeFailsClosedWhenMainWorktreeOwnsTaskBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	run(repoRoot, "add", "README.md")
	run(repoRoot, "commit", "-m", "base")
	run(repoRoot, "checkout", "-b", "task/10")
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	err := ensureTaskWorktree(ctx, repoRoot, filepath.Join(t.TempDir(), "task-10"), "task/10", "main", exec.CommandContext)
	if !errors.Is(err, errBranchAttachedToMainWorktree) && (err == nil || !strings.Contains(err.Error(), "is a main working tree")) {
		t.Fatalf("ensureTaskWorktree err = %v, want %v or main working tree protection", err, errBranchAttachedToMainWorktree)
	}
}

func TestEnsureTaskWorktreeCreatesOrphanBranchForUnbornRepo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoRoot := t.TempDir()
	worktreeRoot := filepath.Join(t.TempDir(), "task-12")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")

	if err := ensureTaskWorktree(ctx, repoRoot, worktreeRoot, "task/12", "main", exec.CommandContext); err != nil {
		t.Fatalf("ensureTaskWorktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreeRoot, ".git")); err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = worktreeRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git symbolic-ref failed: %v\n%s", err, string(out))
	}
	if got := strings.TrimSpace(string(out)); got != "task/12" {
		t.Fatalf("branch = %q, want task/12", got)
	}
}

func TestTaskWorkspaceRootFallsBackToProjectRootWhenMainWorktreeOwnsTaskBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	run(repoRoot, "add", "README.md")
	run(repoRoot, "commit", "-m", "base")
	run(repoRoot, "checkout", "-b", "task/12")
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	executor := &NativeToolExecutor{
		dataDir: t.TempDir(),
		command: exec.CommandContext,
		projects: &fakeProjectRepo{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "native-shared-task-root"},
			},
		},
		environments: &fakeNativeProjectEnvironmentRepo{
			items: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					Name: "workspace",
					RepoPath: func() *string {
						path := repoRoot
						return &path
					}(),
					IsActive: true,
				}},
			},
		},
	}

	root, err := executor.taskWorkspaceRoot(ctx, repo.ProjectTask{
		ProjectID:  projectID,
		TaskNumber: 12,
	})
	if err != nil {
		t.Fatalf("taskWorkspaceRoot: %v", err)
	}
	if got := filepath.Clean(root); got != filepath.Clean(repoRoot) {
		t.Fatalf("taskWorkspaceRoot = %q, want shared project root %q", got, repoRoot)
	}
}

func TestTaskWorkspaceRootFallsBackToProjectRootWhenMainWorktreeOwnsUnbornTaskBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run(repoRoot, "init", "-b", "main")
	run(repoRoot, "checkout", "-b", "task/12")
	if err := os.WriteFile(filepath.Join(repoRoot, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	executor := &NativeToolExecutor{
		dataDir: t.TempDir(),
		command: exec.CommandContext,
		projects: &fakeProjectRepo{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "native-unborn-shared-task-root"},
			},
		},
		environments: &fakeNativeProjectEnvironmentRepo{
			items: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					Name: "workspace",
					RepoPath: func() *string {
						path := repoRoot
						return &path
					}(),
					IsActive: true,
				}},
			},
		},
	}

	root, err := executor.taskWorkspaceRoot(ctx, repo.ProjectTask{
		ProjectID:  projectID,
		TaskNumber: 12,
	})
	if err != nil {
		t.Fatalf("taskWorkspaceRoot: %v", err)
	}
	if got := filepath.Clean(root); got != filepath.Clean(repoRoot) {
		t.Fatalf("taskWorkspaceRoot = %q, want shared project root %q", got, repoRoot)
	}
}

type fakeNativeProjectEnvironmentRepo struct {
	items map[uuid.UUID][]repo.ProjectEnvironment
}

func (f *fakeNativeProjectEnvironmentRepo) Create(_ context.Context, environment repo.ProjectEnvironment) (repo.ProjectEnvironment, error) {
	return environment, nil
}

func (f *fakeNativeProjectEnvironmentRepo) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error) {
	return append([]repo.ProjectEnvironment(nil), f.items[projectID]...), nil
}
