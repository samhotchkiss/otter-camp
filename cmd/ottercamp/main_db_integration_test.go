//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestDBMigrateDryRunAndStatus(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("OTTERCAMP_DATABASE_URL", pool.Config().ConnString())

	migrationsDir := t.TempDir()
	migrationPath := filepath.Join(migrationsDir, "9999_cli_dry_run_probe.sql")
	if err := os.WriteFile(migrationPath, []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatalf("WriteFile migration: %v", err)
	}
	t.Setenv("OTTERCAMP_MIGRATIONS_PATH", migrationsDir)

	code, stdout, stderr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{"--dry-run"})
	})
	if code != 0 {
		t.Fatalf("db migrate --dry-run exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "pending 9999_cli_dry_run_probe") {
		t.Fatalf("db migrate --dry-run output = %q", stdout)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected dry-run not to apply migration, count=%d", count)
	}

	applyCode, applyOut, applyErr := captureCommandOutput(t, func() int {
		return runDBMigrate([]string{})
	})
	if applyCode != 0 {
		t.Fatalf("db migrate apply exit=%d stderr=%q", applyCode, applyErr)
	}
	if !strings.Contains(applyOut, "Applying 9999_cli_dry_run_probe... done (") {
		t.Fatalf("db migrate apply output = %q", applyOut)
	}

	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM schema_migrations WHERE version = 9999`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations after apply: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected apply to record migration, count=%d", count)
	}

	statusCode, statusOut, statusErr := captureCommandOutput(t, func() int {
		return runDBStatus([]string{"--output", "json"})
	})
	if statusCode != 0 {
		t.Fatalf("db status exit=%d stderr=%q", statusCode, statusErr)
	}
	if !strings.Contains(statusOut, `"version": 9999`) {
		t.Fatalf("db status output missing target migration: %q", statusOut)
	}
	if !strings.Contains(statusOut, `"applied": true`) {
		t.Fatalf("db status output missing applied flag: %q", statusOut)
	}
}
