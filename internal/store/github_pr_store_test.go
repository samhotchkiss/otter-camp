package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGitHubIssuePRStore_UpsertAndListPullRequests(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "github-pr-store-org")
	projectID := createTestProject(t, db, orgID, "github-pr-store-project")

	store := NewGitHubIssuePRStore(db)
	ctx := ctxWithWorkspace(orgID)

	createdAt := time.Date(2026, 2, 6, 13, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(15 * time.Minute)

	issue, err := store.UpsertIssue(ctx, UpsertGitHubIssueInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       42,
		Title:              "Track PR progress",
		State:              "open",
		AuthorLogin:        stringPtr("sam"),
		IsPullRequest:      true,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	})
	require.NoError(t, err)
	require.Equal(t, int64(42), issue.GitHubNumber)
	require.True(t, issue.IsPullRequest)
	require.Equal(t, "open", issue.State)

	pr, err := store.UpsertPullRequest(ctx, UpsertGitHubPullRequestInput{
		ProjectID:          projectID,
		IssueID:            &issue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       42,
		Title:              "Add pull request domain model",
		State:              "open",
		Draft:              true,
		Mergeable:          boolPtr(true),
		MergeableState:     stringPtr("clean"),
		HeadRef:            "feature/pr-domain",
		HeadSHA:            "1111111",
		BaseRef:            "main",
		BaseSHA:            stringPtr("aaaaaaa"),
		Merged:             false,
		AuthorLogin:        stringPtr("sam"),
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
	})
	require.NoError(t, err)
	require.NotNil(t, pr.IssueID)
	require.Equal(t, issue.ID, *pr.IssueID)
	require.True(t, pr.Draft)
	require.False(t, pr.Merged)
	require.Equal(t, "feature/pr-domain", pr.HeadRef)

	prs, err := store.ListPullRequests(ctx, projectID, nil, 50)
	require.NoError(t, err)
	require.Len(t, prs, 1)
	require.Equal(t, pr.ID, prs[0].ID)

	mergedAt := updatedAt.Add(20 * time.Minute)
	updatedPR, err := store.UpsertPullRequest(ctx, UpsertGitHubPullRequestInput{
		ProjectID:          projectID,
		IssueID:            &issue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       42,
		Title:              "Add pull request domain model",
		State:              "closed",
		Draft:              false,
		Mergeable:          boolPtr(true),
		MergeableState:     stringPtr("unknown"),
		HeadRef:            "feature/pr-domain",
		HeadSHA:            "2222222",
		BaseRef:            "main",
		BaseSHA:            stringPtr("bbbbbbb"),
		Merged:             true,
		MergedAt:           &mergedAt,
		MergedCommitSHA:    stringPtr("deadbeef"),
		AuthorLogin:        stringPtr("sam"),
		CreatedAt:          createdAt,
		UpdatedAt:          mergedAt,
		ClosedAt:           &mergedAt,
	})
	require.NoError(t, err)
	require.False(t, updatedPR.Draft)
	require.True(t, updatedPR.Merged)
	require.NotNil(t, updatedPR.MergedCommitSHA)
	require.Equal(t, "deadbeef", *updatedPR.MergedCommitSHA)

	closed := "closed"
	closedPRs, err := store.ListPullRequests(ctx, projectID, &closed, 50)
	require.NoError(t, err)
	require.Len(t, closedPRs, 1)
	require.Equal(t, updatedPR.ID, closedPRs[0].ID)
	require.True(t, closedPRs[0].Merged)

	// PR updates should not overwrite issue lifecycle fields.
	var issueState string
	err = db.QueryRow(
		`SELECT state FROM project_github_issues WHERE id = $1`,
		issue.ID,
	).Scan(&issueState)
	require.NoError(t, err)
	require.Equal(t, "open", issueState)
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
