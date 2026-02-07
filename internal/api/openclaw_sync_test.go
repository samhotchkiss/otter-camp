package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

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
