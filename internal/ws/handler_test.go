package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowedSubscriptionOrgID(t *testing.T) {
	validUUID := "550e8400-e29b-41d4-a716-446655440000"
	if !isAllowedSubscriptionOrgID(validUUID) {
		t.Fatalf("expected UUID org id to be allowed")
	}
	if !isAllowedSubscriptionOrgID("demo") {
		t.Fatalf("expected demo org id to be allowed")
	}
	if !isAllowedSubscriptionOrgID("default") {
		t.Fatalf("expected default org id to be allowed")
	}
	if isAllowedSubscriptionOrgID("not-a-uuid") {
		t.Fatalf("expected invalid org id to be rejected")
	}
}

func TestIsWebSocketOriginAllowed_NoOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected empty origin to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_SameOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "http://api.otter.camp")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected same-origin websocket to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_CrossOriginDeniedByDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "https://evil.example")

	if isWebSocketOriginAllowed(req) {
		t.Fatalf("expected cross-origin websocket to be denied by default")
	}
}

func TestIsWebSocketOriginAllowed_AllowListOverride(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "https://app.otter.camp")

	req := httptest.NewRequest(http.MethodGet, "http://api.otter.camp/ws", nil)
	req.Host = "api.otter.camp"
	req.Header.Set("Origin", "https://app.otter.camp")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected allow-listed origin to be allowed")
	}
}

func TestIsWebSocketOriginAllowed_LoopbackAliasAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/ws", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:8080")

	if !isWebSocketOriginAllowed(req) {
		t.Fatalf("expected loopback alias origin to be allowed")
	}
}
