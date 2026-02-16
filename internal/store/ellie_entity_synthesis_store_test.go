package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEllieEntitySynthesisStoreListCandidatesByMentionThreshold(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "entity-synth-threshold-org")
	projectID := createTestProject(t, db, orgID, "Entity Synth Threshold Project")

	base := time.Date(2026, 2, 16, 18, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertEntitySynthesisTestMemory(t, db, orgID, projectID, fmt.Sprintf("ItsAlive update %d", i), fmt.Sprintf("ItsAlive shipped module %d", i), base.Add(time.Duration(i)*time.Minute), "{}")
	}
	for i := 0; i < 4; i++ {
		insertEntitySynthesisTestMemory(t, db, orgID, projectID, fmt.Sprintf("OtterCamp note %d", i), fmt.Sprintf("OtterCamp refinement %d", i), base.Add(10*time.Minute+time.Duration(i)*time.Minute), "{}")
	}

	store := NewEllieEntitySynthesisStore(db)
	candidates, err := store.ListCandidates(context.Background(), orgID, 5, 25)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "itsalive", candidates[0].EntityKey)
	require.Equal(t, 5, candidates[0].MentionCount)
	require.False(t, candidates[0].NeedsResynthesis)
	require.Nil(t, candidates[0].ExistingSynthesisMemoryID)
}

func TestEllieEntitySynthesisStoreListCandidatesNeedsResynthesisOnGrowth(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "entity-synth-growth-org")
	projectID := createTestProject(t, db, orgID, "Entity Synth Growth Project")

	base := time.Date(2026, 2, 16, 18, 20, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertEntitySynthesisTestMemory(t, db, orgID, projectID, fmt.Sprintf("ItsAlive initial %d", i), fmt.Sprintf("ItsAlive fact %d", i), base.Add(time.Duration(i)*time.Minute), "{}")
	}

	existingSynthesisID := insertEntitySynthesisTestMemory(
		t,
		db,
		orgID,
		projectID,
		"ItsAlive definition",
		"ItsAlive is the internal retrieval service.",
		base.Add(6*time.Minute),
		`{"source_type":"synthesis","entity_key":"itsalive","source_memory_count":5}`,
	)

	store := NewEllieEntitySynthesisStore(db)
	beforeGrowth, err := store.ListCandidates(context.Background(), orgID, 5, 25)
	require.NoError(t, err)
	require.Empty(t, beforeGrowth)

	insertEntitySynthesisTestMemory(t, db, orgID, projectID, "ItsAlive hotfix", "ItsAlive now supports project docs indexing.", base.Add(7*time.Minute), "{}")
	afterGrowth, err := store.ListCandidates(context.Background(), orgID, 5, 25)
	require.NoError(t, err)
	require.Len(t, afterGrowth, 1)
	require.Equal(t, "itsalive", afterGrowth[0].EntityKey)
	require.Equal(t, 6, afterGrowth[0].MentionCount)
	require.True(t, afterGrowth[0].NeedsResynthesis)
	require.NotNil(t, afterGrowth[0].ExistingSynthesisMemoryID)
	require.Equal(t, existingSynthesisID, *afterGrowth[0].ExistingSynthesisMemoryID)
	require.Equal(t, 5, afterGrowth[0].ExistingSourceMemoryCount)
}

func TestEllieEntitySynthesisStoreListCandidatesOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "entity-synth-org-a")
	orgB := createTestOrganization(t, db, "entity-synth-org-b")
	projectA := createTestProject(t, db, orgA, "Entity Synth Org A")
	projectB := createTestProject(t, db, orgB, "Entity Synth Org B")

	base := time.Date(2026, 2, 16, 18, 40, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		insertEntitySynthesisTestMemory(t, db, orgA, projectA, fmt.Sprintf("ItsAlive orgA %d", i), "ItsAlive signal for orgA", base.Add(time.Duration(i)*time.Minute), "{}")
	}
	for i := 0; i < 8; i++ {
		insertEntitySynthesisTestMemory(t, db, orgB, projectB, fmt.Sprintf("ItsAlive orgB %d", i), "ItsAlive signal for orgB", base.Add(20*time.Minute+time.Duration(i)*time.Minute), "{}")
	}
	insertEntitySynthesisTestMemory(t, db, orgB, projectB, "ItsAlive orgB synthesis", "ItsAlive synthesized in orgB", base.Add(30*time.Minute), `{"source_type":"synthesis","entity_key":"itsalive","source_memory_count":8}`)

	store := NewEllieEntitySynthesisStore(db)
	candidates, err := store.ListCandidates(context.Background(), orgA, 5, 25)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, "itsalive", candidates[0].EntityKey)
	require.Equal(t, 5, candidates[0].MentionCount)
	require.Nil(t, candidates[0].ExistingSynthesisMemoryID)
}

func insertEntitySynthesisTestMemory(
	t *testing.T,
	db *sql.DB,
	orgID string,
	projectID string,
	title string,
	content string,
	occurredAt time.Time,
	metadataJSON string,
) string {
	t.Helper()

	var memoryID string
	err := db.QueryRow(
		`INSERT INTO memories (org_id, kind, title, content, metadata, status, source_project_id, occurred_at)
		 VALUES ($1, 'fact', $2, $3, $4::jsonb, 'active', $5, $6)
		 RETURNING id`,
		orgID,
		title,
		content,
		metadataJSON,
		projectID,
		occurredAt,
	).Scan(&memoryID)
	require.NoError(t, err)
	return memoryID
}
