package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/stretchr/testify/require"
)

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
