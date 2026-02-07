package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeOpenClawConnectionStatus struct {
	connected bool
}

func (f fakeOpenClawConnectionStatus) IsConnected() bool {
	return f.connected
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
		OpenClawHandler: fakeOpenClawConnectionStatus{connected: true},
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
		OpenClawHandler: fakeOpenClawConnectionStatus{connected: false},
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
