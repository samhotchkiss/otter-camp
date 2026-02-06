package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeEntryStoreReplaceAndListEntries(t *testing.T) {
	db := setupTestDatabase(t, getTestDatabaseURL(t))
	orgID := createTestOrganization(t, db, "knowledge-store-org")
	ctx := ctxWithWorkspace(orgID)

	store := NewKnowledgeEntryStore(db)
	inserted, err := store.ReplaceEntries(ctx, []ReplaceKnowledgeEntryInput{
		{
			Title:     "Alpha entry",
			Content:   "Alpha body",
			Tags:      []string{"Ops", "Sync", "ops"},
			CreatedBy: "Stone",
		},
		{
			Title:     "Beta entry",
			Content:   "Beta body",
			Tags:      []string{"Writing"},
			CreatedBy: "Sam",
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, inserted)

	entries, err := store.ListEntries(ctx, 50)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "Beta entry", entries[0].Title)
	require.Equal(t, "Alpha entry", entries[1].Title)
	require.Equal(t, []string{"ops", "sync"}, entries[1].Tags)

	inserted, err = store.ReplaceEntries(ctx, []ReplaceKnowledgeEntryInput{
		{
			Title:     "Gamma entry",
			Content:   "Gamma body",
			Tags:      []string{"Data"},
			CreatedBy: "Stone",
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, inserted)

	entries, err = store.ListEntries(ctx, 50)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "Gamma entry", entries[0].Title)
}

func TestKnowledgeEntryStoreWorkspaceIsolation(t *testing.T) {
	db := setupTestDatabase(t, getTestDatabaseURL(t))
	orgA := createTestOrganization(t, db, "knowledge-store-org-a")
	orgB := createTestOrganization(t, db, "knowledge-store-org-b")

	store := NewKnowledgeEntryStore(db)
	_, err := store.ReplaceEntries(ctxWithWorkspace(orgA), []ReplaceKnowledgeEntryInput{
		{
			Title:     "Org A entry",
			Content:   "Alpha",
			CreatedBy: "A",
		},
	})
	require.NoError(t, err)
	_, err = store.ReplaceEntries(ctxWithWorkspace(orgB), []ReplaceKnowledgeEntryInput{
		{
			Title:     "Org B entry",
			Content:   "Beta",
			CreatedBy: "B",
		},
	})
	require.NoError(t, err)

	entriesA, err := store.ListEntries(ctxWithWorkspace(orgA), 20)
	require.NoError(t, err)
	require.Len(t, entriesA, 1)
	require.Equal(t, "Org A entry", entriesA[0].Title)

	entriesB, err := store.ListEntries(ctxWithWorkspace(orgB), 20)
	require.NoError(t, err)
	require.Len(t, entriesB, 1)
	require.Equal(t, "Org B entry", entriesB[0].Title)
}

func TestKnowledgeEntryStoreValidation(t *testing.T) {
	db := setupTestDatabase(t, getTestDatabaseURL(t))
	orgID := createTestOrganization(t, db, "knowledge-store-validate-org")
	store := NewKnowledgeEntryStore(db)

	_, err := store.ReplaceEntries(ctxWithWorkspace(orgID), []ReplaceKnowledgeEntryInput{
		{
			Title:   "Missing content",
			Content: "   ",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "content is required")

	_, err = store.ReplaceEntries(ctxWithWorkspace(orgID), []ReplaceKnowledgeEntryInput{
		{
			Title:   "   ",
			Content: "Body",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "title is required")

	_, err = store.ListEntries(context.Background(), 20)
	require.ErrorIs(t, err, ErrNoWorkspace)
}
