package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

type adminConfigReleaseGatePayload struct {
	OK     bool `json:"ok"`
	Checks []struct {
		Category string `json:"category"`
		Status   string `json:"status"`
		Message  string `json:"message"`
	} `json:"checks"`
	GeneratedAt time.Time `json:"generated_at"`
}

func TestAdminConfigGetCurrent(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-current")

	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		"openclaw_config_snapshot",
		`{"hash":"abc123","source":"bridge","path":"/Users/sam/.openclaw/openclaw.json","captured_at":"2026-02-08T22:00:00Z","data":{"gateway":{"port":18791},"agents":[{"id":"main"}]}}`,
		now,
	)
	require.NoError(t, err)

	handler := &AdminConfigHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/config", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.GetCurrent(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Snapshot *struct {
			Hash       string          `json:"hash"`
			Source     string          `json:"source"`
			Path       string          `json:"path"`
			CapturedAt time.Time       `json:"captured_at"`
			UpdatedAt  time.Time       `json:"updated_at"`
			Data       json.RawMessage `json:"data"`
		} `json:"snapshot"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.NotNil(t, payload.Snapshot)
	require.Equal(t, "abc123", payload.Snapshot.Hash)
	require.Equal(t, "bridge", payload.Snapshot.Source)
	require.Equal(t, "/Users/sam/.openclaw/openclaw.json", payload.Snapshot.Path)
	require.WithinDuration(t, now, payload.Snapshot.UpdatedAt, time.Second)
	require.JSONEq(t, `{"gateway":{"port":18791},"agents":[{"id":"main"}]}`, string(payload.Snapshot.Data))
}

func TestAdminConfigListHistory(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-history")

	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		"openclaw_config_history",
		`[
		  {"hash":"first","source":"bridge","captured_at":"2026-02-08T20:00:00Z","data":{"gateway":{"port":18791}}},
		  {"hash":"second","source":"bridge","captured_at":"2026-02-08T21:00:00Z","data":{"gateway":{"port":18888}}}
		]`,
	)
	require.NoError(t, err)

	handler := &AdminConfigHandler{DB: db}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/config/history?limit=1", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ListHistory(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Entries []struct {
			Hash string `json:"hash"`
		} `json:"entries"`
		Total int `json:"total"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Equal(t, 2, payload.Total)
	require.Len(t, payload.Entries, 1)
	require.Equal(t, "second", payload.Entries[0].Hash)
}

func TestAdminConfigPatchValidatesPayload(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-patch-validate")
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: &fakeOpenClawConnectionStatus{connected: true},
		EventStore:      store.NewConnectionEventStore(db),
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "requires confirm",
			body: `{"confirm":false,"patch":{"agents":{"main":{"model":{"primary":"gpt-5.2-codex"}}}}}`,
		},
		{
			name: "rejects unsupported keys",
			body: `{"confirm":true,"patch":{"forbidden":{"foo":"bar"}}}`,
		},
		{
			name: "rejects non-system agent patch targets",
			body: `{"confirm":true,"patch":{"agents":{"main":{"model":{"primary":"gpt-5.2-codex"}}}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/admin/config", strings.NewReader(tc.body))
			req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
			rec := httptest.NewRecorder()
			handler.Patch(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestAdminConfigMutationHandlersRejectOversizedBody(t *testing.T) {
	handler := &AdminConfigHandler{}
	workspaceID := "00000000-0000-0000-0000-000000000001"
	oversizedBody := buildOversizedAdminConfigBody()

	tests := []struct {
		name   string
		method string
		path   string
		handle func(*AdminConfigHandler, http.ResponseWriter, *http.Request)
	}{
		{
			name:   "patch",
			method: http.MethodPatch,
			path:   "/api/admin/config",
			handle: (*AdminConfigHandler).Patch,
		},
		{
			name:   "release gate",
			method: http.MethodPost,
			path:   "/api/admin/config/release-gate",
			handle: (*AdminConfigHandler).ReleaseGate,
		},
		{
			name:   "cutover",
			method: http.MethodPost,
			path:   "/api/admin/config/cutover",
			handle: (*AdminConfigHandler).Cutover,
		},
		{
			name:   "rollback",
			method: http.MethodPost,
			path:   "/api/admin/config/rollback",
			handle: (*AdminConfigHandler).Rollback,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(oversizedBody))
			req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, workspaceID))
			rec := httptest.NewRecorder()
			tc.handle(handler, rec, req)

			require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
			var payload errorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
			require.Equal(t, "request body too large", payload.Error)
		})
	}
}

func TestAdminConfigPatchQueuesWhenBridgeUnavailable(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-patch-queued")
	dispatcher := &fakeOpenClawConnectionStatus{connected: false}
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/admin/config",
		strings.NewReader(`{"confirm":true,"patch":{"agents":{"chameleon":{"heartbeat":{"every":"15m"}}}}}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Patch(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.True(t, payload.Queued)
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)

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

func TestAdminConfigPatchDispatchesCommand(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-patch-dispatch")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/admin/config",
		strings.NewReader(`{"confirm":true,"patch":{"agents":{"chameleon":{"model":{"primary":"gpt-5.2-codex"}}}}}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Patch(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminCommandDispatchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.False(t, payload.Queued)
	require.Equal(t, adminCommandActionConfigPatch, payload.Action)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionConfigPatch, event.Data.Action)
	require.True(t, event.Data.Confirm)
	require.False(t, event.Data.DryRun)
	require.JSONEq(t, `{"agents":{"chameleon":{"model":{"primary":"gpt-5.2-codex"}}}}`, string(event.Data.ConfigPatch))
}

func TestOpenClawConfigCutoverBuildTwoAgentConfig(t *testing.T) {
	reduced, primaryAgentID, err := buildTwoAgentOpenClawConfig(json.RawMessage(`{
		"gateway":{"port":18791},
		"agents":{
			"list":[
				{"id":"main","name":"Frank","default":true,"workspace":"~/.openclaw/workspace"},
				{"id":"writer","name":"Writer","workspace":"~/.openclaw/workspace-writer"},
				{"id":"chameleon","name":"Chameleon","workspace":"~/.openclaw/workspace-chameleon"}
			]
		}
	}`))
	require.NoError(t, err)
	require.Equal(t, "main", primaryAgentID)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(reduced, &parsed))
	agentsNode, ok := parsed["agents"].(map[string]interface{})
	require.True(t, ok)
	list, ok := agentsNode["list"].([]interface{})
	require.True(t, ok)
	require.Len(t, list, 2)

	first, ok := list[0].(map[string]interface{})
	require.True(t, ok)
	second, ok := list[1].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "main", first["id"])
	require.Equal(t, "chameleon", second["id"])
}

func TestOpenClawRollbackHashValidationChecksCanonicalConfig(t *testing.T) {
	firstHash, err := hashCanonicalJSONRaw(json.RawMessage(`{
		"agents":{"main":{"enabled":true},"chameleon":{"enabled":true}},
		"gateway":{"port":18791}
	}`))
	require.NoError(t, err)

	secondHash, err := hashCanonicalJSONRaw(json.RawMessage(`{
		"gateway":{"port":18791},
		"agents":{"chameleon":{"enabled":true},"main":{"enabled":true}}
	}`))
	require.NoError(t, err)
	require.Equal(t, firstHash, secondHash)

	thirdHash, err := hashCanonicalJSONRaw(json.RawMessage(`{
		"gateway":{"port":18888},
		"agents":{"main":{"enabled":true},"chameleon":{"enabled":true}}
	}`))
	require.NoError(t, err)
	require.NotEqual(t, firstHash, thirdHash)
}

func TestAdminConfigReleaseGateFailsWithoutSnapshot(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-release-gate-no-snapshot")
	handler := &AdminConfigHandler{DB: db}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/release-gate",
		strings.NewReader(`{"confirm":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ReleaseGate(rec, req)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)

	var payload adminConfigReleaseGatePayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.OK)
	require.NotEmpty(t, payload.Checks)

	statusByCategory := map[string]string{}
	for _, check := range payload.Checks {
		statusByCategory[check.Category] = check.Status
	}
	require.Equal(t, "fail", statusByCategory["migration"])
}

func TestAdminConfigCutoverBlockedWhenReleaseGateFails(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-cutover-gate-fail")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawConfigSnapshotKey, `{
		"hash":"snapshot-hash",
		"source":"bridge",
		"captured_at":"2026-02-09T19:00:00Z",
		"data":{
			"gateway":{"port":18791},
			"agents":{"list":[
				{"id":"main","name":"Frank","default":true,"workspace":"~/.openclaw/workspace"},
				{"id":"writer","name":"Writer","workspace":"~/.openclaw/workspace-writer"}
			]}
		}
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, "openclaw_bridge_diagnostics", `{"dispatch_queue_depth":0,"errors_last_hour":0}`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/cutover",
		strings.NewReader(`{"confirm":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Cutover(rec, req)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)

	var payload struct {
		Error string                        `json:"error"`
		Gate  adminConfigReleaseGatePayload `json:"gate"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.Contains(t, strings.ToLower(payload.Error), "release gate")
	require.False(t, payload.Gate.OK)
	require.Len(t, dispatcher.calls, 0)
}

func TestAdminConfigCutoverPersistsCheckpointWhenDispatchQueued(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-cutover-dispatch-not-delivered")
	dispatcher := &fakeOpenClawConnectionStatus{connected: false}
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawConfigSnapshotKey, `{
		"source":"bridge",
		"captured_at":"2026-02-09T19:10:00Z",
		"data":{
			"gateway":{"port":18791},
			"agents":{"list":[
				{"id":"main","name":"Frank","default":true,"workspace":"~/.openclaw/workspace"},
				{"id":"writer","name":"Writer","workspace":"~/.openclaw/workspace-writer"}
			]}
		}
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawLegacyImportKey, `{
		"imported_agents":2,
		"imported_long_term_memories":1,
		"imported_daily_memories":1,
		"transition_files_generated":1,
		"skipped_workspace_count":0,
		"processed_workspace_count":2,
		"processed_retired_workspaces":1
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, "openclaw_bridge_diagnostics", `{"dispatch_queue_depth":0,"errors_last_hour":0}`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/cutover",
		strings.NewReader(`{"confirm":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.Cutover(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	require.Len(t, dispatcher.calls, 1)

	var checkpointCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM sync_metadata WHERE key = $1`,
		syncMetadataOpenClawCutoverKey,
	).Scan(&checkpointCount))
	require.Equal(t, 1, checkpointCount)
}

func TestAdminConfigReleaseGatePassesAndCutoverDispatches(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "admin-config-cutover-gate-pass")
	dispatcher := &fakeOpenClawConnectionStatus{connected: true}
	handler := &AdminConfigHandler{
		DB:              db,
		OpenClawHandler: dispatcher,
		EventStore:      store.NewConnectionEventStore(db),
	}

	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawConfigSnapshotKey, `{
		"source":"bridge",
		"captured_at":"2026-02-09T19:10:00Z",
		"data":{
			"gateway":{"port":18791},
			"agents":{"list":[
				{"id":"main","name":"Frank","default":true,"workspace":"~/.openclaw/workspace"},
				{"id":"writer","name":"Writer","workspace":"~/.openclaw/workspace-writer"}
			]}
		}
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawLegacyImportKey, `{
		"imported_agents":2,
		"imported_long_term_memories":1,
		"imported_daily_memories":1,
		"transition_files_generated":1,
		"skipped_workspace_count":0,
		"processed_workspace_count":2,
		"processed_retired_workspaces":1
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, "openclaw_bridge_diagnostics", `{"dispatch_queue_depth":0,"errors_last_hour":0}`)

	releaseReq := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/release-gate",
		strings.NewReader(`{"confirm":true}`),
	)
	releaseReq = releaseReq.WithContext(context.WithValue(releaseReq.Context(), middleware.WorkspaceIDKey, orgID))
	releaseRec := httptest.NewRecorder()
	handler.ReleaseGate(releaseRec, releaseReq)
	require.Equal(t, http.StatusOK, releaseRec.Code)

	var gatePayload adminConfigReleaseGatePayload
	require.NoError(t, json.NewDecoder(releaseRec.Body).Decode(&gatePayload))
	require.True(t, gatePayload.OK)
	require.Len(t, gatePayload.Checks, 5)
	for _, check := range gatePayload.Checks {
		require.Equal(t, "pass", check.Status, check.Category)
	}

	cutoverReq := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/cutover",
		strings.NewReader(`{"confirm":true}`),
	)
	cutoverReq = cutoverReq.WithContext(context.WithValue(cutoverReq.Context(), middleware.WorkspaceIDKey, orgID))
	cutoverRec := httptest.NewRecorder()
	handler.Cutover(cutoverRec, cutoverReq)
	require.Equal(t, http.StatusOK, cutoverRec.Code)
	require.Len(t, dispatcher.calls, 1)

	event, ok := dispatcher.calls[0].(openClawAdminCommandEvent)
	require.True(t, ok)
	require.Equal(t, adminCommandActionConfigCutover, event.Data.Action)

	var checkpointCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM sync_metadata WHERE key = $1`,
		syncMetadataOpenClawCutoverKey,
	).Scan(&checkpointCount))
	require.Equal(t, 1, checkpointCount)
}

func TestSpec110GateFailsWithoutSnapshot(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "spec110-gate-fail-no-snapshot")
	handler := &AdminConfigHandler{DB: db}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/release-gate",
		strings.NewReader(`{"confirm":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ReleaseGate(rec, req)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)

	var payload adminConfigReleaseGatePayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.False(t, payload.OK)
}

func TestSpec110GatePassesWithHealthyPrerequisites(t *testing.T) {
	db := setupMessageTestDB(t)
	orgID := insertMessageTestOrganization(t, db, "spec110-gate-pass")
	handler := &AdminConfigHandler{DB: db}

	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawConfigSnapshotKey, `{
		"source":"bridge",
		"captured_at":"2026-02-09T19:20:00Z",
		"data":{
			"gateway":{"port":18791},
			"agents":{"list":[
				{"id":"main","name":"Frank","default":true,"workspace":"~/.openclaw/workspace"},
				{"id":"writer","name":"Writer","workspace":"~/.openclaw/workspace-writer"}
			]}
		}
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, syncMetadataOpenClawLegacyImportKey, `{
		"imported_agents":2,
		"imported_long_term_memories":1,
		"imported_daily_memories":1,
		"transition_files_generated":1,
		"skipped_workspace_count":0,
		"processed_workspace_count":2,
		"processed_retired_workspaces":1
	}`)
	upsertSyncMetadataForAdminConfigGateTest(t, db, "openclaw_bridge_diagnostics", `{"dispatch_queue_depth":0,"errors_last_hour":0}`)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/admin/config/release-gate",
		strings.NewReader(`{"confirm":true}`),
	)
	req = req.WithContext(context.WithValue(req.Context(), middleware.WorkspaceIDKey, orgID))
	rec := httptest.NewRecorder()
	handler.ReleaseGate(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload adminConfigReleaseGatePayload
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
	require.True(t, payload.OK)
	require.Len(t, payload.Checks, 5)
}

func upsertSyncMetadataForAdminConfigGateTest(t *testing.T, db *sql.DB, key, value string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO sync_metadata (key, value, updated_at)
		 VALUES ($1, $2, NOW())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key,
		value,
	)
	require.NoError(t, err)
}

func buildOversizedAdminConfigBody() string {
	var body strings.Builder
	body.Grow(maxAdminConfigBodyBytes + 256)
	body.WriteString(`{"confirm":true`)
	for body.Len() <= maxAdminConfigBodyBytes+32 {
		body.WriteString(`,"confirm":true`)
	}
	body.WriteString("}")
	return body.String()
}
