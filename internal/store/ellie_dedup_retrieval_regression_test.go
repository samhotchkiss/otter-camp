package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieRetrievalStoreExcludesDeprecatedMemoriesAfterDedup(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-dedup-retrieval-org")
	projectID := createTestProject(t, db, orgID, "Ellie Dedup Retrieval Project")

	var activeID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id, occurred_at)
		 VALUES ($1, 'fact', 'ItsAlive definition', 'ItsAlive is the active definition memory.', 'active', $2, $3)
		 RETURNING id`,
		orgID,
		projectID,
		time.Date(2026, 2, 16, 21, 0, 0, 0, time.UTC),
	).Scan(&activeID)
	require.NoError(t, err)

	var deprecatedID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id, occurred_at)
		 VALUES ($1, 'fact', 'ItsAlive duplicate', 'ItsAlive outdated duplicate memory.', 'deprecated', $2, $3)
		 RETURNING id`,
		orgID,
		projectID,
		time.Date(2026, 2, 16, 20, 59, 0, 0, time.UTC),
	).Scan(&deprecatedID)
	require.NoError(t, err)

	retrievalStore := NewEllieRetrievalStore(db)
	results, err := retrievalStore.SearchMemoriesOrgWide(context.Background(), orgID, "ItsAlive", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	foundActive := false
	for _, row := range results {
		require.NotEqual(t, deprecatedID, row.MemoryID)
		if row.MemoryID == activeID {
			foundActive = true
		}
	}
	require.True(t, foundActive)
}
