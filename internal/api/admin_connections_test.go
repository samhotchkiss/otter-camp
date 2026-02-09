package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

type fakeOpenClawConnectionStatus struct {
	connected bool
	sendErr   error
	calls     []interface{}
}

func (f fakeOpenClawConnectionStatus) IsConnected() bool {
	return f.connected
}

func (f *fakeOpenClawConnectionStatus) SendToOpenClaw(event interface{}) error {
	f.calls = append(f.calls, event)
	if f.sendErr != nil {
		return f.sendErr
	}
	if !f.connected {
		return ws.ErrOpenClawNotConnected
	}
	return nil
}

func TestAdminConnectionsGetReturnsDiagnosticsAndSessionSummary(t *testing.T) {
	db := setupMessageTestDB(t)
	now := time.Now().UTC()

	_, err := db.Exec(
		`INSERT INTO agent_sync_state
			(id, name, status, model, context_tokens, total_tokens, channel, session_key, last_seen, updated_at)
		 VALUES
		    ('main', 'Frank', 'online', 'claude-opus-4-6', 160000, 400000, 'slack', 'agent:main:slack', 'just now', $1),
		    ('2b', 'Derek', 'busy', 'gpt-5.2-codex', 40000, 120000, 'slack', 'agent:2b:slack', '2m ago', $2),
		    ('three-stones', 'Stone', 'offline', 'claude-opus-4-6', 25000, 90000, 'slack', 'agent:three-stones:slack', '3h ago', $3)`,
		now,
		now.Add(-10*time.Minute),
		now.Add(-3*time.Hour),
	)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES
		    ('last_sync', $1, NOW()),
		    ('openclaw_host_diagnostics', $2, NOW()),
		    ('openclaw_bridge_diagnostics', $3, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		now.Format(time.RFC3339),
		`{"hostname":"Mac-Studio","gateway_port":18791,"node_version":"v25.4.0"}`,
		`{"reconnect_count":2,"uptime_seconds":4567}`,
	)
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Bridge.Connected)
	require.NotNil(t, payload.Bridge.LastSync)
	require.True(t, payload.Bridge.SyncHealthy)
	require.NotNil(t, payload.Bridge.Diagnostics)
	require.Equal(t, 2, payload.Bridge.Diagnostics.ReconnectCount)
	require.NotNil(t, payload.Host)
	require.Equal(t, "Mac-Studio", payload.Host.Hostname)
	require.Equal(t, 18791, payload.Host.GatewayPort)
	require.Len(t, payload.Sessions, 3)
	require.Equal(t, 3, payload.Summary.Total)
	require.Equal(t, 1, payload.Summary.Online)
	require.Equal(t, 1, payload.Summary.Busy)
	require.Equal(t, 1, payload.Summary.Offline)
	require.Equal(t, 2, payload.Summary.Stalled)
}

func TestAdminConnectionsGetHandlesMissingDiagnosticsMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: false},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.Bridge.Connected)
	require.Nil(t, payload.Bridge.LastSync)
	require.False(t, payload.Bridge.SyncHealthy)
	require.Nil(t, payload.Bridge.Diagnostics)
	require.Nil(t, payload.Host)
	require.Empty(t, payload.Sessions)
	require.Equal(t, 0, payload.Summary.Total)
}

func TestAdminConnectionsGetUsesFreshLastSyncAsConnectedSignal(t *testing.T) {
	prevLastSync := memoryLastSync
	memoryLastSync = time.Now().UTC().Add(-8 * time.Second)
	defer func() {
		memoryLastSync = prevLastSync
	}()

	handler := &AdminConnectionsHandler{
		OpenClawHandler: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Bridge.Connected)
	require.Equal(t, "healthy", payload.Bridge.Status)
	require.True(t, payload.Bridge.SyncHealthy)
	require.NotNil(t, payload.Bridge.LastSyncAgeSeconds)
	require.LessOrEqual(t, *payload.Bridge.LastSyncAgeSeconds, int64(10))
}

func TestAdminConnectionsGetMarksBridgeDisconnectedWhenLastSyncIsStale(t *testing.T) {
	prevLastSync := memoryLastSync
	memoryLastSync = time.Now().UTC().Add(-10 * time.Minute)
	defer func() {
		memoryLastSync = prevLastSync
	}()

	handler := &AdminConnectionsHandler{
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: false},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.Bridge.Connected)
	require.Equal(t, "unhealthy", payload.Bridge.Status)
	require.False(t, payload.Bridge.SyncHealthy)
}

func TestAdminConnectionsHandlerBridgeHealthChecks(t *testing.T) {
	tests := []struct {
		name               string
		lastSyncAge        time.Duration
		openClawKnown      bool
		openClawConnected  bool
		expectStatus       string
		expectConnected    bool
		expectSyncHealthy  bool
		expectAgeAtMostSec int64
	}{
		{
			name:               "healthy within ten seconds",
			lastSyncAge:        8 * time.Second,
			expectStatus:       "healthy",
			expectConnected:    true,
			expectSyncHealthy:  true,
			expectAgeAtMostSec: 10,
		},
		{
			name:               "degraded between ten and thirty seconds",
			lastSyncAge:        18 * time.Second,
			expectStatus:       "degraded",
			expectConnected:    true,
			expectSyncHealthy:  false,
			expectAgeAtMostSec: 30,
		},
		{
			name:               "unhealthy beyond thirty seconds",
			lastSyncAge:        45 * time.Second,
			expectStatus:       "unhealthy",
			expectConnected:    false,
			expectSyncHealthy:  false,
			expectAgeAtMostSec: 60,
		},
		{
			name:               "websocket disconnected forces unhealthy",
			lastSyncAge:        4 * time.Second,
			openClawKnown:      true,
			openClawConnected:  false,
			expectStatus:       "unhealthy",
			expectConnected:    false,
			expectSyncHealthy:  false,
			expectAgeAtMostSec: 10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prevLastSync := memoryLastSync
			memoryLastSync = time.Now().UTC().Add(-tc.lastSyncAge)
			defer func() {
				memoryLastSync = prevLastSync
			}()

			var openClawHandler openClawConnectionStatus
			if tc.openClawKnown {
				openClawHandler = &fakeOpenClawConnectionStatus{connected: tc.openClawConnected}
			}
			handler := &AdminConnectionsHandler{OpenClawHandler: openClawHandler}

			req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
			rec := httptest.NewRecorder()
			handler.Get(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			var payload adminConnectionsResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, tc.expectStatus, payload.Bridge.Status)
			require.Equal(t, tc.expectConnected, payload.Bridge.Connected)
			require.Equal(t, tc.expectSyncHealthy, payload.Bridge.SyncHealthy)
			require.NotNil(t, payload.Bridge.LastSyncAgeSeconds)
			require.LessOrEqual(t, *payload.Bridge.LastSyncAgeSeconds, tc.expectAgeAtMostSec)
		})
	}
}

func TestDeriveSessionChannel(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		session string
		expect  string
	}{
		{
			name:    "preserves explicit channel",
			channel: "slack:#engineering",
			session: "agent:main:slack:channel:C123",
			expect:  "slack:#engineering",
		},
		{
			name:    "derives slack from session key",
			session: "agent:main:slack:channel:C123",
			expect:  "slack",
		},
		{
			name:    "derives webchat from session key",
			session: "agent:three-stones:webchat:g-agent-three-stones-main",
			expect:  "webchat",
		},
		{
			name:    "returns empty for unknown pattern",
			session: "agent:main:main",
			expect:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveSessionChannel(tc.channel, tc.session)
			require.Equal(t, tc.expect, got)
		})
	}
}

func TestAdminConnectionsGetUsesDerivedChannelForMemorySessions(t *testing.T) {
	prevStates := memoryAgentStates
	memoryAgentStates = map[string]*AgentState{
		"main": {
			ID:            "main",
			Name:          "Frank",
			Status:        "online",
			ContextTokens: 0,
			Channel:       "",
			SessionKey:    "agent:main:slack:channel:C123456",
			UpdatedAt:     time.Now().UTC(),
		},
	}
	defer func() {
		memoryAgentStates = prevStates
	}()

	handler := &AdminConnectionsHandler{
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: true},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	rec := httptest.NewRecorder()
	handler.Get(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Sessions, 1)
	require.Equal(t, "slack", payload.Sessions[0].Channel)
}

func TestAdminConnectionsGetEventsReturnsWorkspaceScopedRows(t *testing.T) {
	db := setupMessageTestDB(t)
	orgA := insertMessageTestOrganization(t, db, "conn-events-org-a")
	orgB := insertMessageTestOrganization(t, db, "conn-events-org-b")

	eventStore := store.NewConnectionEventStore(db)
	_, err := eventStore.Create(testCtxWithWorkspace(orgA), store.CreateConnectionEventInput{
		EventType: "bridge.connected",
		Message:   "org A connected",
	})
	require.NoError(t, err)
	_, err = eventStore.Create(testCtxWithWorkspace(orgB), store.CreateConnectionEventInput{
		EventType: "bridge.connected",
		Message:   "org B connected",
	})
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{
		DB:         db,
		EventStore: eventStore,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/events?limit=10", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgA))
	rec := httptest.NewRecorder()
	handler.GetEvents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConnectionEventsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Events, 1)
	require.Equal(t, "org A connected", payload.Events[0].Message)
}

func TestAdminConnectionsRestartGatewayDispatchesCommand(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-cmd-dispatch-org")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	eventStore := store.NewConnectionEventStore(db)

	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      eventStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/gateway/restart", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.RestartGateway(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.False(t, payload.Queued)
	require.Equal(t, adminCommandActionGatewayRestart, payload.Action)
	require.Len(t, dispatcher.calls, 1)

	dispatched, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionGatewayRestart, dispatched.Data.Action)

	var dispatchedEvents int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM connection_events WHERE org_id = $1 AND event_type = 'admin.command.dispatched'`,
		orgID,
	).Scan(&dispatchedEvents)
	require.NoError(t, err)
	require.Equal(t, 1, dispatchedEvents)
}

func TestAdminConnectionsPingAgentQueuesWhenBridgeOffline(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-cmd-queue-org")
	dispatcher := &fakeOpenClawConnectionStatus{connected: false}
	eventStore := store.NewConnectionEventStore(db)

	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      eventStore,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/agents/main/ping", nil)
	req = addRouteParam(req, "id", "main")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.PingAgent(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.True(t, payload.Queued)
	require.Equal(t, adminCommandActionAgentPing, payload.Action)

	var queuedCount int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM openclaw_dispatch_queue
		 WHERE org_id = $1
		   AND event_type = 'admin.command'
		   AND status = 'pending'`,
		orgID,
	).Scan(&queuedCount)
	require.NoError(t, err)
	require.Equal(t, 1, queuedCount)
}

func TestAdminConnectionsRunDiagnosticsReturnsChecks(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-diagnostics-org")

	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES
		    ('last_sync', $1, NOW()),
		    ('openclaw_host_diagnostics', $2, NOW()),
		    ('openclaw_bridge_diagnostics', $3, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		time.Now().UTC().Format(time.RFC3339),
		`{"memory_total_bytes":1000,"memory_used_bytes":920,"disk_free_bytes":536870912000}`,
		`{"dispatch_queue_depth":3}`,
	)
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: false},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/diagnostics", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.RunDiagnostics(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminDiagnosticsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.NotEmpty(t, payload.Checks)

	checkByKey := map[string]adminDiagnosticCheck{}
	for _, check := range payload.Checks {
		checkByKey[check.Key] = check
	}
	require.Equal(t, "fail", checkByKey["bridge.connection"].Status)
	require.Equal(t, "warn", checkByKey["dispatch.queue_depth"].Status)
	require.Equal(t, "fail", checkByKey["host.memory"].Status)
}

func TestAdminConnectionsGetLogsRedactsSensitiveTokens(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-logs-redact-org")
	eventStore := store.NewConnectionEventStore(db)

	_, err := eventStore.Create(testCtxWithWorkspace(orgID), store.CreateConnectionEventInput{
		EventType: "sync.failed",
		Severity:  store.ConnectionEventSeverityError,
		Message:   "failed with token oc_git_secret123 and Bearer abc.def.ghi",
		Metadata:  json.RawMessage(`{"token":"oc_git_secret123"}`),
	})
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{
		DB:         db,
		EventStore: eventStore,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/logs?limit=10", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetLogs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminLogsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Items, 1)
	require.NotContains(t, payload.Items[0].Message, "oc_git_secret123")
	require.NotContains(t, payload.Items[0].Message, "Bearer abc.def.ghi")
	require.Contains(t, payload.Items[0].Message, "[REDACTED]")
}

func TestAdminConnectionsGetLogsRejectsInvalidLimit(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-logs-invalid-limit-org")
	handler := &AdminConnectionsHandler{
		DB:         db,
		EventStore: store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/logs?limit=abc", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetLogs(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "limit must be a positive integer", payload.Error)
}

func TestAdminConnectionsGetCronJobsReturnsMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-cron-list-org")

	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES
		   ('openclaw_cron_jobs', $1, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		`[
			{"id":"job-2","name":"Nightly Sync","schedule":"0 3 * * *","enabled":false},
			{"id":"job-1","name":"Hourly Heartbeat","schedule":"0 * * * *","enabled":true}
		]`,
	)
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/cron/jobs", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetCronJobs(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCronJobsResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Items, 2)
	require.Equal(t, 2, payload.Total)
	require.Equal(t, "job-1", payload.Items[0].ID)
	require.Equal(t, "Hourly Heartbeat", payload.Items[0].Name)
}

func TestAdminConnectionsRunCronJobDispatchesCommand(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-cron-run-org")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/cron/jobs/job-1/run", nil)
	req = addRouteParam(req, "id", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.RunCronJob(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, dispatcher.calls, 1)
	dispatched, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionCronRun, dispatched.Data.Action)
	require.Equal(t, "job-1", dispatched.Data.JobID)
}

func TestAdminConnectionsToggleCronJobQueuesWhenBridgeOffline(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-cron-toggle-org")
	dispatcher := &fakeOpenClawConnectionStatus{connected: false}
	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/admin/cron/jobs/job-1", strings.NewReader(`{"enabled":false}`))
	req = addRouteParam(req, "id", "job-1")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ToggleCronJob(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.Queued)
	require.Equal(t, adminCommandActionCronDisable, payload.Action)
}

func TestAdminConnectionsGetProcessesReturnsMetadata(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-process-list-org")

	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES
		   ('openclaw_processes', $1, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		`[
			{"id":"proc-2","status":"sleeping","pid":9002},
			{"id":"proc-1","status":"running","pid":9001}
		]`,
	)
	require.NoError(t, err)

	handler := &AdminConnectionsHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/processes", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetProcesses(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminProcessesResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Len(t, payload.Items, 2)
	require.Equal(t, "proc-1", payload.Items[0].ID)
	require.Equal(t, 9001, payload.Items[0].PID)
}

func TestAdminConnectionsKillProcessDispatchesCommand(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "conn-process-kill-org")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminConnectionsHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/admin/processes/proc-1/kill", nil)
	req = addRouteParam(req, "id", "proc-1")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.KillProcess(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, dispatcher.calls, 1)
	dispatched, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionProcessKill, dispatched.Data.Action)
	require.Equal(t, "proc-1", dispatched.Data.ProcessID)
}
