package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenClawWebhookRejectsMissingSignature(t *testing.T) {
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/openclaw", bytes.NewBufferString(`{"event":"task.updated","org_id":"00000000-0000-0000-0000-000000000000","task_id":"00000000-0000-0000-0000-000000000001"}`))
	rec := httptest.NewRecorder()

	OpenClawWebhookHandler(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenClawWebhookRejectsInvalidSignature(t *testing.T) {
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "test-secret")

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/openclaw", bytes.NewBufferString(`{"event":"task.updated","org_id":"00000000-0000-0000-0000-000000000000","task_id":"00000000-0000-0000-0000-000000000001"}`))
	req.Header.Set(openClawSignatureHeader, "sha256=deadbeef")
	rec := httptest.NewRecorder()

	OpenClawWebhookHandler(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestOpenClawWebhookStoresTaskEvent(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "test-secret")

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "openclaw-task")

	taskID := "00000000-0000-0000-0000-000000000111"
	payload := map[string]interface{}{
		"event":   "task.status_changed",
		"org_id":  orgID,
		"task_id": taskID,
		"task": map[string]string{
			"id":              taskID,
			"status":          "done",
			"previous_status": "in_progress",
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/openclaw", bytes.NewBuffer(body))
	req.Header.Set(openClawSignatureHeader, signOpenClawTestPayload(body, "test-secret"))
	rec := httptest.NewRecorder()

	OpenClawWebhookHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND task_id = $2 AND action = $3",
		orgID,
		taskID,
		"task.status_changed",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestOpenClawWebhookStoresAgentEvent(t *testing.T) {
	connStr := feedTestDatabaseURL(t)
	resetFeedDatabase(t, connStr)
	t.Setenv("DATABASE_URL", connStr)
	t.Setenv("OPENCLAW_WEBHOOK_SECRET", "test-secret")

	db := openFeedDatabase(t, connStr)
	orgID := insertFeedOrganization(t, db, "openclaw-agent")

	agentID := "00000000-0000-0000-0000-000000000222"
	payload := map[string]interface{}{
		"event":    "agent.updated",
		"org_id":   orgID,
		"agent_id": agentID,
		"agent": map[string]string{
			"id":     agentID,
			"status": "active",
		},
		"at": time.Now().UTC().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/openclaw", bytes.NewBuffer(body))
	req.Header.Set(openClawSignatureHeader, signOpenClawTestPayload(body, "test-secret"))
	rec := httptest.NewRecorder()

	OpenClawWebhookHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM activity_log WHERE org_id = $1 AND agent_id = $2 AND action = $3",
		orgID,
		agentID,
		"agent.updated",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func signOpenClawTestPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func openFeedDatabase(t *testing.T, connStr string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}
