package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func insertFeedAgent(t *testing.T, db *sql.DB, orgID, slug, displayName string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status) VALUES ($1, $2, $3, 'active') RETURNING id",
		orgID,
		slug,
		displayName,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertFeedTask(t *testing.T, db *sql.DB, orgID, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, title, status, priority) VALUES ($1, $2, 'queued', 'P2') RETURNING id",
		orgID,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertFeedActivityWithRefs(t *testing.T, db *sql.DB, orgID string, taskID, agentID *string, action string, metadata json.RawMessage, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		orgID,
		taskID,
		agentID,
		action,
		metadata,
		createdAt,
	)
	require.NoError(t, err)
}

func TestFeedHandlerV2RequiresOrgID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/feed", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Contains(t, resp.Error, "org_id")
}

func TestFeedHandlerV2InvalidOrgID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id=not-a-uuid", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFeedHandlerV2InvalidLimit(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000000"

	for _, tc := range []string{
		"/api/feed?org_id=" + orgID + "&limit=0",
		"/api/feed?org_id=" + orgID + "&limit=-1",
		"/api/feed?org_id=" + orgID + "&limit=abc",
	} {
		req := httptest.NewRequest(http.MethodGet, tc, nil)
		rec := httptest.NewRecorder()

		FeedHandlerV2(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for %s", tc)
	}
}

func TestFeedHandlerV2InvalidOffset(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000000"

	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&offset=-1", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFeedHandlerV2InvalidDateFormat(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000000"

	for _, tc := range []string{
		"/api/feed?org_id=" + orgID + "&from=not-a-date",
		"/api/feed?org_id=" + orgID + "&to=invalid",
	} {
		req := httptest.NewRequest(http.MethodGet, tc, nil)
		rec := httptest.NewRecorder()

		FeedHandlerV2(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for %s", tc)
	}
}

func TestFeedHandlerV2MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/feed?org_id=00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestFeedHandlerV2WithRelatedEntities(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	// Create test data
	orgID := insertFeedOrganization(t, db, "feed-v2-org")
	agentID := insertFeedAgent(t, db, orgID, "frank", "Frank the Agent")
	taskID := insertFeedTask(t, db, orgID, "Build the thing")

	base := time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC)
	insertFeedActivityWithRefs(t, db, orgID, &taskID, &agentID, "task_update", json.RawMessage(`{"field":"status"}`), base)

	// Test request
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID, nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	require.Equal(t, orgID, resp.OrgID)
	require.Equal(t, 1, resp.Total)
	require.Len(t, resp.Items, 1)

	item := resp.Items[0]
	require.NotNil(t, item.TaskTitle)
	require.Equal(t, "Build the thing", *item.TaskTitle)
	require.NotNil(t, item.AgentName)
	require.Equal(t, "Frank the Agent", *item.AgentName)
	require.NotEmpty(t, item.Summary)
	require.Contains(t, item.Summary, "Frank the Agent")
	require.Contains(t, item.Summary, "Build the thing")
}

func TestFeedHandlerV2FilterByTypes(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-types-org")

	base := time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC)
	insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), base.Add(-3*time.Hour))
	insertFeedActivity(t, db, orgID, "message", json.RawMessage(`{"preview":"hello"}`), base.Add(-2*time.Hour))
	insertFeedActivity(t, db, orgID, "commit", json.RawMessage(`{"repo":"pearl"}`), base.Add(-1*time.Hour))
	insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), base)

	// Test filtering by single type
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&types=message", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Len(t, resp.Items, 1)
	require.Equal(t, "message", resp.Items[0].Type)

	// Test filtering by multiple types
	req = httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&types=task_update,message", nil)
	rec = httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 3, resp.Total)
	require.Len(t, resp.Items, 3)
}

func TestFeedHandlerV2FilterByDateRange(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-dates-org")

	insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC))
	insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC))
	insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC))

	// Test from filter
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&from=2026-02-02", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 2, resp.Total)

	// Test to filter
	req = httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&to=2026-02-04", nil)
	rec = httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 2, resp.Total)

	// Test both from and to
	req = httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&from=2026-02-02&to=2026-02-04", nil)
	rec = httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
}

func TestFeedHandlerV2Pagination(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-page-org")

	base := time.Date(2026, 2, 3, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		insertFeedActivity(t, db, orgID, "task_update", json.RawMessage(`{}`), base.Add(time.Duration(i)*time.Hour))
	}

	// First page
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&limit=3&offset=0", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 10, resp.Total)
	require.Len(t, resp.Items, 3)
	require.Equal(t, 3, resp.Limit)
	require.Equal(t, 0, resp.Offset)

	// Second page
	req = httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&limit=3&offset=3", nil)
	rec = httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 10, resp.Total)
	require.Len(t, resp.Items, 3)
	require.Equal(t, 3, resp.Offset)
}

func TestFeedHandlerV2MaxLimit(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-max-org")

	// Request with limit exceeding max
	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID+"&limit=500", nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, maxFeedLimit, resp.Limit)
}

func TestFeedHandlerV2EmptyResult(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "feed-empty-org")

	req := httptest.NewRequest(http.MethodGet, "/api/feed?org_id="+orgID, nil)
	rec := httptest.NewRecorder()

	FeedHandlerV2(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp PaginatedFeedResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 0, resp.Total)
	require.Empty(t, resp.Items)
}
