package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitHubInstallationStore_UpsertAndGetByOrg(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-install-org")

	store := NewGitHubInstallationStore(db)
	ctx := ctxWithWorkspace(orgID)

	created, err := store.Upsert(ctx, UpsertGitHubInstallationInput{
		InstallationID: 101001,
		AccountLogin:   "otter-camp",
		AccountType:    "Organization",
		Permissions:    []byte(`{"contents":"write","issues":"write"}`),
	})
	require.NoError(t, err)
	require.Equal(t, orgID, created.OrgID)
	require.Equal(t, int64(101001), created.InstallationID)
	require.Equal(t, "otter-camp", created.AccountLogin)

	fetched, err := store.GetByOrg(ctx)
	require.NoError(t, err)
	require.Equal(t, created.ID, fetched.ID)
	require.Equal(t, created.InstallationID, fetched.InstallationID)
	require.JSONEq(t, string(created.Permissions), string(fetched.Permissions))

	updated, err := store.Upsert(ctx, UpsertGitHubInstallationInput{
		InstallationID: 101001,
		AccountLogin:   "otter-camp-updated",
		AccountType:    "Organization",
		Permissions:    []byte(`{"contents":"read","issues":"write"}`),
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, "otter-camp-updated", updated.AccountLogin)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM github_installations WHERE org_id = $1", orgID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestGitHubInstallationStore_GetByInstallationID_RespectsWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "github-install-org-a")
	orgB := createTestOrganization(t, db, "github-install-org-b")

	store := NewGitHubInstallationStore(db)

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	_, err := store.Upsert(ctxA, UpsertGitHubInstallationInput{
		InstallationID: 202001,
		AccountLogin:   "org-a",
		AccountType:    "Organization",
		Permissions:    []byte(`{"metadata":"read"}`),
	})
	require.NoError(t, err)

	_, err = store.Upsert(ctxB, UpsertGitHubInstallationInput{
		InstallationID: 202002,
		AccountLogin:   "org-b",
		AccountType:    "Organization",
		Permissions:    []byte(`{"metadata":"read"}`),
	})
	require.NoError(t, err)

	_, err = store.GetByInstallationID(ctxA, 202002)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrForbidden))

	fetched, err := store.GetByInstallationID(ctxA, 202001)
	require.NoError(t, err)
	require.Equal(t, orgA, fetched.OrgID)
	require.Equal(t, int64(202001), fetched.InstallationID)
}

func TestGitHubInstallationStore_GetByInstallationID_WithoutWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-install-no-workspace")

	store := NewGitHubInstallationStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.Upsert(ctx, UpsertGitHubInstallationInput{
		InstallationID: 303001,
		AccountLogin:   "org-public-lookup",
		AccountType:    "Organization",
		Permissions:    []byte(`{"contents":"read"}`),
	})
	require.NoError(t, err)

	fetched, err := store.GetByInstallationID(t.Context(), 303001)
	require.NoError(t, err)
	require.Equal(t, orgID, fetched.OrgID)
}

func TestGitHubInstallationStore_RejectsInvalidInput(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-install-invalid")

	store := NewGitHubInstallationStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.Upsert(ctx, UpsertGitHubInstallationInput{})
	require.Error(t, err)

	_, err = store.GetByInstallationID(ctx, 0)
	require.Error(t, err)

	_, err = store.GetByOrg(t.Context())
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNoWorkspace))

	// Verify table exists after migrations, even when no records are present.
	var exists bool
	err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'github_installations')`).Scan(&exists)
	require.NoError(t, err)
	require.True(t, exists)
}
