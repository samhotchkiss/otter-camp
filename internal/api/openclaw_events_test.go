package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenClawEventsHandler_Compaction(t *testing.T) {
	t.Setenv("OPENCLAW_SYNC_SECRET", "sync-secret")
	t.Setenv("OPENCLAW_SYNC_TOKEN", "")
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "")

	t.Run("rejects unauthorized requests", func(t *testing.T) {
		db := setupMessageTestDB(t)
		handler := &OpenClawEventsHandler{DB: db}
		body := bytes.NewBufferString(`{"event":"session.compaction","org_id":"00000000-0000-0000-0000-000000000000","session_key":"agent:chameleon:oc:00000000-0000-0000-0000-000000000000"}`)

		req := httptest.NewRequest(http.MethodPost, "/api/openclaw/events", body)
		rec := httptest.NewRecorder()
		handler.Handle(rec, req)

		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("rejects invalid payload", func(t *testing.T) {
		db := setupMessageTestDB(t)
		handler := &OpenClawEventsHandler{DB: db}
		body := bytes.NewBufferString(`{"event":"session.compaction","org_id":"00000000-0000-0000-0000-000000000000"}`)

		req := httptest.NewRequest(http.MethodPost, "/api/openclaw/events", body)
		req.Header.Set("X-OpenClaw-Token", "sync-secret")
		rec := httptest.NewRecorder()
		handler.Handle(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("marks matching session rows compacted", func(t *testing.T) {
		db := setupMessageTestDB(t)
		orgID := insertMessageTestOrganization(t, db, "openclaw-events-org")
		agentID := insertMessageTestAgent(t, db, orgID, "openclaw-events-agent")
		threadID := "dm_" + agentID
		sessionKey := canonicalChameleonSessionKey(agentID)

		_, err := db.Exec(
			`INSERT INTO dm_injection_state (
				org_id,
				thread_id,
				session_key,
				agent_id,
				injected_at,
				injection_hash,
				compaction_detected
			) VALUES ($1, $2, $3, $4, NOW(), 'hash-v1', FALSE)`,
			orgID,
			threadID,
			sessionKey,
			agentID,
		)
		require.NoError(t, err)

		payload := map[string]interface{}{
			"event":       "session.compaction",
			"org_id":      orgID,
			"session_key": sessionKey,
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)

		handler := &OpenClawEventsHandler{DB: db}
		req := httptest.NewRequest(http.MethodPost, "/api/openclaw/events", bytes.NewReader(body))
		req.Header.Set("X-OpenClaw-Token", "sync-secret")
		rec := httptest.NewRecorder()
		handler.Handle(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var response openClawEventIngestResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.True(t, response.OK)
		require.Equal(t, 1, response.Updated)

		var compactionDetected bool
		err = db.QueryRow(
			`SELECT compaction_detected
			 FROM dm_injection_state
			 WHERE org_id = $1 AND thread_id = $2`,
			orgID,
			threadID,
		).Scan(&compactionDetected)
		require.NoError(t, err)
		require.True(t, compactionDetected)
	})
}
