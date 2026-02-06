package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestProjectRepoStore_UpsertAndGetBinding(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "repo-binding-org")
	projectID := createTestProject(t, db, orgID, "repo-binding-project")

	store := NewProjectRepoStore(db)
	ctx := ctxWithWorkspace(orgID)

	binding, err := store.UpsertBinding(ctx, UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      RepoConflictNone,
	})
	require.NoError(t, err)
	require.Equal(t, projectID, binding.ProjectID)
	require.Equal(t, "samhotchkiss/otter-camp", binding.RepositoryFullName)

	fetched, err := store.GetBinding(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, binding.ID, fetched.ID)
	require.Equal(t, RepoConflictNone, fetched.ConflictState)

	updated, err := store.UpsertBinding(ctx, UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           RepoSyncModePush,
		AutoSync:           false,
		ConflictState:      RepoConflictNeedsDecision,
		ConflictDetails:    []byte(`{"reason":"merge_conflict"}`),
	})
	require.NoError(t, err)
	require.Equal(t, binding.ID, updated.ID)
	require.Equal(t, RepoConflictNeedsDecision, updated.ConflictState)
	require.Equal(t, RepoSyncModePush, updated.SyncMode)
	require.False(t, updated.AutoSync)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM project_repo_bindings WHERE project_id = $1", projectID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestProjectRepoStore_SetAndListActiveBranches(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "repo-branches-org")
	projectID := createTestProject(t, db, orgID, "repo-branches-project")

	store := NewProjectRepoStore(db)
	ctx := ctxWithWorkspace(orgID)

	branches, err := store.SetActiveBranches(ctx, projectID, []string{"main", "feature/a", "feature/a", "  feature/b  "})
	require.NoError(t, err)
	require.Len(t, branches, 3)

	listed, err := store.ListActiveBranches(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, listed, 3)
	require.Equal(t, "feature/a", listed[0].BranchName)
	require.Equal(t, "feature/b", listed[1].BranchName)
	require.Equal(t, "main", listed[2].BranchName)

	branches, err = store.SetActiveBranches(ctx, projectID, []string{"release/1.0"})
	require.NoError(t, err)
	require.Len(t, branches, 1)
	require.Equal(t, "release/1.0", branches[0].BranchName)

	listed, err = store.ListActiveBranches(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "release/1.0", listed[0].BranchName)
}

func TestProjectRepoStore_UpdateBranchCheckpoint(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "repo-checkpoint-org")
	projectID := createTestProject(t, db, orgID, "repo-checkpoint-project")

	store := NewProjectRepoStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.UpsertBinding(ctx, UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      RepoConflictNone,
	})
	require.NoError(t, err)

	firstSyncedAt := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	branch, err := store.UpdateBranchCheckpoint(ctx, projectID, "main", "sha-1", firstSyncedAt)
	require.NoError(t, err)
	require.Equal(t, "main", branch.BranchName)
	require.NotNil(t, branch.LastSyncedSHA)
	require.Equal(t, "sha-1", *branch.LastSyncedSHA)

	secondSyncedAt := firstSyncedAt.Add(30 * time.Minute)
	branch, err = store.UpdateBranchCheckpoint(ctx, projectID, "main", "sha-2", secondSyncedAt)
	require.NoError(t, err)
	require.NotNil(t, branch.LastSyncedSHA)
	require.Equal(t, "sha-2", *branch.LastSyncedSHA)

	active, err := store.ListActiveBranches(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.NotNil(t, active[0].LastSyncedSHA)
	require.Equal(t, "sha-2", *active[0].LastSyncedSHA)

	binding, err := store.GetBinding(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, binding.LastSyncedSHA)
	require.Equal(t, "sha-2", *binding.LastSyncedSHA)
}

func TestProjectRepoStore_UpdateLocalCloneState(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "repo-local-clone-org")
	projectID := createTestProject(t, db, orgID, "repo-local-clone-project")

	store := NewProjectRepoStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.UpsertBinding(ctx, UpsertProjectRepoBindingInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		DefaultBranch:      "main",
		Enabled:            true,
		SyncMode:           RepoSyncModeSync,
		AutoSync:           true,
		ConflictState:      RepoConflictNone,
	})
	require.NoError(t, err)

	updated, err := store.UpdateLocalCloneState(ctx, projectID, "release/2026", "/tmp/repos/project-a")
	require.NoError(t, err)
	require.Equal(t, "release/2026", updated.DefaultBranch)
	require.NotNil(t, updated.LocalRepoPath)
	require.Equal(t, "/tmp/repos/project-a", *updated.LocalRepoPath)

	fetched, err := store.GetBinding(ctx, projectID)
	require.NoError(t, err)
	require.NotNil(t, fetched.LocalRepoPath)
	require.Equal(t, "/tmp/repos/project-a", *fetched.LocalRepoPath)

	_, err = store.UpdateLocalCloneState(ctx, projectID, "main", "   ")
	require.Error(t, err)
}

func TestProjectRepoStore_IsolationAndValidation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "repo-iso-a")
	orgB := createTestOrganization(t, db, "repo-iso-b")
	projectA := createTestProject(t, db, orgA, "repo-iso-project-a")
	projectB := createTestProject(t, db, orgB, "repo-iso-project-b")

	store := NewProjectRepoStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	_, err := store.UpsertBinding(ctxA, UpsertProjectRepoBindingInput{
		ProjectID:          projectA,
		RepositoryFullName: "org/a",
		SyncMode:           RepoSyncModeSync,
		ConflictState:      RepoConflictNone,
	})
	require.NoError(t, err)

	_, err = store.UpsertBinding(ctxB, UpsertProjectRepoBindingInput{
		ProjectID:          projectB,
		RepositoryFullName: "org/b",
		SyncMode:           RepoSyncModeSync,
		ConflictState:      RepoConflictNone,
	})
	require.NoError(t, err)

	_, err = store.GetBinding(ctxA, projectB)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden))

	_, err = store.UpsertBinding(ctxA, UpsertProjectRepoBindingInput{
		ProjectID:          projectA,
		RepositoryFullName: "",
		SyncMode:           RepoSyncModeSync,
	})
	require.Error(t, err)

	_, err = store.SetActiveBranches(ctxA, "invalid", []string{"main"})
	require.Error(t, err)
}
