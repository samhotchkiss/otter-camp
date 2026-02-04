package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

const testDBURLKey = "OTTER_TEST_DATABASE_URL"

func getTestDatabaseURL(t *testing.T) string {
	t.Helper()
	connStr := os.Getenv(testDBURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", testDBURLKey)
	}
	return connStr
}

func getMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	return dir
}

func setupTestDatabase(t *testing.T, connStr string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+getMigrationsDir(t), connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = m.Close()
	})

	err = m.Down()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func createTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO organizations (name, slug, tier) VALUES ($1, $2, 'free') RETURNING id",
		"Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func ctxWithWorkspace(workspaceID string) context.Context {
	return context.WithValue(context.Background(), middleware.WorkspaceIDKey, workspaceID)
}
