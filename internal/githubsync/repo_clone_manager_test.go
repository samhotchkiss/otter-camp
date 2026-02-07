package githubsync

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeRepoCloneStateStore struct {
	updates         []fakeRepoCloneStateUpdate
	conflictUpdates []fakeConflictStateUpdate
}

type fakeRepoCloneStateUpdate struct {
	ProjectID     string
	DefaultBranch string
	LocalRepoPath string
}

type fakeConflictStateUpdate struct {
	ProjectID      string
	ConflictState  string
	ConflictDetail json.RawMessage
}

func (f *fakeRepoCloneStateStore) UpdateLocalCloneState(
	_ context.Context,
	projectID string,
	defaultBranch string,
	localRepoPath string,
) (*store.ProjectRepoBinding, error) {
	f.updates = append(f.updates, fakeRepoCloneStateUpdate{
		ProjectID:     projectID,
		DefaultBranch: defaultBranch,
		LocalRepoPath: localRepoPath,
	})
	return &store.ProjectRepoBinding{
		ProjectID:     projectID,
		DefaultBranch: defaultBranch,
		LocalRepoPath: stringPtr(localRepoPath),
	}, nil
}

func (f *fakeRepoCloneStateStore) SetConflictState(
	_ context.Context,
	projectID string,
	conflictState string,
	conflictDetails json.RawMessage,
) (*store.ProjectRepoBinding, error) {
	f.conflictUpdates = append(f.conflictUpdates, fakeConflictStateUpdate{
		ProjectID:      projectID,
		ConflictState:  conflictState,
		ConflictDetail: conflictDetails,
	})
	return &store.ProjectRepoBinding{
		ProjectID:       projectID,
		ConflictState:   conflictState,
		ConflictDetails: conflictDetails,
	}, nil
}

func TestRepoCloneManagerRepoPathIsStableAndCollisionSafe(t *testing.T) {
	manager := NewRepoCloneManager("/tmp/repos", nil)
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	first, err := manager.RepoPath(projectID, "samhotchkiss/otter-camp")
	require.NoError(t, err)
	second, err := manager.RepoPath(projectID, "samhotchkiss/otter-camp")
	require.NoError(t, err)
	require.Equal(t, first, second)

	// These normalize to similar slugs, but hashed suffix keeps paths distinct.
	collisionA, err := manager.RepoPath(projectID, "owner/foo-bar")
	require.NoError(t, err)
	collisionB, err := manager.RepoPath(projectID, "owner/foo_bar")
	require.NoError(t, err)
	require.NotEqual(t, collisionA, collisionB)
}

func TestRepoCloneManagerInitialCloneAndStatePersistence(t *testing.T) {
	remote := newTestGitRemote(t)
	initialSHA := remote.CommitAndPush(t, "README.md", "initial\n", "initial commit")

	stateStore := &fakeRepoCloneStateStore{}
	manager := NewRepoCloneManager(t.TempDir(), stateStore)
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	result, err := manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     projectID,
		Repository:    "file://" + remote.RemotePath,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	require.True(t, result.Cloned)
	require.DirExists(t, filepath.Join(result.RepoPath, ".git"))

	localSHA := runGitOutput(t, result.RepoPath, "rev-parse", "HEAD")
	require.Equal(t, initialSHA, localSHA)

	require.Len(t, stateStore.updates, 1)
	require.Equal(t, projectID, stateStore.updates[0].ProjectID)
	require.Equal(t, "main", stateStore.updates[0].DefaultBranch)
	require.Equal(t, result.RepoPath, stateStore.updates[0].LocalRepoPath)
}

func TestRepoCloneManagerFetchesUpdatesWithoutReclone(t *testing.T) {
	remote := newTestGitRemote(t)
	_ = remote.CommitAndPush(t, "README.md", "initial\n", "initial commit")

	stateStore := &fakeRepoCloneStateStore{}
	manager := NewRepoCloneManager(t.TempDir(), stateStore)
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	first, err := manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     projectID,
		Repository:    "file://" + remote.RemotePath,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	require.True(t, first.Cloned)

	latestSHA := remote.CommitAndPush(t, "README.md", "second\n", "second commit")

	second, err := manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     projectID,
		Repository:    "file://" + remote.RemotePath,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	require.False(t, second.Cloned)
	require.Equal(t, first.RepoPath, second.RepoPath)

	localSHA := runGitOutput(t, second.RepoPath, "rev-parse", "HEAD")
	require.Equal(t, latestSHA, localSHA)
}

func TestRepoCloneManagerDetectsNonFastForwardConflictAndPersistsState(t *testing.T) {
	remote := newTestGitRemote(t)
	_ = remote.CommitAndPush(t, "README.md", "initial\n", "initial commit")

	stateStore := &fakeRepoCloneStateStore{}
	manager := NewRepoCloneManager(t.TempDir(), stateStore)
	projectID := "550e8400-e29b-41d4-a716-446655440000"

	first, err := manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     projectID,
		Repository:    "file://" + remote.RemotePath,
		DefaultBranch: "main",
	})
	require.NoError(t, err)
	require.NotNil(t, first)

	// Create a local commit that has not been pushed.
	runGit(t, first.RepoPath, "config", "user.email", "local@example.com")
	runGit(t, first.RepoPath, "config", "user.name", "Local Tester")
	require.NoError(t, os.WriteFile(filepath.Join(first.RepoPath, "README.md"), []byte("local\n"), 0o644))
	runGit(t, first.RepoPath, "add", "README.md")
	runGit(t, first.RepoPath, "commit", "-m", "local commit")

	// Create a conflicting commit on origin/main so local can no longer fast-forward.
	_ = remote.CommitAndPush(t, "README.md", "remote\n", "remote commit")

	_, err = manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     projectID,
		Repository:    "file://" + remote.RemotePath,
		DefaultBranch: "main",
	})
	require.Error(t, err)
	var conflictErr *SyncConflictError
	require.ErrorAs(t, err, &conflictErr)
	require.Equal(t, projectID, conflictErr.ProjectID)
	require.Equal(t, "main", conflictErr.Branch)
	require.NotEmpty(t, conflictErr.LocalSHA)
	require.NotEmpty(t, conflictErr.RemoteSHA)

	require.NotEmpty(t, stateStore.conflictUpdates)
	lastConflict := stateStore.conflictUpdates[len(stateStore.conflictUpdates)-1]
	require.Equal(t, projectID, lastConflict.ProjectID)
	require.Equal(t, store.RepoConflictNeedsDecision, lastConflict.ConflictState)
	require.JSONEq(
		t,
		`{"branch":"main","reason":"non_fast_forward"}`,
		subsetConflictJSON(lastConflict.ConflictDetail, []string{"branch", "reason"}),
	)
}

func TestRepoCloneManagerRejectsInvalidRepositoryMapping(t *testing.T) {
	manager := NewRepoCloneManager(t.TempDir(), nil)

	_, err := manager.EnsureLocalClone(context.Background(), EnsureRepoCloneInput{
		ProjectID:     "550e8400-e29b-41d4-a716-446655440000",
		Repository:    "not a repo mapping",
		DefaultBranch: "main",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "owner/repo")
}

type testGitRemote struct {
	RemotePath string
	WorkPath   string
}

func newTestGitRemote(t *testing.T) *testGitRemote {
	t.Helper()

	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	workPath := filepath.Join(root, "work")

	runGit(t, "", "init", "--bare", remotePath)
	runGit(t, "", "init", "--initial-branch=main", workPath)
	runGit(t, workPath, "config", "user.email", "otter@example.com")
	runGit(t, workPath, "config", "user.name", "Otter Test")
	runGit(t, workPath, "remote", "add", "origin", remotePath)

	return &testGitRemote{
		RemotePath: remotePath,
		WorkPath:   workPath,
	}
}

func (r *testGitRemote) CommitAndPush(t *testing.T, filename, contents, message string) string {
	t.Helper()

	fullPath := filepath.Join(r.WorkPath, filename)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(contents), 0o644))
	runGit(t, r.WorkPath, "add", filename)
	runGit(t, r.WorkPath, "commit", "-m", message)
	runGit(t, r.WorkPath, "push", "-u", "origin", "main")
	return runGitOutput(t, r.WorkPath, "rev-parse", "HEAD")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = runGitOutput(t, dir, args...)
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s failed: %s", strings.Join(args, " "), string(output))
	return strings.TrimSpace(string(output))
}

func stringPtr(value string) *string {
	return &value
}

func subsetConflictJSON(raw json.RawMessage, keys []string) string {
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	subset := map[string]any{}
	for _, key := range keys {
		subset[key] = payload[key]
	}
	out, _ := json.Marshal(subset)
	return string(out)
}
