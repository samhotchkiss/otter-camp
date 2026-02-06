package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
