//go:build integration

package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/bootstrap"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestTestResetRouteResetsAndRebootstrapsInTestMode(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	bootstrapper := bootstrap.NewBootstrapper(bootstrap.Options{
		Pool:          pool,
		OrgSlug:       "test-reset-org",
		OrgName:       "Test Reset Org",
		AdminEmail:    "admin@example.com",
		AdminPassword: "reset-password",
		SkillsDir:     t.TempDir(),
		AppVersion:    "test-version",
	})

	if _, err := pool.Exec(ctx, `INSERT INTO organization (slug, display_name) VALUES ('noise-org', 'Noise Org')`); err != nil {
		t.Fatalf("seed noise org: %v", err)
	}

	handler := NewHandlerWithOptions(HandlerOptions{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pool:         pool,
		Mode:         "test",
		TestResetter: bootstrapper,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/test/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /test/reset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	assertIntCount(t, pool, `SELECT COUNT(*) FROM organization`, 1)
	assertIntCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'test-reset-org'`, 1)
	assertIntCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE event_type = 'system.bootstrap'`, 1)
}

func TestTestResetRouteIsNotRegisteredInProductionMode(t *testing.T) {
	pool := testdb.New(t)
	handler := NewHandlerWithOptions(HandlerOptions{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Pool:   pool,
		Mode:   "production",
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/test/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /test/reset: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func assertIntCount(t *testing.T, pool *pgxpool.Pool, sql string, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(context.Background(), sql).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d (sql=%q)", got, want, sql)
	}
}
