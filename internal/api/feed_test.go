package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const feedTestDatabaseURLKey = "OTTER_TEST_DATABASE_URL"

func feedTestDatabaseURL(t *testing.T) string {
	t.Helper()
	connStr := os.Getenv(feedTestDatabaseURLKey)
	if connStr == "" {
		t.Skipf("set %s to a dedicated test database", feedTestDatabaseURLKey)
	}
	return connStr
}

func feedMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	require.NoError(t, err)
	return dir
}

func resetFeedDatabase(t *testing.T, connStr string) {
	t.Helper()
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec("CREATE EXTENSION IF NOT EXISTS pgcrypto")
	require.NoError(t, err)

	m, err := migrate.New("file://"+feedMigrationsDir(t), connStr)
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
}

func insertFeedOrganization(t *testing.T, db *sql.DB, slug string) string {
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

func insertFeedActivity(t *testing.T, db *sql.DB, orgID, action string, metadata json.RawMessage, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO activity_log (org_id, action, metadata, created_at) VALUES ($1, $2, $3, $4)",
		orgID,
		action,
		metadata,
		createdAt,
	)
	require.NoError(t, err)
}

func TestFeedHandlerRequiresOrgID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	rec := httptest.NewRecorder()

	FeedHandler(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFeedHandlerInvalidLimitOffset(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000000"

	for _, tc := range []string{
		"/api/feed?org_id=" + orgID + "&limit=0",
		"/api/feed?org_id=" + orgID + "&limit=-1",
		"/api/feed?org_id=" + orgID + "&offset=-1",
	} {
		req := httptest.NewRequest(http.MethodGet, tc, nil)
		rec := httptest.NewRecorder()

		FeedHandler(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestFeedHandlerListsAndFilters(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-org")

	base := time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC)
	insertFeedActivity(t, db, orgID, "commit", json.RawMessage(`{"repo":"pearl"}`), base.Add(-2*time.Hour))
	insertFeedActivity(t, db, orgID, "comment", json.RawMessage(`{"body":"hello"}`), base.Add(-1*time.Hour))
	insertFeedActivity(t, db, orgID, "commit", json.RawMessage(`{"repo":"river"}`), base)

	{
		req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&limit=2&offset=0", nil)
		rec := httptest.NewRecorder()

		FeedHandler(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp FeedResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Items, 2)
		require.Equal(t, orgID, resp.OrgID)
		require.Equal(t, "commit", resp.Items[0].Type)
		require.Equal(t, "comment", resp.Items[1].Type)
	}

	{
		req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&type=comment", nil)
		rec := httptest.NewRecorder()

		FeedHandler(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		var resp FeedResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		require.Len(t, resp.Items, 1)
		require.Equal(t, "comment", resp.Items[0].Type)
	}
}
