package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func createDeployConfigTestProject(t *testing.T, db *sql.DB, orgID, name string) string {
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

func TestNormalizeDeployConfigInput(t *testing.T) {
	t.Run("defaults empty method to none", func(t *testing.T) {
		cfg, err := normalizeDeployConfigInput(UpsertDeployConfigInput{})
		require.NoError(t, err)
		require.Equal(t, DeployMethodNone, cfg.DeployMethod)
		require.Equal(t, "main", cfg.GitHubBranch)
		require.Nil(t, cfg.GitHubRepoURL)
		require.Nil(t, cfg.CLICommand)
	})

	t.Run("github push defaults branch to main", func(t *testing.T) {
		repo := "https://github.com/example/repo.git"
		cfg, err := normalizeDeployConfigInput(UpsertDeployConfigInput{
			DeployMethod:  DeployMethodGitHubPush,
			GitHubRepoURL: &repo,
		})
		require.NoError(t, err)
		require.Equal(t, DeployMethodGitHubPush, cfg.DeployMethod)
		require.Equal(t, "main", cfg.GitHubBranch)
		require.NotNil(t, cfg.GitHubRepoURL)
		require.Equal(t, repo, *cfg.GitHubRepoURL)
		require.Nil(t, cfg.CLICommand)
	})

	t.Run("cli command requires command", func(t *testing.T) {
		_, err := normalizeDeployConfigInput(UpsertDeployConfigInput{
			DeployMethod: DeployMethodCLICommand,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "cli_command")
	})

	t.Run("none clears optional fields", func(t *testing.T) {
		repo := "https://github.com/example/repo.git"
		command := "npx itsalive-co"
		cfg, err := normalizeDeployConfigInput(UpsertDeployConfigInput{
			DeployMethod:  DeployMethodNone,
			GitHubRepoURL: &repo,
			CLICommand:    &command,
		})
		require.NoError(t, err)
		require.Equal(t, DeployMethodNone, cfg.DeployMethod)
		require.Nil(t, cfg.GitHubRepoURL)
		require.Nil(t, cfg.CLICommand)
	})
}

func TestDeployConfigStore_UpsertAndGetByProject(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "deploy-config-org")
	projectID := createDeployConfigTestProject(t, db, orgID, "Deploy Config Project")

	store := NewDeployConfigStore(db)
	ctx := ctxWithWorkspace(orgID)

	defaultConfig, err := store.GetByProject(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, DeployMethodNone, defaultConfig.DeployMethod)
	require.Equal(t, "main", defaultConfig.GitHubBranch)
	require.Nil(t, defaultConfig.GitHubRepoURL)
	require.Nil(t, defaultConfig.CLICommand)

	repoURL := "https://github.com/example/repo.git"
	updated, err := store.Upsert(ctx, UpsertDeployConfigInput{
		ProjectID:     projectID,
		DeployMethod:  DeployMethodGitHubPush,
		GitHubRepoURL: &repoURL,
		GitHubBranch:  "release",
	})
	require.NoError(t, err)
	require.Equal(t, DeployMethodGitHubPush, updated.DeployMethod)
	require.NotNil(t, updated.GitHubRepoURL)
	require.Equal(t, repoURL, *updated.GitHubRepoURL)
	require.Equal(t, "release", updated.GitHubBranch)
	require.Nil(t, updated.CLICommand)

	command := "npx itsalive-co"
	updated, err = store.Upsert(ctx, UpsertDeployConfigInput{
		ProjectID:    projectID,
		DeployMethod: DeployMethodCLICommand,
		CLICommand:   &command,
	})
	require.NoError(t, err)
	require.Equal(t, DeployMethodCLICommand, updated.DeployMethod)
	require.NotNil(t, updated.CLICommand)
	require.Equal(t, command, *updated.CLICommand)
	require.Nil(t, updated.GitHubRepoURL)
}

func TestDeployConfigStore_RejectsCrossWorkspaceProject(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "deploy-config-org-a")
	orgB := createTestOrganization(t, db, "deploy-config-org-b")
	projectA := createDeployConfigTestProject(t, db, orgA, "Deploy Config Project A")

	store := NewDeployConfigStore(db)
	ctxB := ctxWithWorkspace(orgB)

	_, err := store.Upsert(ctxB, UpsertDeployConfigInput{
		ProjectID:    projectA,
		DeployMethod: DeployMethodCLICommand,
		CLICommand:   deployConfigStringPtr("echo deploy"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func deployConfigStringPtr(v string) *string {
	return &v
}
