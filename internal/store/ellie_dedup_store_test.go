package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieDedupStoreReviewedPairsCanonicalizeOrder(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-dedup-canonical-org")
	projectID := createTestProject(t, db, orgID, "Ellie Dedup Canonical Project")
	memoryA := insertEllieDedupTestMemory(t, db, orgID, projectID, "Memory A", "Duplicate fact A")
	memoryB := insertEllieDedupTestMemory(t, db, orgID, projectID, "Memory B", "Duplicate fact B")

	s := NewEllieDedupStore(db)
	err := s.RecordReviewedPair(context.Background(), RecordEllieDedupReviewedPairInput{
		OrgID:     orgID,
		MemoryID1: memoryB,
		MemoryID2: memoryA,
		Decision:  "deprecated_b",
	})
	require.NoError(t, err)

	reviewed, err := s.IsPairReviewed(context.Background(), orgID, memoryA, memoryB)
	require.NoError(t, err)
	require.True(t, reviewed)

	var (
		storedA string
		storedB string
	)
	err = db.QueryRow(
		`SELECT memory_id_a::text, memory_id_b::text
		 FROM ellie_dedup_reviewed
		 WHERE org_id = $1`,
		orgID,
	).Scan(&storedA, &storedB)
	require.NoError(t, err)
	require.Less(t, storedA, storedB)
	require.Equal(t, memoryA, storedA)
	require.Equal(t, memoryB, storedB)
}

func TestEllieDedupStoreReviewedPairsOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "ellie-dedup-org-a")
	orgB := createTestOrganization(t, db, "ellie-dedup-org-b")
	projectA := createTestProject(t, db, orgA, "Org A Project")
	projectB := createTestProject(t, db, orgB, "Org B Project")
	memA1 := insertEllieDedupTestMemory(t, db, orgA, projectA, "A1", "Fact A1")
	memA2 := insertEllieDedupTestMemory(t, db, orgA, projectA, "A2", "Fact A2")
	memB1 := insertEllieDedupTestMemory(t, db, orgB, projectB, "B1", "Fact B1")
	memB2 := insertEllieDedupTestMemory(t, db, orgB, projectB, "B2", "Fact B2")

	s := NewEllieDedupStore(db)
	err := s.RecordReviewedPair(context.Background(), RecordEllieDedupReviewedPairInput{
		OrgID:     orgA,
		MemoryID1: memA1,
		MemoryID2: memA2,
		Decision:  "keep_both",
	})
	require.NoError(t, err)

	reviewedA, err := s.IsPairReviewed(context.Background(), orgA, memA1, memA2)
	require.NoError(t, err)
	require.True(t, reviewedA)

	reviewedB, err := s.IsPairReviewed(context.Background(), orgB, memB1, memB2)
	require.NoError(t, err)
	require.False(t, reviewedB)
}

func TestEllieDedupStoreCursorRoundTrip(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-dedup-cursor-org")
	s := NewEllieDedupStore(db)

	err := s.UpsertCursor(context.Background(), UpsertEllieDedupCursorInput{
		OrgID:             orgID,
		LastClusterKey:    ptrString("cluster:0003"),
		ProcessedClusters: 3,
		TotalClusters:     10,
	})
	require.NoError(t, err)

	cursor, err := s.GetCursor(context.Background(), orgID)
	require.NoError(t, err)
	require.NotNil(t, cursor)
	require.NotNil(t, cursor.LastClusterKey)
	require.Equal(t, "cluster:0003", *cursor.LastClusterKey)
	require.Equal(t, 3, cursor.ProcessedClusters)
	require.Equal(t, 10, cursor.TotalClusters)

	err = s.UpsertCursor(context.Background(), UpsertEllieDedupCursorInput{
		OrgID:             orgID,
		LastClusterKey:    ptrString("cluster:0008"),
		ProcessedClusters: 8,
		TotalClusters:     10,
	})
	require.NoError(t, err)

	updated, err := s.GetCursor(context.Background(), orgID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	require.NotNil(t, updated.LastClusterKey)
	require.Equal(t, "cluster:0008", *updated.LastClusterKey)
	require.Equal(t, 8, updated.ProcessedClusters)
	require.Equal(t, 10, updated.TotalClusters)
}

func insertEllieDedupTestMemory(t *testing.T, db *sql.DB, orgID, projectID, title, content string) string {
	t.Helper()
	var memoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, metadata, status, source_project_id, occurred_at)
		 VALUES ($1, 'fact', $2, $3, '{}'::jsonb, 'active', $4, $5)
		 RETURNING id`,
		orgID,
		title,
		fmt.Sprintf("%s %s", title, content),
		projectID,
		time.Date(2026, 2, 16, 20, 0, 0, 0, time.UTC),
	).Scan(&memoryID)
	require.NoError(t, err)
	return memoryID
}

func ptrString(value string) *string {
	trimmed := value
	return &trimmed
}
