package api

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCommandSearchValidation(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	for _, tc := range []struct {
		name       string
		target     string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing query",
			target:     "/api/commands/search?org_id=00000000-0000-0000-0000-000000000000",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing query parameter: q",
		},
		{
			name:       "missing org_id",
			target:     "/api/commands/search?q=agent",
			wantStatus: http.StatusBadRequest,
			wantError:  "missing query parameter: org_id",
		},
		{
			name:       "invalid org_id",
			target:     "/api/commands/search?q=agent&org_id=not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid org_id",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}

			var payload map[string]string
			if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if payload["error"] != tc.wantError {
				t.Fatalf("expected error %q, got %q", tc.wantError, payload["error"])
			}
		})
	}
}

func TestCommandSearchMethodNotAllowed(t *testing.T) {
	t.Parallel()

	router := NewRouter()

	req := httptest.NewRequest(http.MethodPost, "/api/commands/search?q=agent&org_id=00000000-0000-0000-0000-000000000000", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestCommandSearchReturnsRankedResults(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	orgID := insertFeedOrganization(t, db, "command-search-org")
	now := time.Date(2026, 2, 4, 12, 0, 0, 0, time.UTC)

	var agentID string
	require.NoError(t, db.QueryRow(
		"INSERT INTO agents (org_id, slug, display_name, status, updated_at) VALUES ($1, $2, $3, 'active', $4) RETURNING id",
		orgID,
		"scout",
		"Scout Agent",
		now.Add(-2*time.Hour),
	).Scan(&agentID))

	var taskID string
	require.NoError(t, db.QueryRow(
		"INSERT INTO tasks (org_id, title, description, status, priority, updated_at) VALUES ($1, $2, $3, 'queued', 'P2', $4) RETURNING id",
		orgID,
		"Agent onboarding checklist",
		"Prepare Scout Agent for launch",
		now.Add(-30*time.Minute),
	).Scan(&taskID))

	var projectID string
	require.NoError(t, db.QueryRow(
		"INSERT INTO projects (org_id, name, description, status, updated_at) VALUES ($1, $2, $3, 'active', $4) RETURNING id",
		orgID,
		"Agent Ops",
		"Operations playbook for agents",
		now.Add(-3*time.Hour),
	).Scan(&projectID))
	_ = projectID

	_, err = db.Exec(
		"INSERT INTO activity_log (org_id, task_id, agent_id, action, metadata, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		orgID,
		taskID,
		agentID,
		"Assigned agent to task",
		json.RawMessage(`{"note":"Scout assigned"}`),
		now.Add(-15*time.Minute),
	)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/commands/search?q=agent&org_id="+orgID, nil)
	rec := httptest.NewRecorder()

	CommandSearchHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommandSearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, "agent", resp.Query)
	require.Equal(t, orgID, resp.OrgID)
	require.NotEmpty(t, resp.Results)

	var hasTask, hasProject, hasAgent, hasRecent bool
	for _, item := range resp.Results {
		require.NotEmpty(t, item.Type)
		require.NotEmpty(t, item.Title)
		require.NotEmpty(t, item.Action)

		switch item.Type {
		case "task":
			hasTask = true
		case "project":
			hasProject = true
		case "agent":
			hasAgent = true
		case "recent":
			hasRecent = true
		}
	}

	require.True(t, hasTask, "expected task result")
	require.True(t, hasProject, "expected project result")
	require.True(t, hasAgent, "expected agent result")
	require.True(t, hasRecent, "expected recent result")

	prevScore := math.Inf(1)
	for _, item := range resp.Results {
		score := commandSearchScore(item, "agent")
		require.GreaterOrEqual(t, score, -1.0)
		require.LessOrEqual(t, score, prevScore+0.0001)
		prevScore = score
	}
}
