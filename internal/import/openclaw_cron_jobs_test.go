package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func setupOpenClawCronImportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("OTTER_TEST_DATABASE_URL")
	if connStr == "" {
		t.Skip("set OTTER_TEST_DATABASE_URL to run openclaw cron import integration tests")
	}

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	migrationsDir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)

	m, err := migrate.New("file://"+migrationsDir, connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
		_ = db.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}
	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	return db
}

func openClawCronImportCtx(orgID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, orgID)
}

func openClawCronImportCreateOrg(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		"Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func openClawCronImportCreateAgent(t *testing.T, db *sql.DB, orgID, slug, displayName string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		`INSERT INTO agents (org_id, slug, display_name, status)
		 VALUES ($1, $2, $3, 'active')
		 RETURNING id`,
		orgID,
		slug,
		displayName,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestOpenClawCronJobImport(t *testing.T) {
	db := setupOpenClawCronImportTestDB(t)
	orgID := openClawCronImportCreateOrg(t, db, "openclaw-cron-import")
	agentID := openClawCronImportCreateAgent(t, db, orgID, "cron-agent", "Cron Agent")

	records := []OpenClawCronJobMetadata{
		{
			ID:            "cron-1",
			Name:          "Cron Check",
			Schedule:      "*/15 * * * *",
			SessionTarget: "cron-agent",
			PayloadType:   "message",
			PayloadText:   "check status",
		},
		{
			ID:            "interval-1",
			Name:          "Interval Check",
			Schedule:      "every 30s",
			SessionTarget: "agent:chameleon:oc:" + agentID,
			PayloadType:   "system_event",
			PayloadText:   "collect metrics",
		},
		{
			ID:            "once-1",
			Name:          "One Shot",
			Schedule:      "at 2026-02-14T01:30:00Z",
			SessionTarget: "Cron Agent",
			PayloadType:   "message",
		},
		{
			ID:            "missing-agent",
			Name:          "Missing Agent",
			Schedule:      "every 10s",
			SessionTarget: "does-not-exist",
			PayloadType:   "message",
			PayloadText:   "skip me",
		},
	}
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		openClawCronSyncMetadataKey,
		string(raw),
		time.Now().UTC(),
	)
	require.NoError(t, err)

	importer := NewOpenClawCronJobImporter(db)
	first, err := importer.ImportFromSyncMetadata(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 4, first.Total)
	require.Equal(t, 3, first.Imported)
	require.Equal(t, 0, first.Updated)
	require.Equal(t, 1, first.Skipped)
	require.Len(t, first.Warnings, 1)

	jobStore := store.NewAgentJobStore(db)
	jobs, err := jobStore.List(openClawCronImportCtx(orgID), store.AgentJobFilter{Limit: 100})
	require.NoError(t, err)
	require.Len(t, jobs, 3)

	seenKinds := map[string]bool{}
	for _, job := range jobs {
		require.False(t, job.Enabled)
		require.Equal(t, store.AgentJobStatusPaused, job.Status)
		seenKinds[job.ScheduleKind] = true
	}
	require.True(t, seenKinds[store.AgentJobScheduleCron])
	require.True(t, seenKinds[store.AgentJobScheduleInterval])
	require.True(t, seenKinds[store.AgentJobScheduleOnce])

	second, err := importer.ImportFromSyncMetadata(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, 4, second.Total)
	require.Equal(t, 0, second.Imported)
	require.Equal(t, 3, second.Updated)
	require.Equal(t, 1, second.Skipped)
	require.Len(t, second.Warnings, 1)

	jobsAfter, err := jobStore.List(openClawCronImportCtx(orgID), store.AgentJobFilter{Limit: 100})
	require.NoError(t, err)
	require.Len(t, jobsAfter, 3)
}
