package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func requireBareRepo(t *testing.T, repoPath string) {
	t.Helper()
	require.DirExists(t, repoPath)
	require.FileExists(t, filepath.Join(repoPath, "HEAD"))
	require.DirExists(t, filepath.Join(repoPath, "objects"))
}

func TestProjectStore_InitProjectRepo(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-repo-init-org")
	projectID := createTestProject(t, db, orgID, "project-repo-init")

	store := NewProjectStore(db)
	ctx := ctxWithWorkspace(orgID)

	err := store.InitProjectRepo(ctx, projectID)
	require.NoError(t, err)

	repoPath, err := store.GetRepoPath(ctx, projectID)
	require.NoError(t, err)

	requireBareRepo(t, repoPath)

	project, err := store.GetByID(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, project.LocalRepoPath)
	require.Equal(t, repoPath, *project.LocalRepoPath)
}

func TestProjectStore_GetRepoPath(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-repo-path-org")
	projectID := createTestProject(t, db, orgID, "project-repo-path")

	store := NewProjectStore(db)
	ctx := ctxWithWorkspace(orgID)

	require.NoError(t, store.InitProjectRepo(ctx, projectID))

	repoPath, err := store.GetRepoPath(ctx, projectID)
	require.NoError(t, err)
	require.NotEmpty(t, repoPath)
	requireBareRepo(t, repoPath)
}

func TestProjectStore_ArchiveProjectRepo(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "project-repo-archive-org")
	projectID := createTestProject(t, db, orgID, "project-repo-archive")

	store := NewProjectStore(db)
	ctx := ctxWithWorkspace(orgID)

	require.NoError(t, store.InitProjectRepo(ctx, projectID))

	originalPath, err := store.GetRepoPath(ctx, projectID)
	require.NoError(t, err)

	require.NoError(t, store.ArchiveProjectRepo(ctx, projectID))

	archivedPath, err := projectArchivePath(orgID, projectID)
	require.NoError(t, err)

	requireBareRepo(t, archivedPath)
	_, err = os.Stat(originalPath)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	project, err := store.GetByID(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, project.LocalRepoPath)
	require.Equal(t, archivedPath, *project.LocalRepoPath)
}

func TestProjectStore_ProjectRepoIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "project-repo-iso-a")
	orgB := createTestOrganization(t, db, "project-repo-iso-b")
	projectID := createTestProject(t, db, orgA, "project-repo-iso")

	store := NewProjectStore(db)
	ctxB := ctxWithWorkspace(orgB)

	require.Error(t, store.InitProjectRepo(ctxB, projectID))

	_, err := store.GetRepoPath(ctxB, projectID)
	require.Error(t, err)

	require.Error(t, store.ArchiveProjectRepo(ctxB, projectID))
}

func TestGitRepoRoot_Default(t *testing.T) {
	t.Setenv("GIT_REPO_ROOT", "")

	root := gitRepoRoot()
	require.Equal(t, filepath.Clean("./data/repos"), root)
}

func TestGitRepoRoot_UsesEnvOverride(t *testing.T) {
	t.Setenv("GIT_REPO_ROOT", " ./tmp/test-repos ")

	root := gitRepoRoot()
	require.Equal(t, filepath.Clean("./tmp/test-repos"), root)
}
