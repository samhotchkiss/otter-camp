package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func insertProjectChatTestProject(t *testing.T, db Querier, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRowContext(
		context.Background(),
		"INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id",
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func setProjectChatMessageCreatedAt(t *testing.T, db Querier, messageID string, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(
		context.Background(),
		"UPDATE project_chat_messages SET created_at = $1, updated_at = $1 WHERE id = $2",
		createdAt.UTC(),
		messageID,
	)
	require.NoError(t, err)
}

func TestProjectChatStoreCreateListPaginationOrder(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "chat-order-org")
	ctx := ctxWithWorkspace(orgID)
	projectID := insertProjectChatTestProject(t, db, orgID, "Chat Project")

	chatStore := NewProjectChatStore(db)

	first, err := chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Sam",
		Body:      "First thought",
	})
	require.NoError(t, err)

	second, err := chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Stone",
		Body:      "Second thought",
	})
	require.NoError(t, err)

	third, err := chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Sam",
		Body:      "Third thought",
	})
	require.NoError(t, err)

	base := time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC)
	setProjectChatMessageCreatedAt(t, db, first.ID, base)
	setProjectChatMessageCreatedAt(t, db, second.ID, base.Add(1*time.Minute))
	setProjectChatMessageCreatedAt(t, db, third.ID, base.Add(2*time.Minute))

	pageOne, hasMore, err := chatStore.List(ctx, projectID, 2, nil, nil)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, pageOne, 2)
	require.Equal(t, third.ID, pageOne[0].ID)
	require.Equal(t, second.ID, pageOne[1].ID)

	beforeCreatedAt := pageOne[1].CreatedAt
	beforeID := pageOne[1].ID

	pageTwo, hasMore, err := chatStore.List(ctx, projectID, 2, &beforeCreatedAt, &beforeID)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, pageTwo, 1)
	require.Equal(t, first.ID, pageTwo[0].ID)
}

func TestProjectChatStoreWorkspaceIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "chat-org-a")
	orgB := createTestOrganization(t, db, "chat-org-b")

	projectA := insertProjectChatTestProject(t, db, orgA, "Org A Project")
	projectB := insertProjectChatTestProject(t, db, orgB, "Org B Project")

	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	chatStore := NewProjectChatStore(db)

	_, err := chatStore.Create(ctxA, CreateProjectChatMessageInput{
		ProjectID: projectA,
		Author:    "Sam",
		Body:      "Visible only to org A",
	})
	require.NoError(t, err)

	_, err = chatStore.Create(ctxB, CreateProjectChatMessageInput{
		ProjectID: projectA,
		Author:    "Stone",
		Body:      "Cross-org write should fail",
	})
	require.ErrorIs(t, err, ErrForbidden)

	_, _, err = chatStore.List(ctxB, projectA, 20, nil, nil)
	require.ErrorIs(t, err, ErrForbidden)

	messagesB, hasMoreB, err := chatStore.List(ctxB, projectB, 20, nil, nil)
	require.NoError(t, err)
	require.False(t, hasMoreB)
	require.Empty(t, messagesB)
}

func TestProjectChatStoreSearchRankingAndFilters(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "chat-search-org")
	projectID := insertProjectChatTestProject(t, db, orgID, "Search Project")
	otherProjectID := insertProjectChatTestProject(t, db, orgID, "Other Project")

	chatStore := NewProjectChatStore(db)
	ctx := ctxWithWorkspace(orgID)

	exact, err := chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Sam",
		Body:      "Launch plan draft for newsletter launch plan",
	})
	require.NoError(t, err)

	loose, err := chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: projectID,
		Author:    "Stone",
		Body:      "We should launch next month with a plan for the release",
	})
	require.NoError(t, err)

	_, err = chatStore.Create(ctx, CreateProjectChatMessageInput{
		ProjectID: otherProjectID,
		Author:    "Sam",
		Body:      "Launch plan but in another project",
	})
	require.NoError(t, err)

	base := time.Date(2026, 2, 7, 11, 0, 0, 0, time.UTC)
	setProjectChatMessageCreatedAt(t, db, exact.ID, base)
	setProjectChatMessageCreatedAt(t, db, loose.ID, base.Add(1*time.Minute))

	results, err := chatStore.Search(ctx, SearchProjectChatInput{
		ProjectID: projectID,
		Query:     "launch plan",
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, exact.ID, results[0].Message.ID)
	require.NotEmpty(t, results[0].Snippet)

	author := "Stone"
	filteredByAuthor, err := chatStore.Search(ctx, SearchProjectChatInput{
		ProjectID: projectID,
		Query:     "launch plan",
		Author:    &author,
	})
	require.NoError(t, err)
	require.Len(t, filteredByAuthor, 1)
	require.Equal(t, loose.ID, filteredByAuthor[0].Message.ID)

	from := base.Add(30 * time.Second)
	filteredByTime, err := chatStore.Search(ctx, SearchProjectChatInput{
		ProjectID: projectID,
		Query:     "launch plan",
		From:      &from,
	})
	require.NoError(t, err)
	require.Len(t, filteredByTime, 1)
	require.Equal(t, loose.ID, filteredByTime[0].Message.ID)
}
