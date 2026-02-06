package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createIssueTestAgent(t *testing.T, db Querier, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRowContext(
		context.Background(),
		`INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id`,
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

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
	_, err = issueStore.UpsertGitHubLink(ctx, UpsertProjectIssueGitHubLinkInput{
		IssueID:            openGitHubIssue.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       123,
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/issues/123"),
		GitHubState:        "open",
	})
	require.NoError(t, err)
	openGitHubPR, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Imported open PR",
		State:     "open",
		Origin:    "github",
	})
	require.NoError(t, err)
	_, err = issueStore.UpsertGitHubLink(ctx, UpsertProjectIssueGitHubLinkInput{
		IssueID:            openGitHubPR.ID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       124,
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/pull/124"),
		GitHubState:        "open",
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
	require.Len(t, results, 2)
	resultIDs := map[string]bool{
		results[0].ID: true,
		results[1].ID: true,
	}
	require.True(t, resultIDs[openGitHubIssue.ID])
	require.True(t, resultIDs[openGitHubPR.ID])

	kindIssue := "issue"
	issuesOnly, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID: projectID,
		State:     &state,
		Origin:    &origin,
		Kind:      &kindIssue,
	})
	require.NoError(t, err)
	require.Len(t, issuesOnly, 1)
	require.Equal(t, openGitHubIssue.ID, issuesOnly[0].ID)

	kindPR := "pull_request"
	prsOnly, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID: projectID,
		State:     &state,
		Origin:    &origin,
		Kind:      &kindPR,
	})
	require.NoError(t, err)
	require.Len(t, prsOnly, 1)
	require.Equal(t, openGitHubPR.ID, prsOnly[0].ID)

	links, err := issueStore.ListGitHubLinksByIssueIDs(ctx, []string{openGitHubIssue.ID, openGitHubPR.ID})
	require.NoError(t, err)
	require.Len(t, links, 2)
	require.Equal(t, int64(123), links[openGitHubIssue.ID].GitHubNumber)
	require.Equal(t, int64(124), links[openGitHubPR.ID].GitHubNumber)
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

func TestProjectIssueStore_ParticipantAddRemoveAndUniqueness(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-participants-org")
	projectID := createTestProject(t, db, orgID, "Issue Participant Project")
	ownerAgentID := createIssueTestAgent(t, db, orgID, "owner-agent")
	collabAgentID := createIssueTestAgent(t, db, orgID, "collab-agent")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Participants issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	owner, err := issueStore.AddParticipant(ctx, AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerAgentID,
		Role:    "owner",
	})
	require.NoError(t, err)

	ownerAgain, err := issueStore.AddParticipant(ctx, AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: ownerAgentID,
		Role:    "owner",
	})
	require.NoError(t, err)
	require.Equal(t, owner.ID, ownerAgain.ID)

	collab, err := issueStore.AddParticipant(ctx, AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: collabAgentID,
		Role:    "collaborator",
	})
	require.NoError(t, err)

	_, err = issueStore.AddParticipant(ctx, AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: collabAgentID,
		Role:    "owner",
	})
	require.Error(t, err)

	require.NoError(t, issueStore.RemoveParticipant(ctx, issue.ID, collabAgentID))
	readded, err := issueStore.AddParticipant(ctx, AddProjectIssueParticipantInput{
		IssueID: issue.ID,
		AgentID: collabAgentID,
		Role:    "collaborator",
	})
	require.NoError(t, err)
	require.NotEqual(t, collab.ID, readded.ID)

	activeParticipants, err := issueStore.ListParticipants(ctx, issue.ID, false)
	require.NoError(t, err)
	require.Len(t, activeParticipants, 2)
}

func TestProjectIssueStore_CommentOrderingAndPaginationByIssue(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-comments-org")
	projectID := createTestProject(t, db, orgID, "Issue Comment Project")
	authorID := createIssueTestAgent(t, db, orgID, "comment-author")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Comment thread issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	bodies := []string{"first", "second", "third"}
	for index, body := range bodies {
		comment, err := issueStore.CreateComment(ctx, CreateProjectIssueCommentInput{
			IssueID:       issue.ID,
			AuthorAgentID: authorID,
			Body:          body,
		})
		require.NoError(t, err)
		timestamp := base.Add(time.Duration(index) * time.Minute)
		_, err = db.Exec(
			`UPDATE project_issue_comments SET created_at = $1, updated_at = $1 WHERE id = $2`,
			timestamp,
			comment.ID,
		)
		require.NoError(t, err)
	}

	firstPage, err := issueStore.ListComments(ctx, issue.ID, 2, 0)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	require.Equal(t, "first", firstPage[0].Body)
	require.Equal(t, "second", firstPage[1].Body)

	secondPage, err := issueStore.ListComments(ctx, issue.ID, 2, 2)
	require.NoError(t, err)
	require.Len(t, secondPage, 1)
	require.Equal(t, "third", secondPage[0].Body)
}

func TestProjectIssueStore_ParticipantAndCommentIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "issue-ops-iso-a")
	orgB := createTestOrganization(t, db, "issue-ops-iso-b")
	projectA := createTestProject(t, db, orgA, "Issue Ops A")
	projectB := createTestProject(t, db, orgB, "Issue Ops B")
	agentA := createIssueTestAgent(t, db, orgA, "ops-agent-a")
	agentB := createIssueTestAgent(t, db, orgB, "ops-agent-b")

	issueStore := NewProjectIssueStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)
	issueA, err := issueStore.CreateIssue(ctxA, CreateProjectIssueInput{
		ProjectID: projectA,
		Title:     "Issue A",
		Origin:    "local",
	})
	require.NoError(t, err)
	issueB, err := issueStore.CreateIssue(ctxB, CreateProjectIssueInput{
		ProjectID: projectB,
		Title:     "Issue B",
		Origin:    "local",
	})
	require.NoError(t, err)

	_, err = issueStore.AddParticipant(ctxA, AddProjectIssueParticipantInput{
		IssueID: issueB.ID,
		AgentID: agentB,
		Role:    "collaborator",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))

	_, err = issueStore.CreateComment(ctxA, CreateProjectIssueCommentInput{
		IssueID:       issueB.ID,
		AuthorAgentID: agentA,
		Body:          "cross org",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))

	participants, err := issueStore.ListParticipants(ctxA, issueB.ID, false)
	require.NoError(t, err)
	require.Empty(t, participants)

	comments, err := issueStore.ListComments(ctxA, issueB.ID, 20, 0)
	require.NoError(t, err)
	require.Empty(t, comments)

	_, err = issueStore.CreateComment(ctxA, CreateProjectIssueCommentInput{
		IssueID:       issueA.ID,
		AuthorAgentID: agentA,
		Body:          "valid",
	})
	require.NoError(t, err)
}
