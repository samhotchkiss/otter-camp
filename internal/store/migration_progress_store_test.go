package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationProgressStoreCreateAndGet(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "migration-progress-start-org")
	progressStore := NewMigrationProgressStore(db)

	created, err := progressStore.StartPhase(context.Background(), StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "agent_import",
		TotalItems:    migrationProgressIntPtr(42),
		CurrentLabel:  migrationProgressStringPtr("starting import"),
	})
	require.NoError(t, err)
	require.Equal(t, orgID, created.OrgID)
	require.Equal(t, "agent_import", created.MigrationType)
	require.Equal(t, MigrationProgressStatusRunning, created.Status)
	require.NotNil(t, created.TotalItems)
	require.Equal(t, 42, *created.TotalItems)
	require.Equal(t, 0, created.ProcessedItems)
	require.Equal(t, 0, created.FailedItems)
	require.Equal(t, "starting import", created.CurrentLabel)

	loaded, err := progressStore.GetByType(context.Background(), orgID, "agent_import")
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, created.ID, loaded.ID)
	require.NotNil(t, loaded.TotalItems)
	require.Equal(t, 42, *loaded.TotalItems)
}

func TestMigrationProgressStoreAdvanceAndFail(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "migration-progress-advance-org")
	progressStore := NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "history_backfill",
		TotalItems:    migrationProgressIntPtr(100),
		CurrentLabel:  migrationProgressStringPtr("bootstrapping"),
	})
	require.NoError(t, err)

	advanced, err := progressStore.Advance(context.Background(), AdvanceMigrationProgressInput{
		OrgID:          orgID,
		MigrationType:  "history_backfill",
		ProcessedDelta: 25,
		FailedDelta:    2,
		CurrentLabel:   migrationProgressStringPtr("processed first batch"),
	})
	require.NoError(t, err)
	require.Equal(t, 25, advanced.ProcessedItems)
	require.Equal(t, 2, advanced.FailedItems)
	require.Equal(t, "processed first batch", advanced.CurrentLabel)

	failed, err := progressStore.SetStatus(context.Background(), SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "history_backfill",
		Status:        MigrationProgressStatusFailed,
		Error:         migrationProgressStringPtr("batch parse failed"),
	})
	require.NoError(t, err)
	require.Equal(t, MigrationProgressStatusFailed, failed.Status)
	require.NotNil(t, failed.Error)
	require.Equal(t, "batch parse failed", *failed.Error)
}

func TestMigrationProgressStorePauseResume(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "migration-progress-pause-org")
	progressStore := NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), StartMigrationProgressInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		TotalItems:    migrationProgressIntPtr(5),
		CurrentLabel:  migrationProgressStringPtr("starting"),
	})
	require.NoError(t, err)

	paused, err := progressStore.SetStatus(context.Background(), SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        MigrationProgressStatusPaused,
	})
	require.NoError(t, err)
	require.Equal(t, MigrationProgressStatusPaused, paused.Status)

	resumed, err := progressStore.SetStatus(context.Background(), SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        MigrationProgressStatusRunning,
		CurrentLabel:  migrationProgressStringPtr("resumed"),
	})
	require.NoError(t, err)
	require.Equal(t, MigrationProgressStatusRunning, resumed.Status)
	require.Equal(t, "resumed", resumed.CurrentLabel)

	completed, err := progressStore.SetStatus(context.Background(), SetMigrationProgressStatusInput{
		OrgID:         orgID,
		MigrationType: "memory_extraction",
		Status:        MigrationProgressStatusCompleted,
	})
	require.NoError(t, err)
	require.Equal(t, MigrationProgressStatusCompleted, completed.Status)
	require.NotNil(t, completed.CompletedAt)
}

func TestMigrationProgressStoreOrgScoping(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgA := createTestOrganization(t, db, "migration-progress-org-a")
	orgB := createTestOrganization(t, db, "migration-progress-org-b")
	progressStore := NewMigrationProgressStore(db)

	_, err := progressStore.StartPhase(context.Background(), StartMigrationProgressInput{
		OrgID:         orgA,
		MigrationType: "history_backfill",
		TotalItems:    migrationProgressIntPtr(10),
		CurrentLabel:  migrationProgressStringPtr("org-a"),
	})
	require.NoError(t, err)

	_, err = progressStore.StartPhase(context.Background(), StartMigrationProgressInput{
		OrgID:         orgB,
		MigrationType: "history_backfill",
		TotalItems:    migrationProgressIntPtr(20),
		CurrentLabel:  migrationProgressStringPtr("org-b"),
	})
	require.NoError(t, err)

	_, err = progressStore.Advance(context.Background(), AdvanceMigrationProgressInput{
		OrgID:          orgA,
		MigrationType:  "history_backfill",
		ProcessedDelta: 7,
		FailedDelta:    1,
	})
	require.NoError(t, err)

	loadedA, err := progressStore.GetByType(context.Background(), orgA, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, loadedA)
	require.Equal(t, 7, loadedA.ProcessedItems)
	require.Equal(t, 1, loadedA.FailedItems)

	loadedB, err := progressStore.GetByType(context.Background(), orgB, "history_backfill")
	require.NoError(t, err)
	require.NotNil(t, loadedB)
	require.Equal(t, 0, loadedB.ProcessedItems)
	require.Equal(t, 0, loadedB.FailedItems)
	require.Equal(t, "org-b", loadedB.CurrentLabel)
}

func migrationProgressIntPtr(value int) *int {
	return &value
}

func migrationProgressStringPtr(value string) *string {
	return &value
}
