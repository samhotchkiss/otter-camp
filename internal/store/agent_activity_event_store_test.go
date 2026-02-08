package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentActivityEventStoreCreate(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentActivityEventStore(db)
	startedAt := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(8 * time.Second)

	event, err := store.Create(ctx, CreateAgentActivityEventInput{
		ID:          "01HZZ000000000000000000001",
		AgentID:     "main",
		SessionKey:  "agent:main:main",
		Trigger:     "chat.slack",
		Channel:     "slack",
		Summary:     "Responded to leadership update",
		Detail:      "Acknowledged progress summary request",
		ThreadID:    "thread-1",
		TokensUsed:  4200,
		ModelUsed:   "opus-4-6",
		DurationMs:  8000,
		Status:      "completed",
		StartedAt:   startedAt,
		CompletedAt: &completedAt,
	})
	require.NoError(t, err)
	require.NotNil(t, event)

	assert.Equal(t, orgID, event.OrgID)
	assert.Equal(t, "main", event.AgentID)
	assert.Equal(t, "chat.slack", event.Trigger)
	assert.Equal(t, "slack", event.Channel)
	assert.Equal(t, "Responded to leadership update", event.Summary)
	assert.Equal(t, 4200, event.TokensUsed)
	assert.Equal(t, int64(8000), event.DurationMs)
	require.NotNil(t, event.CompletedAt)
	assert.Equal(t, completedAt.UTC(), event.CompletedAt.UTC())
}

func TestAgentActivityEventStoreListByAgent(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-list-agent")
	ctx := ctxWithWorkspace(orgID)

	projectA := createAgentActivityProject(t, db, orgID, "project-a")
	projectB := createAgentActivityProject(t, db, orgID, "project-b")

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 10, 0, 0, 0, time.UTC)

	_, err := store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000101",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Channel:   "slack",
		Summary:   "Slack response",
		ProjectID: projectA,
		Status:    "completed",
		StartedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000102",
		AgentID:   "main",
		Trigger:   "cron.scheduled",
		Channel:   "cron",
		Summary:   "Cron run failed",
		ProjectID: projectA,
		Status:    "failed",
		StartedAt: base.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000103",
		AgentID:   "main",
		Trigger:   "cron.scheduled",
		Channel:   "cron",
		Summary:   "Cron run recovered",
		ProjectID: projectB,
		Status:    "completed",
		StartedAt: base.Add(3 * time.Minute),
	})
	require.NoError(t, err)

	firstPage, err := store.ListByAgent(ctx, "main", ListAgentActivityOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	assert.Equal(t, "01HZZ000000000000000000103", firstPage[0].ID)
	assert.Equal(t, "01HZZ000000000000000000102", firstPage[1].ID)

	before := firstPage[0].StartedAt
	older, err := store.ListByAgent(ctx, "main", ListAgentActivityOptions{
		Limit:  10,
		Before: &before,
	})
	require.NoError(t, err)
	require.Len(t, older, 2)
	assert.Equal(t, "01HZZ000000000000000000102", older[0].ID)
	assert.Equal(t, "01HZZ000000000000000000101", older[1].ID)

	filtered, err := store.ListByAgent(ctx, "main", ListAgentActivityOptions{
		Limit:   10,
		Trigger: "cron.scheduled",
		Channel: "cron",
		Status:  "failed",
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "01HZZ000000000000000000102", filtered[0].ID)

	projectFiltered, err := store.ListByAgent(ctx, "main", ListAgentActivityOptions{
		Limit:     10,
		ProjectID: projectA,
	})
	require.NoError(t, err)
	require.Len(t, projectFiltered, 2)
	assert.Equal(t, projectA, projectFiltered[0].ProjectID)
	assert.Equal(t, projectA, projectFiltered[1].ProjectID)
}

func TestAgentActivityEventStoreListRecent(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "agent-activity-recent-a")
	orgB := createTestOrganization(t, db, "agent-activity-recent-b")
	ctxA := ctxWithWorkspace(orgA)
	ctxB := ctxWithWorkspace(orgB)

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 11, 0, 0, 0, time.UTC)

	_, err := store.Create(ctxA, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000201",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Org A event 1",
		Status:    "completed",
		StartedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)

	_, err = store.Create(ctxA, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000202",
		AgentID:   "nova",
		Trigger:   "cron.scheduled",
		Summary:   "Org A event 2",
		Status:    "completed",
		StartedAt: base.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	_, err = store.Create(ctxB, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000203",
		AgentID:   "other",
		Trigger:   "chat.slack",
		Summary:   "Org B event",
		Status:    "completed",
		StartedAt: base.Add(3 * time.Minute),
	})
	require.NoError(t, err)

	recentA, err := store.ListRecent(ctxA, ListAgentActivityOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, recentA, 2)
	assert.Equal(t, "01HZZ000000000000000000202", recentA[0].ID)
	assert.Equal(t, "01HZZ000000000000000000201", recentA[1].ID)
	for _, event := range recentA {
		assert.Equal(t, orgA, event.OrgID)
	}

	onlyNova, err := store.ListRecent(ctxA, ListAgentActivityOptions{
		Limit:   10,
		AgentID: "nova",
	})
	require.NoError(t, err)
	require.Len(t, onlyNova, 1)
	assert.Equal(t, "nova", onlyNova[0].AgentID)
}

func TestAgentActivityEventStoreCreateNoWorkspace(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewAgentActivityEventStore(db)
	event, err := store.Create(context.Background(), CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000901",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Missing workspace",
		StartedAt: time.Now().UTC(),
	})
	assert.Error(t, err)
	assert.Nil(t, event)
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestAgentActivityEventStoreBatchCreate(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-batch-create")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 9, 0, 0, 0, time.UTC)

	err := store.CreateEvents(ctx, []CreateAgentActivityEventInput{
		{
			ID:        "01HZZ000000000000000000301",
			AgentID:   "main",
			Trigger:   "chat.slack",
			Summary:   "Batch event 1",
			Status:    "completed",
			StartedAt: base.Add(1 * time.Minute),
		},
		{
			ID:        "01HZZ000000000000000000302",
			AgentID:   "nova",
			Trigger:   "cron.scheduled",
			Summary:   "Batch event 2",
			Status:    "completed",
			StartedAt: base.Add(2 * time.Minute),
		},
	})
	require.NoError(t, err)

	recent, err := store.ListRecent(ctx, ListAgentActivityOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, recent, 2)
	assert.Equal(t, "01HZZ000000000000000000302", recent[0].ID)
	assert.Equal(t, "01HZZ000000000000000000301", recent[1].ID)
}

func TestAgentActivityEventStoreLatestByAgent(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-latest")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 9, 0, 0, 0, time.UTC)

	_, err := store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000401",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Main old",
		Status:    "completed",
		StartedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000402",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Main new",
		Status:    "completed",
		StartedAt: base.Add(3 * time.Minute),
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000403",
		AgentID:   "nova",
		Trigger:   "cron.scheduled",
		Summary:   "Nova latest",
		Status:    "completed",
		StartedAt: base.Add(2 * time.Minute),
	})
	require.NoError(t, err)

	latest, err := store.LatestByAgent(ctx)
	require.NoError(t, err)
	require.Len(t, latest, 2)
	assert.Equal(t, "01HZZ000000000000000000402", latest["main"].ID)
	assert.Equal(t, "01HZZ000000000000000000403", latest["nova"].ID)
}

func TestAgentActivityEventStoreCountByAgentSince(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-count")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 9, 0, 0, 0, time.UTC)

	_, err := store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000501",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Older event",
		Status:    "completed",
		StartedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000502",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Recent event",
		Status:    "completed",
		StartedAt: base.Add(5 * time.Minute),
	})
	require.NoError(t, err)

	count, err := store.CountByAgentSince(ctx, "main", base.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestAgentActivityEventStoreCleanupOlderThan(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-activity-cleanup")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentActivityEventStore(db)
	base := time.Date(2026, 2, 8, 9, 0, 0, 0, time.UTC)

	_, err := store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000601",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "Old event",
		Status:    "completed",
		StartedAt: base.Add(1 * time.Minute),
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentActivityEventInput{
		ID:        "01HZZ000000000000000000602",
		AgentID:   "main",
		Trigger:   "chat.slack",
		Summary:   "New event",
		Status:    "completed",
		StartedAt: base.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	deleted, err := store.CleanupOlderThan(ctx, base.Add(5*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	remaining, err := store.ListRecent(ctx, ListAgentActivityOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "01HZZ000000000000000000602", remaining[0].ID)
}

func createAgentActivityProject(t *testing.T, db *sql.DB, orgID, name string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO projects (org_id, name, status) VALUES ($1, $2, 'active') RETURNING id`,
		orgID,
		name,
	).Scan(&id)
	require.NoError(t, err)
	return id
}
