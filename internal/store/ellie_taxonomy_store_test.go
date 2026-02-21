package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieTaxonomyStoreCreateGetList(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "taxonomy-store-org")
	taxonomyStore := NewEllieTaxonomyStore(db)

	root, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "technical",
		DisplayName: "Technical",
		Description: taxonomyPtr("Technical decisions and patterns"),
	})
	require.NoError(t, err)
	require.Equal(t, 0, root.Depth)
	require.Nil(t, root.ParentID)

	child, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    taxonomyPtr(root.ID),
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)
	require.Equal(t, 1, child.Depth)
	require.NotNil(t, child.ParentID)
	require.Equal(t, root.ID, *child.ParentID)

	loaded, err := taxonomyStore.GetNodeByID(context.Background(), orgID, child.ID)
	require.NoError(t, err)
	require.Equal(t, child.ID, loaded.ID)
	require.Equal(t, "embeddings", loaded.Slug)
	require.Equal(t, 1, loaded.Depth)

	roots, err := taxonomyStore.ListNodesByParent(context.Background(), orgID, nil, 10)
	require.NoError(t, err)
	require.Len(t, roots, 1)
	require.Equal(t, root.ID, roots[0].ID)

	children, err := taxonomyStore.ListNodesByParent(context.Background(), orgID, taxonomyPtr(root.ID), 10)
	require.NoError(t, err)
	require.Len(t, children, 1)
	require.Equal(t, child.ID, children[0].ID)
}

func TestEllieTaxonomyStoreDuplicateSlugConflict(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "taxonomy-store-duplicate")
	taxonomyStore := NewEllieTaxonomyStore(db)

	_, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "process",
		DisplayName: "Process",
	})
	require.NoError(t, err)

	_, err = taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "process",
		DisplayName: "Process Duplicate",
	})
	require.ErrorIs(t, err, ErrConflict)
}

func TestEllieTaxonomyStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "taxonomy-store-org-a")
	orgB := createTestOrganization(t, db, "taxonomy-store-org-b")
	taxonomyStore := NewEllieTaxonomyStore(db)

	nodeA, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgA,
		Slug:        "agents",
		DisplayName: "Agents",
	})
	require.NoError(t, err)

	_, err = taxonomyStore.GetNodeByID(context.Background(), orgB, nodeA.ID)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestEllieMemoryTaxonomyStoreUpsertAndList(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "memory-taxonomy-store-org")
	memoryID := insertTaxonomyTestMemory(t, db, orgID, "Embedding rollout", "We moved to 1536 embeddings.")
	taxonomyStore := NewEllieTaxonomyStore(db)

	root, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)

	child, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    taxonomyPtr(root.ID),
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	err = taxonomyStore.UpsertMemoryClassification(context.Background(), UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgID,
		MemoryID:   memoryID,
		NodeID:     child.ID,
		Confidence: 0.73,
	})
	require.NoError(t, err)

	err = taxonomyStore.UpsertMemoryClassification(context.Background(), UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgID,
		MemoryID:   memoryID,
		NodeID:     child.ID,
		Confidence: 0.91,
	})
	require.NoError(t, err)

	classifications, err := taxonomyStore.ListMemoryClassifications(context.Background(), orgID, memoryID)
	require.NoError(t, err)
	require.Len(t, classifications, 1)
	require.Equal(t, memoryID, classifications[0].MemoryID)
	require.Equal(t, child.ID, classifications[0].NodeID)
	require.Equal(t, "technical/embeddings", classifications[0].NodePath)
	require.InDelta(t, 0.91, classifications[0].Confidence, 0.0001)
}

func TestEllieMemoryTaxonomyStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "memory-taxonomy-org-a")
	orgB := createTestOrganization(t, db, "memory-taxonomy-org-b")
	taxonomyStore := NewEllieTaxonomyStore(db)

	memoryA := insertTaxonomyTestMemory(t, db, orgA, "Org A memory", "Scoped to org A")
	nodeB, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgB,
		Slug:        "personal",
		DisplayName: "Personal",
	})
	require.NoError(t, err)

	err = taxonomyStore.UpsertMemoryClassification(context.Background(), UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgA,
		MemoryID:   memoryA,
		NodeID:     nodeB.ID,
		Confidence: 0.6,
	})
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestEllieTaxonomyStoreReparentPreventsCycles(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "taxonomy-store-reparent-cycle")
	taxonomyStore := NewEllieTaxonomyStore(db)

	root, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "projects",
		DisplayName: "Projects",
	})
	require.NoError(t, err)

	child, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    taxonomyPtr(root.ID),
		Slug:        "otter-camp",
		DisplayName: "Otter Camp",
	})
	require.NoError(t, err)

	_, err = taxonomyStore.ReparentNode(context.Background(), orgID, root.ID, taxonomyPtr(child.ID))
	require.ErrorIs(t, err, ErrConflict)
}

func TestEllieTaxonomyStoreListMemoriesBySubtree(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "taxonomy-store-subtree")
	taxonomyStore := NewEllieTaxonomyStore(db)

	root, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		Slug:        "technical",
		DisplayName: "Technical",
	})
	require.NoError(t, err)

	child, err := taxonomyStore.CreateNode(context.Background(), CreateEllieTaxonomyNodeInput{
		OrgID:       orgID,
		ParentID:    taxonomyPtr(root.ID),
		Slug:        "embeddings",
		DisplayName: "Embeddings",
	})
	require.NoError(t, err)

	memoryID := insertTaxonomyTestMemory(t, db, orgID, "Embedding migration", "1536 dimensional switch")
	require.NoError(t, taxonomyStore.UpsertMemoryClassification(context.Background(), UpsertEllieMemoryTaxonomyInput{
		OrgID:      orgID,
		MemoryID:   memoryID,
		NodeID:     child.ID,
		Confidence: 0.8,
	}))

	memories, err := taxonomyStore.ListMemoriesBySubtree(context.Background(), orgID, root.ID, 50)
	require.NoError(t, err)
	require.Len(t, memories, 1)
	require.Equal(t, memoryID, memories[0].MemoryID)
}

func insertTaxonomyTestMemory(t *testing.T, db *sql.DB, orgID, title, content string) string {
	t.Helper()
	var memoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, metadata, status)
		 VALUES ($1, 'fact', $2, $3, '{}'::jsonb, 'active')
		 RETURNING id`,
		orgID,
		title,
		content,
	).Scan(&memoryID)
	require.NoError(t, err)
	return memoryID
}

func taxonomyPtr(value string) *string {
	return &value
}
