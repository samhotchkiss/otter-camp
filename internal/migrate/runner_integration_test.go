//go:build integration

package migrate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRunnerAppliesMigrationsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbURL, cleanup := createEmptyIntegrationDatabase(t)
	defer cleanup()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	runner := NewRunner(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if err := runner.Run(ctx); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("migration row count = %d, want 1", count)
	}

	var typname string
	if err := pool.QueryRow(ctx, `SELECT typname FROM pg_type WHERE typname = 'vector'`).Scan(&typname); err != nil {
		t.Fatalf("vector type query failed: %v", err)
	}
	if typname != "vector" {
		t.Fatalf("typname = %q, want vector", typname)
	}
}

func TestRunnerAdvisoryLockPreventsConcurrentDoubleApply(t *testing.T) {
	ctx := context.Background()
	dbURL, cleanup := createEmptyIntegrationDatabase(t)
	defer cleanup()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	fsys := fstest.MapFS{
		"0001_sleep_then_create.sql": {Data: []byte("SELECT pg_sleep(0.2); CREATE TABLE lock_probe(id int primary key);")},
	}

	runnerA := NewRunnerWithFS(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), fsys)
	runnerB := NewRunnerWithFS(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), fsys)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		<-start
		errs <- runnerA.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- runnerB.Run(ctx)
	}()

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent runner returned error: %v", err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations count = %d, want 1", count)
	}
}

func TestRunnerRollsBackFailedMigrationAndPreservesPriorApplied(t *testing.T) {
	ctx := context.Background()
	dbURL, cleanup := createEmptyIntegrationDatabase(t)
	defer cleanup()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	fsys := fstest.MapFS{
		"0001_create_ok.sql":   {Data: []byte("CREATE TABLE ok_table(id int primary key);")},
		"0002_invalid_sql.sql": {Data: []byte("CREATE TABL broken (")},
	}

	runner := NewRunnerWithFS(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), fsys)
	err = runner.Run(ctx)
	if err == nil {
		t.Fatal("expected migration failure")
	}

	var count1 int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count1); err != nil {
		t.Fatalf("query schema_migrations for version 1: %v", err)
	}
	if count1 != 1 {
		t.Fatalf("version 1 count = %d, want 1", count1)
	}

	var count2 int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 2`).Scan(&count2); err != nil {
		t.Fatalf("query schema_migrations for version 2: %v", err)
	}
	if count2 != 0 {
		t.Fatalf("version 2 count = %d, want 0", count2)
	}
}

func createEmptyIntegrationDatabase(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	baseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_DATABASE_URL"))
	if baseURL == "" {
		baseURL = "postgres://localhost/ottercamp_test_template"
	}

	adminURL, err := withDBName(baseURL, "postgres")
	if err != nil {
		t.Fatalf("withDBName(admin): %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}

	dbName := fmt.Sprintf("ottercamp_migrate_%d", time.Now().UnixNano())
	createSQL := fmt.Sprintf("CREATE DATABASE %s TEMPLATE template0", pgx.Identifier{dbName}.Sanitize())
	if _, err := adminPool.Exec(ctx, createSQL); err != nil {
		adminPool.Close()
		t.Fatalf("create database: %v", err)
	}

	dbURL, err := withDBName(baseURL, dbName)
	if err != nil {
		adminPool.Close()
		t.Fatalf("withDBName(test db): %v", err)
	}

	cleanup := func() {
		_, _ = adminPool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		dropSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{dbName}.Sanitize())
		_, _ = adminPool.Exec(ctx, dropSQL)
		adminPool.Close()
	}

	return dbURL, cleanup
}

func withDBName(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
