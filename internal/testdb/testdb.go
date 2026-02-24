package testdb

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
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/migrate"
)

var (
	templateOnce sync.Once
	templateErr  error
)

const templatePrepLockID int64 = 9_226_014_608

func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	baseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_DATABASE_URL"))
	if baseURL == "" {
		baseURL = "postgres://localhost/ottercamp_test_template"
	}

	templateName, err := dbNameFromURL(baseURL)
	if err != nil {
		t.Fatalf("parse OTTERCAMP_TEST_DATABASE_URL: %v", err)
	}

	templateOnce.Do(func() {
		templateErr = prepareTemplateDatabase(ctx, baseURL, templateName)
	})
	if templateErr != nil {
		t.Fatalf("prepare template database: %v", templateErr)
	}

	adminURL, err := withDBName(baseURL, "postgres")
	if err != nil {
		t.Fatalf("admin url: %v", err)
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	testDBName := fmt.Sprintf("ottercamp_test_%d_%s", time.Now().UnixNano(), strings.ReplaceAll(uuid.NewString(), "-", ""))
	createSQL := fmt.Sprintf(
		"CREATE DATABASE %s TEMPLATE %s",
		pgx.Identifier{testDBName}.Sanitize(),
		pgx.Identifier{templateName}.Sanitize(),
	)
	if _, err := adminPool.Exec(ctx, createSQL); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	testURL, err := withDBName(baseURL, testDBName)
	if err != nil {
		t.Fatalf("test database url: %v", err)
	}
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("test pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cleanupAdmin, cleanupErr := pgxpool.New(cleanupCtx, adminURL)
		if cleanupErr != nil {
			t.Fatalf("cleanup admin pool: %v", cleanupErr)
		}
		defer cleanupAdmin.Close()

		_, _ = cleanupAdmin.Exec(cleanupCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()
		`, testDBName)

		dropSQL := fmt.Sprintf("DROP DATABASE IF EXISTS %s", pgx.Identifier{testDBName}.Sanitize())
		if _, dropErr := cleanupAdmin.Exec(cleanupCtx, dropSQL); dropErr != nil {
			t.Fatalf("drop test database %s: %v", testDBName, dropErr)
		}
	})

	return pool
}

func prepareTemplateDatabase(ctx context.Context, baseURL, templateName string) error {
	adminURL, err := withDBName(baseURL, "postgres")
	if err != nil {
		return err
	}
	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		return err
	}
	defer adminPool.Close()

	if _, err := adminPool.Exec(ctx, `SELECT pg_advisory_lock($1)`, templatePrepLockID); err != nil {
		return err
	}
	defer func() {
		_, _ = adminPool.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, templatePrepLockID)
	}()

	var exists bool
	if err := adminPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, templateName).Scan(&exists); err != nil {
		return err
	}

	if !exists {
		createSQL := fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{templateName}.Sanitize())
		if _, err := adminPool.Exec(ctx, createSQL); err != nil {
			return err
		}
	}

	templateURL, err := withDBName(baseURL, templateName)
	if err != nil {
		return err
	}
	templatePool, err := pgxpool.New(ctx, templateURL)
	if err != nil {
		return err
	}
	defer templatePool.Close()

	runner := migrate.NewRunner(templatePool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return runner.Run(ctx)
}

func dbNameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("database name missing in %q", rawURL)
	}
	return name, nil
}

func withDBName(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
