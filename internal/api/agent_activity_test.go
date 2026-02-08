package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActivityEventsIngestHandler(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "activity-ingest-org")
	handler := &AgentActivityHandler{DB: db}

	startedAt := time.Date(2026, 2, 8, 17, 0, 0, 0, time.UTC)
	body, err := json.Marshal(ingestAgentActivityEventsRequest{
		OrgID: orgID,
		Events: []ingestAgentActivityRecord{
			{
				ID:         "01HZZ000000000000000000701",
				AgentID:    "main",
				Trigger:    "chat.slack",
				Channel:    "slack",
				Summary:    "Answered a Slack prompt",
				Detail:     "Detailed response payload",
				TokensUsed: 1500,
				ModelUsed:  "opus-4-6",
				DurationMs: 4200,
				Status:     "completed",
				StartedAt:  startedAt,
			},
			{
				ID:       "01HZZ000000000000000000702",
				AgentID:  "main",
				Trigger:  "cron.scheduled",
				Summary:  "Ran summary cron",
				Status:   "failed",
				StartedAt: startedAt.Add(30 * time.Second),
				Scope: &ingestAgentActivityScope{
					IssueNumber: intPtr(42),
				},
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/activity/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	rec := httptest.NewRecorder()

	handler.IngestEvents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp ingestAgentActivityEventsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.True(t, resp.OK)
	require.Equal(t, 2, resp.Inserted)

	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM agent_activity_events WHERE org_id = $1`, orgID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	var trigger string
	err = db.QueryRow(
		`SELECT trigger FROM agent_activity_events WHERE id = $1`,
		"01HZZ000000000000000000701",
	).Scan(&trigger)
	require.NoError(t, err)
	require.Equal(t, "chat.slack", trigger)
}

func TestActivityEventsIngestHandlerValidation(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "activity-ingest-validation-org")
	handler := &AgentActivityHandler{DB: db}

	tests := []struct {
		name       string
		payload    ingestAgentActivityEventsRequest
		token      string
		wantStatus int
		wantError  string
	}{
		{
			name: "missing auth",
			payload: ingestAgentActivityEventsRequest{
				OrgID: orgID,
				Events: []ingestAgentActivityRecord{{
					ID:        "01HZZ000000000000000000801",
					AgentID:   "main",
					Trigger:   "chat.slack",
					Summary:   "No auth",
					StartedAt: time.Now().UTC(),
				}},
			},
			wantStatus: http.StatusUnauthorized,
			wantError:  "missing authentication",
		},
		{
			name: "missing summary",
			payload: ingestAgentActivityEventsRequest{
				OrgID: orgID,
				Events: []ingestAgentActivityRecord{{
					ID:        "01HZZ000000000000000000802",
					AgentID:   "main",
					Trigger:   "chat.slack",
					StartedAt: time.Now().UTC(),
				}},
			},
			token:      "sync-secret",
			wantStatus: http.StatusBadRequest,
			wantError:  "summary is required",
		},
		{
			name: "invalid status",
			payload: ingestAgentActivityEventsRequest{
				OrgID: orgID,
				Events: []ingestAgentActivityRecord{{
					ID:        "01HZZ000000000000000000803",
					AgentID:   "main",
					Trigger:   "chat.slack",
					Summary:   "Bad status",
					Status:    "unknown",
					StartedAt: time.Now().UTC(),
				}},
			},
			token:      "sync-secret",
			wantStatus: http.StatusBadRequest,
			wantError:  "status is invalid",
		},
		{
			name: "invalid org id",
			payload: ingestAgentActivityEventsRequest{
				OrgID: "not-a-uuid",
				Events: []ingestAgentActivityRecord{{
					ID:        "01HZZ000000000000000000804",
					AgentID:   "main",
					Trigger:   "chat.slack",
					Summary:   "Bad org",
					StartedAt: time.Now().UTC(),
				}},
			},
			token:      "sync-secret",
			wantStatus: http.StatusBadRequest,
			wantError:  "org_id must be a UUID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.payload)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/activity/events", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tc.token != "" {
				req.Header.Set("X-OpenClaw-Token", tc.token)
			}
			rec := httptest.NewRecorder()

			handler.IngestEvents(rec, req)
			require.Equal(t, tc.wantStatus, rec.Code)
			require.Contains(t, rec.Body.String(), tc.wantError)
		})
	}
}

func intPtr(v int) *int {
	return &v
}
