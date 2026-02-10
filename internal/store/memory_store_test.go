package store

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestMemoryStoreCreateListSearchRecallDelete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "memory-store-org")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-store-agent', 'Memory Store Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	created, err := store.Create(ctx, CreateMemoryEntryInput{
		AgentID:       agentID,
		Kind:          MemoryKindDecision,
		Title:         "Adopt pgvector for semantic recall",
		Content:       "Use vector-backed search to improve recall quality.",
		Importance:    5,
		Confidence:    0.85,
		Sensitivity:   MemorySensitivityInternal,
		OccurredAt:    time.Now().UTC(),
		SourceIssue:   memoryStringPtr("111-memory-infrastructure-overhaul"),
		SourceSession: memoryStringPtr("session-123"),
	})
	require.NoError(t, err)
	require.Equal(t, MemoryKindDecision, created.Kind)
	require.Equal(t, "Adopt pgvector for semantic recall", created.Title)
	require.Equal(t, 5, created.Importance)
	require.Equal(t, MemoryStatusActive, created.Status)

	listed, err := store.ListByAgent(ctx, agentID, MemoryKindDecision, 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)

	results, err := store.Search(ctx, MemorySearchParams{
		AgentID:       agentID,
		Query:         "pgvector recall",
		Kinds:         []string{MemoryKindDecision},
		MinRelevance:  0.5,
		MinImportance: 3,
		AllowedScopes: []string{MemorySensitivityInternal},
		Limit:         5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotNil(t, results[0].Relevance)
	require.Equal(t, created.ID, results[0].ID)

	recall, err := store.GetRecallContext(ctx, agentID, "semantic recall", RecallContextConfig{
		MaxResults:    3,
		MinRelevance:  0.5,
		MinImportance: 3,
		MaxChars:      500,
	})
	require.NoError(t, err)
	require.Contains(t, recall, "[RECALLED CONTEXT]")
	require.Contains(t, recall, "Adopt pgvector for semantic recall")

	err = store.Delete(ctx, created.ID)
	require.NoError(t, err)

	postDelete, err := store.Search(ctx, MemorySearchParams{
		AgentID: agentID,
		Query:   "pgvector",
		Limit:   5,
	})
	require.NoError(t, err)
	require.Len(t, postDelete, 0)
}

func TestGetRecallContextTruncatesUTF8Safely(t *testing.T) {
	recall := "[RECALLED CONTEXT]\n- [fact] emoji: prefix🙂suffix"
	// Split in the middle of the emoji rune.
	maxChars := len("[RECALLED CONTEXT]\n- [fact] emoji: prefix") + 1
	truncated := truncateRecallText(recall, maxChars)

	require.True(t, utf8.ValidString(truncated))
	require.Equal(t, "[RECALLED CONTEXT]\n- [fact] emoji: prefix", truncated)
}

func TestMemoryStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "memory-store-org-a")
	orgB := createTestOrganization(t, db, "memory-store-org-b")

	var agentA string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-store-agent-a', 'Memory Store Agent A', 'active')
		 RETURNING id`,
		orgA,
	).Scan(&agentA)
	require.NoError(t, err)

	var agentB string
	err = db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-store-agent-b', 'Memory Store Agent B', 'active')
		 RETURNING id`,
		orgB,
	).Scan(&agentB)
	require.NoError(t, err)

	store := NewMemoryStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	entryA, err := store.Create(ctxA, CreateMemoryEntryInput{
		AgentID:    agentA,
		Kind:       MemoryKindFact,
		Title:      "Org A fact",
		Content:    "Only org A should see this entry.",
		Importance: 4,
		Confidence: 0.9,
	})
	require.NoError(t, err)

	_, err = store.Create(ctxB, CreateMemoryEntryInput{
		AgentID:    agentB,
		Kind:       MemoryKindFact,
		Title:      "Org B fact",
		Content:    "Only org B should see this entry.",
		Importance: 4,
		Confidence: 0.9,
	})
	require.NoError(t, err)

	resultsA, err := store.Search(ctxA, MemorySearchParams{
		AgentID: agentA,
		Query:   "fact",
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, resultsA, 1)
	require.Equal(t, "Org A fact", resultsA[0].Title)

	resultsB, err := store.Search(ctxB, MemorySearchParams{
		AgentID: agentB,
		Query:   "fact",
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, resultsB, 1)
	require.Equal(t, "Org B fact", resultsB[0].Title)

	err = store.Delete(ctxB, entryA.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestMemoryStoreValidation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "memory-store-validation")
	store := NewMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)

	_, err := store.Create(ctx, CreateMemoryEntryInput{
		AgentID: "not-a-uuid",
		Kind:    MemoryKindFact,
		Title:   "Invalid agent",
		Content: "Should fail.",
	})
	require.Error(t, err)

	_, err = store.Create(ctx, CreateMemoryEntryInput{
		AgentID:    "00000000-0000-0000-0000-000000000000",
		Kind:       "invalid-kind",
		Title:      "Invalid kind",
		Content:    "Should fail.",
		Importance: 3,
		Confidence: 0.5,
	})
	require.Error(t, err)

	_, err = store.Search(ctx, MemorySearchParams{
		AgentID: "not-a-uuid",
		Query:   "x",
	})
	require.Error(t, err)

	_, err = store.Search(ctx, MemorySearchParams{
		AgentID: "00000000-0000-0000-0000-000000000000",
		Query:   "   ",
	})
	require.Error(t, err)
}

func TestMemoryStoreCreateDuplicateReturnsErrDuplicateMemory(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "memory-store-dedup")

	var agentID string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-store-dedup-agent', 'Memory Store Dedup Agent', 'active')
		 RETURNING id`,
		orgID,
	).Scan(&agentID)
	require.NoError(t, err)

	store := NewMemoryStore(db)
	ctx := ctxWithWorkspace(orgID)
	input := CreateMemoryEntryInput{
		AgentID:     agentID,
		Kind:        MemoryKindFact,
		Title:       "dedup-test-title",
		Content:     "dedup-test-content",
		Importance:  3,
		Confidence:  0.8,
		Sensitivity: MemorySensitivityInternal,
	}

	_, err = store.Create(ctx, input)
	require.NoError(t, err)

	_, err = store.Create(ctx, input)
	require.ErrorIs(t, err, ErrDuplicateMemory)
}

func TestMemoryStoreUpdateStatus(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "memory-store-status-a")
	orgB := createTestOrganization(t, db, "memory-store-status-b")

	var agentA string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, 'memory-status-agent-a', 'Memory Status Agent A', 'active')
		 RETURNING id`,
		orgA,
	).Scan(&agentA)
	require.NoError(t, err)

	store := NewMemoryStore(db)
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	created, err := store.Create(ctxA, CreateMemoryEntryInput{
		AgentID:    agentA,
		Kind:       MemoryKindDecision,
		Title:      "status transition seed",
		Content:    "initial status should be active",
		Importance: 3,
		Confidence: 0.7,
	})
	require.NoError(t, err)
	require.Equal(t, MemoryStatusActive, created.Status)

	require.NoError(t, store.UpdateStatus(ctxA, created.ID, MemoryStatusWarm))
	listed, err := store.ListByAgent(ctxA, agentA, "", 10, 0)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, MemoryStatusWarm, listed[0].Status)

	require.ErrorIs(t, store.UpdateStatus(ctxA, created.ID, MemoryStatusActive), ErrMemoryInvalidStatusTransition)
	require.NoError(t, store.UpdateStatus(ctxA, created.ID, MemoryStatusArchived))
	require.ErrorIs(t, store.UpdateStatus(ctxA, created.ID, MemoryStatusWarm), ErrMemoryInvalidStatusTransition)
	require.ErrorIs(t, store.UpdateStatus(ctxA, created.ID, "invalid"), ErrMemoryInvalidStatus)

	require.ErrorIs(t, store.UpdateStatus(ctxB, created.ID, MemoryStatusWarm), ErrNotFound)
}

func memoryStringPtr(value string) *string {
	return &value
}
