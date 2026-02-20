package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/ws"
	"github.com/stretchr/testify/require"
)

func TestRequireOpenClawSyncAuth_NoSecretConfigured(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", nil)
	status, err := requireOpenClawSyncAuth(req.Context(), nil, req)
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
	status, err := requireOpenClawSyncAuth(req.Context(), nil, req)
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

	status, err := requireOpenClawSyncAuth(req.Context(), nil, req)
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

	status, err := requireOpenClawSyncAuth(req.Context(), nil, req)
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

	status, err := requireOpenClawSyncAuth(req.Context(), nil, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, status)
	}
}

func TestOpenClawSyncRejectsNonUUIDWorkspaceIDFallback(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	handler := &OpenClawSyncHandler{}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", strings.NewReader(`{"type":"heartbeat"}`))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	req.Header.Set("X-Org-ID", "not-a-uuid")
	rec := httptest.NewRecorder()

	handler.Handle(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var payload errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, "workspace id must be a UUID", payload.Error)
}

func TestOpenClawSyncHandlerHealthEndpoint(t *testing.T) {
	handler := &OpenClawSyncHandler{}

	tests := []struct {
		name              string
		lastSyncAge       time.Duration
		expectStatus      string
		expectSyncHealthy bool
		expectAgeMax      int64
	}{
		{
			name:              "healthy within ten seconds",
			lastSyncAge:       8 * time.Second,
			expectStatus:      "healthy",
			expectSyncHealthy: true,
			expectAgeMax:      10,
		},
		{
			name:              "degraded between ten and thirty seconds",
			lastSyncAge:       20 * time.Second,
			expectStatus:      "degraded",
			expectSyncHealthy: false,
			expectAgeMax:      30,
		},
		{
			name:              "unhealthy when stale beyond thirty seconds",
			lastSyncAge:       45 * time.Second,
			expectStatus:      "unhealthy",
			expectSyncHealthy: false,
			expectAgeMax:      60,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prevLastSync := memoryLastSync
			memoryLastSync = time.Now().UTC().Add(-tc.lastSyncAge)
			defer func() {
				memoryLastSync = prevLastSync
			}()

			req := httptest.NewRequest(http.MethodGet, "/api/sync/agents", nil)
			rec := httptest.NewRecorder()
			handler.GetAgents(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)

			var payload struct {
				BridgeStatus       string `json:"bridge_status"`
				SyncHealthy        bool   `json:"sync_healthy"`
				LastSyncAgeSeconds *int64 `json:"last_sync_age_seconds"`
			}
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, tc.expectStatus, payload.BridgeStatus)
			require.Equal(t, tc.expectSyncHealthy, payload.SyncHealthy)
			require.NotNil(t, payload.LastSyncAgeSeconds)
			require.LessOrEqual(t, *payload.LastSyncAgeSeconds, tc.expectAgeMax)
		})
	}

	t.Run("unhealthy when no sync timestamp is available", func(t *testing.T) {
		prevLastSync := memoryLastSync
		memoryLastSync = time.Time{}
		defer func() {
			memoryLastSync = prevLastSync
		}()

		req := httptest.NewRequest(http.MethodGet, "/api/sync/agents", nil)
		rec := httptest.NewRecorder()
		handler.GetAgents(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var payload struct {
			BridgeStatus       string `json:"bridge_status"`
			SyncHealthy        bool   `json:"sync_healthy"`
			LastSyncAgeSeconds *int64 `json:"last_sync_age_seconds"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		require.Equal(t, "unhealthy", payload.BridgeStatus)
		require.False(t, payload.SyncHealthy)
		require.Nil(t, payload.LastSyncAgeSeconds)
	})
}

func TestOpenClawSyncMemoryFallbackConcurrentReadWrite(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	memoryStateMu.Lock()
	prevAgentStates := memoryAgentStates
	prevAgentConfigs := memoryAgentConfigs
	prevLastSync := memoryLastSync
	prevHostDiag := memoryHostDiag
	prevBridgeDiag := memoryBridgeDiag
	prevCronJobs := memoryCronJobs
	prevProcesses := memoryProcesses
	prevConfig := memoryConfig
	prevConfigHistory := memoryConfigHistory
	memoryAgentStates = make(map[string]*AgentState)
	memoryAgentConfigs = make(map[string]*OpenClawAgentConfig)
	memoryLastSync = time.Time{}
	memoryHostDiag = nil
	memoryBridgeDiag = nil
	memoryCronJobs = nil
	memoryProcesses = nil
	memoryConfig = nil
	memoryConfigHistory = nil
	memoryStateMu.Unlock()
	t.Cleanup(func() {
		memoryStateMu.Lock()
		memoryAgentStates = prevAgentStates
		memoryAgentConfigs = prevAgentConfigs
		memoryLastSync = prevLastSync
		memoryHostDiag = prevHostDiag
		memoryBridgeDiag = prevBridgeDiag
		memoryCronJobs = prevCronJobs
		memoryProcesses = prevProcesses
		memoryConfig = prevConfig
		memoryConfigHistory = prevConfigHistory
		memoryStateMu.Unlock()
	})

	syncHandler := &OpenClawSyncHandler{}
	adminConnectionsHandler := &AdminConnectionsHandler{}
	adminConfigHandler := &AdminConfigHandler{}

	buildSyncRequest := func() *http.Request {
		now := time.Now().UTC()
		payload := SyncPayload{
			Type:      "full",
			Timestamp: now,
			Source:    "bridge",
			Agents: []OpenClawAgent{
				{ID: "main"},
			},
			Sessions: []OpenClawSession{
				{
					Key:           "agent:main:webchat",
					Channel:       "webchat",
					DisplayName:   "Main Agent",
					UpdatedAt:     now.UnixMilli(),
					Model:         "gpt-5.2-codex",
					ContextTokens: 100,
					TotalTokens:   120,
				},
			},
			Host: &OpenClawHostDiagnostics{
				Hostname: "test-host",
			},
			Bridge: &OpenClawBridgeDiagnostics{
				UptimeSeconds: 99,
			},
			CronJobs: []OpenClawCronJobDiagnostics{
				{ID: "cron-1", Name: "Daily run", Enabled: true},
			},
			Processes: []OpenClawProcessDiagnostics{
				{ID: "proc-1", Status: "running"},
			},
			Config: &OpenClawConfigSnapshot{
				Path:       "/tmp/openclaw.config.json",
				Source:     "bridge",
				CapturedAt: now,
				Data: map[string]interface{}{
					"agents": map[string]interface{}{
						"list": []interface{}{
							map[string]interface{}{
								"id":        "main",
								"name":      "Main",
								"workspace": "/tmp/workspace-main",
								"default":   true,
							},
						},
					},
				},
			},
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
		req.Header.Set("X-OpenClaw-Token", "sync-secret")
		req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, "550e8400-e29b-41d4-a716-446655440000"))
		return req
	}

	const iterations = 40
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(4)

		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			syncHandler.Handle(rec, buildSyncRequest())
			if rec.Code != http.StatusOK {
				t.Errorf("sync handle returned %d", rec.Code)
			}
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/sync/agents", nil)
			rec := httptest.NewRecorder()
			syncHandler.GetAgents(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("get agents returned %d", rec.Code)
			}
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil)
			rec := httptest.NewRecorder()
			adminConnectionsHandler.Get(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("admin connections returned %d", rec.Code)
			}
		}()

		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/admin/config/openclaw/current", nil)
			req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, "550e8400-e29b-41d4-a716-446655440000"))
			rec := httptest.NewRecorder()
			adminConfigHandler.GetCurrent(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("admin config current returned %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}

func TestOpenClawMigrationExtractWorkspaceDescriptors(t *testing.T) {
	config := map[string]interface{}{
		"agents": map[string]interface{}{
			"list": []interface{}{
				map[string]interface{}{
					"id":        "main",
					"name":      "Frank",
					"workspace": "/tmp/workspace-main",
					"default":   true,
				},
				map[string]interface{}{
					"id":             "writer",
					"name":           "Writer",
					"workspace_path": "/tmp/workspace-writer",
				},
			},
		},
	}

	descriptors := extractOpenClawConfigWorkspaceDescriptors(config)
	require.Len(t, descriptors, 2)
	require.Equal(t, "main", descriptors[0].ID)
	require.Equal(t, "/tmp/workspace-main", descriptors[0].Workspace)
	require.True(t, descriptors[0].IsDefault)
	require.Equal(t, "writer", descriptors[1].ID)
	require.Equal(t, "/tmp/workspace-writer", descriptors[1].Workspace)
	require.Equal(t, 0, resolvePrimaryWorkspaceDescriptorIndex(descriptors))
}

func TestLegacyTransitionEnsureFilesIdempotent(t *testing.T) {
	workspace := t.TempDir()
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	require.NoError(t, os.WriteFile(agentsPath, []byte("# AGENTS\nOriginal instructions"), 0o644))

	require.NoError(t, ensureLegacyTransitionFiles(workspace))
	customTransition := "# LEGACY_TRANSITION.md\n\nOperator notes: keep this custom section."
	require.NoError(t, os.WriteFile(filepath.Join(workspace, legacyTransitionFilename), []byte(customTransition), 0o644))
	require.NoError(t, ensureLegacyTransitionFiles(workspace))

	transitionBytes, err := os.ReadFile(filepath.Join(workspace, legacyTransitionFilename))
	require.NoError(t, err)
	require.Equal(t, customTransition, string(transitionBytes))

	agentsBytes, err := os.ReadFile(agentsPath)
	require.NoError(t, err)
	agentsBody := string(agentsBytes)
	require.Contains(t, agentsBody, "Original instructions")
	require.Equal(t, 1, strings.Count(agentsBody, legacyTransitionMarker))
}

func TestLegacyWorkspacePathValidationRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	targetWorkspace := filepath.Join(root, "workspace-target")
	require.NoError(t, os.MkdirAll(targetWorkspace, 0o755))

	symlinkWorkspace := filepath.Join(root, "workspace-link")
	if err := os.Symlink(targetWorkspace, symlinkWorkspace); err != nil {
		t.Skipf("symlink unsupported in test environment: %v", err)
	}

	traversalWorkspace := root + string(os.PathSeparator) + ".." + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "workspace-escape"

	_, err := loadLegacyWorkspaceFiles(traversalWorkspace)
	require.ErrorContains(t, err, "must not contain '..' traversal segments")
	err = ensureLegacyTransitionFiles(traversalWorkspace)
	require.ErrorContains(t, err, "must not contain '..' traversal segments")

	_, err = loadLegacyWorkspaceFiles(symlinkWorkspace)
	require.ErrorContains(t, err, "must not be a symlink")
	err = ensureLegacyTransitionFiles(symlinkWorkspace)
	require.ErrorContains(t, err, "must not be a symlink")
}

func TestEnsureLegacyTransitionFilesAtomicWritePreservesAgentsOnFailure(t *testing.T) {
	workspace := t.TempDir()
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	originalAgents := "# AGENTS\nExisting instructions"
	require.NoError(t, os.WriteFile(agentsPath, []byte(originalAgents), 0o644))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(workspace, legacyTransitionFilename),
			[]byte("# LEGACY_TRANSITION.md\n\nExisting transition notes"),
			0o644,
		),
	)

	originalRename := renameFileForAtomicWrite
	renameFileForAtomicWrite = func(oldPath string, newPath string) error {
		if filepath.Base(newPath) == "AGENTS.md" {
			return errors.New("rename failed")
		}
		return originalRename(oldPath, newPath)
	}
	t.Cleanup(func() {
		renameFileForAtomicWrite = originalRename
	})

	err := ensureLegacyTransitionFiles(workspace)
	require.ErrorContains(t, err, "rename failed")

	agentsBytes, readErr := os.ReadFile(agentsPath)
	require.NoError(t, readErr)
	require.Equal(t, originalAgents, string(agentsBytes))
}

func TestOpenClawMigrationCommitFailureDoesNotGenerateTransitionFiles(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-commit-failure")

	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspace-main")
	legacyWorkspace := filepath.Join(root, "workspace-legacy")
	require.NoError(t, os.MkdirAll(mainWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(legacyWorkspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "AGENTS.md"), []byte("# AGENTS\nLegacy instructions"), 0o644))

	originalCommit := commitLegacyWorkspaceTx
	commitLegacyWorkspaceTx = func(tx *sql.Tx) error {
		return errors.New("forced commit failure")
	}
	t.Cleanup(func() {
		commitLegacyWorkspaceTx = originalCommit
	})

	configData := map[string]any{
		"agents": map[string]any{
			"list": []map[string]any{
				{"id": "main", "name": "Frank", "workspace": mainWorkspace, "default": true},
				{"id": "writer", "name": "Writer", "workspace": legacyWorkspace},
			},
		},
	}

	report, err := importLegacyOpenClawWorkspaces(context.Background(), db, orgID, configData)
	require.Error(t, err)
	require.Nil(t, report)

	_, statErr := os.Stat(filepath.Join(legacyWorkspace, legacyTransitionFilename))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
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

func TestOpenClawMigrationImportsLegacyWorkspacesIdempotent(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	handler := &OpenClawSyncHandler{DB: db}
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-idempotent")

	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspace-main")
	legacyWorkspace := filepath.Join(root, "workspace-2b")
	require.NoError(t, os.MkdirAll(filepath.Join(mainWorkspace, "memory"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyWorkspace, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "SOUL.md"), []byte("# SOUL\nLegacy systems thinker"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "IDENTITY.md"), []byte("# IDENTITY\n- Name: Derek"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "TOOLS.md"), []byte("# TOOLS\n- rg\n- git"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "AGENTS.md"), []byte("# AGENTS\nOriginal legacy instructions"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "MEMORY.md"), []byte("# MEMORY\nLong-term memory"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "memory", "2026-02-08.md"), []byte("# 2026-02-08\n- Shipped migration"), 0o644))

	buildRequest := func() *http.Request {
		payload := SyncPayload{
			Type:      "full",
			Timestamp: time.Now().UTC(),
			Source:    "bridge",
			Config: &OpenClawConfigSnapshot{
				Path: "/Users/sam/.openclaw/openclaw.json",
				Data: map[string]any{
					"agents": map[string]any{
						"list": []map[string]any{
							{"id": "main", "name": "Frank", "workspace": mainWorkspace, "default": true},
							{"id": "2b", "name": "Derek", "workspace": legacyWorkspace},
						},
					},
				},
			},
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
		req.Header.Set("X-OpenClaw-Token", "sync-secret")
		return req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	}

	rec1 := httptest.NewRecorder()
	handler.Handle(rec1, buildRequest())
	require.Equal(t, http.StatusOK, rec1.Code)

	rec2 := httptest.NewRecorder()
	handler.Handle(rec2, buildRequest())
	require.Equal(t, http.StatusOK, rec2.Code)

	var (
		agentID      string
		soulMD       string
		identityMD   string
		instructions string
	)
	err := db.QueryRow(
		`SELECT id::text, COALESCE(soul_md, ''), COALESCE(identity_md, ''), COALESCE(instructions_md, '')
		 FROM agents
		 WHERE org_id = $1 AND slug = '2b'`,
		orgID,
	).Scan(&agentID, &soulMD, &identityMD, &instructions)
	require.NoError(t, err)
	require.Contains(t, soulMD, "Legacy systems thinker")
	require.Contains(t, identityMD, "Name: Derek")
	require.Contains(t, instructions, "Original legacy instructions")
	require.Contains(t, instructions, "# TOOLS")

	var memoryCount int
	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM agent_memories
		 WHERE org_id = $1 AND agent_id = $2`,
		orgID,
		agentID,
	).Scan(&memoryCount)
	require.NoError(t, err)
	require.Equal(t, 2, memoryCount)

	_, err = os.Stat(filepath.Join(legacyWorkspace, "MEMORY.md"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(legacyWorkspace, "memory", "2026-02-08.md"))
	require.NoError(t, err)
}

func TestLegacyTransitionFileGenerationPrependsAgentsPointerIdempotent(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	handler := &OpenClawSyncHandler{DB: db}
	orgID := insertMessageTestOrganization(t, db, "legacy-transition-generation")

	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspace-main")
	legacyWorkspace := filepath.Join(root, "workspace-legacy")
	require.NoError(t, os.MkdirAll(mainWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(legacyWorkspace, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyWorkspace, "AGENTS.md"), []byte("# AGENTS\nExisting legacy instructions"), 0o644))

	payload := SyncPayload{
		Type:      "full",
		Timestamp: time.Now().UTC(),
		Source:    "bridge",
		Config: &OpenClawConfigSnapshot{
			Path: "/Users/sam/.openclaw/openclaw.json",
			Data: map[string]any{
				"agents": map[string]any{
					"list": []map[string]any{
						{"id": "main", "name": "Frank", "workspace": mainWorkspace, "default": true},
						{"id": "writer", "name": "Writer", "workspace": legacyWorkspace},
					},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	runSync := func() {
		req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
		req.Header.Set("X-OpenClaw-Token", "sync-secret")
		req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
		rec := httptest.NewRecorder()
		handler.Handle(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	runSync()
	runSync()

	transitionBytes, err := os.ReadFile(filepath.Join(legacyWorkspace, "LEGACY_TRANSITION.md"))
	require.NoError(t, err)
	transition := string(transitionBytes)
	require.Contains(t, transition, "OtterCamp + Chameleon is now the active execution path")
	require.Contains(t, transition, "create one before writing deliverables")
	require.Contains(t, transition, "Do not keep final work product in the legacy workspace")

	agentsBytes, err := os.ReadFile(filepath.Join(legacyWorkspace, "AGENTS.md"))
	require.NoError(t, err)
	agentsBody := string(agentsBytes)
	require.Contains(t, agentsBody, "LEGACY_TRANSITION.md")
	require.Contains(t, agentsBody, "Existing legacy instructions")
	require.Equal(t, 1, strings.Count(agentsBody, "OtterCamp Legacy Transition"))

	_, err = os.Stat(filepath.Join(mainWorkspace, "LEGACY_TRANSITION.md"))
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func TestOpenClawMigrationPartialImportRecoveryContinuesOnWorkspaceErrors(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	handler := &OpenClawSyncHandler{DB: db}
	orgID := insertMessageTestOrganization(t, db, "openclaw-migration-partial-recovery")

	root := t.TempDir()
	mainWorkspace := filepath.Join(root, "workspace-main")
	validWorkspace := filepath.Join(root, "workspace-valid")
	missingWorkspace := filepath.Join(root, "workspace-missing")
	require.NoError(t, os.MkdirAll(mainWorkspace, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(validWorkspace, "memory"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(validWorkspace, "SOUL.md"), []byte("# SOUL\nRecovered workspace"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(validWorkspace, "AGENTS.md"), []byte("# AGENTS\nRecovered instructions"), 0o644))

	payload := SyncPayload{
		Type:      "full",
		Timestamp: time.Now().UTC(),
		Source:    "bridge",
		Config: &OpenClawConfigSnapshot{
			Path: "/Users/sam/.openclaw/openclaw.json",
			Data: map[string]any{
				"agents": map[string]any{
					"list": []map[string]any{
						{"id": "main", "name": "Frank", "workspace": mainWorkspace, "default": true},
						{"id": "missing-agent", "name": "Missing", "workspace": missingWorkspace},
						{"id": "valid-agent", "name": "Valid", "workspace": validWorkspace},
					},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var validCount int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM agents WHERE org_id = $1 AND slug = 'valid-agent'`,
		orgID,
	).Scan(&validCount)
	require.NoError(t, err)
	require.Equal(t, 1, validCount)
}

func TestOpenClawSyncEmissionsPayloadIngestsIntoBuffer(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	buffer := NewEmissionBuffer(10)
	handler := &OpenClawSyncHandler{EmissionBuffer: buffer}
	orgID := "550e8400-e29b-41d4-a716-446655440000"

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
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	recent := buffer.Recent(10, EmissionFilter{})
	require.Len(t, recent, 1)
	require.Equal(t, "sync-em-1", recent[0].ID)
	require.Equal(t, "bridge-main", recent[0].SourceID)
	require.Equal(t, orgID, recent[0].OrgID)
}

func TestOpenClawSyncEmissionsOrgIsolation(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	resetProgressLogEmissionSeen()
	t.Cleanup(resetProgressLogEmissionSeen)

	buffer := NewEmissionBuffer(10)
	handler := &OpenClawSyncHandler{EmissionBuffer: buffer}

	orgID := "550e8400-e29b-41d4-a716-446655440000"
	otherOrgID := "660e8400-e29b-41d4-a716-446655440000"
	now := time.Now().UTC()
	payload := SyncPayload{
		Type:      "delta",
		Timestamp: now,
		Source:    "bridge",
		Emissions: []Emission{
			{
				ID:         "sync-org-1",
				SourceType: "bridge",
				SourceID:   "bridge-main",
				Kind:       "status",
				Summary:    "Bridge heartbeat",
				Timestamp:  now,
			},
		},
		ProgressLogLines: []string{
			"- [2026-02-08 12:30 MST] Issue #405 | Commit abc1234 | in_progress | Completed 1/2 checklist items | Tests: go test ./internal/api -run TestOpenClawSyncEmissionsOrgIsolation -count=1",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	orgRecent := buffer.Recent(10, EmissionFilter{OrgID: orgID})
	require.Len(t, orgRecent, 2)
	for _, emission := range orgRecent {
		require.Equal(t, orgID, emission.OrgID)
	}

	otherRecent := buffer.Recent(10, EmissionFilter{OrgID: otherOrgID})
	require.Len(t, otherRecent, 0)
}

func TestOpenClawSyncEmissionsBroadcastsToWebSocket(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	resetProgressLogEmissionSeen()
	t.Cleanup(resetProgressLogEmissionSeen)

	buffer := NewEmissionBuffer(10)
	broadcaster := &fakeEmissionBroadcaster{}
	handler := &OpenClawSyncHandler{
		EmissionBuffer:      buffer,
		EmissionBroadcaster: broadcaster,
	}

	orgID := "550e8400-e29b-41d4-a716-446655440000"
	projectID := "project-live"
	issueID := "issue-live"
	now := time.Now().UTC()
	payload := SyncPayload{
		Type:      "delta",
		Timestamp: now,
		Source:    "bridge",
		Emissions: []Emission{
			{
				ID:         "sync-ws-1",
				SourceType: "bridge",
				SourceID:   "bridge-main",
				Kind:       "status",
				Summary:    "Bridge heartbeat",
				Timestamp:  now,
				Scope: &EmissionScope{
					ProjectID: &projectID,
					IssueID:   &issueID,
				},
			},
		},
		ProgressLogLines: []string{
			"- [2026-02-08 12:30 MST] Issue #406 | Commit abc1234 | in_progress | Completed 1/2 checklist items | Tests: go test ./internal/api -run TestOpenClawSyncEmissionsBroadcastsToWebSocket -count=1",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, broadcaster.orgBroadcasts, 2)
	for _, actualOrgID := range broadcaster.orgBroadcasts {
		require.Equal(t, orgID, actualOrgID)
	}
	require.Contains(t, broadcaster.topicBroadcasts, orgID+":project:"+projectID)
	require.Contains(t, broadcaster.topicBroadcasts, orgID+":issue:"+issueID)
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

func TestOpenClawDispatchQueueAckBroadcastsDMDeliveryDelivered(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "sync-dispatch-dm-delivered-org")

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"dm.message",
		"dm.message:msg-delivered",
		map[string]any{
			"type":   "dm.message",
			"org_id": orgID,
			"data": map[string]any{
				"message_id": "msg-delivered",
				"thread_id":  "dm_main",
			},
		},
	)
	require.NoError(t, err)
	require.True(t, queued)

	hub := ws.NewHub()
	go hub.Run()
	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })
	time.Sleep(20 * time.Millisecond)

	handler := &OpenClawSyncHandler{DB: db, Hub: hub}

	pullReq := httptest.NewRequest(http.MethodGet, "/api/sync/openclaw/dispatch/pending?limit=5", nil)
	pullReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	pullRec := httptest.NewRecorder()
	handler.PullDispatchQueue(pullRec, pullReq)
	require.Equal(t, http.StatusOK, pullRec.Code)

	var pullResp openClawDispatchQueuePullResponse
	require.NoError(t, json.NewDecoder(pullRec.Body).Decode(&pullResp))
	require.Len(t, pullResp.Jobs, 1)

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

	select {
	case raw := <-client.Send:
		var event map[string]any
		require.NoError(t, json.Unmarshal(raw, &event))
		require.Equal(t, "DMMessageDeliveryUpdated", event["type"])

		data, ok := event["data"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "msg-delivered", data["messageId"])
		require.Equal(t, "msg-delivered", data["message_id"])
		require.Equal(t, "dm_main", data["threadId"])
		require.Equal(t, "dm_main", data["thread_id"])
		require.Equal(t, "delivered", data["deliveryStatus"])
		require.Equal(t, "delivered", data["delivery_status"])
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected DM delivery broadcast event")
	}
}

func TestOpenClawDispatchQueueAckBroadcastsDMDeliveryFailed(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "sync-dispatch-dm-failed-org")

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"dm.message",
		"dm.message:msg-failed",
		map[string]any{
			"type":   "dm.message",
			"org_id": orgID,
			"data": map[string]any{
				"message_id": "msg-failed",
				"thread_id":  "dm_main",
			},
		},
	)
	require.NoError(t, err)
	require.True(t, queued)

	_, err = db.Exec(`UPDATE openclaw_dispatch_queue SET attempts = 19 WHERE dedupe_key = 'dm.message:msg-failed'`)
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()
	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })
	time.Sleep(20 * time.Millisecond)

	handler := &OpenClawSyncHandler{DB: db, Hub: hub}

	pullReq := httptest.NewRequest(http.MethodGet, "/api/sync/openclaw/dispatch/pending?limit=5", nil)
	pullReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	pullRec := httptest.NewRecorder()
	handler.PullDispatchQueue(pullRec, pullReq)
	require.Equal(t, http.StatusOK, pullRec.Code)

	var pullResp openClawDispatchQueuePullResponse
	require.NoError(t, json.NewDecoder(pullRec.Body).Decode(&pullResp))
	require.Len(t, pullResp.Jobs, 1)
	require.Equal(t, 20, pullResp.Jobs[0].Attempts)

	ackBody := bytes.NewReader([]byte(`{"claim_token":"` + pullResp.Jobs[0].ClaimToken + `","success":false,"error":"bridge timeout"}`))
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

	select {
	case raw := <-client.Send:
		var event map[string]any
		require.NoError(t, json.Unmarshal(raw, &event))
		require.Equal(t, "DMMessageDeliveryUpdated", event["type"])

		data, ok := event["data"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "msg-failed", data["messageId"])
		require.Equal(t, "msg-failed", data["message_id"])
		require.Equal(t, "dm_main", data["threadId"])
		require.Equal(t, "dm_main", data["thread_id"])
		require.Equal(t, "failed", data["deliveryStatus"])
		require.Equal(t, "failed", data["delivery_status"])
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected DM delivery failure broadcast event")
	}

	var status string
	err = db.QueryRow(`SELECT status FROM openclaw_dispatch_queue WHERE dedupe_key = 'dm.message:msg-failed'`).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, "failed", status)
}

func TestOpenClawDispatchQueueAckSkipsBroadcastForNonDMEvents(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "sync-dispatch-nondm-org")

	queued, err := enqueueOpenClawDispatchEvent(
		context.Background(),
		db,
		orgID,
		"project.chat.message",
		"project.chat.message:msg-1",
		map[string]any{
			"type": "project.chat.message",
			"data": map[string]any{"message_id": "msg-1"},
		},
	)
	require.NoError(t, err)
	require.True(t, queued)

	hub := ws.NewHub()
	go hub.Run()
	client := ws.NewClient(hub, nil)
	client.SetOrgID(orgID)
	hub.Register(client)
	t.Cleanup(func() { hub.Unregister(client) })
	time.Sleep(20 * time.Millisecond)

	handler := &OpenClawSyncHandler{DB: db, Hub: hub}

	pullReq := httptest.NewRequest(http.MethodGet, "/api/sync/openclaw/dispatch/pending?limit=5", nil)
	pullReq.Header.Set("X-OpenClaw-Token", "sync-secret")
	pullRec := httptest.NewRecorder()
	handler.PullDispatchQueue(pullRec, pullReq)
	require.Equal(t, http.StatusOK, pullRec.Code)

	var pullResp openClawDispatchQueuePullResponse
	require.NoError(t, json.NewDecoder(pullRec.Body).Decode(&pullResp))
	require.Len(t, pullResp.Jobs, 1)

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

	select {
	case raw := <-client.Send:
		t.Fatalf("unexpected websocket event for non-DM ack: %s", string(raw))
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAgentStatusConsistency(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	db := setupMessageTestDB(t)
	syncHandler := &OpenClawSyncHandler{DB: db}
	workspaceID := "550e8400-e29b-41d4-a716-446655440001"
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
	req.Header.Set("X-Workspace-ID", workspaceID)
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
	adminReq = adminReq.WithContext(context.WithValue(adminReq.Context(), middleware.WorkspaceIDKey, workspaceID))
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

func TestOpenClawSyncProgressLogEmissions(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	resetProgressLogEmissionSeen()
	t.Cleanup(resetProgressLogEmissionSeen)

	buffer := NewEmissionBuffer(10)
	handler := &OpenClawSyncHandler{EmissionBuffer: buffer}
	orgID := "550e8400-e29b-41d4-a716-446655440000"

	payload := SyncPayload{
		Type:      "delta",
		Timestamp: time.Now().UTC(),
		Source:    "bridge",
		ProgressLogLines: []string{
			"- [2026-02-08 12:30 MST] Issue #318 | Commit 8fc69ae | in_progress | Completed 3/7 sub-issues | Tests: go test ./internal/api -run TestOpenClawSyncProgressLogEmissions -count=1",
			"- [2026-02-08 12:30 MST] Issue #318 | Commit 8fc69ae | in_progress | Completed 3/7 sub-issues | Tests: go test ./internal/api -run TestOpenClawSyncProgressLogEmissions -count=1",
			"   ",
			"- [bad timestamp] no structured issue payload",
			"## [2026-02-08 12:31 MST] Completed Spec008 issue #318 (Phase2B progress-log watcher emissions)",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/sync/openclaw", bytes.NewReader(body))
	req.Header.Set("X-OpenClaw-Token", "sync-secret")
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Handle(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	recent := buffer.Recent(10, EmissionFilter{})
	require.Len(t, recent, 3)

	require.Equal(t, "codex", recent[0].SourceType)
	require.Equal(t, "codex-progress-log", recent[0].SourceID)
	require.Equal(t, "milestone", recent[0].Kind)
	require.Contains(t, recent[0].Summary, "Completed Spec008 issue #318")
	require.NotNil(t, recent[0].Scope)
	require.NotNil(t, recent[0].Scope.IssueNumber)
	require.EqualValues(t, 318, *recent[0].Scope.IssueNumber)

	var progressEmission *Emission
	for idx := range recent {
		if recent[idx].Kind == "progress" {
			progressEmission = &recent[idx]
			break
		}
	}
	require.NotNil(t, progressEmission)
	require.NotNil(t, progressEmission.Progress)
	require.Equal(t, 3, progressEmission.Progress.Current)
	require.Equal(t, 7, progressEmission.Progress.Total)
	require.Equal(t, orgID, progressEmission.OrgID)
}

func TestProgressLogEmissionSeenCleanup(t *testing.T) {
	resetProgressLogEmissionSeen()
	t.Cleanup(resetProgressLogEmissionSeen)

	// Phase 1: crossing 4k with stale entries should evict >24h entries.
	oldBase := time.Now().Add(-26 * time.Hour)
	progressLogEmissionMu.Lock()
	for i := 0; i < 4000; i++ {
		progressLogEmissionSeen[fmt.Sprintf("old-%04d", i)] = oldBase.Add(time.Duration(i) * time.Second)
	}
	progressLogEmissionMu.Unlock()

	require.True(t, markProgressLogEmissionSeen("fresh-after-old-cleanup"))

	progressLogEmissionMu.Lock()
	_, hasOld := progressLogEmissionSeen["old-0000"]
	_, hasFresh := progressLogEmissionSeen["fresh-after-old-cleanup"]
	sizeAfterOldCleanup := len(progressLogEmissionSeen)
	progressLogEmissionMu.Unlock()

	require.False(t, hasOld)
	require.True(t, hasFresh)
	require.Equal(t, 1, sizeAfterOldCleanup)

	// Phase 2: all entries are recent; hard cap must still evict oldest quarter.
	resetProgressLogEmissionSeen()
	recentBase := time.Now().Add(-2 * time.Hour)
	progressLogEmissionMu.Lock()
	for i := 0; i < 8000; i++ {
		progressLogEmissionSeen[fmt.Sprintf("recent-%04d", i)] = recentBase.Add(time.Duration(i) * time.Second)
	}
	progressLogEmissionMu.Unlock()

	require.True(t, markProgressLogEmissionSeen("recent-hard-cap-trigger"))

	progressLogEmissionMu.Lock()
	_, hasOldest := progressLogEmissionSeen["recent-0000"]
	_, hasQuarterBoundary := progressLogEmissionSeen["recent-1999"]
	_, hasAfterBoundary := progressLogEmissionSeen["recent-2000"]
	_, hasRecentTrigger := progressLogEmissionSeen["recent-hard-cap-trigger"]
	sizeAfterHardCap := len(progressLogEmissionSeen)
	progressLogEmissionMu.Unlock()

	require.False(t, hasOldest)
	require.False(t, hasQuarterBoundary)
	require.True(t, hasAfterBoundary)
	require.True(t, hasRecentTrigger)
	require.Equal(t, 6001, sizeAfterHardCap)
}
