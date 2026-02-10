package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAgentMemoryStoreCreateListSearch(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-memory-store")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-agent', 'Memory Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewAgentMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	createdDaily, err := store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindDaily,
		Content: "Shipped parser guard",
	})
	require.NoError(t, err)
	require.Equal(t, AgentMemoryKindDaily, createdDaily.Kind)
	require.NotNil(t, createdDaily.Date)

	d := time.Now().UTC().AddDate(0, 0, -1)
	createdLongTerm, err := store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindLongTerm,
		Date:    &d,
		Content: "Always verify identity before writes",
	})
	require.NoError(t, err)
	require.Equal(t, AgentMemoryKindLongTerm, createdLongTerm.Kind)

	daily, longTerm, err := store.ListByAgent(ctx, agentID, 3, true)
	require.NoError(t, err)
	require.NotEmpty(t, daily)
	require.NotEmpty(t, longTerm)

	results, err := store.SearchByAgent(ctx, agentID, "verify identity", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
}

func TestAgentMemoryStoreRejectsInvalidAgentID(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-memory-invalid")
	store := NewAgentMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.Create(ctx, CreateAgentMemoryInput{
		AgentID: "not-a-uuid",
		Kind:    AgentMemoryKindDaily,
		Content: "bad",
	})
	require.ErrorIs(t, err, ErrAgentMemoryInvalidAgentID)
}

func TestAgentMemoryStoreRejectsDuplicateDailyEntryForSameDate(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-memory-daily-unique")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-agent-dupe', 'Memory Agent Dupe', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewAgentMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)
	date := time.Date(2026, time.February, 9, 0, 0, 0, 0, time.UTC)

	_, err = store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindDaily,
		Date:    &date,
		Content: "Daily note #1",
	})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindDaily,
		Date:    &date,
		Content: "Daily note #2 duplicate",
	})
	require.Error(t, err)
}
