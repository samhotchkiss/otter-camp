package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectCommitStore_UpsertIsIdempotentAndPreservesBody(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "project-commit-upsert-org")
	projectID := createTestProject(t, db, orgID, "Project Commit Upsert")

	commitStore := NewProjectCommitStore(db)
	ctx := ctxWithWorkspace(orgID)

	authoredAt := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	first, created, err := commitStore.UpsertCommit(ctx, UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "main",
		SHA:                "abc123",
		AuthorName:         "Sam",
		AuthorEmail:        stringPtr("sam@example.com"),
		AuthoredAt:         &authoredAt,
		Subject:            "Initial commit",
		Body:               stringPtr("First body"),
		Message:            "Initial commit\n\nFirst body",
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first.Body)
	require.Equal(t, "First body", *first.Body)

	updatedAt := authoredAt.Add(15 * time.Minute)
	second, created, err := commitStore.UpsertCommit(ctx, UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "main",
		SHA:                "abc123",
		AuthorName:         "Sam",
		AuthorEmail:        stringPtr("sam@example.com"),
		AuthoredAt:         &updatedAt,
		Subject:            "Initial commit (edited)",
		Body:               stringPtr("Updated body"),
		Message:            "Initial commit (edited)\n\nUpdated body",
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
	require.NotNil(t, second.Body)
	require.Equal(t, "Updated body", *second.Body)

	commits, err := commitStore.ListCommits(ctx, ProjectCommitFilter{ProjectID: projectID})
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, "abc123", commits[0].SHA)
	require.NotNil(t, commits[0].Body)
	require.Equal(t, "Updated body", *commits[0].Body)
}

func TestProjectCommitStore_ListAndGetBySHA(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "project-commit-list-org")
	projectID := createTestProject(t, db, orgID, "Project Commit List")

	commitStore := NewProjectCommitStore(db)
	ctx := ctxWithWorkspace(orgID)

	older := time.Date(2026, 2, 6, 10, 0, 0, 0, time.UTC)
	_, _, err := commitStore.UpsertCommit(ctx, UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "main",
		SHA:                "sha-old",
		AuthorName:         "Sam",
		AuthoredAt:         &older,
		Subject:            "Old commit",
		Message:            "Old commit",
	})
	require.NoError(t, err)

	newer := older.Add(1 * time.Hour)
	_, _, err = commitStore.UpsertCommit(ctx, UpsertProjectCommitInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		BranchName:         "feature/review",
		SHA:                "sha-new",
		AuthorName:         "Stone",
		AuthoredAt:         &newer,
		Subject:            "New commit",
		Body:               stringPtr("Verbose body"),
		Message:            "New commit\n\nVerbose body",
	})
	require.NoError(t, err)

	allCommits, err := commitStore.ListCommits(ctx, ProjectCommitFilter{ProjectID: projectID})
	require.NoError(t, err)
	require.Len(t, allCommits, 2)
	require.Equal(t, "sha-new", allCommits[0].SHA)
	require.Equal(t, "sha-old", allCommits[1].SHA)

	branch := "feature/review"
	filtered, err := commitStore.ListCommits(ctx, ProjectCommitFilter{ProjectID: projectID, Branch: &branch})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "sha-new", filtered[0].SHA)

	loaded, err := commitStore.GetCommitBySHA(ctx, projectID, "sha-new")
	require.NoError(t, err)
	require.Equal(t, "New commit", loaded.Subject)
	require.NotNil(t, loaded.Body)
	require.Equal(t, "Verbose body", *loaded.Body)
}
