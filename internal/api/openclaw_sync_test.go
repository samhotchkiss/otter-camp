package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequireOpenClawSyncAuth_NoSecretConfigured(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	status, err := requireOpenClawSyncAuth(req)
	if err == nil {
		t.Fatalf("expected error")
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, status)
	}
}

func TestRequireOpenClawSyncAuth_MissingToken(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	status, err := requireOpenClawSyncAuth(req)
	if err == nil {
		t.Fatalf("expected error")
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, status)
	}
}

func TestRequireOpenClawSyncAuth_ValidBearerToken(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	req.Header.Set("Authorization", "Bearer sync-secret")

	status, err := requireOpenClawSyncAuth(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, status)
	}
}

func TestRequireOpenClawSyncAuth_FallbackToWebhookSecret(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "webhook-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	req.Header.Set("X-OpenClaw-Token", "webhook-secret")

	status, err := requireOpenClawSyncAuth(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, status)
	}
}

func TestRequireOpenClawSyncAuth_BackwardCompatibleTokenVariable(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "legacy-sync-secret")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	req.Header.Set("X-OpenClaw-Token", "legacy-sync-secret")

	status, err := requireOpenClawSyncAuth(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, status)
	}
}

func TestOpenClawSyncHandlePersistsDiagnosticsMetadata(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	handler := &OpenClawSyncHandler{DB: db}

	payload := SyncPayload{
		Type:      "full",
		Timestamp: time.Now().UTC(),
		Source:    "bridge",
		Host: &OpenClawHostDiagnostics{
			Hostname:        "Mac-Studio",
			OS:              "Darwin 25.2.0",
			Arch:            "arm64",
			GatewayPort:     18791,
			NodeVersion:     "v25.4.0",
			MemoryUsedBytes: 48318382080,
		},
		Bridge: &OpenClawBridgeDiagnostics{
			UptimeSeconds:      123456,
			ReconnectCount:     3,
			LastSyncDurationMS: 45,
			SyncCountTotal:     8920,
		},
		CronJobs: []OpenClawCronJobDiagnostics{
			{
				ID:          "cron-1",
				Name:        "Heartbeat Sweep",
				Schedule:    "*/5 * * * *",
				LastStatus:  "success",
				Enabled:     true,
				PayloadType: "systemEvent",
			},
		},
		Processes: []OpenClawProcessDiagnostics{
			{
				ID:              "proc-1",
				Command:         "openclaw run --session agent:main:main",
				Status:          "running",
				PID:             4242,
				DurationSeconds: 88,
				AgentID:         "main",
				SessionKey:      "agent:main:main",
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var hostValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_host_diagnostics'`).Scan(&hostValue)
	require.NoError(t, err)
	require.NotEmpty(t, hostValue)
	var hostDiag OpenClawHostDiagnostics
	require.NoError(t, json.Unmarshal([]byte(hostValue), &hostDiag))
	require.Equal(t, "Mac-Studio", hostDiag.Hostname)
	require.Equal(t, 18791, hostDiag.GatewayPort)

	var bridgeValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_bridge_diagnostics'`).Scan(&bridgeValue)
	require.NoError(t, err)
	require.NotEmpty(t, bridgeValue)
	var bridgeDiag OpenClawBridgeDiagnostics
	require.NoError(t, json.Unmarshal([]byte(bridgeValue), &bridgeDiag))
	require.Equal(t, 3, bridgeDiag.ReconnectCount)
	require.Equal(t, int64(8920), bridgeDiag.SyncCountTotal)

	var cronValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_cron_jobs'`).Scan(&cronValue)
	require.NoError(t, err)
	var cronJobs []OpenClawCronJobDiagnostics
	require.NoError(t, json.Unmarshal([]byte(cronValue), &cronJobs))
	require.Len(t, cronJobs, 1)
	require.Equal(t, "cron-1", cronJobs[0].ID)
	require.Equal(t, "Heartbeat Sweep", cronJobs[0].Name)

	var processValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_processes'`).Scan(&processValue)
	require.NoError(t, err)
	var processes []OpenClawProcessDiagnostics
	require.NoError(t, json.Unmarshal([]byte(processValue), &processes))
	require.Len(t, processes, 1)
	require.Equal(t, "proc-1", processes[0].ID)
	require.Equal(t, 4242, processes[0].PID)
}

func TestOpenClawSyncHandlePersistsConfigSnapshot(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	handler := &OpenClawSyncHandler{DB: db}

	baseTime := time.Now().UTC().Truncate(time.Second)
	originalConfig := map[string]any{
		"gateway": map[string]any{
			"port": 18791,
		},
		"agents": []map[string]any{
			{
				"id": "main",
				"model": map[string]any{
					"primary": "gpt-5.2-codex",
				},
			},
		},
	}

	firstPayload := SyncPayload{
		Type:      "full",
		Timestamp: baseTime,
		Source:    "bridge",
		Config: &OpenClawConfigSnapshot{
			Path:       "/Users/sam/.openclaw/openclaw.json",
			CapturedAt: baseTime,
			Data:       originalConfig,
		},
	}
	firstBody, err := json.Marshal(firstPayload)
	require.NoError(t, err)

	firstReq := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(firstBody))
	firstReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	firstRec := httptest.NewRecorder()
	handler.Handle(firstRec, firstReq)
	require.Equal(t, http.StatusOK, firstRec.Code)

	var snapshotValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_config_snapshot'`).Scan(&snapshotValue)
	require.NoError(t, err)

	var snapshot openClawConfigSnapshotRecord
	require.NoError(t, json.Unmarshal([]byte(snapshotValue), &snapshot))
	require.Equal(t, "/Users/sam/.openclaw/openclaw.json", snapshot.Path)
	require.Equal(t, "bridge", snapshot.Source)
	require.NotZero(t, snapshot.CapturedAt)
	require.NotEmpty(t, snapshot.Hash)
	require.JSONEq(t, `{"agents":[{"id":"main","model":{"primary":"gpt-5.2-codex"}}],"gateway":{"port":18791}}`, string(snapshot.Data))

	// Duplicate snapshot should not create a second history record.
	duplicatePayload := firstPayload
	duplicatePayload.Timestamp = baseTime.Add(30 * time.Second)
	duplicatePayload.Config = &OpenClawConfigSnapshot{
		Path:       firstPayload.Config.Path,
		CapturedAt: duplicatePayload.Timestamp,
		Data:       originalConfig,
	}
	duplicateBody, err := json.Marshal(duplicatePayload)
	require.NoError(t, err)
	duplicateReq := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(duplicateBody))
	duplicateReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	duplicateRec := httptest.NewRecorder()
	handler.Handle(duplicateRec, duplicateReq)
	require.Equal(t, http.StatusOK, duplicateRec.Code)

	var historyValue string
	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_config_history'`).Scan(&historyValue)
	require.NoError(t, err)
	var history []openClawConfigSnapshotRecord
	require.NoError(t, json.Unmarshal([]byte(historyValue), &history))
	require.Len(t, history, 1)
	require.Equal(t, snapshot.Hash, history[0].Hash)

	updatedConfig := map[string]any{
		"gateway": map[string]any{
			"port": 18888,
		},
		"agents": []map[string]any{
			{
				"id": "main",
				"model": map[string]any{
					"primary": "gpt-5.2-codex",
				},
			},
			{
				"id": "2b",
				"model": map[string]any{
					"primary": "claude-opus-4-6",
				},
			},
		},
	}
	thirdPayload := SyncPayload{
		Type:      "full",
		Timestamp: baseTime.Add(1 * time.Minute),
		Source:    "bridge",
		Config: &OpenClawConfigSnapshot{
			Path:       "/Users/sam/.openclaw/openclaw.json",
			CapturedAt: baseTime.Add(1 * time.Minute),
			Data:       updatedConfig,
		},
	}
	thirdBody, err := json.Marshal(thirdPayload)
	require.NoError(t, err)
	thirdReq := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(thirdBody))
	thirdReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	thirdRec := httptest.NewRecorder()
	handler.Handle(thirdRec, thirdReq)
	require.Equal(t, http.StatusOK, thirdRec.Code)

	err = db.QueryRow(`SELECT value FROM sync_metadata WHERE key = 'openclaw_config_history'`).Scan(&historyValue)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(historyValue), &history))
	require.Len(t, history, 2)
	require.NotEqual(t, history[0].Hash, history[1].Hash)
}

func TestOpenClawSyncEmissionsPayloadIngestsIntoBuffer(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	buffer := NewEmissionBuffer(10)
	handler := &OpenClawSyncHandler{EmissionBuffer: buffer}

	now := time.Now().UTC()
	payload := SyncPayload{
		Type:      "delta",
		Timestamp: now,
		Source:    "bridge",
		Emissions: []Emission{
			{
				ID:         "sync-em-1",
				SourceType: "bridge",
				SourceID:   "bridge-main",
				Kind:       "status",
				Summary:    "Bridge heartbeat",
				Timestamp:  now,
			},
			{
				ID:         "sync-em-invalid",
				SourceType: "bridge",
				SourceID:   "",
				Kind:       "status",
				Summary:    "invalid",
				Timestamp:  now,
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	recent := buffer.Recent(10, EmissionFilter{})
	require.Len(t, recent, 1)
	require.Equal(t, "sync-em-1", recent[0].ID)
	require.Equal(t, "bridge-main", recent[0].SourceID)
}

func TestOpenClawDispatchQueuePullAndAck(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "sync-dispatch-org")

	event := map[string]any{
		"type": "project.chat.message",
		"data": map[string]any{"message_id": "msg-1"},
	}
	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"project.chat.message",
		"project.chat.message:msg-1",
		event,
	)
	require.NoError(t, err)
	require.True(t, queued)

	handler := &OpenClawSyncHandler{DB: db}

	pullReq := httptest.NewRequest(http.MethodGet, "/api/sync/openclaw/dispatch/pending?limit=5", nil)
	pullReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	pullRec := httptest.NewRecorder()
	handler.PullDispatchQueue(pullRec, pullReq)
	require.Equal(t, http.StatusOK, pullRec.Code)

	var pullResp openClawDispatchQueuePullResponse
	require.NoError(t, json.NewDecoder(pullRec.Body).Decode(&pullResp))
	require.Len(t, pullResp.Jobs, 1)
	require.NotEmpty(t, pullResp.Jobs[0].ClaimToken)

	ackBody := bytes.NewReader([]byte(`{"claim_token":"` + pullResp.Jobs[0].ClaimToken + `","success":true}`))
	ackReq := httptest.NewRequest(
		http.MethodPost,
		"/api/sync/openclaw/dispatch/"+strconv.FormatInt(pullResp.Jobs[0].ID, 10)+"/ack",
		ackBody,
	)
	ackReq = addRouteParam(ackReq, "id", strconv.FormatInt(pullResp.Jobs[0].ID, 10))
	ackReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	ackRec := httptest.NewRecorder()
	handler.AckDispatchQueue(ackRec, ackReq)
	require.Equal(t, http.StatusOK, ackRec.Code)

	pullAgainReq := httptest.NewRequest(http.MethodGet, "/api/sync/openclaw/dispatch/pending?limit=5", nil)
	pullAgainReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	pullAgainRec := httptest.NewRecorder()
	handler.PullDispatchQueue(pullAgainRec, pullAgainReq)
	require.Equal(t, http.StatusOK, pullAgainRec.Code)

	var pullAgainResp openClawDispatchQueuePullResponse
	require.NoError(t, json.NewDecoder(pullAgainRec.Body).Decode(&pullAgainResp))
	require.Empty(t, pullAgainResp.Jobs)
}

func TestAgentStatusConsistency(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	syncHandler := &OpenClawSyncHandler{DB: db}
	now := time.Now().UTC()

	payload := SyncPayload{
		Type:      "full",
		Timestamp: now,
		Source:    "bridge",
		Sessions: []OpenClawSession{
			{
				Key:           "agent:main:slack",
				Channel:       "slack",
				DisplayName:   "slack:#engineering",
				UpdatedAt:     now.Add(-20 * time.Second).UnixMilli(),
				Model:         "claude-opus-4-6",
				ContextTokens: 120,
			},
			{
				Key:           "agent:2b:slack",
				Channel:       "slack",
				DisplayName:   "slack:#engineering",
				UpdatedAt:     now.Add(-35 * time.Minute).UnixMilli(),
				Model:         "claude-opus-4-6",
				ContextTokens: 120,
			},
			{
				Key:           "agent:three-stones:webchat",
				Channel:       "webchat",
				DisplayName:   "webchat:g-agent-three-stones-main",
				UpdatedAt:     now.Add(-3 * time.Hour).UnixMilli(),
				Model:         "claude-opus-4-6",
				ContextTokens: 170000,
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	rec := httptest.NewRecorder()
	syncHandler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	syncListReq := httptest.NewRequest(http.MethodGet, "/api/sync/agents", nil)
	syncListRec := httptest.NewRecorder()
	syncHandler.GetAgents(syncListRec, syncListReq)
	require.Equal(t, http.StatusOK, syncListRec.Code)

	var syncList struct {
		Agents []AgentState `json:"agents"`
	}
	require.NoError(t, json.NewDecoder(syncListRec.Body).Decode(&syncList))
	require.NotEmpty(t, syncList.Agents)

	statusByAgent := make(map[string]string)
	for _, agent := range syncList.Agents {
		statusByAgent[agent.ID] = agent.Status
	}
	require.Equal(t, "online", statusByAgent["main"])
	require.Equal(t, "offline", statusByAgent["three-stones"])

	adminHandler := &AdminConnectionsHandler{DB: db}
	adminReq := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
	adminRec := httptest.NewRecorder()
	adminHandler.Get(adminRec, adminReq)
	require.Equal(t, http.StatusOK, adminRec.Code)

	var adminResp adminConnectionsResponse
	require.NoError(t, json.NewDecoder(adminRec.Body).Decode(&adminResp))
	require.NotEmpty(t, adminResp.Sessions)

	adminStatusByAgent := make(map[string]string)
	for _, session := range adminResp.Sessions {
		adminStatusByAgent[session.ID] = session.Status
	}
	require.Equal(t, statusByAgent["main"], adminStatusByAgent["main"])
	require.Equal(t, statusByAgent["three-stones"], adminStatusByAgent["three-stones"])
}
