package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentFilesWorkingRepoPath(t *testing.T) {
	t.Parallel()

	require.Equal(t, "/tmp/repos/project-working", agentFilesWorkingRepoPath("/tmp/repos/project.git"))
	require.Equal(t, "/tmp/repos/project-working", agentFilesWorkingRepoPath("/tmp/repos/project"))
}

func TestEnsureAgentFilesWorkingRepo(t *testing.T) {
	t.Parallel()

	t.Run("clones bare repo and is idempotent", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		root := t.TempDir()
		barePath := filepath.Join(root, "agent-files.git")

		cmd := exec.CommandContext(ctx, "git", "init", "--bare", barePath)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		workingPath, err := ensureAgentFilesWorkingRepo(ctx, barePath)
		require.NoError(t, err)
		require.Equal(t, agentFilesWorkingRepoPath(barePath), workingPath)
		require.DirExists(t, filepath.Join(workingPath, ".git"))

		secondPath, err := ensureAgentFilesWorkingRepo(ctx, barePath)
		require.NoError(t, err)
		require.Equal(t, workingPath, secondPath)
	})

	t.Run("errors when bare repo is missing", func(t *testing.T) {
		t.Parallel()
		_, err := ensureAgentFilesWorkingRepo(context.Background(), filepath.Join(t.TempDir(), "missing.git"))
		require.Error(t, err)
	})

	t.Run("errors when working repo path is not a directory", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		root := t.TempDir()
		barePath := filepath.Join(root, "agent-files.git")

		cmd := exec.CommandContext(ctx, "git", "init", "--bare", barePath)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		badPath := agentFilesWorkingRepoPath(barePath)
		require.NoError(t, os.WriteFile(badPath, []byte("not a dir"), 0o644))

		_, err = ensureAgentFilesWorkingRepo(ctx, barePath)
		require.Error(t, err)
	})
}
