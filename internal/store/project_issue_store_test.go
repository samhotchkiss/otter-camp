package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectIssueStore_GitHubLinkUpsertIsIdempotent(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-link-org")
	projectID := createTestProject(t, db, orgID, "Issue Link Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Imported issue",
		Origin:    "github",
	})
	require.NoError(t, err)

	first, err := issueStore.UpsertGitHubLink(ctx, UpsertProjectIssueGitHubLinkInput{
		IssueID:            issue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       123,
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/issues/123"),
		GitHubState:        "open",
	})
	require.NoError(t, err)

	second, err := issueStore.UpsertGitHubLink(ctx, UpsertProjectIssueGitHubLinkInput{
		IssueID:            issue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       123,
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/issues/123"),
		GitHubState:        "open",
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM project_issue_github_links WHERE issue_id = $1", issue.ID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestProjectIssueStore_ListByProjectStateAndOrigin(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-list-org")
	projectID := createTestProject(t, db, orgID, "Issue List Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Local draft",
		State:     "open",
		Origin:    "local",
	})
	require.NoError(t, err)
	openGitHubIssue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Imported open issue",
		State:     "open",
		Origin:    "github",
	})
	require.NoError(t, err)
	_, err = issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Imported closed issue",
		State:     "closed",
		Origin:    "github",
	})
	require.NoError(t, err)

	state := "open"
	origin := "github"
	results, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID: projectID,
		State:     &state,
		Origin:    &origin,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, openGitHubIssue.ID, results[0].ID)
	require.Equal(t, "open", results[0].State)
	require.Equal(t, "github", results[0].Origin)
}

func TestProjectIssueStore_IsolationBlocksCrossOrgReadsAndWrites(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "issue-iso-org-a")
	orgB := createTestOrganization(t, db, "issue-iso-org-b")
	projectA := createTestProject(t, db, orgA, "Issue Isolation A")
	projectB := createTestProject(t, db, orgB, "Issue Isolation B")

	issueStore := NewProjectIssueStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	_, err := issueStore.CreateIssue(ctxA, CreateProjectIssueInput{
		ProjectID: projectA,
		Title:     "Org A issue",
		Origin:    "local",
	})
	require.NoError(t, err)
	issueB, err := issueStore.CreateIssue(ctxB, CreateProjectIssueInput{
		ProjectID: projectB,
		Title:     "Org B issue",
		Origin:    "github",
	})
	require.NoError(t, err)

	results, err := issueStore.ListIssues(ctxA, ProjectIssueFilter{ProjectID: projectB})
	require.NoError(t, err)
	require.Empty(t, results)

	_, err = issueStore.CreateIssue(ctxA, CreateProjectIssueInput{
		ProjectID: projectB,
		Title:     "Cross-org write",
		Origin:    "local",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))

	_, err = issueStore.UpsertGitHubLink(ctxA, UpsertProjectIssueGitHubLinkInput{
		IssueID:            issueB.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       200,
		GitHubState:        "open",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))

	_, err = issueStore.UpsertSyncCheckpoint(ctxA, UpsertProjectIssueSyncCheckpointInput{
		ProjectID:          projectB,
		RepositoryFullName: "samhotchkiss/otter-camp",
		Resource:           "issues",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))
}
