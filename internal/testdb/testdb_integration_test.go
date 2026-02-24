//go:build integration

package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNewReturnsIsolatedDatabaseAndDropsOnCleanup(t *testing.T) {
	var dbName string
	t.Run("create clone", func(t *testing.T) {
		pool := New(t)
		var one int
		if err := pool.QueryRow(context.Background(), `SELECT 1`).Scan(&one); err != nil {
			t.Fatalf("SELECT 1 failed: %v", err)
		}
		if one != 1 {
			t.Fatalf("SELECT 1 = %d, want 1", one)
		}

		if err := pool.QueryRow(context.Background(), `SELECT current_database()`).Scan(&dbName); err != nil {
			t.Fatalf("current_database query failed: %v", err)
		}

		checkDatabaseExists(t, dbName, true)
	})

	if dbName == "" {
		t.Fatal("expected database name from subtest")
	}
	checkDatabaseExists(t, dbName, false)
}

func checkDatabaseExists(t *testing.T, dbName string, want bool) {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("OTTERCAMP_TEST_DATABASE_URL"))
	if baseURL == "" {
		baseURL = "postgres://localhost/ottercamp_test_template"
	}

	adminURL, err := withDBNameForTest(baseURL, "postgres")
	if err != nil {
		t.Fatalf("admin url: %v", err)
	}
	adminPool, err := pgxpool.New(context.Background(), adminURL)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()

	var exists bool
	if err := adminPool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		t.Fatalf("database existence query failed: %v", err)
	}

	if exists != want {
		t.Fatalf("database %q exists=%v, want %v", dbName, exists, want)
	}
}

func withDBNameForTest(rawURL, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}
