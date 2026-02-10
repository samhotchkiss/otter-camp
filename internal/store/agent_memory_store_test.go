package store

import (
	"context"
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

func TestAgentMemoryStoreSearchEscapesLikeWildcards(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-memory-search-wildcards")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-agent-wildcard', 'Memory Agent Wildcard', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewAgentMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err = store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindNote,
		Content: "Contains 100% certainty marker",
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindNote,
		Content: "Contains underscore_token marker",
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindNote,
		Content: "Ordinary note without wildcard tokens",
	})
	require.NoError(t, err)

	percentResults, err := store.SearchByAgent(ctx, agentID, "%", 10)
	require.NoError(t, err)
	require.Len(t, percentResults, 1)
	require.Contains(t, percentResults[0].Content, "100%")

	underscoreResults, err := store.SearchByAgent(ctx, agentID, "_", 10)
	require.NoError(t, err)
	require.Len(t, underscoreResults, 1)
	require.Contains(t, underscoreResults[0].Content, "underscore_token")
}

func TestEscapeLikePattern(t *testing.T) {
	got := escapeLikePattern(`100%_ready\set`)
	require.Equal(t, `100\%\_ready\\set`, got)
}

func TestAgentMemoryStoreUpdateAdvancesUpdatedAt(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-memory-updated-at")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-agent-updated', 'Memory Agent Updated', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewAgentMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	created, err := store.Create(ctx, CreateAgentMemoryInput{
		AgentID: agentID,
		Kind:    AgentMemoryKindLongTerm,
		Content: "Version 1",
	})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	conn, err := WithWorkspace(ctx, db)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(
		context.Background(),
		`UPDATE agent_memories
		 SET content = $1
		 WHERE id = $2`,
		"Version 2",
		created.ID,
	)
	require.NoError(t, err)

	var updatedAt time.Time
	err = conn.QueryRowContext(
		context.Background(),
		`SELECT updated_at
		 FROM agent_memories
		 WHERE id = $1`,
		created.ID,
	).Scan(&updatedAt)
	require.NoError(t, err)
	require.True(t, updatedAt.After(created.UpdatedAt), "updated_at should advance after UPDATE")
}
