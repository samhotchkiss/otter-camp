package store

import (
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func createAgentJobTestAgent(t *testing.T, db *sql.DB, orgID, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, $2, $3, 'active')
		 RETURNING id`,
		orgID,
		slug,
		"Agent "+slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestAgentJobStoreCreateListGetUpdateDelete(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-job-store-crud")
	agentID := createAgentJobTestAgent(t, db, orgID, "jobs-crud-agent")
	ctx := ctxWithWorkspace(orgID)

	store := NewAgentJobStore(db)
	now := time.Date(2026, 2, 12, 18, 45, 0, 0, time.UTC)
	nextRunAt := now.Add(10 * time.Minute)
	maxFailures := 7
	createdBy := "11111111-2222-3333-4444-555555555555"

	created, err := store.Create(ctx, CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Heartbeat",
		Description:  agentJobStrPtr("check status"),
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(30000),
		Timezone:     agentJobStrPtr("UTC"),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "Run heartbeat check",
		Enabled:      agentJobBoolPtr(true),
		NextRunAt:    &nextRunAt,
		MaxFailures:  &maxFailures,
		CreatedBy:    &createdBy,
	})
	require.NoError(t, err)
	require.Equal(t, orgID, created.OrgID)
	require.Equal(t, agentID, created.AgentID)
	require.Equal(t, AgentJobScheduleInterval, created.ScheduleKind)
	require.Equal(t, int64(30000), derefInt64(t, created.IntervalMS))
	require.Equal(t, AgentJobPayloadMessage, created.PayloadKind)
	require.Equal(t, "Run heartbeat check", created.PayloadText)
	require.True(t, created.Enabled)
	require.Equal(t, AgentJobStatusActive, created.Status)
	require.Equal(t, maxFailures, created.MaxFailures)

	listed, err := store.List(ctx, AgentJobFilter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, created.ID, listed[0].ID)

	loaded, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, loaded.ID)
	require.Equal(t, "Heartbeat", loaded.Name)

	updatedNext := now.Add(30 * time.Minute)
	updated, err := store.Update(ctx, created.ID, UpdateAgentJobInput{
		Name:        agentJobStrPtr("Heartbeat Updated"),
		PayloadText: agentJobStrPtr("Run heartbeat check now"),
		Enabled:     agentJobBoolPtr(false),
		NextRunAt:   &updatedNext,
	})
	require.NoError(t, err)
	require.Equal(t, "Heartbeat Updated", updated.Name)
	require.Equal(t, "Run heartbeat check now", updated.PayloadText)
	require.False(t, updated.Enabled)
	require.WithinDuration(t, updatedNext, derefTime(t, updated.NextRunAt), time.Second)

	require.NoError(t, store.Delete(ctx, created.ID))
	_, err = store.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAgentJobStoreOrgIsolation(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgA := createTestOrganization(t, db, "agent-job-store-org-a")
	orgB := createTestOrganization(t, db, "agent-job-store-org-b")
	agentA := createAgentJobTestAgent(t, db, orgA, "jobs-org-a")
	agentB := createAgentJobTestAgent(t, db, orgB, "jobs-org-b")

	store := NewAgentJobStore(db)
	now := time.Date(2026, 2, 12, 19, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(2 * time.Minute)

	jobA, err := store.Create(ctxWithWorkspace(orgA), CreateAgentJobInput{
		AgentID:      agentA,
		Name:         "Org A Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "A",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)
	_, err = store.Create(ctxWithWorkspace(orgB), CreateAgentJobInput{
		AgentID:      agentB,
		Name:         "Org B Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "B",
		NextRunAt:    &nextRunAt,
	})
	require.NoError(t, err)

	listA, err := store.List(ctxWithWorkspace(orgA), AgentJobFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, listA, 1)
	require.Equal(t, "Org A Job", listA[0].Name)

	listB, err := store.List(ctxWithWorkspace(orgB), AgentJobFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, listB, 1)
	require.Equal(t, "Org B Job", listB[0].Name)

	_, err = store.GetByID(ctxWithWorkspace(orgB), jobA.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAgentJobStorePickupDueUsesSkipLockedAndLeasesRows(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-job-store-pickup")
	agentID := createAgentJobTestAgent(t, db, orgID, "jobs-pickup-agent")
	ctx := ctxWithWorkspace(orgID)
	store := NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 19, 15, 0, 0, time.UTC)
	dueAt := now.Add(-1 * time.Minute)
	futureAt := now.Add(5 * time.Minute)

	_, err := store.Create(ctx, CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Due Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "due",
		NextRunAt:    &dueAt,
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Future Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "future",
		NextRunAt:    &futureAt,
	})
	require.NoError(t, err)

	picked, err := store.PickupDue(ctx, 10, now)
	require.NoError(t, err)
	require.Len(t, picked, 1)
	require.Equal(t, "Due Job", picked[0].Name)
	require.Nil(t, picked[0].NextRunAt)

	pickedAgain, err := store.PickupDue(ctx, 10, now)
	require.NoError(t, err)
	require.Len(t, pickedAgain, 0)

	source, err := os.ReadFile("agent_job_store.go")
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(string(source)), "for update skip locked")
}

func TestAgentJobStorePruneRunHistory(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-job-store-prune")
	agentID := createAgentJobTestAgent(t, db, orgID, "jobs-prune-agent")
	ctx := ctxWithWorkspace(orgID)
	store := NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 19, 30, 0, 0, time.UTC)
	nextRun := now.Add(1 * time.Minute)

	job, err := store.Create(ctx, CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Prune Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "prune",
		NextRunAt:    &nextRun,
	})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		startedAt := now.Add(time.Duration(i) * time.Minute)
		run, err := store.StartRun(ctx, StartAgentJobRunInput{
			JobID:       job.ID,
			PayloadText: "run",
			StartedAt:   startedAt,
		})
		require.NoError(t, err)
		completedAt := startedAt.Add(2 * time.Second)
		_, err = store.CompleteRun(ctx, CompleteAgentJobRunInput{
			JobID:       job.ID,
			RunID:       run.ID,
			RunStatus:   AgentJobRunStatusSuccess,
			CompletedAt: completedAt,
			NextRunAt:   &nextRun,
		})
		require.NoError(t, err)
	}

	pruned, err := store.PruneRunHistory(ctx, job.ID, 2)
	require.NoError(t, err)
	require.Equal(t, 3, pruned)

	runs, err := store.ListRuns(ctx, job.ID, 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
}

func TestAgentJobStoreCleanupStaleRunning(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)
	orgID := createTestOrganization(t, db, "agent-job-store-stale")
	agentID := createAgentJobTestAgent(t, db, orgID, "jobs-stale-agent")
	ctx := ctxWithWorkspace(orgID)
	store := NewAgentJobStore(db)

	now := time.Date(2026, 2, 12, 20, 0, 0, 0, time.UTC)
	nextRun := now.Add(1 * time.Minute)

	job, err := store.Create(ctx, CreateAgentJobInput{
		AgentID:      agentID,
		Name:         "Stale Job",
		ScheduleKind: AgentJobScheduleInterval,
		IntervalMS:   agentJobInt64Ptr(60000),
		PayloadKind:  AgentJobPayloadMessage,
		PayloadText:  "stale",
		NextRunAt:    &nextRun,
	})
	require.NoError(t, err)

	staleRun, err := store.StartRun(ctx, StartAgentJobRunInput{
		JobID:       job.ID,
		PayloadText: "stale",
		StartedAt:   now.Add(-20 * time.Minute),
	})
	require.NoError(t, err)
	_, err = store.StartRun(ctx, StartAgentJobRunInput{
		JobID:       job.ID,
		PayloadText: "fresh",
		StartedAt:   now.Add(-1 * time.Minute),
	})
	require.NoError(t, err)

	cleaned, err := store.CleanupStaleRuns(ctx, 5*time.Minute, now)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned)

	runs, err := store.ListRuns(ctx, job.ID, 10)
	require.NoError(t, err)
	var staleStatus string
	for _, run := range runs {
		if run.ID == staleRun.ID {
			staleStatus = run.Status
		}
	}
	require.Equal(t, AgentJobRunStatusTimeout, staleStatus)
}

func agentJobStrPtr(v string) *string {
	return &v
}

func agentJobBoolPtr(v bool) *bool {
	return &v
}

func agentJobInt64Ptr(v int64) *int64 {
	return &v
}

func derefInt64(t *testing.T, value *int64) int64 {
	t.Helper()
	require.NotNil(t, value)
	return *value
}

func derefTime(t *testing.T, value *time.Time) time.Time {
	t.Helper()
	require.NotNil(t, value)
	return *value
}
