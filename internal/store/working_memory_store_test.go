package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkingMemoryStoreCreateListCleanup(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "working-memory-store-org")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'working-memory-agent', 'Working Memory Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewWorkingMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	expiredAt := time.Now().UTC().Add(-1 * time.Hour)
	_, err = store.Create(ctx, CreateWorkingMemoryInput{
		AgentID:    agentID,
		SessionKey: "session-cleanup",
		Content:    "Expired scratch note",
		ExpiresAt:  &expiredAt,
	})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateWorkingMemoryInput{
		AgentID:    agentID,
		SessionKey: "session-cleanup",
		Content:    "Active scratch note",
	})
	require.NoError(t, err)

	beforeCleanup, err := store.ListBySession(ctx, agentID, "session-cleanup", 10)
	require.NoError(t, err)
	require.Len(t, beforeCleanup, 2)

	deleted, err := store.CleanupExpired(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	afterCleanup, err := store.ListBySession(ctx, agentID, "session-cleanup", 10)
	require.NoError(t, err)
	require.Len(t, afterCleanup, 1)
	require.Equal(t, "Active scratch note", afterCleanup[0].Content)
}

func TestWorkingMemoryStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "working-memory-org-a")
	orgB := createTestOrganization(t, db, "working-memory-org-b")

	var agentA string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'working-memory-agent-a', 'Working Memory Agent A', 'active')
		 RETURNING id`,
		orgA,
	).Scan(&agentA)
	require.NoError(t, err)

	var agentB string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'working-memory-agent-b', 'Working Memory Agent B', 'active')
		 RETURNING id`,
		orgB,
	).Scan(&agentB)
	require.NoError(t, err)

	store := NewWorkingMemoryStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	_, err = store.Create(ctxA, CreateWorkingMemoryInput{
		AgentID:    agentA,
		SessionKey: "shared-session-key",
		Content:    "Org A scratch",
	})
	require.NoError(t, err)
	_, err = store.Create(ctxB, CreateWorkingMemoryInput{
		AgentID:    agentB,
		SessionKey: "shared-session-key",
		Content:    "Org B scratch",
	})
	require.NoError(t, err)

	entriesA, err := store.ListBySession(ctxA, agentA, "shared-session-key", 10)
	require.NoError(t, err)
	require.Len(t, entriesA, 1)
	require.Equal(t, "Org A scratch", entriesA[0].Content)

	entriesB, err := store.ListBySession(ctxB, agentB, "shared-session-key", 10)
	require.NoError(t, err)
	require.Len(t, entriesB, 1)
	require.Equal(t, "Org B scratch", entriesB[0].Content)
}
