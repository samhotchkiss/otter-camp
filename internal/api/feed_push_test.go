package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func resetFeedPushDatabase(t *testing.T, connStr string) {
	t.Helper()
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

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

func insertTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO organizations (name, slug, tier) VALUES ($1, $2, 'free') RETURNING id",
		"Test Org "+slug,
		slug,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertTestAgent(t *testing.T, db *sql.DB, orgID, name, apiKey string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status, api_key) VALUES ($1, $2, $3, 'active', $4) RETURNING id",
		orgID,
		strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		name,
		apiKey,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertTestTask(t *testing.T, db *sql.DB, orgID, title string) string {
	t.Helper()
	var id string
	err := db.QueryRow(
		"INSERT INTO tasks (org_id, title, status, priority, context) VALUES ($1, $2, 'queued', 'P2', '{}') RETURNING id",
		orgID,
		title,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// TestAgentPushInsight verifies that pushed insights appear in the feed.
func TestAgentPushInsight(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "push-org")
	apiKey := "test-api-key-123"
	agentID := insertTestAgent(t, db, orgID, "Test Agent", apiKey)
	taskID := insertTestTask(t, db, orgID, "Test Task")

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	// Push an insight
	reqBody := FeedPushRequest{
		OrgID: orgID,
		Items: []FeedPushItem{
			{
				TaskID:   &taskID,
				Type:     "insight",
				Metadata: json.RawMessage(`{"message":"Test insight"}`),
			},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp FeedPushResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.OK)
	require.Equal(t, 1, resp.Inserted)
	require.Len(t, resp.IDs, 1)

	// Verify it's in the database
	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND agent_id = $2 AND action = 'insight'",
		orgID,
		agentID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Verify metadata is stored correctly
	var metadata json.RawMessage
	err = db.QueryRow(
		"SELECT metadata FROM activity_log WHERE id = $1",
		resp.IDs[0],
	).Scan(&metadata)
	require.NoError(t, err)
	require.Contains(t, string(metadata), "Test insight")
}

// TestAgentPushValidation verifies that invalid input is rejected.
func TestAgentPushValidation(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "push-validate-org")
	apiKey := "test-api-key-validate"
	_ = insertTestAgent(t, db, orgID, "Validate Agent", apiKey)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	testCases := []struct {
		name       string
		body       interface{}
		wantStatus int
		wantError  string
	}{
		{
			name: "invalid mode",
			body: FeedPushRequest{
				OrgID: orgID,
				Mode:  "bad",
				Items: []FeedPushItem{{Type: "insight"}},
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid mode",
		},
		{
			name:       "empty items",
			body:       FeedPushRequest{OrgID: orgID, Items: []FeedPushItem{}},
			wantStatus: http.StatusBadRequest,
			wantError:  "no items provided",
		},
		{
			name: "missing type",
			body: FeedPushRequest{
				OrgID: orgID,
				Items: []FeedPushItem{{Type: ""}},
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "missing type",
		},
		{
			name: "invalid task_id",
			body: FeedPushRequest{
				OrgID: orgID,
				Items: []FeedPushItem{{Type: "insight", TaskID: strPtr("not-a-uuid")}},
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid task_id",
		},
		{
			name:       "invalid JSON",
			body:       "not json",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid request body",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			if s, ok := tc.body.(string); ok {
				body = []byte(s)
			} else {
				body, _ = json.Marshal(tc.body)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
			req.Header.Set("X-API-Key", apiKey)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.Handle(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			require.Contains(t, rec.Body.String(), tc.wantError)
		})
	}
}

// TestAgentPushAuth verifies that only valid agents can push.
func TestAgentPushAuth(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "push-auth-org")
	validKey := "valid-api-key"
	_ = insertTestAgent(t, db, orgID, "Auth Agent", validKey)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	reqBody := FeedPushRequest{
		OrgID: orgID,
		Items: []FeedPushItem{{Type: "test"}},
	}
	body, _ := json.Marshal(reqBody)

	testCases := []struct {
		name       string
		apiKey     string
		authHeader string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing auth",
			wantStatus: http.StatusUnauthorized,
			wantError:  "missing authentication",
		},
		{
			name:       "invalid API key",
			apiKey:     "invalid-key",
			wantStatus: http.StatusUnauthorized,
			wantError:  "invalid API key",
		},
		{
			name:       "valid API key",
			apiKey:     validKey,
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid Bearer token",
			authHeader: "Bearer " + validKey,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
			if tc.apiKey != "" {
				req.Header.Set("X-API-Key", tc.apiKey)
			}
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.Handle(rec, req)

			require.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantError != "" {
				require.Contains(t, rec.Body.String(), tc.wantError)
			}
		})
	}
}

// TestAgentPushRateLimit verifies that rate limiting works per agent.
func TestAgentPushRateLimit(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "push-rate-org")
	apiKey := "rate-limit-key"
	_ = insertTestAgent(t, db, orgID, "Rate Agent", apiKey)

	hub := ws.NewHub()
	go hub.Run()

	// Create handler with strict rate limit for testing
	rateLimiter := NewAgentRateLimiter(time.Minute, 3) // 3 requests per minute
	handler := &FeedPushHandler{
		Hub:         hub,
		RateLimiter: rateLimiter,
	}

	reqBody := FeedPushRequest{
		OrgID: orgID,
		Items: []FeedPushItem{{Type: "test"}},
	}
	body, _ := json.Marshal(reqBody)

	// First 3 requests should succeed
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.Handle(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "request %d should succeed", i+1)
	}

	// 4th request should be rate limited
	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "rate limit exceeded")
}

// TestAgentPushOrgMismatch verifies that agents can only push to their own org.
func TestAgentPushOrgMismatch(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID1 := insertTestOrganization(t, db, "org-1")
	orgID2 := insertTestOrganization(t, db, "org-2")
	apiKey := "mismatch-key"
	_ = insertTestAgent(t, db, orgID1, "Mismatch Agent", apiKey)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	// Try to push to wrong org
	reqBody := FeedPushRequest{
		OrgID: orgID2, // Different org
		Items: []FeedPushItem{{Type: "test"}},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "org_id mismatch")
}

// TestAgentPushBatch verifies batch insertion works correctly.
func TestAgentPushBatch(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "batch-org")
	apiKey := "batch-key"
	agentID := insertTestAgent(t, db, orgID, "Batch Agent", apiKey)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	// Push multiple items
	reqBody := FeedPushRequest{
		OrgID: orgID,
		Items: []FeedPushItem{
			{Type: "commit", Metadata: json.RawMessage(`{"sha":"abc123"}`)},
			{Type: "comment", Metadata: json.RawMessage(`{"body":"hello"}`)},
			{Type: "status_change", Metadata: json.RawMessage(`{"from":"queued","to":"done"}`)},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp FeedPushResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.OK)
	require.Equal(t, 3, resp.Inserted)
	require.Len(t, resp.IDs, 3)

	// Verify all items in database
	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND agent_id = $2",
		orgID,
		agentID,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 3, count)
}

// TestAgentPushReplaceMode verifies replace mode clears prior agent push items.
func TestAgentPushReplaceMode(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "replace-org")
	apiKey := "replace-key"
	agentID := insertTestAgent(t, db, orgID, "Replace Agent", apiKey)

	// Insert a non-agent-push activity to ensure it is retained.
	_, err = db.Exec(
		"INSERT INTO activity_log (org_id, agent_id, action, metadata) VALUES ($1, $2, $3, $4)",
		orgID,
		agentID,
		"webhook_event",
		json.RawMessage(`{"source":"webhook"}`),
	)
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	// First push (augment)
	firstBody, _ := json.Marshal(FeedPushRequest{
		OrgID: orgID,
		Mode:  "augment",
		Items: []FeedPushItem{
			{Type: "commit"},
			{Type: "comment"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(firstBody))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	// Second push (replace)
	secondBody, _ := json.Marshal(FeedPushRequest{
		OrgID: orgID,
		Mode:  "replace",
		Items: []FeedPushItem{
			{Type: "insight"},
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(secondBody))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var agentPushCount int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND agent_id = $2 AND (metadata->>'source') = 'agent_push'",
		orgID,
		agentID,
	).Scan(&agentPushCount)
	require.NoError(t, err)
	require.Equal(t, 1, agentPushCount)

	var totalCount int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND agent_id = $2",
		orgID,
		agentID,
	).Scan(&totalCount)
	require.NoError(t, err)
	require.Equal(t, 2, totalCount)
}

// TestAgentPushPriorityFlag verifies priority flag is stored in metadata.
func TestAgentPushPriorityFlag(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedPushDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	orgID := insertTestOrganization(t, db, "priority-org")
	apiKey := "priority-key"
	_ = insertTestAgent(t, db, orgID, "Priority Agent", apiKey)

	hub := ws.NewHub()
	go hub.Run()
	handler := NewFeedPushHandler(hub)

	reqBody := FeedPushRequest{
		OrgID: orgID,
		Items: []FeedPushItem{
			{
				Type:     "insight",
				Priority: true,
			},
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/feed/push", bytes.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var metadata json.RawMessage
	err = db.QueryRow(
		"SELECT metadata FROM activity_log WHERE org_id = $1 AND action = 'insight'",
		orgID,
	).Scan(&metadata)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(metadata, &decoded))
	require.Equal(t, true, decoded["priority"])
	require.Equal(t, "agent_push", decoded["source"])
}

func strPtr(s string) *string {
	return &s
}
