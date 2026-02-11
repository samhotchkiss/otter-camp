package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func insertChatThreadTestUser(t *testing.T, db *sql.DB, orgID, subject string) string {
	t.Helper()

	var userID string
	err := db.QueryRow(
		`INSERT INTO users (org_id, subject, issuer, display_name, email)
		 VALUES ($1, $2, 'tests', $3, $4)
		 RETURNING id`,
		orgID,
		subject,
		"User "+subject,
		subject+"@example.com",
	).Scan(&userID)
	require.NoError(t, err)
	return userID
}

func insertChatThreadTestIssue(t *testing.T, db *sql.DB, orgID, projectID, title string) string {
	t.Helper()

	var issueID string
	err := db.QueryRow(
		`INSERT INTO project_issues (org_id, project_id, issue_number, title, state, origin)
		 VALUES (
			 $1,
			 $2,
			 COALESCE((SELECT MAX(issue_number) + 1 FROM project_issues WHERE project_id = $2), 1),
			 $3,
			 'open',
			 'local'
		 )
		 RETURNING id`,
		orgID,
		projectID,
		title,
	).Scan(&issueID)
	require.NoError(t, err)
	return issueID
}

func TestChatThreadStore_TouchListArchiveUnarchive(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "chat-thread-store-org")
	userID := insertChatThreadTestUser(t, db, orgID, "chat-thread-user")
	projectID := createTestProject(t, db, orgID, "chat-thread-project")
	issueID := insertChatThreadTestIssue(t, db, orgID, projectID, "chat-thread-issue")

	ctx := ctxWithWorkspace(orgID)
	store := NewChatThreadStore(db)

	base := time.Date(2026, 2, 11, 9, 0, 0, 0, time.UTC)

	dmThread, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "dm:dm_main",
		ThreadType:         ChatThreadTypeDM,
		Title:              "Main Agent",
		LastMessagePreview: "First DM",
		LastMessageAt:      base,
	})
	require.NoError(t, err)
	require.NotEmpty(t, dmThread.ID)

	projectThread, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "project:" + projectID,
		ThreadType:         ChatThreadTypeProject,
		ProjectID:          &projectID,
		Title:              "Project Alpha",
		LastMessagePreview: "Project update",
		LastMessageAt:      base.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	issueThread, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "issue:" + issueID,
		ThreadType:         ChatThreadTypeIssue,
		ProjectID:          &projectID,
		IssueID:            &issueID,
		Title:              "Issue #1",
		LastMessagePreview: "Issue comment",
		LastMessageAt:      base.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	dmThreadUpdated, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "dm:dm_main",
		ThreadType:         ChatThreadTypeDM,
		Title:              "Main Agent",
		LastMessagePreview: "Latest DM",
		LastMessageAt:      base.Add(3 * time.Minute),
	})
	require.NoError(t, err)
	require.Equal(t, dmThread.ID, dmThreadUpdated.ID)
	require.Equal(t, "Latest DM", dmThreadUpdated.LastMessagePreview)

	active, err := store.ListByUser(ctx, userID, ListChatThreadsInput{
		Archived: false,
		Limit:    20,
	})
	require.NoError(t, err)
	require.Len(t, active, 3)
	require.Equal(t, dmThreadUpdated.ID, active[0].ID)
	require.Equal(t, issueThread.ID, active[1].ID)
	require.Equal(t, projectThread.ID, active[2].ID)

	archivedThread, err := store.Archive(ctx, userID, projectThread.ID, "")
	require.NoError(t, err)
	require.NotNil(t, archivedThread.ArchivedAt)
	require.Nil(t, archivedThread.AutoArchivedReason)

	activeAfterArchive, err := store.ListByUser(ctx, userID, ListChatThreadsInput{
		Archived: false,
		Limit:    20,
	})
	require.NoError(t, err)
	require.Len(t, activeAfterArchive, 2)
	require.Equal(t, dmThreadUpdated.ID, activeAfterArchive[0].ID)
	require.Equal(t, issueThread.ID, activeAfterArchive[1].ID)

	archivedList, err := store.ListByUser(ctx, userID, ListChatThreadsInput{
		Archived: true,
		Limit:    20,
	})
	require.NoError(t, err)
	require.Len(t, archivedList, 1)
	require.Equal(t, projectThread.ID, archivedList[0].ID)

	unarchived, err := store.Unarchive(ctx, userID, projectThread.ID)
	require.NoError(t, err)
	require.Nil(t, unarchived.ArchivedAt)
	require.Nil(t, unarchived.AutoArchivedReason)
}

func TestChatThreadStore_WorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "chat-thread-org-a")
	orgB := createTestOrganization(t, db, "chat-thread-org-b")
	userA := insertChatThreadTestUser(t, db, orgA, "chat-thread-user-a")
	userB := insertChatThreadTestUser(t, db, orgB, "chat-thread-user-b")

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)
	store := NewChatThreadStore(db)

	thread, err := store.TouchThread(ctxA, TouchChatThreadInput{
		UserID:             userA,
		ThreadKey:          "dm:dm_a",
		ThreadType:         ChatThreadTypeDM,
		Title:              "A Thread",
		LastMessagePreview: "hello",
		LastMessageAt:      time.Now().UTC(),
	})
	require.NoError(t, err)

	_, err = store.GetByIDForUser(ctxB, userB, thread.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = store.Archive(ctxB, userB, thread.ID, "")
	require.ErrorIs(t, err, ErrNotFound)

	threadsB, err := store.ListByUser(ctxB, userB, ListChatThreadsInput{
		Archived: false,
		Limit:    20,
	})
	require.NoError(t, err)
	require.Empty(t, threadsB)
}

func TestChatThreadStore_AutoArchiveByIssueAndProject(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "chat-thread-auto-archive-org")
	userID := insertChatThreadTestUser(t, db, orgID, "chat-thread-auto-user")
	projectID := createTestProject(t, db, orgID, "chat-thread-auto-project")
	issueID := insertChatThreadTestIssue(t, db, orgID, projectID, "auto archive issue")

	ctx := ctxWithWorkspace(orgID)
	store := NewChatThreadStore(db)
	now := time.Now().UTC()

	issueThread, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "issue:" + issueID,
		ThreadType:         ChatThreadTypeIssue,
		ProjectID:          &projectID,
		IssueID:            &issueID,
		Title:              "Issue Thread",
		LastMessagePreview: "issue message",
		LastMessageAt:      now,
	})
	require.NoError(t, err)

	projectThread, err := store.TouchThread(ctx, TouchChatThreadInput{
		UserID:             userID,
		ThreadKey:          "project:" + projectID,
		ThreadType:         ChatThreadTypeProject,
		ProjectID:          &projectID,
		Title:              "Project Thread",
		LastMessagePreview: "project message",
		LastMessageAt:      now.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	affectedByIssue, err := store.AutoArchiveByIssue(ctx, issueID)
	require.NoError(t, err)
	require.EqualValues(t, 1, affectedByIssue)

	issueThreadAfter, err := store.GetByIDForUser(ctx, userID, issueThread.ID)
	require.NoError(t, err)
	require.NotNil(t, issueThreadAfter.ArchivedAt)
	require.NotNil(t, issueThreadAfter.AutoArchivedReason)
	require.Equal(t, ChatThreadArchiveReasonIssueClosed, *issueThreadAfter.AutoArchivedReason)

	affectedByProject, err := store.AutoArchiveByProject(ctx, projectID)
	require.NoError(t, err)
	require.EqualValues(t, 1, affectedByProject)

	projectThreadAfter, err := store.GetByIDForUser(ctx, userID, projectThread.ID)
	require.NoError(t, err)
	require.NotNil(t, projectThreadAfter.ArchivedAt)
	require.NotNil(t, projectThreadAfter.AutoArchivedReason)
	require.Equal(t, ChatThreadArchiveReasonProjectArchived, *projectThreadAfter.AutoArchivedReason)
}
