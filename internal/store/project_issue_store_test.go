package store

import (
	"context"
	"database/sql"
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

func TestProjectIssueStore_WorkTrackingSchemaDefaultsAndConstraints(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-work-tracking-schema-org")
	projectID := createTestProject(t, db, orgID, "Issue Work Tracking Schema Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Schema default verification issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	var workStatus string
	var priority string
	var ownerAgentID sql.NullString
	var dueAt sql.NullTime
	var nextStep sql.NullString
	var nextStepDueAt sql.NullTime
	err = db.QueryRowContext(
		ctx,
		`SELECT work_status, priority, owner_agent_id, due_at, next_step, next_step_due_at
			FROM project_issues
			WHERE id = $1`,
		issue.ID,
	).Scan(&workStatus, &priority, &ownerAgentID, &dueAt, &nextStep, &nextStepDueAt)
	require.NoError(t, err)
	require.Equal(t, "queued", workStatus)
	require.Equal(t, "P2", priority)
	require.False(t, ownerAgentID.Valid)
	require.False(t, dueAt.Valid)
	require.False(t, nextStep.Valid)
	require.False(t, nextStepDueAt.Valid)

	_, err = db.ExecContext(ctx, `UPDATE project_issues SET work_status = 'not-a-status' WHERE id = $1`, issue.ID)
	require.Error(t, err)

	_, err = db.ExecContext(ctx, `UPDATE project_issues SET priority = 'P9' WHERE id = $1`, issue.ID)
	require.Error(t, err)
}

func TestProjectIssueStore_UpsertIssueFromGitHubIsIdempotentAndUpdatesFields(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-github-upsert-org")
	projectID := createTestProject(t, db, orgID, "Issue GitHub Upsert Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	first, created, err := issueStore.UpsertIssueFromGitHub(ctx, UpsertProjectIssueFromGitHubInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       321,
		Title:              "Original imported issue",
		Body:               stringPtr("First body"),
		State:              "open",
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/issues/321"),
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "Original imported issue", first.Title)
	require.Equal(t, "open", first.State)

	closedAt := time.Now().UTC()
	second, created, err := issueStore.UpsertIssueFromGitHub(ctx, UpsertProjectIssueFromGitHubInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		GitHubNumber:       321,
		Title:              "Updated imported issue",
		Body:               stringPtr("Updated body"),
		State:              "closed",
		GitHubURL:          stringPtr("https://github.com/samhotchkiss/otter-camp/issues/321"),
		ClosedAt:           &closedAt,
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "Updated imported issue", second.Title)
	require.Equal(t, "closed", second.State)
	require.NotNil(t, second.ClosedAt)

	issues, err := issueStore.ListIssues(ctx, ProjectIssueFilter{ProjectID: projectID})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	require.Equal(t, second.ID, issues[0].ID)

	links, err := issueStore.ListGitHubLinksByIssueIDs(ctx, []string{second.ID})
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, int64(321), links[second.ID].GitHubNumber)
	require.NotNil(t, links[second.ID].GitHubURL)
}

func TestProjectIssueStore_CreateIssuePersistsWorkTrackingFields(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-work-fields-org")
	projectID := createTestProject(t, db, orgID, "Issue Work Fields Project")
	ownerAgentID := createIssueTestAgent(t, db, orgID, "issue-owner")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	dueAt := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	nextStepDueAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	nextStep := "Ship first draft to review"

	created, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Build issue-first workflow",
		Origin:        "local",
		OwnerAgentID:  &ownerAgentID,
		WorkStatus:    IssueWorkStatusInProgress,
		Priority:      IssuePriorityP1,
		DueAt:         &dueAt,
		NextStep:      &nextStep,
		NextStepDueAt: &nextStepDueAt,
	})
	require.NoError(t, err)
	require.NotNil(t, created.OwnerAgentID)
	require.Equal(t, ownerAgentID, *created.OwnerAgentID)
	require.Equal(t, IssueWorkStatusInProgress, created.WorkStatus)
	require.Equal(t, IssuePriorityP1, created.Priority)
	require.NotNil(t, created.DueAt)
	require.NotNil(t, created.NextStep)
	require.NotNil(t, created.NextStepDueAt)
	require.Equal(t, dueAt.UTC().Format(time.RFC3339), created.DueAt.UTC().Format(time.RFC3339))
	require.Equal(t, nextStep, *created.NextStep)
	require.Equal(t, nextStepDueAt.UTC().Format(time.RFC3339), created.NextStepDueAt.UTC().Format(time.RFC3339))

	loaded, err := issueStore.GetIssueByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.OwnerAgentID)
	require.Equal(t, ownerAgentID, *loaded.OwnerAgentID)
	require.Equal(t, IssueWorkStatusInProgress, loaded.WorkStatus)
	require.Equal(t, IssuePriorityP1, loaded.Priority)
	require.NotNil(t, loaded.DueAt)
	require.NotNil(t, loaded.NextStep)
	require.NotNil(t, loaded.NextStepDueAt)
}

func TestProjectIssueStore_ListIssuesFiltersByOwnerStatusAndPriority(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-filter-fields-org")
	projectID := createTestProject(t, db, orgID, "Issue Filter Fields Project")
	ownerAgentID := createIssueTestAgent(t, db, orgID, "issue-filter-owner")
	otherAgentID := createIssueTestAgent(t, db, orgID, "issue-filter-other")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Owner issue #1",
		Origin:       "local",
		OwnerAgentID: &ownerAgentID,
		WorkStatus:   IssueWorkStatusInProgress,
		Priority:     IssuePriorityP1,
	})
	require.NoError(t, err)

	_, err = issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Owner issue #2",
		Origin:       "local",
		OwnerAgentID: &otherAgentID,
		WorkStatus:   IssueWorkStatusBlocked,
		Priority:     IssuePriorityP3,
	})
	require.NoError(t, err)

	latestIssues, err := issueStore.ListIssues(ctx, ProjectIssueFilter{ProjectID: projectID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, latestIssues, 1)
	secondIssueNumber := latestIssues[0].IssueNumber

	ownerFilter := ownerAgentID
	filteredByOwner, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID:    projectID,
		OwnerAgentID: &ownerFilter,
	})
	require.NoError(t, err)
	require.Len(t, filteredByOwner, 1)
	require.NotNil(t, filteredByOwner[0].OwnerAgentID)
	require.Equal(t, ownerAgentID, *filteredByOwner[0].OwnerAgentID)

	workStatusFilter := IssueWorkStatusBlocked
	filteredByStatus, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID:  projectID,
		WorkStatus: &workStatusFilter,
	})
	require.NoError(t, err)
	require.Len(t, filteredByStatus, 1)
	require.Equal(t, IssueWorkStatusBlocked, filteredByStatus[0].WorkStatus)

	priorityFilter := IssuePriorityP1
	filteredByPriority, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID: projectID,
		Priority:  &priorityFilter,
	})
	require.NoError(t, err)
	require.Len(t, filteredByPriority, 1)
	require.Equal(t, IssuePriorityP1, filteredByPriority[0].Priority)

	filteredByIssueNumber, err := issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID:   projectID,
		IssueNumber: &secondIssueNumber,
	})
	require.NoError(t, err)
	require.Len(t, filteredByIssueNumber, 1)
	require.Equal(t, secondIssueNumber, filteredByIssueNumber[0].IssueNumber)

	badStatus := "invalid"
	_, err = issueStore.ListIssues(ctx, ProjectIssueFilter{
		ProjectID:  projectID,
		WorkStatus: &badStatus,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "work_status")
}

func TestProjectIssueStore_TransitionWorkStatusEnforcesStateMachine(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-work-transition-org")
	projectID := createTestProject(t, db, orgID, "Issue Work Transition Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Transition work state",
		Origin:    "local",
	})
	require.NoError(t, err)
	require.Equal(t, IssueWorkStatusQueued, issue.WorkStatus)

	inProgress, err := issueStore.TransitionWorkStatus(ctx, issue.ID, IssueWorkStatusInProgress)
	require.NoError(t, err)
	require.Equal(t, "open", inProgress.State)
	require.Equal(t, IssueWorkStatusInProgress, inProgress.WorkStatus)

	review, err := issueStore.TransitionWorkStatus(ctx, issue.ID, IssueWorkStatusReview)
	require.NoError(t, err)
	require.Equal(t, IssueWorkStatusReview, review.WorkStatus)

	done, err := issueStore.TransitionWorkStatus(ctx, issue.ID, IssueWorkStatusDone)
	require.NoError(t, err)
	require.Equal(t, "closed", done.State)
	require.Equal(t, IssueWorkStatusDone, done.WorkStatus)
	require.NotNil(t, done.ClosedAt)

	_, err = issueStore.TransitionWorkStatus(ctx, issue.ID, IssueWorkStatusInProgress)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition")

	_, err = issueStore.TransitionWorkStatus(ctx, issue.ID, "unknown")
	require.Error(t, err)
	require.Contains(t, err.Error(), "work_status")
}

func TestProjectIssueStore_CreateIssuePersistsAndValidatesLinkedDocumentAndApprovalState(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-linked-fields-org")
	projectID := createTestProject(t, db, orgID, "Issue Linked Fields Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	documentPath := "/posts/2026-02-06-launch-plan.md"
	created, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Review launch plan",
		Origin:        "local",
		DocumentPath:  &documentPath,
		ApprovalState: IssueApprovalStateReadyForReview,
	})
	require.NoError(t, err)
	require.NotNil(t, created.DocumentPath)
	require.Equal(t, documentPath, *created.DocumentPath)
	require.Equal(t, IssueApprovalStateReadyForReview, created.ApprovalState)

	loaded, err := issueStore.GetIssueByID(ctx, created.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.DocumentPath)
	require.Equal(t, documentPath, *loaded.DocumentPath)
	require.Equal(t, IssueApprovalStateReadyForReview, loaded.ApprovalState)

	invalidPath := "/notes/not-a-post.md"
	_, err = issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Invalid path issue",
		Origin:       "local",
		DocumentPath: &invalidPath,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "document_path")

	_, err = issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:     projectID,
		Title:         "Invalid approval state issue",
		Origin:        "local",
		DocumentPath:  &documentPath,
		ApprovalState: "queued",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "approval_state")
}

func TestProjectIssueStore_TransitionApprovalStateEnforcesStateMachine(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-transition-org")
	projectID := createTestProject(t, db, orgID, "Issue Transition Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Transition me",
		Origin:    "local",
	})
	require.NoError(t, err)
	require.Equal(t, IssueApprovalStateDraft, issue.ApprovalState)

	ready, err := issueStore.TransitionApprovalState(ctx, issue.ID, IssueApprovalStateReadyForReview)
	require.NoError(t, err)
	require.Equal(t, IssueApprovalStateReadyForReview, ready.ApprovalState)

	needsChanges, err := issueStore.TransitionApprovalState(ctx, issue.ID, IssueApprovalStateNeedsChanges)
	require.NoError(t, err)
	require.Equal(t, IssueApprovalStateNeedsChanges, needsChanges.ApprovalState)

	backToReady, err := issueStore.TransitionApprovalState(ctx, issue.ID, IssueApprovalStateReadyForReview)
	require.NoError(t, err)
	require.Equal(t, IssueApprovalStateReadyForReview, backToReady.ApprovalState)

	approved, err := issueStore.TransitionApprovalState(ctx, issue.ID, IssueApprovalStateApproved)
	require.NoError(t, err)
	require.Equal(t, IssueApprovalStateApproved, approved.ApprovalState)
	require.Equal(t, "closed", approved.State)
	require.NotNil(t, approved.ClosedAt)

	_, err = issueStore.TransitionApprovalState(ctx, issue.ID, IssueApprovalStateDraft)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition")

	_, err = issueStore.TransitionApprovalState(ctx, issue.ID, "queued")
	require.Error(t, err)
	require.Contains(t, err.Error(), "approval_state")
}

func TestProjectIssueStore_UpdateAndListIssuesByDocumentPath(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-doc-path-org")
	projectID := createTestProject(t, db, orgID, "Issue Doc Path Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	pathA := "/posts/2026-02-06-post-a.md"
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Linked issue",
		Origin:       "local",
		DocumentPath: &pathA,
	})
	require.NoError(t, err)

	matches, err := issueStore.ListIssuesByDocumentPath(ctx, projectID, pathA)
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, issue.ID, matches[0].ID)

	pathB := "/posts/2026-02-07-post-b.md"
	updated, err := issueStore.UpdateIssueDocumentPath(ctx, issue.ID, &pathB)
	require.NoError(t, err)
	require.NotNil(t, updated.DocumentPath)
	require.Equal(t, pathB, *updated.DocumentPath)

	matches, err = issueStore.ListIssuesByDocumentPath(ctx, projectID, pathA)
	require.NoError(t, err)
	require.Empty(t, matches)
	matches, err = issueStore.ListIssuesByDocumentPath(ctx, projectID, pathB)
	require.NoError(t, err)
	require.Len(t, matches, 1)

	detached, err := issueStore.UpdateIssueDocumentPath(ctx, issue.ID, nil)
	require.NoError(t, err)
	require.Nil(t, detached.DocumentPath)
}

func TestProjectIssueStore_ReviewCheckpointUpsertAndGet(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-review-checkpoint-org")
	projectID := createTestProject(t, db, orgID, "Issue Review Checkpoint Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)

	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Checkpoint issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	first, err := issueStore.UpsertReviewCheckpoint(ctx, issue.ID, "sha-review-1")
	require.NoError(t, err)
	require.Equal(t, issue.ID, first.IssueID)
	require.Equal(t, "sha-review-1", first.LastReviewCommitSHA)

	second, err := issueStore.UpsertReviewCheckpoint(ctx, issue.ID, "sha-review-2")
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, "sha-review-2", second.LastReviewCommitSHA)

	loaded, err := issueStore.GetReviewCheckpoint(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, second.ID, loaded.ID)
	require.Equal(t, "sha-review-2", loaded.LastReviewCommitSHA)
}

func TestProjectIssueStore_GetReviewCheckpointMissingReturnsNotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-review-checkpoint-missing-org")
	projectID := createTestProject(t, db, orgID, "Issue Review Checkpoint Missing Project")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID: projectID,
		Title:     "Checkpoint missing issue",
		Origin:    "local",
	})
	require.NoError(t, err)

	_, err = issueStore.GetReviewCheckpoint(ctx, issue.ID)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestProjectIssueStore_ReviewVersionLifecycle(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-review-version-org")
	projectID := createTestProject(t, db, orgID, "Issue Review Version Project")
	reviewerID := createIssueTestAgent(t, db, orgID, "review-version-reviewer")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Review version issue",
		Origin:       "local",
		DocumentPath: stringPtr("/posts/2026-02-06-review-version.md"),
	})
	require.NoError(t, err)

	first, err := issueStore.UpsertReviewVersion(
		ctx,
		issue.ID,
		"/posts/2026-02-06-review-version.md",
		"review-sha-1",
		&reviewerID,
	)
	require.NoError(t, err)
	require.Equal(t, issue.ID, first.IssueID)
	require.Equal(t, "review-sha-1", first.ReviewCommitSHA)
	require.NotNil(t, first.ReviewerAgentID)
	require.Equal(t, reviewerID, *first.ReviewerAgentID)
	require.Nil(t, first.AddressedInCommitSHA)

	second, err := issueStore.UpsertReviewVersion(
		ctx,
		issue.ID,
		"/posts/2026-02-06-review-version.md",
		"review-sha-2",
		&reviewerID,
	)
	require.NoError(t, err)

	versions, err := issueStore.ListReviewVersions(ctx, issue.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, second.ReviewCommitSHA, versions[0].ReviewCommitSHA)
	require.Equal(t, first.ReviewCommitSHA, versions[1].ReviewCommitSHA)

	latestUnaddressed, err := issueStore.GetLatestUnaddressedReviewVersion(ctx, issue.ID)
	require.NoError(t, err)
	require.Equal(t, second.ReviewCommitSHA, latestUnaddressed.ReviewCommitSHA)

	addressed, err := issueStore.MarkLatestReviewVersionAddressed(ctx, issue.ID, "addressed-sha-1")
	require.NoError(t, err)
	require.Equal(t, second.ReviewCommitSHA, addressed.ReviewCommitSHA)
	require.NotNil(t, addressed.AddressedInCommitSHA)
	require.Equal(t, "addressed-sha-1", *addressed.AddressedInCommitSHA)
	require.NotNil(t, addressed.AddressedAt)

	addressed, err = issueStore.MarkLatestReviewVersionAddressed(ctx, issue.ID, "addressed-sha-2")
	require.NoError(t, err)
	require.Equal(t, first.ReviewCommitSHA, addressed.ReviewCommitSHA)
	require.NotNil(t, addressed.AddressedInCommitSHA)
	require.Equal(t, "addressed-sha-2", *addressed.AddressedInCommitSHA)

	_, err = issueStore.GetLatestUnaddressedReviewVersion(ctx, issue.ID)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))

	_, err = issueStore.MarkLatestReviewVersionAddressed(ctx, issue.ID, "addressed-sha-3")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestProjectIssueStore_CreateReviewNotificationDeduplicatesTuples(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "issue-review-notification-org")
	projectID := createTestProject(t, db, orgID, "Issue Review Notification Project")
	targetAgentID := createIssueTestAgent(t, db, orgID, "review-notification-target")

	issueStore := NewProjectIssueStore(db)
	ctx := ctxWithWorkspace(orgID)
	issue, err := issueStore.CreateIssue(ctx, CreateProjectIssueInput{
		ProjectID:    projectID,
		Title:        "Review notification issue",
		Origin:       "local",
		DocumentPath: stringPtr("/posts/2026-02-06-review-notification.md"),
	})
	require.NoError(t, err)

	first, created, err := issueStore.CreateReviewNotification(ctx, CreateProjectIssueReviewNotificationInput{
		IssueID:          issue.ID,
		NotificationType: IssueReviewNotificationSavedForOwner,
		TargetAgentID:    targetAgentID,
		ReviewCommitSHA:  "review-sha-1",
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, first)

	second, created, err := issueStore.CreateReviewNotification(ctx, CreateProjectIssueReviewNotificationInput{
		IssueID:          issue.ID,
		NotificationType: IssueReviewNotificationSavedForOwner,
		TargetAgentID:    targetAgentID,
		ReviewCommitSHA:  "review-sha-1",
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Nil(t, second)

	addressedSHA := "addressed-sha-1"
	third, created, err := issueStore.CreateReviewNotification(ctx, CreateProjectIssueReviewNotificationInput{
		IssueID:              issue.ID,
		NotificationType:     IssueReviewNotificationAddressedForReviewer,
		TargetAgentID:        targetAgentID,
		ReviewCommitSHA:      "review-sha-1",
		AddressedInCommitSHA: &addressedSHA,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, third)
	require.Equal(t, addressedSHA, third.AddressedInCommitSHA)
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

	counts, err := issueStore.GetProjectIssueCounts(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, 4, counts.Total)
	require.Equal(t, 3, counts.Open)
	require.Equal(t, 1, counts.Closed)
	require.Equal(t, 3, counts.GitHubOrigin)
	require.Equal(t, 1, counts.LocalOrigin)
	require.Equal(t, 1, counts.PullRequests)

	firstSyncAt := time.Now().UTC().Add(-1 * time.Hour)
	_, err = issueStore.UpsertSyncCheckpoint(ctx, UpsertProjectIssueSyncCheckpointInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		Resource:           "issues",
		Cursor:             stringPtr("cursor-1"),
		LastSyncedAt:       &firstSyncAt,
	})
	require.NoError(t, err)

	secondSyncAt := time.Now().UTC()
	_, err = issueStore.UpsertSyncCheckpoint(ctx, UpsertProjectIssueSyncCheckpointInput{
		ProjectID:          projectID,
		RepositoryFullName: "samhotchkiss/otter-camp",
		Resource:           "pull_requests",
		Cursor:             stringPtr("cursor-2"),
		LastSyncedAt:       &secondSyncAt,
	})
	require.NoError(t, err)

	checkpoints, err := issueStore.ListSyncCheckpoints(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, checkpoints, 2)
	require.Equal(t, "pull_requests", checkpoints[0].Resource)
	require.Equal(t, "issues", checkpoints[1].Resource)
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
