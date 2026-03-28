package delivery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type workspaceGitProjectRepoStub struct {
	projects map[uuid.UUID]repo.Project
}

func (s *workspaceGitProjectRepoStub) GetByID(_ context.Context, id uuid.UUID) (repo.Project, error) {
	projectRecord, ok := s.projects[id]
	if !ok {
		return repo.Project{}, repo.ErrNotFound
	}
	return projectRecord, nil
}

type workspaceGitEnvironmentRepoStub struct {
	byProject map[uuid.UUID][]repo.ProjectEnvironment
	byID      map[uuid.UUID]repo.ProjectEnvironment
}

func (s *workspaceGitEnvironmentRepoStub) GetByID(_ context.Context, id uuid.UUID) (repo.ProjectEnvironment, error) {
	environment, ok := s.byID[id]
	if !ok {
		return repo.ProjectEnvironment{}, repo.ErrNotFound
	}
	return environment, nil
}

func (s *workspaceGitEnvironmentRepoStub) ListByProject(_ context.Context, projectID uuid.UUID) ([]repo.ProjectEnvironment, error) {
	return append([]repo.ProjectEnvironment(nil), s.byProject[projectID]...), nil
}

func TestWorkspaceGitServiceMergeMergesBranchIntoBoundRepoPath(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "base\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "base")

	workspaceGitCmd(t, ctx, repoRoot, "checkout", "-b", "task/56")
	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "merged output\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "task update")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "main")

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "main",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	if err := service.Merge(ctx, projectID, "task/56", "main"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	got := readWorkspaceGitFile(t, filepath.Join(repoRoot, "content/posts/post.md"))
	if got != "merged output\n" {
		t.Fatalf("merged file content = %q, want %q", got, "merged output\n")
	}
	if branch := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("current branch = %q, want main", branch)
	}
}

func TestWorkspaceGitServiceMergeSeedsEmptyTargetBranchFromTaskBranch(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	workspaceGitCmd(t, ctx, repoRoot, "checkout", "-b", "task/56")
	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "from task branch\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "task output")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "--orphan", "main")

	if err := os.MkdirAll(filepath.Join(repoRoot, ".ottercamp"), 0o755); err != nil {
		t.Fatalf("mkdir .ottercamp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "planning", "prd-spec"), 0o755); err != nil {
		t.Fatalf("mkdir planning/prd-spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, ".ottercamp", "scratch.txt"), []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("write .ottercamp/scratch.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "planning", "prd-spec", "notes.md"), []byte("junk\n"), 0o644); err != nil {
		t.Fatalf("write planning/prd-spec/notes.md: %v", err)
	}

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "main",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	if err := service.Merge(ctx, projectID, "task/56", "main"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	taskSHA := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "task/56")
	mainSHA := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "main")
	if mainSHA != taskSHA {
		t.Fatalf("main sha = %q, want %q", mainSHA, taskSHA)
	}
	if branch := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("current branch = %q, want main", branch)
	}
	got := readWorkspaceGitFile(t, filepath.Join(repoRoot, "content/posts/post.md"))
	if got != "from task branch\n" {
		t.Fatalf("seeded file content = %q, want %q", got, "from task branch\n")
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".ottercamp")); !os.IsNotExist(err) {
		t.Fatalf(".ottercamp exists after seed, err=%v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "planning")); !os.IsNotExist(err) {
		t.Fatalf("planning exists after seed, err=%v, want not exist", err)
	}
}

func TestWorkspaceGitServiceMergeReturnsConflictAndAborts(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "base\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "base")

	workspaceGitCmd(t, ctx, repoRoot, "checkout", "-b", "task/99")
	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "branch change\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "content/posts/post.md")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "branch change")

	workspaceGitCmd(t, ctx, repoRoot, "checkout", "main")
	writeWorkspaceGitFile(t, repoRoot, "content/posts/post.md", "main change\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "content/posts/post.md")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "main change")

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "main",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	if err := service.Merge(ctx, projectID, "task/99", "main"); err != ErrMergeConflict {
		t.Fatalf("Merge err = %v, want %v", err, ErrMergeConflict)
	}
	if status := workspaceGitCmd(t, ctx, repoRoot, "status", "--porcelain"); status != "" {
		t.Fatalf("git status = %q, want empty after merge abort", status)
	}
	if branch := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("current branch = %q, want main", branch)
	}
	got := readWorkspaceGitFile(t, filepath.Join(repoRoot, "content/posts/post.md"))
	if got != "main change\n" {
		t.Fatalf("post-conflict file content = %q, want %q", got, "main change\n")
	}
}

func TestWorkspaceGitServiceMergeAllowsUnrelatedHistories(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	writeWorkspaceGitFile(t, repoRoot, "README.md", "main root\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "main root")

	workspaceGitCmd(t, ctx, repoRoot, "checkout", "--orphan", "task/77")
	workspaceGitCmd(t, ctx, repoRoot, "rm", "-rf", "--cached", ".")
	if err := os.Remove(filepath.Join(repoRoot, "README.md")); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove README: %v", err)
	}
	writeWorkspaceGitFile(t, repoRoot, "content/posts/unrelated.md", "task root\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "task root")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "main")

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "main",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	if err := service.Merge(ctx, projectID, "task/77", "main"); err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if got := readWorkspaceGitFile(t, filepath.Join(repoRoot, "README.md")); got != "main root\n" {
		t.Fatalf("README content = %q, want %q", got, "main root\n")
	}
	if got := readWorkspaceGitFile(t, filepath.Join(repoRoot, "content/posts/unrelated.md")); got != "task root\n" {
		t.Fatalf("task file content = %q, want %q", got, "task root\n")
	}
}

func TestWorkspaceGitServicePushPushesActiveTargetBranch(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	remoteID := uuid.New()
	repoRoot := t.TempDir()
	remoteRoot := filepath.Join(t.TempDir(), "remote.git")
	initWorkspaceGitRepo(t, ctx, repoRoot)
	initWorkspaceGitBareRepo(t, ctx, remoteRoot)

	writeWorkspaceGitFile(t, repoRoot, "README.md", "base\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "base")
	workspaceGitCmd(t, ctx, repoRoot, "checkout", "-b", "release")
	writeWorkspaceGitFile(t, repoRoot, "README.md", "release\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "README.md")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "release")

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RemoteID:     &remoteID,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "release",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	remoteRecord := repo.ProjectRemote{
		ID:        remoteID,
		ProjectID: projectID,
		Name:      "origin",
		URL:       remoteRoot,
	}
	if err := service.Push(ctx, remoteRecord); err != nil {
		t.Fatalf("Push: %v", err)
	}

	localRelease := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "release")
	remoteRelease := workspaceGitCmdWithDir(t, ctx, t.TempDir(), "git", "--git-dir", remoteRoot, "rev-parse", "refs/heads/release")
	if localRelease != remoteRelease {
		t.Fatalf("remote release sha = %q, want %q", remoteRelease, localRelease)
	}
}

func TestWorkspaceGitServiceCommitExistsChecksHistory(t *testing.T) {
	ctx := context.Background()
	projectID := uuid.New()
	repoRoot := t.TempDir()
	initWorkspaceGitRepo(t, ctx, repoRoot)

	writeWorkspaceGitFile(t, repoRoot, "README.md", "base\n")
	workspaceGitCmd(t, ctx, repoRoot, "add", "-A")
	workspaceGitCmd(t, ctx, repoRoot, "commit", "-m", "base")
	headSHA := workspaceGitCmd(t, ctx, repoRoot, "rev-parse", "HEAD")

	service, err := NewWorkspaceGitService(WorkspaceGitServiceOptions{
		Projects: &workspaceGitProjectRepoStub{
			projects: map[uuid.UUID]repo.Project{
				projectID: {ID: projectID, Slug: "sam-blog"},
			},
		},
		Environments: &workspaceGitEnvironmentRepoStub{
			byProject: map[uuid.UUID][]repo.ProjectEnvironment{
				projectID: {{
					ID:           uuid.New(),
					ProjectID:    projectID,
					IsActive:     true,
					RepoPath:     workspaceGitStringPointer(repoRoot),
					TargetBranch: "main",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewWorkspaceGitService: %v", err)
	}

	exists, err := service.CommitExists(ctx, projectID, headSHA)
	if err != nil {
		t.Fatalf("CommitExists(head): %v", err)
	}
	if !exists {
		t.Fatal("CommitExists(head) = false, want true")
	}

	exists, err = service.CommitExists(ctx, projectID, "deadbeef")
	if err != nil {
		t.Fatalf("CommitExists(deadbeef): %v", err)
	}
	if exists {
		t.Fatal("CommitExists(deadbeef) = true, want false")
	}
}

func initWorkspaceGitRepo(t *testing.T, ctx context.Context, repoRoot string) {
	t.Helper()
	workspaceGitCmdWithDir(t, ctx, repoRoot, "git", "init", "-b", "main")
	workspaceGitCmdWithDir(t, ctx, repoRoot, "git", "config", "user.email", "test@example.com")
	workspaceGitCmdWithDir(t, ctx, repoRoot, "git", "config", "user.name", "Workspace Git Test")
}

func initWorkspaceGitBareRepo(t *testing.T, ctx context.Context, repoRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(repoRoot), 0o755); err != nil {
		t.Fatalf("mkdir bare repo parent: %v", err)
	}
	workspaceGitCmdWithDir(t, ctx, filepath.Dir(repoRoot), "git", "init", "--bare", repoRoot)
}

func workspaceGitCmd(t *testing.T, ctx context.Context, repoRoot string, args ...string) string {
	t.Helper()
	return workspaceGitCmdWithDir(t, ctx, repoRoot, "git", args...)
}

func workspaceGitCmdWithDir(t *testing.T, ctx context.Context, dir, binary string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		t.Fatalf("%s %s: %v: %s", binary, strings.Join(args, " "), err, trimmed)
	}
	return trimmed
}

func writeWorkspaceGitFile(t *testing.T, repoRoot, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(repoRoot, relativePath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fullPath, err)
	}
}

func readWorkspaceGitFile(t *testing.T, fullPath string) string {
	t.Helper()
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read %s: %v", fullPath, err)
	}
	return string(data)
}

func workspaceGitStringPointer(value string) *string {
	out := value
	return &out
}
