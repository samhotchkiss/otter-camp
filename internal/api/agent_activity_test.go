package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
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
				ID:        "01HZZ000000000000000000702",
				AgentID:   "main",
				Trigger:   "cron.scheduled",
				Summary:   "Ran summary cron",
				Status:    "failed",
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

func TestActivityEventWebsocketBroadcast(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "activity-ingest-ws-org")
	otherOrgID := insertMessageTestOrganization(t, db, "activity-ingest-ws-other-org")

	hub := ws.NewHub()
	go hub.Run()

	handler := &AgentActivityHandler{DB: db, Hub: hub}

	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })

	otherClient := ws.NewClient(hub, nil)
	otherClient.SetOrgID(otherOrgID)
	hub.Register(otherClient)
	t.Cleanup(func() { hub.Unregister(otherClient) })

	time.Sleep(25 * time.Millisecond)

	startedAt := time.Date(2026, 2, 8, 17, 30, 0, 0, time.UTC)
	body, err := json.Marshal(ingestAgentActivityEventsRequest{
		OrgID: orgID,
		Events: []ingestAgentActivityRecord{
			{
				ID:         "01HZZ000000000000000000799",
				AgentID:    "main",
				SessionKey: "agent:main:main",
				Trigger:    "chat.slack",
				Channel:    "slack",
				Summary:    "Answered a Slack prompt",
				TokensUsed: 120,
				ModelUsed:  "opus-4-6",
				DurationMs: 800,
				Status:     "completed",
				StartedAt:  startedAt,
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

	select {
	case payload := <-client.Send:
		var event struct {
			Type string `json:"type"`
			Data struct {
				Event map[string]any `json:"event"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(payload, &event))
		require.Equal(t, "ActivityEventReceived", event.Type)
		require.Equal(t, "01HZZ000000000000000000799", event.Data.Event["id"])
		require.Equal(t, orgID, event.Data.Event["org_id"])
		require.Equal(t, "main", event.Data.Event["agent_id"])
		require.Equal(t, "chat.slack", event.Data.Event["trigger"])
		require.Equal(t, "Answered a Slack prompt", event.Data.Event["summary"])
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected websocket subscriber to receive activity broadcast")
	}

	select {
	case payload := <-otherClient.Send:
		t.Fatalf("did not expect cross-org websocket payload: %s", string(payload))
	case <-time.After(120 * time.Millisecond):
	}
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

func TestAgentActivityListByAgentHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "activity-list-agent-org-a")
	orgB := insertMessageTestOrganization(t, db, "activity-list-agent-org-b")

	handler := &AgentActivityHandler{DB: db}
	router := newAgentActivityTestRouter(handler)

	base := time.Date(2026, 2, 8, 18, 0, 0, 0, time.UTC)
	projectA := "11111111-1111-1111-1111-111111111111"
	projectB := "22222222-2222-2222-2222-222222222222"

	seedAgentActivityEvents(t, handler, orgA, []store.CreateAgentActivityEventInput{
		{
			ID:        "01HZZ000000000000000000901",
			AgentID:   "main",
			Trigger:   "chat.slack",
			Channel:   "slack",
			Summary:   "Slack response",
			Status:    "completed",
			StartedAt: base,
		},
		{
			ID:        "01HZZ000000000000000000902",
			AgentID:   "main",
			Trigger:   "cron.scheduled",
			Channel:   "cron",
			Summary:   "Cron failed",
			Status:    "failed",
			ProjectID: projectA,
			StartedAt: base.Add(1 * time.Minute),
		},
		{
			ID:        "01HZZ000000000000000000903",
			AgentID:   "main",
			Trigger:   "chat.telegram",
			Channel:   "telegram",
			Summary:   "Telegram response",
			Status:    "completed",
			ProjectID: projectB,
			StartedAt: base.Add(2 * time.Minute),
		},
		{
			ID:        "01HZZ000000000000000000904",
			AgentID:   "other",
			Trigger:   "heartbeat",
			Channel:   "system",
			Summary:   "Heartbeat",
			Status:    "completed",
			StartedAt: base.Add(3 * time.Minute),
		},
	})
	seedAgentActivityEvents(t, handler, orgB, []store.CreateAgentActivityEventInput{
		{
			ID:        "01HZZ000000000000000000905",
			AgentID:   "main",
			Trigger:   "chat.slack",
			Channel:   "slack",
			Summary:   "Other org event",
			Status:    "completed",
			StartedAt: base.Add(4 * time.Minute),
		},
	})

	firstReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/main/activity?org_id=%s&limit=2", orgA),
		nil,
	)
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)

	var firstResp listAgentActivityResponse
	require.NoError(t, json.NewDecoder(firstRec.Body).Decode(&firstResp))
	require.Len(t, firstResp.Items, 2)
	require.Equal(t, "01HZZ000000000000000000903", firstResp.Items[0].ID)
	require.Equal(t, "01HZZ000000000000000000902", firstResp.Items[1].ID)
	require.Equal(t, base.Add(1*time.Minute).Format(time.RFC3339), firstResp.NextBefore)

	secondReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/main/activity?org_id=%s&limit=2&before=%s", orgA, firstResp.NextBefore),
		nil,
	)
	secondRec := httptest.NewRecorder()
	router.ServeHTTP(secondRec, secondReq)
	require.Equal(t, http.StatusOK, secondRec.Code)

	var secondResp listAgentActivityResponse
	require.NoError(t, json.NewDecoder(secondRec.Body).Decode(&secondResp))
	require.Len(t, secondResp.Items, 1)
	require.Equal(t, "01HZZ000000000000000000901", secondResp.Items[0].ID)
	require.Empty(t, secondResp.NextBefore)

	filterReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/agents/main/activity?org_id=%s&trigger=cron.scheduled&channel=cron&status=failed&project_id=%s",
			orgA,
			projectA,
		),
		nil,
	)
	filterRec := httptest.NewRecorder()
	router.ServeHTTP(filterRec, filterReq)
	require.Equal(t, http.StatusOK, filterRec.Code)

	var filterResp listAgentActivityResponse
	require.NoError(t, json.NewDecoder(filterRec.Body).Decode(&filterResp))
	require.Len(t, filterResp.Items, 1)
	require.Equal(t, "01HZZ000000000000000000902", filterResp.Items[0].ID)

	missingScopeReq := httptest.NewRequest(http.MethodGet, "/api/agents/main/activity", nil)
	missingScopeRec := httptest.NewRecorder()
	router.ServeHTTP(missingScopeRec, missingScopeReq)
	require.Equal(t, http.StatusBadRequest, missingScopeRec.Code)
	require.Contains(t, missingScopeRec.Body.String(), "org_id is required")

	invalidBeforeReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/main/activity?org_id=%s&before=not-a-time", orgA),
		nil,
	)
	invalidBeforeRec := httptest.NewRecorder()
	router.ServeHTTP(invalidBeforeRec, invalidBeforeReq)
	require.Equal(t, http.StatusBadRequest, invalidBeforeRec.Code)
	require.Contains(t, invalidBeforeRec.Body.String(), "before must be RFC3339")

	invalidProjectReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/agents/main/activity?org_id=%s&project_id=bad-project", orgA),
		nil,
	)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)
	require.Contains(t, invalidProjectRec.Body.String(), "project_id must be a UUID")
}

func TestAgentActivityRecentHandler(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "activity-list-recent-org-a")
	orgB := insertMessageTestOrganization(t, db, "activity-list-recent-org-b")

	handler := &AgentActivityHandler{DB: db}
	router := newAgentActivityTestRouter(handler)

	base := time.Date(2026, 2, 8, 19, 0, 0, 0, time.UTC)
	projectA := "33333333-3333-3333-3333-333333333333"

	seedAgentActivityEvents(t, handler, orgA, []store.CreateAgentActivityEventInput{
		{
			ID:        "01HZZ000000000000000001001",
			AgentID:   "main",
			Trigger:   "chat.slack",
			Channel:   "slack",
			Summary:   "Slack response",
			Status:    "completed",
			StartedAt: base,
		},
		{
			ID:        "01HZZ000000000000000001002",
			AgentID:   "main",
			Trigger:   "cron.scheduled",
			Channel:   "cron",
			Summary:   "Cron failed",
			Status:    "failed",
			ProjectID: projectA,
			StartedAt: base.Add(1 * time.Minute),
		},
		{
			ID:        "01HZZ000000000000000001003",
			AgentID:   "main",
			Trigger:   "chat.telegram",
			Channel:   "telegram",
			Summary:   "Telegram response",
			Status:    "completed",
			StartedAt: base.Add(2 * time.Minute),
		},
		{
			ID:        "01HZZ000000000000000001004",
			AgentID:   "other",
			Trigger:   "heartbeat",
			Channel:   "system",
			Summary:   "Heartbeat",
			Status:    "completed",
			StartedAt: base.Add(3 * time.Minute),
		},
	})
	seedAgentActivityEvents(t, handler, orgB, []store.CreateAgentActivityEventInput{
		{
			ID:        "01HZZ000000000000000001005",
			AgentID:   "main",
			Trigger:   "chat.slack",
			Channel:   "slack",
			Summary:   "Other org event",
			Status:    "completed",
			StartedAt: base.Add(4 * time.Minute),
		},
	})

	firstReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/activity/recent?org_id=%s&limit=3", orgA),
		nil,
	)
	firstRec := httptest.NewRecorder()
	router.ServeHTTP(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)

	var firstResp listAgentActivityResponse
	require.NoError(t, json.NewDecoder(firstRec.Body).Decode(&firstResp))
	require.Len(t, firstResp.Items, 3)
	require.Equal(t, "01HZZ000000000000000001004", firstResp.Items[0].ID)
	require.Equal(t, "01HZZ000000000000000001003", firstResp.Items[1].ID)
	require.Equal(t, "01HZZ000000000000000001002", firstResp.Items[2].ID)
	require.Equal(t, base.Add(1*time.Minute).Format(time.RFC3339), firstResp.NextBefore)
	for _, item := range firstResp.Items {
		require.Equal(t, orgA, item.OrgID)
	}

	filterReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"/api/activity/recent?org_id=%s&agent_id=main&trigger=cron.scheduled&channel=cron&status=failed&project_id=%s",
			orgA,
			projectA,
		),
		nil,
	)
	filterRec := httptest.NewRecorder()
	router.ServeHTTP(filterRec, filterReq)
	require.Equal(t, http.StatusOK, filterRec.Code)

	var filterResp listAgentActivityResponse
	require.NoError(t, json.NewDecoder(filterRec.Body).Decode(&filterResp))
	require.Len(t, filterResp.Items, 1)
	require.Equal(t, "01HZZ000000000000000001002", filterResp.Items[0].ID)

	missingScopeReq := httptest.NewRequest(http.MethodGet, "/api/activity/recent", nil)
	missingScopeRec := httptest.NewRecorder()
	router.ServeHTTP(missingScopeRec, missingScopeReq)
	require.Equal(t, http.StatusBadRequest, missingScopeRec.Code)
	require.Contains(t, missingScopeRec.Body.String(), "org_id is required")

	invalidBeforeReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/activity/recent?org_id=%s&before=not-a-time", orgA),
		nil,
	)
	invalidBeforeRec := httptest.NewRecorder()
	router.ServeHTTP(invalidBeforeRec, invalidBeforeReq)
	require.Equal(t, http.StatusBadRequest, invalidBeforeRec.Code)
	require.Contains(t, invalidBeforeRec.Body.String(), "before must be RFC3339")

	invalidProjectReq := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/activity/recent?org_id=%s&project_id=bad-project", orgA),
		nil,
	)
	invalidProjectRec := httptest.NewRecorder()
	router.ServeHTTP(invalidProjectRec, invalidProjectReq)
	require.Equal(t, http.StatusBadRequest, invalidProjectRec.Code)
	require.Contains(t, invalidProjectRec.Body.String(), "project_id must be a UUID")
}

func TestAgentActivityRoutesRegistered(t *testing.T) {
	router := NewRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/activity/recent", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code)

	req = httptest.NewRequest(http.MethodGet, "/api/agents/main/activity", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code)
}

func seedAgentActivityEvents(
	t *testing.T,
	handler *AgentActivityHandler,
	orgID string,
	inputs []store.CreateAgentActivityEventInput,
) {
	t.Helper()

	activityStore, err := handler.resolveStore()
	require.NoError(t, err)
	require.NoError(t, activityStore.CreateEvents(testCtxWithWorkspace(orgID), inputs))
}

func newAgentActivityTestRouter(handler *AgentActivityHandler) http.Handler {
	router := chi.NewRouter()
	router.With(middleware.OptionalWorkspace).Get("/api/agents/{id}/activity", handler.ListByAgent)
	router.With(middleware.OptionalWorkspace).Get("/api/activity/recent", handler.ListRecent)
	return router
}

func intPtr(v int) *int {
	return &v
}
