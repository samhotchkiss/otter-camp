package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
)

func ellieProjectDocsEmbeddingVector(values ...float64) []float64 {
	vector := make([]float64, 1536)
	for i, value := range values {
		if i >= len(vector) {
			break
		}
		vector[i] = value
	}
	return vector
}

func TestEllieProjectDocsStoreUpsertAndIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "ellie-project-docs-org-a")
	orgB := createTestOrganization(t, db, "ellie-project-docs-org-b")
	projectA := createTestProject(t, db, orgA, "Project Docs A")
	projectB := createTestProject(t, db, orgB, "Project Docs B")

	docsStore := NewEllieProjectDocsStore(db)
	ctx := context.Background()

	_, err := docsStore.UpsertProjectDoc(ctx, UpsertEllieProjectDocInput{
		OrgID:            orgA,
		ProjectID:        projectA,
		FilePath:         "docs/overview.md",
		Title:            "Overview",
		Summary:          "Initial summary",
		SummaryEmbedding: ellieProjectDocsEmbeddingVector(1, 0, 0),
		ContentHash:      "hash-v1",
	})
	require.NoError(t, err)

	_, err = docsStore.UpsertProjectDoc(ctx, UpsertEllieProjectDocInput{
		OrgID:            orgA,
		ProjectID:        projectA,
		FilePath:         "docs/overview.md",
		Title:            "Overview",
		Summary:          "Updated summary",
		SummaryEmbedding: ellieProjectDocsEmbeddingVector(0.5, 0.5, 0),
		ContentHash:      "hash-v2",
	})
	require.NoError(t, err)

	_, err = docsStore.UpsertProjectDoc(ctx, UpsertEllieProjectDocInput{
		OrgID:            orgB,
		ProjectID:        projectB,
		FilePath:         "docs/overview.md",
		Title:            "Overview B",
		Summary:          "Org B summary",
		SummaryEmbedding: ellieProjectDocsEmbeddingVector(0, 1, 0),
		ContentHash:      "hash-b1",
	})
	require.NoError(t, err)

	docsA, err := docsStore.ListActiveProjectDocs(ctx, orgA, projectA)
	require.NoError(t, err)
	require.Len(t, docsA, 1)
	require.Equal(t, "docs/overview.md", docsA[0].FilePath)
	require.Equal(t, "Updated summary", docsA[0].Summary)
	require.Equal(t, "hash-v2", docsA[0].ContentHash)

	docsB, err := docsStore.ListActiveProjectDocs(ctx, orgB, projectB)
	require.NoError(t, err)
	require.Len(t, docsB, 1)
	require.Equal(t, "docs/overview.md", docsB[0].FilePath)
	require.Equal(t, "Org B summary", docsB[0].Summary)
	require.Equal(t, "hash-b1", docsB[0].ContentHash)

	var rowCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM ellie_project_docs
		 WHERE org_id = $1
		   AND project_id = $2
		   AND file_path = 'docs/overview.md'`,
		orgA,
		projectA,
	).Scan(&rowCount)
	require.NoError(t, err)
	require.Equal(t, 1, rowCount, "upsert should not duplicate project docs rows")
}

func TestEllieProjectDocsStoreMarksInactiveOnDelete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-project-docs-delete-org")
	projectID := createTestProject(t, db, orgID, "Project Docs Delete")

	docsStore := NewEllieProjectDocsStore(db)
	ctx := context.Background()

	_, err := docsStore.UpsertProjectDoc(ctx, UpsertEllieProjectDocInput{
		OrgID:            orgID,
		ProjectID:        projectID,
		FilePath:         "docs/keep.md",
		Title:            "Keep",
		Summary:          "Keep summary",
		SummaryEmbedding: ellieProjectDocsEmbeddingVector(1, 0, 0),
		ContentHash:      "keep-hash",
	})
	require.NoError(t, err)
	_, err = docsStore.UpsertProjectDoc(ctx, UpsertEllieProjectDocInput{
		OrgID:            orgID,
		ProjectID:        projectID,
		FilePath:         "docs/delete.md",
		Title:            "Delete",
		Summary:          "Delete summary",
		SummaryEmbedding: ellieProjectDocsEmbeddingVector(0, 1, 0),
		ContentHash:      "delete-hash",
	})
	require.NoError(t, err)

	inactivated, err := docsStore.MarkProjectDocsInactiveExcept(ctx, orgID, projectID, []string{"docs/keep.md"})
	require.NoError(t, err)
	require.Equal(t, 1, inactivated)

	docs, err := docsStore.ListActiveProjectDocs(ctx, orgID, projectID)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "docs/keep.md", docs[0].FilePath)

	var (
		isActive  bool
		deletedAt sql.NullTime
	)
	err = db.QueryRow(
		`SELECT is_active, deleted_at
		 FROM ellie_project_docs
		 WHERE org_id = $1
		   AND project_id = $2
		   AND file_path = 'docs/delete.md'`,
		orgID,
		projectID,
	).Scan(&isActive, &deletedAt)
	require.NoError(t, err)
	require.False(t, isActive)
	require.True(t, deletedAt.Valid)
}
