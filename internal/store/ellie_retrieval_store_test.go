package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieRetrievalStoreProjectAndOrgScopes(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-scope-org")
	projectID := createTestProject(t, db, orgID, "Ellie Retrieval Scope Project")

	store := NewEllieRetrievalStore(db)

	var projectMemoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Project DB choice', 'Project chose Postgres', 'active', $2)
		 RETURNING id`,
		orgID,
		projectID,
	).Scan(&projectMemoryID)
	require.NoError(t, err)

	var orgMemoryID string
	err = db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, status)
		 VALUES ($1, 'preference', 'Org DB preference', 'Sam prefers explicit SQL migrations', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&orgMemoryID)
	require.NoError(t, err)

	projectResults, err := store.SearchMemoriesByProject(context.Background(), orgID, projectID, "postgres", 10)
	require.NoError(t, err)
	require.Len(t, projectResults, 1)
	require.Equal(t, projectMemoryID, projectResults[0].MemoryID)

	orgResults, err := store.SearchMemoriesOrgWide(context.Background(), orgID, "sql", 10)
	require.NoError(t, err)
	require.Len(t, orgResults, 1)
	require.Equal(t, orgMemoryID, orgResults[0].MemoryID)
}

func TestEllieRetrievalStoreKeywordScaffoldBehavior(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "ellie-retrieval-keyword-scaffold-org")
	projectID := createTestProject(t, db, orgID, "Keyword Scaffold Project")

	retrievalStore := NewEllieRetrievalStore(db)

	_, err := db.Exec(
		`INSERT INTO memories (org_id, kind, title, content, status, source_project_id)
		 VALUES ($1, 'technical_decision', 'Storage', 'We chose Postgres as the persistence layer', 'active', $2)`,
		orgID,
		projectID,
	)
	require.NoError(t, err)

	results, err := retrievalStore.SearchMemoriesByProject(context.Background(), orgID, projectID, "database choice", 10)
	require.NoError(t, err)
	require.Empty(t, results)
}
