//go:build integration

package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/server"
	"github.com/samhotchkiss/otter-camp/internal/storage"
	"github.com/samhotchkiss/otter-camp/internal/testdb"
)

func TestBootstrapRunIdempotentSeedsCoreData(t *testing.T) {
	t.Setenv("OTTERCAMP_ORG_SLUG", "default")
	t.Setenv("OTTERCAMP_ORG_NAME", "OtterCamp")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "super-secret")

	pool := testdb.New(t)
	bootstrapper, store := newBootstrapperForIntegration(t, pool)

	if err := bootstrapper.Run(context.Background()); err != nil {
		t.Fatalf("first bootstrap run: %v", err)
	}
	if err := bootstrapper.Run(context.Background()); err != nil {
		t.Fatalf("second bootstrap run: %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'default'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user WHERE role = 'admin'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill WHERE created_by_type = 'system'`, 3)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_provider`, 2)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile WHERE logical_profile_id IN ('high-capability','standard','haiku') AND is_current = true`, 3)
	assertCount(t, pool, `SELECT COUNT(*) FROM model_profile_assignment`, 8)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE event_type = 'system.bootstrap'`, 1)

	listed, err := store.List(context.Background(), "skills/")
	if err != nil {
		t.Fatalf("list seeded skills in storage: %v", err)
	}
	if len(listed) < 3 {
		t.Fatalf("skills stored = %d, want at least 3", len(listed))
	}
}

func TestBootstrapSkipsAdminUserWhenEnvUnset(t *testing.T) {
	t.Setenv("OTTERCAMP_ORG_SLUG", "no-admin")
	t.Setenv("OTTERCAMP_ORG_NAME", "No Admin Org")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "")

	pool := testdb.New(t)
	bootstrapper, _ := newBootstrapperForIntegration(t, pool)

	if err := bootstrapper.Run(context.Background()); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'no-admin'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM human_user`, 0)
}

func TestTestResetEndpointResetsAndRebootstraps(t *testing.T) {
	t.Setenv("OTTERCAMP_ORG_SLUG", "reset-org")
	t.Setenv("OTTERCAMP_ORG_NAME", "Reset Org")
	t.Setenv("OTTERCAMP_ADMIN_EMAIL", "reset-admin@example.com")
	t.Setenv("OTTERCAMP_ADMIN_PASSWORD", "reset-secret")

	pool := testdb.New(t)
	bootstrapper, _ := newBootstrapperForIntegration(t, pool)
	if err := bootstrapper.Run(context.Background()); err != nil {
		t.Fatalf("bootstrap run: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `INSERT INTO organization (slug, display_name, settings) VALUES ('extra-org', 'Extra Org', '{}'::jsonb)`); err != nil {
		t.Fatalf("insert extra org: %v", err)
	}

	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version:      "test-version",
		Mode:         "test",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		TestResetter: bootstrapper,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/test/reset", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /test/reset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, http.StatusNoContent, string(body))
	}

	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'extra-org'`, 0)
	assertCount(t, pool, `SELECT COUNT(*) FROM organization WHERE slug = 'reset-org'`, 1)
	assertCount(t, pool, `SELECT COUNT(*) FROM skill`, 3)
	assertCount(t, pool, `SELECT COUNT(*) FROM audit_event WHERE event_type = 'system.bootstrap'`, 1)
}

func TestTestResetEndpointNotRegisteredInProductionMode(t *testing.T) {
	handler := server.NewHandlerWithOptions(server.HandlerOptions{
		Version: "test-version",
		Mode:    "production",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/test/reset", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d, body=%s", resp.StatusCode, http.StatusNotFound, string(body))
	}
}

func newBootstrapperForIntegration(t *testing.T, pool *pgxpool.Pool) (*Bootstrapper, storage.Store) {
	t.Helper()

	store, err := storage.New(storage.Config{Backend: storage.BackendFS, FSRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("create storage: %v", err)
	}

	bootstrapper, err := New(Options{
		Pool:    pool,
		Store:   store,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version: "test-version",
	})
	if err != nil {
		t.Fatalf("new bootstrapper: %v", err)
	}

	return bootstrapper, store
}

func assertCount(t *testing.T, pool *pgxpool.Pool, query string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if count != want {
		t.Fatalf("query %q count = %d, want %d", query, count, want)
	}
}
